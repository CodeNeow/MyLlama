package core

import (
	"encoding/json"
	"fmt"
	"net"
	"net/http"
	"os"
	"strings"
	"testing"
	"time"
)

// ─── Headless startup decision ────────────────────────────────────

// TestShouldRunHeadlessPure verifies the pure headless decision matrix:
// non-Windows never headless; --headless wins; --gui overrides the persisted
// preference; dev builds ignore the preference (only the flag works);
// otherwise the preference decides.
func TestShouldRunHeadlessPure(t *testing.T) {
	cases := []struct {
		name        string
		goos        string
		headlessFlg bool
		guiFlag     bool
		enabled     bool
		devBuild    bool
		want        bool
	}{
		{"non-windows never headless", "linux", true, false, true, false, false},
		{"headless flag wins", "windows", true, true, false, false, true},
		{"headless flag wins in dev build", "windows", true, false, false, true, true},
		{"config enabled", "windows", false, false, true, false, true},
		{"config disabled", "windows", false, false, false, false, false},
		{"gui flag overrides config", "windows", false, true, true, false, false},
		{"dev build ignores config preference", "windows", false, false, true, true, false},
		{"dev build gui flag stays GUI", "windows", false, true, false, true, false},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := shouldRunHeadless(c.goos, c.headlessFlg, c.guiFlag, c.enabled, c.devBuild); got != c.want {
				t.Errorf("shouldRunHeadless(%q,%v,%v,%v,%v) = %v, want %v", c.goos, c.headlessFlg, c.guiFlag, c.enabled, c.devBuild, got, c.want)
			}
		})
	}
}

