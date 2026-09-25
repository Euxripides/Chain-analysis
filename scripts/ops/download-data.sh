#!/usr/bin/env bash
#
# download-data.sh —— 通过 GPT Actions Bearer Key 接口下载链上数据。
#
# 完整说明见 docs/manuals/data-download-api.md。三条子命令：
#   ensure  触发/复用下载任务（可能产生真实 Provider 取数与费用），轮询到结束
#   export  把已下载的数据导出成文件并拉到本地（不产生 Provider 费用）
#   all     ensure 后紧接着 export
#
# 用法示例：
#   ./download-data.sh export --chain bsc --address 0xabc... --datasets transactions --format csv --out ./out
#   ./download-data.sh ensure --chain bsc --address 0xabc... --datasets transactions,token_transfers --range full
#   BASE=http://127.0.0.1:8080 ./download-data.sh export --chain bsc --address 0xabc... --direct
#
# Key 解析顺序：$GPT_ACTIONS_KEY → 同仓库 deploy/.env 的 GPT_ACTIONS_API_KEY（仅部署机可读）。
# 脚本从不打印 Key。

set -euo pipefail

BASE="${BASE:-https://aurore.online}"
DOWNLOAD_ORIGIN="${DOWNLOAD_ORIGIN:-https://download.aurore.online:8443}"
ENSURE_TIMEOUT="${ENSURE_TIMEOUT:-3600}"
EXPORT_TIMEOUT="${EXPORT_TIMEOUT:-900}"

die() { echo "错误：$*" >&2; exit 1; }

resolve_key() {
  if [ -n "${GPT_ACTIONS_KEY:-}" ]; then printf '%s' "$GPT_ACTIONS_KEY"; return; fi
  local here env_file
  here="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
  for env_file in "$here/../../deploy/.env" "$here/../deploy/.env" "$PWD/deploy/.env"; do
    if [ -f "$env_file" ]; then
      local value
      value="$(grep -m1 '^GPT_ACTIONS_API_KEY=' "$env_file" | cut -d= -f2- | tr -d '"' | tr -d "'" | tr -d '\r')"
      if [ -n "$value" ]; then printf '%s' "$value"; return; fi
    fi
  done
  die "未找到 API Key：请设置 GPT_ACTIONS_KEY，或在部署机仓库根目录执行（可读 deploy/.env）"
}

KEY="$(resolve_key)"
[ -n "$KEY" ] || die "API Key 为空"

api() { # api METHOD PATH [JSON_BODY]
  local method="$1" path="$2" body="${3:-}"
  if [ -n "$body" ]; then
    curl -fsS -X "$method" -H "Authorization: Bearer $KEY" -H 'Content-Type: application/json' \
      "${PROTO_HEADER[@]}" --data-binary "$body" "$BASE$path"
  else
    curl -fsS -X "$method" -H "Authorization: Bearer $KEY" "${PROTO_HEADER[@]}" "$BASE$path"
  fi
}

json_get() { python3 -c 'import json,sys;cur=json.load(sys.stdin)
for part in sys.argv[1].split("."):
    cur = cur[int(part)] if isinstance(cur, list) else cur.get(part)
    if cur is None: print(""); break
else: print(cur)' "$1"; }

case "${1:-}" in
  -h|--help) sed -n '2,20p' "${BASH_SOURCE[0]}"; exit 0 ;;
esac
CMD="${1:-}"; shift || true
[ "$CMD" = "ensure" ] || [ "$CMD" = "export" ] || [ "$CMD" = "all" ] || die "用法：download-data.sh <ensure|export|all> [参数...]"

CHAIN=""; DATASETS="transactions"; FORMAT="csv"; RANGE_TYPE="full"
FROM_BLOCK=""; TO_BLOCK=""; FROM_TIME=""; TO_TIME=""; OUT="."; DIRECT=0
ADDRESSES=()

while [ $# -gt 0 ]; do
  case "$1" in
    --chain) CHAIN="$2"; shift 2 ;;
    --address) ADDRESSES+=("$2"); shift 2 ;;
    --datasets) DATASETS="$2"; shift 2 ;;
    --format) FORMAT="$2"; shift 2 ;;
    --range) RANGE_TYPE="$2"; shift 2 ;;
    --from-block) FROM_BLOCK="$2"; shift 2 ;;
    --to-block) TO_BLOCK="$2"; shift 2 ;;
    --from) FROM_TIME="$2"; shift 2 ;;
    --to) TO_TIME="$2"; shift 2 ;;
    --out) OUT="$2"; shift 2 ;;
    --base) BASE="$2"; shift 2 ;;
    --direct) DIRECT=1; shift ;;
    -h|--help) sed -n '2,20p' "${BASH_SOURCE[0]}"; exit 0 ;;
    *) die "未知参数：$1" ;;
  esac
done

[ -n "$CHAIN" ] || die "--chain 必填"
[ "${#ADDRESSES[@]}" -gt 0 ] || die "至少一个 --address"

# 参数先在前台校验，避免在命令替换的子 shell 里报错（错误信息会丢）。
case "$RANGE_TYPE" in
  full) ;;
  block) [ -n "$FROM_BLOCK" ] && [ -n "$TO_BLOCK" ] || die "--range block 需要 --from-block/--to-block" ;;
  time)  [ -n "$FROM_TIME" ] && [ -n "$TO_TIME" ] || die "--range time 需要 --from/--to" ;;
  *) die "--range 只接受 full|block|time" ;;
esac
if [ "$CMD" = "export" ] && [ -n "$FROM_BLOCK" ]; then
  die "export 不支持区块范围：请用 --from/--to 指定时间范围（区块范围只在 ensure 生效）"
