import {
  useCallback,
  useState } from "react";
import { Link,
  useNavigate,
  useParams } from "react-router-dom";
import { useAuth } from "@chain/api-client";
import {
  Button,
  Card,
  EmptyState,
  ErrorState,
  HashValue,
  InspectorDrawer,
  KeyValueList,
  MetaLine,
  PageSection,
  PageSkeleton,
  SplitPane,
  StatusBadge,
  StickyActionBar,
  Timeline,
  statusToneFor,
  useDialog,
} from "@chain/ui";
import { CsPageHeader } from "@chain/user-ui";
import { formatTime, useAction, useAsync } from "../sprint4/common";
import { canVisitCaseTabUrl } from "../app/case-tabs";

interface Approval {
  id: string;
  report_revision: number;
  reviewer_id?: string | null;
  decision: string;
  content_hash: string;
  note: string;
  created_at: string;
}
interface ReportDetail {
  id: string;
  investigation_id: string;
  case_id?: string | null;
  case_no?: string;
  revision: number;
  format: string;
  content_hash: string;
  artifact_sha256: string;
  size_bytes: number;
  status: string;
  status_revision: number;
  generated_at: string;
  submitted_at?: string | null;
  submitted_by?: string | null;
  approved_at?: string | null;
  approved_by?: string | null;
  rejected_at?: string | null;
  reject_reason: string;
  signed_at?: string | null;
  signature_hash: string;
  archived_at?: string | null;
  downloadable: boolean;
  approvals: Approval[];
  metadata: Record<string, unknown>;
}

/** 服务端状态 → 中文标签；原始状态值仍保留在状态徽标的 title 上。 */
const REPORT_STATUS_LABEL: Record<string, string> = {
  generated: "草稿",
  in_review: "待审批",
  approved: "已批准",
  rejected: "已驳回",
  signed: "已签署",
  archived: "已归档",
};

function decisionTone(decision: string): "ok" | "danger" | "warn" | "neutral" {
  if (decision === "approve") return "ok";
  if (decision === "reject") return "danger";
  return "neutral";
}

/**
 * §12.23 报告详情：审批状态机 + 一次性下载票据。artifact revision 不可覆盖。
 *
 * 表现层重构**没有改动任何请求**：提交/批准/驳回/签署/归档继续走原来的
 * `POST /api/v2/reports/{id}/commands/{cmd}`（含 `If-Match: status_revision`、意见输入、
 * 二次确认、Trace 错误处理），下载继续走原来的 download-ticket 一次性票据。
 */
