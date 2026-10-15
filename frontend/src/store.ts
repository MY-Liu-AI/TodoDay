/* 全局共享状态与全部服务端写操作。
 *
 * 每个操作都是「乐观更新 → 请求 → 提交或回滚」：
 *  - 服务端成功：用服务端返回的权威任务替换本地项（id、version 对齐）；
 *  - 409 冲突：采用服务端版本并提示，绝不默默覆盖其他设备的新修改；
 *  - 断网/超时/5xx：回滚本地改动并如实提示，不留下“已保存”的假象。
 */
import { reactive } from "vue";
import { api, ApiError, type WireTask } from "./api";
import { C } from "./copy";
import { reorder as reorderList, dateValue, validState } from "./lib/model.js";

export interface Item {
  id: string;
  t: string;
  p: number;
  due: number; // 本地午夜色时间戳；0 = 未排期
  g: string;
  done: boolean;
  at: number;
  version: number;
}

export function toItem(w: WireTask): Item {
  return {
    id: w.id,
    t: w.t,
    p: w.p,
    due: w.due ? +new Date(w.due + "T00:00:00") : 0,
    g: w.g,
    done: w.done,
    at: w.at || 0,
    version: w.version,
  };
}

export interface UserInfo {
  id: number;
  username: string;
}
export interface WorkspaceInfo {
  id: number;
  name: string;
  timezone: string;
  role?: string;
}

const SORT_KEY = "todo-day-sort";
const LEGACY_IMPORTED_KEY = "todo-day-imported";

function loadSort(): "manual" | "due" | "pri" | "group" {
  try {
    const v = localStorage.getItem(SORT_KEY);
    return v === "due" || v === "pri" || v === "group" ? v : "manual";
  } catch {
    return "manual";
  }
}

export const st = reactive({
  ready: false, // 首次会话检查是否完成（决定是否显示登录页）
  authed: false,
  user: null as UserInfo | null,
  workspace: null as WorkspaceInfo | null,
  items: [] as Item[],
  sort: loadSort(),
  loading: true,
  loadError: "",
  notice: "",
  noticeKind: "ok" as "ok" | "error",
  deleted: null as Item[] | null,
  online: true,
  syncing: 0,
  sessionNotice: "",
  // 统计页与热力/近7天/连续天数同源：服务端 task_events 按工作区时区聚合。
  activity: {} as Record<string, number>,
  statsLoaded: false,
  // 本地旧数据导入（必须用户明确同意，绝不静默上传）。
  importPrompt: null as { items: unknown[]; count: number } | null,
});

let noticeTimer = 0;
export function notify(msg: string, kind: "ok" | "error" = "ok") {
  st.notice = msg;
  st.noticeKind = kind;
  clearTimeout(noticeTimer);
  noticeTimer = window.setTimeout(() => {
    st.notice = "";
  }, 4200);
}

function wsPath(p: string): string {
  return `/api/workspaces/${st.workspace!.id}${p}`;
}

function replaceItem(task: WireTask) {
  const item = toItem(task);
  const idx = st.items.findIndex((i) => i.id === item.id);
  if (idx >= 0) st.items.splice(idx, 1, item);
  else st.items.unshift(item);
}

function removeLocal(ids: string[]) {
  st.items = st.items.filter((i) => !ids.includes(i.id));
}

async function reload() {
  if (!st.workspace) return;
  try {
    const data = await api<{ tasks: WireTask[] }>(wsPath("/tasks?pageSize=500"));
    st.items = data.tasks.map(toItem);
    st.online = true;
  } catch {
    /* 保留现有内容 */
  }
}

export async function loadTasks() {
  if (!st.workspace) return;
  st.loading = true;
  st.loadError = "";
  try {
    const data = await api<{ tasks: WireTask[] }>(wsPath("/tasks?pageSize=500"));
    st.items = data.tasks.map(toItem);
    st.online = true;
  } catch (e) {
    st.loadError = e instanceof ApiError && e.status === 0 ? C.loadError : C.loadError;
  } finally {
    st.loading = false;
  }
}

export function retryLoad() {
  return loadTasks();
}

/* ── 会话 ─────────────────────────────────────────────────────────────── */

export async function initSession() {
  try {
    const me = await api<{ user: UserInfo; workspaces: WorkspaceInfo[] }>("/api/me");
    st.user = me.user;
    st.workspace = me.workspaces[0] || null;
    st.authed = true;
    await loadTasks();
    offerLegacyImport();
  } catch (e) {
    st.authed = false;
    if (e instanceof ApiError && e.status !== 401 && e.status !== 0) {
      st.sessionNotice = C.loadError;
    }
  } finally {
    st.ready = true;
  }
}

export async function login(username: string, password: string) {
  await api("/api/auth/login", { method: "POST", body: { username, password } });
  await afterAuth();
}

export async function register(username: string, password: string) {
  await api("/api/auth/register", { method: "POST", body: { username, password } });
  await afterAuth();
}

