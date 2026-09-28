package api

import (
	"context"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/blockchain-platform/blockchain-platform/internal/addressnorm"
	"github.com/blockchain-platform/blockchain-platform/internal/app"
	"github.com/blockchain-platform/blockchain-platform/internal/config"
	"github.com/blockchain-platform/blockchain-platform/internal/policy"
	"github.com/blockchain-platform/blockchain-platform/internal/store"
)

/*
 * 用**前端真实发出的 JSON 字节**走一遍预生成与导出两个处理器。
 *
 * 为什么要这样测：规格哈希失配是静默的——字段名写错、少一个默认值、地址大小写
 * 不同，都不会报错，用户点导出只是每次都重新算。单测能证明哈希函数本身稳定，
 * 但证明不了"前端发来的这份 JSON 解码后落在预期的字段上"。这里直接把前端
 * buildAddressExportBody 产出的字段集合写进请求体，走真实 handler。
 *
 * 鉴权用 withPrincipal 注入（与仓库其它 live 测试同一手法）；这里只测本 PR 新增的
 * 处理器，鉴权中间件由既有测试覆盖。
 */
func TestPrefetchHTTPBodyMatchesExportBody(t *testing.T) {
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

	// 前端 buildAddressExportBody 产出的请求体：字段名必须与 exportV2Request 的
	// json tag 一一对应。默认草稿是单地址 + 单数据集 + csv + force_zip=false。
	exportBody := `{"chain":"bsc","addresses":["` + subject + `"],"datasets":["token_transfers"],"format":"csv","include_meta":false,"force_zip":false,"include_decoded_input":false}`
	// 预生成请求与导出请求同形（前端复用同一个构造函数）。
	prefetchBody := exportBody

	id, err := resolveScopeIdentity(ctx, srv, scope.OrganizationID, scope.WorkspaceID)
	if err != nil {
		t.Skipf("no identity in scope: %v", err)
	}
	principal := policy.Principal{
		IdentityID: id, OrganizationID: scope.OrganizationID, WorkspaceID: scope.WorkspaceID,
		Authenticated: true,
	}

	// 1) 预生成：走真实 handler
	req := httptest.NewRequest(http.MethodPost, "/api/v2/exports/prefetch", strings.NewReader(prefetchBody))
	rec := httptest.NewRecorder()
	srv.handleExportPrefetch(rec, req.WithContext(withPrincipal(ctx, principal)))
	if rec.Code != http.StatusAccepted {
		t.Fatalf("prefetch handler returned %d: %s", rec.Code, rec.Body.String())
	}
	t.Logf("prefetch 202: %s", strings.TrimSpace(rec.Body.String()))

	// 等预生成完成（最多 3 分钟）
	var pre *store.PrefetchRow
	deadline := time.Now().Add(3 * time.Minute)
	for time.Now().Before(deadline) {
		// 预生成的规格哈希由 handler 算出；这里用同一份快照复算以取得哈希。
		snapshot := buildExportSnapshot("bsc", []string{normalizeForTest(subject)}, []string{"token_transfers"},
			"csv", false, false, false, "", false, "", exportBounds{})
		pre, _ = srv.deps.Store.FindCompletedPrefetch(ctx, store.PrefetchSpecHash(snapshot),
			scope.OrganizationID, scope.WorkspaceID)
		if pre != nil {
			break
		}
		time.Sleep(2 * time.Second)
	}
	if pre == nil {
		t.Fatal("prefetch never completed; see export.prefetch_failed audit for the cause")
	}
	t.Logf("预生成完成：object_ref=%s rows=%d", pre.ObjectRef, pre.Rows)

	// 2) 导出：用**同一份请求体**，必须命中并提升
	req2 := httptest.NewRequest(http.MethodPost, "/api/v2/exports", strings.NewReader(exportBody))
	rec2 := httptest.NewRecorder()
	start := time.Now()
	srv.handleCreateExport(rec2, req2.WithContext(withPrincipal(ctx, principal)))
	promoteDur := time.Since(start)
	if rec2.Code != http.StatusOK {
		t.Fatalf("create export returned %d: %s", rec2.Code, rec2.Body.String())
	}
	body := rec2.Body.String()
	if promoteDur > 3*time.Second {
		t.Fatalf("export took %s; it must have hit the prefetched artifact (body=%s)", promoteDur, truncateStr(body, 200))
	}
	if !strings.Contains(body, pre.ObjectRef) {
		t.Fatalf("export did not reuse the prefetched object %q; body=%s", pre.ObjectRef, truncateStr(body, 300))
	}
	t.Logf("导出命中预生成：%s（体=%s）", promoteDur.Round(time.Millisecond), truncateStr(body, 160))
}

func truncateStr(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n] + "..."
}

// normalizeForTest 与 handler 内部的地址归一化保持一致（测试里复算规格哈希用）。
func normalizeForTest(addr string) string {
	return addressnorm.NormalizeKey("bsc", addr)
}

// resolveScopeIdentity 取一个属于该 scope 的身份 id。
func resolveScopeIdentity(ctx context.Context, srv *Server, orgID, wsID string) (string, error) {
	var id string
	err := srv.deps.Store.Pool.QueryRow(ctx,
		"SELECT i.id::text FROM identities i JOIN organization_memberships m ON m.identity_id=i.id "+
			"WHERE m.organization_id=$1 LIMIT 1",
		orgID).Scan(&id)
	return id, err
}

var _ = io.Discard

