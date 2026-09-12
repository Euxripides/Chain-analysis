import { useCallback, useEffect, useState } from "react";
import { ApiError, useAuth } from "@chain/api-client";
import {
  Card,
  DataTable,
  EmptyState,
  EntityLink,
  EvidenceRail,
  Metric,
  PageHeader,
  RelativeTime,
  SkeletonRows,
  StatusBadge,
  useDialog,
} from "@chain/ui";
import type { StatusTone } from "@chain/ui";
import { KeyValueList } from "../components/s6";
import { fmtDateTime, useAdminCommand } from "../lib/adminApi";

/** 审计链完整性边界上的一个观测缺口。 */
interface ObservedGap {
  chain: string;
  table: string;
  has_prefix_gap: boolean;
  gap_from_seq: number;
  gap_to_seq: number;
  gap_event_count: number;
  first_surviving_seq: number;
  sealed_tail_hash: string;
  sealed_tail_source: string;
  head_seq: number;
  head_hash: string;
  row_count: number;
  internal_gap_count: number;
  rows_inside_sealed_gap: number;
  sealed: boolean;
  incident_ref?: string;
  detected_at: string;
}

interface IncidentAnchor {
  chain: string;
  anchor_date: string;
  from_seq: number;
  to_seq: number;
  head_hash: string;
  event_count: number;
  object_ref: string;
  content_hash: string;
  archive_hash: string;
  archive_intact: boolean;
  disposition: string;
}

interface Incident {
  id: string;
  incident_ref: string;
  chain: string;
  kind: string;
  gap_from_seq: number;
  gap_to_seq: number;
  gap_event_count: number;
  trusted_from_seq: number;
  sealed_tail_hash: string;
  sealed_tail_source: string;
  detection_method: string;
  detected_at: string;
  detected_by_email: string;
  sealed_at: string;
  sealed_by_email: string;
  reason: string;
  approval_request_id?: string;
  evidence: Record<string, unknown>;
  evidence_hash: string;
  retained_anchors: IncidentAnchor[];
  already_sealed: boolean;
}

interface AnchorVerification {
  chain: string;
  anchor_date: string;
  from_seq: number;
  to_seq: number;
  head_hash: string;
  event_count: number;
  object_ref: string;
  content_hash: string;
  disposition: string;
  verified: boolean;
  recomputed_head_hash: string;
  checked: number;
  reason: string;
  archive_object_hash: string;
  archive_intact: boolean;
  head_hash_derivation: string;
  incident_ref?: string;
}

interface VerifyChainResult {
  chain: string;
  from_seq: number;
  to_seq: number;
  checked: number;
  valid: boolean;
  broken_at_seq: number | null;
  reason: string;
  head_hash: string;
  errors: string[];
  integrity_status: string;
  prefix_status: string;
  verified_from_seq: number;
  sealed_boundary?: { incident_ref: string; gap_from_seq: number; gap_to_seq: number; statement: string };
  integrity_warnings: string[];
}

interface IntegrityReport {
  chain: string;
  observed: ObservedGap;
  incident: Incident | null;
  integrity_status: string;
  prefix_status: string;
  chain_verification: VerifyChainResult;
  anchors: AnchorVerification[];
  anchors_verified_count: number;
  anchors_legacy_derivation_count: number;
  anchors_sealed_count: number;
  anchors_unexplained_count: number;
  all_anchors_accounted: boolean;
  truth_statement: string;
  as_of: string;
}

const INTEGRITY_LABEL: Record<string, string> = {
  intact: "全链完整",
  sealed_incident: "存在已封存缺口",
  undeclared_gap: "存在未封存缺口",
  boundary_conflict: "边界与实际数据冲突",
};

const INTEGRITY_TONE: Record<string, StatusTone> = {
  intact: "ok",
  sealed_incident: "warn",
  undeclared_gap: "danger",
  boundary_conflict: "danger",
};

const ANCHOR_LABEL: Record<string, string> = {
  verified: "已验证",
  verified_legacy_derivation: "已验证（历史派生）",
  sealed_unrecoverable: "封存不可恢复",
  spans_boundary: "跨越边界",
  broken: "不一致",
};

const ANCHOR_TONE: Record<string, StatusTone> = {
  verified: "ok",
  verified_legacy_derivation: "info",
  sealed_unrecoverable: "warn",
  spans_boundary: "danger",
  broken: "danger",
};

