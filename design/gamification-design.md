# 「青圃」游戏化奖惩机制 — 产品设计方案

> 状态：设计稿（未实施）。设计依据：现有任务/目标/番茄/周期事项/账本全链路均有服务端事件落库，游戏数值全部从这些真实事件派生，不引入任何伪造状态。
>
> **部署形态：独立后端服务**（见 §3.1）。游戏系统不自建行为数据，只读主库事件做派生，主后端**零代码改动**。

## 1. 一句话定位

**你每完成一件真实的事，青圃里的那株苗就长一寸；断一天不枯死，它只是休眠，回来浇水就返青。**

- 「青圃」不是另一个待办系统，而是**现有行为的镜子**：它只读 `task_events` / `focus_sessions` / `habits` 等已有事实，绝不新增用户要维护的实体。
- 奖惩的对象是**动量**而不是**资产**：惩罚永远只清「乘数/势头」，不删历史、不清零等级、不抹掉徽章——这是本项目「不惩罚新的一天」既有先例的直接延伸。

## 2. 设计原则（游戏化专项）

1. **奖励真实行为，数字必须能对账。** 每一分 XP 都能回溯到一条服务端事件；前端只展示，不申报。沿用「构建 ≠ 验收」的红线：游戏界面不许出现任何无事件支撑的数值。
2. **惩罚动量，不惩罚人。** 损失厌恶（Kahneman）说明「失去 100」约等于「得到 200」的痛。所以设计上只让用户失去*乘数和连击势头*——这些东西本来就是「借来的风」，失去时的痛感恰好等于当初得到的爽感，账面永远平衡。绝不允许：清零等级、收回已得徽章、删除历史（这会触发「破罐破摔效应」：既然纪录已毁，干脆放弃）。
3. **为「重新开始」发奖。** 断档后 48 小时内完成第一件事，触发「返青」加成（§5.4）。游戏化最大的失败模式是让用户在断档后永久流失；翻盘机制比防断签更重要。
4. **外在奖励不顶替内在动机（自我决定论）。** 系统是**默认开启、一键整体关闭**的。关闭时冻结计分（不累计、不惩罚、隐藏全部游戏元素）。称号/徽章/生长阶段是唯一的「货币出口」，不卖功能、不卖 AI 配额、不做排行榜压力（多人工作区默认不展示他人数据，见 §10）。
5. **AI 只写文案，不编数字。** 周评级的字母（S/A/B/C）由服务端确定性公式算出；AI（复用技能体系）只负责把数字翻译成一段复盘文字。模型不可达时照常显示评级与数字，文案位置如实标注「本周未生成点评」。
6. **诚实优先于好看。** 番茄放弃就是 0 分并如实显示；逾期完成就是打折；没有「补签卡」——任何改写历史的机制都与本产品的诚实承诺冲突。「温室罩」（护盾）只能预防未来的断签，不能修改过去。

## 3. 核心循环

```
行为（任务/番茄/打卡/里程碑/记账/复盘）
  → 服务端事件落库（现有管道，不变）
    → 奖惩引擎派生 XP 流水（game_rewards，只增不改）
      → 等级 / 乘数 / 连续天数 / 徽章 实时推导
        → 当日结算（工作区时区 23:59）
          → 断签？→ 自动消耗温室罩 / 乘数回落 / 休眠标记
            → 周结算 → 确定性评级 S/A/B/C → 宝箱（XP+罩+徽章进度）
              → AI 周报复盘文案（可选层，失败如实降级）
```

关键：**奖惩引擎是事件的纯函数**。同一批事件重放，结果必须一致（幂等、可重算、可审计）。

### 3.1 部署架构：独立服务 `gamification/`

游戏系统做成**独立的 Go 服务端项目**（建议仓库内 `gamification/` 目录、独立 Go module、独立二进制、独立端口，例如 `:8642`），与主后端进程完全解耦：

```
┌─────────────┐   读（只读连接，WAL 可多进程）   ┌──────────────────┐
│ 主后端       │ ─────────────────────────────→ │ gamification 服务 │
│ (唯一写者)   │   task_events / focus_sessions  │  · 事件游标轮询   │
│   SQLite    │   / habits / sessions          │  · 奖惩派生引擎   │
│  (WAL)      │   / users / workspaces(时区)    │  · 自有 SQLite   │
└─────────────┘                                 │  · 游戏 API :8642│
       ↑                                        └──────────────────┘
   前端 (计划/目标/专注/统计/账本)                      ↑
                                              前端「统计」页游戏区块
                                              （会话 Cookie 直连）
```

