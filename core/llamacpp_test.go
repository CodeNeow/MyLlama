package core

import (
	"archive/zip"
	"bytes"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"
)

// llamaServerBinName returns the llama-server binary filename for the current platform
// (Windows includes .exe suffix), used by tests to construct stubs.
func llamaServerBinName() string {
	if runtime.GOOS == "windows" {
		return "llama-server.exe"
	}
	return "llama-server"
}

// saveDownloadState snapshots llama.cpp download-related globals and restores them
// after the test, preventing full-chain downloadLlamaCpp tests from polluting other
// test cases (same style as saveServerState).
// llamaCacheValid is an atomic independent of downloadMu; read directly and restored
// in cleanup.
func saveDownloadState(t *testing.T) {
	t.Helper()
	downloadMu.Lock()
	orig := *downloadState
	origCancel := downloadCancel
	downloadMu.Unlock()
	origLlamaCache := llamaCacheValid.Load()
	t.Cleanup(func() {
		downloadMu.Lock()
		*downloadState = orig
		downloadCancel = origCancel
		downloadMu.Unlock()
		llamaCacheValid.Store(origLlamaCache)
	})
}

// TestGetLlamaCppInfoDetectsDownloadDir verifies getLlamaCppInfo can detect llama-server
// in the llama-cpp/ download directory (the default landing path after download+extract):
// after switching to a temp directory and creating an empty stub, Installed=true and
// Path is an absolute path; the control group (no llama-cpp/ directory) → Installed=false.
// Previously detection only checked PATH and custom directories; a successfully extracted
// binary could never be recognized as installed (home page showed "not found").

// wantSamePath asserts the produced path points at the expected stub file,
// compared by file identity (SameFile) rather than string form: darwin's
// os.Getwd returns the physical /private/var prefix while t.TempDir hands out
// the logical /var path, and windows CI cwd may carry the 8.3 short form
// (RUNNER~1) — the same file under different path strings.
func wantSamePath(t *testing.T, got, want string) {
	t.Helper()
	gi, err := os.Stat(got)
	if err != nil {
		t.Fatalf("stat produced path %q: %v", got, err)
	}
	wi, err := os.Stat(want)
	if err != nil {
		t.Fatalf("stat expected path %q: %v", want, err)
	}
	if !os.SameFile(gi, wi) {
		t.Fatalf("produced path %q does not point at expected %q", got, want)
	}
}

func TestGetLlamaCppInfoDetectsDownloadDir(t *testing.T) {
	// llama-related binaries on PATH would interfere with the control group (misdetected as installed), skip
	for _, bin := range []string{"llama-server", "llama-cli", "llama.cpp", "llama"} {
		if _, err := exec.LookPath(bin); err == nil {
			t.Skipf("PATH contains %s, cannot verify not-installed scenario, skipping", bin)
		}
	}
	saveServerState(t)

	tmp := withTempCwd(t)
	binName := llamaServerBinName()
	if err := os.MkdirAll("llama-cpp", 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join("llama-cpp", binName), []byte("stub"), 0755); err != nil {
		t.Fatal(err)
	}

	info := getLlamaCppInfo()
	if !info.Installed {
		t.Fatal("Installed should be true when llama-cpp/llama-server stub exists")
	}
	wantSamePath(t, info.Path, filepath.Join(tmp, "llama-cpp", binName))

	// control group: no llama-cpp/ directory should be judged as not installed
	withTempCwd(t)
	if info := getLlamaCppInfo(); info.Installed {
		t.Error("Installed should be false when no llama-cpp/ directory exists")
	}
}

// TestDetectCudartRuntime verifies the cudart-runtime DLL detection feeding
// the per-component status on the Runtime page: the cudart asset's DLLs
// (cudart64_*.dll / cublas*.dll) mark a CUDA build with its runtime present,
// while a binary-only or missing directory does not. Non-Windows never ships
// the Windows-exclusive cudart asset, so detection stays false there.
func TestDetectCudartRuntime(t *testing.T) {
	tmp := withTempCwd(t)

	if detectCudartRuntime("") {
		t.Error("empty dir must never report the cudart runtime")
	}
	if detectCudartRuntime(filepath.Join(tmp, "does-not-exist")) {
		t.Error("missing dir must never report the cudart runtime")
	}

	dir := filepath.Join(tmp, "llama-cpp")
	if err := os.MkdirAll(dir, 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, llamaServerBinName()), []byte("stub"), 0755); err != nil {
		t.Fatal(err)
	}
	if detectCudartRuntime(dir) {
		t.Error("binary-only directory must not report the cudart runtime")
	}

	if runtime.GOOS == "windows" {
		for _, dll := range []string{"cudart64_12.dll", "cublas64_12.dll", "cublasLt64_12.dll"} {
			p := filepath.Join(dir, dll)
			if err := os.WriteFile(p, []byte("stub"), 0644); err != nil {
				t.Fatal(err)
			}
			if !detectCudartRuntime(dir) {
				t.Errorf("%s present should report the cudart runtime", dll)
			}
			if err := os.Remove(p); err != nil {
				t.Fatal(err)
			}
		}
	} else if detectCudartRuntime(dir) {
		t.Error("non-Windows must never report the cudart runtime")
	}
}

