#!/usr/bin/env node
/**
 * 回归证明：会话 roles / permissions 为 JSON null 时页面仍然渲染（不再白屏）。
 *
 * 缺陷背景
 * --------
 * 后端在该身份**没有任何角色绑定**时把 data.roles / data.permissions 序列化成 JSON null。
 * null 落进会话状态后，AppShell 渲染 user.roles.join(" / ") 抛
 * "TypeError: Cannot read properties of null (reading 'join')"；仓库里没有任何 ErrorBoundary，
 * React 会卸载整棵树，页面只剩一个空的 <div id="root">（白屏）。
 *
 * 这个脚本用**真实浏览器**（Playwright + 系统 Edge，与仓库既有 E2E 同一个 channel）验证：
 *   1. 静态托管两份构建产物（后台 base=/admin/，用户层 base=/app-v2/）；
 *   2. 用 page.route 伪造 GET /api/auth/v1/session，返回 roles: null / permissions: null，
 *      其余 /api/** 返回空信封（不碰任何真实后端）；
 *   3. 断言 #root 里渲染出 AppShell 导航（而不是空 div），并收集 window 上的 pageerror。
 *
 * 用法
 * ----
 *   node scripts/verify-session-null-roles.mjs
 *       只验证当前构建产物（apps/admin-console/dist、apps/user-console/dist）。
 *   BEFORE_DIR=<dir> node scripts/verify-session-null-roles.mjs
 *       额外验证一份**修复前**的产物快照做对照；<dir> 下需有 admin/ 与 app-v2/ 两个子目录
 *       （直接放修复前的 dist 内容）。对照断言：修复前必须白屏（复现缺陷），修复后必须渲染。
 *
 * 退出码：0 = 全部符合预期；1 = 有不符合预期的项（白屏、渲染崩溃、或修复前未复现）。
 */
import http from "node:http";
import fs from "node:fs";
import os from "node:os";
import path from "node:path";
import { fileURLToPath } from "node:url";
import { chromium } from "@playwright/test";

const HERE = path.dirname(fileURLToPath(import.meta.url));
const FRONTEND = path.resolve(HERE, "..");

const MIME = {
  ".html": "text/html; charset=utf-8",
  ".js": "text/javascript; charset=utf-8",
  ".mjs": "text/javascript; charset=utf-8",
  ".css": "text/css; charset=utf-8",
  ".json": "application/json; charset=utf-8",
  ".svg": "image/svg+xml",
  ".png": "image/png",
  ".jpg": "image/jpeg",
  ".ico": "image/x-icon",
  ".woff": "font/woff",
  ".woff2": "font/woff2",
  ".map": "application/json; charset=utf-8",
};

/** 静态服务器：把产物挂在 <root>/<mount>/ 下，使产物里的绝对资源路径（/admin/assets/...）可解析。 */
function serveRoot(root) {
  const server = http.createServer((req, res) => {
    const url = new URL(req.url ?? "/", "http://127.0.0.1");
    let rel = decodeURIComponent(url.pathname);
    if (rel.endsWith("/")) rel += "index.html";
    const file = path.join(root, rel);
    if (!file.startsWith(root)) {
      res.writeHead(403).end("forbidden");
      return;
    }
    fs.readFile(file, (err, body) => {
      if (err) {
        res.writeHead(404, { "content-type": "text/plain; charset=utf-8" }).end("not found: " + rel);
        return;
      }
      res.writeHead(200, { "content-type": MIME[path.extname(file)] ?? "application/octet-stream" });
      res.end(body);
    });
  });
  return new Promise((resolve) => {
    server.listen(0, "127.0.0.1", () => resolve({ server, port: server.address().port }));
  });
}

/** 后端"该身份没有任何角色绑定"时的真实响应形态：roles / permissions 是 JSON null。 */
function sessionPayload(audience) {
  const now = Date.now();
  return {
    user: { id: "identity-norole-1", display_name: "无角色管理员", email: "norole@example.com" },
    email: "norole@example.com",
    display_name: "无角色管理员",
    organization_id: "org-1",
    workspace_id: "ws-1",
    audience,
    session_id: "sess-norole-1",
    roles: null,
    permissions: null,
    expires_at: new Date(now + 3600000).toISOString(),
    absolute_expires_at: new Date(now + 86400000).toISOString(),
    must_change_password: false,
    mfa_satisfied: false,
    mfa_enrolled: false,
    csrf_token: "csrf-norole",
  };
}

