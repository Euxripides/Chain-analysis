import { useCallback, useEffect, useRef, useState } from "react";
import { useNavigate } from "react-router-dom";
import { ApiError } from "@chain/api-client";
import {
  Card,
  DataTable,
  EmptyState,
  ErrorState,
  EvidenceRail,
  Field,
  Metric,
  PageHeader,
  SkeletonRows,
  StatusBadge,
  useDialog,
} from "@chain/ui";
import { formatRawAmount, formatTime, shortValue, useApi, useS5Action } from "../sprint5/common";

/* ============================================================================
 * U-DL-01 /downloads/new：真实顺序 对象 → 链与范围 → Dataset → 复用策略 →
 * Coverage/成本预览 → 确认创建。所有说明/风险/结果只通过 Dialog 呈现。
 * ==========================================================================*/

interface ChainMeta {
  slug: string;
  name: string;
  type: string;
  enabled: boolean;
  supports_trace: boolean;
}

interface DatasetMeta {
  id: string;
  label: string;
  status: string;
}

type RangeType = "full" | "block" | "time";
type ReusePolicy = "prefer_local" | "refresh";
type PerformanceMode = "safe" | "fast" | "turbo";

interface RangeBody {
  type: RangeType;
  from_block?: number;
  to_block?: number;
  from?: string;
  to?: string;
}

interface DownloadBody {
  chain: string;
  addresses: string[];
  datasets: string[];
  range: RangeBody;
  reuse_policy: ReusePolicy;
  performance_mode: PerformanceMode;
}

interface PreviewCoverageItem {
  subject: string;
  dataset: string;
  covered_blocks: number;
  total_blocks: number;
  ratio: number;
  gaps: Array<{ from_block: number; to_block: number }>;
}

interface PreviewCandidate {
  job_id: string;
  job_no: string;
  status: string;
  coverage: number;
  updated_at: string;
}

interface PreviewReuse {
  policy: string;
  will_reuse: boolean;
  candidate: PreviewCandidate | null;
  coverage_hit: boolean;
  refresh: boolean;
}

interface DownloadEstimate {
  estimated_missing_blocks: number | null;
  estimated_rows: number | null;
  estimated_cost_usd: string | null;
  estimated_time_seconds: number | null;
  basis: string;
}

interface DownloadPreviewData {
  request: {
    chain: string;
    addresses: string[];
    datasets: string[];
    range: RangeBody;
    reuse_policy: ReusePolicy;
    performance_mode: PerformanceMode;
  };
  coverage: { chain: string; datasets: string[]; items: PreviewCoverageItem[]; overall_ratio: number };
  missing_blocks: number;
  provider_work_required: boolean;
  reuse: PreviewReuse;
  estimate: DownloadEstimate;
  risks: string[];
  source: Record<string, unknown>;
}

interface DownloadCreateData {
  status: "ready" | "reused" | "planned";
  created: boolean;
  reused: boolean;
  provider_work_required: boolean;
  coverage: number;
  job_id: string | null;
  job_no?: string;
}

function parseAddressInput(raw: string) {
  const tokens = raw
    .split(/[\s,;，；|]+/)
    .map((value) => value.trim())
    .filter(Boolean);
  const seen = new Set<string>();
  const list: string[] = [];
  let duplicates = 0;
  for (const token of tokens) {
    const localKey = token.toLowerCase();
    if (seen.has(localKey)) {
      duplicates += 1;
      continue;
    }
    seen.add(localKey);
    list.push(token);
  }
  return { tokens: tokens.length, list, duplicates };
}

function toIso(localValue: string): string {
  if (!localValue) return "";
  const date = new Date(localValue);
  if (Number.isNaN(date.getTime())) return "";
  return date.toISOString();
}

function percent(value: number | null | undefined, digits = 1): string {
  if (value === null || value === undefined || Number.isNaN(value)) return "—";
  return `${(value * 100).toFixed(digits)}%`;
}

function rangeText(body: { range: RangeBody }): string {
  const range = body.range;
  if (range.type === "full") return "全量范围";
  if (range.type === "block") return `区块 ${range.from_block ?? "—"} → ${range.to_block ?? "—"}`;
  return `时间 ${range.from ? formatTime(range.from) : "—"} → ${range.to ? formatTime(range.to) : "—"}`;
}

