package core

import (
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"testing"
	"time"
)

// saveServerState snapshots server-related globals (logs, running state, command, custom
// llama.cpp dir, models dir) and restores them after the test.
func saveServerState(t *testing.T) (origLogs []serverLogEntry, origDir string) {
	t.Helper()
	serverLogsMu.Lock()
	origLogs = serverLogs
	origLogNext := serverLogNext
	serverLogsMu.Unlock()
	serverMu.Lock()
	origRunning := serverRunning
	origCmd := serverCmd
	serverMu.Unlock()
	customLlamaCppMu.Lock()
	origDir = customLlamaCppDir
	customLlamaCppMu.Unlock()
	modelsDirMu.Lock()
	origModelsDir := customModelsDir
	modelsDirMu.Unlock()
	llamaCppDownloadDirMu.Lock()
	origLlamaDownloadDir := llamaCppDownloadDirOverride
	llamaCppDownloadDirMu.Unlock()
	modelDownloadDirMu.Lock()
	origModelDownloadDir := modelDownloadDirOverride
	modelDownloadDirMu.Unlock()
	t.Cleanup(func() {
		serverLogsMu.Lock()
		serverLogs = origLogs
		serverLogNext = origLogNext
		serverLogsMu.Unlock()
		serverMu.Lock()
		serverRunning = origRunning
		serverCmd = origCmd
		serverMu.Unlock()
		customLlamaCppMu.Lock()
		customLlamaCppDir = origDir
		customLlamaCppMu.Unlock()
		modelsDirMu.Lock()
		customModelsDir = origModelsDir
		modelsDirMu.Unlock()
		llamaCppDownloadDirMu.Lock()
		llamaCppDownloadDirOverride = origLlamaDownloadDir
		llamaCppDownloadDirMu.Unlock()
		modelDownloadDirMu.Lock()
		modelDownloadDirOverride = origModelDownloadDir
		modelDownloadDirMu.Unlock()
	})
	return
}

// TestBuildServerCommand verifies server command construction: default llama-server binary
// and fixed argument sequence (host/port/preset/max/batching/webui). No --models-dir is
// passed: the preset already registers every model, and auto-scanning the directory would
// duplicate each model under a second id.
func TestBuildServerCommand(t *testing.T) {
	saveServerState(t)
	cfg := ServerConfig{AccessMode: accessLocal, Host: "127.0.0.1", Port: 8080, MaxModels: 1, CacheRAM: 0}
	bin, args, err := buildServerCommand(cfg, "/tmp/preset.ini", nil)
	if err != nil {
		t.Fatal(err)
	}

	if bin != "llama-server" {
		t.Errorf("bin = %q, want llama-server", bin)
	}
	want := []string{
		"--host", "127.0.0.1",
		"--port", "8080",
		"--models-preset", "/tmp/preset.ini",
		"--models-max", "1",
		"--cont-batching",
		"--no-webui",
	}
	if len(args) != len(want) {
		t.Fatalf("args = %v, want %v", args, want)
	}
	for i := range want {
		if args[i] != want[i] {
			t.Errorf("args[%d] = %q, want %q", i, args[i], want[i])
		}
	}
}

// TestBuildServerCommandLANHost verifies --host is derived as 0.0.0.0 when AccessMode=lan;
// even if cfg.Host is still 127.0.0.1 (not normalized), the effectiveHost derivation
// result takes precedence.
func TestBuildServerCommandLANHost(t *testing.T) {
	saveServerState(t)
	cfg := ServerConfig{AccessMode: accessLAN, Host: "127.0.0.1", Port: 8080, MaxModels: 1, CacheRAM: 0}
	_, args, err := buildServerCommand(cfg, "/tmp/preset.ini", nil)
	if err != nil {
		t.Fatal(err)
	}

	found := false
	for i, a := range args {
		if a == "--host" && i+1 < len(args) {
			if args[i+1] != "0.0.0.0" {
				t.Fatalf("lan mode --host = %q, want 0.0.0.0 (args=%v)", args[i+1], args)
			}
			found = true
		}
	}
	if !found {
		t.Fatalf("args missing --host: %v", args)
	}
}

