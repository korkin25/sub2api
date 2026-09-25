import { flushPromises, mount } from '@vue/test-utils'
import { beforeEach, describe, expect, it, vi } from 'vitest'
const { getAttribution, getAttributionOptions, renameAttribution, exportAttribution, saveAs } = vi.hoisted(() => ({ getAttribution: vi.fn(), getAttributionOptions: vi.fn(), renameAttribution: vi.fn(), exportAttribution: vi.fn(), saveAs: vi.fn() }))
vi.mock('@/api/usageAttribution', () => ({ getAttribution, getAttributionOptions, renameAttribution, exportAttribution }))
vi.mock('file-saver', () => ({ saveAs }))
vi.mock('vue-chartjs', () => ({ Bar: { template: '<div />' }, Line: { template: '<div />' } }))
vi.mock('vue-i18n', async () => {
  const { default: en } = await import('@/i18n/locales/en/attribution')
  return { useI18n: () => ({ t: (key: string, values: Record<string, unknown> = {}) => {
    const message = en.attribution[key.replace('attribution.', '') as keyof typeof en.attribution] || key
    return message.replace(/\{(\w+)\}/g, (_match, name: string) => String(values[name] ?? ''))
  } }) }
})
import UsageAttributionPanel from '../UsageAttributionPanel.vue'
const totals = { requests: 3, input_tokens: 100, output_tokens: 20, cache_creation_tokens: 30, cache_read_tokens: 40, total_tokens: 190, total_cost: '0.001234567890', actual_cost: '0.000012345678', rpm: 0.002, tpm: 0.13 }
const report = { groups: [{ ...totals, id: 'task-stable', name: 'Initial task', user_id: 42 }, { ...totals, id: null, name: null, user_id: 42 }], totals, series: [{ ...totals, date: '2026-09-25' }], window_start: '2026-09-25T00:00:00Z', window_end: '2026-09-26T00:00:00Z', window_minutes: 1440 }
const mountPanel = (admin = false) => mount(UsageAttributionPanel, { props: { admin, startDate: '2026-09-25', endDate: '2026-09-25', apiKeyId: 8, model: 'gpt-5' }, global: { stubs: { Bar: true, Line: true, Select: true } } })
describe('UsageAttributionPanel', () => {
  beforeEach(() => { vi.clearAllMocks(); getAttribution.mockResolvedValue(report); getAttributionOptions.mockResolvedValue({ projects: report.groups, tasks: report.groups, clients: report.groups, hosts: report.groups, users: [], truncated: false }); renameAttribution.mockResolvedValue(undefined); exportAttribution.mockResolvedValue(new Blob(['csv'])) })
  it('shows exact costs, Unknown, detailed tokens and full-period rate semantics', async () => {
    const wrapper = mountPanel()
    await flushPromises()
    expect(wrapper.text()).toContain('Unknown')
    expect(wrapper.text()).toContain('$0.000012345678')
    expect(wrapper.text()).toContain('Cache creation tokens')
    expect(wrapper.text()).toContain('entire selected window (1,440 minutes)')
    expect(getAttribution).toHaveBeenCalledWith(false, expect.objectContaining({ group_by: 'task', api_key_id: 8, model: 'gpt-5' }), expect.any(AbortSignal))
    wrapper.unmount()
  })
  it('renames the exact owner and stable task ID then refreshes totals', async () => {
    const wrapper = mountPanel(true)
    await flushPromises()
    await wrapper.get('[aria-label="Rename Initial task"]').trigger('click')
    await wrapper.get('#attribution-name').setValue('Renamed task')
    await wrapper.get('form').trigger('submit')
    await flushPromises()
    expect(renameAttribution).toHaveBeenCalledWith(true, { kind: 'task', id: 'task-stable', name: 'Renamed task', user_id: 42 })
    expect(wrapper.find('form').exists()).toBe(false)
    wrapper.unmount()
  })
  it('exports the selected unknown scope without leaking user-specific filters', async () => {
    const wrapper = mountPanel()
    await flushPromises()
    await wrapper.get('[data-testid="attribution-unknown"]').setValue(true)
    await flushPromises()
    const button = wrapper.findAll('button').find(button => button.text() === 'Export report CSV')!
    await button.trigger('click'); await flushPromises()
    expect(exportAttribution).toHaveBeenCalledWith(false, expect.objectContaining({ unknown: true, api_key_id: 8, group_by: 'task' }))
    expect(saveAs).toHaveBeenCalled()
    wrapper.unmount()
  })
  it('drops stale data and prevents export after a changed-period load fails', async () => {
    const wrapper = mountPanel(); await flushPromises()
    getAttribution.mockRejectedValue(new Error('offline'))
    await wrapper.setProps({ endDate: '2026-09-26' }); await flushPromises()
    expect(wrapper.text()).toContain('Could not load the attribution report')
    expect(wrapper.find('[data-testid="attribution-table"]').exists()).toBe(false)
    expect(wrapper.findAll('button').find(button => button.text() === 'Export report CSV')!.attributes('disabled')).toBeDefined()
    wrapper.unmount()
  })
  it('keeps duplicate project IDs in different owners separate and scopes task options', async () => {
    getAttributionOptions.mockResolvedValue({ projects: [
      { id: 'same-project', name: 'Project A', user_id: 42 },
      { id: 'same-project', name: 'Project B', user_id: 43 },
    ], tasks: [], clients: [], hosts: [], users: [], truncated: false })
    const wrapper = mountPanel(true); await flushPromises()
    const project = wrapper.findAllComponents({ name: 'Select' }).find(component => component.attributes('id') === 'attribution-project')!
    const options = project.props('options') as { value: string; label: string }[]
    expect(options).toHaveLength(3)
    expect(options[1].value).not.toBe(options[2].value)
    project.vm.$emit('update:modelValue', options[2].value)
    await flushPromises()
    expect(getAttribution).toHaveBeenCalledWith(true, expect.objectContaining({ group_by: 'task', project_id: 'same-project', user_id: 43 }), expect.any(AbortSignal))
    wrapper.unmount()
  })
  it('allows retrying a failed CSV export without reloading the report', async () => {
    const wrapper = mountPanel(); await flushPromises()
    exportAttribution.mockRejectedValueOnce(new Error('offline'))
    const button = wrapper.findAll('button').find(button => button.text() === 'Export report CSV')!
    await button.trigger('click'); await flushPromises()
    expect(button.attributes('disabled')).toBeUndefined()
    await button.trigger('click'); await flushPromises()
    expect(exportAttribution).toHaveBeenCalledTimes(2)
    wrapper.unmount()
  })

})
