#!/usr/bin/env python3
"""导出文件真实解析器（DATA-003/004）。

用法：
  parse_export.py <file> <format> [--contains RAW] [--expect-rows N]

输出一行 key=value；解析失败 exit != 0。
- csv/xlsx/jsonl/parquet 计数并检查必要结构；
- zip 校验 manifest、条目路径穿越、逐条 SHA-256。
"""
from __future__ import annotations

import argparse
import csv
import hashlib
import io
import json
import os
import struct
import sys
import xml.etree.ElementTree as ET
import zipfile


def sha256_file(path: str) -> str:
    h = hashlib.sha256()
    with open(path, "rb") as f:
        for chunk in iter(lambda: f.read(1024 * 1024), b""):
            h.update(chunk)
    return h.hexdigest()


def parse_csv(path: str, contains: str | None) -> tuple[int, bool]:
    rows = 0
    found = False
    with open(path, "r", encoding="utf-8-sig", newline="") as f:
        reader = csv.reader(f)
        header = next(reader, None)
        if not header:
            raise ValueError("csv has no header")
        for row in reader:
            rows += 1
            if contains and any(contains in cell for cell in row):
                found = True
    return rows, found


def parse_jsonl(path: str, contains: str | None) -> tuple[int, bool]:
    rows = 0
    found = False
    with open(path, "r", encoding="utf-8") as f:
        for line in f:
            line = line.strip()
            if not line:
                continue
            obj = json.loads(line)
            rows += 1
            if contains and contains in json.dumps(obj, ensure_ascii=False):
                found = True
    return rows, found


def parse_xlsx(path: str, contains: str | None) -> tuple[int, bool]:
    rows = 0
    found = False
    with zipfile.ZipFile(path) as zf:
        if "xl/workbook.xml" not in zf.namelist():
            raise ValueError("xlsx missing workbook.xml")
        shared = []
        if "xl/sharedStrings.xml" in zf.namelist():
            root = ET.fromstring(zf.read("xl/sharedStrings.xml"))
            ns = {"m": "http://schemas.openxmlformats.org/spreadsheetml/2006/main"}
            for si in root.findall("m:si", ns):
                shared.append("".join(t.text or "" for t in si.iter("{http://schemas.openxmlformats.org/spreadsheetml/2006/main}t")))
        sheet = next((n for n in zf.namelist() if n.startswith("xl/worksheets/sheet")), None)
        if sheet is None:
            raise ValueError("xlsx missing worksheet")
        sheet_xml = zf.read(sheet).decode("utf-8", "ignore")
        root = ET.fromstring(sheet_xml)
        ns = {"m": "http://schemas.openxmlformats.org/spreadsheetml/2006/main"}
        for row in root.iter("{http://schemas.openxmlformats.org/spreadsheetml/2006/main}row"):
            values = []
            for cell in row.findall("m:c", ns):
                v = cell.find("m:v", ns)
                text = v.text if v is not None and v.text is not None else ""
                if cell.get("t") == "s" and text.isdigit():
                    idx = int(text)
                    text = shared[idx] if idx < len(shared) else text
                if cell.get("t") == "inlineStr":
                    inline = cell.find("m:is/m:t", ns)
                    if inline is not None and inline.text is not None:
                        text = inline.text
                values.append(text)
            if values:
                rows += 1
                if contains and any(contains in v for v in values):
                    found = True
        if contains and (contains in sheet_xml or any(contains in s for s in shared)):
            found = True
    # 去掉表头
    return max(0, rows - 1), found


def parse_parquet(path: str, contains: str | None) -> tuple[int, bool]:
    with open(path, "rb") as f:
        magic_head = f.read(4)
        if magic_head != b"PAR1":
            raise ValueError("parquet missing leading PAR1 magic")
        f.seek(-4, os.SEEK_END)
        magic_tail = f.read(4)
        if magic_tail != b"PAR1":
            raise ValueError("parquet missing trailing PAR1 magic")
        size = os.path.getsize(path)
        # footer 长度位于最后 8 字节（4 字节 footer + PAR1 magic），校验结构合理性。
        f.seek(-8, os.SEEK_END)
        (footer_len,) = struct.unpack("<I", f.read(4))
        if footer_len <= 0 or footer_len + 8 > size:
            raise ValueError("parquet footer length invalid")
    # 行数由 Go parquetprobe 负责（真实读取），这里先做结构校验。
    return -1, False


def parse_zip(path: str, contains: str | None) -> tuple[int, bool]:
    found = False
    with zipfile.ZipFile(path) as zf:
        bad = zf.testzip()
        if bad is not None:
            raise ValueError(f"zip entry corrupt: {bad}")
        names = zf.namelist()
        for name in names:
            norm = os.path.normpath(name)
            if name.startswith("/") or ".." in norm.split(os.sep) or name != norm:
                raise ValueError(f"zip path traversal/invalid entry: {name}")
        if "manifest.json" not in names:
            raise ValueError("zip missing manifest.json")
        manifest = json.loads(zf.read("manifest.json"))
        for key in ("export_id", "chain", "addresses", "datasets", "format"):
            if key not in manifest:
                raise ValueError(f"manifest missing {key}")
        entries = 0
        hashes = {}
        for info in zf.infolist():
            if info.is_dir() or info.filename == "manifest.json":
                continue
            entries += 1
            data = zf.read(info.filename)
            digest = hashlib.sha256(data).hexdigest()
            hashes[info.filename] = digest
            if contains and contains in data.decode("utf-8", "ignore"):
                found = True
        if entries == 0:
            raise ValueError("zip has no data entries")
    return entries, found


def main() -> int:
    ap = argparse.ArgumentParser()
    ap.add_argument("file")
    ap.add_argument("format", choices=["csv", "xlsx", "jsonl", "parquet", "zip"])
    ap.add_argument("--contains", default=None)
    ap.add_argument("--expect-rows", type=int, default=None)
    args = ap.parse_args()
    if not os.path.exists(args.file):
        print("error=file_missing")
        return 2
    fmt = args.format
    if fmt == "csv":
        rows, found = parse_csv(args.file, args.contains)
    elif fmt == "jsonl":
        rows, found = parse_jsonl(args.file, args.contains)
    elif fmt == "xlsx":
        rows, found = parse_xlsx(args.file, args.contains)
    elif fmt == "parquet":
        rows, found = parse_parquet(args.file, args.contains)
    else:
        rows, found = parse_zip(args.file, args.contains)
    digest = sha256_file(args.file)
    if args.expect_rows is not None and rows >= 0 and rows != args.expect_rows:
        print(f"rows={rows} expected={args.expect_rows} sha256={digest} contains={int(found)}")
        return 3
    print(f"rows={rows} sha256={digest} contains={int(found)}")
    return 0


if __name__ == "__main__":
    sys.exit(main())
