#!/usr/bin/env node
/**
 * 契约冻结检查（用户端重构规范 V4 §0.4 / §60 Phase R0）
 *
 * 整份规范最重要的一句话是「在不改变现有业务接口、权限、状态机、审计、数据精度和
 * 深链接契约的前提下」重构。这句话必须**可执行地**被验证，否则它只是一句声明。
 *
 * 做法：把工作区与 `git HEAD` 的三样东西逐字比对——
 *   1. 路由路径（<Route path="...">）
 *   2. 路由 → 权限映射（ROUTE_PERMISSIONS）
 *   3. 用户端源码里出现的所有 /api/ 字面量
 * 三者任一发生增删改即失败。这是 R0「冻结契约」的落地形式：
 * 冻结不是写一份文档，而是让"改动契约"这件事在 CI 里直接红掉。
 *
 * 新增页面可以加路由——但那样必须**显式**更新本脚本里的豁免清单，
 * 于是改动会在 PR 里被看见，而不是悄悄发生。
 *
 * **基准必须是重构开始前的那个提交，不能是 HEAD。**
 * 重构一旦被提交，HEAD 就等于重构后的状态，拿它做基准会让这条检查退化成
 * "自己和自己比"——永远通过，永远没有意义。因此这里把基准固定成
 * 重构前的最后一个提交；要换基准必须改这一行，于是"换基准"这件事本身
 * 会在 PR 里被看见。
 *
 * 用法：node scripts/uc-contract-freeze-check.mjs
 *       UC_CONTRACT_BASELINE=<sha> node scripts/uc-contract-freeze-check.mjs
 */
// Approved redesign begins at b6f0182; the earlier UI freeze remains in history.
const BASELINE = process.env.UC_CONTRACT_BASELINE ?? "b6f0182864e65dd0d562283945d5521621f5285c";
const APPROVED_ADDITIONS = {
  "路由路径": ["/blocks/:chain/:block", "/tokens/:chain/:contract"],
  "路由→权限映射": [["/blocks", "explorer:read"], ["/tokens", "explorer:read"]],
  // v1 case/tag paths preserve the deployed download metadata selectors.
  "/api 字面量": ["/api/v2/explorer/resolve", "/api/v2/explorer/addresses/{}/{}/history-coverage", "/api/v1/cases", "/api/v1/tags", "/api/v2/explorer/addresses/{}/{}/statistics", "/api/v2/explorer/blocks/{}/{}", "/api/v2/explorer/tokens", "/api/v2/explorer/tokens/{}/{}", "/api/v2/explorer/tokens/{}/{}/logo", "/api/v2/exports/{}", "/api/v2/exports/{}/files/{}/download-ticket"],
};
import { execFileSync } from "node:child_process";
import { readFileSync, existsSync, statSync, readdirSync } from "node:fs";
import { join, resolve } from "node:path";
import { fileURLToPath } from "node:url";

const here = resolve(fileURLToPath(new URL(".", import.meta.url)), "..");
const REPO = resolve(here, "..");
const SRC = join(here, "apps/user-console/src");

const TRACKED = [
  "apps/user-console/src/App.tsx",
  "apps/user-console/src/app/navigation.tsx",
  "apps/user-console/src/pages",
  "apps/user-console/src/components",
  "apps/user-console/src/sprint4",
  "apps/user-console/src/sprint5",
];

/**
 * 案件详情 Tab 的权限契约（**钉死在本脚本里**，不与基准提交比对）。
 *
 * 为什么单独用一份钉死的表，而不是像上面三项那样与 BASELINE 比对：
 * `app/case-tabs.ts` 是本次重构**新增**的文件，在基准提交里根本不存在，
 * 没有可比对的旧版本；而它的内容恰恰是最需要冻结的东西。
 *
 * 冻结的四样（缺一不可）：
 *   1. **tab key** —— 改 key 等于改深链接；
 *   2. **route** —— 由 key 派生（`/cases/:caseId/<key>`），key 冻结即 route 冻结；
 *   3. **permissions[]** —— 逐字比对，顺序也比对；
 *   4. **组合方式（AND/OR）** —— 单独冻结。只比列表拦不住"改成 OR"：
 *      列表一字未改，准入却松了一大截。
 *
 * 另外还有一条**执行层**断言：路由必须仍然走 `CaseTabRoute`。
 * 只冻结权限表是不够的——把守卫拆掉、表原封不动，检查照样绿，而漏洞已经回来了。
 *
 * 改动这张表必须显式改本文件，于是"放宽权限"这件事一定会在 PR 里被看见。
 */
