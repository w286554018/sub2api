<template>
  <Teleport to="body">
    <div v-if="show" class="fixed inset-0 z-[10000] flex items-center justify-center bg-black/40 p-4" @click.self="emit('close')">
      <div class="w-full max-w-3xl rounded-lg bg-white shadow-xl dark:bg-dark-800">
        <div class="flex items-center justify-between border-b border-gray-200 px-5 py-4 dark:border-dark-700">
          <div>
            <h3 class="text-lg font-semibold text-gray-900 dark:text-white">{{ t('admin.accounts.runtime.title') }}</h3>
            <p class="mt-1 text-sm text-gray-500 dark:text-gray-400">{{ account?.name }}</p>
          </div>
          <div class="flex items-center gap-2">
            <button class="btn btn-secondary btn-sm" :disabled="loading" @click="load">
              <Icon name="refresh" size="sm" :class="loading ? 'animate-spin' : ''" />
              {{ t('common.refresh') }}
            </button>
            <button class="rounded-md p-2 text-gray-500 hover:bg-gray-100 dark:hover:bg-dark-700" @click="emit('close')">
              <Icon name="x" size="sm" />
            </button>
          </div>
        </div>

        <div class="max-h-[70vh] overflow-y-auto p-5">
          <div v-if="loading" class="py-8 text-center text-sm text-gray-500">{{ t('common.loading') }}</div>
          <div v-else-if="error" class="rounded-md border border-red-200 bg-red-50 p-3 text-sm text-red-700 dark:border-red-800 dark:bg-red-900/20 dark:text-red-300">
            {{ error }}
          </div>
          <div v-else-if="snapshot" class="space-y-5">
            <section class="grid gap-3 sm:grid-cols-2">
              <RuntimeItem :label="t('admin.accounts.runtime.authType')" :value="snapshot.auth_type" />
              <RuntimeItem :label="t('admin.accounts.runtime.revision')" :value="formatDateTime(snapshot.account_revision)" />
              <RuntimeItem :label="t('admin.accounts.runtime.source')" :value="snapshot.source" />
              <RuntimeItem :label="t('admin.accounts.runtime.observed')" :value="formatBool(snapshot.observed)" />
            </section>
            <section>
              <h4 class="mb-3 text-sm font-semibold text-gray-800 dark:text-gray-100">{{ t('admin.accounts.runtime.effective') }}</h4>
              <div class="grid gap-3 sm:grid-cols-2">
                <RuntimeItem :label="t('admin.accounts.runtime.transport')" :value="snapshot.effective.transport" />
                <RuntimeItem :label="t('admin.accounts.runtime.reason')" :value="snapshot.effective.transport_reason" />
                <RuntimeItem :label="t('admin.accounts.runtime.plugin')" :value="formatBool(snapshot.effective.plugin_routed)" />
                <RuntimeItem :label="t('admin.accounts.runtime.fingerprint')" :value="snapshot.effective.fingerprint_mode" />
                <RuntimeItem :label="t('admin.accounts.runtime.convergence')" :value="formatBool(snapshot.effective.fingerprint_convergence)" />
                <RuntimeItem :label="t('admin.accounts.runtime.proxy')" :value="proxyText" />
                <RuntimeItem :label="t('admin.accounts.runtime.concurrency')" :value="String(snapshot.effective.concurrency)" />
                <RuntimeItem :label="t('admin.accounts.runtime.loadFactor')" :value="String(snapshot.effective.load_factor)" />
              </div>
            </section>
            <section>
              <h4 class="mb-3 text-sm font-semibold text-gray-800 dark:text-gray-100">{{ t('admin.accounts.runtime.configured') }}</h4>
              <div class="grid gap-3 sm:grid-cols-2">
                <RuntimeItem :label="t('admin.accounts.runtime.wsMode')" :value="snapshot.configured.websocket_mode" />
                <RuntimeItem :label="t('admin.accounts.runtime.passthrough')" :value="formatBool(snapshot.configured.passthrough)" />
                <RuntimeItem :label="t('admin.accounts.runtime.forceHttp')" :value="formatBool(snapshot.configured.force_http)" />
                <RuntimeItem :label="t('admin.accounts.runtime.loadFactor')" :value="snapshot.configured.load_factor == null ? '-' : String(snapshot.configured.load_factor)" />
              </div>
            </section>
          </div>
        </div>
      </div>
    </div>
  </Teleport>
</template>

<script setup lang="ts">
import { computed, defineComponent, h, onUnmounted, ref, watch } from 'vue'
import { useI18n } from 'vue-i18n'
import { Icon } from '@/components/icons'
import { adminAPI } from '@/api'
import { extractApiErrorMessage } from '@/utils/apiError'
import { formatDateTime } from '@/utils/format'
import type { Account, OpenAIAccountRuntimeSnapshot } from '@/types'

const RuntimeItem = defineComponent({
  props: { label: { type: String, required: true }, value: { type: String, required: true } },
  setup(props) {
    return () => h('div', { class: 'rounded-md border border-gray-200 p-3 dark:border-dark-700' }, [
      h('div', { class: 'text-xs text-gray-500 dark:text-gray-400' }, props.label),
      h('div', { class: 'mt-1 break-words text-sm font-medium text-gray-900 dark:text-gray-100' }, props.value || '-')
    ])
  }
})

const props = defineProps<{ show: boolean; account: Account | null }>()
const emit = defineEmits(['close'])
const { t } = useI18n()
const loading = ref(false)
const error = ref('')
const snapshot = ref<OpenAIAccountRuntimeSnapshot | null>(null)
let controller: AbortController | null = null

const formatBool = (value: boolean) => value ? t('common.yes') : t('common.no')
const proxyText = computed(() => {
  if (!snapshot.value) return '-'
  const proxyID = snapshot.value.effective.proxy_id
  return proxyID ? `${snapshot.value.effective.proxy_mode} #${proxyID}` : snapshot.value.effective.proxy_mode
})

async function load() {
  if (!props.account) return
  controller?.abort()
  controller = new AbortController()
  loading.value = true
  error.value = ''
  try {
    snapshot.value = await adminAPI.accounts.getRuntime(props.account.id, { signal: controller.signal })
  } catch (err) {
    if ((err as { code?: string }).code === 'ERR_CANCELED') return
    error.value = extractApiErrorMessage(err, t('admin.accounts.runtime.loadFailed'))
  } finally {
    loading.value = false
  }
}

watch(() => [props.show, props.account?.id], ([visible]) => {
  if (visible) load()
  else controller?.abort()
})

onUnmounted(() => controller?.abort())
</script>
