<template>
  <AppLayout>
    <div class="space-y-5 pb-8">
      <div class="flex flex-wrap items-start justify-between gap-3">
        <div>
          <h1 class="text-2xl font-semibold text-gray-900 dark:text-gray-100">
            {{ t('admin.accountHealth.title') }}
          </h1>
          <p class="mt-1 text-sm text-gray-500 dark:text-gray-400">
            {{ t('admin.accountHealth.description', { minutes: windowMinutes || '-' }) }}
          </p>
          <p v-if="evaluatedAt" class="mt-1 text-xs text-gray-400 dark:text-gray-500">
            {{ t('admin.accountHealth.evaluatedAt', { time: formatDate(evaluatedAt) }) }}
          </p>
        </div>
        <div class="flex flex-wrap gap-2">
          <button type="button" class="btn btn-secondary" :disabled="loading" @click="reload">
            <Icon name="refresh" size="sm" :class="loading ? 'animate-spin' : ''" />
            {{ t('common.refresh') }}
          </button>
          <button
            v-if="authStore.isSuperAdmin"
            type="button"
            class="btn btn-primary"
            :disabled="settingsLoading"
            data-test="settings-button"
            @click="openSettings"
          >
            <Icon name="cog" size="sm" />
            {{ t('common.settings') }}
          </button>
        </div>
      </div>

      <section class="grid gap-3 sm:grid-cols-2 xl:grid-cols-5" :aria-label="t('admin.accountHealth.overview.title')">
        <div
          v-for="card in overviewCards"
          :key="card.key"
          class="rounded-md border border-gray-200 bg-white px-4 py-3 dark:border-dark-700 dark:bg-dark-800"
        >
          <div class="text-xs font-medium uppercase text-gray-500 dark:text-gray-400">
            {{ t(`admin.accountHealth.overview.${card.key}`) }}
          </div>
          <div class="mt-1 text-2xl font-semibold tabular-nums text-gray-900 dark:text-gray-100">
            {{ card.value }}
          </div>
        </div>
      </section>

      <form class="rounded-md border border-gray-200 bg-white p-4 dark:border-dark-700 dark:bg-dark-800" @submit.prevent="applyFilters">
        <div class="grid gap-3 md:grid-cols-4">
          <label class="block">
            <span class="input-label">{{ t('admin.accountHealth.filters.search') }}</span>
            <div class="relative">
              <Icon name="search" size="sm" class="pointer-events-none absolute left-3 top-1/2 -translate-y-1/2 text-gray-400" />
              <input v-model.trim="filters.search" type="text" class="input pl-9" :placeholder="t('admin.accountHealth.filters.searchPlaceholder')" />
            </div>
          </label>
          <label class="block">
            <span class="input-label">{{ t('admin.accountHealth.filters.platform') }}</span>
            <input v-model.trim="filters.platform" type="text" class="input" :placeholder="t('admin.accountHealth.filters.platformPlaceholder')" />
          </label>
          <label class="block">
            <span class="input-label">{{ t('admin.accountHealth.filters.state') }}</span>
            <Select v-model="filters.state" :options="stateOptions" />
          </label>
          <div class="flex items-end gap-2">
            <button type="submit" class="btn btn-primary" :disabled="loading">{{ t('common.apply') }}</button>
            <button type="button" class="btn btn-secondary" :disabled="loading" @click="clearFilters">{{ t('common.clear') }}</button>
          </div>
        </div>
      </form>

      <div v-if="error" role="alert" class="rounded-md border border-red-200 bg-red-50 px-4 py-3 text-sm text-red-700 dark:border-red-800 dark:bg-red-900/20 dark:text-red-300">
        {{ error }}
      </div>

      <section class="overflow-hidden rounded-md border border-gray-200 bg-white dark:border-dark-700 dark:bg-dark-800">
        <div class="overflow-x-auto">
          <table class="min-w-full divide-y divide-gray-200 text-sm dark:divide-dark-700">
            <thead class="bg-gray-50 text-left text-xs uppercase text-gray-500 dark:bg-dark-700/60 dark:text-gray-400">
              <tr>
                <th class="px-4 py-3">{{ t('admin.accountHealth.columns.account') }}</th>
                <th class="px-4 py-3">{{ t('admin.accountHealth.columns.platform') }}</th>
                <th class="px-4 py-3">{{ t('admin.accountHealth.columns.state') }}</th>
                <th class="px-4 py-3 text-right">{{ t('admin.accountHealth.columns.success') }}</th>
                <th class="px-4 py-3 text-right">{{ t('admin.accountHealth.columns.errors') }}</th>
                <th class="px-4 py-3 text-right">{{ t('admin.accountHealth.columns.errorRate') }}</th>
                <th class="px-4 py-3 text-right">{{ t('admin.accountHealth.columns.latency') }}</th>
                <th class="px-4 py-3 text-right">{{ t('admin.accountHealth.columns.score') }}</th>
                <th class="px-4 py-3">{{ t('admin.accountHealth.columns.scheduling') }}</th>
              </tr>
            </thead>
            <tbody class="divide-y divide-gray-100 dark:divide-dark-700">
              <tr v-if="loading">
                <td colspan="9" class="px-4 py-10 text-center text-sm text-gray-500 dark:text-gray-400">
                  {{ t('common.loading') }}
                </td>
              </tr>
              <tr v-else-if="items.length === 0">
                <td colspan="9" class="px-4 py-10 text-center text-sm text-gray-500 dark:text-gray-400">
                  {{ t('common.noData') }}
                </td>
              </tr>
              <template v-else>
                <tr v-for="item in items" :key="item.account_id" class="hover:bg-gray-50/70 dark:hover:bg-dark-700/40">
                  <td class="px-4 py-3">
                    <div class="min-w-[180px]">
                      <div class="truncate font-medium text-gray-900 dark:text-gray-100" :title="item.name">
                        {{ item.name || t('admin.accountHealth.unnamedAccount') }}
                      </div>
                      <div class="mt-0.5 text-xs text-gray-400">
                        #{{ item.account_id }} · {{ item.account_status }}
                      </div>
                    </div>
                  </td>
                  <td class="whitespace-nowrap px-4 py-3 text-gray-700 dark:text-gray-300">{{ item.platform }}</td>
                  <td class="px-4 py-3">
                    <span :class="stateBadgeClass(item.state)">{{ stateLabel(item.state) }}</span>
                    <span v-if="!item.has_enough_samples" class="ml-1 inline-flex rounded-full bg-gray-100 px-2 py-0.5 text-xs font-medium text-gray-600 dark:bg-dark-700 dark:text-gray-300">
                      {{ t('admin.accountHealth.state.insufficientSamples') }}
                    </span>
                  </td>
                  <td class="px-4 py-3 text-right tabular-nums text-gray-700 dark:text-gray-300">{{ item.success_count }}</td>
                  <td class="px-4 py-3 text-right tabular-nums text-gray-700 dark:text-gray-300">{{ item.error_count }} / {{ item.total_count }}</td>
                  <td class="px-4 py-3 text-right tabular-nums" :class="errorRateClass(item.state)">
                    {{ formatPercent(item.error_rate) }}
                  </td>
                  <td class="px-4 py-3 text-right tabular-nums text-gray-700 dark:text-gray-300">{{ formatLatency(item.avg_latency_ms) }}</td>
                  <td class="px-4 py-3 text-right tabular-nums text-gray-700 dark:text-gray-300">{{ formatScore(item.score) }}</td>
                  <td class="px-4 py-3 text-gray-600 dark:text-gray-300">
                    <div v-if="item.temporarily_unschedulable" class="max-w-[260px] text-xs">
                      <div class="font-medium text-amber-700 dark:text-amber-300">{{ t('admin.accountHealth.unschedulable') }}</div>
                      <div class="truncate" :title="item.unschedulable_reason || undefined">{{ item.unschedulable_reason || t('common.unknown') }}</div>
                      <div v-if="item.unschedulable_until" class="text-gray-400">{{ formatDate(item.unschedulable_until) }}</div>
                    </div>
                    <span v-else class="text-xs text-gray-400">{{ t('admin.accountHealth.schedulable') }}</span>
                  </td>
                </tr>
              </template>
            </tbody>
          </table>
        </div>
        <Pagination
          :total="total"
          :page="page"
          :page-size="pageSize"
          @update:page="changePage"
          @update:pageSize="changePageSize"
        />
      </section>
    </div>

    <BaseDialog
      :show="settingsVisible"
      :title="t('admin.accountHealth.settings.title')"
      width="wide"
      :close-on-click-outside="true"
      @close="settingsVisible = false"
    >
      <form v-if="settingsForm" class="space-y-4" data-test="settings-form" @submit.prevent="saveSettings">
        <label class="flex items-center gap-3 rounded-md bg-gray-50 px-3 py-2 text-sm text-gray-700 dark:bg-dark-900 dark:text-gray-300">
          <input v-model="settingsForm.enabled" type="checkbox" class="rounded text-primary-600" />
          {{ t('admin.accountHealth.settings.enabled') }}
        </label>
        <div class="grid gap-3 sm:grid-cols-2 lg:grid-cols-3">
          <label v-for="field in numericSettingsFields" :key="field.key" class="block">
            <span class="input-label">{{ field.label }}</span>
            <input
              v-model.number="settingsForm[field.key]"
              type="number"
              :min="field.min"
              :max="field.max"
              step="1"
              class="input"
            />
          </label>
        </div>
        <div class="grid gap-3 sm:grid-cols-2">
          <label class="block">
            <span class="input-label">{{ t('admin.accountHealth.settings.isolateErrorRate') }}</span>
            <input v-model.number="settingsForm.isolate_error_rate" type="number" min="0.01" max="1" step="0.01" class="input" />
          </label>
          <label class="block">
            <span class="input-label">{{ t('admin.accountHealth.settings.recoverErrorRate') }}</span>
            <input v-model.number="settingsForm.recover_error_rate" type="number" min="0" max="1" step="0.01" class="input" />
          </label>
        </div>
      </form>
      <div v-else class="py-10 text-center text-sm text-gray-500 dark:text-gray-400">{{ t('common.loading') }}</div>
      <template #footer>
        <button type="button" class="btn btn-secondary" @click="settingsVisible = false">{{ t('common.cancel') }}</button>
        <button type="button" class="btn btn-primary" :disabled="settingsSaving || !settingsForm" @click="saveSettings">
          {{ settingsSaving ? t('common.saving') : t('common.save') }}
        </button>
      </template>
    </BaseDialog>
  </AppLayout>
