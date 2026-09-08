package core

// One-click auto-tuning: reads the model's real GGUF metrics (layers, attention
// heads, KV geometry, training context) and the local hardware snapshot
// (GPU vendor / VRAM, RAM, CPU cores), then computes optimal llama-server
// parameters for that model. The sizing core is the pure function
// tuneModelConfig — deterministic, no globals, no time — so every rule below is
// unit-testable in isolation.

import (
	"encoding/binary"
	"fmt"
	"io"
	"log"
	"math"
	"os"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
)

// GPU vendor classification used by the tuner.
const (
	vendorNvidia = "nvidia"
	vendorAMD    = "amd"
	vendorApple  = "apple"
	vendorNone   = "none"
)

// ─── GGUF metrics reader ─────────────────────────────────────────

// modelMetrics holds the architecture-relevant metadata extracted from a GGUF
// header. Missing keys stay 0; estimators apply documented fallbacks.
type modelMetrics struct {
	Arch, Name string
	// Path is the parsed GGUF file path, letting buildTuneModel run the
	// tensor-table expert/dense split over the same file.
	Path string
	// Quant is the quantization name derived from general.file_type (empty when
	// the key is absent); used by the metadata-based expert size estimator.
	Quant string
	// BlockCount is {arch}.block_count (transformer layers).
	BlockCount int
	// ContextLength is {arch}.context_length (trained context window).
	ContextLength   int
	EmbeddingLength int
	// HeadCount / HeadCountKV are {arch}.attention.head_count[_kv]; HeadCountKV
	// carries the GQA group size and falls back to HeadCount when absent.
	HeadCount   int
	HeadCountKV int
	// KeyLength / ValueLength are {arch}.attention.key_length /
	// {arch}.attention.value_length, falling back to 128 when absent.
	KeyLength, ValueLength int
	// KVLoRaRank is {arch}.attention.kv_lora_rank; >0 marks MLA architectures
	// (e.g. deepseek2) whose KV cache is one compressed latent per token.
	KVLoRaRank int
	// RopeDimCount is {arch}.rope.dimension_count (per-head rope dimensions).
	RopeDimCount int
	// FullAttentionInterval is {arch}.full_attention_interval; >1 marks hybrid
	// attention (e.g. qwen3.5) where only every Nth layer holds a KV cache.
	FullAttentionInterval int
	// ExpertCount / ExpertUsedCount / ExpertSharedCount are {arch}.expert_count
	// / expert_used_count / expert_shared_count (MoE geometry). ExpertCount <= 0
	// marks a dense model for the tuner.
	ExpertCount, ExpertUsedCount, ExpertSharedCount int
	// ExpertFFNLength is {arch}.expert_feed_forward_length (per-expert hidden
	// width) used by the expert size estimator.
	ExpertFFNLength int
	// LeadingDenseBlockCount is {arch}.leading_dense_block_count: dense prefix
	// layers before the first MoE layer (0 for pure MoE stacks).
	LeadingDenseBlockCount int
}

// ggufHeaderData carries the fixed GGUF header fields plus the scalar/string
// KVs collected while walking the metadata region; shared by
// readGGUFModelMetrics and readGGUFTensorSplit so both stay byte-compatible.
type ggufHeaderData struct {
	tensorCount uint64
	nums        map[string]int
	strs        map[string]string
}

// readGGUFHeader parses the GGUF fixed header of f (magic, version 2-3, tensor
// and KV counts — kvCount over 4096 aborts, mirroring readGGUFMeta's #7.2
// guard) and walks the whole KV region, collecting numeric (u32/u64) and
// string values. On success f is positioned exactly at the first tensor info
// record, so callers may continue straight into the tensor table.
func readGGUFHeader(f *os.File) (ggufHeaderData, error) {
	var magic uint32
	if err := binary.Read(f, binary.LittleEndian, &magic); err != nil || magic != 0x46554747 {
		return ggufHeaderData{}, fmt.Errorf("invalid GGUF magic")
	}
	var version uint32
	if err := binary.Read(f, binary.LittleEndian, &version); err != nil || version < 2 || version > 3 {
		return ggufHeaderData{}, fmt.Errorf("unsupported GGUF version %d", version)
	}
	h := ggufHeaderData{nums: make(map[string]int, 64), strs: make(map[string]string, 8)}
	if err := binary.Read(f, binary.LittleEndian, &h.tensorCount); err != nil {
		return ggufHeaderData{}, err
	}
	var kvCount uint64
	if err := binary.Read(f, binary.LittleEndian, &kvCount); err != nil {
		return ggufHeaderData{}, err
	}
	if kvCount > 4096 {
		return ggufHeaderData{}, fmt.Errorf("kv count %d over limit", kvCount)
	}
	for i := uint64(0); i < kvCount; i++ {
		key, err := readGGUFString(f)
		if err != nil {
			return ggufHeaderData{}, err
		}
		var valueType uint32
		if err := binary.Read(f, binary.LittleEndian, &valueType); err != nil {
			return ggufHeaderData{}, err
		}
		switch valueType {
		case 4: // uint32
			var v uint32
			if err := binary.Read(f, binary.LittleEndian, &v); err != nil {
				return ggufHeaderData{}, err
			}
			h.nums[key] = int(v)
		case 10: // uint64
			var v uint64
			if err := binary.Read(f, binary.LittleEndian, &v); err != nil {
				return ggufHeaderData{}, err
			}
			h.nums[key] = int(v)
		case 8: // string
			s, err := readGGUFString(f)
			if err != nil {
				return ggufHeaderData{}, err
			}
			h.strs[key] = s
		default:
			// Other types (bool/float/array/...) carry no metric we need;
			// skip them so the stream stays in sync.
			if err := skipMetricsValue(f, valueType); err != nil {
				return ggufHeaderData{}, err
			}
		}
	}
	return h, nil
}

// readGGUFModelMetrics parses the GGUF header of path and extracts the model
// metrics listed above. Validation mirrors readGGUFMeta (magic, version 2-3,
// kvCount <= 4096); numeric KVs (type 4 = u32, 10 = u64) are collected under
// their full key names and the {arch}.* lookups happen after the loop, once
// general.architecture is known. Returns false when the file is unreadable or
// not a valid GGUF.
func readGGUFModelMetrics(path string) (modelMetrics, bool) {
	f, err := os.Open(path)
	if err != nil {
		return modelMetrics{}, false
	}
	defer f.Close()

	h, err := readGGUFHeader(f)
	if err != nil {
		return modelMetrics{}, false
	}

	m := modelMetrics{
		Arch: h.strs["general.architecture"],
		Name: h.strs["general.name"],
		Path: path,
	}
	if ft, has := h.nums["general.file_type"]; has {
		m.Quant = ggufQuantName(uint32(ft))
	}
	// Architecture-prefixed lookups happen after the loop: real key names are
	// e.g. qwen35.attention.head_count, deepseek2.attention.kv_lora_rank and
	// {arch}.rope.dimension_count (verified against local qwen3.5 / deepseek2
	// GGUF files).
	get := func(name string) int { return h.nums[m.Arch+"."+name] }
	m.BlockCount = get("block_count")
	m.ContextLength = get("context_length")
	m.EmbeddingLength = get("embedding_length")
	m.HeadCount = get("attention.head_count")
	m.HeadCountKV = get("attention.head_count_kv")
	m.KeyLength = get("attention.key_length")
	m.ValueLength = get("attention.value_length")
	m.KVLoRaRank = get("attention.kv_lora_rank")
	m.RopeDimCount = get("rope.dimension_count")
	m.FullAttentionInterval = get("full_attention_interval")
	m.ExpertCount = get("expert_count")
	m.ExpertUsedCount = get("expert_used_count")
	m.ExpertSharedCount = get("expert_shared_count")
	m.ExpertFFNLength = get("expert_feed_forward_length")
	m.LeadingDenseBlockCount = get("leading_dense_block_count")
	return m, true
}

