package core

import (
	"context"
	"errors"
	"fmt"
	"log"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"sync/atomic"
	"time"
)

// ─── Adopted-server state ────────────────────────────────────────

// adoptedPid records the pid of an adopted llama-server (handed over by the
// previous process during a mode switch, see core/handover.go); 0 means the
// running server (if any) is our own child tracked in serverCmd. Guarded by
// serverMu alongside serverRunning/serverCmd, preserving the invariant
// "adoptedPid > 0 ⟹ serverCmd == nil".
var adoptedPid int

// directServingModel records the resident model of a direct-mode llama-server
// (Android: one process serves exactly one model chosen at start). Set on a
// successful direct start and cleared wherever serverPort is reset (the wait
// goroutine, stopAdoptedServerIfAny), guarded by serverMu alongside it.
// Desktop (router mode) always leaves it "" — models there lazy-load instead.
var directServingModel string

// ─── Wails binding helpers ───────────────────────────────────────

// osInterrupt is os.Interrupt on Unix, os.Kill on Windows.
var osInterrupt = func() os.Signal {
	if runtime.GOOS == "windows" {
		return os.Kill
	}
	return os.Interrupt
}()

// serverDone is the per-start completion channel of the running child: the
// wait goroutine in startServerInternal closes it after cmd.Wait returns and
// the lifecycle state is cleared under serverMu (close happens strictly after
// that critical section, so a graceful-stop waiter never races the state
// cleanup). Guarded by serverMu alongside serverCmd — a fresh channel is
// registered per start and cleared together with serverCmd; nil means no
// completion handle exists (adopted servers never reach the graceful path,
// and forged test state may omit it).
var serverDone chan struct{}

// stopGrace is the bounded period stopServerInternal waits after a successful
// interrupt before escalating to Kill: the child must exit within it or be
// killed — force is never implicit. Declared as a var (same style as
// cmdTimeout) so tests can shorten it.
var stopGrace = 5 * time.Second

// ─── Server start/stop (extracted from HTTP handlers) ────────────

func startServerInternal() error {
	return startServerInternalWithModel("")
}

// directStartAction is the android direct-mode start decision produced by
// directStartDecision.
type directStartAction int

const (
	directStartSpawn   directStartAction = iota // not running: fresh spawn
	directStartNoop                             // running with the same resident model: no-op
	directStartRestart                          // running with a different model: stop + start
)

// directStartDecision is the pure android direct-mode start decision: a
// running server whose resident model equals the requested one makes the
// start a no-op (idempotent); a different resident model requires a stop +
// start restart; nothing running is a plain spawn. Compare CONCRETE resolved
// model names, so a repeat start with the default pick (requested resolved to
// the first scanned model) stays a no-op while that model is resident.
func directStartDecision(running bool, resident, requested string) directStartAction {
	if !running {
		return directStartSpawn
	}
	if resident == requested {
		return directStartNoop
	}
	return directStartRestart
}

// resolveDirectModel picks the direct-mode resident from the scanned models:
// the exact ModelInfo.Name match for requested, or the first scanned model
// (deterministic scan order) when requested is empty.
func resolveDirectModel(models []ModelInfo, requested string) (ModelInfo, error) {
	if len(models) == 0 {
		return ModelInfo{}, errors.New(tr("LLM-Models 目录中没有模型", "no models found in the LLM-Models directory"))
	}
	if requested == "" {
		return models[0], nil
	}
	for _, m := range models {
		// The UI addresses models by the id the API exposes — the scanned
		// alias (sanitized display name) falling back to the display name —
		// so accept either form as an exact match.
		if m.Name == requested || m.Alias == requested {
			return m, nil
		}
	}
	return ModelInfo{}, fmt.Errorf(tr("未找到模型: %s", "model not found: %s"), requested)
}

