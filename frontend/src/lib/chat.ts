/**
 * Chat page pure functions and a thin fetch wrapper.
 *
 * Conventions:
 * - Pure functions (parseSSEChunks, buildChatBody, tokenRates) are unit-test friendly, no network/IO;
 * - The fetch wrapper talks to the local llama-server directly instead of going through
 *   Wails bindings, because streaming reads are required;
 * - Port and running state are obtained by the caller (Chat.vue) via getServerConfig / getServerStatus.
 */

/** Router model entry (only the fields the frontend cares about) */
export interface RouterModel {
  id: string
  status: string
}

/** Chat sampling parameters (kept in sync with chatState.ChatParams to avoid a hard dependency of this lib on chatState) */
export interface ChatParams {
  temperature: number
  topP: number
  topK: number
  repeatPenalty: number
  maxTokens: number
  systemPrompt: string
}

/** Chat-send readiness verdict: ok to send, or the missing prerequisite the user must fix first. */
export type ChatReadiness = 'ok' | 'needModels' | 'needRuntime'

/**
 * Decide what is missing before a chat send can auto-start the service.
 *
 * Runtime missing takes precedence: without llama-server even a stocked model
 * directory cannot serve, so the guidance always points at the deepest blocker.
 * Pure: the caller fetches the model count and the runtime install state.
 */
export function chatReadiness(modelCount: number, runtimeInstalled: boolean): ChatReadiness {
  if (!runtimeInstalled) return 'needRuntime'
  if (modelCount < 1) return 'needModels'
  return 'ok'
}

/**
 * Whether a model can take image input. True when the local scan found a
 * sibling mmproj file (hasMmproj), OR when the model settings carry an explicit
 * mmproj override: ModelSettings lets users point at a projector file manually,
 * and the backend preset then injects that explicit path even when the
 * same-directory scan reports no mmproj — so the override must count as vision
 * capability here too. Pure.
 */
export function isVisionCapable(hasMmproj: boolean, mmprojOverride: string | undefined | null): boolean {
  if (hasMmproj) return true
  return typeof mmprojOverride === 'string' && mmprojOverride.trim() !== ''
}

/**
 * Resolve the config-persistence key for the selected model. Per-model configs
 * are keyed by the model's display Name on the backend: core/app.go's
 * GetModelConfig reads cachedModelConfigs[modelID], and both consumers index
 * cfgs[m.Name] (core/bridge.go direct mode, core/preset.go's router preset
 * writer) — ModelSettings.vue likewise loads/saves by model.name. The chat
 * picker's value is the router id (alias || name), so an INI-sanitized or
 * deduplicated alias would MISS the config store and lose the explicit mmproj
 * override. Resolve the record with the same identity rule the picker uses
 * ((m.alias || m.name) === selectedId) and return its Name, falling back to
 * the selected id itself when the list has no match. Pure.
 */
export function visionConfigKey(models: { name: string; alias?: string }[], selectedId: string): string {
  const hit = models.find((m) => (m.alias || m.name) === selectedId)
  return hit?.name || selectedId
}

/**
 * Classify a raw llama-server error message. 'vision-unsupported' matches the
 * known rejection a text-only model produces when a chat request carries image
 * parts ("image input is not supported - hint: ... may need to provide the
 * mmproj ...", or any message naming mmproj), so the chat page can map it to
 * actionable bilingual guidance instead of the raw English text. Pure.
 */
export function chatErrorKind(raw: string): 'vision-unsupported' | null {
  const msg = (raw || '').toLowerCase()
  if (msg.includes('image input is not supported') || msg.includes('mmproj')) {
    return 'vision-unsupported'
  }
  return null
}

/**
 * Determine which currently loaded models must be unloaded so the selected
 * model becomes the only resident: exactly the entries whose status is
 * 'loaded' (llama-server's router status string) excluding the selected id.
 * 'loading' entries are left alone (one may be the incoming load) and
 * 'sleeping' entries are already out of memory. Pure.
 */
export function modelsToUnload(loaded: { id: string; status: string }[], selected: string): string[] {
  return loaded
    .filter((m) => m.status === 'loaded' && m.id !== selected)
    .map((m) => m.id)
}

/**
 * Direct-mode (Android) resident check: true when the selected model is NOT
 * among the currently resident ids, so serving the request needs a model
 * switch — on Android that means restarting the service with the new model
 * as the single resident (startServerWithModel → backend bridge). Callers
 * probe GET /models (lib fetchRouterModels, direct-mode fallback included)
 * and pass the resident ids; an empty list means "nothing resident" and
 * therefore always needs the switch. Pure.
 */
