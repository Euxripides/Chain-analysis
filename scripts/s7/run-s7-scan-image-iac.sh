#!/usr/bin/env bash
# =====================================================================
# §16.9 镜像漏洞 + IaC 配置复扫（可在"漏洞库已离线注入"的环境执行）
#
# 为什么单独一个脚本：镜像扫描此前被判定为"不可执行"，根因是漏洞库拉取（mirror.gcr.io 超时、
# ghcr.io 实测约 40 KiB/s）。实际可用路径有两条，任一即可把 trivy 的 DB 落到本地缓存：
#   ① 镜像源：docker pull ghcr.m.daocloud.io/aquasecurity/trivy-db:2（实测 119 MB / 55 s 内完成），
#      再 docker save + 从 OCI blob 里取出 trivy.db 与 metadata.json；
#   ② trivy 自取：trivy image --download-db-only --db-repository <mirror>/aquasecurity/trivy-db（较慢但可行）。
# 本脚本只负责**用已就绪的本地漏洞库做扫描并留证据**，不联网、不改动任何镜像。
#
# 用法：bash scripts/s7/run-s7-scan-image-iac.sh
# 环境变量：
#   SCAN_IMAGES        镜像清单（默认 deploy/compose.yaml 里的 4 个 + minio/mc:latest）
#   SCAN_SEVERITY      扫描严重级别（默认 HIGH,CRITICAL）
#   TRIVY_IMAGE        trivy 容器镜像（默认 aquasec/trivy:latest）
#   TRIVY_CACHE        漏洞库缓存目录（默认 $HOME/.cache/trivy，须含 db/trivy.db 与 db/metadata.json）
#   SCAN_CONFIG_PATHS  IaC 扫描路径（默认 deploy）
#   SCAN_LABEL         证据批次标签（默认时间戳）
# 输出：acceptance-evidence/dual-system-v1/sprint7/security/image-scan-<标签>/
#   image-<slug>.json、config-<name>.json、summary.md、summary.json、commands.log
# 退出码：0 = 全部扫描完成（有没有漏洞看报告）；1 = 有扫描未完成（**禁止**当成"无漏洞"）；2 = 前置错误
# =====================================================================
set -uo pipefail

PROJECT="${PROJECT:-/home/euripides/projects/blockchain-platform}"
TRIVY_IMAGE="${TRIVY_IMAGE:-aquasec/trivy:latest}"
TRIVY_CACHE="${TRIVY_CACHE:-$HOME/.cache/trivy}"
SEVERITY="${SCAN_SEVERITY:-HIGH,CRITICAL}"
CONFIG_PATHS="${SCAN_CONFIG_PATHS:-deploy}"
LABEL="${SCAN_LABEL:-$(date -u +%Y%m%dT%H%M%SZ)}"
EV="$PROJECT/acceptance-evidence/dual-system-v1/sprint7/security/image-scan-$LABEL"
DEFAULT_IMAGES="postgres:18 minio/minio:latest clickhouse/clickhouse-server:26.7 nats:2-alpine minio/mc:latest"
IMAGES="${SCAN_IMAGES:-$DEFAULT_IMAGES}"

log()  { printf '%s\n' "$*"; }
fail() { printf 'FAIL: %s\n' "$*" >&2; FAILURES=$((FAILURES+1)); }
FAILURES=0

command -v docker >/dev/null 2>&1 || { echo "ABORT: 找不到 docker" >&2; exit 2; }
if ! docker image inspect "$TRIVY_IMAGE" >/dev/null 2>&1; then
  echo "ABORT: 缺少 trivy 容器镜像 $TRIVY_IMAGE（本脚本不联网拉取）" >&2; exit 2
fi
if [ ! -s "$TRIVY_CACHE/db/trivy.db" ] || [ ! -s "$TRIVY_CACHE/db/metadata.json" ]; then
  echo "ABORT: 本地漏洞库缺失：$TRIVY_CACHE/db/trivy.db 或 metadata.json" >&2
  echo "  取证路径见本脚本头部注释（镜像源 docker pull <mirror>/aquasecurity/trivy-db:2 后取 blob 里的 trivy.db）" >&2
  exit 2
fi
mkdir -p "$EV" || { echo "ABORT: 无法创建 $EV" >&2; exit 2; }
CMDLOG="$EV/commands.log"
: > "$CMDLOG"
DB_META="$(cat "$TRIVY_CACHE/db/metadata.json" 2>/dev/null | tr -d '\n')"

