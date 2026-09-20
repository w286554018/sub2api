<template>
  <AppLayout>
    <div class="space-y-5">
      <div class="flex flex-wrap items-start justify-between gap-3">
        <div>
          <h1 class="text-2xl font-semibold text-gray-900 dark:text-gray-100">{{ t('admin.intelligentTests.title') }}</h1>
          <p class="mt-1 text-sm text-gray-500 dark:text-gray-400">{{ t(`admin.intelligentTests.modes.${mode}.description`) }}</p>
        </div>
        <button type="button" class="btn btn-secondary" :disabled="loading || settingsLoading" @click="refresh">
          <Icon name="refresh" size="sm" :class="loading || settingsLoading ? 'animate-spin' : ''" />
          {{ t('common.refresh') }}
        </button>
      </div>

      <nav class="flex flex-wrap gap-2" :aria-label="t('admin.intelligentTests.tabsLabel')">
        <router-link
          v-for="tab in visibleTabs"
          :key="tab.mode"
          :to="tab.path"
          class="rounded-md border px-3 py-2 text-sm font-medium transition-colors"
          :class="mode === tab.mode ? 'border-primary-500 bg-primary-50 text-primary-700 dark:bg-primary-900/30 dark:text-primary-300' : 'border-gray-200 bg-white text-gray-600 hover:bg-gray-50 dark:border-dark-700 dark:bg-dark-800 dark:text-gray-300 dark:hover:bg-dark-700'"
        >
          {{ t(`admin.intelligentTests.modes.${tab.mode}.title`) }}
        </router-link>
      </nav>

      <div v-if="error" role="alert" class="rounded-md border border-red-200 bg-red-50 px-4 py-3 text-sm text-red-700 dark:border-red-800 dark:bg-red-900/20 dark:text-red-300">
        {{ error }}
      </div>

      <section v-if="mode === 'tests'" class="space-y-4">
        <div class="grid gap-3 sm:grid-cols-2 xl:grid-cols-5">
          <div v-for="item in overviewCards" :key="item.key" class="rounded-md border border-gray-200 bg-white p-4 dark:border-dark-700 dark:bg-dark-800">
            <div class="text-xs text-gray-500 dark:text-gray-400">{{ t(`admin.intelligentTests.overview.${item.key}`) }}</div>
            <div class="mt-2 text-2xl font-semibold tabular-nums text-gray-900 dark:text-gray-100">{{ item.value }}</div>
          </div>
        </div>

        <form class="rounded-md border border-gray-200 bg-white p-4 dark:border-dark-700 dark:bg-dark-800" @submit.prevent="applyFilters">
          <div class="grid gap-3 md:grid-cols-3 xl:grid-cols-6">
            <input v-model.trim="filters.search" class="input" :placeholder="t('admin.intelligentTests.filters.search')" />
            <input v-model.trim="filters.platform" class="input" :placeholder="t('admin.intelligentTests.filters.platform')" />
            <Select v-model="filters.account_type" :options="accountTypeOptions" />
            <Select v-model="filters.account_status" :options="accountStatusOptions" />
            <Select v-model="filters.test_type" :options="testTypeOptions" />
            <Select v-model="filters.status" :options="statusOptions" />
          </div>
          <div class="mt-4 flex flex-wrap items-center justify-between gap-3">
            <span class="text-sm text-gray-500 dark:text-gray-400">{{ t('admin.intelligentTests.selectedAccounts', { count: selectedAccountIds.length }) }}</span>
            <div class="flex flex-wrap gap-2">
              <button type="button" class="btn btn-secondary" @click="resetFilters">{{ t('common.reset') }}</button>
              <button type="submit" class="btn btn-primary">{{ t('common.apply') }}</button>
              <button type="button" class="btn btn-primary" :disabled="running || !selectedAccountIds.length || !selectedTestTypes.length" @click="runSelected">
                <Icon name="play" size="sm" />
                {{ t('admin.intelligentTests.runSelected') }}
              </button>
            </div>
          </div>
          <div class="mt-3 flex flex-wrap gap-3">
            <label v-for="setting in enabledSettings" :key="setting.test_type" class="inline-flex items-center gap-2 text-sm text-gray-700 dark:text-gray-300">
              <input v-model="selectedTestTypes" type="checkbox" class="rounded text-primary-600" :value="setting.test_type" />
              {{ testName(setting) }}
            </label>
          </div>
        </form>

        <div class="overflow-hidden rounded-md border border-gray-200 bg-white dark:border-dark-700 dark:bg-dark-800">
          <table class="min-w-full divide-y divide-gray-200 text-sm dark:divide-dark-700">
            <thead class="bg-gray-50 text-left text-xs uppercase text-gray-500 dark:bg-dark-700/60 dark:text-gray-400">
              <tr>
                <th class="w-10 px-4 py-3"><input type="checkbox" class="rounded text-primary-600" :checked="pageSelected" @change="togglePageSelection" /></th>
                <th class="px-4 py-3">{{ t('admin.intelligentTests.account') }}</th>
                <th class="px-4 py-3">{{ t('admin.intelligentTests.platform') }}</th>
                <th class="min-w-56 px-4 py-3">{{ t('admin.intelligentTests.model') }}</th>
                <th class="px-4 py-3">{{ t('admin.intelligentTests.tests') }}</th>
              </tr>
            </thead>
            <tbody class="divide-y divide-gray-100 dark:divide-dark-700">
              <tr v-if="loading"><td colspan="5" class="px-4 py-8 text-center text-gray-500">{{ t('common.loading') }}</td></tr>
              <tr v-else-if="!accounts.length"><td colspan="5" class="px-4 py-8 text-center text-gray-500">{{ t('common.noData') }}</td></tr>
              <tr v-for="account in accounts" v-else :key="account.account_id" class="align-top hover:bg-gray-50 dark:hover:bg-dark-700/50">
                <td class="px-4 py-3"><input v-model="selectedAccountIds" type="checkbox" class="rounded text-primary-600" :value="account.account_id" /></td>
                <td class="px-4 py-3">
                  <div class="font-medium text-gray-900 dark:text-gray-100">{{ account.name || `#${account.account_id}` }}</div>
                  <div class="text-xs text-gray-500">#{{ account.account_id }} · {{ account.account_type }} · {{ account.account_status }}</div>
                  <div v-if="account.notes" class="mt-1 max-w-sm truncate text-xs text-gray-500">{{ account.notes }}</div>
                </td>
                <td class="px-4 py-3 text-gray-700 dark:text-gray-300">{{ account.platform }}</td>
                <td class="px-4 py-3">
                  <div @click.capture="loadAccountModels(account.account_id)">
                    <Select
                      v-model="selectedAccountModels[account.account_id]"
                      :data-test="`account-model-${account.account_id}`"
                      :options="accountModelOptions(account.account_id)"
                      value-key="id"
                      label-key="display_name"
                      size="sm"
                      :loading="accountModelsLoading[account.account_id]"
                    />
                  </div>
                  <p class="mt-1 text-xs text-gray-500">{{ t('admin.intelligentTests.modelHint') }}</p>
                </td>
                <td class="px-4 py-3">
                  <div class="grid gap-2 lg:grid-cols-2">
                    <div v-for="test in account.tests" :key="test.test_type" class="rounded-md border border-gray-100 p-3 dark:border-dark-700">
                      <div class="flex items-center justify-between gap-2">
                        <span class="font-medium text-gray-900 dark:text-gray-100">{{ settingName(test.test_type) }}</span>
                        <StatusPill :status="test.latest?.status" :label="statusLabel(test.latest?.status)" />
                      </div>
                      <div class="mt-2 flex flex-wrap items-center gap-2 text-xs text-gray-500">
                        <span>{{ t('admin.intelligentTests.historyCount', { count: test.history_count }) }}</span>
                        <span v-if="test.latest?.model">{{ test.latest.model }}</span>
                        <span v-if="test.latest?.created_at">{{ formatDate(test.latest.created_at) }}</span>
                      </div>
                      <div class="mt-3 flex flex-wrap gap-2">
                        <button type="button" class="btn btn-xs btn-secondary" @click="runAccounts([account.account_id], [test.test_type])">{{ t('admin.intelligentTests.run') }}</button>
                        <button v-if="test.latest" type="button" class="btn btn-xs btn-ghost" @click="openDetail(test.latest.id)">{{ t('common.view') }}</button>
                        <button v-if="test.latest && canCancel(test.latest.status)" type="button" class="btn btn-xs btn-ghost" @click="cancelJob(test.latest.id)">{{ t('common.cancel') }}</button>
                      </div>
                    </div>
                  </div>
                </td>
              </tr>
            </tbody>
          </table>
          <Pagination :total="total" :page="page" :page-size="pageSize" @update:page="changePage" @update:page-size="changePageSize" />
        </div>
      </section>

      <section v-else-if="mode === 'history'" class="space-y-4">
        <form class="rounded-md border border-gray-200 bg-white p-4 dark:border-dark-700 dark:bg-dark-800" @submit.prevent="applyFilters">
          <div class="grid gap-3 md:grid-cols-3 xl:grid-cols-6">
            <input v-model.trim="filters.account_id" class="input" :placeholder="t('admin.intelligentTests.filters.accountId')" />
            <Select v-model="filters.test_type" :options="testTypeOptions" />
            <Select v-model="filters.status" :options="statusOptions" />
            <input v-model="filters.from" class="input" type="datetime-local" />
            <input v-model="filters.to" class="input" type="datetime-local" />
            <button type="submit" class="btn btn-primary">{{ t('common.apply') }}</button>
          </div>
        </form>
        <div class="overflow-hidden rounded-md border border-gray-200 bg-white dark:border-dark-700 dark:bg-dark-800">
          <table class="min-w-full divide-y divide-gray-200 text-sm dark:divide-dark-700">
            <thead class="bg-gray-50 text-left text-xs uppercase text-gray-500 dark:bg-dark-700/60 dark:text-gray-400"><tr><th class="px-4 py-3">ID</th><th class="px-4 py-3">{{ t('admin.intelligentTests.account') }}</th><th class="px-4 py-3">{{ t('admin.intelligentTests.testType') }}</th><th class="px-4 py-3">{{ t('admin.intelligentTests.model') }}</th><th class="px-4 py-3">{{ t('common.status') }}</th><th class="px-4 py-3">{{ t('admin.intelligentTests.score') }}</th><th class="px-4 py-3">{{ t('common.actions') }}</th></tr></thead>
            <tbody class="divide-y divide-gray-100 dark:divide-dark-700">
              <tr v-if="loading"><td colspan="7" class="px-4 py-8 text-center text-gray-500">{{ t('common.loading') }}</td></tr>
              <tr v-else-if="!records.length"><td colspan="7" class="px-4 py-8 text-center text-gray-500">{{ t('common.noData') }}</td></tr>
              <tr v-for="record in records" v-else :key="record.id" class="hover:bg-gray-50 dark:hover:bg-dark-700/50">
                <td class="px-4 py-3">#{{ record.id }}</td>
                <td class="px-4 py-3">#{{ record.account_id }}</td>
                <td class="px-4 py-3">{{ settingName(record.test_type) }}</td>
                <td class="px-4 py-3 text-xs text-gray-600 dark:text-gray-300">{{ record.model || '-' }}</td>
                <td class="px-4 py-3"><StatusPill :status="record.status" :label="statusLabel(record.status)" /></td>
                <td class="px-4 py-3">{{ record.score ?? '-' }}</td>
                <td class="px-4 py-3"><div class="flex flex-wrap gap-2"><button class="btn btn-xs btn-secondary" type="button" @click="openDetail(record.id)">{{ t('common.view') }}</button><button v-if="canCancel(record.status)" class="btn btn-xs btn-ghost" type="button" @click="cancelJob(record.id)">{{ t('common.cancel') }}</button><button v-if="isTerminal(record.status)" class="btn btn-xs btn-ghost" type="button" @click="reevaluateJob(record.id)">{{ t('admin.intelligentTests.reevaluate') }}</button></div></td>
              </tr>
            </tbody>
          </table>
          <Pagination :total="total" :page="page" :page-size="pageSize" @update:page="changePage" @update:page-size="changePageSize" />
        </div>
      </section>

      <section v-else class="space-y-4">
        <div v-if="!authStore.isSuperAdmin" class="rounded-md border border-amber-200 bg-amber-50 px-4 py-3 text-sm text-amber-800 dark:border-amber-800 dark:bg-amber-900/20 dark:text-amber-200">{{ t('admin.intelligentTests.settings.superAdminOnly') }}</div>
        <div v-for="setting in editableSettings" :key="setting.test_type" class="rounded-md border border-gray-200 bg-white p-4 dark:border-dark-700 dark:bg-dark-800">
          <form class="space-y-4" @submit.prevent="saveSetting(setting.test_type)">
            <div class="flex flex-wrap items-center justify-between gap-3"><div><h2 class="text-lg font-semibold text-gray-900 dark:text-gray-100">{{ testName(setting) }}</h2><p class="text-xs text-gray-500">{{ setting.test_type }}</p></div><div class="flex flex-wrap gap-4"><label class="inline-flex items-center gap-2 text-sm"><input v-model="setting.enabled" type="checkbox" class="rounded text-primary-600" />{{ t('common.enabled') }}</label></div></div>
            <div class="grid gap-3 lg:grid-cols-2"><label class="text-sm text-gray-600 dark:text-gray-300">{{ t('admin.intelligentTests.settings.prompt') }}<textarea v-model="setting.config.prompt" class="input mt-1 min-h-28" /></label><label class="text-sm text-gray-600 dark:text-gray-300">{{ t('admin.intelligentTests.settings.expectedAnswer') }}<textarea v-model="setting.config.expected_answer" class="input mt-1 min-h-28" /></label></div>
            <div class="grid gap-3 md:grid-cols-4"><label class="text-sm text-gray-600 dark:text-gray-300">{{ t('admin.intelligentTests.settings.model') }}<input v-model="setting.config.model" class="input mt-1" /></label><label class="text-sm text-gray-600 dark:text-gray-300">{{ t('admin.intelligentTests.settings.evaluator') }}<Select v-model="setting.config.evaluator" class="mt-1" :options="evaluatorOptions" /></label><label class="text-sm text-gray-600 dark:text-gray-300">{{ t('admin.intelligentTests.settings.answerFormat') }}<Select v-model="setting.config.answer_format" class="mt-1" :options="answerFormatOptions" /></label><label class="text-sm text-gray-600 dark:text-gray-300">{{ t('admin.intelligentTests.settings.timeout') }}<input v-model.number="setting.config.timeout_seconds" type="number" min="30" max="600" class="input mt-1" /></label></div>
            <div class="flex flex-wrap items-center gap-2"><button type="submit" class="btn btn-primary" :disabled="saving[setting.test_type] || !isDirty(setting.test_type)">{{ saving[setting.test_type] ? t('common.saving') : t('common.save') }}</button><button type="button" class="btn btn-secondary" @click="previewSetting(setting)">{{ t('admin.intelligentTests.settings.preview') }}</button><span v-if="saved[setting.test_type]" class="text-sm text-green-600">{{ t('common.saved') }}</span></div>
          </form>
        </div>
      </section>
    </div>

    <BaseDialog :show="Boolean(detailRecord)" :title="t('admin.intelligentTests.detail.title')" width="extra-wide" @close="detailRecord = null">
      <div v-if="detailRecord" class="space-y-4 text-sm">
        <div class="grid gap-3 md:grid-cols-5"><InfoItem label="ID" :value="`#${detailRecord.id}`" /><InfoItem :label="t('admin.intelligentTests.account')" :value="`#${detailRecord.account_id}`" /><InfoItem :label="t('admin.intelligentTests.testType')" :value="settingName(detailRecord.test_type)" /><InfoItem :label="t('admin.intelligentTests.model')" :value="detailRecord.model || '-'" /><InfoItem :label="t('common.status')" :value="statusLabel(detailRecord.status)" /></div>
        <div v-if="detailRecord.error_message" class="rounded-md bg-red-50 p-3 text-red-700 dark:bg-red-900/20 dark:text-red-300">{{ detailRecord.error_message }}</div>
        <div v-if="safeSvg" class="rounded-md border border-gray-200 bg-white p-3 dark:border-dark-700 dark:bg-dark-900" v-html="safeSvg"></div>
        <div class="rounded-md border border-gray-200 dark:border-dark-700"><div class="border-b border-gray-200 px-3 py-2 font-medium dark:border-dark-700">{{ t('admin.intelligentTests.detail.result') }}</div><pre class="max-h-80 overflow-auto whitespace-pre-wrap p-3 text-xs text-gray-700 dark:text-gray-300">{{ boundedResult(detailRecord.result) }}</pre></div>
        <div class="rounded-md border border-gray-200 dark:border-dark-700"><div class="border-b border-gray-200 px-3 py-2 font-medium dark:border-dark-700">{{ t('admin.intelligentTests.detail.evaluation') }}</div><pre class="max-h-60 overflow-auto whitespace-pre-wrap p-3 text-xs text-gray-700 dark:text-gray-300">{{ boundedJSON(detailRecord.evaluation) }}</pre></div>
      </div>
      <template #footer><button type="button" class="btn btn-secondary" @click="detailRecord = null">{{ t('common.close') }}</button></template>
    </BaseDialog>

    <BaseDialog :show="Boolean(previewRecord)" :title="t('admin.intelligentTests.settings.preview')" width="wide" @close="previewRecord = null">
      <pre v-if="previewRecord" class="max-h-96 overflow-auto whitespace-pre-wrap text-xs">{{ boundedJSON(previewRecord.evaluation) }}</pre>
      <template #footer><button type="button" class="btn btn-secondary" @click="previewRecord = null">{{ t('common.close') }}</button></template>
    </BaseDialog>
  </AppLayout>
