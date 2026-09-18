#!/usr/bin/env python3
# make-r10v4-suite.py — R10 v9 施工单（1420 交接 §5）的**机械**生成器。
#
# 生成 3 个 v4 文件（v3 原件逐字节保留，不 hot-edit）：
#   1) frontend/tests/r10v4-export-excelize.spec.ts   = v3 export spec + toggles 参数 + R10-01 快照唯一化 + R10-09 用例
#   2) frontend/tests/r10v4-legacy-export-import.spec.ts = v3 legacy spec **逐字节复制**（只换文件名，内容不变）
#   3) frontend/playwright.r10v4.config.ts            = v3 config，仅 testMatch/outputDir 默认名 + 版本注释
#
# 纪律：每个补丁必须"恰好匹配一次"，否则整体退出非 0 且**不写任何文件**（fail-closed，无部分替换）。
import hashlib, os, sys

R = os.environ.get("R10V4_ROOT", "/home/euripides/projects/blockchain-platform")
JOBS = []


def add(path, expect_sha, patches, note):
    JOBS.append({"path": path, "expect": expect_sha, "patches": patches, "note": note})


TOGGLES_APPLY = '''  await dlg.locator("#export-format").selectOption(spec.format);
  // v4：把导出选项（include_meta / force_zip / include_decoded_input）**真的**通过 UI 勾选。
  // 这三个字段都会进入服务端 query_snapshot，因此打开其中任意一个都会产生**新的快照**，
  // 从而绕开 export_v2.go 的复用分支（复用要求快照逐字节相同）。
  const toggles = { includeMeta: false, forceZip: false, includeDecodedInput: false };
  const toggleSpec: Array<[keyof typeof toggles, string]> = [
    ["includeMeta", "include_meta"],
    ["forceZip", "force_zip"],
    ["includeDecodedInput", "include_decoded_input"],
  ];
  for (const [key, label] of toggleSpec) {
    if (spec.toggles?.[key] !== true) continue;
    const box = dlg.getByLabel(label);
    await expect(box, "导出选项 " + label + " 必须唯一存在").toHaveCount(1);
    if (!(await box.isChecked())) await box.check();
    expect(await box.isChecked(), label + " 必须真的被勾选").toBe(true);
    toggles[key] = true;
  }
  await dlg.getByRole("button", { name: "创建导出" }).click();'''

R1009 = '''
  test("R10-09 全新导出（不复用）完成认证：真实 UI 新建 -> 202 -> COMPLETED -> 文件落盘 -> SheetJS 读回", async ({ page }, testInfo) => {
    test.skip(!EMAIL || !PASSWORD || !ADDRESS, "R10_EMAIL/R10_PASSWORD/R10_ADDRESS not provided");
    test.setTimeout(600_000);
    const net: NetRecord[] = [];
    startCapture(page, net);
    await login(page, EMAIL, PASSWORD);
    await openExports(page);
    const headers = await authHeaders(page);
    const dir = artifactsDir(testInfo);

    // 为什么本用例结构上**不可能复用**（静态依据，不是"跑一次看看"）：
    //   backend/internal/api/export_v2.go 的复用分支写在 "if job != nil { ... }" 之内，
    //   而 job 只在请求携带 job_id 时才被加载（var job *model.DownloadJob; if req.JobID != "" {...}）。
    //   地址维度导出不携带 job_id => job == nil => 复用分支不可达 => 每轮必然真实新建一行 exports。
    // 另叠加 include_meta=true（R10-02 未使用该快照），使快照唯一性不依赖单一路径。
    const created = await createExportViaUi(page, net, {
      mode: "addresses", chain: CHAIN, address: ADDRESS, format: "xlsx", toggles: { includeMeta: true },
    });
    const exportId = String(created.id);
    expect(
      created.__http.status,
      "全新导出必须走新建路径（202）。复用路径（200，export_v2.go 命中 query_snapshot 完全一致的历史导出）" +
      "不得作为本用例证据；它的完成事件属于**当初那次**创建，不是本轮。",
    ).toBe(202);
    expect(String(created.format)).toBe("xlsx");
    expect(created.zip, "单地址单数据集不强制 ZIP").toBe(false);
    expect(created.__toggles.includeMeta, "include_meta 必须真的通过 UI 勾选（快照唯一性来源之一）").toBe(true);

    const detail = await waitExportTerminal(page, headers, exportId);
    expect(String(detail.id), "导出详情必须就是本次新建的 export_id").toBe(exportId);
    expect(String(detail.status)).toBe("COMPLETED");

    const { saved, ticket, ticketHttp } = await downloadViaUi(page, net, dir, "r10-09-fresh-xlsx", exportId);
    expect(saved.bytes.length, "下载文件必须非空").toBeGreaterThan(0);
    const fileSha = sha256(saved.bytes);
    expect(fileSha, "下载文件 sha256 必须等于服务端记录 sha256（同一份对象）").toBe(String(detail.sha256));
    expect(saved.bytes.subarray(0, 2).toString("latin1"), "裸 xlsx 必须是 ZIP/OOXML 魔数").toBe("PK");

    const readback = await readWorkbook(saved.bytes, "addresses/<addr>/transactions.xlsx");
    expect(readback.orderCheck.ok, "表头必须符合服务端 exportFieldOrder：" + readback.orderCheck.violations.join("; ")).toBe(true);
    expect(readback.dataRowCount, "真实数据行数必须等于服务端 rows").toBe(Number(detail.rows));
    // include_meta=true 的真实后果（不是文案）：服务端 annotateRows 会给每行加三列。
    expect(
      readback.header,
      "include_meta=true 必须在表头真实产生案件/标签列（证明该 UI 选项真的进了服务端快照）",
    ).toEqual(expect.arrayContaining(["matched_address", "case_names", "tag_names"]));
    const party = [readback.firstRow.from_address, readback.firstRow.to_address].map((v) => String(v ?? "").toLowerCase());
    expect(party, "首行必须包含导出地址本身").toContain(ADDRESS.toLowerCase());
    const txHash = String(readback.firstRow.tx_hash ?? "");
    expect(txHash, "首行必须有真实 tx_hash").toMatch(/^0x[0-9a-f]{64}$/);

    dump(testInfo, "r10-09-evidence.json", {
      job: {
        id: null, job_no: null, chain: CHAIN,
        note: "地址维度导出（无 job 绑定）：export_v2.go 的复用分支位于 if job != nil 之内，对地址维度结构上不可达。",
      },
      address: ADDRESS, dataset: DATASET, chain: CHAIN,
      export: detail,
      createHttp: created.__http,
      createToggles: created.__toggles,
      responseShape: created.__shape,
      responseKeys: created.__keys,
      successDialogText: created.__dialogText,
      ticket, ticketHttp,
      file: { path: saved.file, bytes: statSync(saved.file).size, sha256: fileSha, apiSha256: detail.sha256 },
      workbook: readback,
      cellProbe: { column: "tx_hash", value: txHash },
      network: net.filter((n) => n.url.includes("/api/v2/")),
    });
  });
});'''

