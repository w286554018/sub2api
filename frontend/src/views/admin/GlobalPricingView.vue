<template>
  <AppLayout>
    <div class="space-y-6">
      <div class="flex flex-col gap-4 lg:flex-row lg:items-center lg:justify-between">
        <div>
          <h1 class="text-2xl font-semibold text-gray-900 dark:text-white">{{ t('admin.globalPricing.title') }}</h1>
          <p class="mt-1 max-w-3xl text-sm text-gray-500 dark:text-gray-400">{{ t('admin.globalPricing.description') }}</p>
        </div>
        <div class="flex flex-wrap items-center gap-2">
          <button type="button" class="btn btn-secondary inline-flex items-center gap-2" :disabled="loading" @click="loadItems">
            <Icon name="refresh" size="sm" :class="loading ? 'animate-spin' : ''" />
            {{ t('common.refresh') }}
          </button>
          <button type="button" class="btn btn-primary inline-flex items-center gap-2" @click="openCreate">
            <Icon name="plus" size="sm" />
            {{ t('admin.globalPricing.addPricing') }}
          </button>
        </div>
      </div>

      <div class="rounded-lg border border-blue-100 bg-blue-50 px-4 py-3 text-sm text-blue-800 dark:border-blue-900/50 dark:bg-blue-950/30 dark:text-blue-200">
        {{ t('admin.globalPricing.matchingHint') }}
      </div>

      <div v-if="loading" class="flex items-center justify-center py-16">
        <div class="h-8 w-8 animate-spin rounded-full border-b-2 border-primary-600"></div>
      </div>

      <div v-else-if="items.length === 0" class="rounded-lg border border-dashed border-gray-300 bg-white px-6 py-12 text-center dark:border-dark-600 dark:bg-dark-800">
        <p class="text-sm text-gray-500 dark:text-gray-400">{{ t('admin.globalPricing.noPricing') }}</p>
      </div>

      <div v-else class="overflow-x-auto rounded-lg border border-gray-100 bg-white shadow-sm dark:border-dark-700 dark:bg-dark-800">
        <table class="min-w-full divide-y divide-gray-200 text-sm dark:divide-dark-700">
          <thead class="bg-gray-50 dark:bg-dark-700">
            <tr>
              <th class="px-4 py-3 text-left font-medium text-gray-500 dark:text-gray-300">{{ t('admin.globalPricing.modelPattern') }}</th>
              <th class="px-4 py-3 text-left font-medium text-gray-500 dark:text-gray-300">{{ t('admin.globalPricing.billingMode') }}</th>
              <th class="px-4 py-3 text-left font-medium text-gray-500 dark:text-gray-300">{{ t('admin.globalPricing.priceSummary') }}</th>
              <th class="px-4 py-3 text-center font-medium text-gray-500 dark:text-gray-300">{{ t('admin.globalPricing.enabled') }}</th>
              <th class="px-4 py-3 text-right font-medium text-gray-500 dark:text-gray-300">{{ t('common.actions') }}</th>
            </tr>
          </thead>
          <tbody class="divide-y divide-gray-100 dark:divide-dark-700">
            <tr v-for="item in items" :key="item.id" class="hover:bg-gray-50 dark:hover:bg-dark-700/50">
              <td class="px-4 py-3 font-mono text-gray-900 dark:text-white">{{ item.model_pattern }}</td>
              <td class="px-4 py-3 text-gray-600 dark:text-gray-300">{{ modeLabel(item.billing_mode || 'token') }}</td>
              <td class="px-4 py-3 text-gray-700 dark:text-gray-200">{{ priceSummary(item) }}</td>
              <td class="px-4 py-3 text-center">
                <span :class="pendingToggles.has(item.id) ? 'pointer-events-none opacity-50' : ''">
                  <Toggle :model-value="item.enabled" @update:model-value="(value: boolean) => toggleEnabled(item, value)" />
                </span>
              </td>
              <td class="px-4 py-3 text-right">
                <button type="button" class="btn btn-sm btn-secondary mr-2" @click="openEdit(item)">{{ t('common.edit') }}</button>
                <button type="button" class="btn btn-sm btn-danger" @click="askDelete(item)">{{ t('common.delete') }}</button>
              </td>
            </tr>
          </tbody>
        </table>
      </div>
    </div>

    <BaseDialog :show="showForm" :title="editing ? t('admin.globalPricing.editPricing') : t('admin.globalPricing.addPricing')" width="normal" @close="closeForm">
      <div class="space-y-4">
        <div>
          <label class="input-label">{{ t('admin.globalPricing.modelPattern') }}</label>
          <input v-model="form.model_pattern" type="text" class="input font-mono" :placeholder="t('admin.globalPricing.modelPatternPlaceholder')" />
          <p class="input-hint">{{ t('admin.globalPricing.modelPatternHint') }}</p>
        </div>

        <div>
          <label class="input-label">{{ t('admin.globalPricing.billingMode') }}</label>
          <Select v-model="form.billing_mode" :options="modeOptions" />
        </div>

        <div v-if="form.billing_mode === 'token'" class="grid gap-4 sm:grid-cols-2">
          <div>
            <label class="input-label">{{ t('admin.globalPricing.inputPrice') }}</label>
            <input v-model="priceText.input" type="number" min="0" step="0.000001" class="input" />
          </div>
          <div>
            <label class="input-label">{{ t('admin.globalPricing.outputPrice') }}</label>
            <input v-model="priceText.output" type="number" min="0" step="0.000001" class="input" />
          </div>
          <div>
            <label class="input-label">{{ t('admin.globalPricing.cacheWritePrice') }}</label>
            <input v-model="priceText.cacheWrite" type="number" min="0" step="0.000001" class="input" />
          </div>
          <div>
            <label class="input-label">{{ t('admin.globalPricing.cacheWrite1hPrice') }}</label>
            <input v-model="priceText.cacheWrite1h" type="number" min="0" step="0.000001" class="input" />
          </div>
          <div>
            <label class="input-label">{{ t('admin.globalPricing.cacheReadPrice') }}</label>
            <input v-model="priceText.cacheRead" type="number" min="0" step="0.000001" class="input" />
          </div>
          <p class="sm:col-span-2 input-hint">{{ t('admin.globalPricing.tokenUnitHint') }}</p>
        </div>

        <div v-else>
          <label class="input-label">{{ t('admin.globalPricing.perRequestPrice') }}</label>
          <input v-model="priceText.perRequest" type="number" min="0" step="0.000001" class="input" />
          <p class="input-hint">{{ t('admin.globalPricing.requestUnitHint') }}</p>
        </div>
      </div>

      <template #footer>
        <div class="flex justify-end gap-3">
          <button type="button" class="btn btn-secondary" :disabled="saving" @click="closeForm">{{ t('common.cancel') }}</button>
          <button type="button" class="btn btn-primary" :disabled="saving" @click="saveForm">{{ saving ? t('common.saving') : t('common.save') }}</button>
        </div>
      </template>
    </BaseDialog>

    <ConfirmDialog
      :show="showDelete"
      :title="t('admin.globalPricing.deletePricing')"
      :message="t('admin.globalPricing.deleteConfirm', { pattern: deleting?.model_pattern || '' })"
      :confirm-text="t('common.delete')"
      :cancel-text="t('common.cancel')"
      danger
      @confirm="confirmDelete"
      @cancel="showDelete = false"
    />
  </AppLayout>
