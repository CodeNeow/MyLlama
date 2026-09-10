package core

import (
	"context"
	"crypto/subtle"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"net"
	"net/http"
	"os"
	"strconv"
	"strings"
	"sync"
	"time"
)

// ─── Headless control plane ──────────────────────────────────────
//
// A tiny loopback-only HTTP daemon exposing service state / logs / stop to
// local tooling while the app runs in headless API-route mode. Modeled on
// FreeToken's ft daemon (see its design reference), with the same
// self-preservation rules scoped to our single-process reality:
//   - it must never prevent headless startup: a bind failure (port occupied)
//     logs a warning and the app continues degraded;
//   - it must never take the headless process down: every handler recovers
//     panics into a 500 JSON and nothing blocks long (the stop trigger is
//     async by design);
//   - destructive endpoints reject non-loopback callers with 403 even though
//     the listener is already loopback-bound (defense in depth), and honor an
//     optional shared token;
//   - browser-initiated cross-site requests are rejected at the transport
//     layer: every endpoint validates the Host header against the bound
//     loopback address (DNS-rebinding defense — a rebound attacker hostname
//     never matches), and /stop additionally validates Origin (CSRF defense —
//     a cross-site browser POST necessarily carries the attacker's origin).

// controlPlaneAddr is the fixed loopback listen address. Fixed rather than
// configurable keeps the surface tiny; a future config knob can re-point it
// without changing callers.
const controlPlaneAddr = "127.0.0.1:1900"

// controlPlaneTokenHeader carries the shared token required on gated
// endpoints when the token env var is set.
const controlPlaneTokenHeader = "X-Control-Token"

// controlPlaneTokenEnv names the environment variable holding the shared
// token. Declared as a var so tests can re-point it.
var controlPlaneTokenEnv = "LLAMA_DESKTOP_CONTROL_TOKEN"

// controlPlaneListen is the listener factory, a var so tests can inject a
// failing (port occupied) or ephemeral listener and exercise the
// degraded-start and serving paths without touching the fixed port.
var controlPlaneListen = func(network, addr string) (net.Listener, error) {
	return net.Listen(network, addr)
}

// controlPlaneServer holds the active control-plane HTTP server (nil when the
// control plane is not running); guarded by controlPlaneMu. Headless startup
// is single-instance gated, so at most one exists per process.
//
// controlPlanePort records the port of the listener actually bound at start
// (empty when not running). In production this is always the 1900 constant —
// a bind failure degrades to not serving at all, never to a replacement port —
// but tests inject ephemeral listeners, and the Host whitelist must match the
// port that is really serving.
var (
	controlPlaneMu     sync.Mutex
	controlPlaneServer *http.Server
	controlPlanePort   string
)

// startControlPlane binds the loopback listener and serves the control-plane
// mux in a background goroutine. It returns the server (for shutdown wiring)
// or an error when the port cannot be bound — the caller logs a warning and
// continues degraded, so a busy port never fails headless startup.
func startControlPlane() (*http.Server, error) {
	ln, err := controlPlaneListen("tcp", controlPlaneAddr)
	if err != nil {
		return nil, fmt.Errorf("bind %s: %w", controlPlaneAddr, err)
	}
	// Record the bound port before serving so the Host whitelist matches the
	// listener actually accepting connections (see controlPlanePort).
	if _, port, perr := net.SplitHostPort(ln.Addr().String()); perr == nil {
		controlPlaneMu.Lock()
		controlPlanePort = port
		controlPlaneMu.Unlock()
	}
	srv := &http.Server{
		Handler:           controlPlaneMux(),
		ReadHeaderTimeout: 5 * time.Second,
	}
	go func() {
		// Serve always returns a non-nil error (http.ErrServerClosed on
		// graceful shutdown); anything else is logged, never propagated —
		// a control-plane failure must not take the headless process down.
		if err := srv.Serve(ln); err != nil && !errors.Is(err, http.ErrServerClosed) {
			log.Printf("[WARN] control plane serve error: %v", err)
		}
	}()
	return srv, nil
}

