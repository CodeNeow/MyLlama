package core

import (
	"encoding/json"
	"log"
	"os"
	"strconv"
	"strings"
	"sync"
)

// ─── Config persistence ─────────────────────────────────────────
// Persisted app config (llama-desktop-config.json): schema types, load/save with
// legacy migration, and the guarded in-memory app-state variables.

// modelDownloadDirOverride is the user-chosen download path for new model
// downloads (empty means unset, use the default modelsDir). Distinct from
// customModelsDir (the imported existing model directory): downloads land in
// the download path, and the model list merges both sources.
var modelDownloadDirOverride string
var modelDownloadDirMu sync.Mutex

const (
	sourceHF              = "hf"
	sourceHuggingFace     = "huggingface"
	sourceModelScope      = "modelscope"
	defaultDownloadSource = sourceHF
)

// downloadSource is the current model download source (hf / modelscope);
// downloadSourceMu guards its reads/writes, consistent with the style of
// customLlamaCppDir and other config entries. Search, file listing, description,
// and download URL construction all route on the current activeDownloadSource().
var downloadSource = defaultDownloadSource
var downloadSourceMu sync.Mutex

// activeDownloadSource returns the currently active download source, read under the lock.
func activeDownloadSource() string {
	downloadSourceMu.Lock()
	s := downloadSource
	downloadSourceMu.Unlock()
	return s
}

func defaultModelConfig() ModelConfig {
	return ModelConfig{
		Threads: -1, GPULayers: "auto",
		CtxSize: 4096, BatchSize: 2048, UBatchSize: 512,
	}
}

// customModelsDir is the imported model directory (empty means unset). It is
// the directory of models the user already has and wants to reuse; distinct
// from modelDownloadDirOverride, where new downloads land. modelsDirMu guards
// its reads/writes, consistent with the style of customLlamaCppMu guarding
// customLlamaCppDir.
var customModelsDir string
var modelsDirMu sync.Mutex

// effectiveModelDownloadDir returns the directory new model downloads land in:
// the user-chosen download path when configured, otherwise the default
// modelsDir.
func effectiveModelDownloadDir() string {
	modelDownloadDirMu.Lock()
	dir := modelDownloadDirOverride
	modelDownloadDirMu.Unlock()
	if dir != "" {
		return dir
	}
	return defaultModelsDir()
}

// configFile is the config persistence path override: the bare default means
// "resolve via configFilePath" (cwd-relative on Windows, under the app-data
// base on other platforms, see paths.go); tests assign an explicit path to
// pin the location.
var configFile = configFileName

// configFilePath resolves the active config persistence path: an explicit
// configFile override wins, otherwise the per-OS default applies.
func configFilePath() string {
	if configFile != configFileName {
		return configFile
	}
	return resolveStateFile(configFileName)
}

// legacyConfigFile is the config filename from before the llama-gui →
// llama-desktop rename. It serves only as a one-shot migration source (see
// migrateLegacyConfig): when the new file does not exist but the old one does,
// it is renamed wholesale and reused, preserving theme / directories / model
// params / download queue for existing users losslessly.
var legacyConfigFile = "llama-gui-config.json"

// renameFile is a test injection point (same style as configFile), used to
// simulate the branch where renaming the temp file after download fails (#10).
var renameFile = os.Rename

type appConfig struct {
	LlamaCppDir         string                 `json:"llamaCppDir"`
	ModelDir            string                 `json:"modelDir"`
	LlamaCppDownloadDir string                 `json:"llamaCppDownloadDir,omitempty"`
	ModelDownloadDir    string                 `json:"modelDownloadDir,omitempty"`
	Theme               string                 `json:"theme"`
	ModelConfigs        map[string]ModelConfig `json:"modelConfigs"`
	ServerConfig        ServerConfig           `json:"serverConfig"`
	DownloadSource      string                 `json:"downloadSource"`
	Language            string                 `json:"language"`         // language preference: zh / en / auto (empty or invalid falls back to auto)
	TrayEnabled         bool                   `json:"trayEnabled"`      // Windows/macOS system tray toggle, default true
	SidebarCollapsed    bool                   `json:"sidebarCollapsed"` // sidebar collapsed state, default true (collapsed)
	// OnboardingDismissed records that the user closed (or auto-completed) the
	// Home page quick-start checklist. False is the Go zero value, so old
	// configs missing the field fall back to false (checklist shown) naturally.
	OnboardingDismissed bool `json:"onboardingDismissed"`
	// ApiRouteMode is the API-route (headless) mode toggle, default false:
	// when true, the next app start skips the GUI and runs as tray +
	// llama-server only (Windows; see core/headless.go). False is the Go zero
	// value, so old configs missing the field fall back to false naturally.
	ApiRouteMode  bool              `json:"apiRouteMode"`
	DownloadTasks []PersistedDlTask `json:"downloadTasks,omitempty"`
}

