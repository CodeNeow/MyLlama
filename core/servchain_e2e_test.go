package core

// Service-chain end-to-end test: drives the REAL production start path
// (model scan -> preset generation -> command build -> llama-server spawn ->
// log tailer) against a tiny GGUF, exercises the OpenAI-compatible HTTP API,
// then verifies graceful stop. This is the core backend pipeline that unit
// tests can only mock.
//
// Deliberately skipped in normal `go test` runs — it needs external assets
// and minutes of runtime — and wired into CI instead (see
// .github/workflows/ci.yml, e2e steps in the windows / macos / backend jobs):
//
//	LLAMA_DESKTOP_E2E=1                      enable the test
//	LLAMA_DESKTOP_E2E_LLAMA_SERVER=<dir>     directory whose root (or one-level
//	                                         subdir, the release-zip layout)
//	                                         contains the llama-server binary
//	LLAMA_DESKTOP_E2E_MODEL=<file>           path to any valid .gguf (CI pins the
//	                                         ~2 MB tinyllamas stories260K)

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"
)

const (
	e2eReadyTimeout   = 3 * time.Minute // model load + HTTP bring-up bound
	e2eRequestTimeout = 30 * time.Second
)

func TestServiceChainE2E(t *testing.T) {
	if os.Getenv("LLAMA_DESKTOP_E2E_MODE") == "direct" {
		t.Skip("LLAMA_DESKTOP_E2E_MODE=direct selects the direct-mode variant")
	}
	runServiceChainE2E(t, false)
}

// TestServiceChainE2EDirect drives the same service chain through the Android
// direct-mode start path (platformGOOS pinned to "android" via the test seam):
// startServerInternalWithModel spawns llama-server with -m/--alias instead of
// a models preset — the mode the official Android release build REQUIRES,
// since it ships without LLAMA_SUBPROCESS and the router-models subsystem
// aborts startup ("subprocess is not enabled on this build"). Also exercises
// the direct-mode contract end to end: fetchRouterModels reports the single
// resident as loaded, whether via the native /models listing (newer builds
// answer 200 with the OpenAI shape) or the /v1/models fallback (older builds
// answer 404). Same env gates as the router variant plus
// LLAMA_DESKTOP_E2E_MODE=direct.
func TestServiceChainE2EDirect(t *testing.T) {
	if os.Getenv("LLAMA_DESKTOP_E2E_MODE") != "direct" {
		t.Skip("LLAMA_DESKTOP_E2E_MODE=direct not set; direct-mode e2e runs only on demand")
	}
	runServiceChainE2E(t, true)
}