// startControlPlaneHeadless starts the control plane during headless startup,
// degrading gracefully: a bind failure only logs a warning — headless must
// never fail to start because of the control plane. Called only from
// RunHeadless (core/headless.go).
func startControlPlaneHeadless() {
	srv, err := startControlPlane()
	if err != nil {
		log.Printf("[WARN] control plane unavailable: %v", err)
		return
	}
	controlPlaneMu.Lock()
	controlPlaneServer = srv
	controlPlaneMu.Unlock()
	log.Printf("[OK] Control plane listening on %s", controlPlaneAddr)
}

// stopControlPlane gracefully shuts down the control plane (idempotent, a
// no-op when it never started). Headless exit is process exit — main.go
// returns immediately after RunHeadless — so the OS would reclaim the
// listener anyway; the explicit drain keeps the exit path tidy and bounded.
func stopControlPlane() {
	controlPlaneMu.Lock()
	srv := controlPlaneServer
	controlPlaneServer = nil
	// Clear the bound port too: after a stop, direct-mux users (tests) fall
	// back to the constant-address whitelist instead of a stale test port.
	controlPlanePort = ""
	controlPlaneMu.Unlock()
	if srv == nil {
		return
	}
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	if err := srv.Shutdown(ctx); err != nil {
		// Drain timeout: drop the remaining connections instead of hanging.
		srv.Close()
	}
}

// controlPlaneMux builds the route table. Every handler is wrapped with
// controlGuard (panic recovery → 500 JSON); unknown paths get a JSON 404 via
// the catch-all.
func controlPlaneMux() *http.ServeMux {
	mux := http.NewServeMux()
	mux.HandleFunc("/health", controlGuard(handleControlHealth))
	mux.HandleFunc("/status", controlGuard(handleControlStatus))
	mux.HandleFunc("/logs", controlGuard(handleControlLogs))
	mux.HandleFunc("/stop", controlGuard(handleControlStop))
	mux.HandleFunc("/", controlGuard(handleControlNotFound))
	return mux
}

// controlGuard wraps a handler with panic recovery: a panicking handler
// becomes a 500 JSON response instead of crashing the headless process (the
// FreeToken crash-proof rule). If the handler already wrote a response before
// panicking, the recovery write is a harmless no-op (headers already sent).
// It also rejects requests whose Host header does not name this control plane
// (see isAllowedControlHost) — transport-layer addressing validation applied
// to every endpoint, /health included.
func controlGuard(next http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		defer func() {
			if p := recover(); p != nil {
				log.Printf("[ERROR] control plane panic on %s %s: %v", r.Method, r.URL.Path, p)
				writeControlJSON(w, http.StatusInternalServerError, map[string]string{"error": "internal error"})
			}
		}()
		if !isAllowedControlHost(r.Host) {
			writeControlJSON(w, http.StatusMisdirectedRequest, map[string]string{"error": "host not allowed"})
			return
		}
		next(w, r)
	}
}

// controlPlaneHosts returns the Host header values accepted on requests to
// the control plane: the three loopback spellings of the bound port. The port
// comes from the listener recorded at start (in production always the 1900
// constant, since a bind failure degrades to not serving — never to a new
// port); while no server runs, the constant's port is used so the direct-mux
// tests see the production whitelist.
func controlPlaneHosts() []string {
	controlPlaneMu.Lock()
	port := controlPlanePort
	controlPlaneMu.Unlock()
	if port == "" {
		if _, p, err := net.SplitHostPort(controlPlaneAddr); err == nil {
			port = p
		}
	}
	return []string{
		"127.0.0.1:" + port,
		"localhost:" + port,
		"[::1]:" + port,
	}
}

// isAllowedControlHost reports whether the request's Host header names this
// control plane. This is the DNS-rebinding defense: a rebound attacker
// hostname resolves to 127.0.0.1 but its requests still carry the attacker's
// Host, which never matches the whitelist. It is transport-layer addressing
// validation, not authentication — /health stays token/loopback-ungated (see
// handleControlHealth); it must merely be addressed to this service. Mismatch
// answers 421 Misdirected Request: the request was aimed at a host this
// listener does not serve (401/403 remain reserved for authorization).
func isAllowedControlHost(host string) bool {
	for _, h := range controlPlaneHosts() {
		if strings.EqualFold(host, h) {
			return true
		}
	}
	return false
}

