import { useCallback, useEffect, useState } from "react";
import { Link } from "react-router-dom";
import { ApiError, useAuth, type AdminOverview } from "@chain/api-client";
import { Card, EmptyState, ErrorState, Metric, PageHeader, SkeletonRows, StatusBadge, useDialog } from "@chain/ui";

/** §19.2 A-HOME-01 运营总览。所有指标来自真实聚合，无占位值。 */
export default function OverviewPage() {
  const { client } = useAuth();
  const dialog = useDialog();
  const [data, setData] = useState<AdminOverview | null>(null);
  const [error, setError] = useState<string | null>(null);
  const [loading, setLoading] = useState(true);

  const load = useCallback(async () => {
    setLoading(true);
    setError(null);
    try {
      const res = await client.get<AdminOverview>("/api/admin/v1/overview");
      setData(res.data);
    } catch (err) {
      setError(err instanceof ApiError ? err.userMessage : "无法加载运营总览");
    } finally {
      setLoading(false);
    }
  }, [client]);

  useEffect(() => {
    void load();
  }, [load]);

  const toneFor = (key: string): "ok" | "warn" | "danger" | "neutral" => {
    const value = data?.cards.find((c) => c.key === key)?.value ?? 0;
    if (key === "jobs_failed" || key === "login_failures_24h") return value > 0 ? "danger" : "ok";
    if (key === "jobs_running") return value > 0 ? "warn" : "neutral";
    return "neutral";
  };

  return (
    <>
      <PageHeader
        title="运营总览"
        subtitle={data ? <>数据截至 <span className="mono">{new Date(data.as_of).toLocaleString("zh-CN")}</span></> : "正在读取平台状态"}
        actions={
          <>
            <button type="button" className="btn btn--sm" onClick={load}>
              刷新
            </button>
            <button
              type="button"
              className="btn btn--sm"
              onClick={() =>
                dialog.notify("info", "指标口径", null, Object.entries(data?.data_source ?? {}).map(([k, v]) => ({ label: k, value: v })))
              }
            >
              指标口径
            </button>
          </>
        }
      />
      {loading ? (
        <SkeletonRows rows={3} columns={4} />
      ) : error ? (
        <Card>
          <ErrorState title="总览加载失败" message={error} onRetry={load} />
        </Card>
      ) : !data ? (
        <Card>
          <EmptyState title="暂无数据" />
        </Card>
      ) : (
        <>
          <div className="metric-grid">
            {data.cards.map((card) => (
              <Metric key={card.key} label={card.label} value={card.value} hint={card.unit} tone={toneFor(card.key)} />
            ))}
          </div>
          <Card title="平台状态">
            <div className="toolbar">
              <StatusBadge tone={data.status === "ok" ? "ok" : "warn"}>{data.status === "ok" ? "正常" : "注意"}</StatusBadge>
              <span style={{ color: "var(--c-slate)", fontSize: "var(--fs-12)" }}>
                当前身份角色：{data.my_roles.join(" / ") || "—"}
              </span>
              <span className="shell__spacer" />
              <Link className="btn btn--sm" to="/system">
                系统健康检查
              </Link>
              <Link className="btn btn--sm" to="/audit">
                审计日志
              </Link>
              <Link className="btn btn--sm" to="/login-events">
                登录记录
              </Link>
            </div>
          </Card>
        </>
      )}
    </>
  );
}
