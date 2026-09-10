package core

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"runtime"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"
)

// ─── llama.cpp runtime & download ───────────────────────────────
// Environment detection (binary lookup, version probing, CUDA runtime) and the
// resumable llama.cpp GitHub release download (main program + cudart runtime),
// including the download retry policy shared with the other download paths.

type GitHubRelease struct {
	TagName     string        `json:"tag_name"`
	Name        string        `json:"name"`
	Body        string        `json:"body"`
	PublishedAt string        `json:"published_at"`
	Assets      []GitHubAsset `json:"assets"`
}

type GitHubAsset struct {
	Name               string `json:"name"`
	Size               int64  `json:"size"`
	BrowserDownloadURL string `json:"browser_download_url"`
}

type LlamaCppInfo struct {
	Installed bool   `json:"installed"`
	Path      string `json:"path"`
	Version   string `json:"version"`
	// CudartInstalled reports whether the CUDA runtime DLLs (cudart/cublas,
	// co-downloaded with CUDA builds since llama.cpp b10342) sit next to the
	// resolved binary; always false on non-Windows and for CPU/Vulkan builds.
	CudartInstalled bool `json:"cudartInstalled"`
	// CudartVersion is the CUDA major family of the installed cudart runtime
	// ("13", "12"), derived from the cudart64_*.dll file name. The DLL's
	// embedded FileVersion does not encode the CUDA release version, so the
	// file name is the only reliable source; a bare "12" cannot prove the
	// 12.8 Blackwell floor. Empty when not installed, unparsable, or
	// non-Windows.
	CudartVersion string `json:"cudartVersion"`
	// Accel names the acceleration backend of the installed build:
	// "cuda" | "vulkan" | "metal" | "cpu". Official llama.cpp builds are
	// GGML_BACKEND_DL everywhere, so the value is detected from the backend
	// library sitting next to the resolved binary (windows ggml-cuda.dll /
	// ggml-vulkan.dll, linux libggml-vulkan.so), from the arch on darwin
	// (arm64 release embeds Metal; x64 release is CPU-only) or the platform
	// default (android = CPU-only). Empty when llama.cpp is not installed.
	Accel string `json:"accel"`
}

var cachedLlamaCpp LlamaCppInfo
var llamaCacheValid atomic.Bool

var downloadState = &DownloadState{Status: "idle"}
var downloadMu sync.Mutex
var downloadCancel context.CancelFunc
var downloadResumeCh = make(chan struct{}, 1)
var customLlamaCppDir string
var customLlamaCppMu sync.Mutex

// llamaCppDownloadDirOverride is the user-chosen download path for new
// llama.cpp installs (empty means unset, use the defaultLlamaCppDir default).
// Distinct from customLlamaCppDir (the imported existing install): new
// downloads land in the download path, while detection falls back to the
// imported directory second.
var llamaCppDownloadDirOverride string
var llamaCppDownloadDirMu sync.Mutex

type DownloadState struct {
	Status     string `json:"status"` // idle, fetching, downloading, paused, extracting, done, error
	Paused     bool   `json:"paused"`
	Progress   int    `json:"progress"`
	Total      int64  `json:"total"`
	Downloaded int64  `json:"downloaded"`
	FileName   string `json:"fileName"`
	Version    string `json:"version"`
	Error      string `json:"error"`
}

// findLlamaBinInDir searches for llama.cpp binary bin under dir: first the dir
// root, then one-level subdirectories (the downloaded zip may extract with a
// top-level folder, e.g. llama-b9999-bin/). On Windows, also accepts files
// without the .exe suffix. Returns the absolute path on hit, empty string otherwise.
func findLlamaBinInDir(dir, bin string) string {
	name := bin
	if runtime.GOOS == "windows" {
		name += ".exe"
	}
	check := func(p string) string {
		if fi, err := os.Stat(p); err == nil && !fi.IsDir() {
			if abs, err := filepath.Abs(p); err == nil {
				return abs
			}
			return p
		}
		return ""
	}
	if p := check(filepath.Join(dir, name)); p != "" {
		return p
	}
	// Windows fallback: files without the .exe suffix
	if runtime.GOOS == "windows" {
		if p := check(filepath.Join(dir, bin)); p != "" {
			return p
		}
	}
	entries, err := os.ReadDir(dir)
	if err != nil {
		return ""
	}
	for _, e := range entries {
		if !e.IsDir() {
			continue
		}
		if p := check(filepath.Join(dir, e.Name(), name)); p != "" {
			return p
		}
		if runtime.GOOS == "windows" {
			if p := check(filepath.Join(dir, e.Name(), bin)); p != "" {
				return p
			}
		}
	}
	return ""
}

// findLlamaBin searches for llama.cpp binary bin under dir: an empty dir means
// PATH lookup (exec.LookPath), returning the LookPath-resolved result; a
// non-empty directory delegates to findLlamaBinInDir.
func findLlamaBin(dir, bin string) string {
	if dir == "" {
		path, err := exec.LookPath(bin)
		if err != nil {
			return ""
		}
		return path
	}
	return findLlamaBinInDir(dir, bin)
}

// androidLdEnv returns the child-environment entries needed to run a llama.cpp
// binary on Android: the release packages ship their shared libraries
// (libllama-server-impl.so, libllama-bench-impl.so, libggml*.so, …) NEXT to
// the executables, and the Android dynamic linker does not search the
// executable's own directory — LD_LIBRARY_PATH must point there (the same
// pattern Termux uses to run its programs). Returns nil on every other
// platform so callers can append unconditionally.
func androidLdEnv(binPath string) []string {
	if pathsGOOS != "android" {
		return nil
	}
	return []string{"LD_LIBRARY_PATH=" + filepath.Dir(binPath)}
}

// resolveLlamaServerBin resolves the llama-server executable path by priority
// llamaCppDownloadDir() > customLlamaCppDir (imported install) > PATH, shared by
// getLlamaCppInfo and buildServerCommand to keep the two lookups from drifting.
// A directory hit returns an absolute path; a PATH hit returns the bare binary
// name "llama-server" (left for exec.Command to resolve); no hit returns "".
func resolveLlamaServerBin() string {
	if p := findLlamaBinInDir(llamaCppDownloadDir(), "llama-server"); p != "" {
		return p
	}
	customLlamaCppMu.Lock()
	customDir := customLlamaCppDir
	customLlamaCppMu.Unlock()
	if customDir != "" {
		if p := findLlamaBinInDir(customDir, "llama-server"); p != "" {
			return p
		}
	}
	if _, err := exec.LookPath("llama-server"); err == nil {
		return "llama-server"
	}
	return ""
}