{
  echo "# §16.9 镜像 / IaC 复扫（$LABEL）"
  echo
  echo "- trivy 容器：\`$TRIVY_IMAGE\`"
  echo "- 本地漏洞库：\`$TRIVY_CACHE/db/trivy.db\`（metadata：\`$DB_META\`）"
  echo "- 扫描级别：$SEVERITY；证据目录：\`$EV\`"
  echo
  echo "## 1. 镜像"
  echo
  echo "| 镜像 | 镜像 ID | digest | CRITICAL | HIGH | 判定 |"
  echo "|---|---|---|---:|---:|---|"
} > "$EV/summary.md"

SCANNED=0
for img in $IMAGES; do
  slug="$(printf '%s' "$img" | tr '/:@' '___')"
  out="$EV/image-$slug.json"
  iid="$(docker image inspect "$img" --format '{{.Id}}' 2>/dev/null || echo "")"
  digest="$(docker image inspect "$img" --format '{{index .RepoDigests 0}}' 2>/dev/null || echo "")"
  [ -n "$iid" ] || { fail "镜像不存在：$img"; echo "| $img | - | - | - | - | 镜像不存在 |" >> "$EV/summary.md"; continue; }
  echo "trivy image --skip-db-update --scanners vuln --severity $SEVERITY $img" >> "$CMDLOG"
  docker run --rm -v /var/run/docker.sock:/var/run/docker.sock -v "$TRIVY_CACHE:/root/.cache/trivy" \
    "$TRIVY_IMAGE" image --skip-db-update --image-src docker --scanners vuln \
    --severity "$SEVERITY" --format json "$img" > "$out" 2>> "$EV/trivy-stderr.log"
  rc=$?
  if [ "$rc" -ne 0 ] || [ ! -s "$out" ]; then
    fail "trivy image 扫描未完成：$img（rc=$rc，见 $EV/trivy-stderr.log）"
    echo "| $img | $iid | \`$digest\` | - | - | **扫描未完成** |" >> "$EV/summary.md"
    continue
  fi
  SCANNED=$((SCANNED+1))
  python3 - "$out" "$EV" "$img" "$iid" "$digest" "$SEVERITY" "$EV/summary.md" <<'PY'
import json, sys
path, ev, img, iid, digest, sev, summary = sys.argv[1:8]
d = json.load(open(path))
counts = {"CRITICAL": 0, "HIGH": 0, "MEDIUM": 0, "LOW": 0, "UNKNOWN": 0}
rows = []
for r in (d.get("Results") or []):
    for v in (r.get("Vulnerabilities") or []):
        s = (v.get("Severity") or "UNKNOWN").upper()
        counts[s] = counts.get(s, 0) + 1
        rows.append((s, r.get("Target", ""), v.get("VulnerabilityID", ""), v.get("PkgName", ""),
                     v.get("InstalledVersion", ""), v.get("FixedVersion", "") or "-", v.get("Title", "")[:80]))
base = img.replace("/", "_").replace(":", "_")
with open("%s/image-%s.findings.tsv" % (ev, base), "w") as f:
    f.write("severity\ttarget\tid\tpackage\tinstalled\tfixed\ttitle\n")
    for r in sorted(rows):
        f.write("\t".join(str(x) for x in r) + "\n")
summary_rows = [r for r in rows if r[0] in ("CRITICAL", "HIGH")]
with open(summary, "a") as f:
    f.write("| `%s` | %s | `%s` | %d | %d | %s |\n" % (img, iid[:19], digest, counts["CRITICAL"], counts["HIGH"],
            "有 CRITICAL/HIGH" if summary_rows else "本级别无发现"))
    if summary_rows:
        f.write("\n<details><summary>%s：CRITICAL/HIGH 明细（%d 条）</summary>\n\n" % (img, len(summary_rows)))
        f.write("| 级别 | 目标 | 漏洞 | 包 | 已安装 | 修复版本 |\n|---|---|---|---|---|---|\n")
        for r in summary_rows[:200]:
            f.write("| %s | %s | %s | %s | %s | %s |\n" % (r[0], r[1], r[2], r[3], r[4], r[5]))
        f.write("\n</details>\n\n")
print("%s: CRITICAL=%d HIGH=%d MEDIUM=%d LOW=%d" % (img, counts["CRITICAL"], counts["HIGH"], counts["MEDIUM"], counts["LOW"]))
PY
  rc=$?
  [ "$rc" -eq 0 ] || fail "镜像 $img 的报告解析失败（rc=$rc）"
done