</template>

<script setup lang="ts">
import { computed, onMounted, reactive, ref } from 'vue'
import { useI18n } from 'vue-i18n'
import AppLayout from '@/components/layout/AppLayout.vue'
import BaseDialog from '@/components/common/BaseDialog.vue'
import ConfirmDialog from '@/components/common/ConfirmDialog.vue'
import Select from '@/components/common/Select.vue'
import Toggle from '@/components/common/Toggle.vue'
import Icon from '@/components/icons/Icon.vue'
import { useAppStore } from '@/stores/app'
import { extractApiErrorMessage } from '@/utils/apiError'
import { formatScaled } from '@/utils/pricing'
import { mTokToPerToken, perTokenToMTok } from '@/components/admin/channel/types'
import globalPricingAPI, { type GlobalModelPrice, type GlobalModelPriceInput } from '@/api/admin/globalPricing'
import type { BillingMode } from '@/constants/channel'

const { t } = useI18n()
const appStore = useAppStore()

const tokenScale = 1_000_000
const items = ref<GlobalModelPrice[]>([])
const loading = ref(false)
const saving = ref(false)
const showForm = ref(false)
const showDelete = ref(false)
const editing = ref<GlobalModelPrice | null>(null)
const deleting = ref<GlobalModelPrice | null>(null)
const pendingToggles = ref(new Set<number>())

