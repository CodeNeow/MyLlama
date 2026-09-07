package core

import (
	"encoding/binary"
	"encoding/json"
	"io"
	"math"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// ─── Test fixtures: minimal GGUF header bytes ───────────────────

// testKV is one GGUF metadata key/value pair written by writeTestGGUF.
type testKV struct {
	key string
	typ uint32
	str string
	num float64
}

// writeTestGGUF serializes a minimal GGUF v3 header (magic, version, zero
// tensors, kv pairs) into path — just enough bytes for the metadata readers,
// no tensor data.
func writeTestGGUF(t *testing.T, path string, kvs []testKV) {
	t.Helper()
	var buf []byte
	magic := []byte("GGUF")
	buf = append(buf, magic...)
	buf = binary.LittleEndian.AppendUint32(buf, 3) // version
	buf = binary.LittleEndian.AppendUint64(buf, 0) // tensor count
	buf = binary.LittleEndian.AppendUint64(buf, uint64(len(kvs)))
	for _, kv := range kvs {
		buf = binary.LittleEndian.AppendUint64(buf, uint64(len(kv.key)))
		buf = append(buf, kv.key...)
		buf = binary.LittleEndian.AppendUint32(buf, kv.typ)
		switch kv.typ {
		case 8: // string
			buf = binary.LittleEndian.AppendUint64(buf, uint64(len(kv.str)))
			buf = append(buf, kv.str...)
		case 4: // uint32
			buf = binary.LittleEndian.AppendUint32(buf, uint32(kv.num))
		case 6: // float32
			buf = binary.LittleEndian.AppendUint32(buf, math.Float32bits(float32(kv.num)))
		case 12: // float64
			buf = binary.LittleEndian.AppendUint64(buf, math.Float64bits(kv.num))
		default:
			t.Fatalf("unsupported test kv type %d", kv.typ)
		}
	}
	if err := os.WriteFile(path, buf, 0644); err != nil {
		t.Fatal(err)
	}
}

// loraAdapterKVs builds the metadata key set convert_lora_to_gguf.py writes
// (general.type "adapter" + adapter.type "lora" + optional alpha/arch).
func loraAdapterKVs(alpha float64, hasAlpha bool, arch string) []testKV {
	kvs := []testKV{
		{key: "general.type", typ: 8, str: "adapter"},
		{key: "adapter.type", typ: 8, str: "lora"},
	}
	if arch != "" {
		kvs = append(kvs, testKV{key: "general.architecture", typ: 8, str: arch})
	}
	if hasAlpha {
		kvs = append(kvs, testKV{key: "adapter.lora.alpha", typ: 6, num: alpha})
	}
	return kvs
}

// ─── scanLoraAdapters ─────────────────────────────────────────────

// withLoraDirReset snapshots the loraDirOverride global and restores it after
// the test (saveConfigState does not cover the LoRA directory override).
func withLoraDirReset(t *testing.T) {
	t.Helper()
	loraDirMu.Lock()
	orig := loraDirOverride
	loraDirMu.Unlock()
	t.Cleanup(func() {
		loraDirMu.Lock()
		loraDirOverride = orig
		loraDirMu.Unlock()
	})
}

func TestScanLoraAdapters(t *testing.T) {
	tmp := withTempCwd(t)
	saveConfigState(t)
	withLoraDirReset(t)
	loraDir := filepath.Join(tmp, "LLM-Models", "lora")
	if err := os.MkdirAll(loraDir, 0755); err != nil {
		t.Fatal(err)
	}

	writeTestGGUF(t, filepath.Join(loraDir, "my-lora.gguf"), loraAdapterKVs(16, true, "llama"))
	// A regular model GGUF (general.type "model"): scans but must not be valid.
	writeTestGGUF(t, filepath.Join(loraDir, "not-adapter.gguf"), []testKV{
		{key: "general.type", typ: 8, str: "model"},
		{key: "general.architecture", typ: 8, str: "llama"},
	})
	// An adapter without the optional alpha key: Valid, HasAlpha=false.
	writeTestGGUF(t, filepath.Join(loraDir, "no-alpha.gguf"), loraAdapterKVs(0, false, "qwen2"))
	// Garbage bytes with a .gguf suffix: Valid=false, not a crash.
	if err := os.WriteFile(filepath.Join(loraDir, "garbage.gguf"), []byte("not gguf at all"), 0644); err != nil {
		t.Fatal(err)
	}
	// Non-gguf files and subdirectories are skipped entirely.
	if err := os.WriteFile(filepath.Join(loraDir, "readme.txt"), []byte("hi"), 0644); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(loraDir, "nested"), 0755); err != nil {
		t.Fatal(err)
	}

	got := scanLoraAdapters()
	if len(got) != 4 {
		t.Fatalf("scanLoraAdapters returned %d entries (%+v), want 4", len(got), got)
	}
	byName := map[string]LoraInfo{}
	for _, info := range got {
		byName[info.Name] = info
	}
	lora := byName["my-lora.gguf"]
	if !lora.Valid || !lora.HasAlpha || lora.Alpha != 16 || lora.Arch != "llama" {
		t.Errorf("my-lora.gguf = %+v, want valid adapter with alpha 16 arch llama", lora)
	}
	if lora.SizeBytes == 0 || lora.SizeHuman == "" {
		t.Errorf("my-lora.gguf missing size info: %+v", lora)
	}
	if byName["not-adapter.gguf"].Valid {
		t.Errorf("regular model GGUF must not be marked as an adapter")
	}
	noAlpha := byName["no-alpha.gguf"]
	if !noAlpha.Valid || noAlpha.HasAlpha {
		t.Errorf("no-alpha.gguf = %+v, want valid without alpha", noAlpha)
	}
	if byName["garbage.gguf"].Valid {
		t.Errorf("garbage.gguf must not be marked as an adapter")
	}
	// Sorted by name for a stable UI order.
	for i := 1; i < len(got); i++ {
		if got[i-1].Name >= got[i].Name {
			t.Errorf("scan results not sorted by name: %v", got)
		}
	}
	_ = tmp
}