// Service access scope values: local means reachable only from this machine
// (listen on 127.0.0.1), lan means reachable from devices on the same network
// (listen on 0.0.0.0). ServerConfig.AccessMode accepts only these two values;
// anything else (including empty) falls back to local.
const accessLocal = "local"
const accessLAN = "lan"

type ServerConfig struct {
	// AccessMode is the service access scope ("local" | "lan", default
	// "local"); Host is the derived actual listen address per AccessMode and
	// never takes direct user input.
	AccessMode string `json:"accessMode"`
	Host       string `json:"host"`
	Port       int    `json:"port"`
	MaxModels  int    `json:"maxModels"`
	CacheRAM   int    `json:"cacheRam"`
	// APIKey is the optional llama-server --api-key bearer token; empty means
	// no authentication (default, current behavior).
	APIKey string `json:"apiKey"`
	// DeviceID is the serving-GPU selection: the stable nvidia-smi UUID of the
	// GPU llama-server is pinned to via CUDA_VISIBLE_DEVICES (see
	// bridge.cudaDeviceEnv). Empty means auto (CUDA default device order, the
	// historical behavior). Non-empty values are validated against the current
	// GPU probe list in SaveServerConfig; old configs missing the field load
	// as "" (auto) naturally.
	DeviceID string `json:"deviceId"`
}

type ModelConfig struct {
	Threads       int     `json:"threads"`
	GPULayers     string  `json:"gpuLayers"`
	CtxSize       int     `json:"ctxSize"`
	BatchSize     int     `json:"batchSize"`
	UBatchSize    int     `json:"ubatchSize"`
	FlashAttn     bool    `json:"flashAttn"`
	CacheTypeK    string  `json:"cacheTypeK"`
	CacheTypeV    string  `json:"cacheTypeV"`
	LoadMode      string  `json:"loadMode"`      // "", none, mmap, mlock, mmap+mlock, dio
	CPUMoe        bool    `json:"cpuMoe"`        // keep all MoE experts on CPU
	NCpuMoe       int     `json:"nCpuMoe"`       // keep first N MoE layers on CPU, 0=disabled
	SplitMode     string  `json:"splitMode"`     // "", none, layer, row, tensor
	TensorSplit   string  `json:"tensorSplit"`   // e.g. "3,1"
	MainGPU       int     `json:"mainGpu"`       // default 0
	RopeScaling   string  `json:"ropeScaling"`   // "", none, linear, yarn
	RopeScale     float64 `json:"ropeScale"`     // 0=disabled
	MMProj        string  `json:"mmproj"`        // explicit mmproj path override, empty=auto-detect
	Reasoning     bool    `json:"reasoning"`     // disable thinking (writes reasoning = off)
	SpecType      string  `json:"specType"`      // "", draft-mtp, ngram-simple, ngram-mod
	SpecDraftNMax int     `json:"specDraftNMax"` // >0 writes spec-draft-n-max
	// CtxCheckpointsOff disables llama-server's context checkpoints
	// (writes ctx-checkpoints = 0; upstream default 32): each auto-checkpoint
	// copies KV state into host RAM during prompt processing and the app never
	// calls the restore API. Set by the auto-tuner (Windows full-offload
	// plans, every Android plan); zero value keeps the upstream default, and
	// omitempty keeps old config JSONs loading byte-compatibly.
	CtxCheckpointsOff bool `json:"ctxCheckpointsOff,omitempty"`
	MLock             bool `json:"mlock,omitempty"`  // deprecated, kept only to migrate old configs
	NoMMap            bool `json:"noMmap,omitempty"` // deprecated, kept only to migrate old configs
	// The former loraAdapters field (LoRA adapter refs, removed feature) is
	// intentionally absent: Go's json.Unmarshal ignores unknown keys, so old
	// config JSONs carrying loraAdapters (per model) or loraDir (app level)
	// still load harmlessly and the stale keys are dropped on the next save.
}

