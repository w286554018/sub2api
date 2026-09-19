import { apiClient } from '../client'
import type { BillingMode } from '@/constants/channel'

export interface GlobalModelPrice {
  id: number
  model_pattern: string
  billing_mode: BillingMode
  input_price?: number | null
  output_price?: number | null
  cache_write_price?: number | null
  cache_write_1h_price?: number | null
  cache_read_price?: number | null
  per_request_price?: number | null
  enabled: boolean
  created_at?: string
  updated_at?: string
}

export interface GlobalModelPriceInput {
  model_pattern: string
  billing_mode: BillingMode
  input_price?: number | null
  output_price?: number | null
  cache_write_price?: number | null
  cache_write_1h_price?: number | null
  cache_read_price?: number | null
  per_request_price?: number | null
}

export interface GlobalModelPricingListResponse {
  items: GlobalModelPrice[]
  count: number
}

export async function listGlobalPricing(): Promise<GlobalModelPricingListResponse> {
  const { data } = await apiClient.get<GlobalModelPricingListResponse>('/admin/global-pricing')
  return data
}

export async function createGlobalPricing(input: GlobalModelPriceInput): Promise<GlobalModelPrice> {
  const { data } = await apiClient.post<GlobalModelPrice>('/admin/global-pricing', input)
  return data
}

export async function updateGlobalPricing(id: number, input: GlobalModelPriceInput): Promise<GlobalModelPrice> {
  const { data } = await apiClient.put<GlobalModelPrice>(`/admin/global-pricing/${id}`, input)
  return data
}

export async function deleteGlobalPricing(id: number): Promise<{ message: string }> {
  const { data } = await apiClient.delete<{ message: string }>(`/admin/global-pricing/${id}`)
  return data
}

export async function setGlobalPricingEnabled(id: number, enabled: boolean): Promise<GlobalModelPrice> {
  const { data } = await apiClient.post<GlobalModelPrice>(`/admin/global-pricing/${id}/enable`, { enabled })
  return data
}

export const globalPricingAPI = {
  list: listGlobalPricing,
  create: createGlobalPricing,
  update: updateGlobalPricing,
  remove: deleteGlobalPricing,
  setEnabled: setGlobalPricingEnabled
}

export default globalPricingAPI
