import {
  useCallback,
  useEffect,
  useState } from "react";
import { Link,
  useNavigate,
  useParams } from "react-router-dom";
import { useAuth } from "@chain/api-client";
import {
  Button,
  Card,
  DataTable,
  EmptyState,
  ErrorState,
  FilterBar,
  FilterChip,
  HashValue,
  InspectorDrawer,
  KeyValueList,
  MetaLine,
  PageSection,
  SearchInput,
  SkeletonRows,
  SplitPane,
  StatusBadge,
  dialogInputValue,
  statusToneFor,
  useDialog,
  type DataTableColumn,
} from "@chain/ui";
import { CsPageHeader } from "@chain/user-ui";
import { formatTime, useAction, useAsync } from "../sprint4/common";

interface EvidenceRef {
  id: string;
  title: string;
  status: string;
  content_hash: string;
  evidence_type: string;
}
interface Finding {
  id: string;
  investigation_id: string;
  finding_type: string;
  title: string;
  summary: string;
  confidence: number;
  status: string;
  nature: string;
  algorithm: string;
  algorithm_version: string;
  revision: number;
  review_note: string;
  created_at: string;
  evidence: EvidenceRef[];
}

/** 与 CanvasPage 相同的本地断点读取方式：只在需要切换结构（列表 / 全屏详情）时用 JS。 */
function useMediaQuery(query: string): boolean {
  const [matches, setMatches] = useState(
    () => typeof window !== "undefined" && window.matchMedia(query).matches,
  );
  useEffect(() => {
    if (typeof window === "undefined") return;
    const mql = window.matchMedia(query);
    const update = () => setMatches(mql.matches);
    update();
    mql.addEventListener("change", update);
    return () => mql.removeEventListener("change", update);
  }, [query]);
  return matches;
}

