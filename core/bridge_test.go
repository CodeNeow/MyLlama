package core

import (
	"bytes"
	"log"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"os/signal"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"
)

// withModelScope404Server injects a local 404 server as modelscopeLegacyBase and
// switches the download source to modelscope, returning a restore function.
// Used when a test needs real queueing (startHFDownload spawns a download goroutine)
// but must avoid external network: the 404 response makes the goroutine fail fast
// without leaving open connections.
func withModelScope404Server(t *testing.T) func() {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusNotFound)
	}))
	t.Cleanup(srv.Close)
	origLegacy := modelscopeLegacyBase
	modelscopeLegacyBase = srv.URL
	downloadSourceMu.Lock()
	origSource := downloadSource
	downloadSource = sourceModelScope
	downloadSourceMu.Unlock()
	return func() {
		modelscopeLegacyBase = origLegacy
		downloadSourceMu.Lock()
		downloadSource = origSource
		downloadSourceMu.Unlock()
	}
}

// waitTasksTerminal polls until all tasks reach a terminal state (error/done/cancelled),
// with a timeout guard. Returns immediately when no tasks exist. Used by queue tests
// to avoid stray goroutines polluting subsequent test cases.
func waitTasksTerminal(t *testing.T, timeout time.Duration) {
	t.Helper()
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		dlTasksMu.Lock()
		allTerminal := true
		for _, tt := range dlTasks {
			switch tt.Status {
			case "error", "done", "cancelled":
			default:
				allTerminal = false
			}
		}
		dlTasksMu.Unlock()
		if allTerminal {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	dlTasksMu.Lock()
	statuses := make([]string, 0, len(dlTasks))
	for _, tt := range dlTasks {
		statuses = append(statuses, tt.ID+":"+tt.Status)
	}
	dlTasksMu.Unlock()
	t.Fatalf("tasks did not reach terminal state in %v: %v", timeout, statuses)
}

// waitTaskTerminal polls until the task with the given id reaches a terminal state
// (error/done/cancelled), with a timeout guard. Waits for a single task only;
// useful when the queue also contains tasks with no goroutine (e.g. resumed paused
// tasks) and we only need to wait for the newly enqueued active one.
func waitTaskTerminal(t *testing.T, id string, timeout time.Duration) {
	t.Helper()
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		dlTasksMu.Lock()
		var status string
		for _, tt := range dlTasks {
			if tt.ID == id {
				status = tt.Status
				break
			}
		}
		dlTasksMu.Unlock()
		switch status {
		case "error", "done", "cancelled":
			return
		case "":
			t.Fatalf("task %s not found", id)
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatalf("task %s did not reach terminal state in %v", id, timeout)
}

// cancelAllTasks cancels every non-terminal download task and waits for their
// goroutines to exit. Queue tests enqueue real download goroutines; without
// waiting, a goroutine can outlive its test and later call saveConfig (the
// configFile path is cwd-relative) from a subsequent test's temp cwd,
// truncating that test's config file and making its loadConfig read empty
// JSON. Must be called before the test's defer resets dlTasks.
func cancelAllTasks(t *testing.T) {
	t.Helper()
	dlTasksMu.Lock()
	for _, tt := range dlTasks {
		switch tt.Status {
		case "error", "done", "cancelled":
		default:
			if tt.cancel != nil {
				tt.cancel()
			}
		}
	}
	dlTasksMu.Unlock()
	waitTasksTerminal(t, 5*time.Second)
}

// waitDlGoroutinesForTestBounded waits (bounded) until every downloadTask
// goroutine spawned through spawnDownloadTask has exited. A downloadTask's
// terminal persistTasksNow write is its FINAL action, so goroutine exit — not
// terminal task status — is the happens-before edge that proves the trailing
// config write (temp file + fsync + rename inside atomicWriteFile) has
// completed. Without this drain, t.TempDir cleanup can RemoveAll the directory
// while a goroutine's trailing write is still in flight: the temp file then
// lands after the cleanup scan ("directory is not empty") or its rename
// targets the already-removed directory (observed as a flaky FAIL on Go 1.24+,
// where TempDir cleanup errors fail the test). Call only after
// waitTasksTerminal / cancelAllTasks: a paused (non-terminal) task's goroutine
// never exits.
//
// On timeout this reports via t.Errorf and returns instead of calling
// t.Fatal: t.Fatal runtime.Goexits out of the current goroutine, so inside a
// t.Cleanup closure it would skip the remaining cleanup body — including
// withTempCwd's subsequent path-var restore and os.Chdir back to the original
// directory — leaving the process cwd inside the already-removed temp dir and
// poisoning every later test in the package.
func waitDlGoroutinesForTestBounded(t *testing.T, timeout time.Duration) {
	t.Helper()
	done := make(chan struct{})
	go func() {
		dlTaskGoroutines.Wait()
		close(done)
	}()
	select {
	case <-done:
	case <-time.After(timeout):
		t.Errorf("download goroutines did not exit within %v", timeout)
	}
}

// waitDlGoroutinesForTest drains the download goroutines with the default 5s
// bound (thin wrapper over waitDlGoroutinesForTestBounded).
func waitDlGoroutinesForTest(t *testing.T) {
	t.Helper()
	waitDlGoroutinesForTestBounded(t, 5*time.Second)
}

// TestStartHFDownloadNoDeadlock is a #B1 deadlock regression test: startHFDownload
// calls persistTasksNow → saveConfig after enqueue, and saveConfig re-acquires dlTasksMu
// at its tail for snapshotting. If persistTasksNow is called while dlTasksMu is held
// (inside a defer Unlock scope), it deadlocks on itself — the old implementation hung
// here, causing go test 600s timeouts. The test asserts startHFDownload returns fast,
// then GetDownloadTasks snapshots without blocking, and waits for task terminal state
// to avoid stray goroutines.
func TestStartHFDownloadNoDeadlock(t *testing.T) {
	withTempCwd(t)
	saveConfigState(t)
	restoreSource := withModelScope404Server(t)
	defer restoreSource()
	dlTasksMu.Lock()
	dlTasks = nil
	dlTaskCounter = 0
	dlTasksMu.Unlock()
	defer func() {
		dlTasksMu.Lock()
		dlTasks = nil
		dlTaskCounter = 0
		dlTasksMu.Unlock()
	}()

	done := make(chan struct{})
	go func() {
		if err := startHFDownload("author/model", []string{"a.gguf", "b.gguf"}); err != nil {
			t.Errorf("startHFDownload returned error: %v", err)
		}
		close(done)
	}()
	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("startHFDownload deadlocked: did not return within 5s (persistTasksNow should not be called while holding dlTasksMu)")
	}

	// Snapshot immediately after enqueue: GetDownloadTasks acquires dlTasksMu internally;
	// a deadlocked implementation would also hang here.
	tasks := (&App{}).GetDownloadTasks()
	if len(tasks) != 2 {
		t.Fatalf("task count = %d, want 2", len(tasks))
	}
	// No status assertion here, deliberately: with a 404 source each download
	// goroutine reaches terminal "error" within microseconds of its own
	// persist, while this snapshot necessarily follows main's persist
	// (temp+fsync+rename ≈ milliseconds under atomicWriteFile) — whichever
	// write lands last would decide the assertion, a write-completion-order
	// lottery. The queued-state contract is covered deterministically by
	// TestStartHFDownloadQueue; this test's contract is exactly:
	// (1) startHFDownload returns within the 5s guard above, and
	// (2) GetDownloadTasks snapshots without blocking, with both tasks present.

	// Wait for goroutines to exit (404 fails fast to error state) to avoid leakage.
	waitTasksTerminal(t, 5*time.Second)
	// Drain the goroutines themselves: their terminal persistTasksNow is the
	// last action before exit, so waiting only on status can still race the
	// trailing config write into this test's temp cwd (the TempDir-cleanup
	// flake this drain fixes).
	waitDlGoroutinesForTest(t)
}

// TestStartHFDownloadQueue verifies batch file enqueue: IDs increment, URLs use
// HF Mirror domain, initial status is queued, and destination is effectiveModelDownloadDir/<author>/<repo>
// (three-level layout: author/model/file).
func TestStartHFDownloadQueue(t *testing.T) {
	saveConfigState(t)
	dlTasksMu.Lock()
	dlTasks = nil
	dlTaskCounter = 0
	dlTasksMu.Unlock()
	defer func() {
		dlTasksMu.Lock()
		dlTasks = nil
		dlTaskCounter = 0
		dlTasksMu.Unlock()
	}()

	if err := startHFDownload("author/model", []string{"a.gguf", "b.gguf"}); err != nil {
		t.Fatal(err)
	}

	dlTasksMu.Lock()
	if len(dlTasks) != 2 {
		t.Fatalf("task count = %d, want 2", len(dlTasks))
	}
	if dlTasks[0].ID != "dl-1" || dlTasks[1].ID != "dl-2" {
		t.Errorf("task IDs did not increment: %s, %s", dlTasks[0].ID, dlTasks[1].ID)
	}
	// Enqueue immediately spawns the download goroutine (go downloadTask), and the
	// downloadTask entry flips status to downloading. At read time the status may
	// still be queued (goroutine not yet scheduled) or already downloading (scheduled
	// and acquired the lock); both are valid initial states after enqueue. The #B1
	// deadlock regression also accepts "status becomes downloading first or flows through pause".
	if dlTasks[0].Status != "queued" && dlTasks[0].Status != "downloading" {
		t.Errorf("initial status = %q, want queued or downloading", dlTasks[0].Status)
	}
	if !strings.HasPrefix(dlTasks[0].URL, hfMirrorBase+"/author/model/resolve/main/") {
		t.Errorf("URL prefix wrong: %q", dlTasks[0].URL)
	}
	if !strings.HasSuffix(dlTasks[0].DestDir, filepath.Join(effectiveModelDownloadDir(), "author", "model")) {
		t.Errorf("DestDir = %q, want to end with %q", dlTasks[0].DestDir, filepath.Join(effectiveModelDownloadDir(), "author", "model"))
	}
	if dlTasks[0].cancel == nil {
		t.Error("task should hold cancel func (used by cancel / exit cleanup)")
	}
	dlTasksMu.Unlock()

	cancelAllTasks(t) // stop the real download goroutine before it outlives this test
}

// TestStopHFDownload verifies cancelling a single task: cancel triggers context
// cancellation and status becomes cancelled.
func TestStopHFDownload(t *testing.T) {
	saveConfigState(t)
	dlTasksMu.Lock()
	dlTasks = nil
	dlTaskCounter = 0
	dlTasksMu.Unlock()
	defer func() {
		dlTasksMu.Lock()
		dlTasks = nil
		dlTaskCounter = 0
		dlTasksMu.Unlock()
	}()

	if err := startHFDownload("author/model", []string{"a.gguf"}); err != nil {
		t.Fatal(err)
	}

	dlTasksMu.Lock()
	task := dlTasks[0]
	dlTasksMu.Unlock()

	task.cancel()
	if err := task.ctx.Err(); err == nil {
		t.Error("context should be cancelled after cancel()")
	}

	cancelAllTasks(t) // wait for the goroutine to actually exit
}

// TestCancelDownloadTaskUnknownID verifies cancelling a non-existent task returns nil (idempotent).
func TestCancelDownloadTaskUnknownID(t *testing.T) {
	saveConfigState(t)
	dlTasksMu.Lock()
	dlTasks = nil
	dlTaskCounter = 0
	dlTasksMu.Unlock()
	defer func() {
		dlTasksMu.Lock()
		dlTasks = nil
		dlTaskCounter = 0
		dlTasksMu.Unlock()
	}()

	if err := (&App{}).CancelDownloadTask("dl-999"); err != nil {
		t.Errorf("unknown task ID should return nil, got %v", err)
	}
}

// TestPauseResumeDownloadTask verifies pause/resume state machine: only downloading
// can be paused; only paused can be resumed.
func TestPauseResumeDownloadTask(t *testing.T) {
	saveConfigState(t)
	restoreSource := withModelScope404Server(t)
	defer restoreSource()
	dlTasksMu.Lock()
	dlTasks = nil
	dlTaskCounter = 0
	dlTasksMu.Unlock()
	defer func() {
		dlTasksMu.Lock()
		dlTasks = nil
		dlTaskCounter = 0
		dlTasksMu.Unlock()
	}()

	if err := startHFDownload("author/model", []string{"a.gguf"}); err != nil {
		t.Fatal(err)
	}
	dlTasksMu.Lock()
	id := dlTasks[0].ID
	dlTasksMu.Unlock()
	// Must wait for download goroutine to exit (local 404 fails fast to error state)
	// before manually mutating status: downloadTask unconditionally flips status to
	// downloading at entry (engine.go downloadTask prologue). While the goroutine is
	// alive, setting downloading → pause may be overwritten back to downloading by the
	// goroutine's entry write landing after Pause — CI full-load runners have hit this
	// intermittent failure. Production paths have no such interleaving (downloading is
	// only set by the goroutine itself; Pause on queued is a no-op); this is a race
	// introduced by test-forged state.
	waitTaskTerminal(t, id, 5*time.Second)

	dlTasksMu.Lock()
	task := dlTasks[0]
	task.Status = "downloading"
	dlTasksMu.Unlock()

	if err := (&App{}).PauseDownloadTask(task.ID); err != nil {
		t.Fatal(err)
	}
	dlTasksMu.Lock()
	if task.Status != "paused" {
		t.Errorf("status after pause = %q, want paused", task.Status)
	}
	dlTasksMu.Unlock()

	if err := (&App{}).ResumeDownloadTask(task.ID); err != nil {
		t.Fatal(err)
	}
	dlTasksMu.Lock()
	if task.Status != "downloading" {
		t.Errorf("status after resume = %q, want downloading", task.Status)
	}
	dlTasksMu.Unlock()
}

// TestGetDownloadTasksSnapshot verifies GetDownloadTasks returns a deep copy:
// mutating the returned slice must not affect internal task state.
func TestGetDownloadTasksSnapshot(t *testing.T) {
	saveConfigState(t)
	dlTasksMu.Lock()
	dlTasks = nil
	dlTaskCounter = 0
	dlTasksMu.Unlock()
	defer func() {
		dlTasksMu.Lock()
		dlTasks = nil
		dlTaskCounter = 0
		dlTasksMu.Unlock()
	}()

	if err := startHFDownload("author/model", []string{"a.gguf"}); err != nil {
		t.Fatal(err)
	}

	tasks := (&App{}).GetDownloadTasks()
	if len(tasks) != 1 {
		t.Fatalf("task count = %d, want 1", len(tasks))
	}
	tasks[0].Status = "hacked"
	dlTasksMu.Lock()
	realStatus := dlTasks[0].Status
	dlTasksMu.Unlock()
	if realStatus == "hacked" {
		t.Error("mutating snapshot must not affect internal task state")
	}

	cancelAllTasks(t) // stop the real download goroutine before it outlives this test
}

// TestStartHFDownloadRejectsPathTraversal verifies startHFDownload returns an error
// for malicious modelID / fileName and creates no tasks (#1). When the author segment
// contains a path separator or "../", DestDir escapes LLM-Models via filepath.Join,
// so rejection must happen before enqueue.
func TestStartHFDownloadRejectsPathTraversal(t *testing.T) {
	saveConfigState(t)
	dlTasksMu.Lock()
	dlTasks = nil
	dlTaskCounter = 0
	dlTasksMu.Unlock()
	defer func() {
		dlTasksMu.Lock()
		dlTasks = nil
		dlTaskCounter = 0
		dlTasksMu.Unlock()
	}()

	// author segment is "../evil": DestDir joins outside LLM-Models
	if err := startHFDownload("../evil", []string{"evil.gguf"}); err == nil {
		t.Error("../evil modelID should return error")
	}
	// modelID containing path separator ("a/b" or "a\\b") is also rejected
	if err := startHFDownload("a\\b/model", []string{"x.gguf"}); err == nil {
		t.Error("modelID with \\ in author should return error")
	}
	// filename can escape to parent directory
	if err := startHFDownload("author/model", []string{"../../etc/x.gguf"}); err == nil {
		t.Error("../../etc/x.gguf filename should return error")
	}
	// absolute-path filenames are rejected (platform-dependent: Windows needs C:\ prefix,
	// Unix needs / prefix for filepath.IsAbs; no single string works cross-platform)
	absName := "/etc/x.gguf"
	if runtime.GOOS == "windows" {
		absName = `C:\Windows\system.ini`
	}
	if err := startHFDownload("author/model", []string{absName}); err == nil {
		t.Error("absolute-path filename should return error")
	}

	// rejection branch must create zero tasks
	dlTasksMu.Lock()
	defer dlTasksMu.Unlock()
	if len(dlTasks) != 0 || dlTaskCounter != 0 {
		t.Errorf("invalid input must not create tasks: len=%d counter=%d", len(dlTasks), dlTaskCounter)
	}
}

// TestStartHFDownloadRejectsInvalidModelID verifies empty / "." / ".." author parts
// return an error (#1).
func TestStartHFDownloadRejectsInvalidModelID(t *testing.T) {
	saveConfigState(t)
	dlTasksMu.Lock()
	dlTasks = nil
	dlTaskCounter = 0
	dlTasksMu.Unlock()
	defer func() {
		dlTasksMu.Lock()
		dlTasks = nil
		dlTaskCounter = 0
		dlTasksMu.Unlock()
	}()

	if err := startHFDownload("/model", []string{"x.gguf"}); err == nil {
		t.Error("/model with empty author should return error")
	}
	if err := startHFDownload("./model", []string{"x.gguf"}); err == nil {
		t.Error("./model with . author should return error")
	}
	if err := startHFDownload("../model", []string{"x.gguf"}); err == nil {
		t.Error("../model with .. author should return error")
	}
}

// TestStartHFDownloadRejectsInvalidRepoPart verifies empty or path-separator-containing
// repo parts return an error without creating tasks (#1). DestDir uses repoPart in
// filepath.Join: repoPart containing "/" (e.g. SplitN("author/model/extra","/",2) → "model/extra")
// or empty (e.g. "author/") both write the download target to a wrong directory level.
func TestStartHFDownloadRejectsInvalidRepoPart(t *testing.T) {
	saveConfigState(t)
	dlTasksMu.Lock()
	dlTasks = nil
	dlTaskCounter = 0
	dlTasksMu.Unlock()
	defer func() {
		dlTasksMu.Lock()
		dlTasks = nil
		dlTaskCounter = 0
		dlTasksMu.Unlock()
	}()

	// repoPart contains "/": SplitN("a/b/c","/",2) → repoPart = "b/c"
	if err := startHFDownload("author/model/extra", []string{"x.gguf"}); err == nil {
		t.Error("modelID with / in repoPart should return error")
	}
	// repoPart is empty: SplitN("author/","/",2) → repoPart = ""
	if err := startHFDownload("author/", []string{"x.gguf"}); err == nil {
		t.Error("modelID with empty repoPart should return error")
	}
	// repoPart is "." / "..": equivalent to parent directory after join
	if err := startHFDownload("author/.", []string{"x.gguf"}); err == nil {
		t.Error("modelID with . repoPart should return error")
	}
	if err := startHFDownload("author/..", []string{"x.gguf"}); err == nil {
		t.Error("modelID with .. repoPart should return error")
	}
	// modelID with no "/" has no repo part; also rejected
	if err := startHFDownload("author", []string{"x.gguf"}); err == nil {
		t.Error("modelID without repoPart should return error")
	}

	// rejection branch must create zero tasks
	dlTasksMu.Lock()
	defer dlTasksMu.Unlock()
	if len(dlTasks) != 0 || dlTaskCounter != 0 {
		t.Errorf("invalid input must not create tasks: len=%d counter=%d", len(dlTasks), dlTaskCounter)
	}
}

// TestStartHFDownloadUsesDownloadDirOverride verifies new model downloads land
// under the configured model download path (not the imported model directory).
func TestStartHFDownloadUsesDownloadDirOverride(t *testing.T) {
	saveConfigState(t)
	restoreSource := withModelScope404Server(t)
	defer restoreSource()

	dlDir := t.TempDir()
	modelDownloadDirMu.Lock()
	modelDownloadDirOverride = dlDir
	modelDownloadDirMu.Unlock()
	dlTasksMu.Lock()
	dlTasks = nil
	dlTaskCounter = 0
	dlTasksMu.Unlock()
	defer func() {
		dlTasksMu.Lock()
		dlTasks = nil
		dlTaskCounter = 0
		dlTasksMu.Unlock()
	}()

	if err := startHFDownload("author/model", []string{"a.gguf"}); err != nil {
		t.Fatal(err)
	}
	dlTasksMu.Lock()
	destDir := dlTasks[0].DestDir
	dlTasksMu.Unlock()
	want := filepath.Join(dlDir, "author", "model")
	if destDir != want {
		t.Errorf("DestDir = %q, want %q (model download path must be used)", destDir, want)
	}
	cancelAllTasks(t)
}

// withPlatformGOOS pins the platformGOOS seam (runtime.GOOS in production) for
// the duration of the test, restoring the previous value afterwards; used to
// drive the per-OS gates from a single test binary (same style as the
// updateExePath / renameFile injection points).
func withPlatformGOOS(t *testing.T, goos string) {
	t.Helper()
	orig := platformGOOS
	platformGOOS = goos
	t.Cleanup(func() { platformGOOS = orig })
}

// TestCudaDeviceEnvEmpty verifies an empty serving-GPU selection (auto) yields
// nil so exec.Command inherits the parent environment unchanged (historical
// behavior). Runs on the Windows branch: CUDA device pinning is Windows-only.
func TestCudaDeviceEnvEmpty(t *testing.T) {
	withPlatformGOOS(t, "windows")
	if got := cudaDeviceEnv(""); got != nil {
		t.Errorf("cudaDeviceEnv(\"\") = %v, want nil", got)
	}
}

// TestCudaDeviceEnvOverridesInherited verifies a non-empty device UUID pins the
// child environment: the inherited variables are kept and CUDA_VISIBLE_DEVICES
// is appended LAST so it overrides any inherited value (last-wins semantics).
// Runs on the Windows branch: CUDA device pinning is Windows-only.
func TestCudaDeviceEnvOverridesInherited(t *testing.T) {
	withPlatformGOOS(t, "windows")
	env := cudaDeviceEnv("GPU-12345678-9abc-def0-1234-56789abcdef0")
	if len(env) < len(os.Environ()) {
		t.Fatalf("cudaDeviceEnv kept %d entries, want >= %d (inherited environment)", len(env), len(os.Environ()))
	}
	want := "CUDA_VISIBLE_DEVICES=GPU-12345678-9abc-def0-1234-56789abcdef0"
	if env[len(env)-1] != want {
		t.Errorf("last env entry = %q, want %q (override must be appended last)", env[len(env)-1], want)
	}
	inherited := false
	for _, kv := range env[:len(env)-1] {
		if strings.HasPrefix(kv, "PATH=") {
			inherited = true
			break
		}
	}
	if !inherited {
		t.Error("cudaDeviceEnv should keep the inherited environment entries")
	}
}

// TestCudaDeviceEnvNonWindowsGated verifies the CUDA device pin never reaches
// the child environment off-Windows: the shipped llama.cpp builds there are
// Vulkan / Metal / CPU, where CUDA_VISIBLE_DEVICES is meaningless — even an
// explicit device UUID must yield nil.
func TestCudaDeviceEnvNonWindowsGated(t *testing.T) {
	for _, goos := range []string{"linux", "darwin"} {
		t.Run(goos, func(t *testing.T) {
			withPlatformGOOS(t, goos)
			if got := cudaDeviceEnv("GPU-12345678-9abc-def0-1234-56789abcdef0"); got != nil {
				t.Errorf("cudaDeviceEnv(deviceID) on %s = %v, want nil (CUDA pinning is Windows-only)", goos, got)
			}
			if got := cudaDeviceEnv(""); got != nil {
				t.Errorf("cudaDeviceEnv(\"\") on %s = %v, want nil", goos, got)
			}
		})
	}
}

// ─── Graceful stop: grace period and kill escalation ─────────────

// TestHelperProcessIgnoreInterrupt serves as an interrupt-immune llama-server
// surrogate child: it ignores SIGINT, announces readiness by writing the
// GO_HELPER_READY_FILE marker (so the parent never sends its interrupt before
// the ignore disposition is installed — no startup race) and then sleeps, so
// only the kill escalation can end it. Used by
// TestStopServerInternalEscalatesToKillAfterGrace; the env guard keeps it
// inert during the suite's own run.
func TestHelperProcessIgnoreInterrupt(t *testing.T) {
	if os.Getenv("GO_WANT_HELPER_PROCESS_IGNORE_INT") != "1" {
		return
	}
	signal.Ignore(os.Interrupt)
	if path := os.Getenv("GO_HELPER_READY_FILE"); path != "" {
		if err := os.WriteFile(path, []byte("ready"), 0644); err != nil {
			return // the parent's bounded readiness wait fails the test instead
		}
	}
	time.Sleep(30 * time.Second)
}

// startSurrogateChild spawns a real surrogate llama-server child process (the
// test binary re-invoking the helper selected by testRun with extraEnv set) —
// the same pattern as TestStopServerInternalKillsRunningProcess. The cleanup
// Kill is the stray-process guarantee: a child that survives the behavior
// under test (failure path) is still terminated before the suite moves on.
func startSurrogateChild(t *testing.T, testRun string, extraEnv ...string) *exec.Cmd {
	t.Helper()
	cmd := exec.Command(os.Args[0], "-test.run="+testRun)
	cmd.Env = append(os.Environ(), extraEnv...)
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		cmd.Process.Kill() // error (already exited) deliberately ignored
	})
	return cmd
}

