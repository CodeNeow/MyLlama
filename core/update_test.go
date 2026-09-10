package core

import (
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"testing"
	"time"
)

// saveUpdateState snapshots update-related globals (API URL, download state, executable-path
// injection) and restores them after the test, preventing cross-test pollution.
func saveUpdateState(t *testing.T) (origAPI string, origState UpdateDownloadState, origExe func() (string, error)) {
	t.Helper()
	origAPI = updateRepoAPI
	updateDownloadMu.Lock()
	origState = *updateDownloadState
	updateDownloadMu.Unlock()
	origExe = updateExePath
	t.Cleanup(func() {
		updateRepoAPI = origAPI
		updateDownloadMu.Lock()
		*updateDownloadState = origState
		updateDownloadMu.Unlock()
		updateExePath = origExe
	})
	return
}

// TestCompareVersions verifies version comparison strips the v prefix and compares
// segment-by-segment; when segment counts differ, missing segments are padded with 0
// (1.0 < 1.0.1).
func TestCompareVersions(t *testing.T) {
	cases := []struct {
		a, b string
		want int
	}{
		{"v0.1.0", "v0.1.1", -1},
		{"v0.1.1", "v0.1.1", 0},
		{"v1.0.0", "v0.9.9", 1},
		{"0.1.0", "v0.1.0", 0},
		{"V0.2.0", "v0.10.0", -1},
		{"v1.0", "v1.0.1", -1},
		{"v2.0.0", "v1.9.9", 1},
		{"v0.1.0", "v0.1.0-alpha", 0}, // non-numeric segments treated as 0
	}
	for _, c := range cases {
		if got := compareVersions(c.a, c.b); got != c.want {
			t.Errorf("compareVersions(%q, %q) = %d, want %d", c.a, c.b, got, c.want)
		}
	}
}

// TestPickUpdateAsset verifies update asset selection by install type, compatible with
// both old and new naming conventions: setup picks installers (installer / setup names),
// portable picks portable builds (portable name or old names like llama-gui.exe that are
// not installers). Since portable builds are no longer published, a release may ship only
// the setup installer; in that case portable falls back to the installer asset.
func TestPickUpdateAsset(t *testing.T) {
	// setup: old installer name matches
	if got := pickUpdateAsset([]GitHubAsset{{Name: "llama-gui-amd64-installer.exe"}}, installKindSetup); got == nil || got.Name != "llama-gui-amd64-installer.exe" {
		t.Errorf("setup should pick llama-gui-amd64-installer.exe, got %v", got)
	}
	// setup: new setup name matches (name does not contain "installer" but still matches)
	if got := pickUpdateAsset([]GitHubAsset{{Name: "MyLlama-setup-v0.2.0-windows-amd64.exe"}}, installKindSetup); got == nil || got.Name != "MyLlama-setup-v0.2.0-windows-amd64.exe" {
		t.Errorf("setup should pick MyLlama-setup-v0.2.0-windows-amd64.exe, got %v", got)
	}
	// setup: only portable asset available → nil (must not pick portable by mistake)
	if got := pickUpdateAsset([]GitHubAsset{{Name: "MyLlama-portable-v0.2.0-amd64.exe"}}, installKindSetup); got != nil {
		t.Errorf("setup with only portable asset should return nil, got %v", got)
	}

	// portable: old llama-gui.exe matches (skips installer)
	assets := []GitHubAsset{
		{Name: "llama-gui-amd64-installer.exe"},
		{Name: "llama-gui.exe", Size: 10516480},
	}
	if got := pickUpdateAsset(assets, installKindPortable); got == nil || got.Name != "llama-gui.exe" {
		t.Errorf("portable should pick llama-gui.exe, got %v", got)
	}
	// portable: new portable name matches
	if got := pickUpdateAsset([]GitHubAsset{{Name: "MyLlama-portable-v0.2.0-amd64.exe"}}, installKindPortable); got == nil || got.Name != "MyLlama-portable-v0.2.0-amd64.exe" {
		t.Errorf("portable should pick MyLlama-portable-v0.2.0-amd64.exe, got %v", got)
	}
	// portable: only a setup installer is published (portable builds retired) →
	// fall back to the installer so existing portable installs keep updating
	if got := pickUpdateAsset([]GitHubAsset{{Name: "MyLlama-setup-v0.2.0-windows-amd64.exe"}}, installKindPortable); got == nil || got.Name != "MyLlama-setup-v0.2.0-windows-amd64.exe" {
		t.Errorf("portable with only a setup installer should fall back to it, got %v", got)
	}
	// portable: only installer assets (setup + old installer) → fall back to the first
	// installer seen (replaces the old nil expectation: portable builds are no longer
	// published, so portable installs must update via the setup installer)
	if got := pickUpdateAsset([]GitHubAsset{{Name: "MyLlama-setup-v0.2.0-windows-amd64.exe"}, {Name: "llama-gui-amd64-installer.exe"}}, installKindPortable); got == nil || got.Name != "MyLlama-setup-v0.2.0-windows-amd64.exe" {
		t.Errorf("portable with only installer assets should fall back to the first installer, got %v", got)
	}

	// empty asset list returns nil for both kinds
	if got := pickUpdateAsset(nil, installKindSetup); got != nil {
		t.Errorf("nil assets (setup) should return nil, got %v", got)
	}
	if got := pickUpdateAsset(nil, installKindPortable); got != nil {
		t.Errorf("nil assets (portable) should return nil, got %v", got)
	}
}

