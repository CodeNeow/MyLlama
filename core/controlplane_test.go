package core

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

// ─── Control plane endpoint tests (httptest, no real bind) ───────
//
// Requests go through controlPlaneMux with hand-set RemoteAddr values, so the
// loopback/foreign-address guards are exercised without binding port 1900.

// testControlHost is the Host header stamped on every request by controlRequest
// (the production loopback address): the mux's Host whitelist requires a valid
// Host, and the table-driven whitelist test overrides it per case.
const testControlHost = "127.0.0.1:1900"

// controlRequest runs one request against the mux with the given remote
// address and headers and returns the recorded response. The "Host" header
// entry overrides req.Host (the field the server-side host check reads —
// net/http ignores a Host entry in the header map on server requests);
// every other header defaults req.Host to testControlHost.
func controlRequest(mux *http.ServeMux, method, target, remoteAddr string, headers map[string]string) *httptest.ResponseRecorder {
	req := httptest.NewRequest(method, target, nil)
	req.Host = testControlHost
	if remoteAddr != "" {
		req.RemoteAddr = remoteAddr
	}
	for k, v := range headers {
		if k == "Host" {
			req.Host = v
			continue
		}
		req.Header.Set(k, v)
	}
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, req)
	return rec
}

// decodeControlJSON decodes a response body into v, failing the test on a
// non-2xx status or a decode error.
func decodeControlJSON(t *testing.T, rec *httptest.ResponseRecorder, wantStatus int, v interface{}) {
	t.Helper()
	if rec.Code != wantStatus {
		t.Fatalf("status = %d, want %d (body: %s)", rec.Code, wantStatus, rec.Body.String())
	}
	if err := json.Unmarshal(rec.Body.Bytes(), v); err != nil {
		t.Fatalf("decode response %q: %v", rec.Body.String(), err)
	}
}

// TestControlPlaneHealth verifies /health always answers 200 for a properly
// addressed request (valid Host), never gated by the token or the loopback
// check — even when the shared token env var is set. The Host whitelist is
// transport-layer validation applied above every handler and is covered by
// TestControlPlaneHostWhitelist.
func TestControlPlaneHealth(t *testing.T) {
	mux := controlPlaneMux()

	// without token env
	var body map[string]interface{}
	decodeControlJSON(t, controlRequest(mux, http.MethodGet, "/health", "", nil), http.StatusOK, &body)
	if body["status"] != "ok" || body["headless"] != true {
		t.Errorf("health body = %v, want {status:ok, headless:true}", body)
	}

	// with token env set: still ungated
	t.Setenv(controlPlaneTokenEnv, "secret")
	decodeControlJSON(t, controlRequest(mux, http.MethodGet, "/health", "", nil), http.StatusOK, &body)
	if body["status"] != "ok" || body["headless"] != true {
		t.Errorf("health body with token env = %v, want {status:ok, headless:true}", body)
	}
}

// controlStatusBody mirrors the GET /status response for decoding.
type controlStatusBody struct {
	Running bool    `json:"running"`
	Port    int     `json:"port"`
	PID     int     `json:"pid"`
	Adopted bool    `json:"adopted"`
	UptimeS float64 `json:"uptimeS"`
	Version string  `json:"version"`
}