</template>

<script setup lang="ts">
import { computed, onMounted, onUnmounted, reactive, ref } from 'vue'
import { useI18n } from 'vue-i18n'
import AppLayout from '@/components/layout/AppLayout.vue'
import BaseDialog from '@/components/common/BaseDialog.vue'
import Icon from '@/components/icons/Icon.vue'
import Pagination from '@/components/common/Pagination.vue'
import Select, { type SelectOption } from '@/components/common/Select.vue'
import accountHealthAPI, {
  type AccountHealthItem,
  type AccountHealthListParams,
  type AccountHealthOverview,
  type AccountHealthSettings,
  type AccountHealthState,
} from '@/api/admin/accountHealth'
import { useAppStore, useAuthStore } from '@/stores'
import { extractApiErrorMessage } from '@/utils/apiError'

const { t } = useI18n()
const appStore = useAppStore()
const authStore = useAuthStore()

const emptyOverview: AccountHealthOverview = {
  healthy: 0,
  degraded: 0,
  isolated: 0,
  no_samples: 0,
  total_accounts: 0,
}

const items = ref<AccountHealthItem[]>([])
const overview = ref<AccountHealthOverview>({ ...emptyOverview })
const loading = ref(false)
const error = ref('')
const total = ref(0)
const page = ref(1)
const pageSize = ref(20)
const evaluatedAt = ref('')
const windowMinutes = ref<number | null>(null)
const filters = reactive<{ search: string; platform: string; state: AccountHealthState | '' }>({
  search: '',
  platform: '',
  state: '',
})