三条硬性约定：

1. **主后端零改动。** 游戏服务以**只读模式**打开主库 SQLite 文件（DSN 加 `mode=ro&immutable=0`），靠 WAL 的多进程读能力消费事件；主库始终只有主后端一个写者，不存在写竞争。主后端的任何写路径、迁移、API 都不为游戏系统让路。
2. **游戏数据自立门户。** `game_rewards` / `game_state` / `game_badges_awarded` / `game_stakes` 存在**游戏服务自己的 SQLite 文件**里，迁移由游戏服务自己的迁移器管理（编号独立，从 `0001` 起），主库迁移永远不收游戏表。
3. **身份与授权只读复用。** 游戏服务自己实现 HTTP 中间件：从请求 Cookie 取 session token，**只读查询主库** `sessions`/`workspace_members` 做等值校验（沿用「只做等值查找」的既有约定），越权一律 404/403 与主后端同语义。不反向调用主后端 API、不共享内存、不共享代码（鉴权逻辑允许复制，fail-closed 语义一致）。

消费方式：游戏服务内嵌一个 per-workspace 轮询 goroutine，用**游标（high-water mark）**记录每个工作区已消费到的事件位点，增量拉取 `task_events.id > cursor` 等事件，派生写入自己的库。重启后从游标续传，天然补齐漏算。轮询间隔默认 5s，事件量级（单人待办）下绰绰有余；不做消息队列、不引入第三方组件。

收益与代价：主后端保持精简单一职责；游戏系统可独立启停、独立删库重建（从主库事件全量重放即可恢复，这正是「引擎是纯函数」的回报）。代价是游戏服务对主库**表结构有只读依赖**（见 §12 结构漂移风险与对策）。

## 4. 数值体系

### 4.1 XP 来源表（奖励）

| 行为 | 来源事件 | XP | 说明 |
| --- | --- | --- | --- |
| 完成今日任务 | `task_events.completed`（due=今天） | 10 | 基础分 |
| 完成逾期任务 | 同上（due<今天） | 10×衰减系数（§5.2） | 做了永远比不做好 |
| 完成无日期任务 | 同上（due IS NULL） | 8 | |
| 高优先级加成 | 同上 ×priority | ×1.0/1.2/1.5 | priority 1/2/3 |
| 周期事项当日打卡 | routine 实例 completed | 12 | 习惯是骨架，单价最高 |
| 完成番茄钟 | `focus_sessions.status='completed'` | 15 | 满计划时长才算 |
| 完成里程碑 | 其下任务全部 completed（推导） | 30 | 一次性 |
| 完成目标 | `goals.status→done` | 100 | 一次性，含战略三问的仪式感 |
| 完成周复盘 | 查看周结算卡 / 调用 recap 技能 | 20 | 奖励「回头看」 |
| 返青加成 | 断档后 48h 内首次完成 | ×1.5 | 与乘数叠乘（§5.4） |

计分公式：`XP = 基础 × 优先级系数 × 动量乘数 × 返青系数`，四舍五入。

### 4.2 等级（称号制，纯外观）

累计 XP 定级，公式 `升到 Lv n 需累计 50·n·(n−1)`：

| 等级 | 累计 XP | 称号（中文 / EN） |
| --- | --- | --- |
| 1 | 0 | 萌芽 / Sprout |
| 2 | 100 | 展叶 / Seedling |
| 3 | 300 | 抽枝 / Sapling |
| 4 | 600 | 成株 / Young Tree |
| 5 | 1000 | 繁茂 / Canopy |
| 6 | 1500 | 开花 / Blossom |
| 7 | 2100 | 结实 / Fruiting |
| 8 | 2800 | 成荫 / Shade |
| 9 | 3600 | 深根 / Deep Root |
| 10 | 4500 | 青圃之心 / Heart of the Grove |

10 级后每 +1000 XP 升一级，称号沿用「深根」+ 年轮圈数（如「深根 · Ⅲ」）。称号只展示在统计页与侧栏底部，**不解锁功能**——出口只有外观，防止为刷等级而刷任务。

### 4.3 动量乘数（奖励引擎，也是最大的「罚项」）

- 连续有进展（沿用现有 streak 口径：今天未完成不算断）每 +1 天，乘数 `+0.05`，上限 `×2.0`（第 20 天封顶）。
- 展示在热力格 tooltip：「连续 12 天 · 势头 ×1.60」。
- 断签（见 §5.1）后乘数回落到 `×1.0`，**当天重新开始重新累加**。

