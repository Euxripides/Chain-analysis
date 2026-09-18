import type {
  AnkrProductionProfile,
  CoveragePreview,
  ExportRow,
  Job,
  JobDetail,
  PerformanceMode,
  PerformanceModeState,
  Snapshot,
  VolumeGuard,
} from "../types";

export type TokenMetadata = {
  chain: string;
  contract_address: string;
  name?: string;
  symbol?: string;
  decimals: number | null;
  token_type?: string;
  logo_url?: string;
  verified: boolean;
  metadata_source?: string;
  confidence: string;
  metadata_status: string;
};
export type Investigation = {
  id: string;
  subject: { type: string; chain: string; value: string };
  goal: string;
  mode: string;
  status: string;
  budget: Record<string, unknown>;
  state: Record<string, unknown>;
  revision: number;
  created_at: string;
  updated_at: string;
};
export type InvestigationRuntime = {
  runs: Array<{
    id: string;
    status: string;
    objective: string;
    pending_data_jobs: string[];
    checkpoint: Record<string, unknown>;
    steps: Array<{
      id: string;
      ordinal: number;
      action: string;
      capability: string;
      status: string;
      external_job_id?: string;
      error?: string;
      output?: {
        algorithm_version?: string;
        transaction_count?: number;
        unique_counterparties?: number;
        evidence_boundary?: string;
        edges?: Array<{ direction: "in" | "out"; counterparty: string; asset: string; amount: string; transactions: number; evidence: string[] }>;
      };
      output_hash?: string;
      algorithm_version?: string;
    }>;
  }>;
};
export type InvestigationArtifacts = {
  findings: Array<{ id: string; type: string; title: string; summary: string; confidence: number; status: string; properties: Record<string, unknown>; algorithm: string; algorithm_version: string; created_at: string }>;
  evidence: Array<{ id: string; type: string; title: string; source_refs: string[]; content_hash: string; provenance: Record<string, unknown>; created_at: string }>;
  graph_patches: Array<{ id: string; base_revision: number; revision: number; patch: { upsert_nodes?: unknown[]; upsert_edges?: unknown[] }; created_at: string }>;
};
export type FlowProfileEdge = { direction: "in" | "out"; counterparty: string; asset: string; amount: string; flow_ratio: string; transactions: number; first_seen?: string; last_seen?: string; evidence: string[] };
export type FlowProfile = { algorithm_version: string; chain: string; address: string; transaction_count: number; unique_counterparties: number; first_seen?: string; last_seen?: string; edges: FlowProfileEdge[]; first_funder?: FlowProfileEdge; cashout_candidates: FlowProfileEdge[]; evidence_boundary: string };
export type AnalysisCanvas = { id:string; case_id?:string; investigation_id?:string; parent_canvas_id?:string; name:string; node_refs:string[]; edge_refs:string[]; groups:Record<string,unknown>[]; annotations:Record<string,unknown>[]; filters:Record<string,unknown>; layout:Record<string,unknown>; camera:Record<string,unknown>; view_state:Record<string,unknown>; revision:number; updated_at:string };
export type CanvasSnapshot = { id:string; canvas_id:string; canvas_revision:number; name:string; state:Record<string,unknown>; created_at:string };
export type SemanticResult = { chain:string; subject:string; events:Array<{id:string;tx_hash:string;type:string;confidence:number;payload:Record<string,unknown>;provenance:Record<string,unknown>;projection_version:string;algorithm_version:string;data_version:string;occurred_at?:string;computed_at:string}>; counts:Record<string,number>; projection_version:string;algorithm_version:string;data_version:string;computed_at?:string;evidence_boundary:string };
export type AdvancedAnalysisResult = { chain:string;subject:string;signals:Array<{type:string;confidence:number;summary:string;metrics:Record<string,unknown>;evidence:string[];nature:string}>;asset_flows:Array<{asset:string;inflow:string;outflow:string;net_flow:string;basis_status:string}>;related_wallets:Array<{address:string;score:number;transactions:number;directions:string[];evidence:string[];relation_kind:string;evidence_level:string}>;projection_version:string;algorithm_version:string;data_version:string;computed_at?:string;evidence_boundary:string;limitations:string[] };
export type InvestigationReport = {id:string;investigation_id:string;revision:number;format:string;object_key:string;content_hash:string;size_bytes:number;metadata:Record<string,unknown>;generated_at:string};
export type InvestigationSubject = {id:string;investigation_id:string;type:string;value:string;label:string;source:string;confidence:number;evidence_refs:string[];provenance:Record<string,unknown>;created_at:string};
export type EntityRelationship = {id:string;investigation_id:string;source_subject_id:string;target_subject_id:string;type:string;source:string;confidence:number;evidence_refs:string[];provenance:Record<string,unknown>;created_at:string};

