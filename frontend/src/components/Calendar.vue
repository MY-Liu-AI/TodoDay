<script setup lang="ts">
/* 一天一格，格子底下是那天的负载。点一格就把列表收到那一天，再点一次放开 ——
   日历在这里是筛选器，不是另一个视图，所以它和下面那张列表永远是同一份数据。 */
import { computed } from "vue";
import { DAY, TODAY, WD, loadOn, midnight } from "../lib/todo.js";
import { sound } from "../lib/fx";
import type { Item } from "../store";

const props = defineProps<{
  items: Item[];
  month: Date;
  daySel: number;
}>();
const emit = defineEmits<{
  month: [d: Date];
  pick: [ts: number];
}>();

/* 周一开头。getDay() 里周日是 0，直接拿来当偏移会把整个月错开一格。 */
const cells = computed(() => {
  const first = new Date(props.month);
  const lead = (first.getDay() + 6) % 7;
  const start = midnight(+first - lead * DAY);
  return Array.from({ length: 42 }, (_, k) => {
    const ts = midnight(start + k * DAY);
    const d = new Date(ts);
    const load = loadOn(props.items, ts);
    const dots: string[] = [];
    const add = (cls: string, n: number) => {
      for (let i = 0; i < Math.min(n, 3); i++) dots.push(cls);
    };
    add("late", load.late);
    add("", load.open);
    add("did", load.did);
    return { ts, d, dots, outside: d.getMonth() !== props.month.getMonth(), today: ts === TODAY, on: ts === props.daySel };
  });
});

const title = computed(() => props.month.getFullYear() + " 年 " + (props.month.getMonth() + 1) + " 月");

function shiftMonth(delta: number) {
  const d = new Date(props.month);
  d.setMonth(d.getMonth() + delta);
  emit("month", d);
}
function goToday() {
  const d = new Date(TODAY);
  d.setDate(1);
  emit("month", d);
  emit("pick", TODAY);
}
function pick(cell: { ts: number; on: boolean }) {
  emit("pick", cell.on ? 0 : cell.ts);
  if (!cell.on) {
    const m = new Date(cell.ts);
    m.setDate(1);
    emit("month", m);
  }
  sound.tick();
}
</script>

<template>
  <div class="cal">
    <div class="calhead">
      <div id="calm" class="m">{{ title }}</div>
      <button id="calprev" class="miniib" title="上个月" @click="shiftMonth(-1)">
        <svg viewBox="0 0 24 24"><path d="M15 5l-7 7 7 7" /></svg>
      </button>
      <button id="calnext" class="miniib" title="下个月" @click="shiftMonth(1)">
        <svg viewBox="0 0 24 24"><path d="M9 5l7 7-7 7" /></svg>
      </button>
      <button id="caltoday" class="chip" @click="goToday">今天</button>
    </div>

    <div id="calwd" class="calgrid">
      <div v-for="(w, n) in WD" :key="n" class="wd">{{ w }}</div>
    </div>

    <div id="calgrid" class="calgrid">
      <button
        v-for="(cell, k) in cells"
        :key="k"
        type="button"
        class="cell"
        :class="(cell.outside ? ' out' : '') + (cell.today ? ' today' : '') + (cell.on ? ' on' : '')"
        :aria-current="cell.on ? 'true' : undefined"
        :aria-label="cell.d.toLocaleDateString('zh-CN', { month: 'long', day: 'numeric' })"
        @click="pick(cell)"
      >
        <span>{{ cell.d.getDate() }}</span>
        <span class="dots">
          <i v-for="(c, i) in cell.dots" :key="i" :class="c || undefined" />
        </span>
      </button>
    </div>
  </div>
</template>
