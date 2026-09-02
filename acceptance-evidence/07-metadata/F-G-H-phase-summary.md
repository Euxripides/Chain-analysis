# 阶段 F/G/H 汇总：元数据、Token Metadata、Method/ABI

## 阶段 F（案件/标签）
- META-001 PASS: 任意名称案件/标签创建→关联→刷新保持→恢复/删除全链路；重复名称 409（但错误文本泄露 → BUG-004）；最终状态恢复为 盘古/入金地址（display_name 恢复为空）
- META-002 PASS: 删除测试案件/标签后地址安全回退到原主案件；无悬空关联
- META-003 风险记录: 并发 PATCH 均 200、last-write-wins（并发修改B 生效），无版本冲突保护——已按方案记录为风险与复现步骤

## 阶段 G（Token Metadata）
- TOK-001 PASS: 官方 USDT= Tether USD/USDT/18/BEP20，source=verified_registry，verified=true，logo 后端代理 PNG 21909B（smoke 验证 naturalWidth>0）
- TOK-002 PASS: 仿冒 U5DТ（0xefc391...）独立 logo、verified=false、source=ankr_token_transfer+rpc，不继承官方状态；同名多合约检索隔离
- TOK-003 PASS: EOA/非ERC20/未收录合约 → 404 token not found，列表不崩溃（smoke 断言无“解析/未验证”文案、无地址冒充名称）
- TOK-004 PASS(后端) + 观察: logo 只从 MinIO 对象读取（无任意 URL 拉取/内网探测面）；worker 侧 LimitReader 限长；1 例 logo 404 优雅回退（U5DТ trustwallet 无图，WARN 可归因）

## 阶段 H（Method/ABI）
- MTH-001 PASS: 原生转账 4/4 分类 Native Transfer（heuristic, verified=0），input=0x 保留；合约调用 selector 从 input 本地提取（0xcbc026d3 等）
- MTH-002 PASS(解析层): 只有 contract_abi/manual_override/provider 深度解码可标 verified；local_signature 与 public_signature 解析层恒 verified=false（signature_resolver.go verifiedFor()）；实测 /methods/0xa9059cbb → local_signature verified=false。观察: /methods/search 直接读 PG 表暴露 seed 库级 verified=true（与解析层语义不同，属"策划签名"语义，代码注释已说明）——跨端点字段同名异义，P3 观察
- MTH-003 PASS: 多候选 selector 返回全部候选；无法收敛时名称空/confidence 低（0xcbc026d3: unknown/confidence=unknown，不随机选择）
- MTH-004 PASS: 本地已存交易 local_only 返回本地状态（200）；auto 首次真实 Provider 解码（11.1s，decode_source=ankr，usage +3 请求）；二次 auto 263ms cache_hit=true；force_refresh 200（V1 单用户默认 admin，符合 SEC-001 记录口径）；大整数输出十进制字符串
- MTH-005 PASS: backfill 创建→RUNNING(38k 行)→PAUSE_REQUESTED→checkpoint 持久化→resume(58k)→CANCEL_REQUESTED→CANCELLED；keyset checkpoint（timestamp+tx_hash）；scanned 行均为本地重解析（provider_usage 无新增下载类请求）；rewritten_rows=0 幂等。观察 BUG-006（68 条 UNRESOLVED 队列滞留 >24h）
