package tokenmeta

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"sort"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/blockchain-platform/blockchain-platform/internal/clickhouse"
	"github.com/blockchain-platform/blockchain-platform/internal/rawstore"
	"github.com/blockchain-platform/blockchain-platform/internal/store"
)

type Service struct {
	Store     *store.Store
	CH        *clickhouse.Conn
	RPC       RPCResolver
	Log       *slog.Logger
	Raw       *rawstore.Store
	Logo      LogoConfig
	logoQueue chan Metadata
}

func New(st *store.Store, ch *clickhouse.Conn, rpc RPCResolver, log *slog.Logger) *Service {
	return &Service{Store: st, CH: ch, RPC: rpc, Log: log}
}

// ProcessPage performs one PostgreSQL observation/catalog write per distinct
// contract, never one write per transfer.
func (s *Service) ProcessPage(ctx context.Context, chain, source string, rows []json.RawMessage) error {
	observations := ExtractPage(chain, source, rows)
	if len(observations) == 0 {
		return nil
	}
	counts := pageContractCounts(rows)
	tx, err := s.Store.Pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	batch := &pgx.Batch{}
	for _, obs := range observations {
		ObservationsTotal.WithLabelValues(obs.Source, fmt.Sprint(obs.Valid)).Inc()
		var block any
		if obs.BlockNumber != nil {
			block = int64(*obs.BlockNumber)
		}
		batch.Queue(`INSERT INTO token_metadata_observations
			(id,chain,contract_address,normalized_address,name,symbol,decimals,token_type,logo_url,source,source_ref,block_number,observed_at,raw_hash,valid,validation_error)
			VALUES($1,$2,$3,$3,NULLIF($4,''),NULLIF($5,''),$6,NULLIF($7,''),NULLIF($8,''),$9,NULLIF($10,''),$11,$12,$13,$14,NULLIF($15,''))
			ON CONFLICT(chain,normalized_address,source,raw_hash) DO NOTHING`,
			uuid.NewString(), obs.Chain, obs.ContractAddress, obs.Name, obs.Symbol, obs.Decimals, obs.TokenType, obs.LogoURL,
			obs.Source, obs.SourceRef, block, obs.ObservedAt, obs.RawHash, obs.Valid, obs.ValidationError)
		count := counts[obs.ContractAddress]
		if count == 0 {
			count = 1
		}
		batch.Queue(`INSERT INTO token_contract_catalog(chain,normalized_address,first_seen_block,last_seen_block,first_seen_at,last_seen_at,transfer_count)
			VALUES($1,$2,$3,$3,$4,$4,$5) ON CONFLICT(chain,normalized_address) DO UPDATE SET
			first_seen_block=LEAST(token_contract_catalog.first_seen_block,EXCLUDED.first_seen_block),
			last_seen_block=GREATEST(token_contract_catalog.last_seen_block,EXCLUDED.last_seen_block),
			first_seen_at=LEAST(token_contract_catalog.first_seen_at,EXCLUDED.first_seen_at),
			last_seen_at=GREATEST(token_contract_catalog.last_seen_at,EXCLUDED.last_seen_at),
			transfer_count=token_contract_catalog.transfer_count+EXCLUDED.transfer_count,updated_at=now()`,
			obs.Chain, obs.ContractAddress, block, obs.ObservedAt, count)
	}
	results := tx.SendBatch(ctx, batch)
	for range observations {
		if _, err = results.Exec(); err != nil {
			results.Close()
			return err
		}
		if _, err = results.Exec(); err != nil {
			results.Close()
			return err
		}
	}
	if err = results.Close(); err != nil {
		return err
	}
	if err = tx.Commit(ctx); err != nil {
		return err
	}
	for _, obs := range observations {
		if err := s.Recompute(ctx, obs.Chain, obs.ContractAddress); err != nil {
			s.Log.Warn("token metadata recompute failed", "contract", obs.ContractAddress, "err", err)
		}
	}
	return nil
}

