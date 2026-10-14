import { midnight, TODAY } from "../lib/todo.js";
import { monthDays } from "../lib/model.js";
import type { Item } from "../lib/types";
import { C } from "../copy";
import Icon from "./Icon";
import { groupColor } from "../lib/palette.js";
export default function CalendarView({
  items,
  month,
  selected,
  onMonth,
  onPick,
  onEdit,
  onAdd,
  onToggle,
}: {
  items: Item[];
  month: Date;
  selected: number;
  onMonth: (d: Date) => void;
  onPick: (d: number) => void;
  onEdit: (i: Item) => void;
  onAdd: (d: number) => void;
  onToggle: (i: Item) => void;
}) {
  const chosen = items.filter((i) => i.due && midnight(i.due) === selected);
  const days = monthDays(month);
  return (
    <section className="calendar-view" aria-label={C.calendar}>
      <div className="calendar-toolbar">
        <h2>
          {month.toLocaleDateString(C.locale, {
            year: "numeric",
            month: "long",
          })}
        </h2>
        <div className="month-actions">
          <button
            className="icon-button prev-arrow"
            aria-label={C.prevMonth}
            onClick={() =>
              onMonth(new Date(month.getFullYear(), month.getMonth() - 1, 1))
            }
          >
            <Icon name="arrow" />
          </button>
          <button
            className="secondary-button"
            onClick={() => {
              onMonth(
                new Date(
                  new Date(TODAY).getFullYear(),
                  new Date(TODAY).getMonth(),
                  1,
                ),
              );
              onPick(TODAY);
            }}
          >
            {C.today}
          </button>
          <button
            className="icon-button"
            aria-label={C.nextMonth}
            onClick={() =>
              onMonth(new Date(month.getFullYear(), month.getMonth() + 1, 1))
            }
          >
            <Icon name="arrow" />
          </button>
        </div>
      </div>
      <div className="month-weekdays">
        {C.weekdays.map((w) => (
          <span key={w}>{w}</span>
        ))}
      </div>
      <div className="month-grid">
        {days.map((ts) => {
          const d = new Date(ts),
            on = items.filter((i) => i.due && midnight(i.due) === ts);
          return (
            <div
              key={ts}
              className={
                "month-cell" +
                (d.getMonth() !== month.getMonth() ? " is-outside" : "") +
                (ts === selected ? " is-selected" : "")
              }
            >
              <button
                className={"day-number" + (ts === TODAY ? " is-today" : "")}
                aria-label={
                  d.toLocaleDateString(C.locale, {
                    year: "numeric",
                    month: "long",
                    day: "numeric",
                  }) +
                  " · " +
                  on.length +
                  " " +
                  C.tasks
                }
                aria-pressed={ts === selected}
                onClick={() => onPick(ts)}
              >
                <span>{d.getDate()}</span>
                <small>{on.length || ""}</small>
                <span className="mobile-event-colors" aria-hidden="true">
                  {[...new Set(on.map((i) => groupColor(i.g)))].map((color) => (
                    <i key={color} data-color={color} />
                  ))}
                </span>
              </button>
              <div className="day-events">
                {on.slice(0, 2).map((i) => (
                  <button
                    key={i.id}
                    className={"day-event" + (i.done ? " is-done" : "")}
                    data-color={groupColor(i.g)}
                    title={i.t + " · " + i.g}
                    aria-label={i.t + " · " + i.g + (i.done ? " · " + C.done : "")}
                    onClick={() => onEdit(i)}
                  >
                    <span className="event-dot" />
                    <span>{i.t}</span>
                  </button>
                ))}
                {on.length > 2 && (
                  <button className="more-events" onClick={() => onPick(ts)}>
                    +{on.length - 2} {C.more}
                  </button>
                )}
              </div>
            </div>
          );
        })}
      </div>
      <div className="day-agenda">
        <div className="agenda-heading">
          <div>
            <span className="overline">{C.agenda}</span>
            <h3>
              {new Date(selected).toLocaleDateString(C.locale, {
                month: "long",
                day: "numeric",
                weekday: "long",
              })}
            </h3>
          </div>
          <button className="secondary-button" onClick={() => onAdd(selected)}>
            <Icon name="plus" />
            {C.addThisDay}
          </button>
        </div>
        {!chosen.length ? (
          <p className="agenda-empty">{C.dayEmpty}</p>
        ) : (
          chosen.map((i) => (
            <div className="agenda-item" key={i.id} data-color={groupColor(i.g)}>
              <button
                className={"task-check" + (i.done ? " is-checked" : "")}
                aria-label={(i.done ? C.reopen : C.complete) + " " + i.t}
                aria-pressed={i.done}
                onClick={() => onToggle(i)}
              >
                <Icon name="check" size={14} />
              </button>
              <button
                className={"agenda-task" + (i.done ? " is-done" : "")}
                onClick={() => onEdit(i)}
              >
                {i.t}
                <span>{i.g}</span>
              </button>
            </div>
          ))
        )}
      </div>
      {!!items.filter((i) => !i.due).length && (
        <details className="unscheduled">
          <summary>
            {C.unscheduled} · {items.filter((i) => !i.due).length}
          </summary>
          {items
            .filter((i) => !i.due)
            .map((i) => (
              <button
                className="text-button"
                key={i.id}
                onClick={() => onEdit(i)}
              >
                {i.t}
              </button>
            ))}
        </details>
      )}
    </section>
  );
}
