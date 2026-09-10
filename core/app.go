package core

import (
	"context"
	"errors"
	"fmt"
	"log"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"sync"

	"github.com/wailsapp/wails/v3/pkg/application"
)

// MainWindowName is the unique name of the single application window. main.go
// creates the frameless main window with this name; the theme background
// switcher (applyThemeBackground) looks the window up by it at runtime.
const MainWindowName = "main"

// App is the Wails application struct whose methods are bound to the frontend.
type App struct {
	ctx context.Context
}

func NewApp() *App {
	return &App{}
}

// applyThemeBackground paints the main window background to match the theme
// (light: near-white, dark: near-black). Uses the v3 global application's
// window manager; degrades to a no-op when no application / window exists
// (unit tests, headless mode). Before the window is realized the call only
// mutates the pending window options — exactly right for the startup path,
// where ServiceStartup runs before the queued main window is built.
func applyThemeBackground(theme string) {
	wa := application.Get()
	if wa == nil {
		return
	}
	win, ok := wa.Window.GetByName(MainWindowName)
	if !ok || win == nil {
		return
	}
	if theme == "light" {
		win.SetBackgroundColour(application.NewRGBA(248, 250, 252, 255))
	} else {
		win.SetBackgroundColour(application.NewRGBA(15, 15, 20, 255))
	}
}

// quitApp asks the Wails v3 application to quit (runs the full shutdown
// sequence, then exits). No-op when no application instance exists (tests,
// headless mode), matching the nil-ctx guard the v2 runtime.Quit call sites
// had.
func quitApp() {
	if wa := application.Get(); wa != nil {
		wa.Quit()
	}
}

// pickDirectory shows a native folder chooser and returns the selected path.
// Cancellation normalizes to ("", nil) — the contract the v2
// OpenDirectoryDialog exposed and the frontend browse wrappers rely on. v3
// surfaces a cancel as an error whose sentinel (cfd.ErrorCancelled) is
// internal to the v3 module, so its stable message is matched here.
func pickDirectory(title string) (string, error) {
	wa := application.Get()
	if wa == nil {
		return "", nil
	}
	dir, err := wa.Dialog.OpenFile().
		SetTitle(title).
		CanChooseDirectories(true).
		CanChooseFiles(false).
		PromptForSingleSelection()
	if err != nil {
		if err.Error() == "cancelled by user" {
			return "", nil
		}
		return "", err
	}
	if dir == "" {
		return "", nil
	}
	return dir, nil
}

// Startup loads persisted config, adopts a handed-over llama-server (when
// returning from headless API-route mode) and applies the saved theme to the
// window background. Called by ServiceStartup (the v3 lifecycle hook); kept
// public because core/handover_test.go exercises it directly.
//
//wails:ignore
func (a *App) Startup(ctx context.Context) {
	a.ctx = ctx

	// Load persisted config on startup
	loadConfig()

	// Adopt a healthy llama-server handed over by a headless predecessor
	// (serverRunning=true, serverCmd=nil, adoptedPid set); a stale handover
	// record is deleted. GUI never auto-starts the service.
	adoptOrCleanHandover()

	// Start the background monitor sampler (CPU / memory / GPU / TPS)
	StartMonitorSampler()

	// Set initial window background from saved theme
	configMu.Lock()
	theme := currentTheme
	configMu.Unlock()
	applyThemeBackground(theme)

	log.Println("[INFO] MyLlama started")
}

// ServiceStartup is the Wails v3 service lifecycle hook (replacement for the
// v2 OnStartup callback); it runs inside application.Run, before the queued
// main window is realized.
func (a *App) ServiceStartup(ctx context.Context, options application.ServiceOptions) error {
	a.Startup(ctx)

	// Start system tray based on persisted config (unconditionally since
	// 4aacac2; user can disable in settings). loadConfig defaults missing
	// legacy fields to true. When enabled, the app starts with a tray icon;
	// disabling the setting requires an app restart. Platform gate: the tray
	// is a Windows (taskbar) / macOS (NSStatusItem) feature — Linux never
	// auto-starts one (DBUS StatusNotifierItem depends on the desktop
	// environment; see TraySupported).
	if TrayEnabled() && trayPlatformSupported() {
		InitTray(ctx, TrayIcon)
	}
	return nil
}

// Shutdown persists the download queue, stops llama-server and cancels all
// in-flight downloads — except during a mode-switch restart (GUI → headless
// via SetApiRouteMode), where the relaunched process adopts the running
// llama-server via the handover file and in-flight downloads continue from
// the persisted queue, so both the service stop and the download cancels are
// skipped. Called by ServiceShutdown (the v3 lifecycle hook); kept public
// because core/handover_test.go exercises it directly.
//
//wails:ignore
func (a *App) Shutdown(ctx context.Context) {
	// Persist the download task queue before cancelling anything (#12): save
	// before cancelling downloads to guarantee the latest state is restored
	// after restart. saveConfig acquires dlTasksMu at the end, which is not
	// held here.
	saveConfig()

	if switchRestartPending.Load() {
		// Mode-switch restart: the successor process (already relaunched)
		// adopts llama-server via the handover file; keep the service and the
		// download goroutines alive until this process exits.
		log.Println("[INFO] MyLlama stopping for mode switch; llama-server keeps running")
		return
	}

	// Stop running llama-server if any (#3): take a local copy under
	// serverMu, then Signal the copy outside the lock, avoiding data races
	// with concurrent start/stop during app exit. An adopted server (handed
	// over from headless mode: cmd nil, adoptedPid set) is killed by pid and
	// its handover record removed.
	if stopAdoptedServerIfAny() {
		// adopted server stopped and handover record removed
	} else {
		serverMu.Lock()
		running := serverRunning
		cmd := serverCmd
		serverMu.Unlock()
		if running && cmd != nil {
			addServerLog("[INFO] Stopping llama-server on shutdown...")
			cmd.Process.Signal(osInterrupt)
		}
	}

	// Cancel ongoing llama.cpp download (#3): downloadCancel is guarded by
	// downloadMu; take a local copy under the lock, then call outside.
	downloadMu.Lock()
	cancelDownload := downloadCancel
	downloadMu.Unlock()
	if cancelDownload != nil {
		cancelDownload()
	}

	// Cancel ongoing app update download
	updateDownloadMu.Lock()
	cancelUpdate := updateDownloadCancel
	updateDownloadMu.Unlock()
	if cancelUpdate != nil {
		cancelUpdate()
	}

	// Cancel all HF model download tasks
	dlTasksMu.Lock()
	for _, t := range dlTasks {
		if t.cancel != nil {
			t.cancel()
		}
	}
	dlTasksMu.Unlock()

	log.Println("[INFO] MyLlama stopped")
}