add(R + "/frontend/tests/r10v3-export-excelize.spec.ts",
    "b7253c25e04d95c18e7abed5c2474b3402a12731cfaf400aabfd7535c0130ea9",
    [
      ("header-v4",
       " * 真实链路不做任何 mock：登录 -> 导出中心真实 UI -> POST /api/v2/exports（真实 Trace/Request ID）",
       """ * v4（本文件）：R10 v9 施工单（1420 交接 §5）。
 *   v3 实测（轮 r10-export-excelize-v8-20260913T060647Z）暴露两个**测量侧**缺陷：
 *     V8-1 R10-01 的任务维度导出命中了服务端复用分支，复用的是 02:49 产出的**修复前**导出
 *          （f62e4308，早于 05:55 审计修复部署），因此它的 export.completed 永远只存在于 legacy 表
 *          —— 结构上无法观测修复后的新完成事件（不是产品回归，但也绝不能记 PASS）。
 *     V8-2 crosscheck 的 SELECT seq::text ... ORDER BY seq 被 PostgreSQL 解析为按**输出列**（text）排序。
 *   本版只做"让证据可被观测"的测试侧更正，不放宽任何断言：
 *     F1 createExportViaUi 支持通过 UI 真实勾选 include_meta / force_zip / include_decoded_input
 *        （三者都进入服务端 query_snapshot，因此可产生新的快照 -> 不再复用旧导出）；
 *     F2 R10-01 打开 include_meta，使任务维度导出**每轮真实新建**（首轮 202）；
 *     F3 新增 R10-09：**结构上不可能复用**的地址维度全新导出认证用例（附 export_v2.go 的静态依据），
 *        并落地 Export/Job/Request/Trace/文件 全部证据，供 crosscheck 做同一次完成事件的硬断言。
 *
 * 真实链路不做任何 mock：登录 -> 导出中心真实 UI -> POST /api/v2/exports（真实 Trace/Request ID）"""),
      ("iface-v4",
       "  __keys: string[];\n};",
       "  __keys: string[];\n  __toggles: { includeMeta: boolean; forceZip: boolean; includeDecodedInput: boolean };\n};"),
      ("signature-v4",
       '  spec: { mode: "job" | "addresses"; jobId?: string; chain?: string; address?: string; format: string },\n): Promise<CreatedExport> {',
       '  spec: {\n    mode: "job" | "addresses"; jobId?: string; chain?: string; address?: string; format: string;\n    toggles?: { includeMeta?: boolean; forceZip?: boolean; includeDecodedInput?: boolean };\n  },\n): Promise<CreatedExport> {'),
      ("apply-toggles-v4",
       '  await dlg.locator("#export-format").selectOption(spec.format);\n  await dlg.getByRole("button", { name: "创建导出" }).click();',
       TOGGLES_APPLY),
      ("return-toggles-v4",
       "    __dialogText: dialogText,\n    __keys: Object.keys(data).sort(),\n  };\n}",
       "    __dialogText: dialogText,\n    __keys: Object.keys(data).sort(),\n    __toggles: toggles,\n  };\n}"),
      ("r1001-fresh-v4",
       '    const created = await createExportViaUi(page, net, { mode: "job", jobId: JOB_ID, format: "xlsx" });',
       """    // v4：任务维度导出必须**真实新建**。服务端复用要求 query_snapshot 逐字节相同；
    // 打开 include_meta 会改变快照（include_meta 在 snapshot 里），因此不会命中 02:49 那个修复前的旧导出。
    // 注意：本轮之后再次运行本 spec 时，桌面端会复用**本轮新建的**导出（它已有 v2 完成事件，
    // 仍可被硬断言证明），移动端的复用响应为 200 —— 因此这里不断言状态码，由证据+crosscheck 判定。
    const created = await createExportViaUi(page, net, { mode: "job", jobId: JOB_ID, format: "xlsx", toggles: { includeMeta: true } });"""),
      ("r1001-dump-v4",
       "      createHttp: created.__http,\n      successDialogText: created.__dialogText,\n      ticket, ticketHttp,\n      file: { path: saved.file, bytes: statSync(saved.file).size, sha256: fileSha, apiSha256: detail.sha256 },\n      zip: {",
       "      createHttp: created.__http,\n      createToggles: created.__toggles,\n      successDialogText: created.__dialogText,\n      ticket, ticketHttp,\n      file: { path: saved.file, bytes: statSync(saved.file).size, sha256: fileSha, apiSha256: detail.sha256 },\n      zip: {"),
      ("append-r1009",
       "    ).toEqual([]);\n  });\n});",
       "    ).toEqual([]);\n  });\n" + R1009),
    ],
    "v3 export spec + toggles + R10-09")

