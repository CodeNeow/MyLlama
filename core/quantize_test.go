package core

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// TestBuildQuantizeArgs verifies the positional <input> <output> <type> form
// llama-quantize consumes (quantize.cpp: the explicit-output path), with the
// quant type lowercased verbatim.
func TestBuildQuantizeArgs(t *testing.T) {
	args := buildQuantizeArgs(`C:\m\model.gguf`, `C:\m\model-q4_k_m.gguf`, "Q4_K_M")
	if len(args) != 3 {
		t.Fatalf("args = %v, want 3 positionals", args)
	}
	if args[0] != `C:\m\model.gguf` || args[1] != `C:\m\model-q4_k_m.gguf` || args[2] != "q4_k_m" {
		t.Errorf("args = %v, want [src out q4_k_m]", args)
	}
}

// TestValidQuantizeType pins the accepted vocabulary (the unsloth export
// GGUF_QUANTS set). Matching is strict: the frontend radio buttons send exact
// lowercase values, and padded / cased variants are rejected as garbage.
func TestValidQuantizeType(t *testing.T) {
	for _, q := range []string{"q4_k_m", "q5_k_m", "q8_0", "f16"} {
		if !validQuantizeType(q) {
			t.Errorf("validQuantizeType(%q) = false, want true", q)
		}
	}
	for _, q := range []string{"", "Q4_K_M", " F16 ", "q4_k", "q6_k", "iq4_xs", "excel; rm -rf", "q4_k_m\n"} {
		if validQuantizeType(q) {
			t.Errorf("validQuantizeType(%q) = true, want false", q)
		}
	}
}

// TestValidQuantOutName verifies the output-name rules: plain file names only,
// .gguf suffix appended when missing, whitespace/path tricks rejected.
func TestValidQuantOutName(t *testing.T) {
	if got, ok := validQuantOutName("model-q4_k_m.gguf"); !ok || got != "model-q4_k_m.gguf" {
		t.Errorf("validQuantOutName(gguf) = %q, %v", got, ok)
	}
	if got, ok := validQuantOutName("model"); !ok || got != "model.gguf" {
		t.Errorf("validQuantOutName(no ext) = %q, %v, want model.gguf", got, ok)
	}
	bad := []string{"", ".", "..", "sub/dir.gguf", `sub\dir.gguf`, " x.gguf", "x.gguf ", "x\n.gguf"}
	for _, name := range bad {
		if _, ok := validQuantOutName(name); ok {
			t.Errorf("validQuantOutName(%q) accepted, want rejected", name)
		}
	}
}

// TestStartQuantizeValidation drives StartQuantize's pre-spawn rejections with
// a pinned (empty) binary resolver: no process can start during these.
func TestStartQuantizeValidation(t *testing.T) {
	withTempCwd(t)
	saveConfigState(t)
	withLanguage(t, "en")
	resetQuantizeState(t)
	origResolve := resolveQuantizeBin
	resolveQuantizeBin = func() string { return "" }
	t.Cleanup(func() { resolveQuantizeBin = origResolve })

	a := &App{}

	// Unknown quant type rejected before anything else touches the filesystem.
	err := a.StartQuantize("C:/m/model.gguf", "q6_k", "out.gguf")
	if err == nil || !strings.Contains(err.Error(), "q4_k_m") {
		t.Errorf("bad type err = %v, want the supported-vocabulary message", err)
	}

	// Missing source file.
	err = a.StartQuantize(filepath.Join("C:/m", "missing.gguf"), "q4_k_m", "out.gguf")
	if err == nil || !strings.Contains(err.Error(), "not found") {
		t.Errorf("missing src err = %v, want not-found message", err)
	}

	// Relative source path.
	err = a.StartQuantize("model.gguf", "q4_k_m", "out.gguf")
	if err == nil || !strings.Contains(err.Error(), "absolute path") {
		t.Errorf("relative src err = %v, want absolute-path message", err)
	}

	// Real source file with a bad extension.
	src := filepath.Join(t.TempDir(), "model.bin")
	if err := os.WriteFile(src, []byte("x"), 0644); err != nil {
		t.Fatal(err)
	}
	err = a.StartQuantize(src, "q4_k_m", "out.gguf")
	if err == nil || !strings.Contains(err.Error(), ".gguf") {
		t.Errorf("non-gguf src err = %v, want .gguf message", err)
	}

	// Existing output file name (the output always lands next to the source).
	ggufDir := t.TempDir()
	gguf := filepath.Join(ggufDir, "model.gguf")
	if err := os.WriteFile(gguf, []byte("GGUF..."), 0644); err != nil {
		t.Fatal(err)
	}
	existing := filepath.Join(ggufDir, "taken.gguf")
	if err := os.WriteFile(existing, []byte("x"), 0644); err != nil {
		t.Fatal(err)
	}
	err = a.StartQuantize(gguf, "q4_k_m", "taken.gguf")
	if err == nil || !strings.Contains(err.Error(), "already exists") {
		t.Errorf("existing out err = %v, want already-exists message", err)
	}

	// Everything valid but the binary missing → the install hint surfaces.
	err = a.StartQuantize(gguf, "q4_k_m", "fresh.gguf")
	if err == nil || !strings.Contains(err.Error(), "llama-quantize not found") {
		t.Errorf("missing bin err = %v, want install-hint message", err)
	}

	// A validation failure must not flip the running flag.
	quantizeMu.Lock()
	running := quantizeRunning
	quantizeMu.Unlock()
	if running {
		t.Error("quantizeRunning stuck true after validation failures")
	}
}