// TestDetectInstallKind verifies install-type detection: executable directory containing
// uninstall.exe is classified as setup (NSIS install); absence means portable; when
// updateExePath returns an error, portable is the fallback.
// updateExePath injection value is saved/restored throughout.
func TestDetectInstallKind(t *testing.T) {
	origExe := updateExePath
	t.Cleanup(func() { updateExePath = origExe })

	dir := t.TempDir()
	// no uninstall.exe → portable (green portable build)
	updateExePath = func() (string, error) {
		return filepath.Join(dir, "MyLlama.exe"), nil
	}
	if got := detectInstallKind(); got != installKindPortable {
		t.Errorf("no uninstall.exe should detect %q, got %q", installKindPortable, got)
	}

	// has uninstall.exe → setup (NSIS install)
	if err := os.WriteFile(filepath.Join(dir, "uninstall.exe"), []byte("uninstaller"), 0644); err != nil {
		t.Fatal(err)
	}
	if got := detectInstallKind(); got != installKindSetup {
		t.Errorf("with uninstall.exe should detect %q, got %q", installKindSetup, got)
	}

	// updateExePath returns error → fallback to portable
	updateExePath = func() (string, error) {
		return "", errors.New("no executable path")
	}
	if got := detectInstallKind(); got != installKindPortable {
		t.Errorf("updateExePath error should fall back to %q, got %q", installKindPortable, got)
	}
}

// TestPickUpdateAssetAndroid verifies the Android branch of asset selection:
// the release's android .apk artifact is picked (arm64 preferred when several
// ABIs are attached), desktop exe assets are never selected for the android
// kind, and an empty asset list yields nil.
func TestPickUpdateAssetAndroid(t *testing.T) {
	assets := []GitHubAsset{
		{Name: "MyLlama-setup-v0.4.0-windows-amd64.exe"},
		{Name: "MyLlama-v0.4.0-android-armv7a.apk"},
		{Name: "MyLlama-v0.4.0-android-arm64.apk"},
	}
	got := pickUpdateAsset(assets, installKindAndroid)
	if got == nil || got.Name != "MyLlama-v0.4.0-android-arm64.apk" {
		t.Errorf("android pick = %+v, want the arm64 apk", got)
	}

	if got := pickUpdateAsset([]GitHubAsset{{Name: "MyLlama-setup-v0.4.0-windows-amd64.exe"}}, installKindAndroid); got != nil {
		t.Errorf("android pick with exe-only assets = %+v, want nil", got)
	}

	if got := pickUpdateAsset([]GitHubAsset{{Name: "MyLlama-v0.4.0-android-arm64.apk"}}, installKindAndroid); got == nil {
		t.Error("android pick with a single apk = nil, want the apk")
	}

	if got := pickUpdateAsset(nil, installKindAndroid); got != nil {
		t.Errorf("android pick with no assets = %+v, want nil", got)
	}
}

