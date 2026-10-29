<script setup lang="ts">
/* 三个配置弹框（企业微信助手 / AI 解析 / 邮箱账单导入）：常驻挂载在 App 里，
 * 侧栏「设置」菜单经 ref 直接打开，不经页面跳转。配置视图懒加载——第一次
 * 打开任一弹框才拉取。
 *
 * 密钥输入框永远不回显已存的值 —— 服务端只回「是否已设置」，
 * 前端也就无从泄漏。留空提交表示「保留原值」，删除必须显式勾选清除。 */
import { computed, ref } from "vue";
import { C } from "../copy";
import {
  st,
  loadAssistantConfig,
  saveAssistantConfig,
  testAssistantConfig,
  testAssistantLlm,
  startAssistant,
  stopAssistant,
  loadMailConfig,
  saveMailConfig,
  testMailConnection,
  discoverMailSenders,
  startMailImport,
  stopMailImport,
  type AssistantConfigInput,
  type DiscoveredSender,
  type MailConfigInput,
} from "../store";
import Icon from "./Icon.vue";

const botId = ref("");
const botSecret = ref("");
const kimiApiKey = ref("");
const kimiBase = ref("");
const kimiModel = ref("");
const mailAddr = ref("");
const mailCode = ref("");
/* 账单来源发件人清单（用户勾选/手动添加的来源，保存时 | 拼接）。 */
const senders = ref<string[]>([]);
const customSender = ref("");
const discovered = ref<DiscoveredSender[] | null>(null);
const discovering = ref(false);
const busy = ref(false);
const errBot = ref("");
const errLlm = ref("");
const errMail = ref("");
/* 撤销：留空只表示「不修改」，所以删掉已存密钥要显式勾选。 */
const clearBotSecret = ref(false);
const clearKimiApiKey = ref(false);
const clearAuthCode = ref(false);
/* 测试 / 启停的本地忙碌态与行内反馈。 */
const testing = ref(false);
const controlling = ref(false);
const controlMessage = ref("");
const controlError = ref("");
/* LLM（Kimi）测试连接的本地状态。 */
const testingLlm = ref(false);
const llmMessage = ref("");
const llmError = ref("");
/* 邮箱账单导入的本地状态。 */
const testingMail = ref(false);
const controllingMail = ref(false);
const mailMessage = ref("");
const mailError = ref("");

/* 三个配置弹框。 */
const botEl = ref<HTMLDialogElement | null>(null);
const llmEl = ref<HTMLDialogElement | null>(null);
const mailEl = ref<HTMLDialogElement | null>(null);
function closeDlg(d: HTMLDialogElement | null) {
  d?.close();
}
/* 点遮罩（dialog 背景本身）关闭，框内点击不冒泡到背景。 */
function onBackdrop(e: MouseEvent) {
  if (e.target === e.currentTarget) (e.currentTarget as HTMLDialogElement).close();
}

const view = computed(() => st.assistant);
const mailView = computed(() => st.mail);
const ownerOnly = computed(() => !!st.workspace?.role && st.workspace.role !== "owner");

/* 服务端没有主密钥时，填了密钥也存不进去 —— 提前说清楚，
   而不是等用户点保存才报错。 */
const encryptionMissing = computed(() => view.value !== null && !view.value.encryptionAvailable);

/* 运行状态：由掩码视图的 running（此刻有没有连接）+ configured 驱动。 */
const running = computed(() => !!view.value?.running);
const configured = computed(() => !!view.value?.configured);
const kimiConfigured = computed(() => !!view.value?.kimiApiKeySet);
/* 表单里有了 BotID 就能测试/启动（即便还没保存）：测试与启动都吃表单当前值，
   不必先点「保存」。 */
