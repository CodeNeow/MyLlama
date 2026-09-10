package core

import (
	"bytes"
	"encoding/binary"
	"os"
	"path/filepath"
	"testing"
	"time"
)

// ggufKV is a test helper for GGUF metadata key-value pairs: valueType is the GGUF
// value type number, raw holds the type's underlying bytes (serialized by helpers).
type ggufKV struct {
	key       string
	valueType uint32
	raw       []byte
}

// putU32 / putU64 write uints in little-endian, matching readGGUFMeta's read side.
func putU32(buf *bytes.Buffer, v uint32) { binary.Write(buf, binary.LittleEndian, v) }
func putU64(buf *bytes.Buffer, v uint64) { binary.Write(buf, binary.LittleEndian, v) }

// strKV builds a GGUF string-type KV (valueType=8).
func strKV(key, val string) ggufKV {
	var buf bytes.Buffer
	putU64(&buf, uint64(len(val)))
	buf.WriteString(val)
	return ggufKV{key: key, valueType: 8, raw: buf.Bytes()}
}

// u32KV builds a GGUF uint32-type KV (valueType=4).
func u32KV(key string, val uint32) ggufKV {
	var buf bytes.Buffer
	putU32(&buf, val)
	return ggufKV{key: key, valueType: 4, raw: buf.Bytes()}
}

// u64KV builds a GGUF uint64-type KV (valueType=10).
func u64KV(key string, val uint64) ggufKV {
	var buf bytes.Buffer
	putU64(&buf, val)
	return ggufKV{key: key, valueType: 10, raw: buf.Bytes()}
}

// boolKV builds a GGUF bool-type KV (valueType=7), used to exercise the skip branch.
func boolKV(key string, val byte) ggufKV {
	return ggufKV{key: key, valueType: 7, raw: []byte{val}}
}

// buildGGUF assembles a byte slice matching the GGUF header layout: magic/version/tensorCount/kvCount
// + KV sequence. The first four bytes are "GGUF", which reads as 0x46554747 in little-endian (GGUF magic).
func buildGGUF(version uint32, kvs ...ggufKV) []byte {
	var buf bytes.Buffer
	buf.WriteString("GGUF")
	putU32(&buf, version)
	putU64(&buf, 0) // tensorCount
	putU64(&buf, uint64(len(kvs)))
	for _, kv := range kvs {
		putU64(&buf, uint64(len(kv.key)))
		buf.WriteString(kv.key)
		putU32(&buf, kv.valueType)
		buf.Write(kv.raw)
	}
	return buf.Bytes()
}

// writeTempGGUF writes GGUF bytes to the specified filename under dir, returning the full path.
func writeTempGGUF(t *testing.T, dir, name string, data []byte) string {
	t.Helper()
	path := filepath.Join(dir, name)
	if err := os.WriteFile(path, data, 0644); err != nil {
		t.Fatalf("failed to write test GGUF: %v", err)
	}
	return path
}

// TestReadGGUFMeta verifies readGGUFMeta extracts the three target fields (name/arch/quant)
// from a valid GGUF header and skips uninteresting KVs.
func TestReadGGUFMeta(t *testing.T) {
	dir := t.TempDir()
	path := writeTempGGUF(t, dir, "model.gguf", buildGGUF(3,
		strKV("general.name", "Qwen2.5-7B-Instruct"),
		u32KV("general.size", 12345), // unknown key, should be skipped
		boolKV("general.foo", 1),     // bool type, should be skipped
		strKV("general.architecture", "Qwen"),
		u32KV("general.file_type", 15), // 15 → Q4_K_M
	))

	meta := readGGUFMeta(path)
	if meta == nil {
		t.Fatal("readGGUFMeta returned nil, expected successful parse")
	}
	if meta["name"] != "Qwen2.5-7B-Instruct" {
		t.Errorf("name = %q, want Qwen2.5-7B-Instruct", meta["name"])
	}
	if meta["arch"] != "Qwen" {
		t.Errorf("arch = %q, want Qwen", meta["arch"])
	}
	if meta["quant"] != "Q4_K_M" {
		t.Errorf("quant = %q, want Q4_K_M", meta["quant"])
	}
}

// TestReadGGUFMetaUint64Quant verifies file_type encoded as uint64 (type 10) is also parsed correctly.
func TestReadGGUFMetaUint64Quant(t *testing.T) {
	dir := t.TempDir()
	path := writeTempGGUF(t, dir, "model.gguf", buildGGUF(2,
		strKV("general.name", "bge-small-zh"),
		u64KV("general.file_type", 8), // 8 → Q8_0
	))

	meta := readGGUFMeta(path)
	if meta == nil {
		t.Fatal("readGGUFMeta returned nil")
	}
	if meta["quant"] != "Q8_0" {
		t.Errorf("quant = %q, want Q8_0", meta["quant"])
	}
}

// TestReadGGUFMetaInvalidMagic verifies a non-GGUF file returns nil (no panic).
func TestReadGGUFMetaInvalidMagic(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "bad.gguf")
	if err := os.WriteFile(path, []byte("NOTGUF DATA"), 0644); err != nil {
		t.Fatal(err)
	}
	if meta := readGGUFMeta(path); meta != nil {
		t.Error("invalid magic should return nil")
	}
}

// ─── ggufQuantName ───────────────────────────────────────────────

// TestReadGGUFMetaUnsupportedVersion verifies an unsupported GGUF version returns nil.
func TestReadGGUFMetaUnsupportedVersion(t *testing.T) {
	dir := t.TempDir()
	path := writeTempGGUF(t, dir, "v1.gguf", buildGGUF(1, strKV("general.name", "old")))
	if meta := readGGUFMeta(path); meta != nil {
		t.Error("GGUF version 1 should return nil")
	}
}

