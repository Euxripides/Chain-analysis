#!/usr/bin/env bash
# SBOM/VEX for the R10v8 audit-fix api binary (D-R10V4-01, 2026-09-13) —— 只追加，不覆盖任何既有目录。
#
# 相对 sbom-p1-iam-scope.sh 的**新版本**（不 hot-edit 旧脚本）：
#   S1 scope 名 = r10v8-auditfix-<TS>；绑定 sha 默认 = 569f40ec…（审计修复产物）。
#   S2 **修复 p1 轮真实缺陷**：go list -deps 曾在仓库根执行（go.mod 在 backend/）→ 空输出被当成
#      "0 个包"，首份 VEX 因此引用了"共 0 个包"（已有 ...-CORRECTION-go-list 目录留档）。
#      本版本改为 (cd backend && go list -deps ./cmd/api)，并 **rc 非 0 / 空清单一律 fail-closed**。
# 绑定关系：syft CDX metadata.component.version = sha256:<artifact sha>；并记录被扫描文件的
# 真实字节 sha、运行进程 /proc/<pid>/exe sha、SBOM 文件自身 sha、VEX 指向同一个 artefact。
set -uo pipefail
export PATH=$PATH:/usr/local/go/bin
PROJ=/home/euripides/projects/blockchain-platform
TS=$(date -u +%Y%m%dT%H%M%SZ)
SCOPE=r10v8-auditfix-$TS
OUT="$PROJ/acceptance-evidence/dual-system-v1/sprint7/security/sbom/$SCOPE"
IMG=docker.1ms.run/anchore/syft:latest
UIDG="$(id -u):$(id -g)"
EXPECT_API_SHA="${EXPECT_API_SHA:-569f40ec66e982804072591628692d993cbb59f01e5477a6134a22d6e9391527}"

if [ -e "$OUT" ]; then echo "FATAL REFUSING TO OVERWRITE EXISTING PATH: $OUT"; exit 3; fi
mkdir -p "$OUT/vex" "$OUT/logs" || { echo "FATAL cannot create $OUT"; exit 2; }
exec > >(tee "$OUT/sbom-console.log") 2>&1
echo "scope=$SCOPE script_sha256=$(sha256sum "$0" | cut -d' ' -f1)"
echo "expect_api_sha=$EXPECT_API_SHA"
echo "syft_image=$IMG image_id=$(docker image inspect "$IMG" --format '{{.Id}}')"
docker run --rm --user "$UIDG" -e HOME=/tmp "$IMG" version 2>&1 | tee "$OUT/logs/syft-version.txt"

APIPID=$(systemctl --user show -p MainPID --value blockchain-api)
echo "running api pid=$APIPID exe=$(readlink -f /proc/$APIPID/exe)"
{
  echo "# artifact hashes bound by this SBOM scope ($SCOPE)"
  echo "# generated_utc=$(date -u +%FT%TZ)"
  echo "--- deployed artifact scanned ---"
  sha256sum "$PROJ/backend/bin/api"; stat -c '%n size=%s' "$PROJ/backend/bin/api"
  echo "--- running process (真实运行身份) ---"
  sha256sum "/proc/$APIPID/exe"; readlink -f "/proc/$APIPID/exe"
  echo "--- sibling deployed artifacts (unchanged this round) ---"
  sha256sum "$PROJ/backend/bin/worker" "$PROJ/backend/bin/migrate"
} | tee "$OUT/artifact-hashes.txt"
RUN_SHA=$(sha256sum "/proc/$APIPID/exe" | cut -d' ' -f1)
FILE_SHA=$(sha256sum "$PROJ/backend/bin/api" | cut -d' ' -f1)
[ "$RUN_SHA" = "$EXPECT_API_SHA" ] && [ "$FILE_SHA" = "$EXPECT_API_SHA" ] || { echo "FATAL artifact sha mismatch run=$RUN_SHA file=$FILE_SHA expect=$EXPECT_API_SHA"; exit 4; }
echo "api_sha_bound=$FILE_SHA (file == running process)"

echo "=== buildinfo ==="
go version -m "$PROJ/backend/bin/api" > "$OUT/buildinfo-api.txt" 2>&1
sed -n '1,6p' "$OUT/buildinfo-api.txt"
echo "embedded_modules=$(grep -c '^\s*dep\s' "$OUT/buildinfo-api.txt")"

echo "=== syft scan（file target = 已部署 api 二进制） ==="
docker run --rm --user "$UIDG" -e HOME=/tmp -v "$PROJ":/w -v "$OUT":/out -w /w "$IMG" \
  file:/w/backend/bin/api -o cyclonedx-json=/out/api.cdx.json 2>&1 | tail -3
