<script setup lang="ts">
/* 热力格底下的两块小结：一行本周读数，一组分组进度。
   全栈版本：完成数来自服务端 task_events 聚合，与热力格同源。 */
import { computed } from "vue";
import { DAY, TODAY, midnight } from "../lib/todo.js";
import { best, recent, countsFromActivity } from "../lib/heat.js";
import { st } from "../store";

const c = computed(() => countsFromActivity(st.activity));
const wk = computed(() => recent(c.value, 7));
const last = computed(() => recent(c.value, 28));
const groups = computed(() =>
  Object.entries(
    st.items.reduce((m: Record<string, { n: number; d: number }>, i) => {
      const g = m[i.g] || (m[i.g] = { n: 0, d: 0 });
      g.n++;
      if (i.done) g.d++;
      return m;
    }, {}),
  ).sort((a, b) => b[1].n - a[1].n),
);
const soon = computed(() => {
  const open = st.items.filter((i) => !i.done);
  return open.filter((i) => i.due && midnight(i.due) <= TODAY + DAY * 7).length;
});
</script>

<template>
  <div class="grouphead"><span>这一周</span></div>
  <div class="wkstat">
    <div><b>{{ wk }}</b><i>本周完成</i></div>
    <div><b>{{ (last / 28).toFixed(1) }}</b><i>日均</i></div>
    <div><b>{{ best(c) }}</b><i>最长连续</i></div>
  </div>

  <div class="grouphead">
    <span>按分组</span>
    <b>{{ soon + " 件待办在一周内" }}</b>
  </div>
  <div class="glist">
    <div v-for="[g, v] in groups" :key="g" class="grow">
      <span class="nm">{{ g }}</span>
      <span class="bar"><i :style="{ width: (v.d / v.n) * 100 + '%' }" /></span>
      <span class="v">{{ v.d + "/" + v.n }}</span>
    </div>
  </div>
</template>
