cd /home/euripides/projects/blockchain-platform/acceptance-evidence/dual-system-v1/tests/sprint7/perf 2>/dev/null && ls -la | tail -12
for f in *.json; do [ "$f" = "summary.json" ] && continue; python3 -c "
import json,sys
d=json.load(open('$f'))
print('%-26s n=%-6s rps=%-7.1f p50=%-7.2f p95=%-8.2f p99=%-8.2f err=%s' % (d['label'], d['requests'], d['rps'], d['p50_ms'], d['p95_ms'], d['p99_ms'], d['errors']))
" 2>/dev/null; done
echo SCRIPT_END
