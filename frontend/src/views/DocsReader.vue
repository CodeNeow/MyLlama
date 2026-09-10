<template>
  <div ref="rootEl" class="page reader-page">
    <!-- Top bar (frame ⑱ .mstop): circular back button + section title +
         content-source badge. Phone tier only — desktop never mounts this
         view (it redirects back to /docs below). -->
    <div class="mstop">
      <button class="bk" type="button" :aria-label="t('docs.back')" @click="goBack">
        <svg width="17" height="17" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2.4" stroke-linecap="round" stroke-linejoin="round"><path d="M15 5l-7 7 7 7"/></svg>
      </button>
      <h1 class="mstop-t">{{ sectionTitle }}</h1>
      <span class="sb" :class="'sb-' + docSource">{{ sourceLabel }}</span>
    </div>

    <!-- Bundled-load failure: shared error card + retry (frame ㉑). Remote
         fetch trouble never lands here — it degrades to the bundled copy. -->
    <div v-if="loadError" class="errcard">
      <div class="et">
        <svg width="15" height="15" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2.4" stroke-linecap="round"><circle cx="12" cy="12" r="9"/><path d="M12 8v5"/><path d="M12 16.5v.5"/></svg>
        {{ t('docs.loadFailed') }}
      </div>
      <div class="em">{{ loadError }}</div>
      <button class="retry" type="button" @click="reload">↻ {{ t('docs.retry') }}</button>
    </div>

    <template v-else>
      <!-- First-load skeleton (shared .skel lines); later section switches dim
           the previous content instead of flashing empty -->
      <div v-if="loading && !content" class="reader-skel" aria-hidden="true">
        <div class="skel skel-line skel-w62"></div>
        <div class="skel skel-line skel-w88"></div>
        <div class="skel skel-line skel-w74"></div>
        <div class="skel skel-block"></div>
        <div class="skel skel-line skel-w46"></div>
      </div>

      <!-- Rendered markdown. v-html is safe here: the shared markdown-it
           instance (lib/markdown.ts) runs with html:false, so raw HTML in the
           docs source is escaped, never rendered — the same injection defense
           as Docs.vue and chat messages. -->
      <!-- Delegated link handler: external links open in the system browser,
           the WebView never navigates — left click, middle click and drag
           included (see lib/linkHandler.ts) -->
      <article
        class="docpost"
        :class="{ 'is-loading': loading }"
        v-html="rendered"
        @click="handleLinkClick"
        @auxclick="handleLinkAuxClick"
        @dragstart="handleLinkDragStart"
      ></article>

      <!-- Prev / next pager (frame ⑱ .pager): pushes /docs/{id}; each end
           hides its own side -->
      <nav v-if="prevSection || nextSection" class="pager" :aria-label="t('docs.toc')">
        <button v-if="prevSection" class="pg" type="button" @click="openSection(prevSection.id)">
          <small>{{ t('docs.pagerPrev') }}</small>{{ t(prevSection.titleKey) }}
        </button>
        <button v-if="nextSection" class="pg nx" type="button" @click="openSection(nextSection.id)">
          <small>{{ t('docs.pagerNext') }}</small>{{ t(nextSection.titleKey) }}
        </button>
      </nav>
    </template>
  </div>
</template>

<script setup lang="ts">
import { computed, nextTick, onMounted, ref, watch } from 'vue'
import { useRoute, useRouter } from 'vue-router'
import { docSections, type DocSectionId } from '../docs/manifest'
import { renderMarkdown } from '../lib/markdown'
import { handleLinkClick, handleLinkAuxClick, handleLinkDragStart } from '../lib/linkHandler'
import { t } from '../lib/i18n'
import { usePlatform } from '../lib/platform'
// Shared remote-first content pipeline (bundled → online → disk cache, with
// the LatestOnly race guard) — exported by Docs.vue's plain <script> block so
// both surfaces load sections identically.
import { useDocContent } from './Docs.vue'

const route = useRoute()
const router = useRouter()
const platformState = usePlatform()

const RAW_IDS: readonly string[] = docSections.map((s) => s.id)

// Valid section from the route param (decoded automatically by vue-router).
// An unknown id keeps rendering the first section for the instant before the
// mount redirect back to /docs fires.
const sectionId = computed<DocSectionId>(() => {
  const raw = route.params.id
  const id = Array.isArray(raw) ? raw[0] : raw
  return RAW_IDS.includes(id ?? '') ? (id as DocSectionId) : docSections[0].id
})

