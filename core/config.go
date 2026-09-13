package core

import (
	"encoding/json"
	"log"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
)

// ─── Config persistence ─────────────────────────────────────────
// Persisted app config (myllama-config.json): schema types, load/save with
// legacy migration (llama-desktop- and llama-gui-era files), and the guarded
// in-memory app-state variables.

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

// legacyConfigFile is the config filename from the llama-gui era (the
// oldest link of the migration chain, see migrateLegacyConfig). It serves
// only as a one-shot migration source: when the new file does not exist but
// an old one does, the old file is renamed (copy as fallback) to the new
// name and reused, preserving theme / directories / model params / download
// queue for existing users losslessly.
var legacyConfigFile = legacyGuiConfigFileName

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
	ApiRouteMode bool `json:"apiRouteMode"`
	// RemoteChat is the LAN remote-chat pairing (phone → PC llama-server).
	// Old configs missing the remoteChat key load as the zero value; loadConfig
	// normalizes the port back to 8080 (the llama-server default) while
	// enabled/host/apiKey keep their zero values (disabled pairing).
	RemoteChat    RemoteChatConfig  `json:"remoteChat"`
	DownloadTasks []PersistedDlTask `json:"downloadTasks,omitempty"`
}

// RemoteChatConfig holds the phone-to-PC LAN chat pairing used by the chat
// page when Enabled is true: the LAN address of the peer machine running
// llama-server and the optional API key configured there.
type RemoteChatConfig struct {
	Enabled bool   `json:"enabled"`
	Host    string `json:"host"`   // LAN host or IP, e.g. 192.168.1.5 (no scheme, no path)
	Port    int    `json:"port"`   // llama-server port on the peer, default 8080
	APIKey  string `json:"apiKey"` // optional bearer token of the peer server
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

// migrateLegacyConfig migrates older config files forward to the active
// config path (configFilePath) before loadConfig reads it. The fallback /
// migration chain, newest era first:
//
//	myllama-config.json (active) ← llama-desktop-config.json ← llama-gui-config.json
//
// Sources, probed in order:
//
//  1. The llama-desktop-era name at its resolved location (bare
//     cwd-relative on Windows — the install dir; inside the app-data base on
//     non-Windows / Android — the base directory itself was already renamed
//     from "llama-desktop" to "myllama" by migrateAppDataDirName, so the
//     file inside carries the old name).
//  2. The bare cwd-relative llama-desktop-era name (pre-app-data layout,
//     non-Windows only; identical to source 1 on Windows and skipped there).
//  3. The llama-gui-era file (bare cwd-relative, the pre-rebrand behavior).
//
// The first hit is renamed onto the active path (same directory = atomic);
// when the rename is impossible (cross-device etc.) it degrades to a copy
// with the source left in place. Migration is skipped when the active file
// already exists. Failures only log a warning and fall back to loadConfig's
// defaults, never blocking startup.
//
// Historical note: before the MyLlama rebrand this function copied instead
// of renamed, because wails dev's file watcher watches the project root and
// renaming root files during startup could crash the Wails CLI. The rename
// is one-shot per install (the new file's existence short-circuits every
// later start) and the copy fallback keeps the source in place whenever the
// rename fails, so the watcher-safe behavior remains available on failure.
//
// Migration asymmetry (by design): only the config file and the app-data
// directory rename. The other state files are caches or transient state that
// regenerate on demand — the bench cache re-benchmarks (but is still renamed,
// it is cheap), the docs cache re-fetches, and the handover record only
// matters within a single GUI↔headless switch (read via its legacy-name
// fallback, never renamed).
func migrateLegacyConfig() {
	target := configFilePath()
	if _, err := os.Stat(target); err == nil {
		return
	}
	// Source 1: previous-era name at the resolved location.
	src := resolveStateFile(legacyConfigFileName)
	if migrateConfigFile(src, target) {
		return
	}
	// Source 2: bare cwd-relative name of the same era (pre-app-data layout,
	// distinct from source 1 only on non-Windows platforms).
	if src != legacyConfigFileName && migrateConfigFile(legacyConfigFileName, target) {
		return
	}
	// Source 3: llama-gui era.
	migrateConfigFile(legacyConfigFile, target)
}

// migrateConfigFile migrates one legacy config source to the active target
// path: rename when possible (same directory = atomic), copy as fallback
// (source kept). Reports whether the migration happened; a missing source is
// silently not a migration.
func migrateConfigFile(src, dst string) bool {
	if src == dst {
		return false
	}
	if _, err := os.Stat(src); err != nil {
		return false
	}
	if err := os.Rename(src, dst); err == nil {
		log.Printf("[OK] Migrated legacy config %s -> %s (renamed)", src, dst)
		return true
	}
	// Rename failed (cross-device, lock, ...): copy instead, keeping the
	// source in place — the pre-rebrand behavior and the wails-dev-safe path.
	data, err := os.ReadFile(src)
	if err != nil {
		log.Printf("[WARN] Failed to migrate legacy config %s: %v", src, err)
		return false
	}
	if err := atomicWriteFile(dst, data, 0644); err != nil {
		log.Printf("[WARN] Failed to migrate legacy config %s: %v", src, err)
		return false
	}
	log.Printf("[OK] Migrated legacy config %s -> %s (copied, source kept)", src, dst)
	return true
}

// legacyPathMarkerName is the marker file the Windows NSIS installer plants in
// the install directory (the process cwd on Windows) after copying a legacy
// Llama Desktop install's data over: its content is the legacy install
// directory's absolute path (single line). The app consumes it once at
// startup to rewrite persisted absolute paths from the legacy prefix to the
// new install dir, then deletes the marker.
const legacyPathMarkerName = "migration-legacy-path.txt"

// applyLegacyPathMarker consumes the installer-planted legacy-path marker:
// reads the legacy install dir from it, rewrites every persisted absolute
// path under that prefix to the current working directory (the new install
// dir), persists the rewritten config, and deletes the marker — always, even
// when there is nothing to rewrite (empty content, marker naming the cwd, or
// no matching paths), so a broken marker can never re-trigger every start.
// The marker only ever exists on Windows (planted by the installer), so the
// path matching inside is case-insensitive without a platform gate.
func applyLegacyPathMarker(cfg appConfig) appConfig {
	data, err := os.ReadFile(legacyPathMarkerName)
	if err != nil {
		return cfg // no marker: fresh install or already consumed
	}
	legacy := strings.TrimSpace(string(data))
	cwd, err := os.Getwd()
	if err != nil {
		cwd = ""
	}
	removeLegacyPathMarker()
	if legacy == "" {
		log.Println("[INFO] Empty legacy-path migration marker, ignored")
		return cfg
	}
	if cwd != "" && sameDirPath(legacy, cwd) {
		// The legacy install IS this install dir (overlay upgrade): nothing to
		// rewrite, the recorded paths are already correct.
		log.Printf("[INFO] Legacy install dir equals the current one (%s), no path rewrite needed", cwd)
		return cfg
	}
	if !rewriteLegacyPaths(&cfg, legacy, cwd) {
		log.Printf("[INFO] No persisted paths under legacy install dir %s needed rewriting", legacy)
		return cfg
	}
	if out, err := json.MarshalIndent(cfg, "", "  "); err == nil {
		if err := atomicWriteFile(configFilePath(), out, 0644); err != nil {
			log.Printf("[WARN] Failed to persist rewritten config after legacy-path migration: %v", err)
		}
	}
	log.Printf("[OK] Rewrote persisted paths from legacy install dir %s to %s", legacy, cwd)
	return cfg
}

// removeLegacyPathMarker deletes the installer-planted marker file; a missing
// file is not an error.
func removeLegacyPathMarker() {
	if err := os.Remove(legacyPathMarkerName); err != nil && !os.IsNotExist(err) {
		log.Printf("[WARN] Failed to remove legacy-path migration marker: %v", err)
	}
}

// sameDirPath compares two directory paths for equality after cleaning,
// case-insensitively (the legacy-path migration is Windows-only; see
// applyLegacyPathMarker).
func sameDirPath(a, b string) bool {
	return strings.EqualFold(filepath.Clean(a), filepath.Clean(b))
}

// rewriteLegacyPaths rewrites every persisted absolute path in cfg that sits
// under the legacyDir prefix to the same relative location under newDir.
// Pure (no I/O): the caller persists the result. Used by the Windows
// installer migration (applyLegacyPathMarker); matching is case-insensitive
// (Windows paths) and requires a separator boundary after the prefix, so a
// sibling directory sharing a leading substring never matches. Top-level
// directory fields and the per-model MMProj override are rewritten; fields
// that never store legacy-prefixed absolute paths (theme, ports, server
// config, ...) are structurally untouched. Returns true when at least one
// field changed.
func rewriteLegacyPaths(cfg *appConfig, legacyDir, newDir string) bool {
	if legacyDir == "" || newDir == "" {
		return false
	}
	changed := false
	rew := func(p string) string {
		q, ok := rewritePathUnderPrefix(p, legacyDir, newDir)
		if ok {
			changed = true
		}
		return q
	}
	cfg.LlamaCppDir = rew(cfg.LlamaCppDir)
	cfg.ModelDir = rew(cfg.ModelDir)
	cfg.LlamaCppDownloadDir = rew(cfg.LlamaCppDownloadDir)
	cfg.ModelDownloadDir = rew(cfg.ModelDownloadDir)
	for k, mc := range cfg.ModelConfigs {
		if q, ok := rewritePathUnderPrefix(mc.MMProj, legacyDir, newDir); ok {
			mc.MMProj = q
			cfg.ModelConfigs[k] = mc
			changed = true
		}
	}
	return changed
}

// rewritePathUnderPrefix rewrites one path from the legacyDir prefix to
// newDir (see rewriteLegacyPaths). Only absolute paths are considered; the
// original suffix (including its case) is preserved, and a path equal to the
// legacy dir itself maps to newDir exactly.
func rewritePathUnderPrefix(path, legacyDir, newDir string) (string, bool) {
	if path == "" {
		return path, false
	}
	p := filepath.Clean(path)
	legacy := filepath.Clean(legacyDir)
	if !filepath.IsAbs(p) || !filepath.IsAbs(legacy) {
		return path, false
	}
	lp, ll := strings.ToLower(p), strings.ToLower(legacy)
	if lp != ll && !strings.HasPrefix(lp, ll+string(filepath.Separator)) &&
		!strings.HasPrefix(lp, ll+"/") && !strings.HasPrefix(lp, ll+`\`) {
		return path, false
	}
	return filepath.Join(filepath.Clean(newDir), p[len(legacy):]), true
}

func loadConfig() {
	migrateLegacyConfig()
	data, err := os.ReadFile(configFilePath())
	if err != nil {
		// No config file yet: nothing persisted to rewrite — still consume the
		// installer's legacy-path marker so it cannot re-trigger every start.
		removeLegacyPathMarker()
		return
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
		removeLegacyPathMarker()
		return
	}
	// Windows installer legacy-install migration: rewrite persisted absolute
	// paths from the legacy install dir to the current one (see the marker
	// contract at applyLegacyPathMarker). Runs before the values land in the
	// in-memory state so every consumer sees the rewritten paths.
	cfg = applyLegacyPathMarker(cfg)
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
	// Defensive locking (theme / model configs / server config below):
	// loadConfig runs only on the single startup goroutine (App.Startup,
	// ShouldRunHeadless, RunHeadless — none of which hold these mutexes at
	// call time, so the acquisitions cannot deadlock), but routing every
	// write through the same mutex the saveConfig readers use costs nothing
	// and removes the latent race for any future caller. dlTasksMu stays the
	// last lock acquired (queue-restore block below), per the saveConfig
	// lock-ordering rule.
	configMu.Lock()
	currentTheme = cfg.Theme
	configMu.Unlock()
	if cfg.ModelConfigs == nil {
		cfg.ModelConfigs = make(map[string]ModelConfig)
	}
	modelConfigsMu.Lock()
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
	modelConfigsMu.Unlock()
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
	serverConfigMu.Lock()
	cachedServerConfig = scfg
	serverConfigMu.Unlock()

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
	// Remote-chat pairing: old configs missing the remoteChat key load as the
	// zero value — Enabled=false (chat targets the local service), Host="",
	// APIKey="" and the port normalized to the llama-server default 8080.
	// An explicit port 0 (corrupt / never validated) normalizes the same way;
	// SaveRemoteChat is the only writer of valid ports (1..65535).
	if cfg.RemoteChat.Port == 0 {
		cfg.RemoteChat.Port = 8080
	}
	cachedRemoteChat = cfg.RemoteChat
	configMu.Unlock()

	// Restore the download task queue (after a process restart there are no
	// active goroutines, so no task auto-starts its download): Source falls
	// back to hf; statuses outside the whitelist and downloading are all
	// normalized to paused (the downloading goroutine died with the process;
	// the frontend can offer resume/retry); URLs are rebuilt via
	// buildModelDownloadURL; resumeCh is a fresh buffered channel while
	// ctx/cancel stay nil and the running flag stays false (zero value — no
	// goroutine exists), so ResumeDownloadTask's respawn branch and
	// RetryDownloadTask both rebuild the ctx before starting a fresh
	// goroutine.
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
// checklist has been dismissed (manually closed or auto-completed); guarded by
// configMu and persisted to the config file's onboardingDismissed field.
// Default false: old configs lacking the field show the checklist.
var currentOnboardingDismissed = false

// cachedRemoteChat is the persisted LAN remote-chat pairing (phone → PC
// llama-server, see RemoteChatConfig); guarded by configMu like the other
// app-state config entries. Default: disabled pairing against the llama-server
// default port; SaveRemoteChat is the only writer after loadConfig.
var cachedRemoteChat = RemoteChatConfig{Enabled: false, Host: "", Port: 8080, APIKey: ""}

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
	remoteChat := cachedRemoteChat
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
		RemoteChat:          remoteChat,
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
