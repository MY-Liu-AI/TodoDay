/* 与 Go 后端通信的薄客户端。
   所有错误统一抛 ApiError：status 为 HTTP 状态码，0 表示网络层失败
   （断网/超时/服务不可达），调用方据此决定重试或回滚。 */

export class ApiError extends Error {
  status: number;
  code: string;
  /** 409 时服务端返回的最新任务，供客户端直接 reconcile。 */
  task: WireTask | null;
  constructor(status: number, code: string, message: string, task: WireTask | null = null) {
    super(message);
    this.status = status;
    this.code = code;
    this.task = task;
  }
  get network() {
    return this.status === 0;
  }
}

export interface WireTask {
  id: string;
  t: string;
  p: number;
  due: string; // "YYYY-MM-DD" 或 ""
  g: string;
  done: boolean;
  at: number; // 完成时间 ms，未完成时为 0
  version: number;
  position: number;
}

/* 目标：目标 → 里程碑 → 任务 三层拆解。进度不落库，由挂载任务的完成
   情况推导；里程碑是独立实体，自带标题与开始/截止日期，与计划页日历
   联动（开始/截止各成一个日历标记）。「目标决策」三问（我的目标/验收
   标准/失败代价）随目标存储，可空。 */
export interface WireGoalTask {
  taskId: string;
  title: string;
  done: boolean;
  due: string; // "YYYY-MM-DD" 或 ""
  group: string;
  priority: number;
  position: number;
}

export interface WireGoalMilestone {
  id: number;
  title: string;
  start: string; // "YYYY-MM-DD" 或 ""（未设开始）
  due: string; // "YYYY-MM-DD" 或 ""（未设截止）
  position: number;
  version: number;
  tasks: WireGoalTask[];
}

export interface WireGoal {
  id: number;
  title: string;
  goalText: string; // 目标决策·我的目标
  acceptance: string; // 目标决策·验收标准
  failureCost: string; // 目标决策·失败代价
  group: string; // 分组标签，"" = 未分组（与任务分组同名同色）
  status: "active" | "done" | "archived";
  position: number;
  version: number;
  createdAt: string; // YYYY-MM-DD
  milestones: WireGoalMilestone[];
}

/* 习惯：工作区级的定期练习（健身/阅读这类没有终点的重复事项），与目标
   相互独立。标题 + 每周重复的星期（1=周一 … 7=周日，可多选）。服务端把
   习惯滚动展开成真实任务实例（未来 8 周），实例出现在计划页/日历/今天
   清单，可勾选完成。 */
export interface WireHabitTask {
  taskId: string;
  title: string;
  done: boolean;
  due: string; // YYYY-MM-DD
  version: number;
}

export interface WireHabit {
  id: number;
  title: string;
  weekdays: number[]; // [1..7] 升序去重
  position: number;
  version: number;
  tasks: WireHabitTask[]; // 本周一起的实例（习惯页本周打卡条用）
}

const TIMEOUT = 12_000;

/* 记账本：一笔收入或支出。date 是 YYYY-MM-DD 本地日历日期（与任务 due 同
 * 约定，不做时区换算）；category 是自由文本（六分类名之一），收入为 ""。 */
export interface WireTx {
  id: number;
  date: string; // YYYY-MM-DD
  type: "exp" | "inc";
  category: string;
  amountFen: number;
  note: string;
  version: number;
  createdAt: string;
}

/* 单月列表 + 同口径汇总（只计未删除）。 */
export interface WireLedgerMonth {
  entries: WireTx[];
  stats: { exp: number; inc: number; net: number };
}

export async function api<T = any>(
  path: string,
  opts: { method?: string; body?: unknown; timeoutMs?: number } = {},
): Promise<T> {
  const { method = "GET", body, timeoutMs = TIMEOUT } = opts;
  const ctrl = new AbortController();
  const timer = setTimeout(() => ctrl.abort(), timeoutMs);
  let res: Response;
  try {
    res = await fetch(path, {
      method,
      signal: ctrl.signal,
      headers: {
        ...(body !== undefined ? { "Content-Type": "application/json" } : {}),
        // 服务端要求变更请求带此头（CSRF 防护），same-origin 下发没有问题。
        "X-Requested-With": "XMLHttpRequest",
      },
      body: body !== undefined ? JSON.stringify(body) : undefined,
      credentials: "same-origin",
    });
  } catch (e) {
    clearTimeout(timer);
    throw new ApiError(0, "network", "网络异常或服务不可达");
  }
  clearTimeout(timer);

  let data: any = null;
  try {
    data = await res.json();
  } catch {
    /* 非 JSON 响应 */
  }
  if (!res.ok) {
    throw new ApiError(
      res.status,
      data?.error || "error",
      data?.message || `请求失败（${res.status}）`,
      data?.task || null,
    );
  }
  return data as T;
}

/* ── AI 技能调用 ─────────────────────────────────────────────── */

/* 技能产出里的单条结果（以 quadrant 技能为例，各技能的 result 形状
   由服务端校验后下发，前端按技能约定的结构读取）。 */
export interface WireQuadrantItem {
  id: string;
  quadrant: "IU" | "IN" | "NU" | "NN";
  reason: string;
}

export interface WireSkillResult {
  skill: string;
  version: string;
  result: { items: WireQuadrantItem[]; dropped: number };
}

/* 按名调用服务端技能注册表里的技能。推理模型生成要十几秒到一两分钟，
   超时给足（服务端写超时 120s）。 */
export function invokeSkill(wid: number, skill: string): Promise<WireSkillResult> {
  return api<WireSkillResult>(`/api/workspaces/${wid}/ai/invoke`, {
    method: "POST",
    body: { skill },
    timeoutMs: 125_000,
  });
}
