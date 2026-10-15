# 必须补齐的全栈能力

这份文件是给使用模板的线上 agent 的实施契约，不是显示给最终用户的网页文案。
当前已实现 Vue 3 前端 + Go 后端的真实全栈版本（见下方「实施状态」）；用户要求完整应用时，以下条目为验收标准，不得以 localStorage/mock 代替。

## 实施状态（2026-10）

已落地，测试证据见 DELIVERY.md：

- 数据库：嵌入式 SQLite（modernc.org/sqlite，WAL），表含 users、sessions、workspaces、workspace_members、task_groups、tasks、task_events；embedded migration + 回滚。`workspace_id` 行级隔离，全部查询带工作区归属校验。
- 身份：bcrypt 密码、httpOnly SameSite=Lax 会话 Cookie（7 天滚动续期）、注册即建默认工作区、登录限流、变更请求强制 `X-Requested-With` 头（CSRF 防护）。
- 任务 API：listTasks、createTask（client_id 幂等键）、updateTask / setCompletion（version 乐观并发，409 携带最新任务）、reorderTasks（原子事务）、deleteTasks（软删 + task_events）、restoreTasks（服务端恢复）、getActivity（task_events 按工作区时区聚合）、importTasks（用户确认后导入旧 localStorage）。
- 前端：frontend/ 为 Vue 3 + Vite + TypeScript，页面与原模板一致；store 乐观更新 + 失败回滚 + 409 以服务端版本替换；legacy 导入需用户在弹窗中明确同意。
- 统计同源：热力格、连续天数、近 7 天完成全部来自同一 task_events 聚合（`src/lib/heat.js` 只做换算），无合成历史。
- 未接入（如实列出）：无对象存储/附件上传（产品本就没有该功能，不伪造）；无通知推送；单机 SQLite，未接外部数据库服务；未部署上线。详见 DELIVERY.md。

## 1. 找到所有本地替身

| 位置 | 当前实现 | 全栈替换要求 |
| --- | --- | --- |
| frontend/src/App.vue 对应的 store.ts（STORAGE/load/保存/patch） | 已接真实 API；localStorage `todo-day-demo-cn`、兼容旧键 `todo-day` 仅用于用户确认的 legacy 导入（带 `todo-day-imported` 标记） | 所有写操作已接真实数据库事务；导入必须保留预览、去重与失败回退 |
| src/lib/todo.js 的 seed、enrichSampleState；src/lib/samples.js（旧 React 目录，已删除） | 已移除样本注入与月度样本生成；生产新用户任务列表为空 | 仅开发/显式样例工作区使用；生产禁止自动注入 |
| frontend/src/store.ts 的 Item | 服务端生成字符串 ID（`t_` 前缀 base32），version 用于乐观并发 | 不得把自增数字或 UUID 直接传入旧数字逻辑（旧逻辑已删） |
| frontend/src/lib/heat.js 的 counts 及 Heat/Recap/Stats 消费处 | 已接完成事件聚合（task_events，工作区时区口径）；热力/连续天数/近7天统计同源 | 不得把合成历史当真实进度 |
| frontend/src/lib/export.ts 的 CSV/海报导出；frontend/public/fonts | 客户端生成，无上传；字体为静态资源 | 当前没有附件上传能力；不要伪造"已存云端"或上传 API |

## 2. 数据库与身份（全栈必做）

- 使用用户已授权的数据库和认证服务，不擅自开通服务。至少有 users、workspaces、workspace_members、task_groups、tasks、task_events。✅ 已实现（SQLite 嵌入式，未开通外部服务）。
- tasks 至少包括 id、workspace_id、created_by、title、group_id、priority(1–3)、due_date(可空的 YYYY-MM-DD 日历日期)、position、completed_at、deleted_at、created_at、updated_at、version。日期不要直接序列化成本地午夜 UTC 时间造成跨时区前一天；事件时间用 UTC，统计按工作区时区聚合。✅ 已实现（事件时间为固定宽度 UTC 字符串，按工作区 timezone 聚合，默认 Asia/Shanghai）。
- 每次完成/撤销完成写 task_events；删除采用软删除，撤销恢复原任务及排序。清理已完成是可恢复的批量事务。明确历史事件在删除后是否保留，所有统计使用同一约定。✅ 已实现（删除保留历史事件；统计只计"最新完成事件且仍为完成"的任务）。
- 建 migration、索引(workspace_id/due_date/position/deleted_at)、约束和回滚方案；nid 不再是服务端 ID 来源。✅ 已实现（partial index 仅覆盖未删除行；up/down migration + schema_migrations）。
- 服务端从会话获取用户身份并校验 workspace_members。每一个读取/编辑/批量操作都校验归属；不能信任请求体的 user_id/workspace_id。配套行级隔离或等价授权。✅ 已实现（workspace 取自路径，membership 校验失败返回 403）。
- 未登录/令牌过期给恢复入口；敏感信息和服务角色密钥仅存在服务端。✅ 已实现（401 前端引导重新登录；无密钥写入前端）。

