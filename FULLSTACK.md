# 必须补齐的全栈能力

这份文件是给使用模板的线上 agent 的实施契约，不是显示给最终用户的网页文案。
当前没有真实后端；只做视觉改造不需要实现以下服务。用户要求完整应用时，这些是必做项，不能以 localStorage/mock 代替。

## 1. 找到所有本地替身

| 位置 | 当前实现 | 全栈替换要求 |
| --- | --- | --- |
| src/App.tsx 的 STORAGE、load、保存 effect、patch | localStorage `todo-day-demo-cn`，兼容旧键 `todo-day`；浏览器内存中的新增、编辑、完成、排序、删除/撤销 | 提取 taskRepository/API adapter，所有写操作接真实数据库事务；保留交互反馈 |
| src/lib/todo.js 的 seed、enrichSampleState；src/lib/samples.js | 59–64 条当前月份样本；仅未编辑的旧七条样本允许一次性追加升级 | 仅开发/显式样例工作区使用；生产新用户为空，禁止自动注入 |
| src/lib/types.ts 的 Item、State.nid | 数字 ID、自增计数、本地时间戳 | 服务端生成稳定 ID；同步更新类型、排序、撤销及存储校验；不要直接把 UUID 传入当前数字逻辑 |
| src/lib/heat.js 的 counts 及 Heat/Recap/Stats 消费处 | 18 周合成历史 + 当天任务完成数 | 接完成事件聚合；热力/连续天数/近7天统计必须同源，不得把合成历史当真实进度 |
| src/lib/tool.js、kit.js 的导出；public/fonts | CSV/海报/PDF由客户端生成；字体是静态资源 | 当前没有附件上传能力；不要伪造“已存云端”或上传 API |

## 2. 数据库与身份（全栈必做）

- 使用用户已授权的数据库和认证服务，不擅自开通服务。至少有 users、workspaces、workspace_members、task_groups、tasks、task_events。
- tasks 至少包括 id、workspace_id、created_by、title、group_id、priority(1–3)、due_date(可空的 YYYY-MM-DD 日历日期)、position、completed_at、deleted_at、created_at、updated_at、version。日期不要直接序列化成本地午夜 UTC 时间造成跨时区前一天；事件时间用 UTC，统计按工作区时区聚合。
- 每次完成/撤销完成写 task_events；删除采用软删除，撤销恢复原任务及排序。清理已完成是可恢复的批量事务。明确历史事件在删除后是否保留，所有统计使用同一约定。
- 建 migration、索引(workspace_id/due_date/position/deleted_at)、约束和回滚方案；nid 不再是服务端 ID 来源。
- 服务端从会话获取用户身份并校验 workspace_members。每一个读取/编辑/批量操作都校验归属；不能信任请求体的 user_id/workspace_id。配套行级隔离或等价授权。
- 未登录/令牌过期给恢复入口；敏感信息和服务角色密钥仅存在服务端。

## 3. API 与同步（全栈必做）

抽出 listTasks、createTask、updateTask、setCompletion、reorderTasks、deleteTasks、restoreTasks、getActivity 接口。可用 HTTP/RPC/服务端 action，不强制框架；必须有真实持久化：

- list 支持 workspace、日期范围、分组、搜索、状态及分页，日历和 Todo 共用一个规范化缓存。
- create 使用服务端 ID 和幂等请求键；edit/reorder/complete 使用 version 或 updated_at 条件防止覆盖其他设备的新修改。
- 排序和批量删除原子提交；undo 调服务端恢复，不能只复原本地列表。说明撤销期限与超时行为。
- 页面加载/提交/断网/超时/401/403/409/5xx 都有明确状态、重试和乐观更新回滚；服务端拒绝时不得留下“已保存”。
- 刷新、重新登录、另一个浏览器/设备可读到同一份数据；多标签冲突不能默默丢数据。离线缓存只能作缓存，不冒充云同步。
- 既有 localStorage 仅可提供用户确认的导入流程，预览数量、验证内容、去重/ID映射，并在导入成功前保留原值。绝不能清缓存来掩盖迁移失败。

## 4. 存储（区分数据与文件）

- 任务/分组/完成历史存数据库，不能把 JSON 写浏览器就算完成持久化。
- 当前无上传、头像、附件或云端导出：不要为了“有 storage”凭空增加产品功能或开 bucket。字体/静态资产可随构建托管。
- 若用户需要任务附件、头像、截图、导出归档，接真实私有对象存储。数据库记录 owner/workspace/task、object_key、原始文件名、可信 MIME、size、checksum、状态和时间；不把二进制/base64 塞进任务行。
- 上传流程：鉴权 → 创建待上传记录 → 短时签名上传 URL → 客户端上传 → 服务端验证对象存在/大小/类型 → 标记完成；失败显示重试，不能收到文件名就报成功。
- 下载/预览必须再次鉴权并生成短时签名 URL。私有文件禁止公共桶/永久公开 URL；服务端限制大小、类型、配额，防路径遍历；必要时扫描。
- 删除/撤销和对象生命周期联动：在撤销窗口内保留对象，过期清理孤立/未完成上传；失败可审计和重试。不擅自删除已有用户文件。

## 5. 真实验收与交付

- 两个独立浏览器会话：创建/编辑/完成/排序/删除/撤销，在另一会话和刷新后都一致；服务重启后仍存在。
- 两个不同账号：越权读取、猜测任务/文件 ID、跨 workspace 写入均被服务端拒绝。
- 数据库/API 故障、断网、重复请求和并发冲突：无重复任务、不丢已保存数据，错误可恢复。
- 月末/闰年/夏令时/跨时区日期正确；日历、清单、统计共享真实数据；生产无自动样本和合成历史。
- 若有文件：真实上传/下载/授权拒绝/超限/失败重试/删除撤销/孤立对象清理全链路验收。
- 保留 `npm test`、`npm run typecheck`、`npm run build`，并增加 API/授权/数据库/对象存储集成测试。不要把本地单测当作云服务已接入的证据。
- 交付说明列出已实现、未实现、所需环境变量名称（非密钥值）、migration/回滚/备份策略及测试证据。未接入时明确说未接入；不要用前端“Demo”横幅代替实施说明。
- 本文件不授权发布、账号创建、费用支出或真实数据导入。