// TestDetectInstallKindAndroid verifies the Android build always reports the
// apk install kind without touching the exe filesystem probe (the runtime
// lives on the read-only APK volume and never has an uninstall.exe sibling).
func TestDetectInstallKindAndroid(t *testing.T) {
	withPlatformGOOS(t, "android")
	if got := detectInstallKind(); got != installKindAndroid {
		t.Errorf("detectInstallKind on android = %q, want %q", got, installKindAndroid)
	}
}

// TestInstallUpdateNowAndroidGuard verifies the desktop installer-launch flow
// is rejected on Android: the APK install is triggered by the frontend through
// the Java bridge (os/exec is unusable in the app sandbox), never by Go.
func TestInstallUpdateNowAndroidGuard(t *testing.T) {
	withPlatformGOOS(t, "android")
	updateDownloadMu.Lock()
	updateDownloadState.Status = "done"
	updateDownloadState.Installer = true
	updateDownloadState.FilePath = "MyLlama-android-v9.9.9.apk"
	updateDownloadMu.Unlock()
	if err := installUpdateNow(func() {}); err == nil {
		t.Error("installUpdateNow must be rejected on android, got nil error")
	}
}

// TestCheckForUpdateNewer verifies hasUpdate is true when the remote version is newer
// than the current version, carrying the version number and release notes.
// Runs on the Windows branch: the update check is Windows-only (see the gate in
// CheckForUpdateAt).
func TestCheckForUpdateNewer(t *testing.T) {
	withPlatformGOOS(t, "windows")
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.Write([]byte(`{"tag_name":"v9.9.9","name":"Release","body":"new feature","published_at":"2026-08-10T00:00:00Z","assets":[]}`))
	}))
	defer srv.Close()

	res, err := CheckForUpdateAt(srv.URL)
	if err != nil {
		t.Fatal(err)
	}
	if !res.HasUpdate {
		t.Error("v9.9.9 > current version, hasUpdate should be true")
	}
	if res.Version != "v9.9.9" || !strings.Contains(res.Notes, "new feature") {
		t.Errorf("version info wrong: %+v", res)
	}
}

// TestCheckForUpdateSame verifies hasUpdate is false when the remote version matches
// the current version.
// currentVersion comes from the core/VERSION embedded file (overridden by CI at tag release);
// the assertion only checks the format (vX.Y.Z) without binding to a specific version,
// avoiding test churn on every release.
// Runs on the Windows branch: the update check is Windows-only (see the gate in
// CheckForUpdateAt).
func TestCheckForUpdateSame(t *testing.T) {
	withPlatformGOOS(t, "windows")
	if !regexp.MustCompile(`^v\d+\.\d+\.\d+$`).MatchString(currentVersion) {
		t.Fatalf("currentVersion should match vX.Y.Z, got %q", currentVersion)
	}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.Write([]byte(`{"tag_name":"` + currentVersion + `","name":"Release","assets":[]}`))
	}))
	defer srv.Close()

	res, err := CheckForUpdateAt(srv.URL)
	if err != nil {
		t.Fatal(err)
	}
	if res.HasUpdate {
		t.Error("hasUpdate should be false when versions match")
	}
}

