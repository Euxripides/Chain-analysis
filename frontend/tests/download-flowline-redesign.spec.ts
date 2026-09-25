import { expect, test, type Locator, type Page, type Response } from "@playwright/test";

/**
 * DL-FLOW —— 新建数据下载 Flowline 界面重构专项验收
 * （《新建数据下载 Flowline 界面重构实施规范》§25.2）
 *
 * 真实浏览器 + 真实服务端：登录 → /app-v2/downloads/new → 真实 validate-addresses →
 * 真实 preview → 真实 create。**没有任何 mock**：断言的是浏览器实际发出的请求与
 * 服务端真实返回后页面上出现的内容。
 *
 * 环境变量（缺失即 skip，绝不把 skip 当 PASS）：
 *   DLF_EMAIL / DLF_PASSWORD（或复用 R9_EMAIL / R9_PASSWORD）
 *   DLF_ADDRESS / DLF_CHAIN（或复用 R9_ADDRESS / R9_CHAIN）
 *   E2E_BASE_URL（默认 https://127.0.0.1:8448）
 *
 * 覆盖：DL-FLOW-001 … DL-FLOW-010（逐条对应规范 §25.2）。
 */

const EMAIL = process.env.DLF_EMAIL ?? process.env.R9_EMAIL ?? "";
const PASSWORD = process.env.DLF_PASSWORD ?? process.env.R9_PASSWORD ?? "";
const ADDRESS = process.env.DLF_ADDRESS ?? process.env.R9_ADDRESS ?? "";
const CHAIN = process.env.DLF_CHAIN ?? process.env.R9_CHAIN ?? "bsc";
const LOGIN_PATH = process.env.DLF_LOGIN_PATH ?? "/app-v2/login";

const FORBIDDEN = ["复用策略", "prefer_local", "refresh", "默认复用", "强制重新拉取"];
const PREVIEW_TITLES = /无需新增 Provider 请求|Coverage 与成本预览完成|预览完成，存在风险提示/;

type Captured = { url: string; body: string | null; idempotencyKey: string | null; status: number; responseBody: string | null };

function captureApi(page: Page, sink: Captured[]) {
  page.on("response", (response: Response) => {
    const request = response.request();
    if (request.method() !== "POST") return;
    const url = request.url();
    if (!url.includes("/api/v2/downloads")) return;
    void response
      .text()
      .then((text) => {
        sink.push({
          url,
          body: request.postData(),
          idempotencyKey: request.headers()["idempotency-key"] ?? null,
          status: response.status(),
          responseBody: text.slice(0, 20000),
        });
      })
      .catch(() => {});
  });
}

async function login(page: Page) {
  await page.goto(LOGIN_PATH, { waitUntil: "domcontentloaded" });
  await expect(page.locator("#identifier"), "登录页应渲染账号输入框").toBeVisible({ timeout: 30_000 });
  await page.locator("#identifier").fill(EMAIL);
  await page.locator("#password").fill(PASSWORD);
  await page.getByRole("button", { name: /^登录(后台)?$/ }).click();
  await expect.poll(() => page.url(), { timeout: 30_000 }).not.toContain(LOGIN_PATH);
}

async function openPage(page: Page) {
  await page.goto("/app-v2/downloads/new", { waitUntil: "domcontentloaded" });
  await expect(page.locator(".dl-flow-page"), "Flowline 页面根节点").toBeVisible({ timeout: 30_000 });
  await expect(page.locator("#download-addresses"), "地址输入框").toBeVisible({ timeout: 30_000 });
}

/** 选择链：链下拉仍然是 .cs-chainselect（Ant Select）。 */
async function selectChain(page: Page, slug: string) {
  await ensureSectionExpanded(page, "range");
  await page.locator(".cs-chainselect").click();
  await page.locator(`.cs-select-dropdown-wizard .cs-chainoption[data-chain="${slug}"]`).click();
  await page.keyboard.press("Escape");
}

/** 地址导入 + 服务端校验（服务端 accepted 是唯一权威集合）。 */
async function importAddresses(page: Page) {
  await page.locator("#download-addresses").fill(ADDRESS);
  await page.getByRole("button", { name: "解析导入" }).click();
  const verdict = page.locator('[role="dialog"]').filter({ hasText: "服务端地址校验结果" });
  await expect(verdict).toBeVisible({ timeout: 60_000 });
  const text = await verdict.innerText();
  expect(text, "必须显示服务端接受数").toContain("服务端接受");
  await verdict.getByRole("button", { name: "使用服务端结果" }).click();
  await expect(verdict).toBeHidden({ timeout: 20_000 });
}

/**
 * 触发预览并返回预览弹窗。
 * 新链路：`预览 Coverage 与成本` →（必要时先弹服务端校验）→ 真实 preview → 预览弹窗。
 */
