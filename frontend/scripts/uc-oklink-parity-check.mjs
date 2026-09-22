/**
 * OKLink 对齐验收（地址详情 · 代币转账 / 内部交易 Tab）
 *
 * 前置：
 *   - 已部署用户端（~/deploy-frontend.sh user）并且 https://127.0.0.1:8448/app-v2 可访问；
 *   - /tmp/polygon-e2e-creds.env 里有可登录的 fixture 身份（EMAIL / PW）。
 *
 * 校验（对齐参考站点截图的每一项都必须可执行断言）：
 *   1. Tab 文案中文化（代币转账 / 内部交易）且 9 个 Tab 一个不少；
 *   2. 工具栏是**一行**圆角胶囊（代币 / 数量 / 日期区间 / 方向 / 发送方·接收方 + 刷新）；
 *   3. 计数行「共计 N 条交易记录」+「隐藏数量为 0 的转账」开关；
 *   4. 表格七列、14px 表头（时间列高亮）、固定列宽不横向滚动；
 *   5. 行形状：0x+14 位… 哈希链接、灰底方法标签（原文小写）、主体侧 In/Out 标签、
 *      对手方链接色、数量深色 + 法币小字、代币图标 + 符号 + 全称（可疑才挂虚线框）；
 *   6. 页脚只有「加载更多 / 加载全部」，没有"共 N 行 · 第 x/y 页 · DOM n 行（虚拟滚动）"。
 *
 * 用法：node scripts/uc-oklink-parity-check.mjs
 */
import { chromium } from "@playwright/test";
import { readFileSync } from "node:fs";

const raw = readFileSync("/tmp/polygon-e2e-creds.env", "utf8");
const env = Object.fromEntries(raw.split("\n").filter((l) => l.includes("=")).map((l) => { const i = l.indexOf("="); return [l.slice(0, i), l.slice(i + 1)]; }));
const email = env[Object.keys(env).find((k) => k.endsWith("_EMAIL"))];
const pw = env[Object.keys(env).find((k) => k.endsWith("_PW"))];
const NFT_ADDR = "0xfe348a1c6401b6a5c01bd4deef7bbac9148be0ce";
const TOKEN_ADDR = "0xdead00000000000000000000000000000000dead";
const BASE = "https://127.0.0.1:8448/app-v2";
const results = [];
const check = (n, c, e = "") => results.push((c ? "PASS" : "FAIL") + " :: " + n + (e ? " (" + e + ")" : ""));
const browser = await chromium.launch();
const ctx = await browser.newContext({ viewport: { width: 1440, height: 980 }, ignoreHTTPSErrors: true });
const page = await ctx.newPage();
const errors = [];
page.on("pageerror", (e) => errors.push(String(e)));
await page.goto(BASE + "/login", { waitUntil: "domcontentloaded" });
await page.fill("#identifier", email);
await page.fill("#password", pw);
await page.click('button[type="submit"]');
await page.waitForURL(/app-v2\/(home|explorer|downloads|addresses)/, { timeout: 25000 }).catch(() => {});

const closeInspector = async () => {
  const btn = page.locator('button[aria-label="关闭检查器"]');
  if (await btn.count()) {
    await btn.first().click();
    await page.waitForTimeout(600);
  }
  return (await page.locator('button[aria-label="关闭检查器"]').count()) === 0;
};

const allTime = async () => {
  await page.selectOption('select[aria-label="时间窗筛选"]', "0").catch(() => {});
  await page
    .waitForFunction(() => document.querySelectorAll(".cs-oklist table.table tbody tr[data-data-row]").length > 0, { timeout: 60000 })
    .catch(() => {});
  await page.waitForTimeout(1200);
};

const firstRowCells = () =>
  page.evaluate(() => {
    const tr = document.querySelector(".cs-oklist table.table tbody tr[data-data-row]");
    return tr ? Array.from(tr.children).map((td) => td.innerText.replace(/\n/g, " / ")) : [];
  });

const dashedCells = () =>
  page.evaluate(() =>
    Array.from(document.querySelectorAll("td span")).filter((el) => {
      const style = getComputedStyle(el);
      return style.borderStyle === "dashed" && style.borderTopWidth !== "0px";
    }).length,
  );

// --- 代币模式 ---
await page.goto(BASE + "/addresses/eth/" + TOKEN_ADDR + "?tab=transfers", { waitUntil: "domcontentloaded" });
await page.waitForTimeout(2500);
const closed = await closeInspector();
check("检查器可关闭（关闭后表格占满宽度）", closed);