// migrateLegacyConfig copies older config files forward to the active config
// path (configFilePath) before loadConfig reads it, newest source first:
//
//  1. Non-Windows only: a legacy cwd-relative llama-desktop-config.json (the
//     pre-app-data layout, see paths.go) is copied into the app-data base;
//     [INFO]-logged, the source file is kept. On Windows this stage never
//     runs (the active path is the same cwd-relative name — zero behavior
//     change).
//  2. Any platform: the llama-gui-era llama-gui-config.json is copied to the
//     active config path ([OK]-logged, unchanged behavior).
//
// Both stages copy instead of move (source stays in place): wails dev's file
// watcher watches the project root, and deleting/renaming root files during
// startup triggers a GetFileAttributesEx race in the Wails CLI that crashes
// the run; copying never deletes the source, the new file's existence
// short-circuits, and a leftover old file has no side effects — migration
// re-triggers only if the user deletes the new file. Failures only log a
// warning and fall back to loadConfig's defaults, never blocking startup.
//
// Migration asymmetry (by design): only the config file migrates. The other
// state files are caches or transient state that regenerate on demand — the
// bench cache re-benchmarks, the docs cache re-fetches, and the handover
// record only matters within a single GUI↔headless switch — so none of them
// are copied to the app-data base.
func migrateLegacyConfig() {
	target := configFilePath()
	if _, err := os.Stat(target); err == nil {
		return
	}
	// Stage 1: pre-app-data cwd config (non-Windows only). The source is the
	// bare cwd-relative name, deliberately not run through resolveStateFile.
	if base := appDataDir(); base != "" {
		if data, err := os.ReadFile(configFileName); err == nil {
			if err := atomicWriteFile(target, data, 0644); err != nil {
				log.Printf("[WARN] Failed to migrate legacy cwd config %s -> %s: %v", configFileName, target, err)
				return
			}
			log.Printf("[INFO] Migrated legacy cwd config %s -> %s (source kept)", configFileName, target)
			return
		}
	}
	// Stage 2: llama-gui-era file (cwd-relative legacyConfigFile).
	if _, err := os.Stat(legacyConfigFile); err != nil {
		return
	}
	data, err := os.ReadFile(legacyConfigFile)
	if err != nil {
		log.Printf("[WARN] Failed to migrate legacy config %s: %v", legacyConfigFile, err)
		return
	}
	if err := atomicWriteFile(target, data, 0644); err != nil {
		log.Printf("[WARN] Failed to migrate legacy config %s: %v", legacyConfigFile, err)
		return
	}
	log.Printf("[OK] Migrated legacy config %s -> %s", legacyConfigFile, target)
}

