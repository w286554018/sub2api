import { beforeEach, describe, expect, it, vi } from 'vitest'

const client = vi.hoisted(() => ({ get: vi.fn() }))
vi.mock('@/api/client', () => ({ apiClient: client }))

import billingAPI, { getBillingBlobErrorMessage } from '../billing'

describe('billing API', () => {
  beforeEach(() => client.get.mockReset())

  it('requests a monthly statement with the selected timezone', async () => {
    client.get.mockResolvedValue({ data: { rows: [], requests: 0, total_tokens: 0, cost: 0 } })

    await billingAPI.statement(2026, 9, 'Asia/Shanghai')

    expect(client.get).toHaveBeenCalledWith('/billing/statement', {
      params: { year: 2026, month: 9, timezone: 'Asia/Shanghai' }
    })
  })

  it('downloads a bounded server-side CSV export', async () => {
    const blob = new Blob(['created_at,model'])
    client.get.mockResolvedValue({ data: blob })

    await expect(
      billingAPI.exportCSV('2026-09-01', '2026-09-17', 'Asia/Shanghai')
    ).resolves.toBe(blob)

    expect(client.get).toHaveBeenCalledWith('/billing/export', {
      params: {
        start_date: '2026-09-01',
        end_date: '2026-09-17',
        timezone: 'Asia/Shanghai'
      },
      responseType: 'blob'
    })
  })

  it('extracts an API message returned as a JSON blob', async () => {
    const data = {
      size: 29,
      slice: vi.fn(),
      text: vi.fn().mockResolvedValue(JSON.stringify({ message: 'range too large' }))
    }

    await expect(getBillingBlobErrorMessage(data)).resolves.toBe('range too large')
    expect(data.text).toHaveBeenCalledOnce()
  })
})
