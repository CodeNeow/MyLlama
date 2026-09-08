package core

import (
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"os"
	"time"
)

// ─── Server handover file ─────────────────────────────────────────
//
// When switching between GUI and headless (API-route) mode the app relaunches
// itself and exits. To keep llama-server running uninterrupted across the
// switch, the exiting process writes a "handover record" (pid + port) next to
// the config file; the new process probes the record and adopts the still
// running llama-server instead of starting a new one.

// handoverFile is the handover-record path override: the bare default means
// "resolve via handoverFilePath" (cwd-relative on Windows, under the app-data
// base on other platforms, see paths.go); tests assign an explicit path to
// pin the location.
var handoverFile = handoverFileName

// handoverFilePath resolves the active handover-record path: an explicit
// handoverFile override wins, otherwise the per-OS default applies.
func handoverFilePath() string {
	if handoverFile != handoverFileName {
		return handoverFile
	}
	return resolveStateFile(handoverFileName)
}

// handoverLegacyPath resolves the pre-rebrand handover-record name for the
// one-version read transition, or "" when an explicit handoverFile override
// is active (tests) — the record is transient state, so it is read from the
// legacy name when the new one is absent but never renamed (see readHandover).
func handoverLegacyPath() string {
	if handoverFile != handoverFileName {
		return ""
	}
	return resolveStateFile(legacyHandoverFileName)
}

// handoverRecord is the JSON payload of the handover file: the llama-server
// child pid, the port it listens on, when the record was written, the server
// process's real creation time, and the absolute path of the server log file.
// LogPath and ServerStartedAt are omitempty and may be missing entirely in
// records written by older versions; readHandover must accept such records
// (an empty LogPath adopts without log tailing, an empty ServerStartedAt
// skips the pid-reuse start-time check).
type handoverRecord struct {
	Pid       int    `json:"pid"`
	Port      int    `json:"port"`
	StartedAt string `json:"startedAt"`
	// ServerStartedAt is the llama-server process's REAL creation time
	// (RFC3339, from serverTrueStart), recorded so the successor can detect
	// pid reuse (Windows recycles pids quickly; a live pid plus an answering
	// port is not proof the recorded server still owns them).
	ServerStartedAt string `json:"serverStartedAt,omitempty"`
	LogPath         string `json:"logPath,omitempty"`
}

// writeHandover persists the handover record (pid/port, RFC3339 timestamps,
// absolute server log path — the successor must not depend on our cwd).
// serverStartedAt carries the running server's real creation time
// (serverTrueStart) for the successor's pid-reuse check; when unknown (zero,
// e.g. no server state was tracked) the field is omitted and the successor's
// check fails open.
func writeHandover(pid, port int) error {
	serverMu.Lock()
	trueStart := serverTrueStart
	serverMu.Unlock()
	rec := handoverRecord{
		Pid:       pid,
		Port:      port,
		StartedAt: time.Now().Format(time.RFC3339),
		LogPath:   absServerLogPath(),
	}
	if !trueStart.IsZero() {
		rec.ServerStartedAt = trueStart.Format(time.RFC3339)
	}
	data, err := json.MarshalIndent(rec, "", "  ")
	if err != nil {
		return fmt.Errorf("marshal handover record: %w", err)
	}
	if err := atomicWriteFile(handoverFilePath(), data, 0644); err != nil {
		return fmt.Errorf("write handover file: %w", err)
	}
	return nil
}

// readHandover loads the handover record. A missing file and a corrupt file
// are both errors; callers distinguish missing via errors.Is(err, fs.ErrNotExist)
// and treat anything else as a stale record (delete + start fresh).
// One-version transition: when the new-name record is absent, the pre-rebrand
// llama-desktop-server-handover.json is read in place (no rename — the record
// is transient; the next writeHandover writes the new name and removeHandover
// clears both).
func readHandover() (*handoverRecord, error) {
	data, err := os.ReadFile(handoverFilePath())
	if err != nil && os.IsNotExist(err) {
		if legacy := handoverLegacyPath(); legacy != "" {
			if legacyData, legacyErr := os.ReadFile(legacy); legacyErr == nil {
				data, err = legacyData, nil
			}
		}
	}
	if err != nil {
		return nil, err
	}
	var rec handoverRecord
	if err := json.Unmarshal(data, &rec); err != nil {
		return nil, fmt.Errorf("parse handover file: %w", err)
	}
	return &rec, nil
}

