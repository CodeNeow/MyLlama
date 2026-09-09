package core

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"
)

// TestPickBestAssetForWindowsCUDA verifies that in Windows+CUDA environments, the asset
// with an exact match to the host CUDA version is preferred.
func TestPickBestAssetForWindowsCUDA(t *testing.T) {
	assets := []GitHubAsset{
		{Name: "llama-b3840-bin-win-cuda-12.8-x64.zip"},
		{Name: "llama-b3840-bin-win-cuda-12.4-x64.zip"},
		{Name: "llama-b3840-bin-win-avx2-x64.zip"},
		{Name: "llama-b3840-bin-win-cpu-x64.zip"},
	}
	got := pickBestAssetFor(assets, "windows", "amd64", true, "12.8", 0)
	if got == nil || got.Name != "llama-b3840-bin-win-cuda-12.8-x64.zip" {
		t.Errorf("should pick cuda-12.8 exact-match build, got %v", got)
	}
}

// TestPickBestAssetForWindowsCPU verifies that when no GPU is present, a CPU build is
// selected instead of cuda: the "-cpu-" asset wins thanks to its decisive bonus, and
// legacy "avx2"-style names still win on releases predating the "-cpu-" naming.
func TestPickBestAssetForWindowsCPU(t *testing.T) {
	assets := []GitHubAsset{
		{Name: "llama-b3840-bin-win-cuda-12.8-x64.zip"},
		{Name: "llama-b3840-bin-win-avx2-x64.zip"},
		{Name: "llama-b3840-bin-win-cpu-x64.zip"},
	}
	got := pickBestAssetFor(assets, "windows", "amd64", false, "", 0)
	if got == nil || got.Name != "llama-b3840-bin-win-cpu-x64.zip" {
		t.Errorf("no GPU should pick cpu build, got %v", got)
	}

	// Backward compat: without a "-cpu-" asset the legacy avx2 build wins.
	legacy := []GitHubAsset{
		{Name: "llama-b9999-bin-win-cuda-12.4-x64.zip"},
		{Name: "llama-b9999-bin-win-avx2-x64.zip"},
	}
	got = pickBestAssetFor(legacy, "windows", "amd64", false, "", 0)
	if got == nil || got.Name != "llama-b9999-bin-win-avx2-x64.zip" {
		t.Errorf("no GPU and no cpu asset should pick legacy avx2 build, got %v", got)
	}
}

// TestPickBestAssetForLinuxArm64 verifies that Linux arm64 matches the arm64 archive
// (legacy "linux-" keyword still accepted alongside the current "ubuntu-" naming).
func TestPickBestAssetForLinuxArm64(t *testing.T) {
	assets := []GitHubAsset{
		{Name: "llama-b3840-bin-linux-x64.zip"},
		{Name: "llama-b3840-bin-linux-arm64.zip"},
		{Name: "llama-b3840-bin-macos-arm64.zip"},
	}
	got := pickBestAssetFor(assets, "linux", "arm64", false, "", 0)
	if got == nil || got.Name != "llama-b3840-bin-linux-arm64.zip" {
		t.Errorf("should pick linux arm64 build, got %v", got)
	}
}

// TestPickBestAssetForNoMatch verifies that nil is returned when no asset matches
// the current platform.
func TestPickBestAssetForNoMatch(t *testing.T) {
	assets := []GitHubAsset{{Name: "llama-b3840-bin-macos-arm64.zip"}}
	if got := pickBestAssetFor(assets, "windows", "amd64", false, "", 0); got != nil {
		t.Errorf("no windows assets should return nil, got %v", got)
	}
	if got := pickBestAssetFor(nil, "windows", "amd64", false, "", 0); got != nil {
		t.Errorf("nil asset list should return nil, got %v", got)
	}
	// Android builds are arm64-only: an x64 Android host matches nothing
	// (the arm64 arch tag is enforced even within the matching platform).
	androidOnly := []GitHubAsset{{Name: "llama-b9999-bin-android-arm64.tar.gz"}}
	if got := pickBestAssetFor(androidOnly, "android", "amd64", false, "", 0); got != nil {
		t.Errorf("arm64-only android assets must not match an x64 android host, got %v", got)
	}
}

// TestPickBestAssetForAndroid verifies the Android platform branch: the
// "android" keyword matches the upstream arm64 CPU-only tarball
// (llama-b*-bin-android-arm64.tar.gz), and an Android host never falls back
// to desktop (ubuntu / macos / win) builds — without an android asset the
// picker returns nil so the download surfaces a clear "no build found"
// instead of installing a binary the device cannot run.
func TestPickBestAssetForAndroid(t *testing.T) {
	assets := []GitHubAsset{
		{Name: "llama-b3840-bin-android-arm64.tar.gz"},
		{Name: "llama-b3840-bin-ubuntu-x64.tar.gz"},
		{Name: "llama-b3840-bin-macos-arm64.tar.gz"},
		{Name: "llama-b3840-bin-win-cpu-x64.zip"},
	}
	got := pickBestAssetFor(assets, "android", "arm64", false, "", 0)
	if got == nil || got.Name != "llama-b3840-bin-android-arm64.tar.gz" {
		t.Errorf("android arm64 should pick the android-arm64 tarball, got %v", got)
	}

	// Cross-platform isolation: with the android asset removed, an android
	// host picks none of the ubuntu/macos/win assets.
	if got := pickBestAssetFor(assets[1:], "android", "arm64", false, "", 0); got != nil {
		t.Errorf("android must never pick ubuntu/macos/win desktop assets, got %v", got)
	}
}

// TestPickBestAssetForWindowsCUDAExcludesCudart verifies that when selecting the main
// program asset in a Windows+CUDA environment, cudart runtime assets are excluded:
// since llama.cpp b10342, Windows CUDA builds are split into cudart runtime zip
// (cudart-llama-bin-win-cuda-XX.X-x64.zip) and main program zip (llama-b*-bin-win-cuda-XX.X-x64.zip).
// Both score the same and cudart appears before the main program in the release list;
// without exclusion, only the runtime library would be selected and the main program
// would be missed (user-visible symptom: extracted artifacts only contain
// cublas64_12.dll / cublasLt64_12.dll / cudart64_12.dll, no llama-server.exe).
// Assertion: from a list ordered [cudart, main cuda, main cpu], the main cuda asset
// is selected rather than the preceding cudart.
func TestPickBestAssetForWindowsCUDAExcludesCudart(t *testing.T) {
	assets := []GitHubAsset{
		{Name: "cudart-llama-bin-win-cuda-12.4-x64.zip"},
		{Name: "llama-b9999-bin-win-cuda-12.4-x64.zip"},
		{Name: "llama-b9999-bin-win-cpu-x64.zip"},
	}
	got := pickBestAssetFor(assets, "windows", "amd64", true, "12.4", 0)
	if got == nil || got.Name != "llama-b9999-bin-win-cuda-12.4-x64.zip" {
		t.Errorf("should still pick main cuda asset when cudart comes first, got %v", got)
	}
}

