mkdir -p /home/euripides/projects/blockchain-platform/scripts/backup
cp /tmp/backup-all.sh /tmp/restore-drill.sh /tmp/kill9-drill.sh /home/euripides/projects/blockchain-platform/scripts/backup/
cd /home/euripides/projects/blockchain-platform/backend && export PATH=/usr/local/go/bin:$PATH
go vet ./cmd/loadprobe/ 2>&1 | head -20
go build -o bin/loadprobe ./cmd/loadprobe && echo LOADPROBE_BUILD_OK
./bin/loadprobe -url http://127.0.0.1:8080/health/live -concurrency 2 -duration 2s -label smoke
echo SCRIPT_END
