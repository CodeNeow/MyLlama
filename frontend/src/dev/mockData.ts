/**
 * Mock preview data layer (dev only, wired in exclusively by the
 * `--mode mock` Vite alias in vite.config.ts → src/dev/mockRuntime.ts).
 *
 * Holds the whole mutable fake backend state (config / system probe / models /
 * server / downloads / docs) and the handler table keyed by the bare binding
 * method name. Shapes mirror the generated binding classes in
 * frontend/bindings/github.com/CodeNeow/MyLlama/core/models.ts, which
 * in turn mirror the Go structs in core/. Data is tuned for the Android phone
 * layout walkthrough: OS = android/arm64, GPU list empty (android probe is
 * unsupported → no GPU/CUDA cards on Home), tray / API-route / serving-GPU
 * settings hidden, update section in link mode.
 */

// ─── Small helpers ───────────────────────────────────────────────────────────

/** Promise-based delay. */
function sleep(ms: number): Promise<void> {
  return new Promise((resolve) => setTimeout(resolve, ms))
}

/** Symmetric random jitter around a base value, clamped at 0. */
function jitter(base: number, amp: number): number {
  return Math.max(0, base + (Math.random() * 2 - 1) * amp)
}

/** GiB → bytes. */
const GiB = 1024 * 1024 * 1024

// ─── Config (mirrors the persisted llama-desktop-config.json) ────────────────

const config: Record<string, any> = {
  theme: 'light',
  llamaCppDir: 'C:\\Users\\demo\\llama.cpp',
  modelsDir: 'C:\\Users\\demo\\LLM-Models',
  llamaCppDownloadDir: 'C:\\Users\\demo\\llama.cpp-dl',
  modelDownloadDir: 'C:\\Users\\demo\\model-downloads',
  downloadSource: 'hf',
  language: 'zh',
  resolvedLanguage: 'zh',
  trayEnabled: false,
  apiRouteMode: false,
  sidebarCollapsed: false,
  onboardingDismissed: true,
}

// ─── System probes (android semantics: no GPU/CUDA, CPU-only accel) ─────────

// Walkthrough persona: the default preview is the Android phone layout (?sc=
// desktop switches to a Windows desktop persona so the desktop-gated UI can be
// exercised in the mock too).
const os = new URLSearchParams(window.location.search).get('sc') === 'desktop'
  ? { os: 'windows', arch: 'x86_64' }
  : { os: 'android', arch: 'arm64' }

const cpu = { model: 'Mock Snapdragon 8 Gen 3', cores: 8, logicalCpus: 8 }

const memory = { totalGb: 16, freeGb: 9.4 }

// Android: gpuProbesUnsupported → always empty, no GPU/CUDA cards on Home.
const gpus: any[] = []

const cuda = { available: false, driverVersion: '', toolkitVersion: '' }

const llamaCpp = {
  installed: true,
  path: 'C:\\Users\\demo\\llama.cpp\\llama-server',
  version: 'b5913',
  cudartInstalled: false,
  cudartVersion: '',
  // Android probe cannot detect the accel backend (no DLLs to inspect), so it
  // reports '' and the UI falls back to the platform build guess
  // (cpuArm64 → "加速构建 · cpuArm64（NEON）", design draft frame ③).
  accel: '',
}

const disk = { path: 'C:\\', used: 231 * GiB, total: 512 * GiB }

const systemInfo = {
  os: os.os,
  arch: os.arch,
  cpu,
  memory,
  gpu: gpus,
  cuda,
  llamaCpp,
  disk,
}

// ─── Local models (download dir + external import dir) ───────────────────────

const IMPORT_DIR = 'C:\\Users\\demo\\Imported-Models'

