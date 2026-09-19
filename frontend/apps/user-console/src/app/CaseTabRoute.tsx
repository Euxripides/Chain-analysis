import { lazy, useRef } from "react";
import { Navigate, useParams } from "react-router-dom";
import { useAuth } from "@chain/api-client";
import { NoPermissionState } from "@chain/ui";
import { CASE_TABS, caseTabDef, missingCaseTabPermissions } from "./case-tabs";

/**
 * 案件详情的 **Tab 级路由守卫**（Tab 级权限 + 运行中权限撤销）
 *
 * 解决的问题一：`/cases/:caseId/:tab` 之前只校验 `case:read`，
 * 7 个 Tab 的权限只在渲染层做 UI 过滤。手输 URL / 收藏夹 / 浏览器历史
 * 都能绕过 UI 直达无权限的子页面。
 *
 * 解决的问题二：**权限在运行中变化**。用户已经打开了「案件成员」，
 * 管理员随后撤销了 `case:member:list`。如果没有处理，屏幕上会继续留着
 * 已经取回来的成员数据——那是"数据已经给了不该看的人"，比"进不去"更严重。
 *
 * 三个刻意的设计：
 *
 * 1. **在 CaseDetailPage 挂载之前就返回**。这是这一层存在的全部意义：
 *    如果先渲染父页面再在父页面里判断，父页面那些 `useAsync` 已经发过请求了。
 *    由于用的是 `lazy()`，未授权的用户连带该页面的 chunk 都不会下载。
 *
 * 2. **守卫本身不取数**。`useAuth()` 读的是已经加载好的会话上下文。
 *    这里不会、也不应该发起任何网络请求（否则就是为了鉴权先泄露一次请求）。
 *
 * 3. **未知 tab 保持原行为**：`missingCaseTabPermissions` 对它返回空数组，
 *    于是照常渲染 CaseDetailPage，由它把未知 tab 回落到概览。
 *
 * 权限被**撤销**时的四步（与"直接访问无权限 URL"区分处理）：
 *   ① 立即卸载受限 Tab —— 渲染分支改变，CaseDetailPage 直接 unmount；
 *   ② 清除本地敏感状态 —— 数据只存在组件 `useState` 里（`useAsync` 没有跨卸载缓存，
 *      api-client 也没有响应缓存，两处都已逐一确认），卸载即清除；
 *   ③ 跳到第一个仍有权限的 Tab；
 *   ④ **不播任何页面动画** —— 安全跳转不该有 Motion 延迟（csInstant 标记）。
 *
 * 为什么必须区分"撤销"与"直达"：直达无权限 URL 是用户主动行为，
 * 应当明确告知"你没有这个权限"；而正在看的页面突然消失是权限变更的结果，
 * 把用户丢到一个无权限页上会让他以为是自己点错了。
 */
const CaseDetailPage = lazy(() => import("../pages/CaseDetailPage"));

export function CaseTabRoute() {
  const { caseId = "", tab = "" } = useParams();
  const { can } = useAuth();

  /*
   * 记录"本守卫已经成功渲染过哪个 tab"。
   *
   * 用 ref 而不是 state：它只在渲染期间被读来做分支判断，
   * 变化本身不需要触发重渲染（真正触发重渲染的是会话变化）。
   * 写入是幂等的，重复渲染不产生副作用。
   */
  const renderedTabRef = useRef<string | null>(null);

  const missing = missingCaseTabPermissions(tab, can);

  if (missing.length === 0) {
    renderedTabRef.current = tab;
    /*
     * 外层 <Suspense>（App 的路由容器）负责懒加载回退，
     * 这里不再套一层：两层 Suspense 会让骨架形状与 route-meta 声明的不一致。
     */
    return <CaseDetailPage />;
  }

  /* ---- 权限被撤销（而不是直接访问无权限 URL）：这个 tab 曾经渲染成功过 ---- */
  if (renderedTabRef.current === tab) {
    const fallback = CASE_TABS.find((item) => item.permissions.every((permission) => can(permission)));
    if (fallback && fallback.key !== tab) {
      return (
        <Navigate
          to={`/cases/${caseId}/${fallback.key}`}
          replace
          /* csInstant：让路由容器跳过页面入场动画（安全跳转不做 Motion 延迟）。 */
          state={{ csInstant: true }}
        />
      );
    }
    /* 连一个可去的 Tab 都没有（例如 case:read 也被撤销）——落到下面的无权限页。 */
  }

  const def = caseTabDef(tab);
  return (
    <div className="card">
      <NoPermissionState
        /* 缺少的权限全部列出：只说第一个会让用户申请错权限。 */
        permission={missing.join("、")}
        title={def ? `没有访问「${def.label}」的权限` : undefined}
      />
    </div>
  );
}
