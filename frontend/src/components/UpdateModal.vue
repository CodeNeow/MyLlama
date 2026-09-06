<template>
  <div v-if="visible" class="modal-overlay" @click.self="close">
    <div class="modal" role="dialog" aria-modal="true" aria-labelledby="update-title">
      <div class="modal-header">
        <h2 id="update-title">{{ t('updateModal.title') }}</h2>
        <span class="modal-version">{{ version }}</span>
        <button ref="closeBtn" class="modal-close" :aria-label="t('updateModal.close')" @click="close">✕</button>
      </div>

      <div class="modal-body">
        <!-- Download in progress / finished -->
        <template v-if="download && (download.status === 'downloading' || download.status === 'done' || download.status === 'installing' || download.status === 'error')">
          <div class="dl-status">
            <!-- Downloading: centered glyph + thick gradient bar + percent + transferred size -->
            <div v-if="download.status === 'downloading'" class="dl-downloading">
              <div class="dl-glyph" aria-hidden="true">
                <svg width="20" height="20" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round" stroke-linejoin="round">
                  <path d="M21 15v4a2 2 0 0 1-2 2H5a2 2 0 0 1-2-2v-4"/><polyline points="7 10 12 15 17 10"/><line x1="12" y1="15" x2="12" y2="3"/>
                </svg>
              </div>
              <p class="dl-version">{{ t('updateModal.downloading', { version: download.version }) }}</p>
              <div class="dl-progress-bar">
                <div class="dl-progress-fill" :style="{ width: download.progress + '%' }"></div>
              </div>
              <span class="dl-percent">{{ download.progress }}%</span>
              <p v-if="download.total > 0" class="dl-size">{{ formatBytes(download.downloaded) }} / {{ formatBytes(download.total) }}</p>
            </div>

            <template v-else-if="download.status === 'done' || download.status === 'installing'">
              <div class="done-msg">
                <span class="ok-icon">✓</span>
                <div>
                  <p class="done-title">{{ t('updateModal.doneTitle') }}</p>
                  <p class="done-detail">{{ t('updateModal.doneDetail') }}</p>
                  <code class="done-path">{{ download.filePath }}</code>
                  <!-- installer artifact: offer the install-now flow (app exits and runs
                       the installer); otherwise manual hints (setup: run the installer
                       later; portable or legacy state (kind empty): replace the exe) -->
                  <p class="done-tip">{{ installTip }}</p>
                  <p v-if="installing" class="done-tip">{{ t('updateModal.installing') }}</p>
                </div>
              </div>
            </template>

            <template v-else-if="download.status === 'error'">
              <p class="err-msg">{{ t('updateModal.downloadFailed', { msg: download.error }) }}</p>
            </template>
          </div>
        </template>

        <!-- Awaiting confirmation -->
        <template v-else>
          <p class="new-version-tip">{{ t('updateModal.newVersionTip') }}</p>
          <!-- Phone tier (frame ⑳ .mchips) shows the BARE values; the
               descriptive labels stay available to assistive tech via the
               aria-labels. Desktop keeps the prefixed chips. -->
          <div class="meta">
            <span class="meta-chip" :aria-label="t('updateModal.newVersion', { version })">
              {{ isMobileTier ? version : t('updateModal.newVersion', { version }) }}
            </span>
            <span
              v-if="published"
              class="meta-chip plain"
              :aria-label="t('updateModal.published', { date: formatDate(published) })"
            >
              {{ isMobileTier ? formatDate(published) : t('updateModal.published', { date: formatDate(published) }) }}
            </span>
          </div>
          <div v-if="notes" class="release-notes">
            <h3 class="notes-title">{{ t('updateModal.notesTitle') }}</h3>
            <!-- Compact tiers render the parsed bullet digest as a list
                 (frame ⑳ .relnotes, phones and both tablet tracks); desktop
                 keeps the raw preformatted body -->
            <ul v-if="compactTier && noteBullets.length" class="notes-list">
              <li v-for="(bullet, i) in noteBullets" :key="i">{{ bullet }}</li>
            </ul>
            <pre v-else class="notes-body">{{ notes }}</pre>
          </div>
        </template>
      </div>

      <div class="modal-footer">
        <span v-if="download && download.status === 'error'" class="footer-msg footer-err">{{ download.error }}</span>
        <!-- Downloading: cancel the download, or hide the modal and continue in the background -->
        <template v-if="downloading">
          <button class="btn-cancel" @click="cancelUpdateDownload">{{ t('updateModal.cancelDownload') }}</button>
          <button class="btn-save" @click="close">{{ t('updateModal.hideDownload') }}</button>
        </template>
        <!-- Done: acknowledge (manual install later), or install now (installer artifact only) -->
        <template v-else-if="download && (download.status === 'done' || download.status === 'installing')">
          <span v-if="installError" class="footer-msg footer-err">{{ installError }}</span>
          <button class="btn-cancel" :disabled="installing" @click="close">{{ t('updateModal.gotIt') }}</button>
          <button v-if="download.installer" class="btn-save" :disabled="installing" @click="installNow">{{ t('updateModal.installNow') }}</button>
        </template>
        <!-- Confirm view and error view share the close / start(retry) actions;
             the confirm view (no download state yet) uses the softer "later"
             copy (frame ⑳ .mbtns), the error view keeps "cancel" -->
        <template v-else>
          <button class="btn-cancel" @click="close">{{ download ? t('updateModal.cancel') : t('updateModal.later') }}</button>
          <button class="btn-save" @click="downloadNow">{{ t('updateModal.download') }}</button>
        </template>
      </div>
    </div>
  </div>