const models: any[] = [
  {
    author: '',
    name: 'Qwen3-4B-Instruct-Q4_K_M',
    path: 'C:\\Users\\demo\\model-downloads\\Qwen3-4B-Instruct-Q4_K_M.gguf',
    sizeBytes: 2_476_000_000,
    sizeHuman: '2.3 GB',
    architecture: 'qwen3',
    quantization: 'Q4_K_M',
    // One vision-capable entry so `npm run dev:mock` exercises the chat page's
    // mmproj gating (attach enabled) and My Models' badge instead of a flat all-false list
    hasMmproj: true,
    sourceDir: config.modelDownloadDir,
    alias: 'Qwen3-4B-Instruct-Q4_K_M',
  },
  {
    author: '',
    name: 'Qwen2.5-Coder-7B-Instruct-Q4_K_M',
    path: 'C:\\Users\\demo\\model-downloads\\Qwen2.5-Coder-7B-Instruct-Q4_K_M.gguf',
    sizeBytes: 4_689_000_000,
    sizeHuman: '4.4 GB',
    architecture: 'qwen2',
    quantization: 'Q4_K_M',
    hasMmproj: false,
    sourceDir: config.modelDownloadDir,
    alias: 'Qwen2.5-Coder-7B-Instruct-Q4_K_M',
  },
  {
    author: '',
    name: 'Llama-3.2-3B-Instruct-Q5_K_M',
    path: IMPORT_DIR + '\\Llama-3.2-3B-Instruct-Q5_K_M.gguf',
    sizeBytes: 2_291_000_000,
    sizeHuman: '2.1 GB',
    architecture: 'llama',
    quantization: 'Q5_K_M',
    hasMmproj: false,
    sourceDir: IMPORT_DIR,
    alias: 'Llama-3.2-3B-Instruct-Q5_K_M',
  },
  {
    author: '',
    name: 'TinyLlama-1.1B-Chat-v1.0-Q8_0',
    path: IMPORT_DIR + '\\TinyLlama-1.1B-Chat-v1.0-Q8_0.gguf',
    sizeBytes: 1_146_000_000,
    sizeHuman: '1.1 GB',
    architecture: 'llama',
    quantization: 'Q8_0',
    hasMmproj: false,
    sourceDir: IMPORT_DIR,
    alias: 'TinyLlama-1.1B-Chat-v1.0-Q8_0',
  },
]

// ─── Server state (running on startup so the API page / TaskDock have data) ──

const serverConfig: Record<string, any> = {
  accessMode: 'local',
  host: '127.0.0.1',
  port: 8080,
  maxModels: 4,
  cacheRam: 8192,
  apiKey: '',
  deviceId: '',
}

let serverRunning = true
// Wall-clock anchor for the uptime counter: the service "started" ~97 min ago.
let serverStartedAt = Date.now() - 97 * 60 * 1000
// Android direct-mode resident model (single in-memory model).
let residentModel = 'Qwen3-4B-Instruct-Q4_K_M'

let loadedModels: any[] = [{ id: residentModel, type: 'chat', status: 'loaded' }]

// Server log ring (mirrors core/server.go: monotonic seq + capped lines).
const serverLogsCap = 2000
let logSeq = 0
const serverLogRing: { seq: number; text: string }[] = []

function addServerLog(text: string): void {
  logSeq += 1
  serverLogRing.push({ seq: logSeq, text })
  if (serverLogRing.length > serverLogsCap) serverLogRing.shift()
}

// Initial startup transcript, [INFO]-prefixed like the real capture.
addServerLog(
  '[INFO] Starting llama-server: C:\\Users\\demo\\llama.cpp\\llama-server --host 127.0.0.1 --port 8080 ' +
    '--max-models 4 --cache-ram 8192',
)
addServerLog('main: llama-server version b5913 (mock)')
addServerLog('main: build = cpu-arm64 (mock android build)')
addServerLog('main: system memory used = 2810.4 MiB')
addServerLog('main: listening on http://127.0.0.1:8080')
addServerLog('srv  update_slots: all slots are idle')

function startServerLogs(): void {
  addServerLog(
    '[INFO] Starting llama-server: C:\\Users\\demo\\llama.cpp\\llama-server --host 127.0.0.1 --port 8080 ' +
      '--max-models 4 --cache-ram 8192',
  )
  addServerLog('main: llama-server version b5913 (mock)')
  addServerLog('main: listening on http://127.0.0.1:8080')
  addServerLog('srv  update_slots: all slots are idle')
}

// ─── Monitor sample (jitters per call so the API page chart draws a curve) ───
// memUsed/memTotal are BYTES (uint64 in core/monitor.go; the Home memory card
// divides by GiB for display), so GB-scale magnitudes must be converted —
// raw GB floats would render as "0.0 /0 GB".

function monitorStatus(): Record<string, any> {
  if (!serverRunning) {
    return {
      cpuPercent: jitter(6, 4),
      memUsed: Math.round(jitter(3.1, 0.3) * GiB),
      memTotal: memory.totalGb * GiB,
      gpus,
      serverRunning: false,
      promptTps: 0,
      decodeTps: 0,
      uptimeSeconds: 0,
      disk,
    }
  }
  return {
    cpuPercent: Math.round(jitter(34, 12) * 10) / 10,
    memUsed: Math.round(jitter(6.9, 0.4) * GiB),
    memTotal: memory.totalGb * GiB,
    gpus,
    serverRunning: true,
    promptTps: Math.round(jitter(118, 35) * 10) / 10,
    decodeTps: Math.round(jitter(15, 3) * 10) / 10,
    uptimeSeconds: Math.floor((Date.now() - serverStartedAt) / 1000),
    disk,
  }
}

