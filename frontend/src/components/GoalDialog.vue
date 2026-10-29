<script setup lang="ts">
/* 新建/编辑目标的原生 dialog 表单。空标题错误不关闭弹窗。
   目标不选截止日期——排期在里程碑（挂载任务）上。

   创建 = 三步「强制决策」向导（2026-10-26）：定目标（名称+为什么做）
   → 定验收（可验证的标准）→ 定代价（失败代价）。每一步必须给出决策
   才能前进。编辑沿用同一布局，但尊重既有数据——三问留空不阻拦
   （标题仍必填）。 */
import { computed, nextTick, onMounted, onUnmounted, ref } from "vue";
import { C } from "../copy";
import type { GoalInput } from "../store";
import Icon from "./Icon.vue";

const props = defineProps<{ goal: { title: string; goalText: string; acceptance: string; failureCost: string; group: string } | null }>();
const emit = defineEmits<{
  save: [input: GoalInput];
  close: [];
}>();

const isCreate = computed(() => !props.goal);
const refEl = ref<HTMLDialogElement | null>(null);
const title = ref(props.goal?.title || "");
const goalText = ref(props.goal?.goalText || "");
const acceptance = ref(props.goal?.acceptance || "");
const failureCost = ref(props.goal?.failureCost || "");
const step = ref(0);
const error = ref("");
let previous: Element | null = null;

const steps = computed(() => [
  { label: C.goalStepBasics, hint: C.goalStepBasicsHint },
  { label: C.goalStepAcceptance, hint: C.goalStepAcceptanceHint },
  { label: C.goalStepTradeoff, hint: C.goalStepTradeoffHint },
]);
const stepCount = computed(() => C.goalStepCount.replace("{n}", String(step.value + 1)).replace("{total}", String(steps.value.length)));

onMounted(() => {
  previous = document.activeElement;
  const dialog = refEl.value;
  dialog?.showModal();
  dialog?.querySelector("input")?.focus();
});
onUnmounted(() => {
  refEl.value?.close();
  (previous as HTMLElement | null)?.focus?.();
});

/* 每一步的强制决策。创建时三问必填；编辑时尊重既有数据、不回填逼迫。 */
function stepError(i: number): string {
  if (i === 0) {
    if (!title.value.trim()) return C.goalErrTitle;
    if (isCreate.value && !goalText.value.trim()) return C.goalErrGoal;
  }
  if (i === 1 && isCreate.value && !acceptance.value.trim()) return C.goalErrAcceptance;
  if (i === 2 && isCreate.value && !failureCost.value.trim()) return C.goalErrFailure;
  return "";
}

async function focusStepControl() {
  await nextTick();
  const body = refEl.value?.querySelector(".goal-step-body");
  const empty = Array.from(body?.querySelectorAll<HTMLInputElement | HTMLTextAreaElement>("input, textarea") || []).find(
    (el) => !el.value.trim(),
  );
  (empty || body?.querySelector("input, textarea"))?.focus();
}

function next() {
  const msg = stepError(step.value);
  if (msg) {
    error.value = msg;
    focusStepControl();
    return;
  }
  error.value = "";
  if (step.value < steps.value.length - 1) {
    step.value += 1;
    focusStepControl();
  } else {
    submit();
  }
}

function back() {
  if (step.value === 0) return;
  error.value = "";
  step.value -= 1;
  focusStepControl();
}

function submit() {
  if (!title.value.trim()) {
    error.value = C.goalErrTitle;
    step.value = 0;
    return;
  }
  emit("save", {
    title: title.value.trim(),
    goalText: goalText.value.trim(),
    acceptance: acceptance.value.trim(),
    failureCost: failureCost.value.trim(),
    /* 不再有分组输入：编辑时原样回传旧值（不清空），新建为 ""。 */
    group: props.goal?.group || "",
    status: "active",
  });
}

function onDialogClick(e: MouseEvent) {
  if (e.target === e.currentTarget) emit("close");
}
</script>

<template>
  <dialog ref="refEl" class="task-dialog goal-dialog" aria-labelledby="goal-dialog-title" @cancel="emit('close')" @click="onDialogClick">
    <form novalidate @submit.prevent="next">
      <div class="dialog-head">
        <div>
          <span class="overline">TODO DAY</span>
          <h2 id="goal-dialog-title">{{ goal ? C.goalEdit : C.goalNew }}</h2>
        </div>
        <button type="button" class="icon-button" :aria-label="C.close" @click="emit('close')">
          <Icon name="close" />
        </button>
      </div>

      <ol class="goal-steps" :aria-label="C.goalStepCount.replace('{n}', String(step + 1)).replace('{total}', String(steps.length))">
        <li v-for="(s, i) in steps" :key="s.label" :class="['goal-step', { 'is-done': i < step, 'is-current': i === step }]">
          <span class="goal-step-dot" aria-hidden="true">
            <Icon v-if="i < step" name="check" :size="10" />
            <template v-else>{{ i + 1 }}</template>
          </span>
          <span class="goal-step-name">{{ s.label }}</span>
          <span v-if="i < steps.length - 1" class="goal-step-line" aria-hidden="true" />
        </li>
      </ol>

      <div class="goal-step-body" :key="step">
        <p class="goal-step-hint">{{ steps[step].hint }}</p>

        <template v-if="step === 0">
          <label class="form-field">
            <span class="goal-field-label">{{ C.goalName }}<i class="goal-req" aria-hidden="true" /></span>
            <input
              v-model="title"
              maxlength="240"
              :placeholder="C.goalName"
              @input="error = ''"
              @keydown.enter.prevent="next"
            />
          </label>
          <label class="form-field">
            <span class="goal-field-label">{{ C.goalStrategyGoal }}<i v-if="isCreate" class="goal-req" aria-hidden="true" /></span>
            <textarea v-model="goalText" maxlength="1000" rows="3" :placeholder="C.goalStrategyGoalPlaceholder" @input="error = ''" />
          </label>
        </template>

        <template v-else-if="step === 1">
          <label class="form-field">
            <span class="goal-field-label">{{ C.goalStrategyAcceptance }}<i v-if="isCreate" class="goal-req" aria-hidden="true" /></span>
            <textarea
              v-model="acceptance"
              maxlength="1000"
              rows="4"
              :placeholder="C.goalStrategyAcceptancePlaceholder"
              @input="error = ''"
            />
          </label>
        </template>

        <template v-else>
          <label class="form-field">
            <span class="goal-field-label">{{ C.goalStrategyFailure }}<i v-if="isCreate" class="goal-req" aria-hidden="true" /></span>
            <textarea
              v-model="failureCost"
              maxlength="1000"
              rows="3"
              :placeholder="C.goalStrategyFailurePlaceholder"
              @input="error = ''"
            />
          </label>
        </template>
      </div>

      <p v-if="error" role="alert" class="form-error">{{ error }}</p>

      <div class="dialog-actions">
        <span class="goal-step-count">{{ stepCount }}</span>
        <button v-if="step > 0" type="button" class="secondary-button" @click="back">{{ C.goalBack }}</button>
        <button class="primary-button" type="submit">{{ step === steps.length - 1 ? (goal ? C.save : C.goalCreate) : C.goalNext }}</button>
      </div>
    </form>
  </dialog>
</template>
