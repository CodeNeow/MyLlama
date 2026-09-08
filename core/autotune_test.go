package core

import (
	"bytes"
	"fmt"
	"math"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
)

// ─── readGGUFModelMetrics ────────────────────────────────────────

// TestReadGGUFModelMetricsGQA verifies the standard GQA fixture: architecture
// keys are resolved via the {arch} prefix, u32 and u64 numerics both parse,
// and non-metric / non-numeric KVs (bool, strings) are skipped without
// desynchronizing the stream. MoE geometry keys and general.file_type round
// out the expert metadata surface.
func TestReadGGUFModelMetricsGQA(t *testing.T) {
	dir := t.TempDir()
	path := writeTempGGUF(t, dir, "qwen35.gguf", buildGGUF(3,
		strKV("general.name", "Qwen3.5-4B"),
		strKV("general.architecture", "qwen35"),
		u32KV("general.size", 12345), // non-metric numeric key, ignored by lookups
		u32KV("general.file_type", 15),
		boolKV("general.foo", 1), // bool type exercises the skip branch
		u64KV("qwen35.block_count", 36),
		u64KV("qwen35.context_length", 262144),
		u32KV("qwen35.embedding_length", 2560),
		u32KV("qwen35.attention.head_count", 32),
		u32KV("qwen35.attention.head_count_kv", 8),
		u32KV("qwen35.attention.key_length", 128),
		u32KV("qwen35.attention.value_length", 128),
		u32KV("qwen35.rope.dimension_count", 128),
		u32KV("qwen35.full_attention_interval", 3),
		u32KV("qwen35.expert_count", 128),
		u32KV("qwen35.expert_used_count", 8),
		u32KV("qwen35.expert_shared_count", 1),
		u32KV("qwen35.expert_feed_forward_length", 960),
		u32KV("qwen35.leading_dense_block_count", 1),
		strKV("tokenizer.ggml.model", "gpt2"), // unrelated string KV, skipped
	))

	m, ok := readGGUFModelMetrics(path)
	if !ok {
		t.Fatal("readGGUFModelMetrics returned false, expected successful parse")
	}
	if m.Arch != "qwen35" || m.Name != "Qwen3.5-4B" {
		t.Errorf("Arch/Name = %q/%q, want qwen35/Qwen3.5-4B", m.Arch, m.Name)
	}
	if m.Path != path {
		t.Errorf("Path = %q, want %q", m.Path, path)
	}
	if m.Quant != "Q4_K_M" {
		t.Errorf("Quant = %q, want Q4_K_M (file_type 15)", m.Quant)
	}
	want := []struct {
		name string
		got  int
		want int
	}{
		{"BlockCount", m.BlockCount, 36},
		{"ContextLength", m.ContextLength, 262144},
		{"EmbeddingLength", m.EmbeddingLength, 2560},
		{"HeadCount", m.HeadCount, 32},
		{"HeadCountKV", m.HeadCountKV, 8},
		{"KeyLength", m.KeyLength, 128},
		{"ValueLength", m.ValueLength, 128},
		{"RopeDimCount", m.RopeDimCount, 128},
		{"FullAttentionInterval", m.FullAttentionInterval, 3},
		{"KVLoRaRank", m.KVLoRaRank, 0},
		{"ExpertCount", m.ExpertCount, 128},
		{"ExpertUsedCount", m.ExpertUsedCount, 8},
		{"ExpertSharedCount", m.ExpertSharedCount, 1},
		{"ExpertFFNLength", m.ExpertFFNLength, 960},
		{"LeadingDenseBlockCount", m.LeadingDenseBlockCount, 1},
	}
	for _, w := range want {
		if w.got != w.want {
			t.Errorf("%s = %d, want %d", w.name, w.got, w.want)
		}
	}
}

// TestReadGGUFModelMetricsMLA verifies the MLA fixture (deepseek2): kv_lora_rank
// is picked up, missing head_count_kv stays 0, and rope uses the
// {arch}.rope.dimension_count key.
func TestReadGGUFModelMetricsMLA(t *testing.T) {
	dir := t.TempDir()
	path := writeTempGGUF(t, dir, "deepseek2.gguf", buildGGUF(2,
		strKV("general.architecture", "deepseek2"),
		u32KV("deepseek2.block_count", 61),
		u32KV("deepseek2.context_length", 16384),
		u32KV("deepseek2.attention.kv_lora_rank", 512),
		u32KV("deepseek2.attention.key_length", 576),
		u32KV("deepseek2.attention.value_length", 512),
		u32KV("deepseek2.rope.dimension_count", 64),
	))

	m, ok := readGGUFModelMetrics(path)
	if !ok {
		t.Fatal("readGGUFModelMetrics returned false, expected successful parse")
	}
	if m.Arch != "deepseek2" {
		t.Errorf("Arch = %q, want deepseek2", m.Arch)
	}
	if m.BlockCount != 61 || m.ContextLength != 16384 {
		t.Errorf("BlockCount/ContextLength = %d/%d, want 61/16384", m.BlockCount, m.ContextLength)
	}
	if m.KVLoRaRank != 512 {
		t.Errorf("KVLoRaRank = %d, want 512", m.KVLoRaRank)
	}
	if m.HeadCountKV != 0 {
		t.Errorf("missing head_count_kv should stay 0, got %d", m.HeadCountKV)
	}
	if m.RopeDimCount != 64 {
		t.Errorf("RopeDimCount = %d, want 64", m.RopeDimCount)
	}
}

// TestReadGGUFModelMetricsInvalid verifies invalid magic, a missing file and an
// over-limit kvCount all return false (no panic), consistent with readGGUFMeta.
func TestReadGGUFModelMetricsInvalid(t *testing.T) {
	dir := t.TempDir()
	badPath := filepath.Join(dir, "bad.gguf")
	if err := os.WriteFile(badPath, []byte("NOTGUF DATA"), 0644); err != nil {
		t.Fatal(err)
	}
	if _, ok := readGGUFModelMetrics(badPath); ok {
		t.Error("invalid magic should return false")
	}
	if _, ok := readGGUFModelMetrics(filepath.Join(dir, "nope.gguf")); ok {
		t.Error("missing file should return false")
	}

	// Hand-crafted header with kvCount 5000 (over the 4096 guard) and no KV
	// entries: parsing must abort before the loop, same as readGGUFMeta.
	var buf bytes.Buffer
	buf.WriteString("GGUF")
	putU32(&buf, 3)
	putU64(&buf, 0)    // tensorCount
	putU64(&buf, 5000) // kvCount exceeds the limit
	hugePath := writeTempGGUF(t, dir, "huge.gguf", buf.Bytes())
	if _, ok := readGGUFModelMetrics(hugePath); ok {
		t.Error("kvCount over the 4096 limit should return false")
	}
}

// arrayStrKV builds a GGUF string-array KV (valueType=9: element type u32 +
// count u64 + elements, per the GGUF v3 array layout), used to prove
// full-header parsing survives tokenizer arrays far larger than the shared
// skipGGUFValue's 1000-element cap.
func arrayStrKV(key string, values []string) ggufKV {
	var buf bytes.Buffer
	putU32(&buf, 8) // element type: string
	putU64(&buf, uint64(len(values)))
	for _, v := range values {
		putU64(&buf, uint64(len(v)))
		buf.WriteString(v)
	}
	return ggufKV{key: key, valueType: 9, raw: buf.Bytes()}
}

// TestReadGGUFModelMetricsLargeArraySkipsFully verifies the parser skips a
// complete tokenizer-sized string array (1500 elements, past the shared
// skipGGUFValue 1000-element cap) and still reads metric keys that come after
// it: real GGUF files embed such arrays, and a truncating skip desynchronizes
// the stream (found via the local real-model evidence run).
func TestReadGGUFModelMetricsLargeArraySkipsFully(t *testing.T) {
	dir := t.TempDir()
	tokens := make([]string, 1500)
	for i := range tokens {
		tokens[i] = "tok"
	}
	path := writeTempGGUF(t, dir, "big.gguf", buildGGUF(3,
		strKV("general.architecture", "qwen35"),
		arrayStrKV("tokenizer.ggml.tokens", tokens),
		arrayStrKV("tokenizer.ggml.merges", tokens), // two big arrays back to back
		u32KV("qwen35.block_count", 36),
		u32KV("qwen35.context_length", 262144),
		u32KV("qwen35.attention.head_count", 32),
	))

	m, ok := readGGUFModelMetrics(path)
	if !ok {
		t.Fatal("readGGUFModelMetrics returned false; large arrays must be skipped fully")
	}
	if m.Arch != "qwen35" || m.BlockCount != 36 || m.ContextLength != 262144 || m.HeadCount != 32 {
		t.Errorf("metrics after large arrays = %+v, want block_count=36 context=262144 head_count=32", m)
	}
}

