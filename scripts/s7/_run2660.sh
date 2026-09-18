cd /home/euripides/projects/blockchain-platform/acceptance-evidence/dual-system-v1/sprint7/security/negative-tests 2>/dev/null || cd /home/euripides/projects/blockchain-platform/acceptance-evidence/dual-system-v1/tests/sprint7/security 2>/dev/null
ls -la 2>/dev/null | head
echo "=== FAIL 行 ==="
grep -rn 'FAIL' /home/euripides/projects/blockchain-platform/acceptance-evidence/dual-system-v1/sprint7/security/negative-tests/*.log 2>/dev/null | tail -5
find /home/euripides/projects/blockchain-platform/acceptance-evidence/dual-system-v1 -name 'result*.json' -newermt '-10 minutes' 2>/dev/null | head -5
echo SCRIPT_END
