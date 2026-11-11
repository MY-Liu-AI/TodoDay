/* 目标列表统计图：lib/goalcharts.js 纯函数真单测 + 源码契约。
   一块图：进行中目标的里程碑甘特（排期 + 子任务进度）。数据全部来自
   现有 API 形状，纯展示。
   设计契约（2026-11-11 效率复核，见 design/goal-gantt-design.md）：
   - 视窗 = 过去 7 天（钉死，更早的条带截到轴首）+ 未来 ≥28 天（随最远
     截止延展）；今天稳定在轴左侧。
   - 已完成里程碑（total>0 且 done>=total；total=0 视为未拆解）不占行，
     折进组头 doneCount；全完成的目标整组退出（返回 null）。
   - 逾期天数走 overdueDays 文字；刻度铺满整个视窗，日/周两种粒度。 */
import test from "node:test";
import assert from "node:assert/strict";
import { readFileSync } from "node:fs";

import { ganttChart } from "../src/lib/goalcharts.js";

/* 2026-10-27 是周二（与 routines.test.mjs 同一今天口径）。 */
const NOW = new Date(2026, 9, 27, 15, 0, 0).getTime();
const D = (m, d) => new Date(2026, m - 1, d).getTime();

const ms = (id, title, start, due, done, total) => ({
  id,
  title,
  start,
  due,
  tasks: Array.from({ length: total }, (_, i) => ({ done: i < done })),
});

test("ganttChart: null when no active goals or all milestones complete", () => {
  assert.equal(ganttChart([], NOW), null);
  const done = { id: 1, title: "全完成", milestones: [ms(11, "唯一", "2026-10-01", "2026-10-10", 1, 1)] };
  assert.equal(ganttChart([done], NOW), null);
});

test("ganttChart: window axis = 7d past + >=28d future, today near left", () => {
  const g = { id: 2, title: "只有未排期", milestones: [ms(21, "没排期", "", "", 0, 1)] };
  const out = ganttChart([g], NOW);
  assert.equal(out.from, D(10, 20), "过去窗钉死为今天-7 天");
  assert.ok(out.to >= D(11, 24), "未来窗至少今天+28 天");
  assert.ok(out.todayPos > 5 && out.todayPos < 30, "今天稳定在轴左侧");
  assert.equal(out.todayLabel, "10/27");
  /* 全部未排期也要画出刻度尺/月份带/周末底纹/小刻度。 */
  assert.ok(out.ticks.length >= 2);
  assert.ok(out.months.length >= 1);
  assert.ok(out.weekends.length >= 2);
  assert.ok(out.marks.length >= out.ticks.length);
});

test("ganttChart: completed milestones collapse into doneCount, not rows", () => {
  const g = {
    id: 3,
    title: "上线新版",
    milestones: [
      ms(31, "原型", "2026-10-18", "2026-10-22", 2, 2), // 完成 → 折进组头
      ms(32, "开发", "2026-10-25", "2026-11-30", 1, 4),
      ms(33, "收尾", "2026-11-05", "2026-11-08", 0, 2),
    ],
  };
  const out = ganttChart([g], NOW);
  const grp = out.groups[0];
  assert.equal(grp.doneCount, 1);
  assert.deepEqual(grp.rows.map((r) => r.title), ["开发", "收尾"]);
  /* 组进度含已完成里程碑的任务（3/8）。 */
  assert.equal(grp.pct, 38);
  /* 开发：不截断（10/25 在过去窗内）、不逾期（11/30 在未来）、进度 1/4。 */
  const dev = grp.rows[0];
  assert.equal(dev.clipPast, false);
  assert.equal(dev.overdueDays, null);
  assert.equal(dev.fill, 0.25);
  assert.match(dev.dateText, /10\/25 – 11\/30/);
});

test("ganttChart: bars reaching before the 7d past window clip to axis start", () => {
  const g = { id: 4, title: "逾期", milestones: [ms(41, "逾期两周", "2026-10-05", "2026-10-13", 0, 1)] };
  const out = ganttChart([g], NOW);
  const r = out.groups[0].rows[0];
  assert.equal(r.clipPast, true);
  assert.equal(r.left, 0);
  assert.ok(r.width >= 0.8, "截断后保留最小可见宽度");
  assert.equal(r.overdueDays, 14);
  assert.match(r.dateText, /10\/5 – 10\/13/);
});

