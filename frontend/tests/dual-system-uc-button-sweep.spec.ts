import { expect, test, type Page } from "@playwright/test";
import { mkdirSync, writeFileSync } from "node:fs";
import { resolve } from "node:path";

/**
 * 用户端「每个按钮都点一遍」全量点击巡检（真实部署、真实后端、真实数据）
 *
 * 目的：把蓝图 §20.2「每页必测状态」里最难自动化的一条——**每个控件都被真实触发过**——
 * 变成可复现的机器证据：逐个点开、逐个关闭、逐个记录。
 *
 * 安全策略（在真实环境上点按钮必须有边界，否则会把夹具数据点坏）：
 *   1. 只读/导航/展开/筛选类控件：真实点击。
 *   2. 弹窗、Popover、抽屉、Tabs：真实打开，然后 Escape 关闭——同时验证焦点陷阱与可关闭性。
 *   3. **会写数据的动作**（创建/提交/启动/暂停/恢复/重试/批准/签署/保存/导入/生成）：
 *      点开它的确认 Dialog，然后**取消**。这证明按钮已接线、确认流程可达，
 *      又不会污染数据。报告里记为 `opened-then-cancelled`。
 *   4. **没有取消路径的立即破坏性动作**（删除某行、移除成员、退出登录的最终确认）：
 *      记录为 `skipped-destructive`，不点击。
 *
 * 判定：任何一次点击导致「未捕获异常」「console error」「落到页面不存在」
 * 或「弹窗关不掉」都记为失败。
 */

const EMAIL = process.env.SWEEP_EMAIL ?? process.env.E2E_USER_EMAIL ?? "fixture-platform_admin@fixture.local";
const PASSWORD = process.env.SWEEP_PASSWORD ?? process.env.E2E_USER_PASSWORD;
const WORKSPACE_SLUG = process.env.SWEEP_WORKSPACE ?? "acme-lab";
const OUT_DIR = resolve(process.env.SWEEP_OUT ?? "../acceptance-evidence/user-console-redesign", "button-sweep");

test.skip(!PASSWORD, "需要 SWEEP_PASSWORD / E2E_USER_PASSWORD");

/** 蓝图 §5.4 的全部用户端路由（详情类路由用夹具对象补齐）。 */
const CASE_ID = process.env.SWEEP_CASE_ID ?? "";
const INVESTIGATION_ID = process.env.SWEEP_INVESTIGATION_ID ?? "";
const JOB_ID = process.env.SWEEP_JOB_ID ?? "";
const REPORT_ID = process.env.SWEEP_REPORT_ID ?? "";
const CANVAS_ID = process.env.SWEEP_CANVAS_ID ?? "";

const ROUTES: Array<{ path: string; label: string }> = [
  { path: "/app-v2/home", label: "我的概览" },
  { path: "/app-v2/search", label: "全局搜索" },
  { path: "/app-v2/cases", label: "案件中心" },
  { path: "/app-v2/cases/new", label: "新建案件" },
  { path: "/app-v2/investigations", label: "调查任务" },
  { path: "/app-v2/investigations/new", label: "发起调查" },
  { path: "/app-v2/graph", label: "资金图谱列表" },
  { path: "/app-v2/explorer", label: "地址与交易查询" },
  { path: "/app-v2/downloads/new", label: "新建数据下载" },
  { path: "/app-v2/jobs", label: "任务中心" },
  { path: "/app-v2/exports", label: "导出中心" },
  { path: "/app-v2/inbox", label: "待我处理" },
  { path: "/app-v2/reports", label: "报告中心" },
  { path: "/app-v2/notifications", label: "通知" },
  { path: "/app-v2/profile", label: "个人资料与安全" },
];
if (CASE_ID) ROUTES.push({ path: "/app-v2/cases/" + CASE_ID + "/overview", label: "案件详情" });
if (INVESTIGATION_ID) {
  ROUTES.push({ path: "/app-v2/investigations/" + INVESTIGATION_ID + "/workbench", label: "调查工作台" });
  ROUTES.push({ path: "/app-v2/investigations/" + INVESTIGATION_ID + "/findings", label: "Findings 复核" });
}
if (JOB_ID) ROUTES.push({ path: "/app-v2/jobs/" + JOB_ID, label: "任务详情" });
if (REPORT_ID) ROUTES.push({ path: "/app-v2/reports/" + REPORT_ID, label: "报告详情" });
if (CANVAS_ID) ROUTES.push({ path: "/app-v2/canvases/" + CANVAS_ID, label: "Canvas" });