const BASE = "/api/v1";

/**
 * 会话受众（§3.3 / §28.4 S2-BE-03）。
 *
 * 后台接口（`admin:*` 权限键）与用户层业务接口使用**不同的 Cookie 与不同的受众**：
 *   - `user`  → `cs_user_session`  / `cs_user_session_csrf`
 *   - `admin` → `cs_admin_session` / `cs_admin_session_csrf`
 *
 * 服务端 `authmw.audienceForPath` 只按**路径前缀**推断受众（`/api/admin/*` → admin，
 * 其余 → user），因此挂在 `/api/v1/api-control/*`、`/api/v1/providers/*`、
 * `/api/v1/count-oracle/*`、`/api/v1/tokens/enrichment` 等路径下的**后台接口**
 * 必须由客户端显式声明 `X-Auth-Audience: admin`，否则服务端会去读 user Cookie。
 *
 * `X-Auth-Audience` 是**收窄信号**，不是授权绕过：
 *   - 它只决定服务端读取哪一套 Cookie；会话令牌本身仍要 `Sessions().Authenticate`
 *     校验"会话记录的受众 == 声明受众"，不匹配直接拒绝；
 *   - 授权结论仍由 Policy 引擎给出（user 会话即使被授予 `admin:*` 也会
 *     `audience_mismatch` 403）。
 * 它与用户层/后台新前端（`frontend/packages/api-client/client.ts`）使用的是同一套契约。
 */
export type Audience = "user" | "admin";

/** 读取可被 JS 访问的 CSRF double-submit Cookie（会话 Cookie 本身是 HttpOnly）。 */
function csrfCookieValue(audience: Audience): string {
  if (typeof document === "undefined") return "";
  const match = document.cookie.match(
    new RegExp(`(?:^|; )cs_${audience}_session_csrf=([^;]*)`),
  );
  return match ? decodeURIComponent(match[1]) : "";
}

function newIdempotencyKey() {
  if (typeof crypto !== "undefined" && typeof crypto.randomUUID === "function") {
    return crypto.randomUUID();
  }
  return `web-${Date.now()}-${Math.random().toString(16).slice(2)}-${Math.random().toString(16).slice(2)}`;
}

async function http<T>(
  path: string,
  init?: RequestInit,
  audience: Audience = "user",
): Promise<T> {
  const method = (init?.method ?? "GET").toUpperCase();
  const headers: Record<string, string> = {
    "Content-Type": "application/json",
    ...((init?.headers as Record<string, string> | undefined) ?? {}),
    "X-Auth-Audience": audience,
  };
  // 写请求必须带与该受众绑定的 CSRF 令牌（服务端 `require()` → `checkCSRF`）。
  if (method !== "GET" && method !== "HEAD") {
    const csrf = csrfCookieValue(audience);
    if (csrf) headers["X-CSRF-Token"] = csrf;
  }
  const res = await fetch(BASE + path, { ...init, headers, credentials: "same-origin" });
  if (!res.ok) {
    let msg = res.statusText;
    try {
      const body = await res.json();
      msg = body.error?.message ?? body.error ?? msg;
    } catch {
      /* ignore */
    }
    throw new Error(msg);
  }
  return res.json() as Promise<T>;
}

/** 后台（admin 受众）业务接口。 */
export const apiControlHttp = <T>(path: string, init?: RequestInit) =>
  http<T>("/api-control" + path, init, "admin");

// V1.3：Count Provider Port 只读端点（§72）。
export const countOracleHttp = <T>(path: string) =>
  http<T>("/count-oracle" + path, undefined, "admin");