// TestDetectCudartVersion verifies the CUDA major-family detection from the
// cudart64_*.dll file name feeding the Home page's three-state CUDA compat
// row: the DLL's internal FileVersion does not encode the CUDA release
// version, so the file name is the only reliable source (cudart64_13.dll =
// CUDA 13 family). A bare "12" major cannot prove >= 12.8, so the consumer
// treats 12.x conservatively as not satisfying the floor. Non-Windows never
// ships the Windows-exclusive cudart asset, so detection stays empty there.
func TestDetectCudartVersion(t *testing.T) {
	dir := t.TempDir()

	// Guard cases valid on every platform: empty dir, missing dir, empty
	// directory, and a cublas-only directory (no cudart64_*.dll to parse).
	if detectCudartVersion("") != "" {
		t.Error("empty dir must never report a cudart version")
	}
	if detectCudartVersion(filepath.Join(dir, "does-not-exist")) != "" {
		t.Error("missing dir must never report a cudart version")
	}
	if detectCudartVersion(dir) != "" {
		t.Error("empty directory must not report a cudart version")
	}
	if err := os.WriteFile(filepath.Join(dir, "cublas64_12.dll"), []byte("stub"), 0644); err != nil {
		t.Fatal(err)
	}
	if detectCudartVersion(dir) != "" {
		t.Error("cublas-only directory must not report a cudart version")
	}

	if runtime.GOOS == "windows" {
		for _, tc := range []struct{ dll, major string }{
			{"cudart64_13.dll", "13"},
			{"cudart64_12.dll", "12"},
		} {
			p := filepath.Join(dir, tc.dll)
			if err := os.WriteFile(p, []byte("stub"), 0644); err != nil {
				t.Fatal(err)
			}
			if got := detectCudartVersion(dir); got != tc.major {
				t.Errorf("%s present: detectCudartVersion = %q, want %q", tc.dll, got, tc.major)
			}
			if err := os.Remove(p); err != nil {
				t.Fatal(err)
			}
		}
		// With several runtime DLLs present, the lexicographically first
		// match wins (Glob sorts): 12 sorts before 13.
		for _, dll := range []string{"cudart64_13.dll", "cudart64_12.dll"} {
			if err := os.WriteFile(filepath.Join(dir, dll), []byte("stub"), 0644); err != nil {
				t.Fatal(err)
			}
		}
		if got := detectCudartVersion(dir); got != "12" {
			t.Errorf("multiple cudart DLLs: detectCudartVersion = %q, want first sorted match %q", got, "12")
		}
	} else if detectCudartVersion(dir) != "" {
		t.Error("non-Windows must never report a cudart version")
	}
}

// TestLlamaCppInfoCudartVersionJSON verifies the LlamaCppInfo JSON round-trip
// carries the cudartVersion key (struct tag contract with the frontend).
func TestLlamaCppInfoCudartVersionJSON(t *testing.T) {
	in := LlamaCppInfo{Installed: true, Path: "p", Version: "v", CudartInstalled: true, CudartVersion: "13"}
	data, err := json.Marshal(in)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(data), `"cudartVersion":"13"`) {
		t.Errorf("marshaled JSON missing cudartVersion key: %s", data)
	}
	var out LlamaCppInfo
	if err := json.Unmarshal(data, &out); err != nil {
		t.Fatal(err)
	}
	if out.CudartVersion != "13" {
		t.Errorf("round-trip CudartVersion = %q, want %q", out.CudartVersion, "13")
	}
}

