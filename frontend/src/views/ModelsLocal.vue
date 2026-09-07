<template>
  <div class="models-local">
    <!-- Body band: model cards below the directory strip. The rail wrapper
         dissolves via display:contents and the main column gets order:1, so
         the flat stack keeps the dir-bar above the list. -->
    <div class="ml-body">
      <aside class="ml-rail" :aria-label="t('models.dir')">
        <!-- Local-model directory bar (non-sticky): the directory sources plus
             the manual rescan action, so this block scrolls with the content.
             On Android the pick button is replaced by a read-only hint (the
             browseModelsDir binding errors there) — the edit affordance is
             desktop-OS only; tablet Android reads the same read-only strip. -->
        <div class="dir-bar">
          <div class="dir-sources">
            <div class="dir-info">
              <span class="dir-label">{{ t('models.downloadDir') }}</span>
              <!-- title exposes the full path when the phone-tier ellipsis truncates -->
              <span class="dir-value" :title="downloadDir || t('settings.dirDefaultModels')">{{ downloadDir || t('settings.dirDefaultModels') }}</span>
            </div>
            <div class="dir-info">
              <span class="dir-label">{{ t('models.importDir') }}</span>
              <span class="dir-value" :class="{ 'dir-empty': !modelsDir }" :title="modelsDir || t('models.dirNotSet')">{{ modelsDir || t('models.dirNotSet') }}</span>
            </div>
          </div>
          <div class="dir-actions" :class="{ 'dir-actions-android': platformState.isAndroid }">
            <!-- Android has no native directory picker (the browseModelsDir binding
                 errors there): the pick button is replaced by a read-only hint;
                 phone tier appends the scanned model count + total size and the
                 rescan link pins to the row's right (design draft frame ⑨ .dirbar) -->
            <span v-if="platformState.isAndroid" class="dir-android-hint">{{ androidHint }}</span>
            <button v-else class="dir-btn" :title="t('models.chooseDirTitle')" @click="chooseModelsDir">{{ t('models.chooseDir') }}</button>
            <button class="refresh-btn" :disabled="loading" :title="t('models.refreshTitle')" @click="fetchModels(true)">
              <svg :class="{ spinning: loading }" width="14" height="14" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round" stroke-linejoin="round">
                <polyline points="23 4 23 10 17 10"/><polyline points="1 20 1 14 7 14"/>
                <path d="M3.51 9a9 9 0 0 1 14.85-3.36L23 10M1 14l4.64 4.36A9 9 0 0 0 20.49 15"/>
              </svg>
              {{ t('models.refresh') }}
            </button>
          </div>
        </div>
      </aside>

      <div class="ml-main-col">

      <!-- Loading skeleton. The tile/mid/block nodes are the design draft frame ㉑
           recipe (48px tile + two lines + 56px block); they stay display:none
           on desktop, which keeps rendering the original three-line skeleton. -->
      <div v-if="loading" class="loading-grid">
        <div v-for="i in 3" :key="i" class="skeleton-card">
          <div class="skeleton-tile"></div>
          <div class="skeleton-line skeleton-title"></div>
          <div class="skeleton-line skeleton-mid"></div>
          <div class="skeleton-line skeleton-short"></div>
          <div class="skeleton-block"></div>
        </div>
      </div>

      <!-- Error state -->
      <div v-else-if="error" class="error-card">
        <div class="error-icon">
          <svg width="48" height="48" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="1.5" stroke-linecap="round" stroke-linejoin="round">
            <circle cx="12" cy="12" r="10"/><line x1="12" y1="8" x2="12" y2="12"/><line x1="12" y1="16" x2="12.01" y2="16"/>
          </svg>
        </div>
        <h2>{{ t('models.errorTitle') }}</h2>
        <p>{{ error }}</p>
        <button class="retry-btn" @click="fetchModels()">{{ t('home.retry') }}</button>
      </div>

      <!-- Empty state -->
      <div v-else-if="models.length === 0" class="empty-state">
        <div class="empty-icon">
          <svg width="64" height="64" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="1" stroke-linecap="round" stroke-linejoin="round">
            <path d="M21 16V8a2 2 0 0 0-1-1.73l-7-4a2 2 0 0 0-2 0l-7 4A2 2 0 0 0 3 8v8a2 2 0 0 0 1 1.73l7 4a2 2 0 0 0 2 0l7-4A2 2 0 0 0 21 16z"/>
            <polyline points="3.27 6.96 12 12.01 20.73 6.96"/>
            <line x1="12" y1="22.08" x2="12" y2="12"/>
          </svg>
        </div>
        <h2>{{ t('models.emptyTitle') }}</h2>
        <p>{{ t('models.emptyHint', { dir: downloadDir || t('settings.dirDefaultModels') }) }}</p>
        <button class="empty-cta" @click="router.push('/models/download')">{{ t('action.gotoDownloads') }}</button>
      </div>

      <!-- Model list -->
      <div v-else class="model-list">
        <!-- Desktop-only context menu: right-click a model card → 量化…
             (llama-quantize lives in the llama.cpp release package, which the
             desktop tiers install; mobile/tablet never render this menu). -->
        <div
          v-for="model in models"
          :key="model.path"
          class="model-card"
          @contextmenu.prevent="openQuantMenu($event, model)"
        >
          <div class="model-icon">
            <svg width="28" height="28" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="1.5" stroke-linecap="round" stroke-linejoin="round">
              <path d="M21 16V8a2 2 0 0 0-1-1.73l-7-4a2 2 0 0 0-2 0l-7 4A2 2 0 0 0 3 8v8a2 2 0 0 0 1 1.73l7 4a2 2 0 0 0 2 0l7-4A2 2 0 0 0 21 16z"/>
            </svg>
          </div>
          <div class="model-info">
            <div class="model-header">
              <h3 class="model-name">{{ model.name }}</h3>
              <span v-if="model.hasMmproj" class="mmproj-badge" :title="t('models.multimodalTitle')">👁️ {{ t('models.multimodal') }}</span>
              <div class="model-actions">
                <button class="model-settings-btn" :title="t('models.settings')" @click.stop="openSettings(model)">
                  <svg width="16" height="16" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round" stroke-linejoin="round">
                    <circle cx="12" cy="12" r="3"/><path d="M19.4 15a1.65 1.65 0 0 0 .33 1.82l.06.06a2 2 0 0 1 0 2.83 2 2 0 0 1-2.83 0l-.06-.06a1.65 1.65 0 0 0-1.82-.33 1.65 1.65 0 0 0-1 1.51V21a2 2 0 0 1-2 2 2 2 0 0 1-2-2v-.09A1.65 1.65 0 0 0 9 19.4a1.65 1.65 0 0 0-1.82.33l-.06.06a2 2 0 0 1-2.83 0 2 2 0 0 1 0-2.83l.06-.06A1.65 1.65 0 0 0 4.68 15a1.65 1.65 0 0 0-1.51-1H3a2 2 0 0 1-2-2 2 2 0 0 1 2-2h.09A1.65 1.65 0 0 0 4.6 9a1.65 1.65 0 0 0-.33-1.82l-.06-.06a2 2 0 0 1 0-2.83 2 2 0 0 1 2.83 0l.06.06A1.65 1.65 0 0 0 9 4.68a1.65 1.65 0 0 0 1-1.51V3a2 2 0 0 1 2-2 2 2 0 0 1 2 2v.09a1.65 1.65 0 0 0 1 1.51 1.65 1.65 0 0 0 1.82-.33l.06.06a2 2 0 0 1 2.83 0 2 2 0 0 1 0 2.83l-.06.06A1.65 1.65 0 0 0 19.4 9a1.65 1.65 0 0 0 1.51 1H21a2 2 0 0 1 2 2 2 2 0 0 1-2 2h-.09a1.65 1.65 0 0 0-1.51 1z"/>
                  </svg>
                </button>
              </div>
            </div>
            <div class="model-author" v-if="model.author">{{ model.author }}</div>
            <div class="source-badge" :class="model.sourceDir === downloadDir ? 'source-download' : 'source-import'">
              {{ model.sourceDir === downloadDir ? t('models.sourceDownload') : t('models.sourceImport') }}
            </div>
            <div class="model-meta">
              <!-- Phone tier (frame ⑨): the standalone author line is demoted into
                   the meta chip row; the desktop-only author block hides via CSS -->
              <span v-if="platformState.isMobile && model.author" class="meta-tag author-tag">{{ model.author }}</span>
              <span v-if="model.architecture && model.architecture !== '-'" class="meta-tag arch-tag">{{ model.architecture }}</span>
              <span v-if="model.quantization && model.quantization !== '-'" class="meta-tag quant-tag">{{ model.quantization }}</span>
              <span class="meta-tag size-tag">{{ model.sizeHuman }}</span>
            </div>
            <div class="model-path">{{ model.path }}</div>
          </div>
        </div>

        <!-- Desktop context menu (right-click a model card): the quantize entry.
             Rendered at the cursor via fixed positioning; closes on any click
             elsewhere or on Escape. mobile/tablet tiers never open it.
             NOTE: both the menu and the dialog live OUTSIDE the
             loading/error/list v-if chain — a quantize-finish triggers a rescan
             (loading=true) which would otherwise unmount the dialog mid-run. -->
      </div>

      <!-- Desktop context menu overlay (see note above) -->
      <Teleport to="body">
        <div v-if="quantMenu.visible" class="qz-ctx-dim" @click="closeQuantMenu" @contextmenu.prevent="closeQuantMenu">
          <div
            class="qz-ctx"
            :style="{ left: quantMenu.x + 'px', top: quantMenu.y + 'px' }"
            role="menu"
            @click.stop
          >
            <button class="qz-ctx-item" role="menuitem" type="button" @click="openQuantDialog">
              <svg width="14" height="14" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round" stroke-linejoin="round">
                <path d="M21 16V8a2 2 0 0 0-1-1.73l-7-4a2 2 0 0 0-2 0l-7 4A2 2 0 0 0 3 8v8a2 2 0 0 0 1 1.73l7 4a2 2 0 0 0 2 0l7-4A2 2 0 0 0 21 16z"/>
                <polyline points="3.27 6.96 12 12.01 20.73 6.96"/><line x1="12" y1="22.08" x2="12" y2="12"/>
              </svg>
              {{ t('quantize.menu') }}
            </button>
          </div>
        </div>
      </Teleport>

      <!-- Quantize dialog (desktop only; llama-quantize driver) -->
      <QuantizeDialog
        v-if="quantDialog.open"
        :src-path="quantDialog.srcPath"
        @close="closeQuantDialog"
        @finished="onQuantFinished"
      />
      </div>
    </div>
  </div>
