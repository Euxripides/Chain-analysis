#!/usr/bin/env python3
# patch-db2raw03.py -- D-B2RAW-03: clamp the audit write to min(caller deadline, now+5s).
#
# Found by the 2026-09-13 review of auditBoundedContext: the D-B2RAW-02 fix only bounded audit
# writes whose context had NO deadline, so a caller carrying a LONGER deadline (a request or
# background context with its own multi-minute/one-hour timeout) silently kept an effectively
# unbounded audit write. TestAuditBoundedContextAddsAndPreservesDeadline even pinned that hole
# with a one-hour parent context. The safety intent is an upper bound, so a later deadline is now
# clamped down to now+auditWriteTimeout, while an EARLIER deadline is still preserved exactly.
#
# Properties (all asserted, never assumed):
#   - ASCII-only source file (this script contains no byte >= 0x80);
#   - every anchor must occur EXACTLY ONCE in its target file, otherwise nothing is written;
#   - --check prints the resulting sha256 without touching the file; --apply backs the original
#     up next to itself as <name>.pre-db2raw03-<TS> before writing;
#   - after apply the file is read back and compared, and the resulting sha256 must equal
#     --expect-sha when that is provided.
#
# Usage:
#   python3 patch-db2raw03.py --check --root /home/euripides/projects/blockchain-platform
#   python3 patch-db2raw03.py --apply --root ... --ts 20260913T1520Z
import argparse
import base64
import hashlib
import os
import sys

PAIR_0_OLD = (
    "Ly8gYXVkaXRCb3VuZGVkQ29udGV4dCBhZGRzIGEgYm91bmRlZCB0aW1lb3V0IHRvIGEgY29udGV4dCB0aGF0IGhhcyBubyBk"
    "ZWFkbGluZSwgcmV0dXJuaW5nIHRoZQovLyBtYXRjaGluZyBjYW5jZWwuIEEgY29udGV4dCB0aGF0IGFscmVhZHkgY2Fycmll"
    "cyBhIGRlYWRsaW5lIChhIGJhY2tncm91bmQgdGFzayB3aXRoIGl0cyBvd24KLy8gdGltZW91dCkgaXMgcmV0dXJuZWQgdW5j"
    "aGFuZ2VkOiBuZXZlciBzaG9ydGVuIG9yIHdpZGVuIHRoZSBjYWxsZXIncyBvd24gc2VtYW50aWNzLgpmdW5jIGF1ZGl0Qm91"
    "bmRlZENvbnRleHQoY3R4IGNvbnRleHQuQ29udGV4dCkgKGNvbnRleHQuQ29udGV4dCwgY29udGV4dC5DYW5jZWxGdW5jKSB7"
    "CglpZiBfLCBvayA6PSBjdHguRGVhZGxpbmUoKTsgb2sgewoJCXJldHVybiBjdHgsIGZ1bmMoKSB7fQoJfQoJcmV0dXJuIGNv"
    "bnRleHQuV2l0aFRpbWVvdXQoY3R4LCBhdWRpdFdyaXRlVGltZW91dCk="
)
PAIR_0_NEW = (
    "Ly8gYXVkaXRCb3VuZGVkQ29udGV4dCBjbGFtcHMgYW4gYXVkaXQgd3JpdGUgdG8gdGhlIFNNQUxMRVIgb2YgdGhlIGNhbGxl"
    "cidzIG93biBkZWFkbGluZSBhbmQKLy8gbm93K2F1ZGl0V3JpdGVUaW1lb3V0LCByZXR1cm5pbmcgdGhlIG1hdGNoaW5nIGNh"
    "bmNlbC4KLy8KLy8gRC1CMlJBVy0wMyAoMjAyNi0wOS0xMyByZXZpZXcsIHNlY29uZCBmYWNlIG9mIEQtQjJSQVctMDIpOiB0"
    "aGUgZmlyc3QgZml4IG9ubHkgYWRkZWQgYSBib3VuZAovLyB3aGVuIHRoZSBjYWxsZXIgaGFkIE5PIGRlYWRsaW5lLCBzbyBh"
    "bnkgY2FsbGVyIGNhcnJ5aW5nIGEgZGVhZGxpbmUgTE9OR0VSIHRoYW4gdGhlIGJvdW5kCi8vIChhIHJlcXVlc3QvYmFja2dy"
    "b3VuZCBjb250ZXh0IHdpdGggaXRzIG93biBtdWx0aS1taW51dGUgb3Igb25lLWhvdXIgdGltZW91dCkgc2lsZW50bHkga2Vw"
    "dCBhbgovLyBlZmZlY3RpdmVseSB1bmJvdW5kZWQgYXVkaXQgd3JpdGUsIGFuZCBUZXN0QXVkaXRCb3VuZGVkQ29udGV4dEFk"
    "ZHNBbmRQcmVzZXJ2ZXNEZWFkbGluZSBwaW5uZWQKLy8gdGhhdCBob2xlIHdpdGggYSBvbmUtaG91ciBwYXJlbnQuIFRoZSBz"
    "YWZldHkgaW50ZW50IGlzIGFuIHVwcGVyIGJvdW5kIG9uIHRoZSB3cml0ZSwgc28gYSBsYXRlcgovLyBkZWFkbGluZSBpcyBu"
    "b3cgY2xhbXBlZCBkb3duIHRvIG5vdythdWRpdFdyaXRlVGltZW91dDsgYW4gRUFSTElFUiBkZWFkbGluZSBpcyBzdGlsbCBw"
    "cmVzZXJ2ZWQKLy8gZXhhY3RseSwgYmVjYXVzZSBzaG9ydGVuaW5nIHRoZSBjYWxsZXIncyBvd24gc2VtYW50aWNzIGlzIG5l"
    "dmVyIGFsbG93ZWQgZWl0aGVyLgpmdW5jIGF1ZGl0Qm91bmRlZENvbnRleHQoY3R4IGNvbnRleHQuQ29udGV4dCkgKGNvbnRl"
    "eHQuQ29udGV4dCwgY29udGV4dC5DYW5jZWxGdW5jKSB7CglsaW1pdCA6PSB0aW1lLk5vdygpLkFkZChhdWRpdFdyaXRlVGlt"
    "ZW91dCkKCWlmIGRsLCBvayA6PSBjdHguRGVhZGxpbmUoKTsgb2sgJiYgIWRsLkFmdGVyKGxpbWl0KSB7CgkJcmV0dXJuIGN0"
    "eCwgZnVuYygpIHt9Cgl9CglyZXR1cm4gY29udGV4dC5XaXRoRGVhZGxpbmUoY3R4LCBsaW1pdCk="
)

