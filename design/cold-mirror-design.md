# 「冷镜」冷酷数据审计 + 反向苏格拉底对练 — 产品设计方案

> 状态：设计稿（未实施）。
>
> 哲学依据：**拒绝过度规划**——清单容器刻意保持极简，复杂度全部让给数据层与审计层。AI 不替你规划人生，只在证据层面纠偏。
>
> 与「青圃」（gamification-design.md）的分工：**青圃奖动量，冷镜找偏差。** 正向反馈归青圃，负向纠偏归冷镜，冷镜自己不发糖。两者数据同源（都只读服务端事件），系统互不调用。

## 1. 一句话定位

**你用极简清单做事、留痕；「冷镜」只读这些痕迹，定期出审计报告：刺破打卡堆出的假性高效，给钝化任务下处置通牒，对产出卡点反向质询并把钝任务切成下一步能做的一小步。**

- 不新增任何用户要维护的实体。用户侧的动作只有三种：**处置**（做/删/留观）、**对练**、**读报告**。
- 一切论断可回溯到服务端事件（`task_events` / `focus_sessions` / `habits` 实例任务 / `goal_milestones`）。界面不许出现无事件支撑的结论，沿用「构建 ≠ 验收」红线。
- 审计是**个人账本**：多人工作区只看自己，不做成员间横向比较（沿用青圃同款条款）。

## 2. 设计原则（冷镜专项）

1. **负向优先。** 审计师只找偏差，不发情绪价值。不庆祝 streak，不安慰「明天加油」。让用户短暂难受是功能，不是 bug。
2. **AI 只写文字，不编数字。** 所有指标由服务端确定性 SQL 算出（纯函数：同一批事件重算结果必须一致）；模型只负责把数字翻译成冷陈述与处置建议。模型不可达时数字照常展示，文字层如实标注「本次未生成点评」（沿用青圃先例）。
3. **审判行为，不审判人。** 措辞对象是「账」：「过去 14 天，这个任务被重新打开 3 次」✓；「你太懒了」✗。
4. **样本不足就如实说。** 新工作区/低活跃期 → 报告状态 `insufficient_data`，写清楚缺什么、攒多久再来，绝不编洞察。
5. **对练的终点是任务，不是感悟。** 每场对练必须产出一个写回清单的新任务，否则不算结束。
6. **可整体关闭。** 沿用青圃「默认开启、一键关闭」先例；关闭后停止生成新报告，历史报告保留可查。
7. **措辞边界 = 数据边界。** 报告只允许说数据支撑的话。v1 数据算不出「你推迟了 6 次」，就只能说「逾期 6 天」——不许用修辞越过指标能力（见 §10.3）。

## 3. 核心循环

```
极简容器执行（任务 / 打卡 / 番茄 / 里程碑）── 事件落库（现有管道，不变）
  → 冷镜·指标引擎：确定性 SQL 派生指标（可重算、可审计、纯函数）
    → 生成审计报告：数字落库为不可变快照，AI 只补冷陈述文字
      → 三类发现：假性高效 / 任务钝化 / 回避模式
        → 处置：做（设明天执行）/ 删（软删可撤销）/ 留观 14 天 / 发起对练
          → 对练：反向苏格拉底，质询「不产出论证」，最多 8 轮
            → 新任务写回清单（client_id 幂等，origin=sparring）
              → 回到容器，执行留痕 → 下期报告的审计对象
```

关键：**指标引擎是事件的纯函数**；报告一旦生成即不可变快照（append-only），不随后续数据改动而重算。连续两期报告的趋势对比才有意义。

## 4. 三类发现（指标定义）

> 所有阈值为服务端常量，可调；下表数值为启动默认值。期间按**工作区时区**的自然周（周一至周日）。

### 4.1 假性高效（打卡幻觉）

**自欺结构**：用低门槛重复事项（习惯实例）维持完成感，真正重要的事一件没碰。30% 的动作贡献了 100% 的满足感。

| 指标 | 定义（v1 数据可支撑） |
| --- | --- |
| 维持感占比 | 周期内 completed 事件中 `habit_id IS NOT NULL` 的任务实例占比 |
| 重要任务完成率 | 周期内 `priority=1` 且非习惯实例的任务完成数 / 到期数 |
| 里程碑静默 | 窗口内挂载的 `goal_milestones` 0 达成且其 `goal_tasks` 0 完成 |

**判定示例**：完成 23 项中 19 项是打卡实例（83%），`priority=1` 完成 0/4 → 出 finding。冷陈述示例：「这周你很忙。忙的 83% 是重复昨天的你。」

