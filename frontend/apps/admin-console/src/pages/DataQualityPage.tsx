import { useCallback, useEffect, useState } from "react";
import { ApiError, useAuth } from "@chain/api-client";
import { Card, DataTable, EmptyState, ErrorState, PageHeader, SkeletonRows, StatusBadge, useDialog } from "@chain/ui";
import { fmtBytes, fmtDateTime, fmtNumber } from "../lib/adminApi";
import "../dataops.css";

/** §19.2 A-DATA-03 Coverage 与数据质量：ClickHouse 表的真实规模与分片健康、
 *  各链各数据集的行数与区块跨度、PG dataset_coverage 的覆盖与缺口。 */

interface TableStat { table: string; parts: number; rows: number; bytes: number }
interface ChainStat { chain: string; dataset: string; rows: number; min_block: number; max_block: number; oldest_at: string; newest_at: string }
interface CoverageStat { chain: string; dataset: string; subjects: number; complete: number; gapped: number; rows: number; raw_bytes: number; last_committed_at?: string }
interface CoverageGap { chain: string; dataset: string; subject: string; to_block: number; head_block: number; missing_blocks: number; updated_at: string }

interface DataQualityOverview {
  as_of: string;
  tables: TableStat[];
  chains: ChainStat[];
  coverage: CoverageStat[];
  gaps: CoverageGap[];
  part_warn_level: number;
}

function partsLevel(parts: number, warn: number): "ok" | "info" | "warn" | "danger" {
  if (parts >= warn * 2) return "danger";
  if (parts >= warn) return "warn";
  if (parts >= warn / 2) return "info";
  return "ok";
}

