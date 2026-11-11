import test from "node:test";
import assert from "node:assert/strict";
import { readFileSync } from "node:fs";
import {
  LEDGER_CATS,
  catBarSeries,
  catColor,
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
} from "../src/lib/ledger.js";

/* 记账本契约（2026-10-29）：
   - 两个视图：账本总览（流水分析 + 流水）/ 分类预算，与目标页同一套
     view-switch 分段切换；主操作随视图变化（记一笔 / 添加预算）；
   - 分类是工作区级清单（ledger_categories，首次读取服务端播种默认
     餐饮/交通/购物/住房/娱乐/其他），预算视图里可增/改名/删；改名
     级联历史流水与预算（服务端单事务）；Kimi 账单解析按这份清单收敛
     分类（清单外一律归「其他」）；
   - 流水分析 = 与上月对比（收支结余三卡 + 环比）+ 双栏图表：左栏每日支出
     折线（番茄专注 focus-trend 语法，覆盖当月每一天含零支出日）、右栏分类
     支出柱（进度统计 weekbar 语法，前 6 名，尾巴折进「其他」，直标金额与
     分类名，身份不靠颜色单独承担），
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

test("fmtBarMoney compacts chart labels: integer yuan from ¥10 up, cents below", () => {
  assert.equal(fmtBarMoney(0), "¥0.00");
  assert.equal(fmtBarMoney(950), "¥9.50");
  assert.equal(fmtBarMoney(1000), "¥10");
  assert.equal(fmtBarMoney(123456), "¥1,235");
});

test("monthKeyOf / fmtMonthTitle / fmtDayHead use local calendar strings", () => {
  assert.equal(monthKeyOf(new Date(2026, 9, 26)), "2026-10");
  assert.equal(monthKeyOf(new Date(2026, 0, 5)), "2026-01");
  assert.equal(fmtMonthTitle("2026-10"), "2026年10月");
  assert.equal(fmtMonthTitle("2026-01"), "2026年1月");
  assert.equal(fmtDayHead("2026-10-26"), "10月26日 周一");
  assert.equal(fmtDayHead("2026-10-25"), "10月25日 周日");
});

test("default categories are the user-specified six; colorOf reads the workspace list", () => {
  const byName = Object.fromEntries(LEDGER_CATS.map((c) => [c.name, c.color]));
  assert.deepEqual(byName, {
    餐饮: "peach",
    交通: "slate",
    购物: "coral",
    住房: "teal",
    娱乐: "cyan",
    其他: "yellow",
  });
  assert.equal(catColor("不存在的分类"), "slate");
  // colorOf 按服务端清单查（含用户新增的分类）；清单外的旧名回落 slate。
  const cats = [...LEDGER_CATS, { name: "医疗", color: "teal" }];
  assert.equal(colorOf(cats, "医疗"), "teal");
  assert.equal(colorOf(cats, "旧分类名"), "slate");
  assert.equal(colorOf([], "餐饮"), "slate");
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

test("catBarSeries lists every workspace category, zero-spend included, desc by amount", () => {
  const cats = [{ name: "餐饮" }, { name: "交通" }, { name: "购物" }, { name: "其他" }];
  // 零支出的分类也出柱（amountFen 0、pct 0），排序按金额降序。
  const rows = catBarSeries([{ type: "exp", category: "购物", amountFen: 300 }], cats);
  assert.deepEqual(
    rows.map((r) => [r.category, r.amountFen]),
    [
      ["购物", 300],
      ["餐饮", 0],
      ["交通", 0],
      ["其他", 0],
    ],
  );
  assert.equal(rows[0].pct, 100);
  assert.equal(rows[1].pct, 0);
  // 当月完全无支出：全部出柱、全 0（不再返回空数组藏起分类盘子）。
  const idle = catBarSeries([], cats);
  assert.equal(idle.length, 4);
  assert.equal(idle.every((r) => r.amountFen === 0 && r.pct === 0), true);
});

test("catBarSeries folds spend outside the workspace list into 其他", () => {
  const cats = [{ name: "餐饮" }, { name: "其他" }];
  // 改名前的自由文本旧名折进已有的「其他」。
  const merged = catBarSeries(
    [
      { type: "exp", category: "餐饮", amountFen: 100 },
      { type: "exp", category: "旧旅行", amountFen: 40 },
    ],
    cats,
  );
  assert.deepEqual(
    merged.map((r) => [r.category, r.amountFen]),
    [
      ["餐饮", 100],
      ["其他", 40],
    ],
  );
  assert.equal(merged[0].pct, 71.4);
  // 清单没有「其他」时补一条兜底（有金额，降序排最前）。
  const appended = catBarSeries([{ type: "exp", category: "杂项", amountFen: 40 }], [{ name: "餐饮" }]);
  assert.deepEqual(
    appended.map((r) => [r.category, r.amountFen]),
    [
      ["其他", 40],
      ["餐饮", 0],
    ],
  );
  // 收入不占分类。
  assert.equal(catBarSeries([{ type: "inc", category: "", amountFen: 999 }], cats).every((r) => r.amountFen === 0), true);
  assert.deepEqual(catBarSeries([], []), []);
});

test("momPct rounds and returns null without a positive base", () => {
  assert.equal(momPct(1120, 1000), 12);
  assert.equal(momPct(880, 1000), -12);
  assert.equal(momPct(0, 0), null);
  assert.equal(momPct(100, 0), null);
});

test("dailyExpSeries covers every day of the month, sums per-day expenses only", () => {
  const txs = [
    { type: "exp", date: "2026-10-01", amountFen: 100 },
    { type: "exp", date: "2026-10-01", amountFen: 250 },
    { type: "exp", date: "2026-10-31", amountFen: 999 },
    { type: "inc", date: "2026-10-15", amountFen: 5000 },
  ];
  const oct = dailyExpSeries(txs, "2026-10");
  assert.equal(oct.length, 31);
  assert.deepEqual(oct[0], { date: "2026-10-01", day: 1, amountFen: 350 });
  assert.equal(oct[14].amountFen, 0); // 只有收入的 10-15 支出为 0
  assert.equal(oct[30].amountFen, 999);
  // 闰年 2 月 29 天；跨年月份按本地日历。
  assert.equal(dailyExpSeries([], "2024-02").length, 29);
  const jan = dailyExpSeries([], "2026-01");
  assert.equal(jan.length, 31);
  assert.equal(jan[30].date, "2026-01-31");
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

test("ledger store exposes category actions backed by the categories endpoint", () => {
  for (const fn of ["loadCategories", "addCategory", "renameCategory", "removeCategory"]) {
    assert.match(store, new RegExp(`export async function ${fn}\\(`), `store 缺少 ${fn}`);
  }
  assert.match(store, /wsPath\("\/ledger\/categories"\)/);
  assert.match(store, /wsPath\(`\/ledger\/categories\/\$\{id\}`\)/);
  assert.match(store, /st\.categories = data\.categories/);
  // 改名级联流水与预算：动作返回后强制重拉当月与预算对齐。
  assert.match(store, /renameCategory[\s\S]*?loadLedger\(st\.ledgerMonth, true\)/);
  assert.match(store, /renameCategory[\s\S]*?loadBudgets\(true\)/);
  assert.match(store, /removeCategory[\s\S]*?loadBudgets\(true\)/);
  // 登出清空分类状态。
  assert.match(store, /st\.categories = \[\][\s\S]*st\.categoriesLoaded = false/);
});

test("wire types mirror the backend contract", () => {
  assert.match(api, /interface WireTx \{/);
  assert.match(api, /amountFen: number/);
  assert.match(api, /interface WireLedgerMonth \{/);
  assert.match(api, /entries: WireTx\[\]/);
  assert.match(api, /interface WireBudget \{/);
  assert.match(api, /interface WireBudgets \{/);
  assert.match(api, /budgets: WireBudget\[\]/);
  assert.match(api, /interface WireCategory \{/);
  assert.match(api, /interface WireCategories \{/);
  assert.match(api, /categories: WireCategory\[\]/);
});

test("ledger view has the two-view switch, analysis section and day groups", () => {
  assert.match(view, /watch\(month, \(m\) => loadLedger\(monthKeyOf\(m\)\), \{ immediate: true \}\)/);
  // 两个视图共用 view-switch 分段控件（与目标页同款），主操作随视图变化。
  assert.match(view, /const view = ref<"overview" \| "budget">\("overview"\)/);
  assert.match(view, /class="view-switch" aria-label="ledger"/);
  assert.match(view, /C\.ledgerViewOverview/);
  assert.match(view, /C\.ledgerViewBudget/);
  // 流水分析：与上月对比三卡 + 双栏图表，全部由当月流水推导；
  // 左折线同番茄专注 focus-trend 语法（面积+细线+圆点+HTML 刻度行），
  // 右分类柱同进度统计 weekbar 语法（--event-bg 底 + 分类色顶边）。
  assert.match(view, /monthTotals\(st\.txs\)/);
  assert.match(view, /catBarSeries\(st\.txs, st\.categories\)/);
  // 总览挂载即拉分类清单（柱图按清单出柱），且清单就绪前不出图——
  // 空清单会把支出全折进「其他」，那是错的呈现。
  assert.match(view, /onMounted\(loadCategories\)/);
  assert.match(view, /!st\.categoriesLoaded && !st\.categoriesLoadError/);
  assert.match(view, /dailyExpSeries\(st\.txs, monthKeyOf\(month\.value\)\)/);
  assert.match(view, /prevMonthKey\(monthKeyOf/);
  assert.match(view, /class="ledger-mom"/);
  assert.match(view, /class="ledger-charts"/);
  assert.match(view, /class="ledger-trend"/);
  // 两图同高契约：柱区固定 100px，折线 viewBox 高 100、宽度由
  // ResizeObserver 实测栏宽 1:1 写入（不再随宽度等比伸缩）。
  assert.match(view, /ResizeObserver/);
  assert.match(view, /:viewBox="`0 0 \$\{trendW\} \$\{LINE_H\}`"/);
  assert.match(view, /class="ledger-catbar"/);
  assert.match(view, /class="ledger-catbar-axis"/);
  assert.match(view, /fmtBarMoney\(b\.amountFen\)/);
  assert.match(view, /class="line"/);
  assert.match(view, /class="area"/);
  assert.match(view, /:data-color="catColorOf\(b\.category\)"/);
  // 颜色从服务端分类清单解析，清单外旧名回落 slate。
  assert.match(view, /colorOf\(st\.categories, name\)/);
  // 流水列表：笔数取代操作提示文案；记一笔主操作在工具栏（无悬浮 FAB，
  // 避免同页两个主操作）。
  assert.match(view, /groupTxByDay\(st\.txs\)/);
  assert.match(view, /class="ledger-row"/);
  assert.match(view, /C\.ledgerFlowCount\.replace/);
  assert.match(view, /class="primary-button workspace-cta"/);
  // N 键唤起记一笔；组件级监听，切走即失效。
  assert.match(view, /e\.key === "n" \|\| e\.key === "N"/);
  assert.match(view, /window\.addEventListener\("keydown", onKey\)/);
  assert.match(view, /window\.removeEventListener\("keydown", onKey\)/);
});

test("ledger budget view manages long-term budgets against displayed month", () => {
  // 进预算视图才拉预算与分类清单；花费从当月流水推导（与三卡同口径）。
  assert.match(view, /if \(v === "budget"\) \{/);
  assert.match(view, /loadBudgets\(\)[\s\S]*?loadCategories\(\)/);
  assert.match(view, /spendByCategory\(st\.txs\)/);
  // 内联表单：添加/编辑共用；分类用胶囊选（与记一笔同款语法），
  // 添加时只列还没预算的分类（budgetCatChoices），编辑锁定当前分类。
  assert.match(view, /mode: "add" \| "edit"/);
  assert.match(view, /budgetCatChoices/);
  assert.match(view, /new Set\(st\.budgets\.map\(\(b\) => b\.category\)\)[\s\S]*?!budgeted\.has\(c\.name\)/);
  assert.match(view, /class="ledger-chip"/);
  assert.match(view, /budgetForm\.category = c\.name/);
  assert.match(view, /await setBudget\(category, fen\)/);
  assert.match(view, /removeBudget\(b\.category\)/);
  // 进度条封顶 100%，超支用 --neg 区分。
  assert.match(view, /Math\.min\(100, budgetPct\(b\)\)/);
  assert.match(view, /'is-over': budgetPct\(b\) > 100/);
  // 无预算的分类支出单列，可一键预填表单。
  assert.match(view, /C\.ledgerBudgetNoBudget/);
  assert.match(view, /prefillBudget\(u\.category\)/);
  // 预算卡片区有区块标题（与无预算区、分类区同语法），计数在右。
  assert.match(view, /C\.ledgerBudgetList/);
  // 添加预算只有工具栏主按钮一个入口：页内不再放虚线添加行
  //（总览去 FAB 的同款决定，主操作唯一）。
  assert.doesNotMatch(view, /class="goal-add-row"/);
  // 金额输入唤小数键盘（inputmode），与记一笔金额键同款体验。
  assert.match(view, /inputmode="decimal"/);
});

test("ledger budget view manages the workspace category list", () => {
  // 分类区块折叠在预算列表之后（低频管理默认收起）：目标页同款折叠范式。
  assert.match(view, /catManageOpen/);
  assert.match(view, /:aria-expanded="catManageOpen"/);
  assert.match(view, /C\.ledgerCatManageUnfold/);
  // 分类区块：行内改名（输入框 + 确认）、删除、底部添加。
  assert.match(view, /class="ledger-catrows"/);
  assert.match(view, /renamingId === c\.id/);
  assert.match(view, /await renameCategory\(renamingId\.value, name\)/);
  assert.match(view, /await removeCategory\(c\.id\)/);
  assert.match(view, /await addCategory\(name\)/);
  // 分类选项以服务端清单为准：编辑模式锁定当前分类，清单外旧预算名
  // slate 兜底显示。
  assert.match(view, /hit \?\? \{ id: -1, name: form\.category, color: "slate" \}/);
  // 收入不占分类的老契约不受影响。
  assert.match(view, /groupTxByDay\(st\.txs\)/);
});

test("ledger sheet is a bottom sheet with keypad and category chips", () => {
  assert.match(sheet, /<dialog ref="refEl" class="ledger-sheet"/);
  assert.match(sheet, /class="ledger-keypad"/);
  assert.match(sheet, /class="ledger-chip"/);
  assert.match(sheet, /keyIn\(e\.key\)/);
  assert.match(sheet, /save\(!e\.shiftKey\)/);
  assert.match(sheet, /await addTx\(input\)/);
  assert.match(sheet, /await saveTx\(props\.tx\.id, input, props\.tx\.version\)/);
  // 分类胶囊来自服务端清单（首开拉一次，幂等）。
  assert.match(sheet, /v-for="c in st\.categories"/);
  assert.match(sheet, /loadCategories\(\)/);
  // 收入不占分类；日期不晚于今天。
  assert.match(sheet, /category: type\.value === "exp" \? category\.value : ""/);
  assert.match(sheet, /date\.value > todayStr/);
});

test("copy has the ledger strings", () => {
  for (const key of [
    "ledger", "ledgerSubtitle", "ledgerFlow", "ledgerFlowCount", "ledgerExp", "ledgerInc", "ledgerNet",
    "ledgerAdd", "ledgerEdit", "ledgerEmpty", "ledgerAmountError", "ledgerCatError",
    "ledgerSaveAnother", "ledgerLoadFailed", "txAdded", "txUpdated", "txDeleted", "txSaveFailed",
    "ledgerViewOverview", "ledgerViewBudget", "ledgerAnalysis", "ledgerMomVs", "ledgerMomNew",
    "ledgerDailyExp", "ledgerCatRank", "ledgerCatRankEmpty", "ledgerBudgetHint", "ledgerBudgetAdd",
    "ledgerBudgetList",
    "ledgerBudgetEdit", "ledgerBudgetDelete", "ledgerBudgetCategory", "ledgerBudgetAmount",
    "ledgerBudgetLeft", "ledgerBudgetOver", "ledgerBudgetEmpty", "ledgerBudgetNoBudget",
    "ledgerBudgetSetFor", "ledgerBudgetCategoryError", "ledgerBudgetAmountError",
    "ledgerBudgetLoadFailed", "ledgerBudgetSaveFailed",
    "ledgerCats", "ledgerCatManageFold", "ledgerCatManageUnfold", "ledgerCatAdd", "ledgerCatNamePh", "ledgerCatNameError",
    "ledgerCatRename", "ledgerCatDelete", "ledgerCatSaveFailed", "ledgerCatLoadFailed",
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
  for (const cls of [".ledger-sheet", ".ledger-keypad", ".ledger-chip", ".ledger-row", ".ledger-mom", ".ledger-charts", ".ledger-trend", ".ledger-catbar", ".ledger-catbar-axis", ".ledger-catrows", ".budget-card", ".budget-track", ".budget-form"]) {
    assert.match(css, new RegExp(`\\${cls}[ .{,:\\[]`), `planner.css 缺少 ${cls}`);
  }
  // 双栏图表在窄屏收成单栏。
  assert.match(css, /@media \(max-width: 900px\)[\s\S]*?\.ledger-charts \{\s*grid-template-columns: 1fr;/);
  // 折线同 focus-trend：主色细线（non-scaling-stroke）+ 浅色面积。
  assert.match(css, /\.ledger-trend \.line \{[\s\S]*?stroke: var\(--accent\)[\s\S]*?vector-effect: non-scaling-stroke/);
  assert.match(css, /\.ledger-trend \.area \{[\s\S]*?fill: var\(--accent-soft\)/);
  // 分类柱同 weekbar：--event-bg 柱底 + 2px 分类色顶边。
  assert.match(css, /\.ledger-catbar \.b \{[\s\S]*?background: var\(--event-bg, var\(--accent-soft\)\)[\s\S]*?border-top: 2px solid var\(--event-line, var\(--accent\)\)/);
  // 六色 [data-color] 编码复用既有变量，不新增主色。
  assert.match(css, /\.ledger-chip \.ledger-dot \{/);
  assert.match(css, /background: var\(--event-line\)/);
  assert.match(css, /\.budget-track i\.is-over \{/);
  assert.match(css, /background: var\(--neg\)/);
  // 总览无悬浮 FAB：主操作唯一（工具栏右上），页底不需要大留白。
  assert.doesNotMatch(css, /\.ledger-fab/);
});
