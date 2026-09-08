package core

import (
	"errors"
	"fmt"
	"io"
	"log"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"
)

// ─── llama-server lifecycle state ───────────────────────────────
// Process handle, log ring buffer and the log-file tailer feeding it, for the
// llama-server child process; start/stop logic lives in bridge.go.

var serverCmd *exec.Cmd

// serverLogEntry is one ring-buffer entry: the log line plus the monotonic
// sequence number it was appended under (the cursor value the incremental
// GetServerLogsSince fetch keys on).
type serverLogEntry struct {
	seq  int64
	text string
}

var serverLogs []serverLogEntry
var serverLogsMu sync.Mutex
var serverRunning bool

// serverMu guards the full lifecycle of serverCmd and serverRunning
// (create/start/stop/cleanup), separate in responsibility from serverLogsMu
// which only guards serverLogs (#3). Any path holding both locks must acquire
// them in the order "serverMu first, then serverLogsMu" to avoid deadlock.
var serverMu sync.Mutex

// serverStartTime records when llama-server started successfully (guarded by
// serverMu), used by GetMonitorStatus to compute uptime; zeroed when the
// process exits (in the cmd.Wait goroutine).
var serverStartTime time.Time

// serverTrueStart records the llama-server process's REAL creation time for
// the handover record's pid-reuse defense (guarded by serverMu): a fresh child
// start stamps it with time.Now() right after Start succeeds (bridge.go, next
// to serverStartTime), while adoptHandover chains the adopted record's stored
// serverStartedAt so a re-handover (headless→GUI→headless) never loses the
// original value. writeHandover formats this into the record; zero means
// unknown and the record omits the field (the successor's start-time check
// then fails open).
var serverTrueStart time.Time

// serverPort records the port used by the successfully started llama-server
// (guarded by serverMu), 0 means not running. Router API queries use this
// value instead of the current config, so editing the config mid-run cannot
// redirect queries to the wrong address.
var serverPort int

// serverLogTail is the active log tailer for the running llama-server (nil
// when log capture is not active). Guarded by serverMu, consistent with the
// other server lifecycle state.
var serverLogTail *serverLogTailer

// serverLogFile is the llama-server log capture path: the child's stdout and
// stderr both write to this file, and a tailer follows it into the ring.
// Declared as a var (same style as configFile / handoverFile) and resolved
// through resolveServerLogPath at use time — bare names land under the
// app-data base on non-Windows (a bare cwd-relative name would be unwritable
// on Android/macOS where the cwd is "/") — so tests can redirect it to
// t.TempDir() by assigning an absolute path, which passes through unchanged.
// Adopting a handed-over server overwrites it with the absolute path from the
// handover record (written under serverMu at the same lifecycle moments that
// touch the other server globals). Transient per run: unlike the other state
// files there is no legacy-name migration (the pre-rebrand
// llama-desktop-server.log is simply abandoned).
var serverLogFile = "myllama-server.log"

// Tailer tuning knobs, declared as vars (same style as cmdTimeout) so tests
// can shrink the intervals or inject a fake clock.
var (
	// serverLogNow is the clock used for partial-redraw throttling.
	serverLogNow = time.Now
	// serverLogThrottle is the minimum interval between ring entries for
	// "partial" redraw pieces (progress bars redraw many times per second).
	serverLogThrottle = 400 * time.Millisecond
	// serverLogIdle is the poll sleep when the log file has no new data.
	serverLogIdle = 100 * time.Millisecond
)

// serverLogsCap is the ring buffer ceiling; addServerLog evicts the oldest
// entries beyond it (2000 retained lines versus the previous 200).
const serverLogsCap = 2000

// serverLogNext is the sequence number the next appended entry receives:
// monotonic for the process lifetime — eviction and ring clears never rewind
// it — so incremental consumers can fetch by cursor. Guarded by
// serverLogsMu alongside the ring it indexes.
var serverLogNext int64

