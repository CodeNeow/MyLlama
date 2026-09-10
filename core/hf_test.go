package core

import (
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// hfModelsPayload is a mock response for searchHFMirrorAt: two models, one with a GGUF
// sibling and pipeline_tag sentence-similarity (embedding filter hit), the other without GGUF.
const hfModelsPayload = `[
  {
    "id": "Xorbits/bge-small-zh-v1.5",
    "modelId": "Xorbits/bge-small-zh-v1.5",
    "author": "Xorbits",
    "downloads": 1000,
    "likes": 10,
    "pipeline_tag": "sentence-similarity",
    "tags": ["sentence-transformers", "text-embeddings"],
    "siblings": [
      {"rfilename": "bge-small-zh-v1.5-q8_0.gguf", "size": 1024},
      {"rfilename": "config.json", "size": 500}
    ]
  },
  {
    "id": "org/no-gguf",
    "modelId": "org/no-gguf",
    "author": "org",
    "downloads": 5,
    "likes": 0,
    "pipeline_tag": "text-generation",
    "tags": [],
    "siblings": [{"rfilename": "model.safetensors", "size": 999}]
  }
]`

// newHFServer starts a local server simulating the HF API; baseURL can be injected into
// *At functions.
func newHFServer(t *testing.T) *httptest.Server {
	t.Helper()
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch r.URL.Path {
		case "/api/models":
			w.Write([]byte(hfModelsPayload))
		case "/api/models/Xorbits/bge-small-zh-v1.5":
			w.Write([]byte(`{"siblings":[
				{"rfilename":"bge-small-zh-v1.5-q8_0.gguf","size":1024},
				{"rfilename":"/model-f16.gguf","size":2048},
				{"rfilename":".hidden.gguf","size":1},
				{"rfilename":"README.md","size":10}
			]}`))
		default:
			w.WriteHeader(http.StatusNotFound)
		}
	}))
}

// TestSearchHFMirrorAt verifies HF search: models without GGUF are filtered out
// (hasGGUF filter), and the filter parameter no longer filters by pipeline_tag type.
func TestSearchHFMirrorAt(t *testing.T) {
	srv := newHFServer(t)
	defer srv.Close()

	results, err := searchHFMirrorAt(srv.URL, "bge", "embedding")
	if err != nil {
		t.Fatal(err)
	}
	if len(results) != 1 {
		t.Fatalf("search returned %d results, want 1", len(results))
	}
	if results[0].ID != "Xorbits/bge-small-zh-v1.5" {
		t.Errorf("ID = %q", results[0].ID)
	}
	if len(results[0].Siblings) != 2 {
		t.Errorf("Siblings = %d, want 2", len(results[0].Siblings))
	}
}

// TestSearchHFMirrorAtAllFilter verifies filter=all does not apply type filtering.
func TestSearchHFMirrorAtAllFilter(t *testing.T) {
	srv := newHFServer(t)
	defer srv.Close()

	results, err := searchHFMirrorAt(srv.URL, "bge", "all")
	if err != nil {
		t.Fatal(err)
	}
	if len(results) != 1 {
		t.Fatalf("filter=all result count = %d, want 1", len(results))
	}
}

// TestSearchHFMirrorAtHTTPError verifies a non-200 response returns an error.
func TestSearchHFMirrorAtHTTPError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusServiceUnavailable)
	}))
	defer srv.Close()

	if _, err := searchHFMirrorAt(srv.URL, "q", "all"); err == nil {
		t.Error("503 response should return error")
	}
}

// TestSearchHFMirrorAtFilterIgnored verifies that when filter="llm", pipeline_tag type
// filtering is no longer applied: both sentence-similarity (has GGUF) and
// text-generation (no GGUF) are retained within the filter range, but models without
// GGUF are still excluded by hasGGUF filtering, so only one model with GGUF is returned.
// This verifies the type-filter branch has been removed while hasGGUF filtering is retained.
func TestSearchHFMirrorAtFilterIgnored(t *testing.T) {
	srv := newHFServer(t)
	defer srv.Close()

	results, err := searchHFMirrorAt(srv.URL, "bge", "llm")
	if err != nil {
		t.Fatal(err)
	}
	if len(results) != 1 {
		t.Fatalf("filter=llm result count = %d, want 1 (type filtering should be ignored, only hasGGUF filtering active)", len(results))
	}
	if results[0].ID != "Xorbits/bge-small-zh-v1.5" {
		t.Errorf("ID = %q, want %q (text-generation model without GGUF should be filtered)",
			results[0].ID, "Xorbits/bge-small-zh-v1.5")
	}
}

