#!/usr/bin/env python3
"""Product fix: download audits must survive client disconnect.

Real defect observed in production logs on 2026-09-13:
  {"level":"ERROR","msg":"audit write failed","action":"export.download","err":"context canceled"}
A full export download writes its success audit AFTER io.CopyBuffer returns. By then the client has
received every byte and closed the connection, so r.Context() is already canceled and the audit
INSERT fails -> the download succeeds but leaves NO audit row. Range (small body) and HEAD (no body)
were unaffected, which is why the previous httptest-only evidence looked complete.
The ticket path shares serveExportObject, so ticket downloads lost their audit the same way.

Fix: every download audit is written through a request whose context keeps the request's values
(principal, request/trace metadata, origin) but is detached from client cancellation.
"""
import hashlib
import sys

path = sys.argv[1]
src = open(path, encoding="utf-8").read()

helper_anchor = '// auditExportDownloadFailed：DATA-008 语义——对象在所有读桶中都不存在时，绝不宣称成功，'
helper = '''// auditRequestDetached 返回一个"与客户端断连解耦"的请求：context 保留原请求的值
// （principal / meta / origin / request_id / trace_id），但**不会**因为客户端关闭连接而被取消。
//
// 真实缺陷（2026-09-13 生产日志实测）：全量导出下载的成功审计在 io.CopyBuffer 返回之后才写，
// 此时客户端已收完字节并关闭连接，r.Context() 已取消，审计 INSERT 直接失败：
//   {"level":"ERROR","msg":"audit write failed","action":"export.download","err":"context canceled"}
// 结果是下载成功但审计链上**没有这一条记录**；Range（响应体很小）与 HEAD（无响应体）不受影响，
// 所以只看 httptest 的旧证据完全看不出来。票据下载路径复用同一个 serveExportObject，同样丢审计。
// 审计写入必须反映"这次下载确实发生了"，而不是"连接还活着"。
func auditRequestDetached(r *http.Request) *http.Request {
	return r.WithContext(context.WithoutCancel(r.Context()))
}

// auditExportDownloadFailed：DATA-008 语义——对象在所有读桶中都不存在时，绝不宣称成功，'''

call_sites = [
    ('s.auditExportDownloadFailed(r, exp, "range")', 's.auditExportDownloadFailed(auditRequestDetached(r), exp, "range")'),
    ('s.auditExportStorageFault(r, exp, "range", err)', 's.auditExportStorageFault(auditRequestDetached(r), exp, "range", err)'),
    ('s.auditExportStreamResult(r, exp, loc, "range", want, written, copyErr, extra)', 's.auditExportStreamResult(auditRequestDetached(r), exp, loc, "range", want, written, copyErr, extra)'),
    ('s.auditExportDownloadFailed(r, exp, "download")', 's.auditExportDownloadFailed(auditRequestDetached(r), exp, "download")'),
    ('s.auditExportStorageFault(r, exp, "download", err)', 's.auditExportStorageFault(auditRequestDetached(r), exp, "download", err)'),
    ('s.auditExportDownloadSucceeded(r, exp, loc, "head", size, extra)', 's.auditExportDownloadSucceeded(auditRequestDetached(r), exp, loc, "head", size, extra)'),
    ('s.auditExportStreamResult(r, exp, loc, "full", size, written, copyErr, extra)', 's.auditExportStreamResult(auditRequestDetached(r), exp, loc, "full", size, written, copyErr, extra)'),
]

before = hashlib.sha256(src.encode("utf-8")).hexdigest()
count = src.count(helper_anchor)
print("helper_anchor_count=" + str(count))
if count != 1:
    print("PATCH_REFUSED helper anchor must appear exactly once")
    sys.exit(3)
src = src.replace(helper_anchor, helper)
for old, new in call_sites:
    c = src.count(old)
    print("call_site_count=" + str(c) + " :: " + old[:60])
    if c != 1:
        print("PATCH_REFUSED call site must appear exactly once")
        sys.exit(3)
    src = src.replace(old, new)
after = hashlib.sha256(src.encode("utf-8")).hexdigest()
open(path, "w", encoding="utf-8").write(src)
print("sha256_before=" + before)
print("sha256_after=" + after)
print("PATCH_APPLIED")
