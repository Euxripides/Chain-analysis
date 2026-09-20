#!/usr/bin/env python3
"""udl01-u5-gate-v6.py — 证明 U5 新增判据**真的在 crosscheck 里执行且通过**（不是只改了注释）。

判据（任一不满足即 rc=1）：
  1) summary 必须存在、非空、可解析，verdict == PASS，checks_failed == 0；
  2) 下列 U5 判据必须**存在**且 ok == true；
  3) checks_total >= 50（v5b 的 42 + 新增 8，证明新判据确实被计入而不是被静默跳过）。
用法：udl01-u5-gate-v6.py <udl01-summary.json>
退出码：0 通过；1 判据不满足；2 核验自身错误。
"""
import json
import os
import sys

REQUIRED = [
    "b-budget-no-zero-seconds",
    "b-budget-shows-milliseconds",
    # 180000ms → “180 秒”的判据（§17.3）：由服务端实际服务的 page chunk 里同一个
    # formatParseBudget 求值验证，且探针 SHA 必须绑定到 served page chunk。
    "c-progress-shows-file",
    "c-budget-shows-300ms",
    "c-budget-shows-180s",
    "c-budget-no-zero-seconds",
    "c-budget-probe-bound-to-served-chunk",
    "bundle-page-chunk-200",
    "bundle-page-chunk-bytes-match",
    "bundle-page-chunk-has-budget-unit",
    "bundle-page-chunk-has-timeout-copy",
]
MIN_TOTAL = 50


def main():
    path = sys.argv[1]
    if not os.path.exists(path) or os.path.getsize(path) == 0:
        print(json.dumps({"ok": False, "reason": "summary missing/empty", "path": path}, ensure_ascii=False))
        return 1
    with open(path, "r", encoding="utf-8") as fh:
        d = json.load(fh)
    by_name = {c.get("check"): c for c in (d.get("checks") or [])}
    problems = []
    for name in REQUIRED:
        row = by_name.get(name)
        if row is None:
            problems.append("缺少 U5 判据：%s" % name)
        elif not row.get("ok"):
            problems.append("U5 判据未通过：%s -> %s" % (name, json.dumps(row.get("detail"), ensure_ascii=False)[:200]))
    if d.get("verdict") != "PASS":
        problems.append("verdict=%r" % d.get("verdict"))
    if int(d.get("checks_failed") or 0) != 0:
        problems.append("checks_failed=%r" % d.get("checks_failed"))
    if int(d.get("checks_total") or 0) < MIN_TOTAL:
        problems.append("checks_total=%r < %d（新判据可能未执行）" % (d.get("checks_total"), MIN_TOTAL))
    out = {"ok": not problems, "checks_total": d.get("checks_total"),
           "required_present": [n for n in REQUIRED if n in by_name], "problems": problems}
    print(json.dumps(out, ensure_ascii=False, indent=2))
    return 0 if not problems else 1


if __name__ == "__main__":
    try:
        sys.exit(main())
    except Exception as exc:  # noqa: BLE001
        print(json.dumps({"ok": False, "error": repr(exc)}, ensure_ascii=False))
        sys.exit(2)