const hasBotId = computed(() => botId.value.trim() !== "");
/* 邮箱账单导入的派生状态。 */
const mailRunning = computed(() => !!mailView.value?.running);
const mailConfigured = computed(() => !!mailView.value?.configured);
const mailStatusLabel = computed(() => {
  if (!mailConfigured.value) return C.assistantNotConfigured;
  return mailRunning.value ? C.assistantRunning : C.assistantStopped;
});
/* 上次运行状态行。 */
const lastRunLabel = computed(() => {
  const v = mailView.value;
  if (!v || !v.configured || !v.lastRunAt) return C.mailNeverRun;
  const when = v.lastRunAt.slice(0, 16).replace("T", " ");
  if (v.lastRunOk === null) return `${C.mailLastRun}：${when}`;
  const result = v.lastRunOk
    ? `${C.mailRunOk} · ${C.mailImported.replace("{n}", String(v.lastImported))}`
    : `${C.mailRunFailed}${v.lastRunError ? ` · ${v.lastRunError}` : ""}`;
  return `${C.mailLastRun}：${when} · ${result}`;
});
const statusLabel = computed(() => {
  if (!configured.value) return C.assistantNotConfigured;
  return running.value ? C.assistantRunning : C.assistantStopped;
});

function fillFromServer() {
  const v = view.value;
  botId.value = v?.botId ?? "";
  kimiBase.value = v?.kimiBase ?? "";
  kimiModel.value = v?.kimiModel ?? "";
  // 密钥字段一律留空：既不回显，也不预填。
  botSecret.value = "";
  kimiApiKey.value = "";
  // 撤销勾选也要复位，否则第二次保存会把刚填的密钥又删掉。
  clearBotSecret.value = false;
  clearKimiApiKey.value = false;
}

function fillMailFromServer() {
  const v = mailView.value;
  senders.value = (v?.senders ?? "")
    .split("|")
    .map((s) => s.trim())
    .filter(Boolean);
  customSender.value = "";
  discovered.value = null;
  mailAddr.value = v?.mailbox ?? "";
  // 授权码不回显：密钥字段一律留空，撤销勾选复位。
  mailCode.value = "";
  clearAuthCode.value = false;
}

/* 配置视图懒加载：第一次打开弹框才拉取，失败可在弹框内重试。 */
let loadSeq = 0;
async function ensureLoaded(): Promise<void> {
  const seq = ++loadSeq;
  await Promise.all([loadAssistantConfig(), loadMailConfig()]);
  if (seq !== loadSeq) return; // 期间又触发了一次，以最后一次为准
  fillFromServer();
  fillMailFromServer();
}

async function reload() {
  errBot.value = "";
  errLlm.value = "";
  errMail.value = "";
  await ensureLoaded();
}

/* 侧栏「设置」菜单直接打开对应弹框。 */
async function openBot() {
  await ensureLoaded();
  botEl.value?.showModal();
}
async function openLlm() {
  await ensureLoaded();
  llmEl.value?.showModal();
}
async function openMail() {
  await ensureLoaded();
  mailEl.value?.showModal();
}
defineExpose({ openBot, openLlm, openMail });

/* 把表单当前值整理成保存入参。密钥只在用户确实输入时才带上（空串=保留原值），
   撤销勾选优先于填写。两个凭据弹框共用同一个服务端配置对象：非机密字段
   （botId/接口地址/模型）按当前表单原样回传，等价于「不修改保留原值」。 */
function buildInput(): AssistantConfigInput {
  const input: AssistantConfigInput = {
    botId: botId.value.trim(),
    kimiBase: kimiBase.value.trim(),
    kimiModel: kimiModel.value.trim(),
    version: view.value?.version ?? 0,
  };
  // 只在用户确实输入了值时才带上密钥字段 —— 空串会被服务端
  // 理解为「保留原值」，但这里干脆不传，语义更明确。
  const secret = botSecret.value.trim();
  if (secret) input.botSecret = secret;
  const key = kimiApiKey.value.trim();
  if (key) input.kimiApiKey = key;
  // 撤销与填写互斥：勾了清除就以清除为准，避免同一次请求语义打架。
  if (clearBotSecret.value) {
    input.clearBotSecret = true;
    delete input.botSecret;
  }
  if (clearKimiApiKey.value) {
    input.clearKimiApiKey = true;
    delete input.kimiApiKey;
  }
  return input;
}