// waitForReadyFile polls until the surrogate child's readiness marker file
// exists (bounded), synchronizing the parent's signal with the child's
// installed signal disposition. Bounded poll, same style as waitTasksTerminal.
func waitForReadyFile(t *testing.T, path string) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for {
		if _, err := os.Stat(path); err == nil {
			return
		}
		if time.Now().After(deadline) {
			t.Fatalf("surrogate child readiness marker %s never appeared", path)
		}
		time.Sleep(5 * time.Millisecond)
	}
}

// forgeRunningServer registers cmd as the running llama-server
// (serverRunning / serverCmd / serverDone under serverMu) and replays
// startServerInternal's wait goroutine with the same ordering: cmd.Wait →
// clear lifecycle state under serverMu → close(done) strictly after the
// critical section, so a graceful-stop waiter resumes only once the state is
// fully cleared.
func forgeRunningServer(t *testing.T, cmd *exec.Cmd) (done chan struct{}) {
	t.Helper()
	done = make(chan struct{})
	serverMu.Lock()
	origDone := serverDone
	serverRunning = true
	serverCmd = cmd
	serverDone = done
	serverMu.Unlock()
	t.Cleanup(func() {
		serverMu.Lock()
		serverRunning = false
		serverCmd = nil
		serverDone = origDone
		serverMu.Unlock()
	})
	go func() {
		cmd.Wait()
		serverMu.Lock()
		serverRunning = false
		if serverCmd == cmd {
			serverCmd = nil
		}
		if serverDone == done {
			serverDone = nil
		}
		serverMu.Unlock()
		close(done)
	}()
	return done
}

