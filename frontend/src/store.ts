/* 全局共享状态与全部服务端写操作。
 *
 * 每个操作都是「乐观更新 → 请求 → 提交或回滚」：
 *  - 服务端成功：用服务端返回的权威任务替换本地项（id、version 对齐）；
 *  - 409 冲突：采用服务端版本并提示，绝不默默覆盖其他设备的新修改；
 *  - 断网/超时/5xx：回滚本地改动并如实提示，不留下“已保存”的假象。
 */
import { reactive } from "vue";
import { api, ApiError, type WireGoal, type WireGoalMilestone, type WireHabit, type WireLedgerMonth, type WireTask, type WireTx } from "./api";
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

/* 助手配置的掩码视图。服务端只回「是否已设置」，永不回传密钥本身，
   所以这里没有任何存密钥的字段 —— 前端也就无从泄漏。 */
export interface AssistantConfigView {
  configured: boolean;
  enabled: boolean;
  /* 内嵌运行状态：此刻是否有一条连接在跑。与 enabled（持久化意图）分开。 */
  running: boolean;
  botId: string;
  botSecretSet: boolean;
  kimiApiKeySet: boolean;
  kimiBase: string;
  kimiModel: string;
  version: number;
  encryptionAvailable: boolean;
}

/* 保存时的入参。密钥字段留空/省略表示「保留原值」，
   这样前端不需要（也不应该）回显已存的密钥。
   注意没有 enabled：启用/停用由 start/stop 端点控制，保存配置不改开关。 */
export interface AssistantConfigInput {
  botId: string;
  botSecret?: string;
  kimiApiKey?: string;
  kimiBase: string;
  kimiModel: string;
  version: number;
  /* 显式撤销。留空只表示「不修改」，所以要删掉已存密钥必须明说。 */
  clearBotSecret?: boolean;
  clearKimiApiKey?: boolean;
}

/* 测试连接的结果。服务端做一次真实订阅，失败是正常结果（HTTP 仍是 200）。 */
export interface AssistantTestResult {
  ok: boolean;
  errcode?: number;
  errmsg?: string;
  message?: string;
  kimiConfigured: boolean;
}

/* LLM（Kimi）测试连接的结果。成功 ok=true；失败 ok=false 且 message 带原因。 */
export interface AssistantLlmTestResult {
  ok: boolean;
  message?: string;
}

/* ── 邮箱账单导入（原生 IMAP：邮箱地址 + 授权码） ─────────────────────────
 * 视图里没有任何凭据字段：授权码由服务端以 AES-256-GCM 密文落库，
 * 前端只是写入通道，服务端只回「是否已设置」掩码（authCodeSet），
 * 与助手密钥同一套规则。 */
export interface MailConfigView {
  configured: boolean;
  enabled: boolean;
  running: boolean;
  mailbox: string;
  authCodeSet: boolean;
  /* 账单来源发件人清单（| 分隔）；空 = 未配置，窗口内全部交给 Kimi 判定。 */
  senders: string;
  lastRunAt: string;
  lastRunOk: boolean | null;
  lastRunError: string;
  lastImported: number;
  version: number;
}

/* 保存入参：授权码留空 = 不修改，删除必须显式 clearAuthCode。
 * senders 每次提交完整清单、整体写入。 */
export interface MailConfigInput {
  mailbox?: string;
  authCode?: string;
  clearAuthCode?: boolean;
  senders: string;
  version: number;
}

/* 「从邮箱发现」返回的一个发件来源：count/sample（最近主题）帮助辨认
 * 这是账单来源还是营销来源。 */
export interface DiscoveredSender {
  from: string;
  count: number;
  sample: string;
}

/* 目标：任务的串联层。进度不落库——由挂载任务的完成情况推导，
   这里是唯一一份目标状态，列表/详情都从这里读。目标没有截止日期，
   排期在挂载任务的 due 上。 */
