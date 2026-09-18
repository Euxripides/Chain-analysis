-- 0070_owner_full_permissions.sql
-- 目的：让「管理员」拥有权限目录里的**全部**权限（需求方 2026-09-18 明确："管理员有最高、所有权限"）。
--
-- 事实依据（2026-09-18 实测）：权限目录共 179 条，系统角色 owner 持有 178 条，
--   唯一缺口就是 0068 新引入的 admin:user:delete —— 也就是说按原口径"删除仅限
--   platform_admin / security_admin"落地后，**生产管理员 admin@local（owner）反而不能删人**，
--   而 platform_admin / security_admin 又不能建号（admin:user:create 仅 owner 持有），
--   实际结果是没有任何真人账号能完成"建号→离职删除"的闭环。
--
-- 决策：管理员（owner）持有全部权限；platform_admin / security_admin 继续保留删除权
--   （它们是平台运维/安全角色，见 0068）。
--
-- 幂等：ON CONFLICT DO NOTHING；只新增一条授权，不改写任何既有授权。
INSERT INTO role_permissions (role_id, permission_key) SELECT r.id, 'admin:user:delete' FROM roles r WHERE r.is_system AND r.key = 'owner' ON CONFLICT DO NOTHING;

-- 自检（把"管理员有最高权限"固化成不变量）：owner 必须持有权限目录中的每一条权限。
--   未来任何新增权限键若忘记授予 owner，这里会当场失败，而不是等到线上发现"管理员点不动"。
DO $$
DECLARE
    missing text;
BEGIN
    SELECT string_agg(p.key, ', ' ORDER BY p.key) INTO missing
      FROM permissions p
     WHERE NOT EXISTS (
           SELECT 1 FROM role_permissions rp
             JOIN roles r ON r.id = rp.role_id
            WHERE r.key = 'owner' AND rp.permission_key = p.key);
    IF missing IS NOT NULL THEN
        RAISE EXCEPTION '0070 self-check failed: owner 缺少权限: %', missing;
    END IF;
END $$;
