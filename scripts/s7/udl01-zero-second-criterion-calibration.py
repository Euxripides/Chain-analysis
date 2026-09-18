#!/usr/bin/env python3
r"""udl01-zero-second-criterion-calibration.py — "不得出现 0 秒" 判据的**可审计字符串校准**。

背景（根 2026-09-13 独立复核的真实结论）：v6 spec 第一版用了 /0\s*秒/，
会从 "180 秒" 的最后一位数字开始匹配 "0 秒" —— 这是**测试假阳性**，不是产品仍在显示 0 秒。
本脚本在**任何浏览器启动之前**证明修正后的判据（数字边界）行为正确：
  * 必须拒绝：0 秒 / 超过 0 秒 / 在 0 秒内没有完成 （真正的产品缺陷形态）
  * 不得误判：10 秒 / 100 秒 / 180 秒 / 300 毫秒（含 "0" 但不是独立的 0 秒 token）
  * 必须保持的正向断言：300 毫秒 命中 /300\s*毫秒/；180 秒 命中 /180\s*秒/
用法：udl01-zero-second-criterion-calibration.py [输出 json 路径]
退出码：0 全部符合预期；1 有不符合；2 自身错误。
"""
import json
import re
import sys

# 与 frontend/tests/udl01-worker-isolation-v6.spec.ts 及
# scripts/s7/udl01-crosscheck-v6.py 中生效的判据**逐字符一致**。
NO_ZERO = re.compile(r"(?:^|[^0-9])0\s*秒")
SHOWS_300MS = re.compile(r"300\s*毫秒")
SHOWS_180S = re.compile(r"180\s*秒")

CASES = [
    ("在 0 秒内没有完成，解析 Worker 已被终止。", True, "真实的 0 秒缺陷文案必须被判据命中（拒绝）"),
    ("超过 0 秒会自动中止。", True, "超过 0 秒必须被判据命中（拒绝）"),
    ("0 秒", True, "裸 0 秒（文本开头）必须被判据命中"),
    ("在 10 秒内没有完成，解析 Worker 已被终止。", False, "10 秒 不得误判"),
    ("超过 100 秒会自动中止。", False, "100 秒 不得误判"),
    ("超过 180 秒会自动中止，也可以立即取消。", False, "180 秒 不得误判"),
    ("udl01-big.xlsx 在 300 毫秒内没有完成，解析 Worker 已被终止。", False, "300 毫秒 不得误判"),
]


def main():
    rows = []
    problems = []
    for text, expect_hit, why in CASES:
        hit = NO_ZERO.search(text) is not None
        ok = hit == expect_hit
        rows.append({"text": text, "expect_hit": expect_hit, "hit": hit, "ok": ok, "why": why})
        if not ok:
            problems.append("判据不符合预期：%r expect_hit=%s hit=%s" % (text, expect_hit, hit))
    pos1 = SHOWS_300MS.search("udl01-big.xlsx 在 300 毫秒内没有完成，解析 Worker 已被终止。") is not None
    pos2 = SHOWS_180S.search("超过 180 秒会自动中止，也可以立即取消。") is not None
    if not pos1:
        problems.append(r"正向断言丢失：300 毫秒 必须命中 /300\s*毫秒/")
    if not pos2:
        problems.append(r"正向断言丢失：180 秒 必须命中 /180\s*秒/")
    out = {"criterion": "no standalone 0-second token", "regex": "(?:^|[^0-9])0\\s*秒",
           "rows": rows, "positive_300ms": pos1, "positive_180s": pos2,
           "ok": not problems, "problems": problems}
    text = json.dumps(out, ensure_ascii=False, indent=2)
    print(text)
    if len(sys.argv) > 1:
        with open(sys.argv[1], "w", encoding="utf-8") as fh:
            fh.write(text + "\n")
    return 0 if not problems else 1


if __name__ == "__main__":
    try:
        sys.exit(main())
    except Exception as exc:  # noqa: BLE001
        print(json.dumps({"ok": False, "error": repr(exc)}, ensure_ascii=False))
        sys.exit(2)
