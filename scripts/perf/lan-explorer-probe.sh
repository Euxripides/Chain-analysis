#!/usr/bin/env bash
# S7-PERF-02：explorer 慢端点**串行单请求**延迟探针。
#
# 为什么不用 run-perf-suite.sh 的 loadprobe：本机是共享部署机（Docker + ClickHouse + 备份/DR
# 任务共用），并发压测的数字会被同机后台扫描污染，也无法把"端点自身慢"与"机器被占满"分开。
# 这里以 concurrency=1 逐请求计时，先固定"单个请求自己花多久"，再谈并发吞吐。
#
# 登录/夹具口径与 run-perf-suite.sh 完全一致（含 workspace_id，避免量到 401 的耗时）。
#
# 用法：bash scripts/perf/lan-explorer-probe.sh <label> [n] [warmup] [outdir]
# 输出：<outdir>/<label>-<endpoint>.json（含逐请求原始 ms）
set -uo pipefail
PROJECT="${PROJECT:-/home/euripides/projects/blockchain-platform}"
LABEL="${1:-probe}"
N="${2:-25}"
WARM="${3:-3}"
OUTDIR="${4:-$PROJECT/.work/perf}"
mkdir -p "$OUTDIR"
export XDG_RUNTIME_DIR="${XDG_RUNTIME_DIR:-/run/user/$(id -u)}"

set -a; . "$PROJECT/deploy/.env"; set +a
if [ -f /tmp/s6-e2e-env.sh ]; then . /tmp/s6-e2e-env.sh; fi
if [ -f /tmp/dual-system-fixture.env ]; then . /tmp/dual-system-fixture.env; fi

BASE="${BASE:-http://127.0.0.1:8080}"
ADDR="${E2E_S5_10K_ADDRESS:-TCFNp179Lg46D16zKoumd4Poa2WFFdtqYj}"
CHAIN="${E2E_S5_10K_CHAIN:-tron}"

cookieheader() {
  awk '
    /^#HttpOnly_/ { sub(/^#HttpOnly_/, ""); printf "%s=%s; ", $6, $7; next }
    /^#/ || /^$/   { next }
    { printf "%s=%s; ", $6, $7 }
  ' "$1"
}

J=$(mktemp)
curl -s -c "$J" -o /dev/null --max-time 10 -H 'Content-Type: application/json' -H "X-Auth-Audience: user" \
  -d "{\"identifier\":\"fixture-investigator@fixture.local\",\"password\":\"${INVESTIGATOR_PASSWORD}\",\"audience\":\"user\",\"workspace_id\":\"${FIXTURE_WORKSPACE_ID}\"}" \
  "$BASE/api/auth/v1/login"
CK=$(cookieheader "$J")
if ! echo "$CK" | grep -q "cs_user_session="; then echo "FATAL: 登录未拿到用户会话 Cookie，数字无意义"; exit 2; fi

echo "== $LABEL: chain=$CHAIN addr=$ADDR base=$BASE n=$N warmup=$WARM =="
printf '%-14s %-8s %-8s %-8s %-9s %-6s %s\n' ENDPOINT p50_ms p95_ms p99_ms max_ms err bytes

probe() { # probe <endpoint> <path>
  local ep="$1"
  local path="$2"
  local raw="$OUTDIR/$LABEL-$ep.raw"
  : > "$raw"
  local i code t bytes
  for ((i=0;i<WARM;i++)); do
    curl -s -o /dev/null -b "$CK" --max-time 120 -w '%{http_code} %{time_total} %{size_download}\n' "$BASE$path" >> "$raw.warm"
  done
  for ((i=0;i<N;i++)); do
    read -r code t bytes < <(curl -s -o /dev/null -b "$CK" --max-time 120 -w '%{http_code} %{time_total} %{size_download}' "$BASE$path")
    echo "$code $t $bytes" >> "$raw"
  done
  python3 - "$raw" "$ep" "$LABEL" "$path" "$OUTDIR" "$N" <<'PY'
import json, sys, statistics
raw, ep, label, path, outdir, n = sys.argv[1:7]
rows = []
for line in open(raw):
    parts = line.split()
    if len(parts) != 3: continue
    code, t, b = parts
    rows.append({"status": int(code), "ms": float(t)*1000.0, "bytes": int(b)})
ms = sorted(r["ms"] for r in rows)
def pct(p):
    if not ms: return None
    k = (len(ms)-1)*p
    lo, hi = int(k), min(int(k)+1, len(ms)-1)
    return ms[lo] + (ms[hi]-ms[lo])*(k-lo)
stats = {
  "label": label, "endpoint": ep, "path": path, "requests": len(ms),
  "p50_ms": round(pct(0.50),2) if ms else None,
  "p95_ms": round(pct(0.95),2) if ms else None,
  "p99_ms": round(pct(0.99),2) if ms else None,
  "max_ms": round(max(ms),2) if ms else None,
  "min_ms": round(min(ms),2) if ms else None,
  "errors": sum(1 for r in rows if r["status"] != 200),
  "status_counts": {str(c): sum(1 for r in rows if r["status"]==c) for c in sorted({r["status"] for r in rows})},
  "avg_bytes": round(statistics.mean([r["bytes"] for r in rows]),1) if rows else 0,
  "raw_ms": [round(r["ms"],2) for r in rows],
}
json.dump(stats, open(f"{outdir}/{label}-{ep}.json","w"), indent=2, ensure_ascii=False)
print("%-14s %-8.2f %-8.2f %-8.2f %-9.2f %-6d %.0f" % (
  ep, stats["p50_ms"] or 0, stats["p95_ms"] or 0, stats["p99_ms"] or 0,
  stats["max_ms"] or 0, stats["errors"], stats["avg_bytes"]))
PY
}

# ENDPOINTS 可选：只跑其中一部分（补测某个端点时不必重跑全部，白等几分钟）。
WANT="${ENDPOINTS:-activity,flow-profile,overview,assets}"
want() { case ",$WANT," in *",$1,"*) return 0;; *) return 1;; esac; }

want activity     && probe activity     "/api/v2/explorer/addresses/$CHAIN/$ADDR/activity?limit=200"
want flow-profile && probe flow-profile "/api/v2/explorer/addresses/$CHAIN/$ADDR/flow-profile"
want overview     && probe overview     "/api/v2/explorer/addresses/$CHAIN/$ADDR/overview"
want assets       && probe assets       "/api/v2/explorer/addresses/$CHAIN/$ADDR/assets"

echo "== raw + json in $OUTDIR =="
