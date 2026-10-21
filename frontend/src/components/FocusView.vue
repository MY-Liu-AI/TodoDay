<script setup lang="ts">
/* 番茄专注页：左边挑今天的事 + 25 分钟倒计时；右边今日/本周/本月读数、
   近 4 周每日柱图、今天的专注记录。所有统计读自服务端。 */
import { computed, onMounted, ref } from "vue";
import { C } from "../copy";
import { DAY, TODAY, midnight } from "../lib/todo.js";
import { dateValue } from "../lib/model.js";
import { sound, haptic } from "../lib/fx";
import { st } from "../store";
import { invokeSkill } from "../api";
import type { Item } from "../store";
import {
  fs,
  POMODORO_SEC,
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
import FocusTaskRow from "./FocusTaskRow.vue";

/* 今天要处理的事 = 今天计划的（含已完成）+ 今天之前未完成的（逾期）。
   排序：逾期未完成最前，其次今天未完成（按优先级），已完成垫底。 */
function bucket(i: Item): number {
  if (i.done) return 2;
  return midnight(i.due!) < TODAY ? 0 : 1;
}
const baseTasks = computed(() =>
  st.items
    .filter((i) => i.due && (midnight(i.due) === TODAY || (midnight(i.due) < TODAY && !i.done)))
    .sort((a, b) => bucket(a) - bucket(b) || a.p - b.p),
);

/* ── 手动排序（视图层，仅本次会话）──────────────────────────
   与「我的目标」卡片同款交互：行尾悬停浮现上下小圆键，与相邻行交换。
   只改本页展示顺序，不写服务端——任务的权威排序仍在清单页。 */
const manualOrder = ref<string[]>([]);
const todayTasks = computed(() => {
  const base = baseTasks.value;
  if (!manualOrder.value.length) return base;
  const rank = new Map(manualOrder.value.map((id, idx) => [id, idx]));
  return [...base].sort(
    (a, b) => (rank.get(a.id) ?? Number.MAX_SAFE_INTEGER) - (rank.get(b.id) ?? Number.MAX_SAFE_INTEGER),
  );
});
function moveTask(item: Item, dir: -1 | 1) {
  const ids = todayTasks.value.map((t) => t.id);
  const idx = ids.indexOf(item.id);
  const j = idx + dir;
  if (idx < 0 || j < 0 || j >= ids.length) return;
  [ids[idx], ids[j]] = [ids[j], ids[idx]];
  manualOrder.value = ids;
}

/* ── AI 四象限标签 ──────────────────────────────────────────
   点「AI 划分」调用服务端 quadrant 技能：服务端按同一口径取今日任务、
   模型分类、服务端校验（id 必须在任务集合内）。产出是一次性建议，
   不落库、不改任务。结果（含每项的归类理由）存在 quadrant 模块里，
   跨页面切换保留到刷新。 */
import { quadItems, quadLoading, quadError, applyQuadResult } from "../quadrant";

async function runQuadrant() {
  if (quadLoading.value || !todayTasks.value.length) return;
  quadLoading.value = true;
  quadError.value = "";
  try {
    const out = await invokeSkill(st.workspace!.id, "quadrant");
    applyQuadResult(out.result.items);
  } catch (e) {
    // 如实呈现失败（未配置/配额/模型错误），不编造一份假划分。
    quadError.value = e instanceof Error ? e.message : String(e);
  } finally {
    quadLoading.value = false;
  }
}
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

/* 折线图。viewBox 固定 280×90，靠 preserveAspectRatio 等比缩放，
   这样圆点不会被拉成椭圆。上下各留一点内边距，免得 0 值和峰值贴着边框。 */
const TREND_W = 280;
const TREND_H = 90;
const TREND_PAD = 10;
const trendPoints = computed(() => {
  const bars = dailyBars.value;
  const max = barMax.value;
  const span = Math.max(1, bars.length - 1);
  return bars.map((b, i) => ({
    ...b,
    x: (i / span) * TREND_W,
    y: TREND_H - TREND_PAD - (b.sec / max) * (TREND_H - TREND_PAD * 2),
  }));
});
const trendLine = computed(() => trendPoints.value.map((p) => `${p.x.toFixed(1)},${p.y.toFixed(1)}`).join(" "));
const trendArea = computed(() => {
  const pts = trendPoints.value;
  if (!pts.length) return "";
  return `${pts[0].x},${TREND_H} ${trendLine.value} ${pts[pts.length - 1].x},${TREND_H}`;
});

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
          <button
            class="text-button focus-quad-toggle"
            :disabled="quadLoading || !todayTasks.length"
            @click="runQuadrant"
          >
            {{ quadLoading ? C.focusQuadrantRunning : C.focusQuadrantRun }}
          </button>
        </div>
        <p v-if="quadError" role="alert" class="storage-warning">
          {{ C.focusQuadrantFailed }}：{{ quadError }}
          <button class="text-button" @click="runQuadrant">{{ C.retry }}</button>
        </p>
        <div v-if="!todayTasks.length" class="agenda-empty">{{ C.focusEmpty }}</div>
        <div v-else class="focus-task-list">
          <FocusTaskRow
            v-for="(i, idx) in todayTasks"
            :key="i.id"
            :i="i"
            :quad="quadItems[i.id] || null"
            :can-up="idx > 0"
            :can-down="idx < todayTasks.length - 1"
            @move="(dir) => moveTask(i, dir)"
          />
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
      <div class="focus-trend">
        <svg
          :viewBox="`0 0 ${TREND_W} ${TREND_H}`"
          preserveAspectRatio="xMidYMid meet"
          role="img"
          :aria-label="C.focusDailyTitle"
        >
          <polygon class="area" :points="trendArea" />
          <polyline class="line" :points="trendLine" />
          <circle
            v-for="p in trendPoints"
            :key="p.key"
            class="pt"
            :class="p.ts === TODAY ? 'is-on' : ''"
            :cx="p.x"
            :cy="p.y"
            r="1.6"
          >
            <title>{{ `${p.key} · ${fmtDuration(p.sec)} · ${p.n} 🍅` }}</title>
          </circle>
        </svg>
        <div class="focus-trend-axis" aria-hidden="true">
          <span v-for="p in trendPoints" :key="p.key" class="tick">
            <template v-if="p.ts === TODAY">{{ C.today }}</template>
            <template v-else-if="new Date(p.ts).getDay() === 1">{{ new Date(p.ts).getDate() }}</template>
          </span>
        </div>
      </div>

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
