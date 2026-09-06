<template>
  <div class="page page-fixed">
    <div class="sticky-top">
      <div class="page-header">
        <div class="page-header-row">
          <div class="greet-block">
            <!-- Greeting header (v1 mobile design draft frame ①): a
                 time-of-day greeting replaces the static page title, and the
                 subline answers "is the AI usable right now" from the live
                 service status + resident model — never hardcoded online. -->
            <h1 class="greet-title">{{ t(greetKey) }}</h1>
            <p class="greet-sub">
              <template v-if="greetState === 'online'">
                <span class="greet-live">
                  <span class="greet-dot on"></span>
                  <span class="greet-model" :title="greetModel">{{ greetLine }}</span>
                </span>
                <span class="greet-rest">· {{ t('home.greet.readySuffix') }}</span>
              </template>
              <template v-else>
                <span class="greet-dot" :class="{ idle: greetState === 'idle', off: greetState === 'off' }"></span>
                <span class="greet-text" :class="{ 'is-off': greetState === 'off' }">{{ greetLine }}</span>
                <!-- Phone + tablet tiers (design draft frames A①/B①): while the checklist card
                     is up and the service is not ready, the subline points at it — "below" on
                     phone / tablet portrait, "on the right" on tablet landscape (lib/onboarding) -->
                <span v-if="showOnboardHint" class="greet-rest greet-hint">· {{ greetHintText }}</span>
              </template>
            </p>
          </div>
        </div>
      </div>

      <!-- Tabs row: same pattern as Models.vue (icon + label buttons with an
           underline active state). The active tab is derived from the route and
           clicks push the child route, so deep links and back/forward
           navigation stay correct. The row's right side carries the system
           tab's updated-at stamp + refresh toolbar, wired to the SystemInfoTab
           instance through its exposed API and shown only while that tab is
           active. Phone tier (design draft frame ①): while the quick-start checklist
           is up on the system tab the row is hidden entirely — the checklist
           IS the first-use screen; the runtime tab keeps its row (frames ③/④),
           so the checklist's own links remain the way in and the tab row the
           way back. -->
      <div v-if="!phoneHideTabs" class="env-tabs-row">
        <div class="env-tabs" role="tablist" :aria-label="t('home.title')">
          <button
            v-for="tab in tabs"
            :key="tab.id"
            :id="`${tab.id}-tab`"
            class="tab-btn"
            :class="{ active: activeTabId === tab.id }"
            role="tab"
            :aria-selected="activeTabId === tab.id"
            :aria-controls="panelId"
            @click="router.push(tab.route)"
          >
            <span class="tab-icon" v-html="tab.icon"></span>
            {{ tab.label() }}
          </button>
        </div>
        <div v-if="activeTabId === 'tab-system'" class="header-actions">
          <span v-if="systemLastUpdated" class="updated-at">
            {{ t(platformState.isMobile ? 'home.updatedAtAsOf' : 'home.updatedAt', { time: systemLastUpdated }) }}
          </span>
          <!-- Phone tier renders this icon-only as a round button (the text
               label is hidden via CSS); the aria-label keeps it announced -->
          <button
            class="refresh-btn"
            :disabled="systemRefreshing"
            :aria-label="systemRefreshing ? t('home.refreshing') : t('home.refresh')"
            @click="refreshSystem"
          >
            <svg :class="{ spinning: systemRefreshing }" width="14" height="14" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round" stroke-linejoin="round">
              <polyline points="23 4 23 10 17 10"/><polyline points="1 20 1 14 7 14"/>
              <path d="M3.51 9a9 9 0 0 1 14.85-3.36L23 10M1 14l4.64 4.36A9 9 0 0 0 20.49 15"/>
            </svg>
            <span class="refresh-label">{{ systemRefreshing ? t('home.refreshing') : t('home.refresh') }}</span>
          </button>
        </div>
      </div>
    </div>

    <!-- Tab panel region: doubles as the page's scroll band (fixed-viewport
         layout — only this region scrolls, never the page). Nested route
         children render through keep-alive, so switching tabs preserves the
         hardware samples and the runtime download state instead of remounting
         them. The bare / route lands on EnvironmentDefault, which resolves the
         smart default tab and replaces itself. -->
    <div :id="panelId" role="tabpanel" :aria-labelledby="`${activeTabId}-tab`" class="page-scroll">
      <router-view v-slot="{ Component }">
        <keep-alive>
          <component :is="Component" ref="systemTabRef" />
        </keep-alive>
      </router-view>
    </div>
  </div>
