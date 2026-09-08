import { ref, reactive, watch } from 'vue'

/** A single chat message. */
export interface ChatMessage {
  role: 'user' | 'assistant'
  content: string
  /** Image data URLs attached to this message (in-memory only; stripped on persist to keep localStorage small) */
  images?: string[]
  /** Accumulated thinking text (assistant only; small enough to persist alongside content) */
  reasoning?: string
  /** Per-phase token rates of the streamed reply (assistant only; persisted for history) */
  stats?: { reasoningTps?: number; answerTps?: number }
}

/** Chat sampling parameters and system prompt. */
export interface ChatParams {
  temperature: number    // Sampling temperature, default 0.8
  topP: number           // Default 0.95
  topK: number           // Default 40
  repeatPenalty: number  // Default 1.1
  maxTokens: number      // Max tokens to generate; -1 means unlimited (llama-server n_predict -1)
  systemPrompt: string   // System prompt; empty means not injected
}

/** localStorage key for persisted messages (follows store.ts llama-desktop- prefix convention) */
const MESSAGES_KEY = 'llama-desktop-chat-messages'
/** localStorage key for the selected model */
const MODEL_KEY = 'llama-desktop-chat-model'
/** localStorage key for chat parameters */
const CHAT_PARAMS_KEY = 'llama-desktop-chat-params'

/** Max number of conversation messages; the oldest are dropped beyond this cap */
const DEFAULT_CAP = 200

/** Default chat parameters. */
const DEFAULT_CHAT_PARAMS: ChatParams = {
  temperature: 0.8,
  topP: 0.95,
  topK: 40,
  repeatPenalty: 1.1,
  maxTokens: -1,
  systemPrompt: '',
}

/**
 * Keep only the most recent `cap` messages.
 *
 * Module-level state survives page switches; localStorage survives restarts;
 * high-frequency streaming deltas only touch memory and are not flushed per token.
 */
export function capMessages(msgs: ChatMessage[], cap = DEFAULT_CAP): ChatMessage[] {
  if (msgs.length <= cap) return msgs
  return msgs.slice(msgs.length - cap)
}

/**
 * What reconcileSelectedModel decided for the persisted selection.
 * - 'kept': the stored model is still available (or there was nothing to fix).
 * - 'switched': the stored model is gone — the first available one takes over.
 * - 'cleared': no models are available at all — the selection is cleared so
 *   the UI shows the guided "download a model first" state.
 */
export type ReconcileAction = 'kept' | 'switched' | 'cleared'

/**
 * Reconcile the persisted selected model against the server's available models.
 *
 * A stored ID still present is kept as-is; a stale ID (model renamed/removed
 * while the app was closed) falls back to the first available model so send()
 * cannot hit "model not found"; an empty available list (no scanned models)
 * clears the stored choice instead of keeping an id the picker cannot resolve
 * (#33). Pure: persistence and user notices are the caller's job.
 */
export function reconcileSelectedModel(
  stored: string,
  available: string[]
): { model: string; changed: boolean; action: ReconcileAction } {
  if (available.includes(stored)) {
    return { model: stored, changed: false, action: 'kept' }
  }
  if (available.length > 0) {
    return { model: available[0], changed: true, action: 'switched' }
  }
  return { model: '', changed: stored !== '', action: 'cleared' }
}

// ─── Phone params-sheet pure helpers (design frame ⑤) ─────────────────────
// The phone tier renders sampling params as sliders / steppers instead of the
// desktop number inputs. The clamp / step / display math lives here so it is
// unit-testable without a component mount; the sheet in Chat.vue only wires
// events into these functions and persists through the existing deep watch.

