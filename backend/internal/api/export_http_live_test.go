package api

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/blockchain-platform/blockchain-platform/internal/app"
	"github.com/blockchain-platform/blockchain-platform/internal/config"
	"github.com/blockchain-platform/blockchain-platform/internal/model"
	"github.com/blockchain-platform/blockchain-platform/internal/policy"
)

/*
 * ---- 走真实 HTTP 处理器的"导出完整吗"验收 ------------------------------------------
 *
 * 单测能证明分页取数无损，证明不了"前端真正发的那份请求体、经过处理器、落到 MinIO
 * 之后，行数依然等于库里的真值"。这里用前端 buildAddressExportBody 的字段集合
 * （chain/addresses/datasets/format/... 以及可选的 start_time/end_time）发两份请求：
 *
 *	1. 全量导出：exports.rows 必须等于库侧 uniqExact（老的 200 万上限正是在这里丢行）
 *	2. 时间范围导出：exports.rows 必须等于库侧该窗口内的 uniqExact，且小于全量
 *
 * 鉴权用 withPrincipal 注入（与仓库其它 live 测试同一手法），中间件已由既有测试覆盖。
 *
 *	EXPORT_HTTP_SUBJECT=0x... go test ./internal/api/ -run TestExportHTTPIsComplete -v
 */
func TestExportHTTPIsComplete(t *testing.T) {
	subject := os.Getenv("EXPORT_HTTP_SUBJECT")
	if subject == "" {
		t.Skip("EXPORT_HTTP_SUBJECT not set")
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

	_, dbKeys := subjectKeyCount(t, srv, ctx, "bsc", subject)

	create := func(body string) *model.Export {
		req := httptest.NewRequest(http.MethodPost, "/api/v2/exports", strings.NewReader(body))
		rec := httptest.NewRecorder()
		srv.handleCreateExport(rec, req.WithContext(withPrincipal(ctx, principal)))
		if rec.Code != http.StatusAccepted && rec.Code != http.StatusOK {
			t.Fatalf("创建导出返回 %d：%s", rec.Code, truncateStr(rec.Body.String(), 400))
		}
		var parsed struct {
			ID string `json:"id"`
		}
		if jerr := json.Unmarshal(rec.Body.Bytes(), &parsed); jerr != nil || parsed.ID == "" {
			t.Fatalf("响应无法解析出导出 id：%v body=%s", jerr, truncateStr(rec.Body.String(), 300))
		}
		exportID := parsed.ID
		exp, gerr := srv.deps.Store.GetExport(ctx, exportID)
		if gerr != nil || exp == nil {
			t.Fatalf("读取导出 %s：%v", exportID, gerr)
		}
		return exp
	}
	wait := func(exp *model.Export, timeout time.Duration) *model.Export {
		deadline := time.Now().Add(timeout)
		for time.Now().Before(deadline) {
			cur, gerr := srv.deps.Store.GetExport(ctx, exp.ID)
			if gerr == nil && cur != nil {
				switch cur.Status {
				case "COMPLETED":
					return cur
				case "FAILED":
					t.Fatalf("导出 %s 失败：%s", cur.ID, cur.Error)
				}
			}
			time.Sleep(2 * time.Second)
		}
		t.Fatalf("导出 %s 在 %v 内未完成", exp.ID, timeout)
		return nil
	}

	// 1) 全量导出（不带时间范围 ⇒ 取全量）。
	start := time.Now()
	full := wait(create(fmt.Sprintf(
		`{"chain":"bsc","addresses":["%s"],"datasets":["token_transfers"],"format":"csv","include_meta":false,"force_zip":false,"include_decoded_input":false}`,
		subject)), 12*time.Minute)
	t.Logf("全量导出 %s：rows=%d object=%s 用时=%v", full.ID, full.Rows, full.ObjectRef, time.Since(start).Round(time.Second))
	if full.Rows != dbKeys {
		t.Fatalf("exports.rows=%d != 库侧 distinct 版本键 %d（差 %d）", full.Rows, dbKeys, dbKeys-full.Rows)
	}
	if full.ObjectRef == "" {
		t.Fatal("导出没有落桶（object_ref 为空）")
	}
	if full.SHA256 == "" {
		t.Fatal("导出没有回填对象 SHA-256")
	}

	// 2) 时间范围导出：窗口内的行数必须等于库侧窗口真值，且严格小于全量。
	var minTS, maxTS time.Time
	if err := srv.query.CH.DB().QueryRow(ctx,
		"SELECT min(timestamp), max(timestamp) FROM blockchain.token_transfers WHERE chain=? AND (from_address=? OR to_address=?)",
		"bsc", subject, subject).Scan(&minTS, &maxTS); err != nil {
		t.Fatalf("时间跨度: %v", err)
	}
	span := maxTS.Sub(minTS)
	if span < 48*time.Hour {
		t.Skipf("时间跨度只有 %v，跳过范围用例", span)
	}
	from := minTS.Add(span / 4)
	to := from.Add(24 * time.Hour)
	var windowKeys uint64
	if err := srv.query.CH.DB().QueryRow(ctx,
		"SELECT uniqExact(tuple(tx_hash, log_index, token_address)) FROM blockchain.token_transfers "+
			"WHERE chain=? AND (from_address=? OR to_address=?) AND timestamp>=? AND timestamp<=?",
		"bsc", subject, subject, from, to).Scan(&windowKeys); err != nil {
		t.Fatalf("窗口计数: %v", err)
	}
	if windowKeys == 0 || int64(windowKeys) >= dbKeys {
		t.Skipf("窗口内键数 %d 不适合做范围用例（全量 %d）", windowKeys, dbKeys)
	}
	ranged := wait(create(fmt.Sprintf(
		`{"chain":"bsc","addresses":["%s"],"datasets":["token_transfers"],"format":"csv","include_meta":false,"force_zip":false,"include_decoded_input":false,"start_time":"%s","end_time":"%s"}`,
		subject, from.UTC().Format(time.RFC3339), to.UTC().Format(time.RFC3339))), 12*time.Minute)
	t.Logf("范围导出 %s [%s, %s]：rows=%d（库侧 %d）object=%s",
		ranged.ID, from.UTC().Format(time.RFC3339), to.UTC().Format(time.RFC3339), ranged.Rows, windowKeys, ranged.ObjectRef)
	if ranged.Rows != int64(windowKeys) {
		t.Fatalf("范围导出 rows=%d != 库侧窗口内键数 %d", ranged.Rows, windowKeys)
	}
	if ranged.Rows >= full.Rows {
		t.Fatalf("范围导出行数 %d 不应达到全量 %d（时间范围没生效）", ranged.Rows, full.Rows)
	}
}