const sectionTitle = computed(() => {
  const section = docSections.find((s) => s.id === sectionId.value)
  return section ? t(section.titleKey) : ''
})

const { content, loading, loadError, docSource, sourceLabel, loadSection } = useDocContent(sectionId)

// Desktop never sees the reader: redirect to the single-page /docs. Also
// fires when the tier flips (e.g. a desktop window resized across 768px).
// On a phone the platform state is wired by App.vue long before this route
// can be reached from the section list, so the unwired-desktop default never
// misfires in practice.
function leaveWhenDesktop(): void {
  if (!platformState.value.isMobile) void router.replace('/docs')
}

// Rendered HTML via the shared markdown-it instance (html disabled — see
// template comment)
const rendered = computed(() => renderMarkdown(content.value))

// Pager neighbors in manifest order; each end hides its own side
const sectionIndex = computed(() => docSections.findIndex((s) => s.id === sectionId.value))
const prevSection = computed(() => (sectionIndex.value > 0 ? docSections[sectionIndex.value - 1] : null))
const nextSection = computed(() =>
  sectionIndex.value >= 0 && sectionIndex.value < docSections.length - 1
    ? docSections[sectionIndex.value + 1]
    : null,
)

function openSection(id: DocSectionId): void {
  router.push(`/docs/${id}`)
}

// Back: history-back when an in-app previous entry exists (vue-router writes
// `back` into the history state), otherwise straight to the section list.
function goBack(): void {
  const state = window.history.state as { back?: string | null } | null
  if (state && state.back != null) router.back()
  else router.push('/docs')
}

const rootEl = ref<HTMLElement | null>(null)

// The scroll container is the shared .content-area ancestor (App.vue), not
// the window: walk up to the nearest scrollable element and reset it.
function scrollTop(): void {
  let node = rootEl.value?.parentElement
  while (node) {
    if (node.scrollHeight > node.clientHeight + 1) {
      node.scrollTop = 0
      return
    }
    node = node.parentElement
  }
}

onMounted(() => {
  // Unknown section ids degrade to the list (never a broken reader state)
  const raw = route.params.id
  const id = Array.isArray(raw) ? raw[0] : raw
  if (!RAW_IDS.includes(id ?? '')) {
    void router.replace('/docs')
    return
  }
  leaveWhenDesktop()
  // The reader always opens at the top
  void nextTick(scrollTop)
})

// Pager / back-forward navigation re-enters at the top
watch(sectionId, () => { void nextTick(scrollTop) })

// Retry after a bundled-load failure: re-run the same pipeline
function reload(): void {
  void loadSection()
}
</script>

<style scoped>
/* Phone-only view (desktop redirects to /docs), so the phone densities are
   unconditional here. Same 18px gutter as the other phone tiers. */
.reader-page {
  padding: 0 18px 40px;
}

/* ─── Top bar (frame ⑱ .mstop) ─── */
.mstop {
  display: flex;
  align-items: center;
  gap: 10px;
  padding: 12px 4px;
}

.bk {
  width: 44px;
  height: 44px;
  border-radius: 50%;
  background: var(--bg-secondary);
  box-shadow: var(--shadow-island);
  border: none;
  padding: 0;
  display: flex;
  align-items: center;
  justify-content: center;
  color: var(--text-secondary);
  cursor: pointer;
  flex-shrink: 0;
  font-family: inherit;
}

.mstop-t {
  flex: 1;
  min-width: 0;
  margin: 0;
  font-size: 17px;
  font-weight: 800;
  line-height: 1.3;
  color: var(--text-primary);
  overflow: hidden;
  text-overflow: ellipsis;
  white-space: nowrap;
}

/* ─── Source badge (same vocabulary as the Docs list pills) ─── */
.sb {
  display: inline-flex;
  align-items: center;
  flex-shrink: 0;
  font-size: 11px;
  font-weight: 800;
  border-radius: 999px;
  padding: 6px 12px;
  background: var(--grad-soft);
  color: var(--accent-light);
  border: none;
}

.sb-cached {
  background: var(--warning-bg);
  color: var(--warning);
}

.sb-bundled {
  background: var(--surface-2);
  color: var(--text-secondary);
}

