// @vitest-environment happy-dom
import { describe, it, expect, vi, beforeEach, afterEach } from 'vitest'
import { mount, flushPromises } from '@vue/test-utils'
import { nextTick } from 'vue'
import TaskDock from '../components/TaskDock.vue'
import { dockReserve, dockSide, dockLane } from '../lib/dockSpace'
import { nudgeDock } from '../lib/dockNudge'
import { DOCK_POSITION_KEY } from '../lib/dockPosition'
import { t } from '../lib/i18n'
import { buildPlatformState, setPlatform } from '../lib/platform'
import { getDownloadTasks, getServerStatus, retryDownloadTask } from '../wails'

// Mock the Wails bridge (window.go is injected only by the Wails runtime).
// Update-related exports are included because lib/update imports the same
// module transitively via TaskDock.
vi.mock('../wails', () => ({
  getLlamaCppDownloadStatus: vi.fn(() => Promise.resolve({ status: 'idle', progress: 0 })),
  getDownloadTasks: vi.fn(() => Promise.resolve([])),
  getServerStatus: vi.fn(() => Promise.resolve({ running: true })),
  getLoadedModels: vi.fn(() =>
    Promise.resolve([{ id: 'test-model', type: 'chat', status: 'loaded' }])
  ),
  unloadModel: vi.fn(() => Promise.resolve()),
  // Phone row-ops bindings (frame ⑲): imported by TaskDock for the pause /
  // resume / retry / cancel circles
  pauseDownloadTask: vi.fn(() => Promise.resolve()),
  resumeDownloadTask: vi.fn(() => Promise.resolve()),
  retryDownloadTask: vi.fn(() => Promise.resolve()),
  cancelDownloadTask: vi.fn(() => Promise.resolve()),
  checkForUpdate: vi.fn(() => Promise.resolve({ hasUpdate: false, version: '', notes: '', published: '' })),
  startUpdateDownload: vi.fn(() => Promise.resolve()),
  stopUpdateDownload: vi.fn(() => Promise.resolve()),
  installUpdate: vi.fn(() => Promise.resolve()),
  getUpdateDownloadStatus: vi.fn(() =>
    Promise.resolve({
      status: 'idle', progress: 0, total: 0, downloaded: 0,
      version: '', filePath: '', error: '', kind: '', installer: false,
    })
  ),
}))

// ─── Controllable ResizeObserver stub ───────────────────────────────────────
// Records instances so tests can manually fire the callback on the last one.

type ROEntry = { target: HTMLElement }

class MockResizeObserver {
  static instances: MockResizeObserver[] = []
  readonly callback: (entries: ROEntry[], observer: MockResizeObserver) => void
  readonly observe = vi.fn()
  readonly disconnect = vi.fn()
  readonly unobserve = vi.fn()
  constructor(callback: (entries: ROEntry[], observer: MockResizeObserver) => void) {
    this.callback = callback
    MockResizeObserver.instances.push(this)
  }
  /** Fire the callback as the browser would after a size change of `target`. */
  trigger(target: HTMLElement) {
    this.callback([{ target }], this)
  }
  static get last(): MockResizeObserver {
    const inst = MockResizeObserver.instances[MockResizeObserver.instances.length - 1]
    if (!inst) throw new Error('no MockResizeObserver instance created')
    return inst
  }
}

/** happy-dom's offsetHeight is always 0; stub a concrete height instead. */
function stubOffsetHeight(el: HTMLElement, height: number) {
  Object.defineProperty(el, 'offsetHeight', { value: height, configurable: true })
}

/** Mount TaskDock with the default mocks and settle the first poll so the
 * dock becomes visible (1 loaded model by default). */
async function mountDock() {
  const wrapper = mount(TaskDock, { attachTo: document.body })
  await flushPromises() // first poll resolves -> visible
  await nextTick() // template ref + watchEffect settle
  return wrapper
}

