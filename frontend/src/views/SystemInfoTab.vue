<template>
  <!-- System Info tab panel of the System Environment page (Home.vue shell):
       redesigned per the v1 mobile design draft frame ① — a gradient hero
       card answering "is the AI usable right now", a two-column mini-card pair
       (memory / CPU with SVG rings), a storage island (disk bar + GGUF and
       llama.cpp bricks) and the resident-model card with in-place unload.
       The platform-gated GPU / CUDA cards and the system card keep their
       existing content below in the same island visual language: they are
       capability surfaces the Android-first mockup never had, so they stay
       where their probes exist. The shell owns the greeting header + tab bar
       + refresh toolbar, so there is no page chrome here — only the cards. -->
  <div class="system-info-tab">
    <!-- Shared SVG gradient for the mini-card rings: declared once and
         referenced by url(#home-ring-grad) from both ring SVGs -->
    <svg width="0" height="0" style="position: absolute" aria-hidden="true">
      <defs>
        <linearGradient id="home-ring-grad" x1="0" y1="0" x2="1" y2="1">
          <stop offset="0%" stop-color="#6366f1" />
          <stop offset="100%" stop-color="#a855f7" />
        </linearGradient>
      </defs>
    </svg>

    <!-- Loading skeleton -->
    <div v-if="loading" class="sys-grid">
      <div class="island skeleton-card hero-skel">
        <div class="skeleton-line skeleton-title"></div>
        <div class="skeleton-line"></div>
        <div class="skeleton-line skeleton-short"></div>
      </div>
      <div class="grid2">
        <div v-for="i in 2" :key="i" class="island skeleton-card mini-skel">
          <div class="skeleton-line skeleton-title"></div>
          <div class="skeleton-line skeleton-short"></div>
        </div>
      </div>
      <div class="island skeleton-card">
        <div class="skeleton-line skeleton-title"></div>
        <div class="skeleton-line"></div>
        <div class="skeleton-line skeleton-short"></div>
      </div>
    </div>

    <!-- Error state: every core probe failed (e.g. backend bridge missing) -->
    <div v-else-if="loadError" class="error-card">
      <div class="error-icon">
        <svg width="48" height="48" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="1.5" stroke-linecap="round" stroke-linejoin="round">
          <circle cx="12" cy="12" r="10"/><line x1="12" y1="8" x2="12" y2="12"/><line x1="12" y1="16" x2="12.01" y2="16"/>
        </svg>
      </div>
      <h2>{{ t('home.errorTitle') }}</h2>
      <p>{{ loadError }}</p>
      <button class="retry-btn" @click="fetchSystemInfo(true)">{{ t('home.retry') }}</button>
    </div>

    <!-- Data. The sys-paired hook (exactly one resident model — see
         lib/homeLayout) lets the portrait band pair the resident-model card
         with the system summary card in one two-column row; first-use and
         multi-resident states keep the single-column stack. -->
    <div v-else class="sys-grid" :class="{ 'sys-paired': residentPairing }">
      <!-- Quick-start checklist: hides once every step completes or the user
           dismisses it. It guides across tabs (its actions router.push other
           routes), which works from any tab. Lives in the side column so the
           portrait band's right column can pack it above the storage island. -->
      <div class="home-side-col">
      <section v-if="onboardingView.visible" class="island onboarding-card">
        <div class="onboarding-head">
          <h2 class="section-title">
            <svg width="18" height="18" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round" stroke-linejoin="round">
              <path d="M4 15s1-1 4-1 5 2 8 2 4-1 4-1V3s-1 1-4 1-5-2-8-2-4 1-4 1z"/><line x1="4" y1="22" x2="4" y2="15"/>
            </svg>
            {{ t('onboarding.title') }}
          </h2>
          <button class="onboarding-dismiss" @click="dismissOnboarding">{{ t('onboarding.dismiss') }}</button>
        </div>
        <ol class="onboarding-steps">
          <li
            v-for="(step, idx) in onboardingView.steps"
            :key="step.id"
            class="onboarding-step"
            :class="{ done: step.done }"
          >
            <span class="step-marker">
              <svg v-if="step.done" width="12" height="12" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="3" stroke-linecap="round" stroke-linejoin="round">
                <polyline points="20 6 9 17 4 12"/>
              </svg>
              <template v-else>{{ idx + 1 }}</template>
            </span>
            <span class="step-text">
              <span class="step-label">{{ t(ONBOARDING_LABELS[step.id]) }}</span>
            <!-- Checklist tiers (draft .cstep .sd): muted per-step sub-description -->
            <span v-if="checklistDetails" class="step-sub">{{ t(ONBOARDING_SUBS[step.id]) }}</span>
            </span>
            <button v-if="!step.done" class="step-action" @click="goStep(step.route)">
              {{ stepGoLabel(step.id) }}
              <svg width="12" height="12" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round" stroke-linejoin="round">
                <line x1="5" y1="12" x2="19" y2="12"/><polyline points="12 5 19 12 12 19"/>
              </svg>
            </button>
            <span v-else class="step-done-label">{{ t('onboarding.done') }}</span>
          </li>
        </ol>
      </section>


      <!-- Storage island (frame ①): disk usage bar + GGUF and llama.cpp
           bricks. The bar keeps the design's amber "disk level" color. -->
      <section class="island storage-card">
        <div class="island-head">
          <h4>{{ t('home.storage') }}</h4>
          <span v-if="diskView" class="head-more">{{ storageFree }}</span>
        </div>
        <template v-if="diskView">
          <div class="storage-row">
            <span>{{ t('home.storage.used', { size: formatBytes(diskView.used) || '—' }) }}</span>
            <b>{{ diskPct }}%</b>
          </div>
          <div
            class="storage-bar"
            role="progressbar"
            :aria-valuenow="diskPct"
            aria-valuemin="0"
            aria-valuemax="100"
            :aria-label="t('home.storage')"
          >
            <i :style="{ width: diskPct + '%' }"></i>
          </div>
        </template>
        <div v-else class="info-empty">
          <span>{{ t('home.disk.notAvailable') }}</span>
        </div>
        <div class="brick-row">
          <div class="brick">
            <span class="brick-lbl">{{ t('home.storage.models') }}</span>
            <b class="brick-val" :title="ggufBrick">{{ ggufBrick }}</b>
          </div>
          <div class="brick">
            <span class="brick-lbl">{{ t('home.storage.llamacpp') }}</span>
            <!-- Phone tier (design draft frame ①): the "not installed" brick reads
                 amber in the first-use state -->
            <b class="brick-val" :class="{ warn: !runtimeInstalled }" :title="llamacppBrick">{{ llamacppBrick }}</b>
          </div>
        </div>
      </section>

      <!-- Resident model cards (frame ① mcard): one per loaded model, with
           the same unload chain as TaskDock: desktop asks the router to evict
           the model; direct-mode Android stops the service (unloading =
           stopping, memory is freed with the process and the next chat send
           auto-restarts the service). -->
      <section v-for="m in loadedModels" :key="m.id" class="island mcard">
        <div class="tile">
          <svg width="22" height="22" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round" stroke-linejoin="round">
            <path d="M12 2l8 4.5v9L12 20l-8-4.5v-9L12 2z"/><path d="M12 11L4.5 6.8M12 11l7.5-4.2M12 11v8.5"/>
          </svg>
        </div>
        <div class="mcard-main">
          <div class="mcard-name" :title="m.id">{{ m.id }}</div>
          <div class="mcard-chips">
            <i>{{ typeLabel(m.type) }}</i>
            <i v-if="residentQuant(m)">{{ residentQuant(m) }}</i>
            <i v-if="residentSize(m)">{{ residentSize(m) }}</i>
            <!-- Checklist tiers (draft .mt i.src-g): trailing green source pill -->
            <i v-if="checklistDetails" class="resident-chip">{{ t('home.residentBadge') }}</i>
          </div>
          <div v-if="unloadErrors[m.id]" class="mcard-error">
            {{ t('dock.unloadFailed', { msg: unloadErrors[m.id] }) }}
          </div>
        </div>
        <!-- Desktop platforms keep the in-place unload (same chain as TaskDock);
             Android renders the status-only "auto switch" pill instead (draft A②) -->
        <button
          v-if="showResidentUnload"
          class="unload-btn"
          :disabled="unloadingId === m.id"
          @click="handleUnload(m.id)"
        >
          {{ unloadingId === m.id ? t('dock.unloading') : t('dock.unload') }}
        </button>
        <span v-else class="resident-auto-pill">{{ t('home.residentAutoSwitch') }}</span>
      </section>
      </div>

      <!-- Column wrappers (the portrait band's two-column pass): the checklist +
           storage + resident cards form the right column, hero + mini pair the
           left column, capability/system cards a full-width band below.
           Everywhere else they dissolve via display:contents into the flat
           single/two-column grid; the base order values in the style block keep
           the flat item order identical to the pre-wrapper layout (checklist
           leads, hero + minis follow, storage/resident/capability cards last). -->
      <div class="home-main-col">
      <!-- Gradient hero card (frame ①): status tag, model name, honest subline
           and the live decode speed with a CTA into the chat page. All values
           come from the live monitor / router / model probes — nothing here is
           hardcoded or invented. -->
      <section class="island hero-card" :class="{ off: !serviceRunning }">
        <span class="hero-tag" :class="{ off: !serviceRunning }">
          <span class="tag-dot"></span>
          {{ serviceRunning ? t('home.hero.tagReady') : t('home.hero.tagOffline') }}
        </span>
        <h2 class="hero-model" :title="heroModel">{{ heroModel }}</h2>
        <p class="hero-sub">{{ heroSub }}</p>
        <div class="hero-row">
          <div class="hero-metric">
            <div class="hero-num">{{ heroTps }}</div>
            <div class="hero-lbl">{{ t('home.hero.metricLbl') }}</div>
          </div>
          <!-- Phone tier (design draft frame ①): the offline hero is a pure status
               statement — the "enter chat" CTA only renders while the service
               is actually ready (frame ②); desktop keeps the CTA in every
               state (existing desktop look untouched). -->
          <router-link v-if="serviceRunning || !platformState.isMobile" class="hero-cta" to="/chat">{{ t('home.hero.cta') }}</router-link>
        </div>
      </section>

      <!-- Mini metric pair (frame ① grid2): memory / CPU with SVG rings fed
           by the live monitor samples, static snapshot as the fallback -->
      <div class="grid2">
        <section class="island mini">
          <div class="mini-lbl">{{ t('home.memory') }}</div>
          <div class="mini-main">
            <div class="mini-val"><span class="mini-num">{{ memoryValUsed }}</span><small> / {{ memoryValTotal }} GB</small></div>
            <svg
              class="ring"
              viewBox="0 0 46 46"
              role="progressbar"
              :aria-valuenow="memoryView.pct"
              aria-valuemin="0"
              aria-valuemax="100"
              :aria-label="t('home.memory')"
            >
              <circle class="bg" cx="23" cy="23" r="19"/>
              <circle class="fg" cx="23" cy="23" r="19" :stroke-dasharray="ringDash(memoryView.pct)" transform="rotate(-90 23 23)"/>
            </svg>
          </div>
          <div class="mini-trend">{{ memoryAux }}</div>
        </section>
        <section class="island mini">
          <div class="mini-lbl">{{ t('home.cpu') }}</div>
          <div class="mini-main">
            <div class="mini-val"><span class="mini-num">{{ cpuVal }}</span><small> %</small></div>
            <svg
              class="ring"
              viewBox="0 0 46 46"
              role="progressbar"
              :aria-valuenow="liveCpuPct ?? 0"
              aria-valuemin="0"
              aria-valuemax="100"
              :aria-label="t('home.cpu.load')"
            >
              <circle class="bg" cx="23" cy="23" r="19"/>
              <circle class="fg" cx="23" cy="23" r="19" :stroke-dasharray="ringDash(liveCpuPct ?? 0)" transform="rotate(-90 23 23)"/>
            </svg>
          </div>
          <div class="mini-trend" :title="info.cpu.model">{{ cpuAux }}</div>
        </section>
      </div>
      </div>

      <div class="home-info-col">
      <!-- GPU Card: only on platforms with a real GPU probe (windows, linux,
           macOS on Apple Silicon). Android probes are unsupported (GPUs always
           empty) and macOS x64 ships the CPU-only release (no GPUs), so the
           card — including its empty state — would be pure noise there.
           Desktop tier (>=1100px) re-homes it into the left column via the
           .gpu-card grid-column override for column balance. -->
      <section v-if="showGpuCard" class="island info-section gpu-card">
        <h2 class="section-title">
          <svg width="18" height="18" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round" stroke-linejoin="round">
            <rect x="2" y="3" width="20" height="14" rx="2" ry="2"/><line x1="8" y1="21" x2="16" y2="21"/><line x1="12" y1="17" x2="12" y2="21"/>
          </svg>
          {{ t('home.gpu') }}
          <span v-if="multiGpu" class="title-chip">×{{ gpuViews.length }}</span>
        </h2>
        <div v-if="gpuViews.length > 0">
          <!-- Aggregate VRAM across NVIDIA GPUs: the number that matters for model offloading
               (non-NVIDIA vendors carry no sampled VRAM and are skipped) -->
          <div v-if="multiGpu && vramTotals" class="usage-block">
            <div class="usage-row">
              <span class="usage-name">{{ t('home.gpu.vramTotal') }}</span>
              <span class="usage-caption">{{ formatMB(vramTotals.usedMb) }} / {{ formatMB(vramTotals.totalMb) }}</span>
              <span class="usage-pct">{{ t('home.usagePercent', { pct: usagePercent(vramTotals.usedMb, vramTotals.totalMb) }) }}</span>
            </div>
            <div
              class="usage-bar"
              role="progressbar"
              :aria-valuenow="usagePercent(vramTotals.usedMb, vramTotals.totalMb)"
              aria-valuemin="0"
              aria-valuemax="100"
              :aria-label="t('home.gpu.vramTotal')"
            >
              <div class="usage-fill" :style="{ width: usagePercent(vramTotals.usedMb, vramTotals.totalMb) + '%' }"></div>
            </div>
          </div>
          <div
            v-for="(gpu, i) in gpuViews"
            :key="i"
            class="gpu-block"
            :class="{ divided: i > 0 }"
          >
            <div class="usage-row">
              <span class="usage-name gpu-name" :title="gpu.name">{{ gpu.name }}</span>
              <!-- Apple Silicon: unified memory badge instead of a VRAM bar -->
              <span v-if="gpu.vendor === 'apple'" class="title-chip">{{ t('home.gpu.metal') }}</span>
              <template v-if="gpuHasVram(gpu)">
                <span class="usage-caption">{{ formatMB(gpu.usedMb) }} / {{ formatMB(gpu.totalMb) }}</span>
                <span class="usage-pct">{{ t('home.usagePercent', { pct: usagePercent(gpu.usedMb, gpu.totalMb) }) }}</span>
              </template>
            </div>
            <div
              v-if="gpuHasVram(gpu)"
              class="usage-bar"
              role="progressbar"
              :aria-valuenow="usagePercent(gpu.usedMb, gpu.totalMb)"
              aria-valuemin="0"
              aria-valuemax="100"
              :aria-label="gpu.name"
            >
              <div class="usage-fill" :style="{ width: usagePercent(gpu.usedMb, gpu.totalMb) + '%' }"></div>
            </div>
            <div class="meta-row">
              <span v-if="gpuHasVram(gpu)">{{ t('home.gpu.memory') }} {{ formatMB(gpu.totalMb) }}</span>
              <span v-if="gpuHasVram(gpu) && gpu.utilPercent !== null">{{ t('home.gpu.util') }} {{ t('home.usagePercent', { pct: gpu.utilPercent }) }}</span>
              <!-- Compute capability is a CUDA concept: NVIDIA-only rows -->
              <span v-if="gpu.vendor === 'nvidia'">{{ t('home.gpu.computeCap') }} {{ gpu.computeCapability > 0 ? gpu.computeCapability.toFixed(1) : 'N/A' }}</span>
              <span>{{ t('home.gpu.driver') }} {{ gpu.driverVersion || 'N/A' }}</span>
            </div>
          </div>
        </div>
        <div v-else class="info-empty">
          <span>{{ t('home.gpu.none') }}</span>
        </div>
      </section>

      <!-- CUDA Card: Windows only AND only with an NVIDIA GPU reporting a
           compute capability — Linux llama.cpp is Vulkan-only (CUDA compat is
           meaningless there), macOS is Metal, Android is CPU-only. -->
      <section v-if="showCudaCard" class="island info-section">
        <h2 class="section-title">
          <svg width="18" height="18" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round" stroke-linejoin="round">
            <polygon points="12 2 22 8.5 22 15.5 12 22 2 15.5 2 8.5 12 2"/><line x1="12" y1="22" x2="12" y2="15.5"/><polyline points="22 8.5 12 15.5 2 8.5"/>
          </svg>
          {{ t('home.cuda') }}
        </h2>
        <div class="info-grid">
          <div class="info-item">
            <span class="info-label">{{ t('home.cuda.status') }}</span>
            <span class="info-value">
              <span class="status-badge" :class="info.cuda.available ? 'available' : 'unavailable'">
                {{ info.cuda.available ? t('home.cuda.available') : t('home.cuda.unavailable') }}
              </span>
            </span>
          </div>
          <div class="info-item">
            <span class="info-label">{{ t('home.cuda.driver') }}</span>
            <span class="info-value">{{ info.cuda.driverVersion || 'N/A' }}</span>
          </div>
          <div class="info-item">
            <span class="info-label">{{ t('home.cuda.toolkit') }}</span>
            <span class="info-value">{{ info.cuda.toolkitVersion || (info.cuda.available ? t('home.cuda.na') : t('home.cuda.notInstalled')) }}</span>
          </div>
          <div class="info-item" v-if="info.cuda.available && firstGpuComputeCap > 0">
            <span class="info-label">{{ t('home.cuda.compat') }}</span>
            <!-- warning-text only for 'need': a satisfied floor is good news, not a warning -->
            <span class="info-value" :class="{ 'warning-text': cudaLevel === 'need' }">
              {{ cudaCompatText }}
            </span>
          </div>
        </div>
      </section>

      <!-- System Card (sys-card hooks the portrait band's pairing row with
           the resident-model card — both are compact summary strips) -->
      <section class="island info-section sys-card">
        <h2 class="section-title">
          <svg width="18" height="18" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round" stroke-linejoin="round">
            <rect x="3" y="4" width="18" height="12" rx="2"/><line x1="2" y1="20" x2="22" y2="20"/>
          </svg>
          {{ t('home.os') }}
        </h2>
        <div class="info-grid">
          <div class="info-item">
            <span class="info-label">{{ t('home.os.name') }}</span>
            <span class="info-value">{{ osLabel }}</span>
          </div>
          <div class="info-item">
            <span class="info-label">{{ t('home.os.arch') }}</span>
            <span class="info-value">{{ info.arch || 'N/A' }}</span>
          </div>
          <div class="info-item">
            <span class="info-label">{{ t('home.appVersion') }}</span>
            <span class="info-value">{{ info.appVersion || 'N/A' }}</span>
          </div>
        </div>
      </section>
      </div>
    </div>
  </div>
</template>

<script setup lang="ts">
import { ref, reactive, computed, watch, onMounted, onUnmounted } from 'vue'
import { useRouter } from 'vue-router'
import { getCPU, getMemory, getGPU, getCUDA, getOS, getDisk, getLlamaCpp, getModels, getMonitorStatus, getAppVersion, getLoadedModels, unloadModel, stopServer, type LoadedModel } from '../wails'
import { t } from '../lib/i18n'
import { usagePercent, formatMB, formatBytes } from '../lib/format'
import { aggregateVram, buildGpuDisplays, cudaCompatLevel, gpuHasVram, type GpuStaticInfo } from '../lib/sysinfo'
import { guessQuant } from '../lib/modelFiles'
import { buildOnboardingView, heroOnboardSubKey, type OnboardingStepId } from '../lib/onboarding'
import { pairsResidentWithSystemCard } from '../lib/homeLayout'
import { nudgeDock } from '../lib/dockNudge'
import { homeResidentShowsUnload, residentReleaseMode } from '../lib/dock'
import type { MonitorStatus } from '../lib/monitor'
import { accelBuildKey, showCudaCompat, showGpuCards, usePlatform } from '../lib/platform'
import { appConfig, setOnboardingDismissed } from '../store'

interface SystemInfo {
  os: string
  arch: string
  cpu: { model: string; cores: number; logicalCpus: number }
  memory: { totalGb: number; freeGb: number }
  gpu: GpuStaticInfo[]
  cuda: { available: boolean; driverVersion: string; toolkitVersion: string }
  disk: { path: string; used: number; total: number } | null
  appVersion: string
}

/** Live metrics sampled periodically via GetMonitorStatus (byte units); falls back to the static snapshot. */
interface LiveSnapshot {
  cpuPercent: number | null
  memUsedBytes: number
  memTotalBytes: number
  gpus: MonitorStatus['gpus']
  disk: { path: string; used: number; total: number } | null
  serverRunning: boolean
  /** Live generation decode speed (tokens/s); 0 when idle — the hero card degrades to a placeholder. */
  decodeTps: number
}

/** Local model entry as returned by GetModels (fields used by this tab). */
interface ModelInfo {
  name: string
  sizeBytes: number
  sizeHuman: string
  quantization: string
}

// Quick-start checklist facts probed alongside the system info. The llama.cpp
// probe also feeds the storage brick (version + acceleration build) and the
// CUDA compat row via cudartVersion — it must stay unconditional (not gated
// behind onboarding visibility).
const runtimeInstalled = ref(false)
const runtimeVersion = ref('')
const runtimeAccel = ref('')
const cudartVersion = ref('')
// Full local model list: feeds the checklist fact, the storage brick and the
// resident-card quant/size chips
const localModels = ref<ModelInfo[]>([])

const router = useRouter()

const ONBOARDING_LABELS: Record<OnboardingStepId, string> = {
  runtime: 'onboarding.step.runtime',
  models: 'onboarding.step.models',
  service: 'onboarding.step.service'
}

// Phone-tier muted sub-descriptions (design draft .cstep .sd), rendered on the
// checklist-shaped tiers (phone + tablet, both draft tracks) where the mockup
// checklist carries them
const ONBOARDING_SUBS: Record<OnboardingStepId, string> = {
  runtime: 'onboarding.sub.runtime',
  models: 'onboarding.sub.models',
  service: 'onboarding.sub.service'
}

// Per-step go-link labels (design draft .cstep .go); the desktop button
// keeps the single generic label
const ONBOARDING_GOTOS: Record<OnboardingStepId, string> = {
  runtime: 'onboarding.goto.runtime',
  models: 'onboarding.goto.models',
  service: 'onboarding.goto.service'
}

// Checklist-mockup tiers: phone + tablet (portrait AND landscape) render the
// per-step sub-lines and go-link labels of the draft .check card; desktop keeps
// its own compact button style
const checklistDetails = computed(() => platformState.value.isMobile || platformState.value.isTablet)

/** Go-link label for one checklist step: per-step copy on phone/tablet, generic on desktop. */
function stepGoLabel(id: OnboardingStepId): string {
  return checklistDetails.value ? t(ONBOARDING_GOTOS[id]) : t('onboarding.goto')
}

const info = ref<SystemInfo>({
  os: '', arch: '',
  cpu: { model: '', cores: 0, logicalCpus: 0 },
  memory: { totalGb: 0, freeGb: 0 },
  gpu: [],
  cuda: { available: false, driverVersion: '', toolkitVersion: '' },
  disk: null,
  appVersion: ''
})
const live = ref<LiveSnapshot | null>(null)
const loading = ref(true)
const refreshing = ref(false)
const loadError = ref('')
const lastUpdated = ref('')

// Router models currently in memory (resident-model cards + hero identity)
const loadedModels = ref<LoadedModel[]>([])
// Unload state, mirroring TaskDock's per-row pending/error handling
const unloadingId = ref('')
const unloadErrors = reactive<Record<string, string>>({})

let pollTimer: ReturnType<typeof setInterval> | null = null

// ─── Derived views ───────────────────────────────────────────────

const gpuViews = computed(() => buildGpuDisplays(info.value.gpu, live.value?.gpus ?? null))
const multiGpu = computed(() => gpuViews.value.length > 1)
const vramTotals = computed(() => aggregateVram(gpuViews.value))

// Hardware-capability gates (OS/arch-scoped, see lib/platform.ts): the GPU
// card renders where a probe exists (windows, linux, macOS on Apple Silicon);
// the CUDA compat card only on Windows with an NVIDIA GPU reporting a compute
// capability.
const platformState = usePlatform()
const showGpuCard = computed(() => showGpuCards(platformState.value))
const showCudaCard = computed(() => showCudaCompat(platformState.value, info.value.gpu))

const memoryView = computed(() => {
  const l = live.value
  if (l && l.memTotalBytes > 0) {
    const gb = 1024 ** 3
    const usedGb = l.memUsedBytes / gb
    const totalGb = l.memTotalBytes / gb
    return {
      usedGb,
      totalGb,
      freeGb: Math.max(0, totalGb - usedGb),
      pct: usagePercent(l.memUsedBytes, l.memTotalBytes)
    }
  }
  const total = info.value.memory.totalGb
  const free = info.value.memory.freeGb
  return {
    usedGb: Math.max(0, total - free),
    totalGb: total,
    freeGb: free,
    pct: usagePercent(total - free, total)
  }
})

const diskView = computed(() => live.value?.disk ?? info.value.disk)
const diskPct = computed(() => {
  const d = diskView.value
  return d ? usagePercent(d.used, d.total) : 0
})

const liveCpuPct = computed(() => {
  const p = live.value?.cpuPercent
  return p === null || p === undefined ? null : Math.round(p * 10) / 10
})

const osLabel = computed(() => {
  const labels: Record<string, string> = {
    windows: 'Windows',
    linux: 'Linux',
    darwin: 'macOS',
    android: 'Android'
  }
  return labels[info.value.os] || info.value.os || 'N/A'
})

const firstGpuComputeCap = computed(() => info.value.gpu[0]?.computeCapability ?? 0)
const cudaLevel = computed(() => cudaCompatLevel(firstGpuComputeCap.value, cudartVersion.value))

// Three-state CUDA compat row text: 'satisfied' composes the detected CUDA
// major family; the version itself stays untranslated so only the label goes
// through i18n
const cudaCompatText = computed(() => {
  switch (cudaLevel.value) {
    case 'satisfied':
      return `${t('home.cuda.compatSatisfied')} · CUDA ${cudartVersion.value}`
    case 'need':
      return t('home.cuda.compatBlackwell')
    default:
      return t('home.cuda.compatOk')
  }
})

// ─── Hero card (frame ①) ─────────────────────────────────────────

const serviceRunning = computed(() => live.value?.serverRunning === true)

/** First resident model: prefer one actually loaded, else any loading/sleeping entry. */
const residentModel = computed<LoadedModel | null>(
  () => loadedModels.value.find(m => m.status === 'loaded') ?? loadedModels.value[0] ?? null
)

const heroModel = computed(() => {
  if (residentModel.value) return residentModel.value.id
  if (serviceRunning.value) return t('home.hero.standby')
  // Offline: show what the first chat would load (a real local model), or an
  // honest empty state — never a fake "online" claim
  return localModels.value[0]?.name || t('home.noModel')
})

const heroSub = computed(() => {
  if (residentModel.value) return t('home.hero.subResident')
  if (serviceRunning.value) return t('home.hero.subIdle')
  // Checklist tiers (design draft frames A①/B①): while the quick-start checklist
  // is on screen the offline subline points at it — "steps above" on phone /
  // tablet portrait, "steps on the right" on tablet landscape (lib/onboarding)
  const subKey = heroOnboardSubKey(platformState.value)
  if (subKey && onboardingView.value.visible) return t(subKey)
  return t('home.hero.subOffline')
})

// Live decode speed: only claimed while a measurement exists (> 0); the
// placeholder "—" covers idle / stopped / no-data without inventing a number
const heroTps = computed(() => {
  const tps = live.value?.decodeTps
  return tps && tps > 0 ? tps.toFixed(1) : '—'
})

// ─── Mini cards (frame ①) ────────────────────────────────────────

const RING_RADIUS = 19
const RING_CIRCUMFERENCE = 2 * Math.PI * RING_RADIUS

/** SVG stroke-dasharray for a 0..100 percent ring value (clamped). */
function ringDash(pct: number): string {
  const clamped = Math.max(0, Math.min(100, pct))
  return `${((clamped / 100) * RING_CIRCUMFERENCE).toFixed(1)} ${RING_CIRCUMFERENCE.toFixed(1)}`
}

/** One-decimal GB for the mini values; '—' when the probe has no data. */
function gbNum(v: number | undefined | null): string {
  return v !== undefined && v !== null && v > 0 ? v.toFixed(1) : '—'
}

const memoryValUsed = computed(() => gbNum(memoryView.value.usedGb))
const memoryValTotal = computed(() => gbNum(memoryView.value.totalGb))
const memoryAux = computed(() =>
  t('home.memory.aux', { pct: memoryView.value.pct, free: `${gbNum(memoryView.value.freeGb)} GB` })
)

const cpuVal = computed(() => (liveCpuPct.value === null ? '—' : String(liveCpuPct.value)))
const cpuAux = computed(() =>
  t('home.cpu.aux', { model: info.value.cpu.model || '—', n: info.value.cpu.cores })
)

// ─── Storage island (frame ①) ────────────────────────────────────

const storageFree = computed(() => {
  const d = diskView.value
  if (!d) return ''
  const free = d.total - d.used
  return t('home.storage.free', { size: free > 0 ? formatBytes(free) : '0 B' })
})

const ggufBrick = computed(() => {
  const n = localModels.value.length
  if (n === 0) return t('home.noModel')
  const total = localModels.value.reduce((s, m) => s + (m.sizeBytes || 0), 0)
  return t('home.storage.modelsValue', { n, size: formatBytes(total) || '—' })
})

// Acceleration build label: same resolution as RuntimeSection — prefer the
// backend's detected accel, fall back to the platform's llama.cpp build guess
// (accelBuildKey), with Android keeping the arm64 CPU qualifier
const accelKey = computed(() => {
  const detected = runtimeAccel.value
  if (detected === 'cuda' || detected === 'vulkan' || detected === 'metal' || detected === 'cpu') {
    return detected
  }
  const fallback = accelBuildKey(platformState.value)
  if (fallback === 'cpu' && platformState.value.isAndroid) {
    return 'cpuArm64'
  }
  return fallback
})

const llamacppBrick = computed(() => {
  if (!runtimeInstalled.value) return t('home.storage.notInstalled')
  const version = runtimeVersion.value
  const accel = t('runtime.accel.' + accelKey.value)
  return version ? `${version} · ${accel}` : accel
})

// ─── Resident model cards (frame ①) ──────────────────────────────

// Portrait-band card pairing: the resident-model card and the system summary
// card share one two-column row below the storage island when exactly one
// model is resident (predicate in lib/homeLayout); zero residents keeps the
// first-use single-column stack unchanged.
const residentPairing = computed(() => pairsResidentWithSystemCard(loadedModels.value.length))

/** Match a loaded router id back to the local model list (chat loads by model name). */
function matchedLocal(id: string): ModelInfo | undefined {
  return (
    localModels.value.find(m => m.name === id) ??
    localModels.value.find(m => id.includes(m.name))
  )
}

/** Quantization chip: the local model's classification, else a filename guess. */
function residentQuant(m: LoadedModel): string {
  const local = matchedLocal(m.id)
  if (local && local.quantization && local.quantization !== '-') return local.quantization
  return guessQuant(m.id)
}

/** Size chip from the matched local model; '' hides the chip. */
function residentSize(m: LoadedModel): string {
  return matchedLocal(m.id)?.sizeHuman || ''
}

function typeLabel(type: string): string {
  const map: Record<string, string> = {
    chat: t('dock.modelType.chat'),
    audio: t('dock.modelType.audio'),
    image: t('dock.modelType.image'),
    video: t('dock.modelType.video')
  }
  return map[type] || type
}

// Android renders the resident-model card as STATUS ONLY (design-draft platform
// crop, frame A②): the unload action is cropped and an "auto switch" pill
// takes its place (lib/dock.homeResidentShowsUnload)
const showResidentUnload = computed(() => homeResidentShowsUnload(platformState.value.isAndroid))

// Unload chain identical to TaskDock: drop the row instantly, then nudge a
// poll that reconciles. Platform split via lib/dock residentReleaseMode:
// desktop asks the router to evict the model; direct-mode Android stops the
// service instead (the single resident leaves memory with the process; the
// next chat send auto-restarts it), and the poll flips serverRunning so the
// card disappears.
async function handleUnload(id: string) {
  unloadingId.value = id
  delete unloadErrors[id]
  try {
    if (residentReleaseMode(platformState.value.isAndroid) === 'stop-server') {
      await stopServer()
    } else {
      await unloadModel(id)
      loadedModels.value = loadedModels.value.filter(m => m.id !== id)
    }
    nudgeDock()
  } catch (e) {
    unloadErrors[id] = e instanceof Error ? e.message : String(e)
  } finally {
    unloadingId.value = ''
  }
}

// Quick-start checklist: derives visibility/steps from probed facts + persisted dismissal
const onboardingView = computed(() => buildOnboardingView({
  runtimeInstalled: runtimeInstalled.value,
  hasModels: localModels.value.length > 0,
  serviceRunning: live.value?.serverRunning === true,
  dismissed: appConfig.onboardingDismissed
}))

// Auto-complete: once every step is satisfied, persist the dismissal so the
// card never reappears after the user later stops the service
watch(() => onboardingView.value.allDone, (allDone) => {
  if (allDone && !appConfig.onboardingDismissed) {
    setOnboardingDismissed(true)
  }
})

function dismissOnboarding() {
  setOnboardingDismissed(true)
}

function goStep(path: string) {
  router.push(path)
}

// ─── Data loading ────────────────────────────────────────────────

function stampUpdated() {
  lastUpdated.value = new Date().toLocaleTimeString([], { hour12: false })
}

/**
 * Full probe of the static system info (hardware identity, versions).
 * Probes run in parallel and settle independently; partial failures render
 * as N/A values while an all-fail result switches to the error card.
 */
async function fetchSystemInfo(manual = false) {
  if (manual) {
    refreshing.value = true
  } else {
    loading.value = true
  }
  loadError.value = ''

  // Best-effort, not part of the all-failed detection (cosmetic field)
  getAppVersion()
    .then(v => { info.value.appVersion = v })
    .catch(() => {})
  // Quick-start / storage-brick facts: best-effort probes (cosmetic as well)
  getLlamaCpp()
    .then(d => {
      runtimeInstalled.value = d?.installed === true
      runtimeVersion.value = typeof d?.version === 'string' ? d.version : ''
      runtimeAccel.value = typeof d?.accel === 'string' ? d.accel : ''
      cudartVersion.value = typeof d?.cudartVersion === 'string' ? d.cudartVersion : ''
    })
    .catch(() => {})
  getModels()
    .then(list => { localModels.value = Array.isArray(list) ? (list as ModelInfo[]) : [] })
    .catch(() => {})

  // Each probe applies its result with an explicit narrowing cast: the Wails
  // bridge is untyped (Promise<any>), so the target shape comes from SystemInfo.
  const probes: [Promise<unknown>, (data: unknown) => void][] = [
    [getCPU(), d => { info.value.cpu = d as SystemInfo['cpu'] }],
    [getMemory(), d => { info.value.memory = d as SystemInfo['memory'] }],
    [getGPU(), d => { info.value.gpu = (d as GpuStaticInfo[] | null) || [] }],
    [getCUDA(), d => { info.value.cuda = d as SystemInfo['cuda'] }],
    [getOS(), d => { const o = d as { os: string; arch: string }; info.value.os = o.os; info.value.arch = o.arch }],
    [getDisk(), d => { info.value.disk = d as SystemInfo['disk'] }]
  ]

  const settled = await Promise.allSettled(probes.map(([p]) => p))
  probes.forEach(([, setter], i) => {
    const r = settled[i]
    if (r.status === 'fulfilled') setter(r.value)
  })

  const firstFailure = settled.find((r): r is PromiseRejectedResult => r.status === 'rejected')
  if (firstFailure && settled.every(r => r.status === 'rejected')) {
    const reason = firstFailure.reason
    loadError.value = t('home.backendError', { msg: reason instanceof Error ? reason.message : String(reason ?? '') })
  }

  loading.value = false
  refreshing.value = false
  stampUpdated()
}

/** Poll live metrics (RAM / VRAM / utilization / disk / decode speed) and the resident models while the page is mounted. */
async function pollLive() {
  try {
    const s = await getMonitorStatus()
    live.value = {
      cpuPercent: Number.isFinite(s.cpuPercent) ? s.cpuPercent : null,
      memUsedBytes: s.memUsed,
      memTotalBytes: s.memTotal,
      gpus: Array.isArray(s.gpus) ? s.gpus : [],
      disk: s.disk ?? null,
      serverRunning: s.serverRunning === true,
      decodeTps: Number.isFinite(s.decodeTps) ? s.decodeTps : 0
    }
    if (s.serverRunning) {
      try {
        loadedModels.value = await getLoadedModels()
      } catch {
        // Transient router query failure: keep the previous list until next tick
      }
    } else {
      loadedModels.value = []
    }
    stampUpdated()
  } catch {
    // Transient sampling failure: keep displaying the last good sample
  }
}

// Expose the manual refresh to the Home shell: the toolbar beside the tab bar
// delegates the re-probe here and mirrors refreshing/lastUpdated (exposed refs
// unwrap, so they read as boolean/string through the shell's ref)
defineExpose({ refresh: () => fetchSystemInfo(true), refreshing, lastUpdated })

onMounted(() => {
  fetchSystemInfo()
  pollLive()
  pollTimer = setInterval(pollLive, 3000)
})

onUnmounted(() => {
  if (pollTimer) {
    clearInterval(pollTimer)
    pollTimer = null
  }
})
</script>

<style scoped>
/* Tab panel root: no page chrome (the Home shell owns the greeting, tabs and
   the scroll band); min-width: 0 lets long unbreakable strings shrink instead
   of blowing the panel past the viewport */
.system-info-tab {
  min-width: 0;
}

/* ─── Cards grid: single floating-island column on phone/tablet; asymmetric
       7fr/5fr two-column split on desktop (>=1100px, design frames ⑤⑥) where
       the memory/CPU mini pair stays a side-by-side sub-grid cell. Never
       three columns. ─── */
.sys-grid {
  display: grid;
  grid-template-columns: minmax(0, 1fr);
  gap: 12px;
  align-content: start;
}

/* Column wrappers (the portrait band's two-column pass): they only become
   real boxes there; every other tier dissolves them into the flat grid so the
   item order and spacing stay exactly as before the wrappers existed */
.home-main-col,
.home-side-col,
.home-info-col {
  display: contents;
}

.sys-grid > .island,
.home-main-col > .island,
.home-side-col > .island,
.home-info-col > .island {
  padding: 20px;
}

/* Desktop tier: >=1100px (design draft frames ⑤⑥) — asymmetric 7fr/5fr
   two-column split. Hero + the memory/CPU mini pair own the left column; the
   quick-start checklist + storage island pack the right column, the
   resident-model card continues in the left column right after the minis,
   the GPU card follows it (balance fix: with the GPU card on the right, that
   column outgrew the left by ~240px on real machines), and the CUDA/system
   cards stack in the right column. */
@media (min-width: 1100px) {
  .sys-grid {
    grid-template-columns: minmax(0, 7fr) minmax(0, 5fr);
    gap: 16px;
    align-items: start;
  }

  /* Hero, memory/CPU mini cards -> left column */
  .home-main-col > * {
    grid-column: 1;
  }

  /* The mini pair keeps its side-by-side 1fr 1fr sub-grid as ONE left-column
     cell: it must NOT dissolve into independent auto-placed grid cells
     (display:contents), which let auto-placement drop the memory mini into
     the right column next to the CPU mini. Column ownership is already
     covered by the .home-main-col > * rule above; restated here to anchor
     the no-dissolve decision. */
  .home-main-col > .grid2 {
    grid-column: 1;
  }

  /* Quick-start checklist + storage island -> right column */
  .home-side-col > *:not(.mcard) {
    grid-column: 2;
  }

  /* Resident-model card -> left column, right after the minis */
  .home-side-col > .mcard {
    grid-column: 1;
  }

  /* CUDA / system cards -> right column */
  .home-info-col > * {
    grid-column: 2;
  }

  /* GPU card -> left column, after the resident-model card: balances the two
     columns (the checklist + storage side outgrew the left once the GPU card
     sat here). Specificity (0,2,0) beats the (0,1,0) rule above, and the
     card's source position after the minis keeps the auto-placed row order
     hero -> minis -> resident -> GPU in the left column. */
  .home-info-col > .gpu-card {
    grid-column: 1;
  }
}

/* Tablet centered column: 768–1099px (design draft F7) */
@media (min-width: 768px) and (max-width: 1099px) {
  .sys-grid {
    max-width: 800px;
    margin-left: auto;
    margin-right: auto;
  }
}

/* ─── Gradient hero card (frame ① .hero): brand gradient + white text, the
       decorative corner circles kept as pseudo-elements ─── */
.hero-card {
  position: relative;
  overflow: hidden;
  background: var(--grad);
  border: none;
  color: #fff;
  box-shadow: none;
}

.hero-card::after {
  content: "";
  position: absolute;
  width: 190px;
  height: 190px;
  border-radius: 50%;
  background: rgba(255, 255, 255, 0.14);
  top: -70px;
  right: -50px;
  pointer-events: none;
}

.hero-card::before {
  content: "";
  position: absolute;
  width: 120px;
  height: 120px;
  border-radius: 50%;
  background: rgba(255, 255, 255, 0.1);
  bottom: -50px;
  left: -30px;
  pointer-events: none;
}

.hero-tag {
  position: relative;
  z-index: 1;
  display: inline-flex;
  align-items: center;
  gap: 6px;
  font-size: 11.5px;
  font-weight: 700;
  background: rgba(255, 255, 255, 0.2);
  border-radius: 999px;
  padding: 5px 12px;
  letter-spacing: 0.4px;
}

.tag-dot {
  width: 7px;
  height: 7px;
  border-radius: 50%;
  background: #6ee7b7;
  box-shadow: none;
}

/* Offline: the glow dies, the dot goes neutral — status stays honest */
.hero-tag.off .tag-dot {
  background: rgba(255, 255, 255, 0.55);
  box-shadow: none;
}

.hero-model {
  position: relative;
  z-index: 1;
  font-size: 23px;
  font-weight: 800;
  margin: 13px 0 0;
  letter-spacing: -0.3px;
  line-height: 1.25;
  overflow: hidden;
  text-overflow: ellipsis;
  white-space: nowrap;
}

.hero-sub {
  position: relative;
  z-index: 1;
  font-size: 12.5px;
  opacity: 0.85;
  margin: 5px 0 0;
}

.hero-row {
  position: relative;
  z-index: 1;
  display: flex;
  align-items: flex-end;
  justify-content: space-between;
  gap: 12px;
  flex-wrap: wrap;
  margin-top: 16px;
}

.hero-num {
  font-size: 30px;
  font-weight: 800;
  letter-spacing: -0.8px;
  line-height: 1;
  font-variant-numeric: tabular-nums;
}

.hero-lbl {
  font-size: 11px;
  opacity: 0.8;
  margin-top: 4px;
}

.hero-cta {
  background: rgba(255, 255, 255, 0.94);
  color: #6d28d9;
  font-size: 13px;
  font-weight: 800;
  border-radius: 999px;
  padding: 11px 20px;
  box-shadow: none;
  text-decoration: none;
  transition: transform 0.15s ease, box-shadow 0.15s ease;
  flex-shrink: 0;
}

.hero-cta:hover {
  color: #5b21b6;
  transform: translateY(-1px);
  box-shadow: none;
}

/* ─── Mini metric pair (frame ① .grid2 + .mini) ─── */
.grid2 {
  display: grid;
  grid-template-columns: 1fr 1fr;
  gap: 12px;
}

.mini {
  display: flex;
  flex-direction: column;
  padding: 16px;
}

.mini-lbl {
  font-size: 12px;
  color: var(--text-dim);
  font-weight: 600;
}

.mini-main {
  display: flex;
  align-items: center;
  justify-content: space-between;
  gap: 8px;
  margin-top: 6px;
  min-width: 0;
}

.mini-val {
  font-size: 21px;
  font-weight: 800;
  letter-spacing: -0.4px;
  color: var(--text-primary);
  font-variant-numeric: tabular-nums;
  overflow: hidden;
  text-overflow: ellipsis;
  white-space: nowrap;
}

.mini-val small {
  font-size: 12px;
  color: var(--text-dim);
  font-weight: 600;
}

.mini-trend {
  margin-top: 10px;
  font-size: 11px;
  color: var(--text-dim);
  font-weight: 600;
  overflow: hidden;
  text-overflow: ellipsis;
  white-space: nowrap;
}

/* Ring: design frame ① .ring — 46px box, r=19, gradient fg rotated -90° */
.ring {
  width: 46px;
  height: 46px;
  flex-shrink: 0;
}

.ring circle {
  fill: none;
  stroke-width: 6;
  stroke-linecap: round;
}

.ring .bg {
  stroke: var(--overlay-8);
}

.ring .fg {
  stroke: url(#home-ring-grad);
  transition: stroke-dasharray 0.6s ease;
}

/* ─── Storage island (frame ① .island + .bar.a) ─── */
.island-head {
  display: flex;
  align-items: baseline;
  justify-content: space-between;
  gap: 12px;
  margin-bottom: 12px;
}

.island-head h4 {
  font-size: 13px;
  font-weight: 700;
  color: var(--text-secondary);
  letter-spacing: 0.3px;
  margin: 0;
}

.head-more {
  color: var(--accent-light);
  font-size: 12px;
  font-weight: 700;
  font-variant-numeric: tabular-nums;
  white-space: nowrap;
}

.storage-row {
  display: flex;
  justify-content: space-between;
  align-items: baseline;
  gap: 12px;
  font-size: 12px;
  color: var(--text-dim);
  margin-bottom: 8px;
  font-variant-numeric: tabular-nums;
}

.storage-row b {
  font-weight: 800;
  color: var(--text-primary);
}

/* Amber disk-level fill per the design language (frame ⑥: amber = disk level) */
.storage-bar {
  height: 7px;
  border-radius: 999px;
  background: var(--overlay-8);
  overflow: hidden;
}

.storage-bar i {
  display: block;
  height: 100%;
  border-radius: 999px;
  background: linear-gradient(90deg, #fbbf24, #f59e0b);
  transition: width 0.6s ease;
}

.brick-row {
  display: flex;
  gap: 8px;
  margin-top: 14px;
}

.brick {
  flex: 1;
  min-width: 0;
  background: var(--overlay-8);
  border-radius: var(--r-sm);
  padding: 10px 12px;
  display: flex;
  flex-direction: column;
  gap: 2px;
}

.brick-lbl {
  font-size: 11px;
  color: var(--text-dim);
  font-weight: 600;
}

.brick-val {
  font-size: 14px;
  font-weight: 800;
  color: var(--text-primary);
  overflow: hidden;
  text-overflow: ellipsis;
  white-space: nowrap;
  font-variant-numeric: tabular-nums;
}

/* ─── Resident model card (frame ① .mcard) ─── */
.mcard {
  display: flex;
  align-items: center;
  gap: 14px;
  padding: 15px 16px;
}

.tile {
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

.mcard-main {
  min-width: 0;
  flex: 1;
}

.mcard-name {
  font-size: 14px;
  font-weight: 700;
  line-height: 1.35;
  color: var(--text-primary);
  overflow: hidden;
  text-overflow: ellipsis;
  white-space: nowrap;
}

.mcard-chips {
  display: flex;
  gap: 6px;
  align-items: center;
  margin-top: 4px;
  flex-wrap: wrap;
}

.mcard-chips i {
  font-style: normal;
  background: var(--overlay-8);
  border-radius: 6px;
  padding: 2px 7px;
  font-weight: 600;
  color: var(--text-secondary);
  font-size: 11.5px;
}

.mcard-error {
  margin-top: 6px;
  font-size: 11px;
  color: var(--danger);
}

.unload-btn {
  margin-left: auto;
  flex-shrink: 0;
  padding: 9px 14px;
  border-radius: 999px;
  border: 1px solid rgba(239, 68, 68, 0.25);
  background: rgba(239, 68, 68, 0.08);
  color: var(--danger);
  font-size: 12px;
  font-weight: 700;
  font-family: inherit;
  cursor: pointer;
  transition: background 0.15s, border-color 0.15s;
}

.unload-btn:hover:not(:disabled) {
  background: rgba(239, 68, 68, 0.16);
  border-color: rgba(239, 68, 68, 0.4);
}

/* Touch press feedback (OS-scoped): mirrors the hover wash on touch. */
html[data-os='android'] .unload-btn:active:not(:disabled),
html[data-os='ios'] .unload-btn:active:not(:disabled) {
  background: rgba(239, 68, 68, 0.16);
  border-color: rgba(239, 68, 68, 0.4);
}

.unload-btn:disabled {
  opacity: 0.6;
  cursor: wait;
}

/* ─── Android resident card: status-only actions (draft frames A②/B②) ─── */
/* Neutral "auto switch" pill taking the unload button's slot (draft A② .act pill) */
.resident-auto-pill {
  margin-left: auto;
  flex-shrink: 0;
  padding: 6px 13px;
  border-radius: 999px;
  background: var(--overlay-8);
  color: var(--text-secondary);
  font-size: 11.5px;
  font-weight: 700;
  white-space: nowrap;
}

/* ─── Retained capability cards (GPU / CUDA / system): same island surface,
       existing inner content untouched ─── */
.info-section {
  display: flex;
  flex-direction: column;
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

.title-chip {
  padding: 0 8px;
  border-radius: 10px;
  background: var(--active-bg);
  color: var(--accent-light);
  font-size: 11px;
  font-weight: 600;
  line-height: 18px;
}

/* ─── Quick-start checklist ─── */
.onboarding-card {
  grid-column: 1 / -1;
  /* Leads the flat grid whenever the wrappers are display:contents (phone /
     tablet-portrait / desktop), and packs first into the right column of the
     portrait band's two-column pass */
  order: -3;
}

/* Flat-grid ordering of the wrapper-dissolved cards: the checklist leads
   (order: -3 above), then hero + minis, then the storage / resident /
   capability cards in their DOM order. Only matters while the wrappers are
   display:contents; inside the portrait band's flex columns each column
   packs its own children (with the overrides set there). .grid2 carries the
   order while it is a real box (phone / tablet tiers); the .mini order kicks
   in on desktop where grid2 dissolves via display:contents and the minis
   become outer-grid items themselves. */
.home-main-col > .hero-card {
  order: -2;
}

.home-main-col > .grid2 {
  order: -1;
}

.mini {
  order: -1;
}

.onboarding-head {
  display: flex;
  align-items: center;
  justify-content: space-between;
  gap: 16px;
}

.onboarding-head .section-title {
  margin-bottom: 14px;
}

.onboarding-dismiss {
  border: none;
  background: transparent;
  color: var(--text-dim);
  font-size: 12px;
  cursor: pointer;
  padding: 4px 10px;
  border-radius: var(--radius-sm);
  transition: color 0.15s, background 0.15s;
  flex-shrink: 0;
}

.onboarding-dismiss:hover {
  color: var(--text-secondary);
  background: var(--hover-bg);
}

.onboarding-steps {
  list-style: none;
  margin: 0;
  padding: 0;
  display: flex;
  flex-direction: column;
  gap: 10px;
}

.onboarding-step {
  display: flex;
  align-items: center;
  gap: 12px;
  font-size: 13px;
  color: var(--text-secondary);
}

.step-marker {
  width: 22px;
  height: 22px;
  border-radius: 50%;
  border: 1px solid var(--border);
  display: flex;
  align-items: center;
  justify-content: center;
  font-size: 11px;
  font-weight: 600;
  color: var(--text-dim);
  flex-shrink: 0;
}

.onboarding-step.done .step-marker {
  background: rgba(34, 197, 94, 0.12);
  border-color: transparent;
  color: var(--success);
}

.onboarding-step.done .step-label {
  color: var(--text-dim);
}

.step-action {
  margin-left: auto;
  display: inline-flex;
  align-items: center;
  gap: 6px;
  padding: 4px 12px;
  background: var(--active-bg);
  color: var(--accent-light);
  border: none;
  border-radius: var(--radius-sm);
  font-size: 12px;
  font-weight: 500;
  cursor: pointer;
  flex-shrink: 0;
  transition: background 0.15s;
}

.step-action:hover {
  background: var(--accent-glow);
}

.step-done-label {
  margin-left: auto;
  color: var(--success);
  font-size: 12px;
  font-weight: 600;
  flex-shrink: 0;
}

/* ─── Info grid (retained cards) ─── */
.info-grid {
  display: grid;
  grid-template-columns: repeat(auto-fit, minmax(140px, 1fr));
  gap: 14px;
}

.info-item {
  display: flex;
  flex-direction: column;
  gap: 4px;
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
  /* Break anywhere but without the mid-glyph harshness of break-all for
     long model names / versions */
  overflow-wrap: anywhere;
  font-variant-numeric: tabular-nums;
}

/* ─── Usage bars inside the GPU card (shared visual language) ─── */
.usage-block {
  margin-bottom: 14px;
}

.usage-block:last-child {
  margin-bottom: 0;
}

.usage-row {
  display: flex;
  justify-content: space-between;
  align-items: baseline;
  gap: 12px;
  margin-bottom: 8px;
  font-size: 13px;
  /* Live metrics refresh every few seconds; monospaced digits stop the
     values from jittering horizontally as they change */
  font-variant-numeric: tabular-nums;
}

.usage-name {
  color: var(--text-dim);
  overflow: hidden;
  text-overflow: ellipsis;
  white-space: nowrap;
}

.usage-caption {
  color: var(--text-muted);
  font-size: 12px;
  white-space: nowrap;
}

.usage-pct {
  font-weight: 700;
  color: var(--accent-light);
  font-size: 13px;
  white-space: nowrap;
}

.usage-bar {
  height: 6px;
  background: var(--overlay-8);
  border-radius: 3px;
  overflow: hidden;
}

.usage-fill {
  height: 100%;
  border-radius: 3px;
  background: linear-gradient(90deg, var(--accent), var(--accent-light));
  transition: width 0.6s ease;
}

.gpu-name {
  color: var(--accent-light);
  font-weight: 600;
}

.meta-row {
  display: flex;
  flex-wrap: wrap;
  gap: 4px 16px;
  font-size: 12px;
  color: var(--text-dim);
  /* Uniform breathing room below the usage bar in every card */
  margin-top: 10px;
  font-variant-numeric: tabular-nums;
}

.gpu-block {
  padding: 14px 0;
}

/* Tight rhythm against the card title / card bottom edge */
.gpu-block:first-child {
  padding-top: 0;
}

.gpu-block:last-child {
  padding-bottom: 0;
}

/* Divider only between consecutive GPU blocks */
.gpu-block.divided {
  border-top: 1px solid var(--border-light);
}

.warning-text {
  color: var(--warning);
  font-weight: 600;
}

/* ─── Status badge (CUDA card) ─── */
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

/* ─── Empty ─── */
.info-empty {
  padding: 16px 0;
  color: var(--text-dim);
  font-size: 13px;
  text-align: center;
}

/* ─── Loading skeleton ─── */
.skeleton-card {
  min-height: 120px;
}

.hero-skel {
  min-height: 170px;
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

/* ─── Phone (<=767px): design draft frames ①/②. Checklist becomes the
       mockup .check card (13px title, dashed separators, 26px markers, muted
       sub-lines, bare bold go-links), the offline hero gets the mockup gray
       gradient, minis + hero drop to the 22px --r-md radius, resident card
       tiles shrink to 42px/13px with the accent stroke and gain the green
       "resident" source pill, the uninstalled llama.cpp brick reads amber and
       metric digits render in the mono face. Touch targets stay ≥44px. ─── */
@media (max-width: 767px) {
  .onboarding-step {
    min-height: 44px;
  }

  .onboarding-dismiss {
    padding: 10px 12px;
  }

  .retry-btn {
    min-height: 44px;
  }

  .unload-btn {
    min-height: 44px;
  }

  /* ── Checklist (mockup .check / .cstep) ── */
  .onboarding-head .section-title {
    font-size: 13px;
    font-weight: 700;
  }

  /* Mockup .check h4 has no leading icon — plain "快速上手" text only */
  .onboarding-head .section-title svg {
    display: none;
  }

  .onboarding-steps {
    gap: 0;
  }

  .onboarding-step {
    padding: 10px 0;
    border-bottom: 1px dashed var(--border);
    font-size: 14px;
    font-weight: 700;
    color: var(--text-primary);
  }

  .onboarding-step:last-child {
    border-bottom: none;
    padding-bottom: 2px;
  }

  .step-marker {
    width: 26px;
    height: 26px;
    border: none;
    background: var(--overlay-8);
    font-size: 12px;
    font-weight: 800;
  }

  .step-text {
    min-width: 0;
    display: flex;
    flex-direction: column;
    gap: 2px;
  }

  .step-sub {
    font-size: 11.5px;
    font-weight: 500;
    color: var(--text-dim);
    overflow: hidden;
    text-overflow: ellipsis;
    white-space: nowrap;
  }

  /* Go-link: mockup .cstep .go — bare bold accent text, no filled chip */
  .step-action {
    min-height: 44px;
    padding: 10px 0 10px 12px;
    background: transparent;
    border-radius: 8px;
    color: var(--accent-light);
    font-size: 12px;
    font-weight: 800;
  }

  .step-action:hover {
    background: transparent;
  }

  .step-action svg {
    display: none;
  }

  /* ── Hero (mockup .hero / .hero.off) ── */
  .hero-card {
    border-radius: var(--r-md);
  }

  .hero-card.off {
    background: linear-gradient(135deg, #4b5069 0%, #6b7186 60%, #8b90a5 100%);
    box-shadow: none;
  }

  .hero-card.off .tag-dot {
    background: #cbd0e0;
    box-shadow: none;
  }

  /* Metric digits in the mono face (mockup .num / .val numerals) */
  .hero-num {
    font-family: var(--font-mono);
  }

  /* ── Mini cards (mockup .mini: --r-md radius) ──
     The used-memory figure is the widest value these cards carry
     ("11.9 / 16 GB" of mono digits beside the ring); on a 390px phone the
     desktop-grade 19px digits + 46px ring + 16px card padding overflow the
     tile and ellipsize the used value. The phone tier tightens all three
     (16px digits, 42px ring, 12px padding) and lets the unit WRAP rather
     than shrink: the number itself must render complete — ellipsis
     truncation of a metric is forbidden — so at 320px the small unit drops
     to a second baseline row inside the value block instead. The CPU mini
     shares every rule here. */
  .mini {
    border-radius: var(--r-md);
    padding: 12px;
  }

  /* Ring scales with its box (viewBox 0 0 46 46): 42px keeps the gauge
     readable while handing 4px back to the value row. */
  .ring {
    width: 42px;
    height: 42px;
  }

  .mini-val {
    font-family: var(--font-mono);
    /* Frame ② .mini .val: number + unit on one baseline while space allows.
       flex-wrap is the overflow valve: when the row cannot fit (320px), the
       non-shrinking <small> unit wraps to the next line — the number is
       never ellipsized and the unit is never cut mid-glyph. */
    display: flex;
    flex-wrap: wrap;
    align-items: baseline;
    min-width: 0;
    overflow: visible;
    text-overflow: clip;
    white-space: normal;
    font-size: 16px;
    line-height: 1.25;
  }

  .mini-num {
    white-space: nowrap;
  }

  .mini-val small {
    flex-shrink: 0;
    white-space: nowrap;
  }

  .mini-main {
    gap: 6px;
  }

  /* ── Resident model card (mockup .mcard tile + .src-g pill) ── */
  .mcard .tile {
    width: 42px;
    height: 42px;
    border-radius: 13px;
    color: var(--accent-light);
  }

  .mcard-chips i.resident-chip {
    background: var(--success-bg);
    color: var(--success);
  }

  html[data-theme='dark'] .mcard-chips i.resident-chip {
    background: #12261f;
    color: #6ee7b7;
  }

  /* ── Storage island: uninstalled llama.cpp brick reads amber ── */
  .brick-val.warn {
    color: var(--warning);
  }
}

/* ─── Tablet portrait (768..1099px, design draft track A frames ①/②): the
       single-column layout from the tier above stays, but the first-use screen
       picks up the draft .check card treatment (26px markers, dashed rows,
       muted sub-lines, bare accent go-links) and the offline hero gets the
       mockup gray gradient. Card order follows draft A②: resident-model card
       above the storage island, capability/system cards last. ─── */
@media (min-width: 768px) and (max-width: 1099px) {
  /* Draft A② card order: hero → minis → resident → storage → capability cards.
     The wrappers are display:contents here, so the promoted children order
     against each other inside the outer grid. */
  .home-side-col > .mcard {
    order: 1;
  }

  .home-side-col > .storage-card {
    order: 2;
  }

  .home-info-col > .island {
    order: 3;
  }

  /* ─── Ready-state pairing (.sys-paired = exactly one resident model, see
         lib/homeLayout): the compact resident-model card and the system
         summary card share one two-column row BELOW the storage island,
         filling the band's dead bottom third. Every other card spans the
         full width, so the grid only ever splits this one row. First-use
         (zero residents) and multi-resident states never get the hook and
         keep the single-column stack above. ─── */
  .sys-grid.sys-paired {
    grid-template-columns: 1fr 1fr;
  }

  .sys-grid.sys-paired .home-side-col > .island,
  .sys-grid.sys-paired .home-main-col > .hero-card,
  .sys-grid.sys-paired .home-main-col > .grid2,
  .sys-grid.sys-paired .home-info-col > .island {
    grid-column: 1 / -1;
  }

  /* The pairing row: both cards take a single cell each. order moves the
     resident card below the storage island (order 2); the un-paired band
     order (resident above storage, draft A②) is unchanged. Source order
     after the span rule matters: these must win the grid-column override. */
  .sys-grid.sys-paired .home-side-col > .mcard {
    order: 3;
  }

  .sys-grid.sys-paired .home-side-col > .mcard,
  .sys-grid.sys-paired .home-info-col > .sys-card {
    grid-column: auto;
  }

  /* Offline hero: mockup .hero.off gray gradient (draft A① 离线灰调) */
  .hero-card.off {
    background: linear-gradient(135deg, #4b5069 0%, #6b7186 60%, #8b90a5 100%);
    box-shadow: none;
  }

  .hero-card.off .tag-dot {
    background: #cbd0e0;
    box-shadow: none;
  }

  /* Metric digits in the mono face (mockup .num / .val numerals) */
  .hero-num,
  .mini-val {
    font-family: var(--font-mono);
  }

  /* Touch targets stay ≥44px on the tablet too */
  .onboarding-step {
    min-height: 44px;
  }

  .onboarding-dismiss {
    padding: 10px 12px;
  }

  .unload-btn,
  .retry-btn {
    min-height: 44px;
  }

  /* ── Checklist (mockup .check / .cstep, same treatment as the phone tier) ── */
  .onboarding-head .section-title {
    font-size: 13px;
    font-weight: 700;
  }

  /* Mockup .check h4 has no leading icon — plain "快速上手" text only */
  .onboarding-head .section-title svg {
    display: none;
  }

  .onboarding-steps {
    gap: 0;
  }

  .onboarding-step {
    padding: 10px 0;
    border-bottom: 1px dashed var(--border);
    font-size: 14px;
    font-weight: 700;
    color: var(--text-primary);
  }

  .onboarding-step:last-child {
    border-bottom: none;
    padding-bottom: 2px;
  }

  .step-marker {
    width: 26px;
    height: 26px;
    border: none;
    background: var(--overlay-8);
    font-size: 12px;
    font-weight: 800;
  }

  .step-text {
    min-width: 0;
    display: flex;
    flex-direction: column;
    gap: 2px;
  }

  .step-sub {
    font-size: 11.5px;
    font-weight: 500;
    color: var(--text-dim);
    overflow: hidden;
    text-overflow: ellipsis;
    white-space: nowrap;
  }

  /* Go-link: mockup .cstep .go — bare bold accent text, no filled chip */
  .step-action {
    min-height: 44px;
    padding: 10px 0 10px 12px;
    background: transparent;
    border-radius: 8px;
    color: var(--accent-light);
    font-size: 12px;
    font-weight: 800;
  }

  .step-action:hover {
    background: transparent;
  }

  .step-action svg {
    display: none;
  }
}

</style>