// ServiceShutdown is the Wails v3 service lifecycle hook (replacement for the
// v2 OnShutdown callback): remove the tray icon first, then clean up the app
// (stop server / persist config), mirroring the v2 OnShutdown ordering.
func (a *App) ServiceShutdown() error {
	QuitTray()
	a.Shutdown(a.ctx)
	return nil
}

// ─── Config ─────────────────────────────────────────────────────

func (a *App) GetConfig() map[string]interface{} {
	customLlamaCppMu.Lock()
	dir := customLlamaCppDir
	customLlamaCppMu.Unlock()

	modelsDirMu.Lock()
	modelDir := customModelsDir
	modelsDirMu.Unlock()

	configMu.Lock()
	theme := currentTheme
	tray := trayEnabled
	apiRoute := apiRouteMode
	sidebarCollapsed := currentSidebarCollapsed
	onboardingDismissed := currentOnboardingDismissed
	configMu.Unlock()

	downloadSourceMu.Lock()
	dsrc := downloadSource
	downloadSourceMu.Unlock()

	languageMu.Lock()
	lang := currentLanguage
	languageMu.Unlock()

	return map[string]interface{}{
		"theme":       theme,
		"llamaCppDir": dir,
		"modelsDir":   modelDir,
		// Effective download paths (default resolved to an absolute path) so
		// the frontend can show the full location even when the user never
		// configured a custom download path.
		"llamaCppDownloadDir": absDir(llamaCppDownloadDir()),
		"modelDownloadDir":    absDir(effectiveModelDownloadDir()),
		"downloadSource":      dsrc,
		"language":            lang,                // raw language preference: zh / en / auto
		"resolvedLanguage":    effectiveLanguage(), // effective language: zh / en (auto follows system detection)
		"trayEnabled":         tray,                // Windows system tray toggle
		"apiRouteMode":        apiRoute,            // API-route (headless) mode toggle
		"sidebarCollapsed":    sidebarCollapsed,    // sidebar collapsed state (default true = collapsed)
		"onboardingDismissed": onboardingDismissed, // quick-start checklist dismissed / auto-completed (default false = visible)
	}
}

// absDir resolves dir to an absolute path for display; returns dir unchanged
// when the resolution fails or dir is empty. Used to surface the effective
// download paths (defaults are cwd-relative, e.g. LLM-Models) as full paths.
func absDir(dir string) string {
	if dir == "" {
		return ""
	}
	if abs, err := filepath.Abs(dir); err == nil {
		return abs
	}
	return dir
}

// GetDownloadSource returns the current model download source ("hf" | "modelscope").
func (a *App) GetDownloadSource() string {
	return activeDownloadSource()
}

// SetDownloadSource sets the model download source: only hf / huggingface /
// modelscope allowlist values are permitted; illegal values return an error
// without mutating state; valid values are written to the global and persisted.
func (a *App) SetDownloadSource(source string) error {
	if source != sourceHF && source != sourceHuggingFace && source != sourceModelScope {
		return fmt.Errorf(tr("非法下载源 %q：仅允许 hf、huggingface 或 modelscope", "invalid download source %q: only hf, huggingface or modelscope are allowed"), source)
	}
	downloadSourceMu.Lock()
	downloadSource = source
	downloadSourceMu.Unlock()
	saveConfig()
	return nil
}

// SetLanguage sets the UI language: only zh / en / auto allowlist values are
// permitted; illegal values return an error without mutating state; valid
// values are written to the global and persisted, and the effective language
// is returned (zh/en; auto resolves to the system detection result). The
// frontend uses the return value to refresh UI text immediately, avoiding a
// second read from GetConfig.
func (a *App) SetLanguage(language string) (string, error) {
	switch language {
	case "zh", "en", "auto":
	default:
		return "", fmt.Errorf(tr("非法语言 %q：仅允许 zh/en/auto", "invalid language %q: only zh/en/auto"), language)
	}
	languageMu.Lock()
	currentLanguage = language
	languageMu.Unlock()
	saveConfig()
	return effectiveLanguage(), nil
}

func (a *App) SetTheme(theme string) {
	configMu.Lock()
	currentTheme = theme
	configMu.Unlock()
	saveConfig()

	// Update window background to match theme
	applyThemeBackground(theme)
}

// SetSidebarCollapsed sets the sidebar collapsed/expanded preference: writes
// to global state and persists to config; no side effects like window
// background changes (pure UI preference, mirroring the minimal form of
// SetTheme).
func (a *App) SetSidebarCollapsed(collapsed bool) {
	configMu.Lock()
	currentSidebarCollapsed = collapsed
	configMu.Unlock()
	saveConfig()
}

// SetOnboardingDismissed marks the Home page quick-start checklist as
// dismissed (manually closed or auto-completed): writes to global state and
// persists to config. Pure UI preference with no side effects, mirroring
// SetSidebarCollapsed.
func (a *App) SetOnboardingDismissed(dismissed bool) {
	configMu.Lock()
	currentOnboardingDismissed = dismissed
	configMu.Unlock()
	saveConfig()
}

