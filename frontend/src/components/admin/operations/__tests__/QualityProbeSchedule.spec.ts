import { afterEach, describe, expect, it, vi } from 'vitest'
import { defineComponent, ref } from 'vue'
import { enableAutoUnmount, mount } from '@vue/test-utils'
import QualityProbeSchedule from '../QualityProbeSchedule.vue'
import { chooseSelectOption, selectModelValue } from '@/__tests__/helpers/select'

vi.mock('vue-i18n', () => ({ useI18n: () => ({ t: (key: string) => key }) }))

enableAutoUnmount(afterEach)

function mountSchedule(initial = '*/2 * * * *') {
  return mount(defineComponent({
    components: { QualityProbeSchedule },
    setup: () => ({ cron: ref(initial) }),
    template: '<QualityProbeSchedule v-model="cron" />',
  }))
}

describe('QualityProbeSchedule', () => {
  it('shows the two-minute preset and updates the schedule on selection', async () => {
    const wrapper = mountSchedule()
    expect(selectModelValue(wrapper, '[data-testid="quality-probe-interval"]')).toBe('*/2 * * * *')
    expect(wrapper.find('[data-testid="quality-probe-cron"]').exists()).toBe(false)
    await chooseSelectOption(wrapper, '[data-testid="quality-probe-interval"]', '0 */2 * * *')
    expect(wrapper.vm.cron).toBe('0 */2 * * *')
    await chooseSelectOption(wrapper, '[data-testid="quality-probe-interval"]', '0 0 * * *')
    expect(wrapper.vm.cron).toBe('0 0 * * *')
  })

  it('reads custom schedules without changing them and supports editing or returning to a preset', async () => {
    const wrapper = mountSchedule('13 9 * * 1-5')
    expect(wrapper.vm.cron).toBe('13 9 * * 1-5')
    expect(selectModelValue(wrapper, '[data-testid="quality-probe-interval"]')).toBe('custom')
    const cron = wrapper.get<HTMLInputElement>('[data-testid="quality-probe-cron"]')
    expect(cron.element.value).toBe('13 9 * * 1-5')
    await cron.setValue(' 7 8 * * 1 ')
    expect(wrapper.vm.cron).toBe('7 8 * * 1')
    await chooseSelectOption(wrapper, '[data-testid="quality-probe-interval"]', '*/5 * * * *')
    expect(wrapper.vm.cron).toBe('*/5 * * * *')
    expect(wrapper.find('[data-testid="quality-probe-cron"]').exists()).toBe(false)
  })

  it('keeps the existing expression when entering custom mode', async () => {
    const wrapper = mountSchedule('*/30 * * * *')
    await chooseSelectOption(wrapper, '[data-testid="quality-probe-interval"]', 'custom')
    expect(wrapper.vm.cron).toBe('*/30 * * * *')
    await wrapper.get('[data-testid="quality-probe-cron"]').setValue('*/2 * * * *')
    expect(selectModelValue(wrapper, '[data-testid="quality-probe-interval"]')).toBe('custom')
    expect(wrapper.find('[data-testid="quality-probe-cron"]').exists()).toBe(true)
  })
})