/** Round a value onto a step grid and clamp it into [min, max]. */
export function clampStep(value: number, min: number, max: number, step: number): number {
  if (!Number.isFinite(value) || !Number.isFinite(min) || !Number.isFinite(max) || !Number.isFinite(step) || step <= 0) {
    return Math.min(min, max)
  }
  const snapped = Math.round(value / step) * step
  // Kill the binary-float dust a step multiply can produce (1.1 + 0.05 etc.);
  // the decimal count of the step literal is the rounding latitude.
  const stepStr = String(step)
  const dot = stepStr.indexOf('.')
  const decimals = dot < 0 ? 0 : stepStr.length - dot - 1
  const fixed = Number(snapped.toFixed(decimals))
  const lo = Math.min(min, max)
  const hi = Math.max(min, max)
  return Math.min(Math.max(fixed, lo), hi)
}

/** One stepper press: ± step from the current value, clamped into range. */
export function stepNumber(current: number, dir: -1 | 1, min: number, max: number, step: number): number {
  return clampStep(current + dir * step, min, max, step)
}

/** Sentinel meaning "no limit" (llama-server n_predict -1). */
export const MAX_TOKENS_UNLIMITED = -1
/** Lowest finite max-tokens value the stepper offers. */
export const MAX_TOKENS_MIN = 256
/** Highest finite max-tokens value the stepper offers. */
export const MAX_TOKENS_MAX = 32768
/** Stepper increment for finite max-tokens values. */
export const MAX_TOKENS_STEP = 256
/** Value a "+" press lands on when leaving the unlimited state. */
export const MAX_TOKENS_UNLIMITED_ENTER = 4096

/**
 * Max-tokens stepper with the unlimited (-1) sentinel: "+" from unlimited
 * enters at MAX_TOKENS_UNLIMITED_ENTER; a step below MIN maps back to
 * unlimited instead of clamping (so "-" at the minimum toggles the sentinel).
 * Documented mapping choice: the lowest finite value and the unlimited state
 * are one press apart in both directions.
 */
export function stepMaxTokens(current: number, dir: -1 | 1): number {
  const base =
    current < MAX_TOKENS_MIN && dir > 0 ? MAX_TOKENS_UNLIMITED_ENTER : current + dir * MAX_TOKENS_STEP
  if (base < MAX_TOKENS_MIN) return MAX_TOKENS_UNLIMITED
  return clampStep(base, MAX_TOKENS_MIN, MAX_TOKENS_MAX, MAX_TOKENS_STEP)
}

/** Whether a max-tokens value renders as the localized "unlimited" label. */
export function isUnlimitedMaxTokens(value: number): boolean {
  return !Number.isFinite(value) || value < MAX_TOKENS_MIN
}

/** 0..100 slider-track fill percent for a value within [min, max]. */
export function sliderFillPercent(value: number, min: number, max: number): number {
  if (!Number.isFinite(value) || !Number.isFinite(min) || !Number.isFinite(max) || max <= min) {
    return 0
  }
  const pct = ((value - min) / (max - min)) * 100
  return Math.min(Math.max(pct, 0), 100)
}

/** Slider readout format: fixed two decimals ("0.80"). */
export function formatParamValue(value: number): string {
  return Number.isFinite(value) ? value.toFixed(2) : (0).toFixed(2)
}

// ─── Module-level state (survives component unmount) ──────────────────────

/** Current conversation messages. */
export const messages = ref<ChatMessage[]>([])

/** Currently selected model ID. */
export const selectedModel = ref('')

/**
 * Whether a streaming generation is currently in progress.
 *
 * Hoisted to module level so a page remount during streaming still reads true;
 * send()'s `if (streaming.value) return` guard keeps working, preventing
 * concurrent streams whose onDelta closures interleave into the same bubble.
 */
export const streaming = ref(false)

/**
 * Mutable holder for the current streaming AbortController.
 *
 * Hoisted to module level so stop() can abort an in-flight stream after a page
 * remount; a component-local abortController would be lost on unmount and the
 * stop button would stop working.
 */
export const chatAbortController = { current: null as AbortController | null }

/** Chat sampling parameters (reactive; auto-persisted on change via watch) */
export const chatParams = reactive<ChatParams>({ ...DEFAULT_CHAT_PARAMS })

