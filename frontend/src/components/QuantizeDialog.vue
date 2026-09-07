<template>
  <Teleport to="body">
    <div class="qz-root" @keydown.esc="maybeClose">
      <div class="qz-dim" @click="maybeClose"></div>
      <div class="qz-dialog" role="dialog" aria-modal="true" :aria-label="t('quantize.title')">
        <div class="qz-head">
          <h3 class="qz-title">{{ t('quantize.title') }}</h3>
          <button class="qz-close" type="button" :aria-label="t('quantize.close')" @click="maybeClose">
            <svg width="16" height="16" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round">
              <line x1="18" y1="6" x2="6" y2="18"/><line x1="6" y1="6" x2="18" y2="18"/>
            </svg>
          </button>
        </div>

        <!-- Form phase: source file / target quant / output name -->
        <template v-if="!running && !finished">
          <label class="qz-field">
            <span class="qz-label">{{ t('quantize.src') }}</span>
            <input v-model="src" class="qz-input" type="text" spellcheck="false" :aria-label="t('quantize.src')" />
          </label>
          <div class="qz-field">
            <span class="qz-label">{{ t('quantize.type') }}</span>
            <div class="qz-quants" role="radiogroup" :aria-label="t('quantize.type')">
              <label v-for="q in QUANT_TYPES" :key="q" class="qz-radio" :class="{ active: quant === q }">
                <input v-model="quant" type="radio" name="qz-quant" :value="q" />
                <span class="qz-radio-mark" aria-hidden="true"></span>
                <span class="qz-radio-text">{{ q }}</span>
              </label>
            </div>
            <p class="qz-hint">{{ t('quantize.typeHint') }}</p>
          </div>
          <label class="qz-field">
            <span class="qz-label">{{ t('quantize.out') }}</span>
            <input v-model="outName" class="qz-input" type="text" spellcheck="false" :aria-label="t('quantize.out')" @input="outDirty = true" />
            <p class="qz-hint">{{ t('quantize.outHint') }}</p>
          </label>
          <p v-if="formError" class="qz-err">{{ formError }}</p>
        </template>

        <!-- Run phase: log tail + cancel -->
        <template v-else>
          <div class="qz-status">
            <span class="qz-status-dot" :class="running ? 'qz-dot--run' : statusSuccess ? 'qz-dot--ok' : 'qz-dot--err'"></span>
            <span class="qz-status-text">
              <template v-if="running">{{ t('quantize.running') }}</template>
              <template v-else-if="statusSuccess">{{ t('quantize.done') }}</template>
              <template v-else>{{ t('quantize.failed') }}</template>
            </span>
            <span v-if="status?.quant" class="qz-status-quant">{{ status.quant }}</span>
          </div>
          <div ref="logBox" class="qz-log" aria-live="polite">
            <div v-for="line in status?.logs ?? []" :key="line.seq" class="qz-log-line">{{ line.text }}</div>
          </div>
          <p v-if="finished && statusSuccess" class="qz-hint">{{ t('quantize.doneHint') }}</p>
          <p v-if="finished && !statusSuccess" class="qz-err">{{ status?.error || t('quantize.failed') }}</p>
        </template>

        <div class="qz-actions">
          <button class="qz-btn" type="button" @click="maybeClose">{{ running ? t('quantize.hide') : t('quantize.close') }}</button>
          <button v-if="running" class="qz-btn qz-btn--danger" type="button" @click="cancel">{{ t('quantize.cancel') }}</button>
          <button
            v-else-if="!finished"
            class="qz-btn qz-btn--primary"
            type="button"
            :disabled="!canRun"
            @click="run"
          >{{ t('quantize.run') }}</button>
          <button v-else class="qz-btn qz-btn--primary" type="button" @click="reset">{{ t('quantize.again') }}</button>
        </div>
      </div>
    </div>
  </Teleport>
</template>

<script setup lang="ts">
import { computed, nextTick, ref, watch } from 'vue'
import { getQuantizeStatus, startQuantize, cancelQuantize, type QuantizeStatus } from '../wails'
import { t } from '../lib/i18n'

/**
 * Desktop quantize dialog (unsloth-export consumer): drives llama-quantize
 * through the backend bindings. One dialog = at most one task lifecycle; the
 * backend enforces the actual single-flight. Form → running (log tail +
 * cancel) → terminal (done / failed + re-run).
 */

const props = defineProps<{
  /** Prefilled source .gguf path (the clicked model card's path). */
  srcPath: string
}>()

