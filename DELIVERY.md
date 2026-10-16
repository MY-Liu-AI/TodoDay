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
- 独立模块 `assistant`（module `assistant`），与 `backend/`、`frontend/` 并列；**未改动 backend 与 frontend 任何代码**。
- 依赖：仅 `github.com/gorilla/websocket`（长连接客户端）。**该模块不再是「仅标准库」**——Go 标准库没有 WebSocket 客户端。因本机 `GOPROXY=off`，版本锁定为模块缓存中已有 `.zip` 的伪版本 `v1.5.4-0.20250319132907-e064f32e3674`（默认解析会挑到只有 `.mod` 的 v1.5.3 而失败）。
- 接入方式：**智能机器人 + WebSocket 长连接**（`wss://openws.work.weixin.qq.com`，由本服务主动连出）。凭据为 **BotID + Secret**。
  - 选它的理由：不需要公网 HTTPS、不需要反向代理、消息是明文 JSON **不需要加解密**。代价是进程必须常驻、每 30 秒心跳、断线自行退避重连。
  - 群机器人 webhook 是单向推送、无接收入口，不适用；「接收 URL」模式需要公网地址与 AES 加解密，本项目未采用。
- 协议实现（`internal/aibot`）：订阅 `aibot_subscribe`（body 带 `bot_id`/`secret`）→ 心跳 `ping`（30 秒，同时发协议层 ping 帧）→ 入站 `aibot_msg_callback` / `aibot_event_callback` → 回复 `aibot_respond_msg`。
  - **回复必须透传回调的 `headers.req_id`**：长连接的回复是「回给某一次回调」而非「回给某个人」，丢了就发不出去。`bot.Sender` 接口因此是 `Reply(ctx, reqID, content)`。
  - 取回调时**先释放锁再调用**，否则回调里调用 `Reply` 会自锁死（`write` 全程持锁）。
  - 断线退避 3 秒起、上限 60 秒；同一机器人只允许一条有效长连接，新连接会踢掉旧的，故多实例会互踢（已在文档中列为未支持项）。
- 解析（`internal/parse`）：只走 Kimi（Moonshot，OpenAI 兼容 `/chat/completions`，function calling 强制结构化输出，system prompt 注入当天日期与工作区时区）。未配置密钥、超时、出错或结果无内容时**如实返回错误**，由编排层告知用户——不静默成功、不退回关键词规则；模型输出经本地 `normalize` 收敛到后端可接受范围（标题 240 字、分组 40 字、优先级 1–3、日期格式）。内嵌后端另有**多轮规划**（`record_plans`/`ask_user` 两个工具）：一句话拆多条、信息不足时追问，规划失败退回单轮解析，仍失败才报错。
- 数据通路（`internal/todoclient`）：HTTP 调用现有后端 API，复用服务端字段校验、工作区归属校验与 `client_id` 幂等；CSRF 头自动携带；401 自动重登一次并重试；403（配置问题）与 400（内容问题）分类成不同错误，回执文案指向不同排查方向。
- 幂等：`msgid` 直接作为 `clientId`（前缀 `wx_`），重投不会产生重复任务；进程内另有 `msgid` 去重缓存（上限 2000），避免重跑解析与重复回复。
- 配置：全部经环境变量，启动时校验必填项并**只打印缺失的键名**（不打印值）；长连接地址额外校验协议前缀。键名与默认值见 `assistant/.env.example` 与 `assistant/README.md`（`ASSISTANT_ADDR`、`WECOM_BOT_ID`/`WECOM_BOT_SECRET`/`WECOM_BOT_WS_URL`、`TODO_API_BASE`/`TODO_USERNAME`/`TODO_PASSWORD`/`TODO_WORKSPACE_ID`、`KIMI_API_KEY`（或 `MOONSHOT_API_KEY`）/`KIMI_BASE_URL`/`ASSISTANT_MODEL`/`PARSE_TIMEOUT`）。旧的自建应用键名（`WECOM_CORP_ID` 等）已不再使用，设置它们不影响行为（有测试固定这一点）。
- 调试入口：`-record-test "<一句话>"` 跳过企业微信，直接跑「解析 → 建任务」，用于配机器人前确认凭据与链路。
- 测试：`cd assistant && go test ./... -count=1` 全部通过 —— 帧结构（出站字段名、入站单聊/群聊/语音/事件、订阅回执 errcode、命令名常量逐个校验）、**长连接完整环路**（本地假 WS 服务端：连接 → 订阅带对凭据 → 下发消息 → 回调内回复 → 断言回复透传 `req_id` 与内容）、缺 `req_id` 的消息被丢弃且不触发回调、订阅被拒时错误被暴露、无连接/无 `req_id` 时回复明确失败、回调后注入生效；解析表驱动（含闰年、时区、非法日期、AI 越界收敛）；编排（重投去重、空消息、记录失败、降级提示、发送失败不影响落库）；配置校验；TodoDay 客户端（401 重登、403/400 分类、不可达）。
- **真实链路已验证的部分**：用假凭据启动，真实连上 `wss://openws.work.weixin.qq.com`，服务端返回 `errcode 853000 invalid bot_id or secret`。服务端能针对性地拒绝凭据，说明**握手、帧结构与订阅命令均被正确识别**。`/healthz` 探活可用，`-record-test` 走真实后端成功建任务（交周报 / 2026-10-16 / 工作 / 优先级 1）。
- **仍未验证**：订阅成功之后的消息收发（需要真实 BotID + Secret），以及在企业微信客户端里的实际对话体验。README 明确写了「订阅成功只代表连接建立，不代表消息链路通了」。
- **未实现**：不含部署（无 Dockerfile/systemd/CI）；不做查询/完成/删除/统计指令（一条消息 = 记一条任务）；固定单工作区，不支持多人绑定；无持久化队列（进程重启会丢未发出的回执，任务本身已落库）；无多轮对话；非文本消息忽略（语音因已转文本可记录）；**不支持多实例**（同一机器人只能一条长连接）。