// TestGetLlamaCppInfoDetectsDownloadDirSubdir verifies that when the downloaded zip
// contains a top-level folder (after extraction the binary is under llama-cpp/<one-subdir>/),
// it is still detected, and Path points precisely to the stub inside the subdirectory
// (assert absolute path equality; PATH matches must not misclassify).
func TestGetLlamaCppInfoDetectsDownloadDirSubdir(t *testing.T) {
	saveServerState(t)
	tmp := withTempCwd(t)
	binName := llamaServerBinName()
	subdir := filepath.Join("llama-cpp", "llama-b9999-bin")
	if err := os.MkdirAll(subdir, 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(subdir, binName), []byte("stub"), 0755); err != nil {
		t.Fatal(err)
	}

	info := getLlamaCppInfo()
	if !info.Installed {
		t.Fatal("Installed should be true when llama-cpp/<subdir>/llama-server stub exists")
	}
	wantSamePath(t, info.Path, filepath.Join(tmp, subdir, binName))
}

// TestBuildServerCommandDetectsDownloadDir verifies buildServerCommand can hit the
// llama-server in the llama-cpp/ download directory (previously only checked PATH
// and custom directories; after download the API page could not start the service).
// Download directory has higher priority than PATH; returns absolute path on hit.
func TestBuildServerCommandDetectsDownloadDir(t *testing.T) {
	saveServerState(t)
	tmp := withTempCwd(t)
	binName := llamaServerBinName()
	if err := os.MkdirAll("llama-cpp", 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join("llama-cpp", binName), []byte("stub"), 0755); err != nil {
		t.Fatal(err)
	}

	cfg := ServerConfig{AccessMode: accessLocal, Host: "127.0.0.1", Port: 8080, MaxModels: 1, CacheRAM: 0}
	bin, _, _ := buildServerCommand(cfg, "/tmp/preset.ini", nil)

	wantSamePath(t, bin, filepath.Join(tmp, "llama-cpp", binName))
}

// TestDownloadLlamaCppInvalidatesLlamaCache verifies that after downloadLlamaCpp
// successfully extracts and sets status to done, llamaCacheValid is invalidated
// (previously only model cache was invalidated; GetLlamaCpp still returned the
// mounted-cached false, home page always showed "not found"). Full chain end-to-end:
// httptest returns release JSON containing platform-matching zip assets, zip contains
// llama-server stub; githubReleasesAPI is injected with a local server to avoid
// real network.
func TestDownloadLlamaCppInvalidatesLlamaCache(t *testing.T) {
	saveDownloadState(t)
	saveServerState(t)
	withTempCwd(t)

	// build a minimal zip containing a llama-server stub
	var buf bytes.Buffer
	zw := zip.NewWriter(&buf)
	w, err := zw.Create(llamaServerBinName())
	if err != nil {
		t.Fatal(err)
	}
	if _, err := w.Write([]byte("stub")); err != nil {
		t.Fatal(err)
	}
	if err := zw.Close(); err != nil {
		t.Fatal(err)
	}
	zipBytes := buf.Bytes()

	// construct asset name for current platform (must contain platform keyword to be picked by pickBestAsset;
	// Windows names like llama-b9999-bin-win-cpu-x64.zip)
	platformKey := map[string]string{"windows": "win", "darwin": "macos", "linux": "linux"}[runtime.GOOS]
	archKey := map[string]string{"amd64": "x64", "arm64": "arm64"}[runtime.GOARCH]
	assetName := fmt.Sprintf("llama-b9999-bin-%s-%s.zip", platformKey, archKey)

	var srv *httptest.Server
	srv = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/release":
			w.Header().Set("Content-Type", "application/json")
			json.NewEncoder(w).Encode(GitHubRelease{
				TagName: "b9999",
				Assets: []GitHubAsset{{
					Name:               assetName,
					Size:               int64(len(zipBytes)),
					BrowserDownloadURL: srv.URL + "/llama.zip",
				}},
			})
		case "/llama.zip":
			w.Write(zipBytes)
		default:
			http.NotFound(w, r)
		}
	}))
	defer srv.Close()

	origAPI := githubReleasesAPI
	githubReleasesAPI = srv.URL + "/release"
	defer func() { githubReleasesAPI = origAPI }()

	// pre-populate cache as valid; verify it is invalidated after download completes
	llamaCacheValid.Store(true)

	downloadLlamaCpp()

	if llamaCacheValid.Load() {
		t.Error("llamaCacheValid should be false after downloadLlamaCpp completes")
	}
	downloadMu.Lock()
	status := downloadState.Status
	downloadMu.Unlock()
	if status != "done" {
		t.Errorf("download status = %q, want done", status)
	}
	if _, err := os.Stat(filepath.Join("llama-cpp", llamaServerBinName())); err != nil {
		t.Errorf("extracted artifact missing: %v", err)
	}
}

