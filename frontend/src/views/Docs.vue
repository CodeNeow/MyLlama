<template>
  <div class="page docs-page">
    <div class="sticky-top">
      <div class="page-header">
        <div class="page-header-text">
          <h1 class="page-title">{{ t('docs.title') }}</h1>
          <p class="page-subtitle">{{ t('docs.subtitle') }}</p>
        </div>
        <!-- Content source badge + escape hatches: the badge names the tier
             that produced the pane (online fetch / disk cache / bundled),
             refresh forces a fresh network round-trip, the GitHub button
             opens the live docs directory in the browser. -->
        <div class="docs-header-actions">
          <span class="source-badge" :class="'source-' + docSource">{{ sourceLabel }}</span>
          <button class="docs-action-btn" type="button" :disabled="refreshing" @click="refreshRemote">
            {{ refreshing ? t('docs.refreshing') : t('docs.refresh') }}
          </button>
          <button class="docs-action-btn" type="button" @click="openOnGithub">{{ t('docs.openOnGithub') }}</button>
        </div>
      </div>
    </div>

    <!-- ─── Phone + tablet tiers (frames ⑰⑱): source badges row ───
         The pill row (source badge / refresh / GitHub) replaces the desktop
         header action buttons on every non-desktop tier (showsSourcePillRow).
         On the phone tier the two panes below are additionally replaced by
         the section list (rows navigate to /docs/:id where DocsReader.vue
         renders the section); the desktop panes stay in the DOM (hidden via
         CSS on that tier) so the shared content pipeline keeps feeding the
         badge pills. On tablet tiers the same panes ARE the master-detail
         layout (frames ⑰⑱ — the tablet has no standalone reader route). -->
    <div v-if="docsMode !== 'desktop'" class="srcbadges">
      <span class="sb" :class="'sb-' + docSource">{{ sourceLabel }}</span>
      <button class="sb sb-action" type="button" :disabled="refreshing" @click="refreshRemote">
        {{ refreshing ? t('docs.refreshing') : t('docs.refresh') }}
      </button>
      <button class="sb sb-action sb-github" type="button" @click="openOnGithub">{{ t('docs.openOnGithub') }}</button>
    </div>
    <!-- Phone-only chapter list (frame ⑰): numbered grad-soft tile + title +
         desc + chevron (design draft .docrow). Desc comes from i18n
         docs.section.<id>.desc. Tablet tiers keep the master-detail split
         instead (the .docs-toc column IS the chapter navigation). -->
    <nav v-if="docsMode === 'phone'" class="doclist" :aria-label="t('docs.toc')">
      <button
        v-for="(section, index) in docSections"
        :key="section.id"
        type="button"
        class="docrow"
        @click="openSection(section.id)"
      >
        <span class="no">{{ String(index + 1).padStart(2, '0') }}</span>
        <span class="docrow-text">
          <span class="docrow-t">{{ t(section.titleKey) }}</span>
          <span class="docrow-desc">{{ t(section.titleKey + '.desc') }}</span>
        </span>
        <span class="ar" aria-hidden="true">›</span>
      </button>
    </nav>

    <div class="docs-layout">
      <!-- In-page section navigator: a sticky vertical list on wide screens,
           collapsing into a wrapping chip row on narrow ones (CSS only) -->
      <nav class="docs-toc" :aria-label="t('docs.toc')">
        <button
          v-for="(section, index) in docSections"
          :key="section.id"
          class="toc-item"
          :class="{ active: section.id === activeSection }"
          type="button"
          @click="selectSection(section.id)"
        >
          <span class="no">{{ String(index + 1).padStart(2, '0') }}</span>
          <span class="toc-item-text">
            <span class="toc-item-title">{{ t(section.titleKey) }}</span>
            <span class="toc-item-desc">{{ t(section.titleKey + '.desc') }}</span>
          </span>
        </button>
      </nav>

      <!-- Rendered markdown pane. v-html is safe here: the shared markdown-it
           instance (lib/markdown.ts) runs with html:false, so raw HTML in the
           docs source is escaped, never rendered — the same injection defense
           as chat messages. -->
      <!-- Delegated link handler: external links open in the system browser,
           the WebView never navigates — left click, middle click and drag
           included (see lib/linkHandler.ts) -->
      <article
        ref="contentEl"
        class="docs-content"
        :class="{ 'is-loading': loading }"
        v-html="rendered"
        @click="handleLinkClick"
        @auxclick="handleLinkAuxClick"
        @dragstart="handleLinkDragStart"
      ></article>
    </div>

    <!-- Load failure of a bundled asset is practically impossible; still handled without silently swallowing -->
    <div v-if="loadError" class="docs-error">{{ loadError }}</div>
  </div>
