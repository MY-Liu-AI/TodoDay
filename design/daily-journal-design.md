# 每日记录 — 产品设计方案

> 状态：设计稿（未实施）。配套演示：`design/daily-journal-demo.html`（浏览器直接打开）。

## 1. 一句话定位

**你在企业微信随手发（文字、照片、地点），AI 替你整理成一条当天的时间线。**

- 「我的计划」管 *要做什么*，「我的目标」管 *想达成什么*，「每日记录」管 *今天发生了什么*。
- 记录是**被动写入**的：用户侧的唯一动作是「发给助手」，组织、归档、排版全部由 AI 完成。
- 首期不做联动（不从记录生成任务）；「这条值得变成任务」放在 P2，用一键转换实现。

## 2. 设计原则（沿用本产品既定原则）

1. **AI 是组织器，不是改写者。** 原始消息永远原样保留（raw 层），AI 产出的是叠加其上的整理层（title / 归组 / 摘要）。用户随时可查看原文、手动编辑、要求重新整理。整理稿与原文冲突时，以原文为准。
2. **如实呈现，不伪造状态。** 消息送达但 AI 未配置/失败 → 原文照常入库，状态标记为「待整理 / 整理失败」，时间线上如实显示；绝不展示假的「已智能整理」。
3. **不承诺做不到的触达。** 长连接回复绑定 req_id，AI 整理完成（分钟级）后**无法主动推送**到企业微信（除非另证实主动发消息能力）。回执只说「已保存原文，整理结果见「每日记录」」。
4. **隐私默认收紧。** 照片不产生公开 URL，一律走带登录态的 API 出图；跨工作区访问按不存在处理（沿用 `requireGoalTx` 同款归属校验模式）。
5. **可撤销。** 删除 = 软删 + 全局 toast 撤销（与任务/目标同一交互）。

## 3. 用户流程

### 3.1 发送侧（企业微信）

```
用户发消息（文字/照片/定位/语音）
  → 助手秒回执①：「已保存 · 21:34」           ← 原文落库成功才发，失败如实说
  → AI 异步组织（分钟级，不阻塞回执）
  → 网页「每日记录」时间线更新（刷新/重进可见）
```

- 语音：企业微信已转文本，与文字同路（现状如此）。
- 照片：回调给出 URL + AESKey，**5 分钟内**下载并解密落盘（见 §7）。
- 定位：若回调支持 location 类型则直接取经纬度/地名；不支持则退化为文字描述（「在西湖苏堤」），由 AI 抽取地点字段。**设计上两种来源都建模，前端不区分。**

### 3.2 浏览侧（网页）

- 进入「每日记录」默认显示**今天**，顶部日期条可左右切换、点日历跳转。
- 页面自上而下：**AI 每日摘要卡**（可重新生成）→ **时刻时间线**。
- 时间线的基本单元是**时刻（Moment）**：AI 把一段时间内的连续消息归并为一个时刻，而不是一条消息一行——否则照片连发会把时间线刷爆。
- 点击时刻 → 右侧滑出详情：整理稿 / 原文对照、编辑、并入上一时刻、删除（可撤销）。

### 3.3 手动补记

- 时间线上有「补记」入口（补今天，或配合日期跳转到过去某天补记）。
- 补记内容**跳过 AI 直接入库**为纯文本时刻（`ai_status=raw, source=manual`），可后续手动编辑。补记走对话框而非 AI，避免「为了记一句话等一次模型调用」。

## 4. AI 组织规则（prompt 层契约）

输入：某一天（或最近 N 小时）未组织的原始消息（含发送时间、文本、照片缩略引用、定位）。
输出：若干时刻 + 当日摘要。约束写死在 system prompt 里：

