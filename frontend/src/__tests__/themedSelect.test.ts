// @vitest-environment happy-dom
import { describe, it, expect, vi, afterEach } from 'vitest'
import { mount, DOMWrapper } from '@vue/test-utils'
import { defineComponent } from 'vue'
import ThemedSelect from '../components/ThemedSelect.vue'
import { selectDisplayLabel } from '../lib/selectOptions'

const options = [
  { value: 'auto', label: 'Auto' },
  { value: 'all', label: 'All' },
  { value: '0', label: 'CPU only' },
]

describe('selectDisplayLabel', () => {
  it('returns the label of the matching option', () => {
    expect(selectDisplayLabel(options, 'all', 'Pick one')).toBe('All')
  })

  it('falls back to the raw value when the value has no matching option', () => {
    expect(selectDisplayLabel(options, 'stale-value', 'Pick one')).toBe('stale-value')
  })

  it('returns the placeholder when the value is empty', () => {
    expect(selectDisplayLabel(options, '', 'Pick one')).toBe('Pick one')
  })

  it('returns the label of the empty-value option when present (e.g. "default" choice)', () => {
    expect(selectDisplayLabel([{ value: '', label: 'Default (F16)' }], '', 'Pick one')).toBe('Default (F16)')
  })
})

// The menu is teleported to <body>, so menu lookups must go through the
// document, not through the component wrapper.
function menuEl(): HTMLElement | null {
  return document.body.querySelector<HTMLElement>('.themed-select__menu')
}

function optionEls(): HTMLElement[] {
  return Array.from(document.body.querySelectorAll<HTMLElement>('.themed-select__option'))
}

function wrapperOf(el: Element): DOMWrapper<Element> {
  return new DOMWrapper(el)
}

/** Plain-object DOMRect stand-in for getBoundingClientRect mocks. */
function rect(left: number, top: number, width: number, height: number): DOMRect {
  return {
    left,
    top,
    width,
    height,
    right: left + width,
    bottom: top + height,
    x: left,
    y: top,
    toJSON: () => ({}),
  } as DOMRect
}