// ─── readGGUFTensorSplit (expert/dense split) ────────────────────

// ggufTensor is a test helper describing one GGUF tensor info record.
type ggufTensor struct {
	name   string
	dims   []uint64
	typ    uint32
	offset uint64
}

// buildGGUFTensors assembles a GGUF byte slice whose tensor info area (after
// the KV region) carries the given records, each serialized as name(string) +
// n_dims(u32) + dims(u64 x n_dims) + type(u32) + offset(u64) per the GGUF
// v2/v3 layout.
func buildGGUFTensors(version uint32, kvs []ggufKV, tensors []ggufTensor) []byte {
	var buf bytes.Buffer
	buf.WriteString("GGUF")
	putU32(&buf, version)
	putU64(&buf, uint64(len(tensors)))
	putU64(&buf, uint64(len(kvs)))
	for _, kv := range kvs {
		putU64(&buf, uint64(len(kv.key)))
		buf.WriteString(kv.key)
		putU32(&buf, kv.valueType)
		buf.Write(kv.raw)
	}
	for _, t := range tensors {
		putU64(&buf, uint64(len(t.name)))
		buf.WriteString(t.name)
		putU32(&buf, uint32(len(t.dims)))
		for _, d := range t.dims {
			putU64(&buf, d)
		}
		putU32(&buf, t.typ)
		putU64(&buf, t.offset)
	}
	return buf.Bytes()
}

// TestReadGGUFTensorSplitQuantBytes verifies the headless size math against
// the ggml block table: a Q4_K expert tensor (256-element blocks), a Q8_0
// dense tensor, a partial final Q8_0 block proving ceil rounding, and an F16
// shared-expert tensor that stays dense.
func TestReadGGUFTensorSplitQuantBytes(t *testing.T) {
	dir := t.TempDir()
	path := writeTempGGUF(t, dir, "split.gguf", buildGGUFTensors(3,
		[]ggufKV{strKV("general.architecture", "glm4")},
		[]ggufTensor{
			// Q4_K: 2304*576*8 = 10616832 elems = 41472 blocks of 256, *144 B.
			{name: "blk.3.ffn_gate_exps.weight", dims: []uint64{2304, 576, 8}, typ: 12},
			// Q8_0: 2048*2048 = 4194304 elems = 131072 blocks of 32, *34 B.
			{name: "blk.3.attn_q.weight", dims: []uint64{2048, 2048}, typ: 8},
			// Q8_0 with a partial final block: ceil(100/32) = 4 blocks, *34 B.
			{name: "token_embd.weight", dims: []uint64{100}, typ: 8},
			// F16 shared-expert tensor stays dense: 64*64 elems * 2 B.
			{name: "blk.3.ffn_down_shexp.weight", dims: []uint64{64, 64}, typ: 1},
		}))

	expert, dense, ok := readGGUFTensorSplit(path)
	if !ok {
		t.Fatal("readGGUFTensorSplit returned false, expected successful parse")
	}
	if want := int64(41472 * 144); expert != want {
		t.Errorf("expert bytes = %d, want %d", expert, want)
	}
	wantDense := int64(131072*34 + 4*34 + 64*64*2)
	if dense != wantDense {
		t.Errorf("dense bytes = %d, want %d", dense, wantDense)
	}
}

// TestReadGGUFTensorSplitNameRules verifies the expert-name classification:
// "_exps", "ffn_exp" and ".exp"+ffn mark expert tensors while routers,
// shared experts and plain dense tensors stay dense. Every tensor here is a
// 1-element F32 (4 bytes) so the sums count classifications directly.
func TestReadGGUFTensorSplitNameRules(t *testing.T) {
	dir := t.TempDir()
	path := writeTempGGUF(t, dir, "names.gguf", buildGGUFTensors(3, nil, []ggufTensor{
		{name: "blk.0.ffn_up_exps.weight", dims: []uint64{1}, typ: 0},         // _exps
		{name: "model.layers.0.ffn_exp_up.weight", dims: []uint64{1}, typ: 0}, // ffn_exp
		{name: "blk.0.ffn_down.exp.weight", dims: []uint64{1}, typ: 0},        // .exp + ffn
		{name: "blk.0.ffn_gate_inp.weight", dims: []uint64{1}, typ: 0},        // router stays dense
		{name: "blk.0.ffn_gate_shexp.weight", dims: []uint64{1}, typ: 0},      // shared expert stays dense
		{name: "output.weight", dims: []uint64{1}, typ: 0},
	}))

	expert, dense, ok := readGGUFTensorSplit(path)
	if !ok {
		t.Fatal("readGGUFTensorSplit returned false, expected successful parse")
	}
	if expert != 12 {
		t.Errorf("expert bytes = %d, want 12 (3 expert tensors x 4 B)", expert)
	}
	if dense != 12 {
		t.Errorf("dense bytes = %d, want 12 (3 dense tensors x 4 B)", dense)
	}
}

// TestReadGGUFTensorSplitUnreliable verifies the safety fallbacks: an unknown
// tensor type, n_dims outside 1..4, an empty tensor table, a truncated stream,
// a non-GGUF file and a tensor count above the cap all return ok=false so
// callers fall back to metadata estimation instead of trusting partial sums.
func TestReadGGUFTensorSplitUnreliable(t *testing.T) {
	dir := t.TempDir()
	kvs := []ggufKV{strKV("general.architecture", "glm4")}

	var truncated bytes.Buffer
	truncated.WriteString("GGUF")
	putU32(&truncated, 3)
	putU64(&truncated, 2) // tensorCount, no records follow
	putU64(&truncated, 0) // kvCount

	cases := []struct {
		name string
		data []byte
	}{
		{"unknown tensor type", buildGGUFTensors(3, kvs, []ggufTensor{
			{name: "blk.0.attn_q.weight", dims: []uint64{16}, typ: 99},
		})},
		{"n_dims zero", buildGGUFTensors(3, kvs, []ggufTensor{
			{name: "blk.0.attn_q.weight", typ: 0},
		})},
		{"n_dims five", buildGGUFTensors(3, kvs, []ggufTensor{
			{name: "blk.0.attn_q.weight", dims: []uint64{1, 1, 1, 1, 1}, typ: 0},
		})},
		{"no tensors at all", buildGGUFTensors(3, kvs, nil)},
		{"truncated after header", truncated.Bytes()},
		{"not a gguf", []byte("NOPE")},
	}
	for _, c := range cases {
		p := writeTempGGUF(t, dir, "bad.gguf", c.data)
		if _, _, ok := readGGUFTensorSplit(p); ok {
			t.Errorf("%s: expected ok=false", c.name)
		}
	}

	// tensorCount above the 2M cap aborts before the walk (crafting a header
	// only: the walk must not spin through two million EOF records).
	var huge bytes.Buffer
	huge.WriteString("GGUF")
	putU32(&huge, 3)
	putU64(&huge, tuneTensorCountMax+1)
	putU64(&huge, 0)
	p := writeTempGGUF(t, dir, "huge.gguf", huge.Bytes())
	if _, _, ok := readGGUFTensorSplit(p); ok {
		t.Error("tensorCount over the cap should return ok=false")
	}
}

// TestEstimateExpertBytes verifies the metadata estimator: exact F16/BF16/
// Q8_0/Q4_K_M/unknown-quant numbers on integer-friendly geometry, the shared
// expert term, dense treatment without expert metadata, MoE-layer arithmetic
// (block_count minus leading dense layers) and the file-size clamp.
func TestEstimateExpertBytes(t *testing.T) {
	// Geometry: 3 MoE layers x 8 experts x 3 matrices of 512x1024 elements.
	base := func(quant string, shared int) modelMetrics {
		return modelMetrics{
			Quant: quant, BlockCount: 4, LeadingDenseBlockCount: 1,
			ExpertCount: 8, ExpertSharedCount: shared,
			ExpertFFNLength: 512, EmbeddingLength: 1024,
		}
	}
	cases := []struct {
		name         string
		m            modelMetrics
		weightsBytes int64
		want         int64
	}{
		// 3*8*3*512*1024 = 37748736 elements.
		{"F16 at 2.0 bpw", base("F16", 0), 1 << 40, 75497472},
		{"BF16 contains F16", base("BF16", 0), 1 << 40, 75497472},
		{"Q8_0 at 1.06 bpw truncates", base("Q8_0", 0), 1 << 40, 40013660},
		{"Q4_K_M at 0.60 bpw truncates", base("Q4_K_M", 0), 1 << 40, 22649241},
		{"unknown quant falls back to 0.6", base("IQ2_XS", 0), 1 << 40, 22649241},
		// Shared experts widen the pool to 9 experts: 42467328 elements.
		{"shared expert adds to the pool", base("F16", 1), 1 << 40, 84934656},
		// Safety: no expert metadata or no MoE layers -> dense (0), estimates
		// above the file size clamp to the file size.
		{"no expert metadata is dense", modelMetrics{BlockCount: 4}, 1 << 40, 0},
		{"all blocks dense-leading is dense", base("F16", 0), 1 << 40, 0},
		{"estimate clamps to weights size", base("F16", 0), 1000, 1000},
	}
	for _, c := range cases {
		if c.name == "all blocks dense-leading is dense" {
			c.m.BlockCount = c.m.LeadingDenseBlockCount
		}
		if got := estimateExpertBytes(c.m, c.weightsBytes); got != c.want {
			t.Errorf("%s: estimateExpertBytes = %d, want %d", c.name, got, c.want)
		}
	}
}

