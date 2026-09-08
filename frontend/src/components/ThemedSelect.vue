<template>
  <div class="themed-select" :class="`themed-select--${variant}`">
    <button
      ref="triggerRef"
      type="button"
      class="themed-select__trigger"
      :disabled="disabled"
      :aria-expanded="open"
      aria-haspopup="listbox"
      :aria-label="label"
      :aria-activedescendant="open && highlightIndex >= 0 ? `${idPrefix}-opt-${highlightIndex}` : undefined"
      @click.stop="open ? closeMenu() : openMenu()"
      @keydown="onTriggerKeydown"
    >
      <span class="themed-select__value">{{ displayValue }}</span>
      <svg class="themed-select__chevron" width="14" height="14" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round" stroke-linejoin="round">
        <polyline points="6 9 12 15 18 9"/>
      </svg>
    </button>
    <!-- The menu is teleported to <body> and position:fixed: pages like Settings
         clip absolutely-positioned menus with `overflow: hidden` on their cards.
         The inline style (computed from the trigger rect on open) carries
         left/width and top-or-bottom; scrolling or resizing the viewport closes. -->
    <Teleport to="body">
      <div
        v-if="open"
        ref="menuRef"
        class="themed-select__menu"
        :class="menuClass"
        role="listbox"
        :style="menuStyle"
      >
        <button
          v-for="(opt, i) in options"
          :key="opt.value"
          :id="`${idPrefix}-opt-${i}`"
          :ref="(el) => setOptionRef(el, i)"
          type="button"
          class="themed-select__option"
          :class="{
            'themed-select__option--selected': opt.value === modelValue,
            'themed-select__option--highlighted': open && i === highlightIndex,
          }"
          role="option"
          :aria-selected="opt.value === modelValue"
          tabindex="-1"
          @click="select(opt.value)"
          @mouseenter="highlightIndex = i"
        >
          <span class="themed-select__option-label">{{ opt.label }}</span>
          <svg v-if="opt.value === modelValue" class="themed-select__option-check" width="14" height="14" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2.5" stroke-linecap="round" stroke-linejoin="round">
            <polyline points="20 6 9 17 4 12"/>
          </svg>
        </button>
        <div v-if="options.length === 0" class="themed-select__empty">{{ emptyText }}</div>
      </div>
    </Teleport>
  </div>
</template>

<script lang="ts">
// Module-level counter: gives each ThemedSelect instance a unique DOM id
// prefix for the aria-activedescendant wiring (option ids are `<prefix>-opt-<i>`).
let themedSelectCount = 0
</script>

<script setup lang="ts">
import { ref, computed, watch, nextTick, onMounted, onUnmounted, type CSSProperties } from 'vue'
import { selectDisplayLabel } from '../lib/selectOptions'

export interface SelectOption {
  value: string
  label: string
}

const props = withDefaults(defineProps<{
  modelValue?: string
  options: SelectOption[]
  placeholder?: string
  disabled?: boolean
  label?: string
  emptyText?: string
  /** 'field' matches form inputs (ModelSettings), 'toolbar' matches the chat toolbar */
  variant?: 'field' | 'toolbar'
  /** Extra class for the teleported menu element. A menu teleported to <body>
      can no longer be reached through the consumer's descendant selectors, so
      consumers that restyle the fixed menu for their own layout (e.g. the
      Settings phone bottom-sheet) mark their instances with this class. */
  menuClass?: string
}>(), {
  modelValue: '',
  placeholder: '',
  disabled: false,
  label: '',
  emptyText: '',
  variant: 'field',
  menuClass: '',
})

const emit = defineEmits<{
  (e: 'update:modelValue', value: string): void
}>()

const open = ref(false)
/** Keyboard-highlighted option index while the menu is open; -1 = none. */
const highlightIndex = ref(-1)
/** Per-instance DOM id prefix so option ids never collide across selects. */
const idPrefix = `themed-select-${++themedSelectCount}`
/** Trigger button element: refocused after selecting so focus never falls to <body>. */
const triggerRef = ref<HTMLButtonElement | null>(null)
/** Teleported menu element: lets the scroll-close guard ignore scrolls that
 * happen inside the menu's own 300px scrollable viewport. */
const menuRef = ref<HTMLElement | null>(null)
/** Fixed-position style for the teleported menu, computed from the trigger's
 * bounding rect on open (see positionMenu). */
const menuStyle = ref<CSSProperties>({})
/** Rendered option elements by index, used to scroll the highlight into view. */
const optionEls: (HTMLElement | null)[] = []

const displayValue = computed(() => selectDisplayLabel(props.options, props.modelValue, props.placeholder))

/** Opening the menu always starts on the option matching modelValue, else the first option. */
function initialHighlightIndex(): number {
  const i = props.options.findIndex((o) => o.value === props.modelValue)
  return i >= 0 ? i : 0
}

