/* 番茄专注的状态与计时器。
 *
 * 计时在本地跑（setInterval 每秒 tick，但剩余时间按「结束时刻 - 现在」算，
 * 后台标签页节流也不会漂），结束或放弃时才把这一次写到服务端。
 * 进行中的计时会持久化到 localStorage：刷新页面、切到别的模块、甚至关掉
 * 标签页再回来，倒计时都按最初定下的结束时刻继续走——恢复时如果已经过点，
 * 就补记这一颗番茄（25 分钟确实跑完了，归属时间仍是当初的结束时刻）。
 * 服务端是统计的唯一来源：今日番茄数、每日/每周/每月专注时长都从
 * /focus/stats 读，前端不自己累加。写失败时如实提示并允许重试。 */
import { reactive } from "vue";
import { api } from "./api";
import { C } from "./copy";
import { st, notify, registerLogoutHook } from "./store";
import type { Item } from "./store";
import { sound, haptic } from "./lib/fx";

export const POMODORO_SEC = 25 * 60;

/* 计时进行中的快照。key 里带工作区 id：换账号不会捡到别人的倒计时。 */
const STORE_KEY = "todo-day-focus";

interface Persisted {
  ws: number;
  taskId: string;
  phase: "running" | "paused";
  startedAt: number;
  endsAt: number;
  remaining: number;
  elapsedBeforePause: number;
}

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

/* ── 持久化 ─────────────────────────────────────────────────────────────── */

function persist() {
  try {
    if (!st.workspace || (fs.phase !== "running" && fs.phase !== "paused")) {
      localStorage.removeItem(STORE_KEY);
      return;
    }
    const snap: Persisted = {
      ws: st.workspace.id,
      taskId: fs.task?.id || "",
      phase: fs.phase,
      startedAt: fs.startedAt,
      endsAt: fs.endsAt,
      remaining: fs.remaining,
      elapsedBeforePause: fs.elapsedBeforePause,
    };
    localStorage.setItem(STORE_KEY, JSON.stringify(snap));
  } catch {
    /* 存不下不影响计时本身 */
  }
}

function clearPersisted() {
  try {
    localStorage.removeItem(STORE_KEY);
  } catch {
    /* ignore */
  }
}

/* 应用启动时调用：把上次没走完的倒计时接上。幂等，可以在多处安全调用。 */
export function restore() {
  if (fs.phase === "running" || fs.phase === "paused" || fs.phase === "done") return;
  if (!st.workspace) return;
  let raw: string | null = null;
  try {
    raw = localStorage.getItem(STORE_KEY);
  } catch {
    return;
  }
  if (!raw) return;
  let p: Persisted;
  try {
    p = JSON.parse(raw);
  } catch {
    clearPersisted();
    return;
  }
  if (!p || p.ws !== st.workspace.id || (p.phase !== "running" && p.phase !== "paused")) {
    clearPersisted();
    return;
  }
  fs.task = st.items.find((i) => i.id === p.taskId) || null;
  fs.startedAt = p.startedAt || Date.now();
  fs.elapsedBeforePause = p.elapsedBeforePause || 0;

  if (p.phase === "paused" && p.remaining > 0) {
    fs.remaining = p.remaining;
    fs.phase = "paused";
    notify(C.focusRestored);
    return;
  }
  const left = Math.round((p.endsAt - Date.now()) / 1000);
  if (left > 0) {
    fs.endsAt = p.endsAt;
    fs.remaining = left;
    fs.phase = "running";
    stopTimer();
    timer = window.setInterval(tick, 1000);
    notify(C.focusRestored);
    return;
  }
  // 离开的这段时间里倒计时已经走完：补记这一颗，归属当初的结束时刻。
  clearPersisted();
  fs.remaining = 0;
  fs.phase = "done";
  sound.ding();
  haptic(30);
  notify(C.focusCaughtUp);
  void record("completed", POMODORO_SEC, p.endsAt);
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
  persist();
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
  persist();
}

export function pause() {
  if (fs.phase !== "running") return;
  stopTimer();
  fs.elapsedBeforePause = POMODORO_SEC - fs.remaining;
  fs.phase = "paused";
  persist();
}

/* 走完 25 分钟：记一个完整番茄。铃响在这里而不是页面里 —— 用户可能已经切到
   别的模块，FocusView 不在挂载状态，页面级的监听就不会触发。 */
async function finish() {
  stopTimer();
  fs.phase = "done";
  fs.remaining = 0;
  clearPersisted();
  sound.ding();
  haptic(30);
  await record("completed", POMODORO_SEC);
}

/* 中途放弃：不算番茄，但已专注的时间仍计入时长（≥1 分钟才记，太短没意义）。 */
export async function abandon() {
  if (fs.phase !== "running" && fs.phase !== "paused") return;
  stopTimer();
  const elapsed = POMODORO_SEC - fs.remaining;
  fs.phase = "idle";
  fs.remaining = POMODORO_SEC;
  clearPersisted();
  if (elapsed >= 60) await record("abandoned", elapsed);
  else notify(C.focusTooShort);
}

export function reset() {
  stopTimer();
  fs.phase = "idle";
  fs.remaining = POMODORO_SEC;
  fs.elapsedBeforePause = 0;
  clearPersisted();
}

/* endedAt 可传入：补记离线期间走完的那一颗时，归属时间仍是当初的结束时刻。 */
async function record(status: "completed" | "abandoned", durationSec: number, endedAt?: number) {
  const ended = endedAt || Date.now();
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
