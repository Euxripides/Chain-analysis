package providercontrol

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math"
	"math/big"
	"net/http"
	"strings"
	"time"
)

type OfficialSnapshot struct {
	Provider         string           `json:"provider"`
	ConnectionStatus string           `json:"connection_status"`
	Balance          string           `json:"balance,omitempty"`
	BalanceUnit      string           `json:"balance_unit,omitempty"`
	RequestsDay      *int64           `json:"requests_day"`
	CUDay            string           `json:"cu_day,omitempty"`
	Keys             []map[string]any `json:"keys"`
	Usage            map[string]any   `json:"usage"`
	Source           string           `json:"source"`
	SyncedAt         time.Time        `json:"synced_at"`
	ErrorCode        string           `json:"error_code,omitempty"`
}

type DRPCFetcher struct {
	Client        *http.Client
	StatisticsURL string
	KeysURL       string
}

func (f DRPCFetcher) client() *http.Client {
	if f.Client != nil {
		return f.Client
	}
	return &http.Client{Timeout: 15 * time.Second, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
}

func (f DRPCFetcher) get(ctx context.Context, endpoint, token string) (any, error) {
	if strings.TrimSpace(token) == "" {
		return nil, errors.New("management token is not configured")
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Authorization", "Bearer "+token)
	req.Header.Set("Accept", "application/json")
	resp, err := f.client().Do(req)
	if err != nil {
		return nil, errors.New("dRPC management request failed")
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("dRPC management HTTP %d", resp.StatusCode)
	}
	var payload any
	decoder := json.NewDecoder(io.LimitReader(resp.Body, 2<<20))
	decoder.UseNumber()
	if err := decoder.Decode(&payload); err != nil {
		return nil, errors.New("dRPC management response is invalid")
	}
	return payload, nil
}

func (f DRPCFetcher) Fetch(ctx context.Context, statisticsToken, keysToken, runtimeKey string) OfficialSnapshot {
	statsURL := f.StatisticsURL
	if statsURL == "" {
		statsURL = "https://drpc.org/api/metrics/json?range=day"
	}
	keysURL := f.KeysURL
	if keysURL == "" {
		keysURL = "https://drpc.org/api/team/keys/list"
	}
	snapshot := OfficialSnapshot{Provider: "drpc", ConnectionStatus: "unavailable", BalanceUnit: "provider_unit",
		Keys: []map[string]any{}, Usage: map[string]any{}, Source: "dRPC Statistics API + Keys API", SyncedAt: time.Now().UTC()}
	stats, statsErr := f.get(ctx, statsURL, statisticsToken)
	if statsErr == nil {
		if obj, ok := stats.(map[string]any); ok {
			balance, balanceOK := obj["balance"].(json.Number)
			rows, rowsOK := obj["data"].([]any)
			if !balanceOK || !rowsOK {
				statsErr = errors.New("dRPC statistics response is invalid")
			} else {
				snapshot.Balance = balance.String()
				cu := new(big.Rat)
				var requests int64
				usageRows := make([]map[string]any, 0, len(rows))
				validRows := true
				for _, row := range rows {
					item, ok := row.(map[string]any)
					countNumber, countOK := item["request_count"].(json.Number)
					cuNumber, cuOK := item["cu"].(json.Number)
					if !ok || !countOK || !cuOK {
						validRows = false
						break
					}
					count, countErr := countNumber.Int64()
					part, validCU := new(big.Rat).SetString(cuNumber.String())
					if countErr != nil || count < 0 || requests > math.MaxInt64-count || !validCU {
						validRows = false
						break
					}
					requests += count
					cu.Add(cu, part)
					clean := RedactManagement(item).(map[string]any)
					for _, field := range []string{"request_count", "cu"} {
						if number, ok := clean[field].(json.Number); ok {
							clean[field] = number.String()
						}
					}
					usageRows = append(usageRows, clean)
				}
				if validRows {
					snapshot.CUDay = exactDecimal(cu)
					snapshot.RequestsDay = &requests
					snapshot.Usage["range"] = "day"
					snapshot.Usage["data"] = usageRows
					snapshot.ConnectionStatus = "available"
				} else {
					statsErr = errors.New("dRPC statistics response is invalid")
				}
			}
		} else {
			statsErr = errors.New("dRPC statistics response is invalid")
		}
	}
	keys, keysErr := f.get(ctx, keysURL, keysToken)
	if keysErr == nil {
		var rows []any
		switch v := keys.(type) {
		case []any:
			rows = v
		case map[string]any:
			rows, _ = v["data"].([]any)
			if rows == nil {
				rows, _ = v["keys"].([]any)
			}
		}
		if rows == nil {
			keysErr = errors.New("dRPC keys response is invalid")
		}
		fingerprint := keyFingerprint(runtimeKey)
		for _, row := range rows {
			item, ok := row.(map[string]any)
			if !ok {
				keysErr = errors.New("dRPC keys response is invalid")
				break
			}
			key, _ := item["key"].(map[string]any)
			if key == nil {
				keysErr = errors.New("dRPC keys response is invalid")
				break
			}
			apiKey, _ := key["api_key"].(string)
			entry := map[string]any{"name": key["name"], "key_id": key["key_id"], "status": key["status"],
				"cu_day_limit": exactJSONNumber(key["cu_day_limit"]), "cu_spent_today": exactJSONNumber(item["cu_spent_today"]), "rate_limit": exactJSONNumber(key["rate_limit"]),
				"updated_at": key["updated_at"], "selected": apiKey != "" && keyFingerprint(apiKey) == fingerprint}
			snapshot.Keys = append(snapshot.Keys, entry)
		}
	}
	if statsErr != nil || keysErr != nil {
		if statsErr == nil || keysErr == nil {
			snapshot.ConnectionStatus = "partial"
		}
		switch {
		case statsErr != nil && keysErr != nil:
			snapshot.ErrorCode = "statistics_and_keys_unavailable"
		case statsErr != nil:
			snapshot.ErrorCode = "statistics_unavailable"
		default:
			snapshot.ErrorCode = "keys_unavailable"
		}
	}
	return snapshot
}

func exactJSONNumber(value any) any {
	if number, ok := value.(json.Number); ok {
		return number.String()
	}
	return value
}

func keyFingerprint(key string) string {
	if key == "" {
		return ""
	}
	sum := sha256.Sum256([]byte(key))
	return hex.EncodeToString(sum[:])
}

// RedactManagement strips credential fields from any provider-supplied object
// before it can be persisted or returned to the browser.
func RedactManagement(value any) any {
	switch v := value.(type) {
	case map[string]any:
		out := make(map[string]any, len(v))
		for key, item := range v {
			normalized := strings.ToLower(strings.ReplaceAll(key, "-", "_"))
			if normalized == "key" {
				if _, raw := item.(string); raw {
					continue
				}
			}
			if normalized == "api_key" || normalized == "client_key" || normalized == "token" || normalized == "secret" ||
				strings.HasSuffix(normalized, "_token") || strings.Contains(normalized, "secret") ||
				normalized == "authorization" || normalized == "url" || normalized == "endpoint" {
				continue
			}
			out[key] = RedactManagement(item)
		}
		return out
	case []any:
		out := make([]any, len(v))
		for i, item := range v {
			out[i] = RedactManagement(item)
		}
		return out
	default:
		return value
	}
}

func exactDecimal(value *big.Rat) string {
	if value == nil {
		return "0"
	}
	denominator := new(big.Int).Set(value.Denom())
	twos, fives := 0, 0
	for new(big.Int).Mod(denominator, big.NewInt(2)).Sign() == 0 {
		denominator.Div(denominator, big.NewInt(2))
		twos++
	}
	for new(big.Int).Mod(denominator, big.NewInt(5)).Sign() == 0 {
		denominator.Div(denominator, big.NewInt(5))
		fives++
	}
	if denominator.Cmp(big.NewInt(1)) != 0 {
		return value.RatString()
	}
	scale := max(twos, fives)
	if scale == 0 {
		return value.Num().String()
	}
	numerator := new(big.Int).Set(value.Num())
	numerator.Mul(numerator, new(big.Int).Exp(big.NewInt(2), big.NewInt(int64(scale-twos)), nil))
	numerator.Mul(numerator, new(big.Int).Exp(big.NewInt(5), big.NewInt(int64(scale-fives)), nil))
	sign := ""
	if numerator.Sign() < 0 {
		sign = "-"
		numerator.Abs(numerator)
	}
	digits := numerator.String()
	if len(digits) <= scale {
		digits = strings.Repeat("0", scale-len(digits)+1) + digits
	}
	whole, fraction := digits[:len(digits)-scale], strings.TrimRight(digits[len(digits)-scale:], "0")
	if fraction == "" {
		return sign + whole
	}
	return sign + whole + "." + fraction
}
