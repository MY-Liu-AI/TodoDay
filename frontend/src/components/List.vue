<script setup lang="ts">
import { computed, onUnmounted } from "vue";
import { ORDER, TODAY, bucketOf, dueText, midnight } from "../lib/todo.js";
import { C } from "../copy";
import type { Item } from "../store";
import { goalOfTask } from "../store";
import { groupColor } from "../lib/palette.js";
import { haptic } from "../lib/fx";
import Icon from "./Icon.vue";

const props = defineProps<{ items: Item[]; sort: string }>();
const emit = defineEmits<{
  toggle: [item: Item];
  edit: [item: Item];
  reorder: [from: string, to: string];
}>();

/* 所属目标徽章：任务挂在目标下时，行内多一个小靶标 + 目标名。 */
const goalBadge = (id: string) => goalOfTask(id)?.title || "";

/* bucket 分组沿用原实现：逾期/今天/明天/以后/没排期/已完成。 */
const grouped = computed(() => {
  const g: Record<string, Item[]> = {};
  props.items.forEach((i) => ((g[bucketOf(i)] ??= []).push(i)));
  return g;
});
const sections = computed(() => ORDER.filter((k: string) => grouped.value[k]));

/* ── pointer 拖拽（原样移植 React 版的 DOM 逻辑） ─────────────────────── */
let cleanup: (() => void) | null = null;
onUnmounted(() => cleanup?.());

function drag(item: Item) {
  return (e: PointerEvent) => {
    if (props.sort !== "manual") return;
    e.preventDefault();
    const handle = e.currentTarget as HTMLButtonElement;
    const row = handle.closest(".task-row") as HTMLElement;
    let target: HTMLElement | null = null;
    handle.setPointerCapture(e.pointerId);
    row.classList.add("is-dragging");
    haptic(8);
    const move = (ev: PointerEvent) => {
      target?.classList.remove("is-drop-target");
      const rows = Array.from(row.parentElement!.querySelectorAll<HTMLElement>(".task-row")).filter(
        (r) => r !== row && r.dataset.bucket === row.dataset.bucket,
      );
      target =
        rows.find((r) => {
          const b = r.getBoundingClientRect();
          return ev.clientY >= b.top && ev.clientY <= b.bottom;
        }) || null;
      target?.classList.add("is-drop-target");
    };
    const clean = () => {
      row.classList.remove("is-dragging");
      target?.classList.remove("is-drop-target");
      handle.removeEventListener("pointermove", move);
      handle.removeEventListener("pointerup", up);
      handle.removeEventListener("pointercancel", cancel);
    };
    const up = () => {
      const id = target?.dataset.id;
      clean();
      if (id) emit("reorder", item.id, id);
    };
    const cancel = () => clean();
    cleanup = clean;
    handle.addEventListener("pointermove", move);
    handle.addEventListener("pointerup", up);
    handle.addEventListener("pointercancel", cancel);
  };
}
</script>

<template>
  <div class="task-list">
    <template v-for="g in sections" :key="g">
      <div class="task-group-heading" :class="g === C.overdue ? 'is-late' : ''">
        <h2>{{ g }}</h2>
        <span>{{ grouped[g].length }}</span>
        <i />
      </div>
      <div
        v-for="(i, index) in grouped[g]"
        :key="i.id"
        class="task-row"
        :class="i.done ? 'is-done' : ''"
        :data-id="i.id"
        :data-bucket="g"
      >
        <button
          class="task-check"
          :class="i.done ? 'is-checked' : ''"
          :aria-label="(i.done ? C.reopen : C.complete) + ' ' + i.t"
          :aria-pressed="i.done"
          @click="emit('toggle', i)"
        >
          <Icon name="check" :size="14" />
        </button>
        <button class="task-title-button" @click="emit('edit', i)">
          <span class="task-title">{{ i.t }}</span>
          <span class="task-meta">
            <span v-if="goalBadge(i.id)" class="goal-badge" :title="goalBadge(i.id)">
              <Icon name="target" :size="10" />
              <span class="goal-badge-label">{{ goalBadge(i.id) }}</span>
            </span>
            <span class="task-group-label" :data-color="groupColor(i.g)">{{ i.g }}</span>
            <span v-if="i.due" :class="midnight(i.due) < TODAY && !i.done ? 'is-late' : ''">{{ dueText(i) }}</span>
            <span v-if="i.p < 3">{{ i.p === 1 ? C.high : C.medium }}</span>
          </span>
        </button>
        <div class="reorder-controls">
          <template v-if="sort === 'manual'">
            <button
              class="reorder-key"
              :aria-label="C.moveUp + ' ' + i.t"
              :disabled="index === 0"
              @click="emit('reorder', i.id, grouped[g][index - 1].id)"
            >
              ↑
            </button>
            <button
              class="reorder-key"
              :aria-label="C.moveDown + ' ' + i.t"
              :disabled="index === grouped[g].length - 1"
              @click="emit('reorder', i.id, grouped[g][index + 1].id)"
            >
              ↓
            </button>
            <button class="drag-handle" tabindex="-1" :aria-label="C.drag" :title="C.drag" @pointerdown="drag(i)">
              <svg width="12" height="18" viewBox="0 0 12 18" fill="currentColor">
                <circle cx="3" cy="4" r="1" />
                <circle cx="9" cy="4" r="1" />
                <circle cx="3" cy="9" r="1" />
                <circle cx="9" cy="9" r="1" />
                <circle cx="3" cy="14" r="1" />
                <circle cx="9" cy="14" r="1" />
              </svg>
            </button>
          </template>
        </div>
      </div>
    </template>
  </div>
</template>
