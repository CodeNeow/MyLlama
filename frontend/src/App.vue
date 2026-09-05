<template>
  <div class="app-layout" :style="{ '--dock-reserve': dockReserve + 'px', '--dock-width': dockWidth + 'px', '--dock-side': dockSide, '--titlebar-h': titlebarH }">
    <Sidebar />
    <main class="main-content">
      <!-- Custom title bar: desktop shells only. It exists to drag / close /
           maximize a FRAMELESS desktop window, so it follows the OS-scoped
           supportsFramelessTitlebar capability (false on android/ios — no
           window chrome there; true on windows/linux/darwin at every viewport
           width, because a narrow frameless window is still a window that
           needs its controls). The viewport tier (platformState.isMobile)
           shapes layout only — the mobile bottom nav bar takes over at
           <=767px while the title bar band stays via --titlebar-h. -->
      <div class="title-bar" v-if="platformState.supportsFramelessTitlebar && isDesktop">
        <!-- macOS keeps the colorful dots (matches existing style); Windows / Linux / unknown use rounded 34x26 chip buttons -->
        <template v-if="platform === 'darwin'">
          <div class="window-controls">
            <button class="win-btn win-min" @click="minimize" :title="t('title.minimize')">
              <svg width="12" height="12" viewBox="0 0 12 12"><rect x="1" y="5.5" width="10" height="1" fill="currentColor"/></svg>
            </button>
            <button class="win-btn win-max" @click="maximize" :title="t('title.maximize')">
              <svg width="12" height="12" viewBox="0 0 12 12"><rect x="1.5" y="1.5" width="9" height="9" stroke="currentColor" stroke-width="1" fill="none"/></svg>
            </button>
            <button class="win-btn win-close" @click="closeWindow" :title="t('title.close')">
              <svg width="12" height="12" viewBox="0 0 12 12"><line x1="1" y1="1" x2="11" y2="11" stroke="currentColor" stroke-width="1.5"/><line x1="11" y1="1" x2="1" y2="11" stroke="currentColor" stroke-width="1.5"/></svg>
            </button>
          </div>
        </template>
        <!-- Windows / Linux / unknown: rounded 34x26 chip buttons (design frame ㉒ .titlebar .winctl i) -->
        <div v-else class="window-controls native">
          <button class="native-btn" @click="minimize" :title="t('title.minimize')">
            <svg width="12" height="12" viewBox="0 0 12 12"><line x1="2" y1="6" x2="10" y2="6" stroke="currentColor" stroke-width="1.2"/></svg>
          </button>
          <button class="native-btn" @click="maximize" :title="isMax ? t('title.restore') : t('title.maximize')">
            <!-- Maximize: hollow square; restore: two overlapping squares (rear offset 3,3 semi-transparent + front 0.5,0.5) -->
            <svg v-if="!isMax" width="12" height="12" viewBox="0 0 12 12"><rect x="2" y="2" width="8" height="8" stroke="currentColor" stroke-width="1.1" fill="none"/></svg>
            <svg v-else width="12" height="12" viewBox="0 0 12 12">
              <rect x="4" y="4" width="7" height="7" stroke="currentColor" stroke-width="1" fill="none" opacity="0.35"/>
              <rect x="1" y="1" width="7" height="7" stroke="currentColor" stroke-width="1" fill="none"/>
            </svg>
          </button>
          <button class="native-btn close" @click="closeWindow" :title="t('title.close')">
            <svg width="12" height="12" viewBox="0 0 12 12"><line x1="2" y1="2" x2="10" y2="10" stroke="currentColor" stroke-width="1.3"/><line x1="10" y1="2" x2="2" y2="10" stroke="currentColor" stroke-width="1.3"/></svg>
          </button>
        </div>
      </div>
      <div class="content-area" :class="{ 'content-fixed': isFixedPage }">
        <router-view v-slot="{ Component, route }">
          <transition :name="(route.meta.transition as string) || 'fade'" mode="out-in">
            <!-- Keyed by the top-level matched record (not the full path):
                 child-route navigation inside a shell (e.g. the /models tab
                 bar) must keep the shell alive so its <keep-alive> cache
                 survives, while distinct top-level pages still remount. -->
            <component :is="Component" :key="(route.matched[0]?.path as string) || route.path" />
          </transition>
        </router-view>
      </div>
    </main>
    <UpdateModal :visible="updateState.showModal" @close="closeUpdateModal" />
    <TaskDock />
    <!-- Mobile shell: bottom tab bar replacing the sidebar at <=767px
         (display:none on desktop/tablet; height published as --mobile-nav-height) -->
    <MobileNav />
  </div>
