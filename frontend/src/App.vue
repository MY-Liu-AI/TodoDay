<script setup lang="ts">
import { computed, ref } from "vue";
import { C } from "./copy";
import {
  SORTS,
  TODAY,
  midnight,
  parseInput,
  dueText,
  visible,
} from "./lib/todo.js";
import { matches } from "./lib/model.js";
import { groupColor } from "./lib/palette.js";
import {
  st,
  notify,
  setSort,
  addTask,
  saveTask,
  toggleTask,
  removeTask,
  clearDone,
  restoreDeleted,
  dismissDeleted,
  reorderTasks,
  refreshStats,
  retryLoad,
  confirmImport,
  skipImport,
  initSession,
} from "./store";
import type { Item } from "./store";
import { download, toCSV, poster, showPoster } from "./lib/export";
import { sound, haptic } from "./lib/fx";
import Icon from "./components/Icon.vue";
import Calendar from "./components/Calendar.vue";
import CalendarView from "./components/CalendarView.vue";
import List from "./components/List.vue";
import Heat from "./components/Heat.vue";
import Recap from "./components/Recap.vue";
import Stats from "./components/Stats.vue";
import TaskDialog from "./components/TaskDialog.vue";
import AuthView from "./components/AuthView.vue";
import FocusView from "./components/FocusView.vue";
import { fs } from "./focus";

const page = ref<"tasks" | "focus" | "stats">("tasks");
const mode = ref<"list" | "calendar">("calendar");
const filter = ref("all");
const group = ref("");
const query = ref("");
const month = ref(new Date(new Date(TODAY).getFullYear(), new Date(TODAY).getMonth(), 1));
const daySel = ref(0);
const calendarDay = ref(TODAY);
const draft = ref("");
const error = ref("");
const editing = ref<Item | null>(null);
const exporting = ref(false);
const theme = ref<"light" | "dark">(
  (() => {
    try {
      return localStorage.getItem("todo-day-theme") === "dark" ? "dark" : "light";
    } catch {
      return "light";
    }
  })(),
);

function setTheme(next: "light" | "dark") {
  theme.value = next;
  document.documentElement.dataset.theme = next;
  try {
    localStorage.setItem("todo-day-theme", next);
  } catch {
    /* ignore */
  }
}
setTheme(theme.value);

const filtered = computed(() => matches(st.items, query.value, group.value) as Item[]);
const shown = computed(() => {
  let items = visible(filtered.value, filter.value, st.sort) as Item[];
  if (daySel.value) items = items.filter((i) => i.due && midnight(i.due) === daySel.value);
  return items;
});
const todayItems = computed(() => st.items.filter((i) => i.due && midnight(i.due) === TODAY));
const todayDone = computed(() => todayItems.value.filter((i) => i.done).length);
const pc = computed(() =>
  todayItems.value.length ? Math.round((todayDone.value / todayItems.value.length) * 100) : 0,
);
const open = computed(() => st.items.filter((i) => !i.done).length);
const groups = computed(() => Array.from(new Set(st.items.map((i) => i.g))).filter(Boolean));
const parsed = computed(() => parseInput(draft.value));

function toggle(i: Item) {
  toggleTask(i);
  i.done ? sound.tick() : sound.ding();
  haptic(14);
}

function newTask(due = 0) {
  editing.value = { id: "", t: "", due, g: group.value || C.personal, p: 3, done: false, at: 0, version: 0 };
}

function clear() {
  query.value = "";
  group.value = "";
  filter.value = "all";
  daySel.value = 0;
}

async function add() {
  if (!parsed.value.t) {
    error.value = C.titleError;
    return;
  }
  const ok = await addTask({
    t: parsed.value.t,
    p: parsed.value.p,
    due: parsed.value.due || (mode.value === "calendar" ? calendarDay.value : daySel.value),
    g: group.value || parsed.value.g,
  });
  if (!ok) return;
  draft.value = "";
  error.value = "";
  notify(C.added);
  filter.value = "all";
  query.value = "";
  sound.pop();
}

async function save(item: Item) {
  const ok = await saveTask(item);
  if (ok) editing.value = null;
}

async function remove(item: Item) {
  editing.value = null;
  await removeTask(item);
}

