package core

import (
	"encoding/binary"
	"fmt"
	"io"
	"math"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
)

// ─── LoRA adapter scanning & identification ─────────────────────
// Scans the LoRA adapter directory for GGUF files and reads the GGUF header
// metadata to tell LoRA adapters apart from regular models. The identification
// keys mirror what llama.cpp itself enforces when loading an adapter
// (src/llama-adapter.cpp): general.type == "adapter", adapter.type == "lora",
// with adapter.lora.alpha carrying the converter's alpha — the same keys
// convert_lora_to_gguf.py writes on the export side (e.g. Unsloth exports).
// The upstream default weight for --lora is 1.0 (arg.cpp pushes {FNAME, 1.0});
// the frontend's scale inputs use the same default so the two stay aligned.

// LoraInfo describes one scanned .gguf file in the LoRA directory. Valid marks
// a file identified as a LoRA adapter GGUF (general.type "adapter" +
// adapter.type "lora"); a regular model GGUF or an unreadable file scans with
// Valid=false so the UI can flag it instead of silently hiding it. Alpha is
// meaningful only when HasAlpha is set (the key is optional in the GGUF spec —
// the loader reads it with a zero default).
type LoraInfo struct {
	Name      string  `json:"name"`
	Path      string  `json:"path"`
	SizeBytes int64   `json:"sizeBytes"`
	SizeHuman string  `json:"sizeHuman"`
	Alpha     float64 `json:"alpha"`
	HasAlpha  bool    `json:"hasAlpha"`
	Arch      string  `json:"arch"`
	Valid     bool    `json:"valid"`
}

// effectiveLoraDir returns the directory LoRA adapters are scanned from: the
// user-chosen path when configured, otherwise <model download dir>/lora. The
// directory is not created on demand — a missing directory scans as empty
// (an empty adapter list is a normal state, not an error).
func effectiveLoraDir() string {
	loraDirMu.Lock()
	dir := loraDirOverride
	loraDirMu.Unlock()
	if dir != "" {
		return dir
	}
	return filepath.Join(effectiveModelDownloadDir(), "lora")
}

// scanLoraAdapters lists the .gguf files in the LoRA directory and classifies
// each as a LoRA adapter or not via its GGUF header metadata. A missing
// directory yields an empty list without error; unreadable / non-GGUF files
// are reported with Valid=false so the user sees why an entry cannot be used.
// Results are sorted by file name for a stable UI order.
func scanLoraAdapters() []LoraInfo {
	dir := effectiveLoraDir()
	entries, err := os.ReadDir(dir)
	if err != nil {
		// Missing (or unreadable) directory: an empty adapter list is the
		// normal fresh-install state, not a failure.
		return []LoraInfo{}
	}
	out := make([]LoraInfo, 0, len(entries))
	for _, e := range entries {
		if e.IsDir() {
			continue
		}
		if !strings.HasSuffix(strings.ToLower(e.Name()), ".gguf") {
			continue
		}
		path := filepath.Join(dir, e.Name())
		info := LoraInfo{Name: e.Name(), Path: path}
		if fi, err := os.Stat(path); err == nil {
			info.SizeBytes = fi.Size()
			info.SizeHuman = formatBytes(info.SizeBytes)
		}
		if meta := readGGUFAdapterMeta(path); meta != nil {
			info.Arch = meta.arch
			info.Alpha = meta.alpha
			info.HasAlpha = meta.hasAlpha
			info.Valid = meta.isLora
		}
		out = append(out, info)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Name < out[j].Name })
	return out
}

// adapterMeta is the identification payload extracted from an adapter GGUF
// header (nil from readGGUFAdapterMeta when the file is not valid GGUF).
type adapterMeta struct {
	isLora   bool
	alpha    float64
	hasAlpha bool
	arch     string
}

