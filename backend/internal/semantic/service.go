package semantic

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/blockchain-platform/blockchain-platform/internal/dbtx"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
)

const ProjectionVersion = "1.0"
const AlgorithmVersion = "protocol-semantic-v1.0"

type rowQuery interface {
	RowsForExport(context.Context, string, string, string, int, int) ([]map[string]any, error)
}
type Service struct {
	pool  *dbtx.Pool
	query rowQuery
}

func New(pool *dbtx.Pool, q rowQuery) *Service { return &Service{pool: pool, query: q} }

type Event struct {
	ID                string         `json:"id"`
	Chain             string         `json:"chain"`
	Subject           string         `json:"subject"`
	TxHash            string         `json:"tx_hash"`
	Type              string         `json:"type"`
	Confidence        float64        `json:"confidence"`
	Payload           map[string]any `json:"payload"`
	Provenance        map[string]any `json:"provenance"`
	ProjectionVersion string         `json:"projection_version"`
	AlgorithmVersion  string         `json:"algorithm_version"`
	DataVersion       string         `json:"data_version"`
	OccurredAt        *time.Time     `json:"occurred_at,omitempty"`
	ComputedAt        time.Time      `json:"computed_at"`
}
type Result struct {
	Chain             string         `json:"chain"`
	Subject           string         `json:"subject"`
	Events            []Event        `json:"events"`
	Counts            map[string]int `json:"counts"`
	ProjectionVersion string         `json:"projection_version"`
	AlgorithmVersion  string         `json:"algorithm_version"`
	DataVersion       string         `json:"data_version"`
	ComputedAt        *time.Time     `json:"computed_at,omitempty"`
	EvidenceBoundary  string         `json:"evidence_boundary"`
}

func (s *Service) Rebuild(ctx context.Context, chain, subject string) (Result, error) {
	txs, err := s.query.RowsForExport(ctx, "transactions", chain, subject, 50000, 0)
	if err != nil {
		return Result{}, err
	}
	transfers, err := s.query.RowsForExport(ctx, "token_transfers", chain, subject, 50000, 0)
	if err != nil {
		return Result{}, err
	}
	byTx := map[string][]map[string]any{}
	for _, r := range transfers {
		h := str(r["tx_hash"])
		if h != "" {
			byTx[h] = append(byTx[h], r)
		}
	}
	events := make([]Event, 0)
	seen := map[string]bool{}
	for _, r := range txs {
		h := str(r["tx_hash"])
		typ, confidence := classify(str(r["method_name"]), str(r["method_signature"]), str(r["input"]))
		if typ == "" {
			continue
		}
		seen[h+typ] = true
		events = append(events, event(chain, subject, h, typ, confidence, r, byTx[h]))
	}
	for h, moves := range byTx {
		if seen[h+"SWAP"] {
			continue
		}
		in, out, assets := false, false, map[string]bool{}
		for _, m := range moves {
			if strings.EqualFold(str(m["to_address"]), subject) {
				in = true
			}
			if strings.EqualFold(str(m["from_address"]), subject) {
				out = true
			}
			assets[str(m["token_address"])+str(m["token_symbol"])] = true
		}
		if in && out && len(assets) > 1 {
			events = append(events, event(chain, subject, h, "SWAP", .72, map[string]any{"method_name": "transfer-pattern"}, moves))
		}
	}
	dataVersion := latestDataVersion(txs, transfers)
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return Result{}, err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	if _, err = tx.Exec(ctx, `DELETE FROM semantic_events WHERE chain=$1 AND lower(subject)=lower($2) AND projection_version=$3`, chain, subject, ProjectionVersion); err != nil {
		return Result{}, err
	}
	for i := range events {
		events[i].DataVersion = dataVersion
		p, _ := json.Marshal(events[i].Payload)
		prov, _ := json.Marshal(events[i].Provenance)
		err = tx.QueryRow(ctx, `INSERT INTO semantic_events(id,chain,subject,tx_hash,event_type,confidence,payload,provenance,projection_version,algorithm_version,data_version,occurred_at) VALUES($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12) RETURNING computed_at`, events[i].ID, chain, subject, events[i].TxHash, events[i].Type, events[i].Confidence, p, prov, ProjectionVersion, AlgorithmVersion, dataVersion, events[i].OccurredAt).Scan(&events[i].ComputedAt)
		if err != nil {
			return Result{}, err
		}
	}
	var computedAt time.Time
	err = tx.QueryRow(ctx, `INSERT INTO semantic_projection_state(chain,subject,projection_version,algorithm_version,data_version,event_count,computed_at)
		VALUES($1,$2,$3,$4,$5,$6,now())
		ON CONFLICT(chain,subject,projection_version) DO UPDATE SET
		 algorithm_version=EXCLUDED.algorithm_version,data_version=EXCLUDED.data_version,event_count=EXCLUDED.event_count,computed_at=now()
		RETURNING computed_at`, chain, subject, ProjectionVersion, AlgorithmVersion, dataVersion, len(events)).Scan(&computedAt)
	if err != nil {
		return Result{}, err
	}
	if err = tx.Commit(ctx); err != nil {
		return Result{}, err
	}
	res := result(chain, subject, events, dataVersion)
	res.ComputedAt = &computedAt
	return res, nil
}