// skipMetricsValue skips one metadata value of the given GGUF type on f,
// returning an error when the value cannot be consumed. Unlike the shared
// skipGGUFValue (whose 1000-element cap is fine for readGGUFMeta's early-exit
// parsing), this variant skips complete arrays: parsing the whole header of a
// real model must step over tokenizer arrays holding hundreds of thousands of
// elements. Fixed-size elements are skipped with a single seek; crafted files
// terminate at EOF (every skipped element still consumes file bytes), so the
// walk is bounded by the file size.
func skipMetricsValue(f *os.File, valueType uint32) error {
	if size, ok := ggufFixedValueSize(valueType); ok {
		_, err := f.Seek(int64(size), io.SeekCurrent)
		return err
	}
	switch valueType {
	case 8: // string
		var length uint64
		if err := binary.Read(f, binary.LittleEndian, &length); err != nil {
			return err
		}
		// Seek instead of reading: skipped strings never enter memory; the
		// 1 GiB sanity cap rejects absurd declared lengths without overflow.
		if length > 1<<30 {
			return fmt.Errorf("string too long: %d", length)
		}
		_, err := f.Seek(int64(length), io.SeekCurrent)
		return err
	case 9: // array: element type u32 + count u64, then the elements
		var arrType uint32
		if err := binary.Read(f, binary.LittleEndian, &arrType); err != nil {
			return err
		}
		var arrLen uint64
		if err := binary.Read(f, binary.LittleEndian, &arrLen); err != nil {
			return err
		}
		// Sanity cap against crafted headers: real vocab arrays stay far
		// below 2^40 elements, and the cap keeps arrLen*elemSize inside int64.
		if arrLen > 1<<40 {
			return fmt.Errorf("array too long: %d", arrLen)
		}
		if size, ok := ggufFixedValueSize(arrType); ok {
			_, err := f.Seek(int64(arrLen)*int64(size), io.SeekCurrent)
			return err
		}
		// String or nested-array elements must be walked one by one.
		for i := uint64(0); i < arrLen; i++ {
			if err := skipMetricsValue(f, arrType); err != nil {
				return err
			}
		}
		return nil
	}
	return fmt.Errorf("unknown GGUF value type %d", valueType)
}

// ggufFixedValueSize returns the on-disk byte size of a fixed-width GGUF
// scalar type; ok=false for strings, arrays and unknown types.
func ggufFixedValueSize(valueType uint32) (int, bool) {
	switch valueType {
	case 0, 1, 7: // uint8, int8, bool
		return 1, true
	case 2, 3: // uint16, int16
		return 2, true
	case 4, 5, 6: // uint32, int32, float32
		return 4, true
	case 10, 11, 12: // uint64, int64, float64
		return 8, true
	}
	return 0, false
}

// ─── Expert / dense weight split (tensor table) ───────────────────

// ggmlBlockLayout describes one ggml quantization block: how many tensor
// elements a block packs (elems) and the block's on-disk byte size (bytes).
type ggmlBlockLayout struct {
	elems uint64
	bytes uint64
}

// ggmlBlockTable maps GGUF tensor types (ggml type ids, ggml.h) to their block
// layouts. A type absent from the table (unsupported / future) makes the whole
// tensor split unreliable — callers fall back to metadata estimation instead
// of guessing sizes.
var ggmlBlockTable = map[uint32]ggmlBlockLayout{
	0:  {1, 4},     // F32
	1:  {1, 2},     // F16
	2:  {32, 18},   // Q4_0
	3:  {32, 20},   // Q4_1
	6:  {32, 22},   // Q5_0
	7:  {32, 24},   // Q5_1
	8:  {32, 34},   // Q8_0
	9:  {32, 36},   // Q8_1
	10: {256, 84},  // Q2_K
	11: {256, 110}, // Q3_K
	12: {256, 144}, // Q4_K
	13: {256, 176}, // Q5_K
	14: {256, 210}, // Q6_K
	15: {256, 292}, // Q8_K
	16: {256, 66},  // IQ2_XXS
	17: {256, 74},  // IQ2_XS
	18: {256, 81},  // IQ3_XXS
	19: {256, 62},  // IQ1_S
	20: {32, 18},   // IQ4_NL
	21: {256, 110}, // IQ3_S
	22: {256, 88},  // IQ2_S
	23: {256, 136}, // IQ4_XS
	24: {256, 56},  // IQ1_M
	25: {1, 2},     // BF16
}

// Guards for the tensor-table walk. Real GGUF models stay far below two
// million tensors and 2^50 elements per tensor; the caps only reject corrupt
// or crafted files quickly and keep the uint64 math overflow-free.
const (
	tuneTensorCountMax = 2_000_000
	tuneTensorElemCap  = 1 << 50
	tuneTensorBytesCap = 1 << 62
	tuneTensorSplitTol = 0.05
)

// isExpertTensor classifies a GGUF tensor name as a MoE expert weight.
// Conventions in the wild: ffn_{up,gate,down}_exps.weight (llama.cpp convert
// for GLM / Qwen / DeepSeek MoE), ffn_exp* names, and ".exp" + "ffn" markers.
// Router (ffn_*_inp) and shared-expert (ffn_*_shexp) weights lack these
// markers and stay dense, matching what llama-server keeps on the GPU under
// --cpu-moe.
func isExpertTensor(name string) bool {
	if strings.Contains(name, "_exps") || strings.Contains(name, "ffn_exp") {
		return true
	}
	return strings.Contains(name, ".exp") && strings.Contains(name, "ffn")
}

