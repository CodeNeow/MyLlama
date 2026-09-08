<template>
  <!-- Runtime Environment tab panel of the Home shell: the tab label replaces
       the former section heading. Owns its own data loading (skeleton / error
       + retry) and all llama.cpp download / custom-directory actions; the
       shell owns the page chrome, so there is no page chrome here — only the
       dependency cards. -->
  <section class="runtime-section" :class="{ 'rt-installed': info.installed }">
    <!-- Phone tier page heading (design draft frames ③/④): the shell's tab row has
         no page title on phone, so the panel carries the mockup .h-greet
         heading itself; hidden on desktop/tablet (v-if, not CSS) -->
    <header v-if="platformState.isMobile" class="runtime-page-head">
      <h1>{{ t('runtime.pageTitle') }}</h1>
      <p>{{ t('runtime.pageSub') }}</p>
    </header>

    <!-- Loading skeleton -->
    <div v-if="loading" class="skeleton-card">
      <div class="skeleton-line skeleton-title"></div>
      <div class="skeleton-line"></div>
      <div class="skeleton-line skeleton-short"></div>
    </div>

    <!-- Error state -->
    <div v-else-if="error" class="error-card">
      <div class="error-icon">
        <svg width="48" height="48" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="1.5" stroke-linecap="round" stroke-linejoin="round">
          <circle cx="12" cy="12" r="10"/><line x1="12" y1="8" x2="12" y2="12"/><line x1="12" y1="16" x2="12.01" y2="16"/>
        </svg>
      </div>
      <h2>{{ t('runtime.errorTitle') }}</h2>
      <p>{{ error }}</p>
      <button class="retry-btn" @click="fetchInfo">{{ t('runtime.retry') }}</button>
    </div>

    <!-- Data: one stacked full-width card per managed dependency (llama.cpp today,
         future runtime dependencies append their own cards below). The two column
         wrappers only become real boxes when the runtime is installed on the
         portrait tablet band (rt-installed below: status+components LEFT,
         paths+about RIGHT); everywhere else they dissolve via display:contents
         into the flat stack / desktop two-column grid, leaving the item order
         untouched. The rt-installed hook keeps the first-download state stacked
         full-width. -->
    <template v-else>
      <div class="rt-main-col">
      <!-- llama.cpp Card -->
      <section class="info-section">
        <h2 class="section-title">
          <svg width="18" height="18" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round" stroke-linejoin="round">
            <path d="M14.7 6.3a1 1 0 0 0 0 1.4l1.6 1.6a1 1 0 0 0 1.4 0l3.77-3.77a6 6 0 0 1-7.94 7.94l-6.91 6.91a2.12 2.12 0 0 1-3-3l6.91-6.91a6 6 0 0 1 7.94-7.94l-3.76 3.76z"/>
          </svg>
          {{ t('runtime.llamacpp') }}
          <!-- Phone tier (design draft .island h4 .more): status rides the card
               header — green when installed, AMBER when not found -->
          <span v-if="platformState.isMobile" class="title-status" :class="{ missing: !info.installed }">
            ● {{ info.installed ? t('runtime.llamacpp.installed') : t('runtime.llamacpp.notFound') }}
          </span>
        </h2>
        <div class="info-grid">
          <div class="info-item" v-if="!platformState.isMobile">
            <span class="info-label">{{ t('runtime.llamacpp.status') }}</span>
            <span class="info-value">
              <span class="status-badge" :class="info.installed ? 'available' : 'unavailable'">
                {{ info.installed ? t('runtime.llamacpp.installed') : t('runtime.llamacpp.notFound') }}
              </span>
            </span>
          </div>
          <!-- Phone tier (design draft frame ④): the not-found state explains itself
               under the card title before the download CTA -->
          <p v-if="platformState.isMobile && !info.installed" class="missing-desc">{{ t('runtime.notFoundDesc') }}</p>
          <div class="info-item" v-if="info.version">
            <span class="info-label">{{ t('runtime.llamacpp.version') }}</span>
            <span class="info-value info-value-version">{{ info.version }}</span>
          </div>
          <div class="info-item info-item-full" v-if="info.installed">
            <span class="info-label">{{ t('runtime.llamacpp.path') }}</span>
            <span class="info-value path-value">{{ info.path }}</span>
          </div>
          <div class="info-item info-item-full" v-if="downloadDir">
            <span class="info-label">{{ t('runtime.downloadDir') }}</span>
            <!-- Android (design draft frame ③): download directory lives in
                 app-internal storage managed by the OS — a friendly label
                 instead of the raw sandbox path; other platforms keep the path -->
            <span v-if="platformState.isAndroid" class="info-value info-value-version">{{ t('runtime.storageInternal') }}</span>
            <span v-else class="info-value path-value">{{ downloadDir }}</span>
          </div>
        </div>
      </section>

      <!-- Right column: components + download (desktop ≥1100) -->
      <div class="runtime-main">
        <!-- Installed components: the download fetches the main-program asset plus
             (on Windows CUDA builds) the cudart runtime asset and extracts both into
             the same directory, so each component is reported separately -->
        <div v-if="info.installed" class="components-area">
          <div class="components-title">{{ t('runtime.components') }}</div>
          <div class="comp-row">
            <!-- Phone tier (design draft frame ③): 42px gradient-soft icon tile -->
            <span v-if="platformState.isMobile" class="comp-tile">
              <svg width="20" height="20" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round"><path d="M12 2l8 4.5v9L12 20l-8-4.5v-9L12 2z"/></svg>
            </span>
            <span class="comp-name">
              {{ t('runtime.compMain') }}
              <!-- Acceleration build of the installed main program: prefers the
                   backend's detected accel (from the backend libs next to the
                   binary: CUDA / Vulkan / Metal / CPU), falling back to the
                   platform-capability guess (see lib/platform.ts); Android
                   names its NEON-qualified cpuArm64 build -->
              <span class="comp-desc">{{ accelLabel }}</span>
            </span>
            <span class="status-badge available">{{ t('runtime.llamacpp.installed') }}</span>
          </div>
          <!-- CUDA runtime component: only the Windows release ships a
               separate cudart asset (Linux = Vulkan, macOS = Metal,
               Android = CPU) — the row must not render elsewhere -->
          <div v-if="showCudartRow" class="comp-row">
            <span class="comp-name">
              {{ t('runtime.compCudart') }}
              <span class="comp-desc">{{ t('runtime.compCudartDesc') }}</span>
            </span>
            <span class="status-badge" :class="info.cudartInstalled ? 'available' : 'unavailable'">
              {{ cudartBadge }}
            </span>
          </div>
        </div>

        <!-- Download section: three blocks render independently via v-if (not mutually bound); display conditions unified
             by downloadVisibility, avoiding v-if/v-else-if mutual exclusion that would swallow the progress area -->
        <div v-if="!info.installed && dlStatus.status !== 'done'" class="download-area">
          <!-- Idle/error: show download + custom buttons. The custom-directory
               browse button is Android-gated out (no native dir picker in the
               sandbox; storage is app-managed) -->
          <div v-if="dlVisibility.showButtons" class="download-btns">
            <button class="download-btn" @click="startDownload">
              <svg width="16" height="16" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round" stroke-linejoin="round">
                <path d="M21 15v4a2 2 0 0 1-2 2H5a2 2 0 0 1-2-2v-4"/>
                <polyline points="7 10 12 15 17 10"/>
                <line x1="12" y1="15" x2="12" y2="3"/>
              </svg>
              {{ t('runtime.downloadLlamaCpp') }}
            </button>
            <button v-if="!platformState.isAndroid" class="custom-btn" @click="browseCustomDir">
              <svg width="16" height="16" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round" stroke-linejoin="round">
                <path d="M22 19a2 2 0 0 1-2 2H4a2 2 0 0 1-2-2V5a2 2 0 0 1 2-2h5l2 3h9a2 2 0 0 1 2 2z"/>
              </svg>
              {{ t('runtime.custom') }}
            </button>
          </div>
          <!-- Android storage hint (design draft frame ④): explains why there is no
               custom-directory option -->
          <p v-if="platformState.isAndroid" class="dl-storage-hint">{{ t('runtime.storageHint') }}</p>
          <!-- Custom path info: independent v-if, not bound to progress area -->
          <div v-if="customPath" class="custom-path-info">
            <span class="custom-path-label">{{ t('runtime.customPath') }}</span>
            <span class="custom-path-value">{{ customPath }}</span>
          </div>

          <!-- Downloading / Paused / Extracting / Error: display depends only on download status,
               independent of whether a custom path is set. is-paused drives the
               phone card's amber left edge (mockup frame ④ paused dlcard). -->
          <div v-if="dlVisibility.showProgress" class="download-progress" :class="{ 'is-paused': dlStatus.status === 'paused' }">
            <!-- Overall summary line + one row per package (the cudart row appears only
                 once its asset actually starts downloading; CPU/Vulkan builds never ship it) -->
            <div class="dl-info">
              <!-- Phone tier (mockup .dlcard .t): "下载进度" title sharing the
                   status line's row — title left, combined status right -->
              <span v-if="platformState.isMobile" class="dl-card-title">{{ t('runtime.dlProgressTitle') }}</span>
              <span class="dl-label">{{ statusLabel[dlStatus.status] }}</span>
              <span class="dl-percent" v-if="dlStatus.status === 'downloading' || dlStatus.status === 'paused'">{{ dlStatus.progress }}%</span>
              <!-- Phone tier (mockup .dlcard .t .st): one combined accent /
                   amber status line; desktop keeps the split label + percent -->
              <span
                v-if="platformState.isMobile && statusLineText"
                class="dl-status-line"
                :class="{ paused: dlStatus.status === 'paused', failed: dlStatus.status === 'error' }"
              >{{ statusLineText }}</span>
            </div>
            <div v-for="pkg in packages" :key="pkg.id" class="pkg-row">
              <div class="pkg-head">
                <span class="pkg-name">
                  <svg v-if="pkg.done" width="13" height="13" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2.5" stroke-linecap="round" stroke-linejoin="round"><polyline points="20 6 9 17 4 12"/></svg>
                  {{ pkg.id === 'main' ? t('runtime.pkgMain') : t('runtime.pkgCudart') }}
                </span>
                <span class="pkg-state">
                  <template v-if="pkg.done">{{ t('dl.done') }}</template>
                  <template v-else-if="pkg.active && dlStatus.status === 'extracting'">{{ t('dl.extracting') }}</template>
                  <template v-else>{{ pkg.progress }}%</template>
                </span>
              </div>
              <div class="dl-bar">
                <div
                  class="dl-fill"
                  :class="{ indeterminate: pkg.active && (dlStatus.status === 'fetching' || dlStatus.status === 'extracting'), paused: dlStatus.status === 'paused' }"
                  :style="{ width: pkg.progress + '%' }"
                ></div>
              </div>
              <div class="dl-meta" v-if="pkg.active && dlStatus.fileName">
                <span>{{ dlStatus.fileName }}</span>
                <span v-if="dlStatus.total > 0">{{ formatSize(dlStatus.total) }}</span>
              </div>
            </div>

            <!-- Action buttons -->
            <div class="dl-actions" v-if="dlStatus.status === 'downloading' || dlStatus.status === 'paused' || dlStatus.status === 'fetching'">
              <button v-if="dlStatus.status === 'downloading'" class="dl-btn pause-btn" @click="pauseDownload">
                <svg width="14" height="14" viewBox="0 0 24 24" fill="currentColor"><rect x="6" y="4" width="4" height="16"/><rect x="14" y="4" width="4" height="16"/></svg>
                {{ t('runtime.pause') }}
              </button>
              <button v-if="dlStatus.status === 'paused'" class="dl-btn resume-btn" @click="resumeDownload">
                <svg width="14" height="14" viewBox="0 0 24 24" fill="currentColor"><polygon points="5 3 19 12 5 21 5 3"/></svg>
                {{ t('runtime.resume') }}
              </button>
              <button class="dl-btn stop-btn" @click="stopDownload">
                <svg width="14" height="14" viewBox="0 0 24 24" fill="currentColor"><rect x="4" y="4" width="16" height="16" rx="2"/></svg>
                {{ t('runtime.stop') }}
              </button>
            </div>

            <div class="dl-paused-hint" v-if="dlStatus.status === 'paused'">
              {{ t('runtime.dlPausedHint') }}
            </div>

            <div class="dl-error" v-if="dlStatus.status === 'error'">
              <span>{{ dlStatus.error }}</span>
              <button class="retry-btn-sm" @click="startDownload">{{ t('runtime.retry') }}</button>
            </div>
          </div>
        </div>
      </div>
      </div>

      <div class="rt-side-col">
      <!-- Read-only install-paths card (the portrait band's right column, see
           the rt-installed split in the styles). The DOM gate is the tablet
           tier (isTablet covers the portrait band exactly), so phone and
           desktop keep the paths inside the info card above (they are hidden
           there on the split layout via CSS) -->
      <section v-if="platformState.isTablet && info.installed" class="runtime-paths">
        <h4 class="paths-title">{{ t('runtime.pathsTitle') }}</h4>
        <div class="path-row">
          <span class="path-label">{{ t('runtime.llamacpp.path') }}</span>
          <span class="path-value">{{ info.path }}</span>
        </div>
        <div v-if="downloadDir" class="path-row">
          <span class="path-label">{{ t('runtime.downloadDir') }}</span>
          <!-- Android keeps the friendly app-storage label instead of the raw
               sandbox path (same convention as the info card above) -->
          <span v-if="platformState.isAndroid" class="path-value">{{ t('runtime.storageInternal') }}</span>
          <span v-else class="path-value">{{ downloadDir }}</span>
        </div>
        <p class="paths-hint">{{ t('runtime.pathsHint') }}</p>
      </section>

      <!-- "About the runtime" island (design draft frame ③): desktop ≥1100 shows
           in the left column alongside the info card; the portrait band packs
           it into its right column (rt-installed split) -->
      <section class="runtime-about">
        <h4>{{ t('runtime.aboutTitle') }}</h4>
        <!-- Android keeps its app-internal-storage wording; every other platform
             gets the desktop variant (download + Settings custom-directory) -->
        <p>{{ platformState.isAndroid ? t('runtime.aboutBody') : t('runtime.aboutBodyDesktop') }}</p>
      </section>
      </div>
    </template>
  </section>