| 规则 | 说明 |
| --- | --- |
| 聚合窗口 | 默认 30 分钟内连续消息合并为一个时刻；用户显式说「另起一段/接下来是…」则强制切分 |
| 时间校正 | 消息时间戳是默认发生时间；「补记：昨晚…」类表述以用户陈述为准并在稿中标注「（补记）」 |
| 标题 | ≤ 16 字，陈述句，不抒情（「苏堤快走三公里」✓，「美好的一天」✗） |
| 正文 | 第一人称、≤ 80 字、保留事实与数字；删除口癖；**不得新增原文没有的事实** |
| 照片 | 同批照片归为一组，配一句图注（≤ 20 字）；不逐张描述 |
| 地点 | 优先取定位消息；文字提及则抽取地名，不编造坐标 |
| 心情/人物 | 仅当原文明确出现才提取，字段可空 |
| 摘要 | 每日 ≤ 60 字，覆盖「去了哪 / 做了什么 / 一个细节」，晚间或按需生成 |

模型不可达时整个组织步骤跳过：原文按「一条消息一个时刻（kind=text）」直接入库，`ai_status=raw`。这是降级显示，不是降级体验造假。

## 5. 信息架构与界面

### 5.1 导航

侧栏第五项：**计划 → 目标 → 专注 → 统计 → 记录**（book 图标）。日历仍是默认入口不变。

### 5.2 页面结构（对应 demo）

```
┌────────────────────────────────────────────┐
│ ‹  10月19日 星期一  ›        [📅] [搜索🔍]   │  ← 日期条 + 日历跳转 + 搜索
├────────────────────────────────────────────┤
│ 🤖 今日摘要  ·已整理            [重新生成]    │  ← 摘要卡：状态如实标注
│ 「早上被猫踩醒……走了 8,432 步。」            │
├────────────────────────────────────────────┤
│  ●── 08:12  💬 被猫踩醒的周一早晨           │
│  │        七点被猫踩醒……（展开原文）        │
│  ●── 12:40  📷 午饭：临海面馆（3 张）        │  ← 时刻 = 时间轴节点 + 卡片
│  │        [图][图][图]  碳水救星            │
│  ●── 15:05  📍 西湖 · 苏堤                 │
│  │        快走三公里，桂花全开了。           │
│  ●── 23:10  💬 补记：给爸妈打电话          │  ← 「待整理」徽章如实显示
│                                              │
│  [全部] [照片] [地点] [文字]                  │  ← 过滤器
└────────────────────────────────────────────┘
```

- **状态徽章三态**：`整理中`（异步任务未完成）/ `已整理` / `待整理`（AI 不可用时的原文态）。徽章用中性色，只有主操作是青绿。
- **编辑粒度**：标题、正文、地点可改；照片不可在网页重排（可整组移除）；「并入上一时刻」是唯一的结构调整操作（拆分 P2，AI 重整理可覆盖）。
- **搜索**：按标题/正文/地点名过滤当前月已加载内容。
- **空态**：某天无记录 → 「这一天还没有记录。把随手拍和一句话发给企业微信助手，我会替你整理到这里。」未配置助手时，引导卡如实写「助手未启用」并提供去设置页的入口。

### 5.3 视觉

沿用现有设计体系：浅色默认/可选深色、青绿主色唯一、分类色只用于日历数据编码（记录模块**不引入新颜色语义**，照片、地点、文字用图标+形状区分）。移动端为单列时间线，照片组横滑。

## 6. 数据模型与 API

### 6.1 表（migration `0006_journal`，含 down）

