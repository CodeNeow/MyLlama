package core

import (
	"bytes"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
)

// ─── Per-OS app path resolution (paths.go) ───────────────────────

// withPathsSeams injects the per-OS resolution seams (runtime.GOOS selector,
// user config root, MkdirAll) and resets the once-resolved app-data base so
// the next appDataDir call re-runs resolution under the injected values.
// Everything is restored after the test, so later tests see the production
// (Windows, real os.UserConfigDir) seams again.
func withPathsSeams(t *testing.T, goos string, userConfigDir string, userConfigDirErr error, mkdirErr error) {
	t.Helper()
	oldGOOS, oldUCD, oldMkdir := pathsGOOS, pathsUserConfigDir, pathsMkdirAll
	t.Cleanup(func() {
		pathsGOOS, pathsUserConfigDir, pathsMkdirAll = oldGOOS, oldUCD, oldMkdir
		resetPathsCache()
	})
	pathsGOOS = goos
	pathsUserConfigDir = func() (string, error) {
		if userConfigDirErr != nil {
			return "", userConfigDirErr
		}
		return userConfigDir, nil
	}
	if mkdirErr != nil {
		pathsMkdirAll = func(string, os.FileMode) error { return mkdirErr }
	} else {
		pathsMkdirAll = os.MkdirAll
	}
	resetPathsCache()
}

// resetPathsCache clears the once-resolved app-data base so the next
// appDataDir call resolves again under the current seams (tests only).
func resetPathsCache() {
	pathsOnce = sync.Once{}
	pathsBase = ""
}

// TestAppDataDirWindowsKeepsCwdRelative verifies the Windows branch is a
// strict no-op: the app-data base stays empty, every state-file and default
// directory resolves to its bare cwd-relative name, the user config root is
// never consulted and no base directory is created on disk.
func TestAppDataDirWindowsKeepsCwdRelative(t *testing.T) {
	root := t.TempDir()
	withPathsSeams(t, "windows", root, nil, nil)
	if got := appDataDir(); got != "" {
		t.Errorf("appDataDir on windows = %q, want empty", got)
	}
	if got := resolveStateFile(configFileName); got != configFileName {
		t.Errorf("resolveStateFile on windows = %q, want bare %q", got, configFileName)
	}
	if got := defaultModelsDir(); got != modelsDirName {
		t.Errorf("defaultModelsDir on windows = %q, want bare %q", got, modelsDirName)
	}
	if got := defaultLlamaCppDir(); got != llamaCppDirName {
		t.Errorf("defaultLlamaCppDir on windows = %q, want bare %q", got, llamaCppDirName)
	}
	if _, err := os.Stat(filepath.Join(root, appDataDirName)); !os.IsNotExist(err) {
		t.Errorf("windows branch must not create the app-data base, stat err = %v", err)
	}
}

// TestResolveStateFileUnderAppDataNonWindows verifies the non-Windows desktop
// branch: the base directory <UserConfigDir>/myllama is created on
// first use and every name resolves beneath it, stable across calls.
func TestResolveStateFileUnderAppDataNonWindows(t *testing.T) {
	root := t.TempDir()
	withPathsSeams(t, "linux", root, nil, nil)
	wantBase := filepath.Join(root, appDataDirName)
	if got := appDataDir(); got != wantBase {
		t.Fatalf("appDataDir = %q, want %q", got, wantBase)
	}
	if fi, err := os.Stat(wantBase); err != nil || !fi.IsDir() {
		t.Fatalf("app-data base not created on first use: stat err = %v", err)
	}
	for _, name := range []string{configFileName, handoverFileName, benchCacheFileName, docsCacheDirName, modelsDirName, llamaCppDirName} {
		if got := resolveStateFile(name); got != filepath.Join(wantBase, name) {
			t.Errorf("resolveStateFile(%q) = %q, want %q", name, got, filepath.Join(wantBase, name))
		}
	}
	if got := appDataDir(); got != wantBase {
		t.Errorf("appDataDir not stable across calls: %q, want %q", got, wantBase)
	}
}