export const api = {
  getFlowProfile: (chain:string,address:string) => http<FlowProfile>(`/analysis/addresses/${encodeURIComponent(chain)}/${encodeURIComponent(address)}/flow-profile`),
  getSemanticEvents: (chain:string,address:string) => http<SemanticResult>(`/analysis/addresses/${encodeURIComponent(chain)}/${encodeURIComponent(address)}/semantic-events`),
  // admin:dataset:manage —— 必须带 admin 受众（否则后台 cookie 不会被解析）。
  rebuildSemanticEvents: (chain:string,address:string) => http<SemanticResult>(`/analysis/addresses/${encodeURIComponent(chain)}/${encodeURIComponent(address)}/semantic-events/rebuild`,{method:"POST",body:"{}"},"admin"),
  getAdvancedAnalysis: (chain:string,address:string) => http<AdvancedAnalysisResult>(`/analysis/addresses/${encodeURIComponent(chain)}/${encodeURIComponent(address)}/advanced`),
  rebuildAdvancedAnalysis: (chain:string,address:string) => http<AdvancedAnalysisResult>(`/analysis/addresses/${encodeURIComponent(chain)}/${encodeURIComponent(address)}/advanced/rebuild`,{method:"POST",body:"{}"},"admin"),
  listInvestigationReports: (id:string) => http<{items:InvestigationReport[]}>(`/investigations/${encodeURIComponent(id)}/reports`),
  generateInvestigationReport: (id:string) => http<InvestigationReport>(`/investigations/${encodeURIComponent(id)}/reports`,{method:"POST",body:"{}"}),
  listInvestigationSubjects: (id:string) => http<{subjects:InvestigationSubject[];relationships:EntityRelationship[]}>(`/investigations/${encodeURIComponent(id)}/subjects`),
  addInvestigationSubject: (id:string,input:{type:string;value:string;label:string;source:string;confidence:number;evidence_refs:string[];provenance:Record<string,unknown>}) => http<InvestigationSubject>(`/investigations/${encodeURIComponent(id)}/subjects`,{method:"POST",body:JSON.stringify(input)}),
  listInvestigations: () => http<{ items: Investigation[] }>("/investigations"),
  getInvestigationRuntime: (id: string) =>
    http<InvestigationRuntime>(`/investigations/${id}/runtime`),
  getInvestigationArtifacts: (id: string) =>
    http<InvestigationArtifacts>(`/investigations/${id}/artifacts`),
  createInvestigation: (payload: unknown) =>
    http<{ investigation: Investigation }>("/investigations", {
      method: "POST",
      headers: {
        "Content-Type": "application/json",
        "Idempotency-Key": newIdempotencyKey(),
      },
      body: JSON.stringify(payload),
    }),
  controlInvestigation: (id: string, action: string, revision: number) =>
    http<Investigation | { investigation: Investigation; runtime: unknown }>(
      `/investigations/${id}/${action}`,
      {
        method: "POST",
        headers: {
          "Content-Type": "application/json",
          "If-Match": String(revision),
        },
        body: "{}",
      },
    ),
  ensure: (payload: unknown) =>
    http<{ job_id: string | null; coverage: number; status: string }>(
      "/datasets/ensure",
      { method: "POST", body: JSON.stringify(payload) },
    ),
  preview: (payload: unknown) =>
    http<CoveragePreview>("/datasets/coverage/preview", {
      method: "POST",
      body: JSON.stringify(payload),
    }),
  listJobs: (status = "") =>
    http<{ jobs: Job[] }>(
      `/download-jobs?limit=100${status ? "&status=" + status : ""}`,
    ),
  getJob: (id: string) => http<JobDetail>(`/download-jobs/${id}`),
  pause: (id: string) =>
    http<{ status: string }>(`/download-jobs/${id}/pause`, { method: "POST" }),
  resume: (id: string) =>
    http<{ status: string }>(`/download-jobs/${id}/resume`, { method: "POST" }),
  cancel: (id: string) =>
    http<{ status: string }>(`/download-jobs/${id}/cancel`, { method: "POST" }),
  volumeGuards: (id: string) =>
    http<{ guards: VolumeGuard[] }>(`/download-jobs/${id}/guard`),
  continueVolumeGuard: (id: string, subject: string, dataset: string) =>
    http<VolumeGuard>(`/download-jobs/${id}/guard/continue`, {
      method: "POST",
      body: JSON.stringify({ subject, dataset }),
    }),
  abortVolumeGuard: (id: string, subject: string, dataset: string) =>
    http<VolumeGuard>(`/download-jobs/${id}/guard/abort`, {
      method: "POST",
      body: JSON.stringify({ subject, dataset }),
    }),
  providerStatus: () =>
    http<Record<string, unknown>>("/providers/ankr/status", undefined, "admin"),
  getAnkrProductionProfile: () =>
    http<AnkrProductionProfile>("/providers/ankr/production-profile", undefined, "admin"),
  getAnkrFetchRuntime: () =>
    http<PerformanceModeState>("/providers/ankr/fetch-runtime", undefined, "admin"),
  setAnkrPerformanceMode: (mode: PerformanceMode) =>
    http<PerformanceModeState>(
      "/providers/ankr/performance-mode",
      { method: "POST", body: JSON.stringify({ mode }) },
      "admin",
    ),
  setJobPerformanceMode: (id: string, mode: PerformanceMode) =>
    http<PerformanceModeState & { job_id: string }>(
      "/download-jobs/" + id + "/performance-mode",
      { method: "POST", body: JSON.stringify({ mode }) },
      "admin",
    ),
  providerUsage: (hours = 24) =>
    http<{
      since: string;
      by_dataset: Record<
        string,
        {
          requests: number;
          credits: number;
          cost_usd: number;
          rows: number;
          bytes: number;
        }
      >;
    }>(`/providers/ankr/usage?hours=${hours}`, undefined, "admin"),
  createExport: (jobId: string, dataset: string, format: string) =>
    http<ExportRow>("/exports", {
      method: "POST",
      body: JSON.stringify({ job_id: jobId, dataset, format }),
    }),
  createExportBySubject: (dataset: string, subject: string, chain: string) =>
    http<ExportRow>("/exports", {
      method: "POST",
      body: JSON.stringify({ dataset, format: "csv", subject, chain }),
    }),
  getExport: (id: string) => http<ExportRow>(`/exports/${id}`),
  snapshot: (id: string) =>
    http<JobDetail>(`/download-jobs/${id}`).then((d) => d.progress as Snapshot),
  queryRows: (
    dataset: string,
    chain: string,
    subject: string,
    limit = 100,
    offset = 0,
  ) =>
    http<{ dataset: string; rows: Record<string, unknown>[]; count: number }>(
      `/datasets/${dataset}?chain=${chain}&subject=${subject}&limit=${limit}&offset=${offset}`,
    ),

  // ---- V1.2 ----
  // R10 实测缺陷（2026-09-13）：未认证时 /cases 返回 401（走 catch），但**已认证**用户在空工作区
  // 拿到的是 200 {"cases":null}（Go nil slice 序列化为 null）。调用方（DownloadPage / DataExplorerPage）
  // 直接 cases.map → "Cannot read properties of null (reading 'map')"，整页白屏。
  // 在 API 边界统一归一化为 []，避免每个页面各写一遍防御。
  listCases: () =>
    http<{ cases: { id: string; name: string; color?: string }[] | null }>("/cases").then((d) => ({
      cases: Array.isArray(d?.cases) ? d.cases : [],
    })),
  createCase: (name: string) =>
    http<{ id: string; name: string }>("/cases", {
      method: "POST",
      body: JSON.stringify({ name }),
    }),
  listCanvases: (caseId:string) => http<{items:AnalysisCanvas[]}>(`/cases/${caseId}/canvases`),
  createCanvas: (caseId:string,payload:unknown) => http<AnalysisCanvas>(`/cases/${caseId}/canvases`,{method:"POST",body:JSON.stringify(payload)}),
  updateCanvas: (id:string,revision:number,payload:unknown) => http<AnalysisCanvas>(`/canvases/${id}`,{method:"PATCH",headers:{"Content-Type":"application/json","If-Match":String(revision)},body:JSON.stringify(payload)}),
  branchCanvas: (id:string,payload:unknown) => http<AnalysisCanvas>(`/canvases/${id}/branch`,{method:"POST",body:JSON.stringify(payload)}),
  createCanvasSnapshot: (id:string,name:string) => http<CanvasSnapshot>(`/canvases/${id}/snapshots`,{method:"POST",body:JSON.stringify({name})}),
  listCanvasSnapshots: (id:string) => http<{items:CanvasSnapshot[]}>(`/canvases/${id}/snapshots`),
  // 同 listCases：空工作区返回 200 {"tags":null}，必须归一化为 []（见上）。
  listTags: () =>
    http<{ tags: { id: string; name: string; color?: string }[] | null }>("/tags").then((d) => ({
      tags: Array.isArray(d?.tags) ? d.tags : [],
    })),
  createTag: (name: string) =>
    http<{ id: string; name: string }>("/tags", {
      method: "POST",
      body: JSON.stringify({ name }),
    }),
  updateAddressMetadata: (chain: string, address: string, payload: unknown) =>
    http<{
      address: {
        case_names: string[];
        tag_names: string[];
        total_activity_count?: number;
        last_data_block?: number;
        last_sync_at?: string;
      };
    }>(
      `/addresses/${encodeURIComponent(chain)}/${encodeURIComponent(address)}/metadata`,
      { method: "PATCH", body: JSON.stringify(payload) },
      "admin",
    ),
  explorerAddresses: (params: string) =>
    http<{ addresses: unknown[]; next_cursor: string }>(
      "/data-explorer/addresses?" + params,
    ),
  explorerQuery: (payload: unknown) =>
    http<{
      rows: Record<string, unknown>[];
      next_cursor: string;
      count: number;
    }>("/data-explorer/query", {
      method: "POST",
      body: JSON.stringify(payload),
    }),
  updatePreview: (payload: unknown) =>
    http<Record<string, unknown>>(
      "/updates/preview",
      { method: "POST", body: JSON.stringify(payload) },
      "admin",
    ),
  updateConfirm: (previewId: string) =>
    http<{ job_ids: string[] }>(
      "/updates/" + previewId + "/confirm",
      { method: "POST", body: "{}" },
      "admin",
    ),
  exportV2: (payload: unknown) =>
    http<ExportRow>("/exports", {
      method: "POST",
      body: JSON.stringify(payload),
    }),
  exportTicket: (id: string) =>
    http<{ ticket_id: string; url: string }>(
      "/exports/" + id + "/download-ticket",
      { method: "POST", body: "{}" },
    ),
  tokenSearch: (chain: string, query: string, limit = 30) =>
    http<{ tokens: TokenMetadata[] }>(
      `/tokens/search?chain=${encodeURIComponent(chain)}&q=${encodeURIComponent(query)}&limit=${limit}`,
    ),
  tokenEnrichment: () =>
    http<{
      token_contracts: number;
      complete: number;
      partial: number;
      unknown: number;
      conflict: number;
      failed: number;
      logo_available: number;
      queue_depth: number;
    }>("/tokens/enrichment", undefined, "admin"),
};


