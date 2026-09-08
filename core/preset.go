package core

import (
	"bytes"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
)

// ─── Model preset generation ────────────────────────────────────
// Generates the llama-server INI preset from the scanned models and their
// per-model configs, with the INI value validation helpers.

// validIniValue validates values written into INI presets: no newlines/null
// bytes (prevents config injection) and no leading/trailing whitespace (avoids
// ambiguity from values being silently trimmed).
func validIniValue(s string) bool {
	return !strings.ContainsAny(s, "\n\r\x00") && s == strings.TrimSpace(s)
}

// validGPULayersValue validates gpu-layers values: empty, auto, all, 0, or a
// pure positive integer are allowed.
func validGPULayersValue(s string) bool {
	if s == "" || s == "auto" || s == "all" || s == "0" {
		return true
	}
	n, err := strconv.Atoi(s)
	return err == nil && n > 0
}

// validCacheTypeValue validates the cache-type-k/v whitelist (the list actually
// supported by b10342).
func validCacheTypeValue(s string) bool {
	switch s {
	case "", "f32", "f16", "bf16", "q8_0", "q4_0", "q4_1", "iq4_nl", "q5_0", "q5_1":
		return true
	}
	return false
}

// validLoadModeValue validates the load-mode whitelist (replaces
// mlock/no-mmap since b10342; empty means use llama-server's default mmap).
func validLoadModeValue(s string) bool {
	switch s {
	case "", "none", "mmap", "mlock", "mmap+mlock", "dio":
		return true
	}
	return false
}

// validSplitModeValue validates the split-mode whitelist (multi-GPU tensor
// split strategy).
func validSplitModeValue(s string) bool {
	switch s {
	case "", "none", "layer", "row", "tensor":
		return true
	}
	return false
}

// validRopeScalingValue validates the rope-scaling whitelist (long-context
// extrapolation strategy).
func validRopeScalingValue(s string) bool {
	switch s {
	case "", "none", "linear", "yarn":
		return true
	}
	return false
}

// validSpecTypeValue validates the spec-type whitelist: draft-mtp is MTP
// multi-token prediction; ngram-simple / ngram-mod are n-gram
// self-speculation (no draft model, zero extra VRAM). Empty means
// llama-server's default single-token prediction.
func validSpecTypeValue(s string) bool {
	switch s {
	case "", "draft-mtp", "ngram-simple", "ngram-mod":
		return true
	}
	return false
}

// presetKV is one ordered per-model preset entry shared by the INI writer and
// the Android direct-mode CLI serializer: key is the INI key (which is also
// the llama-server long-option name in direct mode) and value is the INI
// value verbatim (e.g. "true"/"on"/"off" for boolean entries). The
// boolean-flag convention: entries whose direct-mode form is a bare valueless
// flag (embeddings, cpu-moe) are recognized by key in modelDirectArgs — their
// KV value keeps the INI spelling so the INI serializer stays a dumb printer.
type presetKV struct {
	key   string
	value string
}