// TestBuildServerCommandCacheRAM verifies CacheRAM config appends the --cache-ram argument,
// and MaxModels minimum is 1 (prevents passing 0 to llama-server).
func TestBuildServerCommandCacheRAM(t *testing.T) {
	cfg := ServerConfig{AccessMode: accessLocal, Host: "127.0.0.1", Port: 8080, MaxModels: 0, CacheRAM: 4096}
	_, args, err := buildServerCommand(cfg, "/tmp/preset.ini", nil)
	if err != nil {
		t.Fatal(err)
	}

	joined := strings.Join(args, " ")
	if !strings.Contains(joined, "--cache-ram 4096") {
		t.Errorf("args missing --cache-ram: %v", args)
	}
	if !strings.Contains(joined, "--models-max 1") {
		t.Errorf("MaxModels=0 should fall back to 1: %v", args)
	}
}

// TestBuildServerCommandCustomDir verifies that when llama-server is not found in PATH,
// the binary under the custom directory is used first (llama-server(.exe)).
func TestBuildServerCommandCustomDir(t *testing.T) {
	saveServerState(t)
	custom := t.TempDir()
	binName := "llama-server"
	if runtime.GOOS == "windows" {
		binName += ".exe"
	}
	if err := os.WriteFile(filepath.Join(custom, binName), []byte("x"), 0755); err != nil {
		t.Fatal(err)
	}
	customLlamaCppMu.Lock()
	customLlamaCppDir = custom
	customLlamaCppMu.Unlock()

	cfg := ServerConfig{AccessMode: accessLocal, Host: "127.0.0.1", Port: 8080, MaxModels: 1, CacheRAM: 0}
	bin, _, _ := buildServerCommand(cfg, "/tmp/preset.ini", nil)

	want := filepath.Join(custom, binName)
	if bin != want {
		t.Errorf("bin = %q, want %q", bin, want)
	}
}

// TestBuildServerCommandOmitsModelsDir verifies buildServerCommand no longer
// passes --models-dir: the preset already registers every model (download path
// + imported directory), and letting llama-server also auto-scan the download
// path would register each model twice under different ids.
func TestBuildServerCommandOmitsModelsDir(t *testing.T) {
	saveServerState(t)
	customModels := t.TempDir()
	modelDownloadDirMu.Lock()
	modelDownloadDirOverride = customModels
	modelDownloadDirMu.Unlock()

	cfg := ServerConfig{AccessMode: accessLocal, Host: "127.0.0.1", Port: 8080, MaxModels: 1, CacheRAM: 0}
	_, args, err := buildServerCommand(cfg, "/tmp/preset.ini", nil)
	if err != nil {
		t.Fatal(err)
	}

	for _, a := range args {
		if a == "--models-dir" {
			t.Fatalf("args should not contain --models-dir (duplicate registration), actual args = %v", args)
		}
	}
}

// TestBuildServerCommandAPIKey verifies the API key never lands on argv: the
// key is delivered through the LLAMA_API_KEY environment variable at spawn
// time (llama.cpp b10689 reads --api-key's value from that variable), so the
// command line carries no credential regardless of the configuration, and an
// empty APIKey (default, no authentication) is indistinguishable on argv.
func TestBuildServerCommandAPIKey(t *testing.T) {
	saveServerState(t)

	// empty APIKey → no --api-key flag
	cfg := ServerConfig{AccessMode: accessLocal, Host: "127.0.0.1", Port: 8080, MaxModels: 1, CacheRAM: 0}
	_, args, err := buildServerCommand(cfg, "/tmp/preset.ini", nil)
	if err != nil {
		t.Fatal(err)
	}
	if argsContain(args, "--api-key") {
		t.Fatalf("empty APIKey should omit --api-key, actual args = %v", args)
	}

	// non-empty APIKey → argv STILL has no --api-key pair (env-delivered)
	cfg.APIKey = "test-key-fixture"
	_, args, _ = buildServerCommand(cfg, "/tmp/preset.ini", nil)
	if argsContain(args, "--api-key") {
		t.Fatalf("API key must not appear on argv (env-delivered), actual args = %v", args)
	}
}

