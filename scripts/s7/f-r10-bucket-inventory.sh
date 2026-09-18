#!/usr/bin/env bash
# f-r10-bucket-inventory.sh — F-R10-BUCKET 现状 inventory（只读：不做任何 copy/remove/update）。
#
# 产出（独立证据目录，保留不删）：
#   pg-export-objects.tsv        exports 表 object_ref 非空行（object_ref|sha256|status|org|ws|size_bytes）
#   minio-raw-exports.jsonl      blockchain-raw 桶 exports/ 前缀对象清单（mc ls --json）
#   minio-export-bucket.jsonl    blockchain-exports 桶全量对象清单
#   reconcile.json               两侧对账（覆盖/缺失/孤儿/大小不一致）
set -uo pipefail
R="${R:-/home/euripides/projects/blockchain-platform}"
TS="$(date -u +%Y%m%dT%H%M%SZ)"
EV="${EV:-$R/acceptance-evidence/dual-system-v1/sprint7/f-r10-bucket-inventory-$TS}"
if [ -e "$EV" ]; then echo "EV_EXISTS_FAILCLOSED $EV"; exit 9; fi
mkdir -p "$EV"; chmod 700 "$EV"
LOG="$EV/inventory.log"
echo "evidence=$EV" | tee "$LOG"
echo "script_sha256=$(sha256sum "$0" | cut -d" " -f1)" | tee -a "$LOG"
echo "started_utc=$TS" | tee -a "$LOG"
cd "$R" || exit 3
set -a; . "$R/deploy/.env"; set +a
DBNAME=$(printf "%s" "$DATABASE_URL" | sed -E 's#.*/([^/?]+)([?].*)?$#\1#')
echo "db=$DBNAME dsn_sha12=$(printf "%s" "$DATABASE_URL" | sha256sum | cut -c1-12)" | tee -a "$LOG"

echo "=== 1) PG exports 引用清单（SET ROLE blockchain_app，只读） ===" | tee -a "$LOG"
docker exec deploy-postgres-1 psql -q -U blockchain_api -d "$DBNAME" -tAc "SET ROLE blockchain_app; SELECT object_ref||'|'||coalesce(sha256,'')||'|'||coalesce(status,'')||'|'||coalesce(organization_id::text,'')||'|'||coalesce(workspace_id::text,'')||'|'||coalesce(char_length(object_ref),0)::text FROM exports WHERE coalesce(object_ref,'') <> '' ORDER BY object_ref" > "$EV/pg-export-objects.tsv" 2> "$EV/pg.err"
PG_RC=$?
PG_RAW_LINES=$(wc -l < "$EV/pg-export-objects.tsv")
PG_REFS_PARSED=$(grep -c '^exports/' "$EV/pg-export-objects.tsv" || true)
echo "pg_rc=$PG_RC pg_raw_lines=$PG_RAW_LINES pg_refs_parsed=$PG_REFS_PARSED" | tee -a "$LOG"
if [ "$PG_RAW_LINES" != "$PG_REFS_PARSED" ]; then echo "NOTE: 原始行数含 psql 状态标签，权威计数以 pg_refs_parsed/reconcile 为准" | tee -a "$LOG"; fi
docker exec deploy-postgres-1 psql -U blockchain_api -d "$DBNAME" -tAc "SET ROLE blockchain_app; SELECT count(*) FROM exports" > "$EV/pg-exports-total.txt" 2>&1
echo "pg_exports_total=$(cat "$EV/pg-exports-total.txt")" | tee -a "$LOG"
docker exec deploy-postgres-1 psql -q -U blockchain_api -d "$DBNAME" -tAc "SET ROLE blockchain_app; SELECT status, count(*) FROM exports GROUP BY status ORDER BY 1" > "$EV/pg-exports-status.txt" 2>&1
cat "$EV/pg-exports-status.txt" | tee -a "$LOG"
# 根只读补查口径：270 COMPLETED / 270 非空 ref / 66 条 PG sha256 为空 / 270 唯一 ref
docker exec deploy-postgres-1 psql -q -U blockchain_api -d "$DBNAME" -tAc "SET ROLE blockchain_app; SELECT count(*) FROM exports WHERE coalesce(length(object_ref),0) > 0" > "$EV/pg-ref-notnull.txt" 2>&1
docker exec deploy-postgres-1 psql -q -U blockchain_api -d "$DBNAME" -tAc "SET ROLE blockchain_app; SELECT count(DISTINCT object_ref) FROM exports WHERE coalesce(length(object_ref),0) > 0" > "$EV/pg-ref-unique.txt" 2>&1
docker exec deploy-postgres-1 psql -q -U blockchain_api -d "$DBNAME" -tAc "SET ROLE blockchain_app; SELECT count(*) FROM exports WHERE coalesce(length(object_ref),0) > 0 AND coalesce(length(sha256),0) = 0" > "$EV/pg-sha-null.txt" 2>&1
docker exec deploy-postgres-1 psql -q -U blockchain_api -d "$DBNAME" -tAc "SET ROLE blockchain_app; SELECT count(*) FROM exports WHERE coalesce(length(object_ref),0) > 0 AND coalesce(length(sha256),0) > 0" > "$EV/pg-sha-present.txt" 2>&1
echo "pg_ref_notnull=$(cat "$EV/pg-ref-notnull.txt") pg_ref_unique=$(cat "$EV/pg-ref-unique.txt") pg_sha_null=$(cat "$EV/pg-sha-null.txt") pg_sha_present=$(cat "$EV/pg-sha-present.txt")" | tee -a "$LOG"

