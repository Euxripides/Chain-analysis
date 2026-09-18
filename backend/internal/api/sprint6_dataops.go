package api

// ---- 数据运维面：导出与对象存储（admin:storage:read）+ Coverage 与数据质量（admin:coverage:read） ----
//
// 两个页面此前都只有占位符。这里补齐真实数据源：
//   * 对象存储：MinIO 桶的有界列举（对象数/字节/最新对象，超上限就如实标记 truncated）
//     + PG exports 的聚合与最近导出；
//   * 数据质量：ClickHouse system.parts（每表行数/磁盘/part 数——part 数是真实的健康信号）
//     + 各链各数据集的行数与区块/时间跨度 + PG dataset_coverage 的覆盖与缺口。
// 全部只读、有界；任何一路失败都只影响它自己的分区，不会把整页变成 500。

import (
	"context"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/minio/minio-go/v7"
)

const (
	storageListCap    = 20000
	storageListBudget = 6 * time.Second
	storageRecent     = 20
	dataQualityTables = 12
)

func (s *Server) registerAdminDataOpsRoutes(r chi.Router) {
	r.Get("/storage/overview", s.require("admin:storage:read", s.handleAdminStorageOverview))
	r.Get("/data-quality/overview", s.require("admin:coverage:read", s.handleAdminDataQualityOverview))
}

type bucketStat struct {
	Name        string    `json:"name"`
	Objects     int       `json:"objects"`
	Bytes       int64     `json:"bytes"`
	Newest      time.Time `json:"newest_at,omitempty"`
	Truncated   bool      `json:"truncated"`
	Error       string    `json:"error,omitempty"`
	Description string    `json:"description"`
}

// listBucket 有界列举：超过上限或超过时间预算就停下并如实报告 truncated，绝不无限扫描。
func listBucket(ctx context.Context, client *minio.Client, bucket, description string) bucketStat {
	stat := bucketStat{Name: bucket, Description: description}
	if client == nil || strings.TrimSpace(bucket) == "" {
		stat.Error = "对象存储客户端或桶名未配置"
		return stat
	}
	ctx, cancel := context.WithTimeout(ctx, storageListBudget)
	defer cancel()
	for obj := range client.ListObjects(ctx, bucket, minio.ListObjectsOptions{Recursive: true}) {
		if obj.Err != nil {
			stat.Error = obj.Err.Error()
			break
		}
		stat.Objects++
		stat.Bytes += obj.Size
		if obj.LastModified.After(stat.Newest) {
			stat.Newest = obj.LastModified
		}
		if stat.Objects >= storageListCap {
			stat.Truncated = true
			break
		}
	}
	return stat
}

type storageExportRow struct {
	ID        string    `json:"id"`
	Dataset   string    `json:"dataset"`
	Format    string    `json:"format"`
	Status    string    `json:"status"`
	Rows      int64     `json:"rows"`
	Bytes     int64     `json:"bytes"`
	ObjectRef string    `json:"object_ref"`
	Error     string    `json:"error,omitempty"`
	CreatedAt time.Time `json:"created_at"`
}