// TestCheckForUpdateNonWindowsGate verifies the update check short-circuits to a
// "no update" result off-Windows without any network access: upstream releases ship
// Windows artifacts only (.exe assets / NSIS installer), so there is nothing to
// update to. The release URL is deliberately unreachable — if the gate were
// missing, the fetch would fail and the test would error out.
func TestCheckForUpdateNonWindowsGate(t *testing.T) {
	for _, goos := range []string{"linux", "darwin"} {
		t.Run(goos, func(t *testing.T) {
			withPlatformGOOS(t, goos)
			res, err := CheckForUpdateAt("http://127.0.0.1:1/unreachable")
			if err != nil {
				t.Fatalf("non-Windows check must not touch the network, got error: %v", err)
			}
			if res.HasUpdate {
				t.Errorf("non-Windows check on %s must report no update", goos)
			}
			if res.Version != currentVersion {
				t.Errorf("non-Windows check version = %q, want current %q", res.Version, currentVersion)
			}
		})
	}
}

// TestStartUpdateDownloadRejectsNotNewer verifies StartUpdateDownload returns an error
// for a version that is not newer than the current version, without starting the download.
func TestStartUpdateDownloadRejectsNotNewer(t *testing.T) {
	app := &App{}
	if err := app.StartUpdateDownload(currentVersion); err == nil {
		t.Error("same version as current should return error")
	}
}

// TestDownloadUpdateRelease verifies the portable update download end-to-end: fetch from
// injected release API → pick portable asset by type → download to the executable's
// directory → status becomes done and the file exists, with a version-and-type-prefixed
// filename (MyLlama-portable-v<tag>.exe).
func TestDownloadUpdateRelease(t *testing.T) {
	withTempCwd(t)
	saveUpdateState(t)
	// download landing directory uses temp dir to simulate "same directory as executable" (no uninstall.exe → portable)
	exeDir := t.TempDir()
	updateExePath = func() (string, error) {
		return filepath.Join(exeDir, "MyLlama.exe"), nil
	}

	payload := []byte("MZ fake exe payload")
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/dl/llama-gui.exe" {
			w.Header().Set("Content-Length", strconv.Itoa(len(payload)))
			w.Write(payload)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		dlURL := "http://" + r.Host + "/dl/llama-gui.exe"
		w.Write([]byte(`{"tag_name":"v0.2.0","name":"Release","assets":[{"name":"llama-gui-amd64-installer.exe","size":10,"browser_download_url":"https://x/i.exe"},{"name":"llama-gui.exe","size":` + strconv.Itoa(len(payload)) + `,"browser_download_url":"` + dlURL + `"}]}`))
	}))
	defer srv.Close()
	updateRepoAPI = srv.URL

	downloadUpdateRelease("v0.2.0")

	updateDownloadMu.Lock()
	ds := *updateDownloadState
	updateDownloadMu.Unlock()

	if ds.Status != "done" {
		t.Fatalf("download completion status = %q, want done (error: %s)", ds.Status, ds.Error)
	}
	wantPath := filepath.Join(exeDir, "MyLlama-portable-v0.2.0.exe")
	if ds.FilePath != wantPath {
		t.Errorf("save path = %q, want %q", ds.FilePath, wantPath)
	}
	if ds.Kind != installKindPortable {
		t.Errorf("download kind = %q, want %q", ds.Kind, installKindPortable)
	}
	if ds.Installer {
		t.Error("portable asset download should report installer=false")
	}
	fi, err := os.Stat(wantPath)
	if err != nil {
		t.Fatalf("target file does not exist: %v", err)
	}
	if fi.Size() != int64(len(payload)) {
		t.Errorf("file size = %d, want %d", fi.Size(), len(payload))
	}
}

