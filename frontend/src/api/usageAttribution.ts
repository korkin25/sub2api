import { apiClient } from './client'

export type AttributionDimension = 'project' | 'task' | 'client' | 'host' | 'api_key' | 'model' | 'user'

export interface AttributionFilters {
  start_date: string
  end_date: string
  group_by: AttributionDimension
  user_id?: number
  api_key_id?: number
  model?: string
  project_id?: string
  task_id?: string
  client_kind?: string
  host?: string
  unknown?: boolean
}

export interface AttributionMeasures {
  requests: number
  input_tokens: number
  output_tokens: number
  cache_creation_tokens: number
  cache_read_tokens: number
  total_tokens: number
  /** Decimal USD strings; never sum or round these in the browser. */
  total_cost: string
  actual_cost: string
  rpm: number
  tpm: number
}

export interface AttributionGroup extends AttributionMeasures {
  id: string | null
  name: string | null
  user_id?: number | null
}

export interface AttributionReport {
  groups: AttributionGroup[]
  totals: AttributionMeasures
  series: (AttributionMeasures & { date: string })[]
  window_start: string
  window_end: string
  window_minutes: number
}

export type AttributionOption = Pick<AttributionGroup, 'id' | 'name' | 'user_id'>
export interface AttributionOptions {
  projects: AttributionOption[]
  tasks: AttributionOption[]
  clients: AttributionOption[]
  hosts: AttributionOption[]
  users: AttributionOption[]
  truncated: boolean
}

export interface AttributionNameUpdate {
  kind: 'project' | 'task'
  id: string
  name: string
  user_id?: number
}

const base = (admin: boolean) => `${admin ? '/admin' : ''}/usage/attribution`

export async function getAttribution(admin: boolean, params: AttributionFilters, signal?: AbortSignal): Promise<AttributionReport> {
  const { data } = await apiClient.get<AttributionReport>(base(admin), { params, signal })
  return data
}

export async function renameAttribution(admin: boolean, payload: AttributionNameUpdate): Promise<void> {
  await apiClient.patch(`${base(admin)}/names`, payload)
}

export async function exportAttribution(admin: boolean, params: AttributionFilters): Promise<Blob> {
  const { data } = await apiClient.get<Blob>(`${base(admin)}/export`, { params, responseType: 'blob' })
  return data
}

export async function getAttributionOptions(admin: boolean, params: Omit<AttributionFilters, 'group_by'>, signal?: AbortSignal): Promise<AttributionOptions> {
  const { data } = await apiClient.get<AttributionOptions>(`${base(admin)}/options`, { params, signal })
  return data
}
