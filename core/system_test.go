package core

import "testing"

// ─── parseLinuxCPUModel ──────────────────────────────────────────

// TestParseLinuxCPUModel verifies extracting the "model name" line from /proc/cpuinfo
// text and stripping whitespace after the colon.
func TestParseLinuxCPUModel(t *testing.T) {
	out := "processor\t: 0\n" +
		"model name\t: Intel(R) Core(TM) i7-10700K CPU @ 3.80GHz\n" +
		"stepping\t: 5\n"
	if got := parseLinuxCPUModel(out); got != "Intel(R) Core(TM) i7-10700K CPU @ 3.80GHz" {
		t.Errorf("parseLinuxCPUModel = %q", got)
	}
	if got := parseLinuxCPUModel("no model here"); got != "" {
		t.Errorf("no model name line should return empty string, got %q", got)
	}
}

// TestCountString verifies counting lines by substring (used for Linux core count
// "processor" lines).
func TestCountString(t *testing.T) {
	out := "processor\t: 0\nprocessor\t: 1\nprocessor\t: 2\nmodel\t: x\n"
	if got := countString(out, "processor"); got != 3 {
		t.Errorf("countString = %d, want 3", got)
	}
}

// TestParseCoresDarwin verifies darwin core-count string parsing; illegal input falls
// back to 0. The empty case is the real Intel-Mac path for
// hw.perflevel0.physicalcpu (key does not exist, sysctl prints nothing) — 0
// means "split unknown", so the tuner must not cap.
func TestParseCoresDarwin(t *testing.T) {
	if got := parseCoresDarwin("8"); got != 8 {
		t.Errorf("parseCoresDarwin(8) = %d", got)
	}
	if got := parseCoresDarwin("abc"); got != 0 {
		t.Errorf("parseCoresDarwin(abc) = %d, want 0", got)
	}
	if got := parseCoresDarwin(""); got != 0 {
		t.Errorf("parseCoresDarwin(empty) = %d, want 0", got)
	}
}

// ─── parseMemInfo / parseVMStat ──────────────────────────────────

// TestParseMemInfo verifies extracting the kB value for a specified key from
// /proc/meminfo.
func TestParseMemInfo(t *testing.T) {
	out := "MemTotal:       33554432 kB\n" +
		"MemFree:        10000000 kB\n" +
		"MemAvailable:   20000000 kB\n"
	if got := parseMemInfo(out, "MemTotal"); got != 33554432 {
		t.Errorf("MemTotal = %d, want 33554432", got)
	}
	if got := parseMemInfo(out, "MemAvailable"); got != 20000000 {
		t.Errorf("MemAvailable = %d, want 20000000", got)
	}
	if got := parseMemInfo(out, "SwapTotal"); got != 0 {
		t.Errorf("non-existent key should return 0, got %d", got)
	}
}

// TestParseVMStat verifies parsing "Pages free: N" form from darwin vm_stat output.
func TestParseVMStat(t *testing.T) {
	out := "Mach Virtual Memory Statistics: (page size of 16384 bytes)\n" +
		"Pages free:                            123456.\n" +
		"Pages active:                          456789.\n"
	if got := parseVMStat(out, "free"); got != 123456 {
		t.Errorf("parseVMStat(free) = %d, want 123456", got)
	}
}

// TestDefaultServerConfig verifies server config default-value fallback (accessMode local,
// host derived 127.0.0.1, port 8080).
func TestDefaultServerConfig(t *testing.T) {
	cfg := defaultServerConfig()
	if cfg.AccessMode != accessLocal || cfg.Host != "127.0.0.1" || cfg.Port != 8080 || cfg.MaxModels != 1 || cfg.CacheRAM != 8192 {
		t.Errorf("default server config does not match expected: %+v", cfg)
	}
}