</template>

<script setup lang="ts">
import { computed, defineComponent, h, onMounted, onUnmounted, reactive, ref, watch } from 'vue'
import { useI18n } from 'vue-i18n'
import DOMPurify from 'dompurify'
import AppLayout from '@/components/layout/AppLayout.vue'
import Pagination from '@/components/common/Pagination.vue'
import BaseDialog from '@/components/common/BaseDialog.vue'
import Select from '@/components/common/Select.vue'
import Icon from '@/components/icons/Icon.vue'
import { useAppStore, useAuthStore } from '@/stores'
import { extractApiErrorMessage } from '@/utils/apiError'
import { adminAPI } from '@/api/admin'
import type { ClaudeModel } from '@/types'
import {
  intelligentTestsAPI,
  newIntelligentTestRequestKey,
  type IntelligentTestAccount,
  type IntelligentTestFilters,
  type IntelligentTestRecord,
  type IntelligentTestSetting,
  type IntelligentTestStatus,
} from '@/api/admin/intelligentTests'

const props = withDefaults(defineProps<{ mode?: 'tests' | 'history' | 'settings' }>(), { mode: 'tests' })
const { t } = useI18n()
const appStore = useAppStore()
const authStore = useAuthStore()

const mode = computed(() => props.mode)
const tabs = [
  { mode: 'tests', path: '/admin/intelligent-tests' },
  { mode: 'history', path: '/admin/intelligent-tests/history' },
  { mode: 'settings', path: '/admin/intelligent-tests/settings' },
] as const
const visibleTabs = computed(() => tabs.filter(tab => tab.mode !== 'settings' || authStore.isSuperAdmin))

