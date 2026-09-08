package core

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"time"
)

// ─── Router API (llama-server router mode) ─────────────────────────
//
// Types and functions wrapping the llama-server router HTTP API (GET /models,
// POST /models/unload) so the frontend TaskDock can list in-memory models and
// unload them.

// LoadedModel represents a model entry currently loaded / loading / sleeping
// in the router.
type LoadedModel struct {
	ID     string `json:"id"`     // Model ID (matches API response id)
	Type   string `json:"type"`   // Model type: chat | audio | image | video
	Status string `json:"status"` // loaded | loading | sleeping
}

// routerModelsResponse mirrors the llama-server GET /models router-shape JSON
// structure. Status is a pointer so a missing / null status field (the native
// OpenAI shape served by newer direct-mode builds) is distinguishable from a
// present one.
type routerModelsResponse struct {
	Data []routerModelItem `json:"data"`
}

type routerModelItem struct {
	ID           string             `json:"id"`
	Path         string             `json:"path"`
	Status       *routerModelStatus `json:"status"`
	Architecture routerModelArch    `json:"architecture"`
}

type routerModelStatus struct {
	Value string   `json:"value"` // unloaded / loading / loaded / sleeping / downloading / failed
	Args  []string `json:"args"`
}

type routerModelArch struct {
	InputModalities  []string `json:"input_modalities"`
	OutputModalities []string `json:"output_modalities"`
}

// routerUnloadRequest is the request body for POST /models/unload.
type routerUnloadRequest struct {
	Model string `json:"model"`
}

// routerUnloadResponse is the response body for POST /models/unload.
type routerUnloadResponse struct {
	Success bool `json:"success"`
}

// ─── URL injection point ───────────────────────────────────────────
//
// routerBaseURL is declared as a package-level var so tests can swap in a
// local httptest server (same style as githubReleasesAPI / updateRepoAPI).
var routerBaseURL = func(port int) string {
	return fmt.Sprintf("http://127.0.0.1:%d", port)
}

// ─── Model type classification ─────────────────────────────────────

// classifyModelType determines the model type from output_modalities: audio
// if audio is present, image if image, video if video, otherwise chat. Chat
// models with vision input (e.g. LLaVA) output text, so they naturally fall
// into chat.
func classifyModelType(outputModalities []string) string {
	for _, m := range outputModalities {
		switch m {
		case "audio":
			return "audio"
		case "image":
			return "image"
		case "video":
			return "video"
		}
	}
	return "chat"
}

// ─── Query / Unload ────────────────────────────────────────────────

// fetchRouterModels queries the llama-server router for the model list,
// filters to loaded / loading / sleeping entries, and maps them to a
// LoadedModel slice. GET /models is parsed tolerantly across two response
// shapes (parseModelsBody); a 404 (direct-mode builds without any router
// route) falls back to the OpenAI-compatible /v1/models listing, where every
// served model is by definition loaded.
func fetchRouterModels(port int) ([]LoadedModel, error) {
	base := routerBaseURL(port)
	url := base + "/models"

	client := &http.Client{Timeout: 3 * time.Second}
	resp, err := client.Get(url)
	if err != nil {
		return nil, fmt.Errorf("fetch router models: %w", err)
	}
	defer resp.Body.Close()

	// Direct-mode fallback: the router /models route is absent (404) on
	// servers started with -m instead of a models preset.
	if resp.StatusCode == http.StatusNotFound {
		return fetchOpenAIModels(client, base)
	}

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("fetch router models: HTTP %d", resp.StatusCode)
	}

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("fetch router models: %w", err)
	}
	return parseModelsBody(body)
}

// parseModelsBody decodes a GET /models 200 body that may come in either of
// two shapes:
//
//   - router shape: data[] entries with id + status{value} + architecture
//     (router-mode llama-server);
//   - native OpenAI shape: data[] entries with id only, plus object:"model"
//     style fields — how newer llama.cpp builds answer /models directly even
//     when started with a single -m model (the 404 signal no longer holds).
//
// Router entries keep the loaded / loading / sleeping filter, so a router
// response with nothing resident still yields an empty list. Entries without
// a usable status (native listing, missing / null status, or a future
// upstream status-shape change) are treated leniently as resident chat
// models — a 200 with data entries always means the models are served and
// hence in memory. When an entry's status fails to decode as an object (e.g.
// a plain string), the router-shape pass errors and the same body is re-read
// with the native shape. Only a genuinely empty / absent data array, from
// both passes, yields an empty result.
func parseModelsBody(body []byte) ([]LoadedModel, error) {
	// First pass: router shape.
	var routerRaw routerModelsResponse
	routerErr := json.Unmarshal(body, &routerRaw)
	if routerErr == nil && len(routerRaw.Data) > 0 {
		out := make([]LoadedModel, 0, len(routerRaw.Data))
		for _, item := range routerRaw.Data {
			status := ""
			if item.Status != nil {
				status = item.Status.Value
			}
			if status == "" {
				// Native listing or status shape change: resident.
				out = append(out, LoadedModel{ID: item.ID, Type: "chat", Status: "loaded"})
				continue
			}
			switch status {
			case "loaded", "loading", "sleeping":
				out = append(out, LoadedModel{
					ID:     item.ID,
					Type:   classifyModelType(item.Architecture.OutputModalities),
					Status: status,
				})
			}
		}
		return out, nil
	}

	// Second pass: native OpenAI shape — reached when the router shape failed
	// to decode (e.g. status is not an object) or data was empty / absent.
	var nativeRaw struct {
		Data []struct {
			ID string `json:"id"`
		} `json:"data"`
	}
	nativeErr := json.Unmarshal(body, &nativeRaw)
	if nativeErr == nil && len(nativeRaw.Data) > 0 {
		out := make([]LoadedModel, 0, len(nativeRaw.Data))
		for _, item := range nativeRaw.Data {
			out = append(out, LoadedModel{ID: item.ID, Type: "chat", Status: "loaded"})
		}
		return out, nil
	}

	// Both passes failed to decode: surface the decode error (garbage body).
	// Otherwise both saw an empty data array: router mode with nothing loaded.
	if routerErr != nil && nativeErr != nil {
		return nil, fmt.Errorf("fetch router models: %w", routerErr)
	}
	return []LoadedModel{}, nil
}

