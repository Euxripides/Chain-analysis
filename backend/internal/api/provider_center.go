package api

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/url"
	"os"
	"strconv"
	"strings"
	"time"

	"github.com/blockchain-platform/blockchain-platform/internal/model"
	"github.com/blockchain-platform/blockchain-platform/internal/provider/ankrmgmt"
	"github.com/blockchain-platform/blockchain-platform/internal/provider/providercontrol"
	"github.com/go-chi/chi/v5"
	"github.com/jackc/pgx/v5/pgconn"
)

func (s *Server) registerProviderCenterRoutes(r chi.Router) {
	r.Get("/provider-center/{id}", s.require("admin:provider:read", s.handleProviderCenter))
	r.Put("/provider-center/{id}/config", s.require("admin:provider:update", s.handleProviderCenterConfig))
	r.Post("/provider-center/{id}/sync", s.require("admin:provider:update", s.handleProviderCenterSync))
	r.Post("/provider-center/{id}/test", s.require("admin:provider:read", s.handleProviderCenterTest))
	r.Post("/provider-center/ankr/connect", s.require("admin:provider:update", s.handleProviderCenterAnkrConnect))
}

func validProviderCenterKey(key string) bool { return key == "ankr" || key == "drpc" }

func (s *Server) providerCenterDefaults(ctx context.Context, key string) providercontrol.State {
	if key == "ankr" {
		if s.deps.Cfg == nil {
			return providercontrol.State{Enabled: true, Endpoints: map[string]string{}, RuntimeKey: os.Getenv("ANKR_API_KEY")}
		}
		keyValue := s.deps.Cfg.Ankr.APIKey
		if keyValue == "" {
			keyValue = s.deps.Cfg.RPC.AnkrAPIKey
		}
		return providercontrol.State{Enabled: true, RuntimeKey: keyValue, Endpoints: map[string]string{
			"advanced": s.deps.Cfg.Ankr.Endpoint, "rpc": s.deps.Cfg.RPC.AnkrEndpointTemplate,
			"tron_rpc": "https://rpc.ankr.com/tron_jsonrpc/{key}", "tron_rest": "https://rpc.ankr.com/premium-http/tron/{key}",
		}}
	}
	state := providercontrol.State{Endpoints: map[string]string{}}
	if s.deps.Cfg != nil {
		state.Enabled = s.deps.Cfg.DRPC.Enabled
		state.Endpoints["solana_rpc"] = strings.TrimRight(s.deps.Cfg.DRPC.BaseURL, "/") + "/{key}"
		if s.deps.Secrets != nil {
			state.RuntimeKey, _ = s.deps.Secrets.Resolve(ctx, s.deps.Cfg.DRPC.RuntimeKeyRef)
			state.StatisticsToken, _ = s.deps.Secrets.Resolve(ctx, s.deps.Cfg.DRPC.StatisticsTokenRef)
			state.KeysToken, _ = s.deps.Secrets.Resolve(ctx, s.deps.Cfg.DRPC.KeysTokenRef)
		}
	}
	return state
}

func (s *Server) providerCenterState(ctx context.Context, key string) (providercontrol.State, error) {
	if s.providerControl == nil {
		return providercontrol.State{}, errors.New("provider control is unavailable")
	}
	return s.providerControl.Resolve(ctx, key, s.providerCenterDefaults(ctx, key))
}