func loadConfig() {
	migrateLegacyConfig()
	data, err := os.ReadFile(configFilePath())
	if err != nil {
		return // file doesn't exist yet, that's ok
	}
	var cfg appConfig
	// Pre-populate defaults before Unmarshal: Go's zero value false cannot
	// distinguish "old config missing the field" from "explicitly set to
	// false". trayEnabled must default to true when absent (tray stays on
	// after historical config upgrades, matching 4aacac2's unconditional
	// tray); sidebarCollapsed must default to true when absent (sidebar
	// collapses by default with no saved preference).
	cfg.TrayEnabled = true
	cfg.SidebarCollapsed = true
	if err := json.Unmarshal(data, &cfg); err != nil {
		log.Printf("[WARN] Failed to parse config file: %v", err)
		return
	}
	if cfg.LlamaCppDir != "" {
		customLlamaCppMu.Lock()
		customLlamaCppDir = cfg.LlamaCppDir
		customLlamaCppMu.Unlock()
		log.Printf("[DIR] Loaded custom llama.cpp dir from config: %s", cfg.LlamaCppDir)
	}
	// llama.cpp download path: empty values fall back to the default
	// llama-cpp/ directory (no existence check — a fresh path is a valid
	// target for the next download).
	if cfg.LlamaCppDownloadDir != "" {
		llamaCppDownloadDirMu.Lock()
		llamaCppDownloadDirOverride = cfg.LlamaCppDownloadDir
		llamaCppDownloadDirMu.Unlock()
		log.Printf("[DIR] Loaded llama.cpp download dir from config: %s", cfg.LlamaCppDownloadDir)
	}
	// Model download path: empty values fall back to the default LLM-Models
	// directory (no existence check — a fresh path is a valid target for the
	// next model download).
	if cfg.ModelDownloadDir != "" {
		modelDownloadDirMu.Lock()
		modelDownloadDirOverride = cfg.ModelDownloadDir
		modelDownloadDirMu.Unlock()
		log.Printf("[DIR] Loaded model download dir from config: %s", cfg.ModelDownloadDir)
	}
	// Imported model directory: empty values or paths that do not exist / are
	// not directories are ignored and fall back to the default directory,
	// preventing scans/downloads from landing on invalid paths after config
	// corruption or directory deletion.
	if cfg.ModelDir != "" {
		if fi, err := os.Stat(cfg.ModelDir); err != nil || !fi.IsDir() {
			log.Printf("[WARN] Ignoring invalid model dir from config: %s", cfg.ModelDir)
		} else {
			modelsDirMu.Lock()
			customModelsDir = cfg.ModelDir
			modelsDirMu.Unlock()
			log.Printf("[DIR] Loaded custom models dir from config: %s", cfg.ModelDir)
		}
	}
	if cfg.Theme == "" {
		cfg.Theme = "light"
	}
	currentTheme = cfg.Theme
	if cfg.ModelConfigs == nil {
		cfg.ModelConfigs = make(map[string]ModelConfig)
	}
	cachedModelConfigs = cfg.ModelConfigs
	// Migrate legacy mlock/noMmap to load-mode (both DEPRECATED since b10342):
	// if an old config has no explicit loadMode, derive it from the old boolean
	// combination and clear the compatibility fields; omitempty in saveConfig
	// guarantees the old keys are never written back (gradual cleanup).
	for k, c := range cachedModelConfigs {
		if c.LoadMode == "" && (c.MLock || c.NoMMap) {
			switch {
			case c.MLock && c.NoMMap:
				c.LoadMode = "mlock" // mlock semantics take priority
			case c.MLock:
				c.LoadMode = "mlock"
			case c.NoMMap:
				c.LoadMode = "none"
			}
		}
		c.MLock = false
		c.NoMMap = false
		cachedModelConfigs[k] = c
	}
	// Merge server config with defaults
	scfg := defaultServerConfig()
	// Access scope: empty values or anything outside the {local,lan} whitelist
	// fall back to local (no error when old configs lack accessMode or data is
	// corrupt). Host is always derived by effectiveHost from accessMode; a
	// possibly-invalid host value in old configs is never trusted (extending
	// the #5 defense).
	if cfg.ServerConfig.AccessMode != accessLocal && cfg.ServerConfig.AccessMode != accessLAN {
		cfg.ServerConfig.AccessMode = accessLocal
	}
	scfg.AccessMode = cfg.ServerConfig.AccessMode
	scfg.Host = effectiveHost(scfg.AccessMode)
	// APIKey: Go zero value "" (no authentication) already covers old configs
	// missing the field, no fallback needed.
	scfg.APIKey = cfg.ServerConfig.APIKey
	// DeviceID (serving-GPU UUID): Go zero value "" (auto / default device)
	// already covers old configs missing the field, no fallback needed.
	scfg.DeviceID = cfg.ServerConfig.DeviceID
	if cfg.ServerConfig.Port != 0 {
		scfg.Port = cfg.ServerConfig.Port
	}
	if cfg.ServerConfig.MaxModels != 0 {
		scfg.MaxModels = cfg.ServerConfig.MaxModels
	}
	if cfg.ServerConfig.CacheRAM != 0 {
		scfg.CacheRAM = cfg.ServerConfig.CacheRAM
	}
	cachedServerConfig = scfg

	// Download source: empty or invalid values fall back to the default hf
	// (no error when old configs lack this field or data is corrupt).
	if cfg.DownloadSource != sourceHF && cfg.DownloadSource != sourceHuggingFace && cfg.DownloadSource != sourceModelScope {
		cfg.DownloadSource = defaultDownloadSource
	}
	downloadSourceMu.Lock()
	downloadSource = cfg.DownloadSource
	downloadSourceMu.Unlock()

	// Language preference: empty values or anything outside the zh/en/auto
	// whitelist fall back to auto (no error when old configs lack this field
	// or data is corrupt). Same strategy as downloadSource: invalid values are
	// always normalized back to the default.
	if cfg.Language != "zh" && cfg.Language != "en" && cfg.Language != "auto" {
		cfg.Language = "auto"
	}
	languageMu.Lock()
	currentLanguage = cfg.Language
	languageMu.Unlock()

	// System tray toggle: keep the pre-populated default true when the field is
	// missing; only an explicit false disables it (tray stays on after old
	// config upgrades, matching 4aacac2's unconditional tray behavior).
	// API-route mode: Go zero value false is already the intended default for
	// configs missing the field, no pre-population needed (unlike trayEnabled).
	configMu.Lock()
	trayEnabled = cfg.TrayEnabled
	apiRouteMode = cfg.ApiRouteMode
	configMu.Unlock()

	// Sidebar collapsed state: keep the pre-populated default true when the
	// field is missing (collapsed, see the appConfig pre-population above);
	// only an explicit false (user's expand preference) yields false, same
	// pattern as trayEnabled.
	configMu.Lock()
	currentSidebarCollapsed = cfg.SidebarCollapsed
	// Onboarding checklist: Go zero value false is already the intended
	// default for configs missing the field (checklist visible until the user
	// dismisses it or completes all steps), no pre-population needed.
	currentOnboardingDismissed = cfg.OnboardingDismissed
	configMu.Unlock()

	// Restore the download task queue (after a process restart there are no
	// active goroutines, so no task auto-starts its download): Source falls
	// back to hf; statuses outside the whitelist and downloading are all
	// normalized to paused (the downloading goroutine died with the process;
	// the frontend can offer resume/retry); URLs are rebuilt via
	// buildModelDownloadURL; resumeCh is a fresh buffered channel while
	// ctx/cancel stay nil (RetryDownloadTask rebuilds ctx before starting).
	// After restoring, bump dlTaskCounter to avoid id collisions with
	// existing tasks.
	restored := make([]*DlTask, 0, len(cfg.DownloadTasks))
	for _, pt := range cfg.DownloadTasks {
		src := pt.Source
		if src == "" {
			src = sourceHF
		}
		status := pt.Status
		switch status {
		case "done", "error", "cancelled", "queued", "paused":
			// terminal and controllable states stay as-is
		default:
			// empty, invalid, or downloading → paused
			status = "paused"
		}
		task := &DlTask{
			ID:         pt.ID,
			ModelID:    pt.ModelID,
			FileName:   pt.FileName,
			DestDir:    pt.DestDir,
			Source:     src,
			Status:     status,
			Progress:   pt.Progress,
			Total:      pt.Total,
			Downloaded: pt.Downloaded,
			SizeHuman:  pt.SizeHuman,
			Error:      pt.Error,
			resumeCh:   make(chan struct{}, 1),
		}
		if url, err := buildModelDownloadURL(src, pt.ModelID, pt.FileName); err == nil {
			task.URL = url
		}
		restored = append(restored, task)
	}
	dlTasksMu.Lock()
	dlTasks = restored
	// Bump the id counter to max restored sequence + 1 (parsing "dl-N") to
	// avoid id collisions with new tasks; keep the current value on parse
	// failure or when nothing was restored.
	maxSeq := 0
	for _, t := range restored {
		if n, err := strconv.Atoi(strings.TrimPrefix(t.ID, "dl-")); err == nil && n > maxSeq {
			maxSeq = n
		}
	}
	if maxSeq > dlTaskCounter {
		dlTaskCounter = maxSeq
	}
	dlTasksMu.Unlock()
}

