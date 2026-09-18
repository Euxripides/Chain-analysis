#!/usr/bin/env bash
# secret-scan-v2.sh — fail-closed 按值凭据扫描器（v1 的修正版）。
#
# v1 = secret-scan.sh sha256 1fae56f99f37e51e860456824634dc5873d66840c015a84af86c14e65f81365e
#   真实缺陷：pattern_fingerprints 用 awk '/^pattern /...' 匹配 META，
#   而 META 行前缀是 "key="（"pattern " 只是 stdout 的前缀），故该字段恒为空数组，
#   机器可读结果**没有记录实际检查了哪些凭据键**（覆盖度自证缺口）。v1 未修改，其输出保留。
# 本版只修该字段；其余逻辑与 v1 一致：
#   * 从 env 文件按键名取真实凭据值；再从 DSN 中解析嵌入式口令；
#   * 对每个产物做 grep -a -o -F 逐值计数（按值，不是按 KEY= 模式）；
#   * 任一产物命中 → exit 1；env 不可读/必需键缺失/pattern 数不足/grep 缺失 → exit 2/3；
#   * 状态聚合在**主 shell**（不用管道子 shell），绝不把失败吞成 0 hits PASS。
set -uo pipefail

ENV_FILE="${1:-}"; shift || true
ARTIFACTS=("$@")
MIN_PATTERNS="${MIN_PATTERNS:-3}"
REQUIRED_KEYS="${REQUIRED_KEYS:-POSTGRES_PASSWORD CLICKHOUSE_PASSWORD MINIO_ROOT_PASSWORD}"
OPTIONAL_KEYS="${OPTIONAL_KEYS:-DATABASE_URL MIGRATION_DATABASE_URL CLICKHOUSE_URL GPT_ACTIONS_API_KEY GPT_ACTIONS_ADMIN_KEY MCP_API_KEY ANKR_API_KEY CHAINBASE_API_KEY TRONGRID_API_KEY ANKR_MANAGEMENT_ENCRYPTION_KEY}"
OUT_JSON="${OUT_JSON:-}"

die() { echo "secret-scan: $*" >&2; exit "$1"; }
[ -n "$ENV_FILE" ] || die 2 "usage: secret-scan-v2.sh <env-file> <artifact> [artifact...]"
[ "${#ARTIFACTS[@]}" -gt 0 ] || die 2 "no artifacts given"
[ -r "$ENV_FILE" ] || die 2 "env file not readable: $ENV_FILE"
command -v grep >/dev/null 2>&1 || die 3 "grep not available"

umask 077
TMPD="$(mktemp -d)"
PAT="$TMPD/patterns.txt"; : > "$PAT"
META="$TMPD/meta.txt"; : > "$META"

env_value() { awk -v k="$1" -F= 'index($0, k"=")==1 {print substr($0, length(k)+2); exit}' "$ENV_FILE"; }

MISSING_REQUIRED=""
for k in $REQUIRED_KEYS; do
  v="$(env_value "$k")"
  if [ -z "$v" ]; then MISSING_REQUIRED="$MISSING_REQUIRED $k"; continue; fi
  printf '%s\n' "$v" >> "$PAT"
  printf 'key=%s len=%s sha12=%s\n' "$k" "${#v}" "$(printf '%s' "$v" | sha256sum | cut -c1-12)" >> "$META"
done
[ -n "$MISSING_REQUIRED" ] && die 2 "required credential keys missing/empty in $ENV_FILE:$MISSING_REQUIRED"

for k in $OPTIONAL_KEYS; do
  raw="$(env_value "$k")"
  [ -z "$raw" ] && continue
  case "$raw" in
    *"://"*)
      pw="$(printf '%s' "$raw" | sed -E 's#^[a-zA-Z0-9+.-]+://([^:@/]+):([^@]*)@.*#\2#')"
      case "$pw" in *"://"*) pw="" ;; esac
      if [ -n "$pw" ]; then
        printf '%s\n' "$pw" >> "$PAT"
        printf 'key=%s len=%s sha12=%s\n' "$k" "${#pw}" "$(printf '%s' "$pw" | sha256sum | cut -c1-12)" >> "$META"
      fi
      ;;
    *)
      printf '%s\n' "$raw" >> "$PAT"
      printf 'key=%s len=%s sha12=%s\n' "$k" "${#raw}" "$(printf '%s' "$raw" | sha256sum | cut -c1-12)" >> "$META"
      ;;
  esac
done

SORTED="$TMPD/sorted.txt"
grep -v '^[[:space:]]*$' "$PAT" | awk 'length($0)>=8' | sort -u > "$SORTED"
SKIPPED=$(( $(grep -c . "$PAT" || true) - $(grep -c . "$SORTED" || true) ))
PATTERNS="$(grep -c . "$SORTED" || true)"
[ "$PATTERNS" -ge "$MIN_PATTERNS" ] || die 2 "pattern count $PATTERNS < MIN_PATTERNS $MIN_PATTERNS (parse did not cover credential set)"

TOTAL=0; HITS=0
: > "$TMPD/scan.txt"
for a in "${ARTIFACTS[@]}"; do
  if [ ! -r "$a" ]; then echo "ARTIFACT UNREADABLE $a" >> "$TMPD/scan.txt"; HITS=$((HITS+1)); TOTAL=-1; continue; fi
  n=0
  while IFS= read -r v; do
    [ -z "$v" ] && continue
    c="$(grep -a -o -F -- "$v" "$a" 2>/dev/null | wc -l)"
    grc=$?
    if [ "$grc" -gt 1 ]; then echo "SCAN-TOOL-ERROR artifact=$a rc=$grc" >> "$TMPD/scan.txt"; HITS=$((HITS+1)); continue; fi
    n=$((n+c))
  done < "$SORTED"
  if [ "$n" -gt 0 ]; then HITS=$((HITS+n)); echo "ARTIFACT HIT $a hits=$n" >> "$TMPD/scan.txt"
  else echo "ARTIFACT CLEAN $a hits=0" >> "$TMPD/scan.txt"; fi
done
RC=0
[ "$HITS" -gt 0 ] && RC=1
[ "$TOTAL" -eq -1 ] && RC=1

echo "patterns_checked=$PATTERNS skipped_short_or_empty=$SKIPPED artifacts=${#ARTIFACTS[@]} hits=$HITS rc=$RC"
cat "$TMPD/scan.txt"
sed 's/^/pattern /' "$META"

if [ -n "$OUT_JSON" ]; then
  {
    printf '{\n'
    printf '  "scanner": "secret-scan-v2",\n'
    printf '  "env_file": "%s",\n' "$ENV_FILE"
    printf '  "patterns_checked": %s,\n' "$PATTERNS"
    printf '  "skipped_short_or_empty": %s,\n' "$SKIPPED"
    printf '  "artifacts": %s,\n' "${#ARTIFACTS[@]}"
    printf '  "hits": %s,\n' "$HITS"
    printf '  "rc": %s,\n' "$RC"
    printf '  "verdict": "%s",\n' "$([ "$RC" -eq 0 ] && echo CLEAN || echo LEAK_OR_ERROR)"
    printf '  "pattern_fingerprints": ['
    awk '/^key=/{n++; split($1,a,"="); split($3,b,"="); printf "%s{\"%s\":\"%s\"}", (n>1?",":""), a[2], b[2]}' "$META"
    printf '],\n'
    printf '  "checked_utc": "%s"\n' "$(date -u +%Y-%m-%dT%H:%M:%SZ)"
    printf '}\n'
  } > "$OUT_JSON"
fi

rm -rf "$TMPD"
exit "$RC"