const form = reactive<{ model_pattern: string; billing_mode: BillingMode }>({
  model_pattern: '',
  billing_mode: 'token'
})

const priceText = reactive({
  input: '',
  output: '',
  cacheWrite: '',
  cacheWrite1h: '',
  cacheRead: '',
  perRequest: ''
})

const modeOptions = computed(() => [
  { value: 'token', label: t('admin.globalPricing.modes.token') },
  { value: 'per_request', label: t('admin.globalPricing.modes.perRequest') },
  { value: 'image', label: t('admin.globalPricing.modes.image') },
  { value: 'video', label: t('admin.globalPricing.modes.video') }
])

const modeLabel = (mode: BillingMode) => modeOptions.value.find(option => option.value === mode)?.label || mode

const formatToken = (value?: number | null) => formatScaled(value ?? null, tokenScale)
const formatRequest = (value?: number | null) => formatScaled(value ?? null, 1)

const priceSummary = (item: GlobalModelPrice) => {
  if ((item.billing_mode || 'token') !== 'token') {
    return t('admin.globalPricing.summary.perRequest', { price: formatRequest(item.per_request_price) })
  }
  const parts = [
    t('admin.globalPricing.summary.input', { price: formatToken(item.input_price) }),
    t('admin.globalPricing.summary.output', { price: formatToken(item.output_price) })
  ]
  if (item.cache_write_price != null) parts.push(t('admin.globalPricing.summary.cacheWrite', { price: formatToken(item.cache_write_price) }))
  if (item.cache_write_1h_price != null) parts.push(t('admin.globalPricing.summary.cacheWrite1h', { price: formatToken(item.cache_write_1h_price) }))
  if (item.cache_read_price != null) parts.push(t('admin.globalPricing.summary.cacheRead', { price: formatToken(item.cache_read_price) }))
  return parts.join(' / ')
}

const parsePrice = (value: string | number): { ok: boolean; value: number | null } => {
  if (String(value).trim() === '') return { ok: true, value: null }
  const parsed = Number(value)
  if (!Number.isFinite(parsed) || parsed < 0) return { ok: false, value: null }
  return { ok: true, value: parsed }
}

const parsePerToken = (value: string) => {
  const parsed = parsePrice(value)
  if (!parsed.ok || parsed.value === null) return parsed
  return { ok: true, value: mTokToPerToken(parsed.value) }
}

const displayPerToken = (value?: number | null) => {
  const display = perTokenToMTok(value ?? null)
  return display == null ? '' : String(display)
}

const loadItems = async () => {
  loading.value = true
  try {
    const result = await globalPricingAPI.list()
    items.value = result.items || []
  } catch (error) {
    appStore.showError(extractApiErrorMessage(error, t('admin.globalPricing.loadFailed')))
  } finally {
    loading.value = false
  }
}

const resetForm = () => {
  form.model_pattern = ''
  form.billing_mode = 'token'
  priceText.input = ''
  priceText.output = ''
  priceText.cacheWrite = ''
  priceText.cacheWrite1h = ''
  priceText.cacheRead = ''
  priceText.perRequest = ''
}

const openCreate = () => {
  editing.value = null
  resetForm()
  showForm.value = true
}