// addServerLog appends one entry to the ring buffer and mirrors it to the
// process log. Eviction keeps the newest serverLogsCap entries; the appended
// entry's sequence number (taken from serverLogNext before the increment)
// survives eviction, so GetServerLogsSince can page by cursor.
func addServerLog(msg string) {
	serverLogsMu.Lock()
	serverLogs = append(serverLogs, serverLogEntry{seq: serverLogNext, text: msg})
	serverLogNext++
	if len(serverLogs) > serverLogsCap {
		// Evict the oldest entries beyond the cap; the reslice slides the
		// window forward and append refills the backing array in place.
		serverLogs = serverLogs[len(serverLogs)-serverLogsCap:]
	}
	serverLogsMu.Unlock()
	log.Println("[llama-server]", msg)
}

// serverLogsSince returns the retained entries with seq >= since plus the
// next cursor value (the seq the next appended entry will receive, so it is
// also the exclusive upper bound of assigned sequences). A since of 0 — or
// any value at or below the oldest retained seq — returns everything
// retained. Evicted lines are unrecoverable: when since is older than the
// oldest retained entry the result covers only what remains, and the caller
// must detect the gap (next - since > serverLogsCap) and fall back to a full
// refetch from 0.
func serverLogsSince(since int64) ([]serverLogEntry, int64) {
	serverLogsMu.Lock()
	defer serverLogsMu.Unlock()
	// Entries are ordered by seq; binary-search the first retained entry at
	// or after the cursor.
	i := sort.Search(len(serverLogs), func(i int) bool { return serverLogs[i].seq >= since })
	out := make([]serverLogEntry, len(serverLogs)-i)
	copy(out, serverLogs[i:])
	return out, serverLogNext
}

// absServerLogPath resolves the server log file to an absolute path for the
// handover record: the successor process must not depend on our cwd.
func absServerLogPath() string {
	abs, err := filepath.Abs(resolveServerLogPath())
	if err != nil {
		return resolveServerLogPath()
	}
	return abs
}

// ─── Log-file tailer ─────────────────────────────────────────────
//
// Follows the llama-server log file into the ring — the same file the child
// writes stdout+stderr to. Following a file (instead of holding pipes) means
// the stream is identical whether the server is a child we spawned or one
// re-adopted after a GUI↔headless handover: an adopted child belongs to the
// previous (exited) process, so there is no pipe to us — any process can
// simply re-open the file where the last one left off.
//
// The tailer must always terminate: it loops on the stopped channel with
// short reads, and after stop it drains whatever remains and exits — no
// goroutine/fd leak per start/stop cycle. It must also never take the app
// down: the goroutine recovers defensively (see startServerLogTailerFromReader).

// serverLogTailer follows one log stream into the ring. stopped is closed by
// Stop (idempotent, safe from any goroutine); done is closed when the tailer
// goroutine has exited. asm and lastProgress are touched only by the tailer
// goroutine, so they need no lock.
type serverLogTailer struct {
	stopOnce     sync.Once
	stopped      chan struct{}
	done         chan struct{}
	asm          lineAssembler
	lastProgress time.Time // last ring append of a throttled partial
}

func newServerLogTailer() *serverLogTailer {
	return &serverLogTailer{
		stopped: make(chan struct{}),
		done:    make(chan struct{}),
	}
}

// Stop signals the tailer to drain and exit; idempotent.
func (t *serverLogTailer) Stop() {
	t.stopOnce.Do(func() { close(t.stopped) })
}

// WaitDone blocks until the tailer goroutine has exited or the timeout
// elapses (bounded so a wedged tailer can never block server shutdown).
func (t *serverLogTailer) WaitDone(timeout time.Duration) {
	select {
	case <-t.done:
	case <-time.After(timeout):
	}
}

// startServerLogTailer opens path and spawns a tailer goroutine. fromStart
// reads from offset 0 (a freshly truncated child log); otherwise it seeks to
// EOF first (a re-adopted server's existing log must never replay stale
// content). Opening and positioning happen synchronously so the read position
// is fixed before the caller proceeds.
func startServerLogTailer(path string, fromStart bool) (*serverLogTailer, error) {
	f, err := openServerLogForTail(path)
	if err != nil {
		return nil, err
	}
	if !fromStart {
		if _, err := f.Seek(0, io.SeekEnd); err != nil {
			f.Close()
			return nil, fmt.Errorf("seek server log to EOF: %w", err)
		}
	}
	return startServerLogTailerFromReader(f), nil
}

