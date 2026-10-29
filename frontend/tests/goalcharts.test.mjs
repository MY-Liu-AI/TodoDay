/* 目标列表统计图：lib/goalcharts.js 纯函数真单测 + 源码契约。
   一块图：进行中目标的里程碑甘特（排期 + 子任务进度）。数据全部来自
   现有 API 形状，纯展示。 */
import test from "node:test";
import assert from "node:assert/strict";
import { readFileSync } from "node:fs";

import { ganttChart } from "../src/lib/goalcharts.js";

/* 2026-10-27 是周二（与 routines.test.mjs 同一今天口径）。 */
const NOW = new Date(2026, 9, 27, 15, 0, 0).getTime();

const ms = (id, title, start, due, done, total) => ({
  id,
  title,
  start,
  due,
  tasks: Array.from({ length: total }, (_, i) => ({ done: i < done })),
});

test("ganttChart: null when no active goals", () => {
  assert.equal(ganttChart([], NOW), null);
});

test("ganttChart: dated milestones get positioned bars with task progress", () => {
  const g = {
    id: 1,
    title: "上线新版",
    milestones: [
      ms(11, "原型", "2026-10-20", "2026-10-31", 2, 2),
      ms(12, "开发", "2026-11-01", "2026-11-30", 1, 4),
    ],
  };
  const out = ganttChart([g], NOW);
  assert.equal(out.groups.length, 1);
  const grp = out.groups[0];
  assert.equal(grp.pct, 50); // 3/6 项子任务完成
  const [a, b] = grp.rows;
  assert.equal(a.kind, "range");
  assert.equal(a.fill, 1);
  assert.ok(a.left > 0 && a.left < 5, "最早的开始贴轴首（4% 呼吸位之内）");
  assert.ok(a.width > 0);
  assert.ok(b.left > a.left);
  assert.equal(b.fill, 0.25);
  assert.match(a.dateText, /10\/20/);
  // 轴：九个刻度按时间顺序（M/d 字符串不能直接字典序比，要拆数值）。
  assert.equal(out.ticks.length, 9);
  const ord = out.ticks.map((t) => t.label.split("/").map(Number));
  for (let i = 1; i < ord.length; i++) {
    const [pm, pd] = ord[i - 1];
    const [m, d] = ord[i];
    assert.ok(m > pm || (m === pm && d > pd), `刻度应递增：${out.ticks[i - 1].label} → ${out.ticks[i].label}`);
  }
  // 今天在轴内（10/20 起 41 天，含两端 pad）。
  assert.ok(out.todayPos > 0 && out.todayPos < 100);
  // 刻度从今天起排：首刻度 = 今天（与今天线同位）、尾刻度贴轴尾（100%，
  // 进度列左侧）；间距均匀；今天前的旧日期不再出现在刻度上。
  assert.equal(out.ticks[0].label, "10/27");
  assert.equal(out.ticks[0].pos, out.todayPos);
  assert.equal(out.ticks[8].pos, 100);
  const posGaps = out.ticks.slice(1).map((t, i) => t.pos - out.ticks[i].pos);
  const meanGap = posGaps.reduce((a, b) => a + b, 0) / posGaps.length;
  for (const g of posGaps) assert.ok(Math.abs(g - meanGap) < 0.05, "刻度位置应均匀分布");
  // 任何刻度都不早于今天。
  const todayOrd = out.ticks[0].label.split("/").map(Number);
  for (const t of out.ticks) {
    const [m, d] = t.label.split("/").map(Number);
    assert.ok(m > todayOrd[0] || (m === todayOrd[0] && d >= todayOrd[1]), `刻度不应早于今天：${t.label}`);
  }
});

