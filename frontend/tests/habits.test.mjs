/* 习惯（工作区级定期练习，独立于目标）：lib/habits.js 纯函数真单测 +
   全栈契约断言。场景：习惯「练胸」选每周一 → 未来每周一的日程里都有
   该习惯（服务端滚动生成真实任务实例，计划页/日历/今天清单可见）。 */
import test from "node:test";
import assert from "node:assert/strict";
import { readFileSync } from "node:fs";

import { weekStrip, nextOccurrence, WEEKDAY_SHORT } from "../src/lib/habits.js";

/* 2026-10-27 是周二。 */
const NOW = new Date(2026, 9, 27, 15, 0, 0).getTime();

test("weekStrip returns Monday-to-Sunday of the current week", () => {
  const cells = weekStrip(NOW);
  assert.equal(cells.length, 7);
  assert.equal(cells[0].weekday, 1);
  assert.equal(cells[6].weekday, 7);
  assert.equal(cells[0].date, "2026-10-26"); // 本周一
  assert.equal(cells[1].date, "2026-10-27"); // 今天（周二）
  assert.equal(cells[1].today, true);
  assert.equal(cells[0].today, false);
  assert.equal(cells[6].date, "2026-11-01"); // 本周日
  // 时间戳连续、每天一格。
  for (let i = 1; i < 7; i++) assert.equal(cells[i].ts - cells[i - 1].ts, 864e5);
});

test("weekStrip handles Monday and Sunday boundaries", () => {
  const mon = new Date(2026, 9, 26, 9, 0, 0).getTime();
  assert.equal(weekStrip(mon)[0].today, true);
  const sun = new Date(2026, 9, 25, 9, 0, 0).getTime(); // 周日
  const cells = weekStrip(sun);
  assert.equal(cells[0].date, "2026-10-19"); // 上周一
  assert.equal(cells[6].today, true);
});

test("nextOccurrence picks today when scheduled, else the next matching day", () => {
  // 周三、周四：今天（周二）不命中 → 明天（周三）。
  const n = nextOccurrence([3, 4], NOW);
  assert.equal(n.date, "2026-10-28"); // 周三
  assert.equal(nextOccurrence([4], NOW).date, "2026-10-29"); // 后天周四
  assert.equal(nextOccurrence([2, 4], NOW).date, "2026-10-27"); // 今天周二优先
  assert.equal(nextOccurrence([2], NOW).date, "2026-10-27"); // 今天周二
  assert.equal(nextOccurrence([1], NOW).date, "2026-11-02"); // 下周一
  assert.equal(nextOccurrence([], NOW), null);
  assert.equal(WEEKDAY_SHORT.length, 7);
});

/* ── 全栈契约 ─────────────────────────────────────────────────────────── */

const read = (p) => readFileSync(new URL(p, import.meta.url), "utf8");
const app = read("../src/App.vue");
const view = read("../src/components/HabitsView.vue");
const goalsView = read("../src/components/GoalsView.vue");
const store = read("../src/store.ts");
const api = read("../src/api.ts");
const css = read("../src/styles/planner.css");
const copy = read("../src/copy.ts");

test("habits page is wired into App between goals and focus", () => {
  assert.match(app, /import HabitsView from "\.\/components\/HabitsView\.vue"/);
  assert.match(app, /<HabitsView v-else-if="page === 'habits'" \/>/);
  assert.match(app, /<Icon name="repeat" \/>/);
  // 页头为单行 slim 版（标题 + 同排副标题），页面名在 PAGE_META。
  assert.match(app, /habits: \{ title: C\.habits, subtitle: C\.habitsSubtitle \}/);
  const nav = app.match(/<nav aria-label="workspace"[\s\S]*?<\/nav>/)?.[0] || "";
  const order = ["page === 'goals'", "page === 'habits'", "page === 'focus'"].map((k) => nav.indexOf(k));
  assert.ok(order.every((i) => i >= 0) && order.every((v, i) => i === 0 || v > order[i - 1]),
    `导航次序不对：${order}`);
});

