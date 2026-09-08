package core

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// withTempCwd switches to a temp directory and restores the original working directory
// after the test. loadConfig/saveConfig read the state-file paths relative to the
// working directory on Windows, so tests need an isolated working directory.
// The state-file path vars are additionally pinned to explicit paths inside the
// temp directory so the tests are independent of the per-OS default resolution
// (on non-Windows the defaults resolve under the app-data base, see paths.go).
// The default llama.cpp / models directory vars are likewise pinned to their
// bare cwd-relative names: test fixtures and assertions live in the temp cwd,
// which matches the pre-app-data geometry the tests were written against.
func withTempCwd(t *testing.T) string {
	t.Helper()
	orig, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	tmp := t.TempDir()
	if err := os.Chdir(tmp); err != nil {
		t.Fatal(err)
	}
	oldConfig, oldHandover, oldBench, oldDocs := configFile, handoverFile, benchCacheFile, docsCacheDir
	configFile = filepath.Join(tmp, configFileName)
	handoverFile = filepath.Join(tmp, handoverFileName)
	benchCacheFile = filepath.Join(tmp, benchCacheFileName)
	docsCacheDir = filepath.Join(tmp, docsCacheDirName)
	oldLlamaDir, oldModelsDir := defaultLlamaCppDir, defaultModelsDir
	defaultLlamaCppDir = func() string { return llamaCppDirName }
	defaultModelsDir = func() string { return modelsDirName }
	t.Cleanup(func() {
		// Drain in-flight download goroutines FIRST, while cwd and the pinned
		// state-file paths (configFile etc.) still point inside this test's
		// temp dir: any trailing persistTasksNow write then lands in the
		// directory that is about to be removed, instead of leaking into the
		// real repo/user paths after the vars below are restored. The drain
		// must also precede the framework's t.TempDir RemoveAll (cleanups run
		// LIFO: TempDir registered first), so after it returns the RemoveAll
		// can no longer race a write. Bounded and non-fatal: a WaitGroup
		// counter of 0 returns immediately, so tests that never spawn download
		// goroutines pay no cost.
		waitDlGoroutinesForTestBounded(t, 5*time.Second)
		configFile, handoverFile, benchCacheFile, docsCacheDir = oldConfig, oldHandover, oldBench, oldDocs
		defaultLlamaCppDir, defaultModelsDir = oldLlamaDir, oldModelsDir
		if err := os.Chdir(orig); err != nil {
			t.Errorf("failed to restore working directory: %v", err)
		}
	})
	return tmp
}

// saveConfigState snapshots all config-related global state and restores it after the
// test, preventing cross-test pollution.
func saveConfigState(t *testing.T) (origModels map[string]ModelConfig, origServer ServerConfig, origTheme string, origDir string, origModelsDir string) {
	t.Helper()
	modelConfigsMu.Lock()
	origModels = make(map[string]ModelConfig, len(cachedModelConfigs))
	for k, v := range cachedModelConfigs {
		origModels[k] = v
	}
	modelConfigsMu.Unlock()
	serverConfigMu.Lock()
	origServer = cachedServerConfig
	serverConfigMu.Unlock()
	configMu.Lock()
	origTheme = currentTheme
	origTray := trayEnabled
	origSidebarCollapsed := currentSidebarCollapsed
	origOnboardingDismissed := currentOnboardingDismissed
	origApiRouteMode := apiRouteMode
	configMu.Unlock()
	customLlamaCppMu.Lock()
	origDir = customLlamaCppDir
	customLlamaCppMu.Unlock()
	modelsDirMu.Lock()
	origModelsDir = customModelsDir
	modelsDirMu.Unlock()
	llamaCppDownloadDirMu.Lock()
	origLlamaDownloadDir := llamaCppDownloadDirOverride
	llamaCppDownloadDirMu.Unlock()
	modelDownloadDirMu.Lock()
	origModelDownloadDir := modelDownloadDirOverride
	modelDownloadDirMu.Unlock()
	languageMu.Lock()
	origLanguage := currentLanguage
	languageMu.Unlock()
	t.Cleanup(func() {
		modelConfigsMu.Lock()
		cachedModelConfigs = origModels
		modelConfigsMu.Unlock()
		serverConfigMu.Lock()
		cachedServerConfig = origServer
		serverConfigMu.Unlock()
		configMu.Lock()
		currentTheme = origTheme
		trayEnabled = origTray
		currentSidebarCollapsed = origSidebarCollapsed
		currentOnboardingDismissed = origOnboardingDismissed
		apiRouteMode = origApiRouteMode
		configMu.Unlock()
		customLlamaCppMu.Lock()
		customLlamaCppDir = origDir
		customLlamaCppMu.Unlock()
		modelsDirMu.Lock()
		customModelsDir = origModelsDir
		modelsDirMu.Unlock()
		llamaCppDownloadDirMu.Lock()
		llamaCppDownloadDirOverride = origLlamaDownloadDir
		llamaCppDownloadDirMu.Unlock()
		modelDownloadDirMu.Lock()
		modelDownloadDirOverride = origModelDownloadDir
		modelDownloadDirMu.Unlock()
		languageMu.Lock()
		currentLanguage = origLanguage
		languageMu.Unlock()
	})
	return
}