// hfModelJSON constructs an HF /api/models list-item JSON; id is modelId; whether siblings
// contain gguf or safetensors files is determined by hasGGUF.
func hfModelJSON(id string, hasGGUF bool) string {
	file := `{"rfilename":"model.safetensors","size":999}`
	if hasGGUF {
		file = `{"rfilename":"model-q8_0.gguf","size":1024}`
	}
	return fmt.Sprintf(`{"id":%q,"modelId":%q,"author":"org","downloads":1,"likes":1,"pipeline_tag":"text-generation","tags":[],"siblings":[%s]}`,
		id, id, file)
}

// TestSearchHFMirrorAtMultiSortMerge verifies three-way sort (downloads / likes /
// lastModified) parallel fetch then merge/dedup in downloads → likes → lastModified order:
// different sort mocks return different sets; C without GGUF from downloads and E without
// GGUF from lastModified are filtered; B and D appearing in both likes and lastModified
// are kept only from the downloads path or first appearance. Assertion: merged = [A,B,D].
func TestSearchHFMirrorAtMultiSortMerge(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch r.URL.Query().Get("sort") {
		case "downloads":
			w.Write([]byte(fmt.Sprintf("[%s,%s,%s]",
				hfModelJSON("A", true), hfModelJSON("B", true), hfModelJSON("C", false))))
		case "likes":
			w.Write([]byte(fmt.Sprintf("[%s,%s]",
				hfModelJSON("B", true), hfModelJSON("D", true))))
		case "lastModified":
			w.Write([]byte(fmt.Sprintf("[%s,%s]",
				hfModelJSON("D", true), hfModelJSON("E", false))))
		default:
			w.WriteHeader(http.StatusBadRequest)
		}
	}))
	defer srv.Close()

	results, err := searchHFMirrorAt(srv.URL, "q", "all")
	if err != nil {
		t.Fatal(err)
	}
	var ids []string
	for _, r := range results {
		ids = append(ids, r.ModelID)
	}
	want := []string{"A", "B", "D"}
	if strings.Join(ids, ",") != strings.Join(want, ",") {
		t.Errorf("merged result = %v, want %v (downloads primary order, dedup by modelId, no-GGUF excluded)", ids, want)
	}
}

// TestSearchHFMirrorAtPartialSortFailure verifies that when a single sort path fails,
// the overall operation does not error: the likes path returning 500 is skipped, normal
// results from downloads / lastModified paths are retained and deduped by modelId,
// remaining [A].
func TestSearchHFMirrorAtPartialSortFailure(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		if r.URL.Query().Get("sort") == "likes" {
			w.WriteHeader(http.StatusInternalServerError)
			return
		}
		w.Write([]byte(fmt.Sprintf("[%s]", hfModelJSON("A", true))))
	}))
	defer srv.Close()

	results, err := searchHFMirrorAt(srv.URL, "q", "all")
	if err != nil {
		t.Fatal(err)
	}
	if len(results) != 1 || results[0].ModelID != "A" {
		t.Errorf("single-path failure should retain normal-path results, got %+v", results)
	}
}

// newReadmeServer starts a local server simulating README, used by getModelDescriptionAt
// tests to inject baseURL. Return values are distinguished by path: a full README
// (front-matter + headings + paragraphs), a long-paragraph README (210 runes, verifies
// no excerpt truncation), a huge README (verifies the readmeMaxBytes download cap),
// a front-matter-only README (empty description), and non-existent path (404).
func newReadmeServer(t *testing.T) *httptest.Server {
	t.Helper()
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/author/model/raw/main/README.md":
			w.Write([]byte("---\nlicense: apache-2.0\n---\n\n# 标题\n\n第一段自然语言描述，用于验证 front-matter 跳过。\n\n## 子标题\n\n第二段属于子标题之下，也必须完整返回。\n"))
		case "/author/longmodel/raw/main/README.md":
			// construct a paragraph of 210 runes; the full body is the description,
			// so it must come back complete (no 200-rune excerpt)
			para := strings.Repeat("长描述", 70) // 210 runes
			w.Write([]byte("---\ntags: test\n---\n\n# 标题\n\n" + para + "\n"))
		case "/author/huge/raw/main/README.md":
			// body far beyond the (test-shrunk) cap, no front-matter
			w.Write([]byte(strings.Repeat("x", 200)))
		case "/author/empty/raw/main/README.md":
			// README that is empty after front-matter skipping: silent empty description
			w.Write([]byte("---\nlicense: mit\n---"))
		default:
			w.WriteHeader(http.StatusNotFound)
		}
	}))
}