const statuses: IntelligentTestStatus[] = ['queued', 'running', 'completed', 'success', 'failed', 'rate_limited', 'account_error', 'model_error', 'request_error', 'network_error', 'suspected_degradation', 'cancelled']
const terminalStatuses = new Set<IntelligentTestStatus>(statuses.filter(status => status !== 'queued' && status !== 'running'))

const settings = ref<IntelligentTestSetting[]>([])
const originalSettings = ref<Record<string, string>>({})
const accounts = ref<IntelligentTestAccount[]>([])
const records = ref<IntelligentTestRecord[]>([])
const overview = ref({ total_accounts: 0, tested_today: 0, success_accounts: 0, abnormal_accounts: 0, suspected_degradation: 0 })
const total = ref(0)
const page = ref(1)
const pageSize = ref(24)
const loading = ref(false)
const settingsLoading = ref(false)
const running = ref(false)
const error = ref('')
const selectedAccountIds = ref<number[]>([])
const selectedTestTypes = ref<string[]>([])
const selectedAccountModels = reactive<Record<number, string>>({})
const accountModels = reactive<Record<number, ClaudeModel[]>>({})
const accountModelsLoading = reactive<Record<number, boolean>>({})
const detailRecord = ref<IntelligentTestRecord | null>(null)
const previewRecord = ref<IntelligentTestRecord | null>(null)
const saving = reactive<Record<string, boolean>>({})
const saved = reactive<Record<string, boolean>>({})