</template>

<script setup lang="ts">
import { computed, onMounted, onUnmounted, ref } from 'vue'
import { useRoute, useRouter } from 'vue-router'
import { t } from '../lib/i18n'
import { onboardHintKey } from '../lib/onboarding'
import { usePlatform } from '../lib/platform'
import { appConfig } from '../store'
import { getServerStatus, getLoadedModels, type LoadedModel } from '../wails'

const route = useRoute()
const router = useRouter()
const platformState = usePlatform()

// ─── Greeting header (frame ①) ──────────────────────────────────
// Time-of-day greeting + honest service status. The shell polls the light
// service status pair (getServerStatus + getLoadedModels) every 3s — the same
// facts TaskDock displays, at the SystemInfoTab live-sample cadence. No data
// is invented: before the first successful probe the subline shows a neutral
// loading state, and a stopped service says so.

const now = ref(new Date())
const serverRunning = ref(false)
const statusResolved = ref(false)
const loadedModels = ref<LoadedModel[]>([])

type GreetSlot = 'morning' | 'afternoon' | 'evening'

/** Time-of-day bucket: morning before 12:00, afternoon 12:00–17:59, evening after. */
function greetSlot(d: Date): GreetSlot {
  const h = d.getHours()
  if (h < 12) return 'morning'
  if (h < 18) return 'afternoon'
  return 'evening'
}

const greetKey = computed(() => `home.greet.${greetSlot(now.value)}`)

/** First resident model: prefer one actually loaded, else any loading/sleeping entry. */
const residentModel = computed<LoadedModel | null>(
  () => loadedModels.value.find(m => m.status === 'loaded') ?? loadedModels.value[0] ?? null
)

const greetState = computed<'loading' | 'online' | 'idle' | 'off'>(() => {
  if (!statusResolved.value) return 'loading'
  if (!serverRunning.value) return 'off'
  return residentModel.value ? 'online' : 'idle'
})

const greetModel = computed(() => residentModel.value?.id ?? '')

// Checklist guidance hint (design draft frames A①/B①): shown while the service is
// not ready and the quick-start checklist can be on screen. The tier/orientation
// gate lives in lib/onboarding.onboardHintKey: phone + tablet tiers render it
// (with the "on the right" copy on tablet landscape), desktop never does.
// SystemInfoTab owns the checklist's probe-derived visibility; from the shell,
// "not dismissed and service not ready" is an exact-enough proxy — a stopped /
// model-less service always keeps the checklist incomplete, so the card is up
// unless dismissed.
const onboardHint = computed(() => onboardHintKey(platformState.value))
const showOnboardHint = computed(
  () =>
    onboardHint.value !== null &&
    (greetState.value === 'off' || greetState.value === 'idle') &&
    !appConfig.onboardingDismissed
)
const greetHintText = computed(() => (onboardHint.value ? t(onboardHint.value) : ''))

const greetLine = computed(() => {
  switch (greetState.value) {
    case 'online':
      return t('home.greet.online', { model: residentModel.value!.id })
    case 'idle':
      return t('home.greet.idle')
    case 'off':
      return t('home.greet.offline')
    default:
      return t('home.greet.loading')
  }
})

let statusTimer: ReturnType<typeof setInterval> | null = null

async function pollServiceStatus() {
  try {
    const s = await getServerStatus()
    serverRunning.value = s.running
    if (s.running) {
      try {
        loadedModels.value = await getLoadedModels()
      } catch {
        // Transient router query failure: keep the previous list until next tick
      }
    } else {
      loadedModels.value = []
    }
    statusResolved.value = true
    now.value = new Date()
  } catch {
    // Backend unavailable (e.g. standalone vite): keep the honest loading state
  }
}

onMounted(() => {
  pollServiceStatus()
  statusTimer = setInterval(pollServiceStatus, 3000)
})

