# 每日复盘 — 产品设计方案

> 状态：设计稿（未实施）。配套演示：`design/daily-review-demo.html`（浏览器直接打开，内嵌示例数据）。
>
> 与同目录其他方案的分工：**「每日记录」管今天发生了什么（被动、时间线），「冷镜」管数据里的偏差（系统替你找），「复盘」管你自己承认的问题有没有改掉（主动、结构化、以修正为终点）。** 三者数据同源、互不调用。

## 1. 一句话定位

**每晚五分钟，对着今天的事实回答几个固定问题，把发现的毛病登记进「问题库」；此后每天只需回答「今天又犯了没」，21 天不犯才算改掉。股市想法单独记成「观点卡」，到期回头验证，分清是逻辑对了还是运气好。**

为什么现有的日记/复盘工具改不掉人：

| 常见做法 | 为什么无效 | 本方案的对策 |
| --- | --- | --- |
| 自由文本写感想 | 写完即忘，无法回答「这个问题我第几次犯了」 | 问题是**实体**，每天打点，出现次数是数出来的 |
| 只记反思不记对策 | 反思不落地 | 每个问题必须挂一条**对策**，对策可一键变成明天的任务或一个习惯 |
| 凭记忆回顾 | 记忆美化，复盘对象失真 | 今天的事实由服务端同源数据**预填**（任务/番茄/习惯/支出），不可编辑 |
| 股市复盘看盈亏 | 结果导向，赚了就以为对了 | 观点卡在**决策当时**锁定理由与信心，到期按「过程 × 结果」四格验证 |
| 没有终点 | 问题永远挂着，清单越来越长 | 连续 21 天未复发可标记「已修正」；复发则重新打开并计次 |

## 2. 设计原则

1. **事实层不可编辑，判断层由人填。** 今日完成率、番茄数、打卡、支出来自服务端既有聚合（`task_events` / `focus_sessions` / habits 实例 / `ledger_entries`），复盘页只读展示；用户填的只有判断、归因、对策。
2. **问题必须可数。** 每条问题记录「检查了多少天 / 出现了多少天」，分母诚实：未复盘的日子不算「没犯」。
3. **对策的终点是动作。** 对策文本之外，提供两个落地出口：「明天做一件事」→ 任务（`origin=review`，client_id 幂等）；「变成每天的练习」→ 习惯。次日复盘自动回看昨天承诺的那件事有没有完成（承诺兑现率）。
4. **股市只记决策，不给建议。** 应用不接行情、不算收益、不推荐标的；参考价由用户手填（可空）。验证时问的是「当初的逻辑成立吗」和「结果如何」两件事，分开答。
5. **AI 只写文字，不编数字。** 所有数字（复发率、连续天数、校准、四格计数）由服务端确定性算出；AI 的「镜像」只能引用这些数字做跨日模式陈述。未配置模型或失败 → 如实标注「本次未生成点评」，复盘本身不受影响。
6. **样本不足就如实说。** 信心校准需 ≥ 20 张已验证观点卡；问题复发趋势需 ≥ 7 天检查记录。不够就显示「样本不足，还差 n」，不画曲线。
7. **可撤销、可关闭。** 问题库归档/删除走软删 + 全局 toast 撤销；整个模块可在设置里关闭，历史保留可查。
8. **五分钟上限。** 每日必填项不超过 6 个，其余全部可跳过。拖长了就没人晚上愿意做。

## 3. 用户流程

### 3.1 每日复盘（默认视图）

```
进入「复盘」→ 默认今天，顶部日期条可左右切换补做过去（不可复盘未来）
  ① 今天的事实（自动、只读）：任务 完成/到期、逾期、番茄 个/分钟、习惯 打卡/应打、支出 ¥
  ② 昨天的承诺：「昨天说明天最重要的是 ___」→ 已完成 ✓ / 未完成 ✗（任务状态自动判定）
  ③ 工作（≤ 3 项）：做得好的一件 · 没做好的一件 + 归因（六选一）· 明天最重要的一件事 → 可直接建任务
  ④ 生活（≤ 3 项）：精力 1–5 · 情绪 1–5 · 一句话（可空）
  ⑤ 股市（可整段跳过）：今日观点卡（0–n 张）· 今日有到期待验证的卡则先验证
  ⑥ 问题检查：当前「进行中」的每条问题逐条勾「出现 / 没出现」，可一键「登记新问题」
  → 保存 → 连续天数 +1 → （已配 AI）生成「今日镜像」一句话
```