var cachedModelConfigs = make(map[string]ModelConfig)
var modelConfigsMu sync.Mutex
var cachedServerConfig = defaultServerConfig()
var serverConfigMu sync.Mutex

func defaultServerConfig() ServerConfig {
	return ServerConfig{
		AccessMode: accessLocal, Host: "127.0.0.1", Port: 8080, MaxModels: 1, CacheRAM: 8192,
	}
}

var currentTheme = "light"
var configMu sync.Mutex

// trayEnabled indicates whether the Windows system tray is enabled (closing
// the window minimizes to tray), default true; guarded by configMu and
// persisted to the config file's trayEnabled field. When an old config lacks
// the field, loadConfig falls back to true (see the appConfig{TrayEnabled:
// true} pre-population in loadConfig).
var trayEnabled = true

// apiRouteMode indicates whether API-route (headless) mode is enabled:
// when true, the next app start skips the GUI (WebView2) and runs as the Go
// backend + system tray + llama-server only, keeping the OpenAI API alive
// with a much smaller footprint (Windows only, see core/headless.go).
// Default false; guarded by configMu and persisted to the config file's
// apiRouteMode field. Old configs missing the field fall back to false
// (Go zero value, no pre-population needed).
var apiRouteMode bool

// ApiRouteMode returns the current API-route (headless) mode preference
// (concurrency-safe, guarded by configMu). Used by ShouldRunHeadless when a
// process starts without an explicit --headless/--gui flag.
func ApiRouteMode() bool {
	configMu.Lock()
	defer configMu.Unlock()
	return apiRouteMode
}

