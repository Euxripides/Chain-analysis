package audit

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"time"

	"github.com/jackc/pgx/v5"
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
	pool    *pgxpool.Pool
	store   ObjectPutter
	objects ObjectGetter
	log     *slog.Logger
	env     string
}

func NewArchiver(pool *pgxpool.Pool, store ObjectPutter, env string, log *slog.Logger) *Archiver {
	if log == nil {
		log = slog.Default()
	}
	return &Archiver{pool: pool, store: store, log: log, env: env}
}

// WithObjectGetter 注入对象读取能力，用于把归档对象重新读回并核对哈希
// （锚点不可改写，因此必须能证明对象仍是当初承诺的那一份）。返回自身便于链式调用。
func (a *Archiver) WithObjectGetter(g ObjectGetter) *Archiver {
	if a != nil {
		a.objects = g
	}
	return a
}

// ArchiveOnceForDate 为指定日期生成锚点（运维补偿入口；与定时归档同一套不变量）。
func (a *Archiver) ArchiveOnceForDate(ctx context.Context, date time.Time) ([]Anchor, error) {
	return a.ArchiveOnce(ctx, date)
}

// VerifyAnchors 代理到包级实现，供 API 与定时任务复用同一套锚点逐项验证逻辑。
func (a *Archiver) VerifyAnchors(ctx context.Context, chain string) ([]AnchorVerification, error) {
	return VerifyAnchors(ctx, a.pool, a.objects, chain)
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

// loadAnchor 读取某条链某天的既有锚点；不存在返回 (nil, nil)。
func (a *Archiver) loadAnchor(ctx context.Context, chain string, day time.Time) (*Anchor, error) {
	var anchor Anchor
	err := a.pool.QueryRow(ctx, `SELECT chain, anchor_date::text, from_seq, to_seq, head_hash, event_count,
	       object_ref, content_hash FROM audit_chain_anchors WHERE chain = $1 AND anchor_date = $2`,
		chain, day).Scan(&anchor.Chain, &anchor.AnchorDate, &anchor.FromSeq, &anchor.ToSeq,
		&anchor.HeadHash, &anchor.EventCount, &anchor.ObjectRef, &anchor.ContentHash)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	return &anchor, nil
}

func (a *Archiver) archiveChain(ctx context.Context, chain string, day, from, to time.Time) (*Anchor, error) {
	reader, ok := chainReaders[chain]
	if !ok {
		return nil, fmt.Errorf("unknown chain %q", chain)
	}
	// 0055 起锚点内容不可改写：已存在则回读并复核其仍然自洽，绝不重写、绝不重传对象。
	existing, err := a.loadAnchor(ctx, chain, day)
	if err != nil {
		return nil, err
	}
	if existing != nil {
		return nil, a.reconfirmAnchor(ctx, chain, existing)
	}

	// 统计窗口内的事件数；窗口为空则跳过（不产生空锚点）。
	var count int64
	var windowMaxSeq int64
	err = a.pool.QueryRow(ctx, fmt.Sprintf(`
        SELECT count(*), COALESCE(max(seq), 0)
        FROM %s WHERE chain = $1 AND occurred_at >= $2 AND occurred_at < $3`, reader.table),
		chain, from, to).Scan(&count, &windowMaxSeq)
	if err != nil {
		return nil, err
	}
	if count == 0 {
		return nil, nil
	}
	var fromSeq int64
	if err := a.pool.QueryRow(ctx,
		fmt.Sprintf(`SELECT COALESCE(min(seq),0) FROM %s WHERE chain=$1 AND occurred_at >= $2 AND occurred_at < $3`, reader.table),
		chain, from, to).Scan(&fromSeq); err != nil {
		return nil, err
	}
	// head_hash 必须是区间末行（to_seq）的 event_hash。
	// 0055 之前的实现在这里取的是 max(event_hash)，那是区间内的字典序最大值而不是链头，
	// 属于真实缺陷（已在 Sprint 6 审计修复中纠正）；既有锚点不可改写，
	// 由 AnchorHeadMatches 按历史派生方式复核，见 integrity.go。
	var toSeq int64
	var headHash string
	if err := a.pool.QueryRow(ctx, fmt.Sprintf(`
        SELECT seq, event_hash FROM %s WHERE chain = $1 AND seq <= $2 ORDER BY seq DESC LIMIT 1`, reader.table),
		chain, windowMaxSeq).Scan(&toSeq, &headHash); err != nil {
		return nil, err
	}
	// Sprint 6 审计修复：锚点区间不得跨越已封存的完整性边界。
	if incident, err := LoadIncident(ctx, a.pool, chain); err != nil {
		return nil, err
	} else if incident != nil && fromSeq < incident.TrustedFromSeq {
		if toSeq < incident.TrustedFromSeq {
			return nil, nil // 该窗口内没有可信起点之后的事件，不产生锚点
		}
		fromSeq = incident.TrustedFromSeq
		count = toSeq - fromSeq + 1
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

	tag, err := a.pool.Exec(ctx, `
        INSERT INTO audit_chain_anchors (chain, anchor_date, from_seq, to_seq, head_hash, event_count, object_ref, content_hash)
        VALUES ($1,$2,$3,$4,$5,$6,$7,$8)
        ON CONFLICT (chain, anchor_date) DO NOTHING`,
		chain, day, fromSeq, toSeq, headHash, count, anchor.ObjectRef, anchor.ContentHash)
	if err != nil {
		return nil, err
	}
	if tag.RowsAffected() == 0 {
		// 竞态兜底：并发写入者先落库。0055 起锚点不可改写，改为复核既有行。
		raced, err := a.loadAnchor(ctx, chain, day)
		if err != nil {
			return nil, err
		}
		if raced == nil {
			return nil, fmt.Errorf("anchor %s/%s conflicted but is not readable", chain, anchor.AnchorDate)
		}
		if err := a.reconfirmAnchor(ctx, chain, raced); err != nil {
			return nil, err
		}
		if a.objects != nil {
			if got, ok := ReadArchiveHash(ctx, a.objects, raced.ObjectRef, raced.ContentHash); !ok {
				return nil, fmt.Errorf("anchor %s/%s archive object mismatch after concurrent write (object_hash=%s)",
					chain, raced.AnchorDate, got)
			}
		}
		return nil, nil
	}
	return &anchor, nil
}

// reconfirmAnchor 复核一个既有锚点：区间复算必须通过、head_hash 必须与区间事件一致
// （同时支持 0055 之前的历史派生方式）、归档对象哈希必须与记录的 content_hash 一致。
// 只读：不修改锚点行。
func (a *Archiver) reconfirmAnchor(ctx context.Context, chain string, existing *Anchor) error {
	res, err := VerifyChain(ctx, a.pool, chain, existing.FromSeq, existing.ToSeq)
	if err != nil {
		return err
	}
	derivation, err := AnchorHeadMatches(ctx, a.pool, chain, existing.FromSeq, existing.ToSeq, existing.HeadHash)
	if err != nil {
		return err
	}
	if !res.Valid || derivation == "" || res.Checked != existing.EventCount {
		a.log.Error("existing audit anchor does not verify; it is immutable and was not rewritten",
			"chain", chain, "date", existing.AnchorDate, "reason", res.Reason,
			"checked", res.Checked, "event_count", existing.EventCount, "derivation", derivation)
		return fmt.Errorf("existing anchor %s/%s verification failed: %s", chain, existing.AnchorDate, res.Reason)
	}
	if a.objects != nil {
		if _, ok := ReadArchiveHash(ctx, a.objects, existing.ObjectRef, existing.ContentHash); !ok {
			a.log.Error("existing audit anchor archive object missing or mismatched",
				"chain", chain, "date", existing.AnchorDate, "object_ref", existing.ObjectRef)
			return fmt.Errorf("existing anchor %s/%s archive object missing or mismatched", chain, existing.AnchorDate)
		}
	}
	a.log.Info("audit anchor already exists and reconfirmed; kept unchanged",
		"chain", chain, "date", existing.AnchorDate, "from", existing.FromSeq, "to", existing.ToSeq,
		"checked", res.Checked, "derivation", derivation)
	return nil
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
