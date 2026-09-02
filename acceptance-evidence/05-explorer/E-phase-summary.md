# 阶段 E 汇总：数据浏览/筛选/统计

- BRW-001 PASS（契约确认）: 地址目录前缀/后缀搜索命中（q 覆盖 normalized_address + display_name）；案件/标签按 case_ids/tag_ids 参数过滤（盘古/入金地址均命中）；sync_status 过滤生效；keyset 分页稳定无重复。观察 BUG-008（非法 cursor 200 空列表而非 400）
- BRW-002 PASS（UI 部分）: OKLink 风格表头顺序（交易哈希/方法/时间/发送方/接收方/数量/代币）由 smoke 用例断言；合约图标、token 解析名、外链新窗口 noopener 由 smoke/详情用例覆盖
- BRW-003 PASS（API 部分）: min_amount=50000→6 行；token 按合约=179 行全部 USDT；token 按名称 USDT=179 行；from_address=76 行且 direction=out 76 行一致；method 名称/签名/selector/unknown 均可用（token_transfers 上 method 筛选被忽略 → BUG-009）
- BRW-004 PASS: 金额为 Decimal(76,38) 字符串输出，74 位大数无精度损失/无科学计数法（64518700000000000000000000000000000000000000000000000000000000000000000000 原样返回）；0 值、尾 0 规范化（1317/1225/20）；排序与过滤用数值语义（min/max 过滤按 toDecimal256 比较，见 explorer/service.go）；XLSX 全字符串单元格无浮点转换（sheet1.xml 验证）
- 页面统计与数据库一致性：workspace 计数 transaction_count=4/token_transfer_count=242 与 CH FINAL 完全一致（COV-004）
