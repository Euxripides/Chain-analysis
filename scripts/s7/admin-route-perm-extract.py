#!/usr/bin/env python3
# admin-route-perm-extract.py -- mechanically extract the declared route -> permission
# mapping for /api/admin/v1 from the DEPLOYED source tree.
#
# Why: the C1 A5 acceptance gate must test the RUNNING binary per route, and the only
# defensible expectation is the declaration that the binary was built from. The gate
# therefore extracts the declaration from the VM source (never from a hand-written
# table) and records the sha256 of every source file it read, so a stale declaration
# can never silently vouch for a different binary.
#
# Output (stdout), one record per line:
#   ROUTE|<METHOD>|<full path>|<perm1,perm2,...>|<file>:<line>
#   SRC|<relative file>|<sha256>
# ASCII only. Read-only: opens files for reading and nothing else.
import hashlib
import os
import re
import sys

API_DIR = sys.argv[1] if len(sys.argv) > 1 else "."
ROOT = os.path.dirname(os.path.dirname(os.path.abspath(API_DIR)))

FUNCS = [
    "registerAdminV1",
    "registerAdminOrgRoutes",
    "registerAdminIdentityRoutes",
    "registerAdminControlRoutes",
    "registerAdminProviderRoutes",
    "registerAdminGovernanceRoutes",
    "registerAdminObservabilityRoutes",
    "registerAdminAuditIntegrityRoutes",
]

ROUTE_RE = re.compile(r'^\s*([A-Za-z][A-Za-z0-9_]*)\.(Get|Post|Patch|Put|Delete)\("([^"]*)"\s*,(.*)$')
REQ_RE = re.compile(r's\.require\("([^"]+)"')
REQANY_RE = re.compile(r'requireAny\(\s*\[\](?:string)?\s*\{?([^\]\}]*)')


def func_body(src, name):
    m = re.search(r'func \(s \*Server\) ' + re.escape(name) + r'\(r chi\.Router\) \{', src)
    if not m:
        return None, 0
    start = m.end() - 1
    depth = 0
    i = start
    while i < len(src):
        c = src[i]
        if c == '{':
            depth += 1
        elif c == '}':
            depth -= 1
            if depth == 0:
                return src[start:i + 1], src[:start].count('\n') + 1
        i += 1
    return None, 0


def main():
    if not os.path.isdir(API_DIR):
        print("ERR|api dir not found: " + API_DIR)
        return 2
    files = sorted(f for f in os.listdir(API_DIR) if f.endswith('.go') and not f.endswith('_test.go'))
    shas = {}
    records = []
    sources = {}
    for f in files:
        p = os.path.join(API_DIR, f)
        raw = open(p, 'rb').read()
        shas[f] = hashlib.sha256(raw).hexdigest()
        sources[f] = raw.decode('utf-8', 'replace')

    for f in files:
        src = sources[f]
        for fn in FUNCS:
            body, line0 = func_body(src, fn)
            if body is None:
                continue
            blines = body.split('\n')
            for off, ln in enumerate(blines):
                m = ROUTE_RE.match(ln)
                if not m:
                    continue
                _recv, method, path, rest = m.groups()
                # A route statement may span several physical lines (e.g. the
                # provider command entry wraps its requireAny list). Accumulate
                # until parentheses balance so a wrapped declaration is never
                # mis-reported as "no permission declared".
                j = off
                while rest.count('(') > rest.count(')') and j + 1 < len(blines):
                    j += 1
                    rest = rest + ' ' + blines[j].strip()
                perms = REQ_RE.findall(rest)
                if not perms:
                    for mm in REQANY_RE.finditer(rest):
                        for q in re.findall(r'"([^"]+)"', mm.group(1)):
                            perms.append(q)
                full = '/api/admin/v1' + path
                records.append((method.upper(), full, ','.join(perms) if perms else '(none)',
                                f + ':' + str(line0 + off)))

    seen = set()
    for method, full, perms, loc in records:
        key = method + ' ' + full
        if key in seen:
            print('DUP|' + key + '|' + loc)
            continue
        seen.add(key)
        print('ROUTE|' + method + '|' + full + '|' + perms + '|' + loc)

    for f in files:
        rel = os.path.relpath(os.path.join(API_DIR, f), ROOT)
        print('SRC|' + rel + '|' + shas[f])

    print('COUNT|' + str(len(records)))
    return 0


if __name__ == '__main__':
    sys.exit(main())