func (s *Service) Get(ctx context.Context, chain, subject string) (Result, error) {
	rows, err := s.pool.Query(ctx, `SELECT id::text,tx_hash,event_type,confidence,payload,provenance,projection_version,algorithm_version,data_version,occurred_at,computed_at FROM semantic_events WHERE chain=$1 AND lower(subject)=lower($2) ORDER BY occurred_at DESC NULLS LAST,computed_at DESC`, chain, subject)
	if err != nil {
		return Result{}, err
	}
	defer rows.Close()
	events := []Event{}
	dataVersion := ""
	for rows.Next() {
		var e Event
		var p, pr []byte
		e.Chain = chain
		e.Subject = subject
		if err = rows.Scan(&e.ID, &e.TxHash, &e.Type, &e.Confidence, &p, &pr, &e.ProjectionVersion, &e.AlgorithmVersion, &e.DataVersion, &e.OccurredAt, &e.ComputedAt); err != nil {
			return Result{}, err
		}
		_ = json.Unmarshal(p, &e.Payload)
		_ = json.Unmarshal(pr, &e.Provenance)
		dataVersion = e.DataVersion
		events = append(events, e)
	}
	if err = rows.Err(); err != nil {
		return Result{}, err
	}
	var computedAt time.Time
	err = s.pool.QueryRow(ctx, `SELECT data_version,computed_at FROM semantic_projection_state
		WHERE chain=$1 AND lower(subject)=lower($2) AND projection_version=$3`, chain, subject, ProjectionVersion).Scan(&dataVersion, &computedAt)
	if err != nil && !errors.Is(err, pgx.ErrNoRows) {
		return Result{}, err
	}
	res := result(chain, subject, events, dataVersion)
	if err == nil {
		res.ComputedAt = &computedAt
	}
	return res, nil
}

func classify(name, signature, input string) (string, float64) {
	v := strings.ToLower(name + " " + signature)
	switch {
	case strings.Contains(v, "swap") || strings.Contains(v, "exactinput") || strings.Contains(v, "exactoutput"):
		return "SWAP", .95
	case strings.Contains(v, "bridge") || strings.Contains(v, "sendfrom") || strings.Contains(v, "sendtoken"):
		return "BRIDGE", .9
	case strings.Contains(v, "addliquidity") || strings.Contains(v, "mintliquidity"):
		return "ADD_LIQUIDITY", .94
	case strings.Contains(v, "removeliquidity") || strings.Contains(v, "burnliquidity"):
		return "REMOVE_LIQUIDITY", .94
	case strings.Contains(v, "deposit"):
		return "DEPOSIT", .9
	case strings.Contains(v, "withdraw"):
		return "WITHDRAW", .9
	case input != "" && input != "0x" && input != "0X":
		return "CONTRACT", .6
	}
	return "", 0
}
func event(chain, subject, h, typ string, confidence float64, txrow map[string]any, moves []map[string]any) Event {
	method := str(txrow["method_name"])
	if method == "" {
		method = str(txrow["method_signature"])
	}
	payload := map[string]any{"method": method, "contract": str(txrow["to_address"]), "movements": moves}
	refs := []string{}
	if h != "" {
		refs = []string{"tx:" + chain + ":" + h}
	}
	return Event{ID: uuid.NewString(), Chain: chain, Subject: subject, TxHash: h, Type: typ, Confidence: confidence, Payload: payload, Provenance: map[string]any{"source_type": "normalized_index", "source_refs": refs, "processor": "protocol-semantic", "version": AlgorithmVersion}, ProjectionVersion: ProjectionVersion, AlgorithmVersion: AlgorithmVersion, OccurredAt: asTime(txrow["timestamp"])}
}
func result(chain, subject string, events []Event, dataVersion string) Result {
	counts := map[string]int{}
	for _, e := range events {
		counts[e.Type]++
	}
	sort.Slice(events, func(i, j int) bool {
		if events[i].OccurredAt == nil {
			return false
		}
		if events[j].OccurredAt == nil {
			return true
		}
		return events[i].OccurredAt.After(*events[j].OccurredAt)
	})
	return Result{Chain: chain, Subject: subject, Events: events, Counts: counts, ProjectionVersion: ProjectionVersion, AlgorithmVersion: AlgorithmVersion, DataVersion: dataVersion, EvidenceBoundary: "Only verified method semantics or a same-transaction, multi-asset in/out transfer pattern are classified. Ordinary transfers are not inferred as protocol actions."}
}
func latestDataVersion(groups ...[]map[string]any) string {
	latest := time.Time{}
	for _, rows := range groups {
		for _, r := range rows {
			if t := asTime(r["source_ingested_at"]); t != nil && t.After(latest) {
				latest = *t
			}
		}
	}
	if latest.IsZero() {
		return "empty"
	}
	return latest.UTC().Format(time.RFC3339Nano)
}
func str(v any) string {
	if v == nil {
		return ""
	}
	return strings.TrimSpace(fmt.Sprint(v))
}
func asTime(v any) *time.Time {
	switch x := v.(type) {
	case time.Time:
		return &x
	case *time.Time:
		return x
	case string:
		if t, e := time.Parse(time.RFC3339Nano, x); e == nil {
			return &t
		}
	}
	return nil
}