### 4.2 任务钝化（清单上的家具）

既不做也不删、躺平占 cognition 的任务。它们让清单失去可信度。

| 指标 | 定义 |
| --- | --- |
| 钝化判定 | 未删除、未完成、创建 ≥ 14 天、窗口内无 completed 事件、未被任何 `goal_tasks` 挂载；按 `updated_at` 最久排序取 top 5 |
| 家具指数 | 当前清单中满足钝化判定的任务总数 / 清单总任务数 |

**处置三选一**（不提醒、逼决策）：
- **做**：服务端把任务 `due_date` 设为下一工作日，关闭 finding；
- **删**：软删（复用既有删除 + 全局 toast 撤销交互）；
- **留观**：关闭 finding 并标记 14 天，期内同任务不再出现在新报告。

### 4.3 回避模式（产出卡点的数据侧写）

| 指标 | 定义 |
| --- | --- |
| 计划漂移 | `completed_at` 的工作区时区日期 > `due_date` 的完成事件占比 |
| 反复症 | 窗口内 `task_events` 中 `reopened` 次数 ≥ 2 的任务 |
| 时间投向 | `focus_sessions` 按任务 `priority` 分桶的时长占比（高优先级任务是否分到时间） |
| 完成周期 | `created` → 首次 `completed` 的时长分布的尾部任务 |

**P2 才做得准的**：精确「推迟次数」需要 `task_events` 记录 due 变化前值（新增 migration 加 `meta` JSON 列，不动旧文件）；v1 用逾期 + reopened + 时长近似，措辞与数据能力严格对齐。

## 5. 反向苏格拉底对练

**正苏格拉底**：老师提问，引导学生自己得出答案。**反向**：你已经在用一整套论证阻止自己产出（「还没准备好」「再读两篇」「今天状态不对」），AI 把这套论证当答辩论文质询，直到真实卡点显形。

### 5.1 触发

- 报告里回避模式 finding 的「对练」按钮；
- 任务卡片菜单「丢给冷镜对练」（不依赖已有报告，随时可用）。

### 5.2 Prompt 层契约（写死在技能 prompt.md）

| 规则 | 说明 |
| --- | --- |
| 立场 | 质询者，不是教练。**禁用鼓励、安慰、庆祝**（「你可以的」类话术禁止） |
| 一轮一问 | 每轮只问一个具体问题，且必须可回答：「卡在哪一句？」✓「你为什么拖延？」✗ |
| 质询论证 | 用户的每个「不做理由」必须被追到底：「需要再读两篇」→「哪两篇？读完产出什么？没有它们第一句写不出来吗？」 |
| 不代办产出 | AI 只切任务、定验收；文章/代码的正文必须用户自己写 |
| 收敛上限 | 最多 8 轮必须收敛为**一个**新任务：15 分钟内可完成、标题以「下一步：」开头、写回清单 |
| 终局三态 | ① 产出新任务（成功）② 用户主动结束（finding 视为留观）③ 轮数耗尽（AI 给出它推断的最小任务，用户可改可拒） |
| 降级 | 模型不可达 → 浮层如实提示「对练不可用」，**不降级成关键词模板聊天** |

## 6. 信息架构与界面

侧栏不加项（v1）：「统计」页内新增「审计」区块/Tab。日历仍是默认入口不变。

```
┌────────────────────────────────────────────┐
│ ‹ 统计 ›        [概览] [审计]                 │
├────────────────────────────────────────────┤
│ 🤖 审计报告 · 10/20 – 10/26 · 已生成  [重新生成]│
│                                            │
│ ⚠ 假性高效                                  │
│ 本周完成 23 项，其中 19 项是打卡实例（83%）。  │
│ priority=1 任务完成 0/4。                    │
│                                            │
│ 🧊 任务钝化（3）                             │
│ ·「整理读书笔记」已躺 34 天                    │
│   [今天做]  [删除]  [留观]                   │
│ ·「问物业」已躺 21 天                        │
│   [今天做]  [删除]  [留观]                   │
│                                            │
│ 🥊 回避模式                                 │
│ 「写文章」第 2 次 reopen，5 个番茄放弃 4 个。  │
│ [对练]  [留观]                              │
├────────────────────────────────────────────┤
│ 样本不足提示（如实显示，不编造）：              │
│ 「本工作区事件不足 30 条，暂无可审计内容。      │
│  正常使用 2 周后再来。」                      │
└────────────────────────────────────────────┘
```

- 每条 finding = 一句冷陈述 + 结构化数字（前端渲染服务端字段，**不信 AI 文字里的数字**）+ 处置按钮。
- 对练为全屏浮层（对齐设置弹框风格）：