// b10453Assets mirrors the verbatim asset list of llama.cpp release b10453
// (asset selection regression fixture covering the current upstream naming).
var b10453Assets = []GitHubAsset{
	{Name: "cudart-llama-bin-win-cuda-12.4-x64.zip"},
	{Name: "cudart-llama-bin-win-cuda-13.3-x64.zip"},
	{Name: "cudart-llama-bin-win-cuda-13.4-arm64.zip"},
	{Name: "llama-b10453-bin-android-arm64.tar.gz"},
	{Name: "llama-b10453-bin-macos-arm64.tar.gz"},
	{Name: "llama-b10453-bin-macos-x64.tar.gz"},
	{Name: "llama-b10453-bin-ubuntu-arm64.tar.gz"},
	{Name: "llama-b10453-bin-ubuntu-openvino-2026.2.1-x64.tar.gz"},
	{Name: "llama-b10453-bin-ubuntu-s390x.tar.gz"},
	{Name: "llama-b10453-bin-ubuntu-sycl-fp16-x64.tar.gz"},
	{Name: "llama-b10453-bin-ubuntu-sycl-fp32-x64.tar.gz"},
	{Name: "llama-b10453-bin-ubuntu-vulkan-arm64.tar.gz"},
	{Name: "llama-b10453-bin-ubuntu-vulkan-x64.tar.gz"},
	{Name: "llama-b10453-bin-ubuntu-x64.tar.gz"},
	{Name: "llama-b10453-bin-win-cpu-arm64.zip"},
	{Name: "llama-b10453-bin-win-cpu-x64.zip"},
	{Name: "llama-b10453-bin-win-cuda-12.4-x64.zip"},
	{Name: "llama-b10453-bin-win-cuda-13.3-x64.zip"},
	{Name: "llama-b10453-bin-win-cuda-13.4-arm64.zip"},
	{Name: "llama-b10453-bin-win-opencl-adreno-arm64.zip"},
	{Name: "llama-b10453-bin-win-openvino-2026.2.1-x64.zip"},
	{Name: "llama-b10453-bin-win-rocm-7.14-x64.zip"},
	{Name: "llama-b10453-bin-win-sycl-x64.zip"},
	{Name: "llama-b10453-bin-win-vulkan-x64.zip"},
	{Name: "llama-b10453-ui.tar.gz"},
	{Name: "llama-b10453-xcframework.zip"},
}

// TestPickBestAssetForB10453 is a table-driven scenario walkthrough against the
// verbatim b10453 release asset list, covering every supported host profile:
//   - linux assets are named "ubuntu-*": the platform match must accept the
//     ubuntu keyword (previously "linux" matched nothing and Linux hosts got
//     "No llama.cpp build found");
//   - arch tags are enforced on every platform (an x64 Windows host must not
//     pick win-cpu-arm64, which sorts earlier in the release list);
//   - with an NVIDIA GPU the lowest available CUDA version wins (widest GPU
//     compatibility), while a Blackwell floor (compute capability >= 12.0
//     needs CUDA >= 12.8) hard-skips 12.4 and prefers the highest survivor;
//     the toolkit exact match still wins on top, subject to the floor;
//   - linux + ANY GPU vendor picks ubuntu-vulkan (the only GPU-accelerated
//     linux build; no ubuntu cuda variant exists — the vulkan tarball
//     accelerates NVIDIA, AMD and Intel alike);
//   - android matches the arm64 CPU-only android tarball and never a desktop
//     build, even with a GPU flag set (the GPU bonus only applies to
//     platform-matching assets).
func TestPickBestAssetForB10453(t *testing.T) {
	tests := []struct {
		name     string
		platform string
		arch     string
		hasGPU   bool
		cudaVer  string
		floor    float64
		want     string
	}{
		{"win x64 no GPU", "windows", "amd64", false, "", 0, "llama-b10453-bin-win-cpu-x64.zip"},
		{"win x64 nvidia old gpu lowest cuda", "windows", "amd64", true, "", 0, "llama-b10453-bin-win-cuda-12.4-x64.zip"},
		{"win x64 nvidia blackwell floor", "windows", "amd64", true, "", 12.8, "llama-b10453-bin-win-cuda-13.3-x64.zip"},
		{"win x64 nvidia toolkit 12.4 exact", "windows", "amd64", true, "12.4", 0, "llama-b10453-bin-win-cuda-12.4-x64.zip"},
		{"win x64 blackwell floor overrides toolkit", "windows", "amd64", true, "12.4", 12.8, "llama-b10453-bin-win-cuda-13.3-x64.zip"},
		{"win arm64 no nvidia", "windows", "arm64", false, "", 0, "llama-b10453-bin-win-cpu-arm64.zip"},
		{"linux x64 no GPU", "linux", "amd64", false, "", 0, "llama-b10453-bin-ubuntu-x64.tar.gz"},
		{"linux x64 nvidia prefers vulkan", "linux", "amd64", true, "", 0, "llama-b10453-bin-ubuntu-vulkan-x64.tar.gz"},
		// The linux vulkan bonus fires on any GPU vendor: an AMD-only box
		// (no nvidia-smi hit, PCI probe sees the AMD card) must get the
		// GPU-accelerated vulkan tarball too.
		{"linux x64 amd prefers vulkan", "linux", "amd64", true, "", 0, "llama-b10453-bin-ubuntu-vulkan-x64.tar.gz"},
		{"linux arm64 no GPU", "linux", "arm64", false, "", 0, "llama-b10453-bin-ubuntu-arm64.tar.gz"},
		{"linux arm64 amd prefers vulkan", "linux", "arm64", true, "", 0, "llama-b10453-bin-ubuntu-vulkan-arm64.tar.gz"},
		{"darwin x64", "darwin", "amd64", false, "", 0, "llama-b10453-bin-macos-x64.tar.gz"},
		{"darwin arm64", "darwin", "arm64", false, "", 0, "llama-b10453-bin-macos-arm64.tar.gz"},
		{"android arm64", "android", "arm64", false, "", 0, "llama-b10453-bin-android-arm64.tar.gz"},
		// Android is CPU-only upstream: a GPU flag must not drag the
		// (non-matching) ubuntu-vulkan GPU build in over the android tarball.
		{"android arm64 ignores gpu flag", "android", "arm64", true, "", 0, "llama-b10453-bin-android-arm64.tar.gz"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := pickBestAssetFor(b10453Assets, tt.platform, tt.arch, tt.hasGPU, tt.cudaVer, tt.floor)
			if got == nil || got.Name != tt.want {
				t.Errorf("pickBestAssetFor(%s/%s) = %v, want %s", tt.platform, tt.arch, got, tt.want)
			}
		})
	}
}

// TestGPUComputeCapParsesProbeOutput verifies the parsing of the nvidia-smi
// compute_cap probe via the injectable probeGPUComputeCap var (no shell-out):
// single value, multi-GPU first-value-wins, and empty/garbage inputs.
func TestGPUComputeCapParsesProbeOutput(t *testing.T) {
	orig := probeGPUComputeCap
	defer func() { probeGPUComputeCap = orig }()

	cases := []struct {
		out  string
		want float64
		ok   bool
	}{
		{"12.0", 12.0, true},
		{"8.9", 8.9, true},
		{"8.9\n12.0\n", 8.9, true}, // multi-GPU: first value decides
		{" 12.0 ", 12.0, true},
		{"", 0, false},
		{"not-a-number", 0, false},
	}
	for _, c := range cases {
		probeGPUComputeCap = func() string { return c.out }
		got, ok := gpuComputeCap()
		if ok != c.ok || got != c.want {
			t.Errorf("gpuComputeCap(%q) = (%v, %v), want (%v, %v)", c.out, got, ok, c.want, c.ok)
		}
	}
}