func TestScanLoraAdaptersMissingDir(t *testing.T) {
	withTempCwd(t)
	saveConfigState(t)
	withLoraDirReset(t)
	got := scanLoraAdapters()
	if len(got) != 0 {
		t.Errorf("scanLoraAdapters on missing dir returned %v, want empty", got)
	}
}

func TestEffectiveLoraDirDefaultAndOverride(t *testing.T) {
	tmp := withTempCwd(t)
	saveConfigState(t)
	withLoraDirReset(t)
	// under the pinned test layout (the fixtures live in the temp cwd).
	if want := filepath.Join(modelsDirName, "lora"); effectiveLoraDir() != want {
		t.Errorf("default lora dir = %q, want %q", effectiveLoraDir(), want)
	}
	loraDirMu.Lock()
	loraDirOverride = filepath.Join(tmp, "custom-lora")
	loraDirMu.Unlock()
	if got := effectiveLoraDir(); got != filepath.Join(tmp, "custom-lora") {
		t.Errorf("override lora dir = %q", got)
	}
}

// ─── LoraRef validation & serialization ──────────────────────────

func TestValidLoraRefName(t *testing.T) {
	valid := []string{"my-lora.gguf", "Qwen3 LoRA v2.gguf", "a.B_C.gguf"}
	invalid := []string{"", ".", "..", "../evil.gguf", `sub\dir.gguf`, "sub/dir.gguf",
		"a:b.gguf", "a,b.gguf", `a"b.gguf`, " lead.gguf", "trail.gguf ", "x\ny.gguf"}
	for _, name := range valid {
		if !validLoraRefName(name) {
			t.Errorf("validLoraRefName(%q) = false, want true", name)
		}
	}
	for _, name := range invalid {
		if validLoraRefName(name) {
			t.Errorf("validLoraRefName(%q) = true, want false", name)
		}
	}
}

