import { apiClient } from '../client'

export type IntelligentTestStatus =
  | 'queued'
  | 'running'
  | 'completed'
  | 'success'
  | 'failed'
  | 'rate_limited'
  | 'account_error'
  | 'model_error'
  | 'request_error'
  | 'network_error'
  | 'suspected_degradation'
  | 'cancelled'

export interface IntelligentTestConfig {
  prompt: string
  model: string
  evaluator: 'exact_answer' | 'svg_structure' | string
  expected_answer?: string
  answer_type?: 'auto' | 'number' | 'text' | string
  answer_format?: 'answer_line' | 'free_text' | string
  timeout_seconds: number
}

export interface IntelligentTestSetting {
  test_type: string
  name?: string
  enabled: boolean
  config: IntelligentTestConfig
  updated_at?: string
}

export interface IntelligentTestEvaluation {
  evaluator?: string
  execution_status?: string
  answer_verdict?: string
  format_verdict?: string
  expected_answer?: string
  actual_answer?: string
  image_state?: string
  format_reason?: string
  limitation?: string
  [key: string]: unknown
}

export interface IntelligentTestRecord {
  id: number
  account_id: number
  test_type: string
  status: IntelligentTestStatus
  score: number | null
  result: string
  result_image: string
  raw_truncated?: boolean
  error_message?: string
  duration_ms: number
  model: string
  config_snapshot?: IntelligentTestConfig
  evaluation?: IntelligentTestEvaluation
  requested_by?: number
  available_at?: string | null
  queue_reason?: string
  started_at?: string | null
  finished_at?: string | null
  created_at: string
}

export interface IntelligentTestCardTest {
  test_type: string
  latest: IntelligentTestRecord | null
  history_count: number
}

export interface IntelligentTestAccount {
  account_id: number
  name: string
  notes?: string
  platform: string
  account_type: string
  account_status: string
  tests: IntelligentTestCardTest[]
}

export interface IntelligentTestOverview {
  total_accounts: number
  tested_today: number
  success_accounts: number
  abnormal_accounts: number
  suspected_degradation: number
}

export interface IntelligentTestPage<T> {
  items: T[]
  total: number
  page: number
  page_size: number
}

export type IntelligentTestFilters = Record<string, string | number | undefined>

export interface IntelligentTestRunRequest {
  account_ids: number[]
  test_types: string[]
  idempotency_key: string
  models?: Record<string, string>
}

export interface IntelligentTestRunResponse {
  created_count: number
  reused_count: number
  reused: boolean
  records: IntelligentTestRecord[]
}

const base = '/admin/intelligent-tests'

export function newIntelligentTestRequestKey(): string {
  return globalThis.crypto?.randomUUID?.() ?? `it-${Date.now()}-${Math.random().toString(36).slice(2)}`
}

export async function accounts(params: IntelligentTestFilters, signal?: AbortSignal): Promise<IntelligentTestPage<IntelligentTestAccount> & { overview: IntelligentTestOverview }> {
  const { data } = await apiClient.get(`${base}/accounts`, { params, signal })
  return data
}

export async function jobs(params: IntelligentTestFilters, signal?: AbortSignal): Promise<IntelligentTestPage<IntelligentTestRecord>> {
  const { data } = await apiClient.get(`${base}/jobs`, { params, signal })
  return data
}

export async function detail(id: number): Promise<IntelligentTestRecord> {
  const { data } = await apiClient.get(`${base}/jobs/${id}`)
  return data
}

export async function run(req: IntelligentTestRunRequest): Promise<IntelligentTestRunResponse> {
  const { data } = await apiClient.post(`${base}/jobs`, req)
  return data
}

export async function cancel(id: number): Promise<IntelligentTestRecord> {
  const { data } = await apiClient.post(`${base}/jobs/${id}/cancel`)
  return data
}

export async function reevaluate(id: number): Promise<IntelligentTestRecord> {
  const { data } = await apiClient.post(`${base}/jobs/${id}/reevaluate`)
  return data
}

export async function settings(): Promise<IntelligentTestSetting[]> {
  const { data } = await apiClient.get<{ items: IntelligentTestSetting[] }>(`${base}/settings`)
  return data.items ?? []
}

export async function saveSetting(setting: IntelligentTestSetting): Promise<void> {
  await apiClient.put(`${base}/settings/${encodeURIComponent(setting.test_type)}`, {
    enabled: setting.enabled,
    config: setting.config
  })
}

export async function evaluatePreview(output: string, config: IntelligentTestConfig): Promise<IntelligentTestRecord> {
  const { data } = await apiClient.post(`${base}/evaluate-preview`, { output, config })
  return data
}

export const intelligentTestsAPI = {
  accounts,
  jobs,
  detail,
  run,
  cancel,
  reevaluate,
  settings,
  saveSetting,
  evaluatePreview,
}

export default intelligentTestsAPI
