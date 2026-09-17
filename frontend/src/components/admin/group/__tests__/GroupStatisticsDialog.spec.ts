import { enableAutoUnmount, flushPromises, mount } from '@vue/test-utils'
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'

import GroupStatisticsDialog from '../GroupStatisticsDialog.vue'
import { getStats, type GroupDetailStats } from '@/api/admin/groups'
import type { AdminGroup } from '@/types'

vi.mock('vue-i18n', () => ({
  useI18n: () => ({
    t: (key: string, params?: Record<string, unknown>) => params?.name ? `${key}:${params.name}` : key,
  }),
}))

vi.mock('@/api/admin/groups', () => ({
  getStats: vi.fn(),
}))

const getStatsMock = vi.mocked(getStats)

const group = { id: 7, name: 'Pro Group' } as AdminGroup

function createStats(overrides: Partial<GroupDetailStats> = {}): GroupDetailStats {
  return {
    group_id: 7,
    group_name: 'Pro Group',
    total_api_keys: 4,
    active_api_keys: 3,
    total_accounts: 2,
    total_requests: 19,
    total_tokens: 12345,
    total_cost: 2.34,
    total_actual_cost: 1.23,
    total_account_cost: 3.45,
    balance_cost: 0.5,
    subscription_cost: 0.75,
    zero_charge_requests: 1,
    average_duration_ms: 87.6,
    from: null,
    to: null,
    generated_at: '2026-09-17T01:02:03Z',
    ...overrides,
  }
}

function deferred<T>() {
  let resolve!: (value: T) => void
  let reject!: (reason?: unknown) => void
  const promise = new Promise<T>((res, rej) => {
    resolve = res
    reject = rej
  })
  return { promise, resolve, reject }
}

function mountDialog(props: { show: boolean; group: AdminGroup | null }) {
  return mount(GroupStatisticsDialog, {
    props,
    global: {
      stubs: {
        BaseDialog: {
          props: ['show', 'title'],
          template: '<section v-if="show"><h2>{{ title }}</h2><slot /><footer><slot name="footer" /></footer></section>',
        },
        Icon: true,
      },
    },
  })
}

enableAutoUnmount(afterEach)

describe('GroupStatisticsDialog', () => {
  beforeEach(() => {
    getStatsMock.mockReset()
  })

  it('does not request statistics while closed', () => {
    mountDialog({ show: false, group })

    expect(getStatsMock).not.toHaveBeenCalled()
  })

  it('displays the three cost totals after opening', async () => {
    getStatsMock.mockResolvedValueOnce(createStats())

    const wrapper = mountDialog({ show: true, group })
    await flushPromises()

    expect(getStatsMock).toHaveBeenCalledWith(7, { from: undefined, to: undefined }, expect.any(AbortSignal))
    expect(wrapper.text()).toContain('$1.23')
    expect(wrapper.text()).toContain('$2.34')
    expect(wrapper.text()).toContain('$3.45')
  })

  it('prevents a stale prior request from overwriting the latest group', async () => {
    const first = deferred<GroupDetailStats>()
    const second = deferred<GroupDetailStats>()
    getStatsMock.mockReturnValueOnce(first.promise).mockReturnValueOnce(second.promise)

    const wrapper = mountDialog({ show: true, group })
    await wrapper.setProps({ group: { ...group, id: 8, name: 'Fresh Group' } })

    second.resolve(createStats({ group_id: 8, group_name: 'Fresh Group', total_actual_cost: 8.88 }))
    await flushPromises()
    expect(wrapper.text()).toContain('Fresh Group')
    expect(wrapper.text()).toContain('$8.88')

    first.resolve(createStats({ group_id: 7, group_name: 'Stale Group', total_actual_cost: 7.77 }))
    await flushPromises()
    expect(wrapper.text()).toContain('Fresh Group')
    expect(wrapper.text()).toContain('$8.88')
    expect(wrapper.text()).not.toContain('Stale Group')
    expect(wrapper.text()).not.toContain('$7.77')
  })

  it('shows an invalid range error without calling the API again', async () => {
    getStatsMock.mockResolvedValueOnce(createStats())
    const wrapper = mountDialog({ show: true, group })
    await flushPromises()
    getStatsMock.mockClear()

    const inputs = wrapper.findAll('input[type="datetime-local"]')
    await inputs[0].setValue('2026-09-18T12:00')
    await inputs[1].setValue('2026-09-17T12:00')
    await wrapper.find('button').trigger('click')
    await flushPromises()

    expect(getStatsMock).not.toHaveBeenCalled()
    expect(wrapper.text()).toContain('admin.groups.stats.invalidRange')
  })
})
