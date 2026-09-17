package core

import (
	"os"
	"path/filepath"
	"sync"
	"testing"
)

// saveModelsState snapshots cachedModels/modelsCacheValid globals and restores them
// after the test.
func saveModelsState(t *testing.T) {
	t.Helper()
	modelsMu.Lock()
	origModels := cachedModels
	origValid := modelsCacheValid.Load()
	modelsMu.Unlock()
	t.Cleanup(func() {
		modelsMu.Lock()
		cachedModels = origModels
		modelsCacheValid.Store(origValid)
		modelsMu.Unlock()
	})
}

// TestConcurrentGetRefreshModels verifies concurrent GetModels/RefreshModels across
// many rounds do not panic and produce consistent results (#4). Previously, after
// modelsOnce reset, cachedModels was read/written without a lock: RefreshModels'
// rescan write and GetModels' return of the same underlying array could race
// concurrently. After refactoring, both write and read happen under modelsMu, and
// GetModels returns a copy.
// Uses an empty LLM-Models directory so scan results are always an empty slice, making
// consistency easy to assert.
func TestConcurrentGetRefreshModels(t *testing.T) {
	withTempCwd(t)
	saveModelsState(t)

	app := &App{}
	var wg sync.WaitGroup
	for i := 0; i < 200; i++ {
		wg.Add(2)
		go func() {
			defer wg.Done()
			app.GetModels()
		}()
		go func() {
			defer wg.Done()
			app.RefreshModels()
		}()
	}
	wg.Wait()

	// empty directory should always yield an empty list, and GetModels returns a copy
	models := app.GetModels()
	if len(models) != 0 {
		t.Errorf("empty LLM-Models directory should return 0 models, got %d", len(models))
	}
}

// makeVariant creates a model directory at base/author/variant and writes a gguf file;
// size controls the file size (used as the sorting assertion basis).
func makeVariant(t *testing.T, base, author, variant string, ggufName string, size int) {
	t.Helper()
	dir := filepath.Join(base, author, variant)
	if err := os.MkdirAll(dir, 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, ggufName), make([]byte, size), 0644); err != nil {
		t.Fatal(err)
	}
}

// TestScanModelsDir verifies directory scanning: <author>/<variant>/.gguf layout is
// recognized as one model, the name is taken from the variant directory name, and models
// are sorted by size descending.
func TestScanModelsDir(t *testing.T) {
	base := t.TempDir()
	makeVariant(t, base, "author1", "big-model", "big.gguf", 2048)
	makeVariant(t, base, "author1", "small-model", "small.gguf", 512)
	makeVariant(t, base, "author2", "third-model", "third.gguf", 1024)

	models := scanModelsDir(base)
	if len(models) != 3 {
		t.Fatalf("scanned %d models, want 3", len(models))
	}
	// sorted by size descending: 2048 > 1024 > 512
	if models[0].Name != "big-model" || models[1].Name != "third-model" || models[2].Name != "small-model" {
		t.Errorf("sort error: %v", []string{models[0].Name, models[1].Name, models[2].Name})
	}
	if models[0].Author != "author1" {
		t.Errorf("author = %q, want author1", models[0].Author)
	}
	if models[0].SizeHuman == "" {
		t.Error("SizeHuman must not be empty")
	}
	if models[0].Path == "" || filepath.Dir(models[0].Path) == "" {
		t.Error("Path must be a full path")
	}
}

// TestScanModelsDirMMProj verifies an mmproj file marks multimodal support and is not
// treated as a primary model.
func TestScanModelsDirMMProj(t *testing.T) {
	base := t.TempDir()
	dir := filepath.Join(base, "llava", "llava-v1.6")
	if err := os.MkdirAll(dir, 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "llava-v1.6.gguf"), []byte("model"), 0644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "mmproj-f16.gguf"), []byte("proj"), 0644); err != nil {
		t.Fatal(err)
	}

	models := scanModelsDir(base)
	if len(models) != 1 {
		t.Fatalf("scanned %d models, want 1", len(models))
	}
	if !models[0].HasMMProj {
		t.Error("model with mmproj file should be marked HasMMProj")
	}
}