// TestSaveLoadConfigRoundTrip verifies saveConfig writes and loadConfig reads back
// consistently.
func TestSaveLoadConfigRoundTrip(t *testing.T) {
	tmp := withTempCwd(t)
	saveConfigState(t)

	// Custom model directory must actually exist for loadConfig to accept it (see loadConfig validation).
	modelsDirPath := filepath.Join(tmp, "custom-models")
	if err := os.MkdirAll(modelsDirPath, 0755); err != nil {
		t.Fatal(err)
	}

	// write
	modelConfigsMu.Lock()
	cachedModelConfigs = map[string]ModelConfig{
		"qwen": {Threads: 8, GPULayers: "99", CtxSize: 8192, FlashAttn: true},
	}
	modelConfigsMu.Unlock()
	serverConfigMu.Lock()
	cachedServerConfig = ServerConfig{AccessMode: accessLAN, Host: "0.0.0.0", Port: 9000, MaxModels: 2, CacheRAM: 4096, APIKey: "sk-test"}
	serverConfigMu.Unlock()
	configMu.Lock()
	currentTheme = "light"
	configMu.Unlock()
	customLlamaCppMu.Lock()
	customLlamaCppDir = "D:\\llama-cpp"
	customLlamaCppMu.Unlock()
	modelsDirMu.Lock()
	customModelsDir = modelsDirPath
	modelsDirMu.Unlock()
	languageMu.Lock()
	currentLanguage = "en"
	languageMu.Unlock()
	configMu.Lock()
	trayEnabled = false
	configMu.Unlock()
	saveConfig()

	if _, err := os.Stat(configFile); err != nil {
		t.Fatalf("saveConfig did not create config file: %v", err)
	}

	// clear globals, simulate fresh start
	modelConfigsMu.Lock()
	cachedModelConfigs = map[string]ModelConfig{}
	modelConfigsMu.Unlock()
	serverConfigMu.Lock()
	cachedServerConfig = ServerConfig{}
	serverConfigMu.Unlock()
	configMu.Lock()
	currentTheme = ""
	configMu.Unlock()
	customLlamaCppMu.Lock()
	customLlamaCppDir = ""
	customLlamaCppMu.Unlock()
	modelsDirMu.Lock()
	customModelsDir = ""
	modelsDirMu.Unlock()
	languageMu.Lock()
	currentLanguage = ""
	languageMu.Unlock()
	configMu.Lock()
	trayEnabled = true // matches package default; loadConfig should read back false (explicit disable was persisted)
	configMu.Unlock()

	loadConfig()

	modelConfigsMu.Lock()
	got := cachedModelConfigs["qwen"]
	modelConfigsMu.Unlock()
	if got.Threads != 8 || got.CtxSize != 8192 || !got.FlashAttn {
		t.Errorf("model config round-trip failed: %+v", got)
	}
	serverConfigMu.Lock()
	scfg := cachedServerConfig
	serverConfigMu.Unlock()
	if scfg.AccessMode != accessLAN || scfg.Host != "0.0.0.0" || scfg.Port != 9000 || scfg.MaxModels != 2 {
		t.Errorf("server config round-trip failed (accessMode and derived host must match): %+v", scfg)
	}
	// optional API key round-trips through persistence (empty would silently
	// disable authentication on restart)
	if scfg.APIKey != "sk-test" {
		t.Errorf("server config apiKey round-trip failed: %q, want %q", scfg.APIKey, "sk-test")
	}
	configMu.Lock()
	if currentTheme != "light" {
		t.Errorf("theme round-trip failed: %q", currentTheme)
	}
	configMu.Unlock()
	customLlamaCppMu.Lock()
	if customLlamaCppDir != "D:\\llama-cpp" {
		t.Errorf("custom llama.cpp dir round-trip failed: %q", customLlamaCppDir)
	}
	customLlamaCppMu.Unlock()
	modelsDirMu.Lock()
	if customModelsDir != modelsDirPath {
		t.Errorf("custom models dir round-trip failed: %q, want %q", customModelsDir, modelsDirPath)
	}
	modelsDirMu.Unlock()
	languageMu.Lock()
	if currentLanguage != "en" {
		t.Errorf("language preference round-trip failed: %q, want %q", currentLanguage, "en")
	}
	languageMu.Unlock()
	// trayEnabled=false (non-default) round-trip: saveConfig writes back, loadConfig restores false
	configMu.Lock()
	if trayEnabled != false {
		t.Errorf("trayEnabled round-trip failed: %v, want false (non-default value must persist)", trayEnabled)
	}
	configMu.Unlock()
}

// TestLoadConfigDefaults verifies default-value fallback when config is missing or partial,
// preventing crashes from stale data.
func TestLoadConfigDefaults(t *testing.T) {
	withTempCwd(t)
	saveConfigState(t)

	// partial config with only host
	if err := os.WriteFile(configFile, []byte(`{"serverConfig":{"host":"127.0.0.1"}}`), 0644); err != nil {
		t.Fatal(err)
	}
	loadConfig()

	serverConfigMu.Lock()
	scfg := cachedServerConfig
	serverConfigMu.Unlock()
	if scfg.Port != 8080 || scfg.MaxModels != 1 || scfg.CacheRAM != 8192 {
		t.Errorf("partial config should fall back to default port/model-count/cache: %+v", scfg)
	}
	// old config has no accessMode field: fallback to local and host derived as 127.0.0.1
	if scfg.AccessMode != accessLocal || scfg.Host != "127.0.0.1" {
		t.Errorf("missing accessMode should fall back to local with host=127.0.0.1, got %+v", scfg)
	}
	// old config has no apiKey field: zero value "" = authentication disabled (current behavior)
	if scfg.APIKey != "" {
		t.Errorf("missing apiKey should load as empty (no authentication), got %q", scfg.APIKey)
	}
	configMu.Lock()
	if currentTheme != "light" {
		t.Errorf("missing theme should fall back to light, got %q", currentTheme)
	}
	configMu.Unlock()
	modelConfigsMu.Lock()
	if cachedModelConfigs == nil {
		t.Error("nil model configs map should be initialized to empty map")
	}
	modelConfigsMu.Unlock()
	languageMu.Lock()
	if currentLanguage != "auto" {
		t.Errorf("missing language field should fall back to auto, got %q", currentLanguage)
	}
	languageMu.Unlock()
	// old config has no trayEnabled field: fallback to true (preserving 4aacac2 unconditional tray behavior)
	configMu.Lock()
	if trayEnabled != true {
		t.Errorf("missing trayEnabled field should fall back to true, got %v", trayEnabled)
	}
	configMu.Unlock()
}

// TestLoadConfigTrayEnabledExplicitFalse verifies an explicitly-written trayEnabled=false
// is not overwritten by the default-value fallback (missing fields must be distinguished
// from explicit disable).
func TestLoadConfigTrayEnabledExplicitFalse(t *testing.T) {
	withTempCwd(t)
	saveConfigState(t)

	if err := os.WriteFile(configFile, []byte(`{"trayEnabled":false}`), 0644); err != nil {
		t.Fatal(err)
	}
	loadConfig()
	configMu.Lock()
	if trayEnabled != false {
		t.Errorf("explicit trayEnabled=false must be preserved, got %v", trayEnabled)
	}
	configMu.Unlock()
}

// TestSetTrayEnabledPersists verifies SetTrayEnabled round-trip persistence: after setting
// false, saveConfig writes back and loadConfig restores false; explicit disable is
// distinguished from missing-field default true.
// Does not actually start systray (Windows InitTray/QuitTray is unreliable in test processes
// without a window message loop); only asserts config state-machine behavior.
func TestSetTrayEnabledPersists(t *testing.T) {
	withTempCwd(t)
	saveConfigState(t)

	app := &App{}
	if err := app.SetTrayEnabled(false); err != nil {
		t.Fatal(err)
	}
	configMu.Lock()
	if trayEnabled != false {
		t.Errorf("SetTrayEnabled(false) left trayEnabled = %v, want false", trayEnabled)
	}
	configMu.Unlock()

	// restart simulation: read config file back
	configMu.Lock()
	trayEnabled = true
	configMu.Unlock()
	loadConfig()
	configMu.Lock()
	if trayEnabled != false {
		t.Errorf("after round-trip trayEnabled = %v, want false (explicit disable must persist)", trayEnabled)
	}
	configMu.Unlock()
}

