#!/usr/bin/env bash
# P1 工作区作用域闸门专用夹具（P1-20260913，根 11:12 工程决定）
# 目的：具备恰当 list 权限但属于独立 wsB 的专用 fixture，用于验证
#       (a) 权限闸（缺 export:create → 403）与 (b) 作用域闸（跨 ws → 404/不出现）分别成立。
# 只做**追加**：新身份 + 新成员资格；唯一的"状态变更"是把本次新建夹具的
# 一个成员资格置为 removed（用于 0 成员资格的 fail-closed 用例），不触碰任何真实用户。
set -uo pipefail
R=/home/euripides/projects/blockchain-platform
TS=$(date -u +%Y%m%dT%H%M%SZ)
EV=$R/acceptance-evidence/dual-system-v1/sprint7/p1-workspace-scope-fixtures-$TS
mkdir -p "$EV"; chmod 700 "$EV"
exec > >(tee "$EV/p1-fixtures-console.log") 2>&1
echo "ts_utc=$TS script_sha256=$(sha256sum "$0" | cut -d' ' -f1)"
echo "evidence=$EV"
chmod +x "$0" 2>/dev/null || true
BIN=$R/backend/bin/fixture-identity
echo "fixture_tool_sha256=$(sha256sum "$BIN" | cut -d' ' -f1)"
set -a; . "$R/deploy/.env"; set +a
pgq(){ docker exec -i deploy-postgres-1 psql -U blockchain -d blockchain_platform -q -tA -F'|' -v ON_ERROR_STOP=1; }

mk(){ # role email wsslug outfile tag
  local role="$1" email="$2" ws="$3" out="$4" tag="$5"
  if [ -s "$out" ]; then echo "fixture_exists $tag $email (creds file present, 不重建口令)"; return 0; fi
  "$BIN" -org-slug s7jobscope -org-name "S7 JobScope 回归夹具" -workspaces "$ws" \
    -identities "$role:$email:$ws" -out "$out" || { echo "FIXTURE_CREATE_FAILED $tag"; return 1; }
  echo "fixture_created $tag $email role=$role ws_slug=$ws creds_file=$out"
  chmod 600 "$out"
}

echo "=== 1. 专用夹具创建（追加，幂等：已有 creds 文件则跳过） ==="
mk viewer       p1gate-view-b@fixture.local s7js-b      /home/euripides/.p1gate-view-b.env p1gate-view-b
mk investigator p1gate-inv-b@fixture.local  s7js-b      /home/euripides/.p1gate-inv-b.env  p1gate-inv-b
mk investigator p1gate-inv-a@fixture.local  s7js-a      /home/euripides/.p1gate-inv-a.env  p1gate-inv-a
mk investigator p1gate-multi@fixture.local  s7js-a      /home/euripides/.p1gate-multi.env  p1gate-multi
mk viewer       p1gate-nomem@fixture.local  p1-nomem-ws /home/euripides/.p1gate-nomem.env  p1gate-nomem

echo "=== 2. 追加式成员资格调整（仅本次新建夹具） ==="
# 2.1 多工作区身份：给 p1gate-multi 追加 s7js-b 的 active 成员资格
pgq <<'SQL'
INSERT INTO workspace_memberships (workspace_id, organization_id, identity_id, status)
SELECT w.id, w.organization_id, i.id, 'active'
FROM identities i, workspaces w
WHERE i.email='p1gate-multi@fixture.local' AND w.slug='s7js-b'
  AND w.organization_id=i.organization_id
ON CONFLICT (workspace_id, identity_id) DO NOTHING;
SQL
# 2.2 零成员资格身份：把本夹具自己那个工作区的成员资格置为 removed（保留行，不删除）
pgq <<'SQL'
UPDATE workspace_memberships wm SET status='removed', updated_at=now()
FROM identities i, workspaces w
WHERE wm.identity_id=i.id AND wm.workspace_id=w.id
  AND i.email='p1gate-nomem@fixture.local' AND w.slug='p1-nomem-ws'
  AND wm.status<>'removed';
SQL

echo "=== 3. 夹具状态快照（无口令） ==="
pgq > "$EV/fixtures-state.txt" <<'SQL'
SELECT i.email, i.id, i.status,
       coalesce(string_agg(w.slug || ':' || wm.status, ',' ORDER BY w.slug), '(no workspace membership)') AS memberships,
       (SELECT string_agg(r.key, ',' ORDER BY r.key) FROM member_role_bindings mrb JOIN roles r ON r.id=mrb.role_id
        WHERE mrb.identity_id=i.id AND mrb.revoked_at IS NULL) AS roles
FROM identities i
LEFT JOIN workspace_memberships wm ON wm.identity_id=i.id
LEFT JOIN workspaces w ON w.id=wm.workspace_id
WHERE i.email LIKE 'p1gate-%@fixture.local'
GROUP BY i.email, i.id, i.status ORDER BY i.email;
SQL
cat "$EV/fixtures-state.txt"

echo "=== 4. 口令敏感性核对（只记录 len/sha12，不落值） ==="
for f in /home/euripides/.p1gate-view-b.env /home/euripides/.p1gate-inv-b.env /home/euripides/.p1gate-inv-a.env /home/euripides/.p1gate-multi.env /home/euripides/.p1gate-nomem.env; do
  [ -f "$f" ] || { echo "MISSING $f"; continue; }
  stat -c '%n mode=%a owner=%U' "$f"
  awk -F= '/_PW=/{printf "%s len=%d sha12=%s\n", $1, length($2), substr($2,1,12)}' "$f"
done | tee "$EV/creds-redacted.txt"

echo "=== 5. 成员资格总览（不减少既有数据） ==="
pgq <<'SQL' | tee "$EV/membership-totals.txt"
SELECT 'workspace_memberships_active' AS k, count(*)::text FROM workspace_memberships WHERE status='active'
UNION ALL SELECT 'workspace_memberships_total', count(*)::text FROM workspace_memberships
UNION ALL SELECT 'identities_total', count(*)::text FROM identities
UNION ALL SELECT 'real_users_in_multiws_org_untouched', 'see p1-export-list-crossscope c8 snapshot'::text;
SQL
chmod 600 "$EV"/*.txt 2>/dev/null
echo "FIXTURE_EVIDENCE_DIR=$EV"
