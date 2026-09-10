package core

import (
	"context"
	"fmt"
	"io"
	"log"
	"net/http"
	"os"
	"path/filepath"
	"sync"
	"time"
)

// ─── Model download task queue ──────────────────────────────────
// Download task state, throttled queue persistence, and the resumable per-task
// download goroutine feeding the frontend TaskDock.

type DlTask struct {
	ID         string  `json:"id"`
	ModelID    string  `json:"modelId"`
	FileName   string  `json:"fileName"`
	DestDir    string  `json:"destDir"`
	Source     string  `json:"source"` // download source: hf / modelscope
	URL        string  `json:"-"`
	Status     string  `json:"status"`
	Progress   int     `json:"progress"`
	Total      int64   `json:"total"`
	Downloaded int64   `json:"downloaded"`
	SizeHuman  string  `json:"sizeHuman"`
	Speed      float64 `json:"speed"` // current download speed (bytes/sec)
	Error      string  `json:"error"`
	ctx        context.Context
	cancel     context.CancelFunc
	resumeCh   chan struct{}
}

var dlTasks []*DlTask
var dlTasksMu sync.Mutex
var dlTaskCounter int

// PersistedDlTask is the persisted form of download queue tasks (written to
// myllama-config.json). Differs from DlTask: runtime state such as URL /
// ctx / cancel / resumeCh is not persisted; the URL is rebuilt on loadConfig
// restore from Source + buildModelDownloadURL.
type PersistedDlTask struct {
	ID         string `json:"id"`
	ModelID    string `json:"modelId"`
	FileName   string `json:"fileName"`
	DestDir    string `json:"destDir"`
	Source     string `json:"source"`
	Status     string `json:"status"`
	Progress   int    `json:"progress"`
	Total      int64  `json:"total"`
	Downloaded int64  `json:"downloaded"`
	SizeHuman  string `json:"sizeHuman"`
	Error      string `json:"error"`
}

// computeSpeed computes the download speed (bytes/sec) from the sampling
// interval (seconds) and the bytes downloaded within it. Pure function:
// returns 0 when elapsed or delta is non-positive (not computable or no
// valid progress).
func computeSpeed(elapsedSec float64, deltaBytes int64) float64 {
	if elapsedSec <= 0 || deltaBytes <= 0 {
		return 0
	}
	return float64(deltaBytes) / elapsedSec
}

// lastTaskPersist is the timestamp of the last download-queue persistence;
// lastTaskPersistMu guards its reads/writes. Progress-update paths use
// persistTasksThrottled: saves less than 5 seconds after the previous one are
// skipped, preventing high-frequency download progress from saturating config
// file writes (#12 queue persistence).
var lastTaskPersist time.Time
var lastTaskPersistMu sync.Mutex

// persistTasks is the config-write step behind persistTasksNow and
// persistTasksThrottled. Package-level function var (same injection style as
// renameFile / idleReadTimeout) so tests that start download tasks can swap
// in a no-op: without it, a task goroutine's async persist can run after the
// test restored the original working directory and write the cwd-relative
// config file into the package directory (core/myllama-config.json).
// Production value is saveConfig, so runtime behavior is unchanged.
var persistTasks = saveConfig

// persistTasksNow persists the download task queue immediately (enqueue,
// status-change, and terminal-state paths). Callers must not hold dlTasksMu:
// persistTasks (→ saveConfig) acquires dlTasksMu again at the end for its
// snapshot.
func persistTasksNow() {
	lastTaskPersistMu.Lock()
	lastTaskPersist = time.Now()
	lastTaskPersistMu.Unlock()
	persistTasks()
}

// persistTasksThrottled persists the download task queue with throttling
// (progress-update paths): skips saves less than 5 seconds after the last one
// (whether triggered by persistTasksNow or this function).
func persistTasksThrottled() {
	lastTaskPersistMu.Lock()
	if time.Since(lastTaskPersist) < 5*time.Second {
		lastTaskPersistMu.Unlock()
		return
	}
	lastTaskPersist = time.Now()
	lastTaskPersistMu.Unlock()
	persistTasks()
}

