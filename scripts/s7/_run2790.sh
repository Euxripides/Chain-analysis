cd /home/euripides/projects/blockchain-platform/acceptance-evidence/dual-system-v1/tests/sprint7/qa/ui
grep -E '^  [0-9]+\) ' playwright.log
echo "=========="
python3 - <<'PY'
import re
txt = open('playwright.log', errors='replace').read()
blocks = re.split(r"
  d+) ", txt)
for b in blocks[1:5]:
    head = b.split("
")[0][:90]
    errs = [l.strip() for l in b.split("
") if re.search(r"Error:|expect(|Received|Expected|Timed out|at /home", l)][:6]
    print("###", head)
    for e in errs: print("   ", e[:150])
    print()
PY
echo SCRIPT_END
