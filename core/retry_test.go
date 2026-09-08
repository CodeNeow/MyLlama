package core

import (
	"context"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strconv"
	"sync/atomic"
	"testing"
	"time"
)

// TestCancelDownloadTaskMarksErrorTaskCancelled verifies that calling CancelDownloadTask on an error-terminal task
// immediately sets Status to "cancelled" (bug root cause: error-task goroutine has already exited; the old implementation
// only called cancel() with no visible effect, so the UI stayed on "download failed" and cancel was unresponsive).
// After the fix, the lock sets Status="cancelled" before cancel(), so the frontend polling becomes visible immediately.
// The task is directly constructed as an error-terminal task simulating a dead goroutine; the downloadTask goroutine is not started.
func TestCancelDownloadTaskMarksErrorTaskCancelled(t *testing.T) {
	// CancelDownloadTask persists the queue when the task is found; without a
	// temp cwd that persist would write the cwd-relative config file into the
	// package directory.
	withTaskPersistNoop(t)
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

	task := &DlTask{
		ID:       "dl-1",
		ModelID:  "author/model",
		FileName: "model.gguf",
		Status:   "error",
		Error:    "stream error: stream ID 1; CANCEL; received from peer",
	}
	task.ctx, task.cancel = context.WithCancel(context.Background())
	dlTasks = append(dlTasks, task)

	app := &App{}
	if err := app.CancelDownloadTask("dl-1"); err != nil {
		t.Fatalf("CancelDownloadTask 返回错误: %v", err)
	}

	dlTasksMu.Lock()
	status := task.Status
	dlTasksMu.Unlock()
	if status != "cancelled" {
		t.Errorf("error 任务取消后状态 = %q, want cancelled（锁内置状态应立即生效）", status)
	}
	if task.ctx.Err() == nil {
		t.Error("取消后 ctx 应已结束")
	}

	// unknown id returns nil silently, consistent with existing semantics
	if err := app.CancelDownloadTask("dl-999"); err != nil {
		t.Errorf("未知 id 取消应返回 nil, got %v", err)
	}
}

// TestRetryDownloadTaskCompletesAfterError verifies that calling RetryDownloadTask on an error-terminal task
// reconstructs ctx, clears the error, restores Status to queued, and restarts the downloadTask goroutine;
// the .part resume transfer is automatically effective; httptest returns fixed content, the task eventually reaches
// done and the file is written to disk. It also verifies that a task in downloading state cannot be retried
// (an active goroutine exists, preventing concurrent writes to the same .part file) and that unknown ids return nil silently.
func TestRetryDownloadTaskCompletesAfterError(t *testing.T) {
	withTempCwd(t)
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

	payload := []byte("fake model bytes for retry")
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Length", strconv.Itoa(len(payload)))
		w.Write(payload)
	}))
	defer srv.Close()

	// construct error-terminal task (simulating download failure with goroutine exited)
	task := &DlTask{
		ID:       "dl-1",
		ModelID:  "author/model",
		FileName: "model.gguf",
		DestDir:  filepath.Join(effectiveModelDownloadDir(), "author"),
		URL:      srv.URL,
		Status:   "error",
		Error:    "stream error: stream ID 1; CANCEL; received from peer",
	}
	task.ctx, task.cancel = context.WithCancel(context.Background())
	dlTasks = append(dlTasks, task)

	app := &App{}
	if err := app.RetryDownloadTask("dl-1"); err != nil {
		t.Fatalf("RetryDownloadTask 返回错误: %v", err)
	}

	// immediate check: task should leave error-terminal state (queued if goroutine hasn't started yet,
	// downloading if it has)
	dlTasksMu.Lock()
	status := task.Status
	errMsg := task.Error
	dlTasksMu.Unlock()
	if status != "queued" && status != "downloading" {
		t.Fatalf("重试后状态 = %q, want queued 或 downloading", status)
	}
	if errMsg != "" {
		t.Errorf("重试后应清空旧错误信息: %q", errMsg)
	}

	// poll until done
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		dlTasksMu.Lock()
		status = task.Status
		dlTasksMu.Unlock()
		if status == "done" {
			break
		}
		if status == "error" {
			dlTasksMu.Lock()
			e := task.Error
			dlTasksMu.Unlock()
			t.Fatalf("重试下载失败: %q", e)
		}
		time.Sleep(10 * time.Millisecond)
	}
	dlTasksMu.Lock()
	status = task.Status
	dlTasksMu.Unlock()
	if status != "done" {
		t.Fatalf("重试后任务未完成, 状态 = %q", status)
	}

	got, err := os.ReadFile(filepath.Join(effectiveModelDownloadDir(), "author", "model.gguf"))
	if err != nil {
		t.Fatalf("下载文件未落盘: %v", err)
	}
	if string(got) != string(payload) {
		t.Errorf("文件内容 = %q, want %q", got, payload)
	}

	// tasks in downloading state are not allowed to retry (active goroutine exists)
	active := &DlTask{
		ID:       "dl-2",
		ModelID:  "author/model",
		FileName: "active.gguf",
		DestDir:  filepath.Join(effectiveModelDownloadDir(), "author"),
		URL:      srv.URL,
		Status:   "downloading",
	}
	active.ctx, active.cancel = context.WithCancel(context.Background())
	dlTasks = append(dlTasks, active)
	if err := app.RetryDownloadTask("dl-2"); err != nil {
		t.Fatalf("downloading 任务重试应静默返回 nil, got %v", err)
	}
	dlTasksMu.Lock()
	status = active.Status
	dlTasksMu.Unlock()
	if status != "downloading" {
		t.Errorf("downloading 任务重试后状态 = %q, want 保持 downloading", status)
	}

	// unknown id silently returns nil, consistent with CancelDownloadTask semantics
	if err := app.RetryDownloadTask("dl-999"); err != nil {
		t.Errorf("未知 id 重试应返回 nil, got %v", err)
	}
}

