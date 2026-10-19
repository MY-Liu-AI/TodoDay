import test from "node:test";
import assert from "node:assert/strict";
import { readFileSync } from "node:fs";

/* 目标模块的前端契约（源代码级断言，防回归）：
   进度不落库、由挂载任务推导；一条任务只属于一个目标；
   目标页是主导航第四项；目标删除与任务共用软删 + 撤销交互。 */

const store = readFileSync(new URL("../src/store.ts", import.meta.url), "utf8");
const app = readFileSync(new URL("../src/App.vue", import.meta.url), "utf8");
const copy = readFileSync(new URL("../src/copy.ts", import.meta.url), "utf8");
const view = readFileSync(new URL("../src/components/GoalsView.vue", import.meta.url), "utf8");

test("goals page is wired into App as the fourth nav item", () => {
  assert.match(app, /import GoalsView from "\.\/components\/GoalsView\.vue"/);
  assert.match(app, /<GoalsView v-else-if="page === 'goals'" \/>/);
  assert.match(app, /<Icon name="target" \/>/);
  assert.match(app, /goals: \{ overline: "GOALS"/);
  // 「新建目标」在页头（与「新建任务」同一行），不占单独一行。
  assert.match(app, /v-if="page === 'goals'" class="primary-button new-goal"/);
  assert.match(app, /@click="requestNewGoal"/);
  assert.match(store, /export function requestNewGoal\(\)/);
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
  assert.doesNotMatch(view, /scrollIntoView|panelRef|is-current|goalDetail/);
});

test("goal store exposes lifecycle actions hitting goal endpoints", () => {
  for (const fn of [
    "loadGoals", "addGoal", "saveGoal", "deleteGoal", "restoreDeletedGoal",
    "attachTaskToGoal", "detachTaskFromGoal", "toggleGoalTask",
  ]) {
    assert.match(store, new RegExp(`export async function ${fn}\\(`), `store 缺少 ${fn}`);
  }
  assert.match(store, /wsPath\("\/goals"\)/);
  assert.match(store, /wsPath\(`\/goals\/\$\{id\}`\)/);
  assert.match(store, /wsPath\(`\/goals\/\$\{goalId\}\/tasks`\)/);
  assert.match(store, /registerLogoutHook\(clearGoals\)/);
  // 409 不携带最新目标：重拉列表对齐服务端，绝不静默覆盖。
  assert.match(store, /e\.status === 409[\s\S]*loadGoals\(true\)/);
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

test("copy has the goals strings", () => {
  for (const key of [
    "goals", "goalsSubtitle", "goalNew", "goalModeSequential", "goalHealthRisk",
    "goalHealthLate", "goalMarkDone", "goalLockedHint", "goalTaskAlreadyInGoal",
    "goalsActiveSection", "goalsSettledSection",
  ]) {
    assert.match(copy, new RegExp(`\\b${key}:`), `copy.ts 缺少 ${key}`);
  }
});

test("App toast offers undo for deleted goals", () => {
  assert.match(app, /st\.deletedGoal/);
  assert.match(app, /@click="restoreDeletedGoal"/);
  assert.match(store, /st\.deletedGoal = goal/);
});
