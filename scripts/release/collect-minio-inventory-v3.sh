#!/usr/bin/env bash
# collect-minio-inventory-v3.sh — MinIO 真实清单采集器（v2 的字段名修正版）。
# v2（collect-minio-inventory-v2.sh）真实缺陷：mc stat --json 的字段是 status/name/etag/metadata（**没有 key**），
#   而版本列表的版本号字段是 versionId（不是 versionID）；v2 用 s.get("key") 判 stat_success、用 versionID 计版本号
#   → stat_success 恒为 0、versions_with_versionid 恒为 0 的**错误计数**（数据本身正常）。v2 脚本与输出原样保留。
#
# v1（verify-secret-rotation-v2.sh sha256 66396a3ff0d56c906d983792906c14d08b2a196425bc13227f4dbd01e0038ad4）真实缺陷：
#   A) 用 grep -c '"etag"' 统计所谓 stat records，把**版本列表行**误当成 stat 记录；
#   B) key 提取用 sed 's/.*"key":"\([^"]*\)".*/\1/p'，而 mc 的 JSON 实际是 "key": "..."（冒号后有空格），
#      正则不匹配 → 实际提取到 0 个 key → user metadata 段记录数 0，
#      却被写成"170,675 条含 ETag+user metadata"。经根监控独立解析（verify-minio-metadata-coverage.py）确认：
#      json_records=170675 全部位于 metadata 段之前，metadata_section_records=0，records_with_metadata_field=0。
#      → 该声明**撤回**，见 ERRATUM-minio-metadata-coverage-20260913.md。v1 脚本与输出原样保留、不热改。
#
# v2 做法：
#   * key 一律由 python3 解析 JSON（不是 sed），并显式统计 expected/accounted；
#   * 版本列表（full）与 stat/user-metadata（**显式 sample**）分开采集与计数；
#   * 每桶记录 expected_versions / accounted_versions / stat_attempted / stat_success / stat_error /
#     records_with_etag / records_with_metadata；
#   * **metadata 记录数为 0 时判定 metadata 未验证**（不得当作已验）。
set -uo pipefail
PROJECT="${PROJECT:-/home/euripides/projects/blockchain-platform}"
EV="${EV:?EV required}"
ENVF="${ENVF:-$PROJECT/deploy/.env}"
SAMPLE="${SAMPLE:-50}"
BUCKETS="${BUCKETS:-blockchain-audit blockchain-backups blockchain-exports blockchain-raw blockchain-reports blockchain-reports-drill-20260912t175029z}"
umask 077
mkdir -p "$EV"; chmod 700 "$EV"
valf() { awk -v k="$2" -F= 'index($0, k"=")==1 {print substr($0, length(k)+2); exit}' "$1"; }
MC_USER="$(valf "$ENVF" MINIO_ROOT_USER)"
MC_PW="$(valf "$ENVF" MINIO_ROOT_PASSWORD)"
mc_env() { printf 'export MC_HOST_local=http://%s:%s@127.0.0.1:9000\n' "$MC_USER" "$MC_PW"; }
mc_stream() { { mc_env; cat; } | docker exec -i deploy-minio-1 sh 2>&1; }
OUT="$EV/minio-inventory-v3"
mkdir -p "$OUT"; chmod 700 "$OUT"
echo "collector=collect-minio-inventory-v2 sample_per_bucket=$SAMPLE"
echo "started_utc=$(date -u +%Y-%m-%dT%H:%M:%SZ)"
for b in $BUCKETS; do
  LIST="$OUT/versions-$b.jsonl"; CUR="$OUT/objects-$b.jsonl"
  mc_stream <<EOF > "$LIST"
mc ls --recursive --versions --json local/$b
EOF
  mc_stream <<EOF > "$CUR"
mc ls --recursive --json local/$b
EOF
  mc_stream <<EOF > "$OUT/ilm-$b.txt"
mc ilm rule export local/$b
EOF
  head -n "$SAMPLE" "$CUR" > "$OUT/sample-$b.jsonl"
  python3 - "$OUT/sample-$b.jsonl" "$OUT/statkeys-$b.txt" <<'PY'
import json,sys
keys=[]
for line in open(sys.argv[1]):
    line=line.strip()
    if not line: continue
    try: d=json.loads(line)
    except Exception: continue
    k=d.get("key")
    if k: keys.append(k)