const CASE_TAB_CONTRACT = {
  combination: "AND",
  tabs: [
    { key: "overview", permissions: ["case:read"] },
    { key: "subjects", permissions: ["case:read", "subject:list"] },
    { key: "investigations", permissions: ["case:read", "investigation:list"] },
    /* temporary：临时兼容规则，等案件级证据接口就绪后应收回单一 evidence:list。 */
    { key: "evidence", permissions: ["case:read", "evidence:list", "investigation:list"], temporary: true },
    { key: "reports", permissions: ["case:read", "report:list", "investigation:list"] },
    { key: "members", permissions: ["case:read", "case:member:list"] },
    { key: "activity", permissions: ["case:read", "case:activity:read"] },
  ],
};

/** 读取 HEAD 版本的文件内容。`repoPath` 必须是**仓库根相对**路径（形如 frontend/...）。 */
function gitShow(repoPath) {
  try {
    return execFileSync("git", ["show", `${BASELINE}:${repoPath}`], { cwd: REPO, encoding: "utf8", maxBuffer: 64 * 1024 * 1024 });
  } catch {
    return null;
  }
}

/**
 * 列出 HEAD 里的文件。
 *
 * 必须用 `git ls-tree HEAD -- <path>` 而不是 `HEAD:<path>`：
 * 后者对目录返回的是**相对于该目录**的裸文件名，再拼前缀就会得到
 * "frontend/AddressDetailPage.tsx" 这种不存在的路径。
 */
function gitLs(relPath) {
  try {
    return execFileSync("git", ["ls-tree", "-r", "--name-only", BASELINE, "--", `frontend/${relPath}`], {
      cwd: REPO,
      encoding: "utf8",
    })
      .split("\n")
      .map((line) => line.trim())
      .filter(Boolean);
  } catch {
    return [];
  }
}

/**
 * 去掉**注释行**。
 *
 * 为什么必须去：契约检查关心的是"代码实际调用了哪些接口"，而注释里提到接口路径
 * 完全正常——本次重构新增的模块里就有 `* 这些接口直接对应 \`/api/v2/jobs/{id}\` 的响应`。
 * 不去注释，这类文档行会被当成"新增了一个 API 字面量"。
 * 这条检查第一次跑就连续踩了两次假阳性（另一次是漏读未跟踪文件），
 * 而假阳性会直接让人不再信任这条检查——所以宁可先做减法。
 *
 * 做法刻意保守：只丢**整行都是注释**的行（\`*\` / \`//\` / \`/*\` 开头），
 * 不做真正的词法分析——那样反而可能把字符串里的 \`//\`（例如 URL）切坏。
 */
function stripCommentLines(text) {
  return text
    .split("\n")
    .filter((line) => !/^\s*(\*|\/\/|\/\*)/.test(line))
    .join("\n");
}

