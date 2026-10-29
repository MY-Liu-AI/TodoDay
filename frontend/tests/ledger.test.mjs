import test from "node:test";
import assert from "node:assert/strict";
import { readFileSync } from "node:fs";
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
} from "../src/lib/ledger.js";

/* 记账本契约（2026-10-29）：
   - 两个视图：账本总览（流水分析 + 流水）/ 分类预算，与目标页同一套
     view-switch 分段切换；主操作随视图变化（记一笔 / 添加预算）；
   - 流水分析 = 与上月对比（收支结余三卡 + 环比）+ 分类支出排行（占比条形），
     全部由当月流水推导，恒与服务端同口径；上月汇总独立拉取、失败降级；
   - 分类预算是长期月度预算（每分类一条，设置一次每月沿用），按当前展示月
     考核；花费从当月流水推导，与三卡同口径；当月有支出无预算的分类
     单列提示补预算；
   - 数据全在服务端（per-workspace），client_id 幂等、version 乐观并发、
     软删 + 撤销；分类色是 [data-color] 数据编码，主操作只有青绿一色。 */

const store = readFileSync(new URL("../src/store.ts", import.meta.url), "utf8");
const app = readFileSync(new URL("../src/App.vue", import.meta.url), "utf8");
const copy = readFileSync(new URL("../src/copy.ts", import.meta.url), "utf8");
const view = readFileSync(new URL("../src/components/LedgerView.vue", import.meta.url), "utf8");
const sheet = readFileSync(new URL("../src/components/LedgerSheet.vue", import.meta.url), "utf8");
const api = readFileSync(new URL("../src/api.ts", import.meta.url), "utf8");
const css = readFileSync(new URL("../src/styles/planner.css", import.meta.url), "utf8");

/* ── lib/ledger.js 纯函数单测 ─────────────────────────────────────────── */

test("fmtMoney renders fen as grouped yuan without forced decimals", () => {
  assert.equal(fmtMoney(0), "¥0");
  assert.equal(fmtMoney(3250), "¥32.5");
  assert.equal(fmtMoney(1850000), "¥18,500");
  assert.equal(fmtMoney(123456789), "¥1,234,567.89");
});

test("monthKeyOf / fmtMonthTitle / fmtDayHead use local calendar strings", () => {
  assert.equal(monthKeyOf(new Date(2026, 9, 26)), "2026-10");
  assert.equal(monthKeyOf(new Date(2026, 0, 5)), "2026-01");
  assert.equal(fmtMonthTitle("2026-10"), "2026年10月");
  assert.equal(fmtMonthTitle("2026-01"), "2026年1月");
  assert.equal(fmtDayHead("2026-10-26"), "10月26日 周一");
  assert.equal(fmtDayHead("2026-10-25"), "10月25日 周日");
});

test("catColor maps the six ledger categories and falls back to slate", () => {
  const byName = Object.fromEntries(LEDGER_CATS.map((c) => [c.name, c.color]));
  assert.deepEqual(byName, {
    餐饮: "peach",
    通勤: "slate",
    居家: "teal",
    购物: "coral",
    娱乐: "cyan",
    学习: "yellow",
  });
  assert.equal(catColor("不存在的分类"), "slate");
});

test("groupTxByDay keeps server order and sums per-day exp/inc", () => {
  const txs = [
    { id: 3, date: "2026-10-26", type: "exp", amountFen: 200 },
    { id: 2, date: "2026-10-26", type: "inc", amountFen: 5000 },
    { id: 1, date: "2026-10-24", type: "exp", amountFen: 100 },
  ];
  const groups = groupTxByDay(txs);
  assert.equal(groups.length, 2);
  assert.equal(groups[0].date, "2026-10-26");
  assert.deepEqual(groups[0].items.map((t) => t.id), [3, 2]);
  assert.equal(groups[0].exp, 200);
  assert.equal(groups[0].inc, 5000);
  assert.equal(groups[1].exp, 100);
  assert.equal(groups[1].inc, 0);
});