describe('ThemedSelect', () => {
  afterEach(() => {
    document.body.innerHTML = ''
    vi.restoreAllMocks()
  })

  function mountSelect(props: Record<string, unknown> = {}) {
    return mount(ThemedSelect, {
      props: {
        options,
        ...props,
      },
      attachTo: document.body,
    })
  }

  it('shows the label of the selected value', () => {
    const wrapper = mountSelect({ modelValue: 'auto' })
    expect(wrapper.find('.themed-select__value').text()).toBe('Auto')
    wrapper.unmount()
  })

  it('shows the placeholder when nothing is selected', () => {
    const wrapper = mountSelect({ modelValue: '', placeholder: 'Pick a model' })
    expect(wrapper.find('.themed-select__value').text()).toBe('Pick a model')
    wrapper.unmount()
  })

  it('opens the menu on trigger click and closes on a second click', async () => {
    const wrapper = mountSelect()
    expect(menuEl()).toBeNull()
    await wrapper.find('.themed-select__trigger').trigger('click')
    expect(menuEl()).not.toBeNull()
    await wrapper.find('.themed-select__trigger').trigger('click')
    expect(menuEl()).toBeNull()
    wrapper.unmount()
  })

  it('teleports the open menu to document.body, outside the component element', async () => {
    const wrapper = mountSelect()
    await wrapper.find('.themed-select__trigger').trigger('click')
    const menu = menuEl()
    expect(menu).not.toBeNull()
    // Teleported: a direct child of <body>, NOT inside the component's root
    expect(menu?.parentElement).toBe(document.body)
    expect(wrapper.find('.themed-select__menu').exists()).toBe(false)
    wrapper.unmount()
  })

  it('positions the fixed menu at the trigger rect (left/width, 6px below)', async () => {
    const wrapper = mountSelect()
    const trigger = wrapper.find('.themed-select__trigger').element as HTMLElement
    vi.spyOn(trigger, 'getBoundingClientRect').mockReturnValue(rect(120, 40, 200, 32))
    await wrapper.find('.themed-select__trigger').trigger('click')
    const menu = menuEl()
    expect(menu?.style.left).toBe('120px')
    expect(menu?.style.width).toBe('200px')
    // Default placement: trigger bottom (40 + 32 = 72) + 6px gap
    expect(menu?.style.top).toBe('78px')
    expect(menu?.style.bottom).toBe('')
    wrapper.unmount()
  })

  it('flips the menu above the trigger when it would overflow the viewport bottom', async () => {
    const wrapper = mountSelect()
    const vh = window.innerHeight
    const trigger = wrapper.find('.themed-select__trigger').element as HTMLElement
    // bottom = vh - 68, so bottom + 306 (max-height 300 + gap) always exceeds vh
    vi.spyOn(trigger, 'getBoundingClientRect').mockReturnValue(rect(10, vh - 100, 200, 32))
    await wrapper.find('.themed-select__trigger').trigger('click')
    const menu = menuEl()
    expect(menu?.style.bottom).toBe((vh - (vh - 100) + 6) + 'px')
    expect(menu?.style.top).toBe('')
    wrapper.unmount()
  })

  it('emits update:modelValue and removes the menu from body after selecting an option', async () => {
    const wrapper = mountSelect({ modelValue: 'auto' })
    await wrapper.find('.themed-select__trigger').trigger('click')
    const opts = optionEls()
    await wrapperOf(opts[1]).trigger('click')
    expect(wrapper.emitted('update:modelValue')).toEqual([['all']])
    expect(menuEl()).toBeNull()
    wrapper.unmount()
  })

  it('marks the selected option with the check icon', async () => {
    const wrapper = mountSelect({ modelValue: '0' })
    await wrapper.find('.themed-select__trigger').trigger('click')
    const selected = menuEl()?.querySelector('.themed-select__option--selected')
    expect(selected).not.toBeNull()
    expect(selected?.querySelector('.themed-select__option-check')).not.toBeNull()
    wrapper.unmount()
  })

  it('closes the menu when clicking outside', async () => {
    const wrapper = mountSelect()
    await wrapper.find('.themed-select__trigger').trigger('click')
    expect(menuEl()).not.toBeNull()
    document.body.dispatchEvent(new MouseEvent('click', { bubbles: true }))
    await wrapper.vm.$nextTick()
    expect(menuEl()).toBeNull()
    wrapper.unmount()
  })

  it('closes the open menu on a viewport scroll outside the menu', async () => {
    const wrapper = mountSelect()
    await wrapper.find('.themed-select__trigger').trigger('click')
    expect(menuEl()).not.toBeNull()
    window.dispatchEvent(new Event('scroll'))
    await wrapper.vm.$nextTick()
    expect(menuEl()).toBeNull()
    wrapper.unmount()
  })

  it('keeps the menu open when the scroll happens inside the menu viewport', async () => {
    const wrapper = mountSelect()
    await wrapper.find('.themed-select__trigger').trigger('click')
    menuEl()?.dispatchEvent(new Event('scroll'))
    await wrapper.vm.$nextTick()
    expect(menuEl()).not.toBeNull()
    wrapper.unmount()
  })

  it('closes the open menu on a viewport resize', async () => {
    const wrapper = mountSelect()
    await wrapper.find('.themed-select__trigger').trigger('click')
    expect(menuEl()).not.toBeNull()
    window.dispatchEvent(new Event('resize'))
    await wrapper.vm.$nextTick()
    expect(menuEl()).toBeNull()
    wrapper.unmount()
  })

  it('does not open when disabled', async () => {
    const wrapper = mountSelect({ disabled: true })
    await wrapper.find('.themed-select__trigger').trigger('click')
    expect(menuEl()).toBeNull()
    wrapper.unmount()
  })

  it('shows the empty text when there are no options', async () => {
    const wrapper = mountSelect({ options: [], emptyText: 'No models' })
    await wrapper.find('.themed-select__trigger').trigger('click')
    expect(menuEl()?.querySelector('.themed-select__empty')?.textContent).toBe('No models')
    wrapper.unmount()
  })

  it('works with v-model through a host component', async () => {
    const Host = defineComponent({
      components: { ThemedSelect },
      template: `<ThemedSelect v-model="value" :options="options" />`,
      data: () => ({ value: 'auto', options }),
    })
    const wrapper = mount(Host, { attachTo: document.body })
    await wrapper.find('.themed-select__trigger').trigger('click')
    await wrapperOf(optionEls()[2]).trigger('click')
    expect(wrapper.vm.$data.value).toBe('0')
    wrapper.unmount()
  })

  it('opens the menu when Enter is pressed on the trigger', async () => {
    const wrapper = mountSelect()
    const trigger = wrapper.find('.themed-select__trigger')
    await trigger.trigger('keydown', { key: 'Enter' })
    expect(trigger.attributes('aria-expanded')).toBe('true')
    expect(optionEls()).toHaveLength(3)
    wrapper.unmount()
  })

  it('moves the highlight down from the selected option and wires aria-activedescendant', async () => {
    const wrapper = mountSelect({ modelValue: 'all' })
    const trigger = wrapper.find('.themed-select__trigger')
    await trigger.trigger('click')
    await trigger.trigger('keydown', { key: 'ArrowDown' })
    const items = optionEls()
    const highlighted = menuEl()?.querySelectorAll('.themed-select__option--highlighted')
    expect(highlighted).toHaveLength(1)
    expect(highlighted?.[0]).toBe(items[2])
    expect(trigger.attributes('aria-activedescendant')).toBe(items[2].getAttribute('id'))
    wrapper.unmount()
  })

  it('wraps ArrowUp from the first option to the last and ArrowDown from the last to the first', async () => {
    const wrapper = mountSelect({ modelValue: 'auto' })
    const trigger = wrapper.find('.themed-select__trigger')
    await trigger.trigger('click')
    const items = optionEls()
    await trigger.trigger('keydown', { key: 'ArrowUp' })
    expect(menuEl()?.querySelector('.themed-select__option--highlighted')).toBe(items[2])
    await trigger.trigger('keydown', { key: 'ArrowDown' })
    expect(menuEl()?.querySelector('.themed-select__option--highlighted')).toBe(items[0])
    wrapper.unmount()
  })

  it('selects the highlighted option via the full keyboard path and closes the menu', async () => {
    const wrapper = mountSelect({ modelValue: 'auto' })
    const trigger = wrapper.find('.themed-select__trigger')
    await trigger.trigger('keydown', { key: 'Enter' })
    await trigger.trigger('keydown', { key: 'ArrowDown' })
    await trigger.trigger('keydown', { key: 'ArrowDown' })
    await trigger.trigger('keydown', { key: 'Enter' })
    expect(wrapper.emitted('update:modelValue')).toEqual([['0']])
    expect(menuEl()).toBeNull()
    wrapper.unmount()
  })

  it('closes the open menu with Escape', async () => {
    const wrapper = mountSelect()
    const trigger = wrapper.find('.themed-select__trigger')
    await trigger.trigger('click')
    expect(menuEl()).not.toBeNull()
    await trigger.trigger('keydown', { key: 'Escape' })
    expect(menuEl()).toBeNull()
    wrapper.unmount()
  })

  it('moves the highlight to the first option with Home and the last with End', async () => {
    const wrapper = mountSelect({ modelValue: 'all' })
    const trigger = wrapper.find('.themed-select__trigger')
    await trigger.trigger('click')
    const items = optionEls()
    await trigger.trigger('keydown', { key: 'End' })
    expect(menuEl()?.querySelector('.themed-select__option--highlighted')).toBe(items[2])
    await trigger.trigger('keydown', { key: 'Home' })
    expect(menuEl()?.querySelector('.themed-select__option--highlighted')).toBe(items[0])
    wrapper.unmount()
  })

  it('does not open with the keyboard when disabled', async () => {
    const wrapper = mountSelect({ disabled: true })
    await wrapper.find('.themed-select__trigger').trigger('keydown', { key: 'Enter' })
    expect(menuEl()).toBeNull()
    wrapper.unmount()
  })

  it('highlights the hovered option', async () => {
    const wrapper = mountSelect()
    await wrapper.find('.themed-select__trigger').trigger('click')
    const items = optionEls()
    await wrapperOf(items[1]).trigger('mouseenter')
    const highlighted = menuEl()?.querySelectorAll('.themed-select__option--highlighted')
    expect(highlighted).toHaveLength(1)
    expect(highlighted?.[0]).toBe(items[1])
    wrapper.unmount()
  })
})
