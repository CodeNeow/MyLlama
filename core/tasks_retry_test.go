package core

import (
	"bytes"
	"context"
	"fmt"
	"net"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"
)

// ─── downloadTask retry-path regression tests ─────────────────────────────
//
// These tests exercise the two retry exits of the downloadTask read loop
// (mid-stream read error and idle-stall timeout) against raw TCP servers, so
// the exact byte framing and connection lifetime are under test control (an
// httptest handler cannot keep a connection open and silent, nor close it
// mid-body without the framework's cleanup).

// readRawRequestHead reads bytes off a raw TCP connection until the HTTP
// request head terminator (\r\n\r\n) and returns the Range header value (""
// when absent). ok is false when the connection died before a full head
// arrived.
func readRawRequestHead(c net.Conn) (rangeHdr string, ok bool) {
	head := make([]byte, 0, 4096)
	buf := make([]byte, 1024)
	for !bytes.Contains(head, []byte("\r\n\r\n")) {
		n, err := c.Read(buf)
		if err != nil {
			return "", false
		}
		head = append(head, buf[:n]...)
		if len(head) > 64*1024 {
			return "", false
		}
	}
	for _, line := range strings.Split(string(head), "\r\n") {
		if strings.HasPrefix(line, "Range: ") {
			rangeHdr = strings.TrimPrefix(line, "Range: ")
		}
	}
	return rangeHdr, true
}

// resetDlTasksForTest clears the download task globals so a directly-invoked
// downloadTask starts from a clean queue state.
func resetDlTasksForTest() {
	dlTasksMu.Lock()
	dlTasks = nil
	dlTaskCounter = 0
	dlTasksMu.Unlock()
}

// newDlTaskForTest builds a queued task downloading model.gguf from url into
// destDir with a live cancel context.
func newDlTaskForTest(url, destDir string) *DlTask {
	task := &DlTask{
		ID:       "dl-retry-1",
		ModelID:  "author/model",
		FileName: "model.gguf",
		DestDir:  destDir,
		URL:      url,
		Status:   "queued",
	}
	task.ctx, task.cancel = context.WithCancel(context.Background())
	return task
}

// serveRangeOrFull answers a raw request like a range-capable file server:
// with a "bytes=N-" Range header it serves 206 with payload[N:]; without one
// it serves 200 with the full payload. This is what makes the stale-offset
// bug observable as corruption: a reconnect that lost its Range re-downloads
// from zero and the bytes get appended onto the partial file a second time.
func serveRangeOrFull(c net.Conn, rangeHdr string, payload []byte) error {
	if rangeHdr != "" {
		off := int64(0)
		if _, err := fmt.Sscanf(rangeHdr, "bytes=%d-", &off); err != nil || off < 0 || off > int64(len(payload)) {
			c.Write([]byte("HTTP/1.1 416 Requested Range Not Satisfiable\r\nContent-Length: 0\r\n\r\n"))
			return nil
		}
		hdr := fmt.Sprintf("HTTP/1.1 206 Partial Content\r\nContent-Range: bytes %d-%d/%d\r\nContent-Length: %d\r\n\r\n",
			off, len(payload)-1, len(payload), len(payload)-int(off))
		if _, err := c.Write([]byte(hdr)); err != nil {
			return err
		}
		_, err := c.Write(payload[off:])
		return err
	}
	hdr := fmt.Sprintf("HTTP/1.1 200 OK\r\nContent-Length: %d\r\n\r\n", len(payload))
	if _, err := c.Write([]byte(hdr)); err != nil {
		return err
	}
	_, err := c.Write(payload)
	return err
}

