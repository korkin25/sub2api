<template>
  <section class="card space-y-4 p-4" aria-labelledby="attribution-title" data-testid="attribution-panel">
    <h2 id="attribution-title" class="text-lg font-semibold text-gray-900 dark:text-white">{{ t('attribution.title') }}</h2>
    <p class="text-sm text-gray-500 dark:text-gray-400">{{ t('attribution.scope') }}</p>
    <div class="flex flex-wrap items-end gap-3">
      <div class="w-full sm:w-44">
        <label for="attribution-group" class="input-label">{{ t('attribution.groupBy') }}</label>
        <Select id="attribution-group" v-model="groupBy" :options="dimensionOptions" />
      </div>
      <div v-for="dimension in filterDimensions" :key="dimension" class="w-full sm:w-44">
        <label :for="`attribution-${dimension}`" class="input-label">{{ t(`attribution.${dimension}`) }}</label>
        <Select :id="`attribution-${dimension}`" v-model="selected[dimension]" :options="filterOptions(dimension)" searchable :disabled="unknownOnly || optionsLoading" />
      </div>
      <label class="flex items-center gap-2 text-sm">
        <input v-model="unknownOnly" type="checkbox" data-testid="attribution-unknown" />
        {{ t('attribution.unknownOnly') }}
      </label>
      <button class="btn btn-secondary" :disabled="loading" @click="refresh">{{ t('attribution.refresh') }}</button>
      <button class="btn btn-primary" :disabled="loading || exporting || !report" @click="download">{{ t('attribution.export') }}</button>
    </div>
    <p class="text-xs text-gray-500 dark:text-gray-400">{{ t('attribution.unknownHint') }}</p>
    <p v-if="optionsTruncated" role="status" class="text-sm text-gray-500">{{ t('attribution.optionsTruncated') }}</p>
    <p v-if="optionsError" role="alert" class="text-sm text-red-600">{{ t('attribution.optionsFailed') }}</p>
    <p v-if="error" role="alert" class="text-sm text-red-600">{{ error }}</p>
    <p v-if="loading" role="status">{{ t('attribution.loading') }}</p>
    <template v-else-if="report">
      <p class="text-xs text-gray-500">{{ t('attribution.window', { start: report.window_start, end: report.window_end }) }}</p>
      <h3 class="font-medium">{{ t('attribution.totals') }}</h3>
      <dl class="grid grid-cols-2 gap-3 md:grid-cols-4 xl:grid-cols-6" data-testid="attribution-totals">
        <div v-for="metric in metrics" :key="metric.key" class="rounded-lg bg-gray-50 p-3 dark:bg-dark-800">
          <dt class="text-xs text-gray-500">{{ t(`attribution.${metric.label}`) }}</dt>
          <dd class="mt-1 font-semibold tabular-nums">{{ metricValue(report.totals, metric.key) }}</dd>
        </div>
      </dl>
      <p class="text-xs text-gray-500">{{ t('attribution.costHint') }}</p>
      <p class="text-xs text-gray-500">{{ t('attribution.rateHint', { minutes: report.window_minutes.toLocaleString() }) }}</p>
      <div v-if="report.groups.length" class="grid min-w-0 grid-cols-1 gap-6 lg:grid-cols-2">
        <div class="min-w-0">
          <h3 class="mb-2 text-sm font-medium">{{ t('attribution.distribution') }}</h3>
          <div class="relative h-64 min-w-0"><Bar :data="distributionData" :options="barOptions" /></div>
        </div>
        <div class="min-w-0">
          <h3 class="mb-2 text-sm font-medium">{{ t('attribution.trend') }}</h3>
          <div class="relative h-64 min-w-0"><Line :data="trendData" :options="lineOptions" /></div>
        </div>
      </div>
      <form v-if="editing" class="flex flex-wrap items-end gap-3 rounded-lg border p-3 dark:border-dark-600" @submit.prevent="saveName">
        <div class="min-w-64 flex-1">
          <label for="attribution-name" class="input-label">{{ t('attribution.name') }}</label>
          <input id="attribution-name" v-model="editedName" class="input" maxlength="300" required :disabled="saving" />
          <p class="mt-1 text-xs text-gray-500">{{ t('attribution.renameHint') }}</p>
        </div>
        <button class="btn btn-primary" type="submit" :disabled="saving || !editedName.trim()">{{ t('attribution.save') }}</button>
        <button class="btn btn-secondary" type="button" :disabled="saving" @click="editing = null">{{ t('attribution.cancel') }}</button>
      </form>
      <div class="overflow-x-auto">
        <table class="w-full text-left text-sm" data-testid="attribution-table">
          <thead><tr class="border-b dark:border-dark-600">
            <th class="p-2">{{ t(`attribution.${groupBy}`) }}</th>
            <th v-if="admin" class="p-2">{{ t('attribution.user') }}</th>
            <th v-for="metric in metrics" :key="metric.key" class="whitespace-nowrap p-2 text-right">{{ t(`attribution.${metric.label}`) }}</th>
          </tr></thead>
          <tbody>
            <tr v-for="row in report.groups" :key="`${row.user_id}:${row.id}`" class="border-b dark:border-dark-700">
              <td class="p-2">
                <span :title="row.id || undefined">{{ groupName(row) }}</span>
                <button v-if="canRename(row)" class="ml-2 text-primary-600" :disabled="saving" :aria-label="`${t('attribution.rename')} ${groupName(row)}`" @click="beginRename(row)">{{ t('attribution.rename') }}</button>
              </td>
              <td v-if="admin" class="p-2">{{ ownerName(row.user_id) }}</td>
              <td v-for="metric in metrics" :key="metric.key" class="whitespace-nowrap p-2 text-right tabular-nums">{{ metricValue(row, metric.key) }}</td>
            </tr>
          </tbody>
        </table>
        <p v-if="!report.groups.length" class="py-6 text-center text-gray-500">{{ t('attribution.empty') }}</p>
      </div>
    </template>
  </section>