// readGGUFTensorSplit walks the GGUF tensor info area (after the KV region)
// and sums the headless data size of every tensor, split into expert vs dense
// weights: per tensor, size = ceil(nelem / elemsPerBlock) * bytesPerBlock with
// the block layout from ggmlBlockTable. Each record is name(string) +
// n_dims(u32, 1..4) + dims(u64 x n_dims) + type(u32) + offset(u64). ok is
// false when the file is unreadable/invalid, the tensor count is 0 or above
// tuneTensorCountMax, n_dims falls outside 1..4, or any tensor uses a type
// missing from the block table — callers must then fall back to the metadata
// estimator instead of guessing.
func readGGUFTensorSplit(path string) (expertBytes, denseBytes int64, ok bool) {
	f, err := os.Open(path)
	if err != nil {
		return 0, 0, false
	}
	defer f.Close()

	h, err := readGGUFHeader(f)
	if err != nil || h.tensorCount == 0 || h.tensorCount > tuneTensorCountMax {
		return 0, 0, false
	}

	var expert, dense int64
	for i := uint64(0); i < h.tensorCount; i++ {
		name, err := readGGUFString(f)
		if err != nil {
			return 0, 0, false
		}
		var nDims uint32
		if err := binary.Read(f, binary.LittleEndian, &nDims); err != nil || nDims < 1 || nDims > 4 {
			return 0, 0, false
		}
		nelem := uint64(1)
		for d := uint32(0); d < nDims; d++ {
			var dim uint64
			if err := binary.Read(f, binary.LittleEndian, &dim); err != nil {
				return 0, 0, false
			}
			if dim == 0 {
				// Degenerate zero dimension: a zero-element tensor adds 0 bytes
				// but the remaining dims still advance the stream.
				nelem = 0
				continue
			}
			// Bound the running product so crafted huge dims cannot overflow.
			if nelem > tuneTensorElemCap/dim {
				return 0, 0, false
			}
			nelem *= dim
		}
		var typ uint32
		if err := binary.Read(f, binary.LittleEndian, &typ); err != nil {
			return 0, 0, false
		}
		var offset uint64 // alignment offset inside the data area; size math ignores it
		if err := binary.Read(f, binary.LittleEndian, &offset); err != nil {
			return 0, 0, false
		}
		layout, known := ggmlBlockTable[typ]
		if !known {
			return 0, 0, false
		}
		size := (nelem + layout.elems - 1) / layout.elems * layout.bytes
		if isExpertTensor(name) {
			expert += int64(size)
		} else {
			dense += int64(size)
		}
		// Cap the running sums so crafted files cannot wrap int64.
		if expert > tuneTensorBytesCap || dense > tuneTensorBytesCap {
			return 0, 0, false
		}
	}
	return expert, dense, true
}

// moeBytesPerWeight maps a quantization name to an approximate bytes-per-weight
// for MoE expert tensors. These are coarse approximations: real mixed-quant
// files blend block types (e.g. Q4_K_M mixes Q4_K with Q6_K), so the estimator
// only needs order-of-magnitude accuracy — the tensor-table split is the
// precise path.
func moeBytesPerWeight(quant string) float64 {
	switch {
	case strings.Contains(quant, "F16") || strings.Contains(quant, "BF16"):
		return 2.0
	case strings.Contains(quant, "Q8"):
		return 1.06
	case strings.Contains(quant, "Q6"):
		return 0.82
	case strings.Contains(quant, "Q5"):
		return 0.69
	case strings.Contains(quant, "Q4") || strings.Contains(quant, "IQ4"):
		return 0.60
	default:
		return 0.6
	}
}

// estimateExpertBytes approximates the expert weight bytes from MoE GGUF
// metadata when the tensor-table split is unavailable:
//
//	expertBytes ~= bpw * moeLayers * (expert_count + expert_shared_count)
//	                * 3 * expert_ffn * embedding_length
//
// where moeLayers = block_count - leading_dense_block_count (floored at 0) and
// the factor 3 covers the up/gate/down matrices every expert holds. Missing
// expert metadata (ExpertCount <= 0) or an unusable geometry returns 0 — the
// model is then treated as dense. The result is clamped to weightsBytes as a
// sanity bound (the experts cannot outweigh the whole file).
func estimateExpertBytes(m modelMetrics, weightsBytes int64) int64 {
	if m.ExpertCount <= 0 {
		return 0
	}
	moeLayers := m.BlockCount - m.LeadingDenseBlockCount
	if moeLayers <= 0 || m.ExpertFFNLength <= 0 || m.EmbeddingLength <= 0 {
		return 0
	}
	est := moeBytesPerWeight(m.Quant) *
		float64(moeLayers) * float64(m.ExpertCount+m.ExpertSharedCount) *
		3 * float64(m.ExpertFFNLength) * float64(m.EmbeddingLength)
	if est > float64(weightsBytes) {
		return weightsBytes
	}
	return int64(est)
}

// ─── KV cache estimation ─────────────────────────────────────────

// kvBytesPerTokenF16 estimates the f16 KV cache bytes one token needs on one
// standard (full-attention) layer:
//   - MLA (kv_lora_rank > 0): one compressed latent vector of
//     (kv_lora_rank + rope_dims) elements, 2 bytes each; rope dims fall back
//     to 64 when absent (typical MLA rope dimensionality);
//   - standard MHA/GQA: kv_heads * (key_len + value_len) * 2 bytes, where
//     kv_heads falls back to HeadCount (no GQA) and lens fall back to 128;
//   - no head information at all: conservative 1024 bytes (8 KV heads at the
//     default 128-dim geometry).
func kvBytesPerTokenF16(m modelMetrics) float64 {
	if m.KVLoRaRank > 0 {
		rope := m.RopeDimCount
		if rope <= 0 {
			rope = 64
		}
		return float64(m.KVLoRaRank+rope) * 2.0
	}
	kvHeads := m.HeadCountKV
	if kvHeads <= 0 {
		kvHeads = m.HeadCount
	}
	if kvHeads <= 0 {
		return 1024.0
	}
	keyLen := m.KeyLength
	if keyLen <= 0 {
		keyLen = 128
	}
	valLen := m.ValueLength
	if valLen <= 0 {
		valLen = 128
	}
	return float64(kvHeads*(keyLen+valLen)) * 2.0
}

// kvCacheLayers returns how many of the model's layers actually hold a KV
// cache: hybrid-attention models (full_attention_interval > 1, e.g. qwen3.5)
// keep KV on only every Nth layer, i.e. ceil(block_count / interval); every
// other architecture caches on all layers.
func kvCacheLayers(m modelMetrics) int {
	if m.FullAttentionInterval > 1 && m.BlockCount > 0 {
		return (m.BlockCount + m.FullAttentionInterval - 1) / m.FullAttentionInterval
	}
	return m.BlockCount
}

// ─── Tuning inputs ────────────────────────────────────────────────

