<template>
  <aside class="sidebar" :class="{ collapsed: appConfig.sidebarCollapsed }">
    <div class="sidebar-header">
      <div class="logo">
        <!-- Mascot brand mark: chibi llama head on the brand-gradient plate
             (same geometry as build/appicon.svg) -->
        <svg viewBox="0 0 96 96" class="logo-icon" aria-hidden="true">
          <defs>
            <linearGradient id="logoGrad" x1="0" y1="0" x2="1" y2="1">
              <stop offset="0%" stop-color="#6366F1" />
              <stop offset="55%" stop-color="#8B5CF6" />
              <stop offset="100%" stop-color="#A855F7" />
            </linearGradient>
          </defs>
          <rect width="96" height="96" rx="22" fill="url(#logoGrad)" />
          <path d="M24.75 30.38 C18.75 27.38 14.25 19.88 16.5 13.88 C17.63 10.88 21 11.25 24 15.38 C27 19.5 30.38 25.13 33 29.25 C33.94 30.56 34.5 31.31 34.88 31.69 Z" fill="#EEF2FF" stroke="#43377B" stroke-width="2.44" stroke-linejoin="round" />
          <path d="M71.25 30.38 C77.25 27.38 81.75 19.88 79.5 13.88 C78.38 10.88 75 11.25 72 15.38 C69 19.5 65.63 25.13 63 29.25 C62.06 30.56 61.5 31.31 61.13 31.69 Z" fill="#EEF2FF" stroke="#43377B" stroke-width="2.44" stroke-linejoin="round" />
          <path d="M23.25 28.13 C19.5 25.5 16.5 19.88 18 15.75 C19.13 13.13 22.13 13.88 24.75 17.63 C27 21 29.63 25.88 31.5 28.88 Z" fill="#C9CFF7" />
          <path d="M72.75 28.13 C76.5 25.5 79.5 19.88 78 15.75 C76.88 13.13 73.88 13.88 71.25 17.63 C69 21 66.38 25.88 64.5 28.88 Z" fill="#C9CFF7" />
          <path d="M10.5 60 C10.5 46.13 14.25 37.13 21.38 30.75 C24.38 28.13 27.38 25.88 30 24.38 C31.5 21.75 33 19.88 34.13 18.75 Q36 13.5 39.75 15.38 Q42 16.13 42.75 19.13 Q45 12 50.25 12 Q52.88 12.38 53.63 18 Q55.88 13.13 60.75 14.63 Q63.38 15.75 64.88 19.88 C67.88 23.63 73.13 27.38 76.13 31.88 C80.63 37.13 85.5 46.88 85.5 60 C85.5 75.38 69.75 81.38 48 81.38 C26.25 81.38 10.5 75.38 10.5 60 Z" fill="#EEF2FF" stroke="#43377B" stroke-width="2.44" stroke-linejoin="round" />
          <path d="M75 39.38 C78 45.75 79.13 53.25 78.38 60 C78 64.13 76.88 67.13 75.38 69.75 C75.94 63 75.38 51.75 72.38 42 Z" fill="#D7DEFA" />
          <path d="M25.5 56.25 C25.5 47.25 34.88 42.75 48 42.75 C61.13 42.75 70.5 47.25 70.5 56.63 C70.5 69.75 60.75 80.25 48 80.25 C35.25 80.25 25.5 69.75 25.5 56.25 Z" fill="#FFFFFF" stroke="#43377B" stroke-width="2.06" />
          <ellipse cx="31.88" cy="60" rx="4.5" ry="2.81" fill="#F2D8EC" />
          <ellipse cx="64.13" cy="60" rx="4.5" ry="2.81" fill="#F2D8EC" />
          <ellipse cx="41.25" cy="51" rx="3.19" ry="3.94" fill="#2A2547" />
          <ellipse cx="54.75" cy="51" rx="3.19" ry="3.94" fill="#2A2547" />
          <circle cx="40.13" cy="49.5" r="1.03" fill="#fff" />
          <circle cx="42.19" cy="52.5" r="0.53" fill="#fff" />
          <circle cx="53.63" cy="49.5" r="1.03" fill="#fff" />
          <circle cx="55.69" cy="52.5" r="0.53" fill="#fff" />
          <path d="M43.88 60.75 Q48 64.88 52.13 60.75" fill="none" stroke="#43377B" stroke-width="1.88" stroke-linecap="round" />
        </svg>
        <span class="logo-text">MyLlama</span>
      </div>

    </div>

    <nav class="sidebar-nav">
      <router-link
        v-for="item in navItems"
        :key="item.path"
        :to="item.path"
        class="nav-item"
        :class="{ active: isActive(item.path) }"
        :title="appConfig.sidebarCollapsed ? t(item.labelKey) : undefined"
      >
        <span class="nav-icon" v-html="item.icon"></span>
        <span class="nav-label">{{ t(item.labelKey) }}</span>
      </router-link>
    </nav>

    <div class="sidebar-footer">
      <div class="status-dot" :class="ready ? 'ok' : 'bad'"></div>
      <span class="status-text">{{ ready ? t('nav.ready') : t('nav.notReady') }}</span>
      <button
        class="collapse-toggle"
        :title="appConfig.sidebarCollapsed ? t('nav.expand') : t('nav.collapse')"
        :aria-label="appConfig.sidebarCollapsed ? t('nav.expand') : t('nav.collapse')"
        @click="setSidebarCollapsed(!appConfig.sidebarCollapsed)"
      >
        <svg v-if="appConfig.sidebarCollapsed" width="16" height="16" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round" stroke-linejoin="round"><polyline points="13 17 18 12 13 7"/><polyline points="6 17 11 12 6 7"/></svg>
        <svg v-else width="16" height="16" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round" stroke-linejoin="round"><polyline points="11 17 6 12 11 7"/><polyline points="18 17 13 12 18 7"/></svg>
      </button>
    </div>
  </aside>
