package core

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"time"
)

// ─── HF-compatible model source API ─────────────────────────────
// Search, file listing and README description against hf-mirror.com /
// huggingface.co, plus model download URL construction per download source.

type HFSearchResult struct {
	ID          string   `json:"id"`
	ModelID     string   `json:"modelId"`
	Author      string   `json:"author"`
	Downloads   int      `json:"downloads"`
	Likes       int      `json:"likes"`
	PipelineTag string   `json:"pipelineTag"`
	Tags        []string `json:"tags"`
	Siblings    []HFFile `json:"siblings"`
}

type HFFile struct {
	Filename string `json:"rfilename"`
	Size     int64  `json:"size"`
}

// HFFileOut is the frontend-facing file info with `filename` JSON key.
type HFFileOut struct {
	Filename string `json:"filename"`
	Size     int64  `json:"size"`
}

const hfMirrorBase = "https://hf-mirror.com"
const hfDirectBase = "https://huggingface.co"

// hfJSONMaxBytes caps how many bytes of an HF API JSON response (search pages
// and model file lists) the decoder will read. Declared as a var (same style
// as readmeMaxBytes) so tests can shrink it. 16 MiB is orders of magnitude
// above any real response — a limit=200&full=true search page or a model's
// full sibling list stays well under 1 MiB — while bounding what a hostile or
// misbehaving mirror can make the process buffer. A response beyond the cap
// fails through the existing error path: the reader is truncated mid-JSON, so
// Decode returns an unexpected-EOF style error like any other malformed
// payload.
var hfJSONMaxBytes int64 = 16 << 20

// decodeHFJSON decodes one capped HF API JSON response body into v. The
// LimitReader budget is hfJSONMaxBytes+1 so an exactly-at-cap payload still
// decodes whole while anything larger truncates and errors.
func decodeHFJSON(body io.Reader, v interface{}) error {
	return json.NewDecoder(io.LimitReader(body, hfJSONMaxBytes+1)).Decode(v)
}

// activeHFBase returns the HF-compatible API base for the active non-ModelScope
// source: the official Hugging Face host for "huggingface", otherwise the
// hf-mirror.com mirror. Both expose identical Hub API paths, so the same
// request code serves either host.
func activeHFBase() string {
	if activeDownloadSource() == sourceHuggingFace {
		return hfDirectBase
	}
	return hfMirrorBase
}

// escapeRepoPath escapes a model repo id for use inside an HF-compatible URL
// path: each slash-separated segment is percent-escaped (url.PathEscape) and
// the segments are rejoined with a literal "/". The separating slash must stay
// unescaped because HF-compatible routes address a model as
// {namespace}/{name} — a whole-id url.PathEscape would send "%2F" and break
// the route. Every other metacharacter, including "?", "#" and "%" that would
// rewrite the URL's query or fragment, is escaped inside its segment, so a
// hostile modelID coming from a search API response cannot reshape the
// request beyond the {namespace}/{name} shape. (The ModelScope branch differs
// on purpose: its legacy API accepts the %2F form, so it keeps the existing
// whole-id url.PathEscape — both branches neutralize injection, each in the
// form its endpoint accepts.)
func escapeRepoPath(modelID string) string {
	segs := strings.Split(modelID, "/")
	for i, s := range segs {
		segs[i] = url.PathEscape(s)
	}
	return strings.Join(segs, "/")
}

// buildModelDownloadURL builds the model file download URL per download source:
//   - hf: {hfMirrorBase}/{modelID}/resolve/main/{fileName} (modelID segment-escaped, filename PathEscaped)
//   - huggingface: same path on the official Hugging Face host (hfDirectBase)
//   - modelscope: delegates to buildModelScopeDownloadURL (the legacy API repo endpoint)
//   - unknown source returns an error (defense in depth; callers must not pass invalid values)
func buildModelDownloadURL(source, modelID, fileName string) (string, error) {
	switch source {
	case sourceHF:
		return fmt.Sprintf("%s/%s/resolve/main/%s", hfMirrorBase, escapeRepoPath(modelID), url.PathEscape(fileName)), nil
	case sourceHuggingFace:
		return fmt.Sprintf("%s/%s/resolve/main/%s", hfDirectBase, escapeRepoPath(modelID), url.PathEscape(fileName)), nil
	case sourceModelScope:
		return buildModelScopeDownloadURL(modelscopeLegacyBase, modelID, fileName), nil
	default:
		return "", fmt.Errorf(tr("未知下载源 %q", "unknown download source %q"), source)
	}
}

// searchHFMirror queries the default HF Mirror endpoint.
func searchHFMirror(q string, filter string) ([]HFSearchResult, error) {
	return searchHFMirrorAt(activeHFBase(), q, filter)
}

