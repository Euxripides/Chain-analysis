package api

// D-R10V9-01 回归：导出下载的**服务端业务审计**必须覆盖直接路径的成功与失败，且
// object_bucket 必须是**真实 MinIO 桶名**（object_bucket_role 记录逻辑角色）。
//
// 覆盖根要求的六个 httptest 场景：
//   1) 全量 GET 成功        -> export.download（path=full，bytes=对象大小）
//   2) Range GET 成功       -> export.download（path=range，bytes=区间长度）
//   3) HEAD 成功            -> export.download（path=head，无响应体）
//   4) 两个桶都无该对象     -> 503 DATA_OBJECT_MISSING + export.download_failed(object_missing)，无成功审计
//   5) 非 404（桶不存在）    -> 500 fail closed，无成功审计、不得伪装成 DATA_OBJECT_MISSING
//   6) 写端中断（客户端断开）-> 不写成功审计；写 export.download_failed(reason=client_abort) 且保留 bytes_written
//
// 桩说明：S3 桩与 export_object_store_test.go 共用（newS3Stub/newTestServer）。

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/blockchain-platform/blockchain-platform/internal/audit"
	"github.com/blockchain-platform/blockchain-platform/internal/model"
	"github.com/blockchain-platform/blockchain-platform/internal/policy"
)

type recordingAuditSink struct {
	events []audit.Event
	err    error
}

func (s *recordingAuditSink) Write(_ context.Context, e audit.Event) error {
	if s.err != nil {
		return s.err
	}
	s.events = append(s.events, e)
	return nil
}

func (s *recordingAuditSink) find(action string) *audit.Event {
	for i := range s.events {
		if s.events[i].Action == action {
			return &s.events[i]
		}
	}
	return nil
}

func (s *recordingAuditSink) count(action string) int {
	n := 0
	for i := range s.events {
		if s.events[i].Action == action {
			n++
		}
	}
	return n
}

func newDownloadAuditServer(t *testing.T, stub *s3Stub) (*Server, *recordingAuditSink) {
	t.Helper()
	s := newTestServer(t, stub, nil)
	sink := &recordingAuditSink{}
	s.auditSink = sink
	return s, sink
}

func downloadTestRequest(method, target string) *http.Request {
	r := httptest.NewRequest(method, target, nil)
	ctx := withPrincipal(r.Context(), policy.Principal{
		Authenticated:  true,
		IdentityID:     "11111111-1111-1111-1111-111111111111",
		Email:          "fixture-investigator@fixture.local",
		OrganizationID: "22222222-2222-2222-2222-222222222222",
		WorkspaceID:    "33333333-3333-3333-3333-333333333333",
	})
	ctx = withRequestMeta(ctx, RequestMeta{RequestID: "req-test-1", TraceID: "trace-test-1"})
	return r.WithContext(ctx)
}

func downloadTestExport() *model.Export {
	return &model.Export{
		ID:        "44444444-4444-4444-4444-444444444444",
		Status:    "COMPLETED",
		ObjectRef: "exports/audit-case.csv",
		Format:    "csv",
		SHA256:    "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa",
	}
}

func detailString(e *audit.Event, key string) string {
	if e == nil || e.Detail == nil {
		return ""
	}
	if v, ok := e.Detail[key].(string); ok {
		return v
	}
	return ""
}

func detailInt(e *audit.Event, key string) int64 {
	if e == nil || e.Detail == nil {
		return -1
	}
	switch v := e.Detail[key].(type) {
	case int64:
		return v
	case int:
		return int64(v)
	}
	return -1
}

func assertSuccessAudit(t *testing.T, e *audit.Event, path, method string, bytes int64) {
	t.Helper()
	if e == nil {
		t.Fatalf("expected a successful export.download audit, got none")
	}
	if got := detailString(e, "object_bucket"); got != testExportBucket {
		t.Fatalf("object_bucket must be the real bucket name %q, got %q", testExportBucket, got)
	}
	if got := detailString(e, "object_bucket_role"); got != "export" {
		t.Fatalf("object_bucket_role must be %q, got %q", "export", got)
	}
	if got := detailString(e, "object_ref"); got != "exports/audit-case.csv" {
		t.Fatalf("object_ref mismatch: %q", got)
	}
	if got := detailString(e, "path"); got != path {
		t.Fatalf("path=%q want %q", got, path)
	}
	if e.ResourceID != "44444444-4444-4444-4444-444444444444" {
		t.Fatalf("resource_id must be the export id, got %q", e.ResourceID)
	}
	if e.HTTPMethod != method {
		t.Fatalf("http method=%q want %q", e.HTTPMethod, method)
	}
	if e.Result != "success" {
		t.Fatalf("result=%q want success", e.Result)
	}
	if got := detailInt(e, "bytes"); got != bytes {
		t.Fatalf("bytes=%d want %d", got, bytes)
	}
}