// ─── Model download tasks (progress advances with the wall clock) ────────────

interface MockTask {
  id: string
  modelId: string
  fileName: string
  destDir: string
  source: string
  status: string
  total: number
  base: number
  since: number
  speed: number
  error: string
  queuedAt: number
}

const tasks: MockTask[] = [
  {
    id: 'task-qwen3-8b',
    modelId: 'Qwen/Qwen3-8B-GGUF',
    fileName: 'Qwen3-8B-Q4_K_M.gguf',
    destDir: config.modelDownloadDir,
    source: 'hf',
    status: 'downloading',
    total: 6_200_000_000,
    base: Math.floor(6_200_000_000 * 0.45),
    since: Date.now(),
    speed: 9_500_000,
    error: '',
    queuedAt: 0,
  },
  {
    id: 'task-tinyllama-done',
    modelId: 'TinyLlama/tinyllama-1.1b-chat-v1.0-GGUF',
    fileName: 'tinyllama-1.1b-chat-v1.0.Q8_0.gguf',
    destDir: config.modelDownloadDir,
    source: 'hf',
    status: 'done',
    total: 1_146_000_000,
    base: 1_146_000_000,
    since: 0,
    speed: 0,
    error: '',
    queuedAt: 0,
  },
]

/** Lazily advance a running task: wall-clock bytes since the last resume, flipping to done at 100%. */
function taskDownloaded(t: MockTask): number {
  if (t.status === 'downloading') {
    const bytes = Math.min(t.total, t.base + ((Date.now() - t.since) / 1000) * t.speed)
    if (bytes >= t.total) {
      t.status = 'done'
      t.base = t.total
      t.speed = 0
      return t.total
    }
    return bytes
  }
  return t.base
}

/** Promote tasks that sat in 'queued' for over 1.5s to 'downloading'. */
function promoteQueued(t: MockTask): void {
  if (t.status === 'queued' && Date.now() - t.queuedAt > 1500) {
    t.status = 'downloading'
    t.since = Date.now()
  }
}

function taskJson(t: MockTask): Record<string, any> {
  promoteQueued(t)
  const downloaded = Math.floor(taskDownloaded(t))
  return {
    id: t.id,
    modelId: t.modelId,
    fileName: t.fileName,
    destDir: t.destDir,
    source: t.source,
    status: t.status,
    progress: t.total > 0 ? Math.round((downloaded / t.total) * 100) : 0,
    total: t.total,
    downloaded,
    sizeHuman: (t.total / GiB).toFixed(1) + ' GB',
    speed: t.status === 'downloading' ? t.speed : 0,
    error: t.error,
  }
}

// ─── llama.cpp runtime download state ────────────────────────────────────────

const llamacppDl = {
  status: 'idle',
  paused: false,
  total: 0,
  base: 0,
  since: 0,
  version: '',
  error: '',
}

// Simulated download rate: the rtdl scenario slows the transfer down so the
// "downloading" walkthrough state stays on screen (~55s to completion at the
// 45% start) instead of flipping to done within seconds.
function llamacppDlRate(): number {
  return scenario === 'rtdl' ? 400_000 : 6_400_000
}

function llamacppDownloaded(): number {
  if (llamacppDl.status === 'downloading') {
    const bytes = Math.min(llamacppDl.total, llamacppDl.base + ((Date.now() - llamacppDl.since) / 1000) * llamacppDlRate())
    if (bytes >= llamacppDl.total) {
      llamacppDl.status = 'done'
      llamacppDl.base = llamacppDl.total
      return llamacppDl.total
    }
    return bytes
  }
  return llamacppDl.base
}

function llamacppState(): Record<string, any> {
  const downloaded = Math.floor(llamacppDownloaded())
  return {
    status: llamacppDl.status,
    paused: llamacppDl.paused,
    progress: llamacppDl.total > 0 ? Math.round((downloaded / llamacppDl.total) * 100) : 0,
    total: llamacppDl.total,
    downloaded,
    fileName: llamacppDl.version ? 'llama-cpp-android-arm64-' + llamacppDl.version + '.zip' : '',
    version: llamacppDl.version,
    error: llamacppDl.error,
  }
}

