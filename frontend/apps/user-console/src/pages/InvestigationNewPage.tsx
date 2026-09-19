import {
  useEffect,
  useMemo,
  useState } from "react";
import { useNavigate,
  useSearchParams } from "react-router-dom";
import { ApiError,
  useAuth } from "@chain/api-client";
import {
  Button,
  Card,
  DataTable,
  EmptyState,
  ErrorState,
  Field,
  HashValue,
  KeyValueList,
  MetaLine,
  PageSection,
  SkeletonRows,
  StatusBadge,
  Stepper,
  StickyActionBar,
  useDialog,
  type DataTableColumn,
} from "@chain/ui";
import { CsPageHeader } from "@chain/user-ui";
import { useAsync } from "../sprint4/common";

interface CaseOption {
  id: string;
  name: string;
  case_no: string;
  status: string;
}

interface CoverageItem {
  subject: string;
  dataset: string;
  ratio: number;
  gaps: Array<{ from_block: number; to_block: number }>;
}

interface CoveragePreview {
  chain: string;
  datasets: string[];
  overall_ratio: number;
  items: CoverageItem[];
}

/** 预览参数签名：只包含真正进入 preview 请求的字段，签名变化即表示预览结果已失效。 */
interface PreviewSignature {
  case_id: string;
  chain: string;
  value: string;
  datasets: string;
  range_type: string;
  from_block: string;
  to_block: string;
  reuse_policy: string;
}

const STEPS = [
  { key: "target", label: "选择案件与目标" },
  { key: "subject", label: "配置对象与模式" },
  { key: "coverage", label: "Coverage 预览与确认" },
];

function ratioText(ratio: number): string {
  return `${(ratio * 100).toFixed(1)}%`;
}