func TestDownloadFullWritesSuccessAuditWithRealBucket(t *testing.T) {
	stub := newS3Stub(t)
	body := strings.Repeat("row\n", 512)
	stub.objects[testExportBucket]["exports/audit-case.csv"] = body
	s, sink := newDownloadAuditServer(t, stub)

	rec := httptest.NewRecorder()
	s.serveExportObject(rec, downloadTestRequest(http.MethodGet, "/api/v1/exports/x/download"), downloadTestExport(), nil)

	if rec.Code != http.StatusOK {
		t.Fatalf("status=%d want 200 (body=%s)", rec.Code, rec.Body.String())
	}
	if rec.Body.String() != body {
		t.Fatalf("body mismatch: %d bytes", rec.Body.Len())
	}
	if sink.count("export.download") != 1 {
		t.Fatalf("want exactly 1 export.download audit, got %d (%v)", sink.count("export.download"), sink.events)
	}
	if sink.count("export.download_failed") != 0 {
		t.Fatalf("unexpected download_failed audit on a clean transfer")
	}
	assertSuccessAudit(t, sink.find("export.download"), "full", http.MethodGet, int64(len(body)))
}

func TestDownloadRangeWritesSuccessAudit(t *testing.T) {
	stub := newS3Stub(t)
	body := strings.Repeat("0123456789", 300)
	stub.objects[testExportBucket]["exports/audit-case.csv"] = body
	s, sink := newDownloadAuditServer(t, stub)

	r := downloadTestRequest(http.MethodGet, "/api/v1/exports/x/download")
	r.Header.Set("Range", "bytes=100-199")
	rec := httptest.NewRecorder()
	s.serveExportObject(rec, r, downloadTestExport(), nil)

	if rec.Code != http.StatusPartialContent {
		t.Fatalf("status=%d want 206", rec.Code)
	}
	if rec.Body.String() != body[100:200] {
		t.Fatalf("range body mismatch: %q", rec.Body.String())
	}
	assertSuccessAudit(t, sink.find("export.download"), "range", http.MethodGet, 100)
}

func TestDownloadHeadWritesSuccessAudit(t *testing.T) {
	stub := newS3Stub(t)
	body := strings.Repeat("x", 4096)
	stub.objects[testExportBucket]["exports/audit-case.csv"] = body
	s, sink := newDownloadAuditServer(t, stub)

	rec := httptest.NewRecorder()
	s.serveExportObject(rec, downloadTestRequest(http.MethodHead, "/api/v1/exports/x/download"), downloadTestExport(), nil)

	if rec.Code != http.StatusOK {
		t.Fatalf("status=%d want 200", rec.Code)
	}
	if rec.Body.Len() != 0 {
		t.Fatalf("HEAD must not write a body, got %d bytes", rec.Body.Len())
	}
	assertSuccessAudit(t, sink.find("export.download"), "head", http.MethodHead, int64(len(body)))
}

func TestDownloadMissingObjectIs503WithFailureAudit(t *testing.T) {
	stub := newS3Stub(t)
	s, sink := newDownloadAuditServer(t, stub)

	rec := httptest.NewRecorder()
	s.serveExportObject(rec, downloadTestRequest(http.MethodGet, "/api/v1/exports/x/download"), downloadTestExport(), nil)

	if rec.Code != http.StatusServiceUnavailable {
		t.Fatalf("status=%d want 503 (body=%s)", rec.Code, rec.Body.String())
	}
	if !strings.Contains(rec.Body.String(), "DATA_OBJECT_MISSING") {
		t.Fatalf("body must carry DATA_OBJECT_MISSING, got %s", rec.Body.String())
	}
	if sink.count("export.download") != 0 {
		t.Fatalf("a missing object must never produce a success audit")
	}
	ev := sink.find("export.download_failed")
	if ev == nil {
		t.Fatalf("missing object must write export.download_failed")
	}
	if got := detailString(ev, "reason"); got != "object_missing" {
		t.Fatalf("reason=%q want object_missing", got)
	}
}

