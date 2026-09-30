import { useState, type ReactNode } from "react";
import { ApiError, newIdempotencyKey, useAuth } from "@chain/api-client";
import {
  Card,
  DataTable,
  dialogInputValue,
  EmptyState,
  EntityLink,
  Field,
  PageHeader,
  RelativeTime,
  StatusBadge,
  type StatusTone,
  useDialog,
} from "@chain/ui";
import { fmtDateTime, useAdminList } from "../lib/adminApi";
import { JsonBlock, KeyValueList } from "../components/s6";

/**
 * A-ALERT-01：告警中心（路由 /alerts，权限 admin:alert:read，处置需 admin:alert:manage）。
 *
 * 数据来源：GET /api/admin/v1/alerts（服务端按优先级 P0→P3、last_seen_at 倒序）。
 * 详情：GET /api/admin/v1/alerts/{id}，返回 alert + related 关联链（Provider → request → job → shard → checkpoint → object）。
 * 处置：POST /api/admin/v1/alerts/{id}/commands/{acknowledge|mute|resolve}，均要求 note。
 * 所有提示、口径与冲突说明只通过弹窗呈现；页面正文只有业务数据与操作控件。
 */

interface AlertRow {
  id: string;
  severity?: string;
  priority?: string;
  title?: string;
  message?: string;
  status?: string;
  source?: string;
  slo_key?: string;
  fingerprint?: string;
  trace_id?: string;
  links?: Record<string, unknown>;
  labels?: Record<string, unknown>;
  occurrence_count?: number;
  first_seen_at?: string;
  last_seen_at?: string;
  muted_until?: string;
  resolved_at?: string;
  acknowledged_at?: string;
  acknowledged_by_email?: string;
  acknowledge_note?: string;
  resolved_by_email?: string;
  resolution_note?: string;
}

interface RelatedLayer {
  [key: string]: unknown;
}

interface AlertDetail {
  alert: AlertRow;
  related: {
    provider?: RelatedLayer;
    request?: RelatedLayer;
    job?: RelatedLayer;
    shard?: RelatedLayer;
    checkpoint?: RelatedLayer;
    object?: RelatedLayer;
  };
}

const STATUS_OPTIONS = ["OPEN", "ACK", "RESOLVED", "MUTED"];
const PRIORITY_OPTIONS = ["P0", "P1", "P2", "P3"];

const COMMANDS: Record<"acknowledge" | "mute" | "resolve", { label: string; permission: string; confirmTitle: string; doneTitle: string }> = {
  acknowledge: {
    label: "认领",
    permission: "admin:alert:manage",
    confirmTitle: "认领告警",
    doneTitle: "告警已认领",
  },
  mute: {
    label: "抑制",
    permission: "admin:alert:manage",
    confirmTitle: "抑制告警",
    doneTitle: "告警已抑制",
  },
  resolve: {
    label: "解决",
    permission: "admin:alert:manage",
    confirmTitle: "解决告警",
    doneTitle: "告警已解决",
  },
};

function priorityTone(priority: string | undefined): StatusTone {
  switch ((priority ?? "").toUpperCase()) {
    case "P0":
      return "danger";
    case "P1":
      return "warn";
    case "P2":
      return "info";
    default:
      return "neutral";
  }
}

function alertStatusTone(status: string | undefined): StatusTone {
  switch ((status ?? "").toUpperCase()) {
    case "OPEN":
      return "danger";
    case "ACK":
      return "warn";
    case "RESOLVED":
      return "ok";
    case "MUTED":
      return "neutral";
    default:
      return "neutral";
  }
}

function asRecord(value: unknown): Record<string, unknown> | null {
  return value && typeof value === "object" ? (value as Record<string, unknown>) : null;
}

function text(value: unknown): string {
  if (value === null || value === undefined || value === "") return "—";
  return String(value);
}