// TestGetModelDescriptionAt verifies the FULL README body is served as the
// description: YAML front-matter is dropped, everything after it (headings and
// all paragraphs) is returned unchanged.
func TestGetModelDescriptionAt(t *testing.T) {
	srv := newReadmeServer(t)
	defer srv.Close()

	desc, err := getModelDescriptionAt(srv.URL, "author/model")
	if err != nil {
		t.Fatal(err)
	}
	want := "\n# 标题\n\n第一段自然语言描述，用于验证 front-matter 跳过。\n\n## 子标题\n\n第二段属于子标题之下，也必须完整返回。\n"
	if desc != want {
		t.Errorf("desc = %q, want full body after front-matter %q", desc, want)
	}
	if strings.Contains(desc, "---") || strings.Contains(desc, "license") {
		t.Errorf("desc must not contain front-matter content: %q", desc)
	}
}

// TestGetModelDescriptionAtLongBodyFull verifies the long 210-rune paragraph
// comes back complete: no 200-rune excerpt truncation on the HTTP path.
func TestGetModelDescriptionAtLongBodyFull(t *testing.T) {
	srv := newReadmeServer(t)
	defer srv.Close()

	desc, err := getModelDescriptionAt(srv.URL, "author/longmodel")
	if err != nil {
		t.Fatal(err)
	}
	want := "\n# 标题\n\n" + strings.Repeat("长描述", 70) + "\n"
	if desc != want {
		t.Errorf("desc = %q (len %d), want full untruncated body (len %d)", desc, len(desc), len(want))
	}
	if strings.HasSuffix(desc, "...") || strings.HasSuffix(desc, "…") {
		t.Errorf("long description must not be truncated: %q", desc)
	}
}

// TestGetModelDescriptionAtOverCap verifies the download-time readmeMaxBytes cap:
// with the var shrunk, a README beyond the cap is cut to the cap with one
// trailing "\n\n…" marker (LimitReader wiring exercised end to end).
func TestGetModelDescriptionAtOverCap(t *testing.T) {
	srv := newReadmeServer(t)
	defer srv.Close()

	orig := readmeMaxBytes
	readmeMaxBytes = 64
	defer func() { readmeMaxBytes = orig }()

	desc, err := getModelDescriptionAt(srv.URL, "author/huge")
	if err != nil {
		t.Fatal(err)
	}
	want := strings.Repeat("x", 64) + "\n\n…"
	if desc != want {
		t.Errorf("desc = %q (len %d), want capped %q (len %d)", desc, len(desc), want, len(want))
	}
}

// TestGetModelDescriptionAtNotFound verifies a 404 README response returns an error.
func TestGetModelDescriptionAtNotFound(t *testing.T) {
	srv := newReadmeServer(t)
	defer srv.Close()

	if _, err := getModelDescriptionAt(srv.URL, "author/missing"); err == nil {
		t.Error("404 response should return error")
	} else if !strings.Contains(err.Error(), "HTTP 404") {
		t.Errorf("error message should contain HTTP status code: %v", err)
	}
}

// TestGetModelDescriptionAtEmptyBody verifies that a README which is empty after
// front-matter skipping returns an empty string and nil error (silent handling,
// not treated as failure).
func TestGetModelDescriptionAtEmptyBody(t *testing.T) {
	srv := newReadmeServer(t)
	defer srv.Close()

	desc, err := getModelDescriptionAt(srv.URL, "author/empty")
	if err != nil {
		t.Fatal(err)
	}
	if desc != "" {
		t.Errorf("empty README body should return empty string, got %q", desc)
	}
}

