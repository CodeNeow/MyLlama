<template>
  <!-- Single root div: the parent adds `.chat-precheck-stack` via the class
       fallthrough for the tablet inline placement, which requires a single
       root element. -->
  <div class="start-notice-stack" role="status">
    <!-- Auto-start / model-switch notices (frame ⑦ .notify): an ARRAY model —
         starting, switching and error flags are independent, so several cards
         can stack. Extracted verbatim from Chat.vue (both placements): the
         desktop keeps the single pill in flow; the phone tier floats the
         stack above the composer as anchored cards (media-scoped); tablet
         tiers render the same notices INLINE at the top of the conversation
         (the .chat-precheck-stack copy inside the messages area). Notice
         state (starting / switching / error flags) stays in Chat.vue and
         arrives through props; the guided-fix buttons report back through
         emits. -->
    <div
      v-for="notice in notices"
      :key="notice.kind"
      class="start-notice"
      :class="{ 'start-notice--error': notice.kind === 'error' }"
    >
      <template v-if="notice.kind === 'error'">
        <span class="start-notice-text">{{ notice.text }}</span>
        <button v-if="notice.cause === 'needModels'" class="start-notice-btn" @click="emit('goDownloads')">
          {{ t('action.gotoDownloads') }}
        </button>
        <button v-else-if="notice.cause === 'needRuntime'" class="start-notice-btn" @click="emit('goRuntime')">
          {{ t('chat.goRuntime') }}
        </button>
      </template>
      <template v-else>
        <span v-if="notice.kind === 'starting'" class="start-notice-spinner" aria-hidden="true"></span>
        <span>{{ notice.text }}</span>
      </template>
    </div>
  </div>
</template>

<script setup lang="ts">
import { t } from '../lib/i18n'

// One floating notice card (moved verbatim from Chat.vue's PageNotice): error
// cards carry the guided-fix cause; 'info' cards are transient model-list
// notices (#33). Exported so Chat.vue's activeNotices computed keeps a single
// shared type for both placements.
export interface ChatNotice {
  kind: 'starting' | 'switching' | 'error' | 'info'
  text: string
  cause: '' | 'needModels' | 'needRuntime'
}

defineProps<{
  /** Notice stack to render (Chat.vue maps its start/switch/error state). */
  notices: ChatNotice[]
}>()

const emit = defineEmits<{
  /** Guided fix: the model directory is empty → Downloads tab. */
  goDownloads: []
  /** Guided fix: llama.cpp runtime missing → Runtime Environment tab. */
  goRuntime: []
}>()
</script>

<style scoped>
/* ─── Auto-start / model-switch notice (glass pill above the composer) ─── */
.start-notice {
  display: flex;
  align-items: center;
  gap: 8px;
  padding: 8px 14px;
  border-radius: 999px;
  font-size: 12px;
  font-weight: 500;
  color: var(--text-secondary);
  background: var(--glass);
  border: 1px solid var(--glass-line);
}

/* Error variant: message + guided CTA sharing the .stop-btn color family
   (rgba overlays stay readable on both light and dark themes) */
.start-notice--error {
  justify-content: space-between;
  color: #f87171;
  background: rgba(239, 68, 68, 0.08);
  border-color: rgba(239, 68, 68, 0.25);
}

.start-notice-text {
  flex: 1;
  min-width: 0;
  word-break: break-word;
}

.start-notice-btn {
  padding: 4px 12px;
  border-radius: 6px;
  font-size: 12px;
  font-weight: 600;
  cursor: pointer;
  transition: all 0.2s;
  flex-shrink: 0;
  background: rgba(239, 68, 68, 0.12);
  color: #f87171;
  border: 1px solid rgba(239, 68, 68, 0.3);
}

.start-notice-btn:hover {
  background: rgba(239, 68, 68, 0.2);
}

.start-notice-spinner {
  width: 12px;
  height: 12px;
  border-radius: 50%;
  border: 2px solid var(--border);
  border-top-color: var(--accent-light);
  animation: notice-spin 0.8s linear infinite;
  flex-shrink: 0;
}