**设置页：网页配置企业微信助手与 AI 凭证（2026-10-15 增量）**
- 新表 `workspace_assistant_config`（migration 0003，含 down）：`workspace_id` 作主键（一个工作区一份，由数据库保证）、`enabled`、`bot_id`、`bot_secret_enc`、`kimi_api_key_enc`、`kimi_base`、`kimi_model`、`updated_by`、`updated_at`、`version`（乐观并发）。
- 加密：新包 `backend/internal/secretbox`，AES-256-GCM，主密钥来自环境变量 `TODO_CONFIG_KEY`（base64 编码的 32 字节），密文格式 `base64(nonce ‖ ciphertext ‖ tag)`，nonce 每次随机。**只加密两个密钥字段**，`bot_id`/`kimi_base`/`kimi_model` 保持明文便于排查。`go run ./cmd/server -gen-config-key` 生成一把新密钥。
- **诚实地说明这意味着什么**：这是仓库里第一处需要「解密回明文并交给外部服务」的凭据（Bot Secret 发给企业微信、Kimi Key 发给 Moonshot）。此前唯一的明文凭据是 `sessions.token`，但它只做等值查找、从不解密外出。**主密钥丢失则已存凭据不可恢复**，只能在设置页重新填写——设置页与启动日志都写明了这一点。
- API（`backend/internal/api/assistant.go`）：`GET /api/workspaces/{wid}/assistant`（成员可读，**掩码视图**，只回 `botSecretSet`/`kimiApiKeySet` 布尔值，永不回密钥本身）；`PUT /api/workspaces/{wid}/assistant`（**仅 owner**，密钥字段为 `*string`，`nil`/空表示保留原值，`version` 不匹配 409 且响应**不带 `task` 字段**）；`GET /api/workspaces/{wid}/assistant/credentials`（**仅 owner**，唯一返回明文的接口，供助手进程取用）。
- 权限：新增 `withWorkspaceOwner` 中间件——这是仓库里**第一处 owner-only 边界**（原 `withWorkspace` 调了 `Membership` 但丢掉了角色）。个人工作区里 owner 就是本人，不影响日常使用。
- 主密钥缺失时的行为（关键，避免悄悄降级）：服务**照常启动**（其余功能不受牵连），启动日志给一行提示；`GET` 掩码视图仍可用并回 `encryptionAvailable:false`；保存**含密钥**的请求被拒绝（400 `encryption_unavailable`，信息里指明要设 `TODO_CONFIG_KEY`）并且**不留任何配置行**；只改非密钥项仍允许保存。**绝不退化成明文存储**——有测试直接断言被拒请求后数据库里没有行。
- 前端：侧栏新增「设置」页（`SettingsView.vue`，齿轮图标），两个密钥框为 `type="password"`、**回填时一律清空**、只在用户确实输入时才随请求发送；`store.ts` 新增 `loadAssistantConfig`/`saveAssistantConfig`，`st.assistant` 只存掩码视图；登出时清空；**密钥不进 localStorage**（有测试扫描全部 `localStorage.setItem` 调用断言）。页面显式提示：主密钥缺失、仅 owner 可改、主密钥丢失不可恢复。
- 助手进程改造：`WECOM_BOT_ID`/`WECOM_BOT_SECRET` **不再是启动必填**。`todoclient.FetchCredentials` 向后端取凭据（404 映射为 `ErrNotConfigured`，401 自动重登）。`aibot.Client` 新增 `SetCredentialSource`，**每次重连前重新取凭据**，网页上改完配置重连即生效、无需重启，成功连接会打一行「已连接」便于确认。
- **凭据合并规则（安全复核后收紧）**：后端**有**配置行则以其为准，**机密的与身份字段（BotID / BotSecret / KimiKey）不回落环境变量**；只有接口地址、模型名这类非机密项才取默认值。后端**没有**配置行时全用环境变量，现有 `.env` 部署不受影响。原先的「逐字段回落」会让撤销失效：所有者清空后端密钥后，环境变量里残留的旧凭据会把它悄悄顶回来，助手照旧带着一份用户以为已撤掉的凭据运行。
- **撤销**：`PUT /assistant` 接受 `clearBotSecret` / `clearKimiApiKey`，所有者可真正删除已存密钥（留空只表示「不修改」，两者语义必须并存，否则要么无法撤销、要么误删）。撤销是删除而非存储，**缺主密钥时同样允许**。矛盾输入（同时给清除与新值）以清除为准——代价不对称，缺失是可见的、残留是静默的。前端设置页有对应勾选框。
- **助手侧区分三种情况**（安全复核后收紧）：`ErrDisabled`（已停用）、`ErrNoCredentials`（无凭据／已被撤销）一律**不连接**并保持 3 秒轮询；只有「暂时取不到」（网络抖动等）才沿用旧值继续连。混淆前两者与第三者是 fail-open：把「凭据已撤销」当成暂时故障，助手会带着一份用户以为已删的凭据继续运行。
- **心跳里复查配置**：只在下一次重连时读配置不够 —— 助手正连着时撤销凭据或停用，那条已建立的连接会一直工作，撤了却没生效。心跳（30 秒）顺带复查，发现「不该连接」就主动断开，随后回到 3 秒轮询。所以生效延迟是：未连接时 ≤3 秒，已连接时 ≤30 秒。
- 顺带修复的真 bug：CSRF 中间件原先枚举 `POST/PATCH/DELETE`，**漏了 PUT**，新增的 PUT 路由被测试抓到未校验。改为「非 GET/HEAD/OPTIONS 一律校验」——白名单读方法比黑名单写方法安全，漏一个只会多校验不会少校验。
- 测试：后端新增 `secretbox`（往返、非确定性 nonce、错密钥拒绝、篡改检测、nil Box 不静默通过、长度校验不回显密钥）、`store/assistant_test.go`（首建/更新、留空保留、冲突用 `ErrConfigConflict` 而非 `ErrConflict`、单行约束、级联删除）、`api/assistant_test.go`（**响应体整体扫描不含明文密钥**、缺主密钥拒绝且不落库、留空保留、409 不带 `task`、跨工作区 403、未登录 401、未知字段 400、缺 CSRF 头 403、`-gen-config-key` 输出可用）；`migrate_test.go` 增加「每个 up 必有 down」断言。助手新增 `FetchCredentials` 四条（200/404/401 重登/403）与 `Resolve` 六条（nil 全用环境变量、后端优先、**机密不回落环境变量**、非机密取默认值、撤销后为空、停用优先于环境变量残留）。前端新增 `settings.test.mjs` 八条契约。**安全复核补充的测试**：`store/assistant_revoke_test.go`（清除与保留两条语义并存、矛盾输入以清除为准、首次写入带清除不报错、撤销后可重新配置）、`api/assistant_revoke_test.go`（撤销后掩码与凭据接口均为空、库中密文确已删除、只撤销指定项、缺主密钥也能撤销、未勾选清除时留空仍保留）、`aibot/disabled_test.go`（停用/无凭据都不拨号、来源给空值按未配置处理、普通错误仍继续连接、撤销后重新配置可恢复、两种「不该连接」期间都持续轮询不退出）。全部通过。
- **未实现**：不做密钥轮换/主密钥迁移工具（换主密钥 = 重新填写）；不做多人各自一套机器人；不做 WeChat 用户到工作区成员的发件人绑定（任何在机器人「可见范围」内的人都能以工作区 owner 身份建任务，这是「固定单工作区」的既有设计）。启停能力见下方「内嵌运行助手」。