- 保存后可随时改；修改走 version 乐观并发（409 重拉对齐，与 goals 同约定）。
- 当天未保存不计入连续天数；补做过去某天算补记（`backfilled=1`），连续天数**不**因补记回填（连续天数反映习惯，不反映勤补）。

### 3.2 问题库

- 列表按状态分区：**进行中**（每日检查）/ **观察中**（标记修正后 30 天内复发即打回）/ **已修正** / 归档。
- 每条问题：标题、所属域（工作/生活/股市）、对策、登记日期、最近 30 天复发点阵、出现 n / 检查 m 天、连续未出现天数、复发（重开）次数。
- 操作：编辑对策 · 对策落地（→ 任务 / → 习惯）· 标记已修正（仅当连续未出现 ≥ 21 天时可点，否则按钮置灰并写明还差几天）· 归档（软删可撤销）。
- 登记入口：问题库页「登记问题」，或复盘页「没做好的一件」旁「登记为长期问题」（把归因带过去）。

### 3.3 股市观点

- **待验证**（到期日 ≤ 今天且 verdict=pending）置顶。验证表单两问：
  - 过程：当初的理由现在看成立吗？成立 / 不成立
  - 结果：按当时的方向与时间窗看，结果如何？符合预期 / 不符合 / 无法判断
  - 系统落入四格：**逻辑对·结果对**（可复制）/ **逻辑错·结果对**（运气，最危险）/ **逻辑对·结果错**（接受，不改逻辑）/ **逻辑错·结果错**（改逻辑）
  - 可选：把错误勾连到一条问题（如「追高」「没等确认就加仓」），该卡计入这条问题当日出现。
- **四格矩阵**：近 90 天计数，点格看卡片列表。
- **信心校准**：按声明信心分桶（50–60 / 60–70 / 70–80 / 80–90 / 90–100）对比「结果符合」占比；样本 < 20 张不画。
- **情绪标签分布**：决策当时的情绪（冷静 / FOMO / 恐慌 / 报复 / 无聊）× 结果，给出「FOMO 下决策的 n 张卡中 k 张结果不符」这类可数的陈述。
- 观点卡字段见 §6；**应用不接行情，不算盈亏**，所有价格手填且可空。

### 3.4 周回看

- 每周一自动生成上周的汇总（纯数字，确定性）：复盘 n/7 天、承诺兑现 k/n、精力/情绪折线、各问题出现天数、观点卡验证四格、本周新登记/已修正的问题。
- 已配 AI 时追加「本周最该改的一个问题」一段文字，必须引用上面的数字；未配则该段如实留空。
- 周回看页是只读的，不能在这里填东西（避免变成第二个日报）。

## 4. 复盘框架（表单层契约）

### 4.1 工作域「没做好」的归因六选一

| 归因 | 典型对策方向 |
| --- | --- |
| 计划过满 | 明天只排 3 件 |
| 被打断 | 番茄 + 免打扰时段 |
| 拖延/启动难 | 切成 15 分钟的下一步 |
| 能力/信息缺口 | 列出要问谁、查什么 |
| 依赖他人 | 今天就发催办 |
| 其他 | 自由文本 |

归因是枚举而非自由文本，是为了能跨日统计「我最常栽在哪一类」。

### 4.2 股市观点卡（决策当时填，之后锁定）

| 字段 | 说明 |
| --- | --- |
| 标的 | 代码/名称自由文本 |
| 方向 | 看多 / 看空 / 观望 |
| 理由 | ≤ 140 字，写下当时真正的依据（锁定后不可改，只能追加「事后补注」） |
| 信心 | 50–100 的整数（低于 50 就该是反向观点） |
| 时间窗 | 5 / 10 / 20 / 60 个交易日 → 推出验证到期日（按自然日近似，v1 不接交易日历） |
| 是否实操 | 没动 / 买入 / 加仓 / 减仓 / 卖出 |
| 当时情绪 | 冷静 / FOMO / 恐慌 / 报复 / 无聊 |
| 参考价 | 可空，手填 |

