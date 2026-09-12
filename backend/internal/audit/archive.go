package audit

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"log/slog"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
)

// ArchiveBucket 是审计锚点与归档对象的专用 bucket。
// 与 raw / exports 分离，便于单独配置对象锁与生命周期（§6.6.5）。
const ArchiveBucket = "blockchain-audit"

// ObjectPutter 是归档所需的最小对象存储接口（由 rawstore.Store 满足）。
// 定义成接口是为了让 audit 包不反向依赖 rawstore。
type ObjectPutter interface {
	PutNamed(ctx context.Context, bucket, key string, data []byte, contentType string) (int64, error)
	EnsureNamedBucket(ctx context.Context, name string) error
}

// Anchor 是一次每日归档的产物，同时落库与落对象存储。
type Anchor struct {
	Chain       string    `json:"chain"`
	AnchorDate  string    `json:"anchor_date"`
	FromSeq     int64     `json:"from_seq"`
	ToSeq       int64     `json:"to_seq"`
	HeadHash    string    `json:"head_hash"`
	EventCount  int64     `json:"event_count"`
	ObjectRef   string    `json:"object_ref"`
	ContentHash string    `json:"content_hash"`
	GeneratedAt time.Time `json:"generated_at"`
}

// Archiver 负责每日生成哈希链锚点并归档到 MinIO（S3-BE-02）。
type Archiver struct {
	pool  *pgxpool.Pool
	store ObjectPutter
	log   *slog.Logger
	env   string
}

func NewArchiver(pool *pgxpool.Pool, store ObjectPutter, env string, log *slog.Logger) *Archiver {
	if log == nil {
		log = slog.Default()
	}
	return &Archiver{pool: pool, store: store, log: log, env: env}
}

// ArchiveOnce 为每一天生成一个锚点。date 为零值时取"昨天"（UTC），
// 以保证归档的是已经闭合的时间窗口，而不是仍在写入的当天。
func (a *Archiver) ArchiveOnce(ctx context.Context, date time.Time) ([]Anchor, error) {
	if a == nil || a.pool == nil {
		return nil, nil
	}
	if date.IsZero() {
		date = time.Now().UTC().AddDate(0, 0, -1)
	}
	day := date.UTC().Truncate(24 * time.Hour)
	from := day
	to := day.Add(24 * time.Hour)

	out := make([]Anchor, 0, 2)
	for _, chain := range []string{ChainAudit, ChainLogin} {
		anchor, err := a.archiveChain(ctx, chain, day, from, to)
		if err != nil {
			return out, err
		}
		if anchor != nil {
			out = append(out, *anchor)
		}
	}
	return out, nil
}

