package core

// Measured RAM bandwidth calibration for the auto-tuner.
//
// The cpu-moe plan (MoE experts served from system RAM) decodes at a speed
// bounded by memory read bandwidth, yet the static tuner rules cannot tell a
// 12 GB/s single-channel DDR4 machine from a 60 GB/s dual-channel DDR5 one.
// This file measures the machine's real all-core streaming read bandwidth
// once, caches the result keyed by a hardware fingerprint
// (myllama-benchcache.json, atomic write; legacy llama-desktop-benchcache.json
// is renamed on first load) and feeds it into
// tuneModelConfig as hw.RAMBandwidthGBs, where it gates exactly one
// preference flip: a cramped full-offload plan vs the cpu-moe plan (see
// autotune.go). Every failure path degrades to "unknown" (bandwidth 0),
// which keeps the tuner byte-for-byte on its static rules; the benchmark
// only ever costs a bounded ~1-2 s on the first tune click and never fails
// the tune.
//
// Design borrowed from FreeToken's moe/benchbw.py (philosophy only): measure
// with a real streaming kernel over a working set far larger than any
// last-level cache, keep the reduction alive to defeat dead-code
// elimination, and persist one profile per hardware identity, rejecting
// stale or cross-machine values on load.

import (
	"crypto/sha256"
	"encoding/binary"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"log"
	"os"
	"runtime"
	"sort"
	"sync"
	"time"
)

// Calibration constants.
const (
	// benchCacheVersion invalidates the cache file cleanly on any future
	// payload format change.
	benchCacheVersion = 1
	// Plausibility window: real machines sit between a slow laptop DDR4
	// (~10-20 GB/s) and top desktop DDR5 (~90 GB/s) with wide margins on
	// both ends; values outside the window are broken timers or exotic
	// sandboxes and are rejected instead of flipping tuner decisions.
	benchMinGBs = 2.0
	benchMaxGBs = 500.0
	// Hard buffer guard rails for measureRAMBandwidth: at least 64 KiB
	// (meaningfully past L1/L2) and at most 64 GiB (allocation-bomb defense;
	// the production path picks a few hundred MiB anyway).
	benchBufferMinBytes = 1 << 16
	benchBufferMaxBytes = 64 << 30
	// Upper bound on timed passes so an injected pass count cannot stretch
	// the call; the default of 3 plus one warmup stays well inside ~3 s.
	benchPassesMax = 16
	// Upper bound on read-pass repetitions inside one timed sample
	// (burst-until-measurable): a small working set can complete inside the
	// platform clock's resolution, rounding a sample's elapsed to zero, so
	// the sample repeats until the clock registers a positive interval.
	// Real machines clear that floor within a few repetitions; the cap only
	// stops a pathologically frozen clock from looping forever.
	benchSampleRepsMax = 1024
	// Smallest working set benchBufferFor will use: past every L1/L2 and
	// most L3 slices even when free RAM is scarce (an all-cache working set
	// would report inflated cache bandwidth, so near-OOM boxes measure at
	// this floor rather than shrinking further).
	benchWorkingSetFloorBytes = 8 << 20
)

// Injection points (same style as cmdTimeout): tests shrink the working set
// and pass count or swap the measurement function to keep the suite fast and
// deterministic.
var (
	// benchBufferBytes is the benchmark working set for the production path:
	// 512 MiB stays far above any consumer last-level cache (including
	// 96 MB 3D-V-Cache parts), so every timed pass reads DRAM; the default
	// four passes (warmup + 3 timed) move ~2 GiB, i.e. ~1 s worst case at
	// the 2 GB/s window floor and ~35 ms on a 60 GB/s machine.
	benchBufferBytes = int64(512) << 20
	// benchTimedPasses is the number of timed passes after the untimed
	// warmup (median over the per-pass samples).
	benchTimedPasses = 3
	// benchMeasureFn is the measurement entry point so cache tests can
	// inject stub values instead of running the real kernel.
	benchMeasureFn = measureRAMBandwidth
)

// benchCacheFile is the calibration cache path override: the bare default
// means "resolve via benchCacheFilePath" (cwd-relative on Windows, under the
// app-data base on other platforms, see paths.go); tests assign an explicit
// path to pin the location.
var benchCacheFile = benchCacheFileName

// benchCacheFilePath resolves the active calibration cache path: an explicit
// benchCacheFile override wins, otherwise the per-OS default applies.
func benchCacheFilePath() string {
	if benchCacheFile != benchCacheFileName {
		return benchCacheFile
	}
	return resolveStateFile(benchCacheFileName)
}

