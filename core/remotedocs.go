package core

// Remote docs: the Docs page's remote-first content path. Tutorial fixes
// pushed to this repo's main branch reach old app versions without a release:
// when a section is opened, the app fetches that section's latest markdown
// from GitHub and falls back through a three-tier chain —
//
//	online (GitHub main) → disk cache (last successful fetch) → bundled
//
// (the bundled tier is decided by the frontend, which renders the packaged
// markdown while the remote fetch is in flight and whenever this side reports
// no usable content).
//
// Error contract: the Wails binding GetRemoteDoc returns an error ONLY for
// invalid arguments (unknown lang / bad sectionID). Network unavailability is
// an expected state, not an error — it yields Source "none" (or a cache hit)
// with a nil error, so the frontend never has to surface an error UI for
// offline use. All other failures (cache persist errors, oversized bodies,
// mirror outages) degrade to a lower tier with a [WARN] log line.
//
// Design notes:
//   - Hosts are hardcoded constants and the lang / sectionID path components
//     are strictly validated (lang whitelist, ^[a-z]{3,20}$ section id), so
//     there is no traversal and no generic URL fetching — the remote path is
//     always frontend/src/docs/{lang}/{sectionID}.md inside this repo.
//   - Single-flight via docsMu: the lock spans the whole getRemoteDoc call
//     (same wait-then-run style as benchMu), so concurrent section opens
//     serialize instead of racing the cache files.
//   - The fail backoff (docsFailBackoff) keeps an offline user's section
//     switches instant: after a failed attempt, the network stays muted until
//     the backoff elapses — cached content is served when it exists, otherwise
//     "none" is reported immediately, so a no-cache offline user never burns
//     the 2×docsHTTPTimeout attempt either.
//   - Cache paths are path vars with per-OS default resolution (see paths.go;
//     same convention as configFile / benchCacheFile) so tests redirect them
//     into a temp directory; the clock
//     and HTTP client are package vars (same injection-point style as
//     cmdTimeout / benchMeasureFn) so tests stay deterministic and offline.

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http"
	"os"
	"path/filepath"
	"regexp"
	"sync"
	"time"
)

// ─── Result type & source constants ──────────────────────────────

// Source values of RemoteDocResult (package consts, mirrored by the frontend
// wails.ts RemoteDocResult interface and lib/remoteDocs.ts).
const (
	docsSourceOnline = "online" // content came from a successful network fetch
	docsSourceCache  = "cache"  // content came from the last successful fetch on disk
	docsSourceNone   = "none"   // no usable content this round (offline, no cache)
)

// RemoteDocResult is one docs section fetch outcome: the markdown body (empty
// when Source is "none"), which tier produced it, and the RFC3339 timestamp of
// the fetch that produced Text ("" when none — the frontend only shows it for
// the "cache" badge).
type RemoteDocResult struct {
	Text      string `json:"text"`
	Source    string `json:"source"`
	FetchedAt string `json:"fetchedAt"`
}

// ─── Constants & single-flight ───────────────────────────────────

const (
	// docsRawBaseURL serves docs straight from the GitHub main branch — the
	// authoritative source, always current.
	docsRawBaseURL = "https://raw.githubusercontent.com/CodeNeow/llama-cpp-desktop/main/"
	// docsJSDBaseURL is the reachability fallback for environments where raw
	// githubusercontent.com is blocked or flaky. jsDelivr branch content is
	// CDN-cached and can lag main; raw stays authoritative.
	docsJSDBaseURL = "https://cdn.jsdelivr.net/gh/CodeNeow/llama-cpp-desktop@main/"

	// docsRepoPathFmt builds the in-repo markdown path from lang + sectionID;
	// both components are strictly validated before formatting (no traversal).
	docsRepoPathFmt = "frontend/src/docs/%s/%s.md"

	// docsRemoteTTL is how long a cached fetch stays fresh enough to skip the
	// network entirely on section open.
	docsRemoteTTL = 24 * time.Hour
	// docsFailBackoff mutes network attempts after a total failure: within the
	// backoff window cached content is served immediately, which keeps an
	// offline user's section switches instant (and the [WARN] logs quiet).
	docsFailBackoff = 10 * time.Minute
	// docsHTTPTimeout bounds each mirror attempt; the client itself stays
	// timeout-free because the per-attempt request context enforces it.
	docsHTTPTimeout = 5 * time.Second

	// docsCacheMetaVersion guards the meta.json schema; any other version is
	// [WARN]-dropped and the cache starts empty.
	docsCacheMetaVersion = 1
	// docsMetaName is the meta file name inside docsCacheDir.
	docsMetaName = "meta.json"
)