// TestAPIKeyEnv verifies the spawn-side API-key delivery: a non-empty key
// yields exactly the LLAMA_API_KEY entry (b10689 .set_env name), an empty key
// yields nil so the no-auth child inherits the environment unchanged.
func TestAPIKeyEnv(t *testing.T) {
	if got := apiKeyEnv(""); got != nil {
		t.Errorf("apiKeyEnv(\"\") = %v, want nil", got)
	}
	got := apiKeyEnv("test-key-fixture")
	if len(got) != 1 || got[0] != "LLAMA_API_KEY=test-key-fixture" {
		t.Errorf("apiKeyEnv(test-key-fixture) = %v, want [LLAMA_API_KEY=test-key-fixture]", got)
	}
}

// envHasPrefix reports whether the environment list contains an entry whose
// "NAME=" prefix matches, case-insensitively: Windows env names are
// case-insensitive and their casing depends on how the parent process was
// launched (SystemRoot vs SYSTEMROOT), while our own entries are fixed.
func envHasPrefix(env []string, prefix string) bool {
	for _, e := range env {
		if len(e) >= len(prefix) && strings.EqualFold(e[:len(prefix)], prefix) {
			return true
		}
	}
	return false
}

// baselineEnvPrefix names a variable every parent environment carries, for
// asserting that a child environment still inherits the parent environment
// (SystemRoot on Windows, PATH elsewhere).
func baselineEnvPrefix() string {
	if runtime.GOOS == "windows" {
		return "SystemRoot="
	}
	return "PATH="
}

// TestMergeChildEnv verifies the nil-means-inherit contract of the env merge:
// nil + nil stays nil; a nil base with extra entries is materialized from
// os.Environ() — a bare append to nil would REPLACE the inherited environment
// with only the extra entries (the real-server regression: llama-server
// aborted without SystemRoot/PATH); a non-nil base simply gains the extras.
func TestMergeChildEnv(t *testing.T) {
	if got := mergeChildEnv(nil, nil); got != nil {
		t.Errorf("mergeChildEnv(nil, nil) = %v, want nil", got)
	}
	if got := mergeChildEnv(nil, []string{}); got != nil {
		t.Errorf("mergeChildEnv(nil, empty) = %v, want nil", got)
	}

	base := []string{"A=1"}
	if got := mergeChildEnv(base, nil); len(got) != 1 || got[0] != "A=1" {
		t.Errorf("mergeChildEnv(base, nil) = %v, want base unchanged", got)
	}

	got := mergeChildEnv(nil, []string{"LLAMA_API_KEY=test-key-fixture"})
	if len(got) != len(os.Environ())+1 {
		t.Errorf("nil base materialization: len = %d, want len(os.Environ())+1 = %d", len(got), len(os.Environ())+1)
	}
	if !envHasPrefix(got, baselineEnvPrefix()) {
		t.Errorf("nil base materialization lost the parent baseline (%s…)", baselineEnvPrefix())
	}
	if !envHasPrefix(got, "LLAMA_API_KEY=test-key-fixture") {
		t.Errorf("nil base materialization lost the extra entry: %v", got)
	}

	got = mergeChildEnv([]string{"A=1"}, []string{"B=2"})
	if len(got) != 2 || got[0] != "A=1" || got[1] != "B=2" {
		t.Errorf("mergeChildEnv([A=1], [B=2]) = %v, want [A=1 B=2]", got)
	}
}