test("ganttChart: single-sided dates become open bars, undated stays a chip", () => {
  const g = {
    id: 2,
    title: "半排期",
    milestones: [
      ms(21, "已开始", "2026-10-25", "", 0, 1),
      ms(22, "只定了截止", "", "2026-11-20", 1, 1),
      ms(23, "没排期", "", "", 0, 2),
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

test("ganttChart: today outside the axis is not drawn", () => {
  const far = {
    id: 3,
    title: "明年的事",
    milestones: [ms(31, "阶段一", "2027-03-01", "2027-03-31", 0, 1)],
  };
  assert.equal(ganttChart([far], NOW).todayPos, null);
  // 轴内目标则画线。
  const near = { ...far, milestones: [ms(32, "本周", "2026-10-26", "2026-11-08", 0, 1)] };
  assert.ok(ganttChart([near], NOW).todayPos !== null);
});

test("ganttChart: tiny ranges are padded so bars stay readable", () => {
  const g = {
    id: 4,
    title: "两天冲刺",
    milestones: [ms(41, "冲刺", "2026-10-27", "2026-10-28", 0, 1)],
  };
  const out = ganttChart([g], NOW);
  // pad 后轴 ≥14 天：单个 2 天的条只占很小一段，但不越界。
  const row = out.groups[0].rows[0];
  assert.ok(row.left >= 0 && row.left <= 100);
  assert.ok(row.width >= 0.8);
  assert.ok(row.left + row.width <= 100);
});

test("ganttChart: marks the current step like the detail view", () => {
  const g = {
    id: 5,
    title: "三步走",
    milestones: [
      ms(51, "第一步", "", "", 1, 1), // 完成
      ms(52, "第二步", "", "", 0, 2), // 有未完成 → 当前步
      ms(53, "第三步", "", "", 0, 1), // 后面锁着
    ],
  };
  const rows = ganttChart([g], NOW).groups[0].rows;
  assert.deepEqual(rows.map((r) => r.current), [false, true, false]);
  // 全部完成的里程碑不再标记当前步。
  const done = { ...g, milestones: [ms(54, "唯一步", "", "", 2, 2)] };
  assert.equal(ganttChart([done], NOW).groups[0].rows[0].current, false);
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
  assert.match(view, /\['gantt-bar', row\.kind\]/);
  assert.match(view, /C\.goalChartGanttTitle/);
  assert.doesNotMatch(view, /checkinChart|checkin-cell/, "逐周打卡图已下线（习惯页用本周打卡条替代）");
  // 精致化后的甘特结构：画布 + 连续网格层/今天线 + 三列行（标签/轨/元信息）。
  assert.match(view, /class="gantt-canvas"/);
  assert.match(view, /class="gantt-grid"/);
  assert.match(view, /class="gantt-row-meta"/);
  assert.match(view, /gantt-current-dot/);
  // 行末只留 done/total（日期只进悬停提示）。
  assert.match(view, /\{\{ row\.kind === "undated" \? C\.goalChartUndated : `\$\{row\.done\}\/\$\{row\.total\}` \}\}/);
  assert.doesNotMatch(view, /gantt-bar-text/, "条内文字已挪到元信息列");
});

test("copy has the chart strings and no dead stat keys", () => {
  for (const key of [
    "goalChartGanttTitle", "goalChartGanttEmpty",
    "goalChartUndated", "goalChartLoading", "goalChartToday", "goalCurrentStepShort",
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
  ]) {
    assert.ok(css.includes(cls), `planner.css 缺少 ${cls}`);
  }
  // 网格层与三列行共用同一套列宽变量，今天线/条才能对齐；无边距画布，无 14px 偏移。
  assert.match(css, /grid-template-columns: var\(--gantt-label\) 1fr var\(--gantt-meta\)/);
  assert.match(css, /left: calc\(var\(--gantt-label\) \+ var\(--gantt-gap\)\)/);
  // 无边框泳道式布局：画布无面板底/边框/内边距，甘特区不再渲染区段标题。
  const canvas = css.slice(css.indexOf(".gantt-canvas {"), css.indexOf(".gantt-ticks {"));
  assert.doesNotMatch(canvas, /background|border|border-radius|padding: 14/);
  const ganttSection = view.slice(view.indexOf('class="goal-chart"'), view.indexOf('class="section-heading"'));
  assert.doesNotMatch(ganttSection, /section-heading/, "甘特区的标题与分隔线已删除");
  // 完成度右对齐成列；时间轴基线拉通整行（左标签列、右完成度列两端对齐）。
  const metaBlock = css.slice(css.indexOf(".gantt-row-meta {"), css.indexOf(".goals-empty {"));
  assert.match(metaBlock, /text-align: right/);
  assert.match(css, /\.gantt-ticks::after/);
  // 刻度轨契约：span 到第 3 列后收回元信息列、只留 40px——尾刻度落在
  // 时间轨右缘 +40px，紧邻进度列又不与之对齐；尾刻度与中间刻度同为中心对位。
  assert.match(css, /\.gantt-ticks-track \{[^}]*grid-column: 2 \/ 4;/);
  assert.match(css, /margin-right: calc\(var\(--gantt-meta\) \+ var\(--gantt-gap\) - 40px\)/);
  assert.doesNotMatch(css, /\.gantt-tick:last-child/, "尾刻度无特殊对位规则");
  // 被精致化移除的旧样式不再回来。
  assert.ok(!css.includes(".gantt-bar-text"), "条内文字样式已删");
  const block = css.slice(css.indexOf(".gantt-bar {"), css.indexOf(".gantt-fill {"));
  assert.match(block, /var\(--/);
});
