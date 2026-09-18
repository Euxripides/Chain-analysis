#!/usr/bin/env python3
"""Replace the disconnect regression test with one that cancels the context at the exact moment the
client has received the last byte (the real production sequence), instead of before the handler runs."""
import hashlib
import sys

path = sys.argv[1]
src = open(path, encoding="utf-8").read()
start = src.index("// TestDownloadFullAuditSurvivesClientDisconnect is the regression test")
new_body = '''// cancelOnLastWrite cancels the request context as soon as the client has received the last byte of
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
	body := strings.Repeat("row,value\n", 2048)
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
'''
before = hashlib.sha256(src.encode("utf-8")).hexdigest()
head = src[:start]
# keep the pre-existing tail (nothing follows the test in this file)
src = head + new_body
open(path, "w", encoding="utf-8").write(src)
print("sha256_before=" + before)
print("sha256_after=" + hashlib.sha256(src.encode("utf-8")).hexdigest())
print("REPLACED")