// migrateLegacyBenchCache performs the one-time llama-desktop → myllama
// cache-file rename so an upgraded install keeps its measured bandwidth
// instead of re-benchmarking: when the new-name file is absent and the
// legacy-name file exists, the legacy file is renamed (a pure cache — a
// rename failure only degrades to a cold cache and one re-measure, [WARN]).
// A no-op when an explicit benchCacheFile override is active (tests) or
// either file is missing.
func migrateLegacyBenchCache() {
	path := benchCacheFilePath()
	if _, err := os.Stat(path); err == nil {
		return
	}
	if benchCacheFile != benchCacheFileName {
		return
	}
	legacy := resolveStateFile(legacyBenchCacheFileName)
	if _, err := os.Stat(legacy); err != nil {
		return
	}
	if err := os.Rename(legacy, path); err != nil {
		log.Printf("[WARN] tune: cannot migrate legacy RAM bandwidth cache %s: %v (re-measuring)", legacy, err)
		return
	}
	log.Printf("[OK] tune: migrated legacy RAM bandwidth cache %s -> %s", legacy, path)
}

// benchSink keeps every pass checksum alive: folding it into a package-level
// variable leaves the compiler no license to dead-code-eliminate the loads
// whose traffic the benchmark exists to time (FreeToken keeps the reductions
// reachable for the same reason). The sink is provably observed: its final
// value is read into the plausibility-window error message, so every write
// feeding it must stay.
var benchSink uint64

// benchMu single-flights calibration: two concurrent TuneModelConfig calls
// must run the benchmark once — the second caller blocks here, then hits the
// freshly written cache.
var benchMu sync.Mutex

// benchReadPass streams the whole buffer once: each of the workers
// goroutines reads a private, disjoint, 8-byte-aligned region with unrolled
// uint64 loads (eight independent accumulator chains sustain two loads per
// cycle per core, so the compute loop never bottlenecks below DRAM speed)
// and folds its bytes into sums[idx]. Integer sums — never floats — keep
// arbitrary memory contents free of denormal/NaN stalls. Blocks until every
// worker is done.
func benchReadPass(buf []byte, workers int, sums []uint64) {
	per := (len(buf) / 8 / workers) * 8 // bytes per worker, 8-aligned
	var wg sync.WaitGroup
	wg.Add(workers)
	for i := 0; i < workers; i++ {
		go func(idx int) {
			defer wg.Done()
			region := buf[idx*per : (idx+1)*per]
			var s0, s1, s2, s3, s4, s5, s6, s7 uint64
			off := 0
			for ; off+64 <= len(region); off += 64 {
				s0 += binary.LittleEndian.Uint64(region[off:])
				s1 += binary.LittleEndian.Uint64(region[off+8:])
				s2 += binary.LittleEndian.Uint64(region[off+16:])
				s3 += binary.LittleEndian.Uint64(region[off+24:])
				s4 += binary.LittleEndian.Uint64(region[off+32:])
				s5 += binary.LittleEndian.Uint64(region[off+40:])
				s6 += binary.LittleEndian.Uint64(region[off+48:])
				s7 += binary.LittleEndian.Uint64(region[off+56:])
			}
			for ; off+8 <= len(region); off += 8 {
				s0 += binary.LittleEndian.Uint64(region[off:])
			}
			sums[idx] = s0 ^ s1 ^ s2 ^ s3 ^ s4 ^ s5 ^ s6 ^ s7
		}(i)
	}
	wg.Wait()
}

// foldSums collapses the per-worker checksums into one value.
func foldSums(sums []uint64) uint64 {
	var v uint64
	for _, s := range sums {
		v ^= s
	}
	return v
}