// TestControlPlaneStatus verifies the /status snapshot: running state, port,
// pid (adopted when there is no child handle), uptime and the embedded app
// version, plus the GET-only and token guards.
func TestControlPlaneStatus(t *testing.T) {
	saveAdoptedState(t)
	mux := controlPlaneMux()

	// adopted server: running=true, no child handle, pid from adoptedPid
	serverMu.Lock()
	serverRunning = true
	serverCmd = nil
	adoptedPid = 4321
	serverPort = 8080
	serverStartTime = time.Now().Add(-2 * time.Second)
	serverMu.Unlock()

	var body controlStatusBody
	decodeControlJSON(t, controlRequest(mux, http.MethodGet, "/status", "", nil), http.StatusOK, &body)
	if !body.Running || body.Port != 8080 || body.PID != 4321 || !body.Adopted {
		t.Errorf("status = %+v, want running/port 8080/pid 4321/adopted", body)
	}
	if body.UptimeS <= 0 || body.UptimeS > 60 {
		t.Errorf("uptimeS = %v, want ~2", body.UptimeS)
	}
	if body.Version != currentVersion {
		t.Errorf("version = %q, want %q (same source GetAppVersion uses)", body.Version, currentVersion)
	}

	// not running: zeroed pid/port/uptime
	serverMu.Lock()
	serverRunning = false
	adoptedPid = 0
	serverPort = 0
	serverStartTime = time.Time{}
	serverMu.Unlock()
	decodeControlJSON(t, controlRequest(mux, http.MethodGet, "/status", "", nil), http.StatusOK, &body)
	if body.Running || body.PID != 0 || body.Port != 0 || body.Adopted || body.UptimeS != 0 {
		t.Errorf("stopped status = %+v, want zeroed snapshot", body)
	}

	// non-GET → 405
	if rec := controlRequest(mux, http.MethodPost, "/status", "", nil); rec.Code != http.StatusMethodNotAllowed {
		t.Errorf("POST /status = %d, want 405", rec.Code)
	}

	// token gating: env set → 401 missing, 403 wrong, 200 correct
	t.Setenv(controlPlaneTokenEnv, "secret")
	if rec := controlRequest(mux, http.MethodGet, "/status", "", nil); rec.Code != http.StatusUnauthorized {
		t.Errorf("missing token = %d, want 401", rec.Code)
	}
	if rec := controlRequest(mux, http.MethodGet, "/status", "", map[string]string{"X-Control-Token": "wrong"}); rec.Code != http.StatusForbidden {
		t.Errorf("wrong token = %d, want 403", rec.Code)
	}
	decodeControlJSON(t, controlRequest(mux, http.MethodGet, "/status", "", map[string]string{"X-Control-Token": "secret"}), http.StatusOK, &body)
}

// TestControlPlaneLogs verifies the /logs proxy over the Part-1 ring: seeded
// entries page by cursor, since filtering matches serverLogsSince, and the
// 405/400 guards fire.
func TestControlPlaneLogs(t *testing.T) {
	resetServerLogs(t)
	mux := controlPlaneMux()
	addServerLog("alpha")
	addServerLog("beta")
	addServerLog("gamma")

	// full fetch (since 0): everything retained + next cursor
	var page ServerLogsPage
	decodeControlJSON(t, controlRequest(mux, http.MethodGet, "/logs?since=0", "", nil), http.StatusOK, &page)
	if len(page.Entries) != 3 || page.Next != 3 {
		t.Fatalf("logs since 0 = %+v, want 3 entries / next 3", page)
	}
	if page.Entries[0].Seq != 0 || page.Entries[0].Text != "alpha" || page.Entries[2].Text != "gamma" {
		t.Errorf("entries = %+v, want seq 0..2 alpha/beta/gamma", page.Entries)
	}

	// mid cursor: suffix only
	decodeControlJSON(t, controlRequest(mux, http.MethodGet, "/logs?since=2", "", nil), http.StatusOK, &page)
	if len(page.Entries) != 1 || page.Entries[0].Seq != 2 || page.Entries[0].Text != "gamma" || page.Next != 3 {
		t.Errorf("logs since 2 = %+v, want seq 2/gamma with next 3", page)
	}

	// future cursor: empty page
	decodeControlJSON(t, controlRequest(mux, http.MethodGet, "/logs?since=99", "", nil), http.StatusOK, &page)
	if len(page.Entries) != 0 || page.Next != 3 {
		t.Errorf("logs since 99 = %+v, want 0 entries / next 3", page)
	}

	// missing since defaults to 0
	decodeControlJSON(t, controlRequest(mux, http.MethodGet, "/logs", "", nil), http.StatusOK, &page)
	if len(page.Entries) != 3 || page.Next != 3 {
		t.Errorf("logs without since = %+v, want 3 entries / next 3", page)
	}

	// unparsable since → 400
	if rec := controlRequest(mux, http.MethodGet, "/logs?since=abc", "", nil); rec.Code != http.StatusBadRequest {
		t.Errorf("logs since=abc = %d, want 400", rec.Code)
	}

	// non-GET → 405
	if rec := controlRequest(mux, http.MethodPost, "/logs", "", nil); rec.Code != http.StatusMethodNotAllowed {
		t.Errorf("POST /logs = %d, want 405", rec.Code)
	}
}