</template>

<script setup lang="ts">
import { ref, computed, onMounted, onUnmounted } from 'vue'
import { useRouter } from 'vue-router'
import { getModels, refreshModels, getConfig, browseModelsDir } from '../wails'
import { t } from '../lib/i18n'
import { formatBytes } from '../lib/format'
import { usePlatform } from '../lib/platform'
import QuantizeDialog from '../components/QuantizeDialog.vue'

const router = useRouter()

// OS-scoped gate: Android exposes no native directory picker, so the external
// path browse button (browseModelsDir) is hidden behind a read-only hint.
const platformState = usePlatform()

interface ModelInfo {
  author: string
  name: string
  path: string
  sizeBytes: number
  sizeHuman: string
  architecture: string
  quantization: string
  hasMmproj: boolean
  sourceDir: string
}

const models = ref<ModelInfo[]>([])
const loading = ref(true)
const error = ref('')

// Model sources shown in the directory bar: the model download path (where new
// downloads land) and the imported model directory (reuse of existing models).
const downloadDir = ref('')
const modelsDir = ref('')

async function loadModelsDir() {
  try {
    const cfg = await getConfig()
    downloadDir.value = cfg.modelDownloadDir || ''
    modelsDir.value = cfg.modelsDir || ''
  } catch {}
}