// TestReadGGUFMetaMissingFile verifies a missing file returns nil.
func TestReadGGUFMetaMissingFile(t *testing.T) {
	if meta := readGGUFMeta(filepath.Join(t.TempDir(), "nope.gguf")); meta != nil {
		t.Error("non-existent file should return nil")
	}
}

// TestGGUFQuantName verifies the GGUF file_type number → quant-name mapping and unknown-value fallback.
func TestGGUFQuantName(t *testing.T) {
	cases := []struct {
		in   uint32
		want string
	}{
		{0, "F32"},
		{1, "F16"},
		{15, "Q4_K_M"},
		{26, "IQ4_NL"},
		{999, "Q999"},
	}
	for _, c := range cases {
		if got := ggufQuantName(c.in); got != c.want {
			t.Errorf("ggufQuantName(%d) = %q, want %q", c.in, got, c.want)
		}
	}
}

// TestReadGGUFStringTooLong verifies strings exceeding the 1 MB length limit are rejected with an error.
func TestReadGGUFStringTooLong(t *testing.T) {
	var buf bytes.Buffer
	putU64(&buf, 1024*1024+1)
	if _, err := readGGUFString(&buf); err == nil {
		t.Error("overlong string should return error")
	}
}

// TestReadGGUFMetaRejectsHugeKVCount verifies a malicious/corrupt GGUF with kvCount above the 4096
// limit is rejected outright (#7.2): readGGUFMeta returns nil and does not panic. Previously kvCount
// came from the file with no cap, amplifying parse cost; the fix aborts parsing before the loop.
func TestReadGGUFMetaRejectsHugeKVCount(t *testing.T) {
	dir := t.TempDir()

	// Hand-craft a GGUF header: magic + version 3 + tensorCount 0 + kvCount 5000,
	// with no KV entries (parsing should terminate before reading KVs).
	var buf bytes.Buffer
	buf.WriteString("GGUF")
	putU32(&buf, 3)
	putU64(&buf, 0)    // tensorCount
	putU64(&buf, 5000) // kvCount exceeds limit
	path := writeTempGGUF(t, dir, "huge.gguf", buf.Bytes())

	if meta := readGGUFMeta(path); meta != nil {
		t.Errorf("kvCount over limit should return nil, got %v", meta)
	}
}

// ─── Nested-array depth cap ──────────────────────────────────────

// nestedArrayRaw builds the raw value bytes of a GGUF array chain exactly
// depth levels deep: the first depth-1 headers declare one array element each
// (elemType=9) and the innermost header declares a single uint8 element, so
// the parser legitimately walks all depth levels and terminates on the byte.
// The 32-bit element count matches this parser's array header layout.
func nestedArrayRaw(depth int) []byte {
	if depth <= 0 {
		return []byte{42} // a plain scalar value, no array at all
	}
	var buf bytes.Buffer
	if depth == 1 {
		// Innermost array: its single element is a fixed-size uint8.
		putU32(&buf, 0)
		putU32(&buf, 1)
		buf.WriteByte(42)
		return buf.Bytes()
	}
	putU32(&buf, 9) // element type: array
	putU32(&buf, 1) // one element
	buf.Write(nestedArrayRaw(depth - 1))
	return buf.Bytes()
}

// TestReadGGUFMetaNestedArrayDepthBomb verifies a hostile GGUF whose first
// metadata value is an array nested maxGGUFArrayDepth×10 levels deep fails
// fast: readGGUFMeta aborts with nil well within the wall-clock bound instead
// of recursing until the goroutine stack is exhausted (before the cap, each
// nesting level cost only the 8-byte array header, so a small crafted file
// crashed the whole process during a plain directory scan).
func TestReadGGUFMetaNestedArrayDepthBomb(t *testing.T) {
	dir := t.TempDir()
	path := writeTempGGUF(t, dir, "bomb.gguf", buildGGUF(3,
		ggufKV{key: "general.bomb", valueType: 9, raw: nestedArrayRaw(maxGGUFArrayDepth * 10)},
		strKV("general.name", "never reached"),
	))

	done := make(chan map[string]string, 1)
	go func() { done <- readGGUFMeta(path) }()
	select {
	case meta := <-done:
		if meta != nil {
			t.Errorf("over-deep nesting must abort the whole parse with nil, got %v", meta)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("readGGUFMeta did not terminate within 2s on a depth-bomb GGUF")
	}
}

// TestReadGGUFMetaLegalNestedArray verifies the cap does not overreach: an
// array nested just below and exactly at maxGGUFArrayDepth walks the full
// parse path, the stream stays aligned through it (the target KV following
// the array is still read), and the result is returned normally.
func TestReadGGUFMetaLegalNestedArray(t *testing.T) {
	for _, depth := range []int{maxGGUFArrayDepth - 1, maxGGUFArrayDepth} {
		dir := t.TempDir()
		path := writeTempGGUF(t, dir, "nested.gguf", buildGGUF(3,
			ggufKV{key: "general.vocab", valueType: 9, raw: nestedArrayRaw(depth)},
			strKV("general.name", "Qwen2.5-7B"),
		))

		meta := readGGUFMeta(path)
		if meta == nil {
			t.Fatalf("depth %d: legal nesting must not abort the parse", depth)
		}
		if meta["name"] != "Qwen2.5-7B" {
			t.Errorf("depth %d: name = %q, want Qwen2.5-7B (stream must stay aligned after the array)", depth, meta["name"])
		}
	}
}
