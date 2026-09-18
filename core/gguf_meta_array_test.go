package core

import "testing"

// TestReadGGUFMetaSkipsArraysWithU64Length verifies metadata parsing survives
// array-valued keys. GGUF v2/v3 store an array's element count as u64, and
// every real model carries such keys (general.tags, the tokenizer vocab)
// before general.file_type, so a parser that misreads the count desynchronizes
// the stream and silently loses every key that follows.
func TestReadGGUFMetaSkipsArraysWithU64Length(t *testing.T) {
	dir := t.TempDir()
	tokens := make([]string, 1500)
	for i := range tokens {
		tokens[i] = "tok"
	}
	path := writeTempGGUF(t, dir, "arrays.gguf", buildGGUF(3,
		strKV("general.name", "Ornith 1.5 9B Heretic"),
		strKV("general.architecture", "qwen35"),
		arrayStrKV("general.tags", []string{"heretic", "uncensored"}),
		arrayStrKV("tokenizer.ggml.tokens", tokens),
		u32KV("general.file_type", 15),
	))

	meta := readGGUFMeta(path)
	if meta == nil {
		t.Fatal("readGGUFMeta returned nil; array values must be skipped, not truncated")
	}
	if got := meta["quant"]; got != "Q4_K_M" {
		t.Errorf("quant = %q, want Q4_K_M: general.file_type follows the arrays", got)
	}
	if got := meta["name"]; got != "Ornith 1.5 9B Heretic" {
		t.Errorf("name = %q, want the general.name value", got)
	}
}