// TestBuildChildEnvKeepsInheritance drives the exact env assembly
// spawnServerProcess uses across every override combination. Regression test
// for the real-server finding: on the desktop no-pin path serverChildEnv
// returned nil and a bare append produced an environment containing ONLY
// LLAMA_API_KEY, so the child lost SystemRoot/PATH/... and llama-server
// aborted at startup ("Failed to determine HF cache directory").
func TestBuildChildEnvKeepsInheritance(t *testing.T) {
	baseline := baselineEnvPrefix()
	const key = "test-key-fixture"

	t.Run("desktop no pin with key keeps parent environment", func(t *testing.T) {
		withPlatformGOOS(t, "linux") // cudaDeviceEnv nil, androidLdEnv nil
		env := buildChildEnv("llama-server", ServerConfig{DeviceID: "", APIKey: key})
		if !envHasPrefix(env, baseline) {
			t.Errorf("child env lost the parent baseline (%s…): %d entries", baseline, len(env))
		}
		if !envHasPrefix(env, "LLAMA_API_KEY="+key) {
			t.Errorf("child env missing LLAMA_API_KEY: %d entries", len(env))
		}
	})

	t.Run("desktop no pin empty key stays nil", func(t *testing.T) {
		withPlatformGOOS(t, "linux")
		env := buildChildEnv("llama-server", ServerConfig{DeviceID: "", APIKey: ""})
		if env != nil {
			t.Errorf("no-override child env = %v, want nil (inherit unchanged)", env)
		}
	})

	t.Run("windows cuda pin with key combines all entries", func(t *testing.T) {
		withPlatformGOOS(t, "windows")
		env := buildChildEnv("llama-server", ServerConfig{DeviceID: "GPU-uuid", APIKey: key})
		if !envHasPrefix(env, "CUDA_VISIBLE_DEVICES=GPU-uuid") {
			t.Errorf("child env missing CUDA pin: %d entries", len(env))
		}
		if !envHasPrefix(env, baseline) {
			t.Errorf("cuda-pin child env lost the parent baseline (%s…)", baseline)
		}
		if !envHasPrefix(env, "LLAMA_API_KEY="+key) {
			t.Errorf("cuda-pin child env missing LLAMA_API_KEY")
		}
	})

	t.Run("android ld anchor with key combines all entries", func(t *testing.T) {
		withPlatformGOOS(t, "android") // cudaDeviceEnv nil off windows
		withPathsSeams(t, "android", "", nil, nil)
		env := buildChildEnv("/data/bin/llama-server", ServerConfig{APIKey: key})
		// Same derivation as androidLdEnv: filepath.Dir of the binary path
		// (host-separator dependent, so computed rather than a literal).
		wantLD := "LD_LIBRARY_PATH=" + filepath.Dir("/data/bin/llama-server")
		if !envHasPrefix(env, wantLD) {
			t.Errorf("child env missing %s: %d entries", wantLD, len(env))
		}
		if !envHasPrefix(env, baseline) {
			t.Errorf("android child env lost the parent baseline (%s…)", baseline)
		}
		if !envHasPrefix(env, "LLAMA_API_KEY="+key) {
			t.Errorf("android child env missing LLAMA_API_KEY")
		}
	})
}

// TestRedactAPIKeyArg verifies the defensive startup-log redaction: any
// "--api-key <value>" pair in a joined command line has its value masked,
// other arguments pass through untouched, and a line without the flag is
// returned unchanged (issue #31).
func TestRedactAPIKeyArg(t *testing.T) {
	cases := []struct {
		name string
		in   string
		want string
	}{
		{
			"masks the pair",
			"llama-server --host 127.0.0.1 --api-key test-key-fixture --port 8080",
			"llama-server --host 127.0.0.1 --api-key [REDACTED] --port 8080",
		},
		{
			"masks at end of line",
			"llama-server --api-key test-key-fixture",
			"llama-server --api-key [REDACTED]",
		},
		{
			"handles adjacent pairs",
			"llama-server --api-key k1 --api-key k2",
			"llama-server --api-key [REDACTED] --api-key [REDACTED]",
		},
		{
			"no flag unchanged",
			"llama-server --host 127.0.0.1 --port 8080",
			"llama-server --host 127.0.0.1 --port 8080",
		},
	}
	for _, tt := range cases {
		if got := redactAPIKeyArg(tt.in); got != tt.want {
			t.Errorf("%s: redactAPIKeyArg(%q) = %q, want %q", tt.name, tt.in, got, tt.want)
		}
	}
}

