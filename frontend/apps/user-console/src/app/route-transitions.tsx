import React from "react";
import { CsPageTransition } from "@chain/user-ui";
import { routeMetaFor, type RouteTransitionPolicy } from "./route-meta";

/**
 * 路由内容容器（用户端重构规范 V4 §9.3 / §61）
 *
 * **这不是一个"给所有页面套动画"的组件。** 它是一处分流点：
 * 只有 standard 策略会挂上页面级进场动画，heavy / streaming / security 一律即时挂载。
 *
 * 规范明确禁止全站统一的 `AnimatePresence mode="wait"`——那会让每次跳转都
 * "等旧页面退场完才挂新页面"，在流水线式调查里（列表→详情→返回→再进详情）
 * 累积成明显的拖沓感。因此 standard 分支用的是 `CsPageTransition`：
 * 新页面立刻挂载，只做一次入场，不做等待式退场。
 *
 * `data-policy` 是刻意暴露的：验收脚本靠它区分"这一页到底有没有页面级动画"，
 * 而不是去嗅探 DOM 里有没有 motion 的 style 属性。
 */
export function RoutedContent({
  pathname,
  children,
}: {
  pathname: string;
  children: React.ReactNode;
}) {
  const { policy } = routeMetaFor(pathname);

  if (policy !== "standard") {
    /*
     * heavy / streaming / security：
     * 直接返回 children，连一层包裹 div 都不加——多一层容器就可能多一个
     * 参与布局与合成的节点，而这三类页面正是对合成开销最敏感的。
     */
    return (
      <div className="cs-route cs-route--instant" data-policy={policy} data-route={pathname}>
        {children}
      </div>
    );
  }

  return (
    <CsPageTransition routeKey={pathname} className="cs-route" dataPolicy={policy} dataRoute={pathname}>
      {children}
    </CsPageTransition>
  );
}

export type { RouteTransitionPolicy };