/** §18.1 U-INV-04：Findings 复核。支持 evidence verify 后再 confirm，算法字段只读。 */
export default function FindingsPage() {
  const { id = "" } = useParams();
  const { can } = useAuth();
  const dialog = useDialog();
  const action = useAction();
  const navigate = useNavigate();
  const [status, setStatus] = useState("all");
  const [findingType, setFindingType] = useState("");
  const [keyword, setKeyword] = useState("");
  const [knownTypes, setKnownTypes] = useState<string[]>([]);
  const { data, loading, error, reload } = useAsync<Finding[]>(`/api/v2/investigations/${id}/findings`, [id, status, findingType], {
    limit: 200,
    status: status === "all" ? undefined : status,
    type: findingType || undefined,
  });
  const [selected, setSelected] = useState<Finding | null>(null);
  const isNarrow = useMediaQuery("(max-width: 899px)");

  /* 类型下拉的选项来自服务端真实返回过的 finding_type，不在前端硬编码枚举。 */
  useEffect(() => {
    if (!data) return;
    setKnownTypes((prev) => {
      const next = [...new Set([...prev, ...data.map((f) => f.finding_type)])].sort();
      return next.length === prev.length ? prev : next;
    });
  }, [data]);

  const verifyEvidence = useCallback(
    async (e: EvidenceRef) => {
      const answer = await dialog.confirm({
        kind: "confirm",
        title: "标记 Evidence 为 verified？",
        body: "verified 是确认 Finding 的前置条件；原证据内容不可覆盖。",
        details: [
          { label: "Evidence", value: e.title },
          { label: "类型", value: e.evidence_type },
          { label: "SHA-256", value: e.content_hash },
        ],
      });
      if (answer !== "confirm") return;
      await action.run(() => action.client.post(`/api/v2/evidence/${e.id}/commands/verify`, { note: "Sprint 4 复核通过" }), {
        successTitle: "Evidence 已 verified",
        onDone: async () => {
          const refreshed = await action.client.get<Finding[]>(`/api/v2/investigations/${id}/findings`, { query: { limit: 200 } });
          setSelected(refreshed.data.find((f) => f.id === selected?.id) ?? null);
          void reload();
        },
      });
    },
    [action, dialog, id, reload, selected],
  );

  const decide = useCallback(
    async (finding: Finding, command: "confirm" | "reject" | "supersede") => {
      let note = "";
      if (command === "reject" || command === "supersede") {
        const input = dialogInputValue(await dialog.prompt({ title: command === "reject" ? "驳回 Finding" : "替代 Finding", input: { label: "原因", pattern: /^.{2,}$/ } }));
        if (!input) return;
        note = input;
      }
      const verified = finding.evidence.filter((e) => e.status === "verified").length;
      if (command === "confirm" && verified === 0) {
        const exception = dialogInputValue(await dialog.prompt({
          title: "缺少 verified Evidence",
          body: "确认 Finding 至少需要一条 verified Evidence，或填写经审批的例外原因。",
          input: { label: "例外原因", pattern: /^.{4,}$/ },
        }));
        if (!exception) return;
        note = exception;
      }
      const answer = await dialog.confirm({
        kind: "confirm",
        title: `确认 ${command} Finding？`,
        details: [
          { label: "Finding", value: finding.title },
          { label: "当前状态", value: finding.status },
          { label: "Evidence", value: `${verified}/${finding.evidence.length} verified` },
          { label: "revision", value: `r${finding.revision}` },
          { label: "意见", value: note || "—" },
        ],
      });
      if (answer !== "confirm") return;
      await action.run(
        () => action.client.post(`/api/v2/findings/${finding.id}/commands/${command}`, { note, exception_reason: command === "confirm" && verified === 0 ? note : "" }, { ifMatch: String(finding.revision) }),
        {
          successTitle: "Finding 状态已更新",
          onDone: async () => {
            const refreshed = await action.client.get<Finding[]>(`/api/v2/investigations/${id}/findings`, { query: { limit: 200 } });
            setSelected(refreshed.data.find((f) => f.id === finding.id) ?? null);
            void reload();
          },
        },
      );
    },
    [action, dialog, id, reload],
  );

  const loaded = data ?? [];
  const trimmed = keyword.trim().toLowerCase();
  const rows = trimmed
    ? loaded.filter((f) =>
        `${f.title} ${f.finding_type} ${f.nature} ${f.algorithm} ${f.status}`.toLowerCase().includes(trimmed),
      )
    : loaded;
  const hasFilters = status !== "all" || findingType !== "" || keyword !== "";

  const clearFilters = () => {
    setStatus("all");
    setFindingType("");
    setKeyword("");
  };

  const columns: Array<DataTableColumn<Finding>> = [
    { key: "title", header: "Finding", render: (row) => <span>{row.title}</span> },
    { key: "type", header: "类型", width: "180px", render: (row) => <span className="mono">{row.finding_type}</span> },
    { key: "nature", header: "nature", width: "110px", render: (row) => row.nature },
    {
      key: "confidence",
      header: "confidence",
      width: "110px",
      align: "right",
      render: (row) => <span className="num">{(row.confidence * 100).toFixed(0)}%</span>,
    },
    {
      key: "algorithm",
      header: "算法",
      width: "170px",
      render: (row) => <span className="mono">{`${row.algorithm}@${row.algorithm_version}`}</span>,
    },
    { key: "status", header: "状态", width: "140px", render: (row) => <StatusBadge tone={statusToneFor(row.status)}>{row.status}</StatusBadge> },
  ];

  const listPanel = (
    <PageSection title="Finding 列表" count={`${rows.length} / ${loaded.length} 条`}>
      <FilterBar onClear={clearFilters} showClear={hasFilters} ariaLabel="Finding 筛选">
        <select className="select" aria-label="状态筛选" value={status} onChange={(e) => setStatus(e.target.value)}>
          <option value="all">全部状态</option>
          <option value="candidate">candidate</option>
          <option value="confirmed">confirmed</option>
          <option value="rejected">rejected</option>
          <option value="superseded">superseded</option>
        </select>
        <select className="select" aria-label="类型筛选" value={findingType} onChange={(e) => setFindingType(e.target.value)}>
          <option value="">全部类型</option>
          {knownTypes.map((type) => (
            <option key={type} value={type}>
              {type}
            </option>
          ))}
        </select>
        <SearchInput
          label="关键字（当前结果内）"
          placeholder="标题 / 类型 / nature / 算法"
          value={keyword}
          onChange={setKeyword}
          busy={loading}
        />
      </FilterBar>
      {status !== "all" || findingType ? (
        <div className="cs-filterbar">
          {status !== "all" ? (
            <FilterChip removeLabel="移除状态筛选" onRemove={() => setStatus("all")}>
              状态：{status}
            </FilterChip>
          ) : null}
          {findingType ? (
            <FilterChip removeLabel="移除类型筛选" onRemove={() => setFindingType("")}>
              类型：{findingType}
            </FilterChip>
          ) : null}
        </div>
      ) : null}
      <Card flush>
        {loading && !data ? <SkeletonRows rows={6} columns={5} /> : null}
        {error && !data ? <ErrorState title="Findings 加载失败" message={error} onRetry={reload} /> : null}
        {data && rows.length === 0 ? (
          <EmptyState
            title="没有符合筛选条件的 Finding"
            body="筛选条件会保留，可直接修改；也可以清除全部筛选。"
            action={
              hasFilters ? (
                <Button size="sm" onClick={clearFilters}>
                  清除筛选
                </Button>
              ) : undefined
            }
          />
        ) : null}
        {rows.length > 0 ? (
          <DataTable
            columns={columns}
            rows={rows}
            rowKey={(row) => row.id}
            caption="Finding 列表"
            rowTestId={(row) => `finding-row-${row.id}`}
            rowActivateLabel={(row) => `查看 Finding ${row.title}`}
            onRowActivate={(row) => setSelected(row)}
          />
        ) : null}
      </Card>
    </PageSection>
  );

  const detailPanel = selected ? (
    <InspectorDrawer
      title={selected.title}
      subtitle={`${selected.finding_type} · r${selected.revision}`}
      ariaLabel="Finding 详情"
      onClose={() => setSelected(null)}
      actions={
        can("finding:review") ? (
          <>
            {isNarrow ? (
              <Button size="sm" onClick={() => setSelected(null)}>
                返回列表
              </Button>
            ) : null}
            <button type="button" data-testid="finding-confirm" className="btn btn--primary btn--sm" disabled={["confirmed", "rejected", "superseded"].includes(selected.status)} onClick={() => decide(selected, "confirm")}>
              Confirm
            </button>
            <button type="button" data-testid="finding-reject" className="btn btn--sm" disabled={["confirmed", "rejected", "superseded"].includes(selected.status)} onClick={() => decide(selected, "reject")}>
              Reject
            </button>
            <button type="button" data-testid="finding-supersede" className="btn btn--danger btn--sm" disabled={selected.status === "superseded"} onClick={() => decide(selected, "supersede")}>
              Supersede
            </button>
          </>
        ) : null
      }
    >
      <div style={{ display: "flex", flexDirection: "column", gap: "var(--sp-4)" }}>
        <KeyValueList
          items={[
            { label: "摘要", value: selected.summary || "—", wide: true },
            { label: "状态", value: <StatusBadge tone={statusToneFor(selected.status)}>{selected.status}</StatusBadge> },
            { label: "类型", value: <span className="mono">{selected.finding_type}</span> },
            { label: "nature", value: selected.nature },
            { label: "confidence", value: <span className="num">{(selected.confidence * 100).toFixed(1)}%</span> },
            { label: "算法", value: <span className="mono">{`${selected.algorithm}@${selected.algorithm_version}`}</span> },
            { label: "revision", value: `r${selected.revision}` },
            { label: "创建", value: formatTime(selected.created_at) },
            { label: "复核意见", value: selected.review_note || "—", wide: true },
          ]}
        />
        <PageSection title="引用 Evidence" count={`${selected.evidence.length} 条`}>
          {selected.evidence.length === 0 ? <EmptyState title="没有关联 Evidence" /> : null}
          {selected.evidence.length > 0 ? (
            <div className="cs-rowcards">
              {selected.evidence.slice(0, 8).map((e) => (
                <div className="cs-rowcard" key={e.id}>
                  <div className="cs-rowcard__head">
                    <span className="cs-rowcard__title">{e.title}</span>
                    <StatusBadge tone={statusToneFor(e.status)}>{e.status}</StatusBadge>
                  </div>
                  <div className="cs-rowcard__meta">
                    <span className="mono">{e.evidence_type}</span>
                    <HashValue value={e.content_hash} head={12} tail={8} label="内容哈希" />
                  </div>
                  <div className="cs-rowcard__actions">
                    <Link className="btn btn--sm" to={`/investigations/${selected.investigation_id}/evidence/${e.id}`}>
                      详情
                    </Link>
                    {can("evidence:review") && e.status !== "verified" ? (
                      <button type="button" data-testid={`finding-evidence-verify-${e.id}`} className="btn btn--sm" onClick={() => verifyEvidence(e)}>
                        Verify
                      </button>
                    ) : null}
                  </div>
                </div>
              ))}
            </div>
          ) : null}
          {selected.evidence.length > 8 ? (
            <Button
              size="sm"
              onClick={() =>
                dialog.notify(
                  "info",
                  `其余 ${selected.evidence.length - 8} 条 Evidence`,
                  null,
                  selected.evidence.slice(8).map((e) => ({ label: `${e.evidence_type} · ${e.status}`, value: e.title })),
                )
              }
            >
              查看其余 {selected.evidence.length - 8} 条
            </Button>
          ) : null}
        </PageSection>
      </div>
    </InspectorDrawer>
  ) : (
    <InspectorDrawer title="Finding 详情" ariaLabel="Finding 详情">
      <EmptyState title="选择左侧 Finding" />
    </InspectorDrawer>
  );

  return (
    <>
      <CsPageHeader
        title="Findings 复核"
        breadcrumb={[
          { label: "调查任务", to: "/investigations" },
          { label: "工作台", to: `/investigations/${id}/workbench` },
          { label: "Findings 复核" },
        ]}
        status={<StatusBadge tone="info">{`${rows.length} 条结果`}</StatusBadge>}
        meta={
          <MetaLine
            items={[
              { label: "调查", value: <span className="mono">{id}</span> },
              { label: "数据源", value: "postgres:investigation_findings" },
              { label: "已加载", value: `${loaded.length} 条` },
            ]}
          />
        }
        secondaryAction={
          <>
            <Button size="sm" onClick={() => navigate(`/investigations/${id}/workbench`)}>
              返回工作台
            </Button>
            <Button size="sm" variant="ghost" busy={loading} onClick={reload}>
              刷新
            </Button>
          </>
        }
      />

      {isNarrow ? (
        selected ? detailPanel : listPanel
      ) : (
        <SplitPane main={listPanel} aside={detailPanel} storageKey="cs-findings-split" defaultAsideWidth={420} minAsideWidth={320} maxAsideWidth={620} asideLabel="Finding 详情" />
      )}
    </>
  );
}
