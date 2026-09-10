<template>
  <!-- Single root: the scroll container itself (the class fallthrough is not
       used, but keeping one root matches every other component here). -->
  <div
    ref="messagesEl"
    class="messages-area"
    :class="{
      'messages-area--streaming': streaming,
      'messages-area--blocked': blocked,
    }"
    @click="handleLinkClick"
    @auxclick="handleLinkAuxClick"
    @dragstart="handleLinkDragStart"
  >
    <!-- Messages area. Extracted from Chat.vue (behavior-neutral refactor):
         the scroll container, empty states, message bubbles, reasoning blocks
         and their scoped styles moved here; Chat.vue keeps the data flow and
         reaches the stick-to-bottom scrolling through defineExpose. -->
    <!-- Delegated link handler: links in assistant markdown open in the system
         browser, the WebView never navigates — left click, middle click and
         drag included (see lib/linkHandler.ts) -->
    <!-- Slot: content rendered at the top of the conversation. Chat.vue puts
         the tablet inline precheck banner here, mirroring the original DOM
         order (banner → empty states → message rows); nothing renders on
         desktop/phone. -->
    <slot></slot>
    <!-- Empty states (design frames ⑤⑦ .emptystate): two-line structure with
         an emoji mark on the phone tier; the desktop keeps the original
         single-line text (icon + sub-line are display:none there). -->
    <div v-if="!hasModels" class="empty-hint">
      <span class="empty-ico" aria-hidden="true">📦</span>
      <b class="empty-title">{{ mobileTier ? t('chat.noModelsTitle') : t('chat.noModels') }}</b>
      <span class="empty-sub">{{ t('chat.noModelsSub') }}</span>
    </div>
    <template v-else>
      <div v-if="messages.length === 0" class="empty-hint">
        <span class="empty-ico" aria-hidden="true">💬</span>
        <b class="empty-title">{{ t('chat.emptyHint') }}</b>
        <span class="empty-sub">{{ t('chat.emptySub') }}</span>
      </div>
      <div
        v-for="(msg, idx) in messages"
        :key="idx"
        class="message-row"
        :class="msg.role === 'user' ? 'is-user' : 'is-assistant'"
      >
        <div class="message-bubble">
          <!-- Design frame ②: only assistant bubbles carry a small header —
               the answering model's display name (user bubbles are identified
               by position + the gradient skin) -->
          <span v-if="msg.role === 'assistant'" class="message-role">{{ assistantLabel }}</span>
          <!-- Reasoning (thinking) block, assistant messages with thinking output only -->
          <div v-if="msg.reasoning" class="reasoning-block" :class="{ expanded: isReasoningExpanded(idx, msg) }">
            <button class="reasoning-header" type="button" @click="toggleReasoning(idx)">
              <span>{{ thinkingLabel(idx, msg) }}</span>
              <svg class="reasoning-chevron" width="12" height="12" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round" stroke-linejoin="round">
                <polyline points="6 9 12 15 18 9"/>
              </svg>
            </button>
            <!-- Ref/scroll wiring is bound to the last message only: that is the
                 streaming stick-to-bottom target (see setReasoningBodyRef) -->
            <div
              v-if="isReasoningExpanded(idx, msg)"
              class="reasoning-body"
              :ref="idx === messages.length - 1 ? setReasoningBodyRef : undefined"
              @scroll="onReasoningScroll"
            >{{ msg.reasoning }}</div>
          </div>
          <!-- Images (attached to user messages) -->
          <div v-if="msg.images && msg.images.length" class="message-images">
            <img v-for="(img, i) in msg.images" :key="i" :src="img" class="message-image" alt="" />
          </div>
          <!-- Assistant output renders as markdown (raw HTML escaped by
               renderMarkdown); user input stays plain text with preserved breaks -->
          <div
            v-if="msg.role === 'assistant'"
            class="message-content markdown-body"
            v-html="renderMarkdown(msg.content)"
          ></div>
          <p v-else class="message-content">{{ msg.content }}</p>
          <!-- Streaming state (last assistant bubble only): breathing typing
               dots while no answer text has landed yet, then a small
               "Generating… · N tok/s" meta line fed by the live per-stream
               counters; the existing statsLine takes over once the stream
               ends. Rendering only — the stream wiring stays in Chat.vue. -->
          <template v-if="idx === messages.length - 1 && streaming && msg.role === 'assistant'">
            <div v-if="!msg.content" class="typing-dots" aria-hidden="true"><i /><i /><i /></div>
            <div class="stream-meta">{{ streamMetaLine }}</div>
          </template>
          <span v-if="idx === messages.length - 1 && streaming" class="streaming-cursor" />
          <!-- Per-phase token rates footer, present after streaming ends -->
          <div v-if="statsLine(msg)" class="message-stats">{{ statsLine(msg) }}</div>
        </div>
      </div>
    </template>
  </div>
