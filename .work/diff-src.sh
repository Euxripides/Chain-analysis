#!/usr/bin/env bash
set -u
cd "$(dirname "$0")/.." || exit 1
cd .work || exit 1
# 远程清单带 sftp 头（session_id/truncated/size），只保留哈希行
grep -E '^[0-9a-f]{64}  ' remote-src-manifest.txt > r.txt
grep -E '^[0-9a-f]{64}  ' local-src-manifest.txt  > l.txt
awk '{print $2}' l.txt | LC_ALL=C sort > l.paths
awk '{print $2}' r.txt | LC_ALL=C sort > r.paths
echo "local=$(wc -l < l.txt) remote=$(wc -l < r.txt)"
echo "=== 仅本地存在（未部署）: $(comm -23 l.paths r.paths | wc -l) ==="
comm -23 l.paths r.paths | head -50
echo "=== 同路径不同哈希: ==="
join -j 2 <(LC_ALL=C sort -k2,2 l.txt) <(LC_ALL=C sort -k2,2 r.txt) | awk '$2!=$3 {print $1}'
echo "=== 计数 ==="
join -j 2 <(LC_ALL=C sort -k2,2 l.txt) <(LC_ALL=C sort -k2,2 r.txt) | awk '$2!=$3' | wc -l