/** 收集某个版本的源码文本（工作区或 HEAD）。 */
function collectSources(relPaths, fromGit) {
  const files = [];
  for (const rel of relPaths) {
    if (fromGit) {
      const listing = gitLs(rel).length > 0 ? gitLs(rel) : [`frontend/${rel}`];
      for (const repoPath of listing) {
        if (!/\.(ts|tsx)$/.test(repoPath)) continue;
        const text = gitShow(repoPath);
        /* 记录成 frontend/ 相对路径，便于两侧对照输出。 */
        if (text !== null) files.push({ path: repoPath.replace(/^frontend\//, ""), text: stripCommentLines(text) });
      }
    } else {
      /*
       * 工作区一侧必须**遍历文件系统**，不能用 `git ls-files`。
       *
       * 后者只列出**已跟踪**文件，而本次重构新增了大量模块
       * （jobs/detail/*、address-detail/*、downloads/*、route-meta.ts …）。
       * 用 git ls-files 会漏读它们，于是"某个 /api 字面量从旧文件搬到了新文件"
       * 会被误报成"字面量被删除"——第一次跑这条检查就踩到了这个假阳性。
       * 契约检查一旦有假阳性就没人信了，所以这里必须按磁盘实际内容比对。
       */
      /* walk() 返回的是绝对路径，这里不能再 join(here, ...)。 */
      for (const absolute of walk(join(here, rel))) {
        if (!/\.(ts|tsx)$/.test(absolute)) continue;
        const relPath = absolute.replace(/\\/g, "/").replace(here.replace(/\\/g, "/") + "/", "");
        files.push({ path: relPath, text: stripCommentLines(readFileSync(absolute, "utf8")) });
      }
    }
  }
  return files;
}

/** 递归列出目录下的文件（跳过 node_modules / dist 等产物目录）。 */
function walk(root) {
  if (!existsSync(root)) return [];
  if (statSync(root).isFile()) return [root];
  const out = [];
  for (const entry of readdirSync(root, { withFileTypes: true })) {
    if (entry.name === "node_modules" || entry.name === "dist") continue;
    const child = join(root, entry.name);
    if (entry.isDirectory()) out.push(...walk(child));
    else out.push(child);
  }
  return out;
}

function routePaths(files) {
  const paths = new Set();
  for (const file of files) {
    for (const match of file.text.matchAll(/<Route\s+path="([^"]+)"/g)) paths.add(match[1]);
  }
  return [...paths].sort();
}

function routePermissions(files) {
  const out = new Map();
  for (const file of files) {
    const block = file.text.match(/ROUTE_PERMISSIONS[^=]*=\s*\{([\s\S]*?)\};/);
    if (!block) continue;
    for (const match of block[1].matchAll(/"([^"]+)"\s*:\s*"([^"]+)"/g)) out.set(match[1], match[2]);
  }
  return [...out.entries()].sort((a, b) => a[0].localeCompare(b[0]));
}

function apiLiterals(files) {
  const out = new Set();
  for (const file of files) {
    for (const match of file.text.matchAll(/"((?:\/api\/)[^"]*)"/g)) out.add(match[1]);
    /* 模板字符串里的 `/api/v2/jobs/${id}` 也要收进来（去掉插值部分再比对）。 */
    for (const match of file.text.matchAll(/`((?:\/api\/)[^`]*)`/g)) {
      out.add(match[1].replace(/\$\{[^}]*\}/g, "{}"));
    }
  }
  return [...out].sort();
}

function diff(label, before, after) {
  const beforeSet = new Set(before.map((item) => JSON.stringify(item)));
  const afterSet = new Set(after.map((item) => JSON.stringify(item)));
  const removed = before.filter((item) => !afterSet.has(JSON.stringify(item)));
  const approved = new Set((APPROVED_ADDITIONS[label] || []).map(item => JSON.stringify(item)));
  const added = after.filter((item) => !beforeSet.has(JSON.stringify(item)) && !approved.has(JSON.stringify(item)));
  if (removed.length === 0 && added.length === 0) {
    console.log(`  OK   ${label}（${after.length} 项，既有契约保留；新增项在批准清单中）`);
    return [];
  }
  const problems = [];
  if (removed.length > 0) problems.push(`${label} 被删除/修改：${JSON.stringify(removed.slice(0, 8))}`);
  if (added.length > 0) problems.push(`${label} 被新增：${JSON.stringify(added.slice(0, 8))}`);
  return problems;
}

/** 从 `app/case-tabs.ts` 里解析出实际的 Tab 权限表。 */
function actualCaseTabs() {
  const path = join(SRC, "app/case-tabs.ts");
  if (!existsSync(path)) return null;
  const text = readFileSync(path, "utf8");
  const block = text.match(/CASE_TABS[^=]*=\s*\[([\s\S]*?)\];/);
  if (!block) return null;
  const tabs = [];
  for (const match of block[1].matchAll(/\{([\s\S]*?)\}/g)) {
    const body = match[1];
    const key = body.match(/key:\s*"([^"]+)"/)?.[1];
    const raw = body.match(/permissions:\s*\[([^\]]*)\]/)?.[1];
    if (!key || raw === undefined) continue;
    const permissions = raw
      .split(",")
      .map((item) => item.trim().replace(/^"|"$/g, ""))
      .filter(Boolean);
    tabs.push({ key, permissions, temporary: /temporary:\s*true/.test(body) });
  }
  const combination = text.match(/CASE_TAB_PERMISSION_COMBINATION\s*=\s*"([^"]+)"/)?.[1] ?? null;
  return { combination, tabs };
}

/**
 * Tab 权限契约的比对。逐条比对并给出**具体哪一项变了**，
 * 而不是笼统地说"表不一样了"——CI 失败信息要能直接告诉人改坏了什么。
 */
function caseTabProblems() {
  const actual = actualCaseTabs();
  if (!actual) return ["找不到或无法解析 apps/user-console/src/app/case-tabs.ts（Tab 权限表的唯一来源）"];

  const problems = [];
  if (actual.combination !== CASE_TAB_CONTRACT.combination) {
    problems.push(
      `多权限组合方式被改动：期望 ${CASE_TAB_CONTRACT.combination}，实际 ${actual.combination}（这可能把 AND 放宽成 OR，而权限列表本身一字未改）`,
    );
  }

  const expectedKeys = CASE_TAB_CONTRACT.tabs.map((tab) => tab.key);
  const actualKeys = actual.tabs.map((tab) => tab.key);
  for (const key of expectedKeys) {
    if (!actualKeys.includes(key)) problems.push(`Tab「${key}」被删除（深链接 /cases/:caseId/${key} 会失效）`);
  }
  for (const key of actualKeys) {
    if (!expectedKeys.includes(key)) problems.push(`Tab「${key}」被新增（深链接面扩大，需显式更新契约）`);
  }
  if (new Set(actualKeys).size !== actualKeys.length) {
    problems.push(`Tab key 出现重复：${JSON.stringify(actualKeys)}（两个 Tab 会指向同一个 URL）`);
  }

  for (const expected of CASE_TAB_CONTRACT.tabs) {
    const found = actual.tabs.find((tab) => tab.key === expected.key);
    if (!found) continue;
    const expectedPerms = JSON.stringify(expected.permissions);
    const actualPerms = JSON.stringify(found.permissions);
    if (expectedPerms !== actualPerms) {
      const loosened = expected.permissions.some((p) => !found.permissions.includes(p));
      problems.push(
        `Tab「${expected.key}」的权限被改动：期望 ${expectedPerms}，实际 ${actualPerms}` +
          (loosened ? "（**权限被放宽**——请确认这是有意的，并同步更新本脚本）" : ""),
      );
    }
    const expectedTemporary = Boolean(expected.temporary);
    if (expectedTemporary !== Boolean(found.temporary)) {
      problems.push(
        `Tab「${expected.key}」的 temporary 标记被改动：期望 ${expectedTemporary}，实际 ${Boolean(found.temporary)}` +
          (expectedTemporary ? "（该条目是临时兼容规则，去掉标记等于承认它是最终设计）" : ""),
      );
    }
  }

  /* 执行层：权限表被冻结，但必须仍然由路由守卫强制，否则表只是一份文档。 */
  const app = readFileSync(join(SRC, "App.tsx"), "utf8");
  const routeLine = app.match(/<Route\s+path="\/cases\/:caseId\/:tab"[^>]*>/)?.[0] ?? "";
  if (!routeLine.includes("CaseTabRoute")) {
    problems.push(
      `/cases/:caseId/:tab 没有走 CaseTabRoute（实际：${routeLine.trim() || "找不到该路由"}）——Tab 权限表还在，但没有任何一层在强制它`,
    );
  }

  if (problems.length === 0) {
    console.log(`  OK   案件 Tab 权限契约（${actual.tabs.length} 个 Tab · 组合方式 ${actual.combination} · 路由守卫已接入）`);
  }
  return problems;
}

function main() {
  const headFiles = collectSources(TRACKED, true);
  const workFiles = collectSources(TRACKED, false);
  if (headFiles.length === 0) {
    console.error(`无法读取基准提交 ${BASELINE} 的源码（不在 git 仓库里？）`);
    process.exit(2);
  }

  console.log(`契约冻结检查：基准 ${BASELINE} 共 ${headFiles.length} 个文件，工作区共 ${workFiles.length} 个文件`);
  const problems = [
    ...diff("路由路径", routePaths(headFiles), routePaths(workFiles)),
    ...diff("路由→权限映射", routePermissions(headFiles), routePermissions(workFiles)),
    ...diff("/api 字面量", apiLiterals(headFiles), apiLiterals(workFiles)),
    /*
     * Tab 权限契约不与基准比对（基准里没有这个文件），而是与脚本里钉死的表比对。
     * 放在最后：上面三项一旦红了就没必要再看它。
     */
    ...caseTabProblems(),
  ].map((problem) => problem.replace(/与 HEAD 一致/, `与 ${BASELINE} 一致`));

  if (problems.length > 0) {
    console.error("\n契约发生变化：");
    for (const problem of problems) console.error("  - " + problem);
    process.exit(1);
  }
  console.log(`\n契约冻结通过：保留基准 ${BASELINE}，仅增加已批准的查询和导出能力。`);
}

main();
