import test from "node:test";
import assert from "node:assert/strict";
import { readFileSync } from "node:fs";
import { groupColor } from "../src/lib/palette.js";

/* 日历相关的静态保证：分类配色的对比度、跨语言一致性，以及产品元数据
   （默认落在日历、页面上不出现 demo/后端提示）。样本注入测试已随 seed()
   一起移除 —— 生产环境没有合成数据。 */

test("every light and dark event color meets AA text contrast", () => {
  const css = readFileSync(new URL("../src/styles/planner.css", import.meta.url), "utf8");
  const luminance = hex => hex.match(/[0-9a-f]{2}/gi).map(v => parseInt(v, 16) / 255)
    .map(v => v <= 0.04045 ? v / 12.92 : ((v + 0.055) / 1.055) ** 2.4)
    .reduce((sum, v, i) => sum + v * [0.2126, 0.7152, 0.0722][i], 0);
  const pairs = [...css.matchAll(/--event-bg: (#\w+); --event-ink: (#\w+);/g)];
  assert.equal(pairs.length, 12);
  for (const [, bg, fg] of pairs) {
    const a = luminance(bg), b = luminance(fg);
    assert.ok((Math.max(a, b) + 0.05) / (Math.min(a, b) + 0.05) >= 4.5, bg + " / " + fg);
  }
});
test("category palette agrees across languages, with a neutral fallback", () => {
  for (const [cn, en] of [["工作","Work"], ["生活","Life"], ["学习","Learning"], ["健康","Health"], ["重要事项","Milestones"]]) {
    assert.equal(groupColor(cn), groupColor(en));
  }
  assert.equal(groupColor(" WORK "), "cyan");
  assert.equal(groupColor("Custom project"), "slate");
  assert.equal(groupColor("__proto__"), "slate");
  assert.equal(groupColor("constructor"), "slate");
});
test("calendar landing and clean product metadata remain explicit", () => {
  const app = readFileSync(new URL("../src/App.vue", import.meta.url), "utf8");
  const html = readFileSync(new URL("../index.html", import.meta.url), "utf8");
  assert.match(app, /ref<"list" \| "calendar">\("calendar"\)/);
  // 启动必须调用 initSession()，否则 st.ready 永远为 false，页面空白。
  assert.match(app, /import\s*\{[^}]*\binitSession\b[^}]*\}\s*from\s*["']\.\/store["']/s);
  assert.match(app, /\binitSession\(\)/);
  assert.doesNotMatch(html, /demo|后端/i);
  assert.doesNotMatch(app, /C\.demo|demo-banner|demo-card|LOCAL DEMO/);
});
test("source handoff includes database and storage integration instructions", () => {
  const agent = readFileSync(new URL("../../AGENTS.md", import.meta.url), "utf8");
  const contract = readFileSync(new URL("../../FULLSTACK.md", import.meta.url), "utf8");
  assert.match(agent, /FULLSTACK\.md/);
  for (const marker of ["src/App.vue", "workspace_id", "task_events", "localStorage", "go test", "npm test"]) {
    assert.ok(contract.includes(marker), marker);
  }
});