const settingsVisible = ref(false)
const settingsLoading = ref(false)
const settingsSaving = ref(false)
const settingsForm = ref<AccountHealthSettings | null>(null)

let dataController: AbortController | null = null
let dataVersion = 0
let settingsController: AbortController | null = null

const stateOptions = computed<SelectOption[]>(() => [
  { value: '', label: t('admin.accountHealth.filters.allStates') },
  { value: 'healthy', label: t('admin.accountHealth.state.healthy') },
  { value: 'degraded', label: t('admin.accountHealth.state.degraded') },
  { value: 'isolated', label: t('admin.accountHealth.state.isolated') },
])

const overviewCards = computed(() => [
  { key: 'total_accounts', value: overview.value.total_accounts },
  { key: 'healthy', value: overview.value.healthy },
  { key: 'degraded', value: overview.value.degraded },
  { key: 'isolated', value: overview.value.isolated },
  { key: 'no_samples', value: overview.value.no_samples },
])

const numericSettingsFields = computed(() => [
  { key: 'window_minutes' as const, label: t('admin.accountHealth.settings.windowMinutes'), min: 1, max: 1440 },
  { key: 'min_samples' as const, label: t('admin.accountHealth.settings.minSamples'), min: 1, max: 1000000 },
  { key: 'cooldown_minutes' as const, label: t('admin.accountHealth.settings.cooldownMinutes'), min: 1, max: 10080 },
  { key: 'interval_seconds' as const, label: t('admin.accountHealth.settings.intervalSeconds'), min: 10, max: 3600 },
])

function buildParams(): AccountHealthListParams {
  const params: AccountHealthListParams = { page: page.value, page_size: pageSize.value }
  if (filters.search) params.search = filters.search
  if (filters.platform) params.platform = filters.platform
  if (filters.state) params.state = filters.state
  return params
}

