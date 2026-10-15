# 交付说明 · Vue 3 + Go 全栈版

交付日期：2026-10-15。用户要求：前端改用 Vue 框架、后端使用 Go 语言、前端页面保持不变。

## 已实现（含测试证据）

**后端（backend/，Go 1.26，module `tododay`）**
- 嵌入式 SQLite（modernc.org/sqlite，纯 Go 无 cgo，WAL，busy_timeout，外键开启），单文件数据库，零外部服务依赖。
- 数据表：users（用户名唯一、不区分大小写）、sessions（httpOnly Cookie，7 天滚动续期）、workspaces（含 timezone，默认 Asia/Shanghai）、workspace_members（owner/member）、task_groups、tasks（含 position、completed_at、deleted_at 软删、version、client_id 幂等键）、task_events（每次完成/撤销一条）。
- Embedded migration（up/down SQL + schema_migrations 版本表），含回滚全部迁移的 RollbackAll。
- REST API：POST /api/auth/register|login|logout、GET /api/auth/me；GET/POST /api/workspaces/{wid}/tasks、PATCH /tasks/{tid}、POST /tasks/{tid}/completion、POST /tasks/reorder、POST /tasks/delete、POST /tasks/restore、POST /tasks/import、GET /activity?weeks=N。
- 授权：会话 Cookie 鉴权；workspace 取自路径并校验 membership（403）；非 GET 变更强制 `X-Requested-With: XMLHttpRequest`（CSRF）；登录/注册按 IP 固定窗口限流；安全响应头；bcrypt 密码；未知用户名也做一次 dummy bcrypt 比较防时序探测。
- 并发与一致性：更新/完成走 `version = version + 1 ... AND version = ?`，冲突返回 409 及最新任务；创建用 client_id 唯一键 + 重放返回原任务（网络重试/双击不产生重复）；排序/批量删除/恢复在单事务内完成；撤销无期限（软删期间均可恢复），恢复后位置排在最前。
- 统计口径：activity 按工作区时区聚合 task_events，同一任务只在其「最新完成事件」当天且仍为完成状态时 +1——热力、连续天数、近 7 天柱图因此天然同源。
- 测试：`go test ./internal/...`（10 个 API 集成测试：注册/登录/越权 403/幂等创建/409 冲突/排序校验/删除恢复/导入去重/activity 聚合；migration 全量回滚往返）。全部通过。

**番茄专注（2026-10-15 增量）**
- 表 `focus_sessions`（migration 0002，含 down）：workspace_id / user_id / task_id（可空）/ task_title 快照 / planned_sec / duration_sec / status(completed|abandoned) / started_at / ended_at / client_id 幂等键；索引 (workspace_id, ended_at)。
- API：`POST /api/workspaces/{wid}/focus/sessions`（记一次专注；completed 必须 duration ≥ planned；task_id 必须属于该工作区否则 404；客户端时钟超前封顶到服务端时间）、`GET /api/workspaces/{wid}/focus/stats?days=N`（today / week（周一起）/ month（自然月）聚合 + 每日 map + 今日记录列表，按工作区时区以 ended_at 归属）。
- 前端 `focus.ts`：25 分钟固定，剩余时间按 `endsAt - now` 推算；**进行中的计时持久化到 localStorage（键 `todo-day-focus`，含工作区 id 与结束时刻），刷新页面、切换模块、重开标签页都按原结束时刻继续**，恢复时若已过点则补记一颗并归属当初的结束时刻；铃响由模块触发（不在页面里），切到别的页面也能响；完成/放弃时才写服务端，失败保留待重试（`fs.pending`）并提示，不伪造已统计；登出清状态与快照。`FocusView.vue`：今日任务选择器（可勾选完成）+ 环形倒计时、今日番茄/今日/本周/本月时长、近 4 周每日柱图、今日记录。
- 测试：后端 `TestFocusSessionsAndStats`（幂等重放、abandoned 计时不计番茄、短时 completed 400、跨工作区 task 404 / stats 403、today/week/month/daily/sessions 聚合）；前端 `focus.test.mjs` 14 条契约（含持久化/恢复/补记/铃响位置/勾选完成）。真实服务冒烟：1 完整 + 1 中断（420s）→ `todayAgg {pomodoros:1, seconds:1920}`。
- 已知边界：进行中的倒计时存在浏览器 localStorage，仅对当前浏览器有效——换设备或清浏览器数据不会带走未完成的专注；已完成的记录都在服务端，不受影响。