// TestScanModelsDirEmpty verifies empty directories and directories without GGUF files
// return an empty list.
func TestScanModelsDirEmpty(t *testing.T) {
	base := t.TempDir()
	if models := scanModelsDir(base); len(models) != 0 {
		t.Errorf("empty directory should return 0 models, got %d", len(models))
	}
	makeVariant(t, base, "a", "v", "readme.txt", 100) // non-gguf
	if models := scanModelsDir(base); len(models) != 0 {
		t.Errorf("directory without gguf files should return 0 models, got %d", len(models))
	}
}

// TestScanModelsDirGGUFMeta verifies scanning reads GGUF header metadata to override
// the model name and quantization.
func TestScanModelsDirGGUFMeta(t *testing.T) {
	base := t.TempDir()
	dir := filepath.Join(base, "qwen", "qwen2.5-7b")
	if err := os.MkdirAll(dir, 0755); err != nil {
		t.Fatal(err)
	}
	writeTempGGUF(t, dir, "model.gguf", buildGGUF(3,
		strKV("general.name", "Qwen2.5-7B-Instruct"),
		strKV("general.architecture", "Qwen"),
		u32KV("general.file_type", 15),
	))

	models := scanModelsDir(base)
	if len(models) != 1 {
		t.Fatalf("scanned %d models, want 1", len(models))
	}
	if models[0].Name != "Qwen2.5-7B-Instruct" {
		t.Errorf("Name = %q, want Qwen2.5-7B-Instruct (should use GGUF metadata)", models[0].Name)
	}
	if models[0].Architecture != "Qwen" {
		t.Errorf("Architecture = %q, want Qwen", models[0].Architecture)
	}
	if models[0].Quantization != "Q4_K_M" {
		t.Errorf("Quantization = %q, want Q4_K_M", models[0].Quantization)
	}
}

// TestScanModelsDirGGUFMetaRepoIDName verifies a general.name that embeds the full
// source repo id ("org/model", written by some converters) is trimmed to the model
// segment. The generic file name is not a candidate here (the metadata name does
// not prefix it), so the trimmed metadata name is displayed as-is.
func TestScanModelsDirGGUFMetaRepoIDName(t *testing.T) {
	base := t.TempDir()
	dir := filepath.Join(base, "unsloth", "GLM-4.7-Flash-REAP-23B-A3B-GGUF")
	if err := os.MkdirAll(dir, 0755); err != nil {
		t.Fatal(err)
	}
	writeTempGGUF(t, dir, "model-q4_k_m.gguf", buildGGUF(3,
		strKV("general.name", "cerebras/GLM-4.7-Flash-REAP-23B-A3B"),
	))

	models := scanModelsDir(base)
	if len(models) != 1 {
		t.Fatalf("scanned %d models, want 1", len(models))
	}
	if models[0].Name != "GLM-4.7-Flash-REAP-23B-A3B" {
		t.Errorf("Name = %q, want GLM-4.7-Flash-REAP-23B-A3B (repo id prefix trimmed)", models[0].Name)
	}
}

// TestScanModelsDirGGUFMetaBaseNamePrefix verifies that when the metadata name is
// only a prefix of the main file name (unsloth writes the bare base-model name
// into general.name for every quant variant in a repo, e.g. "Qwen3.5-9B" for
// Qwen3.5-9B-UD-Q4_K_XL.gguf), the more specific file name is displayed. A
// non-separator boundary ("Qwen3.5-9Bv2") keeps the metadata name.
func TestScanModelsDirGGUFMetaBaseNamePrefix(t *testing.T) {
	base := t.TempDir()
	cases := []struct {
		author, file, meta, want string
	}{
		{"unsloth", "Qwen3.5-9B-UD-Q4_K_XL.gguf", "Qwen3.5-9B", "Qwen3.5-9B-UD-Q4_K_XL"},
		{"other", "Qwen3.5-9Bv2.gguf", "Qwen3.5-9B", "Qwen3.5-9B"},
	}
	for _, tc := range cases {
		dir := filepath.Join(base, tc.author, "Qwen3.5-9B-GGUF")
		if err := os.MkdirAll(dir, 0755); err != nil {
			t.Fatal(err)
		}
		writeTempGGUF(t, dir, tc.file, buildGGUF(3, strKV("general.name", tc.meta)))
	}

	models := scanModelsDir(base)
	if len(models) != len(cases) {
		t.Fatalf("scanned %d models, want %d", len(models), len(cases))
	}
	byFile := make(map[string]ModelInfo, len(models))
	for _, m := range models {
		byFile[filepath.Base(m.Path)] = m
	}
	for _, tc := range cases {
		m, ok := byFile[tc.file]
		if !ok {
			t.Fatalf("model for %s not scanned", tc.file)
		}
		if m.Name != tc.want {
			t.Errorf("Name for %s = %q, want %q", tc.file, m.Name, tc.want)
		}
	}
}