**锁定规则**：保存后理由、信心、方向、时间窗不可改（改了就不是复盘了）。允许软删（撤销）与追加补注。

### 4.3 问题生命周期

```
登记 ──► 进行中（每日检查）──连续 21 天未出现──► 可标记「已修正」──► 观察中（30 天）──► 已修正
                 ▲                                                        │
                 └──────────────── 期间任一天出现 ──────────────────────┘（重开，relapse_count +1）
```

- 21 / 30 为服务端常量，可调。
- 「未复盘的日子」不计入连续未出现天数（分母诚实）。

## 5. 信息架构与界面

### 5.1 导航

侧栏新增「复盘」，位置在「统计」与「记账本」之间。日历仍是默认入口不变。页内四个视图走与目标页/记账本同一套 view-switch：**今日复盘 · 问题库 · 股市观点 · 周回看**。

### 5.2 今日复盘（对应 demo 第一屏）

```
┌───────────────────────────────────────────────────────────────┐
│ ‹ 11月11日 周三 ›   连续复盘 12 天        [今日复盘][问题库][股市][周回看]│
├───────────────────────────────────────────────────────────────┤
│ 今天的事实（来自计划/专注/习惯/账本，只读）                         │
│  任务 5/7 · 逾期 1 · 番茄 6 个 150 分钟 · 习惯 2/3 · 支出 ¥86     │
│ 昨天的承诺 「写完方案第二章」  ✓ 已完成 14:32                        │
├───────────────────────────────────────────────────────────────┤
│ 工作                                                           │
│  做得好的一件  [________________]                                │
│  没做好的一件  [________________]  归因 (计划过满)(被打断)(拖延)…    │
│               └ 登记为长期问题                                   │
│  明天最重要的一件事 [________________]  ☑ 建成明天的任务             │
├───────────────────────────────────────────────────────────────┤
│ 生活    精力 ○○●○○   情绪 ○○○●○   一句话 [________]                 │
├───────────────────────────────────────────────────────────────┤
│ 股市（可跳过）  待验证 1 张 ▸   [+ 记一张观点卡]                     │
├───────────────────────────────────────────────────────────────┤
│ 问题检查（进行中 3 条）                                           │
│  晚上刷手机到 1 点        [出现] [没出现]   近 14 天 ●●○●○○●○○○○●○○   │
│  追高买入                [出现] [没出现]                          │
│  会前不看材料            [出现] [没出现]                          │
├───────────────────────────────────────────────────────────────┤
│ 🤖 今日镜像 · 保存后生成          [保存复盘]                        │
└───────────────────────────────────────────────────────────────┘
```

- 「今天的事实」卡片不可点编辑，悬停有来源说明（「来自番茄专注」）。
- 问题检查一行两个按钮，选中后该行收起成一行小字，保持页面短。
- 未配 AI：镜像区域写「AI 未配置，复盘照常保存」并给设置页入口。

### 5.3 视觉

沿用现有设计体系：浅色默认/可选深色、青绿主色唯一、Noto Sans SC、细线不投影、数字等宽。复盘模块**不引入新颜色语义**：
- 复发点阵用现有热力五阶（`--h0…--h4`，单色从浅到深）；
- 四格矩阵用计数大小 + 文字标签，不用红绿填格；「逻辑错·结果对」只用描边强调（它是最该警惕的一格，但不是「错」）；
- 精力/情绪折线各自一张小图，不做双轴。

## 6. 数据模型与 API

### 6.1 表（migration `0018_review`，含 down；编号顺延，不改已应用文件）

