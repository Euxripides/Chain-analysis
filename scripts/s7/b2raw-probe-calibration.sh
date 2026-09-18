#!/usr/bin/env bash
# Read-only calibration harness for the strict dual-bucket probe (objectprobe stat-strict v3).
#
# Purpose: persist, for every calibration case, the raw output, the exit code, the elapsed time
# and the (secret-free) command that produced it, plus the source/binary hashes and the module-wide
# go test log. Earlier rounds printed some of these values to a console transcript that was not
# written to a file; nothing is back-dated here -- this harness simply re-runs the same read-only
# cases and stores the artefacts with their real timestamps and real exit codes.
#
# It never creates, deletes or modifies any bucket or object: every case only issues ListBuckets /
# StatObject against existing buckets, or fails before any request (absent bucket / bad endpoint).
set -uo pipefail
P=/home/euripides/projects/blockchain-platform
CAL=$P/acceptance-evidence/dual-system-v1/sprint7/b2raw-probe-calibration-20260913T133310Z
PROBE=$CAL/objectprobe-strict-v3
TS=$(date -u +%Y%m%dT%H%M%SZ)
OUT=$CAL/v3-calibration-run-$TS
if [ -e "$OUT" ]; then echo "OUT_EXISTS_FAILCLOSED $OUT"; exit 9; fi
mkdir -p "$OUT"; chmod 700 "$OUT"
exec > >(tee -a "$OUT/calibration.console.log") 2>&1
cd $P
set -a; . deploy/.env; set +a
echo "started=$(date -Is)"
echo "probe=$PROBE"
sha256sum "$PROBE" > "$OUT/probe-sha256.txt"
sha256sum $P/backend/cmd/objectprobe/main.go $P/backend/cmd/objectprobe/statstrict.go $P/backend/cmd/objectprobe/statstrict_test.go > "$OUT/source-sha256.txt"
cat "$OUT/probe-sha256.txt" "$OUT/source-sha256.txt"
cat > "$OUT/commands.txt" <<'EOF'
# Every command below is secret-free: credentials come from deploy/.env via the normal config.Load
# path (MINIO_ROOT_USER / MINIO_ROOT_PASSWORD are never echoed, never written to a file).
K1 list-buckets denied : MINIO_ROOT_USER=<bogus> MINIO_ROOT_PASSWORD=<bogus> PROBE -action=stat-strict -key=<random absent key> -out k1.json
K2 bucket absent       : RAW_BUCKET=<fresh random name> PROBE -action=stat-strict -key=<random absent key> -out k2.json
K3 bucket present+miss : PROBE -action=stat-strict -key=<random absent key> -out k3.json
K4 key present         : PROBE -action=stat-strict -key=<real migrated object_ref> -out k4.json
K5 bounded timeout     : MINIO_ENDPOINT=192.0.2.1:9000 PROBE -action=stat-strict -key=<random absent key> -timeout=2s -out k5.json
EOF
run_case(){
  local tag=$1
  shift
  local start end rc
  start=$(date +%s)
  "$@" > "$OUT/$tag.stdout.txt" 2> "$OUT/$tag.stderr.txt"
  rc=$?
  end=$(date +%s)
  echo "$rc" > "$OUT/$tag.rc"
  echo "$((end-start))" > "$OUT/$tag.elapsed_s"
  printf '%s rc=%s elapsed_s=%s
' "$tag" "$rc" "$((end-start))" | tee -a "$OUT/exit-codes.txt"
  return 0
}
KEY="exports/calib-absent-$RANDOM$RANDOM.csv"
echo "$KEY" > "$OUT/absent-key.txt"
ABSENT="blockchain-absent-calib-$RANDOM$RANDOM"
echo "$ABSENT" > "$OUT/absent-bucket-name.txt"
K4KEY=$(docker exec deploy-postgres-1 psql -q -v ON_ERROR_STOP=1 -U blockchain -d blockchain_platform -tAc "SET ROLE blockchain_app; select object_ref from export_object_migrations where status='VERIFIED' order by bytes desc limit 1")
echo "$K4KEY" > "$OUT/k4-key.txt"

MINIO_ROOT_USER="calibration-bogus-user" MINIO_ROOT_PASSWORD="calibration-bogus-secret"   run_case k1 "$PROBE" -action=stat-strict -key="$KEY" -out "$OUT/k1.json"
RAW_BUCKET="$ABSENT" run_case k2 "$PROBE" -action=stat-strict -key="$KEY" -out "$OUT/k2.json"
run_case k3 "$PROBE" -action=stat-strict -key="$KEY" -out "$OUT/k3.json"
run_case k4 "$PROBE" -action=stat-strict -key="$K4KEY" -out "$OUT/k4.json"
MINIO_ENDPOINT="192.0.2.1:9000" run_case k5 "$PROBE" -action=stat-strict -key="$KEY" -timeout=2s -out "$OUT/k5.json"

