<script setup lang="ts">
/* 记账本页：账本总览（流水分析 + 流水）/ 分类预算 两个视图，
 * 与目标页同一套 view-switch 分段切换（纯组件内状态、不持久化）。
 * 流水分析 = 与上月对比（支出/收入/结余 + 环比）+ 分类支出排行（占比条形），
 * 全部由 st.txs 推导、与流水列表恒同口径；上月汇总单独拉 stats，失败降级。
 * 分类预算是长期月度预算（每分类一条，设置一次每月沿用），按当前展示月
 * 的花费考核进度；花费同样从 st.txs 推导。页面挂载期间 N 键唤起记一笔。 */
import { computed, onMounted, onUnmounted, ref, watch } from "vue";
import { C } from "../copy";
import { st, loadLedger, removeTx, loadBudgets, setBudget, removeBudget, fetchMonthStats } from "../store";
import type { WireTx } from "../api";
import { TODAY } from "../lib/todo.js";
import {
  LEDGER_CATS,
  catColor,
  catExpRank,
  fmtDayHead,
  fmtMoney,
  fmtMonthTitle,
  groupTxByDay,
  momPct,
  monthKeyOf,
  monthTotals,
  prevMonthKey,
  spendByCategory,
} from "../lib/ledger.js";
import Icon from "./Icon.vue";
import LedgerSheet from "./LedgerSheet.vue";

const firstOfThisMonth = () => new Date(new Date(TODAY).getFullYear(), new Date(TODAY).getMonth(), 1);
const view = ref<"overview" | "budget">("overview");
const month = ref(firstOfThisMonth());
const sheetOpen = ref(false);
const editing = ref<WireTx | null>(null);

/* 切月即拉流水（含首次进入）；force=false 时同月不重复请求。 */
watch(month, (m) => loadLedger(monthKeyOf(m)), { immediate: true });
/* 预算列表长期有效，不随月份重拉；进预算视图时按需加载。 */
watch(view, (v) => {
  if (v === "budget") loadBudgets();
});

/* 上月汇总（与上月对比用）：切月重拉，seq 防竞态；失败留 null——
 * 分析区降级为只显示当月数、环比显示「—」。 */
const prevStats = ref<{ exp: number; inc: number; net: number } | null>(null);
let prevSeq = 0;
watch(
  month,
  async (m) => {
    const seq = ++prevSeq;
    const stats = await fetchMonthStats(prevMonthKey(monthKeyOf(m)));
    if (seq === prevSeq) prevStats.value = stats;
  },
  { immediate: true },
);

const monthTitle = computed(() => fmtMonthTitle(monthKeyOf(month.value)));
const groups = computed(() => groupTxByDay(st.txs));
const totals = computed(() => monthTotals(st.txs));
const rank = computed(() => catExpRank(st.txs));
const spend = computed(() => spendByCategory(st.txs));

/* 环比文案：上月无基数时当月有数显示「新增」、否则「—」。 */
function momLabel(cur: number, prev: number): string {
  if (prev <= 0) return cur > 0 ? C.ledgerMomNew : "—";
  const p = momPct(cur, prev)!;
  return (p > 0 ? "↑" : p < 0 ? "↓" : "") + Math.abs(p) + "%";
}

/* 与上月对比三卡：沿用原三卡的数值口径（负号/正号着色），
 * 每卡多一行环比。上月汇总未就绪时环比显示「—」。 */
const momCards = computed(() => {
  const t = totals.value;
  const raw = [
    { label: C.ledgerExp, value: "-" + fmtMoney(t.exp), cls: "is-neg", cur: t.exp, prev: prevStats.value?.exp },
    { label: C.ledgerInc, value: "+" + fmtMoney(t.inc), cls: "is-pos", cur: t.inc, prev: prevStats.value?.inc },
    {
      label: C.ledgerNet,
      value: (t.net < 0 ? "-" : "") + fmtMoney(Math.abs(t.net)),
      cls: t.net < 0 ? "is-neg" : "is-pos",
      cur: t.net,
      prev: prevStats.value?.net,
    },
  ];
  return raw.map((c) => ({ ...c, delta: prevStats.value ? momLabel(c.cur, c.prev ?? 0) : "—" }));
});

/* ── 分类预算 ─────────────────────────────────────────────────────────── */

/* 预算行 = 预算 + 当月已花（同一份 st.txs 推导）。 */
const budgetRows = computed(() =>
  st.budgets.map((b) => ({ ...b, spent: spend.value[b.category] || 0 })),
);

/* 当月有支出但没有预算的分类：给「设预算」入口，发现遗漏。 */
const unbudgeted = computed(() => {
  const has = new Set(st.budgets.map((b) => b.category));
  return Object.entries(spend.value)
    .filter(([cat]) => !has.has(cat))
    .map(([category, amountFen]) => ({ category, amountFen }))
    .sort((a, b) => b.amountFen - a.amountFen);
});