// TestBuildTuneModelSplitPreference verifies the split chain in buildTuneModel:
// a tensor table whose sums match the file size wins over the metadata
// estimate, and a split missing the size by more than the tolerance falls
// back to the estimate.
func TestBuildTuneModelSplitPreference(t *testing.T) {
	dir := t.TempDir()
	// Expert 196608 F32 elements (786432 B) + dense 65536 elements (262144 B)
	// = exactly 1 MiB of tensor data.
	path := writeTempGGUF(t, dir, "split.gguf", buildGGUFTensors(3,
		[]ggufKV{strKV("general.architecture", "glm4")},
		[]ggufTensor{
			{name: "blk.0.ffn_up_exps.weight", dims: []uint64{196608}, typ: 0},
			{name: "token_embd.weight", dims: []uint64{65536}, typ: 0},
		}))
	metrics := modelMetrics{
		Path: path, Arch: "glm4", Quant: "F16",
		BlockCount: 47, LeadingDenseBlockCount: 1,
		ExpertCount: 48, ExpertUsedCount: 4,
		ExpertFFNLength: 512, EmbeddingLength: 4096,
		HeadCount: 8, HeadCountKV: 4, KeyLength: 128, ValueLength: 128,
	}

	// Split accepted: 786432 + 262144 == weightsBytes exactly.
	tm := buildTuneModel(metrics, true, 1048576)
	if tm.ExpertBytes != 786432 || tm.DenseBytes != 262144 {
		t.Errorf("tensor split not preferred: expert=%d dense=%d, want 786432/262144",
			tm.ExpertBytes, tm.DenseBytes)
	}
	if math.Abs(tm.ExpertUsedFrac-4.0/48.0) > 1e-9 {
		t.Errorf("ExpertUsedFrac = %g, want %g", tm.ExpertUsedFrac, 4.0/48.0)
	}

	// Same file declared at 32 GiB: the split misses by > 5% so the metadata
	// estimate takes over (F16: 2.0 * 46 layers * 48 experts * 3 * 512 * 4096).
	tm = buildTuneModel(metrics, true, 32<<30)
	if tm.ExpertBytes != 27783069696 {
		t.Errorf("estimate fallback expert = %d, want 27783069696", tm.ExpertBytes)
	}
	if want := int64(32<<30) - 27783069696; tm.DenseBytes != want {
		t.Errorf("estimate fallback dense = %d, want %d", tm.DenseBytes, want)
	}
}

// ─── KV cache estimation ─────────────────────────────────────────

// TestKVBytesPerTokenF16 verifies the three estimator branches: MLA latent,
// standard GQA/MHA geometry (with fallbacks), and the no-head-information
// conservative constant.
func TestKVBytesPerTokenF16(t *testing.T) {
	cases := []struct {
		name string
		m    modelMetrics
		want float64
	}{
		{"MLA with rope dims", modelMetrics{KVLoRaRank: 512, RopeDimCount: 64}, 1152},
		{"MLA without rope dims falls back to 64", modelMetrics{KVLoRaRank: 512}, 1152},
		{"standard GQA", modelMetrics{HeadCount: 8, HeadCountKV: 8, KeyLength: 128, ValueLength: 128}, 4096},
		{"no kv heads falls back to head count and 128 dims", modelMetrics{HeadCount: 8}, 4096},
		{"no head info at all", modelMetrics{}, 1024},
	}
	for _, c := range cases {
		if got := kvBytesPerTokenF16(c.m); math.Abs(got-c.want) > 1e-9 {
			t.Errorf("%s: kvBytesPerTokenF16 = %g, want %g", c.name, got, c.want)
		}
	}
}

// TestKVCacheLayers verifies hybrid-attention folding: only every Nth layer
// holds KV (ceil division), while interval <= 1 caches on all layers.
func TestKVCacheLayers(t *testing.T) {
	cases := []struct {
		m    modelMetrics
		want int
	}{
		{modelMetrics{BlockCount: 36, FullAttentionInterval: 3}, 12},
		{modelMetrics{BlockCount: 37, FullAttentionInterval: 3}, 13}, // ceil(37/3)
		{modelMetrics{BlockCount: 45}, 45},
		{modelMetrics{BlockCount: 45, FullAttentionInterval: 1}, 45},
	}
	for _, c := range cases {
		if got := kvCacheLayers(c.m); got != c.want {
			t.Errorf("kvCacheLayers(%+v) = %d, want %d", c.m, got, c.want)
		}
	}
}

// ─── buildTuneModel (KV folding consistency) ─────────────────────

// TestBuildTuneModelFallback verifies the conservative fallback used when the
// GGUF metadata is unreadable: 32 layers, 1024 bytes/layer/token, ctx 32768,
// all weights dense (no expert split without a parsed file).
func TestBuildTuneModelFallback(t *testing.T) {
	tm := buildTuneModel(modelMetrics{}, false, 4096<<20)
	if tm.Layers != 32 || tm.KVBytesPerTokPerLayerF16 != 1024 || tm.TrainCtx != 32768 {
		t.Errorf("fallback tuneModel = %+v, want layers=32 kv=1024 ctx=32768", tm)
	}
	if tm.WeightsBytes != 4096<<20 {
		t.Errorf("WeightsBytes = %d, want %d", tm.WeightsBytes, 4096<<20)
	}
	if tm.ExpertBytes != 0 || tm.DenseBytes != 4096<<20 {
		t.Errorf("fallback expert/dense = %d/%d, want 0/%d (treated as dense)",
			tm.ExpertBytes, tm.DenseBytes, 4096<<20)
	}
}

// TestBuildTuneModelHybridFold verifies the hybrid-attention KV folding:
// Layers stays the raw block count (per-layer weight math stays correct) while
// the per-layer KV cost is scaled by kvCacheLayers/BlockCount, so per-layer *
// Layers equals the true per-token KV total.
func TestBuildTuneModelHybridFold(t *testing.T) {
	metrics := modelMetrics{
		Arch: "qwen35", BlockCount: 36, ContextLength: 262144,
		HeadCount: 32, HeadCountKV: 8, KeyLength: 128, ValueLength: 128,
		FullAttentionInterval: 3,
	}
	tm := buildTuneModel(metrics, true, 8<<30)
	if tm.Layers != 36 {
		t.Errorf("Layers = %d, want raw block count 36", tm.Layers)
	}
	// Folded per-layer cost = 4096 * 12 / 36.
	wantPerLayer := 4096.0 * 12.0 / 36.0
	if math.Abs(tm.KVBytesPerTokPerLayerF16-wantPerLayer) > 1e-9 {
		t.Errorf("KVBytesPerTokPerLayerF16 = %g, want %g", tm.KVBytesPerTokPerLayerF16, wantPerLayer)
	}
	// Consistency: per-layer * Layers == per-layer(true) * kvCacheLayers.
	foldedTotal := tm.KVBytesPerTokPerLayerF16 * float64(tm.Layers)
	trueTotal := kvBytesPerTokenF16(metrics) * float64(kvCacheLayers(metrics))
	if math.Abs(foldedTotal-trueTotal) > 1e-6 {
		t.Errorf("folded KV total %g != true KV total %g", foldedTotal, trueTotal)
	}
	// No Path (hand-built metrics): no tensor split and no MoE metadata, so
	// the split chain degrades to all-dense.
	if tm.ExpertBytes != 0 || tm.DenseBytes != 8<<30 {
		t.Errorf("hybrid tuneModel expert/dense = %d/%d, want 0/%d",
			tm.ExpertBytes, tm.DenseBytes, 8<<30)
	}

	// Without hybrid attention the per-layer cost is unfolded.
	plain := buildTuneModel(modelMetrics{BlockCount: 36, HeadCount: 8, HeadCountKV: 8, KeyLength: 128, ValueLength: 128, ContextLength: 32768}, true, 0)
	if plain.KVBytesPerTokPerLayerF16 != 4096 {
		t.Errorf("non-hybrid per-layer KV = %g, want 4096", plain.KVBytesPerTokPerLayerF16)
	}
}

