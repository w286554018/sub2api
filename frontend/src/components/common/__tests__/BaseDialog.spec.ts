import { afterEach, describe, expect, it, vi } from 'vitest'
import { VueWrapper, mount } from '@vue/test-utils'
import { nextTick } from 'vue'
import BaseDialog from '../BaseDialog.vue'

vi.mock('vue-i18n', () => ({
  useI18n: () => ({ t: (key: string) => key })
}))

describe('BaseDialog', () => {
  const wrappers: VueWrapper[] = []

  const mountDialog = (options: Parameters<typeof mount>[1]) => {
    const wrapper = mount(BaseDialog, {
      attachTo: document.body,
      ...options,
      global: {
        stubs: { Icon: true },
        ...options?.global
      }
    })
    wrappers.push(wrapper)
    return wrapper
  }

  const pressKey = async (key: string, init: KeyboardEventInit = {}) => {
    document.dispatchEvent(new KeyboardEvent('keydown', { key, bubbles: true, cancelable: true, ...init }))
    await nextTick()
  }

  afterEach(() => {
    wrappers.splice(0).forEach(wrapper => wrapper.unmount())
    document.body.innerHTML = ''
    document.body.classList.remove('modal-open')
  })

  it('resets body scroll position when reopened', async () => {
    const wrapper = mountDialog({
      props: { show: false, title: 'Details' },
      slots: { default: '<div style="height: 2000px">content</div>' }
    })

    await wrapper.setProps({ show: true })
    await nextTick()
    const body = document.body.querySelector<HTMLElement>('.modal-body')
    expect(body).not.toBeNull()
    body!.scrollTop = 480

    await wrapper.setProps({ show: false })
    await wrapper.setProps({ show: true })
    await nextTick()

    expect(document.body.querySelector<HTMLElement>('.modal-body')?.scrollTop).toBe(0)
  })

  it('only lets the top nested dialog handle Escape', async () => {
    const parent = mountDialog({ props: { show: true, title: 'Parent' } })
    await nextTick()
    const child = mountDialog({ props: { show: true, title: 'Child' } })
    await nextTick()

    await pressKey('Escape')

    expect(child.emitted('close')).toHaveLength(1)
    expect(parent.emitted('close')).toBeUndefined()
  })

  it('keeps body scroll locked until the final dialog closes', async () => {
    const parent = mountDialog({ props: { show: true, title: 'Parent' } })
    await nextTick()
    const child = mountDialog({ props: { show: true, title: 'Child' } })
    await nextTick()

    expect(document.body.classList.contains('modal-open')).toBe(true)

    await child.setProps({ show: false })
    await nextTick()
    expect(document.body.classList.contains('modal-open')).toBe(true)

    await parent.setProps({ show: false })
    await nextTick()
    expect(document.body.classList.contains('modal-open')).toBe(false)
  })

  it('cycles focus within the top dialog on Tab and Shift+Tab', async () => {
    mountDialog({
      props: { show: true, title: 'Focusable', showCloseButton: false },
      slots: {
        default: '<button id="first-action">First</button><button id="last-action">Last</button>'
      }
    })
    await nextTick()

    const first = document.getElementById('first-action') as HTMLButtonElement
    const last = document.getElementById('last-action') as HTMLButtonElement

    expect(document.activeElement).toBe(first)

    first.focus()
    await pressKey('Tab', { shiftKey: true })
    expect(document.activeElement).toBe(last)

    await pressKey('Tab')
    expect(document.activeElement).toBe(first)
  })

  it('focuses the dialog panel when no focusable element exists', async () => {
    mountDialog({
      props: { show: true, title: 'Empty', showCloseButton: false },
      slots: { default: '<span>Plain content</span>' }
    })
    await nextTick()

    const panel = document.body.querySelector<HTMLElement>('.modal-content')
    expect(document.activeElement).toBe(panel)
  })

  it('skips controls hidden by their type or an inaccessible ancestor', async () => {
    mountDialog({
      props: { show: true, title: 'Filtered focus', showCloseButton: false },
      slots: {
        default: `
          <input id="hidden-input" type="hidden" />
          <div hidden><button id="hidden-action">Hidden</button></div>
          <div inert><button id="inert-action">Inert</button></div>
          <a id="negative-tab" href="#" tabindex="-1">Skipped</a>
          <button id="visible-action">Visible</button>
        `
      }
    })
    await nextTick()

    expect(document.activeElement).toBe(document.getElementById('visible-action'))
  })

  it('restores focus to the previously focused element when closed', async () => {
    const trigger = document.createElement('button')
    trigger.textContent = 'Open dialog'
    document.body.appendChild(trigger)
    trigger.focus()

    const wrapper = mountDialog({
      props: { show: true, title: 'Restorable', showCloseButton: false },
      slots: { default: '<button id="dialog-action">Action</button>' }
    })
    await nextTick()

    expect(document.activeElement).toBe(document.getElementById('dialog-action'))

    await wrapper.setProps({ show: false })
    await nextTick()

    expect(document.activeElement).toBe(trigger)
  })

  it('assigns stable increasing z-indexes with the top dialog highest', async () => {
    const parent = mountDialog({ props: { show: true, title: 'Parent', zIndex: 100 } })
    await nextTick()
    mountDialog({ props: { show: true, title: 'Child', zIndex: 50 } })
    await nextTick()

    const overlays = Array.from(document.body.querySelectorAll<HTMLElement>('.modal-overlay'))
    expect(Number(overlays[0].style.zIndex)).toBe(100)
    expect(Number(overlays[1].style.zIndex)).toBeGreaterThan(Number(overlays[0].style.zIndex))

    await parent.setProps({ zIndex: 300 })
    await nextTick()

    expect(Number(overlays[0].style.zIndex)).toBe(300)
    expect(Number(overlays[1].style.zIndex)).toBeGreaterThan(Number(overlays[0].style.zIndex))
  })

  it('does not restore outside focus when a non-top dialog closes', async () => {
    const trigger = document.createElement('button')
    document.body.appendChild(trigger)
    trigger.focus()

    const parent = mountDialog({ props: { show: true, title: 'Parent' } })
    await nextTick()
    mountDialog({
      props: { show: true, title: 'Child', showCloseButton: false },
      slots: { default: '<button id="child-action">Child action</button>' }
    })
    await nextTick()

    const childAction = document.getElementById('child-action')
    expect(document.activeElement).toBe(childAction)

    await parent.setProps({ show: false })
    await nextTick()

    expect(document.activeElement).toBe(childAction)
  })

  it('restores the external trigger when a whole dialog stack closes together', async () => {
    const trigger = document.createElement('button')
    document.body.appendChild(trigger)
    trigger.focus()

    const parent = mountDialog({ props: { show: true, title: 'Parent' } })
    await nextTick()
    const child = mountDialog({ props: { show: true, title: 'Child' } })
    await nextTick()

    const parentClosing = parent.setProps({ show: false })
    const childClosing = child.setProps({ show: false })
    await Promise.all([parentClosing, childClosing])
    await nextTick()

    expect(document.activeElement).toBe(trigger)
  })
})