// trayPlatformSupported reports whether this platform runs the system tray:
// Windows (taskbar) and macOS (NSStatusItem) only. Linux is excluded — DBUS
// StatusNotifierItem support depends heavily on the desktop environment
// (GNOME shows nothing by default), so the tray would promise something the
// platform may not deliver. Seam var (platformGOOS) so tests can drive the
// per-OS gate from a single test binary.
var trayPlatformSupported = func() bool {
	return platformGOOS == "windows" || platformGOOS == "darwin"
}

// SetTrayEnabled sets the system tray toggle: persists the preference to
// config; on Windows and macOS it starts/stops the tray according to the
// persisted value, on other platforms it only persists (the callers no-op —
// see trayPlatformSupported). Concurrency-safe (configMu and trayMu guard
// global state), called by the frontend settings page system tray toggle.
//
// Note: the tray is one-shot per process (the old fyne.io/systray had a
// package-level quitOnce, and the v3 SystemTray tray deliberately keeps the
// same semantics — see the trayStarted comment in core/tray.go) — so when
// enabling the tray, InitTray is only called to start a tray that has not
// started yet (idempotent); when disabling the tray, QuitTray removes the
// icon and the tray will not restart in this process. Re-enabling takes
// effect after an app restart.
func (a *App) SetTrayEnabled(enabled bool) error {
	configMu.Lock()
	trayEnabled = enabled
	configMu.Unlock()

	if trayPlatformSupported() {
		if enabled {
			// Enable: systray cannot be Run twice in the same process; only
			// start the tray if it has never been started (idempotent)
			InitTray(a.ctx, TrayIcon)
		} else {
			// Disable: remove the tray icon (systray.Quit is guaranteed to
			// take effect only once via quitOnce)
			QuitTray()
		}
	}
	saveConfig()
	log.Printf("[INFO] trayEnabled set to %v", enabled)
	return nil
}

// SetApiRouteMode toggles API-route (headless) mode. Enabling persists the
// preference, hands a running llama-server over to the successor process
// (handover file), relaunches the app with --headless, marks the upcoming
// shutdown as a mode switch (Shutdown then keeps the service and downloads
// alive) and quits the GUI — the WebView2 process tree is released and only
// the Go backend + tray + llama-server remain. Disabling just persists the
// flag: the GUI toggle always starts from false (the headless "Show Main
// Window" path resets it itself before relaunching the GUI). Enabling is
// rejected while the system tray is disabled: the tray menu is the only way
// back from headless mode, so without it the headless process would have no
// visible entry point at all. Dev builds (wails dev) reject enabling
// outright: the relaunch-based switch escapes the wails dev supervisor and
// tears the dev session down (binary lock + dead vite server), so the
// feature must be exercised on a `wails build` production binary.
func (a *App) SetApiRouteMode(enabled bool) error {
	if enabled && isDevBuild {
		return errors.New(tr("wails dev 开发构建不支持开启 API 路由模式：切换会重启进程并终止开发会话，请使用 wails build 的正式构建测试", "API route mode cannot be enabled in a wails dev build: the switch restarts the process and ends the dev session — test with a wails build production binary"))
	}
	if enabled && !TrayEnabled() {
		return errors.New(tr("需先启用系统托盘，才能开启 API 路由模式（托盘菜单是后台模式的唯一返回入口）", "enable the system tray before turning on API route mode (the tray menu is the only way back from headless mode)"))
	}

	configMu.Lock()
	apiRouteMode = enabled
	configMu.Unlock()
	saveConfig()
	log.Printf("[INFO] apiRouteMode set to %v", enabled)

	if !enabled {
		return nil
	}

	// Hand the running llama-server over so the headless successor adopts it
	// without an interruption; no file is written when no server runs.
	if err := writeServerHandover(); err != nil {
		log.Printf("[WARN] %v", err)
	}

	if err := relaunchSelf("--headless"); err != nil {
		// Relaunch failed: report and keep the GUI running as before (the
		// preference stays persisted; the switch-restart marker is only set
		// after a successful relaunch, so a later shutdown stops the server
		// normally).
		return fmt.Errorf(tr("拉起后台模式失败: %w", "failed to relaunch in headless mode: %w"), err)
	}

	switchRestartPending.Store(true)
	quitApp()
	return nil
}

// SetModelsDir sets the custom model directory: valid directories are written
// to global state, persisted to config, and the model cache is invalidated
// (next GetModels rescans with the new directory); illegal input returns an
// error without mutating state.
func (a *App) SetModelsDir(dir string) error {
	if dir == "" {
		return errors.New(tr("模型目录不能为空", "models directory cannot be empty"))
	}
	fi, err := os.Stat(dir)
	if err != nil {
		return fmt.Errorf(tr("模型目录不存在: %s", "models directory does not exist: %s"), dir)
	}
	if !fi.IsDir() {
		return fmt.Errorf(tr("模型目录不是有效目录: %s", "models directory is not a valid directory: %s"), dir)
	}
	modelsDirMu.Lock()
	customModelsDir = dir
	modelsDirMu.Unlock()
	invalidateModelCache()
	saveConfig()
	return nil
}

// BrowseModelsDir opens a system directory chooser to pick the model
// directory; returns empty string and nil on cancel, and on success performs
// the same validation and write as SetModelsDir and returns the chosen
// directory.
func (a *App) BrowseModelsDir() (string, error) {
	dir, err := pickDirectory(tr("选择模型目录", "Select Models Directory"))
	if err != nil || dir == "" {
		return "", err
	}
	if err := a.SetModelsDir(dir); err != nil {
		return "", err
	}
	return dir, nil
}

