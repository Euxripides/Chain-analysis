/// <reference lib="webworker" />
import { ImportFormatRejected, parseAddressFile } from "../import/parse-core";

/**
 * 隔离解析 Worker（计划 1852）：所有 CSV/XLSX 解析与格式防线都在这里执行。
 *
 * 契约：
 *   入站 { id, file, defaultChain }
 *   出站 { id, ok: true, rows, summary }
 *        { id, ok: false, code, reason }  code ∈ FORMAT_REJECTED | PARSE_ERROR
 *
 * 主线程**不依赖** Worker 自觉结束：超时/取消由主线程直接 terminate()（见 parse-client.ts），
 * 因此这里不需要也无法实现"协同取消"——这正是"有界"的含义。
 */

interface ParseRequest {
  id: number;
  file: File;
  defaultChain: string;
}

const ctx = self as unknown as DedicatedWorkerGlobalScope;

ctx.onmessage = async (event: MessageEvent<ParseRequest>) => {
  const { id, file, defaultChain } = event.data;
  const startedAt = Date.now();
  try {
    const parsed = await parseAddressFile(file, defaultChain);
    ctx.postMessage({
      id,
      ok: true,
      rows: parsed.rows,
      summary: parsed.summary,
      metrics: { workerElapsedMs: Date.now() - startedAt, fileBytes: file.size },
    });
  } catch (error) {
    if (error instanceof ImportFormatRejected) {
      ctx.postMessage({ id, ok: false, code: "FORMAT_REJECTED", reason: error.message });
      return;
    }
    ctx.postMessage({
      id,
      ok: false,
      code: "PARSE_ERROR",
      reason: error instanceof Error ? error.message : String(error),
    });
  }
};