async function afterAuth() {
  const me = await api<{ user: UserInfo; workspaces: WorkspaceInfo[] }>("/api/me");
  st.user = me.user;
  st.workspace = me.workspaces[0] || null;
  st.authed = true;
  st.statsLoaded = false;
  await loadTasks();
  offerLegacyImport();
}

export async function logout() {
  try {
    await api("/api/auth/logout", { method: "POST", body: {} });
  } finally {
    st.authed = false;
    st.user = null;
    st.workspace = null;
    st.items = [];
    st.deleted = null;
    st.activity = {};
    st.statsLoaded = false;
    onLogout.forEach((fn) => fn());
  }
}

/* 其他模块（如番茄专注）在登出时需要清自己的状态；避免循环 import，用回调注册。 */
const onLogout: (() => void)[] = [];
export function registerLogoutHook(fn: () => void) {
  onLogout.push(fn);
}

function onAuthLost(e: unknown): boolean {
  if (e instanceof ApiError && e.status === 401) {
    st.authed = false;
    st.sessionNotice = C.sessionExpired;
    return true;
  }
  return false;
}

/* ── 任务写操作 ───────────────────────────────────────────────────────── */

export async function addTask(input: { t: string; p: number; due: number; g: string }) {
  const clientId = "c_" + Math.random().toString(36).slice(2) + Date.now().toString(36);
  const temp: Item = {
    id: "tmp_" + clientId,
    t: input.t,
    p: input.p,
    due: input.due,
    g: input.g,
    done: false,
    at: 0,
    version: 0,
  };
  st.items.unshift(temp);
  try {
    const task = await api<WireTask>(wsPath("/tasks"), {
      method: "POST",
      body: { t: input.t, p: input.p, due: input.due ? dateValue(input.due) : "", g: input.g, clientId },
    });
    const idx = st.items.findIndex((i) => i.id === temp.id);
    if (idx >= 0) st.items.splice(idx, 1, toItem(task));
    notify(C.added);
  } catch (e) {
    removeLocal([temp.id]);
    fail(e);
    return false;
  }
  return true;
}

export async function saveTask(draft: Item) {
  // 对话框新建的项 id 为空：必须走创建接口（POST /tasks），
  // 否则 PATCH /tasks/ 会因空 id 404。
  if (!draft.id) {
    const clientId = "c_" + Math.random().toString(36).slice(2) + Date.now().toString(36);
    const temp: Item = { ...draft, id: "tmp_" + clientId };
    st.items.unshift(temp);
    try {
      const task = await api<WireTask>(wsPath("/tasks"), {
        method: "POST",
        body: {
          t: draft.t,
          p: draft.p,
          due: draft.due ? dateValue(draft.due) : "",
          g: draft.g,
          clientId,
        },
      });
      const idx = st.items.findIndex((i) => i.id === temp.id);
      if (idx >= 0) st.items.splice(idx, 1, toItem(task));
      notify(C.added);
      return true;
    } catch (e) {
      removeLocal([temp.id]);
      fail(e);
      return false;
    }
  }
  const prev = st.items.find((i) => i.id === draft.id);
  if (prev) {
    rememberSnapshot(prev);
    Object.assign(prev, draft);
  } else {
    st.items.unshift(draft);
  }
  try {
    const task = await api<WireTask>(wsPath(`/tasks/${draft.id}`), {
      method: "PATCH",
      body: {
        t: draft.t,
        p: draft.p,
        due: draft.due ? dateValue(draft.due) : "",
        g: draft.g,
        version: draft.version,
      },
    });
    replaceItem(task);
    notify(C.saved);
    if (st.statsLoaded) refreshStats();
  } catch (e) {
    if (handleConflict(e)) return true;
    if (prev) Object.assign(prev, prevSnapshot(draft.id));
    else removeLocal([draft.id]);
    fail(e);
    return false;
  }
  return true;
}

const snapshots = new Map<string, Item>();
function prevSnapshot(id: string): Item {
  return snapshots.get(id) || { id, t: "", p: 3, due: 0, g: C.personal, done: false, at: 0, version: 0 };
}
export function rememberSnapshot(item: Item) {
  snapshots.set(item.id, { ...item });
}

export async function toggleTask(item: Item) {
  rememberSnapshot(item);
  const next = !item.done;
  item.done = next;
  item.at = next ? Date.now() : 0;
  try {
    const task = await api<WireTask>(wsPath(`/tasks/${item.id}/completion`), {
      method: "POST",
      body: { done: next, version: item.version },
    });
    replaceItem(task);
    notify(next ? C.completed : C.reopen);
    if (st.statsLoaded) refreshStats();
  } catch (e) {
    const snap = prevSnapshot(item.id);
    Object.assign(item, snap);
    if (handleConflict(e)) return;
    fail(e);
  }
}

export async function removeTask(item: Item) {
  st.deleted = [item];
  rememberSnapshot(item);
  removeLocal([item.id]);
  try {
    await api(wsPath("/tasks/delete"), { method: "POST", body: { ids: [item.id] } });
    notify(C.deleted);
  } catch (e) {
    restoreLocal([item]);
    fail(e);
  }
}

