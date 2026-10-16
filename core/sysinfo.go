package core

import (
	"encoding/json"
	"fmt"
	"os"
	"runtime"
	"strconv"
	"strings"
	"sync"
)

// ─── System info ────────────────────────────────────────────────
// Hardware/system detection: CPU, memory, GPU and CUDA probes collected once
// per process and cached; the output parsers are pure, unit-testable functions.

type SystemInfo struct {
	OS       string       `json:"os"`
	Arch     string       `json:"arch"`
	CPU      CPUInfo      `json:"cpu"`
	Memory   MemoryInfo   `json:"memory"`
	GPU      []GPUInfo    `json:"gpu"`
	CUDA     CUDAInfo     `json:"cuda"`
	LlamaCpp LlamaCppInfo `json:"llamaCpp"`
	Disk     *DiskUsage   `json:"disk,omitempty"`
}

type CPUInfo struct {
	Model       string `json:"model"`
	Cores       int    `json:"cores"`
	LogicalCPUs int    `json:"logicalCpus"`
	// PerfCores is the performance-cluster core count on asymmetric CPUs:
	// Android/big.LITTLE SoCs (CPUs whose cpufreq cpuinfo_max_freq reaches 80%
	// of the highest) and Apple Silicon (hw.perflevel0.physicalcpu, the P-core
	// count). 0 = unknown, which means "no capping"; the auto-tuner limits its
	// worker pool to this count when it knows the split.
	PerfCores int `json:"perfCores,omitempty"`
}

type MemoryInfo struct {
	TotalGB float64 `json:"totalGb"`
	FreeGB  float64 `json:"freeGb"`
}

type GPUInfo struct {
	Name string `json:"name"`
	// Vendor classifies the GPU maker for platform-aware planning (asset
	// selection, auto-tune Metal branch, VRAM display rules):
	// "nvidia" | "intel" | "amd" | "apple" | "" (unknown/legacy probe paths).
	Vendor string `json:"vendor"`
	// UUID is the stable nvidia-smi device identifier ("GPU-xxxx-..."),
	// invariant across reboots and driver index changes; it is the value the
	// serving-GPU selection (ServerConfig.DeviceID / CUDA_VISIBLE_DEVICES) is
	// keyed on. Empty when the probe could not read it (always empty for
	// non-NVIDIA entries: apple / PCI-classified cards carry no UUID).
	UUID              string  `json:"uuid"`
	MemoryMB          int     `json:"memoryMb"`
	MemoryUsedMB      int     `json:"memoryUsedMb"`
	DriverVersion     string  `json:"driverVersion"`
	CUDACores         int     `json:"cudaCores"`
	ComputeCapability float64 `json:"computeCapability"`
}

type CUDAInfo struct {
	Available      bool   `json:"available"`
	DriverVersion  string `json:"driverVersion"`
	ToolkitVersion string `json:"toolkitVersion"`
}

// sysInfoCacheMu guards sysInfoCache so the six Home-page probes (fired in
// parallel by the frontend) trigger exactly one collectSystemInfo instead of
// six concurrent shell-outs.
var sysInfoCacheMu sync.Mutex
var sysInfoCache *SystemInfo

// windowsSysOnce guards the batched Windows PowerShell probe (CPU model,
// cores, total/free memory) so all Windows getters share a single process.
var windowsSysOnce sync.Once
var windowsSys windowsSystemSnapshot

// systemInfo returns the static system snapshot, computing it once. Static
// hardware facts (CPU model, GPU identity, CUDA driver) do not change during a
// session, so every binding and manual refresh reuses the same collection.
func systemInfo() SystemInfo {
	sysInfoCacheMu.Lock()
	defer sysInfoCacheMu.Unlock()
	if sysInfoCache != nil {
		return *sysInfoCache
	}
	s := collectSystemInfo()
	sysInfoCache = &s
	return s
}

func collectSystemInfo() SystemInfo {
	info := SystemInfo{
		OS:   runtime.GOOS,
		Arch: runtime.GOARCH,
		CPU:  getCPUInfo(),
		Memory: MemoryInfo{
			TotalGB: getTotalMemoryGB(),
		},
		GPU:      getGPUInfo(),
		LlamaCpp: getLlamaCppInfo(),
	}

	// Free memory
	info.Memory.FreeGB = getFreeMemoryGB()

	// CUDA: reuse the driver version already fetched by the GPU probe so the
	// collection avoids a redundant nvidia-smi call.
	var driverHint string
	if len(info.GPU) > 0 {
		driverHint = info.GPU[0].DriverVersion
	}
	info.CUDA = getCUDAInfoWithDriver(driverHint)

	// Disk usage (sampleDiskUsage returns nil on failure so it never blocks
	// other metrics)
	info.Disk = sampleDiskUsage()

	return info
}