// TestIsReadableNameRejectsConverterPlaceholders verifies placeholder names
// converters write into general.name ("Unsloth_Gguf" from unsloth, "Hf_Model"
// from some HF-space converters, "Safetensors" from converters naming the
// model after the source format) are treated as unreadable regardless of case
// so the scanner falls back to directory/file names, while real model names
// stay readable.
func TestIsReadableNameRejectsConverterPlaceholders(t *testing.T) {
	for _, name := range []string{
		"Unsloth_Gguf", "unsloth_gguf", "UNSLOTH_GGUF", "Unsloth_Gguf-Qwen",
		"Hf_Model", "hf_model", "HF_MODEL",
		"Safetensors", "safetensors", "SAFETENSORS", "Safetensors-Qwen",
	} {
		if isReadableName(name) {
			t.Errorf("isReadableName(%q) = true, want false (converter placeholder)", name)
		}
	}
	for _, name := range []string{"Qwen3.5-9B", "Qwen2.5 7B Instruct"} {
		if !isReadableName(name) {
			t.Errorf("isReadableName(%q) = false, want true (real model name)", name)
		}
	}
}

// TestScanModelsDirGenericSuffixVariantFallback verifies that when GGUF
// metadata is rejected (converter placeholders like "Hf_Model"/"Unsloth_Gguf")
// and the variant-directory fallback only carries a generic "-GGUF"/"_GGUF"
// container suffix, the main file base — which carries the actual quant
// variant — is displayed instead. Variant names without the suffix (and file
// names that are not more specific) keep the existing fallback behavior.
func TestScanModelsDirGenericSuffixVariantFallback(t *testing.T) {
	base := t.TempDir()
	cases := []struct {
		author, variant, file, meta, want string
	}{
		// Reproducer: "Hf_Model" placeholder + "-GGUF"-suffixed variant dir
		// → file base wins over the variant dir name
		{"barozp", "Qwen3.6-29B-REAP-Opus-Reasoning-Distill-MTP-GGUF",
			"Qwen3.6-29B-REAP-Opus-Reasoning-Distill-MTP-Q4_K_M.gguf", "Hf_Model",
			"Qwen3.6-29B-REAP-Opus-Reasoning-Distill-MTP-Q4_K_M"},
		// Case-insensitive placeholder and "_GGUF" suffix behave the same
		{"a", "Foo-9B_GGUF", "Foo-9B-Q4_K_M.gguf", "hf_model", "Foo-9B-Q4_K_M"},
		// Existing "Unsloth_Gguf" rejection still falls through to the file base
		{"b", "Foo-9B-GGUF", "Foo-9B-UD-Q4_K_XL.gguf", "Unsloth_Gguf", "Foo-9B-UD-Q4_K_XL"},
		// Variant name without the -GGUF suffix is kept when the file name is
		// not more specific (no prefix match, no suffix to trim)
		{"c", "Foo-9B", "model.gguf", "Hf_Model", "Foo-9B"},
		// File base equal to the trimmed variant adds nothing: keep variant name
		{"d", "Foo-9B-GGUF", "Foo-9B.gguf", "Hf_Model", "Foo-9B-GGUF"},
		// File base not extending the trimmed variant: keep variant name
		{"e", "Foo-9B-GGUF", "other.gguf", "Hf_Model", "Foo-9B-GGUF"},
	}
	for _, tc := range cases {
		dir := filepath.Join(base, tc.author, tc.variant)
		if err := os.MkdirAll(dir, 0755); err != nil {
			t.Fatal(err)
		}
		writeTempGGUF(t, dir, tc.file, buildGGUF(3, strKV("general.name", tc.meta)))
	}

	models := scanModelsDir(base)
	if len(models) != len(cases) {
		t.Fatalf("scanned %d models, want %d", len(models), len(cases))
	}
	byFile := make(map[string]ModelInfo, len(models))
	for _, m := range models {
		byFile[filepath.Base(m.Path)] = m
	}
	for _, tc := range cases {
		m, ok := byFile[tc.file]
		if !ok {
			t.Fatalf("model for %s not scanned", tc.file)
		}
		if m.Name != tc.want {
			t.Errorf("Name for %s = %q, want %q", tc.file, m.Name, tc.want)
		}
	}
}