// TestCudaFloorForComputeCap verifies the Blackwell floor rule: compute
// capability >= 12.0 (RTX 50 series) needs CUDA >= 12.8; earlier or unknown
// GPUs get no floor.
func TestCudaFloorForComputeCap(t *testing.T) {
	if got := cudaFloorForComputeCap(12.0); got != 12.8 {
		t.Errorf("cudaFloorForComputeCap(12.0) = %v, want 12.8", got)
	}
	if got := cudaFloorForComputeCap(13.0); got != 12.8 {
		t.Errorf("cudaFloorForComputeCap(13.0) = %v, want 12.8", got)
	}
	for _, cc := range []float64{0, 8.9, 11.0} {
		if got := cudaFloorForComputeCap(cc); got != 0 {
			t.Errorf("cudaFloorForComputeCap(%v) = %v, want 0", cc, got)
		}
	}
}

// TestCudartPairsWithMainAsset verifies the runtime pairing flow used by
// downloadLlamaCpp: the cudart version and arch are extracted from the chosen
// main asset name (not the local toolkit), so the runtime zip always pairs
// with the build actually downloaded — including floor-driven selections and
// the arm64 CUDA variant.
func TestCudartPairsWithMainAsset(t *testing.T) {
	// Blackwell floor picks cuda-13.3-x64; cudart must pair with 13.3/x64.
	main := pickBestAssetFor(b10453Assets, "windows", "amd64", true, "", 12.8)
	if main == nil || main.Name != "llama-b10453-bin-win-cuda-13.3-x64.zip" {
		t.Fatalf("main asset = %v, want cuda-13.3-x64", main)
	}
	ver, ok := cudaVerTagOf(main.Name)
	if !ok || ver != "13.3" {
		t.Fatalf("cudaVerTagOf(main) = (%q, %v), want (13.3, true)", ver, ok)
	}
	cudart := pickCudartAssetFor(b10453Assets, ver, archKeyOf("amd64"))
	if cudart == nil || cudart.Name != "cudart-llama-bin-win-cuda-13.3-x64.zip" {
		t.Errorf("cudart pairing for cuda-13.3-x64 = %v, want 13.3 x64 runtime", cudart)
	}

	// arm64 main asset pairs with the arm64 runtime, not an x64 one.
	armMain := pickBestAssetFor(b10453Assets, "windows", "arm64", true, "", 0)
	if armMain == nil || armMain.Name != "llama-b10453-bin-win-cuda-13.4-arm64.zip" {
		t.Fatalf("arm64 main asset = %v, want cuda-13.4-arm64", armMain)
	}
	armVer, _ := cudaVerTagOf(armMain.Name)
	cudart = pickCudartAssetFor(b10453Assets, armVer, archKeyOf("arm64"))
	if cudart == nil || cudart.Name != "cudart-llama-bin-win-cuda-13.4-arm64.zip" {
		t.Errorf("cudart pairing for cuda-13.4-arm64 = %v, want 13.4 arm64 runtime", cudart)
	}
}

// TestPickCudartAssetFor verifies cudart runtime asset matching:
//   - exact version + arch match, case-insensitive (cudaVer=12.4/arch=x64 →
//     cudart-...cuda-12.4-x64.zip);
//   - arch mismatch returns nil (no arm64 runtime for an x64 main asset);
//   - non-existent version returns nil (11.8 not in list);
//   - no cudart assets returns nil;
//   - empty version falls back to the first cudart asset with a matching arch
//     (best-effort when the main asset carries no parseable cuda version).
func TestPickCudartAssetFor(t *testing.T) {
	assets := []GitHubAsset{
		{Name: "cudart-llama-bin-win-cuda-12.4-x64.zip"},
		{Name: "cudart-llama-bin-win-cuda-13.3-x64.zip"},
		{Name: "cudart-llama-bin-win-cuda-13.4-arm64.zip"},
	}
	if got := pickCudartAssetFor(assets, "12.4", "x64"); got == nil || got.Name != "cudart-llama-bin-win-cuda-12.4-x64.zip" {
		t.Errorf("cudaVer=12.4/x64 should match 12.4 asset, got %v", got)
	}
	if got := pickCudartAssetFor(assets, "13.3", "x64"); got == nil || got.Name != "cudart-llama-bin-win-cuda-13.3-x64.zip" {
		t.Errorf("cudaVer=13.3/x64 should match 13.3 asset, got %v", got)
	}
	if got := pickCudartAssetFor(assets, "13.4", "arm64"); got == nil || got.Name != "cudart-llama-bin-win-cuda-13.4-arm64.zip" {
		t.Errorf("cudaVer=13.4/arm64 should match 13.4 arm64 asset, got %v", got)
	}
	// arch mismatch: the x64-only list has no arm64 12.4 runtime
	if got := pickCudartAssetFor(assets, "12.4", "arm64"); got != nil {
		t.Errorf("version 12.4 with arm64 arch should return nil, got %v", got)
	}
	if got := pickCudartAssetFor(assets, "11.8", "x64"); got != nil {
		t.Errorf("non-existent version 11.8 should return nil, got %v", got)
	}
	// case-insensitive: all-uppercase asset name also matches
	upper := []GitHubAsset{{Name: "CUDART-LLAMA-BIN-WIN-CUDA-12.4-X64.ZIP"}}
	if got := pickCudartAssetFor(upper, "12.4", "x64"); got == nil {
		t.Error("asset name should be case-insensitive")
	}
	// no cudart assets returns nil
	noCudart := []GitHubAsset{{Name: "llama-b9999-bin-win-cuda-12.4-x64.zip"}}
	if got := pickCudartAssetFor(noCudart, "12.4", "x64"); got != nil {
		t.Errorf("no cudart assets should return nil, got %v", got)
	}
	// empty version falls back to first cudart asset with matching arch
	if got := pickCudartAssetFor(assets, "", "x64"); got == nil || got.Name != "cudart-llama-bin-win-cuda-12.4-x64.zip" {
		t.Errorf("empty version should fall back to first x64 cudart asset, got %v", got)
	}
	if got := pickCudartAssetFor(assets, "", "arm64"); got == nil || got.Name != "cudart-llama-bin-win-cuda-13.4-arm64.zip" {
		t.Errorf("empty version with arm64 arch should fall back to arm64 cudart asset, got %v", got)
	}
	if got := pickCudartAssetFor(nil, "", "x64"); got != nil {
		t.Errorf("empty version with no cudart assets should return nil, got %v", got)
	}
}

// TestEnsureLlamaServerExecutable verifies the post-extraction belt-and-braces
// chmod: a non-executable llama-server under dir (root or one-level subdir, the
// layouts findLlamaBinInDir supports) is re-marked 0755, and a directory
// without llama-server is a harmless no-op. Windows has no exec bit, where the
// helper itself is a no-op, so the assertion is skipped there.
func TestEnsureLlamaServerExecutable(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("windows does not track unix exec permission bits")
	}

	// Root layout: 0644 → 0755
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "llama-server"), []byte("#!/bin/sh\n"), 0644); err != nil {
		t.Fatal(err)
	}
	ensureLlamaServerExecutable(root)
	fi, err := os.Stat(filepath.Join(root, "llama-server"))
	if err != nil {
		t.Fatal(err)
	}
	if got := fi.Mode().Perm(); got != 0755 {
		t.Errorf("root-layout llama-server mode = %v, want 0755", got)
	}

	// One-level subdir layout (extracted with a top-level folder)
	sub := t.TempDir()
	if err := os.MkdirAll(filepath.Join(sub, "llama-b9999-bin"), 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(sub, "llama-b9999-bin", "llama-server"), []byte("#!/bin/sh\n"), 0600); err != nil {
		t.Fatal(err)
	}
	ensureLlamaServerExecutable(sub)
	fi, err = os.Stat(filepath.Join(sub, "llama-b9999-bin", "llama-server"))
	if err != nil {
		t.Fatal(err)
	}
	if got := fi.Mode().Perm(); got != 0755 {
		t.Errorf("subdir llama-server mode = %v, want 0755", got)
	}

	// No binary present: no-op, must not panic or create anything
	ensureLlamaServerExecutable(t.TempDir())
}