// ─── tuneModelConfig ─────────────────────────────────────────────

// TestTuneModelConfig is a table-driven check of the tuning rules with
// hand-verified numbers (all weight fixtures are exact MiB multiples):
//   - 4B  = 2765 MiB, 36 layers, 4096 B/layer/token KV, trained 262144
//   - 23B = 13824 MiB, 45 layers, 1152 B/layer/token KV (MLA), trained 131072
//   - 9B  = 5837 MiB, 40 layers, 4096 B/layer/token KV, trained 262144
//   - GLM-like MoE = 13435 MiB (11995 expert + 1440 dense), 47 layers,
//     1152 B/layer/token KV, trained 202752
//
// v2 ladder: [131072, 65536, 32768, 16384, 8192, 4096, 2048] filtered by
// TrainCtx; compute buffer 384 MB (<16384) / 640 MB (16384..65535) /
// 1152 MB (>=65536). Huge tiers (>=65536) additionally require the footprint
// to stay within budget * 0.92 (tuneHugeCtxSafetyRatio); the CPU-only branch
// plans against RAM and is unaffected by that margin.
//
// Hardware baseline: 8 physical cores / 16 logical, RAM 31 total / 18 free
// (usable RAM = min(18, 31*0.9)*0.85 = 15.3 GiB); NVIDIA 12 GB → 11776 MiB
// usable, RTX 5070 12227 MB → 11715 MiB, NVIDIA 8 GB → 7680 MiB, AMD 16 GB →
// 15684 MiB.
func TestTuneModelConfig(t *testing.T) {
	hwNvidia12 := tuneHardware{GPUVendor: vendorNvidia, VRAMMB: 12288, RAMTotalGB: 31, RAMFreeGB: 18, PhysicalCores: 8, LogicalCPUs: 16}
	hwRTX5070 := tuneHardware{GPUVendor: vendorNvidia, VRAMMB: 12227, RAMTotalGB: 31, RAMFreeGB: 18, PhysicalCores: 8, LogicalCPUs: 16}
	baseNone := tuneHardware{GPUVendor: vendorNone, RAMTotalGB: 31, RAMFreeGB: 18, PhysicalCores: 8, LogicalCPUs: 16}

	tm4B := tuneModel{WeightsBytes: 2765 << 20, Layers: 36, KVBytesPerTokPerLayerF16: 4096, TrainCtx: 262144}
	tm23B := tuneModel{WeightsBytes: 13824 << 20, Layers: 45, KVBytesPerTokPerLayerF16: 1152, TrainCtx: 131072}
	tm9B := tuneModel{WeightsBytes: 5837 << 20, Layers: 40, KVBytesPerTokPerLayerF16: 4096, TrainCtx: 262144}
	// GLM-4.7-Flash-REAP-23B-A3B-like fixture: 47-layer MoE whose expert
	// tensors (tensor-table measured) dominate the file.
	tmGLM := tuneModel{
		WeightsBytes: 13435 << 20, Layers: 47, KVBytesPerTokPerLayerF16: 1152,
		TrainCtx: 202752, ExpertBytes: 11995 << 20, DenseBytes: 1440 << 20,
	}
	// Small MoE that fully fits a 24 GB card (24064 MiB usable).
	tmSmallMoE := tuneModel{
		WeightsBytes: 5000 << 20, Layers: 47, KVBytesPerTokPerLayerF16: 1152,
		TrainCtx: 131072, ExpertBytes: 3000 << 20, DenseBytes: 2000 << 20,
	}
	// Cramped-full-offload MoE fixture for the measured-bandwidth flip:
	// on the 11776 MiB nvidia-12GB budget the full offload only fits at
	// ctx 4096 (f16@8192 = 11328+128+384 = 11840 and q8_0@8192 =
	// 11328+68+384 = 11780 both exceed 11776; f16@4096 = 11748 fits), so the
	// plan is cramped (4096 < tuneFullOffloadCrampedCtx). The MoE split
	// (9000 MiB experts fit the 15.3 GiB usable RAM, frac 0.25 → 2250 MiB
	// active bytes/token) lets the flip compare the plans via
	// estimateCPUMoeTPS.
	tmCrampedMoE := tuneModel{
		WeightsBytes: 11328 << 20, Layers: 32, KVBytesPerTokPerLayerF16: 512,
		TrainCtx: 131072, ExpertBytes: 9000 << 20, DenseBytes: 2328 << 20,
		ExpertUsedFrac: 0.25,
	}
	// Boundary fixture: weights 11264 MiB fit the same budget exactly at
	// ctx 8192 (f16@8192 = 11264+128+384 = 11776), so fullCtx is NOT cramped
	// and the flip must never engage.
	tmBoundaryMoE := tuneModel{
		WeightsBytes: 11264 << 20, Layers: 32, KVBytesPerTokPerLayerF16: 512,
		TrainCtx: 131072, ExpertBytes: 9000 << 20, DenseBytes: 2264 << 20,
		ExpertUsedFrac: 0.25,
	}

	cases := []struct {
		name        string
		hw          tuneHardware
		tm          tuneModel
		wantGPU     string
		wantCtx     int
		wantFlash   bool
		wantCacheK  string
		wantCacheV  string
		wantThreads int
		wantCPUMoe  bool
	}{
		{
			// f16: 2765+9216+1152 = 13133 > 11776 at 65536, fits 32768
			// (2765+4608+640 = 8013); q8_0 at 65536 = 2765+4896+1152 = 8813
			// fits, so B buys 65536 and wins.
			name: "4B on nvidia 12GB: f16 caps at 32768, q8_0 buys 65536 so B wins",
			hw:   hwNvidia12, tm: tm4B,
			wantGPU: "all", wantCtx: 65536, wantFlash: true, wantCacheK: "q8_0", wantCacheV: "q8_0", wantThreads: 8,
		},
		{
			name: "4B on nvidia 8GB: f16 caps at 16384, q8_0 buys 32768 so B wins",
			hw:   tuneHardware{GPUVendor: vendorNvidia, VRAMMB: 8192, RAMTotalGB: 31, RAMFreeGB: 18, PhysicalCores: 8, LogicalCPUs: 16}, tm: tm4B,
			wantGPU: "all", wantCtx: 32768, wantFlash: true, wantCacheK: "q8_0", wantCacheV: "q8_0", wantThreads: 8,
		},
		{
			// Dense 23B fixture (no expert split): weights 13824 > 11776 usable,
			// partial offload n=36 of 45 at ctx 8192.
			name: "23B dense fixture on nvidia 12GB: partial offload n=36 of 45 at ctx 8192",
			hw:   hwNvidia12, tm: tm23B,
			wantGPU: "36", wantCtx: 8192, wantFlash: true, wantCacheK: "", wantCacheV: "", wantThreads: 8,
		},
		{
			// CPU-only: 65536 needs 13824+3240 = 17064 > 15.3 GiB; 32768 fits
			// at 13824+1620 = 15444.
			name: "23B without GPU: CPU-only, largest ctx fitting usable RAM",
			hw:   baseNone, tm: tm23B,
			wantGPU: "0", wantCtx: 32768, wantFlash: false, wantCacheK: "", wantCacheV: "", wantThreads: 8,
		},
		{
			// AMD (no q8_0 plan): 65536 = 5837+10240+1152 = 17229 > 15684;
			// 32768 = 5837+5120+640 = 11597 fits.
			name: "9B on amd 16GB: full offload, flash-attn off (non-nvidia)",
			hw:   tuneHardware{GPUVendor: vendorAMD, VRAMMB: 16384, RAMTotalGB: 31, RAMFreeGB: 18, PhysicalCores: 8, LogicalCPUs: 16}, tm: tm9B,
			wantGPU: "all", wantCtx: 32768, wantFlash: false, wantCacheK: "", wantCacheV: "", wantThreads: 8,
		},
		{
			// 9B on nvidia 12GB: f16 65536 = 17229 and q8_0 65536 = 12429 both
			// exceed the raw 11776 budget, so both cap at 32768; the tie
			// prefers f16 even though the f16 plan leaves only 179 MiB headroom
			// (the huge-tier safety ratio never engages: 65536 never fit).
			name: "9B on nvidia 12GB: both plans cap at 32768, tie keeps f16",
			hw:   hwNvidia12, tm: tm9B,
			wantGPU: "all", wantCtx: 32768, wantFlash: true, wantCacheK: "", wantCacheV: "", wantThreads: 8,
		},
		{
			// Huge-tier safety margin: a tight 131072 f16 plan (8888+1536+1152
			// = 11576) fits the raw 11776 budget but exceeds the 0.92 cap
			// (10833.92), and the q8_0 131072 plan (8888+816+1152 = 10856) is
			// also over the cap, so both plans drop to 65536
			// (f16 10808 / q8_0 10448 <= cap) and the tie keeps f16.
			name: "huge-tier safety ratio drops a tight 131072 plan to 65536",
			hw:   hwNvidia12, tm: tuneModel{WeightsBytes: 8888 << 20, Layers: 12, KVBytesPerTokPerLayerF16: 1024, TrainCtx: 131072},
			wantGPU: "all", wantCtx: 65536, wantFlash: true, wantCacheK: "", wantCacheV: "", wantThreads: 8,
		},
		{
			// CPU-only ladder now reaches 65536: 2765+9216 = 11981 <= 15.3 GiB
			// (131072 would need 2765+18432 = 21197).
			name: "512MB iGPU is treated as no GPU even with nvidia vendor",
			hw:   tuneHardware{GPUVendor: vendorNvidia, VRAMMB: 512, RAMTotalGB: 31, RAMFreeGB: 18, PhysicalCores: 8, LogicalCPUs: 16}, tm: tm4B,
			wantGPU: "0", wantCtx: 65536, wantFlash: false, wantCacheK: "", wantCacheV: "", wantThreads: 8,
		},
		{
			name: "23B on nvidia 4GB: tiny partial offload (n=10), fields stay valid",
			hw:   tuneHardware{GPUVendor: vendorNvidia, VRAMMB: 4096, RAMTotalGB: 31, RAMFreeGB: 18, PhysicalCores: 8, LogicalCPUs: 16}, tm: tm23B,
			wantGPU: "10", wantCtx: 8192, wantFlash: true, wantCacheK: "", wantCacheV: "", wantThreads: 8,
		},
		{
			name: "conservative metadata fallback on nvidia 12GB: still a legal plan",
			hw:   hwNvidia12, tm: tuneModel{WeightsBytes: 4096 << 20, Layers: 32, KVBytesPerTokPerLayerF16: 1024, TrainCtx: 32768},
			wantGPU: "all", wantCtx: 32768, wantFlash: true, wantCacheK: "", wantCacheV: "", wantThreads: 8,
		},
		{
			name: "threads fall back to half the logical CPUs without physical cores",
			hw:   tuneHardware{GPUVendor: vendorNone, RAMTotalGB: 31, RAMFreeGB: 18, PhysicalCores: 0, LogicalCPUs: 16}, tm: tm4B,
			wantGPU: "0", wantCtx: 65536, wantFlash: false, wantCacheK: "", wantCacheV: "", wantThreads: 8,
		},
		{
			// GLM-like MoE on an RTX 5070: full offload impossible (13435 >
			// 11715) but experts fit 15.3 GiB RAM, so the cpu-moe plan sizes
			// the GPU side: dense 1440 + f16 KV 6768 + compute 1152 = 9360
			// <= 11715x0.92 = 10777.8 at ctx 131072 (huge-tier margin holds);
			// q8_0 also fits 131072, the tie keeps f16.
			name: "GLM MoE on RTX 5070: cpu-moe, all layers GPU, ctx 131072 f16, threads 0",
			hw:   hwRTX5070, tm: tmGLM,
			wantGPU: "all", wantCtx: 131072, wantFlash: true, wantCacheK: "", wantCacheV: "", wantThreads: 0, wantCPUMoe: true,
		},
		{
			// 8 GB RAM machine: usable RAM 4.25 GiB < 11995 MiB experts, the
			// cpu-moe plan is skipped and partial offload takes n=38 of 47 at
			// ctx 8192 (perLayer 13435/47+9 = 294.85, floor(11331/294.85) = 38).
			name: "GLM MoE with 8GB RAM: experts do not fit, partial offload n=38",
			hw:   tuneHardware{GPUVendor: vendorNvidia, VRAMMB: 12227, RAMTotalGB: 8, RAMFreeGB: 5, PhysicalCores: 8, LogicalCPUs: 16}, tm: tmGLM,
			wantGPU: "38", wantCtx: 8192, wantFlash: true, wantCacheK: "", wantCacheV: "", wantThreads: 8,
		},
		{
			// The whole model fits a 24 GB card: 131072 needs
			// 5000+6768+1152 = 12920 <= 24064x0.92 = 22138.88, so full
			// offload returns before the cpu-moe step is ever consulted.
			name: "small MoE on nvidia 24GB: full offload wins, cpu-moe never consulted",
			hw:   tuneHardware{GPUVendor: vendorNvidia, VRAMMB: 24576, RAMTotalGB: 31, RAMFreeGB: 18, PhysicalCores: 8, LogicalCPUs: 16}, tm: tmSmallMoE,
			wantGPU: "all", wantCtx: 131072, wantFlash: true, wantCacheK: "", wantCacheV: "", wantThreads: 8,
		},
		{
			// Measured-bandwidth flip (a): the full offload only fits at the
			// cramped ctx 4096, experts fit usable RAM, and 40 GB/s over
			// 2250 MiB active bytes/token estimates ~17 t/s >= 3.0, so the
			// cpu-moe plan wins instead: dense 2328 + KV 2048 + compute 1152
			// = 5528 <= 11776x0.92 at ctx 131072 (f16/q8_0 tie keeps f16).
			name: "cramped full offload + fast measured RAM flips to cpu-moe ctx 131072",
			hw:   tuneHardware{GPUVendor: vendorNvidia, VRAMMB: 12288, RAMTotalGB: 31, RAMFreeGB: 18, PhysicalCores: 8, LogicalCPUs: 16, RAMBandwidthGBs: 40}, tm: tmCrampedMoE,
			wantGPU: "all", wantCtx: 131072, wantFlash: true, wantCacheK: "", wantCacheV: "", wantThreads: 0, wantCPUMoe: true,
		},
		{
			// Measured-bandwidth flip (b): 5 GB/s over 2250 MiB active
			// bytes/token estimates ~2.1 t/s < 3.0 floor, so the cramped
			// full offload is retained exactly as without calibration.
			name: "cramped full offload + slow measured RAM keeps full offload",
			hw:   tuneHardware{GPUVendor: vendorNvidia, VRAMMB: 12288, RAMTotalGB: 31, RAMFreeGB: 18, PhysicalCores: 8, LogicalCPUs: 16, RAMBandwidthGBs: 5}, tm: tmCrampedMoE,
			wantGPU: "all", wantCtx: 4096, wantFlash: true, wantCacheK: "", wantCacheV: "", wantThreads: 8,
		},
		{
			// Measured-bandwidth flip (c): bandwidth 0 (no measurement) must
			// reproduce today's behavior byte-for-byte — same plan as the
			// slow-RAM case above; every pre-existing fixture also runs with
			// the zero value and keeps its original assertions.
			name: "RAMBandwidthGBs 0 keeps today's behavior (cramped full offload retained)",
			hw:   tuneHardware{GPUVendor: vendorNvidia, VRAMMB: 12288, RAMTotalGB: 31, RAMFreeGB: 18, PhysicalCores: 8, LogicalCPUs: 16, RAMBandwidthGBs: 0}, tm: tmCrampedMoE,
			wantGPU: "all", wantCtx: 4096, wantFlash: true, wantCacheK: "", wantCacheV: "", wantThreads: 8,
		},
		{
			// Measured-bandwidth flip (d): experts (9000 MiB) exceed the
			// usable RAM of an 8 GB machine (min(5, 7.2)x0.85 = 4.25 GiB),
			// so the flip never engages regardless of the fast measurement.
			name: "experts do not fit usable RAM: no flip despite fast RAM",
			hw:   tuneHardware{GPUVendor: vendorNvidia, VRAMMB: 12288, RAMTotalGB: 8, RAMFreeGB: 5, PhysicalCores: 8, LogicalCPUs: 16, RAMBandwidthGBs: 40}, tm: tmCrampedMoE,
			wantGPU: "all", wantCtx: 4096, wantFlash: true, wantCacheK: "", wantCacheV: "", wantThreads: 8,
		},
		{
			// Flip boundary: weights 11264 MiB fit the budget exactly at
			// ctx 8192, which is NOT cramped (< 8192 is the cramped rule),
			// so even a fast measurement keeps the full offload.
			name: "fullCtx exactly 8192 is not cramped: no flip with fast RAM",
			hw:   tuneHardware{GPUVendor: vendorNvidia, VRAMMB: 12288, RAMTotalGB: 31, RAMFreeGB: 18, PhysicalCores: 8, LogicalCPUs: 16, RAMBandwidthGBs: 40}, tm: tmBoundaryMoE,
			wantGPU: "all", wantCtx: 8192, wantFlash: true, wantCacheK: "", wantCacheV: "", wantThreads: 8,
		},
		{
			// Dense model with a fast measurement: ExpertBytes 0 yields no
			// t/s estimate, so the plan is identical to the pre-existing
			// first fixture (all/65536/q8_0/threads 8).
			name: "dense model never flips even with fast measured RAM",
			hw:   tuneHardware{GPUVendor: vendorNvidia, VRAMMB: 12288, RAMTotalGB: 31, RAMFreeGB: 18, PhysicalCores: 8, LogicalCPUs: 16, RAMBandwidthGBs: 40}, tm: tm4B,
			wantGPU: "all", wantCtx: 65536, wantFlash: true, wantCacheK: "q8_0", wantCacheV: "q8_0", wantThreads: 8,
		},
	}

	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			cfg := tuneModelConfig(c.hw, c.tm)

			// Invariants that must hold for every plan.
			if !validGPULayersValue(cfg.GPULayers) {
				t.Errorf("GPULayers %q must pass validGPULayersValue", cfg.GPULayers)
			}
			if cfg.CtxSize < tuneCtxMin {
				t.Errorf("CtxSize = %d, must be >= %d", cfg.CtxSize, tuneCtxMin)
			}
			if cfg.Threads < 0 {
				t.Errorf("Threads = %d, must be >= 0", cfg.Threads)
			}
			// Base fields from defaultModelConfig are preserved.
			if cfg.BatchSize != 2048 || cfg.UBatchSize != 512 {
				t.Errorf("BatchSize/UBatchSize = %d/%d, want defaults 2048/512", cfg.BatchSize, cfg.UBatchSize)
			}

			if cfg.GPULayers != c.wantGPU {
				t.Errorf("GPULayers = %q, want %q", cfg.GPULayers, c.wantGPU)
			}
			if cfg.CtxSize != c.wantCtx {
				t.Errorf("CtxSize = %d, want %d", cfg.CtxSize, c.wantCtx)
			}
			if cfg.FlashAttn != c.wantFlash {
				t.Errorf("FlashAttn = %v, want %v", cfg.FlashAttn, c.wantFlash)
			}
			if cfg.CacheTypeK != c.wantCacheK || cfg.CacheTypeV != c.wantCacheV {
				t.Errorf("CacheTypeK/V = %q/%q, want %q/%q", cfg.CacheTypeK, cfg.CacheTypeV, c.wantCacheK, c.wantCacheV)
			}
			if cfg.Threads != c.wantThreads {
				t.Errorf("Threads = %d, want %d", cfg.Threads, c.wantThreads)
			}
			if cfg.CPUMoe != c.wantCPUMoe {
				t.Errorf("CPUMoe = %v, want %v", cfg.CPUMoe, c.wantCPUMoe)
			}
		})
	}

	// Partial-offload layer count sanity for the 23B case: n must land in
	// [30, 37] per the task specification (exact value 36 asserted above).
	cfg := tuneModelConfig(hwNvidia12, tm23B)
	n, err := strconv.Atoi(cfg.GPULayers)
	if err != nil {
		t.Fatalf("partial GPULayers %q should be a plain integer: %v", cfg.GPULayers, err)
	}
	if n < 30 || n > 37 {
		t.Errorf("partial offload layers = %d, want within [30, 37]", n)
	}
	if n >= tm23B.Layers {
		t.Errorf("partial offload layers = %d, must stay below block count %d", n, tm23B.Layers)
	}
}

