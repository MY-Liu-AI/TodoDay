import { useEffect, useMemo, useRef, useState } from "react";
import Kit from "./lib/kit.js";
import Tool from "./lib/tool.js";
import {
  SORTS,
  TODAY,
  midnight,
  parseInput,
  seed,
  enrichSampleState,
  visible,
  dueText,
} from "./lib/todo.js";
import {
  matches,
  reorder,
  validState,
  removeItems,
  restoreItems,
} from "./lib/model.js";
import type { Item, State } from "./lib/types";
import { C } from "./copy";
import Icon from "./components/Icon";
import Calendar from "./components/Calendar";
import CalendarView from "./components/CalendarView";
import List from "./components/List";
import Heat from "./components/Heat";
import Recap from "./components/Recap";
import Stats from "./components/Stats";
import TaskDialog from "./components/TaskDialog";
import { groupColor } from "./lib/palette.js";

// Local-only adapter. Production agents must follow ../FULLSTACK.md.
// Keep the legacy storage key so existing task data remains readable.
const STORAGE = "todo-day-demo-" + C.lang;
function load() {
  try {
    const raw =
      localStorage.getItem(STORAGE) ?? localStorage.getItem("todo-day");
    if (raw) {
      const data = JSON.parse(raw);
      if (validState(data))
        return { data: enrichSampleState(data) as State, error: "", writable: true };
      return {
        data: seed() as State,
        error: C.storageInvalid,
        writable: false,
      };
    }
    return { data: seed() as State, error: "", writable: true };
  } catch {
    return { data: seed() as State, error: C.storageInvalid, writable: false };
  }
}
export default function App() {
  const [initial] = useState(load);
  const [st, setSt] = useState<State>(initial.data);
  const [storageError, setStorageError] = useState<string>(initial.error);
  const [page, setPage] = useState<"tasks" | "stats">("tasks");
  const [mode, setMode] = useState<"list" | "calendar">("calendar");
  const [filter, setFilter] = useState("all"),
    [group, setGroup] = useState(""),
    [query, setQuery] = useState("");
  const [month, setMonth] = useState(
    () =>
      new Date(new Date(TODAY).getFullYear(), new Date(TODAY).getMonth(), 1),
  );
  const [daySel, setDaySel] = useState(0),
    [calendarDay, setCalendarDay] = useState(TODAY);
  const [draft, setDraft] = useState(""),
    [error, setError] = useState(""),
    [notice, setNotice] = useState("");
  const [editing, setEditing] = useState<Item | null>(null),
    [deleted, setDeleted] = useState<Item[] | null>(null);
  const [exporting, setExporting] = useState(false);
  const [theme, setTheme] = useState(() => {
    try {
      return localStorage.getItem("todo-day-theme") === "dark"
        ? "dark"
        : "light";
    } catch {
      return "light";
    }
  });
  const input = useRef<HTMLInputElement>(null);
  useEffect(() => {
    document.documentElement.dataset.theme = theme;
    try {
      localStorage.setItem("todo-day-theme", theme);
    } catch {}
  }, [theme]);
  useEffect(() => {
    if (!initial.writable) return;
    try {
      localStorage.setItem(STORAGE, JSON.stringify(st));
      setStorageError("");
    } catch {
      setStorageError(C.storageError);
    }
  }, [st, initial.writable]);
  useEffect(() => {
    if (!notice) return;
    const t = setTimeout(() => setNotice(""), 4200);
    return () => clearTimeout(t);
  }, [notice]);
  const patch = (fn: (s: State) => State) =>
    setSt((s) => fn({ ...s, items: s.items.slice() }));
  const filtered = useMemo(
    () => matches(st.items, query, group) as Item[],
    [st.items, query, group],
  );
  const shown = useMemo(() => {
    let items = visible(filtered, filter, st.sort) as Item[];
    if (daySel)
      items = items.filter((i) => i.due && midnight(i.due) === daySel);
    return items;
  }, [filtered, filter, st.sort, daySel]);
  const todayItems = st.items.filter((i) => i.due && midnight(i.due) === TODAY);
  const todayDone = todayItems.filter((i) => i.done).length,
    pc = todayItems.length
      ? Math.round((todayDone / todayItems.length) * 100)
      : 0;
  const open = st.items.filter((i) => !i.done).length;
  const groups = Array.from(new Set(st.items.map((i) => i.g))).filter(Boolean);
  const parsed = parseInput(draft);
  const toggle = (i: Item) => {
    patch((s) => ({
      ...s,
      items: s.items.map((x) =>
        x.id === i.id
          ? { ...x, done: !x.done, at: !x.done ? Date.now() : 0 }
          : x,
      ),
    }));
    setNotice(i.done ? C.reopen : C.completed);
    i.done ? Tool.sound.tick() : Tool.sound.ding();
    Tool.haptic(14);
  };
  const newTask = (due = 0) =>
    setEditing({
      id: 0,
      t: "",
      due,
      g: group || C.personal,
      p: 3,
      done: false,
    });
  const clear = () => {
    setQuery("");
    setGroup("");
    setFilter("all");
    setDaySel(0);
  };
  const add = () => {
    if (!parsed.t) {
      setError(C.titleError);
      input.current?.focus();
      return;
    }
    patch((s) => ({
      ...s,
      items: [
        {
          id: s.nid,
          t: parsed.t,
          p: parsed.p,
          due: parsed.due || (mode === "calendar" ? calendarDay : daySel),
          g: group || parsed.g,
          done: false,
        },
        ...s.items,
      ],
      nid: s.nid + 1,
    }));
    setDraft("");
    setError("");
    setNotice(C.added);
    setFilter("all");
    setQuery("");
    Tool.sound.pop();
  };
  const save = (item: Item) => {
    patch((s) =>
      item.id
        ? { ...s, items: s.items.map((i) => (i.id === item.id ? item : i)) }
        : { ...s, items: [{ ...item, id: s.nid }, ...s.items], nid: s.nid + 1 },
    );
    setEditing(null);
    setNotice(item.id ? C.saved : C.added);
  };
  const remove = (item: Item) => {
    setDeleted([item]);
    patch((s) => ({ ...s, items: removeItems(s.items, [item.id]) }));
    setEditing(null);
    setNotice(C.deleted);
  };
  const clearDone = () => {
    const done = st.items.filter((i) => i.done);
    if (!done.length) {
      setNotice(C.nothingDone);
      return;
    }
    setDeleted(done);
    patch((s) => ({
      ...s,
      items: removeItems(
        s.items,
        done.map((i) => i.id),
      ),
    }));
    setNotice(C.deleted + " · " + done.length);
  };
  const exportCSV = () => {
    const cell = (v: unknown) => {
      const s = String(v ?? "");
      return /^[=+@-]/.test(s) ? "'" + s : s;
    };
    Kit.download(
      "todo-day.csv",
      Kit.toCSV([
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
    setNotice(C.exported);
  };
  const poster = async () => {
    setExporting(true);
    try {
      Tool.showPoster(
        await Tool.poster({
          brand: "Todo Day",
          title: C.myTasks,
          subtitle: new Date().toLocaleDateString(C.locale),
          hero: { value: String(pc), unit: "%", caption: C.dailyProgress },
          rows: st.items
            .filter((i) => !i.done)
            .slice(0, 7)
            .map((i) => [i.t, dueText(i) || C.noDue]),
          footer: C.subtitle,
        }),
        { name: "todo-day.png" },
      );
    } catch {
      setNotice(C.exportError);
    } finally {
      setExporting(false);
    }
  };
  const monthPick = (ts: number) => {
    setDaySel(ts);
    setFilter("all");
  };
  return (
    <div className="planner-shell">
      <a className="skip-link" href="#workspace">
        {C.myTasks}
      </a>
      <aside className="planner-sidebar">
        <div className="brand-lockup">
          <span className="brand-mark">
            <Icon name="check" size={22} />
          </span>
          <div>
            <strong>
              Todo Day<span className="brand-period">.</span>
            </strong>
            <p>{C.brand}</p>
          </div>
        </div>
        <div className="workspace-label">
          <span className="workspace-avatar">P</span>
          <div>
            {C.workspace}
            <small>{C.localSaved}</small>
          </div>
        </div>
        <nav aria-label={C.workspace} className="planner-nav">
          <button
            className={page === "tasks" ? "is-active" : ""}
            onClick={() => setPage("tasks")}
          >
            <Icon name="list" />
            {C.plan}
            <span>{open}</span>
          </button>
          <button
            className={page === "stats" ? "is-active" : ""}
            onClick={() => setPage("stats")}
          >
            <Icon name="stats" />
            {C.insights}
          </button>
        </nav>
        <div className="group-navigation">
          <span className="nav-caption">{C.projects}</span>
          <button
            className={!group ? "is-current" : ""}
            onClick={() => {
              setGroup("");
              setPage("tasks");
            }}
          >
            <span className="group-marker" />
            {C.allGroups}
          </button>
          {groups.map((g) => (
            <button
              key={g}
              data-color={groupColor(g)}
              className={group === g ? "is-current" : ""}
              onClick={() => {
                setGroup(g);
                setPage("tasks");
              }}
            >
              <span className="group-marker" />
              <span>{g}</span>
              <small>
                {st.items.filter((i) => i.g === g && !i.done).length}
              </small>
            </button>
          ))}
        </div>
        <div className="sidebar-bottom">
          <button
            className="theme-button"
            onClick={() => setTheme(theme === "light" ? "dark" : "light")}
          >
            <Icon name={theme === "light" ? "moon" : "sun"} />
            {theme === "light" ? C.themeDark : C.themeLight}
          </button>
        </div>
      </aside>
      <main className="planner-workspace" id="workspace">
        <header className="workspace-header">
          <div>
            <div className="overline">
              PERSONAL / {page === "tasks" ? "PLANNER" : "INSIGHTS"}
            </div>
            <h1>{page === "tasks" ? C.myTasks : C.insights}</h1>
            <p>{page === "tasks" ? C.subtitle : C.statsSubtitle}</p>
          </div>
          <button
            className="primary-button new-task"
            onClick={() => newTask(mode === "calendar" ? calendarDay : daySel)}
          >
            <Icon name="plus" />
            {C.newTask}
          </button>
        </header>
        {storageError && (
          <p role="alert" className="storage-warning">
            {storageError}
          </p>
        )}
        {page === "tasks" ? (
          <>
            <div className="workspace-toolbar">
              <div className="view-switch" aria-label={C.plan}>
                {(
                  [
                    ["list", C.todo, "list"],
                    ["calendar", C.calendar, "calendar"],
                  ] as const
                ).map(([v, label, icon]) => (
                  <button
                    key={v}
                    aria-pressed={mode === v}
                    className={mode === v ? "is-active" : ""}
                    onClick={() => {
                      setMode(v);
                      setDaySel(0);
                      setFilter("all");
                    }}
                  >
                    <Icon name={icon} />
                    {label}
                  </button>
                ))}
              </div>
              <label className="task-search">
                <Icon name="search" size={16} />
                <input
                  type="search"
                  value={query}
                  onChange={(e) => setQuery(e.target.value)}
                  aria-label={C.search}
                  placeholder={C.searchPlaceholder}
                />
              </label>
            </div>
            <div
              className={
                "work-layout " + (mode === "calendar" ? "is-calendar" : "")
              }
            >
              <div className="task-work">
                <form
                  className="quick-composer"
                  onSubmit={(e) => {
                    e.preventDefault();
                    add();
                  }}
                >
                  <div className="quick-input">
                    <Icon name="plus" />
                    <input
                      ref={input}
                      aria-label={C.quick}
                      aria-invalid={!!error}
                      aria-describedby={error ? "quick-error" : "quick-help"}
                      maxLength={240}
                      value={draft}
                      placeholder={C.quickPlaceholder}
                      onChange={(e) => {
                        setDraft(e.target.value);
                        setError("");
                      }}
                    />
                    <button type="submit">
                      {C.add}
                      <span>↵</span>
                    </button>
                  </div>
                  <p id="quick-help">
                    {draft.trim()
                      ? (parsed.due
                          ? new Date(parsed.due).toLocaleDateString(C.locale)
                          : C.noDue) +
                        " · " +
                        parsed.g +
                        " · " +
                        (parsed.p === 1
                          ? C.high
                          : parsed.p === 2
                            ? C.medium
                            : C.low)
                      : C.quickHelp}
                  </p>
                  {error && (
                    <p role="alert" id="quick-error" className="form-error">
                      {error}
                    </p>
                  )}
                </form>
                {mode === "list" ? (
                  <>
                    <div className="task-controls">
                      <div className="status-filters" aria-label={C.myTasks}>
                        {[
                          ["all", C.all],
                          ["today", C.today],
                          ["open", C.open],
                          ["done", C.done],
                        ].map(([v, label]) => (
                          <button
                            key={v}
                            className={filter === v ? "is-active" : ""}
                            aria-pressed={filter === v}
                            onClick={() => {
                              setFilter(v);
                              setDaySel(0);
                            }}
                          >
                            {label}
                          </button>
                        ))}
                      </div>
                      <label className="sort-control">
                        {C.sort}
                        <select
                          aria-label={C.sort}
                          value={st.sort}
                          onChange={(e) =>
                            patch((s) => ({
                              ...s,
                              sort: e.target.value as State["sort"],
                            }))
                          }
                        >
                          {Object.entries(SORTS).map(([v, label]) => (
                            <option key={v} value={v}>
                              {String(label)}
                            </option>
                          ))}
                        </select>
                      </label>
                    </div>
                    {!!daySel && (
                      <div className="date-filter">
                        {new Date(daySel).toLocaleDateString(C.locale, {
                          month: "long",
                          day: "numeric",
                        })}
                        <button
                          className="text-button"
                          onClick={() => setDaySel(0)}
                        >
                          {C.clearDate}
                          <Icon name="close" size={12} />
                        </button>
                      </div>
                    )}
                    {shown.length ? (
                      <List
                        items={shown}
                        filter={filter}
                        sort={st.sort}
                        onToggle={toggle}
                        onEdit={setEditing}
                        onReorder={(from, to) => {
                          patch((s) => ({
                            ...s,
                            items: reorder(s.items, from, to),
                          }));
                          setNotice(C.moved);
                        }}
                      />
                    ) : (
                      <div className="task-empty">
                        <Icon name="check" size={32} />
                        <h2>{C.empty}</h2>
                        <p>{C.emptyHint}</p>
                        <button className="secondary-button" onClick={clear}>
                          {C.clearFilters}
                        </button>
                      </div>
                    )}
                    <div className="list-footer">
                      <span>
                        {shown.length} {C.countSuffix}
                      </span>
                      <button className="text-button" onClick={exportCSV}>
                        <Icon name="download" size={14} />
                        {C.exportCsv}
                      </button>
                    </div>
                  </>
                ) : (
                  <CalendarView
                    items={filtered}
                    month={month}
                    selected={calendarDay}
                    onMonth={setMonth}
                    onPick={setCalendarDay}
                    onAdd={newTask}
                    onEdit={setEditing}
                    onToggle={toggle}
                  />
                )}
              </div>
              {mode === "list" && (
                <aside className="today-aside">
                  <div className="overview-heading">
                    <h2>{C.overview}</h2>
                    <span>
                      {new Date().toLocaleDateString(C.locale, {
                        month: "short",
                        day: "numeric",
                      })}
                    </span>
                  </div>
                  <div className="progress-summary">
                    <div className="ring">
                      <svg width="66" height="66" viewBox="0 0 66 66">
                        <circle className="bgc" cx="33" cy="33" r="28" />
                        <circle
                          className="fg"
                          cx="33"
                          cy="33"
                          r="28"
                          strokeDasharray="175.9"
                          strokeDashoffset={175.9 * (1 - pc / 100)}
                        />
                      </svg>
                      <span className="pc">{pc}%</span>
                    </div>
                    <div>
                      <strong>
                        {todayDone} / {todayItems.length}
                      </strong>
                      <p>{C.dailyProgress}</p>
                    </div>
                  </div>
                  <div className="overview-counts">
                    <div>
                      <b>{open}</b>
                      <span>{C.pending}</span>
                    </div>
                    <div>
                      <b>{todayItems.filter((i) => !i.done).length}</b>
                      <span>{C.todayDue}</span>
                    </div>
                    <div>
                      <b>{st.items.filter((i) => i.done).length}</b>
                      <span>{C.completed}</span>
                    </div>
                  </div>
                  <div className="mini-calendar-label">{C.dateFilter}</div>
                  <Calendar
                    items={st.items}
                    month={month}
                    daySel={daySel}
                    onMonth={setMonth}
                    onPick={monthPick}
                  />
                </aside>
              )}
            </div>
          </>
        ) : (
          <div className="insights-layout">
            <section>
              <p className="history-note">{C.historyNote}</p>
              <Heat items={st.items} />
              <Recap items={st.items} />
            </section>
            <section>
              <Stats items={st.items} />
              <div className="export-actions">
                <button className="secondary-button" onClick={clearDone}>
                  {C.clearDone}
                </button>
                <button className="secondary-button" onClick={exportCSV}>
                  {C.exportCsv}
                </button>
                <button
                  className="secondary-button"
                  disabled={exporting}
                  onClick={poster}
                >
                  {C.poster}
                  {exporting ? "…" : ""}
                </button>
                <button
                  className="secondary-button"
                  onClick={() => window.print()}
                >
                  {C.pdf}
                </button>
              </div>
            </section>
          </div>
        )}
        <footer className="workspace-footer">
          <span>
            TODO DAY <span>—</span> {C.subtitle}
          </span>
          <span>{new Date().getFullYear()}</span>
        </footer>
      </main>
      {(notice || deleted) && (
        <div className="feedback-toast" role="status">
          <Icon name="check" size={16} />
          <span>{notice || C.deleted}</span>
          {deleted && (
            <button
              onClick={() => {
                patch((s) => ({ ...s, items: restoreItems(s.items, deleted) }));
                setDeleted(null);
                setNotice(C.restored);
              }}
            >
              {C.undo}
            </button>
          )}
          {deleted && (
            <button
              className="icon-button"
              aria-label={C.close}
              onClick={() => {
                setDeleted(null);
                setNotice("");
              }}
            >
              <Icon name="close" size={14} />
            </button>
          )}
        </div>
      )}
      {editing && (
        <TaskDialog
          key={editing.id}
          item={editing}
          onSave={save}
          onDelete={remove}
          onClose={() => setEditing(null)}
        />
      )}
    </div>
  );
}