**内嵌运行助手：测试 + 一键启停（2026-10-16 增量）**
- 把 `assistant/` 的连接+解析逻辑并入 backend：`backend/internal/aibot`（长连接客户端 + `Verify` 订阅校验）、`backend/internal/parse`（Kimi + 规则）、`backend/internal/assistant`（`Manager`：per-workspace goroutine 注册表、`storeRecorder` 直写任务、`credentialSource` 心跳复查）。`backend/go.mod` 新增 `github.com/gorilla/websocket`（与 assistant 同版本）。
- 新增 API（均仅 owner）：`POST /api/workspaces/{wid}/assistant/test`（真实订阅验证凭据，回 `{ok, errcode, errmsg, kimiConfigured}`）、`POST /assistant/start`、`POST /assistant/stop`；`GET /assistant` 掩码视图新增 `running` 字段。`enabled` 仍是「是否应运行」的持久化意图（复用，无新 migration），start/stop 改它并启动/停止进程内 goroutine；后端重启后 `SyncAssistants` 按 `enabled=1` 恢复。
- 前端设置页新增「测试连接」「启动助手/停止助手」按钮与运行状态行（运行中/已停止/未配置）；保存配置不再携带 `enabled`（启停由 start/stop 端点控制，保存不改开关）。
- **单实例约束**：`test`/`start` 做的是真实订阅，会占用该机器人唯一的有效长连接——同时手动跑着独立 `assistant/` 进程（或另一个后端实例）会互相踢；`test` 也会让正在跑的连接短暂断一次（客户端自动重连）。
- **安全边界（如实说明）**：① 发件人授权沿用企业微信后台机器人的「可见范围」配置，不在本服务做 WeChat 用户 → 工作区成员映射——任何能对机器人说话的人都会以该工作区 owner 身份建任务（「固定单工作区、无多人绑定」的既有设计，非本次新增）。② Kimi 客户端不跟随重定向，避免 `KimiBase`（仅 owner 可设）被 302 到内网地址造成 SSRF 并带走 Bearer 密钥；直接连接 owner 提供的地址仍被允许（owner-only + 单机自部署）。
- 测试：`backend/internal/assistant/manager_test.go`（凭据三态、缺主密钥、启动校验、本地假 WS 的启停往返）、`backend/internal/api/assistant_control_test.go`（owner 边界、未配置 404、无凭据 400、缺主密钥 400、CSRF）、`parse` 新增「不跟随重定向」；`assistant` 模块同步加「不跟随重定向」与相对日期断言的运行时化。

