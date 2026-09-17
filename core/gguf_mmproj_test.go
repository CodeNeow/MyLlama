package core

import "testing"

// TestIsMMProjName covers the projector naming styles published on Hugging
// Face: the conventional prefix, the "<model>-mmproj-<type>" and
// "<model>.mmproj-<type>" forms, and names that merely contain the token.
func TestIsMMProjName(t *testing.T) {
	tests := []struct {
		name string
		want bool
	}{
		{"mmproj-f16.gguf", true},
		{"mmproj-gemma-4-26B-A4B-it-Q8_0.gguf", true},
		{"MMPROJ-BF16.gguf", true},
		{"Qwen-9B-mmproj-F16.gguf", true},
		{"Ornith-1.5-9B-heretic.mmproj-f16.gguf", true},
		{"Qwen3.6-35B-A3B-Q8_0.gguf", false},
		{"mymmproj-model-Q4_K_M.gguf", false},
		{"model_mmproj_variant.gguf", false},
	}
	for _, tt := range tests {
		if got := isMMProjName(tt.name); got != tt.want {
			t.Errorf("isMMProjName(%q) = %v, want %v", tt.name, got, tt.want)
		}
	}
}
