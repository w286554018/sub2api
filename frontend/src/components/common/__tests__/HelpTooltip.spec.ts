import { afterEach, describe, expect, it, vi } from 'vitest'
import { mount } from '@vue/test-utils'
import { nextTick } from 'vue'
import HelpTooltip from '@/components/common/HelpTooltip.vue'

function getTooltipElement(): HTMLDivElement {
  const tooltip = document.body.querySelector('[role="tooltip"]')
  if (!(tooltip instanceof HTMLDivElement)) {
    throw new Error('tooltip element not found')
  }
  return tooltip
}

describe('HelpTooltip', () => {
  afterEach(() => {
    document.body.innerHTML = ''
    vi.restoreAllMocks()
    vi.unstubAllGlobals()
  })

  it('keeps the existing hover interaction by default', async () => {
    const wrapper = mount(HelpTooltip, {
      attachTo: document.body,
      props: {
        content: 'hover details',
      },
    })

    const trigger = wrapper.get('.group')
    const tooltip = getTooltipElement()

    expect(tooltip.style.display).toBe('none')

    await trigger.trigger('mouseenter')
    await nextTick()
    expect(tooltip.style.display).not.toBe('none')

    await trigger.trigger('mouseleave')
    await nextTick()
    expect(tooltip.style.display).toBe('none')

    wrapper.unmount()
  })

  it('keeps a hover tooltip open while the pointer moves between the trigger and the tooltip', async () => {
    const wrapper = mount(HelpTooltip, {
      attachTo: document.body,
      props: {
        content: 'copyable details',
      },
    })

    const trigger = wrapper.get('.group')
    const tooltip = getTooltipElement()

    await trigger.trigger('mouseenter')
    await nextTick()
    expect(tooltip.style.display).not.toBe('none')

    await trigger.trigger('mouseleave', { relatedTarget: tooltip })
    await nextTick()
    expect(tooltip.style.display).not.toBe('none')

    tooltip.dispatchEvent(new MouseEvent('mouseleave', { relatedTarget: trigger.element }))
    await nextTick()
    expect(tooltip.style.display).not.toBe('none')

    tooltip.dispatchEvent(new MouseEvent('mouseleave', { relatedTarget: null }))
    await nextTick()
    expect(tooltip.style.display).toBe('none')

    wrapper.unmount()
  })

  it('supports click-to-toggle details and closes on outside click', async () => {
    const wrapper = mount(HelpTooltip, {
      attachTo: document.body,
      props: {
        content: 'click details',
        trigger: 'click',
      },
    })

    const trigger = wrapper.get('.group')
    const tooltip = getTooltipElement()

    expect(tooltip.style.display).toBe('none')

    await trigger.trigger('click')
    await nextTick()
    expect(tooltip.style.display).not.toBe('none')
    expect(tooltip.textContent).toContain('click details')

    const closeButton = tooltip.querySelector('button[aria-label="Close"]')
    if (!(closeButton instanceof HTMLButtonElement)) {
      throw new Error('close button not found')
    }
    closeButton.click()
    await nextTick()
    expect(tooltip.style.display).toBe('none')

    await trigger.trigger('click')
    await nextTick()
    expect(tooltip.style.display).not.toBe('none')

    document.body.dispatchEvent(new MouseEvent('click', { bubbles: true }))
    await nextTick()
    expect(tooltip.style.display).toBe('none')

    wrapper.unmount()
  })

  // 回归：气泡是 position: fixed（视口坐标系），而 getBoundingClientRect() 已是视口坐标。
  // 曾经额外加了 scrollX/scrollY，导致滚动距离被重复计一次，页面下滚后气泡跑到触发元素下方。
  it('positions the tooltip in viewport coordinates without double-counting scroll', async () => {
    const rect = { top: 300, left: 100, width: 20, height: 16 } as DOMRect
    const wrapper = mount(HelpTooltip, {
      attachTo: document.body,
      props: { content: 'positioned' },
    })

    const trigger = wrapper.get('.group')
    ;(trigger.element as HTMLElement).getBoundingClientRect = () => rect
    // 模拟页面已向下滚动：滚动量不应进入最终坐标。
    Object.defineProperty(window, 'scrollY', { value: 400, configurable: true })
    Object.defineProperty(window, 'scrollX', { value: 50, configurable: true })

    await trigger.trigger('mouseenter')
    await nextTick()

    const tooltip = getTooltipElement()
    // top: rect.top(300) 经模板的 calc(... - 8px) 得 292；若 scrollY 被重复计入则是 692。
    // left: rect.left(100) + width/2(10) = 110；若 scrollX 被重复计入则是 160。
    expect(tooltip.style.top).toContain('292px')
    expect(tooltip.style.left).toBe('110px')

    wrapper.unmount()
  })
})