// TestAddServerLogRingBuffer verifies the service-log ring buffer: after exceeding
// serverLogsCap (2000) entries the oldest entries are evicted, the latest log
// is always at the end, and the per-entry sequence numbers stay contiguous
// and monotonic across eviction (the incremental cursor fetch relies on it).
// Extended from the original text-only assertions when the ring gained
// sequence numbers: the cursor must never rewind and eviction must not open
// seq gaps within the retained window.
func TestAddServerLogRingBuffer(t *testing.T) {
	serverLogsMu.Lock()
	serverLogs = nil
	serverLogNext = 0
	serverLogsMu.Unlock()
	defer func() {
		serverLogsMu.Lock()
		serverLogs = nil
		serverLogNext = 0
		serverLogsMu.Unlock()
	}()

	const total = serverLogsCap + 500
	for i := 0; i < total; i++ {
		addServerLog(fmt.Sprintf("line-%d", i))
	}

	serverLogsMu.Lock()
	defer serverLogsMu.Unlock()
	if len(serverLogs) > serverLogsCap {
		t.Errorf("log count %d exceeds limit %d", len(serverLogs), serverLogsCap)
	}
	if len(serverLogs) >= total {
		t.Errorf("oldest entries were not evicted: %d entries after %d appends", len(serverLogs), total)
	}
	if serverLogs[len(serverLogs)-1].text != fmt.Sprintf("line-%d", total-1) {
		t.Errorf("latest log should be at the end: %v", serverLogs[len(serverLogs)-1])
	}
	if serverLogs[0].text == "line-0" {
		t.Error("oldest entry should have been evicted")
	}
	// Sequence numbers are contiguous within the retained window and end at
	// total-1: eviction slides the window forward without rewinding or
	// duplicating the cursor.
	for i := 1; i < len(serverLogs); i++ {
		if serverLogs[i].seq != serverLogs[i-1].seq+1 {
			t.Fatalf("seq gap at ring[%d]: %d follows %d", i, serverLogs[i].seq, serverLogs[i-1].seq)
		}
	}
	if want := int64(total - serverLogsCap); serverLogs[0].seq != want {
		t.Errorf("first retained seq = %d, want %d", serverLogs[0].seq, want)
	}
	if serverLogNext != total {
		t.Errorf("serverLogNext = %d, want %d", serverLogNext, total)
	}
}

// TestServerLogsSince verifies the cursor fetch used by GetServerLogsSince:
// from 0 it returns everything retained; a mid cursor returns only the suffix
// at or after it; a current or future cursor returns an empty page; and a
// cursor older than the oldest retained entry returns what remains — the gap
// is detectable only via next - since > serverLogsCap, since evicted lines
// cannot be recovered.
func TestServerLogsSince(t *testing.T) {
	resetServerLogs(t)
	for i := 0; i < 10; i++ {
		addServerLog(fmt.Sprintf("l%d", i))
	}

	// since 0 → everything retained, next cursor = count
	entries, next := serverLogsSince(0)
	if len(entries) != 10 || next != 10 {
		t.Fatalf("since 0: len=%d next=%d, want 10/10", len(entries), next)
	}
	if entries[0].seq != 0 || entries[0].text != "l0" || entries[9].text != "l9" {
		t.Errorf("since 0: first=%+v last=%+v", entries[0], entries[9])
	}

	// mid cursor → suffix only
	entries, next = serverLogsSince(7)
	if len(entries) != 3 || next != 10 {
		t.Fatalf("since 7: len=%d next=%d, want 3/10", len(entries), next)
	}
	for i, e := range entries {
		if want := int64(7 + i); e.seq != want || e.text != fmt.Sprintf("l%d", want) {
			t.Errorf("since 7: entries[%d] = %+v, want seq %d", i, e, want)
		}
	}

	// cursor == next → empty page, cursor unchanged
	entries, next = serverLogsSince(10)
	if len(entries) != 0 || next != 10 {
		t.Errorf("since next: len=%d next=%d, want 0/10", len(entries), next)
	}

	// future cursor → empty (the cursor never rewinds)
	entries, next = serverLogsSince(100)
	if len(entries) != 0 || next != 10 {
		t.Errorf("future cursor: len=%d next=%d, want 0/10", len(entries), next)
	}

	// before the retention window: evict until the oldest retained seq is
	// beyond 0, then a since-0 fetch returns the retained remainder and the
	// caller's gap check (next - since > cap) fires.
	for i := 10; i < serverLogsCap+50; i++ {
		addServerLog(fmt.Sprintf("l%d", i))
	}
	entries, next = serverLogsSince(0)
	if len(entries) != serverLogsCap {
		t.Fatalf("after eviction: len=%d, want %d", len(entries), serverLogsCap)
	}
	if entries[0].seq == 0 {
		t.Error("entries before the retention window must not be returned")
	}
	if next-0 <= serverLogsCap {
		t.Errorf("gap check next-since = %d should exceed cap %d", next-0, serverLogsCap)
	}
}