// removeHandover deletes the handover record; a missing file is not an error.
// Both the active name and the pre-rebrand legacy name are removed, so a
// record consumed from the legacy path cannot resurface after the successor
// writes and later deletes the new-name record.
func removeHandover() error {
	if err := os.Remove(handoverFilePath()); err != nil && !os.IsNotExist(err) {
		return fmt.Errorf("remove handover file: %w", err)
	}
	if legacy := handoverLegacyPath(); legacy != "" {
		if err := os.Remove(legacy); err != nil && !os.IsNotExist(err) {
			return fmt.Errorf("remove legacy handover file: %w", err)
		}
	}
	return nil
}

// probeHandoverHealth is the injection point for the llama-server liveness
// probe: the default implementation GETs /health on 127.0.0.1:port (base URL
// shared with the router wrapper) and counts any HTTP response — status code
// irrelevant — as healthy; a connection failure means no server on that port.
// Tests replace this variable instead of opening sockets.
var probeHandoverHealth = func(port int) bool {
	client := &http.Client{Timeout: 2 * time.Second}
	resp, err := client.Get(routerBaseURL(port) + "/health")
	if err != nil {
		return false
	}
	defer resp.Body.Close()
	return true
}

// handoverPidAlive is the injection point for the pid-liveness check
// (platform-specific processAlive implementation; tests fake this variable).
var handoverPidAlive = func(pid int) bool {
	return processAlive(pid)
}

// handoverProcStartTime is the injection point for the process creation-time
// query (platform-specific processStartTime implementation; tests fake this
// variable, same style as handoverPidAlive).
var handoverProcStartTime = processStartTime

// handoverStartTolerance is how far the queried process creation time may
// differ from the record's serverStartedAt before the record is treated as
// stale (pid recycled). The record is written within milliseconds of process
// creation; 5 s absorbs clock-read and scheduling latency. Package-level var
// (same injection-point style as cmdTimeout) so tests can exercise the
// boundary.
var handoverStartTolerance = 5 * time.Second

// handoverPlan describes what a starting process (GUI or headless) should do
// with a possibly handed-over llama-server.
type handoverPlan struct {
	// Adopt reports the handed-over llama-server is alive and should be
	// adopted (PID/Port carry its identity) instead of starting a new one.
	Adopt bool
	PID   int
	Port  int
	// LogPath carries the record's server log file path ("" in legacy records
	// without logPath → adopt without log tailing).
	LogPath string
	// Start carries the record's start-check outcome (see handoverStartCheck):
	// Start.ServerStartedAt is chained into serverTrueStart on adoption, and
	// Verified/SkipReason drive the truthful adoption log line.
	Start handoverStartCheck
	// RemoveFile reports the handover record is stale (server dead, pid
	// recycled, or file corrupt) and must be deleted before a fresh server is
	// started.
	RemoveFile bool
}

// handoverStartCheck is the outcome of the pid-reuse start-time verification
// for one handover record. Verified=true means the check ran and the queried
// process creation time matched the record's serverStartedAt within
// handoverStartTolerance. Verified=false with a non-empty SkipReason marks a
// fail-open skip (legacy record, unparseable time, query unavailable — the
// port health probe still guards). Verified=false with an empty SkipReason is
// a genuine mismatch: the record is treated as stale and never adopted.
// ServerStartedAt is the record's raw field, chained into serverTrueStart on
// adoption (see adoptHandover).
type handoverStartCheck struct {
	ServerStartedAt string
	Verified        bool
	SkipReason      string
}