</template>

<script setup lang="ts">
import { computed, nextTick, ref, type ComponentPublicInstance } from 'vue'
import type { ChatMessage } from '../lib/chatState'
import { t } from '../lib/i18n'
import { renderMarkdown } from '../lib/markdown'
import { handleLinkClick, handleLinkAuxClick, handleLinkDragStart } from '../lib/linkHandler'
import { isNearBottom } from '../lib/scroll'

const props = defineProps<{
  /** Conversation messages (module-level chatState array owned by Chat.vue). */
  messages: ChatMessage[]
  /** True while a reply streams: typing dots, cursor and dimmed prior bubbles. */
  streaming: boolean
  /** Phone-tier gate (viewport width <= 767, reactive): phone-only empty-state copy. */
  mobileTier: boolean
  /** Tablet-tier gate (768..1099, reactive): contributes the touch-tier check. */
  tabletTier: boolean
  /** Tablet precheck blocker on screen: dims the history behind the banner. */
  blocked: boolean
  /** False when the local model directory is empty: shows the download hint. */
  hasModels: boolean
  /** Assistant bubble header label (answering model's display name). */
  assistantLabel: string
  /** Live answer-phase tok/s while the stream runs; null outside streaming. */
  liveAnswerTps: number | null
  /** Live reasoning-phase tok/s shown until the first answer delta lands. */
  liveReasoningTps: number | null
}>()

/** Touch tiers (phone + tablet, reactive across breakpoints) state the
 *  thinking block's live phase in its header; desktop keeps the static label. */
const touchTier = computed(() => props.mobileTier || props.tabletTier)

/** Streaming meta copy: "Generating…" plus the active phase's live tok/s. */
const streamMetaLine = computed<string>(() => {
  const tps = props.liveAnswerTps ?? props.liveReasoningTps
  return tps !== null ? `${t('chat.generating')} · ${tps.toFixed(1)} tok/s` : t('chat.generating')
})

/** One-line stats footer, e.g. "思考 45.2 tok/s · 生成 38.6 tok/s"; each part shown only when defined. */
function statsLine(msg: ChatMessage): string {
  if (!msg.stats) return ''
  const parts: string[] = []
  if (msg.stats.reasoningTps !== undefined) parts.push(t('chat.statsThinking', { v: msg.stats.reasoningTps.toFixed(1) }))
  if (msg.stats.answerTps !== undefined) parts.push(t('chat.statsAnswer', { v: msg.stats.answerTps.toFixed(1) }))
  return parts.join(' · ')
}

/** Explicit user toggles of reasoning-block expansion, keyed by message index (component-local; never persisted) */
const reasoningExpanded = ref<Record<number, boolean>>({})

/**
 * Effective reasoning-block expansion: an explicit user toggle wins; otherwise the
 * block auto-expands only while streaming the last assistant message that has
 * reasoning but no answer content yet, and auto-collapses once content starts
 * arriving or streaming ends.
 */
function isReasoningExpanded(idx: number, msg: ChatMessage): boolean {
  if (idx in reasoningExpanded.value) return reasoningExpanded.value[idx]
  return idx === props.messages.length - 1 && props.streaming && !!msg.reasoning && !msg.content
}

/** Toggle the reasoning block, recording an explicit override for this message. */
function toggleReasoning(idx: number) {
  const msg = props.messages[idx]
  if (!msg) return
  reasoningExpanded.value[idx] = !isReasoningExpanded(idx, msg)
}