## 环境变量与启动（名称，非密钥值）

| 变量 | 默认 | 用途 |
| --- | --- | --- |
| `TODO_ADDR` | `:8080` | 监听地址 |
| `TODO_DB` | `tododay.db` | SQLite 文件路径 |
| `TODO_STATIC` | 空 | 前端构建产物目录；为空则不托管静态文件 |
| `TODO_SECURE_COOKIES` | 未设置 | 置 `1` 时会话 Cookie 加 Secure（HTTPS 部署时） |
| `TODO_CONFIG_KEY` | 未设置 | 助手凭据的加密主密钥（base64 的 32 字节）。缺失时服务照常启动，但设置页无法保存密钥。用 `go run ./cmd/server -gen-config-key` 生成。**丢失不可恢复**，须妥善保管 |

开发：`cd frontend && npm install && npm run dev`（Vite 将 /api 代理到 :8080）；另起 `cd backend && go run ./cmd/server`。
生产单进程：`npm run build` 后 `go run ./cmd/server -static ../frontend/dist` 同源托管 SPA（非 /api 路径回退 index.html）。

## migration / 回滚 / 备份

- 启动时自动执行未应用的 up migration；每个 migration 有对应 down SQL（有测试强制 up/down 成对）；`store.RollbackAll()`（测试覆盖）可全部回退，含删除 schema_migrations。当前版本：0001_init、0002_focus、0003_assistant_config。
- **备份时必须同时备份 `TODO_CONFIG_KEY`**：只备份数据库文件而丢了主密钥，`workspace_assistant_config` 里的两个 `*_enc` 列就是一堆解不开的字节。
- 备份：直接复制 SQLite 数据库文件（WAL 模式下先 `PRAGMA wal_checkpoint(FULL)` 或随文件复制 `-wal`/`-shm`）；或 `VACUUM INTO 'backup.db'`。**实测提醒**：本服务用单连接且很少触发自动 checkpoint，一个全新库在短时间内**全部数据（含 migration 建的表）都只在 `-wal` 里**，主 `.db` 文件可能只有 4KB 空壳。只复制 `.db` 不复制 `-wal` 等于备份了一个空库——这一点在做加密落库验证时被实际踩到，故在此明示。
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
