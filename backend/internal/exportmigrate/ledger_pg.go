package exportmigrate

import (
	"context"
	"fmt"

	"github.com/blockchain-platform/blockchain-platform/internal/dbtx"
)

// PGLedger 是 0058 账本表（export_object_migrations）的实现。
// 主键 (export_id, to_bucket) 提供幂等；status 提供续跑。
type PGLedger struct {
	Pool *dbtx.Pool
}

func (l *PGLedger) Get(ctx context.Context, exportID, toBucket string) (*Row, error) {
	row := &Row{}
	err := l.Pool.QueryRow(ctx, `
		SELECT export_id::text, object_ref, from_bucket, to_bucket, bytes,
		       source_sha256, destination_sha256, pg_sha256, status, attempts, last_error
		FROM export_object_migrations WHERE export_id=$1 AND to_bucket=$2`, exportID, toBucket).
		Scan(&row.ExportID, &row.ObjectRef, &row.FromBucket, &row.ToBucket, &row.Bytes,
			&row.SourceSHA256, &row.DestinationSHA256, &row.PGSHA256, &row.Status, &row.Attempts, &row.LastError)
	if err != nil {
		if isNoRows(err) {
			return nil, nil
		}
		return nil, err
	}
	return row, nil
}

func (l *PGLedger) Upsert(ctx context.Context, row Row) error {
	_, err := l.Pool.Exec(ctx, `
		INSERT INTO export_object_migrations
			(export_id, object_ref, from_bucket, to_bucket, bytes, source_sha256, destination_sha256,
			 pg_sha256, status, attempts, last_error, updated_at)
		VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,1,$10, now())
		ON CONFLICT (export_id, to_bucket) DO UPDATE SET
			object_ref = EXCLUDED.object_ref,
			from_bucket = EXCLUDED.from_bucket,
			bytes = EXCLUDED.bytes,
			source_sha256 = EXCLUDED.source_sha256,
			destination_sha256 = EXCLUDED.destination_sha256,
			pg_sha256 = EXCLUDED.pg_sha256,
			status = EXCLUDED.status,
			attempts = export_object_migrations.attempts + 1,
			last_error = EXCLUDED.last_error,
			verified_at = CASE WHEN EXCLUDED.status IN ('VERIFIED','ALREADY_VERIFIED')
			                   THEN COALESCE(export_object_migrations.verified_at, now())
			                   ELSE export_object_migrations.verified_at END,
			updated_at = now()`,
		row.ExportID, row.ObjectRef, row.FromBucket, row.ToBucket, row.Bytes, row.SourceSHA256,
		row.DestinationSHA256, row.PGSHA256, row.Status, row.LastError)
	if err != nil {
		return fmt.Errorf("upsert ledger %s: %w", row.ObjectRef, err)
	}
	return nil
}

func (l *PGLedger) List(ctx context.Context, toBucket string) ([]Row, error) {
	rows, err := l.Pool.Query(ctx, `
		SELECT export_id::text, object_ref, from_bucket, to_bucket, bytes,
		       source_sha256, destination_sha256, pg_sha256, status, attempts, last_error
		FROM export_object_migrations WHERE to_bucket=$1 ORDER BY object_ref`, toBucket)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []Row
	for rows.Next() {
		var row Row
		if err := rows.Scan(&row.ExportID, &row.ObjectRef, &row.FromBucket, &row.ToBucket, &row.Bytes,
			&row.SourceSHA256, &row.DestinationSHA256, &row.PGSHA256, &row.Status, &row.Attempts, &row.LastError); err != nil {
			return nil, err
		}
		out = append(out, row)
	}
	return out, rows.Err()
}
