package core

import (
	"io"
	"path/filepath"
	"runtime"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

// ─── Ring helpers ─────────────────────────────────────────────────

// resetServerLogs clears the service-log ring buffer (protected by serverLogsMu) and
// restores it to empty after the test, preventing log pollution in one test case
// from affecting subsequent cases. The cursor is rewound too so sequence
// assertions start from a deterministic 0 (production never rewinds it).
func resetServerLogs(t *testing.T) {
	t.Helper()
	serverLogsMu.Lock()
	serverLogs = nil
	serverLogNext = 0
	serverLogsMu.Unlock()
	t.Cleanup(func() {
		serverLogsMu.Lock()
		serverLogs = nil
		serverLogNext = 0
		serverLogsMu.Unlock()
	})
}

// serverLogsCopy copies the current service logs under the lock for assertion use
// (must not read the global serverLogs directly).
func serverLogsCopy() []string {
	serverLogsMu.Lock()
	defer serverLogsMu.Unlock()
	out := make([]string, len(serverLogs))
	for i, e := range serverLogs {
		out[i] = e.text
	}
	return out
}

// waitForServerLogLine polls the ring until a line containing substr appears
// (the tailer runs asynchronously; appends need a poll or two).
func waitForServerLogLine(t *testing.T, substr string, timeout time.Duration) {
	t.Helper()
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		for _, line := range serverLogsCopy() {
			if strings.Contains(line, substr) {
				return
			}
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatalf("log line containing %q did not reach the ring within %v; ring = %v", substr, timeout, serverLogsCopy())
}

// ─── Log-file tailer over a pipe (no real processes) ─────────────

// TestServerLogTailerFollowsPipeThrottlesPartials drives the tailer path from
// an io.Pipe with an injected clock: complete lines reach the ring
// immediately, "partial" redraw pieces are throttled to the 400 ms window
// (the injected clock steps 100 ms per call, so the 1st and 5th partial are
// admitted and the 2nd–4th dropped), and after Stop the drain at EOF flushes
// the trailing newline-less fragment into the ring. The goroutine must exit.
func TestServerLogTailerFollowsPipeThrottlesPartials(t *testing.T) {
	resetServerLogs(t)

	base := time.Unix(1700000000, 0)
	var tick atomic.Int64
	origNow := serverLogNow
	serverLogNow = func() time.Time {
		return base.Add(time.Duration(tick.Add(1)) * 100 * time.Millisecond)
	}
	t.Cleanup(func() { serverLogNow = origNow })

	pr, pw := io.Pipe()
	tail := startServerLogTailerFromReader(pr)

	writes := []string{
		"line one\n",
		"p1\rp2\rp3\rp4\rp5\r", // five redraw frames → pieces p1..p5
		"final line\n",
		"tail without newline", // completed only by the drain-at-EOF flush
	}
	for _, w := range writes {
		if _, err := pw.Write([]byte(w)); err != nil {
			t.Fatal(err)
		}
	}

	// Child exited: stop the tailer, then close the stream so the drain
	// (reading to EOF) can flush the trailing fragment. Stop is idempotent.
	tail.Stop()
	tail.Stop()
	pw.Close()
	tail.WaitDone(5 * time.Second)

	got := serverLogsCopy()
	want := []string{"line one", "p1", "p5", "final line", "tail without newline"}
	if len(got) != len(want) {
		t.Fatalf("ring = %v, want %v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Errorf("ring[%d] = %q, want %q", i, got[i], want[i])
		}
	}
}

// TestServerLogTailerStripsANSIAndDropsEmptyPieces verifies ring-level
// policies: ANSI sequences are stripped from pieces before they enter the
// ring, empty lines / empty redraw frames / whitespace-only lines are dropped
// (preserving the previous ring-buffer behavior), and finalized redraw frames
// do reach the ring.
func TestServerLogTailerStripsANSIAndDropsEmptyPieces(t *testing.T) {
	resetServerLogs(t)

	pr, pw := io.Pipe()
	tail := startServerLogTailerFromReader(pr)

	writes := []string{
		"\x1b[31mcoloured line\x1b[0m\n", // SGR codes stripped at append time
		"\r\n",                           // CRLF empty line dropped
		"\x1b[2K\rprogress frame\r\n",    // empty redraw partial dropped, frame finalized
		"   \n",                          // whitespace-only line dropped
	}
	for _, w := range writes {
		if _, err := pw.Write([]byte(w)); err != nil {
			t.Fatal(err)
		}
	}
	tail.Stop()
	pw.Close()
	tail.WaitDone(5 * time.Second)

	got := serverLogsCopy()
	want := []string{"coloured line", "progress frame"}
	if len(got) != len(want) {
		t.Fatalf("ring = %v, want %v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Errorf("ring[%d] = %q, want %q", i, got[i], want[i])
		}
	}
}

// TestServerLogTailerCarriesSplitUTF8AcrossReads verifies the incremental
// decode behavior end to end: 2-byte and 3-byte runes cut by read boundaries
// are stitched back together, so the ring sees "héi 中" with no replacement
// characters.
func TestServerLogTailerCarriesSplitUTF8AcrossReads(t *testing.T) {
	resetServerLogs(t)

	pr, pw := io.Pipe()
	tail := startServerLogTailerFromReader(pr)

	// "héi 中" split so both the 2-byte é and the 3-byte 中 are cut by the
	// read boundary.
	chunks := []string{"h\xc3", "\xa9i \xe4\xb8", "\xad\n"}
	for _, w := range chunks {
		if _, err := pw.Write([]byte(w)); err != nil {
			t.Fatal(err)
		}
	}
	tail.Stop()
	pw.Close()
	tail.WaitDone(5 * time.Second)

	got := serverLogsCopy()
	if len(got) != 1 || got[0] != "héi 中" {
		t.Fatalf("ring = %q, want [\"héi 中\"] (no replacement-char garbage)", got)
	}
}

// ─── Log-file path resolution ─────────────────────────────────────

// TestAbsServerLogPath verifies the handover log-path resolution: a bare
// serverLogFile resolves under the app-data base on non-Windows (a
// cwd-relative name would be unwritable on Android / macOS .app where the cwd
// is "/") and stays cwd-relative on Windows; an already-absolute path passes
// through unchanged (tests, handover adoption).
func TestAbsServerLogPath(t *testing.T) {
	orig := serverLogFile
	t.Cleanup(func() { serverLogFile = orig })

	serverLogFile = "relative-server.log"
	var want string
	if runtime.GOOS == "windows" {
		tmp := withTempCwd(t)
		want = filepath.Join(tmp, "relative-server.log")
	} else {
		// Force the non-Windows app-data branch through the path seams so the
		// expectation stays deterministic (a real UserConfigDir would leak).
		root := t.TempDir()
		withPathsSeams(t, "linux", root, nil, nil)
		want = filepath.Join(root, appDataDirName, "relative-server.log")
	}
	if got := absServerLogPath(); got != want {
		t.Errorf("absServerLogPath() = %q, want %q", got, want)
	}

	abs := filepath.Join(t.TempDir(), "abs-server.log")
	serverLogFile = abs
	if got := absServerLogPath(); got != abs {
		t.Errorf("absServerLogPath() = %q, want the unchanged absolute path %q", got, abs)
	}
}
