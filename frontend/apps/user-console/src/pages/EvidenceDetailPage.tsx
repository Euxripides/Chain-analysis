import { useCallback, useState } from "react";
import { Link, useNavigate, useParams } from "react-router-dom";
import { useAuth } from "@chain/api-client";
import {
  Button,
  Card,
  EmptyState,
  ErrorState,
  EvidenceRail,
  HashValue,
  InspectorDrawer,
  KeyValueList,
  MetaLine,
  PageHeader,
  PageSection,
  PageSkeleton,
  SplitPane,
  StatusBadge,
  StickyActionBar,
  dialogInputValue,
  statusToneFor,
  useDialog,
} from "@chain/ui";
import { formatTime, useAction, useAsync } from "../sprint4/common";

interface Evidence {
  id: string;
  investigation_id: string;
  evidence_type: string;
  title: string;
  status: string;
  nature: string;
  source_refs: string[];
  content_hash: string;
  object_ref?: string;
  provenance: Record<string, unknown>;
  version: number;
  revision: number;
  supersedes_id?: string | null;
  superseded_by?: string | null;
  verified_by?: string | null;
  verified_at?: string | null;
  created_at: string;
}

/** §18.1 U-INV-05：Evidence 详情。内容不可覆盖，修正通过 supersede 生成新版本。 */
export default function EvidenceDetailPage() {
  const { id = "", evidenceId = "" } = useParams();
  const { can } = useAuth();
  const dialog = useDialog();
  const action = useAction();
  const navigate = useNavigate();
  const [showRaw, setShowRaw] = useState(false);
  const includeSensitive = can("evidence:sensitive:read") ? "?include=sensitive" : "";
  const { data, loading, error, reload } = useAsync<Evidence>(`/api/v2/evidence/${evidenceId}${includeSensitive}`, [evidenceId]);

  const command = useCallback(
    async (cmd: "verify" | "dispute" | "supersede") => {
      let note = "";
      if (cmd === "dispute") {
        const input = dialogInputValue(await dialog.prompt({ title: "质疑 Evidence", input: { label: "原因", pattern: /^.{2,}$/ } }));
        if (!input) return;
        note = input;
      }
      const answer = await dialog.confirm({
        kind: "confirm",
        title: `确认执行 ${cmd}？`,
        details: [
          { label: "Evidence", value: data?.title ?? "" },
          { label: "当前状态", value: data?.status ?? "" },
          { label: "内容哈希", value: data?.content_hash ?? "" },
          ...(note ? [{ label: "原因", value: note }] : []),
        ],
      });
      if (answer !== "confirm") return;
      await action.run(() => action.client.post(`/api/v2/evidence/${evidenceId}/commands/${cmd}`, { note, reason: note }), {
        successTitle: "Evidence 状态已更新",
        onDone: reload,
      });
    },
    [action, data, dialog, evidenceId, reload],
  );

  /* 对象引用正文只在授予 evidence:sensitive:read 时由服务端返回；
     这条说明属于提示内容，只能通过 Dialog 呈现（AGENTS.md / ADR-009）。 */
  const explainObjectRef = () => {
    dialog.notify("info", "对象引用说明", null, [
      { label: "字段", value: "object_ref（Evidence 的对象引用正文）" },
      { label: "返回条件", value: "当前会话缺少 evidence:sensitive:read 权限时，服务端只返回元数据，该字段为空" },
      { label: "内容哈希", value: data?.content_hash ?? "—" },
    ]);
  };

  if (loading && !data) return <PageSkeleton shape="detail" rows={4} />;
  if (error && !data) return <Card><ErrorState title="Evidence 加载失败" message={error} onRetry={reload} /></Card>;
  if (!data) return <PageSkeleton shape="detail" rows={4} />;

  /* provenance 的键由服务端给出；这里只原样展示，不做任何派生或推算。 */
  const provenance: Record<string, unknown> = data.provenance ?? {};
  const algorithmRaw: unknown = provenance["algorithm_version"];
  const algorithmVersion = typeof algorithmRaw === "string" ? algorithmRaw : "";
  const provenanceEntries = Object.entries(provenance);
  const rawJson = JSON.stringify(data, null, 2);

  const metaItems = [
    { label: "类型", value: <span className="mono">{data.evidence_type}</span> },
    { label: "状态", value: <StatusBadge tone={statusToneFor(data.status)}>{data.status}</StatusBadge> },
    { label: "nature", value: data.nature },
    { label: "version", value: `v${data.version}` },
    { label: "revision", value: `r${data.revision}` },
    { label: "采集时间", value: formatTime(data.created_at) },
    { label: "verified by", value: data.verified_by ?? "—" },
    { label: "verified at", value: formatTime(data.verified_at) },
    ...(algorithmVersion ? [{ label: "算法版本", value: <span className="mono">{algorithmVersion}</span> }] : []),
    {
      label: "supersedes",
      value: data.supersedes_id ? <HashValue value={data.supersedes_id} head={10} tail={8} label="被替代的 Evidence" /> : "—",
    },
    {
      label: "superseded by",
      value: data.superseded_by ? <HashValue value={data.superseded_by} head={10} tail={8} label="替代后的 Evidence" /> : "—",
    },
  ];

  const contentPanel = (
    <PageSection title="证据内容" count={`${data.source_refs.length} 个来源引用`}>
      <Card>
        <KeyValueList
          items={[
            { label: "内容 SHA-256", value: <HashValue value={data.content_hash} wrap label="内容哈希" />, wide: true },
            {
              label: "对象引用",
              value: data.object_ref ? <HashValue value={data.object_ref} wrap label="对象引用" /> : "—",
              wide: true,
            },
          ]}
        />
      </Card>

      <PageSection title="source_refs" count={`${data.source_refs.length} 条`}>
        <Card>
          {data.source_refs.length === 0 ? (
            <EmptyState title="该 Evidence 没有来源引用" />
          ) : (
            <ul style={{ listStyle: "none", margin: 0, padding: 0, display: "flex", flexDirection: "column", gap: "var(--sp-2)" }}>
              {data.source_refs.map((ref) => (
                <li key={ref}>
                  <HashValue value={ref} wrap label="来源引用" />
                </li>
              ))}
            </ul>
          )}
        </Card>
      </PageSection>

      <Card>
        <Button size="sm" variant="ghost" onClick={() => setShowRaw((open) => !open)} aria-expanded={showRaw}>
          {showRaw ? "折叠原始记录" : "展开原始记录"}
        </Button>
        {showRaw ? (
          <div className="cs-chart-frame" style={{ marginTop: "var(--sp-3)" }}>
            <pre className="s5-json">{rawJson}</pre>
          </div>
        ) : null}
      </Card>
    </PageSection>
  );

  const asidePanel = (
    <div style={{ display: "flex", flexDirection: "column", gap: "var(--sp-4)" }}>
      <InspectorDrawer title="证据元数据" subtitle={`v${data.version} · r${data.revision}`} ariaLabel="证据元数据">
        <KeyValueList items={metaItems} />
      </InspectorDrawer>
      <EvidenceRail
        title="Provenance"
        items={
          provenanceEntries.length === 0
            ? [{ label: "provenance", value: "—" }]
            : provenanceEntries.map(([key, value]) => ({
                label: key,
                value: <span className="mono">{typeof value === "object" && value !== null ? JSON.stringify(value) : String(value)}</span>,
              }))
        }
      />
    </div>
  );

  return (
    <>
      <PageHeader
        title={data.title}
        breadcrumb={[
          { label: "调查任务", to: "/investigations" },
          { label: "工作台", to: `/investigations/${id}/workbench` },
          { label: "Findings 复核", to: `/investigations/${id}/findings` },
          { label: "Evidence 详情" },
        ]}
        status={<StatusBadge tone={statusToneFor(data.status)}>{data.status}</StatusBadge>}
        subtitle={<span className="mono">{`${data.evidence_type} · v${data.version} · revision r${data.revision}`}</span>}
        meta={
          <MetaLine
            items={[
              { label: "Evidence ID", value: <span className="mono">{data.id}</span> },
              { label: "采集时间", value: formatTime(data.created_at) },
              { label: "来源引用", value: String(data.source_refs.length) },
              { label: "Provenance 字段", value: String(provenanceEntries.length) },
            ]}
          />
        }
        primaryAction={
          can("evidence:review") ? (
            <Button size="sm" variant="primary" disabled={data.status === "verified"} onClick={() => command("verify")}>
              Verify
            </Button>
          ) : null
        }
        secondaryAction={
          <>
            <Button size="sm" onClick={() => navigate(`/investigations/${id}/findings`)}>
              返回 Findings
            </Button>
            {can("evidence:review") ? (
              <>
                <Button size="sm" disabled={["disputed", "superseded"].includes(data.status)} onClick={() => command("dispute")}>
                  Dispute
                </Button>
                <Button size="sm" variant="danger" disabled={data.status === "superseded"} onClick={() => command("supersede")}>
                  Supersede
                </Button>
              </>
            ) : null}
            {!can("evidence:sensitive:read") ? (
              <Button size="sm" variant="ghost" onClick={explainObjectRef}>
                对象引用说明
              </Button>
            ) : null}
          </>
        }
      />

      <SplitPane
        main={contentPanel}
        aside={asidePanel}
        storageKey="cs-evidence-split"
        defaultAsideWidth={380}
        minAsideWidth={300}
        maxAsideWidth={560}
        asideLabel="证据元数据与 Provenance"
      />

      <StickyActionBar>
        <Link className="btn" to={`/investigations/${data.investigation_id}/findings`}>
          查看相关 Findings
        </Link>
      </StickyActionBar>
    </>
  );
}