// dlTaskGoroutines counts in-flight downloadTask goroutines. Every terminal
// branch of downloadTask persists the queue (persistTasksNow) as its FINAL
// action before returning, so a test that only waits for terminal task STATUS
// can still race the trailing config write (the status flips before the write
// completes; see TestStartHFDownloadNoDeadlock's TempDir-cleanup flake).
// spawnDownloadTask is the single paired Add/Done registration point.
var dlTaskGoroutines sync.WaitGroup

// spawnDownloadTask starts the download goroutine for one task, registered in
// dlTaskGoroutines so tests can drain it (waitDlGoroutinesForTest).
func spawnDownloadTask(task *DlTask) {
	dlTaskGoroutines.Add(1)
	go func() {
		defer dlTaskGoroutines.Done()
		downloadTask(task)
	}()
}

// retryDownloadTask rebuilds the task's download context and restarts the
// download goroutine. Once a task reaches a terminal error/cancelled/done
// state its ctx is already finished (the goroutine exited) and cannot be
// reused; a fresh context.WithCancel is required. downloadTask reads the
// .part file size at startup as the resume offset, naturally reusing
// resumable downloads. Callers must hold dlTasksMu.
func retryDownloadTask(task *DlTask) {
	task.ctx, task.cancel = context.WithCancel(context.Background())
	// Clear the error and stale progress display so the frontend stops
	// showing the previous red error box; downloadTask refills
	// Downloaded/Total/Progress from the .part resume offset.
	task.Error = ""
	task.Downloaded = 0
	task.Total = 0
	task.Progress = 0
	task.SizeHuman = ""
	task.Speed = 0
	task.Status = "queued"
	spawnDownloadTask(task)
}

// idleReadTimeout is how long the download loop waits for the next body chunk
// before treating the stream as stalled (half-open TCP connection or a server
// / proxy that stopped sending) and reconnecting via Range at the current
// .part size. Injectable so tests can use a short value; the production window
// is generous so slow-but-alive streams are never cut off.
var idleReadTimeout = 60 * time.Second

