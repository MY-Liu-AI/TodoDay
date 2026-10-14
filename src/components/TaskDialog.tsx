import { useEffect, useRef, useState } from "react";
import type { Item } from "../lib/types";
import { dateValue } from "../lib/model.js";
import { C } from "../copy";
import Icon from "./Icon";

export default function TaskDialog({
  item,
  onSave,
  onDelete,
  onClose,
}: {
  item: Item;
  onSave: (i: Item) => void;
  onDelete: (i: Item) => void;
  onClose: () => void;
}) {
  const ref = useRef<HTMLDialogElement>(null);
  const [title, setTitle] = useState(item.t),
    [date, setDate] = useState(dateValue(item.due)),
    [group, setGroup] = useState(item.g),
    [priority, setPriority] = useState(item.p),
    [error, setError] = useState("");
  useEffect(() => {
    const previous = document.activeElement as HTMLElement;
    const dialog = ref.current;
    dialog?.showModal();
    dialog?.querySelector("input")?.focus();
    return () => {
      dialog?.close();
      previous?.focus();
    };
  }, []);
  return (
    <dialog
      ref={ref}
      className="task-dialog"
      aria-labelledby="dialog-title"
      onCancel={onClose}
      onClick={(e) => {
        if (e.target === e.currentTarget) onClose();
      }}
    >
      <form
        noValidate
        onSubmit={(e) => {
          e.preventDefault();
          if (!title.trim()) {
            setError(C.titleError);
            return;
          }
          const due = date ? +new Date(date + "T00:00:00") : 0;
          if (!Number.isFinite(due)) {
            setError(C.dateError);
            return;
          }
          onSave({
            ...item,
            t: title.trim(),
            due,
            g: group.trim() || C.personal,
            p: priority,
          });
        }}
      >
        <div className="dialog-head">
          <div>
            <span className="overline">TODO DAY</span>
            <h2 id="dialog-title">{item.id ? C.editTask : C.newTask}</h2>
          </div>
          <button
            type="button"
            className="icon-button"
            aria-label={C.close}
            onClick={onClose}
          >
            <Icon name="close" />
          </button>
        </div>
        <label className="form-field">
          {C.taskName}
          <input
            maxLength={240}
            value={title}
            aria-invalid={!!error}
            aria-describedby={error ? "task-error" : undefined}
            onChange={(e) => {
              setTitle(e.target.value);
              setError("");
            }}
            placeholder={C.titlePlaceholder}
          />
        </label>
        <div className="form-pair">
          <label className="form-field">
            {C.date}
            <input
              type="date"
              value={date}
              onInput={(e) => setDate(e.currentTarget.value)}
              onChange={(e) => setDate(e.target.value)}
            />
          </label>
          <label className="form-field">
            {C.group}
            <input
              maxLength={40}
              value={group}
              onChange={(e) => setGroup(e.target.value)}
            />
          </label>
        </div>
        <label className="form-field">
          {C.priority}
          <select
            aria-label={C.priority}
            value={priority}
            onChange={(e) => setPriority(+e.target.value)}
          >
            <option value={3}>{C.low}</option>
            <option value={2}>{C.medium}</option>
            <option value={1}>{C.high}</option>
          </select>
        </label>
        {error && (
          <p role="alert" id="task-error" className="form-error">
            {error}
          </p>
        )}
        <div className="dialog-actions">
          {!!item.id && (
            <button
              type="button"
              className="text-button danger"
              onClick={() => onDelete(item)}
            >
              {C.deleteTask}
            </button>
          )}
          <span />
          <button type="button" className="secondary-button" onClick={onClose}>
            {C.cancel}
          </button>
          <button className="primary-button" type="submit">
            {item.id ? C.save : C.create}
          </button>
        </div>
      </form>
    </dialog>
  );
}
