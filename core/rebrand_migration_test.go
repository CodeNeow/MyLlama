package core

import (
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// ─── State-file family rename (llama-desktop-* → myllama-*) ──────
//
// Tests for the one-time legacy-name migrations introduced with the MyLlama
// rebrand: config file chain, bench cache, handover record, docs cache dir
// and the non-Windows app-data base directory.

// withCwdRelativePaths forces the Windows branch of the path resolution
// (app-data base stays empty, state-file names stay bare cwd-relative) so
// tests that write legacy fixtures into the temp cwd behave identically on
// every host.
func withCwdRelativePaths(t *testing.T) {
	withPathsSeams(t, "windows", "", nil, nil)
}

// TestLoadConfigMigratesLegacyDesktopConfig verifies the newest link of the
// config era chain: a llama-desktop-config.json in the cwd is renamed onto
// the active myllama-config.json path and loaded (theme survives); the
// legacy file is consumed by the rename.
func TestLoadConfigMigratesLegacyDesktopConfig(t *testing.T) {
	withTempCwd(t)
	saveConfigState(t)

	legacyData := []byte(`{"theme":"dark","language":"zh"}`)
	if err := os.WriteFile(legacyConfigFileName, legacyData, 0644); err != nil {
		t.Fatal(err)
	}
	loadConfig()

	configMu.Lock()
	theme := currentTheme
	configMu.Unlock()
	if theme != "dark" {
		t.Errorf("after migration theme=dark should be loaded, got %q", theme)
	}
	newData, err := os.ReadFile(configFile)
	if err != nil {
		t.Fatalf("active config should exist after the rename migration: %v", err)
	}
	if string(newData) != string(legacyData) {
		t.Errorf("active config content = %q, want the legacy bytes %q", newData, legacyData)
	}
	if _, err := os.Stat(legacyConfigFileName); !os.IsNotExist(err) {
		t.Errorf("legacy config should be consumed by the rename, stat err = %v", err)
	}
}

// TestBenchCacheMigratesLegacyFile verifies the bench cache rename: a legacy
// llama-desktop-benchcache.json with a matching fingerprint is renamed to the
// new name on first load and the value is served as a cache hit (no
// re-measurement).
func TestBenchCacheMigratesLegacyFile(t *testing.T) {
	tmp := withTempCwd(t)
	withCwdRelativePaths(t)
	// Un-pin the withTempCwd override so the cache path resolves to its bare
	// default (cwd-relative on the test host) and the legacy migration applies.
	benchCacheFile = benchCacheFileName
	hw := stubBenchHW()
	fp := hardwareFingerprint(hw)
	legacyPayload, err := json.Marshal(benchCachePayload{
		Version: benchCacheVersion, Fingerprint: fp, RAMGBs: 42.5, MeasuredAt: time.Now(),
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(tmp, legacyBenchCacheFileName), legacyPayload, 0644); err != nil {
		t.Fatal(err)
	}

	bw, ok := loadBenchCache(fp)
	if !ok || bw != 42.5 {
		t.Fatalf("loadBenchCache after legacy migration = (%v, %v), want (42.5, true)", bw, ok)
	}
	if _, err := os.Stat(filepath.Join(tmp, legacyBenchCacheFileName)); !os.IsNotExist(err) {
		t.Errorf("legacy cache file should be consumed by the rename, stat err = %v", err)
	}
	if _, err := os.Stat(filepath.Join(tmp, benchCacheFileName)); err != nil {
		t.Errorf("new cache file should exist after the rename, stat err = %v", err)
	}
}

// TestReadHandoverFallsBackToLegacyName verifies the one-version handover
// transition: a record written under the pre-rebrand name is read when the
// new-name record is absent, and removeHandover clears BOTH names so a
// legacy record cannot resurface after the successor deletes the new-name
// record.
func TestReadHandoverFallsBackToLegacyName(t *testing.T) {
	tmp := withTempCwd(t)
	withCwdRelativePaths(t)
	// Un-pin the withTempCwd override so both names resolve to bare cwd paths.
	handoverFile = handoverFileName

	legacyRec, err := json.Marshal(handoverRecord{Pid: 4242, Port: 8080, StartedAt: time.Now().Format(time.RFC3339)})
	if err != nil {
		t.Fatal(err)
	}
	legacyPath := filepath.Join(tmp, legacyHandoverFileName)
	if err := os.WriteFile(legacyPath, legacyRec, 0644); err != nil {
		t.Fatal(err)
	}

	rec, err := readHandover()
	if err != nil {
		t.Fatalf("readHandover should fall back to the legacy name: %v", err)
	}
	if rec.Pid != 4242 || rec.Port != 8080 {
		t.Errorf("legacy record loaded = (pid %d, port %d), want (4242, 8080)", rec.Pid, rec.Port)
	}
	// Read-only transition: the legacy file is not renamed on read.
	if _, err := os.Stat(legacyPath); err != nil {
		t.Errorf("legacy record must stay in place after the fallback read, stat err = %v", err)
	}

	// Both names present → removeHandover clears both (no resurrection).
	newRec, err := json.Marshal(handoverRecord{Pid: 7, Port: 8081, StartedAt: time.Now().Format(time.RFC3339)})
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(tmp, handoverFileName), newRec, 0644); err != nil {
		t.Fatal(err)
	}
	if err := removeHandover(); err != nil {
		t.Fatalf("removeHandover: %v", err)
	}
	for _, name := range []string{legacyHandoverFileName, handoverFileName} {
		if _, err := os.Stat(filepath.Join(tmp, name)); !os.IsNotExist(err) {
			t.Errorf("%s should be removed, stat err = %v", name, err)
		}
	}
	if _, err := readHandover(); !errors.Is(err, fs.ErrNotExist) {
		t.Errorf("readHandover after removal = (%v, %v), want fs.ErrNotExist", rec, err)
	}
}

// TestMigrateDocsCacheDirRename verifies the docs cache directory rename and
// its degradations: rename when the legacy dir exists and the new one does
// not; keep the legacy dir untouched when the new dir already exists; no-op
// when neither exists.
func TestMigrateDocsCacheDirRename(t *testing.T) {
	t.Run("renames legacy dir", func(t *testing.T) {
		tmp := withTempCwd(t)
		withCwdRelativePaths(t)
		docsCacheDir = docsCacheDirName // un-pin: resolve to the bare cwd name
		legacy := filepath.Join(tmp, legacyDocsCacheDirName)
		if err := os.MkdirAll(legacy, 0755); err != nil {
			t.Fatal(err)
		}
		content := []byte("# cached section")
		if err := os.WriteFile(filepath.Join(legacy, "en-quickstart.md"), content, 0644); err != nil {
			t.Fatal(err)
		}
		migrateLegacyDocsCacheDir()
		data, err := os.ReadFile(filepath.Join(tmp, docsCacheDirName, "en-quickstart.md"))
		if err != nil {
			t.Fatalf("cached section missing in the new dir: %v", err)
		}
		if string(data) != string(content) {
			t.Errorf("migrated content = %q, want %q", data, content)
		}
		if _, err := os.Stat(legacy); !os.IsNotExist(err) {
			t.Errorf("legacy docs cache dir should be consumed by the rename, stat err = %v", err)
		}
	})

	t.Run("keeps legacy dir when new dir exists", func(t *testing.T) {
		tmp := withTempCwd(t)
		withCwdRelativePaths(t)
		docsCacheDir = docsCacheDirName
		if err := os.MkdirAll(filepath.Join(tmp, docsCacheDirName), 0755); err != nil {
			t.Fatal(err)
		}
		legacy := filepath.Join(tmp, legacyDocsCacheDirName)
		if err := os.MkdirAll(legacy, 0755); err != nil {
			t.Fatal(err)
		}
		migrateLegacyDocsCacheDir()
		if _, err := os.Stat(legacy); err != nil {
			t.Errorf("legacy dir must stay when the new dir exists, stat err = %v", err)
		}
	})

	t.Run("no legacy dir is a no-op", func(t *testing.T) {
		tmp := withTempCwd(t)
		withCwdRelativePaths(t)
		docsCacheDir = docsCacheDirName
		migrateLegacyDocsCacheDir()
		if _, err := os.Stat(filepath.Join(tmp, docsCacheDirName)); !os.IsNotExist(err) {
			t.Errorf("migration must not create the cache dir itself, stat err = %v", err)
		}
	})
}

// TestMigrateAppDataDirRename verifies the non-Windows app-data base
// rename: a legacy <UserConfigDir>/llama-desktop directory is renamed
// wholesale to <UserConfigDir>/myllama on first resolution (contents ride
// along); an already-existing new base leaves the legacy dir untouched.
func TestMigrateAppDataDirRename(t *testing.T) {
	t.Run("renames legacy base", func(t *testing.T) {
		root := t.TempDir()
		withPathsSeams(t, "linux", root, nil, nil)
		legacy := filepath.Join(root, legacyAppDataDirName)
		if err := os.MkdirAll(legacy, 0755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(legacy, configFileName), []byte(`{"theme":"dark"}`), 0644); err != nil {
			t.Fatal(err)
		}
		wantBase := filepath.Join(root, appDataDirName)
		if got := appDataDir(); got != wantBase {
			t.Fatalf("appDataDir = %q, want %q", got, wantBase)
		}
		if _, err := os.Stat(filepath.Join(wantBase, configFileName)); err != nil {
			t.Errorf("state file should ride along with the base rename, stat err = %v", err)
		}
		if _, err := os.Stat(legacy); !os.IsNotExist(err) {
			t.Errorf("legacy base should be consumed by the rename, stat err = %v", err)
		}
	})

	t.Run("keeps legacy base when new base exists", func(t *testing.T) {
		root := t.TempDir()
		withPathsSeams(t, "linux", root, nil, nil)
		if err := os.MkdirAll(filepath.Join(root, appDataDirName), 0755); err != nil {
			t.Fatal(err)
		}
		legacy := filepath.Join(root, legacyAppDataDirName)
		if err := os.MkdirAll(legacy, 0755); err != nil {
			t.Fatal(err)
		}
		if got := appDataDir(); got != filepath.Join(root, appDataDirName) {
			t.Fatalf("appDataDir = %q, want the existing new base", got)
		}
		if _, err := os.Stat(legacy); err != nil {
			t.Errorf("legacy base must stay when the new base exists, stat err = %v", err)
		}
	})
}

// ─── rewriteLegacyPaths (pure path rewrite) ──────────────────────

// absTempStyleDir returns a portable absolute directory (resolved against the
// current working directory via filepath.Abs) so path-rewrite assertions
// behave the same on Windows and Unix.
func absTempStyleDir(elems ...string) string {
	abs, err := filepath.Abs(filepath.Join(elems...))
	if err != nil {
		panic(err)
	}
	return abs
}

// TestRewriteLegacyPaths is the table test for the pure rewrite: top-level
// directory fields and per-model MMProj under the legacy prefix are
// rewritten (case-insensitive, separator-bounded); everything else is
// structurally untouched.
func TestRewriteLegacyPaths(t *testing.T) {
	legacy := absTempStyleDir("legacy-root", "llama-desktop", "llama desktop")
	newDir := absTempStyleDir("new-root", "MyLlama")

	t.Run("top-level and per-model fields rewritten", func(t *testing.T) {
		cfg := appConfig{
			LlamaCppDir:         filepath.Join(legacy, "llama-cpp"),
			ModelDir:            filepath.Join(legacy, "LLM-Models"),
			LlamaCppDownloadDir: filepath.Join(legacy, "dl"),
			ModelDownloadDir:    filepath.Join(legacy, "LLM-Models", "downloads"),
			Theme:               "dark",
			ModelConfigs: map[string]ModelConfig{
				"m1": {Threads: 8, MMProj: filepath.Join(legacy, "mmproj.gguf")},
			},
			ServerConfig: ServerConfig{Port: 8080, AccessMode: accessLocal},
		}
		if !rewriteLegacyPaths(&cfg, legacy, newDir) {
			t.Fatal("rewrite should report a change")
		}
		checks := []struct{ name, got, want string }{
			{"llamaCppDir", cfg.LlamaCppDir, filepath.Join(newDir, "llama-cpp")},
			{"modelDir", cfg.ModelDir, filepath.Join(newDir, "LLM-Models")},
			{"llamaCppDownloadDir", cfg.LlamaCppDownloadDir, filepath.Join(newDir, "dl")},
			{"modelDownloadDir", cfg.ModelDownloadDir, filepath.Join(newDir, "LLM-Models", "downloads")},
			{"mmproj", cfg.ModelConfigs["m1"].MMProj, filepath.Join(newDir, "mmproj.gguf")},
		}
		for _, c := range checks {
			if c.got != c.want {
				t.Errorf("%s = %q, want %q", c.name, c.got, c.want)
			}
		}
		// Non-path state must be structurally untouched.
		if cfg.Theme != "dark" || cfg.ServerConfig.Port != 8080 || cfg.ModelConfigs["m1"].Threads != 8 {
			t.Errorf("non-path fields changed: %+v", cfg)
		}
	})

	t.Run("case-insensitive prefix", func(t *testing.T) {
		upper := appConfig{ModelDir: strings.ToUpper(filepath.Join(legacy, "LLM-Models"))}
		if !rewriteLegacyPaths(&upper, legacy, newDir) {
			t.Fatal("case-insensitive match should rewrite")
		}
		// The stored path's own case is preserved in the rewritten suffix.
		if upper.ModelDir != filepath.Join(newDir, "LLM-MODELS") {
			t.Errorf("rewritten modelDir = %q, want %q", upper.ModelDir, filepath.Join(newDir, "LLM-MODELS"))
		}
	})

	t.Run("path equal to the legacy dir maps to the new dir", func(t *testing.T) {
		equal := appConfig{ModelDir: legacy}
		if !rewriteLegacyPaths(&equal, legacy, newDir) {
			t.Fatal("exact-prefix path should rewrite")
		}
		if equal.ModelDir != newDir {
			t.Errorf("modelDir = %q, want %q", equal.ModelDir, newDir)
		}
	})

	t.Run("non-prefix paths untouched", func(t *testing.T) {
		other := absTempStyleDir("prog", "llama-desktop2")
		cfg := appConfig{
			ModelDir:    filepath.Join(other, "LLM-Models"),        // sibling sharing a leading substring
			LlamaCppDir: absTempStyleDir("elsewhere", "llama-cpp"), // different tree
			ModelConfigs: map[string]ModelConfig{
				"m1": {MMProj: "relative/mmproj.gguf"}, // relative path
			},
		}
		if rewriteLegacyPaths(&cfg, legacy, newDir) {
			t.Errorf("no field should match the legacy prefix, got %+v", cfg)
		}
		if cfg.ModelDir != filepath.Join(other, "LLM-Models") ||
			cfg.LlamaCppDir != absTempStyleDir("elsewhere", "llama-cpp") ||
			cfg.ModelConfigs["m1"].MMProj != "relative/mmproj.gguf" {
			t.Errorf("non-prefix fields were modified: %+v", cfg)
		}
	})

	t.Run("empty inputs are a no-op", func(t *testing.T) {
		cfg := appConfig{ModelDir: filepath.Join(legacy, "LLM-Models")}
		if rewriteLegacyPaths(&cfg, "", newDir) || rewriteLegacyPaths(&cfg, legacy, "") {
			t.Error("empty legacy/new dir must be a no-op")
		}
	})
}

// ─── Installer-planted legacy-path marker ────────────────────────

// TestApplyLegacyPathMarker verifies the consumption of the Windows
// installer's migration-legacy-path.txt: matching paths are rewritten,
// persisted and the marker is deleted; an empty marker, a marker naming the
// current directory, or one with no matching paths is consumed with no
// rewrite.
func TestApplyLegacyPathMarker(t *testing.T) {
	t.Run("rewrites and persists", func(t *testing.T) {
		tmp := withTempCwd(t)
		withCwdRelativePaths(t)
		saveConfigState(t)
		legacy := t.TempDir() // a real dir distinct from the cwd
		cfgJSON := fmt.Sprintf(`{"llamaCppDir":%q,"modelDir":%q,"theme":"dark","modelConfigs":{"m1":{"threads":4,"mmproj":%q}}}`,
			filepath.Join(legacy, "llama-cpp"), filepath.Join(legacy, "LLM-Models"), filepath.Join(legacy, "mmproj.gguf"))
		if err := os.WriteFile(configFile, []byte(cfgJSON), 0644); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(legacyPathMarkerName, []byte(legacy+"\r\n"), 0644); err != nil {
			t.Fatal(err)
		}

		loadConfig()

		if _, err := os.Stat(legacyPathMarkerName); !os.IsNotExist(err) {
			t.Fatalf("marker should be consumed, stat err = %v", err)
		}
		customLlamaCppMu.Lock()
		gotLlamaDir := customLlamaCppDir
		customLlamaCppMu.Unlock()
		if want := filepath.Join(tmp, "llama-cpp"); gotLlamaDir != want {
			t.Errorf("in-memory llamaCppDir = %q, want %q", gotLlamaDir, want)
		}
		// The rewrite is persisted to the active config file.
		persisted, err := os.ReadFile(configFile)
		if err != nil {
			t.Fatal(err)
		}
		var reloaded appConfig
		if err := json.Unmarshal(persisted, &reloaded); err != nil {
			t.Fatal(err)
		}
		if reloaded.ModelDir != filepath.Join(tmp, "LLM-Models") {
			t.Errorf("persisted modelDir = %q, want %q", reloaded.ModelDir, filepath.Join(tmp, "LLM-Models"))
		}
		if got := reloaded.ModelConfigs["m1"].MMProj; got != filepath.Join(tmp, "mmproj.gguf") {
			t.Errorf("persisted mmproj = %q, want %q", got, filepath.Join(tmp, "mmproj.gguf"))
		}
	})

	t.Run("marker naming the cwd is consumed without rewrite", func(t *testing.T) {
		withTempCwd(t)
		withCwdRelativePaths(t)
		saveConfigState(t)
		cwd, err := os.Getwd()
		if err != nil {
			t.Fatal(err)
		}
		cfgJSON := fmt.Sprintf(`{"llamaCppDir":%q}`, filepath.Join(cwd, "llama-cpp"))
		if err := os.WriteFile(configFile, []byte(cfgJSON), 0644); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(legacyPathMarkerName, []byte(cwd), 0644); err != nil {
			t.Fatal(err)
		}
		loadConfig()
		if _, err := os.Stat(legacyPathMarkerName); !os.IsNotExist(err) {
			t.Errorf("marker should be consumed, stat err = %v", err)
		}
		customLlamaCppMu.Lock()
		got := customLlamaCppDir
		customLlamaCppMu.Unlock()
		if want := filepath.Join(cwd, "llama-cpp"); got != want {
			t.Errorf("llamaCppDir changed by a self-referencing marker: got %q, want %q", got, want)
		}
	})

	t.Run("marker with no matching paths is consumed without rewrite", func(t *testing.T) {
		withTempCwd(t)
		withCwdRelativePaths(t)
		saveConfigState(t)
		cfgJSON := `{"llamaCppDir":"relative-llama-cpp","theme":"dark"}`
		if err := os.WriteFile(configFile, []byte(cfgJSON), 0644); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(legacyPathMarkerName, []byte(absTempStyleDir("some-other-install")), 0644); err != nil {
			t.Fatal(err)
		}
		loadConfig()
		if _, err := os.Stat(legacyPathMarkerName); !os.IsNotExist(err) {
			t.Errorf("marker should be consumed, stat err = %v", err)
		}
		got, err := os.ReadFile(configFile)
		if err != nil {
			t.Fatal(err)
		}
		if string(got) != cfgJSON {
			t.Errorf("config without matching paths must stay untouched, got %q", got)
		}
	})

	t.Run("empty marker is consumed", func(t *testing.T) {
		withTempCwd(t)
		withCwdRelativePaths(t)
		saveConfigState(t)
		if err := os.WriteFile(legacyPathMarkerName, []byte("  \r\n"), 0644); err != nil {
			t.Fatal(err)
		}
		loadConfig() // must not panic
		if _, err := os.Stat(legacyPathMarkerName); !os.IsNotExist(err) {
			t.Errorf("empty marker should be consumed, stat err = %v", err)
		}
	})

	t.Run("missing config file still consumes the marker", func(t *testing.T) {
		withTempCwd(t)
		withCwdRelativePaths(t)
		saveConfigState(t)
		if err := os.WriteFile(legacyPathMarkerName, []byte(absTempStyleDir("legacy-install")), 0644); err != nil {
			t.Fatal(err)
		}
		loadConfig() // config file absent → marker removed, defaults apply
		if _, err := os.Stat(legacyPathMarkerName); !os.IsNotExist(err) {
			t.Errorf("marker should be consumed even without a config file, stat err = %v", err)
		}
	})
}