// StoreRawObservations restores provider metadata from immutable raw pages.
// Catalog and canonical recomputation remain in the following ClickHouse phase,
// so historical transfer counts are not double-counted.
func (s *Service) StoreRawObservations(ctx context.Context, chain string, rows []json.RawMessage) (int, error) {
	observations := ExtractPage(chain, "raw_ankr", rows)
	if len(observations) == 0 {
		return 0, nil
	}
	batch := &pgx.Batch{}
	for _, obs := range observations {
		var block any
		if obs.BlockNumber != nil {
			block = int64(*obs.BlockNumber)
		}
		batch.Queue(`INSERT INTO token_metadata_observations
			(id,chain,contract_address,normalized_address,name,symbol,decimals,token_type,logo_url,source,source_ref,block_number,observed_at,raw_hash,valid,validation_error)
			VALUES($1,$2,$3,$3,NULLIF($4,''),NULLIF($5,''),$6,NULLIF($7,''),NULLIF($8,''),$9,'historical raw page',$10,$11,$12,$13,NULLIF($14,''))
			ON CONFLICT(chain,normalized_address,source,raw_hash) DO NOTHING`, uuid.NewString(), obs.Chain, obs.ContractAddress, obs.Name, obs.Symbol, obs.Decimals, obs.TokenType, obs.LogoURL, obs.Source, block, obs.ObservedAt, obs.RawHash, obs.Valid, obs.ValidationError)
	}
	results := s.Store.Pool.SendBatch(ctx, batch)
	for range observations {
		if _, err := results.Exec(); err != nil {
			_ = results.Close()
			return 0, err
		}
	}
	if err := results.Close(); err != nil {
		return 0, err
	}
	return len(observations), nil
}

func pageContractCounts(rows []json.RawMessage) map[string]int64 {
	out := map[string]int64{}
	for _, raw := range rows {
		var m map[string]any
		if json.Unmarshal(raw, &m) == nil {
			a := normalizeAddress(stringValue(m, "contractAddress", "tokenAddress"))
			if validEVMAddress(a) {
				out[a]++
			}
		}
	}
	return out
}

func (s *Service) observations(ctx context.Context, chain, contract string) ([]Observation, error) {
	rows, err := s.Store.Pool.Query(ctx, `SELECT chain,contract_address,COALESCE(name,''),COALESCE(symbol,''),decimals,COALESCE(token_type,''),COALESCE(logo_url,''),source,COALESCE(source_ref,''),block_number,observed_at,COALESCE(raw_hash,''),valid,COALESCE(validation_error,'') FROM token_metadata_observations WHERE chain=$1 AND normalized_address=$2`, strings.ToLower(chain), normalizeAddress(contract))
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []Observation
	for rows.Next() {
		var o Observation
		var d, b sql.NullInt64
		if err := rows.Scan(&o.Chain, &o.ContractAddress, &o.Name, &o.Symbol, &d, &o.TokenType, &o.LogoURL, &o.Source, &o.SourceRef, &b, &o.ObservedAt, &o.RawHash, &o.Valid, &o.ValidationError); err != nil {
			return nil, err
		}
		if d.Valid {
			x := int(d.Int64)
			o.Decimals = &x
		}
		if b.Valid {
			x := uint64(b.Int64)
			o.BlockNumber = &x
		}
		out = append(out, o)
	}
	return out, rows.Err()
}

func (s *Service) Recompute(ctx context.Context, chain, contract string) error {
	if !validEVMAddress(contract) {
		return fmt.Errorf("invalid token contract address: %q", contract)
	}
	obs, err := s.observations(ctx, chain, contract)
	if err != nil {
		return err
	}
	meta := Resolve(obs)
	now := time.Now().UTC()
	meta.LastResolvedAt = &now
	_, err = s.Store.Pool.Exec(ctx, `INSERT INTO token_metadata(chain,contract_address,normalized_address,name,symbol,decimals,logo_url,token_type,verified,metadata_source,confidence,metadata_status,last_resolved_at,last_verified_at)
		VALUES($1,$2,$2,NULLIF($3,''),NULLIF($4,''),$5,NULLIF($6,''),NULLIF($7,''),$8,NULLIF($9,''),$10,$11,$12::timestamptz,CASE WHEN $8 THEN $12::timestamptz ELSE NULL::timestamptz END)
		ON CONFLICT(chain,contract_address) DO UPDATE SET normalized_address=EXCLUDED.normalized_address,name=EXCLUDED.name,symbol=EXCLUDED.symbol,decimals=EXCLUDED.decimals,logo_url=EXCLUDED.logo_url,token_type=EXCLUDED.token_type,verified=EXCLUDED.verified,metadata_source=EXCLUDED.metadata_source,confidence=EXCLUDED.confidence,metadata_status=EXCLUDED.metadata_status,last_resolved_at=EXCLUDED.last_resolved_at,last_verified_at=CASE WHEN EXCLUDED.verified THEN EXCLUDED.last_verified_at ELSE token_metadata.last_verified_at END,updated_at=now()`,
		strings.ToLower(chain), normalizeAddress(contract), meta.Name, meta.Symbol, meta.Decimals, meta.LogoURL, meta.TokenType, meta.Verified, meta.MetadataSource, meta.Confidence, meta.MetadataStatus, now)
	if err == nil {
		ResolvedTotal.WithLabelValues(meta.MetadataStatus).Inc()
		_, _ = s.Store.Pool.Exec(ctx, `UPDATE token_contract_catalog SET metadata_status=$3,updated_at=now() WHERE chain=$1 AND normalized_address=$2`, strings.ToLower(chain), normalizeAddress(contract), meta.MetadataStatus)
		if meta.MetadataStatus != StatusComplete {
			_ = s.enqueue(ctx, chain, contract, 0, false)
		}
		s.queueLogo(ctx, chain, contract)
	}
	return err
}