// TestDownloadUpdateReleaseSetup verifies setup installer update download: when the
// executable directory contains uninstall.exe (NSIS install), the setup-type asset is
// selected, downloaded as MyLlama-setup-v<tag>.exe, status becomes done, and kind=setup.
func TestDownloadUpdateReleaseSetup(t *testing.T) {
	withTempCwd(t)
	saveUpdateState(t)
	// download landing directory uses temp dir to simulate "same directory as executable", with uninstall.exe
	// to simulate NSIS install directory
	exeDir := t.TempDir()
	if err := os.WriteFile(filepath.Join(exeDir, "uninstall.exe"), []byte("uninstaller"), 0644); err != nil {
		t.Fatal(err)
	}
	updateExePath = func() (string, error) {
		return filepath.Join(exeDir, "MyLlama.exe"), nil
	}

	payload := []byte("MZ fake setup payload")
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/dl/MyLlama-setup-v0.2.0-windows-amd64.exe" {
			w.Header().Set("Content-Length", strconv.Itoa(len(payload)))
			w.Write(payload)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		dlURL := "http://" + r.Host + "/dl/MyLlama-setup-v0.2.0-windows-amd64.exe"
		w.Write([]byte(`{"tag_name":"v0.2.0","name":"Release","assets":[{"name":"MyLlama-setup-v0.2.0-windows-amd64.exe","size":` + strconv.Itoa(len(payload)) + `,"browser_download_url":"` + dlURL + `"},{"name":"MyLlama-portable-v0.2.0-amd64.exe","size":10,"browser_download_url":"https://x/p.exe"}]}`))
	}))
	defer srv.Close()
	updateRepoAPI = srv.URL

	downloadUpdateRelease("v0.2.0")

	updateDownloadMu.Lock()
	ds := *updateDownloadState
	updateDownloadMu.Unlock()

	if ds.Status != "done" {
		t.Fatalf("download completion status = %q, want done (error: %s)", ds.Status, ds.Error)
	}
	wantPath := filepath.Join(exeDir, "MyLlama-setup-v0.2.0.exe")
	if ds.FilePath != wantPath {
		t.Errorf("save path = %q, want %q", ds.FilePath, wantPath)
	}
	if ds.Kind != installKindSetup {
		t.Errorf("download kind = %q, want %q", ds.Kind, installKindSetup)
	}
	if !ds.Installer {
		t.Error("setup installer download should report installer=true")
	}
	fi, err := os.Stat(wantPath)
	if err != nil {
		t.Fatalf("target file does not exist: %v", err)
	}
	if fi.Size() != int64(len(payload)) {
		t.Errorf("file size = %d, want %d", fi.Size(), len(payload))
	}
}

// TestDownloadUpdateReleasePortableFallbackSetup verifies the portable-install fallback
// end-to-end: a portable install (no uninstall.exe) updating from a release that ships
// only the setup installer (portable builds retired) downloads that installer, and the
// saved file is named after the actual asset type (MyLlama-setup-v<tag>.exe), not
// the local portable kind; the reported kind stays portable.
func TestDownloadUpdateReleasePortableFallbackSetup(t *testing.T) {
	withTempCwd(t)
	saveUpdateState(t)
	// no uninstall.exe in the exe directory → detected install kind is portable
	exeDir := t.TempDir()
	updateExePath = func() (string, error) {
		return filepath.Join(exeDir, "MyLlama.exe"), nil
	}

	payload := []byte("MZ fake setup payload for portable fallback")
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/dl/MyLlama-setup-v0.2.1-windows-amd64.exe" {
			w.Header().Set("Content-Length", strconv.Itoa(len(payload)))
			w.Write(payload)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		dlURL := "http://" + r.Host + "/dl/MyLlama-setup-v0.2.1-windows-amd64.exe"
		w.Write([]byte(`{"tag_name":"v0.2.1","name":"Release","assets":[{"name":"MyLlama-setup-v0.2.1-windows-amd64.exe","size":` + strconv.Itoa(len(payload)) + `,"browser_download_url":"` + dlURL + `"}]}`))
	}))
	defer srv.Close()
	updateRepoAPI = srv.URL

	downloadUpdateRelease("v0.2.1")

	updateDownloadMu.Lock()
	ds := *updateDownloadState
	updateDownloadMu.Unlock()

	if ds.Status != "done" {
		t.Fatalf("status = %q, want done (error: %s)", ds.Status, ds.Error)
	}
	// filename follows the asset type (setup), not the local portable kind
	wantPath := filepath.Join(exeDir, "MyLlama-setup-v0.2.1.exe")
	if ds.FilePath != wantPath {
		t.Errorf("save path = %q, want %q", ds.FilePath, wantPath)
	}
	if ds.Kind != installKindPortable {
		t.Errorf("download kind = %q, want %q (local install kind unchanged)", ds.Kind, installKindPortable)
	}
	if !ds.Installer {
		t.Error("portable fallback to the setup installer should report installer=true (flag follows the artifact)")
	}
	fi, err := os.Stat(wantPath)
	if err != nil {
		t.Fatalf("target file does not exist: %v", err)
	}
	if fi.Size() != int64(len(payload)) {
		t.Errorf("file size = %d, want %d", fi.Size(), len(payload))
	}
}