export interface GoalInput {
  title: string;
  goalText: string; // 目标决策·我的目标
  acceptance: string; // 目标决策·验收标准
  failureCost: string; // 目标决策·失败代价
  group: string; // 分组标签，"" = 未分组
  status: "active" | "done" | "archived";
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
  // 助手配置的掩码视图。密钥不在其中，故也不必担心持久化泄漏。
  assistant: null as AssistantConfigView | null,
  assistantLoading: false,
  // 目标列表（含每个目标挂载的任务视图）。
  goals: [] as WireGoal[],
  goalsLoaded: false,
  // 最近一次软删的目标，供全局 toast 撤销。与任务共用同一交互。
  deletedGoal: null as WireGoal | null,
  // 最近一次删除的里程碑（软删，供全局 toast 撤销）。删除时其下任务解除
  // 挂载、撤销不自动回填任务归属。
  deletedMilestone: null as { goalId: number; milestone: WireGoalMilestone } | null,
  // 「新建目标」按钮在 App 页头、对话框在 GoalsView 里：App 只递增这个
  // 信号，GoalsView 监听后打开对话框（计数器避免重置时序问题）。
  goalsNewSeq: 0,
  // 日历点击里程碑标记 → 打开目标详情的信号（GoalsView 监听）。
  goalsOpenId: 0,
  goalsOpenSeq: 0,
  // GoalsView 详情视图激活中：App 据此隐藏页头，详情页自带返回入口。
  goalsViewing: false,
  // 习惯（工作区级的定期练习，独立于目标）。服务端在读取时滚动补齐未来
  // 8 周的实例任务，所以读取这一步同时是「展开引擎」的触发点。
  habits: [] as WireHabit[],
  habitsLoaded: false,
  // 记账本：当前展示月的流水（st.ledgerMonth 是这份数据对应的月份）。
  // 三卡汇总不单独存——由列表推导（lib/ledger.js monthTotals），恒同口径。
  txs: [] as WireTx[],
  ledgerMonth: "",
  ledgerLoaded: false,
  ledgerLoading: false,
  ledgerLoadError: "",
  // 最近一次软删的账目，供全局 toast 撤销（与任务/目标同一交互）。
  deletedTx: null as WireTx | null,
  // 邮箱账单导入的配置视图（同样没有任何凭据字段）。
  mail: null as MailConfigView | null,
  mailLoading: false,
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
    /* 目标数据随会话一起拉：清单/日历/专注页的「目标徽章」都读
       st.goals，以前只有进过目标页才有数据，徽章会先缺后现。 */
    await loadGoals();
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
  await loadGoals();
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

/* 其他模块（如番茄专注、助手配置）在登出时需要清自己的状态；
   避免循环 import，用回调注册。 */
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
    if (st.statsLoaded) refreshStats(true);
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

/* 完成回执：任务挂在某个目标下时，在完成提示里带上目标进度，
   让「打勾」和「目标推进」两件事在用户心里连成一条线。 */
function completionNotice(taskId: string): string {
  const goal = goalOfTask(taskId);
  if (!goal) return C.completed;
  const all = goal.milestones.flatMap((m) => m.tasks);
  const done = all.filter((t) => t.done || t.taskId === taskId).length;
  return (
    C.completed +
    " · " +
    C.goalDoneProgress.replace("{title}", goal.title)
      .replace("{done}", String(done))
      .replace("{total}", String(all.length))
  );
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
    notify(next ? completionNotice(item.id) : C.reopen);
    // 必须 force：statsLoaded 一旦为 true，不强制就会直接 return，热力图
    // 会一直显示打开统计页那一刻的旧数据（勾完看不到今天多了一格）。
    if (st.statsLoaded) refreshStats(true);
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

/* ── 助手配置（企业微信机器人 + AI 凭证） ─────────────────────────────── */

export async function loadAssistantConfig() {
  if (!st.workspace) return;
  st.assistantLoading = true;
  try {
    st.assistant = await api<AssistantConfigView>(wsPath("/assistant"));
  } catch (e) {
    // 读不到就保持 null，让设置页显示「读取失败」并给重试入口。
    st.assistant = null;
    fail(e, C.assistantLoadFailed);
  } finally {
    st.assistantLoading = false;
  }
}

export async function saveAssistantConfig(input: AssistantConfigInput, opts?: { silent?: boolean }) {
  if (!st.workspace) return false;
  st.assistantLoading = true;
  try {
    const view = await api<AssistantConfigView>(wsPath("/assistant"), {
      method: "PUT",
      body: input,
    });
    // 服务端回的是权威值（含递增后的 version），据它刷新本地掩码视图。
    st.assistant = view;
    // 静默模式供「启动助手」复用：启用前顺手保存，不弹「已保存」提示。
    if (!opts?.silent) notify(C.assistantSaved);
    return true;
  } catch (e) {
    if (e instanceof ApiError && e.code === "encryption_unavailable") {
      notify(C.assistantEncryptionMissing, "error");
      return false;
    }
    if (e instanceof ApiError && e.code === "decrypt_failed") {
      notify(C.assistantDecryptFailed, "error");
      return false;
    }
    if (e instanceof ApiError && e.status === 403) {
      notify(C.assistantOwnerOnly, "error");
      return false;
    }
    fail(e, C.assistantSaveFailed);
    return false;
  } finally {
    st.assistantLoading = false;
  }
}

/* 测试机器人凭据能否真连上企业微信。返回结果对象；HTTP 层错误返回 null
   （已通过 notify 提示）。测试失败本身不是错误 —— 那是正常结果。
   input 是「表单里此刻填的值」：先测当前输入，未填才由服务端回退到已保存值。 */
export async function testAssistantConfig(input?: { botId?: string; botSecret?: string }): Promise<AssistantTestResult | null> {
  if (!st.workspace) return null;
  try {
    return await api<AssistantTestResult>(wsPath("/assistant/test"), { method: "POST", body: input ?? {} });
  } catch (e) {
    if (e instanceof ApiError && e.code === "not_configured") {
      notify(C.assistantTestNotConfigured, "error");
      return null;
    }
    if (e instanceof ApiError && e.code === "encryption_unavailable") {
      notify(C.assistantEncryptionMissing, "error");
      return null;
    }
    if (e instanceof ApiError && e.status === 403) {
      notify(C.assistantOwnerOnly, "error");
      return null;
    }
    fail(e, C.assistantTestFailed);
    return null;
  }
}

/* 测试 LLM（Kimi）凭证能否真调通。返回结果对象；HTTP 层错误返回 null
   （已通过 notify 提示）。测试失败本身不是错误 —— 那是正常结果。
   input 是「表单里此刻填的值」：先测当前输入，未填才由服务端回退到已保存值。 */
export async function testAssistantLlm(input?: { kimiApiKey?: string; kimiModel?: string }): Promise<AssistantLlmTestResult | null> {
  if (!st.workspace) return null;
  try {
    return await api<AssistantLlmTestResult>(wsPath("/assistant/test-llm"), { method: "POST", body: input ?? {} });
  } catch (e) {
    if (e instanceof ApiError && e.code === "not_configured") {
      notify(C.assistantLlmTestNotConfigured, "error");
      return null;
    }
    if (e instanceof ApiError && e.code === "encryption_unavailable") {
      notify(C.assistantEncryptionMissing, "error");
      return null;
    }
    if (e instanceof ApiError && e.status === 403) {
      notify(C.assistantOwnerOnly, "error");
      return null;
    }
    fail(e, C.assistantLlmTestFailed);
    return null;
  }
}

/* 启动内嵌助手。成功后重新读取掩码视图，拿到权威的 running/enabled。 */
export async function startAssistant(): Promise<boolean> {
  if (!st.workspace) return false;
  try {
    await api(wsPath("/assistant/start"), { method: "POST" });
    st.assistant = await api<AssistantConfigView>(wsPath("/assistant"));
    notify(C.assistantStarted);
    return true;
  } catch (e) {
    if (e instanceof ApiError && e.code === "not_found") {
      notify(C.assistantTestNotConfigured, "error");
      return false;
    }
    if (e instanceof ApiError && e.code === "not_configured") {
      notify(C.assistantStartNoCredentials, "error");
      return false;
    }
    if (e instanceof ApiError && e.code === "encryption_unavailable") {
      notify(C.assistantEncryptionMissing, "error");
      return false;
    }
    if (e instanceof ApiError && e.status === 403) {
      notify(C.assistantOwnerOnly, "error");
      return false;
    }
    fail(e, C.assistantStartFailed);
    return false;
  }
}

/* 停止内嵌助手。幂等：未启动时调用也返回成功。 */
export async function stopAssistant(): Promise<boolean> {
  if (!st.workspace) return false;
  try {
    await api(wsPath("/assistant/stop"), { method: "POST" });
    st.assistant = await api<AssistantConfigView>(wsPath("/assistant"));
    notify(C.assistantStoppedNotice);
    return true;
  } catch (e) {
    if (e instanceof ApiError && e.status === 403) {
      notify(C.assistantOwnerOnly, "error");
      return false;
    }
    fail(e, C.assistantStopFailed);
    return false;
  }
}

export function clearAssistantConfig() {
  st.assistant = null;
  st.assistantLoading = false;
}
registerLogoutHook(clearAssistantConfig);

/* ── 邮箱账单导入（原生 IMAP） ─────────────────────────────────────────── */

export async function loadMailConfig() {
  if (!st.workspace) return;
  st.mailLoading = true;
  try {
    st.mail = await api<MailConfigView>(wsPath("/mail"));
  } catch (e) {
    st.mail = null;
    fail(e, C.mailLoadFailed);
  } finally {
    st.mailLoading = false;
  }
}

/* 保存邮箱导入配置（邮箱地址 / 授权码 / 关键词）。授权码经服务端加密落库；
   启用状态由 start/stop 控制，保存不改开关。 */
export async function saveMailConfig(input: MailConfigInput, opts?: { silent?: boolean }) {
  if (!st.workspace) return false;
  st.mailLoading = true;
  try {
    const view = await api<MailConfigView>(wsPath("/mail"), { method: "PUT", body: input });
    st.mail = view;
    if (!opts?.silent) notify(C.mailSaved);
    return true;
  } catch (e) {
    if (e instanceof ApiError && e.status === 403) {
      notify(C.mailOwnerOnly, "error");
      return false;
    }
    fail(e, C.mailSaveFailed);
    return false;
  } finally {
    st.mailLoading = false;
  }
}

/* 测试连接 = 一次真实 IMAP 登录。测的是表单当前值（未保存也能验证），
   留空则回退已存凭据。失败是正常结果（HTTP 仍 200），返回 {ok:false, message}。 */
export async function testMailConnection(
  input?: { mailbox: string; authCode: string },
): Promise<{ ok: boolean; mailbox?: string; message?: string } | null> {
  if (!st.workspace) return null;
  try {
    return await api<{ ok: boolean; mailbox?: string; message?: string }>(wsPath("/mail/test"), {
      method: "POST",
      body: input ?? {},
    });
  } catch (e) {
    if (e instanceof ApiError && e.status === 403) {
      notify(C.mailOwnerOnly, "error");
      return null;
    }
    fail(e, C.mailTestFailed);
    return null;
  }
}

/* 从邮箱发现发件人 = 一次只读 IMAP 扫描（不取正文、不标记邮件）。
   吃的是表单当前值（未保存也能用），留空由服务端回退已存凭据。 */
export async function discoverMailSenders(
  input?: { mailbox: string; authCode: string },
): Promise<{ ok: boolean; senders?: DiscoveredSender[]; message?: string } | null> {
  if (!st.workspace) return null;
  try {
    return await api<{ ok: boolean; senders?: DiscoveredSender[]; message?: string }>(wsPath("/mail/discover"), {
      method: "POST",
      body: input ?? {},
    });
  } catch (e) {
    if (e instanceof ApiError && e.status === 403) {
      notify(C.mailOwnerOnly, "error");
      return null;
    }
    fail(e, C.mailSenderDiscoverFailed);
    return null;
  }
}

export async function startMailImport(): Promise<boolean> {
  if (!st.workspace) return false;
  try {
    await api(wsPath("/mail/start"), { method: "POST", body: {} });
    await loadMailConfig();
    notify(C.mailStartedNotice);
    return true;
  } catch (e) {
    if (e instanceof ApiError && e.code === "not_found") {
      notify(C.mailSaveFirst, "error");
      return false;
    }
    if (e instanceof ApiError && e.code === "no_credentials") {
      notify(C.mailNoCredentials, "error");
      return false;
    }
    if (e instanceof ApiError && e.status === 403) {
      notify(C.mailOwnerOnly, "error");
      return false;
    }
    fail(e, C.mailStartFailed);
    return false;
  }
}

export async function stopMailImport(): Promise<boolean> {
  if (!st.workspace) return false;
  try {
    await api(wsPath("/mail/stop"), { method: "POST", body: {} });
    await loadMailConfig();
    notify(C.mailStoppedNotice);
    return true;
  } catch (e) {
    if (e instanceof ApiError && e.status === 403) {
      notify(C.mailOwnerOnly, "error");
      return false;
    }
    fail(e, C.mailStopFailed);
    return false;
  }
}

export function clearMailConfig() {
  st.mail = null;
  st.mailLoading = false;
}
registerLogoutHook(clearMailConfig);

/* ── 目标（任务串联 + 进度跟踪） ───────────────────────────────────────── */

export async function loadGoals(force = false) {
  if (!st.workspace || (st.goalsLoaded && !force)) return;
  try {
    const data = await api<{ goals: WireGoal[] }>(wsPath("/goals"));
    st.goals = data.goals;
    st.goalsLoaded = true;
  } catch (e) {
    if (!onAuthLost(e)) fail(e, C.goalsLoadFailed);
  }
}

function replaceGoal(goal: WireGoal) {
  const idx = st.goals.findIndex((g) => g.id === goal.id);
  if (idx >= 0) st.goals.splice(idx, 1, goal);
  else st.goals.unshift(goal);
}

export async function addGoal(input: GoalInput): Promise<WireGoal | null> {
  if (!st.workspace) return null;
  try {
    const goal = await api<WireGoal>(wsPath("/goals"), { method: "POST", body: input });
    st.goals.unshift(goal);
    notify(C.goalAdded);
    return goal;
  } catch (e) {
    fail(e, C.goalSaveFailed);
    return null;
  }
}

/* 编辑目标（含标记达成/重新打开/归档——都是 status/mode/title 的编辑）。
   409：目标不携带最新值返回，重拉列表让本地回到服务端状态。 */
export async function saveGoal(id: number, input: GoalInput, version: number): Promise<boolean> {
  if (!st.workspace) return false;
  try {
    const goal = await api<WireGoal>(wsPath(`/goals/${id}`), {
      method: "PATCH",
      body: { ...input, version },
    });
    replaceGoal(goal);
    notify(C.goalSaved);
    return true;
  } catch (e) {
    if (e instanceof ApiError && e.status === 409) {
      await loadGoals(true);
      notify(C.conflict, "error");
      return false;
    }
    fail(e, C.goalSaveFailed);
    return false;
  }
}

export async function deleteGoal(id: number) {
  if (!st.workspace) return;
  const goal = st.goals.find((g) => g.id === id);
  if (!goal) return;
  st.goals = st.goals.filter((g) => g.id !== id);
  st.deletedGoal = goal;
  try {
    await api(wsPath(`/goals/${id}/delete`), { method: "POST", body: {} });
    notify(C.goalDeleted);
  } catch (e) {
    st.goals.unshift(goal);
    st.deletedGoal = null;
    fail(e, C.goalSaveFailed);
  }
}

export async function restoreDeletedGoal() {
  const goal = st.deletedGoal;
  if (!goal) return;
  st.deletedGoal = null;
  st.goals.unshift(goal);
  try {
    const restored = await api<WireGoal>(wsPath(`/goals/${goal.id}/restore`), { method: "POST", body: {} });
    replaceGoal(restored);
    notify(C.restored);
  } catch (e) {
    st.goals = st.goals.filter((g) => g.id !== goal.id);
    fail(e);
  }
}

export function dismissDeletedGoal() {
  st.deletedGoal = null;
  st.notice = "";
}

export async function attachTaskToGoal(
  goalId: number,
  milestoneId: number,
  taskId: string,
): Promise<boolean> {
  if (!st.workspace) return false;
  try {
    const goal = await api<WireGoal>(wsPath(`/goals/${goalId}/milestones/${milestoneId}/tasks`), {
      method: "POST",
      body: { taskId },
    });
    replaceGoal(goal);
    notify(C.goalTaskAttached);
    return true;
  } catch (e) {
    if (e instanceof ApiError && e.status === 409) {
      notify(C.goalTaskAlreadyInGoal, "error");
      return false;
    }
    fail(e, C.goalTaskAttachFailed);
    return false;
  }
}

/* 在里程碑下新建任务：先建任务（走既有幂等接口、进清单），再挂进里程碑。 */
export async function createTaskInGoal(
  goalId: number,
  milestoneId: number,
  input: { t: string; p: number; due: number; g: string },
): Promise<boolean> {
  if (!st.workspace) return false;
  const clientId = "c_" + Math.random().toString(36).slice(2) + Date.now().toString(36);
  try {
    const task = await api<WireTask>(wsPath("/tasks"), {
      method: "POST",
      body: { t: input.t, p: input.p, due: input.due ? dateValue(input.due) : "", g: input.g, clientId },
    });
    st.items.unshift(toItem(task));
    const ok = await attachTaskToGoal(goalId, milestoneId, task.id);
    if (ok) notify(C.added);
    return ok;
  } catch (e) {
    fail(e);
    return false;
  }
}

export async function detachTaskFromGoal(goalId: number, taskId: string): Promise<boolean> {
  if (!st.workspace) return false;
  try {
    const goal = await api<WireGoal>(wsPath(`/goals/${goalId}/tasks/remove`), {
      method: "POST",
      body: { taskId },
    });
    replaceGoal(goal);
    notify(C.goalTaskDetached);
    return true;
  } catch (e) {
    fail(e, C.goalSaveFailed);
    return false;
  }
}

/* 任务所属目标（计划页徽章用）：一条任务最多属于一个目标。 */
export function goalOfTask(taskId: string): WireGoal | null {
  return (
    st.goals.find((g) => g.milestones.some((m) => m.tasks.some((t) => t.taskId === taskId))) ||
    null
  );
}

/* 任务所属里程碑（目标页展开态用）。 */
export function milestoneOfTask(
  taskId: string,
): { goal: WireGoal; milestone: WireGoalMilestone } | null {
  for (const goal of st.goals) {
    const milestone = goal.milestones.find((m) => m.tasks.some((t) => t.taskId === taskId));
    if (milestone) return { goal, milestone };
  }
  return null;
}

/* 目标页勾选任务复用 toggleTask（任务是唯一事实来源）；完成后目标视图
   跟着 st.items 里的任务状态走，这里刷新一次让进度立刻对上。 */
export async function toggleGoalTask(goalId: number, item: Item) {
  await toggleTask(item);
  if (st.goalsLoaded) await loadGoals(true);
}

/* ── 里程碑 ─────────────────────────────────────────────────────────── */

/* 新建里程碑（追加到目标末尾）。 */
export async function addMilestone(
  goalId: number,
  input: { title: string; start: string; due: string },
): Promise<boolean> {
  if (!st.workspace) return false;
  try {
    const goal = await api<WireGoal>(wsPath(`/goals/${goalId}/milestones`), {
      method: "POST",
      body: input,
    });
    replaceGoal(goal);
    notify(C.goalMilestoneAdded);
    return true;
  } catch (e) {
    fail(e, C.goalSaveFailed);
    return false;
  }
}

/* 编辑里程碑标题与起止日期。409：重拉列表让本地回到服务端状态。 */
export async function saveMilestone(
  goalId: number,
  milestoneId: number,
  input: { title: string; start: string; due: string },
  version: number,
): Promise<boolean> {
  if (!st.workspace) return false;
  try {
    const goal = await api<WireGoal>(wsPath(`/goals/${goalId}/milestones/${milestoneId}`), {
      method: "PATCH",
      body: { ...input, version },
    });
    replaceGoal(goal);
    notify(C.goalSaved);
    return true;
  } catch (e) {
    if (e instanceof ApiError && e.status === 409) {
      await loadGoals(true);
      notify(C.conflict, "error");
      return false;
    }
    fail(e, C.goalSaveFailed);
    return false;
  }
}

/* 删除里程碑：乐观移除 + 撤销提示。删除时其下任务解除挂载（任务本身
   保留在清单里）；撤销恢复的是里程碑本身（标题与起止），任务归属不
   自动回填（服务端语义如此，避免误占「一条任务只属于一个目标」约束）。 */
export async function deleteMilestone(goalId: number, milestoneId: number) {
  if (!st.workspace) return;
  const goal = st.goals.find((g) => g.id === goalId);
  const milestone = goal?.milestones.find((m) => m.id === milestoneId);
  if (!goal || !milestone) return;
  const prev = st.goals;
  st.goals = st.goals.map((g) =>
    g.id === goalId
      ? { ...g, milestones: g.milestones.filter((m) => m.id !== milestoneId) }
      : g,
  );
  st.deletedMilestone = { goalId, milestone };
  try {
    await api(wsPath(`/goals/${goalId}/milestones/${milestoneId}/delete`), { method: "POST", body: {} });
    notify(C.goalMilestoneDeleted);
  } catch (e) {
    st.goals = prev;
    st.deletedMilestone = null;
    fail(e, C.goalSaveFailed);
  }
}

export async function restoreDeletedMilestone() {
  const d = st.deletedMilestone;
  if (!d) return;
  st.deletedMilestone = null;
  try {
    const goal = await api<WireGoal>(wsPath(`/goals/${d.goalId}/milestones/${d.milestone.id}/restore`), {
      method: "POST",
      body: {},
    });
    replaceGoal(goal);
    notify(C.restored);
  } catch (e) {
    fail(e);
  }
}

export function dismissDeletedMilestone() {
  st.deletedMilestone = null;
  st.notice = "";
}

/* ── 习惯（工作区级定期练习，独立于目标） ─────────────────────────────── */

/* 读取工作区的习惯列表；服务端在读取时滚动补齐未来 8 周的实例任务，
   所以这一步同时是「展开引擎」的触发点。409 不适用（读操作）。 */
export async function loadHabits() {
  if (!st.workspace) return;
  try {
    const data = await api<{ habits: WireHabit[] }>(wsPath("/habits"));
    st.habits = data.habits;
    st.habitsLoaded = true;
  } catch (e) {
    fail(e, C.habitLoadFailed);
  }
}

export async function addHabit(input: { title: string; days: number[] }): Promise<boolean> {
  if (!st.workspace) return false;
  const clientId = "c_" + Math.random().toString(36).slice(2) + Date.now().toString(36);
  try {
    await api<WireHabit>(wsPath("/habits"), {
      method: "POST",
      body: { title: input.title, weekdays: input.days, clientId },
    });
    await loadHabits();
    await loadTasks(); // 新习惯的实例任务进清单
    notify(C.habitAdded);
    return true;
  } catch (e) {
    fail(e, C.habitSaveFailed);
    return false;
  }
}

/* 编辑习惯标题与重复星期（未来未完成实例随之同步/增删）。409：重拉数据
   让本地回到服务端状态。 */
export async function saveHabit(
  habitId: number,
  input: { title: string; days: number[] },
  version: number,
): Promise<boolean> {
  if (!st.workspace) return false;
  try {
    await api<WireHabit>(wsPath(`/habits/${habitId}`), {
      method: "PATCH",
      body: { title: input.title, weekdays: input.days, version },
    });
    await loadHabits();
    await loadTasks();
    notify(C.habitSaved);
    return true;
  } catch (e) {
    if (e instanceof ApiError && e.status === 409) {
      await loadHabits();
      notify(C.conflict, "error");
      return false;
    }
    fail(e, C.habitSaveFailed);
    return false;
  }
}

/* 删除习惯（硬删）：未来未完成的实例随之删除，历史完成实例保留为普通
   任务。无撤销——实例是自动生成的排期，历史完成记录不受影响。 */
export async function deleteHabit(habitId: number): Promise<boolean> {
  if (!st.workspace) return false;
  try {
    await api(wsPath("/habits/delete"), {
      method: "POST",
      body: { ids: [habitId] },
    });
    st.habits = st.habits.filter((h) => h.id !== habitId);
    await loadTasks();
    notify(C.habitDeleted);
    return true;
  } catch (e) {
    fail(e, C.habitSaveFailed);
    return false;
  }
}

/* 打卡条勾选实例：走任务完成端点（任务是唯一事实来源），成功后刷新
   习惯视图与任务列表。 */
export async function toggleHabitOccurrence(taskId: string, done: boolean, version: number) {
  const hb = st.habits.find((h) => h.tasks.some((t) => t.taskId === taskId));
  const occ = hb?.tasks.find((t) => t.taskId === taskId);
  if (occ) occ.done = !done; // 乐观翻转；失败由 loadHabits 拉正
  try {
    await api<WireTask>(wsPath(`/tasks/${taskId}/completion`), {
      method: "POST",
      body: { done: !done, version },
    });
    await loadHabits();
    await loadTasks();
    if (st.statsLoaded) await refreshStats(true);
  } catch (e) {
    if (occ) occ.done = done;
    if (handleConflict(e)) {
      await loadHabits();
      return;
    }
    fail(e);
  }
}

/* 目标整体重排（卡片上的左移/右移按钮）：把 fromId 插到 toId 之前，
   相邻即交换；提交全部目标 id（含已落定，位置不变），乐观更新，失败
   回滚并由重拉兜底。 */
export async function reorderGoals(fromId: number, toId: number): Promise<boolean> {
  if (!st.workspace || fromId === toId) return false;
  const next = reorderList(st.goals, fromId, toId);
  if (next === st.goals) return false;
  const prev = st.goals;
  st.goals = next;
  try {
    await api(wsPath("/goals/reorder"), { method: "POST", body: { ids: next.map((g: WireGoal) => g.id) } });
    return true;
  } catch (e) {
    st.goals = prev;
    fail(e, C.goalSaveFailed);
    await loadGoals(true);
    return false;
  }
}

function clearGoals() {
  st.goals = [];
  st.goalsLoaded = false;
  st.deletedGoal = null;
  st.deletedMilestone = null;
  st.goalsNewSeq = 0;
  st.goalsOpenId = 0;
  st.goalsOpenSeq = 0;
  st.goalsViewing = false;
}

function clearHabits() {
  st.habits = [];
  st.habitsLoaded = false;
}
registerLogoutHook(clearHabits);
registerLogoutHook(clearGoals);

/* App 页头的「新建目标」按钮： GoalsView 通过 watch 消费这个信号。 */
export function requestNewGoal() {
  st.goalsNewSeq += 1;
}

/* 计划页日历上的里程碑标记被点击时：GoalsView 打开该目标详情。 */
export function requestOpenGoal(id: number) {
  st.goalsOpenId = id;
  st.goalsOpenSeq += 1;
}

/* ── 记账本 ───────────────────────────────────────────────────────────── */

export interface TxInput {
  date: string; // YYYY-MM-DD
  type: "exp" | "inc";
  category: string; // 仅支出使用；收入传 ""
  amountFen: number;
  note: string;
}

/* 按服务端排序口径（date 倒序、同日内 id 倒序）把一笔插回列表。 */
function insertTxSorted(tx: WireTx) {
  const i = st.txs.findIndex((t) => t.date < tx.date || (t.date === tx.date && t.id < tx.id));
  if (i >= 0) st.txs.splice(i, 0, tx);
  else st.txs.push(tx);
}

/* 拉取某月流水。ledgerSeq 防竞态：连点切换月份时，只有最后一次请求的
 * 响应允许落盘，慢响应不会把用户已切走的月份盖回来。 */
let ledgerSeq = 0;
export async function loadLedger(month: string, force = false) {
  if (!st.workspace) return;
  if (!force && st.ledgerLoaded && st.ledgerMonth === month) return;
  const seq = ++ledgerSeq;
  st.ledgerLoading = true;
  st.ledgerLoadError = "";
  try {
    const data = await api<WireLedgerMonth>(wsPath(`/ledger?month=${month}`));
    if (seq !== ledgerSeq) return;
    st.txs = data.entries;
    st.ledgerMonth = month;
    st.ledgerLoaded = true;
    st.online = true;
  } catch (e) {
    if (seq !== ledgerSeq) return;
    st.ledgerLoadError = e instanceof ApiError && e.status === 0 ? C.loadError : C.ledgerLoadFailed;
  } finally {
    if (seq === ledgerSeq) st.ledgerLoading = false;
  }
}

export async function addTx(input: TxInput): Promise<boolean> {
  if (!st.workspace) return false;
  const clientId = "tx_" + Math.random().toString(36).slice(2) + Date.now().toString(36);
  const inMonth = input.date.slice(0, 7) === st.ledgerMonth;
  const temp: WireTx = { id: 0, ...input, version: 0, createdAt: "" };
  if (inMonth) st.txs.unshift(temp);
  try {
    const tx = await api<WireTx>(wsPath("/ledger"), { method: "POST", body: { ...input, clientId } });
    const idx = st.txs.findIndex((t) => t.id === temp.id);
    if (idx >= 0) st.txs.splice(idx, 1);
    if (tx.date.slice(0, 7) === st.ledgerMonth) insertTxSorted(tx);
    notify(C.txAdded);
    return true;
  } catch (e) {
    st.txs = st.txs.filter((t) => t.id !== temp.id);
    fail(e, C.txSaveFailed);
    return false;
  }
}

/* 编辑账目。409 与目标同约定：服务端不带最新值返回，重拉当月对齐，
 * 绝不静默覆盖其他设备的新修改。 */
export async function saveTx(id: number, input: TxInput, version: number): Promise<boolean> {
  if (!st.workspace) return false;
  const prev = st.txs.find((t) => t.id === id);
  const snapshot = prev ? { ...prev } : null;
  if (prev) Object.assign(prev, input);
  try {
    const tx = await api<WireTx>(wsPath(`/ledger/${id}`), { method: "PATCH", body: { ...input, version } });
    const idx = st.txs.findIndex((t) => t.id === id);
    // 改日期可能把这笔移出当前展示月：不再属月就从列表撤下。
    if (idx >= 0) st.txs.splice(idx, 1);
    if (tx.date.slice(0, 7) === st.ledgerMonth) insertTxSorted(tx);
    notify(C.txUpdated);
    return true;
  } catch (e) {
    if (e instanceof ApiError && e.status === 409) {
      await loadLedger(st.ledgerMonth, true);
      notify(C.conflict, "error");
      return false;
    }
    if (prev && snapshot) Object.assign(prev, snapshot);
    fail(e, C.txSaveFailed);
    return false;
  }
}

export async function removeTx(tx: WireTx) {
  if (!st.workspace) return;
  const idx = st.txs.findIndex((t) => t.id === tx.id);
  st.deletedTx = tx;
  if (idx >= 0) st.txs.splice(idx, 1);
  try {
    await api(wsPath("/ledger/delete"), { method: "POST", body: { ids: [tx.id] } });
    notify(C.txDeleted);
  } catch (e) {
    if (idx >= 0) st.txs.splice(Math.min(idx, st.txs.length), 0, tx);
    st.deletedTx = null;
    fail(e);
  }
}

export async function restoreDeletedTx() {
  const tx = st.deletedTx;
  if (!tx) return;
  st.deletedTx = null;
  try {
    const data = await api<{ entries: WireTx[] }>(wsPath("/ledger/restore"), {
      method: "POST",
      body: { ids: [tx.id] },
    });
    for (const t of data.entries) {
      if (t.date.slice(0, 7) === st.ledgerMonth) insertTxSorted(t);
    }
    notify(C.restored);
  } catch (e) {
    st.deletedTx = tx;
    fail(e);
  }
}

export function dismissDeletedTx() {
  st.deletedTx = null;
  st.notice = "";
}

function clearLedger() {
  ledgerSeq += 1;
  st.txs = [];
  st.ledgerMonth = "";
  st.ledgerLoaded = false;
  st.ledgerLoadError = "";
  st.deletedTx = null;
}
registerLogoutHook(clearLedger);

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
