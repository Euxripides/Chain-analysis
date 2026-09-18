package ankrmgmt

import (
	"bufio"
	"bytes"
	"context"
	"crypto/cipher"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/blockchain-platform/blockchain-platform/internal/config"
	"github.com/blockchain-platform/blockchain-platform/internal/dbtx"
)

var ErrAuthorizationRequired = errors.New("Ankr account authorization is required")

type Service struct {
	pool *dbtx.Pool
	cfg  config.AnkrManagementConfig
	http *http.Client
	aead cipher.AEAD
}

type Account struct {
	Provider          string          `json:"provider"`
	Status            string          `json:"status"`
	Configured        bool            `json:"configured"`
	Connected         bool            `json:"connected"`
	OfficialAvailable bool            `json:"official_available"`
	Stale             bool            `json:"stale"`
	Plan              string          `json:"plan"`
	RemainingCredits  *float64        `json:"remaining_credits"`
	BalanceUSD        *float64        `json:"balance_usd"`
	BalanceKind       string          `json:"balance_kind"`
	AccountAddress    string          `json:"account_address"`
	SyncedAt          *time.Time      `json:"synced_at"`
	TokenExpiresAt    *time.Time      `json:"token_expires_at"`
	Source            string          `json:"source"`
	Reason            string          `json:"reason"`
	ConsoleURL        string          `json:"console_url"`
	RawBalance        json.RawMessage `json:"-"`
}

func New(pool *dbtx.Pool, cfg config.AnkrManagementConfig) (*Service, error) {
	s := &Service{pool: pool, cfg: cfg, http: &http.Client{Timeout: 30 * time.Second}}
	if cfg.Enabled {
		var err error
		s.aead, err = newAEAD(cfg.EncryptionKey)
		if err != nil {
			return nil, err
		}
	}
	return s, nil
}

func (s *Service) configurationError() error {
	if !s.cfg.Enabled {
		return fmt.Errorf("Ankr Management MCP integration is disabled")
	}
	if s.cfg.RedirectURI == "" {
		return fmt.Errorf("ANKR_MANAGEMENT_REDIRECT_URI is not configured")
	}
	if s.aead == nil {
		return fmt.Errorf("Ankr Management OAuth credential encryption is unavailable")
	}
	return nil
}

func (s *Service) FrontendURL() string { return s.cfg.FrontendURL }

func (s *Service) Status(ctx context.Context, refresh bool) Account {
	a := Account{Provider: "ankr", Status: "UNAVAILABLE", BalanceKind: "remaining", Source: "Ankr Management MCP", ConsoleURL: "https://www.ankr.com/rpc/"}
	if err := s.configurationError(); err != nil {
		a.Reason = err.Error()
		return a
	}
	a.Configured = true
	var tokenCiphertext []byte
	var raw []byte
	err := s.pool.QueryRow(ctx, `SELECT token_ciphertext,token_expires_at,account_address,remaining_credits::float8,balance_usd::float8,
		plan,last_synced_at,last_error,raw_balance FROM ankr_management_connection WHERE id=1`).Scan(
		&tokenCiphertext, &a.TokenExpiresAt, &a.AccountAddress, &a.RemainingCredits, &a.BalanceUSD, &a.Plan, &a.SyncedAt, &a.Reason, &raw)
	if err != nil {
		a.Reason = "Ankr Management connection state is unavailable"
		return a
	}
	a.Connected = len(tokenCiphertext) > 0
	if len(raw) > 0 {
		a.RawBalance = raw
	}
	if !a.Connected {
		a.Reason = "尚未授权 Ankr 官方账户"
		return a
	}
	if refresh || a.SyncedAt == nil || time.Since(*a.SyncedAt) > s.cfg.SyncTTL {
		if synced, err := s.Sync(ctx); err == nil {
			return synced
		} else {
			a.Reason = err.Error()
		}
	}
	a.OfficialAvailable = a.RemainingCredits != nil || a.BalanceUSD != nil || len(a.RawBalance) > 0
	a.Stale = a.SyncedAt == nil || time.Since(*a.SyncedAt) > 2*s.cfg.SyncTTL
	if a.OfficialAvailable {
		a.Status = "AVAILABLE"
		if a.Stale || a.Reason != "" {
			a.Status = "STALE"
		}
	}
	return a
}