test("ganttChart: single-sided dates become open bars, undated stays a chip", () => {
  const g = {
    id: 5,
    title: "半排期",
    milestones: [
      ms(51, "已开始", "2026-10-25", "", 0, 1),
      ms(52, "只定了截止", "", "2026-11-20", 0, 1),
      ms(53, "没排期", "", "", 0, 2),
    ],
  };
  const rows = ganttChart([g], NOW).groups[0].rows;
  assert.equal(rows[0].kind, "open-end");
  assert.match(rows[0].dateText, /→/);
  assert.equal(rows[1].kind, "open-start");
  assert.match(rows[1].dateText, /→ 11\/20/);
  assert.equal(rows[2].kind, "undated");
  assert.equal(rows[2].left, 0, "未排期行不参与定位（默认 0，模板不渲染条）");
  assert.equal(rows[2].width, 0);
});

test("ganttChart: future axis extends to the furthest due date", () => {
  const g = { id: 6, title: "明年的事", milestones: [ms(61, "阶段一", "2027-03-01", "2027-03-31", 0, 1)] };
  const out = ganttChart([g], NOW);
  /* 轴尾越过 3/31（含呼吸位），今天被挤到极左侧但不消失。 */
  assert.ok(out.to >= D(4, 1));
  assert.ok(out.todayPos > 0 && out.todayPos < 10);
  /* 刻度仍铺满全窗且有序。 */
  assert.ok(out.ticks.length >= 2);
  for (let i = 1; i < out.ticks.length; i++) assert.ok(out.ticks[i].pos > out.ticks[i - 1].pos);
});

test("ganttChart: day ticks are evenly spaced from the axis start, capped at 16", () => {
  const g = { id: 7, title: "长轴", milestones: [ms(71, "长", "2026-10-20", "2026-12-15", 0, 1)] };
  const out = ganttChart([g], NOW);
  /* span ≈ 59 天 → 步长 5（59/5 ≤ 16，59/3 > 16），锚定轴首、间距恒定。 */
  assert.ok(out.ticks.length <= 16);
  assert.equal(out.ticks[0].label, "10/20");
  assert.equal(out.ticks[0].pos, 0);
  assert.equal(out.ticks[1].label, "10/25");
  const gaps = out.ticks.slice(1).map((t, i) => t.pos - out.ticks[i].pos);
  for (const gp of gaps) assert.ok(Math.abs(gp - gaps[0]) < 0.2, "刻度位置应均匀分布");
  /* 周一保留 week 标记（网格线略深）。 */
  assert.ok(out.ticks.some((t) => t.week));
});

test("ganttChart: week granularity takes Mondays only, no weekend shading", () => {
  const g = { id: 8, title: "周刻度", milestones: [ms(81, "长", "2026-10-20", "2026-12-15", 0, 1)] };
  const out = ganttChart([g], NOW, 0, "week");
  assert.ok(out.ticks.length >= 4);
  for (const t of out.ticks) assert.ok(t.week, "周粒度只取周一");
  assert.equal(out.weekends.length, 0, "周末底纹仅日粒度");
  assert.match(out.ticks[0].label, /10\/26/);
});

test("ganttChart: today tab clash suppression follows real track width", () => {
  const g = { id: 9, title: "撞签", milestones: [ms(91, "长", "2026-10-20", "2026-12-15", 0, 1)] };
  const wide = ganttChart([g], NOW, 900);
  /* 今天 ~11.9%：10/25（8.5%）落进尺签脚印被抑制，11/4（25.4%）不受影响。 */
  assert.equal(wide.ticks.find((t) => t.label === "10/25").clash, true);
  assert.equal(wide.ticks.find((t) => t.label === "11/4").clash, false);
  assert.ok(wide.todayTabPos >= 0 && wide.todayTabPos <= 100);
  /* trackPx 未知时不抑制（组件量到后重算）。 */
  const unknown = ganttChart([g], NOW);
  assert.ok(unknown.ticks.every((t) => !t.clash));
  assert.equal(unknown.todayTabPos, unknown.todayPos);
});

