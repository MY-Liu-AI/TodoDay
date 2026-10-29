<script setup lang="ts">
import { computed, ref, watch } from "vue";
import { C } from "./copy";
import {
  SORTS,
  TODAY,
  midnight,
  parseInput,
  dueText,
  visible,
} from "./lib/todo.js";
import { dateValue } from "./lib/model.js";
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
  restoreDeletedGoal,
  dismissDeletedGoal,
  restoreDeletedMilestone,
  dismissDeletedMilestone,
  requestOpenGoal,
  loadGoals,
  restoreDeletedTx,
  dismissDeletedTx,
} from "./store";
import type { Item } from "./store";
import type { WireGoalTask } from "./api";
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
import SettingsView from "./components/SettingsView.vue";
import GoalsView from "./components/GoalsView.vue";
import HabitsView from "./components/HabitsView.vue";
import LedgerView from "./components/LedgerView.vue";
import { fs, restore as restoreFocus, fmtClock } from "./focus";

const page = ref<"tasks" | "focus" | "goals" | "habits" | "ledger" | "stats">("tasks");
const mode = ref<"list" | "calendar">("calendar");
/* 左下角「设置」菜单的展开态。 */
const menuOpen = ref(false);
/* 三个配置弹框常驻挂载（dialog 走 top layer），经 ref 直接打开，
   不再经过设置页跳转。 */
const settingsRef = ref<{ openBot: () => void; openLlm: () => void; openMail: () => void } | null>(null);
function openSettings(which: "bot" | "llm" | "mail") {
  menuOpen.value = false;
  if (which === "bot") settingsRef.value?.openBot();
  else if (which === "llm") settingsRef.value?.openLlm();
  else settingsRef.value?.openMail();
}
const filter = ref("all");
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

const shown = computed(() => {
  let items = visible(st.items, filter.value, st.sort) as Item[];
  if (daySel.value) items = items.filter((i) => i.due && midnight(i.due) === daySel.value);
  return items;
});
const todayItems = computed(() => st.items.filter((i) => i.due && midnight(i.due) === TODAY));
const todayDone = computed(() => todayItems.value.filter((i) => i.done).length);
const pc = computed(() =>
  todayItems.value.length ? Math.round((todayDone.value / todayItems.value.length) * 100) : 0,
);
const open = computed(() => st.items.filter((i) => !i.done).length);
const parsed = computed(() => parseInput(draft.value));

/* 今日建议：每个进行中目标的下一个待办任务。已在今天计划里的不再建议；
   目标是「提醒有哪些目标在推进」，不是把清单再复制一遍。 */
const goalSuggestions = computed(() => {
  const todayStr = dateValue(TODAY);
  const out: { goalTitle: string; task: WireGoalTask }[] = [];
  for (const g of st.goals) {
    if (g.status !== "active") continue;
    const next = g.milestones
      .flatMap((m) => m.tasks)
      .find((t) => !t.done && t.due !== todayStr);
    if (next) out.push({ goalTitle: g.title, task: next });
  }
  return out;
});
async function suggestToday(t: WireGoalTask) {
  const item = st.items.find((i) => i.id === t.taskId);
  if (!item) return;
  if (await saveTask({ ...item, due: TODAY })) await loadGoals(true);
}

/* 里程碑日历标记：进行中目标的每个里程碑，开始/截止各成一个伪日程项
   （id 带 ms_ 前缀），与任务同 feed 进日历格。点击标记跳转到目标详情，
   而不是打开任务编辑（它不是任务）。 */
function msMarker(goalTitle: string, msTitle: string, kind: "s" | "d", dateStr: string, gid: number, msid: number, group: string): Item {
  const [y, m, d] = dateStr.split("-").map(Number);
  return {
    id: `ms_${kind}_${gid}_${msid}`,
    t: (kind === "s" ? C.goalMilestoneStartMark : C.goalMilestoneDueMark)
      .replace("{goal}", goalTitle)
      .replace("{ms}", msTitle),
    p: 3,
    due: midnight(new Date(y, m - 1, d)),
    g: group || C.personal,
    done: false,
    at: 0,
    version: 0,
  };
}
const calendarFeed = computed(() => {
  const out = [...st.items];
  for (const g of st.goals) {
    if (g.status !== "active") continue;
    for (const m of g.milestones) {
      if (m.start) out.push(msMarker(g.title, m.title, "s", m.start, g.id, m.id, g.group));
      if (m.due) out.push(msMarker(g.title, m.title, "d", m.due, g.id, m.id, g.group));
    }
  }
  return out;
});
/* 日历格点击：真任务打开编辑；里程碑标记跳目标详情页。 */
function onCalendarEdit(i: Item) {
  if (i.id.startsWith("ms_")) {
    page.value = "goals";
    requestOpenGoal(Number(i.id.split("_")[2]));
    return;
  }
  editing.value = i;
}

