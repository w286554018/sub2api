import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import { flushPromises, mount } from '@vue/test-utils'

import BillingView from '../BillingView.vue'

const { statement, exportCSV, showError } = vi.hoisted(() => ({
  statement: vi.fn(),
  exportCSV: vi.fn(),
  showError: vi.fn()
}))

vi.mock('@/api/billing', () => ({
  default: { statement, exportCSV }
}))

vi.mock('@/stores/app', () => ({
  useAppStore: () => ({ showError })
}))

vi.mock('vue-i18n', async (importOriginal) => ({
  ...(await importOriginal<typeof import('vue-i18n')>()),
  useI18n: () => ({ t: (key: string) => key })
}))

describe('BillingView', () => {
  beforeEach(() => {
    vi.useFakeTimers()
    vi.setSystemTime(new Date('2026-09-17T04:00:00Z'))
    statement.mockReset().mockResolvedValue({
      rows: [{ model: 'gpt-5.5', requests: 2, total_tokens: 42, cost: 0.25 }],
      requests: 2,
      total_tokens: 42,
      cost: 0.25
    })
    exportCSV.mockReset().mockResolvedValue(new Blob(['csv']))
    showError.mockReset()
    vi.stubGlobal('URL', {
      createObjectURL: vi.fn(() => 'blob:billing'),
      revokeObjectURL: vi.fn()
    })
    vi.spyOn(HTMLAnchorElement.prototype, 'click').mockImplementation(() => {})
  })

  afterEach(() => {
    vi.restoreAllMocks()
    vi.unstubAllGlobals()
    vi.useRealTimers()
  })

  it('loads the current monthly statement on mount', async () => {
    const wrapper = mount(BillingView, {
      global: {
        stubs: { AppLayout: { template: '<div><slot /></div>' }, Icon: true }
      }
    })

    await flushPromises()

    expect(statement).toHaveBeenCalledWith(2026, 9, expect.any(String))
    expect(wrapper.text()).toContain('gpt-5.5')
    expect(wrapper.text()).toContain('42')
  })

  it('exports the selected inclusive date range through the server endpoint', async () => {
    const wrapper = mount(BillingView, {
      global: {
        stubs: { AppLayout: { template: '<div><slot /></div>' }, Icon: true }
      }
    })
    await flushPromises()

    await wrapper.get('[data-testid="billing-export-start"]').setValue('2026-09-01')
    await wrapper.get('[data-testid="billing-export-end"]').setValue('2026-09-17')
    await wrapper.get('[data-testid="billing-export"]').trigger('click')
    await flushPromises()

    expect(exportCSV).toHaveBeenCalledWith('2026-09-01', '2026-09-17', expect.any(String))
  })
})
