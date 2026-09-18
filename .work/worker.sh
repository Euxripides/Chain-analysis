#!/usr/bin/env bash
set -uo pipefail
export PATH=/usr/local/go/bin:$PATH
P=/home/euripides/projects/blockchain-platform
TS=$(date -u +%Y%m%dT%H%M%SZ)
BK="$HOME/deploy-backups/pre-perf-20260916T174927Z"
cd "$P/backend"
echo "== unit exec =="
systemctl --user show blockchain-worker -p ExecStart --value
echo "== build worker =="
go build -o /tmp/worker.new ./cmd/worker 2>&1 | tail -20
echo "built sha: $(sha256sum /tmp/worker.new | awk '{print $1}')"
echo "== backup + swap (only worker is stopped) =="
cp -av "$P/backend/bin/worker" "$BK/worker.pre-deploy-$TS"
sha256sum "$BK/worker.pre-deploy-$TS"
systemctl --user stop blockchain-worker
sleep 2
mv -v /tmp/worker.new "$P/backend/bin/worker"
chmod 755 "$P/backend/bin/worker"
echo "on-disk worker sha: $(sha256sum "$P/backend/bin/worker" | awk '{print $1}')"
systemctl --user start blockchain-worker
for i in $(seq 1 40); do
  st=$(systemctl --user is-active blockchain-worker)
  [ "$st" = "active" ] && { echo "worker active after ${i}s"; break; }
  sleep 1
done
echo "== worker status =="
systemctl --user status blockchain-worker --no-pager | head -12
echo "== api untouched =="
printf 'api=%s pid=%s sha=%s\n' "$(systemctl --user is-active blockchain-api)" "$(pgrep -f 'backend/bin/api$' | head -1)" "$(sha256sum /proc/$(pgrep -f 'backend/bin/api$' | head -1)/exe | awk '{print $1}')"