func TestValidateLoraRefScale(t *testing.T) {
	if err := validateLoraRef(LoraRef{Name: "a.gguf", Scale: 1.5, Enabled: true}); err != nil {
		t.Errorf("valid ref rejected: %v", err)
	}
	for _, scale := range []float64{-0.1, 4.5} {
		if err := validateLoraRef(LoraRef{Name: "a.gguf", Scale: scale}); err == nil {
			t.Errorf("scale %v accepted, want error", scale)
		}
	}
	if err := validateLoraRef(LoraRef{Name: "../evil.gguf", Scale: 1}); err == nil {
		t.Errorf("path-traversal name accepted, want error")
	}
}

func TestFormatLoraScale(t *testing.T) {
	cases := map[float64]string{1: "1", 0.05: "0.05", 1.25: "1.25", 4: "4", 0.75: "0.75"}
	for in, want := range cases {
		if got := formatLoraScale(in); got != want {
			t.Errorf("formatLoraScale(%v) = %q, want %q", in, got, want)
		}
	}
}

func TestLoraScaledValue(t *testing.T) {
	cfg := ModelConfig{LoraAdapters: []LoraRef{
		{Name: "off.gguf", Scale: 1, Enabled: false},
		{Name: "a.gguf", Scale: 1.5, Enabled: true},
		{Name: "b.gguf", Scale: 0.05, Enabled: true},
	}}
	got, err := loraScaledValue(cfg)
	if err != nil {
		t.Fatal(err)
	}
	if want := "a.gguf:1.5,b.gguf:0.05"; got != want {
		t.Errorf("loraScaledValue = %q, want %q", got, want)
	}
	// All disabled → empty value, no error.
	empty, err := loraScaledValue(ModelConfig{LoraAdapters: []LoraRef{{Name: "a.gguf", Scale: 1}}})
	if err != nil || empty != "" {
		t.Errorf("all-disabled loraScaledValue = %q, %v; want \"\", nil", empty, err)
	}
	// Invalid name → error.
	if _, err := loraScaledValue(ModelConfig{LoraAdapters: []LoraRef{{Name: "a:gguf", Scale: 1, Enabled: true}}}); err == nil {
		t.Errorf("colon name accepted, want error")
	}
}

// ─── Preset / direct-args integration ────────────────────────────

// TestModelPresetKVLoraAdapters verifies modelPresetKV emits the single
// lora-scaled CSV entry (INI key == upstream option name) and that the
// android direct-mode serializer passes it through as --lora-scaled.
func TestModelPresetKVLoraAdapters(t *testing.T) {
	m := ModelInfo{Name: "M", Path: "/m.gguf"}
	cfg := ModelConfig{LoraAdapters: []LoraRef{
		{Name: "a.gguf", Scale: 1.5, Enabled: true},
		{Name: "b.gguf", Scale: 1, Enabled: false},
	}}
	kvs, err := modelPresetKV(m, cfg)
	if err != nil {
		t.Fatal(err)
	}
	var scaled []string
	for _, kv := range kvs {
		if kv.key == "lora-scaled" {
			scaled = append(scaled, kv.value)
		}
	}
	if len(scaled) != 1 || scaled[0] != "a.gguf:1.5" {
		t.Errorf("presetKV lora-scaled = %v, want exactly [a.gguf:1.5]", scaled)
	}

	args, err := modelDirectArgs("M", m, cfg)
	if err != nil {
		t.Fatal(err)
	}
	val, ok := argValue(args, "--lora-scaled")
	if !ok || val != "a.gguf:1.5" {
		t.Errorf("direct args --lora-scaled = %q (ok=%v), want a.gguf:1.5", val, ok)
	}

	// No adapters → no lora-scaled entry anywhere (historical shape kept).
	kvs, err = modelPresetKV(m, ModelConfig{})
	if err != nil {
		t.Fatal(err)
	}
	for _, kv := range kvs {
		if kv.key == "lora-scaled" {
			t.Errorf("unexpected lora-scaled entry for adapter-free config")
		}
	}
}