// ─── CPU ─────────────────────────────────────────────────────────

func getCPUInfo() CPUInfo {
	info := CPUInfo{
		LogicalCPUs: runtime.NumCPU(),
	}

	switch runtime.GOOS {
	case "windows":
		w := getWindowsSystem()
		info.Model = w.cpuModel
		if w.cpuCores > 0 {
			info.Cores = w.cpuCores
		}
	case "linux", "android":
		// Android is Linux-kernel: /proc/cpuinfo exists and parses the same
		// way. Read via readProcFile — Android's app sandbox cannot exec, so
		// the read must be in-process there (procfile_android.go).
		cpuinfo := readProcFile("/proc/cpuinfo")
		info.Model = parseLinuxCPUModel(cpuinfo)
		info.Cores = countString(cpuinfo, "processor")
		if info.Model == "" {
			// No x86-style "model name" line (Android arm64, some ARM SBCs):
			// fall back to the SoC identity lines Android devices expose.
			// Desktop x86 boxes all carry "model name" and never pay for the
			// extra reads; an empty result keeps the generic
			// "(unknown model)" fallback below in charge.
			info.Model = parseAndroidCPUModel(cpuinfo, readProcFile("/system/build.prop"))
			// Best-effort big.LITTLE detection via cpufreq max frequencies;
			// any read failure leaves PerfCores 0 (= unknown, no capping).
			info.PerfCores = countPerformanceCPUs(readCPUMaxFreqs())
		}
	case "darwin":
		info.Model = runCmd("sysctl", "-n", "machdep.cpu.brand_string")
		info.Cores = parseCoresDarwin(runCmd("sysctl", "-n", "machdep.cpu.core_count"))
		// Apple Silicon is asymmetric (perflevel0 = P-cores, perflevel1 =
		// E-cores): the P-cluster count is what GEMM workers should be pinned
		// to. Intel Macs have no perflevel keys, so this stays 0 (= unknown,
		// no capping).
		info.PerfCores = parseCoresDarwin(runCmd("sysctl", "-n", "hw.perflevel0.physicalcpu"))
	}

	if info.Model == "" {
		info.Model = fmt.Sprintf("%s %s (unknown model)", runtime.GOOS, runtime.GOARCH)
	}
	if info.Cores == 0 {
		info.Cores = runtime.NumCPU()
	}

	return info
}

// ─── Memory ──────────────────────────────────────────────────────

func getTotalMemoryGB() float64 {
	switch runtime.GOOS {
	case "windows":
		if w := getWindowsSystem(); w.totalMemBytes > 0 {
			return float64(w.totalMemBytes) / (1024 * 1024 * 1024)
		}
	case "linux", "android":
		// Android is Linux-kernel: /proc/meminfo exists and parses the same
		// way (readProcFile — Android cannot exec, see getCPUInfo).
		out := readProcFile("/proc/meminfo")
		kb := parseMemInfo(out, "MemTotal")
		if kb > 0 {
			return float64(kb) / (1024 * 1024)
		}
	case "darwin":
		out := runCmd("sysctl", "-n", "hw.memsize")
		if b, err := strconv.ParseUint(strings.TrimSpace(out), 10, 64); err == nil {
			return float64(b) / (1024 * 1024 * 1024)
		}
	}
	return 0
}

func getFreeMemoryGB() float64 {
	switch runtime.GOOS {
	case "windows":
		if w := getWindowsSystem(); w.freeMemKB > 0 {
			return float64(w.freeMemKB) / (1024 * 1024)
		}
	case "linux", "android":
		// Android is Linux-kernel: /proc/meminfo exists and parses the same
		// way (readProcFile — Android cannot exec, see getCPUInfo).
		out := readProcFile("/proc/meminfo")
		kb := parseMemInfo(out, "MemAvailable")
		if kb == 0 {
			kb = parseMemInfo(out, "MemFree")
		}
		if kb > 0 {
			return float64(kb) / (1024 * 1024)
		}
	case "darwin":
		// macOS memory pressure — approximate free
		out := runCmd("vm_stat")
		freePages := parseVMStat(out, "free")
		if freePages > 0 {
			pageSizeStr := strings.TrimSpace(runCmd("sysctl", "-n", "hw.pagesize"))
			pageSize, err := strconv.ParseUint(pageSizeStr, 10, 64)
			if err != nil || pageSize == 0 {
				pageSize = 16384 // fallback for arm64
			}
			return float64(freePages*pageSize) / (1024 * 1024 * 1024)
		}
	}
	return 0
}