// TestGetServerLogsSinceBinding verifies the Wails binding shape: entries come
// back with their cursor values, the next cursor matches the ring, and the
// page struct carries the JSON field names the frontend contract relies on.
func TestGetServerLogsSinceBinding(t *testing.T) {
	resetServerLogs(t)
	addServerLog("alpha")
	addServerLog("beta")

	app := &App{}
	page, err := app.GetServerLogsSince(0)
	if err != nil {
		t.Fatalf("GetServerLogsSince(0) returned error: %v", err)
	}
	if len(page.Entries) != 2 || page.Next != 2 {
		t.Fatalf("page = %+v, want 2 entries / next 2", page)
	}
	if page.Entries[0].Seq != 0 || page.Entries[0].Text != "alpha" || page.Entries[1].Seq != 1 || page.Entries[1].Text != "beta" {
		t.Errorf("entries = %+v, want seq 0/alpha then seq 1/beta", page.Entries)
	}

	// mid cursor returns only the tail
	page, err = app.GetServerLogsSince(1)
	if err != nil {
		t.Fatalf("GetServerLogsSince(1) returned error: %v", err)
	}
	if len(page.Entries) != 1 || page.Entries[0].Seq != 1 || page.Entries[0].Text != "beta" || page.Next != 2 {
		t.Errorf("page since 1 = %+v, want seq 1/beta with next 2", page)
	}
}

// TestGetServerStatusReturnsPlainStrings verifies GetServerStatus keeps its
// public contract after the ring gained sequence numbers: the "log" field is
// still a plain []string of line texts.
func TestGetServerStatusReturnsPlainStrings(t *testing.T) {
	resetServerLogs(t)
	saveServerState(t)
	addServerLog("plain line")
	addServerLog("another line")

	app := &App{}
	st := app.GetServerStatus()
	logs, ok := st["log"].([]string)
	if !ok {
		t.Fatalf("log field is %T, want []string", st["log"])
	}
	if len(logs) != 2 || logs[0] != "plain line" || logs[1] != "another line" {
		t.Errorf("logs = %v, want [plain line another line]", logs)
	}
	if running, ok := st["running"].(bool); !ok || running {
		t.Errorf("running = %v, want false", st["running"])
	}
}

// TestConcurrentStopServerInternalIdempotent verifies concurrent stopServerInternal calls
// are idempotent-safe when the service is not started (#3): regardless of how many
// concurrent calls are made, all return nil and do not panic, and serverRunning stays
// false. Previously serverRunning was protected by serverLogsMu, and the stop path read
// serverCmd.Process outside the lock; after refactoring, a copy is taken inside serverMu.
func TestConcurrentStopServerInternalIdempotent(t *testing.T) {
	saveServerState(t)
	serverMu.Lock()
	serverRunning = false
	serverCmd = nil
	serverMu.Unlock()

	var wg sync.WaitGroup
	for i := 0; i < 50; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			if err := stopServerInternal(); err != nil {
				t.Errorf("stopServerInternal should return nil, got %v", err)
			}
		}()
	}
	wg.Wait()

	serverMu.Lock()
	running := serverRunning
	serverMu.Unlock()
	if running {
		t.Error("serverRunning should be false after concurrent stop when service was not started")
	}
}