```
┌────────── 对练 ·「写文章」──────────────┐
│ 🤖 你说要写 6 周了。上次动笔停在哪一句？  │
│ 💬 开头不知道怎么起。                    │
│ 🤖 假设开头已经写好。第二段你本来打算说什么？│
│ [ 输入… ]                               │
│ 第 3/8 轮 · 终局必须产出一个新任务        │
└──────────────────────────────────────────┘
```

- 视觉沿用现有体系：浅色默认/可选深色、青绿主色唯一、中性色徽章。冷镜不引入新颜色语义（三类发现用图标区分：⚠/🧊/🥊）。
- 空态：无报告 → 「还没有审计报告。正常使用两周后，点「生成审计」。」未配置 AI 时引导卡如实写「AI 未配置：可生成数字报告，冷点评不可用」。

## 7. 数据模型与 API

### 7.1 表（migration `0017_audit`，含 down；编号顺延，不改已应用文件）

```sql
-- 审计报告：数字快照不可变；AI 文字是可丢弃的装饰层。
CREATE TABLE audit_reports (
  id                     INTEGER PRIMARY KEY AUTOINCREMENT,
  workspace_id           INTEGER NOT NULL REFERENCES workspaces(id),
  user_id                INTEGER NOT NULL REFERENCES users(id),
  period_start           TEXT NOT NULL,          -- 工作区时区 YYYY-MM-DD
  period_end             TEXT NOT NULL,
  kind                   TEXT NOT NULL CHECK (kind IN ('weekly','monthly','adhoc')),
  status                 TEXT NOT NULL DEFAULT 'generating'
                         CHECK (status IN ('generating','ok','ok_no_ai','insufficient_data','error')),
  findings_schema_version INTEGER NOT NULL,      -- 持久化 AI 产出必带 schema 版本
  findings               TEXT NOT NULL DEFAULT '[]',  -- JSON：[{id,type,severity,statement,task_ids,metrics:{...},resolve:{state,until}}]
  narrative              TEXT NOT NULL DEFAULT '',    -- AI 冷陈述文字（可空，绝不伪造）
  model                  TEXT NOT NULL DEFAULT '',
  created_at             TEXT NOT NULL,
  UNIQUE (workspace_id, kind, period_start)      -- 同期同类型只生成一次
);
CREATE INDEX idx_audit_ws ON audit_reports(workspace_id, created_at);

-- 对练会话：transcript 留存，用户可回看「我当时怎么被问住的」。
CREATE TABLE sparring_sessions (
  id               INTEGER PRIMARY KEY AUTOINCREMENT,
  workspace_id     INTEGER NOT NULL REFERENCES workspaces(id),
  user_id          INTEGER NOT NULL REFERENCES users(id),
  task_id          TEXT REFERENCES tasks(id),    -- 锚点任务（可空：直接对练一个想法）
  finding_id       TEXT NOT NULL DEFAULT '',     -- 回指报告内 finding.id
  status           TEXT NOT NULL DEFAULT 'active'
                   CHECK (status IN ('active','done','dismissed','error')),
  outcome_task_id  TEXT,                          -- 终局产出任务
  turns            INTEGER NOT NULL DEFAULT 0,
  created_at       TEXT NOT NULL, updated_at TEXT NOT NULL
);

CREATE TABLE sparring_messages (
  id         INTEGER PRIMARY KEY AUTOINCREMENT,
  session_id INTEGER NOT NULL REFERENCES sparring_sessions(id),
  role       TEXT NOT NULL CHECK (role IN ('user','assistant')),
  content    TEXT NOT NULL,
  created_at TEXT NOT NULL
);
```

### 7.2 API（成员级，归属校验同 goals；fail-closed）

| 方法 & 路径 | 说明 |
| --- | --- |
| `POST /api/workspaces/{wid}/audit/reports` | 生成报告：指标同步算完落库（`ok`/`insufficient_data`），AI 文字异步回补（`ok_no_ai` 中间态如实可查） |
| `GET /api/workspaces/{wid}/audit/reports?limit=` | 报告列表（倒序） |
| `GET /api/workspaces/{wid}/audit/reports/{id}` | 单报告（findings JSON + narrative） |
| `POST /api/workspaces/{wid}/audit/reports/{rid}/findings/{fid}/resolve` | 处置：`{action: do\|delete\|keep}`。do=事务内设任务 due=下一工作日；delete=软删（走既有撤销交互）；keep=留观 14 天。幂等：重复同 action 返回同结果 |
| `POST /api/workspaces/{wid}/sparring` | 开会话 `{task_id?}` |
| `POST /api/workspaces/{wid}/sparring/{sid}/messages` | 发一轮，返回 AI 回复 + 会话状态（`active`/`done` + `outcome_task_id`） |
| `GET /api/workspaces/{wid}/sparring/{sid}` | 会话历史（含 transcript） |

