// Package migrations 内嵌 SQL 迁移文件并提供执行器。
package migrations

import (
	"context"
	"embed"
	"fmt"
	"sort"
	"strings"

	"github.com/jackc/pgx/v5/pgxpool"
)

//go:embed postgres/*.sql clickhouse/*.sql
var FS embed.FS

func PostgresFiles() []string {
	es, _ := FS.ReadDir("postgres")
	var out []string
	for _, e := range es {
		out = append(out, "postgres/"+e.Name())
	}
	sort.Strings(out)
	return out
}

func ClickHouseFiles() []string {
	es, _ := FS.ReadDir("clickhouse")
	var out []string
	for _, e := range es {
		out = append(out, "clickhouse/"+e.Name())
	}
	sort.Strings(out)
	return out
}

// ApplyPostgres 按文件名顺序执行未应用的迁移。
func ApplyPostgres(ctx context.Context, pool *pgxpool.Pool) error {
	conn, err := pool.Acquire(ctx)
	if err != nil {
		return fmt.Errorf("acquire migration connection: %w", err)
	}
	defer conn.Release()
	if _, err := conn.Exec(ctx, `SELECT pg_advisory_lock(hashtext('blockchain-platform:postgres-migrations'))`); err != nil {
		return fmt.Errorf("acquire migration lock: %w", err)
	}
	defer func() {
		_, _ = conn.Exec(context.Background(), `SELECT pg_advisory_unlock(hashtext('blockchain-platform:postgres-migrations'))`)
	}()

	_, err = conn.Exec(ctx, `CREATE TABLE IF NOT EXISTS schema_migrations (name TEXT PRIMARY KEY, applied_at TIMESTAMPTZ NOT NULL DEFAULT now())`)
	if err != nil {
		return fmt.Errorf("create schema_migrations: %w", err)
	}
	for _, name := range PostgresFiles() {
		var exists bool
		err := conn.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM schema_migrations WHERE name=$1)`, name).Scan(&exists)
		if err != nil {
			return err
		}
		if exists {
			continue
		}
		body, err := FS.ReadFile(name)
		if err != nil {
			return err
		}
		tx, err := conn.Begin(ctx)
		if err != nil {
			return err
		}
		if _, err := tx.Exec(ctx, string(body)); err != nil {
			_ = tx.Rollback(ctx)
			return fmt.Errorf("migration %s: %w", name, err)
		}
		if _, err := tx.Exec(ctx, `INSERT INTO schema_migrations(name) VALUES($1)`, name); err != nil {
			_ = tx.Rollback(ctx)
			return err
		}
		if err := tx.Commit(ctx); err != nil {
			return err
		}
	}
	return nil
}

// ApplyClickHouse 逐条语句执行（表创建均为幂等）。
func ApplyClickHouse(ctx context.Context, exec func(ctx context.Context, query string) error) error {
	for _, name := range ClickHouseFiles() {
		body, err := FS.ReadFile(name)
		if err != nil {
			return err
		}
		for _, stmt := range splitStatements(string(body)) {
			if err := exec(ctx, stmt); err != nil {
				return fmt.Errorf("clickhouse migration %s: %w", name, err)
			}
		}
	}
	return nil
}

func splitStatements(s string) []string {
	var out []string
	for _, part := range strings.Split(s, ";") {
		p := strings.TrimSpace(part)
		if p != "" {
			out = append(out, p)
		}
	}
	return out
}
