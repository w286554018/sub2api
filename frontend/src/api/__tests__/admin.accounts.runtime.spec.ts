import { beforeEach, describe, expect, it, vi } from 'vitest'

const { get } = vi.hoisted(() => ({ get: vi.fn() }))

vi.mock('@/api/client', () => ({
  apiClient: { get }
}))

import { getRuntime } from '@/api/admin/accounts'

describe('admin account runtime API', () => {
  beforeEach(() => {
    get.mockReset()
    get.mockResolvedValue({ data: { account_id: 42, source: 'computed' } })
  })

  it('loads the read-only runtime snapshot endpoint', async () => {
    const controller = new AbortController()
    await expect(getRuntime(42, { signal: controller.signal })).resolves.toEqual({ account_id: 42, source: 'computed' })
    expect(get).toHaveBeenCalledWith('/admin/accounts/42/runtime', { signal: controller.signal })
  })
})
