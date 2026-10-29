# 必须补齐的全栈能力

这份文件是给使用模板的线上 agent 的实施契约，不是显示给最终用户的网页文案。
当前已实现 Vue 3 前端 + Go 后端的真实全栈版本（见下方「实施状态」）；用户要求完整应用时，以下条目为验收标准，不得以 localStorage/mock 代替。

## 实施状态（2026-10）

已落地，测试证据见 DELIVERY.md：

- 数据库：嵌入式 SQLite（modernc.org/sqlite，WAL），表含 users、sessions、workspaces、workspace_members、task_groups、tasks、task_events、goals、goal_milestones、goal_tasks、habits、ledger_entries、ledger_budgets；embedded migration + 回滚。`workspace_id` 行级隔离，全部查询带工作区归属校验。
- 身份：bcrypt 密码、httpOnly SameSite=Lax 会话 Cookie（7 天滚动续期）、注册即建默认工作区、登录限流、变更请求强制 `X-Requested-With` 头（CSRF 防护）。
- 任务 API：listTasks、createTask（client_id 幂等键）、updateTask / setCompletion（version 乐观并发，409 携带最新任务）、reorderTasks（原子事务）、deleteTasks（软删 + task_events）、restoreTasks（服务端恢复）、getActivity（task_events 按工作区时区聚合）、importTasks（用户确认后导入旧 localStorage）。
- 前端：frontend/ 为 Vue 3 + Vite + TypeScript，页面与原模板一致；store 乐观更新 + 失败回滚 + 409 以服务端版本替换；legacy 导入需用户在弹窗中明确同意。
- 统计同源：热力格、连续天数、近 7 天完成全部来自同一 task_events 聚合（`src/lib/heat.js` 只做换算），无合成历史。
- 番茄专注（2026-10-15）：`focus_sessions` 表（migration 0002）+ `POST /focus/sessions`（client_id 幂等、completed 需满计划时长、task_id 校验归属）+ `GET /focus/stats`（今日/本周/本月/每日按工作区时区以结束时间归属）。前端计时本地跑、结束才写服务端，写失败保留重试，统计只读服务端。
- 助手配置页（2026-10-15）：`workspace_assistant_config` 表（migration 0003）+ `secretbox` 包（AES-256-GCM，主密钥 `TODO_CONFIG_KEY`）+ 三个接口（成员读掩码 / owner 写 / owner 取明文）。**这是本仓库第一处需要解密回明文交给外部服务的凭据，也是第一处 owner-only 边界**；主密钥缺失时拒绝保存密钥而非退化成明文，主密钥丢失不可恢复。前端只是写入通道，密钥不进浏览器存储。
- 企业微信助手（2026-10-15，`assistant/` 独立模块）：接入**智能机器人长连接**（`wss://openws.work.weixin.qq.com`，BotID + Secret，明文 JSON，无需公网地址与加解密）；`msgid` 作 clientId 复用建任务幂等，回复绑定回调的 `req_id`；Kimi 解析失败退回规则解析；不直连数据库，经 HTTP 复用本契约第 2、3 节的校验与授权。**该服务未接入新的存储或外部数据库，未新增任何上传能力。**
- 我的目标（2026-10-18）：`goals` + `goal_tasks` 表（migration 0004，含 down），任务串联成目标并跟踪进度；`group_name` 分组标签（migration 0005，含 down，自由文本、与任务分组同名同色）。进度**不落库、由挂载任务的完成状态推导**（任务仍是唯一数据源）；一条任务同一时间只属于一个目标（`goal_tasks.task_id` 唯一约束）；版本乐观并发 + 软删 + 服务端恢复与任务模块同一套约定。**2026-10-26 起目标不再有截止日期**（due 列保留不读写，排期下沉到里程碑），备注升级为「目标决策」三问——我的目标/验收标准/失败代价（migration 0007，旧 note 自动迁入 goal_text，含 down）。**同日目标拆解改为 目标 → 里程碑 → 任务 三层**（migration 0008，含 down）：里程碑是独立实体（标题 + 可选开始/截止日期，与计划页日历联动——开始/截止各成一个日历标记，点击标记跳目标详情），任务经 `goal_tasks.milestone_id` 挂到里程碑下；旧数据每条已挂载任务自动包成同名里程碑（归属与进度推导不断档）。里程碑软删 + 恢复（删除时其下任务解除挂载、任务本身保留在清单，撤销恢复里程碑本身不自动回填任务归属）；串行模式的锁定/当前步按里程碑顺序推导。API：GET/POST `/goals`、PATCH `/goals/{gid}`（409 不带最新目标，前端重拉列表对齐）、POST `/goals/{gid}/delete|restore`、`POST /goals/{gid}/milestones`、PATCH `/goals/{gid}/milestones/{mid}`（409 同左）、POST `/goals/{gid}/milestones/{mid}/delete|restore`、`POST /goals/{gid}/milestones/{mid}/tasks`（挂载，一条任务只属于一个目标）、POST `/goals/{gid}/tasks/remove`（移出任务）、`POST /goals/reorder`（目标排序，ids 须正好是全部未删除目标），均为成员级（目标是个人生产力数据，不设 owner 边界）。**2026-10-27 起串行/并行 mode 整体下线**（migration 0013，含 down）：`goals.mode` 列保留在库但不再读写、不分支（先例：退役的 due 列），所有新目标一律 sequential 语义（锁定仅是视图层默认顺序，服务端不强制）；旧并行目标由迁移批量归档（status='archived'，壳与决策三问保留可查，不删除）。原「并行目标下的周期事项」拆分为独立习惯模块，见下条。
- 习惯（2026-10-27，从「并行目标下的周期事项」拆分独立）：`habits` 表（migration 0013，含 down）——工作区级定期练习（健身/阅读这类没有终点的重复事项），标题 + 每周重复的星期（bit 掩码 1–127）；数据由 `goal_routines` 原样迁入（id 保留），`tasks.routine_id` 改挂 `tasks.habit_id`（FK ON DELETE SET NULL）。与目标的区别：目标 = 有终点的结果（里程碑推进），习惯 = 永不结束的练习（每周打卡），两者不再共用一个 mode 字段。服务端在读取时从今天起滚动生成未来 8 周（HabitRollDays=56）的真实任务实例（client_id 确定性 `habit-<hid>-<YYYY-MM-DD>` 天然幂等），实例就是普通任务——计划页/日历/今天清单/统计同源可见可勾选（完成走任务完成端点）；改星期自动增删未来未完成实例，删习惯只清未来未完成、已完成历史保留为普通任务。API（成员级）：GET/POST `/habits`（GET 即滚动补齐引擎）、PATCH `/habits/{hid}`（409 同目标约定）、POST `/habits/delete`（批量单事务）。前端独立「习惯」导航页（目标与番茄专注之间）：添加表单（名称 + 星期胶囊）+ 每行本周打卡条（周一到周日七格，只排了的星期有格子，点格勾选/取消该次实例）+ 行内编辑/删除；目标模块不再有任何 mode/周期事项分支，旧并行目标在归档区可查。详见 DELIVERY.md。
- AI 技能体系（2026-10-20）：`backend/internal/ai/` 三层架构——能力层（`chat.go` 共享 OpenAI 兼容传输，SSRF 加固、不跟随重定向；`invoke.go` 调用器：按注册表声明的 context provider 在服务端组装数据、调 LLM、按 output validator 校验结果）+ 技能注册表（`registry.go`，从 `-skills` 目录加载 `<name>/skill.json` + `prompt.md`，注册表是唯一事实来源，适配器不做技能名分支）+ 接入适配器（`backend/internal/api/ai.go` 网页入口）。首个技能 `skills/quadrant`（四象限划分，IU/IN/NU/NN）：番茄专注页「今天要处理的事」一键 AI 分类，服务端按同一口径取今日任务（今天计划 + 逾期未完成），模型只输出 JSON，服务端丢弃幻觉 id/非法象限。API：`POST /api/workspaces/{wid}/ai/invoke`（成员级，`{skill}` 入参，结果 `{skill,version,result}`）、`GET .../ai/skills`（列技能，不含 prompt）、`POST .../ai/skills/reload`（owner）。凭据解析与助手同规则（配置行优先、撤销后不回落环境变量，fail-closed），配额每工作区每日 60 次。技能无状态、互不调用；本技能结果不落库、纯一次性建议。
- 记账本（2026-10-26，简约版）：`ledger_entries` 表（migration 0006，含 down）：per-workspace 收支流水（entry_date 为 YYYY-MM-DD 本地日历日期，与 tasks.due_date 同约定；金额以分存储 CHECK > 0；category 自由文本服务端不解释、收入不占分类；client_id 幂等键 `UNIQUE(workspace_id, client_id)`；version 乐观并发；软删 + 服务端恢复）。API（成员级，账目是个人数据不设 owner 边界）：`GET /ledger?month=YYYY-MM`（列表 + 同口径月汇总）、`POST /ledger`（幂等重放返回原行）、`PATCH /ledger/{eid}`（409 不带最新值，前端重拉当月对齐）、`POST /ledger/delete|restore`（批量单事务，任一 id 不存在整体回滚）。前端侧栏「记账本」页（统计之后）：月份切换 + 支出/收入/结余三卡（由列表推导、恒同口径）+ 按天分组流水 + 底部记一笔 sheet（数字键盘、六色分类胶囊、Enter/Shift+Enter/N/Esc）；软删走全局 toast 可撤销。**2026-10-29 起页面拆为两个视图**（与目标页同一套 view-switch）：账本总览 = 流水分析（与上月对比三卡——支出/收入/结余 + 环比，上月汇总独立拉取、失败降级为「—」；分类支出排行条形图，超 top 8 聚合「其他」，全部由当月流水推导、恒同口径）+ 流水列表；分类预算 = 每分类一个**长期月度预算**（`ledger_budgets` 表，migration 0016，含 down；设置一次、每月沿用直到修改，`UNIQUE(workspace_id, category)` upsert 收敛、写响应回全量列表），按当前展示月的花费考核进度（超支 --neg），当月有支出无预算的分类单列提示补预算。预算 API（成员级，与账目同边界）：`GET /ledger/budgets`、`PUT /ledger/budgets`（按分类 upsert）、`DELETE /ledger/budgets/{category}`（URL 编码路径，删不存在 404）。预算的当月花费不从服务端取——前端从当月流水推导，与三卡同口径。详见 DELIVERY.md。
- 邮箱账单导入（2026-10-27，**原生 IMAP 方案**，同日凌晨从 Agently CLI 方案迁移）：`workspace_mail_config` + `mail_seen` 表（migration 0010–0015，含 down；0015 下线关键词预筛列）。后端用纯 Go IMAP 客户端（`github.com/emersion/go-imap/v2`，`backend/internal/mailimport/imap.go`）直连用户自己的邮箱：用户在设置弹框填**邮箱地址 + 服务商授权码**即可使用，服务端零额外安装、无 OAuth 浏览器跳转。凭据边界与助手密钥完全同规则（**这是对凌晨「数据库零凭据」设计的反转，记录在此**）：授权码经 secretbox（AES-256-GCM，主密钥 `TODO_CONFIG_KEY`）加密落在 `workspace_mail_config.auth_code_enc`（migration 0012），视图只回 `authCodeSet` 掩码；留空=保留、显式 `clearAuthCode` 撤销（删除不需要主密钥，缺主密钥时存新授权码 400 `encryption_unavailable`，绝不退化成明文）；机密字段不回落环境变量。流程：每日一次轮询（启动后先补一轮）→ IMAP LOGIN（授权码无效映射为 auth 错误如实记录）→ `SEARCH SINCE` 取近 7 天增量（上限 50 封）→ 预筛（只按账单来源发件人清单：用户在设置弹框「从邮箱发现」勾选自己邮箱里真实出现过的发件人，可手动补地址/域名，migration 0014；不配来源则窗口内全部交给 Kimi 判定，is_bill 把关，营销邮件标记 seen 后不再回看；关键词预筛已整体下线，migration 0015）→ `FETCH` 整封邮件提取纯文本（text/plain 优先，HTML 极简去标签，正文截 10 万字符）→ Kimi 函数调用解析 `record_bill`（is_bill / 收支 / 金额 / 分类 / 日期 / 商户，营销与验证码不算账单）→ 落账本（分类兜底「其他」、日期兜底邮件日期、client_id `mail:<UID>` 幂等；`mail_seen` 表 + 账本幂等键双重防重）→ 运行状态落配置行（设置弹框可见上次结果）。配置与启停为 owner 边界（与助手同一权限模型），读视图为成员级；凭据三态语义沿用助手规则：无凭据/已撤销一律不连接（Start 同步检查并回滚 enabled），密文无法解密如实记失败原因。默认 IMAP 端点 `imap.qq.com:993`（腾讯邮箱），`TODO_MAIL_IMAP_ADDR` 可覆盖；另设 `POST /mail/discover`（owner，只读扫描近 30 天/200 封的发件人聚合）供勾选来源。各家银行真实发件地址以用户自己邮箱为准（发现接口），不硬编码银行目录。详见 DELIVERY.md。
- 未接入（如实列出）：无对象存储/附件上传（产品本就没有该功能，不伪造）；无通知推送；单机 SQLite，未接外部数据库服务；未部署上线；企业微信助手的真实消息链路（订阅后收发）需真实 BotID/Secret 才能验证，目前只验证了握手与订阅帧被服务端正确识别（假凭据返回 errcode 853000）。目标模块未实现：里程碑内排序 UI 与跨里程碑移动、任务权重（列已预留恒为 1）、进度历史曲线图。习惯模块不回溯生成过去日期（只从今天起滚动 8 周），实例任务不继承任务分组（group_id 为 NULL，工作区级习惯暂无分组语义）。AI 技能目前仅网页入口（entries=web），企业微信对话入口尚未接技能；仅支持 Kimi/Moonshot 模型；配额 60 次/工作区/日。记账本保持简约：分类预算是长期月度预算（不支持单月预算/预算结转），无周期账/导出/搜索/自定义分类管理，服务端存自由文本已预留。邮箱账单导入：轮询每日一次、每次最多 50 封/近 7 天；**每个工作区各自绑定一个邮箱**（凭据按工作区隔离，与助手一致）；解析依赖 Kimi（共用助手凭据，未配置则如实失败）；真实银行邮件的解析核对（金额/商户与原文一致）是 TODO；邮件正文超 10 万字符截断；授权码在服务商处的有效期由用户在邮箱设置里管理（本服务删除密文即停止取信，彻底作废需在邮箱侧删除授权码）。详见 DELIVERY.md。

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
