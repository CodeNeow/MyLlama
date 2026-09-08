package core

import (
	"runtime"
	"strings"
	"testing"
	"time"
)

// TestRunCmdTrimsOutput verifies runCmd captures child-process stdout and trims
// leading/trailing whitespace.
// Uses `go env GOHOSTOS`: cross-platform, no external binary or network needed.
func TestRunCmdTrimsOutput(t *testing.T) {
	out := runCmd("go", "env", "GOHOSTOS")
	if strings.TrimSpace(out) != out {
		t.Fatalf("runCmd returned untrimmed output: %q", out)
	}
	if out == "" {
		t.Fatal("runCmd(go env GOHOSTOS) returned empty output")
	}
}

// TestRunCmdMissingBinary verifies runCmd returns an empty string instead of panicking
// when the command does not exist.
func TestRunCmdMissingBinary(t *testing.T) {
	out := runCmd("myllama-no-such-binary-xyz", "--version")
	if out != "" {
		t.Fatalf("non-existent command should return empty output, got: %q", out)
	}
}

// TestRunCmdTimeout verifies a hung child process (e.g. a stalled WMI query)
// is killed after cmdTimeout and runCmd returns "" instead of blocking
// forever. Uses a short injected timeout and a command that sleeps far beyond
// it; the elapsed time must stay close to the timeout, not the sleep length.
func TestRunCmdTimeout(t *testing.T) {
	orig := cmdTimeout
	cmdTimeout = 300 * time.Millisecond
	defer func() { cmdTimeout = orig }()

	// Sleep well beyond the timeout on every platform: sleep(1) is missing on
	// Windows, where ping -n keeps the child alive for ~seconds instead.
	var name string
	var args []string
	if runtime.GOOS == "windows" {
		name = "ping"
		args = []string{"-n", "30", "127.0.0.1"} // ~30s, far above the 300ms timeout
	} else {
		name = "sleep"
		args = []string{"30"}
	}
	start := time.Now()
	out := runCmd(name, args...)
	elapsed := time.Since(start)

	if out != "" {
		t.Fatalf("timed-out command should return empty output, got: %q", out)
	}
	if elapsed > 5*time.Second {
		t.Fatalf("runCmd blocked for %v, want close to cmdTimeout=%v", elapsed, cmdTimeout)
	}
}

// TestParseGPUComputeCapability verifies the decimal compute-capability field
// from nvidia-smi (e.g. "9.0", "8.9", "12.0") is parsed directly, without the
// former /10.0 bug that turned 9.0 into 0.9.
func TestParseGPUComputeCapability(t *testing.T) {
	cases := []struct {
		in   string
		want float64
	}{
		{"9.0", 9.0},
		{"8.9", 8.9},
		{"12.0", 12.0},
		{"  7.5  ", 7.5},
		{"", 0},
		{"not-a-number", 0},
	}
	for _, c := range cases {
		if got := parseGPUComputeCapability(c.in); got != c.want {
			t.Errorf("parseGPUComputeCapability(%q) = %v, want %v", c.in, got, c.want)
		}
	}
}

// TestParseWindowsSystemJSON verifies the batched Windows PowerShell JSON
// (one process for CPU model, cores, total/free memory) parses into the
// snapshot, and that garbage yields a zero snapshot instead of panicking.
func TestParseWindowsSystemJSON(t *testing.T) {
	got := parseWindowsSystemJSON(`{"cpuModel":"Intel(R) Core(TM) i7-13700K","cpuCores":16,"totalMem":34359738368,"freeMem":16777216}`)
	want := windowsSystemSnapshot{
		cpuModel:      "Intel(R) Core(TM) i7-13700K",
		cpuCores:      16,
		totalMemBytes: 34359738368,
		freeMemKB:     16777216,
	}
	if got != want {
		t.Errorf("parseWindowsSystemJSON valid = %+v, want %+v", got, want)
	}
	if s := parseWindowsSystemJSON("not json"); s != (windowsSystemSnapshot{}) {
		t.Errorf("parseWindowsSystemJSON garbage = %+v, want zero snapshot", s)
	}
	if s := parseWindowsSystemJSON(""); s != (windowsSystemSnapshot{}) {
		t.Errorf("parseWindowsSystemJSON empty = %+v, want zero snapshot", s)
	}
}

// TestParseGPUInfoCSV verifies the nvidia-smi CSV parsing with the UUID column:
// full 6-column lines parse name/uuid/memory/driver/compute-cap in column order,
// the UUID is kept verbatim (stable serving-GPU identifier), CRLF-terminated
// lines (real Windows nvidia-smi output) parse cleanly, short lines are skipped
// without panicking, and empty output yields nil.
func TestParseGPUInfoCSV(t *testing.T) {
	out := "NVIDIA GeForce RTX 5070 Ti, GPU-aaaaaaaa-bbbb-cccc-dddd-eeeeeeeeeeee, 1024, 16302, 576.52, 12.0\r\n" +
		"NVIDIA GeForce RTX 3070, GPU-11111111-2222-3333-4444-555555555555, 512, 8192, 576.52, 8.6"
	gpus := parseGPUInfoCSV(out)
	if len(gpus) != 2 {
		t.Fatalf("parsed GPU count = %d, want 2", len(gpus))
	}
	first := gpus[0]
	if first.Name != "NVIDIA GeForce RTX 5070 Ti" {
		t.Errorf("Name = %q", first.Name)
	}
	if first.UUID != "GPU-aaaaaaaa-bbbb-cccc-dddd-eeeeeeeeeeee" {
		t.Errorf("UUID = %q", first.UUID)
	}
	if first.MemoryUsedMB != 1024 || first.MemoryMB != 16302 {
		t.Errorf("memory parse failed: used=%d total=%d", first.MemoryUsedMB, first.MemoryMB)
	}
	if first.DriverVersion != "576.52" || first.ComputeCapability != 12.0 {
		t.Errorf("driver/compute-cap parse failed: %q %v", first.DriverVersion, first.ComputeCapability)
	}
	if gpus[1].UUID != "GPU-11111111-2222-3333-4444-555555555555" || gpus[1].MemoryMB != 8192 || gpus[1].ComputeCapability != 8.6 {
		t.Errorf("second GPU parse failed: %+v", gpus[1])
	}

	// short line (below the 6-column format) parses with zeroed later columns
	// instead of panicking
	got := parseGPUInfoCSV("NVIDIA GeForce RTX 5070 Ti, GPU-aaaaaaaa-bbbb-cccc-dddd-eeeeeeeeeeee, 1024")
	if len(got) != 1 || got[0].MemoryMB != 0 || got[0].DriverVersion != "" || got[0].UUID != "GPU-aaaaaaaa-bbbb-cccc-dddd-eeeeeeeeeeee" {
		t.Errorf("short line should parse with zeroed later columns, got %+v", got)
	}

	if got := parseGPUInfoCSV(""); got != nil {
		t.Errorf("empty output should yield nil, got %+v", got)
	}
	if got := parseGPUInfoCSV("garbage"); len(got) != 0 {
		t.Errorf("garbage without commas should yield no GPUs, got %+v", got)
	}
}