const filters = reactive<Record<string, string>>({ search: '', platform: '', account_type: '', account_status: '', test_type: '', status: '', account_id: '', from: '', to: '' })
let appliedFilters: IntelligentTestFilters = {}
let dataController: AbortController | null = null
let dataVersion = 0
let pollTimer: ReturnType<typeof setInterval> | null = null
let runKey = ''
let runSignature = ''

const enabledSettings = computed(() => settings.value.filter(setting => setting.enabled))
const editableSettings = computed(() => settings.value)
const pageSelected = computed(() => accounts.value.length > 0 && accounts.value.every(account => selectedAccountIds.value.includes(account.account_id)))
const hasActiveVisibleJobs = computed(() => {
  if (mode.value === 'history') return records.value.some(record => !isTerminal(record.status))
  return accounts.value.some(account => account.tests.some(test => test.latest && !isTerminal(test.latest.status)))
})
const overviewCards = computed(() => [
  { key: 'total_accounts', value: overview.value.total_accounts },
  { key: 'tested_today', value: overview.value.tested_today },
  { key: 'success_accounts', value: overview.value.success_accounts },
  { key: 'abnormal_accounts', value: overview.value.abnormal_accounts },
  { key: 'suspected_degradation', value: overview.value.suspected_degradation },
])
const accountTypeOptions = computed(() => [
  { value: '', label: t('admin.intelligentTests.filters.allTypes') },
  { value: 'oauth', label: 'OAuth' },
  { value: 'apikey', label: 'API Key' },
  { value: 'setup-token', label: 'Setup Token' },
])
const accountStatusOptions = computed(() => [
  { value: '', label: t('admin.intelligentTests.filters.allAccountStatus') },
  { value: 'active', label: t('common.active') },
  { value: 'disabled', label: t('common.disabled') },
  { value: 'error', label: t('common.error') },
])
const testTypeOptions = computed(() => [
  { value: '', label: t('admin.intelligentTests.filters.allTests') },
  ...settings.value.map(setting => ({ value: setting.test_type, label: testName(setting) })),
])
const statusOptions = computed(() => [
  { value: '', label: t('admin.intelligentTests.filters.allStatuses') },
  ...statuses.map(status => ({ value: status, label: statusLabel(status) })),
])
const evaluatorOptions = [
  { value: 'exact_answer', label: 'exact_answer' },
  { value: 'svg_structure', label: 'svg_structure' },
]
const answerFormatOptions = [
  { value: '', label: '-' },
  { value: 'answer_line', label: 'answer_line' },
  { value: 'free_text', label: 'free_text' },
]
const safeSvg = computed(() => {
  const svg = detailRecord.value?.result_image || ''
  if (!svg.trim()) return ''
  return DOMPurify.sanitize(svg, { USE_PROFILES: { svg: true, svgFilters: true } })
})