</template>

<script setup lang="ts">
import { ref, computed, watch, onMounted, onUnmounted } from 'vue'
import {
  getLlamaCpp, getLlamaCppDownloadStatus, startLlamaCppDownload, pauseLlamaCppDownload,
  resumeLlamaCppDownload, stopLlamaCppDownload, browseLlamaCppDir, getConfig
} from '../wails'
import { downloadVisibility, initialDownloadAction, isCudartAsset, packageRows } from '../lib/llamaDownload'
import { accelBuildKey, showCudaRuntimeComponent, usePlatform } from '../lib/platform'
import { t } from '../lib/i18n'

interface LlamaCppInfo {
  installed: boolean
  path: string
  version: string
  cudartInstalled: boolean
  /** CUDA major family of the installed cudart runtime ("13", "12"), "" when unknown */
  cudartVersion: string
  /** Detected acceleration backend of the installed build:
   * "cuda" | "vulkan" | "metal" | "cpu"; "" when not installed / unknown. */
  accel: string
}

const info = ref<LlamaCppInfo>({ installed: false, path: '', version: '', cudartInstalled: false, cudartVersion: '', accel: '' })
const loading = ref(true)
const error = ref('')

// Capability gates from the shared platform state (OS-scoped): the cudart
// component row renders on Windows only, and the acceleration label names the
// actually installed backend when the probe answered, falling back to the
// platform's llama.cpp build guess (CPU/CUDA vs Vulkan vs Metal vs CPU).
const platformState = usePlatform()
const showCudartRow = computed(() => showCudaRuntimeComponent(platformState.value))
const accelKey = computed(() => {
  const detected = info.value.accel
  if (detected === 'cuda' || detected === 'vulkan' || detected === 'metal' || detected === 'cpu') {
    return detected
  }
  const fallback = accelBuildKey(platformState.value)
  // Android keeps the arm64 qualifier on its CPU-only build label
  if (fallback === 'cpu' && platformState.value.isAndroid) {
    return 'cpuArm64'
  }
  return fallback
})

