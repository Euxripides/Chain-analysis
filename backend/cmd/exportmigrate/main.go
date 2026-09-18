// exportmigrate：F-R10-BUCKET 导出对象桶迁移工具（ADR-019 §2.3）。
//
// 安全默认：
//   - `-mode inventory` 为**默认**模式，只读（PG + 两个桶的 stat），不改任何东西；
//   - `-mode migrate` 默认仍是 **dry-run**：会为每个对象计算**源 GET SHA-256** 并与
//     exports.sha256（204 条有值）交叉校验，但不复制、不写账本；只有显式 `-execute` 才复制；
//   - `-mode verify` 重新读取账本并对每个已 VERIFIED 行做**目标 GET SHA 复算**，发现漂移即报错。
//
// 绝不执行：删除 RAW 对象、移动对象、改写 exports.object_ref、回填 exports.sha256。
package main

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"log/slog"
	"os"
	"strings"
	"time"

	"github.com/minio/minio-go/v7"
	"github.com/minio/minio-go/v7/pkg/credentials"

	"github.com/blockchain-platform/blockchain-platform/internal/config"
	"github.com/blockchain-platform/blockchain-platform/internal/exportmigrate"
	"github.com/blockchain-platform/blockchain-platform/internal/logger"
	"github.com/blockchain-platform/blockchain-platform/internal/store"
)

type stringList []string

func (s *stringList) String() string     { return strings.Join(*s, ",") }
func (s *stringList) Set(v string) error { *s = append(*s, v); return nil }

func main() {
	var (
		mode       = flag.String("mode", "inventory", "inventory|migrate|verify")
		execute    = flag.Bool("execute", false, "migrate 模式下真正执行复制（默认 false = dry-run，零副作用）")
		limit      = flag.Int("limit", 0, "最多处理多少个对象（0 = 全部）")
		fromBucket = flag.String("from-bucket", "", "源桶（默认 RAW_BUCKET）")
		toBucket   = flag.String("to-bucket", "", "目标桶（默认 EXPORT_BUCKET）")
		ledgerKind = flag.String("ledger", "pg", "账本后端：pg（0058 表）| jsonl（隔离校准/计划）")
		ledgerFile = flag.String("ledger-file", "", "jsonl 账本路径")
		reportPath = flag.String("report", "", "JSON 报告输出路径（默认 stdout）")
		stopOnErr  = flag.Bool("stop-on-error", false, "首个失败立即返回")
		itemsFile  = flag.String("items-file", "", "隔离校准用：从 JSON 文件读取待处理对象列表（跳过 PG 读取，不写 exports 表）")
	)
	var exportIDs, keys stringList
	flag.Var(&exportIDs, "export-id", "只处理指定 export id（可重复）")
	flag.Var(&keys, "key", "只处理指定 object_ref（可重复）")
	flag.Parse()

	ctx := context.Background()
	cfg := config.Load()
	log := logger.New("info")
	if err := cfg.Validate(); err != nil {
		log.Error("config invalid", "err", err)
		os.Exit(1)
	}
	src := *fromBucket
	if src == "" {
		src = cfg.RawBucket
	}
	dst := *toBucket
	if dst == "" {
		dst = cfg.ExportBucket
	}
	if src == dst {
		log.Error("source and destination bucket must differ", "bucket", src)
		os.Exit(1)
	}

	client, err := minio.New(cfg.MinioEndpoint, &minio.Options{
		Creds:  credentials.NewStaticV4(cfg.MinioAccessKey, cfg.MinioSecretKey, ""),
		Secure: cfg.MinioSecure,
	})
	if err != nil {
		log.Error("minio client", "err", err)
		os.Exit(1)
	}
	objStore := exportmigrate.NewMinIOStore(client)

	// PG 连接在两种情况下都需要：账本后端是 pg，或物品来源是 exports 表（inventory / migrate 无 -items-file）。
	// 实测缺陷（20260913T124012Z）：jsonl 账本 + PG 物品来源时 pool 为 nil，loadItems 直接 panic。
	// 连接后降权到 DB_RUNTIME_ROLE（ADR-017 §5），与 API 运行期一致：本工具不需要 owner 权限。
	needPG := *ledgerKind == "pg" || *mode == "inventory" || (*mode == "migrate" && *itemsFile == "")
	var ledger exportmigrate.Ledger
	var pool *store.Store
	if needPG {
		pool, err = store.NewWithRuntimeRole(ctx, cfg.DatabaseURL, cfg.DatabaseRuntimeRole)
		if err != nil {
			log.Error("postgres connect failed", "err", err)
			os.Exit(1)
		}
		defer pool.Close()
	}
	switch *ledgerKind {
	case "pg":
		if pool == nil {
			log.Error("-ledger=pg requires a postgres connection")
			os.Exit(1)
		}
		ledger = &exportmigrate.PGLedger{Pool: pool.Pool}
	case "jsonl":
		if *ledgerFile == "" {
			log.Error("-ledger=jsonl requires -ledger-file")
			os.Exit(1)
		}
		ledger = &exportmigrate.JSONLLedger{Path: *ledgerFile}
	default:
		log.Error("unknown -ledger", "ledger", *ledgerKind)
		os.Exit(1)
	}

	eng := &exportmigrate.Engine{Store: objStore, Ledger: ledger, FromBucket: src, ToBucket: dst, Log: log}

	var exitCode int
	if *itemsFile != "" && *mode == "migrate" {
		// 隔离负向校准路径：只读文件里的合成对象，绝不触碰 exports 表。
		exitCode = runMigrateFromFile(ctx, log, eng, *itemsFile, *limit, *execute, *stopOnErr, *reportPath)
		os.Exit(exitCode)
	}
	switch *mode {
	case "inventory":
		exitCode = runInventory(ctx, log, pool, objStore, src, dst, exportIDs, keys, *limit, *reportPath)
	case "migrate":
		if !*execute {
			log.Info("dry-run (no -execute): will compute source SHA-256 and PG cross-check, but copy nothing")
		}
		exitCode = runMigrate(ctx, log, pool, eng, exportIDs, keys, *limit, *execute, *stopOnErr, *reportPath)
	case "verify":
		exitCode = runVerify(ctx, log, ledger, objStore, dst, *reportPath)
	default:
		log.Error("unknown -mode", "mode", *mode)
		exitCode = 1
	}
	os.Exit(exitCode)
}