// TestScanModelsDirQuantFallback verifies quantization is inferred from the directory
// name or filename when GGUF metadata is absent.
func TestScanModelsDirQuantFallback(t *testing.T) {
	base := t.TempDir()
	makeVariant(t, base, "bge", "bge-small-zh-v1.5", "model-q8_0.gguf", 100)

	models := scanModelsDir(base)
	if len(models) != 1 {
		t.Fatalf("scanned %d models, want 1", len(models))
	}
	if models[0].Name != "bge-small-zh-v1.5" {
		t.Errorf("Name = %q, want bge-small-zh-v1.5", models[0].Name)
	}
	// directory name has no quantization info, fallback to filename model-q8_0.gguf
	if models[0].Quantization != "Q8_0" {
		t.Errorf("Quantization = %q, want Q8_0 (filename fallback)", models[0].Quantization)
	}
}

// makeLooseGGUF writes a loose .gguf file directly under base/author (two-level layout,
// matching the old downloader landing path <author>/<file>.gguf).
func makeLooseGGUF(t *testing.T, base, author, ggufName string, size int) {
	t.Helper()
	dir := filepath.Join(base, author)
	if err := os.MkdirAll(dir, 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, ggufName), make([]byte, size), 0644); err != nil {
		t.Fatal(err)
	}
}

// TestScanModelsDirTwoLevel verifies <author>/<file>.gguf two-level layout is recognized
// as a model: when GGUF metadata is absent, the name falls back to the filename without
// the .gguf extension, and the path points to the loose file itself.
func TestScanModelsDirTwoLevel(t *testing.T) {
	base := t.TempDir()
	makeLooseGGUF(t, base, "unsloth", "Qwen3.5-4B-UD-IQ2_XXS.gguf", 100)

	models := scanModelsDir(base)
	if len(models) != 1 {
		t.Fatalf("scanned %d models, want 1", len(models))
	}
	if models[0].Name != "Qwen3.5-4B-UD-IQ2_XXS" {
		t.Errorf("Name = %q, want Qwen3.5-4B-UD-IQ2_XXS (filename without .gguf)", models[0].Name)
	}
	if models[0].Author != "unsloth" {
		t.Errorf("Author = %q, want unsloth", models[0].Author)
	}
	if models[0].Path != filepath.Join(base, "unsloth", "Qwen3.5-4B-UD-IQ2_XXS.gguf") {
		t.Errorf("Path = %q, want full two-level path", models[0].Path)
	}
	if models[0].SizeHuman == "" {
		t.Error("SizeHuman must not be empty")
	}
}

// TestScanModelsDirTwoLevelGGUFMeta verifies that in a two-level layout, reading GGUF
// header metadata overrides name/architecture/quantization (shared buildModelInfo behavior
// is the same as the three-level TestScanModelsDirGGUFMeta).
func TestScanModelsDirTwoLevelGGUFMeta(t *testing.T) {
	base := t.TempDir()
	authorDir := filepath.Join(base, "qwen")
	if err := os.MkdirAll(authorDir, 0755); err != nil {
		t.Fatal(err)
	}
	writeTempGGUF(t, authorDir, "model.gguf", buildGGUF(3,
		strKV("general.name", "Qwen2.5-7B-Instruct"),
		strKV("general.architecture", "Qwen"),
		u32KV("general.file_type", 15),
	))

	models := scanModelsDir(base)
	if len(models) != 1 {
		t.Fatalf("scanned %d models, want 1", len(models))
	}
	if models[0].Name != "Qwen2.5-7B-Instruct" {
		t.Errorf("Name = %q, want Qwen2.5-7B-Instruct (should use GGUF metadata)", models[0].Name)
	}
	if models[0].Architecture != "Qwen" {
		t.Errorf("Architecture = %q, want Qwen", models[0].Architecture)
	}
	if models[0].Quantization != "Q4_K_M" {
		t.Errorf("Quantization = %q, want Q4_K_M", models[0].Quantization)
	}
}

