<script setup lang="ts">
/* 登录/注册：全栈新增的入口页。复用品牌与 CSS 变量，保持浅色默认风格。 */
import { ref } from "vue";
import { C } from "../copy";
import { login, register, st } from "../store";
import Icon from "./Icon.vue";

const isRegister = ref(false);
const username = ref("");
const password = ref("");
const busy = ref(false);
const error = ref("");

async function submit() {
  if (busy.value) return;
  error.value = "";
  busy.value = true;
  try {
    if (isRegister.value) await register(username.value.trim(), password.value);
    else await login(username.value.trim(), password.value);
    st.sessionNotice = "";
  } catch (e: any) {
    if (e?.status === 401) error.value = C.authFailed;
    else if (e?.code === "username_taken") error.value = C.authTaken;
    else if (e?.code === "validation_failed") error.value = C.authInvalid;
    else if (e?.status === 0) error.value = C.offline;
    else error.value = e?.message || C.syncError;
  } finally {
    busy.value = false;
  }
}
</script>

<template>
  <div class="auth-shell">
    <section class="auth-card" aria-labelledby="auth-title">
      <div class="brand-lockup">
        <span class="brand-mark"><Icon name="check" :size="22" /></span>
        <div>
          <strong>Todo Day<span class="brand-period">.</span></strong>
          <p>{{ C.brand }}</p>
        </div>
      </div>
      <h1 id="auth-title">{{ C.authWelcome }}</h1>
      <p class="auth-sub">{{ st.sessionNotice || C.authHint }}</p>
      <form
        class="auth-form"
        @submit.prevent="
          error = '';
          submit();
        "
      >
        <label class="form-field">
          {{ C.username }}
          <input v-model="username" maxlength="32" autocomplete="username" :placeholder="C.usernameRule" />
        </label>
        <label class="form-field">
          {{ C.password }}
          <input
            v-model="password"
            type="password"
            maxlength="72"
            :autocomplete="isRegister ? 'new-password' : 'current-password'"
            :placeholder="C.passwordRule"
            @input="error = ''"
          />
        </label>
        <p v-if="error" role="alert" class="form-error">{{ error }}</p>
        <button class="primary-button auth-submit" type="submit" :disabled="busy">
          {{ busy ? C.loggingIn : isRegister ? C.registerSubmit : C.loginSubmit }}
        </button>
      </form>
      <button class="text-button auth-switch" @click="isRegister = !isRegister">
        {{ isRegister ? C.switchToLogin : C.switchToRegister }}
      </button>
    </section>
  </div>
</template>

<style scoped>
/* 与 planner.css 同一套 CSS 变量，浅色默认、可选深色，保证观感一致。 */
.auth-shell {
  min-height: 100vh;
  display: grid;
  place-items: center;
  padding: 24px;
  background: var(--bg);
}
.auth-card {
  width: min(400px, 100%);
  background: var(--panel);
  border: 1px solid var(--line);
  border-radius: var(--r-lg, 14px);
  padding: 34px 32px 28px;
  box-shadow: var(--shadow, 0 12px 40px rgba(15, 23, 42, 0.08));
}
.auth-card .brand-lockup {
  margin-bottom: 22px;
}
.auth-card h1 {
  margin: 0 0 6px;
  font-size: 26px;
  letter-spacing: -0.02em;
}
.auth-sub {
  margin: 0 0 22px;
  color: var(--muted);
  font-size: 13.5px;
  line-height: 1.6;
}
.auth-form {
  display: grid;
  gap: 14px;
}
.auth-submit {
  width: 100%;
  justify-content: center;
  margin-top: 4px;
}
.auth-switch {
  margin-top: 16px;
  width: 100%;
  text-align: center;
}
</style>