</template>

<script setup lang="ts">
import { useRoute } from 'vue-router'
import { ref, onMounted, onUnmounted } from 'vue'
import { t } from '../lib/i18n'
import { isSystemReady } from '../lib/systemReady'
import { SIDEBAR_AUTO_COLLAPSE_WIDTH } from '../lib/layout'
import { NAV_ITEMS, isActiveNav } from '../lib/navigation'
import { getLlamaCpp, getModels } from '../wails'
import { appConfig, setSidebarCollapsed } from '../store'

const route = useRoute()

// System ready state: llama.cpp is installed and at least one local model exists
const ready = ref(false)

/** Refresh ready state: queries llama.cpp and model list in parallel; fails silently degrade to not-ready */
async function refreshReady(): Promise<void> {
  let installed = false
  let models: Awaited<ReturnType<typeof getModels>> = []

  // Parallel queries, non-blocking; backend unavailable under standalone vite, silently caught
  try {
    const llamaInfo = await getLlamaCpp()
    installed = !!llamaInfo?.installed
  } catch {
    // Keep the default false when the backend is unavailable
  }

  try {
    models = await getModels()
  } catch {
    // Keep the default empty array when the backend is unavailable
  }

  ready.value = isSystemReady(installed, models.length)
}

onMounted(() => {
  refreshReady()
  // Refresh every 15s: after a llama.cpp or model download finishes, auto-updates within at most 15 seconds
  const timer = setInterval(refreshReady, 15000)
  onUnmounted(() => clearInterval(timer))

  // Narrow-window watcher: collapse once when the window crosses from wide to
  // narrow. No auto-expand on the way back — a user who manually expands the
  // sidebar in a narrow window is not fought by this watcher.
  const narrowQuery = window.matchMedia(`(max-width: ${SIDEBAR_AUTO_COLLAPSE_WIDTH}px)`)
  const onNarrowChange = (e: MediaQueryListEvent): void => {
    if (e.matches) setSidebarCollapsed(true)
  }
  narrowQuery.addEventListener('change', onNarrowChange)
  onUnmounted(() => narrowQuery.removeEventListener('change', onNarrowChange))
})

// Shared navigation model (lib/navigation.ts): the same entries feed the
// mobile bottom tab bar (MobileNav.vue), so both surfaces stay in sync.
const navItems = NAV_ITEMS

function isActive(path: string): boolean {
  return isActiveNav(path, route.path)
}
</script>

<style scoped>
.sidebar {
  width: 236px;
  min-width: 236px;
  height: 100vh;
  /* Glass rail (desktop design draft v2 frame ①): translucent panel + backdrop
     blur over the app background; the right edge is a 1px glass highlight
     instead of a solid border. */
  background: var(--glass);
  backdrop-filter: blur(22px) saturate(1.6);
  -webkit-backdrop-filter: blur(22px) saturate(1.6);
  border-right: 1px solid var(--glass-line);
  display: flex;
  flex-direction: column;
  --wails-draggable: drag;
  user-select: none;
  /* Expand 236px ↔ collapse 64px; width and min-width must transition together —
     an instant min-width jump would mask the expand animation */
  transition: width 0.2s cubic-bezier(0.4, 0, 0.2, 1), min-width 0.2s cubic-bezier(0.4, 0, 0.2, 1);
}