// Component-row sublabel: Android names its NEON-qualified cpuArm64 build
// (design draft frame ③); every other platform keeps the plain accel label
const accelLabel = computed(() => {
  if (platformState.value.isAndroid && accelKey.value === 'cpuArm64') {
    return t('runtime.accel.cpuArm64Neon')
  }
  return t('runtime.accel.' + accelKey.value)
})

// Cudart component badge: append the detected CUDA major family (parsed from
// the cudart64_*.dll file name) to the installed label when known
const cudartBadge = computed(() => {
  if (!info.value.cudartInstalled) return t('runtime.compNotDetected')
  return info.value.cudartVersion
    ? `${t('runtime.llamacpp.installed')} · CUDA ${info.value.cudartVersion}`
    : t('runtime.llamacpp.installed')
})

// Download state
interface DlStatus {
  status: string  // idle | fetching | downloading | paused | extracting | done | error
  progress: number
  total: number
  downloaded: number
  fileName: string
  version: string
  error: string
}
const dlStatus = ref<DlStatus>({ status: 'idle', progress: 0, total: 0, downloaded: 0, fileName: '', version: '', error: '' })
const customPath = ref('')
const downloadDir = ref('')
let pollTimer: ReturnType<typeof setInterval> | null = null

// Download area visibility: button group / custom path info / progress area render independently (see lib/llamaDownload)
const dlVisibility = computed(() => downloadVisibility(dlStatus.value.status))

