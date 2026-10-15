<script setup lang="ts">
/* 完成热力格：列是周，行是周一到周日，和上面那本月历同一个起点。
   全栈版本：格子数据来自服务端 task_events（工作区时区口径），无合成历史。 */
import { computed, ref } from "vue";
import { TODAY } from "../lib/todo.js";
import { DAYS, WEEKS, countsFromActivity, level, streak } from "../lib/heat.js";
import { st } from "../store";

const WDL = ["一", "", "三", "", "五", "", ""];
const fmt = (ts: number) => new Date(ts).toLocaleDateString("zh-CN", { month: "long", day: "numeric" });

/* 悬停哪一格，底下那行就报哪一天。系统 tooltip 要停留一秒才出来，等于没人
   知道这些格子是可以问的。 */
const hov = ref(0);
const c = computed(() => countsFromActivity(st.activity));
const total = computed(() => DAYS.reduce((s: number, ts: number) => s + (ts <= TODAY ? (c.value[ts] || 0) : 0), 0));
const run = computed(() => streak(c.value));

const months = computed(() => {
  /* 月份只在换月的那一列标一次。最左那列不标：它多半是半截月份，
     标出来只会和第二列的标签挤在一起。 */
  const out: ({ w: number; label: string } | null)[] = [];
  for (let w = 0; w < WEEKS; w++) {
    const m = new Date(DAYS[w * 7]).getMonth();
    if (!w || m === new Date(DAYS[w * 7 - 7]).getMonth()) out.push(null);
    else out.push({ w, label: m + 1 + " 月" });
  }
  return out.filter(Boolean) as { w: number; label: string }[];
});
</script>

<template>
  <div class="grouphead">
    <span>完成热力</span>
    <b>近 {{ WEEKS }} 周 {{ total }} 件</b>
  </div>

  <div class="heat">
    <div class="heathint">一格一天，颜色越深，那天勾掉的事越多。</div>
    <div class="heatgrid" @mouseleave="hov = 0">
      <template v-for="(w, d) in WDL" :key="d">
        <span v-if="w" class="hwd" :style="{ gridColumn: 1, gridRow: d + 2 }">{{ w }}</span>
      </template>

      <span v-for="m in months" :key="'m' + m.w" class="hmon" :style="{ gridColumn: m.w + 2, gridRow: 1 }">{{ m.label }}</span>

      <button
        v-for="(ts, k) in DAYS"
        :key="ts"
        type="button"
        :disabled="ts > TODAY"
        :aria-label="fmt(ts) + ' · ' + (c[ts] || 0)"
        class="hc"
        :class="'l' + (ts > TODAY ? 'x' : level(c[ts] || 0)) + (ts === hov ? ' on' : '')"
        :style="{ gridColumn: Math.floor(k / 7) + 2, gridRow: (k % 7) + 2 }"
        @focus="hov = ts"
        @click="hov = ts"
        @mouseenter="ts <= TODAY && (hov = ts)"
      />
    </div>

    <div class="heatfoot">
      <span>{{ hov ? `${fmt(hov)} · 完成 ${c[hov] || 0} 件` : run ? `连续 ${run} 天有进展` : "今天还没勾掉任何一件" }}</span>
      <span class="hleg">少<i v-for="l in [0, 1, 2, 3, 4]" :key="l" class="hc" :class="'l' + l" />多</span>
    </div>
  </div>
</template>
