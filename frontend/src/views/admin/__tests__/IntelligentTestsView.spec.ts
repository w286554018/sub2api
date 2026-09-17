import { flushPromises, mount } from '@vue/test-utils'
import { beforeEach, describe, expect, it, vi } from 'vitest'
import { defineComponent } from 'vue'

const { api, authState, appState } = vi.hoisted(() => ({
  api: {
    accounts: vi.fn(),
    jobs: vi.fn(),
    detail: vi.fn(),
    run: vi.fn(),
    cancel: vi.fn(),
    reevaluate: vi.fn(),
    settings: vi.fn(),
    saveSetting: vi.fn(),
    evaluatePreview: vi.fn(),
  },
  authState: { isSuperAdmin: false },
  appState: { showSuccess: vi.fn(), showError: vi.fn() },
}))

vi.mock('@/api/admin/intelligentTests', () => ({
  intelligentTestsAPI: api,
  default: api,
  newIntelligentTestRequestKey: vi.fn(() => 'request-key'),
}))

vi.mock('@/stores', () => ({
  useAuthStore: () => authState,
  useAppStore: () => appState,
}))

vi.mock('vue-i18n', async () => {
  const actual = await vi.importActual<typeof import('vue-i18n')>('vue-i18n')
  return { ...actual, useI18n: () => ({ t: (key: string, params?: Record<string, unknown>) => params?.count === undefined ? key : `${key}:${params.count}` }) }
})

import IntelligentTestsView from '../IntelligentTestsView.vue'

const settings = [
  { test_type: 'candy', enabled: true, config: { prompt: 'p', model: '', evaluator: 'exact_answer', expected_answer: '12', timeout_seconds: 60 } },
  { test_type: 'svg_structure', enabled: true, config: { prompt: 'svg', model: '', evaluator: 'svg_structure', timeout_seconds: 120 } },
]

const mountView = (mode: 'tests' | 'history' | 'settings' = 'tests') => mount(IntelligentTestsView, {
  props: { mode },
  global: {
    stubs: {
      AppLayout: { template: '<div><slot /></div>' },
      RouterLink: { props: ['to'], template: '<a :href="typeof to === \'string\' ? to : to.path"><slot /></a>' },
      Pagination: defineComponent({ props: ['total', 'page', 'pageSize'], emits: ['update:page', 'update:pageSize'], template: '<div data-test="pagination" />' }),
      Select: defineComponent({ props: ['modelValue', 'options'], emits: ['update:modelValue'], template: '<button type="button" data-test="select">select</button>' }),
      BaseDialog: { props: ['show'], template: '<div v-if="show"><slot /><slot name="footer" /></div>' },
      Icon: true,
    },
  },
})

describe('IntelligentTestsView', () => {
  beforeEach(() => {
    vi.clearAllMocks()
    vi.useRealTimers()
    authState.isSuperAdmin = false
    api.settings.mockResolvedValue(structuredClone(settings))
    api.accounts.mockResolvedValue({ items: [], total: 0, page: 1, page_size: 24, overview: { total_accounts: 0, tested_today: 0, success_accounts: 0, abnormal_accounts: 0, suspected_degradation: 0 } })
    api.jobs.mockResolvedValue({ items: [], total: 0, page: 1, page_size: 24 })
    api.saveSetting.mockResolvedValue(undefined)
  })

  it('hides settings navigation from ordinary administrators', async () => {
    const wrapper = mountView('tests')
    await flushPromises()

    expect(wrapper.text()).not.toContain('admin.intelligentTests.modes.settings.title')
    expect(api.settings).toHaveBeenCalled()
    wrapper.unmount()
  })

  it('shows settings to super administrators and saves only dirty settings', async () => {
    authState.isSuperAdmin = true
    const wrapper = mountView('settings')
    await flushPromises()

    expect(wrapper.text()).toContain('admin.intelligentTests.modes.settings.title')
    const textarea = wrapper.find('textarea')
    await textarea.setValue('changed prompt')
    await wrapper.find('form').trigger('submit')
    await flushPromises()

    expect(api.saveSetting).toHaveBeenCalledWith(expect.objectContaining({
      test_type: 'candy',
      config: expect.objectContaining({ prompt: 'changed prompt' }),
    }))
    wrapper.unmount()
  })

  it('does not let a stale account response overwrite a newer load', async () => {
    let finishFirst!: (value: unknown) => void
    api.accounts
      .mockImplementationOnce(() => new Promise(resolve => { finishFirst = resolve }))
      .mockResolvedValueOnce({ items: [{ account_id: 2, name: 'new', platform: 'openai', account_type: 'oauth', account_status: 'active', tests: [] }], total: 1, page: 1, page_size: 24, overview: { total_accounts: 1, tested_today: 0, success_accounts: 0, abnormal_accounts: 0, suspected_degradation: 0 } })
    const wrapper = mountView('tests')
    await flushPromises()

    await wrapper.find('form').trigger('submit')
    await flushPromises()
    finishFirst({ items: [{ account_id: 1, name: 'old', platform: 'openai', account_type: 'oauth', account_status: 'active', tests: [] }], total: 1, page: 1, page_size: 24, overview: { total_accounts: 1, tested_today: 0, success_accounts: 0, abnormal_accounts: 0, suspected_degradation: 0 } })
    await flushPromises()

    expect(wrapper.text()).toContain('new')
    expect(wrapper.text()).not.toContain('old')
    wrapper.unmount()
  })

  it('sanitizes SVG detail output and does not render raw response fields', async () => {
    api.detail.mockResolvedValue({
      id: 5,
      account_id: 9,
      test_type: 'svg_structure',
      status: 'completed',
      score: null,
      result: 'visible result',
      result_image: '<svg onload="alert(1)"><script>alert(1)</script><circle r="5" /></svg>',
      duration_ms: 1,
      model: '',
      created_at: '2026-09-18T00:00:00Z',
      evaluation: { format_verdict: 'compliant' },
    })
    api.jobs.mockResolvedValue({ items: [{ id: 5, account_id: 9, test_type: 'svg_structure', status: 'completed', score: null, result: '', result_image: '', duration_ms: 1, model: '', created_at: '2026-09-18T00:00:00Z' }], total: 1, page: 1, page_size: 24 })
    const wrapper = mountView('history')
    await flushPromises()

    await wrapper.findAll('button').find(button => button.text().includes('common.view'))?.trigger('click')
    await flushPromises()

    expect(wrapper.html()).toContain('<circle')
    expect(wrapper.html()).not.toMatch(/onload=|<script|hidden raw response/i)
    wrapper.unmount()
  })
})
