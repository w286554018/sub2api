import { describe, expect, it, vi } from 'vitest'
import { flushPromises, mount } from '@vue/test-utils'

const { syncUpstreamModels } = vi.hoisted(() => ({
  syncUpstreamModels: vi.fn()
}))

vi.mock('@/api/admin/accounts', () => ({
  accountsAPI: { syncUpstreamModels, syncUpstreamModelsPreview: vi.fn() }
}))
vi.mock('@/stores/app', () => ({
  useAppStore: () => ({
    showSuccess: vi.fn(), showInfo: vi.fn(), showWarning: vi.fn(), showError: vi.fn()
  })
}))
vi.mock('@/composables/useClipboard', () => ({
  useClipboard: () => ({ copyToClipboard: vi.fn() })
}))
vi.mock('vue-i18n', () => ({
  useI18n: () => ({ t: (key: string) => key })
}))

import ModelWhitelistSelector from '../ModelWhitelistSelector.vue'

describe('ModelWhitelistSelector source compatibility', () => {
  it('keeps Kiro upstream IDs while reconciling concrete models and live aliases', async () => {
    syncUpstreamModels.mockResolvedValue({
      discovery_source: 'upstream',
      models: [' kiro-live ', 'kiro-live', 'wildcard-*'],
      metadata: { 'kiro-live': { id: ' provider-live ' } }
    })
    const wrapper = mount(ModelWhitelistSelector, {
      props: {
        platform: 'kiro',
        kiroDirect: true,
        accountId: 46,
        modelValue: ['removed-model'],
        modelMappings: [
          { from: 'public-*', to: 'kiro-live' },
          { from: 'retired-alias', to: 'removed-model' }
        ]
      },
      global: { stubs: { ModelIcon: true, Icon: true } }
    })
    await wrapper.get('[data-testid="sync-upstream-models"]').trigger('click')
    await flushPromises()
    expect(syncUpstreamModels).toHaveBeenCalledWith(46)
    expect(wrapper.emitted('update:modelValue')).toEqual([[['kiro-live']]])
    expect(wrapper.emitted('update:modelMappings')).toEqual([[[{ from: 'public-*', to: 'kiro-live' }]]])
    expect(wrapper.emitted('update:kiroUpstreamIDs')).toEqual([[{ 'kiro-live': 'provider-live' }]])
    wrapper.unmount()
  })

  it.each([
    { platform: 'kiro', kiroDirect: true, kept: true },
    { platform: 'kiro', kiroDirect: false, kept: false },
    { platform: 'openai', kiroDirect: true, kept: false },
    { platform: 'openai', kiroDirect: false, kept: false }
  ])('validates provider-ID redirects only for direct Kiro: $platform/$kiroDirect', async ({ platform, kiroDirect, kept }) => {
    syncUpstreamModels.mockResolvedValue({
      discovery_source: 'upstream',
      models: ['kiro-live'],
      metadata: { 'kiro-live': { id: 'provider-live' } }
    })
    const wrapper = mount(ModelWhitelistSelector, {
      props: {
        platform, kiroDirect, accountId: 46, modelValue: [],
        modelMappings: [{ from: 'public-*', to: 'provider-live' }]
      },
      global: { stubs: { ModelIcon: true, Icon: true } }
    })
    await wrapper.get('[data-testid="sync-upstream-models"]').trigger('click')
    await flushPromises()
    expect(wrapper.emitted('update:modelValue')).toEqual([[['kiro-live']]])
    expect(wrapper.emitted('update:modelMappings')).toEqual([[
      kept ? [{ from: 'public-*', to: 'provider-live' }] : []
    ]])
    wrapper.unmount()
  })
})