</template>

<script lang="ts">
import { computed, ref, watch, type ComputedRef, type Ref } from 'vue'
import { loadDocSection, type DocSectionId } from '../docs/manifest'
import { t, locale } from '../lib/i18n'
import { LatestOnly } from '../lib/latestOnly'
import { getRemoteDoc } from '../wails'
import { formatDocFetchedAt, resolveDocContent, type DocSourceState } from '../lib/remoteDocs'

/**
 * Content pipeline for one docs section, shared by the desktop Docs page and
 * the phone DocsReader (frames ⑰⑱ — this named export keeps the remote-first
 * logic in exactly one place): the bundled text shows immediately, then a
 * remote fetch (getRemoteDoc: online → backend disk cache) may seamlessly
 * replace it. A LatestOnly sequence guards against a slow locale/section
 * fetch overwriting a newer one; network trouble degrades to the bundled copy
 * with a console warning, never an error UI. Reloads on section and locale
 * change (immediate).
 */
export interface DocContentView {
  content: Ref<string>
  loading: Ref<boolean>
  loadError: Ref<string>
  docSource: Ref<DocSourceState>
  fetchedAt: Ref<string>
  sourceLabel: ComputedRef<string>
  refreshing: Ref<boolean>
  loadSection: () => Promise<void>
  refreshRemote: () => Promise<void>
}

export function useDocContent(sectionId: Ref<DocSectionId>): DocContentView {
  // Raw markdown of the active section; previous content stays visible
  // (dimmed) while a new section loads, so switching never flashes an empty pane
  const content = ref('')
  const loading = ref(false)
  const loadError = ref('')
  // Source state of the displayed content (badge): which tier produced it —
  // a successful network fetch, the backend's disk cache, or the bundled copy
  const docSource = ref<DocSourceState>('bundled')
  // RFC3339 fetchedAt of the cached content; the badge shows it for 'cached'
  const fetchedAt = ref('')
  const refreshing = ref(false)

  // Race protection: a slow locale/section fetch must not overwrite a newer one.
  // Shared by the section load and the refresh action so either path discards
  // the other's stale response.
  const latest = new LatestOnly()

async function loadSection(): Promise<void> {
  const seq = latest.begin()
  loading.value = true
  loadError.value = ''
  // Bundled text of this round; the remote result falls back to it (or to the
  // pane's current text if the bundled asset itself failed — near-impossible)
  let bundled = ''
  try {
    const md = await loadDocSection(sectionId.value, locale.value)
    if (!latest.isLatest(seq)) return
    bundled = md
    content.value = md
  } catch (e) {
    if (!latest.isLatest(seq)) return
    loadError.value = t('docs.loadError', { msg: e instanceof Error ? e.message : String(e) })
  } finally {
    if (latest.isLatest(seq)) loading.value = false
  }

  // Remote-first refresh of the freshly shown section, under the SAME
  // LatestOnly sequence: a quick section switch discards the stale response.
  // Network trouble is an expected state — swallowed here (console.warn), the
  // pane keeps the bundled text and the badge stays 'bundled'.
  try {
    const remote = await getRemoteDoc(locale.value, sectionId.value)
    if (!latest.isLatest(seq)) return
    const resolved = resolveDocContent(remote, bundled || content.value)
    content.value = resolved.text
    docSource.value = resolved.state
    fetchedAt.value = resolved.state === 'bundled' ? '' : remote.fetchedAt
  } catch (e) {
    if (!latest.isLatest(seq)) return
    console.warn('remote docs fetch failed; staying with bundled content', e)
  }
}

// Refresh button: force a network round-trip for the current section. A usable
// result swaps content + badge; an unusable one (or a binding rejection)
// silently keeps the current content — network trouble never surfaces as an
// error UI here.
async function refreshRemote(): Promise<void> {
  if (refreshing.value) return
  refreshing.value = true
  const seq = latest.begin()
  try {
    const remote = await getRemoteDoc(locale.value, sectionId.value, true)
    if (!latest.isLatest(seq)) return
    const resolved = resolveDocContent(remote, content.value)
    if (resolved.state !== 'bundled') {
      content.value = resolved.text
      docSource.value = resolved.state
      fetchedAt.value = remote.fetchedAt
    }
  } catch (e) {
    console.warn('remote docs refresh failed; keeping current content', e)
  } finally {
    refreshing.value = false
  }
}

  // Reload on section switch and on language switch (content follows app language reactively)
  watch([sectionId, locale], () => { void loadSection() }, { immediate: true })

  // Badge label per current state; the cached state carries the fetch time, or
  // falls back to the plain label when the timestamp is unavailable (a cached
  // file whose meta entry is missing) — never a dangling "· " separator
  const sourceLabel = computed(() => {
    if (docSource.value === 'online') return t('docs.sourceOnline')
    if (docSource.value === 'cached') {
      const when = formatDocFetchedAt(fetchedAt.value)
      return when !== '' ? t('docs.sourceCached', { time: when }) : t('docs.sourceCachedPlain')
    }
    return t('docs.sourceBundled')
  })

  return { content, loading, loadError, docSource, fetchedAt, sourceLabel, refreshing, loadSection, refreshRemote }
}
</script>