PAIR_1_OLD = (
    "Ly8gVGVzdEF1ZGl0Qm91bmRlZENvbnRleHRBZGRzQW5kUHJlc2VydmVzRGVhZGxpbmUgcGlucyB0aGUgaGVscGVyJ3MgY29u"
    "dHJhY3Q6IGFkZCBhIGJvdW5kIHdoZW4gdGhlCi8vIGNhbGxlciBoYXMgbm9uZSwgYW5kIG5ldmVyIG92ZXJyaWRlIGEgYm91"
    "bmQgdGhlIGNhbGxlciBhbHJlYWR5IHNldC4KZnVuYyBUZXN0QXVkaXRCb3VuZGVkQ29udGV4dEFkZHNBbmRQcmVzZXJ2ZXNE"
    "ZWFkbGluZSh0ICp0ZXN0aW5nLlQpIHsKCWJvdW5kZWQsIGNhbmNlbCA6PSBhdWRpdEJvdW5kZWRDb250ZXh0KGNvbnRleHQu"
    "QmFja2dyb3VuZCgpKQoJZGVmZXIgY2FuY2VsKCkKCWRsLCBvayA6PSBib3VuZGVkLkRlYWRsaW5lKCkKCWlmICFvayB7CgkJ"
    "dC5GYXRhbCgiYSBjb250ZXh0IHdpdGhvdXQgYSBkZWFkbGluZSBtdXN0IHJlY2VpdmUgdGhlIGF1ZGl0IGJvdW5kIikKCX0K"
    "CWlmIGxlZnQgOj0gdGltZS5VbnRpbChkbCk7IGxlZnQgPD0gMCB8fCBsZWZ0ID4gYXVkaXRXcml0ZVRpbWVvdXQgewoJCXQu"
    "RmF0YWxmKCJhdWRpdCBib3VuZCBvdXQgb2YgcmFuZ2U6ICV2IChib3VuZCAldikiLCBsZWZ0LCBhdWRpdFdyaXRlVGltZW91"
    "dCkKCX0KCglwYXJlbnQsIHBjYW5jZWwgOj0gY29udGV4dC5XaXRoVGltZW91dChjb250ZXh0LkJhY2tncm91bmQoKSwgdGlt"
    "ZS5Ib3VyKQoJZGVmZXIgcGNhbmNlbCgpCgl3YW50LCBfIDo9IHBhcmVudC5EZWFkbGluZSgpCglrZXB0LCBrY2FuY2VsIDo9"
    "IGF1ZGl0Qm91bmRlZENvbnRleHQocGFyZW50KQoJZGVmZXIga2NhbmNlbCgpCglnb3QsIG9rIDo9IGtlcHQuRGVhZGxpbmUo"
    "KQoJaWYgIW9rIHx8ICFnb3QuRXF1YWwod2FudCkgewoJCXQuRmF0YWxmKCJhbiBleGlzdGluZyBkZWFkbGluZSBtdXN0IGJl"
    "IHByZXNlcnZlZDogd2FudCAldiBnb3QgJXYgKG9rPSV2KSIsIHdhbnQsIGdvdCwgb2sp"
)
PAIR_1_NEW = (
    "Ly8gVGVzdEF1ZGl0Qm91bmRlZENvbnRleHRBZGRzQW5kUHJlc2VydmVzRGVhZGxpbmUgcGlucyB0aGUgaGVscGVyJ3MgY29u"
    "dHJhY3QgYWZ0ZXIgdGhlCi8vIDIwMjYtMDktMTMgcmV2aWV3IChELUIyUkFXLTAzKTogdGhlIGF1ZGl0IHdyaXRlIGlzIGJv"
    "dW5kZWQgYnkgbWluKGNhbGxlciBkZWFkbGluZSwKLy8gbm93K2F1ZGl0V3JpdGVUaW1lb3V0KS4KLy8gICAtIG5vIGRlYWRs"
    "aW5lICAgICAgLT4gdGhlIGF1ZGl0IGJvdW5kIGlzIGFkZGVkOwovLyAgIC0gRUFSTElFUiBkZWFkbGluZSAtPiBwcmVzZXJ2"
    "ZWQgZXhhY3RseSAodGhlIGNhbGxlcidzIG93biBzZW1hbnRpY3MgYXJlIG5ldmVyIHdpZGVuZWQpOwovLyAgIC0gTEFURVIg"
    "ZGVhZGxpbmUgICAtPiBjbGFtcGVkIGRvd24gdG8gbm93K2F1ZGl0V3JpdGVUaW1lb3V0LiBCZWZvcmUgdGhlIGZpeCB0aGUg"
    "aGVscGVyCi8vICAgICByZXR1cm5lZCBBTlkgZXhpc3RpbmcgZGVhZGxpbmUgdW5jaGFuZ2VkLCBzbyBhIGxvbmdlciAoZS5n"
    "LiBvbmUtaG91cikgY2FsbGVyIGRlYWRsaW5lCi8vICAgICBzaWxlbnRseSBkaXNhYmxlZCB0aGUgYm91bmQgRC1CMlJBVy0w"
    "MiBpbnRyb2R1Y2VkIC0tIHRoZSBwcmV2aW91cyBhc3NlcnRpb24gaW4gdGhpcyB2ZXJ5Ci8vICAgICB0ZXN0IHBpbm5lZCB0"
    "aGF0IGhvbGUgYnkgcmVxdWlyaW5nIGEgb25lLWhvdXIgcGFyZW50IGRlYWRsaW5lIHRvIGJlIGtlcHQgdmVyYmF0aW0uCmZ1"
    "bmMgVGVzdEF1ZGl0Qm91bmRlZENvbnRleHRBZGRzQW5kUHJlc2VydmVzRGVhZGxpbmUodCAqdGVzdGluZy5UKSB7CglwcmV2"
    "aW91cyA6PSBhdWRpdFdyaXRlVGltZW91dAoJYXVkaXRXcml0ZVRpbWVvdXQgPSAxNTAgKiB0aW1lLk1pbGxpc2Vjb25kCglk"
    "ZWZlciBmdW5jKCkgeyBhdWRpdFdyaXRlVGltZW91dCA9IHByZXZpb3VzIH0oKQoKCS8vIDEpIG5vIGRlYWRsaW5lOiB0aGUg"
    "YXVkaXQgYm91bmQgaXMgYWRkZWQuCglib3VuZGVkLCBjYW5jZWwgOj0gYXVkaXRCb3VuZGVkQ29udGV4dChjb250ZXh0LkJh"
    "Y2tncm91bmQoKSkKCWRlZmVyIGNhbmNlbCgpCglkbCwgb2sgOj0gYm91bmRlZC5EZWFkbGluZSgpCglpZiAhb2sgewoJCXQu"
    "RmF0YWwoImEgY29udGV4dCB3aXRob3V0IGEgZGVhZGxpbmUgbXVzdCByZWNlaXZlIHRoZSBhdWRpdCBib3VuZCIpCgl9Cglp"
    "ZiBsZWZ0IDo9IHRpbWUuVW50aWwoZGwpOyBsZWZ0IDw9IDAgfHwgbGVmdCA+IGF1ZGl0V3JpdGVUaW1lb3V0IHsKCQl0LkZh"
    "dGFsZigiYXVkaXQgYm91bmQgb3V0IG9mIHJhbmdlOiAldiAoYm91bmQgJXYpIiwgbGVmdCwgYXVkaXRXcml0ZVRpbWVvdXQp"
    "Cgl9CgoJLy8gMikgZWFybGllciBkZWFkbGluZTogcHJlc2VydmVkIGV4YWN0bHksIG5ldmVyIHdpZGVuZWQuCglzaG9ydGVy"
    "LCBzY2FuY2VsIDo9IGNvbnRleHQuV2l0aFRpbWVvdXQoY29udGV4dC5CYWNrZ3JvdW5kKCksIGF1ZGl0V3JpdGVUaW1lb3V0"
    "LzMpCglkZWZlciBzY2FuY2VsKCkKCXdhbnRTaG9ydGVyLCBfIDo9IHNob3J0ZXIuRGVhZGxpbmUoKQoJa2VwdCwga2NhbmNl"
    "bCA6PSBhdWRpdEJvdW5kZWRDb250ZXh0KHNob3J0ZXIpCglkZWZlciBrY2FuY2VsKCkKCWdvdFNob3J0ZXIsIG9rIDo9IGtl"
    "cHQuRGVhZGxpbmUoKQoJaWYgIW9rIHx8ICFnb3RTaG9ydGVyLkVxdWFsKHdhbnRTaG9ydGVyKSB7CgkJdC5GYXRhbGYoImFu"
    "IGVhcmxpZXIgZGVhZGxpbmUgbXVzdCBiZSBwcmVzZXJ2ZWQ6IHdhbnQgJXYgZ290ICV2IChvaz0ldikiLCB3YW50U2hvcnRl"
    "ciwgZ290U2hvcnRlciwgb2spCgl9CgoJLy8gMykgbGF0ZXIgZGVhZGxpbmU6IGNsYW1wZWQgZG93biB0byB0aGUgYXVkaXQg"
    "Ym91bmQgKHRoaXMgaXMgdGhlIEQtQjJSQVctMDMgZ2FwKS4KCWxvbmdlciwgbGNhbmNlbCA6PSBjb250ZXh0LldpdGhUaW1l"
    "b3V0KGNvbnRleHQuQmFja2dyb3VuZCgpLCB0aW1lLkhvdXIpCglkZWZlciBsY2FuY2VsKCkKCWxvbmdlcldhbnQsIF8gOj0g"
    "bG9uZ2VyLkRlYWRsaW5lKCkKCWNsYW1wZWQsIGNjYW5jZWwgOj0gYXVkaXRCb3VuZGVkQ29udGV4dChsb25nZXIpCglkZWZl"
    "ciBjY2FuY2VsKCkKCWdvdExvbmdlciwgb2sgOj0gY2xhbXBlZC5EZWFkbGluZSgpCglpZiAhb2sgewoJCXQuRmF0YWwoImEg"
    "Y2xhbXBlZCBjb250ZXh0IG11c3Qgc3RpbGwgY2FycnkgYSBkZWFkbGluZSIpCgl9CglpZiAhZ290TG9uZ2VyLkJlZm9yZShs"
    "b25nZXJXYW50KSB7CgkJdC5GYXRhbGYoImEgbGF0ZXIgZGVhZGxpbmUgbXVzdCBiZSBjbGFtcGVkIGRvd246IHBhcmVudCAl"
    "diBjbGFtcGVkICV2IiwgbG9uZ2VyV2FudCwgZ290TG9uZ2VyKQoJfQoJaWYgbGVmdCA6PSB0aW1lLlVudGlsKGdvdExvbmdl"
    "cik7IGxlZnQgPD0gMCB8fCBsZWZ0ID4gYXVkaXRXcml0ZVRpbWVvdXQgewoJCXQuRmF0YWxmKCJjbGFtcGVkIGF1ZGl0IGJv"
    "dW5kIG91dCBvZiByYW5nZTogJXYgKGJvdW5kICV2KSIsIGxlZnQsIGF1ZGl0V3JpdGVUaW1lb3V0KQoJfQp9CgovLyBUZXN0"
    "QXVkaXRCb3VuZENsYW1wc0xvbmdDYWxsZXJEZWFkbGluZSBpcyB0aGUgYmVoYXZpb3VyYWwgaGFsZiBvZiB0aGUgRC1CMlJB"
    "Vy0wMyByZWdyZXNzaW9uOgovLyBhIGNhbGxlciBjb250ZXh0IGNhcnJ5aW5nIGEgT05FIEhPVVIgZGVhZGxpbmUgbXVzdCBu"
    "b3QgYmUgYWJsZSB0byBob2xkIHRoZSBhdWRpdCBzaW5rIGZvciBhbgovLyBob3VyLiBUaGUgc2luayBibG9ja3MgdW50aWwg"
    "aXRzIGNvbnRleHQgZW5kcywgc28gdGhpcyBjYW4gb25seSByZXR1cm4gaW4gdGltZSBpZiB0aGUgaGVscGVyCi8vIHJlYWxs"
    "eSBjbGFtcHMgdGhlIGRlYWRsaW5lIGRvd24gdG8gYXVkaXRXcml0ZVRpbWVvdXQuCmZ1bmMgVGVzdEF1ZGl0Qm91bmRDbGFt"
    "cHNMb25nQ2FsbGVyRGVhZGxpbmUodCAqdGVzdGluZy5UKSB7CglwcmV2aW91cyA6PSBhdWRpdFdyaXRlVGltZW91dAoJYXVk"
    "aXRXcml0ZVRpbWVvdXQgPSAxNTAgKiB0aW1lLk1pbGxpc2Vjb25kCglkZWZlciBmdW5jKCkgeyBhdWRpdFdyaXRlVGltZW91"
    "dCA9IHByZXZpb3VzIH0oKQoKCXMgOj0gJlNlcnZlcnthdWRpdFNpbms6ICZkZWFkbGluZUJsb2NraW5nQXVkaXRTaW5re319"
    "Cglsb25nLCBjYW5jZWwgOj0gY29udGV4dC5XaXRoVGltZW91dChjb250ZXh0LkJhY2tncm91bmQoKSwgdGltZS5Ib3VyKQoJ"
    "ZGVmZXIgY2FuY2VsKCkKCglkb25lIDo9IG1ha2UoY2hhbiBlcnJvciwgMSkKCWdvIGZ1bmMoKSB7IGRvbmUgPC0gcy5lbWl0"
    "QXVkaXQobG9uZywgYXVkaXQuRXZlbnR7fSkgfSgpCglzZWxlY3QgewoJY2FzZSBlcnIgOj0gPC1kb25lOgoJCWlmIGVyciAh"
    "PSBjb250ZXh0LkRlYWRsaW5lRXhjZWVkZWQgewoJCQl0LkZhdGFsZigiYSBjbGFtcGVkIGF1ZGl0IHdyaXRlIG11c3QgZW5k"
    "IHdpdGggRGVhZGxpbmVFeGNlZWRlZCwgZ290ICV2IiwgZXJyKQoJCX0KCWNhc2UgPC10aW1lLkFmdGVyKDMwICogdGltZS5T"
    "ZWNvbmQpOgoJCXQuRmF0YWwoImF1ZGl0IHdyaXRlIHdpdGggYSBvbmUtaG91ciBjYWxsZXIgZGVhZGxpbmUgd2FzIG5vdCBj"
    "bGFtcGVkIChzdGlsbCB1bmJvdW5kZWQpIikKCX0KCXNpbmssIF8gOj0gcy5hdWRpdFNpbmsuKCpkZWFkbGluZUJsb2NraW5n"
    "QXVkaXRTaW5rKQoJaWYgc2luay5yZW1haW5pbmcgPD0gMCB8fCBzaW5rLnJlbWFpbmluZyA+IGF1ZGl0V3JpdGVUaW1lb3V0"
    "IHsKCQl0LkZhdGFsZigiY2xhbXBlZCBhdWRpdCBkZWFkbGluZSBvdXQgb2YgcmFuZ2U6ICV2IChib3VuZCAldikiLCBzaW5r"
    "LnJlbWFpbmluZywgYXVkaXRXcml0ZVRpbWVvdXQp"
)

