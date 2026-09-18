#!/usr/bin/env bash
# secret-scan.sh — **按值**的明文凭据扫描器（fail-closed），供 release 流程统一调用。
#
# 为什么单独成脚本（2026-09-13 v2）：
#   build-and-freeze.sh 原实现把扫描计数器放在 "{ ... } | tee" 的大括号块里，
#   大括号块在 **pipeline 子 shell** 中执行，SCAN_FAIL=1 不回传主 shell，
#   最终 exit 门槛读到的 SCAN_FAIL 恒为 0 → **假 PASS**。
#   同类缺陷在后验脚本 v1/v2 中亦存在。本脚本把状态聚合放在主 shell，
#   并把"读取失败 / pattern 数不足 / 工具缺失"一律视为**失败**，绝不默认为 0 hits。
#
# 口径：
#   * 从 env 文件按**键名**取真实凭据值；再从 DSN 中解析嵌入式口令；
#   * 对每个产物做 grep -a -o -F 逐值计数（按值，不是按 KEY= 模式）；
#   * 任一产物命中 → exit 1；
#   * env 文件不可读 / 必需键缺失 / pattern 总数 < MIN_PATTERNS / grep 不可用 → exit 2/3；
#   * 所有产出（JSON + 文本）不含任何明文值，只含长度与 sha256 前 12 位。
#
# 用法：secret-scan.sh <env-file> <artifact> [artifact...]
#   MIN_PATTERNS=3 REQUIRED_KEYS="POSTGRES_PASSWORD CLICKHOUSE_PASSWORD MINIO_ROOT_PASSWORD"
#   OPTIONAL_KEYS="DATABASE_URL MIGRATION_DATABASE_URL CLICKHOUSE_URL GPT_ACTIONS_API_KEY ..."
#   OUT_JSON=/path/result.json
set -uo pipefail

ENV_FILE="${1:-}"; shift || true
ARTIFACTS=("$@")
MIN_PATTERNS="${MIN_PATTERNS:-3}"
REQUIRED_KEYS="${REQUIRED_KEYS:-POSTGRES_PASSWORD CLICKHOUSE_PASSWORD MINIO_ROOT_PASSWORD}"
OPTIONAL_KEYS="${OPTIONAL_KEYS:-DATABASE_URL MIGRATION_DATABASE_URL CLICKHOUSE_URL GPT_ACTIONS_API_KEY GPT_ACTIONS_ADMIN_KEY MCP_API_KEY ANKR_API_KEY CHAINBASE_API_KEY TRONGRID_API_KEY ANKR_MANAGEMENT_ENCRYPTION_KEY}"
OUT_JSON="${OUT_JSON:-}"

die() { echo "secret-scan: $*" >&2; exit "$1"; }
[ -n "$ENV_FILE" ] || die 2 "usage: secret-scan.sh <env-file> <artifact> [artifact...]"
[ "${#ARTIFACTS[@]}" -gt 0 ] || die 2 "no artifacts given"
[ -r "$ENV_FILE" ] || die 2 "env file not readable: $ENV_FILE"
command -v grep >/dev/null 2>&1 || die 3 "grep not available"

umask 077
TMPD="$(mktemp -d)"
PAT="$TMPD/patterns.txt"; : > "$PAT"
META="$TMPD/meta.txt"; : > "$META"

# --- 1. 按键名取明文值 ---
env_value() { # env_value <KEY> -> stdout value
  awk -v k="$1" -F= 'index($0, k"=")==1 {print substr($0, length(k)+2); exit}' "$ENV_FILE"
}
MISSING_REQUIRED=""
for k in $REQUIRED_KEYS; do
  v="$(env_value "$k")"
  if [ -z "$v" ]; then MISSING_REQUIRED="$MISSING_REQUIRED $k"; continue; fi
  printf '%s\n' "$v" >> "$PAT"
  printf 'key=%s len=%s sha12=%s\n' "$k" "${#v}" "$(printf '%s' "$v" | sha256sum | cut -c1-12)" >> "$META"
done
if [ -n "$MISSING_REQUIRED" ]; then
  echo "required credential keys missing/empty in $ENV_FILE:$MISSING_REQUIRED" >&2
  die 2 "refusing to report 0 hits with incomplete pattern set"
fi

# --- 2. DSN 中嵌入式口令 ---
for k in $OPTIONAL_KEYS; do
  raw="$(env_value "$k")"
  [ -z "$raw" ] && continue
  case "$raw" in
    *"://"*)
      pw="$(printf '%s' "$raw" | sed -E 's#^[a-zA-Z0-9+.-]+://([^:@/]+):([^@]*)@.*#\2#')"
      case "$pw" in *"://"*) pw="" ;; esac
      [ -n "$pw" ] && printf '%s\n' "$pw" >> "$PAT" && \
        printf 'key=%s(dsn-embedded) len=%s sha12=%s\n' "$k" "${#pw}" "$(printf '%s' "$pw" | sha256sum | cut -c1-12)" >> "$META"
      ;;
    *)
      printf '%s\n' "$raw" >> "$PAT"
      printf 'key=%s len=%s sha12=%s\n' "$k" "${#raw}" "$(printf '%s' "$raw" | sha256sum | cut -c1-12)" >> "$META"
      ;;
  esac
done

# 去重 / 去掉过短值（避免噪声），并记录被跳过数量
SORTED="$TMPD/sorted.txt"
grep -v '^[[:space:]]*$' "$PAT" | awk 'length($0)>=8' | sort -u > "$SORTED"
SKIPPED=$(( $(grep -c . "$PAT" || true) - $(grep -c . "$SORTED" || true) ))
PATTERNS="$(grep -c . "$SORTED" || true)"
[ "$PATTERNS" -ge "$MIN_PATTERNS" ] || die 2 "pattern count $PATTERNS < MIN_PATTERNS $MIN_PATTERNS (parse did not cover credential set)"

# --- 3. 扫描（主 shell 聚合，绝不放进 pipeline 子 shell） ---
TOTAL=0; HITS=0; RC=1
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
[ "$TOTAL" -eq 0 ] && RC=0
[ "$HITS" -gt 0 ] && RC=1
case "$TOTAL" in -1) RC=1 ;; esac

echo "patterns_checked=$PATTERNS skipped_short_or_empty=$SKIPPED artifacts=${#ARTIFACTS[@]} hits=$HITS rc=$RC"
cat "$TMPD/scan.txt"
sed 's/^/pattern /' "$META"

# --- 4. 机器可读结果（不含明文） ---
if [ -n "$OUT_JSON" ]; then
  {
    printf '{\n'
    printf '  "env_file": "%s",\n' "$ENV_FILE"
    printf '  "patterns_checked": %s,\n' "$PATTERNS"
    printf '  "skipped_short_or_empty": %s,\n' "$SKIPPED"
    printf '  "artifacts": %s,\n' "${#ARTIFACTS[@]}"
    printf '  "hits": %s,\n' "$HITS"
    printf '  "rc": %s,\n' "$RC"
    printf '  "verdict": "%s",\n' "$([ "$RC" -eq 0 ] && echo CLEAN || echo LEAK_OR_ERROR)"
    printf '  "pattern_fingerprints": ['
    awk '/^pattern /{printf "%s{\"%s\":\"%s\"}", (NR>1?",":""), $2, $4}' "$META"
    printf '],\n'
    printf '  "checked_utc": "%s"\n' "$(date -u +%Y-%m-%dT%H:%M:%SZ)"
    printf '}\n'
  } > "$OUT_JSON"
fi

rm -rf "$TMPD"
exit "$RC"