### 4.4 温室罩（护盾）——唯一的人工干预道具

- 作用：自动抵消一次断签（苗进入休眠的前一天罩住它）。
- 获取：连续 7 天奖励 1 个；周评级 S/A 各奖 1 个；**持有上限 2 个**（囤积无意义，促使使用）。
- 使用：断签结算时自动消耗（设置里可改为「手动确认」）。只能防未来，不能补过去。

## 5. 惩罚机制（温和、可恢复、全部可对账）

### 5.1 断签：休眠，不是死亡

当日结算（工作区时区 23:59）时若今天零进展：

1. 有温室罩 → 消耗 1 个，连续天数**冻结**（不 +1 也不清零，tooltip 显示「罩护中」）。
2. 无罩 → 连续天数清零（「最长连续」纪录保留，现有 UI 已展示）、动量乘数回落 ×1.0、苗标记「休眠」。
3. 休眠期间的**等级、累计 XP、徽章一枚不动**。

### 5.2 逾期折减：对「拖」的温和定价

- 任务每逾期 1 天，完成时的基础 XP ×0.9，下限 ×0.5（逾期 5 天及以上按 5 天算）。
- 不做负分、不扣钱。拖了仍然值得做，这是底线。
- 撤销完成（reopened）→ 该任务的 XP 流水红字冲销（净额归零），再次完成重新计——与 `task_events` 口径一致，防「勾选刷分」。

### 5.3 番茄放弃：零收益 + 如实展示

- `status='abandoned'` = 0 XP，不动乘数、不动连续天数。
- 专注页近 4 周折线已有放弃记录展示，保持原样——放弃已经是信息，再扣分就是羞辱。

### 5.4 返青：为回归发奖

- 休眠后 48 小时内完成任意计分行为：该次 XP ×1.5，苗直接回到休眠前乘数的一半（下限 ×1.1），徽章「返青」+1 进度。
- 连续天数从 1 重计（历史不可伪造），但乘数的快速回归让「回来」立刻有手感。

### 5.5 （可选进阶）承诺金：把 stakes 接进账本

> 设计参考 stickK。**严格 opt-in，默认关闭，且永不做进引导流程。**

- 用户在目标/周计划上可挂一笔承诺：「本周完成率 < 80%，则自动记一笔 ¥X 的账，分类 = 我自己选（如『请客基金』）」。
- 达成 → 什么都不发生（避免「花钱买平安」道德许可效应）。
- 未达 → 游戏服务在自己的库记一笔 `game_stakes_due`（待入账），前端下次打开应用时经**主后端公开账本 API** 代发入账（`client_id = stake-<id>-<period>` 幂等，分类/金额用用户预设值，note 自动填「承诺金 · {目标名} · 第 {n} 周未达」），并弹全局 toast 告知。账本页顶部如实显示「承诺待入账 ¥X」直到入账完成。
- **游戏服务永远不写主库**——包括账本。代价是承诺金的执行依赖用户再次打开应用；但待入账记录会累积并醒目标注，躲得掉入账躲不掉记录，语义仍然诚实。
- 红线：金额上限 ¥500、只对接账本不触达真实支付、撤销目标即取消承诺、账目照常可软删可撤销。

## 6. 徽章体系

定义表静态维护（代码内常量，带 schema 版本号），按真实事件一次性/累计触发。分四类：

| 类别 | 样例（铜/银/金 三档阈值） |
| --- | --- |
| 行动量 | 完成 50/300/1000 件任务；番茄 20/100/400 个 |
| 连续性 | 连续 7/30/100 天（最长纪录口径）；周期打卡率 80% 持续 4/12/26 周 |
| 深度 | 完成 1/5/20 个目标；拆解里程碑覆盖 100% 的目标 1/3/10 个；完成一次「四象限后全部 IU 清零」 |
| 翻盘与生活 | 「返青」1/5/20 次；记账坚持 30/180/365 天（只数真实手工+邮件入账）；「周复盘」连续 4/12/52 周 |

- **翻盘类徽章是本体系的设计灵魂**：它把「跌倒后回来」标记为成就，而不是把「永不跌倒」标记为唯一美德。
- 徽章一次性授予即永久保留（连断签、删任务都不收回——它记录的是「发生过」）。
- 授予时 toast + 统计页「徽章馆」；未解锁的显示进度（x/y），数据全部来自现有聚合。

## 7. 周结算与评级

每周日 23:59（工作区时区）结算上周：

