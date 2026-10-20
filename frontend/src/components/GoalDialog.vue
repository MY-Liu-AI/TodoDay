<script setup lang="ts">
/* 新建/编辑目标的原生 dialog 表单。空标题错误不关闭弹窗。 */
import { onMounted, onUnmounted, ref } from "vue";
import { C } from "../copy";
import type { GoalInput } from "../store";
import Icon from "./Icon.vue";

const props = defineProps<{ goal: { title: string; note: string; group: string; mode: string; due: string } | null }>();
const emit = defineEmits<{
  save: [input: GoalInput];
  close: [];
}>();

const refEl = ref<HTMLDialogElement | null>(null);
const title = ref(props.goal?.title || "");
const note = ref(props.goal?.note || "");
const group = ref(props.goal?.group || "");
const mode = ref<"parallel" | "sequential">(props.goal?.mode === "sequential" ? "sequential" : "parallel");
const due = ref(props.goal?.due || "");
const error = ref("");
let previous: Element | null = null;

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

function submit() {
  if (!title.value.trim()) {
    error.value = C.titleError;
    return;
  }
  if (due.value && !/^\d{4}-\d{2}-\d{2}$/.test(due.value)) {
    error.value = C.dateError;
    return;
  }
  emit("save", {
    title: title.value.trim(),
    note: note.value.trim(),
    group: group.value.trim(),
    mode: mode.value,
    due: due.value,
    status: "active",
  });
}

function onDialogClick(e: MouseEvent) {
  if (e.target === e.currentTarget) emit("close");
}
</script>

<template>
  <dialog ref="refEl" class="task-dialog" aria-labelledby="goal-dialog-title" @cancel="emit('close')" @click="onDialogClick">
    <form novalidate @submit.prevent="submit">
      <div class="dialog-head">
        <div>
          <span class="overline">TODO DAY</span>
          <h2 id="goal-dialog-title">{{ goal ? C.goalEdit : C.goalNew }}</h2>
        </div>
        <button type="button" class="icon-button" :aria-label="C.close" @click="emit('close')">
          <Icon name="close" />
        </button>
      </div>
      <label class="form-field">
        {{ C.goalName }}
        <input
          v-model="title"
          maxlength="240"
          :aria-invalid="!!error"
          :aria-describedby="error ? 'goal-error' : undefined"
          :placeholder="C.goalName"
          @input="error = ''"
        />
      </label>
      <label class="form-field">
        {{ C.goalNote }}
        <textarea v-model="note" maxlength="500" rows="2" :placeholder="C.goalNotePlaceholder" />
      </label>
      <div class="form-pair">
        <label class="form-field">
          {{ C.group }}
          <input v-model="group" maxlength="40" :placeholder="C.group" />
        </label>
        <label class="form-field">
          {{ C.goalDue }}
          <input v-model="due" type="date" />
        </label>
      </div>
      <label class="form-field">
        {{ C.goalMode }}
        <select v-model="mode">
          <option value="parallel">{{ C.goalModeParallel }}</option>
          <option value="sequential">{{ C.goalModeSequential }}</option>
        </select>
      </label>
      <p class="form-hint">{{ mode === "sequential" ? C.goalModeHintSequential : C.goalModeHintParallel }}</p>
      <p v-if="error" id="goal-error" role="alert" class="form-error">{{ error }}</p>
      <div class="dialog-actions">
        <span />
        <button type="button" class="secondary-button" @click="emit('close')">{{ C.cancel }}</button>
        <button class="primary-button" type="submit">{{ goal ? C.save : C.create }}</button>
      </div>
    </form>
  </dialog>
</template>
