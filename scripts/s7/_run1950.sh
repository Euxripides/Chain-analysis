cd /home/euripides/projects/blockchain-platform/acceptance-evidence/dual-system-v1/tests/sprint7/qa/ui
awk '/^  [0-9]+\) /{p=1} p' playwright.log | head -70
echo SCRIPT_END