/* Collapsed state: 64px icon-only bar; text hides via max-width: 0 + opacity: 0
   (animatable, prevents mid-transition wrap); icons center via padding, not justify-content */
.sidebar.collapsed {
  width: 64px;
  min-width: 64px;
}

.sidebar-header {
  padding: 14px 12px 18px;
  --wails-draggable: drag;
}

.logo {
  display: flex;
  align-items: center;
  gap: 12px;
  transition: gap 0.2s cubic-bezier(0.4, 0, 0.2, 1);
}

.logo-icon {
  width: 32px;
  height: 32px;
  flex-shrink: 0;
  filter: none;
}

.logo-text {
  font-size: 16px;
  font-weight: 700;
  background: linear-gradient(135deg, #6366f1 0%, #a855f7 100%);
  -webkit-background-clip: text;
  -webkit-text-fill-color: transparent;
  background-clip: text;
  letter-spacing: -0.3px;
  white-space: nowrap;
  overflow: hidden;
  max-width: 140px;
  opacity: 1;
  transition: max-width 0.2s cubic-bezier(0.4, 0, 0.2, 1), opacity 0.2s ease;
}

/* Collapsed logo: gap animates to 0; the 32px icon stays at header padding-left 16px, exactly centered in the 64px rail */
.sidebar.collapsed .logo {
  gap: 0;
}

/* Collapsed header: widen the side padding so the 32px logo icon centers in
   the 64px rail (16 + 32/2 = 32), matching the nav icons (12 + 10 + 20/2 = 32)
   and the absolutely-centered collapse toggle. Vertical padding stays the base
   value so the header height does not jump on collapse/expand. */
.sidebar.collapsed .sidebar-header {
  padding-left: 16px;
  padding-right: 16px;
}

.sidebar.collapsed .logo-text {
  max-width: 0;
  opacity: 0;
}

.sidebar-nav {
  flex: 1;
  display: flex;
  flex-direction: column;
  gap: 4px;
  padding: 8px 12px;
  --wails-draggable: no-drag;
}

.nav-item {
  display: flex;
  align-items: center;
  gap: 11px;
  padding: 0 12px;
  height: 40px;
  border-radius: var(--r-sm);
  text-decoration: none;
  color: var(--text-muted);
  font-size: 13.5px;
  font-weight: 600;
  position: relative;
  transition: all 0.2s cubic-bezier(0.4, 0, 0.2, 1);
  cursor: pointer;
}

.nav-item:hover {
  color: var(--text-primary);
  background: var(--surface-2);
}

/* Active item = gradient capsule (desktop design draft v2 principles ①③:
   the rail's only gradient chrome — brand gradient fill, white label,
   violet glow shadow; replaces the old left-edge indicator bar). */
.nav-item.active {
  background: var(--grad);
  color: #fff;
  font-weight: 700;
  box-shadow: 0 6px 16px rgba(124, 92, 246, 0.42);
}

.nav-icon {
  display: flex;
  align-items: center;
  justify-content: center;
  width: 20px;
  height: 20px;
  flex-shrink: 0;
  opacity: 0.75;
  transition: opacity 0.2s ease;
}

/* Active icon inherits the capsule's white via currentColor */
.nav-item.active .nav-icon {
  opacity: 1;
}

.nav-label {
  line-height: 1;
  white-space: nowrap;
  overflow: hidden;
  max-width: 120px;
  opacity: 1;
  transition: max-width 0.2s cubic-bezier(0.4, 0, 0.2, 1), opacity 0.2s ease;
}

/* Collapsed nav: label shrinks to zero width; icon centered via horizontal padding
   (item inner width is 40px after nav padding 12px, (40 - 20) / 2 = 10 centers the 20px icon) */
.sidebar.collapsed .nav-label {
  max-width: 0;
  opacity: 0;
}

.sidebar.collapsed .nav-item {
  gap: 0;
  padding: 11px 10px;
}

.sidebar-footer {
  padding: 12px 8px 2px;
  display: flex;
  align-items: center;
  gap: 10px;
  border-top: 1px dashed var(--border);
  --wails-draggable: no-drag;
  position: relative;
  transition: gap 0.2s cubic-bezier(0.4, 0, 0.2, 1), padding 0.2s cubic-bezier(0.4, 0, 0.2, 1);
}

.status-dot {
  width: 8px;
  height: 8px;
  border-radius: 50%;
  /* Ready state (class .ok) is the base look: success green breathing with a
     same-color glow (desktop design draft v2 frame ① footer dot). */
  background: var(--success);
  box-shadow: 0 0 6px var(--success);
  animation: pulse 2s infinite;
  transition: width 0.2s cubic-bezier(0.4, 0, 0.2, 1);
}

/* Not ready: gray static, no glow */
.status-dot.bad {
  background: var(--text-dim);
  box-shadow: none;
  animation: none;
}

@keyframes pulse {
  0%, 100% { opacity: 1; }
  50% { opacity: 0.5; }
}

.status-text {
  font-size: 12px;
  color: var(--text-muted);
  white-space: nowrap;
  overflow: hidden;
  max-width: 100px;
  opacity: 1;
  transition: max-width 0.2s cubic-bezier(0.4, 0, 0.2, 1), opacity 0.2s ease;
}

/* Collapsed footer: status area (dot + text) shrinks to zero width, leaving the
   absolutely-positioned toggle as the only visible child, re-centered via
   left/top + translate. Padding stays the base value in both states so the
   footer height does not jump on collapse/expand. */
.sidebar.collapsed .sidebar-footer {
  justify-content: center;
  gap: 0;
}

/* Zero-width dot renders nothing (height stays 8px); pulse animation untouched */
.sidebar.collapsed .status-dot {
  width: 0;
}

.sidebar.collapsed .status-text {
  max-width: 0;
  opacity: 0;
}

/* Collapsed-state button stays absolutely positioned (never re-enters the flow,
   so the footer height is identical in both states) and re-centers in the rail */
.sidebar.collapsed .collapse-toggle {
  left: 50%;
  right: auto;
  top: 50%;
  transform: translate(-50%, -50%);
}

.collapse-toggle {
  position: absolute;
  right: 12px;
  top: 50%;
  transform: translateY(-50%);
  display: flex;
  align-items: center;
  justify-content: center;
  width: 28px;
  height: 28px;
  border: none;
  border-radius: 8px;
  background: transparent;
  color: var(--text-muted);
  cursor: pointer;
  padding: 0;
  transition: background 0.15s ease, color 0.15s ease;
}

.collapse-toggle:hover {
  background: var(--hover-bg);
  color: var(--text-primary);
}

/* mobile (<=767px): the bottom tab bar (MobileNav in App.vue's mobile shell)
   replaces the sidebar as navigation, freeing the full width for content */
@media (max-width: 767px) {
  .sidebar {
    display: none;
  }
}

/* tablet (768..1099px): force the COLLAPSED icon rail regardless of the
   persisted preference, so the content area keeps its space on roomy-but-not
   wide screens. Pure CSS — the stored sidebarCollapsed value is untouched, so
   on desktop (>1099px, outside this range) the user's own preference still
   wins. The rules mirror .sidebar.collapsed one-for-one; when the persisted
   preference happens to be collapsed both sets agree. */
@media (min-width: 768px) and (max-width: 1099px) {
  .sidebar {
    width: 64px;
    min-width: 64px;
  }

  .sidebar .logo {
    gap: 0;
  }

  /* Same 16px side padding as .sidebar.collapsed: centers the 32px logo icon
     in the forced 64px rail */
  .sidebar .sidebar-header {
    padding-left: 16px;
    padding-right: 16px;
  }

  .sidebar .logo-text {
    max-width: 0;
    opacity: 0;
  }

  .sidebar .nav-item {
    gap: 0;
    padding: 11px 10px;
  }

  .sidebar .nav-label {
    max-width: 0;
    opacity: 0;
  }

  .sidebar .sidebar-footer {
    justify-content: center;
    gap: 0;
  }

  .sidebar .status-dot {
    width: 0;
  }

  .sidebar .status-text {
    max-width: 0;
    opacity: 0;
  }

  /* Same absolute-centering fix as .sidebar.collapsed: the toggle never
     re-enters the flow, so the footer height matches the expanded state */
  .sidebar .collapse-toggle {
    left: 50%;
    right: auto;
    top: 50%;
    transform: translate(-50%, -50%);
  }
}
</style>