// Cumulative byte count snapshotted when the file name switches from the main
// asset to the cudart asset: the backend reports one combined Downloaded/Total
// pair across both sequential packages, so the cudart row's own share is
// (Downloaded − mainBytes) / (Total − mainBytes). A mid-download remount joins
// with the cudart asset already current (mainBytes still 0); the best available
// snapshot is the cumulative count at that moment. Reset when the file name
// clears (retry/idle).
const mainBytes = ref(0)
watch(() => dlStatus.value.fileName, (name, prev) => {
  if (!name) {
    mainBytes.value = 0
    return
  }
  if (isCudartAsset(name) && (mainBytes.value === 0 || (prev && !isCudartAsset(prev)))) {
    mainBytes.value = dlStatus.value.downloaded
  }
})

// Per-package progress rows for the download area (see lib/llamaDownload.packageRows)
const packages = computed(() =>
  packageRows(dlStatus.value.fileName, dlStatus.value.downloaded, dlStatus.value.total, mainBytes.value))

function formatSize(bytes: number): string {
  if (bytes <= 0) return ''
  if (bytes < 1024 * 1024) return `${(bytes / 1024).toFixed(0)} KB`
  if (bytes < 1024 * 1024 * 1024) return `${(bytes / (1024 * 1024)).toFixed(1)} MB`
  return `${(bytes / (1024 * 1024 * 1024)).toFixed(2)} GB`
}

// Download status → text mapping: wrapped in computed so t() re-evaluates on locale switch,
// ensuring status labels update instantly when language changes (computed ref auto-unwraps in template, no template change needed)
const statusLabel = computed<Record<string, string>>(() => ({
  idle: '',
  fetching: t('dl.fetching'),
  downloading: t('dl.downloading'),
  paused: t('dl.paused'),
  extracting: t('dl.extracting'),
  done: t('dl.done'),
  error: t('dl.error'),
}))

// Phone-tier combined status line (mockup .dlcard .t .st): "下载中 · 45%" /
// "已暂停 · 62%", plain label while fetching / extracting / failed; '' hides it
const statusLineText = computed(() => {
  const s = dlStatus.value.status
  if (s === 'downloading' || s === 'paused') {
    return `${statusLabel.value[s]} · ${dlStatus.value.progress}%`
  }
  if (s === 'fetching' || s === 'extracting' || s === 'error') {
    return statusLabel.value[s]
  }
  return ''
})

// Poll download status
function pollDownloadStatus() {
  getLlamaCppDownloadStatus()
    .then((data: any) => {
      dlStatus.value = data as DlStatus
      if (dlStatus.value.status === 'done' || dlStatus.value.status === 'error') {
        stopPolling()
        if (dlStatus.value.status === 'done') {
          setTimeout(fetchInfo, 500)
        }
      }
    })
    .catch(() => {})
}

function startPolling() {
  stopPolling()
  pollTimer = setInterval(pollDownloadStatus, 500)
  pollDownloadStatus()
}

function stopPolling() {
  if (pollTimer) {
    clearInterval(pollTimer)
    pollTimer = null
  }
}

async function startDownload() {
  try {
    dlStatus.value = { status: 'fetching', progress: 0, total: 0, downloaded: 0, fileName: '', version: '', error: '' }
    startPolling()
    await startLlamaCppDownload()
  } catch (e: any) {
    dlStatus.value.status = 'error'
    dlStatus.value.error = t('runtime.backendError', { msg: e.message })
    stopPolling()
  }
}

async function pauseDownload() {
  try { await pauseLlamaCppDownload() } catch {}
}

async function resumeDownload() {
  try { await resumeLlamaCppDownload() } catch {}
}

async function stopDownload() {
  try {
    await stopLlamaCppDownload()
    stopPolling()
    setTimeout(pollDownloadStatus, 300)
  } catch {}
}

async function browseCustomDir() {
  try {
    const dir = await browseLlamaCppDir()
    if (dir) {
      customPath.value = dir
      await fetchInfo()
    }
  } catch {}
}

// Restore the custom llama.cpp dir and the download dir from backend config on mount,
// so returning to the page shows both paths (#restore-original)
async function restoreConfigPaths() {
  try {
    const cfg = await getConfig()
    if (cfg.llamaCppDir) customPath.value = cfg.llamaCppDir
    if (cfg.llamaCppDownloadDir) downloadDir.value = cfg.llamaCppDownloadDir
  } catch {}
}