// TestReadmeDescription is a table-driven offline test for the shared
// readmeDescription helper (pure function, no network):
//   - plain markdown passes through unchanged (no paragraph selection, no
//     excerpt truncation);
//   - YAML front-matter is dropped through the closing ---;
//   - HTML-heavy content passes through WITHOUT any mid-tag cutting in the
//     normal path.
//
// The readmeMaxBytes cap cases live in their own subtests because they shrink
// the package var (restored afterward).
func TestReadmeDescription(t *testing.T) {
	tests := []struct {
		name string
		body string
		want string
	}{
		{
			name: "plain markdown passes through unchanged",
			body: "# Title\n\nFirst paragraph.\n\n- list item\n\nSecond paragraph.\n",
			want: "# Title\n\nFirst paragraph.\n\n- list item\n\nSecond paragraph.\n",
		},
		{
			name: "YAML front-matter is skipped",
			body: "---\nlicense: apache-2.0\ntags: test\n---\n\n# Title\n\nBody after front-matter.\n",
			want: "\n# Title\n\nBody after front-matter.\n",
		},
		{
			name: "HTML-heavy body passes through without mid-tag cutting",
			body: "<p style=\"color:red\">Styled intro</p>\n\n<a href=\"https://example.com\">link</a>\n\n## Usage\n\nNormal text.\n",
			want: "<p style=\"color:red\">Styled intro</p>\n\n<a href=\"https://example.com\">link</a>\n\n## Usage\n\nNormal text.\n",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := readmeDescription(tt.body); got != tt.want {
				t.Errorf("readmeDescription() = %q, want %q", got, tt.want)
			}
		})
	}

	// Cap case: shrink readmeMaxBytes (restored after) and verify a body beyond
	// the cap is cut to the cap with one trailing "\n\n…" marker.
	t.Run("content beyond readmeMaxBytes is cut with trailing marker", func(t *testing.T) {
		orig := readmeMaxBytes
		readmeMaxBytes = 64
		defer func() { readmeMaxBytes = orig }()

		got := readmeDescription(strings.Repeat("x", 100))
		want := strings.Repeat("x", 64) + "\n\n…"
		if got != want {
			t.Errorf("readmeDescription() = %q (len %d), want %q (len %d)", got, len(got), want, len(want))
		}
	})

	// Boundary: a body of exactly readmeMaxBytes bytes is not cut.
	t.Run("body exactly at readmeMaxBytes is not cut", func(t *testing.T) {
		orig := readmeMaxBytes
		readmeMaxBytes = 64
		defer func() { readmeMaxBytes = orig }()

		body := strings.Repeat("y", 64)
		if got := readmeDescription(body); got != body {
			t.Errorf("readmeDescription() = %q, want unchanged %q", got, body)
		}
	})
}

// TestGetHFModelFilesAt verifies the model file list: only top-level .gguf files are
// returned, excess leading slashes are stripped, and directories and hidden files are ignored.
func TestGetHFModelFilesAt(t *testing.T) {
	srv := newHFServer(t)
	defer srv.Close()

	files, err := getHFModelFilesAt(srv.URL, "Xorbits/bge-small-zh-v1.5")
	if err != nil {
		t.Fatal(err)
	}
	if len(files) != 2 {
		t.Fatalf("file count = %d, want 2 (should exclude hidden files and README)", len(files))
	}
	for _, f := range files {
		if f.Filename == ".hidden.gguf" || f.Filename == "README.md" {
			t.Errorf("must not contain %q", f.Filename)
		}
	}
	// "/model-f16.gguf" should be stripped to "model-f16.gguf"
	hasTrimmed := false
	for _, f := range files {
		if f.Filename == "model-f16.gguf" {
			hasTrimmed = true
		}
	}
	if !hasTrimmed {
		t.Errorf("stripped model-f16.gguf not found: %+v", files)
	}
}

// TestGetHFModelMaxGGUFSizeAt verifies model maximum GGUF size aggregation: search card
// size requires the details endpoint (blobs=true for real size), takes the largest .gguf
// file (excluding hidden files and non-gguf). mock siblings: bge q8_0(1024),
// /model-f16.gguf(2048), .hidden.gguf(1), README.md(10) → maximum should be 2048.
func TestGetHFModelMaxGGUFSizeAt(t *testing.T) {
	srv := newHFServer(t)
	defer srv.Close()

	size, err := getHFModelMaxGGUFSizeAt(srv.URL, "Xorbits/bge-small-zh-v1.5")
	if err != nil {
		t.Fatal(err)
	}
	if size != 2048 {
		t.Errorf("max GGUF size = %d, want 2048 (should exclude hidden files and README, take model-f16.gguf)", size)
	}
}

// TestGetHFModelMaxGGUFSizeAtNoGGUF verifies that when a model has no GGUF files,
// 0 and nil are returned (not treated as error; frontend silently does not display size).
func TestGetHFModelMaxGGUFSizeAtNoGGUF(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.Write([]byte(`{"siblings":[
			{"rfilename":"model.safetensors","size":999},
			{"rfilename":"README.md","size":10}
		]}`))
	}))
	defer srv.Close()

	size, err := getHFModelMaxGGUFSizeAt(srv.URL, "org/no-gguf")
	if err != nil {
		t.Fatal(err)
	}
	if size != 0 {
		t.Errorf("no GGUF size = %d, want 0", size)
	}
}

// TestGetHFModelMaxGGUFSizeAtHTTPError verifies a non-200 details endpoint response
// returns an error.
func TestGetHFModelMaxGGUFSizeAtHTTPError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusNotFound)
	}))
	defer srv.Close()

	if _, err := getHFModelMaxGGUFSizeAt(srv.URL, "org/missing"); err == nil {
		t.Error("404 response should return error")
	}
}