func downloadTask(task *DlTask) {
	dlTasksMu.Lock()
	task.Status = "downloading"
	dlTasksMu.Unlock()
	persistTasksNow()

	// Create dest directory
	if err := os.MkdirAll(task.DestDir, 0755); err != nil {
		dlTasksMu.Lock()
		task.Status = "error"
		task.Error = tr("创建目录失败: ", "Failed to create directory: ") + err.Error()
		task.Speed = 0
		dlTasksMu.Unlock()
		persistTasksNow()
		return
	}

	destPath := filepath.Join(task.DestDir, task.FileName)
	tmpPath := destPath + ".part"

	// Check if partial download exists for resume
	var offset int64
	if fi, err := os.Stat(tmpPath); err == nil {
		offset = fi.Size()
	}

	// Speed sampling state (exclusive to the downloadTask goroutine, no
	// locking needed): records the last sample time and byte count;
	// task.Speed is updated inside the read loop at intervals ≥1s.
	var lastSampleTime time.Time
	var lastSampleBytes int64

	client := &http.Client{Timeout: 30 * time.Minute}

	// Automatic transient-failure retries (see the download retry policy
	// block), shared by the connect / status / mid-stream error branches.
	retries := 0

	for {
		// Check cancellation
		select {
		case <-task.ctx.Done():
			dlTasksMu.Lock()
			if task.Status != "paused" {
				task.Status = "cancelled"
			}
			task.Speed = 0
			dlTasksMu.Unlock()
			persistTasksNow()
			return
		default:
		}

		// Per-attempt context: each connection attempt derives its own
		// cancellable context from the task context. Cancelling the attempt
		// makes the transport close the underlying connection — the reliable
		// way to unblock a body read parked on a half-open connection, which
		// a plain resp.Body.Close alone cannot guarantee across transports
		// (the pending Read may hold the body lock Close needs). attemptCancel
		// is called on every exit path of an attempt (success / error /
		// cancel / idle stall / pause) so no attempt context leaks; cancelling
		// task.ctx still terminates everything through context propagation.
		attemptCtx, attemptCancel := context.WithCancel(task.ctx)

		req, err := buildDownloadRequest(attemptCtx, task.URL, offset)
		if err != nil {
			attemptCancel()
			dlTasksMu.Lock()
			task.Status = "error"
			task.Error = err.Error()
			task.Speed = 0
			dlTasksMu.Unlock()
			persistTasksNow()
			return
		}

		resp, err := client.Do(req)
		if err != nil {
			attemptCancel()
			dlTasksMu.Lock()
			if task.Status == "paused" {
				resumeCh := task.resumeCh
				dlTasksMu.Unlock()
				waitForTaskResume(task, resumeCh)
				continue
			}
			// Cancel-vs-network-error race defense: when ctx is already
			// cancelled (e.g. the user just clicked cancel), the task should
			// be marked cancelled rather than error, preventing the race from
			// pushing the task back to the error terminal state (e.g. the
			// network error from hf-mirror actively aborting the stream).
			if task.ctx.Err() != nil {
				task.Status = "cancelled"
				task.Speed = 0
				dlTasksMu.Unlock()
				persistTasksNow()
				return
			}
			dlTasksMu.Unlock()
			// Automatic transient-failure retry (see the download retry
			// policy block): network errors reconnect up to
			// downloadRetryCount times, resuming from the .part on disk; the
			// task stays in downloading state so the UI never flashes error.
			if retries < downloadRetryCount {
				retries++
				log.Printf("[WARN] task %s attempt failed (%v), retrying %d/%d", task.ID, err, retries, downloadRetryCount)
				if sleepDownloadRetry(task.ctx) {
					continue
				}
				dlTasksMu.Lock()
				task.Status = "cancelled"
				task.Speed = 0
				dlTasksMu.Unlock()
				persistTasksNow()
				return
			}
			dlTasksMu.Lock()
			task.Status = "error"
			task.Error = err.Error()
			task.Speed = 0
			dlTasksMu.Unlock()
			persistTasksNow()
			return
		}

		if resp.StatusCode != http.StatusOK && resp.StatusCode != http.StatusPartialContent {
			status := resp.StatusCode
			attemptCancel()
			resp.Body.Close()
			// Same automatic retry for transient HTTP statuses (429/5xx);
			// permanent 4xx (404/403/...) surfaces immediately.
			if retryableDownloadStatus(status) && retries < downloadRetryCount {
				retries++
				log.Printf("[WARN] task %s got HTTP %d, retrying %d/%d", task.ID, status, retries, downloadRetryCount)
				if sleepDownloadRetry(task.ctx) {
					continue
				}
				dlTasksMu.Lock()
				task.Status = "cancelled"
				task.Speed = 0
				dlTasksMu.Unlock()
				persistTasksNow()
				return
			}
			dlTasksMu.Lock()
			task.Status = "error"
			task.Error = fmt.Sprintf("HTTP %d", status)
			task.Speed = 0
			dlTasksMu.Unlock()
			persistTasksNow()
			return
		}

		// Robustness against servers ignoring Range (#B3): when offset>0 this
		// request carried a Range header, but some servers (e.g. the
		// ModelScope repo endpoint) do not guarantee Range support and ignore
		// the header, returning the full body with 200. Appending that full
		// body to the .part at offset would duplicate content onto the
		// existing partial file and corrupt it. Handling: close the response,
		// truncate .part to 0, zero the offset, clear the progress display,
		// then continue the outer loop to reconnect. With offset=0 the next
		// request carries no Range header; if the server keeps ignoring Range
		// and returns 200, writing still starts from zero — the content is
		// correct, the reconnect happens only this once, no infinite loop.
		if offset > 0 && resp.StatusCode == http.StatusOK {
			attemptCancel()
			resp.Body.Close()
			dlTasksMu.Lock()
			task.Downloaded = 0
			task.Total = 0
			task.Progress = 0
			task.SizeHuman = ""
			task.Speed = 0
			dlTasksMu.Unlock()
			if err := os.Truncate(tmpPath, 0); err != nil {
				dlTasksMu.Lock()
				task.Status = "error"
				task.Error = tr("重置 .part 文件失败: ", "Failed to reset the .part file: ") + err.Error()
				task.Speed = 0
				dlTasksMu.Unlock()
				persistTasksNow()
				return
			}
			offset = 0
			// Reset the speed sampling baseline: after a full rewrite,
			// downloaded accumulates from 0 again.
			lastSampleTime = time.Time{}
			lastSampleBytes = 0
			continue
		}

		if resp.ContentLength > 0 {
			dlTasksMu.Lock()
			task.Total = offset + resp.ContentLength
			task.SizeHuman = formatBytes(task.Total)
			dlTasksMu.Unlock()
		}

		// Open temp file for append
		out, err := os.OpenFile(tmpPath, os.O_WRONLY|os.O_CREATE|os.O_APPEND, 0644)
		if err != nil {
			attemptCancel()
			resp.Body.Close()
			dlTasksMu.Lock()
			task.Status = "error"
			task.Error = err.Error()
			task.Speed = 0
			dlTasksMu.Unlock()
			persistTasksNow()
			return
		}

		buf := make([]byte, 32*1024)
		downloaded := offset

		// closeAttempt is the single closing path shared by both retry exits
		// of the read loop (idle stall and mid-stream error). Ordering
		// matters:
		//  1. attemptCancel first — the transport closes the connection,
		//     which unblocks an in-flight body read (the read may hold the
		//     body lock while parked on a half-open connection);
		//  2. close this attempt's response body and .part write handle;
		//  3. re-stat the .part — only after the handle closed (Windows-safe)
		//     — so offset matches the bytes actually on disk. A stale offset
		//     would make the server resend already-appended bytes and
		//     silently corrupt the file. Stat failure falls back to the
		//     in-memory count.
		closeAttempt := func() {
			attemptCancel()
			resp.Body.Close()
			out.Close()
			if fi, err := os.Stat(tmpPath); err == nil {
				offset = fi.Size()
			} else {
				offset = downloaded
			}
		}

	readLoop:
		for {
			// Check pause
			dlTasksMu.Lock()
			paused := task.Status == "paused"
			resumeCh := task.resumeCh
			dlTasksMu.Unlock()
			if paused {
				attemptCancel()
				resp.Body.Close()
				out.Close()
				dlTasksMu.Lock()
				task.Speed = 0
				dlTasksMu.Unlock()
				persistTasksNow()
				waitForTaskResume(task, resumeCh)
				// Update offset for resume
				if fi, err := os.Stat(tmpPath); err == nil {
					offset = fi.Size()
				}
				// Reset the speed sampling baseline: elapsed would be inflated
				// by the pause duration; re-establish sampling from the new
				// offset after resume so the first segment's speed is not
				// dragged down by the paused time.
				lastSampleTime = time.Time{}
				lastSampleBytes = 0
				break // outer loop will re-establish connection
			}

			// Interruptible read
			type readRes struct {
				n   int
				err error
			}
			ch := make(chan readRes, 1)
			go func() {
				n, err := resp.Body.Read(buf)
				ch <- readRes{n, err}
			}()

			var rr readRes
			readTimer := time.NewTimer(idleReadTimeout)
			select {
			case <-task.ctx.Done():
				readTimer.Stop()
				attemptCancel()
				resp.Body.Close()
				out.Close()
				dlTasksMu.Lock()
				task.Status = "cancelled"
				task.Speed = 0
				dlTasksMu.Unlock()
				persistTasksNow()
				return
			case rr = <-ch:
				readTimer.Stop()
			case <-readTimer.C:
				// No data arrived within the idle window: the stream has
				// stalled (half-open connection / server or proxy stopped
				// sending). closeAttempt cancels the attempt — which makes
				// the transport close the underlying connection and unblock
				// the parked body read — then closes this attempt's handles
				// and re-stats the .part, so the reconnect resumes with a
				// Range header from exactly where the stalled read left off:
				// no bytes lost or duplicated.
				closeAttempt()
				dlTasksMu.Lock()
				task.Speed = 0
				dlTasksMu.Unlock()
				// Reset the speed sampling baseline: elapsed would otherwise
				// be inflated by the stall duration.
				lastSampleTime = time.Time{}
				lastSampleBytes = 0
				break readLoop
			}

			if rr.n > 0 {
				if _, err := out.Write(buf[:rr.n]); err != nil {
					attemptCancel()
					resp.Body.Close()
					out.Close()
					dlTasksMu.Lock()
					task.Status = "error"
					task.Error = err.Error()
					task.Speed = 0
					dlTasksMu.Unlock()
					persistTasksNow()
					return
				}
				downloaded += int64(rr.n)
				dlTasksMu.Lock()
				task.Downloaded = downloaded
				if task.Total > 0 {
					task.Progress = int(float64(downloaded) * 100 / float64(task.Total))
				}
				// Speed sampling: update only at intervals ≥1s to avoid
				// high-frequency computation and jitter. After a pause/resume,
				// downloaded accumulates from the new offset; negative deltas
				// are treated as 0.
				now := time.Now()
				if lastSampleTime.IsZero() {
					lastSampleTime = now
					lastSampleBytes = downloaded
				} else if elapsed := now.Sub(lastSampleTime).Seconds(); elapsed >= 1.0 {
					delta := downloaded - lastSampleBytes
					if delta < 0 {
						delta = 0
					}
					task.Speed = computeSpeed(elapsed, delta)
					lastSampleTime = now
					lastSampleBytes = downloaded
				}
				dlTasksMu.Unlock()
				persistTasksThrottled()
			}
			if rr.err == io.EOF {
				attemptCancel()
				resp.Body.Close()
				out.Close()
				// On move failure, mark the task as errored and return without
				// advancing to done (#10). moveFile internally uses the
				// injectable package-level variable renameFile so tests can
				// simulate failure; across devices (cross-drive on Windows) it
				// falls back to copy + delete source.
				if err := moveFile(tmpPath, destPath); err != nil {
					dlTasksMu.Lock()
					task.Status = "error"
					task.Error = tr("重命名失败: ", "Rename failed: ") + err.Error()
					task.Speed = 0
					dlTasksMu.Unlock()
					persistTasksNow()
					return
				}
				dlTasksMu.Lock()
				task.Status = "done"
				task.Progress = 100
				task.Speed = 0
				dlTasksMu.Unlock()
				persistTasksNow()
				invalidateModelCache()
				return
			}
			if rr.err != nil {
				// The read already returned, so the closes cannot block on a
				// pending read. closeAttempt also cancels the attempt and
				// re-stats the .part so a following retry resumes from the
				// bytes actually on disk — the failed attempt already
				// appended everything it received to the append-open .part.
				closeAttempt()
				dlTasksMu.Lock()
				// Cancel-vs-read-error race defense: when ctx is already
				// cancelled, mark cancelled rather than error (same strategy
				// as the client.Do error branch).
				if task.ctx.Err() != nil {
					task.Status = "cancelled"
					task.Speed = 0
					dlTasksMu.Unlock()
					persistTasksNow()
					return
				}
				dlTasksMu.Unlock()
				// Mid-body stream failures are transient: reconnect and
				// resume from the .part size on disk (closeAttempt above
				// refreshed the offset after the write handle closed).
				if retries < downloadRetryCount {
					retries++
					log.Printf("[WARN] task %s stream failed (%v), retrying %d/%d", task.ID, rr.err, retries, downloadRetryCount)
					if sleepDownloadRetry(task.ctx) {
						// Reset the speed sampling baseline: elapsed would
						// otherwise include the retry backoff.
						lastSampleTime = time.Time{}
						lastSampleBytes = 0
						break readLoop
					}
					dlTasksMu.Lock()
					task.Status = "cancelled"
					task.Speed = 0
					dlTasksMu.Unlock()
					persistTasksNow()
					return
				}
				dlTasksMu.Lock()
				task.Status = "error"
				task.Error = rr.err.Error()
				task.Speed = 0
				dlTasksMu.Unlock()
				persistTasksNow()
				return
			}
		}
	}
}

// buildDownloadRequest creates a GET request for a download URL with the
// appUserAgent() User-Agent, adding a Range header when resuming from an offset.
// The request is bound to the caller's context — the per-attempt context
// derived from the task's — so cancelling either the task or the current
// attempt aborts an in-flight transfer immediately.
func buildDownloadRequest(ctx context.Context, downloadURL string, offset int64) (*http.Request, error) {
	req, err := http.NewRequestWithContext(ctx, "GET", downloadURL, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("User-Agent", appUserAgent())
	if offset > 0 {
		req.Header.Set("Range", fmt.Sprintf("bytes=%d-", offset))
	}
	return req, nil
}

func waitForTaskResume(task *DlTask, resumeCh chan struct{}) {
	select {
	case <-resumeCh:
	case <-task.ctx.Done():
	}
}