// TestControlPlaneStop verifies the destructive endpoint's guards in order
// (POST-only → loopback → origin → token) and that the happy path answers
// immediately with {stopping:true} while the actual stop runs asynchronously
// (it is a no-op here: no server is running).
func TestControlPlaneStop(t *testing.T) {
	saveAdoptedState(t)
	serverMu.Lock()
	serverRunning = false
	serverCmd = nil
	adoptedPid = 0
	serverMu.Unlock()

	mux := controlPlaneMux()
	loopback := "127.0.0.1:12345"
	remote := "192.168.1.5:12345"

	// non-POST → 405
	if rec := controlRequest(mux, http.MethodGet, "/stop", loopback, nil); rec.Code != http.StatusMethodNotAllowed {
		t.Errorf("GET /stop = %d, want 405", rec.Code)
	}

	// non-loopback remote → 403 (independent of the token: env unset here)
	if rec := controlRequest(mux, http.MethodPost, "/stop", remote, nil); rec.Code != http.StatusForbidden {
		t.Errorf("remote /stop = %d, want 403", rec.Code)
	}

	// happy path (token unset → loopback bind is the only gate)
	var body map[string]interface{}
	decodeControlJSON(t, controlRequest(mux, http.MethodPost, "/stop", loopback, nil), http.StatusOK, &body)
	if body["stopping"] != true {
		t.Errorf("stop body = %v, want {stopping:true}", body)
	}
	time.Sleep(50 * time.Millisecond) // let the async stop goroutine finish (no-op)

	// token set: loopback caller still needs the header
	t.Setenv(controlPlaneTokenEnv, "secret")
	if rec := controlRequest(mux, http.MethodPost, "/stop", loopback, nil); rec.Code != http.StatusUnauthorized {
		t.Errorf("missing token /stop = %d, want 401", rec.Code)
	}
	if rec := controlRequest(mux, http.MethodPost, "/stop", loopback, map[string]string{"X-Control-Token": "wrong"}); rec.Code != http.StatusForbidden {
		t.Errorf("wrong token /stop = %d, want 403", rec.Code)
	}
	decodeControlJSON(t, controlRequest(mux, http.MethodPost, "/stop", loopback, map[string]string{"X-Control-Token": "secret"}), http.StatusOK, &body)
	if body["stopping"] != true {
		t.Errorf("stop body with token = %v, want {stopping:true}", body)
	}
	time.Sleep(50 * time.Millisecond)
}

// TestControlPlaneUnknownPath verifies the catch-all answers JSON 404.
func TestControlPlaneUnknownPath(t *testing.T) {
	mux := controlPlaneMux()
	var body map[string]string
	decodeControlJSON(t, controlRequest(mux, http.MethodGet, "/nope", "", nil), http.StatusNotFound, &body)
	if body["error"] != "not found" {
		t.Errorf("body = %v, want {error:not found}", body)
	}
}