// TestEstimateCPUMoeTPS verifies the pure bandwidth-to-speed estimate. Unit
// semantics: ramBandwidthGBs is decimal GB/s (1e9 bytes/s, the benchbw
// calibration unit) and expertBytesPerToken is plain bytes, so
// t/s = bandwidth x 1e9 / bytesPerToken — bytes per second over bytes per
// token. Unusable inputs (non-positive on either side) return 0 = "no
// estimate" rather than an infinity.
func TestEstimateCPUMoeTPS(t *testing.T) {
	cases := []struct {
		name         string
		bandwidthGBs float64
		bytesPerTok  float64
		want         float64
	}{
		{"normal", 40, 2e9, 20}, // 40 GB/s over 2 GB of active experts per token
		{"fast RAM small experts", 60, 1.5e9, 40},
		{"slow RAM below the flip floor", 5, 2e9, 2.5},
		{"zero bandwidth", 0, 2e9, 0},
		{"negative bandwidth", -3, 2e9, 0},
		{"zero bytes per token", 40, 0, 0},
		{"negative bytes per token", 40, -1, 0},
	}
	for _, c := range cases {
		if got := estimateCPUMoeTPS(c.bandwidthGBs, c.bytesPerTok); math.Abs(got-c.want) > 1e-9 {
			t.Errorf("%s: estimateCPUMoeTPS(%g, %g) = %g, want %g",
				c.name, c.bandwidthGBs, c.bytesPerTok, got, c.want)
		}
	}
}