```sql
-- 每日复盘：一人一天一行。事实层不落库（读时从既有表聚合），只落判断层。
CREATE TABLE reviews (
  id            INTEGER PRIMARY KEY AUTOINCREMENT,
  workspace_id  INTEGER NOT NULL REFERENCES workspaces(id),
  user_id       INTEGER NOT NULL REFERENCES users(id),
  day           TEXT NOT NULL,                       -- YYYY-MM-DD，工作区时区
  work_win      TEXT NOT NULL DEFAULT '',
  work_miss     TEXT NOT NULL DEFAULT '',
  work_cause    TEXT NOT NULL DEFAULT ''             -- overplan|interrupted|procrastinate|gap|dependency|other|''
                CHECK (work_cause IN ('','overplan','interrupted','procrastinate','gap','dependency','other')),
  tomorrow_top1 TEXT NOT NULL DEFAULT '',
  tomorrow_task_id TEXT REFERENCES tasks(id),        -- 「建成明天的任务」后回填；次日据此判定承诺兑现
  energy        INTEGER CHECK (energy BETWEEN 1 AND 5),
  mood          INTEGER CHECK (mood BETWEEN 1 AND 5),
  life_note     TEXT NOT NULL DEFAULT '',
  market_note   TEXT NOT NULL DEFAULT '',
  backfilled    INTEGER NOT NULL DEFAULT 0,          -- 补记不计连续天数
  version       INTEGER NOT NULL DEFAULT 1,
  created_at    TEXT NOT NULL, updated_at TEXT NOT NULL,
  UNIQUE (workspace_id, user_id, day)
);

-- 问题库：复盘的核心实体。
CREATE TABLE review_issues (
  id            INTEGER PRIMARY KEY AUTOINCREMENT,
  workspace_id  INTEGER NOT NULL REFERENCES workspaces(id),
  user_id       INTEGER NOT NULL REFERENCES users(id),
  title         TEXT NOT NULL,
  domain        TEXT NOT NULL CHECK (domain IN ('work','life','market')),
  countermeasure TEXT NOT NULL DEFAULT '',
  status        TEXT NOT NULL DEFAULT 'active'
                CHECK (status IN ('active','observing','resolved')),
  opened_at     TEXT NOT NULL,                       -- YYYY-MM-DD
  resolved_at   TEXT,
  relapse_count INTEGER NOT NULL DEFAULT 0,
  position      INTEGER NOT NULL DEFAULT 0,
  version       INTEGER NOT NULL DEFAULT 1,
  deleted_at    TEXT,
  created_at    TEXT NOT NULL, updated_at TEXT NOT NULL
);
CREATE INDEX idx_review_issues_ws ON review_issues(workspace_id, user_id, status) WHERE deleted_at IS NULL;

-- 每日检查：出现与「没出现」都落行，分母才诚实。
CREATE TABLE review_issue_checks (
  issue_id   INTEGER NOT NULL REFERENCES review_issues(id),
  day        TEXT NOT NULL,
  occurred   INTEGER NOT NULL CHECK (occurred IN (0,1)),
  note       TEXT NOT NULL DEFAULT '',
  source     TEXT NOT NULL DEFAULT 'review' CHECK (source IN ('review','thesis')), -- thesis=观点卡验证时勾连
  created_at TEXT NOT NULL,
  PRIMARY KEY (issue_id, day)
);

-- 股市观点卡：决策当时锁定，到期验证。应用不接行情。
CREATE TABLE market_theses (
  id            INTEGER PRIMARY KEY AUTOINCREMENT,
  workspace_id  INTEGER NOT NULL REFERENCES workspaces(id),
  user_id       INTEGER NOT NULL REFERENCES users(id),
  day           TEXT NOT NULL,
  symbol        TEXT NOT NULL,
  stance        TEXT NOT NULL CHECK (stance IN ('long','short','watch')),
  thesis        TEXT NOT NULL,
  confidence    INTEGER NOT NULL CHECK (confidence BETWEEN 50 AND 100),
  horizon_days  INTEGER NOT NULL CHECK (horizon_days IN (5,10,20,60)),
  review_due    TEXT NOT NULL,                       -- day + horizon（自然日近似）
  action        TEXT NOT NULL DEFAULT 'none' CHECK (action IN ('none','buy','add','reduce','sell')),
  emotion       TEXT NOT NULL DEFAULT 'calm' CHECK (emotion IN ('calm','fomo','fear','revenge','boredom')),
  ref_price     TEXT NOT NULL DEFAULT '',            -- 手填、可空、不参与计算
  verdict       TEXT NOT NULL DEFAULT 'pending'
                CHECK (verdict IN ('pending','verified','unclear')),
  process_ok    INTEGER CHECK (process_ok IN (0,1)),
  outcome_ok    INTEGER CHECK (outcome_ok IN (0,1)),
  verdict_note  TEXT NOT NULL DEFAULT '',
  verified_at   TEXT,
  issue_id      INTEGER REFERENCES review_issues(id),
  postscript    TEXT NOT NULL DEFAULT '',            -- 事后补注（唯一可追加的文字）
  client_id     TEXT NOT NULL,
  version       INTEGER NOT NULL DEFAULT 1,
  deleted_at    TEXT,
  created_at    TEXT NOT NULL, updated_at TEXT NOT NULL,
  UNIQUE (workspace_id, client_id)
);
CREATE INDEX idx_theses_due ON market_theses(workspace_id, user_id, verdict, review_due) WHERE deleted_at IS NULL;

-- AI 产出：持久化必带 schema 版本；数字不在这里，文字可丢弃。
CREATE TABLE review_ai_notes (
  id             INTEGER PRIMARY KEY AUTOINCREMENT,
  workspace_id   INTEGER NOT NULL REFERENCES workspaces(id),
  user_id        INTEGER NOT NULL REFERENCES users(id),
  kind           TEXT NOT NULL CHECK (kind IN ('daily_mirror','weekly')),
  period_start   TEXT NOT NULL,                      -- 日镜像=当天；周=周一
  schema_version INTEGER NOT NULL,
  content        TEXT NOT NULL,                      -- JSON
  model          TEXT NOT NULL DEFAULT '',
  status         TEXT NOT NULL CHECK (status IN ('ok','error','skipped')),
  created_at     TEXT NOT NULL,
  UNIQUE (workspace_id, user_id, kind, period_start)
);
```