// modelPresetKV builds the ordered per-model preset entries for one scanned
// model: the single source consumed by both the INI preset writer
// (generateModelsPresetFrom) and the Android direct-mode CLI serializer
// (modelDirectArgs), so the two forms can never drift. The emitted keys and
// values are exactly the ones the INI writer has always produced; validation
// errors fire with the same messages as before. A zero ModelConfig (model
// without a config entry) yields only the model line plus auto-detected
// options.
func modelPresetKV(m ModelInfo, cfg ModelConfig) ([]presetKV, error) {
	kvs := make([]presetKV, 0, 24)
	kvs = append(kvs, presetKV{key: "model", value: filepath.ToSlash(m.Path)})

	// Auto-detect embedding model from name or architecture
	if isEmbeddingModel(m) {
		kvs = append(kvs, presetKV{key: "embeddings", value: "true"})
	}

	// With an explicit mmproj path override, skip the same-directory
	// auto-detection below (avoids emitting two mmproj entries)
	explicitMMProj := false

	// Apply per-model config if set (a zero cfg skips every branch below)
	{
		if cfg.CtxSize > 0 {
			kvs = append(kvs, presetKV{key: "ctx-size", value: strconv.Itoa(cfg.CtxSize)})
		}
		if cfg.BatchSize > 0 {
			kvs = append(kvs, presetKV{key: "batch-size", value: strconv.Itoa(cfg.BatchSize)})
		}
		if cfg.UBatchSize > 0 {
			kvs = append(kvs, presetKV{key: "ubatch-size", value: strconv.Itoa(cfg.UBatchSize)})
		}
		if cfg.Threads > 0 {
			kvs = append(kvs, presetKV{key: "threads", value: strconv.Itoa(cfg.Threads)})
		}
		// Context checkpoints: llama-server auto-creates them during prompt
		// processing (each copies KV state into host RAM, upstream default 32)
		// and the app never calls the restore API, so CtxCheckpointsOff pins
		// ctx-checkpoints = 0. The server prompt cache (cache-ram) is a
		// different mechanism and is deliberately untouched here.
		if cfg.CtxCheckpointsOff {
			kvs = append(kvs, presetKV{key: "ctx-checkpoints", value: "0"})
		}
		if cfg.GPULayers != "" && cfg.GPULayers != "auto" {
			if !validIniValue(cfg.GPULayers) {
				return nil, fmt.Errorf(tr("非法 GPULayers 值 %q：不能包含换行或首尾空白", "invalid GPULayers value %q: must not contain newlines or leading/trailing whitespace"), cfg.GPULayers)
			}
			kvs = append(kvs, presetKV{key: "gpu-layers", value: cfg.GPULayers})
		}
		if cfg.FlashAttn {
			kvs = append(kvs, presetKV{key: "flash-attn", value: "on"})
		}
		if cfg.CacheTypeK != "" {
			if !validIniValue(cfg.CacheTypeK) {
				return nil, fmt.Errorf(tr("非法 CacheTypeK 值 %q：不能包含换行或首尾空白", "invalid CacheTypeK value %q: must not contain newlines or leading/trailing whitespace"), cfg.CacheTypeK)
			}
			kvs = append(kvs, presetKV{key: "cache-type-k", value: cfg.CacheTypeK})
		}
		if cfg.CacheTypeV != "" {
			if !validIniValue(cfg.CacheTypeV) {
				return nil, fmt.Errorf(tr("非法 CacheTypeV 值 %q：不能包含换行或首尾空白", "invalid CacheTypeV value %q: must not contain newlines or leading/trailing whitespace"), cfg.CacheTypeV)
			}
			kvs = append(kvs, presetKV{key: "cache-type-v", value: cfg.CacheTypeV})
		}
		// New params below take effect since b10342. LoadMode/SplitMode/
		// RopeScaling are omitted when empty or equal to llama-server's
		// default to avoid noise; MLock/NoMMap are deprecated — loadConfig
		// migrates them into LoadMode and they are never written directly
		// into the preset anymore.
		if cfg.LoadMode != "" && cfg.LoadMode != "mmap" {
			if !validIniValue(cfg.LoadMode) {
				return nil, fmt.Errorf(tr("非法 LoadMode 值 %q：不能包含换行或首尾空白", "invalid LoadMode value %q: must not contain newlines or leading/trailing whitespace"), cfg.LoadMode)
			}
			kvs = append(kvs, presetKV{key: "load-mode", value: cfg.LoadMode})
		}
		if cfg.CPUMoe {
			kvs = append(kvs, presetKV{key: "cpu-moe", value: "on"})
		}
		if cfg.NCpuMoe > 0 {
			kvs = append(kvs, presetKV{key: "n-cpu-moe", value: strconv.Itoa(cfg.NCpuMoe)})
		}
		if cfg.SplitMode != "" && cfg.SplitMode != "layer" {
			if !validIniValue(cfg.SplitMode) {
				return nil, fmt.Errorf(tr("非法 SplitMode 值 %q：不能包含换行或首尾空白", "invalid SplitMode value %q: must not contain newlines or leading/trailing whitespace"), cfg.SplitMode)
			}
			kvs = append(kvs, presetKV{key: "split-mode", value: cfg.SplitMode})
		}
		if cfg.TensorSplit != "" {
			if !validIniValue(cfg.TensorSplit) {
				return nil, fmt.Errorf(tr("非法 TensorSplit 值 %q：不能包含换行或首尾空白", "invalid TensorSplit value %q: must not contain newlines or leading/trailing whitespace"), cfg.TensorSplit)
			}
			kvs = append(kvs, presetKV{key: "tensor-split", value: cfg.TensorSplit})
		}
		if cfg.MainGPU > 0 {
			kvs = append(kvs, presetKV{key: "main-gpu", value: strconv.Itoa(cfg.MainGPU)})
		}
		if cfg.RopeScaling != "" && cfg.RopeScaling != "none" {
			if !validIniValue(cfg.RopeScaling) {
				return nil, fmt.Errorf(tr("非法 RopeScaling 值 %q：不能包含换行或首尾空白", "invalid RopeScaling value %q: must not contain newlines or leading/trailing whitespace"), cfg.RopeScaling)
			}
			kvs = append(kvs, presetKV{key: "rope-scaling", value: cfg.RopeScaling})
		}
		if cfg.RopeScale > 0 {
			kvs = append(kvs, presetKV{key: "rope-scale", value: fmt.Sprintf("%g", cfg.RopeScale)})
		}
		// Explicit mmproj path override: when non-empty and passing INI
		// injection validation it takes priority over same-directory
		// auto-detection; file existence is not required (the model may
		// have been moved; llama-server reports the error at startup).
		if cfg.MMProj != "" {
			if !validIniValue(cfg.MMProj) {
				return nil, fmt.Errorf(tr("非法 MMProj 值 %q：不能包含换行或首尾空白", "invalid MMProj value %q: must not contain newlines or leading/trailing whitespace"), cfg.MMProj)
			}
			kvs = append(kvs, presetKV{key: "mmproj", value: filepath.ToSlash(cfg.MMProj)})
			explicitMMProj = true
		}
		if cfg.Reasoning {
			kvs = append(kvs, presetKV{key: "reasoning", value: "off"})
		}
		if cfg.SpecType != "" {
			if !validSpecTypeValue(cfg.SpecType) {
				return nil, fmt.Errorf(tr("非法 SpecType 值 %q：仅允许 draft-mtp / ngram-simple / ngram-mod", "invalid SpecType value %q: only draft-mtp / ngram-simple / ngram-mod"), cfg.SpecType)
			}
			kvs = append(kvs, presetKV{key: "spec-type", value: cfg.SpecType})
		}
		if cfg.SpecDraftNMax > 0 {
			kvs = append(kvs, presetKV{key: "spec-draft-n-max", value: strconv.Itoa(cfg.SpecDraftNMax)})
		}
		// LoRA adapters: enabled refs serialize into the single lora-scaled
		// entry (upstream CSV form FNAME:SCALE,...; --lora-scaled is a
		// registered option so the INI key maps 1:1, see common/preset.cpp
		// key_to_opt). Values are bare file names relative to the LoRA
		// directory — llama-server is spawned with that directory as its
		// working directory (buildServerCommand), keeping Windows
		// drive-letter colons out of the FNAME:SCALE colon-splitting.
		if len(cfg.LoraAdapters) > 0 {
			v, err := loraScaledValue(cfg)
			if err != nil {
				return nil, err
			}
			if v != "" {
				kvs = append(kvs, presetKV{key: "lora-scaled", value: v})
			}
		}
	}
	if m.HasMMProj && !explicitMMProj {
		// Look for mmproj file in same directory
		dir := filepath.Dir(m.Path)
		entries, _ := os.ReadDir(dir)
		for _, e := range entries {
			if strings.HasPrefix(strings.ToLower(e.Name()), "mmproj") && strings.HasSuffix(strings.ToLower(e.Name()), ".gguf") {
				kvs = append(kvs, presetKV{key: "mmproj", value: filepath.ToSlash(filepath.Join(dir, e.Name()))})
				break
			}
		}
	}
	return kvs, nil
}