echo "=== 2) MinIO 清单 ===" | tee -a "$LOG"
# mc ls 带前缀时 key 是**相对前缀**的 -> 必须列整桶再筛 exports/，否则会全部误判为孤儿
docker exec deploy-minio-1 sh -c 'mc alias set loc http://127.0.0.1:9000 "$MINIO_ROOT_USER" "$MINIO_ROOT_PASSWORD" >/dev/null 2>&1; mc ls --recursive --json loc/blockchain-raw/' > "$EV/minio-raw-bucket-full.jsonl" 2> "$EV/minio-raw.err"
grep -c '"key":"exports/' "$EV/minio-raw-bucket-full.jsonl" > "$EV/minio-raw-exports-count.txt" 2>/dev/null || true
grep '"key":"exports/' "$EV/minio-raw-bucket-full.jsonl" > "$EV/minio-raw-exports.jsonl" || true
echo "raw_listing_rc=$? raw_lines=$(wc -l < "$EV/minio-raw-exports.jsonl")" | tee -a "$LOG"
docker exec deploy-minio-1 sh -c 'mc ls --recursive --json loc/blockchain-exports/' > "$EV/minio-export-bucket.jsonl" 2> "$EV/minio-export.err"
echo "export_listing_rc=$? export_lines=$(wc -l < "$EV/minio-export-bucket.jsonl")" | tee -a "$LOG"
docker exec deploy-minio-1 sh -c 'mc du loc/blockchain-raw/exports/; mc du loc/blockchain-raw/; mc du loc/blockchain-exports/; mc ls loc' > "$EV/minio-du.txt" 2>&1
cat "$EV/minio-du.txt" | tee -a "$LOG"

echo "=== 3) 对账 ===" | tee -a "$LOG"
python3 - "$EV" <<'PY' | tee -a "$LOG"
import json, os, sys
ev = sys.argv[1]
refs = []
with open(os.path.join(ev, "pg-export-objects.tsv"), encoding="utf-8") as fh:
    for line in fh:
        parts = line.rstrip("\n").split("|")
        if len(parts) >= 6 and parts[0].startswith("exports/"):
            # psql -c 会把 SET 的命令标签也打出来（它不是数据行），这里按真实 object_ref 形状过滤
            refs.append({"object_ref": parts[0], "sha256": parts[1], "status": parts[2],
                         "org": parts[3], "ws": parts[4], "ref_len": int(parts[5] or 0)})
def load(name):
    out = {}
    path = os.path.join(ev, name)
    if not os.path.exists(path):
        return out
    with open(path, encoding="utf-8") as fh:
        for line in fh:
            line = line.strip()
            if not line:
                continue
            try:
                row = json.loads(line)
            except Exception:
                continue
            if row.get("key"):
                out[row["key"]] = int(row.get("size") or 0)
    return out
raw = load("minio-raw-exports.jsonl")
exp = load("minio-export-bucket.jsonl")
in_raw = [r for r in refs if r["object_ref"] in raw]
in_exp = [r for r in refs if r["object_ref"] in exp]
both = [r for r in refs if r["object_ref"] in raw and r["object_ref"] in exp]
missing = [r for r in refs if r["object_ref"] not in raw and r["object_ref"] not in exp]
in_raw_sha = sorted(r["object_ref"] for r in in_raw)
orphans_raw = sorted(set(raw) - {r["object_ref"] for r in refs})
unique_refs = sorted({r["object_ref"] for r in refs})
with_sha = [r for r in refs if r["sha256"]]
without_sha = [r for r in refs if not r["sha256"]]
out = {"pg_rows_with_object_ref": len(refs), "pg_unique_object_ref": len(unique_refs),
       "pg_refs_with_sha256": len(with_sha), "pg_refs_without_sha256": len(without_sha),
       "minio_raw_exports_objects": len(raw), "minio_raw_exports_bytes": sum(raw.values()),
       "minio_export_bucket_objects": len(exp), "minio_export_bucket_bytes": sum(exp.values()),
       "refs_present_in_raw": len(in_raw), "refs_present_in_export_bucket": len(in_exp),
       "refs_in_both_buckets": len(both), "refs_missing_in_both": len(missing),
       "raw_reference_sample": in_raw_sha[:5],
       "orphan_objects_in_raw_exports": len(orphans_raw), "orphan_sample": orphans_raw[:10],
       "missing_sample": [r["object_ref"] for r in missing[:10]],
       "export_bucket_keys_sample": sorted(exp)[:10]}
with open(os.path.join(ev, "reconcile.json"), "w", encoding="utf-8") as fh:
    json.dump(out, fh, ensure_ascii=False, indent=2)
print(json.dumps(out, ensure_ascii=False, indent=2))
PY
echo "reconcile_rc=$?" | tee -a "$LOG"
echo "F_R10_BUCKET_INVENTORY_DONE evidence=$EV" | tee -a "$LOG"
exit 0