func (s *Service) Disconnect(ctx context.Context) error {
	// Ankr can invalidate a dynamically registered OAuth client independently of
	// its issued bearer.  Keeping that client ID makes a later "re-authorize"
	// attempt replay the invalid registration and loops back to Ankr's reconnect
	// page.  A disconnect is therefore a complete local removal of the MCP
	// connection, including its dynamic registration and pending PKCE states.
	if _, err := s.pool.Exec(ctx, `DELETE FROM ankr_management_oauth_state`); err != nil {
		return err
	}
	_, err := s.pool.Exec(ctx, `UPDATE ankr_management_connection SET token_ciphertext=NULL,token_nonce=NULL,token_expires_at=NULL,
		client_id='',redirect_uri='',
		account_address='',remaining_credits=NULL,balance_usd=NULL,plan='',raw_balance=NULL,connected_at=NULL,last_synced_at=NULL,
		last_error='',updated_at=now() WHERE id=1`)
	return err
}

func (s *Service) accessToken(ctx context.Context) (string, error) {
	var ciphertext, nonce []byte
	var expiresAt *time.Time
	if err := s.pool.QueryRow(ctx, `SELECT token_ciphertext,token_nonce,token_expires_at FROM ankr_management_connection WHERE id=1`).Scan(&ciphertext, &nonce, &expiresAt); err != nil {
		return "", ErrAuthorizationRequired
	}
	if expiresAt != nil && time.Now().After(expiresAt.Add(-30*time.Second)) {
		return "", ErrAuthorizationRequired
	}
	plaintext, err := open(s.aead, ciphertext, nonce)
	if err != nil {
		return "", ErrAuthorizationRequired
	}
	var token oauthToken
	if json.Unmarshal(plaintext, &token) != nil || token.AccessToken == "" {
		return "", ErrAuthorizationRequired
	}
	return token.AccessToken, nil
}

func (s *Service) Sync(ctx context.Context) (Account, error) {
	token, err := s.accessToken(ctx)
	if err != nil {
		return Account{}, err
	}
	raw, err := s.callTool(ctx, token, "mgmt_get_balance", map[string]any{})
	if err != nil {
		_, _ = s.pool.Exec(ctx, `UPDATE ankr_management_connection SET last_error=$1,updated_at=now() WHERE id=1`, err.Error())
		return Account{}, err
	}
	snapshot := parseBalance(raw)
	encoded, _ := json.Marshal(raw)
	_, err = s.pool.Exec(ctx, `UPDATE ankr_management_connection SET account_address=$1,remaining_credits=$2,balance_usd=$3,
		plan=$4,raw_balance=$5,last_synced_at=now(),last_error='',updated_at=now() WHERE id=1`, snapshot.AccountAddress,
		snapshot.RemainingCredits, snapshot.BalanceUSD, snapshot.Plan, encoded)
	if err != nil {
		return Account{}, err
	}
	return s.Status(ctx, false), nil
}

func (s *Service) doJSON(ctx context.Context, method, endpoint, bearer string, body, out any) error {
	encoded, err := json.Marshal(body)
	if err != nil {
		return err
	}
	req, err := http.NewRequestWithContext(ctx, method, endpoint, bytes.NewReader(encoded))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "application/json")
	if bearer != "" {
		req.Header.Set("Authorization", "Bearer "+bearer)
	}
	resp, err := s.http.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return sanitizedHTTPError("Ankr request", resp)
	}
	return json.NewDecoder(io.LimitReader(resp.Body, 2<<20)).Decode(out)
}

func sanitizedHTTPError(action string, resp *http.Response) error {
	body, _ := io.ReadAll(io.LimitReader(resp.Body, 4096))
	var payload map[string]any
	if json.Unmarshal(body, &payload) == nil {
		if description, ok := payload["error_description"].(string); ok && description != "" {
			return fmt.Errorf("%s: HTTP %d: %s", action, resp.StatusCode, description)
		}
		if message, ok := payload["error"].(string); ok && message != "" {
			return fmt.Errorf("%s: HTTP %d: %s", action, resp.StatusCode, message)
		}
	}
	return fmt.Errorf("%s: HTTP %d", action, resp.StatusCode)
}