/** Wait for a <Transition> leave to finish removing the leaving element.
 * Vue applies/removes the transition classes across two requestAnimationFrame
 * hops before removal; in happy-dom those are 0ms timers and the CSS timing
 * resolves instantly (SFC styles are not loaded), so the render tick plus a
 * macrotask sleep longer than both frames is enough. */
async function flushTransitionLeave() {
  await nextTick()
  await new Promise(resolve => setTimeout(resolve, 20))
  await nextTick()
}

beforeEach(() => {
  MockResizeObserver.instances = []
})

afterEach(() => {
  document.body.innerHTML = ''
  vi.unstubAllGlobals()
  // dockSpace keeps module-level state (loaded once per file); drive it back to
  // 0 / the default side / no lane so each test starts from a clean reserve.
  dockReserve.value = 0
  dockSide.value = 'right'
  dockLane.value = 'none'
  localStorage.clear()
  vi.clearAllMocks()
})

// ─── Collapsed pill (default state) ─────────────────────────────────────────

describe('TaskDock collapsed pill', () => {
  it('renders collapsed by default: pill only, no popover', async () => {
    vi.stubGlobal('ResizeObserver', MockResizeObserver)
    const wrapper = await mountDock()

    expect(wrapper.find('.dock-pill').exists()).toBe(true)
    expect(wrapper.find('.dock-popover').exists()).toBe(false)
    expect(wrapper.find('.dock-pill').attributes('aria-label')).toBe(t('dock.title'))
    wrapper.unmount()
  })

  it('shows the loaded model count in the pill', async () => {
    vi.stubGlobal('ResizeObserver', MockResizeObserver)
    const wrapper = await mountDock()

    // Default mocks: server running with exactly 1 loaded model
    expect(wrapper.findAll('.pill-seg')).toHaveLength(1)
    expect(wrapper.find('.dock-pill').text()).toContain('1')
    wrapper.unmount()
  })

  it('shows the active download count in the pill', async () => {
    vi.stubGlobal('ResizeObserver', MockResizeObserver)
    // One downloading model task and no running server, so the download count
    // is the pill's only segment
    vi.mocked(getDownloadTasks).mockImplementationOnce(() =>
      Promise.resolve([{ id: 'task-1', fileName: 'model.gguf', status: 'downloading', progress: 10 }])
    )
    vi.mocked(getServerStatus).mockImplementationOnce(() =>
      Promise.resolve({ running: false, log: [] })
    )

    const wrapper = await mountDock()
    expect(wrapper.findAll('.pill-seg')).toHaveLength(1)
    expect(wrapper.find('.dock-pill').text()).toContain('1')
    wrapper.unmount()
  })
})

// ─── Phone capsule (frame ⑲): dot + separator + mono count model ────────────

describe('TaskDock phone capsule', () => {
  afterEach(() => {
    // Restore the desktop default so other suites in this file keep the
    // desktop pill segments
    setPlatform(buildPlatformState('windows', Number.POSITIVE_INFINITY))
  })

  it('shows the warn percent text while a download actively progresses', async () => {
    vi.stubGlobal('ResizeObserver', MockResizeObserver)
    setPlatform(buildPlatformState('windows', 390))
    vi.mocked(getDownloadTasks).mockImplementationOnce(() =>
      Promise.resolve([
        { id: 'task-1', fileName: 'model.gguf', status: 'downloading', progress: 10, speed: 0 },
      ])
    )
    vi.mocked(getServerStatus).mockImplementationOnce(() =>
      Promise.resolve({ running: false, log: [] })
    )

    const wrapper = await mountDock()
    expect(wrapper.find('.pill-dot--warn').exists()).toBe(true)
    expect(wrapper.find('.pill-count').text()).toBe(t('dock.downloadingPct', { pct: 10 }))
    wrapper.unmount()
  })

  it('shows joined download and model counts when idle', async () => {
    vi.stubGlobal('ResizeObserver', MockResizeObserver)
    setPlatform(buildPlatformState('windows', 390))
    // Default mocks: 1 loaded model, no active downloads
    const wrapper = await mountDock()
    expect(wrapper.find('.pill-dot--warn').exists()).toBe(false)
    expect(wrapper.find('.pill-count').text()).toBe('1')
    wrapper.unmount()
  })
})

