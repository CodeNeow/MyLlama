<template>
  <div class="page">
    <div class="sticky-top">
      <div class="page-header">
        <!-- Phone tier (frame ⑪ .mstop .bk) restyles this into a circular
             icon-only island button; the aria-label keeps it announced once
             the visible text label is hidden there. Desktop keeps the
             "< 返回" text button. -->
        <button class="back-btn" :aria-label="t('models.back')" @click="router.back()">
          <svg width="18" height="18" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2.2" stroke-linecap="round" stroke-linejoin="round">
            <polyline points="15 18 9 12 15 6"/>
          </svg>
          <span class="back-label">{{ t('models.back') }}</span>
        </button>
        <h1 class="page-title">{{ decodedModelName }}</h1>
        <p class="page-subtitle">{{ t('modelSettings.subtitle') }}</p>
      </div>

      <!-- Tabs row: capability-gated — the Multi-GPU tab renders only where
           splitting across devices is possible (windows/linux); macOS Metal is
           a single GPU and Android is CPU-only, so the tab must not exist there -->
      <div class="settings-tabs" role="tablist" :aria-label="t('modelSettings.tabsAria')">
        <button
          v-for="tab in visibleTabs"
          :key="tab.id"
          :id="`${tab.id}-tab`"
          class="tab-btn"
          :class="{ active: activeTab === tab.id }"
          role="tab"
          :aria-selected="activeTab === tab.id"
          :aria-controls="tab.id"
          @click="activeTab = tab.id"
        >
          <span class="tab-icon" v-html="tab.icon"></span>
          {{ tab.label() }}
        </button>
      </div>

      <!-- Action area: a single feedback line at a time (save vs tune vs bench, extended v-if chain) -->
      <div class="settings-actions">
        <span class="action-msg" v-if="saveSuccess">{{ t('modelSettings.saved') }}</span>
        <span class="action-msg action-err" v-else-if="saveError">{{ saveError }}</span>
        <span class="action-msg" v-else-if="tuneMsg">{{ tuneMsg }}</span>
        <span class="action-msg action-err" v-else-if="tuneError">{{ tuneError }}</span>
        <span class="action-msg" v-else-if="benchMsg">{{ benchMsg }}</span>
        <span class="action-msg action-err" v-else-if="benchError">{{ benchError }}</span>
        <span class="action-spacer"></span>
        <button
          class="btn-secondary tune-btn"
          :class="{ busy: tuning }"
          :disabled="tuning || benching || saving || loading"
          @click="tune"
        >
          <svg width="16" height="16" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round" stroke-linejoin="round">
            <path d="M12 3l1.9 5.8a2 2 0 0 0 1.3 1.3L21 12l-5.8 1.9a2 2 0 0 0-1.3 1.3L12 21l-1.9-5.8a2 2 0 0 0-1.3-1.3L3 12l5.8-1.9a2 2 0 0 0 1.3-1.3L12 3z"/>
            <path d="M5 3v4"/><path d="M3 5h4"/><path d="M19 17v4"/><path d="M17 19h4"/>
          </svg>
          {{ tuning ? t('modelSettings.tuning') : t('models.tune') }}
        </button>
        <!-- Deep benchmark: runs llama-bench next to llama-server with the
             saved config; loads the full model and may take minutes -->
        <button
          class="btn-secondary bench-btn"
          :class="{ busy: benching }"
          :disabled="benching || tuning || saving || loading"
          :title="t('modelSettings.benchHint')"
          @click="bench"
        >
          <svg width="16" height="16" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round" stroke-linejoin="round">
            <path d="m12 14 4-4"/>
            <path d="M3.34 19a10 10 0 1 1 17.32 0"/>
          </svg>
          {{ benching ? t('modelSettings.benching') : t('modelSettings.bench') }}
        </button>
        <button class="btn-secondary" @click="reset">{{ t('modelSettings.reset') }}</button>
        <button
          class="btn-primary"
          :disabled="saving || hasErrors || !isModified"
          @click="save"
        >
          {{ saving ? t('modelSettings.saving') : t('modelSettings.save') }}
        </button>
      </div>

      <!-- Service running hint: the preset is generated at service start, so
           saved parameters only apply after a restart. Phone tier (frame ⑪)
           swaps the old ⚠ text prefix for a circled-! amber SVG icon. -->
      <div v-if="serverRunning" class="restart-note">
        <svg v-if="platformState.isMobile" class="note-icon" width="14" height="14" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2.2" stroke-linecap="round">
          <circle cx="12" cy="12" r="9"/><path d="M12 8v5M12 16.5v.5"/>
        </svg>
        {{ t('modelSettings.restartNote') }}
      </div>
    </div>

    <!-- Loading -->
    <div v-if="loading" class="loading-placeholder">{{ t('modelSettings.loading') }}</div>

    <!-- Error state -->
    <div v-else-if="loadError" class="error-card">
      <p class="error-text">{{ t('modelSettings.loadFailed') }}：{{ loadError }}</p>
      <button class="retry-btn" @click="loadConfig">{{ t('modelSettings.retry') }}</button>
    </div>

    <!-- Tab panels -->
    <div v-else class="settings-body">
      <!-- Basic -->
      <div id="tab-base" role="tabpanel" aria-labelledby="tab-base-tab" v-show="activeTab === 'tab-base'">
        <div class="param-group">
          <h3 class="group-title">{{ t('modelSettings.groupBase') }}</h3>
          <div class="param-grid">
            <!-- param-frow marks plain input rows: the phone tier reflows them
                 into the design draft frame ⑪ .frow layout (label left, mono input
                 right); select / toggle rows keep the stacked layout -->
            <div class="param param-frow">
              <label class="param-field">
                <span class="param-label">{{ t('modelSettings.threads') }}</span>
                <input v-model.number="cfg.threads" type="number" min="-1" step="1" class="param-input" :placeholder="t('modelSettings.threadsPlaceholder')" />
              </label>
              <p class="param-hint">{{ t('modelSettings.threadsHint') }}</p>
            </div>
            <!-- GPU offload (-ngl): meaningful only where a GPU can actually
                 take layers (Windows CUDA / Linux Vulkan / macOS Metal);
                 android/ios are CPU-only, so the selector must not render -->
            <div v-if="showOffload" class="param">
              <label class="param-field">
                <span class="param-label">{{ t('modelSettings.gpuLayers') }}</span>
                <ThemedSelect
                  v-model="cfg.gpuLayers"
                  :options="gpuLayersOptions"
                  :label="t('modelSettings.gpuLayers')"
                />
              </label>
              <p class="param-hint">{{ t('modelSettings.gpuLayersHint') }}</p>
            </div>
            <div class="param param-frow">
              <label class="param-field">
                <span class="param-label">{{ t('modelSettings.ctxSize') }}</span>
                <input v-model.number="cfg.ctxSize" type="number" min="1" step="1" class="param-input" placeholder="4096" />
              </label>
              <p class="param-hint">{{ t('modelSettings.ctxSizeHint') }}</p>
            </div>
            <div class="param param-frow">
              <label class="param-field">
                <span class="param-label">{{ t('modelSettings.batchSize') }}</span>
                <input v-model.number="cfg.batchSize" type="number" min="1" step="1" class="param-input" placeholder="2048" />
              </label>
              <p class="param-hint">{{ t('modelSettings.batchSizeHint') }}</p>
            </div>
            <div class="param param-frow">
              <label class="param-field">
                <span class="param-label">{{ t('modelSettings.ubatchSize') }}</span>
                <input v-model.number="cfg.ubatchSize" type="number" min="1" step="1" class="param-input" placeholder="512" />
              </label>
              <p class="param-hint">{{ t('modelSettings.ubatchSizeHint') }}</p>
            </div>
          </div>
        </div>
      </div>

      <!-- Inference -->
      <div id="tab-infer" role="tabpanel" aria-labelledby="tab-infer-tab" v-show="activeTab === 'tab-infer'">
        <div class="param-group">
          <h3 class="group-title">{{ t('modelSettings.groupInfer') }}</h3>
          <!-- GPU-only params (flash-attn / cpu-moe / n-cpu-moe) share the same
               showOffload gate as the gpu-layers selector: the android build
               ships no GPU backend and the macOS x64 release is CPU-only, so
               these options must not render where no GPU can take them (the
               backend direct-args serializer drops them there as well) -->
          <div class="param-grid col-1">
            <div v-if="showOffload" class="param">
              <div class="toggle-row">
                <span class="toggle-text">
                  Flash Attention
                  <span class="toggle-sub">{{ t('modelSettings.flashAttnSub') }}</span>
                </span>
                <label class="switch">
                  <input type="checkbox" v-model="cfg.flashAttn" aria-label="Flash Attention" />
                  <span class="slider"></span>
                </label>
              </div>
              <p class="param-hint">{{ t('modelSettings.flashAttnHint') }}</p>
            </div>
            <div v-if="showOffload" class="param">
              <div class="toggle-row">
                <span class="toggle-text">
                  cpu-moe
                  <span class="toggle-sub">{{ t('modelSettings.cpuMoeSub') }}</span>
                </span>
                <label class="switch">
                  <input type="checkbox" v-model="cfg.cpuMoe" aria-label="cpu-moe" />
                  <span class="slider"></span>
                </label>
              </div>
              <p class="param-hint">{{ t('modelSettings.cpuMoeHint') }}</p>
            </div>
            <div v-if="showOffload" class="param param-frow">
              <label class="param-field">
                <span class="param-label">{{ t('modelSettings.nCpuMoe') }}</span>
                <input v-model.number="cfg.nCpuMoe" type="number" min="0" step="1" class="param-input" :placeholder="t('modelSettings.nCpuMoePlaceholder')" />
              </label>
              <p class="param-hint">{{ t('modelSettings.nCpuMoeHint') }}</p>
            </div>
          </div>
        </div>
      </div>

      <!-- Memory / Loading -->
      <div id="tab-memory" role="tabpanel" aria-labelledby="tab-memory-tab" v-show="activeTab === 'tab-memory'">
        <div class="param-group">
          <h3 class="group-title">{{ t('modelSettings.groupMemory') }}</h3>
          <div class="param-grid col-1">
            <div class="param">
              <label class="param-field">
                <span class="param-label">{{ t('modelSettings.cacheTypeK') }}</span>
                <ThemedSelect
                  v-model="cfg.cacheTypeK"
                  :options="cacheTypeOptions"
                  :label="t('modelSettings.cacheTypeK')"
                />
              </label>
              <p class="param-hint">{{ t('modelSettings.cacheTypeKHint') }}</p>
            </div>
            <div class="param">
              <label class="param-field">
                <span class="param-label">{{ t('modelSettings.cacheTypeV') }}</span>
                <ThemedSelect
                  v-model="cfg.cacheTypeV"
                  :options="cacheTypeOptions"
                  :label="t('modelSettings.cacheTypeV')"
                />
              </label>
              <p class="param-hint">{{ t('modelSettings.cacheTypeVHint') }}</p>
            </div>
            <div class="param">
              <label class="param-field">
                <span class="param-label">{{ t('modelSettings.loadMode') }}</span>
                <ThemedSelect
                  v-model="cfg.loadMode"
                  :options="loadModeOptions"
                  :label="t('modelSettings.loadMode')"
                />
              </label>
              <p class="param-hint">{{ t('modelSettings.loadModeHint') }}</p>
            </div>
          </div>
        </div>

        <!-- Phone tier (design draft frame ⑫): live "参数速览" summary island at the
             bottom of the memory tab — current form values plus the auto-tune
             hardware basis. Desktop keeps the plain form (isMobile-gated DOM). -->
        <div v-if="platformState.isMobile" class="summary-island">
          <h4 class="summary-title">{{ t('modelSettings.summaryTitle') }}</h4>
          <p class="summary-body">{{ summaryBody }}</p>
          <p class="summary-sub">{{ summarySub }}</p>
        </div>
      </div>

      <!-- Multi-GPU -->
      <!-- Multi-GPU panel: capability-gated to windows/linux (see visibleTabs) -->
      <div v-if="showMultiGpu" id="tab-gpu" role="tabpanel" aria-labelledby="tab-gpu-tab" v-show="activeTab === 'tab-gpu'">
        <div class="param-group">
          <h3 class="group-title">{{ t('modelSettings.groupGpu') }}</h3>
          <div class="param-grid col-1">
            <div class="param">
              <label class="param-field">
                <span class="param-label">{{ t('modelSettings.splitMode') }}</span>
                <ThemedSelect
                  v-model="cfg.splitMode"
                  :options="splitModeOptions"
                  :label="t('modelSettings.splitMode')"
                />
              </label>
              <p class="param-hint">{{ t('modelSettings.splitModeHint') }}</p>
            </div>
            <div class="param param-frow">
              <label class="param-field">
                <span class="param-label">{{ t('modelSettings.tensorSplit') }}</span>
                <input v-model="cfg.tensorSplit" type="text" class="param-input" :placeholder="t('modelSettings.tensorSplitPlaceholder')" />
              </label>
              <p class="param-hint">{{ t('modelSettings.tensorSplitHint') }}</p>
            </div>
            <div class="param param-frow">
              <label class="param-field">
                <span class="param-label">{{ t('modelSettings.mainGpu') }}</span>
                <input v-model.number="cfg.mainGpu" type="number" min="0" step="1" class="param-input" placeholder="0" />
              </label>
              <p class="param-hint">{{ t('modelSettings.mainGpuHint') }}</p>
            </div>
          </div>
        </div>
      </div>

      <!-- Long context -->
      <div id="tab-context" role="tabpanel" aria-labelledby="tab-context-tab" v-show="activeTab === 'tab-context'">
        <div class="param-group">
          <h3 class="group-title">{{ t('modelSettings.groupContext') }}</h3>
          <div class="param-grid col-1">
            <div class="param">
              <label class="param-field">
                <span class="param-label">{{ t('modelSettings.ropeScaling') }}</span>
                <ThemedSelect
                  v-model="cfg.ropeScaling"
                  :options="ropeScalingOptions"
                  :label="t('modelSettings.ropeScaling')"
                />
              </label>
              <p class="param-hint">{{ t('modelSettings.ropeScalingHint') }}</p>
            </div>
            <div class="param param-frow">
              <label class="param-field">
                <span class="param-label">{{ t('modelSettings.ropeScale') }}</span>
                <input v-model.number="cfg.ropeScale" type="number" min="0" step="0.5" class="param-input" placeholder="0" />
              </label>
              <p class="param-hint">{{ t('modelSettings.ropeScaleHint') }}</p>
            </div>
          </div>
        </div>
      </div>

      <!-- Advanced -->
      <div id="tab-advanced" role="tabpanel" aria-labelledby="tab-advanced-tab" v-show="activeTab === 'tab-advanced'">
        <div class="param-group">
          <h3 class="group-title">{{ t('modelSettings.groupAdvanced') }}</h3>
          <div class="param-grid col-1">
            <div class="param param-frow">
              <label class="param-field">
                <span class="param-label">{{ t('modelSettings.mmproj') }}</span>
                <input v-model="cfg.mmproj" type="text" class="param-input" :placeholder="t('modelSettings.mmprojPlaceholder')" />
              </label>
              <p class="param-hint">{{ t('modelSettings.mmprojHint') }}</p>
            </div>
            <div class="param">
              <div class="toggle-row">
                <span class="toggle-text">
                  {{ t('modelSettings.reasoning') }}
                  <span class="toggle-sub">{{ t('modelSettings.reasoningSub') }}</span>
                </span>
                <label class="switch">
                  <input type="checkbox" v-model="cfg.reasoning" aria-label="Reasoning" />
                  <span class="slider"></span>
                </label>
              </div>
              <p class="param-hint">{{ t('modelSettings.reasoningHint') }}</p>
            </div>
            <div class="param">
              <label class="param-field">
                <span class="param-label">{{ t('modelSettings.specType') }}</span>
                <ThemedSelect
                  v-model="cfg.specType"
                  :options="specTypeOptions"
                  :label="t('modelSettings.specType')"
                />
              </label>
              <p class="param-hint">{{ t('modelSettings.specTypeHint') }}</p>
            </div>
            <div class="param param-frow">
              <label class="param-field">
                <span class="param-label">{{ t('modelSettings.specDraftNMax') }}</span>
                <input v-model.number="cfg.specDraftNMax" type="number" min="0" step="1" class="param-input" placeholder="0" />
              </label>
              <p class="param-hint">{{ t('modelSettings.specDraftNMaxHint') }}</p>
            </div>
          </div>
        </div>
      </div>
    </div>

    <!-- Tablet portrait summary island (tablet draft frames ⑪⑫, track A):
         the draft sinks the 参数速览 island to the bottom of the page on
         every tab. Phone keeps its own in-tab island (untouched);
         desktop renders no island. -->
    <div v-if="showPortraitSummary && !loading && !loadError" class="summary-island">
      <h4 class="summary-title">{{ t('modelSettings.summaryTitle') }}</h4>
      <div v-for="row in summaryRows" :key="row.key" class="summary-row">
        <span class="summary-label">{{ row.label }}</span>
        <b
          class="summary-value"
          :class="[`summary-value--${row.tone}`, { 'summary-value--mono': row.mono }]"
        >{{ row.value }}</b>
      </div>
    </div>
  </div>