func TestDownloadNonNotFoundFailsClosedWithoutSuccessAudit(t *testing.T) {
	stub := newS3Stub(t)
	body := strings.Repeat("y", 128)
	stub.objects[testRawBucket]["exports/audit-case.csv"] = body
	// 导出桶不存在（部署/配置故障）——即使 RAW 有对象也绝不回退。
	delete(stub.objects, testExportBucket)
	s, sink := newDownloadAuditServer(t, stub)

	rec := httptest.NewRecorder()
	s.serveExportObject(rec, downloadTestRequest(http.MethodGet, "/api/v1/exports/x/download"), downloadTestExport(), nil)

	if rec.Code != http.StatusInternalServerError {
		t.Fatalf("status=%d want 500 (fail closed, no fallback)", rec.Code)
	}
	if strings.Contains(rec.Body.String(), "DATA_OBJECT_MISSING") {
		t.Fatalf("bucket-level failure must not be reported as an object-level miss")
	}
	if sink.count("export.download") != 0 {
		t.Fatalf("fail-closed path must not write a success audit")
	}
	// 非 404 必须留可操作线索（本轮根复核确认的第二个缺陷面：旧实现这条分支不写任何审计）。
	ev := sink.find("export.download_failed")
	if ev == nil {
		t.Fatalf("storage fault must write export.download_failed")
	}
	// 桶级故障按具体失败形态分类：NoSuchBucket -> bucket_missing；InvalidBucketName/网络/5xx
	// -> storage_fault。两者都必须 fail closed，且**绝不能**被记成 object_missing。
	if got := detailString(ev, "reason"); got != "bucket_missing" && got != "storage_fault" {
		t.Fatalf("reason=%q want bucket_missing or storage_fault (never object_missing)", got)
	}
	if got := detailString(ev, "path"); got != "download" {
		t.Fatalf("path=%q want download", got)
	}
	if got := detailString(ev, "action"); got != "notify_admin" {
		t.Fatalf("action=%q want notify_admin", got)
	}
	if got := detailString(ev, "storage_error"); got == "" {
		t.Fatalf("storage_error must carry the concrete failure text")
	}
}

// failingResponseWriter 在第 budget 字节之后返回写错误，模拟客户端中断/写端失败。
type failingResponseWriter struct {
	hdr     http.Header
	status  int
	budget  int
	written int
}

func (w *failingResponseWriter) Header() http.Header {
	if w.hdr == nil {
		w.hdr = http.Header{}
	}
	return w.hdr
}

func (w *failingResponseWriter) WriteHeader(code int) { w.status = code }

func (w *failingResponseWriter) Write(p []byte) (int, error) {
	// 隐式 200：与 net/http 一致，第一次 Write 前未显式 WriteHeader 时状态码为 200。
	if w.status == 0 {
		w.status = http.StatusOK
	}
	if w.budget <= 0 {
		return 0, errors.New("write tcp: broken pipe")
	}
	if len(p) > w.budget {
		n := w.budget
		w.budget = 0
		w.written += n
		return n, errors.New("write tcp: broken pipe")
	}
	w.budget -= len(p)
	w.written += len(p)
	return len(p), nil
}

func TestDownloadStreamInterruptionIsNotReportedAsSuccess(t *testing.T) {
	stub := newS3Stub(t)
	body := strings.Repeat("z", 64*1024)
	stub.objects[testExportBucket]["exports/audit-case.csv"] = body
	s, sink := newDownloadAuditServer(t, stub)

	w := &failingResponseWriter{budget: 1024}
	s.serveExportObject(w, downloadTestRequest(http.MethodGet, "/api/v1/exports/x/download"), downloadTestExport(), nil)

	if w.status != http.StatusOK {
		t.Fatalf("status=%d want 200 written before the interruption", w.status)
	}
	if sink.count("export.download") != 0 {
		t.Fatalf("an interrupted transfer must never be audited as a successful download")
	}
	ev := sink.find("export.download_failed")
	if ev == nil {
		t.Fatalf("interrupted transfer must write export.download_failed")
	}
	if got := detailString(ev, "reason"); got != "client_abort" {
		t.Fatalf("reason=%q want client_abort", got)
	}
	if got := detailInt(ev, "bytes_written"); got <= 0 || got >= int64(len(body)) {
		t.Fatalf("bytes_written=%d must be >0 and < full size %d", got, len(body))
	}
	if got := detailString(ev, "object_bucket"); got != testExportBucket {
		t.Fatalf("failure audit must still carry the real bucket name, got %q", got)
	}
}