@keyframes notice-spin {
  to { transform: rotate(360deg); }
}

/* ─── Tablet precheck banner (tablet draft frames A⑦/B⑦) ───
   Rendered inline at the top of the conversation, filling the content
   column; only mounts behind the isTabletTier gate, so these base styles
   never apply on desktop/phone. Tier card skins live in the Track A band.
   The class is added by Chat.vue through the class fallthrough on the
   component tag (the stack root stays a single element). */
.chat-precheck-stack {
  display: flex;
  flex-direction: column;
  gap: 8px;
  margin-bottom: 12px;
}

@media (max-width: 767px) {
  /* Precheck notices float as stacked cards above the composer band (frame ⑦
     .notify): anchored to the input-area's top edge, inside the page's
     symmetric 16px gutters (the phone tier reserves no dock lane). The
     positioned ancestor is .input-area (position: relative in Chat.vue's
     phone block) — unchanged by the extraction. */
  .start-notice-stack {
    position: absolute;
    bottom: calc(100% + 8px);
    left: 0;
    right: 0;
    z-index: 5;
    display: flex;
    flex-direction: column;
    gap: 8px;
  }

  .start-notice {
    flex-wrap: wrap;
    background: var(--bg-card);
    border: none;
    border-radius: var(--r-md);
    padding: 10px 14px;
    box-shadow: var(--shadow-island);
    font-size: 12px;
    font-weight: 600;
    color: var(--text-secondary);
  }

  html[data-theme='dark'] .start-notice {
    background: var(--surface-2);
  }

  .start-notice--error {
    background: var(--danger-bg);
    color: #b91c1c;
  }

  html[data-theme='dark'] .start-notice--error {
    color: #fca5a5;
  }

  .start-notice-btn {
    background: transparent;
    border: none;
    padding: 8px 0 8px 10px;
    font-size: 12px;
    font-weight: 700;
    color: var(--accent-light);
  }

  .start-notice-btn:hover {
    background: transparent;
    text-decoration: underline;
  }

  /* Spinner card (frame ⑦ .spin): 13px ring, purple top arc */
  .start-notice-spinner {
    width: 13px;
    height: 13px;
    border: 2px solid var(--border);
    border-top-color: var(--accent-light);
    animation-duration: 1s;
  }
}

/* ─── Tablet portrait Track A (768..1099px; tablet draft frame A⑦). Scoped to
   the band with min-width: 768px so phones (<=767) and desktop (>=1100px)
   stay untouched. ─── */
@media (min-width: 768px) and (max-width: 1099px) {
  /* Frame A⑦ .notify: precheck banner fills the content column as a card */
  .chat-precheck-stack .start-notice {
    background: var(--bg-card);
    border: none;
    border-radius: var(--r-md);
    padding: 10px 14px;
    box-shadow: var(--shadow-island);
    font-size: 12px;
    font-weight: 600;
    color: var(--text-secondary);
  }

  html[data-theme='dark'] .chat-precheck-stack .start-notice {
    background: var(--surface-2);
  }

  .chat-precheck-stack .start-notice--error {
    background: var(--danger-bg);
    color: #b91c1c;
  }

  html[data-theme='dark'] .chat-precheck-stack .start-notice--error {
    color: #fca5a5;
  }

  .chat-precheck-stack .start-notice-btn {
    background: transparent;
    border: none;
    padding: 8px 0 8px 10px;
    font-size: 12px;
    font-weight: 700;
    color: var(--accent-light);
  }

  .chat-precheck-stack .start-notice-btn:hover {
    background: transparent;
    text-decoration: underline;
  }

  /* Frame A⑦ .spin */
  .chat-precheck-stack .start-notice-spinner {
    width: 13px;
    height: 13px;
    border: 2px solid var(--border);
    border-top-color: var(--accent-light);
    animation-duration: 1s;
  }
}
</style>