// TestPrefetchStatusHTTPReportsReadiness 覆盖 status 端点（前端可选用来显示"已预生成"）。
//
// 这个端点不在关键路径上（命中判定在导出时自己做），所以之前 curl 它拿 404 只是
// 因为 API key 不属于这个鉴权面——这里用真实处理器把它验一遍，免得留下一个坏端点。
func TestPrefetchStatusHTTPReportsReadiness(t *testing.T) {
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
	id, ierr := resolveScopeIdentity(ctx, srv, scope.OrganizationID, scope.WorkspaceID)
	if ierr != nil {
		t.Skipf("no identity in scope: %v", ierr)
	}
	principal := policy.Principal{IdentityID: id, OrganizationID: scope.OrganizationID,
		WorkspaceID: scope.WorkspaceID, Authenticated: true}

	// 未知规格：ready 必须是 false（而不是报错）。
	req := httptest.NewRequest(http.MethodGet, "/api/v2/exports/prefetch/status?spec_hash=deadbeef", nil)
	rec := httptest.NewRecorder()
	srv.handleExportPrefetchStatus(rec, req.WithContext(withPrincipal(ctx, principal)))
	if rec.Code != http.StatusOK {
		t.Fatalf("status handler returned %d: %s", rec.Code, rec.Body.String())
	}
	if !strings.Contains(rec.Body.String(), `"ready":false`) {
		t.Fatalf("unknown spec must report ready=false, got %s", rec.Body.String())
	}

	// 缺参数：400。
	req2 := httptest.NewRequest(http.MethodGet, "/api/v2/exports/prefetch/status", nil)
	rec2 := httptest.NewRecorder()
	srv.handleExportPrefetchStatus(rec2, req2.WithContext(withPrincipal(ctx, principal)))
	if rec2.Code != http.StatusBadRequest {
		t.Fatalf("missing spec_hash must be 400, got %d", rec2.Code)
	}
}

// TestPrefetchMultiDatasetProducesZipAndHits 覆盖多数据集预生成（表单里加一个数据集
// 就会进入的形态）。
//
// 这条路径与单数据集不同：exportZipDecision 会强制 ZIP，产物是 .zip、内部按数据集
// 分条目。此前的实现只接受单数据集，用户加一个数据集后预生成直接不启动、
// 点了导出也永远命不中。
func TestPrefetchMultiDatasetProducesZipAndHits(t *testing.T) {
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
	id, ierr := resolveScopeIdentity(ctx, srv, scope.OrganizationID, scope.WorkspaceID)
	if ierr != nil {
		t.Skipf("no identity in scope: %v", ierr)
	}
	principal := policy.Principal{IdentityID: id, OrganizationID: scope.OrganizationID,
		WorkspaceID: scope.WorkspaceID, Authenticated: true}

	// 两个数据集 + force_zip=false：实际会因"数据集数>1"强制 ZIP，两边必须一致。
	body := `{"chain":"bsc","addresses":["` + subject + `"],"datasets":["transactions","token_transfers"],"format":"csv","include_meta":false,"force_zip":false,"include_decoded_input":false}`
	req := httptest.NewRequest(http.MethodPost, "/api/v2/exports/prefetch", strings.NewReader(body))
	rec := httptest.NewRecorder()
	srv.handleExportPrefetch(rec, req.WithContext(withPrincipal(ctx, principal)))
	if rec.Code != http.StatusAccepted || !strings.Contains(rec.Body.String(), `"prefetched":true`) {
		t.Fatalf("multi-dataset prefetch not started: %d %s", rec.Code, rec.Body.String())
	}

	snapshot := buildExportSnapshot("bsc", []string{normalizeForTest(subject)},
		[]string{"transactions", "token_transfers"}, "csv", false, true, false, "", false, "", exportBounds{})
	specHash := store.PrefetchSpecHash(snapshot)

	var pre *store.PrefetchRow
	deadline := time.Now().Add(8 * time.Minute)
	for time.Now().Before(deadline) {
		pre, _ = srv.deps.Store.FindCompletedPrefetch(ctx, specHash, scope.OrganizationID, scope.WorkspaceID)
		if pre != nil {
			break
		}
		time.Sleep(3 * time.Second)
	}
	if pre == nil {
		t.Fatal("multi-dataset prefetch never completed; see export.prefetch_failed audit")
	}
	if !strings.HasSuffix(pre.ObjectRef, ".zip") {
		t.Fatalf("multi-dataset prefetch must produce a ZIP, got %s", pre.ObjectRef)
	}
	t.Logf("多数据集预生成完成：object_ref=%s rows=%d", pre.ObjectRef, pre.Rows)

	// 导出必须命中同一份产物
	req2 := httptest.NewRequest(http.MethodPost, "/api/v2/exports", strings.NewReader(body))
	rec2 := httptest.NewRecorder()
	start := time.Now()
	srv.handleCreateExport(rec2, req2.WithContext(withPrincipal(ctx, principal)))
	hit := time.Since(start)
	if rec2.Code != http.StatusOK {
		t.Fatalf("create export returned %d: %s", rec2.Code, rec2.Body.String())
	}
	if hit > 3*time.Second || !strings.Contains(rec2.Body.String(), pre.ObjectRef) {
		t.Fatalf("multi-dataset export did not hit the prefetched zip (%s): %s", hit.Round(time.Millisecond), truncateStr(rec2.Body.String(), 200))
	}
	t.Logf("多数据集导出命中：%s", hit.Round(time.Millisecond))
}