// captureLogOutput redirects the standard logger into a buffer for the
// duration of the test and returns it, restoring the previous writer on
// cleanup. Used to assert the escalation WARN emitted by stopServerInternal.
func captureLogOutput(t *testing.T) *bytes.Buffer {
	t.Helper()
	var buf bytes.Buffer
	orig := log.Writer()
	log.SetOutput(&buf)
	t.Cleanup(func() { log.SetOutput(orig) })
	return &buf
}

// TestStopServerInternalGracefulExitNoEscalation verifies the graceful branch
// of stopServerInternal: a child that dies on the interrupt makes the stop
// return via the done channel (child reaped, state cleared) WITHOUT the kill
// escalation. On Windows osInterrupt is os.Kill, so the child dies from the
// signal itself and escalation is vacuous — the no-escalation log assertion
// is therefore guarded to platforms where osInterrupt is os.Interrupt.
func TestStopServerInternalGracefulExitNoEscalation(t *testing.T) {
	saveServerState(t)
	serverMu.Lock()
	serverRunning = false
	serverCmd = nil
	serverDone = nil
	serverMu.Unlock()

	// Long grace: the child exits in milliseconds, so reaching the escalation
	// branch can only mean the graceful path is broken (caught by the log
	// assertion below); the fast path itself stays millisecond-fast.
	origGrace := stopGrace
	stopGrace = 10 * time.Second
	defer func() { stopGrace = origGrace }()

	logs := captureLogOutput(t)

	cmd := startSurrogateChild(t, "^TestHelperProcess$", "GO_WANT_HELPER_PROCESS=1")
	done := forgeRunningServer(t, cmd)

	if err := stopServerInternal(); err != nil {
		t.Fatalf("stopServerInternal returned error: %v", err)
	}

	// The wait goroutine reaped the child and closed done: the process is
	// gone without any escalation Kill.
	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("child was not reaped after a graceful stopServerInternal")
	}
	serverMu.Lock()
	running := serverRunning
	serverMu.Unlock()
	if running {
		t.Error("serverRunning should be false after the child exited")
	}
	if runtime.GOOS != "windows" {
		if s := logs.String(); strings.Contains(s, "did not exit within") {
			t.Errorf("graceful exit must not escalate to Kill; log output: %s", s)
		}
	}
}