// TestDownloadTaskMidStreamRetryResumesAtDiskOffset is the mid-stream retry
// regression test: when the body read fails mid-stream (here: the server
// declares Content-Length 16 but delivers only the first 8 bytes and closes),
// the automatic retry must reconnect with a Range header starting at the
// .part size actually on disk. The failed attempt already appended its
// received bytes to the append-open .part, so a retry built from the stale
// pre-attempt offset makes the server resend those bytes and duplicates them
// onto the file — the download then "completes" with corrupt content.
// Assertions: the reconnect Range starts exactly at the delivered byte
// count, exactly one reconnect happens, and the final file equals the full
// payload with no duplicated prefix.
func TestDownloadTaskMidStreamRetryResumesAtDiskOffset(t *testing.T) {
	withTempCwd(t)
	resetDlTasksForTest()
	defer resetDlTasksForTest()

	// Fast retry backoff for a quick run; the retry count stays at its
	// production value (a single retry is needed here).
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

				if reqNum == 1 {
					// First request: declare 16 bytes, deliver only the
					// first 8, then close the connection — the client's
					// next body read fails mid-stream (unexpected EOF),
					// driving the mid-stream retry branch.
					c.Write([]byte("HTTP/1.1 200 OK\r\nContent-Length: 16\r\n\r\n"))
					c.Write(payload[:8])
					return
				}
				// Reconnect: the Range header must resume exactly at the
				// delivered byte count (the .part already holds those 8
				// bytes). Anything else is recorded; the response below
				// mirrors a real server so a missing Range duplicates the
				// body onto the partial file.
				if rangeHdr != "bytes=8-" {
					t.Errorf("reconnect Range header = %q, want bytes=8- (stale offset makes the server resend already-appended bytes)", rangeHdr)
				}
				serveRangeOrFull(c, rangeHdr, payload)
			}(conn)
		}
	}()

	destDir := filepath.Join(effectiveModelDownloadDir(), "author")
	if err := os.MkdirAll(destDir, 0755); err != nil {
		t.Fatal(err)
	}

	task := newDlTaskForTest("http://"+ln.Addr().String(), destDir)
	defer task.cancel()

	downloadTask(task)

	dlTasksMu.Lock()
	status, errMsg := task.Status, task.Error
	dlTasksMu.Unlock()
	if status != "done" {
		t.Fatalf("task status = %q, want done (mid-stream failure must retry and finish); error = %q", status, errMsg)
	}

	got, err := os.ReadFile(filepath.Join(destDir, "model.gguf"))
	if err != nil {
		t.Fatalf("downloaded file not written to disk: %v", err)
	}
	if string(got) != string(payload) {
		t.Errorf("file content = %q (%d bytes), want exactly %q (retry must resume at the .part size, not append the re-sent bytes a second time)", got, len(got), payload)
	}

	mu.Lock()
	count, ranges := reqCount, strings.Join(reqRanges, "; ")
	mu.Unlock()
	if count != 2 {
		t.Errorf("request count = %d, want 2 (initial stream + one mid-stream retry); ranges: %s", count, ranges)
	}
}

// TestDownloadTaskIdleRecoveryHalfOpenConn is the idle-stall recovery
// regression test: the server sends one chunk and then goes silent forever
// while holding the connection open (a half-open connection / proxy stall —
// impossible to simulate with an httptest handler, hence the raw TCP server).
// The read loop's idle timeout must tear down the attempt (cancelling the
// per-attempt context forces the transport to close the underlying
// connection, which unblocks the parked body read), reconnect with a Range
// header at the .part size, and finish the file — all within a bounded
// deadline, never hanging until the 30-minute client timeout.
func TestDownloadTaskIdleRecoveryHalfOpenConn(t *testing.T) {
	withTempCwd(t)
	resetDlTasksForTest()
	defer resetDlTasksForTest()

	// Idle window at the hundred-millisecond scale per the recovery
	// contract; the retry backoff is shortened so the reconnect is immediate.
	oldTimeout := idleReadTimeout
	idleReadTimeout = 150 * time.Millisecond
	defer func() { idleReadTimeout = oldTimeout }()
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

				if reqNum == 1 {
					// First request: deliver half the payload, then stall
					// WITHOUT closing — the connection stays open and
					// silent, the client body read parks on it forever.
					c.Write([]byte("HTTP/1.1 200 OK\r\nContent-Length: 16\r\n\r\n"))
					c.Write(payload[:8])
					<-make(chan struct{}) // blocked forever; torn down by the client
					return
				}
				// Reconnect: must resume exactly at the stalled offset.
				if rangeHdr != "bytes=8-" {
					t.Errorf("reconnect Range header = %q, want bytes=8- (stall recovery must resume at the .part size)", rangeHdr)
				}
				serveRangeOrFull(c, rangeHdr, payload)
			}(conn)
		}
	}()

	destDir := filepath.Join(effectiveModelDownloadDir(), "author")
	if err := os.MkdirAll(destDir, 0755); err != nil {
		t.Fatal(err)
	}

	task := newDlTaskForTest("http://"+ln.Addr().String(), destDir)
	defer task.cancel()

	done := make(chan struct{})
	go func() {
		defer close(done)
		downloadTask(task)
	}()

	// Deadline guard: the idle recovery must reconnect and finish within a
	// bound that is orders of magnitude below the 30-minute client timeout.
	// On timeout, cancel the task so the worker goroutine can still exit
	// before the failure is reported.
	select {
	case <-done:
	case <-time.After(10 * time.Second):
		task.cancel()
		t.Fatal("downloadTask did not finish within 10s (idle-stall recovery failed to reconnect; the attempt hung instead of resuming)")
	}

	dlTasksMu.Lock()
	status, errMsg := task.Status, task.Error
	dlTasksMu.Unlock()
	if status != "done" {
		t.Fatalf("task status = %q, want done (idle timeout must reconnect and finish the download); error = %q", status, errMsg)
	}

	got, err := os.ReadFile(filepath.Join(destDir, "model.gguf"))
	if err != nil {
		t.Fatalf("downloaded file not written to disk: %v", err)
	}
	// The resumed append must not duplicate the first half nor lose the rest.
	if string(got) != string(payload) {
		t.Errorf("file content = %q, want exactly %q (stall-reconnect must resume exactly at the .part size)", got, payload)
	}

	mu.Lock()
	count, ranges := reqCount, strings.Join(reqRanges, "; ")
	mu.Unlock()
	if count != 2 {
		t.Errorf("request count = %d, want 2 (initial stalled stream + one Range reconnect); ranges: %s", count, ranges)
	}
}