test("monthTotals nets income minus expenses", () => {
  const txs = [
    { type: "exp", amountFen: 5650 },
    { type: "inc", amountFen: 1850000 },
  ];
  assert.deepEqual(monthTotals(txs), { exp: 5650, inc: 1850000, net: 1844350 });
  assert.deepEqual(monthTotals([]), { exp: 0, inc: 0, net: 0 });
});

test("prevMonthKey walks across year boundary", () => {
  assert.equal(prevMonthKey("2026-10"), "2026-09");
  assert.equal(prevMonthKey("2026-01"), "2025-12");
  assert.equal(prevMonthKey("2026-12"), "2026-11");
});

test("spendByCategory sums expenses only", () => {
  const txs = [
    { type: "exp", category: "餐饮", amountFen: 100 },
    { type: "exp", category: "餐饮", amountFen: 200 },
    { type: "exp", category: "购物", amountFen: 50 },
    { type: "inc", category: "", amountFen: 999 },
  ];
  assert.deepEqual(spendByCategory(txs), { 餐饮: 300, 购物: 50 });
});

test("catExpRank sorts desc, computes pct of total, folds tail into 其他", () => {
  const txs = [
    { type: "exp", category: "餐饮", amountFen: 500 },
    { type: "exp", category: "购物", amountFen: 300 },
    { type: "exp", category: "娱乐", amountFen: 150 },
    { type: "exp", category: "学习", amountFen: 50 },
  ];
  // topN=3：前 3 名照常展示，第 4 名折进「其他」。
  const rank = catExpRank(txs, 3);
  assert.deepEqual(
    rank.map((r) => r.category),
    ["餐饮", "购物", "娱乐", "其他"],
  );
  assert.equal(rank[0].pct, 50);
  assert.equal(rank[3].amountFen, 50);
  assert.deepEqual(catExpRank([], 3), []);
  // 不超过 topN 时不出现「其他」。
  assert.equal(catExpRank(txs, 8).some((r) => r.category === "其他"), false);
});

test("momPct rounds and returns null without a positive base", () => {
  assert.equal(momPct(1120, 1000), 12);
  assert.equal(momPct(880, 1000), -12);
  assert.equal(momPct(0, 0), null);
  assert.equal(momPct(100, 0), null);
});

/* ── 源码契约 ─────────────────────────────────────────────────────────── */

test("ledger page is wired into App right after the stats nav item", () => {
  assert.match(app, /import LedgerView from "\.\/components\/LedgerView\.vue"/);
  assert.match(app, /<LedgerView v-else-if="page === 'ledger'" \/>/);
  assert.match(app, /<Icon name="ledger" \/>/);
  // 页头为单行 slim 版（标题 + 同排副标题），页面名在 PAGE_META。
  assert.match(app, /ledger: \{ title: C\.ledger, subtitle: C\.ledgerSubtitle \}/);
  // 导航次序不破 goals 契约：计划 → 目标 → 专注 → 统计 → 记账本。
  const nav = app.match(/<nav aria-label="workspace"[\s\S]*?<\/nav>/)?.[0] || "";
  const order = ["page === 'tasks'", "page === 'goals'", "page === 'focus'", "goStats", "page === 'ledger'"].map((k) => nav.indexOf(k));
  assert.ok(order.every((i) => i >= 0) && order.every((v, i) => i === 0 || v > order[i - 1]),
    `导航次序不对：${order}`);
});

