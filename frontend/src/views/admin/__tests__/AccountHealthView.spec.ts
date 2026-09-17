import { flushPromises, mount } from '@vue/test-utils'
import { beforeEach, describe, expect, it, vi } from 'vitest'
import { defineComponent } from 'vue'

const { api, authState, appState } = vi.hoisted(() => ({
  api: {
    list: vi.fn(),
    getSettings: vi.fn(),
    updateSettings: vi.fn(),
  },
  authState: { isSuperAdmin: false },
  appState: { showSuccess: vi.fn(), showError: vi.fn() },
}))

vi.mock('@/api/admin/accountHealth', () => ({
  default: api,
  accountHealthAPI: api,
}))

vi.mock('@/stores', () => ({
  useAuthStore: () => authState,
  useAppStore: () => appState,
}))

vi.mock('vue-i18n', async () => {
  const actual = await vi.importActual<typeof import('vue-i18n')>('vue-i18n')
  return {
    ...actual,
    useI18n: () => ({
      t: (key: string, params?: Record<string, unknown>) => params ? `${key}:${Object.values(params).join(',')}` : key,
    }),
  }
})

vi.mock('@/utils/apiError', () => ({
  extractApiErrorMessage: (_err: unknown, fallback: string) => fallback,
}))

import AccountHealthView from '../AccountHealthView.vue'

const settings = {
  enabled: true,
  window_minutes: 60,
  min_samples: 5,
  isolate_error_rate: 0.5,
  recover_error_rate: 0.1,
  cooldown_minutes: 10,
  interval_seconds: 30,
}

const response = (name = 'primary') => ({
  items: [{
    account_id: 1,
    name,
    platform: 'openai',
    account_status: 'active',
    success_count: 9,
    error_count: 1,
    total_count: 10,
    error_rate: 0.1,
    avg_latency_ms: 123.4,
    score: 0.92,
    state: 'healthy',
    has_enough_samples: true,
    temporarily_unschedulable: false,
    evaluated_at: '2026-09-18T00:00:00Z',
  }],
  total: 1,
  page: 1,
  page_size: 20,
  evaluated_at: '2026-09-18T00:00:00Z',
  window_minutes: 60,
  overview: { healthy: 1, degraded: 0, isolated: 0, no_samples: 0, total_accounts: 1 },
})

const mountView = () => mount(AccountHealthView, {
  global: {
    stubs: {
      AppLayout: { template: '<div><slot /></div>' },
      BaseDialog: { props: ['show', 'title'], template: '<div v-if="show"><slot /><slot name="footer" /></div>' },
      Icon: true,
      Pagination: defineComponent({ props: ['total', 'page', 'pageSize'], emits: ['update:page', 'update:pageSize'], template: '<div data-test="pagination" />' }),
      Select: defineComponent({ props: ['modelValue', 'options'], emits: ['update:modelValue'], template: '<button type="button" data-test="select">select</button>' }),
    },
  },
})

describe('AccountHealthView', () => {
  beforeEach(() => {
    vi.clearAllMocks()
    authState.isSuperAdmin = false
    api.list.mockResolvedValue(response())
    api.getSettings.mockResolvedValue({ ...settings })
    api.updateSettings.mockResolvedValue({ ...settings, enabled: false })
  })

  it('lets ordinary administrators view health without loading settings', async () => {
    const wrapper = mountView()
    await flushPromises()

    expect(wrapper.text()).toContain('primary')
    expect(wrapper.find('[data-test="settings-button"]').exists()).toBe(false)
    expect(api.getSettings).not.toHaveBeenCalled()
    expect(api.list).toHaveBeenCalledWith({ page: 1, page_size: 20 }, expect.objectContaining({ signal: expect.any(AbortSignal) }))
    wrapper.unmount()
  })

  it('shows settings to super administrators and saves the backend payload', async () => {
    authState.isSuperAdmin = true
    const wrapper = mountView()
    await flushPromises()

    await wrapper.find('[data-test="settings-button"]').trigger('click')
    await flushPromises()
    expect(api.getSettings).toHaveBeenCalledWith(expect.objectContaining({ signal: expect.any(AbortSignal) }))

    const enabled = wrapper.find('input[type="checkbox"]')
    await enabled.setValue(false)
    await wrapper.find('[data-test="settings-form"]').trigger('submit')
    await flushPromises()

    expect(api.updateSettings).toHaveBeenCalledWith({ ...settings, enabled: false })
    expect(appState.showSuccess).toHaveBeenCalledWith('admin.accountHealth.messages.settingsSaved')
    wrapper.unmount()
  })

  it('rejects invalid threshold order before calling the API', async () => {
    authState.isSuperAdmin = true
    const wrapper = mountView()
    await flushPromises()

    await wrapper.find('[data-test="settings-button"]').trigger('click')
    await flushPromises()
    const rateInputs = wrapper.findAll('input[type="number"]').slice(-2)
    await rateInputs[0].setValue('0.2')
    await rateInputs[1].setValue('0.2')
    await wrapper.find('[data-test="settings-form"]').trigger('submit')
    await flushPromises()

    expect(api.updateSettings).not.toHaveBeenCalled()
    expect(appState.showError).toHaveBeenCalledWith('admin.accountHealth.errors.settingsInvalid')
    wrapper.unmount()
  })

  it('does not let a stale list response overwrite a newer load', async () => {
    let finishFirst!: (value: unknown) => void
    api.list
      .mockImplementationOnce(() => new Promise(resolve => { finishFirst = resolve }))
      .mockResolvedValueOnce(response('new account'))

    const wrapper = mountView()
    await flushPromises()

    await wrapper.find('form').trigger('submit')
    await flushPromises()
    finishFirst(response('old account'))
    await flushPromises()

    expect(wrapper.text()).toContain('new account')
    expect(wrapper.text()).not.toContain('old account')
    wrapper.unmount()
  })
})