// TestConcurrentStartStopServer verifies high-frequency interleaving of StartServer and
// StopServer does not panic (#3). With an empty LLM-Models directory, startServerInternal
// fails during preset generation and returns without actually launching the llama-server
// child process; StopServer returns nil idempotently in the not-started state. The lock
// acquisition order invariant (serverMu/serverLogsMu) must not deadlock along this
// interleaving path.
func TestConcurrentStartStopServer(t *testing.T) {
	withTempCwd(t)
	saveServerState(t)
	saveConfigState(t)
	serverMu.Lock()
	serverRunning = false
	serverCmd = nil
	serverMu.Unlock()

	app := &App{}
	var wg sync.WaitGroup
	for i := 0; i < 100; i++ {
		wg.Add(2)
		go func() {
			defer wg.Done()
			// empty model directory: StartServer returns error, but does not panic and does not launch child process
			app.StartServer()
		}()
		go func() {
			defer wg.Done()
			app.StopServer()
		}()
	}
	wg.Wait()
}

// TestHelperProcess serves as a llama-server surrogate child process: when the environment
// variable GO_WANT_HELPER_PROCESS=1 is set, it enters a loop (simulating a running service),
// used by TestStopServerInternalKillsRunningProcess to verify the process-termination path.
func TestHelperProcess(t *testing.T) {
	if os.Getenv("GO_WANT_HELPER_PROCESS") != "1" {
		return
	}
	for {
		time.Sleep(100 * time.Millisecond)
	}
}

// TestStopServerInternalKillsRunningProcess verifies the termination path of
// stopServerInternal against a real running process (#3): when serverCmd is the started
// surrogate child process, calling Process.Signal on the in-lock copy should terminate
// the process; if it still reads the global serverCmd outside the lock or calls while
// holding the lock, this test would expose deadlock/crash.
func TestStopServerInternalKillsRunningProcess(t *testing.T) {
	saveServerState(t)
	serverMu.Lock()
	serverRunning = false
	serverCmd = nil
	serverMu.Unlock()

	cmd := exec.Command(os.Args[0], "-test.run=^TestHelperProcess$")
	cmd.Env = append(os.Environ(), "GO_WANT_HELPER_PROCESS=1")
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	serverMu.Lock()
	serverRunning = true
	serverCmd = cmd
	serverMu.Unlock()
	defer func() {
		serverMu.Lock()
		serverRunning = false
		serverCmd = nil
		serverMu.Unlock()
		cmd.Process.Kill()
	}()

	// call cmd.Wait in an independent goroutine; after stop succeeds, Wait should return
	waitDone := make(chan struct{})
	go func() {
		cmd.Wait()
		close(waitDone)
	}()

	if err := stopServerInternal(); err != nil {
		t.Fatalf("stopServerInternal returned error: %v", err)
	}

	select {
	case <-waitDone:
		// process terminated, as expected
	case <-time.After(5 * time.Second):
		t.Fatal("surrogate process was not terminated after stopServerInternal")
	}
}

// saveStopSeams snapshots and restores the stopServerInternal injection
// points and timing knobs the wait-behavior tests swap out.
func saveStopSeams(t *testing.T) (origSignal func(*exec.Cmd, os.Signal) error, origKill func(*exec.Cmd) error) {
	t.Helper()
	origSignal = signalServer
	origKill = killServer
	origGrace, origExit := stopGrace, stopExitWait
	serverMu.Lock()
	origDone := serverDone
	serverMu.Unlock()
	t.Cleanup(func() {
		signalServer = origSignal
		killServer = origKill
		stopGrace, stopExitWait = origGrace, origExit
		serverMu.Lock()
		serverDone = origDone
		serverMu.Unlock()
	})
	return
}

// forgeRunningState registers a running-server lifecycle without a real child:
// serverCmd is an unstarted exec.Cmd (never touched — the seams intercept
// Signal/Kill), done is the completion channel the wait tests control.
func forgeRunningState(t *testing.T) (cmd *exec.Cmd, done chan struct{}) {
	t.Helper()
	saveServerState(t)
	cmd = exec.Command("llama-server-test-surrogate")
	done = make(chan struct{})
	serverMu.Lock()
	serverRunning = true
	serverCmd = cmd
	serverDone = done
	serverMu.Unlock()
	return cmd, done
}

