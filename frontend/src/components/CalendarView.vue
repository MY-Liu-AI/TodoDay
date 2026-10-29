<script setup lang="ts">
import { computed } from "vue";
import { midnight, TODAY } from "../lib/todo.js";
import { monthDays } from "../lib/model.js";
import type { Item } from "../store";
import { goalOfTask } from "../store";
import { C } from "../copy";
import Icon from "./Icon.vue";
import { groupColor } from "../lib/palette.js";

/* 所属目标徽章：任务挂在目标下时，日程行内多一个小靶标 + 目标名。 */
const goalBadge = (id: string) => goalOfTask(id)?.title || "";

const props = defineProps<{
  items: Item[];
  month: Date;
  selected: number;
}>();
const emit = defineEmits<{
  month: [d: Date];
  pick: [d: number];
  edit: [i: Item];
  add: [d: number];
  toggle: [i: Item];
}>();

const chosen = computed(() => props.items.filter((i) => i.due && midnight(i.due) === props.selected));
const days = computed(() => monthDays(props.month));
const unscheduled = computed(() => props.items.filter((i) => !i.due));
const onDay = (ts: number) => props.items.filter((i) => i.due && midnight(i.due) === ts);
const monthTitle = computed(() =>
  props.month.toLocaleDateString(C.locale, { year: "numeric", month: "long" }),
);
const agendaTitle = computed(() =>
  new Date(props.selected).toLocaleDateString(C.locale, { month: "long", day: "numeric", weekday: "long" }),
);
const dayColors = (ts: number) => [...new Set(onDay(ts).map((i) => groupColor(i.g)))];

function prevMonth() {
  emit("month", new Date(props.month.getFullYear(), props.month.getMonth() - 1, 1));
}
function nextMonth() {
  emit("month", new Date(props.month.getFullYear(), props.month.getMonth() + 1, 1));
}
function goToday() {
  emit("month", new Date(new Date(TODAY).getFullYear(), new Date(TODAY).getMonth(), 1));
  emit("pick", TODAY);
}
</script>

<template>
  <section class="calendar-view" aria-label="calendar">
    <div class="calendar-toolbar">
      <h2>{{ monthTitle }}</h2>
      <div class="month-actions">
        <button class="icon-button prev-arrow" :aria-label="C.prevMonth" @click="prevMonth">
          <Icon name="arrow" />
        </button>
        <button class="secondary-button" @click="goToday">{{ C.today }}</button>
        <button class="icon-button" :aria-label="C.nextMonth" @click="nextMonth">
          <Icon name="arrow" />
        </button>
      </div>
    </div>
    <div class="month-weekdays">
      <span v-for="w in C.weekdays" :key="w">{{ w }}</span>
    </div>
    <div class="month-grid">
      <div
        v-for="ts in days"
        :key="ts"
        class="month-cell"
        :class="(new Date(ts).getMonth() !== month.getMonth() ? ' is-outside' : '') + (ts === selected ? ' is-selected' : '')"
      >
        <button
          class="day-number"
          :class="ts === TODAY ? 'is-today' : ''"
          :aria-label="
            new Date(ts).toLocaleDateString(C.locale, { year: 'numeric', month: 'long', day: 'numeric' }) +
            ' · ' +
            onDay(ts).length +
            ' ' +
            C.tasks
          "
          :aria-pressed="ts === selected"
          @click="emit('pick', ts)"
        >
          <span>{{ new Date(ts).getDate() }}</span>
          <small>{{ onDay(ts).length || "" }}</small>
          <span class="mobile-event-colors" aria-hidden="true">
            <i v-for="color in dayColors(ts)" :key="color" :data-color="color" />
          </span>
        </button>
        <div class="day-events">
          <button
            v-for="i in onDay(ts).slice(0, 2)"
            :key="i.id"
            class="day-event"
            :class="i.done ? 'is-done' : ''"
            :data-color="groupColor(i.g)"
            :title="i.t + ' · ' + i.g"
            :aria-label="i.t + ' · ' + i.g + (i.done ? ' · ' + C.done : '')"
            @click="emit('edit', i)"
          >
            <span class="event-dot" />
            <span>{{ i.t }}</span>
          </button>
          <button v-if="onDay(ts).length > 2" class="more-events" @click="emit('pick', ts)">
            +{{ onDay(ts).length - 2 }} {{ C.more }}
          </button>
        </div>
      </div>
    </div>
    <div class="day-agenda">
      <div class="agenda-heading">
        <div>
          <span class="overline">{{ C.agenda }}</span>
          <h3>{{ agendaTitle }}</h3>
        </div>
        <button class="secondary-button" @click="emit('add', selected)">
          <Icon name="plus" />
          {{ C.addThisDay }}
        </button>
      </div>
      <p v-if="!chosen.length" class="agenda-empty">{{ C.dayEmpty }}</p>
      <template v-else>
        <div v-for="i in chosen" :key="i.id" class="agenda-item" :data-color="groupColor(i.g)">
          <!-- 里程碑标记（ms_ 前缀）不是任务：无勾选框，点击跳目标详情。 -->
          <button
            v-if="!i.id.startsWith('ms_')"
            class="task-check"
            :class="i.done ? 'is-checked' : ''"
            :aria-label="(i.done ? C.reopen : C.complete) + ' ' + i.t"
            :aria-pressed="i.done"
            @click="emit('toggle', i)"
          >
            <Icon name="check" :size="14" />
          </button>
          <span v-else class="ms-mark" aria-hidden="true"><Icon name="target" :size="12" /></span>
          <button class="agenda-task" :class="i.done ? 'is-done' : ''" @click="emit('edit', i)">
            {{ i.t }}
            <span v-if="goalBadge(i.id)" class="goal-badge" :title="goalBadge(i.id)">
              <Icon name="target" :size="10" />
              <span class="goal-badge-label">{{ goalBadge(i.id) }}</span>
            </span>
            <span>{{ i.g }}</span>
          </button>
        </div>
      </template>
    </div>
    <details v-if="unscheduled.length" class="unscheduled">
      <summary>{{ C.unscheduled }} · {{ unscheduled.length }}</summary>
      <button v-for="i in unscheduled" :key="i.id" class="text-button" @click="emit('edit', i)">
        {{ i.t }}
      </button>
    </details>
  </section>
</template>