// ─── Search catalog (HF-shaped results for the Downloads tab) ────────────────

function gguf(name: string, size: number): Record<string, any> {
  return { rfilename: name, size }
}

const searchCatalog: Record<string, any>[] = [
  {
    id: 'Qwen/Qwen3-8B-GGUF',
    modelId: 'Qwen/Qwen3-8B-GGUF',
    author: 'Qwen',
    downloads: 128_400,
    likes: 2_310,
    pipelineTag: 'text-generation',
    tags: ['gguf', 'text-generation', 'qwen3'],
    siblings: [
      gguf('Qwen3-8B-Q4_K_M.gguf', 6_200_000_000),
      gguf('Qwen3-8B-Q5_K_M.gguf', 7_350_000_000),
      gguf('Qwen3-8B-Q8_0.gguf', 8_900_000_000),
      gguf('README.md', 18_400),
    ],
  },
  {
    id: 'Qwen/Qwen3-4B-Instruct-2507-GGUF',
    modelId: 'Qwen/Qwen3-4B-Instruct-2507-GGUF',
    author: 'Qwen',
    downloads: 96_800,
    likes: 1_540,
    pipelineTag: 'text-generation',
    tags: ['gguf', 'text-generation', 'qwen3'],
    siblings: [
      gguf('Qwen3-4B-Instruct-2507-Q4_K_M.gguf', 2_476_000_000),
      gguf('Qwen3-4B-Instruct-2507-Q8_0.gguf', 4_310_000_000),
      gguf('README.md', 15_100),
    ],
  },
  {
    id: 'bartowski/Llama-3.2-3B-Instruct-GGUF',
    modelId: 'bartowski/Llama-3.2-3B-Instruct-GGUF',
    author: 'bartowski',
    downloads: 210_300,
    likes: 3_120,
    pipelineTag: 'text-generation',
    tags: ['gguf', 'llama-3', 'instruct'],
    siblings: [
      gguf('Llama-3.2-3B-Instruct-Q4_K_M.gguf', 2_028_000_000),
      gguf('Llama-3.2-3B-Instruct-Q5_K_M.gguf', 2_291_000_000),
      gguf('Llama-3.2-3B-Instruct-Q8_0.gguf', 3_412_000_000),
      gguf('README.md', 9_800),
    ],
  },
  {
    id: 'google/gemma-2-2b-it-GGUF',
    modelId: 'google/gemma-2-2b-it-GGUF',
    author: 'google',
    downloads: 88_900,
    likes: 1_050,
    pipelineTag: 'text-generation',
    tags: ['gguf', 'gemma', 'instruction-tuned'],
    siblings: [
      gguf('gemma-2-2b-it-Q4_K_M.gguf', 1_672_000_000),
      gguf('gemma-2-2b-it-Q8_0.gguf', 2_700_000_000),
      gguf('README.md', 12_600),
    ],
  },
  {
    id: 'microsoft/Phi-4-mini-instruct-GGUF',
    modelId: 'microsoft/Phi-4-mini-instruct-GGUF',
    author: 'microsoft',
    downloads: 64_500,
    likes: 890,
    pipelineTag: 'text-generation',
    tags: ['gguf', 'phi', 'mini'],
    siblings: [
      gguf('Phi-4-mini-instruct-Q4_K_M.gguf', 2_487_000_000),
      gguf('Phi-4-mini-instruct-Q8_0.gguf', 3_980_000_000),
      gguf('README.md', 11_200),
    ],
  },
  {
    id: 'deepseek-ai/DeepSeek-R1-Distill-Qwen-7B-GGUF',
    modelId: 'deepseek-ai/DeepSeek-R1-Distill-Qwen-7B-GGUF',
    author: 'deepseek-ai',
    downloads: 173_900,
    likes: 2_860,
    pipelineTag: 'text-generation',
    tags: ['gguf', 'reasoning', 'distill'],
    siblings: [
      gguf('DeepSeek-R1-Distill-Qwen-7B-Q4_K_M.gguf', 4_689_000_000),
      gguf('DeepSeek-R1-Distill-Qwen-7B-Q8_0.gguf', 8_120_000_000),
      gguf('README.md', 22_900),
    ],
  },
]

function catalogEntry(modelID: string): Record<string, any> | undefined {
  const key = modelID.toLowerCase()
  return (
    searchCatalog.find((e) => e.id.toLowerCase() === key) ??
    searchCatalog.find((e) => e.id.toLowerCase().includes(key) || key.includes(e.id.toLowerCase().split('/')[1] ?? ''))
  )
}