/**
 * Reasoning-block header copy (frame ⑥ .think): the touch tiers state the
 * block's live phase — "deep thinking" while reasoning deltas stream in with
 * no answer text yet, "deep thought" once done; desktop keeps the original
 * static label.
 */
function thinkingLabel(idx: number, msg: ChatMessage): string {
  if (!touchTier.value) return t('chat.thinking')
  const active = idx === props.messages.length - 1 && props.streaming && !!msg.reasoning && !msg.content
  return active ? t('chat.thinkingActive') : t('chat.thinkingDone')
}

// ─── Reasoning body stick-to-bottom ─────────────────────────────────────────

/** Reasoning-body element of the last message (stick-to-bottom scroll target); null when collapsed or unmounted */
const reasoningBodyEl = ref<HTMLDivElement | null>(null)

/** Whether the reasoning body is pinned to its bottom; flipped false when the user scrolls up to read earlier thinking */
const reasoningStuck = ref(true)

/**
 * Function ref for the last message's reasoning body: captures the element and
 * resets the stick state when a NEW element appears (a fresh block starts
 * pinned). Vue re-invokes function refs on every patch with the same element,
 * so the identity guard keeps per-delta re-invocations from resetting a
 * user-scrolled-up state. Element-null transitions (collapse, message stops
 * being last, component unmount/navigation) simply clear the capture.
 */
function setReasoningBodyRef(el: Element | ComponentPublicInstance | null): void {
  const dom = el instanceof HTMLDivElement ? el : null
  if (dom === reasoningBodyEl.value) return
  reasoningBodyEl.value = dom
  reasoningStuck.value = true
}

/** Record near-bottom state from user scrolling; ignores bodies other than the captured target. */
function onReasoningScroll(e: Event) {
  const el = reasoningBodyEl.value
  if (!el || e.target !== el) return
  reasoningStuck.value = isNearBottom(el.scrollTop, el.scrollHeight, el.clientHeight)
}

/**
 * Keep the expanded reasoning body pinned to its bottom while reasoning deltas
 * stream in — but only while the user is themselves near the bottom, so a user
 * reading earlier thinking is never yanked around. Runs after nextTick so the
 * DOM (and scrollHeight) reflects the appended delta; the null check follows
 * scrollToBottom's style and guards element absence after unmount/navigation.
 * Exposed: Chat.vue calls it from the reasoning stream callback.
 */
function scrollReasoningToBottom() {
  nextTick(() => {
    const el = reasoningBodyEl.value
    if (el && reasoningStuck.value) el.scrollTop = el.scrollHeight
  })
}

/** Clear explicit reasoning expansion overrides (Chat.vue's clear conversation flow). */
function clearReasoningExpansion(): void {
  reasoningExpanded.value = {}
}

/** Scroll container element (the stick-to-bottom target). */
const messagesEl = ref<HTMLDivElement | null>(null)

/**
 * Pin the conversation to its newest message (Chat.vue calls this after
 * appending a user/assistant message and on every streamed delta). Runs after
 * nextTick so the DOM (and scrollHeight) reflects the appended content; the
 * null check guards element absence after unmount/navigation.
 */
function scrollToBottom() {
  nextTick(() => {
    const el = messagesEl.value
    if (el) el.scrollTop = el.scrollHeight
  })
}

defineExpose({
  scrollToBottom,
  scrollReasoningToBottom,
  clearReasoningExpansion,
})
</script>

<style scoped>
/* ─── Messages ─── */
.messages-area {
  flex: 1;
  /* min-height: 0 lets this flex child shrink below its content size so
     overflow-y scrolling kicks in; with the default min-height: auto the
     growing conversation pushes .input-area out of the viewport instead */
  min-height: 0;
  overflow-y: auto;
  padding: 8px 0 16px;
}

.empty-hint {
  text-align: center;
  padding: 48px 0;
  color: var(--text-dim);
  font-size: 14px;
  display: flex;
  flex-direction: column;
  align-items: center;
  gap: 8px;
}

.empty-ico {
  font-size: 32px;
  line-height: 1;
}

.empty-title {
  display: block;
  font-size: 15px;
  font-weight: 700;
  color: var(--text-primary);
}

.empty-sub {
  display: block;
  font-size: 13px;
  color: var(--text-muted);
  line-height: 1.6;
}