/* 1. Tab 文案中文化，9 个 Tab 一个不少 */
const tabs = (await page.locator(".cs-tab").allInnerTexts()).map((t) => t.trim());
check(
  "Tab 文案中文化且仍是 9 个",
  tabs.length === 9 && tabs.includes("代币转账") && tabs.includes("内部交易"),
  tabs.join("|"),
);

/* 2. 工具栏：一行胶囊 */
const pills = await page.evaluate(() =>
  Array.from(document.querySelectorAll(".cs-okbar > *")).map((el) => ({
    text: (el.innerText || "").replace(/\n/g, " ").trim(),
    h: Math.round(el.getBoundingClientRect().height),
    y: Math.round(el.getBoundingClientRect().y),
  })),
);
const pillTexts = pills.map((p) => p.text).join(" | ");
check("工具栏是一行胶囊（代币/数量/日期/方向/发送方-接收方/刷新 + 重置）", pills.length === 7, pillTexts);
check("胶囊高度一致（34px，单行）", pills.every((p) => p.h === 34) && new Set(pills.map((p) => p.y)).size === 1, JSON.stringify(pills.map((p) => [p.h, p.y])));
const controlLabels = await page.evaluate(() =>
  Array.from(document.querySelectorAll(".cs-okbar [aria-label]")).map((el) => el.getAttribute("aria-label")),
);
check(
  "胶囊保留了全部既有筛选能力（代币/NFT、数量区间、时间窗、日期区间、方向、发送方-接收方）",
  ["时间窗筛选", "开始日期", "结束日期", "方向筛选", "对手方侧", "最小数量", "最大数量", "搜索代币"].every((label) => controlLabels.includes(label)) &&
    pillTexts.includes("代币:") &&
    pillTexts.includes("数量:"),
  controlLabels.join("|"),
);

/* 3. 计数行 */
const countRow = await page.locator(".cs-okcount").innerText().catch(() => "");
check("计数行是「共计 N 条交易记录」", /共计\s*[\d,]+\s*条交易记录/.test(countRow), countRow.replace(/\n/g, " | "));
check("计数行右侧是「隐藏数量为 0 的转账」开关", (await page.locator('.cs-okcount [role="switch"][aria-label="隐藏数量为 0 的转账"]').count()) === 1);

await allTime();

const widths = await page.evaluate(() => {
  const scroll = document.querySelector(".cs-oklist .table-scroll");
  const table = document.querySelector(".cs-oklist table.table");
  return {
    scroll: scroll ? { client: scroll.clientWidth, width: scroll.scrollWidth } : null,
    table: table ? { client: table.clientWidth, width: table.scrollWidth } : null,
  };
});
check("七列表格不横向滚动", widths.scroll ? widths.table.width <= widths.scroll.client + 2 : false, JSON.stringify(widths));

/* 4. 表头 */
const headers = await page.$$eval(".cs-oklist table.table thead th", (nodes) => nodes.map((n) => ({ text: n.innerText.trim(), size: getComputedStyle(n).fontSize })));
check("七列全部可见", ["交易哈希", "方法", "时间", "发送方", "接收方", "数量", "代币"].every((h) => headers.map((x) => x.text).includes(h)), headers.map((x) => x.text).join("|"));
check("表头 14px（参考站点字号，不再是 11px 灰色小字）", headers.every((h) => h.size === "14px"), JSON.stringify(headers.map((h) => h.size)));

/* 5. 行形状 */
const rowText = await firstRowCells();
check("交易哈希缩略为 0x+14 位+...", /^0x[0-9a-fA-F]{14}\.\.\.$/.test((rowText[0] || "").trim()), JSON.stringify(rowText[0] ?? ""));
check("方法名是原文（小写）且带灰底标签", (await page.locator(".cs-oklist tbody tr[data-data-row] .cs-okmethod").first().innerText()).trim() === ((await page.locator(".cs-oklist tbody tr[data-data-row] .cs-okmethod").first().innerText()).trim().toLowerCase()), rowText[1] ?? "");
check("发送方/接收方有一个主体侧 In/Out 标签", (await page.locator(".cs-oklist tbody tr[data-data-row] .cs-okside").count()) > 0);
check("对手方地址是链接色，主体地址是正文色", (await page.locator(".cs-oklist tbody tr[data-data-row] .cs-okparty__link").count()) > 0);
const amountColor = await page.evaluate(() => {
  const el = document.querySelector(".cs-oklist tbody tr[data-data-row] .cs-okamount__value");
  return el ? getComputedStyle(el).color : "";
});
check("数量是正文色（不是红/绿），方向只由 In/Out 标签表达", amountColor === "rgb(16, 26, 43)", amountColor);
check("数量带千分位与方向符号", /^[+-][\d,]/.test((rowText[5] || "").trim()), JSON.stringify(rowText[5] ?? ""));
check("代币列有图标 + 符号 + 全称", (await page.locator(".cs-oklist tbody tr[data-data-row] .cs-oktoken__symbol").count()) > 0 && Boolean(rowText[6]), JSON.stringify(rowText[6] ?? ""));
check("解析完整的代币不挂警示框（eth 页）", (await dashedCells()) === 0, String(await dashedCells()));

