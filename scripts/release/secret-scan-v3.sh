#!/usr/bin/env bash
# secret-scan-v3.sh — fail-closed 按值凭据扫描器（修正 v1/v2 的真实缺陷）。
#
# 历史（保留，不热改）：
#   v1 secret-scan.sh        sha256 1fae56f99f37e51e860456824634dc5873d66840c015a84af86c14e65f81365e
#       缺陷：(a) 计数器写在 "{ ... } | tee" 大括号块 → pipeline 子 shell，SCAN_FAIL 不回传主 shell（假 PASS 风险）；
#             (b) pattern_fingerprints 用 awk '/^pattern /' 匹配，而 META 行前缀是 "key="，字段恒为空。
#   v2 secret-scan-v2.sh     sha256 96c80e6220bb743afdc4a34bfb1c0d416fbc016f719c3b28e5ff395698f6d782
#       修正 (b) 的匹配前缀，但仍是手写 printf 拼 JSON（引号/反斜线/路径未做安全编码），
#       且用 awk 'length($0)>=8' 把**短 required 值静默过滤**后只靠 optional 计数过门槛。
#
# v3 变更：
#   1. required 键的值**任何长度都实际参与扫描**（不再被长度过滤静默丢弃）；
#      长度低于 PASSWORD_POLICY_MIN(默认 8) 时在 JSON 中记 policy_warning=true，并仍然扫描。
#   2. DSN 口令**结构化解析 + URL decode**：同时扫描"文件中的原样编码形式"与"解码后的真实字节形式"。
#   3. 机器 JSON 全部交给 python3 的 json.dump 生成 → 路径/引号/反斜线安全编码，不手写拼接。
#   4. 状态聚合仍在主 shell；env 不可读 / required 键缺失 / pattern 数不足 / grep 缺失一律非 0。
set -uo pipefail

ENV_FILE="${1:-}"; shift || true
ARTIFACTS=("$@")
MIN_PATTERNS="${MIN_PATTERNS:-3}"
PASSWORD_POLICY_MIN="${PASSWORD_POLICY_MIN:-8}"
REQUIRED_KEYS="${REQUIRED_KEYS:-POSTGRES_PASSWORD CLICKHOUSE_PASSWORD MINIO_ROOT_PASSWORD}"
OPTIONAL_KEYS="${OPTIONAL_KEYS:-GPT_ACTIONS_API_KEY GPT_ACTIONS_ADMIN_KEY MCP_API_KEY ANKR_API_KEY CHAINBASE_API_KEY TRONGRID_API_KEY ANKR_MANAGEMENT_ENCRYPTION_KEY}"
DSN_KEYS="${DSN_KEYS:-DATABASE_URL MIGRATION_DATABASE_URL CLICKHOUSE_URL NATS_URL}"
OUT_JSON="${OUT_JSON:-}"

die() { echo "secret-scan-v3: $*" >&2; exit "$1"; }
[ -n "$ENV_FILE" ] || die 2 "usage: secret-scan-v3.sh <env-file> <artifact> [artifact...]"
[ "${#ARTIFACTS[@]}" -gt 0 ] || die 2 "no artifacts given"
[ -r "$ENV_FILE" ] || die 2 "env file not readable: $ENV_FILE"
command -v grep >/dev/null 2>&1 || die 3 "grep not available"
command -v python3 >/dev/null 2>&1 || die 3 "python3 not available (needed for safe JSON)"

umask 077
TMPD="$(mktemp -d)"; chmod 700 "$TMPD"
PAT="$TMPD/patterns.tsv"          # value<TAB>label
META="$TMPD/meta.tsv"             # key<TAB>form<TAB>len<TAB>sha12<TAB>policy_ok
SCAN="$TMPD/scan.tsv"             # artifact<TAB>hits<TAB>status
: > "$PAT"; : > "$META"; : > "$SCAN"

env_value() { awk -v k="$1" -F= 'index($0, k"=")==1 {print substr($0, length(k)+2); exit}' "$ENV_FILE"; }
sha12() { printf '%s' "$1" | sha256sum | cut -c1-12; }
urldecode() { python3 -c 'import sys,urllib.parse;sys.stdout.write(urllib.parse.unquote(sys.argv[1]))' "$1"; }

add_pattern() { # add_pattern <value> <key> <form> <policy_required:0|1>
  local v="$1" k="$2" form="$3" req="$4"
  [ -z "$v" ] && return 0
  printf '%s\t%s/%s\n' "$v" "$k" "$form" >> "$PAT"
  local ok=true
  if [ "$req" = "1" ] && [ "${#v}" -lt "$PASSWORD_POLICY_MIN" ]; then ok=false; fi
  printf '%s\t%s\t%s\t%s\t%s\n' "$k" "$form" "${#v}" "$(sha12 "$v")" "$ok" >> "$META"
}

MISSING_REQUIRED=""
for k in $REQUIRED_KEYS; do
  v="$(env_value "$k")"
  if [ -z "$v" ]; then MISSING_REQUIRED="$MISSING_REQUIRED $k"; continue; fi
  add_pattern "$v" "$k" "raw" 1          # 任何长度都扫描
done
[ -n "$MISSING_REQUIRED" ] && die 2 "required credential keys missing/empty in $ENV_FILE:$MISSING_REQUIRED"