```sql
-- 时刻：时间线的基本单元。raw 与 organized 分层：原文消息不删，整理稿可重生成。
CREATE TABLE journal_moments (
  id            INTEGER PRIMARY KEY AUTOINCREMENT,
  workspace_id  INTEGER NOT NULL REFERENCES workspaces(id),
  day           TEXT NOT NULL,              -- YYYY-MM-DD，工作区时区
  occurred_at   TEXT NOT NULL,              -- ISO8601，时刻的锚点时间
  kind          TEXT NOT NULL,              -- text | photo | place | mixed
  title         TEXT NOT NULL DEFAULT '',
  content       TEXT NOT NULL DEFAULT '',   -- AI 整理稿（raw 态为空）
  place_name    TEXT NOT NULL DEFAULT '',
  place_lat     REAL, place_lng REAL,
  ai_status     TEXT NOT NULL DEFAULT 'raw',-- raw | organizing | organized | error
  source        TEXT NOT NULL DEFAULT 'wecom', -- wecom | manual
  client_id     TEXT NOT NULL UNIQUE,       -- 幂等："wx_"+msgid / "manual_"+uuid
  deleted       INTEGER NOT NULL DEFAULT 0,
  created_at    INTEGER NOT NULL, updated_at INTEGER NOT NULL
);
CREATE INDEX idx_journal_day ON journal_moments(workspace_id, day, occurred_at);

-- 照片附件：storage_key 抽象存储位置（本地磁盘或未来对象存储）。
CREATE TABLE journal_attachments (
  id         INTEGER PRIMARY KEY AUTOINCREMENT,
  moment_id  INTEGER NOT NULL REFERENCES journal_moments(id),
  storage_key TEXT NOT NULL,                -- 相对存储根的路径
  thumb_key  TEXT NOT NULL DEFAULT '',
  mime       TEXT NOT NULL, width INTEGER, height INTEGER, size INTEGER
);

-- 日摘要：一天一行，可重新生成。
CREATE TABLE journal_day_summaries (
  workspace_id INTEGER NOT NULL, day TEXT NOT NULL,
  summary TEXT NOT NULL DEFAULT '', status TEXT NOT NULL DEFAULT 'pending',
  updated_at INTEGER NOT NULL, PRIMARY KEY(workspace_id, day)
);
```

### 6.2 API（成员级，归属校验同 goals）

| 方法 & 路径 | 说明 |
| --- | --- |
| `GET /api/workspaces/{wid}/journal/day?date=` | 某天的时刻 + 摘要 + 各时刻附件（一次性带回，避免 N+1） |
| `GET /api/workspaces/{wid}/journal/month?month=YYYY-MM` | 月内「有记录的天」清单（日历点标记用） |
| `PATCH /api/workspaces/{wid}/journal/moments/{id}` | 编辑标题/正文/地点（version 乐观并发，409 重拉） |
| `POST /api/workspaces/{wid}/journal/moments` | 手动补记（client_id 幂等） |
| `POST /api/workspaces/{wid}/journal/moments/{id}/merge` | 并入上一时刻（单事务，服务端重算锚点时间） |
| `POST /api/workspaces/{wid}/journal/moments/delete` / `restore` | 软删 + 撤销（复用任务/目标的既有交互） |
| `POST /api/workspaces/{wid}/journal/day/summary` | 生成/重新生成当日摘要（异步，返回 accepted） |
| `GET /api/workspaces/{wid}/journal/media/{id}` | 出图/缩略图，**必须登录且属本工作区**，`Cache-Control: private` |

### 6.3 幂等与并发

- 助手写入 `client_id = "wx_" + msgid`（沿用现有 `wx_` 前缀惯例）；重投由 `firstSight` 挡一次，DB 唯一约束再挡一次。
- 编辑/合并走 version + 409，前端重拉对齐（与 goals 同款）。
- AI 组织任务：**按天单飞**（同一 workspace 同一天同时只跑一个组织任务），避免两个任务交错写乱时刻。

## 7. 助手侧改造

