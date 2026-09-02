# 阶段 J 汇总：导出

- EXP-001 部分通过: csv/xlsx/json 三格式 COMPLETED 且 rows=242 与 CH FINAL 完全一致；CSV UTF-8 解析 242 数据行；JSONL 242 行全部合法 JSON；XLSX PK 魔数（OOXML）且单元格全为 inlineStr（金额无浮点损失）；SHA-256 校验失败（服务端返回空 sha256 → BUG-001 部署落后）；parquet/jsonl 在部署环境 400（BUG-001）；CSV/XLSX token_decimals 列为指针十六进制（BUG-001 症状）
- EXP-002 PASS: 全部/筛选结果/已勾选三种范围由 smoke 用例端到端验证（已勾选 1 行只导出 1 行 JSON；筛选结果含全部命中且不受分页限制；文件名区分 全部/筛选结果/已勾选）
- EXP-003 PASS: 4 地址×2 Dataset ZIP=manifest.json + addresses/{addr}/{dataset}.csv（9 个文件，结构正确、无路径穿越）；manifest 与请求一致；rows=260 汇总正确
- EXP-004 PASS: 合法区间 206 + 正确 Content-Range/Content-Length；开放尾区间 206；越界 416；反向 416；坏 Ticket 403；DB 强制过期 Ticket 403；篡改 Ticket 403；响应字节与文件片段一致（bytes 0-15 == "chain,tx_hash,bl"）
- EXP-005 部分通过: 0 行导出 COMPLETED rows=0 可解释（对象存在）；1 行导出正常；公式注入：include_meta 未输出 case/tag 列（BUG-001 症状，部署版无元数据列，注入面消失但同时 META 导出同步缺失）；中文/换行/引号未触发解析错误
