<script setup lang="ts">
/* 目标模块页面：列表（统计图 + 进行中/已落定卡片）与详情两个视图。
   统计图是里程碑甘特（排期 + 子任务进度），只统计进行中的目标。
   点卡片跳转到该目标的详情视图（返回键回到列表）。
   目标拆解为 目标 → 里程碑 → 任务 三层：里程碑自带标题与开始/截止日期
   （与计划页日历联动，开始/截止各成一个日历标记），任务挂在里程碑下、
   可勾选完成；删除里程碑走软删 + 全局撤销 toast。进度不落库——由挂载
   任务的完成情况推导；里程碑按顺序解锁（锁定只是默认顺序，服务端不
   强制）。定期重复的练习（健身/阅读这类）在独立的「习惯」模块。 */
import { computed, onMounted, onUnmounted, onUpdated, ref, watch } from "vue";
import { C } from "../copy";
import {
  st,
  notify,
  loadGoals,
  addGoal,
  saveGoal,
  deleteGoal,
  addMilestone,
  saveMilestone,
  deleteMilestone,
  createTaskInGoal,
  detachTaskFromGoal,
  toggleGoalTask,
  reorderGoals,
  requestNewGoal,
  type GoalInput,
} from "../store";
import type { Item } from "../store";
import type { WireGoal, WireGoalMilestone, WireGoalTask } from "../api";
import { ganttChart } from "../lib/goalcharts.js";
import Icon from "./Icon.vue";
import GoalDialog from "./GoalDialog.vue";
import TaskDialog from "./TaskDialog.vue";

/* 正在查看的目标 id；null = 列表视图。 */
const viewing = ref<number | null>(null);
/* 列表页的子视图：我的目标 / 目标进度。与计划页的清单/日历切换同一套
   view-switch 分段控件；纯组件内状态、不持久化（与计划页 mode 一致）。
   默认我的目标——卡片是主工作面，进度图是下钻总览。 */
const view = ref<"gantt" | "cards">("cards");
const editing = ref<WireGoal | "new" | null>(null);
/* 正在用 TaskDialog 在某个里程碑下新建的任务（msId 记录挂在哪个里程碑）。 */
const adding = ref<{ msId: number; item: Item } | null>(null);

onMounted(() => {
  loadGoals();
  window.addEventListener("resize", refreshStrategy);
});
onUpdated(() => refreshStrategy());
/* 切走页面时组件卸载，页头状态复位，回列表不受影响。 */
onUnmounted(() => {
  window.removeEventListener("resize", refreshStrategy);
  st.goalsViewing = false;
});

/* ── 目标决策区块：整块可折叠（详情页默认折叠，不占首屏）；单条答案
   超过三行按行折叠并出现「展开/收起」。超高只在收起态量（展开态无 clamp
   量不出来），结果存成 Map 保持粘性——展开后「收起」按钮才不会消失；
   切换目标时复位。 */
const strategyOpen = ref(false);
const strategyExpanded = ref(new Set<string>());
const strategyOverflow = ref(new Map<string, boolean>());
const strategyEls = new Map<string, HTMLElement>();

function bindStrategyP(key: string) {
  return (el: unknown) => {
    if (el instanceof HTMLElement) strategyEls.set(key, el);
    else strategyEls.delete(key);
  };
}

function refreshStrategy() {
  const next = new Map(strategyOverflow.value);
  for (const [key, el] of strategyEls) {
    if (!el.classList.contains("is-clamped")) continue;
    next.set(key, el.scrollHeight > el.clientHeight + 2);
  }
  strategyOverflow.value = next;
}

function toggleStrategyField(key: string) {
  const next = new Set(strategyExpanded.value);
  if (next.has(key)) next.delete(key);
  else next.add(key);
  strategyExpanded.value = next;
  /* 布局完成后再量一次（取消 clamp 的段落自然跳过，保持粘性结果）。 */
  requestAnimationFrame(refreshStrategy);
}

watch(viewing, () => {
  strategyOpen.value = false;
  strategyExpanded.value = new Set();
  strategyOverflow.value = new Map();
});

/* ── 详情头部操作：主操作（达成/重新打开）用唯一主色，编辑是图标按钮，
   归档/删除收进「···」菜单。菜单点击外部或 Esc 关闭。 */
const menuOpen = ref(false);

