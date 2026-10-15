/* 完成热力格的数据。
 *
 * 全栈版本：不再有固定种子合成历史。每一天的计数来自服务端 task_events
 * 聚合（工作区时区口径），今天那一格与过去 18 周使用同一份真实数据 —
 * 热力、连续天数、近 7 天统计同源（见 FULLSTACK.md）。
 */
import { DAY, TODAY, midnight } from './todo.js';

export const WEEKS = 18;

/* 从 18 周前的那个周一起算。周一开头是为了和月历对齐 —— 同一份数据的两个视图，
   起点不一样的话，眼睛得在两个坐标系之间换算。 */
const MON = (new Date(TODAY).getDay() + 6) % 7;
export const DAYS = Array.from({ length: WEEKS * 7 },
  (_, k) => midnight(TODAY - (MON + (WEEKS - 1) * 7) * DAY + k * DAY));

/* 服务端返回 {"YYYY-MM-DD": n}（工作区时区），这里换成按本地午夜毫秒键，
   与 DAYS 网格对齐。 */
/** @param {Record<string, number>} [activity] @returns {Record<number, number>} */
export function countsFromActivity(activity) {
  const c = {};
  for (const [d, n] of Object.entries(activity || {})) {
    if (!n) continue;
    const ts = +new Date(d + 'T00:00:00');
    if (Number.isFinite(ts)) c[ts] = n;
  }
  return c;
}

export const level = n => (!n ? 0 : n < 3 ? 1 : n < 5 ? 2 : n < 8 ? 3 : 4);

/* 当前连续有进展的天数。今天还没勾不算断 —— 一天刚开始就把纪录清零太伤人。 */
/** @param {Record<number, number>} c */
export function streak(c) {
  let n = 0;
  for (let k = DAYS.length - 1; k >= 0; k--) {
    const ts = DAYS[k];
    if (ts > TODAY) continue;
    if (c[ts] > 0) n++;
    else if (ts !== TODAY) break;
  }
  return n;
}

/* 窗口内最长的一段连续。和当前连续放在一起才有参照：光看「连续 2 天」不知道
   2 天是多是少，旁边写着「最长 9 天」就知道了。 */
/** @param {Record<number, number>} c */
export function best(c) {
  let n = 0, m = 0;
  for (const ts of DAYS) {
    if (ts > TODAY) break;
    n = c[ts] > 0 ? n + 1 : 0;
    if (n > m) m = n;
  }
  return m;
}

/* 最近 n 天完成多少件（含今天）。 */
/** @param {Record<number, number>} c @param {number} n */
export function recent(c, n) {
  let s = 0;
  for (let k = 0; k < n; k++) s += c[midnight(TODAY - k * DAY)] || 0;
  return s;
}
