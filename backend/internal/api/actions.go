package api

import (
	"compress/gzip"
	"context"
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/blockchain-platform/blockchain-platform/internal/dbtx"
	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/shopspring/decimal"

	"github.com/blockchain-platform/blockchain-platform/internal/addressnorm"
	"github.com/blockchain-platform/blockchain-platform/internal/chain"
	"github.com/blockchain-platform/blockchain-platform/internal/chainread"
	"github.com/blockchain-platform/blockchain-platform/internal/chaintrace"
	"github.com/blockchain-platform/blockchain-platform/internal/clickhouse"
	"github.com/blockchain-platform/blockchain-platform/internal/dataset"
	"github.com/blockchain-platform/blockchain-platform/internal/method"
	"github.com/blockchain-platform/blockchain-platform/internal/model"
	"github.com/blockchain-platform/blockchain-platform/internal/provider"
	rpc "github.com/blockchain-platform/blockchain-platform/internal/provider/rpc"
	"github.com/blockchain-platform/blockchain-platform/internal/query"
)

type actionEnvelope struct {
	Success bool           `json:"success"`
	Data    any            `json:"data"`
	Meta    map[string]any `json:"meta"`
	Error   *actionError   `json:"error"`
}
type actionError struct {
	Code    string `json:"code"`
	Message string `json:"message"`
}

func actionOK(w http.ResponseWriter, data any, meta map[string]any) {
	if meta == nil {
		meta = map[string]any{}
	}
	if _, ok := meta["coverage"]; !ok {
		meta["coverage"] = map[string]any{"complete": false, "start_block": nil, "end_block": nil, "start_time": nil, "end_time": nil, "missing_ranges": []any{}, "reason": "No complete coverage assertion is available for this response"}
	}
	if _, ok := meta["freshness"]; !ok {
		meta["freshness"] = map[string]any{"as_of_block": nil, "as_of_time": time.Now().UTC()}
	}
	if _, ok := meta["query_generated_at"]; !ok {
		meta["query_generated_at"] = time.Now().UTC()
	}
	if _, ok := meta["dataset_version"]; !ok {
		meta["dataset_version"] = "clickhouse-final-v1"
	}
	if _, ok := meta["normalization_version"]; !ok {
		meta["normalization_version"] = "actions-v1"
	}
	if _, ok := meta["source"]; !ok {
		meta["source"] = "local_clickhouse"
	}
	if _, ok := meta["cached"]; !ok {
		meta["cached"] = true
	}
	writeJSON(w, http.StatusOK, actionEnvelope{Success: true, Data: publicActionChains(data), Meta: meta})
}
func publicActionChains(value any) any {
	switch v := value.(type) {
	case map[string]any:
		if tokenAddress, ok := v["token_address"]; ok {
			if _, exists := v["token_contract"]; !exists {
				v["token_contract"] = tokenAddress
			}
		}
		if decimals, ok := v["token_decimals"]; ok {
			if _, exists := v["decimals"]; !exists {
				v["decimals"] = decimals
			}
		}
		if raw, ok := v["value_raw"]; ok {
			if _, exists := v["raw_value"]; !exists {
				v["raw_value"] = raw
			}
		}
		for key, item := range v {
			if key == "chain" && fmt.Sprint(item) == "eth" {
				v[key] = "ethereum"
			} else {
				v[key] = publicActionChains(item)
			}
		}
	case []map[string]any:
		for i := range v {
			v[i] = publicActionChains(v[i]).(map[string]any)
		}
	case []any:
		for i := range v {
			v[i] = publicActionChains(v[i])
		}
	}
	return value
}
func actionFail(w http.ResponseWriter, status int, code, message string) {
	writeJSON(w, status, actionEnvelope{Success: false, Data: nil, Meta: map[string]any{}, Error: &actionError{Code: code, Message: message}})
}

type actionCoverageState struct {
	full               bool
	fromBlock, toBlock int64
	committedAt        time.Time
}

// actionCoverageMeta turns the downloader's durable completion records into
// an endpoint-scoped evidence assertion. A completed full-range job is the
// proof; ClickHouse row presence alone is deliberately not treated as proof of
// completeness (an address or range may legitimately contain zero rows).
func (s *Server) actionCoverageMeta(ctx context.Context, chain string, addresses, datasets []string) map[string]any {
	coverage := map[string]any{
		"complete": false, "scope": "full_history", "datasets": datasets,
		"start_block": nil, "end_block": nil, "start_time": nil, "end_time": nil,
		"missing_ranges": []any{},
	}
	if s.deps.Store == nil || s.deps.Store.Pool == nil || len(addresses) == 0 || len(datasets) == 0 {
		coverage["reason"] = "Coverage store is unavailable"
		return map[string]any{"coverage": coverage}
	}

	states := make(map[string]*actionCoverageState, len(addresses)*len(datasets))
	key := func(address, dataset string) string { return address + "\x00" + dataset }
	for _, address := range addresses {
		for _, dataset := range datasets {
			states[key(address, dataset)] = &actionCoverageState{}
		}
	}
	fullRows, err := s.deps.Store.Pool.Query(ctx, `SELECT DISTINCT ds.subject,dd.dataset
		FROM download_jobs j
		JOIN download_subjects ds ON ds.job_id=j.id
		JOIN download_datasets dd ON dd.subject_id=ds.id
		WHERE j.chain=$1 AND j.status='COMPLETED' AND j.range_type='full'
		  AND ds.subject=ANY($2) AND dd.dataset=ANY($3) AND dd.status='COMPLETED'`, chain, addresses, datasets)
	if err != nil {
		coverage["reason"] = "Unable to read full-range completion assertions"
		return map[string]any{"coverage": coverage}
	}
	for fullRows.Next() {
		var address, dataset string
		if scanErr := fullRows.Scan(&address, &dataset); scanErr != nil {
			fullRows.Close()
			coverage["reason"] = "Unable to read full-range completion assertions"
			return map[string]any{"coverage": coverage}
		}
		if state := states[key(address, dataset)]; state != nil {
			state.full = true
		}
	}
	if err := fullRows.Err(); err != nil {
		fullRows.Close()
		coverage["reason"] = "Unable to read full-range completion assertions"
		return map[string]any{"coverage": coverage}
	}
	fullRows.Close()

	coveredRows, err := s.deps.Store.Pool.Query(ctx, `SELECT subject,dataset,min(from_block),max(to_block),max(committed_at)
		FROM dataset_coverage
		WHERE chain=$1 AND status='COMPLETE' AND subject=ANY($2) AND dataset=ANY($3)
		GROUP BY subject,dataset`, chain, addresses, datasets)
	if err == nil {
		for coveredRows.Next() {
			var address, dataset string
			var fromBlock, toBlock int64
			var committedAt time.Time
			if coveredRows.Scan(&address, &dataset, &fromBlock, &toBlock, &committedAt) == nil {
				if state := states[key(address, dataset)]; state != nil {
					state.fromBlock, state.toBlock, state.committedAt = fromBlock, toBlock, committedAt
				}
			}
		}
		coveredRows.Close()
	}

	complete := true
	missing := []any{}
	var minBlock, maxBlock int64
	var latest time.Time
	haveBounds := false
	for _, address := range addresses {
		for _, dataset := range datasets {
			state := states[key(address, dataset)]
			if state == nil || !state.full {
				complete = false
				missing = append(missing, map[string]any{"address": address, "dataset": dataset, "reason": "No completed full-range download assertion"})
				continue
			}
			if !state.committedAt.IsZero() {
				if !haveBounds || state.fromBlock < minBlock {
					minBlock = state.fromBlock
				}
				if !haveBounds || state.toBlock > maxBlock {
					maxBlock = state.toBlock
				}
				haveBounds = true
				if state.committedAt.After(latest) {
					latest = state.committedAt
				}
			}
		}
	}
	coverage["complete"] = complete
	coverage["missing_ranges"] = missing
	if haveBounds {
		coverage["start_block"], coverage["end_block"] = minBlock, maxBlock
	}
	if complete {
		coverage["reason"] = "Completed full-range downloads are recorded for every address and dataset in this response"
	} else {
		coverage["reason"] = "One or more address/dataset pairs lack a completed full-range download assertion"
	}
	meta := map[string]any{"coverage": coverage}
	if !latest.IsZero() {
		meta["freshness"] = map[string]any{"as_of_block": func() any {
			if haveBounds {
				return maxBlock
			}
			return nil
		}(), "as_of_time": latest}
	}
	return meta
}

