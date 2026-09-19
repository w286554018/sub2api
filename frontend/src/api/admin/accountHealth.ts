import { apiClient } from '../client'

export type AccountHealthState = 'healthy' | 'degraded' | 'isolated'

export interface AccountHealthOverview {
  healthy: number
  degraded: number
  isolated: number
  no_samples: number
  total_accounts: number
}

export interface AccountHealthItem {
  account_id: number
  name: string
  platform: string
  account_status: string
  success_count: number
  error_count: number
  total_count: number
  error_rate: number
  avg_latency_ms: number | null
  score: number
  state: AccountHealthState
  has_enough_samples: boolean
  temporarily_unschedulable: boolean
  unschedulable_reason?: string | null
  unschedulable_until?: string | null
  evaluated_at: string
}

export interface AccountHealthListParams {
  page?: number
  page_size?: number
  search?: string
  platform?: string
  state?: AccountHealthState | ''
}

export interface AccountHealthListResponse {
  items: AccountHealthItem[]
  total: number
  page: number
  page_size: number
  evaluated_at: string
  window_minutes: number
  overview: AccountHealthOverview
}

export interface AccountHealthSettings {
  enabled: boolean
  window_minutes: number
  min_samples: number
  isolate_error_rate: number
  recover_error_rate: number
  cooldown_minutes: number
  interval_seconds: number
}

export type AccountHealthSettingsPayload = AccountHealthSettings

export interface AccountHealthIsolationPayload {
  duration_minutes: number
  reason: string
}

const base = '/admin/account-health'

export async function list(
  params: AccountHealthListParams = {},
  options?: { signal?: AbortSignal }
): Promise<AccountHealthListResponse> {
  const { data } = await apiClient.get<AccountHealthListResponse>(base, {
    params,
    signal: options?.signal,
  })
  return data
}

export async function getSettings(options?: { signal?: AbortSignal }): Promise<AccountHealthSettings> {
  const { data } = await apiClient.get<AccountHealthSettings>(`${base}/settings`, {
    signal: options?.signal,
  })
  return data
}

export async function updateSettings(payload: AccountHealthSettingsPayload): Promise<AccountHealthSettings> {
  const { data } = await apiClient.put<AccountHealthSettings>(`${base}/settings`, payload)
  return data
}

export async function isolateAccount(accountId: number, payload: AccountHealthIsolationPayload): Promise<void> {
  await apiClient.post(`${base}/${accountId}/isolate`, payload)
}

export async function clearIsolation(accountId: number): Promise<void> {
  await apiClient.delete(`${base}/${accountId}/isolation`)
}

export const accountHealthAPI = {
  list,
  getSettings,
  updateSettings,
  isolateAccount,
  clearIsolation,
}

export default accountHealthAPI
