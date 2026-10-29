/* 目标列表页统计图的纯逻辑：里程碑甘特图。
   放 lib 里是为了 tests/goalcharts.test.mjs 能做真单测（node:test 只能
   import JS 不能 import TS）。日期口径与 todo.js 一致：本地日历日。
   输入只依赖已有的 API 形状（WireGoal），不改服务端。 */

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
/* ts 所在周的周一 0 点。 */
function mondayOf(ts) {
  return midnight(ts) - (weekdayOf(ts) - 1) * DAY;
}
const pct = (x) => Math.round(x * 10000) / 100;
const clampPct = (x) => Math.max(0, Math.min(100, pct(x)));
const M = (ts) => `${new Date(ts).getMonth() + 1}/${new Date(ts).getDate()}`;

/* 单边日期的开放跨度：只有开始 → 向后 7 天；只有截止 → 向前 7 天。
   开放端画成虚线边，读者知道「这端没定」。 */
const OPEN_SPAN = 7 * DAY;
/* 轴太短（全部挤在一两天）时把可读性兜底到 14 天。 */
const MIN_AXIS = 14 * DAY;
/* 轴两端各留 4% 呼吸位。 */
const PAD_RATIO = 0.04;

function msSpan(m) {
  const s = m.start ? tsOf(m.start) : null;
  const e = m.due ? tsOf(m.due) : null;
  if (s !== null && e !== null) return { s, e, kind: "range" };
  if (s !== null) return { s, e: s + OPEN_SPAN, kind: "open-end" };
  if (e !== null) return { s: e - OPEN_SPAN, e, kind: "open-start" };
  return null;
}

/* 里程碑甘特图。goals = 进行中的目标（milestones 含 start/due/
   tasks[{done}]）。返回 null 表示没有可画的目标（调用方给空态）。
   left/width/dateText 对未排期行保持 0/""，模板不用判空。 */
/**
 * @param {Array<{id:number,title:string,milestones:Array<{id:number,title:string,start:string,due:string,tasks:Array<{done:boolean}>}>}>} goals
 * @param {number} [now]
 * @returns {null | {from:number,to:number,todayPos:number|null,ticks:Array<{pos:number,label:string}>,groups:Array<{id:number,title:string,pct:number,rows:Array<{id:number,title:string,kind:string,start:number|null,end:number|null,left:number,width:number,fill:number,dateText:string,done:number,total:number,current:boolean}>}>}}
 */
export function ganttChart(goals, now = Date.now()) {
  const groups = [];
  let lo = null;
  let hi = null;
  for (const g of goals) {
    const rows = [];
    for (const m of g.milestones) {
      const total = m.tasks.length;
      const done = m.tasks.filter((t) => t.done).length;
      const span = msSpan(m);
      if (span) {
        lo = lo === null ? span.s : Math.min(lo, span.s);
        hi = hi === null ? span.e : Math.max(hi, span.e);
      }
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
           锁只是默认顺序，允许跳步）。 */
        current: false,
      });
    }
    let locked = false;
    for (const r of rows) {
      r.current = !locked && r.total > 0 && r.done < r.total;
      if (r.done < r.total) locked = true;
    }
    const all = g.milestones.reduce((n, m) => n + m.tasks.length, 0);
    const allDone = g.milestones.reduce((n, m) => n + m.tasks.filter((t) => t.done).length, 0);
    groups.push({ id: g.id, title: g.title, pct: all ? Math.round((allDone / all) * 100) : 0, rows });
  }
  if (!groups.length) return null;
  if (lo === null) {
    /* 一个带日期的里程碑都没有：轴退化成「进度条列表」，行内只有未排期块。 */
    return { from: 0, to: 0, todayPos: null, ticks: [], groups };
  }
  let span = hi - lo;
  if (span < MIN_AXIS) {
    const c = (lo + hi) / 2;
    lo = c - MIN_AXIS / 2;
    hi = c + MIN_AXIS / 2;
    span = MIN_AXIS;
  }
  const pad = span * PAD_RATIO;
  const from = lo - pad;
  const to = hi + pad;
  const todayTs = midnight(now);
  /* 今天超出轴范围（含两端呼吸位）就不画线，免得把「还很远」误导成「就在边上」。 */
  const todayRaw = (todayTs - from) / (to - from);
  const todayPos = todayRaw < 0 || todayRaw > 1 ? null : pct(todayRaw);
  /* 刻度只从今天起排（今天前不要时间点）：9 个等距刻度均匀覆盖
     [今天, 轴尾]——首刻度就是今天的日期（与今天线同位）、尾刻度落在时间轨
     右缘 +40px（紧邻进度列，见 planner.css 的刻度轨加宽）。今天在轴外时
     退化为整轴刻度，避免画出无标签的轴。 */
  const tickStart = todayTs >= from && todayTs < to ? todayTs : from;
  const ticks = Array.from({ length: 9 }, (_, k) => {
    const ts = tickStart + (k / 8) * (to - tickStart);
    return { pos: pct((ts - from) / (to - from)), label: M(ts) };
  });
  for (const g of groups) {
    for (const r of g.rows) {
      if (r.kind === "undated") continue;
      /* 开放端越出轴的部分收进轴内（开放语义由虚线边表达）。 */
      const s = Math.max(r.start, from);
      const e = Math.min(r.end, to);
      r.left = clampPct((s - from) / (to - from));
      r.width = Math.max(0.8, pct((e - s) / (to - from)));
      r.dateText = r.kind === "open-end" ? `${M(r.start)} →` : r.kind === "open-start" ? `→ ${M(r.end)}` : `${M(r.start)} – ${M(r.end)}`;
    }
  }
  return { from, to, todayPos, ticks, groups };
}