</template>

<script lang="ts">
/**
 * Release-notes display helpers (compact tiers' frame ⑳ .relnotes digest):
 * the bilingual markdown body is reduced to plain bullet strings for a <ul>
 * list. Pure and exported from the plain script block so vitest can exercise
 * them directly.
 */

/** Strip inline markdown emphasis/code/link syntax, keeping the label text. */
export function stripInlineMarkdown(text: string): string {
  return text
    .replace(/`([^`]*)`/g, '$1') // inline code
    .replace(/\*\*([^*]*)\*\*/g, '$1') // bold
    .replace(/\*([^*]*)\*/g, '$1') // emphasis
    .replace(/\[([^\]]*)\]\([^)]*\)/g, '$1') // links keep their label
    .trim()
}

/**
 * Line-based bullet extraction: only "- " list items are kept, emphasis
 * stripped, empties dropped. Non-bullet lines (prose, headings, blank) are
 * ignored — the modal shows the digest list, not the full document.
 */
export function parseReleaseBullets(notes: string): string[] {
  const out: string[] = []
  for (const rawLine of notes.split(/\r?\n/)) {
    const line = rawLine.trim()
    if (!line.startsWith('- ')) continue
    const text = stripInlineMarkdown(line.slice(2))
    if (text) out.push(text)
  }
  return out
}
</script>

<script setup lang="ts">
import { computed, nextTick, ref, watch } from 'vue'
import { updateState, startUpdateDownload, cancelUpdateDownload, installUpdate, extractReleaseNotes } from '../lib/update'
import { locale, t } from '../lib/i18n'
import { formatBytes } from '../lib/format'
import { usePlatform } from '../lib/platform'

const props = defineProps<{ visible: boolean }>()
const emit = defineEmits<{ close: [] }>()

const closeBtn = ref<HTMLButtonElement | null>(null)

const download = computed(() => updateState.download)
const version = computed(() => updateState.result?.version || '')
const published = computed(() => updateState.result?.published || '')
const notes = computed(() => extractReleaseNotes(updateState.result?.notes || '', locale.value))
const downloading = computed(() => download.value?.status === 'downloading')
const installing = computed(() => updateState.installing)
const installError = computed(() => updateState.installError)

// Phone-tier gate (viewport width <= 767): bare-value chips. Desktop keeps the
// prefixed chips.
const platform = usePlatform()
const isMobileTier = computed(() => platform.value.isMobile)

// Compact-tier gate (phone + whole tablet tier, frame ⑳): the draft gives
// tablets the same modal content model — the release notes render as the
// parsed bullet digest instead of the raw preformatted body. Chip copy stays
// prefixed on tablets (draft A20 chips carry labels; the phone draft's are
// bare), so only the notes gate widens.
const compactTier = computed(() => platform.value.isMobile || platform.value.isTablet)

/** Parsed bullet list for the phone release-notes card (empty when none). */
const noteBullets = computed(() => parseReleaseBullets(notes.value))

// Done-view tip: Android hands the APK to the system installer (dialog
// confirms; cancellable and re-triggerable); other installer artifacts offer
// the desktop install-now flow; the rest keep the manual hint (setup: run the
// installer later; portable/legacy: replace exe)
const installTip = computed(() => {
  if (download.value?.kind === 'android') {
    return updateState.androidInstallSubmitted
      ? t('updateModal.installSubmitted')
      : t('updateModal.installAskAndroid')
  }
  if (download.value?.installer) return t('updateModal.installAsk')
  return download.value?.kind === 'setup' ? t('updateModal.doneTipSetup') : t('updateModal.doneTip')
})

watch(() => props.visible, (v) => {
  if (v) nextTick(() => closeBtn.value?.focus())
})

function formatDate(iso: string): string {
  const d = new Date(iso)
  if (isNaN(d.getTime())) return iso
  return d.toLocaleDateString(locale.value === 'en' ? 'en-US' : 'zh-CN')
}
function downloadNow() {
  startUpdateDownload()
}
async function installNow() {
  await installUpdate()
}

function close() {
  // The app is exiting to run the installer: keep the exiting state visible
  if (updateState.installing) return
  emit('close')
}
</script>

<style scoped>
.modal-overlay {
  position: fixed;
  inset: 0;
  background: rgba(0,0,0,0.6);
  display: flex;
  align-items: center;
  justify-content: center;
  z-index: 1000;
}

.modal {
  /* Glass modal (design draft v2 principle ② / frame ㉑ .modal): translucent
     panel + backdrop blur + glass hairline + deep floating shadow. */
  background: var(--glass);
  backdrop-filter: blur(22px) saturate(1.6);
  -webkit-backdrop-filter: blur(22px) saturate(1.6);
  border: 1px solid var(--glass-line);
  border-radius: var(--r-lg);
  /* min() clamp: the fixed 520px overflowed narrow phone viewports; a no-op
     on viewports >= 552px where it resolves to 520px */
  width: min(520px, calc(100vw - 32px));
  max-height: 80vh;
  display: flex;
  flex-direction: column;
  box-shadow: 0 24px 60px rgba(15, 17, 28, 0.40);
}

.modal-header {
  display: flex;
  align-items: center;
  gap: 12px;
  padding: 20px 24px;
  border-bottom: 1px solid var(--border);
}

.modal-header h2 {
  font-size: 17px;
  font-weight: 700;
  color: var(--text-primary);
  margin: 0;
}

.modal-version {
  font-size: 12px;
  color: var(--text-dim);
  flex: 1;
  overflow: hidden;
  text-overflow: ellipsis;
  white-space: nowrap;
}

.modal-close {
  width: 28px;
  height: 28px;
  border: none;
  background: none;
  color: var(--text-dim);
  font-size: 16px;
  cursor: pointer;
  border-radius: 6px;
  transition: all 0.15s;
}

.modal-close:hover {
  background: var(--hover-bg);
  color: var(--text-primary);
}

.modal-body {
  padding: 20px 24px;
  overflow-y: auto;
  flex: 1;
}

.new-version-tip {
  margin: 0 0 14px;
  font-size: 14px;
  color: var(--text-secondary);
}

.meta {
  display: flex;
  flex-wrap: wrap;
  gap: 8px;
  margin-bottom: 14px;
}

/* Version / published chips: accent-tinted for the version, neutral for the date */
.meta-chip {
  display: inline-flex;
  align-items: center;
  padding: 3px 10px;
  border-radius: 999px;
  font-size: 12px;
  font-weight: 600;
  border: 1px solid rgba(99, 102, 241, 0.35);
  background: rgba(99, 102, 241, 0.08);
  color: var(--accent-light);
}

.meta-chip.plain {
  border-color: var(--border);
  background: var(--bg-primary);
  color: var(--text-muted);
}

.release-notes {
  border: 1px solid var(--border-light);
  border-radius: 10px;
  padding: 12px 14px;
  background: var(--bg-primary);
}

.notes-title {
  margin: 0 0 8px;
  font-size: 12px;
  font-weight: 700;
  color: var(--accent-light);
  text-transform: uppercase;
  letter-spacing: 0.5px;
}

.notes-body {
  margin: 0;
  font-size: 12px;
  line-height: 1.6;
  color: var(--text-secondary);
  white-space: pre-wrap;
  word-break: break-word;
  font-family: inherit;
  max-height: 40vh;
  overflow-y: auto;
}

/* ─── Downloads area ─── */
.dl-status {
  min-height: 120px;
  display: flex;
  align-items: center;
  justify-content: center;
}

/* Downloading: centered vertical layout — gradient glyph, thick gradient bar,
   prominent percent, transferred size. Colors reuse the app's indigo-violet accents. */
.dl-downloading {
  width: 100%;
  display: flex;
  flex-direction: column;
  align-items: center;
  gap: 12px;
  padding: 8px 0;
}

.dl-glyph {
  width: 44px;
  height: 44px;
  border-radius: var(--r-sm);
  background: var(--grad);
  color: #fff;
  display: flex;
  align-items: center;
  justify-content: center;
  box-shadow: 0 6px 16px rgba(124, 92, 246, 0.42);
}

.dl-version {
  margin: 0;
  font-size: 15px;
  font-weight: 600;
  color: var(--text-primary);
}

.dl-progress-bar {
  width: 100%;
  height: 10px;
  background: var(--overlay-10);
  border-radius: 5px;
  overflow: hidden;
}

.dl-progress-fill {
  height: 100%;
  background: var(--grad);
  border-radius: 5px;
  transition: width 0.3s ease;
}

.dl-percent {
  font-size: 22px;
  font-weight: 700;
  color: var(--text-primary);
  letter-spacing: 0.5px;
  line-height: 1;
}

.dl-size {
  margin: 0;
  font-size: 12px;
  color: var(--text-muted);
}

.done-msg {
  display: flex;
  gap: 12px;
  align-items: flex-start;
  width: 100%;
}

.ok-icon {
  width: 32px;
  height: 32px;
  border-radius: 50%;
  background: var(--success);
  color: #fff;
  display: flex;
  align-items: center;
  justify-content: center;
  font-size: 16px;
  flex-shrink: 0;
}

.done-title {
  margin: 0 0 6px;
  font-size: 15px;
  font-weight: 700;
  color: var(--text-primary);
}

.done-detail {
  margin: 0 0 6px;
  font-size: 13px;
  color: var(--text-muted);
}

.done-path {
  display: block;
  font-size: 12px;
  color: var(--text-secondary);
  background: var(--bg-primary);
  border: 1px solid var(--border-light);
  border-radius: 6px;
  padding: 6px 8px;
  word-break: break-all;
  margin-bottom: 8px;
}

.done-tip {
  margin: 0;
  font-size: 12px;
  color: var(--accent-light);
}

.err-msg {
  margin: 0;
  font-size: 13px;
  color: #ef4444;
}

.modal-footer {
  display: flex;
  justify-content: flex-end;
  gap: 10px;
  padding: 16px 24px;
  border-top: 1px solid var(--border);
}

.btn-cancel {
  padding: 8px 20px;
  background: transparent;
  border: 1px solid var(--border);
  border-radius: 8px;
  color: var(--text-muted);
  font-size: 13px;
  font-weight: 600;
  cursor: pointer;
  transition: all 0.15s;
}

.btn-cancel:hover:not(:disabled) {
  background: var(--hover-bg);
}

.btn-save {
  padding: 8px 24px;
  background: var(--accent);
  border: none;
  border-radius: 8px;
  color: #fff;
  font-size: 13px;
  font-weight: 600;
  cursor: pointer;
  transition: opacity 0.15s;
}

.btn-save:hover:not(:disabled) {
  opacity: 0.85;
}

.btn-save:disabled,
.btn-cancel:disabled {
  opacity: 0.5;
  cursor: not-allowed;
}

.footer-msg {
  font-size: 13px;
  font-weight: 600;
  margin-right: auto;
}

.footer-err {
  color: #ef4444;
}

/* ─── Phone (<=767px, frame ⑳ .modal): card anchored below the status area,
       rounded floating island with the deep phone shadow, bare-value chips,
       bullet release notes and a flexed two-button footer ─── */
@media (max-width: 767px) {
  .modal-overlay {
    align-items: flex-start;
    padding: 0 16px;
  }

  .modal {
    /* ~200px from the top on the 390x844 reference, clamped on short
       viewports and lifted by the safe-area inset on edge-to-edge devices */
    margin-top: min(calc(200px + var(--safe-area-top, 0px)), 28vh);
    width: 100%;
    border: none;
    border-radius: 24px;
    box-shadow: none;
  }

  .modal-header {
    padding: 20px 20px 10px;
  }

  .modal-header h2 {
    font-size: 17px;
    font-weight: 800;
    /* The version text (the desktop flex spacer) is hidden on this tier:
       pin the title left and the close button right instead */
    flex: 1;
  }

  /* The bare-value version chip replaces the header version text */
  .modal-version {
    display: none;
  }

  .modal-close {
    width: 44px;
    height: 44px;
    margin-right: -8px;
    font-size: 17px;
  }

  .modal-body {
    padding: 8px 20px 16px;
  }

  .new-version-tip {
    margin: 0 0 12px;
    font-size: 13px;
  }

  .meta {
    margin-bottom: 12px;
  }

  /* Version chip: soft gradient fill + deep purple text (frame ⑳ .mchips);
     date chip: neutral surface. Values are bare on this tier. */
  .meta-chip {
    padding: 6px 12px;
    border: none;
    background: var(--grad-soft);
    color: var(--accent-light);
    font-size: 11.5px;
    font-weight: 700;
  }

  html[data-theme='dark'] .meta-chip {
    color: var(--accent-light);
  }

  .meta-chip.plain {
    background: var(--bg-card);
    color: var(--text-secondary);
  }

  html[data-theme='dark'] .meta-chip.plain {
    background: var(--overlay-8);
  }

  .release-notes {
    border: none;
    border-radius: var(--r-md);
    padding: 12px 14px;
    background: var(--bg-card);
  }

  html[data-theme='dark'] .release-notes {
    background: #1e2233;
  }

  .notes-title {
    font-size: 11px;
  }

  /* Bullet digest list (frame ⑳ .relnotes): 12.5/1.7, capped scroll height */
  .notes-list {
    margin: 0;
    padding-left: 18px;
    font-size: 12.5px;
    line-height: 1.7;
    color: var(--text-secondary);
    max-height: 150px;
    overflow-y: auto;
  }

  .notes-list li {
    margin-bottom: 4px;
    word-break: break-word;
  }

  .notes-body {
    max-height: 150px;
  }

  .modal-footer {
    padding: 12px 20px 20px;
  }

  /* Flexed two-button footer (frame ⑳ .mbtns): neutral filled cancel
     (flex 1) + gradient primary (flex 1.4), 44px+ touch bands */
  .btn-cancel {
    flex: 1;
    padding: 13px 0;
    background: var(--bg-primary);
    border: none;
    border-radius: var(--r-md);
    color: var(--text-secondary);
    font-size: 13.5px;
    font-weight: 800;
  }

  .btn-save {
    flex: 1.4;
    padding: 13px 0;
    background: var(--grad);
    border: none;
    border-radius: var(--r-md);
    color: #fff;
    font-size: 13.5px;
    font-weight: 800;
    box-shadow: none;
  }

  .footer-msg {
    flex-basis: 100%;
    margin: 0 0 8px;
  }
}

/* ─── Tablet track (design draft frame ⑳: the centered 560px modal). Track A
       hooks the 768..1099 band. The overlay keeps its flex centering — the
       draft's 居中弹窗 — and the content model matches the draft: soft pill
       chips, bullet release digest and the flexed 44px touch footer. Chip copy
       stays prefixed on tablets (draft A20 chips carry labels, unlike the
       phone draft's bare values), so the markup gates don't move — only
       styling changes here. Phone (<=767) and desktop (>1099) renderings are
       untouched. ─── */

@media (min-width: 768px) and (max-width: 1099px) {
  .modal {
    width: min(560px, calc(100vw - 48px));
    border: none;
    border-radius: 24px;
    box-shadow: 0 24px 60px rgba(20, 22, 45, 0.35);
  }

  /* The chip row carries the version; the header text node hides like the
     phone tier, pinning the title left and the close button right */
  .modal-header {
    padding: 20px 22px 10px;
  }

  .modal-header h2 {
    font-size: 17px;
    font-weight: 800;
    flex: 1;
  }

  .modal-version {
    display: none;
  }

  /* Touch-first close target (draft ㉒: 44px minimum touch points) */
  .modal-close {
    width: 44px;
    height: 44px;
    margin-right: -8px;
    font-size: 17px;
  }

  .modal-body {
    padding: 8px 22px 16px;
  }

  /* Soft pill chips (frame ⑳ .mchips) with the prefixed copy */
  .meta-chip {
    padding: 6px 12px;
    border: none;
    background: var(--grad-soft);
    color: var(--accent-light);
    font-size: 11.5px;
    font-weight: 700;
  }

  html[data-theme='dark'] .meta-chip {
    color: var(--accent-light);
  }

  .meta-chip.plain {
    background: var(--bg-card);
    color: var(--text-secondary);
  }

  html[data-theme='dark'] .meta-chip.plain {
    background: var(--overlay-8);
  }

  .release-notes {
    border: none;
    border-radius: var(--r-md);
    padding: 12px 14px;
    background: var(--bg-card);
  }

  html[data-theme='dark'] .release-notes {
    background: #1e2233;
  }

  .notes-title {
    font-size: 11px;
  }

  /* Bullet digest list (frame ⑳ .relnotes), capped scroll height */
  .notes-list {
    margin: 0;
    padding-left: 18px;
    font-size: 12.5px;
    line-height: 1.7;
    color: var(--text-secondary);
    max-height: 150px;
    overflow-y: auto;
  }

  .notes-list li {
    margin-bottom: 4px;
    word-break: break-word;
  }

  .notes-body {
    max-height: 150px;
  }

  .modal-footer {
    padding: 12px 22px 20px;
  }

  /* Flexed two-button footer (frame ⑳ .mbtns): neutral filled cancel
     (flex 1) + gradient primary (flex 1.4), 44px touch bands */
  .btn-cancel {
    flex: 1;
    padding: 13px 0;
    background: var(--bg-primary);
    border: none;
    border-radius: var(--r-md);
    color: var(--text-secondary);
    font-size: 13.5px;
    font-weight: 800;
  }

  .btn-save {
    flex: 1.4;
    padding: 13px 0;
    background: var(--grad);
    border: none;
    border-radius: var(--r-md);
    color: #fff;
    font-size: 13.5px;
    font-weight: 800;
    box-shadow: none;
  }

  .footer-msg {
    flex-basis: 100%;
    margin: 0 0 8px;
  }
}

</style>