// TestScanModelsDirTwoLevelMMProj verifies that in a two-level layout, mmproj-*.gguf
// loose files in the author directory are not treated as primary models (consistent with
// three-level variant-directory mmproj not acting as primary model; in two-level layout
// mmproj loose files cannot be associated with a model and are skipped outright).
func TestScanModelsDirTwoLevelMMProj(t *testing.T) {
	base := t.TempDir()
	authorDir := filepath.Join(base, "llava")
	if err := os.MkdirAll(authorDir, 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(authorDir, "mmproj-f16.gguf"), []byte("proj"), 0644); err != nil {
		t.Fatal(err)
	}

	models := scanModelsDir(base)
	if len(models) != 0 {
		t.Errorf("only mmproj loose files should scan to 0 models, got %d", len(models))
	}
}

// TestScanModelsDirMixedLayout verifies that two-level loose .gguf files and three-level
// variant subdirectories coexist under the same author directory and are both recognized,
// then uniformly sorted by SizeBytes descending after merging.
func TestScanModelsDirMixedLayout(t *testing.T) {
	base := t.TempDir()
	// two-level loose file (large)
	makeLooseGGUF(t, base, "author1", "loose-big.gguf", 2048)
	// three-level variant directory (small)
	makeVariant(t, base, "author1", "variant-small", "small.gguf", 512)

	models := scanModelsDir(base)
	if len(models) != 2 {
		t.Fatalf("scanned %d models, want 2", len(models))
	}
	// sorted by size descending: 2048 > 512, two-level and three-level outputs sorted uniformly
	if models[0].Name != "loose-big" {
		t.Errorf("models[0].Name = %q, want loose-big (two-level loose file should rank first by size)", models[0].Name)
	}
	if models[1].Name != "variant-small" {
		t.Errorf("models[1].Name = %q, want variant-small", models[1].Name)
	}
}

// TestScanModelsMergesSources verifies scanModels merges the model download
// directory and the imported model directory: models from both roots are
// listed, SourceDir annotates the root each model came from, duplicates of the
// same file reachable from both roots are listed once, and the merged result
// is sorted by size descending.
func TestScanModelsMergesSources(t *testing.T) {
	saveConfigState(t)
	dlDir := t.TempDir()
	importedDir := t.TempDir()

	modelDownloadDirMu.Lock()
	modelDownloadDirOverride = dlDir
	modelDownloadDirMu.Unlock()
	modelsDirMu.Lock()
	customModelsDir = importedDir
	modelsDirMu.Unlock()

	// downloaded root: a large model
	makeVariant(t, dlDir, "dl-author", "dl-model", "model.gguf", 2048)
	// imported root: a smaller model
	makeVariant(t, importedDir, "imp-author", "imp-model", "model.gguf", 512)

	models := scanModels()
	if len(models) != 2 {
		t.Fatalf("scanned %d models, want 2", len(models))
	}
	if models[0].Name != "dl-model" {
		t.Errorf("models[0].Name = %q, want dl-model (sorted by size descending)", models[0].Name)
	}
	if models[0].SourceDir != dlDir {
		t.Errorf("models[0].SourceDir = %q, want %q", models[0].SourceDir, dlDir)
	}
	if models[1].SourceDir != importedDir {
		t.Errorf("models[1].SourceDir = %q, want %q", models[1].SourceDir, importedDir)
	}
}

// TestScanModelsDedupesOverlappingDirs verifies modelScanDirs collapses roots
// that resolve to the same directory (download path and imported dir pointing
// at the same location), so a model file is not listed twice.
func TestScanModelsDedupesOverlappingDirs(t *testing.T) {
	saveConfigState(t)
	dir := t.TempDir()

	modelDownloadDirMu.Lock()
	modelDownloadDirOverride = dir
	modelDownloadDirMu.Unlock()
	modelsDirMu.Lock()
	customModelsDir = dir
	modelsDirMu.Unlock()

	makeVariant(t, dir, "author", "model", "model.gguf", 1024)

	models := scanModels()
	if len(models) != 1 {
		t.Fatalf("scanned %d models, want 1 (overlapping roots must dedupe)", len(models))
	}
	if models[0].Name != "model" {
		t.Errorf("models[0].Name = %q, want model", models[0].Name)
	}
}

