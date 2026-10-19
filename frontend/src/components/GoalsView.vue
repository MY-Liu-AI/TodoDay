<script setup lang="ts">
/* 目标模块页面：列表（统计条 + 进行中/已落定卡片）与详情两个视图。
   点卡片跳转到该目标的详情视图（返回键回到列表）。
   进度不落库——由挂载任务（子任务）的完成情况推导；串行模式按创建顺序解锁，
   锁定只是默认顺序，服务端不强制。子任务在列表末行的入口添加，复用计划页
   的 TaskDialog（标题/日期/分组/优先级），创建即入清单并挂到目标。 */
import { computed, onMounted, onUnmounted, ref, watch } from "vue";
import { C } from "../copy";
import { dateValue } from "../lib/model.js";
import {
  st,
  notify,
  loadGoals,
  addGoal,
  saveGoal,
  deleteGoal,
  createTaskInGoal,
  detachTaskFromGoal,
  toggleGoalTask,
  type GoalInput,
} from "../store";
import type { Item } from "../store";
import type { WireGoal, WireGoalTask } from "../api";
import Icon from "./Icon.vue";
import GoalDialog from "./GoalDialog.vue";
import TaskDialog from "./TaskDialog.vue";

/* 正在查看的目标 id；null = 列表视图。 */
const viewing = ref<number | null>(null);
const editing = ref<WireGoal | "new" | null>(null);
/* 正在用 TaskDialog 新建的子任务草稿（id 为空 = 新建）。 */
const adding = ref<Item | null>(null);

onMounted(() => loadGoals());
/* 切走页面时组件卸载，页头状态复位，回列表不受影响。 */
onUnmounted(() => {
  st.goalsViewing = false;
});

/* App 页头的「新建目标」按钮经 store 信号触发这里的对话框。 */
watch(
  () => st.goalsNewSeq,
  (seq) => {
    if (seq > 0) editing.value = "new";
  },
);

/* ── 视图数据 ─────────────────────────────────────────────────────────── */

const activeGoals = computed(() => st.goals.filter((g) => g.status === "active"));
const settledGoals = computed(() => st.goals.filter((g) => g.status !== "active"));
const goal = computed(() => st.goals.find((g) => g.id === viewing.value) || null);

/* 详情视图激活时同步给 App：页头（大标题/副标题/新建目标按钮）在详情页隐藏。 */
watch(
  goal,
  (g) => {
    st.goalsViewing = !!g;
  },
  { immediate: true },
);

/* ── 进度与健康度（纯推导，不落库） ───────────────────────────────────── */

function progressOf(g: WireGoal): number {
  if (!g.tasks.length) return 0;
  return Math.round((g.tasks.filter((t) => t.done).length / g.tasks.length) * 100);
}

type Health = { kind: "ok" | "risk" | "late" | "none"; label: string };

function healthOf(g: WireGoal): Health {
  if (g.status === "done") return { kind: "ok", label: C.goalStatusDone };
  if (g.status === "archived") return { kind: "none", label: C.goalStatusArchived };
  if (!g.due || !g.createdAt) return { kind: "none", label: C.goalHealthNone };
  const today = dateValue(Date.now());
  const span = +new Date(g.due) - +new Date(g.createdAt);
  if (span <= 0) return { kind: "none", label: C.goalHealthNone };
  const timePct = Math.round(((+new Date(today) - +new Date(g.createdAt)) / span) * 100);
  const taskPct = progressOf(g);
  const diff = taskPct - timePct;
  if (diff >= 0) return { kind: "ok", label: C.goalHealthOk };
  if (diff >= -20) return { kind: "risk", label: C.goalHealthRisk };
  return { kind: "late", label: C.goalHealthLate };
}

function daysLeft(g: WireGoal): { n: number; over: boolean } | null {
  if (!g.due) return null;
  const days = Math.round((+new Date(g.due) - +new Date(dateValue(Date.now()))) / 86400000);
  return { n: Math.abs(days), over: days < 0 };
}

/* 串行：前面有任何未完成项，当前项锁定。锁只是默认顺序，允许「跳过」——
   在清单里先完成后面的任务即可（不制造死锁）。 */
function isLocked(g: WireGoal, index: number): boolean {
  if (g.mode !== "sequential") return false;
  const t = g.tasks[index];
  return !t.done && g.tasks.slice(0, index).some((p) => !p.done);
}

function stepLabel(g: WireGoal, index: number): string {
  const n = index + 1;
  if (g.mode !== "sequential") return "";
  if (!g.tasks[index].done && !g.tasks.slice(0, index).some((p) => !p.done)) {
    return C.goalCurrentStep.replace("{n}", String(n));
  }
  return C.goalStep.replace("{n}", String(n));
}