// TestControlGuardPanicRecovery verifies a panicking handler becomes a 500
// JSON response instead of crashing the headless process (the FreeToken
// crash-proof rule). The Host is set to a whitelisted value because the guard
// now validates Host before dispatching (new contract: a foreign Host answers
// 421 without reaching the wrapped handler at all).
func TestControlGuardPanicRecovery(t *testing.T) {
	boom := controlGuard(func(w http.ResponseWriter, r *http.Request) {
		panic(fmt.Errorf("boom"))
	})
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/boom", nil)
	req.Host = testControlHost
	boom(rec, req)
	if rec.Code != http.StatusInternalServerError {
		t.Fatalf("panic handler status = %d, want 500", rec.Code)
	}
	var body map[string]string
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatalf("decode panic response %q: %v", rec.Body.String(), err)
	}
	if body["error"] != "internal error" {
		t.Errorf("panic body = %v, want {error:internal error}", body)
	}
}

// TestControlPlaneHostWhitelist verifies the DNS-rebinding defense: every
// endpoint (the guard sits above all handlers, /health included) rejects a
// request whose Host header does not name the control plane, answering 421.
// The three loopback spellings of the bound port pass the host gate; /health
// then answers 200 (it has no further gates) proving the gate not only
// rejects foreign hosts but lets legitimate ones through.
func TestControlPlaneHostWhitelist(t *testing.T) {
	mux := controlPlaneMux()
	cases := []struct {
		name string
		host string
		want int
	}{
		{"loopback ip", "127.0.0.1:1900", http.StatusOK},
		{"localhost", "localhost:1900", http.StatusOK},
		{"ipv6 loopback", "[::1]:1900", http.StatusOK},
		{"attacker domain with port", "evil.example:1900", http.StatusMisdirectedRequest},
		{"attacker domain bare", "evil.example", http.StatusMisdirectedRequest},
		{"loopback wrong port", "127.0.0.1:8080", http.StatusMisdirectedRequest},
		{"empty host", "", http.StatusMisdirectedRequest},
	}
	for _, tc := range cases {
		// /health: only the Host gate stands between the request and 200.
		if rec := controlRequest(mux, http.MethodGet, "/health", "", map[string]string{"Host": tc.host}); rec.Code != tc.want {
			t.Errorf("%s: /health host %q = %d, want %d", tc.name, tc.host, rec.Code, tc.want)
		}
	}
	// The catch-all and the gated endpoints run behind the same host gate:
	// one representative foreign-host probe each.
	if rec := controlRequest(mux, http.MethodGet, "/status", "", map[string]string{"Host": "evil.example:1900"}); rec.Code != http.StatusMisdirectedRequest {
		t.Errorf("/status foreign host = %d, want 421", rec.Code)
	}
	if rec := controlRequest(mux, http.MethodGet, "/nope", "", map[string]string{"Host": "evil.example:1900"}); rec.Code != http.StatusMisdirectedRequest {
		t.Errorf("catch-all foreign host = %d, want 421", rec.Code)
	}
}

// TestControlPlaneOriginCheck verifies the CSRF defense on the state-changing
// endpoint: a cross-site browser POST necessarily carries the attacker's
// origin (403), "null" origins (sandboxed iframes) are rejected (403), while
// origin-less requests (curl/script shape) proceed to the token logic
// unchanged, and the plane's own loopback origin is accepted.
func TestControlPlaneOriginCheck(t *testing.T) {
	saveAdoptedState(t)
	serverMu.Lock()
	serverRunning = false
	serverCmd = nil
	adoptedPid = 0
	serverMu.Unlock()

	mux := controlPlaneMux()
	loopback := "127.0.0.1:12345"

	cases := []struct {
		name   string
		origin string
		want   int
	}{
		{"attacker origin", "http://evil.example", http.StatusForbidden},
		{"null origin", "null", http.StatusForbidden},
		{"https attacker origin", "https://evil.example", http.StatusForbidden},
		{"no origin (curl shape)", "", http.StatusOK},
		{"own loopback origin", "http://127.0.0.1:1900", http.StatusOK},
	}
	for _, tc := range cases {
		var headers map[string]string
		if tc.origin != "" {
			headers = map[string]string{"Origin": tc.origin}
		}
		rec := controlRequest(mux, http.MethodPost, "/stop", loopback, headers)
		if rec.Code != tc.want {
			t.Errorf("%s: POST /stop origin %q = %d, want %d", tc.name, tc.origin, rec.Code, tc.want)
		}
		if tc.want == http.StatusOK {
			var body map[string]interface{}
			if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil || body["stopping"] != true {
				t.Errorf("%s: stop body = %s, want {stopping:true}", tc.name, rec.Body.String())
			}
		}
	}
	time.Sleep(50 * time.Millisecond) // let async no-op stop goroutines finish

	// Token logic still applies on the origin-less path: env set → 401.
	t.Setenv(controlPlaneTokenEnv, "secret")
	if rec := controlRequest(mux, http.MethodPost, "/stop", loopback, nil); rec.Code != http.StatusUnauthorized {
		t.Errorf("origin-less stop with token env = %d, want 401 (origin gate must not bypass token)", rec.Code)
	}
}