const InfoItem = defineComponent({
  props: { label: { type: String, required: true }, value: { type: String, required: true } },
  setup(componentProps) {
    return () => h('div', { class: 'rounded-md bg-gray-50 p-3 dark:bg-dark-700' }, [h('div', { class: 'text-xs text-gray-500' }, componentProps.label), h('div', { class: 'mt-1 font-medium text-gray-900 dark:text-gray-100' }, componentProps.value)])
  }
})

const StatusPill = defineComponent({
  props: { status: { type: String, default: '' }, label: { type: String, required: true } },
  setup(componentProps) {
    const tone = computed(() => {
      if (componentProps.status === 'success' || componentProps.status === 'completed') return 'bg-green-50 text-green-700 dark:bg-green-900/30 dark:text-green-300'
      if (componentProps.status === 'queued' || componentProps.status === 'running') return 'bg-blue-50 text-blue-700 dark:bg-blue-900/30 dark:text-blue-300'
      if (componentProps.status === 'cancelled') return 'bg-gray-100 text-gray-600 dark:bg-dark-700 dark:text-gray-300'
      return 'bg-amber-50 text-amber-700 dark:bg-amber-900/30 dark:text-amber-300'
    })
    return () => h('span', { class: ['inline-flex rounded-full px-2 py-0.5 text-xs font-medium', tone.value] }, componentProps.label)
  }
})

