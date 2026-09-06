<template>
  <div class="downloads-tab">
    <!-- Search bar (non-sticky: the merged page shell owns the sticky header).
         Frame ③: the input becomes a floating pill capsule; the search action
         rides the brand gradient, the task-manager entry stays a ghost pill. -->
    <div class="search-bar">
      <div class="search-input-wrap">
        <svg class="search-icon" width="18" height="18" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round" stroke-linejoin="round">
          <circle cx="11" cy="11" r="8"/><line x1="21" y1="21" x2="16.65" y2="16.65"/>
        </svg>
        <input
          v-model="searchQuery"
          class="search-input"
          :placeholder="t('downloads.searchPlaceholder')"
          @keydown.enter="doSearch"
        />
      </div>
      <button class="pill-btn" @click="doSearch" :disabled="searching || !searchQuery">
        {{ searching ? t('downloads.searching') : t('downloads.search') }}
      </button>
      <button class="pill-btn ghost" @click="showTasksModal = true" :title="t('downloads.downloadTitle')">
        {{ t('downloads.tasks') }}
        <span v-if="activeTaskCount > 0" class="task-badge">{{ activeTaskCount }}</span>
      </button>
    </div>

    <!-- Body band: results LEFT; elsewhere .dl-rail dissolves via
         display:contents so the pinned block stays the flat first child above
         the results, exactly as before. -->
    <div class="dl-body">
      <aside class="dl-rail" :aria-label="t('downloads.railAria')">
        <!-- In-flight downloads pinned on top as progress cards (frame ⑧ .dlcard):
             data rides the existing getDownloadTasks polling unchanged; tapping a
             card opens the task manager modal where pause/resume/cancel live.
             The card is a role="button" div (not a native button) for a11y. -->
        <div v-if="activeTaskList.length > 0" class="dl-pinned" role="region" :aria-label="t('downloads.inProgress')">
          <div
            v-for="task in activeTaskList"
            :key="task.id"
            class="dl-card"
            role="button"
            tabindex="0"
            @click="showTasksModal = true"
            @keydown.enter.prevent="showTasksModal = true"
            @keydown.space.prevent="showTasksModal = true"
            :title="t('downloads.downloadTitle')"
          >
            <span class="dl-card-top">
              <span class="dl-name">{{ task.fileName }}</span>
              <span class="dl-pct">{{ task.progress }}%</span>
            </span>
            <span class="dl-bar">
              <span class="dl-fill" :class="taskBarClass(task.status)" :style="{ width: task.progress + '%' }"></span>
            </span>
            <span class="dl-card-meta">
              <span v-if="task.sizeHuman && task.sizeHuman !== '0 B'" class="dl-size">{{ task.sizeHuman }}</span>
              <span v-if="etaText(task)" class="dl-eta">{{ etaText(task) }}</span>
              <!-- Frame ⑧ meta tail "… · 12.4 MB/s": while a transfer speed is
                   measurable the pinned card ends with the speed; the status word
                   is only the fallback. Desktop always keeps the status word. -->
              <span v-if="speedTail(task)" class="dl-speed">{{ formatSpeed(task.speed) }}</span>
              <span v-else class="dl-status" :class="'status-' + task.status">{{ statusMap[task.status] || task.status }}</span>
            </span>
          </div>
        </div>
      </aside>

      <!-- Search results.
           Header split is phone-tier DOM (design draft frame ⑧): plain label left +
           accent count right. Desktop/tablet keep the inline "(n)" form. -->
      <div v-if="searchResults.length > 0" class="results-section">
        <h2 v-if="!platformState.isMobile" class="section-heading">{{ t('downloads.resultsTitle', { n: searchResults.length }) }}</h2>
        <div v-else class="section-head-row">
          <h2 class="section-heading">{{ t('downloads.resultsLabel') }}</h2>
          <span class="section-count">{{ t('downloads.resultsCount', { n: searchResults.length }) }}</span>
        </div>
        <div class="results-grid">
          <div
            v-for="r in searchResults"
            :key="r.modelId"
            class="result-card"
            @click="goToDetail(r.modelId)"
            :title="t('downloads.viewDetail')"
          >
            <div class="result-main">
              <!-- Gradient icon brick (frame ③ .tile): 48px rounded tile on the
                   soft brand gradient wash, cube glyph in the accent color -->
              <div class="result-tile">
                <svg width="22" height="22" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round" stroke-linejoin="round">
                  <path d="M12 2l8 4.5v9L12 20l-8-4.5v-9L12 2z"/><path d="M12 11L4.5 6.8M12 11l7.5-4.2M12 11v8.5"/>
                </svg>
              </div>
              <div class="result-info">
                <h3 class="result-name">{{ r.modelId }}</h3>
                <div class="result-meta">
                  <span v-if="r.author" class="result-chip">{{ r.author }}</span>
                  <span v-if="r.pipelineTag" class="result-chip">{{ r.pipelineTag }}</span>
                  <span v-if="modelSizes[r.modelId]" class="result-chip">{{ formatBytes(modelSizes[r.modelId]) }}</span>
                </div>
              </div>
              <!-- Round gradient download button (frame ③ .dlbtn): entry to the
                   existing download flow (model detail page) -->
              <button class="dl-round" :title="t('downloads.viewDetail')" @click.stop="goToDetail(r.modelId)">
                <svg width="16" height="16" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2.6" stroke-linecap="round" stroke-linejoin="round">
                  <path d="M12 4v12M6 12l6 6 6-6"/>
                </svg>
              </button>
            </div>
          </div>
        </div>
      </div>

      <!-- No results -->
      <div v-else-if="searched && !searching" class="empty-state">
        <h2>{{ t('downloads.noResults') }}</h2>
        <p>{{ t('downloads.tryOther') }}</p>
      </div>

      <!-- Empty-search guidance (portrait tablet band, draft frame A⑧
           adaptation): before any search the area below the pinned progress
           card is a bare void — this card offers a hint plus tappable keyword
           chips that fill the search box. Gated to the portrait band via the
           shared platform state (showsSearchGuidance), so phone and desktop
           render exactly as before. Hidden while a search is in flight and
           never mixed with the no-results state above (that branch matches
           first). -->
      <div v-else-if="showsSearchGuidance" class="empty-state search-empty">
        <div class="search-empty-glyph" aria-hidden="true">
          <svg width="34" height="34" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="1.8" stroke-linecap="round" stroke-linejoin="round">
            <circle cx="11" cy="11" r="8"/><line x1="21" y1="21" x2="16.65" y2="16.65"/>
          </svg>
        </div>
        <h2>{{ t('downloads.searchEmptyHint') }}</h2>
        <p class="search-empty-lead">{{ t('downloads.searchEmptyChips') }}</p>
        <div class="search-chips">
          <button
            v-for="s in SEARCH_SUGGESTIONS"
            :key="s"
            type="button"
            class="search-chip"
            @click="applySuggestion(s)"
          >{{ s }}</button>
        </div>
      </div>
    </div>

    <!-- Download tasks modal -->
    <transition name="modal-fade">
      <div v-if="showTasksModal" class="modal-overlay" @click.self="showTasksModal = false">
        <div class="modal-panel">
          <div class="modal-header">
            <h2 class="modal-title">{{ t('downloads.managerTitle') }}</h2>
            <button class="modal-close" @click="showTasksModal = false" :title="t('downloads.close')">✕</button>
          </div>
          <div class="modal-body">
            <!-- Tabs: in-flight tasks vs finished download history -->
            <div class="task-tabs">
              <button class="task-tab" :class="{ active: taskTab === 'tasks' }" @click="taskTab = 'tasks'">
                {{ t('downloads.tasksTitle', { n: activeTaskList.length }) }}
              </button>
              <button class="task-tab" :class="{ active: taskTab === 'history' }" @click="taskTab = 'history'">
                {{ t('downloads.historyTitle', { n: historyTaskList.length }) }}
              </button>
            </div>
            <div v-if="displayedTaskList.length === 0" class="tasks-empty">
              {{ taskTab === 'tasks' ? t('downloads.noActiveTasks') : t('downloads.historyEmpty') }}
            </div>
            <div v-else class="task-list">
              <div v-for="task in displayedTaskList" :key="task.id" class="task-card">
                <div class="task-info">
                  <span class="task-name">{{ task.fileName }}</span>
                  <span class="task-model">{{ task.modelId }}</span>
                </div>
                <div class="task-bar-wrap">
                  <div class="task-bar">
                    <div
                      class="task-fill"
                      :class="taskBarClass(task.status)"
                      :style="{ width: task.progress + '%' }"
                    ></div>
                  </div>
                  <span class="task-percent">{{ task.progress }}%</span>
                </div>
                <div class="task-meta">
                  <span class="task-status" :class="'status-' + task.status">{{ statusMap[task.status] || task.status }}</span>
                  <span v-if="task.status === 'downloading' && task.speed > 0" class="task-speed">{{ formatSpeed(task.speed) }}</span>
                  <span class="task-size" v-if="task.sizeHuman && task.sizeHuman !== '0 B'">{{ task.sizeHuman }}</span>
                </div>
                <div class="task-error" v-if="task.error">
                  <span>{{ task.error }}</span>
                </div>
                <div class="task-actions">
                  <button
                    v-if="task.status === 'downloading'"
                    class="task-btn pause-btn"
                    @click="pauseTask(task.id)"
                  >⏸ {{ t('downloads.pause') }}</button>
                  <button
                    v-if="task.status === 'paused'"
                    class="task-btn resume-btn"
                    @click="resumeTask(task.id)"
                  >▶ {{ t('downloads.resume') }}</button>
                  <button
                    v-if="task.status === 'error'"
                    class="task-btn retry-btn"
                    @click="retryTask(task.id)"
                  >↻ {{ t('downloads.retry') }}</button>
                  <button
                    v-if="task.status === 'downloading' || task.status === 'paused' || task.status === 'queued' || task.status === 'error'"
                    class="task-btn cancel-btn"
                    @click="cancelTask(task.id)"
                  >✕ {{ t('downloads.cancel') }}</button>
                </div>
              </div>
            </div>
          </div>
        </div>
      </div>
    </transition>
  </div>
