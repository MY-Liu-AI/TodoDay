/* 目标列表页统计图的纯逻辑：里程碑甘特图。
   放 lib 里是为了 tests/goalcharts.test.mjs 能做真单测（node:test 只能
   import JS 不能 import TS）。日期口径与 todo.js 一致：本地日历日。
   输入只依赖已有的 API 形状（WireGoal），不改服务端。

   设计（2026-11-11 效率复核，对齐 design/goal-gantt-design.md）：
   - 视窗 = 过去固定 7 天 + 未来至少 28 天（随最远截止日延展）。今天
     因此稳定在轴左侧 ~1/5 处；更早的条带截到轴首，左缘虚线表达
     「延伸进过去」，逾期多久看 overdueDays 文字而非条带长度。
   - 已完成里程碑（total>0 且 done>=total；total=0 视为未拆解）不占行，
     折进组头 doneCount；全完成的目标整组退出。行数只跟「未完成」走。 */

import { DAY, midnight } from "./todo.js";

/* YYYY-MM-DD ↔ 本地时间戳（与 habits 页同一写法，不走 Date.parse
   的时区歧义）。 */
function tsOf(date) {
  const [y, m, d] = date.split("-").map(Number);
  return midnight(new Date(y, m - 1, d));
}
/* 周一=1 … 周日=7（与 weekStrip 同口径）。 */
function weekdayOf(ts) {
  return (new Date(ts).getDay() + 6) % 7 + 1;
}
const pct = (x) => Math.round(x * 10000) / 100;
const clampPct = (x) => Math.max(0, Math.min(100, pct(x)));
const M = (ts) => `${new Date(ts).getMonth() + 1}/${new Date(ts).getDate()}`;

/* 单边日期的开放跨度：只有开始 → 向后 7 天；只有截止 → 向前 7 天。
   开放端画成虚线边，读者知道「这端没定」。 */
const OPEN_SPAN = 7 * DAY;
/* 轴两端各留 4% 呼吸位。 */
const PAD_RATIO = 0.04;
/* 视窗：过去固定 7 天（更早的一律截断，不再往过去延展）；未来至少
   28 天，随最远截止日延展（截止日必须可见）。 */
const PAST = 7 * DAY;
const FUTURE_MIN = 28 * DAY;
/* 刻度标签密度上限：任意粒度下标签 ≤16 枚（M/d 标签约 30px，常规轨宽
   16 枚不重叠）；日粒度步长按「好看的天数」放大。 */
const MAX_TICK_LABELS = 16;
const DAY_STEPS = [1, 2, 3, 5, 7, 10, 14, 21, 30];
/* 「今天 M/d」尺签与刻度标签的半宽（像素）：尺签脚印内的刻度标签整枚
   隐藏（两者同位即双重文字）。trackPx 未知时先不抑制，量到后再算。 */
const TAB_HALF_PX = 32;
const LABEL_HALF_PX = 15;
/* 日粒度下小刻度（每天一根）的跨度上限：超过就不再画小刻度，只留
   标签刻度，避免几百根 1px 刻度的无谓 DOM。 */
const MAX_MARK_SPAN = 62 * DAY;

function msSpan(m) {
  const s = m.start ? tsOf(m.start) : null;
  const e = m.due ? tsOf(m.due) : null;
  if (s !== null && e !== null) return { s, e, kind: "range" };
  if (s !== null) return { s, e: s + OPEN_SPAN, kind: "open-end" };
  if (e !== null) return { s: e - OPEN_SPAN, e, kind: "open-start" };
  return null;
}

/**
 * 里程碑甘特图。goals = 进行中的目标（milestones 含 start/due/
 * tasks[{done}]）。返回 null 表示没有「未完成」里程碑可画（全完成
 * 的目标整组退出；调用方给空态）。
 * @param {Array<{id:number,title:string,milestones:Array<{id:number,title:string,start:string,due:string,tasks:Array<{done:boolean}>}>}>} goals
 * @param {number} [now]
 * @param {number} [trackPx] 时间轨实际像素宽；>0 时按尺签/标签实际半宽
 *   换算 clash（脚印内的刻度标签由模板隐藏），未知时不抑制。
 * @param {"day"|"week"} [gran] 刻度粒度：日 = 等距天数步长；周 = 每周一。
 * @returns {null | {from:number,to:number,todayPos:number,todayTabPos:number,todayLabel:string,months:Array<{pos:number,width:number,label:string}>,ticks:Array<{pos:number,label:string,week:boolean,clash:boolean}>,marks:number[],weekends:Array<{pos:number,width:number}>,groups:Array<{id:number,title:string,pct:number,doneCount:number,rows:Array<{id:number,title:string,kind:string,start:number|null,end:number|null,left:number,width:number,fill:number,dateText:string,done:number,total:number,current:boolean,clipPast:boolean,overdueDays:number|null}>}>}}
 */
