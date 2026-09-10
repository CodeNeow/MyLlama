package core

import (
	"context"
	_ "embed"
	"errors"
	"fmt"
	"io"
	"log"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"time"
)

// ─── App self-update ────────────────────────────────────────────
// Update check against the GitHub release API and the resumable update download
// / installer launch flow.

//go:embed VERSION
var versionFile []byte

// currentVersion is the current app version, aligned with GitHub release tags
// (e.g. v0.1.0). The version comes from the core/VERSION file (embedded at
// compile time, similar to the frontend .env); bump that file and tag the same
// name when releasing.
var currentVersion = strings.TrimSpace(string(versionFile))

// appUserAgent returns the User-Agent sent with every outbound HTTP request
// (GitHub API, HF mirror, ModelScope, model downloads). It carries the app
// name, the current version and the repository URL so recipients can
// attribute the traffic to this project.
func appUserAgent() string {
	return "MyLlama/" + currentVersion + " (+https://github.com/CodeNeow/MyLlama)"
}

// updateRepoAPI points to this repository's latest release API. The URL is
// received by CheckForUpdateAt to support test injection of a local httptest
// server. Declared as a var so tests can replace the package-level variable to
// simulate the network (same style as configFile / renameFile).
var updateRepoAPI = "https://api.github.com/repos/CodeNeow/MyLlama/releases/latest"

// compareVersions compares two version strings like v1.2.3 (leading v / V
// ignored). Returns -1 when a < b, 0 when equal, 1 when a > b; unparseable
// segments are treated as 0.
func compareVersions(a, b string) int {
	parse := func(s string) []int {
		s = strings.TrimPrefix(strings.TrimPrefix(s, "v"), "V")
		var parts []int
		for _, seg := range strings.Split(s, ".") {
			n, err := strconv.Atoi(seg)
			if err != nil {
				n = 0
			}
			parts = append(parts, n)
		}
		return parts
	}
	pa, pb := parse(a), parse(b)
	for i := 0; i < len(pa) || i < len(pb); i++ {
		var x, y int
		if i < len(pa) {
			x = pa[i]
		}
		if i < len(pb) {
			y = pb[i]
		}
		if x < y {
			return -1
		}
		if x > y {
			return 1
		}
	}
	return 0
}

// UpdateCheckResult is the update-check result returned to the frontend to
// decide whether a new version exists.
type UpdateCheckResult struct {
	HasUpdate bool   `json:"hasUpdate"`
	Version   string `json:"version"`   // latest version (tag name, e.g. v0.1.1)
	Notes     string `json:"notes"`     // release notes
	Published string `json:"published"` // publish time
}

// CheckForUpdateAt requests the latest release of the given repository and
// compares versions. apiURL is injectable so tests can use httptest instead
// of the real network.
//
// Platform gate: only targets with release artifacts ship updates — Windows
// (NSIS installer assets) and Android (the arm64 debug APK attached to every
// release). On linux/macOS there is nothing to update to — the check
// short-circuits to a "no update" result without touching the network.
func CheckForUpdateAt(apiURL string) (*UpdateCheckResult, error) {
	if platformGOOS != "windows" && platformGOOS != "android" {
		return &UpdateCheckResult{HasUpdate: false, Version: currentVersion}, nil
	}
	release, err := fetchLatestReleaseAt(context.Background(), apiURL)
	if err != nil {
		return nil, err
	}
	return &UpdateCheckResult{
		HasUpdate: compareVersions(release.TagName, currentVersion) > 0,
		Version:   release.TagName,
		Notes:     release.Body,
		Published: release.PublishedAt,
	}, nil
}

// updateDownloadState tracks the progress of the app update download
// (updating the exe). State machine values: idle / downloading / installing /
// done / error; "installing" means the downloaded setup installer has been
// launched and the app is about to exit (installUpdateNow).
type UpdateDownloadState struct {
	Status     string `json:"status"`
	Progress   int    `json:"progress"`
	Total      int64  `json:"total"`
	Downloaded int64  `json:"downloaded"`
	Version    string `json:"version"`
	FilePath   string `json:"filePath"`
	Error      string `json:"error"`
	Kind       string `json:"kind"` // install kind of the running app: setup (NSIS install) / portable
	// Installer reports whether the downloaded artifact is the setup
	// installer (a portable install may fall back to the installer asset);
	// only installer artifacts support the install-now flow.
	Installer bool `json:"installer"`
}

