package core

import (
	"encoding/binary"
	"errors"
	"fmt"
	"io"
	"log"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"sync/atomic"
)

// ─── Model scanning & GGUF metadata ─────────────────────────────
// Scans the model directories for GGUF models and reads GGUF header metadata
// to derive each model's display name, architecture and quantization.

type ModelInfo struct {
	Author       string `json:"author"`
	Name         string `json:"name"`
	Path         string `json:"path"`
	SizeBytes    int64  `json:"sizeBytes"`
	SizeHuman    string `json:"sizeHuman"`
	Architecture string `json:"architecture"`
	Quantization string `json:"quantization"`
	HasMMProj    bool   `json:"hasMmproj"`
	// SourceDir is the root directory the model was scanned from (the model
	// download directory or the user-imported model directory). Lets the
	// frontend show which of the two sources a model belongs to when both are
	// configured.
	SourceDir string `json:"sourceDir"`
	// Alias is the llama-server model id for this model: the display Name
	// sanitized for INI section use with its original casing preserved, plus a
	// deterministic -2/-3 suffix on case-insensitive collisions (see
	// aliasDedup). The API page shows it so users copy-paste an id that
	// llama-server matches exactly (model lookup is case-sensitive).
	Alias string `json:"alias"`
}

var cachedModels []ModelInfo

// modelsCacheValid marks whether the model cache is valid (atomic read, used by
// the GetModels fast path). Writes happen inside the modelsMu lock; invalidation
// cannot be implemented by reassigning a sync.Once variable, because concurrent
// Do plus assignment corrupts Once's internal mutex state (#4).
var modelsCacheValid atomic.Bool

// modelsMu guards reads/writes of cachedModels and the cache-validity flag,
// preventing data races during concurrent model scans/refreshes (#4). Readers
// copy the slice under the lock before returning.
var modelsMu sync.Mutex

// invalidateModelCache invalidates the model cache: the next GetModels rescans
// the directory. Called from paths such as download completion and manual
// refresh, keeping invalidation safe under concurrent access.
func invalidateModelCache() {
	modelsMu.Lock()
	modelsCacheValid.Store(false)
	modelsMu.Unlock()
}

// defaultModelsDir resolves the default model directory name (modelsDirName
// in paths.go) to its per-OS location: bare cwd-relative on Windows, under
// the app-data base on non-Windows desktop, and on Android under the
// app-specific external-storage base (multi-GB models need the shared-disk
// headroom; see androidModelsBase). Declared as a function-valued var (same
// injection style as configFile) so tests can pin the directory.
var defaultModelsDir = func() string {
	if pathsGOOS == "android" {
		if base := pathsAndroidModelsBase(); base != "" {
			return filepath.Join(base, modelsDirName)
		}
	}
	return resolveStateFile(modelsDirName)
}

// modelScanDirs returns the roots the model list is scanned from, in priority
// order: the model download directory first, then the imported model directory
// when set. Directories are resolved to absolute paths and duplicates removed,
// so pointing both settings at the same place does not double-list models, and
// so the SourceDir annotated on scanned models matches the absolute download
// path GetConfig reports to the frontend (on Windows the default is
// cwd-relative, e.g. LLM-Models; other platforms resolve it under the
// per-OS app-data base, see paths.go).
func modelScanDirs() []string {
	dirs := []string{effectiveModelDownloadDir()}
	modelsDirMu.Lock()
	imported := customModelsDir
	modelsDirMu.Unlock()
	if imported != "" {
		dirs = append(dirs, imported)
	}
	seen := make(map[string]bool, len(dirs))
	out := make([]string, 0, len(dirs))
	for _, d := range dirs {
		clean := filepath.Clean(d)
		if abs, err := filepath.Abs(clean); err == nil {
			clean = abs
		}
		if seen[clean] {
			continue
		}
		seen[clean] = true
		out = append(out, clean)
	}
	return out
}

