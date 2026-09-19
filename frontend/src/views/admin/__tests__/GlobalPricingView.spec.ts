import { beforeEach, describe, expect, it, vi } from 'vitest'
import { defineComponent, h } from 'vue'
import { flushPromises, mount } from '@vue/test-utils'

const apiMocks = vi.hoisted(() => ({
  list: vi.fn(),
  create: vi.fn(),
  update: vi.fn(),
  remove: vi.fn(),
  setEnabled: vi.fn(),
}))

const storeMocks = vi.hoisted(() => ({
  showError: vi.fn(),
  showSuccess: vi.fn(),
}))

vi.mock('@/api/admin/globalPricing', () => ({ default: apiMocks }))
vi.mock('@/stores/app', () => ({ useAppStore: () => storeMocks }))
vi.mock('vue-i18n', async () => {
  const actual = await vi.importActual<typeof import('vue-i18n')>('vue-i18n')
  return {
    ...actual,
    useI18n: () => ({
      t: (key: string, params?: Record<string, unknown>) =>
        params ? `${key}:${JSON.stringify(params)}` : key,
    }),
  }
})

vi.mock('@/components/layout/AppLayout.vue', () => ({
  default: defineComponent({
    setup(_, { slots }) {
      return () => h('main', slots.default?.())
    },
  }),
}))

vi.mock('@/components/common/BaseDialog.vue', () => ({
  default: defineComponent({
    props: { show: Boolean, title: String },
    emits: ['close'],
    setup(props, { slots }) {
      return () => props.show
        ? h('section', { class: 'base-dialog-stub' }, [slots.default?.(), slots.footer?.()])
        : null
    },
  }),
}))

vi.mock('@/components/common/ConfirmDialog.vue', () => ({
  default: defineComponent({ setup: () => () => null }),
}))

vi.mock('@/components/common/Select.vue', () => ({
  default: defineComponent({
    props: { modelValue: String, options: Array },
    emits: ['update:modelValue'],
    setup(props, { emit }) {
      return () => h('select', {
        value: props.modelValue,
        onChange: (event: Event) => emit('update:modelValue', (event.target as HTMLSelectElement).value),
      }, (props.options as Array<{ value: string; label: string }> | undefined)?.map(option =>
        h('option', { value: option.value }, option.label),
      ))
    },
  }),
}))

vi.mock('@/components/common/Toggle.vue', () => ({
  default: defineComponent({
    props: { modelValue: Boolean },
    emits: ['update:modelValue'],
    setup(props, { emit }) {
      return () => h('button', {
        class: 'toggle-stub',
        onClick: () => emit('update:modelValue', !props.modelValue),
      })
    },
  }),
}))

vi.mock('@/components/icons/Icon.vue', () => ({
  default: defineComponent({ setup: () => () => h('i') }),
}))

import GlobalPricingView from '../GlobalPricingView.vue'

async function mountLoadedView() {
  const wrapper = mount(GlobalPricingView)
  await flushPromises()
  return wrapper
}

function findButton(wrapper: ReturnType<typeof mount>, text: string) {
  return wrapper.findAll('button').find(button => button.text() === text)
}

beforeEach(() => {
  vi.clearAllMocks()
  apiMocks.list.mockResolvedValue({ items: [], count: 0 })
  apiMocks.create.mockResolvedValue({ id: 1, enabled: true })
  apiMocks.update.mockResolvedValue({ id: 1, enabled: false })
  apiMocks.setEnabled.mockResolvedValue({ id: 1, enabled: true })
})

describe('GlobalPricingView', () => {
  it('converts MTok token prices and leaves enabled to the create contract', async () => {
    const wrapper = await mountLoadedView()
    await findButton(wrapper, 'admin.globalPricing.addPricing')!.trigger('click')

    const dialog = wrapper.find('.base-dialog-stub')
    const inputs = dialog.findAll('input')
    await inputs[0].setValue('gpt-5*')
    await inputs[1].setValue('1.5')
    await inputs[2].setValue('2.5')
    await findButton(wrapper, 'common.save')!.trigger('click')
    await flushPromises()

    expect(apiMocks.create).toHaveBeenCalledWith({
      model_pattern: 'gpt-5*',
      billing_mode: 'token',
      input_price: 0.0000015,
      output_price: 0.0000025,
      cache_write_price: null,
      cache_write_1h_price: null,
      cache_read_price: null,
      per_request_price: null,
    })
  })

  it('submits only per-request pricing for image mode', async () => {
    const wrapper = await mountLoadedView()
    await findButton(wrapper, 'admin.globalPricing.addPricing')!.trigger('click')

    const dialog = wrapper.find('.base-dialog-stub')
    await dialog.find('input[type="text"]').setValue('gpt-image-2')
    await dialog.find('select').setValue('image')
    await dialog.find('input[type="number"]').setValue('0.04')
    await findButton(wrapper, 'common.save')!.trigger('click')
    await flushPromises()

    expect(apiMocks.create).toHaveBeenCalledWith({
      model_pattern: 'gpt-image-2',
      billing_mode: 'image',
      input_price: null,
      output_price: null,
      cache_write_price: null,
      cache_write_1h_price: null,
      cache_read_price: null,
      per_request_price: 0.04,
    })
  })

  it('uses the independent enable endpoint for a disabled row', async () => {
    apiMocks.list.mockResolvedValue({
      items: [{
        id: 7,
        model_pattern: 'gpt-5',
        billing_mode: 'token',
        input_price: 0.000001,
        output_price: 0.000002,
        enabled: false,
      }],
      count: 1,
    })
    apiMocks.setEnabled.mockResolvedValue({ id: 7, model_pattern: 'gpt-5', billing_mode: 'token', enabled: true })

    const wrapper = await mountLoadedView()
    await wrapper.find('.toggle-stub').trigger('click')
    await flushPromises()

    expect(apiMocks.setEnabled).toHaveBeenCalledWith(7, true)
    expect(apiMocks.update).not.toHaveBeenCalled()
  })
})
