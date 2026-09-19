import { beforeEach, describe, expect, it, vi } from 'vitest'

const client = vi.hoisted(() => ({ get: vi.fn(), post: vi.fn(), put: vi.fn() }))
vi.mock('@/api/client', () => ({ apiClient: client }))

import intelligentTestsAPI from '../admin/intelligentTests'

describe('admin intelligent tests API', () => {
  beforeEach(() => Object.values(client).forEach(mock => mock.mockReset()))

  it('uses the durable admin test center routes', async () => {
    const signal = new AbortController().signal
    client.get.mockResolvedValueOnce({ data: { items: [], total: 0, page: 1, page_size: 24, overview: {} } })
    await intelligentTestsAPI.accounts({ page: 1 }, signal)
    expect(client.get).toHaveBeenCalledWith('/admin/intelligent-tests/accounts', { params: { page: 1 }, signal })

    client.get.mockResolvedValueOnce({ data: { items: [], total: 0, page: 1, page_size: 24 } })
    await intelligentTestsAPI.jobs({ status: 'queued' }, signal)
    expect(client.get).toHaveBeenCalledWith('/admin/intelligent-tests/jobs', { params: { status: 'queued' }, signal })

    client.get.mockResolvedValueOnce({ data: { id: 9 } })
    await intelligentTestsAPI.detail(9)
    expect(client.get).toHaveBeenCalledWith('/admin/intelligent-tests/jobs/9')
  })

  it('preserves idempotency keys and super-admin setting payload shape', async () => {
    client.post.mockResolvedValueOnce({ data: { records: [], created_count: 0, reused_count: 0, reused: false } })
    await intelligentTestsAPI.run({ account_ids: [1, 2], test_types: ['candy'], idempotency_key: 'request-1' })
    expect(client.post).toHaveBeenCalledWith('/admin/intelligent-tests/jobs', {
      account_ids: [1, 2],
      test_types: ['candy'],
      idempotency_key: 'request-1',
    })

    client.put.mockResolvedValueOnce({ data: { updated: true } })
    await intelligentTestsAPI.saveSetting({
      test_type: 'svg_structure',
      enabled: true,
      config: { prompt: 'draw', model: '', evaluator: 'svg_structure', timeout_seconds: 120 },
    })
    expect(client.put).toHaveBeenCalledWith('/admin/intelligent-tests/settings/svg_structure', {
      enabled: true,
      config: { prompt: 'draw', model: '', evaluator: 'svg_structure', timeout_seconds: 120 },
    })
  })

  it('routes cancel, re-evaluate, settings, and preview explicitly', async () => {
    client.post.mockResolvedValue({ data: { id: 7 } })
    await intelligentTestsAPI.cancel(7)
    expect(client.post).toHaveBeenCalledWith('/admin/intelligent-tests/jobs/7/cancel')
    await intelligentTestsAPI.reevaluate(7)
    expect(client.post).toHaveBeenCalledWith('/admin/intelligent-tests/jobs/7/reevaluate')

    client.get.mockResolvedValueOnce({ data: { items: [] } })
    await intelligentTestsAPI.settings()
    expect(client.get).toHaveBeenCalledWith('/admin/intelligent-tests/settings')

    await intelligentTestsAPI.evaluatePreview('ANSWER: 12', { prompt: 'p', model: '', evaluator: 'exact_answer', expected_answer: '12', timeout_seconds: 60 })
    expect(client.post).toHaveBeenCalledWith('/admin/intelligent-tests/evaluate-preview', {
      output: 'ANSWER: 12',
      config: { prompt: 'p', model: '', evaluator: 'exact_answer', expected_answer: '12', timeout_seconds: 60 },
    })
  })
})