func (s *Server) handleAdminStorageOverview(w http.ResponseWriter, r *http.Request) {
	if !s.adminDBReady(w, r) {
		return
	}
	if s.deps.Raw == nil {
		respondError(w, r, http.StatusServiceUnavailable, "INTERNAL_NOT_READY", "对象存储未就绪", nil)
		return
	}
	ctx := r.Context()
	client := s.deps.Raw.Client()
	buckets := []bucketStat{
		listBucket(ctx, client, s.deps.Cfg.RawBucket, "原始档（含导出产物历史回退读）"),
		listBucket(ctx, client, s.deps.Cfg.ExportBucket, "导出产物（当前写桶）"),
		listBucket(ctx, client, s.deps.Cfg.BackupsBucket, "备份"),
	}

	var exportTotal, exportReady, exportFailed, exportBytes int64
	// exports.status 的真实取值是 COMPLETED / FAILED（实测），不是 "ready"。
	if err := s.deps.Store.Pool.QueryRow(ctx, "SELECT count(*),"+
		" count(*) FILTER (WHERE status = 'COMPLETED'),"+
		" count(*) FILTER (WHERE status = 'FAILED'),"+
		" coalesce(sum(rows),0) FROM exports").Scan(&exportTotal, &exportReady, &exportFailed, &exportBytes); err != nil {
		respondError(w, r, http.StatusInternalServerError, "INTERNAL_ERROR", "读取导出记录失败", nil)
		return
	}

	recent := []storageExportRow{}
	rows, err := s.deps.Store.Pool.Query(ctx, "SELECT id::text, coalesce(dataset,''), coalesce(format,''), coalesce(status,''),"+
		" coalesce(rows,0), coalesce(object_ref,''), coalesce(error,''), created_at FROM exports"+
		" ORDER BY created_at DESC LIMIT "+strconv.Itoa(storageRecent))
	if err != nil {
		respondError(w, r, http.StatusInternalServerError, "INTERNAL_ERROR", "读取最近导出失败", nil)
		return
	}
	for rows.Next() {
		var item storageExportRow
		if err := rows.Scan(&item.ID, &item.Dataset, &item.Format, &item.Status, &item.Rows,
			&item.ObjectRef, &item.Error, &item.CreatedAt); err != nil {
			rows.Close()
			respondError(w, r, http.StatusInternalServerError, "INTERNAL_ERROR", "读取最近导出失败", nil)
			return
		}
		// 产物可能落在导出桶（当前写桶）或原始桶（历史），逐个桶回读真实大小。
		if item.ObjectRef != "" {
			for _, bucket := range []string{s.deps.Cfg.ExportBucket, s.deps.Cfg.RawBucket} {
				if bucket == "" {
					continue
				}
				info, statErr := client.StatObject(ctx, bucket, item.ObjectRef, minio.StatObjectOptions{})
				if statErr == nil {
					item.Bytes = info.Size
					break
				}
			}
		}
		recent = append(recent, item)
	}
	rows.Close()

	var rawBytes int64
	var coveredSubjects int
	_ = s.deps.Store.Pool.QueryRow(ctx, "SELECT coalesce(sum(raw_bytes),0), count(*) FROM dataset_coverage").
		Scan(&rawBytes, &coveredSubjects)

	s.writeAuditNow(r, "admin:storage:read", "admin.data", "object_storage", "",
		map[string]any{"buckets": len(buckets), "exports": exportTotal})

	writeJSON(w, http.StatusOK, map[string]any{"data": map[string]any{
		"as_of":              time.Now().UTC(),
		"buckets":            buckets,
		"export_total":       exportTotal,
		"export_ready":       exportReady,
		"export_failed":      exportFailed,
		"export_rows":        exportBytes,
		"recent_exports":     recent,
		"coverage_subjects":  coveredSubjects,
		"provider_raw_bytes": rawBytes,
		"list_cap":           storageListCap,
	}})
}

type tableStat struct {
	Table string `json:"table"`
	Parts int64  `json:"parts"`
	Rows  int64  `json:"rows"`
	Bytes int64  `json:"bytes"`
}

type chainStat struct {
	Chain    string    `json:"chain"`
	Dataset  string    `json:"dataset"`
	Rows     int64     `json:"rows"`
	MinBlock int64     `json:"min_block"`
	MaxBlock int64     `json:"max_block"`
	Oldest   time.Time `json:"oldest_at"`
	Newest   time.Time `json:"newest_at"`
}

type coverageStat struct {
	Chain      string    `json:"chain"`
	Dataset    string    `json:"dataset"`
	Subjects   int64     `json:"subjects"`
	Complete   int64     `json:"complete"`
	Gapped     int64     `json:"gapped"`
	Rows       int64     `json:"rows"`
	RawBytes   int64     `json:"raw_bytes"`
	LastCommit time.Time `json:"last_committed_at,omitempty"`
}

type coverageGap struct {
	Chain     string    `json:"chain"`
	Dataset   string    `json:"dataset"`
	Subject   string    `json:"subject"`
	ToBlock   int64     `json:"to_block"`
	HeadBlock int64     `json:"head_block"`
	Missing   int64     `json:"missing_blocks"`
	UpdatedAt time.Time `json:"updated_at"`
}

