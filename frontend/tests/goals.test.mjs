import test from "node:test";
import assert from "node:assert/strict";
import { readFileSync } from "node:fs";

/* 目标模块的前端契约（源代码级断言，防回归）：
   进度不落库、由挂载任务推导；一条任务只属于一个目标；
   目标页在主导航第二项（我的计划下面）；目标删除/移出与任务共用
   软删 + 撤销交互；计划页/日历/专注页带目标徽章，完成时有进度回执。 */

const store = readFileSync(new URL("../src/store.ts", import.meta.url), "utf8");
const app = readFileSync(new URL("../src/App.vue", import.meta.url), "utf8");
const copy = readFileSync(new URL("../src/copy.ts", import.meta.url), "utf8");
const view = readFileSync(new URL("../src/components/GoalsView.vue", import.meta.url), "utf8");
const dialog = readFileSync(new URL("../src/components/GoalDialog.vue", import.meta.url), "utf8");
const list = readFileSync(new URL("../src/components/List.vue", import.meta.url), "utf8");
const calendar = readFileSync(new URL("../src/components/CalendarView.vue", import.meta.url), "utf8");
const focus = readFileSync(new URL("../src/components/FocusView.vue", import.meta.url), "utf8");
const css = readFileSync(new URL("../src/styles/planner.css", import.meta.url), "utf8");