/* ─── Message bubbles (design frame ②) ───
   User: brand gradient, white text, small radius tucked at the sender corner
   (22/22/6/22). Assistant: lifted island surface, mirrored radius
   (22/22/22/6), small model-name header on top. Colors ride the theme tokens
   so the dark mapping (lifted #161622 family) comes for free. */
.message-row {
  display: flex;
  margin-bottom: 14px;
}

.message-row.is-user {
  justify-content: flex-end;
}

.message-row.is-assistant {
  justify-content: flex-start;
}

.message-bubble {
  max-width: 78%;
  padding: 12px 16px;
  /* No blanket pre-wrap: markdown output manages its own spacing (code must
     not wrap); plain-text spots scope pre-wrap individually */
  font-size: 13.5px;
  line-height: 1.75;
  font-weight: 400;
  word-break: break-word;
}

.is-user .message-bubble {
  background: var(--grad);
  color: #fff;
  border-radius: var(--r-md) var(--r-md) 6px var(--r-md);
  box-shadow: none;
}

.is-assistant .message-bubble {
  background: var(--bg-secondary);
  border: 1px solid var(--border);
  border-radius: var(--r-md) var(--r-md) var(--r-md) 6px;
  box-shadow: var(--shadow-island);
  color: var(--text-primary);
}

/* Assistant-only header: the answering model's display name (design .who) */
.message-role {
  display: block;
  font-size: 10.5px;
  font-weight: 700;
  letter-spacing: 0.4px;
  color: var(--text-muted);
  margin-bottom: 5px;
  user-select: none;
}

.message-images {
  display: flex;
  flex-wrap: wrap;
  gap: 8px;
  margin-bottom: 8px;
}

.message-image {
  max-width: 100%;
  border-radius: 8px;
  max-height: 280px;
  object-fit: contain;
}

.message-content {
  margin: 0;
}

/* User input stays plain text: preserve explicit line breaks */
.is-user .message-content {
  white-space: pre-wrap;
}

/* ─── Markdown rendering (assistant bubbles) ───
   v-html content does not carry the scope attribute, so child selectors need
   :deep(). Sizes are relative to the 13px bubble text; colors ride the theme
   CSS variables so light/dark both work. */
.markdown-body :deep(h1),
.markdown-body :deep(h2),
.markdown-body :deep(h3),
.markdown-body :deep(h4),
.markdown-body :deep(h5),
.markdown-body :deep(h6) {
  margin: 12px 0 6px;
  line-height: 1.35;
  color: var(--text-primary);
}

.markdown-body :deep(h1) { font-size: 1.35em; }
.markdown-body :deep(h2) { font-size: 1.2em; }
.markdown-body :deep(h3) { font-size: 1.08em; }
.markdown-body :deep(h4),
.markdown-body :deep(h5),
.markdown-body :deep(h6) { font-size: 1em; }

.markdown-body :deep(p) {
  margin: 6px 0;
}

.markdown-body :deep(ul),
.markdown-body :deep(ol) {
  margin: 6px 0;
  padding-left: 1.4em;
}

.markdown-body :deep(li) {
  margin: 2px 0;
}

.markdown-body :deep(li > ul),
.markdown-body :deep(li > ol) {
  margin: 2px 0;
}

/* Inline code: subtle inset chip */
.markdown-body :deep(code) {
  background: var(--overlay-8);
  border: 1px solid var(--border-light);
  border-radius: 4px;
  padding: 0.5px 5px;
  font-size: 0.92em;
  word-break: break-word;
}

/* Fenced code blocks: monospace via the global code/pre rule; long lines
   scroll horizontally instead of wrapping */
.markdown-body :deep(pre) {
  margin: 8px 0;
  padding: 10px 12px;
  background: var(--bg-primary);
  border: 1px solid var(--border);
  border-radius: 8px;
  overflow-x: auto;
}

.markdown-body :deep(pre code) {
  background: transparent;
  border: none;
  padding: 0;
  font-size: 0.95em;
  line-height: 1.5;
  white-space: pre;
  word-break: normal;
}

.markdown-body :deep(blockquote) {
  margin: 8px 0;
  padding: 2px 0 2px 12px;
  border-left: 3px solid var(--accent-glow);
  color: var(--text-secondary);
}

