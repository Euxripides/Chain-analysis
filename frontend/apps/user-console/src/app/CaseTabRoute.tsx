import { lazy } from "react";
import { useParams } from "react-router-dom";
import { useAuth } from "@chain/api-client";
import { NoPermissionState } from "@chain/ui";
import { caseTabDef, missingCaseTabPermissions } from "./case-tabs";

/**
 * 案件详情的 **Tab 级路由守卫**（本轮新增）
 *
 * 解决的问题：`/cases/:caseId/:tab` 之前只校验 `case:read`，
 * 7 个 Tab 的权限只在渲染层做 UI 过滤。手输 URL / 收藏夹 / 浏览器历史
 * 都能绕过 UI 直达无权限的子页面。
 *
 * 三个刻意的设计：
 *
 * 1. **在 CaseDetailPage 挂载之前就返回**。这是这一层存在的全部意义：
 *    如果先渲染父页面再在父页面里判断，父页面那些 `useAsync` 已经发过请求了——
 *    "无权限的页面不该发出任何敏感请求"就无从谈起。
 *    由于用的是 `lazy()`，未授权的用户连带该页面的 chunk 都不会下载。
 *
 * 2. **守卫本身不取数**。`useAuth()` 读的是已经加载好的会话上下文，
 *    这里不会、也不应该发起任何网络请求（否则就是为了鉴权先泄露一次请求）。
 *
 * 3. **未知 tab 保持原行为**：`missingCaseTabPermissions` 对它返回空数组，
 *    于是照常渲染 CaseDetailPage，由它把未知 tab 回落到概览——
 *    与重构前一致，不顺手改掉一条既有的深链接行为。
 */
const CaseDetailPage = lazy(() => import("../pages/CaseDetailPage"));

export function CaseTabRoute() {
  const { tab = "" } = useParams();
  const { can } = useAuth();

  const missing = missingCaseTabPermissions(tab, can);
  if (missing.length > 0) {
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

  /*
   * 外层 <Suspense>（App 的路由容器）负责懒加载回退，
   * 这里不再套一层：两层 Suspense 会让骨架形状与 route-meta 声明的不一致。
   */
  return <CaseDetailPage />;
}