// scanModels scans all model sources (the model download directory and the
// imported model directory, when set), creating the default LLM-Models
// directory only when no custom path is configured. Results are merged by
// model identity (author + name): the download path is scanned first, so a
// model present in both sources is listed once with the download path's copy
// (the imported duplicate is dropped); each result is annotated with the root
// it was scanned from.
func scanModels() []ModelInfo {
	merged := make([]ModelInfo, 0)
	seen := make(map[string]bool)
	for _, dir := range modelScanDirs() {
		// Custom paths are user-picked and expected to exist already; only
		// the default directory needs lazy creation (compare both the
		// default and its absolute form, as scan roots are resolved to
		// absolute paths).
		def := defaultModelsDir()
		isDefault := dir == def
		if abs, err := filepath.Abs(def); err == nil {
			isDefault = isDefault || dir == abs
		}
		if isDefault {
			if err := os.MkdirAll(dir, 0755); err != nil {
				log.Printf("[WARN] Failed to create %s dir: %v", dir, err)
				continue
			}
		}
		for _, m := range scanModelsDir(dir) {
			// Dedupe by author+name (the identity shown in the model list):
			// a copy in the imported directory is dropped when the download
			// path already has the same model.
			key := m.Author + "\x00" + m.Name
			if seen[key] {
				continue
			}
			seen[key] = true
			m.SourceDir = dir
			merged = append(merged, m)
		}
	}
	sort.Slice(merged, func(i, j int) bool {
		return merged[i].SizeBytes > merged[j].SizeBytes
	})
	// Assign llama-server model ids after the final ordering so the alias the
	// UI shows (copy-paste target) is the same section name the preset writes;
	// generateModelsPresetFrom recomputes with the same deterministic helper.
	usedAliases := make(map[string]int)
	for i := range merged {
		merged[i].Alias = aliasDedup(merged[i].Name, usedAliases)
	}
	return merged
}

// scanModelsDir scans the model directory tree for GGUF models. Both the
// <author>/<variant>/<files>.gguf layout (three-level, current HF download
// destination) and the <author>/<file>.gguf layout (two-level, produced by
// earlier HF download versions) are recognized. A variant directory counts
// as a model when it contains at least one non-mmproj .gguf file; mmproj
// files only flag multimodal support.
func scanModelsDir(dir string) []ModelInfo {
	models := make([]ModelInfo, 0)

	// Top-level: author directories
	authors, err := os.ReadDir(dir)
	if err != nil {
		log.Printf("[WARN] Failed to read %s dir: %v", dir, err)
		return models
	}

	for _, authorEntry := range authors {
		if !authorEntry.IsDir() {
			continue
		}
		author := authorEntry.Name()
		authorDir := filepath.Join(dir, author)

		// Second-level: variant directories and/or loose .gguf files
		entries, err := os.ReadDir(authorDir)
		if err != nil {
			continue
		}

		for _, entry := range entries {
			if entry.IsDir() {
				// Three-level layout: <author>/<variant>/<files>.gguf
				variantName := entry.Name()
				variantDir := filepath.Join(authorDir, variantName)

				// Find .gguf files in this variant directory
				files, err := os.ReadDir(variantDir)
				if err != nil {
					continue
				}

				var mainGGUF string
				var hasMMProj bool

				for _, f := range files {
					if f.IsDir() {
						continue
					}
					name := f.Name()
					if !strings.HasSuffix(strings.ToLower(name), ".gguf") {
						continue
					}
					lower := strings.ToLower(name)
					if strings.HasPrefix(lower, "mmproj") {
						hasMMProj = true
						continue
					}
					// Found main model file (non-mmproj .gguf)
					if mainGGUF == "" {
						mainGGUF = filepath.Join(variantDir, name)
					}
				}

				if mainGGUF == "" {
					continue
				}

				model := buildModelInfo(mainGGUF, author, variantName)
				model.HasMMProj = hasMMProj
				models = append(models, model)
				continue
			}

			// Two-level layout: loose .gguf files directly under the author
			// directory. Non-.gguf files are skipped; loose mmproj-*.gguf
			// files cannot be tied to a model here and are skipped too,
			// matching the three-level rule that mmproj never counts as a
			// main model.
			name := entry.Name()
			lower := strings.ToLower(name)
			if !strings.HasSuffix(lower, ".gguf") {
				continue
			}
			if strings.HasPrefix(lower, "mmproj") {
				continue
			}

			model := buildModelInfo(filepath.Join(authorDir, name), author,
				strings.TrimSuffix(name, filepath.Ext(name)))
			models = append(models, model)
		}
	}

	sort.Slice(models, func(i, j int) bool {
		return models[i].SizeBytes > models[j].SizeBytes
	})

	return models
}

