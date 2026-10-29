/* 习惯的纯逻辑：本周打卡条、下次日期、星期短名。放 lib 里是为了
   tests/habits.test.mjs 能做真单测（node:test 只能 import JS 不能
   import TS）。日期口径与 todo.js 一致：本地日历日。 */

import { DAY, midnight } from "./todo.js";

/* 本周一到周日的七格（打卡条用）。ts 是毫秒时间戳，date 是 YYYY-MM-DD，
   weekday 是 1..7（周一=1 … 周日=7），today 标记今天那一格。 */
export function weekStrip(now = Date.now()) {
  const d = new Date(midnight(now));
  // JS getDay：周日=0 … 周六=6 → 本周一 = 今天 - ((getDay()+6)%7)。
  const monday = midnight(now) - ((d.getDay() + 6) % 7) * DAY;
  return Array.from({ length: 7 }, (_, i) => {
    const ts = monday + i * DAY;
    const x = new Date(ts);
    return {
      ts,
      date: `${x.getFullYear()}-${String(x.getMonth() + 1).padStart(2, "0")}-${String(x.getDate()).padStart(2, "0")}`,
      weekday: i + 1,
      today: ts === midnight(now),
    };
  });
}

/* 从今天（含今天）起下一次落在 weekdays（[1..7]）里的日期；今天命中就
   返回今天。返回 { ts, date }；weekdays 为空返回 null。 */
export function nextOccurrence(weekdays, now = Date.now()) {
  if (!weekdays || !weekdays.length) return null;
  const base = midnight(now);
  const cur = new Date(base).getDay(); // 周日=0 … 周六=6
  const curWD = (cur + 6) % 7 + 1; // 转 1..7（周一=1）
  for (let i = 0; i < 7; i++) {
    const wd = ((curWD - 1 + i) % 7) + 1;
    if (weekdays.includes(wd)) {
      const ts = base + i * DAY;
      const x = new Date(ts);
      return {
        ts,
        date: `${x.getFullYear()}-${String(x.getMonth() + 1).padStart(2, "0")}-${String(x.getDate()).padStart(2, "0")}`,
      };
    }
  }
  return null;
}

/* 星期几（1..7）的短名（打卡条小格用）。 */
export const WEEKDAY_SHORT = ["一", "二", "三", "四", "五", "六", "日"];