// ─── POST /start (Phase R: phone-initiated remote service start) ────────────

// withControlLAN pins the control plane's exposure mode for the duration of
// the test (the direct-mux tests never bind a real listener, so the mode is
// set by hand the same way startControlPlane records it).
func withControlLAN(t *testing.T, lanExposed bool) {
	t.Helper()
	controlPlaneMu.Lock()
	origPort, origLAN := controlPlanePort, controlPlaneLAN
	controlPlanePort, controlPlaneLAN = "1900", lanExposed
	controlPlaneMu.Unlock()
	t.Cleanup(func() {
		controlPlaneMu.Lock()
		controlPlanePort, controlPlaneLAN = origPort, origLAN
		controlPlaneMu.Unlock()
	})
}

// withControlAPIKey pins the cached server config's APIKey (the second LAN
// token source) for the duration of the test.
func withControlAPIKey(t *testing.T, key string) {
	t.Helper()
	serverConfigMu.Lock()
	orig := cachedServerConfig
	cachedServerConfig.APIKey = key
	serverConfigMu.Unlock()
	t.Cleanup(func() {
		serverConfigMu.Lock()
		cachedServerConfig = orig
		serverConfigMu.Unlock()
	})
}

// controlStartState carries the stubbed /start bring-up result.
type controlStartState struct{ err error }

// withControlStartStub replaces the /start seam's bring-up function (no test
// ever really scans model dirs or spawns llama-server) and returns the state
// pointer the test flips to inject outcomes.
func withControlStartStub(t *testing.T) *controlStartState {
	t.Helper()
	state := &controlStartState{}
	orig := controlPlaneStartServer
	controlPlaneStartServer = func() error { return state.err }
	t.Cleanup(func() { controlPlaneStartServer = orig })
	return state
}