// TestDownloadUpdateReleaseCrossDeviceFallback verifies update download completes via
// copy fallback in a cross-device scenario (renameFile is injected with EXDEV, corresponding
// to Windows system temp directory and executable on different drives):
// status becomes done, target file exists and content is correct. Also verifies the EXDEV
// branch takes priority over delete-and-retry (an existing old file at the target path is
// not deleted first and then lost on failure).
func TestDownloadUpdateReleaseCrossDeviceFallback(t *testing.T) {
	withTempCwd(t)
	saveUpdateState(t)
	// download landing directory uses temp dir to simulate "same directory as executable" (different device from source temp file)
	exeDir := t.TempDir()
	updateExePath = func() (string, error) {
		return filepath.Join(exeDir, "MyLlama.exe"), nil
	}

	payload := []byte("MZ fake exe payload cross device")
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/dl/llama-gui.exe" {
			w.Header().Set("Content-Length", strconv.Itoa(len(payload)))
			w.Write(payload)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		dlURL := "http://" + r.Host + "/dl/llama-gui.exe"
		w.Write([]byte(`{"tag_name":"v0.2.0","name":"Release","assets":[{"name":"llama-gui.exe","size":` + strconv.Itoa(len(payload)) + `,"browser_download_url":"` + dlURL + `"}]}`))
	}))
	defer srv.Close()
	updateRepoAPI = srv.URL

	// inject renameFile to simulate cross-device failure (moveFile calls this package-level variable;
	// crossDeviceRenameErr is the real cross-device error for the current platform:
	// Windows cross-drive ERROR_NOT_SAME_DEVICE=17 / Unix EXDEV, wrapped in LinkError to simulate real shape)
	origRename := renameFile
	renameFile = func(oldpath, newpath string) error {
		return &os.LinkError{Op: "rename", Old: oldpath, New: newpath, Err: crossDeviceRenameErr}
	}
	defer func() { renameFile = origRename }()

	// target path already contains an old-version file; verify cross-device fallback does not delete the old file first and cause data loss
	wantPath := filepath.Join(exeDir, "MyLlama-portable-v0.2.0.exe")
	if err := os.WriteFile(wantPath, []byte("old version"), 0644); err != nil {
		t.Fatal(err)
	}

	downloadUpdateRelease("v0.2.0")

	updateDownloadMu.Lock()
	ds := *updateDownloadState
	updateDownloadMu.Unlock()

	if ds.Status != "done" {
		t.Fatalf("status after cross-device fallback = %q, want done (error: %s)", ds.Status, ds.Error)
	}
	if ds.FilePath != wantPath {
		t.Errorf("save path = %q, want %q", ds.FilePath, wantPath)
	}
	if ds.Kind != installKindPortable {
		t.Errorf("download kind = %q, want %q", ds.Kind, installKindPortable)
	}
	got, err := os.ReadFile(wantPath)
	if err != nil {
		t.Fatalf("target file does not exist: %v", err)
	}
	if string(got) != string(payload) {
		t.Errorf("target content = %q, want %q", got, payload)
	}
}