</template>

<script setup lang="ts">
import { ref, reactive, computed, watch, onMounted, onUnmounted } from 'vue'
import { useRouter, useRoute } from 'vue-router'
import { getModelConfig, saveModelConfig, getServerStatus, tuneModelConfig, benchmarkModel, getSystemInfo, getModels } from '../wails'
import { t } from '../lib/i18n'
import { tunedSummaryParams, tunedToastKey } from '../lib/modelTune'
import { estimateMemory, memorySummaryRows, type MemorySummaryCopy } from '../lib/modelMemory'
import { showGpuOffloadParam, showMultiGpuPanel, loadModeOptions as buildLoadModeOptions, usePlatform } from '../lib/platform'
import ThemedSelect, { type SelectOption } from '../components/ThemedSelect.vue'

// ─── ModelConfig interface (persisted per-model inference params shape) ───────
export interface ModelConfig {
  threads: number
  gpuLayers: string
  ctxSize: number
  batchSize: number
  ubatchSize: number
  flashAttn: boolean
  cacheTypeK: string
  cacheTypeV: string
  loadMode: string
  cpuMoe: boolean
  nCpuMoe: number
  splitMode: string
  tensorSplit: string
  mainGpu: number
  ropeScaling: string
  ropeScale: number
  mmproj: string
  reasoning: boolean
  specType: string
  specDraftNMax: number
  // Set by the backend auto-tuner (Windows full-offload / Android plans);
  // optional because old configs and the saved JSON omit it. No UI control:
  // the form only round-trips the value so manual saves never wipe it.
  ctxCheckpointsOff?: boolean
}