// docsMaxBytes caps one fetched markdown body (2 MiB is far above the largest
// bundled section); a body reaching the cap is rejected as an attempt failure.
// A var so tests can shrink it instead of allocating megabytes.
var docsMaxBytes int64 = 2 << 20

// docsSectionIDRe is the strict section-id whitelist ("faq", "api",
// "quickstart", ... — the DocSectionId values of the frontend manifest).
var docsSectionIDRe = regexp.MustCompile(`^[a-z]{3,20}$`)

// docsMu single-flights remote doc fetches: the lock spans the whole
// getRemoteDoc call (same style as benchMu), so concurrent section opens
// serialize and never race the cache content/meta writes.
var docsMu sync.Mutex

// Injection points (same style as cmdTimeout / benchMeasureFn): tests swap
// these vars instead of depending on the wall clock or the real network.
var (
	// docsNow is the clock used for every TTL/backoff/persistence decision.
	docsNow func() time.Time = time.Now
	// docsHTTPClient performs the mirror attempts; timeouts come from each
	// attempt's request context, so the client carries no Timeout of its own.
	docsHTTPClient = &http.Client{}
)

// docsCacheDir is the doc cache directory override: the bare default means
// "resolve via docsCacheDirPath" (cwd-relative on Windows, under the app-data
// base on other platforms, see paths.go); tests assign an explicit path to
// pin the location. Content files are {dir}/{lang}-{sectionID}.md.
var docsCacheDir = docsCacheDirName

// docsCacheDirPath resolves the active doc cache directory: an explicit
// docsCacheDir override wins, otherwise the per-OS default applies.
func docsCacheDirPath() string {
	if docsCacheDir != docsCacheDirName {
		return docsCacheDir
	}
	return resolveStateFile(docsCacheDirName)
}

// migrateLegacyDocsCacheDir performs the one-time llama-desktop-docscache →
// myllama-docscache directory rename so an upgraded install keeps its cached
// sections: when the new-name directory is absent and the legacy-name
// directory exists, the legacy directory is renamed. Any failure (or an
// explicit docsCacheDir override, i.e. tests) is a no-op — the cache is
// re-fetchable, so the use sites' MkdirAll then simply creates the new empty
// directory and nothing is lost. A no-op when the new directory already
// exists. Called at the start of getRemoteDoc (inside docsMu), so the rename
// can never race a concurrent cache write.
func migrateLegacyDocsCacheDir() {
	dir := docsCacheDirPath()
	if fi, err := os.Stat(dir); err == nil && fi.IsDir() {
		return
	}
	if docsCacheDir != docsCacheDirName {
		return
	}
	legacy := resolveStateFile(legacyDocsCacheDirName)
	if fi, err := os.Stat(legacy); err != nil || !fi.IsDir() {
		return
	}
	if err := os.Rename(legacy, dir); err != nil {
		log.Printf("[WARN] remotedocs: cannot migrate legacy doc cache dir %s: %v (re-fetching)", legacy, err)
		return
	}
	log.Printf("[OK] remotedocs: migrated legacy doc cache dir %s -> %s", legacy, dir)
}

// ─── Cache meta (meta.json) ──────────────────────────────────────

// docsCacheEntry records when a section's cached content was fetched.
type docsCacheEntry struct {
	FetchedAt string `json:"fetchedAt"` // RFC3339
}

// docsCacheMeta is the on-disk meta.json next to the cached content files:
// per-entry fetch timestamps (TTL freshness) plus the last total network
// failure time (fail backoff).
type docsCacheMeta struct {
	Version           int                       `json:"version"`
	Entries           map[string]docsCacheEntry `json:"entries"`
	LastNetworkFailAt string                    `json:"lastNetworkFailAt,omitempty"` // RFC3339
}