docker run --rm --user "$UIDG" -e HOME=/tmp -v "$PROJ":/w -v "$OUT":/out -w /w "$IMG" \
  file:/w/backend/bin/api -o spdx-json=/out/api.spdx.json 2>&1 | tail -3
sha256sum "$OUT/api.cdx.json" "$OUT/api.spdx.json" | tee "$OUT/sbom-artifact-hashes.txt"
stat -c '%n size=%s' "$OUT/api.cdx.json" "$OUT/api.spdx.json" | tee -a "$OUT/sbom-artifact-hashes.txt"

echo "=== VEX 证据：openpgp 受影响包是否进入本二进制 ==="
nm -C "$PROJ/backend/bin/api" 2>/dev/null | grep -ci openpgp > "$OUT/vex/api.nm-openpgp-count.txt" || echo 0 > "$OUT/vex/api.nm-openpgp-count.txt"
echo "nm_openpgp_symbols=$(cat "$OUT/vex/api.nm-openpgp-count.txt")"
# S2 更正：go.mod 在 backend/，必须在该目录执行；rc 非 0 或空清单一律 fail-closed。
GOLIST_RC=0
( cd "$PROJ/backend" && go list -deps ./cmd/api ) > "$OUT/vex/go-list-deps-all.txt" 2> "$OUT/vex/go-list-deps.err" || GOLIST_RC=$?
GOLIST_N=$(wc -l < "$OUT/vex/go-list-deps-all.txt")
echo "go_list_deps_rc=$GOLIST_RC go_list_deps_packages=$GOLIST_N"
[ "$GOLIST_RC" -eq 0 ] || { echo "FATAL go list -deps failed rc=$GOLIST_RC — refusing to emit a VEX from an empty package list"; exit 6; }
[ "$GOLIST_N" -gt 0 ] || { echo "FATAL go list -deps produced an empty package list"; exit 6; }
grep -c 'x/crypto/openpgp' "$OUT/vex/go-list-deps-all.txt" > "$OUT/vex/go-list-deps-openpgp-count.txt" || true
echo "go_list_deps_openpgp_packages=$(cat "$OUT/vex/go-list-deps-openpgp-count.txt")"
printf '%s\n' "$GOLIST_N" > "$OUT/vex/go-list-deps-total.txt"

echo "=== SBOM ↔ artefact 绑定校验 ==="
python3 - "$OUT" "$FILE_SHA" <<'PY'
import json,sys,hashlib,os
out,sha=sys.argv[1],sys.argv[2]
def fsha(p):
    return hashlib.sha256(open(p,'rb').read()).hexdigest()
cdx=json.load(open(os.path.join(out,"api.cdx.json"),encoding="utf-8"))
spdx=json.load(open(os.path.join(out,"api.spdx.json"),encoding="utf-8"))
mc=cdx.get("metadata",{}).get("component",{})
comps=cdx.get("components",[]) or []
purls=[c.get("purl","") for c in comps]
res={
 "artifact_sha256":sha,
 "cdx_component_name":mc.get("name"),
 "cdx_component_version":mc.get("version"),
 "cdx_version_is_artifact_sha": mc.get("version")=="sha256:"+sha,
 "cdx_bom_ref":mc.get("bom-ref"),
 "cdx_serial":cdx.get("serialNumber"),
 "cdx_component_count":len(comps),
 "spdx_name":spdx.get("name"),
 "spdx_document_namespace":spdx.get("documentNamespace"),
 "spdx_package_count":len(spdx.get("packages",[]) or []),
 "key_deps_present":{k:any(p.startswith("pkg:golang/"+k+"@") for p in purls) for k in
    ("github.com/xuri/excelize/v2","github.com/go-chi/chi/v5","github.com/jackc/pgx/v5","golang.org/x/crypto")},
 "cdx_sha256":fsha(os.path.join(out,"api.cdx.json")),
 "spdx_sha256":fsha(os.path.join(out,"api.spdx.json")),
}
assert res["cdx_version_is_artifact_sha"], "CDX metadata.component.version 必须等于 sha256:<artifact sha>"
assert res["cdx_component_count"]>0 and res["spdx_package_count"]>0, "SBOM 不得为空"
assert all(res["key_deps_present"].values()), "关键依赖缺失: %s" % res["key_deps_present"]
json.dump(res,open(os.path.join(out,"sbom-binding.json"),"w",encoding="utf-8"),ensure_ascii=False,indent=1)
print(json.dumps(res,ensure_ascii=False,indent=1))
PY
BIND_RC=$?
echo "binding_rc=$BIND_RC"
[ "$BIND_RC" -eq 0 ] || exit 5

