# 切流门禁清单（S7-RELEASE-02）

| # | 门禁 | 判定命令 | 当前值 | 结论 |
|---|---|---|---|---|
| 1 | 运行环境为 production | APP_ENV=development | development | **未满足：切流前必须改为 production** |
| 2 | 会话 Cookie 在 HTTPS 入口带 Secure + __Host- | 见 security/S7-SEC-01-fixes-R05-R07.md | SECURE=auto PREFIX=__Host- | 通过（公网 HTTPS 实测） |
| 3 | 运行角色降权 | has_table_privilege(blockchain_app, audit_events_v2, UPDATE) | ROLE=blockchain_app -> f | 通过 |
| 4 | 应用连接账号非超级用户 | rolsuper(blockchain_api) | user=blockchain_api -> f | 通过 |
| 5 | §27 全部套件 FAIL=0 且 NOT_VERIFIED=0 | tests/sprint7/qa/summary.json | 见该文件 | 见结论 |
| 6 | 安全负向套件 FAIL=0 | tests/run-sprint7-sec-negative.sh | 见 security/negative-tests | 见结论 |
| 7 | 备份恢复演练达标 | dr/restore-drill-*.md | 见该文件 | 见结论 |
| 8 | 依赖与镜像阻断项处置 | security/dependency-scan、image-iac | R-09/R-10 未关闭 | **未满足（需风险评估签字或升级镜像/依赖）** |
| 9 | IaC 配置面扫描 | image-iac/SUMMARY.md | NOT_VERIFIED | **未满足（需补扫）** |
| 10 | 旧入口写路径已关闭（§11.5 只读观察前置） |  未认证 | 401 | 通过（S7 P0 修复） |
| 11 | 性能达标（§30.2 P95<200ms） | tests/sprint7/perf/summary.json | 见该文件 | 见结论 |

> 说明：本清单是**可执行门禁**，不是切流动作。切换 `/` 入口属人工维护窗口变更，
> 脚本不会、也不得自动改 Nginx 配置。
