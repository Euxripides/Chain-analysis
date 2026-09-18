#!/usr/bin/env bash
# run-app-rotation-20260913-v2.sh
# Thin wrapper: pins the evidence directory explicitly, then invokes the frozen body
# by ABSOLUTE path. All variable expansion happens inside bash.
set -uo pipefail
REPO=/home/euripides/projects/blockchain-platform
BODY=$REPO/scripts/release/rotate-app-credential-20260913-v3.sh
TS=$(date -u +%Y%m%dT%H%M%SZ)
EVID=$REPO/acceptance-evidence/dual-system-v1/sprint7/app-credential-rotation-$TS
export APP_ROTATION_EVID=$EVID
echo "wrapper_repo=$REPO"
echo "wrapper_body=$BODY"
echo "wrapper_pinned_EVID=$EVID"
if [ -e "$EVID" ]; then echo "FATAL: pinned EVID already exists, refusing to reuse"; exit 88; fi
if [ ! -f "$BODY" ]; then echo "FATAL: body script missing"; exit 89; fi
echo "wrapper_body_sha256=$(sha256sum "$BODY" | cut -d' ' -f1)"
bash "$BODY"
rc=$?
echo "wrapper_body_exit_rc=$rc"
echo "wrapper_EVID=$EVID"
exit $rc
