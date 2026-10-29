<script setup lang="ts">
/* 记一笔 / 编辑账目的底部弹出面板。照简约版 demo 的交互：金额大字号 +
 * 数字键盘、支出/收入切换、六色分类胶囊、备注 + 日期；
 * Enter 保存 · Shift+Enter 保存并再记一笔 · 数字键/退格直接输入 · Esc 关闭。 */
import { onMounted, onUnmounted, ref } from "vue";
import { C } from "../copy";
import { addTx, saveTx } from "../store";
import type { WireTx } from "../api";
import { dateValue } from "../lib/model.js";
import { TODAY } from "../lib/todo.js";
import { LEDGER_CATS } from "../lib/ledger.js";
import Icon from "./Icon.vue";

const props = defineProps<{ tx: WireTx | null }>();
const emit = defineEmits<{ close: [] }>();

const refEl = ref<HTMLDialogElement | null>(null);
const type = ref<"exp" | "inc">(props.tx?.type ?? "exp");
const category = ref(props.tx?.category ?? "");
const note = ref(props.tx?.note ?? "");
const date = ref(props.tx?.date ?? dateValue(TODAY));
const amt = ref(props.tx ? String(props.tx.amountFen / 100) : "");
const error = ref("");
const saving = ref(false);
const todayStr = dateValue(TODAY);
let previous: Element | null = null;

onMounted(() => {
  previous = document.activeElement;
  refEl.value?.showModal();
  window.addEventListener("keydown", onKey);
});
onUnmounted(() => {
  window.removeEventListener("keydown", onKey);
  refEl.value?.close();
  (previous as HTMLElement | null)?.focus?.();
});

/* 键盘即键盘：面板打开时数字键/退格直接进金额（输入框内除外），
 * Enter / Shift+Enter 保存。Esc 走原生 dialog cancel。 */
function onKey(e: KeyboardEvent) {
  const inEditable = (e.target as HTMLElement).closest?.("input, textarea, select");
  if (inEditable) {
    if (e.key === "Enter") {
      e.preventDefault();
      save(!e.shiftKey);
    }
    return;
  }
  if (/^[0-9]$/.test(e.key)) {
    keyIn(e.key);
    e.preventDefault();
  } else if (e.key === ".") {
    keyIn(".");
    e.preventDefault();
  } else if (e.key === "Backspace") {
    keyIn("back");
    e.preventDefault();
  } else if (e.key === "Enter") {
    e.preventDefault();
    save(!e.shiftKey);
  }
}

function keyIn(k: string) {
  let a = amt.value;
  if (k === "clear") a = "";
  else if (k === "back") a = a.slice(0, -1);
  else if (k === ".") {
    if (!a.includes(".")) a = (a || "0") + ".";
  } else if (k === "00") {
    if (a && a !== "0") a += "00";
  } else a = a === "0" ? k : a + k;
  const i = a.indexOf(".");
  if (i >= 0 && a.length - i > 3) a = a.slice(0, i + 3); // 最多两位小数
  if (a.length > 9) a = a.slice(0, 9);
  amt.value = a;
  error.value = "";
}

async function save(closeAfter: boolean) {
  if (saving.value) return;
  const fen = Math.round(parseFloat(amt.value || "0") * 100);
  if (!(fen > 0)) {
    error.value = C.ledgerAmountError;
    return;
  }
  if (type.value === "exp" && !category.value) {
    error.value = C.ledgerCatError;
    return;
  }
  if (!date.value) {
    error.value = C.ledgerDateError;
    return;
  }
  if (date.value > todayStr) {
    error.value = C.ledgerFutureDateError;
    return;
  }
  const input = {
    date: date.value,
    type: type.value,
    category: type.value === "exp" ? category.value : "",
    amountFen: fen,
    note: note.value.trim(),
  };
  saving.value = true;
  const ok = props.tx ? await saveTx(props.tx.id, input, props.tx.version) : await addTx(input);
  saving.value = false;
  if (!ok) return;
  if (closeAfter) {
    emit("close");
  } else {
    // 保存并再记一笔：只清金额与备注，类型/分类/日期沿用——连记时最顺手。
    amt.value = "";
    note.value = "";
    error.value = "";
  }
}

function onDialogClick(e: MouseEvent) {
  if (e.target === e.currentTarget) emit("close");
}
function onCancel() {
  emit("close");
}
</script>

<template>
  <dialog ref="refEl" class="ledger-sheet" :aria-label="tx ? C.ledgerEdit : C.ledgerAdd" @cancel="onCancel" @click="onDialogClick">
    <div class="ledger-sheet-body">
      <div class="dialog-head">
        <div>
          <span class="overline">LEDGER</span>
          <h2>{{ tx ? C.ledgerEdit : C.ledgerAdd }}</h2>
        </div>
        <button type="button" class="icon-button" :aria-label="C.close" @click="emit('close')">
          <Icon name="close" />
        </button>
      </div>

      <div class="ledger-amt-input" aria-live="polite">
        <span v-if="amt" class="num">¥{{ amt }}</span>
        <span v-else class="is-ph">{{ C.ledgerAmountPlaceholder }}</span>
      </div>

      <div class="ledger-seg" role="group" :aria-label="C.group">
        <button type="button" :class="type === 'exp' ? 'is-on' : ''" :aria-pressed="type === 'exp'" @click="type = 'exp'">
          {{ C.ledgerExp }}
        </button>
        <button type="button" :class="type === 'inc' ? 'is-on' : ''" :aria-pressed="type === 'inc'" @click="type = 'inc'">
          {{ C.ledgerInc }}
        </button>
      </div>

      <div v-if="type === 'exp'" class="ledger-cats" role="group" :aria-label="C.ledgerCategory">
        <button
          v-for="c in LEDGER_CATS"
          :key="c.name"
          type="button"
          class="ledger-chip"
          :data-color="c.color"
          :class="{ 'is-on': category === c.name }"
          :aria-pressed="category === c.name"
          @click="category = c.name"
        >
          <span class="ledger-dot" />{{ c.name }}
        </button>
      </div>

      <div class="form-pair">
        <label class="form-field">
          {{ C.ledgerNote }}
          <input v-model="note" maxlength="240" :placeholder="C.ledgerNotePlaceholder" autocomplete="off" />
        </label>
        <label class="form-field">
          {{ C.date }}
          <input v-model="date" type="date" :max="todayStr" />
        </label>
      </div>

      <div class="ledger-keypad">
        <button v-for="k in ['7', '8', '9', 'back', '4', '5', '6', 'clear', '1', '2', '3', '.', '0', '00']" :key="k" type="button" class="ledger-key" @click="keyIn(k)">
          {{ k === "back" ? "⌫" : k === "clear" ? "C" : k }}
        </button>
        <button type="button" class="ledger-key is-ok" :disabled="saving" @click="save(true)">✓ {{ C.ledgerSave }}</button>
      </div>

      <p class="ledger-kbd">{{ C.ledgerKbdHint }}</p>
      <p v-if="error" role="alert" class="form-error">{{ error }}</p>
      <div class="dialog-actions">
        <button type="button" class="secondary-button" @click="save(false)">{{ C.ledgerSaveAnother }}</button>
        <button type="button" class="primary-button" :disabled="saving" @click="save(true)">{{ C.ledgerSave }}</button>
      </div>
    </div>
  </dialog>
</template>