// TestTuneModelConfigHybridEndToEnd runs the folded hybrid-attention tuneModel
// through the planner: an 8 GiB model with 12/36 KV-bearing layers (49152 B
// per token) fully offloads at 32768 with the f16 cache. The q8_0 plan would
// fit 65536 under the raw budget (8192+1632+1152 = 10976 <= 11776, only 6.8%
// headroom) but the huge-tier safety ratio rejects it (10976 > 11776x0.92 =
// 10833.92), so B caps at 32768 too and the tie keeps f16. A dense hybrid
// model must never carry cpu-moe.
func TestTuneModelConfigHybridEndToEnd(t *testing.T) {
	hw := tuneHardware{GPUVendor: vendorNvidia, VRAMMB: 12288, RAMTotalGB: 31, RAMFreeGB: 18, PhysicalCores: 8, LogicalCPUs: 16}
	metrics := modelMetrics{
		Arch: "qwen35", BlockCount: 36, ContextLength: 262144,
		HeadCount: 32, HeadCountKV: 8, KeyLength: 128, ValueLength: 128,
		FullAttentionInterval: 3,
	}
	cfg := tuneModelConfig(hw, buildTuneModel(metrics, true, 8<<30))
	if cfg.GPULayers != "all" || cfg.CtxSize != 32768 || cfg.CacheTypeK != "" || !cfg.FlashAttn {
		t.Errorf("hybrid model tune = gpu %q ctx %d cache %q flash %v, want all/32768/f16/true",
			cfg.GPULayers, cfg.CtxSize, cfg.CacheTypeK, cfg.FlashAttn)
	}
	if cfg.CPUMoe {
		t.Error("dense hybrid model must not carry cpu-moe")
	}
}

// ─── Preset integration ──────────────────────────────────────────

// TestTuneModelConfigPresetIntegration feeds a tuned config through the real
// preset generator and asserts the INI gpu-layers / ctx-size lines match the
// config values (the tuned plan survives persistence into the server preset).
func TestTuneModelConfigPresetIntegration(t *testing.T) {
	hw := tuneHardware{GPUVendor: vendorNvidia, VRAMMB: 12288, RAMTotalGB: 31, RAMFreeGB: 18, PhysicalCores: 8, LogicalCPUs: 16}
	tm := tuneModel{WeightsBytes: 2765 << 20, Layers: 36, KVBytesPerTokPerLayerF16: 4096, TrainCtx: 262144}
	cfg := tuneModelConfig(hw, tm)

	models := []ModelInfo{{Name: "m", Path: "/models/m.gguf"}}
	path, err := generateModelsPresetFrom(models, map[string]ModelConfig{"m": cfg})
	if err != nil {
		t.Fatal(err)
	}
	defer os.Remove(path)

	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	content := string(data)
	wantLines := []string{
		fmt.Sprintf("gpu-layers = %s", cfg.GPULayers),
		fmt.Sprintf("ctx-size = %d", cfg.CtxSize),
		fmt.Sprintf("threads = %d", cfg.Threads),
	}
	for _, w := range wantLines {
		if !strings.Contains(content, w+"\n") {
			t.Errorf("preset missing %q: %q", w, content)
		}
	}
}