// TestAppDataDirUserConfigDirErrorFallsBack verifies the best-effort fallback:
// when the user config root cannot be resolved, the base stays empty, names
// keep their legacy cwd-relative form and a [WARN] is logged.
func TestAppDataDirUserConfigDirErrorFallsBack(t *testing.T) {
	buf := captureLogOutput(t)
	withPathsSeams(t, "darwin", "", errors.New("no config root"), nil)
	if got := appDataDir(); got != "" {
		t.Errorf("appDataDir with failing UserConfigDir = %q, want empty", got)
	}
	if got := resolveStateFile(benchCacheFileName); got != benchCacheFileName {
		t.Errorf("resolveStateFile fallback = %q, want bare %q", got, benchCacheFileName)
	}
	if !strings.Contains(buf.String(), "[WARN]") {
		t.Errorf("fallback should log [WARN], got: %s", buf.String())
	}
}

// TestAppDataDirMkdirFailureFallsBack verifies the best-effort fallback when
// the base directory cannot be created: empty base, legacy cwd-relative names
// and a [WARN] log.
func TestAppDataDirMkdirFailureFallsBack(t *testing.T) {
	buf := captureLogOutput(t)
	withPathsSeams(t, "linux", t.TempDir(), nil, errors.New("disk full"))
	if got := appDataDir(); got != "" {
		t.Errorf("appDataDir with failing MkdirAll = %q, want empty", got)
	}
	if got := resolveStateFile(configFileName); got != configFileName {
		t.Errorf("resolveStateFile fallback = %q, want bare %q", got, configFileName)
	}
	if !strings.Contains(buf.String(), "[WARN]") {
		t.Errorf("fallback should log [WARN], got: %s", buf.String())
	}
}

// ─── Android branch (GOOS=android) ────────────────────────────────

// withAndroidSeams injects the Android storage seams (JNI bridge stand-ins)
// and resets the once-resolved app-data base. filesDirModelBase writes into
// the given directories exactly like the production JNI functions do (the
// production impls mkdir the anchor themselves); pass "" to simulate an
// unavailable anchor.
func withAndroidSeams(t *testing.T, filesDir string, modelsBase string) {
	t.Helper()
	oldGOOS, oldFiles, oldModels := pathsGOOS, pathsAndroidFilesDir, pathsAndroidModelsBase
	t.Cleanup(func() {
		pathsGOOS, pathsAndroidFilesDir, pathsAndroidModelsBase = oldGOOS, oldFiles, oldModels
		resetPathsCache()
	})
	pathsGOOS = "android"
	pathsAndroidFilesDir = func() string { return filesDir }
	pathsAndroidModelsBase = func() string { return modelsBase }
	resetPathsCache()
}

// TestAppDataDirAndroidUsesFilesDir verifies the Android branch resolves the
// app-data base from the JNI bridge's files dir unconditionally — even when
// os.UserConfigDir would succeed, the host cwd is read-only so the bridge
// anchor is the only authoritative location — and state-file names resolve
// beneath it.
func TestAppDataDirAndroidUsesFilesDir(t *testing.T) {
	files := t.TempDir()
	withPathsSeams(t, "android", t.TempDir(), nil, nil) // config root available but ignored
	withAndroidSeams(t, files, files)
	if got := appDataDir(); got != files {
		t.Fatalf("appDataDir on android = %q, want files dir %q", got, files)
	}
	for _, name := range []string{configFileName, handoverFileName, benchCacheFileName, docsCacheDirName} {
		if got := resolveStateFile(name); got != filepath.Join(files, name) {
			t.Errorf("resolveStateFile(%q) = %q, want %q", name, got, filepath.Join(files, name))
		}
	}
	if got := defaultLlamaCppDir(); got != filepath.Join(files, llamaCppDirName) {
		t.Errorf("defaultLlamaCppDir on android = %q, want under files dir", got)
	}
}

// TestAppDataDirAndroidUnavailableKeepsCwdRelative verifies the end-of-chain
// fallback: an unavailable files-dir anchor leaves the base empty (legacy
// cwd-relative names) with a [WARN] logged.
func TestAppDataDirAndroidUnavailableKeepsCwdRelative(t *testing.T) {
	buf := captureLogOutput(t)
	withAndroidSeams(t, "", "")
	if got := appDataDir(); got != "" {
		t.Errorf("appDataDir with unavailable files dir = %q, want empty", got)
	}
	if got := resolveStateFile(benchCacheFileName); got != benchCacheFileName {
		t.Errorf("resolveStateFile fallback = %q, want bare %q", got, benchCacheFileName)
	}
	if !strings.Contains(buf.String(), "[WARN]") {
		t.Errorf("unavailable files dir should log [WARN], got: %s", buf.String())
	}
}