async function loadData(): Promise<void> {
  const current = ++dataVersion
  dataController?.abort()
  const controller = new AbortController()
  dataController = controller
  loading.value = true
  try {
    const response = await accountHealthAPI.list(buildParams(), { signal: controller.signal })
    if (controller.signal.aborted || current !== dataVersion) return
    items.value = response.items
    total.value = response.total
    page.value = response.page
    pageSize.value = response.page_size
    overview.value = response.overview ?? { ...emptyOverview }
    evaluatedAt.value = response.evaluated_at
    windowMinutes.value = response.window_minutes
    error.value = ''
  } catch (err) {
    const canceled = (err as { name?: string; code?: string })?.name === 'AbortError' || (err as { code?: string })?.code === 'ERR_CANCELED'
    if (!canceled && current === dataVersion) {
      error.value = extractApiErrorMessage(err, t('admin.accountHealth.errors.load'))
    }
  } finally {
    if (current === dataVersion) loading.value = false
  }
}

function reload(): void {
  void loadData()
}

function applyFilters(): void {
  page.value = 1
  void loadData()
}

function clearFilters(): void {
  filters.search = ''
  filters.platform = ''
  filters.state = ''
  applyFilters()
}

function changePage(nextPage: number): void {
  page.value = nextPage
  void loadData()
}

function changePageSize(nextPageSize: number): void {
  pageSize.value = nextPageSize
  page.value = 1
  void loadData()
}

async function openSettings(): Promise<void> {
  if (!authStore.isSuperAdmin) return
  settingsVisible.value = true
  settingsLoading.value = true
  settingsController?.abort()
  const controller = new AbortController()
  settingsController = controller
  try {
    settingsForm.value = await accountHealthAPI.getSettings({ signal: controller.signal })
  } catch (err) {
    const canceled = (err as { name?: string; code?: string })?.name === 'AbortError' || (err as { code?: string })?.code === 'ERR_CANCELED'
    if (!canceled) appStore.showError(extractApiErrorMessage(err, t('admin.accountHealth.errors.settingsLoad')))
  } finally {
    if (settingsController === controller) settingsLoading.value = false
  }
}

async function saveSettings(): Promise<void> {
  if (!settingsForm.value || settingsSaving.value || !authStore.isSuperAdmin) return
  if (!validSettings(settingsForm.value)) {
    appStore.showError(t('admin.accountHealth.errors.settingsInvalid'))
    return
  }
  settingsSaving.value = true
  try {
    settingsForm.value = await accountHealthAPI.updateSettings({ ...settingsForm.value })
    settingsVisible.value = false
    appStore.showSuccess(t('admin.accountHealth.messages.settingsSaved'))
    await loadData()
  } catch (err) {
    appStore.showError(extractApiErrorMessage(err, t('admin.accountHealth.errors.settingsSave')))
  } finally {
    settingsSaving.value = false
  }
}

function validSettings(settings: AccountHealthSettings): boolean {
  return settings.window_minutes >= 1
    && settings.window_minutes <= 1440
    && settings.min_samples >= 1
    && settings.min_samples <= 1000000
    && settings.isolate_error_rate > 0
    && settings.isolate_error_rate <= 1
    && settings.recover_error_rate >= 0
    && settings.recover_error_rate < settings.isolate_error_rate
    && settings.cooldown_minutes >= 1
    && settings.cooldown_minutes <= 10080
    && settings.interval_seconds >= 10
    && settings.interval_seconds <= 3600
}

function stateLabel(state: AccountHealthState): string {
  return t(`admin.accountHealth.state.${state}`)
}

function stateBadgeClass(state: AccountHealthState): string {
  const base = 'inline-flex rounded-full px-2 py-0.5 text-xs font-medium'
  if (state === 'healthy') return `${base} bg-green-50 text-green-700 dark:bg-green-900/30 dark:text-green-300`
  if (state === 'degraded') return `${base} bg-amber-50 text-amber-700 dark:bg-amber-900/30 dark:text-amber-300`
  return `${base} bg-red-50 text-red-700 dark:bg-red-900/30 dark:text-red-300`
}

function errorRateClass(state: AccountHealthState): string {
  if (state === 'isolated') return 'text-red-600 dark:text-red-300'
  if (state === 'degraded') return 'text-amber-700 dark:text-amber-300'
  return 'text-gray-700 dark:text-gray-300'
}

function formatPercent(value: number): string {
  return `${(value * 100).toFixed(1)}%`
}

function formatLatency(value: number | null): string {
  return value == null ? t('common.notAvailable') : `${Math.round(value)} ms`
}

function formatScore(value: number): string {
  return Math.round(value).toString()
}

function formatDate(value: string): string {
  if (!value) return t('common.notAvailable')
  return new Date(value).toLocaleString()
}

onMounted(() => {
  void loadData()
})

onUnmounted(() => {
  dataController?.abort()
  settingsController?.abort()
})
</script>
