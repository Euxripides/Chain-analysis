cd /home/euripides/projects/blockchain-platform
echo "=== *.sh in tests ==="; ls acceptance-evidence/dual-system-v1/tests/*.sh
echo "=== *.mjs / *.js ==="; ls acceptance-evidence/dual-system-v1/tests/*.mjs 2>/dev/null
echo "=== playwright specs ==="; ls frontend/tests/ 2>/dev/null | head -30
echo "=== playwright config ==="; ls frontend/*.config.* 2>/dev/null
echo "=== result files of previous runs ==="
for f in acceptance-evidence/dual-system-v1/tests/*/result.json; do printf "%-70s " "$f"; python3 -c "import json,sys;d=json.load(open('$f'));print(d.get('suite',d.get('name','?')), 'pass=%s fail=%s nv=%s'%(d.get('pass'),d.get('fail'),d.get('not_verified', d.get('notVerified'))))" 2>/dev/null || echo "(unparsed)"; done
echo "=== fixture env ==="; sed 's/=.*/=<redacted>/' /tmp/dual-system-fixture.env