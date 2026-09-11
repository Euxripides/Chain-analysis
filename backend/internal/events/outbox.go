package events

import (
	"context"
	"log/slog"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

// RunOutbox publishes transactionally recorded domain events. Multiple API or
// worker processes may run it safely because rows are claimed with SKIP LOCKED.
func RunOutbox(ctx context.Context, pool *pgxpool.Pool, bus *Bus, log *slog.Logger) {
	if pool == nil || bus == nil || !bus.Enabled() {
		log.Warn("outbox publisher disabled", "reason", "event bus unavailable")
		return
	}
	ticker := time.NewTicker(500 * time.Millisecond)
	defer ticker.Stop()
	for {
		if err := publishOutboxBatch(ctx, pool, bus); err != nil && ctx.Err() == nil {
			log.Warn("outbox publish batch failed", "err", err)
		}
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
		}
	}
}

func publishOutboxBatch(ctx context.Context, pool *pgxpool.Pool, bus *Bus) error {
	tx, err := pool.BeginTx(ctx, pgx.TxOptions{})
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	rows, err := tx.Query(ctx, `SELECT id::text,subject,envelope FROM outbox_events WHERE published_at IS NULL AND available_at<=now() ORDER BY created_at LIMIT 100 FOR UPDATE SKIP LOCKED`)
	if err != nil {
		return err
	}
	type item struct {
		id, subject string
		body        []byte
	}
	var items []item
	for rows.Next() {
		var it item
		if err := rows.Scan(&it.id, &it.subject, &it.body); err != nil {
			rows.Close()
			return err
		}
		items = append(items, it)
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return err
	}
	for _, it := range items {
		publishCtx, cancel := context.WithTimeout(ctx, 5*time.Second)
		err := bus.PublishRaw(publishCtx, it.subject, it.body, it.id)
		cancel()
		if err != nil {
			if _, updateErr := tx.Exec(ctx, `UPDATE outbox_events SET attempts=attempts+1,last_error=$2,available_at=now()+LEAST(interval '5 minutes', interval '1 second' * power(2, LEAST(attempts,8))) WHERE id=$1`, it.id, err.Error()); updateErr != nil {
				return updateErr
			}
			continue
		}
		if _, err := tx.Exec(ctx, `UPDATE outbox_events SET published_at=now(),attempts=attempts+1,last_error='' WHERE id=$1`, it.id); err != nil {
			return err
		}
	}
	return tx.Commit(ctx)
}
