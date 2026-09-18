#!/usr/bin/env bash
# build-manifest-negcal.sh -- negative calibration for the BUILD-MANIFEST
# build_script_sha256 defect fixed in build-and-freeze-v2.sh.
#
# The v1 manifest shipped an EMPTY build_script_sha256 because the self-hash ran
# after "cd $PROJECT/backend" with a relative "$0". This calibration proves both
# halves: the old pattern really produced nothing, and the new pattern produces a
# value that equals the real file hash. Runs entirely in a temp dir; it never
# touches the deployment tree.
# ASCII only.
set -uo pipefail
TMP=$(mktemp -d /tmp/s7-manifest-negcal-XXXXXX)
trap 'echo "tmp dir kept for inspection: $TMP"' EXIT
PASS=0; FAIL=0
ok(){ PASS=$((PASS+1)); echo "PASS[$PASS] $1"; }
bad(){ FAIL=$((FAIL+1)); echo "FAIL[$FAIL] $1"; }
chk(){ if [ "$1" = "$2" ]; then ok "$3"; else bad "$3 (got='$1' want='$2')"; fi; }

mkdir -p "$TMP/proj/scripts/release" "$TMP/proj/backend"
cat > "$TMP/proj/scripts/release/build-and-freeze.sh" <<'EOS'
#!/usr/bin/env bash
set -uo pipefail
PROJECT="${PROJECT:?}"
cd "$PROJECT/backend"
echo "build_script_sha256=$(sha256sum "$0" | cut -d' ' -f1)"
EOS
chmod +x "$TMP/proj/scripts/release/build-and-freeze.sh"

# C1: reproduce v1 -- relative $0 after cd => empty hash, non-zero sha256sum rc.
( cd "$TMP/proj" && PROJECT="$TMP/proj" bash scripts/release/build-and-freeze.sh ) > "$TMP/v1.out" 2> "$TMP/v1.err"
V1RC=$?
V1VAL=$(awk -F= '/^build_script_sha256=/{print $2}' "$TMP/v1.out")
[ "$V1RC" -ne 0 ]; chk "$?" "0" "C1 the v1 pattern really exits non-zero (rc=$V1RC)"
chk "$V1VAL" "" "C1 the v1 pattern really emits an empty build_script_sha256"
grep -q 'No such file' "$TMP/v1.err"; chk "$?" "0" "C1 the v1 failure is a path resolution error, not a silent success"

# C2: the fixed pattern -- absolute script path resolved before cd.
cat > "$TMP/proj/scripts/release/build-and-freeze-v2.sh" <<'EOS'
#!/usr/bin/env bash
set -uo pipefail
SCRIPT_PATH=$(readlink -f "$0")
PROJECT="${PROJECT:?}"
cd "$PROJECT/backend"
echo "build_script_sha256=$(sha256sum "$SCRIPT_PATH" | cut -d' ' -f1)"
EOS
chmod +x "$TMP/proj/scripts/release/build-and-freeze-v2.sh"
( cd "$TMP/proj" && PROJECT="$TMP/proj" bash scripts/release/build-and-freeze-v2.sh ) > "$TMP/v2.out" 2> "$TMP/v2.err"
V2RC=$?
V2VAL=$(awk -F= '/^build_script_sha256=/{print $2}' "$TMP/v2.out")
EXPECT=$(sha256sum "$TMP/proj/scripts/release/build-and-freeze-v2.sh" | cut -d' ' -f1)
chk "$V2RC" "0" "C2 the fixed pattern exits 0 even when invoked by a relative path after a cd"
chk "$V2VAL" "$EXPECT" "C2 the reported hash equals the real script hash"
echo "$V2VAL" | grep -Eq '^[0-9a-f]{64}$'; chk "$?" "0" "C2 the reported value is a 64-hex digest, not a placeholder"

# C3: fail-closed self check -- an empty value must be rejected by the gate.
if [ -z "$V1VAL" ]; then ok "C3 an empty build_script_sha256 is detected as a defect"; else bad "C3 empty value was not detected"; fi
echo "NEGCAL_PASS=$([ "$FAIL" -eq 0 ] && echo 1 || echo 0) case_fails=$FAIL pass=$PASS"
[ "$FAIL" -eq 0 ]
