#!/bin/bash
EV="/home/euripides/projects/blockchain-platform/acceptance-evidence/dual-system-v1/sprint7/security-evidence-20260912T223257Z"
D="$EV/iac"; mkdir -p "$D"
exec > "$D/systemd-and-services.txt" 2>&1
set -x
echo "=== hostname/date ==="; hostname; date -u +%Y-%m-%dT%H:%M:%SZ
echo "=== systemctl list-unit-files (blockchain) ==="
timeout 60 systemctl list-unit-files 2>&1 | grep -i blockchain
echo "=== is-active ==="
for u in blockchain-api blockchain-worker blockchain-admin-console blockchain-user-console blockchain-vite blockchain-mcp blockchain-e2e-tls blockchain-containers-watchdog; do printf "%-42s %s\n" "$u" "$(timeout 10 systemctl is-active $u.service 2>&1)"; done
echo "=== exit-codes: systemctl is-active returns non-zero for inactive ==="
echo "=== deployed unit files ==="
ls -la /etc/systemd/system/ 2>&1 | grep -i blockchain
echo "=== unit file hashes (deployed) ==="
sha256sum /etc/systemd/system/blockchain-*.service /etc/systemd/system/blockchain-*.timer 2>&1
echo "=== nginx ==="
nginx -v 2>&1
sha256sum /etc/nginx/sites-enabled/blockchain-platform.conf 2>&1
echo "=== TRIVY CONFIG probe on deploy/ (unrestricted scanners) ==="
docker run --rm -v /home/euripides/projects/blockchain-platform/deploy:/w:ro aquasec/trivy:latest config /w --timeout 15m 2>&1 | tail -40
echo "PROBE_DONE=1"
