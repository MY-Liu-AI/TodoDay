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
  assert.match(app, /goals: \{ title: C\.goals, subtitle: C\.goalsSubtitle \}/);
  // 导航次序：计划 → 目标 → 专注 → 统计。
  const nav = app.match(/<nav aria-label="workspace"[\s\S]*?<\/nav>/)?.[0] || "";
  const order = ["page === 'tasks'", "page === 'goals'", "page === 'focus'", "goStats"].map((k) => nav.indexOf(k));
  assert.ok(order.every((i) => i >= 0) && order.every((v, i) => i === 0 || v > order[i - 1]),
    `导航次序不对：${order}`);
  // 「新建目标」收进目标页工具条（与视图切换同一行），
  // 与计划页「新建任务」同款 workspace-cta 样式；仍走 store 信号开对话框。
  assert.match(view, /class="primary-button workspace-cta new-goal"/);
  assert.match(view, /@click="requestNewGoal"/);
  assert.match(store, /export function requestNewGoal\(\)/);
});

/* 目标带分组标签：数据模型不变（store 仍存 group、状态流转带 group、
   卡片标题旁仍显示 .mode-tag），但弹窗不再有分组输入——2026-10-22 起
   分组入口整体下线（见 shell.test.mjs），编辑时旧值原样回传不清空。 */