// TestAppUserAgent verifies the outbound User-Agent carries the app name, the
// current version and the repository URL for attribution.
func TestAppUserAgent(t *testing.T) {
	ua := appUserAgent()
	if !strings.HasPrefix(ua, "MyLlama/"+currentVersion) {
		t.Errorf("User-Agent = %q, want prefix MyLlama/%s", ua, currentVersion)
	}
	if !strings.Contains(ua, "https://github.com/CodeNeow/MyLlama") {
		t.Errorf("User-Agent = %q, want repository URL", ua)
	}
}

// TestBuildDownloadRequest verifies download requests carry User-Agent and add a Range
// header for resume downloads.
func TestBuildDownloadRequest(t *testing.T) {
	req, err := buildDownloadRequest(context.Background(), "https://example.com/model.gguf", 0)
	if err != nil {
		t.Fatal(err)
	}
	if req.Header.Get("User-Agent") != appUserAgent() {
		t.Errorf("User-Agent = %q, want %q", req.Header.Get("User-Agent"), appUserAgent())
	}
	if req.Header.Get("Range") != "" {
		t.Errorf("no offset should not have Range header: %q", req.Header.Get("Range"))
	}

	req, err = buildDownloadRequest(context.Background(), "https://example.com/model.gguf", 1024)
	if err != nil {
		t.Fatal(err)
	}
	if req.Header.Get("Range") != "bytes=1024-" {
		t.Errorf("Range = %q, want bytes=1024-", req.Header.Get("Range"))
	}
}

// TestFetchLatestReleaseAt verifies fetching and parsing the latest release from an
// injected URL.
func TestFetchLatestReleaseAt(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.Write([]byte(`{"tag_name":"b3840","name":"Release","assets":[{"name":"a.zip","size":10,"browser_download_url":"https://x/a.zip"}]}`))
	}))
	defer srv.Close()

	rel, err := fetchLatestReleaseAt(context.Background(), srv.URL)
	if err != nil {
		t.Fatal(err)
	}
	if rel.TagName != "b3840" || len(rel.Assets) != 1 || rel.Assets[0].Name != "a.zip" {
		t.Errorf("release parse error: %+v", rel)
	}
}

// TestFetchLatestReleaseAtHTTPError verifies a non-200 response returns an error.
func TestFetchLatestReleaseAtHTTPError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
	}))
	defer srv.Close()

	if _, err := fetchLatestReleaseAt(context.Background(), srv.URL); err == nil {
		t.Error("500 response should return error")
	}
}

// TestFetchReleaseListAt verifies fetching and parsing a newest-first release
// list from an injected URL (the nightly-prerelease fallback data source).
func TestFetchReleaseListAt(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.Write([]byte(`[{"tag_name":"b10588","assets":[{"name":"nightly-tag.txt","size":9}]},{"tag_name":"b10587","assets":[{"name":"a.zip","size":10}]}]`))
	}))
	defer srv.Close()

	list, err := fetchReleaseListAt(context.Background(), srv.URL)
	if err != nil {
		t.Fatal(err)
	}
	if len(list) != 2 || list[0].TagName != "b10588" || list[1].Assets[0].Name != "a.zip" {
		t.Errorf("release list parse error: %+v", list)
	}
}

// TestFetchReleaseListAtHTTPError verifies a non-200 response returns an error.
func TestFetchReleaseListAtHTTPError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusForbidden)
	}))
	defer srv.Close()

	if _, err := fetchReleaseListAt(context.Background(), srv.URL); err == nil {
		t.Error("403 response should return error")
	}
}

// TestNewestReleaseWithAssets verifies the fallback release pick: the newest
// release carrying a platform main-asset wins; asset-less marker releases
// (llama.cpp's non-prerelease "latest" carries only nightly-tag.txt since
// 2026-08) are skipped; nil when no listed release has binaries.
func TestNewestReleaseWithAssets(t *testing.T) {
	platformKey := map[string]string{"windows": "win", "darwin": "macos", "linux": "linux"}[runtime.GOOS]
	archKey := map[string]string{"amd64": "x64", "arm64": "arm64"}[runtime.GOARCH]
	matchingName := fmt.Sprintf("llama-b9999-bin-%s-%s.zip", platformKey, archKey)
	asset := func(name string) []GitHubAsset {
		return []GitHubAsset{{Name: name, Size: 10, BrowserDownloadURL: "https://x/" + name}}
	}

	list := []GitHubRelease{
		{TagName: "b10588", Assets: asset("nightly-tag.txt")},            // marker, no binaries
		{TagName: "b10587", Assets: asset("llama-b10587-bin-other.zip")}, // wrong platform/arch
		{TagName: "b10586", Assets: asset(matchingName)},
	}
	rel := newestReleaseWithAssets(list)
	if rel == nil || rel.TagName != "b10586" {
		t.Errorf("newestReleaseWithAssets = %+v, want b10586", rel)
	}
	if newestReleaseWithAssets(nil) != nil {
		t.Error("empty list should return nil")
	}
	if newestReleaseWithAssets([]GitHubRelease{{TagName: "b1", Assets: asset("nightly-tag.txt")}}) != nil {
		t.Error("marker-only list should return nil")
	}
}

// TestDownloadTaskRenameFailure verifies that when the rename in the downloadTask
// completion branch fails, the task is marked as error (#10). Previously the os.Rename
// return value was ignored, causing silent done status with the file not in place;
// after the fix, renameFile (injectable package-level variable) failure immediately
// sets error. The test uses httptest to provide a fixed byte stream, injects renameFile
// failure, runs downloadTask end-to-end, and asserts task status and error message.
func TestDownloadTaskRenameFailure(t *testing.T) {
	// error string is returned by tr in the current language; pin zh so the
	// Chinese-prefix assertion below is language-independent.
	setLanguageForTest(t, "zh")
	withTempCwd(t)
	dlTasksMu.Lock()
	dlTasks = nil
	dlTaskCounter = 0
	dlTasksMu.Unlock()
	defer func() {
		dlTasksMu.Lock()
		dlTasks = nil
		dlTaskCounter = 0
		dlTasksMu.Unlock()
	}()

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte("fake model bytes"))
	}))
	defer srv.Close()

	// inject renameFile failure
	origRename := renameFile
	renameFile = func(oldpath, newpath string) error {
		return errors.New("mock rename fail")
	}
	defer func() { renameFile = origRename }()

	task := &DlTask{
		ID:       "dl-1",
		ModelID:  "author/model",
		FileName: "model.gguf",
		DestDir:  filepath.Join(effectiveModelDownloadDir(), "author"),
		URL:      srv.URL,
	}
	task.ctx, task.cancel = context.WithCancel(context.Background())
	defer task.cancel()

	downloadTask(task)

	dlTasksMu.Lock()
	status := task.Status
	errMsg := task.Error
	dlTasksMu.Unlock()

	if status != "error" {
		t.Errorf("status after rename failure = %q, want error", status)
	}
	if !strings.Contains(errMsg, "重命名失败") {
		t.Errorf("error message should contain 重命名失败: %q", errMsg)
	}
}

