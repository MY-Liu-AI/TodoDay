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
  type AssistantConfigInput,
} from "../store";
import Icon from "./Icon.vue";

const enabled = ref(false);
const botId = ref("");
const botSecret = ref("");
const kimiApiKey = ref("");
const kimiBase = ref("");
const kimiModel = ref("");
const busy = ref(false);
const error = ref("");

const view = computed(() => st.assistant);
const ownerOnly = computed(() => !!st.workspace?.role && st.workspace.role !== "owner");

/* 服务端没有主密钥时，填了密钥也存不进去 —— 提前说清楚，
   而不是等用户点保存才报错。 */
const encryptionMissing = computed(() => view.value !== null && !view.value.encryptionAvailable);

function fillFromServer() {
  const v = view.value;
  enabled.value = v?.enabled ?? false;
  botId.value = v?.botId ?? "";
  kimiBase.value = v?.kimiBase ?? "";
  kimiModel.value = v?.kimiModel ?? "";
  // 密钥字段一律留空：既不回显，也不预填。
  botSecret.value = "";
  kimiApiKey.value = "";
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

async function submit() {
  if (busy.value) return;
  error.value = "";
  busy.value = true;
  try {
    const input: AssistantConfigInput = {
      enabled: enabled.value,
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

    const ok = await saveAssistantConfig(input);
    if (ok) {
      fillFromServer(); // 清空密钥框、刷新掩码状态
    } else {
      error.value = C.assistantSaveFailed;
    }
  } finally {
    busy.value = false;
  }
}
</script>

<template>
  <div class="settings-layout">
    <section class="settings-card">
      <div class="grouphead"><span>{{ C.assistantTitle }}</span></div>
      <p class="dialog-note">{{ C.assistantHint }}</p>

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
        <label class="switch-row">
          <input v-model="enabled" type="checkbox" />
          <span>{{ C.assistantEnabled }}</span>
          <em :class="view?.enabled ? 'is-on' : ''">
            {{ view?.enabled ? C.assistantSecretSet : C.assistantSecretUnset }}
          </em>
        </label>

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

        <p role="status" class="dialog-note">{{ C.assistantNoKeyField }}</p>
        <p v-if="error" role="alert" class="form-error">{{ error }}</p>

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
.settings-card {
  background: var(--panel);
  border: 1px solid var(--line);
  border-radius: var(--r-lg, 14px);
  padding: 24px;
}
.settings-form {
  display: grid;
  gap: 14px;
  margin-top: 8px;
}
.settings-form .grouphead {
  margin-top: 8px;
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
</style>
