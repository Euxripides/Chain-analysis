package tokenmeta

import (
	"compress/gzip"
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"strings"
	"time"

	"github.com/google/uuid"
)

type BackfillRunner struct {
	Service             *Service
	BatchSize           int
	Interval, BatchIdle time.Duration
	MaxRetries          int
	Log                 *slog.Logger
}

func (b *BackfillRunner) Run(ctx context.Context) {
	// 历史目录扫描可能运行数小时，RPC 解析必须独立运行，不能被其阻塞。
	for i := 0; i < 4; i++ {
		go b.runResolver(ctx)
	}
	b.runBackfill(ctx)
}

func (b *BackfillRunner) runBackfill(ctx context.Context) {
	interval := b.Interval
	if interval <= 0 {
		interval = 10 * time.Second
	}
	ticker := time.NewTicker(interval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			if job, err := b.nextJob(ctx); err == nil && job != "" {
				if err = b.runJob(ctx, job); err != nil {
					b.Log.Error("token metadata backfill failed", "job_id", job, "err", err)
				}
			}
		}
	}
}

func (b *BackfillRunner) runResolver(ctx context.Context) {
	idle := b.Interval
	if idle <= 0 {
		idle = 5 * time.Second
	}
	for {
		if err := ctx.Err(); err != nil {
			return
		}
		ok, err := b.resolveNext(ctx)
		if err != nil {
			b.Log.Warn("token metadata resolve failed", "err", err)
		}
		if ok {
			continue
		}
		select {
		case <-ctx.Done():
			return
		case <-time.After(idle):
		}
	}
}

func (b *BackfillRunner) CreateJob(ctx context.Context, chain string) (string, error) {
	id := uuid.NewString()
	_, err := b.Service.Store.Pool.Exec(ctx, `INSERT INTO token_metadata_backfill_jobs(id,chain,status) VALUES($1,NULLIF($2,''),'pending')`, id, strings.ToLower(strings.TrimSpace(chain)))
	return id, err
}

func (b *BackfillRunner) nextJob(ctx context.Context) (string, error) {
	var id string
	err := b.Service.Store.Pool.QueryRow(ctx, `SELECT id FROM token_metadata_backfill_jobs WHERE status IN ('pending','running') ORDER BY updated_at LIMIT 1`).Scan(&id)
	if err == sql.ErrNoRows {
		return "", nil
	}
	return id, err
}

type cursor struct {
	Chain   string `json:"chain"`
	Address string `json:"address"`
	Phase   string `json:"phase,omitempty"`
	RawID   string `json:"raw_id,omitempty"`
}

func (b *BackfillRunner) runJob(ctx context.Context, id string) error {
	BackfillActive.Set(1)
	defer BackfillActive.Set(0)
	var chain, status string
	var raw []byte
	if err := b.Service.Store.Pool.QueryRow(ctx, `SELECT COALESCE(chain,''),status,cursor_json FROM token_metadata_backfill_jobs WHERE id=$1`, id).Scan(&chain, &status, &raw); err != nil {
		return err
	}
	var cur cursor
	_ = json.Unmarshal(raw, &cur)
	if cur.Phase == "" {
		if cur.Address != "" { // 兼容 0006 已开始执行的 checkpoint。
			cur.Phase = "clickhouse"
		} else {
			cur.Phase = "raw"
		}
	}
	_, _ = b.Service.Store.Pool.Exec(ctx, `UPDATE token_metadata_backfill_jobs SET status='running',started_at=COALESCE(started_at,now()),updated_at=now() WHERE id=$1`, id)
	batch := b.BatchSize
	if batch <= 0 {
		batch = 500
	}
	if cur.Phase == "raw" {
		if err := b.runRawPhase(ctx, id, chain, &cur); err != nil {
			return b.failJob(ctx, id, err)
		}
	}
	for {
		if err := ctx.Err(); err != nil {
			return err
		}
		items, err := b.scanCatalog(ctx, chain, cur, batch)
		if err != nil {
			return b.failJob(ctx, id, err)
		}
		if len(items) == 0 {
			_, err = b.Service.Store.Pool.Exec(ctx, `UPDATE token_metadata_backfill_jobs SET status='completed',completed_at=now(),updated_at=now() WHERE id=$1`, id)
			return err
		}
		complete, partial, failed := int64(0), int64(0), int64(0)
		BackfillTokensTotal.Add(float64(len(items)))
		for _, item := range items {
			if err := b.Service.processCatalogItem(ctx, item); err != nil {
				failed++
				continue
			}
			m, _ := b.Service.Get(ctx, item.Chain, item.ContractAddress)
			if m != nil && m.MetadataStatus == StatusComplete {
				complete++
			} else {
				partial++
			}
			cur = cursor{Chain: item.Chain, Address: item.ContractAddress}
		}
		checkpoint, _ := json.Marshal(cur)
		_, err = b.Service.Store.Pool.Exec(ctx, `UPDATE token_metadata_backfill_jobs SET cursor_json=$2,scanned_tokens=scanned_tokens+$3,completed_tokens=completed_tokens+$4,partial_tokens=partial_tokens+$5,failed_tokens=failed_tokens+$6,updated_at=now() WHERE id=$1`, id, checkpoint, len(items), complete, partial, failed)
		if err != nil {
			return err
		}
		if len(items) < batch {
			_, err = b.Service.Store.Pool.Exec(ctx, `UPDATE token_metadata_backfill_jobs SET status='completed',completed_at=now(),updated_at=now() WHERE id=$1`, id)
			return err
		}
		idle := b.BatchIdle
		if idle <= 0 {
			idle = 200 * time.Millisecond
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(idle):
		}
	}
}