const emit = defineEmits<{
  /** Closing is always allowed for the user; the parent v-if unmounts us. */
  (e: 'close'): void
  /** A quantize finished successfully — the parent may offer a model rescan. */
  (e: 'finished', outPath: string): void
}>()

const QUANT_TYPES = ['q4_k_m', 'q5_k_m', 'q8_0', 'f16'] as const
const DEFAULT_QUANT = 'q4_k_m'

const src = ref(props.srcPath)
const quant = ref<string>(DEFAULT_QUANT)
const outName = ref(defaultOutFor(props.srcPath, DEFAULT_QUANT))
const outDirty = ref(false)
const formError = ref('')

const running = ref(false)
const finished = ref(false)
const statusSuccess = ref(false)
const status = ref<QuantizeStatus | null>(null)
let pollTimer: ReturnType<typeof setInterval> | undefined
/** Guards double-clicks between run() and the first poll confirming running. */
let starting = false

const logBox = ref<HTMLElement | null>(null)

/** Output name auto base-<quant>.gguf until the user edits it. */
function defaultOutFor(path: string, q: string): string {
  const base = path.split(/[\\/]/).pop() || 'model.gguf'
  const stem = base.replace(/\.gguf$/i, '')
  return `${stem}-${q}.gguf`
}

watch(quant, (q) => {
  if (!outDirty.value) outName.value = defaultOutFor(src.value, q)
})

// Keep the auto output name in sync when the user edits the source path.
watch(src, () => {
  if (!outDirty.value) outName.value = defaultOutFor(src.value, quant.value)
})

const canRun = computed(() => !!src.value.trim() && !!outName.value.trim() && !!quant.value)

/** Re-running from the terminal state resets to a fresh form. */
function reset() {
  finished.value = false
  statusSuccess.value = false
  status.value = null
  formError.value = ''
}

function maybeClose() {
  // Closing hides the dialog; a running task keeps running server-side (the
  // backend single-flight still applies). Terminal state just closes.
  emit('close')
}

async function run() {
  if (starting || running.value) return
  starting = true
  formError.value = ''
  try {
    await startQuantize(src.value.trim(), quant.value, outName.value.trim())
    running.value = true
    finished.value = false
    status.value = null
    startPolling()
  } catch (e: any) {
    formError.value = e?.message || String(e)
  } finally {
    starting = false
  }
}

function startPolling() {
  stopPolling()
  pollTimer = setInterval(async () => {
    try {
      const st = await getQuantizeStatus()
      status.value = st
      running.value = st.running
      // Terminal on the backend's own done flag (a just-started task reports
      // done=false; a stale done from a previous run is overwritten at start).
      if (!st.running) {
        finished.value = true
        statusSuccess.value = st.success
        stopPolling()
        if (st.success && st.outPath) emit('finished', st.outPath)
        else if (!st.running && !st.done && !st.success) {
          // Backend reports nothing at all (fresh process): degrade to form.
          running.value = false
        }
      }
      scrollLog()
    } catch {
      // Transient bridge hiccup: keep polling; the user can cancel/close.
    }
  }, 700)
}

function stopPolling() {
  if (pollTimer) {
    clearInterval(pollTimer)
    pollTimer = undefined
  }
}

async function cancel() {
  try {
    await cancelQuantize()
  } catch (e: any) {
    formError.value = e?.message || String(e)
  }
}

function scrollLog() {
  nextTick(() => {
    if (logBox.value) logBox.value.scrollTop = logBox.value.scrollHeight
  })
}
</script>

<style scoped>
.qz-root {
  position: fixed;
  inset: 0;
  z-index: 1000;
  display: flex;
  align-items: center;
  justify-content: center;
  padding: 20px;
}

.qz-dim {
  position: absolute;
  inset: 0;
  background: var(--overlay-50, rgba(0, 0, 0, 0.45));
}

.qz-dialog {
  position: relative;
  width: min(520px, 100%);
  max-height: min(640px, 88vh);
  overflow-y: auto;
  background: var(--bg-card);
  border: 1px solid var(--border-light);
  border-radius: var(--radius-md);
  box-shadow: var(--shadow-lg, 0 18px 50px rgba(0, 0, 0, 0.25));
  padding: 18px;
  display: flex;
  flex-direction: column;
  gap: 14px;
}

.qz-head {
  display: flex;
  align-items: center;
  justify-content: space-between;
}

