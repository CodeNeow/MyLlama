<template>
  <!-- Multi-root fragment on purpose (behavior-neutral refactor from
       Chat.vue): the desktop popover is a flex sibling inside .chat-toolbar
       (anchored to it via position:absolute) and the phone sheet / tablet
       modal Teleport overlays <body> — wrapping both in a root div would
       insert an extra box into the toolbar flex chain, so the component
       renders both nodes directly. No class fallthrough is used on the
       component tag. -->
  <!-- Chat parameters panel: desktop keeps the anchored popover; the phone
       tier (frame ⑤) gets a bottom sheet and the tablet-portrait tier a
       centered modal card — both rendered by the Teleport below. The
       paramsLayout gate (not a bare !isMobileTier) also keeps the popover
       from reappearing if showParams is left open across a tier switch
       (e.g. resizing a desktop window into the tablet band). -->
  <div v-if="show && paramsLayout === 'popover'" class="params-popover" @click.stop>
    <div class="params-header">{{ t('chat.settings') }}</div>
    <!-- Sampling fields are owned by the generation preset (toolbar picker):
         the "默认" preset sends no sampling fields (llama-server's saved
         per-model params apply), any other preset sends its four values.
         The panel shows the active preset read-only instead of inputs. -->
    <div class="params-row params-row-full preset-summary">
      <span class="preset-summary-label">{{ t('chat.genPreset') }}</span>
      <span class="preset-summary-value">{{ presetLabel }}<template v-if="presetSummary"> · {{ presetSummary }}</template></span>
    </div>
    <div class="params-row">
      <label class="params-label" for="chat-maxtok">{{ t('chat.maxTokens') }}</label>
      <input id="chat-maxtok" class="params-input" type="number" min="-1" v-model.number="chatParams.maxTokens" />
    </div>
    <div class="params-row params-row-full">
      <label class="params-label" for="chat-sys">{{ t('chat.systemPrompt') }}</label>
      <textarea id="chat-sys" class="params-textarea" rows="2" v-model="chatParams.systemPrompt" :placeholder="t('chat.systemPromptPh')"></textarea>
    </div>
    <div class="params-footer">
      <button class="params-reset-btn" @click="resetParams">{{ t('chat.resetDefaults') }}</button>
    </div>
  </div>

  <!-- Phone params sheet (design frame ⑤) / tablet-portrait modal card
       (tablet draft frame ⑤): dim + overlay with slider / stepper
       controls. Same chatParams state and persistence as the popover —
       only the controls and the overlay geometry differ (phone: docked
       full-width bottom sheet; tablet portrait: centered 560px card,
       styled per tier in CSS). .stop keeps in-overlay taps from hitting
       the document-level close handler; tapping the dim closes.

       WARNING: this Teleport MUST be conditionally rendered with
       v-if="overlayParams" (phone sheet + tablet modal tiers only). A
       persistent empty Teleport node in the vnode tree breaks Vue's
       out-in <transition> afterLeave callback on this page:
       BaseTransition waits for the leaving component's afterLeave, but
       the lingering Teleport causes the leave hook to be lost,
       state.isLeaving sticks permanently, and the content area stays
       blank after navigating away from Chat. This is a known
       Vue×Teleport edge case (see vuejs/core#5836 and related issues).
       Desktop never mounts this Teleport node at all; only the
       sheet/modal tiers teleport their params overlay to <body>. -->
  <Teleport v-if="overlayParams" to="body">
    <div v-if="show && overlayParams" class="params-sheet-root">
      <div class="params-dim" @click="emit('close')"></div>
      <div
        class="params-sheet"
        :class="{ 'params-sheet--modal': paramsLayout === 'modal' }"
        role="dialog"
        aria-modal="true"
        :aria-label="t('chat.paramsTitle')"
        @click.stop
      >
        <div class="params-grab" aria-hidden="true"></div>
        <div class="params-sheet-head">
          <span class="params-sheet-title">{{ t('chat.paramsTitle') }}</span>
          <button class="params-reset-link" type="button" @click="resetParams">{{ t('chat.resetDefaults') }}</button>
        </div>
        <div class="params-sheet-body">
          <!-- Sampling fields are owned by the generation preset (toolbar
               picker): read-only summary here instead of sliders/steppers. -->
          <div class="psheet-row psheet-row--text preset-summary">
            <div class="psheet-label">
              <span class="psheet-name">{{ t('chat.genPreset') }}</span>
              <span class="psheet-sub">{{ t('chat.presetSummaryHint') }}</span>
            </div>
            <span class="preset-summary-value">{{ presetLabel }}<template v-if="presetSummary"> · {{ presetSummary }}</template></span>
          </div>
          <div class="psheet-row">
            <div class="psheet-label">
              <span class="psheet-name">{{ t('chat.maxTokens') }}</span>
              <span class="psheet-sub">{{ t('chat.maxTokensHint') }}</span>
            </div>
            <div class="pstepper">
              <button class="pstep-btn" type="button" :aria-label="t('chat.stepDown')" @click="chatParams.maxTokens = applyMaxTokensStep(-1)">−</button>
              <span class="pstep-val">{{ isUnlimitedMaxTokens(chatParams.maxTokens) ? t('chat.unlimited') : chatParams.maxTokens }}</span>
              <button class="pstep-btn" type="button" :aria-label="t('chat.stepUp')" @click="chatParams.maxTokens = applyMaxTokensStep(1)">+</button>
            </div>
          </div>
          <div class="psheet-row psheet-row--text">
            <div class="psheet-label">
              <span class="psheet-name">{{ t('chat.systemPrompt') }}</span>
              <span class="psheet-sub">{{ t('chat.systemPromptHint') }}</span>
            </div>
            <textarea
              class="ptext"
              rows="2"
              v-model="chatParams.systemPrompt"
              :placeholder="t('chat.systemPromptPh')"
              :aria-label="t('chat.systemPrompt')"
            ></textarea>
          </div>
        </div>
      </div>
    </div>
  </Teleport>
</template>

<script setup lang="ts">
import { computed } from 'vue'
import { chatParamsLayout } from '../lib/chat'
import { chatParams, persistChatParams, stepMaxTokens, isUnlimitedMaxTokens } from '../lib/chatState'
import { t } from '../lib/i18n'

// Chat generation-params panel, extracted from Chat.vue (behavior-neutral
// refactor): the desktop popover, the phone bottom sheet, the
// tablet-portrait modal card and their scoped styles moved here. The panel
// reads and writes chatParams (the lib/chatState module-level reactive that
// the page's send() also consumes) DIRECTLY — no props/emit relay for the
// edited values, so a change takes effect on the next request exactly as
// before. Visibility stays page-owned (showParams in Chat.vue): the toolbar
// gear toggles it and the document-level click handler closes it, so the
// panel receives it as the show prop and only reports the overlay dim tap
// back through close. The generation preset is READ-ONLY in this panel (its
// toolbar picker, manager dialog and send()-time sampling override stay in
// Chat.vue); the resolved display label + sampling summary arrive as props.

const props = defineProps<{
  /** Whether the panel is expanded (the page-owned showParams flag). */
  show: boolean
  /** Phone-tier gate (viewport width <= 767, reactive): picks the bottom sheet. */
  mobileTier: boolean
  /** Tablet-tier gate (768..1099, reactive): picks the centered modal card. */
  tabletTier: boolean
  /** Active generation preset display label (read-only summary rows). */
  presetLabel: string
  /** Compact "temp … / top_p … / top_k … / rep …" readout; '' for the default preset. */
  presetSummary: string
}>()

/** Overlay dim tapped (phone sheet / tablet modal): the page closes the panel. */
const emit = defineEmits<{ close: [] }>()

/**
 * Which surface renders the inference-params editor (tablet draft frame ⑤):
 * desktop popover / phone sheet / tablet-portrait modal. Pure classifier in
 * lib/chat.ts; drives the popover + Teleport gates below.
 */
const paramsLayout = computed(() => chatParamsLayout(props.mobileTier, props.tabletTier))

/** The params editor renders as a Teleported overlay (phone sheet or tablet-portrait modal). */
const overlayParams = computed(() => paramsLayout.value === 'sheet' || paramsLayout.value === 'modal')

/**
 * Phone params-sheet control ranges: max tokens is the only remaining stepper
 * (the four sampling fields are owned by the generation preset, see below).
 */
/** Max-tokens stepper with the unlimited (-1) sentinel mapping. */
function applyMaxTokensStep(dir: -1 | 1): number {
  return stepMaxTokens(chatParams.maxTokens, dir)
}

/** Reset chat params to defaults (in-flight requests unaffected; takes effect on next send). */
function resetParams() {
  // The four sampling fields are owned by the generation preset (toolbar
  // picker) and are not edited here; reset only covers the editor-owned fields.
  Object.assign(chatParams, {
    maxTokens: -1,
    systemPrompt: '',
  })
  persistChatParams()
}
</script>

<style scoped>
/* Params panel (popover + sheet/modal): read-only active-preset summary row.
   (.preset-summary-value is ALSO rendered by the preset manager dialog that
   stayed in Chat.vue — its rule below is duplicated there; keep the two
   declarations in sync.) */
.preset-summary {
  display: flex;
  flex-direction: column;
  gap: 4px;
  padding: 8px 10px;
  background: var(--surface);
  border: 1px solid var(--border-light);
  border-radius: var(--radius-sm);
}

.preset-summary-label {
  font-size: 11px;
  font-weight: 700;
  color: var(--text-muted);
  text-transform: uppercase;
  letter-spacing: 0.4px;
}

.preset-summary-value {
  font-size: 12.5px;
  font-weight: 600;
  color: var(--text-secondary);
  word-break: break-word;
  font-family: var(--font-mono);
}

/* ─── Params Popover (design frame ② skin: floating island + large radius;
       layout / wiring unchanged) ─── */
.params-popover {
  position: absolute;
  right: 0;
  top: calc(100% + 8px);
  z-index: 30;
  width: 320px;
  background: var(--bg-secondary);
  border: 1px solid var(--border);
  border-radius: var(--r-lg);
  box-shadow: var(--shadow-island);
  padding: 18px;
}

.params-header {
  font-size: 12px;
  font-weight: 700;
  letter-spacing: 0.3px;
  color: var(--text-secondary);
  margin-bottom: 12px;
}

.params-row {
  display: flex;
  align-items: center;
  justify-content: space-between;
  gap: 8px;
  margin-bottom: 8px;
}

.params-row-full {
  flex-direction: column;
  align-items: stretch;
}

.params-label {
  font-size: 13px;
  color: var(--text-secondary);
  white-space: nowrap;
}

.params-input {
  width: 110px;
  padding: 7px 10px;
  background: var(--bg-primary);
  border: 1px solid var(--border);
  border-radius: 10px;
  color: var(--text-primary);
  font-size: 13px;
  outline: none;
}

.params-input:focus {
  border-color: rgba(99, 102, 241, 0.4);
}

.params-textarea {
  width: 100%;
  padding: 8px 10px;
  background: var(--bg-primary);
  border: 1px solid var(--border);
  border-radius: var(--r-md);
  color: var(--text-primary);
  font-size: 13px;
  font-family: var(--font-sans);
  line-height: 1.5;
  outline: none;
  resize: vertical;
}

.params-textarea:focus {
  border-color: rgba(99, 102, 241, 0.4);
}

.params-textarea::placeholder {
  color: var(--text-dim);
}

.params-footer {
  display: flex;
  justify-content: flex-end;
  margin-top: 10px;
}

.params-reset-btn {
  padding: 7px 14px;
  background: transparent;
  border: 1px solid var(--border);
  border-radius: 999px;
  color: var(--text-secondary);
  font-size: 12px;
  cursor: pointer;
  transition: all 0.2s;
}

.params-reset-btn:hover {
  background: var(--hover-bg);
  color: var(--text-primary);
  border-color: var(--overlay-20);
}

/* ─── Phone params sheet (design frame ⑤ .dim / .sheet) ───
   Rendered only on the phone tier (isMobileTier), teleported to <body> so the
   dim + sheet float above every page layer. Desktop never mounts this markup.

   The Teleport wrapper in the template is gated by v-if="isMobileTier" (not
   just the inner .params-sheet-root) because a persistent empty Teleport node
   in the vnode tree breaks Vue's out-in <transition> afterLeave callback on
   the chat page: state.isLeaving sticks and the content area stays blank
   after navigating away (Vue×Teleport edge case, vuejs/core#5836). */
.params-sheet-root {
  position: fixed;
  inset: 0;
  z-index: 60;
}

.params-dim {
  position: absolute;
  inset: 0;
  background: rgba(16, 18, 33, 0.42);
  animation: dim-in 0.2s ease;
}

@keyframes dim-in {
  from { opacity: 0; }
  to { opacity: 1; }
}

.params-sheet {
  position: absolute;
  left: 10px;
  right: 10px;
  bottom: calc(10px + var(--safe-area-bottom, 0px) + var(--keyboard-inset, 0px));
  max-height: calc(100vh - 90px);
  max-height: calc(100dvh - 90px);
  display: flex;
  flex-direction: column;
  background: var(--bg-secondary);
  border-radius: 26px;
  padding: 18px 20px 16px;
  box-shadow: none;
  animation: sheet-up 0.25s ease;
}

@keyframes sheet-up {
  from { transform: translateY(100%); }
  to { transform: translateY(0); }
}

.params-grab {
  width: 40px;
  height: 4px;
  border-radius: 999px;
  background: var(--border);
  margin: 0 auto 12px;
  flex-shrink: 0;
}

.params-sheet-head {
  display: flex;
  align-items: center;
  margin-bottom: 4px;
  flex-shrink: 0;
}

.params-sheet-title {
  font-size: 17px;
  font-weight: 800;
  color: var(--text-primary);
}

.params-reset-link {
  margin-left: auto;
  padding: 8px 0 8px 12px;
  background: transparent;
  border: none;
  font-size: 12px;
  font-weight: 700;
  color: var(--accent-light);
  cursor: pointer;
}

.params-sheet-body {
  min-height: 0;
  overflow-y: auto;
}

.psheet-row {
  display: flex;
  align-items: center;
  gap: 10px;
  padding: 9px 0;
  border-bottom: 1px solid var(--border);
}

.psheet-row:last-child {
  border-bottom: none;
}

.psheet-row--text {
  flex-direction: column;
  align-items: stretch;
}

.psheet-label {
  flex: 1;
  min-width: 0;
  display: flex;
  flex-direction: column;
  gap: 2px;
}

.psheet-name {
  font-size: 13px;
  font-weight: 600;
  color: var(--text-primary);
}

.psheet-sub {
  font-size: 11.5px;
  line-height: 1.5;
  color: var(--text-muted);
}

.psheet-ctl {
  display: flex;
  align-items: center;
  gap: 8px;
  flex-shrink: 0;
}

/* Native range input reskinned to the mockup .pslider: 4px gradient track on
   a neutral base (--pfill drives the filled portion), 15px white thumb inside
   a 44px-tall hit area. */
.pslider {
  -webkit-appearance: none;
  appearance: none;
  width: 110px;
  height: 44px;
  background: transparent;
  cursor: pointer;
  margin: 0;
}

.pslider::-webkit-slider-runnable-track {
  height: 4px;
  border-radius: 999px;
  background-color: var(--overlay-10);
  background-image: var(--grad);
  background-size: var(--pfill, 0%) 100%;
  background-repeat: no-repeat;
}

.pslider::-webkit-slider-thumb {
  -webkit-appearance: none;
  width: 15px;
  height: 15px;
  border-radius: 50%;
  background: #fff;
  box-shadow: none;
  margin-top: -5.5px;
}

.pslider::-moz-range-track {
  height: 4px;
  border-radius: 999px;
  background-color: var(--overlay-10);
}

.pslider::-moz-range-progress {
  height: 4px;
  border-radius: 999px;
  background: var(--grad);
}

.pslider::-moz-range-thumb {
  width: 15px;
  height: 15px;
  border: none;
  border-radius: 50%;
  background: #fff;
  box-shadow: none;
}

.psheet-val {
  min-width: 38px;
  text-align: right;
  font-family: var(--font-mono);
  font-size: 12.5px;
  font-weight: 700;
  color: var(--text-primary);
}

/* Stepper (frame ⑤ .pstepper): 32px visual circle inside a 44px touch box
   (background-clip keeps the painted circle at 32px under the padding). */
.pstepper {
  display: flex;
  align-items: center;
  gap: 2px;
  flex-shrink: 0;
}

.pstep-btn {
  width: 44px;
  height: 44px;
  padding: 6px;
  display: flex;
  align-items: center;
  justify-content: center;
  background: var(--bg-card);
  background-clip: content-box;
  border: none;
  border-radius: 50%;
  color: var(--text-secondary);
  font-size: 16px;
  font-weight: 700;
  line-height: 1;
  cursor: pointer;
}

.pstep-btn:active {
  color: var(--accent-light);
}

.pstep-val {
  min-width: 56px;
  text-align: center;
  font-family: var(--font-mono);
  font-size: 12.5px;
  font-weight: 700;
  color: var(--text-primary);
}

/* System prompt textarea (frame ⑤ .ptext) */
.ptext {
  width: 100%;
  padding: 8px 11px;
  background: var(--bg-card);
  border: none;
  border-radius: var(--r-md);
  color: var(--text-primary);
  font-size: 12px;
  font-family: var(--font-sans);
  line-height: 1.5;
  outline: none;
  resize: vertical;
  min-height: 44px;
}

.ptext::placeholder {
  color: var(--text-dim);
}

/* ─── Tablet portrait Track A (768..1099px), frame A⑤ half: the params
       overlay is a CENTERED MODAL CARD (560px) floating on the dim backdrop —
       not the phone's full-width bottom sheet. The class is applied only
       while paramsLayout is 'modal', i.e. exactly this band. (The chip-dot
       half of the original Track A block stays in Chat.vue.) ─── */
@media (min-width: 768px) and (max-width: 1099px) {
  .params-sheet--modal {
    left: 50%;
    right: auto;
    bottom: auto;
    top: 90px;
    transform: translateX(-50%);
    width: min(560px, calc(100vw - 48px));
    max-height: calc(100vh - 120px);
    max-height: calc(100dvh - 120px);
    animation: modal-pop 0.2s ease;
  }

  /* The draft's centered card has no grab handle (that is the sheet affordance) */
  .params-sheet--modal .params-grab {
    display: none;
  }
}

@keyframes modal-pop {
  from {
    opacity: 0;
    transform: translateX(-50%) scale(0.96);
  }
  to {
    opacity: 1;
    transform: translateX(-50%) scale(1);
  }
}

</style>