// TestMoveFileCrossDeviceFallbackCopy verifies that moveFile falls back to
// copy + delete source when renameFile returns the current platform's real
// cross-device error (Windows cross-drive ERROR_NOT_SAME_DEVICE=17 /
// Unix cross-mount EXDEV, constant crossDeviceRenameErr is platform-specific,
// wrapped in LinkError to simulate os.Rename's real error shape):
// asserts destination content matches source, source is deleted, destination
// retains source file permissions (Linux update exe depends on execute bit;
// Windows os.Stat always reports 0666, so assert destination mode matches
// source actual mode rather than hardcoded 0755). renameFile is a package-level
// injection point, restored by defer.
func TestMoveFileCrossDeviceFallbackCopy(t *testing.T) {
	src := filepath.Join(t.TempDir(), "src.bin")
	dst := filepath.Join(t.TempDir(), "dst.bin")
	payload := []byte("cross device payload")
	if err := os.WriteFile(src, payload, 0755); err != nil {
		t.Fatal(err)
	}
	srcFi, err := os.Stat(src)
	if err != nil {
		t.Fatal(err)
	}
	origMode := srcFi.Mode().Perm()

	origRename := renameFile
	renameFile = func(oldpath, newpath string) error {
		return &os.LinkError{Op: "rename", Old: oldpath, New: newpath, Err: crossDeviceRenameErr}
	}
	defer func() { renameFile = origRename }()

	if err := moveFile(src, dst); err != nil {
		t.Fatalf("cross-device fallback should succeed: %v", err)
	}
	got, err := os.ReadFile(dst)
	if err != nil {
		t.Fatalf("failed to read destination: %v", err)
	}
	if string(got) != string(payload) {
		t.Errorf("destination content = %q, want %q", got, payload)
	}
	if _, err := os.Stat(src); !errors.Is(err, os.ErrNotExist) {
		t.Errorf("source should be deleted, stat err = %v", err)
	}
	dstFi, err := os.Stat(dst)
	if err != nil {
		t.Fatal(err)
	}
	if dstFi.Mode().Perm() != origMode {
		t.Errorf("destination mode = %v, want source mode %v", dstFi.Mode().Perm(), origMode)
	}
}

// TestMoveFileNonCrossDeviceError verifies that when renameFile returns a non-cross-device
// error and the destination does not exist (simulating the TestDownloadTaskRenameFailure
// scenario), moveFile does not trigger copy fallback, does not mistakenly delete/move the
// source file, and returns the original error as-is.
func TestMoveFileNonCrossDeviceError(t *testing.T) {
	src := filepath.Join(t.TempDir(), "src.bin")
	dst := filepath.Join(t.TempDir(), "dst.bin")
	if err := os.WriteFile(src, []byte("payload"), 0644); err != nil {
		t.Fatal(err)
	}

	origRename := renameFile
	renameFile = func(oldpath, newpath string) error {
		return errors.New("mock rename fail")
	}
	defer func() { renameFile = origRename }()

	err := moveFile(src, dst)
	if err == nil {
		t.Fatal("non-EXDEV failure should return error")
	}
	if !strings.Contains(err.Error(), "mock rename fail") {
		t.Errorf("error should preserve original error message: %v", err)
	}
	if _, statErr := os.Stat(src); statErr != nil {
		t.Errorf("source file should not be moved or deleted on non-EXDEV failure: %v", statErr)
	}
	if _, statErr := os.Stat(dst); !errors.Is(statErr, os.ErrNotExist) {
		t.Errorf("destination file should not be created on non-EXDEV failure")
	}
}

// TestMoveFileExdevDoesNotDeleteExistingDest verifies cross-device detection takes
// priority over "delete old and retry": when the destination already contains an old
// file and the operation is cross-device, moveFile copies over instead of deleting
// the old file first, preventing data loss if the copy fails. renameFile is injected
// with the current platform's real cross-device error (crossDeviceRenameErr, wrapped
// in LinkError); asserts old file content is overwritten and source file is deleted.
func TestMoveFileExdevDoesNotDeleteExistingDest(t *testing.T) {
	src := filepath.Join(t.TempDir(), "src.bin")
	dst := filepath.Join(t.TempDir(), "dst.bin")
	if err := os.WriteFile(src, []byte("new content"), 0644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(dst, []byte("old content"), 0644); err != nil {
		t.Fatal(err)
	}

	origRename := renameFile
	renameFile = func(oldpath, newpath string) error {
		return &os.LinkError{Op: "rename", Old: oldpath, New: newpath, Err: crossDeviceRenameErr}
	}
	defer func() { renameFile = origRename }()

	if err := moveFile(src, dst); err != nil {
		t.Fatalf("cross-device fallback should succeed: %v", err)
	}
	got, err := os.ReadFile(dst)
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != "new content" {
		t.Errorf("destination content = %q, want overwritten with new content", got)
	}
	if _, err := os.Stat(src); !errors.Is(err, os.ErrNotExist) {
		t.Errorf("source should be deleted, stat err = %v", err)
	}
}

// TestDownloadTaskRangeIgnoredRestart is a #B3 regression test: when the server
// ignores the Range header (a resume request with offset returns 200 + full content),
// downloadTask must first truncate the .part file and re-download from 0, otherwise
// the full content would be repeatedly appended to the existing partial content,
// corrupting the file.
// The test constructs a Range-header scenario: first injects an existing .part file
// (offset>0) and sets Downloaded>0 to simulate interrupted resume; the httptest server
// returns 200 full body for Range requests (and also for non-Range requests, ensuring
// the reconnection after offset-zero can complete).
// Assertions: final file content equals full body (no duplicate concatenation); the
// server receives exactly one Range request (after offset-zero the reconnection does
// not use Range, avoiding infinite loops).
func TestDownloadTaskRangeIgnoredRestart(t *testing.T) {
	withTempCwd(t)
	dlTasksMu.Lock()
	dlTasks = nil
	dlTaskCounter = 0
	dlTasksMu.Unlock()
	defer func() {
		dlTasksMu.Lock()
		dlTasks = nil
		dlTaskCounter = 0
		dlTasksMu.Unlock()
	}()

	payload := []byte("0123456789abcdef") // 16 bytes full content
	rangeReqCount := 0
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Range") != "" {
			rangeReqCount++
		}
		w.Header().Set("Content-Length", strconv.Itoa(len(payload)))
		w.Write(payload)
	}))
	defer srv.Close()

	destDir := filepath.Join(effectiveModelDownloadDir(), "author")
	if err := os.MkdirAll(destDir, 0755); err != nil {
		t.Fatal(err)
	}

	// pre-populate existing .part file (simulating interrupted resume state) and task progress
	tmpPath := filepath.Join(destDir, "model.gguf.part")
	if err := os.WriteFile(tmpPath, []byte("partial"), 0644); err != nil {
		t.Fatal(err)
	}

	task := &DlTask{
		ID:         "dl-1",
		ModelID:    "author/model",
		FileName:   "model.gguf",
		DestDir:    destDir,
		URL:        srv.URL,
		Status:     "queued",
		Downloaded: int64(len("partial")),
		Total:      int64(len(payload) + len("partial")),
	}
	task.ctx, task.cancel = context.WithCancel(context.Background())
	defer task.cancel()

	downloadTask(task)

	dlTasksMu.Lock()
	status := task.Status
	dlTasksMu.Unlock()
	if status != "done" {
		t.Fatalf("task status = %q, want done (server ignoring Range must truncate and redownload)", status)
	}

	got, err := os.ReadFile(filepath.Join(destDir, "model.gguf"))
	if err != nil {
		t.Fatalf("downloaded file not written to disk: %v", err)
	}
	// file content must equal the full body and not contain the old partial
	// bytes — appending the full body onto the old partial corrupts the file
	if string(got) != string(payload) {
		t.Errorf("file content = %q, want full %q (must not append the full body onto the old .part)", got, payload)
	}

	// after offset resets to zero the reconnect carries no Range header; the
	// server must not see another Range request
	if rangeReqCount != 1 {
		t.Errorf("Range request count = %d, want 1 (truncate-reconnect happens once; offset=0 sends no Range header)", rangeReqCount)
	}
}