// windowsSystemSnapshot holds the Windows-only facts that used to require four
// separate PowerShell processes (CPU model, core count, total/free memory).
// They are collected together in a single invocation and memoized per process.
type windowsSystemSnapshot struct {
	cpuModel      string
	cpuCores      int
	totalMemBytes uint64
	freeMemKB     uint64
}

// getWindowsSystem runs one PowerShell query returning CPU model, core count,
// total physical memory (bytes) and free physical memory (KB) as JSON, then
// caches the result. Every Windows getter (CPU / total memory / free memory)
// shares this, collapsing four powershell.exe launches into one.
func getWindowsSystem() windowsSystemSnapshot {
	windowsSysOnce.Do(func() {
		out := runCmd("powershell", "-NoProfile", "-Command", `
$cs = Get-CimInstance Win32_ComputerSystem
$cpu = Get-CimInstance Win32_Processor | Select-Object -First 1
$os = Get-CimInstance Win32_OperatingSystem
[PSCustomObject]@{
  cpuModel = $cpu.Name
  cpuCores = $cpu.NumberOfCores
  totalMem = $cs.TotalPhysicalMemory
  freeMem  = $os.FreePhysicalMemory
} | ConvertTo-Json -Compress`)
		windowsSys = parseWindowsSystemJSON(out)
	})
	return windowsSys
}

// parseWindowsSystemJSON parses the batched PowerShell JSON into a snapshot.
// Invalid/empty output yields a zero snapshot so callers fall back to their
// own defaults.
func parseWindowsSystemJSON(out string) windowsSystemSnapshot {
	var v struct {
		CpuModel string `json:"cpuModel"`
		CpuCores int    `json:"cpuCores"`
		TotalMem uint64 `json:"totalMem"`
		FreeMem  uint64 `json:"freeMem"`
	}
	if err := json.Unmarshal([]byte(out), &v); err != nil {
		return windowsSystemSnapshot{}
	}
	return windowsSystemSnapshot{
		cpuModel:      strings.TrimSpace(v.CpuModel),
		cpuCores:      v.CpuCores,
		totalMemBytes: v.TotalMem,
		freeMemKB:     v.FreeMem,
	}
}

// ─── GPU ─────────────────────────────────────────────────────────

// gpuProbesUnsupported reports whether GPU / CUDA probing must not spawn
// external processes on this platform: the Android app sandbox (the Wails v3
// target) has neither nvidia-smi nor nvcc and no desktop GPU stack, so every
// probe short-circuits instead of attempting a doomed exec per call.
func gpuProbesUnsupported() bool {
	return runtime.GOOS == "android"
}

func getGPUInfo() []GPUInfo {
	if gpuProbesUnsupported() {
		return nil
	}
	// macOS: the arm64 release embeds Metal (one Apple GPU entry), the x64
	// release is CPU-only (no GPU entry at all).
	if runtime.GOOS == "darwin" {
		return probeDarwinGPUs()
	}
	out := runCmd("nvidia-smi",
		"--query-gpu=name,uuid,memory.used,memory.total,driver_version,compute_cap",
		"--format=csv,noheader,nounits",
	)
	gpus := parseGPUInfoCSV(out)
	// Linux only: the release ships a single Vulkan tarball valid for NVIDIA,
	// AMD and Intel alike, so non-NVIDIA PCI display controllers are appended
	// after the nvidia-smi entries (no VRAM fields — sysfs offers none).
	// Windows keeps its nvidia-only CUDA probe; Android short-circuited above.
	if runtime.GOOS == "linux" {
		for _, p := range probePciGpus() {
			gpus = append(gpus, GPUInfo{Vendor: p.Vendor, Name: p.Name})
		}
	}
	return gpus
}

// probeDarwinGPUs reports the integrated Apple Silicon GPU on darwin/arm64:
// unified memory means there is no discrete VRAM to report (MemoryMB stays 0 —
// the UI hides VRAM rows for unified-memory cards). darwin/amd64 release
// builds are CPU-only (Metal OFF upstream), so no GPU entry exists there.
func probeDarwinGPUs() []GPUInfo {
	if runtime.GOARCH != "arm64" {
		return nil
	}
	return []GPUInfo{{
		Vendor: "apple",
		Name:   probeAppleGPUName(),
	}}
}