// currentSidebarCollapsed indicates whether the sidebar is collapsed
// (icon-only rail), default true (collapsed); guarded by configMu and
// persisted to the config file's sidebarCollapsed field. When an old config
// lacks the field, loadConfig pre-populates the default true (see the
// appConfig pre-population in loadConfig), the same fallback pattern as
// trayEnabled.
var currentSidebarCollapsed = true

// currentOnboardingDismissed indicates whether the Home page quick-start
// checklist has been dismissed (manually closed or auto-completed); guarded
// by configMu and persisted to the config file's onboardingDismissed field.
// Default false: old configs lacking the field show the checklist.
var currentOnboardingDismissed = false

// TrayEnabled returns the current tray preference (concurrency-safe, guarded
// by configMu). Used by main.go's OnStartup to decide whether to start the
// tray per the persisted config.
func TrayEnabled() bool {
	configMu.Lock()
	defer configMu.Unlock()
	return trayEnabled
}

func saveConfig() {
	customLlamaCppMu.Lock()
	dir := customLlamaCppDir
	customLlamaCppMu.Unlock()

	modelsDirMu.Lock()
	modelDir := customModelsDir
	modelsDirMu.Unlock()

	llamaCppDownloadDirMu.Lock()
	llamaDownloadDir := llamaCppDownloadDirOverride
	llamaCppDownloadDirMu.Unlock()

	modelDownloadDirMu.Lock()
	modelDownloadDir := modelDownloadDirOverride
	modelDownloadDirMu.Unlock()

	configMu.Lock()
	theme := currentTheme
	configMu.Unlock()

	modelConfigsMu.Lock()
	mcfgs := make(map[string]ModelConfig, len(cachedModelConfigs))
	for k, v := range cachedModelConfigs {
		mcfgs[k] = v
	}
	modelConfigsMu.Unlock()

	serverConfigMu.Lock()
	scfg := cachedServerConfig
	serverConfigMu.Unlock()

	downloadSourceMu.Lock()
	dlsrc := downloadSource
	downloadSourceMu.Unlock()

	languageMu.Lock()
	lang := currentLanguage
	languageMu.Unlock()

	configMu.Lock()
	tray := trayEnabled
	configMu.Unlock()

	configMu.Lock()
	sidebarCollapsed := currentSidebarCollapsed
	apiRoute := apiRouteMode
	onboardingDismissed := currentOnboardingDismissed
	configMu.Unlock()

	// Lock-ordering iron rule: inside saveConfig, dlTasksMu must be the last
	// lock acquired. No call site may call saveConfig while holding dlTasksMu —
	// callers must copy under the lock, unlock, then save (e.g.
	// CancelDownloadTask does not call saveConfig before its deferred Unlock).
	// Otherwise the global ordering between dlTasksMu and other locks
	// (configMu etc.) is violated, causing deadlock.
	dlTasksMu.Lock()
	persistedTasks := make([]PersistedDlTask, 0, len(dlTasks))
	for _, t := range dlTasks {
		persistedTasks = append(persistedTasks, PersistedDlTask{
			ID:         t.ID,
			ModelID:    t.ModelID,
			FileName:   t.FileName,
			DestDir:    t.DestDir,
			Source:     t.Source,
			Status:     t.Status,
			Progress:   t.Progress,
			Total:      t.Total,
			Downloaded: t.Downloaded,
			SizeHuman:  t.SizeHuman,
			Error:      t.Error,
		})
	}
	dlTasksMu.Unlock()

	cfg := appConfig{
		LlamaCppDir:         dir,
		ModelDir:            modelDir,
		LlamaCppDownloadDir: llamaDownloadDir,
		ModelDownloadDir:    modelDownloadDir,
		Theme:               theme,
		ModelConfigs:        mcfgs,
		ServerConfig:        scfg,
		DownloadSource:      dlsrc,
		Language:            lang,
		TrayEnabled:         tray,
		SidebarCollapsed:    sidebarCollapsed,
		OnboardingDismissed: onboardingDismissed,
		ApiRouteMode:        apiRoute,
		DownloadTasks:       persistedTasks,
	}
	data, err := json.MarshalIndent(cfg, "", "  ")
	if err != nil {
		log.Printf("[WARN] Failed to marshal config: %v", err)
		return
	}
	if err := atomicWriteFile(configFilePath(), data, 0644); err != nil {
		log.Printf("[WARN] Failed to write config file: %v", err)
	}
}