// isAllowedControlOrigin validates the Origin header of a state-changing
// request (/stop) — the CSRF defense. Behavior:
//   - no Origin → allowed: non-browser callers (curl, scripts, local tooling —
//     the control plane's actual consumers) never send Origin; the loopback
//     and token gates still apply. Adding Origin here does not break them.
//   - "null" → rejected: browsers send it for sandboxed iframes and certain
//     redirect/privacy contexts, all of them attacker-shapeable, and no
//     legitimate producer exists (the control plane serves no web pages, so
//     nothing is ever same-origin with it). Fail closed.
//   - anything else must be exactly the http loopback origin of the bound
//     port. A browser cross-site POST (even no-cors) carries the attacker's
//     origin and is rejected here.
func isAllowedControlOrigin(origin string) bool {
	if origin == "" {
		return true
	}
	if strings.EqualFold(origin, "null") {
		return false
	}
	for _, h := range controlPlaneHosts() {
		if strings.EqualFold(origin, "http://"+h) {
			return true
		}
	}
	return false
}

// writeControlJSON writes one JSON response with the given status.
func writeControlJSON(w http.ResponseWriter, status int, v interface{}) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}

// checkControlToken validates the X-Control-Token header against the
// LLAMA_DESKTOP_CONTROL_TOKEN env var. When the env var is unset the check is
// disabled and the loopback bind is the only gate (documented on the gated
// endpoints). Returns the HTTP status to reject with: 0 = pass, 401 = header
// absent, 403 = header present but wrong (compared in constant time).
func checkControlToken(r *http.Request) int {
	want := os.Getenv(controlPlaneTokenEnv)
	if want == "" {
		return 0
	}
	got := r.Header.Get(controlPlaneTokenHeader)
	if got == "" {
		return http.StatusUnauthorized
	}
	if subtle.ConstantTimeCompare([]byte(got), []byte(want)) != 1 {
		return http.StatusForbidden
	}
	return 0
}

// isLoopbackRequest reports whether the request's remote address is a
// loopback IP (127.0.0.0/8 or ::1). The listener is already loopback-bound;
// this is defense in depth for destructive endpoints, so a misconfigured
// reverse proxy cannot gain stop power.
func isLoopbackRequest(r *http.Request) bool {
	host, _, err := net.SplitHostPort(r.RemoteAddr)
	if err != nil {
		return false
	}
	ip := net.ParseIP(host)
	return ip != nil && ip.IsLoopback()
}

// handleControlHealth answers unconditionally — never gated by the token or
// the loopback guard (probers need a liveness signal that cannot 401/403).
// The controlGuard's Host whitelist still applies above this handler: it is
// transport-layer addressing validation, not an authorization gate, so a
// properly-addressed probe still gets its ungated 200.
func handleControlHealth(w http.ResponseWriter, _ *http.Request) {
	writeControlJSON(w, http.StatusOK, map[string]interface{}{"status": "ok", "headless": true})
}

// controlStatus is the GET /status response body (camelCase JSON).
type controlStatus struct {
	Running bool    `json:"running"`
	Port    int     `json:"port"`
	PID     int     `json:"pid"`
	Adopted bool    `json:"adopted"`
	UptimeS float64 `json:"uptimeS"`
	Version string  `json:"version"`
}