function openMenu(): void {
  open.value = true
  highlightIndex.value = props.options.length > 0 ? initialHighlightIndex() : -1
  positionMenu()
  // While the fixed menu is open, any scroll outside it or a viewport resize
  // moves it away from its anchor — close instead of tracking (simple and
  // predictable; the capture flag also catches scrolls of inner containers).
  // Listeners are removed again in closeMenu, so they are only active while open.
  window.addEventListener('scroll', onViewportScroll, { capture: true })
  window.addEventListener('resize', onViewportResize)
}

function closeMenu(): void {
  open.value = false
  highlightIndex.value = -1
  window.removeEventListener('scroll', onViewportScroll, { capture: true })
  window.removeEventListener('resize', onViewportResize)
}

/**
 * Fixed positioning for the teleported menu: stretch to the trigger's rect
 * (left + width; the CSS min-width still wins for narrow toolbar triggers)
 * and drop 6px below it, flipping above when the open menu (max-height 300
 * + 6 gap) would overflow the viewport bottom.
 */
function positionMenu(): void {
  const el = triggerRef.value
  if (!el) return
  const rect = el.getBoundingClientRect()
  const style: CSSProperties = {
    left: rect.left + 'px',
    width: rect.width + 'px',
  }
  if (rect.bottom + 306 > window.innerHeight) {
    style.bottom = (window.innerHeight - rect.top + 6) + 'px'
  } else {
    style.top = rect.bottom + 6 + 'px'
  }
  menuStyle.value = style
}

/** Scroll capture handler: close on any scroll that moves the menu away from
 * its anchor, EXCEPT scrolls inside the menu's own scrollable viewport (the
 * event target is the menu or one of its descendants there). */
function onViewportScroll(e: Event): void {
  if (e.target instanceof Node && menuRef.value?.contains(e.target)) return
  closeMenu()
}

function onViewportResize(): void {
  closeMenu()
}

function select(value: string) {
  emit('update:modelValue', value)
  closeMenu()
  triggerRef.value?.focus()
}

/**
 * Select-only combobox keyboard behavior (WAI-ARIA): DOM focus stays on the
 * trigger the whole time. ArrowUp/ArrowDown move the highlight (wrapping),
 * Enter/Space open the menu or pick the highlighted option, Home/End jump to
 * the first/last option, Escape closes. Enter/Space MUST preventDefault or
 * the browser also fires a click on the button and re-toggles the menu. Tab
 * closes without preventDefault so the browser still moves focus away.
 */
function onTriggerKeydown(e: KeyboardEvent): void {
  if (props.disabled) return
  const len = props.options.length
  switch (e.key) {
    case 'Escape':
      if (open.value) closeMenu()
      e.preventDefault()
      break
    case 'ArrowDown':
      if (!open.value) openMenu()
      else if (len > 0) highlightIndex.value = (highlightIndex.value + 1) % len
      e.preventDefault()
      break
    case 'ArrowUp':
      if (!open.value) openMenu()
      else if (len > 0) highlightIndex.value = (highlightIndex.value - 1 + len) % len
      e.preventDefault()
      break
    case 'Enter':
    case ' ':
      if (!open.value) openMenu()
      else if (highlightIndex.value >= 0) select(props.options[highlightIndex.value].value)
      e.preventDefault()
      break
    case 'Home':
      if (!open.value) openMenu()
      if (len > 0) highlightIndex.value = 0
      e.preventDefault()
      break
    case 'End':
      if (!open.value) openMenu()
      if (len > 0) highlightIndex.value = len - 1
      e.preventDefault()
      break
    case 'Tab':
      if (open.value) closeMenu()
      break
    default:
      break
  }
}

/** Track the rendered option elements (:ref callback) for scroll-into-view. */
function setOptionRef(el: unknown, index: number): void {
  optionEls[index] = el instanceof HTMLElement ? el : null
}

// Keep the highlighted option visible inside the 300px scrollable menu.
// Guarded because some test environments do not implement scrollIntoView.
watch(highlightIndex, async (index) => {
  if (index < 0) return
  await nextTick()
  const el = optionEls[index]
  if (typeof el?.scrollIntoView === 'function') el.scrollIntoView({ block: 'nearest' })
})

/** Close the menu when clicking anywhere outside the component */
function onDocClick() {
  closeMenu()
}

onMounted(() => document.addEventListener('click', onDocClick))
onUnmounted(() => {
  document.removeEventListener('click', onDocClick)
  window.removeEventListener('scroll', onViewportScroll, { capture: true })
  window.removeEventListener('resize', onViewportResize)
})
</script>

<style scoped>
/* ─── Trigger ─── */
.themed-select__trigger {
  display: flex;
  align-items: center;
  gap: 8px;
  width: 100%;
  text-align: left;
  font-size: 13px;
  font-weight: 500;
  font-family: inherit;
  outline: none;
  cursor: pointer;
  transition: all 0.2s;
}

.themed-select__value {
  flex: 1;
  min-width: 0;
  overflow: hidden;
  text-overflow: ellipsis;
  white-space: nowrap;
}

.themed-select__chevron {
  flex-shrink: 0;
  color: var(--text-dim);
  transition: transform 0.2s;
}

.themed-select__trigger[aria-expanded='true'] .themed-select__chevron {
  transform: rotate(180deg);
}