function envelope(data) {
  return JSON.stringify({
    success: true,
    data,
    meta: {
      request_id: "req-null-roles",
      trace_id: "trace-null-roles",
      server_time: new Date().toISOString(),
      next_cursor: null,
    },
  });
}

/** 拦截全部 /api/**：会话端点返回 null-roles 会话，其余返回空信封（不触达真实后端）。 */
async function mockApi(page, audience) {
  await page.route("**/api/**", async (route) => {
    const url = new URL(route.request().url());
    const json = { status: 200, contentType: "application/json; charset=utf-8" };
    if (url.pathname === "/api/auth/v1/session") {
      await route.fulfill({ ...json, body: envelope(sessionPayload(audience)) });
      return;
    }
    if (url.pathname === "/api/auth/v1/session/workspaces") {
      await route.fulfill({ ...json, body: envelope({ workspaces: [] }) });
      return;
    }
    await route.fulfill({ ...json, body: envelope({ items: [] }) });
  });
}

/** 打开一个应用并采集渲染结果。 */
async function probe(browser, origin, mount, audience) {
  const context = await browser.newContext({ viewport: { width: 1440, height: 900 } });
  const page = await context.newPage();
  const pageErrors = [];
  const consoleErrors = [];
  page.on("pageerror", (err) => pageErrors.push(String(err && err.message ? err.message : err)));
  page.on("console", (msg) => {
    if (msg.type() === "error") consoleErrors.push(msg.text());
  });
  await mockApi(page, audience);
  let result;
  try {
    await page.goto(origin + mount, { waitUntil: "domcontentloaded" });
    // 等待 AuthProvider 读完 /session 并渲染（或白屏）。固定等待比轮询更稳：
    // 修复前的失败态永远不会出现 .shell，轮询只会白等到超时。
    await page.waitForTimeout(2500);
    result = await page.evaluate(() => {
      const root = document.getElementById("root");
      const userArea = document.querySelector(".shell__user");
      return {
        rootHtmlLength: root ? root.innerHTML.length : -1,
        hasShell: Boolean(document.querySelector(".shell")),
        navLinks: document.querySelectorAll(".shell__nav .shell__link").length,
        userAreaText: userArea ? (userArea.textContent || "").trim() : "",
      };
    });
  } catch (err) {
    result = { rootHtmlLength: -1, hasShell: false, navLinks: 0, userAreaText: "", navigationError: String(err) };
  }
  await context.close();
  return { ...result, pageErrors, consoleErrors };
}

function judge(sample) {
  const joinCrash = sample.pageErrors.some((m) => /Cannot read properties of null|reading 'join'/.test(m));
  const rendered = sample.hasShell && sample.rootHtmlLength > 0;
  return { joinCrash, rendered };
}

async function runTarget(browser, label, origin, mount, audience, expectRendered) {
  const sample = await probe(browser, origin, mount, audience);
  const verdict = judge(sample);
  const ok = expectRendered ? verdict.rendered && !verdict.joinCrash : !verdict.rendered && verdict.joinCrash;

  console.log("");
  console.log("-- " + label + " " + "-".repeat(Math.max(4, 56 - label.length)));
  console.log("  入口              : " + origin + mount);
  console.log("  伪造会话          : roles=null permissions=null（无角色绑定的真实响应形态）");
  console.log("  #root 内容长度    : " + sample.rootHtmlLength);
  console.log("  AppShell(.shell)  : " + (sample.hasShell ? "已渲染" : "未渲染（空 div）"));
  console.log("  侧栏导航链接数    : " + sample.navLinks);
  console.log("  顶栏用户区文本    : " + JSON.stringify(sample.userAreaText));
  console.log("  pageerror         : " + (sample.pageErrors.length === 0 ? "（无）" : sample.pageErrors.join(" | ")));
  if (sample.navigationError) console.log("  导航错误          : " + sample.navigationError);
  if (sample.consoleErrors.length > 0) console.log("  console.error     : " + sample.consoleErrors.slice(0, 3).join(" | "));
  console.log(
    "  判定              : " +
      (verdict.rendered ? "页面渲染正常" : "白屏（#root 为空）") +
      (verdict.joinCrash ? " + 命中 roles=null 渲染崩溃" : ""),
  );
  console.log("  期望              : " + (expectRendered ? "渲染正常" : "复现白屏") + " -> " + (ok ? "符合 OK" : "不符合 FAIL"));
  return ok;
}