复盘、问题、观点卡都带 `user_id`：多人工作区里复盘是**个人账本**，只读自己的，不做成员间比较（沿用冷镜/青圃条款）。

### 6.2 派生指标（确定性 SQL，不落库，读时算）

| 指标 | 定义 |
| --- | --- |
| 连续复盘天数 | 从今天（或昨天）往前数 `backfilled=0` 的连续 `reviews` 行 |
| 承诺兑现率 | 近 30 天 `tomorrow_task_id` 非空的行中，该任务在次日工作区时区内 `completed` 的占比 |
| 问题复发率 | 近 14/30 天 `review_issue_checks` 中 `occurred=1` 天数 / 检查天数 |
| 连续未出现 | 从最近一次检查往前数 `occurred=0` 的连续检查天数（跳过未检查日，不中断也不累加） |
| 可标记修正 | status=active 且连续未出现 ≥ 21 |
| 四格计数 | 近 90 天 `verdict='verified'` 按 `(process_ok, outcome_ok)` 分桶 |
| 信心校准 | 已验证卡按 confidence 分 5 桶，各桶 `outcome_ok` 占比；总数 < 20 返回 `insufficient_data` |
| 情绪 × 结果 | 已验证卡按 emotion 分桶的 `outcome_ok` 占比 |
| 归因分布 | 近 30 天 `work_cause` 非空的计数 |

### 6.3 API（成员级，归属校验同 goals；只读自己的行）

