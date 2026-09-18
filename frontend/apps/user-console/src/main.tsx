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
import { DialogProvider } from "@chain/ui";
import { App } from "./App";

const basename = document.querySelector("base")?.getAttribute("href")?.replace(/\/$/, "") || "/app-v2";

/*
 * 用户端配色重映射的作用域锚点。
 *
 * tokens.css 被用户端与后台端共用，视觉刷新不能直接改 :root，否则后台端会被动改变。
 * 挂上 data-app="user" 后，user-tokens.css 里 `html[data-app="user"]` 的取值生效，
 * 后台读取到的仍是 tokens.css 的原值。
 */
document.documentElement.dataset.app = "user";

ReactDOM.createRoot(document.getElementById("root") as HTMLElement).render(
  <React.StrictMode>
    <BrowserRouter basename={basename}>
      <DialogProvider>
        <App />
      </DialogProvider>
    </BrowserRouter>
  </React.StrictMode>,
);