export default function DataQualityPage() {
  const { client } = useAuth();
  const dialog = useDialog();
  const [data, setData] = useState<DataQualityOverview | null>(null);
  const [loading, setLoading] = useState(true);
  const [error, setError] = useState<string | null>(null);

  const load = useCallback(async () => {
    setLoading(true);
    setError(null);
    try {
      const res = await client.get<DataQualityOverview>("/api/admin/v1/data-quality/overview");
      setData(res.data ?? null);
    } catch (err) {
      setError(err instanceof ApiError ? err.userMessage : "无法加载数据质量概览");
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
      title: "这些口径分别来自哪里",
      body: null,
      details: [
        { label: "表规模与分片", value: "ClickHouse system.parts（active 分区）：行数、磁盘字节、part 数" },
        { label: "part 数为什么重要", value: "part 过多会让 FINAL 合并与查询变慢、内存占用上升；阈值与配色见列表左侧圆点" },
        { label: "链 × 数据集", value: "直接对 blockchain.* 表按 chain 聚合出行数与区块/时间跨度" },
        { label: "Coverage", value: "PostgreSQL dataset_coverage：按 主体 × 数据集 记录的已提交覆盖区间" },
        { label: "缺口", value: "head_block > to_block 的记录，差额即尚未覆盖的区块数（不是估算）" },
      ],
    });

  const totalRows = data?.tables.reduce((sum, t) => sum + t.rows, 0) ?? 0;
  const totalBytes = data?.tables.reduce((sum, t) => sum + t.bytes, 0) ?? 0;
  const totalParts = data?.tables.reduce((sum, t) => sum + t.parts, 0) ?? 0;
  const warn = data?.part_warn_level ?? 300;

  return (
    <div className="dops">
      <PageHeader
        title="Coverage 与数据质量"
        subtitle="表规模、分片健康、各链覆盖与仍未覆盖的缺口——全部是实读数字。"
        actions={
          <button type="button" className="btn btn--ghost" onClick={explain}>
            口径说明
          </button>
        }
      />

      {error ? <ErrorState title="数据质量概览加载失败" message={error} onRetry={() => void load()} /> : null}
      {loading && !data ? <SkeletonRows rows={5} columns={4} /> : null}

      {data ? (
        <>
          <div className="dops-kpis">
            <div className="dops-kpi">
              <div className="dops-kpi__label">ClickHouse 行数</div>
              <div className="dops-kpi__value">{fmtNumber(totalRows)}</div>
              <div className="dops-kpi__hint">{data.tables.length} 张表</div>
            </div>
            <div className="dops-kpi">
              <div className="dops-kpi__label">磁盘占用</div>
              <div className="dops-kpi__value">{fmtBytes(totalBytes)}</div>
              <div className="dops-kpi__hint">未压缩前 · system.parts</div>
            </div>
            <div className="dops-kpi" data-tone={totalParts >= warn * 3 ? "danger" : totalParts >= warn * 2 ? "warn" : "ok"}>
              <div className="dops-kpi__label">分片（part）总数</div>
              <div className="dops-kpi__value">{fmtNumber(totalParts)}</div>
              <div className="dops-kpi__hint">单表 ≥ {warn} 起警示</div>
            </div>
            <div className="dops-kpi" data-tone={data.gaps.length > 0 ? "warn" : "ok"}>
              <div className="dops-kpi__label">未覆盖缺口</div>
              <div className="dops-kpi__value">{data.gaps.length}</div>
              <div className="dops-kpi__hint">{data.gaps.length > 0 ? "见下方缺口清单" : "当前无缺口"}</div>
            </div>
          </div>

          <Card title="表健康（system.parts）" actions={<span className="dops-note">圆点：绿 正常 / 蓝 关注 / 琥珀 警示 / 红 需处置</span>}>
            <div className="dops-matrix">
              <div className="dops-matrix__row dops-matrix__head">
                <span>表</span><span>分片</span><span>行数</span><span>磁盘</span><span className="dops-num">健康</span>
              </div>
              {data.tables.map((table) => (
                <div key={table.table} className="dops-matrix__row">
                  <span className="dops-mono">{table.table}</span>
                  <span className="dops-num" style={{ textAlign: "left" }}>{fmtNumber(table.parts)}</span>
                  <span className="dops-num" style={{ textAlign: "left" }}>{fmtNumber(table.rows)}</span>
                  <span>{fmtBytes(table.bytes)}</span>
                  <span className="dops-num">
                    <i className="hdot" data-level={partsLevel(table.parts, warn)} />
                  </span>
                </div>
              ))}
            </div>
          </Card>

          <Card title="链 × 数据集">
            <DataTable<ChainStat>
              columns={[
                { key: "chain", header: "链", width: "110px", render: (row) => <span className="dops-mono">{row.chain}</span> },
                { key: "dataset", header: "数据集", render: (row) => <span className="dops-mono">{row.dataset}</span> },
                { key: "rows", header: "行数", width: "120px", render: (row) => <span className="dops-num" style={{ display: "block" }}>{fmtNumber(row.rows)}</span> },
                { key: "blocks", header: "区块范围", width: "220px", render: (row) => <span className="dops-mono">{fmtNumber(row.min_block)} → {fmtNumber(row.max_block)}</span> },
                { key: "time", header: "时间跨度", render: (row) => <span className="dops-note">{fmtDateTime(row.oldest_at)} → {fmtDateTime(row.newest_at)}</span> },
              ]}
              rows={data.chains}
              rowKey={(row) => row.chain + row.dataset}
              loading={loading}
              error={null}
              empty={<EmptyState title="没有可读的链数据" body="ClickHouse 尚未返回任何分组结果。" />}
              onRetry={() => void load()}
            />
          </Card>

          <Card title="Coverage 汇总">
            <div className="dops-matrix">
              <div className="dops-matrix__row dops-matrix__head" style={{ gridTemplateColumns: "minmax(70px,.6fr) minmax(110px,1fr) minmax(70px,.6fr) minmax(80px,.7fr) minmax(80px,.7fr) minmax(90px,1fr)" }}>
                <span>链</span><span>数据集</span><span>主体</span><span>已完成</span><span>有缺口</span><span>最近提交</span>
              </div>
              {data.coverage.map((row) => (
                <div
                  key={row.chain + row.dataset}
                  className="dops-matrix__row"
                  style={{ gridTemplateColumns: "minmax(70px,.6fr) minmax(110px,1fr) minmax(70px,.6fr) minmax(80px,.7fr) minmax(80px,.7fr) minmax(90px,1fr)" }}
                >
                  <span className="dops-mono">{row.chain}</span>
                  <span className="dops-mono">{row.dataset}</span>
                  <span className="dops-num" style={{ textAlign: "left" }}>{fmtNumber(row.subjects)}</span>
                  <span className="dops-num" style={{ textAlign: "left" }}>{fmtNumber(row.complete)}</span>
                  <span className="dops-num" style={{ textAlign: "left", color: row.gapped > 0 ? "var(--c-amber-text)" : undefined }}>
                    {fmtNumber(row.gapped)}
                  </span>
                  <span className="dops-note">{row.last_committed_at ? fmtDateTime(row.last_committed_at) : "—"}</span>
                </div>
              ))}
              {data.coverage.length === 0 ? <EmptyState title="还没有 Coverage 记录" body="完成一次覆盖任务后这里会出现对应主体。" /> : null}
            </div>
          </Card>

          <Card title="未覆盖缺口" actions={<span className="dops-note">head_block 大于已覆盖 to_block 的主体</span>}>
            <DataTable<CoverageGap>
              columns={[
                { key: "chain", header: "链", width: "90px", render: (row) => <span className="dops-mono">{row.chain}</span> },
                { key: "dataset", header: "数据集", width: "170px", render: (row) => <span className="dops-mono">{row.dataset}</span> },
                { key: "subject", header: "主体", render: (row) => <span className="dops-mono">{row.subject}</span> },
                { key: "to_block", header: "已覆盖至", width: "130px", render: (row) => <span className="dops-num" style={{ display: "block" }}>{fmtNumber(row.to_block)}</span> },
                { key: "head_block", header: "链头", width: "130px", render: (row) => <span className="dops-num" style={{ display: "block" }}>{fmtNumber(row.head_block)}</span> },
                {
                  key: "missing",
                  header: "缺口",
                  width: "120px",
                  render: (row) => <StatusBadge tone="warn">{fmtNumber(row.missing_blocks)} 块</StatusBadge>,
                },
              ]}
              rows={data.gaps}
              rowKey={(row) => row.chain + row.dataset + row.subject}
              loading={loading}
              error={null}
              empty={<EmptyState title="没有未覆盖缺口" body="所有已提交主体的覆盖区间都追上了当时的链头。" />}
              onRetry={() => void load()}
            />
          </Card>
        </>
      ) : null}
    </div>
  );
}