// TestTuneModelConfigPresetCPUMoe feeds a cpu-moe tuned plan through the real
// preset generator and asserts the INI carries cpu-moe = on while the zero
// thread count omits the threads line (llama-server auto-threads).
func TestTuneModelConfigPresetCPUMoe(t *testing.T) {
	hw := tuneHardware{GPUVendor: vendorNvidia, VRAMMB: 12227, RAMTotalGB: 31, RAMFreeGB: 18, PhysicalCores: 8, LogicalCPUs: 16}
	tm := tuneModel{
		WeightsBytes: 13435 << 20, Layers: 47, KVBytesPerTokPerLayerF16: 1152,
		TrainCtx: 202752, ExpertBytes: 11995 << 20, DenseBytes: 1440 << 20,
	}
	cfg := tuneModelConfig(hw, tm)
	if !cfg.CPUMoe {
		t.Fatal("expected a cpu-moe plan for the GLM-like fixture")
	}

	models := []ModelInfo{{Name: "m", Path: "/models/m.gguf"}}
	path, err := generateModelsPresetFrom(models, map[string]ModelConfig{"m": cfg})
	if err != nil {
		t.Fatal(err)
	}
	defer os.Remove(path)

	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	content := string(data)
	if !strings.Contains(content, "cpu-moe = on\n") {
		t.Errorf("preset missing \"cpu-moe = on\": %q", content)
	}
	if strings.Contains(content, "threads = ") {
		t.Errorf("threads=0 must omit the threads line: %q", content)
	}
	if !strings.Contains(content, fmt.Sprintf("ctx-size = %d\n", cfg.CtxSize)) {
		t.Errorf("preset missing ctx-size = %d", cfg.CtxSize)
	}
}

// TestTunePlanTarget verifies the tuner's GPU planning target: without a
// matching DeviceID the plan uses the largest-VRAM GPU (auto fallback); with a
// DeviceID matching one probed GPU by stable UUID (case-sensitive exact
// match), THAT card decides VRAMMB and vendor, keeping the plan consistent
// with the GPU the llama-server child is pinned to via CUDA_VISIBLE_DEVICES.
func TestTunePlanTarget(t *testing.T) {
	gpus := []GPUInfo{
		{Name: "NVIDIA GeForce RTX 5070 Ti", UUID: "GPU-aaaaaaaa-bbbb-cccc-dddd-eeeeeeeeeeee", MemoryMB: 16302},
		{Name: "NVIDIA GeForce RTX 3070", UUID: "GPU-11111111-2222-3333-4444-555555555555", MemoryMB: 8192},
	}

	// auto fallback (empty DeviceID): largest VRAM wins, machine-wide vendor
	got := tunePlanTarget(gpus, false, "")
	if got.VRAMMB != 16302 || got.Vendor != vendorNvidia || got.Name != "" || got.UUID != "" {
		t.Errorf("auto fallback = %+v, want largest VRAM 16302 / nvidia / no pinned card", got)
	}

	// selected: the smaller card is pinned by UUID → plan against it
	got = tunePlanTarget(gpus, false, "GPU-11111111-2222-3333-4444-555555555555")
	if got.VRAMMB != 8192 || got.Vendor != vendorNvidia || got.Name != "NVIDIA GeForce RTX 3070" {
		t.Errorf("selected card = %+v, want 8192 MB / nvidia / RTX 3070", got)
	}
	if got.UUID != "GPU-11111111-2222-3333-4444-555555555555" {
		t.Errorf("selected UUID = %q", got.UUID)
	}

	// unknown or case-mismatched DeviceID falls back to the largest-VRAM plan
	for _, id := range []string{"GPU-ffffffff-0000-0000-0000-000000000000", "gpu-11111111-2222-3333-4444-555555555555"} {
		if got := tunePlanTarget(gpus, false, id); got.Name != "" || got.VRAMMB != 16302 {
			t.Errorf("unmatched DeviceID %q must fall back to auto, got %+v", id, got)
		}
	}

	// empty GPU list: an available CUDA driver still promotes the vendor
	got = tunePlanTarget(nil, true, "")
	if got.VRAMMB != 0 || got.Vendor != vendorNvidia {
		t.Errorf("empty GPU list with CUDA = %+v, want 0 MB / nvidia", got)
	}
}

// TestTunePlanTargetApple verifies the Apple Silicon classification: a GPU
// carrying Vendor "apple" (the darwin probe's own classification) or an
// Apple-named legacy entry plans a metal target with no VRAM budget (unified
// memory has no discrete VRAM to report), and NVIDIA still wins the machine
// vote when a CUDA card is present alongside.
func TestTunePlanTargetApple(t *testing.T) {
	// Vendor-field classification, auto fallback
	got := tunePlanTarget([]GPUInfo{{Vendor: "apple", Name: "Apple M4 Pro"}}, false, "")
	if got.Vendor != vendorApple || got.VRAMMB != 0 {
		t.Errorf("apple vendor field = %+v, want apple / 0 MB", got)
	}
	// Legacy name-based fallback (no Vendor field)
	got = tunePlanTarget([]GPUInfo{{Name: "Apple M2 Max"}}, false, "")
	if got.Vendor != vendorApple {
		t.Errorf("apple name fallback = %+v, want apple", got)
	}
	// NVIDIA outranks apple in the machine-wide vote
	got = tunePlanTarget([]GPUInfo{
		{Vendor: "apple", Name: "Apple M4 Pro"},
		{Name: "NVIDIA GeForce RTX 3070", MemoryMB: 8192},
	}, false, "")
	if got.Vendor != vendorNvidia || got.VRAMMB != 8192 {
		t.Errorf("nvidia + apple = %+v, want nvidia / 8192 MB", got)
	}
}

// TestTuneModelConfigMetal pins the Apple Silicon plan: all layers offload
// (GPULayers="all"), the context is sized by the usable-RAM budget (unified
// memory), flash-attn stays off (no Metal benchmark path yet), the q8_0 cache
// pair is never written, and a MoE model still gets the plain metal plan —
// cpu-moe is a CUDA-centric split and must not appear (UsedCPUMoe=false, no
// bench chain). A darwin-amd64 host (CPU-only release, vendor none) plans the
// CPU-only branch even for a MoE model.
func TestTuneModelConfigMetal(t *testing.T) {
	// usableRAM = min(24, 32*0.90) * 0.85 GiB = 20.4 GiB = 20889.6 MiB
	hwApple := tuneHardware{GPUVendor: vendorApple, RAMTotalGB: 32, RAMFreeGB: 24, PhysicalCores: 10, LogicalCPUs: 10}

	// Dense 4B fixture: ctx 131072 needs 2765 + 18432 = 21197 MiB > 20889.6 →
	// caps at 65536 (2765 + 9216 = 11981 MiB, fits).
	tm4B := tuneModel{WeightsBytes: 2765 << 20, Layers: 36, KVBytesPerTokPerLayerF16: 4096, TrainCtx: 262144}
	cfg := tuneModelConfig(hwApple, tm4B)
	if cfg.GPULayers != "all" || cfg.CtxSize != 65536 {
		t.Errorf("dense on apple = gpuLayers %q ctx %d, want all / 65536", cfg.GPULayers, cfg.CtxSize)
	}
	if cfg.FlashAttn {
		t.Errorf("metal plan must keep flash-attn off (no benchmark path yet): %+v", cfg)
	}
	if cfg.CacheTypeK != "" || cfg.CacheTypeV != "" {
		t.Errorf("metal plan keeps the f16 cache, got K=%q V=%q", cfg.CacheTypeK, cfg.CacheTypeV)
	}
	if cfg.CPUMoe || cfg.NCpuMoe != 0 {
		t.Errorf("metal plan must not set cpu-moe flags: %+v", cfg)
	}
	if cfg.Threads != 10 {
		t.Errorf("threads = %d, want physical cores 10", cfg.Threads)
	}

	// MoE fixture on apple: the metal branch fires before any cpu-moe step.
	tmGLM := tuneModel{
		WeightsBytes: 13435 << 20, Layers: 47, KVBytesPerTokPerLayerF16: 1152,
		TrainCtx: 202752, ExpertBytes: 11995 << 20, DenseBytes: 1440 << 20,
	}
	// ctx 131072: 13435 + 6768 = 20203 MiB ≤ 20889.6 → fits.
	cfg = tuneModelConfig(hwApple, tmGLM)
	if cfg.GPULayers != "all" || cfg.CtxSize != 131072 {
		t.Errorf("MoE on apple = gpuLayers %q ctx %d, want all / 131072", cfg.GPULayers, cfg.CtxSize)
	}
	if cfg.CPUMoe || cfg.NCpuMoe != 0 {
		t.Errorf("MoE on apple must stay on the plain metal plan: %+v", cfg)
	}

	// darwin-amd64: the x64 release is CPU-only (no GPU entry at all → vendor
	// none) — a MoE model plans CPU-only, no cpu-moe flags.
	hwCPUOnly := tuneHardware{GPUVendor: vendorNone, RAMTotalGB: 32, RAMFreeGB: 24, PhysicalCores: 8, LogicalCPUs: 16}
	cfg = tuneModelConfig(hwCPUOnly, tmGLM)
	if cfg.GPULayers != "0" || cfg.CtxSize != 131072 {
		t.Errorf("darwin-amd64 CPU-only = gpuLayers %q ctx %d, want 0 / 131072", cfg.GPULayers, cfg.CtxSize)
	}
	if cfg.FlashAttn || cfg.CPUMoe || cfg.NCpuMoe != 0 {
		t.Errorf("darwin-amd64 plan must be plain CPU-only: %+v", cfg)
	}
}