// ─── Popover expand/collapse interactions ────────────────────────────────────

describe('TaskDock popover interactions', () => {
  it('expands the popover when the pill is clicked', async () => {
    vi.stubGlobal('ResizeObserver', MockResizeObserver)
    const wrapper = await mountDock()

    await wrapper.find('.dock-pill').trigger('click')
    await nextTick() // watchEffect attaches the document listeners
    expect(wrapper.find('.dock-popover').exists()).toBe(true)
    wrapper.unmount()
  })

  it('collapses on outside click but stays open for clicks inside the dock', async () => {
    vi.stubGlobal('ResizeObserver', MockResizeObserver)
    const wrapper = await mountDock()

    await wrapper.find('.dock-pill').trigger('click')
    await nextTick()
    expect(wrapper.find('.dock-popover').exists()).toBe(true)

    // Click on the body (outside the dock): collapse
    document.body.dispatchEvent(new MouseEvent('click', { bubbles: true }))
    await flushTransitionLeave() // <Transition> removes the card asynchronously
    expect(wrapper.find('.dock-popover').exists()).toBe(false)

    // Re-expand, then click inside the popover: must stay open
    await wrapper.find('.dock-pill').trigger('click')
    await nextTick()
    wrapper.find('.dock-popover').element.dispatchEvent(
      new MouseEvent('click', { bubbles: true })
    )
    await nextTick()
    expect(wrapper.find('.dock-popover').exists()).toBe(true)
    wrapper.unmount()
  })

  it('collapses on Escape', async () => {
    vi.stubGlobal('ResizeObserver', MockResizeObserver)
    const wrapper = await mountDock()

    await wrapper.find('.dock-pill').trigger('click')
    await nextTick()
    expect(wrapper.find('.dock-popover').exists()).toBe(true)

    document.dispatchEvent(new KeyboardEvent('keydown', { key: 'Escape' }))
    await flushTransitionLeave() // <Transition> removes the card asynchronously
    expect(wrapper.find('.dock-popover').exists()).toBe(false)
    wrapper.unmount()
  })

  it('hides the pill visually while the popover is open (morph)', async () => {
    vi.stubGlobal('ResizeObserver', MockResizeObserver)
    // SFC styles are not loaded in unit tests (vitest css processing is off),
    // so mirror the component's pill/morph rules to make the pill's computed
    // visibility observable in happy-dom.
    const style = document.createElement('style')
    style.textContent = `
      .dock-pill { visibility: visible; }
      .dock-pill-hidden { visibility: hidden; opacity: 0; pointer-events: none; }
    `
    document.head.appendChild(style)
    const wrapper = await mountDock()

    await wrapper.find('.dock-pill').trigger('click')
    await nextTick()
    // Morph: while the card is open the pill stays in the DOM (its layout
    // placeholder keeps the measured --dock-reserve constant) but is invisible.
    const pill = wrapper.find('.dock-pill')
    expect(pill.exists()).toBe(true)
    expect(getComputedStyle(pill.element).visibility).toBe('hidden')

    // Outside click collapses the card; the pill fades back in.
    document.body.dispatchEvent(new MouseEvent('click', { bubbles: true }))
    await nextTick()
    expect(getComputedStyle(wrapper.find<HTMLElement>('.dock-pill').element).visibility).toBe('visible')

    style.remove()
    wrapper.unmount()
  })
})

// ─── Draggable capsule (pointer drag + edge snap + position memory) ──────────

