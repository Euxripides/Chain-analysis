#!/usr/bin/env bash
set +e
echo "=== VM export_v2.go line count & sha ==="
wc -l /home/euripides/projects/blockchain-platform/backend/internal/api/export_v2.go
md5sum /home/euripides/projects/blockchain-platform/backend/internal/api/export_v2.go
echo "=== search for case/tag/meta functions ==="
grep -n 'case_name|tag_name|annotate|attachMeta|CaseNames|TagNames' /home/euripides/projects/blockchain-platform/backend/internal/api/export_v2.go | head
echo "=== grep v2 single export meta handling ==="
sed -n '40,120p' /home/euripides/projects/blockchain-platform/backend/internal/api/export_v2.go