export function ganttChart(goals, now = Date.now(), trackPx = 0, gran = "day") {
  const groups = [];
  let hi = null;
  for (const g of goals) {
    const rows = [];
    let doneCount = 0;
    for (const m of g.milestones) {
      const total = m.tasks.length;
      const done = m.tasks.filter((t) => t.done).length;
      /* 已完成不占行，折进组头小签「已完成 n 项」。total=0 视为未拆解，
         仍占行。 */
      if (total > 0 && done >= total) {
        doneCount += 1;
        continue;
      }
      const span = msSpan(m);
      if (span) hi = hi === null ? span.e : Math.max(hi, span.e);
      rows.push({
        id: m.id,
        title: m.title,
        kind: span ? span.kind : "undated",
        start: span ? span.s : null,
        end: span ? span.e : null,
        left: 0,
        width: 0,
        dateText: "",
        done,
        total,
        fill: total ? done / total : 0,
        /* 「当前步」：前面全部完成、自己还有未完成项（与详情页同规则，
           锁只是默认顺序，允许跳步）。在完成项被过滤后，即第一个未完成的
           有任务里程碑。 */
        current: false,
        clipPast: false,
        overdueDays: null,
      });
    }
    /* 全完成的目标整组退出进度图（列表页与详情里仍有完整历史）。 */
    if (!rows.length) continue;
    let locked = false;
    for (const r of rows) {
      r.current = !locked && r.total > 0 && r.done < r.total;
      if (r.done < r.total) locked = true;
    }
    const all = g.milestones.reduce((n, m) => n + m.tasks.length, 0);
    const allDone = g.milestones.reduce((n, m) => n + m.tasks.filter((t) => t.done).length, 0);
    groups.push({ id: g.id, title: g.title, pct: all ? Math.round((allDone / all) * 100) : 0, doneCount, rows });
  }
  if (!groups.length) return null;

  /* 视窗轴：过去钉死 7 天（pad 不多占），未来至少 28 天、随最远截止
     延展；轴尾含最后一天整天。今天必在轴内（左侧 ~1/5 处）。 */
  const todayTs = midnight(now);
  let from = todayTs - PAST;
  let to = Math.max(hi !== null ? hi : 0, todayTs + FUTURE_MIN);
  const pad = (to - from) * PAD_RATIO;
  from = Math.max(midnight(from - pad), todayTs - PAST);
  to = midnight(to + pad) + DAY;
  const todayPos = clampPct((todayTs - from) / (to - from));
  const tabHalf = trackPx > 0 ? (TAB_HALF_PX / trackPx) * 100 : 0;
  const labHalf = trackPx > 0 ? (LABEL_HALF_PX / trackPx) * 100 : 0;
  const todayTabPos = tabHalf > 0 ? Math.min(Math.max(todayPos, tabHalf), 100 - tabHalf) : todayPos;

  /* 刻度：铺满整个视窗（不再从今天起排）。日 = 锚定轴首的等距步长
     （1/2/3/5/7/10/14/21/30 天，取标签 ≤16 枚的最小步长，跨月间距
     恒定）；周 = 每周一（超过 16 周则每 k 个周一取一枚）。 */
  const spanDays = (to - from) / DAY;
  let dayStep = 30;
  for (const s of DAY_STEPS) {
    if (spanDays / s <= MAX_TICK_LABELS) {
      dayStep = s;
      break;
    }
  }
  const weekStep = Math.max(1, Math.ceil(spanDays / 7 / MAX_TICK_LABELS));
  const ticks = [];
  for (let ts = from, k = 0; ts < to; ts += DAY, k++) {
    const wd = weekdayOf(ts);
    const hit = gran === "day" ? k % dayStep === 0 : wd === 1 && Math.round(k / 7) % weekStep === 0;
    if (!hit) continue;
    const pos = clampPct((ts - from) / (to - from));
    ticks.push({ pos, label: M(ts), week: wd === 1, clash: tabHalf > 0 && Math.abs(pos - todayPos) < tabHalf + labHalf });
  }

  /* 小刻度：日粒度每天一根（跨度上限内），周粒度与标签刻度同位。 */
  const marks = [];
  if (gran === "day" && spanDays <= MAX_MARK_SPAN / DAY) {
    for (let ts = from; ts < to; ts += DAY) marks.push(clampPct((ts - from) / (to - from)));
  } else {
    for (const t of ticks) marks.push(t.pos);
  }

  /* 月份带：跨月自动分段（每段左边线 + 「YYYY 年 M 月」），段宽按天占比。 */
  const months = [];
  {
    const f = new Date(from);
    let cur = new Date(f.getFullYear(), f.getMonth(), 1);
    while (cur.getTime() < to) {
      const next = new Date(cur.getFullYear(), cur.getMonth() + 1, 1);
      const s = Math.max(cur.getTime(), from);
      const e = Math.min(next.getTime(), to);
      months.push({
        pos: clampPct((s - from) / (to - from)),
        width: pct((e - s) / (to - from)),
        label: `${cur.getFullYear()} 年 ${cur.getMonth() + 1} 月`,
      });
      cur = next;
    }
  }

  /* 周末底纹：仅日粒度（周六日两列低饱和铺底）。 */
  const weekends = [];
  if (gran === "day") {
    for (let ts = from; ts < to; ts += DAY) {
      const dow = new Date(ts).getDay();
      if (dow === 0 || dow === 6) weekends.push({ pos: clampPct((ts - from) / (to - from)), width: pct(DAY / (to - from)) });
    }
  }

  /* 行定位：开始早于过去窗的一律截到轴首（clipPast → 左缘虚线 =
     「延伸进过去」）；逾期天数进 overdueDays（文字表达，不猜条带长度）。 */
  for (const g of groups) {
    for (const r of g.rows) {
      if (r.kind === "undated") continue;
      r.clipPast = r.start < from;
      const s = Math.max(r.start, from);
      r.left = clampPct((s - from) / (to - from));
      r.width = Math.max(0.8, pct((r.end - s) / (to - from)));
      r.dateText = r.kind === "open-end" ? `${M(r.start)} →` : r.kind === "open-start" ? `→ ${M(r.end)}` : `${M(r.start)} – ${M(r.end)}`;
      if (r.end < todayTs) r.overdueDays = Math.round((todayTs - r.end) / DAY);
    }
  }
  return { from, to, todayPos, todayTabPos, todayLabel: M(todayTs), months, ticks, marks, weekends, groups };
}