open(sys.argv[2],"w").write("".join(k+"\n" for k in keys))
PY
  STAT="$OUT/stat-$b.jsonl"
  if [ -s "$OUT/statkeys-$b.txt" ]; then
    { mc_env; while IFS= read -r k; do printf 'mc stat --json %s\n' "\"local/$b/$k\""; done < "$OUT/statkeys-$b.txt"; } | docker exec -i deploy-minio-1 sh > "$STAT" 2>&1
  else
    : > "$STAT"
  fi
  python3 - "$b" "$LIST" "$CUR" "$OUT/statkeys-$b.txt" "$STAT" "$OUT/perbucket-$b.json" <<'PY'
import json,sys
b,listf,curf,keyf,statf,outf=sys.argv[1:7]
def jl(p):
    r=[]
    for line in open(p):
        line=line.strip()
        if not line: continue
        try: r.append(json.loads(line))
        except Exception: r.append({"__parse_error__":line[:200]})
    return r
vers=jl(listf); cur=jl(curf); stats=jl(statf)
keys=[l.strip() for l in open(keyf) if l.strip()]
vkeys=[v.get("key") for v in vers if isinstance(v,dict) and v.get("key")]
doc={
 "bucket":b,
 "expected_versions":len(vers),
 "accounted_versions":len(vkeys),
 "versions_with_versionid":sum(1 for v in vers if isinstance(v,dict) and (v.get("versionId") or v.get("versionID"))),
 "versions_with_etag":sum(1 for v in vers if isinstance(v,dict) and v.get("etag")),
 "expected_current_objects":len(cur),
 "sample_scope":"first %d of %d current objects"%(len(keys),len(cur)),
 "stat_attempted":len(keys),
 "stat_success":sum(1 for s in stats if isinstance(s,dict) and not s.get("__parse_error__") and s.get("status")=="success" and s.get("name")),
 "stat_error":sum(1 for s in stats if not isinstance(s,dict) or s.get("__parse_error__")),
 "records_with_etag":sum(1 for s in stats if isinstance(s,dict) and s.get("etag")),
 "records_with_metadata":sum(1 for s in stats if isinstance(s,dict) and isinstance(s.get("metadata"),dict) and len(s.get("metadata") or {})>0),
 "metadata_keys_sample":sorted({k for s in stats if isinstance(s,dict) and isinstance(s.get("metadata"),dict) for k in (s.get("metadata") or {}).keys()})[:20],
 "metadata_field_present_empty":sum(1 for s in stats if isinstance(s,dict) and isinstance(s.get("metadata"),dict) and len(s.get("metadata") or {})==0),
}
doc["ok"]=bool(doc["stat_attempted"]>0 and doc["stat_success"]==doc["stat_attempted"] and doc["accounted_versions"]==doc["expected_versions"])
doc["metadata_verified"]=bool(doc["records_with_metadata"]>0)
json.dump(doc,open(outf,"w"),indent=2)
print(json.dumps({k:doc[k] for k in ("bucket","expected_versions","accounted_versions","versions_with_versionid","versions_with_etag","stat_attempted","stat_success","stat_error","records_with_etag","records_with_metadata","metadata_verified","ok")}))
PY
done
python3 - "$OUT" "$BUCKETS" <<'PY'
import json,sys,os
out,buckets=sys.argv[1],sys.argv[2].split()
agg={"per_bucket":[],"totals":{}}
t={}
for b in buckets:
    p=os.path.join(out,"perbucket-%s.json"%b)
    if not os.path.exists(p): agg["per_bucket"].append({"bucket":b,"missing":True}); continue
    d=json.load(open(p)); agg["per_bucket"].append(d)
    for k,v in d.items():
        if isinstance(v,int): t[k]=t.get(k,0)+v
agg["totals"]=t
agg["metadata_verified_any"]=t.get("records_with_metadata",0)>0
agg["verdict"]=("METADATA_VERIFIED_SAMPLE" if agg["metadata_verified_any"] else "METADATA_NOT_VERIFIED")
json.dump(agg,open(os.path.join(out,"SUMMARY.json"),"w"),indent=2)
print(json.dumps({"totals":t,"verdict":agg["verdict"]}))
PY
cp "$OUT/SUMMARY.json" "$EV/minio-inventory-v2-summary.json"
echo "collector_done_utc=$(date -u +%Y-%m-%dT%H:%M:%SZ)"