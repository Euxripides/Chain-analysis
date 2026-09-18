set -uo pipefail
cd /home/euripides/projects/blockchain-platform
echo "=== find content-PRE.txt anywhere under repo ==="
find . -name 'content-PRE.txt' -not -path './.git/*' 2>/dev/null | head
echo "=== find *.tsv.gz ==="
find . -name '*.tsv.gz' -not -path './.git/*' 2>/dev/null | head
echo "=== find columns-pre.json ==="
find . -name 'columns-pre.json' 2>/dev/null | head
echo "=== recent dirs in sprint7 ==="
ls -lat acceptance-evidence/dual-system-v1/sprint7/ | head -12