// TestStopServerInternalWaitsForDone verifies the #28 fix: stopServerInternal
// returns only after the wait goroutine's done channel is closed, so the
// StartServer already-running guard can no longer observe a stale
// running=true after a stop-then-start restart. Uses the signalServer seam to
// simulate a delivered interrupt and a delayed child exit without a real
// process; the graceful path must not escalate to Kill.
func TestStopServerInternalWaitsForDone(t *testing.T) {
	saveStopSeams(t)
	_, done := forgeRunningState(t)

	delivered := make(chan struct{})
	signalServer = func(*exec.Cmd, os.Signal) error {
		close(delivered)
		return nil
	}
	kills := 0
	killServer = func(*exec.Cmd) error {
		kills++
		return nil
	}

	// Child "exits" (done closes) 120ms after the interrupt — well within the
	// default 5s grace period.
	go func() {
		<-delivered
		time.Sleep(120 * time.Millisecond)
		close(done)
	}()

	start := time.Now()
	if err := stopServerInternal(); err != nil {
		t.Fatalf("stopServerInternal returned error: %v", err)
	}
	elapsed := time.Since(start)

	select {
	case <-done:
		// done closed by the delayed-exit goroutine, as expected
	default:
		t.Fatal("stopServerInternal returned before the done channel was closed")
	}
	if elapsed < 100*time.Millisecond {
		t.Errorf("stopServerInternal returned after %v, want it to wait for the child exit (~120ms)", elapsed)
	}
	if kills != 0 {
		t.Errorf("graceful exit within the grace period must not escalate to Kill (%d kills)", kills)
	}
}

// TestStopServerInternalExitWaitTimeout verifies the bounded escalation path:
// when the child ignores the interrupt past the grace period, stopServerInternal
// escalates to Kill exactly once and then waits a bounded stopExitWait for the
// exit confirmation; on timeout it logs a [WARN] and returns instead of hanging
// the caller forever (#28).
func TestStopServerInternalExitWaitTimeout(t *testing.T) {
	saveStopSeams(t)
	_, done := forgeRunningState(t)
	_ = done // never closed: simulates a child that outlives the whole stop

	signalServer = func(*exec.Cmd, os.Signal) error { return nil } // interrupt "delivered"
	kills := 0
	killServer = func(*exec.Cmd) error {
		kills++
		return nil
	}
	stopGrace = 30 * time.Millisecond
	stopExitWait = 60 * time.Millisecond

	start := time.Now()
	if err := stopServerInternal(); err != nil {
		t.Fatalf("stopServerInternal returned error: %v", err)
	}
	elapsed := time.Since(start)

	if kills != 1 {
		t.Errorf("escalation Kill calls = %d, want exactly 1 after the grace timeout", kills)
	}
	if elapsed < 80*time.Millisecond {
		t.Errorf("stopServerInternal returned after %v, want it to hold through the bounded exit wait", elapsed)
	}
	if elapsed > 5*time.Second {
		t.Errorf("stopServerInternal blocked %v; the exit wait must stay bounded", elapsed)
	}
}

// TestStopServerInternalFailedSignalWaitsForDone verifies the failed-interrupt
// branch shares the exit wait: after the Signal error escalates to Kill, the
// call still returns only after done closes (or the bounded wait lapses) —
// never immediately with the child state still uncleared (#28).
func TestStopServerInternalFailedSignalWaitsForDone(t *testing.T) {
	saveStopSeams(t)
	_, done := forgeRunningState(t)

	signalServer = func(*exec.Cmd, os.Signal) error {
		return errors.New("signal: process already finished")
	}
	kills := 0
	killServer = func(*exec.Cmd) error {
		kills++
		return nil
	}
	stopExitWait = 5 * time.Second

	go func() {
		time.Sleep(80 * time.Millisecond)
		close(done)
	}()

	start := time.Now()
	if err := stopServerInternal(); err != nil {
		t.Fatalf("stopServerInternal returned error: %v", err)
	}
	if time.Since(start) < 60*time.Millisecond {
		t.Errorf("stopServerInternal returned immediately after the failed interrupt; want it to wait for the exit confirmation")
	}
	if kills != 1 {
		t.Errorf("failed-interrupt escalation Kill calls = %d, want 1", kills)
	}
}