// tuneHardware is the hardware snapshot the tuner plans against.
type tuneHardware struct {
	GPUVendor     string // "nvidia" | "amd" | "apple" | "none"
	VRAMMB        int    // MemoryMB of the largest-VRAM GPU; 0 when no GPU or unified memory (apple)
	RAMTotalGB    float64
	RAMFreeGB     float64
	PhysicalCores int // CPUInfo.Cores
	LogicalCPUs   int
	// PerfCores is CPUInfo.PerfCores: the performance-cluster core count on
	// big.LITTLE SoCs (0 = unknown). Feeds the Android thread cap in
	// tuneModelConfig.
	PerfCores int
	// AndroidPlatform mirrors runtime.GOOS == "android". Kept as an explicit
	// input (instead of reading the GOOS inside tuneModelConfig) so the
	// sizing core stays free of globals and the cap stays unit-testable on
	// desktop dev machines.
	AndroidPlatform bool
	// WindowsPlatform mirrors runtime.GOOS == "windows". Explicit input like
	// AndroidPlatform: the full-offload plan pins --threads 2 and disables
	// context checkpoints there (measured memory-bus contention, unsloth
	// #5692), and the rule must stay unit-testable on every dev machine.
	WindowsPlatform bool
	// CPUModel is CPUInfo.Model, feeding the RAM-bandwidth cache
	// fingerprint: the CPU package is the most identifying component of the
	// memory subsystem (see hardwareFingerprint).
	CPUModel string
	// RAMBandwidthGBs is the measured all-core memory read bandwidth in
	// decimal GB/s from the benchbw calibration; 0 = unknown (no measurement
	// yet or the benchmark failed), which keeps every tuner rule on its
	// static behavior. Gates the cramped-full-offload → cpu-moe flip in
	// tuneModelConfig.
	RAMBandwidthGBs float64
}

// tuneModel is the model-side input derived from the GGUF metrics and the
// on-disk weight size.
type tuneModel struct {
	// WeightsBytes is the main GGUF size (plus the mmproj projector for
	// multimodal models).
	WeightsBytes int64
	// Layers is the raw block count: per-layer weight math (partial offload)
	// must divide the weights by the real block count, never by the
	// KV-bearing subset.
	Layers int
	// KVBytesPerTokPerLayerF16 is the f16 KV bytes one token needs per layer,
	// already folded for hybrid attention (see buildTuneModel): multiplying it
	// by Layers yields the true per-token KV total.
	KVBytesPerTokPerLayerF16 float64
	// TrainCtx is the trained context window; <=0 is treated as 32768.
	TrainCtx int
	// ExpertBytes / DenseBytes split WeightsBytes into MoE expert weights and
	// everything else. ExpertBytes > 0 marks a MoE model eligible for the
	// --cpu-moe plan (experts in system RAM, dense weights + KV on the GPU).
	ExpertBytes int64
	DenseBytes  int64
	// ExpertUsedFrac is expert_used_count / expert_count (tokens activate only
	// this fraction of experts); 0 when the metadata is absent. Informational
	// for now — the plan keeps all experts on one side.
	ExpertUsedFrac float64
}

// buildTuneModel converts parsed GGUF metrics into the tuneModel inputs.
//
// KV folding consistency: tuneModelConfig computes the per-token KV total as
// KVBytesPerTokPerLayerF16 * Layers and the partial-offload per-layer KV as
// KVBytesPerTokPerLayerF16 * ctx. To keep both correct on hybrid-attention
// models (where only kvCacheLayers = ceil(block_count/interval) layers hold
// KV), Layers stays the raw BlockCount and the per-layer KV cost is scaled by
// kvCacheLayers / BlockCount — so per-layer * Layers == per-layer(true) *
// kvCacheLayers == per-token total, and the per-layer weight estimate W/L
// still uses the true block count.
//
// Expert/dense split chain: the exact tensor-table walk (readGGUFTensorSplit
// via m.Path, re-reading the same file's header) is preferred and accepted
// only when its sums stay within tuneTensorSplitTol of the on-disk size — the
// gap being the header, tensor info area and padding; anything else falls back
// to the metadata estimate (dense treatment when expert metadata is missing).
//
// When ok is false (unreadable/invalid GGUF), conservative fallbacks are used:
// 32 layers, 1024 bytes/layer/token, 32768 training context, all-dense weights.
func buildTuneModel(m modelMetrics, ok bool, weightsBytes int64) tuneModel {
	if !ok {
		return tuneModel{
			WeightsBytes:             weightsBytes,
			Layers:                   32,
			KVBytesPerTokPerLayerF16: 1024,
			TrainCtx:                 32768,
			DenseBytes:               weightsBytes,
		}
	}
	layers := m.BlockCount
	if layers <= 0 {
		layers = 32
	}
	trainCtx := m.ContextLength
	if trainCtx <= 0 {
		trainCtx = 32768
	}
	kvPerLayer := kvBytesPerTokenF16(m)
	if kvLayers := kvCacheLayers(m); kvLayers > 0 && kvLayers != layers {
		kvPerLayer = kvPerLayer * float64(kvLayers) / float64(layers)
	}
	tm := tuneModel{
		WeightsBytes:             weightsBytes,
		Layers:                   layers,
		KVBytesPerTokPerLayerF16: kvPerLayer,
		TrainCtx:                 trainCtx,
	}

	// Expert/dense split: exact tensor sizes when the split validates against
	// the file size, else the metadata estimate.
	expert, dense := int64(0), weightsBytes
	splitUsed := false
	if m.Path != "" && weightsBytes > 0 {
		if e, d, splitOK := readGGUFTensorSplit(m.Path); splitOK &&
			math.Abs(float64(e+d-weightsBytes))/float64(weightsBytes) <= tuneTensorSplitTol {
			expert, dense, splitUsed = e, d, true
		}
	}
	if !splitUsed {
		e := estimateExpertBytes(m, weightsBytes)
		expert, dense = e, weightsBytes-e
	}
	tm.ExpertBytes = expert
	tm.DenseBytes = dense
	if m.ExpertCount > 0 {
		tm.ExpertUsedFrac = float64(m.ExpertUsedCount) / float64(m.ExpertCount)
	}
	return tm
}

// ─── Tuning pure function ─────────────────────────────────────────

