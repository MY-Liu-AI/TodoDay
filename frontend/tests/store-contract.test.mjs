import test from "node:test";
import assert from "node:assert/strict";
import { readFileSync } from "node:fs";

/* store.ts 的写路径契约（源代码级断言，防回归）：
   对话框新建的项 id 为空，必须走 POST /tasks 创建；
   如果对空 id 发 PATCH /tasks/{id}，服务端会 404（2026-10-15 真实故障）。 */

const src = readFileSync(new URL("../src/store.ts", import.meta.url), "utf8");

test("saveTask routes empty-id drafts to the create endpoint", () => {
  assert.match(src, /export async function saveTask\(draft: Item\)/);
  // 空 id 分支必须存在且先于 PATCH 分支
  const fnStart = src.indexOf("export async function saveTask");
  const emptyBranch = src.indexOf("if (!draft.id)");
  const patchCall = src.indexOf("wsPath(`/tasks/${draft.id}`)");
  assert.ok(emptyBranch > fnStart, "saveTask must branch on empty id");
  assert.ok(patchCall > emptyBranch, "PATCH branch must come after the create branch");
  // 空 id 分支里必须发 POST 到集合端点
  const branchBody = src.slice(emptyBranch, patchCall);
  assert.match(branchBody, /wsPath\("\/tasks"\)/);
  assert.match(branchBody, /method: "POST"/);
  assert.match(branchBody, /clientId/);
});

test("saveTask PATCH branch keeps optimistic snapshot + conflict handling", () => {
  const fnStart = src.indexOf("export async function saveTask");
  const body = src.slice(fnStart);
  assert.match(body, /rememberSnapshot\(prev\)/);
  assert.match(body, /handleConflict\(e\)/);
  assert.match(body, /method: "PATCH"/);
});

test("stats refresh after a mutation is forced, not silently skipped", () => {
  const copy = readFileSync(new URL("../src/store.ts", import.meta.url), "utf8");
  const app = readFileSync(new URL("../src/App.vue", import.meta.url), "utf8");
  // refreshStats 的守卫会在 statsLoaded 为真且未 force 时直接返回，
  // 所以完成/编辑任务后的刷新必须带 force，否则热力图一直是旧数据。
  assert.match(copy, /if \(!st\.workspace \|\| \(st\.statsLoaded && !force\)\) return;/);
  const bare = copy.match(/if \(st\.statsLoaded\) refreshStats\(\);/g) || [];
  assert.equal(bare.length, 0, "mutation-time refreshStats must pass force=true");
  const forced = copy.match(/if \(st\.statsLoaded\) refreshStats\(true\);/g) || [];
  assert.equal(forced.length, 2, "toggleTask and saveTask both force-refresh");
  assert.match(app, /refreshStats\(true\)/);
});
