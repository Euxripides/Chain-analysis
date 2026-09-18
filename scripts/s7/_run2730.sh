D=/home/euripides/projects/blockchain-platform/acceptance-evidence/dual-system-v1/sprint7/security/negative-tests
grep -n 'FAIL' $D/run.log | head -6
echo "=== cookie attributes 实测 ==="
sed -n '1,12p' $D/files/cookie-attributes.txt 2>/dev/null
echo SCRIPT_END
