import React from "react";
import ReactDOM from "react-dom/client";
import { BrowserRouter } from "react-router-dom";
import "@chain/design-tokens/tokens.css";
import "@chain/ui/ui.css";
import "./sprint4.css";
import "./sprint5/sprint5.css";
/* 用户端作用域样式（蓝图 §16）：令牌重映射 → 组件层 → Shell → 动效 → 响应式。
   顺序敏感：令牌必须最先加载，响应式兜底必须最后。 */
import "./styles/user-tokens.css";
import "./styles/components.css";
import "./styles/user-shell.css";
import "./styles/motion.css";
import "./styles/responsive.css";
/*
 * ChainSight V4 设计系统（规范 §2.2 / §58）。
 *
 * 必须排在 components.css / responsive.css **之后**：legacy-bridge.css 用与旧规则
 * 相同的选择器重新声明取值，靠加载顺序取胜——这是"25 个页面一次性换肤、
 * 业务代码零改动"的实现方式。adjust 顺序会让整层失效。
 *
 * 内部的加载顺序同样敏感：
 *   css-vars（--cs-* 定义）→ surface/progress/states/shell（新组件）
 *   → legacy-bridge（旧类覆盖）→ adapter（.ant-* 结构性适配）。
 */
import "@chain/user-ui/theme/css-vars.css";
import "@chain/user-ui/styles/surface.css";
import "@chain/user-ui/styles/motion.css";
import "@chain/user-ui/styles/progress.css";
import "@chain/user-ui/styles/states.css";
import "@chain/user-ui/styles/shell.css";
import "@chain/user-ui/styles/graph-shell.css";
import "@chain/user-ui/styles/legacy-bridge.css";
import "@chain/user-ui/styles/adapter.css";
import "./styles/data-tools-clean.css";
import { DialogProvider } from "@chain/ui";
import { ChainSightProvider, UserDialogRenderer } from "@chain/user-ui";
import { App } from "./App";

const basename = document.querySelector("base")?.getAttribute("href")?.replace(/\/$/, "") || "/app-v2";

/*
 * 用户端配色重映射的作用域锚点。
 *
 * tokens.css 被用户端与后台端共用，视觉刷新不能直接改 :root，否则后台端会被动改变。
 * 挂上 data-app="user" 后，user-tokens.css / @chain/user-ui 里
 * `html[data-app="user"]` 的取值生效，后台读取到的仍是 tokens.css 的原值。
 *
 * 保留到 legacy CSS 完全退出为止（规范 §3）。
 */
document.documentElement.dataset.app = "user";

ReactDOM.createRoot(document.getElementById("root") as HTMLElement).render(
  <React.StrictMode>
    <BrowserRouter basename={basename}>
      {/*
        Provider 顺序（规范 §3）：
          BrowserRouter(basename) → ChainSightProvider(ConfigProvider/AntApp/MotionConfig)
          → DialogProvider → App(AuthProvider audience="user")

        - ChainSightProvider 在 DialogProvider **之外**：弹窗内容也要拿到 Ant 主题与
          reducedMotion="user"，但**不产生第二份 DialogContext**（§0.4.6 / §21.0）；
        - AuthProvider 仍留在 App 内部——规范明确不要求为了结构好看颠倒它的位置。
      */}
      <ChainSightProvider>
        {/*
          Dialog Core/Renderer 解耦（规范 §21.0）：
          队列、confirmWord、fields、acknowledge、结果编码全部仍在 @chain/ui/dialog-core，
          **只有视觉**换成 Ant Modal + ChainSight。provider 仍然只有一个，
          因此不可能出现两份队列或两套关闭规则。
        */}
        <DialogProvider renderer={UserDialogRenderer}>
          <App />
        </DialogProvider>
      </ChainSightProvider>
    </BrowserRouter>
  </React.StrictMode>,
);