</template>

<script setup lang="ts">
import { ref, reactive, computed, onMounted, onUnmounted } from 'vue'
import { useRouter } from 'vue-router'
import {
  searchDownloads, getModelMaxFileSize, getDownloadTasks,
  cancelDownloadTask, retryDownloadTask, pauseDownloadTask, resumeDownloadTask, refreshModels
} from '../wails'
import { formatSpeed, formatBytes } from '../lib/format'
import { LatestOnly } from '../lib/latestOnly'
import { hasActiveTask, countActiveTasks, visibleTasks, activeTaskItems, finishedTaskItems } from '../lib/taskStatus'
import { LimitedQueue } from '../lib/limitedQueue'
import {
  searchQuery,
  searched,
  searchResults,
  modelSizes,
  HFResult,
  SEARCH_SUGGESTIONS,
  showsEmptySearchGuidance,
} from '../lib/downloadsState'
import { t } from '../lib/i18n'
import { usePlatform } from '../lib/platform'

interface DlTask {
  id: string
  modelId: string
  fileName: string
  status: string
  progress: number
  total: number
  downloaded: number
  sizeHuman: string
  error: string
  source: string
  speed: number
}

const router = useRouter()

// Viewport tier gate: the phone-only results-header split (frame ⑧) swaps DOM
// structure, so it must branch on the shared platform state, not a media query
const platformState = usePlatform()