// SetModelDownloadDir sets the directory new model downloads land in: any
// non-empty path is accepted (a fresh path is a valid target for the next
// download), written to the global, persisted, and the model cache is
// invalidated so the merged model list reflects the new download root.
func (a *App) SetModelDownloadDir(dir string) error {
	if dir == "" {
		return errors.New(tr("模型下载路径不能为空", "model download path cannot be empty"))
	}
	modelDownloadDirMu.Lock()
	modelDownloadDirOverride = dir
	modelDownloadDirMu.Unlock()
	invalidateModelCache()
	saveConfig()
	return nil
}

// BrowseModelDownloadDir opens a system directory chooser to pick the model
// download path; returns empty string and nil on cancel, and on success
// performs the same write as SetModelDownloadDir and returns the chosen
// directory.
func (a *App) BrowseModelDownloadDir() (string, error) {
	dir, err := pickDirectory(tr("选择模型下载路径", "Select Model Download Path"))
	if err != nil || dir == "" {
		return "", err
	}
	if err := a.SetModelDownloadDir(dir); err != nil {
		return "", err
	}
	return dir, nil
}

// SetLlamaCppDownloadDir sets the directory new llama.cpp downloads are
// extracted into: any non-empty path is accepted, written to the global,
// persisted, and the llama.cpp detection cache is invalidated so the new
// install is picked up immediately.
func (a *App) SetLlamaCppDownloadDir(dir string) error {
	if dir == "" {
		return errors.New(tr("llama.cpp 下载路径不能为空", "llama.cpp download path cannot be empty"))
	}
	llamaCppDownloadDirMu.Lock()
	llamaCppDownloadDirOverride = dir
	llamaCppDownloadDirMu.Unlock()
	// Invalidate under llamaMu (same discipline as invalidateModelCache /
	// modelsMu) so the flag flip is serialized with the snapshot rewrite in
	// GetLlamaCpp's slow path.
	llamaMu.Lock()
	llamaCacheValid.Store(false)
	llamaMu.Unlock()
	saveConfig()
	return nil
}

// BrowseLlamaCppDownloadDir opens a system directory chooser to pick the
// llama.cpp download path; returns empty string and nil on cancel, and on
// success performs the same write as SetLlamaCppDownloadDir and returns the
// chosen directory.
func (a *App) BrowseLlamaCppDownloadDir() (string, error) {
	dir, err := pickDirectory(tr("选择 llama.cpp 下载路径", "Select llama.cpp Download Path"))
	if err != nil || dir == "" {
		return "", err
	}
	if err := a.SetLlamaCppDownloadDir(dir); err != nil {
		return "", err
	}
	return dir, nil
}

// ─── System Info ─────────────────────────────────────────────────

func (a *App) GetSystemInfo() *SystemInfo {
	s := systemInfo()
	return &s
}

func (a *App) GetCPU() *CPUInfo {
	s := systemInfo()
	return &s.CPU
}

func (a *App) GetMemory() *MemoryInfo {
	s := systemInfo()
	return &s.Memory
}

func (a *App) GetGPU() []GPUInfo {
	s := systemInfo()
	return s.GPU
}

func (a *App) GetCUDA() *CUDAInfo {
	s := systemInfo()
	return &s.CUDA
}

// llamaMu guards the cachedLlamaCpp snapshot, mirroring modelsMu for the
// model cache: the atomic llamaCacheValid flag gates the fast path, the slow
// path rechecks under the lock so concurrent first calls run getLlamaCppInfo
// only once, and readers copy the struct out under the lock so callers never
// hold a pointer into the shared cache.
var llamaMu sync.Mutex

// GetLlamaCpp returns the llama.cpp detection snapshot. The slow path
// double-checks llamaCacheValid under llamaMu (concurrent first callers used
// to write the multi-field cachedLlamaCpp struct unsynchronized); the cached
// struct is copied out under the lock so a later invalidate + rescan never
// rewrites a snapshot a caller still holds. Mirrors GetModels.
func (a *App) GetLlamaCpp() *LlamaCppInfo {
	if !llamaCacheValid.Load() {
		llamaMu.Lock()
		if !llamaCacheValid.Load() {
			cachedLlamaCpp = getLlamaCppInfo()
			llamaCacheValid.Store(true)
		}
		llamaMu.Unlock()
	}
	llamaMu.Lock()
	out := cachedLlamaCpp
	llamaMu.Unlock()
	return &out
}

func (a *App) GetOS() map[string]string {
	return map[string]string{"os": runtime.GOOS, "arch": runtime.GOARCH}
}

// GetSafeArea returns the current system-bar insets in physical pixels
// (Android only: status-bar/cutout band on top, gesture/nav bar on the
// bottom; zeros on every other platform). Feeds the frontend's safe-area CSS
// layer, which takes the max of this and the env(safe-area-inset-*) insets —
// a zero result stays a no-op on desktop.
func (a *App) GetSafeArea() SafeArea {
	return currentSafeArea()
}

func (a *App) GetDisk() *DiskUsage {
	// sampleDiskUsage returns nil on failure, so this is safe.
	return sampleDiskUsage()
}

// ─── Models ──────────────────────────────────────────────────────

func (a *App) GetModels() []ModelInfo {
	// Rescan when cache is invalid (#4): fast path reads the atomic flag
	// first, slow path rechecks under modelsMu to avoid duplicate scans on
	// concurrent first calls; cachedModels is only written under the lock.
	if !modelsCacheValid.Load() {
		modelsMu.Lock()
		if !modelsCacheValid.Load() {
			log.Println("[INFO] Scanning LLM-Models ...")
			cachedModels = scanModels()
			modelsCacheValid.Store(true)
			log.Printf("[OK] Found %d models", len(cachedModels))
		}
		modelsMu.Unlock()
	}
	// Copy the slice under modelsMu before returning, so the caller and a
	// later RefreshModels rescan (which writes cachedModels) never race on
	// the same underlying array (#4).
	modelsMu.Lock()
	out := make([]ModelInfo, len(cachedModels))
	copy(out, cachedModels)
	modelsMu.Unlock()
	return out
}