// Check download status on mount (in case there's an ongoing download)
function checkInitialDownloadStatus() {
  getLlamaCppDownloadStatus()
    .then((data: any) => {
      const s = data as DlStatus
      const action = initialDownloadAction(s.status, info.value.installed)
      if (action === 'poll') {
        // Download still in progress; resume polling to keep updating progress
        dlStatus.value = s
        startPolling()
      } else if (action === 'refresh') {
        // Download completed but install not detected; refresh info
        fetchInfo()
      } else if (action === 'showError') {
        // Download failed while away: restore error state; UI auto-shows error info and retry button
        // (downloadVisibility's showButtons / showProgress both cover error)
        dlStatus.value = s
      }
      // 'none': no action needed
    })
    .catch(() => {})
}

async function fetchInfo() {
  loading.value = true
  error.value = ''
  try {
    info.value = await getLlamaCpp()
  } catch (e: any) {
    error.value = t('runtime.backendError', { msg: e.message })
  } finally {
    loading.value = false
  }
}

onMounted(() => {
  restoreConfigPaths()
  fetchInfo().then(checkInitialDownloadStatus)
})

// Stop polling on unmount to avoid interval spinning until download finishes;
// returning to the page remounts and restarts polling (download continues in backend goroutine)
onUnmounted(() => {
  stopPolling()
})
</script>

<style scoped>
/* ─── Tab panel: stacked full-width dependency cards (heading lives in the
       shell's tab bar) ─── */
.runtime-section {
  /* Release the automatic minimum so long unbreakable strings (install paths)
     shrink inside the panel instead of stretching it */
  min-width: 0;
}

/* Column wrappers (the portrait band's rt-installed split): they only become
   real boxes there; every other tier dissolves them via display:contents so
   the stack / desktop grid item order is unchanged */
.rt-main-col,
.rt-side-col {
  display: contents;
}

.info-section {
  margin-bottom: 16px;
  padding: 24px 28px;
  background: var(--surface);
  border: 1px solid var(--border);
  border-radius: var(--r-lg);
  box-shadow: var(--shadow-island);
  transition: border-color 0.2s;
}

.info-section:hover {
  border-color: var(--overlay-10);
}

.section-title {
  display: flex;
  align-items: center;
  gap: 10px;
  font-size: 15px;
  font-weight: 600;
  color: var(--text-secondary);
  margin: 0 0 18px;
}

.section-title svg {
  color: var(--accent-light);
  flex-shrink: 0;
}

/* ─── Phone-tier-only elements (render behind platformState.isMobile /
       isAndroid v-if gates, so these base styles never apply on desktop) ─── */

/* Page heading inside the panel (mockup .h-greet, frames ③/④) */
.runtime-page-head {
  padding: 14px 4px;
}

.runtime-page-head h1 {
  margin: 0;
  font-size: 24px;
  font-weight: 800;
  letter-spacing: -0.4px;
  line-height: 1.2;
  color: var(--text-primary);
}

.runtime-page-head p {
  margin: 6px 0 0;
  font-size: 11.5px;
  color: var(--text-muted);
}

/* Island header status (mockup .island h4 .more): green / amber dot line */
.title-status {
  margin-left: auto;
  font-size: 12px;
  font-weight: 700;
  color: var(--success);
  white-space: nowrap;
}

.title-status.missing {
  color: var(--warning);
}

/* Component-row icon tile (mockup frame ③: 42px gradient-soft brick) */
.comp-tile {
  width: 42px;
  height: 42px;
  border-radius: 13px;
  background: var(--grad-soft);
  color: var(--accent-light);
  display: flex;
  align-items: center;
  justify-content: center;
  flex-shrink: 0;
}

/* Combined download status line (mockup .dlcard .t .st) */
.dl-status-line {
  margin-left: auto;
  font-size: 12px;
  font-weight: 700;
  color: var(--accent-light);
}

/* Phone-tier card title (mockup .dlcard .t left slot, "下载进度"): rendered
   only behind the isMobile v-if inside .dl-info (flex row), so this base
   style is phone-only already */
.dl-card-title {
  font-size: 13px;
  font-weight: 700;
  color: var(--text-primary);
}

.dl-status-line.paused {
  color: var(--warning);
}

.dl-status-line.failed {
  color: var(--danger);
}

/* Android storage hint under the download CTA (mockup frame ④) */
.dl-storage-hint {
  margin: 10px 0 0;
  font-size: 11px;
  color: var(--text-dim);
}

/* Phone-tier not-found description (mockup frame ④ body text under the
   "● 未找到" card title; rendered behind the isMobile v-if) */
.missing-desc {
  grid-column: 1 / -1;
  margin: 0;
  font-size: 12.5px;
  line-height: 1.8;
  color: var(--text-secondary);
}

/* "About the runtime" island (mockup frame ③) */
.runtime-about {
  background: var(--bg-secondary);
  border: 1px solid var(--border);
  border-radius: var(--r-lg);
  box-shadow: var(--shadow-island);
  padding: 20px;
  margin-bottom: 16px;
  min-width: 0;
}

.runtime-about h4 {
  margin: 0 0 12px;
  font-size: 13px;
  font-weight: 700;
  letter-spacing: 0.3px;
  color: var(--text-secondary);
}

.runtime-about p {
  margin: 0;
  font-size: 13.5px;
  line-height: 1.75;
  color: var(--text-muted);
}

/* ─── Read-only install-paths rail card (the portrait band's right column —
       see the rt-installed split). Renders only behind the isTablet v-if, so
       these base styles never apply on phone / desktop tiers. ─── */
.runtime-paths {
  padding: 24px 28px;
  background: var(--surface);
  border: 1px solid var(--border);
  border-radius: var(--r-lg);
  min-width: 0;
}

.paths-title {
  margin: 0 0 14px;
  font-size: 13px;
  font-weight: 700;
  letter-spacing: 0.3px;
  color: var(--text-secondary);
}

