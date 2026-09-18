import { mkdirSync, writeFileSync } from "node:fs";
import path from "node:path";
import { crc32, inflateRawSync } from "node:zlib";
import { expect, type Download, type Page, type Response } from "@playwright/test";

/**
 * R10 共用工具：真实浏览器抓包 + 真实 ZIP 解析 + 证据落盘。
 *
 * 只做两件事：
 *   1) 记录浏览器真实发出的 /api/ 请求/响应（含 X-Request-Id / X-Trace-Id），不做任何 mock；
 *   2) 用 Node 内建 zlib 解析服务端真实产出的 ZIP（中央目录 + CRC32 + inflateRaw），
 *      把每个条目的真实字节交给 SheetJS 读回。
 * 任何失败都抛出，不吞异常，不用占位值。
 */

export const OUT = process.env.R10_OUT ?? "";
export const LOGIN_PATH = process.env.R10_LOGIN_PATH ?? "/app-v2/login";

export type NetRecord = {
  method: string;
  url: string;
  status: number;
  requestId: string | null;
  traceId: string | null;
  requestBody?: string | null;
  responseBody?: string | null;
};

export function startCapture(page: Page, sink: NetRecord[]) {
  page.on("response", (response: Response) => {
    void (async () => {
      const url = response.url();
      if (!url.includes("/api/")) return;
      const request = response.request();
      let requestBody: string | null = null;
      try {
        requestBody = request.postData();
      } catch {
        requestBody = null;
      }
      let responseBody: string | null = null;
      try {
        responseBody = (await response.text()).slice(0, 20000);
      } catch {
        responseBody = null;
      }
      sink.push({
        method: request.method(),
        url,
        status: response.status(),
        requestId: response.headers()["x-request-id"] ?? null,
        traceId: response.headers()["x-trace-id"] ?? null,
        requestBody,
        responseBody,
      });
    })();
  });
}

/** V2 接口是 envelope：真实业务字段在 body.data 下。 */
export function unwrap(body: string | null | undefined): Record<string, unknown> {
  try {
    const parsed = JSON.parse(body ?? "{}");
    if (parsed && typeof parsed === "object" && "data" in parsed) {
      return ((parsed as { data: Record<string, unknown> }).data ?? {}) as Record<string, unknown>;
    }
    return (parsed ?? {}) as Record<string, unknown>;
  } catch {
    return {};
  }
}

export function dump(testInfo: { project: { name: string } }, name: string, value: unknown) {
  if (!OUT) return;
  const dir = path.join(OUT, testInfo.project.name);
  mkdirSync(dir, { recursive: true });
  writeFileSync(path.join(dir, name), JSON.stringify(value, null, 2) + "\n", "utf8");
}

export function artifactsDir(testInfo: { project: { name: string } }): string {
  const dir = OUT ? path.join(OUT, testInfo.project.name, "files") : path.join("test-results-r10-files", testInfo.project.name);
  mkdirSync(dir, { recursive: true });
  return dir;
}

/** 与服务端真实约定一致：状态变更请求必须带 CSRF + 受众头（packages/api-client/client.ts）。 */
export async function authHeaders(page: Page, audience = "user"): Promise<Record<string, string>> {
  const cookies = await page.context().cookies();
  const csrf = cookies.find((c) => c.name === `cs_${audience}_session_csrf`)?.value ?? "";
  return { "Content-Type": "application/json", "X-CSRF-Token": csrf, "X-Auth-Audience": audience };
}

export async function login(page: Page, email: string, password: string) {
  await page.goto(LOGIN_PATH, { waitUntil: "domcontentloaded" });
  await expect(page.locator("#identifier"), "login identifier").toBeVisible({ timeout: 30_000 });
  await page.locator("#identifier").fill(email);
  await page.locator("#password").fill(password);
  await page.getByRole("button", { name: /^登录(后台)?$/ }).click();
  await expect.poll(() => page.url(), { timeout: 30_000 }).not.toContain(LOGIN_PATH);
}

export const dialog = (page: Page) => page.locator('[role="dialog"]');

