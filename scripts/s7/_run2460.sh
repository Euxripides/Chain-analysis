set -e
cd /home/euripides/projects/blockchain-platform
chmod +x scripts/backup/containers-watchdog.sh
cp deploy/blockchain-containers-watchdog.service deploy/blockchain-containers-watchdog.timer ~/.config/systemd/user/
export XDG_RUNTIME_DIR=/run/user/$(id -u)
systemctl --user daemon-reload
systemctl --user enable --now blockchain-containers-watchdog.timer
systemctl --user list-timers blockchain-containers-watchdog.timer --no-pager | head -4
echo "=== 看门狗冒烟（全部 running 时应 recovered=0） ==="
bash scripts/backup/containers-watchdog.sh
echo "=== 真实故障注入：kill -9 NATS，等待看门狗恢复 ==="
sudo -n docker kill -s KILL deploy-nats-1
sleep 2
sudo -n docker inspect -f 'kill 之后立即: {{.State.Status}}' deploy-nats-1
T0=$(date +%s.%N)
for i in $(seq 1 90); do
  if curl -sf -o /dev/null --max-time 2 http://127.0.0.1:8222/healthz; then
    T1=$(date +%s.%N); python3 -c "print('NATS 由看门狗恢复，耗时 %.2f s' % ($T1-$T0))"; break
  fi
  sleep 1
done
sudo -n docker inspect -f '最终: {{.State.Status}}' deploy-nats-1
curl -s http://127.0.0.1:8080/health/ready; echo
echo SCRIPT_END