/** §18.1 U-INV-02：发起调查；先预览 Coverage，再以 Idempotency-Key 创建。 */
export default function InvestigationNewPage() {
  const { client } = useAuth();
  const dialog = useDialog();
  const navigate = useNavigate();
  const [params] = useSearchParams();
  const cases = useAsync<CaseOption[]>("/api/v2/cases", [], { limit: 200, status: "active" });
  const [busy, setBusy] = useState(false);
  const [step, setStep] = useState("target");
  const [coverage, setCoverage] = useState<{ signature: string; preview: CoveragePreview } | null>(null);
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

  const signature = useMemo<PreviewSignature>(
    () => ({
      case_id: form.case_id,
      chain: form.chain,
      value: form.value,
      datasets: form.datasets,
      range_type: form.range_type,
      from_block: form.from_block,
      to_block: form.to_block,
      reuse_policy: form.reuse_policy,
    }),
    [form.case_id, form.chain, form.value, form.datasets, form.range_type, form.from_block, form.to_block, form.reuse_policy],
  );
  const signatureKey = JSON.stringify(signature);
  const previewFresh = coverage !== null && coverage.signature === signatureKey;
  const stepIndex = Math.max(0, STEPS.findIndex((s) => s.key === step));

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

  const rangeText = () =>
    form.range_type === "block" ? `block ${form.from_block || "—"} → ${form.to_block || "—"}` : form.range_type;

  /** Coverage 计算口径属于说明性内容，只能通过 Dialog 呈现（AGENTS.md）。 */
  const explainCoverage = () => {
    dialog.notify("info", "Coverage 计算说明", null, [
      { label: "覆盖率", value: "服务端按 subject × dataset 计算本地索引已覆盖区间占请求区间的比例，界面不做二次估算" },
      { label: "缺口", value: "未被本地索引覆盖的区块区间，逐 subject × dataset 原样返回" },
      { label: "复用策略", value: "prefer_local 优先复用本地；local_only 只用本地；refresh 强制重新拉取" },
      { label: "费用", value: "Coverage 全部命中时不产生 Provider 请求费用" },
    ]);
  };

  const preview = async () => {
    setBusy(true);
    try {
      const res = await client.post<{ coverage: CoveragePreview; summary: Record<string, unknown> }>("/api/v2/investigations/preview", {
        case_id: form.case_id,
        subjects: [{ type: "ADDRESS", chain: form.chain, value: form.value }],
        data_requirement: payload().data_requirement,
      });
      setCoverage({ signature: signatureKey, preview: res.data.coverage });
    } catch (err) {
      dialog.notify("error", "预览失败", err instanceof ApiError ? err.userMessage : "请检查地址和范围");
    } finally {
      setBusy(false);
    }
  };

  const nextStep = () => {
    if (step === "target") {
      if (!form.case_id || !form.goal.trim()) {
        dialog.notify("warning", "请先选择案件并填写调查目的");
        return;
      }
      setStep("subject");
      return;
    }
    if (!form.value.trim()) {
      dialog.notify("warning", "请填写调查地址");
      return;
    }
    setStep("coverage");
  };

  const create = async () => {
    if (!form.case_id || !form.goal.trim() || !form.value.trim()) {
      dialog.notify("warning", "请填写案件、目的与调查地址");
      return;
    }
    if (!previewFresh) {
      dialog.notify("warning", "Coverage 预览已失效", "当前参数下的预览结果已不可用，请重新运行 Coverage 预览后再创建调查。");
      setStep("coverage");
      return;
    }
    const fresh = coverage!.preview;
    const answer = await dialog.confirm({
      kind: "confirm",
      title: "创建调查？",
      details: [
        { label: "案件", value: cases.data?.find((c) => c.id === form.case_id)?.name ?? form.case_id },
        { label: "目的", value: form.goal },
        { label: "地址", value: form.value },
        { label: "链", value: form.chain },
        { label: "模式", value: form.mode },
        { label: "数据集", value: payload().data_requirement.datasets.join(", ") || "—" },
        { label: "范围", value: rangeText() },
        { label: "reuse_policy", value: form.reuse_policy },
        { label: "预算上限 USD", value: form.budget },
        { label: "复用结果", value: `覆盖率 ${ratioText(fresh.overall_ratio)} · ${fresh.overall_ratio >= 1 ? "本地复用，无 Provider 费用" : "存在缺口"}` },
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

  const coverageColumns: Array<DataTableColumn<CoverageItem>> = [
    { key: "dataset", header: "Dataset", width: "180px", render: (row) => <span className="mono">{row.dataset}</span> },
    { key: "subject", header: "Subject", render: (row) => <HashValue value={row.subject} head={12} tail={8} label="调查对象" /> },
    { key: "ratio", header: "覆盖率", width: "110px", align: "right", render: (row) => <span className="num">{ratioText(row.ratio)}</span> },
    {
      key: "gaps",
      header: "缺口数",
      width: "100px",
      align: "right",
      render: (row) => <span className="num">{row.gaps.length}</span>,
    },
  ];

  const previewSummary = coverage ? coverage.preview : null;
  const coverageItems = previewSummary?.items ?? [];
  const gapCount = coverageItems.reduce((n, item) => n + item.gaps.length, 0);

  return (
    <>
      <CsPageHeader
        title="发起调查"
        breadcrumb={[{ label: "调查任务", to: "/investigations" }, { label: "发起调查" }]}
        status={<StatusBadge tone="info">{`第 ${stepIndex + 1} / ${STEPS.length} 步`}</StatusBadge>}
        subtitle={STEPS[stepIndex].label}
        meta={
          <MetaLine
            items={[
              { label: "可选进行中案件", value: cases.data ? String(cases.data.length) : "—" },
              { label: "模式", value: form.mode },
              { label: "范围", value: rangeText() },
            ]}
          />
        }
        primaryAction={
          <Button variant="primary" size="sm" busy={busy} busyLabel="处理中…" onClick={create}>
            创建调查
          </Button>
        }
        secondaryAction={
          <Button size="sm" variant="ghost" onClick={explainCoverage}>
            计算说明
          </Button>
        }
      />

      <Stepper steps={STEPS} current={step} onJump={setStep} ariaLabel="发起调查步骤" />

      {step === "target" ? (
        <PageSection title="案件与调查目的">
          <Card>
            {cases.error ? <ErrorState title="案件列表加载失败" message={cases.error} onRetry={cases.reload} /> : null}
            <div className="s4-form-grid">
              <Field label="案件" htmlFor="inv-case" required>
                {cases.loading ? (
                  <SkeletonRows rows={1} columns={1} />
                ) : (
                  <select id="inv-case" className="select" value={form.case_id} onChange={(e) => setForm({ ...form, case_id: e.target.value })}>
                    <option value="">选择案件</option>
                    {(cases.data ?? []).map((c) => (
                      <option key={c.id} value={c.id}>
                        {c.case_no || "无编号"} · {c.name}
                      </option>
                    ))}
                  </select>
                )}
              </Field>
              <div className="field field--wide">
                <Field label="调查目的" htmlFor="inv-goal" required>
                  <textarea id="inv-goal" className="textarea" value={form.goal} onChange={(e) => setForm({ ...form, goal: e.target.value })} />
                </Field>
              </div>
            </div>
          </Card>
        </PageSection>
      ) : null}

      {step === "subject" ? (
        <PageSection title="对象与数据范围">
          <Card>
            <div className="s4-form-grid">
              <Field label="模式" htmlFor="inv-mode">
                <select id="inv-mode" className="select" value={form.mode} onChange={(e) => setForm({ ...form, mode: e.target.value })}>
                  <option value="assisted">assisted</option>
                  <option value="manual">manual</option>
                  <option value="auto">auto</option>
                </select>
              </Field>
              <Field label="链" htmlFor="inv-chain">
                <select id="inv-chain" className="select" value={form.chain} onChange={(e) => setForm({ ...form, chain: e.target.value })}>
                  <option value="tron">tron</option>
                  <option value="bsc">bsc</option>
                  <option value="eth">eth</option>
                </select>
              </Field>
              <Field label="调查地址" htmlFor="inv-value" required>
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
            </div>
          </Card>
        </PageSection>
      ) : null}

      {step === "coverage" ? (
        <PageSection
          title="Coverage 预览"
          actions={<StatusBadge tone={previewFresh ? "ok" : previewSummary ? "warn" : "neutral"}>{previewFresh ? "预览有效" : previewSummary ? "预览已失效，需重新预览" : "尚未预览"}</StatusBadge>}
        >
          <Card>
            {!previewSummary ? (
              <EmptyState
                title="尚未运行 Coverage 预览"
                body="预览会按当前对象、数据集与范围返回服务端计算的覆盖率与缺口。"
                action={
                  <Button size="sm" busy={busy} busyLabel="预览中…" disabled={!form.value.trim() || busy} onClick={preview}>
                    运行 Coverage 预览
                  </Button>
                }
              />
            ) : (
              <>
                <KeyValueList
                  items={[
                    { label: "链", value: previewSummary.chain },
                    { label: "覆盖率", value: <span className="num">{ratioText(previewSummary.overall_ratio)}</span> },
                    {
                      label: "Coverage HIT",
                      value: previewSummary.overall_ratio >= 1 ? "是（本地复用，无 Provider 费用）" : "否（存在缺口）",
                    },
                    { label: "Datasets", value: previewSummary.datasets.join(", ") || "—" },
                    { label: "Gap 数", value: <span className="num">{String(gapCount)}</span> },
                  ]}
                />
                <DataTable
                  columns={coverageColumns}
                  rows={coverageItems}
                  rowKey={(row) => `${row.subject}|${row.dataset}`}
                  caption="Coverage 预览明细（subject × dataset）"
                  empty={<EmptyState title="预览未返回明细行" />}
                />
              </>
            )}
          </Card>
        </PageSection>
      ) : null}

      <StickyActionBar leading={<span className="cs-section__count">{`第 ${stepIndex + 1} / ${STEPS.length} 步 · ${STEPS[stepIndex].label}`}</span>}>
        {stepIndex > 0 ? (
          <Button size="sm" onClick={() => setStep(STEPS[stepIndex - 1].key)}>
            上一步
          </Button>
        ) : null}
        {stepIndex < STEPS.length - 1 ? (
          <Button size="sm" variant="primary" onClick={nextStep}>
            下一步
          </Button>
        ) : (
          <Button size="sm" variant="primary" busy={busy} busyLabel="预览中…" disabled={!form.value.trim() || busy} onClick={preview}>
            Coverage 预览
          </Button>
        )}
      </StickyActionBar>
    </>
  );
}