// TestConfigSidebarCollapsedRoundtrip verifies sidebar-collapsed preference round-trip
// survives losslessly:
// config file explicitly writes "sidebarCollapsed": true → loadConfig reads it → saveConfig
// writes it back (load→save chain does not drop fields); then writes false and asserts
// false is preserved.
// Like trayEnabled: loadConfig pre-fills default true (sidebar defaults to collapsed),
// distinguishing "old config missing field" (fallback collapsed) from "explicitly set to false" (keep expanded).
func TestConfigSidebarCollapsedRoundtrip(t *testing.T) {
	withTempCwd(t)
	saveConfigState(t)

	// write config with "sidebarCollapsed": true
	if err := os.WriteFile(configFile, []byte(`{"sidebarCollapsed":true}`), 0644); err != nil {
		t.Fatal(err)
	}
	loadConfig()
	configMu.Lock()
	if currentSidebarCollapsed != true {
		t.Errorf("after loadConfig currentSidebarCollapsed = %v, want true", currentSidebarCollapsed)
	}
	configMu.Unlock()

	saveConfig()
	data, err := os.ReadFile(configFile)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(data), `"sidebarCollapsed": true`) {
		t.Errorf("after saveConfig file should retain sidebarCollapsed: true, actual: %s", data)
	}

	// false scenario: explicit false round-trip preserves false (not promoted to true by zero-value fallback)
	if err := os.WriteFile(configFile, []byte(`{"sidebarCollapsed":false}`), 0644); err != nil {
		t.Fatal(err)
	}
	loadConfig()
	configMu.Lock()
	currentSidebarCollapsed = true // set non-default first to verify loadConfig reads back explicit false
	configMu.Unlock()
	loadConfig()
	configMu.Lock()
	if currentSidebarCollapsed != false {
		t.Errorf("after explicit-false round-trip currentSidebarCollapsed = %v, want false", currentSidebarCollapsed)
	}
	configMu.Unlock()

	// missing-field old config: loadConfig pre-fills default true (collapsed), same pattern as trayEnabled
	if err := os.WriteFile(configFile, []byte(`{"theme":"light"}`), 0644); err != nil {
		t.Fatal(err)
	}
	configMu.Lock()
	currentSidebarCollapsed = false // set non-default first to verify missing-field fallback recovers collapsed
	configMu.Unlock()
	loadConfig()
	configMu.Lock()
	if currentSidebarCollapsed != true {
		t.Errorf("old config missing sidebarCollapsed should fall back to true (collapsed), got %v", currentSidebarCollapsed)
	}
	configMu.Unlock()
}

// TestConfigOnboardingDismissedRoundtrip verifies the quick-start checklist
// preference round-trips losslessly:
// config file explicitly writes "onboardingDismissed": true → loadConfig reads it →
// saveConfig writes it back (load→save chain does not drop fields).
// Unlike trayEnabled/sidebarCollapsed, false is the intended default (checklist
// visible), so the Go zero value already covers "old config missing field" — no
// pre-population needed; the test asserts the missing-field case falls back to false.
func TestConfigOnboardingDismissedRoundtrip(t *testing.T) {
	withTempCwd(t)
	saveConfigState(t)

	// explicit true round-trip: load reads true, save retains it in the file
	if err := os.WriteFile(configFile, []byte(`{"onboardingDismissed":true}`), 0644); err != nil {
		t.Fatal(err)
	}
	loadConfig()
	configMu.Lock()
	if currentOnboardingDismissed != true {
		t.Errorf("after loadConfig currentOnboardingDismissed = %v, want true", currentOnboardingDismissed)
	}
	configMu.Unlock()

	saveConfig()
	data, err := os.ReadFile(configFile)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(data), `"onboardingDismissed": true`) {
		t.Errorf("after saveConfig file should retain onboardingDismissed: true, actual: %s", data)
	}

	// missing-field old config: zero-value fallback false (checklist visible again),
	// verified from a non-default state so a stale global cannot pass vacuously
	if err := os.WriteFile(configFile, []byte(`{"theme":"light"}`), 0644); err != nil {
		t.Fatal(err)
	}
	configMu.Lock()
	currentOnboardingDismissed = true // set non-default first to verify missing-field fallback resets it
	configMu.Unlock()
	loadConfig()
	configMu.Lock()
	if currentOnboardingDismissed != false {
		t.Errorf("old config missing onboardingDismissed should fall back to false (visible), got %v", currentOnboardingDismissed)
	}
	configMu.Unlock()
}

// TestLoadConfigAccessModeFallback verifies old-config host values are no longer trusted:
// without an accessMode field, fallback to local and host is force-derived as 127.0.0.1;
// even if the old config wrote a previously-disallowed value like 0.0.0.0, the service
// will not be silently exposed to the LAN.
func TestLoadConfigAccessModeFallback(t *testing.T) {
	withTempCwd(t)
	saveConfigState(t)

	if err := os.WriteFile(configFile, []byte(`{"serverConfig":{"host":"0.0.0.0"}}`), 0644); err != nil {
		t.Fatal(err)
	}
	loadConfig()

	serverConfigMu.Lock()
	scfg := cachedServerConfig
	serverConfigMu.Unlock()
	if scfg.AccessMode != accessLocal || scfg.Host != "127.0.0.1" {
		t.Errorf("old illegal host should fall back to local with host=127.0.0.1, got %+v", scfg)
	}
}

// TestLoadConfigAccessModeLAN verifies that when accessMode=lan is configured, loadConfig
// preserves lan and derives host as 0.0.0.0.
func TestLoadConfigAccessModeLAN(t *testing.T) {
	withTempCwd(t)
	saveConfigState(t)

	if err := os.WriteFile(configFile, []byte(`{"serverConfig":{"accessMode":"lan","host":"127.0.0.1"}}`), 0644); err != nil {
		t.Fatal(err)
	}
	loadConfig()

	serverConfigMu.Lock()
	scfg := cachedServerConfig
	serverConfigMu.Unlock()
	if scfg.AccessMode != accessLAN || scfg.Host != "0.0.0.0" {
		t.Errorf("accessMode=lan should be preserved with host=0.0.0.0, got %+v", scfg)
	}
}

// TestLoadConfigLanguageFallback verifies the language preference whitelist fallback:
// missing fields and illegal values both fall back to auto; only zh/en/auto are kept
// (same policy as downloadSource).
func TestLoadConfigLanguageFallback(t *testing.T) {
	withTempCwd(t)
	saveConfigState(t)

	// missing language field → auto (already covered by TestLoadConfigDefaults; here we cover illegal values)
	if err := os.WriteFile(configFile, []byte(`{"language":"fr"}`), 0644); err != nil {
		t.Fatal(err)
	}
	loadConfig()
	languageMu.Lock()
	if currentLanguage != "auto" {
		t.Errorf("illegal language 'fr' should fall back to auto, got %q", currentLanguage)
	}
	languageMu.Unlock()

	// valid values preserved
	if err := os.WriteFile(configFile, []byte(`{"language":"zh"}`), 0644); err != nil {
		t.Fatal(err)
	}
	loadConfig()
	languageMu.Lock()
	if currentLanguage != "zh" {
		t.Errorf("valid language 'zh' should be kept, got %q", currentLanguage)
	}
	languageMu.Unlock()
}

// TestLoadConfigMissingFile verifies the config file missing case returns silently (first-launch scenario).
func TestLoadConfigMissingFile(t *testing.T) {
	withTempCwd(t)
	saveConfigState(t)
	loadConfig() // must not panic
}

