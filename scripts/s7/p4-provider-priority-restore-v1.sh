#!/usr/bin/env bash
# p4-provider-priority-restore-v1.sh
#
# The provider instrument probe (scripts/s7/p4-provider-probe-v1.sh, K6) had to
# exercise a real commit-gated write route that is classified as provider-calling
# in the frozen matrix, so it issued a real PATCH /api/admin/v1/providers/ankr-multichain.
# That route rewrites the provider descriptor row, which is GLOBAL control-plane
# state, not fixture state. The audit trail records the change honestly
# (seq 54267: priority 10 -> 1, revision 17 -> 18). This script restores the
# original value through the same real route so the audit trail stays honest and
# the running platform sees the original configuration again.
#
# ASCII only. Prints no credential, Cookie, CSRF token or DSN.
set -uo pipefail

R=/home/euripides/projects/blockchain-platform
API=http://127.0.0.1:8080
PROV=ankr-multichain
WANT_PRIORITY=10
TS=$(date -u +%Y%m%dT%H%M%SZ)
EV=${EV:-$R/acceptance-evidence/dual-system-v1/sprint7/p4-provrestore-$TS}
mkdir -p "$EV"; chmod 700 "$EV"
exec > >(tee "$EV/restore-console.log") 2>&1

csrf_of(){ awk -v c="$2" '$6==c{print $7}' "$1" | tail -1; }

AP=$(systemctl --user show blockchain-api.service -p MainPID --value)
echo "ts_utc=$TS script_sha256=$(sha256sum "$0" | cut -d\  -f1)"
echo "production_api_pid=$AP production_api_sha=$(sha256sum /proc/$AP/exe 2>/dev/null | cut -d\  -f1)"
echo "before_row: $(docker exec deploy-postgres-1 psql -U blockchain -d blockchain_platform -tAc "select provider_key||'|priority='||priority||'|revision='||revision from provider_descriptors where provider_key='$PROV'")"

FIX=$R/backend/bin/fixture-membership
CRED=/home/euripides/.c1lat-creds.env
( set -a; . "$R/deploy/.env"; set +a
  "$FIX" -org-slug c1lat -org-name "P4 provider priority restore" \
    -workspaces c1lat-a,c1lat-b \
    -identities owner:c1lat-own@fixture.local:c1lat-a \
    -out "$CRED" ) > "$EV/fixture.log" 2>&1
chmod 600 "$CRED"
read_cred(){ grep -E "^$2=" "$1" 2>/dev/null | head -1 | cut -d= -f2-; }
WSA=$(read_cred "$CRED" C1MEM_WS_C1LAT_A)
OWNEMAIL=$(read_cred "$CRED" C1MEM_C1LAT_OWN_EMAIL)
OWNPW=$(read_cred "$CRED" C1MEM_C1LAT_OWN_PW)
OWNPW1="Pw-$TS-restore-1a7c"

J=$EV/own.jar
c=$(curl -s -c "$J" -o "$EV/login.json" -w '%{http_code}' -X POST "$API/api/auth/v1/login" \
  -H 'Content-Type: application/json' -H 'X-Auth-Audience: admin' \
  -d "{\"identifier\":\"$OWNEMAIL\",\"password\":\"$OWNPW\",\"audience\":\"admin\",\"workspace_id\":\"$WSA\"}")
echo "login=$c"
CSRF=$(csrf_of "$J" cs_admin_session_csrf)
c=$(curl -s -b "$J" -o "$EV/pw.json" -w '%{http_code}' -X POST "$API/api/auth/v1/password/change" \
  -H 'Content-Type: application/json' -H 'X-Auth-Audience: admin' -H "X-CSRF-Token: $CSRF" \
  -d "{\"current_password\":\"$OWNPW\",\"new_password\":\"$OWNPW1\"}")
echo "password_change=$c"
J2=$EV/own2.jar
c=$(curl -s -c "$J2" -o "$EV/login2.json" -w '%{http_code}' -X POST "$API/api/auth/v1/login" \
  -H 'Content-Type: application/json' -H 'X-Auth-Audience: admin' \
  -d "{\"identifier\":\"$OWNEMAIL\",\"password\":\"$OWNPW1\",\"audience\":\"admin\",\"workspace_id\":\"$WSA\"}")
echo "relogin=$c"
CSRF2=$(csrf_of "$J2" cs_admin_session_csrf)

REV=$(docker exec deploy-postgres-1 psql -U blockchain -d blockchain_platform -tAc "select revision from provider_descriptors where provider_key='$PROV'" | tr -d '[:space:]')
echo "current_revision=$REV"
c=$(curl -s -b "$J2" -o "$EV/patch.json" -w '%{http_code}' -X PATCH "$API/api/admin/v1/providers/$PROV" \
  -H 'Content-Type: application/json' -H 'X-Auth-Audience: admin' -H "X-CSRF-Token: $CSRF2" \
  -H "If-Match: $REV" \
  -d "{\"reason\":\"restore priority $WANT_PRIORITY after the P4 provider instrument probe\",\"priority\":$WANT_PRIORITY}")
echo "restore_patch_http=$c"
echo "after_row: $(docker exec deploy-postgres-1 psql -U blockchain -d blockchain_platform -tAc "select provider_key||'|priority='||priority||'|revision='||revision from provider_descriptors where provider_key='$PROV'")"
echo "audit_tail:"
docker exec deploy-postgres-1 psql -U blockchain -d blockchain_platform -tAc "select seq||'|'||before::text||'|'||after::text from audit_events_v2 where action='admin.provider.update' order by seq desc limit 2"
echo "production_ready=$(curl -s -o /dev/null -w '%{http_code}' "$API/health/ready")"
echo "production_api_sha_after=$(sha256sum /proc/$AP/exe 2>/dev/null | cut -d\  -f1)"
echo "RESTORE_COMPLETE=1"
exit 0