const openEdit = (item: GlobalModelPrice) => {
  editing.value = item
  form.model_pattern = item.model_pattern
  form.billing_mode = item.billing_mode || 'token'
  priceText.input = displayPerToken(item.input_price)
  priceText.output = displayPerToken(item.output_price)
  priceText.cacheWrite = displayPerToken(item.cache_write_price)
  priceText.cacheWrite1h = displayPerToken(item.cache_write_1h_price)
  priceText.cacheRead = displayPerToken(item.cache_read_price)
  priceText.perRequest = item.per_request_price == null ? '' : String(item.per_request_price)
  showForm.value = true
}

const closeForm = () => {
  showForm.value = false
}

const buildPayload = (): GlobalModelPriceInput | null => {
  if (!form.model_pattern.trim()) {
    appStore.showError(t('admin.globalPricing.patternRequired'))
    return null
  }
  const input = parsePerToken(priceText.input)
  const output = parsePerToken(priceText.output)
  const cacheWrite = parsePerToken(priceText.cacheWrite)
  const cacheWrite1h = parsePerToken(priceText.cacheWrite1h)
  const cacheRead = parsePerToken(priceText.cacheRead)
  const perRequest = parsePrice(priceText.perRequest)
  if (![input, output, cacheWrite, cacheWrite1h, cacheRead, perRequest].every(result => result.ok)) {
    appStore.showError(t('admin.globalPricing.invalidPrice'))
    return null
  }
  if (form.billing_mode === 'token') {
    if (input.value == null || output.value == null) {
      appStore.showError(t('admin.globalPricing.tokenPriceRequired'))
      return null
    }
    return {
      model_pattern: form.model_pattern.trim(),
      billing_mode: form.billing_mode,
      input_price: input.value,
      output_price: output.value,
      cache_write_price: cacheWrite.value,
      cache_write_1h_price: cacheWrite1h.value,
      cache_read_price: cacheRead.value,
      per_request_price: null
    }
  }
  if (perRequest.value == null) {
    appStore.showError(t('admin.globalPricing.requestPriceRequired'))
    return null
  }
  return {
    model_pattern: form.model_pattern.trim(),
    billing_mode: form.billing_mode,
    input_price: null,
    output_price: null,
    cache_write_price: null,
    cache_write_1h_price: null,
    cache_read_price: null,
    per_request_price: perRequest.value
  }
}

const saveForm = async () => {
  const payload = buildPayload()
  if (!payload) return
  saving.value = true
  try {
    if (editing.value) {
      await globalPricingAPI.update(editing.value.id, payload)
      appStore.showSuccess(t('admin.globalPricing.updateSuccess'))
    } else {
      await globalPricingAPI.create(payload)
      appStore.showSuccess(t('admin.globalPricing.createSuccess'))
    }
    closeForm()
    await loadItems()
  } catch (error) {
    appStore.showError(extractApiErrorMessage(error, t('admin.globalPricing.saveFailed')))
  } finally {
    saving.value = false
  }
}

const toggleEnabled = async (item: GlobalModelPrice, enabled: boolean) => {
  if (pendingToggles.value.has(item.id)) return
  pendingToggles.value.add(item.id)
  try {
    const updated = await globalPricingAPI.setEnabled(item.id, enabled)
    const index = items.value.findIndex(entry => entry.id === item.id)
    if (index >= 0) items.value[index] = updated
  } catch (error) {
    appStore.showError(extractApiErrorMessage(error, t('admin.globalPricing.saveFailed')))
  } finally {
    pendingToggles.value.delete(item.id)
  }
}

const askDelete = (item: GlobalModelPrice) => {
  deleting.value = item
  showDelete.value = true
}

const confirmDelete = async () => {
  if (!deleting.value) return
  saving.value = true
  try {
    await globalPricingAPI.remove(deleting.value.id)
    appStore.showSuccess(t('admin.globalPricing.deleteSuccess'))
    showDelete.value = false
    deleting.value = null
    await loadItems()
  } catch (error) {
    appStore.showError(extractApiErrorMessage(error, t('admin.globalPricing.deleteFailed')))
  } finally {
    saving.value = false
  }
}

onMounted(() => {
  void loadItems()
})
</script>