export async function clearDone() {
  const done = st.items.filter((i) => i.done);
  if (!done.length) {
    notify(C.nothingDone);
    return;
  }
  st.deleted = done;
  const ids = done.map((i) => i.id);
  removeLocal(ids);
  try {
    await api(wsPath("/tasks/delete"), { method: "POST", body: { ids } });
    notify(C.deleted + " · " + done.length);
  } catch (e) {
    restoreLocal(done);
    fail(e);
  }
}

export async function restoreDeleted() {
  const batch = st.deleted;
  if (!batch) return;
  st.deleted = null;
  restoreLocal(batch);
  try {
    const data = await api<{ tasks: WireTask[] }>(wsPath("/tasks/restore"), {
      method: "POST",
      body: { ids: batch.map((i) => i.id) },
    });
    for (const t of data.tasks) replaceItem(t);
    notify(C.restored);
  } catch (e) {
    removeLocal(batch.map((i) => i.id));
    fail(e);
  }
}

export function dismissDeleted() {
  st.deleted = null;
  st.notice = "";
}

function restoreLocal(items: Item[]) {
  const existing = new Set(st.items.map((i) => i.id));
  st.items = [...items.filter((i) => !existing.has(i.id)), ...st.items];
}

export async function reorderTasks(fromId: string, toId: string) {
  const next = reorderList(st.items, fromId, toId);
  if (next === st.items) return;
  const before = st.items;
  st.items = next;
  try {
    await api(wsPath("/tasks/reorder"), {
      method: "POST",
      body: { ids: next.map((i: Item) => i.id) },
    });
    notify(C.moved);
  } catch (e) {
    st.items = before;
    fail(e);
    reload();
  }
}

/* ── 统计 ─────────────────────────────────────────────────────────────── */

export async function refreshStats(force = false) {
  if (!st.workspace || (st.statsLoaded && !force)) return;
  try {
    const data = await api<{ counts: Record<string, number> }>(wsPath("/activity?weeks=18"));
    st.activity = data.counts;
    st.statsLoaded = true;
  } catch (e) {
    if (!onAuthLost(e)) fail(e);
  }
}

/* ── 旧本地数据导入（用户明确同意后才会调用） ─────────────────────────── */

function offerLegacyImport() {
  try {
    if (localStorage.getItem(LEGACY_IMPORTED_KEY)) return;
    const raw = localStorage.getItem("todo-day-demo-cn") || localStorage.getItem("todo-day");
    if (!raw) return;
    const data = JSON.parse(raw);
    if (!validState(data) || !data.items.length || st.items.length) return;
    st.importPrompt = { items: data.items, count: data.items.length };
  } catch {
    /* 损坏的旧数据不处理、不覆盖 */
  }
}

export async function confirmImport() {
  const prompt = st.importPrompt;
  if (!prompt) return;
  const items = (prompt.items as any[])
    .filter((i) => typeof i?.t === "string" && i.t.trim())
    .slice(0, 500)
    .map((i) => ({
      t: String(i.t).trim(),
      p: [1, 2, 3].includes(i.p) ? i.p : 3,
      due: i.due ? dateValue(i.due) : "",
      g: typeof i.g === "string" && i.g.trim() ? String(i.g).trim() : C.personal,
      done: !!i.done,
      at: Number.isFinite(i.at) ? i.at : 0,
    }));
  try {
    const out = await api<{ imported: number; skipped: number }>(wsPath("/tasks/import"), {
      method: "POST",
      body: { items },
    });
    st.importPrompt = null;
    localStorage.setItem(LEGACY_IMPORTED_KEY, String(Date.now()));
    notify(C.imported.replace("{n}", String(out.imported)) + (out.skipped ? C.importDup.replace("{n}", String(out.skipped)) : ""));
    await loadTasks();
    st.statsLoaded = false;
  } catch (e) {
    // 导入失败：旧 localStorage 原样保留，不“清缓存掩盖失败”。
    st.importPrompt = null;
    fail(e, C.importFailed);
  }
}

export function skipImport() {
  st.importPrompt = null;
  try {
    localStorage.setItem(LEGACY_IMPORTED_KEY, "skipped");
  } catch {
    /* ignore */
  }
}

/* ── 共享的错误处理 ───────────────────────────────────────────────────── */

function handleConflict(e: unknown): boolean {
  if (e instanceof ApiError && e.status === 409 && e.task) {
    replaceItem(e.task);
    notify(C.conflict, "error");
    return true;
  }
  return false;
}

function fail(e: unknown, fallback?: string) {
  if (onAuthLost(e)) return;
  if (e instanceof ApiError) {
    if (e.network) {
      st.online = false;
      notify(fallback || C.offline, "error");
      return;
    }
    notify(fallback || `${C.syncError}（${e.message}）`, "error");
    return;
  }
  notify(fallback || C.syncError, "error");
}

export function setSort(v: "manual" | "due" | "pri" | "group") {
  st.sort = v;
  try {
    localStorage.setItem(SORT_KEY, v);
  } catch {
    /* UI 偏好而已 */
  }
}