// TestDownloadLlamaCppUsesCustomDir verifies that after setting a custom llama.cpp
// directory, downloadLlamaCpp extracts download artifacts into that custom directory
// instead of the default llama-cpp/ (previously extraction was fixed to llama-cpp/,
// causing product landing and detection positions to mismatch in custom-dir scenarios).
// Full chain end-to-end: httptest returns release JSON containing platform-matching
// zip assets, zip contains llama-server stub; llamaCppDownloadDirOverride points
// to another temp directory.
func TestDownloadLlamaCppUsesDownloadDir(t *testing.T) {
	saveDownloadState(t)
	saveServerState(t)
	// llama.cpp download path save/restore is handled by saveServerState, set it directly here
	customDir := t.TempDir()
	withTempCwd(t)

	llamaCppDownloadDirMu.Lock()
	llamaCppDownloadDirOverride = customDir
	llamaCppDownloadDirMu.Unlock()

	// build a minimal zip containing a llama-server stub
	var buf bytes.Buffer
	zw := zip.NewWriter(&buf)
	w, err := zw.Create(llamaServerBinName())
	if err != nil {
		t.Fatal(err)
	}
	if _, err := w.Write([]byte("stub")); err != nil {
		t.Fatal(err)
	}
	if err := zw.Close(); err != nil {
		t.Fatal(err)
	}
	zipBytes := buf.Bytes()

	// construct asset name for current platform (same style as TestDownloadLlamaCppInvalidatesLlamaCache)
	platformKey := map[string]string{"windows": "win", "darwin": "macos", "linux": "linux"}[runtime.GOOS]
	archKey := map[string]string{"amd64": "x64", "arm64": "arm64"}[runtime.GOARCH]
	assetName := fmt.Sprintf("llama-b9999-bin-%s-%s.zip", platformKey, archKey)

	var srv *httptest.Server
	srv = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/release":
			w.Header().Set("Content-Type", "application/json")
			json.NewEncoder(w).Encode(GitHubRelease{
				TagName: "b9999",
				Assets: []GitHubAsset{{
					Name:               assetName,
					Size:               int64(len(zipBytes)),
					BrowserDownloadURL: srv.URL + "/llama.zip",
				}},
			})
		case "/llama.zip":
			w.Write(zipBytes)
		default:
			http.NotFound(w, r)
		}
	}))
	defer srv.Close()

	origAPI := githubReleasesAPI
	githubReleasesAPI = srv.URL + "/release"
	defer func() { githubReleasesAPI = origAPI }()

	// pre-populate cache as valid; verify it is invalidated after download completes (same as control group)
	llamaCacheValid.Store(true)

	downloadLlamaCpp()

	if llamaCacheValid.Load() {
		t.Error("llamaCacheValid should be false after downloadLlamaCpp completes")
	}
	downloadMu.Lock()
	status := downloadState.Status
	downloadMu.Unlock()
	if status != "done" {
		t.Fatalf("download status = %q, want done", status)
	}

	// extracted artifact should land in the custom directory (llama-server stub directly at zip root)
	if _, err := os.Stat(filepath.Join(customDir, llamaServerBinName())); err != nil {
		t.Errorf("extracted artifact missing in custom directory: %v", err)
	}
	// the default llama-cpp/ directory should not contain the artifact (not installed to default dir)
	if _, err := os.Stat(filepath.Join("llama-cpp", llamaServerBinName())); err == nil {
		t.Error("extracted artifact should not exist under llama-cpp/ (should install to custom dir only)")
	}
}

// makeZip constructs a minimal zip containing files (filename → content) and returns
// the byte stream, used by downloadLlamaCpp full-chain tests to simulate release assets.
func makeZip(t *testing.T, files map[string]string) []byte {
	t.Helper()
	var buf bytes.Buffer
	zw := zip.NewWriter(&buf)
	for name, content := range files {
		w, err := zw.Create(name)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := w.Write([]byte(content)); err != nil {
			t.Fatal(err)
		}
	}
	if err := zw.Close(); err != nil {
		t.Fatal(err)
	}
	return buf.Bytes()
}