// TestDownloadTaskStalledStreamReconnects verifies that when the server stops
// sending mid-stream (a half-open connection / proxy stall, which an httptest
// handler cannot simulate — Go's HTTP server closes the connection after a
// partial write, while a real CDN keeps the socket open), the read loop's idle
// timeout fires and the download reconnects with a Range header at the current
// .part size, completing with no lost or duplicated bytes. The stall is
// simulated with a raw TCP server so the client read blocks exactly like it
// does against a real stalled CDN.
func TestDownloadTaskStalledStreamReconnects(t *testing.T) {
	withTempCwd(t)
	dlTasksMu.Lock()
	dlTasks = nil
	dlTaskCounter = 0
	dlTasksMu.Unlock()
	defer func() {
		dlTasksMu.Lock()
		dlTasks = nil
		dlTaskCounter = 0
		dlTasksMu.Unlock()
	}()

	oldTimeout := idleReadTimeout
	idleReadTimeout = 150 * time.Millisecond
	defer func() { idleReadTimeout = oldTimeout }()

	payload := []byte("0123456789abcdef") // 16 bytes full content
	var mu sync.Mutex
	var reqCount int
	var reqRanges []string

	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer ln.Close()

	go func() {
		for {
			conn, err := ln.Accept()
			if err != nil {
				return
			}
			go func(c net.Conn) {
				defer c.Close()
				// Read the request head (headers end with a blank line).
				head := make([]byte, 4096)
				n := 0
				for {
					buf := make([]byte, 1024)
					m, err := c.Read(buf)
					if err != nil {
						return
					}
					n += m
					head = append(head[:n], buf[:m]...)
					if bytes.Contains(head, []byte("\r\n\r\n")) {
						break
					}
					if n >= len(head) {
						return
					}
				}
				var rangeHdr string
				for _, line := range strings.Split(string(head), "\r\n") {
					if strings.HasPrefix(line, "Range: ") {
						rangeHdr = strings.TrimPrefix(line, "Range: ")
					}
				}
				mu.Lock()
				reqCount++
				reqRanges = append(reqRanges, rangeHdr)
				reqNum := reqCount
				mu.Unlock()

				if reqNum == 1 {
					// First request: write half the payload, then stall without
					// EOF — the connection stays open and the client read blocks
					// (the half-open scenario behind stalled downloads).
					resp := "HTTP/1.1 200 OK\r\nContent-Length: 16\r\n\r\n"
					c.Write([]byte(resp))
					c.Write(payload[:8])
					// Keep the connection open and silent until the client
					// closes it after its idle timeout.
					<-make(chan struct{}) // blocked forever; connection closed by the client
					return
				}
				// Second request: the reconnect must carry a Range header at
				// exactly the stalled offset; answer 206 with the remainder.
				mu.Lock()
				want := "bytes=8-"
				mu.Unlock()
				if rangeHdr != want {
					t.Errorf("reconnect Range header = %q, want %q", rangeHdr, want)
				}
				resp := "HTTP/1.1 206 Partial Content\r\nContent-Range: bytes 8-15/16\r\nContent-Length: 8\r\n\r\n"
				c.Write([]byte(resp))
				c.Write(payload[8:])
			}(conn)
		}
	}()

	destDir := filepath.Join(effectiveModelDownloadDir(), "author")
	if err := os.MkdirAll(destDir, 0755); err != nil {
		t.Fatal(err)
	}

	task := &DlTask{
		ID:       "dl-1",
		ModelID:  "author/model",
		FileName: "model.gguf",
		DestDir:  destDir,
		URL:      "http://" + ln.Addr().String(),
		Status:   "queued",
	}
	task.ctx, task.cancel = context.WithCancel(context.Background())
	defer task.cancel()

	downloadTask(task)

	dlTasksMu.Lock()
	status := task.Status
	errMsg := task.Error
	dlTasksMu.Unlock()
	if status != "done" {
		t.Fatalf("task status = %q, want done (idle timeout must reconnect and finish the download); error = %q", status, errMsg)
	}

	got, err := os.ReadFile(filepath.Join(destDir, "model.gguf"))
	if err != nil {
		t.Fatalf("downloaded file not written to disk: %v", err)
	}
	// the resumed append must not duplicate the first half nor lose the second
	if string(got) != string(payload) {
		t.Errorf("file content = %q, want full %q (stall-reconnect must resume exactly at the .part size)", got, payload)
	}

	mu.Lock()
	count := reqCount
	ranges := strings.Join(reqRanges, "; ")
	mu.Unlock()
	if count != 2 {
		t.Errorf("request count = %d, want 2 (initial stream + one Range reconnect); ranges: %s", count, ranges)
	}
}

// ─── downloadWithResume robustness (llama.cpp download path) ──────────────

// resetDownloadForTest clears llama.cpp download globals so a directly-invoked
// downloadWithResume starts from a clean, unpaused state (saveDownloadState
// snapshots/restores the rest). Removes leftover llamacpp-download-* temp
// files so the file-size polling helper in the pause/resume test cannot
// mistake a previous test's temp file for its own.
func resetDownloadForTest(t *testing.T) {
	t.Helper()
	saveDownloadState(t)
	downloadMu.Lock()
	downloadState.Paused = false
	downloadMu.Unlock()
	matches, _ := filepath.Glob(filepath.Join(os.TempDir(), "llamacpp-download-*"))
	for _, m := range matches {
		os.Remove(m)
	}
}

// waitLlamaTmpSize polls the system temp dir for the llamacpp-download-* file
// created by downloadWithResume and returns once some temp file reaches want
// bytes (deterministic condition wait: the writer goroutine is guaranteed to
// have flushed that many bytes once observed; the short poll interval is not
// used to guess timing).
func waitLlamaTmpSize(t *testing.T, want int64) string {
	t.Helper()
	deadline := time.Now().Add(10 * time.Second)
	for time.Now().Before(deadline) {
		matches, _ := filepath.Glob(filepath.Join(os.TempDir(), "llamacpp-download-*"))
		for _, m := range matches {
			if fi, err := os.Stat(m); err == nil && fi.Size() >= want {
				return m
			}
		}
		time.Sleep(2 * time.Millisecond)
	}
	t.Fatalf("llamacpp temp file did not reach %d bytes within timeout", want)
	return ""
}