/* 企业微信 / AI 解析两个弹框的保存：PUT 同一个配置对象。 */
async function submitAssistant() {
  if (busy.value) return;
  errBot.value = "";
  errLlm.value = "";
  busy.value = true;
  try {
    const ok = await saveAssistantConfig(buildInput());
    if (!ok) {
      errBot.value = C.assistantSaveFailed;
      errLlm.value = C.assistantSaveFailed;
      return;
    }
    fillFromServer(); // 清空密钥框、刷新掩码状态
  } finally {
    busy.value = false;
  }
}

/* 邮箱弹框的保存入参：授权码只在用户确实输入时才带上（空串 = 保留原值），
   撤销勾选优先于填写。 */
function buildMailInput(): MailConfigInput {
  const input: MailConfigInput = {
    mailbox: mailAddr.value.trim(),
    senders: senders.value.join("|"),
    version: mailView.value?.version ?? 0,
  };
  const code = mailCode.value.trim();
  if (code) input.authCode = code;
  if (clearAuthCode.value) {
    input.clearAuthCode = true;
    delete input.authCode;
  }
  return input;
}

/* ── 账单来源：勾选（从邮箱发现）+ 手动添加，按小写去重 ── */
function hasSender(from: string): boolean {
  const k = from.trim().toLowerCase();
  return senders.value.some((s) => s.toLowerCase() === k);
}
function toggleSender(from: string, on: boolean) {
  const v = from.trim();
  if (!v) return;
  if (on) {
    if (!hasSender(v)) senders.value.push(v);
  } else {
    senders.value = senders.value.filter((s) => s.toLowerCase() !== v.toLowerCase());
  }
}
function addCustomSender() {
  const v = customSender.value.trim();
  if (!v) return;
  if (!hasSender(v)) senders.value.push(v);
  customSender.value = "";
}
function removeSender(i: number) {
  senders.value.splice(i, 1);
}

/* 从邮箱发现发件人：后端用表单当前值（或已存凭据）做一次只读 IMAP 扫描，
   列出窗口内出现过的来源（带封数与最近主题），供勾选。 */
async function discoverSenders() {
  if (discovering.value || ownerOnly.value) return;
  discovering.value = true;
  mailMessage.value = "";
  mailError.value = "";
  try {
    const r = await discoverMailSenders({
      mailbox: mailAddr.value.trim(),
      authCode: mailCode.value.trim(),
    });
    if (!r) return;
    if (r.ok) discovered.value = r.senders ?? [];
    else mailError.value = `${C.mailSenderDiscoverFailed}${r.message ? `：${r.message}` : ""}`;
  } finally {
    discovering.value = false;
  }
}

/* 邮箱弹框的保存。 */
async function submitMail() {
  if (busy.value) return;
  errMail.value = "";
  busy.value = true;
  try {
    const ok = await saveMailConfig(buildMailInput(), { silent: true });
    if (!ok) errMail.value = C.assistantSaveFailed;
    fillMailFromServer(); // 清空授权码框、复位撤销勾选
  } finally {
    busy.value = false;
  }
}

/* 测试连接：做一次真实订阅。测的是表单当前值（含这次新填的 Secret），
   所以先不保存也能验证凭据。测试失败是正常结果（HTTP 仍是 200），
   由返回对象里的 ok/errcode/errmsg 驱动行内反馈；HTTP 层错误已由 store toast。 */
async function test() {
  if (testing.value || controlling.value) return;
  testing.value = true;
  controlMessage.value = "";
  controlError.value = "";
  try {
    const r = await testAssistantConfig({
      botId: botId.value.trim(),
      botSecret: botSecret.value.trim(),
    });
    if (!r) return;
    if (r.ok) {
      controlMessage.value = r.kimiConfigured
        ? C.assistantTestOk
        : `${C.assistantTestOk}${C.assistantTestNoKimi}`;
    } else {
      controlError.value = `${C.assistantTestFailed}：${r.errmsg || r.message || `errcode=${r.errcode ?? "?"}`}`;
    }
  } finally {
    testing.value = false;
  }
}