// TestStartOrAdoptServerDecision verifies the headless startup decision with
// injected probes: healthy record adopts without starting; with no record the
// fresh-start attempt fails against an empty model directory (expected,
// logged) and must not mark the server running; a stale record is deleted.
// A record carrying a verified serverStartedAt adopts only when the queried
// process creation time matches (pid-reuse defense); a mismatch deletes the
// record and falls back to a fresh start.
// Every failed start must surface through notifyHeadlessServerStartFailed,
// while adopting a healthy record must not notify.
func TestStartOrAdoptServerDecision(t *testing.T) {
	withTempCwd(t)
	saveServerState(t)
	saveAdoptedState(t)

	// Record notify calls instead of popping the real Windows alert dialog
	var notifyErrs []error
	origNotify := notifyHeadlessServerStartFailed
	notifyHeadlessServerStartFailed = func(err error) { notifyErrs = append(notifyErrs, err) }
	t.Cleanup(func() { notifyHeadlessServerStartFailed = origNotify })

	// no record → startServerInternal fails (empty LLM-Models), server not running
	if err := removeHandover(); err != nil {
		t.Fatal(err)
	}
	_, _ = injectHandoverProbes(t, true, true)
	startOrAdoptServer()
	serverMu.Lock()
	running := serverRunning
	serverMu.Unlock()
	if running {
		t.Error("no handover record + failed start must not mark the server running")
	}
	if len(notifyErrs) != 1 || notifyErrs[0] == nil {
		t.Errorf("a failed start must notify once with the error, got %d calls: %v", len(notifyErrs), notifyErrs)
	}

	// healthy record → adopted, no start attempt needed
	if err := writeHandover(4321, 8282); err != nil {
		t.Fatal(err)
	}
	startOrAdoptServer()
	serverMu.Lock()
	running, adopted, port := serverRunning, adoptedPid, serverPort
	serverMu.Unlock()
	if !running || adopted != 4321 || port != 8282 {
		t.Errorf("healthy record should be adopted, running=%v adopted=%d port=%d", running, adopted, port)
	}
	if len(notifyErrs) != 1 {
		t.Errorf("adopting a healthy record must not notify, got %d calls", len(notifyErrs))
	}

	// stale record → deleted even though the subsequent start fails
	if err := writeHandover(9999, 8383); err != nil {
		t.Fatal(err)
	}
	_, _ = injectHandoverProbes(t, false, false)
	startOrAdoptServer()
	if _, err := os.Stat(handoverFile); !os.IsNotExist(err) {
		t.Errorf("stale handover record should be deleted, stat err = %v", err)
	}
	if len(notifyErrs) != 2 || notifyErrs[1] == nil {
		t.Errorf("the start after stale-record cleanup must notify again, got %d calls: %v", len(notifyErrs), notifyErrs)
	}

	// record with a verifiable serverStartedAt + matching creation-time fake
	// → adopted end to end (start-time check ran and passed)
	recorded := time.Now().Add(-2 * time.Hour)
	setServerTrueStart(t, recorded)
	if err := writeHandover(5150, 8686); err != nil {
		t.Fatal(err)
	}
	_, _ = injectHandoverProbes(t, true, true)
	_ = injectHandoverProcStart(t, recorded, true)
	startOrAdoptServer()
	serverMu.Lock()
	running, adopted, port = serverRunning, adoptedPid, serverPort
	serverMu.Unlock()
	if !running || adopted != 5150 || port != 8686 {
		t.Errorf("record with matching start time should be adopted, running=%v adopted=%d port=%d", running, adopted, port)
	}
	if len(notifyErrs) != 2 {
		t.Errorf("adopting a start-time-verified record must not notify, got %d calls", len(notifyErrs))
	}

	// mismatching creation-time fake → record removed, fresh start attempted
	// (fails against the empty model directory and notifies)
	serverMu.Lock()
	serverRunning = false
	adoptedPid = 0
	serverPort = 0
	serverMu.Unlock()
	setServerTrueStart(t, recorded.Add(time.Hour))
	if err := writeHandover(6161, 8787); err != nil {
		t.Fatal(err)
	}
	_, _ = injectHandoverProbes(t, true, true)
	_ = injectHandoverProcStart(t, recorded, true)
	startOrAdoptServer()
	if _, err := os.Stat(handoverFile); !os.IsNotExist(err) {
		t.Errorf("mismatched handover record should be deleted, stat err = %v", err)
	}
	serverMu.Lock()
	running = serverRunning
	serverMu.Unlock()
	if running {
		t.Error("a start-time mismatch must not adopt the recorded server")
	}
	if len(notifyErrs) != 3 || notifyErrs[2] == nil {
		t.Errorf("the fresh start after a mismatch must notify again, got %d calls: %v", len(notifyErrs), notifyErrs)
	}
}

// TestAdoptOrCleanHandover verifies the GUI startup variant: a healthy record
// is adopted (the start-time check must pass too), a stale or mismatched one
// deleted, and no server is auto-started when the record is missing.
func TestAdoptOrCleanHandover(t *testing.T) {
	withTempCwd(t)
	saveServerState(t)
	saveAdoptedState(t)

	// missing record → nothing happens (no auto start in GUI mode)
	if err := removeHandover(); err != nil {
		t.Fatal(err)
	}
	adoptOrCleanHandover()
	serverMu.Lock()
	running := serverRunning
	serverMu.Unlock()
	if running {
		t.Error("GUI startup must not auto-start or adopt with no handover record")
	}

	// stale record → deleted
	if err := writeHandover(1111, 8484); err != nil {
		t.Fatal(err)
	}
	_, _ = injectHandoverProbes(t, false, false)
	adoptOrCleanHandover()
	if _, err := os.Stat(handoverFile); !os.IsNotExist(err) {
		t.Errorf("stale record should be deleted on GUI startup, stat err = %v", err)
	}

	// healthy record → adopted
	if err := writeHandover(2222, 8585); err != nil {
		t.Fatal(err)
	}
	_, _ = injectHandoverProbes(t, true, true)
	adoptOrCleanHandover()
	serverMu.Lock()
	running, adopted, port := serverRunning, adoptedPid, serverPort
	serverMu.Unlock()
	if !running || adopted != 2222 || port != 8585 {
		t.Errorf("GUI startup should adopt a healthy record, running=%v adopted=%d port=%d", running, adopted, port)
	}

	// record with a verifiable serverStartedAt + matching creation-time fake
	// → adopted end to end
	recorded := time.Now().Add(-2 * time.Hour)
	setServerTrueStart(t, recorded)
	if err := writeHandover(4444, 8888); err != nil {
		t.Fatal(err)
	}
	_, _ = injectHandoverProbes(t, true, true)
	_ = injectHandoverProcStart(t, recorded, true)
	adoptOrCleanHandover()
	serverMu.Lock()
	running, adopted, port = serverRunning, adoptedPid, serverPort
	serverMu.Unlock()
	if !running || adopted != 4444 || port != 8888 {
		t.Errorf("GUI startup should adopt a start-time-verified record, running=%v adopted=%d port=%d", running, adopted, port)
	}

	// mismatching creation-time fake → record deleted, no adoption
	serverMu.Lock()
	serverRunning = false
	adoptedPid = 0
	serverPort = 0
	serverMu.Unlock()
	setServerTrueStart(t, recorded.Add(time.Hour))
	if err := writeHandover(5555, 8989); err != nil {
		t.Fatal(err)
	}
	_, _ = injectHandoverProbes(t, true, true)
	_ = injectHandoverProcStart(t, recorded, true)
	adoptOrCleanHandover()
	if _, err := os.Stat(handoverFile); !os.IsNotExist(err) {
		t.Errorf("mismatched record should be deleted on GUI startup, stat err = %v", err)
	}
	serverMu.Lock()
	running = serverRunning
	serverMu.Unlock()
	if running {
		t.Error("GUI startup must not adopt a start-time-mismatched record")
	}
}

