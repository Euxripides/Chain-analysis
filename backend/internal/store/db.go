// Package store 提供 PostgreSQL 控制面数据访问。
package store

import (
	"context"
	"fmt"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

type Store struct {
	Pool *pgxpool.Pool
}

// New 打开连接池（连接账号为迁移/运维账号）。
func New(ctx context.Context, url string) (*Store, error) {
	return NewWithRuntimeRole(ctx, url, "")
}

// NewWithRuntimeRole 在 runtimeRole 非空时，对每个新连接执行 SET ROLE。
//
// 目的（§27.6 AUDIT-003）：连接账号（表 owner / 超级用户）只用于迁移与运维，
// 运行期语句统一降权到受限角色，从数据库层拒绝 TRUNCATE、DROP 以及对审计表的
// UPDATE / DELETE，而不是只依赖应用代码自律。
func NewWithRuntimeRole(ctx context.Context, url, runtimeRole string) (*Store, error) {
	cfg, err := pgxpool.ParseConfig(url)
	if err != nil {
		return nil, fmt.Errorf("pgxpool parse: %w", err)
	}
	if runtimeRole != "" {
		role := pgx.Identifier{runtimeRole}.Sanitize()
		cfg.AfterConnect = func(ctx context.Context, conn *pgx.Conn) error {
			_, err := conn.Exec(ctx, "SET ROLE "+role)
			return err
		}
	}
	pool, err := pgxpool.NewWithConfig(ctx, cfg)
	if err != nil {
		return nil, fmt.Errorf("pgxpool: %w", err)
	}
	if err := pool.Ping(ctx); err != nil {
		return nil, fmt.Errorf("ping postgres: %w", err)
	}
	return &Store{Pool: pool}, nil
}

func (s *Store) Close() {
	s.Pool.Close()
}