func (a *App) RefreshModels() []ModelInfo {
	invalidateModelCache()
	return a.GetModels()
}

func (a *App) GetModelConfig(modelID string) ModelConfig {
	modelConfigsMu.Lock()
	cfg, ok := cachedModelConfigs[modelID]
	modelConfigsMu.Unlock()
	if !ok {
		return defaultModelConfig()
	}
	return cfg
}

func (a *App) SaveModelConfig(modelID string, config ModelConfig) error {
	// Validate string fields against allowlists to prevent illegal values
	// (including INI injection payloads) from entering config and being
	// written directly into the INI preset (#9 first defense layer).
	if !validGPULayersValue(config.GPULayers) {
		return fmt.Errorf(tr("非法 GPULayers %q：仅允许 auto/all/0 或正整数", "invalid GPULayers %q: only auto/all/0 or a positive integer"), config.GPULayers)
	}
	if !validCacheTypeValue(config.CacheTypeK) {
		return fmt.Errorf(tr("非法 CacheTypeK %q：仅允许 f32/f16/bf16/q8_0/q4_0/q4_1/iq4_nl/q5_0/q5_1", "invalid CacheTypeK %q: only f32/f16/bf16/q8_0/q4_0/q4_1/iq4_nl/q5_0/q5_1"), config.CacheTypeK)
	}
	if !validCacheTypeValue(config.CacheTypeV) {
		return fmt.Errorf(tr("非法 CacheTypeV %q：仅允许 f32/f16/bf16/q8_0/q4_0/q4_1/iq4_nl/q5_0/q5_1", "invalid CacheTypeV %q: only f32/f16/bf16/q8_0/q4_0/q4_1/iq4_nl/q5_0/q5_1"), config.CacheTypeV)
	}
	// b10342 new-parameter validation: LoadMode/SplitMode/RopeScaling use
	// allowlists; TensorSplit uses INI injection defense (reject if it
	// contains newlines or leading/trailing whitespace).
	if !validLoadModeValue(config.LoadMode) {
		return fmt.Errorf(tr("非法 LoadMode %q：仅允许 none/mmap/mlock/mmap+mlock/dio", "invalid LoadMode %q: only none/mmap/mlock/mmap+mlock/dio"), config.LoadMode)
	}
	if !validSplitModeValue(config.SplitMode) {
		return fmt.Errorf(tr("非法 SplitMode %q：仅允许 none/layer/row/tensor", "invalid SplitMode %q: only none/layer/row/tensor"), config.SplitMode)
	}
	if !validRopeScalingValue(config.RopeScaling) {
		return fmt.Errorf(tr("非法 RopeScaling %q：仅允许 none/linear/yarn", "invalid RopeScaling %q: only none/linear/yarn"), config.RopeScaling)
	}
	if !validIniValue(config.TensorSplit) {
		return fmt.Errorf(tr("非法 TensorSplit %q：不能包含换行或首尾空白", "invalid TensorSplit %q: must not contain newlines or leading/trailing whitespace"), config.TensorSplit)
	}
	// Explicit mmproj path override: non-empty must pass INI injection defense
	// (reject if it contains newlines or leading/trailing whitespace); file
	// existence is not required (models may move). Empty means keep auto-detect.
	if !validIniValue(config.MMProj) {
		return fmt.Errorf(tr("非法 MMProj %q：不能包含换行或首尾空白", "invalid MMProj %q: must not contain newlines or leading/trailing whitespace"), config.MMProj)
	}
	// spec-type allowlist: empty, draft-mtp, or the n-gram self-speculation
	// types (no draft model needed).
	if !validSpecTypeValue(config.SpecType) {
		return fmt.Errorf(tr("非法 SpecType %q：仅允许 draft-mtp / ngram-simple / ngram-mod", "invalid SpecType %q: only draft-mtp / ngram-simple / ngram-mod"), config.SpecType)
	}
	if config.SpecDraftNMax < 0 {
		return fmt.Errorf(tr("非法 SpecDraftNMax %d：不能为负数", "invalid SpecDraftNMax %d: must not be negative"), config.SpecDraftNMax)
	}
	if config.MainGPU < 0 {
		return fmt.Errorf(tr("非法 MainGPU %d：不能为负数", "invalid MainGPU %d: must not be negative"), config.MainGPU)
	}
	if config.NCpuMoe < 0 {
		return fmt.Errorf(tr("非法 NCpuMoe %d：不能为负数", "invalid NCpuMoe %d: must not be negative"), config.NCpuMoe)
	}
	if config.RopeScale < 0 {
		return fmt.Errorf(tr("非法 RopeScale %g：不能为负数", "invalid RopeScale %g: must not be negative"), config.RopeScale)
	}

	modelConfigsMu.Lock()
	cachedModelConfigs[modelID] = config
	modelConfigsMu.Unlock()
	saveConfig()
	return nil
}