function exportCSV() {
  const cell = (v: unknown) => {
    const s = String(v ?? "");
    return /^[=+@-]/.test(s) ? "'" + s : s;
  };
  download(
    "todo-day.csv",
    toCSV([
      [C.taskName, C.group, C.priority, C.date, C.done],
      ...st.items.map((i) =>
        [
          i.t,
          i.g,
          [C.low, C.high, C.medium, C.low][i.p],
          i.due ? new Date(i.due).toLocaleDateString(C.locale) : "",
          i.done ? C.done : C.open,
        ].map(cell),
      ),
    ]),
  );
  notify(C.exported);
}

async function makePoster() {
  exporting.value = true;
  try {
    showPoster(
      await poster({
        brand: "Todo Day",
        title: C.myTasks,
        subtitle: new Date().toLocaleDateString(C.locale),
        hero: { value: String(pc.value), unit: "%", caption: C.dailyProgress },
        rows: st.items
          .filter((i) => !i.done)
          .slice(0, 7)
          .map((i) => [i.t, dueText(i) || C.noDue]),
        footer: C.subtitle,
      }),
      { name: "todo-day.png" },
    );
  } catch {
    notify(C.exportError, "error");
  } finally {
    exporting.value = false;
  }
}

function monthPick(ts: number) {
  daySel.value = ts;
  filter.value = "all";
}

function printPage() {
  window.print();
}

function goStats() {
  page.value = "stats";
  refreshStats();
}

function switchMode(v: "list" | "calendar") {
  mode.value = v;
  daySel.value = 0;
  filter.value = "all";
}

/* 启动即检查会话：已登录直接拉任务，未登录落在 AuthView。 */
initSession();
</script>