// startServerInternalWithModel starts llama-server. Desktop keeps router mode
// (models preset + lazy loading; requested is ignored — the already-running
// no-op is owned by the bindings). On Android the release llama-server ships
// without LLAMA_SUBPROCESS, so the router-models subsystem cannot initialize
// ("subprocess is not enabled on this build") — direct mode is used instead:
// one process, one model, chosen explicitly. A running direct server with the
// same resident model makes the call a no-op; a different resident model
// stops the service first, then starts with the new model.
func startServerInternalWithModel(requested string) error {
	serverConfigMu.Lock()
	cfg := cachedServerConfig
	serverConfigMu.Unlock()

	var llamaServer string
	var args []string
	var err error
	resident := "" // direct-mode resident model name (android); "" on desktop
	if platformGOOS == "android" {
		modelConfigsMu.Lock()
		cfgs := cachedModelConfigs
		modelConfigsMu.Unlock()
		m, err := resolveDirectModel(scanModels(), requested)
		if err != nil {
			return err
		}
		serverMu.Lock()
		running := serverRunning
		prev := directServingModel
		serverMu.Unlock()
		switch directStartDecision(running, prev, m.Name) {
		case directStartNoop:
			addServerLog(fmt.Sprintf("[INFO] direct mode: model %q already resident, start is a no-op", m.Name))
			return nil
		case directStartRestart:
			addServerLog(fmt.Sprintf("[INFO] direct mode: switching model %q -> %q, restarting llama-server", prev, m.Name))
			if err := stopServerInternal(); err != nil {
				return err
			}
		}
		llamaServer, args, err = buildServerCommand(cfg, "", &directModel{info: m, cfg: cfgs[m.Name]})
		if err != nil {
			return err
		}
		resident = m.Name
	} else {
		// Router mode lazy-loads models, so requested is ignored. No running
		// check here: startServerInternal keeps its historical semantics (the
		// bindings own the already-running no-op; the headless startup path
		// legitimately attempts a start while adopted state is registered).
		var presetPath string
		presetPath, err = generateModelsPreset()
		if err != nil {
			return fmt.Errorf(tr("生成模型预设失败: %w", "failed to generate models preset: %w"), err)
		}
		llamaServer, args, err = buildServerCommand(cfg, presetPath, nil)
		if err != nil {
			return err
		}
	}

	return spawnServerProcess(llamaServer, args, cfg, resident)
}