// decideHandoverAction is the pure decision core of the handover takeover:
//   - no record file → start fresh (nothing to delete);
//   - record present + healthy + start time verified or skipped (fail-open)
//     → adopt (pid/port/log paths, start-check outcome);
//   - record present + unhealthy, start-time mismatch, or unparsable
//     (rec == nil) → delete the record and start fresh.
//
// start is the pid-reuse verdict from verifyHandoverStart: a genuine mismatch
// is Verified=false with an empty SkipReason; every skipped (fail-open) check
// has a non-empty SkipReason and still adopts. The outcome is copied onto the
// plan so the adoption log line can tell "verified" from "skipped".
func decideHandoverAction(fileExists bool, rec *handoverRecord, healthy bool, start handoverStartCheck) handoverPlan {
	switch {
	case !fileExists:
		return handoverPlan{}
	case rec == nil || !healthy || (!start.Verified && start.SkipReason == ""):
		return handoverPlan{RemoveFile: true}
	default:
		return handoverPlan{
			Adopt:   true,
			PID:     rec.Pid,
			Port:    rec.Port,
			LogPath: rec.LogPath,
			Start:   start,
		}
	}
}

// evaluateHandover reads the handover file, probes the recorded server and
// returns the plan for the starting process (see decideHandoverAction).
//
// Besides the port health probe and pid liveness, a third probe defends
// against pid reuse: when the record carries a parseable serverStartedAt AND
// the OS reports the pid's creation time, the two must agree within
// handoverStartTolerance or the record is stale (the pid was recycled by an
// unrelated process). Every check-skip case fails open (adopt as today; the
// port health probe still guards — see verifyHandoverStart):
//   - legacy record without serverStartedAt (older app versions);
//   - stored time unparseable → [WARN];
//   - creation-time query unavailable (unsupported platform or OS failure)
//     → [WARN].
//
// The start-time probe only runs when the health probe passed — a dead server
// is stale regardless, so the extra OS query and its warnings would be noise.
func evaluateHandover() handoverPlan {
	rec, err := readHandover()
	if err != nil {
		if os.IsNotExist(err) {
			return decideHandoverAction(false, nil, false, handoverStartCheck{})
		}
		// Corrupt file: exists but unreadable/unparsable → stale record.
		return decideHandoverAction(true, nil, false, handoverStartCheck{})
	}
	healthy := probeHandoverHealth(rec.Port) && handoverPidAlive(rec.Pid)
	if !healthy {
		// A dead server is stale regardless; skip the start-time check —
		// its OS query and warnings would be pure noise.
		return decideHandoverAction(true, rec, false, handoverStartCheck{})
	}
	return decideHandoverAction(true, rec, true, verifyHandoverStart(rec))
}

// verifyHandoverStart runs the pid-reuse start-time check for a record (see
// handoverStartCheck for the outcome vocabulary). Every skip case fails open —
// the port health probe still guards adoption:
//   - legacy record without serverStartedAt (older app versions) → skip;
//   - stored time unparseable → [WARN] + skip;
//   - creation-time query unavailable (unsupported platform or OS failure)
//     → [WARN] + skip;
//   - otherwise the queried time must match the recorded one within
//     handoverStartTolerance, or the record is stale (genuine mismatch).
func verifyHandoverStart(rec *handoverRecord) handoverStartCheck {
	out := handoverStartCheck{}
	if rec != nil {
		out.ServerStartedAt = rec.ServerStartedAt
	}
	if rec == nil || rec.ServerStartedAt == "" {
		// Legacy record without the field: nothing to verify, adopt as today.
		out.SkipReason = "legacy record without serverStartedAt"
		return out
	}
	recorded, err := time.Parse(time.RFC3339, rec.ServerStartedAt)
	if err != nil {
		log.Printf("[WARN] handover record serverStartedAt unparseable (%q): %v — skipping start-time check", rec.ServerStartedAt, err)
		out.SkipReason = "serverStartedAt unparseable"
		return out
	}
	queried, ok := handoverProcStartTime(rec.Pid)
	if !ok {
		log.Printf("[WARN] process start time unavailable for pid %d — skipping start-time check", rec.Pid)
		out.SkipReason = "process start time unavailable"
		return out
	}
	if d := queried.Sub(recorded); d > handoverStartTolerance || d < -handoverStartTolerance {
		log.Printf("[INFO] handover record pid %d start time mismatch (recorded %s, queried %s) — treating as stale",
			rec.Pid, rec.ServerStartedAt, queried.Format(time.RFC3339))
		return handoverStartCheck{}
	}
	out.Verified = true
	return out
}