type rpcEnvelope struct {
	JSONRPC string          `json:"jsonrpc"`
	ID      int             `json:"id,omitempty"`
	Result  json.RawMessage `json:"result,omitempty"`
	Error   *struct {
		Code    int    `json:"code"`
		Message string `json:"message"`
	} `json:"error,omitempty"`
}

func (s *Service) mcpPost(ctx context.Context, token, session string, payload any) (rpcEnvelope, string, error) {
	encoded, _ := json.Marshal(payload)
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, s.cfg.MCPURL, bytes.NewReader(encoded))
	if err != nil {
		return rpcEnvelope{}, "", err
	}
	req.Header.Set("Authorization", "Bearer "+token)
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "application/json, text/event-stream")
	req.Header.Set("MCP-Protocol-Version", "2025-06-18")
	if session != "" {
		req.Header.Set("Mcp-Session-Id", session)
	}
	resp, err := s.http.Do(req)
	if err != nil {
		return rpcEnvelope{}, "", err
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		if resp.StatusCode == http.StatusUnauthorized {
			return rpcEnvelope{}, "", ErrAuthorizationRequired
		}
		return rpcEnvelope{}, "", sanitizedHTTPError("Ankr Management MCP", resp)
	}
	newSession := resp.Header.Get("Mcp-Session-Id")
	if resp.StatusCode == http.StatusAccepted || resp.ContentLength == 0 {
		return rpcEnvelope{}, newSession, nil
	}
	var envelope rpcEnvelope
	if strings.Contains(resp.Header.Get("Content-Type"), "text/event-stream") {
		scanner := bufio.NewScanner(io.LimitReader(resp.Body, 4<<20))
		for scanner.Scan() {
			line := scanner.Text()
			if strings.HasPrefix(line, "data:") && json.Unmarshal([]byte(strings.TrimSpace(strings.TrimPrefix(line, "data:"))), &envelope) == nil {
				break
			}
		}
		if err := scanner.Err(); err != nil {
			return rpcEnvelope{}, newSession, err
		}
	} else if err := json.NewDecoder(io.LimitReader(resp.Body, 4<<20)).Decode(&envelope); err != nil {
		return rpcEnvelope{}, newSession, err
	}
	if envelope.Error != nil {
		return rpcEnvelope{}, newSession, fmt.Errorf("Ankr Management MCP error %d: %s", envelope.Error.Code, envelope.Error.Message)
	}
	return envelope, newSession, nil
}

func (s *Service) callTool(ctx context.Context, token, name string, arguments map[string]any) (map[string]any, error) {
	initPayload := map[string]any{"jsonrpc": "2.0", "id": 1, "method": "initialize", "params": map[string]any{
		"protocolVersion": "2025-06-18", "capabilities": map[string]any{},
		"clientInfo": map[string]any{"name": "blockchain-platform", "version": "1.6"},
	}}
	_, session, err := s.mcpPost(ctx, token, "", initPayload)
	if err != nil {
		return nil, err
	}
	_, _, err = s.mcpPost(ctx, token, session, map[string]any{"jsonrpc": "2.0", "method": "notifications/initialized"})
	if err != nil {
		return nil, err
	}
	response, _, err := s.mcpPost(ctx, token, session, map[string]any{"jsonrpc": "2.0", "id": 2, "method": "tools/call", "params": map[string]any{"name": name, "arguments": arguments}})
	if err != nil {
		return nil, err
	}
	var toolResult map[string]any
	if err := json.Unmarshal(response.Result, &toolResult); err != nil {
		return nil, fmt.Errorf("decode Ankr tool result: %w", err)
	}
	if failed, _ := toolResult["isError"].(bool); failed {
		return nil, fmt.Errorf("Ankr tool %s returned an error", name)
	}
	if structured, ok := toolResult["structuredContent"].(map[string]any); ok {
		return structured, nil
	}
	if content, ok := toolResult["content"].([]any); ok {
		for _, item := range content {
			block, _ := item.(map[string]any)
			text, _ := block["text"].(string)
			if text == "" {
				continue
			}
			var parsed map[string]any
			if json.Unmarshal([]byte(text), &parsed) == nil {
				return parsed, nil
			}
			return map[string]any{"text": text}, nil
		}
	}
	return toolResult, nil
}

