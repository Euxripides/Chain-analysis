import { useCallback, useEffect, useMemo, useState } from "react";
import { useNavigate, useSearchParams } from "react-router-dom";
import { ApiError } from "@chain/api-client";
import {
  Card,
  DataTable,
  EmptyState,
  ErrorState,
  PageHeader,
  SkeletonRows,
  StatusBadge,
  statusToneFor,
  useDialog,
} from "@chain/ui";
import { formatTime, shortValue, useApi, useS5Action } from "../sprint5/common";

/**
 * U-GRAPH-01 / S5-FE-03：Canvas 入口（路由 /graph）。
 * 案件 → 调查 → 画布：Canvas 只能在调查下创建，列表按案件聚合该案件全部调查的画布。
 * 所有说明、错误与确认只通过 Dialog 呈现（AGENTS.md / docs/code-standards.md）。
 */

interface CaseItem {
  id: string;
  name: string;
  case_no?: string;
  status?: string;
  workspace_id?: string;
}

interface InvestigationItem {
  id: string;
  goal: string;
  status: string;
  case_id?: string | null;
}

interface CanvasRow {
  id: string;
  name: string;
  revision: number;
  updated_at?: string;
  parent_canvas_id?: string | null;
  case_id?: string | null;
  investigation_id?: string | null;
}

interface ListEnvelope<T> {
  items: T[];
}

const EMPTY_STATE = {
  node_refs: [] as string[],
  edge_refs: [] as string[],
  groups: [] as Array<Record<string, unknown>>,
  annotations: [] as Array<Record<string, unknown>>,
  filters: {} as Record<string, unknown>,
  layout: {} as Record<string, unknown>,
  camera: {} as Record<string, unknown>,
  view_state: {} as Record<string, unknown>,
};

