package core

import (
	"net"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"
)

// ─── ResumeDownloadTask pause-flavor regression tests ─────────────────────
//
// A paused task comes in two flavors and ResumeDownloadTask must handle both:
//   - user-paused: the downloadTask goroutine is alive, parked in
//     waitForTaskResume — resume via the resumeCh signal (no second spawn);
//   - restart-restored: loadConfig rebuilds the queue with status=paused, a
//     fresh buffered resumeCh, nil ctx/cancel and running == false (zero
//     value) — NO goroutine exists, so signaling resumeCh reaches nobody and
//     the task would stay "downloading" at its old progress forever (the
//     zombie bug). Resume must respawn through the retryDownloadTask path.
//
// Both tests drive raw TCP servers (same helpers as tasks_retry_test.go) so
// request framing, Range headers and connection lifetime stay under test
// control.

// TestResumeRestoredPausedTaskRestartsGoroutine is the zombie-resume
// regression test: the task mirrors loadConfig's restored state (paused,
// nil ctx/cancel, fresh resumeCh, running zero-value false) — constructing
// the fields directly is equivalent to the loadConfig restore output without
// running the full config-file round trip. A partial .part (first 8 bytes)
// is pre-seeded, the state an interrupted download leaves on disk. The old
// implementation only signaled resumeCh (nobody listening): the task stuck
// on "downloading" with zero requests and Retry refused a paused task. The
// fix respawns the goroutine, so exactly ONE ranged request must arrive,
// starting at the .part size, and the file must complete byte-exact.
func TestResumeRestoredPausedTaskRestartsGoroutine(t *testing.T) {
	withTempCwd(t)
	resetDlTasksForTest()
	defer resetDlTasksForTest()

	payload := []byte("0123456789abcdef") // 16 bytes full content
	var mu sync.Mutex
	var reqCount int
	var reqRanges []string

	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer ln.Close()

	// serveGate holds the response back until the test has asserted the
	// post-resume transient status, keeping the restarted goroutine from
	// finishing before the branch check.
	serveGate := make(chan struct{})

	go func() {
		for {
			conn, err := ln.Accept()
			if err != nil {
				return
			}
			go func(c net.Conn) {
				defer c.Close()
				rangeHdr, ok := readRawRequestHead(c)
				if !ok {
					return
				}
				mu.Lock()
				reqCount++
				reqRanges = append(reqRanges, rangeHdr)
				mu.Unlock()
				// The resumed request must start exactly at the seeded .part
				// size; a stale or missing Range corrupts the file.
				if rangeHdr != "bytes=8-" {
					t.Errorf("resume Range header = %q, want bytes=8-", rangeHdr)
				}
				<-serveGate
				serveRangeOrFull(c, rangeHdr, payload)
			}(conn)
		}
	}()

	destDir := filepath.Join(effectiveModelDownloadDir(), "author")
	if err := os.MkdirAll(destDir, 0755); err != nil {
		t.Fatal(err)
	}
	partPath := filepath.Join(destDir, "model.gguf.part")
	if err := os.WriteFile(partPath, payload[:8], 0644); err != nil {
		t.Fatal(err)
	}

	// Mirror loadConfig's restored task: status normalized to paused, fresh
	// buffered resumeCh, nil ctx/cancel (rebuilt by the resume path), running
	// zero-value false (no goroutine after a restart).
	task := &DlTask{
		ID:         "dl-restored-1",
		ModelID:    "author/model",
		FileName:   "model.gguf",
		DestDir:    destDir,
		URL:        "http://" + ln.Addr().String(),
		Status:     "paused",
		Progress:   50,
		Total:      16,
		Downloaded: 8,
		resumeCh:   make(chan struct{}, 1),
	}
	dlTasksMu.Lock()
	dlTasks = append(dlTasks, task)
	dlTasksMu.Unlock()

	app := &App{}
	if err := app.ResumeDownloadTask(task.ID); err != nil {
		t.Fatalf("ResumeDownloadTask returned error: %v", err)
	}

	// The respawn path rebuilds ctx and starts the goroutine, so the status
	// is queued (not yet scheduled) or downloading (already flipped) — never
	// paused. The signal path would have flipped it synchronously.
	dlTasksMu.Lock()
	status := task.Status
	dlTasksMu.Unlock()
	if status != "queued" && status != "downloading" {
		t.Errorf("status right after resume = %q, want queued or downloading (respawned goroutine)", status)
	}

	close(serveGate) // let the server answer the resumed request

	deadline := time.Now().Add(5 * time.Second)
	for {
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
			t.Fatalf("resumed download failed: %q", e)
		}
		if time.Now().After(deadline) {
			t.Fatalf("task status = %q, want done (zombie fix must spawn a real download goroutine)", status)
		}
		time.Sleep(10 * time.Millisecond)
	}
	// Release the finished context resources (the respawned goroutine rebuilt
	// ctx/cancel; the goroutine itself has exited by the done status).
	dlTasksMu.Lock()
	cancelFn := task.cancel
	dlTasksMu.Unlock()
	if cancelFn != nil {
		cancelFn()
	}

	got, err := os.ReadFile(filepath.Join(destDir, "model.gguf"))
	if err != nil {
		t.Fatalf("downloaded file not written to disk: %v", err)
	}
	if string(got) != string(payload) {
		t.Errorf("file content = %q (%d bytes), want exactly %q (must resume from the .part, not duplicate it)", got, len(got), payload)
	}

	mu.Lock()
	count, ranges := reqCount, strings.Join(reqRanges, "; ")
	mu.Unlock()
	if count != 1 {
		t.Errorf("request count = %d, want 1 (single goroutine, one ranged resume request); ranges: %s", count, ranges)
	}
}

