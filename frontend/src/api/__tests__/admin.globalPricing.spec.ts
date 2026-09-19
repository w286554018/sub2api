import { beforeEach, describe, expect, it, vi } from 'vitest'

const client = vi.hoisted(() => ({ get: vi.fn(), post: vi.fn(), put: vi.fn(), delete: vi.fn() }))
vi.mock('@/api/client', () => ({ apiClient: client }))

import globalPricingAPI from '../admin/globalPricing'

describe('admin global pricing API', () => {
  beforeEach(() => Object.values(client).forEach(mock => mock.mockReset()))

  it('uses the admin global-pricing route namespace', async () => {
    client.get.mockResolvedValue({ data: { items: [], count: 0 } })
    await globalPricingAPI.list()
    expect(client.get).toHaveBeenCalledWith('/admin/global-pricing')

    client.delete.mockResolvedValue({ data: { message: 'deleted' } })
    await globalPricingAPI.remove(7)
    expect(client.delete).toHaveBeenCalledWith('/admin/global-pricing/7')
  })

  it('sends create, update, and enable payloads without client-side shape drift', async () => {
    const tokenPayload = {
      model_pattern: 'gpt-5*',
      billing_mode: 'token' as const,
      input_price: 0.000001,
      output_price: 0.000002,
      cache_write_price: null,
      cache_write_1h_price: null,
      cache_read_price: null,
      per_request_price: null
    }
    client.post.mockResolvedValue({ data: { id: 1, ...tokenPayload } })
    await globalPricingAPI.create(tokenPayload)
    expect(client.post).toHaveBeenCalledWith('/admin/global-pricing', tokenPayload)

    const requestPayload = {
      model_pattern: 'gpt-image-2',
      billing_mode: 'image' as const,
      input_price: null,
      output_price: null,
      cache_write_price: null,
      cache_write_1h_price: null,
      cache_read_price: null,
      per_request_price: 0.04
    }
    client.put.mockResolvedValue({ data: { id: 2, enabled: false, ...requestPayload } })
    await globalPricingAPI.update(2, requestPayload)
    expect(client.put).toHaveBeenCalledWith('/admin/global-pricing/2', requestPayload)

    client.post.mockResolvedValue({ data: { id: 2, enabled: true, ...requestPayload } })
    await globalPricingAPI.setEnabled(2, true)
    expect(client.post).toHaveBeenCalledWith('/admin/global-pricing/2/enable', { enabled: true })
  })
})