func (s *Service) Enqueue(ctx context.Context, chain, contract string) error {
	return s.enqueue(ctx, chain, contract, 10, true)

}

func (s *Service) enqueue(ctx context.Context, chain, contract string, priority int, force bool) error {
	contract = normalizeAddress(contract)
	if !validEVMAddress(contract) {
		return fmt.Errorf("invalid token contract address: %q", contract)
	}
	if force {
		_, err := s.Store.Pool.Exec(ctx, `INSERT INTO token_metadata_resolve_queue(chain,normalized_address,status,priority) VALUES($1,$2,'pending',$3) ON CONFLICT(chain,normalized_address) DO UPDATE SET status='pending',priority=GREATEST(token_metadata_resolve_queue.priority,EXCLUDED.priority),attempts=0,next_retry_at=NULL,last_error=NULL,updated_at=now()`, strings.ToLower(chain), contract, priority)
		return err
	}
	// Automatic recomputation may create a new queue item, but must never reset
	// retry_wait, partial, resolved or failed_final rows. Only an explicit admin
	// resolve request is allowed to reactivate a terminal item.
	_, err := s.Store.Pool.Exec(ctx, `INSERT INTO token_metadata_resolve_queue(chain,normalized_address,status,priority) VALUES($1,$2,'pending',$3) ON CONFLICT(chain,normalized_address) DO UPDATE SET priority=GREATEST(token_metadata_resolve_queue.priority,EXCLUDED.priority),updated_at=now()`, strings.ToLower(chain), contract, priority)
	return err
}

func (s *Service) Get(ctx context.Context, chain, contract string) (*Metadata, error) {
	items, err := s.GetMany(ctx, chain, []string{contract})
	if err != nil {
		return nil, err
	}
	m, ok := items[normalizeAddress(contract)]
	if !ok {
		return nil, nil
	}
	return &m, nil
}

func (s *Service) GetMany(ctx context.Context, chain string, contracts []string) (map[string]Metadata, error) {
	out := map[string]Metadata{}
	if len(contracts) == 0 {
		return out, nil
	}
	norm := make([]string, 0, len(contracts))
	for _, v := range contracts {
		norm = append(norm, normalizeAddress(v))
	}
	rows, err := s.Store.Pool.Query(ctx, `SELECT chain,contract_address,COALESCE(name,''),COALESCE(symbol,''),decimals,COALESCE(token_type,''),COALESCE(logo_url,''),COALESCE(logo_object_key,''),COALESCE(logo_mime_type,''),verified,COALESCE(metadata_source,''),confidence,metadata_status,first_seen_block,last_seen_block,first_seen_at,last_seen_at,last_resolved_at,last_verified_at FROM token_metadata WHERE chain=$1 AND normalized_address=ANY($2)`, strings.ToLower(chain), norm)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	for rows.Next() {
		var m Metadata
		var d, fb, lb sql.NullInt64
		var fa, la, lr, lv sql.NullTime
		if err := rows.Scan(&m.Chain, &m.ContractAddress, &m.Name, &m.Symbol, &d, &m.TokenType, &m.LogoURL, &m.LogoObjectKey, &m.LogoMimeType, &m.Verified, &m.MetadataSource, &m.Confidence, &m.MetadataStatus, &fb, &lb, &fa, &la, &lr, &lv); err != nil {
			return nil, err
		}
		if d.Valid {
			x := int(d.Int64)
			m.Decimals = &x
		}
		if fb.Valid {
			x := fb.Int64
			m.FirstSeenBlock = &x
		}
		if lb.Valid {
			x := lb.Int64
			m.LastSeenBlock = &x
		}
		if fa.Valid {
			m.FirstSeenAt = &fa.Time
		}
		if la.Valid {
			m.LastSeenAt = &la.Time
		}
		if lr.Valid {
			m.LastResolvedAt = &lr.Time
		}
		if lv.Valid {
			m.LastVerifiedAt = &lv.Time
		}
		out[normalizeAddress(m.ContractAddress)] = m
	}
	return out, rows.Err()
}