onUnmounted(() => {
  if (statusTimer) {
    clearInterval(statusTimer)
    statusTimer = null
  }
})

// Both tabs control the single panel region rendered by the nested router-view
const panelId = 'home-panel'

// Active tab follows the route: /runtime selects the runtime tab, everything
// else under the shell (/system, and the bare / while the default resolver is
// still probing) shows the system tab
const activeTabId = computed(() => (route.path.startsWith('/runtime') ? 'tab-runtime' : 'tab-system'))

// Phone tier (design draft frame ①): the tab row disappears while the quick-start
// checklist owns the system tab — the checklist state has no tab strip in the
// mockup. The runtime tab (frames ③/④) always keeps its row. Deliberately
// phone-only: the tablet draft keeps the tab strip visible (frames A③/A④ draw
// it), and the tabs stay the only way back from the runtime tab.
const phoneHideTabs = computed(
  () => platformState.value.isMobile && showOnboardHint.value && activeTabId.value === 'tab-system'
)

// Exposed API of the system tab (SystemInfoTab.vue defineExpose): the refresh
// toolbar beside the tab bar delegates the manual re-probe to the active
// system tab instance and mirrors refreshing/lastUpdated. Optional chaining
// everywhere: while the EnvironmentDefault resolver or the RuntimeSection
// child is the bound instance, the exposed API is absent.
interface SystemTabExposed {
  refresh: () => Promise<void> | void
  refreshing: boolean
  lastUpdated: string
}
const systemTabRef = ref<SystemTabExposed | null>(null)
const systemRefreshing = computed(() => systemTabRef.value?.refreshing ?? false)
const systemLastUpdated = computed(() => systemTabRef.value?.lastUpdated ?? '')

/** Header refresh: forces the system tab to re-probe the hardware snapshot. */
function refreshSystem() {
  systemTabRef.value?.refresh()
}

// Tab definitions (icons are inline stroke SVGs, mirroring Models.vue)
const tabs = [
  {
    id: 'tab-system',
    route: '/system',
    label: () => t('home.tabSystem'),
    icon: `<svg width="16" height="16" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round" stroke-linejoin="round"><rect x="4" y="4" width="16" height="16" rx="2" ry="2"/><rect x="9" y="9" width="6" height="6"/><line x1="9" y1="1" x2="9" y2="4"/><line x1="15" y1="1" x2="15" y2="4"/><line x1="9" y1="20" x2="9" y2="23"/><line x1="15" y1="20" x2="15" y2="23"/><line x1="20" y1="9" x2="23" y2="9"/><line x1="20" y1="14" x2="23" y2="14"/><line x1="1" y1="9" x2="4" y2="9"/><line x1="1" y1="14" x2="4" y2="14"/></svg>`,
  },
  {
    id: 'tab-runtime',
    route: '/runtime',
    label: () => t('home.tabRuntime'),
    icon: `<svg width="16" height="16" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round" stroke-linejoin="round"><polygon points="12 2 2 7 12 12 22 7 12 2"/><polyline points="2 17 12 22 22 17"/><polyline points="2 12 12 17 22 12"/></svg>`,
  },
]
</script>

<style scoped>
.page {
  /* No top padding: header flush with content top, title aligns with sidebar logo (see global.css .page-header).
     No bottom padding either: the fixed layout (page-fixed) hands bottom spacing
     to the .page-scroll band below */
  padding: 0 48px;
}

/* Fixed-viewport layout (see .page-fixed in global.css): the header band stays
   pinned; only the content band below scrolls */
.page-fixed .sticky-top {
  flex-shrink: 0;
}

.page-header {
  /* Use padding instead of margin: header background covers this gap so content scrolls without leaving a seam.
     Tighter than a standalone page: the tab row below completes the sticky block (same as Models.vue) */
  padding-bottom: 20px;
}

/* ─── Greeting header (frame ①): time-of-day greeting replaces the static
       page title; the status subline keeps to one line with an ellipsized
       model id so long router ids cannot push the header band wider ─── */
.greet-block {
  min-width: 0;
}

.greet-title {
  font-size: 26px;
  font-weight: 800;
  color: var(--text-primary);
  margin: 0 0 6px;
  letter-spacing: -0.4px;
  line-height: 1.2;
}