// buildModelInfo builds a ModelInfo from one GGUF main-file path: reads GGUF
// metadata to override name/architecture/quantization, falling back to
// fallbackName/author when missing. Shared by the two-level and three-level
// scans, avoiding duplicated metadata reading and fallback logic between
// variant directories and author loose files.
func buildModelInfo(path, author, fallbackName string) ModelInfo {
	model := ModelInfo{Author: author, Path: path, Name: fallbackName}
	if fi, err := os.Stat(path); err == nil {
		model.SizeBytes = fi.Size()
		model.SizeHuman = formatBytes(model.SizeBytes)
	}
	// Try to read GGUF metadata for better name/arch/quant
	if metadata := readGGUFMeta(path); metadata != nil {
		// Only use GGUF name if it looks readable (not a hash)
		if n := metadata["name"]; n != "" && isReadableName(n) {
			// Some converters embed the full source repo id ("org/model")
			// into general.name; display only the model segment.
			if i := strings.LastIndex(n, "/"); i >= 0 && i+1 < len(n) {
				n = n[i+1:]
			}
			model.Name = n
		}
		if a := metadata["arch"]; a != "" {
			model.Architecture = a
		}
		if q := metadata["quant"]; q != "" {
			model.Quantization = q
		}
	}
	// The main file name identifies the actual variant on disk when it is
	// strictly more specific than the resolved name: either the resolved name
	// is only its prefix (unsloth writes the bare base-model name into
	// general.name for every quant in a repo, e.g. "Qwen3.5-9B" for
	// Qwen3.5-9B-UD-Q4_K_XL.gguf), or the resolved name is a "<model>-GGUF"
	// variant-directory fallback that hides the quant the file name carries.
	if fileBase := strings.TrimSuffix(filepath.Base(path), filepath.Ext(path)); fileBase != model.Name &&
		(preferFileNameVariant(model.Name, fileBase) || preferFileNameOverGenericSuffix(model.Name, fileBase)) {
		model.Name = fileBase
	}
	// Fallback quantization from fallbackName, then from file name
	if model.Quantization == "" {
		model.Quantization = guessQuantFromName(fallbackName)
		if model.Quantization == "-" {
			model.Quantization = guessQuantFromName(filepath.Base(path))
		}
	}
	// Fallback architecture from fallbackName/author
	if model.Architecture == "" {
		model.Architecture = guessArchFromName(fallbackName + " " + author)
	}
	return model
}

// preferFileNameVariant reports whether fileBase carries strictly more
// information than name: name is a proper prefix of fileBase and the next
// character is a separator ("-" or "_"). This catches converters that write
// the bare base-model name ("Qwen3.5-9B") while the file name carries the
// quant variant ("Qwen3.5-9B-UD-Q4_K_XL"); generic file names ("model.gguf")
// never qualify because name does not prefix them.
func preferFileNameVariant(name, fileBase string) bool {
	if len(name) == 0 || len(fileBase) <= len(name) {
		return false
	}
	if !strings.EqualFold(fileBase[:len(name)], name) {
		return false
	}
	c := fileBase[len(name)]
	return c == '-' || c == '_'
}

