/**
 * 案件详情 Tab → 权限（本轮新增：单一事实来源）
 *
 * 背景：这 7 个 Tab 之前**只在渲染层**用 `permission` 做过 UI 过滤，
 * 而 `/cases/:caseId/:tab` 这条路由本身只校验 `case:read`。
 * 结果是手输链接、收藏链接、浏览器历史都能绕过 UI 过滤直达子页面——
 * UI 隐藏只是"看不见入口"，不是"进不去"。
 *
 * 现在路由守卫与 Tab 渲染**共用这一张表**，两处各写一份必然漂移。
 *
 * 权限取的是**该 Tab 实际调用的接口所要求的权限**
 * （依据 openapi/user-v2.yaml 的 x-required-permission），
 * 而不是"这个 Tab 看起来属于哪个业务域"：
 *
 *   · `evidence` 目前只调 `GET /api/v2/investigations`，该接口要求
 *     `investigation:list`；而 evidence:list 实际对应的是
 *     `GET /api/v2/investigations/{id}/evidence`。
 *     只写 `evidence:list` 会让"有 evidence:list、没有 investigation:list"
 *     的用户顺利进入页面，然后吃一个 403——这正是要避免的那类问题。
 *   · `reports` 既调 `/cases/{id}/reports`（report:list）
 *     又调 `/investigations`（investigation:list），两个都缺一不可。
 *
 * 多重权限的语义是**全部满足才放行**：任一缺失都会导致进页面后请求失败，
 * 与其让用户看到一个半坏的页面，不如直接给出无权限状态。
 */

export interface CaseTabDef {
  key: string;
  label: string;
  /** 进入该 Tab 所需权限；**全部满足**才放行。 */
  permissions: string[];
}

export const CASE_TABS: CaseTabDef[] = [
  /* 概览只读案件本体。 */
  { key: "overview", label: "概览", permissions: ["case:read"] },
  { key: "subjects", label: "调查对象", permissions: ["subject:list"] },
  { key: "investigations", label: "案件调查", permissions: ["investigation:list"] },
  { key: "evidence", label: "证据中心", permissions: ["evidence:list", "investigation:list"] },
  { key: "reports", label: "案件报告", permissions: ["report:list", "investigation:list"] },
  { key: "members", label: "案件成员", permissions: ["case:member:list"] },
  { key: "activity", label: "案件动态", permissions: ["case:activity:read"] },
];

export const CASE_TAB_KEYS: string[] = CASE_TABS.map((item) => item.key);

/**
 * 取某个 tab 的定义；未知 tab 返回 null。
 *
 * 未知值**不报错也不重定向**：重构前 CaseDetailPage 会把未知 tab 回落到概览，
 * 这里保持同样的行为（路由守卫对未知 tab 只要求 case:read），
 * 以免顺手改掉一条既有的深链接行为。
 */
export function caseTabDef(tab: string): CaseTabDef | null {
  return CASE_TABS.find((item) => item.key === tab) ?? null;
}

/** 当前会话在某个 tab 上**缺少**的权限；为空表示可以进入。 */
export function missingCaseTabPermissions(tab: string, can: (permission: string) => boolean): string[] {
  const def = caseTabDef(tab);
  if (!def) return [];
  return def.permissions.filter((permission) => !can(permission));
}
