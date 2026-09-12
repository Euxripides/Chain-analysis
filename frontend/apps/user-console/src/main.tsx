import React from "react";
import ReactDOM from "react-dom/client";
import { BrowserRouter } from "react-router-dom";
import "@chain/design-tokens/tokens.css";
import "@chain/ui/ui.css";
import "./sprint4.css";
import { DialogProvider } from "@chain/ui";
import { App } from "./App";

const basename = document.querySelector("base")?.getAttribute("href")?.replace(/\/$/, "") || "/app-v2";

ReactDOM.createRoot(document.getElementById("root") as HTMLElement).render(
  <React.StrictMode>
    <BrowserRouter basename={basename}>
      <DialogProvider>
        <App />
      </DialogProvider>
    </BrowserRouter>
  </React.StrictMode>,
);