async function start() {
  if (controlling.value || testing.value) return;
  controlling.value = true;
  controlMessage.value = "";
  controlError.value = "";
  try {
    // 「启动助手」先保存当前表单再启动：测试连接验证的是表单当前值，
    // 启动读的却是已保存值，不先保存就会出现「测试通过却起不来」。
    // 保存静默进行（不弹「已保存」），失败则不再启动。
    if (!(await saveAssistantConfig(buildInput(), { silent: true }))) {
      controlError.value = C.assistantSaveFailed;
      return;
    }
    fillFromServer(); // 密钥已落库，清空密钥框
    if (!(await startAssistant())) controlError.value = C.assistantStartFailed;
  } finally {
    controlling.value = false;
  }
}

async function stop() {
  if (controlling.value || testing.value) return;
  controlling.value = true;
  controlMessage.value = "";
  controlError.value = "";
  try {
    if (!(await stopAssistant())) controlError.value = C.assistantStopFailed;
  } finally {
    controlling.value = false;
  }
}

/* 测试 LLM（Kimi）凭证能否真调通：一次最小调用。测的是表单当前值（含这次
   新填的 Key / 模型），所以先不保存也能验证。失败是正常结果，由返回对象里的
   ok/message 驱动行内反馈；HTTP 层错误已由 store toast。 */
async function testLlm() {
  if (testingLlm.value) return;
  testingLlm.value = true;
  llmMessage.value = "";
  llmError.value = "";
  try {
    const r = await testAssistantLlm({
      kimiApiKey: kimiApiKey.value.trim(),
      kimiModel: kimiModel.value.trim(),
    });
    if (!r) return;
    if (r.ok) {
      llmMessage.value = C.assistantLlmTestOk;
    } else {
      llmError.value = `${C.assistantLlmTestFailed}：${r.message || "未知错误"}`;
    }
  } finally {
    testingLlm.value = false;
  }
}

/* ── 邮箱账单导入 ─────────────────────────────────────────────────── */

/* 测试连接：做一次真实 IMAP 登录。测的是表单当前值（含这次新填的授权码），
   留空则回退已存凭据，所以先不保存也能验证。失败是正常结果（HTTP 仍 200），
   由返回对象里的 ok/message 驱动行内反馈。 */
async function testMail() {
  if (testingMail.value || controllingMail.value) return;
  testingMail.value = true;
  mailMessage.value = "";
  mailError.value = "";
  try {
    const r = await testMailConnection({
      mailbox: mailAddr.value.trim(),
      authCode: mailCode.value.trim(),
    });
    if (!r) return;
    if (r.ok) {
      mailMessage.value = `${C.mailTestOk}${r.mailbox ? `：${r.mailbox}` : ""}`;
    } else {
      mailError.value = `${C.mailTestFailed}：${r.message || "未知错误"}`;
    }
  } finally {
    testingMail.value = false;
  }
}

async function startMail() {
  if (controllingMail.value || testingMail.value) return;
  controllingMail.value = true;
  mailMessage.value = "";
  mailError.value = "";
  try {
    // 与「启动助手」同理：启动读的是已保存值，先静默保存当前表单。
    if (!(await saveMailConfig(buildMailInput(), { silent: true }))) {
      return;
    }
    fillMailFromServer(); // 授权码已落库，清空输入框
    if (!(await startMailImport())) return;
    if (!kimiConfigured.value) mailError.value = C.mailKimiMissing;
  } finally {
    controllingMail.value = false;
  }
}

async function stopMail() {
  if (controllingMail.value || testingMail.value) return;
  controllingMail.value = true;
  mailMessage.value = "";
  mailError.value = "";
  try {
    await stopMailImport();
  } finally {
    controllingMail.value = false;
  }
}
</script>

