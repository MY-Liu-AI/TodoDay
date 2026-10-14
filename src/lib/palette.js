// Stable category colors shared by calendar, agenda and navigation.
// Colors never encode priority or completion; those also have text/icons.
const categories = {
  "工作": "cyan", work: "cyan",
  "学习": "yellow", learning: "yellow",
  "生活": "peach", life: "peach",
  "健康": "teal", health: "teal",
  "重要事项": "coral", milestones: "coral",
};
export function groupColor(group) {
  const key = String(group).trim().toLowerCase();
  return Object.hasOwn(categories, key) ? categories[key] : "slate";
}