| 维度 | 权重 | 数据来源（现有管道） |
| --- | --- | --- |
| 计划完成率 | 40% | 当周 due 任务 completed/(due 任务总数)，到期前主动完成加权 |
| 习惯打卡率 | 30% | 周期事项实例完成数/应完成数 |
| 专注投入 | 20% | completed 番茄数（与上周环比，不跨人比较） |
| 回看 | 10% | 周结算卡被查看 / recap 技能调用 |

评级阈值（确定性公式，服务端算）：S ≥ 90，A ≥ 75，B ≥ 55，C < 55。宝箱：S = 60 XP + 温室罩 ×1；A = 40 XP；B = 20 XP；C = 10 XP + 一句鼓励文案（安慰奖不倒扣任何东西）。

**AI 的角色**：复用现有技能体系新增 `recap` 技能（`kind: "llm"`, `context: "weekly_stats"`），把评级、四项分数、亮点任务、逾期 Top3 作为上下文，产出 ≤120 字口语化周评，注册进技能注册表走既有 invoke API。**评级数字不经过 AI**；AI 失败时周结算卡照常显示，点评位显示「本周未生成点评」。

## 8. 视觉与交互落点（沿用现有页面，不新增主导航）

| 位置 | 新增内容 | 备注 |
| --- | --- | --- |
| 统计页顶部 | 等级条 + 称号 + 动量乘数 chip + 温室罩库存 | 与热力/Recap 同页，不抢戏 |
| 热力格 tooltip | 「连续 X 天 · 势头 ×N」 | 数据同源，只是多显示一个派生值 |
| 任务勾选 / 番茄完成 | +XP 轻量浮层（300ms 自动消失） | 可在设置关闭（安静模式） |
| 目标页 | 里程碑完成 → 徽章弹层；目标 done → 青圃升阶全屏动效（可跳过） | 仪式感集中在「大目标」 |
| 专注页 | 苗的生长阶段缩略图（SVG，青绿色系单色素描） | 与 `Icon.vue` 同风格，不引入新色彩 |
| 设置页 | 「青圃」开关 + 安静模式 + 承诺金管理 | 默认开；关闭即冻结并隐藏全部元素 |
| 侧栏底部 | 称号小字 | 弱展示 |

主操作色仍是青绿一色；分类色语义不变。动效遵循 `planner.css` 的 `--ease`。

## 9. 数据模型与 API（草案，独立服务）

### 9.1 迁移（游戏服务自有迁移器，编号从 0001 起，与主库迁移完全无关）

```sql
-- gamification/migrations/0001_game.up.sql
-- XP 流水：只增不改，每条可回溯到主库来源事件。余额冗余存储便于审计。
CREATE TABLE game_rewards (
  id           INTEGER PRIMARY KEY AUTOINCREMENT,
  workspace_id INTEGER NOT NULL,
  user_id      INTEGER NOT NULL,
  source_type  TEXT NOT NULL,   -- task_completed / focus_completed / routine_done / milestone_done / goal_done / weekly_grade / comeback / reversal(冲销)
  source_ref   TEXT NOT NULL,   -- 主库 task_events.id / focus_sessions.id 等（只读依赖，见 §12）
  day          TEXT NOT NULL,   -- YYYY-MM-DD 工作区时区归属日
  delta        INTEGER NOT NULL,
  balance_after INTEGER NOT NULL,
  meta         TEXT NOT NULL DEFAULT '{}',  -- 乘数快照、逾期天数等审计信息
  occurred_at  TEXT NOT NULL,
  UNIQUE (workspace_id, source_type, source_ref)  -- 幂等：同一来源事件只计一次
);

-- 消费游标：每个工作区已消费到主库事件的位点，重启续传 / 重放重建全靠它。
CREATE TABLE game_cursors (
  workspace_id   INTEGER PRIMARY KEY,
  last_event_id  INTEGER NOT NULL DEFAULT 0,
  last_focus_id  INTEGER NOT NULL DEFAULT 0,
  updated_at     TEXT NOT NULL
);

-- 用户游戏状态：只存「人工干预面」，等级/连续/徽章一律实时推导不落库。
CREATE TABLE game_state (
  workspace_id INTEGER NOT NULL,
  user_id      INTEGER NOT NULL,
  freeze_charges INTEGER NOT NULL DEFAULT 0 CHECK (freeze_charges BETWEEN 0 AND 2),
  freeze_mode  TEXT NOT NULL DEFAULT 'auto' CHECK (freeze_mode IN ('auto', 'manual')),
  enabled      INTEGER NOT NULL DEFAULT 1,
  quiet        INTEGER NOT NULL DEFAULT 0,   -- 安静模式：不弹 XP 浮层
  updated_at   TEXT NOT NULL,
  PRIMARY KEY (workspace_id, user_id)
);

-- 徽章授予记录：一次性事件，永久保留。
CREATE TABLE game_badges_awarded (
  id           INTEGER PRIMARY KEY AUTOINCREMENT,
  workspace_id INTEGER NOT NULL,
  user_id      INTEGER NOT NULL,
  badge_id     TEXT NOT NULL,       -- 定义在代码常量表，含 schema 版本
  tier         INTEGER NOT NULL,    -- 1铜 2银 3金
  awarded_at   TEXT NOT NULL,
  UNIQUE (workspace_id, user_id, badge_id, tier)
);

-- 承诺金（可选模块，默认无行即未启用）
CREATE TABLE game_stakes (
  id           INTEGER PRIMARY KEY AUTOINCREMENT,
  workspace_id INTEGER NOT NULL,
  user_id      INTEGER NOT NULL,
  goal_id      INTEGER NOT NULL,     -- 主库 goals.id（软删后主库保留，此处不级联）
  amount_fen   INTEGER NOT NULL CHECK (amount_fen BETWEEN 100 AND 50000),
  category     TEXT NOT NULL,        -- 用户预设的支出分类（自由文本，同账本约定）
  threshold    INTEGER NOT NULL DEFAULT 80,  -- 完成率阈值 %
  period       TEXT NOT NULL DEFAULT 'weekly',
  active       INTEGER NOT NULL DEFAULT 1,
  version      INTEGER NOT NULL DEFAULT 1,
  created_at   TEXT NOT NULL, updated_at TEXT NOT NULL
);
```