type actionAuth struct {
	hashes  [][32]byte
	limit   int
	pool    *dbtx.Pool
	mu      sync.Mutex
	windows map[[32]byte]struct {
		minute int64
		count  int
	}
}

func newActionAuth(pool *dbtx.Pool) *actionAuth {
	keys := strings.Split(os.Getenv("GPT_ACTIONS_API_KEYS"), ",")
	if len(keys) == 1 && strings.TrimSpace(keys[0]) == "" {
		keys = []string{os.Getenv("GPT_ACTIONS_API_KEY")}
	}
	a := &actionAuth{pool: pool, limit: envPositiveInt("GPT_ACTIONS_RATE_LIMIT_PER_MINUTE", 300), windows: map[[32]byte]struct {
		minute int64
		count  int
	}{}}
	for _, key := range keys {
		if key = strings.TrimSpace(key); key != "" {
			a.hashes = append(a.hashes, sha256.Sum256([]byte(key)))
		}
	}
	return a
}
func envPositiveInt(name string, fallback int) int {
	n, _ := strconv.Atoi(strings.TrimSpace(os.Getenv(name)))
	if n <= 0 {
		return fallback
	}
	return n
}
func (a *actionAuth) middleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if len(a.hashes) == 0 && a.pool == nil {
			actionFail(w, http.StatusServiceUnavailable, "AUTH_NOT_CONFIGURED", "GPT Actions API authentication is not configured")
			return
		}
		header := strings.TrimSpace(r.Header.Get("Authorization"))
		if !strings.HasPrefix(header, "Bearer ") {
			actionFail(w, http.StatusUnauthorized, "UNAUTHORIZED", "Bearer API key required")
			return
		}
		sum := sha256.Sum256([]byte(strings.TrimSpace(strings.TrimPrefix(header, "Bearer "))))
		ok, keyID, limit := false, "", a.limit
		for _, allowed := range a.hashes {
			if subtle.ConstantTimeCompare(sum[:], allowed[:]) == 1 {
				ok = true
			}
		}
		if !ok && a.pool != nil {
			var scopes, allowedIPs []string
			err := a.pool.QueryRow(r.Context(), `SELECT id::text, scopes, rate_limit_per_minute, allowed_ips FROM gpt_action_api_keys WHERE key_hash=$1 AND disabled_at IS NULL AND (expires_at IS NULL OR expires_at>now())`, sum[:]).Scan(&keyID, &scopes, &limit, &allowedIPs)
			if err == nil && actionScopeAllowed(scopes, actionScopeForPath(r.URL.Path)) && actionIPAllowed(allowedIPs, requestIP(r)) {
				ok = true
			}
		}
		if !ok {
			actionFail(w, http.StatusUnauthorized, "UNAUTHORIZED", "Invalid API key")
			return
		}
		minute := time.Now().Unix() / 60
		a.mu.Lock()
		state := a.windows[sum]
		if state.minute != minute {
			state.minute = minute
			state.count = 0
		}
		state.count++
		a.windows[sum] = state
		a.mu.Unlock()
		if state.count > limit {
			actionFail(w, http.StatusTooManyRequests, "RATE_LIMITED", "API key rate limit exceeded")
			return
		}
		started := time.Now()
		recorder := &actionStatusWriter{ResponseWriter: w, status: http.StatusOK}
		next.ServeHTTP(recorder, r)
		if a.pool != nil {
			requestID := r.Header.Get("X-Request-Id")
			if requestID == "" {
				requestID = uuid.NewString()
			}
			var id any
			if keyID != "" {
				id = keyID
			}
			go func() {
				ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
				defer cancel()
				_, _ = a.pool.Exec(ctx, `INSERT INTO gpt_action_request_logs(api_key_id,request_id,method,path,status,duration_ms,remote_ip) VALUES($1,$2,$3,$4,$5,$6,$7)`, id, requestID, r.Method, r.URL.Path, recorder.status, time.Since(started).Milliseconds(), requestIP(r))
				if keyID != "" {
					_, _ = a.pool.Exec(ctx, `UPDATE gpt_action_api_keys SET last_used_at=now() WHERE id=$1`, keyID)
				}
			}()
		}
	})
}

type actionStatusWriter struct {
	http.ResponseWriter
	status int
}

func (w *actionStatusWriter) WriteHeader(code int) {
	w.status = code
	w.ResponseWriter.WriteHeader(code)
}
func actionScopeForPath(path string) string {
	if strings.Contains(path, "/analysis/") {
		return "analysis"
	}
	if strings.Contains(path, "/export/") {
		return "export"
	}
	return "read"
}
func actionScopeAllowed(scopes []string, required string) bool {
	for _, s := range scopes {
		if s == "*" || s == required {
			return true
		}
	}
	return false
}
func requestIP(r *http.Request) string {
	value := strings.TrimSpace(strings.Split(r.Header.Get("X-Forwarded-For"), ",")[0])
	if value != "" {
		return value
	}
	host, _, err := net.SplitHostPort(r.RemoteAddr)
	if err == nil {
		return host
	}
	return r.RemoteAddr
}
func actionIPAllowed(rules []string, ipValue string) bool {
	if len(rules) == 0 {
		return true
	}
	ip := net.ParseIP(ipValue)
	if ip == nil {
		return false
	}
	for _, rule := range rules {
		if candidate := net.ParseIP(rule); candidate != nil && candidate.Equal(ip) {
			return true
		}
		if _, cidr, err := net.ParseCIDR(rule); err == nil && cidr.Contains(ip) {
			return true
		}
	}
	return false
}

type gzipResponseWriter struct {
	http.ResponseWriter
	writer io.Writer
}

func (w gzipResponseWriter) Write(p []byte) (int, error) { return w.writer.Write(p) }
func actionGzip(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !strings.Contains(r.Header.Get("Accept-Encoding"), "gzip") {
			next.ServeHTTP(w, r)
			return
		}
		gz := gzip.NewWriter(w)
		defer gz.Close()
		w.Header().Set("Content-Encoding", "gzip")
		w.Header().Add("Vary", "Accept-Encoding")
		next.ServeHTTP(gzipResponseWriter{w, gz}, r)
	})
}

func (s *Server) registerActionRoutes(api chi.Router) {
	authn := newActionAuth(s.deps.Store.Pool)
	api.Route("/admin/action-api-keys", func(admin chi.Router) {
		admin.Use(actionAdminAuth)
		admin.Get("/", s.handleListActionKeys)
		admin.Post("/", s.handleCreateActionKey)
		admin.Post("/{id}/disable", s.handleDisableActionKey)
		admin.Delete("/{id}", s.handleDeleteActionKey)
		admin.Get("/requests", s.handleActionRequestLogs)
	})
	api.Group(func(actions chi.Router) {
		actions.Use(authn.middleware)
		actions.Use(actionGzip)
		actions.Get("/address/overview", s.handleActionOverview)
		actions.Get("/address/assets", s.handleActionAssets)
		actions.Get("/address/transactions", func(w http.ResponseWriter, r *http.Request) { s.handleActionRows(w, r, "transactions") })
		actions.Get("/address/token-transfers", func(w http.ResponseWriter, r *http.Request) { s.handleActionRows(w, r, "token_transfers") })
		actions.Get("/address/internal-transactions", func(w http.ResponseWriter, r *http.Request) { s.handleActionRows(w, r, "internal_transactions") })
		actions.Get("/address/counterparties", s.handleActionCounterparties)
		actions.Get("/address/flow-summary", s.handleActionFlowSummary)
		actions.Get("/address/top-flows", s.handleActionTopFlows)
		actions.Get("/address/first-funding", s.handleActionFirstFunding)
		actions.Get("/transaction/detail", s.handleActionTransactionDetail)
		actions.Get("/transaction/logs", s.handleActionTransactionLogs)
		actions.Get("/transaction/decode", s.handleActionTransactionDecode)
		actions.Get("/transaction/trace", s.handleActionTransactionTrace)
		actions.Post("/addresses/batch-overview", s.handleActionBatchOverview)
		actions.Post("/addresses/batch-assets", s.handleActionBatchAssets)
		actions.Post("/analysis/path", s.handleActionPath)
		actions.Post("/analysis/address-network", s.handleActionNetwork)
		actions.Post("/export/create", s.handleActionCreateExport)
		actions.Get("/export/status", s.handleActionExportStatus)
		actions.Post("/data/ensure", s.handleActionEnsureData)
		actions.Get("/data/download-status", s.handleActionDownloadStatus)
	})
}