**前端（frontend/，Vue 3.5 + Vite 6 + TypeScript，vue-tsc 严格模式）**
- 全部页面与原模板一致：日历默认入口、Todo 清单、统计页、浅色默认/可选深色、中文标题「专注. 任务」、键盘可达（原生 dialog、快捷键）、中英对应功能、分类色语义不变。
- store.ts：单一响应式状态；乐观更新 + 失败回滚 + 诚实错误提示（断网/超时/服务端拒绝不留"已保存"）；409 用服务端返回的任务替换本地并提示；401 引导重新登录。
- api.ts：12s 超时、CSRF 头、同源 Cookie、ApiError 携带 status/code/服务端最新任务。
- legacy 导入：检测到旧 localStorage（`todo-day-demo-cn`/`todo-day`）时弹提示，用户点「导入」才上传（服务端按 标题+分组+截止日期 去重）；成功前原值保留，成功后写 `todo-day-imported` 标记且不再提示；跳过则本次会话不再打扰。禁止静默上传。
- 生产无样本注入、无合成热力历史；统计来源标注为真实完成数据。
- 测试：`npm test`（30 个 node:test：解析/排序/删除恢复/导入校验纯函数、日历网格、热力换算口径、AA 对比度、跨语言分类色、产品元数据、文档契约）；`npm run typecheck` 通过；`npm run build` 通过。

**企业微信 AI 助手（assistant/，独立 Go 模块，2026-10-15 增量）**
- 独立模块 `assistant`（module `assistant`，**仅标准库**，无第三方依赖），与 `backend/`、`frontend/` 并列；**未改动 backend 与 frontend 任何代码**。
- 接入方式：企业微信**自建应用的「接收消息」回调**（`/wecom/callback`）。群机器人 webhook 是单向推送、无接收入口，因此不适用。
- 加解密（`internal/wecom`）：签名 = 四个值字典序排序后 SHA1（GET 用 echostr、POST 用 Encrypt）；AES-256-CBC，IV = 密钥前 16 字节；`EncodingAESKey` 43 字符补 `=` 后 base64 解码为 32 字节；明文结构 `random(16)+len(4,大端)+msg+receiveid`；**PKCS#7 块大小为 32**；`receiveid` 必须等于 CorpID，否则拒绝。
- 回调处理：GET 验证返回**明文** echostr（不加引号/BOM/换行）；POST 校验签名解密后**立刻回 `success`**，解析与建任务放后台 goroutine，完成后经 `cgi-bin/message/send` 主动推送回执（access_token 缓存，提前 5 分钟失效，42001 自动刷新重试一次）。
- 解析（`internal/parse`）：优先 Kimi（Moonshot，OpenAI 兼容 `/chat/completions`，function calling 强制结构化输出，system prompt 注入当天日期与工作区时区）；未配置密钥、超时、出错或结果无内容时**自动退回规则解析**并记日志（移植前端 `parseInput`：中英文相对日期、`M月D日`/`M/D`、`#分组`、`!!!`/`!!`/`!` 三档优先级、默认分组规则一致）。
- 数据通路（`internal/todoclient`）：HTTP 调用现有后端 API，复用服务端字段校验、工作区归属校验与 `client_id` 幂等；CSRF 头自动携带；401 自动重登一次并重试；403（配置问题）与 400（内容问题）分类成不同错误，回执文案指向不同排查方向。
- 幂等：企业微信 `MsgId` 直接作为 `clientId`（前缀 `wx_`），重投不会产生重复任务；进程内另有 `MsgId` 去重缓存（上限 2000），避免重跑解析与重复回复。
- 配置：全部经环境变量，启动时校验必填项并**只打印缺失的键名**（不打印值）。键名与默认值见 `assistant/.env.example` 与 `assistant/README.md`（`ASSISTANT_ADDR`、`WECOM_CORP_ID`/`WECOM_AGENT_ID`/`WECOM_SECRET`/`WECOM_TOKEN`/`WECOM_ENCODING_AES_KEY`、`TODO_API_BASE`/`TODO_USERNAME`/`TODO_PASSWORD`/`TODO_WORKSPACE_ID`、`KIMI_API_KEY`（或 `MOONSHOT_API_KEY`）/`KIMI_BASE_URL`/`ASSISTANT_MODEL`/`PARSE_TIMEOUT`）。
- 测试：`cd assistant && go test ./... -count=1` 全部通过 —— 加解密用**企业微信官方的回调测试参数**做互操作验证（签名与解密都对齐官方，不只是自往返）；回调 `httptest` 端到端（GET 返回明文 echostr、POST 秒回 success 并投递消息、签名错误 403、外来 CorpID 403、非文本忽略）；解析表驱动（含闰年、时区差异、非法日期、AI 越界输出收敛）；编排（重投去重、空消息、记录失败、降级提示、发送失败不影响落库）；配置校验；TodoDay 客户端（401 重登、403/400 分类、不可达）。
- **未实现**：不含部署（无 Dockerfile/systemd/CI）与内网穿透；不做查询/完成/删除/统计指令（一条消息 = 记一条任务）；固定单工作区，不支持多人绑定；无持久化队列（进程重启会丢未发出的回执，任务本身已落库）；无多轮对话；非文本消息忽略。
- **尚未验证**：签约、保存回调地址、真实推送需要企业凭据与公网 HTTPS 地址，必须实测一次才算接通；仓库里的测试只覆盖加解密与编排逻辑。