// TestDownloadLlamaCppDownloadsCudartOnWindows verifies that on Windows, llama.cpp CUDA
// builds (since b10342, main program and cudart runtime are split into two zip assets)
// download both the main program and cudart assets and extract them to the same directory:
// the target directory contains both llama-server and cublas64_12.dll stub (previously
// only the first-matched cudart asset was selected, extraction artifacts only contained
// the runtime library with no main program); downloadState.Total is the sum of both
// asset sizes, FileName stops at the last downloaded asset name. Asset name version
// is fixed at 12.4 and the GPU probe is stubbed to a pre-Blackwell compute capability,
// keeping asset selection deterministic regardless of host toolkit or GPU.
// Non-Windows platforms do not attach the Windows-exclusive cudart
// asset (matching logic is covered by unit tests), skipped.
func TestDownloadLlamaCppDownloadsCudartOnWindows(t *testing.T) {
	if runtime.GOOS != "windows" {
		t.Skip("cudart co-download is Windows-only; skipped on non-Windows (matching logic covered by unit tests)")
	}
	saveDownloadState(t)
	saveServerState(t)
	withTempCwd(t)

	// build the main-program zip (llama-server stub) and the cudart zip (cublas64_12.dll stub)
	mainZip := makeZip(t, map[string]string{llamaServerBinName(): "stub"})
	cudartZip := makeZip(t, map[string]string{"cublas64_12.dll": "stub"})

	// fixed version label keeps asset selection deterministic regardless of the host toolkit
	ver := "12.4"
	mainName := fmt.Sprintf("llama-b9999-bin-win-cuda-%s-x64.zip", ver)
	cudartName := fmt.Sprintf("cudart-llama-bin-win-cuda-%s-x64.zip", ver)

	var srv *httptest.Server
	srv = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/release":
			w.Header().Set("Content-Type", "application/json")
			json.NewEncoder(w).Encode(GitHubRelease{
				TagName: "b9999",
				// mimic real release order: cudart listed first, verifying the main program is still selected
				Assets: []GitHubAsset{
					{Name: cudartName, Size: int64(len(cudartZip)), BrowserDownloadURL: srv.URL + "/cudart.zip"},
					{Name: mainName, Size: int64(len(mainZip)), BrowserDownloadURL: srv.URL + "/main.zip"},
				},
			})
		case "/main.zip":
			w.Write(mainZip)
		case "/cudart.zip":
			w.Write(cudartZip)
		default:
			http.NotFound(w, r)
		}
	}))
	defer srv.Close()

	origAPI := githubReleasesAPI
	githubReleasesAPI = srv.URL + "/release"
	defer func() { githubReleasesAPI = origAPI }()

	// stub the GPU probe: a pre-Blackwell host (no CUDA floor) keeps asset selection
	// deterministic — Blackwell hosts would floor-skip the 12.4 fixture asset
	origCC := probeGPUComputeCap
	probeGPUComputeCap = func() string { return "8.9" }
	defer func() { probeGPUComputeCap = origCC }()

	downloadLlamaCpp()

	downloadMu.Lock()
	status := downloadState.Status
	fileName := downloadState.FileName
	total := downloadState.Total
	errMsg := downloadState.Error
	downloadMu.Unlock()

	if status != "done" {
		t.Fatalf("download status = %q, want done (error: %s)", status, errMsg)
	}
	// with main program first then cudart, FileName should stop at the last downloaded cudart asset name
	if fileName != cudartName {
		t.Errorf("FileName = %q, want last downloaded cudart asset %q", fileName, cudartName)
	}
	// progress Total should be the sum of both asset sizes
	wantTotal := int64(len(mainZip) + len(cudartZip))
	if total != wantTotal {
		t.Errorf("Total = %d, want sum of both assets %d", total, wantTotal)
	}
	if _, err := os.Stat(filepath.Join("llama-cpp", llamaServerBinName())); err != nil {
		t.Errorf("main program artifact missing after extraction: %v", err)
	}
	if _, err := os.Stat(filepath.Join("llama-cpp", "cublas64_12.dll")); err != nil {
		t.Errorf("cudart runtime artifact missing after extraction: %v", err)
	}
}

// TestGetLlamaCppInfoDownloadDirOverridesImported verifies the detection chain
// priority: the configured llama.cpp download path wins over the imported
// customLlamaCppDir, which in turn wins over PATH.
func TestGetLlamaCppInfoDownloadDirOverridesImported(t *testing.T) {
	for _, bin := range []string{"llama-server", "llama-cli", "llama.cpp", "llama"} {
		if _, err := exec.LookPath(bin); err == nil {
			t.Skipf("PATH contains %s, cannot verify not-installed scenario, skipping", bin)
		}
	}
	saveServerState(t)

	downloadPath := t.TempDir()
	importedPath := t.TempDir()
	binName := llamaServerBinName()
	if err := os.WriteFile(filepath.Join(downloadPath, binName), []byte("download"), 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(importedPath, binName), []byte("imported"), 0755); err != nil {
		t.Fatal(err)
	}

	llamaCppDownloadDirMu.Lock()
	llamaCppDownloadDirOverride = downloadPath
	llamaCppDownloadDirMu.Unlock()
	customLlamaCppMu.Lock()
	customLlamaCppDir = importedPath
	customLlamaCppMu.Unlock()

	info := getLlamaCppInfo()
	if !info.Installed {
		t.Fatal("Installed should be true when a stub exists in the download path")
	}
	wantPath := filepath.Join(downloadPath, binName)
	if info.Path != wantPath {
		t.Errorf("Path = %q, want %q (download path must take priority over the imported dir)", info.Path, wantPath)
	}

	// With only the imported dir set, detection falls back to it.
	llamaCppDownloadDirMu.Lock()
	llamaCppDownloadDirOverride = ""
	llamaCppDownloadDirMu.Unlock()
	info = getLlamaCppInfo()
	if !info.Installed {
		t.Fatal("Installed should be true when only the imported dir holds a stub")
	}
	if info.Path != filepath.Join(importedPath, binName) {
		t.Errorf("Path = %q, want %q (imported dir fallback)", info.Path, filepath.Join(importedPath, binName))
	}
}

