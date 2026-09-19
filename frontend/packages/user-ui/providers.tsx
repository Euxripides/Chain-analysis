import React from "react";
import { App as AntApp, ConfigProvider } from "antd";
import zhCN from "antd/locale/zh_CN";
import { chainSightTheme } from "./theme/chainSightTheme";
import { ensureOverlayRoots, getPopupContainer } from "./theme/z-index";
import { CsMotionConfig } from "./components/motion";

/**
 * 用户端 Provider 层（用户端重构规范 V4 §3）
 *
 * 结构（与规范建议一致）：
 *   <ConfigProvider theme={chainSightTheme}>   ← 主题只在这一处生效
 *     <AntApp>                                 ← 只提供组件 context
 *       <CsMotionConfig reducedMotion="user">  ← 与 CSS prefers-reduced-motion 双保险
 *         {业务树}
 *
 * 明确不做的事：
 *   - **不在这里创建第二份 DialogContext**：业务提示通道仍然是 `@chain/ui` 的
 *     `DialogProvider/useDialog()`（§0.4.6 / §21.0），它由 main.tsx 在更外层提供；
 *   - **不把业务错误改成 `message.error()`**：仓库规范要求提示只能走弹窗，
 *     AntApp 在这里只提供上下文，页面不得用它做业务提示（§20.1）；
 *   - 不在这里插入 QueryClientProvider：第 57 节迁移完成前继续使用现有 `useApi()`。
 *
 * CSP（§3.1）：Ant Design 5 使用 CSS-in-JS 运行时注入 `<style>`。
 * 当前部署的 `style-src` 仍是 `'self' 'unsafe-inline'`（既有取值，本次不放宽、也不收紧），
 * 因此无需 nonce 即可工作；若将来收紧 CSP，把服务端 nonce 写进
 * `<meta name="csp-nonce" content="...">` 或 `window.__CS_CSP_NONCE__`，
 * 这里会自动通过 `ConfigProvider csp={{ nonce }}` 交给 antd，不需要改任何业务代码。
 */

function readCspNonce(): string | undefined {
  if (typeof document === "undefined") return undefined;
  const fromGlobal = (window as unknown as { __CS_CSP_NONCE__?: string }).__CS_CSP_NONCE__;
  if (typeof fromGlobal === "string" && fromGlobal) return fromGlobal;
  const meta = document.querySelector('meta[name="csp-nonce"]');
  const content = meta?.getAttribute("content");
  return content && content.length > 0 ? content : undefined;
}

export function ChainSightProvider({ children }: { children: React.ReactNode }) {
  React.useEffect(() => {
    /* 统一的 Portal 挂载点（§22.4）。幂等，重复挂载不会产生重复节点。 */
    ensureOverlayRoots();
  }, []);

  /* CSP nonce 只有在服务端确实下发时才传；antd 会把它写到注入的 <style> 上（§3.1）。 */
  const nonce = readCspNonce();

  return (
    <ConfigProvider
      theme={chainSightTheme}
      locale={zhCN}
      /* 所有弹层默认挂到全局 overlay root，业务页面不再各自决定 getPopupContainer（§22.4）。 */
      getPopupContainer={getPopupContainer}
      /*
       * 波纹涟漪统一关闭：按压反馈由 CsButton 的 Motion 独占（§9.6），
       * 不允许出现 "Ant Wave + Motion scale + 自定义 ripple" 三套叠加。
       */
      wave={{ disabled: true }}
      {...(nonce ? { csp: { nonce } } : {})}
    >
      <AntApp component={false}>
        <CsMotionConfig>{children}</CsMotionConfig>
      </AntApp>
    </ConfigProvider>
  );
}

export { AntApp };
