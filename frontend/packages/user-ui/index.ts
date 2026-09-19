/**
 * @chain/user-ui —— ChainSight 用户端设计系统（用户端重构规范 V4 §2）
 *
 * 职责边界：
 *   - `@chain/ui` 继续服务用户端与后台端，保持兼容（§2.1）；
 *   - 本包**只被 apps/user-console 引用**，admin-console 不得导入，
 *     这样"用户端换肤"不会让后台端被动改变（§1.3）；
 *   - 业务语义能力（HashValue / 数据格式化 / Dialog）仍从 `@chain/ui` 取，
 *     本包只做包装与视觉，不另造第二套业务实现（§2.1 第 5 条）。
 */

/* ---- 主题与令牌 ---- */
export * from "./theme/tokens";
export * from "./theme/motion";
export * from "./theme/z-index";
export { chainSightTheme, default as chainSightThemeDefault } from "./theme/chainSightTheme";

/* ---- Provider ---- */
export { ChainSightProvider } from "./providers";

/* ---- 动效 ---- */
export {
  CsMotionConfig,
  CsPageTransition,
  CsStagger,
  CsStepTransition,
  CsPressable,
  CsContentFade,
  csLayoutSpring,
  csPageInitial,
  csPageAnimate,
  csPageTransition,
} from "./components/motion";

/* ---- 卡片家族（§10.1 的六种） ---- */
export {
  SurfaceCard,
  InteractiveCard,
  MetricCard,
  ObjectCard,
  PanelCard,
  GlassHeroCard,
  CardFloat,
} from "./components/cards";
export type { MetricCardProps, SurfaceCardProps, CsMetricTone } from "./components/cards";

/* ---- 页面骨架 ---- */
export { CsPageHeader, CsPageHead } from "./components/page-header";
export type { CsPageHeaderProps, CsBreadcrumbItem } from "./components/page-header";

/* ---- 状态与加载 ---- */
export {
  CsEmpty,
  CsError,
  CsNoPermission,
  CsNotFound,
  CsSkeleton,
  CsRefreshBar,
} from "./components/states";
export type { CsSkeletonShape } from "./components/states";

/* ---- Overlay / Inspector / 布局 ---- */
export { CsInspector, CsStickyActions, CsSplitView, CsPage, CsFormFields } from "./components/overlay";
export type { CsInspectorProps } from "./components/overlay";

/*
 * CsProTable（L1 业务表格）**刻意不从本入口导出**。
 *
 * 原因：@chain/user-ui 的入口被 main.tsx 静态引用，因此入口可达的一切都会进初始包；
 * 而 @ant-design/pro-components 是一个很大的包。实测把 CsProTable 放进入口后，
 * antd-vendor chunk 从 90 KB gzip 涨到 536 KB gzip，初始包直接翻了近三倍，
 * 违反规范 §56.1（ProTable/ProForm 不得强制进入首页 common chunk）。
 *
 * 正确用法是从子路径导入——那样它只会落在引用它的**懒加载路由 chunk** 里：
 *   import { CsProTable } from "@chain/user-ui/pro-table";
 */

/* ---- 数据展示 ---- */
export { CsStatusTag, CsTabs, CsFilterToolbar, CsRowCheckbox, CsCheckIcon } from "./components/data";
export type { CsTabItem, CsStatusTone } from "./components/data";

/* ---- Dialog 视觉层（Core 在 @chain/ui/dialog-core，业务语义只有一份） ---- */
export { UserDialogRenderer } from "./components/user-dialog-renderer";

/* ---- 交互控件 ---- */
export { CsButton, CsIconButton } from "./components/buttons";
export type { CsButtonProps, CsButtonVariant, CsIconButtonProps } from "./components/buttons";

/* ---- 进度与实时指标 ---- */
export {
  CsJobProgress,
  CsStateDot,
  formatEta,
  formatEtaClock,
} from "./components/progress";
export type { CsJobProgressFact } from "./components/progress";

/* ---- 工具 ---- */
export { cx, prefersReducedMotion, pageIsVisible } from "./utils";
