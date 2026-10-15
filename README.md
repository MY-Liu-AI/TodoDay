# 专注. 任务 · Todo Day

一个浅色默认、可选深色的个人任务规划应用：**Vue 3 前端 + Go 后端 + 嵌入式 SQLite**，单进程即可运行。默认进入日历，也提供 Todo 清单和进度统计，中英文功能一致。

![cover](cover.png)

## 功能总览

### 📅 日历（默认入口）

- 七列月历视图，任务按**分类色**标注：青蓝 / 浅黄 / 蜜桃 / 珊瑚 / 青绿 / 灰蓝，颜色与分组文字同时存在，不以颜色作为唯一提示
- 点击日期查看当天议程、当天直接添加任务；未排期任务在下方可编辑
- 小月历可作为日期筛选器，与清单**共享同一份数据**
- 移动端以「日期 + 任务数 + 分类色标」展示，具体内容在议程中

### ✅ Todo 清单

- 全部 / 今天 / 未完成 / 已完成 筛选，支持搜索、按分组过滤、四种排序（手动 / 日期 / 优先级 / 分组）
- 「今天」只含当天截止任务，逾期任务单独分组标红
- **快速添加**：一行字带出日期、分组、优先级——`明天 交周报 #工作 !!!`（`!!!` 高 / `!!` 中 / `!` 普通）
- 手动排序支持同分组内 pointer 拖拽与上下按钮，其他排序自动隐藏拖拽入口
- 完成 / 撤销完成、删除 / 撤销删除、清理已完成（同样可撤销）；只保留最近一批删除的撤销

### 🍅 番茄专注

- 主页列出**今天要处理的事**（今天截止 + 逾期未完成），选一件绑定，或不绑定直接开始；**今天的任务可直接在这一页勾选完成**，专注完不用切回清单
- 固定 **25 分钟倒计时**：开始 / 暂停 / 继续 / 放弃；走完铃响并记一个番茄；中途放弃不算番茄但专注时长仍计入（不足 1 分钟不记）
- **倒计时跨页面、跨刷新持续**：进行中的专注存在 localStorage，刷新页面、切到别的模块、关掉标签页再回来都按最初定下的结束时刻继续；离开期间已走完的会自动补记一颗（归属时间仍是当初的结束时刻）
- 倒计时按结束时刻推算，后台标签节流不漂；标签页标题同步显示剩余时间，侧栏入口显示剩余分钟；铃响由模块触发，切到别的页面也听得到
- **今日番茄数、今日 / 本周 / 本月专注时长、近 4 周每日柱图、今天的专注记录**全部来自服务端 `focus_sessions`，按工作区时区归属到番茄结束那一天；记录失败时提示并可重试，不伪造已统计
- 任务标题在开始时快照，之后改名或删除不影响历史统计

### 📊 统计

- **18 周完成热力格**：一格一天，悬停显示当天完成数，颜色越深越多
- 本周完成 / 日均 / 最长连续天数，近 7 天完成柱图，按分组进度读数
- 全部 / 未完成 / 已完成 / **逾期**计数
- 热力、连续天数、近 7 天全部来自服务端同一份 `task_events` 聚合（按工作区时区），**无合成历史、无样本注入**
- CSV 导出、Canvas 海报、打印 / PDF 均由客户端本机生成，不自动外发

### 🔐 账号与数据

- 注册即用，bcrypt 密码 + httpOnly 会话 Cookie（7 天滚动续期）
- 同一账号刷新、重新登录、换浏览器读到同一份数据；服务端按 workspace 成员关系授权，越权访问返回 403
- 编辑/完成走 version 乐观并发：冲突时前端采用服务端最新版本并提示，不丢数据
- 创建走 `client_id` 幂等键：双击、断网重试不会产生重复任务
- 旧版 localStorage 数据提供**用户确认后**的导入（预览数量、服务端去重、失败保留原值），绝不静默上传
- 浅色默认，一键切深色；键盘可操作（原生 dialog、快捷键、跳转链接）

## 技术栈与结构

```
frontend/   Vue 3 + Vite + TypeScript（vue-tsc 严格模式）
  src/App.vue        布局、模式切换、反馈
  src/store.ts       响应式状态、乐观更新/回滚、409 冲突替换、legacy 导入
  src/focus.ts       番茄计时器状态机 + 专注记录/统计读取
  src/api.ts         REST 客户端（超时、CSRF 头、错误携带服务端最新任务）
  src/components/    AuthView / CalendarView / Calendar / List / TaskDialog
                     FocusView / Heat / Recap / Stats / Icon
  src/lib/           解析、分桶、日期网格、热力口径、导出、音效（纯函数）
backend/    Go 1.26（net/http，Go 1.22 路由）
  cmd/server/        入口：优雅退出、内嵌时区库
  internal/api/      会话认证、CSRF/安全头/限流、任务与统计 REST、番茄专注 REST、SPA 托管
  internal/store/    SQLite（WAL）连接、embedded migration、users/tasks/task_events/focus_sessions
```

设计要点：

- **统计同源**：每次完成/撤销写 `task_events`，聚合时同一任务只计最新完成事件且仍为完成——热力、连续、近 7 天天然一致
- **乐观更新**：所有写操作先本地生效，服务端失败回滚并如实提示，不留「已保存」假象
- **单进程部署**：服务端直接托管前端构建产物，非 /api 路径回退 index.html

## 快速开始

```bash
# 1. 构建前端
cd frontend
npm install
npm run build          # 产出 dist/

# 2. 启动（单进程，托管前端 + API）
cd ../backend
go run ./cmd/server -addr :8080 -db tododay.db -static ../frontend/dist
```

浏览器打开 http://localhost:8080 ，注册账号即可使用。

开发模式（前端热更新，/api 代理到 8080）：

```bash
cd backend && go run ./cmd/server            # 终端 1
cd frontend && npm run dev                   # 终端 2 → http://localhost:5173
```

### 环境变量

| 变量 | 默认 | 用途 |
| --- | --- | --- |
| `TODO_ADDR` | `:8080` | 监听地址 |
| `TODO_DB` | `tododay.db` | SQLite 文件路径 |
| `TODO_STATIC` | 空 | 前端构建产物目录；为空则不托管静态文件 |
| `TODO_SECURE_COOKIES` | 未设置 | 置 `1` 时会话 Cookie 加 Secure（HTTPS 部署时） |

## 测试

```bash
cd backend && go test ./internal/...   # API 集成测试（授权/幂等/409/软删恢复/导入去重/番茄记录与统计）+ migration 回滚
cd frontend && npm test                # 39 个 node:test：解析/排序/热力口径/对比度/写路径契约/番茄契约/文档契约
cd frontend && npm run typecheck       # vue-tsc，0 错误
```

## 文档

- [AGENTS.md](AGENTS.md) — 模板使用指令（下游 agent 契约）
- [FULLSTACK.md](FULLSTACK.md) — 全栈实施契约与验收标准（含实施状态）
- [DELIVERY.md](DELIVERY.md) — 交付说明：已实现/未实现、迁移/回滚/备份策略
- [info.md](info.md) — 能力边界、功能入口、结构、字体许可

## 许可说明

Geist / Geist Mono / Noto Sans SC 均本地托管（SIL OFL 1.1，许可证见 `frontend/public/fonts/`），不依赖外部字体请求。无图库品牌图片或商业字体。