/** AlertsRelatedChain 渲染六层关联链；每层显式给出命中状态与未命中原因。 */
function RelatedChain({ related }: { related: AlertDetail["related"] }) {
  const layers: Array<{ key: string; label: string; node?: RelatedLayer }> = [
    { key: "provider", label: "1 · Provider", node: related?.provider },
    { key: "request", label: "2 · Request", node: related?.request },
    { key: "job", label: "3 · Job", node: related?.job },
    { key: "shard", label: "4 · Shard", node: related?.shard },
    { key: "checkpoint", label: "5 · Checkpoint", node: related?.checkpoint },
    { key: "object", label: "6 · Object", node: related?.object },
  ];
  return (
    <div style={{ display: "flex", flexDirection: "column", gap: "var(--sp-4)" }}>
      {layers.map((layer) => {
        const record = asRecord(layer.node);
        if (!record) {
          return (
            <div key={layer.key} style={{ borderLeft: "2px solid var(--c-border)", paddingLeft: "var(--sp-3)" }}>
              <div className="toolbar" style={{ justifyContent: "space-between" }}>
                <strong>{layer.label}</strong>
                <StatusBadge tone="neutral">未关联</StatusBadge>
              </div>
            </div>
          );
        }
        const found = record.found === undefined ? true : Boolean(record.found);
        const exists = record.exists === undefined ? found : Boolean(record.exists);
        const reason = typeof record.reason === "string" ? record.reason : "";
        const entries = Object.entries(record).filter(([key]) => key !== "found" && key !== "reason" && key !== "exists");
        const items = entries.map(([key, value]) => ({
          label: key,
          value: key.endsWith("_id") || key === "id" || key === "trace_id" || key === "object_ref"
            ? <EntityLink value={text(value)} copyable={text(value) !== "—"} />
            : text(typeof value === "object" && value !== null ? JSON.stringify(value) : value),
          mono: true,
        }));
        return (
          <div key={layer.key} style={{ borderLeft: "2px solid var(--c-border)", paddingLeft: "var(--sp-3)" }}>
            <div className="toolbar" style={{ justifyContent: "space-between" }}>
              <strong>{layer.label}</strong>
              <StatusBadge tone={exists ? "ok" : "warn"}>{exists ? "已命中" : "未命中"}</StatusBadge>
            </div>
            {reason ? (
              <div style={{ fontSize: "var(--fs-12)", color: "var(--c-slate)", marginTop: "var(--sp-1)" }}>
                <span className="mono">{reason}</span>
              </div>
            ) : null}
            {items.length > 0 ? (
              <div style={{ marginTop: "var(--sp-2)" }}>
                <KeyValueList items={items} columns={2} />
              </div>
            ) : null}
          </div>
        );
      })}
    </div>
  );
}