<script setup lang="ts">
import { nextTick } from 'vue'
import { useRouter } from 'vue-router'
import { docSections } from '../docs/manifest'
import { renderMarkdown } from '../lib/markdown'
import { handleLinkClick, handleLinkAuxClick, handleLinkDragStart } from '../lib/linkHandler'
import { DOCS_GITHUB_URLS, docsPageMode } from '../lib/remoteDocs'
import { Browser } from '@wailsio/runtime'
import { usePlatform } from '../lib/platform'

// Currently displayed section; defaults to the first entry of the manifest.
// Content loading lives in useDocContent (plain <script> block above) — the
// same pipeline the phone DocsReader imports for /docs/:id. On the phone tier
// it keeps running behind the CSS-hidden desktop panes so the list's badge
// pills report a real content tier (bundled / cached / online).
const activeSection = ref<DocSectionId>(docSections[0].id)
const {
  content,
  loading,
  loadError,
  docSource,
  refreshing,
  sourceLabel,
  refreshRemote,
} = useDocContent(activeSection)

// Viewport composition (frames ⑰⑱, pure helper docsPageMode in
// lib/remoteDocs.ts): 'phone' renders the section list and pushes /docs/:id;
// 'tablet' renders the pill row + master-detail split; the
// list DOM is phone-only while the pill row mounts on phone AND tablet —
// desktop keeps the header action buttons instead. The split geometry itself
// is CSS-only (the Track A media band).
const platformState = usePlatform()
const docsMode = computed(() => docsPageMode(platformState.value))
const router = useRouter()

function openSection(id: DocSectionId): void {
  router.push(`/docs/${id}`)
}

// Escape hatch: open the current locale's docs directory on GitHub in the
// system browser (Wails runtime), so the newest released tutorial is one
// click away even when the remote fetch path is unavailable.
function openOnGithub(): void {
  Browser.OpenURL(DOCS_GITHUB_URLS[locale.value])
}

// Rendered HTML via the shared markdown-it instance (html disabled — see template comment)
const rendered = computed(() => renderMarkdown(content.value))

const contentEl = ref<HTMLElement | null>(null)

function selectSection(id: DocSectionId): void {
  if (id === activeSection.value) return
  activeSection.value = id
  // Bring the freshly selected section's top into view. The scroll container
  // is the shared .content-area ancestor (App.vue); the article's
  // scroll-margin-top clears the sticky page header.
  void nextTick(() => contentEl.value?.scrollIntoView({ block: 'start' }))
}
</script>

<style scoped>
.docs-page {
  /* No top padding: header flush with content top, title aligns with sidebar logo (see global.css .page-header) */
  padding: 0 48px 60px;
}

