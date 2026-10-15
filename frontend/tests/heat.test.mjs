import test from "node:test";
import assert from "node:assert/strict";
import { DAY, TODAY, midnight } from "../src/lib/todo.js";
import { DAYS, WEEKS, countsFromActivity, level, streak, best, recent } from "../src/lib/heat.js";

/* 热力统计现在全部来自服务端 task_events 聚合（工作区时区），
   前端只做换算。这里测换算与统计口径：热力、连续天数、近 7 天同源。 */

test("countsFromActivity converts server date keys to local midnights", () => {
  const d = new Date(TODAY);
  const key = [d.getFullYear(), String(d.getMonth() + 1).padStart(2, "0"), String(d.getDate()).padStart(2, "0")].join("-");
  const c = countsFromActivity({ [key]: 3, "not-a-date": 2, "2020-01-01": 0 });
  assert.equal(c[TODAY], 3);
  assert.equal(Object.keys(c).length, 1);
  assert.deepEqual(countsFromActivity(null), {});
});
test("level buckets match the server count ranges", () => {
  assert.equal(level(0), 0);
  assert.equal(level(1), 1);
  assert.equal(level(2), 1);
  assert.equal(level(3), 2);
  assert.equal(level(4), 2);
  assert.equal(level(5), 3);
  assert.equal(level(7), 3);
  assert.equal(level(8), 4);
});
test("streak counts back from today and forgives an empty today", () => {
  const c = {};
  c[TODAY - DAY] = 1;
  c[TODAY - 2 * DAY] = 1;
  assert.equal(streak(c), 2);
  c[TODAY] = 1;
  assert.equal(streak(c), 3);
  const gap = {};
  gap[TODAY] = 1;
  gap[TODAY - 2 * DAY] = 1;
  assert.equal(streak(gap), 1);
});
test("best finds the longest run inside the grid", () => {
  const c = {};
  for (let k = 4; k < 9; k++) c[TODAY - k * DAY] = 2;
  assert.equal(best(c), 5);
  assert.equal(best({}), 0);
});
test("recent sums the trailing window including today", () => {
  const c = {};
  c[TODAY] = 2;
  c[TODAY - DAY] = 1;
  c[TODAY - 6 * DAY] = 4;
  assert.equal(recent(c, 7), 7);
  assert.equal(recent(c, 3), 3);
  assert.equal(recent({}, 7), 0);
});
test("grid spans whole weeks back from Monday", () => {
  assert.equal(DAYS.length, WEEKS * 7);
  assert.equal(DAYS[0], midnight(TODAY - ((new Date(TODAY).getDay() + 6) % 7 + (WEEKS - 1) * 7) * DAY));
  assert.ok(DAYS[DAYS.length - 1] >= TODAY);
  assert.equal(new Date(DAYS[0]).getDay(), 1);
});