// loadItems 从 PG 读取待处理对象（只读；不更新任何列）。
func loadItems(ctx context.Context, pool *store.Store, exportIDs, keys []string, limit int) ([]exportmigrate.Item, error) {
	if pool == nil || pool.Pool == nil {
		return nil, fmt.Errorf("postgres connection is required to read the exports table")
	}
	q := `SELECT id::text, object_ref, coalesce(sha256,'')
	      FROM exports
	      WHERE status='COMPLETED' AND object_ref <> ''`
	args := []any{}
	if len(exportIDs) > 0 {
		args = append(args, exportIDs)
		q += fmt.Sprintf(" AND id = ANY($%d::uuid[])", len(args))
	}
	if len(keys) > 0 {
		args = append(args, keys)
		q += fmt.Sprintf(" AND object_ref = ANY($%d::text[])", len(args))
	}
	q += " ORDER BY created_at, id"
	if limit > 0 {
		q += fmt.Sprintf(" LIMIT %d", limit)
	}
	rows, err := pool.Pool.Query(ctx, q, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []exportmigrate.Item
	for rows.Next() {
		var item exportmigrate.Item
		if err := rows.Scan(&item.ExportID, &item.ObjectRef, &item.PGSHA256); err != nil {
			return nil, err
		}
		out = append(out, item)
	}
	return out, rows.Err()
}

// loadItemsFromFile 读取隔离校准用的合成对象清单（JSON 数组）。
func loadItemsFromFile(path string) ([]exportmigrate.Item, error) {
	payload, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	var items []exportmigrate.Item
	if err := json.Unmarshal(payload, &items); err != nil {
		return nil, err
	}
	for i, item := range items {
		if strings.TrimSpace(item.ExportID) == "" || strings.TrimSpace(item.ObjectRef) == "" {
			return nil, fmt.Errorf("items[%d] must have export_id and object_ref", i)
		}
	}
	return items, nil
}

func runMigrateFromFile(ctx context.Context, log *slog.Logger, eng *exportmigrate.Engine, itemsFile string,
	limit int, execute, stopOnError bool, reportPath string) int {
	items, err := loadItemsFromFile(itemsFile)
	if err != nil {
		log.Error("load items file", "err", err)
		return 1
	}
	log.Warn("calibration path: reading synthetic items from file; exports table is NOT read or written",
		"items", len(items), "file", itemsFile, "execute", execute)
	rep, err := eng.Run(ctx, items, exportmigrate.Options{Execute: execute, Limit: limit, StopOnError: stopOnError})
	if err != nil && rep == nil {
		log.Error("migrate aborted", "err", err)
		return 1
	}
	emit(rep, reportPath)
	log.Info("migrate(file) done", "execute", execute, "total", rep.Total, "by_status", rep.ByStatus,
		"by_action", rep.ByAction, "failures", len(rep.Failures), "bytes", rep.TotalBytes)
	if !execute {
		if len(rep.Failures) > 0 {
			return 2
		}
		return 0
	}
	if !rep.AllVerified() {
		return 2
	}
	return 0
}

type inventoryReport struct {
	GeneratedAt   string   `json:"generated_at"`
	FromBucket    string   `json:"from_bucket"`
	ToBucket      string   `json:"to_bucket"`
	PGCandidates  int      `json:"pg_candidates"`
	PGShasPresent int      `json:"pg_sha_present"`
	PGShasEmpty   int      `json:"pg_sha_empty"`
	InSource      int      `json:"in_source_bucket"`
	InDestination int      `json:"in_destination_bucket"`
	InBoth        int      `json:"in_both_buckets"`
	Missing       int      `json:"missing_in_both"`
	SourceBytes   int64    `json:"source_bytes"`
	MissingRefs   []string `json:"missing_refs,omitempty"`
	BothRefs      []string `json:"both_refs,omitempty"`
}

// runInventory 只读对账：PG 记录 vs 两个桶的实际存在性/大小。不复制、不写库。
func runInventory(ctx context.Context, log *slog.Logger, pool *store.Store, objStore exportmigrate.ObjectStore,
	from, to string, exportIDs, keys []string, limit int, reportPath string) int {
	items, err := loadItems(ctx, pool, exportIDs, keys, limit)
	if err != nil {
		log.Error("load export rows", "err", err)
		return 1
	}
	rep := inventoryReport{
		GeneratedAt: time.Now().UTC().Format(time.RFC3339),
		FromBucket:  from, ToBucket: to, PGCandidates: len(items),
	}
	for _, item := range items {
		if strings.TrimSpace(item.PGSHA256) == "" {
			rep.PGShasEmpty++
		} else {
			rep.PGShasPresent++
		}
		srcSize, srcExists, err := objStore.Stat(ctx, from, item.ObjectRef)
		if err != nil {
			log.Error("stat source", "object_ref", item.ObjectRef, "err", err)
			return 1
		}
		_, dstExists, err := objStore.Stat(ctx, to, item.ObjectRef)
		if err != nil {
			log.Error("stat destination", "object_ref", item.ObjectRef, "err", err)
			return 1
		}
		switch {
		case srcExists && dstExists:
			rep.InBoth++
			rep.BothRefs = append(rep.BothRefs, item.ObjectRef)
		case srcExists:
			rep.InSource++
			rep.SourceBytes += srcSize
		case dstExists:
			rep.InDestination++
		default:
			rep.Missing++
			rep.MissingRefs = append(rep.MissingRefs, item.ObjectRef)
		}
	}
	emit(rep, reportPath)
	log.Info("inventory done",
		"pg_candidates", rep.PGCandidates, "pg_sha_present", rep.PGShasPresent, "pg_sha_empty", rep.PGShasEmpty,
		"in_source", rep.InSource, "in_destination", rep.InDestination, "in_both", rep.InBoth,
		"missing", rep.Missing, "source_bytes", rep.SourceBytes)
	return 0
}

// runMigrate 执行计划或复制；dry-run 也会算源 SHA 并与 PG sha 交叉校验。
func runMigrate(ctx context.Context, log *slog.Logger, pool *store.Store, eng *exportmigrate.Engine,
	exportIDs, keys []string, limit int, execute, stopOnError bool, reportPath string) int {
	items, err := loadItems(ctx, pool, exportIDs, keys, 0)
	if err != nil {
		log.Error("load export rows", "err", err)
		return 1
	}
	rep, err := eng.Run(ctx, items, exportmigrate.Options{Execute: execute, Limit: limit, StopOnError: stopOnError})
	if err != nil && rep == nil {
		log.Error("migrate aborted", "err", err)
		return 1
	}
	emit(rep, reportPath)
	log.Info("migrate done", "execute", execute, "total", rep.Total, "by_status", rep.ByStatus,
		"by_action", rep.ByAction, "failures", len(rep.Failures), "bytes", rep.TotalBytes)
	if !execute {
		// dry-run 的合格标准：每个对象都算出了源 SHA，且 204 条有 PG sha 的全部匹配。
		if len(rep.Failures) > 0 {
			return 2
		}
		return 0
	}
	if !rep.AllVerified() {
		return 2
	}
	return 0
}

type verifyReport struct {
	GeneratedAt string   `json:"generated_at"`
	Bucket      string   `json:"to_bucket"`
	LedgerRows  int      `json:"ledger_rows"`
	Verified    int      `json:"verified"`
	Drift       []string `json:"drift,omitempty"`
	NotVerified []string `json:"not_verified,omitempty"`
}

// runVerify 对账本已 VERIFIED 的行重新做目标 GET SHA 复算，发现漂移即失败。
func runVerify(ctx context.Context, log *slog.Logger, ledger exportmigrate.Ledger,
	objStore exportmigrate.ObjectStore, to string, reportPath string) int {
	rows, err := ledger.List(ctx, to)
	if err != nil {
		log.Error("list ledger", "err", err)
		return 1
	}
	rep := verifyReport{GeneratedAt: time.Now().UTC().Format(time.RFC3339), Bucket: to, LedgerRows: len(rows)}
	for _, row := range rows {
		if !exportmigrate.IsVerified(row.Status) {
			rep.NotVerified = append(rep.NotVerified, fmt.Sprintf("%s: %s", row.ObjectRef, row.Status))
			continue
		}
		sha, size, err := objStore.SHA256(ctx, to, row.ObjectRef)
		if err != nil {
			rep.Drift = append(rep.Drift, fmt.Sprintf("%s: read failed: %v", row.ObjectRef, err))
			continue
		}
		if sha != row.DestinationSHA256 || sha != row.SourceSHA256 || size != row.Bytes {
			rep.Drift = append(rep.Drift, fmt.Sprintf("%s: destination sha %s size %d != recorded %s/%d",
				row.ObjectRef, sha, size, row.DestinationSHA256, row.Bytes))
			continue
		}
		rep.Verified++
	}
	emit(rep, reportPath)
	log.Info("verify done", "ledger_rows", rep.LedgerRows, "verified", rep.Verified,
		"drift", len(rep.Drift), "not_verified", len(rep.NotVerified))
	if len(rep.Drift) > 0 || len(rep.NotVerified) > 0 {
		return 2
	}
	return 0
}

func emit(v any, reportPath string) {
	payload, err := json.MarshalIndent(v, "", "  ")
	if err != nil {
		fmt.Fprintln(os.Stderr, "marshal report:", err)
		return
	}
	payload = append(payload, '\n')
	if reportPath == "" {
		os.Stdout.Write(payload)
		return
	}
	if err := os.WriteFile(reportPath, payload, 0o644); err != nil {
		fmt.Fprintln(os.Stderr, "write report:", err)
	}
}
