import { flushPromises, mount } from '@vue/test-utils'
import { beforeEach, describe, expect, it, vi } from 'vitest'
import { defineComponent } from 'vue'

const { api, stepUpRun, authState, appState } = vi.hoisted(() => ({
  api: {
    list: vi.fn(),
    getSettings: vi.fn(),
    updateSettings: vi.fn(),
    isolateAccount: vi.fn(),
    clearIsolation: vi.fn(),
  },
  stepUpRun: vi.fn((fn: () => unknown) => fn()),
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

vi.mock('@/composables/useStepUp', () => ({
  useStepUp: () => ({ run: stepUpRun }),
  isStepUpBlocked: () => false,
  isStepUpCancelled: () => false,
  stepUpBlockReason: () => '',
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

const response = (name = 'primary', itemOverrides: Record<string, unknown> = {}) => ({
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
    unschedulable_reason: null,
    unschedulable_until: null,
    evaluated_at: '2026-09-18T00:00:00Z',
    ...itemOverrides,
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
      TotpStepUpDialog: true,
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
    api.isolateAccount.mockResolvedValue(undefined)
    api.clearIsolation.mockResolvedValue(undefined)
    stepUpRun.mockImplementation((fn: () => unknown) => fn())
  })

  it('lets ordinary administrators view health without loading settings', async () => {
    const wrapper = mountView()
    await flushPromises()

    expect(wrapper.text()).toContain('primary')
    expect(wrapper.find('[data-test="settings-button"]').exists()).toBe(false)
    expect(wrapper.find('[data-test="isolate-account-button"]').exists()).toBe(false)
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
    expect(stepUpRun).toHaveBeenCalled()
    expect(appState.showSuccess).toHaveBeenCalledWith('admin.accountHealth.messages.settingsSaved')
    wrapper.unmount()
  })

  it('lets super administrators manually isolate an account with a health-owned reason', async () => {
    authState.isSuperAdmin = true
    const wrapper = mountView()
    await flushPromises()

    await wrapper.find('[data-test="isolate-account-button"]').trigger('click')
    await wrapper.find('textarea').setValue('investigate errors')
    await wrapper.find('[data-test="isolation-form"]').trigger('submit')
    await flushPromises()

    expect(api.isolateAccount).toHaveBeenCalledWith(1, {
      duration_minutes: 60,
      reason: 'investigate errors',
    })
    expect(appState.showSuccess).toHaveBeenCalledWith('admin.accountHealth.messages.isolated')
    wrapper.unmount()
  })

  it('lets super administrators resume health-owned isolation only', async () => {
    authState.isSuperAdmin = true
    api.list.mockResolvedValue(response('isolated account', {
      state: 'isolated',
      temporarily_unschedulable: true,
      unschedulable_reason: 'health:auto:error-rate',
    }))

    const wrapper = mountView()
    await flushPromises()
    await wrapper.find('[data-test="resume-isolation-button"]').trigger('click')
    await flushPromises()

    expect(api.clearIsolation).toHaveBeenCalledWith(1)
    expect(appState.showSuccess).toHaveBeenCalledWith('admin.accountHealth.messages.resumed')
    wrapper.unmount()
  })

  it('keeps foreign temporary unschedulable states read-only in the health view', async () => {
    authState.isSuperAdmin = true
    api.list.mockResolvedValue(response('foreign isolated account', {
      temporarily_unschedulable: true,
      unschedulable_reason: 'stream-timeout',
    }))

    const wrapper = mountView()
    await flushPromises()

    expect(wrapper.find('[data-test="resume-isolation-button"]').exists()).toBe(false)
    expect(wrapper.find('[data-test="isolate-account-button"]').exists()).toBe(false)
    wrapper.unmount()
  })

  it('lets super administrators clear stale health-owned isolation reasons', async () => {
    authState.isSuperAdmin = true
    api.list.mockResolvedValue(response('stale health isolation', {
      temporarily_unschedulable: false,
      unschedulable_reason: 'health:manual:expired maintenance',
    }))

    const wrapper = mountView()
    await flushPromises()

    expect(wrapper.find('[data-test="resume-isolation-button"]').exists()).toBe(true)
    expect(wrapper.find('[data-test="isolate-account-button"]').exists()).toBe(false)
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