var updateDownloadState = &UpdateDownloadState{Status: "idle"}
var updateDownloadMu sync.Mutex
var updateDownloadCancel context.CancelFunc

// updateExePath is a test injection point (same style as renameFile /
// configFile) returning the current executable path, used to determine the
// target directory for the update exe.
var updateExePath = os.Executable

// updateLauncher is a test injection point (same style as renameFile /
// updateExePath) launching the downloaded setup installer as a detached child
// process. On Windows the installer needs elevation (UAC), so launchInstaller
// goes through ShellExecute runas instead of a plain exec.
var updateLauncher = func(path string) error {
	return launchInstaller(path)
}

// updateQuitDelay is the pause between launching the installer and quitting
// the app: it gives the frontend time to resolve the InstallUpdate call and
// render the exiting state before the window closes. Var so tests can zero it.
var updateQuitDelay = 500 * time.Millisecond

// Install-kind constants: setup is the NSIS installer build (downloads the
// setup installer), portable is the portable build (downloads the portable
// exe), android is the Android app build (downloads the arm64 APK attached to
// every release; the install itself goes through the Java bridge's
// PackageInstaller flow, see WailsJSBridge.installUpdateApk). Used for update
// artifact selection and distinguishing frontend hints.
const (
	installKindSetup    = "setup"
	installKindPortable = "portable"
	installKindAndroid  = "android"
)

// detectInstallKind detects the current install type: a setup install is done
// by NSIS and the install directory always contains uninstall.exe; a portable
// install is a green build with no uninstall.exe; Android is always the apk
// kind (the app ships as a single APK). Pure detection, cross-platform.
// Reuses updateExePath (the os.Executable test injection point) to stay
// testable.
func detectInstallKind() string {
	if platformGOOS == "android" {
		return installKindAndroid
	}
	exePath, err := updateExePath()
	if err != nil {
		return installKindPortable
	}
	if _, err := os.Stat(filepath.Join(filepath.Dir(exePath), "uninstall.exe")); err == nil {
		return installKindSetup
	}
	return installKindPortable
}

// pickUpdateAsset picks the update-download asset by install kind, matching
// by keyword (independent of artifact prefix), compatible with three naming
// generations:
//   - Current naming: setup installer MyLlama-setup-vX.Y.Z-windows-amd64.exe
//     (portable builds are no longer published);
//   - Old naming (since v0.1.7): llama-gui-setup- / llama-gui-portable- prefixes;
//   - Oldest naming (v0.1.6): installer llama-gui-amd64-installer.exe,
//     portable llama-gui.exe.
//
// Android (installKindAndroid) matches the release's APK artifact instead of
// the exe family: a .apk asset whose name contains "android", preferring
// arm64 (the only ABI the release pipeline publishes today, matching the
// arm64-only abiFilters of the gradle project).
//
// setup returns the first installer asset (name contains installer or setup) — matches MyLlama-setup-*;
// portable returns the first asset containing portable or any non-installer
// exe (the oldest llama-gui.exe contains none of portable/installer/setup and
// hits the "non-installer" branch). Portable builds are no longer published:
// existing portable installs update to the setup installer going forward, so
// when no portable/non-installer exe matches, the portable branch falls back
// to the first installer asset instead of failing.
func pickUpdateAsset(assets []GitHubAsset, kind string) *GitHubAsset {
	if kind == installKindAndroid {
		var firstAPK, firstARM64 *GitHubAsset
		for i := range assets {
			a := &assets[i]
			name := strings.ToLower(a.Name)
			if !strings.HasSuffix(name, ".apk") || !strings.Contains(name, "android") {
				continue
			}
			if firstAPK == nil {
				firstAPK = a
			}
			if strings.Contains(name, "arm64") && firstARM64 == nil {
				firstARM64 = a
			}
		}
		if firstARM64 != nil {
			return firstARM64
		}
		return firstAPK
	}
	var firstInstaller *GitHubAsset
	for i := range assets {
		a := &assets[i]
		name := strings.ToLower(a.Name)
		if !strings.HasSuffix(name, ".exe") {
			continue
		}
		isInstaller := strings.Contains(name, "installer") || strings.Contains(name, "setup")
		if isInstaller && firstInstaller == nil {
			firstInstaller = a
		}
		switch kind {
		case installKindSetup:
			if isInstaller {
				return a
			}
		default: // installKindPortable (unknown values fall back to portable semantics)
			if strings.Contains(name, "portable") || !isInstaller {
				return a
			}
		}
	}
	if kind != installKindSetup {
		// Portable builds are no longer published: releases ship only the
		// setup installer, so existing portable installs fall back to it.
		return firstInstaller
	}
	return nil
}

