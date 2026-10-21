import { reactive, ref } from "vue";

/* AI 四象限划分结果。放模块层而不是 FocusView 组件里：组件随页面
   切换卸载（v-if），状态要跨页面保留到刷新为止。结果是一次性建议，
   不落库、不改任务；重新划分时整体替换。 */

export interface QuadInfo {
  quadrant: string;
  reason: string;
}

export const quadItems = reactive<Record<string, QuadInfo>>({});
export const quadLoading = ref(false);
export const quadError = ref("");

export function applyQuadResult(items: { id: string; quadrant: string; reason: string }[]): void {
  for (const k of Object.keys(quadItems)) delete quadItems[k];
  for (const it of items) quadItems[it.id] = { quadrant: it.quadrant, reason: it.reason };
}