test("goals keep group data without a dialog input", () => {
  assert.match(store, /group: string; \/\/ 分组标签/);
  assert.doesNotMatch(dialog, /v-model="group" maxlength="40"/);
  assert.match(dialog, /group: props\.goal\?\.group \|\| ""/);
  assert.match(view, /v-if="g\.group" class="mode-tag">\{\{ g\.group \}\}<\/span>/);
  assert.doesNotMatch(view, /groupColor|task-group-label/);
  // 串行/并行 mode 已随「周期事项」拆分整体下线：状态流转只带 group。
  assert.match(view, /group: g\.group, status,/);
  assert.doesNotMatch(view, /g\.mode|goal\.mode/);
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

/* 列表页拆成两个子视图：我的目标 / 目标进度，与计划页清单/日历同一套
   view-switch 分段控件（图标 + 文案 + is-active）；默认我的目标；纯组件
   内状态、不持久化；详情视图（viewing）不受子视图影响，返回后仍落在原分支。 */
test("goals list splits cards and gantt behind the plan-style view-switch", () => {
  assert.match(copy, /goalViewCards: "我的目标"/);
  assert.match(copy, /goalViewGantt: "目标进度"/);
  assert.match(copy, /goalsSubtitle: "把目标拆解成任务，进度随任务完成自动更新。"/);
  assert.match(view, /const view = ref<"gantt" \| "cards">\("cards"\)/);
  assert.match(view, /class="view-switch" aria-label="goals"/);
  assert.match(view, /\['cards', C\.goalViewCards, 'target'\]/);
  assert.match(view, /\['gantt', C\.goalViewGantt, 'stats'\]/);
  assert.match(view, /<section v-if="view === 'gantt'" class="goal-chart"/);
  assert.match(view, /<template v-else>[\s\S]*?goal-cards/);
  assert.doesNotMatch(view, /localStorage/);
});

test("goal store exposes lifecycle actions hitting goal endpoints", () => {
  for (const fn of [
    "loadGoals", "addGoal", "saveGoal", "deleteGoal", "restoreDeletedGoal",
    "addMilestone", "saveMilestone", "deleteMilestone", "restoreDeletedMilestone",
    "attachTaskToGoal", "detachTaskFromGoal", "toggleGoalTask", "reorderGoals",
  ]) {
    assert.match(store, new RegExp(`export async function ${fn}\\(`), `store 缺少 ${fn}`);
  }
  assert.match(store, /export function goalOfTask\(taskId: string\)/);
  assert.match(store, /export function requestOpenGoal\(id: number\)/);
  assert.match(store, /wsPath\("\/goals"\)/);
  assert.match(store, /wsPath\(`\/goals\/\$\{id\}`\)/);
  assert.match(store, /wsPath\(`\/goals\/\$\{goalId\}\/milestones\/\$\{milestoneId\}\/tasks`\)/);
  assert.match(store, /wsPath\(`\/goals\/\$\{goalId\}\/tasks\/remove`\)/);
  assert.match(store, /wsPath\(`\/goals\/\$\{goalId\}\/milestones`\)/);
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
  assert.match(view, /flatMap\(\(m\) => m\.tasks\)/);
  // 串行锁定：前面有未完成的里程碑即锁定当前里程碑。
  assert.match(view, /function isLocked\(g: WireGoal, index: number\)/);
  // 子任务只支持在每个里程碑下新建：复用计划页的 TaskDialog。
  assert.match(view, /import TaskDialog from "\.\/TaskDialog\.vue"/);
  assert.match(view, /class="goal-add-row"/);
  assert.doesNotMatch(view, /attachable|attachedIds|goal-attach|goal-subtask/);
});

/* 目标拆解为 目标 → 里程碑 → 任务 三层（2026-10-26 起）：里程碑是独立
   实体，自带标题与开始/截止日期；没有完成勾选（里程碑是阶段不是任务），
   点左侧箭头展开任务列表，任务项可勾选完成。 */
test("goal decomposition is milestones with expandable task lists", () => {
  // 数据契约：WireGoal.milestones 取代 WireGoal.tasks。
  const api = readFileSync(new URL("../src/api.ts", import.meta.url), "utf8");
  assert.match(api, /export interface WireGoalMilestone \{/);
  const wireGoal = api.slice(api.indexOf("export interface WireGoal {"), api.indexOf("const TIMEOUT"));
  assert.match(wireGoal, /milestones: WireGoalMilestone\[\];/);
  assert.doesNotMatch(wireGoal, /tasks: WireGoalTask\[\];/);
  // 里程碑行：无完成勾选，有展开箭头 + 新建任务/编辑/删除操作组。
  assert.match(view, /class="ms-block"/);
  assert.match(view, /class="task-row ms-row"/);
  assert.match(view, /class="ms-caret"/);
  assert.match(view, /@click="toggleExpand\(m\.id\)"/);
  assert.match(view, /v-if="isExpanded\(goal, idx, m\)"/);
  assert.match(view, /class="ms-tasks"/);
  assert.match(view, /@click="toggleTaskInGoal\(t\)"/);
  assert.match(view, /@click="openMsTaskDialog\(m\)"/);
  assert.match(view, /@click="deleteMilestone\(goal\.id, m\.id\)"/);
  assert.match(view, /@click="startEditMs\(m\)"/);
  // 添加里程碑的内联表单：标题 + 起止日期；开始晚于截止给出内联错误。
  assert.match(view, /class="ms-form"/);
  assert.match(view, /v-model="msDraft\.start" type="date"/);
  assert.match(view, /v-model="msDraft\.due" type="date"/);
  assert.match(view, /msDateError\(msDraft\.value\)/);
  assert.match(view, /await addMilestone\(g\.id,/);
  assert.match(view, /await saveMilestone\(/);
  assert.doesNotMatch(view, /task-check[\s\S]*?ms-row/);
  // 旧「安排」弹层整套下线。
  assert.doesNotMatch(view, /goalSchedule|scheduleTask|scheduling|goal-schedule/);
  assert.doesNotMatch(css, /\.goal-schedule/);
  // 里程碑的行内样式。
  assert.match(css, /\.ms-caret \{/);
  assert.match(css, /\.ms-tasks \{/);
  assert.match(css, /\.ms-form \{/);
});

/* 串行目标的当前步：第一个「前面都完成、自己还有未完成项」的里程碑
   整块高亮（.ms-block.is-current），和 step-tag.now 的描边呼应。 */
test("sequential goals highlight the current milestone block", () => {
  assert.match(view, /function isCurrent\(g: WireGoal, index: number\)/);
  assert.match(view, /'is-current': isCurrent\(goal, idx\)/);
  assert.match(view, /class="ms-block"/);
  assert.match(css, /\.ms-block\.is-current \{[\s\S]*?accent-soft/);
});

/* 里程碑删除走软删 + 全局撤销 toast（与目标删除同一交互）；
   删除时其下任务解除挂载、撤销恢复里程碑本身。 */
test("deleting a milestone offers an undo toast", () => {
  assert.match(store, /deletedMilestone: null as/);
  assert.match(store, /st\.deletedMilestone = \{ goalId, milestone \}/);
  assert.match(store, /restoreDeletedMilestone/);
  assert.match(app, /v-else-if="st\.deletedMilestone"/);
  assert.match(app, /@click="restoreDeletedMilestone"/);
  assert.match(app, /@click="dismissDeletedMilestone"/);
  assert.doesNotMatch(app, /detachedGoalTask|restoreDetachedGoalTask/);
});

/* 里程碑与计划页日历联动：进行中目标的每个里程碑，开始/截止各成一个
   日历标记（ms_ 前缀伪日程项），点击标记跳转目标详情而不是打开任务编辑。 */
test("milestone start and due dates show up as calendar markers", () => {
  assert.match(app, /function msMarker\(/);
  assert.match(app, /id: `ms_\$\{kind\}_\$\{gid\}_\$\{msid\}`/);
  assert.match(app, /:items="calendarFeed"/);
  assert.match(app, /if \(m\.start\) out\.push\(msMarker/);
  assert.match(app, /if \(m\.due\) out\.push\(msMarker/);
  assert.match(app, /i\.id\.startsWith\("ms_"\)/);
  assert.match(app, /requestOpenGoal\(Number\(i\.id\.split\("_"\)\[2\]\)\)/);
  // 日程清单里里程碑标记不是任务：无勾选框，只一个小靶标占位。
  assert.match(calendar, /v-if="!i\.id\.startsWith\('ms_'\)"/);
  assert.match(calendar, /class="ms-mark"/);
  assert.doesNotMatch(calendar, /goalOfTask\(i\.id\).*ms_/);
});

/* 目标徽章：清单 / 日历 / 专注页的任务行都标注所属目标（小靶标 + 目标名，
   超宽截断）；徽章只用中性色，不占用日历的分类色编码。 */
test("task rows show a goal badge across plan, calendar and focus", () => {
  assert.match(store, /export function goalOfTask\(taskId: string\)/);
  // 专注页的任务行在 FocusTaskRow 组件里（列表/四象限视图共用）。
  const focusRow = readFileSync(new URL("../src/components/FocusTaskRow.vue", import.meta.url), "utf8");
  for (const src of [list, calendar, focusRow]) {
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

/* 今日建议：计划页顶部列出每个进行中目标的下一个待办任务
   （已在今天计划里的不重复建议），一键安排到今天。 */
test("plan page suggests the next task of each active goal", () => {
  assert.match(app, /class="goal-suggest"/);
  assert.match(app, /flatMap\(\(m\) => m\.tasks\)[\s\S]*?\.find\(\(t\) => !t\.done && t\.due !== todayStr\)/);
  assert.match(app, /@click="suggestToday\(s\.task\)"/);
  assert.match(app, /saveTask\(\{ \.\.\.item, due: TODAY \}\)/);
  assert.match(css, /\.goal-suggest-row \{[\s\S]*?align-items: center/);
});

test("copy has the goals strings", () => {
  for (const key of [
    "goals", "goalsSubtitle", "goalNew", "goalMarkDone",
    "goalLockedHint", "goalTaskAlreadyInGoal",
    "goalsActiveSection", "goalsSettledSection", "goalMoveLeft", "goalMoveRight",
    "goalMilestoneAdd", "goalMilestoneName", "goalMilestoneStart", "goalMilestoneDue",
    "goalMilestoneDateSep", "goalMilestoneNoDate", "goalMilestoneEdit", "goalMilestoneCancel",
    "goalMilestoneDelete", "goalMilestoneNewTask", "goalMilestoneTaskCount",
    "goalMilestoneEmpty", "goalMilestoneDateError", "goalMilestoneAdded", "goalMilestoneDeleted",
    "goalMilestoneStartMark", "goalMilestoneDueMark",
    "goalDetach", "goalUndo",
    "goalDoneProgress", "goalSuggestTitle", "goalSuggestAdd",
    "goalStrategy", "goalStrategyGoal", "goalStrategyAcceptance", "goalStrategyFailure",
    "goalStrategyFold", "goalStrategyUnfold", "goalStrategyExpand", "goalStrategyCollapse",
    "goalMore",
    "goalStepBasics", "goalStepAcceptance", "goalStepTradeoff", "goalStepCount",
    "goalNext", "goalBack", "goalCreate", "goalErrTitle", "goalErrGoal",
    "goalErrAcceptance", "goalErrFailure",
  ]) {
    assert.match(copy, new RegExp(`\\b${key}:`), `copy.ts 缺少 ${key}`);
  }
  // 串行/并行 mode 与周期事项文案随习惯模块拆分整体下线。
  for (const key of ["goalMode", "goalModeParallel", "goalModeSequential", "goalErrMode"]) {
    assert.doesNotMatch(copy, new RegExp(`\\b${key}:`), `copy.ts 仍残留 ${key}`);
  }
  // 目标不再有截止日期与健康度：旧 key 全部下线。
  for (const key of [
    "goalNote", "goalNotePlaceholder", "goalDue", "goalNoDue",
    "goalHealthOk", "goalHealthRisk", "goalHealthLate", "goalHealthNone",
    "goalDaysLeft", "goalDaysOver", "goalHealthDetail",
    "goalSchedule", "goalScheduleToday", "goalScheduleTomorrow", "goalScheduleNextMonday",
    "goalScheduleCustom", "goalScheduleClear", "goalUnscheduled",
  ]) {
    assert.doesNotMatch(copy, new RegExp(`\\b${key}:`), `copy.ts 仍残留 ${key}`);
  }
});

/* 目标决策三问（2026-10-26 起）：目标创建/编辑不再选截止日期，备注升级为
   我的目标 / 验收标准 / 失败代价。健康度（进度 vs 时间进度）随截止日期
   一并下线；旧 note 由 migration 0007 自动迁入 goal_text。
   创建是三步「强制决策」向导：定目标 → 定验收 → 定代价，
   三问必填。 */
test("goals use strategy questions instead of note and due date", () => {
  // 弹窗：无日期输入，三问各一个 textarea，无下拉选择器。
  assert.doesNotMatch(dialog, /type="date"/);
  assert.doesNotMatch(dialog, /goalDue|goalNote/);
  assert.doesNotMatch(dialog, /<select/);
  assert.match(dialog, /v-model="goalText"/);
  assert.match(dialog, /v-model="acceptance"/);
  assert.match(dialog, /v-model="failureCost"/);
  for (const key of ["goalStrategyGoal", "goalStrategyAcceptance", "goalStrategyFailure"]) {
    assert.match(dialog, new RegExp(`C\\.${key}`), `GoalDialog 缺少 ${key}`);
  }
  // 数据契约：WireGoal/GoalInput 带三问、无 note/due（切片只取 WireGoal
  // 段，到「习惯」接口注释为止）。
  const api = readFileSync(new URL("../src/api.ts", import.meta.url), "utf8");
  const wireGoal = api.slice(api.indexOf("export interface WireGoal {"), api.indexOf("/* 习惯："));
  assert.match(api, /goalText: string; \/\/ 目标决策·我的目标/);
  assert.match(api, /acceptance: string; \/\/ 目标决策·验收标准/);
  assert.match(api, /failureCost: string; \/\/ 目标决策·失败代价/);
  assert.doesNotMatch(wireGoal, /\bnote\b|\bdue\b/);
  // 视图：详情页展示目标决策区块（非空才出现），列表卡片显示「我的目标」摘要；
  // 健康度徽章 / 剩余天数 / 风险统计全部移除。
  assert.match(view, /class="goal-strategy"/);
  assert.match(view, /v-if="goal\.goalText \|\| goal\.acceptance \|\| goal\.failureCost"/);
  assert.match(view, /\{\{ goal\.goalText \}\}/);
  assert.match(view, /\{\{ goal\.acceptance \}\}/);
  assert.match(view, /\{\{ goal\.failureCost \}\}/);
  // 布局契约：整块可折叠且详情页默认折叠；答案超过三行限高折叠 + 「展开/收起」。
  assert.match(view, /const strategyOpen = ref\(false\)/);
  assert.match(view, /class="goal-strategy-fold"/);
  assert.match(view, /'is-clamped': !strategyExpanded\.has\('goalText'\)/);
  assert.match(view, /class="goal-strategy-more"/);
  assert.match(view, /toggleStrategyField\('failureCost'\)/);
  assert.match(view, /<p v-if="g\.goalText" class="goal-note">\{\{ g\.goalText \}\}<\/p>/);
  assert.doesNotMatch(view, /healthOf|daysLeft|goal\.due|g\.due|goal\.note|g\.note/);
  assert.doesNotMatch(view, /class="health"/);
  assert.doesNotMatch(css, /\.health|is-warn|\.due\.is-late/);
  assert.match(css, /\.goal-strategy \{/);
  assert.match(css, /\.goal-strategy-item p \{/);
  assert.match(css, /\.goal-strategy-item p\.is-clamped \{[\s\S]*?-webkit-line-clamp: 3/);
  assert.match(css, /\.goal-strategy-q \{[\s\S]*?border-left: 2px solid var\(--accent\)/);
});

/* 详情页头部操作（2026-10-27 改版）：主操作（达成/重新打开）是唯一主色按钮，
   编辑是图标按钮，归档/删除收进「···」菜单——不再横排五个 secondary 按钮。 */
test("goal detail header actions are compact", () => {
  assert.match(view, /function primaryAction\(g: WireGoal\)/);
  assert.match(view, /@click="changeStatus\(primaryAction\(goal\)\.status\)"/);
  assert.match(view, /:aria-label="C\.goalEdit"[^>]*>\s*<Icon name="edit"/);
  assert.match(view, /class="goal-menu"/);
  assert.match(view, /class="goal-menu-panel" role="menu"/);
  assert.match(view, /class="goal-menu-item danger"/);
  assert.match(view, /@click="changeStatus\('archived'\); closeMenu\(\)"/);
  // 旧的一字排开按钮组下线：详情头部不再有成排 secondary-button。
  const head = view.slice(view.indexOf('class="goal-detail-head"'), view.indexOf("goal-strategy"));
  assert.doesNotMatch(head, /secondary-button/);
  assert.match(css, /\.goal-detail-actions \{[\s\S]*?margin-left: auto/);
  assert.match(css, /\.goal-menu-panel \{[\s\S]*?position: absolute/);
  assert.match(css, /\.goal-menu-item\.danger \{[\s\S]*?color: var\(--neg\)/);
});

/* 创建目标 = 三步强制决策向导：步骤轨道（定目标/定验收/定代价）、
   每步校验不过给出内联错误并停在本步；创建时三问必填（编辑尊重旧值）。
   串行/并行「推进方式」已随习惯模块拆分整体下线，不再出现在向导里。 */
test("goal creation is a three-step forced-decision wizard", () => {
  assert.match(dialog, /class="task-dialog goal-dialog"/);
  // 步骤轨道：三步 + 步名 + 完成态打勾 + 步骤间连接线。
  assert.match(dialog, /<ol class="goal-steps"/);
  for (const key of ["goalStepBasics", "goalStepAcceptance", "goalStepTradeoff"]) {
    assert.match(dialog, new RegExp(`C\\.${key}`), `GoalDialog 缺少 ${key}`);
  }
  assert.match(dialog, /class="goal-step-dot"/);
  assert.match(dialog, /class="goal-step-line"/);
  assert.match(dialog, /'is-done': i < step, 'is-current'/);
  assert.doesNotMatch(dialog, /goal-strategy-fields/);
  // 强制决策：标题必校验；创建时三问必填（编辑尊重旧值，不回填逼迫）。
  assert.match(dialog, /if \(!title\.value\.trim\(\)\) return C\.goalErrTitle/);
  assert.match(dialog, /isCreate\.value && !goalText\.value\.trim\(\)/);
  assert.match(dialog, /isCreate\.value && !acceptance\.value\.trim\(\)/);
  assert.match(dialog, /isCreate\.value && !failureCost\.value\.trim\(\)/);
  // 串行/并行选择器与相关错误文案已下线。
  assert.doesNotMatch(dialog, /modePick|radiogroup|goalErrMode|goal-mode/);
  // 底部：第 n 步共 3 步 + 上一步/下一步，末步按钮 = 创建目标。
  assert.match(dialog, /goal-step-count/);
  assert.match(dialog, /C\.goalBack/);
  assert.match(dialog, /goal \? C\.save : C\.goalCreate/);
  assert.match(css, /\.goal-steps \{/);
  assert.doesNotMatch(css, /\.goal-mode/);
  assert.match(css, /@keyframes goal-step-in/);
});

test("App toast offers undo for deleted goals", () => {
  assert.match(app, /st\.deletedGoal/);
  assert.match(app, /@click="restoreDeletedGoal"/);
  assert.match(store, /st\.deletedGoal = goal/);
});