// TestStopServerInternalEscalatesToKillAfterGrace verifies the escalation
// branch ("force never implicit"): a child that ignores the interrupt
// survives the injected grace period, so stopServerInternal waits at least
// the full grace, logs the "[WARN] llama-server did not exit within" line and
// then Kills the child. Unix-only: on Windows osInterrupt is os.Kill, which a
// child cannot ignore, so the ignoring-child scenario cannot exist there.
func TestStopServerInternalEscalatesToKillAfterGrace(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("osInterrupt is os.Kill on Windows; no child can ignore it")
	}
	saveServerState(t)
	serverMu.Lock()
	serverRunning = false
	serverCmd = nil
	serverDone = nil
	serverMu.Unlock()

	// Shortened injected grace keeps the new-test pair well under 2s total.
	const grace = 300 * time.Millisecond
	origGrace := stopGrace
	stopGrace = grace
	defer func() { stopGrace = origGrace }()

	logs := captureLogOutput(t)

	ready := filepath.Join(t.TempDir(), "int-ignore-ready")
	cmd := startSurrogateChild(t, "^TestHelperProcessIgnoreInterrupt$",
		"GO_WANT_HELPER_PROCESS_IGNORE_INT=1", "GO_HELPER_READY_FILE="+ready)
	waitForReadyFile(t, ready) // SIGINT must not land before the ignore is installed
	done := forgeRunningServer(t, cmd)

	start := time.Now()
	if err := stopServerInternal(); err != nil {
		t.Fatalf("stopServerInternal returned error: %v", err)
	}
	elapsed := time.Since(start)

	// The grace was honored: time.After never fires early, so the stop can
	// only return via the escalation branch after the full grace elapsed.
	if elapsed < grace {
		t.Errorf("stopServerInternal returned after %v, want >= %v (grace must elapse before Kill)", elapsed, grace)
	}
	if s := logs.String(); !strings.Contains(s, "[WARN] llama-server did not exit within") {
		t.Errorf("escalation should log the grace-expiry WARN; log output: %s", s)
	}

	// The escalation Kill took effect: the wait goroutine reaped the child.
	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("interrupt-ignoring child was not killed after the grace expired")
	}
}