// genericVariantSuffixes lists variant-name suffixes that only mark the file
// container format rather than the model itself; HF download destinations
// typically name the variant directory after the source repo ("<model>-GGUF").
var genericVariantSuffixes = []string{"-GGUF", "_GGUF"}

// preferFileNameOverGenericSuffix reports whether fileBase carries strictly
// more information than name: name ends with a generic "-GGUF"/"_GGUF" suffix
// and fileBase begins with the suffix-trimmed name followed by a separator
// ("-" or "_"). The variant-directory fallback hides the actual quant variant
// ("Qwen3.5-9B-GGUF" holding "Qwen3.5-9B-Q4_K_M.gguf"); the main file name
// identifies the real variant on disk. Mirrors preferFileNameVariant's
// prefix/separator style.
func preferFileNameOverGenericSuffix(name, fileBase string) bool {
	for _, suffix := range genericVariantSuffixes {
		if len(name) <= len(suffix) || !strings.EqualFold(name[len(name)-len(suffix):], suffix) {
			continue
		}
		trimmed := name[:len(name)-len(suffix)]
		if len(fileBase) <= len(trimmed) || !strings.EqualFold(fileBase[:len(trimmed)], trimmed) {
			continue
		}
		if c := fileBase[len(trimmed)]; c == '-' || c == '_' {
			return true
		}
	}
	return false
}

// converterPlaceholderNames lists placeholder values converters write into
// general.name when the real model name is unknown ("Unsloth_Gguf" from
// unsloth, "Hf_Model" from some HF-space converters). A name equal to or
// starting with any entry is treated as unreadable (case-insensitive), so the
// scanner falls back to the variant directory / file name.
var converterPlaceholderNames = []string{"Unsloth_Gguf", "Hf_Model"}

// isReadableName returns true if the name doesn't look like a hash/UUID or a
// converter placeholder.
func isReadableName(name string) bool {
	if len(name) < 3 {
		return false
	}
	// If it's all hex and over 32 chars, it's likely a hash
	hexCount := 0
	for _, c := range name {
		if (c >= '0' && c <= '9') || (c >= 'a' && c <= 'f') || (c >= 'A' && c <= 'F') {
			hexCount++
		}
	}
	if hexCount > len(name)*3/4 && len(name) > 10 {
		return false
	}
	// Converter placeholders are auto-generated names, not real model names
	for _, ph := range converterPlaceholderNames {
		if len(name) >= len(ph) && strings.EqualFold(name[:len(ph)], ph) {
			return false
		}
	}
	// If it contains spaces or dashes, it's likely human-readable
	if strings.ContainsAny(name, " -.") {
		return true
	}
	return true
}

func guessArchFromName(name string) string {
	lower := strings.ToLower(name)
	arches := []struct {
		key  string
		arch string
	}{
		{"qwen", "Qwen"},
		{"qwopus", "Qwen"},
		{"llama", "LLaMA"},
		{"mistral", "Mistral"},
		{"phi", "Phi"},
		{"gemma", "Gemma"},
		{"deepseek", "DeepSeek"},
		{"yi", "Yi"},
		{"chatglm", "ChatGLM"},
		{"baichuan", "Baichuan"},
		{"falcon", "Falcon"},
		{"mpt", "MPT"},
		{"starcoder", "StarCoder"},
		{"codellama", "CodeLLaMA"},
		{"claude", "Claude-Distilled"},
	}
	for _, a := range arches {
		if strings.Contains(lower, a.key) {
			return a.arch
		}
	}
	return "-"
}

// ─── GGUF header reader ──────────────────────────────────────────