// ─── Defaults ─────────────────────────────────────────────────────────────────
const defaults: ModelConfig = {
  threads: -1,
  gpuLayers: 'auto',
  ctxSize: 4096,
  batchSize: 2048,
  ubatchSize: 512,
  flashAttn: false,
  cacheTypeK: '',
  cacheTypeV: '',
  loadMode: '',
  cpuMoe: false,
  nCpuMoe: 0,
  splitMode: '',
  tensorSplit: '',
  mainGpu: 0,
  ropeScaling: '',
  ropeScale: 0,
  mmproj: '',
  reasoning: false,
  specType: '',
  specDraftNMax: 0,
  // Round-trip key: loadConfig copies only Object.keys(defaults) onto cfg, so
  // the tuner-set flag must be a defaults key to reach save() unsent-wiped.
  ctxCheckpointsOff: false,
}

// ─── Route params ───────────────────────────────────────────────────────────────
const router = useRouter()
const route = useRoute()

// Model name is URL-encoded in the route; decode it for display
const decodedModelName = computed(() => decodeURIComponent(String(route.params.modelName || '')))
// The raw encoded value is used for backend API calls
const modelName = computed(() => String(route.params.modelName || ''))

// ─── Local state ───────────────────────────────────────────────────────────────
const loading = ref(false)
const loadError = ref('')

// Snapshot taken after the config loads successfully (used to compute isModified)
const initialConfig = ref<ModelConfig>({ ...defaults })
const saving = ref(false)
const saveError = ref('')
const saveSuccess = ref(false)

// ─── Auto-tune ────────────────────────────────────────────────────────────────
// One tune runs at a time; the busy state also drives the button label/spin.
const tuning = ref(false)
// Tune feedback in the action area (mutually exclusive via the v-if chain);
// auto-clears after 5 seconds, timer cleaned up on unmount.
const tuneMsg = ref('')
const tuneError = ref('')
let tuneTimer: ReturnType<typeof setTimeout> | undefined

