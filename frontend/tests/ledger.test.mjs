import test from "node:test";
import assert from "node:assert/strict";
import { readFileSync } from "node:fs";
import {
  LEDGER_CATS,
  catColor,
  fmtDayHead,
  fmtMoney,
  fmtMonthTitle,
  groupTxByDay,
  monthKeyOf,
  monthTotals,
} from "../src/lib/ledger.js";

/* 记账本契约（2026-10-26）：
   - 简约版 demo 交互落地：月份切换 + 支出/收入/结余三卡 + 按天分组流水 +
     底部记一笔 sheet（数字键盘、六色分类胶囊、Enter/Shift+Enter/N/Esc）；
   - 数据全在服务端（per-workspace），client_id 幂等、version 乐观并发、
     软删 + 撤销；三卡由列表推导，恒与服务端同口径；
   - 分类色是 [data-color] 数据编码，主操作只有青绿一色。 */

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

test("wire types mirror the backend contract", () => {
  assert.match(api, /interface WireTx \{/);
  assert.match(api, /amountFen: number/);
  assert.match(api, /interface WireLedgerMonth \{/);
  assert.match(api, /entries: WireTx\[\]/);
});

test("ledger view renders month bar, three stat cards and day groups", () => {
  assert.match(view, /watch\(month, \(m\) => loadLedger\(monthKeyOf\(m\)\), \{ immediate: true \}\)/);
  // 三卡由列表推导（恒与服务端同口径），不单独存服务端 stats。
  assert.match(view, /monthTotals\(st\.txs\)/);
  assert.match(view, /groupTxByDay\(st\.txs\)/);
  assert.match(view, /class="ledger-stats"/);
  assert.match(view, /class="ledger-row"/);
  assert.match(view, /class="ledger-fab"/);
  // N 键唤起记一笔；组件级监听，切走即失效。
  assert.match(view, /e\.key === "n" \|\| e\.key === "N"/);
  assert.match(view, /window\.addEventListener\("keydown", onKey\)/);
  assert.match(view, /window\.removeEventListener\("keydown", onKey\)/);
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
  for (const cls of [".ledger-sheet", ".ledger-keypad", ".ledger-chip", ".ledger-fab", ".ledger-row", ".ledger-stats"]) {
    assert.match(css, new RegExp(`\\${cls}[ .{,:\\[]`), `planner.css 缺少 ${cls}`);
  }
  // 六色 [data-color] 编码复用既有变量，不新增主色。
  assert.match(css, /\.ledger-chip \.ledger-dot \{/);
  assert.match(css, /background: var\(--event-line\)/);
  assert.doesNotMatch(css, /\.ledger-fab[^}]*#[0-9a-fA-F]{3,6}/);
});