// readGGUFMeta reads the GGUF header and extracts key metadata fields.
// Returns nil if the file is not a valid GGUF file.
func readGGUFMeta(path string) map[string]string {
	f, err := os.Open(path)
	if err != nil {
		return nil
	}
	defer f.Close()

	// Read magic
	var magic uint32
	if err := binary.Read(f, binary.LittleEndian, &magic); err != nil {
		return nil
	}
	if magic != 0x46554747 { // "GGUF" in little-endian
		return nil
	}

	// Read version
	var version uint32
	if err := binary.Read(f, binary.LittleEndian, &version); err != nil {
		return nil
	}
	if version < 2 || version > 3 {
		return nil
	}

	// Read tensor count and metadata kv count
	var tensorCount, kvCount uint64
	if err := binary.Read(f, binary.LittleEndian, &tensorCount); err != nil {
		return nil
	}
	if err := binary.Read(f, binary.LittleEndian, &kvCount); err != nil {
		return nil
	}

	// Guard against malicious/corrupt files: without a cap on kvCount, the
	// loop could parse an extremely long KV list and amplify parsing cost
	// (#7.2). Real GGUF metadata keys are few; give up parsing beyond 4096.
	if kvCount > 4096 {
		return nil
	}

	result := make(map[string]string)
	targets := map[string]string{
		"general.name":         "name",
		"general.architecture": "arch",
		"general.file_type":    "quant",
	}
	found := 0

	for i := uint64(0); i < kvCount && found < len(targets); i++ {
		key, err := readGGUFString(f)
		if err != nil {
			break
		}

		var valueType uint32
		if err := binary.Read(f, binary.LittleEndian, &valueType); err != nil {
			break
		}

		field, wanted := targets[key]
		if !wanted {
			if err := skipGGUFValue(f, valueType, 0); err != nil {
				if errors.Is(err, errGGUFDepth) {
					// Hostile structure: fail fast with no result rather than
					// parsing on from a stream we cannot trust.
					return nil
				}
				break // I/O error: stop parsing, keep what we already have
			}
			continue
		}

		switch valueType {
		case 8: // string
			val, err := readGGUFString(f)
			if err != nil {
				break
			}
			result[field] = val
			found++
		case 4: // uint32 (file_type is uint32)
			var val uint32
			if err := binary.Read(f, binary.LittleEndian, &val); err != nil {
				break
			}
			result[field] = ggufQuantName(val)
			found++
		case 10: // uint64
			var val uint64
			if err := binary.Read(f, binary.LittleEndian, &val); err != nil {
				break
			}
			result[field] = ggufQuantName(uint32(val))
			found++
		default:
			if err := skipGGUFValue(f, valueType, 0); err != nil {
				if errors.Is(err, errGGUFDepth) {
					return nil // same fail-fast as the unknown-key path above
				}
				break // I/O error: stop parsing, keep what we already have
			}
		}
	}

	return result
}

// errGGUFDepth marks a GGUF metadata array nested deeper than
// maxGGUFArrayDepth — a malformed (hostile) structure, not an I/O failure.
// readGGUFMeta turns it into a nil result (whole meta parse aborted);
// readGGUFHeader (autotune.go) propagates it so its callers degrade to their
// documented fallbacks.
var errGGUFDepth = errors.New("gguf metadata array nesting too deep")

// maxGGUFArrayDepth caps GGUF array nesting during value skipping. Real model
// metadata arrays are flat — tokenizer vocabularies / token scores are arrays
// of strings or scalars, one level deep — and no known converter emits
// arrays-of-arrays at all, so 32 leaves generous headroom over anything
// legitimate. Without the cap, each nesting level in a crafted file costs only
// the 8-byte array header, so a ~40 MB file can drive recursion deep enough to
// exhaust the goroutine stack (a fatal, unrecoverable error) during a plain
// directory scan.
const maxGGUFArrayDepth = 32

func readGGUFString(r io.Reader) (string, error) {
	var length uint64
	if err := binary.Read(r, binary.LittleEndian, &length); err != nil {
		return "", err
	}
	if length > 1024*1024 { // sanity check
		return "", fmt.Errorf("string too long: %d", length)
	}
	buf := make([]byte, length)
	if _, err := io.ReadFull(r, buf); err != nil {
		return "", err
	}
	return string(buf), nil
}

