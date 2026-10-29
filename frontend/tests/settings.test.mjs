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
  assert.equal(pwInputs.length, 3, "应有三个密码输入框（BotSecret、Kimi Key、邮箱授权码）");
  // 从服务端回填时密钥字段必须留空。
  const fill = view.slice(view.indexOf("function fillFromServer"), view.indexOf("async function ensureLoaded"));
  assert.match(fill, /botSecret\.value = ""/);
  assert.match(fill, /kimiApiKey\.value = ""/);
  // 保存时只在用户确实输入了才带上密钥字段。
  const submit = view.slice(view.indexOf("function buildInput"), view.indexOf("async function submitAssistant"));
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

test("settings dialogs are opened from the sidebar menu, no settings page", () => {
  // 不再有设置页：页面联合类型里没有 settings，也没有 page = 'settings' 跳转。
  assert.match(app, /ref<"tasks" \| "focus" \| "goals" \| "habits" \| "ledger" \| "stats">\("tasks"\)/);
  assert.doesNotMatch(app, /page === 'settings'/);
  assert.match(app, /import SettingsView from "\.\/components\/SettingsView\.vue"/);
  // 弹框常驻挂载，侧栏菜单经 ref 直接打开。
  assert.match(app, /<SettingsView ref="settingsRef" \/>/);
  for (const which of ["bot", "llm", "mail"]) {
    assert.match(app, new RegExp(`openSettings\\('${which}'\\)`));
  }
  assert.match(app, /<Icon name="gear" \/>/);
  // 「新建任务」按钮在任务页工具条（与视图切换同一行），搜索框已下线。
  assert.match(app, /class="primary-button workspace-cta new-task"/);
  assert.doesNotMatch(app, /task-search/);
});