export function directModeNeedsSwitch(loadedIds: string[], selected: string): boolean {
  return !loadedIds.includes(selected)
}

// ─── Chat params layout (design draft tablet frame ⑤) ───────────────────────
// Derivable presentation decision for the inference-params editor, kept out
// of the component so it is unit-testable without a mount.

/**
 * Which surface renders the inference-params editor (draft frame ⑤):
 * - 'sheet':  phone tier — docked bottom sheet (existing Teleport);
 * - 'modal':  tablet portrait — centered 560px modal card on a dim backdrop
 *             (NOT the phone's full-width bottom sheet);
 * - 'popover': desktop — anchored popover (unchanged).
 * Pure: the caller derives the two tier flags from the platform state.
 * (The former 'panel' landscape rail variant was removed with the
 * Android-tablet dedicated layouts: tablet landscape now uses the desktop
 * layout directly — see lib/layout.ts viewportMetaContent.)
 */
export type ChatParamsLayout = 'popover' | 'sheet' | 'modal'

export function chatParamsLayout(isMobileTier: boolean, isTabletTier: boolean): ChatParamsLayout {
  if (isMobileTier) return 'sheet'
  if (isTabletTier) return 'modal'
  return 'popover'
}

/** Structural minimum of a chat message for estimation (chatState.ChatMessage-compatible). */
export interface TokenEstimateMessage {
  role: string
  content: string
}

/** Minimal stats shape of a finished reply (chatState ChatMessage.stats-compatible). */
interface ReplyStatsLike {
  reasoningTps?: number
  answerTps?: number
}

interface ReplyMessageLike {
  role: string
  stats?: ReplyStatsLike
}

/**
 * Rough token estimate of one text, for the context rail's "≈ 1.2k" style
 * readout (draft B⑥). Display-only heuristic: CJK characters cost ~0.75
 * tokens each, everything else ~0.25 per character (~4 chars/token). Pure.
 */
export function estimateChatTokens(text: string): number {
  if (!text) return 0
  const cjk = text.match(/[\u3000-\u9fff\uf900-\ufaff]/g)?.length ?? 0
  const other = text.length - cjk
  return Math.ceil(cjk * 0.75 + other / 4)
}

/** Sum of {@link estimateChatTokens} over a conversation. Pure. */
export function estimateConversationTokens(messages: readonly TokenEstimateMessage[]): number {
  return messages.reduce((sum, m) => sum + estimateChatTokens(m.content), 0)
}

/**
 * tok/s of the most recent finished reply (answer rate preferred, reasoning
 * rate as fallback); null when the history has no stats yet. Pure.
 */
export function lastReplyTps(messages: readonly ReplyMessageLike[]): number | null {
  for (let i = messages.length - 1; i >= 0; i--) {
    const m = messages[i]
    if (m.role !== 'assistant' || !m.stats) continue
    if (m.stats.answerTps !== undefined) return m.stats.answerTps
    if (m.stats.reasoningTps !== undefined) return m.stats.reasoningTps
  }
  return null
}

/** Compact token-count readout: "832" below 1000, "1.2k" above. Pure. */
export function formatTokenCount(tokens: number): string {
  if (!Number.isFinite(tokens) || tokens <= 0) return '0'
  if (tokens < 1000) return String(Math.round(tokens))
  return `${(tokens / 1000).toFixed(1)}k`
}

/** Inputs for the context summary card (draft frames B⑤/B⑥/B⑦). */
export interface ChatContextInput {
  /** llama-server is up and serving. */
  serverRunning: boolean
  /** A chat-initiated bring-up is in progress. */
  starting: boolean
  /** A precheck blocker (start error) is on screen — draft B⑦ state. */
  blocked: boolean
  /** A reply is currently streaming. */
  streaming: boolean
  /** Display label of the selected model; '' = none picked. */
  modelLabel: string
  /** Live tok/s while streaming; null otherwise. */
  liveTps: number | null
  /** Finished history: last-reply rate + token estimate + counts. */
  messages: readonly (ReplyMessageLike & TokenEstimateMessage)[]
}