// Empty-search guidance gate (portrait tablet band only, 768..1099px): the
// band predicate rides the shared platform state so it tracks window resizes
// exactly like the CSS media queries; a search in flight hides the card.
const showsSearchGuidance = computed(
  () =>
    !searching.value &&
    showsEmptySearchGuidance(platformState.value.isTablet),
)

// Suggestion chip click: fills the search box only — running the search stays
// an explicit action on the search button
function applySuggestion(keyword: string): void {
  searchQuery.value = keyword
}

const searching = ref(false)
// Concurrency-limited queue for search cards' batched size requests
let sizeQueue = new LimitedQueue(4)

const tasks = ref<DlTask[]>([])
let taskPollTimer: ReturnType<typeof setInterval> | null = null
let lastDoneCount = 0

// Download tasks modal
const showTasksModal = ref(false)
const activeTaskCount = computed(() => countActiveTasks(tasks.value))
const visibleTaskList = computed(() => visibleTasks(tasks.value))
// Tabbed split: in-flight tasks on one tab, finished downloads (done/error)
// on a history tab, so past downloads no longer mix into the task list
const activeTaskList = computed(() => activeTaskItems(visibleTaskList.value))
const historyTaskList = computed(() => finishedTaskItems(visibleTaskList.value))
const taskTab = ref<'tasks' | 'history'>('tasks')
const displayedTaskList = computed(() => (taskTab.value === 'tasks' ? activeTaskList.value : historyTaskList.value))

const statusMap = computed<Record<string, string>>(() => ({
  queued: t('downloads.statusQueued'),
  downloading: t('downloads.statusDownloading'),
  paused: t('downloads.statusPaused'),
  done: t('downloads.statusDone'),
  error: t('downloads.statusError'),
  cancelled: t('downloads.statusCancelled'),
}))

function taskBarClass(status: string): string {
  if (status === 'paused') return 'paused'
  if (status === 'error') return 'error-fill'
  return ''
}

// Remaining-time estimate for the pinned progress cards: remaining bytes over
// the live speed, humanized to seconds/minutes (frame ③ "剩余约 6 分钟").
// Empty unless actively downloading with a measurable speed.
function etaText(task: DlTask): string {
  if (task.status !== 'downloading' || !(task.speed > 0)) return ''
  const remaining = Math.max(0, task.total - task.downloaded)
  if (remaining <= 0) return ''
  const secs = Math.round(remaining / task.speed)
  if (secs < 60) return t('downloads.etaSeconds', { n: secs })
  return t('downloads.etaMinutes', { n: Math.max(1, Math.round(secs / 60)) })
}

// Frame ⑧ pinned-card meta tail: on the phone tier AND the tablet tiers the
// draft's meta line ends with a measurable transfer speed (tablet draft frame
// A⑧/B⑧ "1.0 / 2.3 GB · 4.2 MB/s"); otherwise — and on desktop always — the
// status word is the last segment.
function speedTail(task: DlTask): boolean {
  const tier = platformState.value
  return (tier.isMobile || tier.isTablet) && task.status === 'downloading' && task.speed > 0
}

function goToDetail(modelId: string) {
  router.push('/models/model/' + encodeURIComponent(modelId))
}