function clearTuneFeedback() {
  tuneMsg.value = ''
  tuneError.value = ''
}

function clearTuneTimer() {
  if (tuneTimer) {
    clearTimeout(tuneTimer)
    tuneTimer = undefined
  }
}

// ─── Deep benchmark ───────────────────────────────────────────────────────────
// llama-bench loads the full model and may run for minutes: one bench at a
// time, the busy state drives the button label/spin, and Tune is disabled
// while a bench runs (the two would contend for the same GPU). Feedback uses
// the same single action line, auto-clearing after 8 s (longer than tune —
// the action itself is much longer), timer cleaned up on unmount.
const benching = ref(false)
const benchMsg = ref('')
const benchError = ref('')
let benchTimer: ReturnType<typeof setTimeout> | undefined

function clearBenchFeedback() {
  benchMsg.value = ''
  benchError.value = ''
}

function clearBenchTimer() {
  if (benchTimer) {
    clearTimeout(benchTimer)
    benchTimer = undefined
  }
}

// Capability gates from the shared platform state (OS-scoped): the Multi-GPU
// tab exists only where devices can be split (windows/linux), and the GPU
// offload selector only where a GPU can take layers (adds macOS Metal).
const platformState = usePlatform()
const showMultiGpu = computed(() => showMultiGpuPanel(platformState.value))
const showOffload = computed(() => showGpuOffloadParam(platformState.value))

// ─── Tablet tracks (design draft frames ⑪⑫) ──────────────────────────────────
// Track A (portrait tablet band, 768..1099): the live memory summary sunk to
// the page bottom — isTablet mirrors the CSS band exactly (width-only
// classifier; Android tablets run the phone/desktop layouts, see App.vue).
const showPortraitSummary = computed(() => platformState.value.isTablet)

// ─── Live memory estimate (phone + tablet portrait summary islands) ───────────
// Weights term: the scanned GGUF file size looked up by model name (0 while
// unknown). Memory terms: the system snapshot captured on mount (0 = probe
// unavailable). The estimate is a pure computed over the live form values, so
// every edit (ctx length, KV quant types, threads) updates both placements
// immediately. See lib/modelMemory.ts for the honesty rules (unknown inputs
// drop their rows instead of guessing).
const GIB = 1024 * 1024 * 1024
const modelSizeBytes = ref(0)
const sysTotalBytes = ref(0)
const sysFreeBytes = ref(0)

const memoryEstimate = computed(() =>
  estimateMemory({
    modelSizeBytes: modelSizeBytes.value,
    ctxSize: Number(cfg.ctxSize) || 0,
    cacheTypeK: cfg.cacheTypeK,
    cacheTypeV: cfg.cacheTypeV,
    totalMemBytes: sysTotalBytes.value,
    freeMemBytes: sysFreeBytes.value,
  })
)

// Localized labels for the summary rows; the computed re-runs on locale
// switches so both placements follow the app language.
const summaryCopy = computed<MemorySummaryCopy>(() => ({
  ctxThreads: t('modelSettings.summaryCtxThreads'),
  kv: t('modelSettings.summaryKv'),
  estimate: t('modelSettings.summaryEstimate'),
  kvSaved: t('modelSettings.summaryKvSaved'),
  available: t('modelSettings.summaryAvailable'),
  headroom: t('modelSettings.summaryHeadroom'),
  threadsAuto: t('modelSettings.threadsAutoShort'),
  headroomOk: t('modelSettings.headroomOk'),
  headroomTight: t('modelSettings.headroomTight'),
  headroomOver: t('modelSettings.headroomOver'),
}))

const summaryRows = computed(() =>
  memorySummaryRows(
    memoryEstimate.value,
    {
      ctx: Number(cfg.ctxSize) || 0,
      threads: Number(cfg.threads) || 0,
      cacheTypeK: cfg.cacheTypeK,
      cacheTypeV: cfg.cacheTypeV,
    },
    summaryCopy.value
  )
)

/** Look up the model's scanned GGUF file size (the estimate's weights term). */
async function lookupModelSize() {
  const name = decodedModelName.value
  if (!name) return
  try {
    const found = (await getModels()).find(
      (m: { name?: string; sizeBytes?: number }) => m?.name === name
    )
    const size = Number(found?.sizeBytes)
    modelSizeBytes.value = size > 0 ? size : 0
  } catch {
    // Model scan unavailable (e.g. standalone preview): leave 0 — the summary
    // then honestly omits the estimate rows instead of showing a guess.
  }
}

// Id of the currently active tab (stable string id, not an index: the
// capability-gated visibleTabs list can be shorter on some platforms)
const activeTab = ref('tab-base')

/** Tabs visible on this platform: filters out the Multi-GPU tab where
    splitting across devices is not a real capability, and the Inference tab
    where every param is GPU-offload-gated — on CPU-only platforms (Android)
    that tab would render as an empty shell (frame ⑪: exactly 基础参数 /
    内存·加载 / 长上下文 / 高级 there). */
const visibleTabs = computed(() =>
  tabs.filter(
    (tab) =>
      (tab.id !== 'tab-gpu' || showMultiGpu.value) &&
      (tab.id !== 'tab-infer' || showOffload.value)
  )
)

/** Current config (reactive), initialized from defaults + loaded values */
const cfg = reactive<ModelConfig>({ ...defaults })

/**
 * Whether the llama-server is currently running: when true, saved parameters
 * only take effect after the service is restarted (the preset is generated at
 * service start), so the page shows a hint.
 */
const serverRunning = ref(false)

// ─── ThemedSelect option lists (mirrors the former native <select> options) ───
const gpuLayersOptions: SelectOption[] = [
  { value: 'auto', label: t('modelSettings.gpuAuto') },
  { value: 'all', label: t('modelSettings.gpuAll') },
  { value: '0', label: t('modelSettings.gpuCpuOnly') },
  { value: '10', label: '10' },
  { value: '20', label: '20' },
  { value: '30', label: '30' },
  { value: '40', label: '40' },
  { value: '50', label: '50' },
  { value: '99', label: '99' },
]
const cacheTypeOptions: SelectOption[] = [
  { value: '', label: t('modelSettings.defaultF16') },
  { value: 'f32', label: 'f32' },
  { value: 'f16', label: 'f16' },
  { value: 'bf16', label: 'bf16' },
  { value: 'q8_0', label: t('modelSettings.q8Recommended') },
  { value: 'q4_0', label: 'q4_0' },
  { value: 'q4_1', label: 'q4_1' },
  { value: 'iq4_nl', label: 'iq4_nl' },
  { value: 'q5_0', label: 'q5_0' },
  { value: 'q5_1', label: 'q5_1' },
]
// Load-mode options are platform-derived: 'dio' is excluded where DirectIO is
// not meaningful (android / darwin); see lib/platform.ts loadModeOptions.
const loadModeOptions = computed(() => buildLoadModeOptions(platformState.value))
const splitModeOptions: SelectOption[] = [
  { value: '', label: t('modelSettings.splitDefaultLayer') },
  { value: 'layer', label: t('modelSettings.splitLayer') },
  { value: 'row', label: t('modelSettings.splitRow') },
  { value: 'tensor', label: t('modelSettings.splitTensor') },
  { value: 'none', label: t('modelSettings.splitNone') },
]
const ropeScalingOptions: SelectOption[] = [
  { value: '', label: t('modelSettings.ropeDefaultNone') },
  { value: 'none', label: 'none' },
  { value: 'linear', label: t('modelSettings.ropeLinear') },
  { value: 'yarn', label: t('modelSettings.ropeYarn') },
]
const specTypeOptions: SelectOption[] = [
  { value: '', label: t('modelSettings.specOff') },
  { value: 'draft-mtp', label: 'draft-mtp' },
  { value: 'ngram-simple', label: 'ngram-simple' },
  { value: 'ngram-mod', label: 'ngram-mod' },
]