// Tuning constants. All budgets/reserves are bytes unless suffixed MB/GB.
const (
	// RAM: plan against min(free, 90% of total) further reduced by a 15%
	// safety margin for the OS and other processes; floored at 1 GB so
	// degenerate snapshots still yield a plan.
	tuneRAMTotalCapRatio = 0.90
	tuneRAMSafetyFactor  = 0.85
	tuneMinUsableRAM     = 1 << 30
	// A discrete GPU needs at least 1536 MB; 512MB-class iGPUs (shared system
	// memory) are treated as no GPU.
	tuneGPUActiveMinVRAMMB = 1536
	// VRAM reserved for the display driver and compute context: NVIDIA's CUDA
	// context is smaller than AMD's ROCm stack.
	tuneNvidiaVRAMReserveMB = 512
	tuneOtherVRAMReserveMB  = 700
	// q8_0 KV cache occupies 8.5 bits per weight vs f16's 16 bits:
	// 8.5/16 = 0.53125.
	tuneQ8KVSizeRatio = 0.53125
	// Compute buffer (logits + activation workspace): a base 384 MB, plus
	// 256 MB for contexts >= 16384 and another 512 MB for contexts >= 65536
	// where the logits/graph grow superlinearly.
	tuneComputeBufBaseMB    = 384
	tuneComputeBufLargeMB   = 256
	tuneComputeBufLargeFrom = 16384
	tuneComputeBufHugeMB    = 512
	tuneComputeBufHugeFrom  = 65536
	// Flash-attention headroom guard: if the chosen f16 full-offload plan
	// leaves less than 15% of usable VRAM free and a q8_0 plan buys a larger
	// context, prefer the q8_0 plan.
	tuneHeadroomGuardRatio = 0.15
	// Huge-context tiers (>= tuneComputeBufHugeFrom) must keep a minimum 8%
	// headroom on the GPU budget: estimation error in the KV geometry and the
	// compute-buffer constants is amplified at 128k-scale contexts (measured
	// edge cases down to ~2% free VRAM), and a service that fails to load on
	// VRAM OOM costs far more than one ladder tier of context.
	tuneHugeCtxSafetyRatio = 0.92
	// Context ladder (descending) and the partial-offload context candidates.
	tuneCtxMax = 131072
	tuneCtxMin = 2048
	// Measured-bandwidth flip: a full-offload plan whose best context is
	// below tuneFullOffloadCrampedCtx counts as cramped (8192 is the
	// smallest context that still feels roomy for chat); when the cpu-moe
	// plan is available and the measured RAM bandwidth predicts at least
	// tuneCPUMoeMinTPS (interactive-speed floor) of expert-in-RAM decode via
	// estimateCPUMoeTPS, the cpu-moe plan's much larger context wins over
	// the cramped full offload. Bandwidth 0 (no measurement) or an estimate
	// below the floor keeps full offload winning outright.
	tuneFullOffloadCrampedCtx = 8192
	tuneCPUMoeMinTPS          = 3.0
)

// ctxLadderFor filters ladder (descending) to entries not exceeding trainCtx
// (trainCtx <= 0 is treated as 32768), always keeping at least the smallest
// entry so every plan has a context.
func ctxLadderFor(trainCtx int, ladder []int) []int {
	if trainCtx <= 0 {
		trainCtx = tuneCtxMax
	}
	out := make([]int, 0, len(ladder))
	for _, ctx := range ladder {
		if ctx <= trainCtx {
			out = append(out, ctx)
		}
	}
	if len(out) == 0 {
		out = append(out, ladder[len(ladder)-1])
	}
	return out
}

// mergeCachePlans applies the shared A/B merge over the ladder: A is the f16
// KV plan (factor 1.0), B the q8_0 KV plan (factor tuneQ8KVSizeRatio, NVIDIA
// only). B wins only when it enables a strictly larger context, or when A is
// tight (< tuneHeadroomGuardRatio of usable VRAM left) while B buys more
// context; equal contexts prefer A (f16). Huge-context tiers
// (>= tuneComputeBufHugeFrom) only count as fitting when they leave the
// tuneHugeCtxSafetyRatio margin of the budget; smaller tiers use the raw
// budget. footprint returns the plan's total footprint for a context under
// the given KV size factor. Returns useB and the chosen context (0 when
// neither plan fits). Shared by the full-offload and the cpu-moe steps so
// both apply identical tie-breaking rules.
func mergeCachePlans(ladder []int, budget float64, nvidia bool, footprint func(ctx int, kvFactor float64) float64) (useB bool, ctx int) {
	fitCtx := func(kvFactor float64) int {
		for _, ctx := range ladder {
			limit := budget
			if ctx >= tuneComputeBufHugeFrom {
				// Huge-context tiers demand a minimum safety margin: at
				// 128k-scale contexts the KV/compute estimates are amplified
				// and a slight overshoot means the server fails to load.
				limit = budget * tuneHugeCtxSafetyRatio
			}
			if footprint(ctx, kvFactor) <= limit {
				return ctx
			}
		}
		return 0
	}
	aCtx := fitCtx(1.0)
	bCtx := 0
	if nvidia {
		bCtx = fitCtx(tuneQ8KVSizeRatio)
	}
	if aCtx > 0 && (bCtx == 0 || aCtx >= bCtx) {
		if bCtx > 0 {
			// Headroom guard: a tight f16 plan (< 15% budget left) with a q8_0
			// plan offering a larger context is swapped for the q8_0 plan.
			used := footprint(aCtx, 1.0)
			if budget-used < tuneHeadroomGuardRatio*budget && bCtx > aCtx {
				return true, bCtx
			}
		}
		return false, aCtx
	}
	if bCtx > 0 {
		// B enables a strictly larger context than A (or A does not exist).
		return true, bCtx
	}
	return false, 0
}

// estimateCPUMoeTPS approximates the decode speed of the cpu-moe plan: with
// the expert weights in system RAM, decode is bound by streaming the active
// expert bytes per token from DRAM, so t/s ≈ RAM read bandwidth / active
// expert bytes per token. Units are consistent: ramBandwidthGBs is decimal
// GB/s (1e9 bytes/s, the unit the benchbw calibration reports) and
// expertBytesPerToken is plain bytes, so the 1e9 factors cancel in the unit
// story (bytes per second over bytes per token). Either input unusable
// (bandwidth <= 0 or bytes-per-token <= 0) returns 0 = "no estimate". The
// caller derives expertBytesPerToken as ExpertBytes × ExpertUsedFrac (the
// router activates that fraction of the expert pool per token); always-
// active shared experts are read on every token but not accounted, so the
// bytes-per-token input can be an underestimate and the t/s result
// correspondingly optimistic — the fixed tuneCPUMoeMinTPS floor absorbs
// that slack.
func estimateCPUMoeTPS(ramBandwidthGBs, expertBytesPerToken float64) float64 {
	if ramBandwidthGBs <= 0 || expertBytesPerToken <= 0 {
		return 0
	}
	return ramBandwidthGBs * 1e9 / expertBytesPerToken
}

// tuneModelConfig computes hardware-aware llama-server parameters for one
// model. Pure and deterministic: same inputs, same ModelConfig. The plan body
// (tuneModelConfigPlan) is post-processed with the platform-level pins that
// apply to every plan shape:
//   - Android (unified memory): context checkpoints are always disabled.
//     llama-server copies KV state into host RAM at each auto-checkpoint and
//     the app never calls the restore API, so on 8-12 GB phones they are pure
//     RAM pressure.
func tuneModelConfig(hw tuneHardware, m tuneModel) ModelConfig {
	cfg := tuneModelConfigPlan(hw, m)
	if hw.AndroidPlatform {
		cfg.CtxCheckpointsOff = true
	}
	return cfg
}