/* 页头文案（单行 slim 版）：标题与同排副标题在左，主操作在右。
   overline 已下线——侧栏激活态本身就是位置信息，不再重复。 */
const PAGE_META: Record<string, { title: string; subtitle: string }> = {
  tasks: { title: C.myTasks, subtitle: C.subtitle },
  focus: { title: C.focus, subtitle: C.focusSubtitle },
  goals: { title: C.goals, subtitle: C.goalsSubtitle },
  habits: { title: C.habits, subtitle: C.habitsSubtitle },
  stats: { title: C.insights, subtitle: C.statsSubtitle },
  ledger: { title: C.ledger, subtitle: C.ledgerSubtitle },
};
const meta = computed(() => PAGE_META[page.value] ?? PAGE_META.tasks);

function toggle(i: Item) {
  toggleTask(i);
  i.done ? sound.tick() : sound.ding();
  haptic(14);
}

function newTask(due = 0) {
  editing.value = { id: "", t: "", due, g: C.personal, p: 3, done: false, at: 0, version: 0 };
}

function clear() {
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
    g: parsed.value.g,
  });
  if (!ok) return;
  draft.value = "";
  error.value = "";
  notify(C.added);
  filter.value = "all";
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
  refreshStats(true);
}

function switchMode(v: "list" | "calendar") {
  mode.value = v;
  daySel.value = 0;
  filter.value = "all";
}

/* 启动即检查会话：已登录直接拉任务，未登录落在 AuthView。 */
initSession();

/* 会话就绪后接上上次没走完的番茄倒计时（任务列表已到齐才能还原绑定的任务）。 */
watch(
  () => [st.ready, st.authed, st.items.length] as const,
  () => {
    if (st.ready && st.authed) restoreFocus();
  },
  { immediate: true },
);

/* 倒计时在标签页标题里可见：切到别的模块、甚至切走标签页，也看得到剩余时间。 */
const baseTitle = document.title;
watch(
  () => [fs.phase, fs.remaining] as const,
  ([p, r]) => {
    document.title = p === "running" || p === "paused" ? `${fmtClock(r)} · ${baseTitle}` : baseTitle;
  },
  { immediate: true },
);
</script>