// llamaVersionProbeTimeout is the upper bound for llama.cpp version probing.
// A healthy binary returns from --version in milliseconds; a timeout means the
// binary is misbehaving (e.g. treating -v as a version flag and starting a full
// HTTP server that runs forever), in which case the child process is killed and
// an empty string returned so getLlamaCppInfo returns quickly and the detection
// chain is never frozen by a broken binary. It is a package-level var instead
// of a const so tests can temporarily shorten it to verify the kill behavior
// (an injection point in the same style as probeLlamaVersion; tests restore it
// immediately after use).
var llamaVersionProbeTimeout = 5 * time.Second

// probeLlamaVersion is the injection point for the llama.cpp version-probe
// command execution (a package-level var in the same style as
// githubReleasesAPI / renameFile / updateRepoAPI): the default implementation
// runs `path --version` with a timeout and merges stdout+stderr. Tests can
// replace this variable to inject a fake probe command and avoid launching a
// real binary; and since the probe argument (--version) is encapsulated inside
// the default implementation, tests can directly assert that only --version is
// ever invoked, with no fallback to -v.
var probeLlamaVersion = func(path string) string {
	ctx, cancel := context.WithTimeout(context.Background(), llamaVersionProbeTimeout)
	defer cancel()

	cmd := exec.CommandContext(ctx, path, "--version")
	hideWindow(cmd)
	// Android only: the binary's siblings must be linker-visible (see
	// androidLdEnv); the desktop environment inherits unchanged.
	if ld := androidLdEnv(path); ld != nil {
		cmd.Env = append(os.Environ(), ld...)
	}
	var out, errOut bytes.Buffer
	cmd.Stdout = &out
	cmd.Stderr = &errOut
	// runCmd only captures stdout, while llama-server's --version output goes
	// entirely to stderr (stdout empirically empty); merge both to get the version
	if err := cmd.Run(); err != nil && errOut.Len() > 0 {
		log.Printf("[CMD] %s --version stderr: %s", path, strings.TrimSpace(errOut.String()))
	}
	return strings.TrimSpace(out.String() + errOut.String())
}

// parseLlamaVersion extracts the version string from probe output: prefer lines
// starting with "version" or containing "build" (typical llama.cpp --version
// output, e.g. "version: 1234"), otherwise return the whole trimmed output.
// Pure string logic, directly assertable by unit tests.
func parseLlamaVersion(versionOut string) string {
	versionOut = strings.TrimSpace(versionOut)
	if versionOut == "" {
		return ""
	}
	for _, line := range strings.Split(versionOut, "\n") {
		trimmed := strings.TrimSpace(line)
		if strings.HasPrefix(trimmed, "version") || strings.Contains(trimmed, "build") {
			return trimmed
		}
	}
	return versionOut
}

// fillLlamaCppVersion tries to run the binary to read its version into
// info.Version. Probing only invokes `--version` (merging stdout+stderr), never
// falling back to `-v`: llama-server 10342's -v is not a version flag but starts
// a full HTTP server, which previously caused version probing to block forever
// and the home page to permanently show "not found". Timeout-protected, so no
// misbehaving binary can freeze the detection chain. A failed run (e.g. a stub
// that is not executable on Windows) only leaves Version empty; Installed is
// unaffected.
func fillLlamaCppVersion(info *LlamaCppInfo, path string) {
	versionOut := probeLlamaVersion(path)
	if versionOut != "" {
		info.Version = parseLlamaVersion(versionOut)
	}
}

// getLlamaCppInfo detects the llama.cpp runtime: searches for the binary by
// priority llamaCppDownloadDir() > customLlamaCppDir (imported install) > PATH.
// llama-server goes through the shared helper resolveLlamaServerBin (the
// download directory supports both root and one-level-subdir layouts); the
// other candidate binaries (llama-cli / llama.cpp / llama) follow the same
// directory priority.
func getLlamaCppInfo() LlamaCppInfo {
	info := LlamaCppInfo{}

	// A PATH hit for llama-server is returned by the helper as the bare binary
	// name; restore the exec.LookPath-resolved result so the frontend can show
	// the full path
	if p := resolveLlamaServerBin(); p != "" {
		if p == "llama-server" {
			if resolved, err := exec.LookPath(p); err == nil {
				p = resolved
			}
		}
		info.Installed = true
		info.Path = p
		fillLlamaCppVersion(&info, p)
		setCudartInfo(&info, filepath.Dir(p))
		info.Accel = detectAccel(filepath.Dir(p), runtime.GOOS, runtime.GOARCH)
		return info
	}

	customLlamaCppMu.Lock()
	customDir := customLlamaCppDir
	customLlamaCppMu.Unlock()

	// Download path first, then the imported install, then PATH — the same
	// order as resolveLlamaServerBin / llamaCppDownloadDir.
	dirsToCheck := []string{llamaCppDownloadDir()}
	if customDir != "" {
		dirsToCheck = append(dirsToCheck, customDir)
	}
	dirsToCheck = append(dirsToCheck, "") // empty string means PATH

	for _, dir := range dirsToCheck {
		for _, bin := range []string{"llama-cli", "llama.cpp", "llama"} {
			if p := findLlamaBin(dir, bin); p != "" {
				info.Installed = true
				info.Path = p
				fillLlamaCppVersion(&info, p)
				setCudartInfo(&info, filepath.Dir(p))
				info.Accel = detectAccel(filepath.Dir(p), runtime.GOOS, runtime.GOARCH)
				return info
			}
		}
	}

	return info
}

// detectAccel classifies the acceleration backend of the llama.cpp build whose
// binaries live in dir ("" means not installed → empty result). Official
// llama.cpp builds are GGML_BACKEND_DL everywhere: the main programs ship
// backend-agnostic and the GPU support comes from whichever backend library
// sits next to them. windows: ggml-cuda.dll → "cuda", ggml-vulkan.dll →
// "vulkan"; linux: libggml-vulkan.so (or plain ggml-vulkan.so) → "vulkan";
// darwin: the arm64 release embeds Metal ("metal", no file to find) while the
// x64 release is CPU-only; android: CPU-only arm64 packages → "cpu". A build
// with no recognized GPU backend library falls back to "cpu". Pure over the
// directory listing (no process spawns), directly unit-testable with a fixture
// directory and explicit goos/goarch values.
func detectAccel(dir, goos, goarch string) string {
	if dir == "" {
		return ""
	}
	switch goos {
	case "windows":
		if dirHasFileFold(dir, "ggml-cuda.dll") {
			return "cuda"
		}
		if dirHasFileFold(dir, "ggml-vulkan.dll") {
			return "vulkan"
		}
	case "linux":
		if dirHasFileFold(dir, "libggml-vulkan.so") || dirHasFileFold(dir, "ggml-vulkan.so") {
			return "vulkan"
		}
	case "darwin":
		if goarch == "arm64" {
			return "metal"
		}
		return "cpu"
	case "android":
		return "cpu"
	}
	return "cpu"
}

