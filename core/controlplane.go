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

// controlPlaneAddr is the loopback listen address used when the control plane
// is NOT LAN-exposed. Kept as a variable (historical constant) so tests can
// re-point it; production never changes it.
var controlPlaneAddr = "127.0.0.1:1900"

// controlPlaneLANAddr is the wildcard listen address used when RemoteStart is
// enabled: the control plane must be reachable from the LAN peer (the phone
// pairing), not just from this machine. Port stays 1900 — the phone derives
// the control-plane URL from the pairing host + the fixed port.
const controlPlaneLANAddr = ":1900"

// controlPlaneAppOrigin is the phone app's own WebView origin. On a LAN-exposed
// control plane, state-changing requests from the phone's MyLlama app carry
// this Origin (browsers cannot forge the Origin header); it is appended to the
// loopback-only whitelist in LAN mode. The mandatory token is the second gate.
const controlPlaneAppOrigin = "https://wails.localhost"

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
// control plane is not running); guarded by controlPlaneMu. Startup is
// single-instance per process, so at most one exists.
//
// controlPlanePort records the port of the listener actually bound at start
// (empty when not running). In production this is always the 1900 constant —
// a bind failure degrades to not serving at all, never to a replacement port —
// but tests inject ephemeral listeners, and the Host whitelist must match the
// port that is really serving.
//
// controlPlaneLAN records whether the active listener is LAN-exposed
// (RemoteStart on, wildcard bind) or loopback-only; handlers branch their
// auth/origin gates on it (see checkControlGatedToken / isAllowedControlHost).
var (
	controlPlaneServer *http.Server
	controlPlanePort   string
	controlPlaneLAN    bool

	controlPlaneMu sync.Mutex
)

// controlPlaneLANMode reports whether the running control plane (if any) is
// LAN-exposed. Read by the guards on every request.
func controlPlaneLANMode() bool {
	controlPlaneMu.Lock()
	defer controlPlaneMu.Unlock()
	return controlPlaneLAN
}

// startControlPlane binds the control-plane listener (loopback by default,
// wildcard when lanExposed) and serves the control-plane mux in a background
// goroutine. It returns the server (for shutdown wiring) or an error when the
// port cannot be bound — the caller logs a warning and continues degraded, so
// a busy port never fails startup.
func startControlPlane(lanExposed bool) (*http.Server, error) {
	addr := controlPlaneAddr
	if lanExposed {
		addr = controlPlaneLANAddr
	}
	ln, err := controlPlaneListen("tcp", addr)
	if err != nil {
		return nil, fmt.Errorf("bind %s: %w", addr, err)
	}
	// Record the bound port and exposure mode before serving so the Host
	// whitelist matches the listener actually accepting connections (see
	// controlPlanePort) and the per-request guards branch on the right mode.
	if _, port, perr := net.SplitHostPort(ln.Addr().String()); perr == nil {
		controlPlaneMu.Lock()
		controlPlanePort = port
		controlPlaneLAN = lanExposed
		controlPlaneMu.Unlock()
	}
	srv := &http.Server{
		Handler:           controlPlaneMux(),
		ReadHeaderTimeout: 5 * time.Second,
	}
	go func() {
		// Serve always returns a non-nil error (http.ErrServerClosed on
		// graceful shutdown); anything else is logged, never propagated —
		// a control-plane failure must not take the process down.
		if err := srv.Serve(ln); err != nil && !errors.Is(err, http.ErrServerClosed) {
			log.Printf("[WARN] control plane serve error: %v", err)
		}
	}()
	return srv, nil
}

// launchControlPlane starts the control plane and records the server handle,
// degrading gracefully: a bind failure only logs a warning. Shared by the
// headless startup (always runs the plane, LAN-exposed per RemoteStart) and
// the GUI startup (only when RemoteStart is on, desktop platforms only).
func launchControlPlane(lanExposed bool) {
	srv, err := startControlPlane(lanExposed)
	if err != nil {
		log.Printf("[WARN] control plane unavailable: %v", err)
		return
	}
	controlPlaneMu.Lock()
	controlPlaneServer = srv
	controlPlaneMu.Unlock()
	if lanExposed {
		log.Printf("[OK] Control plane listening on %s (LAN-exposed, remote start enabled)", controlPlaneLANAddr)
		return
	}
	log.Printf("[OK] Control plane listening on %s", controlPlaneAddr)
}

// startControlPlaneHeadless starts the control plane during headless startup.
// The plane always runs in headless mode (local tooling depends on it); it is
// LAN-exposed only when the persisted RemoteStart flag is on. Called only from
// RunHeadless (core/headless.go).
func startControlPlaneHeadless() {
	serverConfigMu.Lock()
	lanExposed := cachedServerConfig.RemoteStart
	serverConfigMu.Unlock()
	launchControlPlane(lanExposed)
}