// searchHFMirrorAt queries an HF-compatible API base for models matching q,
// filtering to models containing GGUF files. The filter parameter is
// deprecated, kept only for signature compatibility; no pipeline_tag type
// filtering happens anymore (embedding / llm classification was removed).
// The API supports neither library filtering nor pagination, so candidates
// are pulled with a large limit and then filtered for GGUF. To cover as many
// candidates as possible, three sorts — downloads / likes / lastModified —
// are requested in parallel (each limit=200&full=true), each filtered for
// GGUF and then merged and deduplicated by modelId in downloads → likes →
// lastModified order (already-seen entries skipped). A failed sort request
// only skips that route (with a [WARN]); an error is returned only when all
// three routes fail.
func searchHFMirrorAt(baseURL, q, filter string) ([]HFSearchResult, error) {
	sorts := []string{"downloads", "likes", "lastModified"}

	type routeResult struct {
		results []HFSearchResult
		err     error
	}
	routeResults := make([]routeResult, len(sorts))

	// Three sort routes fetched in parallel, each with its own result slice;
	// no shared writes
	var wg sync.WaitGroup
	for i, sort := range sorts {
		wg.Add(1)
		go func(i int, sort string) {
			defer wg.Done()
			routeResults[i].results, routeResults[i].err = searchHFMirrorSortAt(baseURL, q, sort)
		}(i, sort)
	}
	wg.Wait()

	var results []HFSearchResult
	seen := make(map[string]bool)
	failed := 0
	for i, sort := range sorts {
		if routeResults[i].err != nil {
			failed++
			log.Printf("[WARN] HF search sort %s request failed, skipping route: %v", sort, routeResults[i].err)
			continue
		}
		for _, r := range routeResults[i].results {
			key := r.ModelID
			if key == "" {
				key = r.ID
			}
			if seen[key] {
				continue
			}
			seen[key] = true
			results = append(results, r)
		}
	}

	if failed == len(sorts) {
		return nil, errors.New(tr("HF 搜索三路排序（downloads/likes/lastModified）请求全部失败", "all three HF search sort routes (downloads/likes/lastModified) failed"))
	}
	return results, nil
}