function isTerminal(status?: string): boolean {
  return !!status && terminalStatuses.has(status as IntelligentTestStatus)
}

function canCancel(status?: string): boolean {
  return status === 'queued' || status === 'running'
}

function statusLabel(status?: string): string {
  return status ? t(`admin.intelligentTests.status.${status}`) : t('admin.intelligentTests.status.none')
}

function testName(setting: IntelligentTestSetting): string {
  return setting.name || settingName(setting.test_type)
}

function settingName(testType: string): string {
  return t(`admin.intelligentTests.testNames.${testType}`) === `admin.intelligentTests.testNames.${testType}` ? testType : t(`admin.intelligentTests.testNames.${testType}`)
}

function boundedResult(value = ''): string {
  return value.length > 4096 ? `${value.slice(0, 4096)}...` : value
}

function boundedJSON(value: unknown): string {
  const text = JSON.stringify(value ?? {}, null, 2)
  return text.length > 4096 ? `${text.slice(0, 4096)}...` : text
}

function formatDate(value: string): string {
  return new Date(value).toLocaleString()
}

function filtersPayload(): IntelligentTestFilters {
  const payload: IntelligentTestFilters = {}
  for (const [key, value] of Object.entries(filters)) {
    if (!value) continue
    if (key === 'from' || key === 'to') payload[key] = new Date(value).toISOString()
    else if (key === 'account_id') payload[key] = Number(value)
    else payload[key] = value
  }
  return payload
}

