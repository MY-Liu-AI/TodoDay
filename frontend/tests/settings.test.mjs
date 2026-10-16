import test from "node:test";
import assert from "node:assert/strict";
import { readFileSync } from "node:fs";

/* 设置页（企业微信助手 + AI 凭证）的源码契约。
   核心约束来自 AGENTS.md：密钥不得进入前端代码或浏览器存储。
   前端只能是「写入通道」—— 服务端回的是掩码视图，前端无从泄漏。 */

const store = readFileSync(new URL("../src/store.ts", import.meta.url), "utf8");
const view = readFileSync(new URL("../src/components/SettingsView.vue", import.meta.url), "utf8");
const app = readFileSync(new URL("../src/App.vue", import.meta.url), "utf8");
const copy = readFileSync(new URL("../src/copy.ts", import.meta.url), "utf8");

test("store exposes load/save actions and a masked view type", () => {
  assert.match(store, /export async function loadAssistantConfig\(\)/);
  assert.match(store, /export async function saveAssistantConfig\(input: AssistantConfigInput/);
  assert.match(store, /export interface AssistantConfigView/);
  // 掩码视图只有「是否已设置」的布尔值，没有任何存密钥的字段。
  const viewDef = store.slice(store.indexOf("export interface AssistantConfigView"), store.indexOf("export interface AssistantConfigInput"));
  assert.match(viewDef, /botSecretSet: boolean/);
  assert.match(viewDef, /kimiApiKeySet: boolean/);
  assert.match(viewDef, /running: boolean/, "掩码视图应带运行状态");
  assert.doesNotMatch(viewDef, /botSecret: string|kimiApiKey: string/, "掩码视图不应有密钥字符串字段");
});

test("assistant input no longer carries enabled; start/stop own the switch", () => {
  const inputDef = store.slice(store.indexOf("export interface AssistantConfigInput"), store.indexOf("export interface AssistantTestResult"));
  assert.doesNotMatch(inputDef, /enabled\s*:\s*boolean/, "保存配置不应再携带 enabled 字段");
  assert.match(inputDef, /clearBotSecret\?: boolean/);
  assert.match(inputDef, /clearKimiApiKey\?: boolean/);
});

test("save goes to PUT /assistant and clears on logout", () => {
  const fn = store.slice(store.indexOf("export async function saveAssistantConfig"));
  assert.match(fn, /wsPath\("\/assistant"\)/);
  assert.match(fn, /method: "PUT"/);
  assert.match(store, /registerLogoutHook\(clearAssistantConfig\)/);
});

test("secrets never touch localStorage", () => {
  // store 里所有 localStorage 写入都应是偏好项，不含密钥。
  const writes = [...store.matchAll(/localStorage\.setItem\(([^,]+),/g)].map((m) => m[1]);
  for (const key of writes) {
    assert.doesNotMatch(key, /secret|apiKey|kimi|bot/i, `localStorage 不应写入密钥相关键：${key}`);
  }
  assert.doesNotMatch(view, /localStorage|sessionStorage/, "设置页不应触碰浏览器存储");
});

test("settings view uses password inputs and never prefills secrets", () => {
  const pwInputs = view.match(/type="password"/g) || [];
  assert.equal(pwInputs.length, 2, "应有两个密码输入框（BotSecret 与 Kimi Key）");
  // 从服务端回填时密钥字段必须留空。
  // 锚点用 "onMounted(async" 而不是 "onMounted"：后者会先匹配到 import 行，
  // 把切片切成空的。
  const fill = view.slice(view.indexOf("function fillFromServer"), view.indexOf("onMounted(async"));
  assert.match(fill, /botSecret\.value = ""/);
  assert.match(fill, /kimiApiKey\.value = ""/);
  // 保存时只在用户确实输入了才带上密钥字段。
  const submit = view.slice(view.indexOf("function buildInput"), view.indexOf("async function submit"));
  assert.match(submit, /if \(secret\) input\.botSecret = secret/);
  assert.match(submit, /if \(key\) input\.kimiApiKey = key/);
});

test("settings view surfaces encryption-missing and owner-only states", () => {
  assert.match(view, /encryptionMissing/);
  assert.match(view, /ownerOnly/);
  assert.match(view, /C\.assistantEncryptionMissing/);
  assert.match(view, /C\.assistantOwnerOnly/);
  assert.match(view, /C\.assistantKeyLost/, "必须提示主密钥丢失不可恢复");
});

test("settings page is wired into App with a gear icon", () => {
  assert.match(app, /ref<"tasks" \| "focus" \| "stats" \| "settings">\("tasks"\)/);
  assert.match(app, /import SettingsView from "\.\/components\/SettingsView\.vue"/);
  assert.match(app, /<SettingsView v-else-if="page === 'settings'" \/>/);
  assert.match(app, /page = 'settings'/);
  assert.match(app, /<Icon name="gear" \/>/);
  // 「新建任务」按钮只在任务页出现。
  assert.match(app, /v-if="page === 'tasks'"\s+class="primary-button new-task"/);
});

test("copy has the settings strings", () => {
  for (const key of ["settings", "settingsSubtitle", "assistantTitle", "assistantSaved", "assistantEncryptionMissing", "assistantKeyLost"]) {
    assert.match(copy, new RegExp(`\\b${key}:`), `copy.ts 缺少 ${key}`);
  }
});

test("store never logs or notifies with raw secret values", () => {
  // notify 的参数只能是文案常量，不能把输入拼进去。
  const fn = store.slice(store.indexOf("export async function saveAssistantConfig"), store.indexOf("export function clearAssistantConfig"));
  const notifies = [...fn.matchAll(/notify\(([^)]*)\)/g)].map((m) => m[1]);
  for (const arg of notifies) {
    assert.doesNotMatch(arg, /input\.|botSecret|kimiApiKey/, `notify 不应包含输入值：${arg}`);
  }
});

test("store exposes test/start/stop actions hitting control endpoints", () => {
  assert.match(store, /export async function testAssistantConfig\(/);
  assert.match(store, /export async function startAssistant\(\)/);
  assert.match(store, /export async function stopAssistant\(\)/);
  const body = store.slice(store.indexOf("export async function testAssistantConfig"));
  assert.match(body, /wsPath\("\/assistant\/test"\)/);
  assert.match(body, /wsPath\("\/assistant\/start"\)/);
  assert.match(body, /wsPath\("\/assistant\/stop"\)/);
  assert.match(body, /method: "POST"/);
  // 测试连接要带上表单当前值，而不是只测已保存值。
  assert.match(body, /body: input \?\? \{\}/);
});

test("settings view has run controls and no enabled checkbox", () => {
  assert.doesNotMatch(view, /v-model="enabled"/, "启用复选框已移除，启停改为按钮");
  assert.match(view, /@click="test"/);
  assert.match(view, /@click="start"/);
  assert.match(view, /@click="stop"/);
  assert.match(view, /statusLabel/);
});

test("copy has the run-control strings", () => {
  for (const key of ["assistantTest", "assistantTestOk", "assistantTestFailed", "assistantStart", "assistantStop", "assistantRunning", "assistantStopped", "assistantNotConfigured", "assistantStartNoCredentials", "assistantControlHint"]) {
    assert.match(copy, new RegExp(`\\b${key}:`), `copy.ts 缺少 ${key}`);
  }
});

test("store exposes an LLM test action and view wires a button", () => {
  assert.match(store, /export async function testAssistantLlm\(/);
  assert.match(store, /wsPath\("\/assistant\/test-llm"\)/);
  assert.match(view, /@click="testLlm"/);
  assert.match(view, /kimiConfigured/);
});

test("test buttons send the current form values, not just the saved config", () => {
  // 机器人「测试连接」带当前 botId/secret。
  const bot = view.slice(view.indexOf("async function test()"), view.indexOf("async function start("));
  assert.match(bot, /botId: botId\.value\.trim\(\)/);
  assert.match(bot, /botSecret: botSecret\.value\.trim\(\)/);
  // LLM「测试连接」带当前 key/模型；接口地址不随测试提交（防 SSRF）。
  const llm = view.slice(view.indexOf("async function testLlm"), view.indexOf("</script>"));
  assert.match(llm, /kimiApiKey: kimiApiKey\.value\.trim\(\)/);
  assert.match(llm, /kimiModel: kimiModel\.value\.trim\(\)/);
  assert.doesNotMatch(llm, /kimiBase/);
});

test("start saves the current form before enabling the assistant", () => {
  // 「启用助手」先保存当前表单（静默），再启动 —— 避免「测试通过却忘了保存」。
  const fn = view.slice(view.indexOf("async function start("), view.indexOf("async function stop("));
  assert.match(fn, /saveAssistantConfig\(buildInput\(\), \{ silent: true \}\)/);
  assert.match(fn, /startAssistant\(\)/);
});

test("copy has the LLM test strings and the renamed title", () => {
  for (const key of ["assistantLlmTest", "assistantLlmTesting", "assistantLlmTestOk", "assistantLlmTestFailed", "assistantLlmTestNotConfigured"]) {
    assert.match(copy, new RegExp(`\\b${key}:`), `copy.ts 缺少 ${key}`);
  }
  assert.match(copy, /assistantAiTitle:\s*"LLM API 配置"/);
});
