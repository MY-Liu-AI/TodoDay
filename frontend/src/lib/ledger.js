/* 记账本的纯函数：金额格式化、按月/按天聚合、分类色映射。
 * 与 demo（design/expense-ledger-simple.html）同源；放在 lib 里是为了让
 * tests/ledger.test.mjs 能直接做单元测试（node:test 不能 import .ts/.vue）。
 * 分类色是数据编码：六色板与日历分类色同源（palette.js 的 [data-color]），
 * 主操作仍只有青绿一色。
 */

/* 默认六分类（用户指定，2026-10-29）：与服务端 store/category.go 的
 * 播种清单一致——首次读取 /ledger/categories 时服务端播种的就是这份；
 * 此后分类存在服务端（ledger_categories 表），用户可在分类预算视图
 * 增/改名/删，邮箱账单的 Kimi 解析按调整后的清单收敛。
 * name 即存进服务端的 category；color 仅作展示编码，新增分类由服务端
 * 按同序色板自动分配。 */
export const LEDGER_CATS = [
  { name: "餐饮", color: "peach" },
  { name: "交通", color: "slate" },
  { name: "购物", color: "coral" },
  { name: "住房", color: "teal" },
  { name: "娱乐", color: "cyan" },
  { name: "其他", color: "yellow" },
];

/* 按分类清单查颜色：清单来自服务端（含用户新增的分类）；
 * 清单外的历史分类名（如改名前的旧名）回落 slate——分类是用户数据，
 * 不能因为没匹配上就渲染失败。 */
export function colorOf(categories, name) {
  const hit = categories.find((c) => c.name === name);
  return hit ? hit.color : "slate";
}

const CAT_COLORS = Object.fromEntries(LEDGER_CATS.map((c) => [c.name, c.color]));

/* 未识别分类回落 slate：分类是用户数据，不能因为没匹配上就渲染失败。
 * 兼容旧调用（只看默认清单）；新代码请用 colorOf(分类清单, 名)。 */
export function catColor(name) {
  return CAT_COLORS[name] || "slate";
}

/* 分 → "¥1,234.5"。maximumFractionDigits=2 让整数不带小数尾。 */
export function fmtMoney(fen) {
  return "¥" + (fen / 100).toLocaleString("zh-CN", { maximumFractionDigits: 2 });
}

/* 图表柱上标签的紧凑金额：≥¥10 取整元（千分位），<¥10 保留两位小数。
   窄柱上挂全格式金额会互相碰撞，精确值留在 tooltip 的 fmtMoney。 */
export function fmtBarMoney(fen) {
  if (fen >= 1000) return "¥" + Math.round(fen / 100).toLocaleString("en-US");
  return "¥" + (fen / 100).toFixed(2);
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

/* 分类柱序列：工作区分类清单全量出柱——零支出的分类也展示（高度 0），
 * 让用户看到完整的分类盘子。清单外名字的旧支出（改名前的自由文本）
 * 折进「其他」（清单里没有「其他」则补一条）。按金额降序，同额保持
 * 清单顺序；pct = 占总支出百分比（0–100，一位小数，无支出全 0）。 */
export function catBarSeries(txs, categories) {
  const spend = spendByCategory(txs);
  const total = Object.values(spend).reduce((a, b) => a + b, 0);
  const rows = categories.map((c, i) => ({ category: c.name, amountFen: spend[c.name] || 0, i }));
  const inList = new Set(categories.map((c) => c.name));
  const stray = Object.entries(spend)
    .filter(([k]) => !inList.has(k))
    .reduce((a, [, v]) => a + v, 0);
  if (stray) {
    const other = rows.find((r) => r.category === "其他");
    if (other) other.amountFen += stray;
    else rows.push({ category: "其他", amountFen: stray, i: rows.length });
  }
  rows.sort((a, b) => b.amountFen - a.amountFen || a.i - b.i);
  return rows.map((r) => ({
    category: r.category,
    amountFen: r.amountFen,
    pct: total ? Math.round((r.amountFen / total) * 1000) / 10 : 0,
  }));
}

/* 当月每日支出序列：按日聚合（无支出为 0），覆盖当月每一天——
 * 折线图要连续性。月份天数按本地日历（new Date(y, m, 0) 回退语义），
 * 与 entry_date 同约定。只计支出。 */
export function dailyExpSeries(txs, monthKey) {
  const [y, m] = monthKey.split("-").map(Number);
  const days = new Date(y, m, 0).getDate();
  const byDay = {};
  for (const tx of txs) {
    if (tx.type !== "exp") continue;
    byDay[tx.date] = (byDay[tx.date] || 0) + tx.amountFen;
  }
  const out = [];
  for (let d = 1; d <= days; d++) {
    const date = monthKey + "-" + String(d).padStart(2, "0");
    out.push({ date, day: d, amountFen: byDay[date] || 0 });
  }
  return out;
}

/* 环比涨幅百分比：(cur - prev) / prev * 100，四舍五入取整。
 * prev <= 0（上月无数据）返回 null——没有可对比的基数，前端显示「—」。 */
export function momPct(cur, prev) {
  if (prev <= 0) return null;
  return Math.round(((cur - prev) / prev) * 100);
}