// TestLoadConfigMigratesLegacyFile verifies the llama-gui → myllama rename
// migration (the oldest link of the era chain): when the new file does not
// exist but the old one does, loadConfig renames the old file to the new name
// and then loads it (user settings like theme survive). The source is
// consumed by the rename; a rename failure degrades to a copy that keeps the
// source in place (see migrateConfigFile). Migration is skipped when the new
// file already exists, preventing old-file content from overwriting the new
// config.
func TestLoadConfigMigratesLegacyFile(t *testing.T) {
	withTempCwd(t)
	saveConfigState(t)

	legacyData := []byte(`{"theme":"dark"}`)
	if err := os.WriteFile(legacyConfigFile, legacyData, 0644); err != nil {
		t.Fatal(err)
	}
	loadConfig()

	configMu.Lock()
	theme := currentTheme
	configMu.Unlock()
	if theme != "dark" {
		t.Errorf("after migration old config theme=dark should be loaded, got %q", theme)
	}
	newData, err := os.ReadFile(configFile)
	if err != nil {
		t.Fatalf("new config file should exist after migration: %v", err)
	}
	if string(newData) != string(legacyData) {
		t.Errorf("new config file content should be byte-identical to old file, got %q", newData)
	}
	// behavior invariant: the legacy source is consumed by the rename (the
	// one-time era migration), so only the new-name file remains.
	if _, err := os.Stat(legacyConfigFile); !os.IsNotExist(err) {
		t.Errorf("legacy file should be gone after the rename migration, stat err = %v", err)
	}

	// when new file already exists, it is loaded preferentially; old file stays untouched
	if err := os.WriteFile(configFile, []byte(`{"theme":"light"}`), 0644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(legacyConfigFile, []byte(`{"theme":"dark"}`), 0644); err != nil {
		t.Fatal(err)
	}
	loadConfig()
	configMu.Lock()
	theme = currentTheme
	configMu.Unlock()
	if theme != "light" {
		t.Errorf("new file should be loaded when it exists, expected theme=light, got %q", theme)
	}
}

// TestLoadConfigMigratesMLockNoMMap verifies legacy mlock/noMmap fields in old-format configs
// are migrated to load-mode at loadConfig time (mlock/no-mmap DEPRECATED since b10342);
// compatibility fields are zeroed immediately after, preventing old boolean values from
// being written into the new format.
func TestLoadConfigMigratesMLockNoMMap(t *testing.T) {
	withTempCwd(t)
	saveConfigState(t)

	cases := []struct {
		name       string
		configJSON string
		want       string
	}{
		{"mlock only", `{"modelConfigs":{"m1":{"threads":4,"mlock":true,"noMmap":false}}}`, "mlock"},
		{"mlock and noMmap", `{"modelConfigs":{"m1":{"threads":4,"mlock":true,"noMmap":true}}}`, "mlock"}, // mlock takes priority
		{"noMmap only", `{"modelConfigs":{"m1":{"threads":4,"mlock":false,"noMmap":true}}}`, "none"},
		{"neither", `{"modelConfigs":{"m1":{"threads":4,"mlock":false,"noMmap":false}}}`, ""}, // no legacy fields → keep default
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if err := os.WriteFile(configFile, []byte(c.configJSON), 0644); err != nil {
				t.Fatal(err)
			}
			loadConfig()
			modelConfigsMu.Lock()
			got := cachedModelConfigs["m1"]
			modelConfigsMu.Unlock()
			if got.LoadMode != c.want {
				t.Errorf("LoadMode = %q, want %q (config %s)", got.LoadMode, c.want, c.configJSON)
			}
			if got.MLock || got.NoMMap {
				t.Errorf("compatibility fields must be zeroed after migration, MLock=%v NoMMap=%v", got.MLock, got.NoMMap)
			}
		})
	}

	// new-format config (already has loadMode) must not be overwritten by migration
	writeConfig := `{"modelConfigs":{"m1":{"threads":4,"loadMode":"dio"}}}`
	if err := os.WriteFile(configFile, []byte(writeConfig), 0644); err != nil {
		t.Fatal(err)
	}
	loadConfig()
	modelConfigsMu.Lock()
	got := cachedModelConfigs["m1"]
	modelConfigsMu.Unlock()
	if got.LoadMode != "dio" {
		t.Errorf("new-format loadMode=dio must be preserved, got %q", got.LoadMode)
	}
}

// TestSaveServerConfigRejectsInvalidAccessMode verifies SaveServerConfig rejects access
// scopes outside the whitelist (local/lan) (#5). Allowing arbitrary host values would expose
// the inference service to the LAN/public via llama-server; rejection must happen before
// persisting; the rejection branch must not alter the already-stored cachedServerConfig.
func TestSaveServerConfigRejectsInvalidAccessMode(t *testing.T) {
	withTempCwd(t)
	saveConfigState(t)

	app := &App{}
	if err := app.SaveServerConfig(ServerConfig{AccessMode: "wan", Host: "0.0.0.0", Port: 8080, MaxModels: 1, CacheRAM: 0}); err == nil {
		t.Error("AccessMode=wan should return error")
	}
	if err := app.SaveServerConfig(ServerConfig{AccessMode: "192.168.1.10", Host: "0.0.0.0", Port: 8080, MaxModels: 1, CacheRAM: 0}); err == nil {
		t.Error("illegal LAN-address-style value should return error")
	}
	if err := app.SaveServerConfig(ServerConfig{AccessMode: "", Host: "0.0.0.0", Port: 8080, MaxModels: 1, CacheRAM: 0}); err == nil {
		t.Error("empty AccessMode should return error")
	}
	// rejection branch must not alter the stored config
	serverConfigMu.Lock()
	got := cachedServerConfig
	serverConfigMu.Unlock()
	if got.Host != "127.0.0.1" || got.AccessMode != accessLocal {
		t.Errorf("illegal AccessMode must not overwrite config, current = %+v", got)
	}
}

// TestSaveServerConfigDerivesHostFromAccessMode verifies that after SaveServerConfig succeeds,
// Host is force-derived from AccessMode: lan → 0.0.0.0, local → 127.0.0.1, ignoring the
// host value supplied by the frontend.
func TestSaveServerConfigDerivesHostFromAccessMode(t *testing.T) {
	withTempCwd(t)
	saveConfigState(t)

	app := &App{}
	if err := app.SaveServerConfig(ServerConfig{AccessMode: accessLAN, Host: "1.2.3.4", Port: 8080, MaxModels: 2, CacheRAM: 4096}); err != nil {
		t.Fatalf("AccessMode=lan should be accepted: %v", err)
	}
	serverConfigMu.Lock()
	got := cachedServerConfig
	serverConfigMu.Unlock()
	if got.AccessMode != accessLAN || got.Host != "0.0.0.0" {
		t.Errorf("after saving lan, Host should be derived as 0.0.0.0, got %+v", got)
	}

	if err := app.SaveServerConfig(ServerConfig{AccessMode: accessLocal, Host: "0.0.0.0", Port: 8080, MaxModels: 1, CacheRAM: 0}); err != nil {
		t.Fatalf("AccessMode=local should be accepted: %v", err)
	}
	serverConfigMu.Lock()
	got = cachedServerConfig
	serverConfigMu.Unlock()
	if got.AccessMode != accessLocal || got.Host != "127.0.0.1" {
		t.Errorf("after saving local, Host should be derived as 127.0.0.1, got %+v", got)
	}
}

