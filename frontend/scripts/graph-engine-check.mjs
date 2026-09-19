#!/usr/bin/env node
/**
 * 图谱引擎验收（用户端重构规范 V4 §30 / §63C / §63G）
 *
 * 它证明的不是"看起来画出来了"，而是三件可断言的事：
 *   1. WebGL2 上下文可用，shader 编译链接通过，图数据成功上传；
 *   2. **每个节点的中心像素都能拾取到自己** —— 序列化为"几何被正确光栅化到正确位置"；
 *   3. 相机缩放后坐标映射同步变化，且 dispose 幂等。
 *
 * 做法：用 esbuild 把 __verify__/probe.ts 打成一个 ES chunk，配一个最小 HTML 壳，
 * 在真实 Chromium 里加载并读回 window.__graphProbe。
 */
import { chromium } from "@playwright/test";
import { createServer } from "node:http";
import { mkdtemp, writeFile, readFile, rm } from "node:fs/promises";
import { tmpdir } from "node:os";
import { join, resolve } from "node:path";
import { fileURLToPath } from "node:url";

const here = resolve(fileURLToPath(new URL(".", import.meta.url)), "..");
const PORT = Number(process.env.GRAPH_CHECK_PORT ?? 5455);

/*
 * 用 Vite 的构建 API 打包探针，而不是直接调 esbuild：
 * esbuild 在 pnpm 的严格 node_modules 下只是 Vite 的传递依赖，
 * 顶层 import 不到（ERR_MODULE_NOT_FOUND）；vite 本身是顶层 devDependency。
 */
async function bundle(outDir) {
  const { build } = await import("vite");
  await build({
    configFile: false,
    root: outDir,
    logLevel: "silent",
    /*
     * configFile:false 意味着 Vite 不会注入它平时注入的 define，
     * 而 React 的开发/生产分支读取 `process.env.NODE_ENV` —— 不替换就会在浏览器里
     * 直接 ReferenceError: process is not defined。这里显式补上。
     */
    define: { "process.env.NODE_ENV": JSON.stringify("production") },
    /* 探针从包里 import，所以构建时必须知道 workspace 别名（与 app 的 vite.config 一致）。 */
    resolve: {
      alias: {
        "@chain/graph-engine": join(here, "packages/graph-engine/index.ts"),
        "@chain/graph-react": join(here, "packages/graph-react/index.ts"),
      },
    },
    build: {
      lib: { entry: join(here, "packages/graph-engine/__verify__/probe.ts"), formats: ["es"], fileName: () => "probe.js" },
      outDir,
      emptyOutDir: false,
      minify: false,
      target: "es2022",
      sourcemap: false,
    },
  });
}