// probeAppleGPUName reads the chip name from the system_profiler displays
// report (`system_profiler SPDisplaysDataType -json` → SPDisplaysDataType[]
// .sppci_model, e.g. "Apple M4 Pro"); on any parse failure the static
// "Apple Silicon (Metal)" fallback keeps the card identifiable. system_profiler
// takes on the order of a second, so the result is cached for the process —
// the chip does not change at runtime.
var appleGPUNameOnce sync.Once
var cachedAppleGPUName string

func probeAppleGPUName() string {
	appleGPUNameOnce.Do(func() {
		cachedAppleGPUName = parseAppleGPUName(runCmd("system_profiler", "SPDisplaysDataType", "-json"))
	})
	return cachedAppleGPUName
}

// parseAppleGPUName extracts "sppci_model" from the system_profiler
// SPDisplaysDataType JSON output. Pure function, directly unit-testable.
// The report shape is {"SPDisplaysDataType":[{"spdisplays_ndrvs":[...],
// "sppci_model":"Apple M4 Pro", ...}, ...]} — the first entry's sppci_model
// wins; "" input or a missing field yields the "Apple Silicon (Metal)"
// fallback.
func parseAppleGPUName(out string) string {
	fallback := "Apple Silicon (Metal)"
	if strings.TrimSpace(out) == "" {
		return fallback
	}
	var v struct {
		GPUs []struct {
			Model string `json:"sppci_model"`
		} `json:"SPDisplaysDataType"`
	}
	if err := json.Unmarshal([]byte(out), &v); err != nil {
		return fallback
	}
	for _, g := range v.GPUs {
		if name := strings.TrimSpace(g.Model); name != "" {
			return name
		}
	}
	return fallback
}

// parseGPUInfoCSV parses the nvidia-smi CSV query output into GPUInfo entries.
// The column order must match the --query-gpu field list in getGPUInfo:
// name,uuid,memory.used,memory.total,driver_version,compute_cap. UUID sits in
// column 2 (index 1) — the stable identifier the serving-GPU selection is
// keyed on. Lines with fewer than three fields are skipped defensively; later
// columns are read only when present so a short line never panics. Empty
// output yields nil.
func parseGPUInfoCSV(out string) []GPUInfo {
	if out == "" {
		return nil
	}

	var gpus []GPUInfo
	for _, line := range strings.Split(strings.TrimSpace(out), "\n") {
		parts := strings.Split(line, ",")
		if len(parts) < 3 {
			continue
		}
		name := strings.TrimSpace(parts[0])
		uuid := strings.TrimSpace(parts[1])
		memUsedStr := strings.TrimSpace(parts[2])
		memStr := ""
		if len(parts) >= 4 {
			memStr = strings.TrimSpace(parts[3])
		}
		var driver string
		if len(parts) >= 5 {
			driver = strings.TrimSpace(parts[4])
		}

		memUsedMB := 0
		if memUsedStr != "" {
			if v, err := strconv.Atoi(memUsedStr); err == nil {
				memUsedMB = v
			}
		}
		memMB := 0
		if memStr != "" {
			if v, err := strconv.Atoi(memStr); err == nil {
				memMB = v
			}
		}

		gpu := GPUInfo{
			// nvidia-smi output is NVIDIA by definition: every entry this
			// parser produces is a CUDA-capable NVIDIA card (the VRAM-sampled
			// vendor — monitor sampling and the serving-GPU selection key on
			// these entries only).
			Vendor:        "nvidia",
			Name:          name,
			UUID:          uuid,
			MemoryMB:      memMB,
			MemoryUsedMB:  memUsedMB,
			DriverVersion: driver,
		}

		// Compute capability (6th column, index 5): nvidia-smi returns the
		// decimal form directly (e.g. "9.0", "8.9", "12.0"), NOT an integer ×10.
		if len(parts) >= 6 {
			gpu.ComputeCapability = parseGPUComputeCapability(parts[5])
		}

		gpus = append(gpus, gpu)
	}
	return gpus
}

// gpuListSource is the injection point for the detected GPU list (same style
// as probeGPUComputeCap): the default implementation returns the cached
// system-info GPU snapshot so every consumer (SaveServerConfig device
// validation, auto-tuner planning) shares one detection chain per process.
// Tests replace this variable to feed synthetic GPU lists without shelling out
// to nvidia-smi.
var gpuListSource = func() []GPUInfo {
	return systemInfo().GPU
}