// measureRAMBandwidth runs an all-core streaming read benchmark over one
// buffer of bufferBytes bytes and returns the median read bandwidth of the
// timed passes in decimal GB/s (1e9 bytes per second — the same decimal
// unit RAM marketing uses, so the numbers compare directly; byte counts
// below carry the matching 1e9 divisor).
//
// Shape: runtime.GOMAXPROCS(0) workers over disjoint regions, a write
// first-touch (pages become private, dirty memory — never-written zero pages
// can be served through shared zero-page mappings and would not measure true
// DRAM), one untimed warmup read pass so page-fault cost never lands in a
// timed pass, then benchTimedPasses timed samples measured by wall clock.
// Each sample is burst-until-measurable: it repeats the read pass,
// accumulating bytes and elapsed, until the clock registers a positive
// interval — a small working set can complete inside the platform clock's
// resolution (Windows time.Now granularity rounds a ~0.5 ms pass to zero),
// which would otherwise yield a zero-length sample. The sample's bandwidth
// is accumulated bytes / accumulated elapsed (stragglers included — that is
// the true achieved bandwidth) and the median across samples absorbs
// scheduler hiccups. runtime.GC() runs first so a pending collection does
// not steal cycles mid-pass.
//
// Bounded by construction: the call allocates only bufferBytes and reads
// (benchTimedPasses+1) x bufferBytes per sample burst — at the default
// working set one repetition per sample is already ~8 ms, so production
// totals stay ~2 GiB / ~1 s worst case. Values outside the [2, 500] GB/s
// plausibility window return an error; the caller falls back to the static
// tuner rules rather than trusting a garbage number.
func measureRAMBandwidth(bufferBytes int64) (float64, error) {
	if bufferBytes < benchBufferMinBytes || bufferBytes > benchBufferMaxBytes {
		return 0, fmt.Errorf("benchmark buffer size %d bytes outside [%d, %d]",
			bufferBytes, benchBufferMinBytes, benchBufferMaxBytes)
	}
	workers := runtime.GOMAXPROCS(0)
	if workers < 1 {
		workers = 1
	}
	if bufferBytes/int64(workers) < 8 {
		return 0, fmt.Errorf("benchmark buffer %d bytes too small for %d workers",
			bufferBytes, workers)
	}
	regionBytes := (bufferBytes / int64(workers) / 8 * 8) * int64(workers)

	passes := benchTimedPasses
	if passes < 1 {
		passes = 1
	} else if passes > benchPassesMax {
		passes = benchPassesMax
	}

	runtime.GC()
	buf := make([]byte, bufferBytes)
	sums := make([]uint64, workers)

	// Write first-touch (one byte per cache line is enough): marks every
	// page private and dirty before the read passes, and gives the sink
	// checksum non-degenerate content.
	for i := 0; i < len(buf); i += 64 {
		buf[i] = byte(i >> 12)
	}

	// Untimed warmup: finish faulting every page in and warm up the
	// instruction path.
	benchReadPass(buf, workers, sums)
	benchSink ^= foldSums(sums)

	samples := make([]float64, 0, passes)
	for i := 0; i < passes; i++ {
		// Burst-until-measurable sampling: repeat the read pass inside the
		// same timed sample, accumulating bytes and elapsed, until the clock
		// registers a positive interval. A small working set (e.g. the
		// smoke-test buffer) can complete a pass inside the platform clock's
		// resolution, rounding elapsed to zero; at the production working
		// set one repetition is already ~8 ms, so the first pass always wins
		// and production behavior and numbers are unchanged.
		var sampleBytes int64
		var elapsed time.Duration
		for rep := 0; rep < benchSampleRepsMax && elapsed <= 0; rep++ {
			start := time.Now()
			benchReadPass(buf, workers, sums)
			elapsed += time.Since(start)
			sampleBytes += regionBytes
			benchSink ^= foldSums(sums)
		}
		if elapsed <= 0 {
			return 0, fmt.Errorf("clock advanced zero during a %d-byte read pass", regionBytes)
		}
		samples = append(samples, float64(sampleBytes)/elapsed.Seconds()/1e9)
	}
	sort.Float64s(samples)
	median := samples[len(samples)/2] // middle sample (upper middle when even)
	if !(median >= benchMinGBs && median <= benchMaxGBs) {
		// The negated form also rejects NaN/Inf, which plain bounds checks
		// would let through. The checksum doubles as the observable read
		// that keeps every pass reduction (benchSink) alive.
		return 0, fmt.Errorf("measured RAM bandwidth %g GB/s outside the plausibility window [%g, %g] (buffer checksum %d)",
			median, benchMinGBs, benchMaxGBs, benchSink)
	}
	return median, nil
}

// hardwareFingerprint derives the calibration-cache key from the hardware
// inputs the tuner plans against: GPUVendor, VRAMMB, RAMTotalGB,
// PhysicalCores, LogicalCPUs and the CPU model name. CPUInfo exposes Model,
// so it is carried in tuneHardware.CPUModel and included — the CPU package
// is the most identifying component of the memory subsystem (and the six
// fields alone would collide across a same-board CPU swap). RAMFreeGB is
// deliberately excluded: it changes every second and would invalidate the
// cache on every call, while the measured bandwidth depends on the memory
// subsystem, not on current occupancy. The hash is the first 16 hex chars
// of a SHA-256 over a canonical field-labeled string: deterministic across
// runs, and any single component change changes the string.
func hardwareFingerprint(hw tuneHardware) string {
	h := sha256.New()
	fmt.Fprintf(h, "v%d|gpu=%s|vram=%d|ramTotal=%.3f|cores=%d|logical=%d|cpu=%s",
		benchCacheVersion, hw.GPUVendor, hw.VRAMMB, hw.RAMTotalGB,
		hw.PhysicalCores, hw.LogicalCPUs, hw.CPUModel)
	return hex.EncodeToString(h.Sum(nil))[:16]
}