// dirHasFileFold reports whether dir contains a regular entry whose name
// equals name case-insensitively (backend DLL/SO names are lowercase upstream,
// but a user-imported install may carry different casing). Unreadable dirs
// report false so the caller's fallback applies.
func dirHasFileFold(dir, name string) bool {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return false
	}
	for _, e := range entries {
		if !e.IsDir() && strings.EqualFold(e.Name(), name) {
			return true
		}
	}
	return false
}

// detectCudartRuntime reports whether the CUDA runtime DLLs shipped by the
// cudart asset (cudart64_*.dll plus the cublas family) are present in dir.
// The DLLs land next to llama-server.exe because both assets extract into the
// same directory, so their presence marks a CUDA build with its runtime
// installed. Non-Windows never carries the Windows-exclusive cudart asset.
func detectCudartRuntime(dir string) bool {
	if runtime.GOOS != "windows" || dir == "" {
		return false
	}
	patterns := []string{"cudart*.dll", "cublas*.dll"}
	for _, pat := range patterns {
		if matches, err := filepath.Glob(filepath.Join(dir, pat)); err == nil && len(matches) > 0 {
			return true
		}
	}
	return false
}

// setCudartInfo fills the cudart detection fields (runtime presence plus the
// CUDA major family) for the directory holding the resolved llama.cpp binary,
// shared by both getLlamaCppInfo call sites so the two lookups cannot drift.
func setCudartInfo(info *LlamaCppInfo, dir string) {
	info.CudartInstalled = detectCudartRuntime(dir)
	if info.CudartInstalled {
		info.CudartVersion = detectCudartVersion(dir)
	}
}

// cudartVerRe extracts the CUDA major family from a cudart runtime DLL file
// name, e.g. "13" from cudart64_13.dll.
var cudartVerRe = regexp.MustCompile(`cudart64_(\d+)`)

// detectCudartVersion reports the CUDA major family of the installed cudart
// runtime in dir as a string ("13", "12"), parsed from the cudart64_*.dll
// file name (the lexicographically first match wins when several are present;
// Glob results are sorted). Only the file name is reliable: the DLL's embedded
// FileVersion (e.g. "6,14,11,13030") does not encode the CUDA release version.
// The comparison against the 12.8 Blackwell floor is deliberately conservative
// on the consumer side — a bare "12" major cannot prove >= 12.8, so 12.x never
// satisfies the floor (see the frontend's cudartVersionSatisfiesFloor).
// Returns "" when no cudart64_*.dll is present, on non-Windows (the cudart
// asset is Windows-exclusive), or when the name carries no parseable major.
// Pure filesystem + regex, no external process spawn.
func detectCudartVersion(dir string) string {
	if runtime.GOOS != "windows" || dir == "" {
		return ""
	}
	matches, err := filepath.Glob(filepath.Join(dir, "cudart64_*.dll"))
	if err != nil || len(matches) == 0 {
		return ""
	}
	m := cudartVerRe.FindStringSubmatch(filepath.Base(matches[0]))
	if m == nil {
		return ""
	}
	return m[1]
}

// githubReleasesAPI points to the llama.cpp latest release API, declared as a
// var so tests can replace the package-level variable to inject a local
// httptest server (same style as updateRepoAPI).
var githubReleasesAPI = "https://api.github.com/repos/ggml-org/llama.cpp/releases/latest"

// githubReleasesListAPI lists recent llama.cpp releases newest-first (including
// prereleases), declared as a var for test injection like githubReleasesAPI.
// Since 2026-08 upstream ships binaries only in nightly prereleases while
// releases/latest (non-prerelease only) points to an asset-less marker, so the
// download flow falls back to this list to find the newest release with binaries.
var githubReleasesListAPI = "https://api.github.com/repos/ggml-org/llama.cpp/releases?per_page=10"

// defaultLlamaCppDir resolves the default llama.cpp install directory name
// (llamaCppDirName in paths.go) to its per-OS location: bare cwd-relative on
// Windows, under the app-data base elsewhere. Declared as a function-valued
// var (same injection style as configFile) so tests can pin the directory.
var defaultLlamaCppDir = func() string { return resolveStateFile(llamaCppDirName) }

// llamaCppDownloadDir returns the target directory for llama.cpp download
// extraction: the user-chosen download path when configured, otherwise the
// default per-OS llama-cpp/ location. Matches the detection priority of
// getLlamaCppInfo / resolveLlamaServerBin (download path > imported
// customLlamaCppDir > PATH) so the download landing spot and the detection
// location stay consistent.
func llamaCppDownloadDir() string {
	llamaCppDownloadDirMu.Lock()
	dir := llamaCppDownloadDirOverride
	llamaCppDownloadDirMu.Unlock()
	if dir != "" {
		return dir
	}
	return defaultLlamaCppDir()
}