// saveUpdateInstall snapshots the install-now injection points (installer
// launcher / quit delay) and restores them after the test, preventing
// cross-test pollution (same style as saveUpdateState).
func saveUpdateInstall(t *testing.T) {
	t.Helper()
	origLauncher := updateLauncher
	origDelay := updateQuitDelay
	t.Cleanup(func() {
		updateLauncher = origLauncher
		updateQuitDelay = origDelay
	})
}

// TestInstallUpdateNowRejectsNotDone verifies install-now requires a completed
// download: idle / downloading / error statuses are all rejected without
// touching the launcher or quitting.
func TestInstallUpdateNowRejectsNotDone(t *testing.T) {
	saveUpdateState(t)
	saveUpdateInstall(t)

	launched := false
	updateLauncher = func(string) error { launched = true; return nil }

	for _, status := range []string{"idle", "downloading", "error", "installing"} {
		updateDownloadMu.Lock()
		*updateDownloadState = UpdateDownloadState{Status: status, Installer: true, FilePath: "x.exe"}
		updateDownloadMu.Unlock()
		if err := installUpdateNow(func() { t.Error("quit must not run") }); err == nil {
			t.Errorf("status %q should reject install-now", status)
		}
	}
	if launched {
		t.Error("launcher must not run when the download is not done")
	}
}

// TestInstallUpdateNowRejectsNonInstaller verifies install-now only applies to
// setup-installer artifacts: a portable artifact (Installer=false, e.g. a
// legacy release that still shipped portable builds) is rejected with the
// manual-update hint.
func TestInstallUpdateNowRejectsNonInstaller(t *testing.T) {
	saveUpdateState(t)
	saveUpdateInstall(t)

	path := filepath.Join(t.TempDir(), "MyLlama-portable-v0.2.0.exe")
	if err := os.WriteFile(path, []byte("MZ"), 0644); err != nil {
		t.Fatal(err)
	}
	updateDownloadMu.Lock()
	*updateDownloadState = UpdateDownloadState{Status: "done", Installer: false, FilePath: path}
	updateDownloadMu.Unlock()

	launched := false
	updateLauncher = func(string) error { launched = true; return nil }

	if err := installUpdateNow(func() {}); err == nil {
		t.Error("portable artifact should reject install-now")
	}
	if launched {
		t.Error("launcher must not run for a portable artifact")
	}
}

// TestInstallUpdateNowRejectsMissingFile verifies a done+installer state whose
// file no longer exists on disk is rejected (launcher never runs).
func TestInstallUpdateNowRejectsMissingFile(t *testing.T) {
	saveUpdateState(t)
	saveUpdateInstall(t)

	updateDownloadMu.Lock()
	*updateDownloadState = UpdateDownloadState{Status: "done", Installer: true, FilePath: filepath.Join(t.TempDir(), "missing.exe")}
	updateDownloadMu.Unlock()

	launched := false
	updateLauncher = func(string) error { launched = true; return nil }

	if err := installUpdateNow(func() {}); err == nil {
		t.Error("missing installer file should reject install-now")
	}
	if launched {
		t.Error("launcher must not run for a missing file")
	}
}

