import { beforeEach, describe, expect, it, vi } from 'vitest'

const client = vi.hoisted(() => ({ get: vi.fn(), put: vi.fn(), post: vi.fn(), delete: vi.fn() }))
vi.mock('@/api/client', () => ({ apiClient: client }))

import accountHealthAPI from '../admin/accountHealth'

describe('admin account health API', () => {
  beforeEach(() => Object.values(client).forEach(mock => mock.mockReset()))

  it('lists account health with filters and abort signal', async () => {
    const signal = new AbortController().signal
    client.get.mockResolvedValueOnce({ data: { items: [], total: 0, page: 1, page_size: 20, evaluated_at: '2026-09-18T00:00:00Z', window_minutes: 60, overview: {} } })

    await accountHealthAPI.list({ page: 2, page_size: 50, search: 'main', platform: 'openai', state: 'degraded' }, { signal })

    expect(client.get).toHaveBeenCalledWith('/admin/account-health', {
      params: { page: 2, page_size: 50, search: 'main', platform: 'openai', state: 'degraded' },
      signal,
    })
  })

  it('loads and updates settings on the dedicated settings route', async () => {
    const settings = { enabled: true, window_minutes: 60, min_samples: 5, isolate_error_rate: 0.5, recover_error_rate: 0.1, cooldown_minutes: 10, interval_seconds: 30 }
    const signal = new AbortController().signal
    client.get.mockResolvedValueOnce({ data: settings })
    client.put.mockResolvedValueOnce({ data: { ...settings, enabled: false } })

    await accountHealthAPI.getSettings({ signal })
    expect(client.get).toHaveBeenCalledWith('/admin/account-health/settings', { signal })

    await accountHealthAPI.updateSettings({ ...settings, enabled: false })
    expect(client.put).toHaveBeenCalledWith('/admin/account-health/settings', { ...settings, enabled: false })
  })

  it('isolates and resumes an account through ownership-aware routes', async () => {
    client.post.mockResolvedValueOnce({ data: undefined })
    client.delete.mockResolvedValueOnce({ data: undefined })

    await accountHealthAPI.isolateAccount(12, { duration_minutes: 30, reason: 'investigation' })
    expect(client.post).toHaveBeenCalledWith('/admin/account-health/12/isolate', {
      duration_minutes: 30,
      reason: 'investigation',
    })

    await accountHealthAPI.clearIsolation(12)
    expect(client.delete).toHaveBeenCalledWith('/admin/account-health/12/isolation')
  })
})