describe('HelpTooltip viewport positioning', () => {
  afterEach(() => {
    vi.restoreAllMocks()
    vi.unstubAllGlobals()
    document.body.innerHTML = ''
  })

  it.each([320, 375, 390, 768, 1440])('keeps details inside a %ipx viewport at either edge', async (width) => {
    vi.stubGlobal('innerWidth', width)
    vi.stubGlobal('innerHeight', 812)
    vi.stubGlobal('scrollX', 200)
    vi.stubGlobal('scrollY', 500)
    const wrapper = mount(HelpTooltip, { attachTo: document.body, props: { content: 'details' } })
    const trigger = wrapper.get('.group')
    const tooltip = getTooltipElement()
    vi.spyOn(tooltip, 'getBoundingClientRect').mockReturnValue({ width: 280, height: 100 } as DOMRect)
    const anchor = vi.spyOn(trigger.element, 'getBoundingClientRect')
    for (const x of [0, width - 24]) {
      anchor.mockReturnValue({ left: x, right: x + 16, width: 16, top: 200, bottom: 216 } as DOMRect)
      await trigger.trigger('mouseenter')
      await nextTick()
      expect(parseFloat(tooltip.style.left)).toBeGreaterThanOrEqual(8)
      expect(parseFloat(tooltip.style.left) + 280).toBeLessThanOrEqual(width - 8)
      // Fixed positioning must not include document scroll offsets.
      expect(parseFloat(tooltip.style.top)).toBe(92)
      await trigger.trigger('mouseleave')
    }
    anchor.mockReturnValue({ left: 0, right: 16, width: 16, top: 0, bottom: 16 } as DOMRect)
    await trigger.trigger('mouseenter')
    await nextTick()
    expect(parseFloat(tooltip.style.top)).toBe(24)
    anchor.mockReturnValue({ left: 0, right: 16, width: 16, top: 150, bottom: 166 } as DOMRect)
    window.dispatchEvent(new Event('scroll'))
    await nextTick()
    expect(parseFloat(tooltip.style.top)).toBe(42)
    vi.stubGlobal('innerWidth', 320)
    window.dispatchEvent(new Event('resize'))
    await nextTick()
    expect(parseFloat(tooltip.style.left) + 280).toBeLessThanOrEqual(312)
    wrapper.unmount()
  })

  it('opens hover details with one touch click, toggles, and dismisses outside or with Escape', async () => {
    const wrapper = mount(HelpTooltip, { attachTo: document.body, props: { content: 'touch details' } })
    const trigger = wrapper.get('.group')
    await trigger.trigger('pointerdown', { pointerType: 'touch' })
    const tooltip = getTooltipElement()
    await trigger.trigger('mouseenter')
    expect(tooltip.style.display).toBe('none')
    await trigger.trigger('click')
    expect(tooltip.style.display).not.toBe('none')
    await trigger.trigger('mouseleave')
    expect(tooltip.style.display).not.toBe('none')
    await trigger.trigger('click')
    expect(tooltip.style.display).toBe('none')
    await trigger.trigger('click')
    document.dispatchEvent(new KeyboardEvent('keydown', { key: 'Escape' }))
    await nextTick()
    expect(tooltip.style.display).toBe('none')
    await trigger.trigger('click')
    document.body.click()
    await nextTick()
    expect(tooltip.style.display).toBe('none')
    wrapper.unmount()
  })
})