// runServiceChainE2E is the shared scan → start → health → models →
// completion → stop chain; direct selects the android direct-mode start.
func runServiceChainE2E(t *testing.T, direct bool) {
	if os.Getenv("LLAMA_DESKTOP_E2E") != "1" {
		t.Skip("LLAMA_DESKTOP_E2E not set; service-chain e2e runs only in CI or on demand")
	}
	serverDir := os.Getenv("LLAMA_DESKTOP_E2E_LLAMA_SERVER")
	modelFile := os.Getenv("LLAMA_DESKTOP_E2E_MODEL")
	if serverDir == "" || modelFile == "" {
		t.Fatal("LLAMA_DESKTOP_E2E_LLAMA_SERVER and LLAMA_DESKTOP_E2E_MODEL must point at the llama.cpp dir and a .gguf file")
	}
	if _, err := os.Stat(modelFile); err != nil {
		t.Fatalf("e2e model missing: %v", err)
	}

	// Direct mode branches on the platformGOOS seam (runtime.GOOS in
	// production): pin android for the duration so the start path takes the
	// direct branch while the host resolves binaries as usual.
	if direct {
		withPlatformGOOS(t, "android")
	}

	// Isolated state: temp cwd + absolute state-file paths (no ~/config
	// pollution) + the default dirs pinned at the e2e fixtures, so the
	// production resolvers (scanModels / resolveLlamaServerBin /
	// resolveServerLogPath) all hit them. The model lands under the scanned
	// directory's <author>/<file>.gguf layout that scanModelsDir expects.
	tmp := withTempCwd(t)
	serverLogFile = filepath.Join(tmp, "myllama-server.log")
	t.Cleanup(func() { serverLogFile = "myllama-server.log" })

	modelsDir := filepath.Join(tmp, "LLM-Models")
	if err := os.MkdirAll(filepath.Join(modelsDir, "tinyllamas"), 0o755); err != nil {
		t.Fatal(err)
	}
	scannedModel := filepath.Join(modelsDir, "tinyllamas", filepath.Base(modelFile))
	if err := copyFile(modelFile, scannedModel); err != nil {
		t.Fatalf("stage e2e model: %v", err)
	}
	pinDefaultDir(t, &defaultLlamaCppDir, serverDir)
	pinDefaultDir(t, &defaultModelsDir, modelsDir)

	// The llama-server binary must resolve exactly as production would —
	// otherwise the child would fail to spawn and the test would test nothing.
	if got := resolveLlamaServerBin(); got == "" {
		t.Fatalf("llama-server not found under %s (production resolver)", serverDir)
	}
	scanned := scanModels()
	if len(scanned) == 0 {
		t.Fatalf("scanModels found no model next to %s (GGUF header parse failed?)", modelFile)
	}

	// Fresh ephemeral port: bound then released, so llama-server can take it.
	l, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("free port probe: %v", err)
	}
	port := l.Addr().(*net.TCPAddr).Port
	l.Close()

	oldCfg := cachedServerConfig
	t.Cleanup(func() { cachedServerConfig = oldCfg })
	cachedServerConfig = ServerConfig{AccessMode: "local", Host: "127.0.0.1", Port: port, MaxModels: 1}

	base := "http://127.0.0.1:" + strconv.Itoa(port)

	if direct {
		// Direct mode resolves the requested name against the scan; the
		// first scanned model is the default pick.
		if err := startServerInternalWithModel(scanned[0].Name); err != nil {
			t.Fatalf("startServerInternalWithModel(%q): %v", scanned[0].Name, err)
		}
	} else {
		if err := startServerInternal(); err != nil {
			t.Fatalf("startServerInternal: %v", err)
		}
	}
	t.Cleanup(func() {
		serverConfigMu.Lock()
		running := serverRunning
		serverConfigMu.Unlock()
		if running {
			_ = stopServerInternal()
		}
	})
	// The wait goroutine closes serverDone when the child exits — a closed
	// channel during bring-up means llama-server died (bad assets, missing
	// DLLs, port clash): fail immediately with its captured output instead of
	// polling a port nothing will ever serve.
	serverMu.Lock()
	childDone := serverDone
	serverMu.Unlock()

	// /health turns 200 {"status":"ok"} once the model is loaded; 503 while loading.
	waitFor(t, "llama-server /health", func() error {
		select {
		case <-childDone:
			return fmt.Errorf("llama-server exited during bring-up; log tail:\n%s", e2eServerLogDump())
		default:
		}
		resp, err := http.Get(base + "/health")
		if err != nil {
			return err
		}
		defer resp.Body.Close()
		if resp.StatusCode != http.StatusOK {
			return fmt.Errorf("health status %d", resp.StatusCode)
		}
		return nil
	}, e2eReadyTimeout)

	// Preset registration: the scanned tiny model must be served by alias,
	// and the alias feeds the model field of the inference requests below
	// (router mode requires it).
	var modelID string
	waitFor(t, "/v1/models listing", func() error {
		var body struct {
			Data []struct {
				ID string `json:"id"`
			} `json:"data"`
		}
		if err := e2eGetJSON(base+"/v1/models", &body); err != nil {
			return err
		}
		if len(body.Data) == 0 {
			return fmt.Errorf("model list empty")
		}
		modelID = body.Data[0].ID
		return nil
	}, e2eRequestTimeout)

	// Direct-mode contract: the resident model is served under its sanitized
	// display name (--alias) and fetchRouterModels reports it as loaded — via
	// the native /models listing on newer builds (OpenAI shape, no status) or
	// the /v1/models fallback on older builds (router /models absent, 404).
	if direct {
		if want := sanitizeAlias(scanned[0].Name); modelID != want {
			t.Errorf("direct mode model id = %q, want the sanitized alias %q", modelID, want)
		}
		loaded, err := fetchRouterModels(port)
		if err != nil {
			t.Fatalf("fetchRouterModels direct-mode fallback: %v", err)
		}
		if len(loaded) != 1 || loaded[0].ID != modelID || loaded[0].Type != "chat" || loaded[0].Status != "loaded" {
			t.Errorf("direct-mode loaded models = %+v, want [{%s chat loaded}]", loaded, modelID)
		}
	}

	// Real inference round-trip through the OpenAI-compatible completion API.
	var comp struct {
		Choices []struct {
			Text string `json:"text"`
		} `json:"choices"`
	}
	if err := e2ePostJSON(base+"/v1/completions", map[string]any{
		"model": modelID, "prompt": "Once upon a time", "n_predict": 16, "cache_prompt": false,
	}, &comp); err != nil {
		t.Fatalf("POST /v1/completions: %v", err)
	}
	if len(comp.Choices) == 0 {
		t.Fatalf("/v1/completions returned no choices")
	}
	t.Logf("completion sample: %q", comp.Choices[0].Text)

	// Chat endpoint: soft-checked — a base-only GGUF without a chat template
	// may legitimately reject /v1/chat/completions on some llama.cpp builds;
	// the completions round-trip above is the hard inference assertion.
	var chat map[string]any
	if err := e2ePostJSON(base+"/v1/chat/completions", map[string]any{
		"model":    modelID,
		"messages": []map[string]string{{"role": "user", "content": "hi"}}, "n_predict": 8,
	}, &chat); err != nil {
		t.Logf("note: /v1/chat/completions unavailable on this model/build (soft check): %v", err)
	}

	// The log ring must have captured the child output through the tailer.
	if _, cur := serverLogsSince(0); cur == 0 {
		t.Errorf("server log ring is empty; log-file capture pipeline broken")
	}

	// Graceful stop: stopServerInternal must return nil and take the child down.
	if err := stopServerInternal(); err != nil {
		t.Fatalf("stopServerInternal: %v", err)
	}
	waitFor(t, "server port closed after stop", func() error {
		resp, err := http.Get(base + "/health")
		if err == nil {
			resp.Body.Close()
			return fmt.Errorf("still serving (status %d)", resp.StatusCode)
		}
		return nil
	}, 15*time.Second)
}