// readGGUFAdapterMeta reads the LoRA identification keys from a GGUF header.
// Returns nil when the file cannot be opened or is not a valid GGUF file.
// Reuses the shared GGUF primitives (readGGUFString / skipGGUFValue) from
// gguf.go; the key set is the one llama.cpp's adapter loader consumes:
// general.type + adapter.type decide Valid, adapter.lora.alpha and
// general.architecture are surfaced as metadata for the UI.
func readGGUFAdapterMeta(path string) *adapterMeta {
	f, err := os.Open(path)
	if err != nil {
		return nil
	}
	defer f.Close()

	var magic uint32
	if err := binary.Read(f, binary.LittleEndian, &magic); err != nil {
		return nil
	}
	if magic != 0x46554747 { // "GGUF" little-endian
		return nil
	}
	var version uint32
	if err := binary.Read(f, binary.LittleEndian, &version); err != nil {
		return nil
	}
	if version < 2 || version > 3 {
		return nil
	}
	var tensorCount, kvCount uint64
	if err := binary.Read(f, binary.LittleEndian, &tensorCount); err != nil {
		return nil
	}
	if err := binary.Read(f, binary.LittleEndian, &kvCount); err != nil {
		return nil
	}
	// Same corruption guard as readGGUFMeta: real adapter headers carry a
	// handful of KVs; give up beyond the sane cap instead of parsing a
	// hostile file's unbounded KV list.
	if kvCount > 4096 {
		return nil
	}

	meta := &adapterMeta{}
	generalType := ""
	adapterType := ""

	for i := uint64(0); i < kvCount; i++ {
		key, err := readGGUFString(f)
		if err != nil {
			return meta
		}
		var valueType uint32
		if err := binary.Read(f, binary.LittleEndian, &valueType); err != nil {
			return meta
		}
		switch key {
		case "general.type":
			if valueType == 8 {
				generalType, _ = readGGUFString(f)
				continue
			}
		case "adapter.type":
			if valueType == 8 {
				adapterType, _ = readGGUFString(f)
				continue
			}
		case "general.architecture":
			if valueType == 8 {
				meta.arch, _ = readGGUFString(f)
				continue
			}
		case "adapter.lora.alpha":
			if v, ok := readGGUFNumeric(f, valueType); ok {
				meta.alpha = v
				meta.hasAlpha = true
				continue
			}
		}
		skipGGUFValue(f, valueType)
	}
	meta.isLora = generalType == "adapter" && adapterType == "lora"
	return meta
}

// readGGUFNumeric decodes the numeric GGUF value types into a float64, covering
// every integer / float width the converter may plausibly use for alpha
// (convert_lora_to_gguf.py writes float32; the wider set is tolerance).
// Returns ok=false for non-numeric types (caller skips the value).
func readGGUFNumeric(r io.Reader, valueType uint32) (float64, bool) {
	switch valueType {
	case 0: // uint8
		var v uint8
		if binary.Read(r, binary.LittleEndian, &v) == nil {
			return float64(v), true
		}
	case 1: // int8
		var v int8
		if binary.Read(r, binary.LittleEndian, &v) == nil {
			return float64(v), true
		}
	case 2: // uint16
		var v uint16
		if binary.Read(r, binary.LittleEndian, &v) == nil {
			return float64(v), true
		}
	case 3: // int16
		var v int16
		if binary.Read(r, binary.LittleEndian, &v) == nil {
			return float64(v), true
		}
	case 4: // uint32
		var v uint32
		if binary.Read(r, binary.LittleEndian, &v) == nil {
			return float64(v), true
		}
	case 5: // int32
		var v int32
		if binary.Read(r, binary.LittleEndian, &v) == nil {
			return float64(v), true
		}
	case 6: // float32
		var v float32
		if binary.Read(r, binary.LittleEndian, &v) == nil {
			return float64(v), true
		}
	case 10: // uint64
		var v uint64
		if binary.Read(r, binary.LittleEndian, &v) == nil {
			return float64(v), true
		}
	case 11: // int64
		var v int64
		if binary.Read(r, binary.LittleEndian, &v) == nil {
			return float64(v), true
		}
	case 12: // float64
		var v float64
		if binary.Read(r, binary.LittleEndian, &v) == nil {
			return v, true
		}
	}
	return 0, false
}

// ─── LoraRef validation & serialization ─────────────────────────