// adoptHandover marks a handed-over llama-server as the running server:
// serverRunning=true / serverPort set / adoptedPid recorded / serverCmd nil
// (the child belongs to the previous, exited process — there is no handle to
// Signal/Wait, stopServerInternal kills it by pid instead). logPath is the
// server log file from the handover record: when non-empty and present, it
// becomes the log source and a tailer reading from EOF feeds the ring — the
// adopted child keeps writing to the same file, restoring log capture that
// pipes could not provide. An empty logPath (legacy record) adopts without
// tailing.
//
// start is the record's start-check outcome (see handoverStartCheck): its
// ServerStartedAt is chained into serverTrueStart so a subsequent handover
// re-records the server's ORIGINAL creation time instead of the adoption
// moment. A legacy record without the field (or an unparseable one) falls
// back to adoption time — the same value serverStartTime gets. Known edge,
// self-healing in the safe direction: the adoption time chained that way
// will legitimately mismatch the server's real creation time at the NEXT
// handover, so that successor discards the record and starts fresh (one
// extra model reload, never a wrong-process kill) — a lost chain cannot
// silently persist a wrong identity. Verified=false must carry SkipReason;
// the single [OK] log line stays truthful: "(start time verified)" only when
// the check actually ran and matched, otherwise the skip reason is named.
func adoptHandover(pid, port int, logPath string, start handoverStartCheck) {
	trueStart := time.Now()
	if start.ServerStartedAt != "" {
		if parsed, err := time.Parse(time.RFC3339, start.ServerStartedAt); err == nil {
			trueStart = parsed
		}
	}
	serverMu.Lock()
	serverRunning = true
	serverPort = port
	adoptedPid = pid
	serverCmd = nil
	serverStartTime = time.Now()
	serverTrueStart = trueStart
	serverMu.Unlock()
	if start.Verified {
		log.Printf("[OK] Adopted llama-server pid=%d port=%d (start time verified)", pid, port)
	} else {
		log.Printf("[OK] Adopted llama-server pid=%d port=%d (start time check skipped: %s)", pid, port, start.SkipReason)
	}
	adoptServerLogTail(logPath)
}

// adoptServerLogTail points the log capture at an adopted server's log file
// and starts a tailer reading from EOF (never replaying stale content). A
// missing or empty path is tolerated (legacy records, already-deleted files)
// — adoption itself must not fail over log capture.
func adoptServerLogTail(logPath string) {
	if logPath == "" {
		return
	}
	if _, err := os.Stat(logPath); err != nil {
		log.Printf("[WARN] adopted server log file %s not readable, log tailing skipped: %v", logPath, err)
		return
	}
	t, err := startServerLogTailer(logPath, false)
	if err != nil {
		log.Printf("[WARN] adopted server log tailer failed to start: %v", err)
		return
	}
	serverMu.Lock()
	serverLogFile = logPath
	// Defensive: a previous tailer (if any) must not double-append into the
	// ring alongside the new one.
	old := serverLogTail
	serverLogTail = t
	serverMu.Unlock()
	if old != nil {
		old.Stop()
	}
	log.Printf("[OK] Tailing adopted server log: %s", logPath)
}