// tuneModelConfigPlan is the sizing core of tuneModelConfig.
//
// Strategy:
//  1. No usable GPU → CPU-only plan: GPULayers=0, FlashAttn off, f16 cache
//     (no cache-type written), largest ladder context whose weights + KV fit
//     in usable RAM (smallest entry when none fits).
//  2. GPU present → try full offload first:
//     A) f16 KV everywhere, or B) q8_0 KV (NVIDIA only, 0.53125 the size).
//     Prefer A unless B enables a strictly larger context, or A leaves less
//     than 15% VRAM headroom while B buys more context. Full offload wins
//     outright — with one measured-calibration exception: when the winning
//     context is cramped (below tuneFullOffloadCrampedCtx), the model is MoE
//     with experts fitting usable RAM, and a measured RAM bandwidth
//     (hw.RAMBandwidthGBs > 0, see core/benchbw.go) predicts cpu-moe decode
//     at estimateCPUMoeTPS >= tuneCPUMoeMinTPS t/s, the cpu-moe plan of step
//     3 is preferred instead: a huge-context expert-in-RAM plan beats a
//     cramped-context full offload. Without a measurement (bandwidth 0) or
//     below the t/s floor the flip never engages. On Windows the emitted
//     full-offload plan additionally pins Threads=2 (an explicit large
//     --threads contends on the memory bus, unsloth #5692 measurement) and
//     disables context checkpoints (the app never restores them; each
//     auto-checkpoint copies KV state into host RAM) — the pin never touches
//     the cpu-moe, partial-offload, Apple or non-Windows plans.
//  3. Full offload impossible (or flipped away from in step 2) but the model
//     is MoE (ExpertBytes > 0) and the experts fit usable RAM → cpu-moe
//     plan: experts stay on CPU (--cpu-moe), the GPU carries dense weights +
//     KV + compute buffers with GPULayers="all". The same A=f16 / B=q8_0
//     merge picks the context. Threads=0 omits the threads line:
//     llama-server's automatic thread sizing measured >= manual physical-
//     core threads when expert GEMMs run on the CPU.
//  4. Partial offload fallback (dense models, or experts that do not fit
//     RAM): for each candidate context (8192/4096/2048) offload n =
//     floor((vramUsable - computeBuf) / (perLayerWeights + perLayerKV))
//     layers, keeping the CPU-side remainder within usable RAM; cache stays
//     f16.
//  5. Even partial offload impossible → CPU-only plan.
func tuneModelConfigPlan(hw tuneHardware, m tuneModel) ModelConfig {
	const MB = 1 << 20
	// GB in bytes: RAMTotalGB/RAMFreeGB are GiB values (getTotalMemoryGB and
	// getFreeMemoryGB divide by 1024^3), so GiB -> bytes is x1024xMB.
	const GB = 1024 * MB

	usableRAM := math.Min(hw.RAMFreeGB, hw.RAMTotalGB*tuneRAMTotalCapRatio) * tuneRAMSafetyFactor * GB
	if usableRAM < tuneMinUsableRAM {
		usableRAM = tuneMinUsableRAM
	}

	gpuActive := hw.GPUVendor != vendorNone && hw.VRAMMB >= tuneGPUActiveMinVRAMMB
	vramUsable := 0.0
	if gpuActive {
		reserveMB := tuneOtherVRAMReserveMB
		if hw.GPUVendor == vendorNvidia {
			reserveMB = tuneNvidiaVRAMReserveMB
		}
		vramUsable = math.Max(0, float64(hw.VRAMMB)*MB-float64(reserveMB)*MB)
	}

	// Threads: physical cores are llama.cpp's guidance; when unknown, half the
	// logical CPUs (hyperthreads add little); 0 keeps llama-server's default.
	threads := 0
	if hw.PhysicalCores > 0 {
		threads = hw.PhysicalCores
	} else if hw.LogicalCPUs > 1 {
		threads = hw.LogicalCPUs / 2
	}
	// Android big.LITTLE cap: phones report every online core as a physical
	// core, but scheduling GEMM workers onto the LITTLE cluster degrades
	// decode speed and drives SoC thermals, so pin the worker pool to the
	// performance-cluster count when the cpufreq probe knows it and the SoC
	// actually has a split (PerfCores < PhysicalCores). Every other platform
	// — and uniform topologies — keep the rule above byte-for-byte.
	if hw.AndroidPlatform && hw.PerfCores > 0 && hw.PerfCores < hw.PhysicalCores && threads > hw.PerfCores {
		threads = hw.PerfCores
	}

	layers := m.Layers
	if layers <= 0 {
		layers = 32
	}
	// Per-token KV total (f16) = per-layer cost x layer count; see
	// buildTuneModel for the hybrid-attention folding that keeps this equal to
	// kvBytesPerTokenF16 * kvCacheLayers.
	kvPerTok := m.KVBytesPerTokPerLayerF16 * float64(layers)
	w := float64(m.WeightsBytes)

	ladder := ctxLadderFor(m.TrainCtx, []int{131072, 65536, 32768, 16384, 8192, 4096, tuneCtxMin})
	computeBuf := func(ctx int) float64 {
		buf := tuneComputeBufBaseMB
		if ctx >= tuneComputeBufLargeFrom {
			buf += tuneComputeBufLargeMB
		}
		if ctx >= tuneComputeBufHugeFrom {
			buf += tuneComputeBufHugeMB
		}
		return float64(buf) * MB
	}

	cfg := defaultModelConfig()
	cfg.Threads = threads

	// ramCtxSize picks the largest ladder context whose f16 weights + KV fit
	// in usable RAM, falling back to the smallest ladder entry. Shared by the
	// CPU-only plan and the Metal plan (unified memory bounds both the same
	// way).
	ramCtxSize := func() int {
		for _, ctx := range ladder {
			if w+kvPerTok*float64(ctx) <= usableRAM {
				return ctx
			}
		}
		return ladder[len(ladder)-1]
	}

	// cpuOnlyPlan: no GPU offload; largest ladder context whose weights + KV
	// fit in usable RAM, falling back to the smallest ladder entry.
	cpuOnlyPlan := func() {
		cfg.GPULayers = "0"
		cfg.FlashAttn = false
		cfg.CacheTypeK, cfg.CacheTypeV = "", ""
		cfg.CtxSize = ramCtxSize()
	}

	// Metal plan (Apple Silicon): unified memory puts every weight one
	// GPU-visible allocation away, so all layers offload (GPULayers="all")
	// while the context is bounded by the same usable-RAM budget as the
	// CPU-only plan — GPU and CPU share one physical pool, there is no VRAM
	// budget to merge against. FlashAttn stays off in v1: llama.cpp supports
	// flash attention on Metal upstream, but this tuner has no Metal benchmark
	// path yet, so the plan conservatively stays on the default path. cpu-moe
	// is a CUDA-centric VRAM/RAM split (meaningless on unified memory), so
	// CPUMoe/NCpuMoe stay off and the bench chain that calibrates the cpu-moe
	// flip never runs for apple (UsedCPUMoe=false without a bench attempt).
	if hw.GPUVendor == vendorApple {
		cfg.GPULayers = "all"
		cfg.FlashAttn = false
		cfg.CacheTypeK, cfg.CacheTypeV = "", ""
		cfg.CtxSize = ramCtxSize()
		return cfg
	}

	if !gpuActive {
		cpuOnlyPlan()
		return cfg
	}

	nvidia := hw.GPUVendor == vendorNvidia

	// cpuMoePlan builds the --cpu-moe plan (experts stay in system RAM, the
	// GPU carries dense weights + KV + compute buffers) when the model is
	// MoE with experts fitting usable RAM; ok=false leaves the caller's plan
	// untouched. Shared verbatim by the measured-bandwidth flip inside step 1
	// and by the fallback step 2, so both emit identical plans.
	cpuMoePlan := func() (ModelConfig, bool) {
		if m.ExpertBytes <= 0 || float64(m.ExpertBytes) > usableRAM {
			return ModelConfig{}, false
		}
		dense := float64(m.DenseBytes)
		if dense <= 0 {
			// Defensive default for hand-built inputs without a split.
			dense = math.Max(0, w-float64(m.ExpertBytes))
		}
		if useB, moeCtx := mergeCachePlans(ladder, vramUsable, nvidia, func(ctx int, kvFactor float64) float64 {
			return dense + kvPerTok*float64(ctx)*kvFactor + computeBuf(ctx)
		}); moeCtx > 0 {
			out := defaultModelConfig()
			out.GPULayers = "all"
			out.CtxSize = moeCtx
			out.FlashAttn = nvidia
			out.CPUMoe = true
			// Threads 0 omits the threads line so llama-server sizes threads
			// automatically: with expert GEMMs running on the CPU the auto
			// sizing measured >= pinning threads to the physical core count.
			out.Threads = 0
			if useB {
				out.CacheTypeK, out.CacheTypeV = "q8_0", "q8_0"
			}
			return out, true
		}
		return ModelConfig{}, false
	}

	// Step 1: full offload — every weight on the GPU beats any expert split.
	// One measured exception (the bandwidth flip): when the winning context
	// is cramped (fullCtx < tuneFullOffloadCrampedCtx), a cpu-moe plan is
	// available, and the calibrated RAM bandwidth predicts at least
	// tuneCPUMoeMinTPS t/s of expert-in-RAM decode, prefer cpu-moe instead —
	// a huge-context plan on fast RAM beats a cramped-context full offload.
	// Without a measurement (hw.RAMBandwidthGBs == 0) or below the t/s floor
	// the flip never engages and full offload wins outright exactly as
	// before.
	if useB, fullCtx := mergeCachePlans(ladder, vramUsable, nvidia, func(ctx int, kvFactor float64) float64 {
		return w + kvPerTok*float64(ctx)*kvFactor + computeBuf(ctx)
	}); fullCtx > 0 {
		if fullCtx < tuneFullOffloadCrampedCtx && hw.RAMBandwidthGBs > 0 {
			// Active expert bytes per token: the router activates
			// ExpertUsedFrac of the expert pool each token; when the
			// fraction is absent, assume the whole pool streams (the slow,
			// safe direction). Always-active shared experts are read on
			// every token but not accounted here, so this can understate
			// the true bytes per token — see estimateCPUMoeTPS.
			activeBytes := float64(m.ExpertBytes) * m.ExpertUsedFrac
			if activeBytes <= 0 {
				activeBytes = float64(m.ExpertBytes)
			}
			if estimateCPUMoeTPS(hw.RAMBandwidthGBs, activeBytes) >= tuneCPUMoeMinTPS {
				if moeCfg, ok := cpuMoePlan(); ok {
					return moeCfg
				}
			}
		}
		cfg.GPULayers = "all"
		cfg.CtxSize = fullCtx
		cfg.FlashAttn = nvidia
		if useB {
			cfg.CacheTypeK, cfg.CacheTypeV = "q8_0", "q8_0"
		}
		// Windows full-offload pins: an explicit large --threads contends on
		// the memory bus under full GPU offload (unsloth #5692 measurement),
		// so pin 2 worker threads; and llama-server auto-creates context
		// checkpoints during prompt processing (each copies KV state into
		// host RAM, b10689) while the app never calls the restore API, so
		// disable them too. Scoped to this branch only: the cpu-moe, partial
		// and Apple plans keep their own thread rules.
		if hw.WindowsPlatform {
			cfg.Threads = 2
			cfg.CtxCheckpointsOff = true
		}
		return cfg
	}

	// Step 2: cpu-moe fallback — keep the expert weights in system RAM when
	// full offload is impossible. Only when the experts fit usable RAM;
	// otherwise the expert layers straddle both sides and partial offload
	// (step 3) is the better fallback.
	if moeCfg, ok := cpuMoePlan(); ok {
		return moeCfg
	}

	// Step 3: partial offload — offload as many whole layers as fit in VRAM
	// for each candidate context, keeping the CPU-side remainder within
	// usable RAM.
	perLayerW := w / float64(layers)
	for _, ctx := range ctxLadderFor(m.TrainCtx, []int{8192, 4096, tuneCtxMin}) {
		perLayer := perLayerW + m.KVBytesPerTokPerLayerF16*float64(ctx)
		if perLayer <= 0 {
			continue
		}
		n := int(math.Floor((vramUsable - computeBuf(ctx)) / perLayer))
		if n < 0 {
			n = 0
		}
		if n > layers {
			n = layers
		}
		if n >= 1 && float64(layers-n)*perLayer <= usableRAM {
			cfg.GPULayers = strconv.Itoa(n)
			cfg.CtxSize = ctx
			cfg.FlashAttn = hw.GPUVendor == vendorNvidia
			// Partial offload keeps the f16 cache: quantized cache gains are
			// marginal when only part of the pipeline sits on the GPU.
			cfg.CacheTypeK, cfg.CacheTypeV = "", ""
			return cfg
		}
	}

	cpuOnlyPlan()
	return cfg
}