// TestConfigApiRouteModeRoundTrip verifies apiRouteMode persistence: default
// false when the field is missing (old configs), explicit true survives a
// save→load round-trip.
func TestConfigApiRouteModeRoundTrip(t *testing.T) {
	withTempCwd(t)
	saveConfigState(t)

	// old config without the field → false
	if err := os.WriteFile(configFile, []byte(`{"theme":"light"}`), 0644); err != nil {
		t.Fatal(err)
	}
	loadConfig()
	if ApiRouteMode() {
		t.Error("old config missing apiRouteMode should default to false")
	}

	// explicit true round-trips
	configMu.Lock()
	apiRouteMode = true
	configMu.Unlock()
	saveConfig()
	data, err := os.ReadFile(configFile)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(data), `"apiRouteMode": true`) {
		t.Errorf("saved config should contain apiRouteMode: true, actual: %s", data)
	}
	configMu.Lock()
	apiRouteMode = false
	configMu.Unlock()
	loadConfig()
	if !ApiRouteMode() {
		t.Error("apiRouteMode=true should survive a save→load round-trip")
	}

	// SetApiRouteMode(false) persists without side effects (no relaunch path)
	configMu.Lock()
	apiRouteMode = true
	configMu.Unlock()
	app := &App{}
	if err := app.SetApiRouteMode(false); err != nil {
		t.Fatalf("SetApiRouteMode(false) should be nil: %v", err)
	}
	if ApiRouteMode() {
		t.Error("SetApiRouteMode(false) should persist false")
	}
	switchRestartPending.Store(false)
}

// TestSetApiRouteModeDevRejected verifies the enable path is rejected in a
// dev build (wails dev): the relaunch-based switch escapes the dev supervisor
// and tears the dev session down, so the preference stays false, no successor
// is spawned and the switch-restart marker stays unset.
func TestSetApiRouteModeDevRejected(t *testing.T) {
	withTempCwd(t)
	saveAdoptedState(t)
	saveConfigState(t)
	launches := injectRelaunchSelf(t, false)

	origDev := isDevBuild
	isDevBuild = true
	t.Cleanup(func() { isDevBuild = origDev })

	// Tray on so the dev guard — not the tray guard — is what rejects
	configMu.Lock()
	trayEnabled = true
	apiRouteMode = false
	configMu.Unlock()

	app := &App{}
	if err := app.SetApiRouteMode(true); err == nil {
		t.Fatal("enabling API route mode in a dev build should surface an error")
	}
	if ApiRouteMode() {
		t.Error("apiRouteMode must stay false when the dev guard rejects the call")
	}
	if len(*launches) != 0 {
		t.Errorf("no successor should be spawned when the dev guard rejects, launches = %v", *launches)
	}
	if switchRestartPending.Load() {
		t.Error("switch-restart marker must stay unset when the dev guard rejects")
	}
}