/** Structured facts the context summary card renders (lib stays i18n-free). */
export interface ChatContextSummary {
  service: 'running' | 'starting' | 'down'
  modelLabel: string
  /** Live speed while streaming, else the last reply's rate, else null. */
  speedTps: number | null
  messageCount: number
  /** Rough conversation token estimate (draft B⑥ "≈ 1.2k"). */
  estimatedTokens: number
  /** Show the "start the service and retry" hint row (draft B⑦). */
  suggestRestart: boolean
  /** Whether the context card renders at all: idle + empty history degrades
   * to the params-only rail of draft B⑤. */
  showContextCard: boolean
}

export function chatContextSummary(input: ChatContextInput): ChatContextSummary {
  const service = input.serverRunning ? 'running' : input.starting ? 'starting' : 'down'
  return {
    service,
    modelLabel: input.modelLabel,
    speedTps: input.liveTps ?? lastReplyTps(input.messages),
    messageCount: input.messages.length,
    estimatedTokens: estimateConversationTokens(input.messages),
    suggestRestart: input.blocked && !input.serverRunning && !input.starting,
    showContextCard: input.messages.length > 0 || input.blocked || input.streaming,
  }
}

/** Context-rail row identity (drives the component's i18n label lookup). */
export type ChatContextRowKey = 'service' | 'model' | 'speed' | 'suggestion'

/** Visual tone of a row value (green ok / purple busy / red error / neutral). */
export type ChatContextRowTone = 'ok' | 'busy' | 'error' | 'plain'

export interface ChatContextRow {
  key: ChatContextRowKey
  value: string
  tone: ChatContextRowTone
}

/** Localized strings + number formatting supplied by the component. */
export interface ChatContextCopy {
  serviceRunning: string
  serviceStarting: string
  serviceDown: string
  noModel: string
  noSpeed: string
  formatTps: (tps: number) => string
  suggestion: string
}

/**
 * Render the context card's rows from a summary: service state (draft B⑦'s
 * red 未运行), model label, live/last speed (draft B⑥), and the guided
 * "start the service and retry" row only when blocked (draft B⑦). Pure.
 */
export function chatContextRows(summary: ChatContextSummary, copy: ChatContextCopy): ChatContextRow[] {
  const rows: ChatContextRow[] = [
    {
      key: 'service',
      value:
        summary.service === 'running'
          ? copy.serviceRunning
          : summary.service === 'starting'
            ? copy.serviceStarting
            : copy.serviceDown,
      tone: summary.service === 'running' ? 'ok' : summary.service === 'starting' ? 'busy' : 'error',
    },
    { key: 'model', value: summary.modelLabel || copy.noModel, tone: 'plain' },
    { key: 'speed', value: summary.speedTps !== null ? copy.formatTps(summary.speedTps) : copy.noSpeed, tone: 'plain' },
  ]
  if (summary.suggestRestart) {
    rows.push({ key: 'suggestion', value: copy.suggestion, tone: 'error' })
  }
  return rows
}

/**
 * Incrementally parse a chunk of SSE response text.
 *
 * Input is the raw response text accumulated so far (may contain a half-finished
 * JSON line); split on `\n` to get complete lines and only process lines with the
 * `data: ` prefix: extract `choices[0].delta.content` and
 * `choices[0].delta.reasoning_content` (thinking stream, emitted separately by
 * llama-server's default deepseek reasoning format), ignore `[DONE]`;
 * non-data lines and malformed lines are silently skipped.
 *
 * @returns { deltas: content deltas extracted this round, reasoningDeltas: reasoning deltas
 *            extracted this round, rest: unfinished trailing line fragment }
 */
export function parseSSEChunks(buffer: string): { deltas: string[]; reasoningDeltas: string[]; rest: string } {
  const deltas: string[] = []
  const reasoningDeltas: string[] = []
  // Find the last newline: everything before it can be split into complete lines; what follows is the fragment
  const lastNewline = buffer.lastIndexOf('\n')
  const processPart = lastNewline >= 0 ? buffer.slice(0, lastNewline) : ''
  const rest = lastNewline >= 0 ? buffer.slice(lastNewline + 1) : buffer

  const lines = processPart.split('\n')
  for (const rawLine of lines) {
    const line = rawLine.trim()
    if (!line.startsWith('data: ')) continue
    const payload = line.slice(6).trim()
    if (payload === '[DONE]') continue
    if (!payload) continue
    try {
      const json = JSON.parse(payload)
      const delta = json?.choices?.[0]?.delta
      const content = delta?.content
      if (typeof content === 'string' && content) {
        deltas.push(content)
      }
      const reasoning = delta?.reasoning_content
      if (typeof reasoning === 'string' && reasoning) {
        reasoningDeltas.push(reasoning)
      }
    } catch {
      // Silently ignore non-JSON data lines
    }
  }
  return { deltas, reasoningDeltas, rest }
}