test("goals page is wired into App right below the plan nav item", () => {
  assert.match(app, /import GoalsView from "\.\/components\/GoalsView\.vue"/);
  assert.match(app, /<GoalsView v-else-if="page === 'goals'" \/>/);
  assert.match(app, /<Icon name="target" \/>/);
  assert.match(app, /goals: \{ overline: "GOALS"/);
  // 导航次序：计划 → 目标 → 专注 → 统计。
  const nav = app.match(/<nav aria-label="workspace"[\s\S]*?<\/nav>/)?.[0] || "";
  const order = ["page === 'tasks'", "page === 'goals'", "page === 'focus'", "goStats"].map((k) => nav.indexOf(k));
  assert.ok(order.every((i) => i >= 0) && order.every((v, i) => i === 0 || v > order[i - 1]),
    `导航次序不对：${order}`);
  // 「新建目标」在页头（与「新建任务」同一行），不占单独一行。
  assert.match(app, /v-if="page === 'goals'" class="primary-button new-goal"/);
  assert.match(app, /@click="requestNewGoal"/);
  assert.match(store, /export function requestNewGoal\(\)/);
});

/* 目标带分组标签：创建/编辑弹窗有分组输入（与计划弹窗同款），卡片标题旁
   显示分组标签，格式与串行/并行标签一致（.mode-tag 小描边样式），
   未分组不显示；状态流转（达成/归档）必须带上 group。 */
test("goals carry a group label like tasks do", () => {
  assert.match(store, /group: string; \/\/ 分组标签/);
  assert.match(dialog, /v-model="group" maxlength="40"/);
  assert.match(dialog, /group: group\.value\.trim\(\)/);
  assert.match(view, /v-if="g\.group" class="mode-tag">\{\{ g\.group \}\}<\/span>/);
  assert.doesNotMatch(view, /groupColor|task-group-label/);
  assert.match(view, /group: g\.group, mode: g\.mode/);
});

/* 目标详情是独立视图：点卡片跳转、自带返回行；页头（大标题/副标题/
   新建目标按钮）在详情页隐藏，由 st.goalsViewing 驱动。 */
test("goal detail is a separate view; header hidden while viewing", () => {
  assert.match(view, /const viewing = ref<number \| null>\(null\)/);
  assert.match(view, /class="goal-detail-topbar"/);
  assert.match(view, /@click="back"/);
  assert.match(view, /st\.goalsViewing = !!g/);
  assert.match(store, /goalsViewing: false/);
  assert.match(app, /v-if="page !== 'goals' \|\| !st\.goalsViewing" class="workspace-header"/);
  assert.doesNotMatch(view, /scrollIntoView|panelRef|goalDetail/);
});

test("goal store exposes lifecycle actions hitting goal endpoints", () => {
  for (const fn of [
    "loadGoals", "addGoal", "saveGoal", "deleteGoal", "restoreDeletedGoal",
    "attachTaskToGoal", "detachTaskFromGoal", "restoreDetachedGoalTask", "toggleGoalTask", "reorderGoals",
  ]) {
    assert.match(store, new RegExp(`export async function ${fn}\\(`), `store 缺少 ${fn}`);
  }
  assert.match(store, /export function goalOfTask\(taskId: string\)/);
  assert.match(store, /wsPath\("\/goals"\)/);
  assert.match(store, /wsPath\(`\/goals\/\$\{id\}`\)/);
  assert.match(store, /wsPath\(`\/goals\/\$\{goalId\}\/tasks`\)/);
  assert.match(store, /wsPath\("\/goals\/reorder"\)/);
  assert.match(store, /registerLogoutHook\(clearGoals\)/);
  // 409 不携带最新目标：重拉列表对齐服务端，绝不静默覆盖。
  assert.match(store, /e\.status === 409[\s\S]*loadGoals\(true\)/);
});

/* 进行中卡片带前移/后移排序键：跨在卡片左右边框正中，悬停浮现；卡片内嵌
   按钮，故活动卡片是 div + role="button"；首卡无前移键、末卡无后移键；
   点击不冒泡成「打开详情」。 */
test("active goal cards have edge move buttons for reordering", () => {
  assert.match(view, /function moveGoal\(g: WireGoal, dir: -1 \| 1\)/);
  assert.match(view, /reorderGoals\(g\.id, neighbor\.id\)/);
  assert.match(view, /class="goal-move goal-move-prev"/);
  assert.match(view, /class="goal-move goal-move-next"/);
  assert.match(view, /v-if="idx > 0"/);
  assert.match(view, /v-if="idx < activeGoals\.length - 1"/);
  assert.match(view, /@click\.stop="moveGoal\(g, -1\)"/);
  assert.match(view, /@click\.stop="moveGoal\(g, 1\)"/);
  // 排序键用与详情页返回键同款的描边箭头图标，不用 < > 字符。
  assert.match(view, /<Icon name="arrow" :size="13" \/>/);
  assert.doesNotMatch(view, /&lt;|&gt;/);
  // 键跨在边框正中：左缘 translate(-50%)、右缘 translate(50%)。
  assert.match(css, /\.goal-move-prev \{[\s\S]*?translate\(-50%, -50%\)/);
  assert.match(css, /\.goal-move-next \{[\s\S]*?translate\(50%, -50%\)/);
  // 创建目标后停留列表页，不自动跳进详情。
  assert.doesNotMatch(view, /openGoal\(created/);
});

test("goals view derives progress from tasks instead of storing it", () => {
  assert.match(view, /function progressOf\(g: WireGoal\)/);
  assert.match(view, /g\.tasks\.filter\(\(t\) => t\.done\)\.length/);
  // 串行锁定：前面有未完成项即锁定当前项。
  assert.match(view, /function isLocked\(g: WireGoal, index: number\)/);
  // 子任务只支持新建：列表末行入口 + 复用计划页的 TaskDialog。
  assert.match(view, /import TaskDialog from "\.\/TaskDialog\.vue"/);
  assert.match(view, /class="goal-add-row"/);
  assert.doesNotMatch(view, /attachable|attachedIds|goal-attach|goal-subtask/);
});

/* 里程碑行右侧两个操作：「安排」弹层（今天/明天/下周一/自定义/清除，
   写挂载任务的 due）与删除（移出目标，可撤销）；组整体右对齐。 */
test("milestone rows have a schedule popup and a detachable delete", () => {
  assert.match(view, /async function scheduleTask\(t: WireGoalTask, due: number\)/);
  assert.match(view, /saveTask\(\{ \.\.\.item, due \}\)/);
  // 弹层档位：今天 / 明天 / 下周一 / 自定义日期 / 清除日期。
  assert.match(view, /@click="scheduleTask\(t, TODAY\)"/);
  assert.match(view, /@click="scheduleTask\(t, TOMORROW\)"/);
  assert.match(view, /@click="scheduleTask\(t, NEXT_MONDAY\)"/);
  assert.match(view, /<input type="date" @change="scheduleCustom\(t, \$event\)" \/>/);
  assert.match(view, /@click="scheduleTask\(t, 0\)"/);
  assert.match(view, /class="goal-schedule-pop"/);
  assert.match(view, /class="goal-task-actions"/);
  assert.match(view, /text-button danger" @click="detachTaskFromGoal\(goal\.id, t\.taskId\)"/);
  assert.match(css, /\.goal-task-actions \{[\s\S]*?margin-left: auto/);
  // 空日期如实显示「未安排」，不伪造日期。
  assert.match(view, /\{\{ t\.due \|\| C\.goalUnscheduled \}\}/);
});

/* 串行目标的当前步：第一个未完成的里程碑整行高亮（is-current），
   和 step-tag.now 的描边呼应。 */
test("sequential goals highlight the current milestone row", () => {
  assert.match(view, /function isCurrent\(g: WireGoal, index: number\)/);
  assert.match(view, /'is-current': isCurrent\(goal, idx\)/);
  assert.match(css, /\.task-row\.is-current \{[\s\S]*?accent-soft/);
});

/* 移出目标可撤销：toast 复用全局 feedback-toast，撤销 = 重新挂载，
   任务本身没有被删除。 */
test("detaching a milestone offers an undo toast", () => {
  assert.match(store, /detachedGoalTask: null as/);
  assert.match(store, /st\.detachedGoalTask = \{ goalId, taskId, title \}/);
  assert.match(store, /await attachTaskToGoal\(d\.goalId, d\.taskId\)/);
  assert.match(app, /v-else-if="st\.detachedGoalTask"/);
  assert.match(app, /@click="restoreDetachedGoalTask"/);
  assert.match(app, /@click="dismissDetachedGoalTask"/);
});

/* 目标徽章：清单 / 日历 / 专注页的任务行都标注所属目标（小靶标 + 目标名，
   超宽截断）；徽章只用中性色，不占用日历的分类色编码。 */
test("task rows show a goal badge across plan, calendar and focus", () => {
  assert.match(store, /export function goalOfTask\(taskId: string\)/);
  for (const src of [list, calendar, focus]) {
    assert.match(src, /class="goal-badge"/);
    assert.match(src, /<Icon name="target" :size="10" \/>/);
    assert.match(src, /goal-badge-label/);
  }
  assert.match(css, /\.goal-badge \{[\s\S]*?border: 1px solid var\(--line\)/);
  assert.doesNotMatch(css, /\.goal-badge[^}]*event-bg/);
});

/* 完成回执：勾选挂在目标下的任务时，提示里带上目标进度。 */
test("completing a goal task reports goal progress", () => {
  assert.match(store, /function completionNotice\(taskId: string\)/);
  assert.match(store, /notify\(next \? completionNotice\(item\.id\) : C\.reopen\)/);
  assert.match(copy, /goalDoneProgress: "「\{title\}」进度 \{done\}\/\{total\}"/);
});

/* 今日建议：计划页顶部列出每个进行中目标的下一个待办里程碑
   （已在今天计划里的不重复建议），一键安排到今天。 */
test("plan page suggests the next milestone of each active goal", () => {
  assert.match(app, /class="goal-suggest"/);
  assert.match(app, /g\.tasks\.find\(\(t\) => !t\.done && t\.due !== todayStr\)/);
  assert.match(app, /@click="suggestToday\(s\.task\)"/);
  assert.match(app, /saveTask\(\{ \.\.\.item, due: TODAY \}\)/);
  assert.match(css, /\.goal-suggest-row \{[\s\S]*?align-items: center/);
});

test("copy has the goals strings", () => {
  for (const key of [
    "goals", "goalsSubtitle", "goalNew", "goalModeSequential", "goalHealthRisk",
    "goalHealthLate", "goalMarkDone", "goalLockedHint", "goalTaskAlreadyInGoal",
    "goalsActiveSection", "goalsSettledSection", "goalMoveLeft", "goalMoveRight",
    "goalSchedule", "goalScheduleToday", "goalScheduleTomorrow", "goalScheduleNextMonday",
    "goalScheduleCustom", "goalScheduleClear", "goalUnscheduled", "goalDetach", "goalUndo",
    "goalDoneProgress", "goalSuggestTitle", "goalSuggestAdd",
  ]) {
    assert.match(copy, new RegExp(`\\b${key}:`), `copy.ts 缺少 ${key}`);
  }
});

test("App toast offers undo for deleted goals", () => {
  assert.match(app, /st\.deletedGoal/);
  assert.match(app, /@click="restoreDeletedGoal"/);
  assert.match(store, /st\.deletedGoal = goal/);
});