// TestStartQuantizeSingleFlight verifies the second start is rejected while a
// task is running (state forged directly; no process involved).
func TestStartQuantizeSingleFlight(t *testing.T) {
	withTempCwd(t)
	saveConfigState(t)
	withLanguage(t, "en")
	resetQuantizeState(t)

	ggufDir := t.TempDir()
	src := filepath.Join(ggufDir, "model.gguf")
	if err := os.WriteFile(src, []byte("GGUF..."), 0644); err != nil {
		t.Fatal(err)
	}
	origResolve := resolveQuantizeBin
	resolveQuantizeBin = func() string { return "C:/fake/llama-quantize.exe" }
	t.Cleanup(func() { resolveQuantizeBin = origResolve })

	// Forge a running task.
	quantizeMu.Lock()
	quantizeRunning = true
	quantizeMu.Unlock()

	a := &App{}
	err := a.StartQuantize(src, "q4_k_m", "out.gguf")
	if err == nil || !strings.Contains(err.Error(), "already running") {
		t.Errorf("second start err = %v, want already-running message", err)
	}

	// Cancel while running reports through the cancel handle path; forged state
	// has a nil handle → explicit error instead of a nil deref.
	err = a.CancelQuantize()
	if err == nil || !strings.Contains(err.Error(), "no quantization task") {
		t.Errorf("cancel idle err = %v, want no-task message", err)
	}

	quantizeMu.Lock()
	quantizeRunning = false
	quantizeMu.Unlock()
}

// TestGetQuantizeStatusSnapshot verifies the status snapshot reflects forged
// state and copies the log ring.
func TestGetQuantizeStatusSnapshot(t *testing.T) {
	withTempCwd(t)
	resetQuantizeState(t)

	quantAddLog("first line")
	quantAddLog("second line")
	quantizeMu.Lock()
	quantizeRunning = true
	quantizeDone = false
	quantizeSrc = "/m/src.gguf"
	quantizeOut = "/m/src-q8_0.gguf"
	quantizeType = "q8_0"
	quantizeMu.Unlock()

	a := &App{}
	status := a.GetQuantizeStatus()
	if !status.Running || status.Done {
		t.Errorf("status running/done = %v/%v, want true/false", status.Running, status.Done)
	}
	if status.SrcPath != "/m/src.gguf" || status.OutPath != "/m/src-q8_0.gguf" || status.Quant != "q8_0" {
		t.Errorf("status identity = %+v", status)
	}
	if len(status.Logs) != 2 || status.Logs[0].Text != "first line" || status.Logs[1].Text != "second line" {
		t.Errorf("status logs = %+v", status.Logs)
	}
	if status.Next != 2 {
		t.Errorf("status next = %d, want 2", status.Next)
	}
	// The snapshot must be a copy: mutating it cannot touch the ring.
	status.Logs[0].Text = "mutated"
	quantLogsMu.Lock()
	text := quantLogs[0].Text
	quantLogsMu.Unlock()
	if text != "first line" {
		t.Errorf("status logs not copied: ring[0] = %q", text)
	}
}

// resetQuantizeState zeroes the package quantize state so tests start from the
// idle condition (the zero value of the done flags is already idle).
func resetQuantizeState(t *testing.T) {
	t.Helper()
	quantizeMu.Lock()
	quantizeRunning = false
	quantizeDone = false
	quantizeSuccess = false
	quantizeError = ""
	quantizeCancel = nil
	quantizeSrc = ""
	quantizeOut = ""
	quantizeType = ""
	quantizeMu.Unlock()
	quantLogsMu.Lock()
	quantLogs = nil
	quantLogsSeq = 0
	quantLogsMu.Unlock()
	t.Cleanup(func() {
		quantizeMu.Lock()
		quantizeRunning = false
		quantizeDone = false
		quantizeSuccess = false
		quantizeError = ""
		quantizeCancel = nil
		quantizeMu.Unlock()
		quantLogsMu.Lock()
		quantLogs = nil
		quantLogsSeq = 0
		quantLogsMu.Unlock()
	})
}