const searchGate = new LatestOnly()
async function doSearch() {
  if (!searchQuery.value) return
  const seq = searchGate.begin()
  searching.value = true
  searched.value = true
  try {
    const results = await searchDownloads(searchQuery.value, 'all')
    if (!searchGate.isLatest(seq)) return
    searchResults.value = results || []
    sizeQueue = new LimitedQueue(4)
    for (const r of searchResults.value) loadModelSize(r.modelId)
  } catch {} finally {
    if (searchGate.isLatest(seq)) searching.value = false
  }
}

function loadModelSize(modelId: string) {
  if (modelId in modelSizes) return
  modelSizes[modelId] = 0
  sizeQueue.push(async () => {
    try {
      const size = await getModelMaxFileSize(modelId)
      if (size > 0) modelSizes[modelId] = size
    } catch {
      // Silently ignore query failures
    }
  })
}

async function fetchTasks() {
  try {
    tasks.value = await getDownloadTasks() || []
    const doneCount = tasks.value.filter(t => t.status === 'done').length
    if (doneCount > lastDoneCount) {
      lastDoneCount = doneCount
      refreshModels().catch(() => {})
    }
    if (!hasActiveTask(tasks.value) && taskPollTimer) {
      clearInterval(taskPollTimer)
      taskPollTimer = null
    }
  } catch {}
}

async function cancelTask(id: string) {
  try {
    await cancelDownloadTask(id)
    ensurePolling()
  } catch {}
}

async function retryTask(id: string) {
  try {
    await retryDownloadTask(id)
    ensurePolling()
  } catch {}
}

async function pauseTask(id: string) {
  try {
    await pauseDownloadTask(id)
    ensurePolling()
  } catch {}
}

async function resumeTask(id: string) {
  try {
    await resumeDownloadTask(id)
    ensurePolling()
  } catch {}
}

function ensurePolling() {
  if (taskPollTimer) {
    fetchTasks()
  } else {
    startTaskPolling()
  }
}

function startTaskPolling() {
  taskPollTimer = setInterval(fetchTasks, 1000)
  fetchTasks()
}

onMounted(() => {
  startTaskPolling()
})
onUnmounted(() => { if (taskPollTimer) clearInterval(taskPollTimer) })
</script>

<style scoped>
.downloads-tab {
  /* No page padding: the merged shell (.page) already provides the horizontal
     padding and the bottom reserve; this panel only fills the tab body */
  min-width: 0;
}

/* ─── Body band: flat stack on every tier ─── */
.dl-body {
  display: flex;
  flex-direction: column;
  min-width: 0;
}

/* Rail wrapper: dissolves into the flat stack (display:contents) */
.dl-rail {
  display: contents;
}

/* ─── Search (frame ③ .search): floating pill capsule ─── */
.search-bar {
  display: flex;
  gap: 10px;
  margin-bottom: 24px;
}

.search-input-wrap {
  flex: 1;
  position: relative;
  background: var(--bg-secondary);
  border: 1px solid var(--border);
  border-radius: 999px;
  box-shadow: var(--shadow-island);
  transition: border-color 0.2s;
  min-width: 0;
}

.search-input-wrap:focus-within {
  border-color: rgba(99, 102, 241, 0.4);
}

.search-icon {
  position: absolute;
  left: 16px;
  top: 50%;
  transform: translateY(-50%);
  color: var(--text-dim);
  pointer-events: none;
}

.search-input {
  width: 100%;
  padding: 12px 18px 12px 44px;
  background: none;
  border: none;
  border-radius: 999px;
  color: var(--text-secondary);
  font-size: 14px;
  outline: none;
}

/* Pill action buttons: search rides the brand gradient (frame ⑥ "gradient =
   executable"), the task-manager entry stays a neutral ghost pill */
.pill-btn {
  position: relative;
  padding: 0 22px;
  min-height: 44px;
  background: var(--grad);
  color: #fff;
  border: none;
  border-radius: 999px;
  font-size: 14px;
  font-weight: 700;
  font-family: inherit;
  cursor: pointer;
  white-space: nowrap;
  box-shadow: none;
  transition: all 0.2s;
}

.pill-btn:hover:not(:disabled) {
  filter: brightness(1.06);
}

.pill-btn:disabled {
  opacity: 0.4;
  cursor: default;
  box-shadow: none;
}

.pill-btn.ghost {
  background: var(--bg-secondary);
  color: var(--text-secondary);
  border: 1px solid var(--border);
  box-shadow: var(--shadow-island);
}

.pill-btn.ghost:hover:not(:disabled) {
  color: var(--text-primary);
}

.pill-btn.ghost:active:not(:disabled) {
  background: var(--hover-bg);
  transform: scale(0.98);
}

.task-badge {
  position: absolute;
  top: -7px;
  right: -7px;
  min-width: 18px;
  height: 18px;
  padding: 0 5px;
  border-radius: 9px;
  background: var(--danger);
  color: #fff;
  font-size: 11px;
  font-weight: 700;
  line-height: 18px;
  text-align: center;
  box-sizing: border-box;
}