.themed-select__trigger:disabled {
  opacity: 0.35;
  cursor: default;
}

/* field variant: blends with .param-input form fields */
.themed-select--field .themed-select__trigger {
  padding: 8px 10px;
  background: var(--bg-primary);
  border: 1px solid var(--border);
  border-radius: var(--radius-sm);
  color: var(--text-primary);
}

.themed-select--field .themed-select__trigger:hover:not(:disabled) {
  border-color: var(--overlay-20);
}

/* Touch press feedback (OS-scoped): mirrors the hover visuals on touch. */
html[data-os='android'] .themed-select--field .themed-select__trigger:active:not(:disabled),
html[data-os='ios'] .themed-select--field .themed-select__trigger:active:not(:disabled) {
  border-color: var(--overlay-20);
}

.themed-select--field .themed-select__trigger:focus-visible {
  border-color: var(--accent);
  box-shadow: none;
}

.themed-select--field .themed-select__trigger[aria-expanded='true'] {
  border-color: var(--accent);
  box-shadow: none;
}

/* toolbar variant: blends with the chat toolbar controls */
.themed-select--toolbar .themed-select__trigger {
  padding: 8px 12px;
  background: var(--surface);
  border: 1px solid var(--border);
  border-radius: 8px;
  color: var(--text-primary);
  min-width: 240px;
  max-width: 380px;
}

.themed-select--toolbar .themed-select__trigger:hover:not(:disabled) {
  background: var(--hover-bg);
}

html[data-os='android'] .themed-select--toolbar .themed-select__trigger:active:not(:disabled),
html[data-os='ios'] .themed-select--toolbar .themed-select__trigger:active:not(:disabled) {
  background: var(--hover-bg);
}

.themed-select--toolbar .themed-select__trigger:focus-visible {
  border-color: rgba(99, 102, 241, 0.4);
}

.themed-select--toolbar .themed-select__trigger[aria-expanded='true'] {
  background: var(--hover-bg);
  border-color: rgba(99, 102, 241, 0.4);
}

/* ─── Menu (in-app so it follows the theme in both light and dark).
       Teleported to <body> + position:fixed so `overflow: hidden` cards
       (Settings groups) can never clip it. The inline style computed on open
       carries left/width and top-or-bottom from the trigger's rect. z-index 60
       sits above page overlays (the Settings api-key dialog root uses 39).
       Glass floating layer (design draft v2 rule 2): translucent panel +
       backdrop blur + glass hairline + deep floating shadow. ─── */
.themed-select__menu {
  position: fixed;
  z-index: 60;
  min-width: 240px;
  max-height: 300px;
  overflow-y: auto;
  background: var(--glass);
  backdrop-filter: blur(22px) saturate(1.6);
  -webkit-backdrop-filter: blur(22px) saturate(1.6);
  border: 1px solid var(--glass-line);
  border-radius: var(--r-md);
  box-shadow: 0 16px 40px rgba(15, 17, 28, 0.25);
  padding: 4px;
}

.themed-select__option {
  display: flex;
  align-items: center;
  gap: 8px;
  width: 100%;
  padding: 8px 10px;
  background: transparent;
  border: none;
  border-radius: 6px;
  color: var(--text-secondary);
  font-size: 13px;
  font-weight: 500;
  font-family: inherit;
  text-align: left;
  cursor: pointer;
  transition: all 0.15s;
}

.themed-select__option:hover {
  background: var(--hover-bg);
  color: var(--text-primary);
}

/* Keyboard highlight mirrors the mouse hover look */
.themed-select__option--highlighted {
  background: var(--hover-bg);
  color: var(--text-primary);
}

/* Selected option: grad-soft capsule tint + accent text (mockup .ddo.on) */
.themed-select__option--selected {
  background: var(--grad-soft);
  color: var(--accent-light);
}

.themed-select__option-label {
  flex: 1;
  min-width: 0;
  overflow: hidden;
  text-overflow: ellipsis;
  white-space: nowrap;
}

.themed-select__option-check {
  flex-shrink: 0;
}

.themed-select__empty {
  padding: 10px;
  color: var(--text-dim);
  font-size: 13px;
  text-align: center;
}

/* ─── Phone (<=767px): touch-sized trigger and option rows. Desktop/tablet
       keep the compact density above — this block only floors the hit areas
       (44px trigger, 40px option rows) at the mobile breakpoint. ─── */
@media (max-width: 767px) {
  .themed-select__trigger {
    min-height: 44px;
  }

  .themed-select--toolbar .themed-select__trigger {
    /* The desktop min-width 240px would overflow a narrow toolbar; consumers
       size the select with flex and long values truncate in the label */
    min-width: 0;
    max-width: 100%;
  }

  .themed-select__menu {
    /* Keep the 240px option-list floor from exceeding the viewport on
       narrow (<=360px) screens; left/width still come from the trigger-rect
       inline style, top-or-bottom likewise */
    min-width: min(240px, calc(100vw - 24px));
  }

  .themed-select__option {
    min-height: 40px;
  }
}
</style>