/* 分类输入建议：六分类在前，流水里出现过的其他分类殿后；已设预算的
 * 不再建议（改预算走 ✎）。 */
const catSuggestions = computed(() => {
  const fixed = LEDGER_CATS.map((c) => c.name);
  const observed = Object.keys(spend.value).filter((c) => !fixed.includes(c));
  const budgeted = new Set(st.budgets.map((b) => b.category));
  return [...fixed, ...observed].filter((c) => !budgeted.has(c));
});

/* 添加/编辑共用一张内联表单：分类是预算的幂等键，编辑时锁定分类
 * （改名 = 删除重建，避免「改了名其实换了条预算」的错觉）。 */
const budgetForm = ref<{ mode: "add" | "edit"; category: string; amount: string } | null>(null);
const budgetError = ref("");

function openBudgetForm() {
  budgetForm.value = { mode: "add", category: "", amount: "" };
  budgetError.value = "";
}
function editBudget(b: { category: string; amountFen: number }) {
  budgetForm.value = { mode: "edit", category: b.category, amount: String(b.amountFen / 100) };
  budgetError.value = "";
}
function prefillBudget(category: string) {
  budgetForm.value = { mode: "add", category, amount: "" };
  budgetError.value = "";
}

async function submitBudget() {
  const form = budgetForm.value;
  if (!form) return;
  const category = form.category.trim();
  const fen = Math.round(parseFloat(form.amount || "0") * 100);
  if (!category) {
    budgetError.value = C.ledgerBudgetCategoryError;
    return;
  }
  if (!(fen > 0)) {
    budgetError.value = C.ledgerBudgetAmountError;
    return;
  }
  budgetError.value = "";
  if (await setBudget(category, fen)) budgetForm.value = null;
}

/* 预算进度口径：超过 100% 即超支；进度条封顶 100%，用颜色区分。 */
function budgetPct(b: { spent: number; amountFen: number }): number {
  return Math.round((b.spent / b.amountFen) * 100);
}
function budgetRemainText(b: { spent: number; amountFen: number }): string {
  const left = b.amountFen - b.spent;
  return left >= 0
    ? C.ledgerBudgetLeft.replace("{amt}", fmtMoney(left))
    : C.ledgerBudgetOver.replace("{amt}", fmtMoney(-left));
}

