<script setup lang="ts">
/* 番茄专注页：左边挑今天的事 + 25 分钟倒计时；右边今日/本周/本月读数、
   近 4 周每日柱图、今天的专注记录。所有统计读自服务端。 */
import { computed, onMounted } from "vue";
import { C } from "../copy";
import { DAY, TODAY, midnight } from "../lib/todo.js";
import { dateValue } from "../lib/model.js";
import { groupColor } from "../lib/palette.js";
import { sound, haptic } from "../lib/fx";
import { st, toggleTask } from "../store";
import type { Item } from "../store";
import {
  fs,
  POMODORO_SEC,
  pick,
  start,
  pause,
  abandon,
  reset,
  retryPending,
  loadStats,
  todayAgg,
  weekAgg,
  monthAgg,
  fmtDuration,
  fmtClock,
} from "../focus";
import Icon from "./Icon.vue";

/* 今天要处理的事：今天截止 + 逾期未完成（逾期也该今天处理），未完成在前。 */
const todayTasks = computed(() =>
  st.items
    .filter((i) => i.due && midnight(i.due) <= TODAY)
    .sort((a, b) => Number(a.done) - Number(b.done) || a.p - b.p),
);
const busy = computed(() => fs.phase === "running" || fs.phase === "paused");
const progress = computed(() => 1 - fs.remaining / POMODORO_SEC);
const R = 88;
const CIRC = 2 * Math.PI * R;

const hint = computed(() => {
  switch (fs.phase) {
    case "running":
      return C.focusRunningHint;
    case "paused":
      return C.focusPausedHint;
    case "done":
      return C.focusDoneHint;
    default:
      return C.focusIdleHint;
  }
});

/* 近 4 周每日柱图，和热力格一样按本地日期键取服务端数据。 */
const dailyBars = computed(() => {
  const daily = fs.stats?.daily ?? {};
  const out: { ts: number; key: string; sec: number; n: number }[] = [];
  for (let k = 27; k >= 0; k--) {
    const ts = TODAY - k * DAY;
    const key = dateValue(ts);
    const d = daily[key];
    out.push({ ts, key, sec: d?.seconds ?? 0, n: d?.pomodoros ?? 0 });
  }
  return out;
});
const barMax = computed(() => Math.max(1, ...dailyBars.value.map((b) => b.sec)));

function onStart() {
  start();
  sound.tick();
  haptic(10);
}
function onPause() {
  pause();
  sound.tick();
}
function onAbandon() {
  abandon();
  haptic(8);
}
function onAgain() {
  reset();
}
function fmtTime(ms: number) {
  return new Date(ms).toLocaleTimeString(C.locale, { hour: "2-digit", minute: "2-digit" });
}
function choose(task: Item | null) {
  if (busy.value) return;
  pick(task);
  sound.tick();
}

/* 今天的事可以直接在这里勾掉：专注完顺手标记完成，不用切回清单。 */
function markDone(i: Item) {
  if (busy.value && fs.task?.id === i.id) return;
  toggleTask(i);
  i.done ? sound.tick() : sound.ding();
  haptic(14);
}

onMounted(() => loadStats());
</script>