// ─── Automatic internal download retries (shared policy) ────────────────────

// shortRetryDelay speeds retry tests up: the production 3s backoff would make
// each multi-retry case sleep for seconds.
func shortRetryDelay(t *testing.T) {
	t.Helper()
	old := downloadRetryDelay
	downloadRetryDelay = 5 * time.Millisecond
	t.Cleanup(func() { downloadRetryDelay = old })
}

// pollTask waits until the task reaches a terminal-ish status (stop) or the
// deadline passes; returns the last observed status.
func pollTask(t *testing.T, task *DlTask, stop string) string {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	status := task.Status
	for time.Now().Before(deadline) {
		dlTasksMu.Lock()
		status = task.Status
		dlTasksMu.Unlock()
		if status == stop {
			break
		}
		time.Sleep(10 * time.Millisecond)
	}
	return status
}

// TestDownloadTaskRetriesTransientThenSucceeds verifies model-download tasks
// automatically retry transient HTTP failures (503) up to downloadRetryCount
// times while staying in downloading state, then complete once the server
// recovers — the user never sees an error for a transient blip.
func TestDownloadTaskRetriesTransientThenSucceeds(t *testing.T) {
	shortRetryDelay(t)
	withTempCwd(t)
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

	payload := []byte("transient retry payload")
	var requests int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		n := atomic.AddInt32(&requests, 1)
		if n <= 2 {
			w.WriteHeader(http.StatusServiceUnavailable)
			return
		}
		w.Header().Set("Content-Length", strconv.Itoa(len(payload)))
		w.Write(payload)
	}))
	defer srv.Close()

	task := &DlTask{
		ID: "dl-retry", ModelID: "author/model", FileName: "retry.gguf",
		DestDir: filepath.Join(effectiveModelDownloadDir(), "author"),
		URL:     srv.URL, Status: "downloading",
	}
	task.ctx, task.cancel = context.WithCancel(context.Background())
	dlTasksMu.Lock()
	dlTasks = append(dlTasks, task)
	dlTasksMu.Unlock()
	// spawnDownloadTask registers the goroutine in dlTaskGoroutines, making it
	// visible to withTempCwd's cleanup drain.
	spawnDownloadTask(task)

	if status := pollTask(t, task, "done"); status != "done" {
		t.Fatalf("task status = %q, want done after transient retries", status)
	}
	if got := atomic.LoadInt32(&requests); got != 3 {
		t.Errorf("server requests = %d, want 3 (two 503 + one success)", got)
	}
	got, err := os.ReadFile(filepath.Join(effectiveModelDownloadDir(), "author", "retry.gguf"))
	if err != nil || string(got) != string(payload) {
		t.Errorf("downloaded file mismatch: %q (%v)", got, err)
	}
}

// TestDownloadTaskFailsAfterRetriesExhausted verifies the task surfaces the
// error for a manual retry only after the full internal retry budget
// (initial attempt + downloadRetryCount retries) is exhausted.
func TestDownloadTaskFailsAfterRetriesExhausted(t *testing.T) {
	shortRetryDelay(t)
	withTempCwd(t)
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

	var requests int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		atomic.AddInt32(&requests, 1)
		w.WriteHeader(http.StatusServiceUnavailable)
	}))
	defer srv.Close()

	task := &DlTask{
		ID: "dl-exhaust", ModelID: "author/model", FileName: "exhaust.gguf",
		DestDir: filepath.Join(effectiveModelDownloadDir(), "author"),
		URL:     srv.URL, Status: "downloading",
	}
	task.ctx, task.cancel = context.WithCancel(context.Background())
	dlTasksMu.Lock()
	dlTasks = append(dlTasks, task)
	dlTasksMu.Unlock()
	// spawnDownloadTask registers the goroutine in dlTaskGoroutines, making it
	// visible to withTempCwd's cleanup drain.
	spawnDownloadTask(task)

	if status := pollTask(t, task, "error"); status != "error" {
		t.Fatalf("task status = %q, want error after exhausted retries", status)
	}
	dlTasksMu.Lock()
	taskErr := task.Error
	dlTasksMu.Unlock()
	if taskErr == "" {
		t.Error("task error message should be set for the manual retry UI")
	}
	if got := atomic.LoadInt32(&requests); got != int32(1+downloadRetryCount) {
		t.Errorf("server requests = %d, want %d (initial + 3 retries)", got, 1+downloadRetryCount)
	}
}