func (s *Service) Search(ctx context.Context, chain, q string, limit int) ([]Metadata, error) {
	if limit <= 0 || limit > 100 {
		limit = 25
	}
	chain = strings.ToLower(strings.TrimSpace(chain))
	q = strings.TrimSpace(q)
	pattern := "%" + q + "%"
	rows, err := s.Store.Pool.Query(ctx, `SELECT chain,normalized_address FROM token_metadata WHERE ($1='' OR chain=$1) AND (normalized_address ILIKE $2 OR COALESCE(symbol,'') ILIKE $2 OR COALESCE(name,'') ILIKE $2) ORDER BY CASE WHEN normalized_address=$3 THEN 0 WHEN lower(COALESCE(symbol,''))=lower($3) THEN 1 WHEN COALESCE(symbol,'') ILIKE $3||'%' THEN 2 WHEN lower(COALESCE(name,''))=lower($3) THEN 3 WHEN COALESCE(name,'') ILIKE $3||'%' THEN 4 ELSE 5 END,chain,normalized_address LIMIT $4`, chain, pattern, strings.ToLower(q), limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	type key struct{ c, a string }
	var keys []key
	for rows.Next() {
		var k key
		if err := rows.Scan(&k.c, &k.a); err != nil {
			return nil, err
		}
		keys = append(keys, k)
	}
	var out []Metadata
	for _, k := range keys {
		m, e := s.Get(ctx, k.c, k.a)
		if e != nil {
			return nil, e
		}
		if m != nil {
			out = append(out, *m)
		}
	}
	return out, nil
}

func (s *Service) Summary(ctx context.Context) (Summary, error) {
	var v Summary
	err := s.Store.Pool.QueryRow(ctx, `SELECT count(*),count(*) FILTER(WHERE metadata_status='complete'),count(*) FILTER(WHERE metadata_status='partial'),count(*) FILTER(WHERE metadata_status='unknown'),count(*) FILTER(WHERE metadata_status='conflict'),count(*) FILTER(WHERE metadata_status='failed'),count(*) FILTER(WHERE logo_object_key IS NOT NULL AND logo_object_key<>''),(SELECT count(*) FROM token_metadata_resolve_queue WHERE status IN ('pending','running','retry_wait')) FROM token_metadata`).Scan(&v.Contracts, &v.Complete, &v.Partial, &v.Unknown, &v.Conflict, &v.Failed, &v.WithLogo, &v.QueueDepth)
	QueueDepth.Set(float64(v.QueueDepth))
	return v, err
}

func (s *Service) Override(ctx context.Context, chain, contract string, input Metadata, reason string) error {
	obs := Observation{Chain: chain, ContractAddress: contract, Name: input.Name, Symbol: input.Symbol, Decimals: input.Decimals, TokenType: input.TokenType, Source: "admin_override", SourceRef: reason, ObservedAt: time.Now().UTC(), RawHash: uuid.NewString()}
	Validate(&obs)
	if !obs.Valid {
		return errors.New(obs.ValidationError)
	}
	b, _ := json.Marshal([]Observation{obs})
	_ = b
	_, err := s.Store.Pool.Exec(ctx, `INSERT INTO token_metadata_observations(id,chain,contract_address,normalized_address,name,symbol,decimals,token_type,source,source_ref,observed_at,raw_hash,valid) VALUES($1,$2,$3,$3,NULLIF($4,''),NULLIF($5,''),$6,NULLIF($7,''),'admin_override',$8,$9,$10,true)`, uuid.NewString(), obs.Chain, obs.ContractAddress, obs.Name, obs.Symbol, obs.Decimals, obs.TokenType, reason, obs.ObservedAt, obs.RawHash)
	if err != nil {
		return err
	}
	return s.Recompute(ctx, chain, contract)
}

func uniqueStrings(in []string) []string {
	sort.Strings(in)
	out := in[:0]
	for _, v := range in {
		if len(out) == 0 || out[len(out)-1] != v {
			out = append(out, v)
		}
	}
	return out
}
