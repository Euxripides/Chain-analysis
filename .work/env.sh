#!/usr/bin/env bash
set -uo pipefail
echo "== fixture env =="
grep -E '^(FIXTURE_WORKSPACE_ID|INVESTIGATOR_PASSWORD|ADMIN_PASSWORD)=' /tmp/dual-system-fixture.env 2>/dev/null | sed 's/=.*/=<set>/'
echo "== deploy/.env keys =="
grep -oE '^[A-Z_]+=' /home/euripides/projects/blockchain-platform/deploy/.env | tr -d '=' | tr '\n' ' '
echo
echo "== E2E perf env =="
grep -E 'E2E_S5_10K_(ADDRESS|CHAIN)|E2E_S5_10K' /tmp/*.sh /tmp/*.env 2>/dev/null | head -5
echo "== loadprobe =="
ls -la /home/euripides/projects/blockchain-platform/backend/bin/loadprobe 2>&1