注：游戏库的外键一律不指向主库（跨库 FK 不存在）。`workspace_id`/`user_id`/`goal_id` 只是引用主库整数值，游戏服务每次启动时可选地做一轮轻量「孤儿清理」或干脆保留——展示层再做归属校验。

### 9.2 API（游戏服务自有端口，鉴权在中间件完成）

```
基础地址：http://localhost:8642（环境变量 PORT；生产建议挂在同域反代 /game 路径下以便 Cookie 同站）

GET  /api/workspaces/{wid}/game/state
     → { xp, level, title, multiplier, streak, best_streak, freezes,
         badges: [{id, tier, awarded_at}], pending_badge_count, weekly_grade }
GET  /api/workspaces/{wid}/game/log?day=YYYY-MM-DD&limit=50
     → XP 流水（对账用，时间倒序）
POST /api/workspaces/{wid}/game/settings   { enabled, quiet, freeze_mode, version }  ← version 乐观锁
POST /api/workspaces/{wid}/game/freeze/use { day }                                   ← 手动罩模式
POST /api/workspaces/{wid}/game/stakes     ← 建承诺金（version + client_id）
DELETE /api/workspaces/{wid}/game/stakes/{id}  ← 软删取消
GET  /healthz
```

奖惩引擎本身**无写接口**：前端无法直接申报分数——这是防作弊与「如实」的根本保证。前端调用前先用会话 Cookie 过中间件（只读校验主库 `sessions`，过期/越权与主后端同语义 401/404）。

### 9.3 引擎实现要点

- **读一致性：读前追赶。** 后台 goroutine 每 5s 增量消费事件；同时任何 `GET game/state` / `game/log` 请求进来时，先在请求内同步把该工作区游标追平再作答。单机 SQLite 下追赶是毫秒级，用户体感上「勾完任务切到统计页，分已经在」。游戏库写操作只发生在自己的库，追赶与回答不存在跨库事务问题。
- **幂等重放。** `game_rewards` 的 `UNIQUE(workspace_id, source_type, source_ref)` + 游标双保险：同一事件重复消费不重复计分；把游戏库删空、游标归零，即可从主库事件全量重建——这是验收里的破坏性测试项。
- **口径同源。** 归日逻辑（工作区时区、每任务只认最新事件、completed/reopened 净额）复制自主库 `store/events.go` 的 ActivityCounts 口径并在测试中与其对拍，杜绝「热力显示 5 件、XP 给了 6 件」的分叉。
- **结算兜底。** 每日/每周结算由后台 goroutine 按工作区时区驱动；服务停机错过的结算日，启动后按游标与「上次结算日」补齐。
- **首次启用 backfill。** 新工作区首次消费时从游标 0 全量重放历史事件，老用户直接获得与历史相称的等级，并授予「开拓者」徽章——真实历史，不算造假。