async function loadSettings(): Promise<void> {
  settingsLoading.value = true
  try {
    const items = await intelligentTestsAPI.settings()
    settings.value = items.map(item => ({ ...item, config: { ...item.config } }))
    originalSettings.value = Object.fromEntries(settings.value.map(item => [item.test_type, JSON.stringify(item)]))
    if (!selectedTestTypes.value.length) selectedTestTypes.value = items.filter(item => item.enabled).map(item => item.test_type)
  } catch (err) {
    error.value = extractApiErrorMessage(err, t('admin.intelligentTests.errors.settingsLoad'))
  } finally {
    settingsLoading.value = false
  }
}

async function loadData(quiet = false): Promise<void> {
  const current = ++dataVersion
  dataController?.abort()
  dataController = new AbortController()
  if (!quiet) loading.value = true
  try {
    if (mode.value === 'history') {
      const data = await intelligentTestsAPI.jobs({ ...appliedFilters, page: page.value, page_size: pageSize.value }, dataController.signal)
      if (current !== dataVersion) return
      records.value = data.items
      total.value = data.total
    } else if (mode.value === 'tests') {
      const data = await intelligentTestsAPI.accounts({ ...appliedFilters, page: page.value, page_size: pageSize.value }, dataController.signal)
      if (current !== dataVersion) return
      accounts.value = data.items
      overview.value = data.overview
      total.value = data.total
    }
    if (current === dataVersion) error.value = ''
  } catch (err) {
    if (current === dataVersion && !dataController.signal.aborted) error.value = extractApiErrorMessage(err, t('admin.intelligentTests.errors.load'))
  } finally {
    if (current === dataVersion) loading.value = false
  }
}

async function refresh(): Promise<void> {
  await Promise.all([loadSettings(), mode.value === 'settings' ? Promise.resolve() : loadData(false)])
}

function applyFilters(): void {
  if (filters.account_id && !/^\d+$/.test(filters.account_id)) {
    error.value = t('admin.intelligentTests.errors.accountId')
    return
  }
  if (filters.from && filters.to && filters.from > filters.to) {
    error.value = t('admin.intelligentTests.errors.timeRange')
    return
  }
  appliedFilters = filtersPayload()
  page.value = 1
  selectedAccountIds.value = []
  void loadData()
}

function resetFilters(): void {
  for (const key of Object.keys(filters)) filters[key] = ''
  applyFilters()
}

function changePage(value: number): void {
  page.value = value
  selectedAccountIds.value = []
  void loadData()
}

function changePageSize(value: number): void {
  pageSize.value = value
  changePage(1)
}

function togglePageSelection(): void {
  selectedAccountIds.value = pageSelected.value ? [] : accounts.value.map(account => account.account_id)
}

function accountModelOptions(accountID: number): Array<Record<string, unknown>> {
  return [
    { id: '', type: 'model', display_name: t('admin.intelligentTests.modelAuto'), created_at: '' },
    ...(accountModels[accountID] || []).map(model => ({ ...model })),
  ]
}

async function loadAccountModels(accountID: number): Promise<void> {
  if (accountModelsLoading[accountID] || Object.prototype.hasOwnProperty.call(accountModels, accountID)) return
  accountModelsLoading[accountID] = true
  try {
    accountModels[accountID] = await adminAPI.accounts.getAvailableModels(accountID)
  } catch (err) {
    appStore.showError(extractApiErrorMessage(err, t('admin.intelligentTests.errors.modelsLoad')))
  } finally {
    accountModelsLoading[accountID] = false
  }
}