// TestAndroidDefaultModelsDirUnderExternalBase verifies the model directory
// splits off to the external-storage base while the llama.cpp runtime stays
// on internal storage, and that an unavailable external base falls back to
// the internal files dir (same-host fallback in androidModelsBase).
func TestAndroidDefaultModelsDirUnderExternalBase(t *testing.T) {
	files := t.TempDir()
	ext := t.TempDir()
	withAndroidSeams(t, files, ext)
	if got := defaultModelsDir(); got != filepath.Join(ext, modelsDirName) {
		t.Errorf("defaultModelsDir = %q, want under external base %q", got, ext)
	}
	if got := defaultLlamaCppDir(); got != filepath.Join(files, llamaCppDirName) {
		t.Errorf("defaultLlamaCppDir must stay under the internal files dir, got %q", got)
	}

	withAndroidSeams(t, files, "")
	if got := defaultModelsDir(); got != filepath.Join(files, modelsDirName) {
		t.Errorf("defaultModelsDir without external base = %q, want internal fallback %q", got, filepath.Join(files, modelsDirName))
	}
}

// TestResolveTempDirAndroid verifies Android temp-file routing: the app
// process has no TMPDIR so os.TempDir would return the read-only /tmp — the
// base/tmp dir is created and used instead; an unavailable base degrades to
// os.TempDir.
func TestResolveTempDirAndroid(t *testing.T) {
	files := t.TempDir()
	withAndroidSeams(t, files, files)
	if got := resolveTempDir(); got != filepath.Join(files, "tmp") {
		t.Errorf("resolveTempDir on android = %q, want %q", got, filepath.Join(files, "tmp"))
	}
	if fi, err := os.Stat(filepath.Join(files, "tmp")); err != nil || !fi.IsDir() {
		t.Errorf("base/tmp not created on first use: stat err = %v", err)
	}

	withAndroidSeams(t, "", "")
	if got := resolveTempDir(); got != os.TempDir() {
		t.Errorf("resolveTempDir without base = %q, want os.TempDir() %q", got, os.TempDir())
	}
}

// TestResolveServerLogPath verifies the server log resolution contract: bare
// names resolve under the app-data base (never the read-only cwd on
// Android/macOS), absolute overrides pass through unchanged (tests, handover
// adoption).
func TestResolveServerLogPath(t *testing.T) {
	files := t.TempDir()
	withAndroidSeams(t, files, files)
	old := serverLogFile
	t.Cleanup(func() { serverLogFile = old })

	serverLogFile = "myllama-server.log"
	if got := resolveServerLogPath(); got != filepath.Join(files, "myllama-server.log") {
		t.Errorf("resolveServerLogPath(bare) = %q, want under files dir", got)
	}

	abs := filepath.Join(t.TempDir(), "server.log")
	serverLogFile = abs
	if got := resolveServerLogPath(); got != abs {
		t.Errorf("resolveServerLogPath(abs) = %q, want passthrough %q", got, abs)
	}
}

// ─── Legacy config migration (era renames) ───────────────────────

// withLegacyMigrationSeams combines a temp cwd, non-Windows path seams and a
// default (unresolved) configFile so migrateLegacyConfig targets the
// app-data base. Returns the temp cwd and the log buffer.
func withLegacyMigrationSeams(t *testing.T, goos string) (string, *bytes.Buffer) {
	t.Helper()
	tmp := withTempCwd(t)
	buf := captureLogOutput(t)
	withPathsSeams(t, goos, t.TempDir(), nil, nil)
	// Resolve via the seams: undo the withTempCwd pin (its cleanup restores
	// the bare default afterwards).
	configFile = configFileName
	return tmp, buf
}

