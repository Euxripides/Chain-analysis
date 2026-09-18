cd /home/euripides/projects/blockchain-platform/acceptance-evidence/dual-system-v1/tests
echo "START"
for f in run-rbac-e2e.sh run-iam-e2e.sh run-sprint12-e2e.sh; do
  echo "--- $f ---"
  grep -c 'result.json' "$f"
  grep -n 'PASS=' "$f" | tail -3
done
echo "END"