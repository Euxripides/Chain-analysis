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

function newIdempotencyKey() {
  if (typeof crypto !== "undefined" && typeof crypto.randomUUID === "function") {
    return crypto.randomUUID();
  }
  return `web-${Date.now()}-${Math.random().toString(16).slice(2)}-${Math.random().toString(16).slice(2)}`;
}

async function http<T>(path: string, init?: RequestInit): Promise<T> {
  const res = await fetch(BASE + path, {
    headers: { "Content-Type": "application/json" },
    ...init,
  });
  if (!res.ok) {
    let msg = res.statusText;
    try {
      const body = await res.json();
      msg = body.error ?? msg;
    } catch {
      /* ignore */
    }
    throw new Error(msg);
  }
  return res.json() as Promise<T>;
}

export const apiControlHttp = <T>(path: string, init?: RequestInit) =>
  http<T>("/api-control" + path, init);

// V1.3：Count Provider Port 只读端点（§72）。
export const countOracleHttp = <T>(path: string) =>
  http<T>("/count-oracle" + path);

export const api = {
  getFlowProfile: (chain:string,address:string) => http<FlowProfile>(`/analysis/addresses/${encodeURIComponent(chain)}/${encodeURIComponent(address)}/flow-profile`),
  getSemanticEvents: (chain:string,address:string) => http<SemanticResult>(`/analysis/addresses/${encodeURIComponent(chain)}/${encodeURIComponent(address)}/semantic-events`),
  rebuildSemanticEvents: (chain:string,address:string) => http<SemanticResult>(`/analysis/addresses/${encodeURIComponent(chain)}/${encodeURIComponent(address)}/semantic-events/rebuild`,{method:"POST",body:"{}"}),
  getAdvancedAnalysis: (chain:string,address:string) => http<AdvancedAnalysisResult>(`/analysis/addresses/${encodeURIComponent(chain)}/${encodeURIComponent(address)}/advanced`),
  rebuildAdvancedAnalysis: (chain:string,address:string) => http<AdvancedAnalysisResult>(`/analysis/addresses/${encodeURIComponent(chain)}/${encodeURIComponent(address)}/advanced/rebuild`,{method:"POST",body:"{}"}),
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
  providerStatus: () => http<Record<string, unknown>>("/providers/ankr/status"),
  getAnkrProductionProfile: () =>
    http<AnkrProductionProfile>("/providers/ankr/production-profile"),
  getAnkrFetchRuntime: () =>
    http<PerformanceModeState>("/providers/ankr/fetch-runtime"),
  setAnkrPerformanceMode: (mode: PerformanceMode) =>
    http<PerformanceModeState>("/providers/ankr/performance-mode", {
      method: "POST",
      body: JSON.stringify({ mode }),
    }),
  setJobPerformanceMode: (id: string, mode: PerformanceMode) =>
    http<PerformanceModeState & { job_id: string }>(
      "/download-jobs/" + id + "/performance-mode",
      { method: "POST", body: JSON.stringify({ mode }) },
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
    }>(`/providers/ankr/usage?hours=${hours}`),
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
  listCases: () =>
    http<{ cases: { id: string; name: string; color?: string }[] }>("/cases"),
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
  listTags: () =>
    http<{ tags: { id: string; name: string; color?: string }[] }>("/tags"),
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
    http<Record<string, unknown>>("/updates/preview", {
      method: "POST",
      body: JSON.stringify(payload),
    }),
  updateConfirm: (previewId: string) =>
    http<{ job_ids: string[] }>("/updates/" + previewId + "/confirm", {
      method: "POST",
      body: "{}",
    }),
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
    }>("/tokens/enrichment"),
};
