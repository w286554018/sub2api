import { enableAutoUnmount, flushPromises, mount } from '@vue/test-utils'
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'

import OpenAIAccountRuntimeDialog from '../OpenAIAccountRuntimeDialog.vue'
import { adminAPI } from '@/api'
import type { Account, OpenAIAccountRuntimeSnapshot } from '@/types'

vi.mock('vue-i18n', () => ({
  useI18n: () => ({ t: (key: string) => key }),
}))

vi.mock('@/api', () => ({
  adminAPI: {
    accounts: {
      getRuntime: vi.fn(),
    },
  },
}))

vi.mock('@/utils/format', () => ({
  formatDateTime: (value: string) => value,
}))

const getRuntimeMock = vi.mocked(adminAPI.accounts.getRuntime)

const account = {
  id: 42,
  name: 'OpenAI OAuth',
  platform: 'openai',
  type: 'oauth',
} as Account

function createSnapshot(overrides: Partial<OpenAIAccountRuntimeSnapshot> = {}): OpenAIAccountRuntimeSnapshot {
  return {
    source: 'computed',
    observed: true,
    account_id: 42,
    platform: 'openai',
    type: 'oauth',
    auth_type: 'oauth',
    account_revision: '2026-09-17T01:02:03Z',
    configured: {
      passthrough: false,
      websocket_mode: 'auto',
      force_http: false,
      concurrency: 4,
      load_factor: 1.25,
      proxy_id: 9,
    },
    effective: {
      transport: 'websocket',
      transport_reason: 'codex default',
      core_transport: 'websocket',
      core_transport_reason: 'codex default',
      plugin_routed: true,
      plugin_mode: 'enabled',
      passthrough: false,
      fingerprint_mode: 'account-scoped',
      fingerprint_convergence: true,
      device_wire_profile: true,
      proxy_mode: 'dedicated',
      proxy_id: 9,
      concurrency: 6,
      load_factor: 1.5,
    },
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

function mountDialog(props: { show: boolean; account: Account | null }) {
  return mount(OpenAIAccountRuntimeDialog, {
    props,
    attachTo: document.body,
    global: {
      stubs: {
        Icon: true,
      },
    },
  })
}

enableAutoUnmount(afterEach)

describe('OpenAIAccountRuntimeDialog', () => {
  beforeEach(() => {
    getRuntimeMock.mockReset()
    document.body.innerHTML = ''
  })

  afterEach(() => {
    document.body.innerHTML = ''
  })

  it('loads the runtime endpoint when opened', async () => {
    getRuntimeMock.mockResolvedValueOnce(createSnapshot())

    const wrapper = mountDialog({ show: false, account })
    await wrapper.setProps({ show: true })
    await flushPromises()

    expect(getRuntimeMock).toHaveBeenCalledWith(42, { signal: expect.any(AbortSignal) })
  })

  it('displays configured and effective runtime values without secret-shaped content', async () => {
    getRuntimeMock.mockResolvedValueOnce(createSnapshot())

    const wrapper = mountDialog({ show: false, account })
    await wrapper.setProps({ show: true })
    await flushPromises()
    const text = document.body.textContent || ''

    expect(text).toContain('websocket')
    expect(text).toContain('codex default')
    expect(text).toContain('account-scoped')
    expect(text).toContain('dedicated #9')
    expect(text).toContain('auto')
    expect(text).toContain('1.25')
    expect(text).not.toMatch(/access[_-]?token|refresh[_-]?token|id[_-]?token|api[_-]?key|sk-[A-Za-z0-9]/i)
  })

  it('aborts the previous request when switching accounts', async () => {
    const first = deferred<OpenAIAccountRuntimeSnapshot>()
    const second = deferred<OpenAIAccountRuntimeSnapshot>()
    getRuntimeMock.mockReturnValueOnce(first.promise).mockReturnValueOnce(second.promise)

    const wrapper = mountDialog({ show: false, account })
    await wrapper.setProps({ show: true })
    const firstSignal = getRuntimeMock.mock.calls[0][1]?.signal

    await wrapper.setProps({ account: { ...account, id: 43, name: 'Next OAuth' } as Account })
    expect(firstSignal?.aborted).toBe(true)

    second.resolve(createSnapshot({ account_id: 43, effective: { ...createSnapshot().effective, transport: 'http' } }))
    await flushPromises()
    expect(document.body.textContent).toContain('http')
  })

  it('ignores canceled request errors without showing an error state', async () => {
    getRuntimeMock.mockRejectedValueOnce({ code: 'ERR_CANCELED' })

    const wrapper = mountDialog({ show: false, account })
    await wrapper.setProps({ show: true })
    await flushPromises()

    expect(document.body.textContent).not.toContain('admin.accounts.runtime.loadFailed')
  })

  it('shows an error state when loading fails', async () => {
    getRuntimeMock.mockRejectedValueOnce(new Error('backend unavailable'))

    const wrapper = mountDialog({ show: false, account })
    await wrapper.setProps({ show: true })
    await flushPromises()

    expect(document.body.textContent).toContain('backend unavailable')
  })
})
