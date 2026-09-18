#!/usr/bin/env bash
set -uo pipefail
V=/home/euripides/projects/blockchain-platform
echo "=== grep stage5 in /tmp and scripts ==="
ls -la /tmp/*stage5* /tmp/*deploy* 2>/dev/null | head -20
grep -rl "stage5" "$V/scripts" "$V/acceptance-evidence/dual-system-v1/sprint7/security/dependency-remediation-20260913T014342Z" 2>/dev/null | head -20
echo "=== deploy-postverify-strict-v2.sh head ==="
sed -n '1,60p' "$V/scripts/release/deploy-postverify-strict-v2.sh"
echo "=== its sha ==="
sha256sum "$V/scripts/release/deploy-postverify-strict-v2.sh"
echo "=== done ==="