// TuneModelConfig computes hardware-aware optimal inference parameters for the
// model (GGUF metrics + GPU/CPU/RAM snapshot), persists them via SaveModelConfig
// validation, and returns the applied config.
func (a *App) TuneModelConfig(modelID string) (ModelConfig, error) {
	var model *ModelInfo
	models := scanModels()
	for i := range models {
		if models[i].Name == modelID {
			model = &models[i]
			break
		}
	}
	if model == nil {
		return ModelConfig{}, fmt.Errorf(tr("未找到模型 %q", "model %q not found"), modelID)
	}

	metrics, ok := readGGUFModelMetrics(model.Path)
	if !ok {
		log.Println(tuneFallbackLogLine(modelID))
	}
	tm := buildTuneModel(metrics, ok, tuneWeightsBytes(*model))
	cfg := tuneModelConfig(a.tuneHardware(), tm)
	if err := a.SaveModelConfig(modelID, cfg); err != nil {
		return ModelConfig{}, fmt.Errorf(tr("保存调优参数失败: %w", "failed to save tuned config: %w"), err)
	}
	log.Printf("[OK] tune: %s -> gpuLayers=%s ctx=%d flashAttn=%v cacheK=%q threads=%d",
		modelID, cfg.GPULayers, cfg.CtxSize, cfg.FlashAttn, cfg.CacheTypeK, cfg.Threads)
	return cfg, nil
}

// ─── Server ──────────────────────────────────────────────────────

func (a *App) GetServerConfig() ServerConfig {
	serverConfigMu.Lock()
	cfg := cachedServerConfig
	serverConfigMu.Unlock()
	return cfg
}

func (a *App) SaveServerConfig(cfg ServerConfig) error {
	// Access mode allowlist: only local/lan are allowed, preventing arbitrary
	// host values from exposing the inference service to the LAN / internet
	// (#5). After passing, Host is derived by effectiveHost from AccessMode;
	// the host field from the frontend is not trusted.
	if cfg.AccessMode != accessLocal && cfg.AccessMode != accessLAN {
		return fmt.Errorf(tr("非法访问范围 %q：仅允许 local/lan", "invalid access mode %q: only local/lan"), cfg.AccessMode)
	}
	cfg.Host = effectiveHost(cfg.AccessMode)
	// DeviceID (serving-GPU selection) allowlist: empty means auto (CUDA
	// default device order); non-empty must exactly match (case-sensitive) the
	// UUID of a GPU from the current probe list (gpuListSource, the nvidia-smi
	// detection chain), preventing arbitrary CUDA_VISIBLE_DEVICES values from
	// reaching the llama-server child environment.
	if cfg.DeviceID != "" {
		known := false
		for _, g := range gpuListSource() {
			if g.UUID == cfg.DeviceID {
				known = true
				break
			}
		}
		if !known {
			return fmt.Errorf(tr("非法推理显卡 %q：必须是显卡检测列表（nvidia-smi）中的 UUID", "invalid inference GPU %q: must be a UUID from the detected GPU list (nvidia-smi probe)"), cfg.DeviceID)
		}
	}
	// APIKey normalization only (no charset restriction): trimmed whitespace in,
	// empty means authentication disabled.
	cfg.APIKey = strings.TrimSpace(cfg.APIKey)
	if cfg.Port < 1024 || cfg.Port > 65535 {
		return fmt.Errorf(tr("非法 Port %d：端口范围应为 1024-65535", "invalid Port %d: port must be in range 1024-65535"), cfg.Port)
	}
	if cfg.MaxModels < 1 {
		return fmt.Errorf(tr("非法 MaxModels %d：至少为 1", "invalid MaxModels %d: must be at least 1"), cfg.MaxModels)
	}
	if cfg.CacheRAM < 0 {
		return fmt.Errorf(tr("非法 CacheRAM %d：不能为负数", "invalid CacheRAM %d: must not be negative"), cfg.CacheRAM)
	}

	serverConfigMu.Lock()
	cachedServerConfig = cfg
	serverConfigMu.Unlock()
	saveConfig()
	return nil
}

func (a *App) GetServerStatus() map[string]interface{} {
	// serverRunning is guarded by serverMu (#3); serverLogs is guarded by
	// serverLogsMu.
	serverMu.Lock()
	running := serverRunning
	serverMu.Unlock()
	serverLogsMu.Lock()
	logs := make([]string, len(serverLogs))
	for i, e := range serverLogs {
		logs[i] = e.text
	}
	serverLogsMu.Unlock()

	return map[string]interface{}{
		"running": running,
		"log":     logs,
	}
}

// ServerLogEntry is one incremental server-log line returned to the frontend:
// Seq is the line's monotonic ring cursor value, Text the raw line content.
type ServerLogEntry struct {
	Seq  int64  `json:"seq"`
	Text string `json:"text"`
}

// ServerLogsPage bundles one incremental log page: the entries appended since
// the request cursor plus the next cursor to pass on the following call. It
// travels as a single struct because the Wails v2 bridge only forwards a
// bound method's first non-error return value — a second, non-error return
// would be silently dropped.
type ServerLogsPage struct {
	Entries []ServerLogEntry `json:"entries"`
	Next    int64            `json:"next"`
}

// GetServerLogsSince returns the retained log lines with Seq >= since plus
// the next cursor value for the following call (a since of 0 returns
// everything retained, up to the ring cap of serverLogsCap lines). Lines
// already evicted from the ring cannot be recovered: the caller detects that
// case (next - since > serverLogsCap) and refetches from 0.
func (a *App) GetServerLogsSince(since int64) (ServerLogsPage, error) {
	entries, next := serverLogsSince(since)
	out := make([]ServerLogEntry, len(entries))
	for i, e := range entries {
		out[i] = ServerLogEntry{Seq: e.seq, Text: e.text}
	}
	return ServerLogsPage{Entries: out, Next: next}, nil
}

func (a *App) StartServer() error {
	// Already running is idempotent returning nil (#3): serverRunning is
	// guarded by serverMu.
	serverMu.Lock()
	running := serverRunning
	serverMu.Unlock()
	if running {
		return nil
	}

	return startServerInternal()
}

// StartServerWithModel starts the service for one specific model. Desktop
// (router mode) ignores the model and keeps the StartServer already-running
// no-op; on Android (direct mode) the model becomes the single resident: a
// running server with the same model is a no-op, a different one restarts
// the service. Idempotent on both platforms.
func (a *App) StartServerWithModel(model string) error {
	if platformGOOS != "android" {
		serverMu.Lock()
		running := serverRunning
		serverMu.Unlock()
		if running {
			return nil
		}
	}
	return startServerInternalWithModel(model)
}