<template>
  <!-- 三个配置弹框：常驻挂载，由 App 侧栏「设置」菜单经 ref 打开。 -->
  <div>
    <!-- 企业微信助手：机器人凭据 + 测试/启停。 -->
    <dialog ref="botEl" class="task-dialog settings-dialog" aria-labelledby="dlg-bot-title" @cancel="closeDlg(botEl)" @click="onBackdrop">
      <form novalidate @submit.prevent="submitAssistant">
        <div class="dialog-head">
          <div>
            <span class="overline">TODO DAY</span>
            <h2 id="dlg-bot-title">{{ C.assistantTitle }}</h2>
          </div>
          <button type="button" class="icon-button" :aria-label="C.close" @click="closeDlg(botEl)">
            <Icon name="close" />
          </button>
        </div>

        <p v-if="st.assistantLoading && !view" class="dialog-note">{{ C.syncing }}</p>
        <p v-else-if="view === null" class="dialog-note">
          {{ C.assistantLoadFailed }}
          <button class="text-button" @click="reload">{{ C.assistantReload }}</button>
        </p>
        <template v-else>
          <p v-if="ownerOnly" role="alert" class="form-error">{{ C.assistantOwnerOnly }}</p>
          <p v-if="encryptionMissing" role="alert" class="storage-warning">
            {{ C.assistantEncryptionMissing }}
          </p>

          <label class="form-field">
            {{ C.assistantBotId }}
            <input v-model="botId" maxlength="128" :placeholder="C.assistantBotIdPlaceholder" />
          </label>

          <label class="form-field">
            {{ C.assistantBotSecret }}
            <input
              v-model="botSecret"
              type="password"
              maxlength="128"
              autocomplete="new-password"
              :placeholder="view?.botSecretSet ? C.assistantBotSecretKeep : C.assistantBotSecretNew"
              @input="errBot = ''"
            />
          </label>
          <label v-if="view?.botSecretSet" class="switch-row clear-row">
            <input v-model="clearBotSecret" type="checkbox" />
            <span>{{ C.assistantClearBotSecret }}</span>
          </label>

          <div class="control-block">
            <p class="status-line" role="status">
              <span class="status-dot" :class="{ 'is-on': running }"></span>
              {{ statusLabel }}
            </p>
            <div class="control-actions">
              <button class="secondary-button" type="button" :disabled="(!configured && !hasBotId) || testing || controlling || ownerOnly" @click="test">
                {{ testing ? C.assistantTesting : C.assistantTest }}
              </button>
              <button v-if="!running" class="primary-button" type="button" :disabled="(!configured && !hasBotId) || testing || controlling || ownerOnly" @click="start">
                {{ controlling ? C.assistantStarting : C.assistantStart }}
              </button>
              <button v-else class="secondary-button" type="button" :disabled="testing || controlling || ownerOnly" @click="stop">
                {{ controlling ? C.assistantStopping : C.assistantStop }}
              </button>
            </div>
            <p v-if="controlError" role="alert" class="form-error">{{ controlError }}</p>
            <p v-else-if="controlMessage" role="status" class="dialog-note">{{ controlMessage }}</p>
          </div>

          <p class="dialog-note settings-warn">{{ C.assistantKeyLost }}</p>
          <p v-if="errBot" role="alert" class="form-error">{{ errBot }}</p>
        </template>

        <div class="dialog-actions">
          <button class="secondary-button" type="button" :disabled="busy" @click="closeDlg(botEl)">
            {{ C.cancel }}
          </button>
          <span />
          <button class="primary-button" type="submit" :disabled="busy || ownerOnly">
            <Icon name="check" :size="14" />
            {{ busy ? C.assistantSaving : C.assistantSave }}
          </button>
        </div>
      </form>
    </dialog>

    <!-- AI 解析凭证。 -->
    <dialog ref="llmEl" class="task-dialog settings-dialog" aria-labelledby="dlg-llm-title" @cancel="closeDlg(llmEl)" @click="onBackdrop">
      <form novalidate @submit.prevent="submitAssistant">
        <div class="dialog-head">
          <div>
            <span class="overline">TODO DAY</span>
            <h2 id="dlg-llm-title">{{ C.assistantAiTitle }}</h2>
          </div>
          <button type="button" class="icon-button" :aria-label="C.close" @click="closeDlg(llmEl)">
            <Icon name="close" />
          </button>
        </div>

        <p v-if="st.assistantLoading && !view" class="dialog-note">{{ C.syncing }}</p>
        <p v-else-if="view === null" class="dialog-note">
          {{ C.assistantLoadFailed }}
          <button class="text-button" @click="reload">{{ C.assistantReload }}</button>
        </p>
        <template v-else>
          <p v-if="ownerOnly" role="alert" class="form-error">{{ C.assistantOwnerOnly }}</p>
          <p v-if="encryptionMissing" role="alert" class="storage-warning">
            {{ C.assistantEncryptionMissing }}
          </p>

          <label class="form-field">
            {{ C.assistantKimiKey }}
            <input
              v-model="kimiApiKey"
              type="password"
              maxlength="200"
              autocomplete="new-password"
              :placeholder="view?.kimiApiKeySet ? C.assistantKimiKeyKeep : C.assistantKimiKeyNew"
              @input="errLlm = ''"
            />
          </label>
          <label v-if="view?.kimiApiKeySet" class="switch-row clear-row">
            <input v-model="clearKimiApiKey" type="checkbox" />
            <span>{{ C.assistantClearKimiKey }}</span>
          </label>

          <div class="form-pair">
            <label class="form-field">
              {{ C.assistantKimiBase }}
              <input v-model="kimiBase" maxlength="200" :placeholder="C.assistantKimiBasePlaceholder" />
            </label>
            <label class="form-field">
              {{ C.assistantModel }}
              <input v-model="kimiModel" maxlength="100" :placeholder="C.assistantModelPlaceholder" />
            </label>
          </div>

          <div class="control-block">
            <div class="control-actions">
              <button class="secondary-button" type="button" :disabled="!kimiConfigured || testingLlm || ownerOnly" @click="testLlm">
                {{ testingLlm ? C.assistantLlmTesting : C.assistantLlmTest }}
              </button>
            </div>
            <p v-if="llmError" role="alert" class="form-error">{{ llmError }}</p>
            <p v-else-if="llmMessage" role="status" class="dialog-note">{{ llmMessage }}</p>
          </div>

          <p class="dialog-note settings-warn">{{ C.assistantKeyLost }}</p>
          <p v-if="errLlm" role="alert" class="form-error">{{ errLlm }}</p>
        </template>

        <div class="dialog-actions">
          <button class="secondary-button" type="button" :disabled="busy" @click="closeDlg(llmEl)">
            {{ C.cancel }}
          </button>
          <span />
          <button class="primary-button" type="submit" :disabled="busy || ownerOnly">
            <Icon name="check" :size="14" />
            {{ busy ? C.assistantSaving : C.assistantSave }}
          </button>
        </div>
      </form>
    </dialog>

    <!-- 邮箱账单导入：邮箱地址 + IMAP 授权码，凭据经服务端加密落库。 -->
    <dialog ref="mailEl" class="task-dialog settings-dialog" aria-labelledby="dlg-mail-title" @cancel="closeDlg(mailEl)" @click="onBackdrop">
      <form novalidate @submit.prevent="submitMail">
        <div class="dialog-head">
          <div>
            <span class="overline">TODO DAY</span>
            <h2 id="dlg-mail-title">{{ C.mailTitle }}</h2>
          </div>
          <button type="button" class="icon-button" :aria-label="C.close" @click="closeDlg(mailEl)">
            <Icon name="close" />
          </button>
        </div>

        <p v-if="ownerOnly" role="alert" class="form-error">{{ C.assistantOwnerOnly }}</p>

        <label class="form-field">
          {{ C.mailMailbox }}
          <input v-model="mailAddr" maxlength="200" :placeholder="C.mailMailbox" @input="errMail = ''" />
        </label>
        <label class="form-field">
          {{ C.mailAuthCode }}
          <input
            v-model="mailCode"
            type="password"
            maxlength="200"
            autocomplete="new-password"
            :placeholder="mailView?.authCodeSet ? C.mailAuthCodeKeep : C.mailAuthCodeNew"
            @input="errMail = ''"
          />
        </label>
        <label v-if="mailView?.authCodeSet" class="switch-row clear-row">
          <input v-model="clearAuthCode" type="checkbox" />
          <span>{{ C.mailClearAuthCode }}</span>
        </label>

        <div class="form-field">
          <span>{{ C.mailSenders }}</span>
          <div v-if="senders.length" class="sender-chips">
            <span v-for="(s, i) in senders" :key="s + i" class="sender-chip">
              {{ s }}
              <button type="button" class="sender-chip-x" :aria-label="C.mailSenderRemove" @click="removeSender(i)">
                <Icon name="close" :size="11" />
              </button>
            </span>
          </div>
          <div class="sender-add">
            <input
              v-model="customSender"
              maxlength="100"
              :placeholder="C.mailSenderCustomPlaceholder"
              @keydown.enter.prevent="addCustomSender"
            />
            <button class="secondary-button" type="button" :disabled="!customSender.trim() || ownerOnly" @click="addCustomSender">
              {{ C.mailSenderAdd }}
            </button>
          </div>
          <button class="secondary-button" type="button" :disabled="discovering || ownerOnly" @click="discoverSenders">
            {{ discovering ? C.mailSenderDiscovering : C.mailSenderDiscover }}
          </button>
          <div v-if="discovered" class="discover-list">
            <label v-for="d in discovered" :key="d.from" class="discover-item">
              <input
                type="checkbox"
                :checked="hasSender(d.from)"
                @change="toggleSender(d.from, ($event.target as HTMLInputElement).checked)"
              />
              <span class="discover-from" :title="d.from">{{ d.from }}</span>
              <span class="discover-meta">{{ C.mailSenderDiscoverCount.replace("{n}", String(d.count)) }} · {{ d.sample }}</span>
            </label>
            <p v-if="!discovered.length" class="dialog-note">{{ C.mailSenderDiscoverEmpty }}</p>
          </div>
          <p class="dialog-note">{{ C.mailSendersNote }}</p>
        </div>


        <div class="control-block">
          <p class="status-line" role="status">
            <span class="status-dot" :class="{ 'is-on': mailRunning }"></span>
            {{ mailStatusLabel }}
          </p>
          <p class="status-line" role="status">{{ lastRunLabel }}</p>
          <div class="control-actions">
            <button class="secondary-button" type="button" :disabled="testingMail || controllingMail || ownerOnly" @click="testMail">
              {{ testingMail ? C.mailTesting : C.mailTest }}
            </button>
            <button v-if="!mailRunning" class="primary-button" type="button" :disabled="controllingMail || testingMail || ownerOnly" @click="startMail">
              {{ controllingMail ? C.mailStarting : C.mailStart }}
            </button>
            <button v-else class="secondary-button" type="button" :disabled="controllingMail || testingMail || ownerOnly" @click="stopMail">
              {{ controllingMail ? C.mailStopping : C.mailStop }}
            </button>
          </div>
          <p v-if="mailError" role="alert" class="form-error">{{ mailError }}</p>
          <p v-else-if="mailMessage" role="status" class="dialog-note">{{ mailMessage }}</p>
        </div>

        <p class="dialog-note settings-warn">{{ C.assistantKeyLost }}</p>
        <p v-if="errMail" role="alert" class="form-error">{{ errMail }}</p>

        <div class="dialog-actions">
          <button class="secondary-button" type="button" :disabled="busy" @click="closeDlg(mailEl)">
            {{ C.cancel }}
          </button>
          <span />
          <button class="primary-button" type="submit" :disabled="busy || ownerOnly">
            <Icon name="check" :size="14" />
            {{ busy ? C.assistantSaving : C.save }}
          </button>
        </div>
      </form>
    </dialog>
  </div>
