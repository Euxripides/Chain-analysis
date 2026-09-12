-- 0046 设备外键语义修正
--
-- 缺陷来源：sessions.device_id 的外键没有 ON DELETE 动作（默认 NO ACTION），
-- 导致"删除一个设备"会被历史会话引用挡住：
--
--   ERROR: update or delete on table "trusted_devices" violates foreign key
--          constraint "sessions_device_id_fkey" on table "sessions"
--
-- 正常路径下撤销设备走的是 revoked_at（软删除），因此这个约束平时不会触发；
-- 但"用户要求彻底移除设备记录"（隐私删除 / 保留期清理 / 测试夹具重置）是合法操作，
-- 不应该被历史会话阻塞。
--
-- 语义选择：ON DELETE SET NULL。
--   设备记录被删除后，历史会话仍然保留（审计需要），只是不再关联设备指纹，
--   这与 §6.6.4 "设备 ID 不是人员身份结论" 的定位一致。

ALTER TABLE sessions DROP CONSTRAINT IF EXISTS sessions_device_id_fkey;
ALTER TABLE sessions
    ADD CONSTRAINT sessions_device_id_fkey
    FOREIGN KEY (device_id) REFERENCES trusted_devices(id) ON DELETE SET NULL;

-- 同理：会话自身的父子引用不应阻止清理历史会话，否则令牌族的保留期删除会失败。
ALTER TABLE sessions DROP CONSTRAINT IF EXISTS sessions_parent_id_fkey;
ALTER TABLE sessions
    ADD CONSTRAINT sessions_parent_id_fkey
    FOREIGN KEY (parent_id) REFERENCES sessions(id) ON DELETE SET NULL;

ALTER TABLE sessions DROP CONSTRAINT IF EXISTS sessions_replaced_by_fkey;
ALTER TABLE sessions
    ADD CONSTRAINT sessions_replaced_by_fkey
    FOREIGN KEY (replaced_by) REFERENCES sessions(id) ON DELETE SET NULL;

-- revoked_by 指向操作者身份；操作者被删除时不应连带删除审计对象，置空即可。
ALTER TABLE sessions DROP CONSTRAINT IF EXISTS sessions_revoked_by_fkey;
ALTER TABLE sessions
    ADD CONSTRAINT sessions_revoked_by_fkey
    FOREIGN KEY (revoked_by) REFERENCES identities(id) ON DELETE SET NULL;