func downloadLlamaCpp() {
	ctx, cancel := context.WithCancel(context.Background())
	downloadMu.Lock()
	downloadCancel = cancel
	downloadResumeCh = make(chan struct{}, 1)
	downloadMu.Unlock()

	defer func() {
		downloadMu.Lock()
		downloadCancel = nil
		downloadMu.Unlock()
		cancel()
	}()

	// Step 1: Fetch latest release info (ctx-bound: cancel during the fetch
	// aborts the in-flight request immediately instead of waiting out the
	// HTTP timeout, and resets the state to idle rather than an error)
	downloadMu.Lock()
	downloadState.Status = "fetching"
	downloadMu.Unlock()

	release, err := fetchLatestRelease(ctx)
	if err != nil {
		if ctx.Err() != nil {
			// Cancelled by user while fetching release metadata
			downloadMu.Lock()
			if downloadState.Status != "idle" {
				downloadState.Status = "idle"
				downloadState.Error = ""
				downloadState.Paused = false
			}
			downloadMu.Unlock()
			log.Println("⏹️ llama.cpp download stopped by user")
			return
		}
		setDownloadError(tr("获取发布信息失败: ", "Failed to fetch release info: ") + err.Error())
		return
	}

	// Step 2: Find best asset (the main-program asset; the cudart runtime is an additional asset)
	mainAsset := pickBestAsset(release.Assets)
	if mainAsset == nil {
		// Since 2026-08 llama.cpp ships binaries only in nightly prereleases;
		// releases/latest (non-prerelease only) points to an asset-less marker
		// — fall back to the newest listed release that actually carries a
		// matching build. Fallback fetch errors stay silent: the final error
		// below is the more actionable one.
		if list, listErr := fetchReleaseListAt(ctx, githubReleasesListAPI); listErr == nil {
			if rel := newestReleaseWithAssets(list); rel != nil {
				release = rel
				mainAsset = pickBestAsset(release.Assets)
			}
		}
	}
	if mainAsset == nil {
		if ctx.Err() != nil {
			// Cancelled by user during the fallback release-list fetch
			downloadMu.Lock()
			if downloadState.Status != "idle" {
				downloadState.Status = "idle"
				downloadState.Error = ""
				downloadState.Paused = false
			}
			downloadMu.Unlock()
			log.Println("⏹️ llama.cpp download stopped by user")
			return
		}
		setDownloadError(tr("未找到适用于当前平台的 llama.cpp 构建", "No llama.cpp build found for the current platform"))
		return
	}

	// Since b10342, Windows CUDA builds split the runtime into a separate
	// cudart zip; the main-program zip no longer bundles it, so the cudart
	// asset must be co-downloaded and extracted into the same directory.
	// Detected by whether the main asset name contains "cuda"
	// (pickBestAsset only picks a cuda build on Windows when a GPU is
	// detected, so the asset name is the selection result — no second GPU
	// probe needed); non-Windows cuda builds (if any) do not get the
	// Windows-exclusive cudart asset attached.
	assets := []*GitHubAsset{mainAsset}
	if runtime.GOOS == "windows" && strings.Contains(strings.ToLower(mainAsset.Name), "cuda") {
		// Pair the cudart runtime with the CUDA version and arch of the chosen
		// main asset (both extracted from its name, not from the local nvcc
		// toolkit): the runtime DLLs must match the build actually downloaded,
		// which floor/tie-break selection may pick independently of toolkit.
		cudartVer, _ := cudaVerTagOf(mainAsset.Name)
		if cudart := pickCudartAssetFor(release.Assets, cudartVer, archKeyOf(runtime.GOARCH)); cudart != nil {
			assets = append(assets, cudart)
		}
	}

	downloadMu.Lock()
	downloadState.Status = "downloading"
	downloadState.FileName = mainAsset.Name
	// Sequential multi-asset download: Total is the sum of all asset sizes,
	// Downloaded accumulates across assets
	var totalBytes int64
	for _, a := range assets {
		totalBytes += a.Size
	}
	downloadState.Total = totalBytes
	downloadState.Version = release.TagName
	downloadState.Downloaded = 0
	downloadMu.Unlock()

	// Target directory: custom llama.cpp directory first, otherwise the
	// default llama-cpp/; created once before extraction
	targetDir := llamaCppDownloadDir()
	if err := os.MkdirAll(targetDir, 0755); err != nil {
		setDownloadError(tr("创建目录失败: ", "Failed to create directory: ") + err.Error())
		return
	}

	// Step 3: Download and extract each asset sequentially (main program
	// first, then cudart); pause/stop semantics unchanged; progress overlays
	// baseDownloaded with bytes already completed by previous assets
	var baseDownloaded int64
	for _, asset := range assets {
		// Multi-asset downloads must re-enter "downloading" per asset: the
		// status is set once before this loop, but asset 1's extraction
		// flips it to "extracting"; without resetting here, asset 2+ (the
		// cudart runtime zip) downloads under the stale "extracting" label —
		// the UI claims "extracting" while bytes are still being fetched,
		// and a network failure there surfaces as a confusing "download
		// failed" right after "extracting".
		downloadMu.Lock()
		downloadState.Status = "downloading"
		downloadState.FileName = asset.Name
		downloadMu.Unlock()

		tmpPath, err := downloadWithResume(ctx, asset.BrowserDownloadURL, asset.Size, baseDownloaded)
		if err != nil {
			if ctx.Err() != nil {
				// Cancelled by user (stop) — also from the paused state: the
				// previous guard skipped the reset when status was "paused",
				// leaving the state machine stranded in paused with the
				// download goroutine already gone (Cancel and Resume both
				// appeared dead afterwards)
				downloadMu.Lock()
				if downloadState.Status != "idle" {
					downloadState.Status = "idle"
					downloadState.Error = ""
					downloadState.Paused = false
				}
				downloadMu.Unlock()
				log.Println("⏹️ llama.cpp download stopped by user")
			} else {
				setDownloadError(tr("下载失败: ", "Download failed: ") + err.Error())
			}
			return
		}

		// Temp file cleaned up after extraction (including cancel/error paths)
		defer os.Remove(tmpPath)

		// Check if stopped during download
		select {
		case <-ctx.Done():
			return
		default:
		}

		// Step 4: Extract (into the same directory as the main program)
		downloadMu.Lock()
		downloadState.Status = "extracting"
		downloadState.Progress = 100
		downloadMu.Unlock()

		var extractErr error
		switch {
		case strings.HasSuffix(asset.Name, ".zip"):
			extractErr = extractZip(tmpPath, targetDir)
		case strings.HasSuffix(asset.Name, ".tar.gz"):
			extractErr = extractTarGz(tmpPath, targetDir)
		default:
			// Same as the original single-asset logic: unsupported formats
			// error out directly, without the "extraction failed" prefix
			setDownloadError(tr("不支持的文件格式: ", "Unsupported file format: ") + asset.Name)
			return
		}
		if extractErr != nil {
			setDownloadError(tr("解压失败: ", "Extraction failed: ") + extractErr.Error())
			return
		}

		baseDownloaded += asset.Size
	}

	// Belt-and-braces for Unix (linux / macOS / Android): the extractors apply
	// the archive entry modes, but a missing mode or a umask-stripped creation
	// mode would leave llama-server 0644 and unable to exec. Re-assert 0755 on
	// the resolved binary after all assets are extracted (best-effort).
	ensureLlamaServerExecutable(targetDir)

	// Step 5: Done (only set after all assets downloaded and extracted)
	downloadMu.Lock()
	downloadState.Status = "done"
	downloadState.Progress = 100
	downloadMu.Unlock()

	// Reset model cache so new models are picked up
	invalidateModelCache()
	// Invalidate the llama.cpp detection cache: the result cached at mount
	// time (Installed=false) is stale; re-detect after successful extraction,
	// otherwise the home page keeps showing "not found". The flag flip runs
	// under llamaMu (same discipline as the invalidation sites in app.go),
	// serializing it with the snapshot rewrite in GetLlamaCpp's slow path;
	// this goroutine never holds llamaMu, so the lock cannot deadlock.
	llamaMu.Lock()
	llamaCacheValid.Store(false)
	llamaMu.Unlock()

	log.Printf("[OK] llama.cpp %s downloaded and extracted to %s/", release.TagName, targetDir)
}