func (s *Server) handleProviderCenter(w http.ResponseWriter, r *http.Request) {
	key := chi.URLParam(r, "id")
	if !validProviderCenterKey(key) {
		respondError(w, r, http.StatusNotFound, "NOT_FOUND", "数据源不存在", nil)
		return
	}
	state, err := s.providerCenterState(r.Context(), key)
	if err != nil {
		respondError(w, r, http.StatusServiceUnavailable, "PROVIDER_CONFIG_UNAVAILABLE", "数据源配置读取失败", nil)
		return
	}
	var snapshot map[string]any
	var status, balance, unit, cuDay, source, errorCode string
	var requests string
	var keysRaw, usageRaw []byte
	var syncedAt *time.Time
	err = s.deps.Store.Pool.QueryRow(r.Context(), `SELECT connection_status,coalesce(balance,''),coalesce(balance_unit,''),coalesce(requests_day::text,''),
		coalesce(cu_day::text,''),keys,usage,source,synced_at,error_code FROM provider_official_snapshots WHERE provider_key=$1`, key).
		Scan(&status, &balance, &unit, &requests, &cuDay, &keysRaw, &usageRaw, &source, &syncedAt, &errorCode)
	if err == nil {
		var keys, usage any
		_ = json.Unmarshal(keysRaw, &keys)
		_ = json.Unmarshal(usageRaw, &usage)
		snapshot = map[string]any{"connection_status": status, "balance": balance, "balance_unit": unit,
			"requests_day": requests, "cu_day": cuDay, "keys": keys, "usage": usage, "source": source,
			"synced_at": syncedAt, "error_code": errorCode}
	}
	if key == "ankr" && snapshot != nil {
		// A stored result is a historical observation, not proof that the
		// short-lived MCP authorization or the Platform Swagger key still works.
		if source == "Ankr Management MCP" {
			var tokenExpiresAt *time.Time
			_ = s.deps.Store.Pool.QueryRow(r.Context(), `SELECT token_expires_at FROM ankr_management_connection WHERE id=1`).Scan(&tokenExpiresAt)
			if tokenExpiresAt == nil || !time.Now().Before(*tokenExpiresAt) {
				snapshot["connection_status"] = "expired"
				snapshot["error_code"] = "authorization_expired"
			}
		}
		if state.PlatformExpiresAt != nil && !time.Now().Before(*state.PlatformExpiresAt) && source == "Ankr Platform Swagger" {
			snapshot["connection_status"] = "expired"
			snapshot["error_code"] = "platform_key_expired"
		}
		if syncedAt != nil && time.Since(*syncedAt) > time.Hour && snapshot["connection_status"] == "available" {
			snapshot["connection_status"] = "stale"
			snapshot["error_code"] = "snapshot_stale"
		}
		if state.PlatformKey != "" && source != "Ankr Platform Swagger" && snapshot["connection_status"] == "available" {
			snapshot["connection_status"] = "stale"
			snapshot["error_code"] = "platform_sync_required"
		}
	}
	if key == "drpc" && snapshot == nil {
		var legacyStatus, legacyBalance, legacyUnit, legacyRequests, legacyCU, legacySource, legacyError string
		var legacyKeyID, legacyKeyName, legacySpent, legacyLimit string
		var legacyEnabled *bool
		var legacyAt time.Time
		legacyErr := s.deps.Store.Pool.QueryRow(r.Context(), `SELECT sync_status,coalesce(account_balance::text,''),
			account_balance_unit,requests_day::text,cu_day::text,source,sync_error_class,
			coalesce(key_id,''),coalesce(key_name,''),coalesce(cu_spent_today::text,''),
			coalesce(cu_day_limit::text,''),key_enabled,synced_at
			FROM provider_account_snapshots WHERE provider_key='drpc' ORDER BY synced_at DESC LIMIT 1`).Scan(
			&legacyStatus, &legacyBalance, &legacyUnit, &legacyRequests, &legacyCU, &legacySource, &legacyError,
			&legacyKeyID, &legacyKeyName, &legacySpent, &legacyLimit, &legacyEnabled, &legacyAt)
		if legacyErr == nil {
			connection := "unavailable"
			if legacyStatus == "ok" {
				connection = "available"
				if legacyError != "" {
					connection = "partial"
				}
			}
			keys := []map[string]any{}
			if legacyKeyID != "" || legacyKeyName != "" {
				keys = append(keys, map[string]any{"key_id": legacyKeyID, "name": legacyKeyName,
					"status": legacyEnabled, "selected": true, "cu_spent_today": legacySpent,
					"cu_day_limit": legacyLimit})
			}
			snapshot = map[string]any{"connection_status": connection, "balance": legacyBalance,
				"balance_unit": legacyUnit, "requests_day": legacyRequests, "cu_day": legacyCU,
				"keys": keys, "usage": map[string]any{"data": []any{}}, "source": legacySource,
				"synced_at": legacyAt, "error_code": legacyError}
		}
	}
	if key == "ankr" && snapshot == nil {
		var balanceUSD, remainingCredits string
		var legacyAt, tokenExpiresAt *time.Time
		legacyErr := s.deps.Store.Pool.QueryRow(r.Context(), `SELECT coalesce(balance_usd::text,''),
			coalesce(remaining_credits::text,''),last_synced_at,token_expires_at
			FROM ankr_management_connection WHERE id=1`).Scan(&balanceUSD, &remainingCredits, &legacyAt, &tokenExpiresAt)
		if legacyErr == nil && legacyAt != nil {
			balance, unit := balanceUSD, "USD"
			if balance == "" {
				balance, unit = remainingCredits, "credits"
			}
			connection, errorCode := "stale", "snapshot_stale"
			if tokenExpiresAt != nil && time.Now().After(*tokenExpiresAt) {
				connection, errorCode = "expired", "authorization_expired"
			}
			snapshot = map[string]any{"connection_status": connection, "balance": balance,
				"balance_unit": unit, "requests_day": nil, "cu_day": "", "keys": []any{},
				"usage": map[string]any{}, "source": "Ankr Management MCP (历史快照)",
				"synced_at": legacyAt, "error_code": errorCode}
		}
	}
	if snapshot == nil {
		snapshot = map[string]any{"connection_status": "unavailable", "source": "official provider API", "keys": []any{}, "usage": map[string]any{}}
	}
	var calls, errorsCount int64
	var p95 int
	_ = s.deps.Store.Pool.QueryRow(r.Context(), `SELECT count(*)::bigint,
		count(*) FILTER (WHERE status NOT IN ('success','ok'))::bigint,
		coalesce(percentile_disc(.95) WITHIN GROUP (ORDER BY duration_ms),0)::int
		FROM provider_usage WHERE provider=$1 AND request_time>=now()-interval '24 hours'`, key).Scan(&calls, &errorsCount, &p95)
	var detailRows []map[string]any
	rows, err := s.deps.Store.Pool.Query(r.Context(), `SELECT request_time,method,coalesce(chain,''),status,http_status,duration_ms,coalesce(error_class,'')
		FROM provider_usage WHERE provider=$1 ORDER BY request_time DESC LIMIT 25`, key)
	if err == nil {
		defer rows.Close()
		for rows.Next() {
			var at time.Time
			var method, chain, result, errorClass string
			var httpStatus, duration int
			if rows.Scan(&at, &method, &chain, &result, &httpStatus, &duration, &errorClass) == nil {
				detailRows = append(detailRows, map[string]any{"request_time": at, "method": method, "chain": chain,
					"status": result, "http_status": httpStatus, "duration_ms": duration, "error_class": errorClass})
			}
		}
	}
	if detailRows == nil {
		detailRows = []map[string]any{}
	}
	respondOK(w, r, map[string]any{"provider": key, "config": map[string]any{
		"enabled": state.Enabled, "endpoints": providercontrol.PublicEndpoints(key, state.Endpoints), "revision": state.Revision,
		"runtime_mode": func() string {
			if key == "ankr" && s.deps.Cfg != nil {
				return s.deps.Cfg.Ankr.Mode
			}
			return "live"
		}(),
		"runtime_key_configured": state.RuntimeKey != "", "statistics_token_configured": state.StatisticsToken != "",
		"keys_token_configured": state.KeysToken != "", "credential_writes_available": s.providerControl.CredentialWritesAvailable(),
		"platform_key_configured": state.PlatformKey != "", "platform_key_expires_at": state.PlatformExpiresAt,
		"management_available": key != "ankr" || (s.ankrMgmt != nil && s.deps.Cfg != nil && s.deps.Cfg.AnkrManagement.Enabled && s.deps.Cfg.AnkrManagement.RedirectURI != ""),
	}, "official": snapshot, "local": map[string]any{"calls_24h": calls, "errors_24h": errorsCount,
		"p95_ms": p95, "source": "postgres:provider_usage", "recent_calls": detailRows}})
}

