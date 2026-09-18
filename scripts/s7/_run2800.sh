echo "=== UI 运行期间的 403/401（按路由聚合） ==="
grep '"status":40' /tmp/api.log | tail -600 | python3 -c "
import sys, json
from collections import Counter
c=Counter()
for line in sys.stdin:
    try:
        d=json.loads(line)
        c[(d.get('status'), d.get('route_template') or d.get('path','')[:60], d.get('error_code'))]+=1
    except Exception: pass
for k,v in c.most_common(12): print(v, k)
"
echo "=== 失败用例对应的 spec 调用 ==="
sed -n '160,170p' /home/euripides/projects/blockchain-platform/frontend/tests/dual-system-sprint4.spec.ts
echo "---"
sed -n '221,232p' /home/euripides/projects/blockchain-platform/frontend/tests/dual-system-sprint5.spec.ts
echo SCRIPT_END