// ─── Phone summary island (frame ⑫ "参数速览") ────────────────────────────────
// Live readout of the current form values (mirrors the auto-tune vocabulary:
// empty cache types mean the backend default f16; the load-mode label comes
// from the platform-filtered option list). The sub line names the hardware
// basis from the system snapshot (RAM total + CPU/SoC model); when the probe
// is unavailable it degrades to a generic line.
const sysRamGb = ref(0)
const sysCpuModel = ref('')

const summaryBody = computed(() => {
  const modeLabel = loadModeOptions.value.find((o) => o.value === cfg.loadMode)?.label || '—'
  return t('modelSettings.summaryBody', {
    ctx: cfg.ctxSize,
    threads: cfg.threads,
    ctk: cfg.cacheTypeK || 'f16',
    ctv: cfg.cacheTypeV || 'f16',
    mode: modeLabel,
  })
})

const summarySub = computed(() => {
  if (sysRamGb.value > 0 && sysCpuModel.value) {
    return t('modelSettings.summarySubHardware', {
      ram: `${Math.round(sysRamGb.value)}GB`,
      soc: sysCpuModel.value,
    })
  }
  return t('modelSettings.summarySubGeneric')
})

// ─── Tab definitions (6 categories, icons are inline stroke SVGs) ────────────────────────────
const tabs = [
  {
    id: 'tab-base',
    label: () => t('modelSettings.tabBase'),
    icon: `<svg width="16" height="16" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round" stroke-linejoin="round"><circle cx="12" cy="12" r="3"/><path d="M19.4 15a1.65 1.65 0 0 0 .33 1.82l.06.06a2 2 0 0 1 0 2.83 2 2 0 0 1-2.83 0l-.06-.06a1.65 1.65 0 0 0-1.82-.33 1.65 1.65 0 0 0-1 1.51V21a2 2 0 0 1-2 2 2 2 0 0 1-2-2v-.09A1.65 1.65 0 0 0 9 19.4a1.65 1.65 0 0 0-1.82.33l-.06.06a2 2 0 0 1-2.83 0 2 2 0 0 1 0-2.83l.06-.06A1.65 1.65 0 0 0 4.68 15a1.65 1.65 0 0 0-1.51-1H3a2 2 0 0 1-2-2 2 2 0 0 1 2-2h.09A1.65 1.65 0 0 0 4.6 9a1.65 1.65 0 0 0-.33-1.82l-.06-.06a2 2 0 0 1 0-2.83 2 2 0 0 1 2.83 0l.06.06A1.65 1.65 0 0 0 9 4.68a1.65 1.65 0 0 0 1-1.51V3a2 2 0 0 1 2-2 2 2 0 0 1 2 2v.09a1.65 1.65 0 0 0 1 1.51 1.65 1.65 0 0 0 1.82-.33l.06.06a2 2 0 0 1 2.83 0 2 2 0 0 1 0-2.83l-.06-.06A1.65 1.65 0 0 0 19.4 9a1.65 1.65 0 0 0 1.51 1H21a2 2 0 0 1 2 2 2 2 0 0 1-2 2h-.09a1.65 1.65 0 0 0-1.51 1z"/></svg>`,
  },
  {
    id: 'tab-infer',
    label: () => t('modelSettings.tabInfer'),
    icon: `<svg width="16" height="16" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round" stroke-linejoin="round"><polygon points="13 2 3 14 12 14 11 22 21 10 12 10 13 2"/></svg>`,
  },
  {
    id: 'tab-memory',
    label: () => t('modelSettings.tabMemory'),
    icon: `<svg width="16" height="16" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round" stroke-linejoin="round"><path d="M21 16V8a2 2 0 0 0-1-1.73l-7-4a2 2 0 0 0-2 0l-7 4A2 2 0 0 0 3 8v8a2 2 0 0 0 1 1.73l7 4a2 2 0 0 0 2 0l7-4A2 2 0 0 0 21 16z"/><polyline points="3.27 6.96 12 12.01 20.73 6.96"/><line x1="12" y1="22.08" x2="12" y2="12"/></svg>`,
  },
  {
    id: 'tab-gpu',
    label: () => t('modelSettings.tabGpu'),
    icon: `<svg width="16" height="16" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round" stroke-linejoin="round"><polygon points="12 2 2 7 12 12 22 7 12 2"/><polyline points="2 17 12 22 22 17"/><polyline points="2 12 12 17 22 12"/></svg>`,
  },
  {
    id: 'tab-context',
    label: () => t('modelSettings.tabContext'),
    icon: `<svg width="16" height="16" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round" stroke-linejoin="round"><rect x="2" y="9" width="20" height="6" rx="1"/><line x1="6" y1="9" x2="6" y2="12"/><line x1="10" y1="9" x2="10" y2="13"/><line x1="14" y1="9" x2="14" y2="12"/><line x1="18" y1="9" x2="18" y2="13"/></svg>`,
  },
  {
    id: 'tab-advanced',
    label: () => t('modelSettings.tabAdvanced'),
    icon: `<svg width="16" height="16" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round" stroke-linejoin="round"><path d="M14.7 6.3a1 1 0 0 0 0 1.4l1.6 1.6a1 1 0 0 0 1.4 0l3.77-3.77a6 6 0 0 1-7.94 7.94l-6.91 6.91a2.12 2.12 0 0 1-3-3l6.91-6.91a6 6 0 0 1 7.94-7.94l-3.76 3.76z"/></svg>`,
  },
]

// ─── Validation ───────────────────────────────────────────────────────────────────
/** Current set of validation errors */
const errors = ref<Set<string>>(new Set())

function validate(): boolean {
  const errs = new Set<string>()
  if (cfg.ctxSize <= 0) errs.add('ctxSize')
  if (cfg.batchSize <= 0) errs.add('batchSize')
  if (cfg.ubatchSize <= 0) errs.add('ubatchSize')
  errors.value = errs
  return errs.size === 0
}

const hasErrors = computed(() => errors.value.size > 0)