type providerCenterConfigInput struct {
	Enabled           bool              `json:"enabled"`
	Endpoints         map[string]string `json:"endpoints"`
	RuntimeKey        *string           `json:"runtime_key"`
	StatisticsToken   *string           `json:"statistics_token"`
	KeysToken         *string           `json:"keys_token"`
	PlatformKey       *string           `json:"platform_key"`
	PlatformExpiresAt *time.Time        `json:"platform_key_expires_at"`
	Revision          int64             `json:"revision"`
}

func (s *Server) handleProviderCenterConfig(w http.ResponseWriter, r *http.Request) {
	key := chi.URLParam(r, "id")
	if !validProviderCenterKey(key) {
		respondError(w, r, http.StatusNotFound, "NOT_FOUND", "数据源不存在", nil)
		return
	}
	var input providerCenterConfigInput
	if err := decodeJSON(r, &input); err != nil {
		validationError(w, r, "配置不是合法 JSON", nil)
		return
	}
	if key != "ankr" && (input.PlatformKey != nil || input.PlatformExpiresAt != nil) {
		validationError(w, r, "平台管理密钥仅适用于 Ankr", nil)
		return
	}
	if input.PlatformExpiresAt != nil && !input.PlatformExpiresAt.After(time.Now()) {
		validationError(w, r, "平台管理密钥到期时间必须晚于当前时间", nil)
		return
	}
	if input.RuntimeKey != nil || input.StatisticsToken != nil || input.KeysToken != nil || input.PlatformKey != nil || input.PlatformExpiresAt != nil {
		if !PrincipalFrom(r.Context()).Has("admin:provider:key:rotate") {
			respondError(w, r, http.StatusForbidden, "FORBIDDEN", "缺少数据源密钥轮换权限", nil)
			return
		}
	}
	before, err := s.providerCenterState(r.Context(), key)
	if err != nil {
		respondError(w, r, http.StatusServiceUnavailable, "PROVIDER_CONFIG_UNAVAILABLE", "数据源配置读取失败", nil)
		return
	}
	if input.Revision != before.Revision {
		respondError(w, r, http.StatusConflict, "REVISION_CONFLICT", "数据源配置已被其他人修改", nil)
		return
	}
	if input.Endpoints == nil {
		input.Endpoints = providercontrol.PublicEndpoints(key, before.Endpoints)
	}
	if err := providercontrol.ValidateEndpoints(key, input.Endpoints); err != nil {
		validationError(w, r, err.Error(), nil)
		return
	}
	if input.Enabled {
		required := []string{"solana_rpc"}
		if key == "ankr" {
			required = []string{"advanced", "rpc", "tron_rpc", "tron_rest"}
		}
		for _, channel := range required {
			if input.Endpoints[channel] == "" {
				validationError(w, r, "启用前需要填写所有必需的请求地址模板", nil)
				return
			}
		}
	}
	if input.RuntimeKey != nil || input.StatisticsToken != nil || input.KeysToken != nil || input.PlatformKey != nil {
		if !s.providerControl.CredentialWritesAvailable() {
			respondError(w, r, http.StatusServiceUnavailable, "CREDENTIAL_ENCRYPTION_UNAVAILABLE", "数据源密钥加密未配置", nil)
			return
		}
		for _, credential := range []*string{input.RuntimeKey, input.StatisticsToken, input.KeysToken, input.PlatformKey} {
			if credential != nil && strings.TrimSpace(*credential) == "" {
				validationError(w, r, "密钥不能留空；留空输入框可保持原值", nil)
				return
			}
		}
	}
	if input.Enabled && before.RuntimeKey == "" && input.RuntimeKey == nil {
		validationError(w, r, "启用前需要配置运行时 API Key", nil)
		return
	}
	revision, err := s.providerControl.Save(r.Context(), key, input.Revision, providercontrol.Update{
		Enabled: input.Enabled, Endpoints: input.Endpoints, RuntimeKey: input.RuntimeKey,
		StatisticsToken: input.StatisticsToken, KeysToken: input.KeysToken,
		PlatformKey: input.PlatformKey, PlatformExpiresAt: input.PlatformExpiresAt})
	if err != nil {
		if errors.Is(err, providercontrol.ErrRevisionConflict) {
			respondError(w, r, http.StatusConflict, "REVISION_CONFLICT", "数据源配置已被其他人修改", nil)
		} else {
			respondError(w, r, http.StatusInternalServerError, "PROVIDER_CONFIG_SAVE_FAILED", "数据源配置保存失败", nil)
		}
		return
	}
	_, _ = s.deps.Store.Pool.Exec(r.Context(), `DELETE FROM provider_official_snapshots WHERE provider_key=$1`, key)
	if key == "ankr" {
		_, _ = s.deps.Store.Pool.Exec(r.Context(), `UPDATE provider_descriptors SET status=$1,revision=revision+1,updated_at=now() WHERE provider_key='ankr-multichain'`, map[bool]string{true: "connected", false: "disabled"}[input.Enabled])
	} else {
		_, _ = s.deps.Store.Pool.Exec(r.Context(), `UPDATE provider_descriptors SET status=$1,revision=revision+1,updated_at=now() WHERE provider_key='drpc'`, map[bool]string{true: "connected", false: "disabled"}[input.Enabled])
	}
	s.s6AuditChange(r, "admin.provider_center.update", "admin.provider", "provider", key,
		map[string]any{"enabled": before.Enabled, "endpoints": providercontrol.PublicEndpoints(key, before.Endpoints), "revision": before.Revision},
		map[string]any{"enabled": input.Enabled, "endpoints": input.Endpoints, "revision": revision,
			"runtime_key_changed": input.RuntimeKey != nil, "statistics_token_changed": input.StatisticsToken != nil,
			"keys_token_changed": input.KeysToken != nil, "platform_key_changed": input.PlatformKey != nil,
			"platform_expiry_changed": input.PlatformExpiresAt != nil}, nil)
	respondOK(w, r, map[string]any{"provider": key, "revision": revision, "enabled": input.Enabled})
}

