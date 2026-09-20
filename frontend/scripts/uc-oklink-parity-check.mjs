/**
 * OKLink 对齐验收（地址详情 · 转账 Tab）
 *
 * 前置：
 *   - 已部署用户端（~/deploy-frontend.sh user）并且 https://127.0.0.1:8448/app-v2 可访问；
 *   - /tmp/polygon-e2e-creds.env 里有可登录的 fixture 身份（EMAIL / PW）。
 *
 * 校验列形状（交易哈希 / 方法 / 时间 / 发送方 / 接收方 / 数量 / 代币）、
 * 千分位与正负号、Out/In 标签、集合与 token_id、工具栏控件与代币选择器。
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
const results = [];
const check = (n, c, e = "") => results.push((c ? "PASS" : "FAIL") + " :: " + n + (e ? " (" + e + ")" : ""));
const browser = await chromium.launch();
const ctx = await browser.newContext({ viewport: { width: 1440, height: 980 }, ignoreHTTPSErrors: true });
const page = await ctx.newPage();
const errors = [];
page.on("pageerror", (e) => errors.push(String(e)));
await page.goto("https://127.0.0.1:8448/app-v2/login", { waitUntil: "domcontentloaded" });
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

// --- 代币模式 ---
await page.goto("https://127.0.0.1:8448/app-v2/addresses/eth/" + TOKEN_ADDR + "?tab=transfers", { waitUntil: "domcontentloaded" });
await page.waitForTimeout(2500);
await page.selectOption('select[aria-label="时间窗筛选"]', "0").catch(() => {});
await page.waitForTimeout(4000);
const closed = await closeInspector();
check("检查器可关闭（关闭后表格占满宽度）", closed);
const widths = await page.evaluate(() => {
  const scroll = document.querySelector(".table-scroll");
  const table = document.querySelector("table.table");
  return {
    scroll: scroll ? { client: scroll.clientWidth, width: scroll.scrollWidth } : null,
    table: table ? { client: table.clientWidth, width: table.scrollWidth } : null,
  };
});
check(
  "活动 Tab 默认全宽且七列不需要横向滚动",
  widths.scroll ? widths.table.width <= widths.scroll.client + 2 : false,
  JSON.stringify(widths),
);
const headers = await page.$$eval("th", (nodes) => nodes.map((n) => n.innerText.trim()));
check("七列全部可见", ["交易哈希", "方法", "时间", "发送方", "接收方", "数量", "代币"].every((h) => headers.includes(h)), headers.join("|"));
const rowText = await page.evaluate(() => {
  const tr = document.querySelector("table.table tbody tr");
  return tr ? Array.from(tr.children).map((td) => td.innerText.replace(/\n/g, " / ")) : [];
});
check("数量列有千分位/符号", /^[+−-][\d,]/.test((rowText[rowText.length - 2] || "").trim()), JSON.stringify(rowText));
check("代币列有符号+全称", /[A-Z]{2,}/.test(rowText[rowText.length - 1] || ""), JSON.stringify(rowText[rowText.length - 1] ?? ""));
check("发送方列带 Out/In 标签", /Out|In/.test((rowText[3] || "") + (rowText[4] || "")), JSON.stringify(rowText.slice(3, 5)));
const dashedCells = () =>
  page.evaluate(() =>
    Array.from(document.querySelectorAll("td span")).filter((el) => {
      const style = getComputedStyle(el);
      return style.borderStyle === "dashed" && style.borderTopWidth !== "0px";
    }).length,
  );
check("解析完整的代币不挂警示框（eth 页）", (await dashedCells()) === 0, String(await dashedCells()));
await page.screenshot({ path: "/tmp/ok2-token-wide.png", fullPage: false });

// --- NFT 模式 ---
await page.goto("https://127.0.0.1:8448/app-v2/addresses/eth/" + NFT_ADDR + "?tab=transfers", { waitUntil: "domcontentloaded" });
await page.waitForTimeout(2500);
await page.selectOption('select[aria-label="时间窗筛选"]', "0").catch(() => {});
await page.waitForTimeout(3500);
await page.getByRole("button", { name: "NFT", exact: true }).first().click().catch(() => {});
await page.waitForTimeout(3000);
await closeInspector();
const nftRow = await page.evaluate(() => {
  const tr = document.querySelector("table.table tbody tr");
  return tr ? Array.from(tr.children).map((td) => td.innerText.replace(/\n/g, " / ")) : [];
});
check("NFT 行：方法=Transfer", /Transfer/.test(nftRow[1] ?? ""), JSON.stringify(nftRow));
check("NFT 行：时间 YYYY/MM/DD", /\d{4}\/\d{2}\/\d{2} \d{2}:\d{2}:\d{2}/.test(nftRow[2] ?? ""), nftRow[2] ?? "");
check("NFT 行：双方地址 + Out/In 标签", /0xfe34/.test(nftRow[3] ?? "") && /0x26db/.test(nftRow[4] ?? "") && /Out/.test(nftRow[3] ?? ""), JSON.stringify(nftRow.slice(3, 5)));
check("NFT 行：合集 + #编号 + 标准", /BAYC|BoredApe/.test(nftRow[6] ?? "") && /4937/.test(nftRow[6] ?? "") && /ERC721/.test(nftRow[6] ?? ""), nftRow[6] ?? "");
await page.screenshot({ path: "/tmp/ok2-nft-wide.png", fullPage: false });
// --- 可疑代币（metadata_status=partial）必须挂框 ---
// 这个 eth 地址持有 metadata_status=partial 的代币（0xc1436f…，2017 年的转账），
// 且只有 12 行，加载快；bsc 那个地址走全量窗口会撞 40s 交互预算，验收不稳定。
await page.goto("https://127.0.0.1:8448/app-v2/addresses/eth/0x0123456789abcdef0123456789abcdef01234567?tab=transfers", { waitUntil: "domcontentloaded" });
await page.waitForTimeout(2500);
await page.selectOption('select[aria-label="时间窗筛选"]', "0").catch(() => {});
// 这个 bsc 地址要走全量窗口（40s 交互预算内），比 eth 的首屏慢得多：
// 必须等真实行出现再继续，否则读到的是骨架屏（0 行）。
await page
  .waitForFunction(() => document.querySelectorAll("table.table tbody tr").length > 0, { timeout: 60000 })
  .catch(() => {});
await page.waitForTimeout(1500);
// 表格是视口虚拟化的：可疑行在最底部（时间最旧），必须先滚到底再数，
// 否则查到的永远只有首屏那几十行 DOM。
const scrollTable = async () => {
  await page.evaluate(() => {
    const el = document.querySelector(".table-scroll");
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
await page.screenshot({ path: "/tmp/ok4-suspicious.png", fullPage: false });
check("无页面级 JS 错误", errors.length === 0, errors.slice(0, 2).join(" | "));
await ctx.close();
await browser.close();
console.log(results.join("\n"));
process.exit(results.some((r) => r.startsWith("FAIL")) ? 1 : 0);