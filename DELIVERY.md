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

**设置页：网页配置企业微信助手与 AI 凭证（2026-10-15 增量，2026-10-27 改为菜单 + 弹框）**
- 新表 `workspace_assistant_config`（migration 0003，含 down）：`workspace_id` 作主键（一个工作区一份，由数据库保证）、`enabled`、`bot_id`、`bot_secret_enc`、`kimi_api_key_enc`、`kimi_base`、`kimi_model`、`updated_by`、`updated_at`、`version`（乐观并发）。
- 加密：新包 `backend/internal/secretbox`，AES-256-GCM，主密钥来自环境变量 `TODO_CONFIG_KEY`（base64 编码的 32 字节），密文格式 `base64(nonce ‖ ciphertext ‖ tag)`，nonce 每次随机。**只加密两个密钥字段**，`bot_id`/`kimi_base`/`kimi_model` 保持明文便于排查。`go run ./cmd/server -gen-config-key` 生成一把新密钥。
- **诚实地说明这意味着什么**：这是仓库里第一处需要「解密回明文并交给外部服务」的凭据（Bot Secret 发给企业微信、Kimi Key 发给 Moonshot）。此前唯一的明文凭据是 `sessions.token`，但它只做等值查找、从不解密外出。**主密钥丢失则已存凭据不可恢复**，只能在设置页重新填写——设置页与启动日志都写明了这一点。
- API（`backend/internal/api/assistant.go`）：`GET /api/workspaces/{wid}/assistant`（成员可读，**掩码视图**，只回 `botSecretSet`/`kimiApiKeySet` 布尔值，永不回密钥本身）；`PUT /api/workspaces/{wid}/assistant`（**仅 owner**，密钥字段为 `*string`，`nil`/空表示保留原值，`version` 不匹配 409 且响应**不带 `task` 字段**）；`GET /api/workspaces/{wid}/assistant/credentials`（**仅 owner**，唯一返回明文的接口，供助手进程取用）。
- 权限：新增 `withWorkspaceOwner` 中间件——这是仓库里**第一处 owner-only 边界**（原 `withWorkspace` 调了 `Membership` 但丢掉了角色）。个人工作区里 owner 就是本人，不影响日常使用。
- 主密钥缺失时的行为（关键，避免悄悄降级）：服务**照常启动**（其余功能不受牵连），启动日志给一行提示；`GET` 掩码视图仍可用并回 `encryptionAvailable:false`；保存**含密钥**的请求被拒绝（400 `encryption_unavailable`，信息里指明要设 `TODO_CONFIG_KEY`）并且**不留任何配置行**；只改非密钥项仍允许保存。**绝不退化成明文存储**——有测试直接断言被拒请求后数据库里没有行。
- 前端：侧栏新增「设置」页（`SettingsView.vue`，齿轮图标），两个密钥框为 `type="password"`、**回填时一律清空**、只在用户确实输入时才随请求发送；`store.ts` 新增 `loadAssistantConfig`/`saveAssistantConfig`，`st.assistant` 只存掩码视图；登出时清空；**密钥不进 localStorage**（有测试扫描全部 `localStorage.setItem` 调用断言）。页面显式提示：主密钥缺失、仅 owner 可改、主密钥丢失不可恢复。
- **2026-10-27 界面重构**：设置页整体下线——侧栏「设置」弹出菜单直接列出三个配置项（企业微信助手 / LLM API / 邮箱账单导入，各带图标），点击即开对应的原生 dialog 弹框（点遮罩/Esc/关闭键关闭），少一次页面跳转；`SettingsView.vue` 改为常驻挂载的纯弹框组件（`defineExpose` openBot/openLlm/openMail 供 App 调用），配置视图懒加载（首次打开才拉取，弹框内可重试），仅 owner / 主密钥缺失 / 主密钥丢失不可恢复的警示收进对应弹框。弹框内是该配置的表单 + 测试/启停操作 + 取消/主色保存，凭据语义不变（密码框不回显、留空=保留、显式勾选撤销）。删除说明性长文案（各 Hint 段落、控制提示等 8 个 copy 键）与随设置页下线的标题/副标题文案；`settings.test.mjs` 契约同步（无 settings 页面、菜单直开弹框、长文案已删），108 条全绿。
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