type providerSnapshotExecer interface {
	Exec(context.Context, string, ...any) (pgconn.CommandTag, error)
}

func saveOfficialSnapshotTo(ctx context.Context, execer providerSnapshotExecer, snapshot providercontrol.OfficialSnapshot) error {
	keys, _ := json.Marshal(snapshot.Keys)
	usage, _ := json.Marshal(snapshot.Usage)
	_, err := execer.Exec(ctx, `INSERT INTO provider_official_snapshots
		(provider_key,connection_status,balance,balance_unit,requests_day,cu_day,keys,usage,source,synced_at,error_code)
		VALUES($1,$2,$3,$4,$5,NULLIF($6,'')::numeric,$7::jsonb,$8::jsonb,$9,$10,$11)
		ON CONFLICT(provider_key) DO UPDATE SET connection_status=excluded.connection_status,balance=excluded.balance,
		balance_unit=excluded.balance_unit,requests_day=excluded.requests_day,cu_day=excluded.cu_day,
		keys=excluded.keys,usage=excluded.usage,source=excluded.source,synced_at=excluded.synced_at,
		error_code=excluded.error_code,updated_at=now()`, snapshot.Provider, snapshot.ConnectionStatus,
		snapshot.Balance, snapshot.BalanceUnit, snapshot.RequestsDay, snapshot.CUDay, string(keys), string(usage),
		snapshot.Source, snapshot.SyncedAt, snapshot.ErrorCode)
	return err
}