// sanitizeTagName whitelists a release tag for use inside a download file
// name: every character outside [A-Za-z0-9._-] is replaced with "-", and a
// tag that sanitizes to empty is rejected. Defense in depth: the normal path
// serves TagName from this repository's own releases (a v-prefixed semver),
// but the value ultimately arrives in a remote API response and is spliced
// into a file name placed next to the running executable — path separators or
// other file-name metacharacters must never reach it untouched.
func sanitizeTagName(tag string) (string, error) {
	s := strings.Map(func(r rune) rune {
		switch {
		case r >= 'a' && r <= 'z', r >= 'A' && r <= 'Z', r >= '0' && r <= '9', r == '.', r == '_', r == '-':
			return r
		}
		return '-'
	}, tag)
	if s == "" {
		return "", fmt.Errorf(tr("发布版本号无效: %q", "invalid release tag: %q"), tag)
	}
	return s, nil
}

// downloadUpdateRelease downloads the artifact matching the current install
// kind to the executable's directory: a setup install downloads the installer,
// a portable install downloads the portable exe (the running exe cannot be
// replaced directly); when the release ships no portable exe (portable builds
// are retired), a portable install falls back to the setup installer. The
// saved file is named by the selected asset type (setup installer / portable
// exe), not by the local install kind, so the fallback download keeps the
// setup filename. When finished, the user is prompted to close the app and
// complete the update per install kind.
func downloadUpdateRelease(version string) {
	ctx, cancel := context.WithCancel(context.Background())
	updateDownloadMu.Lock()
	updateDownloadCancel = cancel
	updateDownloadMu.Unlock()

	defer func() {
		updateDownloadMu.Lock()
		updateDownloadCancel = nil
		updateDownloadMu.Unlock()
		cancel()
	}()

	// Detect the install kind before the download starts: it decides which
	// asset to pick and the install kind reported to the frontend (the saved
	// filename follows the selected asset type, see Step 2).
	kind := detectInstallKind()

	// Step 1: fetch the latest release info and pick the matching exe asset by install kind
	updateDownloadMu.Lock()
	updateDownloadState.Status = "downloading"
	updateDownloadState.Progress = 0
	updateDownloadState.Downloaded = 0
	updateDownloadState.Total = 0
	updateDownloadState.Version = version
	updateDownloadState.Error = ""
	updateDownloadState.Kind = kind
	updateDownloadState.Installer = false
	updateDownloadMu.Unlock()

	release, err := fetchLatestReleaseAt(context.Background(), updateRepoAPI)
	if err != nil {
		setUpdateDownloadError(tr("获取发布信息失败: ", "Failed to fetch release info: ") + err.Error())
		return
	}
	asset := pickUpdateAsset(release.Assets, kind)
	if asset == nil {
		setUpdateDownloadError(tr("未找到适用于当前平台的主程序", "No main executable found for the current platform"))
		return
	}

	// Whether the picked asset is the setup installer decides the saved
	// filename (Step 2) and the Installer flag reported to the frontend (the
	// install-now flow only applies to installer artifacts). Android APK
	// artifacts are directly installable through the Java bridge's
	// PackageInstaller flow, so the frontend install-now offer applies to
	// them exactly like a desktop setup installer.
	assetName := strings.ToLower(asset.Name)
	isInstallerAsset := strings.Contains(assetName, "setup") || strings.Contains(assetName, "installer")
	if kind == installKindAndroid {
		isInstallerAsset = true
	}

	updateDownloadMu.Lock()
	updateDownloadState.Total = asset.Size
	updateDownloadState.Installer = isInstallerAsset
	updateDownloadMu.Unlock()

	// The tag lands verbatim inside the saved file name below; whitelist-
	// sanitize it first so a hostile API payload cannot smuggle path
	// separators or other metacharacters into a file placed next to the
	// running executable (defense in depth — see sanitizeTagName).
	tag, err := sanitizeTagName(release.TagName)
	if err != nil {
		setUpdateDownloadError(err.Error())
		return
	}

	// Step 2: download into the executable's directory, named by the selected
	// asset type (not the local install kind): installer assets (name contains
	// setup / installer) → MyLlama-setup-v<tag>.exe, anything else →
	// MyLlama-portable-v<tag>.exe. Non-fallback paths pick by kind, so
	// their filenames are unchanged; only the portable→installer fallback
	// switches to the setup name, keeping the filename honest about content.
	exePath, err := updateExePath()
	if err != nil {
		setUpdateDownloadError(tr("无法定位可执行文件路径: ", "Unable to locate the executable path: ") + err.Error())
		return
	}
	dir := filepath.Dir(exePath)
	var fileName string
	if kind == installKindAndroid {
		// The exe path lives on the read-only APK volume on Android: the
		// downloaded update lands in the app-private files dir instead,
		// where the Java installer bridge reads it for the PackageInstaller
		// session.
		dir = pathsAndroidFilesDir()
		if dir == "" {
			setUpdateDownloadError(tr("无法定位应用数据目录", "cannot resolve the app data directory"))
			return
		}
		fileName = "MyLlama-android-" + tag + ".apk"
	} else if isInstallerAsset {
		fileName = "MyLlama-setup-" + tag + ".exe"
	} else {
		fileName = "MyLlama-portable-" + tag + ".exe"
	}
	destPath := filepath.Join(dir, fileName)

	tmpPath, err := downloadUpdateWithResume(ctx, asset.BrowserDownloadURL, asset.Size)
	if err != nil {
		if ctx.Err() != nil {
			updateDownloadMu.Lock()
			updateDownloadState.Status = "idle"
			updateDownloadMu.Unlock()
			log.Println("[INFO] update download stopped by user")
		} else {
			setUpdateDownloadError(tr("下载失败: ", "Download failed: ") + err.Error())
		}
		return
	}
	defer os.Remove(tmpPath)

	// Step 3: move to the destination path (across devices moveFile falls back
	// to copy, preserving source permissions; on non-cross-device failure with
	// an existing destination, delete the old file first and retry)
	if err := moveFile(tmpPath, destPath); err != nil {
		setUpdateDownloadError(tr("保存文件失败: ", "Failed to save file: ") + err.Error())
		return
	}

	updateDownloadMu.Lock()
	updateDownloadState.Status = "done"
	updateDownloadState.Progress = 100
	updateDownloadState.FilePath = destPath
	updateDownloadState.Kind = kind
	updateDownloadMu.Unlock()

	log.Printf("[OK] update %s downloaded to %s", release.TagName, destPath)
}