// pinDefaultDir swaps a default-directory seam for an absolute fixture path,
// restoring the previous value at cleanup.
func pinDefaultDir(t *testing.T, seam *func() string, dir string) {
	t.Helper()
	old := *seam
	*seam = func() string { return dir }
	t.Cleanup(func() { *seam = old })
}

// waitFor polls check until it succeeds or the timeout elapses, failing with
// the last error.
func waitFor(t *testing.T, what string, check func() error, timeout time.Duration) {
	t.Helper()
	deadline := time.Now().Add(timeout)
	var last error
	for time.Now().Before(deadline) {
		if err := check(); err == nil {
			return
		} else {
			last = err
		}
		time.Sleep(500 * time.Millisecond)
	}
	t.Fatalf("%s not ready within %s: %v\nserver log tail:\n%s", what, timeout, last, e2eServerLogDump())
}

// e2eServerLogDump returns the captured llama-server output for failure
// diagnostics.
func e2eServerLogDump() string {
	lines, _ := serverLogsSince(0)
	n := len(lines)
	if n > 40 {
		n = 40
	}
	var b strings.Builder
	for _, e := range lines[len(lines)-n:] {
		b.WriteString(e.text)
		b.WriteByte('\n')
	}
	return b.String()
}

func e2eGetJSON(url string, out any) error {
	resp, err := http.Get(url)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(resp.Body)
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("status %d: %s", resp.StatusCode, truncateBody(body))
	}
	return json.Unmarshal(body, out)
}

func e2ePostJSON(url string, payload any, out any) error {
	buf, err := json.Marshal(payload)
	if err != nil {
		return err
	}
	resp, err := http.Post(url, "application/json", bytes.NewReader(buf))
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(resp.Body)
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("status %d: %s", resp.StatusCode, truncateBody(body))
	}
	if out == nil {
		return nil
	}
	return json.Unmarshal(body, out)
}

func truncateBody(b []byte) string {
	const max = 300
	if len(b) > max {
		b = b[:max]
	}
	return string(b)
}