</template>

<style scoped>
/* 与 planner.css 同一套变量，浅色默认、可选深色。
   弹框复用全局 .task-dialog / .dialog-head / .form-field / .dialog-actions。 */
.settings-dialog .dialog-head {
  margin-bottom: 4px;
}
/* 运行状态 + 测试/启停按钮：用一条分隔线与表单隔开。 */
.control-block {
  display: grid;
  gap: 10px;
  margin-top: 20px;
  padding-top: 14px;
  border-top: 1px solid var(--line-soft);
}
.status-line {
  display: flex;
  align-items: center;
  gap: 8px;
  font-size: 14px;
  font-weight: 600;
  color: var(--ink);
}
.status-dot {
  width: 9px;
  height: 9px;
  border-radius: 50%;
  background: var(--muted);
  transition: background 0.15s ease;
}
.status-dot.is-on {
  background: var(--accent);
}
.control-actions {
  display: flex;
  flex-wrap: wrap;
  gap: 10px;
}
.switch-row {
  display: flex;
  align-items: center;
  gap: 10px;
  font-size: 13px;
  color: var(--ink);
}
.switch-row input {
  width: 16px;
  height: 16px;
  accent-color: var(--accent);
}
.switch-row em {
  margin-left: auto;
  font-style: normal;
  font-family: var(--mono);
  font-size: 11px;
  color: var(--muted);
}
.switch-row em.is-on {
  color: var(--accent);
}
.settings-warn {
  margin-top: 16px;
  padding-top: 12px;
  border-top: 1px solid var(--line-soft);
}
/* 撤销勾选框：比主开关低调，避免被误点。 */
.clear-row {
  margin-top: 10px;
  font-size: 12px;
  color: var(--muted);
}
.clear-row em {
  display: none;
}