// injectRelaunchSelf replaces the relaunchSelf injection point, recording the
// argument lists the code tried to spawn; no real process starts.
func injectRelaunchSelf(t *testing.T, fail bool) (calls *[][]string) {
	t.Helper()
	var got [][]string
	orig := relaunchSelf
	relaunchSelf = func(args ...string) error {
		got = append(got, args)
		if fail {
			return os.ErrPermission
		}
		return nil
	}
	t.Cleanup(func() { relaunchSelf = orig })
	return &got
}

// TestSetApiRouteModeEnableHandsOver verifies the enable path with an injected
// relaunch: apiRouteMode persists true, a running adopted server gets a
// handover record, the successor is spawned with --headless, and the
// switch-restart marker is set (a nil ctx in tests skips the Wails quit).
func TestSetApiRouteModeEnableHandsOver(t *testing.T) {
	withTempCwd(t)
	saveAdoptedState(t)
	saveConfigState(t)
	launches := injectRelaunchSelf(t, false)

	// Enabling requires the tray (the headless return entry point)
	configMu.Lock()
	trayEnabled = true
	configMu.Unlock()

	serverMu.Lock()
	serverRunning = true
	serverCmd = nil
	adoptedPid = 3141
	serverPort = 8080
	serverMu.Unlock()

	app := &App{}
	if err := app.SetApiRouteMode(true); err != nil {
		t.Fatalf("SetApiRouteMode(true) should succeed with injected relaunch: %v", err)
	}
	if !ApiRouteMode() {
		t.Error("SetApiRouteMode(true) should persist apiRouteMode=true")
	}
	if len(*launches) != 1 || len((*launches)[0]) != 1 || (*launches)[0][0] != "--headless" {
		t.Errorf("successor should be spawned once with --headless, launches = %v", *launches)
	}
	if !switchRestartPending.Load() {
		t.Error("switch-restart marker should be set after a successful relaunch")
	}
	rec, err := readHandover()
	if err != nil {
		t.Fatalf("handover record should be written for the adopted server: %v", err)
	}
	if rec.Pid != 3141 || rec.Port != 8080 {
		t.Errorf("handover record should carry the adopted pid/port, got %+v", rec)
	}
	switchRestartPending.Store(false)
}

// TestSetApiRouteModeEnableRelaunchFailure verifies the enable path when the
// relaunch fails: an error is returned and the switch-restart marker stays
// unset, so the subsequent GUI shutdown still stops the server normally.
func TestSetApiRouteModeEnableRelaunchFailure(t *testing.T) {
	withTempCwd(t)
	saveAdoptedState(t)
	saveConfigState(t)
	_ = injectRelaunchSelf(t, true)

	// Enabling requires the tray (the headless return entry point)
	configMu.Lock()
	trayEnabled = true
	configMu.Unlock()

	app := &App{}
	if err := app.SetApiRouteMode(true); err == nil {
		t.Fatal("relaunch failure should surface an error")
	}
	if switchRestartPending.Load() {
		t.Error("switch-restart marker must stay unset when the relaunch fails")
	}
}