/* ─── Skeleton (shared .skel shapes; widths per line) ─── */
.reader-skel {
  display: flex;
  flex-direction: column;
  gap: 10px;
  padding: 6px 4px 0;
}

.skel-w62 { width: 62%; }
.skel-w88 { width: 88%; }
.skel-w74 { width: 74%; }
.skel-w46 { width: 46%; }

/* ─── Article typography (frame ⑱ .docpost) ─── */
.docpost {
  font-size: 13.5px;
  line-height: 1.85;
  color: #33384c;
  word-break: break-word;
  transition: opacity 0.15s ease;
}

html[data-theme='dark'] .docpost {
  color: #c3c8da;
}

/* Dim (do not blank) the previous section while a new one loads */
.docpost.is-loading {
  opacity: 0.55;
}

.docpost :deep(h2) {
  font-size: 19px;
  font-weight: 800;
  line-height: 1.4;
  color: var(--text-primary);
  margin: 6px 0 10px;
}

.docpost :deep(h2:first-child) {
  margin-top: 0;
}

.docpost :deep(h3) {
  font-size: 15px;
  font-weight: 700;
  color: var(--text-primary);
  margin: 14px 0 6px;
}

.docpost :deep(p) {
  margin: 0 0 10px;
}

.docpost :deep(ul),
.docpost :deep(ol) {
  margin: 0 0 10px 18px;
  padding: 0;
}

.docpost :deep(li) {
  margin-bottom: 4px;
}

.docpost :deep(strong) {
  color: var(--text-primary);
  font-weight: 700;
}

/* Links inside the markdown: the delegated container handler opens absolute
   http(s) links in the system browser and blocks everything else */
.docpost :deep(a) {
  color: var(--accent-light);
  word-break: break-all;
}

/* Inline code: subtle chip */
.docpost :deep(code) {
  font-family: var(--font-mono);
  font-size: 11.5px;
  background: var(--surface-2);
  border-radius: 5px;
  padding: 1px 5px;
}

/* Code blocks: dark on both themes (mockup .code), horizontally scrollable */
.docpost :deep(pre) {
  background: #141626;
  color: #9aa3c0;
  border-radius: var(--r-sm);
  padding: 12px 14px;
  font-size: 11.5px;
  line-height: 1.8;
  margin: 0 0 10px;
  overflow-x: auto;
}

.docpost :deep(pre code) {
  background: transparent;
  border-radius: 0;
  padding: 0;
  font-size: inherit;
  color: inherit;
}

/* Blockquote: gradient callout (mockup .tip treatment) */
.docpost :deep(blockquote) {
  margin: 0 0 10px;
  padding: 10px 14px;
  background: var(--grad-soft);
  border: none;
  border-radius: var(--r-sm);
  color: #5b21b6;
  font-size: 12.5px;
}

html[data-theme='dark'] .docpost :deep(blockquote) {
  color: #d8b4fe;
}

/* Tables / images never exceed the column; wide tables scroll in place */
.docpost :deep(img),
.docpost :deep(table) {
  max-width: 100%;
  height: auto;
}

.docpost :deep(table) {
  border-collapse: collapse;
  margin: 0 0 10px;
  font-size: 12.5px;
  display: block;
  overflow-x: auto;
  height: auto;
}

.docpost :deep(th),
.docpost :deep(td) {
  border: 1px solid var(--border);
  padding: 6px 10px;
  text-align: left;
}

.docpost :deep(th) {
  background: var(--surface-2);
  color: var(--text-primary);
  font-weight: 700;
}

/* ─── Pager (frame ⑱ .pager) ─── */
.pager {
  display: flex;
  gap: 10px;
  margin-top: 16px;
}

.pg {
  flex: 1;
  min-width: 0;
  min-height: 44px;
  background: var(--bg-secondary);
  border: none;
  border-radius: var(--r-md);
  box-shadow: var(--shadow-island);
  padding: 11px 14px;
  font-size: 12px;
  font-weight: 600;
  color: var(--text-secondary);
  cursor: pointer;
  text-align: left;
  font-family: inherit;
  overflow: hidden;
  text-overflow: ellipsis;
  white-space: nowrap;
}

.pg small {
  display: block;
  color: var(--text-dim);
  font-size: 10.5px;
  font-weight: 600;
  margin-bottom: 2px;
}

.pg.nx {
  text-align: right;
}

.pg.nx small {
  margin: 2px 0 0;
}
</style>