.page-header {
  /* Flex row: title block left, badge + actions right (same layout as Models.vue) */
  display: flex;
  align-items: center;
  justify-content: space-between;
  flex-wrap: wrap;
  gap: 12px 16px;
  /* Use padding instead of margin: header background covers this gap so content scrolls without leaving a seam */
  padding-bottom: 36px;
}

.page-header-text {
  min-width: 0;
}

/* ─── Header actions: source badge + refresh + open-on-GitHub ─── */
.docs-header-actions {
  display: flex;
  align-items: center;
  gap: 10px;
  flex-shrink: 0;
}

.source-badge {
  display: inline-flex;
  align-items: center;
  padding: 4px 12px;
  border-radius: 999px;
  border: 1px solid var(--border);
  background: var(--surface);
  font-size: 12px;
  white-space: nowrap;
  color: var(--text-muted);
}

.source-badge.source-online {
  color: var(--accent-light);
  border-color: var(--accent-glow);
  background: var(--active-bg);
}

.source-badge.source-cached {
  color: var(--text-secondary);
}

.docs-action-btn {
  padding: 6px 14px;
  background: var(--surface);
  color: var(--text-secondary);
  border: 1px solid var(--border);
  border-radius: 8px;
  font-size: 12.5px;
  cursor: pointer;
  transition: all 0.2s;
  white-space: nowrap;
}

.docs-action-btn:hover:not(:disabled) {
  background: var(--hover-bg);
  color: var(--text-primary);
}

.docs-action-btn:disabled {
  opacity: 0.4;
  cursor: not-allowed;
}

.page-title {
  font-size: 28px;
  font-weight: 700;
  color: var(--text-primary);
  margin: 0 0 4px;
  letter-spacing: -0.5px;
  line-height: 1.2;
}

.page-subtitle {
  font-size: 14px;
  color: var(--text-dim);
  margin: 0;
}

/* ─── Layout: sticky TOC left, content right ─── */
.docs-layout {
  display: flex;
  align-items: flex-start;
  gap: 24px;
}

/* Sticky offset below the sticky page header (title + subtitle + header
   padding amount to roughly 96px; 112px leaves a comfortable gap) */
.docs-toc {
  width: 190px;
  flex-shrink: 0;
  position: sticky;
  top: 112px;
  display: flex;
  flex-direction: column;
  gap: 4px;
}

/* Desktop TOC: sticky vertical list on wide screens (matching phone .docrow pattern) */
.toc-item {
  display: flex;
  align-items: center;
  gap: 14px;
  width: 100%;
  min-height: 44px;
  text-align: left;
  background: var(--bg-secondary);
  border: none;
  border-radius: var(--r-md);
  box-shadow: var(--shadow-island);
  padding: 15px 16px;
  margin-bottom: 11px;
  cursor: pointer;
  font-family: inherit;
}

.toc-item .no {
  width: 34px;
  height: 34px;
  border-radius: var(--r-sm);
  background: var(--grad-soft);
  color: var(--accent-light);
  font-weight: 800;
  font-size: 13px;
  display: flex;
  align-items: center;
  justify-content: center;
  flex-shrink: 0;
}

.toc-item-text {
  display: flex;
  flex-direction: column;
  flex: 1;
  min-width: 0;
}

.toc-item-title {
  font-size: 14px;
  font-weight: 700;
  color: var(--text-primary);
  min-width: 0;
  overflow: hidden;
  text-overflow: ellipsis;
  white-space: nowrap;
}

.toc-item-desc {
  font-size: 11.5px;
  font-weight: 500;
  color: var(--text-muted);
  margin-top: 2px;
  min-width: 0;
  overflow: hidden;
  text-overflow: ellipsis;
  white-space: nowrap;
}

.toc-item:hover {
  background: var(--hover-bg);
}

.toc-item:active {
  background: var(--active-bg);
  transform: scale(0.99);
}

.toc-item.active {
  background: var(--active-bg);
  color: var(--text-primary);
  font-weight: 600;
}

.toc-item:hover .no {
  background: var(--active-bg);
}