1. **意图路由**：Planner 增加第三类决策——`DecisionJournal`（记录意图）与既有 `DecisionWrite`（任务）、`DecisionAsk`（追问）并列。路由依据写进规划 prompt：显式指令（「记一下」「今天…」）、无日期/无行动项的叙述句 → 记录；带行动项/日期 → 任务。拿不准 → 追问「这是要安排的事，还是只想记下来？」
2. **照片**：`aibot` 已能解出 image 帧（URL + AESKey，5 分钟过期）。新增 downloader：收到即下载 → AES 解密（企业微信媒体加密）→ 写存储 → 生成缩略图 → 原始消息入库（含 attachment 行）。下载失败 → 该消息按纯文本记录「[照片下载失败]」并 `ai_status=error`，回执如实告知。
3. **地点**：探测回调的 location msgtype 是否实际可用；不可用则完全交给 AI 从文字抽取（§4）。
4. **两段式回执**：①原文落库即回「已保存 · HH:mm」；②组织完成**不推送**（长连接无主动触达），页面侧随时可见。回执文案不得暗示「已智能整理好」。
5. 凭据与安全沿用现有全部不变式：AES-256-GCM 落库、可撤销、撤销对活动连接生效、密钥不进前端（AGENTS.md 三条安全不变式原样适用）。

## 8. 照片存储（补齐上传能力的缺口）

- 现状：对象存储未接入（AGENTS.md 明示）。本功能**必须**补齐上传链路，否则照片功能不落地。
- 方案：`Storage` 接口（`Put(ctx, key, r, mime)`、`Get(ctx, key)`、`ServeHTTP` 前置于鉴权中间件）+ 本地磁盘实现（`data/uploads/<workspace>/<YYYY-MM>/<uuid>.jpg`，配 `TODO_UPLOAD_DIR`）。表结构只认 `storage_key`，后续换 S3/OSS 不动表。
- 缩略图：落盘时生成 ≤1200px 大图 + ≤400px 缩略图（纯 Go 解码，`golang.org/x/image`）；列表只回 thumb，点开看原图。
- 容量护栏：单文件 ≤ 20MB、每工作区每日 ≤ 200 张，超出回执如实说明。

## 9. 里程碑拆分

| 里程碑 | 内容 | 验收 |
| --- | --- | --- |
| M1 | 文字记录链路（意图路由 → 原文入库）+ 时间线展示（日视图/过滤/详情/编辑/软删撤销） | 发文字 30 秒内时间线可见；AI 关闭时显示「待整理」原文 |
| M2 | 照片：下载解密、存储接口、缩略图、鉴权出图、照片组展示 | 连发 3 张照片聚为一组；跨账号访问 403；缩略图 ≤400px |
| M3 | AI 时刻聚合 + 每日摘要 + 重新生成 | 30 分钟窗口聚合正确；摘要可重生成且原文不丢 |
| M4 | 地点（两种来源）、搜索、月视图打点、导出（纯文本/Markdown） | — |

## 10. 风险与开放问题

1. **长连接主动推送能力未证实** —— 整理完成的二次触达可能不存在；当前设计不依赖它。若日后证实可用，回执②是白捡的体验增益。
2. **location msgtype 可用性未证实** —— 需先抓真实回调验证；不可用则文字抽取兜底（§4 已建模）。
3. **AI 组织成本**：约每天 1–2 次调用（时刻聚合 + 摘要），量小；但需对 Kimi 超时/限流做重试（指数退避，最多 3 次），仍失败落 `error` 态。
4. **照片占盘**：本地磁盘方案需 retention 策略（P2：原图 N 天后清理只留缩略图，或接对象存储生命周期）。
5. **「记录 → 任务」联动**刻意推迟：首期把时间线做扎实，避免记录模块一出生就背 KPI（变成第二个计划页）。

## 11. Demo 说明

`design/daily-journal-demo.html`：单文件、无依赖，直接用浏览器打开。数据为内嵌示例（2026-10-19 与 10-18 两天），演示：日期切换、AI 摘要重新生成、时刻详情（原文对照/编辑/删除撤销）、照片组灯箱、过滤器、搜索、待整理态的如实展示。样式对齐现有设计体系（浅色默认、青绿主色、Noto Sans SC）。