func (s *Server) handleActionEnsureData(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Chain           string            `json:"chain"`
		Addresses       []string          `json:"addresses"`
		Datasets        []string          `json:"datasets"`
		Range           dataset.RangeSpec `json:"range"`
		PerformanceMode string            `json:"performance_mode"`
	}
	if e := decodeActionJSON(r, &req); e != nil {
		actionFail(w, 400, "INVALID_QUERY", e.Error())
		return
	}
	if len(req.Addresses) == 0 || len(req.Addresses) > 200 {
		actionFail(w, 400, "INVALID_QUERY", "addresses must contain 1 to 200 items")
		return
	}
	if req.Range.Type == "" {
		req.Range.Type = "full"
	}
	if req.PerformanceMode == "" {
		req.PerformanceMode = "fast"
	}
	// Actions 的下载任务同样必须带租户作用域：留空会退化成 DefaultOrgWorkspace，
	// 让 API Key 表面写进一个它并未显式绑定的组织。
	scope, scopeErr := s.systemScope(r.Context())
	if scopeErr != nil {
		actionFail(w, 500, "INTERNAL_ERROR", "Unable to resolve Actions workspace")
		return
	}
	resp, e := s.svc.Ensure(r.Context(), dataset.EnsureRequest{Source: "chatgpt_mcp", Priority: 90, PerformanceMode: req.PerformanceMode, Subjects: req.Addresses, Chain: req.Chain, Datasets: req.Datasets, Range: req.Range, ReusePolicy: "prefer_local", CaseName: "ChatGPT MCP", Tags: []string{"ai-managed", "chatgpt-mcp"}, CreateMissingMeta: true, OrganizationID: scope.OrganizationID, WorkspaceID: scope.WorkspaceID})
	if e != nil {
		actionFail(w, 400, "DOWNLOAD_PLAN_FAILED", e.Error())
		return
	}
	actionOK(w, map[string]any{"status": resp.Status, "dataset_ref": resp.DatasetRef, "download_job_id": resp.JobID, "coverage_before": resp.Coverage, "source": "chatgpt_mcp", "ai_managed": true}, map[string]any{"download_engine": "system_dataset_service", "reuse_policy": "prefer_local"})
}

func (s *Server) handleActionDownloadStatus(w http.ResponseWriter, r *http.Request) {
	id := strings.TrimSpace(r.URL.Query().Get("download_job_id"))
	if _, e := uuid.Parse(id); e != nil {
		actionFail(w, 400, "JOB_NOT_FOUND", "Invalid download_job_id")
		return
	}
	job, e := s.deps.Store.GetJob(r.Context(), id)
	if errors.Is(e, pgx.ErrNoRows) || job == nil || !strings.EqualFold(job.Source, "chatgpt_mcp") {
		actionFail(w, 404, "JOB_NOT_FOUND", "AI download job not found")
		return
	}
	if e != nil {
		actionFail(w, 500, "INTERNAL_ERROR", "Unable to query AI download job")
		return
	}
	actionOK(w, map[string]any{"download_job_id": job.ID, "job_no": job.JobNo, "status": strings.ToLower(job.Status), "source": job.Source, "ai_managed": true, "chain": publicActionChain(job.Chain), "datasets": job.Datasets, "range_type": job.RangeType, "from_block": job.FromBlock, "to_block": job.ToBlock, "from_time": job.FromTimestamp, "to_time": job.ToTimestamp, "stats": job.Stats, "error": job.Error}, nil)
}

func actionAdminAuth(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		configured := strings.TrimSpace(os.Getenv("GPT_ACTIONS_ADMIN_KEY"))
		header := strings.TrimSpace(r.Header.Get("Authorization"))
		if configured == "" {
			actionFail(w, http.StatusServiceUnavailable, "AUTH_NOT_CONFIGURED", "GPT Actions key administration is not configured")
			return
		}
		if !strings.HasPrefix(header, "Bearer ") {
			actionFail(w, http.StatusUnauthorized, "UNAUTHORIZED", "Administrator bearer key required")
			return
		}
		got := sha256.Sum256([]byte(strings.TrimSpace(strings.TrimPrefix(header, "Bearer "))))
		want := sha256.Sum256([]byte(configured))
		if subtle.ConstantTimeCompare(got[:], want[:]) != 1 {
			actionFail(w, http.StatusUnauthorized, "UNAUTHORIZED", "Invalid administrator key")
			return
		}
		next.ServeHTTP(w, r)
	})
}

func (s *Server) handleCreateActionKey(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Name       string     `json:"name"`
		Scopes     []string   `json:"scopes"`
		RateLimit  int        `json:"rate_limit_per_minute"`
		AllowedIPs []string   `json:"allowed_ips"`
		ExpiresAt  *time.Time `json:"expires_at"`
	}
	if err := decodeActionJSON(r, &req); err != nil {
		actionFail(w, 400, "INVALID_QUERY", err.Error())
		return
	}
	req.Name = strings.TrimSpace(req.Name)
	if req.Name == "" {
		actionFail(w, 400, "INVALID_QUERY", "name required")
		return
	}
	if len(req.Scopes) == 0 {
		req.Scopes = []string{"read", "analysis", "export"}
	}
	for _, scope := range req.Scopes {
		if scope != "read" && scope != "analysis" && scope != "export" && scope != "*" {
			actionFail(w, 400, "INVALID_QUERY", "invalid scope "+scope)
			return
		}
	}
	for _, rule := range req.AllowedIPs {
		if net.ParseIP(rule) == nil {
			if _, _, err := net.ParseCIDR(rule); err != nil {
				actionFail(w, 400, "INVALID_QUERY", "invalid allowed_ip "+rule)
				return
			}
		}
	}
	if req.RateLimit <= 0 {
		req.RateLimit = 300
	}
	// S7 修复（DEF-01）：省略 allowed_ips 时切片为 nil，pgx 会把 nil 切片编码为 NULL，
	// 而 gpt_action_api_keys.allowed_ips 是 NOT NULL，导致"字段可省略"的契约实际返回 500。
	// 这里显式归一为空数组：省略 = 不限制来源 IP。
	if req.AllowedIPs == nil {
		req.AllowedIPs = []string{}
	}
	secretBytes := make([]byte, 32)
	if _, err := rand.Read(secretBytes); err != nil {
		actionFail(w, 500, "INTERNAL_ERROR", "Unable to generate key")
		return
	}
	secret := "bca_" + base64.RawURLEncoding.EncodeToString(secretBytes)
	sum := sha256.Sum256([]byte(secret))
	prefix := secret
	if len(prefix) > 12 {
		prefix = prefix[:12]
	}
	var id string
	err := s.deps.Store.Pool.QueryRow(r.Context(), `INSERT INTO gpt_action_api_keys(name,key_prefix,key_hash,scopes,rate_limit_per_minute,allowed_ips,expires_at) VALUES($1,$2,$3,$4,$5,$6,$7) RETURNING id::text`, req.Name, prefix, sum[:], req.Scopes, req.RateLimit, req.AllowedIPs, req.ExpiresAt).Scan(&id)
	if err != nil {
		actionFail(w, 500, "INTERNAL_ERROR", "Unable to create API key")
		return
	}
	writeJSON(w, http.StatusCreated, actionEnvelope{Success: true, Data: map[string]any{"id": id, "name": req.Name, "api_key": secret, "key_prefix": prefix, "scopes": req.Scopes, "rate_limit_per_minute": req.RateLimit, "allowed_ips": req.AllowedIPs, "expires_at": req.ExpiresAt}, Meta: map[string]any{"api_key_shown_once": true}})
}
func (s *Server) handleListActionKeys(w http.ResponseWriter, r *http.Request) {
	rows, err := s.deps.Store.Pool.Query(r.Context(), `SELECT id::text,name,key_prefix,scopes,rate_limit_per_minute,allowed_ips,expires_at,disabled_at,last_used_at,created_at,created_by FROM gpt_action_api_keys ORDER BY created_at DESC`)
	if err != nil {
		actionFail(w, 500, "INTERNAL_ERROR", "Unable to list API keys")
		return
	}
	defer rows.Close()
	out := []map[string]any{}
	for rows.Next() {
		var id, name, prefix, createdBy string
		var scopes, ips []string
		var rate int
		var expires, disabled, lastUsed *time.Time
		var created time.Time
		if err := rows.Scan(&id, &name, &prefix, &scopes, &rate, &ips, &expires, &disabled, &lastUsed, &created, &createdBy); err != nil {
			actionFail(w, 500, "INTERNAL_ERROR", "Unable to read API keys")
			return
		}
		out = append(out, map[string]any{"id": id, "name": name, "key_prefix": prefix, "scopes": scopes, "rate_limit_per_minute": rate, "allowed_ips": ips, "expires_at": expires, "disabled_at": disabled, "last_used_at": lastUsed, "created_at": created, "created_by": createdBy})
	}
	actionOK(w, out, map[string]any{"count": len(out)})
}
func (s *Server) handleDisableActionKey(w http.ResponseWriter, r *http.Request) {
	tag, err := s.deps.Store.Pool.Exec(r.Context(), `UPDATE gpt_action_api_keys SET disabled_at=COALESCE(disabled_at,now()) WHERE id=$1`, chi.URLParam(r, "id"))
	if err != nil {
		actionFail(w, 500, "INTERNAL_ERROR", "Unable to disable API key")
		return
	}
	if tag.RowsAffected() == 0 {
		actionFail(w, 404, "JOB_NOT_FOUND", "API key not found")
		return
	}
	actionOK(w, map[string]any{"id": chi.URLParam(r, "id"), "disabled": true}, nil)
}
func (s *Server) handleDeleteActionKey(w http.ResponseWriter, r *http.Request) {
	tag, err := s.deps.Store.Pool.Exec(r.Context(), `DELETE FROM gpt_action_api_keys WHERE id=$1`, chi.URLParam(r, "id"))
	if err != nil {
		actionFail(w, 500, "INTERNAL_ERROR", "Unable to delete API key")
		return
	}
	if tag.RowsAffected() == 0 {
		actionFail(w, 404, "JOB_NOT_FOUND", "API key not found")
		return
	}
	actionOK(w, map[string]any{"id": chi.URLParam(r, "id"), "deleted": true}, nil)
}
func (s *Server) handleActionRequestLogs(w http.ResponseWriter, r *http.Request) {
	limit, _ := strconv.Atoi(r.URL.Query().Get("limit"))
	if limit <= 0 || limit > 1000 {
		limit = 100
	}
	rows, err := s.deps.Store.Pool.Query(r.Context(), `SELECT id,api_key_id::text,request_id,method,path,status,duration_ms,remote_ip,created_at FROM gpt_action_request_logs ORDER BY created_at DESC LIMIT $1`, limit)
	if err != nil {
		actionFail(w, 500, "INTERNAL_ERROR", "Unable to list request logs")
		return
	}
	defer rows.Close()
	out := []map[string]any{}
	for rows.Next() {
		var id, duration int64
		var keyID *string
		var requestID, methodName, path, ip string
		var status int
		var created time.Time
		if err := rows.Scan(&id, &keyID, &requestID, &methodName, &path, &status, &duration, &ip, &created); err != nil {
			actionFail(w, 500, "INTERNAL_ERROR", "Unable to read request logs")
			return
		}
		out = append(out, map[string]any{"id": id, "api_key_id": keyID, "request_id": requestID, "method": methodName, "path": path, "status": status, "duration_ms": duration, "remote_ip": ip, "created_at": created})
	}
	actionOK(w, out, map[string]any{"count": len(out)})
}

