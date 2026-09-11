import { useCallback, useEffect, useState } from "react";
import { Link } from "react-router-dom";
import { ApiError, useAuth, type UserDashboard } from "@chain/api-client";
import { Card, EmptyState, ErrorState, Metric, PageHeader, SkeletonRows, useDialog } from "@chain/ui";

/** §18.3 U-HOME-01 我的工作台。所有数字都来自 /api/v2/dashboard 的真实聚合。 */
export default function HomePage() {
  const { client, session } = useAuth();
  const dialog = useDialog();
  const [data, setData] = useState<UserDashboard | null>(null);
  const [error, setError] = useState<string | null>(null);
  const [loading, setLoading] = useState(true);

  const load = useCallback(async () => {
    setLoading(true);
    setError(null);
    try {
      const { data } = await client.get<UserDashboard>("/api/v2/dashboard");
      setData(data);
    } catch (err) {
      setError(err instanceof ApiError ? err.userMessage : "无法加载工作台数据");
    } finally {
      setLoading(false);
    }
  }, [client]);

  useEffect(() => {
    void load();
  }, [load]);

  const toneFor = (key: string): "ok" | "warn" | "danger" | "neutral" => {
    if (key === "jobs_failed") return (data?.cards.find((c) => c.key === key)?.value ?? 0) > 0 ? "danger" : "ok";
    if (key === "active_jobs") return (data?.cards.find((c) => c.key === key)?.value ?? 0) > 0 ? "warn" : "neutral";
    return "neutral";
  };

  return (
    <>
      <PageHeader
        title={`你好，${session?.display_name ?? ""}`}
        subtitle={
          data ? (
            <>
              数据截至 <span className="mono">{new Date(data.as_of).toLocaleString("zh-CN")}</span> · 数据源{" "}
              <span className="mono">{data.data_source}</span>
            </>
          ) : (
            "正在读取个人概览"
          )
        }
        actions={
          <button
            type="button"
            className="btn btn--sm"
            onClick={() =>
              dialog.notify("info", "工作台口径", null, [
                { label: "组织", value: data?.scope.organization_id ?? "—" },
                { label: "工作区", value: data?.scope.workspace_id ?? "—" },
                { label: "统计范围", value: "当前工作区内未归档对象" },
              ])
            }
          >
            口径说明
          </button>
        }
      />

      {loading ? (
        <SkeletonRows rows={3} columns={4} />
      ) : error ? (
        <Card>
          <ErrorState title="工作台数据加载失败" message={error} onRetry={load} />
        </Card>
      ) : !data ? (
        <Card>
          <EmptyState title="暂无数据" body="当前工作区还没有可统计的对象。" />
        </Card>
      ) : (
        <>
          <div className="metric-grid">
            {data.cards.map((card) => (
              <Metric key={card.key} label={card.label} value={card.value} hint={card.unit} tone={toneFor(card.key)} />
            ))}
          </div>
          <Card title="快捷入口">
            {data.quick_actions.length === 0 ? (
              <EmptyState title="没有可用的快捷入口" body="当前角色未被授予任何创建类权限。" />
            ) : (
              <div className="toolbar">
                {data.quick_actions.map((action) => {
                  const isLegacy = ["download.create", "investigation.create", "report.create", "search.global"].includes(action.key);
                  return isLegacy ? (
                    <a key={action.key} className="btn btn--sm" href={action.href.startsWith("/admin") ? action.href : "/"}>
                      {action.label}
                      <span className="badge badge--neutral" style={{ marginLeft: 6 }}>
                        旧工作台
                      </span>
                    </a>
                  ) : (
                    <Link key={action.key} className="btn btn--sm" to={action.href}>
                      {action.label}
                    </Link>
                  );
                })}
              </div>
            )}
          </Card>
        </>
      )}
    </>
  );
}