func (s *Server) handleAdminDataQualityOverview(w http.ResponseWriter, r *http.Request) {
	if !s.adminDBReady(w, r) {
		return
	}
	ctx := r.Context()
	pool := s.deps.Store.Pool

	tables := []tableStat{}
	if s.deps.CH != nil {
		// ClickHouse 的 count()/sum() 是 UInt64，驱动不会隐式转成 int64 —— 必须在 SQL 里显式 toInt64。
		rows, err := s.deps.CH.DB().Query(ctx, "SELECT table, toInt64(count()) AS parts, toInt64(sum(rows)) AS rows,"+
			" toInt64(sum(bytes_on_disk)) AS bytes FROM system.parts WHERE database = 'blockchain' AND active"+
			" GROUP BY table ORDER BY rows DESC LIMIT "+strconv.Itoa(dataQualityTables))
		if err != nil && s.deps.Log != nil {
			s.deps.Log.Error("data quality: clickhouse table stats failed", "err", err)
		}
		if err == nil {
			for rows.Next() {
				var item tableStat
				if err := rows.Scan(&item.Table, &item.Parts, &item.Rows, &item.Bytes); err == nil {
					tables = append(tables, item)
				}
			}
			rows.Close()
		}
	}

	chains := []chainStat{}
	if s.deps.CH != nil {
		for _, spec := range []struct{ dataset, table string }{
			{"transactions", "transactions"},
			{"token_transfers", "token_transfers"},
			{"logs", "logs"},
			{"internal_transactions", "internal_calls"},
		} {
			rows, err := s.deps.CH.DB().Query(ctx, "SELECT chain, toInt64(count()), toInt64(min(block_number)),"+
				" toInt64(max(block_number)), min(timestamp), max(timestamp) FROM blockchain."+spec.table+
				" GROUP BY chain ORDER BY 2 DESC LIMIT 6")
			if err != nil {
				if s.deps.Log != nil {
					s.deps.Log.Error("data quality: chain aggregate failed", "dataset", spec.dataset, "err", err)
				}
				continue
			}
			for rows.Next() {
				var item chainStat
				if err := rows.Scan(&item.Chain, &item.Rows, &item.MinBlock, &item.MaxBlock, &item.Oldest, &item.Newest); err == nil {
					item.Dataset = spec.dataset
					chains = append(chains, item)
				}
			}
			rows.Close()
		}
	}

	coverage := []coverageStat{}
	covRows, err := pool.Query(ctx, "SELECT chain, dataset, count(*), count(*) FILTER (WHERE status = 'COMPLETE'),"+
		" count(*) FILTER (WHERE head_block > to_block), coalesce(sum(row_count),0), coalesce(sum(raw_bytes),0),"+
		" max(committed_at) FROM dataset_coverage GROUP BY 1,2 ORDER BY 6 DESC LIMIT 20")
	if err != nil {
		respondError(w, r, http.StatusInternalServerError, "INTERNAL_ERROR", "读取 Coverage 汇总失败", nil)
		return
	}
	for covRows.Next() {
		var item coverageStat
		var lastCommit *time.Time
		if err := covRows.Scan(&item.Chain, &item.Dataset, &item.Subjects, &item.Complete, &item.Gapped,
			&item.Rows, &item.RawBytes, &lastCommit); err != nil {
			covRows.Close()
			respondError(w, r, http.StatusInternalServerError, "INTERNAL_ERROR", "读取 Coverage 汇总失败", nil)
			return
		}
		if lastCommit != nil {
			item.LastCommit = *lastCommit
		}
		coverage = append(coverage, item)
	}
	covRows.Close()

	gaps := []coverageGap{}
	gapRows, err := pool.Query(ctx, "SELECT chain, dataset, subject, to_block, head_block,"+
		" greatest(head_block - to_block, 0) AS missing, updated_at FROM dataset_coverage"+
		" WHERE head_block > to_block ORDER BY missing DESC, updated_at DESC LIMIT 20")
	if err != nil {
		respondError(w, r, http.StatusInternalServerError, "INTERNAL_ERROR", "读取 Coverage 缺口失败", nil)
		return
	}
	for gapRows.Next() {
		var item coverageGap
		if err := gapRows.Scan(&item.Chain, &item.Dataset, &item.Subject, &item.ToBlock,
			&item.HeadBlock, &item.Missing, &item.UpdatedAt); err != nil {
			gapRows.Close()
			respondError(w, r, http.StatusInternalServerError, "INTERNAL_ERROR", "读取 Coverage 缺口失败", nil)
			return
		}
		gaps = append(gaps, item)
	}
	gapRows.Close()

	s.writeAuditNow(r, "admin:coverage:read", "admin.data", "data_quality", "",
		map[string]any{"tables": len(tables), "coverage_groups": len(coverage), "gaps": len(gaps)})

	writeJSON(w, http.StatusOK, map[string]any{"data": map[string]any{
		"as_of":           time.Now().UTC(),
		"tables":          tables,
		"chains":          chains,
		"coverage":        coverage,
		"gaps":            gaps,
		"part_warn_level": 300,
	}})
}
