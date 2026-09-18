#!/usr/bin/env bash
# Shared bounded, rc-accounted helpers for the B2 raw-only UI round and its error-path calibration.
# Sourced (never executed) by:
#   scripts/s7/b2-raw-only-ui-sample-v2.sh
#   scripts/s7/b2-raw-helper-error-calibration.sh
# EV (evidence directory) must be set and writable before sourcing.
#
# WHY the rc is written to a FILE instead of a shell variable:
#   every call site is a command substitution, which runs in a SUBSHELL. A counter incremented
#   inside the helper is lost when the subshell exits, so the parent keeps reading 0 while helper
#   failures actually happened -- a real defect found by review on 2026-09-13. Appending each rc to
#   a persisted file survives the subshell, and both the preflight and the final PASS gate require
#   "no non-zero rc recorded anywhere" plus "the logs are empty", so a failed query can never be
#   silently compared as empty-equals-empty.
#
# WHY every call is bounded:
#   the first version of this helper had NO connect/statement/exec timeout. An unreachable database
#   then hung for minutes (real incident 2026-09-13: the client had to be terminated manually), so
#   bounded execution is now enforced in three layers for PG -- PGCONNECT_TIMEOUT (client connect),
#   statement_timeout via PGOPTIONS (server side, this connection only) and an outer timeout(1)
#   around docker exec (container/exec layer) -- and one layer for HTTP (--max-time).
#
# Overridable knobs (used by the calibration to force a negative control through THIS helper):
#   B2RAW_PG_HOST     host passed to psql; empty means the in-container server over the unix socket
#   B2RAW_PG_PORT     port passed to psql when B2RAW_PG_HOST is set
#   B2RAW_PG_TIMEOUT  outer timeout in seconds for one PG call (default 20)
#   B2RAW_CURL_TIMEOUT max-time in seconds for one HTTP call (default 120)
# They are initialised here so that set -u callers are safe; a caller may override them at any time
# before the call (the helper reads them at call time).
B2RAW_PG_HOST=""
B2RAW_PG_PORT="5432"
B2RAW_PG_TIMEOUT="20"
B2RAW_CURL_TIMEOUT="120"

# pgq <sql> : echoes the query result; returns the RAW rc of the bounded psql invocation.
pgq(){
  local out rc
  if [ -n "$B2RAW_PG_HOST" ]; then
    out=$(timeout "$B2RAW_PG_TIMEOUT" docker exec -e PGCONNECT_TIMEOUT=5 -e "PGOPTIONS=-c statement_timeout=15000" deploy-postgres-1 psql -q -v ON_ERROR_STOP=1 -h "$B2RAW_PG_HOST" -p "$B2RAW_PG_PORT" -U blockchain -d blockchain_platform -tAc "$1" 2>>"$EV/pg-errors.log")
  else
    out=$(timeout "$B2RAW_PG_TIMEOUT" docker exec -e PGCONNECT_TIMEOUT=5 -e "PGOPTIONS=-c statement_timeout=15000" deploy-postgres-1 psql -q -v ON_ERROR_STOP=1 -U blockchain -d blockchain_platform -tAc "$1" 2>>"$EV/pg-errors.log")
  fi
  rc=$?
  printf '%s\n' "$rc" >> "$EV/pg-rcs.txt"
  printf '%s' "$out"
  return $rc
}

# httpc <tag> <outfile> <curl args...> : echoes the HTTP status code (000 on a transport failure);
# returns the RAW curl rc.
httpc(){
  local tag outfile code rc
  tag=$1
  outfile=$2
  shift 2
  code=$(curl -sS --max-time "$B2RAW_CURL_TIMEOUT" --connect-timeout 10 -o "$outfile" -w '%{http_code}' "$@" 2>>"$EV/curl-errors.log")
  rc=$?
  printf '%s\n' "$rc" >> "$EV/curl-rcs.txt"
  printf '%s' "$code"
  return $rc
}

# helper_counts echoes: <pg_total> <pg_bad> <curl_total> <curl_bad>
helper_counts(){
  local pg_total=0 pg_bad=0 curl_total=0 curl_bad=0
  if [ -f "$EV/pg-rcs.txt" ]; then
    pg_total=$(grep -c . "$EV/pg-rcs.txt" || true)
    pg_bad=$(grep -cv '^0$' "$EV/pg-rcs.txt" || true)
  fi
  if [ -f "$EV/curl-rcs.txt" ]; then
    curl_total=$(grep -c . "$EV/curl-rcs.txt" || true)
    curl_bad=$(grep -cv '^0$' "$EV/curl-rcs.txt" || true)
  fi
  printf '%s %s %s %s' "$pg_total" "$pg_bad" "$curl_total" "$curl_bad"
}

# helper_log_lines echoes the number of lines in the persisted error logs.
helper_log_lines(){
  local pg=0 curl=0
  if [ -f "$EV/pg-errors.log" ]; then pg=$(grep -c . "$EV/pg-errors.log" || true); fi
  if [ -f "$EV/curl-errors.log" ]; then curl=$(grep -c . "$EV/curl-errors.log" || true); fi
  printf '%s %s' "$pg" "$curl"
}