// ─── Config compatibility ─────────────────────────────────────────

// TestLoadConfigLoraCompat verifies old configs (no loraDir / loraAdapters
// fields) load with the defaults, and that a config carrying both round-trips.
func TestLoadConfigLoraCompat(t *testing.T) {
	tmp := withTempCwd(t)
	saveConfigState(t)
	withLoraDirReset(t)
	// Old-shape config: neither loraDir nor loraAdapters present.
	oldCfg := map[string]any{
		"theme": "light",
		"modelConfigs": map[string]any{
			"qwen": map[string]any{"threads": 4, "ctxSize": 2048},
		},
	}
	data, err := json.Marshal(oldCfg)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(configFile, data, 0644); err != nil {
		t.Fatal(err)
	}
	loraDirMu.Lock()
	loraDirOverride = ""
	loraDirMu.Unlock()
	loadConfig()

	if got := effectiveLoraDir(); got != filepath.Join(modelsDirName, "lora") {
		t.Errorf("old config loraDir = %q, want default %q", got, filepath.Join(modelsDirName, "lora"))
	}
	modelConfigsMu.Lock()
	refs := cachedModelConfigs["qwen"].LoraAdapters
	modelConfigsMu.Unlock()
	if refs != nil {
		t.Errorf("old config LoraAdapters = %v, want nil", refs)
	}

	// New-shape config round-trip through save/load.
	modelConfigsMu.Lock()
	cachedModelConfigs["qwen"] = ModelConfig{
		Threads: 4, CtxSize: 2048,
		LoraAdapters: []LoraRef{{Name: "a.gguf", Scale: 0.75, Enabled: true}},
	}
	modelConfigsMu.Unlock()
	loraDirMu.Lock()
	loraDirOverride = filepath.Join(tmp, "my-loras")
	loraDirMu.Unlock()
	saveConfig()

	// Reset and reload.
	loraDirMu.Lock()
	loraDirOverride = ""
	loraDirMu.Unlock()
	modelConfigsMu.Lock()
	cachedModelConfigs = map[string]ModelConfig{}
	modelConfigsMu.Unlock()
	loadConfig()

	modelConfigsMu.Lock()
	got := cachedModelConfigs["qwen"]
	modelConfigsMu.Unlock()
	if len(got.LoraAdapters) != 1 || got.LoraAdapters[0].Name != "a.gguf" || got.LoraAdapters[0].Scale != 0.75 || !got.LoraAdapters[0].Enabled {
		t.Errorf("lora refs round-trip failed: %+v", got.LoraAdapters)
	}
	if got.Threads != 4 {
		t.Errorf("base params lost during lora round-trip: %+v", got)
	}
	loraDirMu.Lock()
	gotDir := loraDirOverride
	loraDirMu.Unlock()
	if gotDir != filepath.Join(tmp, "my-loras") {
		t.Errorf("loraDir round-trip = %q", gotDir)
	}
}

// TestSaveModelConfigPreservesLoraRefs verifies the SaveModelConfig guard:
// a config written without the lora field (nil slice) keeps the persisted
// refs; an explicit empty slice clears them.
func TestSaveModelConfigPreservesLoraRefs(t *testing.T) {
	withTempCwd(t)
	saveConfigState(t)
	withLoraDirReset(t)
	modelConfigsMu.Lock()
	cachedModelConfigs["m"] = ModelConfig{CtxSize: 2048, LoraAdapters: []LoraRef{{Name: "a.gguf", Scale: 1, Enabled: true}}}
	modelConfigsMu.Unlock()

	a := &App{}
	// Frontend base-param save: no lora field at all.
	if err := a.SaveModelConfig("m", ModelConfig{CtxSize: 4096}); err != nil {
		t.Fatal(err)
	}
	modelConfigsMu.Lock()
	got := cachedModelConfigs["m"]
	modelConfigsMu.Unlock()
	if got.CtxSize != 4096 {
		t.Errorf("base params not updated: %+v", got)
	}
	if len(got.LoraAdapters) != 1 || got.LoraAdapters[0].Name != "a.gguf" {
		t.Errorf("nil lora field must preserve refs, got %+v", got.LoraAdapters)
	}

	// Explicit empty slice clears the refs.
	if err := a.SaveModelConfig("m", ModelConfig{CtxSize: 4096, LoraAdapters: []LoraRef{}}); err != nil {
		t.Fatal(err)
	}
	modelConfigsMu.Lock()
	got = cachedModelConfigs["m"]
	modelConfigsMu.Unlock()
	if got.LoraAdapters == nil || len(got.LoraAdapters) != 0 {
		t.Errorf("explicit empty slice must clear refs, got %+v", got.LoraAdapters)
	}
}