for k in $DSN_KEYS; do
  raw="$(env_value "$k")"
  [ -z "$raw" ] && continue
  case "$raw" in
    *"://"*)
      pw="$(printf '%s' "$raw" | sed -E 's#^[a-zA-Z0-9+.-]+://([^:@/]+):([^@]*)@.*#\2#')"
      case "$pw" in *"://"*) pw="" ;; esac
      if [ -n "$pw" ]; then
        add_pattern "$pw" "$k" "dsn-raw" 1
        dec="$(urldecode "$pw")"
        if [ -n "$dec" ] && [ "$dec" != "$pw" ]; then add_pattern "$dec" "$k" "dsn-urldecoded" 1; fi
      fi
      ;;
  esac
done

for k in $OPTIONAL_KEYS; do
  raw="$(env_value "$k")"
  [ -z "$raw" ] && continue
  case "$raw" in
    *"://"*)
      pw="$(printf '%s' "$raw" | sed -E 's#^[a-zA-Z0-9+.-]+://([^:@/]+):([^@]*)@.*#\2#')"
      case "$pw" in *"://"*) pw="" ;; esac
      [ -n "$pw" ] && add_pattern "$pw" "$k" "dsn-raw" 0
      ;;
    *) add_pattern "$raw" "$k" "raw" 0 ;;
  esac
done

# 仅对"非 required/非凭据"的极短值做噪声过滤；required 值已在上面全量登记
sort -u -k1,1 "$PAT" > "$TMPD/patterns.sorted.tsv"
PATTERNS="$(wc -l < "$TMPD/patterns.sorted.tsv")"
[ "$PATTERNS" -ge "$MIN_PATTERNS" ] || die 2 "pattern count $PATTERNS < MIN_PATTERNS $MIN_PATTERNS (parse did not cover credential set)"

TOTAL=0; HITS=0
while IFS=$'\t' read -r v label; do
  [ -z "$v" ] && continue
  for a in "${ARTIFACTS[@]}"; do
    if [ ! -r "$a" ]; then printf '%s\t%s\tUNREADABLE\n' "$a" 0 >> "$SCAN"; TOTAL=-1; continue; fi
    c="$(grep -a -o -F -- "$v" "$a" 2>/dev/null | wc -l)"; grc=$?
    if [ "$grc" -gt 1 ]; then printf '%s\t0\tTOOL-ERROR(rc=%s)\n' "$a" "$grc" >> "$SCAN"; HITS=$((HITS+1)); continue; fi
    [ "$c" -gt 0 ] && { HITS=$((HITS+c)); printf '%s\t%s\tHIT\n' "$a" "$c" >> "$SCAN"; }
  done
done < "$TMPD/patterns.sorted.tsv"
# 去重：每个 artifact 记一行汇总
: > "$TMPD/scan.summary.tsv"
for a in "${ARTIFACTS[@]}"; do
  if [ ! -r "$a" ]; then printf '%s\t0\tUNREADABLE\n' "$a" >> "$TMPD/scan.summary.tsv"; continue; fi
  s=$(awk -F'\t' -v A="$a" '$1==A {t+=$2} END{print t+0}' "$TMPD/scan.tsv")
  st=CLEAN; [ "$s" -gt 0 ] && st=HIT
  grep -q "$a.*TOOL-ERROR" "$TMPD/scan.tsv" 2>/dev/null && st=TOOL-ERROR
  printf '%s\t%s\t%s\n' "$a" "$s" "$st" >> "$TMPD/scan.summary.tsv"
  [ "$st" = "UNREADABLE" ] || [ "$st" = "TOOL-ERROR" ] && TOTAL=-1
done

RC=0
[ "$HITS" -gt 0 ] && RC=1
[ "$TOTAL" -eq -1 ] && RC=1

echo "patterns_checked=$PATTERNS artifacts=${#ARTIFACTS[@]} hits=$HITS rc=$RC"
cat "$TMPD/scan.summary.tsv"
awk -F'\t' '{printf "pattern key=%s form=%s len=%s sha12=%s policy_ok=%s\n", $1,$2,$3,$4,$5}' "$META"

if [ -n "$OUT_JSON" ]; then
  PATTERNS="$PATTERNS" HITS="$HITS" RC="$RC" ENV_FILE="$ENV_FILE" \
  python3 - "$TMPD/meta.tsv" "$TMPD/scan.summary.tsv" "$OUT_JSON" <<'PY'
import json,sys,os
meta_f, scan_f, out = sys.argv[1], sys.argv[2], sys.argv[3]
fp=[]
for line in open(meta_f):
    p=line.rstrip("\n").split("\t")
    if len(p)>=5: fp.append({"key":p[0],"form":p[1],"len":int(p[2]),"sha12":p[3],"policy_ok":p[4]=="true"})
arts=[]
for line in open(scan_f):
    p=line.rstrip("\n").split("\t")
    if len(p)>=3: arts.append({"artifact":p[0],"hits":int(p[1]),"status":p[2]})
doc={"scanner":"secret-scan-v3","env_file":os.environ["ENV_FILE"],
     "patterns_checked":int(os.environ["PATTERNS"]),"artifacts":arts,
     "hits":int(os.environ["HITS"]),"rc":int(os.environ["RC"]),
     "verdict":"CLEAN" if int(os.environ["RC"])==0 else "LEAK_OR_ERROR",
     "pattern_fingerprints":fp,
     "policy_warnings":[f["key"] for f in fp if not f["policy_ok"]],
     "checked_utc":__import__("datetime").datetime.utcnow().strftime("%Y-%m-%dT%H:%M:%SZ")}
json.dump(doc, open(out,"w"), indent=2, ensure_ascii=False)
PY
fi

rm -rf "$TMPD"
exit "$RC"