<template>
  <AuthView v-if="st.ready && !st.authed" />
  <div v-else-if="st.ready" class="planner-shell">
    <a class="skip-link" href="#workspace">{{ C.myTasks }}</a>
    <aside class="planner-sidebar">
      <div class="brand-lockup">
        <span class="brand-mark">
          <Icon name="check" :size="22" />
        </span>
        <div>
          <strong>Todo Day<span class="brand-period">.</span></strong>
          <p>{{ C.brand }}</p>
        </div>
      </div>
      <div class="workspace-label">
        <span class="workspace-avatar">{{ (st.user?.username || "P").slice(0, 1).toUpperCase() }}</span>
        <div>
          {{ st.workspace?.name || C.workspace }}
          <small>{{ C.localSaved }}</small>
        </div>
      </div>
      <nav aria-label="workspace" class="planner-nav">
        <button :class="page === 'tasks' ? 'is-active' : ''" @click="page = 'tasks'">
          <Icon name="list" />
          {{ C.plan }}
          <span>{{ open }}</span>
        </button>
        <button :class="page === 'focus' ? 'is-active' : ''" @click="page = 'focus'">
          <Icon name="timer" />
          {{ C.focus }}
          <span v-if="fs.phase === 'running' || fs.phase === 'paused'">{{ Math.ceil(fs.remaining / 60) }}'</span>
        </button>
        <button :class="page === 'stats' ? 'is-active' : ''" @click="goStats">
          <Icon name="stats" />
          {{ C.insights }}
        </button>
      </nav>
      <div class="group-navigation">
        <span class="nav-caption">{{ C.projects }}</span>
        <button
          :class="!group ? 'is-current' : ''"
          @click="
            group = '';
            page = 'tasks';
          "
        >
          <span class="group-marker" />
          {{ C.allGroups }}
        </button>
        <button
          v-for="g in groups"
          :key="g"
          :data-color="groupColor(g)"
          :class="group === g ? 'is-current' : ''"
          @click="
            group = g;
            page = 'tasks';
          "
        >
          <span class="group-marker" />
          <span>{{ g }}</span>
          <small>{{ st.items.filter((i) => i.g === g && !i.done).length }}</small>
        </button>
      </div>
      <div class="sidebar-bottom">
        <button class="theme-button" @click="setTheme(theme === 'light' ? 'dark' : 'light')">
          <Icon :name="theme === 'light' ? 'moon' : 'sun'" />
          {{ theme === "light" ? C.themeDark : C.themeLight }}
        </button>
      </div>
    </aside>
    <main id="workspace" class="planner-workspace">
      <header class="workspace-header">
        <div>
          <div class="overline">PERSONAL / {{ page === "tasks" ? "PLANNER" : page === "focus" ? "FOCUS" : "INSIGHTS" }}</div>
          <h1>{{ page === "tasks" ? C.myTasks : page === "focus" ? C.focus : C.insights }}</h1>
          <p>{{ page === "tasks" ? C.subtitle : page === "focus" ? C.focusSubtitle : C.statsSubtitle }}</p>
        </div>
        <button v-if="page !== 'focus'" class="primary-button new-task" @click="newTask(mode === 'calendar' ? calendarDay : daySel)">
          <Icon name="plus" />
          {{ C.newTask }}
        </button>
      </header>
      <p v-if="st.loadError" role="alert" class="storage-warning">
        {{ st.loadError }}
        <button class="text-button" @click="retryLoad">{{ C.retry }}</button>
      </p>
      <template v-if="page === 'tasks'">
        <div class="workspace-toolbar">
          <div class="view-switch" aria-label="plan">
            <button
              v-for="[v, label, icon] in [
                ['list', C.todo, 'list'],
                ['calendar', C.calendar, 'calendar'],
              ] as const"
              :key="v"
              :aria-pressed="mode === v"
              :class="mode === v ? 'is-active' : ''"
              @click="switchMode(v)"
            >
              <Icon :name="icon" />
              {{ label }}
            </button>
          </div>
          <label class="task-search">
            <Icon name="search" :size="16" />
            <input v-model="query" type="search" :aria-label="C.search" :placeholder="C.searchPlaceholder" />
          </label>
        </div>
        <div class="work-layout" :class="mode === 'calendar' ? 'is-calendar' : ''">
          <div class="task-work">
            <form
              class="quick-composer"
              @submit.prevent="
                error = '';
                add();
              "
            >
              <div class="quick-input">
                <Icon name="plus" />
                <input
                  v-model="draft"
                  :aria-label="C.quick"
                  :aria-invalid="!!error"
                  :aria-describedby="error ? 'quick-error' : 'quick-help'"
                  maxlength="240"
                  :placeholder="C.quickPlaceholder"
                  @input="error = ''"
                />
                <button type="submit">{{ C.add }}<span>↵</span></button>
              </div>
              <p id="quick-help">
                {{
                  draft.trim()
                    ? (parsed.due ? new Date(parsed.due).toLocaleDateString(C.locale) : C.noDue) +
                      " · " +
                      parsed.g +
                      " · " +
                      (parsed.p === 1 ? C.high : parsed.p === 2 ? C.medium : C.low)
                    : C.quickHelp
                }}
              </p>
              <p v-if="error" id="quick-error" role="alert" class="form-error">{{ error }}</p>
            </form>
            <template v-if="mode === 'list'">
              <div class="task-controls">
                <div class="status-filters" aria-label="myTasks">
                  <button
                    v-for="[v, label] in [
                      ['all', C.all],
                      ['today', C.today],
                      ['open', C.open],
                      ['done', C.done],
                    ] as const"
                    :key="v"
                    :class="filter === v ? 'is-active' : ''"
                    :aria-pressed="filter === v"
                    @click="
                      filter = v;
                      daySel = 0;
                    "
                  >
                    {{ label }}
                  </button>
                </div>
                <label class="sort-control">
                  {{ C.sort }}
                  <select aria-label="sort" :value="st.sort" @change="setSort(($event.target as HTMLSelectElement).value as any)">
                    <option v-for="(label, v) in SORTS" :key="v" :value="v">{{ label }}</option>
                  </select>
                </label>
              </div>
              <div v-if="daySel" class="date-filter">
                {{ new Date(daySel).toLocaleDateString(C.locale, { month: "long", day: "numeric" }) }}
                <button class="text-button" @click="daySel = 0">{{ C.clearDate }} <Icon name="close" :size="12" /></button>
              </div>
              <List
                v-if="shown.length"
                :items="shown"
                :sort="st.sort"
                @toggle="toggle"
                @edit="(i) => (editing = i)"
                @reorder="(from, to) => reorderTasks(from, to)"
              />
              <div v-else-if="!st.loading" class="task-empty">
                <Icon name="check" :size="32" />
                <h2>{{ C.empty }}</h2>
                <p>{{ C.emptyHint }}</p>
                <button class="secondary-button" @click="clear">{{ C.clearFilters }}</button>
              </div>
              <div class="list-footer">
                <span>{{ shown.length }} {{ C.countSuffix }}</span>
                <button class="text-button" @click="exportCSV">
                  <Icon name="download" :size="14" />
                  {{ C.exportCsv }}
                </button>
              </div>
            </template>
            <CalendarView
              v-else
              :items="filtered"
              :month="month"
              :selected="calendarDay"
              @month="(d) => (month = d)"
              @pick="(d) => (calendarDay = d)"
              @add="newTask"
              @edit="(i) => (editing = i)"
              @toggle="toggle"
            />
          </div>
          <aside v-if="mode === 'list'" class="today-aside">
            <div class="overview-heading">
              <h2>{{ C.overview }}</h2>
              <span>{{
                new Date().toLocaleDateString(C.locale, { month: "short", day: "numeric" })
              }}</span>
            </div>
            <div class="progress-summary">
              <div class="ring">
                <svg width="66" height="66" viewBox="0 0 66 66">
                  <circle class="bgc" cx="33" cy="33" r="28" />
                  <circle class="fg" cx="33" cy="33" r="28" stroke-dasharray="175.9" :stroke-dashoffset="175.9 * (1 - pc / 100)" />
                </svg>
                <span class="pc">{{ pc }}%</span>
              </div>
              <div>
                <strong>{{ todayDone }} / {{ todayItems.length }}</strong>
                <p>{{ C.dailyProgress }}</p>
              </div>
            </div>
            <div class="overview-counts">
              <div>
                <b>{{ open }}</b>
                <span>{{ C.pending }}</span>
              </div>
              <div>
                <b>{{ todayItems.filter((i) => !i.done).length }}</b>
                <span>{{ C.todayDue }}</span>
              </div>
              <div>
                <b>{{ st.items.filter((i) => i.done).length }}</b>
                <span>{{ C.completed }}</span>
              </div>
            </div>
            <div class="mini-calendar-label">{{ C.dateFilter }}</div>
            <Calendar :items="st.items" :month="month" :day-sel="daySel" @month="(d) => (month = d)" @pick="monthPick" />
          </aside>
        </div>
      </template>
      <FocusView v-else-if="page === 'focus'" />
      <div v-else class="insights-layout">
        <section>
          <p class="history-note">{{ C.historyNote }}</p>
          <Heat />
          <Recap />
        </section>
        <section>
          <Stats />
          <div class="export-actions">
            <button class="secondary-button" @click="clearDone">{{ C.clearDone }}</button>
            <button class="secondary-button" @click="exportCSV">{{ C.exportCsv }}</button>
            <button class="secondary-button" :disabled="exporting" @click="makePoster">
              {{ C.poster }}{{ exporting ? "…" : "" }}
            </button>
            <button class="secondary-button" @click="printPage">{{ C.pdf }}</button>
          </div>
        </section>
      </div>
      <footer class="workspace-footer">
        <span>TODO DAY <span>—</span> {{ C.subtitle }}</span>
        <span>{{ new Date().getFullYear() }}</span>
      </footer>
    </main>
    <div v-if="st.notice || st.deleted || st.importPrompt" class="feedback-toast" role="status">
      <Icon name="check" :size="16" />
      <template v-if="st.importPrompt">
        <span>{{ C.importOffer.replace("{n}", String(st.importPrompt.count)) }}</span>
        <button @click="confirmImport">{{ C.importNow }}</button>
        <button class="icon-button" :aria-label="C.importSkip" @click="skipImport">
          <Icon name="close" :size="14" />
        </button>
      </template>
      <template v-else>
        <span>{{ st.notice || C.deleted }}</span>
        <button v-if="st.deleted" @click="restoreDeleted">{{ C.undo }}</button>
        <button v-if="st.deleted" class="icon-button" :aria-label="C.close" @click="dismissDeleted">
          <Icon name="close" :size="14" />
        </button>
      </template>
    </div>
    <TaskDialog v-if="editing" :key="editing.id || 'new'" :item="editing" @save="save" @delete="remove" @close="editing = null" />
  </div>
  <div v-else></div>
</template>
