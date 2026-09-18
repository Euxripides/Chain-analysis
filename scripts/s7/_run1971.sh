cd /home/euripides/projects/blockchain-platform
set -a; . deploy/.env; set +a
. /tmp/s6-e2e-env.sh
BASE=http://127.0.0.1:8080
J=$(mktemp); JA=$(mktemp)
login() { curl -s -c "$1" -H 'Content-Type: application/json' -H "X-Auth-Audience: $4" -d "{\"identifier\":\"$2\",\"password\":\"$3\",\"audience\":\"$4\"}" "$BASE/api/auth/v1/login" >/dev/null; }
csrf() { grep -i 'csrf' "$1" | awk '{print $7}'; }
login "$J" admin@local "$ADMIN_PASSWORD" admin
CS=$(csrf "$J")
echo "=== 1) audit/export 不再是 501 占位 ==="
printf "no-reason  -> "; curl -s -o /tmp/o.json -w '%{http_code} ' -b "$J" -H "X-CSRF-Token: $CS" -H 'Content-Type: application/json' -X POST -d '{}' "$BASE/api/admin/v1/audit/export"; head -c 160 /tmp/o.json; echo
echo "=== 2) provider 命令按权限精确鉴权 ==="
login "$JA" fixture-auditor@fixture.local "$AUDITOR_PASSWORD" admin
CSA=$(csrf "$JA")
PROV=$(curl -s -b "$J" "$BASE/api/admin/v1/providers" | python3 -c "import sys,json;d=json.load(sys.stdin);items=(d.get('data') or {}).get('items') or [];print(items[0].get('key','') if items else '')" 2>/dev/null)
echo "provider key = $PROV"
printf "auditor rotate-secret -> "; curl -s -o /tmp/r.json -w '%{http_code} ' -b "$JA" -H "X-CSRF-Token: $CSA" -H 'Content-Type: application/json' -X POST -d '{"secret_ref":"env:X","step_up_password":"x"}' "$BASE/api/admin/v1/providers/$PROV/commands/rotate-secret"; head -c 200 /tmp/r.json; echo
printf "auditor test          -> "; curl -s -o /tmp/t.json -w '%{http_code} ' -b "$JA" -H "X-CSRF-Token: $CSA" -H 'Content-Type: application/json' -X POST -d '{"reason":"sec-test"}' "$BASE/api/admin/v1/providers/$PROV/commands/test"; head -c 160 /tmp/t.json; echo
printf "owner  rotate-secret(no stepup) -> "; curl -s -o /tmp/o2.json -w '%{http_code} ' -b "$J" -H "X-CSRF-Token: $CS" -H 'Content-Type: application/json' -X POST -d '{"secret_ref":"env:X"}' "$BASE/api/admin/v1/providers/$PROV/commands/rotate-secret"; head -c 200 /tmp/o2.json; echo
echo SCRIPT_END