// TestDownloadLlamaCppNightlyPrereleaseFallback verifies the full-chain fallback when
// llama.cpp's releases/latest (non-prerelease only) is an asset-less marker — the state
// upstream entered in 2026-08, where binaries ship only in nightly prereleases. The
// injected "latest" release carries only nightly-tag.txt; the injected release list has
// a marker-only newest entry followed by one with a platform-matching zip, and the
// download must complete from that fallback release instead of failing with
// "no llama.cpp build found".
func TestDownloadLlamaCppNightlyPrereleaseFallback(t *testing.T) {
	saveDownloadState(t)
	saveServerState(t)
	withTempCwd(t)

	zipBytes := makeZip(t, map[string]string{llamaServerBinName(): "stub"})

	platformKey := map[string]string{"windows": "win", "darwin": "macos", "linux": "linux"}[runtime.GOOS]
	archKey := map[string]string{"amd64": "x64", "arm64": "arm64"}[runtime.GOARCH]
	assetName := fmt.Sprintf("llama-b10586-bin-%s-%s.zip", platformKey, archKey)

	var srv *httptest.Server
	srv = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/release": // releases/latest: asset-less marker
			w.Header().Set("Content-Type", "application/json")
			json.NewEncoder(w).Encode(GitHubRelease{
				TagName: "b10590",
				Assets:  []GitHubAsset{{Name: "nightly-tag.txt", Size: 9, BrowserDownloadURL: srv.URL + "/tag.txt"}},
			})
		case "/releases": // list newest-first: marker, then the binary release
			w.Header().Set("Content-Type", "application/json")
			json.NewEncoder(w).Encode([]GitHubRelease{
				{TagName: "b10588", Assets: []GitHubAsset{{Name: "nightly-tag.txt", Size: 9}}},
				{TagName: "b10586", Assets: []GitHubAsset{{
					Name: assetName, Size: int64(len(zipBytes)), BrowserDownloadURL: srv.URL + "/llama.zip",
				}}},
			})
		case "/llama.zip":
			w.Write(zipBytes)
		default:
			http.NotFound(w, r)
		}
	}))
	defer srv.Close()

	origAPI, origList := githubReleasesAPI, githubReleasesListAPI
	githubReleasesAPI = srv.URL + "/release"
	githubReleasesListAPI = srv.URL + "/releases"
	defer func() {
		githubReleasesAPI = origAPI
		githubReleasesListAPI = origList
	}()

	downloadLlamaCpp()

	downloadMu.Lock()
	status := downloadState.Status
	downloadMu.Unlock()
	if status != "done" {
		t.Fatalf("download status = %q, want done (nightly prerelease fallback)", status)
	}
	if _, err := os.Stat(filepath.Join("llama-cpp", llamaServerBinName())); err != nil {
		t.Errorf("extracted artifact missing: %v", err)
	}
}

