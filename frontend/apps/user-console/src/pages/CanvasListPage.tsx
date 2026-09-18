import { useCallback, useEffect, useMemo, useState } from "react";
import { useNavigate, useSearchParams } from "react-router-dom";
import { ApiError } from "@chain/api-client";
import {
  Button,
  Card,
  DataTable,
  EmptyState,
  ErrorState,
  FilterBar,
  FilterChip,
  HashValue,
  MetaLine,
  PageHeader,
  PageSection,
  RelativeTime,
  SearchInput,
  SkeletonRows,
  SortMenu,
  StatusBadge,
  statusToneFor,
  useDialog,
} from "@chain/ui";
import { formatTime, useApi, useS5Action } from "../sprint5/common";

/**
 * U-GRAPH-01 / S5-FE-03：Canvas 入口（路由 /graph）。
 *
 * 案件 → 调查 → 画布：Canvas 只能在调查下创建，列表按案件聚合该案件全部调查的画布。
 * 所有说明、错误与确认只通过 Dialog 呈现（AGENTS.md / docs/code-standards.md）。
 *
 * 数据来源（全部为接口真实返回，不做前端推算）：
 *   - 案件         GET /api/v2/cases?limit=200
 *   - 调查         GET /api/v2/investigations?limit=200
 *   - 画布         GET /api/v2/investigations/{id}/canvases（每个调查一次，按画布 id 合并）
 *   画布行的 node_refs / edge_refs / revision / created_at / updated_at 均由该接口返回；
 *   节点数、边数直接取数组长度，不额外猜测。
 *
 * 表现层（蓝图 §12.12）：页头 + 筛选栏 + 最近画布卡片（静态预览，无持续动画）+ 完整结果表格。
 * 完全结果的排序与名称筛选只作用于**已取回的行**，不改动任何请求参数。
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
  created_at?: string | null;
  updated_at?: string | null;
  parent_canvas_id?: string | null;
  case_id?: string | null;
  investigation_id?: string | null;
  node_refs?: string[] | null;
  edge_refs?: string[] | null;
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

/** 最近画布卡片的数量：卡片只用于"最近"，完整结果始终在表格里（蓝图 §12.12）。 */
const RECENT_CARD_LIMIT = 3;

type SortKey = "updated_at" | "created_at" | "name" | "revision" | "nodes";

const SORT_OPTIONS = [
  { key: "updated_at", label: "更新时间" },
  { key: "created_at", label: "创建时间" },
  { key: "name", label: "名称" },
  { key: "revision", label: "revision" },
  { key: "nodes", label: "节点数" },
];

const NODE_KIND_LABEL: Record<string, string> = { address: "地址", tx: "交易", evidence: "证据" };
const NODE_KIND_COLOR: Record<string, string> = {
  address: "var(--c-cobalt, #175cd3)",
  tx: "var(--c-neutral, #667085)",
  evidence: "var(--c-amber-text, #b45309)",
};

function nodeCount(row: CanvasRow): number {
  return row.node_refs?.length ?? 0;
}

function edgeCount(row: CanvasRow): number {
  return row.edge_refs?.length ?? 0;
}

/** 按 ref 前缀（address / tx / evidence）统计节点类型，前缀就是 ref 约定的第一部分。 */
function kindCounts(row: CanvasRow): Array<{ key: string; label: string; value: number }> {
  const counts: Record<string, number> = { address: 0, tx: 0, evidence: 0, other: 0 };
  for (const ref of row.node_refs ?? []) {
    if (typeof ref !== "string") continue;
    const kind = ref.slice(0, Math.max(0, ref.indexOf(":"))).toLowerCase();
    if (kind === "address" || kind === "tx" || kind === "evidence") counts[kind] += 1;
    else counts.other += 1;
  }
  /* 显式标注为宽类型：后面要追加 `other` 桶，用 `as const` 推出来的字面量联合放不下它。 */
  const out: Array<{ key: string; label: string; value: number }> = (["address", "tx", "evidence"] as const)
    .map((kind) => ({ key: kind as string, label: NODE_KIND_LABEL[kind], value: counts[kind] }))
    .filter((item) => item.value > 0);
  if (counts.other > 0) out.push({ key: "other", label: "未识别引用", value: counts.other });
  return out;
}