func (s *Server) saveOfficialSnapshot(ctx context.Context, snapshot providercontrol.OfficialSnapshot) error {
	return saveOfficialSnapshotTo(ctx, s.deps.Store.Pool, snapshot)
}

func fetchAnkrPlatform(ctx context.Context, state providercontrol.State) providercontrol.OfficialSnapshot {
	if state.PlatformExpiresAt != nil && !time.Now().Before(*state.PlatformExpiresAt) {
		return providercontrol.OfficialSnapshot{Provider: "ankr", ConnectionStatus: "expired", ErrorCode: "platform_key_expired",
			Keys: []map[string]any{}, Usage: map[string]any{}, Source: "Ankr Platform Swagger", SyncedAt: time.Now().UTC()}
	}
	return (providercontrol.AnkrPlatformFetcher{}).Fetch(ctx, state.PlatformKey)
}

func (s *Server) handleProviderCenterSync(w http.ResponseWriter, r *http.Request) {
	key := chi.URLParam(r, "id")
	if !validProviderCenterKey(key) {
		respondError(w, r, http.StatusNotFound, "NOT_FOUND", "数据源不存在", nil)
		return
	}
	// A key can have been saved through another API instance moments ago.
	if s.providerControl != nil {
		s.providerControl.Invalidate(key)
	}
	var last *time.Time
	_ = s.deps.Store.Pool.QueryRow(r.Context(), `SELECT synced_at FROM provider_official_snapshots WHERE provider_key=$1`, key).Scan(&last)
	if last != nil && time.Since(*last) < time.Minute {
		respondOK(w, r, map[string]any{"provider": key, "synced_at": last, "throttled": true})
		return
	}
	var snapshot providercontrol.OfficialSnapshot
	if key == "drpc" {
		state, err := s.providerCenterState(r.Context(), key)
		if err != nil {
			respondError(w, r, http.StatusServiceUnavailable, "PROVIDER_CONFIG_UNAVAILABLE", "数据源配置读取失败", nil)
			return
		}
		snapshot = (providercontrol.DRPCFetcher{}).Fetch(r.Context(), state.StatisticsToken, state.KeysToken, state.RuntimeKey)
	} else {
		state, err := s.providerCenterState(r.Context(), key)
		if err != nil {
			respondError(w, r, http.StatusServiceUnavailable, "PROVIDER_CONFIG_UNAVAILABLE", "数据源配置读取失败", nil)
			return
		}
		if state.PlatformKey != "" {
			snapshot = fetchAnkrPlatform(r.Context(), state)
		} else {
			if s.ankrMgmt == nil {
				respondError(w, r, http.StatusServiceUnavailable, "ANKR_MANAGEMENT_UNAVAILABLE", "Ankr 官方账户连接未配置", nil)
				return
			}
			account, err := s.ankrMgmt.Sync(r.Context())
			if err != nil {
				respondError(w, r, http.StatusBadGateway, "ANKR_MANAGEMENT_SYNC_FAILED", "Ankr 官方账户同步失败", nil)
				return
			}
			usage, keys, detailsErr := s.ankrMgmt.ReadDetails(r.Context())
			snapshot = providercontrol.OfficialSnapshot{Provider: "ankr", ConnectionStatus: "available", BalanceUnit: "USD",
				Keys: []map[string]any{}, Usage: map[string]any{},
				Source: "Ankr Management MCP", SyncedAt: time.Now().UTC()}
			if usage != nil {
				snapshot.Usage = usage
				if requests, ok := usage["total_requests"].(int64); ok {
					snapshot.RequestsDay = &requests
				}
				if credits, ok := usage["total_credits"].(string); ok {
					snapshot.CUDay = credits
				}
			}
			if keys != nil {
				if rows, ok := keys["data"].([]map[string]any); ok {
					snapshot.Keys = rows
				}
			}
			snapshot.Usage["plan"] = account.Plan
			snapshot.Usage["remaining_credits"] = account.RemainingCredits
			snapshot.Usage["account_address"] = account.AccountAddress
			if account.BalanceUSD != nil {
				snapshot.Balance = strconv.FormatFloat(*account.BalanceUSD, 'f', -1, 64)
			}
			if detailsErr != nil {
				snapshot.ConnectionStatus = "partial"
				snapshot.ErrorCode = "usage_or_keys_unavailable"
			}
		}
	}
	if err := s.saveOfficialSnapshot(r.Context(), snapshot); err != nil {
		respondError(w, r, http.StatusInternalServerError, "OFFICIAL_SNAPSHOT_SAVE_FAILED", "官方账户数据保存失败", nil)
		return
	}
	s.s6Audit(r, "admin.provider_center.sync", "admin.provider", "provider", key,
		map[string]any{"connection_status": snapshot.ConnectionStatus, "source": snapshot.Source, "error_code": snapshot.ErrorCode})
	respondOK(w, r, snapshot)
}

