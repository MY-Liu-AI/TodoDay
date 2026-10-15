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

**前端（frontend/，Vue 3.5 + Vite 6 + TypeScript，vue-tsc 严格模式）**
- 全部页面与原模板一致：日历默认入口、Todo 清单、统计页、浅色默认/可选深色、中文标题「专注. 任务」、键盘可达（原生 dialog、快捷键）、中英对应功能、分类色语义不变。
- store.ts：单一响应式状态；乐观更新 + 失败回滚 + 诚实错误提示（断网/超时/服务端拒绝不留"已保存"）；409 用服务端返回的任务替换本地并提示；401 引导重新登录。
- api.ts：12s 超时、CSRF 头、同源 Cookie、ApiError 携带 status/code/服务端最新任务。
- legacy 导入：检测到旧 localStorage（`todo-day-demo-cn`/`todo-day`）时弹提示，用户点「导入」才上传（服务端按 标题+分组+截止日期 去重）；成功前原值保留，成功后写 `todo-day-imported` 标记且不再提示；跳过则本次会话不再打扰。禁止静默上传。
- 生产无样本注入、无合成热力历史；统计来源标注为真实完成数据。
- 测试：`npm test`（30 个 node:test：解析/排序/删除恢复/导入校验纯函数、日历网格、热力换算口径、AA 对比度、跨语言分类色、产品元数据、文档契约）；`npm run typecheck` 通过；`npm run build` 通过。

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

- 启动时自动执行未应用的 up migration；每个 migration 有对应 down SQL；`store.RollbackAll()`（测试覆盖）可全部回退，含删除 schema_migrations。
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
