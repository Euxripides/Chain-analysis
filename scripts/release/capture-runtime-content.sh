#!/usr/bin/env bash
# capture-runtime-content.sh <LABEL> <OUTDIR>
# Deterministic content-level snapshot of job-runtime tables (rotation parity proof).
# Read-only. Writes gzipped TSV dumps + sha256 into OUTDIR. No secrets touched.
set -uo pipefail
LABEL="$1"; OUT="$2"
mkdir -p "$OUT"; chmod 700 "$OUT"
cd /home/euripides/projects/blockchain-platform || exit 90
PGP=$(python3 -c "
import re,pathlib
t=pathlib.Path('deploy/.env').read_text()
m=re.search(r'^POSTGRES_PASSWORD=(.*)',t,re.M); print(m.group(1).strip())")

COLS_JSON="$OUT/columns-$LABEL.json"
echo "{" > "$COLS_JSON"
first=1
for t in download_jobs download_shards checkpoints outbox_events platform_jobs backup_jobs job_event_log; do
  case "$t" in
    checkpoints) pkol=shard_id ;;
    *) pkol=id ;;
  esac
  cols=$(printf "SELECT string_agg(column_name,',' ORDER BY ordinal_position) FROM information_schema.columns WHERE table_name='%s';\n" "$t" | \
    PGPASSWORD="$PGP" psql -h 127.0.0.1 -p 5433 -U blockchain -d blockchain_platform -v ON_ERROR_STOP=1 -At)
  [ -n "$cols" ] || { echo "FATAL: no columns for $t"; exit 91; }
  [ $first -eq 1 ] || echo "," >> "$COLS_JSON"
  first=0
  printf '"%s": {"columns": "%s", "pk": "%s"}' "$t" "$cols" "$pkol" >> "$COLS_JSON"
  PGPASSWORD="$PGP" psql -h 127.0.0.1 -p 5433 -U blockchain -d blockchain_platform -v ON_ERROR_STOP=1 -At -c \
    "COPY (SELECT $cols FROM $t ORDER BY $pkol) TO STDOUT" 2>"$OUT/$t.err" | gzip -9 > "$OUT/$t.tsv.gz"
  rc=$?
  if [ "$rc" -ne 0 ]; then echo "FATAL: dump $t rc=$rc"; sed -n '1,5p' "$OUT/$t.err"; exit 92; fi
done
echo "}" >> "$COLS_JSON"
{
  echo "# label=$LABEL captured_utc=$(date -u +%Y-%m-%dT%H:%M:%SZ)"
  for f in "$OUT"/*.tsv.gz; do
    b=$(basename "$f" .tsv.gz)
    rows=$(zcat "$f" | wc -l)
    echo "table=$b rows=$rows content_sha256=$(zcat "$f" | sha256sum | cut -d' ' -f1)"
  done
} > "$OUT/content-$LABEL.txt"
cat "$OUT/content-$LABEL.txt"