// generateModelsPreset scans the default model directory and writes a llama-server
// INI preset to a temp file, returning its path.
func generateModelsPreset() (string, error) {
	models := scanModels()
	if len(models) == 0 {
		return "", errors.New(tr("LLM-Models 目录中没有模型", "no models found in the LLM-Models directory"))
	}
	modelConfigsMu.Lock()
	cfgs := cachedModelConfigs
	modelConfigsMu.Unlock()
	return generateModelsPresetFrom(models, cfgs)
}

// generateModelsPresetFrom writes a llama-server INI preset for the given models
// and per-model configs to a temp file and returns its path. Models without a
// matching config entry only emit the model path (plus auto-detected options).
func generateModelsPresetFrom(models []ModelInfo, cfgs map[string]ModelConfig) (string, error) {
	if len(models) == 0 {
		return "", errors.New(tr("LLM-Models 目录中没有模型", "no models found in the LLM-Models directory"))
	}

	var buf bytes.Buffer
	// Deterministic alias dedup: sanitizeAlias maps different characters like
	// spaces and slashes all to '-', so distinct model names can collide into
	// the same section name (#7.1). Aliases preserve the display name's casing
	// (llama-server matches model ids case-sensitively) — what the UI shows is
	// exactly the id the API accepts; aliasDedup appends -2, -3... to
	// case-insensitive collisions in model order until unique. The result is
	// deterministic, independent of randomness/time, and identical to the
	// aliases assigned by scanModels for the same model order.
	used := make(map[string]int)
	for _, m := range models {
		alias := aliasDedup(m.Name, used)
		buf.WriteString(fmt.Sprintf("[%s]\n", alias))
		// A model without a config entry gets the zero ModelConfig: only the
		// model line (plus auto-detected options) is emitted.
		kvs, err := modelPresetKV(m, cfgs[m.Name])
		if err != nil {
			return "", err
		}
		for _, kv := range kvs {
			buf.WriteString(fmt.Sprintf("%s = %s\n", kv.key, kv.value))
		}
		buf.WriteString("\n")
	}

	tmpFile, err := os.CreateTemp(resolveTempDir(), "llama-models-*.ini")
	if err != nil {
		return "", err
	}
	path := tmpFile.Name()
	if _, err := tmpFile.Write(buf.Bytes()); err != nil {
		tmpFile.Close()
		return "", err
	}
	tmpFile.Close()
	return path, nil
}

