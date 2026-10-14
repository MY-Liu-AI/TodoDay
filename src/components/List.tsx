import { Fragment, useEffect, useRef } from "react";
import Tool from "../lib/tool.js";
import { ORDER, TODAY, bucketOf, dueText, midnight } from "../lib/todo.js";
import { C } from "../copy";
import Icon from "./Icon";
import type { Item, State } from "../lib/types";
import { groupColor } from "../lib/palette.js";
export default function List({
  items,
  sort,
  onToggle,
  onEdit,
  onReorder,
}: {
  items: Item[];
  filter: string;
  sort: State["sort"];
  onToggle: (i: Item) => void;
  onEdit: (i: Item) => void;
  onReorder: (from: number, to: number) => void;
}) {
  const cleanup = useRef(() => {});
  useEffect(() => () => cleanup.current(), []);
  const groups: Record<string, Item[]> = {};
  items.forEach((i) => (groups[bucketOf(i)] ||= []).push(i));
  const drag = (item: Item) => (e: React.PointerEvent<HTMLButtonElement>) => {
    if (sort !== "manual") return;
    e.preventDefault();
    const handle = e.currentTarget,
      row = handle.closest(".task-row") as HTMLElement;
    let target: HTMLElement | null = null;
    handle.setPointerCapture(e.pointerId);
    row.classList.add("is-dragging");
    Tool.haptic(8);
    const move = (ev: PointerEvent) => {
      target?.classList.remove("is-drop-target");
      const rows = Array.from(
        row.parentElement!.querySelectorAll<HTMLElement>(".task-row"),
      ).filter((r) => r !== row && r.dataset.bucket === row.dataset.bucket);
      target =
        rows.find((r) => {
          const b = r.getBoundingClientRect();
          return ev.clientY >= b.top && ev.clientY <= b.bottom;
        }) || null;
      target?.classList.add("is-drop-target");
    };
    const clean = () => {
      row.classList.remove("is-dragging");
      target?.classList.remove("is-drop-target");
      handle.removeEventListener("pointermove", move);
      handle.removeEventListener("pointerup", up);
      handle.removeEventListener("pointercancel", cancel);
    };
    const up = () => {
      const id = Number(target?.dataset.id);
      clean();
      if (id) onReorder(item.id, id);
    };
    const cancel = () => clean();
    cleanup.current = clean;
    handle.addEventListener("pointermove", move);
    handle.addEventListener("pointerup", up);
    handle.addEventListener("pointercancel", cancel);
  };
  return (
    <div className="task-list">
      {ORDER.filter((g: string) => groups[g]).map((g: string) => (
        <Fragment key={g}>
          <div
            className={
              "task-group-heading" + (g === C.overdue ? " is-late" : "")
            }
          >
            <h2>{g}</h2>
            <span>{groups[g].length}</span>
            <i />
          </div>
          {groups[g].map((i, index) => (
            <div
              className={"task-row" + (i.done ? " is-done" : "")}
              key={i.id}
              data-id={i.id}
              data-bucket={g}
            >
              <button
                className={"task-check" + (i.done ? " is-checked" : "")}
                aria-label={(i.done ? C.reopen : C.complete) + " " + i.t}
                aria-pressed={i.done}
                onClick={() => onToggle(i)}
              >
                <Icon name="check" size={14} />
              </button>
              <button className="task-title-button" onClick={() => onEdit(i)}>
                <span className="task-title">{i.t}</span>
                <span className="task-meta">
                  <span className="task-group-label" data-color={groupColor(i.g)}>{i.g}</span>
                  {i.due ? (
                    <span
                      className={
                        midnight(i.due) < TODAY && !i.done ? "is-late" : ""
                      }
                    >
                      {dueText(i)}
                    </span>
                  ) : null}
                  {i.p < 3 && <span>{i.p === 1 ? C.high : C.medium}</span>}
                </span>
              </button>
              <div className="reorder-controls">
                {sort === "manual" && (
                  <>
                    <button
                      className="reorder-key"
                      aria-label={C.moveUp + " " + i.t}
                      disabled={index === 0}
                      onClick={() => onReorder(i.id, groups[g][index - 1].id)}
                    >
                      ↑
                    </button>
                    <button
                      className="reorder-key"
                      aria-label={C.moveDown + " " + i.t}
                      disabled={index === groups[g].length - 1}
                      onClick={() => onReorder(i.id, groups[g][index + 1].id)}
                    >
                      ↓
                    </button>
                    <button
                      className="drag-handle"
                      tabIndex={-1}
                      aria-label={C.drag}
                      title={C.drag}
                      onPointerDown={drag(i)}
                    >
                      <svg
                        width="12"
                        height="18"
                        viewBox="0 0 12 18"
                        fill="currentColor"
                      >
                        <circle cx="3" cy="4" r="1" />
                        <circle cx="9" cy="4" r="1" />
                        <circle cx="3" cy="9" r="1" />
                        <circle cx="9" cy="9" r="1" />
                        <circle cx="3" cy="14" r="1" />
                        <circle cx="9" cy="14" r="1" />
                      </svg>
                    </button>
                  </>
                )}
              </div>
            </div>
          ))}
        </Fragment>
      ))}
    </div>
  );
}
