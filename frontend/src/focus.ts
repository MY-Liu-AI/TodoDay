/* 番茄专注的状态与计时器。
 *
 * 计时在本地跑（setInterval 每秒 tick，但剩余时间按「结束时刻 - 现在」算，
 * 后台标签页节流也不会漂），结束或放弃时才把这一次写到服务端。
 * 服务端是统计的唯一来源：今日番茄数、每日/每周/每月专注时长都从
 * /focus/stats 读，前端不自己累加。写失败时如实提示并允许重试。 */
import { reactive } from "vue";
import { api } from "./api";
import { C } from "./copy";
import { st, notify, registerLogoutHook } from "./store";
import type { Item } from "./store";

export const POMODORO_SEC = 25 * 60;

export interface FocusSession {
  id: number;
  taskId: string;
  taskTitle: string;
  plannedSec: number;
  durationSec: number;
  status: "completed" | "abandoned";
  startedAt: number;
  endedAt: number;
}
export interface FocusDay {
  pomodoros: number;
  seconds: number;
}
export interface FocusStats {
  timezone: string;
  today: string;
  todayAgg: FocusDay;
  week: FocusDay;
  month: FocusDay;
  daily: Record<string, FocusDay>;
  sessions: FocusSession[];
}

const EMPTY: FocusDay = { pomodoros: 0, seconds: 0 };

export const fs = reactive({
  /* 计时器 */
  phase: "idle" as "idle" | "running" | "paused" | "done",
  task: null as Item | null,
  remaining: POMODORO_SEC, // 秒
  startedAt: 0, // ms，本次专注开始（含暂停前）
  endsAt: 0, // ms，运行中预计结束时刻
  elapsedBeforePause: 0, // 秒，暂停前已累计
  /* 统计（服务端） */
  stats: null as FocusStats | null,
  loading: false,
  loadError: "",
  /* 失败待重试的记录 */
  pending: null as null | {
    body: Record<string, unknown>;
    label: string;
  },
  saving: false,
});

let timer = 0;

function stopTimer() {
  clearInterval(timer);
  timer = 0;
}

function tick() {
  if (fs.phase !== "running") return;
  const left = Math.max(0, Math.round((fs.endsAt - Date.now()) / 1000));
  fs.remaining = left;
  if (left <= 0) finish();
}

export function pick(task: Item | null) {
  if (fs.phase === "running" || fs.phase === "paused") return;
  fs.task = task;
}

export function start() {
  if (fs.phase === "running") return;
  const now = Date.now();
  if (fs.phase === "paused") {
    fs.endsAt = now + fs.remaining * 1000;
  } else {
    fs.remaining = POMODORO_SEC;
    fs.startedAt = now;
    fs.elapsedBeforePause = 0;
    fs.endsAt = now + POMODORO_SEC * 1000;
  }
  fs.phase = "running";
  stopTimer();
  timer = window.setInterval(tick, 1000);
  tick();
}

export function pause() {
  if (fs.phase !== "running") return;
  stopTimer();
  fs.elapsedBeforePause = POMODORO_SEC - fs.remaining;
  fs.phase = "paused";
}

/* 走完 25 分钟：记一个完整番茄。 */
async function finish() {
  stopTimer();
  fs.phase = "done";
  fs.remaining = 0;
  await record("completed", POMODORO_SEC);
}

/* 中途放弃：不算番茄，但已专注的时间仍计入时长（≥1 分钟才记，太短没意义）。 */
export async function abandon() {
  if (fs.phase !== "running" && fs.phase !== "paused") return;
  stopTimer();
  const elapsed = POMODORO_SEC - fs.remaining;
  fs.phase = "idle";
  fs.remaining = POMODORO_SEC;
  if (elapsed >= 60) await record("abandoned", elapsed);
  else notify(C.focusTooShort);
}

export function reset() {
  stopTimer();
  fs.phase = "idle";
  fs.remaining = POMODORO_SEC;
  fs.elapsedBeforePause = 0;
}

async function record(status: "completed" | "abandoned", durationSec: number) {
  const ended = Date.now();
  const body = {
    taskId: fs.task?.id && !fs.task.id.startsWith("tmp_") ? fs.task.id : "",
    plannedSec: POMODORO_SEC,
    durationSec,
    status,
    startedAt: fs.startedAt || ended - durationSec * 1000,
    endedAt: ended,
    clientId: "f_" + Math.random().toString(36).slice(2) + ended.toString(36),
  };
  await submit(body, status === "completed" ? C.focusDone : C.focusAbandoned);
}

async function submit(body: Record<string, unknown>, label: string) {
  if (!st.workspace) return;
  fs.saving = true;
  try {
    await api(`/api/workspaces/${st.workspace.id}/focus/sessions`, { method: "POST", body });
    fs.pending = null;
    notify(label);
    await loadStats(true);
  } catch {
    // 记录没写进服务端，不能当作已统计：留着重试，提示用户。
    fs.pending = { body, label };
    notify(C.focusSaveFailed, "error");
  } finally {
    fs.saving = false;
  }
}

export function retryPending() {
  if (!fs.pending || fs.saving) return;
  return submit(fs.pending.body, fs.pending.label);
}

export async function loadStats(force = false) {
  if (!st.workspace || (fs.stats && !force)) return;
  fs.loading = true;
  fs.loadError = "";
  try {
    fs.stats = await api<FocusStats>(`/api/workspaces/${st.workspace.id}/focus/stats?days=182`);
  } catch {
    fs.loadError = C.loadError;
  } finally {
    fs.loading = false;
  }
}

export function clearStats() {
  fs.stats = null;
  fs.pending = null;
  reset();
}
registerLogoutHook(clearStats);

export const todayAgg = () => fs.stats?.todayAgg ?? EMPTY;
export const weekAgg = () => fs.stats?.week ?? EMPTY;
export const monthAgg = () => fs.stats?.month ?? EMPTY;

/* 秒 → "1 小时 25 分" / "25 分" / "0 分"，给统计读数用。 */
export function fmtDuration(sec: number): string {
  const m = Math.round(sec / 60);
  if (m < 60) return `${m} ${C.minutes}`;
  const h = Math.floor(m / 60);
  const r = m % 60;
  return r ? `${h} ${C.hours} ${r} ${C.minutes}` : `${h} ${C.hours}`;
}

/* 秒 → "24:59"，倒计时用。 */
export function fmtClock(sec: number): string {
  const m = Math.floor(sec / 60);
  const s = sec % 60;
  return `${String(m).padStart(2, "0")}:${String(s).padStart(2, "0")}`;
}