func setUpdateDownloadError(msg string) {
	updateDownloadMu.Lock()
	updateDownloadState.Status = "error"
	updateDownloadState.Error = msg
	updateDownloadMu.Unlock()
	log.Printf("[ERROR] update download error: %s", msg)
}

// installUpdateNow launches the downloaded setup installer and then quits the
// app (via quit) so the installer can replace the program files without the
// user closing anything manually. Guards: only a completed download (status
// done) whose artifact is the setup installer (Installer) and whose file still
// exists can be installed. The status moves to "installing" before launching,
// which both tells the frontend the app is exiting and rejects a double click
// (this function requires status done). A launch failure restores status
// "done" so the user can retry from the update modal.
//
// Android never reaches the launch path: its APK install goes through the
// Java bridge (WailsJSBridge.installUpdateApk → PackageInstaller), triggered
// by the frontend directly — os/exec is unusable in the app sandbox and the
// system confirmation dialog, not an app exit, owns the install UX.
func installUpdateNow(quit func()) error {
	if platformGOOS == "android" {
		return errors.New(tr("请在前端触发的系统安装弹窗中完成安装", "the Android install runs through the frontend-triggered system installer dialog"))
	}
	updateDownloadMu.Lock()
	status := updateDownloadState.Status
	installer := updateDownloadState.Installer
	filePath := updateDownloadState.FilePath
	updateDownloadMu.Unlock()

	if status != "done" {
		return errors.New(tr("更新尚未完成，无法安装", "update download is not finished; cannot install"))
	}
	if !installer {
		return errors.New(tr("下载的文件不是安装器，请手动完成更新", "the downloaded file is not the installer; finish the update manually"))
	}
	if _, err := os.Stat(filePath); err != nil {
		return fmt.Errorf(tr("找不到安装器文件: %w", "cannot find the installer file: %w"), err)
	}

	updateDownloadMu.Lock()
	updateDownloadState.Status = "installing"
	updateDownloadMu.Unlock()

	if err := updateLauncher(filePath); err != nil {
		updateDownloadMu.Lock()
		updateDownloadState.Status = "done"
		updateDownloadMu.Unlock()
		return fmt.Errorf(tr("启动安装器失败: %w", "failed to launch the installer: %w"), err)
	}
	log.Printf("[OK] update installer launched (%s), quitting app", filePath)

	// Quit from a goroutine after a short delay so the InstallUpdate binding
	// call resolves and the frontend can render the exiting state first.
	go func() {
		time.Sleep(updateQuitDelay)
		quit()
	}()
	return nil
}