// Android hint (frame ⑨ .dirbar .hint): the phone tier appends the scanned
// model count and total size; desktop Android keeps the plain hint line
const androidHint = computed(() => {
  if (!platformState.value.isMobile) return t('models.dirAndroidHint')
  const totalBytes = models.value.reduce((sum, m) => sum + (m.sizeBytes || 0), 0)
  return t('models.dirAndroidHintCounted', { n: models.value.length, size: formatBytes(totalBytes) })
})

async function chooseModelsDir() {
  try {
    const dir = await browseModelsDir()
    // Only update display and rescan when user actually picks a dir (non-empty); cancel (empty string) leaves existing dir unchanged
    if (dir) {
      modelsDir.value = dir
      await fetchModels(true)
    }
  } catch {}
}

// Settings: navigate to the dedicated settings page for this model. The
// one-click auto-tune entry lives there too (tuning fills the settings form
// in real time).
function openSettings(model: ModelInfo) {
  router.push('/models/settings/' + encodeURIComponent(model.name))
}

// ─── Quantize (desktop only) ──────────────────────────────────────────────────
// Right-click a model card → 量化… opens the llama-quantize dialog prefilled
// with that card's .gguf path. Desktop tier + non-touch OS only: the llama.cpp
// release package (with llama-quantize) is a desktop install, and the backend
// rejects Android calls anyway.
const quantMenu = ref({ visible: false, x: 0, y: 0, path: '' })
const quantDialog = ref({ open: false, srcPath: '' })

const canQuantize = computed(
  () => platformState.value.isDesktop && platformState.value.os !== 'android' && platformState.value.os !== 'ios'
)

function openQuantMenu(e: MouseEvent, model: ModelInfo) {
  if (!canQuantize.value) return
  quantMenu.value = {
    visible: true,
    x: Math.min(e.clientX, window.innerWidth - 190),
    y: Math.min(e.clientY, window.innerHeight - 70),
    path: model.path,
  }
}

function closeQuantMenu() {
  quantMenu.value.visible = false
}

function openQuantDialog() {
  const path = quantMenu.value.path
  closeQuantMenu()
  if (!path) return
  quantDialog.value = { open: true, srcPath: path }
}

function closeQuantDialog() {
  quantDialog.value.open = false
}

/** A successful quantize lands a new .gguf next to the source: rescan so the
    "My Models" list shows it without a manual refresh. */
function onQuantFinished(_outPath: string) {
  fetchModels(true)
}

function onGlobalKeydown(e: KeyboardEvent) {
  if (e.key === 'Escape') closeQuantMenu()
}

async function fetchModels(force = false) {
  loading.value = true
  error.value = ''
  try {
    // force=true forces a rescan (refreshModels); otherwise uses cache (getModels) (#18)
    models.value = (force ? await refreshModels() : await getModels()) as ModelInfo[]
  } catch (e) {
    error.value = t('models.backendError', { msg: e instanceof Error ? e.message : String(e) })
  } finally {
    loading.value = false
  }
}

onMounted(() => {
  loadModelsDir()
  fetchModels()
  document.addEventListener('keydown', onGlobalKeydown)
})

onUnmounted(() => {
  document.removeEventListener('keydown', onGlobalKeydown)
})
</script>

<style scoped>
.models-local {
  /* No page padding: the merged shell (.page) already provides the horizontal
     padding and the bottom reserve; this panel only fills the tab body */
  min-width: 0;
}

/* ─── Body band: flat column stack on every tier ─── */
.ml-body {
  display: flex;
  flex-direction: column;
  min-width: 0;
}

/* Rail wrapper (directory strip): dissolves into the flat stack. The main
   column takes order:1 so the dissolved dir-bar stays the first block of the
   stack. */
.ml-rail {
  display: contents;
}

.ml-main-col {
  order: 1;
  min-width: 0;
}

/* ─── Models directory bar (frame ③ language: floating-island skin) ─── */
.dir-bar {
  /* Bottom spacing before the list: this bar is the first content block */
  margin-bottom: 24px;
  display: flex;
  align-items: center;
  justify-content: space-between;
  gap: 12px;
  padding: 14px 18px;
  background: var(--bg-secondary);
  border: 1px solid var(--border);
  border-radius: var(--r-md);
  box-shadow: var(--shadow-island);
}