// spawnServerProcess is the shared spawn/log/tail/wait machinery behind every
// llama-server start (router and direct mode alike): builds the exec.Command,
// binds the shared log-file capture, starts the child, registers the
// lifecycle state (serverRunning/serverCmd/serverPort and, in direct mode,
// the resident model) and starts the wait goroutine that clears it. The child
// inherits the app's working directory.
func spawnServerProcess(llamaServer string, args []string, cfg ServerConfig, resident string) error {
	// Stop a leftover tailer from a previous server (e.g. an adopted one not
	// stopped through the normal path) so it cannot double-append the new
	// child's lines into the ring alongside the tailer started below.
	serverMu.Lock()
	oldTail := serverLogTail
	serverLogTail = nil
	serverMu.Unlock()
	if oldTail != nil {
		oldTail.Stop()
		oldTail.WaitDone(2 * time.Second)
	}

	// Create the command and bind log output inside serverMu (#3). Do not set
	// serverRunning=true yet: it must only be set after Start() succeeds,
	// preserving the invariant "serverRunning==true ⟹ serverCmd.Process != nil".
	serverMu.Lock()

	// Capture the child's stdout+stderr in one log file: both fds get the
	// SAME *os.File (one open file description → one shared write offset), so
	// concurrent writes never interleave mid-line. File capture (instead of
	// pipes) makes the log stream identical for a spawned child and a
	// re-adopted one: an adopted llama-server belongs to the previous, exited
	// process and has no pipe to us — but it keeps writing to this file,
	// which any process can tail (see serverLogTailer).
	logFile, err := os.OpenFile(resolveServerLogPath(), os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0644)
	if err != nil {
		serverMu.Unlock()
		return fmt.Errorf(tr("打开服务日志文件失败: %w", "failed to open server log file: %w"), err)
	}
	cmd := exec.Command(llamaServer, args...)
	hideWindow(cmd)
	// Serving-GPU pinning: when the server config selects a device by stable
	// UUID, CUDA_VISIBLE_DEVICES remaps CUDA device 0 to that card, so
	// llama-server needs no extra flag — its default device 0 IS the chosen
	// GPU. The env entry is appended after os.Environ() so it overrides any
	// inherited CUDA_VISIBLE_DEVICES (last entry wins). Empty DeviceID (auto)
	// yields nil: the child then inherits the parent environment unchanged.
	// serverChildEnv additionally carries the Android LD_LIBRARY_PATH anchor.
	cmd.Env = serverChildEnv(llamaServer, cudaDeviceEnv(cfg.DeviceID))
	cmd.Stdout = logFile
	cmd.Stderr = logFile
	serverLogsMu.Lock()
	serverLogs = nil
	serverLogsMu.Unlock()
	serverMu.Unlock()

	addServerLog(fmt.Sprintf("[INFO] Starting llama-server: %s %s", llamaServer, strings.Join(args, " ")))

	if err := cmd.Start(); err != nil {
		logFile.Close() // the child never inherited the handle; do not leak the fd
		serverMu.Lock()
		serverCmd = nil
		serverRunning = false
		serverStartTime = time.Time{}
		serverDone = nil
		serverMu.Unlock()
		addServerLog("[ERROR] Failed to start: " + err.Error())
		return err
	}
	// The child owns its inherited copy of the handle now; drop ours so each
	// start/stop cycle does not leak an fd.
	logFile.Close()

	// Tail the freshly truncated log file from offset 0 into the ring,
	// replacing the old pipe-based serverLogWriter (the ring keeps receiving
	// complete lines either way, so parseTPS is unaffected).
	tailer, err := startServerLogTailer(resolveServerLogPath(), true)
	if err != nil {
		// Log capture is best-effort: the file itself still receives the
		// child output, so only the in-app view degrades.
		log.Printf("[WARN] server log tailer failed to start: %v", err)
		tailer = nil
	}

	// done is closed by the wait goroutine below (strictly after the state
	// critical section) and is the graceful-stop completion signal that
	// stopServerInternal selects on during its bounded grace period.
	done := make(chan struct{})

	serverMu.Lock()
	serverCmd = cmd
	serverRunning = true
	serverStartTime = time.Now()
	serverTrueStart = serverStartTime
	serverPort = cfg.Port
	directServingModel = resident
	serverLogTail = tailer
	serverDone = done
	serverMu.Unlock()

	go func(cmd *exec.Cmd, tailer *serverLogTailer, done chan struct{}) {
		err := cmd.Wait()
		serverMu.Lock()
		serverRunning = false
		serverStartTime = time.Time{}
		serverPort = 0
		directServingModel = ""
		// Only clear the global references when they still point to this
		// instance, to avoid clobbering a newly started server.
		if serverCmd == cmd {
			serverCmd = nil
		}
		if serverLogTail == tailer {
			serverLogTail = nil
		}
		if serverDone == done {
			serverDone = nil
		}
		serverMu.Unlock()
		// close(done) strictly AFTER the serverMu critical section (a
		// graceful-stop waiter in stopServerInternal must resume only once
		// the lifecycle state is fully cleared, so it can never observe or
		// act on half-cleared state), and BEFORE the tailer drain (which only
		// affects the in-app log view and must never delay the stop path).
		close(done)
		// The child has exited: stop the tailer (it drains the file's final
		// bytes and flushes its assembler first, so the ring keeps the last
		// lines) — bounded wait, the tailer goroutine always terminates.
		if tailer != nil {
			tailer.Stop()
			tailer.WaitDone(2 * time.Second)
		}
		if err != nil {
			addServerLog("[WARN] llama-server exited: " + err.Error())
		} else {
			addServerLog("[INFO] llama-server stopped")
		}
	}(cmd, tailer, done)

	return nil
}

// directModel carries the resolved Android direct-mode launch target to
// buildServerCommand: the scanned model info plus its per-model config.
type directModel struct {
	info ModelInfo
	cfg  ModelConfig
}