</template>

<script setup lang="ts">
import { ref, computed, watch, onMounted, onUnmounted } from 'vue'
import { useRoute } from 'vue-router'
import { Application, Window as WailsWindow } from '@wailsio/runtime'
import Sidebar from './components/Sidebar.vue'
import UpdateModal from './components/UpdateModal.vue'
import TaskDock from './components/TaskDock.vue'
import MobileNav from './components/MobileNav.vue'
import { updateState, checkForUpdate, shouldAutoCheck, closeUpdateModal } from './lib/update'
import { dockReserve, dockWidth, dockSide } from './lib/dockSpace'
import { initSafeArea } from './lib/safeArea'
import { initKeyboardTracking, stopKeyboardTracking, keyboardVisible } from './lib/keyboard'
import { t } from './lib/i18n'
import { appConfig } from './store'
import { getOS } from './wails'
import { buildPlatformState, parseOs, setPlatform, usePlatform, type OsId } from './lib/platform'
import { viewportMetaContent } from './lib/layout'

const w = window as any
// Wails v3 no longer injects window.go; the bundled @wailsio/runtime marks the
// page with window._wails (Electron keeps its own electronAPI bridge). In a
// plain browser (standalone vite) _wails is set by the bundle too, so the
// title bar shows there as well — the control clicks then fail silently
// inside the runtime, matching the old w.runtime?. optional-chaining style.
const isDesktop = !!(w._wails || w.go || w.electronAPI)  // Wails or Electron

// Shared platform state (lib/platform): the viewport tier (isMobile) gates the
// mobile shell (bottom tab bar shown by CSS at <=767px), while the OS-scoped
// supportsFramelessTitlebar gates the custom title bar. The reactive singleton
// starts at the module's desktop-windows default and is republished by
// syncPlatformState below on OS detection and every window resize.
const platformState = usePlatform()

// Current route: fixed-viewport pages (route meta fixed: true) fill the window
// below the titlebar and manage their own internal scroll bands, so the shared
// content area must neither scroll nor reserve TaskDock space for them (see
// the .content-fixed rule in the style block).
const route = useRoute()
const isFixedPage = computed(() => route.meta.fixed === true)

// Custom-property height of the title bar band, consumed by the fixed-viewport
// page shells (global.css .page-fixed, Chat.vue .chat-page): 40px while the
// bar renders, 0px otherwise. Derived from the OS-scoped
// supportsFramelessTitlebar capability (correct at any viewport width — media
// queries cannot see the OS); fixed pages then always fill the visible
// viewport exactly.
const titlebarH = computed(() => (isDesktop && platformState.value.supportsFramelessTitlebar ? '40px' : '0px'))

// Current OS: 'darwin' (macOS) / 'windows' / 'linux' / empty string (unknown or backend unavailable)
// Drives window-control button platform adaptation: macOS keeps the colorful dots, other platforms use native flat buttons
const platform = ref('')
const isMax = ref(false)  // Maximized state: query the Wails API when possible, fall back to a local toggle

// Shared platform state (lib/platform): the parsed OS id starts at the module's
// desktop-windows default and is republished with the current viewport tier on
// OS detection and every window resize (passive listener; no debounce needed —
// buildPlatformState is a cheap pure classifier).
const osId = ref<OsId>('windows')
// Backend architecture (Go runtime.GOARCH, e.g. 'amd64' / 'arm64'); '' keeps
// buildPlatformState's unknown-arch fallback (darwin Metal-gated UI hidden).
// Feeds the third buildPlatformState argument so arch-scoped gates (macOS
// arm64 GPU card / Metal offload selector) react to the real backend arch.
const arch = ref('')

// Default viewport meta content — must stay in sync with index.html's meta
// tag (index.html owns it; this copy only exists to RESTORE the default after
// an Android-tablet portrait viewport switch, e.g. on rotation to landscape).
const DEFAULT_VIEWPORT_META =
  'width=device-width, initial-scale=1.0, viewport-fit=cover, interactive-widget=resizes-content'