.dir-sources {
  display: flex;
  flex-direction: column;
  gap: 4px;
  min-width: 0;
}

.dir-info {
  display: flex;
  align-items: baseline;
  gap: 10px;
  min-width: 0;
}

.dir-label {
  font-size: 12px;
  color: var(--text-dim);
  flex-shrink: 0;
}

.dir-value {
  font-size: 12px;
  color: var(--text-muted);
  font-family: 'JetBrains Mono', 'Fira Code', monospace;
  word-break: break-all;
}

.dir-empty {
  color: var(--text-dim);
  font-style: italic;
}

.dir-btn {
  padding: 6px 16px;
  background: var(--border-light);
  color: var(--text-muted);
  border: 1px solid var(--overlay-10);
  border-radius: 8px;
  font-size: 12px;
  font-weight: 500;
  cursor: pointer;
  transition: all 0.2s;
  flex-shrink: 0;
  white-space: nowrap;
}

.dir-btn:hover {
  background: var(--overlay-8);
  border-color: var(--scrollbar-thumb-hover);
  color: var(--text-primary);
}

/* Android-only replacement for the pick button: one-line read-only hint */
.dir-android-hint {
  font-size: 12px;
  color: var(--text-dim);
  text-align: right;
}

/* Right-side action group: keeps the directory picker and the rescan button
   side by side while .dir-bar's space-between pins the group to the row's end */
.dir-actions {
  display: flex;
  align-items: center;
  gap: 12px;
  flex-shrink: 0;
}

/* Rescan button (same visual language as the former toolbar refresh buttons) */
.refresh-btn {
  display: inline-flex;
  align-items: center;
  gap: 6px;
  padding: 6px 14px;
  background: transparent;
  color: var(--text-secondary);
  border: 1px solid var(--border);
  border-radius: var(--radius-sm);
  font-size: 13px;
  font-weight: 500;
  cursor: pointer;
  transition: all 0.2s;
}

.refresh-btn:hover:not(:disabled) {
  background: var(--hover-bg);
  border-color: var(--overlay-20);
  color: var(--text-primary);
}

.refresh-btn:disabled {
  opacity: 0.6;
  cursor: default;
}

.refresh-btn svg.spinning {
  animation: spin 0.8s linear infinite;
}

@keyframes spin {
  from { transform: rotate(0deg); }
  to { transform: rotate(360deg); }
}

/* ─── Model list (frame ③ .mcard: island cards with gradient icon bricks) ─── */
.model-list {
  display: flex;
  flex-direction: column;
  gap: 12px;
}

/* Desktop two-column grid: 1280px+ (design draft D5) */
@media (min-width: 1280px) {
  .model-list {
    display: grid;
    grid-template-columns: 1fr 1fr;
    gap: 16px;
  }
}

.model-card {
  display: flex;
  align-items: flex-start;
  gap: 16px;
  padding: 16px 18px;
  background: var(--bg-secondary);
  border: 1px solid var(--border);
  border-radius: var(--r-md);
  box-shadow: var(--shadow-island);
  transition: border-color 0.2s ease;
}

.model-card:hover {
  border-color: var(--overlay-10);
}

.model-card:active {
  border-color: var(--overlay-20);
  background: var(--bg-secondary);
}

/* 48px gradient icon brick (frame ③ .tile): soft brand-gradient wash with the
   cube glyph in the accent color */
.model-icon {
  width: 48px;
  height: 48px;
  display: flex;
  align-items: center;
  justify-content: center;
  background: var(--grad-soft);
  border-radius: var(--r-md);
  color: var(--accent-light);
  flex-shrink: 0;
  margin-top: 2px;
}

.model-info {
  flex: 1;
  min-width: 0;
}

.model-header {
  display: flex;
  align-items: center;
  gap: 10px;
  margin-bottom: 4px;
}

/* Header icon button groups at the row's end: the wrapper owns margin-left:auto
 * so the free space stays on one side instead of splitting the row. */
.model-actions {
  margin-left: auto;
  display: flex;
  align-items: center;
  gap: 4px;
  flex-shrink: 0;
}

.model-settings-btn {
  width: 30px;
  height: 30px;
  display: flex;
  align-items: center;
  justify-content: center;
  background: none;
  border: none;
  color: var(--text-dim);
  cursor: pointer;
  border-radius: 6px;
  /* Faintly visible at rest so the actions are discoverable without hover;
     the tooltip titles label them. Full opacity on card hover. */
  opacity: 0.55;
  transition: opacity 0.2s, background 0.15s, color 0.15s;
  flex-shrink: 0;
}

.model-card:hover .model-settings-btn {
  opacity: 1;
}

.model-settings-btn:hover {
  background: var(--hover-bg);
  color: var(--accent-light);
}

/* Touch press feedback (OS-scoped): the model card itself is not clickable;
   its clickable area is this gear button, whose active state mirrors the
   hover visuals on touch. */
html[data-os='android'] .model-settings-btn:active,
html[data-os='ios'] .model-settings-btn:active {
  background: var(--hover-bg);
  color: var(--accent-light);
}

.model-name {
  font-size: 16px;
  font-weight: 700;
  color: var(--text-primary);
  margin: 0;
  word-break: break-word;
  line-height: 1.4;
}

