import type { CsSkeletonShape } from "@chain/user-ui";

/**
 * 路由元信息（用户端重构规范 V4 §9.3 / §23.4 / §61）
 *
 * 规范对 `App.tsx` 的要求是"route meta；按页面类型选择 transition policy；page skeleton shape"。
 * 这里把这两件事收成一张表，原因是它们**必须一起决定**：
 * 一个页面适合什么样的进场动画，取决于它是列表还是工作台；
 * 而它的骨架形状同样取决于这个。分成两处写迟早会出现
 * "图谱页用了表格骨架 + 整页淡入"这种自相矛盾的组合。
 *
 * transition policy（§9.3 明确列举了四类，禁止全站无脑 AnimatePresence mode="wait"）：
 *   standard  —— 列表 / 普通详情 / 表单：opacity 0→1 + y 8→0，180–220ms
 *   heavy     —— 图谱 / 调查工作台 / 大表格：**即时挂载**，只对工具条与 Inspector 做局部动画。
 *                对整个 Canvas 或 10k+ 行容器做 transform/blur 会直接吃掉交互帧
 *   streaming —— Job Detail 等实时页：即时挂载。**不能用退出动画延迟 SSE 清理**，
 *                否则新页面会在旧连接还在推数据时开始渲染
 *   security  —— 登录 / 会话失效 / 权限失效：立即卸载敏感内容。
 *                为了"动画完整"而让旧页面多留 200ms，是这类路径上最不该有的取舍
 */

export type RouteTransitionPolicy = "standard" | "heavy" | "streaming" | "security";

export interface RouteMeta {
  policy: RouteTransitionPolicy;
  skeleton: CsSkeletonShape;
}

/**
 * 前缀表：按最长前缀匹配。
 *
 * 顺序无关（匹配时取最长），但按长度降序书写便于人工核对。
 */
const ROUTE_META: Array<{ prefix: string; meta: RouteMeta }> = [
  /* 安全路径：绝不为了观感延迟卸载。 */
  { prefix: "/login", meta: { policy: "security", skeleton: "form" } },

  /* 重交互工作台：即时挂载。 */
  { prefix: "/canvases", meta: { policy: "heavy", skeleton: "graph" } },
  { prefix: "/graph", meta: { policy: "heavy", skeleton: "graph" } },
  { prefix: "/investigations", meta: { policy: "heavy", skeleton: "workspace" } },
  { prefix: "/contract-analyses", meta: { policy: "heavy", skeleton: "workspace" } },

  /* 实时流式页：即时挂载，先卸载再谈观感。 */
  { prefix: "/jobs", meta: { policy: "streaming", skeleton: "detail" } },

  /* 表单页。 */
  { prefix: "/cases/new", meta: { policy: "standard", skeleton: "form" } },
  { prefix: "/downloads/new", meta: { policy: "standard", skeleton: "form" } },

  /* 列表页。 */
  { prefix: "/cases", meta: { policy: "standard", skeleton: "table" } },
  { prefix: "/reports", meta: { policy: "standard", skeleton: "table" } },
  { prefix: "/exports", meta: { policy: "standard", skeleton: "table" } },
  { prefix: "/inbox", meta: { policy: "standard", skeleton: "table" } },
  { prefix: "/notifications", meta: { policy: "standard", skeleton: "table" } },
  { prefix: "/profile", meta: { policy: "standard", skeleton: "form" } },
  { prefix: "/explorer", meta: { policy: "standard", skeleton: "form" } },
  { prefix: "/search", meta: { policy: "standard", skeleton: "detail" } },

  /* 首页与对象详情。 */
  { prefix: "/home", meta: { policy: "standard", skeleton: "dashboard" } },
  { prefix: "/addresses", meta: { policy: "standard", skeleton: "detail" } },
  { prefix: "/transactions", meta: { policy: "standard", skeleton: "detail" } },
];

const FALLBACK: RouteMeta = { policy: "standard", skeleton: "detail" };

/** 取某个路径的元信息：最长前缀匹配，命中不到就退化成标准策略。 */
export function routeMetaFor(pathname: string): RouteMeta {
  let best: { prefix: string; meta: RouteMeta } | null = null;
  for (const entry of ROUTE_META) {
    if (pathname === entry.prefix || pathname.startsWith(entry.prefix + "/")) {
      if (!best || entry.prefix.length > best.prefix.length) best = entry;
    }
  }
  return best ? best.meta : FALLBACK;
}
