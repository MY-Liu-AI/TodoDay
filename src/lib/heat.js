/* 完成热力格的数据。
 *
 * 左边那本月历回答的是「前面还堆着多少」，这块格子回答的是反方向的那个问题：
 * 「过去这些天，我到底做掉了多少」。清单本身答不了 —— 勾掉的事被「清理已完成」
 * 一按就没了，只靠 items 画出来的是一整片空白。所以除今天以外的每一天都由固定
 * 种子生成，今天那一格永远从真实清单里数：勾掉一件，最后一格立刻变深。
 */
import Kit from './kit.js';
import { DAY, TODAY, midnight } from './todo.js';

export const WEEKS = 18;

/* 从 18 周前的那个周一起算。周一开头是为了和月历对齐 —— 同一份数据的两个视图，
   起点不一样的话，眼睛得在两个坐标系之间换算。 */
const MON = (new Date(TODAY).getDay() + 6) % 7;
export const DAYS = Array.from({ length: WEEKS * 7 },
  (_, k) => midnight(TODAY - (MON + (WEEKS - 1) * 7) * DAY + k * DAY));

const SEEDED = (() => {
  const r = Kit.rng(0x7ad0), h = {};
  for (const ts of DAYS) {
    if (ts >= TODAY) break;
    const wd = new Date(ts).getDay(), rest = wd === 0 || wd === 6;
    /* 「有没有做事」和「做了多少」得是两次独立的抽签。同一个 x 既定生死又定
       大小的话，活下来的那些天全挤在阈值上方，整块格子会是一片均匀的中绿。
       y*y 把分布压向低位：多数日子一两件，偶尔一天九件，这才像人过的日子。 */
    const x = r(), y = r();
    h[ts] = x < (rest ? .6 : .24) ? 0 : Math.ceil(y * y * (rest ? 3.5 : 8.5));
  }
  return h;
})();

/* 种子是底子，真实勾掉的事加在上面：昨天补勾的一件也会让昨天那格变深。 */
export function counts(items) {
  const c = { ...SEEDED };
  items.forEach(i => {
    if (!i.done || !i.at) return;
    const d = midnight(i.at);
    if (d >= DAYS[0] && d <= TODAY) c[d] = (c[d] || 0) + 1;
  });
  return c;
}

export const level = n => (!n ? 0 : n < 3 ? 1 : n < 5 ? 2 : n < 8 ? 3 : 4);

/* 当前连续有进展的天数。今天还没勾不算断 —— 一天刚开始就把纪录清零太伤人。 */
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
export function recent(c, n) {
  let s = 0;
  for (let k = 0; k < n; k++) s += c[midnight(TODAY - k * DAY)] || 0;
  return s;
}
