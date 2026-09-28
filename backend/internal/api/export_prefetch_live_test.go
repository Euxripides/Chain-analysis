package api

import (
	"context"
	"io"
	"log/slog"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/minio/minio-go/v7"

	"github.com/blockchain-platform/blockchain-platform/internal/app"
	"github.com/blockchain-platform/blockchain-platform/internal/config"
	"github.com/blockchain-platform/blockchain-platform/internal/store"
)

func minioPutOptions() minio.PutObjectOptions {
	return minio.PutObjectOptions{ContentType: "text/plain"}
}
func minioStatOptions() minio.StatObjectOptions     { return minio.StatObjectOptions{} }
func minioRemoveOptions() minio.RemoveObjectOptions { return minio.RemoveObjectOptions{} }

// TestPrefetchPromotesToInstantExport 是预生成的端到端判据：
//
//  1. 按一份草稿跑预生成；
//  2. 用**同一个规格**调用提升逻辑，必须命中且是毫秒级（不再跑流水线）；
//  3. 提升出的导出对象名必须与预生成产物一致、行数一致。
//
// 需要真实依赖与已应用迁移，未配置时跳过。
func TestPrefetchPromotesToInstantExport(t *testing.T) {
	subject := os.Getenv("EXPORT_FLOW_SUBJECT")
	if subject == "" {
		t.Skip("EXPORT_FLOW_SUBJECT not set")
	}
	ctx := context.Background()
	app.TuneExportGC()
	cfg := config.Load()
	log := slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelWarn}))
	infra, err := app.Bootstrap(ctx, cfg, log)
	if err != nil {
		t.Skipf("bootstrap: %v", err)
	}
	srv := New(Deps{Cfg: cfg, Store: infra.Store, CH: infra.CH, Raw: infra.Raw, Log: log,
		TokenMeta: infra.TokenMeta, MethodData: infra.MethodData})
	scope, serr := srv.systemScope(ctx)
	if serr != nil {
		t.Skipf("scope: %v", serr)
	}
	orgID, wsID := scope.OrganizationID, scope.WorkspaceID

	const dataset, format = "token_transfers", "csv"
	zipIt := false
	snapshot := buildExportSnapshot("bsc", []string{subject}, []string{dataset}, format, false, zipIt, false, "", false, "")
	specHash := store.PrefetchSpecHash(snapshot)

	// 1) 预生成
	id, created, err := srv.deps.Store.CreatePrefetch(ctx, specHash, orgID, wsID, nil, dataset, format, zipIt, prefetchTTL)
	if err != nil {
		t.Fatalf("create prefetch: %v", err)
	}
	if !created {
		t.Logf("an identical prefetch already exists (%s); reusing its row", id)
	}
	start := time.Now()
	srv.runExportPrefetch(ctx, id, "bsc", subject, []string{dataset}, format, zipIt, false, false, orgID, wsID)
	t.Logf("预生成耗时 %s", time.Since(start).Round(time.Millisecond))

	pre, err := srv.deps.Store.FindCompletedPrefetch(ctx, specHash, orgID, wsID)
	if err != nil || pre == nil {
		t.Fatalf("prefetch did not complete: row=%v err=%v", pre, err)
	}
	t.Logf("预生成产物 object_ref=%s rows=%d", pre.ObjectRef, pre.Rows)
	if pre.Rows == 0 || pre.ObjectRef == "" {
		t.Fatalf("prefetch produced nothing: %+v", pre)
	}

	// 2) 用同一规格提升：必须是毫秒级
	start = time.Now()
	exp, ok := srv.tryPromotePrefetch(ctx, specHash, orgID, wsID, "", dataset, format, zipIt, snapshot)
	promoteDur := time.Since(start)
	if !ok || exp == nil {
		t.Fatal("promotion missed: a completed prefetch with an identical spec must be promoted")
	}
	t.Logf("提升耗时 %s（导出 id=%s rows=%d）", promoteDur.Round(time.Millisecond), exp.ID, exp.Rows)
	if promoteDur > 2*time.Second {
		t.Fatalf("promotion took %s; it must not re-run the export pipeline", promoteDur)
	}
	if exp.ObjectRef != pre.ObjectRef {
		t.Fatalf("promoted object_ref=%q, want the prefetched %q", exp.ObjectRef, pre.ObjectRef)
	}
	if exp.Rows != pre.Rows {
		t.Fatalf("promoted rows=%d, want %d", exp.Rows, pre.Rows)
	}

	// 3) 提升后原预生成行应被消费掉（否则同一份草稿会被反复提升出多条导出）
	if again, _ := srv.deps.Store.FindCompletedPrefetch(ctx, specHash, orgID, wsID); again != nil {
		t.Fatalf("prefetch row %s survived promotion", again.ID)
	}

	// 清理：删掉测试产生的导出行（对象保留，便于人工核对）
	if _, derr := srv.deps.Store.Pool.Exec(ctx, "DELETE FROM exports WHERE id=$1", exp.ID); derr != nil {
		t.Logf("cleanup export row: %v", derr)
	}
	_ = io.Discard
}