// buildServerCommand resolves the llama-server binary (custom dir, then the
// llama-cpp/ download dir, then PATH) and builds its argument list from the
// server config. The preset path points at the generated models INI file
// (desktop router mode); on Android the direct-mode branch ignores it and
// assembles single-model serving args from d instead.
func buildServerCommand(cfg ServerConfig, presetPath string, d *directModel) (string, []string, error) {
	// Shares resolveLlamaServerBin with getLlamaCppInfo to keep llama.cpp
	// install-location resolution consistent in both places (download dir is
	// ready to serve immediately after extraction); falls back to the bare
	// binary name when not found, letting exec.Command report the error.
	llamaServer := resolveLlamaServerBin()
	if llamaServer == "" {
		llamaServer = "llama-server"
	}

	if platformGOOS == "android" {
		// The official Android llama.cpp release ships llama-server WITHOUT
		// the LLAMA_SUBPROCESS macro, so the router-models subsystem cannot
		// initialize ("failed to initialize router models: subprocess is not
		// enabled on this build"). Serve the one resolved model directly
		// instead: no --models-preset / --models-max, no lazy loading —
		// switching models restarts the process (see directStartDecision).
		modelArgs, err := modelDirectArgs(sanitizeAlias(d.info.Name), d.info, d.cfg)
		if err != nil {
			return "", nil, err
		}
		args := append([]string{
			"--host", effectiveHost(cfg.AccessMode),
			"--port", strconv.Itoa(cfg.Port),
		}, modelArgs...)
		args = append(args,
			"--cont-batching",
			"--no-webui",
		)
		if cfg.CacheRAM > 0 {
			args = append(args, "--cache-ram", strconv.Itoa(cfg.CacheRAM))
		}
		// Optional bearer-token authentication for the inference API: only
		// passed when set; empty keeps llama-server's default
		// no-authentication behavior. Both flags are valid in direct mode.
		if cfg.APIKey != "" {
			args = append(args, "--api-key", cfg.APIKey)
		}
		return llamaServer, args, nil
	}

	args := []string{
		"--host", effectiveHost(cfg.AccessMode),
		"--port", strconv.Itoa(cfg.Port),
		// No --models-dir: every model (download path + imported directory) is
		// already registered through the preset. Passing --models-dir too would
		// make llama-server auto-register the download path by directory layout,
		// duplicating each model under a second id (e.g. preset id "qwen3.5-4b"
		// plus directory-derived id "unsloth" for the same file).
		"--models-preset", presetPath,
		"--models-max", strconv.Itoa(max(cfg.MaxModels, 1)),
		"--cont-batching",
		"--no-webui",
	}
	if cfg.CacheRAM > 0 {
		args = append(args, "--cache-ram", strconv.Itoa(cfg.CacheRAM))
	}
	// Optional bearer-token authentication for the inference API: only passed
	// when set; empty keeps llama-server's default no-authentication behavior.
	if cfg.APIKey != "" {
		args = append(args, "--api-key", cfg.APIKey)
	}
	return llamaServer, args, nil
}

// platformGOOS is the OS branch selector (runtime.GOOS in production); a var
// so tests can drive the per-OS branches from a single test binary (same
// style as pathsGOOS in paths.go).
var platformGOOS = runtime.GOOS

// serverChildEnv assembles the llama-server child environment from the CUDA
// device pin (cudaDeviceEnv, windows-only; empty = plain inheritance) plus,
// on Android, the LD_LIBRARY_PATH anchor pointing at the binary's directory
// (androidLdEnv). Setting cmd.Env replaces the inherited environment
// wholesale, so any override must start from os.Environ() — and with no
// overrides at all the result stays nil so the child inherits unchanged.
func serverChildEnv(llamaServer string, cudaExtra []string) []string {
	env := cudaExtra
	if ld := androidLdEnv(llamaServer); ld != nil {
		if env == nil {
			env = os.Environ()
		}
		env = append(env, ld...)
	}
	return env
}

// cudaDeviceEnv builds the child-process environment for llama-server from the
// configured serving GPU (ServerConfig.DeviceID, a stable nvidia-smi UUID).
// A non-empty deviceID pins the child to that card by appending
// CUDA_VISIBLE_DEVICES=<uuid> after os.Environ() — the later duplicate entry
// wins, overriding any inherited value. CUDA_VISIBLE_DEVICES remaps CUDA
// device 0 to the chosen card, so llama-server needs no extra flag. An empty
// deviceID (auto / default device) returns nil so exec.Command inherits the
// parent environment unchanged (historical behavior).
//
// The pin is Windows-only: the llama.cpp builds shipped for linux/macOS are
// Vulkan / Metal / CPU, where CUDA_VISIBLE_DEVICES is meaningless (and a
// stale value could silently hide devices) — non-Windows always returns nil.
func cudaDeviceEnv(deviceID string) []string {
	if platformGOOS != "windows" {
		return nil
	}
	if deviceID == "" {
		return nil
	}
	return append(os.Environ(), "CUDA_VISIBLE_DEVICES="+deviceID)
}

// switchRestartPending marks an in-flight mode-switch restart (GUI → headless
// via SetApiRouteMode, or headless → GUI via the tray "Show Main Window"):
// the exiting process has already relaunched its successor and handed the
// running llama-server over, so Shutdown/headless-exit must NOT stop the
// service and must NOT cancel in-flight downloads (the handover window is
// short; the new process restores the persisted download queue).
var switchRestartPending atomic.Bool

