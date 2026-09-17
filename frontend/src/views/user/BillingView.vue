<template>
  <AppLayout>
    <div class="mx-auto max-w-5xl space-y-6">
      <div class="flex flex-col gap-4 sm:flex-row sm:items-end sm:justify-between">
        <div>
          <h1 class="text-2xl font-semibold text-gray-900 dark:text-white">
            {{ t('billing.title') }}
          </h1>
          <p class="mt-1 text-sm text-gray-500 dark:text-gray-400">
            {{ t('billing.description') }}
          </p>
        </div>
        <label class="w-full sm:w-48">
          <span class="input-label">{{ t('billing.statementMonth') }}</span>
          <input v-model="monthText" type="month" class="input" @change="loadStatement" />
        </label>
      </div>

      <div class="grid grid-cols-1 gap-3 sm:grid-cols-3">
        <div
          v-for="card in overviewCards"
          :key="card.key"
          class="rounded-lg border border-gray-200 bg-white px-4 py-3 dark:border-dark-700 dark:bg-dark-800"
        >
          <p class="text-xs font-medium text-gray-500 dark:text-gray-400">{{ card.label }}</p>
          <p class="mt-1 text-xl font-semibold tabular-nums text-gray-900 dark:text-white">
            {{ card.value }}
          </p>
        </div>
      </div>

      <div class="overflow-hidden rounded-lg border border-gray-200 bg-white dark:border-dark-700 dark:bg-dark-800">
        <div v-if="loading" class="flex items-center justify-center py-16">
          <div class="h-8 w-8 animate-spin rounded-full border-2 border-gray-200 border-t-primary-600" />
        </div>

        <div v-else-if="rows.length === 0" class="px-6 py-14 text-center">
          <p class="text-sm text-gray-500 dark:text-gray-400">{{ t('billing.noData') }}</p>
        </div>

        <div v-else class="overflow-x-auto">
          <table class="min-w-full divide-y divide-gray-200 text-sm dark:divide-dark-700">
            <thead class="bg-gray-50 dark:bg-dark-700">
              <tr>
                <th class="px-4 py-3 text-left font-medium text-gray-500 dark:text-gray-300">{{ t('billing.model') }}</th>
                <th class="px-4 py-3 text-right font-medium text-gray-500 dark:text-gray-300">{{ t('billing.requests') }}</th>
                <th class="px-4 py-3 text-right font-medium text-gray-500 dark:text-gray-300">{{ t('billing.inputTokens') }}</th>
                <th class="px-4 py-3 text-right font-medium text-gray-500 dark:text-gray-300">{{ t('billing.outputTokens') }}</th>
                <th class="px-4 py-3 text-right font-medium text-gray-500 dark:text-gray-300">{{ t('billing.cacheTokens') }}</th>
                <th class="px-4 py-3 text-right font-medium text-gray-500 dark:text-gray-300">{{ t('billing.totalTokens') }}</th>
                <th class="px-4 py-3 text-right font-medium text-gray-500 dark:text-gray-300">{{ t('billing.cost') }}</th>
              </tr>
            </thead>
            <tbody class="divide-y divide-gray-100 dark:divide-dark-700">
              <tr v-for="row in rows" :key="row.model" class="hover:bg-gray-50 dark:hover:bg-dark-700/50">
                <td class="whitespace-nowrap px-4 py-3 font-mono text-gray-900 dark:text-white">{{ row.model }}</td>
                <td class="px-4 py-3 text-right tabular-nums text-gray-700 dark:text-gray-200">{{ formatNumber(row.requests) }}</td>
                <td class="px-4 py-3 text-right tabular-nums text-gray-700 dark:text-gray-200">{{ formatNumber(row.input_tokens) }}</td>
                <td class="px-4 py-3 text-right tabular-nums text-gray-700 dark:text-gray-200">{{ formatNumber(row.output_tokens) }}</td>
                <td class="px-4 py-3 text-right tabular-nums text-gray-700 dark:text-gray-200">{{ formatNumber(row.cache_tokens) }}</td>
                <td class="px-4 py-3 text-right tabular-nums font-medium text-gray-900 dark:text-white">{{ formatNumber(row.total_tokens) }}</td>
                <td class="px-4 py-3 text-right tabular-nums font-medium text-gray-900 dark:text-white">{{ formatMoney(row.cost) }}</td>
              </tr>
            </tbody>
          </table>
        </div>
      </div>

      <section class="rounded-lg border border-gray-200 bg-white p-4 dark:border-dark-700 dark:bg-dark-800">
        <div class="mb-4">
          <h2 class="text-sm font-semibold text-gray-900 dark:text-white">{{ t('billing.exportTitle') }}</h2>
          <p class="mt-1 text-sm text-gray-500 dark:text-gray-400">{{ t('billing.exportHint') }}</p>
        </div>
        <div class="flex flex-col gap-3 sm:flex-row sm:items-end">
          <label class="flex-1">
            <span class="input-label">{{ t('billing.startDate') }}</span>
            <input
              v-model="exportStart"
              data-testid="billing-export-start"
              type="date"
              class="input"
            />
          </label>
          <label class="flex-1">
            <span class="input-label">{{ t('billing.endDate') }}</span>
            <input
              v-model="exportEnd"
              data-testid="billing-export-end"
              type="date"
              class="input"
            />
          </label>
          <button
            data-testid="billing-export"
            type="button"
            class="btn btn-secondary inline-flex min-w-[132px] items-center justify-center gap-2"
            :disabled="exporting"
            @click="doExport"
          >
            <Icon name="download" size="sm" />
            {{ exporting ? t('billing.exporting') : t('billing.exportCSV') }}
          </button>
        </div>
      </section>
    </div>
  </AppLayout>
