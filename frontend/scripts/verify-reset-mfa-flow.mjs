#!/usr/bin/env node
/**
 * 管理台「重置 MFA」前端流程验证（真实浏览器 + 路由 mock，不触达任何后端）。
 *
 * 后端接口 POST /api/admin/v1/users/{id}/commands/reset-mfa 尚未在本机部署，
 * 因此这里用 page.route 按契约伪造响应，验证三段式弹窗编排真的能跑通：
 *   详情弹窗「重置 MFA」入口（未启用时 disabled + title 说明）
 *     → 影响预览（脱敏邮箱/将删除/将吊销会话/后果/本人登出警示）
 *     → 执行弹窗（原因 + 密码 + 影响确认，三项齐全前提交按钮 disabled）
 *     → 结果弹窗（被移除内容与吊销会话数；失败按错误码分支）
 *
 * 用法：node scripts/verify-reset-mfa-flow.mjs
 * 退出码：0 = 全部断言通过；1 = 有断言失败。
 */
import http from "node:http";
import fs from "node:fs";
import os from "node:os";
import path from "node:path";
import { fileURLToPath } from "node:url";
import { chromium } from "@playwright/test";

const HERE = path.dirname(fileURLToPath(import.meta.url));
const FRONTEND = path.resolve(HERE, "..");
const TARGET_ID = "u-mfa-1";
const OTHER_ID = "u-no-mfa-2";

const MIME = {
  ".html": "text/html; charset=utf-8",
  ".js": "text/javascript; charset=utf-8",
  ".css": "text/css; charset=utf-8",
  ".json": "application/json; charset=utf-8",
  ".svg": "image/svg+xml",
  ".png": "image/png",
  ".ico": "image/x-icon",
  ".woff2": "font/woff2",
};

async function serveDist(distDir) {
  const root = fs.mkdtempSync(path.join(os.tmpdir(), "cs-reset-mfa-"));
  fs.mkdirSync(path.join(root, "admin"), { recursive: true });
  fs.cpSync(distDir, path.join(root, "admin"), { recursive: true });
  const server = http.createServer((req, res) => {
    const url = new URL(req.url ?? "/", "http://127.0.0.1");
    let rel = decodeURIComponent(url.pathname);
    if (rel.endsWith("/")) rel += "index.html";
    const send = (file) =>
      fs.readFile(file, (err, body) => {
        if (err) {
          res.writeHead(404).end("not found");
          return;
        }
        res.writeHead(200, { "content-type": MIME[path.extname(file)] ?? "application/octet-stream" });
        res.end(body);
      });
    // SPA 深链回退：/admin/users 这类前端路由没有对应文件，回退到该挂载点的 index.html。
    if (path.extname(rel) === "") {
      send(path.join(root, "admin", "index.html"));
      return;
    }
    send(path.join(root, rel));
  });
  const port = await new Promise((resolve) => server.listen(0, "127.0.0.1", () => resolve(server.address().port)));
  return { server, root, origin: "http://127.0.0.1:" + port };
}

function envelope(data) {
  return JSON.stringify({
    success: true,
    data,
    meta: { request_id: "req-1", trace_id: "trace-1", server_time: new Date().toISOString(), next_cursor: null },
  });
}

function failure(status, code, message) {
  return JSON.stringify({ success: false, error: { code, message, field_errors: [], retryable: false } });
}

function identity(overrides) {
  return Object.assign(
    {
      id: TARGET_ID,
      organization_id: "org-1",
      email: "mfa.user@example.com",
      display_name: "MFA 测试用户",
      status: "active",
      is_platform_admin: false,
      mfa_required: true,
      mfa_enrolled: true,
      must_change_password: false,
      failed_login_count: 0,
      last_login_at: null,
      created_at: new Date().toISOString(),
      updated_at: new Date().toISOString(),
      has_password: true,
      roles: null,
    },
    overrides,
  );
}

const results = [];
function check(name, condition, detail) {
  results.push({ name, ok: Boolean(condition), detail: detail === undefined ? "" : String(detail) });
  console.log("  " + (condition ? "[PASS]" : "[FAIL]") + " " + name + (detail === undefined ? "" : "  -> " + detail));
}