/** Whether any field differs from its initial value */
const isModified = computed(() => {
  for (const key of Object.keys(defaults) as (keyof ModelConfig)[]) {
    if (cfg[key] !== initialConfig.value[key]) return true
  }
  return false
})

// Re-validate whenever a field changes
watch(
  () => ({ ...cfg }),
  () => {
    validate()
    saveSuccess.value = false
  },
  { deep: true }
)

// ─── Route linkage: reload when modelName changes ────────────────────────────────
watch(modelName, () => {
  loadConfig()
  // The weights term of the memory estimate follows the model too
  lookupModelSize()
})

// ─── Load / Reset / Save ──────────────────────────────────────────────────────
async function loadConfig() {
  loading.value = true
  loadError.value = ''
  saveError.value = ''
  saveSuccess.value = false
  // Tune/bench feedback from the previous model must not leak into a reloaded page
  clearTuneTimer()
  clearTuneFeedback()
  clearBenchTimer()
  clearBenchFeedback()
  activeTab.value = 'tab-base'

  const name = modelName.value
  if (!name) {
    loadError.value = 'Missing model name'
    loading.value = false
    return
  }

  try {
    const remote = await getModelConfig(name)
    const merged: ModelConfig = { ...defaults, ...(remote || {}) }
    // Reset the reactive cfg with the merged values (keeps the reactive reference)
    for (const key of Object.keys(defaults) as (keyof ModelConfig)[]) {
      ;(cfg as any)[key] = merged[key]
    }
    initialConfig.value = { ...merged }
    errors.value = new Set()
  } catch (e: any) {
    loadError.value = e?.message || String(e)
  } finally {
    loading.value = false
  }
}

function reset() {
  // Fall back to initialConfig (values from the last load/save), keeping the reactive reference
  for (const key of Object.keys(defaults) as (keyof ModelConfig)[]) {
    ;(cfg as any)[key] = initialConfig.value[key]
  }
  errors.value = new Set()
  saveSuccess.value = false
  saveError.value = ''
}

async function save() {
  if (!validate()) return
  saving.value = true
  saveError.value = ''
  saveSuccess.value = false
  // Starting a save clears the tune/bench feedback (single visible message line)
  clearTuneTimer()
  clearTuneFeedback()
  clearBenchTimer()
  clearBenchFeedback()
  try {
    await saveModelConfig(modelName.value, { ...cfg })
    // Update initialConfig after a successful save; isModified becomes false and the save button disables
    initialConfig.value = { ...cfg }
    saveSuccess.value = true
    setTimeout(() => {
      router.back()
    }, 300)
  } catch (e: any) {
    saveError.value = e?.message || String(e)
  } finally {
    saving.value = false
  }
}

/**
 * Auto-tune: the backend reads the GGUF metrics + hardware snapshot, computes
 * and persists the optimal params, and returns the applied config. The form is
 * refreshed in place (same merge as loadConfig) and initialConfig mirrors the
 * persisted state, so isModified stays false and the tuned values are visible
 * immediately.
 */
async function tune() {
  if (tuning.value) return
  tuning.value = true
  // Starting a tune clears the save feedback (single visible message line)
  saveSuccess.value = false
  saveError.value = ''
  clearTuneTimer()
  clearTuneFeedback()
  try {
    const tuned = (await tuneModelConfig(modelName.value)) as ModelConfig
    // Merge exactly like loadConfig: defaults fill missing fields. Object.assign
    // mutates the reactive cfg in place (reference kept), so the deep watcher
    // re-runs validation.
    const merged: ModelConfig = { ...defaults, ...(tuned || {}) }
    Object.assign(cfg, merged)
    // The backend already persisted this config, so the snapshot advances the
    // same way save() does on success: isModified is false right after tuning.
    initialConfig.value = { ...merged }
    errors.value = new Set()
    tuneMsg.value = t(tunedToastKey(merged.gpuLayers), tunedSummaryParams(merged))
    tuneTimer = setTimeout(clearTuneFeedback, 5000)
  } catch (e) {
    tuneError.value = t('models.tuneError', { msg: e instanceof Error ? e.message : String(e) })
    tuneTimer = setTimeout(clearTuneFeedback, 5000)
  } finally {
    tuning.value = false
  }
}

/**
 * Deep benchmark: the backend runs llama-bench (installed next to llama-server)
 * with the model's saved config, loading the full model — minutes-scale, so
 * the button is busy-guarded and mutual exclusion with Tune is enforced via
 * the disabled bindings. The result is display-only (not persisted).
 */
async function bench() {
  if (benching.value) return
  benching.value = true
  // Starting a bench clears the save/tune feedback (single visible message line)
  saveSuccess.value = false
  saveError.value = ''
  clearTuneTimer()
  clearTuneFeedback()
  clearBenchTimer()
  clearBenchFeedback()
  try {
    const r = await benchmarkModel(modelName.value)
    benchMsg.value = t('modelSettings.benched', {
      tps: r.tgTps.toFixed(1),
      ngl: r.ngl,
      threads: r.threads,
    })
    benchTimer = setTimeout(clearBenchFeedback, 8000)
  } catch (e) {
    benchError.value = t('modelSettings.benchError', {
      msg: e instanceof Error ? e.message : String(e),
    })
    benchTimer = setTimeout(clearBenchFeedback, 8000)
  } finally {
    benching.value = false
  }
}

// ─── Lifecycle ────────────────────────────────────────────────────────────────
onMounted(async () => {
  loadConfig()
  try {
    const status = await getServerStatus()
    serverRunning.value = status.running
  } catch {}
  // Best-effort hardware snapshot for the phone summary island sub line
  try {
    const info = await getSystemInfo()
    sysRamGb.value = Number(info?.memory?.totalGb) || 0
    sysCpuModel.value = String(info?.cpu?.model || '')
    // Tablet summary estimate inputs: total / free memory in bytes
    sysTotalBytes.value = (Number(info?.memory?.totalGb) || 0) * GIB
    sysFreeBytes.value = (Number(info?.memory?.freeGb) || 0) * GIB
  } catch {}
  // Best-effort weights term for the tablet memory estimate
  lookupModelSize()
})

// Pending tune/bench feedback timers must not fire after the page is left
onUnmounted(() => {
  clearTuneTimer()
  clearBenchTimer()
})
</script>