/**
 * §19.8 审计中心子页：完整性事件边界（Sprint 6 审计修复阻断项）。
 *
 * 页面职责：如实呈现「旧链在何处不连续、缺口为何不可恢复、新链可信起点在哪里」，
 * 以及边界之后的链与每日锚点是否逐项验证通过。页面正文不放任何提示横幅，
 * 所有说明与确认只通过 useDialog() 弹窗呈现（AGENTS.md / AD-009）。
 */
export default function AuditIntegrityPage() {
  const { client, can } = useAuth();
  const dialog = useDialog();
  const command = useAdminCommand();
  const [report, setReport] = useState<IntegrityReport | null>(null);
  const [loading, setLoading] = useState(true);
  const [error, setError] = useState<string | null>(null);
  const [busy, setBusy] = useState(false);
  const [pendingApprovalId, setPendingApprovalId] = useState<string | null>(null);

  const load = useCallback(async () => {
    setLoading(true);
    setError(null);
    try {
      const res = await client.get<IntegrityReport>("/api/admin/v1/audit/integrity", { query: { chain: "audit" } });
      setReport(res.data ?? null);
    } catch (err) {
      setError(err instanceof ApiError ? err.userMessage : "无法加载完整性报告");
    } finally {
      setLoading(false);
    }
  }, [client]);

  useEffect(() => {
    void load();
  }, [load]);

  const detect = async () => {
    setBusy(true);
    try {
      const res = await client.post<Record<string, unknown>>("/api/admin/v1/audit/integrity/preview", { chain: "audit" });
      const data = res.data ?? {};
      const observed = (data.observed ?? {}) as ObservedGap;
      await dialog.open({
        kind: observed.has_prefix_gap ? "warning" : "info",
        title: observed.has_prefix_gap ? "检测到不可恢复缺口" : "未检测到缺口",
        body: null,
        details: [
          { label: "链", value: observed.chain },
          { label: "前缀缺口", value: observed.has_prefix_gap ? `seq ${observed.gap_from_seq} → ${observed.gap_to_seq}` : "无" },
          { label: "缺口事件数", value: String(observed.gap_event_count ?? 0) },
          { label: "最小幸存 seq", value: String(observed.first_surviving_seq ?? 0) },
          { label: "封存尾哈希", value: <span className="mono" style={{ fontSize: "var(--fs-11)" }}>{observed.sealed_tail_hash || "—"}</span> },
          { label: "已封存", value: observed.sealed ? "是" : "否" },
          { label: "封存事件", value: observed.incident_ref || "—" },
        ],
        actions: [{ label: "关闭", variant: "primary", result: "ok" }],
      });
      await load();
    } catch (err) {
      dialog.notify("error", "缺口检测失败", err instanceof ApiError ? err.userMessage : "请稍后重试");
    } finally {
      setBusy(false);
    }
  };

  const verifyAnchors = async () => {
    setBusy(true);
    try {
      const res = await client.get<Record<string, unknown>>("/api/admin/v1/audit/anchors", { query: { chain: "audit" } });
      const data = res.data ?? {};
      await dialog.open({
        kind: data.all_accounted ? "info" : "warning",
        title: data.all_accounted ? "锚点逐项验证完成" : "存在无法归因的锚点",
        body: null,
        details: [
          { label: "锚点总数", value: String((data.items as unknown[] | undefined)?.length ?? 0) },
          { label: "已验证", value: String(data.verified ?? 0) },
          { label: "历史派生已验证", value: String(data.legacy_derivation ?? 0) },
          { label: "封存不可恢复", value: String(data.sealed_unrecoverable ?? 0) },
          { label: "无法归因", value: String(data.unexplained ?? 0) },
        ],
        actions: [{ label: "关闭", variant: "primary", result: "ok" }],
      });
      await load();
    } catch (err) {
      dialog.notify("error", "锚点验证失败", err instanceof ApiError ? err.userMessage : "请稍后重试");
    } finally {
      setBusy(false);
    }
  };

  const requestSeal = async () => {
    const data = await command.run({
      path: "/api/admin/v1/audit/integrity/incidents/requests",
      body: { chain: "audit" },
      confirmTitle: "发起审计缺口边界封存？",
      confirmBody:
        "封存会写入一条不可变的完整性事件记录：明确旧链在何处不连续、缺口不可恢复、新链可信起点在哪里。系统不会回填或伪造历史审计行，也不会改写既有锚点。该请求需要第二名具备审批权限的人员批准后才能执行。",
      confirmLabel: "发起封存请求",
      variant: "danger",
      requireReason: true,
      reasonLabel: "封存原因",
      requireStepUp: true,
      successTitle: "封存请求已创建",
      successDetails: (result) => {
        const approval = (result.approval ?? {}) as Record<string, unknown>;
        return [
          { label: "审批请求", value: String(approval.id ?? "—") },
          { label: "操作", value: String(approval.operation ?? "—") },
          { label: "状态", value: String(approval.status ?? "—") },
        ];
      },
    });
    const approval = data?.approval as Record<string, unknown> | undefined;
    if (approval?.id) setPendingApprovalId(String(approval.id));
    await load();
  };

  const decide = async (decision: "approved" | "rejected") => {
    const id = pendingApprovalId;
    if (!id) {
      dialog.notify("warning", "没有待审批的封存请求", "请先发起封存请求，或从审批中心选择对应请求。");
      return;
    }
    const data = await command.run({
      path: `/api/admin/v1/audit/integrity/incidents/requests/${id}/${decision === "approved" ? "approve" : "reject"}`,
      confirmTitle: decision === "approved" ? "批准封存请求？" : "驳回封存请求？",
      confirmBody:
        decision === "approved"
          ? "批准后由发起人执行封存。请求人不能自行审批（数据库约束兜底）。"
          : "驳回后该请求不再可执行，缺口将保持未封存状态。",
      confirmLabel: decision === "approved" ? "批准" : "驳回",
      variant: decision === "approved" ? "primary" : "danger",
      requireReason: true,
      reasonLabel: "审批意见",
      requireStepUp: false,
      successTitle: decision === "approved" ? "已批准" : "已驳回",
    });
    if (data) setPendingApprovalId(null);
    await load();
  };

  const executeSeal = async () => {
    const id = pendingApprovalId;
    if (!id) {
      dialog.notify("warning", "没有可执行的封存请求", "请先发起封存请求并完成双人审批。");
      return;
    }
    const data = await command.run({
      path: `/api/admin/v1/audit/integrity/incidents/requests/${id}/execute`,
      confirmTitle: "执行边界封存？",
      confirmBody:
        "执行后完整性事件记录与哈希证据永久不可修改、不可删除、不可清空，应用运行角色也无法 UPDATE / DELETE / TRUNCATE / DROP。此操作不会修改任何既有审计事件或锚点。",
      confirmLabel: "执行封存",
      variant: "danger",
      requireReason: true,
      reasonLabel: "执行说明",
      requireStepUp: true,
      successTitle: "边界已封存",
      successDetails: (result) => {
        const incident = (result.incident ?? {}) as Record<string, unknown>;
        return [
          { label: "事件引用", value: String(incident.incident_ref ?? "—") },
          { label: "缺口区间", value: `seq ${incident.gap_from_seq} → ${incident.gap_to_seq}` },
          { label: "新链可信起点", value: String(incident.trusted_from_seq ?? "—") },
          { label: "证据哈希", value: <span className="mono" style={{ fontSize: "var(--fs-11)" }}>{String(incident.evidence_hash ?? "—")}</span> },
        ];
      },
    });
    if (data) setPendingApprovalId(null);
    await load();
  };

  const createAnchor = async () => {
    await command.run({
      path: "/api/admin/v1/audit/anchors",
      body: { chain: "audit" },
      confirmTitle: "生成/确认当日审计锚点？",
      confirmBody:
        "锚点区间自新链可信起点开始，不会跨越已封存边界。既有锚点不可改写：若该日期已有锚点，本次只做逐项复核，不重写、不重传归档对象。",
      confirmLabel: "生成锚点",
      variant: "primary",
      requireReason: true,
      reasonLabel: "生成原因",
      requireStepUp: false,
      successTitle: "锚点已处理",
      successDetails: (result) => [
        { label: "日期", value: String(result.date ?? "—") },
        { label: "本次新建", value: result.created ? "是" : "否（已存在，已复核）" },
      ],
    });
    await load();
  };

  const status = report?.integrity_status ?? "unknown";
  const chain = report?.chain_verification;
  const observed = report?.observed;
  const incident = report?.incident ?? null;

  return (
    <div className="workbench">
      <div style={{ flex: 1, minWidth: 0, display: "flex", flexDirection: "column", gap: "var(--sp-5)" }}>
        <PageHeader
          title="完整性事件边界"
          subtitle="审计链缺口检测、封存与边界之后逐项验证 · 数据源 postgres:audit_integrity_incidents"
          actions={
            <>
              <button className="btn btn--sm" onClick={load} disabled={loading || busy}>
                刷新
              </button>
              <button className="btn btn--sm" disabled={!can("admin:audit:verify") || busy} onClick={detect}>
                检测缺口
              </button>
              <button className="btn btn--sm" disabled={!can("admin:audit:verify") || busy} onClick={verifyAnchors}>
                逐项校验锚点
              </button>
              <button className="btn btn--sm" disabled={!can("admin:audit:seal") || busy || !observed?.has_prefix_gap} onClick={createAnchor}>
                生成当日锚点
              </button>
              <button
                className="btn btn--primary btn--sm"
                disabled={!can("admin:audit:seal") || busy || !observed?.has_prefix_gap || Boolean(incident)}
                onClick={requestSeal}
              >
                发起封存
              </button>
            </>
          }
        />

        {loading && !report ? (
          <Card>
            <SkeletonRows rows={6} columns={4} />
          </Card>
        ) : error ? (
          <Card>
            <EmptyState title="无法读取完整性报告" body={error} action={<button className="btn btn--sm" onClick={load}>重试</button>} />
          </Card>
        ) : report ? (
          <>
            <div className="metric-grid">
              <Metric
                label="完整性状态"
                value={<StatusBadge tone={INTEGRITY_TONE[status] ?? "neutral"}>{INTEGRITY_LABEL[status] ?? status}</StatusBadge>}
                hint={`前缀：${report.prefix_status}`}
              />
              <Metric
                label="可验证起点"
                value={chain?.verified_from_seq ? `seq ${chain.verified_from_seq}` : "—"}
                hint={chain ? `校验 ${chain.checked} 条 · ${chain.valid ? "通过" : "失败"}` : "—"}
              />
              <Metric
                label="不可恢复缺口"
                value={observed?.has_prefix_gap ? `${observed.gap_event_count} 条` : "无"}
                hint={observed?.has_prefix_gap ? `seq ${observed.gap_from_seq} → ${observed.gap_to_seq}` : "—"}
              />
              <Metric
                label="锚点"
                value={`${report.anchors_verified_count + report.anchors_legacy_derivation_count} / ${report.anchors.length}`}
                hint={`封存 ${report.anchors_sealed_count} · 无法归因 ${report.anchors_unexplained_count}`}
              />
            </div>

            <Card title="封存事件">
              {incident ? (
                <KeyValueList
                  columns={2}
                  items={[
                    { label: "事件引用", value: incident.incident_ref, mono: true },
                    { label: "链", value: incident.chain, mono: true },
                    { label: "类型", value: incident.kind, mono: true },
                    { label: "缺口区间", value: `seq ${incident.gap_from_seq} → ${incident.gap_to_seq}（${incident.gap_event_count} 条）`, mono: true },
                    { label: "新链可信起点", value: `seq ${incident.trusted_from_seq}`, mono: true },
                    { label: "封存尾哈希", value: incident.sealed_tail_hash, mono: true },
                    { label: "封存尾来源", value: incident.sealed_tail_source },
                    { label: "检测方式", value: incident.detection_method },
                    { label: "证据哈希", value: incident.evidence_hash, mono: true },
                    { label: "审批请求", value: incident.approval_request_id || "—", mono: true },
                    { label: "封存人", value: incident.sealed_by_email || "—" },
                    { label: "封存时间", value: fmtDateTime(incident.sealed_at) },
                    { label: "封存原因", value: incident.reason },
                    { label: "保留锚点", value: `${incident.retained_anchors.length} 项（原样保留，未改写）` },
                  ]}
                />
              ) : (
                <EmptyState
                  title="尚未封存完整性边界"
                  body="检测到不可恢复缺口但未封存时，链校验会返回 undeclared_gap 且 valid=false；请发起封存请求并完成双人审批。"
                />
              )}
            </Card>

            {incident && incident.retained_anchors.length > 0 ? (
              <Card flush title="边界引用的保留锚点">
                <DataTable<IncidentAnchor>
                  rows={incident.retained_anchors}
                  rowKey={(r) => `${r.chain}-${r.anchor_date}`}
                  empty={<EmptyState title="没有保留锚点" />}
                  columns={[
                    { key: "date", header: "锚点日期", width: "130px", render: (r) => <span className="mono">{r.anchor_date}</span> },
                    { key: "range", header: "区间", width: "160px", render: (r) => <span className="mono">seq {r.from_seq} → {r.to_seq}</span> },
                    { key: "count", header: "事件数", width: "90px", render: (r) => String(r.event_count) },
                    { key: "head", header: "head_hash", width: "170px", render: (r) => <EntityLink value={r.head_hash} /> },
                    {
                      key: "archive",
                      header: "归档一致性",
                      width: "140px",
                      render: (r) =>
                        r.archive_intact ? (
                          <StatusBadge tone="ok">归档一致</StatusBadge>
                        ) : (
                          <StatusBadge tone="warn">未核对</StatusBadge>
                        ),
                    },
                    { key: "disposition", header: "处置", width: "150px", render: (r) => <StatusBadge tone={ANCHOR_TONE[r.disposition] ?? "neutral"}>{ANCHOR_LABEL[r.disposition] ?? r.disposition}</StatusBadge> },
                  ]}
                />
              </Card>
            ) : null}

            <Card flush title="每日锚点逐项验证">
              <DataTable<AnchorVerification>
                rows={report.anchors}
                rowKey={(r) => `${r.chain}-${r.anchor_date}`}
                empty={<EmptyState title="该链还没有每日锚点" />}
                columns={[
                  { key: "date", header: "锚点日期", width: "130px", render: (r) => <span className="mono">{r.anchor_date}</span> },
                  { key: "range", header: "区间", width: "170px", render: (r) => <span className="mono">seq {r.from_seq} → {r.to_seq}</span> },
                  {
                    key: "disposition",
                    header: "结论",
                    width: "180px",
                    render: (r) => <StatusBadge tone={ANCHOR_TONE[r.disposition] ?? "neutral"}>{ANCHOR_LABEL[r.disposition] ?? r.disposition}</StatusBadge>,
                  },
                  { key: "checked", header: "复算条数", width: "100px", render: (r) => String(r.checked) },
                  { key: "head", header: "锚点 head_hash", width: "170px", render: (r) => <EntityLink value={r.head_hash} /> },
                  { key: "recomputed", header: "复算 head_hash", width: "170px", render: (r) => (r.recomputed_head_hash ? <EntityLink value={r.recomputed_head_hash} /> : "—") },
                  { key: "reason", header: "说明", render: (r) => r.reason || "—" },
                ]}
              />
            </Card>

            <Card title="边界之后逐行复算">
              <KeyValueList
                columns={2}
                items={[
                  { label: "链", value: report.chain, mono: true },
                  { label: "integrity_status", value: chain?.integrity_status ?? "—", mono: true },
                  { label: "prefix_status", value: chain?.prefix_status ?? "—", mono: true },
                  { label: "实际校验起点", value: chain ? `seq ${chain.verified_from_seq}` : "—", mono: true },
                  { label: "实际校验终点", value: chain ? `seq ${chain.to_seq}` : "—", mono: true },
                  { label: "复算条数", value: chain ? String(chain.checked) : "—" },
                  { label: "结论", value: chain ? (chain.valid ? "区间自洽" : `失败：${chain.reason}`) : "—" },
                  { label: "链头哈希", value: chain?.head_hash ?? "—", mono: true },
                  { label: "报告时间（UTC）", value: fmtDateTime(report.as_of) },
                ]}
              />
            </Card>
          </>
        ) : null}
      </div>

      <EvidenceRail
        title="完整性说明"
        items={[
          { label: "事实陈述", value: report?.truth_statement ?? "—" },
          { label: "缺口是否可恢复", value: "不可恢复：无基备、无 WAL 归档、无快照、MinIO 仅存锚点、ClickHouse 无审计镜像、导出均晚于缺口" },
          { label: "是否回填历史", value: "禁止。缺口区间保持为空，任何回填都会被校验器判定为 boundary_conflict" },
          { label: "旧锚点", value: "原样保留；数据库层禁止改写与删除（0055 触发器 + 运行角色 ACL）" },
          { label: "边界证据", value: "创建 / 审批 / 哈希证据为 append-only；运行角色无 UPDATE/DELETE/TRUNCATE/DROP" },
          { label: "新链可信起点", value: observed?.first_surviving_seq ? `seq ${observed.first_surviving_seq}（以封存尾哈希衔接）` : "—" },
          { label: "封存尾哈希", value: <span className="mono" style={{ fontSize: "var(--fs-11)" }}>{observed?.sealed_tail_hash || "—"}</span> },
          { label: "检测时间", value: observed ? <RelativeTime value={observed.detected_at} /> : "—" },
        ]}
      />
    </div>
  );
}