## 3. API 与同步（全栈必做）

抽出 listTasks、createTask、updateTask、setCompletion、reorderTasks、deleteTasks、restoreTasks、getActivity 接口。可用 HTTP/RPC/服务端 action，不强制框架；必须有真实持久化：✅ 已实现为 REST（Go net/http），见 backend/internal/api/tasks.go。

- list 支持 workspace、日期范围、分组、搜索、状态及分页，日历和 Todo 共用一个规范化缓存。✅ 已实现（listTasks 查询参数 + 前端单一 st.items 状态）。
- create 使用服务端 ID 和幂等请求键；edit/reorder/complete 使用 version 或 updated_at 条件防止覆盖其他设备的新修改。✅ 已实现（client_id 唯一键防重；version 条件更新，409 返回最新任务）。
- 排序和批量删除原子提交；undo 调服务端恢复，不能只复原本地列表。说明撤销期限与超时行为。✅ 已实现（单事务；撤销无期限，软删期间均可恢复）。
- 页面加载/提交/断网/超时/401/403/409/5xx 都有明确状态、重试和乐观更新回滚；服务端拒绝时不得留下"已保存"。✅ 已实现（offline/loadError/sessionNotice/conflict 提示 + 乐观回滚）。
- 刷新、重新登录、另一个浏览器/设备可读到同一份数据；多标签冲突不能默默丢数据。离线缓存只能作缓存，不冒充云同步。✅ 已实现（服务端持久化；前端不留长期离线缓存）。
- 既有 localStorage 仅可提供用户确认的导入流程，预览数量、验证内容、去重/ID映射，并在导入成功前保留原值。绝不能清缓存来掩盖迁移失败。✅ 已实现（offerLegacyImport/confirmImport，失败保留原值）。

## 4. 存储（区分数据与文件）

- 任务/分组/完成历史存数据库，不能把 JSON 写浏览器就算完成持久化。✅ 已实现。
- 当前无上传、头像、附件或云端导出：不要为了"有 storage"凭空增加产品功能或开 bucket。字体/静态资产可随构建托管。✅ 未增加上传功能，导出仍由客户端生成。
- 若用户需要任务附件、头像、截图、导出归档，接真实私有对象存储。数据库记录 owner/workspace/task、object_key、原始文件名、可信 MIME、size、checksum、状态和时间；不把二进制/base64 塞进任务行。⬜ 未接入（用户未要求；保持未实现）。
- 上传流程：鉴权 → 创建待上传记录 → 短时签名上传 URL → 客户端上传 → 服务端验证对象存在/大小/类型 → 标记完成；失败显示重试，不能收到文件名就报成功。⬜ 未接入。
- 下载/预览必须再次鉴权并生成短时签名 URL。私有文件禁止公共桶/永久公开 URL；服务端限制大小、类型、配额，防路径遍历；必要时扫描。⬜ 未接入。
- 删除/撤销和对象生命周期联动：在撤销窗口内保留对象，过期清理孤立/未完成上传；失败可审计和重试。不擅自删除已有用户文件。⬜ 未接入（无对象，不涉及）。

## 5. 真实验收与交付

- 两个独立浏览器会话：创建/编辑/完成/排序/删除/撤销，在另一会话和刷新后都一致；服务重启后仍存在。✅ 已验证（集成测试 + 真实服务冒烟）。
- 两个不同账号：越权读取、猜测任务/文件 ID、跨 workspace 写入均被服务端拒绝。✅ 已验证（403 测试）。
- 数据库/API 故障、断网、重复请求和并发冲突：无重复任务、不丢已保存数据，错误可恢复。✅ 已验证（幂等键、409、乐观回滚测试）。
- 月末/闰年/夏令时/跨时区日期正确；日历、清单、统计共享真实数据；生产无自动样本和合成历史。✅ 已验证（前端单测 + 服务端时区聚合）。
- 若有文件：真实上传/下载/授权拒绝/超限/失败重试/删除撤销/孤立对象清理全链路验收。⬜ 无文件功能。
- 保留 `npm test`、`npm run typecheck`、`npm run build`，并增加 API/授权/数据库/对象存储集成测试。不要把本地单测当作云服务已接入的证据。✅ 前端保留三个脚本；后端新增 `go test ./...`（API 集成测试 + migration 回滚测试，覆盖授权/冲突/幂等）。
- 交付说明列出已实现、未实现、所需环境变量名称（非密钥值）、migration/回滚/备份策略及测试证据。未接入时明确说未接入；不要用前端"Demo"横幅代替实施说明。✅ 见 DELIVERY.md。
- 本文件不授权发布、账号创建、费用支出或真实数据导入。