function closeMenu() {
  menuOpen.value = false;
}
function onDocClick(e: MouseEvent) {
  if (!(e.target as HTMLElement).closest(".goal-menu")) closeMenu();
}
function onMenuKey(e: KeyboardEvent) {
  if (e.key === "Escape") closeMenu();
}
watch(menuOpen, (open) => {
  if (open) {
    document.addEventListener("click", onDocClick);
    document.addEventListener("keydown", onMenuKey);
  } else {
    document.removeEventListener("click", onDocClick);
    document.removeEventListener("keydown", onMenuKey);
  }
});
onUnmounted(() => {
  document.removeEventListener("click", onDocClick);
  document.removeEventListener("keydown", onMenuKey);
});

/* 状态对应的主操作：进行中→标记达成；已达成/已归档→重新打开。 */
function primaryAction(g: WireGoal): { label: string; status: "done" | "active" } {
  return g.status === "active"
    ? { label: C.goalMarkDone, status: "done" }
    : { label: C.goalReopen, status: "active" };
}

/* App 页头的「新建目标」按钮经 store 信号触发这里的对话框。 */
watch(
  () => st.goalsNewSeq,
  (seq) => {
    if (seq > 0) editing.value = "new";
  },
);

/* 计划页日历上的里程碑标记：跳转该目标详情。 */
watch(
  () => st.goalsOpenSeq,
  (seq) => {
    if (seq > 0 && st.goals.some((g) => g.id === st.goalsOpenId)) {
      viewing.value = st.goalsOpenId;
      adding.value = null;
    }
  },
);

/* ── 视图数据 ─────────────────────────────────────────────────────────── */

const activeGoals = computed(() => st.goals.filter((g) => g.status === "active"));
const settledGoals = computed(() => st.goals.filter((g) => g.status !== "active"));
const goal = computed(() => st.goals.find((g) => g.id === viewing.value) || null);

/* 详情视图激活时同步给 App：页头在详情页隐藏（自带返回行，页头重复）。 */
watch(
  goal,
  (g) => {
    st.goalsViewing = !!g;
  },
  { immediate: true },
);

/* ── 进度（纯推导，不落库） ─────────────────────────────────────────── */

function tasksOf(g: WireGoal): WireGoalTask[] {
  return g.milestones.flatMap((m) => m.tasks);
}

function progressOf(g: WireGoal): number {
  const all = tasksOf(g);
  if (!all.length) return 0;
  return Math.round((all.filter((t) => t.done).length / all.length) * 100);
}

/* 前面有任何未完成里程碑时，当前里程碑锁定。锁只是默认顺序，允许
   「跳过」——先完成后面的里程碑即可（不制造死锁）。 */
function isLocked(g: WireGoal, index: number): boolean {
  return g.milestones.slice(0, index).some((m) => m.tasks.some((t) => !t.done));
}

/* 当前步：第一个「前面都完成、自己还有未完成项」的里程碑。详情页给
   这一行加一点底色，让「现在该做哪步」不用找。 */
function isCurrent(g: WireGoal, index: number): boolean {
  return !isLocked(g, index) && g.milestones[index].tasks.some((t) => !t.done);
}

function stepLabel(g: WireGoal, index: number): string {
  const n = index + 1;
  if (isCurrent(g, index)) return C.goalCurrentStep.replace("{n}", String(n));
  return C.goalStep.replace("{n}", String(n));
}

const priorityLabel = (p: number) => (p === 1 ? C.high : p === 2 ? C.medium : C.low);

/* 里程碑的日期区间文案：空如实显示「未设置日期」。 */
function msDateText(m: WireGoalMilestone): string {
  return (m.start || C.goalMilestoneNoDate) + " " + C.goalMilestoneDateSep + " " + (m.due || C.goalMilestoneNoDate);
}

/* ── 列表页统计图：里程碑甘特（替代原三格数字块） ─────────────────────
   只统计进行中的目标；数据全部来自现有 API（里程碑），纯展示、不改任何
   服务端状态。 */

const gantt = computed(() => ganttChart(activeGoals.value));

/* ── 里程碑操作 ───────────────────────────────────────────────────────── */

/* 添加里程碑的内联表单。 */
const msFormOpen = ref(false);
const msDraft = ref({ title: "", start: "", due: "" });
const msError = ref("");

/* 里程碑展开态：默认收起；当前步默认展开。 */
const expanded = ref<Record<number, boolean>>({});
function isExpanded(g: WireGoal, index: number, m: WireGoalMilestone): boolean {
  return expanded.value[m.id] ?? isCurrent(g, index);
}
function toggleExpand(id: number) {
  expanded.value[id] = !(expanded.value[id] ?? false);
}