</template>

<script setup lang="ts">
import { computed, onMounted, ref } from 'vue'
import { useI18n } from 'vue-i18n'
import billingAPI from '@/api/billing'
import AppLayout from '@/components/layout/AppLayout.vue'
import Icon from '@/components/icons/Icon.vue'
import { useAppStore } from '@/stores/app'
import type { BillingStatementRow } from '@/types'
import { extractApiErrorMessage } from '@/utils/apiError'

const { t } = useI18n()
const appStore = useAppStore()

const now = new Date()
const formatDateInput = (date: Date): string =>
  `${date.getFullYear()}-${String(date.getMonth() + 1).padStart(2, '0')}-${String(date.getDate()).padStart(2, '0')}`

const monthText = ref(`${now.getFullYear()}-${String(now.getMonth() + 1).padStart(2, '0')}`)
const exportStart = ref(`${now.getFullYear()}-${String(now.getMonth() + 1).padStart(2, '0')}-01`)
const exportEnd = ref(formatDateInput(now))
const rows = ref<BillingStatementRow[]>([])
const totalRequests = ref(0)
const totalTokens = ref(0)
const totalCost = ref(0)
const loading = ref(false)
const exporting = ref(false)

const timezone = (): string => {
  try {
    return Intl.DateTimeFormat().resolvedOptions().timeZone || 'UTC'
  } catch {
    return 'UTC'
  }
}

const formatNumber = (value: number): string => new Intl.NumberFormat().format(value)
const formatMoney = (value: number): string => `$${value.toFixed(6)}`

const overviewCards = computed(() => [
  { key: 'requests', label: t('billing.requests'), value: formatNumber(totalRequests.value) },
  { key: 'tokens', label: t('billing.totalTokens'), value: formatNumber(totalTokens.value) },
  { key: 'cost', label: t('billing.totalCost'), value: formatMoney(totalCost.value) },
])

async function loadStatement(): Promise<void> {
  const [year, month] = monthText.value.split('-').map(Number)
  if (!year || !month) return

  loading.value = true
  try {
    const statement = await billingAPI.statement(year, month, timezone())
    rows.value = statement.rows || []
    totalRequests.value = statement.requests || 0
    totalTokens.value = statement.total_tokens || 0
    totalCost.value = statement.cost || 0
  } catch (error: unknown) {
    appStore.showError(extractApiErrorMessage(error, t('billing.loadFailed')))
  } finally {
    loading.value = false
  }
}

function inclusiveDays(start: string, end: string): number {
  const startMs = Date.parse(`${start}T00:00:00Z`)
  const endMs = Date.parse(`${end}T00:00:00Z`)
  if (!Number.isFinite(startMs) || !Number.isFinite(endMs)) return 0
  return Math.floor((endMs - startMs) / 86400000) + 1
}

async function doExport(): Promise<void> {
  const days = inclusiveDays(exportStart.value, exportEnd.value)
  if (days < 1) {
    appStore.showError(t('billing.invalidRange'))
    return
  }
  if (days > 31) {
    appStore.showError(t('billing.rangeTooLong'))
    return
  }

  exporting.value = true
  try {
    const blob = await billingAPI.exportCSV(exportStart.value, exportEnd.value, timezone())
    const url = URL.createObjectURL(blob)
    const link = document.createElement('a')
    link.href = url
    link.download = `billing-${exportStart.value}-to-${exportEnd.value}.csv`
    document.body.appendChild(link)
    link.click()
    link.remove()
    URL.revokeObjectURL(url)
  } catch (error: unknown) {
    appStore.showError(extractApiErrorMessage(error, t('billing.exportFailed')))
  } finally {
    exporting.value = false
  }
}

onMounted(loadStatement)
</script>
