#!/usr/bin/env bash
# SBOM/VEX 更正轮（P1-20260913）ERRATUM：首轮 go list 从仓库根执行（go.mod 在 backend/）失败，
# 空输出被记成 0，使 vex/go-list-deps-*.txt 成为无效测量，且首份 VEX 引用了『共 0 个包』。
# 本轮做正确测量并重发 VEX（首份保留不删，标记被更正）。只追加。
set -uo pipefail
export PATH=$PATH:/usr/local/go/bin
PROJ=/home/euripides/projects/blockchain-platform
BASE=$PROJ/acceptance-evidence/dual-system-v1/sprint7/security/sbom/p1-iam-workspace-scope-20260913T032734Z
OUT=$BASE-CORRECTION-go-list
if [ -e "$OUT" ]; then echo "FATAL REFUSING TO OVERWRITE: $OUT"; exit 3; fi
mkdir -p "$OUT" || exit 2
exec > >(tee "$OUT/correction-console.log") 2>&1
echo "ts_utc=$(date -u +%Y%m%dT%H%M%SZ) script_sha256=$(sha256sum "$0" | cut -d' ' -f1)"
APIPID=$(systemctl --user show -p MainPID --value blockchain-api)
SHA=$(sha256sum /proc/$APIPID/exe | cut -d' ' -f1)
echo "running_api_pid=$APIPID running_api_sha=$SHA"
echo "=== 1. go list -deps ./cmd/api（工作目录 = 模块根 backend/）==="
cd "$PROJ/backend" || exit 4
go list -deps ./cmd/api > "$OUT/go-list-deps.txt" 2> "$OUT/go-list-deps.err"
GL_RC=$?
TOTAL=$(grep -c . "$OUT/go-list-deps.txt" || true)
OPENPGP=$(grep -c 'x/crypto/openpgp' "$OUT/go-list-deps.txt" || true)
echo "go_list_rc=$GL_RC packages=$TOTAL openpgp_packages=$OPENPGP"
echo "$TOTAL" > "$OUT/go-list-deps-total.txt"
echo "$OPENPGP" > "$OUT/go-list-deps-openpgp-count.txt"
echo "=== 2. nm 有效性（符号表非空才使 0 openpgp 有意义）==="
cd "$PROJ" || exit 4
nm -C backend/bin/api > "$OUT/nm-api.txt" 2> "$OUT/nm-api.err"
NM_RC=$?
NM_TOTAL=$(grep -c . "$OUT/nm-api.txt" || true)
NM_OPENPGP=$(grep -ci openpgp "$OUT/nm-api.txt" || true)
echo "nm_rc=$NM_RC symbols=$NM_TOTAL openpgp_symbols=$NM_OPENPGP"
echo "$NM_TOTAL" > "$OUT/nm-api-symbol-count.txt"
echo "$NM_OPENPGP" > "$OUT/nm-api-openpgp-count.txt"
echo "=== 3. 首轮（无效）vs 本轮（正确）==="
{
  echo "field first_round corrected status"
  echo "go_list_deps_total 0 $TOTAL check_rc=$GL_RC"
  echo "go_list_deps_openpgp 0 $OPENPGP check_rc=$GL_RC"
  echo "nm_symbols n/a $NM_TOTAL check_rc=$NM_RC"
  echo "nm_openpgp_symbols 0 $NM_OPENPGP check_rc=$NM_RC"
} | tee "$OUT/field-comparison.txt"
echo "首轮 vex/go-list-deps-*.txt 由失败的 go list（错误工作目录）产生，0 是空输出的假象；本目录给出正确测量，首份 VEX 的 impact_statement 中『共 0 个包』一句作废。" > "$OUT/ERRATUM.txt"
cdx_serial=$(python3 -c 'import json,sys;print(json.load(open(sys.argv[1])).get("serialNumber"))' "$BASE/api.cdx.json")
cdx_ref=$(python3 -c 'import json,sys;print(json.load(open(sys.argv[1])).get("metadata",{}).get("component",{}).get("bom-ref"))' "$BASE/api.cdx.json")
SIZE=$(stat -c %s backend/bin/api)
cd "$OUT" || exit 4
python3 -c 'import json,sys,os,hashlib,datetime
out,sha,pid,total,openpgp,nmtot,nmop,serial,ref,size,sup=sys.argv[1:12]
now=datetime.datetime.now(datetime.UTC).strftime("%Y-%m-%dT%H:%M:%SZ")
vex={"vex_version":"1.0","generated_at_utc":now,
 "generated_by":"DSH main writer (P1-20260913 IAM workspace-scope fix round)",
 "supersedes":sup,
 "erratum":"首份 VEX 的 go-list 数字来自一次失败的 go list（工作目录错误）；本文件为更正后的重发版，首份保留不删。",
 "scope":"per-artifact, THIS BUILD ONLY (api sha256=%s)"%sha,
 "sbom_reference":{"document":"../api.cdx.json","serial":serial,"artefact_bom_ref":ref},
 "statements":[{"vulnerability":{"name":"GO-2026-5932","source":"https://vuln.go.dev/ID/GO-2026-5932.json","summary":"golang.org/x/crypto/openpgp is unmaintained and unsafe by design"},
  "status":"not_affected","justification":"vulnerable_code_not_in_execute_path",
  "impact_statement":"受影响包仅为 golang.org/x/crypto/openpgp 及其子包。本 artefact（sha256=%s）：nm -C 共 %s 个符号、openpgp 命中 %s 个；go list -deps ./cmd/api（工作目录 backend/，rc=0）共 %s 个包、openpgp 前缀 %s 个。符号表非空且两项计数均为 0。"%(sha,nmtot,nmop,total,openpgp),
  "products":[{"component":"api","bom_ref":ref,"hashes":[{"alg":"SHA-256","content":sha}],"size_bytes":int(size),"running_process":{"pid":int(pid),"exe":"/home/euripides/projects/blockchain-platform/backend/bin/api"}}],
  "evidence":{"proof_dir":"(本目录)","files":["go-list-deps.txt","go-list-deps-total.txt","go-list-deps-openpgp-count.txt","nm-api.txt","nm-api-symbol-count.txt","nm-api-openpgp-count.txt","field-comparison.txt","ERRATUM.txt"],"nm_symbols":int(nmtot),"nm_openpgp_symbols":int(nmop),"go_list_deps_total":int(total),"go_list_deps_openpgp_packages":int(openpgp)},
  "assumptions":["该版本不动态加载该包（无 plugin / 无运行时模块加载）。若引入 plugin，本声明作废。","这是对该 sha 具体产物的影响分类，不是豁免，也不是宣称该公告无害。","不基于『无 CVSS』，也不单基于 symbol scan level 0。"],
  "invalidation_triggers":["api/worker/migrate 任何重新构建（产物 sha 变化）","go.mod / go.sum / 依赖图任何变化","引入 plugin 或动态加载"],"reviewed_at":now}],
 "explicitly_not_claimed":["全项目 P0=0 或 P1=0","release GO / 生产切换","容器镜像 P0/P1 状态（另行处理，仍 OPEN）","任何其它依赖发现、SBOM 缺口或 §27/§31 验收项"],
 "notes":["更正版：修正 go-list 测量缺陷后重发；产物哈希变化即失效。"]}
p=os.path.join(out,"scoped-vex-GO-2026-5932-%s-corrected.json"%sha[:12])
json.dump(vex,open(p,"w",encoding="utf-8"),ensure_ascii=False,indent=1)
print("vex_corrected",p,hashlib.sha256(open(p,"rb").read()).hexdigest())' "$OUT" "$SHA" "$APIPID" "$TOTAL" "$OPENPGP" "$NM_TOTAL" "$NM_OPENPGP" "$cdx_serial" "$cdx_ref" "$SIZE" "$BASE/vex/scoped-vex-GO-2026-5932-$SHA.json"
sha256sum * > correction-hashes.txt 2>/dev/null; chmod 600 * 2>/dev/null; true
echo "SBOM_CORRECTION_DIR=$OUT"
if [ "$GL_RC" -eq 0 ] && [ "$TOTAL" -gt 100 ] && [ "$NM_TOTAL" -gt 1000 ]; then echo "CORRECTION_OK total=$TOTAL openpgp=$OPENPGP nm_symbols=$NM_TOTAL nm_openpgp=$NM_OPENPGP"; else echo "CORRECTION_INCOMPLETE"; exit 6; fi
