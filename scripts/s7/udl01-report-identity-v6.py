#!/usr/bin/env python3
"""udl01-report-identity-v6.py — 证明"实际执行的 spec 就是 v6 spec"（机器判据，不看命令行字符串）。

背景（根 2026-09-13 独立审查发现的真实 fail-closed 缺陷）：
  v6 runner 的 P0b0 只 grep 配置文件内容，但真正传给 Playwright 的 --config 仍指向 v5c，
  于是跑的是 v5b spec —— 新增的 U5 断言根本没执行，却可能打印 PASS。
判据（任一不满足即 rc=1）：
  1) report 必须存在、非空、可解析；
  2) 递归遍历所有 suite/spec，每个节点记录的 file 必须**恰好**是本轮唯一 spec 文件名；
  3) 必须恰好 5 个 spec，标题前缀恰为 UDL01-A..E，每个 spec 恰 1 个 test；
  4) stats: expected==5, unexpected==0, skipped==0, flaky==0。
用法：udl01-report-identity-v6.py <playwright-report.json> [expected_spec_basename]
退出码：0 通过；1 判据不满足；2 核验自身错误。
"""
import json
import os
import sys

EXPECTED_TITLES = ["UDL01-A", "UDL01-B", "UDL01-C", "UDL01-D", "UDL01-E"]


def main():
    path = sys.argv[1]
    expect_spec = sys.argv[2] if len(sys.argv) > 2 else "udl01-worker-isolation-v6.spec.ts"
    if not os.path.exists(path) or os.path.getsize(path) == 0:
        print(json.dumps({"ok": False, "reason": "report missing/empty", "path": path}, ensure_ascii=False))
        return 1
    with open(path, "r", encoding="utf-8") as fh:
        rep = json.load(fh)
    files = set()
    specs = []
    problems = []

    def walk(node):
        f = node.get("file")
        if f:
            files.add(os.path.basename(str(f)))
        for sp in node.get("specs") or []:
            sf = sp.get("file") or f
            if sf:
                files.add(os.path.basename(str(sf)))
            specs.append({"title": sp.get("title") or "", "file": os.path.basename(str(sf or "")), "tests": len(sp.get("tests") or [])})
        for child in node.get("suites") or []:
            walk(child)

    for s in rep.get("suites") or []:
        walk(s)

    if files != {expect_spec}:
        problems.append("report 中的 spec 文件集合不是唯一预期的 %s：%s" % (expect_spec, sorted(files)))
    if len(specs) != 5:
        problems.append("spec 数量 %d != 5" % len(specs))
    keys = sorted([t for t in EXPECTED_TITLES if any(sp["title"].startswith(t) for sp in specs)])
    if keys != EXPECTED_TITLES:
        problems.append("标题前缀集合不符：%s" % keys)
    for sp in specs:
        if sp["tests"] != 1:
            problems.append("spec %r 的 test 数 %d != 1" % (sp["title"][:40], sp["tests"]))
        if sp["file"] != expect_spec:
            problems.append("spec %r 记录的文件 %r != %s" % (sp["title"][:40], sp["file"], expect_spec))
    stats = rep.get("stats") or {}
    if not (stats.get("expected") == 5 and stats.get("unexpected") == 0 and stats.get("skipped") == 0 and stats.get("flaky") == 0):
        problems.append("stats 不符：%s" % stats)

    out = {"ok": not problems, "expected_spec": expect_spec, "files": sorted(files),
           "spec_titles": [sp["title"] for sp in specs], "stats": stats, "problems": problems}
    print(json.dumps(out, ensure_ascii=False, indent=2))
    return 0 if not problems else 1


if __name__ == "__main__":
    try:
        sys.exit(main())
    except Exception as exc:  # noqa: BLE001
        print(json.dumps({"ok": False, "error": repr(exc)}, ensure_ascii=False))
        sys.exit(2)
