import test from "node:test";
import assert from "node:assert/strict";
import {
  DAY,
  TODAY,
  parseInput,
  visible,
  seed,
  loadOn,
  bucketOf,
} from "../src/lib/todo.js";
import {
  validState,
  reorder,
  matches,
  dateValue,
  monthDays,
  removeItems,
  restoreItems,
} from "../src/lib/model.js";

test("delete and undo preserve task contents and IDs", () => {
  const s = seed(),
    removed = [s.items[0]];
  const next = removeItems(
    s.items,
    removed.map((i) => i.id),
  );
  assert.equal(next.length, s.items.length - 1);
  assert.deepEqual(restoreItems(next, removed), s.items);
  assert.ok(s.items.length >= 59);
});
test("bulk completion cleanup can be restored without duplicate IDs", () => {
  const s = seed(),
    done = s.items.filter((i) => i.done);
  const next = removeItems(
    s.items,
    done.map((i) => i.id),
  );
  assert.ok(next.every((i) => !i.done));
  const restored = restoreItems(next, done);
  assert.equal(restored.length, s.items.length);
  assert.deepEqual(restoreItems(restored, done), restored);
});

test("seed state validates", () => assert.equal(validState(seed()), true));
test("empty input is not a task", () => assert.equal(parseInput("  ").t, ""));
test("Chinese relative date, group and priority", () => {
  const p = parseInput("明天 交周报 #工作 !!!");
  assert.equal(p.t, "交周报");
  assert.equal(p.due, TODAY + DAY);
  assert.equal(p.g, "工作");
  assert.equal(p.p, 1);
});
test("English relative date, group and priority", () => {
  const p = parseInput("tomorrow Send report #Work !!");
  assert.equal(p.t, "Send report");
  assert.equal(p.due, TODAY + DAY);
  assert.equal(p.g, "Work");
  assert.equal(p.p, 2);
});
test("day after tomorrow is not tomorrow", () => {
  assert.equal(
    parseInput("day after tomorrow Send report").due,
    TODAY + 2 * DAY,
  );
  assert.equal(parseInput("后天 交周报").due, TODAY + 2 * DAY);
});
test("today filter excludes overdue", () => {
  const v = visible(seed().items, "today", "manual");
  assert.ok(v.every((i) => i.due === TODAY));
  assert.ok(v.length >= 3);
});
test("completed filter and calendar load agree", () => {
  const s = seed();
  assert.ok(visible(s.items, "done", "manual").length >= 1);
  assert.equal(loadOn(s.items, TODAY).did, 1);
});
test("search is case insensitive and group constrained", () => {
  const i = [
    { t: "Review layout", g: "Work" },
    { t: "Review notes", g: "Life" },
  ];
  assert.equal(matches(i, "REVIEW", "Work").length, 1);
  assert.equal(matches(i, "absent", "").length, 0);
});
test("reorder moves both up and down without mutation", () => {
  const a = [{ id: 1 }, { id: 2 }, { id: 3 }];
  assert.deepEqual(
    reorder(a, 1, 2).map((i) => i.id),
    [2, 1, 3],
  );
  assert.deepEqual(
    reorder(a, 3, 1).map((i) => i.id),
    [3, 1, 2],
  );
  assert.deepEqual(
    a.map((i) => i.id),
    [1, 2, 3],
  );
});
test("unknown reorder IDs are ignored", () => {
  const a = [{ id: 1 }, { id: 2 }];
  assert.equal(reorder(a, 9, 1), a);
  assert.equal(reorder(a, 1, 9), a);
});
test("stored state rejects malformed/duplicate IDs", () => {
  assert.equal(validState(null), false);
  assert.equal(validState({ items: "broken" }), false);
  const s = seed();
  s.items[1].id = s.items[0].id;
  assert.equal(validState(s), false);
});
test("stored state rejects invalid dates and priorities", () => {
  const s = seed();
  s.items[0].due = Infinity;
  assert.equal(validState(s), false);
  s.items[0].due = TODAY;
  s.items[0].p = 9;
  assert.equal(validState(s), false);
});
test("stored state rejects nid collisions", () => {
  const s = seed();
  s.nid = 1;
  assert.equal(validState(s), false);
});
test("blank titles are rejected without clobbering user data", () => {
  const s = seed();
  s.items[0].t = "  ";
  assert.equal(validState(s), false);
});
test("month grid has 42 unique local dates and starts Monday", () => {
  for (const m of [0, 1, 8, 11]) {
    const days = monthDays(new Date(2026, m, 1));
    assert.equal(days.length, 42);
    assert.equal(new Set(days).size, 42);
    assert.equal(new Date(days[0]).getDay(), 1);
    assert.equal(new Date(days[41]).getDay(), 0);
  }
});
test("month grid spans year and leap boundaries", () => {
  assert.ok(
    monthDays(new Date(2024, 1, 1)).some(
      (ts) => dateValue(ts) === "2024-02-29",
    ),
  );
  assert.equal(dateValue(monthDays(new Date(2026, 0, 1))[0]), "2025-12-29");
});
test("local date formatting is timezone safe", () => {
  assert.equal(dateValue(+new Date(2026, 8, 22, 0, 0)), "2026-09-22");
  assert.equal(dateValue(0), "");
});
test("completion keeps task identity and updates calendar count", () => {
  const s = seed(),
    i = s.items.find((i) => i.due === TODAY && !i.done);
  const next = s.items.map((x) =>
    x.id === i.id ? { ...x, done: true, at: Date.now() } : x,
  );
  assert.equal(loadOn(next, TODAY).did, 2);
  assert.equal(next.length, s.items.length);
  assert.notEqual(bucketOf(next.find((x) => x.id === i.id)), bucketOf(i));
});