// handleControlStatus answers with the llama-server lifecycle snapshot (the
// same state the frontend reads through GetServerStatus / GetMonitorStatus).
// Gated by the token check when LLAMA_DESKTOP_CONTROL_TOKEN is set (FreeToken
// rule: everything except /health is gated).
func handleControlStatus(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		writeControlJSON(w, http.StatusMethodNotAllowed, map[string]string{"error": "method not allowed"})
		return
	}
	if code := checkControlToken(r); code != 0 {
		writeControlJSON(w, code, map[string]string{"error": "unauthorized"})
		return
	}
	// serverMu guards the whole lifecycle snapshot; no serverLogsMu nesting.
	serverMu.Lock()
	running := serverRunning
	port := serverPort
	cmd := serverCmd
	adopted := adoptedPid
	start := serverStartTime
	serverMu.Unlock()
	pid := 0
	if cmd != nil && cmd.Process != nil {
		pid = cmd.Process.Pid
	} else if adopted > 0 {
		pid = adopted
	}
	var uptime float64
	if running && !start.IsZero() {
		uptime = time.Since(start).Seconds()
	}
	writeControlJSON(w, http.StatusOK, controlStatus{
		Running: running,
		Port:    port,
		PID:     pid,
		Adopted: adopted > 0,
		UptimeS: uptime,
		Version: currentVersion,
	})
}

// handleControlLogs proxies the incremental server-log cursor
// (GetServerLogsSince): entries with seq >= since plus the next cursor for
// the following call. A missing since means 0 (everything retained); an
// unparsable one is rejected instead of guessed. Gated by the token check
// when set.
func handleControlLogs(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		writeControlJSON(w, http.StatusMethodNotAllowed, map[string]string{"error": "method not allowed"})
		return
	}
	if code := checkControlToken(r); code != 0 {
		writeControlJSON(w, code, map[string]string{"error": "unauthorized"})
		return
	}
	var since int64
	if raw := r.URL.Query().Get("since"); raw != "" {
		v, err := strconv.ParseInt(raw, 10, 64)
		if err != nil {
			writeControlJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid since"})
			return
		}
		since = v
	}
	entries, next := serverLogsSince(since)
	out := make([]ServerLogEntry, len(entries))
	for i, e := range entries {
		out[i] = ServerLogEntry{Seq: e.seq, Text: e.text}
	}
	writeControlJSON(w, http.StatusOK, ServerLogsPage{Entries: out, Next: next})
}

// handleControlStop triggers the graceful llama-server stop
// (stopServerInternal) in a goroutine and answers immediately — the handler
// must never block on the stop's process-kill path.
//
// Guards, in order: POST-only (405); non-loopback remote address rejected
// with 403 even though the listener is already loopback-bound (defense in
// depth, the FreeToken rule for destructive endpoints); Origin rejected with
// 403 when present and not this control plane's own loopback origin (a
// browser-initiated cross-site POST reaches the loopback listener with the
// attacker's origin — see isAllowedControlOrigin); X-Control-Token required to
// match LLAMA_DESKTOP_CONTROL_TOKEN when that env var is set (401 missing /
// 403 wrong). Unset env var = token check disabled: the loopback bind plus the
// Host/Origin gates are then the only gates.
func handleControlStop(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		writeControlJSON(w, http.StatusMethodNotAllowed, map[string]string{"error": "method not allowed"})
		return
	}
	if !isLoopbackRequest(r) {
		writeControlJSON(w, http.StatusForbidden, map[string]string{"error": "loopback only"})
		return
	}
	if !isAllowedControlOrigin(r.Header.Get("Origin")) {
		writeControlJSON(w, http.StatusForbidden, map[string]string{"error": "origin not allowed"})
		return
	}
	if code := checkControlToken(r); code != 0 {
		writeControlJSON(w, code, map[string]string{"error": "unauthorized"})
		return
	}
	go func() {
		// The stop path must never take the process down either.
		defer func() {
			if p := recover(); p != nil {
				log.Printf("[ERROR] control plane stop panic: %v", p)
			}
		}()
		if err := stopServerInternal(); err != nil {
			log.Printf("[WARN] control plane stop llama-server: %v", err)
		}
	}()
	writeControlJSON(w, http.StatusOK, map[string]interface{}{"stopping": true})
}

// handleControlNotFound answers unknown paths with a JSON 404 (the catch-all
// behind controlPlaneMux).
func handleControlNotFound(w http.ResponseWriter, _ *http.Request) {
	writeControlJSON(w, http.StatusNotFound, map[string]string{"error": "not found"})
}