async function main() {
  const outDir = await mkdtemp(join(tmpdir(), "graph-engine-check-"));
  await bundle(outDir);
  await writeFile(
    join(outDir, "index.html"),
    `<!doctype html><html><head><meta charset="utf-8"><title>graph-engine probe</title>
<style>html,body{margin:0;background:#F4F8FF}#stage{position:relative}#shapes{position:relative}</style></head>
<body><div id="stage"></div><div id="shapes"></div><div id="lod"></div><script type="module" src="./probe.js"></script></body></html>`,
    "utf8",
  );

  const server = createServer(async (req, res) => {
    const name = (req.url ?? "/").split("?")[0];
    const file = name === "/" ? "index.html" : name.replace(/^\//, "");
    try {
      if (file === "favicon.ico") {
        res.writeHead(204).end();
        return;
      }
      const body = await readFile(join(outDir, file));
      const type = file.endsWith(".js") ? "text/javascript" : "text/html";
      res.writeHead(200, { "content-type": type + "; charset=utf-8" });
      res.end(body);
    } catch {
      res.writeHead(404).end("not found");
    }
  });
  await new Promise((ok) => server.listen(PORT, "127.0.0.1", ok));

  const browser = await chromium.launch({
    channel: process.env.E2E_BROWSER_CHANNEL ?? "msedge",
    /* 无头环境靠 SwiftShader 提供 WebGL2；这正是"中端 GPU"之外的兜底路径。 */
    args: ["--use-gl=angle", "--use-angle=swiftshader", "--enable-unsafe-swiftshader"],
  });

  const problems = [];
  const notes = [];
  try {
    const page = await browser.newPage({ viewport: { width: 800, height: 600 } });
    const consoleErrors = [];
    const diagnostics = [];
    page.on("console", (msg) => {
      diagnostics.push(msg.type() + ": " + msg.text().slice(0, 300));
      /* favicon 的 404 与被测代码无关，不纳入失败线。 */
      if (msg.type() === "error" && !/favicon/i.test(msg.text())) consoleErrors.push(msg.text());
    });
    page.on("pageerror", (err) => {
      diagnostics.push("pageerror: " + String(err).slice(0, 400));
      consoleErrors.push(String(err));
    });
    page.on("requestfailed", (req) => diagnostics.push("requestfailed: " + req.url().slice(0, 160)));

    await page.goto(`http://127.0.0.1:${PORT}/`, { waitUntil: "load" });
    await page
      .waitForFunction(() => Boolean(window.__graphProbe), null, { timeout: 30_000 })
      .catch(() => {
        console.error("PROBE_NEVER_REPORTED");
        for (const line of diagnostics.slice(0, 20)) console.error("  " + line);
        process.exit(4);
      });
    const report = await page.evaluate(() => window.__graphProbe);

    notes.push(`webgl2=${report.webgl2} nodes=${report.nodes} edges=${report.edges} storeBytes=${report.bytes}`);
    if (report.error) problems.push("引擎探针抛出异常：" + report.error);
    if (!report.webgl2) problems.push("WebGL2 上下文不可用（降级路径应提示性能受限模式）");
    if (report.nodes !== 12 || report.edges !== 12) {
      problems.push(`图数据装载不正确：${report.nodes} 节点 / ${report.edges} 边，期望 12 / 12`);
    }
    if (report.hits !== 12) {
      problems.push(`初始视图只有 ${report.hits}/12 个节点的中心能拾取到自己：${JSON.stringify(report.misses)}`);
    }
    if (report.emptyHit !== -1) {
      problems.push(`空白处拾取到了节点 ${report.emptyHit}（应为 -1）`);
    }
    if (report.afterZoomHits !== 12) {
      problems.push(`缩放后只有 ${report.afterZoomHits}/12 命中：${JSON.stringify(report.afterZoomMisses)}`);
    }
    if (report.zoomMovedScreenPoint < 1) {
      problems.push(`缩放没有改变屏幕映射（位移 ${report.zoomMovedScreenPoint}px）——相机矩阵可能没生效`);
    }
    if (report.offCanvasHit !== -1) {
      problems.push(`画布外坐标拾取返回 ${report.offCanvasHit}（应为 -1，不能随机命中）`);
    }
    if (report.selectionEvents < 1) problems.push("setSelection 没有发出 selection:change 事件");
    if (report.exportedPngBytes < 2000) problems.push(`exportView 返回值不像一张 PNG（${report.exportedPngBytes} 字节）`);
    if (!report.disposed) problems.push("dispose 未完成");

    /* React 桥与可访问列表（§30.3 / §55.2）。 */
    if (!report.reactCanvas) problems.push("FundGraph 没有渲染出 canvas");
    if (!report.reactReady) problems.push("FundGraph 没有把引擎就绪上报给 React");
    if (report.a11yListedNeighbors === 0) {
      problems.push("选中节点后，可访问对象列表没有列出任何邻接（§55.2 要求 canvas 不是唯一信息通道）");
    }
    if (!report.a11yHasSelectedLabel) problems.push("可访问列表没有反映当前选中项");
    if (!report.keyboardMovedSelection) problems.push("方向键没有在邻接之间移动选中");
    if (!report.reactDisposedOnUnmount) problems.push("FundGraph 卸载后没有 dispose 引擎（§63C 生命周期）");

    /* 布局（§30.14）与 worker 处理器（§30.5）。 */
    if (!report.layoutFinite) problems.push("布局产生了非有限坐标（NaN/Infinity 会让节点在着色器里被整体丢弃且不报错）");
    if (!report.layoutDeterministic) problems.push("同一 seed 两次力导向结果不一致（§63C 要求可复现）");
    if (!report.columnsMonotonic) problems.push("columns 布局没有按 BFS 层号单调分层");
    if (!report.ringRadiusMonotonic) problems.push("ring 布局的半径与层号不匹配");
    if (report.forceSeparated !== 0) problems.push(`力导向后有 ${report.forceSeparated} 对节点仍然重合（斥力没生效）`);
    if (!report.warmStartChangedResult) problems.push("warmStart 语义失效：热启动与冷启动结果相同，说明起点没有被沿用");
    if (!report.handlerOk) problems.push("handleLayoutRequest 没有返回可用结果");
    if (report.handlerResolvedMode === "") problems.push("worker 处理器没有回报解析后的布局模式");

    /* 路径与播放（§30.12）。 */
    if (report.pathNodes !== 4) problems.push(`路径节点数 ${report.pathNodes} ≠ 4（A→B→C→D）`);
    if (!report.pathPrefersCheapEdge) {
      problems.push(`加权最短路没有选更省的链路（cost=${report.pathCostUnweighted}）——权重可能被忽略、退化成了 BFS`);
    }
    if (!report.pathUndirected) problems.push("有向/无向语义不正确：反向在有向模式下应无路，无向模式下应有路");
    if (report.playbackHopsAtTick !== 1) problems.push(`播放推进不正确：t=310ms 时应为第 1 跳，实际 ${report.playbackHopsAtTick}`);
    if (!report.playbackCompleted) problems.push("播放没有走到 completed 并发出 path:complete");
    if (!report.playbackSpeed2xFaster) problems.push("2x 速度没有让播放更快");
    if (!report.playbackManualStep) problems.push("nextHop / previousHop 手动步进不正确");
    /*
     * 必须拿**引擎上那条路径**的边数来比。
     * 第一版拿的是 chain 夹具的 pathNodes-1（4-1=3），而高亮来自 12 环上的 n0→n4（4 条边），
     * 两个数字来自两条毫不相干的路径——这种"拿错参照物"的断言比不写还糟。
     */
    if (report.litEdgesAfterHighlight !== report.enginePathEdges) {
      problems.push(`高亮边数 ${report.litEdgesAfterHighlight} ≠ 引擎路径边数 ${report.enginePathEdges}`);
    }
    if (report.particleCount <= 0) problems.push("高亮路径上没有生成任何粒子");

    /* 节点形状（§30.9）：形状必须真的不同，且拾取热区与所见形状一致。 */
    if (report.shapeChecks === "webgl2-unavailable") {
      notes.push("形状检查跳过：WebGL2 不可用");
    } else if (!report.shapesDistinct) {
      problems.push(`节点形状未能区分，或拾取热区与形状不一致：${report.shapeChecks}`);
    } else {
      notes.push(`形状检查：${report.shapeChecks}`);
    }

    /* 细节层次与渲染预算（§30.8 / §30.15）。 */
    if (!report.budgetFull || !report.budgetAggregate || !report.budgetLimit) {
      problems.push(`预算档位判定不正确：full=${report.budgetFull} aggregate=${report.budgetAggregate} limit=${report.budgetLimit}`);
    }
    if (!report.lodThresholds) problems.push("LOD 阈值判定不正确（远景/中景/近景）");
    if (report.lodChecks === "webgl2-unavailable") {
      notes.push("LOD 检查跳过：WebGL2 不可用");
    } else {
      if (report.lodFarLevel !== "far") problems.push(`远景缩放下的 LOD 是 ${report.lodFarLevel}，期望 far`);
      if (report.lodNearLevel !== "near") problems.push(`近景缩放下的 LOD 是 ${report.lodNearLevel}，期望 near`);
      if (report.renderedEdgesFar !== 20_000) {
        problems.push(`远景应把边降到 20000 条，实际 ${report.renderedEdgesFar}`);
      }
      if (report.renderedEdgesNear !== 20_500) {
        problems.push(`近景应画出全部 20500 条边，实际 ${report.renderedEdgesNear}`);
      }
      if (!report.farKeepsHeaviest) problems.push("远景降采样没有保留权重最高的边（截断成了任意前 N 条）");
      notes.push(
        `LOD：far=${report.renderedEdgesFar} 边 · near=${report.renderedEdgesNear} 边 · 预算档位=${report.budgetModeForProbe} · 保留主干=${report.farKeepsHeaviest}`,
      );
    }
    /* 高亮边的实际颜色（§30.11）。 */
    if (report.highlightChecks === "webgl2-unavailable") {
      notes.push("高亮边颜色检查跳过：WebGL2 不可用");
    } else if (report.highlightChecks === "no-sample-in-view") {
      problems.push("高亮边颜色检查没有采到任何样点（夹具或相机设置有误，判据形同虚设）");
    } else if (!report.highlightPixelMatch) {
      problems.push("高亮路径的边没有画出 edgeActiveColor（" + report.highlightChecks + "）：" + report.highlightPixelRgb);
    } else {
      notes.push("高亮边颜色：" + report.highlightPixelsLit + "/" + report.highlightSamples + " 个边中点匹配 edgeActiveColor");
    }
    /* 标签（§30.8 / G2「基础 label」）。 */
    if (report.labelChecks === "webgl2-unavailable") {
      notes.push("标签检查跳过：WebGL2 不可用");
    } else {
      /*
       * 这一组判据的核心是 labelPixels*——它们是从 exportView 的 PNG 上
       * **逐像素数出来的**。"实例数正确但文字没画出来"（着色器写错、图集传错）
       * 只有像素判据能抓到，实例数会照常通过。
       */
      if (report.labelsNear <= 0) {
        problems.push("近景下没有任何标签实例（§30.8 近景应显示地址短值 / entity tag）");
      }
      if (report.labelPixelsNear <= 0) {
        problems.push("近景导出图里，主标签的屏幕矩形内一个非背景像素都没有——标签没有真正被 GPU 画出来");
      }
      if (!report.labelAnchoredRightOfNode) {
        problems.push("标签锚点没有落在节点右侧（左对齐文本块的排版约束）");
      }
      if (report.labelPixelsBackground !== 0) {
        problems.push("对照区域（确定无内容）出现 " + report.labelPixelsBackground + " 个非背景像素，像素判据本身不可信");
      }
      if (report.labelPixelsFar !== 0) {
        problems.push("远景下同一块位置仍有 " + report.labelPixelsFar + " 个标签像素（§30.8 远景不显示 label）");
      }
      if (report.labelsFar !== 0) problems.push("远景下标签实例数应为 0，实际 " + report.labelsFar);
      if (report.labelsFarPinned <= 0) {
        problems.push("选中节点在远景下也丢了标签——固定显示的标签不应受档位预算约束");
      }
      if (report.labelsMid <= 0 || report.labelsMid > report.labelsNear) {
        problems.push("中景标签数 " + report.labelsMid + " 不合理（应 > 0 且 <= 近景 " + report.labelsNear + "）");
      }
      if (report.labelDropped !== 0) {
        problems.push("图集放不下 " + report.labelDropped + " 条文字——标签会静默消失，需要调大图集");
      }
      notes.push(
        "标签：近景 " + report.labelsNear + " 条 / " + report.labelPixelsNear + " 像素 · 副标签 " +
          report.labelPixelsSublabel + " 像素 · 中景 " + report.labelsMid + " 条 · 远景 " + report.labelsFar +
          " 条（选中时 " + report.labelsFarPinned + " 条） · 图集 " + report.labelGlyphs + " 字形",
      );
    }
    if (consoleErrors.length > 0) problems.push("console/pageerror：" + JSON.stringify(consoleErrors.slice(0, 3)));

    const result = { ok: problems.length === 0, problems, notes, report };
    console.log(JSON.stringify(result, null, 2));
    if (problems.length > 0) process.exitCode = 1;
  } finally {
    await browser.close();
    server.close();
    await rm(outDir, { recursive: true, force: true });
  }
}

main().catch((error) => {
  console.error(error);
  process.exit(3);
});
