echo "=== UI 用例 1 的 API 侧错误（调查创建） ==="
grep -E 'investigations' /tmp/api.log | tail -3 | python3 -c "
import sys,json
for line in sys.stdin:
    try:
        d=json.loads(line)
        print(d.get('time','')[:19], d.get('method'), d.get('path','')[:60], 'status=%s' % d.get('status'), 'err=%s' % (d.get('error') or d.get('business_error_code') or ''))
    except Exception: pass
"
echo "=== 最近的 4xx/5xx 请求 ==="
tail -400 /tmp/api.log | python3 -c "
import sys,json
from collections import Counter
c=Counter()
for line in sys.stdin:
    try:
        d=json.loads(line)
        st=d.get('status')
        if st and int(st)>=400:
            c[(st, d.get('route_template') or d.get('path','')[:50])]+=1
    except Exception: pass
for k,v in c.most_common(10): print(v, k)
"
echo SCRIPT_END
