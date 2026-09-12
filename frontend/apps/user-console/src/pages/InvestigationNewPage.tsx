import { useEffect, useState } from "react";
import { useNavigate, useSearchParams } from "react-router-dom";
import { ApiError, useAuth } from "@chain/api-client";
import { Card, Field, PageHeader, useDialog } from "@chain/ui";
import { useAsync } from "../sprint4/common";

interface CaseOption {
  id: string;
  name: string;
  case_no: string;
  status: string;
}

interface CoveragePreview {
  chain: string;
  datasets: string[];
  overall_ratio: number;
  items: Array<{ subject: string; dataset: string; ratio: number; gaps: Array<{ from_block: number; to_block: number }> }>;
}

/** §18.1 U-INV-02：发起调查；先预览 Coverage，再以 Idempotency-Key 创建。 */
export default function InvestigationNewPage() {
  const { client } = useAuth();
  const dialog = useDialog();
  const navigate = useNavigate();
  const [params] = useSearchParams();
  const cases = useAsync<CaseOption[]>("/api/v2/cases", [], { limit: 200, status: "active" });
  const [busy, setBusy] = useState(false);
  const [form, setForm] = useState({
    case_id: params.get("case_id") ?? "",
    goal: "",
    mode: "assisted",
    chain: "tron",
    value: "",
    datasets: "token_transfers",
    range_type: "block",
    from_block: "",
    to_block: "",
    reuse_policy: "prefer_local",
    budget: "10.00",
  });

  useEffect(() => {
    if (!form.case_id && cases.data && cases.data.length > 0) {
      setForm((f) => ({ ...f, case_id: cases.data![0].id }));
    }
  }, [cases.data, form.case_id]);

  const payload = () => ({
    case_id: form.case_id,
    goal: form.goal,
    mode: form.mode,
    subjects: [{ type: "ADDRESS", chain: form.chain, value: form.value }],
    expected_outputs: ["FLOW_GRAPH", "FINDINGS", "EVIDENCE_INDEX"],
    data_requirement: {
      datasets: form.datasets.split(",").map((s) => s.trim()).filter(Boolean),
      range:
        form.range_type === "block"
          ? { type: "block", from_block: Number(form.from_block), to_block: Number(form.to_block) }
          : { type: form.range_type },
      reuse_policy: form.reuse_policy,
    },
    budget: { max_provider_cost_usd: form.budget },
    capability_versions: { "analysis.flow": "1.0" },
  });

  const preview = async () => {
    setBusy(true);
    try {
      const res = await client.post<{ coverage: CoveragePreview; summary: Record<string, unknown> }>("/api/v2/investigations/preview", {
        case_id: form.case_id,
        subjects: [{ type: "ADDRESS", chain: form.chain, value: form.value }],
        data_requirement: payload().data_requirement,
      });
      const c = res.data.coverage;
      dialog.notify("info", "Coverage 预览", null, [
        { label: "覆盖率", value: `${(c.overall_ratio * 100).toFixed(1)}%` },
        { label: "Coverage HIT", value: c.overall_ratio >= 1 ? "是（本地复用，无 Provider 费用）" : "否（存在缺口）" },
        { label: "Datasets", value: c.datasets.join(", ") },
        { label: "Gap 数", value: String(c.items.reduce((n, i) => n + i.gaps.length, 0)) },
      ]);
    } catch (err) {
      dialog.notify("error", "预览失败", err instanceof ApiError ? err.userMessage : "请检查地址和范围");
    } finally {
      setBusy(false);
    }
  };

  const create = async () => {
    if (!form.case_id || !form.goal.trim() || !form.value.trim()) {
      dialog.notify("warning", "请填写案件、目的与调查地址");
      return;
    }
    const answer = await dialog.confirm({
      kind: "confirm",
      title: "创建调查？",
      details: [
        { label: "案件", value: cases.data?.find((c) => c.id === form.case_id)?.name ?? form.case_id },
        { label: "目的", value: form.goal },
        { label: "对象", value: `${form.chain} / ${form.value}` },
        { label: "reuse_policy", value: form.reuse_policy },
      ],
    });
    if (answer !== "confirm") return;
    setBusy(true);
    try {
      const res = await client.post<{ investigation: { id: string }; idempotent_replay: boolean }>("/api/v2/investigations", payload(), {
        idempotencyKey: crypto.randomUUID(),
      });
      await dialog.open({
        kind: "success",
        title: res.data.idempotent_replay ? "已复用幂等结果" : "调查已创建",
        details: [{ label: "调查 ID", value: res.data.investigation.id }],
        actions: [{ label: "打开工作台", result: "ok", variant: "primary" }],
      });
      navigate(`/investigations/${res.data.investigation.id}/workbench`);
    } catch (err) {
      dialog.notify("error", "创建失败", err instanceof ApiError ? err.userMessage : "请稍后重试");
    } finally {
      setBusy(false);
    }
  };

  return (
    <>
      <PageHeader title="发起调查" subtitle="创建前先跑 Coverage 预览；Sprint 4 支持本地索引复用，缺口下载在 S5 完成。" />
      <Card>
        <div className="s4-form-grid">
          <Field label="案件" htmlFor="inv-case">
            <select id="inv-case" className="select" value={form.case_id} onChange={(e) => setForm({ ...form, case_id: e.target.value })}>
              <option value="">选择案件</option>
              {(cases.data ?? []).map((c) => (
                <option key={c.id} value={c.id}>
                  {c.case_no || "无编号"} · {c.name}
                </option>
              ))}
            </select>
          </Field>
          <Field label="模式" htmlFor="inv-mode">
            <select id="inv-mode" className="select" value={form.mode} onChange={(e) => setForm({ ...form, mode: e.target.value })}>
              <option value="assisted">assisted</option>
              <option value="manual">manual</option>
              <option value="auto">auto</option>
            </select>
          </Field>
          <div className="field field--wide">
            <Field label="调查目的" htmlFor="inv-goal">
              <textarea id="inv-goal" className="textarea" value={form.goal} onChange={(e) => setForm({ ...form, goal: e.target.value })} />
            </Field>
          </div>
          <Field label="链" htmlFor="inv-chain">
            <select id="inv-chain" className="select" value={form.chain} onChange={(e) => setForm({ ...form, chain: e.target.value })}>
              <option value="tron">tron</option>
              <option value="bsc">bsc</option>
              <option value="eth">eth</option>
            </select>
          </Field>
          <Field label="调查地址" htmlFor="inv-value">
            <input id="inv-value" className="input mono" value={form.value} onChange={(e) => setForm({ ...form, value: e.target.value })} placeholder="TRON Base58 或 EVM 0x…" />
          </Field>
          <Field label="Datasets" htmlFor="inv-datasets">
            <input id="inv-datasets" className="input" value={form.datasets} onChange={(e) => setForm({ ...form, datasets: e.target.value })} />
          </Field>
          <Field label="复用策略" htmlFor="inv-reuse">
            <select id="inv-reuse" className="select" value={form.reuse_policy} onChange={(e) => setForm({ ...form, reuse_policy: e.target.value })}>
              <option value="prefer_local">prefer_local</option>
              <option value="local_only">local_only</option>
              <option value="refresh">refresh</option>
            </select>
          </Field>
          <Field label="范围类型" htmlFor="inv-range">
            <select id="inv-range" className="select" value={form.range_type} onChange={(e) => setForm({ ...form, range_type: e.target.value })}>
              <option value="block">block</option>
              <option value="full">full</option>
            </select>
          </Field>
          <Field label="预算上限 USD" htmlFor="inv-budget">
            <input id="inv-budget" className="input" value={form.budget} onChange={(e) => setForm({ ...form, budget: e.target.value })} />
          </Field>
          {form.range_type === "block" ? (
            <>
              <Field label="From block" htmlFor="inv-from">
                <input id="inv-from" className="input mono" inputMode="numeric" value={form.from_block} onChange={(e) => setForm({ ...form, from_block: e.target.value })} />
              </Field>
              <Field label="To block" htmlFor="inv-to">
                <input id="inv-to" className="input mono" inputMode="numeric" value={form.to_block} onChange={(e) => setForm({ ...form, to_block: e.target.value })} />
              </Field>
            </>
          ) : null}
          <div className="s4-sticky-actions field--wide">
            <button type="button" className="btn" disabled={busy || !form.value} onClick={preview}>
              Coverage 预览
            </button>
            <button type="button" className="btn btn--primary" disabled={busy} onClick={create}>
              {busy ? "处理中…" : "创建调查"}
            </button>
          </div>
        </div>
      </Card>
    </>
  );
}