### 7.3 幂等与并发

- 报告 `UNIQUE(workspace_id, kind, period_start)` 挡住重试重复生成；重新生成走先软删旧行再插新版（旧快照保留可查——账不可改）。
- 对练产出任务 `client_id = "sparring-" + sid + "-outcome"`，天然幂等；重复提交返回已产出任务。
- 留观语义：新报告生成时，同 `task_id` + 同 type 且留观未过期的 finding 不再出现（v1 不做「恶化破例」，P2）。
- 处置与任务编辑并发：resolve 内对任务的修改走既有 version + 409 语义（与 goals 同款）。

## 8. AI 接入

- 两个新技能注册进既有注册表（`skills/`，`<name>/skill.json` + `prompt.md`）：`audit`（narrative 撰写 + finding 冷陈述）与 `sparring`（对练引擎）。**注册表是唯一事实来源，适配器不得按技能名写分支**（沿用现有不变式）。
- 技能无状态、互不调用：`audit` 只接收服务端算好的指标 JSON，输出文字；`sparring` 每轮接收 transcript + 锚点任务快照，输出一轮质询。
- 触发路径：网页「生成审计」→ 后端生成端点 → 服务端直接调适配器（不经 `/ai/invoke`，因为发起方是服务端任务而非网页一次性调用；通道与凭据解析完全复用，凭据三不变式原样适用：配置行优先、可撤销、密钥绝不进前端）。
- 未配置模型：生成按钮可用（数字报告照出），narrative 位置如实标注；对练入口置灰并说明原因。

## 9. 里程碑拆分

| 里程碑 | 内容 | 验收 |
| --- | --- | --- |
| M1 | 指标引擎（三类发现确定性计算）+ 报告表/生成 API + 统计页「审计」区块（含 `insufficient_data` 护栏） | 同一数据库重算两次，findings 完全一致；无 AI 配置时数字照常、点评位置如实标注空 |
| M2 | 处置闭环：do/delete/keep 落库生效（delete 复用软删 + 全局 toast 撤销）；留观期内不再出现 | 删除可撤销；keep 后 14 天内新报告不含该任务 |
| M3 | 对练：浮层对话、transcript 留存、8 轮上限、终局任务回写（`origin=sparring`，client_id 幂等） | 轮数上限生效；模型不可达如实报错不降级；产出任务正常出现在清单并可完成 |
| M4 | 定时周报（按工作区时区周一 08:00 生成，沿用 mailimport 每日轮询的 goroutine 先例）+ 频率配置（周/月/关） | 重启后按配置自动恢复；关闭后停生成、历史可查 |

## 10. 风险与开放问题

1. **「冷酷」劝退风险。** 缓释：措辞契约（对事不对人）+ 一键整体关闭。上线后若反馈两极，再考虑「冷度」档位（P2，不做预设配置项）。
2. **对练沦为聊天玩具。** 缓释：轮数硬上限、终局必须产任务、每次进对练必须有明确任务锚点、禁用安慰话术写死 prompt。
3. **指标能力 vs 措辞边界的漂移。** v1 算不出「推迟 N 次」。若日后给 `task_events` 加 `meta` 列（记录 due 变化前值，新 migration），措辞才可升级——这是数据能力问题，不是 prompt 问题。
4. **AI 成本。** 周报 1 次/周 + 对练按需（每场 ≤ 8 轮）；narrative 失败只损失文字层，报告本身不失败。
5. **审计与青圃的体感冲突。** 一个奖、一个罚，同屏出现可能精神分裂。v1 接受（两模块各自可关），观察后再定是否在 UI 上互斥展示。
6. **与 quadrant 技能的边界。** quadrant 是一次性优先级建议（番茄前用），冷镜是周期审计 + 对练（番茄后用）。不合并、不互相调用。
7. **每日记录（journal）落地后**，对练输入可增加当日时间线作上下文（「你说要写作，今天 21 条记录里没有一条和写作有关」）——P2，本设计不依赖它。

## 11. Demo 说明（待做）

`design/cold-mirror-demo.html`（后续补）：单文件、无依赖。演示：报告三区（含样本不足态如实展示）、处置三按钮流转、对练浮层多轮收敛、留观 14 天不再出现。数据为内嵌示例，样式对齐现有设计体系。