var actionChains = map[string]bool{"bsc": true, "ethereum": true, "eth": true, "tron": true, "polygon": true, "arbitrum": true, "optimism": true, "base": true}

func canonicalActionChain(v string) (string, error) {
	v = strings.ToLower(strings.TrimSpace(v))
	if v == "ethereum" {
		v = "eth"
	}
	if !actionChains[v] {
		return "", fmt.Errorf("unsupported chain")
	}
	return v, nil
}
func actionIdentity(r *http.Request) (string, string, error) {
	c, e := canonicalActionChain(r.URL.Query().Get("chain"))
	if e != nil {
		return "", "", e
	}
	// 按链归一化：Solana 保留 base58 大小写，EVM 折叠小写，TRON 归一到 Base58Check。
	normalized, e := addressnorm.Normalize(c, r.URL.Query().Get("address"))
	if e != nil {
		return "", "", e
	}
	return c, normalized, nil
}
func parseActionTime(v string) (*time.Time, error) {
	if strings.TrimSpace(v) == "" {
		return nil, nil
	}
	t, e := time.Parse(time.RFC3339, v)
	if e != nil {
		return nil, e
	}
	t = t.UTC()
	return &t, nil
}
func parseActionFilter(r *http.Request) (query.ActionFilter, error) {
	chain, address, e := actionIdentity(r)
	if e != nil {
		return query.ActionFilter{}, e
	}
	q := r.URL.Query()
	start, e := parseActionTime(q.Get("start_time"))
	if e != nil {
		return query.ActionFilter{}, fmt.Errorf("invalid start_time")
	}
	end, e := parseActionTime(q.Get("end_time"))
	if e != nil {
		return query.ActionFilter{}, fmt.Errorf("invalid end_time")
	}
	page, _ := strconv.Atoi(q.Get("page"))
	size, _ := strconv.Atoi(q.Get("page_size"))
	if size > 500 {
		return query.ActionFilter{}, fmt.Errorf("page_size cannot exceed 500")
	}
	direction := strings.ToLower(q.Get("direction"))
	if direction != "" && direction != "all" && direction != "in" && direction != "out" {
		return query.ActionFilter{}, fmt.Errorf("direction must be all, in or out")
	}
	status := strings.ToLower(q.Get("status"))
	if status != "" && status != "success" && status != "failed" {
		return query.ActionFilter{}, fmt.Errorf("status must be success or failed")
	}
	sort := strings.ToLower(q.Get("sort"))
	if sort != "" && sort != "asc" && sort != "desc" {
		return query.ActionFilter{}, fmt.Errorf("sort must be asc or desc")
	}
	return query.ActionFilter{Chain: chain, Address: address, Direction: direction, Token: q.Get("token"), TokenContract: q.Get("token_contract"), Counterparty: q.Get("counterparty"), FromAddress: q.Get("from_address"), ToAddress: q.Get("to_address"), Status: status, MethodID: q.Get("method_id"), Sort: sort, StartTime: start, EndTime: end, MinAmount: firstNonEmpty(q.Get("min_amount"), q.Get("min_value")), MaxAmount: firstNonEmpty(q.Get("max_amount"), q.Get("max_value")), Page: page, PageSize: size}, nil
}
func firstNonEmpty(v ...string) string {
	for _, s := range v {
		if strings.TrimSpace(s) != "" {
			return strings.TrimSpace(s)
		}
	}
	return ""
}
func publicActionChain(chain string) string {
	if chain == "eth" {
		return "ethereum"
	}
	return chain
}