export default function AlertsPage() {
  const dialog = useDialog();
  const { client, can } = useAuth();
  const list = useAdminList<AlertRow>("/api/admin/v1/alerts", {}, 50);

  const [status, setStatus] = useState("all");
  const [priority, setPriority] = useState("all");
  const [busyId, setBusyId] = useState<string | null>(null);
  const [detailLoadingId, setDetailLoadingId] = useState<string | null>(null);

  const canManage = can("admin:alert:manage");
  const openP0 = Number(list.extra.open_p0 ?? 0);

  const guard = (permission: string): boolean => {
    if (can(permission)) return true;
    dialog.notify("warning", "缺少控制权限", "该操作需要 " + permission + "。");
    return false;
  };

  const openDetail = async (row: AlertRow) => {
    setDetailLoadingId(row.id);
    try {
      const response = await client.get<AlertDetail>("/api/admin/v1/alerts/" + encodeURIComponent(row.id));
      const detail = response.data;
      const alert = detail.alert;
      await dialog.open({
        kind: "info",
        title: alert.title || "告警详情",
        body: (
          <div style={{ display: "flex", flexDirection: "column", gap: "var(--sp-4)" }}>
            <KeyValueList
              items={[
                { label: "优先级", value: <StatusBadge tone={priorityTone(alert.priority)}>{alert.priority || alert.severity || "—"}</StatusBadge> },
                { label: "状态", value: <StatusBadge tone={alertStatusTone(alert.status)}>{alert.status || "—"}</StatusBadge> },
                { label: "来源", value: alert.source || "—" },
                { label: "SLO", value: alert.slo_key || "—", mono: true },
                { label: "指纹", value: alert.fingerprint || "—", mono: true },
                { label: "Trace", value: <EntityLink value={alert.trace_id || ""} copyable={Boolean(alert.trace_id)} /> },
                { label: "发生次数", value: alert.occurrence_count ?? "—" },
                { label: "首次出现", value: fmtDateTime(alert.first_seen_at) },
                { label: "最近出现", value: fmtDateTime(alert.last_seen_at) },
                { label: "告警信息", value: <span className="mono">{alert.message || "—"}</span> },
                { label: "认领时间", value: fmtDateTime(alert.acknowledged_at) },
                { label: "认领人", value: alert.acknowledged_by_email || "—" },
                { label: "认领说明", value: alert.acknowledge_note || "—" },
                { label: "抑制到", value: fmtDateTime(alert.muted_until) },
                { label: "解决时间", value: fmtDateTime(alert.resolved_at) },
                { label: "解决人", value: alert.resolved_by_email || "—" },
                { label: "解决说明", value: alert.resolution_note || "—" },
                { label: "告警 ID", value: <EntityLink value={alert.id} /> },
              ]}
            />
            <RelatedChain related={detail.related ?? {}} />
            <div style={{ display: "flex", gap: "var(--sp-4)", flexWrap: "wrap" }}>
              <JsonBlock value={alert.labels ?? {}} label="labels" />
              <JsonBlock value={alert.links ?? {}} label="links" />
            </div>
            <p style={{ margin: 0, fontSize: "var(--fs-12)", color: "var(--c-slate)" }}>
              告警不可删除，处置通过认领 / 抑制 / 解决完成，原记录保留。
            </p>
          </div>
        ),
        actions: [{ label: "关闭", variant: "primary", result: "ok" }],
      });
    } catch (loadError) {
      const details: Array<{ label: string; value: ReactNode }> = [];
      if (loadError instanceof ApiError) {
        if (loadError.status) details.push({ label: "HTTP", value: String(loadError.status) });
        if (loadError.code) details.push({ label: "错误码", value: loadError.code });
        if (loadError.traceId) details.push({ label: "Trace", value: loadError.traceId });
      }
      dialog.notify(
        "error",
        "读取告警详情失败",
        loadError instanceof ApiError ? loadError.userMessage : "请稍后重试",
        details.length > 0 ? details : undefined,
      );
    } finally {
      setDetailLoadingId(null);
    }
  };

  const runCommand = async (row: AlertRow, command: "acknowledge" | "mute" | "resolve") => {
    const meta = COMMANDS[command];
    if (!guard(meta.permission)) return;
    let ttlMinutes: number | undefined;
    if (command === "mute") {
      const raw = dialogInputValue(
        await dialog.prompt({
          kind: "warning",
          title: "抑制时长",
          body: "抑制到期后记录保留，状态按到期时间自动失效。",
          input: {
            label: "分钟数",
            placeholder: "1 - 10080",
            pattern: /^[0-9]{1,5}$/,
            patternHint: "请输入 1-10080 之间的整数分钟",
          },
          submitLabel: "下一步",
        }),
      );
      if (!raw) return;
      const parsed = Number(raw);
      if (!Number.isFinite(parsed) || parsed <= 0 || parsed > 10080) {
        dialog.notify("warning", "抑制时长不合法", "分钟数必须是 1-10080 之间的整数。");
        return;
      }
      ttlMinutes = Math.round(parsed);
    }
    const note = dialogInputValue(
      await dialog.prompt({
        kind: "warning",
        title: meta.confirmTitle,
        body: "处置说明会写入审计日志，无法为空。",
        input: {
          label: "处置说明",
          placeholder: "例如：已定位到上游 429，正在切换 Provider",
          pattern: /^.{1,500}$/,
          patternHint: "1-500 个字符",
        },
        submitLabel: meta.label,
      }),
    );
    if (!note) return;
    setBusyId(row.id);
    try {
      const response = await client.post<AlertRow>(
        "/api/admin/v1/alerts/" + encodeURIComponent(row.id) + "/commands/" + command,
        command === "mute" ? { note, ttl_minutes: ttlMinutes } : { note },
        { idempotencyKey: newIdempotencyKey() },
      );
      dialog.notify("success", meta.doneTitle, null, [
        { label: "告警", value: row.title || row.id },
        { label: "优先级", value: response.data?.priority || row.priority || "—" },
        { label: "新状态", value: response.data?.status || "—" },
        ...(command === "mute" ? [{ label: "抑制到", value: fmtDateTime(response.data?.muted_until) }] : []),
      ]);
      list.reload();
    } catch (commandError) {
      const details: Array<{ label: string; value: ReactNode }> = [];
      let message = "请稍后重试";
      if (commandError instanceof ApiError) {
        message = commandError.userMessage;
        if (commandError.code === "CONFLICT_STATE") {
          message = "告警当前状态不允许该处置：" + commandError.userMessage;
        }
        if (commandError.status) details.push({ label: "HTTP", value: String(commandError.status) });
        if (commandError.code) details.push({ label: "错误码", value: commandError.code });
        if (commandError.traceId) details.push({ label: "Trace", value: commandError.traceId });
        if (commandError.details) {
          Object.entries(commandError.details).forEach(([key, value]) => {
            details.push({ label: key, value: typeof value === "object" ? JSON.stringify(value) : String(value) });
          });
        }
      }
      dialog.notify("error", "处置失败", message, details.length > 0 ? details : undefined);
    } finally {
      setBusyId(null);
    }
  };

  const explainScope = () => {
    dialog.notify("info", "告警口径", null, [
      { label: "接口", value: "GET /api/admin/v1/alerts" },
      { label: "排序", value: "优先级 P0 → P3，其次 last_seen_at 倒序" },
      { label: "P0 统计", value: "状态为 OPEN 且优先级为 P0（含历史 CRITICAL）的告警数" },
      { label: "状态", value: "OPEN 待处置 / ACK 已认领 / MUTED 已抑制 / RESOLVED 已解决" },
      { label: "处置", value: "认领、抑制、解决；三者都要求处置说明，原记录保留不可删除" },
      { label: "关联链", value: "详情按 Provider → request → job → shard → checkpoint → object 回查" },
    ]);
  };

  return (
    <>
      <PageHeader
        title="告警中心"
        subtitle={"P0 未处置 " + openP0 + " · 本页 " + list.items.length + " 条 · 来源 /api/admin/v1/alerts"}
        actions={
          <>
            {openP0 > 0 ? <StatusBadge tone="danger">P0 未处置 {openP0}</StatusBadge> : null}
            <button type="button" className="btn btn--sm" onClick={explainScope}>
              口径
            </button>
            <button type="button" className="btn btn--sm" onClick={list.reload} disabled={list.loading}>
              刷新
            </button>
          </>
        }
      />

      <Card title="筛选">
        <div className="toolbar" style={{ alignItems: "flex-end" }}>
          <div style={{ flex: "1 1 180px", minWidth: 0 }}>
            <Field label="状态" htmlFor="alerts-status">
              <select
                id="alerts-status"
                className="select"
                value={status}
                onChange={(event) => {
                  const value = event.target.value;
                  setStatus(value);
                  list.setFilter({ status: value === "all" ? undefined : value });
                }}
              >
                <option value="all">全部状态</option>
                {STATUS_OPTIONS.map((option) => (
                  <option key={option} value={option}>
                    {option}
                  </option>
                ))}
              </select>
            </Field>
          </div>
          <div style={{ flex: "1 1 180px", minWidth: 0 }}>
            <Field label="优先级" htmlFor="alerts-priority">
              <select
                id="alerts-priority"
                className="select"
                value={priority}
                onChange={(event) => {
                  const value = event.target.value;
                  setPriority(value);
                  list.setFilter({ priority: value === "all" ? undefined : value });
                }}
              >
                <option value="all">全部优先级</option>
                {PRIORITY_OPTIONS.map((option) => (
                  <option key={option} value={option}>
                    {option}
                  </option>
                ))}
              </select>
            </Field>
          </div>
        </div>
      </Card>

      <Card title={"告警（本页 " + list.items.length + "）"} flush>
        <DataTable<AlertRow>
          loading={list.loading}
          error={list.error}
          onRetry={list.reload}
          rows={list.items}
          rowKey={(row) => row.id}
          empty={<EmptyState title="没有符合条件的告警" />}
          columns={[
            {
              key: "priority",
              header: "优先级",
              width: "120px",
              render: (row) => (
                <span>
                  <StatusBadge tone={priorityTone(row.priority)}>{row.priority || row.severity || "—"}</StatusBadge>
                  <div style={{ fontSize: "var(--fs-11)", color: "var(--c-slate)" }}>{row.severity || "—"}</div>
                </span>
              ),
            },
            {
              key: "title",
              header: "告警",
              width: "280px",
              render: (row) => (
                <span>
                  <strong>{row.title || "—"}</strong>
                  <div className="mono" style={{ fontSize: "var(--fs-11)", color: "var(--c-slate)" }} title={row.message || ""}>
                    {row.message || "—"}
                  </div>
                </span>
              ),
            },
            {
              key: "status",
              header: "状态",
              width: "130px",
              render: (row) => (
                <span>
                  <StatusBadge tone={alertStatusTone(row.status)}>{row.status || "—"}</StatusBadge>
                  <div style={{ fontSize: "var(--fs-11)", color: "var(--c-slate)" }}>×{row.occurrence_count ?? 1}</div>
                </span>
              ),
            },
            {
              key: "source",
              header: "来源 / SLO",
              width: "180px",
              render: (row) => (
                <span>
                  <div>{row.source || "—"}</div>
                  <div className="mono" style={{ fontSize: "var(--fs-11)", color: "var(--c-slate)" }}>{row.slo_key || "—"}</div>
                </span>
              ),
            },
            {
              key: "last_seen",
              header: "最近出现",
              width: "150px",
              render: (row) => (
                <span>
                  <RelativeTime value={row.last_seen_at} />
                  <div style={{ fontSize: "var(--fs-11)", color: "var(--c-slate)" }}>{fmtDateTime(row.first_seen_at)}</div>
                </span>
              ),
            },
            {
              key: "trace",
              header: "Trace",
              width: "150px",
              render: (row) => (
                <span className="mono" style={{ fontSize: "var(--fs-11)" }} title={row.trace_id || ""}>
                  {row.trace_id ? row.trace_id.slice(0, 18) : "—"}
                </span>
              ),
            },
            {
              key: "actions",
              header: "操作",
              width: "260px",
              align: "right",
              render: (row) => (
                <span className="toolbar" style={{ justifyContent: "flex-end" }}>
                  <button
                    type="button"
                    className="btn btn--sm"
                    disabled={detailLoadingId === row.id}
                    onClick={() => void openDetail(row)}
                  >
                    详情
                  </button>
                  <button
                    type="button"
                    className="btn btn--sm"
                    disabled={!canManage || busyId === row.id}
                    onClick={() => void runCommand(row, "acknowledge")}
                  >
                    认领
                  </button>
                  <button
                    type="button"
                    className="btn btn--sm"
                    disabled={!canManage || busyId === row.id}
                    onClick={() => void runCommand(row, "mute")}
                  >
                    抑制
                  </button>
                  <button
                    type="button"
                    className="btn btn--sm btn--ghost"
                    disabled={!canManage || busyId === row.id}
                    onClick={() => void runCommand(row, "resolve")}
                  >
                    解决
                  </button>
                </span>
              ),
            },
          ]}
        />
        <div className="toolbar" style={{ padding: "var(--sp-3) var(--sp-4)", justifyContent: "space-between" }}>
          <span style={{ color: "var(--c-slate)", fontSize: "var(--fs-12)" }}>
            服务端按优先级排序 · 本页 {list.items.length} 条 · 单页上限 200
          </span>
        </div>
      </Card>
    </>
  );
}