// TestSaveServerConfigRejectsInvalidPort verifies the port must be within 1024-65535,
// avoiding privileged ports and out-of-range values (#5).
func TestSaveServerConfigRejectsInvalidPort(t *testing.T) {
	withTempCwd(t)
	saveConfigState(t)

	app := &App{}
	if err := app.SaveServerConfig(ServerConfig{AccessMode: accessLocal, Host: "127.0.0.1", Port: 80, MaxModels: 1, CacheRAM: 0}); err == nil {
		t.Error("Port=80 (privileged) should return error")
	}
	if err := app.SaveServerConfig(ServerConfig{AccessMode: accessLocal, Host: "127.0.0.1", Port: 99999, MaxModels: 1, CacheRAM: 0}); err == nil {
		t.Error("Port=99999 is out of range and should return error")
	}
}

// TestSaveServerConfigRejectsInvalidNumbers verifies MaxModels >= 1 and CacheRAM >= 0 (#5).
func TestSaveServerConfigRejectsInvalidNumbers(t *testing.T) {
	withTempCwd(t)
	saveConfigState(t)

	app := &App{}
	if err := app.SaveServerConfig(ServerConfig{AccessMode: accessLocal, Host: "127.0.0.1", Port: 8080, MaxModels: 0, CacheRAM: 0}); err == nil {
		t.Error("MaxModels=0 should return error")
	}
	if err := app.SaveServerConfig(ServerConfig{AccessMode: accessLocal, Host: "127.0.0.1", Port: 8080, MaxModels: 1, CacheRAM: -1}); err == nil {
		t.Error("negative CacheRAM should return error")
	}
}

// TestSaveServerConfigAcceptsLocalAccessMode verifies a valid access scope is accepted and
// written to the global cache and config file: in local mode, Host is always derived as
// 127.0.0.1 (regardless of the host value passed by the frontend, e.g. localhost/::1 are
// all normalized to 127.0.0.1).
func TestSaveServerConfigAcceptsLocalAccessMode(t *testing.T) {
	withTempCwd(t)
	saveConfigState(t)

	app := &App{}
	if err := app.SaveServerConfig(ServerConfig{AccessMode: accessLocal, Host: "localhost", Port: 8080, MaxModels: 2, CacheRAM: 4096}); err != nil {
		t.Fatalf("AccessMode=local should be accepted: %v", err)
	}
	serverConfigMu.Lock()
	got := cachedServerConfig
	serverConfigMu.Unlock()
	if got.AccessMode != accessLocal || got.Host != "127.0.0.1" || got.Port != 8080 || got.MaxModels != 2 || got.CacheRAM != 4096 {
		t.Errorf("AccessMode=local config not written correctly (host should be normalized to 127.0.0.1): %+v", got)
	}
}

// TestSaveModelConfigRejectsInjection verifies SaveModelConfig rejects string fields
// containing newlines (#9). Such values would be written verbatim into INI during preset
// generation, constituting a config injection.
// Rejection branch must not write to config cache; valid values write normally (control group).
func TestSaveModelConfigRejectsInjection(t *testing.T) {
	withTempCwd(t)
	saveConfigState(t)

	app := &App{}
	badGPU := "99\n[evil]\nmodel=/tmp/x"
	if err := app.SaveModelConfig("m1", ModelConfig{GPULayers: badGPU}); err == nil {
		t.Error("GPULayers with newline should return error")
	}
	if err := app.SaveModelConfig("m1", ModelConfig{CacheTypeK: "q8_0\nfoo"}); err == nil {
		t.Error("CacheTypeK with newline should return error")
	}
	// rejection branch must not write to config cache
	modelConfigsMu.Lock()
	_, ok := cachedModelConfigs["m1"]
	modelConfigsMu.Unlock()
	if ok {
		t.Error("rejected config must not be written to cache")
	}

	// valid CacheTypeK should be accepted and written
	if err := app.SaveModelConfig("m1-ok", ModelConfig{CacheTypeK: "q4_0"}); err != nil {
		t.Errorf("valid CacheTypeK should be accepted: %v", err)
	}
	modelConfigsMu.Lock()
	got, ok := cachedModelConfigs["m1-ok"]
	modelConfigsMu.Unlock()
	if !ok || got.CacheTypeK != "q4_0" {
		t.Error("valid config was not written to cache")
	}
}

// TestSaveModelConfigRejectsInvalidWhitelist verifies GPULayers / CacheType values outside
// the whitelist are rejected (#9 first layer).
func TestSaveModelConfigRejectsInvalidWhitelist(t *testing.T) {
	withTempCwd(t)
	saveConfigState(t)

	app := &App{}
	if err := app.SaveModelConfig("m2", ModelConfig{GPULayers: "-1"}); err == nil {
		t.Error("GPULayers=-1 should return error")
	}
	if err := app.SaveModelConfig("m2", ModelConfig{GPULayers: "1.5"}); err == nil {
		t.Error("GPULayers=1.5 should return error")
	}
	if err := app.SaveModelConfig("m2", ModelConfig{CacheTypeV: "q4_2"}); err == nil {
		t.Error("CacheTypeV=q4_2 outside whitelist should return error")
	}
}

// TestEffectiveModelDownloadDir verifies the model download path: defaults to
// LLM-Models; returns the configured download path when one is set.
func TestEffectiveModelDownloadDir(t *testing.T) {
	saveConfigState(t)

	modelDownloadDirMu.Lock()
	modelDownloadDirOverride = ""
	modelDownloadDirMu.Unlock()
	if got := effectiveModelDownloadDir(); got != defaultModelsDir() {
		t.Errorf("default download dir = %q, want %q", got, defaultModelsDir())
	}

	custom := t.TempDir()
	modelDownloadDirMu.Lock()
	modelDownloadDirOverride = custom
	modelDownloadDirMu.Unlock()
	if got := effectiveModelDownloadDir(); got != custom {
		t.Errorf("download dir after setting custom = %q, want %q", got, custom)
	}
}