/* ─── Content pane ─── */
.docs-content {
  flex: 1;
  min-width: 0;
  /* scroll-margin-top keeps the article top clear of the sticky page header
     when scrollIntoView jumps here after a TOC click */
  scroll-margin-top: 112px;
  background: var(--bg-card);
  border: 1px solid var(--border);
  border-radius: var(--r-lg);
  padding: 24px 28px 32px;
  color: var(--text-secondary);
  font-size: 14px;
  line-height: 1.75;
  transition: opacity 0.15s ease;
}

/* Dim (do not blank) the previous section while a new one loads */
.docs-content.is-loading {
  opacity: 0.45;
}

.docs-error {
  margin-top: 12px;
  font-size: 13px;
  color: var(--danger);
}

/* ─── Rendered markdown ───
   v-html content does not carry the scope attribute, so child selectors need
   :deep() (same approach as ModelDetail.vue's description styles). */
.docs-content :deep(h2) {
  font-size: 19px;
  font-weight: 700;
  color: var(--text-primary);
  margin: 22px 0 12px;
  padding-bottom: 8px;
  border-bottom: 1px solid var(--border);
}

.docs-content :deep(h2:first-child) {
  margin-top: 0;
}

.docs-content :deep(h3) {
  font-size: 15.5px;
  font-weight: 600;
  color: var(--text-primary);
  margin: 20px 0 8px;
}

.docs-content :deep(p) {
  margin: 10px 0;
}

.docs-content :deep(ul),
.docs-content :deep(ol) {
  margin: 10px 0;
  padding-left: 1.5em;
}

.docs-content :deep(li) {
  margin: 4px 0;
}

.docs-content :deep(strong) {
  color: var(--text-primary);
}

.docs-content :deep(a) {
  color: var(--accent-light);
  word-break: break-all;
}

/* Guard for future docs images: never wider than the content pane */
.docs-content :deep(img) {
  max-width: 100%;
  height: auto;
}

.docs-content :deep(code) {
  font-family: var(--font-mono);
  font-size: 12.5px;
  background: var(--surface);
  border: 1px solid var(--border);
  border-radius: 5px;
  padding: 1px 5px;
}

.docs-content :deep(pre) {
  background: var(--bg-primary);
  border: 1px solid var(--border);
  border-radius: 10px;
  padding: 14px 16px;
  overflow-x: auto;
  margin: 12px 0;
}

.docs-content :deep(pre code) {
  background: transparent;
  border: none;
  border-radius: 0;
  padding: 0;
  line-height: 1.6;
}

.docs-content :deep(table) {
  border-collapse: collapse;
  width: 100%;
  margin: 12px 0;
  font-size: 13.5px;
}

.docs-content :deep(th),
.docs-content :deep(td) {
  border: 1px solid var(--border);
  padding: 7px 12px;
  text-align: left;
}

.docs-content :deep(th) {
  background: var(--surface);
  color: var(--text-primary);
  font-weight: 600;
}

.docs-content :deep(blockquote) {
  margin: 10px 0;
  padding: 6px 14px;
  border-left: 3px solid var(--accent);
  background: var(--surface);
  color: var(--text-muted);
}

/* ─── Tablet portrait Track A (768–1099px, draft frames ⑰⑱): MASTER-DETAIL
       split — 250px chapter column + content pane (draft
       .split.md adapted: chapter titles are short, so the narrow column keeps
       the active highlight and pill row while handing the text pane ~430px+
       of readable width instead of the old 380px squeeze). The desktop TOC
       items already carry the draft .docrow look (number tile + title + desc
       cards), so only the geometry changes here. Scoped to the band with
       min-width: 768px so phones (<=767) and desktop (>=1100px) stay
       untouched. ─── */
@media (min-width: 768px) and (max-width: 1099px) {
  /* Frame ⑰ has no subtitle and no header action buttons: the pills move
     into the .srcbadges row below the header */
  .page-header {
    padding-bottom: 20px;
  }

  .page-subtitle,
  .docs-header-actions {
    display: none;
  }

  /* Master-detail split (draft .split.md adapted): narrow chapter column +
     1fr pane. Chapter titles ellipsize only in pathological cases — the
     250px column keeps every zh/en title fully visible at 14px/700. */
  .docs-layout {
    display: grid;
    grid-template-columns: 250px 1fr;
    gap: 18px;
    align-items: start;
  }

  /* The chapter column rides the grid width (the desktop 190px cap is a
     flex leftover) and stays sticky under the shorter tablet header */
  .docs-toc {
    width: auto;
    top: 76px;
  }

  .docs-content {
    min-width: 0;
    scroll-margin-top: 76px;
  }
}

