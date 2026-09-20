#!/usr/bin/env python3
"""udl01-fixture-selfcheck.py — U-DL-01 夹具机器自检的**轮次内**复核（fail-closed）。

生成器 frontend/scripts/r9-make-fixtures-v5c.mjs 已经做过一次自检并写出 JSON；
本脚本在 runner 里再独立复核一次，防止"生成器自检被跳过/自检文件是旧轮次的"：

  1) 自检 JSON 存在、非空、可解析，ok == true，problems 为空，且记录了生成器路径；
  2) 行数语义（§13.3）：requestedRows > actualFormatCapacity.maxDataRows >= actualDataRowsOnDisk > 0，
     即"请求 200k 行、该格式只能落 65535 以内"这一事实被机器记录，而不是靠人工记忆；
  3) ZIP64 布局（§14.3）：EOCD 存在、locator 位于 EOCD 前 20 字节且 signature=0x07064b50、
     ZIP64 EOCD signature=0x06064b50、locator 声明的偏移指向 ZIP64 EOCD；
  4) 每个夹具：bytes > 0 且 < 16MiB（导入上限）、sha256 为 64 位十六进制；
  5) manifest 与 selfcheck 的夹具 sha256 必须逐项一致（同一批字节）。

用法：udl01-fixture-selfcheck.py <selfcheck.json> [manifest.json]
退出码：0 全通过；1 有失败；2 自身错误。
"""
import json
import os
import re
import sys

IMPORT_MAX_FILE_BYTES = 16 * 1024 * 1024
SHA_RE = re.compile(r"^[0-9a-f]{64}$")


def load(path):
    if not os.path.exists(path) or os.path.getsize(path) == 0:
        raise ValueError("文件缺失或为空：" + path)
    with open(path, "r", encoding="utf-8") as fh:
        return json.load(fh)


def main():
    if len(sys.argv) < 2:
        print(json.dumps({"ok": False, "error": "usage: udl01-fixture-selfcheck.py <selfcheck.json> [manifest.json]"}, ensure_ascii=False))
        return 2
    problems = []
    selfcheck = load(sys.argv[1])
    if selfcheck.get("ok") is not True:
        problems.append("selfcheck.ok != true：%r" % (selfcheck.get("ok"),))
    if selfcheck.get("problems"):
        problems.append("selfcheck.problems 非空：%r" % (selfcheck.get("problems"),))
    if "r9-make-fixtures-v5c.mjs" not in str(selfcheck.get("generator")):
        problems.append("selfcheck.generator 不是权威生成器：%r" % (selfcheck.get("generator"),))

    rows = selfcheck.get("rowSemantics") or {}
    requested = int(rows.get("requestedRows") or 0)
    capacity = int((rows.get("actualFormatCapacity") or {}).get("maxDataRows") or 0)
    on_disk = int(rows.get("actualDataRowsOnDisk") or 0)
    if not (requested > capacity >= on_disk > 0):
        problems.append("行数语义不自洽：requested=%r capacity=%r on_disk=%r" % (requested, capacity, on_disk))
    if capacity != 65535:
        problems.append("BIFF8 数据行上限应为 65535，实际 %r" % (capacity,))
    if not rows.get("withinFormatCapacity"):
        problems.append("actualDataRowsOnDisk 超出该格式容量")

    z = selfcheck.get("zip64Layout") or {}
    for key, want in (("eocd_present", True), ("locator_is_20_bytes_before_eocd", True),
                      ("locator_signature_ok", True), ("zip64_eocd_signature_ok", True),
                      ("locator_points_at_zip64_eocd", True)):
        if z.get(key) is not want:
            problems.append("ZIP64 布局判据 %s=%r != %r" % (key, z.get(key), want))
    if z.get("locator_signature") != "0x07064b50":
        problems.append("locator signature=%r" % (z.get("locator_signature"),))
    if z.get("zip64_eocd_signature") != "0x06064b50":
        problems.append("ZIP64 EOCD signature=%r" % (z.get("zip64_eocd_signature"),))
    if z.get("layout") != "ZIP64 EOCD -> ZIP64 locator -> classic EOCD":
        problems.append("ZIP64 布局顺序=%r" % (z.get("layout"),))

    fixtures = selfcheck.get("fixtures") or []
    if len(fixtures) < 9:
        problems.append("夹具数量 %d < 9" % len(fixtures))
    for row in fixtures:
        size = int(row.get("bytes") or 0)
        if not (0 < size < IMPORT_MAX_FILE_BYTES):
            problems.append("夹具大小越界：%s -> %r" % (row.get("name"), size))
        if not SHA_RE.match(str(row.get("sha256") or "")):
            problems.append("夹具 sha256 非法：%s" % (row.get("name"),))

    manifest_path = sys.argv[2] if len(sys.argv) > 2 else None
    if manifest_path:
        manifest = load(manifest_path)
        by_name = {f.get("name"): f.get("sha256") for f in (manifest.get("fixtures") or [])}
        for row in fixtures:
            name = row.get("name")
            if by_name.get(name) != row.get("sha256"):
                problems.append("manifest 与 selfcheck 的 sha256 不一致：%s" % (name,))
        if int((manifest.get("requestedRows") or {}).get("xls") or 0) != requested:
            problems.append("manifest.requestedRows.xls 与 selfcheck 不一致")
        if int(manifest.get("actualDataRowsOnDisk") or 0) != on_disk:
            problems.append("manifest.actualDataRowsOnDisk 与 selfcheck 不一致")
        if not manifest.get("actualFormatCapacity"):
            problems.append("manifest 缺少 actualFormatCapacity（§13.3 第 4 条）")

    out = {
        "ok": not problems,
        "problems": problems,
        "generator": selfcheck.get("generator"),
        "requestedRows": requested,
        "actualFormatCapacity": rows.get("actualFormatCapacity"),
        "actualDataRowsOnDisk": on_disk,
        "zip64Layout": z,
        "fixtures": [{"name": r.get("name"), "bytes": r.get("bytes"), "sha256": r.get("sha256")} for r in fixtures],
    }
    print(json.dumps(out, ensure_ascii=False, indent=2))
    return 0 if not problems else 1


if __name__ == "__main__":
    try:
        sys.exit(main())
    except Exception as exc:  # noqa: BLE001
        print(json.dumps({"ok": False, "unexpected_error": repr(exc)}, ensure_ascii=False))
        sys.exit(2)