func (s *Server) handleActionOverview(w http.ResponseWriter, r *http.Request) {
	c, a, e := actionIdentity(r)
	if e != nil {
		actionFail(w, 400, "INVALID_ADDRESS", e.Error())
		return
	}
	type balanceResult struct {
		balances []provider.CurrentBalance
		err      error
	}
	var balanceCh chan balanceResult
	if resolver, ok := s.deps.Prov.(provider.CurrentBalanceResolver); ok {
		balanceCh = make(chan balanceResult, 1)
		go func() {
			balances, err := resolver.CurrentBalances(r.Context(), c, a)
			balanceCh <- balanceResult{balances: balances, err: err}
		}()
	}
	overviews, e := s.query.ActionBatchOverview(r.Context(), c, []string{a})
	if e != nil {
		if s.deps.Log != nil {
			s.deps.Log.Error("action address overview query failed", "chain", c, "address", a, "err", e)
		}
		actionFail(w, 500, "INTERNAL_ERROR", "Unable to query address overview")
		return
	}
	o := overviews[0]
	o.Chain = publicActionChain(o.Chain)
	if balanceCh != nil {
		if balanceResult := <-balanceCh; balanceResult.err == nil {
			for _, balance := range balanceResult.balances {
				if balance.Native {
					o.NativeBalance = baseUnitBalance(balance.RawBalance, 6)
					o.NativeBalanceSource = "balance_snapshot"
					break
				}
			}
		}
	}
	if s.deps.AddressType != nil && c != "tron" {
		if types, e := s.deps.AddressType.ResolveAddressTypes(r.Context(), c, []string{a}); e == nil {
			if types[a] {
				o.AddressType = "contract"
			} else {
				o.AddressType = "wallet"
			}
		}
	}
	meta := s.actionCoverageMeta(r.Context(), c, []string{a}, []string{"transactions", "token_transfers"})
	meta["coverage"].(map[string]any)["not_asserted_datasets"] = []string{"internal_transactions"}
	actionOK(w, o, meta)
}
func (s *Server) handleActionAssets(w http.ResponseWriter, r *http.Request) {
	c, a, e := actionIdentity(r)
	if e != nil {
		actionFail(w, 400, "INVALID_ADDRESS", e.Error())
		return
	}
	assets, total, e := s.query.ActionAssets(r.Context(), c, a)
	if e != nil {
		actionFail(w, 500, "INTERNAL_ERROR", "Unable to query address assets")
		return
	}
	native := "0"
	nativeSource := "unavailable"
	nativeCurrent := false
	var nativeAsOfBlock, nativeAsOfTime any
	tokenAssets := make([]map[string]any, 0, len(assets))
	for _, x := range assets {
		if fmt.Sprint(x["token_address"]) == "" {
			native = fmt.Sprint(x["balance"])
			nativeSource = fmt.Sprint(x["balance_source"])
			nativeCurrent, _ = x["is_current_balance"].(bool)
			nativeAsOfBlock, nativeAsOfTime = x["as_of_block"], x["as_of_time"]
			continue
		}
		tokenAssets = append(tokenAssets, x)
	}
	meta := map[string]any{}
	if resolver, ok := s.deps.Prov.(provider.CurrentBalanceResolver); ok {
		if balances, balanceErr := resolver.CurrentBalances(r.Context(), c, a); balanceErr == nil {
			native, nativeSource, nativeCurrent, nativeAsOfBlock, nativeAsOfTime, tokenAssets = applyCurrentBalanceSnapshot(balances, tokenAssets)
			total = ""
			meta["balance_snapshot_source"] = "provider_latest"
		} else {
			meta["balance_snapshot_error"] = "Current provider balance unavailable; historical evidence returned"
		}
	}
	var estimatedUSD any
	if total != "" && total != "unavailable" {
		estimatedUSD = total
	}
	actionOK(w, map[string]any{"address": a, "native_balance": native, "native_balance_source": nativeSource, "native_is_current_balance": nativeCurrent, "native_as_of_block": nativeAsOfBlock, "native_as_of_time": nativeAsOfTime, "assets": tokenAssets, "estimated_total_usd": estimatedUSD}, meta)
}

func baseUnitBalance(raw string, decimals int) string {
	value, err := decimal.NewFromString(strings.TrimSpace(raw))
	if err != nil {
		return "0"
	}
	return value.Shift(-int32(decimals)).String()
}

func applyCurrentBalanceSnapshot(snapshot []provider.CurrentBalance, assets []map[string]any) (string, string, bool, any, any, []map[string]any) {
	native, nativeSource, nativeCurrent := "0", "unavailable", false
	var nativeBlock, nativeTime any
	tokens := make(map[string]provider.CurrentBalance, len(snapshot))
	for _, balance := range snapshot {
		if balance.Native {
			native = baseUnitBalance(balance.RawBalance, 6)
			nativeSource, nativeCurrent, nativeTime = "balance_snapshot", true, balance.AsOfTime
			continue
		}
		tokens[strings.ToLower(balance.TokenAddress)] = balance
	}
	for _, asset := range assets {
		contract := strings.ToLower(strings.TrimSpace(fmt.Sprint(asset["token_address"])))
		decimals, _ := strconv.Atoi(fmt.Sprint(asset["token_decimals"]))
		balance, exists := tokens[contract]
		raw := "0"
		asOf := nativeTime
		if exists {
			raw, asOf = balance.RawBalance, balance.AsOfTime
		}
		asset["balance"] = baseUnitBalance(raw, decimals)
		asset["raw_balance"] = raw
		asset["balance_source"] = "balance_snapshot"
		asset["is_current_balance"] = true
		asset["as_of_time"] = asOf
		asset["as_of_block"] = nil
		asset["usd_value"] = nil
		asset["balance_usd"] = nil
	}
	return native, nativeSource, nativeCurrent, nativeBlock, nativeTime, assets
}
func (s *Server) handleActionRows(w http.ResponseWriter, r *http.Request, dataset string) {
	f, e := parseActionFilter(r)
	if e != nil {
		actionFail(w, 400, "INVALID_QUERY", e.Error())
		return
	}
	p, e := s.query.ActionRows(r.Context(), dataset, f)
	if e != nil {
		// The blanket 500 used to swallow the cause, so a strict ScanStruct
		// mismatch and a real server refusal looked identical in the log.
		if s.deps.Log != nil {
			s.deps.Log.Error("action rows query failed", "dataset", dataset, "chain", f.Chain, "address", f.Address, "err", e)
		}
		actionFail(w, 500, "INTERNAL_ERROR", "Unable to query records")
		return
	}
	var nextPage any
	if p.HasMore {
		nextPage = p.Page + 1
	}
	actionOK(w, p.Items, map[string]any{"pagination": map[string]any{"page": p.Page, "page_size": p.PageSize, "returned": len(p.Items), "total": p.Total, "has_more": p.HasMore, "next_page": nextPage}})
}
func (s *Server) handleActionCounterparties(w http.ResponseWriter, r *http.Request) {
	f, e := parseActionFilter(r)
	if e != nil {
		actionFail(w, 400, "INVALID_QUERY", e.Error())
		return
	}
	limit, _ := strconv.Atoi(r.URL.Query().Get("limit"))
	minCount, _ := strconv.Atoi(r.URL.Query().Get("min_tx_count"))
	rows, e := s.query.ActionCounterparties(r.Context(), f, r.URL.Query().Get("min_total_amount"), minCount, limit, r.URL.Query().Get("sort_by"))
	if e != nil {
		if s.deps.Log != nil {
			s.deps.Log.Error("action counterparties query failed", "chain", f.Chain, "address", f.Address, "err", e)
		}
		actionFail(w, 500, "INTERNAL_ERROR", "Unable to aggregate counterparties")
		return
	}
	actionOK(w, rows, map[string]any{"count": len(rows)})
}
func flowMap(f query.FlowSummary) map[string]any {
	return map[string]any{"total_in": f.TotalIn, "total_out": f.TotalOut, "net_flow": f.NetFlow, "in_tx_count": f.InTxCount, "out_tx_count": f.OutTxCount, "unique_in_addresses": f.UniqueInAddresses, "unique_out_addresses": f.UniqueOutAddresses, "first_in_time": f.FirstInTime, "last_in_time": f.LastInTime, "first_out_time": f.FirstOutTime, "last_out_time": f.LastOutTime}
}
func (s *Server) handleActionFlowSummary(w http.ResponseWriter, r *http.Request) {
	f, e := parseActionFilter(r)
	if e != nil {
		actionFail(w, 400, "INVALID_QUERY", e.Error())
		return
	}
	o, e := s.query.ActionFlowSummary(r.Context(), f.Chain, f.Address, f.Token, f.TokenContract, f.StartTime, f.EndTime)
	if e != nil {
		// The underlying cause was previously swallowed by the blanket 500, so a
		// server refusal and a code defect looked identical in the log.
		if s.deps.Log != nil {
			s.deps.Log.Error("action flow summary query failed", "chain", f.Chain, "address", f.Address, "err", e)
		}
		// A scan refused on its own bounded budget is reported with a diagnosable
		// code and a retryable status, so a timeout or a Code 241 is no longer
		// indistinguishable from a code defect. The caller can act on it by
		// narrowing start_time and end_time; anything else stays a 500.
		switch {
		case errors.Is(e, clickhouse.ErrScanTooLarge):
			actionFail(w, http.StatusServiceUnavailable, "SCAN_TOO_LARGE", "The flow aggregate exceeds the bounded ClickHouse scan budget; narrow it with start_time and end_time")
			return
		case errors.Is(e, clickhouse.ErrScanTimeout):
			actionFail(w, http.StatusServiceUnavailable, "SCAN_TIMEOUT", "The flow aggregate exceeded the bounded ClickHouse scan time; narrow it with start_time and end_time")
			return
		}
		actionFail(w, 500, "INTERNAL_ERROR", "Unable to aggregate flow")
		return
	}
	data := flowMap(o)
	data["address"] = f.Address
	data["token_symbol"] = strings.ToUpper(strings.TrimSpace(f.Token))
	data["token_contract"] = f.TokenContract
	actionOK(w, data, nil)
}
func (s *Server) handleActionTopFlows(w http.ResponseWriter, r *http.Request) {
	f, e := parseActionFilter(r)
	if e != nil {
		actionFail(w, 400, "INVALID_QUERY", e.Error())
		return
	}
	limit, _ := strconv.Atoi(r.URL.Query().Get("limit"))
	groupBy := r.URL.Query().Get("group_by")
	rows, e := s.query.ActionTopFlows(r.Context(), f, groupBy, limit)
	if e != nil {
		if s.deps.Log != nil {
			s.deps.Log.Error("action top flows query failed", "chain", f.Chain, "address", f.Address, "group_by", groupBy, "err", e)
		}
		actionFail(w, 500, "INTERNAL_ERROR", "Unable to query top flows")
		return
	}
	actionOK(w, rows, map[string]any{"count": len(rows)})
}
func (s *Server) handleActionFirstFunding(w http.ResponseWriter, r *http.Request) {
	c, a, e := actionIdentity(r)
	if e != nil {
		actionFail(w, 400, "INVALID_ADDRESS", e.Error())
		return
	}
	data, e := s.query.ActionFirstFunding(r.Context(), c, a)
	if e != nil {
		actionFail(w, 500, "INTERNAL_ERROR", "Unable to query first funding")
		return
	}
	data["coverage_complete_before_first_observed_tx"] = false
	actionOK(w, data, map[string]any{"evidence_scope": "earliest observed within platform coverage; not asserted as the address's true first funding"})
}

