package api

import (
	"context"
	"io"
	"log/slog"
	"os"
	"testing"
	"time"

	"github.com/blockchain-platform/blockchain-platform/internal/app"
	"github.com/blockchain-platform/blockchain-platform/internal/config"
	"github.com/blockchain-platform/blockchain-platform/internal/store"
)

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
	srv.runExportPrefetch(ctx, id, specHash, "bsc", subject, dataset, format, zipIt, false, false, orgID, wsID)
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