// TestAndroidLdEnv verifies the Android LD_LIBRARY_PATH anchor: the binary's
// directory only on android, nil elsewhere.
func TestAndroidLdEnv(t *testing.T) {
	old := pathsGOOS
	t.Cleanup(func() { pathsGOOS = old })

	pathsGOOS = "windows"
	if got := androidLdEnv("/data/x/llama-server"); got != nil {
		t.Errorf("androidLdEnv on windows = %v, want nil", got)
	}
	pathsGOOS = "android"
	want := "LD_LIBRARY_PATH=" + filepath.Join("/data", "x")
	if got := androidLdEnv("/data/x/llama-server"); len(got) != 1 || got[0] != want {
		t.Errorf("androidLdEnv on android = %v, want [%q]", got, want)
	}
}

// TestServerChildEnv verifies the llama-server child-environment assembly:
// nil extra keeps exec's inherit semantics (nil), the CUDA pin passes
// through on windows, and android gets the LD_LIBRARY_PATH entry appended
// on top of the full parent environment (setting cmd.Env replaces
// inheritance wholesale).
func TestServerChildEnv(t *testing.T) {
	old := pathsGOOS
	t.Cleanup(func() { pathsGOOS = old })
	pathsGOOS = "windows"

	if got := serverChildEnv("/x/llama-server", nil); got != nil {
		t.Errorf("serverChildEnv(nil extra) on windows = %v, want nil (inherit)", got)
	}
	cuda := []string{"CUDA_VISIBLE_DEVICES=GPU-abc"}
	if got := serverChildEnv("/x/llama-server", cuda); len(got) != 1 || got[0] != cuda[0] {
		t.Errorf("serverChildEnv with cuda pin = %v, want %v", got, cuda)
	}

	pathsGOOS = "android"
	got := serverChildEnv("/data/x/llama-server", nil)
	if len(got) < 2 {
		t.Fatalf("serverChildEnv on android = %d entries, want parent env + LD_LIBRARY_PATH", len(got))
	}
	if got[len(got)-1] != "LD_LIBRARY_PATH="+filepath.Join("/data", "x") {
		t.Errorf("last env entry = %q, want the LD_LIBRARY_PATH anchor", got[len(got)-1])
	}
	base := false
	for _, e := range got[:len(got)-1] {
		if strings.HasPrefix(e, "PATH=") || strings.HasPrefix(e, "HOME=") {
			base = true
		}
	}
	if !base {
		t.Errorf("android env must be based on the parent environment, got %d entries with neither PATH nor HOME", len(got))
	}
}

