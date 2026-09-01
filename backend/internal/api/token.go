package api

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"io"
	"math/big"
	"net/http"
	"strconv"
	"strings"

	"github.com/go-chi/chi/v5"

	"github.com/blockchain-platform/blockchain-platform/internal/tokenmeta"
	"github.com/shopspring/decimal"
)

func (s *Server) handleTokenGet(w http.ResponseWriter, r *http.Request) {
	if s.deps.TokenMeta == nil {
		writeJSON(w, 503, map[string]any{"error": "token metadata disabled"})
		return
	}
	m, err := s.deps.TokenMeta.Get(r.Context(), chi.URLParam(r, "chain"), chi.URLParam(r, "contract"))
	if err != nil {
		writeJSON(w, 500, map[string]any{"error": err.Error()})
		return
	}
	if m == nil {
		writeJSON(w, 404, map[string]any{"error": "token not found"})
		return
	}
	writeJSON(w, 200, map[string]any{"token": m})
}

func (s *Server) handleTokenLogo(w http.ResponseWriter, r *http.Request) {
	if s.deps.TokenMeta == nil || s.deps.Raw == nil {
		http.NotFound(w, r)
		return
	}
	m, err := s.deps.TokenMeta.Get(r.Context(), chi.URLParam(r, "chain"), chi.URLParam(r, "contract"))
	if err != nil || m == nil || m.LogoObjectKey == "" {
		http.NotFound(w, r)
		return
	}
	reader, size, err := s.deps.Raw.Open(r.Context(), m.LogoObjectKey)
	if err != nil {
		http.NotFound(w, r)
		return
	}
	defer reader.Close()
	w.Header().Set("Content-Type", m.LogoMimeType)
	w.Header().Set("Cache-Control", "public, max-age=86400, immutable")
	w.Header().Set("Content-Length", strconv.FormatInt(size, 10))
	_, _ = io.Copy(w, reader)
}

func (s *Server) handleTokenSearch(w http.ResponseWriter, r *http.Request) {
	if s.deps.TokenMeta == nil {
		writeJSON(w, 200, map[string]any{"tokens": []any{}})
		return
	}
	limit, _ := strconv.Atoi(r.URL.Query().Get("limit"))
	tokens, err := s.deps.TokenMeta.Search(r.Context(), r.URL.Query().Get("chain"), r.URL.Query().Get("q"), limit)
	if err != nil {
		writeJSON(w, 500, map[string]any{"error": err.Error()})
		return
	}
	writeJSON(w, 200, map[string]any{"tokens": tokens})
}

func (s *Server) handleTokenEnrichment(w http.ResponseWriter, r *http.Request) {
	if s.deps.TokenMeta == nil {
		writeJSON(w, 503, map[string]any{"error": "token metadata disabled"})
		return
	}
	summary, err := s.deps.TokenMeta.Summary(r.Context())
	if err != nil {
		writeJSON(w, 500, map[string]any{"error": err.Error()})
		return
	}
	writeJSON(w, 200, summary)
}

func (s *Server) handleTokenResolve(w http.ResponseWriter, r *http.Request) {
	if s.deps.TokenMeta == nil {
		writeJSON(w, 503, map[string]any{"error": "token metadata disabled"})
		return
	}
	if err := s.deps.TokenMeta.Enqueue(r.Context(), chi.URLParam(r, "chain"), chi.URLParam(r, "contract")); err != nil {
		writeJSON(w, 500, map[string]any{"error": err.Error()})
		return
	}
	writeJSON(w, 202, map[string]any{"status": "pending"})
}

func (s *Server) handleTokenOverride(w http.ResponseWriter, r *http.Request) {
	if s.deps.TokenMeta == nil {
		writeJSON(w, 503, map[string]any{"error": "token metadata disabled"})
		return
	}
	var req struct {
		Name      string `json:"name"`
		Symbol    string `json:"symbol"`
		Decimals  *int   `json:"decimals"`
		TokenType string `json:"token_type"`
		Reason    string `json:"reason"`
	}
	if err := decodeJSON(r, &req); err != nil {
		writeJSON(w, 400, map[string]any{"error": err.Error()})
		return
	}
	err := s.deps.TokenMeta.Override(r.Context(), chi.URLParam(r, "chain"), chi.URLParam(r, "contract"), tokenmeta.Metadata{Name: req.Name, Symbol: req.Symbol, Decimals: req.Decimals, TokenType: req.TokenType}, req.Reason)
	if err != nil {
		writeJSON(w, 400, map[string]any{"error": err.Error()})
		return
	}
	m, _ := s.deps.TokenMeta.Get(r.Context(), chi.URLParam(r, "chain"), chi.URLParam(r, "contract"))
	writeJSON(w, 200, map[string]any{"token": m})
}