// ─── Runtime endpoint wrappers ────────────────────────────────────

// TestFetchLoraAdapters verifies the GET wrapper parses the entry list,
// addresses router children via ?model=, and degrades 404s to
// ErrLoraUnsupported.
func TestFetchLoraAdapters(t *testing.T) {
	var gotQuery string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotQuery = r.URL.RawQuery
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`[{"id":0,"path":"a.gguf","scale":1.5},{"id":1,"path":"b.gguf","scale":0}]`))
	}))
	defer srv.Close()
	orig := routerBaseURL
	routerBaseURL = func(int) string { return srv.URL }
	defer func() { routerBaseURL = orig }()

	entries, err := fetchLoraAdapters(8080, "Mymodel")
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 2 || entries[0].Path != "a.gguf" || entries[0].Scale != 1.5 || entries[1].ID != 1 {
		t.Fatalf("entries = %+v", entries)
	}
	if gotQuery != "model=Mymodel" {
		t.Errorf("query = %q, want model=Mymodel", gotQuery)
	}
	// Direct mode: no model selector.
	if _, err := fetchLoraAdapters(8080, ""); err != nil {
		t.Fatal(err)
	}
	if gotQuery != "" {
		t.Errorf("direct-mode query = %q, want empty", gotQuery)
	}

	notFound := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.NotFound(w, r)
	}))
	defer notFound.Close()
	routerBaseURL = func(int) string { return notFound.URL }
	if _, err := fetchLoraAdapters(8080, ""); err != ErrLoraUnsupported {
		t.Errorf("404 err = %v, want ErrLoraUnsupported", err)
	}
	routerBaseURL = orig
}

// TestApplyLoraAdapters verifies the POST wrapper sends the plain array body
// and surfaces success / upstream error shapes.
func TestApplyLoraAdapters(t *testing.T) {
	var gotBody []byte
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		b, _ := io.ReadAll(r.Body)
		gotBody = b
		_, _ = w.Write([]byte(`{"success":true}`))
	}))
	defer srv.Close()
	orig := routerBaseURL
	routerBaseURL = func(int) string { return srv.URL }
	defer func() { routerBaseURL = orig }()

	if err := applyLoraAdapters(8080, []routerLoraSet{{ID: 0, Scale: 0.75}}); err != nil {
		t.Fatalf("applyLoraAdapters: %v", err)
	}
	var sets []routerLoraSet
	if err := json.Unmarshal(gotBody, &sets); err != nil {
		t.Fatalf("POST body not an array: %s", gotBody)
	}
	if len(sets) != 1 || sets[0].ID != 0 || sets[0].Scale != 0.75 {
		t.Errorf("POST sets = %+v", sets)
	}

	// Router-mode upstream quirk: the proxy rejects the array body with a
	// structured error — surfaced as a plain error, not a crash.
	proxyReject := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusBadRequest)
		_, _ = w.Write([]byte(`{"error":{"message":"model name is missing from the request","type":"invalid_request_error"}}`))
	}))
	defer proxyReject.Close()
	routerBaseURL = func(int) string { return proxyReject.URL }
	err := applyLoraAdapters(8080, nil)
	if err == nil || !strings.Contains(err.Error(), "model name is missing") {
		t.Errorf("proxy rejection err = %v, want the upstream message", err)
	}

	notFound := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.NotFound(w, r)
	}))
	defer notFound.Close()
	routerBaseURL = func(int) string { return notFound.URL }
	if err := applyLoraAdapters(8080, nil); err != ErrLoraUnsupported {
		t.Errorf("404 err = %v, want ErrLoraUnsupported", err)
	}
	routerBaseURL = orig
}