func actionTransactionIdentity(r *http.Request) (string, string, error) {
	c, e := canonicalActionChain(r.URL.Query().Get("chain"))
	if e != nil {
		return "", "", e
	}
	h, e := chain.ParseHash(r.URL.Query().Get("tx_hash"))
	if e != nil {
		return "", "", e
	}
	return c, h.String(), nil
}
func (s *Server) transactionActionData(ctx context.Context, c, h string) (map[string]any, error) {
	if s.detail == nil {
		return nil, errors.New("transaction detail disabled")
	}
	d, e := s.detail.Detail(ctx, c, h, method.DecodeAuto)
	if e != nil {
		return nil, e
	}
	transfers, e := s.query.ActionTransactionRows(ctx, "token_transfers", c, h)
	if e != nil {
		return nil, e
	}
	internal, e := s.query.ActionTransactionRows(ctx, "internal_transactions", c, h)
	if e != nil {
		return nil, e
	}
	var receipt any
	if s.deps.ChainRead != nil {
		hash, _ := chain.ParseHash(h)
		if value, err := s.deps.ChainRead.Receipt(ctx, c, hash, chainread.Options{Purpose: rpc.PurposeReceipt, Priority: rpc.PriorityInteractive}); err == nil {
			receipt = value
		}
	}
	return map[string]any{"tx_hash": h, "transaction": d.Transaction, "receipt": receipt, "decoded_input": d.DecodedInput, "token_transfers": transfers, "internal_transactions": internal, "logs": d.Logs}, nil
}
func (s *Server) handleActionTransactionDetail(w http.ResponseWriter, r *http.Request) {
	c, h, e := actionTransactionIdentity(r)
	if e != nil {
		actionFail(w, 400, "INVALID_TX_HASH", e.Error())
		return
	}
	data, e := s.transactionActionData(r.Context(), c, h)
	if errors.Is(e, method.ErrTransactionNotFound) {
		actionFail(w, 404, "TRANSACTION_NOT_FOUND", "Transaction not found")
		return
	}
	if e != nil {
		actionFail(w, 500, "INTERNAL_ERROR", "Unable to query transaction detail")
		return
	}
	actionOK(w, data, nil)
}

func (s *Server) handleActionTransactionDecode(w http.ResponseWriter, r *http.Request) {
	c, h, e := actionTransactionIdentity(r)
	if e != nil {
		actionFail(w, 400, "INVALID_TX_HASH", e.Error())
		return
	}
	if s.detail == nil {
		actionFail(w, 503, "INTERNAL_ERROR", "Transaction detail service is disabled")
		return
	}
	d, e := s.detail.Detail(r.Context(), c, h, method.DecodeAuto)
	if errors.Is(e, method.ErrTransactionNotFound) {
		actionFail(w, 404, "TRANSACTION_NOT_FOUND", "Transaction not found")
		return
	}
	if e != nil {
		actionFail(w, 500, "INTERNAL_ERROR", "Unable to decode transaction")
		return
	}
	decoded := d.DecodedInput != nil
	data := map[string]any{"chain": publicActionChain(c), "tx_hash": h, "to_address": d.Transaction["to_address"], "method_id": d.Method.ID, "method_name": d.Method.Name, "method_signature": d.Method.Signature, "decoded": decoded, "decoded_input": d.DecodedInput, "decoded_output": nil, "abi_source": "unknown", "selector_source": d.Method.Source, "confidence": "low"}
	actionOK(w, data, map[string]any{"decode_warning": "Treat names as unverified unless abi_source is verified_abi or local_abi"})
}
func (s *Server) handleActionTransactionLogs(w http.ResponseWriter, r *http.Request) {
	c, h, e := actionTransactionIdentity(r)
	if e != nil {
		actionFail(w, 400, "INVALID_TX_HASH", e.Error())
		return
	}
	rows, e := s.query.ActionTransactionRows(r.Context(), "logs", c, h)
	if e != nil {
		actionFail(w, 500, "INTERNAL_ERROR", "Unable to query transaction logs")
		return
	}
	actionOK(w, rows, map[string]any{"count": len(rows)})
}
func (s *Server) handleActionTransactionTrace(w http.ResponseWriter, r *http.Request) {
	c, h, e := actionTransactionIdentity(r)
	if e != nil {
		actionFail(w, 400, "INVALID_TX_HASH", e.Error())
		return
	}
	if s.deps.Trace == nil {
		actionFail(w, 503, "INTERNAL_ERROR", "Trace service is disabled")
		return
	}
	hash, _ := chain.ParseHash(h)
	result, e := s.deps.Trace.TransactionTrace(r.Context(), c, hash, chaintrace.Options{})
	if errors.Is(e, chaintrace.ErrTraceNotAvailableLocally) {
		actionFail(w, 404, "TRANSACTION_NOT_FOUND", e.Error())
		return
	}
	if e != nil {
		actionFail(w, 500, "INTERNAL_ERROR", "Unable to query trace")
		return
	}
	actionOK(w, result, nil)
}

type actionBatchRequest struct {
	Chain         string   `json:"chain"`
	Addresses     []string `json:"addresses"`
	Token         string   `json:"token"`
	TokenContract string   `json:"token_contract"`
	MinBalance    string   `json:"min_balance"`
}

func decodeActionJSON(r *http.Request, v any) error {
	defer r.Body.Close()
	d := json.NewDecoder(io.LimitReader(r.Body, 2<<20))
	d.DisallowUnknownFields()
	return d.Decode(v)
}
func validateActionBatch(req actionBatchRequest) (string, []string, error) {
	c, e := canonicalActionChain(req.Chain)
	if e != nil {
		return "", nil, e
	}
	if len(req.Addresses) == 0 || len(req.Addresses) > 200 {
		return "", nil, fmt.Errorf("addresses must contain 1 to 200 items")
	}
	out := make([]string, 0, len(req.Addresses))
	seen := map[string]bool{}
	for _, a := range req.Addresses {
		normalized, e := addressnorm.Normalize(c, a)
		if e != nil {
			return "", nil, e
		}
		a = normalized
		if !seen[a] {
			seen[a] = true
			out = append(out, a)
		}
	}
	return c, out, nil
}
func (s *Server) handleActionBatchOverview(w http.ResponseWriter, r *http.Request) {
	var req actionBatchRequest
	if e := decodeActionJSON(r, &req); e != nil {
		actionFail(w, 400, "INVALID_QUERY", e.Error())
		return
	}
	c, addresses, e := validateActionBatch(req)
	if e != nil {
		actionFail(w, 400, "INVALID_ADDRESS", e.Error())
		return
	}
	out, e := s.query.ActionBatchOverview(r.Context(), c, addresses)
	if e != nil {
		if s.deps.Log != nil {
			s.deps.Log.Error("action batch overview query failed", "chain", c, "address_count", len(addresses), "err", e)
		}
		actionFail(w, 500, "INTERNAL_ERROR", "Unable to query batch overview")
		return
	}
	for i := range out {
		out[i].Chain = publicActionChain(out[i].Chain)
	}
	meta := s.actionCoverageMeta(r.Context(), c, addresses, []string{"transactions", "token_transfers"})
	meta["count"] = len(out)
	meta["coverage"].(map[string]any)["not_asserted_datasets"] = []string{"internal_transactions"}
	actionOK(w, out, meta)
}
func (s *Server) handleActionBatchAssets(w http.ResponseWriter, r *http.Request) {
	var req actionBatchRequest
	if e := decodeActionJSON(r, &req); e != nil {
		actionFail(w, 400, "INVALID_QUERY", e.Error())
		return
	}
	c, addresses, e := validateActionBatch(req)
	if e != nil {
		actionFail(w, 400, "INVALID_ADDRESS", e.Error())
		return
	}
	out := []map[string]any{}
	for _, a := range addresses {
		assets, total, e := s.query.ActionAssets(r.Context(), c, a)
		if e != nil {
			actionFail(w, 500, "INTERNAL_ERROR", "Unable to query batch assets")
			return
		}
		filtered := make([]map[string]any, 0, len(assets))
		for _, asset := range assets {
			if req.Token != "" && !strings.EqualFold(fmt.Sprint(asset["token_symbol"]), req.Token) {
				continue
			}
			if req.TokenContract != "" && !strings.EqualFold(fmt.Sprint(asset["token_address"]), req.TokenContract) {
				continue
			}
			if req.MinBalance != "" {
				got, ge := decimal.NewFromString(fmt.Sprint(asset["balance"]))
				min, me := decimal.NewFromString(req.MinBalance)
				if ge != nil || me != nil || got.LessThan(min) {
					continue
				}
			}
			filtered = append(filtered, asset)
		}
		out = append(out, map[string]any{"address": a, "assets": filtered, "estimated_total_usd": total})
	}
	actionOK(w, out, map[string]any{"count": len(out)})
}