func (a *Archiver) archiveChain(ctx context.Context, chain string, day, from, to time.Time) (*Anchor, error) {
	reader, ok := chainReaders[chain]
	if !ok {
		return nil, fmt.Errorf("unknown chain %q", chain)
	}
	// 统计窗口内的事件数并取最后一条的哈希；窗口为空则跳过（不产生空锚点）。
	var count int64
	var toSeq int64
	var headHash string
	err := a.pool.QueryRow(ctx, fmt.Sprintf(`
        SELECT count(*), COALESCE(max(seq), 0)
        FROM %s WHERE chain = $1 AND occurred_at >= $2 AND occurred_at < $3`, reader.table),
		chain, from, to).Scan(&count, &toSeq)
	if err != nil {
		return nil, err
	}
	if count == 0 {
		return nil, nil
	}
	if err := a.pool.QueryRow(ctx,
		fmt.Sprintf(`SELECT COALESCE(max(seq),0), COALESCE(max(event_hash),'') FROM %s WHERE chain=$1 AND seq <= $2`, reader.table),
		chain, toSeq).Scan(&toSeq, &headHash); err != nil {
		return nil, err
	}
	var fromSeq int64
	if err := a.pool.QueryRow(ctx,
		fmt.Sprintf(`SELECT COALESCE(min(seq),0) FROM %s WHERE chain=$1 AND occurred_at >= $2 AND occurred_at < $3`, reader.table),
		chain, from, to).Scan(&fromSeq); err != nil {
		return nil, err
	}

	anchor := Anchor{
		Chain: chain, AnchorDate: day.Format("2006-01-02"),
		FromSeq: fromSeq, ToSeq: toSeq, HeadHash: headHash,
		EventCount: count, GeneratedAt: time.Now().UTC(),
	}

	// manifest 里带上链头与区间，任何人拿到对象都能独立复算并比对。
	manifest := map[string]any{
		"chain":        chain,
		"anchor_date":  anchor.AnchorDate,
		"from_seq":     fromSeq,
		"to_seq":       toSeq,
		"head_hash":    headHash,
		"event_count":  count,
		"environment":  a.env,
		"generated_at": anchor.GeneratedAt.Format(time.RFC3339Nano),
		"algorithm":    "sha256(chain|seq|prev_hash|sha256(canonical_payload))",
		"verify_hint":  "POST /api/admin/v1/audit/verify {\"chain\":\"<chain>\",\"from_seq\":<from>,\"to_seq\":<to>}",
	}
	body, err := json.MarshalIndent(manifest, "", "  ")
	if err != nil {
		return nil, err
	}
	sum := sha256.Sum256(body)
	anchor.ContentHash = hex.EncodeToString(sum[:])
	anchor.ObjectRef = fmt.Sprintf("audit/%s/%s/anchor.json", chain, anchor.AnchorDate)

	if a.store != nil {
		if err := a.store.EnsureNamedBucket(ctx, ArchiveBucket); err != nil {
			return nil, fmt.Errorf("ensure audit bucket: %w", err)
		}
		if _, err := a.store.PutNamed(ctx, ArchiveBucket, anchor.ObjectRef, body, "application/json"); err != nil {
			return nil, fmt.Errorf("upload anchor: %w", err)
		}
	}

	if _, err := a.pool.Exec(ctx, `
        INSERT INTO audit_chain_anchors (chain, anchor_date, from_seq, to_seq, head_hash, event_count, object_ref, content_hash)
        VALUES ($1,$2,$3,$4,$5,$6,$7,$8)
        ON CONFLICT (chain, anchor_date) DO UPDATE
            SET from_seq = EXCLUDED.from_seq, to_seq = EXCLUDED.to_seq, head_hash = EXCLUDED.head_hash,
                event_count = EXCLUDED.event_count, object_ref = EXCLUDED.object_ref,
                content_hash = EXCLUDED.content_hash`,
		chain, day, fromSeq, toSeq, headHash, count, anchor.ObjectRef, anchor.ContentHash); err != nil {
		return nil, err
	}
	return &anchor, nil
}

// VerifyAll 重算两条链并返回结果，供定时校验器与后台"校验"按钮复用。
func (a *Archiver) VerifyAll(ctx context.Context) (map[string]VerifyChainResult, error) {
	out := map[string]VerifyChainResult{}
	if a == nil || a.pool == nil {
		return out, nil
	}
	for _, chain := range []string{ChainAudit, ChainLogin} {
		res, err := VerifyChain(ctx, a.pool, chain, 0, 0)
		if err != nil {
			return out, fmt.Errorf("verify chain %s: %w", chain, err)
		}
		out[chain] = res
	}
	return out, nil
}

// OutboxStats 是 Outbox 积压指标的数据源（§28.5 S3-OBS-01）。
type OutboxStats struct {
	Pending         int64
	OldestAgeSecond float64
	FailedTotal     int64
}

func ReadOutboxStats(ctx context.Context, pool *pgxpool.Pool) (OutboxStats, error) {
	var st OutboxStats
	if pool == nil {
		return st, nil
	}
	err := pool.QueryRow(ctx, `
        SELECT count(*) FILTER (WHERE published_at IS NULL),
               COALESCE(EXTRACT(EPOCH FROM (now() - min(created_at) FILTER (WHERE published_at IS NULL))), 0),
               count(*) FILTER (WHERE published_at IS NULL AND attempts > 0)
        FROM outbox_events`).Scan(&st.Pending, &st.OldestAgeSecond, &st.FailedTotal)
	return st, err
}
