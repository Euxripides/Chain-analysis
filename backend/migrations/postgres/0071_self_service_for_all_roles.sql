-- 0071_self_service_for_all_roles.sql
-- 目的：让**每个系统角色**都能管理自己的账号安全（MFA 注册、改密、自己的会话与设备）。
--
-- 背景：后台管理台新增的「账号安全」页，其入口与路由按路由表权限反查（App.tsx 的 fail-closed 规则：
--   反查不到权限的入口一律不显示、页面也被 Guard 挡住）。而实测 platform_admin / security_admin /
--   auditor 三个系统角色**不持有任何 self 类权限**（security:update / profile:read /
--   session:self:revoke …），于是最需要绑定验证器的平台管理员反而看不到入口 —— 这正是 D7 的缺口。
--
-- 为什么这不是提权：self 类权限只作用于**调用者本人**（自己的凭据、自己的会话、自己的偏好），
--   不涉及他人数据或平台资源；而且 MFA 注册 / 改密 / 会话管理这些接口本身只要求"已确认会话"，
--   并不检查 admin:* 权限。授予它们不会扩大任何人对他人或平台资源的访问面。
--
-- 幂等：ON CONFLICT DO NOTHING；只新增授权，不改写既有授权。

INSERT INTO role_permissions (role_id, permission_key)
SELECT r.id, k.key
  FROM roles r
  JOIN (VALUES ('security:update'), ('profile:read'), ('profile:update'), ('preference:update'), ('session:self:revoke')) AS k(key) ON true
  JOIN permissions p ON p.key = k.key
 WHERE r.is_system
ON CONFLICT DO NOTHING;

-- 自检：每个系统角色都必须能管理自己的账号安全（缺了就是"管理员绑不了验证器"的老毛病）。
DO $$
DECLARE
    missing text;
BEGIN
    SELECT string_agg(r.key, ', ' ORDER BY r.key) INTO missing
      FROM roles r
     WHERE r.is_system
       AND NOT EXISTS (
             SELECT 1 FROM role_permissions rp
              WHERE rp.role_id = r.id AND rp.permission_key = 'security:update');
    IF missing IS NOT NULL THEN
        RAISE EXCEPTION '0071 self-check failed: 系统角色缺少 security:update: %', missing;
    END IF;
END $$;