type actionPathRequest struct {
	Chain            string     `json:"chain"`
	Source           string     `json:"source"`
	Token            string     `json:"token"`
	TokenContract    string     `json:"token_contract"`
	MinAmount        string     `json:"min_amount"`
	MaxDepth         int        `json:"max_depth"`
	MaxNodes         int        `json:"max_nodes"`
	StartTime        *time.Time `json:"start_time"`
	EndTime          *time.Time `json:"end_time"`
	ExcludeAddresses []string   `json:"exclude_addresses"`
}

func (s *Server) buildActionNetwork(ctx context.Context, req actionPathRequest) ([]map[string]any, []map[string]any, error) {
	c, e := canonicalActionChain(req.Chain)
	if e != nil {
		return nil, nil, e
	}
	if strings.TrimSpace(req.Token) == "" && strings.TrimSpace(req.TokenContract) == "" {
		return nil, nil, fmt.Errorf("token or token_contract is required; cross-asset amounts cannot be aggregated")
	}
	if req.MaxDepth <= 0 {
		req.MaxDepth = 3
	}
	if req.MaxDepth > 8 {
		return nil, nil, fmt.Errorf("max_depth cannot exceed 8")
	}
	if req.MaxNodes <= 0 {
		req.MaxNodes = 500
	}
	if req.MaxNodes > 2000 {
		return nil, nil, fmt.Errorf("max_nodes cannot exceed 2000")
	}
	// 按链归一化：Solana 保留 base58 大小写，EVM 折叠小写，TRON 归一到 Base58Check。
	source, e := addressnorm.Normalize(c, req.Source)
	if e != nil {
		return nil, nil, e
	}
	// 图遍历的去重键同样按链归一化：Solana 地址折叠后会被误当成同一个节点。
	blocked := map[string]bool{}
	for _, a := range req.ExcludeAddresses {
		blocked[addressnorm.NormalizeKey(c, a)] = true
	}
	seen := map[string]bool{addressnorm.NormalizeKey(c, source): true}
	frontier := []string{source}
	nodes := []map[string]any{{"address": source, "depth": 0, "current_balance": nil, "balance_source": "unavailable", "is_current_balance": false, "balance_as_of_block": nil, "balance_as_of_time": nil}}
	edges := []map[string]any{}
	for depth := 1; depth <= req.MaxDepth && len(frontier) > 0 && len(nodes) < req.MaxNodes; depth++ {
		found, e := s.query.ActionEdges(ctx, c, frontier, req.Token, req.TokenContract, req.MinAmount, req.StartTime, req.EndTime, req.MaxNodes-len(nodes))
		if e != nil {
			return nil, nil, e
		}
		next := []string{}
		for _, x := range found {
			if blocked[addressnorm.NormalizeKey(c, x.To)] {
				continue
			}
			edges = append(edges, map[string]any{"from": x.From, "to": x.To, "token": x.Token, "token_symbol": x.Token, "token_contract": x.TokenContract, "amount": x.Amount, "tx_count": x.TxCount, "first_time": x.FirstTime, "last_time": x.LastTime, "depth": depth})
			key := addressnorm.NormalizeKey(c, x.To)
			if !seen[key] && len(nodes) < req.MaxNodes {
				seen[key] = true
				next = append(next, x.To)
				nodes = append(nodes, map[string]any{"address": x.To, "depth": depth, "current_balance": nil, "balance_source": "unavailable", "is_current_balance": false, "balance_as_of_block": nil, "balance_as_of_time": nil})
			}
		}
		frontier = next
	}
	return nodes, edges, nil
}

func actionTraversalMeta(nodes, edges []map[string]any, requestedDepth, requestedNodes int) map[string]any {
	if requestedDepth <= 0 {
		requestedDepth = 3
	}
	if requestedNodes <= 0 {
		requestedNodes = 500
	}
	reached := 0
	for _, node := range nodes {
		if d, ok := node["depth"].(int); ok && d > reached {
			reached = d
		}
	}
	limitReached := len(nodes) >= requestedNodes
	return map[string]any{"truncated": limitReached, "node_count": len(nodes), "edge_count": len(edges), "max_depth_requested": requestedDepth, "max_depth_reached": reached, "node_limit_reached": limitReached}
}
func (s *Server) handleActionPath(w http.ResponseWriter, r *http.Request) {
	var req actionPathRequest
	if e := decodeActionJSON(r, &req); e != nil {
		actionFail(w, 400, "INVALID_QUERY", e.Error())
		return
	}
	nodes, edges, e := s.buildActionNetwork(r.Context(), req)
	if e != nil {
		actionFail(w, 400, "INVALID_QUERY", e.Error())
		return
	}
	pathByAddress := map[string][]string{addressnorm.NormalizeKey(req.Chain, req.Source): {req.Source}}
	paths := make([]map[string]any, 0, len(edges))
	for _, edge := range edges {
		from, to := fmt.Sprint(edge["from"]), fmt.Sprint(edge["to"])
		base := append([]string(nil), pathByAddress[addressnorm.NormalizeKey(req.Chain, from)]...)
		if len(base) == 0 {
			base = []string{from}
		}
		path := append(base, to)
		if _, exists := pathByAddress[addressnorm.NormalizeKey(req.Chain, to)]; !exists {
			pathByAddress[addressnorm.NormalizeKey(req.Chain, to)] = path
		}
		paths = append(paths, map[string]any{"path_id": fmt.Sprintf("path-%d", len(paths)+1), "addresses": path, "hop_count": len(path) - 1, "total_amount": edge["amount"], "hops": []any{edge}, "hop": edge})
	}
	data := map[string]any{"source": req.Source, "paths": paths, "nodes": nodes, "edges": edges}
	for k, v := range actionTraversalMeta(nodes, edges, req.MaxDepth, req.MaxNodes) {
		data[k] = v
	}
	actionOK(w, data, map[string]any{"bounded_traversal": true})
}
func (s *Server) handleActionNetwork(w http.ResponseWriter, r *http.Request) {
	var raw struct {
		Chain          string     `json:"chain"`
		SeedAddresses  []string   `json:"seed_addresses"`
		Token          string     `json:"token"`
		TokenContract  string     `json:"token_contract"`
		StartTime      *time.Time `json:"start_time"`
		EndTime        *time.Time `json:"end_time"`
		MinTotalAmount string     `json:"min_total_amount"`
		MaxDepth       int        `json:"max_depth"`
		MaxNodes       int        `json:"max_nodes"`
	}
	if e := decodeActionJSON(r, &raw); e != nil {
		actionFail(w, 400, "INVALID_QUERY", e.Error())
		return
	}
	if len(raw.SeedAddresses) == 0 {
		actionFail(w, 400, "INVALID_QUERY", "seed_addresses required")
		return
	}
	allNodes := []map[string]any{}
	allEdges := []map[string]any{}
	for _, seed := range raw.SeedAddresses {
		n, e2, e := s.buildActionNetwork(r.Context(), actionPathRequest{Chain: raw.Chain, Source: seed, Token: raw.Token, TokenContract: raw.TokenContract, MinAmount: raw.MinTotalAmount, MaxDepth: raw.MaxDepth, MaxNodes: raw.MaxNodes, StartTime: raw.StartTime, EndTime: raw.EndTime})
		if e != nil {
			actionFail(w, 400, "INVALID_QUERY", e.Error())
			return
		}
		allNodes = append(allNodes, n...)
		allEdges = append(allEdges, e2...)
	}
	uniqueNodes := make([]map[string]any, 0, len(allNodes))
	seenNodes := map[string]bool{}
	for _, node := range allNodes {
		key := addressnorm.NormalizeKey(raw.Chain, fmt.Sprint(node["address"]))
		if !seenNodes[key] {
			seenNodes[key] = true
			uniqueNodes = append(uniqueNodes, node)
		}
	}
	uniqueEdges := make([]map[string]any, 0, len(allEdges))
	seenEdges := map[string]bool{}
	for _, edge := range allEdges {
		key := addressnorm.NormalizeKey(raw.Chain, fmt.Sprint(edge["from"])) + "|" + addressnorm.NormalizeKey(raw.Chain, fmt.Sprint(edge["to"])) + "|" + strings.ToUpper(fmt.Sprint(edge["token"]))
		if !seenEdges[key] {
			seenEdges[key] = true
			uniqueEdges = append(uniqueEdges, edge)
		}
	}
	allNodes, allEdges = uniqueNodes, uniqueEdges
	data := map[string]any{"nodes": allNodes, "edges": allEdges}
	for k, v := range actionTraversalMeta(allNodes, allEdges, raw.MaxDepth, raw.MaxNodes) {
		data[k] = v
	}
	actionOK(w, data, map[string]any{"bounded_traversal": true})
}