// TestBuildModelDownloadURLEscapesRepoPath verifies the modelID is escaped in
// the HF download-URL branches: a normal {author}/{name} id passes through
// byte-identical (the separating slash must stay a literal slash — a whole-id
// PathEscape would send %2F and break the route), while query/fragment
// metacharacters smuggled in a hostile id (e.g. from a search API response)
// are percent-escaped so they cannot reshape the request URL.
func TestBuildModelDownloadURLEscapesRepoPath(t *testing.T) {
	// Normal ids unchanged on both HF sources.
	for _, source := range []string{"hf", sourceHuggingFace} {
		base := hfMirrorBase
		if source == sourceHuggingFace {
			base = hfDirectBase
		}
		u, err := buildModelDownloadURL(source, "author/model", "a.gguf")
		if err != nil {
			t.Fatal(err)
		}
		want := base + "/author/model/resolve/main/a.gguf"
		if u != want {
			t.Errorf("source %q: URL = %q, want unchanged %q", source, u, want)
		}
	}

	// Hostile ids: query/fragment/percent metacharacters escaped per segment.
	cases := []struct {
		modelID string
		wantSeg string
	}{
		{"author/evil?x=1#f", "author/evil%3Fx=1%23f"},
		{"a/b%c", "a/b%25c"},
		{"my models/x y", "my%20models/x%20y"},
	}
	for _, tc := range cases {
		u, err := buildModelDownloadURL("hf", tc.modelID, "a.gguf")
		if err != nil {
			t.Fatal(err)
		}
		want := hfMirrorBase + "/" + tc.wantSeg + "/resolve/main/a.gguf"
		if u != want {
			t.Errorf("modelID %q: URL = %q, want escaped %q", tc.modelID, u, want)
		}
	}
}

// TestSearchHFMirrorSortAtEscapesQuery verifies the search term is
// query-escaped: a term containing spaces, "&", "?" and "#" must arrive at the
// server intact as the search parameter instead of being split into extra
// parameters or truncated at the fragment.
func TestSearchHFMirrorSortAtEscapesQuery(t *testing.T) {
	var gotSearch string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotSearch = r.URL.Query().Get("search")
		w.Write([]byte("[" + hfModelJSON("A", true) + "]"))
	}))
	defer srv.Close()

	q := `bge small&fake=1?x#anchor`
	if _, err := searchHFMirrorSortAt(srv.URL, q, "downloads"); err != nil {
		t.Fatal(err)
	}
	if gotSearch != q {
		t.Errorf("server saw search = %q, want the full escaped term %q", gotSearch, q)
	}
}

// TestHFJSONResponseSizeCap verifies the size cap on HF API JSON responses:
// with hfJSONMaxBytes shrunk, a valid JSON body beyond the cap fails through
// the existing decode-error path (the truncated stream is malformed JSON),
// while a body within the cap (the +1 budget keeps an exactly-at-cap payload
// whole) decodes normally.
func TestHFJSONResponseSizeCap(t *testing.T) {
	filesPayload := `{"siblings":[{"rfilename":"model-q8_0.gguf","size":1024}]}`
	searchPayload := "[" + hfModelJSON("A", true) + "]"
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		if r.URL.Path == "/api/models" {
			w.Write([]byte(searchPayload))
			return
		}
		w.Write([]byte(filesPayload))
	}))
	defer srv.Close()

	orig := hfJSONMaxBytes
	defer func() { hfJSONMaxBytes = orig }()

	// Beyond the (shrunk) cap: decode must fail on both endpoints.
	hfJSONMaxBytes = 16
	if _, err := getHFModelFilesAt(srv.URL, "author/model"); err == nil {
		t.Error("file-list response beyond the JSON cap should fail to decode")
	}
	if _, err := searchHFMirrorSortAt(srv.URL, "q", "downloads"); err == nil {
		t.Error("search response beyond the JSON cap should fail to decode")
	}

	// Within the cap: decode succeeds.
	hfJSONMaxBytes = int64(len(filesPayload))
	files, err := getHFModelFilesAt(srv.URL, "author/model")
	if err != nil {
		t.Fatalf("file-list response at the cap should decode: %v", err)
	}
	if len(files) != 1 || files[0].Filename != "model-q8_0.gguf" {
		t.Errorf("files = %+v, want the single q8_0 gguf", files)
	}
	hfJSONMaxBytes = int64(len(searchPayload))
	if _, err := searchHFMirrorSortAt(srv.URL, "q", "downloads"); err != nil {
		t.Errorf("search response at the cap should decode: %v", err)
	}
}