// TestDownloadLlamaCppCancelDuringFetch verifies that cancelling while the
// release metadata is still being fetched (status "fetching") aborts the
// in-flight HTTP request immediately and resets the state to idle — previously
// the fetch request carried no cancel context, so the Cancel button appeared
// dead until the 30s HTTP timeout elapsed.
func TestDownloadLlamaCppCancelDuringFetch(t *testing.T) {
	saveDownloadState(t)
	saveServerState(t)
	withTempCwd(t)

	// /release blocks until the test signals; cancel must unblock it via ctx
	unblock := make(chan struct{})
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/release" {
			select {
			case <-unblock:
			case <-r.Context().Done(): // aborted by the download's cancel ctx
			}
			return
		}
		http.NotFound(w, r)
	}))
	defer srv.Close()
	defer close(unblock)

	origAPI := githubReleasesAPI
	githubReleasesAPI = srv.URL + "/release"
	defer func() { githubReleasesAPI = origAPI }()

	done := make(chan struct{})
	go func() {
		defer close(done)
		downloadLlamaCpp()
	}()

	// wait until the fetch is in flight (status flips to fetching), then cancel
	deadline := time.Now().Add(5 * time.Second)
	for {
		downloadMu.Lock()
		status := downloadState.Status
		cancelDownload := downloadCancel
		downloadMu.Unlock()
		if status == "fetching" && cancelDownload != nil {
			cancelDownload()
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("download never entered fetching state")
		}
		time.Sleep(20 * time.Millisecond)
	}

	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("cancel during fetch did not abort downloadLlamaCpp within 5s")
	}

	downloadMu.Lock()
	status := downloadState.Status
	errMsg := downloadState.Error
	downloadMu.Unlock()
	if status != "idle" {
		t.Errorf("status = %q after cancel-during-fetch, want idle", status)
	}
	if errMsg != "" {
		t.Errorf("Error = %q after cancel-during-fetch, want empty", errMsg)
	}
}

// TestDownloadLlamaCppStopWhilePaused verifies that stopping from the paused
// state resets the download to idle. The previous reset guard skipped the
// status change when it was "paused", stranding the state machine in paused
// with the download goroutine already exited — afterwards both Cancel and
// Resume appeared dead (the user-visible "no reaction" bug).
func TestDownloadLlamaCppStopWhilePaused(t *testing.T) {
	saveDownloadState(t)
	saveServerState(t)
	withTempCwd(t)

	zipBytes := makeZip(t, map[string]string{llamaServerBinName(): "stub"})
	platformKey := map[string]string{"windows": "win", "darwin": "macos", "linux": "linux"}[runtime.GOOS]
	archKey := map[string]string{"amd64": "x64", "arm64": "arm64"}[runtime.GOARCH]
	assetName := fmt.Sprintf("llama-b9999-bin-%s-%s.zip", platformKey, archKey)

	var srv *httptest.Server
	srv = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/release":
			w.Header().Set("Content-Type", "application/json")
			json.NewEncoder(w).Encode(GitHubRelease{
				TagName: "b9999",
				Assets: []GitHubAsset{{
					Name: assetName, Size: 1 << 20, BrowserDownloadURL: srv.URL + "/llama.zip",
				}},
			})
		case "/llama.zip":
			// slow trickle so the download stays in flight until paused
			for i := 0; i < 500; i++ {
				select {
				case <-r.Context().Done():
					return
				default:
				}
				if _, err := w.Write(zipBytes); err != nil {
					return
				}
				if f, ok := w.(http.Flusher); ok {
					f.Flush()
				}
				time.Sleep(10 * time.Millisecond)
			}
		default:
			http.NotFound(w, r)
		}
	}))
	defer srv.Close()

	origAPI := githubReleasesAPI
	githubReleasesAPI = srv.URL + "/release"
	defer func() { githubReleasesAPI = origAPI }()

	done := make(chan struct{})
	go func() {
		defer close(done)
		downloadLlamaCpp()
	}()

	waitFor := func(want string) {
		deadline := time.Now().Add(5 * time.Second)
		for {
			downloadMu.Lock()
			status := downloadState.Status
			downloadMu.Unlock()
			if status == want {
				return
			}
			if time.Now().After(deadline) {
				t.Fatalf("status never reached %q", want)
			}
			time.Sleep(20 * time.Millisecond)
		}
	}

	waitFor("downloading")
	(&App{}).PauseLlamaCppDownload()
	waitFor("paused")
	time.Sleep(100 * time.Millisecond) // let the goroutine settle into waitForResume

	downloadMu.Lock()
	cancelDownload := downloadCancel
	downloadMu.Unlock()
	if cancelDownload == nil {
		t.Fatal("downloadCancel is nil while paused")
	}
	cancelDownload()

	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("stop from paused did not end the download goroutine within 5s")
	}

	downloadMu.Lock()
	status := downloadState.Status
	paused := downloadState.Paused
	errMsg := downloadState.Error
	downloadMu.Unlock()
	if status != "idle" {
		t.Errorf("status = %q after stop-from-paused, want idle", status)
	}
	if paused {
		t.Error("Paused flag should be cleared after stop-from-paused")
	}
	if errMsg != "" {
		t.Errorf("Error = %q after stop-from-paused, want empty", errMsg)
	}
}