// probeGPUComputeCap is the injection point for the GPU compute-capability
// probe (a package-level var in the same style as probeLlamaVersion): the
// default implementation runs `nvidia-smi --query-gpu=compute_cap` and returns
// raw stdout. Tests replace this variable instead of shelling out.
var probeGPUComputeCap = func() string {
	if gpuProbesUnsupported() {
		return ""
	}
	return runCmd("nvidia-smi", "--query-gpu=compute_cap", "--format=csv,noheader")
}

// gpuComputeCap parses the first value of the compute-capability probe output
// (e.g. "12.0" for RTX 50 series); ok=false on empty output or parse failure.
// Only the first value is used: on multi-GPU hosts the first enumerated GPU
// decides the CUDA floor.
func gpuComputeCap() (float64, bool) {
	out := strings.TrimSpace(probeGPUComputeCap())
	if out == "" {
		return 0, false
	}
	first := out
	if idx := strings.IndexAny(first, "\r\n"); idx >= 0 {
		first = first[:idx]
	}
	cc, err := strconv.ParseFloat(strings.TrimSpace(first), 64)
	if err != nil {
		return 0, false
	}
	return cc, true
}

// parseGPUComputeCapability parses the compute-capability field returned by
// `nvidia-smi --query-gpu=compute_cap` (the decimal form, e.g. "9.0", "8.9",
// "12.0"). Returns 0 on empty/garbage input so callers can treat 0 as unknown.
func parseGPUComputeCapability(s string) float64 {
	v, err := strconv.ParseFloat(strings.TrimSpace(s), 64)
	if err != nil {
		return 0
	}
	return v
}

// cudaFloorForComputeCap returns the minimum CUDA runtime version the GPU can
// actually run: Blackwell GPUs (compute capability >= 12.0, e.g. RTX 50 series)
// need CUDA >= 12.8 or binaries fail with "no kernel image"; earlier (or
// unknown) GPUs have no floor.
func cudaFloorForComputeCap(cc float64) float64 {
	if cc >= 12.0 {
		return 12.8
	}
	return 0
}

// ─── CUDA ────────────────────────────────────────────────────────

// getCUDAInfo returns CUDA availability: the driver version is taken from the
// GPU probe when driverHint is non-empty, otherwise queried via nvidia-smi.
// The collection path passes the GPU driver so it avoids a redundant nvidia-smi
// process.
func getCUDAInfo() CUDAInfo {
	return getCUDAInfoWithDriver("")
}

func getCUDAInfoWithDriver(driverHint string) CUDAInfo {
	info := CUDAInfo{}

	// Android: no nvidia-smi / nvcc exists in the app sandbox — skip both
	// spawns and report CUDA unavailable.
	if gpuProbesUnsupported() {
		return info
	}

	// Driver version: prefer the one already collected from the GPU probe.
	if driverHint != "" {
		info.Available = true
		info.DriverVersion = driverHint
	} else {
		out := runCmd("nvidia-smi", "--query-gpu=driver_version", "--format=csv,noheader")
		if out != "" {
			info.Available = true
			info.DriverVersion = strings.TrimSpace(out)
		}
	}

	// Toolkit version from nvcc
	nvccOut := runCmd("nvcc", "--version")
	if nvccOut != "" {
		// Typical output: "Cuda compilation tools, release X.Y, VX.Y.Z"
		for _, line := range strings.Split(nvccOut, "\n") {
			if strings.Contains(line, "release") {
				// Extract version after "release "
				idx := strings.Index(line, "release")
				if idx >= 0 {
					rest := line[idx+len("release"):]
					rest = strings.TrimSpace(rest)
					// Take until comma or space
					if commaIdx := strings.IndexAny(rest, ", "); commaIdx > 0 {
						rest = rest[:commaIdx]
					}
					info.ToolkitVersion = rest
				}
				break
			}
		}
	}

	return info
}

func parseLinuxCPUModel(out string) string {
	for _, line := range strings.Split(out, "\n") {
		if strings.HasPrefix(line, "model name") {
			parts := strings.SplitN(line, ":", 2)
			if len(parts) == 2 {
				return strings.TrimSpace(parts[1])
			}
		}
	}
	return ""
}