// TestSaveLoadConfigDownloadDirsRoundTrip verifies the two new download-path
// fields persist through saveConfig / loadConfig: a non-empty value round-trips
// losslessly, and an old config missing both fields falls back to defaults
// (empty overrides → llama-cpp/ and LLM-Models).
func TestSaveLoadConfigDownloadDirsRoundTrip(t *testing.T) {
	withTempCwd(t)
	saveConfigState(t)

	llamaPath := filepath.Join(t.TempDir(), "llama-custom")
	modelPath := filepath.Join(t.TempDir(), "models-custom")
	llamaCppDownloadDirMu.Lock()
	llamaCppDownloadDirOverride = llamaPath
	llamaCppDownloadDirMu.Unlock()
	modelDownloadDirMu.Lock()
	modelDownloadDirOverride = modelPath
	modelDownloadDirMu.Unlock()

	saveConfig()
	data, err := os.ReadFile(configFile)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(data), `"llamaCppDownloadDir": `) || !strings.Contains(string(data), `"modelDownloadDir": `) {
		t.Errorf("saved config should contain both download dir fields, actual: %s", data)
	}

	llamaCppDownloadDirMu.Lock()
	llamaCppDownloadDirOverride = ""
	llamaCppDownloadDirMu.Unlock()
	modelDownloadDirMu.Lock()
	modelDownloadDirOverride = ""
	modelDownloadDirMu.Unlock()

	loadConfig()
	llamaCppDownloadDirMu.Lock()
	gotLlama := llamaCppDownloadDirOverride
	llamaCppDownloadDirMu.Unlock()
	modelDownloadDirMu.Lock()
	gotModel := modelDownloadDirOverride
	modelDownloadDirMu.Unlock()
	if gotLlama != llamaPath {
		t.Errorf("after load llamaCppDownloadDirOverride = %q, want %q", gotLlama, llamaPath)
	}
	if gotModel != modelPath {
		t.Errorf("after load modelDownloadDirOverride = %q, want %q", gotModel, modelPath)
	}

	// Old config without the new fields must not populate them: start from an
	// empty override, load the legacy-shaped config, and both stay empty
	// (the effective functions then fall back to llama-cpp/ and LLM-Models).
	llamaCppDownloadDirMu.Lock()
	llamaCppDownloadDirOverride = ""
	llamaCppDownloadDirMu.Unlock()
	modelDownloadDirMu.Lock()
	modelDownloadDirOverride = ""
	modelDownloadDirMu.Unlock()
	if err := os.WriteFile(configFile, []byte(`{"theme":"light"}`), 0644); err != nil {
		t.Fatal(err)
	}
	loadConfig()
	llamaCppDownloadDirMu.Lock()
	gotLlama = llamaCppDownloadDirOverride
	llamaCppDownloadDirMu.Unlock()
	modelDownloadDirMu.Lock()
	gotModel = modelDownloadDirOverride
	modelDownloadDirMu.Unlock()
	if gotLlama != "" {
		t.Errorf("old config should leave llamaCppDownloadDirOverride empty (default llama-cpp/), got %q", gotLlama)
	}
	if gotModel != "" {
		t.Errorf("old config should leave modelDownloadDirOverride empty (default LLM-Models), got %q", gotModel)
	}
}

// TestLoadConfigIgnoresInvalidModelDir verifies that when modelDir in the config points to
// a non-existent directory or a plain file, loadConfig ignores it (logs WARN and falls back
// to default), leaving customModelsDir empty.
func TestLoadConfigIgnoresInvalidModelDir(t *testing.T) {
	withTempCwd(t)
	saveConfigState(t)
	modelsDirMu.Lock()
	customModelsDir = ""
	modelsDirMu.Unlock()

	// writeConfig uses json.Marshal to encode the path, avoiding Windows backslash JSON escape issues.
	writeConfig := func(modelDir string) {
		t.Helper()
		cfg := appConfig{ModelDir: modelDir}
		data, err := json.Marshal(cfg)
		if err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(configFile, data, 0644); err != nil {
			t.Fatal(err)
		}
	}

	// modelDir points to a non-existent directory
	writeConfig(filepath.Join(t.TempDir(), "does-not-exist"))
	loadConfig()
	modelsDirMu.Lock()
	if customModelsDir != "" {
		t.Errorf("non-existent modelDir should be ignored, customModelsDir = %q", customModelsDir)
	}
	modelsDirMu.Unlock()

	// modelDir points to a plain file (not a directory)
	filePath := filepath.Join(t.TempDir(), "plain-file")
	if err := os.WriteFile(filePath, []byte("x"), 0644); err != nil {
		t.Fatal(err)
	}
	writeConfig(filePath)
	loadConfig()
	modelsDirMu.Lock()
	if customModelsDir != "" {
		t.Errorf("plain-file modelDir should be ignored, customModelsDir = %q", customModelsDir)
	}
	modelsDirMu.Unlock()
}

// TestSetModelsDir verifies SetModelsDir: illegal input (empty string / non-existent path /
// plain file) returns an error without rewriting customModelsDir; a valid directory is written
// successfully and invalidates the model cache.
func TestSetModelsDir(t *testing.T) {
	withTempCwd(t)
	saveConfigState(t)
	saveModelsState(t)
	app := &App{}

	// prime cache to a valid state so SetModelsDir invalidation is assertable
	modelsMu.Lock()
	modelsCacheValid.Store(true)
	modelsMu.Unlock()

	// empty string
	if err := app.SetModelsDir(""); err == nil {
		t.Error("empty string should return error")
	}
	// non-existent path
	if err := app.SetModelsDir(filepath.Join(t.TempDir(), "missing")); err == nil {
		t.Error("non-existent path should return error")
	}
	// plain file
	filePath := filepath.Join(t.TempDir(), "a.txt")
	if err := os.WriteFile(filePath, []byte("x"), 0644); err != nil {
		t.Fatal(err)
	}
	if err := app.SetModelsDir(filePath); err == nil {
		t.Error("plain file should return error")
	}
	// illegal input must not rewrite state
	modelsDirMu.Lock()
	if customModelsDir != "" {
		t.Errorf("illegal input must not rewrite customModelsDir, got %q", customModelsDir)
	}
	modelsDirMu.Unlock()

	// valid directory
	valid := t.TempDir()
	if err := app.SetModelsDir(valid); err != nil {
		t.Fatalf("valid directory should be written successfully: %v", err)
	}
	modelsDirMu.Lock()
	if customModelsDir != valid {
		t.Errorf("customModelsDir = %q, want %q", customModelsDir, valid)
	}
	modelsDirMu.Unlock()
	if modelsCacheValid.Load() {
		t.Error("model cache must be invalidated after successful SetModelsDir")
	}
}

// ─── downloadSource persistence ────────────────────────────────────────

// TestLoadConfigDownloadSourceDefault verifies the download source falls back to hf when
// the old config lacks a downloadSource field (#12: backward compatibility, no error or
// empty value for missing fields).
func TestLoadConfigDownloadSourceDefault(t *testing.T) {
	withTempCwd(t)
	saveConfigState(t)

	if err := os.WriteFile(configFile, []byte(`{"serverConfig":{"host":"127.0.0.1"}}`), 0644); err != nil {
		t.Fatal(err)
	}
	loadConfig()

	if got := activeDownloadSource(); got != sourceHF {
		t.Errorf("old config without downloadSource should fall back to hf, got %q", got)
	}
}

// TestSetDownloadSourcePersist verifies SetDownloadSource valid-value write and round-trip:
// after setting modelscope, activeDownloadSource takes effect immediately; after
// saveConfig + loadConfig it is still modelscope (non-default value survives restart).
func TestSetDownloadSourcePersist(t *testing.T) {
	withTempCwd(t)
	saveConfigState(t)

	app := &App{}
	if err := app.SetDownloadSource(sourceModelScope); err != nil {
		t.Fatal(err)
	}
	if got := activeDownloadSource(); got != sourceModelScope {
		t.Errorf("after set, activeDownloadSource = %q, want modelscope", got)
	}

	// restart simulation: read config file back
	downloadSourceMu.Lock()
	downloadSource = sourceHF
	downloadSourceMu.Unlock()
	loadConfig()
	if got := activeDownloadSource(); got != sourceModelScope {
		t.Errorf("after round-trip activeDownloadSource = %q, want modelscope", got)
	}
}