const priorityLabel = (p: number) => (p === 1 ? C.high : p === 2 ? C.medium : C.low);

const stats = computed(() => {
  const active = activeGoals.value;
  const avg = active.length
    ? Math.round(active.reduce((s, g) => s + progressOf(g), 0) / active.length)
    : 0;
  const risk = active.filter((g) => ["risk", "late"].includes(healthOf(g).kind)).length;
  return {
    active: active.length,
    avg,
    risk,
    done: settledGoals.value.filter((g) => g.status === "done").length,
  };
});

/* ── 操作 ─────────────────────────────────────────────────────────────── */

function openGoal(id: number) {
  viewing.value = id;
  adding.value = null;
}

function back() {
  viewing.value = null;
  adding.value = null;
}

function toggleTaskInGoal(t: WireGoalTask) {
  const item = st.items.find((i) => i.id === t.taskId);
  if (!item) {
    notify(C.syncError, "error");
    loadGoals(true);
    return;
  }
  toggleGoalTask(goal.value!.id, item);
}

/* 列表末行的添加入口：打开与计划页同一个 TaskDialog。 */
function openSubtaskDialog() {
  adding.value = { id: "", t: "", due: 0, g: C.personal, p: 3, done: false, at: 0, version: 0 };
}

/* 对话框保存：创建任务并挂到当前目标（id 恒为空，这里只处理新建）。 */
async function onSubtaskSave(item: Item) {
  const g = goal.value;
  if (!g) return;
  adding.value = null;
  await createTaskInGoal(g.id, { t: item.t, p: item.p, due: item.due, g: item.g });
}

async function changeStatus(status: "done" | "active" | "archived") {
  const g = goal.value;
  if (!g) return;
  const input: GoalInput = { title: g.title, note: g.note, mode: g.mode, due: g.due, status };
  await saveGoal(g.id, input, g.version);
}