echo "=== post-check: the absent calibration bucket must still not exist ==="
docker exec deploy-minio-1 sh -c 'MC_HOST_p="http://$MINIO_ROOT_USER:$MINIO_ROOT_PASSWORD@127.0.0.1:9000" mc ls --json p/ 2>/dev/null' > "$OUT/post-bucket-list.json"
ABSENT_HITS=$(grep -c "$ABSENT" "$OUT/post-bucket-list.json" || true)
echo "absent_bucket_mentions_after=$ABSENT_HITS" | tee "$OUT/post-check.txt"

echo "=== assertion gate ==="
python3 - "$OUT" > "$OUT/assertions.txt" 2>&1 <<'PY'
import json, os, sys
out = sys.argv[1]
ok = [True]
def line(text, good):
    ok[0] = ok[0] and good
    print(("PASS " if good else "FAIL ") + text)
def rc(tag):
    return int(open(os.path.join(out, tag + ".rc")).read().strip())
def load(tag):
    return json.load(open(os.path.join(out, tag + ".json"), encoding="utf-8"))

k1 = load("k1")
line("K1 list-buckets denied -> exit 4", rc("k1") == 4)
line("K1 verdict BUCKET_CHECK_ERROR", k1.get("verdict") == "BUCKET_CHECK_ERROR")
line("K1 list_buckets_authorized false", k1.get("list_buckets_authorized") is False)
line("K1 no results fabricated", k1.get("results") == [])

k2 = load("k2")
raw2 = [r for r in k2["results"] if r["role"] == "raw"][0]
line("K2 absent bucket -> exit 4", rc("k2") == 4)
line("K2 raw classification BUCKET_MISSING", raw2["classification"] == "BUCKET_MISSING")
line("K2 absent bucket is NOT reported as an existing bucket", raw2["bucket_exists_confirmed"] is False)
line("K2 bucket check completed", raw2["bucket_check_completed"] is True)
line("K2 absence confirmed", raw2["bucket_absence_confirmed"] is True)

k3 = load("k3")
line("K3 present bucket + absent key -> exit 0", rc("k3") == 0)
line("K3 both buckets OBJECT_MISSING", all(r["classification"] == "OBJECT_MISSING" for r in k3["results"]))
line("K3 both absences are NoSuchKey", all(r.get("error_code") == "NoSuchKey" for r in k3["results"]))
line("K3 bucket existence confirmed for both", all(r["bucket_exists_confirmed"] is True for r in k3["results"]))

k4 = load("k4")
line("K4 key present -> exit 0", rc("k4") == 0)
line("K4 both buckets OBJECT_PRESENT", all(r["classification"] == "OBJECT_PRESENT" for r in k4["results"]))
line("K4 sizes equal and non-zero", len({r["object_size"] for r in k4["results"]}) == 1 and k4["results"][0]["object_size"] > 0)

k5 = load("k5")
line("K5 bounded timeout -> exit 4", rc("k5") == 4)
line("K5 verdict BUCKET_CHECK_ERROR", k5.get("verdict") == "BUCKET_CHECK_ERROR")
line("K5 error kind is transport/timeout, not a missing object", k5.get("list_buckets_error_kind") == "transport_or_timeout")
line("K5 elapsed < 30s (bounded, did not hang)", int(open(os.path.join(out, "k5.elapsed_s")).read().strip()) < 30)

print("absent_bucket_mentions_after=" + open(os.path.join(out, "post-check.txt")).read().strip())
sys.exit(0 if ok[0] else 1)
PY
ASSERT_RC=$?
cat "$OUT/assertions.txt"
echo "$ASSERT_RC" > "$OUT/assertions.rc"
echo "assertions_rc=$ASSERT_RC"

echo "=== module-wide go test log (recorded from the run started earlier this session) ==="
if [ -s /tmp/b2raw-gotest.log ]; then
  cp -p /tmp/b2raw-gotest.log "$OUT/go-test-all.log"
  tail -1 "$OUT/go-test-all.log"
  grep -c '^ok ' "$OUT/go-test-all.log" | sed 's/^/ok_packages=/'
  grep -c '^FAIL' "$OUT/go-test-all.log" | sed 's/^/fail_lines=/'
  grep '^GO_TEST_RC=' "$OUT/go-test-all.log" || echo "GO_TEST_RC_MISSING"
else
  echo "GO_TEST_LOG_ABSENT" | tee "$OUT/go-test-all.log"
fi

echo "finished=$(date -Is)"
if [ "$ASSERT_RC" -eq 0 ] && [ "$ABSENT_HITS" = "0" ]; then
  echo "STRICT_PROBE_CALIBRATION=PASS"
  exit 0
fi
echo "STRICT_PROBE_CALIBRATION=FAIL"
exit 6