// TestPrefetchGCSweepsOnlyExpired 锁定清理循环的边界：
// 过期行连同对象一起删；**未过期**行必须原样保留（否则用户刚预生成的东西会被清掉）。
func TestPrefetchGCSweepsOnlyExpired(t *testing.T) {
	subject := os.Getenv("EXPORT_FLOW_SUBJECT")
	if subject == "" {
		t.Skip("EXPORT_FLOW_SUBJECT not set")
	}
	ctx := context.Background()
	cfg := config.Load()
	log := slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelWarn}))
	infra, err := app.Bootstrap(ctx, cfg, log)
	if err != nil {
		t.Skipf("bootstrap: %v", err)
	}
	srv := New(Deps{Cfg: cfg, Store: infra.Store, CH: infra.CH, Raw: infra.Raw, Log: log,
		TokenMeta: infra.TokenMeta, MethodData: infra.MethodData})
	scope, serr := srv.systemScope(ctx)
	if serr != nil {
		t.Skipf("scope: %v", serr)
	}
	orgID, wsID := scope.OrganizationID, scope.WorkspaceID

	// 造两行：一行已过期、一行未过期。分别指向同一个真实存在的对象（用 MinIO 里的
	// bench 前缀对象不现实，这里直接放一个空对象）。
	const bucket = "blockchain-exports"
	client := srv.deps.Raw.Client()
	mkObject := func(name string) string {
		key := "bench/gc-" + name + ".txt"
		if _, err := client.PutObject(ctx, bucket, key, strings.NewReader("x"), 1, minioPutOptions()); err != nil {
			t.Skipf("cannot put test object: %v", err)
		}
		return key
	}
	expiredKey := mkObject("expired")
	freshKey := mkObject("fresh")

	// 表里只有 status/object_ref 等列；这里用最小插入直接落两行。
	insert := func(key string, expires string) string {
		id := uuid.NewString()
		_, err := srv.deps.Store.Pool.Exec(ctx,
			"INSERT INTO export_prefetch (id, spec_hash, organization_id, workspace_id, dataset, format, zip, status, object_ref, object_bucket, rows, expires_at) "+
				"VALUES ($1,$2,$3,$4,'token_transfers','csv',false,'COMPLETED',$5,$6,1, now() + $7::interval)",
			id, "gctest-"+id, orgID, wsID, key, bucket, expires)
		if err != nil {
			t.Fatalf("insert prefetch row: %v", err)
		}
		return id
	}
	expiredID := insert(expiredKey, "-1 hour")
	freshID := insert(freshKey, "+1 hour")
	t.Cleanup(func() {
		_, _ = srv.deps.Store.Pool.Exec(context.Background(), "DELETE FROM export_prefetch WHERE id IN ($1,$2)", expiredID, freshID)
		client.RemoveObject(context.Background(), bucket, expiredKey, minioRemoveOptions())
		client.RemoveObject(context.Background(), bucket, freshKey, minioRemoveOptions())
	})

	srv.sweepExpiredPrefetch(ctx)

	// 过期行应被删除
	var n int
	if err := srv.deps.Store.Pool.QueryRow(ctx, "SELECT count(*) FROM export_prefetch WHERE id=$1", expiredID).Scan(&n); err != nil {
		t.Fatalf("query expired row: %v", err)
	}
	if n != 0 {
		t.Fatalf("expired prefetch row %s survived the sweep", expiredID)
	}
	// 未过期行必须还在
	if err := srv.deps.Store.Pool.QueryRow(ctx, "SELECT count(*) FROM export_prefetch WHERE id=$1", freshID).Scan(&n); err != nil {
		t.Fatalf("query fresh row: %v", err)
	}
	if n != 1 {
		t.Fatalf("unexpired prefetch row %s was swept away", freshID)
	}
	// 过期对象应被删除
	if _, err := client.StatObject(ctx, bucket, expiredKey, minioStatOptions()); err == nil {
		t.Fatalf("expired object %s still exists", expiredKey)
	}
	// 未过期对象必须还在
	if _, err := client.StatObject(ctx, bucket, freshKey, minioStatOptions()); err != nil {
		t.Fatalf("unexpired object %s was removed: %v", freshKey, err)
	}
}
