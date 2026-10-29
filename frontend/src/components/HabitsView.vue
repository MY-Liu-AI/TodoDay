<script setup lang="ts">
/* 习惯页：工作区级的定期练习（健身/阅读这类没有终点的重复事项），
   独立于目标模块。顶部添加表单（名称 + 每周哪几天），列表每行给出
   本周打卡条：只在排了的星期有格子，点格子勾选/取消该次实例（实例
   是真实任务，勾选走任务完成端点）。进入页面时读取习惯列表——服务
   端在读取时滚动补齐未来 8 周的实例任务，所以读取即「展开引擎」。 */
import { computed, onMounted, ref } from "vue";
import { C } from "../copy";
import { st, loadHabits, addHabit, saveHabit, deleteHabit, toggleHabitOccurrence } from "../store";
import type { WireHabit } from "../api";
import { nextOccurrence, weekStrip, WEEKDAY_SHORT } from "../lib/habits.js";
import { TODAY } from "../lib/todo.js";
import Icon from "./Icon.vue";

onMounted(() => {
  if (!st.habitsLoaded) void loadHabits();
});

/* 本周一到周日的打卡条骨架（lib 纯函数，可单测）。 */
const weekCells = computed(() => weekStrip());
/* 打卡条「已过期未完成」判定用（与 lib/todo.js 同一今天口径）。 */
const todayTs = TODAY;

const draft = ref({ title: "", days: [] as number[] });
const error = ref("");
const editingId = ref<number | null>(null);
const editDraft = ref({ title: "", days: [] as number[] });

function toggleDay(days: number[], d: number) {
  const i = days.indexOf(d);
  if (i >= 0) days.splice(i, 1);
  else days.push(d);
  error.value = "";
}

function dayOn(days: number[], d: number) {
  return days.includes(d);
}

async function submit() {
  if (!draft.value.title.trim()) {
    error.value = C.habitNameError;
    return;
  }
  if (!draft.value.days.length) {
    error.value = C.habitDayError;
    return;
  }
  error.value = "";
  const ok = await addHabit({
    title: draft.value.title.trim(),
    days: [...draft.value.days].sort((a, b) => a - b),
  });
  if (ok) draft.value = { title: "", days: [] };
}

function startEdit(h: WireHabit) {
  editingId.value = h.id;
  editDraft.value = { title: h.title, days: [...h.weekdays] };
}

async function submitEdit(h: WireHabit) {
  if (!editDraft.value.title.trim() || !editDraft.value.days.length) {
    error.value = !editDraft.value.title.trim() ? C.habitNameError : C.habitDayError;
    return;
  }
  error.value = "";
  const ok = await saveHabit(
    h.id,
    { title: editDraft.value.title.trim(), days: [...editDraft.value.days].sort((a, b) => a - b) },
    h.version,
  );
  if (ok) editingId.value = null;
}

/* 打卡条某格对应的实例任务（按日期找）。 */
function taskOn(h: WireHabit, date: string) {
  return h.tasks.find((t) => t.due === date) || null;
}

function nextText(h: WireHabit): string {
  const n = nextOccurrence(h.weekdays);
  if (!n) return "";
  const d = new Date(n.ts);
  const wd = WEEKDAY_SHORT[(d.getDay() + 6) % 7];
  return C.habitNext.replace("{date}", `${d.getMonth() + 1}/${d.getDate()}（周${wd}）`);
}
</script>

