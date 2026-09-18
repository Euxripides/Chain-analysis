-- 0065_role_read_permission_gaps.sql
-- 系统角色的「有写无读」权限缺口（计划 §7.1 权限矩阵正确性 / §7.4 删除审批闭环）。
--
-- 现象（2026-09-16 由浏览器用例 tests/dual-system-nav-permission.spec.ts 在真实环境暴露）：
--   Platform Admin 持有 admin:deletion:request + admin:deletion:approve，却没有 admin:deletion:read，
--   于是「保留与删除」页里的删除请求列表 403 —— 能创建、能审批，却看不到自己审批的对象；
--   Security Admin 同理：有 admin:alert:manage 没有 admin:alert:read，有 admin:approval:decide
--   没有 admin:approval:read（Kill Switch / 审计完整性审批都能决定，却列不出待审批项）。
--
-- 规则：系统角色若持有某资源的写/管理类权限，就必须同时持有该资源的读权限（read 或 list）。
--   本迁移按这条规则**通用地**补齐缺口（不写死角色名与权限名），且只补「权限目录里真实存在」的读权限；
--   例如 admin:provider:key:rotate 的对应读权限 admin:provider:key:read 在目录中不存在，
--   因此不会被误补（是否需要给 Security Admin 增加 admin:provider:read 属于安全策略决定，见开放项）。
--
-- 幂等与安全性：ON CONFLICT DO NOTHING；不删除、不改写任何既有授权；重复执行结果一致。

INSERT INTO role_permissions (role_id, permission_key)
SELECT DISTINCT rp.role_id, reads.key
FROM role_permissions rp
JOIN roles r ON r.id = rp.role_id AND r.is_system
JOIN permissions p ON p.key = rp.permission_key
JOIN permissions reads
  ON reads.key IN (
       regexp_replace(p.key, ':[^:]+$', '') || ':read',
       regexp_replace(p.key, ':[^:]+$', '') || ':list'
     )
WHERE split_part(p.key, ':', -1) NOT IN ('read', 'list')
  AND NOT EXISTS (
    SELECT 1 FROM role_permissions have
    WHERE have.role_id = rp.role_id
      AND have.permission_key IN (
        regexp_replace(p.key, ':[^:]+$', '') || ':read',
        regexp_replace(p.key, ':[^:]+$', '') || ':list'
      )
  )
ON CONFLICT DO NOTHING;

-- 自检：迁移结束时不允许再存在「有写无读」的缺口（不允许静默降级）。
DO $$
DECLARE
    gaps text;
BEGIN
    SELECT string_agg(DISTINCT r.name || ' -> ' || p.key, ', ')
      INTO gaps
      FROM role_permissions rp
      JOIN roles r ON r.id = rp.role_id AND r.is_system
      JOIN permissions p ON p.key = rp.permission_key
     WHERE split_part(p.key, ':', -1) NOT IN ('read', 'list')
       AND EXISTS (
             SELECT 1 FROM permissions reads
              WHERE reads.key IN (
                      regexp_replace(p.key, ':[^:]+$', '') || ':read',
                      regexp_replace(p.key, ':[^:]+$', '') || ':list'
                    )
           )
       AND NOT EXISTS (
             SELECT 1 FROM role_permissions have
              WHERE have.role_id = rp.role_id
                AND have.permission_key IN (
                      regexp_replace(p.key, ':[^:]+$', '') || ':read',
                      regexp_replace(p.key, ':[^:]+$', '') || ':list'
                    )
           );
    IF gaps IS NOT NULL THEN
        RAISE EXCEPTION '0065 self-check failed: system roles still hold a write permission without its read counterpart -> %', gaps;
    END IF;
END
$$;