/* 里程碑行内编辑（标题 + 起止日期）。 */
const editingMs = ref<number | null>(null);
const msEditDraft = ref({ title: "", start: "", due: "" });

function startEditMs(m: WireGoalMilestone) {
  editingMs.value = m.id;
  msEditDraft.value = { title: m.title, start: m.start, due: m.due };
}

function msDateError(draft: { start: string; due: string }): string {
  return draft.start && draft.due && draft.start > draft.due ? C.goalMilestoneDateError : "";
}

async function submitMs() {
  const g = goal.value;
  if (!g) return;
  if (!msDraft.value.title.trim()) {
    msError.value = C.goalErrTitle;
    return;
  }
  const dateErr = msDateError(msDraft.value);
  if (dateErr) {
    msError.value = dateErr;
    return;
  }
  msError.value = "";
  const ok = await addMilestone(g.id, {
    title: msDraft.value.title.trim(),
    start: msDraft.value.start,
    due: msDraft.value.due,
  });
  if (ok) {
    msDraft.value = { title: "", start: "", due: "" };
    msFormOpen.value = false;
  }
}

async function submitMsEdit(m: WireGoalMilestone) {
  const g = goal.value;
  if (!g) return;
  if (!msEditDraft.value.title.trim()) {
    msError.value = C.goalErrTitle;
    return;
  }
  const dateErr = msDateError(msEditDraft.value);
  if (dateErr) {
    msError.value = dateErr;
    return;
  }
  msError.value = "";
  const ok = await saveMilestone(
    g.id,
    m.id,
    { title: msEditDraft.value.title.trim(), start: msEditDraft.value.start, due: msEditDraft.value.due },
    m.version,
  );
  if (ok) editingMs.value = null;
}

/* ── 任务操作 ─────────────────────────────────────────────────────────── */

function toggleTaskInGoal(t: WireGoalTask) {
  const item = st.items.find((i) => i.id === t.taskId);
  if (!item) {
    notify(C.syncError, "error");
    loadGoals(true);
    return;
  }
  toggleGoalTask(goal.value!.id, item);
}

/* 里程碑下的「新建任务」：打开与计划页同一个 TaskDialog。 */
function openMsTaskDialog(m: WireGoalMilestone) {
  adding.value = {
    msId: m.id,
    item: { id: "", t: "", due: 0, g: C.personal, p: 3, done: false, at: 0, version: 0 },
  };
}

/* 对话框保存：创建任务并挂到对应里程碑（item.id 恒为空，只处理新建）。 */
async function onSubtaskSave(item: Item) {
  const g = goal.value;
  const target = adding.value;
  if (!g || !target) return;
  adding.value = null;
  await createTaskInGoal(g.id, target.msId, { t: item.t, p: item.p, due: item.due, g: item.g });
  expanded.value[target.msId] = true;
}

/* ── 目标操作 ─────────────────────────────────────────────────────────── */

function openGoal(id: number) {
  viewing.value = id;
  adding.value = null;
}

function back() {
  viewing.value = null;
  adding.value = null;
}

async function changeStatus(status: "done" | "active" | "archived") {
  const g = goal.value;
  if (!g) return;
  const input: GoalInput = {
    title: g.title, goalText: g.goalText, acceptance: g.acceptance, failureCost: g.failureCost,
    group: g.group, status,
  };
  await saveGoal(g.id, input, g.version);
}

async function onDialogSave(input: GoalInput) {
  const e = editing.value;
  if (e === "new") {
    // 创建后停留列表页，不自动跳进详情。
    await addGoal(input);
  } else if (e) {
    await saveGoal(e.id, input, e.version);
  }
  editing.value = null;
}

/* 进行中卡片的左移/右移：与相邻目标交换位置（整单提交由 reorderGoals 完成）。 */
function moveGoal(g: WireGoal, dir: -1 | 1) {
  const idx = activeGoals.value.findIndex((a) => a.id === g.id);
  const neighbor = activeGoals.value[idx + dir];
  if (idx < 0 || !neighbor) return;
  void reorderGoals(g.id, neighbor.id);
}

const RING_CIRC = 213.6;
function ringOffset(g: WireGoal): number {
  return RING_CIRC * (1 - progressOf(g) / 100);
}
</script>

