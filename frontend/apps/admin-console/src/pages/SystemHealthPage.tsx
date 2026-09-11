import { useCallback, useEffect, useState } from "react";
import { ApiError, useAuth } from "@chain/api-client";
import { Card, ErrorState, EvidenceRail, Metric, PageHeader, StatusBadge, SkeletonRows, useDialog } from "@chain/ui";

interface HealthResponse {
  checks: Record<string, string>;
  auth_mode: string;
  env: string;
}

/** §19.2 系统健康（A-HOME-02）。只展示真实探针结果，不伪造状态。 */
export default function SystemHealthPage() {
  const { client, session } = useAuth();
  const dialog = useDialog();
  const [data, setData] = useState<HealthResponse | null>(null);
  const [error, setError] = useState<string | null>(null);
  const [loading, setLoading] = useState(true);

  const load = useCallback(async () => {
    setLoading(true);
    setError(null);
    try {
      const res = await client.get<HealthResponse>("/api/admin/v1/system/health");
      setData(res.data);
    } catch (err) {
      setError(err instanceof ApiError ? err.userMessage : "无法加载系统健康状态");
    } finally {
      setLoading(false);
    }
  }, [client]);

  useEffect(() => {
    void load();
  }, [load]);

  const tone = (value: string) => (value === "ok" ? "ok" : value.startsWith("degraded") ? "warn" : "danger");

  return (
    <div className="workbench">
      <div style={{ flex: 1, minWidth: 0, display: "flex", flexDirection: "column", gap: "var(--sp-5)" }}>
        <PageHeader
          title="系统健康"
          subtitle="依赖探针来自 /api/admin/v1/system/health"
          actions={
            <>
              <button className="btn btn--sm" onClick={load}>
                刷新
              </button>
              <button
                type="button"
                className="btn btn--sm"
                onClick={() =>
                  dialog.notify("info", "探针口径", null, [
                    { label: "PostgreSQL", value: "pgxpool Ping" },
                    { label: "ClickHouse", value: "SELECT 1" },
                    { label: "NATS", value: "客户端启用状态（不代表 JetStream 已就绪）" },
                    { label: "MinIO", value: "客户端已初始化（未做对象级探测）" },
                  ])
                }
              >
                探针口径
              </button>
            </>
          }
        />
        {loading ? (
          <SkeletonRows rows={3} columns={3} />
        ) : error ? (
          <Card>
            <ErrorState title="健康检查失败" message={error} onRetry={load} />
          </Card>
        ) : !data ? null : (
          <>
            <div className="metric-grid">
              {Object.entries(data.checks).map(([name, value]) => (
                <Metric key={name} label={name} value={<span style={{ fontSize: "var(--fs-18)" }}>{value}</span>} tone={tone(value)} />
              ))}
            </div>
            <Card title="运行配置">
              <div className="toolbar">
                <StatusBadge tone={data.env === "production" ? "ok" : "warn"}>APP_ENV={data.env}</StatusBadge>
                <StatusBadge tone={data.auth_mode === "session" ? "ok" : "warn"}>AUTH_MODE={data.auth_mode}</StatusBadge>
                {data.auth_mode !== "session" ? (
                  <button
                    type="button"
                    className="btn btn--sm"
                    onClick={() =>
                      dialog.notify("warning", "认证模式不是最严格档位", "当前仍允许在缺少会话时回退到兼容身份头。生产环境必须设置 AUTH_MODE=session。")
                    }
                  >
                    为什么需要注意
                  </button>
                ) : null}
              </div>
            </Card>
          </>
        )}
      </div>

      <EvidenceRail
        title="会话与权限"
        items={[
          { label: "会话 ID", value: session?.session_id ?? "—" },
          { label: "受众", value: session?.audience ?? "—" },
          { label: "角色", value: session?.roles.join(", ") || "—" },
          { label: "权限数", value: String(session?.permissions.length ?? 0) },
          { label: "组织", value: session?.organization_id ?? "—" },
          { label: "工作区", value: session?.workspace_id ?? "—" },
          { label: "会话到期", value: session ? new Date(session.expires_at).toLocaleString("zh-CN") : "—" },
        ]}
      />
    </div>
  );
}
