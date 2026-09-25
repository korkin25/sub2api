import { beforeEach, describe, expect, it, vi } from 'vitest'
const { get, patch } = vi.hoisted(() => ({ get: vi.fn(), patch: vi.fn() }))
vi.mock('@/api/client', () => ({ apiClient: { get, patch } }))
import { exportAttribution, getAttribution, renameAttribution } from '../usageAttribution'
import { attributionCost } from '@/utils/usageAttribution'

describe('attribution API', () => {
  beforeEach(() => { vi.clearAllMocks(); get.mockResolvedValue({ data: {} }); patch.mockResolvedValue({}) })
  const params = { start_date: '2026-09-01', end_date: '2026-09-25', group_by: 'task' as const, project_id: 'project-1', task_id: 'stable-task', model: 'gpt-5', api_key_id: 8 }
  it('uses the same scope for reports and CSV with separate admin endpoints', async () => {
    await getAttribution(false, params)
    expect(get).toHaveBeenLastCalledWith('/usage/attribution', { params, signal: undefined })
    await exportAttribution(true, { ...params, user_id: 42 })
    expect(get).toHaveBeenLastCalledWith('/admin/usage/attribution/export', { params: { ...params, user_id: 42 }, responseType: 'blob' })
  })
  it('renames by stable ID and owner without changing grouping identity', async () => {
    const update = { kind: 'task' as const, id: 'stable-task', name: 'Readable task', user_id: 42 }
    await renameAttribution(true, update)
    expect(patch).toHaveBeenCalledWith('/admin/usage/attribution/names', update)
  })
  it('preserves exact decimal reference and deduction costs', () => {
    expect(attributionCost('0.000000012345678900')).toBe('$0.0000000123456789')
    expect(attributionCost('12345678901234567890.12000')).toBe('$12345678901234567890.12')
    expect(attributionCost('0')).toBe('$0.00')
    expect(attributionCost('not-a-cost')).toBe('—')
  })
})