// readDocsMeta loads meta.json; a missing, corrupt or wrong-version file
// degrades to an empty cache with a [WARN] log — never a failure.
func readDocsMeta() docsCacheMeta {
	empty := docsCacheMeta{Version: docsCacheMetaVersion, Entries: map[string]docsCacheEntry{}}
	metaPath := filepath.Join(docsCacheDirPath(), docsMetaName)
	data, err := os.ReadFile(metaPath)
	if err != nil {
		if os.IsNotExist(err) {
			log.Printf("[WARN] remotedocs: no doc cache meta yet at %s (first run?)", metaPath)
		} else {
			log.Printf("[WARN] remotedocs: cannot read doc cache meta %s: %v", metaPath, err)
		}
		return empty
	}
	var meta docsCacheMeta
	if err := json.Unmarshal(data, &meta); err != nil {
		log.Printf("[WARN] remotedocs: corrupt doc cache meta %s, ignoring: %v", metaPath, err)
		return empty
	}
	if meta.Version != docsCacheMetaVersion {
		log.Printf("[WARN] remotedocs: unsupported doc cache meta version %d in %s, ignoring", meta.Version, metaPath)
		return empty
	}
	if meta.Entries == nil {
		meta.Entries = map[string]docsCacheEntry{}
	}
	return meta
}

// writeDocsMeta persists meta.json crash-safely; failures are [WARN]-logged
// and never fail the fetch (the cache is an optimization, not a requirement).
func writeDocsMeta(meta docsCacheMeta) {
	if err := os.MkdirAll(docsCacheDirPath(), 0755); err != nil {
		log.Printf("[WARN] remotedocs: cannot create doc cache dir %s: %v", docsCacheDirPath(), err)
		return
	}
	meta.Version = docsCacheMetaVersion
	data, err := json.Marshal(meta)
	if err != nil {
		log.Printf("[WARN] remotedocs: cannot encode doc cache meta: %v", err)
		return
	}
	if err := atomicWriteFile(filepath.Join(docsCacheDirPath(), docsMetaName), data, 0644); err != nil {
		log.Printf("[WARN] remotedocs: cannot persist doc cache meta: %v", err)
	}
}

// ─── Network attempts ────────────────────────────────────────────

// docsFetchURL performs one mirror attempt: GET url with a docsHTTPTimeout
// context, require status 200, read through a LimitReader that rejects bodies
// reaching docsMaxBytes (a truncated doc must never reach the cache).
func docsFetchURL(url string) (string, error) {
	ctx, cancel := context.WithTimeout(context.Background(), docsHTTPTimeout)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return "", fmt.Errorf("build request for %s: %w", url, err)
	}
	resp, err := docsHTTPClient.Do(req)
	if err != nil {
		return "", fmt.Errorf("request %s: %w", url, err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("unexpected status %d from %s", resp.StatusCode, url)
	}
	data, err := io.ReadAll(io.LimitReader(resp.Body, docsMaxBytes+1))
	if err != nil {
		return "", fmt.Errorf("read %s: %w", url, err)
	}
	if int64(len(data)) > docsMaxBytes {
		return "", fmt.Errorf("doc at %s exceeds the %d byte cap", url, docsMaxBytes)
	}
	return string(data), nil
}

// fetchDocFromSources tries each mirror in order and returns the first
// success; every failure is [WARN]-logged and the next mirror is attempted.
func fetchDocFromSources(urls ...string) (string, bool) {
	for _, url := range urls {
		content, err := docsFetchURL(url)
		if err != nil {
			log.Printf("[WARN] remotedocs: fetch attempt failed: %v", err)
			continue
		}
		return content, true
	}
	return "", false
}

// ─── Decision core ───────────────────────────────────────────────

