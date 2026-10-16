<script setup lang="ts">
/* 设置页：配置企业微信助手与 AI 解析凭证。
 *
 * 两个密钥输入框永远不回显已存的值 —— 服务端只回「是否已设置」，
 * 前端也就无从泄漏。留空提交表示「保留原值」，由服务端保证。 */
import { computed, onMounted, ref } from "vue";
import { C } from "../copy";
import {
  st,
  loadAssistantConfig,
  saveAssistantConfig,
  testAssistantConfig,
  testAssistantLlm,
  startAssistant,
  stopAssistant,
  type AssistantConfigInput,
} from "../store";
import Icon from "./Icon.vue";

const botId = ref("");
const botSecret = ref("");
const kimiApiKey = ref("");
const kimiBase = ref("");
const kimiModel = ref("");
const busy = ref(false);
const error = ref("");
/* 撤销：留空只表示「不修改」，所以删掉已存密钥要显式勾选。 */
const clearBotSecret = ref(false);
const clearKimiApiKey = ref(false);
/* 测试 / 启停的本地忙碌态与行内反馈。 */
const testing = ref(false);
const controlling = ref(false);
const controlMessage = ref("");
const controlError = ref("");
/* LLM（Kimi）测试连接的本地状态。 */
const testingLlm = ref(false);
const llmMessage = ref("");
const llmError = ref("");

const view = computed(() => st.assistant);
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

onMounted(async () => {
  await loadAssistantConfig();
  fillFromServer();
});

async function reload() {
  error.value = "";
  await loadAssistantConfig();
  fillFromServer();
}

/* 把表单当前值整理成保存入参。密钥只在用户确实输入时才带上（空串=保留原值），
   撤销勾选优先于填写。submit 与「启动助手」共用：启用前也要先保存。 */
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

async function submit() {
  if (busy.value) return;
  error.value = "";
  busy.value = true;
  try {
    const ok = await saveAssistantConfig(buildInput());
    if (ok) {
      fillFromServer(); // 清空密钥框、刷新掩码状态
    } else {
      error.value = C.assistantSaveFailed;
    }
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
    // 「启用助手」先保存当前表单再启动：测试连接验证的是表单当前值，
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
</script>

<template>
  <div class="settings-layout">
    <section>
      <p v-if="st.assistantLoading && !view" class="dialog-note">{{ C.syncing }}</p>

      <p v-if="view === null && !st.assistantLoading" class="dialog-note">
        {{ C.assistantLoadFailed }}
        <button class="text-button" @click="reload">{{ C.assistantReload }}</button>
      </p>

      <p v-if="ownerOnly" role="alert" class="form-error">{{ C.assistantOwnerOnly }}</p>
      <p v-if="encryptionMissing" role="alert" class="storage-warning">
        {{ C.assistantEncryptionMissing }}
      </p>

      <form class="settings-form" @submit.prevent="submit">
        <!-- 企业微信助手：机器人凭据 + 测试/启停，独占一个外框。 -->
        <div class="settings-group">
          <div class="grouphead"><span>{{ C.assistantTitle }}</span></div>
          <p class="dialog-note">{{ C.assistantHint }}</p>

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
              @input="error = ''"
            />
          </label>
          <label v-if="view?.botSecretSet" class="switch-row clear-row">
            <input v-model="clearBotSecret" type="checkbox" />
            <span>{{ C.assistantClearBotSecret }}</span>
          </label>
          <p v-if="clearBotSecret" role="status" class="dialog-note">{{ C.assistantClearHint }}</p>

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
            <p class="dialog-note">{{ C.assistantControlHint }}</p>
          </div>
        </div>

        <!-- AI 解析凭证：另一个外框。 -->
        <div class="settings-group">
          <div class="grouphead"><span>{{ C.assistantAiTitle }}</span></div>
          <p class="dialog-note">{{ C.assistantAiHint }}</p>

          <label class="form-field">
            {{ C.assistantKimiKey }}
            <input
              v-model="kimiApiKey"
              type="password"
              maxlength="200"
              autocomplete="new-password"
              :placeholder="view?.kimiApiKeySet ? C.assistantKimiKeyKeep : C.assistantKimiKeyNew"
              @input="error = ''"
            />
          </label>
          <label v-if="view?.kimiApiKeySet" class="switch-row clear-row">
            <input v-model="clearKimiApiKey" type="checkbox" />
            <span>{{ C.assistantClearKimiKey }}</span>
          </label>
          <p v-if="clearKimiApiKey" role="status" class="dialog-note">{{ C.assistantClearHint }}</p>

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

          <p role="status" class="dialog-note">{{ C.assistantNoKeyField }}</p>
          <p v-if="error" role="alert" class="form-error">{{ error }}</p>
        </div>

        <div class="dialog-actions">
          <button class="secondary-button" type="button" :disabled="busy" @click="reload">
            {{ C.assistantReload }}
          </button>
          <span />
          <button class="primary-button" type="submit" :disabled="busy || ownerOnly">
            <Icon name="check" :size="14" />
            {{ busy ? C.assistantSaving : C.assistantSave }}
          </button>
        </div>
      </form>

      <p class="dialog-note settings-warn">{{ C.assistantKeyLost }}</p>
    </section>
  </div>
</template>

<style scoped>
/* 与 planner.css 同一套变量，浅色默认、可选深色。
   表单控件复用全局的 .form-field / .form-pair / .dialog-actions / .form-error。 */
.settings-layout {
  display: grid;
  grid-template-columns: minmax(0, 640px);
  margin-top: 32px;
}
.settings-form {
  display: grid;
  gap: 14px;
  margin-top: 8px;
}
.settings-form .grouphead {
  margin-top: 8px;
}
/* 两个凭据组各占一个外框：机器人凭据 + AI 解析凭证。 */
.settings-group {
  display: grid;
  gap: 14px;
  padding: 16px;
  border: 1px solid var(--line);
  border-radius: var(--r-lg, 14px);
}
.settings-group .grouphead {
  margin-top: 0;
}
/* 运行状态 + 测试/启停按钮：放在机器人凭据框内，用一条分隔线隔开。 */
.control-block {
  display: grid;
  gap: 10px;
  margin-top: 4px;
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
  font-size: 12px;
  color: var(--muted);
}
.clear-row em {
  display: none;
}
</style>