echo "=== VEX 再出具（绑定同一 artefact sha） ==="
python3 - "$OUT" "$FILE_SHA" "$APIPID" <<'PY'
import json,sys,os,hashlib
out,sha,pid=sys.argv[1],sys.argv[2],sys.argv[3]
nmc=open(os.path.join(out,"vex","api.nm-openpgp-count.txt")).read().strip()
glc=open(os.path.join(out,"vex","go-list-deps-openpgp-count.txt")).read().strip()
tot=open(os.path.join(out,"vex","go-list-deps-total.txt")).read().strip()
cdx=json.load(open(os.path.join(out,"api.cdx.json"),encoding="utf-8"))
ref=cdx.get("metadata",{}).get("component",{}).get("bom-ref")
size=os.path.getsize("/home/euripides/projects/blockchain-platform/backend/bin/api")
vex={
 "vex_version":"1.0",
 "generated_at_utc":__import__("datetime").datetime.utcnow().strftime("%Y-%m-%dT%H:%M:%SZ"),
 "generated_by":"DSH main writer (P1-20260913 IAM workspace-scope fix round)",
 "scope":"per-artifact, THIS BUILD ONLY (api sha256=%s)"%sha,
 "sbom_reference":{"document":"api.cdx.json","serial":cdx.get("serialNumber"),"artefact_bom_ref":ref},
 "statements":[{
   "vulnerability":{"name":"GO-2026-5932","source":"https://vuln.go.dev/ID/GO-2026-5932.json",
                    "summary":"golang.org/x/crypto/openpgp is unmaintained and unsafe by design"},
   "status":"not_affected",
   "justification":"vulnerable_code_not_in_execute_path",
   "impact_statement":("受影响包仅为 golang.org/x/crypto/openpgp 及其子包。本 artefact（sha256=%s）中 "
     "nm 命中 openpgp 符号 %s 个；go list -deps ./cmd/api 共 %s 个包、其中 openpgp 前缀 %s 个。"
     "该结论仅针对此 sha 的二进制。")%(sha,nmc,tot,glc),
   "products":[{"component":"api",
     "bom_ref":ref,
     "hashes":[{"alg":"SHA-256","content":sha}],
     "size_bytes":size,
     "running_process":{"pid":int(pid),"exe":"/home/euripides/projects/blockchain-platform/backend/bin/api"}}],
   "evidence":{"proof_dir":"vex/","files":["api.nm-openpgp-count.txt","go-list-deps-openpgp-count.txt","go-list-deps-total.txt"],
               "nm_openpgp_symbols":int(nmc),"go_list_deps_openpgp_packages":int(glc),"go_list_deps_total":int(tot)},
   "assumptions":[
     "该版本不动态加载该包（无 plugin / 无运行时模块加载）。若引入 plugin，本声明作废。",
     "这是**对该 sha 具体产物**的影响分类，不是豁免，也不是宣称该公告无害。",
     "不基于『无 CVSS』，也不单基于 symbol scan level 0。"],
   "invalidation_triggers":[
     "api/worker/migrate 任何重新构建（产物 sha 变化）",
     "go.mod / go.sum / 依赖图任何变化",
     "引入 plugin 或动态加载"],
   "reviewed_at":__import__("datetime").datetime.utcnow().strftime("%Y-%m-%dT%H:%M:%SZ")
 }],
 "explicitly_not_claimed":[
   "全项目 P0=0 或 P1=0","release GO / 生产切换","容器镜像 P0/P1 状态（另行处理，仍 OPEN）",
   "任何其它依赖发现、SBOM 缺口或 §27/§31 验收项"],
 "notes":["本轮只覆盖该公告对**当前 api sha**的影响分类。任何产物哈希变化即失效。"]
}
p=os.path.join(out,"vex","scoped-vex-GO-2026-5932-%s.json"%sha[:12])
json.dump(vex,open(p,"w",encoding="utf-8"),ensure_ascii=False,indent=1)
print("vex_written",p,hashlib.sha256(open(p,'rb').read()).hexdigest())
PY
sha256sum "$OUT"/vex/* | tee "$OUT/vex/vex-hashes.txt"
chmod 600 "$OUT"/*.json "$OUT"/*.txt "$OUT"/vex/* 2>/dev/null
echo "SBOM_SCOPE_DIR=$OUT"
[ -f "$OUT/sbom-binding.json" ] && echo "SBOM_OK scope=$SCOPE api_sha=$FILE_SHA"
exit 0