// ─── Per-model inference configs ─────────────────────────────────────────────

const defaultModelConfig: Record<string, any> = {
  threads: 8,
  gpuLayers: '',
  ctxSize: 4096,
  batchSize: 2048,
  ubatchSize: 2048,
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
}

const modelConfigs: Record<string, Record<string, any>> = {}

// ─── Chat SSE sample answer (POST /v1/chat/completions via the mock runtime) ─
// Language-aware demo replies, written in the voice of a local model so the
// mock chat reads naturally in walkthrough captures of either locale.

const chatReplyZh =
  '你好！我是一个运行在本机 llama-server 上的本地模型，我们的对话完全在这台设备上完成，不会上传任何数据。' +
  '有什么想了解的，直接继续提问就好。'

const chatReplyEn =
  "Hi! I'm a local model running on a llama-server instance on this machine. " +
  'Everything we discuss stays on your device and is never uploaded. ' +
  'Feel free to continue with any question you like.'

// Reasoning prelude streamed BEFORE the answer when the ?sc=chatthink scenario
// is active (design frame ⑥: collapsible thinking block + auto collapse).
const chatThinkReasoning =
  '用户在 16GB 内存的手机上运行 4B Q4 模型，权重约 2.3GB，需要为 KV 缓存预留足够的空间。' +
  '上下文从 4096 起步比较稳妥；若系统仍有富余，再逐步加倍并观察内存水位。'

// ─── Walkthrough scenarios (?sc=…, dev-only) ─────────────────────────────────
// The phone-layout walkthrough (docs/branding/android-design.html
// frames) needs states the interactive UI cannot reach quickly (fresh
// first-use, mid-download runtime, blocked chat). A `sc` query param
// reshuffles the initial fake-backend state at module load. This module is
// only wired in by `vite --mode mock` (see vite.config.ts), so production
// builds never read the query string.

const scenario = new URLSearchParams(window.location.search).get('sc') ?? ''

// Companion of ?sc=: ?th=dark boots the mock config with the dark theme so
// walkthrough captures of the dark rail survive page reloads.
const themeParam = new URLSearchParams(window.location.search).get('th') ?? ''

function applyScenario(name: string): void {
  if (themeParam === 'dark') {
    config.theme = 'dark'
  }
  if (name === 'firstuse') {
    // Frame ①: fresh install — no runtime, no models, service stopped,
    // quick-start checklist not dismissed.
    serverRunning = false
    llamaCpp.installed = false
    llamaCpp.version = ''
    llamaCpp.accel = ''
    models.length = 0
    config.onboardingDismissed = false
    tasks.length = 0
  } else if (name === 'rtdl' || name === 'rtpause' || name === 'rtextract' || name === 'rtfail') {
    // Frame ④: runtime not installed + the download card in one of its
    // non-idle states (downloading / paused / extracting / failed).
    serverRunning = false
    llamaCpp.installed = false
    llamaCpp.version = ''
    config.onboardingDismissed = true
    tasks.length = 0
    llamacppDl.total = 40_200_000
    llamacppDl.version = 'b5913'
    if (name === 'rtdl') {
      llamacppDl.status = 'downloading'
      llamacppDl.base = Math.floor(40_200_000 * 0.45)
      llamacppDl.since = Date.now()
    } else if (name === 'rtpause') {
      llamacppDl.status = 'paused'
      llamacppDl.paused = true
      llamacppDl.base = Math.floor(40_200_000 * 0.62)
    } else if (name === 'rtextract') {
      llamacppDl.status = 'extracting'
      llamacppDl.base = 40_200_000
    } else {
      llamacppDl.status = 'error'
      llamacppDl.error = 'download failed: mock scenario (network reset)'
    }
  } else if (name === 'chatnomodels') {
    // Frame ⑦: no local models — gray model chip, 📦 empty state, blocked
    // composer; tapping send surfaces the guided precheck notice.
    serverRunning = false
    models.length = 0
    config.onboardingDismissed = true
    tasks.length = 0
  } else if (name === 'chatstarting') {
    // Frame ⑦ top card: StartServerWithModel stalls ~8s so the "starting"
    // notice stays on screen long enough to capture.
    serverRunning = false
    config.onboardingDismissed = true
    tasks.length = 0
  } else if (name === 'chatthink') {
    // Frame ⑥: service running (default state) + the chat stream grows a
    // reasoning prelude (see mockChatThinkParts).
  } else if (name === 'dock') {
    // Frame ⑲: runtime download resumed at ~63% alongside the default
    // downloading model task and the resident in-memory model.
    llamacppDl.status = 'downloading'
    llamacppDl.total = 58_000_000
    llamacppDl.base = Math.floor(58_000_000 * 0.63)
    llamacppDl.since = Date.now()
    llamacppDl.version = 'b5913'
  }
}