type balanceSnapshot struct {
	RemainingCredits *float64
	BalanceUSD       *float64
	Plan             string
	AccountAddress   string
}

func parseBalance(raw map[string]any) balanceSnapshot {
	var out balanceSnapshot
	out.RemainingCredits = findNumber(raw, "remainingcredits", "apicredits", "creditbalance", "balancecredits", "credits")
	out.BalanceUSD = findNumber(raw, "balanceusd", "usdbalance", "usdvalue")
	out.Plan = findString(raw, "plan", "level", "tier")
	out.AccountAddress = findString(raw, "accountaddress", "account", "address")
	if text, _ := raw["text"].(string); text != "" {
		// Management MCP 当前文本格式把账户余额和 API credits 分行返回：
		// "USD: 19.26" 是美元余额，"credit USD: 192608400" 才是 API credits。
		// 必须先按完整行标签解析，不能把相邻的 ANKR 数量误认成 credits。
		if n := labeledNumber(text, `credit[ \t]+USD`); n != nil {
			out.RemainingCredits = n
		}
		if n := labeledNumber(text, `USD`); n != nil {
			out.BalanceUSD = n
		}
		if out.Plan == "" {
			re := regexp.MustCompile(`(?im)^[ \t]*level[ \t]*:[ \t]*([^\r\n]+)`)
			if match := re.FindStringSubmatch(text); len(match) == 2 {
				out.Plan = strings.TrimSpace(match[1])
			}
		}
		if out.RemainingCredits == nil {
			re := regexp.MustCompile(`(?i)([0-9][0-9,]*(?:\.[0-9]+)?)[ \t]*(?:API[ \t]*)?credits?`)
			if m := re.FindStringSubmatch(text); len(m) == 2 {
				if n, err := strconv.ParseFloat(strings.ReplaceAll(m[1], ",", ""), 64); err == nil {
					out.RemainingCredits = &n
				}
			}
		}
		if out.BalanceUSD == nil {
			re := regexp.MustCompile(`\$\s*([0-9][0-9,]*(?:\.[0-9]+)?)`)
			if m := re.FindStringSubmatch(text); len(m) == 2 {
				if n, err := strconv.ParseFloat(strings.ReplaceAll(m[1], ",", ""), 64); err == nil {
					out.BalanceUSD = &n
				}
			}
		}
	}
	return out
}

func labeledNumber(text, labelPattern string) *float64 {
	re := regexp.MustCompile(`(?im)^[ \t]*` + labelPattern + `[ \t]*:[ \t]*([0-9][0-9,]*(?:\.[0-9]+)?)`)
	match := re.FindStringSubmatch(text)
	if len(match) != 2 {
		return nil
	}
	number, err := strconv.ParseFloat(strings.ReplaceAll(match[1], ",", ""), 64)
	if err != nil {
		return nil
	}
	return &number
}

func normalizedKey(v string) string {
	return strings.NewReplacer("_", "", "-", "", " ", "").Replace(strings.ToLower(v))
}

func findNumber(value any, keys ...string) *float64 {
	switch v := value.(type) {
	case map[string]any:
		for key, item := range v {
			for _, wanted := range keys {
				if normalizedKey(key) == wanted {
					switch number := item.(type) {
					case float64:
						return &number
					case string:
						if n, err := strconv.ParseFloat(strings.ReplaceAll(number, ",", ""), 64); err == nil {
							return &n
						}
					}
				}
			}
		}
		for _, item := range v {
			if n := findNumber(item, keys...); n != nil {
				return n
			}
		}
	case []any:
		for _, item := range v {
			if n := findNumber(item, keys...); n != nil {
				return n
			}
		}
	}
	return nil
}

func findString(value any, keys ...string) string {
	switch v := value.(type) {
	case map[string]any:
		for key, item := range v {
			for _, wanted := range keys {
				if normalizedKey(key) == wanted {
					if text, ok := item.(string); ok {
						return text
					}
				}
			}
		}
		for _, item := range v {
			if text := findString(item, keys...); text != "" {
				return text
			}
		}
	case []any:
		for _, item := range v {
			if text := findString(item, keys...); text != "" {
				return text
			}
		}
	}
	return ""
}
