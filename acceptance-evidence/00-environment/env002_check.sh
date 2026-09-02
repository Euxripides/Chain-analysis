#!/usr/bin/env bash
# ENV-002 service/port/container check
set +e
echo "=== date ==="
date -u '+%Y-%m-%d %H:%M:%SZ'
echo "=== pgrep api/worker ==="
pgrep -af 'bin/api' ; pgrep -af 'bin/worker'
echo "=== ports ==="
ss -lntp | grep -E ':3000|:8080|:8081|:5433|:8123|:9001|:4222'
echo "=== docker ps ==="
docker ps --format '{{.Names}}	{{.Status}}	{{.Ports}}'
echo "=== docker ps -a ==="
docker ps -a --format '{{.Names}}	{{.Status}}'
echo "=== df /data ==="
df -h /data
echo "=== free ==="
free -m