test("ledger store exposes lifecycle actions hitting ledger endpoints", () => {
  for (const fn of ["loadLedger", "addTx", "saveTx", "removeTx", "restoreDeletedTx", "dismissDeletedTx"]) {
    assert.match(store, new RegExp(`export async function ${fn}\\(|export function ${fn}\\(`), `store 缺少 ${fn}`);
  }
  assert.match(store, /wsPath\("\/ledger"\)/);
  assert.match(store, /wsPath\(`\/ledger\/\$\{id\}`\)/);
  assert.match(store, /wsPath\("\/ledger\/delete"\)/);
  assert.match(store, /wsPath\("\/ledger\/restore"\)/);
  assert.match(store, /registerLogoutHook\(clearLedger\)/);
  assert.match(store, /clientId = "tx_/);
  // 409 与目标同约定：不带最新值，重拉当月对齐，绝不静默覆盖。
  assert.match(store, /e\.status === 409[\s\S]*loadLedger\(st\.ledgerMonth, true\)/);
  // 幂等创建 + 乐观插入；切换月份有竞态保护。
  assert.match(store, /let ledgerSeq = 0/);
});

test("ledger store exposes budget actions and prev-month stats fetch", () => {
  for (const fn of ["loadBudgets", "setBudget", "removeBudget", "fetchMonthStats"]) {
    assert.match(store, new RegExp(`export async function ${fn}\\(`), `store 缺少 ${fn}`);
  }
  // 预算写操作响应回全量列表，客户端直接替换。
  assert.match(store, /wsPath\("\/ledger\/budgets"\)/);
  assert.match(store, /encodeURIComponent\(category\)/);
  assert.match(store, /st\.budgets = data\.budgets/);
  // 登出清空预算状态。
  assert.match(store, /st\.budgets = \[\][\s\S]*st\.budgetsLoaded = false/);
});

test("wire types mirror the backend contract", () => {
  assert.match(api, /interface WireTx \{/);
  assert.match(api, /amountFen: number/);
  assert.match(api, /interface WireLedgerMonth \{/);
  assert.match(api, /entries: WireTx\[\]/);
  assert.match(api, /interface WireBudget \{/);
  assert.match(api, /interface WireBudgets \{/);
  assert.match(api, /budgets: WireBudget\[\]/);
});

test("ledger view has the two-view switch, analysis section and day groups", () => {
  assert.match(view, /watch\(month, \(m\) => loadLedger\(monthKeyOf\(m\)\), \{ immediate: true \}\)/);
  // 两个视图共用 view-switch 分段控件（与目标页同款），主操作随视图变化。
  assert.match(view, /const view = ref<"overview" \| "budget">\("overview"\)/);
  assert.match(view, /class="view-switch" aria-label="ledger"/);
  assert.match(view, /C\.ledgerViewOverview/);
  assert.match(view, /C\.ledgerViewBudget/);
  // 流水分析：与上月对比三卡 + 分类排行，全部由当月流水推导。
  assert.match(view, /monthTotals\(st\.txs\)/);
  assert.match(view, /catExpRank\(st\.txs\)/);
  assert.match(view, /prevMonthKey\(monthKeyOf/);
  assert.match(view, /class="ledger-mom"/);
  assert.match(view, /class="ledger-rank"/);
  // 流水列表与记一笔 FAB。
  assert.match(view, /groupTxByDay\(st\.txs\)/);
  assert.match(view, /class="ledger-row"/);
  assert.match(view, /class="ledger-fab"/);
  // N 键唤起记一笔；组件级监听，切走即失效。
  assert.match(view, /e\.key === "n" \|\| e\.key === "N"/);
  assert.match(view, /window\.addEventListener\("keydown", onKey\)/);
  assert.match(view, /window\.removeEventListener\("keydown", onKey\)/);
});

test("ledger budget view manages long-term budgets against displayed month", () => {
  // 进预算视图才拉预算；花费从当月流水推导（与三卡同口径）。
  assert.match(view, /if \(v === "budget"\) loadBudgets\(\)/);
  assert.match(view, /spendByCategory\(st\.txs\)/);
  // 内联表单：添加/编辑共用；编辑锁分类（改名=删除重建）。
  assert.match(view, /mode: "add" \| "edit"/);
  assert.match(view, /:disabled="budgetForm\.mode === 'edit'"/);
  assert.match(view, /await setBudget\(category, fen\)/);
  assert.match(view, /removeBudget\(b\.category\)/);
  // 进度条封顶 100%，超支用 --neg 区分。
  assert.match(view, /Math\.min\(100, budgetPct\(b\)\)/);
  assert.match(view, /'is-over': budgetPct\(b\) > 100/);
  // 无预算的分类支出单列，可一键预填表单。
  assert.match(view, /C\.ledgerBudgetNoBudget/);
  assert.match(view, /prefillBudget\(u\.category\)/);
});

test("ledger sheet is a bottom sheet with keypad and category chips", () => {
  assert.match(sheet, /<dialog ref="refEl" class="ledger-sheet"/);
  assert.match(sheet, /class="ledger-keypad"/);
  assert.match(sheet, /class="ledger-chip"/);
  assert.match(sheet, /keyIn\(e\.key\)/);
  assert.match(sheet, /save\(!e\.shiftKey\)/);
  assert.match(sheet, /await addTx\(input\)/);
  assert.match(sheet, /await saveTx\(props\.tx\.id, input, props\.tx\.version\)/);
  // 收入不占分类；日期不晚于今天。
  assert.match(sheet, /category: type\.value === "exp" \? category\.value : ""/);
  assert.match(sheet, /date\.value > todayStr/);
});

test("copy has the ledger strings", () => {
  for (const key of [
    "ledger", "ledgerSubtitle", "ledgerFlow", "ledgerExp", "ledgerInc", "ledgerNet",
    "ledgerAdd", "ledgerEdit", "ledgerEmpty", "ledgerAmountError", "ledgerCatError",
    "ledgerSaveAnother", "ledgerLoadFailed", "txAdded", "txUpdated", "txDeleted", "txSaveFailed",
    "ledgerViewOverview", "ledgerViewBudget", "ledgerAnalysis", "ledgerMomVs", "ledgerMomNew",
    "ledgerCatRank", "ledgerCatRankEmpty", "ledgerBudgetHint", "ledgerBudgetAdd",
    "ledgerBudgetEdit", "ledgerBudgetDelete", "ledgerBudgetCategory", "ledgerBudgetAmount",
    "ledgerBudgetLeft", "ledgerBudgetOver", "ledgerBudgetEmpty", "ledgerBudgetNoBudget",
    "ledgerBudgetSetFor", "ledgerBudgetCategoryError", "ledgerBudgetAmountError",
    "ledgerBudgetLoadFailed", "ledgerBudgetSaveFailed",
  ]) {
    assert.match(copy, new RegExp(`\\b${key}:`), `copy.ts 缺少 ${key}`);
  }
});

test("App toast offers undo for deleted ledger entries", () => {
  assert.match(app, /st\.deletedTx/);
  assert.match(app, /@click="restoreDeletedTx"/);
  assert.match(store, /st\.deletedTx = tx/);
});

test("planner.css has the ledger styles, variable-driven for dark mode", () => {
  for (const cls of [".ledger-sheet", ".ledger-keypad", ".ledger-chip", ".ledger-fab", ".ledger-row", ".ledger-mom", ".ledger-rank", ".budget-card", ".budget-track", ".budget-form"]) {
    assert.match(css, new RegExp(`\\${cls}[ .{,:\\[]`), `planner.css 缺少 ${cls}`);
  }
  // 六色 [data-color] 编码复用既有变量，不新增主色。
  assert.match(css, /\.ledger-chip \.ledger-dot \{/);
  assert.match(css, /\.ledger-rank-bar i \{/);
  assert.match(css, /background: var\(--event-line\)/);
  assert.match(css, /\.budget-track i\.is-over \{/);
  assert.match(css, /background: var\(--neg\)/);
  assert.doesNotMatch(css, /\.ledger-fab[^}]*#[0-9a-fA-F]{3,6}/);
});