// TestInstallUpdateNowLaunchFailureRestoresDone verifies a launcher error is
// returned to the caller and the status returns to done, so the update modal
// can surface the failure and the user can retry.
func TestInstallUpdateNowLaunchFailureRestoresDone(t *testing.T) {
	saveUpdateState(t)
	saveUpdateInstall(t)

	path := filepath.Join(t.TempDir(), "MyLlama-setup-v0.2.0.exe")
	if err := os.WriteFile(path, []byte("MZ"), 0644); err != nil {
		t.Fatal(err)
	}
	updateDownloadMu.Lock()
	*updateDownloadState = UpdateDownloadState{Status: "done", Installer: true, FilePath: path}
	updateDownloadMu.Unlock()

	updateLauncher = func(string) error { return errors.New("exec failed") }

	if err := installUpdateNow(func() { t.Error("quit must not run on launch failure") }); err == nil {
		t.Fatal("launcher failure should return an error")
	}
	updateDownloadMu.Lock()
	st := updateDownloadState.Status
	updateDownloadMu.Unlock()
	if st != "done" {
		t.Errorf("status after launch failure = %q, want done (retryable)", st)
	}
}

// TestInstallUpdateNowLaunchesAndQuits verifies the happy path: the downloaded
// installer path is launched, the status moves to installing (which also
// rejects a second install call — the double-click guard), and quit fires
// after the configured delay.
func TestInstallUpdateNowLaunchesAndQuits(t *testing.T) {
	saveUpdateState(t)
	saveUpdateInstall(t)

	path := filepath.Join(t.TempDir(), "MyLlama-setup-v0.2.0.exe")
	if err := os.WriteFile(path, []byte("MZ"), 0644); err != nil {
		t.Fatal(err)
	}
	updateDownloadMu.Lock()
	*updateDownloadState = UpdateDownloadState{Status: "done", Installer: true, FilePath: path}
	updateDownloadMu.Unlock()

	var gotPath string
	updateLauncher = func(p string) error { gotPath = p; return nil }
	updateQuitDelay = 0

	quitCh := make(chan struct{})
	if err := installUpdateNow(func() { close(quitCh) }); err != nil {
		t.Fatalf("installUpdateNow error: %v", err)
	}
	if gotPath != path {
		t.Errorf("launched path = %q, want %q", gotPath, path)
	}
	updateDownloadMu.Lock()
	st := updateDownloadState.Status
	updateDownloadMu.Unlock()
	if st != "installing" {
		t.Errorf("status = %q, want installing", st)
	}
	// double-click guard: a second install attempt is rejected while installing
	if err := installUpdateNow(func() {}); err == nil {
		t.Error("second install call while installing should be rejected")
	}

	select {
	case <-quitCh:
	case <-time.After(5 * time.Second):
		t.Error("quit was not called after the delay")
	}
}

// TestSanitizeTagName verifies the whitelist sanitization of release tags used
// inside update download file names: [A-Za-z0-9._-] pass through unchanged,
// every other character is replaced with '-', and a tag that sanitizes to
// empty is rejected. This is the defense-in-depth layer keeping a hostile
// API-served TagName from smuggling path separators into a file name next to
// the running executable.
func TestSanitizeTagName(t *testing.T) {
	cases := []struct {
		name    string
		tag     string
		want    string
		wantErr bool
	}{
		{"plain semver passes through", "v0.3.3", "v0.3.3", false},
		{"prerelease suffix passes through", "v1.2.3-beta.1", "v1.2.3-beta.1", false},
		{"path traversal separators replaced", "../../evil", "..-..-evil", false},
		{"slash and backslash replaced", "a/b\\c", "a-b-c", false},
		{"spaces and URL metacharacters replaced", "v1 x?y#z", "v1-x-y-z", false},
		{"empty tag rejected", "", "", true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got, err := sanitizeTagName(tc.tag)
			if tc.wantErr {
				if err == nil {
					t.Fatalf("sanitizeTagName(%q) should fail, got %q", tc.tag, got)
				}
				return
			}
			if err != nil {
				t.Fatalf("sanitizeTagName(%q) unexpected error: %v", tc.tag, err)
			}
			if got != tc.want {
				t.Errorf("sanitizeTagName(%q) = %q, want %q", tc.tag, got, tc.want)
			}
		})
	}
}
