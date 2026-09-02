# 阶段 M 汇总：权限与安全

- SEC-001 PASS: 角色矩阵实测（X-User-Role 头）：普通查询全角色 200；force_refresh viewer 403、analyst/investigator/admin/owner 200；trace viewer 403、其余 200；admin backfill 仅 admin/owner 200；无身份头 = V1 单用户默认（admin 语义），与方案“当前 V1 单用户策略一致并被明确记录”相符
- SEC-002 PASS(注入防护) + BUG-004: 地址目录 q 注入（' OR '1'='1、DROP TABLE、UNION SELECT）→ 200 空结果、无 SQL 执行、数据库无修改（参数化查询）；控制字符/超长案件名被拒但错误文本泄露原始 PG 错误（BUG-004）
- SEC-003 PASS: Ticket 只能下载绑定对象；篡改/过期/伪造 → 403；错误不暴露 MinIO 内部地址；Bucket 私有（blockchain-raw/exports/backups 无匿名访问面）
- SEC-004 PASS: 本地 dist 与 VM 前端构建产物扫描 0 命中（ANKR_API_KEY/POSTGRES_PASSWORD/CLICKHOUSE_PASSWORD/MINIO_ROOT_PASSWORD/私钥头/Bearer）；无 source map；index.html 无环境变量嵌入；浏览器控制台/Network 未出现 Secret（smoke 用例捕获 console error 均为 0）
- 角色矩阵测试产生的测试签名/预算已清理（2 签名 + 2 预算删除，token override 未生效，ABI 未写入）