// ensureLlamaServerExecutable re-asserts the 0755 exec permission on the
// llama-server binary under dir (best-effort: a [WARN] is logged on failure
// and the download still completes). Belt-and-braces for the exec-bit
// handling in extractZip / extractTarGz: the upstream Android / Linux / macOS
// builds ship as .tar.gz whose entries carry 0755, but an archive without
// usable mode bits or a umask-stripped creation mode would leave the binary
// 0644 and unable to exec — breaking service start right after a successful
// download. No-op on Windows (no exec bit) and when no binary is found.
func ensureLlamaServerExecutable(dir string) {
	if runtime.GOOS == "windows" {
		return
	}
	p := findLlamaBinInDir(dir, "llama-server")
	if p == "" {
		return
	}
	if err := os.Chmod(p, 0755); err != nil {
		log.Printf("[WARN] could not mark %s executable: %v", p, err)
	}
}

// ─── Download retry policy ─────────────────────────────────────────
//
// All download paths (llama.cpp, model files, app update) share the same
// automatic retry policy: a transient failure (network error, HTTP 429/5xx)
// is retried internally up to downloadRetryCount times — keeping the
// downloading state, never losing the bytes already on disk — before the
// error is surfaced for a manual user retry. Permanent failures (other 4xx,
// disk errors) and user-initiated cancellation are never retried.

var (
	// downloadRetryCount bounds the automatic retries per failure episode.
	// Package-level var so tests can keep runs fast without sleeping.
	downloadRetryCount = 3
	// downloadRetryDelay is the backoff between automatic retries.
	downloadRetryDelay = 3 * time.Second
)

// retryableDownloadStatus reports whether an HTTP status is transient and
// worth retrying: 429 and 5xx qualify; other 4xx (404/403/...) are permanent.
func retryableDownloadStatus(status int) bool {
	return status == http.StatusTooManyRequests || status >= 500
}

// sleepDownloadRetry waits the retry delay, interruptible by ctx; returns
// false when the context was cancelled first (the caller then gives up).
func sleepDownloadRetry(ctx context.Context) bool {
	select {
	case <-ctx.Done():
		return false
	case <-time.After(downloadRetryDelay):
		return true
	}
}