func (a *App) StopServer() error {
	return stopServerInternal()
}

// ─── LlamaCpp Download ───────────────────────────────────────────

func (a *App) GetLlamaCppDownloadStatus() *DownloadState {
	downloadMu.Lock()
	ds := *downloadState
	downloadMu.Unlock()
	return &ds
}

func (a *App) StartLlamaCppDownload() error {
	startLlamaCppDownload()
	return nil
}

func (a *App) PauseLlamaCppDownload() {
	downloadMu.Lock()
	downloadState.Status = "paused"
	downloadState.Paused = true
	downloadResumeCh = make(chan struct{}, 1) // fresh channel for resume signal
	downloadMu.Unlock()
}

func (a *App) ResumeLlamaCppDownload() {
	downloadMu.Lock()
	downloadState.Paused = false
	if downloadState.Status == "paused" {
		downloadState.Status = "downloading"
	}
	// Signal resume (non-blocking send to buffered channel)
	select {
	case downloadResumeCh <- struct{}{}:
	default:
	}
	downloadMu.Unlock()
}

func (a *App) StopLlamaCppDownload() {
	// downloadCancel is guarded by downloadMu: take a local copy under the
	// lock, then call outside (#3).
	downloadMu.Lock()
	cancelDownload := downloadCancel
	downloadMu.Unlock()
	if cancelDownload != nil {
		cancelDownload()
	}
}

func (a *App) BrowseLlamaCppDir() (string, error) {
	dir, err := pickDirectory(tr("选择 llama.cpp 目录", "Select llama.cpp Directory"))
	if err != nil || dir == "" {
		return "", err
	}
	customLlamaCppMu.Lock()
	customLlamaCppDir = dir
	customLlamaCppMu.Unlock()
	// Invalidate under llamaMu (same discipline as invalidateModelCache /
	// modelsMu) so the flag flip is serialized with the snapshot rewrite in
	// GetLlamaCpp's slow path.
	llamaMu.Lock()
	llamaCacheValid.Store(false)
	llamaMu.Unlock()
	saveConfig()
	return dir, nil
}

// ─── App Update ──────────────────────────────────────────────────

// GetAppVersion returns the current app version (aligned with GitHub release
// tags).
func (a *App) GetAppVersion() string {
	return currentVersion
}

// CheckForUpdate queries the remote repo latest release and returns whether a
// new version exists along with version info.
func (a *App) CheckForUpdate() (*UpdateCheckResult, error) {
	return CheckForUpdateAt(updateRepoAPI)
}

// StartUpdateDownload starts downloading the new version exe to the same
// directory as the executable. The version is passed by the caller (from the
// CheckForUpdate result) and is used for target file naming.
func (a *App) StartUpdateDownload(version string) error {
	if compareVersions(version, currentVersion) <= 0 {
		return fmt.Errorf(tr("没有可更新的版本: %s", "no update available for version %s"), version)
	}
	updateDownloadMu.Lock()
	inProgress := updateDownloadState.Status == "downloading"
	updateDownloadMu.Unlock()
	if inProgress {
		return errors.New(tr("更新下载已在进行中", "update download already in progress"))
	}
	go downloadUpdateRelease(version)
	return nil
}

// GetUpdateDownloadStatus returns the current state and progress of the
// update download.
func (a *App) GetUpdateDownloadStatus() *UpdateDownloadState {
	updateDownloadMu.Lock()
	ds := *updateDownloadState
	updateDownloadMu.Unlock()
	return &ds
}

// StopUpdateDownload cancels the in-flight update download (user-initiated
// stop / app exit).
func (a *App) StopUpdateDownload() {
	updateDownloadMu.Lock()
	cancel := updateDownloadCancel
	updateDownloadMu.Unlock()
	if cancel != nil {
		cancel()
	}
}

// InstallUpdate launches the downloaded setup installer and exits the app so
// the installer can complete the update (user confirmed "install now" in the
// update modal). Only valid for a completed download of the installer
// artifact; see installUpdateNow for the guards.
func (a *App) InstallUpdate() error {
	return installUpdateNow(quitApp)
}

// ─── Downloads (HF Mirror / ModelScope) ──────────────────────────

// SearchDownloads routes the search by current download source: modelscope
// → searchModelScopeAt, otherwise (default hf) → searchHFMirror.
func (a *App) SearchDownloads(query, filter string) ([]HFSearchResult, error) {
	if activeDownloadSource() == sourceModelScope {
		return searchModelScope(query)
	}
	return searchHFMirror(query, filter)
}

// GetModelFiles routes the file list by current download source: modelscope
// → listModelScopeFiles, otherwise (default hf) → getHFModelFiles.
func (a *App) GetModelFiles(modelID string) ([]HFFileOut, error) {
	if activeDownloadSource() == sourceModelScope {
		return listModelScopeFiles(modelID)
	}
	return getHFModelFiles(modelID)
}

// GetModelMaxFileSize returns the largest GGUF file size for a model (shown
// on the search card). modelscope source derives it from the file list; hf
// source uses the details endpoint (blobs=true for real sizes).
func (a *App) GetModelMaxFileSize(modelID string) (int64, error) {
	if activeDownloadSource() == sourceModelScope {
		files, err := listModelScopeFiles(modelID)
		if err != nil {
			return 0, err
		}
		var max int64
		for _, f := range files {
			if f.Size > max {
				max = f.Size
			}
		}
		return max, nil
	}
	return getHFModelMaxGGUFSize(modelID)
}

