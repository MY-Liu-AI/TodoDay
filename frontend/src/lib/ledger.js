/* 记账本的纯函数：金额格式化、按月/按天聚合、分类色映射。
 * 与 demo（design/expense-ledger-simple.html）同源；放在 lib 里是为了让
 * tests/ledger.test.mjs 能直接做单元测试（node:test 不能 import .ts/.vue）。
 * 分类色是数据编码：六色板与日历分类色同源（palette.js 的 [data-color]），
 * 主操作仍只有青绿一色。
 */

/* 六分类：name 即存进服务端的 category（自由文本），color 仅作展示编码。 */
export const LEDGER_CATS = [
  { name: "餐饮", color: "peach" },
  { name: "通勤", color: "slate" },
  { name: "居家", color: "teal" },
  { name: "购物", color: "coral" },
  { name: "娱乐", color: "cyan" },
  { name: "学习", color: "yellow" },
];

const CAT_COLORS = Object.fromEntries(LEDGER_CATS.map((c) => [c.name, c.color]));

/* 未识别分类回落 slate：分类是用户数据，不能因为没匹配上就渲染失败。 */
export function catColor(name) {
  return CAT_COLORS[name] || "slate";
}

/* 分 → "¥1,234.5"。maximumFractionDigits=2 让整数不带小数尾。 */
export function fmtMoney(fen) {
  return "¥" + (fen / 100).toLocaleString("zh-CN", { maximumFractionDigits: 2 });
}

/* Date（或毫秒时间戳）→ "YYYY-MM"，与服务端 month 参数同约定。 */
export function monthKeyOf(d) {
  const dt = d instanceof Date ? d : new Date(d);
  return dt.getFullYear() + "-" + String(dt.getMonth() + 1).padStart(2, "0");
}

/* "YYYY-MM" → "2026年10月"。手写而不靠 toLocaleDateString：node 测试环境
 * 的 ICU 不可控，展示串要确定。 */
export function fmtMonthTitle(monthKey) {
  const [y, m] = monthKey.split("-");
  return y + "年" + parseInt(m, 10) + "月";
}

const WEEKDAYS = "日一二三四五六";

/* "YYYY-MM-DD" → "10月26日 周日"（按本地日历解释，与 entry_date 同约定）。 */
export function fmtDayHead(dateStr) {
  const d = new Date(dateStr + "T00:00:00");
  return d.getMonth() + 1 + "月" + d.getDate() + "日 周" + WEEKDAYS[d.getDay()];
}

/* 列表 → 按天分组的视图模型。输入假定已按服务端口径排好序
 * （entry_date 倒序、同日内新记的在前）；分组保持该顺序，组的当日
 * 小计与列表同口径（sum of live）。 */
export function groupTxByDay(txs) {
  const groups = [];
  for (const tx of txs) {
    let g = groups.find((x) => x.date === tx.date);
    if (!g) {
      g = { date: tx.date, items: [], exp: 0, inc: 0 };
      groups.push(g);
    }
    g.items.push(tx);
    if (tx.type === "exp") g.exp += tx.amountFen;
    else g.inc += tx.amountFen;
  }
  return groups;
}

/* 当月三卡汇总：与 groupTxByDay 同一份数据推导，三卡恒等于列表之和。 */
export function monthTotals(txs) {
  let exp = 0;
  let inc = 0;
  for (const tx of txs) {
    if (tx.type === "exp") exp += tx.amountFen;
    else inc += tx.amountFen;
  }
  return { exp, inc, net: inc - exp };
}

/* "YYYY-MM" → 上一个月的 "YYYY-MM"（1 月回退到上一年 12 月）。 */
export function prevMonthKey(monthKey) {
  const [y, m] = monthKey.split("-").map(Number);
  return m === 1 ? y - 1 + "-12" : y + "-" + String(m - 1).padStart(2, "0");
}

/* 当月支出按分类求和：{ 分类: 分 }。只计支出——收入不占分类。 */
export function spendByCategory(txs) {
  const out = {};
  for (const tx of txs) {
    if (tx.type !== "exp") continue;
    out[tx.category] = (out[tx.category] || 0) + tx.amountFen;
  }
  return out;
}

/* 分类支出排行：按金额降序，pct = 占总支出百分比（0–100，一位小数）。
 * 超过 topN 的分类聚合进「其他」——邮件导入会造出任意分类，排行不能
 * 无限长。当月无支出返回空数组。 */
export function catExpRank(txs, topN = 8) {
  const spend = spendByCategory(txs);
  const total = Object.values(spend).reduce((a, b) => a + b, 0);
  if (!total) return [];
  const rows = Object.entries(spend)
    .map(([category, amountFen]) => ({ category, amountFen }))
    .sort((a, b) => b.amountFen - a.amountFen);
  const head = rows.slice(0, topN);
  const tail = rows.slice(topN);
  if (tail.length) {
    head.push({
      category: "其他",
      amountFen: tail.reduce((a, r) => a + r.amountFen, 0),
    });
  }
  return head.map((r) => ({ ...r, pct: Math.round((r.amountFen / total) * 1000) / 10 }));
}

/* 环比涨幅百分比：(cur - prev) / prev * 100，四舍五入取整。
 * prev <= 0（上月无数据）返回 null——没有可对比的基数，前端显示「—」。 */
export function momPct(cur, prev) {
  if (prev <= 0) return null;
  return Math.round(((cur - prev) / prev) * 100);
}
