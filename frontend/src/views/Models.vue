<template>
  <div class="page">
    <div class="sticky-top">
      <div class="page-header">
        <h1 class="page-title">{{ t('models.title') }}</h1>
        <p class="page-subtitle">{{ t('models.subtitle') }}</p>
      </div>

      <!-- Segmented control (v1 mobile design draft frame ③ .seg): a pill
           track with the active segment lifted on a surface chip. Same tab
           semantics as before — the active tab is derived from the route and
           clicks push the child route, so deep links and back/forward
           navigation stay correct. -->
      <div class="models-seg" role="tablist" :aria-label="t('models.title')">
        <button
          v-for="tab in tabs"
          :key="tab.id"
          :id="`${tab.id}-tab`"
          class="seg-btn"
          :class="{ active: activeTabId === tab.id }"
          role="tab"
          :aria-selected="activeTabId === tab.id"
          :aria-controls="panelId"
          @click="router.push(tab.route)"
        >
          {{ tab.label() }}
        </button>
      </div>
    </div>

    <!-- Tab panel region: nested route children rendered through a keep-alive,
         so switching tabs preserves the search input/results and the local
         model list instead of remounting them. Each tab owns its own actions:
         the local tab's rescan button lives in its directory bar. -->
    <div :id="panelId" role="tabpanel" :aria-labelledby="`${activeTabId}-tab`">
      <router-view v-slot="{ Component }">
        <keep-alive>
          <component :is="Component" />
        </keep-alive>
      </router-view>
    </div>
  </div>
</template>

<script setup lang="ts">
import { computed } from 'vue'
import { useRoute, useRouter } from 'vue-router'
import { t } from '../lib/i18n'

const route = useRoute()
const router = useRouter()

// Both tabs control the single panel region rendered by the nested router-view
const panelId = 'models-panel'

// Active tab follows the route: /models/local selects the local tab, anything
// else under /models (including /models/download) shows the download tab
const activeTabId = computed(() => (route.path.startsWith('/models/local') ? 'tab-local' : 'tab-download'))

// Tab definitions (download first: it is the default landing tab; the
// segmented control renders text-only labels per design frame ③)
const tabs = [
  {
    id: 'tab-download',
    route: '/models/download',
    label: () => t('models.tabDownload'),
  },
  {
    id: 'tab-local',
    route: '/models/local',
    label: () => t('models.tabLocal'),
  },
]
</script>

<style scoped>
.page {
  /* No top padding: header flush with content top, title aligns with sidebar logo (see global.css .page-header) */
  padding: 0 48px 60px;
}

.page-header {
  /* Use padding instead of margin: header background covers this gap so content scrolls without leaving a seam.
     Tighter than a standalone page: the tab row below completes the sticky block (same as Settings) */
  padding-bottom: 20px;
}

.page-title {
  font-size: 28px;
  font-weight: 700;
  color: var(--text-primary);
  margin: 0 0 4px;
  letter-spacing: -0.5px;
  line-height: 1.2;
}

.page-subtitle {
  font-size: 14px;
  color: var(--text-dim);
  margin: 0;
}

/* ─── Segmented control (design frame ③ .seg): pill track, active segment on a
       lifted surface chip ─── */
.models-seg {
  display: flex;
  gap: 0;
  padding: 4px;
  background: rgba(120, 124, 160, 0.12);
  border-radius: 999px;
  /* Capped so the control reads as a compact switch on wide desktop windows
     instead of a stretched full-width bar (frame ③ proportions) */
  width: min(100%, 340px);
  flex-shrink: 0;
}

.seg-btn {
  flex: 1;
  padding: 9px 0;
  background: none;
  border: none;
  border-radius: 999px;
  color: var(--text-muted);
  font-size: 13px;
  font-weight: 700;
  font-family: inherit;
  cursor: pointer;
  transition: color 0.15s, background 0.2s, box-shadow 0.2s;
  white-space: nowrap;
}

.seg-btn:hover {
  color: var(--text-secondary);
}

/* Active segment = gradient capsule + glow (desktop design draft v2 frames
   ⑩-⑬ .segb.on; replaces the lifted surface chip) */
.seg-btn.active {
  background: var(--grad);
  color: #fff;
  box-shadow: 0 4px 12px rgba(124, 92, 246, 0.35);
}

/* The 20px gap under the control lives here as sticky-top padding instead of a
   margin on .models-seg: the sticky background never paints a child's margin,
   so the old margin left a transparent 20px band where scrolled content showed
   through above the sticky header. */
.sticky-top {
  padding-bottom: 20px;
}

/* ─── Phone (<=767px): the control spans the full content width so both
       segments get tap-equal halves (frame ③). ─── */
@media (max-width: 767px) {
  .models-seg {
    width: 100%;
  }

  /* Phone heading = the design's 24px phone tier (same as Home's .greet-title
     phone rule, same 1.2 line-height), so every page header block reads the
     same height as the greeting. Weight 800 matches the design draft h1 (frame ⑧). */
  .page-title {
    font-size: 24px;
    font-weight: 800;
  }
}
</style>