/* ─── In-flight download progress cards (frame ③ .dlcard, pinned on top) ─── */
.dl-pinned {
  display: flex;
  flex-direction: column;
  gap: 10px;
  margin-bottom: 24px;
}

.dl-card {
  display: block;
  width: 100%;
  text-align: left;
  background: var(--bg-secondary);
  border: 1px solid var(--border);
  border-radius: var(--r-md);
  box-shadow: var(--shadow-island);
  padding: 16px 18px;
  font-family: inherit;
  cursor: pointer;
  transition: border-color 0.2s;
}

.dl-card:hover {
  border-color: var(--overlay-10);
}

/* role="button" card focus parity with the former native button */
.dl-card:focus-visible {
  outline: 2px solid var(--accent);
  outline-offset: 2px;
}

.dl-card-top {
  display: flex;
  justify-content: space-between;
  align-items: baseline;
  gap: 12px;
  font-size: 13px;
  font-weight: 700;
}

.dl-name {
  color: var(--text-primary);
  word-break: break-all;
  min-width: 0;
}

.dl-pct {
  color: var(--accent-light);
  font-variant-numeric: tabular-nums;
  flex-shrink: 0;
}

.dl-bar {
  display: block;
  height: 7px;
  border-radius: 999px;
  background: var(--hover-bg);
  overflow: hidden;
  margin: 10px 0;
}

.dl-fill {
  display: block;
  height: 100%;
  border-radius: 999px;
  background: var(--grad);
  transition: width 0.3s;
}