// TestControlPlaneStartLoopback verifies /start under the historical loopback
// bind: POST-only, non-loopback callers rejected, token semantics unchanged
// (env unset → the loopback bind is the only gate; env set → 401 missing /
// 403 wrong), and the response bodies carry the started flag / failure reason.
func TestControlPlaneStartLoopback(t *testing.T) {
	saveAdoptedState(t)
	withControlLAN(t, false)
	stub := withControlStartStub(t)

	serverMu.Lock()
	serverRunning = false
	serverCmd = nil
	adoptedPid = 0
	serverMu.Unlock()

	mux := controlPlaneMux()
	loopback := "127.0.0.1:12345"
	remote := "192.168.1.5:12345"

	// non-POST → 405
	if rec := controlRequest(mux, http.MethodGet, "/start", loopback, nil); rec.Code != http.StatusMethodNotAllowed {
		t.Errorf("GET /start = %d, want 405", rec.Code)
	}

	// non-loopback remote → 403 on the loopback bind
	if rec := controlRequest(mux, http.MethodPost, "/start", remote, nil); rec.Code != http.StatusForbidden {
		t.Errorf("remote /start on loopback bind = %d, want 403", rec.Code)
	}

	// happy path: 200 {"started":true}
	var body struct {
		Started bool   `json:"started"`
		Reason  string `json:"reason"`
	}
	decodeControlJSON(t, controlRequest(mux, http.MethodPost, "/start", loopback, nil), http.StatusOK, &body)
	if !body.Started {
		t.Errorf("start body = %+v, want started:true", body)
	}

	// start failure: 500 with the error text as reason
	stub.err = fmt.Errorf("no models found in the LLM-Models directory")
	rec := controlRequest(mux, http.MethodPost, "/start", loopback, nil)
	if rec.Code != http.StatusInternalServerError {
		t.Fatalf("failing /start = %d, want 500", rec.Code)
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil || body.Started || body.Reason == "" {
		t.Errorf("failure body = %s, want {started:false, reason:<err>}", rec.Body.String())
	}

	// token env set: loopback callers need the header (401 missing / 403 wrong)
	t.Setenv(controlPlaneTokenEnv, "secret")
	if rec := controlRequest(mux, http.MethodPost, "/start", loopback, nil); rec.Code != http.StatusUnauthorized {
		t.Errorf("missing token /start = %d, want 401", rec.Code)
	}
	if rec := controlRequest(mux, http.MethodPost, "/start", loopback, map[string]string{"X-Control-Token": "wrong"}); rec.Code != http.StatusForbidden {
		t.Errorf("wrong token /start = %d, want 403", rec.Code)
	}
	stub.err = nil
	decodeControlJSON(t, controlRequest(mux, http.MethodPost, "/start", loopback, map[string]string{"X-Control-Token": "secret"}), http.StatusOK, &body)
	if !body.Started {
		t.Errorf("start body with env token = %+v, want started:true", body)
	}
}

// TestControlPlaneStartAlreadyRunning verifies the idempotence branch: a
// running llama-server answers 409 {"started":false,"reason":"already running"}
// without invoking the bring-up seam (a phone re-pressing the button against
// a live server is a no-op, not an error surface).
func TestControlPlaneStartAlreadyRunning(t *testing.T) {
	saveAdoptedState(t)
	withControlLAN(t, false)
	withControlStartStub(t)

	serverMu.Lock()
	serverRunning = true
	serverCmd = nil
	adoptedPid = 4321
	serverMu.Unlock()

	mux := controlPlaneMux()
	var body struct {
		Started bool   `json:"started"`
		Reason  string `json:"reason"`
	}
	decodeControlJSON(t, controlRequest(mux, http.MethodPost, "/start", "127.0.0.1:12345", nil), http.StatusConflict, &body)
	if body.Started || body.Reason != "already running" {
		t.Errorf("already-running body = %+v, want {started:false, reason:\"already running\"}", body)
	}
}

// TestControlPlaneStartLANAuth is the LAN-exposure auth matrix: on the
// wildcard bind /start is reachable from a LAN remote address, requires a
// token (env token OR the persisted APIKey, constant-time compared), refuses
// a key-less plane outright (403), keeps the Origin whitelist (phone app
// origin allowed, browser attacker origins and "null" rejected), and the
// LAN-exposed /status /logs gates follow the same mandatory-token policy so a
// key-less wildcard bind leaks nothing.
func TestControlPlaneStartLANAuth(t *testing.T) {
	saveAdoptedState(t)
	withControlLAN(t, true)
	stub := withControlStartStub(t)
	stub.err = nil

	serverMu.Lock()
	serverRunning = false
	serverCmd = nil
	adoptedPid = 0
	serverMu.Unlock()

	mux := controlPlaneMux()
	phone := "192.168.1.8:51000"

	cases := []struct {
		name    string
		envSet  bool
		apiKey  string
		headers map[string]string
		want    int
	}{
		// token sources
		{name: "correct APIKey 200", apiKey: "pair-key", headers: map[string]string{"X-Control-Token": "pair-key"}, want: http.StatusOK},
		{name: "correct env token 200", envSet: true, headers: map[string]string{"X-Control-Token": "env-secret"}, want: http.StatusOK},
		{name: "wrong token 401", apiKey: "pair-key", headers: map[string]string{"X-Control-Token": "wrong"}, want: http.StatusUnauthorized},
		{name: "missing token 401", apiKey: "pair-key", want: http.StatusUnauthorized},
		{name: "no token source 403", want: http.StatusForbidden},
		{name: "empty APIKey with header 403", headers: map[string]string{"X-Control-Token": "anything"}, want: http.StatusForbidden},
		// origin gate (APIKey configured, correct token)
		{name: "phone app origin allowed", apiKey: "pair-key", headers: map[string]string{"X-Control-Token": "pair-key", "Origin": "https://wails.localhost"}, want: http.StatusOK},
		{name: "attacker origin 403", apiKey: "pair-key", headers: map[string]string{"X-Control-Token": "pair-key", "Origin": "http://evil.example"}, want: http.StatusForbidden},
		{name: "null origin 403", apiKey: "pair-key", headers: map[string]string{"X-Control-Token": "pair-key", "Origin": "null"}, want: http.StatusForbidden},
		{name: "own loopback origin allowed", apiKey: "pair-key", headers: map[string]string{"X-Control-Token": "pair-key", "Origin": "http://127.0.0.1:1900"}, want: http.StatusOK},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if tc.envSet {
				t.Setenv(controlPlaneTokenEnv, "env-secret")
			}
			withControlAPIKey(t, tc.apiKey)
			var body struct {
				Started bool   `json:"started"`
				Reason  string `json:"reason"`
			}
			rec := controlRequest(mux, http.MethodPost, "/start", phone, tc.headers)
			if rec.Code != tc.want {
				t.Fatalf("POST /start = %d, want %d (body: %s)", rec.Code, tc.want, rec.Body.String())
			}
			if tc.want == http.StatusOK {
				if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil || !body.Started {
					t.Errorf("start body = %s, want {started:true}", rec.Body.String())
				}
			}
		})
	}

	// LAN-mode Host relaxation: the phone addresses the plane by the pairing
	// host (any hostname on the bound port passes the 421 host gate), while
	// a bare host or a wrong port still fails addressing.
	hostCases := []struct {
		name string
		host string
		want int
	}{
		{"pairing host", "192.168.1.5:1900", http.StatusOK},
		{"arbitrary hostname on port", "my-pc.local:1900", http.StatusOK},
		{"bare host no port", "192.168.1.5", http.StatusMisdirectedRequest},
		{"wrong port", "192.168.1.5:8080", http.StatusMisdirectedRequest},
	}
	for _, tc := range hostCases {
		withControlAPIKey(t, "pair-key")
		headers := map[string]string{"X-Control-Token": "pair-key", "Host": tc.host}
		if rec := controlRequest(mux, http.MethodPost, "/start", phone, headers); rec.Code != tc.want {
			t.Errorf("%s: host %q = %d, want %d", tc.name, tc.host, rec.Code, tc.want)
		}
	}

	// /status and /logs on the LAN bind follow the same mandatory-token rule:
	// key-less plane refuses outright, configured key unlocks.
	withControlAPIKey(t, "")
	if rec := controlRequest(mux, http.MethodGet, "/status", phone, nil); rec.Code != http.StatusForbidden {
		t.Errorf("LAN key-less /status = %d, want 403", rec.Code)
	}
	withControlAPIKey(t, "pair-key")
	if rec := controlRequest(mux, http.MethodGet, "/status", phone, nil); rec.Code != http.StatusUnauthorized {
		t.Errorf("LAN /status without header = %d, want 401", rec.Code)
	}
	var statusBody controlStatusBody
	decodeControlJSON(t, controlRequest(mux, http.MethodGet, "/status", phone, map[string]string{"X-Control-Token": "pair-key"}), http.StatusOK, &statusBody)
	if rec := controlRequest(mux, http.MethodGet, "/logs", phone, map[string]string{"X-Control-Token": "wrong"}); rec.Code != http.StatusUnauthorized {
		t.Errorf("LAN /logs wrong token = %d, want 401", rec.Code)
	}
}
