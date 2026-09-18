-- 0062：管理台重置 MFA 的权限（计划 §21.6 /users/{id}/commands/reset-mfa）。
--
-- 背景：MFA/TOTP 已由 0060/0061 落地（登录第二步 + 自助注册）。但账号一旦启用 MFA，
-- 丢失认证器的用户无法完成登录第二步，而管理侧此前没有任何解除手段 —— 规范 §21.6 列出的
-- reset-mfa 命令在代码里根本不存在，只能直接改库。本迁移补齐该命令所需的权限键。
--
-- 授权原则（§4.1）：与 admin:user:password:reset 同级（critical），授予 platform_admin 与
-- security_admin 这两个本就承担账号安全处置的系统角色。owner 的「全部权限」是 0041 里的一次性
-- CROSS JOIN，新增权限键不会被自动补授，因此这里必须显式授予 owner。
--
-- 全部为幂等写入，不删除既有授权。
INSERT INTO permissions (key, category, description, risk_level, sensitive) VALUES
    ('admin:user:reset_mfa', 'admin.identity', '重置用户 MFA', 'critical', false)
ON CONFLICT (key) DO UPDATE SET category = EXCLUDED.category, description = EXCLUDED.description,
    risk_level = EXCLUDED.risk_level, sensitive = EXCLUDED.sensitive;

INSERT INTO role_permissions (role_id, permission_key)
SELECT r.id, 'admin:user:reset_mfa' FROM roles r
 WHERE r.key IN ('owner', 'platform_admin', 'security_admin') AND r.is_system
ON CONFLICT DO NOTHING;