// ─── ApplyLoraRuntime end-to-end (httptest llama-server) ─────────

// TestApplyLoraRuntimeAppliesMatchingRefs drives the full binding against a
// fake llama-server: model addressing (router alias via ?model=), ref ↔ loaded
// matching by file name, disabled refs omitted (upstream resets unmentioned
// scales to 0).
func TestApplyLoraRuntimeAppliesMatchingRefs(t *testing.T) {
	withTempCwd(t)
	saveConfigState(t)
	withLoraDirReset(t)

	// Fake scanned model: LLM-Models/TestAuthor/TestModel/m.gguf.
	modelDir := filepath.Join("LLM-Models", "TestAuthor", "TestModel")
	if err := os.MkdirAll(modelDir, 0755); err != nil {
		t.Fatal(err)
	}
	writeTestGGUF(t, filepath.Join(modelDir, "m.gguf"), []testKV{
		{key: "general.type", typ: 8, str: "model"},
	})
	invalidateModelCache()

	modelConfigsMu.Lock()
	cachedModelConfigs["TestModel"] = ModelConfig{LoraAdapters: []LoraRef{
		{Name: "a.gguf", Scale: 0.75, Enabled: true},
		{Name: "dropped.gguf", Scale: 1, Enabled: false},
	}}
	modelConfigsMu.Unlock()

	var postBody []byte
	var gotQuery string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.Method {
		case http.MethodGet:
			gotQuery = r.URL.RawQuery
			_, _ = w.Write([]byte(`[{"id":0,"path":"a.gguf","scale":1},{"id":1,"path":"legacy.gguf","scale":0.5}]`))
		case http.MethodPost:
			b, _ := io.ReadAll(r.Body)
			postBody = b
			_, _ = w.Write([]byte(`{"success":true}`))
		}
	}))
	defer srv.Close()
	orig := routerBaseURL
	routerBaseURL = func(int) string { return srv.URL }
	defer func() { routerBaseURL = orig }()

	setServerPort(8080)
	defer setServerPort(0)

	a := &App{}
	if err := a.ApplyLoraRuntime("TestModel"); err != nil {
		t.Fatalf("ApplyLoraRuntime: %v", err)
	}
	if gotQuery != "model=TestModel" {
		t.Errorf("GET query = %q, want model=TestModel", gotQuery)
	}
	var sets []routerLoraSet
	if err := json.Unmarshal(postBody, &sets); err != nil {
		t.Fatalf("POST body not an array: %s", postBody)
	}
	if len(sets) != 1 || sets[0].ID != 0 || sets[0].Scale != 0.75 {
		t.Errorf("POST sets = %+v, want [{0 0.75}] only (disabled ref omitted)", sets)
	}
}