PAIRS = []

PAIRS.append((
    "backend/internal/api/server.go",
    base64.b64decode(PAIR_0_OLD).decode('utf-8'),
    base64.b64decode(PAIR_0_NEW).decode('utf-8'),
))

PAIRS.append((
    "backend/internal/api/export_download_audit_test.go",
    base64.b64decode(PAIR_1_OLD).decode('utf-8'),
    base64.b64decode(PAIR_1_NEW).decode('utf-8'),
))


def main():
    ap = argparse.ArgumentParser()
    ap.add_argument('--root', default='/home/euripides/projects/blockchain-platform')
    ap.add_argument('--check', action='store_true')
    ap.add_argument('--apply', action='store_true')
    ap.add_argument('--ts', default='')
    ap.add_argument('--expect-sha', default='')
    args = ap.parse_args()
    if args.check == args.apply:
        print('ERROR: choose exactly one of --check / --apply')
        return 2
    if not args.ts and args.apply:
        print('ERROR: --ts is required with --apply')
        return 2

    patched = {}
    for rel, old, new in PAIRS:
        path = os.path.join(args.root, rel)
        with open(path, 'r', encoding='utf-8') as fh:
            body = fh.read()
        hits = body.count(old)
        print('file=%s anchors=%d old_bytes=%d new_bytes=%d' % (rel, hits, len(old), len(new)))
        if hits != 1:
            print('ABORT: anchor must occur exactly once in %s (got %d); nothing written' % (rel, hits))
            return 3
        if not new.isascii():
            print('ABORT: replacement text for %s is not ASCII' % rel)
            return 4
        patched[rel] = body.replace(old, new, 1)
        print('  result_sha256=%s' % hashlib.sha256(patched[rel].encode('utf-8')).hexdigest())

    if args.expect_sha:
        wanted = dict(pair.split('=', 1) for pair in args.expect_sha.split(',') if pair)
        for rel, sha in wanted.items():
            if rel not in patched:
                print('ABORT: --expect-sha names an unknown file %s' % rel)
                return 8
            got = hashlib.sha256(patched[rel].encode('utf-8')).hexdigest()
            if got != sha:
                print('ABORT: %s would become %s, expected %s' % (rel, got, sha))
                return 5
        print('expect_sha_check=OK')

    if args.check:
        print('CHECK_ONLY: no file written')
        return 0

    for rel, text in patched.items():
        path = os.path.join(args.root, rel)
        backup = path + '.pre-db2raw03-' + args.ts
        if os.path.exists(backup):
            print('ABORT: backup %s already exists; refusing to overwrite' % backup)
            return 6
        with open(path, 'r', encoding='utf-8') as fh:
            original = fh.read()
        with open(backup, 'w', encoding='utf-8', newline='') as fh:
            fh.write(original)
        with open(path, 'w', encoding='utf-8', newline='') as fh:
            fh.write(text)
        with open(path, 'r', encoding='utf-8') as fh:
            back = fh.read()
        if back != text:
            print('ABORT: readback mismatch for %s' % rel)
            return 7
        print('APPLIED %s backup=%s sha256=%s' % (rel, backup, hashlib.sha256(back.encode('utf-8')).hexdigest()))
    return 0


if __name__ == '__main__':
    sys.exit(main())
