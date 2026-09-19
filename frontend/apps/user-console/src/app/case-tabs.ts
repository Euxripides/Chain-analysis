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

/**
 * 多个权限之间的组合方式：**AND（全部满足才放行）**。
 *
 * 之所以写成显式常量而不是只体现在代码里：契约冻结检查要能把"组合方式"
 * 一起冻结。如果哪天有人把它改成"任一满足即可"（OR），
 * 光比对权限列表是看不出来的——列表一字未改，准入却松了一大截。
 */
export const CASE_TAB_PERMISSION_COMBINATION = "AND";

export interface CaseTabDef {
  key: string;
  label: string;
  /** 进入该 Tab 所需权限；按 `CASE_TAB_PERMISSION_COMBINATION` 组合。 */
  permissions: string[];
  /** 临时兼容规则：**不是最终设计**，等后端确定案件级证据模型后需要重新审视。 */
  temporary?: boolean;
}

export const CASE_TABS: CaseTabDef[] = [
  /*
   * **每个 Tab 都要求 case:read**，包括非概览 Tab。
   *
   * 这不是冗余：案件详情页本身要调 `GET /api/v2/cases/{id}` 才能渲染标题与页头，
   * 该接口要求 case:read。只写 Tab 自己的权限，
   * 会让"有 case:member:list、没有 case:read"的用户进得了 /members，
   * 然后父页面取案件时吃 403——又是"进得去但用不了"。
   * 把它显式列在每一项里而不是代码里隐式补，是为了让冻结的契约表自解释。
   */
  { key: "overview", label: "概览", permissions: ["case:read"] },
  { key: "subjects", label: "调查对象", permissions: ["case:read", "subject:list"] },
  { key: "investigations", label: "案件调查", permissions: ["case:read", "investigation:list"] },
  /*
   * ⚠️ 临时兼容规则（temporary=true）——**不是最终权限设计**。
   *
   * 证据 Tab 现在只调 `GET /api/v2/investigations`，该接口要求 investigation:list；
   * 而 evidence:list 管的是 `/investigations/{id}/evidence`。
   * 于是"有 evidence:list、没有 investigation:list"的用户会进去吃 403。
   * 当前用双权限把它挡住，保证"进得去的人一定取得到数"。
   *
   * **完整决策与被否决的方案见 `docs/adr/ADR-022-case-evidence-tab-permission.md`。**
   *
   * 目标状态是**案件级证据接口**（形如 `/cases/{caseId}/evidence`，
   * 以案件证据权限为边界），届时本条目应改回单一 evidence:list 并去掉 temporary。
   * 在那之前不要把它当成设计意图引用。
   */
  { key: "evidence", label: "证据中心", permissions: ["case:read", "evidence:list", "investigation:list"], temporary: true },
  /* 同理：reports Tab 既调 /cases/{id}/reports（report:list）又调 /investigations（investigation:list）。 */
  { key: "reports", label: "案件报告", permissions: ["case:read", "report:list", "investigation:list"] },
  { key: "members", label: "案件成员", permissions: ["case:read", "case:member:list"] },
  { key: "activity", label: "案件动态", permissions: ["case:read", "case:activity:read"] },
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

/**
 * 判断一个 URL 指向的案件子 Tab 当前是否可访问（**入口过滤**用）。
 *
 * 命令面板（搜索命中 / 最近访问）、报告详情里的深链接、面包屑等入口，
 * 必须与路由守卫用**同一判据**：否则会出现"入口看得见、点进去被拦"——
 * 用户会以为是自己点错了，而实际上是被权限挡住了。
 *
 * 前缀权限表（ROUTE_PERMISSIONS）在这里是不够的：`/cases/x/members` 的前缀是
 * `/cases` → `case:list`，它对"有没有 case:member:list"一无所知。
 *
 * 非案件子 Tab 的 URL 一律返回 true —— 不由本模块负责，交回各自原有逻辑。
 */
export function canVisitCaseTabUrl(url: string, can: (permission: string) => boolean): boolean {
  const match = /^\/cases\/([^/?#]+)\/([^/?#]+)/.exec(url);
  if (!match) return true;
  const def = caseTabDef(match[2]);
  /* 未知 tab 保持原行为（CaseDetailPage 会把它回落成概览）。 */
  if (!def) return true;
  return def.permissions.every((permission) => can(permission));
}

/** 当前会话在某个 tab 上**缺少**的权限；为空表示可以进入。 */
export function missingCaseTabPermissions(tab: string, can: (permission: string) => boolean): string[] {
  const def = caseTabDef(tab);
  if (!def) return [];
  return def.permissions.filter((permission) => !can(permission));
}