.path-row {
  padding: 10px 0;
  min-width: 0;
}

.path-row + .path-row {
  border-top: 1px dashed var(--border);
}

.path-label {
  display: block;
  font-size: 12px;
  color: var(--text-dim);
  text-transform: uppercase;
  letter-spacing: 0.5px;
  font-weight: 500;
  margin-bottom: 4px;
}

.paths-hint {
  margin: 10px 0 0;
  padding-top: 10px;
  border-top: 1px dashed var(--border);
  font-size: 11.5px;
  line-height: 1.7;
  color: var(--text-dim);
}

/* ─── Info grid ─── */
.info-grid {
  display: grid;
  grid-template-columns: repeat(auto-fit, minmax(140px, 1fr));
  gap: 14px;
}

.info-item {
  display: flex;
  flex-direction: column;
  gap: 4px;
  /* Release the grid-item automatic minimum so the ellipsized path-value
     can shrink to the track width instead of stretching the item */
  min-width: 0;
}

.info-item-full {
  grid-column: 1 / -1;
}

.info-label {
  font-size: 12px;
  color: var(--text-dim);
  text-transform: uppercase;
  letter-spacing: 0.5px;
  font-weight: 500;
}

.info-value {
  font-size: 14px;
  color: var(--text-secondary);
  font-weight: 500;
  /* Soft wrap at word boundaries only; long unbreakable strings may overflow
     visually instead of being cut mid-word (version strings, hex hashes) */
  overflow-wrap: break-word;
}

.path-value {
  font-family: var(--font-mono);
  font-size: 12px;
  color: var(--text-muted);
  /* Single-line ellipsis: install paths stay on one line and truncate at the end */
  display: block;
  min-width: 0;
  white-space: nowrap;
  overflow: hidden;
  text-overflow: ellipsis;
}

/* ─── Status badge ─── */
.status-badge {
  display: inline-flex;
  align-items: center;
  gap: 6px;
  padding: 2px 12px;
  border-radius: var(--r-sm);
  font-size: 12px;
  font-weight: 600;
}

.status-badge.available {
  background: rgba(34, 197, 94, 0.12);
  color: var(--success);
  border: 1px solid rgba(34, 197, 94, 0.2);
}

.status-badge.unavailable {
  background: rgba(239, 68, 68, 0.1);
  color: var(--danger);
  border: 1px solid rgba(239, 68, 68, 0.15);
}

/* ─── Loading skeleton ─── */
.skeleton-card {
  padding: 24px 28px;
  background: var(--surface);
  border: 1px solid var(--skeleton-bg);
  border-radius: var(--r-lg);
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
  background: var(--active-bg);
  color: var(--accent-light);
  border: 1px solid var(--overlay-20);
  border-radius: 8px;
  font-size: 13px;
  font-weight: 500;
  cursor: pointer;
  transition: all 0.2s;
}

.retry-btn:hover {
  background: var(--accent-glow);
}

/* ─── Download area ─── */
.download-area {
  margin-top: 20px;
  padding-top: 16px;
  border-top: 1px solid var(--border);
}

.download-btns {
  display: flex;
  gap: 10px;
  flex-wrap: wrap;
}

.download-btn {
  display: inline-flex;
  align-items: center;
  gap: 8px;
  padding: 10px 22px;
  /* Primary action = solid brand gradient + white label + glow (design draft
     v2 principle ③); the secondary "自定义" stays a bordered neutral button. */
  background: var(--grad);
  color: #fff;
  border: none;
  border-radius: var(--r-md);
  font-size: 14px;
  font-weight: 600;
  cursor: pointer;
  transition: all 0.2s ease;
  box-shadow: 0 6px 16px rgba(124, 92, 246, 0.42);
}

.download-btn:hover {
  color: #fff;
  transform: translateY(-1px);
  box-shadow: 0 8px 20px rgba(124, 92, 246, 0.5);
}

.download-btn:active {
  transform: translateY(0);
  box-shadow: 0 6px 16px rgba(124, 92, 246, 0.42);
}

/* ─── Custom button ─── */
.custom-btn {
  display: inline-flex;
  align-items: center;
  gap: 8px;
  padding: 10px 22px;
  /* Secondary action: surface fill + 1px border (design draft v2 principle ⑤) */
  background: var(--surface);
  color: var(--text-muted);
  border: 1px solid var(--border);
  border-radius: var(--r-md);
  font-size: 14px;
  font-weight: 500;
  cursor: pointer;
  transition: all 0.2s ease;
}

.custom-btn:hover {
  background: var(--surface-2);
  border-color: var(--overlay-20);
  color: var(--text-primary);
}

.custom-btn:active {
  background: var(--overlay-10);
  transform: scale(0.98);
}

.custom-path-info {
  display: flex;
  align-items: center;
  gap: 8px;
  margin-top: 12px;
  padding: 8px 14px;
  background: rgba(34, 197, 94, 0.06);
  border: 1px solid rgba(34, 197, 94, 0.12);
  border-radius: 8px;
}

.custom-path-label {
  font-size: 12px;
  color: var(--text-dim);
}

.custom-path-value {
  font-size: 12px;
  color: var(--success);
  font-family: var(--font-mono);
  word-break: break-all;
}

/* ─── Installed components list ─── */
.components-area {
  margin-top: 20px;
  padding-top: 16px;
  border-top: 1px solid var(--border);
  display: flex;
  flex-direction: column;
  gap: 10px;
}

.components-title {
  font-size: 12px;
  color: var(--text-dim);
  text-transform: uppercase;
  letter-spacing: 0.5px;
  font-weight: 500;
}

.comp-row {
  display: flex;
  align-items: center;
  justify-content: space-between;
  gap: 12px;
}

