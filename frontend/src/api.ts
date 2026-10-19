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

/* 目标：把任务串联成可跟踪的整体。进度不落库，由挂载任务的完成情况
   推导（progress = 已完成权重 / 全部权重）；串行模式按挂载顺序解锁。 */
export interface WireGoalTask {
  taskId: string;
  title: string;
  done: boolean;
  due: string; // "YYYY-MM-DD" 或 ""
  group: string;
  priority: number;
  position: number;
}

export interface WireGoal {
  id: number;
  title: string;
  note: string;
  mode: "parallel" | "sequential";
  due: string; // "YYYY-MM-DD" 或 ""（无排期）
  status: "active" | "done" | "archived";
  position: number;
  version: number;
  createdAt: string; // "YYYY-MM-DD"，健康度的时间起点
  tasks: WireGoalTask[];
}

const TIMEOUT = 12_000;

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