export default function ReportDetailPage() {
  const { id = "" } = useParams();
  const { can } = useAuth();
  const dialog = useDialog();
  const action = useAction();
  const navigate = useNavigate();
  const { data, loading, error, reload } = useAsync<ReportDetail>(`/api/v2/reports/${id}`, [id]);
  /* 只用于给被点击的那个按钮显示 busy，不改变 command 本身的语义。 */
  const [pending, setPending] = useState<string | null>(null);

  const command = useCallback(
    async (cmd: string) => {
      if (!data) return;
      let note = "";
      let reviewer = "";
      if (cmd === "reject" || cmd === "approve") {
        const raw = await dialog.prompt({
          title: cmd === "reject" ? "驳回原因" : "审批意见（可留空）",
          input: { label: "意见", pattern: cmd === "reject" ? /^.{2,}$/ : /^.*$/ },
        });
        if (raw === "" || raw === null) return;
        note = raw.startsWith("input:") ? raw.slice("input:".length).trim() : "";
        if (cmd === "reject" && !note) return;
      }
      if (cmd === "submit") {
        const raw = await dialog.prompt({
          title: "指定审批人（可留空）",
          body: "留空时通知本组织全部拥有 report:review 权限的成员。",
          input: { label: "Reviewer identity ID", placeholder: "uuid 或留空", pattern: /^$|^[0-9a-f-]{36}$/ },
        });
        if (raw === "" || raw === null) return;
        reviewer = raw.startsWith("input:") ? raw.slice("input:".length).trim() : "";
      }
      const answer = await dialog.confirm({
        kind: "confirm",
        title: `确认执行 ${cmd}？`,
        details: [
          { label: "当前状态", value: data.status },
          { label: "报告 revision", value: `r${data.revision}` },
          { label: "status_revision", value: `r${data.status_revision}` },
          { label: "内容哈希", value: data.content_hash },
          ...(note ? [{ label: "意见", value: note }] : []),
          ...(reviewer ? [{ label: "审批人", value: reviewer }] : []),
        ],
      });
      if (answer !== "confirm") return;
      await action.run(
        () => action.client.post(`/api/v2/reports/${id}/commands/${cmd}`, { note, reason: note, reviewer_id: reviewer }, { ifMatch: String(data.status_revision) }),
        { successTitle: "报告状态已更新", onDone: reload },
      );
    },
    [action, data, dialog, id, reload],
  );

  const download = useCallback(async () => {
    const res = await action.run(() => action.client.post<{ url: string; expires_in_seconds: number; content_hash: string }>(`/api/v2/reports/${id}/download-ticket`, {}), {
      successTitle: "下载票据已签发",
      successBody: "票据 60 秒内有效且只能使用一次。",
      onDone: (r) => window.open(r.data.url, "_blank"),
    });
    if (!res) return;
  }, [action, id]);

  /** 包裹层：只负责按钮 busy 指示，命令与错误语义完全由 command / download 决定。 */
  const runCommand = useCallback(
    async (cmd: string) => {
      setPending(cmd);
      try {
        await command(cmd);
      } finally {
        setPending(null);
      }
    },
    [command],
  );

  const runDownload = useCallback(async () => {
    setPending("download");
    try {
      await download();
    } finally {
      setPending(null);
    }
  }, [download]);

  if (loading) return <PageSkeleton shape="detail" />;
  if (error || !data) {
    return (
      <Card>
        <ErrorState title="报告加载失败" message={error ?? "不存在或无权访问"} onRetry={reload} />
      </Card>
    );
  }

  /* 徽标同时给出中文标签与服务端原始值：原始值是接口与审计里的权威写法。 */
  const statusText = REPORT_STATUS_LABEL[data.status] ? `${REPORT_STATUS_LABEL[data.status]} · ${data.status}` : data.status;
  const canSubmit = can("report:create") && ["generated", "rejected"].includes(data.status);
  const canReview = can("report:review") && data.status === "in_review";
  const canSign = can("report:sign") && data.status === "approved";
  const canArchive = can("report:review") && ["approved", "signed"].includes(data.status);
  const canDownload = can("report:download") && data.downloadable;

  const submitButton = canSubmit ? (
    <Button size="sm" busy={pending === "submit"} busyLabel="提交中…" onClick={() => void runCommand("submit")}>
      提交复核
    </Button>
  ) : null;
  const approveButton = canReview ? (
    <Button size="sm" variant="primary" busy={pending === "approve"} busyLabel="批准中…" onClick={() => void runCommand("approve")}>
      批准
    </Button>
  ) : null;
  const rejectButton = canReview ? (
    <Button size="sm" variant="danger" busy={pending === "reject"} busyLabel="驳回中…" onClick={() => void runCommand("reject")}>
      驳回
    </Button>
  ) : null;
  const signButton = canSign ? (
    <Button size="sm" variant="primary" busy={pending === "sign"} busyLabel="签署中…" onClick={() => void runCommand("sign")}>
      签署
    </Button>
  ) : null;
  const archiveButton = canArchive ? (
    <Button size="sm" busy={pending === "archive"} busyLabel="归档中…" onClick={() => void runCommand("archive")}>
      归档
    </Button>
  ) : null;
  const downloadButton = canDownload ? (
    <Button size="sm" busy={pending === "download"} busyLabel="签发中…" onClick={() => void runDownload()}>
      下载（票据）
    </Button>
  ) : null;

  /* 危险操作（驳回）永远不是主按钮：主按钮只承载"推进流程"的动作。 */
  const primaryAction = approveButton ?? signButton ?? submitButton ?? downloadButton;
  const secondaryActions = (
    <span className="toolbar" style={{ justifyContent: "flex-end" }}>
      <Button size="sm" variant="ghost" onClick={() => navigate(-1)}>
        返回
      </Button>
      {rejectButton}
      {archiveButton}
      {primaryAction === submitButton ? null : submitButton}
      {primaryAction === signButton ? null : signButton}
      {primaryAction === downloadButton ? null : downloadButton}
    </span>
  );

  const approvals = data.approvals ?? [];
  const metadata = Object.entries(data.metadata ?? {});

  return (
    <>
      <CsPageHeader
        title={`报告 r${data.revision}`}
        breadcrumb={[
          { label: "工作台", to: "/home" },
          { label: "报告中心", to: "/reports" },
          { label: `报告 r${data.revision}` },
        ]}
        status={
          <span title={data.status}>
            <StatusBadge tone={statusToneFor(data.status)}>{statusText}</StatusBadge>
          </span>
        }
        meta={
          <MetaLine
            items={[
              { label: "案件", value: data.case_no || data.case_id || "—" },
              { label: "调查", value: data.investigation_id.slice(0, 8) + "…" },
              { label: "格式", value: data.format || "—" },
              { label: "大小", value: `${data.size_bytes} B` },
              { label: "生成", value: formatTime(data.generated_at) },
              { label: "数据源", value: "postgres:investigation_reports + MinIO" },
            ]}
          />
        }
        primaryAction={primaryAction}
        secondaryAction={secondaryActions}
      />

      <SplitPane
        storageKey="cs-report-detail-aside"
        asideLabel="审批状态"
        aside={
          <InspectorDrawer title="审批状态" subtitle={`r${data.revision}`} ariaLabel="审批状态与审批记录">
            <PageSection title="当前状态">
              <KeyValueList
                ariaLabel="审批状态"
                items={[
                  { label: "状态", value: <span title={data.status}><StatusBadge tone={statusToneFor(data.status)}>{statusText}</StatusBadge></span> },
                  { label: "当前 revision", value: <span className="mono">r{data.revision}</span> },
                  { label: "status_revision", value: <span className="mono">s{data.status_revision}</span> },
                  { label: "提交时间", value: formatTime(data.submitted_at) },
                  { label: "批准时间", value: formatTime(data.approved_at) },
                  { label: "驳回时间", value: formatTime(data.rejected_at) },
                  { label: "驳回原因", value: data.reject_reason || "—" },
                  { label: "签署时间", value: formatTime(data.signed_at) },
                  { label: "归档时间", value: formatTime(data.archived_at) },
                ]}
              />
            </PageSection>
            <PageSection title="审批记录" count={approvals.length + " 条"}>
              {approvals.length === 0 ? (
                <EmptyState title="尚无审批记录" />
              ) : (
                <Timeline
                  ariaLabel="审批记录"
                  items={approvals.map((approval) => ({
                    key: approval.id,
                    title: `${approval.decision} · revision r${approval.report_revision}`,
                    tone: decisionTone(approval.decision),
                    meta: (
                      <>
                        <span>reviewer {approval.reviewer_id ?? "—"}</span>
                        <div>{formatTime(approval.created_at)}</div>
                        <div>{approval.note || "无意见"}</div>
                        <div className="mono" title={approval.content_hash}>
                          {approval.content_hash.slice(0, 16)}…
                        </div>
                      </>
                    ),
                  }))}
                />
              )}
            </PageSection>
          </InspectorDrawer>
        }
        main={
          <div style={{ display: "flex", flexDirection: "column", gap: "var(--sp-5)", minWidth: 0 }}>
            <PageSection title="报告元数据">
              <Card>
                <KeyValueList
                  ariaLabel="报告元数据"
                  items={[
                    { label: "报告 ID", value: <HashValue value={data.id} wrap label="报告 ID" />, wide: true },
                    { label: "案件", value: data.case_no || data.case_id || "—" },
                    { label: "调查", value: <HashValue value={data.investigation_id} head={12} tail={8} label="调查 ID" /> },
                    { label: "revision", value: <span className="mono">r{data.revision}</span> },
                    { label: "状态", value: <span title={data.status}><StatusBadge tone={statusToneFor(data.status)}>{statusText}</StatusBadge></span> },
                    { label: "status_revision", value: <span className="mono">s{data.status_revision}</span> },
                    { label: "格式", value: data.format || "—" },
                    { label: "大小", value: `${data.size_bytes} bytes` },
                    { label: "生成时间", value: formatTime(data.generated_at) },
                    { label: "提交时间", value: formatTime(data.submitted_at) },
                    { label: "提交人", value: <HashValue value={data.submitted_by ?? ""} label="提交人身份 ID" /> },
                    { label: "批准时间", value: formatTime(data.approved_at) },
                    { label: "审批人", value: <HashValue value={data.approved_by ?? ""} label="审批人身份 ID" /> },
                    { label: "驳回原因", value: data.reject_reason || "—" },
                    { label: "签署时间", value: formatTime(data.signed_at) },
                    { label: "归档时间", value: formatTime(data.archived_at) },
                  ]}
                />
              </Card>
            </PageSection>

            <PageSection title="报告文件" count={data.downloadable ? "可下载" : "当前状态不可下载"}>
              <Card>
                <KeyValueList
                  ariaLabel="报告文件"
                  items={[
                    { label: "artifact SHA-256", value: <HashValue value={data.artifact_sha256} wrap label="artifact SHA-256" />, wide: true },
                    { label: "内容哈希", value: <HashValue value={data.content_hash} wrap label="报告内容哈希" />, wide: true },
                    { label: "签名哈希", value: data.signature_hash ? <HashValue value={data.signature_hash} wrap label="签名哈希" /> : "—", wide: true },
                    { label: "格式", value: data.format || "—" },
                    { label: "大小", value: `${data.size_bytes} bytes` },
                    { label: "下载可用", value: data.downloadable ? "是" : "否" },
                  ]}
                />
              </Card>
            </PageSection>

            <PageSection title="Report metadata" count={metadata.length + " 项"}>
              <Card>
                {metadata.length === 0 ? (
                  <EmptyState title="服务端未返回附加元数据" />
                ) : (
                  <KeyValueList
                    ariaLabel="Report metadata"
                    items={metadata.map(([key, value]) => ({
                      label: key,
                      value: (
                        <span className="mono">
                          {typeof value === "object" && value !== null ? JSON.stringify(value) : String(value)}
                        </span>
                      ),
                    }))}
                  />
                )}
              </Card>
            </PageSection>

            <StickyActionBar>
              <Link className="btn btn--sm" to={`/investigations/${data.investigation_id}/workbench`}>
                调查工作台
              </Link>
              {/*
                深链接必须与案件 Tab 的路由守卫同源：没有 report:list 就不该给出这个入口，
                否则用户点进去只会看到无权限页，还以为是页面坏了。
              */}
              {data.case_id && canVisitCaseTabUrl(`/cases/${data.case_id}/reports`, can) ? (
                <Link className="btn btn--sm" to={`/cases/${data.case_id}/reports`}>
                  案件报告
                </Link>
              ) : null}
            </StickyActionBar>
          </div>
        }
      />
    </>
  );
}