## 环境变量与启动（名称，非密钥值）

| 变量 | 默认 | 用途 |
| --- | --- | --- |
| `TODO_ADDR` | `:8080` | 监听地址 |
| `TODO_DB` | `tododay.db` | SQLite 文件路径 |
| `TODO_STATIC` | 空 | 前端构建产物目录；为空则不托管静态文件 |
| `TODO_SECURE_COOKIES` | 未设置 | 置 `1` 时会话 Cookie 加 Secure（HTTPS 部署时） |

开发：`cd frontend && npm install && npm run dev`（Vite 将 /api 代理到 :8080）；另起 `cd backend && go run ./cmd/server`。
生产单进程：`npm run build` 后 `go run ./cmd/server -static ../frontend/dist` 同源托管 SPA（非 /api 路径回退 index.html）。

## migration / 回滚 / 备份

- 启动时自动执行未应用的 up migration；每个 migration 有对应 down SQL；`store.RollbackAll()`（测试覆盖）可全部回退，含删除 schema_migrations。当前版本：0001_init、0002_focus。
- 备份：直接复制 SQLite 数据库文件（WAL 模式下先 `PRAGMA wal_checkpoint(FULL)` 或随文件复制 `-wal`/`-shm`）；或 `VACUUM INTO 'backup.db'`。
- 回滚代码：checkout 上一版本 tag/commit，二进制与数据库文件一起回退；migration 只增不改，旧代码兼容新库的风险限于新增表/列（当前无此类变更）。

## 未实现（如实列出）

- 对象存储 / 附件 / 头像上传：产品本没有该功能，未新增、未伪造（FULLSTACK.md §4 相应条目保持未接入）。
- 通知推送（邮件/WebPush/飞书等）。
- 外部数据库（PostgreSQL/MySQL 等）：当前为单机嵌入式 SQLite；多副本部署需要外接数据库，未做。
- 找回密码/邮箱验证：未接邮件服务；忘记密码目前无解，需在文档说明。
- HTTPS/TLS 终止：由反向代理负责，未内置。
- 部署、域名、付费资源：按 AGENTS.md 需另行取得用户授权，本次未做。

## 验收记录

- `cd backend && go test ./internal/... -timeout 120s` → ok（api、store 两个包）。
- `cd frontend && npm test` → 30/30；`npm run typecheck` → 0 错误；`npm run build` → 成功（gzip 后 JS 46KB / CSS 10KB）。
- 真实服务冒烟：注册 → 建任务（带 clientId）→ 列表 → 完成 → activity 返回 `{counts: {"2026-10-15": 1}, timezone: "Asia/Shanghai"}`。
- 浏览器双会话、跨账号越权、服务重启持久性由集成测试与上述冒烟覆盖；如需手动复核，按「启动」节起服务后开两个浏览器窗口即可。