async function previewCoverage(
  page: Page,
  buttonName: string | RegExp = /预览 Coverage 与成本|重新预览|重新计算/,
): Promise<Locator> {
  const responsePromise = page.waitForResponse(
    (response) => response.url().includes("/api/v2/downloads/preview") && response.request().method() === "POST",
    { timeout: 120_000 },
  );
  await page.getByRole("button", { name: buttonName }).first().click({ timeout: 30_000 });
  const verdict = page.locator('[role="dialog"]').filter({ hasText: "服务端地址校验结果" });
  if (await verdict.isVisible().catch(() => false)) {
    await verdict.getByRole("button", { name: "使用服务端结果" }).click();
  }
  const response = await responsePromise;
  expect(response.status(), "preview 必须成功").toBeLessThan(300);
  const dialog = page.locator('[role="dialog"]').filter({ hasText: PREVIEW_TITLES });
  await expect(dialog).toBeVisible({ timeout: 90_000 });
  return dialog;
}

/**
 * 移动端（<640px）已完成/未到达的章节是折叠摘要卡（§7.5）：
 * 要操作某个章节里的控件，先点该章节头部的「编辑」把正文展开。桌面端是 no-op。
 */
async function ensureSectionExpanded(page: Page, key: "objects" | "range" | "datasets" | "preview") {
  const section = page.locator("#download-step-" + key);
  const body = section.locator(".dl-section-body");
  if ((await body.count()) > 0) return;
  const edit = section.locator(".dl-section-head__edit");
  if ((await edit.count()) === 0) return;
  await edit.click();
  await expect(body).toBeVisible({ timeout: 10_000 });
  await page.waitForTimeout(150);
}

async function closeDialog(page: Page, dialog: Locator) {
  await dialog.getByRole("button", { name: "知道了" }).click();
  await expect(dialog).toBeHidden({ timeout: 20_000 });
}

async function createTask(page: Page): Promise<Locator> {
  await page.locator(".dl-task-preview button.dl-btn--primary, .dl-actionbar button.dl-btn--primary").first().click();
  return page.locator('[role="dialog"]').filter({ hasText: "确认创建下载任务？" });
}

function skipWithoutCreds() {
  test.skip(!EMAIL || !PASSWORD || !ADDRESS, "缺少 DLF_EMAIL/DLF_PASSWORD/DLF_ADDRESS（不得把 skip 当 PASS）");
}

