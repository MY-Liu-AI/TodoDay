<script setup lang="ts">
/* 可键盘操作的原生 dialog 表单。空标题错误不会关闭弹窗。 */
import { onMounted, onUnmounted, ref } from "vue";
import type { Item } from "../store";
import { dateValue } from "../lib/model.js";
import { C } from "../copy";
import Icon from "./Icon.vue";

const props = defineProps<{ item: Item }>();
const emit = defineEmits<{
  save: [item: Item];
  delete: [item: Item];
  close: [];
}>();

const refEl = ref<HTMLDialogElement | null>(null);
const title = ref(props.item.t);
const date = ref(dateValue(props.item.due));
const group = ref(props.item.g);
const priority = ref(props.item.p);
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
  const due = date.value ? +new Date(date.value + "T00:00:00") : 0;
  if (!Number.isFinite(due)) {
    error.value = C.dateError;
    return;
  }
  emit("save", {
    ...props.item,
    t: title.value.trim(),
    due,
    g: group.value.trim() || C.personal,
    p: priority.value,
  });
}

function onDialogClick(e: MouseEvent) {
  if (e.target === e.currentTarget) emit("close");
}
function onCancel() {
  emit("close");
}
</script>

<template>
  <dialog ref="refEl" class="task-dialog" aria-labelledby="dialog-title" @cancel="onCancel" @click="onDialogClick">
    <form novalidate @submit.prevent="submit">
      <div class="dialog-head">
        <div>
          <span class="overline">TODO DAY</span>
          <h2 id="dialog-title">{{ item.id ? C.editTask : C.newTask }}</h2>
        </div>
        <button type="button" class="icon-button" :aria-label="C.close" @click="emit('close')">
          <Icon name="close" />
        </button>
      </div>
      <label class="form-field">
        {{ C.taskName }}
        <input
          v-model="title"
          maxlength="240"
          :aria-invalid="!!error"
          :aria-describedby="error ? 'task-error' : undefined"
          :placeholder="C.titlePlaceholder"
          @input="error = ''"
        />
      </label>
      <div class="form-pair">
        <label class="form-field">
          {{ C.date }}
          <input v-model="date" type="date" />
        </label>
        <label class="form-field">
          {{ C.group }}
          <input v-model="group" maxlength="40" />
        </label>
      </div>
      <label class="form-field">
        {{ C.priority }}
        <select v-model="priority" :aria-label="C.priority">
          <option :value="3">{{ C.low }}</option>
          <option :value="2">{{ C.medium }}</option>
          <option :value="1">{{ C.high }}</option>
        </select>
      </label>
      <p v-if="error" id="task-error" role="alert" class="form-error">{{ error }}</p>
      <div class="dialog-actions">
        <button v-if="item.id" type="button" class="text-button danger" @click="emit('delete', item)">
          {{ C.deleteTask }}
        </button>
        <span />
        <button type="button" class="secondary-button" @click="emit('close')">{{ C.cancel }}</button>
        <button class="primary-button" type="submit">{{ item.id ? C.save : C.create }}</button>
      </div>
    </form>
  </dialog>
</template>