.comp-name {
  font-size: 14px;
  color: var(--text-secondary);
  font-weight: 500;
  display: inline-flex;
  align-items: baseline;
  gap: 8px;
  flex-wrap: wrap;
}

.comp-desc {
  font-size: 12px;
  color: var(--text-dim);
  font-weight: 400;
}

/* ─── Download progress ─── */
.download-progress {
  display: flex;
  flex-direction: column;
  gap: 10px;
}

.pkg-row {
  display: flex;
  flex-direction: column;
  gap: 6px;
  padding-bottom: 4px;
}

.pkg-head {
  display: flex;
  justify-content: space-between;
  align-items: center;
}

.pkg-name {
  display: inline-flex;
  align-items: center;
  gap: 6px;
  font-size: 13px;
  color: var(--text-secondary);
}

.pkg-name svg {
  color: var(--success);
  flex-shrink: 0;
}

.pkg-state {
  font-size: 12px;
  color: var(--text-muted);
}

.dl-info {
  display: flex;
  justify-content: space-between;
  align-items: center;
}

.dl-label {
  font-size: 13px;
  color: var(--text-muted);
}

.dl-percent {
  font-size: 13px;
  font-weight: 700;
  color: var(--accent-light);
}

.dl-bar {
  height: 6px;
  background: var(--overlay-8);
  border-radius: 3px;
  overflow: hidden;
}

.dl-fill {
  height: 100%;
  border-radius: 3px;
  background: var(--grad);
  transition: width 0.3s ease;
}

.dl-fill.indeterminate {
  width: 30% !important;
  animation: dl-slide 1.5s ease-in-out infinite;
}

@keyframes dl-slide {
  0% { transform: translateX(-100%); }
  100% { transform: translateX(430%); }
}

.dl-meta {
  display: flex;
  justify-content: space-between;
  font-size: 11px;
  color: var(--text-dim);
  font-family: var(--font-mono);
}

.dl-error {
  display: flex;
  align-items: center;
  justify-content: space-between;
  padding: 8px 12px;
  background: rgba(239, 68, 68, 0.08);
  border-radius: 8px;
  font-size: 12px;
  color: rgba(239, 68, 68, 0.8);
}

/* ─── Download action buttons ─── */
.dl-actions {
  display: flex;
  gap: 8px;
  padding-top: 4px;
}

.dl-btn {
  display: inline-flex;
  align-items: center;
  gap: 5px;
  padding: 5px 14px;
  border-radius: 7px;
  font-size: 12px;
  font-weight: 500;
  cursor: pointer;
  border: 1px solid transparent;
  transition: all 0.2s ease;
}

.pause-btn {
  background: rgba(251, 191, 36, 0.12);
  color: #fbbf24;
  border-color: rgba(251, 191, 36, 0.2);
}

.pause-btn:hover {
  background: rgba(251, 191, 36, 0.2);
  border-color: rgba(251, 191, 36, 0.35);
}

.resume-btn {
  background: rgba(34, 197, 94, 0.12);
  color: var(--success);
  border-color: rgba(34, 197, 94, 0.2);
}

.resume-btn:hover {
  background: rgba(34, 197, 94, 0.2);
  border-color: rgba(34, 197, 94, 0.35);
}

.stop-btn {
  background: rgba(239, 68, 68, 0.08);
  color: rgba(239, 68, 68, 0.7);
  border-color: rgba(239, 68, 68, 0.15);
}

.stop-btn:hover {
  background: rgba(239, 68, 68, 0.15);
  border-color: rgba(239, 68, 68, 0.3);
  color: rgba(239, 68, 68, 0.9);
}

.dl-paused-hint {
  font-size: 12px;
  color: rgba(251, 191, 36, 0.6);
}