// TestDownloadTaskPermanentStatusNotRetried verifies permanent HTTP failures
// (404) surface immediately without burning retry attempts.
func TestDownloadTaskPermanentStatusNotRetried(t *testing.T) {
	shortRetryDelay(t)
	withTempCwd(t)
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

	var requests int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		atomic.AddInt32(&requests, 1)
		w.WriteHeader(http.StatusNotFound)
	}))
	defer srv.Close()

	task := &DlTask{
		ID: "dl-404", ModelID: "author/model", FileName: "missing.gguf",
		DestDir: filepath.Join(effectiveModelDownloadDir(), "author"),
		URL:     srv.URL, Status: "downloading",
	}
	task.ctx, task.cancel = context.WithCancel(context.Background())
	dlTasksMu.Lock()
	dlTasks = append(dlTasks, task)
	dlTasksMu.Unlock()
	// spawnDownloadTask registers the goroutine in dlTaskGoroutines, making it
	// visible to withTempCwd's cleanup drain.
	spawnDownloadTask(task)

	if status := pollTask(t, task, "error"); status != "error" {
		t.Fatalf("task status = %q, want error", status)
	}
	dlTasksMu.Lock()
	taskErr := task.Error
	dlTasksMu.Unlock()
	if taskErr != "HTTP 404" {
		t.Errorf("task error = %q, want HTTP 404", taskErr)
	}
	if got := atomic.LoadInt32(&requests); got != 1 {
		t.Errorf("server requests = %d, want 1 (404 is permanent, no retry)", got)
	}
}

// TestDownloadWithResumeRetriesTransientStatus verifies the llama.cpp
// download path auto-retries transient statuses and completes once the
// server recovers.
func TestDownloadWithResumeRetriesTransientStatus(t *testing.T) {
	shortRetryDelay(t)
	payload := []byte("llama.cpp asset bytes")
	var requests int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		n := atomic.AddInt32(&requests, 1)
		if n == 1 {
			w.WriteHeader(http.StatusInternalServerError)
			return
		}
		w.Header().Set("Content-Length", strconv.Itoa(len(payload)))
		w.Write(payload)
	}))
	defer srv.Close()

	path, err := downloadWithResume(context.Background(), srv.URL, int64(len(payload)), 0)
	if err != nil {
		t.Fatalf("downloadWithResume should succeed after retry: %v", err)
	}
	defer os.Remove(path)
	if got := atomic.LoadInt32(&requests); got != 2 {
		t.Errorf("server requests = %d, want 2 (one 500 + one success)", got)
	}
	got, err := os.ReadFile(path)
	if err != nil || string(got) != string(payload) {
		t.Errorf("downloaded file mismatch: %q (%v)", got, err)
	}
}

// TestDownloadUpdateWithResumeRetriesTransient verifies the app-update
// download path retries transient failures (each attempt restarting clean)
// and surfaces the error only after the retry budget is exhausted.
func TestDownloadUpdateWithResumeRetriesTransient(t *testing.T) {
	shortRetryDelay(t)

	t.Run("recovers", func(t *testing.T) {
		payload := []byte("update exe bytes")
		var requests int32
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			n := atomic.AddInt32(&requests, 1)
			if n <= 2 {
				w.WriteHeader(http.StatusBadGateway)
				return
			}
			w.Header().Set("Content-Length", strconv.Itoa(len(payload)))
			w.Write(payload)
		}))
		defer srv.Close()

		path, err := downloadUpdateWithResume(context.Background(), srv.URL, int64(len(payload)))
		if err != nil {
			t.Fatalf("downloadUpdateWithResume should succeed after retries: %v", err)
		}
		defer os.Remove(path)
		if got := atomic.LoadInt32(&requests); got != 3 {
			t.Errorf("server requests = %d, want 3", got)
		}
		got, err := os.ReadFile(path)
		if err != nil || string(got) != string(payload) {
			t.Errorf("downloaded file mismatch: %q (%v)", got, err)
		}
	})

	t.Run("exhausted", func(t *testing.T) {
		var requests int32
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			atomic.AddInt32(&requests, 1)
			w.WriteHeader(http.StatusBadGateway)
		}))
		defer srv.Close()

		path, err := downloadUpdateWithResume(context.Background(), srv.URL, 128)
		if err == nil {
			t.Fatal("downloadUpdateWithResume should fail after exhausted retries")
		}
		if path != "" {
			os.Remove(path)
		}
		if got := atomic.LoadInt32(&requests); got != int32(1+downloadRetryCount) {
			t.Errorf("server requests = %d, want %d", got, 1+downloadRetryCount)
		}
	})
}
