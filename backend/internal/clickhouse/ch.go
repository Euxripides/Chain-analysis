// Package clickhouse：ClickHouse 数据面连接。
package clickhouse

import (
	"context"
	"fmt"
	"net/url"
	"time"

	ch "github.com/ClickHouse/clickhouse-go/v2"
	"github.com/ClickHouse/clickhouse-go/v2/lib/driver"
)

type Conn struct {
	db driver.Conn
}

func New(ctx context.Context, dsn string) (*Conn, error) {
	u, err := url.Parse(dsn)
	if err != nil {
		return nil, fmt.Errorf("parse clickhouse url: %w", err)
	}
	var user, pass string
	if u.User != nil {
		user = u.User.Username()
		pass, _ = u.User.Password()
	}
	dbName := "blockchain"
	if p := u.Path; p != "" && p != "/" {
		dbName = p[1:]
	}
	opts := &ch.Options{
		Addr: []string{u.Host},
		Auth: ch.Auth{Database: dbName, Username: user, Password: pass},
		DialTimeout:      10 * time.Second,
		MaxOpenConns:     16,
		MaxIdleConns:     8,
		ConnMaxLifetime:  time.Hour,
		Compression:      &ch.Compression{Method: ch.CompressionLZ4},
		Settings: ch.Settings{
			"async_insert":           1,
			"wait_for_async_insert":  1,
			"async_insert_max_data_size": "10000000",
		},
	}
	db, err := ch.Open(opts)
	if err != nil {
		return nil, fmt.Errorf("clickhouse open: %w", err)
	}
	if err := db.Ping(ctx); err != nil {
		return nil, fmt.Errorf("clickhouse ping: %w", err)
	}
	return &Conn{db: db}, nil
}

func (c *Conn) DB() driver.Conn { return c.db }

func (c *Conn) Exec(ctx context.Context, query string) error {
	return c.db.Exec(ctx, query)
}

func (c *Conn) Close() error { return c.db.Close() }