// TestSetApiRouteModeRequiresTray verifies the enable path is rejected while
// the system tray is disabled (the tray menu is the only way back from
// headless mode): the preference stays false, no successor is spawned and
// the switch-restart marker stays unset.
func TestSetApiRouteModeRequiresTray(t *testing.T) {
	withTempCwd(t)
	saveAdoptedState(t)
	saveConfigState(t)
	launches := injectRelaunchSelf(t, false)

	configMu.Lock()
	trayEnabled = false
	apiRouteMode = false
	configMu.Unlock()

	app := &App{}
	if err := app.SetApiRouteMode(true); err == nil {
		t.Fatal("enabling API route mode with the tray disabled should surface an error")
	}
	if ApiRouteMode() {
		t.Error("apiRouteMode must stay false when the tray guard rejects the call")
	}
	if len(*launches) != 0 {
		t.Errorf("no successor should be spawned when the guard rejects, launches = %v", *launches)
	}
	if switchRestartPending.Load() {
		t.Error("switch-restart marker must stay unset when the guard rejects the call")
	}
}

// ─── Control plane wiring (startControlPlaneHeadless / stopControlPlane) ──

// resetControlPlaneState forces the control plane to the "not running" state
// before and after a test, so wiring tests never leak a stored server.
func resetControlPlaneState(t *testing.T) {
	t.Helper()
	controlPlaneMu.Lock()
	controlPlaneServer = nil
	controlPlaneMu.Unlock()
	t.Cleanup(func() {
		controlPlaneMu.Lock()
		controlPlaneServer = nil
		controlPlaneMu.Unlock()
	})
}

// TestControlPlaneHeadlessWiring verifies the RunHeadless wiring helpers on
// an injected ephemeral listener (the fixed port is never bound): a
// successful start stores the server and serves /health, and stopControlPlane
// shuts it down so subsequent requests fail.
func TestControlPlaneHeadlessWiring(t *testing.T) {
	resetControlPlaneState(t)
	orig := controlPlaneListen
	var ln net.Listener
	controlPlaneListen = func(network, addr string) (net.Listener, error) {
		// Bind an ephemeral loopback port instead of the fixed 1900.
		l, err := net.Listen(network, "127.0.0.1:0")
		ln = l
		return l, err
	}
	t.Cleanup(func() { controlPlaneListen = orig })

	startControlPlaneHeadless()
	controlPlaneMu.Lock()
	stored := controlPlaneServer
	controlPlaneMu.Unlock()
	if stored == nil || ln == nil {
		t.Fatal("successful start must store the control-plane server")
	}

	var body map[string]interface{}
	resp, err := http.Get("http://" + ln.Addr().String() + "/health")
	if err != nil {
		t.Fatalf("GET /health after start: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Errorf("health status = %d, want 200", resp.StatusCode)
	}
	if err := json.NewDecoder(resp.Body).Decode(&body); err != nil {
		t.Fatalf("decode health body: %v", err)
	}
	if body["status"] != "ok" || body["headless"] != true {
		t.Errorf("health body = %v, want {status:ok, headless:true}", body)
	}

	stopControlPlane()
	if _, err := http.Get("http://" + ln.Addr().String() + "/health"); err == nil {
		t.Error("requests must fail after stopControlPlane (listener closed)")
	}
	controlPlaneMu.Lock()
	stored = controlPlaneServer
	controlPlaneMu.Unlock()
	if stored != nil {
		t.Error("stopControlPlane must clear the stored server")
	}
}

// TestControlPlaneDegradedStart verifies the degraded branch the headless
// startup relies on: an occupied port surfaces an error from
// startControlPlane, the wrapper logs a warning and continues (stores
// nothing, does not panic), and stopControlPlane stays a safe no-op.
func TestControlPlaneDegradedStart(t *testing.T) {
	resetControlPlaneState(t)
	orig := controlPlaneListen
	controlPlaneListen = func(network, addr string) (net.Listener, error) {
		return nil, fmt.Errorf("listen %s: port busy", addr)
	}
	t.Cleanup(func() { controlPlaneListen = orig })

	if _, err := startControlPlane(false); err == nil {
		t.Fatal("an occupied port must surface an error from startControlPlane")
	}

	startControlPlaneHeadless() // degraded: logs WARN, keeps going
	controlPlaneMu.Lock()
	stored := controlPlaneServer
	controlPlaneMu.Unlock()
	if stored != nil {
		t.Error("degraded start must not store a control-plane server")
	}
	stopControlPlane() // no-op, must not panic
}