// 审计写失败不得把下载伪装成成功，也不得静默：事件写入被拒绝时调用方仍成功返回，
// 但必须留下 Error 日志（由 emitAudit 的调用点负责），且不能写入第二条成功审计。
func TestAuditWriterFailureDoesNotDuplicateOrFakeSuccess(t *testing.T) {
	stub := newS3Stub(t)
	body := strings.Repeat("k", 256)
	stub.objects[testExportBucket]["exports/audit-case.csv"] = body
	s, sink := newDownloadAuditServer(t, stub)
	sink.err = errors.New("audit backend unavailable")

	rec := httptest.NewRecorder()
	s.serveExportObject(rec, downloadTestRequest(http.MethodGet, "/api/v1/exports/x/download"), downloadTestExport(), nil)

	if rec.Code != http.StatusOK || rec.Body.String() != body {
		t.Fatalf("business result must stay intact when audit write fails")
	}
	if len(sink.events) != 0 {
		t.Fatalf("no audit event may be recorded when the writer fails, got %d", len(sink.events))
	}
}

// cancelAwareAuditSink models a real database driver: a write attempted with an already-canceled
// context fails. pgx behaved exactly this way in production on 2026-09-13
// ({"level":"ERROR","msg":"audit write failed","action":"export.download","err":"context canceled"}),
// which is why a fully delivered export download left NO audit row while Range/HEAD were fine.
type cancelAwareAuditSink struct {
	recordingAuditSink
}

func (s *cancelAwareAuditSink) Write(ctx context.Context, e audit.Event) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	return s.recordingAuditSink.Write(ctx, e)
}

// cancelOnLastWrite cancels the request context as soon as the client has received the last byte of
// the body -- the real sequence that produced the production failure. The audit is written right
// after that point, which is why a canceled context used to lose it.
type cancelOnLastWrite struct {
	http.ResponseWriter
	remaining int
	cancel    context.CancelFunc
	canceled  bool
}

func (w *cancelOnLastWrite) Write(p []byte) (int, error) {
	n, err := w.ResponseWriter.Write(p)
	w.remaining -= n
	if w.remaining <= 0 && !w.canceled {
		w.canceled = true
		w.cancel()
	}
	return n, err
}

// TestDownloadFullAuditSurvivesClientDisconnect is the regression test for the production defect
// found on 2026-09-13: the success audit of a FULL download is written after the body has been
// streamed, i.e. after the client closed the connection, so the audit INSERT failed with
// "context canceled" and a successful download left NO audit row (Range/HEAD were unaffected).
// The audit describes a download that really happened and must survive the disconnect.
func TestDownloadFullAuditSurvivesClientDisconnect(t *testing.T) {
	stub := newS3Stub(t)
	body := strings.Repeat("row-value-line-", 2048)
	stub.objects[testExportBucket]["exports/audit-case.csv"] = body
	s := newTestServer(t, stub, nil)
	sink := &cancelAwareAuditSink{}
	s.auditSink = sink

	req := downloadTestRequest(http.MethodGet, "/api/v1/exports/x/download")
	ctx, cancel := context.WithCancel(req.Context())
	defer cancel()
	req = req.WithContext(ctx)

	rec := httptest.NewRecorder()
	writer := &cancelOnLastWrite{ResponseWriter: rec, remaining: len(body), cancel: cancel}
	s.serveExportObject(writer, req, downloadTestExport(), nil)

	if rec.Code != http.StatusOK || rec.Body.String() != body {
		t.Fatalf("download must still deliver the real bytes (status=%d bytes=%d)", rec.Code, rec.Body.Len())
	}
	if !writer.canceled {
		t.Fatalf("test setup error: the context was not canceled after the last byte")
	}
	if got := sink.count("export.download"); got != 1 {
		t.Fatalf("client disconnect must NOT suppress the success audit: got %d export.download events", got)
	}
	assertSuccessAudit(t, sink.find("export.download"), "full", http.MethodGet, int64(len(body)))
}