| 方法 & 路径 | 说明 |
| --- | --- |
| `GET /api/workspaces/{wid}/reviews/day?date=` | 当天复盘（可空）+ 事实层（服务端聚合）+ 昨天承诺判定 + 进行中问题及其当天检查 + 当天到期待验证观点卡 |
| `PUT /api/workspaces/{wid}/reviews/day?date=` | upsert 当天复盘（version 乐观并发，409 不带最新值、前端重拉）；body 可带 `checks:[{issueId,occurred}]` 同事务落检查行；`createTomorrowTask=true` 时事务内建任务（client_id `review-<day>-top1` 幂等）并回填 `tomorrow_task_id`；date 晚于今天 400；早于今天自动 `backfilled=1` |
| `GET /api/workspaces/{wid}/reviews/issues?status=` | 问题库（含最近 30 天检查点阵、派生指标） |
| `POST /api/workspaces/{wid}/reviews/issues` | 登记问题 |
| `PATCH /api/workspaces/{wid}/reviews/issues/{iid}` | 改标题/对策/域（409 同上） |
| `POST /api/workspaces/{wid}/reviews/issues/{iid}/resolve` | 标记已修正：服务端复核连续未出现 ≥ 21 否则 409 `not_eligible`（带还差天数）；成功转 observing |
| `POST /api/workspaces/{wid}/reviews/issues/{iid}/to-task` / `to-habit` | 对策落地：建任务（`origin=review`，client_id 幂等）/ 建习惯（复用 habits API 语义） |
| `POST /api/workspaces/{wid}/reviews/issues/delete` / `restore` | 软删 + 撤销（批量单事务） |
| `GET /api/workspaces/{wid}/reviews/theses?status=pending\|verified&from=&to=` | 观点卡列表 + 四格/校准/情绪聚合 |
| `POST /api/workspaces/{wid}/reviews/theses` | 记观点卡（client_id 幂等，服务端算 `review_due`） |
| `POST /api/workspaces/{wid}/reviews/theses/{tid}/verify` | 验证 `{processOk, outcomeOk\|null, note, issueId?}`；`outcomeOk=null` → `unclear`；带 issueId 时同事务写 `review_issue_checks(source='thesis')` |
| `PATCH /api/workspaces/{wid}/reviews/theses/{tid}` | 仅允许改 `postscript` / `ref_price`，其余字段改动 400 `locked` |
| `POST /api/workspaces/{wid}/reviews/theses/delete` / `restore` | 软删 + 撤销 |
| `GET /api/workspaces/{wid}/reviews/week?start=YYYY-MM-DD` | 周回看数字（确定性）+ 已生成的 AI 周评（可空） |
| `POST /api/workspaces/{wid}/reviews/ai/mirror?date=` / `.../ai/weekly?start=` | 触发 AI 文字（走技能注册表，见 §7）；未配置返回 `skipped` 而非 5xx |

### 6.4 幂等与并发

- 复盘 `UNIQUE(workspace_id, user_id, day)` + version；检查行主键 `(issue_id, day)` 重复提交覆盖同值。
- 观点卡 `client_id` 幂等；验证是一次性的——已验证的卡再次 verify 返回 409 `already_verified`（复盘不允许改口）。
- 「建成明天的任务」client_id 确定性，重复保存不产生第二条任务；用户在清单里删了该任务，复盘行保留 `tomorrow_task_id`，次日承诺判定按「未完成」计并如实标注「任务已删除」。
- 状态流转（active → observing → resolved / 重开）在单事务内复核派生指标，避免前端按旧数据点按钮。

## 7. AI 接入

- 两个技能注册进既有注册表（`skills/review-mirror/`、`skills/review-weekly/`，各含 `skill.json` + `prompt.md`）。**注册表是唯一事实来源，适配器不按技能名分支**；技能无状态、互不调用。
- 输入全部由服务端 context provider 组装：当天复盘判断层 + 事实层数字 + 问题库派生指标 + 近 30 天归因分布 + 观点卡四格/情绪聚合。模型只看到数字与短文本，不看任务正文以外的内容。
- Prompt 层契约（写死）：

| 规则 | 说明 |
| --- | --- |
| 立场 | 镜子，不是教练。不鼓励、不安慰、不评价人格 |
| 只引数字 | 每句陈述必须引用输入里的一个数字或枚举值；不得出现输入中没有的事实 |
| 一天一句 | 日镜像 ≤ 60 字，一句话；周评 ≤ 200 字、必须点名**一个**最该改的问题并说明依据 |
| 不给投资建议 | 股市部分只能谈决策过程（信心 / 情绪 / 验证），禁止谈标的、价格、方向 |
| 不编对策 | 对策由用户写；AI 最多指出「这条问题还没有对策」 |