test("copy has the settings strings", () => {
  for (const key of ["settingsMenu", "assistantTitle", "assistantSaved", "assistantEncryptionMissing", "assistantKeyLost"]) {
    assert.match(copy, new RegExp(`\\b${key}:`), `copy.ts 缺少 ${key}`);
  }
  // 设置页已下线：页面标题/副标题文案随之删除。
  assert.doesNotMatch(copy, /\bsettingsSubtitle:/);
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
  for (const key of ["assistantTest", "assistantTestOk", "assistantTestFailed", "assistantStart", "assistantStop", "assistantRunning", "assistantStopped", "assistantNotConfigured", "assistantStartNoCredentials"]) {
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

test("three config dialogs are exposed and opened via defineExpose", () => {
  // 三个原生 dialog，由 openBot/openLlm/openMail 打开（App 侧栏菜单调用）。
  const dialogs = view.match(/<dialog /g) || [];
  assert.equal(dialogs.length, 3);
  assert.match(view, /defineExpose\(\{ openBot, openLlm, openMail \}\)/);
  for (const fn of ["openBot", "openLlm", "openMail"]) {
    assert.match(view, new RegExp(`async function ${fn}\\(\\)`));
    assert.match(view, new RegExp(`${fn.replace("open", "").toLowerCase()}El\\.value\\?\\.showModal\\(\\)`));
  }
  // 配置视图懒加载：第一次打开弹框才拉取。
  assert.match(view, /async function ensureLoaded\(\): Promise<void>/);
  // 点遮罩关闭。
  assert.match(view, /function onBackdrop\(e: MouseEvent\)/);
  for (const ref of ["botEl", "llmEl", "mailEl"]) {
    assert.match(view, new RegExp(`ref="${ref}"`));
  }
  // 弹框内仍是独立表单：取消 + 主色保存。
  assert.equal((view.match(/type="submit"/g) || []).length, 3);
});

test("long hint paragraphs are gone from the settings UI", () => {
  // 说明性长文案不再渲染（菜单行状态 + 内联反馈已足够）。
  for (const key of [
    "assistantHint", "assistantAiHint", "assistantControlHint",
    "assistantNoKeyField", "assistantClearHint",
    "mailHint", "mailKeywordsHint", "mailAuthLinkHint",
  ]) {
    assert.doesNotMatch(view, new RegExp(`C\\.${key}\\b`), `设置页不应再渲染 ${key}`);
    assert.doesNotMatch(copy, new RegExp(`\\b${key}:`), `copy.ts 应删除 ${key}`);
  }
  // 密钥丢失不可恢复的安全提示保留（AGENTS.md 要求如实告知）。
  assert.match(view, /C\.assistantKeyLost/);
});

/* ── 邮箱账单导入：原生 IMAP（邮箱地址 + 授权码） ─────────────────────────
 * CLI / OAuth 方案已下线：没有 authorize/revoke 端点、没有授权链接轮询、
 * 没有 cliAvailable 提示。凭据经服务端加密落库，前端只回显掩码。 */

test("mail view is a masked IMAP view with no CLI/OAuth leftovers", () => {
  const viewDef = store.slice(store.indexOf("export interface MailConfigView"), store.indexOf("export interface MailConfigInput"));
  assert.match(viewDef, /mailbox: string/);
  assert.match(viewDef, /authCodeSet: boolean/, "掩码视图只回「是否已设置」");
  assert.doesNotMatch(viewDef, /authCode: string|authCodeEnc|cliAvailable|MailAuthView/, "视图不得含凭据字段");
  assert.doesNotMatch(store, /authorizeMail|revokeMail|MailAuthView/);
  // 启动失败映射：无凭据（未填/已撤销）如实提示，没有 CLI 缺失这类错误码。
  const start = store.slice(store.indexOf("export async function startMailImport"));
  assert.match(start, /no_credentials/);
  assert.doesNotMatch(start, /cli_unavailable/);
});

test("mail save input carries credentials with keep/clear semantics", () => {
  const inputDef = store.slice(store.indexOf("export interface MailConfigInput"), store.indexOf("/* 目标"));
  assert.match(inputDef, /mailbox\?: string/);
  assert.match(inputDef, /authCode\?: string/);
  assert.match(inputDef, /clearAuthCode\?: boolean/);
  // 测试连接带表单当前值；留空由服务端回退已存凭据。
  const testFn = store.slice(store.indexOf("export async function testMailConnection"), store.indexOf("export async function startMailImport"));
  assert.match(testFn, /body: input \?\? \{\}/);
});

test("mail dialog collects mailbox + auth code and never prefills secrets", () => {
  // 授权码是 password 输入，永不回显；邮箱地址可编辑回显。
  const mailDlg = view.slice(view.indexOf('ref="mailEl"'));
  assert.match(mailDlg, /v-model="mailAddr"/);
  assert.match(mailDlg, /v-model="mailCode"[\s\S]*?type="password"/);
  const fill = view.slice(view.indexOf("function fillMailFromServer"), view.indexOf("async function ensureLoaded"));
  assert.match(fill, /mailAddr\.value = v\?\.mailbox \?\? ""/);
  assert.match(fill, /mailCode\.value = ""/);
  assert.match(fill, /clearAuthCode\.value = false/);
  // 保存入参：授权码只在确实输入时带上，撤销优先于填写。
  const build = view.slice(view.indexOf("function buildMailInput"), view.indexOf("async function submitMail"));
  assert.match(build, /if \(code\) input\.authCode = code/);
  assert.match(build, /input\.clearAuthCode = true/);
  assert.doesNotMatch(view, /authorize\(|revoke\(|authUrl|cliAvailable|mailAuthPending|syncAuthPoll/);
});

test("copy has the IMAP credential strings and no OAuth/CLI strings", () => {
  for (const key of [
    "mailMailbox", "mailAuthCode", "mailAuthCodeKeep", "mailAuthCodeNew",
    "mailClearAuthCode", "mailNoCredentials",
    "mailTest", "mailStart", "mailStop", "mailSaved",
  ]) {
    assert.match(copy, new RegExp(`\\b${key}:`), `copy.ts 缺少 ${key}`);
  }
  for (const dead of [
    "mailCliMissing", "mailCliInstall", "mailMailboxUnset", "mailAuthorize",
    "mailAuthorizing", "mailReauthorize", "mailAuthorizeFailed", "mailAuthWaiting",
    "mailAuthRequired", "mailRevoke", "mailRevokeFailed", "mailRevokedNotice",
  ]) {
    assert.doesNotMatch(copy, new RegExp(`\\b${dead}:`), `copy.ts 应删除 ${dead}`);
  }
});

/* ── 账单邮件来源：预筛只按发件人（关键词预筛已整体下线） ──
 * 银行发件地址固定，让用户从自己邮箱里发现真实发件人来勾选，
 * 不硬编码银行地址目录；senders 整体写入；不配来源则全部交给 Kimi 判定。 */

test("mail config carries sender allowlist with replace-on-write semantics", () => {
  const viewDef = store.slice(store.indexOf("export interface MailConfigView"), store.indexOf("export interface MailConfigInput"));
  assert.match(viewDef, /senders: string/, "掩码视图应回来源清单（非机密）");
  const inputDef = store.slice(store.indexOf("export interface MailConfigInput"), store.indexOf("/* 目标"));
  assert.match(inputDef, /senders: string/, "保存入参带完整来源清单");
});

test("store exposes discover action hitting the discover endpoint", () => {
  assert.match(store, /export async function discoverMailSenders\(/);
  assert.match(store, /wsPath\("\/mail\/discover"\)/);
  const body = store.slice(store.indexOf("export async function discoverMailSenders"), store.indexOf("export async function startMailImport"));
  assert.match(body, /body: input \?\? \{\}/);
});

test("mail dialog picks senders from discovered list and custom input", () => {
  const mailDlg = view.slice(view.indexOf('ref="mailEl"'));
  assert.match(mailDlg, /@click="discoverSenders"/);
  assert.match(mailDlg, /toggleSender\(d\.from/);
  assert.match(mailDlg, /@click="addCustomSender"/);
  assert.match(mailDlg, /v-model="customSender"/);
  assert.match(mailDlg, /hasSender\(d\.from\)/);
  const fill = view.slice(view.indexOf("function fillMailFromServer"), view.indexOf("async function ensureLoaded"));
  assert.match(fill, /v\?\.senders \?\? ""\)/);
  const build = view.slice(view.indexOf("function buildMailInput"), view.indexOf("async function submitMail"));
  assert.match(build, /senders: senders\.value\.join\("\|"\)/);
});

test("copy has the sender allowlist strings", () => {
  for (const key of [
    "mailSenders", "mailSendersNote", "mailSenderAdd", "mailSenderRemove",
    "mailSenderCustomPlaceholder", "mailSenderDiscover", "mailSenderDiscovering",
    "mailSenderDiscoverCount", "mailSenderDiscoverEmpty", "mailSenderDiscoverFailed",
  ]) {
    assert.ok(copy.includes(key + ":"), `copy.ts 缺少 ${key}`);
  }
});
