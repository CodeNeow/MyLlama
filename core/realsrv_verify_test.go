package core

// Real-server regression harness for the llama-server-facing fixes: API-key
// delivery via the LLAMA_API_KEY environment (#26/#31), authenticated router
// calls (#30), the chat-auth contract (#29) and the stop-wait/restart chain
// (#28). Not part of the unit-test tier: it needs a real llama-server build
// and a real model, so it is gated on LLAMA_DESKTOP_E2E=1 AND VERIFY_FIXES=1
// plus the standard LLAMA_DESKTOP_E2E_LLAMA_SERVER (llama.cpp install dir)
// and LLAMA_DESKTOP_E2E_MODELS_DIR (a scanned models directory used READ-ONLY,
// unlike servchain's copy-in model file). Without the gates it skips cleanly.

import (
	"fmt"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"
)

func TestRealServerVerify(t *testing.T) {
	if os.Getenv("LLAMA_DESKTOP_E2E") != "1" || os.Getenv("VERIFY_FIXES") != "1" {
		t.Skip("real-server verification; enable with LLAMA_DESKTOP_E2E=1 VERIFY_FIXES=1")
	}
	serverDir := os.Getenv("LLAMA_DESKTOP_E2E_LLAMA_SERVER")
	modelsDir := os.Getenv("LLAMA_DESKTOP_E2E_MODELS_DIR")
	if serverDir == "" || modelsDir == "" {
		t.Fatal("set LLAMA_DESKTOP_E2E_LLAMA_SERVER and LLAMA_DESKTOP_E2E_MODELS_DIR")
	}

	tmp := withTempCwd(t)
	serverLogFile = filepath.Join(tmp, "myllama-server.log")
	t.Cleanup(func() { serverLogFile = "myllama-server.log" })
	pinDefaultDir(t, &defaultLlamaCppDir, serverDir)
	pinDefaultDir(t, &defaultModelsDir, modelsDir)

	if got := resolveLlamaServerBin(); got == "" {
		t.Fatalf("llama-server not found under %s", serverDir)
	}
	scanned := scanModels()
	if len(scanned) == 0 {
		t.Fatalf("scanModels found no model under %s", modelsDir)
	}
	t.Logf("scanned %d models (read-only real dir)", len(scanned))

	l, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("port probe: %v", err)
	}
	port := l.Addr().(*net.TCPAddr).Port
	l.Close()

	const key = "verify-key-abc123"
	oldCfg := cachedServerConfig
	t.Cleanup(func() { cachedServerConfig = oldCfg })
	cachedServerConfig = ServerConfig{AccessMode: "local", Host: "127.0.0.1", Port: port, MaxModels: 1, APIKey: key}

	base := "http://127.0.0.1:" + strconv.Itoa(port)

	if err := startServerInternal(); err != nil {
		t.Fatalf("startServerInternal: %v", err)
	}
	serverMu.Lock()
	childDone := serverDone
	serverMu.Unlock()

	// #31: the startup log line must not contain the plaintext key.
	logLines, _ := serverLogsSince(0)
	joined := ""
	for _, e := range logLines {
		joined += e.text + "\n"
	}
	if strings.Contains(joined, key) {
		t.Errorf("#31 FAIL: startup log contains the plaintext key:\n%s", joined)
	} else {
		t.Logf("#31 OK: startup log free of the plaintext key (%d lines)", len(logLines))
	}

	// /health 200 once loaded (big real model: generous bound).
	waitFor(t, "llama-server /health (model load)", func() error {
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
	}, 6*time.Minute)

	// #29 contract: completion without bearer -> 401, with bearer -> 200.
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
	t.Logf("model id: %s", modelID)

	req, _ := http.NewRequest(http.MethodPost, base+"/v1/completions",
		strings.NewReader(`{"model":"`+modelID+`","prompt":"hi","n_predict":4}`))
	req.Header.Set("Content-Type", "application/json")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("completion without bearer: %v", err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusUnauthorized {
		t.Errorf("#29 FAIL: completion without bearer = %d, want 401", resp.StatusCode)
	} else {
		t.Logf("#29 OK: no-bearer completion rejected with 401")
	}

	req, _ = http.NewRequest(http.MethodPost, base+"/v1/completions",
		strings.NewReader(`{"model":"`+modelID+`","prompt":"hi","n_predict":4}`))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+key)
	resp, err = http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("completion with bearer: %v", err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Errorf("#29 FAIL: completion with bearer = %d, want 200", resp.StatusCode)
	} else {
		t.Logf("#29 OK: bearer completion accepted (model loaded)")
	}

	// #30: the app's own router helpers now attach the bearer — unload the
	// loaded model through unloadRouterModel and list through fetchRouterModels.
	loaded, err := fetchRouterModels(port)
	if err != nil {
		t.Errorf("#30 FAIL: fetchRouterModels against authed server: %v", err)
	} else {
		t.Logf("#30 OK: fetchRouterModels listed %d loaded model(s)", len(loaded))
	}
	if err := unloadRouterModel(port, modelID); err != nil {
		t.Errorf("#30 FAIL: unloadRouterModel against authed server: %v", err)
	} else {
		t.Logf("#30 OK: unloadRouterModel accepted (loaded model evicted)")
	}

	// #28: stop then IMMEDIATELY start — the start must really spawn (the old
	// race silently no-op'd it). Health must come back.
	stopStart := time.Now()
	if err := stopServerInternal(); err != nil {
		t.Fatalf("#28 stopServerInternal: %v", err)
	}
	t.Logf("#28 stop returned after %v (bounded exit wait)", time.Since(stopStart).Round(time.Millisecond))
	if err := startServerInternal(); err != nil {
		t.Fatalf("#28 startServerInternal after stop: %v", err)
	}
	serverMu.Lock()
	childDone2 := serverDone
	serverMu.Unlock()
	waitFor(t, "#28 /health after immediate restart", func() error {
		select {
		case <-childDone2:
			return fmt.Errorf("restarted llama-server exited; log tail:\n%s", e2eServerLogDump())
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
	}, 6*time.Minute)
	t.Logf("#28 OK: immediate stop->start really restarted the server")

	if err := stopServerInternal(); err != nil {
		t.Fatalf("final stop: %v", err)
	}
	t.Logf("final stop clean")
}