add(R + "/frontend/tests/r10v3-legacy-export-import.spec.ts",
    "29370531314d4585ed38a878ad0dddf6c836b2af5f60daa0e1bd00c8ea0d7251",
    [],
    "legacy spec 逐字节复制（内容零差异，只换文件名）")

add(R + "/frontend/playwright.r10v3.config.ts",
    "82f72d9705b13ef938e9afae7ca7c893627147de4ad2f9f1d08088110b84b1d0",
    [
      ("cfg-title",
       " * R10 v3 — 服务端 excelize XLSX 导出 + legacy 客户端导出/导入 + 列表契约 + 跨工作区作用域 专用配置。",
       " * R10 v4 — 服务端 excelize XLSX 导出 + legacy 客户端导出/导入 + 列表契约 + 跨工作区作用域 专用配置。"),
      ("cfg-note",
       " * v2（playwright.r10v2.config.ts）原样保留，不 hot-edit。",
       """ * v3（playwright.r10v3.config.ts）原样保留，不 hot-edit。v4 相对 v3 只有两处机械变更：
 *   testMatch r10v3-* -> r10v4-*；outputDir 未指定时的占位目录名同名替换。其余逐字节相同。"""),
      ("cfg-match",
       "  testMatch: /r10v3-.*\\.spec\\.ts/,",
       "  testMatch: /r10v4-.*\\.spec\\.ts/,"),
      ("cfg-outdir",
       '"test-results-r10v3-unspecified"',
       '"test-results-r10v4-unspecified"'),
    ],
    "v3 config -> v4 testMatch")

PLAN = {}
for job in JOBS:
    raw = open(job["path"], "rb").read()
    sha = hashlib.sha256(raw).hexdigest()
    print("src=%s sha256=%s" % (job["path"], sha))
    if sha != job["expect"]:
        print("FATAL baseline sha mismatch for %s (want %s)" % (job["path"], job["expect"]))
        sys.exit(3)
    text = raw.decode("utf-8")
    for tag, old, new in job["patches"]:
        n = text.count(old)
        if n != 1:
            print("FATAL patch %s@%s matched %d times (want 1)" % (tag, os.path.basename(job["path"]), n))
            sys.exit(4)
        text = text.replace(old, new, 1)
        print("  applied %s" % tag)
    PLAN[job["path"]] = (text, job["note"])

OUT = {
    R + "/frontend/tests/r10v3-export-excelize.spec.ts": R + "/frontend/tests/r10v4-export-excelize.spec.ts",
    R + "/frontend/tests/r10v3-legacy-export-import.spec.ts": R + "/frontend/tests/r10v4-legacy-export-import.spec.ts",
    R + "/frontend/playwright.r10v3.config.ts": R + "/frontend/playwright.r10v4.config.ts",
}
for src, dst in OUT.items():
    text, note = PLAN[src]
    if os.path.exists(dst):
        print("FATAL destination already exists (refusing to overwrite): %s" % dst)
        sys.exit(5)
    with open(dst, "w", encoding="utf-8", newline="\n") as fh:
        fh.write(text)
    out = open(dst, "rb").read()
    print("dst=%s note=%s bytes=%d sha256=%s" % (dst, note, len(out), hashlib.sha256(out).hexdigest()))
print("OK")
