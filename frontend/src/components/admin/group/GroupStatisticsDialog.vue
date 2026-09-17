<template>
  <BaseDialog
    :show="show"
    :title="dialogTitle"
    width="wide"
    @close="emit('close')"
  >
    <div class="space-y-5">
      <p class="text-sm text-gray-500 dark:text-gray-400">
        {{ t("admin.groups.stats.description") }}
      </p>

      <div class="grid gap-3 sm:grid-cols-[1fr_1fr_auto_auto] sm:items-end">
        <label class="space-y-1 text-sm font-medium text-gray-700 dark:text-gray-300">
          <span>{{ t("admin.groups.stats.from") }}</span>
          <input v-model="from" type="datetime-local" class="input" />
        </label>
        <label class="space-y-1 text-sm font-medium text-gray-700 dark:text-gray-300">
          <span>{{ t("admin.groups.stats.to") }}</span>
          <input v-model="to" type="datetime-local" class="input" />
        </label>
        <button type="button" class="btn btn-secondary" :disabled="loading" @click="load">
          <Icon name="refresh" size="sm" :class="loading ? 'animate-spin' : ''" />
          {{ t("admin.groups.stats.refresh") }}
        </button>
        <button type="button" class="btn btn-secondary" :disabled="loading" @click="resetRange">
          {{ t("admin.groups.stats.allTime") }}
        </button>
      </div>

      <p v-if="error" role="alert" class="rounded-lg bg-red-50 px-3 py-2 text-sm text-red-700 dark:bg-red-900/20 dark:text-red-300">
        {{ error }}
      </p>
      <p v-if="loading" role="status" class="py-8 text-center text-sm text-gray-500 dark:text-gray-400">
        {{ t("admin.groups.stats.loading") }}
      </p>

      <div v-if="stats && !loading" class="space-y-4">
        <div class="grid gap-3 md:grid-cols-3">
          <div v-for="item in amountCards" :key="item.key" class="rounded-lg bg-gray-50 p-4 dark:bg-dark-800">
            <p class="text-xs text-gray-500 dark:text-gray-400">{{ item.label }}</p>
            <p class="mt-2 break-all text-lg font-semibold tabular-nums text-gray-900 dark:text-white">
              ${{ formatMoney(item.value) }}
            </p>
            <p class="mt-2 text-xs text-gray-500 dark:text-gray-400">{{ item.note }}</p>
          </div>
        </div>

        <dl class="grid gap-3 rounded-lg border border-gray-200 p-4 text-sm dark:border-dark-700 sm:grid-cols-2 lg:grid-cols-3">
          <div v-for="item in metricItems" :key="item.key">
            <dt class="text-xs text-gray-500 dark:text-gray-400">{{ item.label }}</dt>
            <dd class="mt-1 font-medium tabular-nums text-gray-900 dark:text-white">{{ item.value }}</dd>
          </div>
        </dl>

        <p v-if="stats.total_requests === 0" class="text-sm text-gray-500 dark:text-gray-400">
          {{ t("admin.groups.stats.empty") }}
        </p>
        <p class="text-xs text-gray-500 dark:text-gray-400">
          {{ rangeSummary }} · {{ t("admin.groups.stats.generatedAt", { time: formatDateTime(stats.generated_at) }) }}
        </p>
      </div>
    </div>

    <template #footer>
      <button type="button" class="btn btn-secondary" @click="emit('close')">
        {{ t("common.close") }}
      </button>
    </template>
  </BaseDialog>
</template>

<script setup lang="ts">
import { computed, onUnmounted, ref, watch } from "vue";
import { useI18n } from "vue-i18n";
import BaseDialog from "@/components/common/BaseDialog.vue";
import Icon from "@/components/icons/Icon.vue";
import { getStats, type GroupDetailStats } from "@/api/admin/groups";
import type { AdminGroup } from "@/types";
import { extractApiErrorMessage } from "@/utils/apiError";

const props = defineProps<{
  show: boolean;
  group: AdminGroup | null;
}>();

const emit = defineEmits<{
  close: [];
}>();

const { t } = useI18n();

const stats = ref<GroupDetailStats | null>(null);
const loading = ref(false);
const error = ref("");
const from = ref("");
const to = ref("");

let controller: AbortController | null = null;
let requestVersion = 0;

const dialogTitle = computed(() =>
  stats.value?.group_name || props.group?.name
    ? t("admin.groups.stats.titleWithName", { name: stats.value?.group_name || props.group?.name })
    : t("admin.groups.stats.title"),
);

