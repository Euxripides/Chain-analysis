package store

import (
	"context"
	"fmt"

	"github.com/blockchain-platform/blockchain-platform/internal/model"
)

func (s *Store) PageArchiveKey(ctx context.Context, id string) (string, error) {
	var key string
	err := s.Pool.QueryRow(ctx, "SELECT raw_object_key FROM download_pages WHERE id=$1 AND raw_uploaded_at IS NOT NULL", id).Scan(&key)
	return key, err
}

// A persisted lease prevents a full in-memory queue from dropping archive work.
func (s *Store) ClaimPageArchives(ctx context.Context, limit int) ([]model.Page, error) {
	rows, err := s.Pool.Query(ctx, `WITH picked AS (
 SELECT p.id FROM download_pages p JOIN download_jobs j ON j.id=p.job_id
 WHERE p.raw_archive_enabled AND p.raw_uploaded_at IS NULL AND p.next_upload_at<=now()
 AND p.status IN ('DURABLE','FAILED','INGESTED','VALIDATED')
 AND j.status NOT IN ('CANCELLED','ABORTED_BY_USER')
 ORDER BY p.next_upload_at,p.id LIMIT $1 FOR UPDATE OF p SKIP LOCKED)
 UPDATE download_pages p SET next_upload_at=now()+interval '5 minutes'
 FROM picked WHERE p.id=picked.id
 RETURNING p.id,p.job_id,p.shard_id,p.dataset,p.page_no,p.query_hash,p.raw_path,p.raw_sha256,p.rows_fetched`, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var pages []model.Page
	for rows.Next() {
		var p model.Page
		if err := rows.Scan(&p.ID, &p.JobID, &p.ShardID, &p.Dataset, &p.PageNo, &p.QueryHash, &p.RawPath, &p.RawSHA256, &p.RowsFetched); err != nil {
			return nil, err
		}
		pages = append(pages, p)
	}
	return pages, rows.Err()
}

func (s *Store) CompletePageArchive(ctx context.Context, id, key, sha256 string) error {
	if key == "" || sha256 == "" {
		return fmt.Errorf("empty raw archive key or checksum")
	}
	// A refetched page can retain its ID. An older upload must not replace the
	// new raw object or authorize deletion of the refetched spool file.
	tag, err := s.Pool.Exec(ctx, "UPDATE download_pages SET raw_object_key=$2,raw_uploaded_at=now() WHERE id=$1 AND raw_sha256=$3", id, key, sha256)
	if err == nil && tag.RowsAffected() != 1 {
		return fmt.Errorf("archive page missing: %s", id)
	}
	return err
}

func (s *Store) RetryPageArchive(ctx context.Context, id string) error {
	_, err := s.Pool.Exec(ctx, `UPDATE download_pages SET upload_attempts=upload_attempts+1,
 next_upload_at=now()+make_interval(secs=>LEAST(600,5*(2^LEAST(upload_attempts,7))::int))
 WHERE id=$1 AND raw_uploaded_at IS NULL`, id)
	return err
}

func (s *Store) ListArchiveReadyShards(ctx context.Context, limit int) ([]model.Shard, error) {
	rows, err := s.Pool.Query(ctx, "SELECT "+shardCols+" FROM download_shards s WHERE status='COMPLETED' AND raw_object='' AND EXISTS(SELECT 1 FROM download_pages p WHERE p.shard_id=s.id AND p.raw_archive_enabled) AND NOT EXISTS(SELECT 1 FROM download_pages p WHERE p.shard_id=s.id AND p.raw_uploaded_at IS NULL) ORDER BY updated_at,id LIMIT $1", limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var shards []model.Shard
	for rows.Next() {
		sh, err := scanShard(rows)
		if err != nil {
			return nil, err
		}
		shards = append(shards, *sh)
	}
	return shards, rows.Err()
}