fi

# 直连 127.0.0.1:8080 时服务端会把 download_url 拼成 https://127.0.0.1:8080/...，
# 因此显式声明协议，让它回 http；走 https://aurore.online 时由 nginx 自己设置。
# 必须在参数解析之后求值，否则 --base 传进来的地址不生效。
PROTO_HEADER=()
case "$BASE" in
  http://*) PROTO_HEADER=(-H 'X-Forwarded-Proto: http') ;;
esac

mkdir -p "$OUT"

range_json() {
  case "$RANGE_TYPE" in
    full) printf '{"type":"full"}' ;;
    block) [ -n "$FROM_BLOCK" ] && [ -n "$TO_BLOCK" ] || die "--range block 需要 --from-block/--to-block"
           printf '{"type":"block","from_block":%s,"to_block":%s}' "$FROM_BLOCK" "$TO_BLOCK" ;;
    time)  [ -n "$FROM_TIME" ] && [ -n "$TO_TIME" ] || die "--range time 需要 --from/--to"
           printf '{"type":"time","from":"%s","to":"%s"}' "$FROM_TIME" "$TO_TIME" ;;
    *) die "--range 只接受 full|block|time" ;;
  esac
}

do_ensure() {
  local payload addresses
  addresses="$(python3 -c 'import json,sys;print(json.dumps(sys.argv[1:]))' "${ADDRESSES[@]}")"
  payload="$(python3 - "$CHAIN" "$addresses" "$DATASETS" "$(range_json)" <<'PY'
import json,sys
chain, addresses, datasets, rng = sys.argv[1], json.loads(sys.argv[2]), sys.argv[3].split(","), json.loads(sys.argv[4])
print(json.dumps({"chain": chain, "addresses": addresses,
                  "datasets": [d.strip() for d in datasets if d.strip()],
                  "range": rng, "performance_mode": "fast"}))
PY
)"
  echo "[ensure] POST $BASE/api/v1/data/ensure  chain=$CHAIN addresses=${#ADDRESSES[@]} datasets=$DATASETS"
  local resp job
  resp="$(api POST /api/v1/data/ensure "$payload")"
  echo "[ensure] $resp"
  job="$(printf '%s' "$resp" | json_get data.download_job_id)"
  if [ -z "$job" ]; then
    echo "[ensure] 本地 Coverage 已覆盖请求范围，未创建任务、未调用 Provider。"
    return 0
  fi
  echo "[ensure] job_id=$job  开始轮询（最长 ${ENSURE_TIMEOUT}s）"
  local deadline=$(( $(date +%s) + ENSURE_TIMEOUT )) status
  while :; do
    status="$(api GET "/api/v1/data/download-status?download_job_id=$job" | json_get data.status)"
    printf '[ensure] %s %s\n' "$(date +%H:%M:%S)" "$status"
    case "$status" in
      completed) echo "[ensure] 完成"; return 0 ;;
      failed|cancelled|aborted_by_user|partial_failed) die "任务未成功：$status" ;;
    esac
    [ "$(date +%s)" -lt "$deadline" ] || die "等待下载任务超时"
    sleep 5
  done
}

do_export() {
  local addr resp job url file
  for addr in "${ADDRESSES[@]}"; do
    local payload
    payload="$(python3 - "$CHAIN" "$addr" "$FORMAT" "$DATASETS" "$FROM_TIME" "$TO_TIME" <<'PY'
import json,sys
chain, address, fmt, datasets, t0, t1 = sys.argv[1:7]
body = {"chain": chain, "address": address, "format": fmt,
        "data_types": [d.strip() for d in datasets.split(",") if d.strip()]}
if fmt == "csv":
    body["compression"] = "zip"
if t0: body["StartTime"] = t0
if t1: body["EndTime"] = t1
print(json.dumps(body))
PY
)"
    echo "[export] POST $BASE/api/v1/export/create  address=$addr format=$FORMAT datasets=$DATASETS"
    resp="$(api POST /api/v1/export/create "$payload")"
    job="$(printf '%s' "$resp" | json_get data.job_id)"
    [ -n "$job" ] || die "创建导出失败：$resp"
    local deadline=$(( $(date +%s) + EXPORT_TIMEOUT )) status
    while :; do
      resp="$(api GET "/api/v1/export/status?job_id=$job")"
      status="$(printf '%s' "$resp" | json_get data.status)"
      printf '[export] %s %s\n' "$(date +%H:%M:%S)" "$status"
      case "$status" in
        completed) break ;;
        failed) die "导出失败：$(printf '%s' "$resp" | json_get data.error)" ;;
      esac
      [ "$(date +%s)" -lt "$deadline" ] || die "等待导出超时"
      sleep 3
    done
    url="$(printf '%s' "$resp" | json_get data.download_url)"
    [ -n "$url" ] || die "服务端未返回 download_url：$resp"
    if [ "$DIRECT" = "1" ]; then
      url="$DOWNLOAD_ORIGIN$(printf '%s' "$url" | sed -E 's#^[a-z]+://[^/]+##')"
    fi
    local ext="parquet"
    [ "$FORMAT" = "csv" ] && ext="zip"
    file="$OUT/${CHAIN}-${addr}-${job:0:8}.$ext"
    echo "[export] GET $url -> $file"
    curl -fsSL -o "$file" "$url"
    echo "[export] 完成：$file ($(wc -c < "$file") 字节, sha256=$(sha256sum "$file" | cut -d' ' -f1))"
  done
}

case "$CMD" in
  ensure) do_ensure ;;
  export) do_export ;;
  all)    do_ensure; do_export ;;
esac