<template>
  <div class="focus-layout">
    <section class="focus-main">
      <div class="focus-timer" :class="'is-' + fs.phase">
        <div class="focus-ring">
          <svg :width="R * 2 + 16" :height="R * 2 + 16" :viewBox="`0 0 ${R * 2 + 16} ${R * 2 + 16}`" aria-hidden="true">
            <circle class="track" :cx="R + 8" :cy="R + 8" :r="R" />
            <circle
              class="fill"
              :cx="R + 8"
              :cy="R + 8"
              :r="R"
              :stroke-dasharray="CIRC"
              :stroke-dashoffset="CIRC * (1 - progress)"
            />
          </svg>
          <div class="focus-clock" role="timer" aria-live="off" :aria-label="fmtClock(fs.remaining)">
            {{ fmtClock(fs.remaining) }}
          </div>
          <div class="focus-task-name">
            <template v-if="fs.phase === 'done'">{{ C.focusDoneTitle }}</template>
            <template v-else-if="fs.task">{{ fs.task.t }}</template>
            <template v-else>{{ C.focusNoTask }}</template>
          </div>
        </div>
        <p class="focus-hint">{{ hint }}</p>
        <div class="focus-controls">
          <template v-if="fs.phase === 'idle'">
            <button class="primary-button" @click="onStart">
              <Icon name="play" :size="16" />
              {{ C.focusStart }}
            </button>
          </template>
          <template v-else-if="fs.phase === 'running'">
            <button class="secondary-button" @click="onPause">
              <Icon name="pause" :size="16" />
              {{ C.focusPause }}
            </button>
            <button class="text-button danger" @click="onAbandon">{{ C.focusAbandon }}</button>
          </template>
          <template v-else-if="fs.phase === 'paused'">
            <button class="primary-button" @click="onStart">
              <Icon name="play" :size="16" />
              {{ C.focusResume }}
            </button>
            <button class="text-button danger" @click="onAbandon">{{ C.focusAbandon }}</button>
          </template>
          <template v-else>
            <button class="primary-button" @click="onAgain">
              <Icon name="timer" :size="16" />
              {{ C.focusAgain }}
            </button>
          </template>
        </div>
        <p v-if="fs.pending" role="alert" class="focus-pending">
          {{ C.focusSaveFailed }}
          <button class="text-button" :disabled="fs.saving" @click="retryPending">{{ C.focusRetry }}</button>
        </p>
      </div>

      <div class="focus-picker">
        <div class="overview-heading">
          <h2>{{ C.focusTodayList }}</h2>
          <span>{{ todayTasks.length }} {{ C.tasks }}</span>
        </div>
        <div v-if="!todayTasks.length" class="agenda-empty">{{ C.focusEmpty }}</div>
        <div v-else class="focus-task-list">
          <div
            v-for="i in todayTasks"
            :key="i.id"
            class="focus-task"
            :class="[fs.task?.id === i.id ? 'is-picked' : '', i.done ? 'is-done' : '']"
            :data-color="groupColor(i.g)"
          >
            <button
              class="task-check"
              :class="i.done ? 'is-checked' : ''"
              :aria-label="(i.done ? C.focusToggleReopen : C.focusToggleDone) + ' ' + i.t"
              :aria-pressed="i.done"
              @click="markDone(i)"
            >
              <Icon name="check" :size="14" />
            </button>
            <button
              class="focus-task-pick"
              :disabled="busy && fs.task?.id !== i.id"
              :aria-pressed="fs.task?.id === i.id"
              :aria-label="C.focusPick + ' ' + i.t"
              @click="choose(fs.task?.id === i.id ? null : i)"
            >
              <span class="event-dot" />
              <span class="focus-task-title">{{ i.t }}</span>
              <span class="task-group-label">{{ i.g }}</span>
              <span class="focus-task-state">
                <template v-if="fs.task?.id === i.id">{{ busy ? C.focusPicked : C.focusChange }}</template>
                <template v-else>{{ C.focusPick }}</template>
              </span>
            </button>
          </div>
        </div>
      </div>
    </section>

    <section class="focus-side">
      <p v-if="fs.loadError" role="alert" class="storage-warning">
        {{ fs.loadError }}
        <button class="text-button" @click="loadStats(true)">{{ C.retry }}</button>
      </p>
      <div class="stats focus-stats">
        <div>
          <div class="n">{{ todayAgg().pomodoros }}</div>
          <div class="l">{{ C.focusTodayCount }}</div>
        </div>
        <div>
          <div class="n">{{ fmtDuration(todayAgg().seconds) }}</div>
          <div class="l">{{ C.focusTodayTime }}</div>
        </div>
        <div>
          <div class="n">{{ fmtDuration(weekAgg().seconds) }}</div>
          <div class="l">{{ C.focusWeekTime }}</div>
        </div>
        <div>
          <div class="n">{{ fmtDuration(monthAgg().seconds) }}</div>
          <div class="l">{{ C.focusMonthTime }}</div>
        </div>
      </div>

      <div class="grouphead"><span>{{ C.focusDailyTitle }}</span></div>
      <div class="weekbar focus-daily">
        <div
          v-for="b in dailyBars"
          :key="b.key"
          class="d"
          :title="`${b.key} · ${fmtDuration(b.sec)} · ${b.n} 🍅`"
        >
          <div class="b" :style="{ height: (b.sec / barMax) * 58 + 2 + 'px' }" />
          <div v-if="new Date(b.ts).getDay() === 1 || b.ts === TODAY" class="l">
            {{ b.ts === TODAY ? C.today : new Date(b.ts).getDate() }}
          </div>
        </div>
      </div>
      <p class="history-note">{{ C.focusStatsNote }}</p>

      <div class="grouphead"><span>{{ C.focusTodaySessions }}</span></div>
      <p v-if="!fs.stats?.sessions.length" class="agenda-empty">{{ C.focusNoSessions }}</p>
      <div v-else class="focus-sessions">
        <div v-for="s in fs.stats!.sessions" :key="s.id" class="focus-session" :class="'is-' + s.status">
          <span class="focus-session-time">{{ fmtTime(s.endedAt) }}</span>
          <span class="focus-session-title">{{ s.taskTitle || C.focusNoTask }}</span>
          <span class="focus-session-dur">{{ fmtDuration(s.durationSec) }}</span>
          <span class="focus-session-badge">{{ s.status === "completed" ? "🍅" : C.focusPartial }}</span>
        </div>
      </div>
    </section>
  </div>
</template>
