import { useCallback, useEffect, useState } from "react";
import { ApiError, useAuth } from "@chain/api-client";
import { Card, DataTable, EmptyState, ErrorState, PageHeader, SkeletonRows, StatusBadge, statusToneFor, useDialog } from "@chain/ui";
import { fmtBytes, fmtDateTime, fmtNumber } from "../lib/adminApi";
import "../dataops.css";

/** §19.2 A-DATA-02 导出与对象存储：桶的真实用量 + 导出产物的真实记录。
 *  桶用量来自 MinIO 的有界列举（超上限如实标记），导出记录来自 PG exports。 */

interface BucketStat {
  name: string;
  objects: number;
  bytes: number;
  newest_at?: string;
  truncated: boolean;
  error?: string;
  description: string;
}

interface ExportRow {
  id: string;
  dataset: string;
  format: string;
  status: string;
  rows: number;
  bytes: number;
  object_ref: string;
  error?: string;
  created_at: string;
}

interface StorageOverview {
  as_of: string;
  buckets: BucketStat[];
  export_total: number;
  export_ready: number;
  export_failed: number;
  export_rows: number;
  recent_exports: ExportRow[];
  coverage_subjects: number;
  provider_raw_bytes: number;
  list_cap: number;
}

export default function StoragePage() {
  const { client } = useAuth();
  const dialog = useDialog();
  const [data, setData] = useState<StorageOverview | null>(null);
  const [loading, setLoading] = useState(true);
  const [error, setError] = useState<string | null>(null);

  const load = useCallback(async () => {
    setLoading(true);
    setError(null);
    try {
      const res = await client.get<StorageOverview>("/api/admin/v1/storage/overview");
      setData(res.data ?? null);
    } catch (err) {
      setError(err instanceof ApiError ? err.userMessage : "无法加载对象存储用量");
    } finally {
      setLoading(false);
    }
  }, [client]);

  useEffect(() => {
    void load();
  }, [load]);

  const explain = () =>
    dialog.open({
      kind: "info",
      title: "桶用量是怎么得到的",
      body: null,
      details: [
        { label: "来源", value: "对每个桶执行一次有界对象列举（含大小与最后修改时间）" },
        { label: "上限", value: `单桶最多枚举 ${data?.list_cap ?? 20000} 个对象或 6 秒；触顶的桶标记为「已截断」` },
        { label: "导出记录", value: "来自 PostgreSQL exports 表；产物大小按对象引用逐个回读，读不到就留空" },
        { label: "覆盖数据量", value: "dataset_coverage.raw_bytes 的合计，是平台从 Provider 取回的原始字节量" },
      ],
    });

  const showExport = async (row: ExportRow) => {
    await dialog.open({
      kind: row.status === "ready" ? "info" : "warning",
      title: `导出 ${row.dataset} · ${row.format}`,
      body: null,
      details: [
        { label: "状态", value: <StatusBadge tone={statusToneFor(row.status)}>{row.status}</StatusBadge> },
        { label: "行数", value: fmtNumber(row.rows) },
        { label: "产物大小", value: row.bytes > 0 ? fmtBytes(row.bytes) : "—" },
        { label: "对象引用", value: <span className="dops-mono">{row.object_ref || "—"}</span> },
        { label: "创建时间（UTC）", value: fmtDateTime(row.created_at) },
        { label: "错误", value: row.error || "—" },
        { label: "导出 ID", value: <span className="dops-mono">{row.id}</span> },
      ],
    });
  };

  const totalBytes = data?.buckets.reduce((sum, bucket) => sum + bucket.bytes, 0) ?? 0;
  const totalObjects = data?.buckets.reduce((sum, bucket) => sum + bucket.objects, 0) ?? 0;

  return (
    <div className="dops">
      <PageHeader
        title="导出与对象存储"
        subtitle="桶的真实用量与导出产物的真实记录；清单有界，触顶会如实标注。"
        actions={
          <button type="button" className="btn btn--ghost" onClick={explain}>
            口径说明
          </button>
        }
      />

      {error ? <ErrorState title="对象存储用量加载失败" message={error} onRetry={() => void load()} /> : null}
      {loading && !data ? <SkeletonRows rows={4} columns={4} /> : null}

      {data ? (
        <>
          <div className="dops-kpis">
            <div className="dops-kpi" data-tone="ok">
              <div className="dops-kpi__label">对象总数</div>
              <div className="dops-kpi__value">{fmtNumber(totalObjects)}</div>
              <div className="dops-kpi__hint">{fmtBytes(totalBytes)} · {data.buckets.length} 个桶</div>
            </div>
            <div className="dops-kpi">
              <div className="dops-kpi__label">导出产物</div>
              <div className="dops-kpi__value">{fmtNumber(data.export_total)}</div>
              <div className="dops-kpi__hint">就绪 {data.export_ready} · 其他 {data.export_failed}</div>
            </div>
            <div className="dops-kpi" data-tone="warn">
              <div className="dops-kpi__label">覆盖主体数</div>
              <div className="dops-kpi__value">{fmtNumber(data.coverage_subjects)}</div>
              <div className="dops-kpi__hint">原始取回 {fmtBytes(data.provider_raw_bytes)}</div>
            </div>
            <div className="dops-kpi">
              <div className="dops-kpi__label">快照时间（UTC）</div>
              <div className="dops-kpi__value" style={{ fontSize: "var(--fs-16)" }}>{fmtDateTime(data.as_of)}</div>
              <div className="dops-kpi__hint">按需读取，无缓存</div>
            </div>
          </div>

          <div className="dops-grid">
            {data.buckets.map((bucket) => (
              <div key={bucket.name} className="bucket">
                <div className="bucket__head">
                  <span className="bucket__name">{bucket.name}</span>
                  {bucket.truncated ? <span className="bucket__flag" data-kind="truncated">已截断（达到枚举上限）</span> : null}
                  {bucket.error ? <span className="bucket__flag" data-kind="error">{bucket.error}</span> : null}
                </div>
                <div className="bucket__desc">{bucket.description}</div>
                <div className="bucket__figures">
                  <span className="bucket__figure"><b>{fmtNumber(bucket.objects)}</b><span>对象</span></span>
                  <span className="bucket__figure"><b>{fmtBytes(bucket.bytes)}</b><span>容量</span></span>
                  <span className="bucket__figure">
                    <b style={{ fontSize: "var(--fs-13)" }}>{bucket.newest_at ? fmtDateTime(bucket.newest_at) : "—"}</b>
                    <span>最新写入</span>
                  </span>
                </div>
                <div className="bucket__bar">
                  <i style={{ width: `${totalBytes > 0 ? Math.max(2, Math.round((bucket.bytes / totalBytes) * 100)) : 2}%` }} />
                </div>
              </div>
            ))}
          </div>

          <Card title="最近导出">
            <DataTable<ExportRow>
              columns={[
                { key: "created_at", header: "创建时间", width: "150px", render: (row) => fmtDateTime(row.created_at) },
                { key: "dataset", header: "数据集", render: (row) => <span className="dops-mono">{row.dataset}</span> },
                { key: "format", header: "格式", width: "90px", render: (row) => row.format || "—" },
                { key: "status", header: "状态", width: "110px", render: (row) => <StatusBadge tone={statusToneFor(row.status)}>{row.status}</StatusBadge> },
                { key: "rows", header: "行数", width: "110px", render: (row) => <span className="dops-num" style={{ display: "block" }}>{fmtNumber(row.rows)}</span> },
                { key: "bytes", header: "大小", width: "110px", render: (row) => (row.bytes > 0 ? fmtBytes(row.bytes) : "—") },
                {
                  key: "detail",
                  header: "取证",
                  width: "84px",
                  render: (row) => (
                    <button type="button" className="btn btn--ghost btn--sm" onClick={() => void showExport(row)}>
                      详情
                    </button>
                  ),
                },
              ]}
              rows={data.recent_exports}
              rowKey={(row) => row.id}
              loading={loading}
              error={null}
              empty={<EmptyState title="还没有导出产物" />}
              onRetry={() => void load()}
            />
          </Card>
        </>
      ) : null}
    </div>
  );
}