{
  echo
  echo "## 2. IaC 配置扫描"
  echo
  echo "扫描路径：\`$CONFIG_PATHS\`（trivy config；不支持 systemd unit / nginx 配置 ⇒ 这三类不进本节结论）"
  echo
} >> "$EV/summary.md"
for p in $CONFIG_PATHS; do
  name="$(printf '%s' "$p" | tr '/' '_')"
  out="$EV/config-$name.json"
  echo "trivy config --severity $SEVERITY $p" >> "$CMDLOG"
  docker run --rm -v "$PROJECT:/src:ro" -v "$TRIVY_CACHE:/root/.cache/trivy" \
    "$TRIVY_IMAGE" config --skip-check-update --severity "$SEVERITY" --format json "/src/$p" \
    > "$out" 2>> "$EV/trivy-stderr.log"
  rc=$?
  if [ "$rc" -ne 0 ] || [ ! -s "$out" ]; then
    fail "trivy config 扫描未完成：$p（rc=$rc，见 $EV/trivy-stderr.log）"
    echo "- \`$p\`：**扫描未完成**（rc=$rc）" >> "$EV/summary.md"
    continue
  fi
  python3 - "$out" "$EV/summary.md" "$p" <<'PY'
import json, sys
path, summary, p = sys.argv[1:4]
d = json.load(open(path))
rows = []
for r in (d.get("Results") or []):
    for m in (r.get("Misconfigurations") or []):
        rows.append((m.get("Severity", ""), r.get("Target", ""), m.get("ID", ""), m.get("Title", "")[:100], m.get("Resolution", "")[:120]))
with open(summary, "a") as f:
    if rows:
        f.write("| 级别 | 目标 | 规则 | 标题 |\n|---|---|---|---|\n")
        for r in sorted(rows)[:200]:
            f.write("| %s | %s | %s | %s |\n" % (r[0], r[1], r[2], r[3]))
    else:
        f.write("- `%s`：$SEVERITY 级别无发现\n" % p)
    f.write("\n")
print("%s: %d 条" % (p, len(rows)))
PY
  rc=$?
  [ "$rc" -eq 0 ] || fail "IaC 报告解析失败：$p（rc=$rc）"
done

{
  echo "## 3. 范围与限制（不得外推）"
  echo
  echo "- 镜像扫描只覆盖**运行中的依赖容器镜像**；平台自身的 Go 二进制与前端产物不在镜像扫描范围内（分别由 \`govulncheck\` 与 \`pnpm audit\` 覆盖）。"
  echo "- trivy config 覆盖 compose / Dockerfile / Kubernetes 等**受支持**格式；systemd unit（\`deploy/*.service\`）、\`deploy/nginx*\`、\`deploy/monitoring/*\` 不在其支持范围，本节不为其结论背书。"
  echo "- 漏洞库为本地快照（metadata 见上）；扫描结论的有效期受该快照时间约束。"
  echo "- 本脚本**不修改**任何镜像、不做修复动作。"
} >> "$EV/summary.md"

python3 - "$EV" "$LABEL" "$FAILURES" "$SCANNED" "$SEVERITY" "$DB_META" <<'PY'
import json, os, sys
ev, label, failures, scanned, sev, meta = sys.argv[1:7]
def counts(path):
    try:
        d = json.load(open(path))
    except Exception:
        return None
    c = {"CRITICAL": 0, "HIGH": 0, "MEDIUM": 0, "LOW": 0, "UNKNOWN": 0}
    if d.get("ArtifactName") or d.get("Results"):
        for r in (d.get("Results") or []):
            for v in (r.get("Vulnerabilities") or []):
                s = (v.get("Severity") or "UNKNOWN").upper(); c[s] = c.get(s, 0) + 1
    elif isinstance(d.get("Results"), list):
        pass
    return c
res = {"scan": "s7-image-iac", "label": label, "severity_filter": sev, "db_metadata": meta,
       "images": {}, "iac": {}, "scans_incomplete": int(failures), "images_scanned": int(scanned),
       "verdict": "INCOMPLETE" if int(failures) else "COMPLETE"}
for n in sorted(os.listdir(ev)):
    if n.startswith("image-") and n.endswith(".json"):
        res["images"][n[len("image-"):-5]] = counts(os.path.join(ev, n))
for n in sorted(os.listdir(ev)):
    if n.startswith("config-") and n.endswith(".json"):
        try:
            d = json.load(open(os.path.join(ev, n)))
        except Exception:
            res["iac"][n] = None; continue
        mis = 0
        for r in (d.get("Results") or []):
            mis += len(r.get("Misconfigurations") or [])
        res["iac"][n[len("config-"):-5]] = mis
json.dump(res, open(os.path.join(ev, "summary.json"), "w"), indent=2, ensure_ascii=False)
print(json.dumps(res, indent=2, ensure_ascii=False))
PY

echo "证据目录：$EV"
echo "汇总：$EV/summary.md"
if [ "$FAILURES" -gt 0 ]; then
  echo "扫描未完成 $FAILURES 项：**不得**当作"无漏洞"（见 $EV/trivy-stderr.log）" >&2
  exit 1
fi
echo "全部扫描完成（是否有漏洞见 summary.md）"
exit 0
