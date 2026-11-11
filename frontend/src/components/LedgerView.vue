<script setup lang="ts">
/* 记账本页：账本总览（流水分析 + 流水）/ 分类预算 两个视图，
 * 与目标页同一套 view-switch 分段切换（纯组件内状态、不持久化）。
 * 流水分析 = 与上月对比（支出/收入/结余 + 环比）+ 分类支出排行（占比条形），
 * 全部由 st.txs 推导、与流水列表恒同口径；上月汇总单独拉 stats，失败降级。
 * 分类预算是长期月度预算（每分类一条，设置一次每月沿用），按当前展示月
 * 的花费考核进度；花费同样从 st.txs 推导。页面挂载期间 N 键唤起记一笔。 */
import { computed, onMounted, onUnmounted, ref, watch } from "vue";
import { C } from "../copy";
import {
  st,
  loadLedger,
  removeTx,
  loadBudgets,
  loadCategories,
  setBudget,
  removeBudget,
  addCategory,
  renameCategory,
  removeCategory,
  fetchMonthStats,
} from "../store";
import type { WireTx } from "../api";
import { TODAY } from "../lib/todo.js";
import {
  catBarSeries,
  colorOf,
  dailyExpSeries,
  fmtBarMoney,
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
/* 预算列表长期有效，不随月份重拉；进预算视图时按需加载
 * （分类清单同一时机：管理分类与设预算在同一视图）。 */
watch(view, (v) => {
  if (v === "budget") {
    loadBudgets();
    loadCategories();
  }
});

/* 分类色从服务端分类清单解析（用户新增的分类也有色）；清单外的历史
 * 分类名（改名前的旧名等）回落 slate。 */
function catColorOf(name: string): string {
  return colorOf(st.categories, name);
}

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
const spend = computed(() => spendByCategory(st.txs) as Record<string, number>);

/* ── 流水分析图表：左每日支出折线（与番茄专注 focus-trend 同一套语法）、
 * 右分类支出柱（与进度统计 weekbar 同一套语法）。都是无面板裸图 +
 * HTML 刻度/标签行，颜色全走变量；分类柱身份由下方文字标签承担，
 * [data-color] 只是辅助编码（AGENTS.md 指定的日历数据编码，不动色板）。 */

/* 每日支出：覆盖当月每一天（无支出为 0），折线连续。 */
const daily = computed(() => dailyExpSeries(st.txs, monthKeyOf(month.value)));
const dailyMax = computed(() => daily.value.reduce((a, d) => Math.max(a, d.amountFen), 0));

/* 折线几何：高度固定 100，与右侧柱区（.ledger-catbar height:100px）严格
 * 同高——两图都是 100px 绘图区 + 底部一行刻度，任何窗口宽度下对齐。
 * 宽度用 ResizeObserver 实测栏宽写进 viewBox，1:1 渲染（线宽/圆点都是
 * 真实像素，不再依赖等比缩放 + non-scaling-stroke）。
 * 纵向只留顶部留白（LINE_PAD）：零值点落在 SVG 底边，与右栏柱底/
 * 基线同一条水平线——两图的「零线」对齐。 */
const LINE_H = 100;
const LINE_PAD = 8;
const trendW = ref(340);
const trendBox = ref<HTMLElement | null>(null);
let trendRO: ResizeObserver | null = null;
/* 图表区块是 v-if 延迟渲染的（要等分类清单，bars 才有值），ref 出现得
 * 比 onMounted 晚——watch 跟随 ref 挂卸观察器，卸载/消失时断开。
 * 宽度 1:1 写进 viewBox，与右栏柱区（固定 100px 高）严格同高对齐。 */
watch(
  trendBox,
  (el) => {
    trendRO?.disconnect();
    trendRO = null;
    if (!el) return;
    trendRO = new ResizeObserver((es) => {
      const w = es[0]?.contentRect.width ?? 0;
      if (w > 0) trendW.value = w;
    });
    trendRO.observe(el);
  },
  { immediate: true, flush: "post" },
);
const trendPts = computed(() => {
  const max = dailyMax.value || 1;
  const span = Math.max(1, daily.value.length - 1);
  return daily.value.map((d, i) => ({
    ...d,
    x: (i / span) * trendW.value,
    y: LINE_H - (d.amountFen / max) * (LINE_H - LINE_PAD),
  }));
});
const trendLine = computed(() => trendPts.value.map((p) => `${p.x.toFixed(1)},${p.y.toFixed(1)}`).join(" "));
const trendArea = computed(() => {
  const pts = trendPts.value;
  if (!pts.length) return "";
  return `${pts[0].x},${LINE_H} ${trendLine.value} ${pts[pts.length - 1].x},${LINE_H}`;
});
/* x 轴刻度（同 focus-trend-axis）：每格 flex:1，只标 1/10/20/月末。 */
const axisDays = computed(() => {
  const n = daily.value.length;
  const marks = new Set([1, 10, 20, n].filter((d) => d <= n));
  return daily.value.map((d) => ({ day: marks.has(d.day) ? d.day : null }));
});

/* 分类支出柱：工作区清单全量出柱（零支出也占位，让用户看到完整分类
 * 盘子），高度与左侧每日支出折线（viewBox 高 100）对齐——两图同高，
 * 底部都是一行刻度。柱高按当月最高分类归一，2px 保底（weekbar 同规则）。 */
const BAR_MAX_H = 84;
const bars = computed(() => catBarSeries(st.txs, st.categories) as { category: string; amountFen: number; pct: number }[]);
const barMax = computed(() => Math.max(1, ...bars.value.map((b) => b.amountFen)));
const barCols = computed(() => bars.value.map((b) => ({ ...b, h: (b.amountFen / barMax.value) * BAR_MAX_H + 2 })));
/* 分类名截断（按 rune），防长文本溢出柱宽；全名在 <title>。 */
function shortCat(s: string): string {
  const r = [...s];
  return r.length > 4 ? r.slice(0, 4).join("") + "…" : s;
}

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

/* 添加/编辑共用一张内联表单：分类是预算的幂等键，编辑时锁定分类
 * （改名 = 删除重建，避免「改了名其实换了条预算」的错觉）。 */
const budgetForm = ref<{ mode: "add" | "edit"; category: string; amount: string } | null>(null);
const budgetError = ref("");

/* 预算表单的分类胶囊（与记一笔同款语法，不再手输）：添加时 = 清单里
 * 还没有预算的分类——给已设过的再选一遍是误操作；编辑时锁定当前分类，
 * 清单外名字的旧预算也能显示（slate 兜底）。 */
const budgetCatChoices = computed<{ id: number; name: string; color: string }[]>(() => {
  const form = budgetForm.value;
  if (!form) return [];
  if (form.mode === "edit") {
    const hit = st.categories.find((c) => c.name === form.category);
    return [hit ?? { id: -1, name: form.category, color: "slate" }];
  }
  const budgeted = new Set(st.budgets.map((b) => b.category));
  return st.categories.filter((c) => !budgeted.has(c.name));
});

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

/* ── 分类体系管理：增/改名/删（折叠在分类预算视图底部）。
 * 管理是低频操作，默认收起，不挤占日常预算视口。改名/删除的级联在
 * 服务端单事务完成（流水、预算一起动），store 动作返回后已重拉当月
 * 流水与预算。删除被在用流水挡住时服务端 409，toast 如实提示笔数。 */
const catManageOpen = ref(false);
const catAddName = ref("");
const catError = ref("");
const renamingId = ref<number | null>(null);
const renameName = ref("");

async function submitCatAdd() {
  const name = catAddName.value.trim();
  if (!name) {
    catError.value = C.ledgerCatNameError;
    return;
  }
  catError.value = "";
  if (await addCategory(name)) catAddName.value = "";
  else catError.value = C.ledgerCatSaveFailed;
}

function startRename(c: { id: number; name: string }) {
  renamingId.value = c.id;
  renameName.value = c.name;
  catError.value = "";
}

async function submitRename() {
  if (renamingId.value === null) return;
  const name = renameName.value.trim();
  if (!name) {
    catError.value = C.ledgerCatNameError;
    return;
  }
  catError.value = "";
  if (await renameCategory(renamingId.value, name)) renamingId.value = null;
  else catError.value = C.ledgerCatSaveFailed;
}

async function deleteCategory(c: { id: number; name: string }) {
  catError.value = "";
  if (!(await removeCategory(c.id))) catError.value = C.ledgerCatSaveFailed;
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
/* 分类柱图按工作区清单出柱（清单外的旧名折「其他」），总览同样需要
 * 清单才能画对——挂载即拉（幂等，已加载则跳过）。修复刷新后总览先
 * 渲染、支出全被折进「其他」的时序问题。 */
onMounted(loadCategories);
onMounted(() => window.addEventListener("keydown", onKey));
onUnmounted(() => {
  window.removeEventListener("keydown", onKey);
  trendRO?.disconnect();
});

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

        <!-- 分类清单就绪前不出图：空清单会把支出全折进「其他」，那是
             错的呈现。读取失败放行兜底（旧名归「其他」）。 -->
        <div v-if="!st.categoriesLoaded && !st.categoriesLoadError" class="ledger-empty">{{ C.syncing }}</div>
        <div v-else class="ledger-charts">
          <p v-if="!bars.length" class="ledger-rank-empty">{{ C.ledgerCatRankEmpty }}</p>
          <template v-else>
            <!-- 左：每日支出折线（番茄专注 focus-trend 语法：面积 + 细线 +
                 圆点，刻度是 HTML 行；点上看当日金额） -->
            <div>
              <div class="grouphead"><span>{{ C.ledgerDailyExp }}</span></div>
              <div class="ledger-trend" ref="trendBox">
                <svg
                  :viewBox="`0 0 ${trendW} ${LINE_H}`"
                  role="img"
                  :aria-label="C.ledgerDailyExp"
                >
                  <polygon class="area" :points="trendArea" />
                  <polyline class="line" :points="trendLine" />
                  <circle v-for="p in trendPts" :key="p.date" class="pt" :cx="p.x" :cy="p.y" r="1.6">
                    <title>{{ p.date }} · {{ fmtMoney(p.amountFen) }}</title>
                  </circle>
                </svg>
                <div class="ledger-trend-axis" aria-hidden="true">
                  <span v-for="(a, i) in axisDays" :key="i" class="tick">{{ a.day ?? "" }}</span>
                </div>
              </div>
            </div>

            <!-- 右：分类支出柱（进度统计 weekbar 语法：--event-bg 柱底 +
                 2px 分类色顶边）。金额标签用紧凑格式挂在柱顶，分类名在
                 基线刻度行（同左图 HTML 刻度行语法），基线让柱落地。 -->
            <div>
              <div class="grouphead"><span>{{ C.ledgerCatRank }}</span></div>
              <div class="ledger-catbar" role="img" :aria-label="C.ledgerCatRank">
                <div v-for="b in barCols" :key="b.category" class="d" :data-color="catColorOf(b.category)">
                  <span class="v">{{ fmtBarMoney(b.amountFen) }}</span>
                  <div class="b" :style="{ height: b.h + 'px' }" :title="`${b.category} · ${fmtMoney(b.amountFen)} · ${b.pct}%`" />
                </div>
              </div>
              <div class="ledger-catbar-axis" aria-hidden="true">
                <span v-for="b in barCols" :key="b.category" class="l">{{ shortCat(b.category) }}</span>
              </div>
            </div>
          </template>
        </div>
      </section>

      <div class="ledger-listhead">
        <h3>{{ C.ledgerFlow }}</h3>
        <span class="ledger-tip">{{ C.ledgerFlowCount.replace("{n}", String(st.txs.length)) }}</span>
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
            <span class="ledger-dot" :data-color="tx.type === 'exp' ? catColorOf(tx.category) : 'teal'" />
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
        <!-- 添加/编辑共用一张内联表单；编辑时分类锁定（改名=删除重建）。
             分类用胶囊选（与记一笔同款语法）：添加时只列还没预算的分类。 -->
        <form v-if="budgetForm" class="budget-form" @submit.prevent="submitBudget">
          <div class="ledger-cats budget-form-cats" role="group" :aria-label="C.ledgerBudgetCategory">
            <button
              v-for="c in budgetCatChoices"
              :key="c.id"
              type="button"
              class="ledger-chip"
              :data-color="c.color"
              :class="{ 'is-on': budgetForm.category === c.name }"
              :aria-pressed="budgetForm.category === c.name"
              @click="budgetForm.category = c.name"
            >
              <span class="ledger-dot" />{{ c.name }}
            </button>
          </div>
          <input
            v-model="budgetForm.amount"
            type="number"
            inputmode="decimal"
            min="0"
            step="0.01"
            :placeholder="C.ledgerBudgetAmount"
            :aria-label="C.ledgerBudgetAmount"
            autocomplete="off"
          />
          <div class="budget-form-actions">
            <button type="submit" class="primary-button">
              {{ budgetForm.mode === "edit" ? C.save : C.ledgerBudgetAdd }}
            </button>
            <button type="button" class="text-button" @click="budgetForm = null">{{ C.goalMilestoneCancel }}</button>
          </div>
          <p v-if="budgetError" role="alert" class="form-error">{{ budgetError }}</p>
        </form>
        <p v-if="!st.budgets.length && !budgetForm" class="ledger-empty">{{ C.ledgerBudgetEmpty }}</p>

        <!-- 添加预算的唯一入口是工具栏右上主按钮（与总览「记一笔」同款
             单主操作语法）；区块标题给卡片分区，计数与流水标题同款。 -->
        <div v-if="budgetRows.length || budgetForm" class="ledger-listhead">
          <h3>{{ C.ledgerBudgetList }}</h3>
          <span class="ledger-tip">{{ budgetRows.length }}</span>
        </div>

        <div v-for="b in budgetRows" :key="b.category" class="budget-card">
          <div class="budget-top">
            <span class="ledger-dot" :data-color="catColorOf(b.category)" />
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
            <i :data-color="catColorOf(b.category)" :style="{ width: Math.min(100, budgetPct(b)) + '%' }" :class="{ 'is-over': budgetPct(b) > 100 }" />
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
            <span class="ledger-dot" :data-color="catColorOf(u.category)" />
            <span class="ledger-cat">{{ u.category }}</span>
            <b class="ledger-amt is-neg">-{{ fmtMoney(u.amountFen) }}</b>
            <button class="text-button" @click="prefillBudget(u.category)">{{ C.ledgerBudgetSetFor }}</button>
          </div>
        </template>

        <!-- 分类体系管理：清单驱动记一笔胶囊与账单解析，折叠在预算列表
             之后。管理是低频操作，默认收起；改名级联历史流水与预算，删除
             被在用流水挡住会如实提示。 -->
        <div class="ledger-listhead">
          <h3>{{ C.ledgerCats }}</h3>
          <button
            class="goal-strategy-fold"
            :aria-expanded="catManageOpen"
            :aria-label="catManageOpen ? C.ledgerCatManageFold : C.ledgerCatManageUnfold"
            @click="catManageOpen = !catManageOpen"
          >
            <span class="ledger-tip">{{ st.categories.length }}</span>
            <Icon name="arrow" :size="12" :style="catManageOpen ? 'transform: rotate(90deg)' : ''" />
          </button>
        </div>
        <template v-if="catManageOpen">
          <div class="ledger-catrows">
            <div v-for="c in st.categories" :key="c.id" class="ledger-cat-row">
              <span class="ledger-dot" :data-color="c.color" />
              <template v-if="renamingId === c.id">
                <input
                  v-model="renameName"
                  class="ledger-cat-rename"
                  maxlength="40"
                  :aria-label="C.ledgerCatRename + ' ' + c.name"
                  @keydown.enter.prevent="submitRename"
                  @keydown.esc="renamingId = null"
                />
                <span class="ledger-ops">
                  <button class="icon-button" :aria-label="C.save" @click="submitRename">
                    <Icon name="check" :size="14" />
                  </button>
                  <button class="icon-button" :aria-label="C.goalMilestoneCancel" @click="renamingId = null">
                    <Icon name="close" :size="14" />
                  </button>
                </span>
              </template>
              <template v-else>
                <span class="ledger-cat">{{ c.name }}</span>
                <span class="ledger-ops">
                  <button class="icon-button" :aria-label="C.ledgerCatRename + ' ' + c.name" @click="startRename(c)">
                    <Icon name="edit" :size="14" />
                  </button>
                  <button class="icon-button" :aria-label="C.ledgerCatDelete + ' ' + c.name" @click="deleteCategory(c)">
                    <Icon name="close" :size="14" />
                  </button>
                </span>
              </template>
            </div>
            <form class="ledger-cat-add" @submit.prevent="submitCatAdd">
              <input v-model="catAddName" maxlength="40" :placeholder="C.ledgerCatNamePh" :aria-label="C.ledgerCatNamePh" autocomplete="off" />
              <button type="submit" class="secondary-button">{{ C.ledgerCatAdd }}</button>
            </form>
            <p v-if="catError" role="alert" class="form-error">{{ catError }}</p>
          </div>
        </template>
      </template>
    </section>

    <LedgerSheet v-if="sheetOpen" :tx="editing" @close="sheetOpen = false" />
  </section>
</template>