.dl-fill.paused {
  background: linear-gradient(90deg, #fbbf24, #f59e0b);
}

.dl-fill.error-fill {
  background: rgba(239, 68, 68, 0.6);
}

.dl-card-meta {
  display: flex;
  flex-wrap: wrap;
  align-items: baseline;
  gap: 4px 10px;
  font-size: 11.5px;
  color: var(--text-dim);
}

.dl-eta {
  font-weight: 600;
  color: var(--text-muted);
  font-variant-numeric: tabular-nums;
}

/* Frame ⑧ meta tail: live transfer speed, same muted voice as the ETA */
.dl-speed {
  font-weight: 600;
  color: var(--text-muted);
  font-variant-numeric: tabular-nums;
}

.dl-status {
  margin-left: auto;
  font-weight: 600;
}

.status-done { color: var(--success); }
.status-downloading { color: var(--accent-light); }
.status-paused { color: #fbbf24; }
.status-queued { color: var(--text-dim); }
.status-error { color: var(--danger); }
.status-cancelled { color: var(--overlay-20); }

/* ─── Section heading ─── */
.section-heading {
  font-size: 16px;
  font-weight: 600;
  color: var(--text-secondary);
  margin: 0 0 14px;
}

/* ─── Results (frame ③ .mcard: island cards with a gradient icon brick) ─── */
.results-grid {
  display: grid;
  grid-template-columns: repeat(2, 1fr);
  gap: 12px;
  margin-bottom: 36px;
}

.result-card {
  background: var(--bg-secondary);
  border: 1px solid var(--border);
  border-radius: var(--r-md);
  box-shadow: var(--shadow-island);
  cursor: pointer;
  transition: border-color 0.2s;
  padding: 14px 16px;
  /* Grid item hardening: kill the automatic min-content minimum so a long
     unbreakable token inside the card can never widen the 1fr track past
     the container (the phone tier is single-column, full-bleed — a track
     breach there clips the card's right edge on the viewport). */
  min-width: 0;
}

.result-card:hover {
  border-color: var(--overlay-10);
}

.result-main {
  display: flex;
  align-items: center;
  gap: 14px;
}

/* 48px gradient icon brick (frame ③ .tile): soft brand-gradient wash with the
   cube glyph in the accent color */
.result-tile {
  width: 48px;
  height: 48px;
  border-radius: var(--r-md);
  background: var(--grad-soft);
  color: var(--accent-light);
  display: flex;
  align-items: center;
  justify-content: center;
  flex-shrink: 0;
}

.result-info {
  flex: 1;
  min-width: 0;
}

.result-name {
  font-size: 14px;
  font-weight: 700;
  color: var(--text-primary);
  margin: 0 0 6px;
  line-height: 1.35;
  /* Wrap at word boundaries AND allow a break anywhere inside an
     overlong token: `break-word` alone never shrinks the heading's
     min-content width, so a token longer than the card (real HF ids do
     contain 30+ char underscored/dotted runs) used to widen the grid track
     and clip the card's right edge on phones. `anywhere` both wraps the
     token and keeps the min-content at a single character. */
  overflow-wrap: anywhere;
}

.result-meta {
  display: flex;
  flex-wrap: wrap;
  gap: 6px;
}

/* Small neutral chips (frame ③ .mt i): static info stays off the action color */
.result-chip {
  font-style: normal;
  background: var(--hover-bg);
  border-radius: 6px;
  padding: 2px 8px;
  font-size: 11.5px;
  font-weight: 600;
  color: var(--text-muted);
  /* Same min-content logic as .result-name: a long author/team token must
     wrap inside its chip instead of stretching the chip row */
  overflow-wrap: anywhere;
}

/* Round gradient download button (frame ③ .dlbtn): 38px circle, the one
   glanceable action on the card; opens the existing download flow (detail page) */
.dl-round {
  width: 38px;
  height: 38px;
  border-radius: 50%;
  background: var(--grad);
  color: #fff;
  display: flex;
  align-items: center;
  justify-content: center;
  border: none;
  cursor: pointer;
  flex-shrink: 0;
  box-shadow: none;
  transition: filter 0.2s;
}

.dl-round:hover {
  filter: brightness(1.08);
}

/* Touch press feedback (OS-scoped): mirrors the hover brightness on touch. */
html[data-os='android'] .dl-round:active,
html[data-os='ios'] .dl-round:active {
  filter: brightness(1.08);
}

/* ─── Tasks ─── */
.task-list {
  display: flex;
  flex-direction: column;
  gap: 10px;
}

.task-card {
  padding: 16px 20px;
  background: var(--surface);
  border: 1px solid var(--border);
  border-radius: var(--r-md);
  box-shadow: var(--shadow-island);
}

.task-info {
  display: flex;
  justify-content: space-between;
  margin-bottom: 8px;
}

.task-name {
  font-size: 13px;
  font-weight: 600;
  color: var(--text-secondary);
  word-break: break-all;
}

.task-model {
  font-size: 11px;
  color: var(--text-dim);
  flex-shrink: 0;
  margin-left: 12px;
}

.task-bar-wrap {
  display: flex;
  align-items: center;
  gap: 10px;
  margin-bottom: 8px;
}

.task-bar {
  flex: 1;
  height: 5px;
  background: var(--border);
  border-radius: 3px;
  overflow: hidden;
}

.task-fill {
  height: 100%;
  border-radius: 3px;
  background: var(--grad);
  transition: width 0.3s;
}

.task-fill.paused {
  background: linear-gradient(90deg, #fbbf24, #f59e0b);
}

.task-fill.error-fill {
  background: rgba(239, 68, 68, 0.6);
}

.task-percent {
  font-size: 12px;
  font-weight: 600;
  color: var(--accent-light);
  min-width: 36px;
  text-align: right;
}

.task-meta {
  display: flex;
  justify-content: space-between;
  font-size: 11px;
  margin-bottom: 8px;
}

.task-status {
  font-weight: 500;
}

.task-size {
  color: var(--text-dim);
}

.task-speed {
  color: var(--accent-light);
  font-weight: 600;
}

.task-error {
  font-size: 11px;
  color: #ef4444;
  margin-bottom: 8px;
  padding: 6px 10px;
  background: rgba(239, 68, 68, 0.06);
  border-radius: 6px;
}

.task-actions {
  display: flex;
  gap: 6px;
}

.task-btn {
  padding: 4px 12px;
  border-radius: 6px;
  font-size: 11px;
  font-weight: 500;
  cursor: pointer;
  border: 1px solid transparent;
  transition: all 0.2s;
}

.pause-btn {
  background: rgba(251, 191, 36, 0.1);
  color: #fbbf24;
  border-color: rgba(251, 191, 36, 0.2);
}

.pause-btn:hover { background: rgba(251, 191, 36, 0.2); }

.resume-btn {
  background: rgba(34, 197, 94, 0.1);
  color: var(--success);
  border-color: rgba(34, 197, 94, 0.2);
}

.resume-btn:hover { background: rgba(34, 197, 94, 0.2); }

.retry-btn {
  background: rgba(99, 102, 241, 0.1);
  color: var(--accent-light);
  border-color: rgba(99, 102, 241, 0.2);
}

.retry-btn:hover { background: rgba(99, 102, 241, 0.2); }

.cancel-btn {
  background: rgba(239, 68, 68, 0.06);
  color: rgba(239, 68, 68, 0.7);
  border-color: rgba(239, 68, 68, 0.12);
}

.cancel-btn:hover { background: rgba(239, 68, 68, 0.12); }

/* Touch press feedback (OS-scoped): :hover never fires on touch input, so
   each variant's active state mirrors its hover wash. */
html[data-os='android'] .pause-btn:active,
html[data-os='ios'] .pause-btn:active {
  background: rgba(251, 191, 36, 0.2);
}

html[data-os='android'] .resume-btn:active,
html[data-os='ios'] .resume-btn:active {
  background: rgba(34, 197, 94, 0.2);
}

html[data-os='android'] .retry-btn:active,
html[data-os='ios'] .retry-btn:active {
  background: rgba(99, 102, 241, 0.2);
}

html[data-os='android'] .cancel-btn:active,
html[data-os='ios'] .cancel-btn:active {
  background: rgba(239, 68, 68, 0.12);
}

/* ─── Tasks modal ─── */
.modal-overlay {
  position: fixed;
  inset: 0;
  background: rgba(0, 0, 0, 0.5);
  z-index: 1000;
  display: flex;
  align-items: center;
  justify-content: center;
  padding: 24px;
}

.modal-panel {
  width: 100%;
  max-width: 560px;
  /* Fixed height: switching between the tasks and history tabs never resizes
     the modal; long lists scroll inside the body instead (80vh caps it on
     very short windows) */
  height: 520px;
  max-height: 80vh;
  /* Glass modal (design draft v2 rule 2) */
  background: var(--glass);
  backdrop-filter: blur(22px) saturate(1.6);
  -webkit-backdrop-filter: blur(22px) saturate(1.6);
  border: 1px solid var(--glass-line);
  border-radius: var(--r-lg);
  display: flex;
  flex-direction: column;
  overflow: hidden;
  box-shadow: 0 24px 60px rgba(15, 17, 28, 0.40);
}

.modal-header {
  display: flex;
  align-items: center;
  justify-content: space-between;
  padding: 16px 20px;
  border-bottom: 1px solid var(--border);
  flex-shrink: 0;
}

.modal-title {
  font-size: 16px;
  font-weight: 600;
  color: var(--text-secondary);
  margin: 0;
}

.modal-close {
  background: transparent;
  border: none;
  color: var(--text-dim);
  font-size: 16px;
  line-height: 1;
  cursor: pointer;
  padding: 6px 8px;
  border-radius: 6px;
  transition: all 0.2s;
}

.modal-close:hover {
  background: var(--hover-bg);
  color: var(--text-primary);
}

.modal-body {
  padding: 16px 20px;
  overflow-y: auto;
  flex: 1;
  min-height: 0;
}

.tasks-empty {
  padding: 48px 0;
  text-align: center;
  color: var(--text-dim);
  font-size: 14px;
}

/* Tabs switching between in-flight tasks and download history; sticky so they
   stay visible while the list scrolls */
.task-tabs {
  display: flex;
  gap: 8px;
  margin: -16px -20px 12px;
  padding: 0 20px;
  position: sticky;
  top: -16px;
  z-index: 1;
  background: var(--bg-primary);
  border-bottom: 1px solid var(--border-light);
}

.task-tab {
  padding: 8px 14px;
  border: none;
  background: transparent;
  color: var(--text-dim);
  font-size: 13px;
  font-weight: 600;
  cursor: pointer;
  border-bottom: 2px solid transparent;
  margin-bottom: -1px;
  transition: color 0.15s, border-color 0.15s;
}

.task-tab:hover {
  color: var(--text-primary);
}

.task-tab.active {
  color: var(--accent-light);
  border-bottom-color: var(--accent);
}

.modal-fade-enter-active,
.modal-fade-leave-active {
  transition: opacity 0.2s;
}

.modal-fade-enter-from,
.modal-fade-leave-to {
  opacity: 0;
}

/* ─── Empty ─── */
.empty-state {
  text-align: center;
  padding: 48px;
  color: var(--text-dim);
}

.empty-state h2 {
  font-size: 18px;
  color: var(--text-muted);
  margin: 0 0 8px;
}

/* ─── Empty-search guidance card (portrait tablet band). The card renders
       only behind the portrait-band platform gate (showsSearchGuidance), so
       these styles can never affect the other tiers' rendering. Reuses the
       .empty-state surface (frame ㉑ emptycard vocabulary) with a guidance
       glyph and the keyword chips added. ─── */
.search-empty-glyph {
  color: var(--accent-light);
  margin-bottom: 10px;
}

.search-empty-lead {
  font-size: 12.5px;
  color: var(--text-dim);
  margin: 0 0 16px;
}

.search-chips {
  display: flex;
  justify-content: center;
  flex-wrap: wrap;
  gap: 8px;
}

/* Keyword chip: fills the search box on tap — neutral surface with the
   44px-min touch height, gradient wash only on hover/press feedback */
.search-chip {
  min-height: 44px;
  padding: 9px 20px;
  border-radius: 999px;
  border: 1px solid var(--border);
  background: var(--bg-secondary);
  color: var(--text-secondary);
  font-size: 13.5px;
  font-weight: 700;
  font-family: inherit;
  cursor: pointer;
  transition: border-color 0.15s, color 0.15s, background 0.15s;
}

.search-chip:hover {
  border-color: rgba(99, 102, 241, 0.4);
  color: var(--accent-light);
  background: var(--active-bg);
}

.search-chip:active {
  transform: scale(0.98);
}

.search-chip:focus-visible {
  outline: 2px solid var(--accent);
  outline-offset: 2px;
}

/* ─── Tablet portrait (768..1099px, design draft frame A⑧): the pinned
       progress card's meta line joins into one muted "size · remaining ·
       speed" line like the draft's dlcard (the speed tail itself is gated in
       the template by the shared platform state). The two-column results grid
       and the pinned-on-top composition already match the draft. ─── */
@media (min-width: 768px) and (max-width: 1099px) {
  .dl-card-meta {
    display: block;
    white-space: nowrap;
    overflow: hidden;
    text-overflow: ellipsis;
  }

  .dl-card-meta span + span::before {
    content: '·';
    margin: 0 6px;
  }

  .dl-status {
    margin-left: 0;
  }
}

/* ─── Phone (<=767px, design draft frame ⑧): the search controls stay in ONE flex
       row (pill input + 搜索 + 任务), result cards go single-column with the
       whole card as the tap target, and the round download button grows to a
       44px touch target. Tablet (768..1099px) keeps the desktop two-column
       results grid but gets wider cards automatically from the collapsed
       sidebar rail. ─── */
@media (max-width: 767px) {
  /* Single search row: input pill + both actions share one line (frame ⑧
     .searchrow), so the input no longer takes its own wrapped row */
  .search-bar {
    flex-wrap: nowrap;
    gap: 8px;
    margin-bottom: 20px;
  }

  /* Frame ⑧ .search: surface pill + island shadow only — no 1px border */
  .search-input-wrap {
    flex: 1 1 auto;
    border: none;
  }

  .search-input {
    min-height: 44px;
    font-size: 13.5px;
  }

  /* Frame ⑧ .gbtn: 13px/800 with the deeper purple glow */
  .pill-btn {
    flex: 0 0 auto;
    min-height: 44px;
    padding: 0 16px;
    font-size: 13px;
    font-weight: 800;
    box-shadow: none;
  }

  /* Frame ⑧ search button: circular gradient (equal width/height, fully
     rounded) instead of the desktop rounded-rect pill; real text renders
     inside the 44px circle for correct accessible naming */
  .search-bar > .pill-btn:first-of-type {
    width: 44px;
    min-width: 44px;
    padding: 0;
    border-radius: 50%;
    font-size: 13px;
    font-weight: 800;
    box-shadow: none;
  }

  /* Frame ⑧ .gbtn.ghost: surface + island shadow, no border */
  .pill-btn.ghost {
    border: none;
  }

  /* Frame ⑧ .gbtn .nbadge: 17px red dot ringed by the page background,
     tucked onto the button's corner */
  .task-badge {
    top: -5px;
    right: -5px;
    min-width: 17px;
    height: 17px;
    padding: 0 4px;
    border: 2px solid var(--bg-primary);
    border-radius: 999px;
    background: #ef4444;
    font-size: 10px;
    font-weight: 700;
    line-height: 1;
    display: flex;
    align-items: center;
    justify-content: center;
  }

  .dl-pinned {
    margin-bottom: 20px;
  }

  .dl-card {
    padding: 14px 16px;
  }

  /* Frame ⑧ .dlcard: mono filename, ink-bold percent, and one muted
     "size · remaining · speed" meta line joined with separators (the status
     word only substitutes for the speed while none is measurable) */
  .dl-name {
    font-family: var(--font-mono);
    font-size: 12px;
    font-weight: 700;
  }

  .dl-pct {
    color: var(--accent-light);
    font-weight: 700;
  }

  .dl-card-meta {
    display: block;
    white-space: nowrap;
    overflow: hidden;
    text-overflow: ellipsis;
  }

  .dl-card-meta span + span::before {
    content: '·';
    margin: 0 6px;
  }

  .dl-status {
    margin-left: 0;
  }

  /* Frame ⑧ results header: plain label left + accent count right (the
     isMobile v-if swap in the template provides the two nodes) */
  .section-head-row {
    display: flex;
    align-items: baseline;
    justify-content: space-between;
    gap: 12px;
    margin-bottom: 10px;
  }

  .section-heading {
    font-size: 13px;
    font-weight: 700;
    color: var(--text-muted);
    margin-bottom: 0;
  }

  .section-count {
    color: var(--accent-light);
    font-size: 12px;
    font-weight: 700;
    flex-shrink: 0;
  }

  /* Full-width result cards; the whole card is the tap target */
  .results-grid {
    grid-template-columns: 1fr;
    gap: 8px;
  }

  .result-card {
    min-height: 76px;
  }

  /* Frame ⑧ .dlbtn with a 44px hit: negative margins keep the card row's
     38px visual rhythm around the larger circle (same technique as the
     model-card gear button) */
  .dl-round {
    width: 44px;
    height: 44px;
    margin: -3px;
  }

  .task-btn {
    min-height: 44px;
    padding: 10px 16px;
    font-size: 12px;
  }

  .task-actions {
    flex-wrap: wrap;
  }

  /* Task-manager modal tab row stops sticking on the phone tier (same policy
     as the page headers in global.css: the whole sheet scrolls). Position
     only — the negative-margin/padding geometry is position-independent, so
     the row keeps its exact placement at the top of the task panel. */
  .task-tabs {
    position: static;
  }

  /* The tasks/history modal keeps an inner scroll; trim the outer padding so
     the panel gets maximal room on small screens */
  .modal-overlay {
    padding: 12px;
  }

  .modal-panel {
    height: auto;
    max-height: calc(100dvh - 24px);
  }
}

</style>
