import test from "node:test";
import assert from "node:assert/strict";
import { readFileSync } from "node:fs";

/* 番茄专注模块的静态契约：
   - 25 分钟固定；
   - 记录只写服务端，失败时保留待重试（不伪造已统计）；
   - 倒计时按「结束时刻 - 现在」算（后台标签节流不漂）；
   - 统计读数全部来自 /focus/stats，前端不自己累加。 */

const focus = readFileSync(new URL("../src/focus.ts", import.meta.url), "utf8");
const view = readFileSync(new URL("../src/components/FocusView.vue", import.meta.url), "utf8");
const app = readFileSync(new URL("../src/App.vue", import.meta.url), "utf8");

test("pomodoro is exactly 25 minutes", () => {
  assert.match(focus, /export const POMODORO_SEC = 25 \* 60;/);
});

test("timer derives remaining from end timestamp, not from tick count", () => {
  assert.match(focus, /fs\.endsAt - Date\.now\(\)/);
});

test("records go to the server and failures stay retryable", () => {
  assert.match(focus, /\/focus\/sessions/);
  assert.match(focus, /fs\.pending = \{ body, label \}/);
  assert.match(focus, /export function retryPending/);
  assert.match(focus, /clientId: "f_"/);
});

test("completed sessions post the full planned length; abandoned under a minute are dropped", () => {
  assert.match(focus, /record\("completed", POMODORO_SEC\)/);
  assert.match(focus, /if \(elapsed >= 60\) await record\("abandoned", elapsed\)/);
});

test("stats view reads today/week/month from the server model", () => {
  assert.match(focus, /\/focus\/stats\?days=182/);
  for (const k of ["todayAgg", "weekAgg", "monthAgg"]) {
    assert.ok(view.includes(k + "()"), k);
  }
  assert.doesNotMatch(view, /pomodoros\s*\+\+|seconds\s*\+=/);
});

test("focus page is wired into the sidebar between plan and insights", () => {
  assert.match(app, /ref<"tasks" \| "focus" \| "stats">\("tasks"\)/);
  const plan = app.indexOf("page = 'tasks'\"");
  const focusNav = app.indexOf("page = 'focus'\"");
  const stats = app.indexOf("@click=\"goStats\"");
  assert.ok(plan > 0 && focusNav > plan && stats > focusNav, "nav order: plan → focus → stats");
  assert.match(app, /<FocusView v-else-if="page === 'focus'" \/>/);
});

test("focus module clears itself on logout", () => {
  assert.match(focus, /registerLogoutHook\(clearStats\)/);
});