// TestSetDownloadSourceRejectsInvalid verifies SetDownloadSource rejects values outside
// the whitelist and does not overwrite current state (illegal values return Chinese error).
func TestSetDownloadSourceRejectsInvalid(t *testing.T) {
	withTempCwd(t)
	saveConfigState(t)

	downloadSourceMu.Lock()
	downloadSource = sourceHF
	downloadSourceMu.Unlock()

	app := &App{}
	if err := app.SetDownloadSource("github"); err == nil {
		t.Error("illegal download source 'github' should return error")
	}
	if err := app.SetDownloadSource(""); err == nil {
		t.Error("empty download source should return error")
	}
	if got := activeDownloadSource(); got != sourceHF {
		t.Errorf("illegal value must not change download source, got %q", got)
	}
}

// ─── download task queue persistence ───────────────────────────────────

// TestDownloadTasksPersistRoundTrip verifies download task queue saveConfig/loadConfig round-trip:
// all fields (ID/ModelID/FileName/DestDir/Source/Status/Progress/Total/Downloaded/SizeHuman/Error)
// stay consistent for terminal tasks (done); runtime fields (URL/ctx/cancel/resumeCh) are
// not persisted, URL is rebuilt on restore.
func TestDownloadTasksPersistRoundTrip(t *testing.T) {
	withTempCwd(t)
	saveConfigState(t)

	dlTasksMu.Lock()
	dlTasks = []*DlTask{
		{ID: "dl-1", ModelID: "author/model", FileName: "a.gguf", DestDir: "D:/models/author/model", Source: "hf", Status: "done", Progress: 100, Total: 100, Downloaded: 100, SizeHuman: "100 B"},
		{ID: "dl-2", ModelID: "author/model2", FileName: "b.gguf", DestDir: "D:/models/author/model2", Source: "modelscope", Status: "paused", Progress: 50, Total: 200, Downloaded: 100, SizeHuman: "200 B", Error: "previously failed"},
	}
	dlTasksMu.Unlock()

	saveConfig()

	// clear globals, simulate fresh start
	dlTasksMu.Lock()
	dlTasks = nil
	dlTaskCounter = 0
	dlTasksMu.Unlock()

	loadConfig()

	dlTasksMu.Lock()
	restored := dlTasks
	dlTasksMu.Unlock()
	if len(restored) != 2 {
		t.Fatalf("restored task count = %d, want 2", len(restored))
	}
	got := restored[0]
	if got.ID != "dl-1" || got.ModelID != "author/model" || got.FileName != "a.gguf" ||
		got.DestDir != "D:/models/author/model" || got.Source != "hf" || got.Status != "done" ||
		got.Progress != 100 || got.Total != 100 || got.Downloaded != 100 || got.SizeHuman != "100 B" {
		t.Errorf("done task fields inconsistent after round-trip: %+v", got)
	}
	got2 := restored[1]
	if got2.Source != "modelscope" || got2.Status != "paused" || got2.Error != "previously failed" || got2.Downloaded != 100 {
		t.Errorf("paused task fields inconsistent after round-trip: %+v", got2)
	}
	// URL is rebuilt from Source at restore time (original URL is not persisted)
	wantURL, err := buildModelDownloadURL("hf", "author/model", "a.gguf")
	if err != nil {
		t.Fatal(err)
	}
	if restored[0].URL != wantURL {
		t.Errorf("URL should be rebuilt from source, got %q", restored[0].URL)
	}
}

// TestLoadConfigRestoresDownloadTasks verifies restore normalization (#12):
//   - downloading status (goroutine died after process exit) is normalized to paused;
//   - illegal/empty status is normalized to paused;
//   - empty Source falls back to hf;
//   - URL is rebuilt from source correctly.
func TestLoadConfigRestoresDownloadTasks(t *testing.T) {
	withTempCwd(t)
	saveConfigState(t)

	dlTasksMu.Lock()
	dlTasks = nil
	dlTaskCounter = 0
	dlTasksMu.Unlock()

	configJSON := `{
		"downloadTasks": [
			{"id":"dl-1","modelId":"author/model","fileName":"a.gguf","destDir":"D:/m/author/model","source":"hf","status":"downloading","progress":50,"total":100,"downloaded":50,"sizeHuman":"100 B"},
			{"id":"dl-2","modelId":"author/model","fileName":"b.gguf","destDir":"D:/m/author/model","source":"","status":"weird","progress":0},
			{"id":"dl-3","modelId":"author/model","fileName":"c.gguf","destDir":"D:/m/author/model","source":"modelscope","status":"queued","progress":0}
		]
	}`
	if err := os.WriteFile(configFile, []byte(configJSON), 0644); err != nil {
		t.Fatal(err)
	}
	loadConfig()

	dlTasksMu.Lock()
	defer dlTasksMu.Unlock()
	if len(dlTasks) != 3 {
		t.Fatalf("restored task count = %d, want 3", len(dlTasks))
	}
	// downloading → paused
	if dlTasks[0].Status != "paused" {
		t.Errorf("dl-1 downloading should be normalized to paused, got %q", dlTasks[0].Status)
	}
	if dlTasks[0].Source != "hf" {
		t.Errorf("dl-1 Source = %q, want hf", dlTasks[0].Source)
	}
	wantURL := hfMirrorBase + "/author/model/resolve/main/a.gguf"
	if dlTasks[0].URL != wantURL {
		t.Errorf("dl-1 URL rebuild = %q, want %q", dlTasks[0].URL, wantURL)
	}
	// illegal status + empty source → paused / hf
	if dlTasks[1].Status != "paused" {
		t.Errorf("dl-2 illegal status should be normalized to paused, got %q", dlTasks[1].Status)
	}
	if dlTasks[1].Source != sourceHF {
		t.Errorf("dl-2 empty Source should fall back to hf, got %q", dlTasks[1].Source)
	}
	// queued is preserved, modelscope URL rebuilt with default Legacy Base
	if dlTasks[2].Status != "queued" {
		t.Errorf("dl-3 queued should be preserved, got %q", dlTasks[2].Status)
	}
	if !strings.HasPrefix(dlTasks[2].URL, "https://modelscope.cn/api/v1/models/") {
		t.Errorf("dl-3 modelscope URL rebuild prefix wrong: %q", dlTasks[2].URL)
	}
}

