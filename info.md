# Todo Day · 专注. 任务

一个浅色默认的个人任务规划模板，React 19 + TypeScript + Vite。默认进入日历，也提供 Todo 清单和进度统计，中英文功能一致。参考用户提供的图片采用青绿主色及分类色日程，可选深色主题。

## 给线上 agent 的强制接入指令

使用模板前先读 [AGENTS.md](./AGENTS.md) 和 [FULLSTACK.md](./FULLSTACK.md)。用户要求完整应用时，必须补齐数据库、身份/权限、任务 API、跨设备持久化与真实活动记录；用户需要上传时再接私有对象存储及鉴权/签名/生命周期。后者不是当前已有功能。
前端保持产品文案，不显示“交互 Demo”或开发横幅。不得因此把 mock 当成真实后端：本地状态和存储错误仍须如实反馈，交付时明确未实现项。详细入口、数据字段、迁移和验收条件见 FULLSTACK.md。源码交付必须保留这三份说明。

## 能力边界

任务只保存在本浏览器 localStorage（todo-day-demo-cn，保留旧键以兼容数据），没有登录、后端数据库、云同步或通知。首次使用含59–64条当月任务（随月份天数变化），包括工作、学习、生活、健康、重要事项和收件箱。原七条样本仅在内容/顺序/状态完全未编辑且尚未升级时追加月度样本；修改过的工作区不动，不重置存储。热力图历史为固定种子示例，统计页保留简短来源说明；生产必须替换真实活动数据。
旧 todo-day 存储可迁移读取，不删除旧键；损坏数据不覆盖，保存失败给提示，可导出 CSV 备份。隐私模式/清缓存不保证保留数据。

## 功能与入口

- Todo：全部/今天/未完成/已完成、搜索、分组、排序；今天只包含当天截止任务，逾期单独分组。
- 快速添加：“明天 交周报 #工作 !!!”，支持日期/分组/优先级（!!!高、!!中、!普通），不是AI，不承诺小时、重复任务识别。
- 新建/编辑：标题、日期、分组、优先级；可键盘操作的原生dialog，空标题错误不会关闭弹窗。
- 日历（默认入口）：七列月历、分类色任务、日期选择、当天议程、当天添加；手机以日期+任务数+分类色标展示，具体内容在议程中。颜色、分组文字同时存在，不以颜色作为唯一提示。清单/日历共用一份数据。未排期任务在日历下方可编辑。
- 完成/撤销完成、删除/撤销删除、清理已完成/撤销；只保留最近一批删除的撤销。
- 手动排序支持同日期分组内pointer拖拽与上下按钮；其他排序隐藏拖动入口。
- 统计保留18周热力、分组读数、近7天完成柱图；历史示例数据与清单统计不是同一口径。
- CSV、Canvas海报、打印/PDF保留在统计页。图片/PDF由用户触发，不自动外发。

## 结构

src/App.tsx：共享state、布局、模式、存储与反馈。
src/copy.ts：本地化UI文案。
src/components/CalendarView.tsx：独立月历与日议程。
src/components/Calendar.tsx：小月历日期筛选。
src/components/TaskDialog.tsx：可访问新建/编辑表单。
src/components/List.tsx：列表、勾选、拖拽及键盘排序。
src/components/Heat.tsx / Recap.tsx / Stats.tsx：原有统计。
src/lib/model.js：存储校验、搜索、排序、日期网格纯函数。
src/lib/todo.js：解析、分桶、日期标签。
src/lib/samples.js / palette.js：按当月日期生成样本、固定中英分类色映射。
src/lib/heat.js / kit.js / tool.js：原有引擎，改版未修改。
src/styles/planner.css：本次浅色工作台样式，沿用kit/tool/app基础。

## 字体与许可

Geist / Geist Mono 与 Noto Sans SC 均本地托管，SIL OFL 1.1许可证在public/fonts。Noto Sans SC来自Google Fonts官方，完整中文覆盖（WOFF2约7.4MB，font-display:swap），不依赖外部字体请求。英文数字优先Geist，中文回退Noto Sans SC。没有图库品牌图片或商业字体。

## 开发

npm install
npm run dev
npm test
npm run typecheck
npm run build

浏览器实测与设计依据见仓库 pipeline/runs/todo-day。此改造不包含部署、账号或飞书写回。