// skipGGUFValue consumes one metadata value of the given GGUF type so the
// stream stays aligned for subsequent fields. depth counts the array levels
// enclosing this value; an array nested deeper than maxGGUFArrayDepth stops
// the recursion with errGGUFDepth (fail fast on hostile structure — the
// caller aborts the whole meta parse instead of guessing at a misaligned
// stream). I/O errors (e.g. EOF) are returned as well.
func skipGGUFValue(r io.Reader, valueType uint32, depth int) error {
	switch valueType {
	case 0, 1: // uint8, int8
		return binary.Read(r, binary.LittleEndian, make([]byte, 1))
	case 2, 3: // uint16, int16
		return binary.Read(r, binary.LittleEndian, make([]byte, 2))
	case 4, 5: // uint32, int32
		return binary.Read(r, binary.LittleEndian, make([]byte, 4))
	case 6: // float32
		return binary.Read(r, binary.LittleEndian, make([]byte, 4))
	case 7: // bool
		return binary.Read(r, binary.LittleEndian, make([]byte, 1))
	case 8: // string
		_, err := readGGUFString(r)
		return err
	case 10, 11: // uint64, int64
		return binary.Read(r, binary.LittleEndian, make([]byte, 8))
	case 12: // float64
		return binary.Read(r, binary.LittleEndian, make([]byte, 8))
	case 9: // array — the only value type that recurses
		if depth >= maxGGUFArrayDepth {
			return errGGUFDepth
		}
		var arrType uint32
		var arrLen uint32
		if err := binary.Read(r, binary.LittleEndian, &arrType); err != nil {
			return err
		}
		if err := binary.Read(r, binary.LittleEndian, &arrLen); err != nil {
			return err
		}
		for j := uint32(0); j < arrLen && j < 1000; j++ {
			if err := skipGGUFValue(r, arrType, depth+1); err != nil {
				return err
			}
		}
		return nil
	}
	return nil
}

func ggufQuantName(fileType uint32) string {
	// Common GGUF file_type values
	names := map[uint32]string{
		0:  "F32",
		1:  "F16",
		2:  "Q4_0",
		3:  "Q4_1",
		6:  "Q5_0",
		7:  "Q5_1",
		8:  "Q8_0",
		9:  "Q8_1",
		10: "Q2_K",
		11: "Q3_K_S",
		12: "Q3_K_M",
		13: "Q3_K_L",
		14: "Q4_K_S",
		15: "Q4_K_M",
		16: "Q5_K_S",
		17: "Q5_K_M",
		18: "Q6_K",
		19: "Q8_K",
		20: "IQ2_XXS",
		21: "IQ2_XS",
		22: "IQ3_XXS",
		23: "IQ3_S",
		24: "IQ3_M",
		25: "IQ4_XS",
		26: "IQ4_NL",
	}
	if name, ok := names[fileType]; ok {
		return name
	}
	return fmt.Sprintf("Q%d", fileType)
}

func guessQuantFromName(name string) string {
	name = strings.ToLower(name)
	quants := []string{
		"Q8_K", "Q8_0", "Q6_K", "Q5_K_M", "Q5_K_S", "Q5_1", "Q5_0",
		"Q4_K_M", "Q4_K_S", "Q4_1", "Q4_0", "Q3_K_L", "Q3_K_M", "Q3_K_S",
		"Q2_K", "Q2_K_S",
		"IQ4_NL", "IQ4_XS", "IQ3_M", "IQ3_S", "IQ3_XXS", "IQ2_XS", "IQ2_XXS",
		"F16", "F32", "BF16",
	}
	for _, q := range quants {
		if strings.Contains(name, strings.ToLower(q)) {
			return q
		}
	}
	return "-"
}
