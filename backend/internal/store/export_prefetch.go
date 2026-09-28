package store

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"time"

	"github.com/google/uuid"
)

/*
 * 导出预生成（prefetch）账本。
 *
 * 用户在导出表单上停留的 30-60 秒足够把产物先生成好；点"导出"时若命中同一份规格
 * 的已完成产物，就直接把它提升成正式导出，用户等待接近零。
 *
 * 为什么单独一张表而不是往 exports 里塞：预生成是投机产物——大部分草稿改到一半就被
 * 覆盖，最终并不会被导出。把它们写进 exports 会污染用户的导出列表与审计口径。
 */

// PrefetchSpecHash 把一份导出规格算成稳定的短哈希。
//
// 必须与"用户点导出"时算出的哈希一致，否则永远命不中。因此哈希输入必须是
// **规格快照本身**（createExportV2 里已经在算的那份 JSON），而不是各字段拼接——
// 后者在字段顺序或默认值变化时会静默失配。
func PrefetchSpecHash(snapshot []byte) string {
	sum := sha256.Sum256(snapshot)
	return hex.EncodeToString(sum[:16])
}

// PrefetchRow 是 export_prefetch 的一行。
type PrefetchRow struct {
	ID           string
	SpecHash     string
	Dataset      string
	Format       string
	Zip          bool
	Status       string
	ObjectRef    string
	ObjectBucket string
	SHA256       string
	Rows         int64
	Error        string
}

// CreatePrefetch 预登记一条预生成任务（PENDING）。同租户 + 同规格已存在未过期行时
// 返回该行的 ID 且 created=false，避免表单每变一次就堆一条。
func (s *Store) CreatePrefetch(ctx context.Context, specHash, orgID, wsID string, createdBy *string,
	dataset, format string, zip bool, ttl time.Duration) (id string, created bool, err error) {
	var existing string
	err = s.Pool.QueryRow(ctx,
		"SELECT id::text FROM export_prefetch WHERE organization_id=$1 AND workspace_id=$2 AND spec_hash=$3 "+
			"AND expires_at > now() ORDER BY created_at DESC LIMIT 1",
		orgID, wsID, specHash).Scan(&existing)
	if err == nil {
		return existing, false, nil
	}
	id = uuid.NewString()
	_, err = s.Pool.Exec(ctx,
		"INSERT INTO export_prefetch (id, spec_hash, organization_id, workspace_id, created_by, dataset, format, zip, status, expires_at) "+
			"VALUES ($1,$2,$3,$4,$5,$6,$7,$8,'PENDING', now() + $9::interval)",
		id, specHash, orgID, wsID, createdBy, dataset, format, zip, ttl.String())
	if err != nil {
		return "", false, err
	}
	return id, true, nil
}

// UpdatePrefetch 写入预生成结果。
func (s *Store) UpdatePrefetch(ctx context.Context, id, status, objectRef, bucket, sha string, rows int64, errMsg string) error {
	_, err := s.Pool.Exec(ctx,
		"UPDATE export_prefetch SET status=$2, object_ref=$3, object_bucket=$4, object_sha256=$5, rows=$6, error=$7, updated_at=now() WHERE id=$1",
		id, status, objectRef, bucket, sha, rows, errMsg)
	return err
}

// FindCompletedPrefetch 找同租户 + 同规格的已完成预生成产物。
//
// 只返回 COMPLETED 且对象名非空的行：PENDING/RUNNING 命中没有意义（用户还是要等），
// FAILED 更不能命中（否则用户拿到一个永远失败的结果）。
func (s *Store) FindCompletedPrefetch(ctx context.Context, specHash, orgID, wsID string) (*PrefetchRow, error) {
	row := &PrefetchRow{}
	err := s.Pool.QueryRow(ctx,
		"SELECT id::text, spec_hash, dataset, format, zip, status, object_ref, object_bucket, object_sha256, rows, error "+
			"FROM export_prefetch WHERE organization_id=$1 AND workspace_id=$2 AND spec_hash=$3 "+
			"AND status='COMPLETED' AND object_ref<>'' AND expires_at > now() "+
			"ORDER BY created_at DESC LIMIT 1",
		orgID, wsID, specHash).Scan(&row.ID, &row.SpecHash, &row.Dataset, &row.Format, &row.Zip,
		&row.Status, &row.ObjectRef, &row.ObjectBucket, &row.SHA256, &row.Rows, &row.Error)
	if err != nil {
		return nil, err
	}
	return row, nil
}

// ExpirePrefetches 返回并删除已过期的预生成行（对象由调用方删除）。
//
// 预生成是投机产物：过期即弃，不需要保留审计价值；这里连同对象名一起返回，
// 让调用方把对象也清掉，避免 MinIO 里堆积没人认领的文件。
func (s *Store) ExpirePrefetches(ctx context.Context, limit int) ([]PrefetchRow, error) {
	if limit <= 0 {
		limit = 200
	}
	rows, err := s.Pool.Query(ctx,
		"DELETE FROM export_prefetch WHERE id IN ("+
			"SELECT id FROM export_prefetch WHERE expires_at <= now() ORDER BY expires_at LIMIT $1"+
			") RETURNING id::text, object_bucket, object_ref", limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []PrefetchRow
	for rows.Next() {
		var r PrefetchRow
		if err := rows.Scan(&r.ID, &r.ObjectBucket, &r.ObjectRef); err != nil {
			return nil, err
		}
		out = append(out, r)
	}
	return out, rows.Err()
}