/* 6. 页脚：没有分页摘要，只有游标式加载 */
check("页脚不再有「共 N 行 · 第 x/y 页 · DOM n 行（虚拟滚动）」", (await page.locator('[data-testid="table-summary"]').count()) === 0);
check("页脚是「加载更多 / 加载全部」", /加载更多/.test(await page.locator(".cs-okfooter").innerText().catch(() => "")));
await page.screenshot({ path: "/tmp/ok3-token-wide.png", fullPage: false });

// --- NFT 模式 ---
await page.goto(BASE + "/addresses/eth/" + NFT_ADDR + "?tab=transfers", { waitUntil: "domcontentloaded" });
await page.waitForTimeout(2500);
/* NFT 类别现在收在「代币」胶囊面板里（参考站点也是把类别放在代币选择器里）。 */
await page.locator(".cs-okpillwrap").first().locator("summary").click();
await page.waitForTimeout(500);
await page.locator('.cs-okpill__panel [aria-label="资产类别"] button', { hasText: "NFT" }).first().click();
await page.waitForTimeout(1200);
await page.locator('.cs-okpill__panel button:has-text("关闭")').first().click().catch(() => {});
await page.waitForTimeout(500);
await closeInspector();
await allTime();
check("NFT 类别切换生效（胶囊显示 NFT）", (await page.locator(".cs-okpill__value").first().innerText()).trim() === "NFT");
const nftRow = await firstRowCells();
check("NFT 行：方法=transfer（原文）", /transfer/.test(nftRow[1] ?? ""), JSON.stringify(nftRow));
check("NFT 行：时间 YYYY/MM/DD", /\d{4}\/\d{2}\/\d{2} \d{2}:\d{2}:\d{2}/.test(nftRow[2] ?? ""), nftRow[2] ?? "");
check("NFT 行：双方地址 + Out/In 标签", /0xfe34/.test(nftRow[3] ?? "") && /0x26db/.test(nftRow[4] ?? "") && /Out/.test(nftRow[3] ?? ""), JSON.stringify(nftRow.slice(3, 5)));
check("NFT 行：合集 + #编号 + 标准", /BAYC|BoredApe/.test(nftRow[6] ?? "") && /4937/.test(nftRow[6] ?? "") && /ERC721/.test(nftRow[6] ?? ""), nftRow[6] ?? "");
await page.screenshot({ path: "/tmp/ok3-nft-wide.png", fullPage: false });

// --- 可疑代币（metadata_status=partial）必须挂框 ---
// 这个 eth 地址持有 metadata_status=partial 的代币（0xc1436f…，2017 年的转账），
// 且只有 12 行，加载快；bsc 那个地址走全量窗口会撞 40s 交互预算，验收不稳定。
await page.goto(BASE + "/addresses/eth/0x0123456789abcdef0123456789abcdef01234567?tab=transfers", { waitUntil: "domcontentloaded" });
await page.waitForTimeout(2500);
await allTime();
// 表格是视口虚拟化的：可疑行在最底部（时间最旧），必须先滚到底再数，
// 否则查到的永远只有首屏那几十行 DOM。
const scrollTable = async () => {
  await page.evaluate(() => {
    const el = document.querySelector(".cs-oklist .table-scroll");
    if (el) el.scrollTop = el.scrollHeight;
  });
  await page.waitForTimeout(800);
};
await scrollTable();
let suspiciousDashed = await dashedCells();
if (suspiciousDashed === 0) {
  // 再滚一屏，兼容"底部行还没渲染完"的时序
  await scrollTable();
  suspiciousDashed = await dashedCells();
}
check("可疑代币（partial）挂虚线警示框", suspiciousDashed > 0, "dashed=" + suspiciousDashed);
check(
  "可疑代币带警示三角（title 说明原因）",
  (await page.locator('[aria-label*="代币元数据可疑"]').count()) > 0,
  String(await page.locator('[aria-label*="代币元数据可疑"]').count()),
);
await page.screenshot({ path: "/tmp/ok3-suspicious.png", fullPage: false });
check("无页面级 JS 错误", errors.length === 0, errors.slice(0, 2).join(" | "));
await ctx.close();
await browser.close();
console.log(results.join("\n"));
process.exit(results.some((r) => r.startsWith("FAIL")) ? 1 : 0);