/** 等待浏览器真实下载并落盘，返回路径与字节。 */
export async function saveDownload(page: Page, dir: string, prefix: string, trigger: () => Promise<void>): Promise<{ file: string; bytes: Buffer }> {
  const wait = page.waitForEvent("download", { timeout: 120_000 });
  await trigger();
  const download: Download = await wait;
  const suggested = download.suggestedFilename() || `${prefix}.bin`;
  const file = path.join(dir, `${prefix}__${suggested.replace(/[^A-Za-z0-9._-]/g, "_")}`);
  await download.saveAs(file);
  await download.delete().catch(() => undefined);
  const { readFileSync } = await import("node:fs");
  return { file, bytes: readFileSync(file) };
}

// ---------------------------------------------------------------------------
// 真实 ZIP 结构解析（不依赖任何第三方 unzip 实现；失败即抛）
// ---------------------------------------------------------------------------

export type ZipEntry = {
  name: string;
  method: number;
  flags: number;
  crc32: number;
  compressed: number;
  uncompressed: number;
  localOffset: number;
};

export function readZipCentralDirectory(buf: Buffer): ZipEntry[] {
  const EOCD = 0x06054b50;
  const CDH = 0x02014b50;
  const maxScan = Math.min(buf.length, 22 + 65535);
  let eocd = -1;
  for (let i = buf.length - 22; i >= buf.length - maxScan; i--) {
    if (i < 0) break;
    if (buf.readUInt32LE(i) === EOCD) {
      eocd = i;
      break;
    }
  }
  if (eocd < 0) throw new Error("ZIP EOCD not found");
  const count = buf.readUInt16LE(eocd + 10);
  const cdSize = buf.readUInt32LE(eocd + 12);
  const cdOffset = buf.readUInt32LE(eocd + 16);
  if (cdOffset + cdSize > buf.length) throw new Error("ZIP central directory out of range");
  const entries: ZipEntry[] = [];
  let p = cdOffset;
  for (let i = 0; i < count; i++) {
    if (buf.readUInt32LE(p) !== CDH) throw new Error(`bad CDH signature at ${p}`);
    const flags = buf.readUInt16LE(p + 8);
    const method = buf.readUInt16LE(p + 10);
    const crc = buf.readUInt32LE(p + 16);
    const compressed = buf.readUInt32LE(p + 20);
    const uncompressed = buf.readUInt32LE(p + 24);
    const nameLen = buf.readUInt16LE(p + 28);
    const extraLen = buf.readUInt16LE(p + 30);
    const commentLen = buf.readUInt16LE(p + 32);
    const localOffset = buf.readUInt32LE(p + 42);
    const name = buf.subarray(p + 46, p + 46 + nameLen).toString("utf8");
    entries.push({ name, method, flags, crc32: crc, compressed, uncompressed, localOffset });
    p += 46 + nameLen + extraLen + commentLen;
  }
  if (p !== cdOffset + cdSize) throw new Error("ZIP central directory not fully consumed");
  return entries;
}

/** 读出条目真实字节，并用声明的 CRC32 校验（不一致即抛）。 */
export function readZipEntry(buf: Buffer, entry: ZipEntry): Buffer {
  const LH = 0x04034b50;
  if (buf.readUInt32LE(entry.localOffset) !== LH) throw new Error(`bad local header for ${entry.name}`);
  const nameLen = buf.readUInt16LE(entry.localOffset + 26);
  const extraLen = buf.readUInt16LE(entry.localOffset + 28);
  const dataOffset = entry.localOffset + 30 + nameLen + extraLen;
  const raw = buf.subarray(dataOffset, dataOffset + entry.compressed);
  if (raw.length !== entry.compressed) throw new Error(`truncated entry data for ${entry.name}`);
  const data = entry.method === 0 ? Buffer.from(raw) : inflateRawSync(raw);
  if (data.length !== entry.uncompressed) {
    throw new Error(`${entry.name}: inflated ${data.length} != declared ${entry.uncompressed}`);
  }
  const actualCrc = crc32(data) >>> 0;
  if (actualCrc !== entry.crc32 >>> 0) {
    throw new Error(`${entry.name}: crc32 mismatch ${actualCrc} != ${entry.crc32}`);
  }
  return data;
}
