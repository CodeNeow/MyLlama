package core

import (
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

// stubBench redirects the calibration cache into a temp dir and swaps the
// measurement function for a counting stub; everything is restored via
// t.Cleanup. Returns the call counter (use atomic loads to read it).
func stubBench(t *testing.T, value float64, err error, delay time.Duration) *int64 {
	t.Helper()
	oldFile, oldFn := benchCacheFile, benchMeasureFn
	benchCacheFile = filepath.Join(t.TempDir(), "bench.json")
	calls := new(int64)
	benchMeasureFn = func(bufferBytes int64) (float64, error) {
		atomic.AddInt64(calls, 1)
		if delay > 0 {
			time.Sleep(delay)
		}
		return value, err
	}
	t.Cleanup(func() { benchCacheFile, benchMeasureFn = oldFile, oldFn })
	return calls
}

// stubBenchHW is a stable fingerprint input shared by the cache tests.
func stubBenchHW() tuneHardware {
	return tuneHardware{
		GPUVendor: vendorNvidia, VRAMMB: 12288, RAMTotalGB: 31, RAMFreeGB: 18,
		PhysicalCores: 8, LogicalCPUs: 16, CPUModel: "Test CPU Model",
	}
}

// ─── hardwareFingerprint ─────────────────────────────────────────

// TestHardwareFingerprint verifies determinism (same inputs twice → same
// hash), the 16-hex-char shape, sensitivity (each fingerprint component
// changed → different hash) and the deliberate insensitivity to RAMFreeGB
// (it changes every second and must not invalidate the cache).
func TestHardwareFingerprint(t *testing.T) {
	base := stubBenchHW()
	h1 := hardwareFingerprint(base)
	if h2 := hardwareFingerprint(base); h1 != h2 {
		t.Errorf("same inputs produced different fingerprints %q / %q", h1, h2)
	}
	if len(h1) != 16 {
		t.Errorf("fingerprint length = %d, want 16 hex chars", len(h1))
	}

	sensitive := []struct {
		name string
		mut  func(*tuneHardware)
	}{
		{"GPUVendor", func(hw *tuneHardware) { hw.GPUVendor = vendorAMD }},
		{"VRAMMB", func(hw *tuneHardware) { hw.VRAMMB = 8192 }},
		{"RAMTotalGB", func(hw *tuneHardware) { hw.RAMTotalGB = 62 }},
		{"PhysicalCores", func(hw *tuneHardware) { hw.PhysicalCores = 16 }},
		{"LogicalCPUs", func(hw *tuneHardware) { hw.LogicalCPUs = 32 }},
		{"CPUModel", func(hw *tuneHardware) { hw.CPUModel = "Different CPU" }},
	}
	for _, s := range sensitive {
		changed := base
		s.mut(&changed)
		if got := hardwareFingerprint(changed); got == h1 {
			t.Errorf("%s change did not change the fingerprint", s.name)
		}
	}

	freeChanged := base
	freeChanged.RAMFreeGB = 3
	if got := hardwareFingerprint(freeChanged); got != h1 {
		t.Error("RAMFreeGB must not affect the fingerprint (cache would invalidate every call)")
	}
}

// ─── calibration cache ───────────────────────────────────────────

// TestBenchCacheRoundTripHit verifies the happy path: the first call
// measures (once), the second call hits the cache with the same value and
// without a second underlying measurement.
func TestBenchCacheRoundTripHit(t *testing.T) {
	calls := stubBench(t, 42.7, nil, 0)
	hw := stubBenchHW()

	bw, ok := getCalibratedRAMBandwidth(hw)
	if !ok || bw != 42.7 {
		t.Fatalf("first call = (%v, %v), want (42.7, true)", bw, ok)
	}
	if got := atomic.LoadInt64(calls); got != 1 {
		t.Fatalf("underlying measurements after first call = %d, want 1", got)
	}
	bw, ok = getCalibratedRAMBandwidth(hw)
	if !ok || bw != 42.7 {
		t.Fatalf("second call = (%v, %v), want (42.7, true)", bw, ok)
	}
	if got := atomic.LoadInt64(calls); got != 1 {
		t.Errorf("second call must hit the cache, underlying measurements = %d", got)
	}
}

// TestBenchCacheCorruptRemeasures verifies a corrupt cache file is a [WARN]
// miss: the calibration re-measures, persists a valid record and the next
// call hits.
func TestBenchCacheCorruptRemeasures(t *testing.T) {
	calls := stubBench(t, 50, nil, 0)
	hw := stubBenchHW()
	if err := os.WriteFile(benchCacheFile, []byte("{not valid json"), 0644); err != nil {
		t.Fatal(err)
	}

	bw, ok := getCalibratedRAMBandwidth(hw)
	if !ok || bw != 50 {
		t.Fatalf("corrupt cache must re-measure, got (%v, %v)", bw, ok)
	}
	if got := atomic.LoadInt64(calls); got != 1 {
		t.Fatalf("underlying measurements = %d, want 1", got)
	}
	if _, ok := getCalibratedRAMBandwidth(hw); !ok {
		t.Fatal("re-persisted record must be a hit on the next call")
	}
	if got := atomic.LoadInt64(calls); got != 1 {
		t.Errorf("underlying measurements after re-persist = %d, want 1", got)
	}
}

// TestBenchCacheStaleFingerprint verifies a record written under a different
// hardware fingerprint (other machine / changed component) is a miss.
func TestBenchCacheStaleFingerprint(t *testing.T) {
	calls := stubBench(t, 51, nil, 0)
	hw := stubBenchHW()
	saveBenchCache(hardwareFingerprint(hw)+"-other", 30)

	bw, ok := getCalibratedRAMBandwidth(hw)
	if !ok || bw != 51 {
		t.Fatalf("stale fingerprint must re-measure, got (%v, %v)", bw, ok)
	}
	if got := atomic.LoadInt64(calls); got != 1 {
		t.Errorf("underlying measurements = %d, want 1", got)
	}
}

// TestBenchCacheOutOfRangeValue verifies cached values outside the
// plausibility window are treated as misses on both ends of the window.
func TestBenchCacheOutOfRangeValue(t *testing.T) {
	calls := stubBench(t, 52, nil, 0)
	hw := stubBenchHW()
	fp := hardwareFingerprint(hw)

	for _, bad := range []float64{benchMinGBs - 1, benchMaxGBs + 1} {
		saveBenchCache(fp, bad)
		if _, ok := loadBenchCache(fp); ok {
			t.Errorf("cached %g GB/s must be a miss", bad)
		}
		if bw, ok := getCalibratedRAMBandwidth(hw); !ok || bw != 52 {
			t.Errorf("out-of-window cache must re-measure, got (%v, %v)", bw, ok)
		}
	}
	if got := atomic.LoadInt64(calls); got != 2 {
		t.Errorf("underlying measurements = %d, want 2 (one per out-of-window lookup)", got)
	}
}

// TestBenchFailureNotCached verifies a failed measurement returns (0, false),
// writes no cache file, and is retried (not remembered) on the next call.
func TestBenchFailureNotCached(t *testing.T) {
	calls := stubBench(t, 0, fmt.Errorf("injected bench failure"), 0)
	hw := stubBenchHW()

	if bw, ok := getCalibratedRAMBandwidth(hw); ok || bw != 0 {
		t.Fatalf("failed measurement = (%v, %v), want (0, false)", bw, ok)
	}
	if got := atomic.LoadInt64(calls); got != 1 {
		t.Fatalf("underlying measurements = %d, want 1", got)
	}
	if _, err := os.Stat(benchCacheFile); !os.IsNotExist(err) {
		t.Errorf("failed measurement must not be cached (stat err = %v)", err)
	}
	if _, ok := getCalibratedRAMBandwidth(hw); ok {
		t.Fatal("next call must retry the measurement, not report success")
	}
	if got := atomic.LoadInt64(calls); got != 2 {
		t.Errorf("underlying measurements = %d, want 2 (failures are never cached)", got)
	}
}

// TestBenchSingleFlight verifies N concurrent getCalibratedRAMBandwidth
// calls run the underlying measurement exactly once and all get the same
// value (the benchMu critical section spans the measurement).
func TestBenchSingleFlight(t *testing.T) {
	calls := stubBench(t, 44, nil, 30*time.Millisecond) // widen the race window
	hw := stubBenchHW()

	const n = 8
	results := make([]float64, n)
	oks := make([]bool, n)
	var wg sync.WaitGroup
	wg.Add(n)
	for i := 0; i < n; i++ {
		go func(idx int) {
			defer wg.Done()
			results[idx], oks[idx] = getCalibratedRAMBandwidth(hw)
		}(i)
	}
	wg.Wait()

	if got := atomic.LoadInt64(calls); got != 1 {
		t.Errorf("underlying measurements = %d, want exactly 1", got)
	}
	for i := range results {
		if !oks[i] || results[i] != 44 {
			t.Errorf("caller %d got (%v, %v), want (44, true)", i, results[i], oks[i])
		}
	}
}

// ─── measureRAMBandwidth ─────────────────────────────────────────

// TestMeasureRAMBandwidthSmoke runs the real benchmark kernel over a small
// injected 128 MiB working set (vs the 512 MiB production default) with a
// shortened pass count: the value must be plausible (> 0, inside the
// window), the call fast enough for CI, and degenerate buffer sizes must
// error instead of measuring garbage. 128 MiB keeps a pass comfortably
// above the platform clock's resolution even on fast machines, so the
// burst-until-measurable repetition rarely engages.
func TestMeasureRAMBandwidthSmoke(t *testing.T) {
	if raceEnabled {
		t.Skip("the benchmark asserts real memory throughput; race instrumentation slows memory traffic by about an order of magnitude, so the measured value necessarily falls outside the plausibility window - environmental, not a regression")
	}
	oldPasses := benchTimedPasses
	benchTimedPasses = 2
	defer func() { benchTimedPasses = oldPasses }()

	start := time.Now()
	bw, err := measureRAMBandwidth(128 << 20)
	elapsed := time.Since(start)
	if err != nil {
		t.Fatalf("measureRAMBandwidth(128MiB) failed: %v", err)
	}
	if elapsed > 2*time.Second {
		t.Errorf("benchmark took %v, want < 2s", elapsed)
	}
	if !(bw > 0 && bw >= benchMinGBs && bw <= benchMaxGBs) {
		t.Errorf("measured bandwidth %g GB/s outside [%g, %g]", bw, benchMinGBs, benchMaxGBs)
	}
	t.Logf("measured RAM bandwidth on this machine: %.1f GB/s", bw)

	// Degenerate buffer sizes must error, never measure.
	for _, bad := range []int64{0, 1024, benchBufferMaxBytes + 1} {
		if _, err := measureRAMBandwidth(bad); err == nil {
			t.Errorf("buffer size %d must error", bad)
		}
	}
}