/** 资源回收：断言失败时也必须关掉浏览器与静态服务器，否则 Node 永远不会退出。 */
const cleanups = [];
async function cleanupAll() {
  while (cleanups.length > 0) {
    const fn = cleanups.pop();
    try {
      await fn();
    } catch {
      /* 回收失败不掩盖真正的失败原因 */
    }
  }
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
  const staged = await serveDist(path.join(FRONTEND, "apps", "admin-console", "dist"));
  cleanups.push(() => new Promise((resolve) => staged.server.close(resolve)));
  cleanups.push(() => fs.rmSync(staged.root, { recursive: true, force: true }));
const browser = await launchBrowser();
  cleanups.push(() => browser.close());
  const context = await browser.newContext({ viewport: { width: 1440, height: 900 } });
  const page = await context.newPage();
  const pageErrors = [];
  page.on("pageerror", (err) => pageErrors.push(String(err && err.message ? err.message : err)));

  // 场景开关：reset 端点的下一次响应由用例决定。
  const apiLog = [];
  page.on("request", (req) => {
    if (req.url().indexOf("/api/") >= 0) apiLog.push(req.method() + " " + new URL(req.url()).pathname);
  });
  const state = { resetStatus: 200, resetBody: envelope({ reset: true, mfa_enrolled: false, self: false, removed: { secret: true, recovery_codes: 10 }, sessions_revoked: 3 }), posted: [], listCalls: 0, detailCalls: 0 };

  await page.route("**/api/**", async (route) => {
    const req = route.request();
    const url = new URL(req.url());
    const json = { status: 200, contentType: "application/json; charset=utf-8" };
    if (url.pathname === "/api/auth/v1/session") {
      await route.fulfill({
        ...json,
        body: envelope({
          user: { id: "admin-self", display_name: "值班管理员", email: "ops@example.com" },
          email: "ops@example.com",
          display_name: "值班管理员",
          organization_id: "org-1",
          workspace_id: "ws-1",
          audience: "admin",
          session_id: "sess-admin",
          roles: ["owner"],
          permissions: ["admin:user:read", "admin:user:reset_mfa"],
          expires_at: new Date(Date.now() + 3600000).toISOString(),
          absolute_expires_at: new Date(Date.now() + 86400000).toISOString(),
          must_change_password: false,
          mfa_satisfied: true,
          csrf_token: "csrf",
        }),
      });
      return;
    }
    if (url.pathname === "/api/auth/v1/session/workspaces") {
      await route.fulfill({ ...json, body: envelope({ workspaces: [] }) });
      return;
    }
    if (url.pathname === "/api/admin/v1/users") {
      state.listCalls += 1;
      await route.fulfill({ ...json, body: envelope([identity(), identity({ id: OTHER_ID, email: "no.mfa@example.com", display_name: "未启用 MFA 用户", mfa_enrolled: false })]) });
      return;
    }
    if (url.pathname === "/api/admin/v1/users/" + TARGET_ID && req.method() === "GET") {
      state.detailCalls += 1;
      await route.fulfill({
        ...json,
        body: envelope({ identity: identity(), roles: null, active_sessions: [{ id: "s1" }, { id: "s2" }, { id: "s3" }] }),
      });
      return;
    }
    if (url.pathname === "/api/admin/v1/users/" + OTHER_ID && req.method() === "GET") {
      await route.fulfill({
        ...json,
        body: envelope({ identity: identity({ id: OTHER_ID, email: "no.mfa@example.com", display_name: "未启用 MFA 用户", mfa_enrolled: false }), roles: [], active_sessions: [] }),
      });
      return;
    }
    if (url.pathname.indexOf("/commands/reset-mfa") >= 0) {
      // headers() 是同步 API；headerValue() 返回 Promise，直接放进对象会变成 "[object Promise]"。
      state.posted.push({ url: req.url(), body: req.postDataJSON(), idempotencyKey: req.headers()["idempotency-key"] });
      await route.fulfill({ status: state.resetStatus, contentType: "application/json; charset=utf-8", body: state.resetBody });
      return;
    }
    await route.fulfill({ ...json, body: envelope({ items: [] }) });
  });

  const dialogTitle = page.locator(".dialog__title");
  const dialogBody = page.locator(".dialog__body");
  const foot = page.locator(".dialog__foot");
  const openDetail = async (name) => {
    try {
      await page.locator("button:has(strong:text-is('" + name + "'))").click({ timeout: 8000 });
    } catch (err) {
      console.log("DEBUG url      : " + page.url());
      console.log("DEBUG bodyText : " + JSON.stringify((await page.locator("body").innerText()).slice(0, 1200)));
      console.log("DEBUG rowBtns  : " + (await page.locator("table button").count()));
      console.log("DEBUG shell    : " + (await page.locator(".shell").count()));
      console.log("DEBUG apiLog   :\n" + apiLog.join("\n"));
      throw err;
    }
    await page.locator(".dialog__title:text-is('" + name + "')").waitFor({ timeout: 10000 });
  };

  console.log("");
  console.log("== 管理台「重置 MFA」流程验证（mock 后端，真实浏览器） ==");
  await page.goto(staged.origin + "/admin/users", { waitUntil: "domcontentloaded" });

  // ---- 1. 详情弹窗入口 + roles=null 不崩 ----
  await openDetail("MFA 测试用户");
  const detailText = await dialogBody.innerText();
  check("详情弹窗打开且 roles=null 正常降级为 —", detailText.indexOf("角色") >= 0 && detailText.indexOf("null") < 0, JSON.stringify(detailText.slice(0, 60)));
  const resetBtn = foot.locator("button:text-is('重置 MFA')");
  check("详情弹窗出现「重置 MFA」入口", (await resetBtn.count()) === 1);
  check("已启用 MFA 时入口可用", (await resetBtn.isDisabled()) === false);

  // ---- 2. 影响预览 ----
  await resetBtn.click();
  await page.locator(".dialog__title:text-is('重置 MFA · 影响预览')").waitFor({ timeout: 10000 });
  const previewKv = await page.locator(".dialog__kv").innerText();
  const previewText = await dialogBody.innerText();
  check("预览展示脱敏邮箱", previewKv.indexOf("m*******@example.com") >= 0, "kv 中不含完整邮箱? " + (previewKv.indexOf("mfa.user@example.com") < 0));
  check("预览隐藏完整邮箱", previewKv.indexOf("mfa.user@example.com") < 0);
  check("预览列出将删除的内容", previewKv.indexOf("TOTP 密钥") >= 0 && previewKv.indexOf("恢复码") >= 0);
  check("预览列出将吊销的活跃会话数", previewKv.indexOf("3 个") >= 0);
  check("预览写明后果（登录不再需要第二步验证码）", previewText.indexOf("此后该账号登录不再需要第二步验证码") >= 0);

  // ---- 3. 执行弹窗：三项齐全才可提交 ----
  await foot.locator("button:text-is('继续')").click();
  await page.locator(".dialog__title:text-is('重置 MFA · 执行')").waitFor({ timeout: 10000 });
  const submit = foot.locator("button:text-is('确认重置')");
  check("执行弹窗有原因/密码/影响确认三个字段", (await page.locator("#dialog-field-reason").count()) === 1 && (await page.locator("#dialog-field-password").count()) === 1 && (await page.locator("#dialog-field-ack").count()) === 1);
  check("密码字段 type=password", (await page.locator("#dialog-field-password").getAttribute("type")) === "password");
  check("三项未填时提交按钮 disabled", await submit.isDisabled());
  await page.locator("#dialog-field-reason").fill("员工更换手机且未保存恢复码");
  check("只填原因时仍 disabled（缺密码与勾选）", await submit.isDisabled());
  await page.locator("#dialog-field-password").fill("correct horse battery staple");
  check("填了原因+密码但未勾选时仍 disabled", await submit.isDisabled());
  await page.locator("#dialog-field-ack").check();
  check("三项齐全后提交按钮可用", (await submit.isDisabled()) === false);

  // ---- 4. 提交：请求体契约 ----
  await submit.click();
  await page.locator(".dialog__title:text-is('MFA 已重置')").waitFor({ timeout: 10000 });
  const posted = state.posted[state.posted.length - 1];
  check("提交到 /commands/reset-mfa", posted && posted.url.indexOf("/api/admin/v1/users/" + TARGET_ID + "/commands/reset-mfa") >= 0, posted && posted.url);
  check("body 含 confirm=true", posted && posted.body.confirm === true);
  check("body 含 reason", posted && posted.body.reason === "员工更换手机且未保存恢复码", posted && posted.body.reason);
  check("body 含 step_up_password", posted && posted.body.step_up_password === "correct horse battery staple");
  check("携带 Idempotency-Key", Boolean(posted && posted.idempotencyKey), posted && posted.idempotencyKey);
  const successKv = await page.locator(".dialog__kv").innerText();
  check("成功弹窗显示被移除内容与吊销会话数", successKv.indexOf("10 枚") >= 0 && successKv.indexOf("3 个") >= 0, JSON.stringify(successKv));
  await foot.locator("button:text-is('知道了')").click();
  await page.waitForTimeout(800);
  check("成功后刷新了用户列表与详情", state.listCalls >= 2 && state.detailCalls >= 2, "list=" + state.listCalls + " detail=" + state.detailCalls);
  await foot.locator("button:text-is('关闭')").click();
  await page.waitForTimeout(300);

  // ---- 5. 未启用 MFA 的账号：入口 disabled + title 说明 ----
  await openDetail("未启用 MFA 用户");
  const disabledBtn = foot.locator("button:text-is('重置 MFA')");
  check("未启用 MFA 时入口 disabled", await disabledBtn.isDisabled());
  check("入口 title 说明原因", ((await disabledBtn.getAttribute("title")) || "").indexOf("未启用 MFA") >= 0, await disabledBtn.getAttribute("title"));
  const disabledKv = await page.locator(".dialog__kv").innerText();
  check("详情里同时给出不可用原因", disabledKv.indexOf("不可用：该账号未启用 MFA") >= 0);
  await foot.locator("button:text-is('关闭')").click();
  await page.waitForTimeout(300);

  // ---- 6. step-up 失败：回到执行弹窗重新输入密码 ----
  state.resetStatus = 403;
  state.resetBody = failure(403, "STEP_UP_INVALID", "当前账号密码不正确");
  await openDetail("MFA 测试用户");
  await foot.locator("button:text-is('重置 MFA')").click();
  await page.locator(".dialog__title:text-is('重置 MFA · 影响预览')").waitFor({ timeout: 10000 });
  await foot.locator("button:text-is('继续')").click();
  await page.locator(".dialog__title:text-is('重置 MFA · 执行')").waitFor({ timeout: 10000 });
  await page.locator("#dialog-field-reason").fill("重试场景");
  await page.locator("#dialog-field-password").fill("wrong-password");
  await page.locator("#dialog-field-ack").check();
  await foot.locator("button:text-is('确认重置')").click();
  await page.locator(".dialog__title:text-is('重置 MFA · 执行')").waitFor({ timeout: 10000 });
  const retryBody = await dialogBody.innerText();
  check("STEP_UP_INVALID 回到执行弹窗并说明原因", retryBody.indexOf("当前账号密码不正确") >= 0, JSON.stringify(retryBody.slice(0, 80)));
  check("重试时原因保留、密码清空", (await page.locator("#dialog-field-reason").inputValue()) === "重试场景" && (await page.locator("#dialog-field-password").inputValue()) === "");
  check("重试时密码为空，提交按钮保持 disabled（影响勾选与原因保留）", await foot.locator("button:text-is('确认重置')").isDisabled());

  // ---- 7. 409 未启用 MFA：结束流程并给出权威结论 ----
  state.resetStatus = 409;
  state.resetBody = failure(409, "CONFLICT_MFA_NOT_ENROLLED", "该账号未启用 MFA");
  await page.locator("#dialog-field-password").fill("correct horse battery staple");
  if (!(await page.locator("#dialog-field-ack").isChecked())) await page.locator("#dialog-field-ack").check();
  await foot.locator("button:text-is('确认重置')").click();
  await page.locator(".dialog__title:text-is('该账号未启用 MFA')").waitFor({ timeout: 10000 });
  check("409 给出「该账号未启用 MFA」", (await dialogBody.innerText()).indexOf("没有启用 MFA") >= 0);
  await foot.locator("button:text-is('知道了')").click();
  await page.waitForTimeout(300);

  check("全流程无未捕获渲染异常", pageErrors.length === 0, pageErrors.join(" | "));

  await cleanupAll();

  const failed = results.filter((r) => !r.ok);
  console.log("");
  console.log("===========================================================");
  console.log("  合计：" + (results.length - failed.length) + " 通过 / " + failed.length + " 未通过");
  console.log("===========================================================");
  process.exit(failed.length > 0 ? 1 : 0);
}

main().catch(async (err) => {
  console.error("验证脚本自身失败：" + (err && err.stack ? err.stack : err));
  await cleanupAll();
  process.exit(1);
});
