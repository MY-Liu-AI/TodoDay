# Todo Day · 专注. 任务

一个浅色默认的个人任务规划应用，前端 Vue 3 + TypeScript + Vite，后端 Go + SQLite。默认进入日历，也提供 Todo 清单和进度统计，中英文功能一致。参考用户提供的图片采用青绿主色及分类色日程，可选深色主题。

## 给线上 agent 的强制接入指令

使用模板前先读 [AGENTS.md](./AGENTS.md) 和 [FULLSTACK.md](./FULLSTACK.md)。数据库、身份/权限、任务 API、跨设备持久化与真实活动记录已在本仓库实现（frontend/ + backend/）；用户需要上传附件时再接私有对象存储及鉴权/签名/生命周期，后者当前未接入，不是已有功能。
前端保持产品文案，不显示"交互 Demo"或开发横幅。不得把 mock 当成真实后端：本地状态和存储错误仍须如实反馈，交付时明确未实现项。详细入口、数据字段、迁移和验收条件见 FULLSTACK.md，实施与未接入清单见 DELIVERY.md。源码交付必须保留这三份说明。

## 能力边界

任务保存在服务端 SQLite 数据库（WAL），注册即用，bcrypt 密码 + httpOnly 会话 Cookie（7 天滚动）；同一账号在刷新、重新登录、另一个浏览器读到同一份数据。工作区按 workspace_members 做服务端授权，越权访问返回 403。编辑/完成用 version 乐观并发，冲突返回 409 并由前端以服务端版本替换；创建用 client_id 幂等键防重复提交。删除为软删除，撤销走服务端恢复；批量排序/删除为单事务。
统计（18 周热力、连续天数、近 7 天完成、分组读数）全部来自 task_events 同一聚合口径（工作区时区，默认 Asia/Shanghai），无合成历史；新账号没有任何样本任务。旧版 localStorage（`todo-day-demo-cn` / `todo-day`）仅提供用户确认的导入：预览数量、服务端去重、成功前保留原值。
未实现：对象存储/附件上传、通知推送、外部数据库、多工作区切换 UI（服务端已支持多工作区成员）。CSV/海报/打印导出由客户端本机生成，不自动外发。隐私模式/清浏览器缓存不影响服务端数据，但本地草稿不保留。

企业微信 AI 助手（`assistant/`）：独立 Go 模块，让你在企业微信里说一句计划、助手记进任务列表。走**自建应用的「接收消息」回调**（群机器人 webhook 是单向推送，收不到消息）。解析优先用 Kimi（Moonshot），未配置密钥或调用失败/超时自动退回规则解析，服务照常可用。走 HTTP 调用现有后端，复用服务端校验、授权与幂等，不直连数据库。回调只做签名校验与解密并**立刻回 `success`**，解析建任务与回执推送异步进行——企业微信 5 秒不响应会重投 3 次，而 AI 调用耗时不可控。`MsgId` 作为幂等键，重投不产生重复任务。**不含部署与内网穿透**，回调地址需公网可达 HTTPS；不做查询/完成/删除/统计指令，固定单工作区，无多人绑定。详见 [assistant/README.md](./assistant/README.md)。

## 功能与入口