// TestDownloadWithResumeTruncatedEOFAutoResumes verifies that a clean io.EOF
// before the declared totalSize (a truncated body, e.g. a proxy cutting the
// stream at a chunk boundary) is not treated as success: the download
// auto-resumes with a Range request from the bytes already on disk and
// finishes the file. Previously EOF returned the partial file as success,
// producing a corrupt zip that only failed later at extraction.
func TestDownloadWithResumeTruncatedEOFAutoResumes(t *testing.T) {
	resetDownloadForTest(t)

	payload := []byte("0123456789abcdef") // 16 bytes total
	var mu sync.Mutex
	rangeReqs := 0
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if rng := r.Header.Get("Range"); rng != "" {
			if !strings.Contains(rng, "bytes=8-") {
				t.Errorf("resume Range header = %q, want bytes=8- (resume from bytes already on disk)", rng)
			}
			mu.Lock()
			rangeReqs++
			mu.Unlock()
			w.Header().Set("Content-Range", "bytes 8-15/16")
			w.Header().Set("Content-Length", "8")
			w.WriteHeader(http.StatusPartialContent)
			w.Write(payload[8:])
			return
		}
		// First request: half the payload, chunked (no Content-Length) →
		// the handler returns normally and the client sees a clean EOF.
		w.Write(payload[:8])
	}))
	defer srv.Close()

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	path, err := downloadWithResume(ctx, srv.URL+"/asset.zip", int64(len(payload)), 0)
	if err != nil {
		t.Fatalf("downloadWithResume = %v, want nil (truncated EOF must auto-resume and finish)", err)
	}
	defer os.Remove(path)

	got, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != string(payload) {
		t.Errorf("file content = %q, want full payload %q (auto-resume must complete the truncated body)", got, payload)
	}
	mu.Lock()
	defer mu.Unlock()
	if rangeReqs != 1 {
		t.Errorf("Range request count = %d, want 1 (single auto-resume after the truncated first response)", rangeReqs)
	}
}

// TestDownloadWithResumePersistentTruncationErrors verifies that a server
// which keeps truncating the body (every response carries only half of the
// declared size) never yields success: after 3 auto-resume attempts the
// download fails with a clear "incomplete download" error instead of
// returning a partial file that would later fail extraction as a corrupt zip.
func TestDownloadWithResumePersistentTruncationErrors(t *testing.T) {
	resetDownloadForTest(t)
	setLanguageForTest(t, "en")

	payload := []byte("0123456789abcdef")
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// Every request (Range or not) answers 200 with only half the
		// payload, chunked → clean EOF far below the declared size.
		w.Write(payload[:8])
	}))
	defer srv.Close()

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	path, err := downloadWithResume(ctx, srv.URL+"/asset.zip", int64(len(payload)), 0)
	defer os.Remove(path)
	if err == nil {
		t.Fatal("persistently truncated download must return an error, got nil")
	}
	if !strings.Contains(err.Error(), "incomplete download") {
		t.Errorf("error = %q, want it to contain %q", err.Error(), "incomplete download")
	}
}

// TestDownloadWithResumeRangeIgnoredRestartsClean verifies that when a resume
// request (offset>0 → Range header) hits a server that ignores Range and
// answers 200 with the full body, the temp file is truncated and the body is
// written from zero; naively appending would produce "partial prefix + full
// body" corruption. Setup: the first request truncates halfway (clean EOF,
// triggering the auto-resume), the second request ignores the Range header
// and returns 200 with the full body.
func TestDownloadWithResumeRangeIgnoredRestartsClean(t *testing.T) {
	resetDownloadForTest(t)

	payload := []byte("0123456789abcdef")
	var mu sync.Mutex
	reqs := 0
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		reqs++
		n := reqs
		mu.Unlock()
		if n == 1 {
			// First request: half then clean EOF (chunked) → auto-resume triggers.
			w.Write(payload[:8])
			return
		}
		// Subsequent requests ignore Range and return the full body with 200.
		w.Header().Set("Content-Length", strconv.Itoa(len(payload)))
		w.Write(payload)
	}))
	defer srv.Close()

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	path, err := downloadWithResume(ctx, srv.URL+"/asset.zip", int64(len(payload)), 0)
	if err != nil {
		t.Fatalf("downloadWithResume = %v, want nil (200-on-Range must restart clean and finish)", err)
	}
	defer os.Remove(path)

	got, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != string(payload) {
		t.Errorf("file content = %q (%d bytes), want exactly the full payload %q (no partial prefix duplicated)", got, len(got), payload)
	}
}

// TestDownloadWithResumePauseResumeReopensFile verifies the pause → resume
// path reopens the temp file before writing: the pause branch closes the
// handle, and the pre-fix outer loop never reopened it, so every post-resume
// Write failed with "file already closed" and the download died. Fully
// deterministic orchestration (no sleep-based guessing):
//   - the first connection delivers bytes 0..7, then blocks on gate1;
//   - the test observes 8 bytes on disk, then flips Paused=true (mimicking
//     PauseLlamaCppDownload, including a fresh resume channel);
//   - gate1 releases bytes 8..11 only; since the stream has not ended (the
//     handler still blocks on gate2), a (n>0, io.EOF) merged read cannot
//     escape the pause check — the read loop is guaranteed to take the pause
//     branch and block in waitForResume once 12 bytes are on disk;
//   - the test observes 12 bytes, then mimics ResumeLlamaCppDownload
//     (Paused=false + buffered resume signal);
//   - the resumed pass reopens the temp file (the fix), requests
//     Range bytes=12- and writes the remaining bytes through the reopened
//     handle — the pre-fix code fails here with "file already closed".
func TestDownloadWithResumePauseResumeReopensFile(t *testing.T) {
	resetDownloadForTest(t)

	payload := []byte("0123456789abcdef") // 16 bytes total
	gate1 := make(chan struct{})
	gate2 := make(chan struct{})

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if rng := r.Header.Get("Range"); rng != "" {
			// Resumed pass: serve exactly the requested remainder with 206.
			off, err := strconv.ParseInt(strings.TrimSuffix(strings.TrimPrefix(rng, "bytes="), "-"), 10, 64)
			if err != nil || off < 0 || off > int64(len(payload)) {
				t.Errorf("unexpected Range header %q", rng)
				w.WriteHeader(http.StatusRequestedRangeNotSatisfiable)
				return
			}
			w.Header().Set("Content-Range", fmt.Sprintf("bytes %d-%d/%d", off, len(payload)-1, len(payload)))
			w.Header().Set("Content-Length", strconv.Itoa(len(payload)-int(off)))
			w.WriteHeader(http.StatusPartialContent)
			if off < int64(len(payload)) {
				w.Write(payload[off:])
			}
			return
		}
		// First connection: staged delivery so the pause lands mid-body.
		w.Write(payload[:8])
		if f, ok := w.(http.Flusher); ok {
			f.Flush()
		}
		<-gate1
		w.Write(payload[8:12])
		if f, ok := w.(http.Flusher); ok {
			f.Flush()
		}
		<-gate2 // keep the stream open: no EOF can merge with the last chunk
		w.Write(payload[12:])
	}))
	defer srv.Close()
	// Registered after srv.Close, so LIFO runs it FIRST: releasing gate2 lets
	// the parked first-connection handler return before Close waits on it.
	defer func() { close(gate2) }()

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	type result struct {
		path string
		err  error
	}
	resCh := make(chan result, 1)
	go func() {
		p, err := downloadWithResume(ctx, srv.URL+"/asset.zip", int64(len(payload)), 0)
		resCh <- result{p, err}
	}()

	// Bytes 0..7 are on disk; the download goroutine is now either at the
	// read-loop top or blocked reading — both lead into the pause branch
	// once Paused=true is set and gate1 releases more (non-terminal) bytes.
	tmpPath := waitLlamaTmpSize(t, 8)
	downloadMu.Lock()
	downloadState.Status = "paused"
	downloadState.Paused = true
	downloadResumeCh = make(chan struct{}, 1) // fresh channel, as PauseLlamaCppDownload does
	downloadMu.Unlock()

	close(gate1) // release bytes 8..11 (stream stays open on gate2)

	// 12 bytes on disk: the read loop has consumed the released chunk and,
	// with the stream not ended, must now be blocked in the pause branch's
	// waitForResume (the pause check happens before the next read).
	waitLlamaTmpSize(t, 12)

	// Mimic ResumeLlamaCppDownload: Paused=false first, then the buffered
	// resume signal — the resumed pass reads Paused after waitForResume
	// returns, so ordering guarantees it proceeds instead of re-pausing.
	downloadMu.Lock()
	downloadState.Paused = false
	if downloadState.Status == "paused" {
		downloadState.Status = "downloading"
	}
	select {
	case downloadResumeCh <- struct{}{}:
	default:
	}
	downloadMu.Unlock()

	var res result
	select {
	case res = <-resCh:
	case <-time.After(30 * time.Second):
		cancel()
		t.Fatal("downloadWithResume did not finish after resume (deadlock or repeated re-pause)")
	}
	if res.err != nil {
		t.Fatalf("downloadWithResume after pause→resume = %v, want nil (pre-fix code fails writing to the closed pre-pause handle: file already closed)", res.err)
	}
	got, err := os.ReadFile(res.path)
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != string(payload) {
		t.Errorf("file content = %q, want full payload %q", got, payload)
	}
	if res.path != tmpPath {
		t.Logf("note: temp file path changed across pause (%q → %q)", tmpPath, res.path)
	}
	os.Remove(res.path)
}