<template>
  <AuthView v-if="st.ready && !st.authed" />
  <div v-else-if="st.ready" class="planner-shell">
    <a class="skip-link" href="#workspace">{{ C.myTasks }}</a>
    <aside class="planner-sidebar">
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
        <button :class="page === 'goals' ? 'is-active' : ''" @click="page = 'goals'">
          <Icon name="target" />
          {{ C.goals }}
        </button>
        <button :class="page === 'habits' ? 'is-active' : ''" @click="page = 'habits'">
          <Icon name="repeat" />
          {{ C.habits }}
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
        <button :class="page === 'ledger' ? 'is-active' : ''" @click="page = 'ledger'">
          <Icon name="ledger" />
          {{ C.ledger }}
        </button>
        <!-- 窄屏侧栏折叠：设置入口挪到导航行，桌面端由 CSS 隐藏 -->
        <div class="settings-menu nav-settings">
          <button class="settings-trigger" :aria-expanded="menuOpen" @click="menuOpen = !menuOpen">
            <Icon name="gear" />
          </button>
          <div v-if="menuOpen" class="settings-backdrop" @click="menuOpen = false" />
          <div v-if="menuOpen" class="settings-popup" role="menu">
            <button role="menuitem" @click="openSettings('bot')">
              <Icon name="robot" />
              {{ C.assistantTitle }}
            </button>
            <button role="menuitem" @click="openSettings('llm')">
              <Icon name="ai" />
              {{ C.assistantAiTitle }}
            </button>
            <button role="menuitem" @click="openSettings('mail')">
              <Icon name="mail" />
              {{ C.mailTitle }}
            </button>
            <button role="menuitem" @click="setTheme(theme === 'light' ? 'dark' : 'light')">
              <Icon :name="theme === 'light' ? 'moon' : 'sun'" />
              {{ theme === "light" ? C.themeDark : C.themeLight }}
            </button>
          </div>
        </div>
      </nav>
      <div class="sidebar-bottom">
        <div class="settings-menu">
          <button class="settings-trigger" :aria-expanded="menuOpen" @click="menuOpen = !menuOpen">
            <Icon name="gear" />
            {{ C.settingsMenu }}
          </button>
          <!-- 透明遮罩：点菜单外任意处收起 -->
          <div v-if="menuOpen" class="settings-backdrop" @click="menuOpen = false" />
          <div v-if="menuOpen" class="settings-popup" role="menu">
            <button role="menuitem" @click="openSettings('bot')">
              <Icon name="robot" />
              {{ C.assistantTitle }}
            </button>
            <button role="menuitem" @click="openSettings('llm')">
              <Icon name="ai" />
              {{ C.assistantAiTitle }}
            </button>
            <button role="menuitem" @click="openSettings('mail')">
              <Icon name="mail" />
              {{ C.mailTitle }}
            </button>
            <button role="menuitem" @click="setTheme(theme === 'light' ? 'dark' : 'light')">
              <Icon :name="theme === 'light' ? 'moon' : 'sun'" />
              {{ theme === "light" ? C.themeDark : C.themeLight }}
            </button>
          </div>
        </div>
      </div>
    </aside>
    <main id="workspace" class="planner-workspace">
      <!-- 目标详情页自带标题与返回入口，页头整块隐藏。 -->
      <header v-if="page !== 'goals' || !st.goalsViewing" class="workspace-header">
        <div class="workspace-header-text">
          <h1>{{ meta.title }}</h1>
          <p>{{ meta.subtitle }}</p>
        </div>
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
          <button class="primary-button workspace-cta new-task" @click="newTask(mode === 'calendar' ? calendarDay : daySel)">
            <Icon name="plus" />
            {{ C.newTask }}
          </button>
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
            <section v-if="goalSuggestions.length" class="goal-suggest">
              <div class="task-group-heading">
                <h2>{{ C.goalSuggestTitle }}</h2>
                <span>{{ goalSuggestions.length }}</span>
                <i />
              </div>
              <div v-for="s in goalSuggestions" :key="s.task.taskId" class="goal-suggest-row">
                <span class="goal-badge" :title="s.goalTitle">
                  <Icon name="target" :size="10" />
                  <span class="goal-badge-label">{{ s.goalTitle }}</span>
                </span>
                <span class="goal-suggest-title">{{ s.task.title }}</span>
                <button class="text-button" @click="suggestToday(s.task)">{{ C.goalSuggestAdd }}</button>
              </div>
            </section>
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
              :items="calendarFeed"
              :month="month"
              :selected="calendarDay"
              @month="(d) => (month = d)"
              @pick="(d) => (calendarDay = d)"
              @add="newTask"
              @edit="onCalendarEdit"
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
      <GoalsView v-else-if="page === 'goals'" />
      <HabitsView v-else-if="page === 'habits'" />
      <LedgerView v-else-if="page === 'ledger'" />
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
      <!-- 三个配置弹框常驻挂载：侧栏「设置」菜单直接打开，不经页面跳转。
           dialog 走 top layer，放 main 末尾不影响布局。 -->
      <SettingsView ref="settingsRef" />
    </main>
    <div v-if="st.notice || st.deleted || st.deletedGoal || st.deletedMilestone || st.deletedTx || st.importPrompt" class="feedback-toast" role="status">
      <Icon name="check" :size="16" />
      <template v-if="st.importPrompt">
        <span>{{ C.importOffer.replace("{n}", String(st.importPrompt.count)) }}</span>
        <button @click="confirmImport">{{ C.importNow }}</button>
        <button class="icon-button" :aria-label="C.importSkip" @click="skipImport">
          <Icon name="close" :size="14" />
        </button>
      </template>
      <template v-else-if="st.deletedGoal">
        <span>{{ C.goalDeleted }}</span>
        <button @click="restoreDeletedGoal">{{ C.undo }}</button>
        <button class="icon-button" :aria-label="C.close" @click="dismissDeletedGoal">
          <Icon name="close" :size="14" />
        </button>
      </template>
      <template v-else-if="st.deletedMilestone">
        <span>{{ C.goalMilestoneDeleted }}{{ st.deletedMilestone.milestone.title ? " · " + st.deletedMilestone.milestone.title : "" }}</span>
        <button @click="restoreDeletedMilestone">{{ C.goalUndo }}</button>
        <button class="icon-button" :aria-label="C.close" @click="dismissDeletedMilestone">
          <Icon name="close" :size="14" />
        </button>
      </template>
      <template v-else-if="st.deletedTx">
        <span>{{ C.txDeleted }}{{ st.deletedTx.note ? " · " + st.deletedTx.note : "" }}</span>
        <button @click="restoreDeletedTx">{{ C.undo }}</button>
        <button class="icon-button" :aria-label="C.close" @click="dismissDeletedTx">
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