// validLoraRefName validates one LoraRef.Name: it must be a plain file name
// inside the LoRA directory. Beyond the INI-injection basics (newlines, NUL,
// surrounding whitespace) this rejects path separators and ".." (the name is
// resolved against the llama-server working directory), colons (upstream
// splits FNAME:SCALE on every colon and rejects != 2 parts — a Windows
// drive-letter path or any colon-containing name would fail the parse) and
// commas / quotes (the adapters ride one CSV row, where those characters are
// structural).
func validLoraRefName(name string) bool {
	if name == "" || name == "." || name == ".." {
		return false
	}
	if len(name) > 255 {
		return false
	}
	if name != filepath.Base(name) {
		return false
	}
	if strings.ContainsAny(name, `\/:,"`+"\n\r\x00") {
		return false
	}
	// Leading/trailing whitespace would be silently trimmed by the INI
	// parser on the llama.cpp side, changing the file name; interior spaces
	// stay legal (they are valid in file names).
	return strings.TrimSpace(name) == name
}

// validLoraScale reports whether a scale value is within the supported
// [0, 4] range and finite (NaN/Inf would serialize as unparseable garbage).
func validLoraScale(scale float64) bool {
	return !math.IsNaN(scale) && !math.IsInf(scale, 0) && scale >= 0 && scale <= 4
}

// validateLoraRef checks one reference's name and scale, returning a
// user-facing bilingual error for the first violation.
func validateLoraRef(ref LoraRef) error {
	if !validLoraRefName(ref.Name) {
		return fmt.Errorf(tr("非法 LoRA 适配器名 %q：必须是适配器目录内的纯文件名，且不含 : , \" 或路径分隔符", "invalid LoRA adapter name %q: must be a plain file name inside the adapter directory without : , \" or path separators"), ref.Name)
	}
	if !validLoraScale(ref.Scale) {
		return fmt.Errorf(tr("非法 LoRA 权重 %g：仅允许 0–4 之间的数值", "invalid LoRA scale %g: only values between 0 and 4 are allowed"), ref.Scale)
	}
	return nil
}

// formatLoraScale renders a scale for the --lora-scaled FNAME:SCALE value the
// way upstream parses it (std::stof accepts both "1" and "1.05"); %g keeps
// values compact and free of trailing zeros.
func formatLoraScale(scale float64) string {
	return strconv.FormatFloat(scale, 'g', -1, 64)
}

// loraScaledPart renders one enabled reference as its "name:scale" segment.
// The caller validates the reference first.
func loraScaledPart(ref LoraRef) string {
	return ref.Name + ":" + formatLoraScale(ref.Scale)
}

// enabledLoraRefs returns the enabled references of one config, in order.
func enabledLoraRefs(cfg ModelConfig) []LoraRef {
	out := make([]LoraRef, 0, len(cfg.LoraAdapters))
	for _, ref := range cfg.LoraAdapters {
		if ref.Enabled {
			out = append(out, ref)
		}
	}
	return out
}

// loraScaledValue joins the enabled references into the single CSV value of
// the upstream --lora-scaled option (FNAME:SCALE,...). Returns "" when no
// reference is enabled.
func loraScaledValue(cfg ModelConfig) (string, error) {
	parts := make([]string, 0, len(cfg.LoraAdapters))
	for _, ref := range cfg.LoraAdapters {
		if !ref.Enabled {
			continue
		}
		if err := validateLoraRef(ref); err != nil {
			return "", err
		}
		parts = append(parts, loraScaledPart(ref))
	}
	return strings.Join(parts, ","), nil
}

// hasEnabledLoraRefs reports whether any persisted model config enables at
// least one adapter — the signal that tells buildServerCommand to pin the
// llama-server working directory to the LoRA directory (the preset and
// direct-mode args carry bare file names that must resolve there).
func hasEnabledLoraRefs() bool {
	modelConfigsMu.Lock()
	defer modelConfigsMu.Unlock()
	for _, cfg := range cachedModelConfigs {
		for _, ref := range cfg.LoraAdapters {
			if ref.Enabled {
				return true
			}
		}
	}
	return false
}