// TestDownloadTaskCounterNoConflict verifies dlTaskCounter does not conflict with restored
// tasks after loadConfig: config contains dl-3 task, so counter must be >= 3 after loadConfig;
// a subsequently enqueued task has a unique id (dl-4) that does not overwrite existing tasks.
func TestDownloadTaskCounterNoConflict(t *testing.T) {
	withTempCwd(t)
	saveConfigState(t)
	restoreSource := withModelScope404Server(t)
	defer restoreSource()

	configJSON := `{"downloadTasks":[{"id":"dl-3","modelId":"author/model","fileName":"x.gguf","destDir":"D:/m","source":"hf","status":"paused"}]}`
	if err := os.WriteFile(configFile, []byte(configJSON), 0644); err != nil {
		t.Fatal(err)
	}
	dlTasksMu.Lock()
	dlTasks = nil
	dlTaskCounter = 0
	dlTasksMu.Unlock()
	loadConfig()

	dlTasksMu.Lock()
	restoredCounter := dlTaskCounter
	dlTasksMu.Unlock()
	if restoredCounter < 3 {
		t.Errorf("after restore dlTaskCounter = %d, want >= 3 (must not be less than max restored task sequence)", restoredCounter)
	}

	// enqueue via real path: id must be unique (dl-4)
	if err := startHFDownload("author/model", []string{"new.gguf"}); err != nil {
		t.Fatal(err)
	}
	dlTasksMu.Lock()
	var ids []string
	for _, tt := range dlTasks {
		ids = append(ids, tt.ID)
	}
	dlTasksMu.Unlock()
	seen := map[string]bool{}
	for _, id := range ids {
		if seen[id] {
			t.Errorf("duplicate task id: %v", ids)
		}
		seen[id] = true
	}
	if !seen["dl-4"] {
		t.Errorf("newly enqueued task id should be dl-4 (after restoring dl-3 counter=3), got ids=%v", ids)
	}
	if len(ids) != 2 {
		t.Errorf("total task count = %d, want 2 (dl-3 restored + dl-4 newly enqueued): %v", len(ids), ids)
	}

	// wait for new task goroutine to reach terminal state (404 fails fast), avoiding leakage;
	// restored dl-3 is paused with no goroutine and is not waited on.
	waitTaskTerminal(t, "dl-4", 5*time.Second)
}

// TestBuildModelDownloadURLHuggingFace verifies the huggingface source builds
// download URLs on the official Hugging Face host (huggingface.co), and that
// activeHFBase switches between the mirror and the official host by source.
func TestBuildModelDownloadURLHuggingFace(t *testing.T) {
	url, err := buildModelDownloadURL(sourceHuggingFace, "author/model", "a.gguf")
	if err != nil {
		t.Fatal(err)
	}
	want := hfDirectBase + "/author/model/resolve/main/a.gguf"
	if url != want {
		t.Errorf("huggingface download URL = %q, want %q", url, want)
	}

	prev := downloadSource
	defer func() { downloadSource = prev }()

	downloadSource = sourceHuggingFace
	if got := activeHFBase(); got != hfDirectBase {
		t.Errorf("activeHFBase() with huggingface source = %q, want %q", got, hfDirectBase)
	}
	downloadSource = sourceHF
	if got := activeHFBase(); got != hfMirrorBase {
		t.Errorf("activeHFBase() with hf source = %q, want %q", got, hfMirrorBase)
	}
}

// TestServerConfigDeviceIDRoundTrip verifies the serving-GPU selection
// (ServerConfig.DeviceID, a stable GPU UUID) persists through saveConfig /
// loadConfig, and that old configs missing the field load as "" (auto = CUDA
// default device), keeping backward compatibility without migration.
func TestServerConfigDeviceIDRoundTrip(t *testing.T) {
	withTempCwd(t)
	saveConfigState(t)

	const deviceID = "GPU-12345678-9abc-def0-1234-56789abcdef0"
	serverConfigMu.Lock()
	cachedServerConfig = ServerConfig{AccessMode: accessLocal, Host: "127.0.0.1", Port: 8080, MaxModels: 1, CacheRAM: 8192, DeviceID: deviceID}
	serverConfigMu.Unlock()
	saveConfig()

	// clear to the zero value, simulate fresh start
	serverConfigMu.Lock()
	cachedServerConfig = ServerConfig{}
	serverConfigMu.Unlock()
	loadConfig()

	serverConfigMu.Lock()
	scfg := cachedServerConfig
	serverConfigMu.Unlock()
	if scfg.DeviceID != deviceID {
		t.Errorf("deviceID round-trip failed: %q, want %q", scfg.DeviceID, deviceID)
	}

	// old config without the deviceId field: loads as "" (auto)
	if err := os.WriteFile(configFile, []byte(`{"serverConfig":{"port":8080}}`), 0644); err != nil {
		t.Fatal(err)
	}
	loadConfig()
	serverConfigMu.Lock()
	scfg = cachedServerConfig
	serverConfigMu.Unlock()
	if scfg.DeviceID != "" {
		t.Errorf("old config missing deviceId should load as empty (auto), got %q", scfg.DeviceID)
	}
}

// TestSaveServerConfigDeviceIDValidation verifies the serving-GPU selection
// allowlist: empty (auto) and a UUID from the current GPU probe list are
// accepted; unknown or case-mismatched values are rejected with an error and
// do not overwrite the stored config (preventing arbitrary
// CUDA_VISIBLE_DEVICES values from reaching the llama-server child env).
func TestSaveServerConfigDeviceIDValidation(t *testing.T) {
	withTempCwd(t)
	saveConfigState(t)

	origSource := gpuListSource
	gpuListSource = func() []GPUInfo {
		return []GPUInfo{
			{Name: "NVIDIA GeForce RTX 5070 Ti", UUID: "GPU-aaaaaaaa-bbbb-cccc-dddd-eeeeeeeeeeee", MemoryMB: 16302},
			{Name: "NVIDIA GeForce RTX 3070", UUID: "GPU-11111111-2222-3333-4444-555555555555", MemoryMB: 8192},
		}
	}
	t.Cleanup(func() { gpuListSource = origSource })

	app := &App{}
	// empty DeviceID (auto) is always accepted
	if err := app.SaveServerConfig(ServerConfig{AccessMode: accessLocal, Port: 8080, MaxModels: 1, CacheRAM: 0}); err != nil {
		t.Errorf("empty DeviceID should be accepted: %v", err)
	}
	// a known UUID (from the injected GPU list) is accepted
	known := ServerConfig{AccessMode: accessLocal, Port: 8080, MaxModels: 1, CacheRAM: 0, DeviceID: "GPU-11111111-2222-3333-4444-555555555555"}
	if err := app.SaveServerConfig(known); err != nil {
		t.Errorf("known GPU UUID should be accepted: %v", err)
	}

	// unknown UUID is rejected
	if err := app.SaveServerConfig(ServerConfig{AccessMode: accessLocal, Port: 8080, MaxModels: 1, CacheRAM: 0, DeviceID: "GPU-ffffffff-0000-0000-0000-000000000000"}); err == nil {
		t.Error("unknown GPU UUID should return error")
	}
	// case-sensitive exact match: lowercase variant of a known UUID is rejected
	if err := app.SaveServerConfig(ServerConfig{AccessMode: accessLocal, Port: 8080, MaxModels: 1, CacheRAM: 0, DeviceID: "gpu-11111111-2222-3333-4444-555555555555"}); err == nil {
		t.Error("case-mismatched GPU UUID should return error")
	}
	// rejections must not overwrite the stored config
	serverConfigMu.Lock()
	got := cachedServerConfig
	serverConfigMu.Unlock()
	if got.DeviceID != known.DeviceID {
		t.Errorf("rejected save must not overwrite stored DeviceID, got %q, want %q", got.DeviceID, known.DeviceID)
	}
}
