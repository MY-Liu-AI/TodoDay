// Pure state helpers. They never touch browser storage or the DOM.
//
// 全栈版本：state 来自服务器（字符串 id、version 用于乐观并发控制），
// validState 现在用于校验旧版 localStorage 的导入数据。
export function validState(s) {
  return (
    !!s &&
    Array.isArray(s.items) &&
    ["manual", "due", "pri", "group"].includes(s.sort) &&
    Number.isSafeInteger(s.nid) &&
    s.nid > 0 &&
    s.items.every(
      (i) =>
        Number.isSafeInteger(i.id) &&
        i.id > 0 &&
        i.id < s.nid &&
        typeof i.t === "string" &&
        !!i.t.trim() &&
        typeof i.g === "string" &&
        [1, 2, 3].includes(i.p) &&
        typeof i.done === "boolean" &&
        Number.isFinite(i.due) &&
        i.due >= 0 &&
        i.due <= 8640000000000000 &&
        (i.at === undefined || (Number.isFinite(i.at) && i.at >= 0)),
    ) &&
    new Set(s.items.map((i) => i.id)).size === s.items.length
  );
}
export function reorder(items, fromId, toId) {
  if (
    fromId === toId ||
    !items.some((i) => i.id === fromId) ||
    !items.some((i) => i.id === toId)
  )
    return items;
  const next = items.slice();
  const target = next.findIndex((i) => i.id === toId);
  const [moved] = next.splice(
    next.findIndex((i) => i.id === fromId),
    1,
  );
  next.splice(target, 0, moved);
  return next;
}
export function matches(items, query, group) {
  const q = query.trim().toLocaleLowerCase();
  return items.filter(
    (i) =>
      (!group || i.g === group) &&
      (!q || (i.t + " " + i.g).toLocaleLowerCase().includes(q)),
  );
}
export function dateValue(ts) {
  if (!ts) return "";
  const d = new Date(ts);
  return [
    d.getFullYear(),
    String(d.getMonth() + 1).padStart(2, "0"),
    String(d.getDate()).padStart(2, "0"),
  ].join("-");
}
export function removeItems(items, ids) {
  return items.filter((i) => !ids.includes(i.id));
}
export function restoreItems(items, deleted) {
  return [
    ...deleted.filter((d) => !items.some((i) => i.id === d.id)),
    ...items,
  ];
}
export function monthDays(month) {
  const first = new Date(month.getFullYear(), month.getMonth(), 1);
  first.setDate(1 - ((first.getDay() + 6) % 7));
  return Array.from(
    { length: 42 },
    (_, k) =>
      +new Date(first.getFullYear(), first.getMonth(), first.getDate() + k),
  );
}