// ─── Android big.LITTLE awareness ────────────────────────────────

// TestTuneModelConfigAndroidThreadCap verifies the big.LITTLE thread cap: on
// Android with a known performance-core split the worker pool is capped to the
// performance-cluster size; unknown splits, uniform topologies and desktop
// platforms keep the prior rule byte-for-byte.
func TestTuneModelConfigAndroidThreadCap(t *testing.T) {
	tm := tuneModel{WeightsBytes: 2 << 30, Layers: 32, KVBytesPerTokPerLayerF16: 1024, TrainCtx: 32768}
	base := tuneHardware{
		GPUVendor:       vendorNone,
		RAMTotalGB:      12,
		RAMFreeGB:       6,
		PhysicalCores:   8,
		LogicalCPUs:     8,
		AndroidPlatform: true,
	}

	// 8-core phone with a 4+4 split: GEMM workers stay on the big cluster.
	hw := base
	hw.PerfCores = 4
	if got := tuneModelConfig(hw, tm).Threads; got != 4 {
		t.Errorf("android 4+4 split: threads = %d, want 4", got)
	}

	// Unknown split (PerfCores 0): keeps the all-core behavior.
	hw.PerfCores = 0
	if got := tuneModelConfig(hw, tm).Threads; got != 8 {
		t.Errorf("android unknown split: threads = %d, want 8", got)
	}

	// Uniform topology (PerfCores == Cores): unchanged.
	hw.PerfCores = 8
	if got := tuneModelConfig(hw, tm).Threads; got != 8 {
		t.Errorf("android uniform: threads = %d, want 8", got)
	}

	// Desktop platforms never cap, whatever the cpufreq probe reported.
	hw.PerfCores = 4
	hw.AndroidPlatform = false
	if got := tuneModelConfig(hw, tm).Threads; got != 8 {
		t.Errorf("non-android: threads = %d, want 8", got)
	}
}

// TestTuneModelConfigPlatformPins verifies the platform-scoped launch pins:
// the Windows full-offload plan pins --threads 2 and disables context
// checkpoints (memory-bus contention, unsloth #5692; the app never calls the
// restore API), every Android plan disables context checkpoints (unified
// memory: each auto-checkpoint copies KV state into host RAM), and the
// cpu-moe / partial / Apple / non-Windows plans keep their own thread rules
// with both pins off.
func TestTuneModelConfigPlatformPins(t *testing.T) {
	// 4B dense fixture fully offloads the 12 GB NVIDIA budget (ctx 65536 via
	// the q8_0 plan), so the full-offload emission branch fires.
	tm4B := tuneModel{WeightsBytes: 2765 << 20, Layers: 36, KVBytesPerTokPerLayerF16: 4096, TrainCtx: 262144}
	// Cramped MoE fixture on the same budget only fits at ctx 4096; the fast
	// measured RAM bandwidth flips the plan to cpu-moe (experts fit RAM).
	tmCrampedMoE := tuneModel{
		WeightsBytes: 11328 << 20, Layers: 32, KVBytesPerTokPerLayerF16: 512,
		TrainCtx: 131072, ExpertBytes: 9000 << 20, DenseBytes: 2328 << 20,
		ExpertUsedFrac: 0.25,
	}

	// (1) Windows full offload: threads pinned to 2, checkpoints disabled.
	hw := tuneHardware{GPUVendor: vendorNvidia, VRAMMB: 12288, RAMTotalGB: 31, RAMFreeGB: 18, PhysicalCores: 8, LogicalCPUs: 16, WindowsPlatform: true}
	cfg := tuneModelConfig(hw, tm4B)
	if cfg.GPULayers != "all" {
		t.Fatalf("expected the full-offload plan, got GPULayers %q", cfg.GPULayers)
	}
	if cfg.Threads != 2 {
		t.Errorf("windows full offload: threads = %d, want 2", cfg.Threads)
	}
	if !cfg.CtxCheckpointsOff {
		t.Error("windows full offload: ctx checkpoints must be disabled")
	}

	// (2) Windows cramped-flip cpu-moe plan: keeps auto threads and the
	// checkpoint default — the pin is scoped to the full-offload branch.
	hw.RAMBandwidthGBs = 40
	cfg = tuneModelConfig(hw, tmCrampedMoE)
	if !cfg.CPUMoe {
		t.Fatalf("expected the cpu-moe flip plan, got %+v", cfg)
	}
	if cfg.Threads != 0 {
		t.Errorf("windows cpu-moe plan: threads = %d, want 0 (auto)", cfg.Threads)
	}
	if cfg.CtxCheckpointsOff {
		t.Error("windows cpu-moe plan must keep the checkpoint default")
	}

	// (3) Linux (all platform flags false) full offload: physical-core
	// threads and the checkpoint default are untouched.
	hw = tuneHardware{GPUVendor: vendorNvidia, VRAMMB: 12288, RAMTotalGB: 31, RAMFreeGB: 18, PhysicalCores: 8, LogicalCPUs: 16}
	cfg = tuneModelConfig(hw, tm4B)
	if cfg.GPULayers != "all" {
		t.Fatalf("expected the full-offload plan, got GPULayers %q", cfg.GPULayers)
	}
	if cfg.Threads != 8 {
		t.Errorf("non-windows full offload: threads = %d, want 8 (physical cores)", cfg.Threads)
	}
	if cfg.CtxCheckpointsOff {
		t.Error("non-windows full offload must keep the checkpoint default")
	}

	// (4) Android CPU-only plan: checkpoints always disabled (unified memory).
	hw = tuneHardware{GPUVendor: vendorNone, RAMTotalGB: 12, RAMFreeGB: 6, PhysicalCores: 8, LogicalCPUs: 8, AndroidPlatform: true}
	cfg = tuneModelConfig(hw, tm4B)
	if cfg.GPULayers != "0" {
		t.Fatalf("expected the CPU-only plan, got GPULayers %q", cfg.GPULayers)
	}
	if !cfg.CtxCheckpointsOff {
		t.Error("android plans must always disable ctx checkpoints")
	}

	// (5) Apple Metal plan: neither pin applies.
	hw = tuneHardware{GPUVendor: vendorApple, RAMTotalGB: 32, RAMFreeGB: 24, PhysicalCores: 10, LogicalCPUs: 10}
	cfg = tuneModelConfig(hw, tm4B)
	if cfg.GPULayers != "all" {
		t.Fatalf("expected the metal plan, got GPULayers %q", cfg.GPULayers)
	}
	if cfg.Threads != 10 {
		t.Errorf("apple plan: threads = %d, want 10 (physical cores)", cfg.Threads)
	}
	if cfg.CtxCheckpointsOff {
		t.Error("apple plan must keep the checkpoint default")
	}
}

// TestTuneNeedsRAMBandwidth verifies the calibration skip: the measured
// bandwidth only gates the CUDA-centric cpu-moe flip, so Apple Silicon (Metal
// plan) and Android (cpu-only plan) skip the benchmark while desktop GPU hosts
// keep running it.
func TestTuneNeedsRAMBandwidth(t *testing.T) {
	if !tuneNeedsRAMBandwidth(vendorNvidia, "windows") {
		t.Error("nvidia on windows should run the RAM bandwidth calibration")
	}
	if !tuneNeedsRAMBandwidth(vendorNvidia, "linux") {
		t.Error("nvidia on linux should run the RAM bandwidth calibration")
	}
	if !tuneNeedsRAMBandwidth(vendorNone, "windows") {
		t.Error("cpu-only desktop still runs the calibration (historical behavior)")
	}
	if tuneNeedsRAMBandwidth(vendorApple, "darwin") {
		t.Error("apple should skip the RAM bandwidth calibration")
	}
	if tuneNeedsRAMBandwidth(vendorNone, "android") {
		t.Error("android should skip the RAM bandwidth calibration")
	}
	if tuneNeedsRAMBandwidth(vendorNvidia, "android") {
		t.Error("android should skip the RAM bandwidth calibration regardless of vendor")
	}
}