/* ─── Phone (<=767px): the two-pane layout is replaced by the section list
       (the .srcbadges/.doclist blocks below, v-if-gated on the platform
       state). The desktop panes stay in the DOM (the shared content pipeline
       keeps feeding the badge pills) but do not render. ─── */
@media (max-width: 767px) {
  .docs-page {
    padding-bottom: 40px;
  }

  .page-header {
    padding-bottom: 20px;
  }

  .page-title {
    font-size: 24px;
  }

  /* Frame ⑰ has no subtitle and no header action buttons: the pills move
     into the .srcbadges row, the panes give way to the section list */
  .page-subtitle,
  .docs-header-actions,
  .docs-layout {
    display: none;
  }
}

/* ─── Phone section list (frame ⑰) — the markup is v-if-gated on
       platformState.isMobile, so these classes are inert on desktop tiers ─── */
.srcbadges {
  display: flex;
  align-items: center;
  gap: 8px;
  padding: 0 4px 14px;
}

.sb {
  display: inline-flex;
  align-items: center;
  font-size: 11px;
  font-weight: 800;
  border-radius: 999px;
  padding: 6px 12px;
  background: var(--grad-soft);
  color: var(--accent-light);
  border: none;
  font-family: inherit;
}

/* cached content: amber pill carrying the fetch time (mockup .sb.cache) */
.sb-cached {
  background: var(--warning-bg);
  color: var(--warning);
}

/* bundled fallback: neutral pill */
.sb-bundled {
  background: var(--surface-2);
  color: var(--text-secondary);
}

/* Action pills: surface island chips with a 44px hit target; the GitHub
   pill is right-aligned (mockup margin-left:auto) */
.sb-action {
  min-height: 44px;
  padding: 6px 16px;
  background: var(--bg-secondary);
  box-shadow: var(--shadow-island);
  color: var(--text-secondary);
  cursor: pointer;
}

.sb-action:disabled {
  opacity: 0.45;
  cursor: not-allowed;
}

.sb-github {
  margin-left: auto;
}

.doclist {
  display: flex;
  flex-direction: column;
}

.docrow {
  display: flex;
  align-items: center;
  gap: 14px;
  width: 100%;
  min-height: 44px;
  text-align: left;
  background: var(--bg-secondary);
  border: none;
  border-radius: var(--r-md);
  box-shadow: var(--shadow-island);
  padding: 15px 16px;
  margin-bottom: 11px;
  cursor: pointer;
  font-family: inherit;
}

.docrow .no {
  width: 34px;
  height: 34px;
  border-radius: var(--r-sm);
  background: var(--grad-soft);
  color: var(--accent-light);
  font-weight: 800;
  font-size: 13px;
  display: flex;
  align-items: center;
  justify-content: center;
  flex-shrink: 0;
}

.docrow .docrow-t {
  font-size: 14px;
  font-weight: 700;
  color: var(--text-primary);
  min-width: 0;
  overflow: hidden;
  text-overflow: ellipsis;
  white-space: nowrap;
}

.docrow .docrow-desc {
  font-size: 11.5px;
  font-weight: 500;
  color: var(--text-muted);
  margin-top: 2px;
  min-width: 0;
  overflow: hidden;
  text-overflow: ellipsis;
  white-space: nowrap;
}

.docrow .docrow-text {
  display: flex;
  flex-direction: column;
  flex: 1;
  min-width: 0;
}

.docrow .ar {
  margin-left: auto;
  color: var(--text-dim);
  font-size: 15px;
  flex-shrink: 0;
}

.docrow:hover {
  background: var(--hover-bg);
}

.docrow:active {
  background: var(--active-bg);
  transform: scale(0.99);
}

.docrow:hover .no {
  background: var(--active-bg);
}

</style>