**我的目标（2026-10-18 增量，2026-10-26 目标决策改版 + 里程碑拆解）**
- 数据：`goals`（group_name 分组标签、goal_text/acceptance/failure_cost 目标决策三问、status active|done|archived、position、version、软删；mode 列随 2026-10-27 习惯拆分整体下线，保留在库不读写）+ `goal_milestones`（**2026-10-26 起，migration 0008 含 down**：标题、可空 start_date/due_date、position、version、软删）+ `goal_tasks`（挂载关系，`milestone_id` 指向里程碑；`UNIQUE(task_id)` 保证一条任务同一时间只属于一个目标；position/weight 预留），migration 0004_goals 含 down。分组标签为自由文本（与任务分组同名同色，颜色由前端 palette 按名称推导，服务端只存储不解释），migration 0005_goal_group 含 down。**目标不再有截止日期**：due 列保留在库中但不再读写（历史数据不丢），排期下沉到里程碑的开始/截止日期。
- **进度不落库**：后端读时联表返回挂载任务的实时视图（完成状态、标题、日期、分组、优先级），前端按「已完成数 / 总数」推导百分比；任务仍是唯一事实来源，改任务本身两边同时生效。软删的任务暂时不计入进度，撤销删除后归属与进度自动恢复。
- **目标决策三问（migration 0007_goal_strategy，含 down）**：创建/编辑目标的备注升级为「我的目标 / 验收标准 / 失败代价」三个问答（均可空，各 1000 字上限），详情页在非空时以「目标决策」小节展示，列表卡片显示「我的目标」摘要；旧 note 内容由 up migration 自动迁入 goal_text（有数据迁移测试钉死：note 时代的库升级后 goal_text 收到旧备注、note 列保留原文）。随截止日期一并下线：健康度（进度 vs 时间进度）、剩余/超期天数、风险统计卡（统计条 4 卡 → 3 卡）。里程碑的「锁定」纯视图层——前面有未完成项时当前项置灰，是默认顺序而非服务端强制，允许先完成后面的任务（不制造死锁）。
- API（成员级，不设 owner 边界——目标是个人生产力数据）：GET/POST `/api/workspaces/{wid}/goals`、PATCH `/goals/{gid}`（version 乐观并发，409 **不携带最新目标**，前端重拉列表对齐）、POST `/goals/{gid}/delete|restore`（软删 + 撤销恢复，挂载关系随目标保留）、POST `/goals/{gid}/milestones`（新建里程碑，追加到末尾）、PATCH `/goals/{gid}/milestones/{mid}`（version 乐观并发，409 同左；标题 1–240，日期须 YYYY-MM-DD 且开始 ≤ 截止）、POST `/goals/{gid}/milestones/{mid}/delete|restore`（软删 + 恢复；**删除时其下任务解除挂载、任务本身保留在清单，撤销恢复里程碑本身、任务归属不自动回填**）、POST `/goals/{gid}/milestones/{mid}/tasks`（挂载到里程碑；任务须属本工作区且未删除，跨工作区按不存在处理不泄露归属；已挂别的目标 → 409；里程碑 id 不属于该目标按 404 处理）、`POST /goals/{gid}/tasks/remove`（移出任务）、`POST /goals/reorder`（目标排序，ids 必须正好是本工作区全部未删除目标，否则 400/404，单事务落 position；展示顺序为 active 在前、position 升序，故提交「新顺序 + 已落定目标的既有位置」即可只重排进行中的目标）。里程碑级挂载写操作先经 `requireMilestoneTx` 校验里程碑属于该目标（同 requireGoalTx 的 IDOR 防线）。
- **安全**：挂载类的写操作（attach/detach）先经 `requireGoalTx`/`requireMilestoneTx` 校验目标归属再下笔——此前不带校验的写法会让跨工作区 goalID「先写成功、再报 NotFound」，返回 404 但副作用已经发生（安全复核抓到的 IDOR，已修）。有回归测试钉死：跨区调用返回 NotFound 且对方目标、里程碑、挂载原封不动。
- 前端：侧栏「目标」页在主导航第二项（我的计划下面，target 图标）。列表视图：**统计块 2026-10-27 改版**——原三格数字卡（进行中/平均进度/已达成）下线，改为两块统计图（见下条「列表统计图」）；进行中与已落定两个卡片区（标题无数据也常驻；卡片标题旁显示彩色分组标签，未分组不显示；进行中卡片左右边框正中各有前移/后移小圆键交换相邻卡片排序——卡片内嵌按钮故活动卡片用 div + role="button"，键盘 Enter 仍进详情；已落定卡片不可排序）；创建目标后停留列表页，不自动跳详情。创建/编辑弹窗：**无截止日期输入**；「目标决策」小节内含我的目标/验收标准/失败代价三个问答（textarea，均可空）；分组输入维持 10-22 起的下线状态（编辑时旧值原样回传）。详情视图：进度环（`.goal-ring`，与 app.css 的 `.ring` 区分）、目标决策小节（三问非空才展示，灰底卡片）、**里程碑链（2026-10-26 起）**：每个里程碑一行——左侧箭头展开/收起任务列表（默认收起，当前步默认展开）、里程碑链显示第 N 步标签与锁定态（前面有未完成里程碑即锁定，纯视图层不制造死锁）、当前步整块 `is-current` 青绿高亮；行内显示「开始 至 截止」日期区间（空如实显示「未设置日期」）与任务数；行右侧操作：「新建任务」（复用计划页 TaskDialog，创建即入清单并挂进该里程碑）、「编辑」（行内表单改标题与起止，开始晚于截止给内联错误）、「删除里程碑」（软删走全局 toast 撤销，恢复找回标题与起止；**删除时其下任务解除挂载回到普通清单**）；展开的任务项带完成勾选（复用 toggleTask，进度即时重算）与「移出目标」。底部「添加里程碑」内联表单（标题 + 开始/截止日期）。旧「安排」弹层整套下线。编辑/标记达成/重新打开/归档/删除（软删走全局 toast 撤销）；详情页隐藏 App 页头，只留自带返回行。store 新增 goals 状态与生命周期动作（里程碑 CRUD、挂载/移出、`reorderGoals` 乐观更新 + 失败回滚重拉、`goalOfTask` 供徽章/回执查找归属、删除里程碑乐观移除 + 撤销），409 重拉列表、登出清空（`registerLogoutHook`）。目标 ↔ 计划联动：清单/日历/专注页的任务行都显示目标徽章；勾选挂在目标下的任务时，完成提示附带目标进度；计划页顶部有「今日建议」区（每个进行中目标的下一个待办任务，一键安排到今天）；**计划页日历上每个里程碑的开始/截止各成一个标记**（`ms_` 前缀伪日程项，按目标分组着色，日程清单里显示小靶标、无勾选框），点击标记跳转目标详情（`requestOpenGoal` 信号）。
- 测试：后端 `store/goals_test.go` 9 条（建列/里程碑 CRUD 与乐观并发/里程碑删除恢复与任务解除挂载/挂载与一条任务一目标/跨里程碑与跨目标 NotFound/软删保留归属与任务撤销恢复/目标决策三问往返/版本冲突/目标重排/**跨工作区写操作零副作用**）、`api/goals_test.go` 4 条（全生命周期含里程碑与 409 与目标重排、里程碑输入校验、目标校验含三问超长 400、跨工作区 403）、`migrate_test.go` 增补 0007 数据迁移用例（旧 note → goal_text）与 0008 数据迁移用例（旧 goal_tasks → 同名里程碑 + 挂载指回，迁移后 store 视图齐全）；前端 `goals.test.mjs` 18 条契约（App 接线、store 动作与端点、卡片左移/右移排序、进度推导与锁定、**里程碑三层拆解契约**、里程碑删除撤销、当前步高亮、日历标记联动、三页目标徽章、完成进度回执、今日建议、文案键、**目标决策三问取代备注与截止日期**、**mode 与推进方式选择器已下线**）。`focus.test.mjs` 的页面并集断言同步更新。
- **未实现**：里程碑内/跨里程碑排序 UI 与移动；任务权重（列已预留，恒为 1）；进度历史曲线图（P2）；任务弹窗里没有目标字段（挂载只在目标页操作）。

**目标列表统计图：里程碑甘特（2026-10-27 两次改版）**
- 需求：列表页删掉「进行中/平均进度/已达成」三格数字卡，换成数据统计图。当日先做了「串行甘特 + 并行打卡」两块；同日的「习惯模块拆分」（见下条）把并行目标与周期事项整体迁出目标模块后，打卡图随之下线——逐周打卡改由独立习惯页的本周打卡条呈现，目标列表只保留一块甘特图。
- 数据：全部来自现有 API，**零服务端改动**——甘特用 goal 列表里的里程碑（start/due/tasks.done）。
- 甘特图（`lib/goalcharts.js` 纯函数 `ganttChart`，node:test 真单测）：所有进行中目标共享一条日期轴（4% 两端呼吸位、<14 天兜底 14 天）；画布 = 干净面板卡，三列固定对齐（标签列/时间轨/元信息列，同一套 CSS 变量定宽）；细网格线（25/50/75%）与**连续今天线 + 「今天」标签**画在覆盖层上，与条严格对齐（今天不在轴内就不画，免得误导）；里程碑条 = 浮起胶囊（浅灰底 = 排期跨度，青绿实心填充 = 子任务完成度），双日期→实条，只有开始/只有截止 → 7 天开放跨度 + 开放端小圆角虚线边（如实表达「这端没定」），无日期 → 元信息列显示「未排期」；日期区间与 done/total 放在元信息列（不再塞进条内），条的悬浮 title 给全文；「当前步」标题前加青绿小圆点（与详情页 is-current 同语义，lib 计算有单测）；五个等距日期刻度。
- 前端：列表页顶部原生 HTML/CSS（无图表库，全 CSS 变量自动深浅色），超窄屏横向滚动；只统计进行中目标（已落定进卡片区，不进图）；空态如实。
- 测试：`goalcharts.test.mjs`（gantt 定位/开放端/未排期/今天线/短轴兜底/当前步标记纯函数单测 + 源码契约：旧统计块与死 copy 键下线、甘特接线（画布/网格层/元信息列结构）、新文案键、CSS 走变量且网格与行共用列宽变量、checkin 图与 goalRoutineMap 缓存已清除）；`goals.test.mjs`/`habits.test.mjs` 全量保持绿。
- **未实现**：甘特图按周/月自适应刻度（当前固定 5 等距日期刻度）；图点击行跳转详情（可后续加）。

**习惯模块（2026-10-27 增量：从「并行目标下的周期事项」拆分独立）**
- 拆分背景：并行/串行 mode 让用户混淆——并行目标实际承载的是健身/阅读这类「没有终点的定期练习」，与有终点的目标混在一个模型里。产品决策：目标 = 有终点的结果（只剩里程碑推进一种语义），习惯 = 永不结束的每周练习，独立成模块。
- 数据：`habits` 表（migration 0013 含 down；**工作区级**，不再有 goal_id——id 自 goal_routines 原样迁入、title 1–240、weekday_mask INTEGER CHECK 1–127（bit0=周一…bit6=周日）、position、version、client_id、`UNIQUE(workspace_id, client_id)`）+ `tasks.habit_id INTEGER REFERENCES habits(id) ON DELETE SET NULL`（**实例就是真实任务**，partial 索引；与计划页/日历/今天清单/统计天然同源；实例不继承任务分组，group_id 为 NULL，工作区级习惯暂无分组语义）。**mode 字段整体下线**：`goals.mode` 列保留在库但不再读写、不分支（先例：退役的 due 列）；所有新目标一律 sequential 语义，目标 API 的出入参不再含 mode。**旧并行目标由迁移批量归档**（`UPDATE goals SET status='archived' WHERE mode='parallel' AND status='active'`，壳与决策三问保留可查，不删除）；goal_routines 表删除。同一迁移里 tasks/goal_tasks 按 12-step 重建以加 habit_id 列——SQLite 在事务内 RENAME 会改写他表外键文本、且 DROP 被引用表会触发 FK 检查，故先复制数据、改指回新表、DROP 旧表后再建索引（索引名 schema 全局唯一）。
- 生成模型：**读取触发滚动补齐**——`GET /habits` 把「工作区时区今天 → +56 天（8 周）」内所有命中星期的实例落库；实例 client_id 确定性 `habit-<hid>-<YYYY-MM-DD>`，唯一键冲突即跳过，重试/并发/多设备不产生重复；实例以习惯 position 紧随其后排入当日。窗口语义：过去未完成的实例保留为逾期普通任务，不自动补造历史；删除习惯只删未来未完成实例，已完成的 habit_id 被 SET NULL 成为普通历史任务。习惯不再向目标提供进度汇总——目标的进度只由挂载任务推导。
- API（成员级，不设 owner 边界）：GET/POST `/api/workspaces/{wid}/habits`（GET = 列表 + 触发补齐 + 返回本周一起的实例 `{taskId,title,done,due,version}`，201；client_id 幂等重放返回原习惯）、PATCH `/habits/{hid}`（version 乐观并发，409 不携带最新值；标题同步到未来未完成实例；新加星期补生成、移除星期删除对应未来未完成实例）、POST `/habits/delete`（批量单事务；删未来未完成实例 + 删习惯行，历史保留）。
- 前端：侧栏新增「习惯」导航页（目标与番茄专注之间，repeat 图标）。顶部添加表单（名称 + 7 个星期胶囊多选 + 主色提交）；每条习惯一行——标题 + 静态星期胶囊 + 「下次 x/x（周x）」+ 本周一到周日七格打卡条（实例按日落格：完成=主色实点、过去未完成=虚点、今天=描边，点击格直接勾选/取消实例，走任务完成端点），行内编辑（改名/改星期，409 重拉 + 提示）与删除（确认后删除，文案如实说明历史保留）。store：`loadHabits`/`addHabit`/`saveHabit`/`deleteHabit`/`toggleHabitOccurrence`（乐观翻转 + 409 重拉），登出清空（`registerLogoutHook`）。本周打卡条与下次日期是 `lib/habits.js` 纯函数（weekStrip/nextOccurrence，node:test 真单测）。目标模块不再有任何 mode/周期事项分支：GoalDialog 三步向导（定目标/定验收/定代价）无推进方式选择，GoalsView 详情只保留里程碑区，目标列表的打卡图与 goalRoutineMap 缓存一并下线。
- 测试：后端 `store/habits_test.go`（生命周期含 8±1 个周一实例与 clientId 重放、更新同步（409/标题同步/星期增删）、删除保留已完成历史、跨工作区 NotFound）、`api/habits_test.go`（全生命周期：实例出现在 GET /tasks、输入校验 400、跨工作区 403）、`migrate_test.go` 增补 0013 用例（goal_routines 数据迁入 habits 且 id 保留、旧并行目标归档、up/down 往返）；前端 `habits.test.mjs`（lib 纯函数单测 3 + 契约 5：App 导航接线（目标与专注之间）、本周打卡条、添加表单与校验、store 动作与端点、Wire 类型、后端表/迁移/路由/文案/CSS）。
- **未实现**：习惯不回溯生成过去日期（只从今天起滚动 8 周）；无按月的周期完成率曲线（实例完成事件已入 task_events，统计同源可直接扩展）；习惯实例不继承任务分组（group_id 为 NULL）。

**AI 技能体系 + 番茄专注四象限（2026-10-20 增量）**
- 架构（`backend/internal/ai/`，三层）：能力层 `chat.go`（共享 OpenAI 兼容传输，SSRF 加固——owner 可配的 base URL **不跟随重定向**，Bearer 凭据不外泄；Kimi 系不支持 temperature 参数故不传）、`invoke.go`（调用器：按注册表声明的 context provider **在服务端**组装数据 → 调 LLM → 按 output validator 校验并丢弃幻觉/非法输出）；注册表 `registry.go`（`-skills` 目录加载 `<name>/skill.json` + `prompt.md`，目录缺失=空注册表，单个坏技能=整个加载失败不半加载；**注册表是唯一事实来源，适配器不做技能名 if/else**）；接入适配器 `backend/internal/api/ai.go`（按 `entries` 声明分发入口）。技能无状态、互不调用。
- 凭据：`resolveLLM` 与助手同规则——配置行有 KimiAPIKeyEnc → secretbox 解密（fail-closed，box 缺失报错）；配置行存在但密钥已撤销（空密文）→ **不回落环境变量**；无配置行 → 环境变量兜底（`KIMI_API_KEY`→`MOONSHOT_API_KEY`，`KIMI_BASE_URL` 默认 `https://api.moonshot.cn/v1`，`ASSISTANT_MODEL` 默认 `moonshot-v1-8k`）；都没有 → 409 `ai_not_configured`。
- 配额：每工作区每日 60 次（429 `ai_quota_exceeded`）；其余失败 502 `ai_invoke_failed`，前端如实提示「AI 分析失败」，**绝不编造假结果**。
- 首个技能 `skills/quadrant`（version 0.1.0，entries=["web"]，permission="read"，context="today_tasks"，output="quadrant_result"）：prompt 定义 IU/IN/NU/NN 判定规则（逾期默认 IU、尊重用户优先级、已完成任务如实分类）；服务端 provider 按与前端同一口径取今日任务（今天截止含已完成 + 之前逾期未完成，最多 500 条），validator 归一化象限写法、丢弃幻觉 id 与非法象限（计入 `dropped`）。
- API：`POST /api/workspaces/{wid}/ai/invoke`（成员级，body `{skill}`，结果 `{skill,version,result}`）、`GET .../ai/skills`（列技能元数据，**不含 prompt**）、`POST .../ai/skills/reload`（owner 热加载）。
- 前端：番茄专注页「今天要处理的事」标题旁「四象限」按钮 → `invokeSkill()`（35s 超时）；四格 + 「未分类」分区渲染（复用抽出的 `FocusTaskRow.vue`，reason 显示在任务行下方）；失败显示真实错误信息可重试；结果只在本次会话缓存，不落库、不改任务。
- `parse/kimi.go`/`planner.go` 重构为复用 `ai.DoChat`/`DoChatLenient`（Verify 的响应可空 choices，用 Lenient 版本），解析语义不变。
- 测试：后端 `ai/registry_test.go`（加载/重载/坏技能拒绝）、`ai/invoke_test.go`（validator 归一化与丢弃、provider 口径过滤）、`api/ai_test.go`（fake LLM 端到端 200 / unknown skill 502 / 缺技能 400 / 无凭据 409 / 列技能）；既有全部测试通过。前端 `focus.test.mjs` 契约更新（任务行抽组件后勾选逻辑随行迁移）。
- **未实现**：AI 技能目前只有网页入口（`entries` 已预留 `chat`，企业微信对话入口尚未接技能）；仅支持 Kimi/Moonshot 模型；结果不落库（若未来持久化 AI 产出，必须带 schema 版本）。

**记账本（2026-10-26 增量，简约版）**
- 数据：`ledger_entries`（migration 0006，含 down）：workspace_id / created_by / entry_date（YYYY-MM-DD 本地日历，与 tasks.due_date 同约定）/ type(exp|inc) / category（自由文本服务端不解释，收入为 NULL）/ amount_fen（CHECK > 0，上限 ¥100 亿防溢出）/ note / client_id 幂等键 / version / 软删 deleted_at；`UNIQUE(workspace_id, client_id)`；索引 `(workspace_id, entry_date)` + `(workspace_id) WHERE deleted_at IS NULL` partial 索引。账目是个人数据：成员级授权，不设 owner 边界（与目标同约定）。
- API：`GET /api/workspaces/{wid}/ledger?month=YYYY-MM`（列表 + 同口径月汇总 `{entries, stats:{exp,inc,net}}`）、`POST /ledger`（client_id 幂等：重放返回原行含已软删的，不产生重复）、`PATCH /ledger/{eid}`（version 乐观并发，**409 不带最新值**——与目标同约定，前端重拉当月对齐）、`POST /ledger/delete` / `POST /ledger/restore`（批量、单事务逐条，任一 id 不存在整体回滚；恢复返回完整行 version+1）。支出必须带分类（40 字内），备注 240 字内，金额以分传入 ≥1。
- 前端：侧栏「记账本」页在「统计」之后（钱包图标）。月份条（复用日历的 ‹ 今天 ›）+ 支出/收入/结余三卡（复用 `.goal-stat` 样式，**由列表推导、恒与服务端同口径**）+ 按天分组流水（组头「10月26日 周日 · 支 ¥44.5 · 收 …」，行内色点 + 分类 + 备注 + 金额，悬停浮现 ✎/✕）+ 右下角「＋ 记一笔」FAB；页面挂载期间 **N 键唤起记一笔**（组件级监听，切走即失效，输入框内不抢键）。记一笔是底部弹出 sheet（原生 dialog `margin-top:auto` 滑入）：金额 30px 大字号 + 4 列数字键盘（7 8 9 ⌫ / 4 5 6 C / 1 2 3 . / 0 00 ✓保存）、支出/收入切换、六色分类胶囊（收入不占分类）、备注 + 日期（不晚于今天）；**Enter 保存 · Shift+Enter 保存并再记一笔**（清金额与备注、类型/分类/日期沿用）· Esc 关闭 · 数字键/退格直接输入。store：`loadLedger`（切月竞态保护：连点只落最后一次响应）、`addTx`（tmp-id 乐观插入 → 服务端权威行按 date desc/id desc 归位）、`saveTx`（快照回滚；改日期移出当月即从列表撤下；409 重拉当月 + 提示）、`removeTx`/`restoreDeletedTx`（软删 + 全局 toast 撤销，与任务/目标同一交互）、登出清空（`registerLogoutHook`）。
- 交互源头是 `design/expense-ledger-simple.html`（简约版概念原型，同仓库可复核）；分类色是 `[data-color]` 数据编码（餐饮 peach/通勤 slate/居家 teal/购物 coral/娱乐 cyan/学习 yellow），主操作仅青绿一色，深浅色全走变量。
- 测试：后端 `store/ledger_test.go`（幂等重放不重复、空 clientId 连记两笔各自成行、409/404、批量删除含坏 id 整体回滚、校验）、`api/ledger_test.go`（全生命周期含 409、校验 400、跨工作区 5 条路由全 403）；前端 `ledger.test.mjs` 15 条（lib 纯函数单测 5 + 源码契约 10：导航次序、store 动作与端点、Wire 类型、视图结构、sheet 交互、文案键、toast 撤销、CSS 变量化）。`settings.test.mjs`/`focus.test.mjs` 的页面并集断言同步加 `ledger`。
- 真实服务冒烟（临时脚本，已清理）：注册 → 未登录 401 → 创建 → 幂等重放（同 clientId 不同 body 仍返回原行）→ 列表/汇总 → 编辑 v2 → 过期 version 409 → 无分类支出 400 → 软删消失 → 恢复回来 → 前端 `dist/` 可达 → **重启后数据仍在**，20 项全过。
- **未实现**（保持简约，后续按需再加）：预算/周期账/报销拆分；账目搜索与导出 CSV；分类自定义（当前固定六分类，服务端存自由文本已预留）；与日历/目标的联动；多币种。
- 环境备注：跑 backend 测试需 unset 主密钥——本机 shell 里设置了 `TODO_CONFIG_KEY` 时，两个助手「缺主密钥」用例会失败（环境导致，与记账本无关）。注意本机 `PATH` 里的 `env` 是个静默吞参数的 shim（`env -u ...` 无输出、exit 0、什么都没跑），必须用 `/usr/bin/env -u TODO_CONFIG_KEY`。

**邮箱账单导入（2026-10-27 增量，同日下午从 Agently CLI 方案迁移为原生 IMAP）**
- 需求：每笔消费由银行以邮件推送，后端自动监听邮箱、筛选账单邮件、解析并自动落记账本；邮箱配置作为设置菜单的一个选项。
- 接入方式：**原生 IMAP**（`github.com/emersion/go-imap/v2`，纯 Go，无 CGO）。服务端零额外安装——不依赖任何外部 CLI、没有 OAuth 浏览器跳转：用户在设置弹框填**邮箱地址 + 服务商授权码**即可使用。默认端点 `imap.qq.com:993`（隐式 TLS），`TODO_MAIL_IMAP_ADDR` 可覆盖（测试指向本地内存 IMAP 服务器）。这是对凌晨「数据库零凭据 + CLI 外壳」方案的反转，记录在此。
- **凭据边界（与助手密钥完全同规则）**：授权码经 secretbox（AES-256-GCM，主密钥 `TODO_CONFIG_KEY`）加密落在 `workspace_mail_config.auth_code_enc`（migration 0012）；读视图只回 `authCodeSet` 掩码，永不回明文；**留空=保留 / 显式 `clearAuthCode`=撤销**两条语义并存（撤销是删除，缺主密钥时同样允许）；缺主密钥时存新授权码被拒（400 `encryption_unavailable`），**绝不退化成明文**；机密字段不回落环境变量（`TODO_MAIL_IMAP_ADDR` 这类非机密项取默认值无妨）。
- 数据：`workspace_mail_config`（migration 0010 + 0012 + 0014 + 0015，含 down）：enabled / mailbox（邮箱地址，明文展示字段）/ auth_code_enc（密文授权码）/ senders（migration 0014：`|` 分隔的账单来源发件人，≤30 条×100 字，整体写入无保留语义；0015 下线关键词预筛列）/ last_run_at|ok|error|imported（运行状态，设置弹框可见）/ version 乐观并发；`mail_seen`（migration 0011，含 down）：PK(workspace_id, message_id)，entry_id 可空（NULL=非账单），INSERT OR IGNORE 不覆盖。消息身份 = IMAP UID 十进制串。
- 流程（`backend/internal/mailimport/manager.go`，与助手 Manager 同构：enabled=持久化意图、running=运行态，启动后先补一轮再每日一次）：取配置 → 凭据三态判定（无凭据/已撤销一律不连接，Start 同步检查并回滚 enabled；密文无法解密才记为运行失败）→ 取助手配置里的 Kimi 凭据（共用助手 LLM 配置，未配置如实失败，不退回规则解析）→ IMAP LOGIN + SELECT INBOX（授权码无效映射为 auth 错误，如实写入运行状态「请到邮箱设置核对授权码，并确认已开启 IMAP/SMTP 服务」）→ `UID SEARCH SINCE` 取近 7 天（上限 50 封，超限时只取最新 50 个 UID）→ 逐封：mail_seen 去重 → 预筛（配了 senders：From 小写子串命中来源才放行；没配 senders：不过滤，全部交给 Kimi 判定；不命中标记 seen 跳过）→ `UID FETCH` 整封邮件 → MIME 提取纯文本（text/plain 优先，HTML 极简去标签兜底，base64/quoted-printable 解码，正文截 10 万字符）→ Kimi 函数调用解析 `record_bill`（is_bill / type exp|inc / amount_yuan → 分 / category / date / merchant；营销、对账单、验证码不算账单）→ 落账本（分类兜底「其他」、日期兜底邮件日期→今天、备注「商户 · 主题」、**client_id `mail:<UID>` 与 mail_seen 双重防重**）→ RecordMailRun。关键词预筛已整体下线（2026-10-29，migration 0015），预筛只按来源。来源清单不硬编码银行地址目录——以用户自己邮箱的真实发件人为准：`Discover` 用已存（或表单）凭据只读扫描近 30 天/200 封，按 From 聚合返回 {from, count, sample（最近主题）} 供设置页勾选，不取正文、不标记邮件。
- API（配置与启停 owner 边界，读视图成员级）：`GET /api/workspaces/{wid}/mail`（掩码视图：`mailbox`/`authCodeSet`/`senders`/`lastRun*`/`version`）、`PUT`（存邮箱地址与授权码密文、关键词，不动 enabled；`clearAuthCode` 撤销）、`POST /mail/test`（可带表单当前值，留空回退已存凭据；无凭据回 200 `{ok:false}` 如实提示）、`POST /mail/discover`（owner：同上凭据回退，回 200 `{ok, senders:[{from,count,sample}]}` 或 `{ok:false, message}`，IMAP 登录失败如实提示）、`/mail/start|stop`。**没有 authorize/revoke 端点**（CLI 方案的 OAuth 流程已整体下线）。未配置启动 → 404；无凭据启动 → 400 `no_credentials` 并回滚 enabled；缺主密钥存授权码 → 400 `encryption_unavailable`。
- 前端：设置菜单第三个选项「邮箱账单导入」开原生 dialog（owner 才有保存/启停，成员只读提示）：邮箱地址文本框（可回显）、授权码密码框（**永不回显**，已保存时占位「已保存，留空表示不修改」）、已保存时的「清除授权码」勾选、「账单邮件来源」区（已选来源 chips 可单个移除 + 手动输入添加 + 「从邮箱发现」按钮列出真实发件人（封数 · 最近主题）供勾选，按小写去重；不配来源则提示全部交给 Kimi 判定）、状态行 + 上次运行结果（时间/成功/导入 N 条/失败原因）、测试连接 / 启动 / 停止按钮；启动前未配 Kimi 给出提示；保存配置静默同存来源与关键词后重新拉取视图。store 增 `loadMailConfig`/`saveMailConfig`/`testMailConnection`/`discoverMailSenders`/`startMailImport`/`stopMailImport` 与登出清理。
- 测试：后端 `mailimport/manager_test.go`（**真实内存 IMAP 服务器** `imapmemserver` 跑在自签 TLS 上：端到端 2 封账单入库 + 营销邮件标记 seen + 第二轮 0 重复、按发件人预筛（只配来源放行 / 不配来源全量进 Kimi 两态）、从邮箱发现（3 发件人聚合 + 无凭据如实失败）、授权码错误映射 auth 失败、无凭据/无 Kimi 如实失败、启停、MIME 正文提取 plain/multipart/html/base64、编码词解码、来源拆分匹配/日期校验）、`parse/bill_test.go`（假 Kimi httptest：支出解析、非账单、归一化 15000.005 元→1500001 分、坏日期、缺 key）、`store/mail_test.go`（CRUD、version 冲突 +99、授权码设置/留空保留/显式清除三语义、mailbox 保留/清除、校验、MarkMailSeen 幂等不覆盖 entry_id）、`api/mail_test.go`（成员读/owner 写 403、掩码视图无明文字段、PUT 落库为密文、缺主密钥 400 `encryption_unavailable` 且撤销仍可用、未配置 404、无凭据启动 400 + 回滚 enabled、test 端点如实失败、discover 无凭据如实失败 + 成员 403）、`store/migrate_test.go` 版本 14。前端 `settings.test.mjs` 契约（IMAP 掩码视图无 CLI/OAuth 残留、保存入参 keep/clear 语义、授权码不回显、copy 无 OAuth/CLI 文案）。
- 使用路径：邮箱网页设置里开启 IMAP/SMTP 服务并生成**授权码** → 设置菜单「邮箱账单导入」填邮箱地址 + 授权码 → 保存 → 「测试连接」（可选）→ 「启动自动导入」。服务器零安装；之后每日自动入库，账本页可核对「商户 · 主题」备注来源。
- **未实现**（如实列出）：轮询每日一次、每次近 7 天最多 50 封；**每个工作区各自绑定一个邮箱**（凭据按工作区隔离，与助手一致；CLI 方案的「全服务器共用一份授权」随之取消）；真实银行邮件的解析核对（金额/商户与原文一致）是 TODO；邮件正文超 10 万字符截断；授权码在服务商处的有效期由用户在邮箱侧管理（本服务删除密文即停止取信，彻底作废需在邮箱侧删除授权码）。

## 环境变量与启动（名称，非密钥值）

| 变量 | 默认 | 用途 |
| --- | --- | --- |
| `TODO_ADDR` | `:8080` | 监听地址 |
| `TODO_DB` | `tododay.db` | SQLite 文件路径 |
| `TODO_STATIC` | 空 | 前端构建产物目录；为空则不托管静态文件 |
| `TODO_SECURE_COOKIES` | 未设置 | 置 `1` 时会话 Cookie 加 Secure（HTTPS 部署时） |
| `TODO_CONFIG_KEY` | 未设置 | 助手凭据的加密主密钥（base64 的 32 字节）。缺失时服务照常启动，但设置页无法保存密钥。用 `go run ./cmd/server -gen-config-key` 生成。**丢失不可恢复**，须妥善保管 |
| `TODO_SKILLS` | `../skills` | AI 技能定义目录（也可用 `-skills` 覆盖）；目录缺失时服务照常启动、技能调用不可用（启动日志有提示） |
| `TODO_MAIL_IMAP_ADDR` | `imap.qq.com:993` | 邮箱账单导入的 IMAP 端点（隐式 TLS）。授权码按工作区加密落库（`auth_code_enc`，主密钥 `TODO_CONFIG_KEY`），与助手密钥同一套规则 |

开发：`cd frontend && npm install && npm run dev`（Vite 将 /api 代理到 :8080）；另起 `cd backend && go run ./cmd/server`。
生产单进程：`npm run build` 后 `go run ./cmd/server -static ../frontend/dist` 同源托管 SPA（非 /api 路径回退 index.html）。

## migration / 回滚 / 备份

- 启动时自动执行未应用的 up migration；每个 migration 有对应 down SQL（有测试强制 up/down 成对）；`store.RollbackAll()`（测试覆盖）可全部回退，含删除 schema_migrations。当前版本：0001_init、0002_focus、0003_assistant_config、0004_goals、0005_goal_group、0006_ledger、0007_goal_strategy、0008_goal_milestones、0009_goal_routines（习惯拆分后由 0013 建空表回滚，不再使用）、0010_mail_config、0011_mail_seen、0012_mail_auth、0013_habits（含 down：goal_routines 数据迁入 habits、tasks/goal_tasks 重建加 habit_id、旧并行目标归档）、0014_mail_senders（含 down：预筛来源清单列）、0015_mail_drop_keywords（含 down：下线关键词预筛列）。
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

- `cd backend && go test ./... -timeout 120s` → ok（ai、aibot、api、assistant、mailimport、parse、secretbox、store 八个包）。注意：`newTestEnv` 已用 `t.Setenv` 清空 `TODO_CONFIG_KEY`，缺主密钥用例不再受本机环境变量影响（2026-10-29 修复此前「环境有密钥则三个用例失败」的测试隔离问题）。
- `cd frontend && npm test` → 123/123；`npm run typecheck` → 0 错误；`npm run build` → 成功。
- 真实服务冒烟：注册 → 建任务（带 clientId）→ 列表 → 完成 → activity 返回 `{counts: {"2026-10-15": 1}, timezone: "Asia/Shanghai"}`。
- 记账本冒烟（2026-10-26）：注册 → 未登录 401 → 幂等创建/重放 → 列表与汇总同口径 → 编辑 v2 → 过期 version 409 → 校验 400 → 软删/恢复 → 前端 dist 可达 → 重启持久，20 项全过（脚本为临时产物，已清理）。
- 目标目标决策冒烟（2026-10-26）：注册 → 建目标带三问（goalText/acceptance/failureCost 入线、响应无 due）→ PATCH v2 → 过期 version 409 → 重启后三问仍在且 version 不丢（临时产物已清理）。
- 习惯冒烟（2026-10-27，拆分后口径）：注册 → 建习惯「练胸」选周一 → 8 个周一实例（从今天起滚动）即刻出现在 GET /habits 与 GET /tasks（计划页同源）→ 同 clientId 重放返回原习惯不重复 → 勾选首个实例（走任务完成端点）→ PATCH 改周一+周三：周三 8 个新实例生成、未完成实例标题同步（已完成的历史实例标题保留）→ 删除习惯：未来未完成实例清空、已完成历史保留为普通任务 → 新进程同库重启后习惯与历史仍在；旧并行目标经 0013 迁移归档可查（临时产物已清理）。
- 邮箱账单导入验收（2026-10-27，原生 IMAP 方案）：后端 mailimport 改跑**真实内存 IMAP 服务器**（`imapmemserver` over 自签 TLS：注册登录 + INBOX + append 测试邮件），端到端 2 封账单入库 + 营销邮件标记 seen + 第二轮 0 重复、关键词预筛、授权码错误如实映射为 auth 失败、无凭据/无 Kimi 如实失败、启停、MIME 正文提取（plain/multipart/html/base64）、编码词解码全部通过；parse/bill（假 Kimi：解析/归一化/非账单）、store/mail（CRUD/冲突/授权码设置-保留-撤销三语义/幂等）、api/mail（权限/掩码无明文/落库为密文/缺主密钥 400 且撤销仍可用/404/no_credentials 启动回滚/test 如实失败）、migration 版本 12 全部通过；`go build`/`go vet` 干净。前端 vue-tsc 0 错误、单测全过。**真实邮箱链路未冒烟**——需真实邮箱地址 + 授权码（IMAP 服务已开启）后按「使用路径」验证（如实列出）。
- 账单来源预筛升级 + 关键词预筛下线（2026-10-29）：migration 0014 加 `senders` 列（整体写入）、0015 删 `keywords` 列——预筛只按「账单邮件来源」：配了来源按发件人放行（From 小写子串，可配地址或域名），不配来源则窗口内全部交给 Kimi 判定（is_bill 把关，营销邮件标记 seen 不再回看）；新增 `POST /mail/discover`（owner，只读扫描近 30 天/200 封，按 From 聚合 {from,count,sample}，不取正文不标记邮件）与设置弹框「账单邮件来源」区（chips + 手动添加 + 发现勾选）。后端 memserver 端到端覆盖：按发件人预筛两态（只配来源 / 不配来源全量进 Kimi）、发现聚合与无凭据如实失败、来源 split/match 单测（含 Name <addr> 形态与域名配法）；store 校验（≤30 条×100 字×总长 2000）与整体写入语义、api 层（视图回 senders、discover 无凭据 200 ok:false、成员 403）；migration 版本 13→15；`newTestEnv` 增加 `t.Setenv` 隔离环境主密钥（修复环境有 `TODO_CONFIG_KEY` 时三个「缺主密钥」用例失败）；`go build`/`go vet`/`go test ./...`（环境有密钥）全绿。前端 vue-tsc 0 错误、单测 128 全过（含来源清单契约）。**真实邮箱链路仍未冒烟**（同上）。
- 浏览器双会话、跨账号越权、服务重启持久性由集成测试与上述冒烟覆盖；如需手动复核，按「启动」节起服务后开两个浏览器窗口即可。