describe('TaskDock draggable capsule', () => {
  /** happy-dom reports offsetWidth/Height 0; stub a concrete pill box. */
  function stubPillBox(el: HTMLElement, width: number, height: number) {
    Object.defineProperty(el, 'offsetWidth', { value: width, configurable: true })
    Object.defineProperty(el, 'offsetHeight', { value: height, configurable: true })
  }

  /** Dispatch a PointerEvent-shaped MouseEvent that bubbles up to window. */
  function firePointer(target: EventTarget, type: string, x: number, y: number) {
    target.dispatchEvent(new MouseEvent(type, { clientX: x, clientY: y, bubbles: true }))
  }

  it('drags beyond the threshold, snaps left and does not expand', async () => {
    vi.stubGlobal('ResizeObserver', MockResizeObserver)
    const wrapper = await mountDock()
    const dockEl = wrapper.find<HTMLElement>('.task-dock').element
    stubPillBox(dockEl, 48, 32)
    const pillEl = wrapper.find<HTMLElement>('.dock-pill').element

    // Viewport arithmetic mirrors the default desktop metrics (no title bar /
    // nav in the test DOM): anchor at right 16 / bottom 29, band [8, vh-48].
    const vw = window.innerWidth
    const vh = window.innerHeight
    const pillW = 48
    const pillH = 32
    const anchorX = vw - 16 - pillW
    const anchorY = vh - 29 - pillH
    const dropX = Math.floor(vw * 0.4) // clearly left of mid-width
    const dropY = Math.floor(vh * 0.35) // upper half -> card must anchor below

    firePointer(pillEl, 'pointerdown', vw - 100, vh - 100)
    firePointer(pillEl, 'pointermove', dropX, dropY)
    await nextTick()
    expect(wrapper.find('.task-dock').classes()).toContain('task-dock--dragging')
    expect(wrapper.find('.dock-popover').exists()).toBe(false)

    firePointer(pillEl, 'pointerup', dropX, dropY)
    await nextTick()
    expect(wrapper.find('.task-dock').classes()).not.toContain('task-dock--dragging')

    // Released left of mid-width: snaps to the left edge, keeps the dropped
    // (in-band) height, persists the normalized position, republishes the side.
    const curTop = anchorY + (dropY - (vh - 100))
    expect(curTop).toBeGreaterThanOrEqual(8) // in-band drop for this viewport
    expect(dockSide.value).toBe('left')
    expect(wrapper.find<HTMLElement>('.task-dock').element.style.transform).toBe(
      `translate(${16 - anchorX}px, ${curTop - anchorY}px)`
    )
    const expectedYNorm = (curTop - 8) / (vh - 16 - pillH - 8)
    expect(JSON.parse(localStorage.getItem(DOCK_POSITION_KEY) ?? '{}')).toEqual({
      side: 'left',
      yNorm: expect.closeTo(expectedYNorm, 6),
    })
    // Capsule parked mid-screen (upper half): the chat lane must retract.
    expect(dockLane.value).toBe('none')

    // The drag-release compatibility click must NOT expand the card...
    pillEl.dispatchEvent(new MouseEvent('click', { bubbles: true }))
    await nextTick()
    expect(wrapper.find('.dock-popover').exists()).toBe(false)

    // ...but once the suppression clears, clicking still expands — and the
    // card anchors BELOW the pill, which now rides in the upper half.
    await new Promise((resolve) => setTimeout(resolve, 10))
    await wrapper.find('.dock-pill').trigger('click')
    await nextTick()
    const popover = wrapper.find('.dock-popover')
    expect(popover.exists()).toBe(true)
    expect(popover.classes()).toContain('dock-popover--below')
    wrapper.unmount()
  })

  it('keeps the click behavior for presses within the threshold', async () => {
    vi.stubGlobal('ResizeObserver', MockResizeObserver)
    const wrapper = await mountDock()
    stubPillBox(wrapper.find<HTMLElement>('.task-dock').element, 48, 32)
    const pillEl = wrapper.find<HTMLElement>('.dock-pill').element

    // 5px of travel (< 6px threshold): a click, not a drag
    firePointer(pillEl, 'pointerdown', 900, 700)
    firePointer(pillEl, 'pointermove', 904, 703)
    firePointer(pillEl, 'pointerup', 904, 703)
    expect(wrapper.find('.task-dock').classes()).not.toContain('task-dock--dragging')
    expect(dockSide.value).toBe('right')
    expect(localStorage.getItem(DOCK_POSITION_KEY)).toBeNull()

    // The release click toggles the card normally
    await wrapper.find('.dock-pill').trigger('click')
    await nextTick()
    expect(wrapper.find('.dock-popover').exists()).toBe(true)
    wrapper.unmount()
  })

  it('ignores drag attempts while the popover is open', async () => {
    vi.stubGlobal('ResizeObserver', MockResizeObserver)
    const wrapper = await mountDock()
    stubPillBox(wrapper.find<HTMLElement>('.task-dock').element, 48, 32)

    await wrapper.find('.dock-pill').trigger('click')
    await nextTick()
    expect(wrapper.find('.dock-popover').exists()).toBe(true)

    const pillEl = wrapper.find<HTMLElement>('.dock-pill').element
    firePointer(pillEl, 'pointerdown', 500, 500)
    firePointer(window, 'pointermove', 100, 100)
    firePointer(window, 'pointerup', 100, 100)
    await nextTick()
    expect(wrapper.find('.dock-popover').exists()).toBe(true)
    expect(wrapper.find('.task-dock').classes()).not.toContain('task-dock--dragging')
    expect(dockSide.value).toBe('right')
    expect(wrapper.find<HTMLElement>('.task-dock').element.style.transform).toBe('translate(0px, 0px)')
    wrapper.unmount()
  })

  it('restores a persisted position and re-fits it on resize', async () => {
    localStorage.setItem(DOCK_POSITION_KEY, JSON.stringify({ side: 'left', yNorm: 0 }))
    vi.stubGlobal('ResizeObserver', MockResizeObserver)
    const wrapper = await mountDock()
    stubPillBox(wrapper.find<HTMLElement>('.task-dock').element, 48, 32)

    // Any resize re-fits the remembered position into the current viewport.
    window.dispatchEvent(new Event('resize'))
    await nextTick()

    const vw = window.innerWidth
    const vh = window.innerHeight
    expect(wrapper.find<HTMLElement>('.task-dock').element.style.transform).toBe(
      `translate(${16 - (vw - 16 - 48)}px, ${8 - (vh - 29 - 32)}px)`
    )
    expect(dockSide.value).toBe('left')
    // Restored at the TOP of the band: the chat lane stays retracted.
    expect(dockLane.value).toBe('none')
    wrapper.unmount()
  })

  it('grants the chat lane only for a release inside the composer band', async () => {
    vi.stubGlobal('ResizeObserver', MockResizeObserver)
    const wrapper = await mountDock()
    const dockEl = wrapper.find<HTMLElement>('.task-dock').element
    stubPillBox(dockEl, 48, 32)
    const pillEl = wrapper.find<HTMLElement>('.dock-pill').element

    const vw = window.innerWidth
    const vh = window.innerHeight
    // Drop on the left half, at the very floor of the safe band (bottom-right
    // corner territory): the release clamps to maxTop = vh - 16 - 32, whose
    // bottom edge (vh - 16) is well inside the composer band.
    const dropX = Math.floor(vw * 0.25)
    const dropY = vh - 40

    firePointer(pillEl, 'pointerdown', vw - 100, vh - 100)
    firePointer(pillEl, 'pointermove', dropX, dropY)
    firePointer(pillEl, 'pointerup', dropX, dropY)
    await nextTick()

    expect(dockSide.value).toBe('left')
    expect(JSON.parse(localStorage.getItem(DOCK_POSITION_KEY) ?? '{}')).toEqual({
      side: 'left',
      yNorm: 1,
    })
    // In-band release: the chat page reserves the capsule's side lane.
    expect(dockLane.value).toBe('left')
    wrapper.unmount()
  })

  it('starts from the legacy anchor spot with no stored position', async () => {
    vi.stubGlobal('ResizeObserver', MockResizeObserver)
    const wrapper = await mountDock()
    stubPillBox(wrapper.find<HTMLElement>('.task-dock').element, 48, 32)
    window.dispatchEvent(new Event('resize'))
    await nextTick()
    expect(wrapper.find<HTMLElement>('.task-dock').element.style.transform).toBe('translate(0px, 0px)')
    expect(dockSide.value).toBe('right')
    // The legacy bottom-right spot rides in the composer band: lane granted.
    expect(dockLane.value).toBe('right')
    // Unmount retracts the lane together with the side reset.
    wrapper.unmount()
    expect(dockLane.value).toBe('none')
    expect(dockSide.value).toBe('right')
  })

  it('retracts the chat lane when the dock hides without unmounting', async () => {
    vi.stubGlobal('ResizeObserver', MockResizeObserver)
    const wrapper = await mountDock()
    stubPillBox(wrapper.find<HTMLElement>('.task-dock').element, 48, 32)
    window.dispatchEvent(new Event('resize'))
    await nextTick()
    expect(dockLane.value).toBe('right')

    // Server stops (and nothing else is trackable): the dock hides while the
    // component stays mounted — the lane must retract via the visible watcher.
    vi.mocked(getServerStatus).mockImplementationOnce(() =>
      Promise.resolve({ running: false, log: [] })
    )
    nudgeDock()
    await flushPromises()
    await nextTick()
    expect(dockLane.value).toBe('none')
    wrapper.unmount()
  })
})