<style scoped>
.page {
  padding: 0 48px 60px;
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
  font-family: inherit;
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

/* ─── Tabs ─── */
.settings-tabs {
  display: flex;
  gap: 4px;
  padding: 0;
  border-bottom: 1px solid var(--border);
  margin-bottom: 0;
  flex-shrink: 0;
}

.tab-btn {
  display: flex;
  align-items: center;
  gap: 6px;
  padding: 10px 14px;
  background: none;
  border: none;
  border-bottom: 2px solid transparent;
  margin-bottom: -1px;
  color: var(--text-muted);
  font-size: 13px;
  font-weight: 500;
  font-family: inherit;
  cursor: pointer;
  transition: color 0.15s, border-color 0.15s;
}

.tab-btn:hover {
  color: var(--text-secondary);
}

.tab-btn.active {
  color: var(--accent);
  border-bottom-color: var(--accent);
  font-weight: 600;
}

.tab-btn .tab-icon {
  display: flex;
}

/* ─── Action area ─── */
.settings-actions {
  display: flex;
  align-items: center;
  gap: 10px;
  padding: 16px 0;
  border-bottom: 1px solid var(--border);
  margin-bottom: 20px;
}

.action-msg {
  font-size: 13px;
  font-weight: 600;
  color: var(--success);
}

.action-err {
  color: #ef4444;
}

.action-spacer {
  flex: 1;
}

/* Restart note shown while the service is running: saved params only apply on next start */
.restart-note {
  margin: -8px 0 20px;
  padding: 8px 12px;
  background: rgba(245, 158, 11, 0.08);
  border: 1px solid rgba(245, 158, 11, 0.25);
  border-radius: var(--radius-sm);
  color: #fbbf24;
  font-size: 12px;
  font-weight: 500;
}

.btn-secondary {
  padding: 8px 20px;
  background: transparent;
  border: 1px solid var(--border);
  border-radius: var(--radius-sm);
  color: var(--text-muted);
  font-size: 13px;
  font-weight: 600;
  font-family: inherit;
  cursor: pointer;
  transition: all 0.15s;
}

.btn-secondary:hover {
  background: var(--hover-bg);
}

/* Tune / bench buttons: icon + label; the icon spins while the backend
 * computes the plan or runs llama-bench */
.tune-btn,
.bench-btn {
  display: inline-flex;
  align-items: center;
  gap: 6px;
}

.tune-btn:disabled,
.bench-btn:disabled {
  cursor: wait;
}

.tune-btn.busy svg,
.bench-btn.busy svg {
  animation: tune-spin 1s linear infinite;
}

@keyframes tune-spin {
  to { transform: rotate(360deg); }
}

.btn-primary {
  padding: 8px 24px;
  background: var(--accent);
  border: none;
  border-radius: var(--radius-sm);
  color: #fff;
  font-size: 13px;
  font-weight: 600;
  font-family: inherit;
  cursor: pointer;
  transition: opacity 0.15s;
}

.btn-primary:hover:not(:disabled) {
  opacity: 0.85;
}

.btn-primary:disabled {
  opacity: 0.5;
  cursor: not-allowed;
}

/* ─── Body ─── */
.settings-body {
  /* tabs preserve entered values; no extra container needed */
}

.loading-placeholder {
  padding: 48px 0;
  text-align: center;
  color: var(--text-dim);
  font-size: 14px;
}

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
  color: var(--danger);
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
  font-family: inherit;
}

.retry-btn:hover {
  background: rgba(99, 102, 241, 0.2);
}

/* ─── Parameter cards (ported from original modal, scoped to this page) ─── */
.param-group {
  background: var(--bg-card);
  border: 1px solid var(--border-light);
  border-radius: var(--radius-md);
  padding: 16px 18px;
  margin-bottom: 16px;
}

.group-title {
  font-size: 13px;
  font-weight: 700;
  color: var(--text-secondary);
  margin: 0 0 14px;
}

.param-grid {
  display: grid;
  grid-template-columns: 1fr 1fr;
  gap: 14px 18px;
}

.param-grid.col-1 {
  grid-template-columns: 1fr;
}

.param-field {
  display: block;
}

.param-label {
  display: block;
  font-size: 13px;
  font-weight: 600;
  color: var(--text-secondary);
  margin-bottom: 6px;
  cursor: pointer;
}

.param-input {
  width: 100%;
  padding: 8px 10px;
  background: var(--bg-primary);
  border: 1px solid var(--border);
  border-radius: var(--radius-sm);
  color: var(--text-primary);
  font-size: 13px;
  font-family: inherit;
  outline: none;
  transition: border-color 0.15s, box-shadow 0.15s;
}

.param-input:hover {
  border-color: var(--overlay-20);
}

.param-input:focus {
  border-color: var(--accent);
  box-shadow: none;
}

.param-input::placeholder {
  color: var(--text-dim);
}

.param-hint {
  margin-top: 6px;
  font-size: 12px;
  color: var(--text-muted);
  line-height: 1.5;
}

/* Pill toggle switch */
.toggle-row {
  display: flex;
  align-items: center;
  justify-content: space-between;
  gap: 12px;
  padding: 12px 14px;
  background: var(--surface);
  border: 1px solid var(--border-light);
  border-radius: var(--radius-sm);
}

.toggle-text {
  display: flex;
  flex-direction: column;
  gap: 2px;
  font-size: 13px;
  font-weight: 600;
  color: var(--text-secondary);
}

.toggle-sub {
  font-size: 11px;
  font-weight: 400;
  color: var(--text-dim);
  line-height: 1.5;
}

.switch {
  position: relative;
  display: inline-block;
  width: 40px;
  height: 22px;
  flex-shrink: 0;
}

.switch input {
  opacity: 0;
  width: 0;
  height: 0;
}

.slider {
  position: absolute;
  cursor: pointer;
  inset: 0;
  background: var(--overlay-20);
  border-radius: 999px;
  transition: background 0.2s;
}

.slider:before {
  content: "";
  position: absolute;
  width: 16px;
  height: 16px;
  left: 3px;
  top: 3px;
  background: #fff;
  border-radius: 50%;
  transition: transform 0.2s;
}

.switch input:checked + .slider {
  background: var(--accent);
}

.switch input:checked + .slider:before {
  transform: translateX(18px);
}

.switch input:focus-visible + .slider {
  outline: 2px solid var(--accent);
  outline-offset: 2px;
}

/* ─── Tablet summary rows (draft ⑪⑫ "参数速览"): shared by the portrait
       page-bottom island and the landscape sticky rail — one rows builder
       (lib/modelMemory.ts), two placements. Both DOM nodes are tier-gated
       v-ifs, so these base styles never apply on phone / desktop. ─── */
.summary-row {
  display: flex;
  align-items: baseline;
  justify-content: space-between;
  gap: 10px;
  padding: 7px 0;
  border-bottom: 1px dashed var(--border);
  font-size: 12.5px;
  min-width: 0;
}

.summary-row:last-child {
  border-bottom: none;
}

.summary-label {
  color: var(--text-secondary);
  flex-shrink: 0;
}

.summary-value {
  color: var(--text-primary);
  font-weight: 600;
  text-align: right;
  word-break: break-word;
}

.summary-value--mono {
  font-family: var(--font-mono);
}

.summary-value--ok {
  color: #10b981;
}

.summary-value--warn {
  color: #f59e0b;
}

.summary-value--error {
  color: #ef4444;
}

