cd /home/euripides/projects/blockchain-platform
echo "=== 证据目录总览 ==="
du -sh acceptance-evidence/dual-system-v1/sprint7/* 2>/dev/null
echo "=== 文件数 ==="
find acceptance-evidence/dual-system-v1/sprint7 -type f | wc -l
echo "=== 新脚本/迁移/手册 ==="
ls backend/migrations/postgres/00{56,57}*.sql backend/migrations/rollback/ scripts/backup/*.sh scripts/perf/*.sh scripts/release/*.sh docs/manuals/*.md backend/cmd/auditverify/main.go backend/cmd/loadprobe/main.go 2>/dev/null | wc -l
echo "=== 服务与入口最终状态 ==="
export XDG_RUNTIME_DIR=/run/user/$(id -u)
systemctl --user is-active blockchain-api blockchain-worker blockchain-containers-watchdog.timer
curl -s http://127.0.0.1:8080/health/ready; echo
echo SCRIPT_END