// ─── Dock-space wiring smoke (migrated from dockSpace.test.ts) ──────────────

describe('TaskDock dock-space wiring', () => {
  it('publishes the measured root height; the popover never changes it', async () => {
    vi.stubGlobal('ResizeObserver', MockResizeObserver)
    const wrapper = await mountDock()

    expect(wrapper.find('.task-dock').exists()).toBe(true)
    const dockEl = wrapper.find<HTMLElement>('.task-dock').element
    stubOffsetHeight(dockEl, 120)

    // ResizeObserver reports the initial size asynchronously; simulate that
    // initial callback delivery here
    MockResizeObserver.last.trigger(dockEl)
    expect(dockReserve.value).toBe(157) // 120 + 29 + 8

    // Expanding the popover must not change the root's measured height: the
    // popover is absolutely positioned, so the reserve stays constant
    await wrapper.find('.dock-pill').trigger('click')
    await nextTick()
    MockResizeObserver.last.trigger(dockEl)
    expect(dockReserve.value).toBe(157)

    // Unmount clears the polling interval and the reserved space
    wrapper.unmount()
    expect(dockReserve.value).toBe(0)
  })
})

// ─── Queued-row retry op (restart-restored queued tasks have no goroutine) ───

describe('TaskDock queued retry op', () => {
  /** Mount with one model task in the given status and the popover expanded. */
  async function mountDockWithTask(status: string) {
    vi.stubGlobal('ResizeObserver', MockResizeObserver)
    vi.mocked(getDownloadTasks).mockImplementationOnce(() =>
      Promise.resolve([{ id: 'task-1', fileName: 'model.gguf', status, progress: 40, speed: 0 }])
    )
    vi.mocked(getServerStatus).mockImplementationOnce(() =>
      Promise.resolve({ running: false, log: [] })
    )
    const wrapper = await mountDock()
    await wrapper.find('.dock-pill').trigger('click')
    await nextTick()
    return wrapper
  }

  const retrySelector = `.dock-op[aria-label="${t('downloads.retry')}"]`

  it('offers retry on a queued row and calls the retry binding on click', async () => {
    const wrapper = await mountDockWithTask('queued')

    const retryOp = wrapper.find(retrySelector)
    expect(retryOp.exists()).toBe(true)
    // The queued row keeps its cancel escape hatch next to the new retry op
    expect(wrapper.find(`.dock-op[aria-label="${t('downloads.cancel')}"]`).exists()).toBe(true)

    await retryOp.trigger('click')
    await flushPromises()
    expect(retryDownloadTask).toHaveBeenCalledTimes(1)
    expect(retryDownloadTask).toHaveBeenCalledWith('task-1')
    wrapper.unmount()
  })

  it('does not offer retry on non-queued rows', async () => {
    const wrapper = await mountDockWithTask('downloading')
    expect(wrapper.find(retrySelector).exists()).toBe(false)
    wrapper.unmount()
  })
})