.mmproj-badge {
  display: inline-flex;
  align-items: center;
  gap: 4px;
  padding: 2px 10px;
  background: rgba(168, 85, 247, 0.12);
  color: #c084fc;
  border: 1px solid rgba(168, 85, 247, 0.2);
  border-radius: var(--r-sm);
  font-size: 11px;
  font-weight: 600;
  white-space: nowrap;
  flex-shrink: 0;
}

.model-author {
  font-size: 12px;
  color: var(--text-dim);
  margin-bottom: 6px;
}

.source-badge {
  display: inline-flex;
  align-items: center;
  padding: 2px 10px;
  border-radius: var(--r-sm);
  font-size: 11px;
  font-weight: 600;
  margin-bottom: 8px;
}

.source-download {
  background: rgba(99, 102, 241, 0.1);
  color: var(--accent-light);
  border: 1px solid rgba(99, 102, 241, 0.22);
}

.source-import {
  background: rgba(168, 85, 247, 0.1);
  color: #c084fc;
  border: 1px solid rgba(168, 85, 247, 0.2);
}

.model-meta {
  display: flex;
  gap: 8px;
  flex-wrap: wrap;
  margin-bottom: 8px;
}

.meta-tag {
  display: inline-flex;
  align-items: center;
  padding: 3px 10px;
  border-radius: 6px;
  font-size: 11px;
  font-weight: 600;
  letter-spacing: 0.3px;
}

.arch-tag {
  background: rgba(59, 130, 246, 0.12);
  color: #60a5fa;
  border: 1px solid rgba(59, 130, 246, 0.2);
}

.quant-tag {
  background: rgba(34, 197, 94, 0.1);
  color: var(--success);
  border: 1px solid rgba(34, 197, 94, 0.18);
}

.size-tag {
  background: var(--border-light);
  color: var(--text-muted);
  border: 1px solid var(--overlay-8);
}

.model-path {
  font-size: 11px;
  color: var(--overlay-20);
  font-family: 'JetBrains Mono', 'Fira Code', monospace;
  /* Single-line ellipsis: long model paths truncate at the end instead of wrapping */
  white-space: nowrap;
  overflow: hidden;
  text-overflow: ellipsis;
  line-height: 1.4;
}

/* Desktop card chips/path alignment (design draft D13) */
@media (min-width: 768px) {
  .model-meta {
    gap: 10px;
  }

  .meta-tag {
    padding: 3px 12px;
  }
}

/* ─── Empty ─── */
.empty-state {
  display: flex;
  flex-direction: column;
  align-items: center;
  padding: 56px 32px;
  background: var(--surface);
  border: 1px solid var(--border);
  border-radius: var(--r-lg);
  text-align: center;
}

.empty-icon {
  color: rgba(99, 102, 241, 0.3);
  margin-bottom: 20px;
}

.empty-state h2 {
  font-size: 18px;
  font-weight: 600;
  color: var(--text-secondary);
  margin: 0 0 8px;
}

.empty-state p {
  font-size: 13px;
  color: var(--text-dim);
  margin: 0 0 20px;
  max-width: 420px;
  line-height: 1.6;
}

.empty-state code {
  background: var(--border);
  padding: 2px 6px;
  border-radius: 4px;
  font-size: 12px;
  color: var(--text-secondary);
}

/* Empty-state CTA: jump to the download tab of the merged page */
.empty-cta {
  padding: 8px 24px;
  background: var(--active-bg);
  color: var(--accent-light);
  border: 1px solid transparent;
  border-radius: 8px;
  font-size: 13px;
  font-weight: 500;
  cursor: pointer;
  transition: background 0.2s;
}

.empty-cta:hover {
  background: var(--accent-glow);
}

/* ─── Loading skeleton ───
   The tile/block nodes below belong to the phone-tier frame ㉑ recipe; they
   are display:none at the desktop tier so the original three-line skeleton
   renders byte-identically there. */
.loading-grid {
  display: flex;
  flex-direction: column;
  gap: 12px;
}

.skeleton-tile,
.skeleton-block {
  display: none;
}

.skeleton-card {
  padding: 24px 28px;
  background: var(--bg-secondary);
  border: 1px solid var(--skeleton-bg);
  border-radius: var(--r-md);
  box-shadow: var(--shadow-island);
}

.skeleton-line {
  height: 12px;
  background: var(--skeleton-bg);
  border-radius: 4px;
  margin-bottom: 10px;
  animation: shimmer 1.5s infinite;
}

.skeleton-title {
  width: 30%;
  height: 16px;
}

.skeleton-short {
  width: 50%;
}

@keyframes shimmer {
  0%, 100% { opacity: 0.4; }
  50% { opacity: 0.8; }
}

/* ─── Error ─── */
.error-card {
  display: flex;
  flex-direction: column;
  align-items: center;
  padding: 56px 32px;
  background: rgba(239, 68, 68, 0.04);
  border: 1px solid rgba(239, 68, 68, 0.12);
  border-radius: var(--r-lg);
  text-align: center;
}

.error-icon {
  color: rgba(239, 68, 68, 0.5);
  margin-bottom: 16px;
}

.error-card h2 {
  font-size: 18px;
  font-weight: 600;
  color: var(--text-secondary);
  margin: 0 0 8px;
}

.error-card p {
  font-size: 13px;
  color: var(--text-dim);
  margin: 0 0 20px;
}

