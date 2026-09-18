cd /home/euripides/projects/blockchain-platform
mkdir -p acceptance-evidence/dual-system-v1/sprint7/perf acceptance-evidence/dual-system-v1/sprint7/qa
cp acceptance-evidence/dual-system-v1/tests/sprint7/perf/*.json acceptance-evidence/dual-system-v1/sprint7/perf/ 2>/dev/null
cp acceptance-evidence/dual-system-v1/tests/sprint7/qa/summary.json acceptance-evidence/dual-system-v1/sprint7/qa/ 2>/dev/null
mkdir -p acceptance-evidence/dual-system-v1/sprint7/release
cp acceptance-evidence/dual-system-v1/tests/sprint7/release/* acceptance-evidence/dual-system-v1/sprint7/release/ 2>/dev/null
echo "=== sprint7 evidence tree ==="
find acceptance-evidence/dual-system-v1/sprint7 -maxdepth 2 -type d | sort
echo "=== 小文件清单（将回传到本地） ==="
find acceptance-evidence/dual-system-v1/sprint7 acceptance-evidence/dual-system-v1/tests/sprint7 -type f \( -name '*.md' -o -name '*.json' -o -name '*.txt' -o -name '*.log' -o -name '*.env' \) ! -name '*.dump' ! -name '*.tar.gz' | wc -l
echo SCRIPT_END