// killProcessByPid is the injection point killing an arbitrary process by pid
// (same style as updateLauncher / renameFile): adopted llama-server processes
// belong to the previous, exited process — there is no child handle to
// Signal, so os.FindProcess(pid).Kill() is the only stop path. Tests replace
// this variable instead of killing real processes.
var killProcessByPid = func(pid int) error {
	proc, err := os.FindProcess(pid)
	if err != nil {
		return err
	}
	return proc.Kill()
}

// stopAdoptedServerIfAny stops a running adopted llama-server (handed over
// from another process: serverCmd nil, adoptedPid > 0): kills it by pid via
// the killProcessByPid injection point, clears the adopted state and removes
// the handover record. Graceful stop is intentionally out of scope here: the
// adopted llama-server belongs to the previous, exited process — there is no
// child handle to Signal and no wait goroutine to close a done channel, so
// killProcessByPid (Kill by pid) is the only lever; a graceful adopted-server
// stop would require OS-level signalling by pid (future work). Returns true
// when an adopted server was stopped; false when nothing adopted is running
// (callers fall back to the child-stop path).
func stopAdoptedServerIfAny() bool {
	serverMu.Lock()
	running := serverRunning
	cmd := serverCmd
	adopted := adoptedPid
	serverMu.Unlock()
	if !running || cmd != nil || adopted <= 0 {
		return false
	}

	addServerLog(fmt.Sprintf("[INFO] Stopping adopted llama-server (pid %d)...", adopted))
	if err := killProcessByPid(adopted); err != nil {
		log.Printf("[WARN] failed to kill adopted llama-server pid %d: %v", adopted, err)
	}
	serverMu.Lock()
	serverRunning = false
	serverPort = 0
	directServingModel = ""
	serverStartTime = time.Time{}
	adoptedPid = 0
	tail := serverLogTail
	serverLogTail = nil
	serverMu.Unlock()
	if tail != nil {
		// The adopted child has no Wait handle; stopping the tailer here is
		// what ends log capture (it drains the file's remaining bytes first).
		tail.Stop()
		tail.WaitDone(2 * time.Second)
	}
	if err := removeHandover(); err != nil {
		log.Printf("[WARN] %v", err)
	}
	return true
}

func stopServerInternal() error {
	// Adopted server (handed over by the previous process): no child handle
	// exists, so stop by pid and remove the handover record — graceful stop
	// is unsupported on this path (see stopAdoptedServerIfAny).
	if stopAdoptedServerIfAny() {
		return nil
	}

	// Read running/cmd/done local copies inside serverMu, operate on the
	// copies outside (#3), to avoid concurrent access to serverCmd/Process
	// between stopServerInternal and start/goroutine. The grace wait below
	// must never happen while holding serverMu. Concurrent stop calls are
	// idempotent: a caller arriving after the child exited reads
	// running=false and returns; overlapping callers select on the same done.
	serverMu.Lock()
	running := serverRunning
	cmd := serverCmd
	done := serverDone
	serverMu.Unlock()
	if !running || cmd == nil {
		return nil
	}

	addServerLog("[INFO] Stopping llama-server...")

	if err := cmd.Process.Signal(osInterrupt); err != nil {
		// The interrupt could not even be delivered (e.g. the process already
		// finished): keep the historical behavior — escalate straight to Kill.
		cmd.Process.Kill()
		return nil
	}

	// Uniform graceful-stop sequence on every platform, no runtime.GOOS
	// branch: give the child a bounded grace period to exit on its own after
	// the interrupt, and only then escalate to Kill ("force never implicit").
	// On Windows osInterrupt IS os.Kill, so the child dies from the signal
	// itself and done always wins the select — the escalation branch stays
	// dormant there by construction, and the wait completes immediately after
	// the kill. done is closed only after the wait goroutine cleared the
	// lifecycle state under serverMu, so returning via done never races the
	// state cleanup. done == nil means no completion handle exists (a
	// startServerInternal child always registers one; only forged test state
	// omits it) — keep the historical fire-and-forget behavior then.
	if done == nil {
		return nil
	}
	select {
	case <-done:
		// The child exited within the grace period; nothing more to do.
	case <-time.After(stopGrace):
		log.Printf("[WARN] llama-server did not exit within %v, killed", stopGrace)
		if err := cmd.Process.Kill(); err != nil && !errors.Is(err, os.ErrProcessDone) {
			log.Printf("[WARN] failed to kill llama-server after grace period: %v", err)
		}
	}
	return nil
}