// Android-tablet viewport switching (lib/layout.viewportMetaContent): Android
// tablet PORTRAIT forces a fixed 430px layout viewport (phone layout,
// upscaled by the WebView); every other platform/rotation keeps the default
// meta. Two device inputs feed the decision:
//   - min(window.screen.width/height): stable across meta switches (screen is
//     meta-independent) and rotation-invariant — the phone/tablet split;
//   - the orientation media query below: matchMedia orientation reads the
//     LIVE viewport aspect, which flips on rotation even under the fixed
//     430px meta — window.screen cannot provide this because the Android
//     WebView does NOT rotate its screen dimensions (a natural-landscape
//     tablet keeps reporting 1280x800 in portrait).
const portraitMql = window.matchMedia('(orientation: portrait)')
let metaRerafPending = false
let metaRerafOuter = 0
let metaRerafInner = 0
// Schedule ONE re-run of syncPlatformState after a double-rAF. Needed after a
// CHANGED meta: the WebView relayouts asynchronously (the resize event may
// fire late or not at all for a pure meta switch), and classification reads
// window.innerWidth — the explicit re-run converges the tier without leaving
// a one-frame stale classification. Guarded so stacked calls collapse.
function scheduleMetaReclassify() {
  if (metaRerafPending) return
  metaRerafPending = true
  metaRerafOuter = requestAnimationFrame(() => {
    metaRerafInner = requestAnimationFrame(() => {
      metaRerafPending = false
      syncPlatformState()
    })
  })
}

// Deterministic reload fallback for a meta TRANSITION: some WebView versions
// ignore DYNAMIC viewport meta changes even with wide viewport enabled (the
// meta width is only honored at load time there). A real transition — the
// 430px override applied, or the default restored from a previous override —
// therefore persists the desired content and reloads ONCE, so the new meta is
// in the DOM at load time, where every wide-viewport WebView honors it. The
// pending flag collapses stacked triggers (resize + mql change fire
// together); the persisted value makes the post-reload pass converge without
// another reload (the early restore below applies it at load time, so
// desired === applied there). Desktop OSes and Android phones never
// transition (the meta is constant for them) so they never reload. Without
// sessionStorage the fallback degrades to the dynamic set + double-rAF
// converge only — a reload loop would be worse than a stale meta.
let viewportReloadPending = false
const VIEWPORT_OVERRIDE_KEY = 'llama-viewport-meta-override'

function requestViewportReload(wanted: string): void {
  if (viewportReloadPending) return
  try {
    sessionStorage.setItem(VIEWPORT_OVERRIDE_KEY, wanted)
  } catch {
    return // no storage: deterministic reload impossible, dynamic set stands
  }
  viewportReloadPending = true
  // Reload after the current task so the new attribute is committed first.
  setTimeout(() => location.reload(), 0)
}

// Early restore: a persisted override is applied synchronously at load time
// (before first layout), so after a fallback reload the page already starts
// with the correct meta and the first classified pass sees
// desired === applied — the reload never re-triggers.
try {
  const persisted = sessionStorage.getItem(VIEWPORT_OVERRIDE_KEY)
  if (persisted) {
    document.querySelector('meta[name="viewport"]')?.setAttribute('content', persisted)
  }
} catch {
  // No storage available: nothing was persisted, the dynamic path handles it.
}

function syncPlatformState() {
  // 1. Apply the Android-tablet viewport meta BEFORE classifying: the tier
  //    read below uses window.innerWidth, which only reflects the new meta
  //    after the WebView relayouts. The helper returns null where the default
  //    meta already produces the right tier (non-Android, Android phones,
  //    Android tablet landscape).
  const metaEl = document.querySelector('meta[name="viewport"]')
  if (metaEl) {
    const wanted =
      viewportMetaContent(
        osId.value === 'android',
        Math.min(window.screen.width, window.screen.height),
        portraitMql.matches,
      ) ?? DEFAULT_VIEWPORT_META
    if (metaEl.getAttribute('content') !== wanted) {
      const applied = metaEl.getAttribute('content') ?? ''
      metaEl.setAttribute('content', wanted)
      // Real meta TRANSITION (override applied, or default restored from a
      // previous override): arm the deterministic reload fallback above.
      if (
        osId.value === 'android' &&
        (wanted !== DEFAULT_VIEWPORT_META || applied !== DEFAULT_VIEWPORT_META)
      ) {
        requestViewportReload(wanted)
      }
      scheduleMetaReclassify()
    }
  }
  // 2. Classify from the (possibly just-updated) viewport.
  // Build once and reuse: the same freshly classified PlatformState feeds both
  // setPlatform and the data-viewport mirror below.
  const next = buildPlatformState(osId.value, window.innerWidth, arch.value)
  setPlatform(next)
  // Mirror the classified viewport tier onto <html> as data-viewport (same
  // pattern as the data-os attribute below): global.css / components may key
  // tier styling off `[data-viewport='...']` because media queries cannot see
  // the OS.
  const viewportMode = next.isMobile ? 'mobile' : next.isTablet ? 'tablet' : 'desktop'
  document.documentElement.setAttribute('data-viewport', viewportMode)
  // Mirror the OS id onto <html> as data-os (same pattern as main.ts's
  // data-theme): global.css keys OS-scoped behavior off it — touch platforms
  // hide scrollbars there while desktop keeps them at every viewport width.
  document.documentElement.setAttribute('data-os', osId.value)
}
window.addEventListener('resize', syncPlatformState, { passive: true })
// Rotation has its own channel: the orientation media query flips on the
// LIVE viewport aspect (the resize event alone also fires on rotation, but
// the mql keeps the meta decision tied to the same signal the helper
// consumes, so meta switching and classification can never disagree).
portraitMql.addEventListener('change', syncPlatformState)
onUnmounted(() => {
  window.removeEventListener('resize', syncPlatformState)
  portraitMql.removeEventListener('change', syncPlatformState)
  // Never leave a pending meta reclassification firing after teardown.
  if (metaRerafPending) {
    cancelAnimationFrame(metaRerafOuter)
    cancelAnimationFrame(metaRerafInner)
    metaRerafPending = false
  }
})
syncPlatformState()