// TestResumeUserPausedTaskSingleGoroutine pins the user-pause resume path:
// pausing a task whose goroutine is alive and resuming it must continue the
// SAME goroutine via the resumeCh signal — no second goroutine may spawn
// (a respawn would issue duplicate requests and race two writers onto the
// same .part). Server choreography: request 1 delivers the first 8 bytes and
// holds the connection open, so the client parks on its next body read; the
// test pauses there, closing the connection drives the automatic retry into
// request 2, whose connection is closed without a response — with the task
// paused, downloadTask either parks in waitForTaskResume or loops through
// one more retry; in both interleavings running stays true, so Resume must
// take the signal path. Assertions: right after ResumeDownloadTask returns
// the status is already "downloading" (the signal path flips it
// synchronously; a respawn would leave "queued"), exactly three scripted
// requests occur (full + two ranged reconnects), and the final file equals
// the full payload byte for byte.
func TestResumeUserPausedTaskSingleGoroutine(t *testing.T) {
	withTempCwd(t)
	resetDlTasksForTest()
	defer resetDlTasksForTest()

	// Shorten the automatic-retry backoff; the retry count stays at its
	// production value (at most two retries are consumed here).
	oldDelay := downloadRetryDelay
	downloadRetryDelay = 10 * time.Millisecond
	defer func() { downloadRetryDelay = oldDelay }()

	payload := []byte("0123456789abcdef") // 16 bytes full content
	var mu sync.Mutex
	var reqCount int
	var reqRanges []string

	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer ln.Close()

	firstHold := make(chan struct{}) // request 1: hold the conn open + silent
	// resumeGate holds request 3's response back until the test has asserted
	// the post-resume status: ResumeDownloadTask's trailing queue persist
	// (saveConfig disk write) would otherwise give the goroutine enough time
	// to finish request 3 and reach done before the test reads the status.
	resumeGate := make(chan struct{})

	go func() {
		for {
			conn, err := ln.Accept()
			if err != nil {
				return
			}
			go func(c net.Conn) {
				defer c.Close()
				rangeHdr, ok := readRawRequestHead(c)
				if !ok {
					return
				}
				mu.Lock()
				reqCount++
				reqNum := reqCount
				reqRanges = append(reqRanges, rangeHdr)
				mu.Unlock()

				switch reqNum {
				case 1:
					if rangeHdr != "" {
						t.Errorf("first request Range = %q, want empty (fresh download)", rangeHdr)
					}
					c.Write([]byte("HTTP/1.1 200 OK\r\nContent-Length: 16\r\n\r\n"))
					c.Write(payload[:8])
					<-firstHold // held open + silent: parks the client body read
					return      // close → ErrUnexpectedEOF → automatic retry
				case 2:
					// Closed without a response: with the task paused, the
					// client.Do error branch either parks on resumeCh or
					// burns one retry — either way the goroutine stays alive.
				case 3:
					// Post-resume reconnect: completes the download from the
					// .part offset, but only once the test has pinned the
					// signal-path status flip.
					if rangeHdr != "bytes=8-" {
						t.Errorf("resume request Range = %q, want bytes=8-", rangeHdr)
					}
					<-resumeGate
					serveRangeOrFull(c, rangeHdr, payload)
				}
			}(conn)
		}
	}()

	destDir := filepath.Join(effectiveModelDownloadDir(), "author")
	if err := os.MkdirAll(destDir, 0755); err != nil {
		t.Fatal(err)
	}

	task := newDlTaskForTest("http://"+ln.Addr().String(), destDir)
	defer task.cancel()
	dlTasksMu.Lock()
	dlTasks = append(dlTasks, task)
	dlTasksMu.Unlock()
	// spawnDownloadTask registers the goroutine in dlTaskGoroutines, making it
	// visible to withTempCwd's cleanup drain, and sets the running flag.
	spawnDownloadTask(task)

	partPath := filepath.Join(destDir, "model.gguf.part")
	deadline := time.Now().Add(5 * time.Second)
	for {
		if fi, err := os.Stat(partPath); err == nil && fi.Size() == 8 {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("first 8 bytes never reached the .part file")
		}
		time.Sleep(10 * time.Millisecond)
	}

	// User pause while the goroutine is parked on the held connection.
	if err := (&App{}).PauseDownloadTask(task.ID); err != nil {
		t.Fatal(err)
	}
	dlTasksMu.Lock()
	status := task.Status
	dlTasksMu.Unlock()
	if status != "paused" {
		t.Fatalf("status after pause = %q, want paused", status)
	}
	close(firstHold) // teardown attempt 1 → automatic retry reconnect (req 2)

	// Wait for the second request (closed without a response), then resume.
	deadline = time.Now().Add(5 * time.Second)
	for {
		mu.Lock()
		count := reqCount
		mu.Unlock()
		if count >= 2 {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("retry reconnect (request 2) never arrived")
		}
		time.Sleep(10 * time.Millisecond)
	}

	// The goroutine is alive (running flag true), so Resume must take the
	// signal path: the status flips to downloading synchronously — a respawn
	// path would leave "queued" here. Request 3's response is still gated, so
	// the goroutine cannot reach done before this read.
	if err := (&App{}).ResumeDownloadTask(task.ID); err != nil {
		t.Fatalf("ResumeDownloadTask returned error: %v", err)
	}
	dlTasksMu.Lock()
	status = task.Status
	dlTasksMu.Unlock()
	if status != "downloading" {
		t.Errorf("status right after resume = %q, want downloading (signal path on a live goroutine; a respawn would show queued)", status)
	}
	close(resumeGate) // let request 3 complete the download

	// Wait for completion through the post-resume reconnect (request 3).
	deadline = time.Now().Add(5 * time.Second)
	for {
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
			t.Fatalf("resumed download failed: %q", e)
		}
		if time.Now().After(deadline) {
			t.Fatalf("task status = %q, want done after user pause + resume", status)
		}
		time.Sleep(10 * time.Millisecond)
	}

	got, err := os.ReadFile(filepath.Join(destDir, "model.gguf"))
	if err != nil {
		t.Fatalf("downloaded file not written to disk: %v", err)
	}
	if string(got) != string(payload) {
		t.Errorf("file content = %q (%d bytes), want exactly %q (two concurrent goroutines would duplicate or interleave bytes)", got, len(got), payload)
	}

	mu.Lock()
	count, ranges := reqCount, strings.Join(reqRanges, "; ")
	mu.Unlock()
	// Exactly three requests pin a single goroutine: initial stream + one
	// retry reconnect after the held connection closed + one post-resume
	// reconnect. A respawned second goroutine adds a duplicate ranged (or
	// full) request; interleaved .part writers corrupt the content above.
	if count != 3 {
		t.Errorf("request count = %d, want 3 (initial + retry reconnect + post-resume reconnect); ranges: %s", count, ranges)
	}
	if ranges != "; bytes=8-; bytes=8-" {
		t.Errorf("request ranges = %q, want the reconnects to resume at bytes=8- twice", ranges)
	}
}