// parseAndroidCPUModel resolves the SoC identity for /proc/cpuinfo text that
// lacks an x86 "model name" line (Android arm64 devices). Lookup order:
//  1. the "Hardware :" line Android kernels append to /proc/cpuinfo
//     (e.g. "Hardware\t: Qualcomm Technologies, Inc SM8550"), used as-is;
//  2. "ro.soc.model" (+ "ro.soc.manufacturer") from /system/build.prop
//     (Android 12+), combined "Qualcomm SM8550" style;
//  3. "" — the caller keeps its generic "(unknown model)" fallback.
//
// Pure function, directly unit-testable.
func parseAndroidCPUModel(cpuinfo, buildProp string) string {
	for _, line := range strings.Split(cpuinfo, "\n") {
		if !strings.HasPrefix(line, "Hardware") {
			continue
		}
		parts := strings.SplitN(line, ":", 2)
		if len(parts) == 2 {
			if v := strings.TrimSpace(parts[1]); v != "" {
				return v
			}
		}
	}
	var manufacturer, model string
	for _, line := range strings.Split(buildProp, "\n") {
		line = strings.TrimSpace(line)
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		parts := strings.SplitN(line, "=", 2)
		if len(parts) != 2 {
			continue
		}
		switch strings.TrimSpace(parts[0]) {
		case "ro.soc.manufacturer":
			manufacturer = strings.TrimSpace(parts[1])
		case "ro.soc.model":
			model = strings.TrimSpace(parts[1])
		}
	}
	switch {
	case manufacturer != "" && model != "":
		return manufacturer + " " + model
	case model != "":
		return model
	case manufacturer != "":
		return manufacturer
	}
	return ""
}

// countPerformanceCPUs counts the CPUs whose maximum frequency reaches at
// least 80% of the highest maximum frequency: on big.LITTLE SoCs the little
// cluster clocks meaningfully lower, so this approximates the performance-core
// count. maxFreqs holds per-CPU cpuinfo_max_freq values in kHz ordered by CPU
// index. Returns 0 when the data is unavailable or degenerate (no positive
// frequency at all), so callers treat 0 as "topology unknown".
func countPerformanceCPUs(maxFreqs []int) int {
	hi := 0
	for _, f := range maxFreqs {
		if f > hi {
			hi = f
		}
	}
	if hi <= 0 {
		return 0
	}
	count := 0
	for _, f := range maxFreqs {
		// f >= 0.8*hi via integers (freqs are kHz; no overflow at *10).
		if f*10 >= hi*8 {
			count++
		}
	}
	return count
}

// readCPUMaxFreqs reads the per-CPU maximum frequencies (kHz) from
// /sys/devices/system/cpu/cpuN/cpufreq/cpuinfo_max_freq for N in 0..NumCPU-1,
// ordered by CPU index. Offline/unreadable entries are skipped; a totally
// unavailable sysfs yields nil (countPerformanceCPUs then reports 0).
func readCPUMaxFreqs() []int {
	n := runtime.NumCPU()
	if n <= 0 {
		return nil
	}
	freqs := make([]int, 0, n)
	for i := 0; i < n; i++ {
		data, err := os.ReadFile(fmt.Sprintf("/sys/devices/system/cpu/cpu%d/cpufreq/cpuinfo_max_freq", i))
		if err != nil {
			continue
		}
		if v, err := strconv.Atoi(strings.TrimSpace(string(data))); err == nil && v > 0 {
			freqs = append(freqs, v)
		}
	}
	return freqs
}

func parseCoresDarwin(out string) int {
	n, _ := strconv.Atoi(strings.TrimSpace(out))
	return n
}

func parseMemInfo(out, key string) uint64 {
	for _, line := range strings.Split(out, "\n") {
		if strings.Contains(line, key+":") {
			parts := strings.Fields(line)
			if len(parts) >= 2 {
				val, err := strconv.ParseUint(parts[len(parts)-2], 10, 64)
				if err != nil {
					val, err = strconv.ParseUint(parts[1], 10, 64)
					if err != nil {
						return 0
					}
				}
				return val
			}
		}
	}
	return 0
}

func parseVMStat(out, key string) uint64 {
	for _, line := range strings.Split(out, "\n") {
		fields := strings.Fields(line)
		if len(fields) < 2 {
			continue
		}
		// Match fields of the form "<key>:" (macOS output is "Pages free:
		// 123456." — the value follows the key field and ends with a period),
		// avoiding parse failures that would leave free memory stuck at 0.
		for i, f := range fields {
			if strings.TrimRight(f, ":") != key {
				continue
			}
			if i+1 >= len(fields) {
				break
			}
			val, err := strconv.ParseUint(strings.TrimSuffix(fields[i+1], "."), 10, 64)
			if err == nil {
				return val
			}
		}
	}
	return 0
}
