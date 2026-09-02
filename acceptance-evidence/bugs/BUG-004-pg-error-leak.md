# BUG-004：错误响应泄露原始 PostgreSQL 错误文本（约束名/SQLSTATE/表结构信息）

- 严重等级：P2
- 状态：可复现
- 发现时间：2026-09-02 21:03 +07:00
- 环境：VM 192.168.31.152，commit 62a6d98
- 影响模块：/cases、/tags、地址元数据等写接口的错误处理
- 影响范围：SEC-002 要求“无 SQL 文本、堆栈、文件路径”；实际将底层驱动错误原文透出（唯一约束名、SQLSTATE、编码错误、varchar 长度上限）
- 前置条件：无
- 完整测试地址：0x9b77e3b4f85c7cea7da451f454895edbd10b80dd
- Request ID：无（错误响应未含 request_id）

### 复现步骤

1. POST /cases 使用已存在的名称 → 409 返回 "ERROR: duplicate key value violates unique constraint \"cases_workspace_id_name_key\" (SQLSTATE 23505)"。
2. POST /cases 名称含 \u0000 控制字符 → 409 返回 "ERROR: invalid byte sequence for encoding \"UTF8\": 0x00 (SQLSTATE 22021)"。
3. POST /cases 名称 10000 字符 → 409 返回 "ERROR: value too long for type character varying(255) (SQLSTATE 22001)"。

### 实际结果

原始 DB 错误文本直接透出，泄露 schema 对象名与约束命名。

### 预期结果

映射为业务错误（如“名称已存在”/“名称包含非法字符”/“名称过长”）+ 409/400，不含数据库内部文本。

### 证据

- 请求与响应：本次验收执行记录（阶段 F/阶段 M，SEC-002 注入用例）
- 数据影响：无（参数化查询，无注入成功）

### 临时绕行方式

无（前端做预校验可减少触发）。