<template>
  <div class="habits-view">
    <form class="habit-add" @submit.prevent="submit">
      <input
        v-model="draft.title"
        maxlength="240"
        :placeholder="C.habitNamePlaceholder"
        :aria-label="C.habitName"
        @input="error = ''"
      />
      <div class="habit-days" role="group" :aria-label="C.habitRepeat">
        <button
          v-for="d in 7"
          :key="d"
          type="button"
          :class="['habit-chip', { on: dayOn(draft.days, d) }]"
          :aria-pressed="dayOn(draft.days, d)"
          :aria-label="C.habitWeekdays[d - 1]"
          @click="toggleDay(draft.days, d)"
        >
          {{ WEEKDAY_SHORT[d - 1] }}
        </button>
      </div>
      <button class="primary-button" type="submit">{{ C.habitAdd }}</button>
    </form>
    <p v-if="error" role="alert" class="form-error">{{ error }}</p>

    <p v-if="!st.habits.length && st.habitsLoaded" class="goal-detail-empty">{{ C.habitEmpty }}</p>

    <div class="habit-list">
      <div v-for="h in st.habits" :key="h.id" class="habit-row">
        <!-- 行内编辑：标题 + 星期胶囊。 -->
        <form v-if="editingId === h.id" class="habit-edit" @submit.prevent="submitEdit(h)">
          <input v-model="editDraft.title" maxlength="240" :aria-label="C.habitName" />
          <div class="habit-days" role="group" :aria-label="C.habitRepeat">
            <button
              v-for="d in 7"
              :key="d"
              type="button"
              :class="['habit-chip', { on: dayOn(editDraft.days, d) }]"
              :aria-pressed="dayOn(editDraft.days, d)"
              :aria-label="C.habitWeekdays[d - 1]"
              @click="toggleDay(editDraft.days, d)"
            >
              {{ WEEKDAY_SHORT[d - 1] }}
            </button>
          </div>
          <div class="habit-edit-actions">
            <button type="submit" class="primary-button">{{ C.save }}</button>
            <button type="button" class="text-button" @click="editingId = null">{{ C.goalMilestoneCancel }}</button>
          </div>
        </form>

        <template v-else>
          <div class="habit-main">
            <div class="habit-head">
              <span class="habit-title">{{ h.title }}</span>
              <span class="habit-days static" aria-hidden="true">
                <span v-for="d in 7" :key="d" :class="['habit-chip', 'static', { on: dayOn(h.weekdays, d) }]">
                  {{ WEEKDAY_SHORT[d - 1] }}
                </span>
              </span>
              <span class="habit-next">{{ nextText(h) }}</span>
            </div>
            <!-- 本周打卡条：只在排了的星期有格子，点格子勾选/取消该次实例。 -->
            <div class="habit-week">
              <span
                v-for="cell in weekCells"
                :key="cell.date"
                class="habit-weekday"
                :class="{ today: cell.today, on: dayOn(h.weekdays, cell.weekday) }"
              >
                <i>{{ WEEKDAY_SHORT[cell.weekday - 1] }}</i>
                <button
                  v-if="dayOn(h.weekdays, cell.weekday)"
                  type="button"
                  class="habit-day"
                  :class="{
                    done: taskOn(h, cell.date)?.done,
                    missed: !cell.today && cell.ts < todayTs && !taskOn(h, cell.date)?.done,
                    today: cell.today,
                  }"
                  :aria-label="`${h.title} ${cell.date}`"
                  :aria-pressed="!!taskOn(h, cell.date)?.done"
                  @click="
                    taskOn(h, cell.date) &&
                      toggleHabitOccurrence(taskOn(h, cell.date)!.taskId, taskOn(h, cell.date)!.done, taskOn(h, cell.date)!.version)
                  "
                >
                  {{ new Date(cell.ts).getDate() }}
                </button>
                <span v-else class="habit-day off" aria-hidden="true">·</span>
              </span>
            </div>
          </div>
          <div class="goal-task-actions">
            <button class="icon-button" :aria-label="C.habitEdit + ' ' + h.title" @click="startEdit(h)">
              <Icon name="edit" :size="14" />
            </button>
            <button class="icon-button danger" :aria-label="C.habitDelete + ' ' + h.title" @click="deleteHabit(h.id)">
              <Icon name="trash" :size="14" />
            </button>
          </div>
        </template>
      </div>
    </div>
  </div>
</template>