func (s *Server) handleActionCreateExport(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Chain              string   `json:"chain"`
		Address            string   `json:"address"`
		Format             string   `json:"format"`
		Compression        string   `json:"compression"`
		Token              string   `json:"token"`
		TokenContract      string   `json:"token_contract"`
		DataTypes          []string `json:"data_types"`
		StartTime, EndTime *time.Time
	}
	if e := decodeActionJSON(r, &req); e != nil {
		actionFail(w, 400, "INVALID_QUERY", e.Error())
		return
	}
	c, e := canonicalActionChain(req.Chain)
	if e != nil {
		actionFail(w, 400, "INVALID_CHAIN", e.Error())
		return
	}
	if len(req.DataTypes) == 0 {
		actionFail(w, 400, "INVALID_QUERY", "data_types required")
		return
	}
	if req.Format == "" {
		req.Format = "parquet"
	}
	if req.Format != "parquet" && req.Format != "csv" {
		actionFail(w, 400, "INVALID_QUERY", "format must be parquet or csv")
		return
	}
	if req.Compression == "" {
		if req.Format == "parquet" {
			req.Compression = "zstd"
		} else {
			req.Compression = "zip"
		}
	}
	if (req.Format == "parquet" && req.Compression != "zstd") || (req.Format == "csv" && req.Compression != "zip") {
		actionFail(w, 400, "INVALID_QUERY", "supported export pairs are parquet+zstd and csv+zip")
		return
	}
	for _, d := range req.DataTypes {
		if d != "transactions" && d != "token_transfers" && d != "internal_transactions" && d != "logs" {
			actionFail(w, 400, "INVALID_QUERY", "unsupported data_type "+d)
			return
		}
	}
	zipIt := req.Format == "csv" || len(req.DataTypes) > 1
	snapshot, _ := json.Marshal(map[string]any{"chain": c, "addresses": []string{req.Address}, "datasets": req.DataTypes, "format": req.Format, "compression": req.Compression, "token": req.Token, "token_contract": req.TokenContract, "start_time": req.StartTime, "end_time": req.EndTime})
	// GPT Actions 走独立的 Bearer API Key，不产生 session 主体，因此使用
	// systemScope 显式解析平台数据面的租户绑定（见 objectaccess.go 的说明）。
	scope, workspaceErr := s.systemScope(r.Context())
	if workspaceErr != nil {
		actionFail(w, 500, "INTERNAL_ERROR", "Unable to resolve export workspace")
		return
	}
	orgID, wsID := scope.OrganizationID, scope.WorkspaceID
	exp, e := s.deps.Store.CreateExportV2(r.Context(), wsID, orgID, nil, nil, strings.Join(req.DataTypes, "+"), req.Format, zipIt, snapshot)
	if e != nil {
		actionFail(w, 500, "INTERNAL_ERROR", "Unable to create export")
		return
	}
	var boundsJob *model.DownloadJob
	if req.StartTime != nil || req.EndTime != nil || req.Token != "" || req.TokenContract != "" {
		boundsJob = &model.DownloadJob{RangeType: "time", FromTimestamp: req.StartTime, ToTimestamp: req.EndTime, Status: model.JobCompleted, Stats: map[string]any{"gpt_actions_token_filter": strings.ToUpper(strings.TrimSpace(req.Token)), "gpt_actions_token_contract_filter": strings.TrimSpace(req.TokenContract)}}
	}
	go s.runExportMulti(backgroundAuditCtx(r), exp, boundsJob, c, []string{req.Address}, req.DataTypes, req.Format, true, zipIt, false)
	writeJSON(w, http.StatusAccepted, actionEnvelope{Success: true, Data: map[string]any{"job_id": exp.ID, "status": "processing"}, Meta: map[string]any{}})
}

// handleActionExportStatus 返回 Actions 自己创建的导出状态。
//
// P0 修复（Actions 侧越权读取）：旧实现只要 job_id 是一个合法 UUID，就返回
// **任意**导出的元数据，并在 COMPLETED 时签发下载票据 —— 也就是说一个
// "export" scope 的 API Key 可以读取任何组织创建的导出（靠 UUID 不可枚举性兜底，
// 不构成授权）。这里把可读范围收窄到本 Actions 平台作用域内的导出，
// 越界仍返回 404（不泄露存在性），API Key 契约（路径/字段/状态码语义）保持不变。
func (s *Server) handleActionExportStatus(w http.ResponseWriter, r *http.Request) {
	id := strings.TrimSpace(r.URL.Query().Get("job_id"))
	if _, e := uuid.Parse(id); e != nil {
		actionFail(w, 400, "JOB_NOT_FOUND", "Invalid job_id")
		return
	}
	scope, scopeErr := s.systemScope(r.Context())
	if scopeErr != nil {
		actionFail(w, 500, "INTERNAL_ERROR", "Unable to resolve Actions workspace")
		return
	}
	var exportOrg, exportWS string
	if e := s.deps.Store.Pool.QueryRow(r.Context(),
		`SELECT COALESCE(organization_id::text,''), COALESCE(workspace_id::text,'') FROM exports WHERE id=$1`, id).
		Scan(&exportOrg, &exportWS); e != nil {
		actionFail(w, 404, "JOB_NOT_FOUND", "Export job not found")
		return
	}
	if exportOrg != scope.OrganizationID || exportWS != scope.WorkspaceID {
		actionFail(w, 404, "JOB_NOT_FOUND", "Export job not found")
		return
	}
	exp, e := s.deps.Store.GetExport(r.Context(), id)
	if errors.Is(e, pgx.ErrNoRows) || exp == nil {
		actionFail(w, 404, "JOB_NOT_FOUND", "Export job not found")
		return
	}
	if e != nil {
		actionFail(w, 500, "INTERNAL_ERROR", "Unable to query export")
		return
	}
	data := map[string]any{"job_id": exp.ID, "status": strings.ToLower(exp.Status), "total_rows": exp.Rows, "row_count": exp.Rows, "format": exp.Format, "compression": nil, "files": []any{}, "manifest_url": nil, "download_url": nil, "sha256": nil, "expires_at": nil}
	if exp.Status == "COMPLETED" {
		// 票据绑定到导出对象自己的租户归属（上面已校验它与 Actions 作用域一致）。
		if ticketID, ticketErr := s.deps.Store.CreateExportTicket(r.Context(), exp.ID, nil, exportOrg, exportWS, 15*time.Minute); ticketErr == nil {
			if ticketErr == nil {
				scheme := r.Header.Get("X-Forwarded-Proto")
				if scheme == "" {
					scheme = "https"
				}
				data["download_url"] = scheme + "://" + r.Host + "/api/v1/download/" + ticketID
				data["expires_at"] = time.Now().UTC().Add(15 * time.Minute)
			}
		}
	}
	if exp.Status == "FAILED" {
		data["error"] = exp.Error
	}
	actionOK(w, data, nil)
}