// Soft keyboard (lib/keyboard.ts): mirror the reactive visibility onto <html>
// as the keyboard-open class — global.css hides the mobile nav and collapses
// its height band so fixed-page layouts run down to the keyboard's top edge.
// Desktop / tablet never get the class (the detector requires a viewport
// shrink around an editable focus, which desktop resizing does not produce).
watch(keyboardVisible, (open) => {
  document.documentElement.classList.toggle('keyboard-open', open)
})

// Detect the OS on startup; on failure silently keep the empty string (same style as existing getOS optional chaining)
onMounted(async () => {
  // Android edge-to-edge bridge: pull the native system-bar insets and start
  // listening for pushes, publishing them as the --safe-area-js-* variables
  // the composed --safe-area-top/--safe-area-bottom vars read (no-op on
  // desktop, where every source is zero).
  initSafeArea()
  // Soft-keyboard visibility tracking (mobile nav hiding, see the watcher above).
  initKeyboardTracking()
  try {
    const info = await getOS()
    platform.value = (info as { os?: string }).os ?? ''
    // Publish the detected OS + arch + current viewport tier into the shared
    // platform state so platform-scoped UI gates (Settings visibility etc.)
    // react.
    osId.value = parseOs((info as { os?: string }).os)
    arch.value = (info as { arch?: string }).arch ?? ''
    syncPlatformState()
  } catch {
    // Backend unavailable (standalone vite) or parse failure: keep the default empty string
  }
  // Existing silent update check logic unchanged
  if (shouldAutoCheck()) {
    checkForUpdate()
  }
})

function minimize() {
  // .catch guards the standalone-vite case where the runtime has no backend.
  WailsWindow.Minimise().catch(() => {})
}

onUnmounted(() => {
  // Safety net: never leave window-level keyboard listeners attached.
  stopKeyboardTracking()
})

// Maximize/restore: toggle the window; flip the icon state locally first for instant feedback,
// then correct it later against the real state
// (Toggle is asynchronous, so an immediate query could read a stale value).
function maximize() {
  WailsWindow.ToggleMaximise().catch(() => {})
  isMax.value = !isMax.value
  setTimeout(async () => {
    try {
      isMax.value = await WailsWindow.IsMaximised()
    } catch {
      // On query failure keep the locally flipped value
    }
  }, 150)
}

// Close button: only minimize to tray when on Windows with the system tray enabled
// (Settings page toggle; llama-server keeps running in the background); otherwise quit.
// Tray state is read from the store cache — main.ts already ran loadConfig before
// mount, so the backend config (including persisted trayEnabled) is guaranteed to be
// loaded by the time the user clicks close; no extra getConfig call needed. If getOS
// fails (e.g. standalone vite), silently fall back to a direct quit; the runtime
// calls themselves .catch-guard the no-backend case.
async function closeWindow() {
  let onWindows = false
  try {
    const info = await getOS()
    onWindows = info.os === 'windows'
    // Keep the shared arch ref current for any later platform-state
    // republish; no immediate state change here (closing quits or hides).
    arch.value = (info as { arch?: string }).arch ?? ''
  } catch {
    // Backend unavailable (standalone vite): keep default behavior
  }
  if (onWindows && appConfig.trayEnabled) {
    WailsWindow.Hide().catch(() => {})
  } else {
    Application.Quit().catch(() => {})
  }
}
</script>

<style scoped>
.app-layout {
  display: flex;
  width: 100vw;
  height: 100vh;
  overflow: hidden;
  background: var(--bg-primary);
}

