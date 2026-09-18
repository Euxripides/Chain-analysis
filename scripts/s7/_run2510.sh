cd /home/euripides/projects/blockchain-platform/acceptance-evidence/dual-system-v1/tests/sprint7/perf 2>/dev/null || exit 0
for f in *.json; do python3 -c "
import json
d=json.load(open('$f'))
if 'label' not in d: raise SystemExit
sc=d.get('status_counts',{})
print('%-24s n=%-7s rps=%-8.1f p50=%-7.2f p95=%-8.2f p99=%-8.2f err=%-5s codes=%s' % (d['label'], d['requests'], d['rps'], d['p50_ms'], d['p95_ms'], d['p99_ms'], d['errors'], sc))
" 2>/dev/null; done
echo SCRIPT_END