// downloadWithResume downloads a file with pause/resume support.
// baseDownloaded is the total bytes of assets already completed before this
// file: in sequential multi-asset downloads (e.g. llama.cpp main program +
// cudart runtime) progress must overlay the previous cumulative value; pass 0
// for a single-asset call.
// Robustness: after a pause the temp file is reopened before writing (the
// pre-pause handle is closed); a 200 answer to a Range resume truncates the
// temp file and restarts clean; a clean EOF before totalSize (a truncated
// body) auto-resumes via Range, giving up after 3 retries with a clear error
// instead of returning a corrupt zip for extraction.
// Returns the path to the downloaded temp file.
func downloadWithResume(ctx context.Context, url string, totalSize int64, baseDownloaded int64) (string, error) {
	tmpFile, err := os.CreateTemp(resolveTempDir(), "llamacpp-download-*"+filepath.Ext(url[strings.LastIndex(url, "."):]))
	if err != nil {
		return "", fmt.Errorf(tr("创建临时文件失败: %w", "failed to create temporary file: %w"), err)
	}
	tmpPath := tmpFile.Name()

	// closeTmp closes the current handle and clears it so the next
	// outer-loop pass knows to reopen the file: the resume path must
	// reopen the temp file, because writing to the pre-pause handle fails
	// with "file already closed".
	closeTmp := func() {
		if tmpFile != nil {
			tmpFile.Close()
			tmpFile = nil
		}
	}
	// reopenTmp reopens the temp file for append when a previous pass
	// (pause or truncated-EOF retry) closed the handle; O_APPEND makes
	// every Write land at the current end of file, no seek needed.
	reopenTmp := func() error {
		if tmpFile != nil {
			return nil
		}
		f, err := os.OpenFile(tmpPath, os.O_WRONLY|os.O_CREATE|os.O_APPEND, 0644)
		if err != nil {
			return fmt.Errorf(tr("打开临时文件失败: %w", "failed to reopen temporary file: %w"), err)
		}
		tmpFile = f
		return nil
	}

	// A clean EOF before the declared size means a truncated body: the
	// loop below auto-resumes with a Range request from the bytes already
	// on disk, giving up after maxTruncResumes retries with a clear error
	// instead of extracting a corrupt zip.
	const maxTruncResumes = 3
	truncResumes := 0

	// Automatic transient-failure retries (see the download retry policy
	// block): network errors and HTTP 429/5xx reconnect up to
	// downloadRetryCount times — the outer loop re-stats the temp file, so
	// every retry resumes from the bytes already on disk.
	retries := 0

	// We'll loop to handle pause → resume cycles
	for {
		// Check if cancelled
		select {
		case <-ctx.Done():
			closeTmp()
			return tmpPath, ctx.Err()
		default:
		}

		// Reopen the temp file if a previous pass (pause / truncated-EOF
		// retry) closed it; downloads resume by appending to the bytes
		// already on disk.
		if err := reopenTmp(); err != nil {
			return tmpPath, err
		}

		// Get current file size for Range header
		fi, err := os.Stat(tmpPath)
		var offset int64
		if err == nil {
			offset = fi.Size()
		}

		// Reset downloaded count to current file size on resume
		downloadMu.Lock()
		downloadState.Downloaded = baseDownloaded + offset
		downloadMu.Unlock()

		// Build request
		req, err := http.NewRequestWithContext(ctx, "GET", url, nil)
		if err != nil {
			closeTmp()
			return tmpPath, err
		}
		req.Header.Set("User-Agent", appUserAgent())
		if offset > 0 {
			req.Header.Set("Range", fmt.Sprintf("bytes=%d-", offset))
		}

		client := &http.Client{Timeout: 30 * time.Minute}
		resp, err := client.Do(req)
		if err != nil {
			// If paused, don't return error — wait for resume
			downloadMu.Lock()
			isPaused := downloadState.Paused
			resumeCh := downloadResumeCh
			downloadMu.Unlock()
			if isPaused {
				waitForResume(ctx, resumeCh)
				continue
			}
			closeTmp()
			if ctx.Err() == nil && retries < downloadRetryCount {
				retries++
				log.Printf("[WARN] llama.cpp download attempt failed (%v), retrying %d/%d", err, retries, downloadRetryCount)
				if sleepDownloadRetry(ctx) {
					continue
				}
				return tmpPath, ctx.Err()
			}
			return tmpPath, err
		}

		// Handle response
		expectedStatus := http.StatusOK
		if offset > 0 {
			expectedStatus = http.StatusPartialContent
		}
		if resp.StatusCode != expectedStatus && resp.StatusCode != http.StatusOK {
			status := resp.StatusCode
			resp.Body.Close()
			closeTmp()
			if retryableDownloadStatus(status) && ctx.Err() == nil && retries < downloadRetryCount {
				retries++
				log.Printf("[WARN] llama.cpp download got HTTP %d, retrying %d/%d", status, retries, downloadRetryCount)
				if sleepDownloadRetry(ctx) {
					continue
				}
				return tmpPath, ctx.Err()
			}
			return tmpPath, fmt.Errorf("HTTP %d", status)
		}

		// Robustness against servers ignoring Range: this request carried
		// a Range header (offset>0) but the server answered 200 with the
		// full body from byte 0. Appending that body at the current offset
		// would yield "partial prefix + full body" corruption, so truncate
		// the temp file to zero — with the O_APPEND handle the next writes
		// start from 0, no seek needed — reset the offset and the progress
		// display, then read this same response as a clean full
		// re-download. Truncate goes through the path, not the handle:
		// O_APPEND handles are opened with FILE_APPEND_DATA on Windows and
		// reject SetEndOfFile with "Access is denied" (same approach as
		// the downloadTask Range-ignored branch).
		if offset > 0 && resp.StatusCode == http.StatusOK {
			if err := os.Truncate(tmpPath, 0); err != nil {
				resp.Body.Close()
				closeTmp()
				return tmpPath, fmt.Errorf(tr("重置临时文件失败: %w", "failed to reset temporary file: %w"), err)
			}
			offset = 0
			downloadMu.Lock()
			downloadState.Downloaded = baseDownloaded
			downloadMu.Unlock()
		}

		// Update total from Content-Length or Content-Range
		if resp.ContentLength > 0 {
			effectiveSize := resp.ContentLength
			if offset > 0 {
				effectiveSize += offset
			}
			downloadMu.Lock()
			downloadState.Total = baseDownloaded + effectiveSize
			downloadMu.Unlock()
		}

		// Read body with pause/stop checking
		buf := make([]byte, 32*1024)
		downloaded := offset

		for {
			// Check pause
			downloadMu.Lock()
			paused := downloadState.Paused
			resumeCh := downloadResumeCh
			downloadMu.Unlock()
			if paused {
				resp.Body.Close()
				waitForResume(ctx, resumeCh)
				closeTmp()
				break // breaks inner for, outer for reopens the file and re-establishes
			}

			// Interruptible read: do Read in goroutine, select on ctx.Done
			type readRes struct {
				n   int
				err error
			}
			ch := make(chan readRes, 1)
			go func() {
				n, err := resp.Body.Read(buf)
				ch <- readRes{n, err}
			}()

			var rr readRes
			select {
			case <-ctx.Done():
				resp.Body.Close()
				closeTmp()
				return tmpPath, ctx.Err()
			case rr = <-ch:
			}

			if rr.n > 0 {
				if _, writeErr := tmpFile.Write(buf[:rr.n]); writeErr != nil {
					resp.Body.Close()
					closeTmp()
					return tmpPath, writeErr
				}
				downloaded += int64(rr.n)

				downloadMu.Lock()
				downloadState.Downloaded = baseDownloaded + downloaded
				if downloadState.Total > 0 {
					// progress computed from cumulative bytes including the
					// base offset, monotonically non-decreasing across assets
					downloadState.Progress = int(float64(baseDownloaded+downloaded) * 100 / float64(downloadState.Total))
				}
				downloadMu.Unlock()
			}
			if rr.err == io.EOF {
				resp.Body.Close()
				// A clean EOF before the declared size means a truncated
				// body (e.g. a proxy cutting the stream exactly at a chunk
				// boundary): never return the partial file as success — a
				// corrupt zip would only blow up later at extraction with
				// a confusing error. Auto-resume with a Range request from
				// the bytes already on disk; give up after maxTruncResumes
				// retries with a clear error. A Content-Length smaller than
				// the remaining asset size ends up here too (same
				// truncated-body handling).
				if totalSize > 0 && downloaded < totalSize {
					truncResumes++
					if truncResumes > maxTruncResumes {
						closeTmp()
						return tmpPath, fmt.Errorf(tr("下载不完整: 已下载 %d / %d 字节", "incomplete download: got %d of %d bytes"), downloaded, totalSize)
					}
					closeTmp()
					break // back to the outer loop: reopen the file, Range resume from the current file size
				}
				closeTmp()
				return tmpPath, nil
			}
			if rr.err != nil {
				resp.Body.Close()
				closeTmp()
				// Mid-body read failures (connection reset, stream errors) are
				// transient: reconnect and resume from the bytes on disk.
				if rr.err != io.EOF && ctx.Err() == nil && retries < downloadRetryCount {
					retries++
					log.Printf("[WARN] llama.cpp download stream failed (%v), retrying %d/%d", rr.err, retries, downloadRetryCount)
					if sleepDownloadRetry(ctx) {
						break // back to the outer loop: reopen + Range resume
					}
					return tmpPath, ctx.Err()
				}
				return tmpPath, rr.err
			}
		}
	}
}

// waitForResume blocks until the download is resumed (via resumeCh) or stopped (via ctx).
func waitForResume(ctx context.Context, resumeCh chan struct{}) {
	select {
	case <-resumeCh:
		// Resumed
	case <-ctx.Done():
		// Stopped
	}
}

// fetchLatestRelease fetches the latest llama.cpp release from the default API
// URL. The request is bound to ctx so a user cancel aborts an in-flight fetch
// immediately (the click otherwise appears dead until the HTTP timeout).
func fetchLatestRelease(ctx context.Context) (*GitHubRelease, error) {
	return fetchLatestReleaseAt(ctx, githubReleasesAPI)
}