type rawManifest struct {
	Chain string `json:"chain"`
	Parts []struct {
		Key string `json:"key"`
	} `json:"parts"`
}

func (b *BackfillRunner) runRawPhase(ctx context.Context, jobID, requestedChain string, cur *cursor) error {
	if b.Service.Raw == nil {
		cur.Phase = "clickhouse"
		return b.checkpointRaw(ctx, jobID, *cur, 0, 0)
	}
	for {
		rows, err := b.Service.Store.Pool.Query(ctx, `SELECT id::text,raw_object FROM download_shards WHERE dataset='token_transfers' AND status='COMPLETED' AND raw_object<>'' AND id::text>$1 ORDER BY id::text LIMIT 50`, cur.RawID)
		if err != nil {
			return err
		}
		type object struct{ id, key string }
		var objects []object
		for rows.Next() {
			var item object
			if err := rows.Scan(&item.id, &item.key); err != nil {
				rows.Close()
				return err
			}
			objects = append(objects, item)
		}
		err = rows.Err()
		rows.Close()
		if err != nil {
			return err
		}
		if len(objects) == 0 {
			cur.Phase, cur.RawID, cur.Chain, cur.Address = "clickhouse", "", "", ""
			return b.checkpointRaw(ctx, jobID, *cur, 0, 0)
		}
		var objectCount, observationCount int64
		for _, object := range objects {
			var manifest rawManifest
			if err := b.readGzipJSON(ctx, object.key, &manifest); err != nil {
				b.Log.Warn("skip unreadable token raw manifest", "object", object.key, "err", err)
				cur.RawID = object.id
				continue
			}
			if requestedChain != "" && !strings.EqualFold(requestedChain, manifest.Chain) {
				cur.RawID = object.id
				continue
			}
			for _, part := range manifest.Parts {
				var page struct {
					Rows []json.RawMessage `json:"rows"`
				}
				if err := b.readGzipJSON(ctx, part.Key, &page); err != nil {
					b.Log.Warn("skip unreadable token raw part", "object", part.Key, "err", err)
					continue
				}
				n, err := b.Service.StoreRawObservations(ctx, manifest.Chain, page.Rows)
				if err != nil {
					return err
				}
				observationCount += int64(n)
			}
			objectCount++
			cur.RawID = object.id
		}
		if err := b.checkpointRaw(ctx, jobID, *cur, objectCount, observationCount); err != nil {
			return err
		}
	}
}

func (b *BackfillRunner) readGzipJSON(ctx context.Context, key string, target any) error {
	reader, _, err := b.Service.Raw.Open(ctx, key)
	if err != nil {
		return err
	}
	defer reader.Close()
	gz, err := gzip.NewReader(reader)
	if err != nil {
		return err
	}
	defer gz.Close()
	return json.NewDecoder(io.LimitReader(gz, 64<<20)).Decode(target)
}