.markdown-body :deep(blockquote p) {
  margin: 4px 0;
}

/* Tables: block + auto scroll so wide tables stay inside the bubble */
.markdown-body :deep(table) {
  display: block;
  margin: 8px 0;
  border-collapse: collapse;
  overflow-x: auto;
  max-width: 100%;
}

.markdown-body :deep(th),
.markdown-body :deep(td) {
  border: 1px solid var(--border);
  padding: 4px 10px;
  text-align: left;
}

.markdown-body :deep(th) {
  background: var(--surface);
  font-weight: 600;
}

.markdown-body :deep(a) {
  color: var(--accent-light);
}

.markdown-body :deep(a:hover) {
  text-decoration: underline;
}

.markdown-body :deep(img) {
  max-width: 100%;
  height: auto;
}

.markdown-body :deep(hr) {
  border: none;
  border-top: 1px solid var(--border);
  margin: 12px 0;
}

/* Avoid doubled spacing where the markdown content meets the bubble padding */
.markdown-body > :deep(:first-child) {
  margin-top: 0;
}

.markdown-body > :deep(:last-child) {
  margin-bottom: 0;
}

/* ─── Reasoning (thinking) block ─── */
.reasoning-block {
  margin-bottom: 8px;
}

.reasoning-header {
  display: inline-flex;
  align-items: center;
  gap: 4px;
  background: transparent;
  border: none;
  padding: 0;
  color: var(--text-muted);
  font-size: 12px;
  font-weight: 600;
  cursor: pointer;
  user-select: none;
}

.reasoning-header:hover {
  color: var(--text-secondary);
}

.reasoning-chevron {
  transition: transform 0.2s;
}

.reasoning-block.expanded .reasoning-chevron {
  transform: rotate(180deg);
}

.reasoning-body {
  margin-top: 4px;
  /* Slightly smaller and muted so long thinking stays secondary to the answer */
  font-size: 12.5px;
  line-height: 1.5;
  font-weight: 500;
  color: var(--text-secondary);
  white-space: pre-wrap;
  max-height: 220px;
  overflow-y: auto;
}

/* ─── Token rate stats footer ─── */
.message-stats {
  margin-top: 6px;
  font-size: 11px;
  color: var(--text-dim);
  user-select: none;
}

/* ─── Streaming typing indicator (design .typing) ───
   Three breathing dots shown in the streaming bubble before the first answer
   text lands; the meta line under it carries the live per-stream tok/s. */
.typing-dots {
  display: inline-flex;
  gap: 4px;
  padding: 4px 0;
}

.typing-dots i {
  width: 7px;
  height: 7px;
  border-radius: 50%;
  background: var(--text-muted);
  animation: typing-breathe 1.2s infinite;
}

.typing-dots i:nth-child(2) {
  animation-delay: 0.2s;
}

.typing-dots i:nth-child(3) {
  animation-delay: 0.4s;
}

@keyframes typing-breathe {
  0%, 60%, 100% { transform: translateY(0); opacity: 0.5; }
  30% { transform: translateY(-4px); opacity: 1; }
}

/* Live "Generating… · N tok/s" line under the streaming bubble */
.stream-meta {
  margin-top: 7px;
  font-size: 10.5px;
  font-weight: 600;
  color: var(--text-muted);
  user-select: none;
}

/* ─── Streaming cursor ─── */
.streaming-cursor {
  display: inline-block;
  width: 6px;
  height: 14px;
  margin-left: 2px;
  vertical-align: text-bottom;
  background: var(--accent-light);
  border-radius: 1px;
  animation: blink 1s steps(2) infinite;
}

@keyframes blink {
  0%, 100% { opacity: 1; }
  50% { opacity: 0; }
}

/* ─── Mobile (<=767px): compact bubbles, two-line empty states, streaming
       polish and the thinking-block card skin ─── */