// fetchLatestReleaseAt fetches and decodes a GitHub-style latest release JSON
// document from the given URL. The URL is injectable so tests can use a local
// httptest server instead of hitting the network.
func fetchLatestReleaseAt(ctx context.Context, apiURL string) (*GitHubRelease, error) {
	req, err := http.NewRequestWithContext(ctx, "GET", apiURL, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Accept", "application/vnd.github+json")
	req.Header.Set("User-Agent", appUserAgent())

	client := &http.Client{Timeout: 30 * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("GitHub API returned status %d", resp.StatusCode)
	}

	var release GitHubRelease
	if err := json.NewDecoder(resp.Body).Decode(&release); err != nil {
		return nil, err
	}
	return &release, nil
}

// fetchReleaseListAt fetches and decodes a GitHub-style release list document
// (newest-first, including prereleases) from the given URL; the URL is
// injectable for tests, mirroring fetchLatestReleaseAt.
func fetchReleaseListAt(ctx context.Context, apiURL string) ([]GitHubRelease, error) {
	req, err := http.NewRequestWithContext(ctx, "GET", apiURL, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Accept", "application/vnd.github+json")
	req.Header.Set("User-Agent", appUserAgent())

	client := &http.Client{Timeout: 30 * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("GitHub API returned status %d", resp.StatusCode)
	}

	var list []GitHubRelease
	if err := json.NewDecoder(resp.Body).Decode(&list); err != nil {
		return nil, err
	}
	return list, nil
}

// newestReleaseWithAssets returns the newest release of a newest-first list
// that carries a main-asset candidate for the current platform, or nil when
// no listed release has binaries for this host.
func newestReleaseWithAssets(list []GitHubRelease) *GitHubRelease {
	for i := range list {
		if pickBestAsset(list[i].Assets) != nil {
			return &list[i]
		}
	}
	return nil
}

// pickBestAsset picks the most suitable release asset for the current platform.
// The CUDA floor is derived from the GPU compute capability (Blackwell needs
// CUDA >= 12.8); a probe failure or pre-Blackwell GPU yields no floor.
func pickBestAsset(assets []GitHubAsset) *GitHubAsset {
	floor := 0.0
	if cc, ok := gpuComputeCap(); ok {
		floor = cudaFloorForComputeCap(cc)
	}
	return pickBestAssetFor(assets, runtime.GOOS, runtime.GOARCH, len(getGPUInfo()) > 0, cudaVersionFromToolkit(), floor)
}

// cudaVersionFromToolkit derives the "major.minor" version used in asset
// naming (e.g. "12.4") from the local CUDA Toolkit version (nvcc output, e.g.
// "12.4.131"); returns an empty string with no Toolkit or on parse failure.
// Used only by pickBestAssetFor's exact-version bonus; the cudart runtime
// pairing derives its version from the chosen main asset name instead.
func cudaVersionFromToolkit() string {
	cudaInfo := getCUDAInfo()
	if cudaInfo.ToolkitVersion == "" {
		return ""
	}
	parts := strings.Split(cudaInfo.ToolkitVersion, ".")
	if len(parts) < 2 {
		return ""
	}
	return parts[0] + "." + parts[1]
}

// archKeyOf maps a GOARCH value to the arch tag used in llama.cpp release
// asset names ("x64" / "arm64"); empty when unmapped. Shared by
// pickBestAssetFor and the cudart pairing in downloadLlamaCpp.
func archKeyOf(goarch string) string {
	switch goarch {
	case "amd64":
		return "x64"
	case "arm64":
		return "arm64"
	}
	return ""
}

// cudaVerRe matches the CUDA version tag embedded in asset names, e.g. the
// "12.4" in "llama-b*-bin-win-cuda-12.4-x64.zip".
var cudaVerRe = regexp.MustCompile(`cuda-(\d+\.\d+)`)

// cudaVerTagOf extracts the CUDA version tag from an asset name as a string
// (e.g. "12.4"); ok=false when the name carries no cuda-<major.minor> tag.
func cudaVerTagOf(name string) (string, bool) {
	m := cudaVerRe.FindStringSubmatch(strings.ToLower(name))
	if m == nil {
		return "", false
	}
	return m[1], true
}

// cudaVerOf parses the asset's CUDA version tag as a float for ordering
// comparisons (e.g. 12.4); ok=false when the tag is absent or unparseable.
func cudaVerOf(name string) (float64, bool) {
	tag, ok := cudaVerTagOf(name)
	if !ok {
		return 0, false
	}
	v, err := strconv.ParseFloat(tag, 64)
	if err != nil {
		return 0, false
	}
	return v, true
}

// pickBestAssetFor scores release assets for a given platform/arch and returns
// the best match. hasGPU and cudaVer allow preferring GPU-accelerated builds:
// hasGPU means ANY GPU vendor visible to the probes (nvidia on the windows
// CUDA paths — the windows GPU probe is nvidia-smi-only; any PCI GPU on the
// linux vulkan path since that build accelerates AMD/Intel/NVIDIA alike);
// cudaFloor is the minimum CUDA version the GPU can run (Blackwell
// compute capability >= 12.0 needs >= 12.8; 0 means no constraint) — cuda
// assets below the floor are skipped entirely because the hardware cannot run
// them. Returns nil when no asset matches the platform.
//
// Matching rules:
//   - Platform: "win" for Windows, "macos" for macOS; Linux accepts both the
//     "ubuntu" keyword (current upstream naming, e.g. llama-b*-bin-ubuntu-x64)
//     and the legacy "linux" keyword, so a future rename back keeps working;
//     Android matches the "android" keyword (arm64 CPU-only tarballs).
//   - Arch: enforced for every platform via the arch tag in the asset name
//     ("x64" / "arm64"); assets with no tag are accepted only on x64 hosts
//     (historical implicit-x64 naming). This drops wrong-arch builds such as
//     win-cpu-arm64 on x64 hosts, and ubuntu-s390x (no tag) / android-arm64
//     (wrong tag) everywhere they do not belong.
//   - Windows without an NVIDIA GPU: the "-cpu-" build gets a decisive bonus
//     over rocm/sycl/openvino/cuda; legacy "avx2"-style names keep their bonus
//     for releases predating the "-cpu-" naming.
//   - Windows with an NVIDIA GPU: among cuda builds the toolkit exact match
//     wins (+50); the version tie-break prefers the lowest available version
//     (widest GPU compatibility — CUDA 13 dropped pre-Turing support), or the
//     highest version >= floor when a floor is active (newest GPUs need the
//     newest runtime).
//   - Linux with any GPU (NVIDIA, AMD or Intel — the ubuntu-vulkan tarball is
//     the only GPU-accelerated Linux build, no ubuntu cuda variant exists):
//     vulkan wins decisively.
//
// On Windows, cudart runtime assets are skipped (the main program and runtime
// are downloaded separately; the runtime is matched by pickCudartAssetFor).
func pickBestAssetFor(assets []GitHubAsset, platform, arch string, hasGPU bool, cudaVer string, cudaFloor float64) *GitHubAsset {
	if len(assets) == 0 {
		return nil
	}

	// Map GOOS to release naming conventions
	platformKey := ""
	switch platform {
	case "windows":
		platformKey = "win"
	case "darwin":
		platformKey = "macos"
	case "linux":
		platformKey = "ubuntu"
	case "android":
		// Upstream Android builds are arm64 CPU-only tarballs:
		// llama-b*-bin-android-arm64.tar.gz (no cuda/rocm/vulkan variants).
		platformKey = "android"
	}
	archKey := archKeyOf(arch)

	matchesPlatform := func(name string) bool {
		if platformKey == "ubuntu" {
			return strings.Contains(name, "ubuntu") || strings.Contains(name, "linux")
		}
		return strings.Contains(name, platformKey)
	}

	// Arch-tag rule: the asset must carry the host's arch tag, or no tag at
	// all on x64 hosts (historical implicit-x64 naming).
	matchesArch := func(name string) bool {
		hasX64 := strings.Contains(name, "x64")
		hasArm64 := strings.Contains(name, "arm64")
		if hasArm64 && archKey != "arm64" {
			return false
		}
		if hasX64 && archKey != "x64" {
			return false
		}
		return hasX64 || hasArm64 || archKey == "x64"
	}

	isMainCandidate := func(name string) bool {
		// Skip cudart runtime assets: since llama.cpp b10342, Windows CUDA
		// builds are split into a main-program zip and a separate cudart
		// runtime zip; both contain "win-cuda" and would score equally, and
		// cudart is listed earlier in the release — without exclusion only the
		// runtime would be picked and the main program lost (extraction
		// artifacts would contain only runtime DLLs like cudart64_12.dll, no
		// llama-server.exe). The runtime is matched separately by
		// pickCudartAssetFor and downloaded alongside the main program.
		return !strings.HasPrefix(name, "cudart") && matchesPlatform(name) && matchesArch(name)
	}

	// Precompute the preferred CUDA version among surviving windows cuda
	// candidates: lowest when no floor is active (widest GPU compatibility),
	// highest when a floor is active (newest GPUs need the newest runtime).
	var targetCuda float64
	var hasTargetCuda bool
	if platformKey == "win" && hasGPU {
		for i := range assets {
			name := strings.ToLower(assets[i].Name)
			if !isMainCandidate(name) || !strings.Contains(name, "cuda") {
				continue
			}
			v, ok := cudaVerOf(name)
			if !ok || (cudaFloor > 0 && v < cudaFloor) {
				continue
			}
			if !hasTargetCuda ||
				(cudaFloor > 0 && v > targetCuda) ||
				(cudaFloor <= 0 && v < targetCuda) {
				targetCuda = v
				hasTargetCuda = true
			}
		}
	}

	// Score each asset — higher is better
	type scored struct {
		asset *GitHubAsset
		score int
	}
	var candidates []scored

	for i := range assets {
		a := &assets[i]
		name := strings.ToLower(a.Name)
		if !isMainCandidate(name) {
			continue
		}

		score := 0

		// Prefer CUDA builds on Windows when an NVIDIA GPU is present (the
		// windows GPU probe is nvidia-smi-only, so hasGPU == nvidia there)
		if platformKey == "win" && hasGPU && strings.Contains(name, "cuda") {
			v, hasVer := cudaVerOf(name)
			if hasVer && cudaFloor > 0 && v < cudaFloor {
				// Below the GPU's usable CUDA floor: hardware cannot run it
				continue
			}
			score += 100
			if hasVer && hasTargetCuda && v == targetCuda {
				score += 30 // Preferred version (see targetCuda computation)
			}
			// Match CUDA version — prefer exact match to installed toolkit
			if cudaVer != "" && strings.Contains(name, "cuda-"+cudaVer) {
				score += 50
			}
		}

		// Prefer AVX2 builds (most compatible for modern CPUs)
		if strings.Contains(name, "avx2") {
			score += 30
		}

		// Prefer AVX512 (best performance on supported CPUs)
		if strings.Contains(name, "avx512") {
			score += 20
		}

		if platformKey == "win" && !strings.Contains(name, "cuda") {
			if strings.Contains(name, "-cpu-") {
				// Decisive bonus: the plain CPU build beats rocm/sycl/openvino
				score += 40
			} else if !strings.Contains(name, "avx") && !strings.Contains(name, "vulkan") && !strings.Contains(name, "opencl") {
				// Basic win-x64 build without extra suffixes (legacy naming)
				score += 10
			}
		}

		// Generic non-win scoring: plain distro build outranks specialized
		// variants (vulkan/sycl/openvino); kleidiai builds stay excluded; an
		// explicit arch tag outranks untagged oddities (ubuntu-s390x carries
		// no x64/arm64 tag).
		if platformKey != "win" && !strings.Contains(name, "kleidiai") {
			score += 10
			if !strings.Contains(name, "vulkan") && !strings.Contains(name, "sycl") && !strings.Contains(name, "openvino") {
				score += 10
			}
			if strings.Contains(name, "x64") || strings.Contains(name, "arm64") {
				score += 5
			}
			// Any GPU vendor present: ubuntu-vulkan is the only GPU-accelerated
			// Linux build (no ubuntu cuda variant exists) and accelerates
			// NVIDIA, AMD and Intel alike
			if hasGPU && strings.Contains(name, "vulkan") {
				score += 80
			}
		}

		candidates = append(candidates, scored{asset: a, score: score})
	}

	if len(candidates) == 0 {
		return nil
	}

	// Pick highest score
	best := candidates[0]
	for _, c := range candidates[1:] {
		if c.score > best.score {
			best = c
		}
	}

	return best.asset
}

// pickCudartAssetFor returns the cudart runtime asset pairing with the chosen
// main CUDA asset. cudaVer and arch must be derived from the main asset name
// (cudaVerTagOf / archKeyOf), not from the local nvcc toolkit: the runtime DLLs
// must match the build actually downloaded, which floor/tie-break selection
// may pick independently of the installed toolkit. It matches
// cudart-llama-bin-win-cuda-<cudaVer>-<arch>.zip case-insensitively. With an
// empty cudaVer it skips exact-version matching and falls back to the first
// cudart asset with a matching arch (best-effort when the main asset carries
// no parseable cuda version but still needs the runtime to launch).
// This function does not check the platform: whether to attach the runtime is
// decided by the caller (the Windows+CUDA check in downloadLlamaCpp), letting
// tests construct cudart assets directly and assert matching across platforms.
func pickCudartAssetFor(assets []GitHubAsset, cudaVer, arch string) *GitHubAsset {
	for i := range assets {
		a := &assets[i]
		lower := strings.ToLower(a.Name)
		if !strings.HasPrefix(lower, "cudart-llama-bin-win-cuda-") {
			continue
		}
		// The runtime arch tag must pair with the main asset (x64 / arm64)
		if arch != "" && !strings.Contains(lower, arch) {
			continue
		}
		if cudaVer == "" {
			return a
		}
		if arch != "" && lower == "cudart-llama-bin-win-cuda-"+cudaVer+"-"+arch+".zip" {
			return a
		}
	}
	return nil
}

func setDownloadError(msg string) {
	downloadMu.Lock()
	downloadState.Status = "error"
	downloadState.Error = msg
	downloadMu.Unlock()
	log.Printf("[ERROR] llama.cpp download error: %s", msg)
}