.greet-sub {
  display: flex;
  align-items: center;
  gap: 7px;
  font-size: 13px;
  color: var(--text-dim);
  margin: 0;
  min-width: 0;
  font-variant-numeric: tabular-nums;
}

.greet-live {
  display: inline-flex;
  align-items: center;
  gap: 7px;
  color: var(--success);
  font-weight: 700;
  min-width: 0;
}

.greet-model {
  overflow: hidden;
  text-overflow: ellipsis;
  white-space: nowrap;
}

.greet-rest {
  white-space: nowrap;
  overflow: hidden;
  text-overflow: ellipsis;
}

.greet-text {
  overflow: hidden;
  text-overflow: ellipsis;
  white-space: nowrap;
}

.greet-dot {
  width: 7px;
  height: 7px;
  border-radius: 50%;
  background: var(--text-dim);
  flex-shrink: 0;
}

/* Online/resident: glowing green dot (design frame ① .live) */
.greet-dot.on {
  background: var(--success);
  box-shadow: none;
}

/* Service up but no resident model yet: green but unlit */
.greet-dot.idle {
  background: var(--success);
  opacity: 0.55;
}

.page-header-row {
  display: flex;
  align-items: center;
  justify-content: space-between;
  gap: 16px;
}

/* ─── Tabs row (same pattern as Models.vue): tab bar left, actions right.
       Bottom gap lives on the row; no border — the pill tabs carry their own
       active state (design frame ① .tabs). ─── */
.env-tabs-row {
  display: flex;
  align-items: center;
  justify-content: space-between;
  gap: 16px;
  margin-bottom: 20px;
  flex-shrink: 0;
}

.env-tabs {
  display: flex;
  gap: 4px;
  padding: 0;
}

.header-actions {
  display: flex;
  align-items: center;
  gap: 12px;
  flex-shrink: 0;
}

.updated-at {
  font-size: 12px;
  color: var(--text-dim);
  white-space: nowrap;
  font-variant-numeric: tabular-nums;
}

.refresh-btn {
  display: inline-flex;
  align-items: center;
  gap: 6px;
  padding: 6px 14px;
  background: transparent;
  color: var(--text-secondary);
  border: 1px solid var(--border);
  border-radius: var(--radius-sm);
  font-size: 13px;
  font-weight: 500;
  cursor: pointer;
  transition: all 0.2s;
}

.refresh-btn:hover:not(:disabled) {
  background: var(--hover-bg);
  border-color: var(--overlay-20);
  color: var(--text-primary);
}

.refresh-btn:disabled {
  opacity: 0.6;
  cursor: default;
}

.refresh-btn svg.spinning {
  animation: spin 0.8s linear infinite;
}

@keyframes spin {
  from { transform: rotate(0deg); }
  to { transform: rotate(360deg); }
}

.tab-btn {
  display: flex;
  align-items: center;
  gap: 6px;
  padding: 8px 16px;
  background: rgba(120, 124, 160, 0.12);
  border: none;
  border-radius: 999px;
  color: var(--text-muted);
  font-size: 13px;
  font-weight: 700;
  font-family: inherit;
  cursor: pointer;
  transition: background 0.2s, color 0.2s, box-shadow 0.2s;
  white-space: nowrap;
}

.tab-btn:hover {
  background: rgba(120, 124, 160, 0.18);
  color: var(--text-secondary);
}

.tab-btn.active {
  /* Active tab = gradient capsule + glow (desktop design draft v2 frame ①
     .tabs .tab.on); replaces the old ink chip. */
  background: var(--grad);
  color: #fff;
  box-shadow: 0 6px 16px rgba(124, 92, 246, 0.42);
}

.tab-btn .tab-icon {
  display: flex;
}

/* ─── Phone (<=767px): design draft frames ①/② header. Greeting 27px, the
       ready-suffix stays visible (ellipsis on overflow), the offline status
       turns red, the tab row becomes mockup pill chips sharing one line with
       an icon-only round refresh button (44px touch targets throughout). ─── */