export default function DownloadNewPage() {
  const dialog = useDialog();
  const navigate = useNavigate();
  const { busy, run, client } = useS5Action();

  const chainsApi = useApi<{ items: ChainMeta[] }>("/api/v2/meta/chains");
  const datasetsApi = useApi<{ items: DatasetMeta[] }>("/api/v2/meta/datasets");

  const [addressText, setAddressText] = useState("");
  const [addresses, setAddresses] = useState<string[]>([]);
  const [parsedFrom, setParsedFrom] = useState("");
  const [chain, setChain] = useState("");
  const [rangeType, setRangeType] = useState<RangeType>("full");
  const [fromBlock, setFromBlock] = useState("");
  const [toBlock, setToBlock] = useState("");
  const [fromTime, setFromTime] = useState("");
  const [toTime, setToTime] = useState("");
  const [datasets, setDatasets] = useState<string[]>([]);
  const [reusePolicy, setReusePolicy] = useState<ReusePolicy>("prefer_local");
  const [performanceMode, setPerformanceMode] = useState<PerformanceMode>("safe");
  const [preview, setPreview] = useState<DownloadPreviewData | null>(null);
  const [previewFingerprint, setPreviewFingerprint] = useState<string | null>(null);
  const [lastTrace, setLastTrace] = useState<string | null>(null);

  const datasetsTouched = useRef(false);
  const lastNotifiedError = useRef<string | null>(null);

  /* 加载错误：ErrorState 只显示通用文案，原始错误正文只允许出现在 Dialog。 */
  useEffect(() => {
    for (const error of [chainsApi.error, datasetsApi.error]) {
      if (error && lastNotifiedError.current !== error) {
        lastNotifiedError.current = error;
        dialog.notify("error", "加载失败", error);
      }
    }
  }, [chainsApi.error, datasetsApi.error, dialog]);

  const chainItems = chainsApi.data?.items ?? [];
  const datasetItems = datasetsApi.data?.items ?? [];

  useEffect(() => {
    if (chain) return;
    const first = chainItems.find((item) => item.enabled) ?? chainItems[0];
    if (first) setChain(first.slug);
  }, [chain, chainItems]);

  useEffect(() => {
    if (datasetsTouched.current) return;
    const available = datasetItems.map((item) => item.id);
    if (available.length === 0) return;
    const preferred = ["transactions", "token_transfers"].filter((id) => available.includes(id));
    setDatasets(preferred.length > 0 ? preferred : [available[0]]);
  }, [datasetItems]);

  const buildPayload = useCallback(
    (addrs: string[]): DownloadBody => {
      let range: RangeBody;
      if (rangeType === "block") {
        range = { type: "block", from_block: Number(fromBlock), to_block: Number(toBlock) };
      } else if (rangeType === "time") {
        range = { type: "time", from: toIso(fromTime), to: toIso(toTime) };
      } else {
        range = { type: "full" };
      }
      return { chain, addresses: addrs, datasets, range, reuse_policy: reusePolicy, performance_mode: performanceMode };
    },
    [chain, datasets, fromBlock, fromTime, performanceMode, rangeType, reusePolicy, toBlock, toTime],
  );

  const importedAddressText = parsedFrom !== "" && parsedFrom === addressText.trim();

  const importAddresses = useCallback(async (): Promise<string[] | null> => {
    const parsed = parseAddressInput(addressText);
    if (parsed.tokens === 0) {
      dialog.notify("warning", "没有可导入的地址", "请粘贴至少一个地址，支持换行、逗号、分号或空白分隔。");
      return null;
    }
    const capped = parsed.list.slice(0, 100);
    const details = [
      { label: "原始条目", value: String(parsed.tokens) },
      { label: "本地去重后", value: String(parsed.list.length) },
      { label: "重复条目", value: String(parsed.duplicates) },
      { label: "本次导入", value: `${capped.length} 个地址` },
    ];
    if (parsed.list.length > 100) {
      details.push({ label: "超出单次上限", value: `${parsed.list.length - 100} 个未导入（上限 100）` });
    }
    const answer = await dialog.open({
      kind: parsed.list.length > 100 ? "warning" : "info",
      title: "地址导入解析摘要",
      body: "服务端最终以 chain + 规范化地址 作为去重键，本地仅做初步拆分与大小写去重。",
      details,
      actions: [
        { label: "取消", result: "", variant: "default" },
        { label: "使用这些地址", result: "use", variant: "primary" },
      ],
    });
    if (answer !== "use") return null;
    const normalizedText = capped.join("\n");
    setAddressText(normalizedText);
    setAddresses(capped);
    setParsedFrom(normalizedText);
    return capped;
  }, [addressText, dialog]);

  const ensureAddresses = useCallback(async (): Promise<string[] | null> => {
    if (addresses.length > 0 && importedAddressText) return addresses;
    return importAddresses();
  }, [addresses, importAddresses, importedAddressText]);

  const validateForm = useCallback(
    (addrs: string[]): boolean => {
      if (!chain) {
        dialog.notify("warning", "请选择链", "链选项来自服务端 /api/v2/meta/chains，未加载时无法提交。");
        return false;
      }
      if (addrs.length === 0) {
        dialog.notify("warning", "请导入地址", "至少需要一个地址才能创建下载。");
        return false;
      }
      if (addrs.length > 100) {
        dialog.notify("warning", "地址数量超出上限", "单次下载最多 100 个地址，请拆分后重试。");
        return false;
      }
      if (datasets.length === 0) {
        dialog.notify("warning", "请选择 Dataset", "至少选择一个 Dataset。");
        return false;
      }
      if (datasets.length > 6) {
        dialog.notify("warning", "Dataset 数量超出上限", "单次下载最多选择 6 个 Dataset。");
        return false;
      }
      if (rangeType === "block") {
        const from = Number(fromBlock);
        const to = Number(toBlock);
        if (fromBlock.trim() === "" || toBlock.trim() === "" || !Number.isInteger(from) || !Number.isInteger(to) || from < 0 || to < 0) {
          dialog.notify("warning", "区块范围不合法", "请填写从 0 开始的整数区块高度。");
          return false;
        }
        if (from > to) {
          dialog.notify("warning", "区块范围不合法", "起始区块不能大于结束区块。");
          return false;
        }
      }
      if (rangeType === "time") {
        const from = toIso(fromTime);
        const to = toIso(toTime);
        if (!from || !to) {
          dialog.notify("warning", "时间范围不合法", "请选择合法的起始与结束时间。");
          return false;
        }
        if (new Date(from).getTime() > new Date(to).getTime()) {
          dialog.notify("warning", "时间范围不合法", "起始时间不能晚于结束时间。");
          return false;
        }
      }
      return true;
    },
    [chain, datasets.length, dialog, fromBlock, fromTime, rangeType, toBlock, toTime],
  );

  const previewDetails = useCallback((data: DownloadPreviewData) => {
    const details: Array<{ label: string; value: string }> = [
      { label: "复用策略", value: data.reuse.policy },
      { label: "Coverage 命中", value: data.reuse.coverage_hit ? "是" : "否" },
      { label: "将复用现有任务", value: data.reuse.will_reuse ? "是" : "否" },
      { label: "provider_work_required", value: String(data.provider_work_required) },
      { label: "整体覆盖率", value: percent(data.coverage.overall_ratio) },
      { label: "未覆盖区块", value: formatRawAmount(String(data.missing_blocks), 0) },
      { label: "预估行数", value: data.estimate.estimated_rows === null ? "—" : formatRawAmount(String(data.estimate.estimated_rows), 0) },
      { label: "预估成本 USD", value: data.estimate.estimated_cost_usd ?? "—" },
      { label: "预估耗时（秒）", value: data.estimate.estimated_time_seconds === null ? "—" : String(data.estimate.estimated_time_seconds) },
      { label: "估算依据", value: data.estimate.basis },
    ];
    if (data.reuse.candidate) {
      details.splice(3, 0, {
        label: "候选任务",
        value: `${data.reuse.candidate.job_no} · ${data.reuse.candidate.status} · ${percent(data.reuse.candidate.coverage)}`,
      });
    }
    data.risks.forEach((risk, index) => details.push({ label: `风险 ${index + 1}`, value: risk }));
    return details;
  }, []);

  const showPreviewDialog = useCallback(
    async (data: DownloadPreviewData) => {
      const details = previewDetails(data);
      if (!data.provider_work_required) {
        await dialog.open({
          kind: "info",
          title: "无需新增 Provider 请求",
          body: "将复用本地数据/已有任务，不新增 Provider 请求。",
          details,
          actions: [{ label: "知道了", result: "ok", variant: "primary" }],
        });
        return;
      }
      await dialog.open({
        kind: data.risks.length > 0 ? "warning" : "success",
        title: data.risks.length > 0 ? "预览完成，存在风险提示" : "Coverage 与成本预览完成",
        body: data.risks.length > 0 ? "以下风险由服务端预览接口返回，请确认后再创建任务。" : undefined,
        details,
        actions: [{ label: "知道了", result: "ok", variant: "primary" }],
      });
    },
    [dialog, previewDetails],
  );

  const handlePreview = useCallback(async (): Promise<{ data: DownloadPreviewData; fingerprint: string } | null> => {
    const addrs = await ensureAddresses();
    if (!addrs) return null;
    if (!validateForm(addrs)) return null;
    const payload = buildPayload(addrs);
    const fingerprint = JSON.stringify(payload);
    const response = await run(async () => {
      try {
        return await client.post<DownloadPreviewData>("/api/v2/downloads/preview", payload);
      } catch (error) {
        if (error instanceof ApiError && error.traceId) setLastTrace(error.traceId);
        throw error;
      }
    });
    if (!response) return null;
    setPreview(response.data);
    setPreviewFingerprint(fingerprint);
    await showPreviewDialog(response.data);
    return { data: response.data, fingerprint };
  }, [buildPayload, client, ensureAddresses, run, showPreviewDialog, validateForm]);

  const handleCreate = useCallback(async () => {
    const addrs = await ensureAddresses();
    if (!addrs) return;
    if (!validateForm(addrs)) return;
    const payload = buildPayload(addrs);
    const fingerprint = JSON.stringify(payload);
    if (!preview || previewFingerprint !== fingerprint) {
      dialog.notify("info", "需要先完成真实预览", "创建前必须调用 /api/v2/downloads/preview 确认 Coverage 与成本，参数变化后会重新预览。");
      await handlePreview();
      return;
    }
    const confirm = await dialog.open({
      kind: "confirm",
      title: "确认创建下载任务？",
      body:
        payload.reuse_policy === "refresh"
          ? "refresh 将新增 Provider 取数工作并产生成本，不复用本地已完成覆盖。"
          : "默认 prefer_local：优先复用本地数据或已有任务，服务端会再次校验 Coverage 与复用候选。",
      details: [
        { label: "链", value: payload.chain },
        { label: "地址数", value: String(payload.addresses.length) },
        { label: "Dataset", value: payload.datasets.join("、") },
        { label: "范围", value: rangeText(payload) },
        { label: "复用策略", value: payload.reuse_policy },
        { label: "性能模式", value: payload.performance_mode },
        { label: "预览结论", value: preview.provider_work_required ? "需要新增 Provider 工作" : "将复用本地数据/已有任务" },
        {
          label: "预估缺口 / 成本",
          value: `${formatRawAmount(String(preview.missing_blocks), 0)} 区块 / ${preview.estimate.estimated_cost_usd ?? "—"} USD`,
        },
      ],
      actions: [
        { label: "取消", result: "", variant: "default" },
        { label: "确认创建", result: "create", variant: "primary" },
      ],
    });
    if (confirm !== "create") return;

    const response = await run(async () => {
      try {
        return await client.post<DownloadCreateData>("/api/v2/downloads", payload, {
          idempotencyKey: crypto.randomUUID(),
        });
      } catch (error) {
        if (error instanceof ApiError && error.traceId) setLastTrace(error.traceId);
        throw error;
      }
    });
    if (!response) return;
    const data = response.data;
    const title = data.status === "ready" ? "本地数据已覆盖" : data.reused ? "已复用现有任务" : "下载任务已创建";
    const answer = await dialog.open({
      kind: "success",
      title,
      body: data.status === "ready" ? "本地 Coverage 已完整命中，未创建任务、未调用 Provider。" : undefined,
      details: [
        { label: "created", value: String(data.created) },
        { label: "reused", value: String(data.reused) },
        { label: "status", value: data.status },
        { label: "provider_work_required", value: String(data.provider_work_required) },
        { label: "coverage", value: percent(data.coverage) },
        { label: "job_id", value: data.job_id ?? "—" },
        { label: "job_no", value: data.job_no ?? "—" },
      ],
      actions: data.job_id
        ? [{ label: "查看任务", result: "view", variant: "primary" as const }]
        : [{ label: "知道了", result: "ok", variant: "primary" as const }],
    });
    if (answer === "view" && data.job_id) navigate(`/jobs/${data.job_id}`);
  }, [buildPayload, client, dialog, ensureAddresses, handlePreview, navigate, preview, previewFingerprint, run, validateForm]);

  const chooseReusePolicy = useCallback(
    async (next: ReusePolicy) => {
      if (next === "prefer_local") {
        setReusePolicy("prefer_local");
        setPreview(null);
        setPreviewFingerprint(null);
        return;
      }
      const impact = preview
        ? `当前预览：未覆盖 ${formatRawAmount(String(preview.missing_blocks), 0)} 个区块，预估成本 ${preview.estimate.estimated_cost_usd ?? "未知"} USD。`
        : "当前尚未预览，实际 Provider 工作与成本以预览和执行结果为准。";
      const answer = await dialog.open({
        kind: "confirm",
        title: "确认改用 refresh？",
        body: `refresh 会跳过本地 Coverage 复用，新增 Provider 取数工作并产生成本。${impact}`,
        details: [
          { label: "当前策略", value: reusePolicy },
          { label: "目标策略", value: "refresh" },
        ],
        actions: [
          { label: "保持 prefer_local", result: "keep", variant: "default" },
          { label: "确认使用 refresh", result: "refresh", variant: "danger" },
        ],
      });
      if (answer === "refresh") {
        setReusePolicy("refresh");
        setPreview(null);
        setPreviewFingerprint(null);
      } else {
        setReusePolicy("prefer_local");
      }
    },
    [dialog, preview, reusePolicy],
  );

  const toggleDataset = useCallback((id: string) => {
    datasetsTouched.current = true;
    setDatasets((prev) => (prev.includes(id) ? prev.filter((item) => item !== id) : [...prev, id]));
    setPreview(null);
    setPreviewFingerprint(null);
  }, []);

  const previewItems = preview?.coverage.items ?? [];

  return (
    <>
      <PageHeader
        title="新建数据下载"
        actions={
          <button type="button" className="btn btn--sm" onClick={() => navigate("/jobs")}>
            任务中心
          </button>
        }
      />

      <div className="workbench">
        <div style={{ flex: 1, minWidth: 0, display: "flex", flexDirection: "column", gap: "var(--sp-5)" }}>
          <Card title="1 · 对象（地址）">
            <div className="s4-form-grid">
              <div className="field field--wide">
                <Field label={`地址列表（已导入 ${addresses.length} 个）`} htmlFor="download-addresses">
                  <textarea
                    id="download-addresses"
                    className="textarea"
                    value={addressText}
                    placeholder="每行一个地址，或用逗号/分号/空白分隔后粘贴"
                    onChange={(event) => {
                      setAddressText(event.target.value);
                      if (event.target.value.trim() !== parsedFrom) {
                        setAddresses([]);
                        setPreview(null);
                        setPreviewFingerprint(null);
                      }
                    }}
                  />
                </Field>
              </div>
              <div className="field--wide toolbar">
                <button type="button" className="btn btn--sm" onClick={() => void importAddresses()}>
                  解析导入
                </button>
                <button
                  type="button"
                  className="btn btn--sm btn--ghost"
                  disabled={addresses.length === 0 && addressText.trim() === ""}
                  onClick={() => {
                    setAddressText("");
                    setAddresses([]);
                    setParsedFrom("");
                  }}
                >
                  清空
                </button>
              </div>
            </div>
          </Card>

          <Card title="2 · 链与范围">
            <div className="s4-form-grid">
              <Field label="链" htmlFor="download-chain">
                <select
                  id="download-chain"
                  className="select"
                  value={chain}
                  onChange={(event) => {
                    setChain(event.target.value);
                    setPreview(null);
                    setPreviewFingerprint(null);
                  }}
                >
                  <option value="">请选择链</option>
                  {chainItems.map((item) => (
                    <option key={item.slug} value={item.slug} disabled={!item.enabled}>
                      {item.name}（{item.slug}）{item.enabled ? "" : " · 不可用"}
                    </option>
                  ))}
                </select>
              </Field>
              <Field label="范围类型" htmlFor="download-range-type">
                <select
                  id="download-range-type"
                  className="select"
                  value={rangeType}
                  onChange={(event) => {
                    setRangeType(event.target.value as RangeType);
                    setPreview(null);
                    setPreviewFingerprint(null);
                  }}
                >
                  <option value="full">full · 全量</option>
                  <option value="block">block · 区块区间</option>
                  <option value="time">time · 时间区间</option>
                </select>
              </Field>
              {rangeType === "block" ? (
                <>
                  <Field label="起始区块" htmlFor="download-from-block">
                    <input id="download-from-block" className="input" inputMode="numeric" value={fromBlock} onChange={(event) => { setFromBlock(event.target.value); setPreview(null); setPreviewFingerprint(null); }} />
                  </Field>
                  <Field label="结束区块" htmlFor="download-to-block">
                    <input id="download-to-block" className="input" inputMode="numeric" value={toBlock} onChange={(event) => { setToBlock(event.target.value); setPreview(null); setPreviewFingerprint(null); }} />
                  </Field>
                </>
              ) : null}
              {rangeType === "time" ? (
                <>
                  <Field label="起始时间" htmlFor="download-from-time">
                    <input id="download-from-time" className="input" type="datetime-local" value={fromTime} onChange={(event) => { setFromTime(event.target.value); setPreview(null); setPreviewFingerprint(null); }} />
                  </Field>
                  <Field label="结束时间" htmlFor="download-to-time">
                    <input id="download-to-time" className="input" type="datetime-local" value={toTime} onChange={(event) => { setToTime(event.target.value); setPreview(null); setPreviewFingerprint(null); }} />
                  </Field>
                </>
              ) : null}
              <Field label="性能模式" htmlFor="download-performance">
                <select
                  id="download-performance"
                  className="select"
                  value={performanceMode}
                  onChange={(event) => {
                    setPerformanceMode(event.target.value as PerformanceMode);
                    setPreview(null);
                    setPreviewFingerprint(null);
                  }}
                >
                  <option value="safe">safe</option>
                  <option value="fast">fast</option>
                  <option value="turbo">turbo</option>
                </select>
              </Field>
            </div>
          </Card>

          <Card title="3 · Dataset">
            {datasetsApi.loading ? (
              <SkeletonRows rows={2} columns={3} />
            ) : datasetItems.length === 0 ? (
              <EmptyState title="服务端未返回可用 Dataset" />
            ) : (
              <div className="toolbar" role="group" aria-label="Dataset 选择">
                {datasetItems.map((item) => (
                  <label key={item.id} className="btn btn--sm" style={{ cursor: item.status === "available" ? "pointer" : "not-allowed" }}>
                    <input
                      type="checkbox"
                      checked={datasets.includes(item.id)}
                      disabled={item.status !== "available"}
                      onChange={() => toggleDataset(item.id)}
                    />
                    {item.label}（{item.id}）
                  </label>
                ))}
              </div>
            )}
          </Card>

          <Card title="4 · 复用策略">
            <div className="toolbar" role="radiogroup" aria-label="复用策略">
              <label className="btn btn--sm" style={{ cursor: "pointer" }}>
                <input type="radio" name="reuse-policy" checked={reusePolicy === "prefer_local"} onChange={() => void chooseReusePolicy("prefer_local")} />
                prefer_local（默认，优先复用本地）
              </label>
              <label className="btn btn--sm" style={{ cursor: "pointer" }}>
                <input type="radio" name="reuse-policy" checked={reusePolicy === "refresh"} onChange={() => void chooseReusePolicy("refresh")} />
                refresh（不复用，新增 Provider 工作）
              </label>
            </div>
          </Card>

          <Card
            title="5 · Coverage / 成本预览"
            actions={
              <span className="toolbar">
                {preview ? (
                  <button type="button" className="btn btn--sm" onClick={() => void showPreviewDialog(preview)}>
                    {preview.provider_work_required ? `成本/风险说明（${preview.risks.length}）` : "复用说明"}
                  </button>
                ) : null}
                <button type="button" className="btn btn--sm" disabled={busy} onClick={() => void handlePreview()}>
                  {busy ? "处理中…" : "预览 Coverage 与成本"}
                </button>
              </span>
            }
          >
            {chainsApi.loading || datasetsApi.loading ? (
              <SkeletonRows rows={3} columns={4} />
            ) : chainsApi.error ? (
              <ErrorState
                title="链元数据加载失败"
                message="加载失败，请重试。"
                onRetry={() => {
                  void chainsApi.reload();
                }}
              />
            ) : !preview ? (
              <EmptyState
                title="尚未预览"
                action={
                  <button type="button" className="btn btn--sm" onClick={() => void handlePreview()}>
                    立即预览
                  </button>
                }
              />
            ) : (
              <>
                <div className="metric-grid">
                  <Metric label="整体覆盖率" value={percent(preview.coverage.overall_ratio)} tone={preview.coverage.overall_ratio >= 1 ? "ok" : "warn"} />
                  <Metric label="未覆盖区块" value={formatRawAmount(String(preview.missing_blocks), 0)} />
                  <Metric
                    label="预估行数"
                    value={preview.estimate.estimated_rows === null ? "—" : formatRawAmount(String(preview.estimate.estimated_rows), 0)}
                  />
                  <Metric label="预估成本 USD" value={preview.estimate.estimated_cost_usd ?? "—"} />
                  <Metric
                    label="预估耗时（秒）"
                    value={preview.estimate.estimated_time_seconds === null ? "—" : String(preview.estimate.estimated_time_seconds)}
                  />
                  <Metric
                    label="Provider 工作"
                    value={
                      <StatusBadge tone={preview.provider_work_required ? "warn" : "ok"}>
                        {preview.provider_work_required ? "需要新增" : "复用本地"}
                      </StatusBadge>
                    }
                  />
                </div>
                <div style={{ marginTop: "var(--sp-4)" }}>
                  <DataTable<PreviewCoverageItem>
                    rows={previewItems}
                    rowKey={(row, index) => `${row.subject}-${row.dataset}-${index}`}
                    columns={[
                      { key: "subject", header: "对象", width: "170px", render: (row) => <span className="mono" title={row.subject}>{shortValue(row.subject, 12, 8)}</span> },
                      { key: "dataset", header: "Dataset", width: "120px", render: (row) => row.dataset },
                      { key: "ratio", header: "Coverage", width: "90px", render: (row) => percent(row.ratio) },
                      {
                        key: "blocks",
                        header: "已覆盖 / 总区块",
                        width: "150px",
                        render: (row) => `${formatRawAmount(String(row.covered_blocks), 0)} / ${formatRawAmount(String(row.total_blocks), 0)}`,
                      },
                      { key: "gaps", header: "缺口", width: "90px", render: (row) => String(row.gaps.length) },
                      {
                        key: "gap-range",
                        header: "首个缺口",
                        render: (row) => (row.gaps.length > 0 ? <span className="mono">{`${row.gaps[0].from_block} → ${row.gaps[0].to_block}`}</span> : "—"),
                      },
                    ]}
                    empty={<EmptyState title="服务端未返回 Coverage 明细" />}
                  />
                </div>
              </>
            )}
          </Card>

          <div className="toolbar" style={{ justifyContent: "flex-end" }}>
            <button type="button" className="btn" onClick={() => navigate("/jobs")}>
              取消
            </button>
            <button type="button" className="btn btn--primary" disabled={busy} onClick={() => void handleCreate()}>
              {busy ? "正在创建…" : "创建下载任务"}
            </button>
          </div>
        </div>

        <EvidenceRail
          title="预览来源与复用"
          items={[
            { label: "复用策略", value: preview?.reuse.policy ?? reusePolicy },
            { label: "Coverage 命中", value: preview ? (preview.reuse.coverage_hit ? "是" : "否") : "—" },
            { label: "将复用", value: preview ? (preview.reuse.will_reuse ? "是" : "否") : "—" },
            { label: "候选任务", value: preview?.reuse.candidate ? `${preview.reuse.candidate.job_no} · ${preview.reuse.candidate.status}` : "—" },
            { label: "refresh", value: preview ? (preview.reuse.refresh ? "是" : "否") : "—" },
            { label: "估算依据", value: preview?.estimate.basis ?? "—" },
            { label: "Coverage 来源", value: String(preview?.source?.coverage ?? "—") },
            { label: "Provider 来源", value: String(preview?.source?.provider ?? "—") },
            { label: "数据时点", value: preview ? formatTime(String(preview.source.as_of ?? "")) : "—" },
            { label: "最近 Trace", value: lastTrace ?? "—" },
          ]}
        />
      </div>
    </>
  );
}
