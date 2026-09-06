<template>
  <div class="page">
    <div class="sticky-top">
      <!-- Back + header -->
      <div class="page-header">
        <button class="back-btn" :aria-label="t('downloads.back')" @click="router.back()">
          <svg width="18" height="18" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2.2" stroke-linecap="round" stroke-linejoin="round">
            <polyline points="15 18 9 12 15 6"/>
          </svg>
          <span>{{ t('downloads.back') }}</span>
        </button>
        <h1 class="page-title">{{ decodedModelId }}</h1>
        <p class="page-subtitle">{{ fileCountLabel }}</p>
      </div>

      <!-- Sticky action bar (desktop/tablet): rides the pinned header band.
           Phone renders it inside .detail-scroll as a sticky island instead
           (frame ⑩ .stickbar) — the DOM placement branches on the shared
           platform state because position:sticky can only move an element
           within its own parent's box. -->
      <div v-if="showActionBar && !isMobile" class="action-bar">
        <span class="selected-count">{{ selectedCountLabel }}</span>
        <div class="action-actions">
          <button
            class="select-all-btn"
            :disabled="files.length === 0"
            @click="toggleSelectAll"
          >
            {{ allSelected ? t('downloads.deselectAll') : t('downloads.selectAll') }}
          </button>
          <button
            class="download-btn"
            :disabled="selectedCount === 0"
            @click="handleDownload"
          >
            <span aria-hidden="true">⬇</span> {{ t('downloads.downloadSelected') }}
          </button>
        </div>
      </div>
    </div>

    <!-- Content band: fills the window below the sticky header; only scrolls
         when the window is too short for the grid's internal columns -->
    <div class="detail-scroll">
      <!-- Phone-tier sticky island action bar (frame ⑩ .stickbar): same
           controls as the desktop band above, kept reachable while the file
           list scrolls -->
      <div v-if="showActionBar && isMobile" class="action-bar action-bar-sticky">
        <span class="selected-count">{{ selectedCountLabel }}</span>
        <div class="action-actions">
          <button
            class="select-all-btn"
            :disabled="files.length === 0"
            @click="toggleSelectAll"
          >
            {{ allSelected ? t('downloads.deselectAll') : t('downloads.selectAll') }}
          </button>
          <button
            class="download-btn"
            :disabled="selectedCount === 0"
            @click="handleDownload"
          >
            <span aria-hidden="true">⬇</span> {{ t('downloads.downloadSelected') }}
          </button>
        </div>
      </div>

      <!-- Error state -->
      <div v-if="loadError" class="error-card">
        <p class="error-text">{{ t('home.errorTitle') }}：{{ loadError }}</p>
        <button class="retry-btn" @click="retry">{{ t('home.retry') }}</button>
      </div>

      <!-- Two-column layout (mirrors Api.vue's .monitor-grid): description on
           the left, file list on the right, each scrolling independently -->
      <div class="detail-grid">
        <!-- Left column: model description -->
        <section class="desc-section">
          <h2 class="section-title">{{ t('downloads.descTitle') }}</h2>
          <div class="desc-body">
            <div v-if="descLoading" class="desc-loading">{{ t('downloads.loadingDesc') }}</div>
            <!-- Delegated link handler: external links open in the system browser,
                 the WebView never navigates (see lib/linkHandler.ts) -->
            <div v-else-if="description" class="desc-text" v-html="renderDescription(description)" @click="handleLinkClick"></div>
            <div v-else class="desc-empty">{{ t('downloads.noDesc') }}</div>
          </div>
        </section>

        <!-- Right column: file list -->
        <section class="files-section">
          <h2 class="section-title">{{ t('downloads.fileCount', { n: files.length }) }}</h2>
          <div class="files-body">
            <div v-if="filesLoading" class="files-loading">{{ t('downloads.loadingFiles') }}</div>
            <div v-else-if="sortedFilesList.length === 0" class="files-empty">{{ t('downloads.noFiles') }}</div>
            <div v-else class="files-list">
              <label
                v-for="f in sortedFilesList"
                :key="f.filename"
                class="file-item"
                :class="{ selected: selectedFiles.includes(f.filename) }"
              >
                <!-- Native input stays for a11y/keyboard; the phone tier hides
                     it visually and draws the design draft .ck box instead -->
                <input
                  type="checkbox"
                  class="file-check"
                  :checked="selectedFiles.includes(f.filename)"
                  @change="toggleFile(f.filename)"
                />
                <span class="file-ck" :class="{ on: selectedFiles.includes(f.filename) }" aria-hidden="true"></span>
                <span class="file-name" :title="f.filename">{{ f.filename }}</span>
                <span v-if="guessQuant(f.filename)" class="file-quant">{{ guessQuant(f.filename) }}</span>
                <span v-if="f.size" class="file-size">{{ formatBytes(f.size) }}</span>
              </label>
            </div>
          </div>
        </section>
      </div>
    </div>
  </div>
</template>

<script setup lang="ts">
import { ref, reactive, computed, onMounted } from 'vue'
import { useRouter, useRoute } from 'vue-router'
import { getModelFiles, getModelDescription, startDownload } from '../wails'
import { sortModelFiles, guessQuant, selectedBytes } from '../lib/modelFiles'
import { formatBytes } from '../lib/format'
import { renderDescription } from '../lib/markdown'
import { handleLinkClick } from '../lib/linkHandler'
import { searchResults } from '../lib/downloadsState'
import { t } from '../lib/i18n'
import { usePlatform } from '../lib/platform'

const router = useRouter()
const route = useRoute()

  // modelId may contain slashes (org/name), encoded with encodeURIComponent in URL
const decodedModelId = computed(() => decodeURIComponent(String(route.params.modelId || '')))
const modelId = computed(() => String(route.params.modelId || ''))

  // File list (interface fields use any to align with wails.ts return shape)
interface ModelFile {
  filename: string
  size?: number
}

const files = ref<ModelFile[]>([])
const filesLoading = ref(false)
const loadError = ref('')

  // Selection state
const selectedFiles = reactive<string[]>([])

  // Description
const description = ref('')
const descLoading = ref(false)

// File count label. The phone header (frame ⑩ .dthead .s) appends the model's
// pipeline tag when the detail was reached from a search (the payload carries
// no pipeline field of its own; deep links degrade to the bare file count).
const pipelineTag = computed(() => {
  const hit = searchResults.value.find((r) => r.modelId === decodedModelId.value)
  return hit?.pipelineTag || ''
})

const fileCountLabel = computed(() => {
  const n = files.value.length
  if (n <= 0) return ''
  const base = t('downloads.fileCount', { n })
  return pipelineTag.value ? `${base} · ${pipelineTag.value}` : base
})

// Selected count label. Tablet tiers (draft frames A⑩/B⑩) append the selected
// byte total ("已选 1 个 · 2.3 GB") when something is actually selected; phone
// and desktop keep the plain count word.
const selectedCount = computed(() => selectedFiles.length)
const selectedCountLabel = computed(() => {
  if (platformState.value.isTablet && selectedCount.value > 0) {
    const size = formatBytes(selectedBytes(selectedFiles, files.value))
    if (size) return t('downloads.selectedCountSize', { n: selectedCount.value, size })
  }
  return t('downloads.selectedCount', { n: selectedCount.value })
})

// Whether all files are selected
const allSelected = computed(() => files.value.length > 0 && selectedFiles.length === files.value.length)

// Viewport tier gate: the phone action bar is a different DOM placement
// (sticky island inside the scroll band), so it branches on platform state
const platformState = usePlatform()
const isMobile = computed(() => platformState.value.isMobile)

// Whether the action bar renders at all (either tier)
const showActionBar = computed(() => files.value.length > 0 || filesLoading.value)

const sortedFilesList = computed(() => sortModelFiles(files.value))

async function loadData() {
  const mid = modelId.value
  if (!mid) {
    loadError.value = 'Missing model ID'
    return
  }

  // Load file list and description in parallel, each with independent error handling
  filesLoading.value = true
  loadError.value = ''

  const filePromise = getModelFiles(mid)
    .then((result: ModelFile[]) => { files.value = result || [] })
    .catch((err: any) => { if (!loadError.value) loadError.value = err?.message || String(err) })
    .finally(() => { filesLoading.value = false })

  const descPromise = getModelDescription(mid)
    .then((text: string) => { if (text) description.value = text })
    .catch(() => {})
    .finally(() => { descLoading.value = false })

  descLoading.value = true
  await Promise.all([filePromise, descPromise])
}

function toggleFile(filename: string) {
  const idx = selectedFiles.indexOf(filename)
  if (idx >= 0) {
    selectedFiles.splice(idx, 1)
  } else {
    selectedFiles.push(filename)
  }
}

function toggleSelectAll() {
  if (allSelected.value) {
    selectedFiles.length = 0
  } else {
    selectedFiles.length = 0
    for (const f of files.value) selectedFiles.push(f.filename)
  }
}

async function handleDownload() {
  if (selectedFiles.length === 0) return
  try {
    await startDownload(modelId.value, [...selectedFiles])
    router.push('/models/download')
  } catch {
    // On failure, silently stay on this page (consistent with existing page behavior)
  }
}

function retry() {
  loadData()
}

onMounted(() => {
  loadData()
})
</script>

<style scoped>
/* Fixed-viewport layout: the router cannot mark this page fixed (route meta
   is outside this file), so the page fills the window below the titlebar band
   on its own — the exact .page-fixed height contract from global.css
   (--titlebar-h + --mobile-nav-height, so the phone tier reserves the floating
   bottom nav instead of scrolling under it) plus the .content-area
   dock-reserve term, so the page sits exactly inside the shared content box
   and the content area itself never scrolls. On desktop both mobile vars
   resolve to 0px, leaving the previous 100vh - 36px - dock contract. The
   sticky header band stays pinned above the scrolling detail columns; its
   variable height (conditional action bar, wrappable title) is resolved by
   flexbox, not a pixel constant. */
.page {
  padding: 0 48px 0;
  height: calc(100vh - var(--titlebar-h, 36px) - var(--mobile-nav-height, 0px) - var(--dock-reserve, 0px));
  /* dvh twin: progressive override for dynamic viewports (soft keyboard);
     no-op where dvh is unsupported (same as .page-fixed) */
  height: calc(100dvh - var(--titlebar-h, 36px) - var(--mobile-nav-height, 0px) - var(--dock-reserve, 0px));
  display: flex;
  flex-direction: column;
  /* Match .content-area's padding-bottom transition so appearing/disappearing
     TaskDock pill never leaves the page taller than the content box */
  transition: height 0.2s ease;
}

/* Keep the sticky header band from compressing inside the fixed-height page */
.sticky-top {
  flex-shrink: 0;
}

.page-header {
  padding-bottom: 20px;
}

.back-btn {
  display: inline-flex;
  align-items: center;
  gap: 6px;
  background: transparent;
  border: none;
  color: var(--text-muted);
  font-size: 13px;
  cursor: pointer;
  padding: 4px 0;
  margin-bottom: 12px;
  border-radius: 6px;
  transition: color 0.2s;
}

.back-btn:hover {
  color: var(--text-secondary);
}

.page-title {
  font-size: 24px;
  font-weight: 700;
  color: var(--text-primary);
  margin: 0 0 4px;
  letter-spacing: -0.5px;
  line-height: 1.2;
  word-break: break-all;
}

.page-subtitle {
  font-size: 13px;
  color: var(--text-dim);
  margin: 0;
}

/* ─── Action bar ─── */
.action-bar {
  display: flex;
  align-items: center;
  justify-content: space-between;
  gap: 12px;
  padding: 10px 0;
  margin-top: 12px;
  border-top: 1px solid var(--border);
}

/* Desktop glass sticky action bar (design draft D11): glassmorphism pill bar */
@media (min-width: 1100px) {
  .action-bar {
    position: sticky;
    top: 0;
    z-index: 10;
    background: var(--glass);
    border: 1px solid var(--glass-line);
    border-radius: var(--r-md);
    padding: 10px 16px;
    margin-top: 0;
    margin-bottom: 16px;
    box-shadow: none;
  }
}

.selected-count {
  font-size: 13px;
  color: var(--text-dim);
}

.action-actions {
  display: flex;
  gap: 8px;
}

.select-all-btn {
  padding: 7px 16px;
  background: transparent;
  border: 1px solid var(--overlay-10);
  border-radius: 8px;
  color: var(--text-muted);
  font-size: 13px;
  cursor: pointer;
  transition: all 0.2s;
}

.select-all-btn:hover:not(:disabled) {
  border-color: var(--overlay-20);
  color: var(--text-primary);
}

.select-all-btn:active:not(:disabled) {
  background: var(--overlay-8);
  transform: scale(0.98);
}

.select-all-btn:disabled {
  opacity: 0.4;
  cursor: default;
}

.download-btn {
  padding: 7px 22px;
  /* Primary action = solid brand gradient + white + glow (design draft v2 rule 3) */
  background: var(--grad);
  color: #fff;
  border: none;
  border-radius: var(--r-md);
  font-size: 13px;
  font-weight: 600;
  cursor: pointer;
  transition: all 0.2s;
  box-shadow: 0 6px 16px rgba(124, 92, 246, 0.42);
}

.download-btn:hover:not(:disabled) {
  box-shadow: 0 8px 20px rgba(124, 92, 246, 0.5);
}

.download-btn:active:not(:disabled) {
  transform: scale(0.98);
  box-shadow: none;
}

.download-btn:disabled {
  opacity: 0.4;
  cursor: default;
}

/* ─── Content band: flexes under the sticky header, scrolls only when the
     window is too short; the error card and the grid share it ─── */
.detail-scroll {
  flex: 1;
  min-height: 0;
  overflow-y: auto;
  display: flex;
  flex-direction: column;
}

/* ─── Error ─── */
.error-card {
  display: flex;
  align-items: center;
  gap: 16px;
  padding: 14px 18px;
  background: rgba(239, 68, 68, 0.06);
  border: 1px solid rgba(239, 68, 68, 0.15);
  border-radius: 10px;
  margin-bottom: 24px;
}

.error-text {
  flex: 1;
  font-size: 13px;
  color: #f87171;
  margin: 0;
}

.retry-btn {
  padding: 6px 16px;
  background: rgba(99, 102, 241, 0.1);
  color: var(--accent-light);
  border: 1px solid rgba(99, 102, 241, 0.2);
  border-radius: 6px;
  font-size: 12px;
  font-weight: 600;
  cursor: pointer;
  transition: all 0.2s;
  white-space: nowrap;
}

.retry-btn:hover {
  background: rgba(99, 102, 241, 0.2);
}

/* ─── Two-column grid: description (left) + files (right) ─── */
.detail-grid {
  display: grid;
  /* minmax(0, Nfr): floor the tracks at 0 (not auto) so long unbreakable
     strings (model filenames, README URLs) cannot widen a column past its
     share; the narrow-viewport block at the end collapses the grid */
  grid-template-columns: minmax(0, 6fr) minmax(0, 4fr);
  /* The single row shares the grid height exactly; min-height: 0 on the
     columns lets excess content scroll inside each column instead of
     stretching the row */
  grid-template-rows: minmax(0, 1fr);
  gap: 16px;
  /* Fill the band below the sticky header so both columns scroll internally
     on typical window sizes (no minimum floor: both columns are plain
     scrollable lists, so nothing gets crushed on short windows) */
  flex: 1;
  min-height: 0;
  margin-bottom: 16px;
}

/* ─── Columns: pinned title + internally scrolling body (same structure as
     Api.vue's .log-panel: header flex-shrink: 0, body flex: 1 + min-height: 0
     + overflow-y: auto) ─── */
.desc-section,
.files-section {
  display: flex;
  flex-direction: column;
  min-height: 0;
  min-width: 0;
}

.section-title {
  font-size: 14px;
  font-weight: 600;
  color: var(--text-secondary);
  margin: 0 0 12px;
  flex-shrink: 0;
}

.desc-body,
.files-body {
  flex: 1;
  min-height: 0;
  overflow-y: auto;
}

.desc-loading,
.desc-empty {
  font-size: 13px;
  color: var(--text-dim);
  padding: 10px 0;
}

.desc-text {
  font-size: 13px;
  line-height: 1.7;
  color: var(--text-dim);
  padding: 12px 16px;
  background: var(--bg-card);
  border-radius: 8px;
  word-break: break-all;
}

/* ─── Markdown-rendered description content ───
   v-html content does not carry the scope attribute, so child selectors need
   :deep(). Compact spacing so rendered blocks keep the card look (same
   approach as Chat.vue's .markdown-body rules). */
.desc-text :deep(h1),
.desc-text :deep(h2),
.desc-text :deep(h3),
.desc-text :deep(h4) {
  margin: 12px 0 6px;
  line-height: 1.4;
  color: var(--text-secondary);
}

.desc-text :deep(h1) { font-size: 16px; }
.desc-text :deep(h2) { font-size: 15px; }
.desc-text :deep(h3),
.desc-text :deep(h4) { font-size: 14px; }

.desc-text :deep(h1:first-child),
.desc-text :deep(h2:first-child),
.desc-text :deep(h3:first-child),
.desc-text :deep(h4:first-child) {
  margin-top: 0;
}

.desc-text :deep(p) {
  margin: 6px 0;
}

.desc-text :deep(ul),
.desc-text :deep(ol) {
  margin: 6px 0;
  padding-left: 1.4em;
}

.desc-text :deep(li) {
  margin: 2px 0;
}

.desc-text :deep(a) {
  color: var(--accent-light);
  word-break: break-all;
}

.desc-text :deep(code) {
  background: var(--surface);
  border-radius: 4px;
  padding: 0.5px 5px;
  font-family: var(--font-mono);
  font-size: 12px;
}

/* README banners are often wider than the description column */
.desc-text :deep(img) {
  max-width: 100%;
  height: auto;
}

/* Fenced code blocks scroll horizontally instead of stretching the column */
.desc-text :deep(pre) {
  overflow-x: auto;
}

/* ─── Files ─── */
.files-loading,
.files-empty {
  font-size: 13px;
  color: var(--text-dim);
  padding: 10px 0;
}

.files-list {
  display: flex;
  flex-direction: column;
  gap: 4px;
}

.file-item {
  display: flex;
  align-items: center;
  /* Two-axis gap: tighter row gap for the wrapped quant/size line */
  gap: 6px 10px;
  /* Let the quant tag and size drop onto a second line when the 4fr column
     is too narrow to fit them beside the filename */
  flex-wrap: wrap;
  padding: 8px 12px;
  border-radius: 8px;
  cursor: pointer;
  transition: background 0.15s;
  font-size: 13px;
}

.file-item:hover {
  background: var(--bg-card);
}

.file-item.selected {
  background: rgba(99, 102, 241, 0.08);
}

.file-item input {
  accent-color: var(--accent);
  flex-shrink: 0;
}

/* design draft .ck custom checkbox: desktop keeps the native input; the phone tier
   hides it visually and draws the box instead (aria-hidden decoration) */
.file-ck {
  display: none;
}

.file-name {
  flex: 1 1 auto;
  /* Truncate long filenames with an ellipsis (full name exposed via the
     title tooltip) instead of wrapping and blowing out the 4fr column; the
     min-width floor pushes the quant tag + size to a second line first */
  min-width: 140px;
  overflow: hidden;
  text-overflow: ellipsis;
  white-space: nowrap;
  color: var(--text-secondary);
}

.file-size {
  color: var(--text-dim);
  font-size: 11px;
  flex-shrink: 0;
}

.file-quant {
  background: rgba(34, 197, 94, 0.1);
  color: var(--success);
  padding: 1px 7px;
  border-radius: 4px;
  font-size: 10px;
  font-weight: 600;
  flex-shrink: 0;
  letter-spacing: 0.3px;
}

/* ─── Narrow viewports (< 1100px, same breakpoint as Api.vue): collapse to a
     single column with the description first, files below. The page releases
     its fixed height so the shared content area scrolls naturally and both
     columns flow unbounded (the sticky header pins via the global
     .sticky-top rule) ─── */
@media (max-width: 1099px) {
  .page {
    height: auto;
  }

  .detail-scroll {
    flex: none;
    overflow: visible;
  }

  .detail-grid {
    grid-template-columns: minmax(0, 1fr);
    grid-template-rows: none;
    /* Content-driven height: the content area scrolls, the grid does not
       stretch; the row gap separates the stacked sections */
    gap: 24px;
    flex: none;
    min-height: 0;
    margin-bottom: 24px;
  }

  .desc-body,
  .files-body {
    flex: none;
    overflow: visible;
  }
}

/* ─── Tablet portrait (768..1099px, design draft frame A⑩): the selection
       count + download action ride a GLASS stickbar in the pinned header
       band; the file list becomes a two-column card grid placed ABOVE the
       description island (draft order: files, then the intro island). ─── */
@media (min-width: 768px) and (max-width: 1099px) {
  /* Draft ⑩ .stickbar: glass pill bar (same skin as the desktop ≥1100 bar) */
  .action-bar {
    background: var(--glass);
    backdrop-filter: blur(22px) saturate(1.6);
    border: 1px solid var(--glass-line);
    border-radius: var(--r-md);
    padding: 10px 14px;
    margin-top: 0;
    margin-bottom: 16px;
  }

  /* Draft .stickbar .all: accent text link, not a bordered chip */
  .select-all-btn {
    background: transparent;
    border: none;
    color: var(--accent-light);
    font-weight: 700;
  }

  .select-all-btn:hover:not(:disabled),
  .select-all-btn:active:not(:disabled) {
    background: transparent;
    border: none;
    color: var(--accent-light);
    transform: none;
  }

  /* Draft .stickbar .dl: solid gradient pill */
  .download-btn {
    background: var(--grad);
    color: #fff;
    border: none;
    border-radius: 999px;
    font-weight: 800;
    box-shadow: none;
  }

  .download-btn:hover:not(:disabled),
  .download-btn:active:not(:disabled) {
    background: var(--grad);
    transform: none;
  }

  /* Draft order: the file grid comes first, the intro island stacks below */
  .detail-grid {
    grid-template-columns: minmax(0, 1fr);
  }

  .files-section {
    order: -1;
  }

  /* Draft ⑩ .grid2 of .file cards: two columns of free-standing cards */
  .files-list {
    display: grid;
    grid-template-columns: repeat(2, minmax(0, 1fr));
    gap: 9px;
    align-items: start;
  }

  .file-item {
    position: relative;
    flex-wrap: nowrap;
    min-height: 44px;
    padding: 12px 14px;
    background: var(--bg-secondary);
    border: 1px solid var(--border);
    border-radius: var(--r-md);
    box-shadow: var(--shadow-island);
  }

  .file-item:hover {
    background: var(--bg-secondary);
  }

  /* Drawn 20px .ck checkbox replaces the visually hidden native input (kept
     for keyboard/a11y; the row is a wrapping label) */
  .file-check {
    position: absolute;
    width: 1px;
    height: 1px;
    opacity: 0;
    margin: 0;
    pointer-events: none;
  }

  .file-ck {
    display: inline-block;
    position: relative;
    width: 20px;
    height: 20px;
    border: 2px solid #c9cdde;
    border-radius: 6px;
    flex-shrink: 0;
  }

  html[data-theme='dark'] .file-ck {
    border-color: #3d4759;
  }

  .file-ck.on {
    background: var(--grad);
    border-color: transparent;
  }

  .file-ck.on::after {
    content: '';
    position: absolute;
    left: 5px;
    top: 1.5px;
    width: 5px;
    height: 9px;
    border: solid #fff;
    border-width: 0 2px 2px 0;
    transform: rotate(42deg);
  }

  .file-check:focus-visible ~ .file-ck {
    outline: 2px solid var(--accent);
    outline-offset: 2px;
  }

  /* Draft .file .fn: bold mono filename truncating inside its card */
  .file-name {
    flex: 0 1 auto;
    min-width: 0;
    font-family: var(--font-mono);
    font-size: 12px;
    font-weight: 700;
    color: var(--text-primary);
  }

  .file-size {
    margin-left: auto;
    font-size: 11.5px;
    color: var(--text-muted);
    font-family: var(--font-mono);
  }

  /* Draft .qbadge: purple-on-gradient-soft quant tag */
  .file-quant {
    background: var(--grad-soft);
    color: var(--accent-light);
    border-radius: 6px;
    font-size: 10px;
    font-weight: 800;
  }

  /* Draft ⑩ .island: the description becomes a free-standing island card */
  .desc-section {
    background: var(--bg-secondary);
    border: 1px solid var(--border);
    border-radius: var(--r-lg);
    box-shadow: var(--shadow-island);
    padding: 18px 20px;
  }

  .desc-section .section-title {
    font-size: 13px;
    font-weight: 700;
    letter-spacing: 0.3px;
    color: var(--text-muted);
  }

  .desc-text {
    background: transparent;
    padding: 0;
  }
}

/* ─── Phone (<=767px, design draft frame ⑩): circular chevron-only back button,
       compact 17px/800 title, island action bar that sticks below the status
       bar while the file list scrolls, drawn .ck checkboxes, hairline file
       rows, and the model-intro island with gradient blockquote callouts. ─── */
@media (max-width: 767px) {
  /* Frame ⑩ .dthead .bk: 44px surface circle, chevron only (the text span
     hides; the button keeps its aria-label) */
  .back-btn {
    width: 44px;
    height: 44px;
    padding: 0;
    justify-content: center;
    margin-bottom: 8px;
    border-radius: 50%;
    background: var(--bg-secondary);
    box-shadow: var(--shadow-island);
  }

  .back-btn span {
    display: none;
  }

  /* Frame ⑩ .dthead .t: 17px/800 single line with ellipsis (long org/name
     ids truncate instead of the desktop break-all wrap) */
  .page-title {
    font-size: 17px;
    font-weight: 800;
    letter-spacing: -0.3px;
    white-space: nowrap;
    overflow: hidden;
    text-overflow: ellipsis;
    word-break: normal;
  }

  /* Frame ⑩ .dthead .s: "{n} 个文件 · {pipeline}" 11.5px sub line */
  .page-subtitle {
    font-size: 11.5px;
  }

  /* Frame ⑩ .stickbar: surface island (radius 22, island shadow) pinned
     below the safe-area inset so 下载所选 stays reachable while the list
     scrolls. Rendered inside .detail-scroll by the isMobile template branch,
     so position:sticky moves within that band's full height. */
  .action-bar-sticky {
    position: sticky;
    top: var(--safe-area-top);
    z-index: 15;
    justify-content: flex-start;
    gap: 10px;
    padding: 10px 14px;
    margin-top: 0;
    margin-bottom: 14px;
    background: var(--bg-secondary);
    border: none;
    border-radius: var(--r-lg);
    box-shadow: var(--shadow-island);
  }

  .action-bar-sticky .action-actions {
    margin-left: auto;
  }

  .action-bar-sticky .selected-count {
    font-size: 12px;
    font-weight: 600;
    color: var(--text-secondary);
    flex-shrink: 0;
  }

  /* Frame ⑩ .stickbar .all: accent text link, not a bordered chip */
  .action-bar-sticky .select-all-btn {
    min-height: 44px;
    padding: 10px 12px 10px 4px;
    background: transparent;
    border: none;
    color: var(--accent-light);
    font-size: 12px;
    font-weight: 700;
  }

  .action-bar-sticky .select-all-btn:hover:not(:disabled) {
    background: transparent;
    border: none;
    color: var(--accent-light);
  }

  /* Frame ⑩ .stickbar .dl: solid gradient pill with the purple glow */
  .action-bar-sticky .download-btn {
    min-height: 44px;
    padding: 10px 18px;
    background: var(--grad);
    color: #fff;
    border: none;
    border-radius: 999px;
    font-size: 12.5px;
    font-weight: 800;
    box-shadow: none;
  }

  .action-bar-sticky .download-btn:hover:not(:disabled) {
    background: var(--grad);
  }

  /* Frame ⑩ .file .ck: drawn 20px checkbox replaces the visually hidden
     native input (kept for keyboard/a11y; the row is a wrapping label) */
  .file-item {
    position: relative;
    flex-wrap: nowrap;
    min-height: 44px;
    padding: 10px 12px;
  }

  .file-check {
    position: absolute;
    width: 1px;
    height: 1px;
    opacity: 0;
    margin: 0;
    pointer-events: none;
  }

  .file-ck {
    display: inline-block;
    position: relative;
    width: 20px;
    height: 20px;
    border: 2px solid #c9cdde;
    border-radius: 6px;
    flex-shrink: 0;
  }

  html[data-theme='dark'] .file-ck {
    border-color: #3d4759;
  }

  .file-ck.on {
    background: var(--grad);
    border-color: transparent;
  }

  .file-ck.on::after {
    content: '';
    position: absolute;
    left: 5px;
    top: 1.5px;
    width: 5px;
    height: 9px;
    border: solid #fff;
    border-width: 0 2px 2px 0;
    transform: rotate(42deg);
  }

  .file-check:focus-visible ~ .file-ck {
    outline: 2px solid var(--accent);
    outline-offset: 2px;
  }

  /* Frame ⑩ .file: hairline separators instead of the desktop gap + hover
     card; bold dark filename left, muted size right */
  .files-list {
    gap: 0;
  }

  .file-item + .file-item {
    border-top: 1px solid var(--border);
  }

  .file-item:hover {
    background: transparent;
  }

  .file-item.selected {
    background: rgba(99, 102, 241, 0.08);
  }

  .file-name {
    flex: 0 1 auto;
    min-width: 0;
    font-family: var(--font-mono);
    font-size: 12px;
    font-weight: 700;
    color: var(--text-primary);
  }

  .file-size {
    margin-left: auto;
    font-size: 11.5px;
    color: var(--text-muted);
  }

  /* Frame ⑩ .qbadge: purple-on-gradient-soft quant tag (replaces green) */
  .file-quant {
    background: var(--grad-soft);
    color: var(--accent-light);
    border-radius: 6px;
    font-size: 10px;
    font-weight: 800;
  }

  /* Frame ⑩ .docpost: intro island body at the reading size + gradient
     blockquote callouts in the rendered README */
  .desc-text {
    font-size: 13.5px;
    line-height: 1.75;
  }

  .desc-text :deep(blockquote) {
    margin: 8px 0;
    padding: 10px 14px;
    border-radius: var(--r-sm);
    background: var(--grad-soft);
    color: #5b21b6;
  }

  html[data-theme='dark'] .desc-text :deep(blockquote) {
    color: #d8b4fe;
  }

  .error-card {
    flex-wrap: wrap;
  }

  .retry-btn {
    min-height: 44px;
  }
}

</style>