/* 账单来源：已选来源 chips + 手动添加 + 从邮箱发现的选择列表。 */
.sender-chips {
  display: flex;
  flex-wrap: wrap;
  gap: 6px;
}
.sender-chip {
  display: inline-flex;
  align-items: center;
  gap: 4px;
  max-width: 100%;
  padding: 3px 8px;
  border: 1px solid var(--line);
  border-radius: 999px;
  background: var(--panel);
  font-size: 12px;
  color: var(--ink);
  word-break: break-all;
}
.sender-chip-x {
  display: inline-flex;
  align-items: center;
  justify-content: center;
  padding: 2px;
  border: 0;
  border-radius: 50%;
  background: transparent;
  color: var(--muted);
  cursor: pointer;
}
.sender-chip-x:hover {
  color: var(--ink);
  background: var(--line-soft);
}
.sender-add {
  display: flex;
  gap: 8px;
}
.sender-add input {
  flex: 1;
}
.discover-list {
  display: grid;
  gap: 4px;
  max-height: 180px;
  overflow: auto;
  padding: 8px;
  border: 1px solid var(--line-soft);
  border-radius: 8px;
  background: var(--bg);
}
.discover-item {
  display: flex;
  align-items: baseline;
  gap: 8px;
  font-size: 13px;
  cursor: pointer;
}
.discover-item input {
  width: 15px;
  height: 15px;
  accent-color: var(--accent);
  flex: none;
}
.discover-from {
  flex: none;
  max-width: 45%;
  overflow: hidden;
  text-overflow: ellipsis;
  white-space: nowrap;
  font-family: var(--mono);
  font-size: 12px;
}
.discover-meta {
  overflow: hidden;
  text-overflow: ellipsis;
  white-space: nowrap;
  color: var(--muted);
  font-size: 12px;
}
</style>