export default function CanvasListPage() {
  const navigate = useNavigate();
  const dialog = useDialog();
  const [params] = useSearchParams();
  const { client, run, busy } = useS5Action();

  const cases = useApi<ListEnvelope<CaseItem>>("/api/v2/cases", [], { limit: 200 });
  const investigations = useApi<ListEnvelope<InvestigationItem>>("/api/v2/investigations", [], { limit: 200 });

  const [selectedCase, setSelectedCase] = useState(() => params.get("case_id") ?? params.get("case") ?? "");
  const [selectedInvestigation, setSelectedInvestigation] = useState(() => params.get("investigation") ?? "");
  const [canvases, setCanvases] = useState<CanvasRow[]>([]);
  const [canvasesLoading, setCanvasesLoading] = useState(false);
  const [canvasesError, setCanvasesError] = useState<string | null>(null);

  const caseItems = cases.data?.items ?? [];
  const activeCase = selectedCase || caseItems[0]?.id || "";
  const caseInvestigations = useMemo(
    () => (investigations.data?.items ?? []).filter((item) => item.case_id === activeCase),
    [investigations.data, activeCase],
  );
  const activeInvestigation = selectedInvestigation || caseInvestigations[0]?.id || "";

  useEffect(() => {
    if (caseItems.length === 0) return;
    if (!selectedCase || !caseItems.some((item) => item.id === selectedCase)) {
      setSelectedCase(caseItems[0].id);
    }
  }, [caseItems, selectedCase]);

  useEffect(() => {
    if (caseInvestigations.length === 0) {
      if (selectedInvestigation) setSelectedInvestigation("");
      return;
    }
    if (!caseInvestigations.some((item) => item.id === selectedInvestigation)) {
      setSelectedInvestigation(caseInvestigations[0].id);
    }
  }, [caseInvestigations, selectedInvestigation]);

  /** 画布属于调查：按案件下全部调查聚合，列表列出来源调查。 */
  const loadCanvases = useCallback(async () => {
    if (!activeCase) {
      setCanvases([]);
      setCanvasesLoading(false);
      return;
    }
    if (caseInvestigations.length === 0) {
      setCanvases([]);
      setCanvasesLoading(false);
      return;
    }
    setCanvasesLoading(true);
    setCanvasesError(null);
    try {
      const results = await Promise.all(
        caseInvestigations.map((item) =>
          client.get<ListEnvelope<CanvasRow>>(`/api/v2/investigations/${encodeURIComponent(item.id)}/canvases`),
        ),
      );
      const merged: CanvasRow[] = [];
      results.forEach((result, index) => {
        for (const canvas of result.data?.items ?? []) {
          merged.push({ ...canvas, investigation_id: canvas.investigation_id ?? caseInvestigations[index].id });
        }
      });
      merged.sort((a, b) => String(b.updated_at ?? "").localeCompare(String(a.updated_at ?? "")));
      setCanvases(merged);
    } catch (error) {
      if (error instanceof ApiError) setCanvasesError(error.userMessage);
      else setCanvasesError("读取画布列表失败");
    } finally {
      setCanvasesLoading(false);
    }
  }, [client, activeCase, caseInvestigations]);

  useEffect(() => {
    void loadCanvases();
  }, [loadCanvases]);

  const createCanvas = async () => {
    if (!activeInvestigation) {
      dialog.notify("warning", "请先选择调查", "画布必须挂在调查下创建。");
      return;
    }
    const name = await dialog.prompt({
      title: "新建画布",
      input: { label: "名称", placeholder: "例如：资金流向主画布", pattern: /^.{1,120}$/, patternHint: "1-120 个字符" },
      submitLabel: "创建",
    });
    const trimmed = (name ?? "").trim();
    if (!trimmed) return;
    const investigation = caseInvestigations.find((item) => item.id === activeInvestigation);
    await run(
      () =>
        client.post<CanvasRow>(`/api/v2/investigations/${encodeURIComponent(activeInvestigation)}/canvases`, {
          name: trimmed,
          ...EMPTY_STATE,
        }),
      {
        successTitle: "画布已创建",
        successBody: `调查「${investigation?.goal ?? activeInvestigation}」下的画布已建立。`,
        onDone: () => {
          void loadCanvases();
        },
      },
    );
  };

  const loading = cases.loading || investigations.loading;
  const loadError = cases.error ?? investigations.error;
  const noCase = !loading && !loadError && caseItems.length === 0;
  const noInvestigation = !loading && !loadError && caseItems.length > 0 && caseInvestigations.length === 0;

  const explainEmpty = () => {
    dialog.notify("info", "Canvas 可用性说明", "Canvas 归属案件内的调查，创建前需要同时具备可访问案件与调查。", [
      { label: "案件接口", value: "/api/v2/cases?limit=200" },
      { label: "调查接口", value: "/api/v2/investigations?limit=200" },
      { label: "所需权限", value: "canvas:list / canvas:create" },
      { label: "当前可访问案件", value: String(caseItems.length) },
      { label: "当前案件可访问调查", value: String(caseInvestigations.length) },
    ]);
  };

  const selectedCaseItem = caseItems.find((item) => item.id === activeCase);

  return (
    <>
      <PageHeader
        title="资金图谱"
        subtitle={`Canvas 归属调查 · 来源 /api/v2/cases · /api/v2/investigations`}
        actions={
          <>
            <button className="btn btn--sm" onClick={() => void loadCanvases()} disabled={!activeCase}>
              刷新
            </button>
            <button className="btn btn--primary btn--sm" onClick={() => void createCanvas()} disabled={busy || !activeInvestigation}>
              新建画布
            </button>
          </>
        }
      />

      {loading ? (
        <SkeletonRows rows={4} columns={3} />
      ) : loadError ? (
        <Card>
          <ErrorState
            title="案件/调查加载失败"
            message={loadError}
            onRetry={() => {
              cases.reload();
              investigations.reload();
            }}
          />
        </Card>
      ) : noCase || noInvestigation ? (
        <Card>
          <EmptyState
            title={noCase ? "没有可访问的案件" : "该案件没有可访问的调查"}
            body="数据来源 /api/v2/cases · /api/v2/investigations"
            action={
              <button type="button" className="btn btn--sm" onClick={explainEmpty}>
                查看说明
              </button>
            }
          />
        </Card>
      ) : (
        <>
          <Card title="选择范围">
            <div className="s5-form-grid">
              <label className="s5-field-label">
                案件
                <select
                  className="select"
                  aria-label="案件"
                  value={activeCase}
                  onChange={(event) => {
                    setSelectedCase(event.target.value);
                    setSelectedInvestigation("");
                  }}
                >
                  {caseItems.map((item) => (
                    <option key={item.id} value={item.id}>
                      {item.case_no ? `${item.case_no} · ` : ""}
                      {item.name}
                    </option>
                  ))}
                </select>
              </label>
              <label className="s5-field-label">
                调查
                <select
                  className="select"
                  aria-label="调查"
                  value={activeInvestigation}
                  onChange={(event) => setSelectedInvestigation(event.target.value)}
                >
                  {caseInvestigations.map((item) => (
                    <option key={item.id} value={item.id}>
                      {item.goal || item.id}
                    </option>
                  ))}
                </select>
              </label>
            </div>
          </Card>

          <Card title={`画布（${canvases.length}）`} flush>
            <DataTable<CanvasRow>
              loading={canvasesLoading}
              error={canvasesError}
              onRetry={() => void loadCanvases()}
              rows={canvases}
              rowKey={(row) => row.id}
              empty={<EmptyState title="该案件还没有画布" />}
              columns={[
                {
                  key: "name",
                  header: "名称",
                  render: (row) => (
                    <a
                      href={`/canvases/${row.id}`}
                      onClick={(event) => {
                        event.preventDefault();
                        navigate(`/canvases/${row.id}`);
                      }}
                    >
                      <strong>{row.name}</strong>
                    </a>
                  ),
                },
                {
                  key: "revision",
                  header: "revision",
                  width: "100px",
                  render: (row) => <span className="num">r{row.revision}</span>,
                },
                {
                  key: "updated",
                  header: "更新时间",
                  width: "180px",
                  render: (row) => <span>{formatTime(row.updated_at)}</span>,
                },
                {
                  key: "source",
                  header: "分支来源",
                  width: "190px",
                  render: (row) =>
                    row.parent_canvas_id ? (
                      <span className="mono" style={{ fontSize: "var(--fs-11)" }}>
                        分支自 {shortValue(row.parent_canvas_id, 8, 4)}
                      </span>
                    ) : (
                      <StatusBadge tone="neutral">主线</StatusBadge>
                    ),
                },
                {
                  key: "investigation",
                  header: "调查",
                  width: "220px",
                  render: (row) => {
                    const investigation = (investigations.data?.items ?? []).find((item) => item.id === row.investigation_id);
                    return (
                      <span title={row.investigation_id ?? ""}>
                        {investigation?.goal || shortValue(row.investigation_id ?? "", 8, 4)}
                      </span>
                    );
                  },
                },
                {
                  key: "actions",
                  header: "操作",
                  width: "100px",
                  align: "right",
                  render: (row) => (
                    <button
                      type="button"
                      className="btn btn--sm"
                      onClick={() => navigate(`/canvases/${row.id}`)}
                    >
                      打开
                    </button>
                  ),
                },
              ]}
            />
          </Card>

          <Card title="案件状态">
            <div className="s5-actions">
              <StatusBadge tone={selectedCaseItem?.status ? statusToneFor(selectedCaseItem.status) : "neutral"}>
                {selectedCaseItem?.status ?? "status 未返回"}
              </StatusBadge>
              <span className="mono" style={{ fontSize: "var(--fs-12)" }}>
                {selectedCaseItem?.case_no ?? shortValue(selectedCaseItem?.id ?? "", 8, 4)}
              </span>
            </div>
          </Card>
        </>
      )}
    </>
  );
}