// getRemoteDoc resolves one docs section through the fallback chain:
//
//  1. !force + cached content fresher than docsRemoteTTL → cache, no network;
//  2. !force + a network failure within docsFailBackoff → no network at all:
//     cache when one exists, otherwise "none" immediately (keeps offline
//     section switches instant with zero transport calls, cache or not);
//  3. otherwise try raw.githubusercontent.com, then the jsDelivr fallback;
//     success persists content + meta and reports "online";
//  4. total failure persists lastNetworkFailAt and reports the cached content
//     ("cache") or "none" when no cache exists.
//
// The only errors are invalid arguments; everything else degrades a tier.
func getRemoteDoc(lang, sectionID string, force bool) (RemoteDocResult, error) {
	if lang != "zh" && lang != "en" {
		return RemoteDocResult{}, fmt.Errorf(tr("未知语言 %q", "unknown language %q"), lang)
	}
	if !docsSectionIDRe.MatchString(sectionID) {
		return RemoteDocResult{}, fmt.Errorf(tr("非法文档小节 ID %q", "invalid doc section id %q"), sectionID)
	}

	docsMu.Lock()
	defer docsMu.Unlock()

	// One-time legacy cache-directory rename before any path is resolved.
	migrateLegacyDocsCacheDir()

	key := lang + "-" + sectionID
	contentPath := filepath.Join(docsCacheDirPath(), key+".md")
	now := docsNow()

	// Snapshot the current cache state: content file + meta entry.
	meta := readDocsMeta()
	entry, hasEntry := meta.Entries[key]
	var cachedAt time.Time
	if hasEntry {
		if ts, err := time.Parse(time.RFC3339, entry.FetchedAt); err == nil {
			cachedAt = ts
		}
	}
	cachedContent := ""
	if raw, err := os.ReadFile(contentPath); err == nil {
		cachedContent = string(raw)
	} else if !os.IsNotExist(err) {
		log.Printf("[WARN] remotedocs: cannot read cached doc %s: %v", contentPath, err)
	}

	// 1. Fresh cache short-circuit: no network at all within the TTL.
	if !force && cachedContent != "" && !cachedAt.IsZero() && now.Sub(cachedAt) < docsRemoteTTL {
		return RemoteDocResult{Text: cachedContent, Source: docsSourceCache, FetchedAt: entry.FetchedAt}, nil
	}

	// 2. Fail backoff: a recent total failure mutes the network entirely —
	// serve the cache when one exists, otherwise report "none" right away, so
	// an offline user's section switches stay instant with zero transport
	// calls regardless of cache presence.
	if !force {
		if failAt, err := time.Parse(time.RFC3339, meta.LastNetworkFailAt); err == nil && !failAt.IsZero() && now.Sub(failAt) < docsFailBackoff {
			if cachedContent != "" {
				return RemoteDocResult{Text: cachedContent, Source: docsSourceCache, FetchedAt: entry.FetchedAt}, nil
			}
			return RemoteDocResult{Source: docsSourceNone}, nil
		}
	}

	// 3. Network attempts: raw first (authoritative), jsDelivr as the
	// reachability fallback.
	remotePath := fmt.Sprintf(docsRepoPathFmt, lang, sectionID)
	content, ok := fetchDocFromSources(docsRawBaseURL+remotePath, docsJSDBaseURL+remotePath)
	if ok {
		if err := os.MkdirAll(docsCacheDirPath(), 0755); err != nil {
			log.Printf("[WARN] remotedocs: cannot create doc cache dir %s: %v", docsCacheDirPath(), err)
		} else if err := atomicWriteFile(contentPath, []byte(content), 0644); err != nil {
			log.Printf("[WARN] remotedocs: cannot persist %s: %v", contentPath, err)
		}
		meta.Entries[key] = docsCacheEntry{FetchedAt: now.Format(time.RFC3339)}
		meta.LastNetworkFailAt = ""
		writeDocsMeta(meta)
		log.Printf("[OK] remotedocs: fetched %s (%d bytes)", key, len(content))
		return RemoteDocResult{Text: content, Source: docsSourceOnline, FetchedAt: now.Format(time.RFC3339)}, nil
	}

	// 4. Total failure: record the failure time for the backoff, then fall
	// back to whatever cache exists ("none" when there is none).
	log.Printf("[WARN] remotedocs: all sources failed for %s", key)
	meta.LastNetworkFailAt = now.Format(time.RFC3339)
	writeDocsMeta(meta)
	if cachedContent != "" {
		return RemoteDocResult{Text: cachedContent, Source: docsSourceCache, FetchedAt: entry.FetchedAt}, nil
	}
	return RemoteDocResult{Text: "", Source: docsSourceNone}, nil
}

// ─── Wails binding ───────────────────────────────────────────────

// GetRemoteDoc fetches one docs section's latest markdown for the Docs page's
// remote-first content path (online → disk cache → bundled fallback decided
// here; the bundled tier lives in the frontend). See the file header for the
// error contract: errors are returned ONLY for invalid arguments — network
// unavailability is an expected state reported through Source "cache"/"none".
func (a *App) GetRemoteDoc(lang, sectionID string, force bool) (RemoteDocResult, error) {
	return getRemoteDoc(lang, sectionID, force)
}
