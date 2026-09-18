# 双写核查

## 数据源：新旧前端与新旧 API 都指向同一 PostgreSQL / ClickHouse / MinIO
DATABASE_URL=postgres://blockchain_api:<redacted>@127.0.0.1:5433/blockchain_platform?sslmode=disable
CLICKHOUSE_URL=clickhouse://blockchain:<redacted>@127.0.0.1:9000/blockchain
MINIO_ENDPOINT=127.0.0.1:9001

## v1 与 v2 的写路径是否落到同一批表（按 handler 归属）
v1 cases 写入表：1 处 store 实现
v2 cases 复用同一 store 包：

## 结论口径
v1 与 v2 是同一后端进程的不同路由前缀，写入同一 store 层与同一批表；
不存在第二套数据库/表作为并行事实源，因此不存在双写。
唯一需要避免的是同一对象被两个前端并发编辑：v2 用 revision/If-Match 进行并发控制，
灰度期旧前端仍可写同一对象，属于**并发编辑**而非双写；已由 S7-RELEASE-02 的旧入口只读步骤消除。