func isEmbeddingModel(m ModelInfo) bool {
	lower := strings.ToLower(m.Name + " " + m.Architecture)
	return strings.Contains(lower, "embedding") || strings.Contains(lower, "embd") ||
		strings.Contains(lower, "all-minilm") || strings.Contains(lower, "bge-") ||
		strings.Contains(lower, "gte-") || strings.Contains(lower, "e5-")
}

// modelDirectArgs serializes the same per-model entries the INI preset writer
// emits (modelPresetKV) into llama-server direct-mode command-line arguments:
// single-model serving with no INI file, one process, one model. The alias is
// the sanitized display name (sanitizeAlias); direct mode serves exactly one
// model, so no alias-dedup suffix is needed. Option forms verified against
// upstream b10698 common/arg.cpp (gpu-layers accepts auto/all/int verbatim,
// load-mode passes the enum through, embeddings/cpu-moe are bare flags).
func modelDirectArgs(alias string, m ModelInfo, cfg ModelConfig) ([]string, error) {
	kvs, err := modelPresetKV(m, cfg)
	if err != nil {
		return nil, err
	}
	// Android official builds are CPU-only (arm64, no GPU backend in the
	// package): GPU-only flags from persisted desktop-era configs must never
	// reach the direct-mode command line. The INI writer (desktop-only) stays
	// byte-identical — the filter applies to this serializer only.
	androidCPUOnly := platformGOOS == "android"
	args := []string{"--alias", alias}
	for _, kv := range kvs {
		if androidCPUOnly {
			switch kv.key {
			case "flash-attn", "cpu-moe", "n-cpu-moe":
				// GPU-only flags: the android build rejects them outright
				// (llama-server: no GPU backend compiled in).
				continue
			}
		}
		switch kv.key {
		case "model":
			args = append(args, "-m", kv.value)
		case "embeddings", "cpu-moe":
			// Bare valueless flags upstream: --embeddings / --cpu-moe.
			args = append(args, "--"+kv.key)
		case "reasoning":
			// Upstream has no reasoning option at b10698; its INI parser
			// silently ignores unknown keys, so this entry is a no-op on
			// every platform today. Direct mode skips it entirely (desktop
			// INI output is unchanged).
			continue
		default:
			// Same-name long option with the INI value verbatim.
			args = append(args, "--"+kv.key, kv.value)
		}
	}
	return args, nil
}

// sanitizeAlias maps a display name to a llama-server INI section name: spaces
// become hyphens, characters outside [A-Za-z0-9-_.] are replaced with hyphens.
// Casing is preserved so the model id equals what the UI displays — users
// copy-paste the shown name and llama-server (case-sensitive lookup) matches.
func sanitizeAlias(name string) string {
	name = strings.ReplaceAll(name, " ", "-")
	return strings.Map(func(r rune) rune {
		if (r >= 'a' && r <= 'z') || (r >= 'A' && r <= 'Z') || (r >= '0' && r <= '9') || r == '-' || r == '_' || r == '.' {
			return r
		}
		return '-'
	}, name)
}

// aliasDedup returns a section-name-unique alias for name: the first
// occurrence keeps the sanitized name as-is; later names colliding with it
// case-insensitively get -2, -3... appended in order. The used map is keyed by
// the lowercased alias so two sections can never differ only by casing (both
// would be valid INI sections but ambiguous as copy-paste ids). Deterministic
// for a fixed input order.
func aliasDedup(name string, used map[string]int) string {
	alias := sanitizeAlias(name)
	if n := used[strings.ToLower(alias)]; n > 0 {
		for i := n + 1; ; i++ {
			candidate := fmt.Sprintf("%s-%d", alias, i)
			if used[strings.ToLower(candidate)] == 0 {
				alias = candidate
				break
			}
		}
	}
	used[strings.ToLower(alias)]++
	return alias
}