// stopControlPlane gracefully shuts down the control plane (idempotent, a
// no-op when it never started). Headless exit is process exit — main.go
// returns immediately after RunHeadless — so the OS would reclaim the
// listener anyway; the explicit drain keeps the exit path tidy and bounded.
func stopControlPlane() {
	controlPlaneMu.Lock()
	srv := controlPlaneServer
	controlPlaneServer = nil
	// Clear the bound port and the exposure mode too: after a stop, direct-mux
	// users (tests) fall back to the constant-address loopback whitelist and
	// the loopback auth gates instead of stale test state.
	controlPlanePort = ""
	controlPlaneLAN = false
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
	mux.HandleFunc("/start", controlGuard(handleControlStart))
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

// controlPlaneBoundPort returns the port of the listener actually bound at
// start (the 1900 fallback when no server runs), for the LAN-mode Host check.
func controlPlaneBoundPort() string {
	controlPlaneMu.Lock()
	port := controlPlanePort
	controlPlaneMu.Unlock()
	if port == "" {
		if _, p, err := net.SplitHostPort(controlPlaneAddr); err == nil {
			port = p
		}
	}
	return port
}

// isAllowedControlHost reports whether the request's Host header names this
// control plane. This is the DNS-rebinding defense: a rebound attacker
// hostname resolves to 127.0.0.1 but its requests still carry the attacker's
// Host, which never matches the whitelist. It is transport-layer addressing
// validation, not authentication — /health stays token/loopback-ungated (see
// handleControlHealth); it must merely be addressed to this service. Mismatch
// answers 421 Misdirected Request: the request was aimed at a host this
// listener does not serve (401/403 remain reserved for authorization).
//
// LAN-exposed mode relaxes the check to "any hostname on the bound port": a
// phone addresses the plane by the pairing host (192.168.x.x:1900) which
// cannot be enumerated here, and the rebinding attack shape is moot on an
// intentionally LAN-exposed, token-mandatory plane (every gated endpoint
// still requires a valid token; /health is a liveness probe by design).
func isAllowedControlHost(host string) bool {
	if controlPlaneLANMode() {
		_, port, err := net.SplitHostPort(host)
		if err != nil || port == "" {
			return false
		}
		return port == controlPlaneBoundPort()
	}
	for _, h := range controlPlaneHosts() {
		if strings.EqualFold(host, h) {
			return true
		}
	}
	return false
}

// isAllowedControlOrigin validates the Origin header of a state-changing
// request (/stop, /start) — the CSRF defense. Behavior:
//   - no Origin → allowed: non-browser callers (curl, scripts, local tooling —
//     the control plane's actual consumers) never send Origin; the bind and
//     token gates still apply. Adding Origin here does not break them.
//   - "null" → rejected: browsers send it for sandboxed iframes and certain
//     redirect/privacy contexts, all of them attacker-shapeable, and no
//     legitimate producer exists (the control plane serves no web pages, so
//     nothing is ever same-origin with it). Fail closed.
//   - anything else must be exactly the http loopback origin of the bound
//     port — or, on a LAN-exposed plane, the phone app's own WebView origin
//     (controlPlaneAppOrigin): browsers cannot forge Origin, and the token
//     gate remains. A browser cross-site POST (even no-cors) carries the
//     attacker's origin and is rejected here.
func isAllowedControlOrigin(origin string) bool {
	return isAllowedControlOriginFor(controlPlaneLANMode(), origin)
}

// isAllowedControlOriginFor is the mode-explicit form; loopback callers (/stop)
// keep the loopback-only whitelist regardless of the exposure mode.
func isAllowedControlOriginFor(lanExposed bool, origin string) bool {
	if origin == "" {
		return true
	}
	if strings.EqualFold(origin, "null") {
		return false
	}
	if lanExposed && strings.EqualFold(origin, controlPlaneAppOrigin) {
		return true
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

// checkControlGatedToken is the mode-aware token gate for the LAN-exposed
// plane's gated endpoints (/status, /logs, /start). Loopback mode keeps the
// historical checkControlToken semantics. LAN mode REQUIRES a token: the
// request must present either the env token or the persisted ServerConfig
// APIKey (the phone pairing already stores it — zero new secret provisioning),
// each compared in constant time. Distinct failures: no token source
// configured at all → 403 (the LAN feature is unusable without a key —
// "remote start requires the API key"); header absent → 401; header present
// but matching neither → 401 (authentication failure).
func checkControlGatedToken(lanExposed bool, r *http.Request) int {
	if !lanExposed {
		return checkControlToken(r)
	}
	env := os.Getenv(controlPlaneTokenEnv)
	serverConfigMu.Lock()
	apiKey := cachedServerConfig.APIKey
	serverConfigMu.Unlock()
	if env == "" && apiKey == "" {
		// A wildcard-bound plane with zero token sources would serve gated
		// endpoints to the whole LAN: refuse instead.
		return http.StatusForbidden
	}
	got := r.Header.Get(controlPlaneTokenHeader)
	if got == "" {
		return http.StatusUnauthorized
	}
	if env != "" && subtle.ConstantTimeCompare([]byte(got), []byte(env)) == 1 {
		return 0
	}
	if apiKey != "" && subtle.ConstantTimeCompare([]byte(got), []byte(apiKey)) == 1 {
		return 0
	}
	return http.StatusUnauthorized
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
// Gated by the token check: env token when set in loopback mode; the dual
// token (env or APIKey, mandatory) on a LAN-exposed plane.
func handleControlStatus(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		writeControlJSON(w, http.StatusMethodNotAllowed, map[string]string{"error": "method not allowed"})
		return
	}
	if code := checkControlGatedToken(controlPlaneLANMode(), r); code != 0 {
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
// (env token when set in loopback mode; mandatory dual token on a
// LAN-exposed plane).
func handleControlLogs(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		writeControlJSON(w, http.StatusMethodNotAllowed, map[string]string{"error": "method not allowed"})
		return
	}
	if code := checkControlGatedToken(controlPlaneLANMode(), r); code != 0 {
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

// controlPlaneStartServer is the seam for the /start handler's service
// bring-up (tests inject stub results so no test ever spawns a real
// llama-server); production wires startServerInternal — the same entry the
// GUI "start service" button drives.
var controlPlaneStartServer = startServerInternal

// handleControlStart serves POST /start: a phone pairing whose /models probe
// failed can start this machine's llama-server remotely (the control-plane
// equivalent of pressing the GUI "start service" button). Semantics:
//   - 200 {"started":true}          — startServerInternal returned nil;
//   - 409 {"started":false,...}     — llama-server already running ("already running");
//   - 500 {"started":false,...}     — the start attempt failed (reason in "reason").
//
// Guards, in order: POST-only (405); loopback-only bind additionally rejects
// non-loopback remote addresses with 403 (the LAN-exposed bind serves the
// phone, so the loopback guard is skipped there); Origin rejected with 403
// when present and not this plane's loopback origin nor the phone app's own
// origin on a LAN-exposed plane (isAllowedControlOriginFor); token required —
// env token or persisted APIKey on a LAN-exposed plane, mandatory with a
// distinct 403 "no key configured" refusal (checkControlGatedToken).
// Unlike /stop, /start is intentionally available over the LAN (that is its
// whole purpose); /stop never is (no remote stop).
func handleControlStart(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		writeControlJSON(w, http.StatusMethodNotAllowed, map[string]string{"error": "method not allowed"})
		return
	}
	lanExposed := controlPlaneLANMode()
	if !lanExposed && !isLoopbackRequest(r) {
		writeControlJSON(w, http.StatusForbidden, map[string]string{"error": "loopback only"})
		return
	}
	if !isAllowedControlOriginFor(lanExposed, r.Header.Get("Origin")) {
		writeControlJSON(w, http.StatusForbidden, map[string]string{"error": "origin not allowed"})
		return
	}
	if code := checkControlGatedToken(lanExposed, r); code != 0 {
		writeControlJSON(w, code, map[string]string{"error": "unauthorized"})
		return
	}
	// Idempotence: a running server makes the call a no-op answer, mirroring
	// the GUI bindings' already-running branch (the phone then resumes
	// probing — the models were reachable all along).
	serverMu.Lock()
	running := serverRunning
	serverMu.Unlock()
	if running {
		writeControlJSON(w, http.StatusConflict, map[string]interface{}{"started": false, "reason": "already running"})
		return
	}
	// Synchronous start: the phone needs the outcome to decide between
	// "resuming probing" and "surfacing the failure". The bring-up is bounded
	// (scan + preset generation + spawn); controlGuard's panic recovery above
	// keeps a panicking start from taking the process down.
	if err := controlPlaneStartServer(); err != nil {
		writeControlJSON(w, http.StatusInternalServerError, map[string]interface{}{"started": false, "reason": err.Error()})
		return
	}
	writeControlJSON(w, http.StatusOK, map[string]interface{}{"started": true})
}

// handleControlNotFound answers unknown paths with a JSON 404 (the catch-all
// behind controlPlaneMux).
func handleControlNotFound(w http.ResponseWriter, _ *http.Request) {
	writeControlJSON(w, http.StatusNotFound, map[string]string{"error": "not found"})
}