</template>

<script setup lang="ts">
import { computed, onBeforeUnmount, reactive, ref, watch } from 'vue'
import { useI18n } from 'vue-i18n'
import { Bar, Line } from 'vue-chartjs'
import { Chart as ChartJS, CategoryScale, LinearScale, BarElement, PointElement, LineElement, Tooltip, Legend, type ChartOptions } from 'chart.js'
import { saveAs } from 'file-saver'
import Select from '@/components/common/Select.vue'
import { getAttribution, getAttributionOptions, renameAttribution, exportAttribution, type AttributionOption, type AttributionDimension, type AttributionFilters, type AttributionGroup, type AttributionMeasures, type AttributionReport } from '@/api/usageAttribution'
import { attributionCost } from '@/utils/usageAttribution'

ChartJS.register(CategoryScale, LinearScale, BarElement, PointElement, LineElement, Tooltip, Legend)
const props = withDefaults(defineProps<{ admin?: boolean; startDate: string; endDate: string; userId?: number; apiKeyId?: number; model?: string }>(), { admin: false })
const { t } = useI18n()
const groupBy = ref<AttributionDimension>('task')
const filterDimensions = ['project', 'task', 'client', 'host'] as const
type FilterDimension = typeof filterDimensions[number]
const selected = reactive<Record<FilterDimension, string>>({ project: '', task: '', client: '', host: '' })
const available = reactive<Record<FilterDimension, AttributionOption[]>>({ project: [], task: [], client: [], host: [] })
const ownerNames = ref<Record<string, string>>({})
const unknownOnly = ref(false)
const report = ref<AttributionReport | null>(null)
const loading = ref(false)
const optionsLoading = ref(false)
const optionsError = ref(false)
const optionsTruncated = ref(false)
const exporting = ref(false)
const saving = ref(false)
const error = ref('')
const editing = ref<{ kind: 'project' | 'task'; row: AttributionGroup } | null>(null)
const editedName = ref('')
let reportController: AbortController | undefined
let optionsController: AbortController | undefined
const metrics: { key: keyof AttributionMeasures; label: string }[] = [
  { key: 'requests', label: 'requests' }, { key: 'input_tokens', label: 'input' },
  { key: 'output_tokens', label: 'output' }, { key: 'cache_creation_tokens', label: 'cacheCreation' },
  { key: 'cache_read_tokens', label: 'cacheRead' }, { key: 'total_tokens', label: 'tokens' },
  { key: 'total_cost', label: 'referenceCost' }, { key: 'actual_cost', label: 'actualCost' },
  { key: 'rpm', label: 'rpm' }, { key: 'tpm', label: 'tpm' },
]
const dimensionOptions = computed(() => (['task', 'project', 'client', 'host', 'api_key', 'model', ...(props.admin ? ['user'] : [])] as AttributionDimension[]).map(value => ({ value, label: t(`attribution.${value}`) })))
const scope = computed(() => ({ start_date: props.startDate, end_date: props.endDate, user_id: props.admin ? props.userId : undefined, api_key_id: props.apiKeyId, model: props.model || undefined }))
function identity(value: string): { id?: string; user_id?: number } {
  if (!value) return {}
  const [user_id, id] = JSON.parse(value) as [number, string]
  return { id, user_id }
}
const projectIdentity = computed(() => identity(selected.project))
const taskIdentity = computed(() => identity(selected.task))
const params = computed<AttributionFilters>(() => ({
  ...scope.value, group_by: groupBy.value,
  ...(unknownOnly.value ? { unknown: true } : {
    user_id: props.admin ? taskIdentity.value.user_id || projectIdentity.value.user_id || props.userId : undefined,
    project_id: projectIdentity.value.id, task_id: taskIdentity.value.id,
    client_kind: selected.client || undefined, host: selected.host || undefined,
  }),
}))
function groupName(row: AttributionGroup) {
  return row.name || (row.id == null || row.id === '' ? t('attribution.unknown') : t('attribution.unnamed'))
}
function filterOptions(dimension: FilterDimension) {
  const unique = new Map<string, string>()
  for (const row of available[dimension]) {
    if (!row.id) continue
    const scoped = dimension === 'project' || dimension === 'task'
    const value = scoped ? JSON.stringify([row.user_id, row.id]) : row.id
    const name = row.name || (scoped ? t('attribution.unnamed') : row.id)
    unique.set(value, scoped && props.admin ? `${name} · ${ownerName(row.user_id)}` : name)
  }
  return [{ value: '', label: t('attribution.all') }, ...Array.from(unique, ([value, label]) => ({ value, label }))]
}
function ownerName(id?: number | null) {
  return id ? ownerNames.value[String(id)] || `${t('attribution.unnamed')} (#${id})` : t('attribution.unknown')
}
function metricValue(row: AttributionMeasures, key: keyof AttributionMeasures) {
  const value = row[key]
  return typeof value === 'string' ? attributionCost(value) : value.toLocaleString(undefined, { maximumFractionDigits: key === 'rpm' || key === 'tpm' ? 4 : 0 })
}
async function loadReport() {
  reportController?.abort()
  const controller = new AbortController()
  reportController = controller
  loading.value = true
  error.value = ''
  report.value = null
  editing.value = null
  try {
    const result = await getAttribution(props.admin, params.value, controller.signal)
    if (!controller.signal.aborted) report.value = result
  } catch {
    if (!controller.signal.aborted) error.value = t('attribution.loadFailed')
  } finally {
    if (reportController === controller) loading.value = false
  }
}
async function loadOptions() {
  optionsController?.abort()
  const controller = new AbortController()
  optionsController = controller
  optionsLoading.value = true
  optionsError.value = false
  for (const dimension of filterDimensions) available[dimension] = []
  try {
    const result = await getAttributionOptions(props.admin, {
      ...scope.value,
      project_id: projectIdentity.value.id,
      user_id: props.admin ? projectIdentity.value.user_id || props.userId : undefined,
    }, controller.signal)
    if (!controller.signal.aborted) {
      available.project = result.projects
      available.task = result.tasks
      available.client = result.clients
      available.host = result.hosts
      optionsTruncated.value = result.truncated
      ownerNames.value = Object.fromEntries(result.users.filter(row => row.id && row.name).map(row => [row.id!, row.name!]))
    }
  } catch {
    if (!controller.signal.aborted) optionsError.value = true
  } finally {
    if (optionsController === controller) optionsLoading.value = false
  }
}
function refresh() { void loadReport(); void loadOptions() }
function canRename(row: AttributionGroup) {
  return !!row.id && (groupBy.value === 'task' || groupBy.value === 'project') && (!props.admin || !!row.user_id)
}
function beginRename(row: AttributionGroup) {
  if (groupBy.value !== 'task' && groupBy.value !== 'project') return
  editing.value = { kind: groupBy.value, row }
  editedName.value = row.name || ''
}
async function saveName() {
  if (!editing.value?.row.id || !editedName.value.trim() || saving.value) return
  const target = editing.value
  saving.value = true
  error.value = ''
  try {
    await renameAttribution(props.admin, { kind: target.kind, id: target.row.id!, name: editedName.value.trim(), ...(props.admin ? { user_id: target.row.user_id! } : {}) })
    editing.value = null
    refresh()
  } catch { error.value = t('attribution.renameFailed') }
  finally { saving.value = false }
}
async function download() {
  exporting.value = true
  error.value = ''
  try {
    const snapshot = { ...params.value }
    const blob = await exportAttribution(props.admin, snapshot)
    saveAs(blob, `usage-${snapshot.group_by}-${snapshot.start_date}-${snapshot.end_date}.csv`)
  } catch { error.value = t('attribution.exportFailed') }
  finally { exporting.value = false }
}
const distributionData = computed(() => {
  const rows = [...(report.value?.groups || [])].sort((a, b) => Number(b.actual_cost) - Number(a.actual_cost)).slice(0, 10)
  return { labels: rows.map(row => { const name = groupName(row); return name.length > 24 ? `${name.slice(0, 24)}…` : name }), datasets: [{ label: t('attribution.actualCost'), data: rows.map(row => Number(row.actual_cost)), backgroundColor: '#3b82f6' }] }
})
const trendData = computed(() => ({
  labels: report.value?.series.map(row => row.date) || [],
  datasets: ([['input_tokens', 'input', '#3b82f6'], ['output_tokens', 'output', '#10b981'], ['cache_creation_tokens', 'cacheCreation', '#f59e0b'], ['cache_read_tokens', 'cacheRead', '#8b5cf6']] as const).map(([key, label, color]) => ({ label: t(`attribution.${label}`), data: report.value?.series.map(row => row[key]) || [], borderColor: color, backgroundColor: color, tension: 0.2 })),
}))
const barOptions: ChartOptions<'bar'> = { responsive: true, maintainAspectRatio: false, indexAxis: 'y', plugins: { legend: { display: false } }, scales: { x: { beginAtZero: true } } }
const lineOptions: ChartOptions<'line'> = { responsive: true, maintainAspectRatio: false, scales: { y: { beginAtZero: true } } }
watch(() => [props.userId, props.apiKeyId], () => {
  for (const dimension of filterDimensions) selected[dimension] = ''
})
watch(() => selected.project, () => { selected.task = ''; void loadOptions() })
watch(params, () => { void loadReport() }, { immediate: true })
watch(scope, () => { void loadOptions() }, { immediate: true })
onBeforeUnmount(() => { reportController?.abort(); optionsController?.abort() })
</script>
