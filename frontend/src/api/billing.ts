/**
 * User billing API endpoints: monthly statements and bounded CSV exports.
 */

import { apiClient } from './client'
import type { BillingStatement } from '@/types'

function readBlobText(blob: Blob): Promise<string> {
  if (typeof blob.text === 'function') return blob.text()
  return new Promise((resolve, reject) => {
    const reader = new FileReader()
    reader.onload = () => resolve(String(reader.result ?? ''))
    reader.onerror = () => reject(reader.error ?? new Error('Failed to read response body'))
    reader.readAsText(blob)
  })
}

function isBlobPayload(value: unknown): value is Blob {
  if (!value || typeof value !== 'object') return false
  if (typeof Blob !== 'undefined' && value instanceof Blob) return true
  const candidate = value as { size?: unknown; slice?: unknown; text?: unknown }
  return (
    typeof candidate.size === 'number' &&
    (typeof candidate.slice === 'function' || typeof candidate.text === 'function')
  )
}

export async function getBillingBlobErrorMessage(value: unknown): Promise<string | null> {
  if (!isBlobPayload(value)) return null
  try {
    const payload = JSON.parse(await readBlobText(value)) as {
      message?: string
      detail?: string
    }
    return payload.message || payload.detail || null
  } catch {
    return null
  }
}

export async function getStatement(
  year: number,
  month: number,
  timezone: string,
): Promise<BillingStatement> {
  const { data } = await apiClient.get<BillingStatement>('/billing/statement', {
    params: { year, month, timezone },
  })
  return data
}

export async function exportUsageCSV(
  startDate: string,
  endDate: string,
  timezone: string,
): Promise<Blob> {
  try {
    const { data } = await apiClient.get<Blob>('/billing/export', {
      params: {
        start_date: startDate,
        end_date: endDate,
        timezone,
      },
      responseType: 'blob',
    })
    return data
  } catch (error: unknown) {
    const responseData = (error as { response?: { data?: unknown } } | null)?.response?.data
    const message = await getBillingBlobErrorMessage(responseData)
    if (message) {
      const parsedError = new Error(message)
      if (typeof error === 'object' && error) Object.assign(parsedError, error)
      parsedError.message = message
      throw parsedError
    }
    throw error
  }
}

export const billingAPI = {
  statement: getStatement,
  exportCSV: exportUsageCSV,
}

export default billingAPI
