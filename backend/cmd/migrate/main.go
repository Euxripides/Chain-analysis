// 迁移工具：go run ./cmd/migrate
package main

import (
	"context"
	"fmt"
	"os"

	"github.com/blockchain-platform/blockchain-platform/internal/clickhouse"
	"github.com/blockchain-platform/blockchain-platform/internal/config"
	"github.com/blockchain-platform/blockchain-platform/internal/logger"
	"github.com/blockchain-platform/blockchain-platform/internal/store"
	"github.com/blockchain-platform/blockchain-platform/migrations"
)

func main() {
	ctx := context.Background()
	cfg := config.Load()
	log := logger.New("info")
	// 迁移必须使用具备 DDL 权限的运维账号（MIGRATION_DATABASE_URL，未配置时回落 DATABASE_URL）。
	st, err := store.New(ctx, cfg.MigrationDatabaseURL)
	if err != nil {
		log.Error("postgres connect failed", "err", err)
		os.Exit(1)
	}
	defer st.Close()
	if err := migrations.ApplyPostgres(ctx, st.Pool.Raw()); err != nil {
		log.Error("postgres migrate failed", "err", err)
		os.Exit(1)
	}
	fmt.Println("postgres migrations applied:", migrations.PostgresFiles())

	ch, err := clickhouse.New(ctx, cfg.ClickHouseURL)
	if err != nil {
		log.Error("clickhouse connect failed", "err", err)
		os.Exit(1)
	}
	defer ch.Close()
	if err := migrations.ApplyClickHouse(ctx, ch.Exec); err != nil {
		log.Error("clickhouse migrate failed", "err", err)
		os.Exit(1)
	}
	fmt.Println("clickhouse migrations applied:", migrations.ClickHouseFiles())
	fmt.Println("migrate done")
}