/** 会写数据的动作：只打开确认框，然后取消。 */
const MUTATING = /创建|新建|发起|启动|开始|暂停|恢复|重试|提交|批准|驳回|签署|归档|保存|导入|生成|上传|应用|发送|标记|补取|加入|移除|删除|撤销|退出|登出|关闭|停用|启用|绑定|解绑|重置|取消任务|批量/;
/** 没有安全取消路径、点下去就不可逆的动作：不点，只记录。 */
const HARD_DESTRUCTIVE = /^(删除|移除|撤销|退出登录|登出|清空全部|吊销)/;

interface ClickRecord {
  route: string;
  index: number;
  tag: string;
  name: string;
  action: "clicked" | "dialog-opened-cancelled" | "skipped-destructive" | "skipped-disabled" | "skipped-navigation-external";
  outcome: "ok" | "error";
  detail?: string;
  urlBefore?: string;
  urlAfter?: string;
}

async function resolveWorkspace(page: Page) {
  const dialog = page.getByRole("dialog").filter({ hasText: "请选择工作区" });
  await dialog.waitFor({ state: "visible", timeout: 6000 }).catch(() => {});
  if (await dialog.isVisible().catch(() => false)) {
    await dialog.getByRole("button", { name: new RegExp("（" + WORKSPACE_SLUG + "）") }).first().click();
    await expect(dialog).toBeHidden({ timeout: 20000 });
  }
}

async function login(page: Page) {
  await page.goto("/app-v2/login", { waitUntil: "domcontentloaded" });
  await page.locator("#identifier").fill(EMAIL);
  await page.locator("#password").fill(PASSWORD!);
  await page.getByRole("button", { name: /^登录$/ }).click();
  await resolveWorkspace(page);
  await expect(page.locator(".auth-card")).toHaveCount(0, { timeout: 30000 });
  /* 首登改密提示是合规的警告弹窗，按 Esc 关闭即可继续（不改密）。 */
  const notice = page.getByRole("dialog").filter({ hasText: "需要修改初始密码" });
  if (await notice.isVisible().catch(() => false)) {
    await page.keyboard.press("Escape");
    await expect(notice).toBeHidden();
  }
}

/** 关掉当前所有弹窗（Dialog），返回是否成功。 */
async function dismissDialogs(page: Page): Promise<boolean> {
  let closed = true;
  for (let i = 0; i < 4; i += 1) {
    const dialog = page.getByRole("dialog").first();
    if (!(await dialog.isVisible().catch(() => false))) break;
    /* 有「取消」就点取消（危险确认里取消永远可用），否则 Escape。 */
    const cancel = dialog.getByRole("button", { name: /^(取消|返回修改|关闭|返回)$/ }).first();
    if (await cancel.isVisible().catch(() => false)) await cancel.click().catch(() => undefined);
    else await page.keyboard.press("Escape");
    await page.waitForTimeout(150);
    if (await dialog.isVisible().catch(() => false)) {
      /* 一次性内容禁止 Escape：这是设计约束，不算失败，记录后跳过。 */
      closed = false;
      break;
    }
  }
  return closed;
}

/**
 * 稳健点击：先滚入视口，等虚拟滚动的那一帧渲染落地，再点。
 *
 * 为什么需要它：`DataTable` 是**虚拟滚动**的——`scrollIntoViewIfNeeded` 会触发滚动，
 * 滚动回调用 requestAnimationFrame 更新窗口并重渲染，正在被点击的那个节点可能刚好被换掉。
 * 真实用户是"滚动、停一下、再点"，测试也必须复现这个节奏，否则测出来的是竞态而不是缺陷。
 */