// TestDetectAccel verifies the acceleration-backend classification from the
// directory holding the resolved llama-server binary: official builds are
// GGML_BACKEND_DL, so the GPU support is whichever backend library sits next
// to the binaries (windows ggml-cuda.dll / ggml-vulkan.dll; linux
// libggml-vulkan.so or plain ggml-vulkan.so); darwin is arch-driven (arm64 =
// embedded Metal, amd64 = CPU-only release), android is CPU-only, an empty
// directory means "not installed", and unrecognized builds degrade to "cpu".
func TestDetectAccel(t *testing.T) {
	dir := t.TempDir()
	write := func(name string) {
		t.Helper()
		if err := os.WriteFile(filepath.Join(dir, name), []byte("x"), 0644); err != nil {
			t.Fatal(err)
		}
	}

	// Not installed: empty dir argument
	if got := detectAccel("", "windows", "amd64"); got != "" {
		t.Errorf("detectAccel(empty) = %q, want empty", got)
	}

	// No recognized backend library → cpu (every platform)
	if got := detectAccel(dir, "windows", "amd64"); got != "cpu" {
		t.Errorf("bare dir on windows = %q, want cpu", got)
	}

	write("ggml-cuda.dll")
	if got := detectAccel(dir, "windows", "amd64"); got != "cuda" {
		t.Errorf("windows + ggml-cuda.dll = %q, want cuda", got)
	}
	// CUDA wins over vulkan when both are present (deterministic preference)
	write("ggml-vulkan.dll")
	if got := detectAccel(dir, "windows", "amd64"); got != "cuda" {
		t.Errorf("windows + both backends = %q, want cuda", got)
	}
	if err := os.Remove(filepath.Join(dir, "ggml-cuda.dll")); err != nil {
		t.Fatal(err)
	}
	if got := detectAccel(dir, "windows", "amd64"); got != "vulkan" {
		t.Errorf("windows + ggml-vulkan.dll = %q, want vulkan", got)
	}

	if err := os.Remove(filepath.Join(dir, "ggml-vulkan.dll")); err != nil {
		t.Fatal(err)
	}
	write("libggml-vulkan.so")
	if got := detectAccel(dir, "linux", "amd64"); got != "vulkan" {
		t.Errorf("linux + libggml-vulkan.so = %q, want vulkan", got)
	}
	if err := os.Remove(filepath.Join(dir, "libggml-vulkan.so")); err != nil {
		t.Fatal(err)
	}
	write("ggml-vulkan.so")
	if got := detectAccel(dir, "linux", "amd64"); got != "vulkan" {
		t.Errorf("linux + ggml-vulkan.so = %q, want vulkan", got)
	}
	if got := detectAccel(dir, "linux", "amd64"); got == "cuda" {
		t.Errorf("linux has no cuda backend lib form, got %q", got)
	}

	// darwin: arch-driven, no file needed
	if got := detectAccel(dir, "darwin", "arm64"); got != "metal" {
		t.Errorf("darwin arm64 = %q, want metal", got)
	}
	if got := detectAccel(dir, "darwin", "amd64"); got != "cpu" {
		t.Errorf("darwin amd64 = %q, want cpu (x64 release is CPU-only)", got)
	}

	// android: CPU-only regardless of directory contents
	if got := detectAccel(dir, "android", "arm64"); got != "cpu" {
		t.Errorf("android = %q, want cpu", got)
	}
}

// TestParseAppleGPUName verifies the system_profiler SPDisplaysDataType JSON
// parsing: the first sppci_model wins, empty/garbage output and a report
// without the field fall back to the static "Apple Silicon (Metal)" label.
func TestParseAppleGPUName(t *testing.T) {
	got := parseAppleGPUName(`{"SPDisplaysDataType":[{"sppci_model":"Apple M4 Pro","spdisplays_ndrvs":[]}]}`)
	if got != "Apple M4 Pro" {
		t.Errorf("parseAppleGPUName(chip) = %q, want Apple M4 Pro", got)
	}
	got = parseAppleGPUName(`{"SPDisplaysDataType":[{"sppci_model":""},{"sppci_model":"Apple M2 Max"}]}`)
	if got != "Apple M2 Max" {
		t.Errorf("parseAppleGPUName(second entry) = %q, want Apple M2 Max", got)
	}
	for _, out := range []string{"", "not json", `{"SPDisplaysDataType":[{}]}`, `{"SPDisplaysDataType":[]}`} {
		if got := parseAppleGPUName(out); got != "Apple Silicon (Metal)" {
			t.Errorf("parseAppleGPUName(%q) = %q, want fallback", out, got)
		}
	}
}