/**
 * Compute per-phase token rates from chunk counts and phase durations (ms).
 * Returns null inputs as undefined.
 *
 * llama-server streams one token per data chunk, so delta counts are exact token
 * counts; callers round to 1 decimal only at the display layer.
 */
export function tokenRates(
  reasoningTokens: number,
  reasoningMs: number,
  answerTokens: number,
  answerMs: number
): { reasoningTps?: number; answerTps?: number } {
  const result: { reasoningTps?: number; answerTps?: number } = {}
  if (reasoningTokens > 0 && reasoningMs > 0) {
    result.reasoningTps = reasoningTokens / (reasoningMs / 1000)
  }
  if (answerTokens > 0 && answerMs > 0) {
    result.answerTps = answerTokens / (answerMs / 1000)
  }
  return result
}

/**
 * Build the chat completion request body.
 *
 * Each messages entry may carry an optional images array (data URLs); with images
 * present it uses the OpenAI-compatible multimodal format: content becomes a parts
 * array with text first and images after, matching how the llama.cpp webui sends.
 *
 * When params is provided, the request is customized at the top level of the
 * body: a non-empty systemPrompt is inserted as the first system message and
 * max_tokens carries the editor value. The four sampling fields
 * (temperature/top_p/top_k/repeat_penalty) follow generation-preset controls
 * (lib/genPresets.ts): by default they are sent from the params, and with
 * `{ serverSampling: true }` they are omitted entirely so llama-server falls
 * back to the per-model sampling parameters saved in ModelSettings (verified
 * against llama.cpp b10689: server-schema.cpp field_num("temperature"|"top_p"|
 * "top_k"|"repeat_penalty") evaluates only fields present in the body, and the
 * per-request sampling defaults to the server context when absent).
 */
export interface BuildChatBodyOptions {
  /** Omit the four sampling override fields (the "default" generation preset). */
  serverSampling?: boolean
}

export function buildChatBody(
  model: string,
  messages: { role: string; content: string; images?: string[] }[],
  params?: ChatParams,
  options?: BuildChatBodyOptions
): object {
  const body: Record<string, unknown> = {
    model,
    messages: messages.map(m => ({
      role: m.role,
      content: buildMessageContent(m.content, m.images),
    })),
    stream: true,
  }

  if (params) {
    if (!options?.serverSampling) {
      body.temperature = params.temperature
      body.top_p = params.topP
      body.top_k = params.topK
      body.repeat_penalty = params.repeatPenalty
    }
    body.max_tokens = params.maxTokens
    if (params.systemPrompt) {
      ;(body.messages as Array<{ role: string; content: string }>).unshift({
        role: 'system',
        content: params.systemPrompt,
      })
    }
  }

  return body
}

/**
 * Build an OpenAI-compatible message content from text and optional images.
 *
 * - No images: return the plain text string (stays compatible with non-multimodal backends).
 * - With images: return a parts array; the text part is included only when the text is
 *   non-empty, and each image becomes one image_url part, text first and images after,
 *   matching the llama.cpp webui image_url multimodal format.
 */
export function buildMessageContent(text: string, images?: string[]): string | Array<{ type: string; text?: string; image_url?: { url: string } }> {
  if (!images || images.length === 0) {
    return text
  }
  const parts: Array<{ type: string; text?: string; image_url?: { url: string } }> = []
  if (text) {
    parts.push({ type: 'text', text })
  }
  for (const url of images) {
    parts.push({ type: 'image_url', image_url: { url } })
  }
  return parts
}

/**
 * Transport-level options for direct llama-server HTTP requests: when the
 * server was started with an API key, every request must carry the bearer
 * token or it answers 401 (issue #29).
 */
export interface ChatAuthOptions {
  /** Configured llama-server API key; empty/undefined sends no auth header. */
  apiKey?: string
}

/** Build the request headers for a direct llama-server call: adds the Authorization bearer header only for a non-empty key. */
export function chatRequestHeaders(apiKey?: string): Record<string, string> {
  const headers: Record<string, string> = {}
  if (apiKey) {
    headers['Authorization'] = `Bearer ${apiKey}`
  }
  return headers
}