// ─── Android direct mode ─────────────────────────────────────────

// withLanguage pins the UI language used by tr() for the duration of the test
// (default "auto" follows the machine locale, so message-text assertions must
// pin it), restoring the previous value afterwards.
func withLanguage(t *testing.T, lang string) {
	t.Helper()
	languageMu.Lock()
	orig := currentLanguage
	currentLanguage = lang
	languageMu.Unlock()
	t.Cleanup(func() {
		languageMu.Lock()
		currentLanguage = orig
		languageMu.Unlock()
	})
}

// argValue returns the value following flag in args (ok=false when the flag
// is absent or valueless) — a small helper for the direct-args assertions.
func argValue(args []string, flag string) (string, bool) {
	for i, a := range args {
		if a == flag && i+1 < len(args) {
			return args[i+1], true
		}
	}
	return "", false
}

// TestBuildServerCommandAndroidDirect verifies the android branch of
// buildServerCommand: single-model direct args driven by modelDirectArgs
// (-m / --alias present, per-model options included) with NO router flags
// (--models-preset / --models-max must be absent), while the shared service
// flags (host/port/cont-batching/no-webui/api-key/cache-ram) stay in place.
func TestBuildServerCommandAndroidDirect(t *testing.T) {
	withPlatformGOOS(t, "android")
	cfg := ServerConfig{AccessMode: accessLocal, Host: "127.0.0.1", Port: 8080, MaxModels: 1, CacheRAM: 512, APIKey: "sk-secret"}
	d := &directModel{
		info: ModelInfo{Name: "Qwen2.5 7B", Path: "/models/q.gguf"},
		cfg:  ModelConfig{CtxSize: 4096, GPULayers: "99"},
	}
	bin, args, err := buildServerCommand(cfg, "", d)
	if err != nil {
		t.Fatal(err)
	}
	if bin != "llama-server" {
		t.Errorf("bin = %q, want llama-server", bin)
	}

	if v, ok := argValue(args, "-m"); !ok || v != "/models/q.gguf" {
		t.Errorf("args missing -m /models/q.gguf: %v", args)
	}
	if v, ok := argValue(args, "--alias"); !ok || v != "Qwen2.5-7B" {
		t.Errorf("args missing --alias Qwen2.5-7B: %v", args)
	}
	if v, ok := argValue(args, "--ctx-size"); !ok || v != "4096" {
		t.Errorf("args missing --ctx-size 4096: %v", args)
	}
	if v, ok := argValue(args, "--gpu-layers"); !ok || v != "99" {
		t.Errorf("args missing --gpu-layers 99: %v", args)
	}
	for _, banned := range []string{"--models-preset", "--models-max", "--models-dir"} {
		for _, a := range args {
			if a == banned {
				t.Errorf("direct mode must not pass %s: %v", banned, args)
			}
		}
	}
	for _, want := range []string{"--cont-batching", "--no-webui", "--api-key", "sk-secret", "--cache-ram", "512"} {
		found := false
		for _, a := range args {
			if a == want {
				found = true
			}
		}
		if !found {
			t.Errorf("direct args missing %q: %v", want, args)
		}
	}
}