// ─── Hardware snapshot & weight budget ────────────────────────────

// tuneVendorForName classifies a GPU display name for the tuner: "nvidia",
// "amd" or "apple" when the name matches (case-insensitive), "" when
// unclassifiable (callers keep their machine-wide classification as fallback).
// The GPUInfo.Vendor field is the primary classification; name matching is the
// fallback for entries without one.
func tuneVendorForName(name string) string {
	lower := strings.ToLower(name)
	switch {
	case strings.Contains(lower, vendorNvidia):
		return vendorNvidia
	case strings.Contains(lower, "amd") || strings.Contains(lower, "radeon"):
		return vendorAMD
	case strings.Contains(lower, vendorApple):
		return vendorApple
	}
	return ""
}

// tuneTarget is the GPU-planning decision for the tuner: the VRAM budget and
// vendor classification the sizing rules run against, plus the identity of the
// pinned GPU for the planning log (empty when the plan fell back to auto).
type tuneTarget struct {
	VRAMMB int
	Vendor string
	Name   string
	UUID   string
}

// tunePlanTarget resolves the GPU the tuner plans against. deviceID is the
// persisted serving-GPU selection (ServerConfig.DeviceID, a stable nvidia-smi
// UUID — the same value pinned into the llama-server child via
// CUDA_VISIBLE_DEVICES): when it exactly matches (case-sensitive) one of the
// probed GPUs, THAT card decides VRAMMB and vendor, keeping the plan
// consistent with the GPU llama-server actually uses on multi-GPU hosts.
// An empty or unmatched deviceID falls back to the historical behavior: the
// largest-VRAM GPU across the list decides VRAMMB, and the vendor is voted
// machine-wide (any GPU named nvidia (case-insensitive) or an available CUDA
// driver → nvidia; else any GPU named amd/radeon → amd; else any GPU carrying
// (or named like) an Apple card → apple; else none).
func tunePlanTarget(gpus []GPUInfo, cudaAvailable bool, deviceID string) tuneTarget {
	vramMB := 0
	hasNVIDIA, hasAMD, hasApple := false, false, false
	for _, g := range gpus {
		if g.MemoryMB > vramMB {
			vramMB = g.MemoryMB
		}
		// GPUInfo.Vendor is the probe's own classification; the name-based
		// tuneVendorForName covers legacy entries without a Vendor field.
		if g.Vendor == vendorApple {
			hasApple = true
		}
		switch tuneVendorForName(g.Name) {
		case vendorNvidia:
			hasNVIDIA = true
		case vendorAMD:
			hasAMD = true
		case vendorApple:
			hasApple = true
		}
	}
	target := tuneTarget{VRAMMB: vramMB, Vendor: vendorNone}
	switch {
	case hasNVIDIA || cudaAvailable:
		target.Vendor = vendorNvidia
	case hasAMD:
		target.Vendor = vendorAMD
	case hasApple:
		target.Vendor = vendorApple
	}
	if deviceID != "" {
		for _, g := range gpus {
			if g.UUID == deviceID {
				target.VRAMMB = g.MemoryMB
				target.Name = g.Name
				target.UUID = g.UUID
				// The pinned card's own identity decides its vendor: the
				// probe's Vendor field first, the name classification as the
				// legacy fallback; an unclassifiable card keeps the
				// machine-wide vote above.
				if g.Vendor != "" {
					target.Vendor = g.Vendor
				} else if v := tuneVendorForName(g.Name); v != "" {
					target.Vendor = v
				}
				break
			}
		}
	}
	return target
}