async function stageAfter(scratch) {
  const root = path.join(scratch, "after");
  fs.mkdirSync(path.join(root, "admin"), { recursive: true });
  fs.mkdirSync(path.join(root, "app-v2"), { recursive: true });
  fs.cpSync(path.join(FRONTEND, "apps", "admin-console", "dist"), path.join(root, "admin"), { recursive: true });
  fs.cpSync(path.join(FRONTEND, "apps", "user-console", "dist"), path.join(root, "app-v2"), { recursive: true });
  return root;
}

/**
 * 启动浏览器：默认沿用仓库既有 E2E 的 msedge channel；该 channel 在部署主机（Linux）上
 * 通常不存在，此时**回退到 Playwright 自带的 chromium**，而不是让验证脚本自己先失败
 * —— 校验脚本跑不起来和被测系统有问题必须区分开。可用 E2E_BROWSER_CHANNEL 覆盖。
 */
async function launchBrowser() {
  const channel = process.env.E2E_BROWSER_CHANNEL ?? 'msedge';
  try {
    return await chromium.launch({ channel });
  } catch (err) {
    console.log(`INFO channel ${channel} 不可用（${String(err.message).split('\n')[0]}），回退到内置 chromium`);
    return await chromium.launch();
  }
}

async function main() {
  const beforeDir = process.env.BEFORE_DIR ? path.resolve(process.env.BEFORE_DIR) : "";
  const scratch = fs.mkdtempSync(path.join(os.tmpdir(), "cs-null-roles-"));
  const targets = [];

  const afterRoot = await stageAfter(scratch);
  targets.push({ label: "修复后 · 后台管理台（admin）", root: afterRoot, mount: "/admin/", audience: "admin", expectRendered: true });
  targets.push({ label: "修复后 · 用户层（user）", root: afterRoot, mount: "/app-v2/", audience: "user", expectRendered: true });

  if (beforeDir) {
    if (!fs.existsSync(path.join(beforeDir, "admin", "index.html"))) {
      throw new Error("BEFORE_DIR 下没有 admin/index.html：" + beforeDir);
    }
    targets.push({ label: "修复前 · 后台管理台（admin）", root: beforeDir, mount: "/admin/", audience: "admin", expectRendered: false });
    targets.push({ label: "修复前 · 用户层（user）", root: beforeDir, mount: "/app-v2/", audience: "user", expectRendered: false });
  }

const browser = await launchBrowser();
  const outcomes = [];
  try {
    for (const target of targets) {
      const { server, port } = await serveRoot(target.root);
      try {
        outcomes.push({
          label: target.label,
          ok: await runTarget(browser, target.label, "http://127.0.0.1:" + port, target.mount, target.audience, target.expectRendered),
        });
      } finally {
        await new Promise((resolve) => server.close(resolve));
      }
    }
  } finally {
    await browser.close();
    fs.rmSync(scratch, { recursive: true, force: true });
  }

  console.log("");
  console.log("===========================================================");
  for (const outcome of outcomes) console.log("  " + (outcome.ok ? "PASS OK " : "FAIL !  ") + outcome.label);
  const failed = outcomes.filter((o) => !o.ok).length;
  console.log("  合计：" + (outcomes.length - failed) + " 通过 / " + failed + " 未通过" + (beforeDir ? "" : "（未提供 BEFORE_DIR，本次未做修复前对照）"));
  console.log("===========================================================");
  if (failed > 0) process.exitCode = 1;
}

main().catch((err) => {
  console.error("验证脚本自身失败：" + (err && err.stack ? err.stack : err));
  process.exitCode = 1;
});