@media (max-width: 767px) {
  .greet-title {
    font-size: 27px;
  }

  /* Offline status (mockup frame ① .h-greet .off): red dot + red text */
  .greet-dot.off {
    background: var(--danger);
  }

  .greet-text.is-off {
    color: var(--danger);
    font-weight: 700;
  }

  /* Online glow (mockup .hero .tag .dot): mint 0 0 8px halo */
  .greet-dot.on {
    box-shadow: none;
  }

  /* Subline must never clip mid-glyph: every segment is a single-line
     ellipsizing box. The greeting block gets min-width: 0 so it can shrink
     inside the header row; the model name shrinks first (long router ids
     ellipsize) while the green dot stays non-shrinking. */
  .greet-block {
    min-width: 0;
    max-width: 100%;
  }

  .greet-sub {
    min-width: 0;
    max-width: 100%;
    flex-wrap: wrap;
    overflow: hidden;
  }

  .greet-live {
    min-width: 0;
    flex: 0 1 auto;
    overflow: hidden;
  }

  .greet-model,
  .greet-text,
  .greet-rest {
    min-width: 0;
    overflow: hidden;
    text-overflow: ellipsis;
    white-space: nowrap;
  }

  /* First-use guidance (mockup frame ① .h-greet p): the "complete the 3
     steps" note rides the SAME line as the red status — dot + red bold
     status + muted note, single-line ellipsis when space runs out (no wrap
     onto a second row) */
  .greet-hint {
    flex-basis: auto;
    margin-left: 0;
  }

  /* Tab row: pill chips (mockup .tabs), no underline, refresh stays on the
     same line — the row must never wrap below the tabs */
  .env-tabs-row {
    flex-wrap: nowrap;
    gap: 8px;
    border-bottom: none;
    margin-bottom: 14px;
  }

  .env-tabs {
    flex: 0 1 auto;
    min-width: 0;
    gap: 8px;
    overflow-x: auto;
    scrollbar-width: none;
  }

  .env-tabs::-webkit-scrollbar {
    display: none;
  }

  .tab-btn {
    flex-shrink: 0;
    min-height: 44px;
    padding: 8px 15px;
    border-bottom: none;
    margin-bottom: 0;
    border-radius: 999px;
    background: rgba(120, 124, 160, 0.12);
    color: var(--text-muted);
    font-size: 13px;
    font-weight: 700;
  }

  .tab-btn .tab-icon {
    display: none;
  }

  /* Active pill follows the ink color and flips per theme via tokens
     (mockup .tabs .tab.on + dark override .dark .tabs .tab.on) */
  .tab-btn.active {
    background: var(--text-primary);
    color: var(--bg-primary);
    box-shadow: none;
  }

  .header-actions {
    flex: 0 1 auto;
    min-width: 0;
    gap: 8px;
    padding-bottom: 0;
  }

  .updated-at {
    font-size: 11.5px;
    overflow: hidden;
    text-overflow: ellipsis;
  }

  .refresh-btn {
    flex-shrink: 0;
    width: 44px;
    height: 44px;
    padding: 0;
    justify-content: center;
    border: none;
    border-radius: 50%;
    background: transparent;
  }

  .refresh-btn:hover:not(:disabled) {
    background: var(--hover-bg);
    border-color: transparent;
  }

  .refresh-btn .refresh-label {
    display: none;
  }

  .refresh-btn svg {
    width: 16px;
    height: 16px;
  }
}

/* ─── Scrollable content band (also the tab panel region) ─── */
.page-scroll {
  flex: 1;
  min-height: 0;
  overflow-y: auto;
  /* Bottom clearance so the last row of cards clears the floating TaskDock
     pill (--dock-reserve is bound globally by App.vue; 0 while hidden) */
  padding-bottom: calc(24px + var(--dock-reserve, 0px));
}

/* Thin scrollbar matching .content-area (App.vue) so the inner band does not
   render a full-width system scrollbar */
.page-scroll::-webkit-scrollbar {
  width: 6px;
}

.page-scroll::-webkit-scrollbar-track {
  background: transparent;
}

.page-scroll::-webkit-scrollbar-thumb {
  background: var(--overlay-10);
  border-radius: 3px;
}

.page-scroll::-webkit-scrollbar-thumb:hover {
  background: var(--scrollbar-thumb-hover);
}
</style>
