import type { ApiClient } from "@chain/api-client";
import type { DownloadMetadataOption } from "./DownloadMetadataPanel";

/**
 * 案件/标签目录仍由已上线的 v1 元数据接口提供（v2 没有 /tags）。
 * v1 返回原始 JSON 而不是 v2 envelope，因此不能交给 ApiClient.get/post 的解包器。
 * 请求仍携带同源会话 Cookie、受众头与写操作 CSRF，并由服务端做工作区作用域/RBAC 校验。
 */
async function metadataRequest<T>(
  client: ApiClient,
  path: string,
  method: "GET" | "POST",
  body?: Record<string, string>,
  signal?: AbortSignal,
): Promise<T> {
  const headers: Record<string, string> = {
    Accept: "application/json",
    "X-Auth-Audience": "user",
  };
  if (method === "POST") {
    headers["Content-Type"] = "application/json; charset=utf-8";
    const csrf = client.csrfToken();
    if (csrf) headers["X-CSRF-Token"] = csrf;
  }
  const response = await fetch(path, {
    method,
    headers,
    credentials: "same-origin",
    body: body ? JSON.stringify(body) : undefined,
    signal,
  });
  if (!response.ok) {
    if (response.status === 401) throw new Error("登录状态已失效，请重新登录");
    if (response.status === 403) throw new Error("当前账号无权读取或创建案件/标签");
    if (response.status === 409) throw new Error("同名案件或标签已存在，请刷新列表后选择");
    throw new Error(response.status >= 500 ? "案件/标签服务暂时不可用" : `案件/标签请求失败（${response.status}）`);
  }
  return response.json() as Promise<T>;
}

function isMetadataOption(value: unknown): value is DownloadMetadataOption {
  if (!value || typeof value !== "object") return false;
  const item = value as Record<string, unknown>;
  return typeof item.id === "string" && item.id !== "" && typeof item.name === "string" && item.name !== "";
}

function optionsFrom(raw: unknown, key: "cases" | "tags"): DownloadMetadataOption[] {
  if (!raw || typeof raw !== "object") throw new Error("案件/标签服务返回了无效数据");
  const rows = (raw as Record<string, unknown>)[key];
  if (!Array.isArray(rows)) throw new Error("案件/标签服务返回了无效列表");
  if (!rows.every(isMetadataOption)) throw new Error("案件/标签列表包含无效条目");
  return rows;
}

export async function listDownloadCases(client: ApiClient, signal?: AbortSignal): Promise<DownloadMetadataOption[]> {
  const raw = await metadataRequest<unknown>(client, "/api/v1/cases", "GET", undefined, signal);
  return optionsFrom(raw, "cases");
}

export async function listDownloadTags(client: ApiClient, signal?: AbortSignal): Promise<DownloadMetadataOption[]> {
  const raw = await metadataRequest<unknown>(client, "/api/v1/tags", "GET", undefined, signal);
  return optionsFrom(raw, "tags");
}

export async function createDownloadCase(client: ApiClient, name: string): Promise<DownloadMetadataOption> {
  const row = await metadataRequest<unknown>(client, "/api/v1/cases", "POST", { name });
  if (!isMetadataOption(row)) throw new Error("创建案件后服务返回了无效数据");
  return row;
}

export async function createDownloadTag(client: ApiClient, name: string): Promise<DownloadMetadataOption> {
  const row = await metadataRequest<unknown>(client, "/api/v1/tags", "POST", { name });
  if (!isMetadataOption(row)) throw new Error("创建标签后服务返回了无效数据");
  return row;
}