async function runSelected(): Promise<void> {
  await runAccounts(selectedAccountIds.value, selectedTestTypes.value)
}

async function runAccounts(accountIds: number[], testTypes: string[]): Promise<void> {
  if (!accountIds.length || !testTypes.length || running.value) return
  if (accountIds.length > 50) {
    appStore.showError(t('admin.intelligentTests.errors.batchLimit'))
    return
  }
  const modelOverrides = Object.fromEntries(
    accountIds
      .map(accountID => [String(accountID), (selectedAccountModels[accountID] || '').trim()] as const)
      .filter(([, model]) => model),
  )
  const signature = JSON.stringify([[...accountIds].sort((a, b) => a - b), [...testTypes].sort(), modelOverrides])
  if (signature !== runSignature) {
    runSignature = signature
    runKey = newIntelligentTestRequestKey()
  }
  running.value = true
  try {
    const response = await intelligentTestsAPI.run({
      account_ids: accountIds,
      test_types: testTypes,
      idempotency_key: runKey,
      ...(Object.keys(modelOverrides).length ? { account_models: modelOverrides } : {}),
    })
    appStore.showSuccess(t(response.reused ? 'admin.intelligentTests.messages.reused' : 'admin.intelligentTests.messages.enqueued', { count: response.created_count || response.reused_count || response.records.length }))
    runSignature = ''
    await loadData(false)
  } catch (err) {
    appStore.showError(extractApiErrorMessage(err, t('admin.intelligentTests.errors.run')))
  } finally {
    running.value = false
  }
}

async function openDetail(id: number): Promise<void> {
  try {
    detailRecord.value = await intelligentTestsAPI.detail(id)
  } catch (err) {
    appStore.showError(extractApiErrorMessage(err, t('admin.intelligentTests.errors.detail')))
  }
}

async function cancelJob(id: number): Promise<void> {
  try {
    await intelligentTestsAPI.cancel(id)
    appStore.showSuccess(t('admin.intelligentTests.messages.cancelled'))
    await loadData(true)
  } catch (err) {
    appStore.showError(extractApiErrorMessage(err, t('admin.intelligentTests.errors.cancel')))
  }
}

async function reevaluateJob(id: number): Promise<void> {
  try {
    const updated = await intelligentTestsAPI.reevaluate(id)
    appStore.showSuccess(t('admin.intelligentTests.messages.reevaluated'))
    if (detailRecord.value?.id === id) detailRecord.value = updated
    await loadData(true)
  } catch (err) {
    appStore.showError(extractApiErrorMessage(err, t('admin.intelligentTests.errors.reevaluate')))
  }
}

function isDirty(testType: string): boolean {
  const setting = settings.value.find(item => item.test_type === testType)
  return !!setting && JSON.stringify(setting) !== originalSettings.value[testType]
}

async function saveSetting(testType: string): Promise<void> {
  const setting = settings.value.find(item => item.test_type === testType)
  if (!setting) return
  saving[testType] = true
  saved[testType] = false
  try {
    const snapshot = JSON.parse(JSON.stringify(setting)) as IntelligentTestSetting
    await intelligentTestsAPI.saveSetting(snapshot)
    originalSettings.value[testType] = JSON.stringify(snapshot)
    saved[testType] = true
    appStore.showSuccess(t('common.saved'))
  } catch (err) {
    appStore.showError(extractApiErrorMessage(err, t('admin.intelligentTests.errors.save')))
  } finally {
    saving[testType] = false
  }
}

async function previewSetting(setting: IntelligentTestSetting): Promise<void> {
  try {
    previewRecord.value = await intelligentTestsAPI.evaluatePreview(setting.config.expected_answer || setting.config.prompt, setting.config)
  } catch (err) {
    appStore.showError(extractApiErrorMessage(err, t('admin.intelligentTests.errors.preview')))
  }
}

watch(mode, () => {
  page.value = 1
  appliedFilters = {}
  selectedAccountIds.value = []
  dataController?.abort()
  error.value = ''
  void refresh()
})

onMounted(() => {
  void refresh()
  pollTimer = setInterval(() => {
    if (mode.value !== 'settings' && hasActiveVisibleJobs.value && !loading.value && !document.hidden) void loadData(true)
  }, 5000)
})

onUnmounted(() => {
  dataVersion++
  dataController?.abort()
  if (pollTimer) clearInterval(pollTimer)
})
</script>
