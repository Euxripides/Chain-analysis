import type { ImportedAddress, LocalParseSummary } from "./parse-core";

/**
 * 导入解析的 Worker 客户端：**有界超时 + 可取消 + 解析失败可隔离**。
 *
 * 设计要点（对应计划 §23.7 / frontend-design plan 1852 的"隔离解析"要求）：
 *   1. 解析在专用 Worker 中执行，主线程只等消息 —— UI 在解析期间保持可交互；
 *   2. 超时与取消都由主线程 **terminate()** 实现：CPU 密集的同步解析无法协作取消，
 *      只有终止线程才能真正停止（否则"取消"是假的）；
 *   3. Worker 被终止后不复用：下次解析新建一个，保证不会把上一次的残留状态带进来；
 *   4. 三种失败以带 code 的错误区分，UI 才能给出不同（且不误导）的弹窗：
 *      PARSE_TIMEOUT / PARSE_CANCELLED / FORMAT_REJECTED / PARSE_ERROR。
 */

/** 默认有界超时：超过即终止 Worker 并报 PARSE_TIMEOUT（不做无限等待）。 */
export const IMPORT_PARSE_DEFAULT_TIMEOUT_MS = 15_000;

export type ImportParseFailureCode = "PARSE_TIMEOUT" | "PARSE_CANCELLED" | "FORMAT_REJECTED" | "PARSE_ERROR";

export class ImportParseError extends Error {
  readonly code: ImportParseFailureCode;

  constructor(code: ImportParseFailureCode, reason: string) {
    super(reason);
    this.name = "ImportParseError";
    this.code = code;
  }
}

export interface ImportParseMetrics {
  workerElapsedMs: number;
  fileBytes: number;
  wallElapsedMs: number;
  timeoutMs: number;
  /** 该次解析是否落在 Worker 中（恒为 true；用于证据与断言，不做假分支）。 */
  isolatedInWorker: true;
}

export interface ImportParseResult {
  rows: ImportedAddress[];
  summary: LocalParseSummary;
  metrics: ImportParseMetrics;
}

/**
 * 超时取自 window.__IMPORT_PARSE_TIMEOUT_MS__（正数时生效），否则用默认值。
 * 这是**测试用钩子**：真实环境不设置该全局变量，行为即默认 15s。
 */
export function importParseTimeoutMs(): number {
  const raw = (globalThis as { __IMPORT_PARSE_TIMEOUT_MS__?: unknown }).__IMPORT_PARSE_TIMEOUT_MS__;
  const value = typeof raw === "number" ? raw : Number(raw);
  return Number.isFinite(value) && value > 0 ? value : IMPORT_PARSE_DEFAULT_TIMEOUT_MS;
}

/**
 * 超时预算的**人类可读**文案。
 * U5 产品缺陷：预算小于 1 秒时 `Math.round(ms / 1000)` 会渲染成"0 秒"，对用户毫无意义。
 * 规则：< 1s 用毫秒；>= 1s 用秒（整数不带小数，非整数保留 1 位）；非法值回退"未知时长"。
 */
export function formatParseBudget(ms: number): string {
  if (!Number.isFinite(ms) || ms <= 0) return "未知时长";
  if (ms < 1000) return `${Math.round(ms)} 毫秒`;
  const seconds = ms / 1000;
  return Number.isInteger(seconds) ? `${seconds} 秒` : `${seconds.toFixed(1)} 秒`;
}

let sharedWorker: Worker | null = null;
let sequence = 0;

function spawnWorker(): Worker {
  if (sharedWorker) return sharedWorker;
  sharedWorker = new Worker(new URL("../workers/import-parse.worker.ts", import.meta.url), {
    type: "module",
    name: "import-parse",
  });
  return sharedWorker;
}

/** 终止并丢弃当前 Worker（超时/取消/错误后必须调用，避免线程继续跑 CPU）。 */
function killWorker(): void {
  if (!sharedWorker) return;
  sharedWorker.terminate();
  sharedWorker = null;
}

export interface ParseOptions {
  signal?: AbortSignal;
  timeoutMs?: number;
}

export async function parseAddressFileInWorker(
  file: File,
  defaultChain: string,
  options: ParseOptions = {},
): Promise<ImportParseResult> {
  const timeoutMs = options.timeoutMs && options.timeoutMs > 0 ? options.timeoutMs : importParseTimeoutMs();
  const signal = options.signal;
  const startedAt = performance.now();
  const id = ++sequence;
  const worker = spawnWorker();

  return new Promise<ImportParseResult>((resolve, reject) => {
    let settled = false;
    let timer: ReturnType<typeof setTimeout> | null = null;

    const cleanup = () => {
      if (timer !== null) clearTimeout(timer);
      worker.removeEventListener("message", onMessage);
      worker.removeEventListener("error", onError);
      signal?.removeEventListener("abort", onAbort);
    };
    const settle = (run: () => void) => {
      if (settled) return;
      settled = true;
      cleanup();
      run();
    };

    const onMessage = (event: MessageEvent) => {
      const data = event.data as
        | { id: number; ok: true; rows: ImportedAddress[]; summary: LocalParseSummary; metrics?: { workerElapsedMs?: number } }
        | { id: number; ok: false; code?: ImportParseFailureCode; reason?: string }
        | undefined;
      if (!data || data.id !== id) return;
      if (data.ok) {
        settle(() =>
          resolve({
            rows: data.rows,
            summary: data.summary,
            metrics: {
              workerElapsedMs: Number(data.metrics?.workerElapsedMs ?? 0),
              fileBytes: file.size,
              wallElapsedMs: Math.round(performance.now() - startedAt),
              timeoutMs,
              isolatedInWorker: true,
            },
          }),
        );
        return;
      }
      settle(() => reject(new ImportParseError(data.code ?? "PARSE_ERROR", data.reason ?? "解析失败")));
    };

    const onError = (event: ErrorEvent) => {
      killWorker();
      settle(() => reject(new ImportParseError("PARSE_ERROR", event.message || "解析 Worker 异常终止")));
    };

    const onAbort = () => {
      // 真正的取消：终止线程（CPU 密集解析无法协作取消）。
      killWorker();
      settle(() => reject(new ImportParseError("PARSE_CANCELLED", "解析已取消")));
    };

    timer = setTimeout(() => {
      killWorker();
      settle(() => reject(new ImportParseError("PARSE_TIMEOUT", `解析超过 ${timeoutMs} ms 未完成，已终止并隔离`)));
    }, timeoutMs);

    worker.addEventListener("message", onMessage);
    worker.addEventListener("error", onError);
    if (signal) {
      if (signal.aborted) {
        onAbort();
        return;
      }
      signal.addEventListener("abort", onAbort);
    }
    try {
      worker.postMessage({ id, file, defaultChain });
    } catch (error) {
      killWorker();
      settle(() => reject(new ImportParseError("PARSE_ERROR", error instanceof Error ? error.message : String(error))));
    }
  });
}