// GetModelDescription fetches the natural-language description from a model's
// README (shown on the download page). Routed to HF or ModelScope README
// endpoint by current download source.
func (a *App) GetModelDescription(modelID string) (string, error) {
	if activeDownloadSource() == sourceModelScope {
		return getModelScopeDescription(modelID)
	}
	return getModelDescription(modelID)
}

func (a *App) StartDownload(modelID string, files []string) error {
	return startHFDownload(modelID, files)
}

func (a *App) GetDownloadTasks() []DlTask {
	dlTasksMu.Lock()
	defer dlTasksMu.Unlock()
	tasks := make([]DlTask, 0, len(dlTasks))
	for _, t := range dlTasks {
		tasks = append(tasks, *t)
	}
	return tasks
}

func (a *App) CancelDownloadTask(id string) error {
	dlTasksMu.Lock()
	var found bool
	for _, t := range dlTasks {
		if t.ID == id {
			// Set cancelled status inside the lock, then cancel(): for terminal
			// tasks (error/cancelled, goroutine already exited) clicking cancel
			// takes effect immediately so the frontend sees the cancelled state
			// on next poll; for downloading tasks, downloadTask's ctx.Done()
			// branch also sets cancelled, so both paths agree.
			t.Status = "cancelled"
			if t.cancel != nil {
				t.cancel()
			}
			found = true
			break
		}
	}
	dlTasksMu.Unlock()
	// Persist after state change: must happen after unlocking (saveConfig
	// acquires dlTasksMu again at the end).
	if found {
		persistTasksNow()
	}
	return nil
}

func (a *App) PauseDownloadTask(id string) error {
	dlTasksMu.Lock()
	var found bool
	for _, t := range dlTasks {
		if t.ID == id && t.Status == "downloading" {
			t.Status = "paused"
			t.resumeCh = make(chan struct{}, 1) // fresh channel for resume signal
			found = true
			break
		}
	}
	dlTasksMu.Unlock()
	if found {
		persistTasksNow()
	}
	return nil
}

// ResumeDownloadTask resumes a paused download task. Two paused flavors are
// told apart by the runtime running flag: a task paused in this process has a
// live goroutine parked in waitForTaskResume — flip the status and signal it
// to continue. A task restored from the persisted queue after a restart has
// NO goroutine (loadConfig never spawns one; signaling its fresh resumeCh
// would reach nobody and leave the task stuck on "downloading" at its old
// progress forever — a zombie only cancellable by the user), so it takes the
// same rebuild-ctx + respawn path as RetryDownloadTask; downloadTask resumes
// from the .part size already on disk.
func (a *App) ResumeDownloadTask(id string) error {
	dlTasksMu.Lock()
	var found bool
	for _, t := range dlTasks {
		if t.ID == id && t.Status == "paused" {
			if t.running {
				// User-paused: the goroutine is parked on resumeCh.
				t.Status = "downloading"
				// Signal resume (non-blocking send to buffered channel)
				select {
				case t.resumeCh <- struct{}{}:
				default:
				}
			} else {
				// Restart-restored: nobody waits on resumeCh; restart the
				// goroutine (retryDownloadTask rebuilds ctx and requires
				// dlTasksMu held).
				retryDownloadTask(t)
			}
			found = true
			break
		}
	}
	dlTasksMu.Unlock()
	if found {
		persistTasksNow()
	}
	return nil
}

// RetryDownloadTask retries a download task: for finished tasks
// (error/cancelled/done) or queued tasks, it rebuilds the ctx and restarts
// the download goroutine; downloadTask checks .part file size as the resume
// offset, naturally reusing resume capability. Downloading tasks have an
// active goroutine, so retrying them is disallowed to prevent concurrent
// writes to the same .part file; paused tasks are refused here too — a
// running one is owned by ResumeDownloadTask's signal path and a
// goroutine-less (restart-restored) one by its respawn path. When the id is
// not found, returns nil silently, matching CancelDownloadTask semantics.
func (a *App) RetryDownloadTask(id string) error {
	dlTasksMu.Lock()
	var found bool
	for _, t := range dlTasks {
		if t.ID == id {
			if t.Status == "downloading" || t.Status == "paused" {
				found = true // no retry, but no state change here so no persist
				break
			}
			retryDownloadTask(t)
			found = true
			break
		}
	}
	dlTasksMu.Unlock()
	// Persist after state change (retryDownloadTask sets Status=queued and
	// starts the goroutine).
	if found {
		persistTasksNow()
	}
	return nil
}

// GetMonitorStatus returns the realtime monitoring sample snapshot (CPU/
// memory / GPU are cached by the background sampler; ServerRunning / TPS /
// UptimeSeconds are fetched live).
func (a *App) GetMonitorStatus() *MonitorStatus {
	return GetMonitorStatus()
}

// ─── Router Models ──────────────────────────────────────────────────

// GetLoadedModels queries the llama-server router for the list of loaded /
// loading / sleeping models. Returns nil when the server is not running
// (frontend wails.ts normalizes to an empty array); on query failure it logs
// a warning and returns nil (transient connection issues are not reported to
// the frontend, retried by polling).
func (a *App) GetLoadedModels() []LoadedModel {
	port := getServerPort()
	if port == 0 {
		return nil
	}
	models, err := fetchRouterModels(port)
	if err != nil {
		log.Println("[WARN] GetLoadedModels failed:", err)
		return nil
	}
	return models
}

// UnloadModel sends a model unload request to llama-server. Returns error
// when the server is not running; returns invalid-parameter error when id is
// empty.
func (a *App) UnloadModel(id string) error {
	port := getServerPort()
	if port == 0 {
		return errors.New(tr("llama-server 未运行", "llama-server is not running"))
	}
	if id == "" {
		return errors.New(tr("模型 id 不能为空", "model id cannot be empty"))
	}
	return unloadRouterModel(port, id)
}
