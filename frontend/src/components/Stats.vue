<script setup lang="ts">
/* 统计块：全部/未完成/已完成/逾期 + 近7天完成柱图 + 分组读数。
   近 7 天完成数与服务端 activity 同源（task_events，工作区时区）。 */
import { computed } from "vue";
import { DAY, TODAY, midnight } from "../lib/todo.js";
import { countsFromActivity } from "../lib/heat.js";
import { st } from "../store";

const open = computed(() => st.items.filter((i) => !i.done));
const done = computed(() => st.items.filter((i) => i.done));
const late = computed(() => open.value.filter((i) => i.due && midnight(i.due) < TODAY));

const week = computed(() => {
  const counts = countsFromActivity(st.activity);
  const out: { d: number; n: number }[] = [];
  for (let k = 6; k >= 0; k--) {
    const d = TODAY - k * DAY;
    out.push({ d, n: counts[d] || 0 });
  }
  return out;
});
const max = computed(() => Math.max(1, ...week.value.map((w) => w.n)));

const byG = computed(() => {
  const m: Record<string, { n: number; d: number }> = {};
  st.items.forEach((i) => {
    m[i.g] = m[i.g] || { n: 0, d: 0 };
    m[i.g].n++;
    if (i.done) m[i.g].d++;
  });
  return Object.entries(m).sort((a, b) => b[1].n - a[1].n);
});
</script>

<template>
  <div class="stats">
    <div>
      <div class="n">{{ st.items.length }}</div>
      <div class="l">全部</div>
    </div>
    <div>
      <div class="n">{{ open.length }}</div>
      <div class="l">未完成</div>
    </div>
    <div>
      <div class="n">{{ done.length }}</div>
      <div class="l">已完成</div>
    </div>
    <div>
      <div class="n" :style="late.length ? { color: 'var(--neg)' } : undefined">{{ late.length }}</div>
      <div class="l">逾期</div>
    </div>
  </div>

  <div class="grouphead"><span>近 7 天完成</span></div>
  <div class="weekbar">
    <div v-for="w in week" :key="w.d" class="d">
      <div class="b" :style="{ height: (w.n / max) * 58 + 2 + 'px' }" />
      <div class="l">{{ "日一二三四五六"[new Date(w.d).getDay()] }}</div>
    </div>
  </div>

  <div class="grouphead"><span>按分组</span></div>
  <div v-for="[g, v] in byG" :key="g" class="item" style="--pri: transparent">
    <div class="body">
      <div class="txt">{{ g }}</div>
      <div class="meta"><span>{{ v.d + " / " + v.n + " 已完成" }}</span></div>
    </div>
    <div class="bar" style="width: 80px; align-self: center">
      <i :style="{ width: (v.d / v.n) * 100 + '%' }" />
    </div>
  </div>
  <div style="height: 16px" />
</template>