- Todo：全部/今天/未完成/已完成、搜索、分组、排序；今天只包含当天截止任务，逾期单独分组。
- 快速添加："明天 交周报 #工作 !!!"，支持日期/分组/优先级（!!!高、!!中、!普通），不是AI，不承诺小时、重复任务识别。
- 新建/编辑：标题、日期、分组、优先级；可键盘操作的原生dialog，空标题错误不会关闭弹窗。
- 日历（默认入口）：七列月历、分类色任务、日期选择、当天议程、当天添加；手机以日期+任务数+分类色标展示，具体内容在议程中。颜色、分组文字同时存在，不以颜色作为唯一提示。清单/日历共用一份数据。未排期任务在日历下方可编辑。
- 完成/撤销完成、删除/撤销删除、清理已完成/撤销；只保留最近一批删除的撤销。
- 手动排序支持同日期分组内pointer拖拽与上下按钮；其他排序隐藏拖动入口。
- 统计保留 18 周热力、分组读数、近 7 天完成柱图；与清单数据同一口径（task_events）。
- 番茄专注（侧栏「我的计划」之下）：列出今天要处理的事（今天截止 + 逾期未完成），可绑定一件或自由开始，任务可在本页直接勾选完成；固定 25 分钟倒计时，开始/暂停/继续/放弃；走完记一个番茄，中途放弃只计时长（不足 1 分钟不记）。进行中的倒计时存在浏览器 localStorage，刷新页面或切换模块后按原结束时刻继续，离开期间走完的自动补记。今日番茄数、今日/本周/本月专注时长、近 4 周每日柱图、今日记录均来自服务端 focus_sessions，按工作区时区归属到结束当天；写入失败提示并可重试。
- CSV、Canvas海报、打印/PDF保留在统计页。图片/PDF由用户触发，不自动外发。
- 企业微信助手（可选，独立进程）：在企业微信里发消息即记录一条任务，支持中英文相对日期、`#分组`、`!!!`/`!!`/`!` 优先级；记录成功后回复内容摘要（日期+星期、分组、优先级）。不处理查询、完成、删除与统计，非文本消息忽略。

## 结构

frontend/src/App.vue：共享状态（store.ts）、布局、模式、反馈与导入弹窗。
frontend/src/store.ts：响应式状态、乐观更新/回滚、409 冲突替换、legacy 导入。
frontend/src/focus.ts：番茄计时器状态机（idle/running/paused/done）、进行中计时的 localStorage 持久化与恢复、专注记录写入与重试、统计读取、结束提示音。
frontend/src/api.ts：REST 客户端（超时、CSRF 头、ApiError 带服务端最新任务）。
frontend/src/copy.ts：本地化 UI 文案。
frontend/src/components/：AuthView、CalendarView（独立月历与日议程）、Calendar（小月历筛选）、TaskDialog（可访问新建/编辑表单）、List（列表、勾选、拖拽及键盘排序）、FocusView（番茄专注页）、Heat/Recap/Stats（统计，同源数据）、Icon。
frontend/src/lib/model.js：搜索、排序、日期网格纯函数（validState 仅用于 legacy 导入校验）。
frontend/src/lib/todo.js：解析、分桶、日期标签（无样本注入）。
frontend/src/lib/heat.js：task_events 聚合的换算与统计口径。
frontend/src/lib/palette.js / export.ts / fx.ts：中英分类色映射、CSV/海报导出、音效触感。
backend/cmd/server/main.go：服务入口（优雅退出、内嵌时区库）。
backend/internal/api/：HTTP 路由、会话认证、CSRF/安全头/限流、任务与统计接口、番茄专注接口、静态托管 SPA。
backend/internal/store/：SQLite 连接（WAL、单写）、embedded migration、users/sessions、tasks、task_events 聚合、focus_sessions 记录与聚合。
frontend/src/styles/planner.css：浅色工作台样式，沿用 kit/tool/app 基础，可选深色。

## 字体与许可

Geist / Geist Mono 与 Noto Sans SC 均本地托管，SIL OFL 1.1 许可证在 frontend/public/fonts。Noto Sans SC 来自 Google Fonts 官方，完整中文覆盖（WOFF2 约 7.4MB，font-display:swap），不依赖外部字体请求。英文数字优先 Geist，中文回退 Noto Sans SC。没有图库品牌图片或商业字体。

## 开发

前端：
```
cd frontend
npm install
npm run dev        # Vite，/api 代理到 localhost:8080
npm test           # node:test 纯函数/静态契约测试
npm run typecheck  # vue-tsc
npm run build      # 产出 frontend/dist
```

后端：
```
cd backend
go test ./...      # API 集成测试 + migration 回滚测试
go run ./cmd/server -addr :8080 -db tododay.db -static ../frontend/dist
```

单进程即可运行：服务端托管前端构建产物并代理同源 API。浏览器实测与设计依据见仓库 pipeline/runs/todo-day。此改造不包含部署、通知或附件存储。