// startServerLogTailerFromReader spawns the tailer goroutine consuming an
// already-positioned reader (production: the opened log file; tests: an
// io.Pipe). The reader is closed when the tailer exits if it is an io.Closer.
func startServerLogTailerFromReader(r io.Reader) *serverLogTailer {
	t := newServerLogTailer()
	go func() {
		// The tailer must never take the app down with it.
		defer func() {
			if p := recover(); p != nil {
				log.Println("[WARN] server log tailer panicked:", p)
			}
		}()
		defer close(t.done)
		if c, ok := r.(io.Closer); ok {
			defer c.Close()
		}
		t.follow(r)
	}()
	return t
}

// openServerLogForTail opens the log file for reading, retrying briefly while
// it does not exist (a spawn races the child's first write; callers that
// follow an adopted server's log have usually verified the file already).
func openServerLogForTail(path string) (*os.File, error) {
	deadline := serverLogNow().Add(5 * time.Second)
	for {
		f, err := os.Open(path)
		if err == nil {
			return f, nil
		}
		if !os.IsNotExist(err) || !serverLogNow().Before(deadline) {
			return nil, fmt.Errorf("open server log for tailing: %w", err)
		}
		time.Sleep(serverLogIdle)
	}
}

// follow consumes r until the tailer is stopped (or a fatal read error
// occurs). Reaching EOF while not stopped is not final for a growing log
// file — it just means "no new data yet", so the loop polls again after
// serverLogIdle. When stopped, the remainder is drained and the assembler
// flushed so the child's trailing partial/line still reaches the ring.
func (t *serverLogTailer) follow(r io.Reader) {
	buf := make([]byte, 65536)
	for {
		n, err := r.Read(buf)
		if n > 0 {
			t.consume(buf[:n])
			continue
		}
		if err != nil && !errors.Is(err, io.EOF) {
			log.Printf("[WARN] server log tailer read error: %v", err)
			break
		}
		select {
		case <-t.stopped:
			t.drain(r)
			return
		case <-time.After(serverLogIdle):
		}
	}
	// Fatal read error: keep what we have and flush the assembler.
	t.flushAssembler()
}

// drain reads the reader to its current end — the child has exited, so
// nothing more will be written — and flushes the assembler.
func (t *serverLogTailer) drain(r io.Reader) {
	buf := make([]byte, 65536)
	for {
		n, err := r.Read(buf)
		if n > 0 {
			t.consume(buf[:n])
		}
		if err != nil {
			break
		}
	}
	t.flushAssembler()
}

// flushAssembler emits the assembler's trailing pieces into the ring
// (forced, bypassing the partial throttle — the stream has ended).
func (t *serverLogTailer) flushAssembler() {
	for _, piece := range t.asm.Flush() {
		t.appendPiece(piece, true)
	}
}

// consume feeds one raw read (plain byte→string conversion, no decode: the
// assembler carries incomplete multibyte UTF-8 sequences across reads) and
// appends the completed pieces: lines immediately, partials throttled.
func (t *serverLogTailer) consume(data []byte) {
	for _, piece := range t.asm.Feed(string(data)) {
		t.appendPiece(piece, piece.Kind == pieceLine)
	}
}

// appendPiece appends one cleaned piece to the ring via addServerLog, so
// monitor.go's TPS parsing keeps receiving complete lines. Empty lines and
// empty redraw frames are dropped; "partial" redraw pieces are throttled to
// serverLogThrottle (unless forced) so a multi-minute model load shows live
// movement without flooding the ring.
func (t *serverLogTailer) appendPiece(piece logPiece, force bool) {
	clean := strings.TrimSpace(stripANSI(piece.Text))
	if clean == "" {
		return
	}
	if piece.Kind == piecePartial && !force {
		now := serverLogNow()
		if now.Sub(t.lastProgress) < serverLogThrottle {
			return
		}
		t.lastProgress = now
	}
	addServerLog(clean)
}

// effectiveHost derives the actual listen address from the access scope:
// lan → "0.0.0.0", any other value (including empty and invalid) →
// "127.0.0.1". Pure function shared by SaveServerConfig normalization,
// loadConfig compatibility, and buildServerCommand, keeping Host consistent
// everywhere.
func effectiveHost(mode string) string {
	if mode == accessLAN {
		return "0.0.0.0"
	}
	return "127.0.0.1"
}