// downloadUpdateWithResume downloads the update file to a temp file and
// reports progress, supporting cancellation. Unlike downloadWithResume: the
// update exe is small and does not support pause/resume; it only responds to
// context cancellation (app exit / stop download). Transient failures
// (network errors, HTTP 429/5xx, mid-stream errors) are retried internally
// up to downloadRetryCount times (each attempt restarts clean); the error is
// surfaced for a manual retry only after the retries are exhausted.
func downloadUpdateWithResume(ctx context.Context, url string, totalSize int64) (string, error) {
	tmpFile, err := os.CreateTemp(resolveTempDir(), "myllama-update-*.exe")
	if err != nil {
		return "", fmt.Errorf(tr("创建临时文件失败: %w", "failed to create temporary file: %w"), err)
	}
	tmpPath := tmpFile.Name()

	retries := 0
	for {
		// Each attempt restarts clean: truncate and rewind the handle (no
		// Range resume for updates) and reset the progress display.
		if err := tmpFile.Truncate(0); err != nil {
			tmpFile.Close()
			return tmpPath, err
		}
		if _, err := tmpFile.Seek(0, 0); err != nil {
			tmpFile.Close()
			return tmpPath, err
		}
		updateDownloadMu.Lock()
		updateDownloadState.Downloaded = 0
		updateDownloadMu.Unlock()

		req, err := http.NewRequestWithContext(ctx, "GET", url, nil)
		if err != nil {
			tmpFile.Close()
			return tmpPath, err
		}
		req.Header.Set("User-Agent", appUserAgent())

		client := &http.Client{Timeout: 30 * time.Minute}
		resp, err := client.Do(req)
		if err != nil {
			if ctx.Err() == nil && retries < downloadRetryCount {
				retries++
				log.Printf("[WARN] update download attempt failed (%v), retrying %d/%d", err, retries, downloadRetryCount)
				if sleepDownloadRetry(ctx) {
					continue
				}
			}
			tmpFile.Close()
			return tmpPath, err
		}

		if resp.StatusCode != http.StatusOK {
			status := resp.StatusCode
			resp.Body.Close()
			if retryableDownloadStatus(status) && ctx.Err() == nil && retries < downloadRetryCount {
				retries++
				log.Printf("[WARN] update download got HTTP %d, retrying %d/%d", status, retries, downloadRetryCount)
				if sleepDownloadRetry(ctx) {
					continue
				}
			}
			tmpFile.Close()
			return tmpPath, fmt.Errorf("HTTP %d", status)
		}

		buf := make([]byte, 32*1024)
		downloaded := int64(0)
		for {
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
				tmpFile.Close()
				return tmpPath, ctx.Err()
			case rr = <-ch:
			}

			if rr.n > 0 {
				if _, writeErr := tmpFile.Write(buf[:rr.n]); writeErr != nil {
					resp.Body.Close()
					tmpFile.Close()
					return tmpPath, writeErr
				}
				downloaded += int64(rr.n)
				updateDownloadMu.Lock()
				updateDownloadState.Downloaded = downloaded
				if updateDownloadState.Total > 0 {
					updateDownloadState.Progress = int(float64(downloaded) * 100 / float64(updateDownloadState.Total))
				}
				updateDownloadMu.Unlock()
			}
			if rr.err != nil {
				resp.Body.Close()
				if rr.err == io.EOF {
					tmpFile.Close()
					return tmpPath, nil
				}
				// Mid-body stream failures are transient: restart the
				// download (clean truncate) up to downloadRetryCount times.
				if ctx.Err() == nil && retries < downloadRetryCount {
					retries++
					log.Printf("[WARN] update download stream failed (%v), retrying %d/%d", rr.err, retries, downloadRetryCount)
					if sleepDownloadRetry(ctx) {
						break // back to the attempt loop
					}
				}
				tmpFile.Close()
				return tmpPath, rr.err
			}
		}
	}
}