test.describe("DL-FLOW 新建数据下载 Flowline", () => {
  test.beforeEach(() => {
    skipWithoutCreds();
  });

  test("DL-FLOW-001 复用策略在所有用户可见层不可见", async ({ page }) => {
    await login(page);
    await openPage(page);

    const assertHidden = async (stage: string) => {
      const body = await page.locator("body").innerText();
      for (const needle of FORBIDDEN) {
        expect(body, stage + " 正文不得出现「" + needle + "」").not.toContain(needle);
      }
      const names = await page.evaluate(() =>
        Array.from(document.querySelectorAll("button, a, input, select, option, label, [role], [aria-label], [title]")).map(
          (el) => [
            el.getAttribute("aria-label") ?? "",
            el.getAttribute("title") ?? "",
            (el.textContent ?? "").trim(),
            el.getAttribute("name") ?? "",
          ].join(" "),
        ),
      );
      for (const needle of FORBIDDEN) {
        const hit = names.find((entry) => entry.includes(needle));
        expect(hit, stage + " 不得存在以「" + needle + "」命名的控件（实际：" + hit + "）").toBeUndefined();
      }
      /*
       * 禁止 CSS 假隐藏：不得存在"整类不可见"的表单控件。
       * 例外是刻意做成"透明但铺满可点区域"的原生控件（Dataset 卡 / 分段控件 / 模式卡），
       * 以及被裁剪的文件输入 —— 它们仍然可聚焦、可点击，不属于假隐藏。
       */
      const hidden = await page.evaluate(() =>
        Array.from(document.querySelectorAll("input, select, textarea"))
          .filter((el) => {
            const style = getComputedStyle(el);
            if (style.display === "none" || style.visibility === "hidden") return true;
            if (Number(style.opacity) !== 0) return false;
            const rect = el.getBoundingClientRect();
            return rect.width * rect.height < 400;
          })
          .map((el) => el.getAttribute("aria-label") ?? el.getAttribute("name") ?? el.tagName),
      );
      expect(hidden, stage + " 不得用 CSS 隐藏表单控件（实际：" + hidden.join(" / ") + "）").toHaveLength(0);
    };

    await assertHidden("初始");

    await importAddresses(page);
    await assertHidden("服务端校验弹窗之后");

    const previewDialog = await previewCoverage(page);
    await assertHidden("Coverage 预览弹窗打开时");
    await closeDialog(page, previewDialog);

    const confirm = await createTask(page);
    await expect(confirm).toBeVisible({ timeout: 30_000 });
    await assertHidden("创建确认弹窗打开时");
    await expect(confirm.getByRole("button", { name: "确认创建" })).toBeVisible();
    await page.keyboard.press("Escape");
  });

  test("DL-FLOW-002 preview 与 create 请求体都带 reuse_policy=prefer_local", async ({ page }) => {
    const sink: Captured[] = [];
    captureApi(page, sink);
    await login(page);
    await openPage(page);
    await selectChain(page, CHAIN);
    await importAddresses(page);

    const previewDialog = await previewCoverage(page);
    await closeDialog(page, previewDialog);

    const confirm = await createTask(page);
    await expect(confirm).toBeVisible({ timeout: 30_000 });
    await confirm.getByRole("button", { name: "确认创建" }).click();
    const result = page.locator('[role="dialog"]').filter({ hasText: /下载任务已创建|已复用现有任务|本地数据已覆盖|部分创建成功/ });
    await expect(result).toBeVisible({ timeout: 120_000 });

    const previewCalls = sink.filter((item) => item.url.includes("/api/v2/downloads/preview"));
    const createCalls = sink.filter((item) => /\/api\/v2\/downloads$/.test(item.url));
    expect(previewCalls.length, "必须真实调用过 preview").toBeGreaterThan(0);
    expect(createCalls.length, "必须真实调用过 create").toBeGreaterThan(0);
    for (const call of previewCalls) {
      const body = JSON.parse(call.body ?? "{}") as { reuse_policy?: string; chain?: string; addresses?: string[] };
      expect(body.reuse_policy, "preview 请求必须带 reuse_policy").toBe("prefer_local");
      expect(body.chain, "preview 必须按链分组提交").toBe(CHAIN);
      expect(body.addresses?.length ?? 0, "preview 必须带上服务端接受的地址").toBeGreaterThan(0);
    }
    for (const call of createCalls) {
      const body = JSON.parse(call.body ?? "{}") as { reuse_policy?: string };
      expect(body.reuse_policy, "create 请求必须带 reuse_policy").toBe("prefer_local");
    }
  });

  test("DL-FLOW-003 五步轨道：名称、顺序与当前步骤推导", async ({ page }) => {
    await login(page);
    await openPage(page);

    const isCompact = (await page.viewportSize())!.width < 900;
    const expected = isCompact
      ? ["对象", "范围", "Dataset", "预览", "创建"]
      : ["对象与地址", "链与范围", "Dataset", "Coverage 预览", "创建任务"];

    /* 桌面用横向轨道、移动端用竖向轨道：任一时刻只存在一套（§19.3）。 */
    const horizontal = await page.locator("nav.dl-rail").count();
    const vertical = await page.locator("nav.dl-vrail").count();
    expect(horizontal + vertical, "必须存在且只存在一套 Flowline 轨道").toBe(1);

    const labels = await page.locator(".dl-rail__label, .dl-vrail__label").allInnerTexts();
    expect(labels.map((item) => item.trim())).toEqual(expected);
    expect(labels.join(" ").toLowerCase(), "轨道里不允许出现 reuse 步骤").not.toContain("reuse");
    expect(await page.locator(".ant-steps, .cs-stepper").count(), "不得复用通用 Stepper").toBe(0);

    const currentLabel = async () => (await page.locator('[aria-current="step"] .dl-rail__label, [aria-current="step"] .dl-vrail__label').first().innerText()).trim();

    expect(await currentLabel(), "地址为空时当前步骤必须是对象与地址").toMatch(/对象/);
    const stepCountBefore = await page.locator(".dl-rail__node[disabled], .dl-vrail__node[disabled]").count();
    expect(stepCountBefore, "未到达的步骤必须是 disabled").toBeGreaterThan(0);

    await importAddresses(page);
    const afterImport = await currentLabel();
    /* 链与 Dataset 由服务端元数据自动预填：填完地址后规则上应落在 datasets 或 preview（§6.2）。 */
    expect(afterImport, "地址导入后当前步骤必须前进（datasets 或 Coverage 预览）").toMatch(/Dataset|预览|Coverage/);

    await ensureSectionExpanded(page, "datasets");
    const datasetChecked = await page.locator(".dl-dataset-card[data-selected='true']").count();
    expect(datasetChecked, "服务端元数据必须预选至少一个 Dataset").toBeGreaterThan(0);

    const previewDialog = await previewCoverage(page);
    await closeDialog(page, previewDialog);
    expect(await currentLabel(), "预览有效时当前步骤必须是创建任务").toMatch(/创建/);

    /* 改变范围 → 预览失效 → 当前步骤退回 Coverage 预览，且创建按钮不再直接可提交。 */
    await ensureSectionExpanded(page, "range");
    await page.locator('label:has(input[name="download-range-type"])').filter({ hasText: "区块范围" }).click();
    await expect.poll(currentLabel, { timeout: 10_000 }).toMatch(/预览|Coverage/);
  });

  test("DL-FLOW-004 Dataset 是原生 checkbox，键盘可用，第 7 项被上限挡住", async ({ page }) => {
    await login(page);
    await openPage(page);
    /* 服务端 Dataset 元数据是异步的；移动端还要先展开第 ③ 步（折叠卡里没有 checkbox）。 */
    await ensureSectionExpanded(page, "datasets");
    await expect(page.locator(".dl-dataset-card:not(.dl-dataset-card--skeleton)").first()).toBeVisible({ timeout: 30_000 });

    const boxes = page.getByRole("checkbox");
    const total = await boxes.count();
    /* 真实服务端的 Dataset 白名单是按链给的最多 5 项（backend/internal/model chainDatasetsByChain），
       因此这里只断言"真的渲染出了原生 checkbox"，上限场景见下面的 total >= 7 分支。 */
    expect(total, "服务端返回的 Dataset 必须是原生 checkbox").toBeGreaterThanOrEqual(3);
    await expect(page.locator(".dl-section-head__meta").filter({ hasText: "上限 6" }).first(), "页面必须声明服务端的 6 项上限").toBeVisible();

    const first = page.locator(".dl-dataset-card").first();
    const firstBox = first.getByRole("checkbox");
    const before = await firstBox.isChecked();
    await firstBox.focus();
    await page.keyboard.press("Space");
    await expect.poll(() => firstBox.isChecked(), { timeout: 10_000 }).toBe(!before);
    await page.keyboard.press("Space");
    await expect.poll(() => firstBox.isChecked(), { timeout: 10_000 }).toBe(before);

    /* 选中态不能只靠颜色：卡片必须同时给出 data-selected 与勾选图标。 */
    const selectedCard = page.locator(".dl-dataset-card[data-selected='true']").first();
    await expect(selectedCard).toBeVisible();
    expect(await selectedCard.locator(".dl-dataset-card__check").count(), "选中态必须有勾选图标（不仅靠颜色）").toBe(1);

    if (total < 7) {
      /*
       * 现行链注册表最多 5 个 Dataset，真实环境里够不到 6 项上限。
       * 这里**如实标注**而不是假装通过：上限行为由
       * frontend/scripts/download-flowline-acceptance.mjs（7 个 Dataset 夹具）覆盖。
       */
      test.info().annotations.push({
        type: "note",
        description: "服务端当前链的 Dataset 白名单只有 " + total + " 项，真实环境无法触达 6 项上限；上限行为由 download-flowline-acceptance.mjs 的 7 项夹具断言。",
      });
      return;
    }

    /* 选满 6 项：第 7 项必须不可再选，并通过 Dialog 说明上限。 */
    const ids = ["transactions", "token_transfers", "nft_transfers", "logs", "contract_interactions", "trace", "balances"];
    for (const id of ids) {
      if ((await page.locator(".dl-dataset-card[data-selected='true']").count()) >= 6) break;
      const box = page.getByRole("checkbox", { name: new RegExp("（" + id + "）") }).first();
      if ((await box.count()) === 0) continue;
      if (!(await box.isChecked())) await box.click();
      await page.waitForTimeout(120);
    }
    const checkedCount = await page.locator(".dl-dataset-card[data-selected='true']").count();
    expect(checkedCount, "最多只能选 6 个 Dataset").toBe(6);

    const extra = page.getByRole("checkbox").nth(await page.locator(".dl-dataset-card[data-selected='true']").count());
    for (const id of ids) {
      const box = page.getByRole("checkbox", { name: new RegExp("（" + id + "）") }).first();
      if ((await box.count()) === 0 || (await box.isChecked())) continue;
      await box.click();
      const limitDialog = page.locator('[role="dialog"]').filter({ hasText: "Dataset 数量超出上限" });
      await expect(limitDialog, "选择第 7 项必须通过 Dialog 说明上限").toBeVisible({ timeout: 10_000 });
      await limitDialog.getByRole("button", { name: "知道了" }).click();
      break;
    }
    expect(await page.locator(".dl-dataset-card[data-selected='true']").count(), "第 7 项不得被选中").toBe(6);
    void extra;
  });
  test("DL-FLOW-005 风险与说明只走 Dialog，键盘与滚动行为正确", async ({ page }) => {
    const sink: Captured[] = [];
    captureApi(page, sink);
    await login(page);
    await openPage(page);
    await importAddresses(page);

    /* 页面正文不得出现任何风险横幅/说明卡：风险只允许在弹窗里。 */
    const pageRootText = await page.locator(".dl-flow-page").innerText();
    expect(pageRootText, "页面正文不得出现风险/说明横幅").not.toMatch(/风险|说明|提示/);

    const previewDialog = await previewCoverage(page);
    const dialogText = await previewDialog.innerText();
    expect(dialogText, "预览结论必须在弹窗里给出").toMatch(/Coverage|Provider/);

    /* 服务端返回的风险必须只出现在弹窗内。 */
    const previewCall = sink.filter((item) => item.url.includes("/api/v2/downloads/preview")).pop();
    const risks = (JSON.parse(previewCall?.responseBody ?? "{}") as { data?: { risks?: string[] } }).data?.risks ?? [];
    if (risks.length > 0) {
      const closedDialogText = await previewDialog.innerText();
      for (const risk of risks) {
        expect(closedDialogText, "服务端风险必须出现在弹窗里：" + risk).toContain(risk);
      }
      await closeDialog(page, previewDialog);
      const afterClose = await page.locator("body").innerText();
      for (const risk of risks) {
        expect(afterClose, "弹窗关闭后风险不得留在页面上：" + risk).not.toContain(risk);
      }
    } else {
      await closeDialog(page, previewDialog);
    }

    /* Esc 关闭 + 焦点归还到触发按钮（§55 / DL-FLOW-005）。移动端预览章节此时已折叠，先展开。 */
    await ensureSectionExpanded(page, "preview");
    const trigger = page.getByRole("button", { name: /重新预览|重新计算|预览 Coverage 与成本/ }).first();
    await expect(trigger).toBeVisible({ timeout: 20_000 });
    await trigger.focus();
    const reopened = await previewCoverage(page, /预览 Coverage 与成本|重新预览|重新计算/);
    await expect(reopened).toBeVisible();
    await page.keyboard.press("Escape");
    await expect(reopened).toBeHidden({ timeout: 20_000 });
    const active = await page.evaluate(() => (document.activeElement?.textContent ?? "").trim());
    expect(active, "关闭后焦点必须回到触发按钮").toMatch(/预览|重新计算/);

    /* Tab 焦点不得离开 Dialog。 */
    const dialog = await previewCoverage(page);
    for (let index = 0; index < 8; index += 1) {
      await page.keyboard.press("Tab");
      const inside = await page.evaluate(() => Boolean(document.activeElement?.closest('[role="dialog"]')));
      expect(inside, "Tab 第 " + (index + 1) + " 次后焦点必须仍在弹窗内").toBe(true);
    }

    /* 内容超高时弹窗内部滚动，页面背景不滚动（滚轮必须落在弹窗上）。 */
    const scrollable = page.locator('[role="dialog"] .cs-dialog__body, [role="dialog"] .dl-dialog').first();
    const box = await scrollable.boundingBox();
    if (box) await page.mouse.move(box.x + box.width / 2, box.y + Math.min(box.height / 2, 160));
    const before = await page.evaluate(() => window.scrollY);
    await page.mouse.wheel(0, 600);
    await page.waitForTimeout(400);
    const after = await page.evaluate(() => window.scrollY);
    expect(Math.abs(after - before), "滚动弹窗时页面背景不得跟着滚").toBeLessThanOrEqual(2);
    const overflow = await scrollable.evaluate((node) => node.scrollHeight - node.clientHeight);
    if (overflow > 10) {
      expect(await scrollable.evaluate((node) => node.scrollTop), "超高的弹窗内容必须在弹窗内部滚动").toBeGreaterThan(0);
    }
    await page.keyboard.press("Escape");
  });

  test("DL-FLOW-006 关键文字的清晰度与对比度", async ({ page }) => {
    await login(page);
    await openPage(page);
    await importAddresses(page);

    const targets: Array<{ name: string; selector: string; minSize: number }> = [
      { name: "页面标题", selector: ".cs-pageheader__title", minSize: 22 },
      { name: "章节标题", selector: ".dl-section-head__title", minSize: 16 },
      { name: "Dataset 标签", selector: ".dl-dataset-card__label", minSize: 14 },
      { name: "右侧指标标签", selector: ".dl-preview-metric__label", minSize: 12 },
      { name: "右侧指标数值", selector: ".dl-preview-metric__value", minSize: 20 },
      { name: "主按钮", selector: ".dl-task-preview .dl-btn--primary:not([disabled])", minSize: 14 },
      { name: "轨道标签", selector: ".dl-rail__label, .dl-vrail__label", minSize: 12 },
    ];

    for (const target of targets) {
      const locator = page.locator(target.selector).first();
      if ((await locator.count()) === 0) continue;
      const probe = await locator.evaluate((node) => {
        const own = getComputedStyle(node);
        const ancestors: Array<{ opacity: string; filter: string; backdropFilter: string; background: string }> = [];
        let current: Element | null = node;
        while (current) {
          const computed = getComputedStyle(current);
          ancestors.push({
            opacity: computed.opacity,
            filter: computed.filter,
            backdropFilter: computed.backdropFilter || "none",
            background: computed.backgroundColor,
          });
          current = current.parentElement;
        }
        return {
          color: own.color,
          fontSize: own.fontSize,
          fontWeight: own.fontWeight,
          textShadow: own.textShadow,
          ancestors,
        };
      });

      for (const ancestor of probe.ancestors) {
        expect(Number(ancestor.opacity), target.name + " 的祖先 opacity 必须为 1").toBe(1);
        expect(ancestor.filter, target.name + " 的祖先 filter 必须为 none").toBe("none");
      }
      expect(probe.textShadow, target.name + " 不得使用 text-shadow").toBe("none");
      expect(parseFloat(probe.fontSize), target.name + " 的字号不得小于 " + target.minSize + "px").toBeGreaterThanOrEqual(target.minSize);

      const contrast = await locator.evaluate((node) => {
        const parse = (value: string) => {
          const match = /rgba?\(([^)]+)\)/.exec(value ?? "");
          if (!match) return null;
          const parts = match[1].split(",").map((item) => Number.parseFloat(item));
          return { rgb: [parts[0], parts[1], parts[2]], alpha: parts.length > 3 ? parts[3] : 1 };
        };
        const srgb = (channel: number) => {
          const c = channel / 255;
          return c <= 0.03928 ? c / 12.92 : Math.pow((c + 0.055) / 1.055, 2.4);
        };
        const luminance = (rgb: number[]) => 0.2126 * srgb(rgb[0]) + 0.7152 * srgb(rgb[1]) + 0.0722 * srgb(rgb[2]);
        const chain: Array<{ background: string }> = [];
        let current: Element | null = node;
        while (current) {
          chain.push({ background: getComputedStyle(current).backgroundColor });
          current = current.parentElement;
        }
        let base = [255, 255, 255];
        for (let index = chain.length - 1; index >= 0; index -= 1) {
          const parsed = parse(chain[index].background);
          if (!parsed || parsed.alpha === 0) continue;
          base = [
            Math.round(parsed.rgb[0] * parsed.alpha + base[0] * (1 - parsed.alpha)),
            Math.round(parsed.rgb[1] * parsed.alpha + base[1] * (1 - parsed.alpha)),
            Math.round(parsed.rgb[2] * parsed.alpha + base[2] * (1 - parsed.alpha)),
          ];
        }
        const foreground = parse(getComputedStyle(node).color);
        if (!foreground) return 21;
        const la = luminance(foreground.rgb);
        const lb = luminance(base);
        return (Math.max(la, lb) + 0.05) / (Math.min(la, lb) + 0.05);
      });
      expect(contrast, target.name + " 的对比度必须达到 4.5:1（实际 " + contrast.toFixed(2) + "）").toBeGreaterThanOrEqual(4.5);
    }
  });

  test("DL-FLOW-011 桌面布局舒展、地址导入对齐且链下拉不拼接内部标识", async ({ page }) => {
    await login(page);
    await page.setViewportSize({ width: 1440, height: 900 });
    await openPage(page);

    const layout = await page.evaluate(() => {
      const rect = (selector: string) => {
        const box = document.querySelector(selector)?.getBoundingClientRect();
        return box ? { top: box.top, bottom: box.bottom, height: box.height } : null;
      };
      const style = (selector: string) => {
        const node = document.querySelector(selector);
        if (!node) return null;
        const computed = getComputedStyle(node);
        return {
          borderTopWidth: computed.borderTopWidth,
          overflow: computed.overflow,
          maxHeight: computed.maxHeight,
        };
      };
      const sections = Array.from(document.querySelectorAll(".dl-config-section")).map((node) => node.getBoundingClientRect());
      return {
        page: style(".dl-flow-page"),
        surface: style(".dl-config-surface"),
        rangeFieldset: style(".dl-field--range"),
        modeFieldset: style(".dl-field--mode"),
        textarea: rect(".dl-address__textarea"),
        fileTrigger: rect(".dl-address__file-trigger"),
        sectionGap: sections.length > 1 ? sections[1].top - sections[0].bottom : 0,
      };
    });

    expect(layout.page?.overflow, "桌面页应由浏览器自然滚动，不应固定裁切").toBe("visible");
    expect(layout.page?.maxHeight, "桌面页不应锁死为一屏高度").toBe("none");
    expect(layout.surface?.borderTopWidth, "左栏不应再是一整块拉满高度的边框").toBe("0px");
    expect(layout.rangeFieldset?.borderTopWidth, "范围 fieldset 不应出现浏览器默认横线框").toBe("0px");
    expect(layout.modeFieldset?.borderTopWidth, "性能模式 fieldset 不应出现浏览器默认横线框").toBe("0px");
    expect(layout.sectionGap, "四个配置卡之间应留出明确间距").toBeGreaterThanOrEqual(12);
    expect(Math.abs((layout.textarea?.top ?? 0) - (layout.fileTrigger?.top ?? 0)), "地址框与文件入口顶部必须对齐").toBeLessThanOrEqual(1);
    expect(Math.abs((layout.textarea?.height ?? 0) - (layout.fileTrigger?.height ?? 0)), "地址框与文件入口高度必须一致").toBeLessThanOrEqual(1);
    expect(Math.abs((layout.textarea?.bottom ?? 0) - (layout.fileTrigger?.bottom ?? 0)), "地址框与文件入口底部必须对齐").toBeLessThanOrEqual(1);

    await page.locator(".cs-chainselect").click();
    const options = page.locator(".cs-select-dropdown-wizard .cs-chainoption");
    await expect(options.first()).toBeVisible();
    expect(await options.count(), "链下拉应渲染真实服务端选项").toBeGreaterThan(3);
    expect(await page.locator(".cs-select-dropdown-wizard .cs-chainoption__slug").count(), "可见名称后不得拼接内部 slug").toBe(0);

    const optionGeometry = await options.evaluateAll((nodes) =>
      nodes.map((node) => {
        const icon = node.querySelector(".cs-chainicon")?.getBoundingClientRect();
        const name = node.querySelector(".cs-chainoption__name")?.getBoundingClientRect();
        return {
          iconWidth: icon?.width ?? 0,
          iconHeight: icon?.height ?? 0,
          iconLeft: icon?.left ?? 0,
          centerDelta: icon && name ? Math.abs(icon.top + icon.height / 2 - (name.top + name.height / 2)) : 99,
        };
      }),
    );
    const firstLeft = optionGeometry[0].iconLeft;
    for (const geometry of optionGeometry) {
      expect(geometry.iconWidth, "下拉图标槽位宽度必须统一").toBe(24);
      expect(geometry.iconHeight, "下拉图标槽位高度必须统一").toBe(24);
      expect(Math.abs(geometry.iconLeft - firstLeft), "所有下拉图标必须左对齐").toBeLessThanOrEqual(1);
      expect(geometry.centerDelta, "下拉图标与名称必须垂直居中").toBeLessThanOrEqual(1);
    }
    await page.keyboard.press("Escape");
  });

  test("DL-FLOW-008 四视口：无横向滚动、无遮挡、结构正确", async ({ page }) => {
    await login(page);
    await openPage(page);
    await importAddresses(page);

    const viewports = [
      { name: "1440x900", width: 1440, height: 900 },
      { name: "1024x768", width: 1024, height: 768 },
      { name: "768x1024", width: 768, height: 1024 },
      { name: "390x844", width: 390, height: 844 },
    ];

    for (const viewport of viewports) {
      await page.setViewportSize({ width: viewport.width, height: viewport.height });
      await page.waitForTimeout(400);
      const metrics = await page.evaluate(() => ({
        scrollWidth: document.documentElement.scrollWidth,
        clientWidth: document.documentElement.clientWidth,
      }));
      expect(metrics.scrollWidth, viewport.name + " 不得出现页面级横向滚动").toBeLessThanOrEqual(metrics.clientWidth + 1);

      if (viewport.width >= 1200) {
        const position = await page.locator(".dl-task-preview").evaluate((node) => getComputedStyle(node).position);
        expect(position, "桌面端右栏必须是 sticky").toBe("sticky");
        expect(await page.locator("nav.dl-rail").count(), "桌面端必须是横向轨道").toBe(1);
      }
      if (viewport.width < 640) {
        expect(await page.locator("nav.dl-vrail").count(), "390px 下必须是竖向轨道").toBe(1);
        /* 移动端已完成章节是折叠摘要卡：要量 Dataset 网格先展开第 ③ 步。 */
        if ((await page.locator(".dl-dataset-grid").count()) === 0) {
          await page.locator("#download-step-datasets .dl-section-head__edit").click();
          await page.waitForTimeout(400);
        }
        const columns = await page.locator(".dl-dataset-grid").evaluate((node) => getComputedStyle(node).gridTemplateColumns.split(" ").length);
        expect(columns, "移动端 Dataset 必须是两列").toBe(2);
        await expect(page.locator(".dl-actionbar"), "移动端必须有底部操作栏").toBeVisible();
        for (const selector of [".dl-actionbar button", ".dl-vrail__node", ".dl-dataset-card"]) {
          const box = await page.locator(selector).first().boundingBox();
          if (!box) continue;
          expect(box.height, selector + " 的触摸目标不得小于 44px").toBeGreaterThanOrEqual(43.5);
          expect(box.width, selector + " 的触摸目标不得小于 44px").toBeGreaterThanOrEqual(43.5);
        }
        /* 底部操作栏不得遮住最后一个输入控件。 */
        await page.evaluate(() => window.scrollTo(0, document.documentElement.scrollHeight));
        await page.waitForTimeout(400);
        const card = await page.locator("#download-step-create").boundingBox();
        const bar = await page.locator(".dl-actionbar").boundingBox();
        if (card && bar) {
          expect(card.y + card.height, "底部操作栏不得遮挡最后一个区块").toBeLessThanOrEqual(bar.y + 1);
        }
      }
    }
  });

  test("DL-FLOW-009 真实预览失效：参数变化后必须重新预览", async ({ page }) => {
    await login(page);
    await openPage(page);
    await importAddresses(page);
    const previewDialog = await previewCoverage(page);
    await closeDialog(page, previewDialog);

    await expect(page.locator(".dl-preview-stale"), "预览有效时不得出现失效标记").toHaveCount(0);

    /* 改一个参数（取消一个 Dataset）→ 旧预览必须标记为失效。移动端先展开第 ③ 步。 */
    await ensureSectionExpanded(page, "datasets");
    const selected = page.locator(".dl-dataset-card[data-selected='true']").first();
    await selected.getByRole("checkbox").click();
    await expect(page.locator(".dl-preview-stale").first()).toBeVisible({ timeout: 10_000 });

    const style = await page.locator(".dl-preview-stale").first().evaluate((node) => {
      const computed = getComputedStyle(node);
      return { opacity: computed.opacity, borderTopColor: computed.borderTopColor };
    });
    expect(Number(style.opacity), "失效预览不得用 opacity 表达").toBe(1);
    const rgb = /rgba?\((\d+),\s*(\d+),\s*(\d+)/.exec(style.borderTopColor);
    expect(rgb, "失效预览必须有可见边框（实际 " + style.borderTopColor + "）").not.toBeNull();
    expect(Number(rgb![1]), "失效边框必须是琥珀色（红分量显著高于蓝分量，实际 " + style.borderTopColor + "）").toBeGreaterThan(Number(rgb![3]) + 40);

    /* 失效状态下点创建：必须自动重新预览（真实请求）后才能进入创建确认。 */
    const responsePromise = page.waitForResponse(
      (response) => response.url().includes("/api/v2/downloads/preview") && response.request().method() === "POST",
      { timeout: 120_000 },
    );
    await page.locator(".dl-task-preview button.dl-btn--primary, .dl-actionbar button.dl-btn--primary").first().click();
    await responsePromise;
    const dialog = page.locator('[role="dialog"]').filter({ hasText: PREVIEW_TITLES });
    await expect(dialog).toBeVisible({ timeout: 90_000 });
    await closeDialog(page, dialog);
    const confirm = page.locator('[role="dialog"]').filter({ hasText: "确认创建下载任务？" });
    await expect(confirm, "重新预览之后才允许创建").toBeVisible({ timeout: 30_000 });
    await page.keyboard.press("Escape");
  });

  test("DL-FLOW-010 动效不产生额外业务请求，幂等键保持稳定", async ({ page }) => {
    const sink: Captured[] = [];
    captureApi(page, sink);
    await login(page);
    await openPage(page);
    await importAddresses(page);

    const previewDialog = await previewCoverage(page);
    await closeDialog(page, previewDialog);
    await page.waitForTimeout(1500);

    /* 一条链的地址集合只应产生一次 preview；UI 动效不得再发请求。 */
    expect(sink.filter((item) => item.url.includes("/api/v2/downloads/preview")).length, "一次预览只允许一次 preview 请求").toBe(1);

    /* 第一次创建。 */
    const confirm = await createTask(page);
    await expect(confirm).toBeVisible({ timeout: 30_000 });
    await confirm.getByRole("button", { name: "确认创建" }).click();
    const result = page.locator('[role="dialog"]').filter({ hasText: /下载任务已创建|已复用现有任务|本地数据已覆盖|部分创建成功/ });
    await expect(result).toBeVisible({ timeout: 120_000 });
    const firstCreates = sink.filter((item) => /\/api\/v2\/downloads$/.test(item.url));
    expect(firstCreates.length, "一次创建只允许一次 create 请求").toBe(1);

    /* 第二次创建（同 payload 重试）必须复用同一个幂等键。 */
    await page.locator('[role="dialog"] .cs-dialog__close').first().click();
    await expect(result).toBeHidden({ timeout: 20_000 });
    const confirm2 = await createTask(page);
    await expect(confirm2).toBeVisible({ timeout: 30_000 });
    await confirm2.getByRole("button", { name: "确认创建" }).click();
    await expect(result).toBeVisible({ timeout: 120_000 });
    const allCreates = sink.filter((item) => /\/api\/v2\/downloads$/.test(item.url));
    expect(allCreates.length, "重试必须再发一次 create（由幂等键保证复用同一任务）").toBe(2);
    expect(allCreates[0].idempotencyKey, "每次创建都必须携带 Idempotency-Key").toBeTruthy();
    expect(allCreates[1].idempotencyKey, "同 payload 重试必须复用同一个幂等键").toBe(allCreates[0].idempotencyKey);
    await page.locator('[role="dialog"] .cs-dialog__close').first().click();
  });

  test.describe("DL-FLOW-007 reduced motion", () => {
    test.use({ reducedMotion: "reduce" });

    test("DL-FLOW-007 减少动效：没有移动光点，功能仍然完整", async ({ page }) => {
      /* test.use({reducedMotion}) 之外再显式 emulateMedia 一次：确保媒体特性在任何加载顺序下都生效。 */
      await page.emulateMedia({ reducedMotion: "reduce" });
      await login(page);
      await openPage(page);
      expect(await page.evaluate(() => window.matchMedia("(prefers-reduced-motion: reduce)").matches), "prefers-reduced-motion 必须生效").toBe(true);

      expect(await page.locator(".dl-rail__dot").count(), "reduced motion 下不得渲染移动光点").toBe(0);
      /* 横向轨道有 SVG 激活路径；竖向轨道（<640px）用 CSS 线段表达进度。 */
      const hasPath = (await page.locator(".dl-rail__active").count()) > 0;
      const readProgress = async () =>
        hasPath
          ? page.locator(".dl-rail__active").evaluate((node) => getComputedStyle(node).strokeDasharray)
          : page.locator(".dl-vrail__line").first().evaluate((node) => getComputedStyle(node).backgroundImage);
      const initialDash = await readProgress();

      await importAddresses(page);
      const previewDialog = await previewCoverage(page);
      expect(await page.locator(".dl-rail__dot").count(), "预览后也不得渲染移动光点").toBe(0);

      const dialogTransform = await previewDialog.evaluate((node) => getComputedStyle(node).transform);
      expect(["none", "matrix(1, 0, 0, 1, 0, 0)"], "reduced motion 下弹窗不得有 scale/位移").toContain(dialogTransform);

      await closeDialog(page, previewDialog);
      /* Motion 用 pathLength 归一化后写的是 stroke-dasharray；dashoffset 恒为 0，不能当进度用。 */
      const afterDash = await readProgress();
      expect(afterDash, "激活轨道必须随步骤前进").not.toBe(initialDash);
      await page.waitForTimeout(800);
      const settledDash = await readProgress();
      expect(settledDash, "reduced motion 下 pathLength 必须立即到最终值（没有过渡绘制）").toBe(afterDash);

      /* 功能仍然完整：仍然可以走到创建确认。 */
      const confirm = await createTask(page);
      await expect(confirm).toBeVisible({ timeout: 30_000 });
      await expect(confirm.getByRole("button", { name: "确认创建" })).toBeVisible();
      await page.keyboard.press("Escape");
    });
  });
});