func (s *Server) handleTokenBackfill(w http.ResponseWriter, r *http.Request) {
	if s.deps.TokenBackfill == nil {
		writeJSON(w, 503, map[string]any{"error": "token metadata backfill disabled"})
		return
	}
	var req struct {
		Chain string `json:"chain"`
	}
	_ = json.NewDecoder(r.Body).Decode(&req)
	id, err := s.deps.TokenBackfill.CreateJob(r.Context(), strings.TrimSpace(req.Chain))
	if err != nil {
		writeJSON(w, 500, map[string]any{"error": err.Error()})
		return
	}
	writeJSON(w, 202, map[string]any{"job_id": id, "status": "pending"})
}

func (s *Server) handleTokenBackfillList(w http.ResponseWriter, r *http.Request) {
	rows, err := s.deps.Store.Pool.Query(r.Context(), `SELECT id,COALESCE(chain,''),status,cursor_json,scanned_tokens,completed_tokens,partial_tokens,failed_tokens,raw_objects,raw_observations,raw_failed_objects,raw_failed_parts,started_at,updated_at,completed_at,COALESCE(last_error,'') FROM token_metadata_backfill_jobs ORDER BY updated_at DESC LIMIT 50`)
	if err != nil {
		writeJSON(w, 500, map[string]any{"error": err.Error()})
		return
	}
	defer rows.Close()
	out := []map[string]any{}
	for rows.Next() {
		var id, chain, status, errorText string
		var cursor json.RawMessage
		var scanned, complete, partial, failed, rawObjects, rawObservations, rawFailedObjects, rawFailedParts int64
		var started, completed sql.NullTime
		var updated any
		if err := rows.Scan(&id, &chain, &status, &cursor, &scanned, &complete, &partial, &failed, &rawObjects, &rawObservations, &rawFailedObjects, &rawFailedParts, &started, &updated, &completed, &errorText); err != nil {
			continue
		}
		out = append(out, map[string]any{"id": id, "chain": chain, "status": status, "cursor": cursor, "scanned_tokens": scanned, "completed_tokens": complete, "partial_tokens": partial, "failed_tokens": failed, "raw_objects": rawObjects, "raw_observations": rawObservations, "raw_failed_objects": rawFailedObjects, "raw_failed_parts": rawFailedParts, "started_at": nullableTime(started), "updated_at": updated, "completed_at": nullableTime(completed), "last_error": errorText})
	}
	writeJSON(w, 200, map[string]any{"jobs": out})
}

func nullableTime(value sql.NullTime) any {
	if value.Valid {
		return value.Time
	}
	return nil
}

func (s *Server) attachTokenMetadata(ctx context.Context, chain string, rows []map[string]any) {
	if s.deps.TokenMeta == nil || len(rows) == 0 {
		return
	}
	seen := map[string]bool{}
	var contracts []string
	for _, row := range rows {
		contract := strings.ToLower(strings.TrimSpace(stringFromAny(row["token_address"])))
		if contract == "" {
			contract = strings.ToLower(strings.TrimSpace(stringFromAny(row["contract_address"])))
		}
		if contract != "" && !seen[contract] {
			seen[contract] = true
			contracts = append(contracts, contract)
		}
	}
	metas, err := s.deps.TokenMeta.GetMany(ctx, chain, contracts)
	if err != nil {
		return
	}
	for _, row := range rows {
		contract := strings.ToLower(strings.TrimSpace(stringFromAny(row["token_address"])))
		if contract == "" {
			contract = strings.ToLower(strings.TrimSpace(stringFromAny(row["contract_address"])))
		}
		if contract == "" {
			continue
		}
		row["token_contract"] = contract
		row["raw_value"] = row["amount_raw"]
		m, ok := metas[contract]
		if !ok {
			row["formatted_amount"] = nil
			continue
		}
		if m.Name != "" {
			row["token_name"] = m.Name
		}
		if m.Symbol != "" {
			row["token_symbol"] = m.Symbol
		}
		row["token_decimals"] = m.Decimals
		row["token_type"] = m.TokenType
		row["token_metadata_status"] = m.MetadataStatus
		row["token_metadata_source"] = m.MetadataSource
		row["token_metadata_confidence"] = m.Confidence
		if m.Decimals == nil {
			row["formatted_amount"] = nil
			continue
		}
		raw := strings.TrimSpace(stringFromAny(row["amount_raw"]))
		integer, ok := new(big.Int).SetString(raw, 10)
		if !ok {
			row["formatted_amount"] = nil
			continue
		}
		row["formatted_amount"] = decimal.NewFromBigInt(integer, -int32(*m.Decimals)).String()
	}
}
func stringFromAny(v any) string {
	if v == nil {
		return ""
	}
	return fmt.Sprint(v)
}