.qz-title {
  margin: 0;
  font-size: 15px;
  font-weight: 700;
  color: var(--text-primary);
}

.qz-close {
  background: transparent;
  border: none;
  color: var(--text-muted);
  cursor: pointer;
  padding: 4px;
  border-radius: var(--radius-sm);
}

.qz-close:hover {
  color: var(--text-primary);
  background: var(--hover-bg);
}

.qz-field {
  display: flex;
  flex-direction: column;
  gap: 6px;
}

.qz-label {
  font-size: 12px;
  font-weight: 600;
  color: var(--text-muted);
}

.qz-input {
  width: 100%;
  padding: 9px 12px;
  background: var(--bg-primary);
  border: 1px solid var(--border);
  border-radius: var(--radius-sm);
  color: var(--text-primary);
  font-size: 12.5px;
  font-family: var(--font-mono);
  outline: none;
}

.qz-input:focus {
  border-color: var(--accent);
}

.qz-quants {
  display: flex;
  gap: 8px;
  flex-wrap: wrap;
}

.qz-radio {
  display: inline-flex;
  align-items: center;
  gap: 6px;
  padding: 7px 14px;
  background: var(--surface);
  border: 1px solid var(--border-light);
  border-radius: 999px;
  cursor: pointer;
  transition: all 0.15s;
}

.qz-radio input {
  position: absolute;
  opacity: 0;
  width: 0;
  height: 0;
}

.qz-radio-mark {
  width: 12px;
  height: 12px;
  border-radius: 50%;
  border: 2px solid var(--border);
  background: transparent;
  flex-shrink: 0;
}

.qz-radio.active {
  border-color: var(--accent);
  background: rgba(99, 102, 241, 0.08);
}

.qz-radio.active .qz-radio-mark {
  border-color: var(--accent);
  background: var(--accent);
}

.qz-radio-text {
  font-size: 12.5px;
  font-weight: 600;
  color: var(--text-secondary);
  font-family: var(--font-mono);
}

.qz-hint {
  margin: 0;
  font-size: 11.5px;
  color: var(--text-dim);
  line-height: 1.5;
}

.qz-err {
  margin: 0;
  font-size: 12px;
  color: #ef4444;
}

.qz-status {
  display: flex;
  align-items: center;
  gap: 8px;
}

.qz-status-dot {
  width: 10px;
  height: 10px;
  border-radius: 50%;
  flex-shrink: 0;
}

.qz-dot--run {
  background: var(--accent);
  animation: qz-pulse 1.2s ease-in-out infinite;
}

.qz-dot--ok {
  background: #10b981;
}

.qz-dot--err {
  background: #ef4444;
}

@keyframes qz-pulse {
  0%, 100% { opacity: 1; }
  50% { opacity: 0.35; }
}

.qz-status-text {
  font-size: 13px;
  font-weight: 600;
  color: var(--text-primary);
}

.qz-status-quant {
  margin-left: auto;
  font-size: 12px;
  font-weight: 700;
  color: var(--text-muted);
  font-family: var(--font-mono);
}

.qz-log {
  min-height: 200px;
  max-height: 300px;
  overflow-y: auto;
  background: var(--bg-primary);
  border: 1px solid var(--border);
  border-radius: var(--radius-sm);
  padding: 10px 12px;
  font-family: var(--font-mono);
  font-size: 11.5px;
  line-height: 1.65;
  color: var(--text-secondary);
}

.qz-log-line {
  word-break: break-all;
  white-space: pre-wrap;
}

.qz-actions {
  display: flex;
  justify-content: flex-end;
  gap: 10px;
}

.qz-btn {
  padding: 8px 18px;
  background: transparent;
  border: 1px solid var(--border);
  border-radius: var(--radius-sm);
  color: var(--text-muted);
  font-size: 12.5px;
  font-weight: 600;
  font-family: inherit;
  cursor: pointer;
}

.qz-btn:hover:not(:disabled) {
  background: var(--hover-bg);
}

.qz-btn--primary {
  background: var(--accent);
  border-color: var(--accent);
  color: #fff;
}

.qz-btn--primary:hover:not(:disabled) {
  opacity: 0.88;
  background: var(--accent);
}

.qz-btn--danger {
  border-color: rgba(239, 68, 68, 0.4);
  color: #ef4444;
}

.qz-btn--danger:hover:not(:disabled) {
  background: rgba(239, 68, 68, 0.08);
}

.qz-btn:disabled {
  opacity: 0.5;
  cursor: not-allowed;
}
</style>