<template>
  <!-- ── 详情视图 ── -->
  <div v-if="goal" class="goals-view">
    <div class="goal-detail-topbar">
      <button class="text-button" @click="back">
        <Icon name="arrow" :size="14" style="transform: rotate(180deg)" />
        {{ C.goals }}
      </button>
    </div>

    <section class="goal-detail">
      <div class="goal-detail-head">
        <div class="goal-ring">
          <svg viewBox="0 0 88 88">
            <circle class="track" cx="44" cy="44" r="34" />
            <circle class="val" cx="44" cy="44" r="34" :stroke-dashoffset="ringOffset(goal)" />
          </svg>
          <b>{{ progressOf(goal) }}{{ C.goalProgressUnit }}</b>
        </div>
        <div class="goal-detail-title">
          <h3>{{ goal.title }}</h3>
          <p class="goal-detail-meta">
            {{ C.goalTasksCount.replace("{done}", String(tasksOf(goal).filter((t) => t.done).length)).replace("{total}", String(tasksOf(goal).length)) }}
          </p>
        </div>
        <div class="goal-detail-actions">
          <button class="primary-button" @click="changeStatus(primaryAction(goal).status)">
            {{ primaryAction(goal).label }}
          </button>
          <button class="icon-button" :aria-label="C.goalEdit" @click="editing = goal">
            <Icon name="edit" :size="15" />
          </button>
          <div class="goal-menu">
            <button
              class="icon-button"
              :aria-expanded="menuOpen"
              :aria-label="C.goalMore"
              @click.stop="menuOpen = !menuOpen"
            >
              <Icon name="more" :size="16" />
            </button>
            <div v-if="menuOpen" class="goal-menu-panel" role="menu">
              <button
                v-if="goal.status !== 'archived'"
                class="goal-menu-item"
                role="menuitem"
                @click="changeStatus('archived'); closeMenu()"
              >
                {{ C.goalArchive }}
              </button>
              <button
                class="goal-menu-item danger"
                role="menuitem"
                @click="deleteGoal(goal.id); closeMenu(); viewing = null"
              >
                {{ C.goalDelete }}
              </button>
            </div>
          </div>
        </div>
      </div>

      <!-- 目标决策三问：非空才展示，没回答就不占位。整块可折叠；长答案
           超过三行折叠成「展开」，短答案与问题同一行，不再撑出大片空白。 -->
      <div v-if="goal.goalText || goal.acceptance || goal.failureCost" class="goal-strategy">
        <div class="goal-strategy-head">
          <h3 class="goal-strategy-title">{{ C.goalStrategy }}</h3>
          <button
            type="button"
            class="goal-strategy-fold"
            :aria-expanded="strategyOpen"
            :aria-label="strategyOpen ? C.goalStrategyFold : C.goalStrategyUnfold"
            @click="strategyOpen = !strategyOpen"
          >
            <Icon name="arrow" :size="12" :style="strategyOpen ? 'transform: rotate(90deg)' : ''" />
          </button>
        </div>
        <div v-show="strategyOpen" class="goal-strategy-body">
          <div v-if="goal.goalText" class="goal-strategy-item">
            <span class="goal-strategy-q">{{ C.goalStrategyGoal }}</span>
            <p :ref="bindStrategyP('goalText')" :class="{ 'is-clamped': !strategyExpanded.has('goalText') }">
              {{ goal.goalText }}
            </p>
            <button
              v-if="strategyOverflow.get('goalText')"
              type="button"
              class="goal-strategy-more"
              @click="toggleStrategyField('goalText')"
            >
              {{ strategyExpanded.has("goalText") ? C.goalStrategyCollapse : C.goalStrategyExpand }}
            </button>
          </div>
          <div v-if="goal.acceptance" class="goal-strategy-item">
            <span class="goal-strategy-q">{{ C.goalStrategyAcceptance }}</span>
            <p :ref="bindStrategyP('acceptance')" :class="{ 'is-clamped': !strategyExpanded.has('acceptance') }">
              {{ goal.acceptance }}
            </p>
            <button
              v-if="strategyOverflow.get('acceptance')"
              type="button"
              class="goal-strategy-more"
              @click="toggleStrategyField('acceptance')"
            >
              {{ strategyExpanded.has("acceptance") ? C.goalStrategyCollapse : C.goalStrategyExpand }}
            </button>
          </div>
          <div v-if="goal.failureCost" class="goal-strategy-item">
            <span class="goal-strategy-q">{{ C.goalStrategyFailure }}</span>
            <p :ref="bindStrategyP('failureCost')" :class="{ 'is-clamped': !strategyExpanded.has('failureCost') }">
              {{ goal.failureCost }}
            </p>
            <button
              v-if="strategyOverflow.get('failureCost')"
              type="button"
              class="goal-strategy-more"
              @click="toggleStrategyField('failureCost')"
            >
              {{ strategyExpanded.has("failureCost") ? C.goalStrategyCollapse : C.goalStrategyExpand }}
            </button>
          </div>
        </div>
      </div>

      <!-- 里程碑拆解 -->
      <div class="task-group-heading">
        <h2>{{ C.goalMilestones }}</h2>
        <span>{{ goal.milestones.length }}</span>
        <i />
      </div>

      <p v-if="!goal.milestones.length" class="goal-detail-empty">{{ C.goalNoMilestone }}</p>
      <div class="ms-list">
        <div
          v-for="(m, idx) in goal.milestones"
          :key="m.id"
          class="ms-block"
          :class="{ 'is-current': isCurrent(goal, idx) }"
        >
          <!-- 里程碑行：无完成勾选（里程碑是阶段不是任务），点左侧箭头展开任务。 -->
          <div class="task-row ms-row" :class="{ 'is-locked': isLocked(goal, idx) }">
            <button
              class="ms-caret"
              :aria-expanded="isExpanded(goal, idx, m)"
              :aria-label="m.title"
              @click="toggleExpand(m.id)"
            >
              <Icon name="arrow" :size="12" :style="isExpanded(goal, idx, m) ? 'transform: rotate(90deg)' : ''" />
            </button>
            <span class="step-tag" :class="{ now: isCurrent(goal, idx) }">
              {{ stepLabel(goal, idx) }}
            </span>
            <div class="task-body">
              <span class="task-title">{{ m.title }}</span>
              <span class="task-meta">
                <span v-if="isLocked(goal, idx)">{{ C.goalLockedHint }}</span>
                <span class="ms-date">
                  <Icon name="calendar" :size="11" />
                  {{ msDateText(m) }}
                </span>
                <span>{{ C.goalMilestoneTaskCount.replace("{n}", String(m.tasks.length)) }}</span>
              </span>
            </div>
            <div class="goal-task-actions">
              <button
                v-if="goal.status === 'active'"
                class="text-button"
                @click="openMsTaskDialog(m)"
              >
                {{ C.goalMilestoneNewTask }}
              </button>
              <button class="icon-button" :aria-label="C.goalMilestoneEdit + ' ' + m.title" @click="startEditMs(m)">
                <Icon name="edit" :size="14" />
              </button>
              <button class="icon-button danger" :aria-label="C.goalMilestoneDelete + ' ' + m.title" @click="deleteMilestone(goal.id, m.id)">
                <Icon name="trash" :size="14" />
              </button>
            </div>
          </div>

          <!-- 行内编辑：标题 + 起止日期。 -->
          <form v-if="editingMs === m.id" class="ms-form" @submit.prevent="submitMsEdit(m)">
            <input v-model="msEditDraft.title" maxlength="240" :aria-label="C.goalMilestoneName" />
            <label>
              {{ C.goalMilestoneStart }}
              <input v-model="msEditDraft.start" type="date" />
            </label>
            <label>
              {{ C.goalMilestoneDue }}
              <input v-model="msEditDraft.due" type="date" />
            </label>
            <div class="ms-form-actions">
              <button type="submit" class="primary-button">{{ C.save }}</button>
              <button type="button" class="text-button" @click="editingMs = null">{{ C.goalMilestoneCancel }}</button>
            </div>
            <p v-if="msError" role="alert" class="form-error">{{ msError }}</p>
          </form>

          <!-- 展开的任务项：可勾选完成、可移出目标。 -->
          <div v-if="isExpanded(goal, idx, m)" class="ms-tasks">
            <p v-if="!m.tasks.length" class="goal-detail-empty">{{ C.goalMilestoneEmpty }}</p>
            <div v-for="t in m.tasks" :key="t.taskId" class="task-row" :class="{ 'is-done': t.done }">
              <button
                class="task-check"
                :class="{ 'is-checked': t.done }"
                :aria-label="t.title"
                @click="toggleTaskInGoal(t)"
              >
                <Icon v-if="t.done" name="check" :size="12" />
              </button>
              <div class="task-body">
                <span class="task-title">{{ t.title }}</span>
                <span class="task-meta">
                  <span v-if="t.due">{{ t.due }}</span>
                  <span>{{ t.group }}</span>
                  <span>{{ priorityLabel(t.priority) }}</span>
                </span>
              </div>
              <div class="goal-task-actions">
                <button class="text-button danger" @click="detachTaskFromGoal(goal.id, t.taskId)">
                  {{ C.goalDetach }}
                </button>
              </div>
            </div>
          </div>
        </div>
      </div>

      <!-- 添加里程碑：内联表单（标题 + 起止日期），开始/截止与计划页日历联动。 -->
      <button v-if="goal.status === 'active' && !msFormOpen" class="goal-add-row" @click="msFormOpen = true">
        <Icon name="plus" :size="14" />
        {{ C.goalMilestoneAdd }}
      </button>
      <form v-if="msFormOpen" class="ms-form" @submit.prevent="submitMs">
        <input
          v-model="msDraft.title"
          maxlength="240"
          :aria-label="C.goalMilestoneName"
          :placeholder="C.goalMilestoneName"
        />
        <label>
          {{ C.goalMilestoneStart }}
          <input v-model="msDraft.start" type="date" />
        </label>
        <label>
          {{ C.goalMilestoneDue }}
          <input v-model="msDraft.due" type="date" />
        </label>
        <div class="ms-form-actions">
          <button type="submit" class="primary-button">{{ C.goalMilestoneAdd }}</button>
          <button type="button" class="text-button" @click="msFormOpen = false">{{ C.goalMilestoneCancel }}</button>
        </div>
        <p v-if="msError" role="alert" class="form-error">{{ msError }}</p>
      </form>
    </section>
  </div>

  <!-- ── 列表视图 ── -->
  <div v-else class="goals-view">
    <!-- 我的目标 / 目标进度：与计划页清单/日历同一套分段切换；
         主操作（新建目标）同行右侧，与计划页新建任务同款样式。 -->
    <div class="workspace-toolbar">
      <div class="view-switch" aria-label="goals">
        <button
          v-for="[v, label, icon] in [
            ['cards', C.goalViewCards, 'target'],
            ['gantt', C.goalViewGantt, 'stats'],
          ] as const"
          :key="v"
          :aria-pressed="view === v"
          :class="view === v ? 'is-active' : ''"
          @click="view = v"
        >
          <Icon :name="icon" />
          {{ label }}
        </button>
      </div>
      <button class="primary-button workspace-cta new-goal" @click="requestNewGoal">
        <Icon name="plus" />
        {{ C.goalNew }}
      </button>
    </div>

    <!-- 里程碑甘特图：所有进行中目标的里程碑排期与进度。
         无边框泳道式布局：无区段标题，刻度行自带时间轴基线，
         行底线收节奏，目标组之间用留白分隔。 -->
    <section v-if="view === 'gantt'" class="goal-chart" :aria-label="C.goalChartGanttTitle">
      <template v-if="gantt">
        <div class="gantt">
          <div class="gantt-canvas">
            <!-- 刻度行：三列与里程碑行一致；刻度轨 = 时间轨 + 40px，
                 刻度从今天起均匀分布（今天前不标日期），结尾刻度落在
                 时间轨右缘 +40px、紧邻进度列；行下沿拉通成时间轴基线。 -->
            <div class="gantt-ticks" aria-hidden="true">
              <span class="gantt-ticks-track">
                <span v-for="t in gantt.ticks" :key="t.pos" class="gantt-tick" :style="{ left: t.pos + '%' }">{{ t.label }}</span>
              </span>
            </div>
            <!-- 今天线定位层：与时间轨同宽同高，浮在所有行之上。
                 内部竖网格线已下线——刻度 + 今天线足够定位。 -->
            <div class="gantt-grid" aria-hidden="true">
              <span v-if="gantt.todayPos !== null" class="gantt-today" :style="{ left: gantt.todayPos + '%' }">
                <em>{{ C.goalChartToday }}</em>
              </span>
            </div>
            <div v-for="grp in gantt.groups" :key="grp.id" class="gantt-goal">
              <header class="gantt-goal-head">
                <span class="gantt-goal-title">{{ grp.title }}</span>
                <span class="gantt-goal-pct">{{ grp.pct }}{{ C.goalProgressUnit }}</span>
              </header>
              <div v-for="row in grp.rows" :key="row.id" class="gantt-row">
                <span class="gantt-row-label" :title="row.title">
                  <i v-if="row.current" class="gantt-current-dot" :title="C.goalCurrentStepShort" />{{ row.title }}
                </span>
                <div class="gantt-track">
                  <span
                    v-if="row.kind !== 'undated'"
                    :class="['gantt-bar', row.kind]"
                    :style="{ left: row.left + '%', width: row.width + '%' }"
                    :title="`${row.title} · ${row.dateText} · ${row.done}/${row.total}`"
                  >
                    <span class="gantt-fill" :style="{ width: row.fill * 100 + '%' }" />
                  </span>
                </div>
                <span class="gantt-row-meta">{{ row.kind === "undated" ? C.goalChartUndated : `${row.done}/${row.total}` }}</span>
              </div>
            </div>
          </div>
        </div>
      </template>
      <p v-else class="goals-empty">{{ C.goalChartGanttEmpty }}</p>
    </section>

    <template v-else>
      <div class="section-heading">
        <h2>{{ C.goalsActiveSection }}</h2>
        <span>{{ activeGoals.length }}</span>
        <i />
      </div>
      <div v-if="activeGoals.length" class="goal-cards">
        <!-- 卡片内有排序按钮，按钮套按钮不合法，故用 div + role="button" 保持键盘可达。 -->
        <div
          v-for="(g, idx) in activeGoals"
          :key="g.id"
          class="goal-card"
          role="button"
          tabindex="0"
          @click="openGoal(g.id)"
          @keydown.enter.prevent="openGoal(g.id)"
        >
          <button
            v-if="idx > 0"
            class="goal-move goal-move-prev"
            :aria-label="C.goalMoveLeft + ' ' + g.title"
            @click.stop="moveGoal(g, -1)"
          >
            <Icon name="arrow" :size="13" style="transform: rotate(180deg)" />
          </button>
          <button
            v-if="idx < activeGoals.length - 1"
            class="goal-move goal-move-next"
            :aria-label="C.goalMoveRight + ' ' + g.title"
            @click.stop="moveGoal(g, 1)"
          >
            <Icon name="arrow" :size="13" />
          </button>
          <div class="goal-card-top">
            <h3>{{ g.title }}</h3>
            <span v-if="g.group" class="mode-tag">{{ g.group }}</span>
          </div>
          <p v-if="g.goalText" class="goal-note">{{ g.goalText }}</p>
          <div class="progress-track">
            <div class="progress-fill" :style="{ width: progressOf(g) + '%' }" />
          </div>
          <div class="goal-card-foot">
            <span>
              {{
                C.goalTasksCount
                  .replace("{done}", String(tasksOf(g).filter((t) => t.done).length))
                  .replace("{total}", String(tasksOf(g).length))
              }}
            </span>
            <span class="pct">{{ progressOf(g) }}{{ C.goalProgressUnit }}</span>
          </div>
        </div>
      </div>

      <div class="section-heading">
        <h2>{{ C.goalsSettledSection }}</h2>
        <span>{{ settledGoals.length }}</span>
        <i />
      </div>
      <div v-if="settledGoals.length" class="goal-cards">
        <button v-for="g in settledGoals" :key="g.id" class="goal-card is-done" @click="openGoal(g.id)">
          <div class="goal-card-top">
            <h3>{{ g.title }}</h3>
            <span v-if="g.group" class="mode-tag">{{ g.group }}</span>
          </div>
          <div class="progress-track">
            <div class="progress-fill" :style="{ width: progressOf(g) + '%' }" />
          </div>
          <div class="goal-card-foot">
            <span>
              {{
                C.goalTasksCount
                  .replace("{done}", String(tasksOf(g).filter((t) => t.done).length))
                  .replace("{total}", String(tasksOf(g).length))
              }}
            </span>
            <span class="pct">{{ progressOf(g) }}{{ C.goalProgressUnit }}</span>
          </div>
        </button>
      </div>

      <p v-if="!st.goals.length && st.goalsLoaded" class="goals-empty">{{ C.goalsEmpty }}</p>
    </template>
  </div>

  <GoalDialog v-if="editing" :goal="editing === 'new' ? null : editing" @save="onDialogSave" @close="editing = null" />
  <TaskDialog v-if="adding" :item="adding.item" @save="onSubtaskSave" @close="adding = null" />
</template>