test("ganttChart: marks the current step among unfinished rows", () => {
  const g = {
    id: 10,
    title: "三步走",
    milestones: [
      ms(101, "第一步", "", "", 1, 1), // 完成 → 过滤
      ms(102, "第二步", "", "", 0, 2), // 当前步
      ms(103, "第三步", "", "", 0, 1),
    ],
  };
  const rows = ganttChart([g], NOW).groups[0].rows;
  assert.deepEqual(rows.map((r) => r.title), ["第二步", "第三步"]);
  assert.deepEqual(rows.map((r) => r.current), [true, false]);
  /* 全部完成的里程碑不再标记当前步（整组已退出）。 */
  const allDone = { id: 11, title: "做完", milestones: [ms(111, "唯一", "", "", 2, 2)] };
  assert.equal(ganttChart([allDone], NOW), null);
});

/* ── 源码契约 ─────────────────────────────────────────────── */

const view = readFileSync(new URL("../src/components/GoalsView.vue", import.meta.url), "utf8");
const copy = readFileSync(new URL("../src/copy.ts", import.meta.url), "utf8");
const css = readFileSync(new URL("../src/styles/planner.css", import.meta.url), "utf8");

test("goals list no longer renders the three stat blocks", () => {
  assert.doesNotMatch(view, /class="goal-stats"/, "三格数字统计块已删除");
  assert.doesNotMatch(view, /goalsActiveCount|goalsAvgProgress|goalStatusDone/);
  assert.doesNotMatch(view, /const stats = computed/);
});