/**
 * Fetch the router's currently available model list (excluding failed entries).
 *
 * A 404 from GET /models means the server was started in direct mode (older
 * builds: no router routes at all) — fall back to the OpenAI-compatible
 * /v1/models listing, where every served model is by definition loaded.
 * Newer llama.cpp builds also answer /models natively in direct mode with the
 * OpenAI shape (data[].id, object:"model", no per-entry status): such entries
 * have no status field and map to status 'loaded' — a 200 with entries always
 * means resident models. Non-loaded router statuses pass through untouched
 * (modelsToUnload only acts on 'loaded'), and only a genuinely empty data
 * array yields an empty list (router mode with nothing loaded).
 */
export async function fetchRouterModels(port: number, auth?: ChatAuthOptions): Promise<RouterModel[]> {
  const res = await fetch(`http://127.0.0.1:${port}/models`, { headers: chatRequestHeaders(auth?.apiKey) })
  if (res.status === 404) {
    return fetchOpenAIModels(port, auth)
  }
  if (!res.ok) {
    const err = await res.json().catch(() => ({}))
    throw new Error(err?.error?.message || `GET /models failed: ${res.status}`)
  }
  const json = await res.json()
  const list = Array.isArray(json?.data) ? json.data : []
  return list
    .filter((m: any) => m?.status?.value !== 'failed')
    .map((m: any) => ({ id: m.id, status: m.status?.value ?? 'loaded' }))
}

/**
 * Direct-mode fallback: map GET /v1/models data[].id entries to RouterModel
 * values with status 'loaded' (direct servers always have their model in
 * memory).
 */
async function fetchOpenAIModels(port: number, auth?: ChatAuthOptions): Promise<RouterModel[]> {
  const res = await fetch(`http://127.0.0.1:${port}/v1/models`, { headers: chatRequestHeaders(auth?.apiKey) })
  if (!res.ok) {
    throw new Error(`GET /v1/models failed: ${res.status}`)
  }
  const json = await res.json()
  const list = Array.isArray(json?.data) ? json.data : []
  return list.map((m: any) => ({ id: m.id, status: 'loaded' }))
}

/**
 * Streaming chat completion: POST /v1/chat/completions, invoking onDelta per
 * answer token and onReasoningDelta per thinking token (reasoning_content).
 *
 * @param auth When the server runs with an API key, pass it here so the
 *        request carries the Authorization bearer header (issue #29);
 *        empty/undefined keeps the header set unchanged.
 * @throws On non-2xx, reads error.message from the body and throws it.
 */
export async function streamChatCompletion(
  port: number,
  model: string,
  messages: { role: string; content: string }[],
  onDelta: (text: string) => void,
  onReasoningDelta: (text: string) => void,
  signal: AbortSignal,
  params?: ChatParams,
  bodyOptions?: BuildChatBodyOptions,
  auth?: ChatAuthOptions
): Promise<void> {
  const res = await fetch(`http://127.0.0.1:${port}/v1/chat/completions`, {
    method: 'POST',
    headers: { 'Content-Type': 'application/json', ...chatRequestHeaders(auth?.apiKey) },
    body: JSON.stringify(buildChatBody(model, messages, params, bodyOptions)),
    signal,
  })

  if (!res.ok) {
    let msg = `HTTP ${res.status}`
    try {
      const err = await res.json()
      if (typeof err?.error?.message === 'string') msg = err.error.message
    } catch {
      // Ignore body parse failures
    }
    throw new Error(msg)
  }

  const reader = res.body?.getReader()
  if (!reader) throw new Error('Response body is not readable')

  const decoder = new TextDecoder()
  let buffer = ''
  try {
    while (true) {
      const { done, value } = await reader.read()
      if (done) break
      buffer += decoder.decode(value, { stream: true })
      const { deltas, reasoningDeltas, rest } = parseSSEChunks(buffer)
      buffer = rest
      for (const d of deltas) onDelta(d)
      for (const d of reasoningDeltas) onReasoningDelta(d)
    }
    // Process the final fragment
    const { deltas, reasoningDeltas } = parseSSEChunks(buffer)
    for (const d of deltas) onDelta(d)
    for (const d of reasoningDeltas) onReasoningDelta(d)
  } finally {
    reader.releaseLock()
  }
}