/* ── 通用 ─────────────────────────────────────────────────────────────── */

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
    <!-- 视图切换与目标页同款分段控件；主操作随视图变化：总览记一笔、预算加预算。 -->
    <div class="workspace-toolbar">
      <div class="view-switch" aria-label="ledger">
        <button
          v-for="[v, label, icon] of [
            ['overview', C.ledgerViewOverview, 'ledger'],
            ['budget', C.ledgerViewBudget, 'stats'],
          ] as const"
          :key="v"
          :aria-pressed="view === v"
          :class="view === v ? 'is-active' : ''"
          @click="view = v"
        >
          <Icon :name="icon" />
          {{ label }}
        </button>
      </div>
      <button class="primary-button workspace-cta" @click="view === 'overview' ? openNew() : openBudgetForm()">
        <Icon name="plus" />
        {{ view === "overview" ? C.ledgerAdd : C.ledgerBudgetAdd }}
      </button>
    </div>

    <!-- 月份对两个视图同义：总览的流水与预算的当月花费都按它考核。 -->
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

    <!-- ── 账本总览：流水分析 + 流水 ── -->
    <template v-if="view === 'overview'">
      <section class="ledger-analysis" :aria-label="C.ledgerAnalysis">
        <div class="ledger-mom">
          <div v-for="card in momCards" :key="card.label" class="ledger-mom-card">
            <span class="ledger-mom-label">{{ card.label }}</span>
            <b :class="card.cls">{{ card.value }}</b>
            <em>{{ C.ledgerMomVs }} {{ card.delta }}</em>
          </div>
        </div>

        <div class="ledger-listhead">
          <h3>{{ C.ledgerCatRank }}</h3>
          <span class="ledger-tip">{{ C.ledgerCatRankTip }}</span>
        </div>
        <p v-if="!rank.length" class="ledger-rank-empty">{{ C.ledgerCatRankEmpty }}</p>
        <div v-else class="ledger-rank">
          <div v-for="r in rank" :key="r.category" class="ledger-rank-row">
            <span class="ledger-dot" :data-color="catColor(r.category)" />
            <span class="ledger-rank-cat">{{ r.category }}</span>
            <span class="ledger-rank-bar"><i :data-color="catColor(r.category)" :style="{ width: (r.amountFen / rank[0].amountFen) * 100 + '%' }" /></span>
            <b class="ledger-rank-amt">{{ fmtMoney(r.amountFen) }}</b>
            <span class="ledger-rank-pct">{{ r.pct }}%</span>
          </div>
        </div>
      </section>

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
    </template>

    <!-- ── 分类预算：长期月度预算，按当前展示月考核 ── -->
    <section v-else class="ledger-budget" :aria-label="C.ledgerViewBudget">
      <p class="ledger-tip ledger-budget-hint">{{ C.ledgerBudgetHint }}</p>

      <p v-if="st.budgetsLoadError" role="alert" class="storage-warning">
        {{ st.budgetsLoadError }}
        <button class="text-button" @click="loadBudgets(true)">{{ C.retry }}</button>
      </p>
      <div v-else-if="!st.budgetsLoaded" class="ledger-empty">{{ C.syncing }}</div>

      <template v-else>
        <!-- 添加/编辑共用一张内联表单；编辑时分类锁定（改名=删除重建）。 -->
        <form v-if="budgetForm" class="budget-form" @submit.prevent="submitBudget">
          <input
            v-model="budgetForm.category"
            :disabled="budgetForm.mode === 'edit'"
            list="ledger-cat-suggest"
            maxlength="40"
            :placeholder="C.ledgerBudgetCategoryPh"
            :aria-label="C.ledgerBudgetCategory"
            autocomplete="off"
          />
          <datalist id="ledger-cat-suggest">
            <option v-for="c in catSuggestions" :key="c" :value="c" />
          </datalist>
          <input
            v-model="budgetForm.amount"
            type="number"
            min="0"
            step="0.01"
            :placeholder="C.ledgerBudgetAmount"
            :aria-label="C.ledgerBudgetAmount"
            autocomplete="off"
          />
          <div class="budget-form-actions">
            <button type="submit" class="primary-button">{{ C.save }}</button>
            <button type="button" class="text-button" @click="budgetForm = null">{{ C.goalMilestoneCancel }}</button>
          </div>
          <p v-if="budgetError" role="alert" class="form-error">{{ budgetError }}</p>
        </form>
        <button v-else class="goal-add-row" @click="openBudgetForm">
          <Icon name="plus" :size="14" />
          {{ C.ledgerBudgetAdd }}
        </button>

        <p v-if="!st.budgets.length && !budgetForm" class="ledger-empty">{{ C.ledgerBudgetEmpty }}</p>

        <div v-for="b in budgetRows" :key="b.category" class="budget-card">
          <div class="budget-top">
            <span class="ledger-dot" :data-color="catColor(b.category)" />
            <span class="ledger-cat">{{ b.category }}</span>
            <span class="budget-nums">
              <b :class="budgetPct(b) > 100 ? 'is-neg' : ''">{{ fmtMoney(b.spent) }}</b>
              <i>/</i>
              {{ fmtMoney(b.amountFen) }}
            </span>
            <span class="ledger-ops">
              <button class="icon-button" :aria-label="C.ledgerBudgetEdit + ' ' + b.category" @click="editBudget(b)">
                <Icon name="edit" :size="14" />
              </button>
              <button class="icon-button" :aria-label="C.ledgerBudgetDelete + ' ' + b.category" @click="removeBudget(b.category)">
                <Icon name="close" :size="14" />
              </button>
            </span>
          </div>
          <div class="budget-track">
            <i :data-color="catColor(b.category)" :style="{ width: Math.min(100, budgetPct(b)) + '%' }" :class="{ 'is-over': budgetPct(b) > 100 }" />
          </div>
          <div class="budget-foot">
            <span>{{ budgetPct(b) }}%</span>
            <span :class="budgetPct(b) > 100 ? 'is-neg' : ''">{{ budgetRemainText(b) }}</span>
          </div>
        </div>

        <!-- 当月有支出但没设预算的分类：提示补预算，防止漏管。 -->
        <template v-if="unbudgeted.length">
          <div class="ledger-listhead">
            <h3>{{ C.ledgerBudgetNoBudget }}</h3>
            <span class="ledger-tip">{{ unbudgeted.length }}</span>
          </div>
          <div v-for="u in unbudgeted" :key="u.category" class="budget-unbudgeted">
            <span class="ledger-dot" :data-color="catColor(u.category)" />
            <span class="ledger-cat">{{ u.category }}</span>
            <b class="ledger-amt is-neg">-{{ fmtMoney(u.amountFen) }}</b>
            <button class="text-button" @click="prefillBudget(u.category)">{{ C.ledgerBudgetSetFor }}</button>
          </div>
        </template>
      </template>
    </section>

    <LedgerSheet v-if="sheetOpen" :tx="editing" @close="sheetOpen = false" />
  </section>
</template>
