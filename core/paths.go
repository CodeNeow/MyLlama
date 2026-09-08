package core

// ─── App path resolution ─────────────────────────────────────────
//
// Per-OS resolution of the app's state-file and default-directory locations.
//
// Windows keeps the historical layout unchanged: every state file (config /
// handover record / bench cache / docs cache) and every default directory
// (LLM-Models, llama-cpp) resolves to its bare cwd-relative name, exactly as
// before this file existed (in practice the process cwd is the install dir).
//
// Non-Windows desktop platforms (darwin / linux) resolve a per-user app-data
// base via os.UserConfigDir() + "/myllama", created on first use (a legacy
// "llama-desktop" base is renamed to it once, see migrateAppDataDirName):
// macOS .app bundles launch with cwd = "/" and Linux launchers may pick
// arbitrary working directories, so bare names would scatter state across
// the filesystem. Android (GOOS=android, the Wails v3 app target) has a
// read-only process cwd ("/") and provides neither $HOME nor $XDG_CONFIG_HOME,
// so its base comes from the Wails v3 JNI bridge instead —
// Context.getFilesDir() via application.Android.StoragePath() — with the
// static /data/data/<host package>/files path as a fallback (see
// androidpath_android.go). Large model storage additionally prefers the
// app-specific external-storage dir derived by path symmetry
// (androidExternalBase); the llama.cpp runtime stays on internal storage
// because SELinux forbids exec on the external mount.
//
// The per-OS branch is a runtime.GOOS switch (not build tags) so a single
// test binary exercises every branch via the injected seams below.

import (
	"log"
	"os"
	"path/filepath"
	"runtime"
	"sync"
)

// Canonical state-file and default-directory names. The state-file vars
// (configFile, handoverFile, benchCacheFile, docsCacheDir) initialize to the
// bare names and resolve through the *Path getters at use time; the default
// directories resolve through defaultModelsDir / defaultLlamaCppDir.
//
// The legacy*Name constants are the pre-MyLlama-rebrand names. They are
// migration sources only: each consumer renames/copy-falls-back from the
// legacy name to the new one on first use (see migrateLegacyConfig,
// migrateLegacyBenchCache, migrateLegacyDocsCacheDir, migrateAppDataDirName,
// readHandover) and never writes the legacy names again.
const (
	configFileName     = "myllama-config.json"
	handoverFileName   = "myllama-server-handover.json"
	benchCacheFileName = "myllama-benchcache.json"
	docsCacheDirName   = "myllama-docscache"
	modelsDirName      = "LLM-Models"
	llamaCppDirName    = "llama-cpp"

	// Legacy (pre-rebrand) names, kept solely as migration sources.
	legacyConfigFileName     = "llama-desktop-config.json"
	legacyHandoverFileName   = "llama-desktop-server-handover.json"
	legacyBenchCacheFileName = "llama-desktop-benchcache.json"
	legacyDocsCacheDirName   = "llama-desktop-docscache"
	legacyAppDataDirName     = "llama-desktop"
	legacyGuiConfigFileName  = "llama-gui-config.json"
)

// Injection seams (same style as cmdTimeout / benchMeasureFn): tests swap
// these to drive the per-OS branches from a single test binary.
var (
	// pathsGOOS is the OS branch selector (runtime.GOOS in production).
	pathsGOOS = runtime.GOOS
	// pathsUserConfigDir resolves the per-user config root (os.UserConfigDir
	// in production).
	pathsUserConfigDir = os.UserConfigDir
	// pathsMkdirAll creates the base directory (os.MkdirAll in production).
	pathsMkdirAll = os.MkdirAll
	// pathsAndroidFilesDir / pathsAndroidModelsBase resolve the Android
	// storage anchors (JNI bridge in production); injected in tests so a
	// single desktop binary drives the android branches.
	pathsAndroidFilesDir   = androidFilesDir
	pathsAndroidModelsBase = androidModelsBase
)

// The app-data base resolves once per process; pathsBase stays empty whenever
// the platform keeps cwd-relative paths (Windows) or resolution failed
// (non-Windows fallback to the legacy cwd-relative behavior).
var (
	pathsOnce sync.Once
	pathsBase string
)

