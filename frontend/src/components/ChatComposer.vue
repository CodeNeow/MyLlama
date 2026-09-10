<template>
  <!-- Multi-root fragment on purpose (behavior-neutral refactor from
       Chat.vue): the pending-image preview bar and the input row are two
       SIBLING flex items of the parent .input-area column — wrapping them
       in a root div would insert an extra box into that flex chain and
       change the 10px column gap / layout, so the component renders both
       nodes directly. No class fallthrough is used on the component tag. -->
  <!-- Pending attachment preview bar -->
  <div v-if="pendingImages.length" class="pending-bar">
    <div class="pending-item" v-for="(img, i) in pendingImages" :key="i">
      <img :src="img" class="pending-thumb" alt="" />
      <button class="pending-remove" @click="removePendingImage(i)" :title="t('chat.removeImage')">✕</button>
    </div>
  </div>
  <div class="input-row" :class="{ 'input-row--blocked': blocked }">
    <!-- Vision gate (issue #35): the attach entry is disabled only when a
         model IS selected and it cannot take images (no sibling mmproj and
         no explicit projector override) — with no selection, attachments
         stay open and the send-time fallback guides instead. -->
    <button class="attach-btn" @click="triggerAttach" :disabled="attachBlocked" :title="attachBlocked ? t('chat.attachBlocked') : t('chat.attach')" type="button">
      <svg width="18" height="18" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round" stroke-linejoin="round">
        <path d="M21.44 11.05l-9.19 9.19a6 6 0 0 1-8.49-8.49l9.19-9.19a4 4 0 0 1 5.66 5.66l-9.2 9.19a2 2 0 0 1-2.83-2.83l8.49-8.48"/>
      </svg>
    </button>
    <input
      ref="fileInput"
      type="file"
      accept="image/*"
      multiple
      class="file-input-hidden"
      @change="onFileSelected"
    />
    <textarea
      ref="inputBox"
      class="chat-input"
      rows="1"
      :placeholder="inputPlaceholder"
      :disabled="serviceStarting || streaming || (!hasSelectedModel && !mobileTier)"
      @keydown="onInputKeydown"
      @input="onInputResize"
      @paste="onInputPaste"
    ></textarea>
    <!-- Design frame ② composer: circular gradient send button that
         flips to a red circular stop button while streaming — the state
         must read at a glance, so the icons + aria-labels swap with it.
         Phone tier (frame ⑦): with no model selected the button stays
         enabled so tapping it runs the chatReadiness precheck, which
         surfaces the guided "no models" notice + download CTA instead of
         a dead button; the desktop keeps the disabled gate unchanged. -->
    <button
      v-if="!streaming"
      class="send-btn"
      :disabled="serviceStarting || (!hasSelectedModel && !mobileTier)"
      :aria-label="t('chat.send')"
      :title="t('chat.send')"
      @click="attemptSend"
      type="button"
    >
      <svg width="19" height="19" viewBox="0 0 24 24" fill="currentColor" aria-hidden="true">
        <path d="M3 11.5L21 3l-8.5 18-2.3-7.2L3 11.5z"/>
      </svg>
    </button>
    <button
      v-else
      class="send-btn stop-btn"
      :aria-label="t('chat.stop')"
      :title="t('chat.stop')"
      @click="emit('stop')"
      type="button"
    >
      <svg width="15" height="15" viewBox="0 0 24 24" fill="currentColor" aria-hidden="true">
        <rect x="5.5" y="5.5" width="13" height="13" rx="2.5"/>
      </svg>
    </button>
  </div>
</template>

<script setup lang="ts">
import { computed, ref } from 'vue'
import { t } from '../lib/i18n'

// Chat input composer, extracted from Chat.vue (behavior-neutral refactor):
// the pending-image preview bar, the attach entry + hidden file input, the
// auto-resizing textarea and the send/stop button moved here with their
// scoped styles. The draft (input text + pending attachments) is COMPONENT
// state now: the page hands the gating flags down through props and receives
// the composed message through the send emit — only fired once the same
// guard chain the page's inline send() ran has passed (re-entrancy → element
// presence → non-empty input). The page clears the composer through the
// exposed clear() on the success path only, so every guided failure keeps
// the draft exactly as before; the vision-gate notice on attach stays on the
// page (it owns the notice stack) and is reported through attachRejected.

const props = defineProps<{
  /** True while a reply streams: the textarea disables and the send button flips to stop. */
  streaming: boolean
  /** Chat-initiated server bring-up in progress: disables the textarea and the send button. */
  serviceStarting: boolean
  /** A model is picked (picker value non-empty): desktop keeps the disabled gate, the phone tier does not. */
  hasSelectedModel: boolean
  /** Vision gate (issue #35): a selected model cannot take images — attach entry disabled. */
  attachBlocked: boolean
  /** Precheck blocker on screen (phone frame ⑦): dims the input row (blocked variant). */
  blocked: boolean
  /** Phone-tier gate (viewport width <= 767, reactive): short placeholder copy + the phone send-gate exception. */
  mobileTier: boolean
  /** Tablet-tier gate (768..1099, reactive): contributes the touch-tier placeholder check. */
  tabletTier: boolean
  /** False when the local model directory is empty: guided placeholder on the touch tiers. */
  hasModels: boolean
}>()

const emit = defineEmits<{
  /** Composed message: trimmed text + a copy of the pending attachment data
   *  URLs; fired only after the original guard chain passed. Guided failures
   *  in the page keep the composer untouched (it clears itself via clear()). */
  send: [text: string, images: string[]]
  /** Stop button pressed while streaming. */
  stop: []
  /** Attachment dropped by the vision gate (issue #35): the page surfaces the
   *  guided "images need a vision model" notice (the notice stack is page-owned). */
  attachRejected: []
}>()

// ─── Composer-local state (moved verbatim from Chat.vue) ────────────────────

const inputBox = ref<HTMLTextAreaElement | null>(null)
const fileInput = ref<HTMLInputElement | null>(null)

/** Pending images to send (data URLs), cleared after sending */
const pendingImages = ref<string[]>([])

/**
 * Composer placeholder (moved from Chat.vue): the desktop copy documents the
 * Enter / Shift+Enter keyboard affordances, which are meaningless on touch
 * keyboards and wrap to ~3 lines inside the narrow phone input. Touch tiers
 * (phone + tablet, both reactive across breakpoints) get the short copy
 * instead; desktop keeps the original text and behavior unchanged. A blocked
 * precheck (no models / runtime missing) swaps in the guided copy on the
 * touch tiers — and an empty model directory blocks the composer outright,
 * so the same guided copy shows there (tablet draft frames A⑦
 * "先下载模型后即可发送").
 */
const inputPlaceholder = computed(() => {
  if ((props.mobileTier || props.tabletTier) && (props.blocked || !props.hasModels)) {
    return t('chat.blockedPlaceholder')
  }
  return props.mobileTier || props.tabletTier ? t('chat.inputPlaceholderShort') : t('chat.inputPlaceholder')
})

/** Input auto-resizes to 1-6 rows: triggered by @input, reset after send/clear. */
function onInputResize() {
  const el = inputBox.value
  if (!el) return
  el.style.height = 'auto'
  const maxPx = 1.5 * parseFloat(getComputedStyle(document.documentElement).fontSize) * 6 + 20
  el.style.height = Math.min(el.scrollHeight, maxPx) + 'px'
}

function resetInputHeight() {
  const el = inputBox.value
  if (el) el.style.height = 'auto'
}

/** Read file as data URL; only image types; silently ignore failures or non-images */
async function readFileAsDataUrl(file: File): Promise<string | null> {
  if (!file.type.startsWith('image/')) {
    return null
  }
  return new Promise((resolve) => {
    const reader = new FileReader()
    reader.onload = () => resolve(typeof reader.result === 'string' ? reader.result : null)
    reader.onerror = () => resolve(null)
    reader.readAsDataURL(file)
  })
}

function triggerAttach() {
  fileInput.value?.click()
}

/**
 * Single pending-image write path (issue #35): both the file picker and the
 * paste handler funnel here, so the vision gate lives in exactly one place.
 * When a model is selected and it cannot take images, the data URL is silently
 * dropped and the page is told to raise its light notice (attachRejected —
 * the notice stack is page-owned); with no selection nothing is dropped
 * (send()'s fallback guides instead).
 */
function addPendingImage(dataUrl: string): void {
  if (props.attachBlocked) {
    emit('attachRejected')
    return
  }
  pendingImages.value.push(dataUrl)
}

async function onFileSelected(e: Event) {
  const input = e.target as HTMLInputElement
  const files = input.files
  if (!files) return
  for (const file of Array.from(files)) {
    const dataUrl = await readFileAsDataUrl(file)
    if (dataUrl) {
      addPendingImage(dataUrl)
    }
  }
  // Reset input so the same file can be selected again
  input.value = ''
}

/** Paste handler: extract image/* files from clipboardData */
async function onInputPaste(e: ClipboardEvent) {
  const items = e.clipboardData?.items
  if (!items) return
  const imageFiles: File[] = []
  for (const item of Array.from(items)) {
    if (item.type.startsWith('image/')) {
      const file = item.getAsFile()
      if (file) imageFiles.push(file)
    }
  }
  if (imageFiles.length === 0) return
  for (const file of imageFiles) {
    const dataUrl = await readFileAsDataUrl(file)
    if (dataUrl) {
      addPendingImage(dataUrl)
    }
  }
}

function removePendingImage(index: number) {
  pendingImages.value.splice(index, 1)
}

/**
 * Send guard chain, verbatim from the page's former inline send() head:
 * re-entrancy (never interleave streams or start two bring-ups), element
 * presence, and the empty-input check (text or at least one attachment).
 * On pass, hand the trimmed text and a COPY of the pending attachments to
 * the page — guided failures there keep the composer untouched, and the
 * success path clears it through the exposed clear().
 */
function attemptSend(): void {
  if (props.streaming || props.serviceStarting) return
  const input = inputBox.value
  if (!input) return
  const text = input.value.trim()
  if (!text && pendingImages.value.length === 0) return
  emit('send', text, [...pendingImages.value])
}

function onInputKeydown(e: KeyboardEvent) {
  if (e.key === 'Enter' && !e.shiftKey) {
    e.preventDefault()
    attemptSend()
  }
}

// ─── Exposed controls (Chat.vue's former inputBox/pendingImages touch points) ──

/** Clear the draft: input text, pending attachments and the auto-resize
 *  height — the exact triple the page's send success path cleared inline. */
function clear(): void {
  const el = inputBox.value
  if (el) el.value = ''
  pendingImages.value = []
  resetInputHeight()
}

/** Focus the textarea (Chat.vue's clear-conversation flow + model-switch watch). */
function focus(): void {
  inputBox.value?.focus()
}

/** Collapse the auto-resized textarea back to one row (Chat.vue's clear-conversation flow). */
function resetHeight(): void {
  resetInputHeight()
}

defineExpose({
  clear,
  focus,
  resetHeight,
})
</script>

<style scoped>
/* ─── Composer (extracted from Chat.vue, behavior-neutral): the glass input
   bar inside the page's .input-area flex column. The page owns .input-area
   (the height/100dvh chain and the --dock-width lane paddings live there);
   everything below skins the bar itself. Selectors and declarations moved
   verbatim — class names, order and media bands are unchanged so the
   computed cascade is identical. ─── */
.input-row {
  display: flex;
  align-items: center;
  gap: 8px;
  padding: 6px 6px 6px 10px;
  background: var(--glass);
  border: 1px solid var(--glass-line);
  border-radius: var(--r-lg);
  box-shadow: var(--shadow-island);
  transition: border-color 0.2s;
}

.input-row:focus-within {
  border-color: rgba(99, 102, 241, 0.45);
}

.pending-bar {
  display: flex;
  flex-wrap: wrap;
  gap: 8px;
}

.pending-item {
  position: relative;
  width: 64px;
  height: 64px;
  border-radius: 8px;
  overflow: hidden;
  border: 1px solid var(--border);
}

.pending-thumb {
  width: 100%;
  height: 100%;
  object-fit: cover;
  display: block;
}

.pending-remove {
  position: absolute;
  top: 2px;
  right: 2px;
  width: 18px;
  height: 18px;
  display: flex;
  align-items: center;
  justify-content: center;
  background: rgba(0, 0, 0, 0.55);
  color: #fff;
  border: none;
  border-radius: 50%;
  font-size: 10px;
  cursor: pointer;
  line-height: 1;
}

.attach-btn {
  width: 42px;
  height: 42px;
  display: flex;
  align-items: center;
  justify-content: center;
  background: transparent;
  border: none;
  border-radius: 50%;
  color: var(--text-secondary);
  cursor: pointer;
  transition: all 0.2s;
  flex-shrink: 0;
}

.attach-btn:hover:not(:disabled) {
  background: var(--hover-bg);
  color: var(--text-primary);
}

/* Vision-gated attach entry (issue #35): dimmed with a not-allowed cursor so
   the blocked state reads at a glance (the title carries the reason). */
.attach-btn:disabled {
  opacity: 0.35;
  cursor: not-allowed;
}

/* Touch press feedback (OS-scoped): :hover never fires on touch input, so
   the active state mirrors the hover visuals under html[data-os]. */
html[data-os='android'] .attach-btn:active,
html[data-os='ios'] .attach-btn:active {
  background: var(--hover-bg);
  color: var(--text-primary);
}

.file-input-hidden {
  display: none;
}

.chat-input {
  flex: 1;
  resize: none;
  padding: 10px 4px;
  background: transparent;
  border: none;
  color: var(--text-primary);
  font-size: 14px;
  font-weight: 500;
  font-family: var(--font-sans);
  line-height: 1.5;
  outline: none;
  /* Auto-resize 1-6 rows; focus feedback lives on the glass bar
     (.input-row:focus-within), not on the naked textarea */
  min-height: 42px;
  max-height: calc(1.5em * 6 + 20px);
  overflow-y: auto;
}

.chat-input::placeholder {
  color: var(--text-dim);
}

/* Circular gradient send button (design .composer .send): gradient = the one
   actionable element. Desktop keeps the 42px band so the TaskDock pill's
   vertical centering (dockSpace DOCK_BOTTOM_OFFSET 29px) is unchanged. */
.send-btn {
  width: 42px;
  height: 42px;
  padding: 0;
  display: flex;
  align-items: center;
  justify-content: center;
  background: var(--grad);
  color: #fff;
  border: none;
  border-radius: 50%;
  cursor: pointer;
  transition: all 0.2s;
  flex-shrink: 0;
  box-shadow: none;
}

.send-btn:hover:not(:disabled) {
  filter: brightness(1.08);
  box-shadow: none;
}

/* Touch press feedback (OS-scoped): mirrors the hover lift on touch (also
   covers the streaming stop state, which shares this class). */
html[data-os='android'] .send-btn:active:not(:disabled),
html[data-os='ios'] .send-btn:active:not(:disabled) {
  filter: brightness(1.08);
  box-shadow: none;
}

.send-btn:disabled {
  opacity: 0.35;
  cursor: default;
  box-shadow: none;
}

/* Streaming state flips the same circle to danger red (design frame ⑥:
   red is reserved for stop/unload) with a square stop glyph */
.stop-btn {
  background: var(--danger);
  box-shadow: none;
}

.stop-btn:hover:not(:disabled) {
  filter: brightness(1.08);
  box-shadow: none;
}

/* ─── Mobile (<=767px): compact composer (moved from Chat.vue's phone block;
   the .input-area band sizing stays on the page) ─── */
@media (max-width: 767px) {
  /* Degraded composer (frame ⑦): a visible precheck blocker dims the bar */
  .input-row--blocked {
    opacity: 0.6;
  }

  /* Disabled send (frame ⑦): neutral filled circle instead of a ghosted
     gradient — the shape still reads, the affordance clearly gone */
  .send-btn:disabled {
    background: var(--bg-card);
    color: #9aa1b2;
    opacity: 1;
    box-shadow: none;
  }

  /* Composer: 44px touch controls inside the trimmed band above the bottom
     tab bar (the .input-area padding anchor arithmetic stays in Chat.vue). */
  .attach-btn {
    width: 44px;
    height: 44px;
  }

  .chat-input {
    min-height: 44px;
  }

  .send-btn {
    width: 44px;
    height: 44px;
  }

  .send-btn svg {
    width: 20px;
    height: 20px;
  }
}

/* ─── Tablet portrait Track A (768..1099px; tablet draft frame A⑦). Scoped to
   the band with min-width: 768px so phones (<=767) and desktop (>=1100px)
   stay untouched. ─── */
@media (min-width: 768px) and (max-width: 1099px) {
  /* Frame A⑦: composer degrades */
  .input-row--blocked {
    opacity: 0.6;
  }
}
</style>
