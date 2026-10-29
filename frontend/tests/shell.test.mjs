/* 侧栏外壳契约（2026-10-22 重构，2026-10-26 皮肤功能移除）：
   - 品牌区（TodoDay 图标+标题）从侧栏移除；
   - 「个人工作区」块上移至侧栏最顶、字体放大；
   - 任务分组筛选导航整块移除（分组数据与任务/日历上的色点显示保留，
     见 goals.test.mjs 与 calendar.test.mjs 的保留断言）；
   - 导航栏皮肤已整体移除（预设色板 + 自定义背景图 + 透明度滑杆）。 */
import test from "node:test";
import assert from "node:assert/strict";
import { readFileSync } from "node:fs";

const app = readFileSync(new URL("../src/App.vue", import.meta.url), "utf8");
const taskDialog = readFileSync(new URL("../src/components/TaskDialog.vue", import.meta.url), "utf8");
const copy = readFileSync(new URL("../src/copy.ts", import.meta.url), "utf8");
const css = readFileSync(new URL("../src/styles/planner.css", import.meta.url), "utf8");

test("sidebar has no brand lockup; workspace block sits at the top, enlarged", () => {
  assert.doesNotMatch(app, /brand-lockup/);
  // 工作区块出现在导航之前（侧栏最顶）。
  assert.match(app, /class="planner-sidebar">[\s\S]*?class="workspace-label"[\s\S]*?<nav aria-label="workspace"/);
  // 放大：16px 名称 + 40px 头像。
  assert.match(css, /\.workspace-label \{[\s\S]*?font-size: 16px/);
  assert.match(css, /\.workspace-avatar \{[\s\S]*?width: 40px/);
  assert.doesNotMatch(css, /\.brand-lockup/);
});

test("task group filter nav is gone; group data display stays", () => {
  assert.doesNotMatch(app, /group-navigation/);
  assert.doesNotMatch(app, /C\.projects|C\.allGroups/);
  assert.doesNotMatch(copy, /\bprojects: |\ballGroups: /);
  // 不再按分组筛选，搜索框也已下线：App 不再调用 matches（签名仍在 model.js）。
  assert.doesNotMatch(app, /matches\(/);
  assert.doesNotMatch(app, /task-search/);
});

test("task dialog no longer offers a group input but keeps the value", () => {
  assert.doesNotMatch(taskDialog, /v-model="group"/);
  assert.match(taskDialog, /g: \(props\.item\.g \|\| ""\)\.trim\(\) \|\| C\.personal/);
});

test("sidebar skin feature is fully removed", () => {
  // App：无皮肤状态/换肤入口/背景图层。
  assert.doesNotMatch(app, /skin/i);
  assert.doesNotMatch(app, /sidebar-bg/);
  // 文案：皮肤相关 key 全删。
  assert.doesNotMatch(copy, /\bskin/i);
  // CSS：皮肤选择器与背景层样式全删，侧栏不再为绝对背景层留 relative 锚点。
  assert.doesNotMatch(css, /data-skin|skin-picker|skin-swatch|skin-custom|skin-upload|skin-opacity|skin-caption|sidebar-bg/);
  assert.doesNotMatch(css, /\.planner-sidebar \{\s*position: relative;/);
});