func (s *Server) handleProviderCenterAnkrConnect(w http.ResponseWriter, r *http.Request) {
	if s.ankrMgmt == nil {
		respondError(w, r, http.StatusServiceUnavailable, "ANKR_MANAGEMENT_UNAVAILABLE", "Ankr 官方账户连接未配置", nil)
		return
	}
	link, err := s.ankrMgmt.StartOAuth(r.Context())
	if err != nil {
		if errors.Is(err, ankrmgmt.ErrClientMetadataUnsupported) {
			respondError(w, r, http.StatusFailedDependency, "ANKR_CIMD_UNSUPPORTED", "Ankr 授权服务尚未开启客户端元数据文档，官方账户暂时无法连接", nil)
			return
		}
		respondError(w, r, http.StatusBadGateway, "ANKR_OAUTH_UNAVAILABLE", "Ankr 授权链接获取失败", nil)
		return
	}
	respondOK(w, r, map[string]any{"authorization_url": link})
}

func (s *Server) handleProviderCenterTest(w http.ResponseWriter, r *http.Request) {
	key := chi.URLParam(r, "id")
	if !validProviderCenterKey(key) {
		respondError(w, r, http.StatusNotFound, "NOT_FOUND", "数据源不存在", nil)
		return
	}
	state, err := s.providerCenterState(r.Context(), key)
	if err != nil {
		respondError(w, r, http.StatusServiceUnavailable, "PROVIDER_CONFIG_UNAVAILABLE", "数据源配置读取失败", nil)
		return
	}
	if state.RuntimeKey == "" {
		respondError(w, r, http.StatusBadRequest, "RUNTIME_KEY_REQUIRED", "运行时 API Key 未配置", nil)
		return
	}
	endpoint, method := "", ""
	if key == "ankr" {
		method = "eth_chainId"
		endpoint = strings.ReplaceAll(strings.ReplaceAll(state.Endpoints["rpc"], "{chain}", "bsc"), "{key}", url.PathEscape(state.RuntimeKey))
	} else {
		method = "getHealth"
		endpoint = strings.ReplaceAll(state.Endpoints["solana_rpc"], "{key}", url.PathEscape(state.RuntimeKey))
	}
	ctx, cancel := context.WithTimeout(r.Context(), 10*time.Second)
	defer cancel()
	started := time.Now()
	requestBody, _ := json.Marshal(map[string]any{"jsonrpc": "2.0", "id": 1, "method": method, "params": []any{}})
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint, bytes.NewReader(requestBody))
	if err != nil {
		respondError(w, r, http.StatusBadRequest, "INVALID_PROVIDER_ENDPOINT", "测试地址无效", nil)
		return
	}
	req.Header.Set("Content-Type", "application/json")
	client := &http.Client{Timeout: 10 * time.Second, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
	response, callErr := client.Do(req)
	status, ok := 0, false
	if response != nil {
		status = response.StatusCode
		var envelope struct {
			Result json.RawMessage `json:"result"`
			Error  json.RawMessage `json:"error"`
		}
		if status == http.StatusOK && json.NewDecoder(io.LimitReader(response.Body, 1<<20)).Decode(&envelope) == nil {
			ok = len(envelope.Result) > 0 && string(envelope.Result) != "null" && len(envelope.Error) == 0
		}
		response.Body.Close()
	}
	_ = s.deps.Store.InsertUsage(r.Context(), model.UsageRow{Provider: key, Method: method, Dataset: "control_plane", Chain: map[string]string{"ankr": "bsc", "drpc": "solana"}[key],
		RequestTime: started, DurationMs: int(time.Since(started).Milliseconds()), Status: map[bool]string{true: "success", false: "error"}[ok],
		HTTPStatus: status, UsageType: "provider_test", APIType: "rpc", HTTPRequests: 1, LogicalCalls: 1})
	result := map[string]any{"provider": key, "ok": ok, "method": method, "http_status": status,
		"latency_ms": time.Since(started).Milliseconds(), "checked_at": time.Now().UTC()}
	if callErr != nil {
		result["error_class"] = "transport"
	} else if !ok {
		result["error_class"] = "upstream"
	}
	encoded, _ := json.Marshal(result)
	_, _ = s.deps.Store.Pool.Exec(r.Context(), `UPDATE provider_descriptors SET last_test_at=now(),last_test_result=$2::jsonb WHERE provider_key=$1`,
		map[string]string{"ankr": "ankr-multichain", "drpc": "drpc"}[key], string(encoded))
	respondOK(w, r, result)
}
