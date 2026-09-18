#!/usr/bin/env node
/**
 * 本地渲染验证用的最小认证夹具（**不是产品代码**）
 *
 * 用途：在没有真实后端的开发机上，让用户端进入"未登录"这一合法状态，
 * 从而对登录页做真实浏览器度量（四断点 + 1920/2560 宽屏、对比度、字号、触摸目标）。
 *
 * 只实现用户端引导阶段真正会打的两个端点：
 *   GET  /api/auth/v1/session  → 401（未登录，这是**合法**的业务状态，不是错误）
 *   GET  /api/auth/v1/methods  → 200 { data: { oidc: false } }
 *   其它路径                    → 404（不伪造任何业务数据）
 *
 * 绝不能把它当成后端使用：它不返回任何业务对象。
 */
import { createServer } from "node:http";

const PORT = Number(process.env.FIXTURE_PORT ?? 8080);

const server = createServer((req, res) => {
  const url = req.url ?? "/";
  res.setHeader("Content-Type", "application/json; charset=utf-8");
  if (url.startsWith("/api/auth/v1/session")) {
    res.statusCode = 401;
    res.end(JSON.stringify({ code: "AUTHN_UNAUTHENTICATED", message: "未登录", field_errors: [], retryable: false }));
    return;
  }
  if (url.startsWith("/api/auth/v1/methods")) {
    res.statusCode = 200;
    res.end(JSON.stringify({ data: { oidc: false } }));
    return;
  }
  res.statusCode = 404;
  res.end(JSON.stringify({ code: "NOT_FOUND", message: "本地夹具没有该端点", field_errors: [], retryable: false }));
});

server.listen(PORT, "127.0.0.1", () => {
  console.log("auth fixture listening on http://127.0.0.1:" + PORT);
});