.main-content {
  flex: 1;
  display: flex;
  flex-direction: column;
  overflow: hidden;
}

/* ─── Title bar = pure window chrome: drag band + window controls only.
   Branding lives in the sidebar logo, page identity in each page's own
   header. The controls group is the sole flex child, so flex-end keeps it
   pinned to the right edge. ─── */
.title-bar {
  height: 40px;
  display: flex;
  align-items: center;
  justify-content: flex-end;
  padding: 0 12px;
  --wails-draggable: drag;
  user-select: none;
  flex-shrink: 0;
}

/* ─── Two window-control button styles ───
 * darwin (macOS): .win-btn colorful dots (yellow/green/red), matching the system look;
 * windows / linux / unknown: .window-controls.native rounded 34x26 chip buttons,
 *   hover background var(--surface-2), close button hover red with white glyph.
 * Trigger: render the dots when platform === 'darwin', otherwise the native chip group.
 * --wails-draggable: no-drag preserved: .window-controls existing setting carries over to the new button group.
 */
.window-controls {
  display: flex;
  gap: 8px;
  --wails-draggable: no-drag;
}

.window-controls.native {
  gap: 2px;  /* Compact chip spacing (design frame ㉒ .titlebar .winctl gap:2px) */
}

.native-btn {
  width: 34px;
  height: 26px;
  border: none;
  border-radius: 8px;
  background: transparent;
  color: var(--text-secondary);
  cursor: pointer;
  display: flex;
  align-items: center;
  justify-content: center;
  padding: 0;
  transition: background 0.15s ease, color 0.15s ease;
}

.native-btn:hover {
  background: var(--surface-2);
  color: var(--text-primary);
}

/* Close button hover: red background, white glyph (design frame ㉒ .titlebar .winctl i.close.hover) */
.native-btn.close:hover {
  background: #ef4444;
  color: #fff;
}

.win-btn {
  width: 18px;
  height: 18px;
  border-radius: 50%;
  border: none;
  cursor: pointer;
  display: flex;
  align-items: center;
  justify-content: center;
  padding: 0;
  transition: opacity 0.15s;
  opacity: 0.6;
}

.win-btn:hover { opacity: 1; }

.win-min {
  background: #fbbf24;
  color: #92400e;
}

.win-max {
  background: #22c55e;
  color: #14532d;
}

.win-close {
  background: #ef4444;
  color: #7f1d1d;
}

/* ─── Content area ─── */
.content-area {
  flex: 1;
  overflow-y: auto;
  overflow-x: hidden;
  /* Reserve bottom space for the floating TaskDock card (--dock-reserve, bound
     above from lib/dockSpace; 0 when hidden) plus the mobile bottom tab bar
     (--mobile-nav-height from global.css; 0px on desktop/tablet), so when
     scrolled to the end the last content stays visible above both overlays. */
  padding-bottom: calc(var(--dock-reserve, 0px) + var(--mobile-nav-height, 0px));
  /* Smooth transition when the reserve changes (dock appears/disappears,
     0 <-> pill height + offset ~56px) so content doesn't jump. */
  transition: padding-bottom 0.2s ease;
}

/* Scrolled pages only: pad the top by the safe-area inset so page headers
   clear an edge-to-edge Android status bar (the sticky headers keep the same
   offset via .sticky-top in global.css). Fixed-viewport pages are excluded —
   their shells (.page-fixed / Chat.vue) pad the inset inside their own
   height, and the shared padding would shift their exact-fit geometry. 0px
   on desktop: no visual change. */
.content-area:not(.content-fixed) {
  padding-top: var(--safe-area-top, 0px);
}

/* Fixed-viewport pages (route meta fixed: true): these pages own the full
   window height below the titlebar and manage their own internal scroll
   bands, which reserve TaskDock space themselves — scroll bands use
   padding-bottom: calc(... + var(--dock-reserve, 0px)) and the chat page
   keeps the pill inside its 72px right padding band. The shared content area
   must therefore add no bottom reserve (it would push the page past the
   viewport and create a scrollbar) and must not scroll on its own. */
.content-area.content-fixed {
  padding-bottom: 0;
  overflow: hidden;
}

/* Route transitions — opacity-only, avoiding sub-pixel horizontal jumps from composited-layer/layout switches during translate animations (fixes centered-text jitter of the chat page offline hint) */
.fade-enter-active,
.fade-leave-active {
  transition: opacity 0.2s ease;
}

.fade-enter-from {
  opacity: 0;
}

.fade-leave-to {
  opacity: 0;
}
</style>
