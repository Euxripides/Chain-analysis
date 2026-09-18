f=/home/euripides/projects/blockchain-platform/acceptance-evidence/dual-system-v1/tests/sprint7/qa/ui/playwright.log
wc -l "$f"
LC_ALL=C grep -c 'passed\|failed' "$f"
tail -n 3 "$f"
echo "--- rerun proc ---"; pgrep -af 'run-sprint7-rerun' | head -2
echo SCRIPT_END