// TestMigrateCwdConfigToAppData verifies the non-Windows migration: a legacy
// cwd-relative llama-desktop-config.json (pre-app-data layout) is renamed
// onto the app-data base config path. The source is consumed by the rename;
// when a rename is impossible the chain degrades to a copy that keeps the
// source in place.
func TestMigrateCwdConfigToAppData(t *testing.T) {
	tmp, buf := withLegacyMigrationSeams(t, "linux")
	legacy := []byte(`{"theme":"dark"}`)
	if err := os.WriteFile(filepath.Join(tmp, legacyConfigFileName), legacy, 0644); err != nil {
		t.Fatal(err)
	}
	migrateLegacyConfig()
	target := filepath.Join(appDataDir(), configFileName)
	data, err := os.ReadFile(target)
	if err != nil {
		t.Fatalf("migrated config missing at %s: %v", target, err)
	}
	if !bytes.Equal(data, legacy) {
		t.Errorf("migrated content = %q, want %q", data, legacy)
	}
	if _, err := os.Stat(filepath.Join(tmp, legacyConfigFileName)); !os.IsNotExist(err) {
		t.Errorf("legacy cwd config should be consumed by the rename, stat err = %v", err)
	}
	if !strings.Contains(buf.String(), "[OK]") {
		t.Errorf("migration should log [OK], got: %s", buf.String())
	}
}

// TestMigrateCwdConfigToAppDataSkippedWhenTargetExists verifies the
// existence short-circuit: an already-present config at the target location
// is never overwritten by the legacy rename.
func TestMigrateCwdConfigToAppDataSkippedWhenTargetExists(t *testing.T) {
	tmp, buf := withLegacyMigrationSeams(t, "linux")
	existing := []byte(`{"theme":"light"}`)
	if err := os.WriteFile(configFilePath(), existing, 0644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(tmp, legacyConfigFileName), []byte(`{"theme":"dark"}`), 0644); err != nil {
		t.Fatal(err)
	}
	migrateLegacyConfig()
	data, err := os.ReadFile(configFilePath())
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(data, existing) {
		t.Errorf("existing target overwritten: got %q, want %q", data, existing)
	}
	if strings.Contains(buf.String(), "Migrated legacy config") {
		t.Errorf("no migration should run when the target exists, got: %s", buf.String())
	}
}

// TestMigrateLegacyConfigWindowsRenamesEraFile verifies the Windows branch of
// the era rename: with cwd-relative paths (the install-dir layout), a legacy
// llama-desktop-config.json in the cwd is renamed onto the active
// myllama-config.json in the same directory (atomic same-dir rename).
func TestMigrateLegacyConfigWindowsRenamesEraFile(t *testing.T) {
	tmp, buf := withLegacyMigrationSeams(t, "windows")
	legacy := []byte(`{"theme":"dark"}`)
	if err := os.WriteFile(filepath.Join(tmp, legacyConfigFileName), legacy, 0644); err != nil {
		t.Fatal(err)
	}
	migrateLegacyConfig()
	data, err := os.ReadFile(filepath.Join(tmp, configFileName))
	if err != nil {
		t.Fatalf("active config missing after rename: %v", err)
	}
	if !bytes.Equal(data, legacy) {
		t.Errorf("renamed content = %q, want %q", data, legacy)
	}
	if _, err := os.Stat(filepath.Join(tmp, legacyConfigFileName)); !os.IsNotExist(err) {
		t.Errorf("legacy config should be gone after the rename, stat err = %v", err)
	}
	if !strings.Contains(buf.String(), "[OK]") {
		t.Errorf("migration should log [OK], got: %s", buf.String())
	}
}

// TestMigrateGuiConfigToAppDataNonWindows verifies the llama-gui-era
// migration targets the app-data base on non-Windows platforms: the
// cwd-relative llama-gui-config.json content lands at the resolved config
// path via rename and the source is consumed.
func TestMigrateGuiConfigToAppDataNonWindows(t *testing.T) {
	tmp, _ := withLegacyMigrationSeams(t, "linux")
	gui := []byte(`{"theme":"dark","trayEnabled":false}`)
	if err := os.WriteFile(filepath.Join(tmp, legacyConfigFile), gui, 0644); err != nil {
		t.Fatal(err)
	}
	migrateLegacyConfig()
	data, err := os.ReadFile(configFilePath())
	if err != nil {
		t.Fatalf("gui-era config not migrated to %s: %v", configFilePath(), err)
	}
	if !bytes.Equal(data, gui) {
		t.Errorf("gui migration content = %q, want %q", data, gui)
	}
	if _, err := os.Stat(filepath.Join(tmp, legacyConfigFile)); !os.IsNotExist(err) {
		t.Errorf("gui-era source should be consumed by the rename, stat err = %v", err)
	}
}