const amountCards = computed(() => {
  if (!stats.value) return [];
  return [
    {
      key: "actual",
      label: t("admin.groups.stats.totalActualCost"),
      value: stats.value.total_actual_cost,
      note: t("admin.groups.stats.totalActualCostNote"),
    },
    {
      key: "book",
      label: t("admin.groups.stats.totalCost"),
      value: stats.value.total_cost,
      note: t("admin.groups.stats.totalCostNote"),
    },
    {
      key: "account",
      label: t("admin.groups.stats.totalAccountCost"),
      value: stats.value.total_account_cost,
      note: t("admin.groups.stats.totalAccountCostNote"),
    },
  ];
});

const metricItems = computed(() => {
  if (!stats.value) return [];
  return [
    { key: "requests", label: t("admin.groups.stats.totalRequests"), value: formatInteger(stats.value.total_requests) },
    { key: "tokens", label: t("admin.groups.stats.totalTokens"), value: formatInteger(stats.value.total_tokens) },
    { key: "duration", label: t("admin.groups.stats.averageDuration"), value: t("admin.groups.stats.durationMs", { value: formatNumber(stats.value.average_duration_ms, 2) }) },
    { key: "keys", label: t("admin.groups.stats.totalApiKeys"), value: formatInteger(stats.value.total_api_keys) },
    { key: "activeKeys", label: t("admin.groups.stats.activeApiKeys"), value: formatInteger(stats.value.active_api_keys) },
    { key: "accounts", label: t("admin.groups.stats.totalAccounts"), value: formatInteger(stats.value.total_accounts) },
    { key: "balance", label: t("admin.groups.stats.balanceCost"), value: `$${formatMoney(stats.value.balance_cost)}` },
    { key: "subscription", label: t("admin.groups.stats.subscriptionCost"), value: `$${formatMoney(stats.value.subscription_cost)}` },
    { key: "zero", label: t("admin.groups.stats.zeroChargeRequests"), value: formatInteger(stats.value.zero_charge_requests) },
  ];
});

const rangeSummary = computed(() => {
  if (!stats.value) return "";
  if (!stats.value.from && !stats.value.to) return t("admin.groups.stats.allHistory");
  return t("admin.groups.stats.rangeApplied");
});

function formatInteger(value: number) {
  return Math.round(value || 0).toLocaleString();
}

function formatNumber(value: number, digits: number) {
  return (value || 0).toLocaleString(undefined, {
    minimumFractionDigits: 0,
    maximumFractionDigits: digits,
  });
}

function formatMoney(value: number) {
  return (value || 0).toLocaleString("en-US", {
    minimumFractionDigits: 2,
    maximumFractionDigits: 8,
  });
}

function formatDateTime(value: string) {
  const date = new Date(value);
  return Number.isFinite(date.getTime()) ? date.toLocaleString() : value;
}

function toRFC3339(value: string, label: string) {
  if (!value) return undefined;
  const date = new Date(value);
  if (!Number.isFinite(date.getTime())) {
    throw new Error(t("admin.groups.stats.invalidTime", { label }));
  }
  return date.toISOString();
}

async function load() {
  if (!props.show || !props.group) return;
  let params: { from?: string; to?: string };
  try {
    params = {
      from: toRFC3339(from.value, t("admin.groups.stats.from")),
      to: toRFC3339(to.value, t("admin.groups.stats.to")),
    };
    if (params.from && params.to && params.from >= params.to) {
      throw new Error(t("admin.groups.stats.invalidRange"));
    }
  } catch (err) {
    error.value = err instanceof Error ? err.message : t("admin.groups.stats.invalidRange");
    return;
  }

  const currentVersion = ++requestVersion;
  controller?.abort();
  controller = new AbortController();
  loading.value = true;
  error.value = "";
  try {
    const result = await getStats(props.group.id, params, controller.signal);
    if (currentVersion === requestVersion) stats.value = result;
  } catch (err) {
    if (currentVersion === requestVersion && !controller.signal.aborted) {
      stats.value = null;
      error.value = extractApiErrorMessage(err, t("admin.groups.stats.loadFailed"));
    }
  } finally {
    if (currentVersion === requestVersion) loading.value = false;
  }
}

function resetRange() {
  from.value = "";
  to.value = "";
  void load();
}

watch(
  () => [props.show, props.group?.id] as const,
  ([show]) => {
    requestVersion++;
    controller?.abort();
    stats.value = null;
    error.value = "";
    from.value = "";
    to.value = "";
    if (show) void load();
  },
  { immediate: true },
);

onUnmounted(() => {
  requestVersion++;
  controller?.abort();
});
</script>
