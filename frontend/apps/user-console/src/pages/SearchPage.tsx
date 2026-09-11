import { useState } from "react";
import { ApiError, useAuth } from "@chain/api-client";
import { Card, EmptyState, ErrorState, PageHeader, SkeletonRows } from "@chain/ui";

interface SearchHit {
  type: string;
  id: string;
  title: string;
  subtitle: string;
}

/** §21.5 GET /api/v2/search —— 服务端按权限裁剪结果，前端不做任何越权拼装。 */
export default function SearchPage() {
  const { client } = useAuth();
  const [query, setQuery] = useState("");
  const [hits, setHits] = useState<SearchHit[] | null>(null);
  const [scopes, setScopes] = useState<string[]>([]);
  const [loading, setLoading] = useState(false);
  const [error, setError] = useState<string | null>(null);

  const run = async (event: React.FormEvent) => {
    event.preventDefault();
    const q = query.trim();
    if (q.length < 2) {
      setError(null);
      setHits([]);
      return;
    }
    setLoading(true);
    setError(null);
    try {
      const res = await client.get<{ query: string; items: SearchHit[]; scopes: string[] }>("/api/v2/search", { query: { q } });
      setHits(res.data?.items ?? []);
      setScopes(res.data?.scopes ?? []);
    } catch (err) {
      setError(err instanceof ApiError ? err.userMessage : "搜索失败");
    } finally {
      setLoading(false);
    }
  };

  return (
    <>
      <PageHeader title="全局搜索" subtitle="当前仅覆盖案件与调查任务；地址/交易/报告的检索在 S5 分批开放。" />
      <Card>
        <form className="toolbar" onSubmit={run} role="search">
          <input
            className="input"
            style={{ maxWidth: 420 }}
            aria-label="搜索关键词"
            value={query}
            onChange={(e) => setQuery(e.target.value)}
            placeholder="输入案件名称、编号或调查目标"
          />
          <button className="btn btn--primary" type="submit" disabled={loading}>
            {loading ? "搜索中…" : "搜索"}
          </button>
        </form>
      </Card>
      <Card flush>
        {loading ? (
          <SkeletonRows rows={4} columns={3} />
        ) : error ? (
          <ErrorState title="搜索失败" message={error} onRetry={() => void run(new Event("submit") as unknown as React.FormEvent)} />
        ) : hits === null ? (
          <EmptyState title="输入关键词开始搜索" body="至少 2 个字符。搜索会记录审计事件，但不记录结果正文。" />
        ) : hits.length === 0 ? (
          <EmptyState title="没有匹配结果" body="尝试缩短关键词，或确认对象属于当前工作区。" />
        ) : (
          <ul style={{ listStyle: "none", margin: 0, padding: 0 }}>
            {hits.map((hit) => (
              <li key={`${hit.type}-${hit.id}`} style={{ padding: "var(--sp-3) var(--sp-4)", borderBottom: "1px solid var(--c-border)" }}>
                <span className="badge badge--info">{hit.type}</span> <strong>{hit.title}</strong>
                {hit.subtitle ? <span style={{ color: "var(--c-slate)", marginLeft: 8 }}>{hit.subtitle}</span> : null}
                <div className="mono" style={{ fontSize: "var(--fs-11)", color: "var(--c-slate)" }}>
                  {hit.id}
                </div>
              </li>
            ))}
          </ul>
        )}
      </Card>
      {scopes.length > 0 ? <div className="visually-hidden">搜索范围：{scopes.join(", ")}</div> : null}
    </>
  );
}