.retry-btn {
  padding: 8px 24px;
  background: rgba(99, 102, 241, 0.15);
  color: var(--accent-light);
  border: 1px solid rgba(99, 102, 241, 0.25);
  border-radius: 8px;
  font-size: 13px;
  font-weight: 500;
  cursor: pointer;
  transition: all 0.2s;
}

.retry-btn:hover {
  background: rgba(99, 102, 241, 0.25);
  border-color: rgba(99, 102, 241, 0.4);
}

/* ─── Tablet portrait (768..1099px, design draft frame A⑨): the directory bar
       stacks into the draft's vertical strip (sources above, actions below),
       and the model list becomes the draft's TWO-column card grid with the
       compact card treatment (author/path lines hidden, 14px names, semantic
       source pills, muted meta chips, 44px gear target). ─── */
@media (min-width: 768px) and (max-width: 1099px) {
  .dir-bar {
    flex-direction: column;
    align-items: stretch;
    gap: 10px;
    padding: 12px;
  }

  .dir-actions {
    justify-content: flex-end;
  }

  /* Frame ⑨ .dirbar .d .p: single-line ellipsis (full path in the title
     tooltip) instead of the wrapping desktop value */
  .dir-value {
    min-width: 0;
    white-space: nowrap;
    overflow: hidden;
    text-overflow: ellipsis;
    word-break: normal;
  }

  .dir-btn,
  .refresh-btn {
    min-height: 44px;
    padding: 10px 16px;
  }

  /* Android hint row: hint text left, the rescan action pinned right as a
     bold accent text link (frame ⑨ "↻ 刷新") */
  .dir-actions-android {
    width: 100%;
    justify-content: space-between;
  }

  .dir-android-hint {
    flex: 1;
    min-width: 0;
    text-align: left;
  }

  .dir-actions-android .refresh-btn {
    min-height: 44px;
    padding: 10px 0 10px 12px;
    background: transparent;
    border: none;
    border-radius: 8px;
    color: var(--accent-light);
    font-weight: 800;
  }

  .dir-actions-android .refresh-btn:hover:not(:disabled) {
    background: transparent;
    border: none;
    color: var(--accent-light);
  }

  /* Frame A⑨ .grid2: the tablet width dividend — two model cards per row */
  .model-list {
    display: grid;
    grid-template-columns: repeat(2, minmax(0, 1fr));
    gap: 12px;
    align-items: start;
  }

  .model-card {
    padding: 14px 16px;
  }

  /* Balanced card row: the name owns the header line (flex:1, wraps freely
     inside its own box) and the gear pins to the top-right corner */
  .model-header {
    flex-wrap: nowrap;
    align-items: flex-start;
  }

  /* Frame ⑨ .mcard .nm: 14px card title */
  .model-name {
    flex: 1 1 auto;
    min-width: 0;
    font-size: 14px;
  }

  /* Frame ⑨ .gearbtn: 38px-circle skin at rest, 44px touch target via the
     negative-margin technique */
  .model-settings-btn {
    width: 44px;
    height: 44px;
    margin: -8px;
    border-radius: 50%;
    background: var(--bg-card);
    color: var(--text-muted);
    opacity: 1;
  }

  .model-card:hover .model-settings-btn {
    opacity: 1;
  }

  /* Frame ⑨ .mt i: source pills take semantic colors — download green,
     external blue — at the shared chip geometry */
  .source-badge {
    padding: 2px 7px;
    border-radius: 6px;
    font-size: 10px;
    font-weight: 700;
  }

  .source-download {
    background: var(--success-bg);
    color: var(--success);
    border: none;
  }

  html[data-theme='dark'] .source-download {
    background: #12261f;
    color: #6ee7b7;
  }

  .source-import {
    background: #e8f1fd;
    color: #2563eb;
    border: none;
  }

  html[data-theme='dark'] .source-import {
    background: #1a2740;
    color: #93c5fd;
  }

  /* Frame ⑨ .mt i base: all meta chips unify to the muted surface-2 chip */
  .meta-tag {
    padding: 2px 7px;
    border-radius: 6px;
    font-size: 11px;
    font-weight: 600;
    letter-spacing: 0;
    background: var(--hover-bg);
    color: var(--text-muted);
    border: none;
  }

  .arch-tag,
  .quant-tag {
    background: var(--hover-bg);
    color: var(--text-muted);
    border: none;
  }

  /* Frame ⑨ size: plain muted text in the chip row (no chip background) */
  .size-tag {
    background: none;
    border: none;
    padding: 0;
    color: var(--text-dim);
  }

  /* Card slimming: the standalone author line and the full mono path line
     hide (the grid cards read name + source + chips like the draft) */
  .model-author,
  .model-path {
    display: none;
  }

  /* ─── Frame ㉑ portrait: the state trio at tablet scale ───
     The loading skeleton previews the TWO-column card grid the page really
     renders at this width (draft 双列网格), and the boundary-state actions
     keep the phone recipes' touch-sized pills (draft B21: boundary-state copy
     and action sizes unchanged, touch first). ─── */
  .loading-grid {
    display: grid;
    grid-template-columns: repeat(2, minmax(0, 1fr));
    gap: 12px;
    align-items: start;
  }

  /* Frame ㉑ skeleton recipe (same anatomy as the phone tier): 48px tile +
     70%/45% lines + 56px block, shimmer as a 1.4s background sweep */
  .skeleton-card {
    display: grid;
    grid-template-columns: 48px 1fr;
    grid-template-areas:
      'tile l1'
      'tile l2'
      'block block';
    gap: 8px 12px;
    align-items: center;
    padding: 16px 18px;
  }

  .skeleton-tile {
    display: block;
    grid-area: tile;
    width: 48px;
    height: 48px;
    border-radius: var(--r-md);
  }

  .skeleton-title {
    grid-area: l1;
    width: 70%;
    height: 12px;
    margin-bottom: 0;
  }

  .skeleton-mid {
    grid-area: l2;
    width: 45%;
    height: 10px;
    margin-bottom: 0;
  }

  .skeleton-short {
    display: none;
  }

  .skeleton-block {
    display: block;
    grid-area: block;
    height: 56px;
    border-radius: var(--r-sm);
    margin-top: 4px;
  }

  .skeleton-line,
  .skeleton-tile,
  .skeleton-block {
    background: linear-gradient(90deg, var(--bg-card) 25%, #eceef6 45%, var(--bg-card) 65%);
    background-size: 200% 100%;
    animation: skel-sweep 1.4s linear infinite;
  }

  html[data-theme='dark'] .skeleton-line,
  html[data-theme='dark'] .skeleton-tile,
  html[data-theme='dark'] .skeleton-block {
    background: linear-gradient(90deg, var(--bg-card) 25%, #262b3d 45%, var(--bg-card) 65%);
  }

  /* Frame ㉑ errcard: red retry pill (light #fdecec / dark #2c1a1f disc) */
  .retry-btn {
    min-height: 44px;
    padding: 10px 20px;
    background: #fdecec;
    color: #ef4444;
    border: none;
    border-radius: 999px;
    font-weight: 800;
  }

  .retry-btn:hover {
    background: #fdecec;
    border: none;
    color: #ef4444;
  }

  html[data-theme='dark'] .retry-btn,
  html[data-theme='dark'] .retry-btn:hover {
    background: #2c1a1f;
    color: #f87171;
  }

  /* Frame ㉑ emptycard: gradient pill CTA */
  .empty-cta {
    display: inline-flex;
    align-items: center;
    justify-content: center;
    min-height: 44px;
    padding: 10px 22px;
    background: var(--grad);
    color: #fff;
    border: none;
    border-radius: 999px;
    font-size: 12.5px;
    font-weight: 800;
    box-shadow: none;
  }

  .empty-cta:hover {
    background: var(--grad);
  }
}

/* ─── Phone (<=767px, design draft frame ⑨): the directory bar stacks (sources
       above, actions in a full-width row), source pills take the semantic
       green/blue chip colors, model names drop to 14px with muted meta chips,
       the per-card path line hides (author demotes into the chip row), and
       skeleton / error / empty states follow the frame ㉑ recipes. Tablet
       (768px+) keeps the desktop list. ─── */
@media (max-width: 767px) {
  .dir-bar {
    flex-direction: column;
    align-items: stretch;
    gap: 10px;
    padding: 12px;
  }

  .dir-actions {
    justify-content: flex-end;
  }

  /* Frame ⑨ .dirbar .d .p: single-line ellipsis (full path in the title
     tooltip) instead of the wrapping desktop value */
  .dir-value {
    min-width: 0;
    white-space: nowrap;
    overflow: hidden;
    text-overflow: ellipsis;
    word-break: normal;
  }

  .dir-btn,
  .refresh-btn {
    min-height: 44px;
    padding: 10px 16px;
  }

  /* Android hint row: hint text left, the rescan action pinned right as a
     bold accent text link (frame ⑨ "↻ 刷新") */
  .dir-actions-android {
    width: 100%;
    justify-content: space-between;
  }

  .dir-android-hint {
    flex: 1;
    min-width: 0;
    text-align: left;
  }

  .dir-actions-android .refresh-btn {
    min-height: 44px;
    padding: 10px 0 10px 12px;
    background: transparent;
    border: none;
    border-radius: 8px;
    color: var(--accent-light);
    font-weight: 800;
  }

  .dir-actions-android .refresh-btn:hover:not(:disabled) {
    background: transparent;
    border: none;
    color: var(--accent-light);
  }

  .model-card {
    padding: 14px 16px;
  }

  /* Balanced card row: the name owns the header line (flex:1, wraps freely
     inside its own box) and the gear pins to the top-right corner. Without
     flex:1 a long name used to push the gear onto its own wrapped line,
     stranding it mid-card with dead space beside the tag rows below. The
     mmproj badge (when present) keeps its spot between name and gear. */
  .model-header {
    flex-wrap: nowrap;
    align-items: flex-start;
  }

  /* Frame ⑨ .mcard .nm: 14px card title on the phone tier */
  .model-name {
    flex: 1 1 auto;
    min-width: 0;
    font-size: 14px;
  }

  /* Frame ⑨ .gearbtn: 38px-circle skin at rest (bg-card disc, ink-2 glyph),
     44px touch target via the negative-margin technique */
  .model-settings-btn {
    width: 44px;
    height: 44px;
    /* 44px touch target without growing the header row: negative margins
       keep the card's visual rhythm (28px layout box around a 44px button);
       with the top-aligned header the glyph lands on the name's first line */
    margin: -8px;
    border-radius: 50%;
    background: var(--bg-card);
    color: var(--text-muted);
    opacity: 1;
  }

  .model-card:hover .model-settings-btn {
    opacity: 1;
  }

  /* Frame ⑨ .mt i: source pills take semantic colors — download green,
     external blue — at the shared chip geometry (radius 6, 10/700, no border) */
  .source-badge {
    padding: 2px 7px;
    border-radius: 6px;
    font-size: 10px;
    font-weight: 700;
  }

  .source-download {
    background: var(--success-bg);
    color: var(--success);
    border: none;
  }

  html[data-theme='dark'] .source-download {
    background: #12261f;
    color: #6ee7b7;
  }

  .source-import {
    background: #e8f1fd;
    color: #2563eb;
    border: none;
  }

  html[data-theme='dark'] .source-import {
    background: #1a2740;
    color: #93c5fd;
  }

  /* Frame ⑨ .mt i base: all meta chips unify to the muted surface-2 chip at
     this tier (the desktop-tier blue/green coding drops away) */
  .meta-tag {
    padding: 2px 7px;
    border-radius: 6px;
    font-size: 11px;
    font-weight: 600;
    letter-spacing: 0;
    background: var(--hover-bg);
    color: var(--text-muted);
    border: none;
  }

  .arch-tag,
  .quant-tag {
    background: var(--hover-bg);
    color: var(--text-muted);
    border: none;
  }

  /* Frame ⑨ size: plain muted text below the chip row (no chip background) */
  .size-tag {
    background: none;
    border: none;
    padding: 0;
    color: var(--text-dim);
  }

  /* Phone card slimming: the standalone author line demotes into the meta
     chip row and the full mono path line hides (title tooltip covers it on
     desktop; phone keeps cards compact) */
  .model-author,
  .model-path {
    display: none;
  }

  /* Frame ㉑ skeleton recipe: 48px tile + 70%/45% lines + 56px block, shimmer
     as a 1.4s background sweep (gradient defined locally; global.css is out
     of scope for this page) */
  .skeleton-card {
    display: grid;
    grid-template-columns: 48px 1fr;
    grid-template-areas:
      'tile l1'
      'tile l2'
      'block block';
    gap: 8px 12px;
    align-items: center;
    padding: 16px 18px;
  }

  .skeleton-tile {
    display: block;
    grid-area: tile;
    width: 48px;
    height: 48px;
    border-radius: var(--r-md);
  }

  .skeleton-title {
    grid-area: l1;
    width: 70%;
    height: 12px;
    margin-bottom: 0;
  }

  .skeleton-mid {
    grid-area: l2;
    width: 45%;
    height: 10px;
    margin-bottom: 0;
  }

  .skeleton-short {
    display: none;
  }

  .skeleton-block {
    display: block;
    grid-area: block;
    height: 56px;
    border-radius: var(--r-sm);
    margin-top: 4px;
  }

  .skeleton-line,
  .skeleton-tile,
  .skeleton-block {
    background: linear-gradient(90deg, var(--bg-card) 25%, #eceef6 45%, var(--bg-card) 65%);
    background-size: 200% 100%;
    animation: skel-sweep 1.4s linear infinite;
  }

  html[data-theme='dark'] .skeleton-line,
  html[data-theme='dark'] .skeleton-tile,
  html[data-theme='dark'] .skeleton-block {
    background: linear-gradient(90deg, var(--bg-card) 25%, #262b3d 45%, var(--bg-card) 65%);
  }

  /* Frame ㉑ errcard: red retry pill (light #fdecec / dark #2c1a1f disc) */
  .retry-btn {
    min-height: 44px;
    padding: 10px 20px;
    background: #fdecec;
    color: #ef4444;
    border: none;
    border-radius: 999px;
    font-weight: 800;
  }

  .retry-btn:hover {
    background: #fdecec;
    border: none;
    color: #ef4444;
  }

  html[data-theme='dark'] .retry-btn,
  html[data-theme='dark'] .retry-btn:hover {
    background: #2c1a1f;
    color: #f87171;
  }

  /* Frame ㉑ emptycard: gradient pill CTA */
  .empty-cta {
    display: inline-flex;
    align-items: center;
    justify-content: center;
    min-height: 44px;
    padding: 10px 22px;
    background: var(--grad);
    color: #fff;
    border: none;
    border-radius: 999px;
    font-size: 12.5px;
    font-weight: 800;
    box-shadow: none;
  }

  .empty-cta:hover {
    background: var(--grad);
  }
}

@keyframes skel-sweep {
  to { background-position: -200% 0; }
}

/* ─── Desktop right-click quantize menu ─────────────────────────────── */
.qz-ctx-dim {
  position: fixed;
  inset: 0;
  z-index: 999;
}

.qz-ctx {
  position: fixed;
  min-width: 168px;
  background: var(--bg-card);
  border: 1px solid var(--border-light);
  border-radius: var(--radius-sm);
  box-shadow: var(--shadow-lg, 0 10px 30px rgba(0, 0, 0, 0.18));
  padding: 4px;
}

.qz-ctx-item {
  display: flex;
  align-items: center;
  gap: 8px;
  width: 100%;
  padding: 8px 12px;
  background: transparent;
  border: none;
  border-radius: var(--radius-sm);
  color: var(--text-primary);
  font-size: 12.5px;
  font-weight: 600;
  font-family: inherit;
  cursor: pointer;
  text-align: left;
}

.qz-ctx-item:hover {
  background: var(--hover-bg);
}

</style>