// fetchOpenAIModels maps GET /v1/models data[].id entries to LoadedModel
// values (type chat, status loaded) — the direct-mode fallback for
// router-unaware servers.
func fetchOpenAIModels(client *http.Client, base string) ([]LoadedModel, error) {
	resp, err := client.Get(base + "/v1/models")
	if err != nil {
		return nil, fmt.Errorf("fetch router models: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("fetch router models: HTTP %d", resp.StatusCode)
	}

	var raw struct {
		Data []struct {
			ID string `json:"id"`
		} `json:"data"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&raw); err != nil {
		return nil, fmt.Errorf("fetch router models: %w", err)
	}

	out := make([]LoadedModel, 0, len(raw.Data))
	for _, item := range raw.Data {
		out = append(out, LoadedModel{ID: item.ID, Type: "chat", Status: "loaded"})
	}
	return out, nil
}

// unloadRouterModel sends a model unload request to llama-server. Measured
// contract (real b10342 and the pinned b10695, router mode with a models
// preset): POST /models/unload {"model":<id>} answers 200 {"success":true}
// for a resident model; 400 with a structured error object when the model is
// not running / unknown; 404 on direct-mode servers (-m, no preset — no
// router routes at all), which surfaces the guided "stop the service instead"
// message below. No newer alternative unload endpoint exists upstream at the
// pin, so no dual-path attempt is made.
func unloadRouterModel(port int, id string) error {
	if id == "" {
		return errors.New("unload router model: empty model id")
	}

	base := routerBaseURL(port)
	url := base + "/models/unload"

	body := routerUnloadRequest{Model: id}
	payload, err := json.Marshal(body)
	if err != nil {
		return fmt.Errorf("unload router model: %w", err)
	}

	client := &http.Client{Timeout: 3 * time.Second}
	resp, err := client.Post(url, "application/json", bytes.NewReader(payload))
	if err != nil {
		return fmt.Errorf("unload router model: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		// Direct-mode llama-server has no /models/unload route (404): the
		// single resident model can only leave memory by stopping the
		// service — surface that instead of the raw HTTP status.
		if resp.StatusCode == http.StatusNotFound {
			return fmt.Errorf("unload router model: %s", tr("直连模式不支持卸载，请停止服务", "direct mode: unload not supported, stop the service instead"))
		}
		var errResp struct {
			Error json.RawMessage `json:"error"`
		}
		_ = json.NewDecoder(resp.Body).Decode(&errResp)
		if msg := unloadErrorMessage(errResp.Error); msg != "" {
			return fmt.Errorf("unload router model: %s (HTTP %d)", msg, resp.StatusCode)
		}
		return fmt.Errorf("unload router model: HTTP %d", resp.StatusCode)
	}

	var result routerUnloadResponse
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		return fmt.Errorf("unload router model: %w", err)
	}
	if !result.Success {
		return fmt.Errorf("unload router model: server returned success=false")
	}
	return nil
}

// unloadErrorMessage extracts the human-readable message from a llama-server
// error body's "error" field, which has shipped in two shapes: a plain string
// ("error":"not found") and a structured object
// ("error":{"code":400,"message":"model is not running","type":...} — the
// shape measured on real b10342 / b10695 router-mode servers, e.g. when the
// target model is not resident or unknown). Empty input or an unrecognized
// shape (number, array, garbage) yields "" so the caller can fall back to the
// bare HTTP status.
func unloadErrorMessage(raw json.RawMessage) string {
	if len(raw) == 0 {
		return ""
	}
	var s string
	if err := json.Unmarshal(raw, &s); err == nil {
		return s
	}
	var obj struct {
		Message string `json:"message"`
	}
	if err := json.Unmarshal(raw, &obj); err == nil {
		return obj.Message
	}
	return ""
}

// getServerPort returns the currently recorded server port (0 means not
// running); safe for concurrent use.
func getServerPort() int {
	serverMu.Lock()
	port := serverPort
	serverMu.Unlock()
	return port
}

// setServerPort sets the current server port (called on successful start; 0
// means not running).
func setServerPort(port int) {
	serverMu.Lock()
	serverPort = port
	serverMu.Unlock()
}
