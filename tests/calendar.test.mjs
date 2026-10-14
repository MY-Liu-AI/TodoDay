import test from "node:test";
import assert from "node:assert/strict";
import { readFileSync } from "node:fs";
import { seed, enrichSampleState, TODAY } from "../src/lib/todo.js";
import { monthlySamples } from "../src/lib/samples.js";
import { groupColor } from "../src/lib/palette.js";
import { validState } from "../src/lib/model.js";

test("monthly fixtures cover the month with six stable categories", () => {
  const state = seed();
  assert.ok(state.items.length >= 59 && state.items.length <= 64);
  assert.equal(validState(state), true);
  const samples = state.items.filter(i => i.id >= 8);
  assert.equal(new Set(samples.map(i => new Date(i.due).getDate())).size,
    new Date(new Date(TODAY).getFullYear(), new Date(TODAY).getMonth() + 1, 0).getDate());
  assert.equal(new Set(samples.map(i => groupColor(i.g))).size, 6);
});
test("monthly samples stay in February, including leap years", () => {
  for (const year of [2024, 2025, 2026]) {
    const items = monthlySamples(+new Date(year, 1, 15));
    assert.ok(items.every(i => new Date(i.due).getMonth() === 1));
    assert.equal(Math.max(...items.map(i => new Date(i.due).getDate())), year === 2024 ? 29 : 28);
    assert.ok(items.every(i => new Date(i.due).getHours() === 0));
    assert.equal(items.length + 7, year === 2024 ? 61 : 59);
  }
});
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
const legacy = () => ({ items: seed().items.slice(0, 7), sort: "manual", nid: 8 });
test("untouched legacy samples are enriched once, preserving original objects", () => {
  const before = legacy();
  const after = enrichSampleState(before);
  assert.equal(after.sampleVersion, 2);
  assert.equal(after.items.length, seed().items.length);
  assert.deepEqual(after.items.slice(0, 7), before.items);
  assert.equal(before.items.length, 7);
  assert.equal(enrichSampleState(after), after);
});
test("edited, reordered, deleted and completed workspaces are never reseeded", () => {
  for (const change of [
    s => { s.items[0].t = "My actual task"; },
    s => { s.items[0].done = true; },
    s => { s.items[0].due += 864e5; },
    s => { s.items.reverse(); },
    s => { s.items.pop(); },
    s => { s.nid = 9; },
    s => { s.sort = "due"; },
    s => { s.sampleVersion = 2; },
  ]) {
    const before = legacy();
    change(before);
    assert.equal(enrichSampleState(before), before);
  }
});
test("a cleared workspace stays empty", () => {
  const state = { items: [], sort: "manual", nid: 65 };
  assert.equal(enrichSampleState(state), state);
});
test("calendar landing and clean product metadata remain explicit", () => {
  const app = readFileSync(new URL("../src/App.tsx", import.meta.url), "utf8");
  const html = readFileSync(new URL("../index.html", import.meta.url), "utf8");
  assert.match(app, /useState<"list" \| "calendar">\("calendar"\)/);
  assert.doesNotMatch(html, /demo|后端/i);
  assert.doesNotMatch(app, /C\.demo|demo-banner|demo-card|LOCAL DEMO/);
});
test("source handoff includes database and storage integration instructions", () => {
  const agent = readFileSync(new URL("../AGENTS.md", import.meta.url), "utf8");
  const contract = readFileSync(new URL("../FULLSTACK.md", import.meta.url), "utf8");
  assert.match(agent, /FULLSTACK\.md/);
  for (const marker of ["src/App.tsx", "src/lib/heat.js", "workspace_id", "task_events", "object_key", "localStorage", "npm test"]) {
    assert.ok(contract.includes(marker), marker);
  }
});