// benchCachePayload is the on-disk calibration record. Version guards future
// payload format changes: a mismatch invalidates cleanly instead of
// misreading old shapes.
type benchCachePayload struct {
	Version     int       `json:"version"`
	Fingerprint string    `json:"fingerprint"`
	RAMGBs      float64   `json:"ramGBs"`
	MeasuredAt  time.Time `json:"measuredAt"`
}

// loadBenchCache returns the cached bandwidth when the record is readable,
// carries the current version, matches the fingerprint and sits inside the
// plausibility window. Anything else — missing file, corrupt JSON, stale or
// cross-machine fingerprint, garbage value — is a miss. A corrupt file is
// only ever a [WARN]: cache trouble must never fail the tune.
func loadBenchCache(fingerprint string) (float64, bool) {
	migrateLegacyBenchCache()
	data, err := os.ReadFile(benchCacheFilePath())
	if err != nil {
		if !os.IsNotExist(err) {
			log.Printf("[WARN] tune: reading RAM bandwidth cache: %v", err)
		}
		return 0, false
	}
	var rec benchCachePayload
	if err := json.Unmarshal(data, &rec); err != nil {
		log.Printf("[WARN] tune: RAM bandwidth cache is corrupt, re-measuring: %v", err)
		return 0, false
	}
	if rec.Version != benchCacheVersion || rec.Fingerprint != fingerprint {
		return 0, false
	}
	if !(rec.RAMGBs >= benchMinGBs && rec.RAMGBs <= benchMaxGBs) {
		log.Printf("[WARN] tune: cached RAM bandwidth %g GB/s out of the plausibility window, re-measuring", rec.RAMGBs)
		return 0, false
	}
	return rec.RAMGBs, true
}

// saveBenchCache persists the calibration record atomically (tmp + fsync +
// atomic rename via atomicWriteFile, mirroring FreeToken's pidfile save)
// with 0644, matching the handover record. Persistence is best-effort: a
// failure only costs one re-measure on the next call.
func saveBenchCache(fingerprint string, ramGBs float64) {
	data, err := json.Marshal(benchCachePayload{
		Version:     benchCacheVersion,
		Fingerprint: fingerprint,
		RAMGBs:      ramGBs,
		MeasuredAt:  time.Now(),
	})
	if err != nil {
		log.Printf("[WARN] tune: encoding RAM bandwidth cache: %v", err)
		return
	}
	if err := atomicWriteFile(benchCacheFilePath(), data, 0644); err != nil {
		log.Printf("[WARN] tune: persisting RAM bandwidth cache: %v", err)
	}
}

// benchBufferFor picks the benchmark working set: the 512 MiB default,
// capped under a quarter of the currently free RAM when the snapshot knows
// it (never crowd the machine mid-benchmark), floored so a near-OOM box
// still measures something sane instead of an all-cache number.
func benchBufferFor(hw tuneHardware) int64 {
	buf := benchBufferBytes
	if hw.RAMFreeGB > 0 {
		if budget := int64(hw.RAMFreeGB * (1 << 30) / 4); budget > 0 && buf > budget {
			buf = budget
		}
	}
	if buf < benchWorkingSetFloorBytes {
		buf = benchWorkingSetFloorBytes
	}
	return buf
}

// getCalibratedRAMBandwidth returns the machine's measured all-core RAM read
// bandwidth in decimal GB/s and whether it is usable. Order: cache hit (same
// hardware fingerprint, current version, plausible value) → use it; else
// measure once, persist, and report. On measurement failure it logs [WARN]
// and returns (0, false) — the tuner keeps its static rules — and the
// failure is deliberately not cached, so a transient hiccup costs one retry
// on the next tune click. The benchMu critical section spans the whole
// measurement: that is the single-flight (concurrent callers measure once
// and share the value).
func getCalibratedRAMBandwidth(hw tuneHardware) (float64, bool) {
	benchMu.Lock()
	defer benchMu.Unlock()

	fingerprint := hardwareFingerprint(hw)
	if bw, ok := loadBenchCache(fingerprint); ok {
		log.Printf("[OK] tune: RAM bandwidth %.1f GB/s (cached=true)", bw)
		return bw, true
	}
	log.Println("[INFO] tune: measuring RAM bandwidth...")
	bw, err := benchMeasureFn(benchBufferFor(hw))
	if err != nil {
		log.Printf("[WARN] tune: RAM bandwidth measurement failed: %v", err)
		return 0, false
	}
	saveBenchCache(fingerprint, bw)
	log.Printf("[OK] tune: RAM bandwidth %.1f GB/s (cached=false)", bw)
	return bw, true
}