async function onDialogSave(input: GoalInput) {
  const e = editing.value;
  if (e === "new") {
    const created = await addGoal(input);
    if (created) openGoal(created.id);
  } else if (e) {
    await saveGoal(e.id, input, e.version);
  }
  editing.value = null;
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
          <h3>
            {{ goal.title }}
            <span class="health" :class="healthOf(goal).kind">{{ healthOf(goal).label }}</span>
          </h3>
          <p v-if="goal.note">{{ goal.note }}</p>
          <p class="goal-detail-meta">
            <template v-if="goal.due">
              {{
                daysLeft(goal)!.over
                  ? C.goalDaysOver.replace("{n}", String(daysLeft(goal)!.n))
                  : C.goalDaysLeft.replace("{n}", String(daysLeft(goal)!.n))
              }}
            </template>
            <template v-else>{{ C.goalNoDue }}</template>
            · {{ C.goalTasksCount.replace("{done}", String(goal.tasks.filter((t) => t.done).length)).replace("{total}", String(goal.tasks.length)) }}
            <template v-if="goal.mode === 'sequential'"> · {{ C.goalModeSequential }}</template>
          </p>
        </div>
        <div class="goal-detail-actions">
          <button class="secondary-button" @click="editing = goal">{{ C.goalEdit }}</button>
          <button v-if="goal.status === 'active'" class="secondary-button" @click="changeStatus('done')">
            {{ C.goalMarkDone }}
          </button>
          <button v-if="goal.status === 'done'" class="secondary-button" @click="changeStatus('active')">
            {{ C.goalReopen }}
          </button>
          <button v-if="goal.status !== 'archived'" class="secondary-button" @click="changeStatus('archived')">
            {{ C.goalArchive }}
          </button>
          <button class="text-button danger" @click="deleteGoal(goal.id); viewing = null">{{ C.goalDelete }}</button>
        </div>
      </div>

      <div class="task-group-heading">
        <h2>{{ goal.mode === "sequential" ? C.goalMilestones : C.goalMilestonesParallel }}</h2>
        <span>{{ goal.tasks.length }}</span>
        <i />
      </div>

      <p v-if="!goal.tasks.length" class="goal-detail-empty">{{ C.goalAddTask }}</p>

      <div
        v-for="(t, idx) in goal.tasks"
        :key="t.taskId"
        class="task-row"
        :class="{ 'is-done': t.done, 'is-locked': isLocked(goal, idx) }"
      >
        <button
          v-if="!isLocked(goal, idx)"
          class="task-check"
          :class="{ 'is-checked': t.done }"
          :aria-label="t.title"
          @click="toggleTaskInGoal(t)"
        >
          <Icon v-if="t.done" name="check" :size="12" />
        </button>
        <span v-else class="lock" :title="C.goalLockedHint"><Icon name="lock" :size="11" /></span>
        <span v-if="goal.mode === 'sequential'" class="step-tag" :class="{ now: !t.done && !isLocked(goal, idx) }">
          {{ stepLabel(goal, idx) }}
        </span>
        <div class="task-body">
          <span class="task-title">{{ t.title }}</span>
          <span class="task-meta">
            <span v-if="isLocked(goal, idx)">{{ C.goalLockedHint }}</span>
            <span>{{ t.due || C.noDue }}</span>
            <span>{{ t.group }}</span>
            <span>{{ priorityLabel(t.priority) }}</span>
          </span>
        </div>
        <button class="text-button" @click="detachTaskFromGoal(goal.id, t.taskId)">{{ C.goalDetach }}</button>
      </div>

      <!-- 列表末行的添加入口：点开计划页同款对话框。 -->
      <button v-if="goal.status === 'active'" class="goal-add-row" @click="openSubtaskDialog">
        <Icon name="plus" :size="14" />
        {{ C.goalAddTask }}
      </button>
    </section>
  </div>

  <!-- ── 列表视图 ── -->
  <div v-else class="goals-view">
    <div class="goal-stats">
      <div class="goal-stat"><b>{{ stats.active }}</b><span>{{ C.goalsActiveCount }}</span></div>
      <div class="goal-stat"><b>{{ stats.avg }}{{ C.goalProgressUnit }}</b><span>{{ C.goalsAvgProgress }}</span></div>
      <div class="goal-stat" :class="{ 'is-warn': stats.risk > 0 }"><b>{{ stats.risk }}</b><span>{{ C.goalHealthRisk }}</span></div>
      <div class="goal-stat"><b>{{ stats.done }}</b><span>{{ C.goalStatusDone }}</span></div>
    </div>

    <div class="section-heading">
      <h2>{{ C.goalsActiveSection }}</h2>
      <span>{{ activeGoals.length }}</span>
      <i />
    </div>
    <div v-if="activeGoals.length" class="goal-cards">
      <button v-for="g in activeGoals" :key="g.id" class="goal-card" @click="openGoal(g.id)">
        <div class="goal-card-top">
          <h3>{{ g.title }}</h3>
          <span class="mode-tag">{{ g.mode === "sequential" ? C.goalModeSequential : C.goalModeParallel }}</span>
          <span class="health" :class="healthOf(g).kind">{{ healthOf(g).label }}</span>
        </div>
        <p v-if="g.note" class="goal-note">{{ g.note }}</p>
        <div class="progress-track">
          <div class="progress-fill" :style="{ width: progressOf(g) + '%' }" />
        </div>
        <div class="goal-card-foot">
          <span>
            {{
              C.goalTasksCount
                .replace("{done}", String(g.tasks.filter((t) => t.done).length))
                .replace("{total}", String(g.tasks.length))
            }}
          </span>
          <span v-if="daysLeft(g)" class="due" :class="{ 'is-late': daysLeft(g)!.over }">
            {{
              daysLeft(g)!.over
                ? C.goalDaysOver.replace("{n}", String(daysLeft(g)!.n))
                : C.goalDaysLeft.replace("{n}", String(daysLeft(g)!.n))
            }}
          </span>
          <span class="pct">{{ progressOf(g) }}{{ C.goalProgressUnit }}</span>
        </div>
      </button>
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
          <span class="mode-tag">{{ g.mode === "sequential" ? C.goalModeSequential : C.goalModeParallel }}</span>
          <span class="health" :class="healthOf(g).kind">{{ healthOf(g).label }}</span>
        </div>
        <div class="progress-track">
          <div class="progress-fill" :style="{ width: progressOf(g) + '%' }" />
        </div>
        <div class="goal-card-foot">
          <span>
            {{
              C.goalTasksCount
                .replace("{done}", String(g.tasks.filter((t) => t.done).length))
                .replace("{total}", String(g.tasks.length))
            }}
          </span>
          <span class="pct">{{ progressOf(g) }}{{ C.goalProgressUnit }}</span>
        </div>
      </button>
    </div>

    <p v-if="!st.goals.length && st.goalsLoaded" class="goals-empty">{{ C.goalsEmpty }}</p>
  </div>

  <GoalDialog v-if="editing" :goal="editing === 'new' ? null : editing" @save="onDialogSave" @close="editing = null" />
  <TaskDialog v-if="adding" :item="adding" @save="onSubtaskSave" @close="adding = null" />
</template>