/* ─── Phone + tablet portrait (<=1099px, design draft frames ⑪⑫): the header becomes the mockup
       .mstop — a 44px circular island back button (chevron only) in its own
       row, then the model name on a single ellipsized line, then the muted
       sub-line; the tab row becomes scrollable pill chips (active = gradient);
       the four actions share ONE equal-flex row; tune/bench results render as
       the green toast block; input rows reflow into the .frow layout (label
       left, mono input right); the memory tab gains the live "参数速览"
       summary island.
       Track A (tablet draft frames ⑪⑫ portrait) widens this band from the
       phone's 767px to the full 768..1099 tablet-portrait band — the draft
       portrait frames are exactly the phone anatomy at tablet width. The
       summary island DOM differs by tier: phone renders its in-tab island
       (isMobile v-if, unchanged), tablet portrait renders the page-bottom
       island (showPortraitSummary v-if); both share the .summary-island
       styles below. ─── */
@media (max-width: 1099px) {
  /* Frame ⑪ .mstop .bk: 44px circular surface button with the island shadow
     (mockup .bk uses 36px; 44px keeps the app-wide touch-target floor) */
  .back-btn {
    width: 44px;
    height: 44px;
    padding: 0;
    margin-bottom: 10px;
    justify-content: center;
    border-radius: 50%;
    background: var(--bg-card);
    box-shadow: var(--shadow-island);
    color: var(--text-secondary);
  }

  /* Icon-only on phone: the visible text label is hidden (the button's
     aria-label keeps the accessible name) */
  .back-btn .back-label {
    display: none;
  }

  /* Frame ⑪ .mstop .t: 17px/800 model name, one line with an ellipsis so
     long GGUF names cannot push the header band wider */
  .page-title {
    font-size: 17px;
    font-weight: 800;
    letter-spacing: -0.3px;
    margin-bottom: 2px;
    white-space: nowrap;
    overflow: hidden;
    text-overflow: ellipsis;
  }

  /* Frame ⑪ .mstop .s: 11.5px muted sub-line ("模型推理参数") */
  .page-subtitle {
    font-size: 11.5px;
    color: var(--text-muted);
  }

  /* Frame ⑪⑫ .mtabs: inactive = surface chips, active = gradient pill with
     the purple glow; the row scrolls horizontally, never the page */
  .settings-tabs {
    gap: 8px;
    overflow-x: auto;
    scrollbar-width: none;
    border-bottom: none;
    padding-bottom: 14px;
  }

  .settings-tabs::-webkit-scrollbar {
    display: none;
  }

  .tab-btn {
    flex-shrink: 0;
    padding: 8px 14px;
    background: rgba(120, 124, 160, 0.12);
    border: none;
    border-bottom: none;
    margin-bottom: 0;
    border-radius: 999px;
    color: var(--text-muted);
    font-size: 12.5px;
    font-weight: 700;
  }

  .tab-btn .tab-icon {
    display: none;
  }

  .tab-btn.active {
    background: var(--grad);
    color: #fff;
    border-bottom-color: transparent;
    box-shadow: none;
  }

  /* Frame ⑪ .actbar: ONE row of 4 equal-flex buttons (radius 14, 13/700);
     the feedback message rides its own wrapped line above */
  .settings-actions {
    flex-wrap: wrap;
    gap: 8px;
  }

  .action-spacer {
    display: none;
  }

  .btn-secondary,
  .btn-primary {
    flex: 1 1 0;
    min-width: 0;
    min-height: 44px;
    padding: 11px 0;
    border: none;
    border-radius: var(--r-md);
    font-size: 13px;
    font-weight: 700;
    text-align: center;
    justify-content: center;
  }

  .btn-secondary {
    background: var(--hover-bg);
    color: var(--text-secondary);
  }

  .btn-secondary:hover {
    background: var(--overlay-8);
  }

  /* ✨ 一键调优: the row's leading gradient action */
  .tune-btn {
    background: var(--grad);
    color: #fff;
    box-shadow: none;
  }

  .tune-btn:hover {
    background: var(--grad);
  }

  /* 保存: gradient while dirty, neutral wash while disabled */
  .btn-primary:not(:disabled) {
    background: var(--grad);
    color: #fff;
    box-shadow: none;
  }

  .btn-primary:hover:not(:disabled) {
    background: var(--grad);
    opacity: 0.92;
  }

  .btn-primary:disabled {
    background: var(--hover-bg);
    color: var(--text-dim);
    opacity: 1;
  }

  /* Frame ⑪ .toast: tune/bench results as the green block; errors keep the
     red variant of the same shape */
  .action-msg {
    display: flex;
    align-items: center;
    gap: 8px;
    flex-basis: 100%;
    padding: 10px 14px;
    border-radius: var(--r-sm);
    background: var(--success-bg);
    color: var(--success);
    font-size: 12px;
    font-weight: 700;
  }

  html[data-theme='dark'] .action-msg {
    background: #12261f;
    color: #6ee7b7;
  }

  .action-err {
    background: #fdecec;
    color: #ef4444;
  }

  html[data-theme='dark'] .action-err {
    background: var(--danger-bg);
    color: #f87171;
  }

  /* Frame ⑪ .frow / .formcard: hairline-separated rows; plain input rows
     (.param-frow) reserve a 110px input zone and absolutely position the
     field on the row's right, vertically centered */
  .param-grid,
  .param-grid.col-1 {
    display: flex;
    flex-direction: column;
    gap: 0;
  }

  .param {
    padding: 12px 0;
    border-bottom: 1px solid var(--border);
  }

  .param:last-child {
    border-bottom: none;
  }

  .param-label {
    margin-bottom: 0;
    font-size: 13px;
    font-weight: 600;
  }

  .param-hint {
    margin-top: 2px;
    font-size: 11.5px;
  }

  .param-frow {
    position: relative;
    padding-right: 122px;
  }

  .param-frow .param-input {
    position: absolute;
    right: 0;
    top: 50%;
    transform: translateY(-50%);
    width: 110px;
    padding: 8px 12px;
    text-align: right;
    font-family: var(--font-mono);
    background: var(--bg-card);
    border: 1px solid var(--border-light);
    border-radius: var(--r-sm);
  }

  /* Frame ⑫ summary island: live parameter readout + hardware basis sub */
  .summary-island {
    background: var(--bg-secondary);
    border: 1px solid var(--border);
    border-radius: var(--r-md);
    box-shadow: var(--shadow-island);
    padding: 16px 18px;
    margin-bottom: 16px;
  }

  .summary-title {
    font-size: 13px;
    font-weight: 700;
    color: var(--text-secondary);
    margin: 0 0 8px;
  }

  .summary-body {
    font-size: 12.5px;
    line-height: 1.7;
    color: var(--text-secondary);
    margin: 0;
  }

  .summary-sub {
    font-size: 11.5px;
    color: var(--text-dim);
    margin: 4px 0 0;
  }

  /* Frame ⑪ .notify: icon + text share one amber row */
  .restart-note {
    display: flex;
    align-items: center;
    gap: 8px;
  }

  .restart-note .note-icon {
    flex-shrink: 0;
  }

  .retry-btn {
    min-height: 44px;
  }
}

</style>
