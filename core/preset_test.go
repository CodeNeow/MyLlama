package core

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// TestGenerateModelsPresetFrom verifies preset INI generation: section names use the
// alias (sanitized model name with display casing preserved), model lines use
// forward-slash paths, and embedding models automatically get embeddings=true.
func TestGenerateModelsPresetFrom(t *testing.T) {
	// construct paths via filepath.Join (Windows produces backslashes, Unix produces
	// forward slashes); assert INI model lines equal filepath.ToSlash(path), cross-platform
	// verification that "model paths use forward slashes".
	models := []ModelInfo{
		{Name: "Qwen2.5 7B", Path: filepath.Join("models", "qwen", "model.gguf")},
		{Name: "bge-small-zh", Path: filepath.Join("models", "bge", "model.gguf")},
	}
	path, err := generateModelsPresetFrom(models, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer os.Remove(path)

	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	content := string(data)

	if !strings.Contains(content, "[Qwen2.5-7B]\n") {
		t.Errorf("missing Qwen2.5-7B section: %q", content)
	}
	if !strings.Contains(content, "model = "+filepath.ToSlash(models[0].Path)+"\n") {
		t.Errorf("model path should use forward slashes: %q", content)
	}
	if !strings.Contains(content, "[bge-small-zh]\n") || !strings.Contains(content, "embeddings = true\n") {
		t.Errorf("embedding model should output embeddings=true: %q", content)
	}
}

// TestGenerateModelsPresetFromConfigs verifies per-model parameters are fully written
// into the preset.
func TestGenerateModelsPresetFromConfigs(t *testing.T) {
	models := []ModelInfo{{Name: "deepseek-r1", Path: "/models/deepseek.gguf"}}
	cfgs := map[string]ModelConfig{
		"deepseek-r1": {
			Threads: 8, GPULayers: "99", CtxSize: 8192, BatchSize: 512,
			UBatchSize: 256, FlashAttn: true, CacheTypeK: "q8_0", CacheTypeV: "q8_0",
			LoadMode: "mlock",
		},
	}
	path, err := generateModelsPresetFrom(models, cfgs)
	if err != nil {
		t.Fatal(err)
	}
	defer os.Remove(path)

	data, _ := os.ReadFile(path)
	content := string(data)
	wantLines := []string{
		"ctx-size = 8192",
		"batch-size = 512",
		"ubatch-size = 256",
		"threads = 8",
		"gpu-layers = 99",
		"flash-attn = on",
		"cache-type-k = q8_0",
		"cache-type-v = q8_0",
		"load-mode = mlock",
	}
	for _, w := range wantLines {
		if !strings.Contains(content, w+"\n") {
			t.Errorf("preset missing %q: %q", w, content)
		}
	}
	if strings.Contains(content, "no-mmap") {
		t.Errorf("NoMMap=false should not output no-mmap: %q", content)
	}
}

// TestGenerateModelsPresetFromNoConfig verifies a model with no configured parameters
// only outputs a model line.
func TestGenerateModelsPresetFromNoConfig(t *testing.T) {
	models := []ModelInfo{{Name: "plain", Path: "/models/plain.gguf"}}
	path, err := generateModelsPresetFrom(models, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer os.Remove(path)

	data, _ := os.ReadFile(path)
	content := string(data)
	if content != "[plain]\nmodel = /models/plain.gguf\n\n" {
		t.Errorf("preset content does not match expected: %q", content)
	}
}

// TestGenerateModelsPresetFromEmpty verifies an empty model list returns an error.
func TestGenerateModelsPresetFromEmpty(t *testing.T) {
	if _, err := generateModelsPresetFrom(nil, nil); err == nil {
		t.Error("empty model list should return error")
	}
}

// TestGenerateModelsPresetFromMMProj verifies a multimodal model outputs an mmproj line.
func TestGenerateModelsPresetFromMMProj(t *testing.T) {
	dir := t.TempDir()
	mmprojPath := filepath.Join(dir, "mmproj-f16.gguf")
	if err := os.WriteFile(mmprojPath, []byte("proj"), 0644); err != nil {
		t.Fatal(err)
	}
	modelPath := filepath.Join(dir, "llava.gguf")
	if err := os.WriteFile(modelPath, []byte("model"), 0644); err != nil {
		t.Fatal(err)
	}

	models := []ModelInfo{{Name: "llava", Path: modelPath, HasMMProj: true}}
	path, err := generateModelsPresetFrom(models, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer os.Remove(path)

	data, _ := os.ReadFile(path)
	content := string(data)
	if !strings.Contains(content, "mmproj = "+filepath.ToSlash(mmprojPath)) {
		t.Errorf("preset missing mmproj line: %q", content)
	}
}

// TestGenerateModelsPresetFromRejectsInjection verifies generateModelsPresetFrom
// returns errors for GPULayers / CacheType values containing newlines or leading/trailing
// whitespace (#9 second defense layer).
// This function is a pure function that directly writes INI text; if values carry newlines,
// arbitrary sections/keys can be injected.
func TestGenerateModelsPresetFromRejectsInjection(t *testing.T) {
	models := []ModelInfo{{Name: "m", Path: "/models/m.gguf"}}
	badCfgs := []map[string]ModelConfig{
		{"m": {GPULayers: "99\n[evil]\nmodel=/tmp/x"}},
		{"m": {CacheTypeK: "q8_0\nfoo"}},
		{"m": {CacheTypeV: "f16\nbar"}},
		{"m": {GPULayers: " 99 "}}, // leading/trailing whitespace rejected
	}
	for i, cfgs := range badCfgs {
		if _, err := generateModelsPresetFrom(models, cfgs); err == nil {
			t.Errorf("case %d: illegal values should return error", i)
		}
	}
}

// TestGenerateModelsPresetFromAliasDedup verifies alias deduplication (#7.1): sanitizeAlias
// unifies spaces/slashes into '-', so different model names may collide case-insensitively
// into the same section name. The first occurrence keeps its natural form; later
// collisions get -2, -3… appended in model order until unique (case-fold keying, so two
// sections can never differ only by casing). The result is deterministic.
func TestGenerateModelsPresetFromAliasDedup(t *testing.T) {
	models := []ModelInfo{
		{Name: "Model v1", Path: "/models/a.gguf"},
		{Name: "Model/v1", Path: "/models/b.gguf"}, // case-fold collision → Model-v1-2
		{Name: "Model-V1", Path: "/models/c.gguf"}, // another collision → Model-V1-3
	}
	path, err := generateModelsPresetFrom(models, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer os.Remove(path)

	data, _ := os.ReadFile(path)
	content := string(data)

	// three models must each have a unique section name, and each must contain its own model path
	if !strings.Contains(content, "[Model-v1]\nmodel = /models/a.gguf") {
		t.Errorf("first model section should be Model-v1: %q", content)
	}
	if !strings.Contains(content, "[Model-v1-2]\nmodel = /models/b.gguf") {
		t.Errorf("second model section should be Model-v1-2: %q", content)
	}
	if !strings.Contains(content, "[Model-V1-3]\nmodel = /models/c.gguf") {
		t.Errorf("third model section should be Model-V1-3: %q", content)
	}
}

// TestAliasDedup verifies the pure dedup helper: the first name keeps its natural
// sanitized form regardless of casing, case-insensitive collisions get -2/-3 suffixes
// in order, and distinct names never interfere.
func TestAliasDedup(t *testing.T) {
	used := make(map[string]int)
	if got := aliasDedup("Qwen2.5 7B", used); got != "Qwen2.5-7B" {
		t.Errorf("first alias = %q, want Qwen2.5-7B (natural casing kept)", got)
	}
	if got := aliasDedup("qwen2.5/7b", used); got != "qwen2.5-7b-2" {
		t.Errorf("case-fold collision alias = %q, want qwen2.5-7b-2", got)
	}
	if got := aliasDedup("DeepSeek-R1", used); got != "DeepSeek-R1" {
		t.Errorf("distinct name alias = %q, want DeepSeek-R1 (unaffected)", got)
	}
	if got := aliasDedup("Qwen2.5-7B", used); got != "Qwen2.5-7B-3" {
		t.Errorf("third collision alias = %q, want Qwen2.5-7B-3", got)
	}
}

// TestGenerateModelsPresetFromAliasPreservesCasing verifies every preset section alias
// preserves the display name's casing (llama-server matches the OpenAI-API model field
// against section names case-sensitively): the id users copy from the UI — Models page
// display name, API page tags, chat picker via GET /models — is exactly the id the API
// accepts. Exactly one section per model.
func TestGenerateModelsPresetFromAliasPreservesCasing(t *testing.T) {
	models := []ModelInfo{
		{Name: "Qwen2.5 7B", Path: "/models/a.gguf"},
		{Name: "DeepSeek-R1-Distill-Qwen-7B", Path: "/models/b.gguf"},
	}
	path, err := generateModelsPresetFrom(models, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer os.Remove(path)

	data, _ := os.ReadFile(path)
	content := string(data)

	for _, want := range []string{"[Qwen2.5-7B]\n", "[DeepSeek-R1-Distill-Qwen-7B]\n"} {
		if !strings.Contains(content, want) {
			t.Errorf("preset missing display-cased section %q: %q", want, content)
		}
	}
	// exactly one section per model, no case variants of the same model
	sections := 0
	for _, line := range strings.Split(content, "\n") {
		if strings.HasPrefix(line, "[") {
			sections++
		}
	}
	if sections != len(models) {
		t.Errorf("expected exactly %d sections, got %d: %q", len(models), sections, content)
	}
}

// TestGenerateModelsPresetFromAcceptsValidValues verifies valid values (empty / auto /
// all / 0 / positive integers / cache whitelist) generate successfully (#9 control group).
func TestGenerateModelsPresetFromAcceptsValidValues(t *testing.T) {
	models := []ModelInfo{{Name: "m", Path: "/models/m.gguf"}}
	cfgs := map[string]ModelConfig{
		"m": {GPULayers: "auto", CacheTypeK: "q8_0", CacheTypeV: "bf16"},
	}
	path, err := generateModelsPresetFrom(models, cfgs)
	if err != nil {
		t.Fatal(err)
	}
	defer os.Remove(path)

	data, _ := os.ReadFile(path)
	content := string(data)
	if !strings.Contains(content, "cache-type-k = q8_0\n") || !strings.Contains(content, "cache-type-v = bf16\n") {
		t.Errorf("valid cache types not written: %q", content)
	}
	if strings.Contains(content, "gpu-layers") {
		t.Errorf("GPULayers=auto should not output gpu-layers line: %q", content)
	}
}

// TestGenerateModelsPresetNewFields verifies b10342 new fields are written to preset INI:
// all non-default values are output line-by-line; legacy mlock/noMmap compatibility
// fields are no longer written directly (post-migration LoadMode takes over), preventing
// deprecated keys from re-entering presets.
func TestGenerateModelsPresetNewFields(t *testing.T) {
	models := []ModelInfo{{Name: "m", Path: "/models/m.gguf"}}

	t.Run("all new fields", func(t *testing.T) {
		cfgs := map[string]ModelConfig{
			"m": {
				LoadMode: "mlock", CPUMoe: true, NCpuMoe: 2, SplitMode: "row",
				TensorSplit: "3,1", MainGPU: 1, RopeScaling: "yarn", RopeScale: 2.0,
			},
		}
		path, err := generateModelsPresetFrom(models, cfgs)
		if err != nil {
			t.Fatal(err)
		}
		defer os.Remove(path)

		data, _ := os.ReadFile(path)
		content := string(data)
		wantLines := []string{
			"load-mode = mlock",
			"cpu-moe = on",
			"n-cpu-moe = 2",
			"split-mode = row",
			"tensor-split = 3,1",
			"main-gpu = 1",
			"rope-scaling = yarn",
			"rope-scale = 2",
		}
		for _, w := range wantLines {
			if !strings.Contains(content, w+"\n") {
				t.Errorf("preset missing %q: %q", w, content)
			}
		}
	})

	t.Run("defaults not written", func(t *testing.T) {
		// LoadMode=mmap/empty, SplitMode=layer/empty, MainGPU=0, RopeScale=0,
		// CPUMoe=false etc. defaults must not produce keys, avoiding preset noise.
		for _, lm := range []string{"", "mmap"} {
			for _, sm := range []string{"", "layer"} {
				cfgs := map[string]ModelConfig{
					"m": {LoadMode: lm, SplitMode: sm, MainGPU: 0, RopeScale: 0, RopeScaling: "none"},
				}
				path, err := generateModelsPresetFrom(models, cfgs)
				if err != nil {
					t.Fatal(err)
				}
				data, _ := os.ReadFile(path)
				content := string(data)
				for _, banned := range []string{"load-mode", "split-mode", "cpu-moe", "main-gpu", "rope-scale", "rope-scaling"} {
					if strings.Contains(content, banned+" =") {
						t.Errorf("defaults must not output %q (load-mode=%q split-mode=%q): %q", banned, lm, sm, content)
					}
				}
				os.Remove(path)
			}
		}
	})

	t.Run("legacy mlock noMmap not written", func(t *testing.T) {
		// simulate post-migration state: legacy booleans zeroed, LoadMode already derived;
		// even if compatibility fields residue to true, preset only writes load-mode,
		// never writes deprecated keys mlock/no-mmap.
		cfgs := map[string]ModelConfig{
			"m": {LoadMode: "mlock", MLock: true, NoMMap: true},
		}
		path, err := generateModelsPresetFrom(models, cfgs)
		if err != nil {
			t.Fatal(err)
		}
		defer os.Remove(path)

		data, _ := os.ReadFile(path)
		content := string(data)
		if !strings.Contains(content, "load-mode = mlock\n") {
			t.Errorf("post-migration should output load-mode = mlock: %q", content)
		}
		if strings.Contains(content, "mlock =") {
			t.Errorf("compatibility field MLock must not output mlock key directly: %q", content)
		}
		if strings.Contains(content, "no-mmap") {
			t.Errorf("compatibility field NoMMap must not output no-mmap key directly: %q", content)
		}
	})
}

// TestValidCacheTypeValueExtended verifies cache-type whitelist expansion under b10342:
// newly added f32/q4_1/iq4_nl/q5_0/q5_1 must all be valid, values outside the list are illegal.
func TestValidCacheTypeValueExtended(t *testing.T) {
	for _, v := range []string{"", "f32", "f16", "bf16", "q8_0", "q4_0", "q4_1", "iq4_nl", "q5_0", "q5_1"} {
		if !validCacheTypeValue(v) {
			t.Errorf("validCacheTypeValue(%q) should be true (b10342 supported)", v)
		}
	}
	for _, v := range []string{"q4_2", "q6_k", "f32\nx", " q8_0"} {
		if validCacheTypeValue(v) {
			t.Errorf("validCacheTypeValue(%q) should be false", v)
		}
	}
}

// TestValidLoadModeValue verifies load-mode whitelist (b10342 replacement for mlock/no-mmap).
func TestValidLoadModeValue(t *testing.T) {
	for _, v := range []string{"", "none", "mmap", "mlock", "mmap+mlock", "dio"} {
		if !validLoadModeValue(v) {
			t.Errorf("validLoadModeValue(%q) should be true", v)
		}
	}
	for _, v := range []string{"foo", "mmap+", " mlock", "mlock\n"} {
		if validLoadModeValue(v) {
			t.Errorf("validLoadModeValue(%q) should be false", v)
		}
	}
}

// TestValidSplitModeValue verifies split-mode whitelist (multi-GPU split strategy).
func TestValidSplitModeValue(t *testing.T) {
	for _, v := range []string{"", "none", "layer", "row", "tensor"} {
		if !validSplitModeValue(v) {
			t.Errorf("validSplitModeValue(%q) should be true", v)
		}
	}
	for _, v := range []string{"layers", "column", " row"} {
		if validSplitModeValue(v) {
			t.Errorf("validSplitModeValue(%q) should be false", v)
		}
	}
}

// TestValidRopeScalingValue verifies rope-scaling whitelist (long-context extrapolation).
func TestValidRopeScalingValue(t *testing.T) {
	for _, v := range []string{"", "none", "linear", "yarn"} {
		if !validRopeScalingValue(v) {
			t.Errorf("validRopeScalingValue(%q) should be true", v)
		}
	}
	for _, v := range []string{"dynamic", "linear2", " yarn"} {
		if validRopeScalingValue(v) {
			t.Errorf("validRopeScalingValue(%q) should be false", v)
		}
	}
}

// TestValidSpecTypeValue verifies spec-type whitelist (MTP multi-token
// prediction plus the n-gram self-speculation types, which need no draft
// model and no extra VRAM).
func TestValidSpecTypeValue(t *testing.T) {
	for _, v := range []string{"", "draft-mtp", "ngram-simple", "ngram-mod"} {
		if !validSpecTypeValue(v) {
			t.Errorf("validSpecTypeValue(%q) should be true", v)
		}
	}
	for _, v := range []string{"draft", "mtp", "draft-mtp2", " draft-mtp", "ngram", "ngram-simple2", "ngram-mod "} {
		if validSpecTypeValue(v) {
			t.Errorf("validSpecTypeValue(%q) should be false", v)
		}
	}
}

// TestGenerateModelsPresetFromMMProjReasoningSpec verifies new model parameters are
// written to preset INI: explicit mmproj path (takes priority over auto-detection,
// outputs exactly one mmproj line), reasoning=off, spec-type / spec-draft-n-max.
// When MMProj is empty and HasMMProj=true, auto-detection is maintained.
func TestGenerateModelsPresetFromMMProjReasoningSpec(t *testing.T) {
	// explicit mmproj scenario: place a real mmproj file in the same directory;
	// explicit path should override auto-detection
	dir := t.TempDir()
	autoMMProj := filepath.Join(dir, "mmproj-auto.gguf")
	if err := os.WriteFile(autoMMProj, []byte("proj"), 0644); err != nil {
		t.Fatal(err)
	}
	modelPath := filepath.Join(dir, "llava.gguf")

	models := []ModelInfo{{Name: "llava", Path: modelPath, HasMMProj: true}}
	cfgs := map[string]ModelConfig{
		"llava": {
			MMProj:        filepath.Join(dir, "mmproj-explicit.gguf"),
			Reasoning:     true,
			SpecType:      "draft-mtp",
			SpecDraftNMax: 4,
		},
	}
	path, err := generateModelsPresetFrom(models, cfgs)
	if err != nil {
		t.Fatal(err)
	}
	defer os.Remove(path)

	data, _ := os.ReadFile(path)
	content := string(data)

	// explicit mmproj line exists and uses forward slashes
	wantMM := "mmproj = " + filepath.ToSlash(cfgs["llava"].MMProj)
	if !strings.Contains(content, wantMM+"\n") {
		t.Errorf("preset missing explicit mmproj line %q: %q", wantMM, content)
	}
	// when explicit path exists, auto-detected mmproj line must not be output (only one mmproj line)
	if strings.Contains(content, "mmproj-auto.gguf") {
		t.Errorf("explicit mmproj exists, auto-detected mmproj must not be output: %q", content)
	}
	if strings.Count(content, "mmproj =") != 1 {
		t.Errorf("only one mmproj line should be output: %q", content)
	}
	if !strings.Contains(content, "reasoning = off\n") {
		t.Errorf("preset missing reasoning = off: %q", content)
	}
	if !strings.Contains(content, "spec-type = draft-mtp\n") {
		t.Errorf("preset missing spec-type = draft-mtp: %q", content)
	}
	if !strings.Contains(content, "spec-draft-n-max = 4\n") {
		t.Errorf("preset missing spec-draft-n-max = 4: %q", content)
	}
}

// TestGenerateModelsPresetFromMMProjAutoDetection verifies that when MMProj is empty,
// the existing same-directory auto-detection logic is maintained (HasMMProj=true and
// directory contains mmproj-*.gguf → mmproj line is output).
func TestGenerateModelsPresetFromMMProjAutoDetection(t *testing.T) {
	dir := t.TempDir()
	mmprojPath := filepath.Join(dir, "mmproj-f16.gguf")
	if err := os.WriteFile(mmprojPath, []byte("proj"), 0644); err != nil {
		t.Fatal(err)
	}
	modelPath := filepath.Join(dir, "llava.gguf")

	models := []ModelInfo{{Name: "llava", Path: modelPath, HasMMProj: true}}
	// MMProj is empty: behavior matches unconfigured field, uses auto-detection
	path, err := generateModelsPresetFrom(models, map[string]ModelConfig{"llava": {}})
	if err != nil {
		t.Fatal(err)
	}
	defer os.Remove(path)

	data, _ := os.ReadFile(path)
	content := string(data)
	if !strings.Contains(content, "mmproj = "+filepath.ToSlash(mmprojPath)) {
		t.Errorf("empty MMProj should auto-detect mmproj: %q", content)
	}
}

// TestGenerateModelsPresetFromRejectsSpecAndMMProj verifies preset generation returns
// errors for illegal SpecType and illegal mmproj (INI injection payload) (second defense layer).
func TestGenerateModelsPresetFromRejectsSpecAndMMProj(t *testing.T) {
	models := []ModelInfo{{Name: "m", Path: "/models/m.gguf"}}
	badCfgs := []map[string]ModelConfig{
		{"m": {SpecType: "draft-unknown"}},
		{"m": {MMProj: "x.gguf\n[evil]\nmodel=/tmp/x"}},
		{"m": {MMProj: " /etc/x.gguf"}}, // leading/trailing whitespace rejected
	}
	for i, cfgs := range badCfgs {
		if _, err := generateModelsPresetFrom(models, cfgs); err == nil {
			t.Errorf("case %d: illegal SpecType/MMProj should return error", i)
		}
	}

	// control group: SpecDraftNMax default 0 and valid SpecType generate successfully
	okCfgs := map[string]ModelConfig{"m": {SpecType: "draft-mtp", SpecDraftNMax: 0}}
	if _, err := generateModelsPresetFrom(models, okCfgs); err != nil {
		t.Errorf("valid SpecType should generate successfully: %v", err)
	}
}

// ─── modelPresetKV / modelDirectArgs (Android direct mode) ─────────

// iniFromKVs serializes per-model preset entries exactly the way
// generateModelsPresetFrom prints them (section header, `key = value` lines,
// blank line separator) — the reference format the golden test compares
// modelPresetKV output against.
func iniFromKVs(alias string, kvs []presetKV) string {
	var b strings.Builder
	fmt.Fprintf(&b, "[%s]\n", alias)
	for _, kv := range kvs {
		fmt.Fprintf(&b, "%s = %s\n", kv.key, kv.value)
	}
	b.WriteString("\n")
	return b.String()
}

// TestModelPresetKVGoldenINI verifies the refactor is byte-identical: for
// models covering every emission branch (no config, full config, embedding,
// explicit mmproj, auto-detected mmproj), serializing modelPresetKV output
// through the INI printer reproduces the generateModelsPresetFrom file byte
// for byte (alias dedup included).
func TestModelPresetKVGoldenINI(t *testing.T) {
	dir := t.TempDir()
	autoMMProj := filepath.Join(dir, "mmproj-auto.gguf")
	if err := os.WriteFile(autoMMProj, []byte("proj"), 0644); err != nil {
		t.Fatal(err)
	}

	models := []ModelInfo{
		{Name: "plain", Path: "/models/plain.gguf"},
		{Name: "full", Path: "/models/full.gguf"},
		{Name: "bge-small-zh", Path: "/models/bge.gguf"},
		{Name: "llava-explicit", Path: "/models/llava1.gguf", HasMMProj: true},
		{Name: "llava-auto", Path: filepath.Join(dir, "llava2.gguf"), HasMMProj: true},
	}
	cfgs := map[string]ModelConfig{
		"full": {
			Threads: 8, GPULayers: "99", CtxSize: 8192, BatchSize: 512,
			UBatchSize: 256, FlashAttn: true, CacheTypeK: "q8_0", CacheTypeV: "bf16",
			LoadMode: "mlock", CPUMoe: true, NCpuMoe: 2, SplitMode: "row",
			TensorSplit: "3,1", MainGPU: 1, RopeScaling: "yarn", RopeScale: 2.0,
			MMProj: "/models/proj.gguf", Reasoning: true,
			SpecType: "draft-mtp", SpecDraftNMax: 4,
		},
		"llava-explicit": {MMProj: filepath.Join(dir, "mmproj-explicit.gguf")},
	}

	path, err := generateModelsPresetFrom(models, cfgs)
	if err != nil {
		t.Fatal(err)
	}
	defer os.Remove(path)

	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}

	used := make(map[string]int)
	var want strings.Builder
	for _, m := range models {
		alias := aliasDedup(m.Name, used)
		kvs, err := modelPresetKV(m, cfgs[m.Name])
		if err != nil {
			t.Fatal(err)
		}
		want.WriteString(iniFromKVs(alias, kvs))
	}
	if string(data) != want.String() {
		t.Errorf("INI preset drifted from modelPresetKV serialization:\n--- file ---\n%s\n--- kv ---\n%s", string(data), want.String())
	}
}

// TestModelDirectArgs verifies the direct-mode CLI mapping of every
// modelPresetKV row against upstream b10698 long options: same-name options
// with values verbatim, -m for the model path, --alias from the sanitized
// display name, bare --embeddings/--cpu-moe flags, --flash-attn on as a
// valued flag, and no --reasoning (upstream has no such option).
func TestModelDirectArgs(t *testing.T) {
	cfg := ModelConfig{
		Threads: 8, GPULayers: "99", CtxSize: 8192, BatchSize: 512,
		UBatchSize: 256, FlashAttn: true, CacheTypeK: "q8_0", CacheTypeV: "bf16",
		LoadMode: "mmap+mlock", CPUMoe: true, NCpuMoe: 2, SplitMode: "row",
		TensorSplit: "3,1", MainGPU: 1, RopeScaling: "yarn", RopeScale: 2.0,
		MMProj: "/models/proj.gguf", Reasoning: true,
		SpecType: "draft-mtp", SpecDraftNMax: 4,
		CtxCheckpointsOff: true,
	}
	m := ModelInfo{Name: "Qwen2.5 7B", Path: "/models/q.gguf"}

	args, err := modelDirectArgs(sanitizeAlias(m.Name), m, cfg)
	if err != nil {
		t.Fatal(err)
	}

	want := []string{
		"--alias", "Qwen2.5-7B",
		"-m", "/models/q.gguf",
		"--ctx-size", "8192",
		"--batch-size", "512",
		"--ubatch-size", "256",
		"--threads", "8",
		"--ctx-checkpoints", "0",
		"--gpu-layers", "99",
		"--flash-attn", "on",
		"--cache-type-k", "q8_0",
		"--cache-type-v", "bf16",
		"--load-mode", "mmap+mlock",
		"--cpu-moe",
		"--n-cpu-moe", "2",
		"--split-mode", "row",
		"--tensor-split", "3,1",
		"--main-gpu", "1",
		"--rope-scaling", "yarn",
		"--rope-scale", "2",
		"--mmproj", "/models/proj.gguf",
		"--spec-type", "draft-mtp",
		"--spec-draft-n-max", "4",
	}
	if len(args) != len(want) {
		t.Fatalf("args = %v, want %v", args, want)
	}
	for i := range want {
		if args[i] != want[i] {
			t.Errorf("args[%d] = %q, want %q", i, args[i], want[i])
		}
	}
}

// TestModelDirectArgsBareEmbeddings verifies an embedding model gets the bare
// --embeddings flag (upstream takes no value there) while everything else in
// a zero-config model stays just alias + model path.
func TestModelDirectArgsBareEmbeddings(t *testing.T) {
	m := ModelInfo{Name: "bge-small-zh", Path: "/models/bge.gguf"}
	args, err := modelDirectArgs(sanitizeAlias(m.Name), m, ModelConfig{})
	if err != nil {
		t.Fatal(err)
	}
	want := []string{"--alias", "bge-small-zh", "-m", "/models/bge.gguf", "--embeddings"}
	if len(args) != len(want) {
		t.Fatalf("args = %v, want %v", args, want)
	}
	for i := range want {
		if args[i] != want[i] {
			t.Errorf("args[%d] = %q, want %q", i, args[i], want[i])
		}
	}
}

// TestModelDirectArgsGPULayersAll verifies gpu-layers values pass through
// verbatim (upstream accepts auto/all/int); "auto" never reaches the entry
// list (the INI writer omits it), so "all" is the passthrough representative.
func TestModelDirectArgsGPULayersAll(t *testing.T) {
	args, err := modelDirectArgs("m", ModelInfo{Name: "m", Path: "/m.gguf"}, ModelConfig{GPULayers: "all"})
	if err != nil {
		t.Fatal(err)
	}
	joined := strings.Join(args, " ")
	if !strings.Contains(joined, "--gpu-layers all") {
		t.Errorf("args missing --gpu-layers all: %v", args)
	}
}

// TestModelDirectArgsMMProjAutoDetect verifies the same-directory mmproj
// auto-detection survives into direct mode: HasMMProj with an empty MMProj
// config emits --mmproj with the detected file path.
func TestModelDirectArgsMMProjAutoDetect(t *testing.T) {
	dir := t.TempDir()
	mmprojPath := filepath.Join(dir, "mmproj-f16.gguf")
	if err := os.WriteFile(mmprojPath, []byte("proj"), 0644); err != nil {
		t.Fatal(err)
	}
	m := ModelInfo{Name: "llava", Path: filepath.Join(dir, "llava.gguf"), HasMMProj: true}
	args, err := modelDirectArgs("llava", m, ModelConfig{})
	if err != nil {
		t.Fatal(err)
	}
	joined := strings.Join(args, " ")
	if !strings.Contains(joined, "--mmproj "+filepath.ToSlash(mmprojPath)) {
		t.Errorf("args missing auto-detected --mmproj: %v", args)
	}
}

// TestModelDirectArgsRejectsInjection verifies modelDirectArgs propagates the
// modelPresetKV validation errors (same messages as the INI writer) instead
// of emitting an unsafe value into a command line.
func TestModelDirectArgsRejectsInjection(t *testing.T) {
	if _, err := modelDirectArgs("m", ModelInfo{Name: "m", Path: "/m.gguf"}, ModelConfig{GPULayers: "99\n[evil]"}); err == nil {
		t.Error("injection GPULayers should return an error")
	}
	if _, err := modelDirectArgs("m", ModelInfo{Name: "m", Path: "/m.gguf"}, ModelConfig{SpecType: "bogus"}); err == nil {
		t.Error("illegal SpecType should return an error")
	}
}

// TestModelDirectArgsAndroidSkipsGPUOnlyFlags verifies the CPU-only defense:
// with the platformGOOS seam pinned to android, GPU-only flags from persisted
// desktop-era configs (flash-attn / cpu-moe / n-cpu-moe) are dropped from the
// direct-mode command line — the android build ships no GPU backend — while
// neutral options (--ctx-size) and the CPU-safe checkpoint pin
// (--ctx-checkpoints 0) survive untouched.
func TestModelDirectArgsAndroidSkipsGPUOnlyFlags(t *testing.T) {
	withPlatformGOOS(t, "android")
	cfg := ModelConfig{
		CtxSize: 8192, FlashAttn: true,
		CPUMoe: true, NCpuMoe: 3,
		CtxCheckpointsOff: true,
	}
	args, err := modelDirectArgs("m", ModelInfo{Name: "m", Path: "/m.gguf"}, cfg)
	if err != nil {
		t.Fatal(err)
	}
	joined := strings.Join(args, " ")
	for _, forbidden := range []string{"flash-attn", "cpu-moe", "n-cpu-moe"} {
		if strings.Contains(joined, forbidden) {
			t.Errorf("android args must not carry %q: %v", forbidden, args)
		}
	}
	if !strings.Contains(joined, "--ctx-size 8192") {
		t.Errorf("android args must keep --ctx-size: %v", args)
	}
	if !strings.Contains(joined, "--ctx-checkpoints 0") {
		t.Errorf("android args must keep the CPU-safe --ctx-checkpoints 0: %v", args)
	}
}

// TestGenerateModelsPresetFromCtxCheckpoints verifies the checkpoint pin is
// written into the INI iff CtxCheckpointsOff is set: the zero value stays
// silent so llama-server's own default (32 checkpoints) applies, and old
// configs without the field keep generating byte-identical presets.
func TestGenerateModelsPresetFromCtxCheckpoints(t *testing.T) {
	models := []ModelInfo{{Name: "m", Path: "/models/m.gguf"}}

	path, err := generateModelsPresetFrom(models, map[string]ModelConfig{"m": {CtxCheckpointsOff: true}})
	if err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	os.Remove(path)
	if !strings.Contains(string(data), "ctx-checkpoints = 0\n") {
		t.Errorf("preset missing \"ctx-checkpoints = 0\": %q", string(data))
	}

	path, err = generateModelsPresetFrom(models, map[string]ModelConfig{"m": {}})
	if err != nil {
		t.Fatal(err)
	}
	defer os.Remove(path)
	data, err = os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(data), "ctx-checkpoints") {
		t.Errorf("zero config must not emit ctx-checkpoints: %q", string(data))
	}
}

// TestGenerateModelsPresetRemovesPreviousTempFile verifies the temp-INI
// cleanup: generating a preset removes the previous run's temp file instead of
// leaking one llama-models-*.ini into the system temp directory per server
// start. The INI is parsed by llama-server once at child boot, so the previous
// file is garbage by the time a next preset is written.
func TestGenerateModelsPresetRemovesPreviousTempFile(t *testing.T) {
	presetPathMu.Lock()
	lastPresetPath = ""
	presetPathMu.Unlock()
	t.Cleanup(func() {
		presetPathMu.Lock()
		p := lastPresetPath
		lastPresetPath = ""
		presetPathMu.Unlock()
		if p != "" {
			os.Remove(p)
		}
	})

	models := []ModelInfo{{Name: "cleanup", Path: "/models/cleanup.gguf"}}
	first, err := generateModelsPresetFrom(models, nil)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(first); err != nil {
		t.Fatalf("first preset missing: %v", err)
	}

	second, err := generateModelsPresetFrom(models, nil)
	if err != nil {
		t.Fatal(err)
	}
	if second == first {
		t.Fatalf("second generation should write a fresh temp file, got %q twice", second)
	}
	if _, err := os.Stat(first); !os.IsNotExist(err) {
		t.Errorf("previous preset %s should be removed by the next generation, stat err = %v", first, err)
	}
	if _, err := os.Stat(second); err != nil {
		t.Errorf("current preset %s must survive: %v", second, err)
	}
}
