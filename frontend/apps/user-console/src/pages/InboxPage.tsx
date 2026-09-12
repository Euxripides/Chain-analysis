import { Link } from "react-router-dom";
import { Card, EmptyState, ErrorState, PageHeader, SkeletonRows, StatusBadge, statusToneFor } from "@chain/ui";
import { formatTime, useAsync } from "../sprint4/common";

interface InboxItem {
  id: string;
  resource_type: string;
  resource_id: string;
  case_id?: string | null;
  investigation_id?: string | null;
  requester: string;
  status: string;
  reason: string;
  created_at: string;
  title: string;
}

function targetFor(item: InboxItem): string {
  switch (item.resource_type) {
    case "finding":
      return `/findings/${item.resource_id}`;
    case "report":
      return `/reports/${item.resource_id}`;
    case "investigation":
      return `/investigations/${item.resource_id}/workbench`;
    case "evidence":
      return `/investigations/${item.investigation_id}/evidence/${item.resource_id}`;
    default:
      return "";
  }
}

export default function InboxPage() {
  const { data, loading, error, reload } = useAsync<InboxItem[]>("/api/v2/inbox", [], { status: "pending", limit: 200 });
  return (
    <>
      <PageHeader title="待我处理" subtitle={`${(data ?? []).length} 条待复核请求 · 数据源 postgres:review_requests`} />
      <Card>
        {loading ? <SkeletonRows rows={5} columns={3} /> : null}
        {error ? <ErrorState title="Inbox 加载失败" message={error} onRetry={reload} /> : null}
        {!loading && !error && (data ?? []).length === 0 ? <EmptyState title="没有待处理的复核请求" /> : null}
        <div className="s4-list">
          {(data ?? []).map((item) => (
            <div className="s4-list__item" key={item.id}>
              <div className="s4-list__main">
                <div className="s4-list__title">{item.title || item.resource_type}</div>
                <div className="s4-list__sub">
                  {item.resource_type} · 请求人 {item.requester || "system"} · {formatTime(item.created_at)}
                  {item.reason ? ` · ${item.reason}` : ""}
                </div>
              </div>
              <StatusBadge tone={statusToneFor(item.status)}>{item.status}</StatusBadge>
              <span className="s4-list__actions">
                <Link className="btn btn--sm btn--primary" to={targetFor(item)}>
                  处理
                </Link>
              </span>
            </div>
          ))}
        </div>
      </Card>
    </>
  );
}