@media (max-width: 767px) {
  .message-bubble {
    max-width: 88%;
  }

  /* Two-line empty states (frame ⑦ .emptystate): emoji mark + bold title +
     muted sub-caption */
  .empty-hint {
    display: block;
    padding: 60px 20px 0;
    color: var(--text-muted);
  }

  .empty-ico {
    display: block;
    font-size: 40px;
    margin-bottom: 12px;
  }

  .empty-title {
    display: block;
    font-size: 15px;
    font-weight: 700;
    color: var(--text-secondary);
  }

  .empty-sub {
    display: block;
    margin-top: 6px;
    font-size: 12.5px;
    line-height: 1.6;
    color: var(--text-muted);
  }

  /* Streaming polish (frame ⑥): finished assistant bubbles recede while a
     new reply streams; stats/meta numerals go mono; stats line gets the
     dashed hairline; attached images round to 12px */
  .messages-area--streaming .message-row.is-assistant:not(:last-child) .message-bubble {
    opacity: 0.72;
  }

  .stream-meta {
    font-family: var(--font-mono);
  }

  .message-stats {
    font-size: 10.5px;
    margin-top: 8px;
    padding-top: 6px;
    border-top: 1px dashed var(--border);
    font-family: var(--font-mono);
  }

  .message-image {
    border-radius: var(--r-sm);
  }

  /* Thinking block container (frame ⑥ .think): bordered inset card with the
     11/700 state header and 11.5/1.7 muted body */
  .reasoning-block {
    border: 1px solid var(--border);
    border-radius: var(--r-sm);
    background: var(--bg-card);
    padding: 10px 12px;
  }

  html[data-theme='dark'] .reasoning-block {
    background: var(--surface-2);
  }

  .reasoning-header {
    font-size: 11px;
    font-weight: 700;
  }

  .reasoning-body {
    margin-top: 4px;
    font-size: 11.5px;
    line-height: 1.7;
    color: var(--text-muted);
  }
}

/* ─── Tablet (768..1099px): a comfortable centered message column instead of
       full-bleed bubbles; the sidebar rail keeps the surrounding chrome.
       No-op above 1099px (desktop keeps the wide layout). (.input-area's half
       of the original shared rule stays in Chat.vue.) ─── */
@media (min-width: 768px) and (max-width: 1099px) {
  .messages-area {
    width: 100%;
    max-width: 800px;
    margin-left: auto;
    margin-right: auto;
  }
}

/* ─── Tablet portrait Track A (768..1099px; tablet draft frames A⑤/A⑥/A⑦).
   Scoped to the band with min-width: 768px so phones (<=767) and desktop
   (>=1100px) stay untouched. ─── */
@media (min-width: 768px) and (max-width: 1099px) {
  /* Frame A⑤ .emptystate: emoji mark + bold title + muted sub-caption, like
     the phone tier's two-line empty states */
  .empty-hint {
    display: block;
    padding: 60px 20px 0;
    color: var(--text-muted);
  }

  .empty-ico {
    display: block;
    font-size: 40px;
    margin-bottom: 12px;
  }

  .empty-title {
    display: block;
    font-size: 15px;
    font-weight: 700;
    color: var(--text-secondary);
  }

  .empty-sub {
    display: block;
    margin-top: 6px;
    font-size: 12.5px;
    line-height: 1.6;
    color: var(--text-muted);
  }

  /* Frame A⑥ .think: bordered inset card with the 11/700 state header and the
     11.5/1.7 muted body (same treatment as the phone band) */
  .reasoning-block {
    border: 1px solid var(--border);
    border-radius: var(--r-sm);
    background: var(--bg-card);
    padding: 10px 12px;
  }

  html[data-theme='dark'] .reasoning-block {
    background: var(--surface-2);
  }

  .reasoning-header {
    font-size: 11px;
    font-weight: 700;
  }

  .reasoning-body {
    margin-top: 4px;
    font-size: 11.5px;
    line-height: 1.7;
    color: var(--text-muted);
  }

  /* Frame A⑥: numerals go mono, stats footer gets the dashed hairline */
  .stream-meta {
    font-family: var(--font-mono);
  }

  .message-stats {
    font-size: 10.5px;
    margin-top: 8px;
    padding-top: 6px;
    border-top: 1px dashed var(--border);
    font-family: var(--font-mono);
  }

  /* Frame A⑦: history dims behind the inline banner */
  .messages-area--blocked .message-row {
    opacity: 0.45;
  }
}
</style>