## 10. 多人工作区与隐私

- 所有游戏数据按 (workspace, user) 隔离，跨用户一律按不存在处理（沿用 `requireGoalTx` 同款归属校验）。
- **默认无排行榜、无他人可见数据。**  P2 可考虑「并肩周报」：只展示队友的连续天数与称号（不含完成率/XP），且每人可单独关闭。竞争压力与本产品气质冲突，做成可关的弱功能。

## 11. 里程碑与验收

**M0 — 独立服务骨架**
- `gamification/` 独立 Go module + 自有迁移器（0001_game）+ 自有 SQLite 文件 + `:8642` 端口
- 只读连接主库（`mode=ro` DSN + busy_timeout）、鉴权中间件（复制主后端会话等值校验，fail-closed）、`/healthz`
- 验收：**主后端 `git diff` 为空**（零改动是硬指标）；主库文件在游戏服务运行期间加只读锁，主后端全量测试不受影响

**M1 — 引擎与骨架**
- 游标增量消费（task_events / focus_sessions）+ 读前追赶 + `game_rewards` 派生（含 reopened 冲销、逾期折减）
- `GET state` / `GET log` / settings 接口；统计页等级条 + 热力 tooltip 乘数
- 验收：同一事件重放不产生重复流水；reopen 后净额为零；**破坏性测试：清空游戏库后从主库事件全量重建，重建结果与重建前逐行一致**；两个服务的 `go test ./...` 全绿

**M2 — 动量与结算**
- 每日结算（罩/休眠/乘数回落）、返青加成、周评级与宝箱、徽章常量表与授予
- XP 浮层（可安静模式）、徽章馆
- 验收：断签场景全链路测试（有罩/无罩/48h 内回归）；周评级公式单元测试覆盖阈值边界；服务重启后漏算结算日补齐

**M3 — AI 复盘与承诺金（可选模块）**
- `recap` 技能接入主后端技能注册表（这是主后端侧的唯一新增，走它自己的技能管道）；游戏服务读评级数字、AI 只写文案
- 承诺金：due 记录 + 前端代发入账（主后端账本 API，client_id 幂等）
- 验收：AI 未配置时周结算卡照常显示且如实标注；due 入账与手工账同管道、可撤销；游戏服务全程零主库写入（可用只读文件权限运行做验证）

## 12. 风险与红线

| 风险 | 对策 |
| --- | --- |
| 外在奖励侵蚀内在动机 | 出口只有外观；默认可关；不挂任何真实资源（AI 配额、功能） |
| 为刷分而建垃圾任务 | reopened 冲销 + 重复标题不额外加分 + 徽章阈值按「净完成」计 |
| 断档即流失 | 休眠不死亡 + 返青 ×1.5 + 翻盘徽章，三件套护住回归路径 |
| 口径分叉（游戏 vs 统计） | 单一事实源：只从 task_events/focus_sessions 派生，归日逻辑与 ActivityCounts 对拍测试 |
| **主库结构漂移**（只读依赖） | 游戏服务只依赖稳定核心表（task_events/focus_sessions/users/sessions/workspaces）；消费查询用显式列名，主库加列不破坏、删列/改语义时游戏服务的对拍测试会红——测试就是契约警报 |
| **双进程开一个 SQLite 文件** | 主库已是 WAL；游戏服务恒 `mode=ro` + `busy_timeout=5000`，永不获取写锁；主后端 checkpoint/backup 不受读者影响 |
| 鉴权逻辑复制后两边漂移 | 中间件语义写进双方测试（等值查 sessions、过期 401、越权 404）；漂移时游戏服务宁可 fail-closed（全拒）也不 fail-open |
| 事件消费滞后导致分数延迟 | 读前追赶（§9.3）：进 GET state 先同步追平游标，体感无延迟 |
| 承诺金引发反感或误触 | 默认关、上限 ¥500、撤销即取消、只写账本不碰支付；游戏服务零主库写入，入账经主后端公开 API |
| 多人环境比较压力 | 默认无排行榜；并肩周报 P2 且可关 |
| 动效打扰专注气质 | 浮层 ≤300ms、安静模式、动效可降级为纯文字 |

**不做的事**（写明以免后续误加）：不做补签/改写历史；不做积分兑换真实权益；不做全局排行榜；不做负 XP 让用户「欠债」；不在引导流程里强推游戏开关。