.dl-fill.paused {
  background: linear-gradient(90deg, #fbbf24, #f59e0b);
}

.retry-btn-sm {
  padding: 4px 14px;
  background: rgba(99, 102, 241, 0.15);
  color: var(--accent-light);
  border: 1px solid rgba(99, 102, 241, 0.25);
  border-radius: 6px;
  font-size: 12px;
  cursor: pointer;
  transition: all 0.2s;
  flex-shrink: 0;
}

.retry-btn-sm:hover {
  background: rgba(99, 102, 241, 0.25);
}

/* ─── Phone (<=767px): design draft frames ③/④. The llama.cpp card becomes
       a 28px floating island; the download CTA is one full-width gradient
       button; the progress area wraps in its own 22px bordered card with the
       paused state carrying an amber left edge; ops become 999px pills. All
       interactive controls keep ≥44px touch height. Tablet (768px+) keeps the
       shared desktop layout. ─── */
@media (max-width: 767px) {
  /* D1: island treatment for the main card (mockup .island) */
  .info-section {
    padding: 20px;
    background: var(--bg-secondary);
    border-radius: var(--r-lg);
    box-shadow: var(--shadow-island);
  }

  /* Version value bold (mockup frame ③ <b>b5913</b>) */
  .info-value-version {
    font-weight: 700;
  }

  /* D4: component row — name bold with the sublabel stacked beneath */
  .comp-row {
    flex-wrap: wrap;
  }

  .comp-name {
    flex: 1;
    min-width: 0;
    display: block;
    font-size: 14px;
    font-weight: 700;
  }

  .comp-desc {
    display: block;
    margin-top: 2px;
    font-size: 11px;
  }

  /* D5: download CTA — single full-width gradient button (mockup frame ④) */
  .download-btns {
    flex-direction: column;
  }

  .download-btn {
    justify-content: center;
    width: 100%;
    min-height: 44px;
    padding: 13px 0;
    background: var(--grad);
    color: #fff;
    border: none;
    border-radius: var(--r-md);
    font-size: 13.5px;
    font-weight: 800;
    box-shadow: none;
  }

  .download-btn:hover {
    background: var(--grad);
    border-color: transparent;
    color: #fff;
    transform: none;
    box-shadow: none;
  }

  .custom-btn {
    justify-content: center;
    width: 100%;
    min-height: 44px;
  }

  /* D6: progress area wraps as its own card (mockup .dlcard) */
  .download-progress {
    background: var(--bg-secondary);
    border: 1px solid var(--border);
    border-radius: var(--r-md);
    padding: 16px 18px;
  }

  .download-progress.is-paused {
    border-left: 3px solid var(--warning);
  }

  /* Combined status line replaces the split label + percent */
  .dl-label,
  .dl-percent {
    display: none;
  }

  /* 7px rounded progress bar (mockup .bar) */
  .dl-bar {
    height: 7px;
    border-radius: 999px;
  }

  .dl-fill {
    border-radius: 999px;
  }

  /* Ops pills (mockup .pill): 999px radius, neutral pause / gradient resume
     (mockup .pill.g) / red-bg stop (mockup .pill.r); min-height keeps the 44px
     touch target */
  .dl-btn {
    min-height: 44px;
    padding: 6px 13px;
    border-radius: 999px;
    font-size: 11.5px;
    font-weight: 700;
  }

  .pause-btn {
    background: var(--surface-2);
    color: var(--text-secondary);
    border-color: transparent;
  }

  .pause-btn:hover {
    background: var(--surface-2);
    color: var(--text-secondary);
    border-color: transparent;
  }

  .resume-btn {
    background: var(--grad);
    color: #fff;
    border-color: transparent;
    box-shadow: none;
  }

  .resume-btn:hover {
    background: var(--grad);
    color: #fff;
    border-color: transparent;
  }

  .stop-btn {
    background: var(--danger-bg);
    color: var(--danger);
    border-color: transparent;
  }

  .stop-btn:hover {
    background: var(--danger-bg);
    color: var(--danger);
    border-color: transparent;
  }

  /* Paused note at full amber (mockup frame ④) */
  .dl-paused-hint {
    color: var(--warning);
  }

  .retry-btn-sm {
    min-height: 44px;
  }

  .retry-btn {
    min-height: 44px;
  }
}

/* Tablet centered column: 768–1099px (design draft F7) */
@media (min-width: 768px) and (max-width: 1099px) {
  .runtime-section {
    max-width: 800px;
    margin-left: auto;
    margin-right: auto;
  }

  /* Frame ㉑ errcard at tablet scale: the boundary-state actions keep the
     44px touch target (draft ㉒/B21 — touch first, no hover dependence) */
  .retry-btn,
  .retry-btn-sm {
    min-height: 44px;
  }

  /* ─── Installed state: two-column split (portrait pass). Status card +
       components island take the LEFT 1fr column; the read-only paths card +
       about island pack a ~300px RIGHT column, filling the dead bottom area
       of the old single column. Hooked on .rt-installed so the first-download
       state (progress + explanation) keeps its stacked full-width flow.
       Scoped to the band with min-width: 768px. ─── */
  .runtime-section.rt-installed {
    display: grid;
    grid-template-columns: minmax(0, 1fr) 300px;
    gap: 16px;
    align-items: start;
  }

  .runtime-section.rt-installed .rt-main-col,
  .runtime-section.rt-installed .rt-side-col {
    display: flex;
    flex-direction: column;
    gap: 16px;
    min-width: 0;
  }

  .runtime-section.rt-installed .rt-main-col {
    grid-column: 1;
  }

  .runtime-section.rt-installed .rt-side-col {
    grid-column: 2;
  }

  /* Grid-cell treatment: the stacked-flow outer margins vanish (the column
     gap takes over) */
  .runtime-section.rt-installed .info-section,
  .runtime-section.rt-installed .runtime-paths,
  .runtime-section.rt-installed .runtime-about {
    margin-bottom: 0;
  }

  /* The components list becomes its own island in the LEFT column */
  .runtime-section.rt-installed .components-area {
    margin-top: 0;
    padding: 20px 24px;
    background: var(--surface);
    border: 1px solid var(--border);
    border-radius: var(--r-lg);
  }

  /* The in-card install paths move into the right-rail card on this layout */
  .runtime-section.rt-installed .info-section .info-item-full {
    display: none;
  }

  /* The narrower right column tightens the paths card padding */
  .runtime-section.rt-installed .runtime-paths {
    padding: 20px 22px;
  }

  /* The narrower left column squeezes the component-row status badge into
     wrapping mid-word ("已安/装"): keep badges on one line and let the name
     flex instead */
  .status-badge {
    flex-shrink: 0;
    white-space: nowrap;
  }

  /* Skeleton / error states span the full split width */
  .runtime-section.rt-installed > .skeleton-card,
  .runtime-section.rt-installed > .error-card {
    grid-column: 1 / -1;
  }
}

/* Desktop layout: constrain to 1280px max-width (design draft D5) */
@media (min-width: 1280px) {
  .runtime-section {
    max-width: 1280px;
    margin-left: auto;
    margin-right: auto;
  }
}

/* Desktop ≥1100: two-column layout — left: info card + about, right: components + download (design draft F6) */
@media (min-width: 1100px) {
  .runtime-section {
    display: grid;
    grid-template-columns: 1fr 1fr;
    gap: 20px;
  }

  .info-section {
    /* Left column: info card */
  }

  .runtime-about {
    /* Left column: about card, below info card */
  }

  .runtime-main {
    /* Right column: components + download */
  }
}
</style>