// TestDirectStartDecision verifies the pure android direct-mode start
// decision: spawn when nothing runs, no-op when the requested model already
// is the resident, restart when a different model is resident.
func TestDirectStartDecision(t *testing.T) {
	cases := []struct {
		running   bool
		resident  string
		requested string
		want      directStartAction
	}{
		{false, "", "", directStartSpawn},
		{false, "", "model-a", directStartSpawn},
		{true, "model-a", "model-a", directStartNoop},
		{true, "model-a", "model-b", directStartRestart},
		{true, "model-a", "", directStartRestart},
		{true, "", "model-a", directStartRestart},
	}
	for i, c := range cases {
		if got := directStartDecision(c.running, c.resident, c.requested); got != c.want {
			t.Errorf("case %d: directStartDecision(%v, %q, %q) = %v, want %v", i, c.running, c.resident, c.requested, got, c.want)
		}
	}
}

// TestResolveDirectModel verifies the direct-mode model resolution: exact
// Name match wins, empty requested falls back to the first scanned model in
// deterministic scan order, an unknown name and an empty scan return errors
// (the same "no models" message the preset path uses).
func TestResolveDirectModel(t *testing.T) {
	withLanguage(t, "en")
	models := []ModelInfo{
		{Name: "model-a", Path: "/a.gguf"},
		{Name: "model-b", Path: "/b.gguf"},
	}

	if m, err := resolveDirectModel(models, "model-b"); err != nil || m.Name != "model-b" {
		t.Errorf("exact match = %+v, %v; want model-b, nil", m, err)
	}
	// The scanned alias is the id the UI actually sends (sanitized display
	// name): it must resolve to the same model.
	aliased := []ModelInfo{{Name: "Model A", Alias: "Model-A", Path: "/a.gguf"}}
	if m, err := resolveDirectModel(aliased, "Model-A"); err != nil || m.Name != "Model A" {
		t.Errorf("alias match = %+v, %v; want Model A, nil", m, err)
	}
	if m, err := resolveDirectModel(models, ""); err != nil || m.Name != "model-a" {
		t.Errorf("default pick = %+v, %v; want first model model-a, nil", m, err)
	}
	if _, err := resolveDirectModel(models, "model-z"); err == nil || !strings.Contains(err.Error(), "model not found: model-z") {
		t.Errorf("unknown name error = %v, want 'model not found: model-z'", err)
	}
	if _, err := resolveDirectModel(nil, ""); err == nil || !strings.Contains(err.Error(), "no models found") {
		t.Errorf("empty scan error = %v, want the no-models message", err)
	}
}