// searchHFMirrorSortAt fetches one page of candidates from an HF-compatible
// API with the given sort (limit=200&full=true), filtering for results with
// GGUF files. Request failures or non-200 statuses return an error;
// searchHFMirrorAt then decides to skip the whole route (other sorts are
// unaffected).
func searchHFMirrorSortAt(baseURL, q, sort string) ([]HFSearchResult, error) {
	// QueryEscape the user search term: raw spaces, "&", "?", "#" etc. would
	// otherwise corrupt the query string (splitting/injecting parameters or
	// truncating the term at the fragment).
	apiURL := fmt.Sprintf("%s/api/models?search=%s&sort=%s&limit=200&full=true", baseURL, url.QueryEscape(q), sort)

	req, err := http.NewRequest("GET", apiURL, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("User-Agent", appUserAgent())

	client := &http.Client{Timeout: 30 * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("HF API returned status %d", resp.StatusCode)
	}

	var rawResults []struct {
		ID          string   `json:"id"`
		ModelID     string   `json:"modelId"`
		Author      string   `json:"author"`
		Downloads   int      `json:"downloads"`
		Likes       int      `json:"likes"`
		PipelineTag string   `json:"pipeline_tag"`
		Tags        []string `json:"tags"`
		Siblings    []HFFile `json:"siblings"`
	}
	if err := decodeHFJSON(resp.Body, &rawResults); err != nil {
		return nil, err
	}

	var results []HFSearchResult
	for _, r := range rawResults {
		result := HFSearchResult{
			ID:          r.ID,
			ModelID:     r.ModelID,
			Author:      r.Author,
			Downloads:   r.Downloads,
			Likes:       r.Likes,
			PipelineTag: r.PipelineTag,
			Tags:        r.Tags,
			Siblings:    r.Siblings,
		}

		// Only include models that have .gguf files
		if !hasGGUF(result) {
			continue
		}

		results = append(results, result)
	}

	return results, nil
}

// hasGGUF reports whether an HF search result contains a .gguf file (the GGUF
// filter for HF search candidates).
func hasGGUF(r HFSearchResult) bool {
	for _, s := range r.Siblings {
		if strings.HasSuffix(strings.ToLower(s.Filename), ".gguf") {
			return true
		}
	}
	return false
}

// getModelDescription fetches a model's README description via the default mirror.
func getModelDescription(modelID string) (string, error) {
	return getModelDescriptionAt(activeHFBase(), modelID)
}

// getModelDescriptionAt fetches the README of a model on an HF-compatible base
// and returns its full body as the model description:
//   - GET {base}/{modelID}/raw/main/README.md (User-Agent via appUserAgent(), 30s timeout)
//   - non-200 returns an error
//   - the body is read up to readmeMaxBytes+1 bytes (overflow detection) and
//     passed through the shared readmeDescription helper (front-matter skip +
//     size cap; no paragraph selection or excerpt truncation)
func getModelDescriptionAt(baseURL, modelID string) (string, error) {
	readmeURL := fmt.Sprintf("%s/%s/raw/main/README.md", baseURL, modelID)

	req, err := http.NewRequest("GET", readmeURL, nil)
	if err != nil {
		return "", err
	}
	req.Header.Set("User-Agent", appUserAgent())

	client := &http.Client{Timeout: 30 * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf(tr("README 获取失败: HTTP %d", "failed to fetch README: HTTP %d"), resp.StatusCode)
	}

	// Read at most readmeMaxBytes+1 bytes so an over-cap README is detected
	// without buffering an unbounded response.
	body, err := io.ReadAll(io.LimitReader(resp.Body, readmeMaxBytes+1))
	if err != nil {
		return "", err
	}

	return readmeDescription(string(body)), nil
}

// readmeMaxBytes caps the README body served as a model description: 512 KiB is
// far above any real README while keeping the description bounded against
// abuse. Declared as a var (same injection-point style as cmdTimeout /
// docsMaxBytes) so tests can shrink it.
var readmeMaxBytes int64 = 512 * 1024

// readmeDescription turns a fetched README body into the model description
// (shared by HF and ModelScope). The FULL body is the description — no
// paragraph selection and no 200-rune excerpt truncation — so users always see
// the complete document (HTML-first READMEs are no longer cut mid-tag):
//   - when the body exceeds readmeMaxBytes (callers read up to
//     readmeMaxBytes+1 bytes to detect overflow), cut it to readmeMaxBytes and
//     append one trailing "\n\n…" marker. A 512 KiB cut landing mid-HTML-tag is
//     accepted: the frontend strips tags and this only triggers on
//     pathologically huge READMEs.
//   - skip YAML front-matter: when the first line trims to ---, drop
//     everything through the closing ---
func readmeDescription(body string) string {
	if int64(len(body)) > readmeMaxBytes {
		body = body[:readmeMaxBytes] + "\n\n…"
	}

	lines := strings.Split(body, "\n")
	start := 0
	// Skip YAML front-matter: when the first line trims to ---, drop everything
	// through the next ---
	if len(lines) > 0 && strings.TrimSpace(lines[0]) == "---" {
		for i := 1; i < len(lines); i++ {
			if strings.TrimSpace(lines[i]) == "---" {
				start = i + 1
				break
			}
		}
	}
	return strings.Join(lines[start:], "\n")
}

// getHFModelFiles lists downloadable GGUF files for a model via the default mirror.
func getHFModelFiles(modelID string) ([]HFFileOut, error) {
	return getHFModelFilesAt(activeHFBase(), modelID)
}

// getHFModelFilesAt lists the GGUF siblings of a model on an HF-compatible API base.
// blobs=true makes the API return real file sizes (HF search/detail APIs do
// not include size on siblings by default).
func getHFModelFilesAt(baseURL, modelID string) ([]HFFileOut, error) {
	apiURL := fmt.Sprintf("%s/api/models/%s?blobs=true", baseURL, modelID)

	req, err := http.NewRequest("GET", apiURL, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("User-Agent", appUserAgent())

	client := &http.Client{Timeout: 30 * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("HF API returned status %d", resp.StatusCode)
	}

	var raw struct {
		Siblings []HFFile `json:"siblings"`
	}
	if err := decodeHFJSON(resp.Body, &raw); err != nil {
		return nil, err
	}

	// Filter to only .gguf files
	var files []HFFileOut
	for _, s := range raw.Siblings {
		name := strings.TrimPrefix(s.Filename, "/")
		if !strings.HasSuffix(name, "/") && !strings.HasPrefix(name, ".") && strings.HasSuffix(strings.ToLower(name), ".gguf") {
			files = append(files, HFFileOut{Filename: name, Size: s.Size})
		}
	}

	return files, nil
}

// getHFModelMaxGGUFSize returns the size of the model's largest GGUF file
// (via the default mirror).
func getHFModelMaxGGUFSize(modelID string) (int64, error) {
	return getHFModelMaxGGUFSizeAt(activeHFBase(), modelID)
}

// getHFModelMaxGGUFSizeAt queries the model detail API (blobs=true is required
// for real sizes) and returns the size of the model's largest .gguf file; 0
// and nil when there is no GGUF. The HF search API's siblings carry no size
// (empirically all null), so model sizes on search cards can only be fetched
// one by one via the detail API by modelId; the largest file is used instead
// of the sum of all GGUFs to avoid the inflated totals of multi-quant models
// (dozens of quantized files) misleading users about model scale.
func getHFModelMaxGGUFSizeAt(baseURL, modelID string) (int64, error) {
	apiURL := fmt.Sprintf("%s/api/models/%s?blobs=true", baseURL, modelID)

	req, err := http.NewRequest("GET", apiURL, nil)
	if err != nil {
		return 0, err
	}
	req.Header.Set("User-Agent", appUserAgent())

	client := &http.Client{Timeout: 30 * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		return 0, err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return 0, fmt.Errorf("HF API returned status %d", resp.StatusCode)
	}

	var raw struct {
		Siblings []HFFile `json:"siblings"`
	}
	if err := decodeHFJSON(resp.Body, &raw); err != nil {
		return 0, err
	}

	var max int64
	for _, s := range raw.Siblings {
		name := strings.TrimPrefix(s.Filename, "/")
		if strings.HasSuffix(name, "/") || strings.HasPrefix(name, ".") || !strings.HasSuffix(strings.ToLower(name), ".gguf") {
			continue
		}
		if s.Size > max {
			max = s.Size
		}
	}
	return max, nil
}
