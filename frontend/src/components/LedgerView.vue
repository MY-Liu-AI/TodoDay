<script setup lang="ts">
/* 记账本页：月份切换 + 支出/收入/结余三卡 + 按天分组的流水 + FAB 记一笔。
 * 数据全部来自 store（st.txs 是当前展示月），三卡由列表推导、恒同口径。
 * 页面挂载期间 N 键唤起记一笔（组件级监听，切走即失效）。 */
import { computed, onMounted, onUnmounted, ref, watch } from "vue";
import { C } from "../copy";
import { st, loadLedger, removeTx } from "../store";
import type { WireTx } from "../api";
import { TODAY } from "../lib/todo.js";
import { catColor, fmtDayHead, fmtMoney, fmtMonthTitle, groupTxByDay, monthKeyOf, monthTotals } from "../lib/ledger.js";
import Icon from "./Icon.vue";
import LedgerSheet from "./LedgerSheet.vue";

const firstOfThisMonth = () => new Date(new Date(TODAY).getFullYear(), new Date(TODAY).getMonth(), 1);
const month = ref(firstOfThisMonth());
const sheetOpen = ref(false);
const editing = ref<WireTx | null>(null);

/* 切月即拉取（含首次进入）；force=false 时同月不重复请求。 */
watch(month, (m) => loadLedger(monthKeyOf(m)), { immediate: true });

const monthTitle = computed(() => fmtMonthTitle(monthKeyOf(month.value)));
const groups = computed(() => groupTxByDay(st.txs));
const totals = computed(() => monthTotals(st.txs));

function shiftMonth(n: number) {
  month.value = new Date(month.value.getFullYear(), month.value.getMonth() + n, 1);
}
function goThisMonth() {
  month.value = firstOfThisMonth();
}
/* 读取失败重试：按当前展示月强制重拉（store 的 retryLoad 只重拉任务）。 */
function retryLedger() {
  return loadLedger(monthKeyOf(month.value), true);
}
function openNew() {
  editing.value = null;
  sheetOpen.value = true;
}
function openEdit(tx: WireTx) {
  editing.value = tx;
  sheetOpen.value = true;
}

function onKey(e: KeyboardEvent) {
  const inEditable = (e.target as HTMLElement).closest?.("input, textarea, select");
  if (inEditable || sheetOpen.value) return;
  if (e.key === "n" || e.key === "N") {
    e.preventDefault();
    openNew();
  }
}
onMounted(() => window.addEventListener("keydown", onKey));
onUnmounted(() => window.removeEventListener("keydown", onKey));

/* 组头的当日小计文案：「支 ¥44.5 · 收 ¥100」，缺哪边显示哪边。 */
function dayNetText(g: { exp: number; inc: number }): string {
  const parts: string[] = [];
  if (g.exp) parts.push(`支 ${fmtMoney(g.exp)}`);
  if (g.inc) parts.push(`收 ${fmtMoney(g.inc)}`);
  return parts.join(" · ");
}
</script>

<template>
  <section class="ledger-view" aria-label="ledger">
    <div class="calendar-toolbar">
      <h2>{{ monthTitle }}</h2>
      <div class="month-actions">
        <button class="icon-button prev-arrow" :aria-label="C.prevMonth" @click="shiftMonth(-1)">
          <Icon name="arrow" />
        </button>
        <button class="secondary-button" @click="goThisMonth">{{ C.today }}</button>
        <button class="icon-button" :aria-label="C.nextMonth" @click="shiftMonth(1)">
          <Icon name="arrow" />
        </button>
      </div>
    </div>

    <div class="ledger-stats">
      <div class="goal-stat">
        <b class="is-neg">-{{ fmtMoney(totals.exp) }}</b>
        <span>{{ C.ledgerExp }}</span>
      </div>
      <div class="goal-stat">
        <b class="is-pos">+{{ fmtMoney(totals.inc) }}</b>
        <span>{{ C.ledgerInc }}</span>
      </div>
      <div class="goal-stat">
        <b :class="totals.net < 0 ? 'is-neg' : 'is-pos'">
          {{ totals.net < 0 ? "-" : "" }}{{ fmtMoney(Math.abs(totals.net)) }}
        </b>
        <span>{{ C.ledgerNet }}</span>
      </div>
    </div>

    <div class="ledger-listhead">
      <h3>{{ C.ledgerFlow }}</h3>
      <span class="ledger-tip">{{ C.ledgerFlowHint }}</span>
    </div>

    <p v-if="st.ledgerLoadError" role="alert" class="storage-warning">
      {{ st.ledgerLoadError }}
      <button class="text-button" @click="retryLedger">{{ C.retry }}</button>
    </p>
    <div v-else-if="st.ledgerLoading && !st.txs.length" class="ledger-empty">{{ C.syncing }}</div>
    <div v-else-if="!groups.length" class="ledger-empty">{{ C.ledgerEmpty }}</div>

    <template v-else>
      <div v-for="g in groups" :key="g.date" class="ledger-day">
        <div class="ledger-dayhead">
          <span>{{ fmtDayHead(g.date) }}</span>
          <span class="ledger-daynet">{{ dayNetText(g) }}</span>
        </div>
        <div v-for="tx in g.items" :key="tx.id" class="ledger-row" :class="{ 'is-temp': !tx.id }">
          <span class="ledger-dot" :data-color="tx.type === 'exp' ? catColor(tx.category) : 'teal'" />
          <span class="ledger-cat">{{ tx.type === "exp" ? tx.category : C.ledgerInc }}</span>
          <span class="ledger-note">{{ tx.note }}</span>
          <span class="ledger-amt" :class="tx.type === 'exp' ? 'is-neg' : 'is-pos'">
            {{ tx.type === "exp" ? "-" : "+" }}{{ fmtMoney(tx.amountFen) }}
          </span>
          <span class="ledger-ops">
            <button class="icon-button" :aria-label="C.save" @click="openEdit(tx)">
              <Icon name="edit" :size="14" />
            </button>
            <button class="icon-button" :aria-label="C.close" @click="removeTx(tx)">
              <Icon name="close" :size="14" />
            </button>
          </span>
        </div>
      </div>
    </template>

    <button class="ledger-fab" @click="openNew">
      <Icon name="plus" />
      {{ C.ledgerAdd }}
    </button>

    <LedgerSheet v-if="sheetOpen" :tx="editing" @close="sheetOpen = false" />
  </section>
</template>