// ─── Persistence: restore on startup ──────────────────────────────────────

/** Restore chat history from localStorage on startup; silently fall back to empty when missing or corrupt. */
export function loadChatHistory(): void {
  try {
    const raw = localStorage.getItem(MESSAGES_KEY)
    if (raw) {
      const parsed = JSON.parse(raw) as ChatMessage[]
      messages.value = capMessages(parsed)
    }
  } catch {
    // Corrupt or non-JSON; silently fall back to empty
    messages.value = []
  }

  try {
    const model = localStorage.getItem(MODEL_KEY)
    if (model !== null) {
      selectedModel.value = model
    }
  } catch {
    // Silent fallback
  }
}

/** Restore chat params from localStorage on startup; silently fall back to defaults when missing or corrupt. */
export function loadChatParams(): void {
  try {
    const raw = localStorage.getItem(CHAT_PARAMS_KEY)
    if (raw) {
      const parsed = JSON.parse(raw) as Partial<ChatParams>
      // Merge over defaults so newly added fields keep a fallback; valid fields override the defaults
      chatParams.temperature = typeof parsed.temperature === 'number' ? parsed.temperature : DEFAULT_CHAT_PARAMS.temperature
      chatParams.topP = typeof parsed.topP === 'number' ? parsed.topP : DEFAULT_CHAT_PARAMS.topP
      chatParams.topK = typeof parsed.topK === 'number' ? parsed.topK : DEFAULT_CHAT_PARAMS.topK
      chatParams.repeatPenalty = typeof parsed.repeatPenalty === 'number' ? parsed.repeatPenalty : DEFAULT_CHAT_PARAMS.repeatPenalty
      chatParams.maxTokens = typeof parsed.maxTokens === 'number' ? parsed.maxTokens : DEFAULT_CHAT_PARAMS.maxTokens
      chatParams.systemPrompt = typeof parsed.systemPrompt === 'string' ? parsed.systemPrompt : DEFAULT_CHAT_PARAMS.systemPrompt
    }
  } catch {
    // Corrupt or non-JSON; silently fall back to defaults
  }
}

// Restore immediately on first module load (same pattern as store.ts readStoredTheme module-level init)
loadChatHistory()
loadChatParams()

// ─── Persistence: flush on demand ─────────────────────────────────────────

/**
 * Write current messages + selectedModel to localStorage.
 *
 * Caller contract (avoids high-frequency I/O):
 * - after the user clicks "send"
 * - when streaming ends or errors (send's finally)
 * - when the conversation is cleared
 *
 * Not called during onDelta streaming; only the last in-memory message is mutated.
 */
export function persistChat(): void {
  try {
    // Strip images on persist (base64 payloads would bloat localStorage); text, reasoning and stats survive restarts
    const toStore = capMessages(messages.value).map(({ images: _images, ...rest }) => rest)
    localStorage.setItem(MESSAGES_KEY, JSON.stringify(toStore))
  } catch {
    // Silently ignore localStorage write failures; chat keeps working
  }
  try {
    if (selectedModel.value) {
      localStorage.setItem(MODEL_KEY, selectedModel.value)
    } else {
      localStorage.removeItem(MODEL_KEY)
    }
  } catch {
    // Silent fallback
  }
}

/**
 * Write current chatParams to localStorage.
 *
 * Persisted automatically via a deep watch: any field change flushes to disk,
 * so nothing is lost across page switches or refreshes. Panel edits during
 * streaming only affect the next send (the in-flight request already locked a
 * buildChatBody snapshot).
 */
export function persistChatParams(): void {
  try {
    localStorage.setItem(CHAT_PARAMS_KEY, JSON.stringify(chatParams))
  } catch {
    // Silent fallback
  }
}

// Auto-persist whenever any chatParams field changes
watch(
  chatParams,
  () => {
    persistChatParams()
  },
  { deep: true }
)