/**
 * 静态节点类型条：只按 node_refs 的真实前缀计数，一次绘制、不播放任何动画（蓝图 §12.12）。
 * 具体数值同时以文字给出，颜色不是唯一通道。
 */
function KindBar({ counts, label }: { counts: Array<{ key: string; label: string; value: number }>; label: string }) {
  const total = counts.reduce((sum, item) => sum + item.value, 0);
  if (total === 0) return null;
  let offset = 0;
  return (
    <svg
      viewBox="0 0 100 4"
      preserveAspectRatio="none"
      width="100%"
      height="4"
      role="img"
      aria-label={label}
      style={{ display: "block" }}
    >
      {counts.map((item) => {
        const width = (item.value / total) * 100;
        const rect = (
          <rect key={item.key} x={offset} y={0} width={width} height={4} fill={NODE_KIND_COLOR[item.key] ?? "#98A2B3"} />
        );
        offset += width;
        return rect;
      })}
    </svg>
  );
}

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
  const [nameQuery, setNameQuery] = useState("");
  const [structure, setStructure] = useState<"all" | "main" | "branch">("all");
  const [sortKey, setSortKey] = useState<SortKey>("updated_at");
  const [sortDirection, setSortDirection] = useState<"asc" | "desc">("desc");

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
      const seen = new Set<string>();
      results.forEach((result, index) => {
        for (const canvas of result.data?.items ?? []) {
          /* 该端点按案件返回画布，每个调查都会取回同一批行：按画布 id 去重，避免重复展示。 */
          if (seen.has(canvas.id)) continue;
          seen.add(canvas.id);
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
  const caseLabel = selectedCaseItem
    ? `${selectedCaseItem.case_no ? selectedCaseItem.case_no + " · " : ""}${selectedCaseItem.name}`
    : "—";

  const filtersActive = nameQuery.trim() !== "" || structure !== "all";

  /** 名称与结构筛选 + 排序都只作用于已取回的行，不改变任何请求。 */
  const rows = useMemo(() => {
    const query = nameQuery.trim().toLowerCase();
    const filtered = canvases.filter((row) => {
      if (structure === "main" && row.parent_canvas_id) return false;
      if (structure === "branch" && !row.parent_canvas_id) return false;
      if (!query) return true;
      return (
        row.name.toLowerCase().includes(query) ||
        row.id.toLowerCase().includes(query) ||
        String(row.investigation_id ?? "").toLowerCase().includes(query)
      );
    });
    const direction = sortDirection === "asc" ? 1 : -1;
    return [...filtered].sort((a, b) => {
      let compare = 0;
      if (sortKey === "name") compare = a.name.localeCompare(b.name, "zh-CN");
      else if (sortKey === "revision") compare = a.revision - b.revision;
      else if (sortKey === "nodes") compare = nodeCount(a) - nodeCount(b);
      else if (sortKey === "created_at") compare = String(a.created_at ?? "").localeCompare(String(b.created_at ?? ""));
      else compare = String(a.updated_at ?? "").localeCompare(String(b.updated_at ?? ""));
      return compare * direction;
    });
  }, [canvases, nameQuery, structure, sortKey, sortDirection]);

  /** 卡片只呈现"最近"，因此固定按更新时间倒序取前几个，与当前排序无关。 */
  const recentRows = useMemo(() => {
    const query = nameQuery.trim().toLowerCase();
    return [...canvases]
      .filter((row) => {
        if (structure === "main" && row.parent_canvas_id) return false;
        if (structure === "branch" && !row.parent_canvas_id) return false;
        if (!query) return true;
        return row.name.toLowerCase().includes(query) || row.id.toLowerCase().includes(query);
      })
      .sort((a, b) => String(b.updated_at ?? "").localeCompare(String(a.updated_at ?? "")))
      .slice(0, RECENT_CARD_LIMIT);
  }, [canvases, nameQuery, structure]);

  const clearFilters = () => {
    setNameQuery("");
    setStructure("all");
  };

  const investigationLabel = (row: CanvasRow): string => {
    const investigation = (investigations.data?.items ?? []).find((item) => item.id === row.investigation_id);
    return investigation?.goal || row.investigation_id || "—";
  };

  return (
    <>
      <PageHeader
        title="资金图谱"
        breadcrumb={[{ label: "工作台", to: "/home" }, { label: "资金图谱" }]}
        status={
          selectedCaseItem ? (
            <StatusBadge tone={statusToneFor(selectedCaseItem.status ?? "")}>
              {selectedCaseItem.status ?? "status 未返回"}
            </StatusBadge>
          ) : null
        }
        meta={
          <MetaLine
            items={[
              { label: "当前案件", value: caseLabel },
              { label: "可访问调查", value: `${caseInvestigations.length} 个` },
              { label: "画布", value: `${canvases.length} 个` },
              { label: "数据来源", value: "/api/v2/cases · /api/v2/investigations" },
            ]}
          />
        }
        primaryAction={
          <Button
            variant="primary"
            size="sm"
            busy={busy}
            busyLabel="创建中…"
            onClick={() => void createCanvas()}
            disabled={!activeInvestigation}
          >
            新建画布
          </Button>
        }
        secondaryAction={
          <Button size="sm" onClick={() => void loadCanvases()} disabled={!activeCase}>
            刷新
          </Button>
        }
      />

      {loading ? (
        <Card>
          <SkeletonRows rows={4} columns={3} widths={["32%", "18%", "50%"]} />
        </Card>
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
              <Button size="sm" onClick={explainEmpty}>
                查看说明
              </Button>
            }
          />
        </Card>
      ) : (
        <>
          <PageSection title="范围" count={`${caseInvestigations.length} 个调查`}>
            <Card>
              <FilterBar
                onClear={clearFilters}
                showClear={filtersActive}
                ariaLabel="画布筛选与排序"
              >
                <label className="cs-range__field" style={{ flex: "0 1 220px" }}>
                  <span className="cs-range__label">案件</span>
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
                <label className="cs-range__field" style={{ flex: "0 1 220px" }}>
                  <span className="cs-range__label">调查</span>
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
                <FilterChip active={structure === "all"} onClick={() => setStructure("all")}>
                  全部
                </FilterChip>
                <FilterChip active={structure === "main"} onClick={() => setStructure("main")}>
                  主线
                </FilterChip>
                <FilterChip active={structure === "branch"} onClick={() => setStructure("branch")}>
                  分支
                </FilterChip>
                <SearchInput
                  value={nameQuery}
                  onChange={setNameQuery}
                  label="筛选画布名称"
                  placeholder="筛选名称 / ID"
                />
                <SortMenu
                  options={SORT_OPTIONS}
                  value={sortKey}
                  onChange={(key) => setSortKey(key as SortKey)}
                  direction={sortDirection}
                  onDirectionChange={setSortDirection}
                  ariaLabel="画布排序字段"
                />
              </FilterBar>
            </Card>
          </PageSection>

          {canvasesError ? (
            <PageSection title="画布">
              <Card>
                <ErrorState title="画布列表加载失败" message={canvasesError} onRetry={() => void loadCanvases()} />
              </Card>
            </PageSection>
          ) : canvasesLoading ? (
            <PageSection title="画布">
              <Card>
                <SkeletonRows rows={4} columns={6} widths={["26%", "16%", "16%", "10%", "10%", "22%"]} />
              </Card>
            </PageSection>
          ) : canvases.length === 0 ? (
            <PageSection title="画布" count="0 个">
              <Card>
                <EmptyState title="该案件还没有画布" />
              </Card>
            </PageSection>
          ) : (
            <>
              <PageSection title="最近画布" count={`${recentRows.length} / ${canvases.length} 个`}>
                <div className="cs-rowcards cs-stagger">
                  {recentRows.map((row) => {
                    const counts = kindCounts(row);
                    return (
                      <div className="cs-rowcard" key={row.id}>
                        <div className="cs-rowcard__head">
                          <a
                            className="cs-rowcard__title"
                            href={`/canvases/${row.id}`}
                            onClick={(event) => {
                              event.preventDefault();
                              navigate(`/canvases/${row.id}`);
                            }}
                          >
                            {row.name}
                          </a>
                          {row.parent_canvas_id ? (
                            <StatusBadge tone="info">分支</StatusBadge>
                          ) : (
                            <StatusBadge tone="neutral">主线</StatusBadge>
                          )}
                        </div>
                        <div className="cs-rowcard__meta">
                          <span title={row.case_id ?? ""}>{caseLabel}</span>
                          <span>节点 {nodeCount(row)}</span>
                          <span>边 {edgeCount(row)}</span>
                          <span className="num">r{row.revision}</span>
                          <RelativeTime value={row.updated_at} />
                        </div>
                        <div className="cs-rowcard__meta">
                          {counts.length > 0 ? (
                            counts.map((item) => (
                              <span key={item.key}>
                                {item.label} {item.value}
                              </span>
                            ))
                          ) : (
                            <span>还没有节点</span>
                          )}
                        </div>
                        <KindBar
                          counts={counts}
                          label={`节点类型构成：${counts.map((item) => `${item.label} ${item.value}`).join("，") || "空"}`}
                        />
                        <div className="cs-rowcard__actions">
                          <Button size="sm" onClick={() => navigate(`/canvases/${row.id}`)}>
                            进入画布
                          </Button>
                        </div>
                      </div>
                    );
                  })}
                </div>
              </PageSection>

              <PageSection title="全部画布" count={`${rows.length} / ${canvases.length} 个`}>
                <Card flush>
                  <DataTable<CanvasRow>
                    caption="该案件下的全部画布"
                    rows={rows}
                    rowKey={(row) => row.id}
                    empty={
                      <EmptyState
                        title="当前筛选没有匹配的画布"
                        action={
                          <Button size="sm" onClick={clearFilters}>
                            重置筛选条件
                          </Button>
                        }
                      />
                    }
                    columns={[
                      {
                        key: "name",
                        header: "名称",
                        frozen: true,
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
                        key: "case",
                        header: "关联案件",
                        width: "200px",
                        render: (row) => {
                          const rowCase = caseItems.find((item) => item.id === row.case_id) ?? selectedCaseItem ?? null;
                          const label = rowCase?.case_no ? `${rowCase.case_no} · ${rowCase.name}` : rowCase?.name ?? "—";
                          return <span title={rowCase ? label : row.case_id ?? ""}>{label}</span>;
                        },
                      },
                      {
                        key: "investigation",
                        header: "调查",
                        width: "200px",
                        render: (row) => (
                          <span title={row.investigation_id ?? ""}>{investigationLabel(row)}</span>
                        ),
                      },
                      {
                        key: "nodes",
                        header: "节点",
                        width: "84px",
                        align: "right",
                        render: (row) => <span className="num">{nodeCount(row)}</span>,
                      },
                      {
                        key: "edges",
                        header: "边",
                        width: "76px",
                        align: "right",
                        render: (row) => <span className="num">{edgeCount(row)}</span>,
                      },
                      {
                        key: "revision",
                        header: "revision",
                        width: "96px",
                        render: (row) => <span className="num">r{row.revision}</span>,
                      },
                      {
                        key: "source",
                        header: "分支来源",
                        width: "200px",
                        render: (row) =>
                          row.parent_canvas_id ? (
                            <span className="toolbar">
                              <span>分支自</span>
                              <HashValue value={row.parent_canvas_id} head={8} tail={4} label="来源画布 ID" />
                            </span>
                          ) : (
                            <StatusBadge tone="neutral">主线</StatusBadge>
                          ),
                      },
                      {
                        key: "created",
                        header: "创建时间",
                        width: "180px",
                        render: (row) => <span>{formatTime(row.created_at)}</span>,
                      },
                      {
                        key: "updated",
                        header: "更新时间",
                        width: "170px",
                        render: (row) => <RelativeTime value={row.updated_at} />,
                      },
                      {
                        key: "actions",
                        header: "操作",
                        width: "100px",
                        align: "right",
                        render: (row) => (
                          <Button size="sm" onClick={() => navigate(`/canvases/${row.id}`)}>
                            打开
                          </Button>
                        ),
                      },
                    ]}
                  />
                </Card>
              </PageSection>
            </>
          )}
        </>
      )}
    </>
  );
}