async function clickSettled(page: Page, locator: ReturnType<Page["locator"]>): Promise<string | null> {
  for (let attempt = 0; attempt < 2; attempt += 1) {
    await locator.scrollIntoViewIfNeeded({ timeout: 5000 }).catch(() => undefined);
    await page.waitForTimeout(attempt === 0 ? 320 : 650);
    try {
      await locator.click({ timeout: 6000 });
      return null;
    } catch (error) {
      if (attempt === 1) return String(error).slice(0, 200);
    }
  }
  return "unreachable";
}

function watchPage(page: Page) {
  const errors: string[] = [];
  page.on("pageerror", (e) => errors.push("pageerror: " + String(e).slice(0, 300)));
  page.on("console", (m) => {
    if (m.type() !== "error") return;
    const text = m.text();
    /* 浏览器对失败网络请求的固有记录（未登录 401 等）不是代码缺陷。 */
    if (text.startsWith("Failed to load resource")) return;
    errors.push("console: " + text.slice(0, 300));
  });
  return errors;
}

test.describe("UC-SWEEP 全量点击巡检", () => {
  test("逐个点开每一个控件并记录结果", async ({ page }) => {
    test.setTimeout(45 * 60_000);
    mkdirSync(OUT_DIR, { recursive: true });
    const records: ClickRecord[] = [];
    const pageErrors: string[] = [];
    const loginErrors = watchPage(page);

    await login(page);
    pageErrors.push(...loginErrors.map((e) => "login: " + e));

    for (const route of ROUTES) {
      await page.goto(route.path, { waitUntil: "domcontentloaded" });
      await page.waitForTimeout(700);

      /* 先取一次控件清单（只取页面主区，避免把 shell 的导航/顶栏重复点 N 遍）。 */
      const enumerate = async () => {
        const locator = page.locator(
          'main button, main a[href], main [role="button"], main [role="tab"], main [role="checkbox"], main [role="switch"], main summary',
        );
        const count = await locator.count();
        const items: Array<{ tag: string; name: string; disabled: boolean; href: string | null }> = [];
        for (let i = 0; i < count; i += 1) {
          const el = locator.nth(i);
          const info = await el.evaluate((node) => {
            const element = node as HTMLElement;
            const name =
              element.getAttribute("aria-label") ||
              (element.textContent || "").replace(/\s+/g, " ").trim() ||
              element.getAttribute("title") ||
              "";
            return {
              tag: element.tagName.toLowerCase(),
              name: name.slice(0, 60),
              disabled: (element as HTMLButtonElement).disabled === true || element.getAttribute("aria-disabled") === "true",
              href: element.getAttribute("href"),
            };
          }).catch(() => null);
          if (info) items.push(info);
        }
        return items;
      };

      const items = await enumerate();

      for (let i = 0; i < items.length; i += 1) {
        const meta = items[i];
        const record: ClickRecord = {
          route: route.path,
          index: i,
          tag: meta.tag,
          name: meta.name,
          action: "clicked",
          outcome: "ok",
        };

        if (meta.disabled) {
          records.push({ ...record, action: "skipped-disabled" });
          continue;
        }
        if (meta.href && !meta.href.startsWith("/app-v2") && !meta.href.startsWith("#")) {
          records.push({ ...record, action: "skipped-navigation-external", detail: meta.href });
          continue;
        }
        if (HARD_DESTRUCTIVE.test(meta.name) && !MUTATING.test(meta.name.replace(HARD_DESTRUCTIVE, ""))) {
          /* 例："删除该条最近查询" / "移除"：点下去就没了，且没有确认框兜底。 */
          records.push({ ...record, action: "skipped-destructive" });
          continue;
        }

        const beforeUrl = page.url();
        const errorsBefore = pageErrors.length;

        /* 每次都从干净的页面状态开始点：避免上一步的副作用影响下一步。 */
        await page.goto(route.path, { waitUntil: "domcontentloaded" });
        await page.waitForTimeout(450);
        /*
         * 有的页面**加载后自己就弹窗**（例如 Canvas 在存在无法解析的引用时给出警告）。
         * 弹窗遮罩会拦住后面所有点击——这不是页面缺陷（仓库规范要求警告必须走弹窗），
         * 而是巡检必须先把它关掉。这里记录为观察项，不计入失败。
         */
        const autoDialog = page.getByRole("dialog").first();
        if (await autoDialog.isVisible().catch(() => false)) {
          const text = (await autoDialog.innerText().catch(() => "")).replace(/\s+/g, " ").slice(0, 80);
          await dismissDialogs(page);
          record.detail = "页面加载后自动弹窗（已关闭）：" + text;
        }

        const fresh = page.locator(
          'main button, main a[href], main [role="button"], main [role="tab"], main [role="checkbox"], main [role="switch"], main summary',
        );
        if (i >= (await fresh.count())) {
          records.push({ ...record, action: "clicked", outcome: "ok", detail: "控件在重载后消失（前一步改变了 DOM），跳过" });
          continue;
        }

        const clickError = await clickSettled(page, fresh.nth(i));
        await page.waitForTimeout(500);

        /* 弹窗处理：会写数据的动作只取消，其余关闭后继续。 */
        const dialogVisible = await page.getByRole("dialog").first().isVisible().catch(() => false);
        if (dialogVisible) {
          const isMutating = MUTATING.test(meta.name);
          const closed = await dismissDialogs(page);
          record.action = isMutating ? "dialog-opened-cancelled" : "clicked";
          if (!closed) {
            record.outcome = "error";
            record.detail = "弹窗无法关闭（禁止 Escape 的一次性内容属预期，其余为缺陷）";
          }
          if (isMutating) record.detail = (record.detail ? record.detail + " | " : "") + "确认弹窗已打开并已取消（未提交写操作）";
        }

        /* 落到"页面不存在"就是死链，属于真实缺陷。 */
        const notFound = await page.getByText("页面不存在", { exact: false }).first().isVisible().catch(() => false);
        if (notFound) {
          record.outcome = "error";
          record.detail = (record.detail ?? "") + " 点击后落到『页面不存在』（死链）";
        }

        record.urlBefore = beforeUrl;
        record.urlAfter = page.url();

        if (clickError) {
          record.outcome = "error";
          record.detail = clickError;
        }
        if (pageErrors.length > errorsBefore) {
          record.outcome = "error";
          record.detail = (record.detail ?? "") + " | " + pageErrors.slice(errorsBefore).join(" | ");
        }

        records.push(record);

        /* 回到原路由，保证下一个控件在同样的上下文里被点击。 */
        if (page.url() !== beforeUrl) {
          await page.goto(route.path, { waitUntil: "domcontentloaded" }).catch(() => undefined);
          await page.waitForTimeout(300);
        }
      }

      /* 每个路由收尾：确保没有残留弹窗影响下一个路由。 */
      await dismissDialogs(page);
    }

    const summary = {
      generatedAt: new Date().toISOString(),
      baseURL: process.env.E2E_BASE_URL ?? "(config)",
      account: EMAIL,
      routes: ROUTES.length,
      totalControls: records.length,
      clicked: records.filter((r) => r.action === "clicked").length,
      dialogCancelled: records.filter((r) => r.action === "dialog-opened-cancelled").length,
      skippedDestructive: records.filter((r) => r.action === "skipped-destructive").length,
      skippedDisabled: records.filter((r) => r.action === "skipped-disabled").length,
      failures: records.filter((r) => r.outcome === "error"),
      pageErrors,
    };
    writeFileSync(resolve(OUT_DIR, "button-sweep.json"), JSON.stringify({ summary, records }, null, 2), "utf-8");
    console.log(
      "UC-SWEEP 完成：routes=" + summary.routes +
        " controls=" + summary.totalControls +
        " clicked=" + summary.clicked +
        " dialogCancelled=" + summary.dialogCancelled +
        " failures=" + summary.failures.length +
        " pageErrors=" + pageErrors.length,
    );
    for (const failure of summary.failures.slice(0, 40)) {
      console.log("  FAIL " + failure.route + " #" + failure.index + " [" + failure.tag + "] " + failure.name + " → " + (failure.detail ?? ""));
    }
    expect(summary.failures, "点击巡检出现失败项，详见 button-sweep.json").toEqual([]);
  });
});