applyScenario(scenario)

// ─── Handler table (keyed by bare binding method name) ───────────────────────

export const handlers: Record<string, (...args: any[]) => any> = {
  // ── Config ──
  GetConfig: () => ({ ...config }),
  SetTheme: (theme: string) => {
    config.theme = theme
  },
  SetSidebarCollapsed: (collapsed: boolean) => {
    config.sidebarCollapsed = collapsed
  },
  SetOnboardingDismissed: (dismissed: boolean) => {
    config.onboardingDismissed = dismissed
  },
  SetTrayEnabled: (enabled: boolean) => {
    config.trayEnabled = enabled
  },
  SetApiRouteMode: (enabled: boolean) => {
    config.apiRouteMode = enabled
  },
  SetLanguage: (language: string) => {
    if (language !== 'zh' && language !== 'en' && language !== 'auto') {
      throw new Error('invalid language: ' + language)
    }
    config.language = language
    config.resolvedLanguage = language === 'en' ? 'en' : 'zh'
    return config.resolvedLanguage
  },

  // ── System info ──
  GetSystemInfo: () => ({ ...systemInfo }),
  GetCPU: () => ({ ...cpu }),
  GetMemory: () => ({ ...memory }),
  GetGPU: () => [...gpus],
  GetCUDA: () => ({ ...cuda }),
  GetLlamaCpp: () => ({ ...llamaCpp }),
  GetOS: () => ({ ...os }),
  // Android system-bar insets: the mock preview has no native bars, so the
  // safe-area bridge stays at the env() CSS baseline (all zeros).
  GetSafeArea: () => ({ top: 0, bottom: 0, left: 0, right: 0 }),
  GetDisk: () => ({ ...disk }),

  // ── Monitor ──
  GetMonitorStatus: () => monitorStatus(),

  // ── Models ──
  GetModels: () => models.map((m) => ({ ...m })),
  RefreshModels: async () => {
    await sleep(300)
    return models.map((m) => ({ ...m }))
  },
  GetModelConfig: (modelID: string) => ({ ...defaultModelConfig, ...(modelConfigs[modelID] ?? {}) }),
  SaveModelConfig: (modelID: string, cfg: any) => {
    modelConfigs[modelID] = { ...defaultModelConfig, ...modelConfigs[modelID], ...cfg }
  },
  TuneModelConfig: (modelID: string) => {
    const tuned = {
      ...defaultModelConfig,
      threads: 8,
      gpuLayers: '999',
      ctxSize: 8192,
      batchSize: 512,
      ubatchSize: 256,
      flashAttn: true,
      cacheTypeK: 'q8_0',
      cacheTypeV: 'q8_0',
      cpuMoe: true,
      loadMode: 'mmap',
    }
    modelConfigs[modelID] = tuned
    return { ...tuned }
  },
  BenchmarkModel: async () => {
    await sleep(2000)
    return { tgTps: 8.4, ngl: '999', threads: 0, usedCpuMoe: true, elapsedS: 74.6 }
  },

  // ── Server ──
  GetServerConfig: () => ({ ...serverConfig }),
  SaveServerConfig: (cfg: any) => {
    Object.assign(serverConfig, cfg)
  },
  GetServerStatus: () => ({ running: serverRunning, log: serverLogRing.map((e) => e.text) }),
  GetServerLogsSince: (since: number) => ({
    entries: serverLogRing.filter((e) => e.seq >= since).map((e) => ({ ...e })),
    next: logSeq + 1,
  }),
  StartServer: () => {
    if (serverRunning) return
    serverRunning = true
    serverStartedAt = Date.now()
    loadedModels = [{ id: residentModel, type: 'chat', status: 'loaded' }]
    startServerLogs()
  },
  StartServerWithModel: async (model: string) => {
    // chatstarting scenario: stall the bring-up so the "starting service"
    // notice stays visible for the walkthrough capture.
    if (scenario === 'chatstarting') await sleep(30000)
    if (!serverRunning) {
      serverRunning = true
      serverStartedAt = Date.now()
      residentModel = model
      loadedModels = [{ id: model, type: 'chat', status: 'loaded' }]
      addServerLog('[INFO] direct mode: starting llama-server for model "' + model + '"')
      startServerLogs()
      return
    }
    if (residentModel === model) {
      addServerLog('[INFO] direct mode: model "' + model + '" already resident, start is a no-op')
      return
    }
    addServerLog('[INFO] direct mode: switching model "' + residentModel + '" -> "' + model + '", restarting llama-server')
    residentModel = model
    loadedModels = [{ id: model, type: 'chat', status: 'loaded' }]
    serverStartedAt = Date.now()
    startServerLogs()
  },
  StopServer: () => {
    if (!serverRunning) return
    addServerLog('[INFO] Stopping llama-server...')
    serverRunning = false
    loadedModels = []
    addServerLog('[INFO] llama-server stopped')
  },

  // ── llama.cpp runtime download ──
  GetLlamaCppDownloadStatus: () => llamacppState(),
  StartLlamaCppDownload: () => {
    llamacppDl.status = 'downloading'
    llamacppDl.paused = false
    llamacppDl.total = 58_000_000
    llamacppDl.base = 0
    llamacppDl.since = Date.now()
    llamacppDl.version = 'b5913'
    llamacppDl.error = ''
  },
  PauseLlamaCppDownload: () => {
    if (llamacppDl.status !== 'downloading') return
    llamacppDl.base = llamacppDownloaded()
    llamacppDl.status = 'paused'
    llamacppDl.paused = true
  },
  ResumeLlamaCppDownload: () => {
    if (llamacppDl.status !== 'paused') return
    llamacppDl.status = 'downloading'
    llamacppDl.paused = false
    llamacppDl.since = Date.now()
  },
  StopLlamaCppDownload: () => {
    if (llamacppDl.status !== 'downloading' && llamacppDl.status !== 'paused') return
    llamacppDl.status = 'cancelled'
    llamacppDl.paused = false
  },

  // ── Directory pickers (resolve empty = cancelled dialog) ──
  BrowseLlamaCppDir: async () => {
    await sleep(300)
    return ''
  },
  BrowseLlamaCppDownloadDir: async () => {
    await sleep(300)
    return ''
  },
  BrowseModelsDir: async () => {
    await sleep(300)
    return ''
  },
  BrowseModelDownloadDir: async () => {
    await sleep(300)
    return ''
  },

  // ── App update ──
  GetAppVersion: () => 'v0.3.9-mock',
  CheckForUpdate: () => ({ hasUpdate: false, version: 'v0.3.9-mock', notes: '', published: '' }),
  StartUpdateDownload: () => {},
  GetUpdateDownloadStatus: () => ({
    status: 'idle',
    progress: 0,
    total: 0,
    downloaded: 0,
    version: '',
    filePath: '',
    error: '',
    kind: 'setup',
    installer: false,
  }),
  StopUpdateDownload: () => {},
  InstallUpdate: () => {},

  // ── Downloads (search / files / tasks) ──
  SearchDownloads: async (query: string, filter: string) => {
    await sleep(600)
    void query
    return searchCatalog.map((entry) => ({
      ...entry,
      tags: [...entry.tags, ...(filter ? [filter] : [])],
      siblings: entry.siblings.map((s: any) => ({ ...s })),
    }))
  },
  GetModelFiles: async (modelID: string) => {
    await sleep(250)
    const entry = catalogEntry(modelID)
    if (entry) return entry.siblings.map((s: any) => ({ filename: s.rfilename, size: s.size }))
    const base = modelID.split('/').pop() ?? modelID
    return [
      { filename: base + '-Q4_K_M.gguf', size: 4_690_000_000 },
      { filename: base + '-Q8_0.gguf', size: 7_650_000_000 },
      { filename: 'README.md', size: 12_000 },
    ]
  },
  GetModelMaxFileSize: (modelID: string) => {
    const entry = catalogEntry(modelID)
    if (!entry) return 4_690_000_000
    return entry.siblings.reduce((max: number, s: any) => Math.max(max, s.size), 0)
  },
  GetModelDescription: () =>
    'Mock preview model card: a compact instruction-tuned LLM distilled for on-device inference. ' +
    'Quantized GGUF weights trade a little accuracy for a much smaller memory footprint, ' +
    'so mid-range phones can serve it through llama-server.',
  StartDownload: (modelID: string, files: string[]) => {
    if (!files || files.length === 0) {
      throw new Error('no files selected for download')
    }
    const entry = catalogEntry(modelID)
    const ggufs = entry ? entry.siblings.filter((s: any) => s.rfilename.endsWith('.gguf')) : []
    const total =
      ggufs.find((s: any) => files.includes(s.rfilename))?.size ??
      files.length * 4_690_000_000
    const first = files[0]
    tasks.push({
      id: 'task-' + Date.now(),
      modelId: modelID,
      fileName: first,
      destDir: config.modelDownloadDir,
      source: config.downloadSource,
      status: 'queued',
      total,
      base: 0,
      since: 0,
      speed: 8_200_000,
      error: '',
      queuedAt: Date.now(),
    })
  },
  GetDownloadTasks: () => tasks.map(taskJson),
  CancelDownloadTask: (id: string) => {
    const t = tasks.find((x) => x.id === id)
    if (!t) return
    if (t.status === 'downloading') t.base = taskDownloaded(t)
    t.status = 'cancelled'
    t.speed = 0
  },
  RetryDownloadTask: (id: string) => {
    const t = tasks.find((x) => x.id === id)
    if (!t) return
    if (t.status === 'downloading' || t.status === 'paused') return
    t.status = 'downloading'
    t.base = 0
    t.since = Date.now()
    t.error = ''
  },
  PauseDownloadTask: (id: string) => {
    const t = tasks.find((x) => x.id === id)
    if (!t || t.status !== 'downloading') return
    t.base = taskDownloaded(t)
    t.status = 'paused'
  },
  ResumeDownloadTask: (id: string) => {
    const t = tasks.find((x) => x.id === id)
    if (!t || t.status !== 'paused') return
    t.status = 'downloading'
    t.since = Date.now()
  },

  // ── Download source ──
  GetDownloadSource: () => config.downloadSource,
  SetDownloadSource: (source: string) => {
    if (source !== 'hf' && source !== 'huggingface' && source !== 'modelscope') {
      throw new Error('invalid download source: ' + source)
    }
    config.downloadSource = source
  },

  // ── Router models (TaskDock in-memory list) ──
  GetLoadedModels: () => (serverRunning ? loadedModels.map((m) => ({ ...m })) : []),
  UnloadModel: (id: string) => {
    if (!serverRunning) {
      throw new Error('server is not running')
    }
    if (!id) {
      throw new Error('invalid model id')
    }
    loadedModels = loadedModels.filter((m) => m.id !== id)
    addServerLog('[INFO] unloading model "' + id + '"')
  },

  // ── Remote docs (degrade to the bundled tutorial) ──
  GetRemoteDoc: () => ({ text: '', source: 'none', fetchedAt: '' }),

  // ── Directory setters (frontend callers exist only via future paths; cover anyway) ──
  SetLlamaCppDownloadDir: (dir: string) => {
    if (!dir) throw new Error('empty directory')
    config.llamaCppDownloadDir = dir
  },
  SetModelDownloadDir: (dir: string) => {
    if (!dir) throw new Error('empty directory')
    config.modelDownloadDir = dir
  },
  SetModelsDir: (dir: string) => {
    if (!dir) throw new Error('empty directory')
    config.modelsDir = dir
  },
}

/** Current mock llama-server port (the fetch interceptor matches on this). */
export function mockServerPort(): number {
  return serverConfig.port
}

/** Whether the mock llama-server is running (drives the fetch mock behavior). */
export function mockServerRunning(): boolean {
  return serverRunning
}

/** Loaded chat models, shared with the fetch interceptor's /models listing. */
export function mockLoadedModels(): { id: string; status: string }[] {
  return loadedModels.map((m) => ({ id: m.id, status: m.status }))
}

/** Register one mock-produced server log line (used by the chat fetch mock). */
export function mockAddServerLog(text: string): void {
  addServerLog(text)
}

/** The canned streamed chat reply (used by the chat fetch mock); zh or en per browser language. */
export function mockChatReply(): string {
  return navigator.language.toLowerCase().startsWith('zh') ? chatReplyZh : chatReplyEn
}

/**
 * Reasoning prelude for the ?sc=chatthink scenario (design frame ⑥): streamed
 * as `reasoning_content` deltas BEFORE the answer; null in every other
 * scenario so the default chat behavior is unchanged.
 */
export function mockChatThinkParts(): { reasoning: string; content: string } | null {
  return scenario === 'chatthink' ? { reasoning: chatThinkReasoning, content: mockChatReply() } : null
}
