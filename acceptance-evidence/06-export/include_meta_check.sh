#!/usr/bin/env bash
set +e
grep -n 'case_names|tag_names|annotateRows|includeMeta' /home/euripides/projects/blockchain-platform/backend/internal/api/export_v2.go | head -20
echo "=== deployed source check for include_meta ==="
grep -rn 'include_meta' /home/euripides/projects/blockchain-platform/backend/internal/api/export.go | head -5
