package api

import (
	"context"
	"encoding/json"
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
 * 用户端真正调用的那条路径必须能命中预生成。
 *
 * 为什么单独测：用户端 `POST /api/v2/exports` 与预生成 `POST /api/v2/exports/prefetch`
 * 是**两个不同的处理器**（sprint5 的 handleExportV2Create 与 handleExportPrefetch）。
 * 预生成算出的规格哈希只有在"创建路径也用同一份快照、并且真的去查了一次"时才可能命中；
 * 少了任何一半，表现都是**静默的**——预生成照常生成、导出照常重新算一遍，
 * 用户只是白等（实测一份 173 万行产物要 17 秒）。
 *
 *	EXPORT_FLOW_SUBJECT=0x... go test ./internal/api/ -run TestV2ExportPromotesPrefetch -v
 */
func TestV2ExportPromotesPrefetch(t *testing.T) {
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
	id, err := resolveScopeIdentity(ctx, srv, scope.OrganizationID, scope.WorkspaceID)
	if err != nil {
		t.Skipf("no identity in scope: %v", err)
	}
	principal := policy.Principal{IdentityID: id, OrganizationID: scope.OrganizationID,
		WorkspaceID: scope.WorkspaceID, Authenticated: true}
	// 前端 buildAddressExportBody 产出的字段集合（默认草稿：单地址 + 单数据集 + csv）。
	body := `{"chain":"bsc","addresses":["` + subject + `"],"datasets":["token_transfers"],"format":"csv","include_meta":false,"force_zip":false,"include_decoded_input":false}`

	// 1) 预生成（真实处理器）。
	req := httptest.NewRequest(http.MethodPost, "/api/v2/exports/prefetch", strings.NewReader(body))
	rec := httptest.NewRecorder()
	srv.handleExportPrefetch(rec, req.WithContext(withPrincipal(ctx, principal)))
	if rec.Code != http.StatusAccepted {
		t.Fatalf("预生成返回 %d：%s", rec.Code, truncateStr(rec.Body.String(), 300))
	}

	// 2) 等产物就绪（最长 3 分钟）。
	snapshot := buildExportSnapshot("bsc", []string{addressnorm.NormalizeKey("bsc", subject)},
		[]string{"token_transfers"}, "csv", false, false, false, "", false, "", exportBounds{})
	var pre *store.PrefetchRow
	deadline := time.Now().Add(3 * time.Minute)
	for time.Now().Before(deadline) {
		pre, _ = srv.deps.Store.FindCompletedPrefetch(ctx, store.PrefetchSpecHash(snapshot),
			scope.OrganizationID, scope.WorkspaceID)
		if pre != nil {
			break
		}
		time.Sleep(2 * time.Second)
	}
	if pre == nil {
		t.Fatal("预生成始终没有完成（见 export.prefetch_failed 审计事件）")
	}
	t.Logf("预生成就绪：object=%s rows=%d", pre.ObjectRef, pre.Rows)

	// 3) 走用户端创建接口：必须命中提升，而不是重新生成一遍。
	req2 := httptest.NewRequest(http.MethodPost, "/api/v2/exports", strings.NewReader(body))
	req2.Header.Set("Idempotency-Key", "test-v2-prefetch-promotion")
	rec2 := httptest.NewRecorder()
	start := time.Now()
	srv.handleExportV2Create(rec2, req2.WithContext(withPrincipal(ctx, principal)))
	promoteDur := time.Since(start)
	if rec2.Code != http.StatusOK && rec2.Code != http.StatusCreated {
		t.Fatalf("创建导出返回 %d：%s", rec2.Code, truncateStr(rec2.Body.String(), 400))
	}
	// 用户端接口统一包在 {success,data,meta} 信封里（respondOK）。
	var envelope struct {
		Data struct {
			ID        string `json:"id"`
			ObjectRef string `json:"object_ref"`
			Rows      int64  `json:"rows"`
			Status    string `json:"status"`
		} `json:"data"`
	}
	if jerr := json.Unmarshal(rec2.Body.Bytes(), &envelope); jerr != nil {
		t.Fatalf("响应解析失败：%v body=%s", jerr, truncateStr(rec2.Body.String(), 300))
	}
	created := envelope.Data
	if promoteDur > 5*time.Second {
		t.Fatalf("创建导出耗时 %s，说明没有命中预生成（body=%s）", promoteDur, truncateStr(rec2.Body.String(), 200))
	}
	if created.ObjectRef == "" {
		// 提升分支返回的是 exports 行本身，字段名以 model.Export 的 json tag 为准；
		// 与预生成对象不一致时把整份响应打出来，避免"看起来通过了"。
		t.Fatalf("提升响应里没有 object_ref：%s", truncateStr(rec2.Body.String(), 600))
	}
	if created.ObjectRef != pre.ObjectRef {
		t.Fatalf("命中的不是预生成产物：created=%q prefetch=%q", created.ObjectRef, pre.ObjectRef)
	}
	if created.Status != "COMPLETED" {
		t.Fatalf("提升后的状态是 %q，应为 COMPLETED", created.Status)
	}
	if created.Rows != pre.Rows {
		t.Fatalf("提升后的行数 %d != 预生成行数 %d", created.Rows, pre.Rows)
	}
	t.Logf("命中预生成：%s 用时 %v object=%s rows=%d", created.ID, promoteDur.Round(time.Millisecond), created.ObjectRef, created.Rows)
}
