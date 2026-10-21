<script setup lang="ts">
/* 番茄专注页的任务行：勾选完成 + 象限色标 + 点击任务展开 AI 归类理由
   + 选择专注 + 上下移动。象限只读展示：色标用颜色区分类别，不可改选；
   有理由的任务点击标题区展开，原因显示在行下方。 */
import { computed, ref } from "vue";
import { C } from "../copy";
import { TODAY, midnight } from "../lib/todo.js";
import { groupColor } from "../lib/palette.js";
import { sound, haptic } from "../lib/fx";
import { toggleTask, goalOfTask } from "../store";
import type { Item } from "../store";
import { fs, pick } from "../focus";
import type { QuadInfo } from "../quadrant";
import Icon from "./Icon.vue";

const props = defineProps<{
  i: Item;
  quad?: QuadInfo | null;
  canUp?: boolean;
  canDown?: boolean;
}>();
const emit = defineEmits<{ (e: "move", dir: -1 | 1): void }>();

const QUADS = [
  { q: "IU", label: C.quadrantIU },
  { q: "IN", label: C.quadrantIN },
  { q: "NU", label: C.quadrantNU },
  { q: "NN", label: C.quadrantNN },
];
const hasReason = computed(() => !!props.quad?.reason);
const quadTitle = computed(() => QUADS.find((x) => x.q === props.quad?.quadrant)?.label ?? "");
/* 圆点双 duty：有象限时按象限变色（四象限语义色），否则沿用分组分类色。 */
const QUAD_COLORS: Record<string, string> = {
  IU: "var(--neg)",
  IN: "var(--accent)",
  NU: "#b98a2e",
  NN: "var(--muted)",
};
const dotStyle = computed(() => {
  const c = props.quad && QUAD_COLORS[props.quad.quadrant];
  return c ? { background: c } : undefined;
});
const open = ref(false);

function onExpand() {
  if (!hasReason.value) return;
  open.value = !open.value;
  sound.tick();
}

const busy = () => fs.phase === "running" || fs.phase === "paused";
const isOverdue = (i: Item) => !!i.due && !i.done && midnight(i.due) < TODAY;

function fmtPlan(ms: number): string {
  const d = new Date(ms);
  return `${d.getMonth() + 1}月${d.getDate()}日`;
}

function onMarkDone(i: Item) {
  if (busy() && fs.task?.id === i.id) return;
  toggleTask(i);
  i.done ? sound.tick() : sound.ding();
  haptic(14);
}
function onChoose(i: Item) {
  if (busy()) return;
  pick(fs.task?.id === i.id ? null : i);
  sound.tick();
}
function onMove(dir: -1 | 1) {
  sound.tick();
  emit("move", dir);
}
</script>

<template>
  <div
    class="focus-task"
    :class="[fs.task?.id === i.id ? 'is-picked' : '', i.done ? 'is-done' : '', open ? 'is-open' : '']"
    :data-color="groupColor(i.g)"
  >
    <button
      class="task-check"
      :class="i.done ? 'is-checked' : ''"
      :aria-label="(i.done ? C.focusToggleReopen : C.focusToggleDone) + ' ' + i.t"
      :aria-pressed="i.done"
      @click="onMarkDone(i)"
    >
      <Icon name="check" :size="14" />
    </button>
    <!-- 标题区：有 AI 理由时可点击展开/收起，原因在行下方展开显示。
         行首圆点有象限时按象限变色（title 提示象限全称），否则仍是分组色。 -->
    <component
      :is="hasReason ? 'button' : 'div'"
      class="focus-task-expand"
      :aria-expanded="hasReason ? open : undefined"
      @click="onExpand"
    >
      <span class="event-dot" :style="dotStyle" :title="quad ? quadTitle : ''" />
      <span class="focus-task-title">{{ i.t }}</span>
      <span v-if="goalOfTask(i.id)" class="goal-badge" :title="goalOfTask(i.id)!.title">
        <Icon name="target" :size="10" />
        <span class="goal-badge-label">{{ goalOfTask(i.id)!.title }}</span>
      </span>
      <span v-if="isOverdue(i)" class="focus-task-late">{{ C.overdue }} · {{ fmtPlan(i.due!) }}</span>
      <span class="task-group-label">{{ i.g }}</span>
    </component>
    <button
      class="focus-task-pick"
      :disabled="busy() && fs.task?.id !== i.id"
      :aria-pressed="fs.task?.id === i.id"
      :aria-label="C.focusPick + ' ' + i.t"
      @click="onChoose(i)"
    >
      <span class="focus-task-state">
        <template v-if="fs.task?.id === i.id">{{ busy() ? C.focusPicked : C.focusChange }}</template>
        <template v-else>{{ C.focusPick }}</template>
      </span>
    </button>
    <!-- 上下移动键：与「我的目标」卡片同款——悬停浮现的圆形小键。 -->
    <span v-if="canUp || canDown" class="focus-task-move">
      <button
        v-if="canUp"
        type="button"
        class="focus-move-btn"
        :aria-label="C.focusMoveUp + ' ' + i.t"
        @click.stop="onMove(-1)"
      >
        <Icon name="arrow" :size="12" class="rot-up" />
      </button>
      <button
        v-if="canDown"
        type="button"
        class="focus-move-btn"
        :aria-label="C.focusMoveDown + ' ' + i.t"
        @click.stop="onMove(1)"
      >
        <Icon name="arrow" :size="12" class="rot-down" />
      </button>
    </span>
    <p v-if="open && hasReason" class="focus-quad-reason">
      <span class="focus-quad-reason-tag" :data-q="quad!.quadrant">{{ quadTitle }}</span>
      {{ quad!.reason }}
    </p>
  </div>
</template>