// appDataDir returns the per-OS app-data base directory, or "" when paths
// must stay cwd-relative: always on Windows; on other platforms only when
// os.UserConfigDir fails or the base directory cannot be created (best-effort
// — the failure is [WARN]-logged once and the legacy cwd-relative layout
// applies, matching pre-app-data releases).
func appDataDir() string {
	pathsOnce.Do(func() {
		if pathsGOOS == "windows" {
			return
		}
		if pathsGOOS == "android" {
			// The Android host provides neither $HOME nor $XDG_CONFIG_HOME
			// (os.UserConfigDir always fails there) and the process cwd is
			// the read-only filesystem root, so the base comes from the JNI
			// bridge's files dir unconditionally (see androidpath_android.go).
			pathsBase = pathsAndroidFilesDir()
			if pathsBase == "" {
				log.Println("[WARN] Android app files dir unavailable, keeping cwd-relative app paths")
			}
			return
		}
		root, err := pathsUserConfigDir()
		if err != nil {
			log.Printf("[WARN] Cannot resolve user config dir, keeping cwd-relative app paths: %v", err)
			return
		}
		if root == "" {
			log.Println("[WARN] User config dir is empty, keeping cwd-relative app paths")
			return
		}
		base := filepath.Join(root, appDataDirName)
		migrateAppDataDirName(base)
		if err := pathsMkdirAll(base, 0755); err != nil {
			log.Printf("[WARN] Cannot create app data dir %s, keeping cwd-relative app paths: %v", base, err)
			return
		}
		pathsBase = base
	})
	return pathsBase
}

// appDataDirName is the per-user app-data directory name under
// os.UserConfigDir() (non-Windows desktop platforms only).
const appDataDirName = "myllama"

// migrateAppDataDirName performs the one-time llama-desktop → myllama base
// directory rename on non-Windows desktop platforms: when the NEW base does
// not exist but the legacy one (same parent) does, the legacy directory is
// renamed wholesale (same parent = atomic), carrying every state file inside
// it to the new layout; the per-file name migrations then apply on top. When
// the rename fails, the legacy directory is left in place ([WARN]) and the
// new directory is created empty — caches and config regenerate, no data is
// destroyed. A no-op when the new base already exists or no legacy directory
// exists.
func migrateAppDataDirName(base string) {
	if _, err := os.Stat(base); err == nil {
		return
	}
	legacy := filepath.Join(filepath.Dir(base), legacyAppDataDirName)
	if fi, err := os.Stat(legacy); err != nil || !fi.IsDir() {
		return
	}
	if err := os.Rename(legacy, base); err != nil {
		log.Printf("[WARN] Cannot rename legacy app data dir %s -> %s, starting fresh: %v", legacy, base, err)
		return
	}
	log.Printf("[OK] Renamed legacy app data dir %s -> %s", legacy, base)
}

// resolveStateFile resolves a state-file (or directory) name to its active
// location: under the app-data base on non-Windows desktop platforms, or the
// bare cwd-relative name on Windows / fallback.
func resolveStateFile(name string) string {
	if base := appDataDir(); base != "" {
		return filepath.Join(base, name)
	}
	return name
}

// resolveTempDir returns the directory for large transient download files.
// os.TempDir everywhere except Android: the app process has no TMPDIR, so
// os.TempDir falls back to the read-only /tmp — use the app-private files
// dir (base/tmp, created on demand) instead.
func resolveTempDir() string {
	if pathsGOOS == "android" {
		if base := pathsAndroidFilesDir(); base != "" {
			dir := filepath.Join(base, "tmp")
			if err := pathsMkdirAll(dir, 0755); err == nil {
				return dir
			}
		}
	}
	return os.TempDir()
}

// resolveServerLogPath resolves the server log file: explicit absolute paths
// (tests, handover adoption) pass through unchanged, bare names resolve under
// the app-data base like every other state file.
func resolveServerLogPath() string {
	if filepath.IsAbs(serverLogFile) {
		return serverLogFile
	}
	return resolveStateFile(serverLogFile)
}