test("habit list shows a week check-in strip per habit", () => {
  assert.match(view, /class="habit-week"/);
  assert.match(view, /toggleHabitOccurrence\(/);
  assert.match(view, /nextText\(h\)/);
  assert.match(view, /missed: !cell\.today && cell\.ts < todayTs/);
  // 读取即展开引擎：进入页面拉习惯列表（服务端滚动补齐未来 8 周实例）。
  assert.match(view, /if \(!st\.habitsLoaded\) void loadHabits\(\)/);
  // 目标详情页不再按模式分流：没有周期事项区，里程碑区对所有目标展示。
  assert.doesNotMatch(goalsView, /goal\.mode/);
  assert.doesNotMatch(goalsView, /routine|Routine/);
});

test("habit add form has title input, weekday chips and validation", () => {
  assert.match(view, /class="habit-add"/);
  assert.match(view, /v-model="draft\.title"/);
  assert.match(view, /habit-chip/);
  assert.match(view, /toggleDay\(draft\.days, d\)/);
  assert.match(view, /C\.habitNameError/);
  assert.match(view, /C\.habitDayError/);
  // 行内编辑。
  assert.match(view, /class="habit-edit"/);
  assert.match(view, /submitEdit\(h\)/);
});

test("store exposes habit actions hitting the habits endpoints", () => {
  for (const fn of ["loadHabits", "addHabit", "saveHabit", "deleteHabit", "toggleHabitOccurrence"]) {
    assert.match(store, new RegExp(`export async function ${fn}\\b`), `store 缺少 ${fn}`);
  }
  assert.match(store, /wsPath\("\/habits"\)/);
  assert.match(store, /wsPath\(`\/habits\/\$\{habitId\}`\)/);
  assert.match(store, /habits\/delete/);
  assert.match(store, /\/tasks\/\$\{taskId\}\/completion/);
  assert.match(store, /habitsLoaded: false/);
  assert.match(store, /registerLogoutHook\(clearHabits\)/);
  assert.doesNotMatch(store, /routine|Routine|goalRoutineMap/);
});

test("api types carry habits with weekdays and no routine progress on goals", () => {
  assert.match(api, /export interface WireHabit \{/);
  assert.match(api, /weekdays: number\[\]; \/\/ \[1\.\.7\] 升序去重/);
  assert.match(api, /export interface WireHabitTask \{/);
  assert.doesNotMatch(api, /routineDone|routineTotal|mode: "parallel" \| "sequential"/);
});

test("backend has habits table, task link and routes", () => {
  const up = read("../../backend/internal/store/migrations/0013_habits.up.sql");
  const down = read("../../backend/internal/store/migrations/0013_habits.down.sql");
  assert.match(up, /CREATE TABLE habits/);
  assert.match(up, /weekday_mask INTEGER NOT NULL CHECK \(weekday_mask BETWEEN 1 AND 127\)/);
  assert.match(up, /UNIQUE \(workspace_id, client_id\)/);
  assert.match(up, /INSERT INTO habits \(([\s\S]*?)\)\s*SELECT \1\s*FROM goal_routines/);
  assert.match(up, /habit_id\s+INTEGER REFERENCES habits/);
  // 旧并行目标归档而非删除：目标壳与决策三问保留可查。
  assert.match(up, /UPDATE goals SET status = 'archived'[\s\S]*?mode = 'parallel'/);
  assert.doesNotMatch(up, /DROP TABLE goals/);
  assert.match(down, /DROP TABLE habits;/);
  const srv = read("../../backend/internal/api/server.go");
  for (const route of [
    "GET /api/workspaces/{wid}/habits",
    "POST /api/workspaces/{wid}/habits",
    "PATCH /api/workspaces/{wid}/habits/{hid}",
    "POST /api/workspaces/{wid}/habits/delete",
  ]) {
    assert.match(srv, new RegExp(route.replace(/\//g, "\\/").replace(/\{/g, "\\{").replace(/\}/g, "\\}")), `server.go 缺路由 ${route}`);
  }
  assert.doesNotMatch(srv, /routines/);
  const st = read("../../backend/internal/store/habits.go");
  assert.match(st, /HabitRollDays = 56/);
  assert.match(st, /ensureHabitOccurrencesTx/);
  assert.match(st, /habit-%d-%s/);
});

test("copy has the habit strings and css has habit styles", () => {
  for (const key of [
    "habits", "habitsSubtitle", "habitAdd", "habitName", "habitRepeat",
    "habitEmpty", "habitAdded", "habitSaved", "habitDeleted",
    "habitNameError", "habitDayError", "habitEdit", "habitDelete",
    "habitNext", "habitLoadFailed", "habitSaveFailed",
  ]) {
    assert.match(copy, new RegExp(`\\b${key}:`), `copy.ts 缺少 ${key}`);
  }
  assert.match(copy, /habitWeekdays: \[/);
  for (const dead of ["goalRoutines", "goalMode", "goalErrMode", "goalChartCheckinTitle", "goalChartNoRoutines", "goalChartWeekSummary"]) {
    assert.doesNotMatch(copy, new RegExp(`\\b${dead}:`), `copy.ts 应删除 ${dead}`);
  }
  for (const cls of [".habit-add", ".habit-chip.on", ".habit-week", ".habit-day.done", ".habit-day.missed", ".habit-day.today"]) {
    assert.match(css, new RegExp(cls.replace(/\./g, "\\.") + " \\{|" + cls.replace(/\./g, "\\.") + "\\{"), `planner.css 缺少 ${cls}`);
  }
  assert.doesNotMatch(css, /checkin-|\.routine-|\.goal-mode/);
});