/**
 * 后台会话探测：以 admin 受众调用 `GET /api/auth/v1/session`。
 *
 * 这是**只读探测**：它不签发、不提升任何权限，只是问服务端"当前浏览器是否持有
 * 一个 admin 受众的有效会话"。未持有时服务端返回 401，页面据此走后台登录入口。
 */
export type AdminSessionProbe = {
  authenticated: boolean;
  email?: string;
  displayName?: string;
  roles?: string[];
  mustChangePassword?: boolean;
};

export async function probeAdminSession(signal?: AbortSignal): Promise<AdminSessionProbe> {
  try {
    const res = await fetch("/api/auth/v1/session", {
      headers: { Accept: "application/json", "X-Auth-Audience": "admin" },
      credentials: "same-origin",
      signal,
    });
    if (!res.ok) return { authenticated: false };
    type SessionPayload = {
      audience?: string;
      email?: string;
      display_name?: string;
      roles?: string[];
      must_change_password?: boolean;
    };
    const body = (await res.json()) as SessionPayload & { data?: SessionPayload };
    const data: SessionPayload = body.data ?? body;
    return {
      authenticated: data.audience === "admin",
      email: data.email,
      displayName: data.display_name,
      roles: data.roles,
      mustChangePassword: data.must_change_password,
    };
  } catch {
    // 网络/解析失败一律按"没有后台会话"处理（fail closed）。
    return { authenticated: false };
  }
}

/** 带 admin 受众的只读 GET（供页面级非 api.* 端点使用，例如 /api/v1/coverage/stats）。 */
export async function adminGetJson<T>(path: string, signal?: AbortSignal): Promise<T> {
  const res = await fetch(BASE + path, {
    headers: { Accept: "application/json", "X-Auth-Audience": "admin" },
    credentials: "same-origin",
    signal,
  });
  if (!res.ok) {
    let msg = res.statusText;
    try {
      const body = await res.json();
      msg = body.error?.message ?? body.error ?? msg;
    } catch {
      /* ignore */
    }
    throw new Error(msg);
  }
  return res.json() as Promise<T>;
}