- 输出 validator：JSON schema 校验 + 数字一致性抽检（文字里出现的整数必须在输入数字集合中）；不通过落 `status=error`、文字不展示。
- 未配置模型：复盘、问题库、观点卡全部可用；镜像/周评位置如实写「AI 未配置」。凭据三不变式原样适用：配置行优先、可撤销、密钥绝不进前端。

## 8. 里程碑拆分

| 里程碑 | 内容 | 验收 |
| --- | --- | --- |
| M1 | 复盘表 + 事实层聚合 + 今日复盘页（工作/生活/承诺回看/建明日任务）+ 连续天数 | 两个浏览器会话同一天复盘一致；重启后仍在；未来日期 400；补记不增连续天数；重复保存不产生第二条明日任务 |
| M2 | 问题库 + 每日检查 + 生命周期（21/30 天、重开计次）+ 对策落地（任务/习惯）+ 软删撤销 | 同一数据库重算两次派生指标完全一致；连续未出现 < 21 时 resolve 回 409；观察期出现自动重开 |
| M3 | 观点卡：记卡/锁定/到期待验证/四格/情绪聚合；校准 `insufficient_data` 护栏；验证勾连问题 | 已验证卡再验证 409；锁定字段 PATCH 400；< 20 张不出校准图 |
| M4 | 周回看（数字层）+ 两个 AI 技能（镜像/周评）+ validator + 设置页模块开关 | AI 未配置时全部功能可用且位置如实标注；文字层数字与输入不一致时被 validator 拦下 |

## 9. 风险与开放问题

1. **复盘变成负担。** 缓释：必填 ≤ 6 项、股市段整段可跳、问题检查一行两键；上线后看 14 天留存，若 < 40% 再砍字段，不加功能。
2. **问题库膨胀。** 缓释：进行中问题上限 7 条（超出需先归档或修正一条），迫使聚焦；上限为服务端常量。
3. **股市验证的主观性。** 「逻辑是否成立」是自评，可能自我宽恕。缓释：理由在决策时锁定、验证时原样展示；情绪标签与信心是当时填的，不能事后改。接受残余主观性——本模块目标是修正过程，不是统计收益。
4. **交易日历。** v1 时间窗按自然日近似，到期日可能落在周末；验证按「到期日 ≤ 今天」判定，不影响正确性，只影响精确度。P2 接交易日历（本地静态表，不接外部服务）。
5. **与冷镜的重叠。** 冷镜从数据里找偏差，复盘让用户自己登记；两者可能指向同一问题。v1 不打通；P2 允许把冷镜 finding「登记为问题」（单向，冷镜不读问题库）。
6. **与每日记录的重叠。** 记录是「发生了什么」，复盘是「怎么看、改什么」。P2 复盘页可拉当天时间线作参考（只读）。
7. **AI 成本。** 日镜像 1 次/天、周评 1 次/周，量小；失败只损失文字层。
8. **隐私。** 股市观点与情绪是敏感个人数据：严格 `user_id` 隔离、跨工作区按不存在处理、导出需用户触发（P2，纯文本/CSV 本机生成，不外发）。

## 10. Demo 说明

`design/daily-review-demo.html`：单文件、无依赖，浏览器直接打开。数据为内嵌示例（2026-11-11 及之前约 30 天）。演示：

- 今日复盘：事实层只读卡、昨日承诺判定、工作/生活表单、归因六选一、「登记为长期问题」、问题检查一行两键与收起、保存后连续天数与「今日镜像」（示例文字，标明为示例）。
- 问题库：进行中/观察中/已修正分区、30 天复发点阵（热力五阶）、连续未出现天数、修正按钮按 21 天门槛置灰并显示还差几天、对策落地按钮、归档撤销 toast。
- 股市观点：待验证卡的两问验证、四格矩阵实时计数、信心校准的「样本不足」态、情绪 × 结果统计、记卡表单与锁定提示。
- 周回看：复盘出勤、承诺兑现、精力/情绪两张单序列折线、问题出现天数条、周评位置（示例/未配置两态切换）。

样式对齐现有设计体系（浅色默认、青绿主色唯一、Noto Sans SC、热力五阶复用）。demo 中所有保存都只改页面内状态，不代表任何持久化能力。
