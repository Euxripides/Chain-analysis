# 阶段 A/B 汇总：环境基线 + 自动化基线（PASS）

## 阶段 A
- ENV-001 PASS: commit 62a6d98 (master)，工作区干净
- ENV-002 PASS: API 1684125(→测试后重启 1748910)、Worker 1684126(→1748576)、前端 1598016；端口无重复；PG/CH/MinIO/NATS 容器 healthy（mobsf 与项目无关，不健康）
- ENV-003 PASS: ANKR_MODE=live；RPC_CHAINS 未显式设置，由默认链注册表提供（RPC-001 实测 7 链可用）；进程参数/日志无 Secret
- ENV-004 PASS: /health/live ok；/health/ready postgres/clickhouse/minio/nats 全 ok

## 阶段 B
- AUTO-001 PASS: go test ./... 退出码 0；20 个含测试包全部 ok（缓存命中），-count=1 非缓存复核同样 0 失败；33 个包（15 个无测试文件）
- AUTO-002 PASS: tsc -b && vite build 成功 5.50s；chunk 警告记录为性能观察（ApiControlPage 1.11MB、TrendChart 503KB、xlsx 430KB）
- AUTO-003 PASS: 39 passed / 9 skipped / 0 failed（Edge 152.0.4191.53，desktop 1440×900 + mobile 390×844）。跳过项均为条件用例：live-download×2项目（未启用写入）、wide-address(mobile-only 限制)、metadata-mutation×2（未启用）。首轮失败为环境问题（Playwright webServer 在管道化 PowerShell 中未保活），以托管后台任务方式启动 dev server 后全绿
- AUTO-004 PASS: 44 passed / 4 skipped / 0 failed（E2E_LIVE_DOWNLOAD=1 + E2E_LIVE_METADATA_MUTATION=1 + E2E_JOB_EXPORT_ID=47b7393e-b256-4297-8315-e7b2662f2481）。产物：LIVE_JOB_ID=88e97c80-7aa7-47b0-8a9c-34b4cadcd55f（复用路径）；LIVE_EXPORT_ID=db282626-51c2-4c92-ae80-b36d7fb6a63c（ZIP）；CSV/XLSX 任务详情导出 591345d6/1478ab6d
- 记录：go-test.log、npm-build.log、AUTO-003-playwright-run2.log、AUTO-004-playwright-live.log