// TestScanModelsSourceDirAbsolute verifies SourceDir is reported as an absolute
// path even when the download path is the default cwd-relative LLM-Models: the
// frontend compares model.sourceDir against the absolute modelDownloadDir from
// GetConfig, so a relative SourceDir would mislabel download-path models as
// imported (regression).
func TestScanModelsSourceDirAbsolute(t *testing.T) {
	withTempCwd(t)
	saveConfigState(t)

	modelDownloadDirMu.Lock()
	modelDownloadDirOverride = "LLM-Models"
	modelDownloadDirMu.Unlock()
	modelsDirMu.Lock()
	customModelsDir = ""
	modelsDirMu.Unlock()

	makeVariant(t, "LLM-Models", "author", "model", "model.gguf", 1024)

	models := scanModels()
	if len(models) != 1 {
		t.Fatalf("scanned %d models, want 1", len(models))
	}
	absWant, err := filepath.Abs("LLM-Models")
	if err != nil {
		t.Fatal(err)
	}
	if models[0].SourceDir != absWant {
		t.Errorf("SourceDir = %q, want absolute %q", models[0].SourceDir, absWant)
	}
}

// TestScanModelsDedupesRelativeAndAbsoluteRoots verifies a relative download
// path and an imported dir pointing at the same location (one relative, one
// absolute) are collapsed into a single scan root.
func TestScanModelsDedupesRelativeAndAbsoluteRoots(t *testing.T) {
	withTempCwd(t)
	saveConfigState(t)

	modelDownloadDirMu.Lock()
	modelDownloadDirOverride = "LLM-Models"
	modelDownloadDirMu.Unlock()
	modelsDirMu.Lock()
	abs, err := filepath.Abs("LLM-Models")
	if err != nil {
		t.Fatal(err)
	}
	customModelsDir = abs
	modelsDirMu.Unlock()

	makeVariant(t, "LLM-Models", "author", "model", "model.gguf", 1024)

	models := scanModels()
	if len(models) != 1 {
		t.Fatalf("scanned %d models, want 1 (relative+absolute duplicate roots must dedupe)", len(models))
	}
}

// TestScanModelsDownloadWinsDuplicates verifies that when the same model
// (author + name) exists in both the model download directory and the
// imported model directory, only the download path's copy is listed and it is
// annotated with the download path as its source.
func TestScanModelsDownloadWinsDuplicates(t *testing.T) {
	saveConfigState(t)
	dlDir := t.TempDir()
	importedDir := t.TempDir()

	modelDownloadDirMu.Lock()
	modelDownloadDirOverride = dlDir
	modelDownloadDirMu.Unlock()
	modelsDirMu.Lock()
	customModelsDir = importedDir
	modelsDirMu.Unlock()

	// same author+name in both roots: the download copy must win
	makeVariant(t, dlDir, "shared", "my-model", "model.gguf", 2048)
	makeVariant(t, importedDir, "shared", "my-model", "model.gguf", 512)

	models := scanModels()
	if len(models) != 1 {
		t.Fatalf("scanned %d models, want 1 (imported duplicate of the same model must be dropped)", len(models))
	}
	if models[0].Name != "my-model" || models[0].Author != "shared" {
		t.Errorf("model identity = %s/%s, want shared/my-model", models[0].Author, models[0].Name)
	}
	if models[0].SourceDir != dlDir {
		t.Errorf("SourceDir = %q, want %q (download path copy must win)", models[0].SourceDir, dlDir)
	}
}

// TestScanModelsKeepsDistinctAuthorsAcrossSources verifies a model with the
// same name under a different author in the imported directory is NOT treated
// as a duplicate: both entries are listed (dedupe key is author+name).
func TestScanModelsKeepsDistinctAuthorsAcrossSources(t *testing.T) {
	saveConfigState(t)
	dlDir := t.TempDir()
	importedDir := t.TempDir()

	modelDownloadDirMu.Lock()
	modelDownloadDirOverride = dlDir
	modelDownloadDirMu.Unlock()
	modelsDirMu.Lock()
	customModelsDir = importedDir
	modelsDirMu.Unlock()

	makeVariant(t, dlDir, "authorA", "my-model", "model.gguf", 1024)
	makeVariant(t, importedDir, "authorB", "my-model", "model.gguf", 512)

	models := scanModels()
	if len(models) != 2 {
		t.Fatalf("scanned %d models, want 2 (same name under different authors is not a duplicate)", len(models))
	}
	byAuthor := map[string]string{}
	for _, m := range models {
		byAuthor[m.Author] = m.SourceDir
	}
	if byAuthor["authorA"] != dlDir {
		t.Errorf("authorA SourceDir = %q, want %q", byAuthor["authorA"], dlDir)
	}
	if byAuthor["authorB"] != importedDir {
		t.Errorf("authorB SourceDir = %q, want %q", byAuthor["authorB"], importedDir)
	}
}