// ─── llama.cpp download trigger ──────────────────────────────────

func startLlamaCppDownload() {
	downloadMu.Lock()
	downloadState.Status = "fetching"
	downloadState.Paused = false
	downloadMu.Unlock()
	go downloadLlamaCpp()
}

// ─── HF Mirror download trigger ──────────────────────────────────

func startHFDownload(modelID string, files []string) error {
	// Validate the author segment of modelID (DestDir uses it in filepath.Join)
	// to prevent "../evil", ".", "..", or modelIDs containing path separators
	// from writing downloads outside LLM-Models (path traversal #1).
	parts := strings.SplitN(modelID, "/", 2)
	authorPart := parts[0]
	if authorPart == "" || authorPart == "." || authorPart == ".." ||
		strings.ContainsAny(authorPart, `\/`) {
		return fmt.Errorf("invalid modelID: %q", modelID)
	}

	// Validate each file name: the cleaned name must not be empty, start with
	// ".." (directory escape), or be an absolute path. Tasks uniformly use the
	// cleaned cleanName (#1).
	for _, fileName := range files {
		cleanName := filepath.Clean(fileName)
		if cleanName == "." || strings.HasPrefix(cleanName, "..") || filepath.IsAbs(cleanName) {
			return fmt.Errorf("invalid fileName: %q", fileName)
		}
	}

	// Validate the repo segment of modelID (DestDir uses it in filepath.Join),
	// same strategy as authorPart (#1 path traversal defense): missing repo
	// segment, empty repoPart, ".", "..", or containing \ / are all rejected.
	// Note that SplitN("a/b/c","/",2) yields repoPart "b/c", so "/" must be
	// rejected to avoid landing outside the target directory.
	if len(parts) < 2 {
		return fmt.Errorf("invalid modelID: %q", modelID)
	}
	repoPart := parts[1]
	if repoPart == "" || repoPart == "." || repoPart == ".." ||
		strings.ContainsAny(repoPart, `\/`) {
		return fmt.Errorf("invalid modelID: %q", modelID)
	}

	// The active download source determines URL construction: hf uses the
	// hf-mirror resolve endpoint, modelscope uses the legacy repo endpoint;
	// the task records Source so the queue can rebuild URLs on restore.
	source := activeDownloadSource()

	// Pre-validate source and pre-build one URL before enqueuing (#B2):
	// buildModelDownloadURL only returns error for unknown sources (defense in
	// depth; activeDownloadSource is already allowlisted). Probing once before
	// any task is enqueued means failure returns a single error, leaving no
	// half-enqueued state and no need to roll back previously enqueued tasks.
	// For valid sources, per-file URL construction never fails; error branches
	// inside the loop are defensive only.
	if _, err := buildModelDownloadURL(source, modelID, "probe.gguf"); err != nil {
		return err
	}

	dlTasksMu.Lock()
	for _, fileName := range files {
		cleanName := filepath.Clean(fileName)
		url, err := buildModelDownloadURL(source, modelID, cleanName)
		if err != nil {
			// Defensive branch: URL build failure is treated as a global error;
			// already-enqueued tasks remain as-is (#B2). Unlock before
			// returning (must not return while holding dlTasksMu; see #B1
			// explanation below).
			dlTasksMu.Unlock()
			return err
		}

		dlTaskCounter++
		id := fmt.Sprintf("dl-%d", dlTaskCounter)
		task := &DlTask{
			ID:       id,
			ModelID:  modelID,
			FileName: cleanName,
			DestDir:  filepath.Join(effectiveModelDownloadDir(), authorPart, repoPart),
			Source:   source,
			URL:      url,
			Status:   "queued",
			resumeCh: make(chan struct{}, 1),
		}
		task.ctx, task.cancel = context.WithCancel(context.Background())
		dlTasks = append(dlTasks, task)
		spawnDownloadTask(task)
	}
	// Unlock before persisting the queue (#B1): persistTasksNow → saveConfig
	// acquires dlTasksMu again at the end to take a snapshot. If dlTasksMu is
	// still held here (e.g. inside a defer Unlock scope), it deadlocks with
	// itself — this was the root cause of a previous go test 600s timeout
	// stuck on dlTasksMu.Lock().
	dlTasksMu.Unlock()
	persistTasksNow()
	return nil
}