// TestDownloadLlamaCppMultiAssetStatusTransitions verifies that in a
// two-asset download (Windows CUDA main zip + cudart runtime zip) the status
// re-enters "downloading" for the second asset instead of staying on the
// stale "extracting" left behind by the first asset's extraction: an
// edge-triggered sampler records every distinct downloadState.Status in
// order, and the recorded sequence must contain "extracting" (asset 1
// extraction) followed by "downloading" again (asset 2 download), end at
// "done", with both assets extracted on disk. The main zip is padded with
// many entries and the asset handlers add a small fixed delay so each phase
// is comfortably longer than the sampler's poll period. Windows-only: the
// cudart co-download path is Windows-exclusive.
func TestDownloadLlamaCppMultiAssetStatusTransitions(t *testing.T) {
	if runtime.GOOS != "windows" {
		t.Skip("multi-asset cudart co-download is Windows-only; skipped on non-Windows")
	}
	resetDownloadForTest(t)
	saveServerState(t)
	withTempCwd(t)

	// Pad the main zip so its extraction lasts far longer than one sampler
	// poll period, guaranteeing the "extracting" edge is always captured.
	mainFiles := map[string]string{llamaServerBinName(): "stub"}
	for i := 0; i < 2000; i++ {
		mainFiles[fmt.Sprintf("pad/file-%04d.bin", i)] = "padding"
	}
	mainZip := makeZip(t, mainFiles)
	cudartZip := makeZip(t, map[string]string{"cublas64_12.dll": "stub"})

	ver := "12.4"
	mainName := fmt.Sprintf("llama-b9999-bin-win-cuda-%s-x64.zip", ver)
	cudartName := fmt.Sprintf("cudart-llama-bin-win-cuda-%s-x64.zip", ver)

	var srv *httptest.Server
	srv = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/release":
			w.Header().Set("Content-Type", "application/json")
			fmt.Fprintf(w, `{"tag_name":"b9999","assets":[{"name":%q,"size":%d,"browser_download_url":%q},{"name":%q,"size":%d,"browser_download_url":%q}]}`,
				cudartName, len(cudartZip), srv.URL+"/cudart.zip",
				mainName, len(mainZip), srv.URL+"/main.zip")
		case "/main.zip":
			time.Sleep(20 * time.Millisecond) // widen the "downloading" phase
			w.Write(mainZip)
		case "/cudart.zip":
			time.Sleep(20 * time.Millisecond) // widen the second "downloading" phase
			w.Write(cudartZip)
		default:
			http.NotFound(w, r)
		}
	}))
	defer srv.Close()

	origAPI := githubReleasesAPI
	githubReleasesAPI = srv.URL + "/release"
	defer func() { githubReleasesAPI = origAPI }()

	// Stub the GPU probe: a pre-Blackwell host (no CUDA floor) keeps asset
	// selection deterministic regardless of the host GPU.
	origCC := probeGPUComputeCap
	probeGPUComputeCap = func() string { return "8.9" }
	defer func() { probeGPUComputeCap = origCC }()

	// Edge-triggered status sampler: busy-polls downloadState.Status and
	// records each transition in order (denser than any ticker, so no
	// phase wider than a poll period can be missed).
	stopSample := make(chan struct{})
	sampled := make(chan []string, 1)
	go func() {
		var seq []string
		last := ""
		for {
			select {
			case <-stopSample:
				sampled <- seq
				return
			default:
			}
			downloadMu.Lock()
			s := downloadState.Status
			downloadMu.Unlock()
			if s != last {
				seq = append(seq, s)
				last = s
			}
			runtime.Gosched()
		}
	}()

	downloadLlamaCpp()
	close(stopSample)
	seq := <-sampled

	downloadMu.Lock()
	status := downloadState.Status
	errMsg := downloadState.Error
	downloadMu.Unlock()

	if status != "done" {
		t.Fatalf("download status = %q, want done (error: %s)", status, errMsg)
	}

	// The defect: asset 2 downloaded under the stale "extracting" label, so
	// no "downloading" ever followed the first "extracting".
	extractIdx := -1
	downloadAfterExtract := -1
	for i, s := range seq {
		if s == "extracting" && extractIdx == -1 {
			extractIdx = i
		}
		if extractIdx != -1 && s == "downloading" && downloadAfterExtract == -1 {
			downloadAfterExtract = i
		}
	}
	if extractIdx == -1 {
		t.Fatalf("sampled status sequence %v lacks extracting (asset 1 extraction phase)", seq)
	}
	if downloadAfterExtract == -1 {
		t.Fatalf("sampled status sequence %v lacks a downloading phase after extracting (asset 2 must re-enter downloading, not stay on the stale extracting label)", seq)
	}

	// Both assets must be extracted on disk.
	if _, err := os.Stat(filepath.Join("llama-cpp", llamaServerBinName())); err != nil {
		t.Errorf("main program artifact missing after extraction: %v", err)
	}
	if _, err := os.Stat(filepath.Join("llama-cpp", "cublas64_12.dll")); err != nil {
		t.Errorf("cudart runtime artifact missing after extraction: %v", err)
	}
}