// TestApplyLoraRuntimeDegrades verifies the two best-effort degrade paths:
// a server without the endpoint (404) and a server whose loaded list matches
// none of the persisted refs (configured after start).
func TestApplyLoraRuntimeDegrades(t *testing.T) {
	withTempCwd(t)
	saveConfigState(t)
	withLoraDirReset(t)
	withLanguage(t, "en")

	modelDir := filepath.Join("LLM-Models", "A", "M")
	if err := os.MkdirAll(modelDir, 0755); err != nil {
		t.Fatal(err)
	}
	writeTestGGUF(t, filepath.Join(modelDir, "m.gguf"), []testKV{
		{key: "general.type", typ: 8, str: "model"},
	})
	invalidateModelCache()
	modelConfigsMu.Lock()
	cachedModelConfigs["M"] = ModelConfig{LoraAdapters: []LoraRef{{Name: "a.gguf", Scale: 1, Enabled: true}}}
	modelConfigsMu.Unlock()

	// 404 on GET: endpoint missing entirely (older llama.cpp).
	notFound := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.NotFound(w, r)
	}))
	defer notFound.Close()
	orig := routerBaseURL
	routerBaseURL = func(int) string { return notFound.URL }
	setServerPort(8080)
	a := &App{}
	err := a.ApplyLoraRuntime("M")
	if err == nil || !strings.Contains(err.Error(), "next time the service starts") {
		t.Errorf("404 degrade err = %v, want restart guidance", err)
	}

	// Endpoint alive but loaded list matches nothing (server started before
	// the adapters were configured).
	mismatch := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodGet {
			_, _ = w.Write([]byte(`[]`))
		}
	}))
	defer mismatch.Close()
	routerBaseURL = func(int) string { return mismatch.URL }
	err = a.ApplyLoraRuntime("M")
	if err == nil || !strings.Contains(err.Error(), "restart the service to apply") {
		t.Errorf("mismatch degrade err = %v, want restart guidance", err)
	}
	routerBaseURL = orig
	setServerPort(0)
}

// TestApplyLoraRuntimeNotRunning verifies the not-running guard.
func TestApplyLoraRuntimeNotRunning(t *testing.T) {
	withTempCwd(t)
	saveConfigState(t)
	withLoraDirReset(t)
	withLanguage(t, "en")
	setServerPort(0)
	a := &App{}
	err := a.ApplyLoraRuntime("whatever")
	if err == nil || !strings.Contains(err.Error(), "not running") {
		t.Errorf("err = %v, want not-running message", err)
	}
}

// ─── buildServerCommand LoRA working directory ───────────────────

// TestBuildServerCommandLoraWorkDir verifies the child working directory is
// pinned to the LoRA directory only when some persisted model config enables
// an adapter, and that the android direct args carry the relative adapter
// names.
func TestBuildServerCommandLoraWorkDir(t *testing.T) {
	withTempCwd(t)
	saveConfigState(t)
	withLoraDirReset(t)
	withPlatformGOOS(t, "android")

	cfg := ServerConfig{AccessMode: accessLocal, Host: "127.0.0.1", Port: 8080, MaxModels: 1}
	d := &directModel{
		info: ModelInfo{Name: "M", Path: "/m.gguf"},
		cfg:  ModelConfig{LoraAdapters: []LoraRef{{Name: "a.gguf", Scale: 1.5, Enabled: true}}},
	}

	// No persisted config enables an adapter yet → no work dir.
	modelConfigsMu.Lock()
	cachedModelConfigs = map[string]ModelConfig{}
	modelConfigsMu.Unlock()
	_, args, workDir, err := buildServerCommand(cfg, "", d)
	if err != nil {
		t.Fatal(err)
	}
	if workDir != "" {
		t.Errorf("workDir = %q with no enabled refs, want \"\"", workDir)
	}
	if _, ok := argValue(args, "--lora-scaled"); !ok {
		t.Errorf("direct args must carry the model's enabled adapter: %v", args)
	}

	// A DIFFERENT model's config enables an adapter → the working directory
	// pin kicks in globally (router children inherit it), while the direct
	// model without refs still gets no --lora-scaled.
	modelConfigsMu.Lock()
	cachedModelConfigs = map[string]ModelConfig{
		"Other": {LoraAdapters: []LoraRef{{Name: "x.gguf", Scale: 1, Enabled: true}}},
	}
	modelConfigsMu.Unlock()
	wantDir := effectiveLoraDir()
	bare := &directModel{info: ModelInfo{Name: "M", Path: "/m.gguf"}}
	_, args, workDir, err = buildServerCommand(cfg, "", bare)
	if err != nil {
		t.Fatal(err)
	}
	if workDir != wantDir {
		t.Errorf("workDir = %q, want %q", workDir, wantDir)
	}
	// The direct model has no refs of its own → no adapter on its command line.
	if _, ok := argValue(args, "--lora-scaled"); ok {
		t.Error("direct model without refs must not carry --lora-scaled")
	}
}