func (b *BackfillRunner) checkpointRaw(ctx context.Context, jobID string, cur cursor, objects, observations int64) error {
	checkpoint, _ := json.Marshal(cur)
	_, err := b.Service.Store.Pool.Exec(ctx, `UPDATE token_metadata_backfill_jobs SET cursor_json=$2,raw_objects=raw_objects+$3,raw_observations=raw_observations+$4,updated_at=now() WHERE id=$1`, jobID, checkpoint, objects, observations)
	return err
}

func (b *BackfillRunner) scanCatalog(ctx context.Context, chain string, cur cursor, limit int) ([]CatalogItem, error) {
	where := "(chain,token_address) > (?,?)"
	args := []any{cur.Chain, cur.Address}
	if chain != "" {
		where = "chain=? AND token_address>?"
		args = []any{chain, cur.Address}
	}
	args = append(args, limit)
	q := `SELECT chain,token_address,min(block_number),max(block_number),min(timestamp),max(timestamp),count(),argMax(token_symbol,timestamp),argMax(token_decimals,timestamp) FROM blockchain.token_transfers FINAL WHERE ` + where + ` AND token_address!='' GROUP BY chain,token_address ORDER BY chain,token_address LIMIT ?`
	rows, err := b.Service.CH.DB().Query(ctx, q, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []CatalogItem
	for rows.Next() {
		var item CatalogItem
		var decimals uint16
		if err := rows.Scan(&item.Chain, &item.ContractAddress, &item.FirstBlock, &item.LastBlock, &item.FirstSeen, &item.LastSeen, &item.TransferCount, &item.Symbol, &decimals); err != nil {
			return nil, err
		}
		d := int(decimals)
		item.Decimals = &d
		out = append(out, item)
	}
	return out, rows.Err()
}

func (s *Service) processCatalogItem(ctx context.Context, item CatalogItem) error {
	contract := normalizeAddress(item.ContractAddress)
	rawHash := fmt.Sprintf("ch:%s:%d:%d:%s:%d", contract, item.FirstBlock, item.LastBlock, item.Symbol, *item.Decimals)
	obs := Observation{Chain: item.Chain, ContractAddress: contract, Symbol: item.Symbol, Decimals: item.Decimals, Source: "clickhouse_history", ObservedAt: item.LastSeen, RawHash: rawHash}
	Validate(&obs)
	_, err := s.Store.Pool.Exec(ctx, `INSERT INTO token_metadata_observations(id,chain,contract_address,normalized_address,symbol,decimals,source,block_number,observed_at,raw_hash,valid,validation_error) VALUES($1,$2,$3,$3,NULLIF($4,''),$5,$6,$7,$8,$9,$10,NULLIF($11,'')) ON CONFLICT(chain,normalized_address,source,raw_hash) DO NOTHING`, uuid.NewString(), obs.Chain, obs.ContractAddress, obs.Symbol, obs.Decimals, obs.Source, int64(item.LastBlock), obs.ObservedAt, obs.RawHash, obs.Valid, obs.ValidationError)
	if err != nil {
		return err
	}
	_, err = s.Store.Pool.Exec(ctx, `INSERT INTO token_contract_catalog(chain,normalized_address,first_seen_block,last_seen_block,first_seen_at,last_seen_at,transfer_count) VALUES($1,$2,$3,$4,$5,$6,$7) ON CONFLICT(chain,normalized_address) DO UPDATE SET first_seen_block=LEAST(token_contract_catalog.first_seen_block,EXCLUDED.first_seen_block),last_seen_block=GREATEST(token_contract_catalog.last_seen_block,EXCLUDED.last_seen_block),first_seen_at=LEAST(token_contract_catalog.first_seen_at,EXCLUDED.first_seen_at),last_seen_at=GREATEST(token_contract_catalog.last_seen_at,EXCLUDED.last_seen_at),transfer_count=GREATEST(token_contract_catalog.transfer_count,EXCLUDED.transfer_count),updated_at=now()`, item.Chain, contract, item.FirstBlock, item.LastBlock, item.FirstSeen, item.LastSeen, item.TransferCount)
	if err != nil {
		return err
	}
	return s.Recompute(ctx, item.Chain, contract)
}

func (b *BackfillRunner) resolveNext(ctx context.Context) (bool, error) {
	tx, err := b.Service.Store.Pool.Begin(ctx)
	if err != nil {
		return false, err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	var chain, contract string
	var attempts int
	err = tx.QueryRow(ctx, `SELECT chain,normalized_address,attempts FROM token_metadata_resolve_queue WHERE status IN ('pending','retry_wait') AND (next_retry_at IS NULL OR next_retry_at<=now()) ORDER BY priority DESC,updated_at FOR UPDATE SKIP LOCKED LIMIT 1`).Scan(&chain, &contract, &attempts)
	if err == sql.ErrNoRows {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	if _, err = tx.Exec(ctx, `UPDATE token_metadata_resolve_queue SET status='running',attempts=attempts+1,updated_at=now() WHERE chain=$1 AND normalized_address=$2`, chain, contract); err != nil {
		return false, err
	}
	if err = tx.Commit(ctx); err != nil {
		return false, err
	}
	if b.Service.RPC == nil {
		return true, b.finishQueue(ctx, chain, contract, "failed_final", "rpc resolver unavailable", attempts+1)
	}
	RPCRequestsTotal.Inc()
	result, resolveErr := b.Service.RPC.ResolveTokenMetadata(ctx, chain, contract)
	if resolveErr != nil {
		RPCErrorsTotal.Inc()
		max := b.MaxRetries
		if max <= 0 {
			max = 3
		}
		status := "retry_wait"
		if attempts+1 >= max {
			status = "failed_final"
		}
		return true, b.finishQueue(ctx, chain, contract, status, resolveErr.Error(), attempts+1)
	}
	now := time.Now().UTC()
	obs := Observation{Chain: chain, ContractAddress: contract, Source: "rpc", ObservedAt: now, RawHash: fmt.Sprintf("rpc:%s:%v:%v:%v", contract, result.Name, result.Symbol, result.Decimals), Valid: true}
	if result.Name != nil {
		obs.Name = *result.Name
	}
	if result.Symbol != nil {
		obs.Symbol = *result.Symbol
	}
	obs.Decimals = result.Decimals
	Validate(&obs)
	_, err = b.Service.Store.Pool.Exec(ctx, `INSERT INTO token_metadata_observations(id,chain,contract_address,normalized_address,name,symbol,decimals,source,source_ref,observed_at,raw_hash,valid,validation_error) VALUES($1,$2,$3,$3,NULLIF($4,''),NULLIF($5,''),$6,'rpc',$7,$8,$9,$10,NULLIF($11,'')) ON CONFLICT(chain,normalized_address,source,raw_hash) DO NOTHING`, uuid.NewString(), chain, contract, obs.Name, obs.Symbol, obs.Decimals, strings.Join([]string{result.NameError, result.SymbolError, result.DecimalsError}, "; "), now, obs.RawHash, obs.Valid, obs.ValidationError)
	if err != nil {
		return true, err
	}
	if err = b.Service.Recompute(ctx, chain, contract); err != nil {
		return true, err
	}
	m, _ := b.Service.Get(ctx, chain, contract)
	status := "partial"
	if m != nil && m.MetadataStatus == StatusComplete {
		status = "resolved"
	}
	return true, b.finishQueue(ctx, chain, contract, status, "", attempts+1)
}

func (b *BackfillRunner) finishQueue(ctx context.Context, chain, contract, status, message string, attempts int) error {
	next := any(nil)
	if status == "retry_wait" {
		next = time.Now().Add(time.Duration(attempts*attempts) * time.Minute)
	}
	_, err := b.Service.Store.Pool.Exec(ctx, `UPDATE token_metadata_resolve_queue SET status=$3,priority=0,next_retry_at=$4,last_error=NULLIF($5,''),updated_at=now() WHERE chain=$1 AND normalized_address=$2`, chain, contract, status, next, message)
	return err
}
func (b *BackfillRunner) failJob(ctx context.Context, id string, cause error) error {
	_, _ = b.Service.Store.Pool.Exec(ctx, `UPDATE token_metadata_backfill_jobs SET status='failed',last_error=$2,updated_at=now() WHERE id=$1`, id, cause.Error())
	return cause
}