test("goals list renders the gantt chart section", () => {
  assert.match(view, /ganttChart\(activeGoals/);
  assert.match(view, /\['gantt-bar', row\.kind, \{ 'clip-past': row\.clipPast \}\]/);
  assert.match(view, /C\.goalChartGanttTitle/);
  assert.doesNotMatch(view, /checkinChart|checkin-cell/, "逐周打卡图已下线（习惯页用本周打卡条替代）");
  /* 视窗式刻度尺：月份带 + 小刻度 + 日/周标签 + 今天尺签。 */
  assert.match(view, /class="gantt-ruler"/);
  assert.match(view, /class="gantt-months"/);
  assert.match(view, /class="gantt-tick-mark"/);
  assert.match(view, /class="gantt-today-tab"/);
  assert.match(view, /v-show="!t\.clash"/, "尺签脚印内的刻度标签整枚隐藏");
  assert.match(view, /gantt\.todayTabPos/);
  /* 参考层：周末底纹（日粒度）+ 网格线（周一加深）+ 今天线。 */
  assert.match(view, /class="gantt-weekend"/);
  assert.match(view, /class="gantt-gridline"/);
  assert.match(view, /\{ 'is-week': t\.week \}/);
  /* 行：逾期元信息小签 + 过去窗截断虚线 + 组头「已完成 n 项」小签。 */
  assert.match(view, /class="gantt-late"/);
  assert.match(view, /\{ 'clip-past': row\.clipPast \}/);
  assert.match(view, /class="gantt-done-chip"/);
  /* 粒度切换（日/周）。 */
  assert.match(view, /ganttGran = 'day'/);
  assert.match(view, /ganttGran = 'week'/);
  assert.match(view, /C\.goalChartGranDay/);
  assert.match(view, /C\.goalChartGranWeek/);
  /* 刻度/尺签按时间轨实际像素宽换算（clash、tab 位置），量宽走
     ResizeObserver；今天线永远是 bare span，不带浮动标签。 */
  assert.match(view, /ganttChart\(activeGoals\.value, Date\.now\(\), ganttTrackPx\.value, ganttGran\.value\)/);
  assert.match(view, /ResizeObserver/);
  assert.doesNotMatch(view, /isTodayTick/, "「首刻度即今天」实现已随视窗轴下线");
  assert.doesNotMatch(view, /<em>\{\{ C\.goalChartToday \}\}<\/em>/, "浮动今天标签已下线");
});

test("copy has the chart strings and no dead stat keys", () => {
  for (const key of [
    "goalChartGanttTitle", "goalChartGanttEmpty",
    "goalChartUndated", "goalChartLoading", "goalChartToday", "goalCurrentStepShort",
    "goalChartGranDay", "goalChartGranWeek", "goalChartDoneChip", "goalChartOverdue",
  ]) {
    assert.match(copy, new RegExp(`\\b${key}:`), `copy.ts 缺少 ${key}`);
  }
  for (const dead of ["goalChartCheckinTitle", "goalChartCheckinEmpty", "goalChartNoRoutines", "goalChartWeekSummary"]) {
    assert.doesNotMatch(copy, new RegExp(`\\b${dead}:`), `copy.ts 应删除 ${dead}`);
  }
  for (const dead of ["goalsActiveCount", "goalsAvgProgress", "goalStatusDone", "goalStatusArchived"]) {
    assert.doesNotMatch(copy, new RegExp(`\\b${dead}:`), `copy.ts 应删除 ${dead}`);
  }
});

test("planner.css has chart styles on CSS variables (light/dark both covered)", () => {
  for (const cls of [
    ".gantt-canvas {", ".gantt-grid {", ".gantt-bar {", ".gantt-fill {",
    ".gantt-today {", ".gantt-row-meta {", ".gantt-current-dot {",
    ".gantt-ruler {", ".gantt-month {", ".gantt-tick-mark {", ".gantt-tick {",
    ".gantt-today-tab {", ".gantt-weekend {", ".gantt-gridline {",
    ".gantt-done-chip {", ".gantt-late {",
  ]) {
    assert.ok(css.includes(cls), `planner.css 缺少 ${cls}`);
  }
  /* 刻度尺与三列行共用同一套列宽变量，今天线/条才能对齐；参考层顶对齐
     刻度尺高度变量。 */
  assert.match(css, /grid-template-columns: var\(--gantt-label\) 1fr var\(--gantt-meta\)/);
  assert.match(css, /left: calc\(var\(--gantt-label\) \+ var\(--gantt-gap\)\)/);
  assert.match(css, /top: var\(--gantt-ruler-h\)/);
  assert.match(css, /\.gantt-ruler::after/);
  /* 无边框泳道式画布；周末底纹浅色/深色两套。 */
  const canvas = css.slice(css.indexOf(".gantt-canvas {"), css.indexOf(".gantt-ruler {"));
  assert.doesNotMatch(canvas, /background|border|border-radius|padding: 14/);
  assert.match(canvas, /--gantt-weekend/);
  assert.match(css, /:root\[data-theme="dark"\] \.gantt-canvas \{[^}]*--gantt-weekend/);
  /* 逾期走既有 --neg 状态 token，不新增装饰色。 */
  assert.match(css, /\.gantt-late \{[^}]*var\(--neg\)/);
  /* 完成度右对齐成列。 */
  const metaBlock = css.slice(css.indexOf(".gantt-row-meta {"), css.indexOf(".goals-empty {"));
  assert.match(metaBlock, /text-align: right/);
  /* 贴轴首/轴尾刻度对位规则。 */
  assert.match(css, /\.gantt-tick\.is-start/);
  assert.match(css, /\.gantt-tick\.is-end/);
  /* 「今天刻度」与浮动标签的旧实现不再回来。 */
  assert.doesNotMatch(css, /\.gantt-tick\.is-today/);
  assert.doesNotMatch(css, /\.gantt-today em/);
  /* 刻度轨不再伸进元信息列。 */
  assert.doesNotMatch(css, /margin-right: calc\(var\(--gantt-meta\)/);
  /* 条带/截断样式在变量上。 */
  const block = css.slice(css.indexOf(".gantt-bar {"), css.indexOf(".gantt-fill {"));
  assert.match(block, /var\(--/);
  assert.match(css, /\.gantt-bar\.clip-past/);
});