// tuneHardware snapshots the hardware through the same caches the Home page
// uses (GetCPU/GetMemory/gpuListSource/GetCUDA — one detection chain per
// process, no separate probing) and resolves the tuner's GPU planning target
// (see tunePlanTarget): the serving GPU selected in the server config by UUID
// when present and matching, else the largest-VRAM GPU. RAMBandwidthGBs comes
// from the measured-bandwidth calibration (getCalibratedRAMBandwidth in
// benchbw.go: cached per hardware fingerprint in llama-desktop-benchcache.json,
// single-flight); 0 on failure keeps every tuner rule on its static behavior.
func (a *App) tuneHardware() tuneHardware {
	cpu := a.GetCPU()
	mem := a.GetMemory()
	gpus := gpuListSource()
	cuda := a.GetCUDA()

	hw := tuneHardware{
		RAMTotalGB:      mem.TotalGB,
		RAMFreeGB:       mem.FreeGB,
		PhysicalCores:   cpu.Cores,
		LogicalCPUs:     cpu.LogicalCPUs,
		CPUModel:        cpu.Model,
		PerfCores:       cpu.PerfCores,
		AndroidPlatform: runtime.GOOS == "android",
		WindowsPlatform: runtime.GOOS == "windows",
	}

	// Serving-GPU selection: read the persisted DeviceID under serverConfigMu
	// (the same cache startServerInternal pins the child env from) so the plan
	// targets the GPU llama-server actually runs on.
	serverConfigMu.Lock()
	deviceID := cachedServerConfig.DeviceID
	serverConfigMu.Unlock()

	target := tunePlanTarget(gpus, cuda.Available, deviceID)
	hw.VRAMMB = target.VRAMMB
	hw.GPUVendor = target.Vendor
	if target.Name != "" {
		log.Printf("[INFO] tune: planning against GPU %s (uuid %.8s)", target.Name, target.UUID)
	} else if target.Vendor == vendorApple {
		log.Printf("[INFO] tune: planning against Apple Silicon (Metal, unified memory)")
	} else if len(gpus) > 0 {
		log.Printf("[INFO] tune: planning against largest-VRAM GPU (auto)")
	}

	// Measured RAM bandwidth calibration: cached per hardware fingerprint,
	// single-flight across concurrent tune clicks; 0 on failure, which keeps
	// the tuner on its static rules. Skipped when no plan can ever consume
	// the value (see tuneNeedsRAMBandwidth): running the all-core benchmark
	// anyway would be wasted time — and on a phone an avoidable OOM/lmkd risk.
	if !tuneNeedsRAMBandwidth(hw.GPUVendor, runtime.GOOS) {
		return hw
	}
	hw.RAMBandwidthGBs, _ = getCalibratedRAMBandwidth(hw)
	return hw
}

// tuneNeedsRAMBandwidth reports whether the measured RAM-bandwidth calibration
// (getCalibratedRAMBandwidth in benchbw.go) must run for this tune: the value
// only gates the CUDA-centric cpu-moe flip inside tuneModelConfig, which is
// reached by neither the Metal plan (apple, unified memory) nor the Android
// plan (cpu-only — the app sandbox has no GPU stack). Pure function, directly
// unit-testable; goos is passed in so desktop machines can test the android
// branch.
func tuneNeedsRAMBandwidth(gpuVendor, goos string) bool {
	return gpuVendor != vendorApple && goos != "android"
}

// tuneWeightsBytes returns the tuner's weight budget: the main GGUF size plus
// the same-directory mmproj-*.gguf projector when the model is multimodal.
// Projector stat failures are ignored (best-effort extra weight).
func tuneWeightsBytes(m ModelInfo) int64 {
	weights := m.SizeBytes
	if !m.HasMMProj {
		return weights
	}
	matches, err := filepath.Glob(filepath.Join(filepath.Dir(m.Path), "mmproj-*.gguf"))
	if err != nil {
		return weights
	}
	for _, p := range matches {
		if fi, err := os.Stat(p); err == nil {
			weights += fi.Size()
		}
	}
	return weights
}

// tuneFallbackLogLine is the log message emitted when a model's GGUF metrics
// cannot be read and the conservative fallback is used. Separate helper so the
// exact wording is stable and greppable.
func tuneFallbackLogLine(modelID string) string {
	return "[INFO] tune: " + modelID + " has unreadable GGUF metadata, using conservative fallback (layers=32, kv=1024B/layer/token, ctx=32768)"
}
