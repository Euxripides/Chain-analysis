package providercontrol

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math/big"
	"net/http"
	"net/url"
	"sort"
	"strings"
	"time"
)

const ankrPlatformBaseURL = "https://mainnet.multirpc.ankr.com"

// AnkrPlatformFetcher uses only the account read endpoints described by Ankr's
// Platform Swagger specification. BaseURL and Client are injectable for tests.
type AnkrPlatformFetcher struct {
	BaseURL string
	Client  *http.Client
}

func (f AnkrPlatformFetcher) get(ctx context.Context, path, key string) (any, int, error) {
	base := f.BaseURL
	if base == "" {
		base = ankrPlatformBaseURL
	}
	uri, err := url.Parse(strings.TrimRight(base, "/") + path)
	if err != nil || (f.BaseURL == "" && (uri.Scheme != "https" || uri.Hostname() != "mainnet.multirpc.ankr.com")) {
		return nil, 0, errors.New("invalid Ankr Platform API URL")
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, uri.String(), nil)
	if err != nil {
		return nil, 0, errors.New("invalid Ankr Platform request")
	}
	req.Header.Set("Authorization", "Bearer "+key)
	req.Header.Set("Accept", "application/json")
	client := f.Client
	if client == nil {
		client = &http.Client{Timeout: 15 * time.Second, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
	}
	resp, err := client.Do(req)
	if err != nil {
		return nil, 0, errors.New("Ankr Platform request failed")
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, resp.StatusCode, fmt.Errorf("Ankr Platform HTTP %d", resp.StatusCode)
	}
	var payload any
	decoder := json.NewDecoder(io.LimitReader(resp.Body, 2<<20))
	decoder.UseNumber()
	if err := decoder.Decode(&payload); err != nil {
		return nil, resp.StatusCode, errors.New("Ankr Platform response is invalid")
	}
	return payload, resp.StatusCode, nil
}

func (f AnkrPlatformFetcher) Fetch(ctx context.Context, key string) OfficialSnapshot {
	snapshot := OfficialSnapshot{Provider: "ankr", ConnectionStatus: "unavailable", BalanceUnit: "USD",
		Keys: []map[string]any{}, Usage: map[string]any{}, Source: "Ankr Platform Swagger", SyncedAt: time.Now().UTC()}
	if strings.TrimSpace(key) == "" {
		snapshot.ErrorCode = "platform_key_missing"
		return snapshot
	}
	balanceRaw, status, err := f.get(ctx, "/api/v1/auth/balance", key)
	if err != nil {
		if status == http.StatusUnauthorized {
			snapshot.ConnectionStatus, snapshot.ErrorCode = "expired", "platform_key_rejected"
		} else if status == http.StatusForbidden {
			snapshot.ErrorCode = "platform_access_denied"
		} else {
			snapshot.ErrorCode = "platform_balance_unavailable"
		}
		return snapshot
	}
	balance, ok := balanceRaw.(map[string]any)
	if !ok {
		snapshot.ErrorCode = "platform_balance_invalid"
		return snapshot
	}
	if amount, ok := balance["balance_usd"].(string); ok && amount != "" {
		snapshot.Balance = amount
	} else if amount, ok := balance["balance_credit_usd"].(string); ok && amount != "" {
		snapshot.Balance = amount
	} else {
		snapshot.ErrorCode = "platform_balance_invalid"
		return snapshot
	}
	snapshot.ConnectionStatus = "available"
	snapshot.Usage["balance_details"] = RedactManagement(balance)
	statsRaw, _, statsErr := f.get(ctx, "/api/v1/auth/stats?intervalType=h24", key)
	if statsErr == nil {
		stats, ok := statsRaw.(map[string]any)
		if !ok {
			statsErr = errors.New("invalid Ankr statistics")
		} else {
			count, ok := stats["total_requests"].(json.Number)
			if !ok {
				statsErr = errors.New("Ankr request count is missing")
			} else if requests, parseErr := count.Int64(); parseErr != nil || requests < 0 {
				statsErr = errors.New("invalid Ankr request count")
			} else {
				snapshot.RequestsDay = &requests
				snapshot.Usage["range"] = "h24"
				snapshot.Usage["total_requests"] = count.String()
				snapshot.Usage["data"] = ankrPlatformUsageRows(stats["stats"])
				if cost, ok := ankrPlatformTotalCost(stats["stats"]); ok {
					snapshot.CUDay = cost
				}
			}
		}
	}
	keysRaw, _, keysErr := f.get(ctx, "/api/v1/auth/jwt/all", key)
	if keysErr == nil {
		items, ok := keysRaw.([]any)
		if !ok {
			keysErr = errors.New("invalid Ankr key list")
		} else {
			for _, raw := range items {
				row, ok := raw.(map[string]any)
				if !ok {
					keysErr = errors.New("invalid Ankr key metadata")
					break
				}
				// jwt_data contains a usable RPC secret. Never persist or return it.
				clean := map[string]any{}
				for _, name := range []string{"index", "name", "description", "config"} {
					if value, exists := row[name]; exists {
						clean[name] = value
					}
				}
				if encrypted, ok := row["is_encrypted"].(bool); ok {
					clean["encrypted"] = encrypted
				}
				snapshot.Keys = append(snapshot.Keys, clean)
			}
		}
	}
	if statsErr != nil || keysErr != nil {
		snapshot.ConnectionStatus, snapshot.ErrorCode = "partial", "usage_or_keys_unavailable"
		if statsErr != nil && keysErr == nil {
			snapshot.ErrorCode = "usage_unavailable"
		} else if keysErr != nil && statsErr == nil {
			snapshot.ErrorCode = "keys_unavailable"
		}
	}
	return snapshot
}

func ankrPlatformUsageRows(raw any) []map[string]any {
	stats, ok := raw.(map[string]any)
	if !ok {
		return []map[string]any{}
	}
	chains := make([]string, 0, len(stats))
	for chain := range stats {
		chains = append(chains, chain)
	}
	sort.Strings(chains)
	rows := make([]map[string]any, 0)
	for _, chain := range chains {
		stat, ok := stats[chain].(map[string]any)
		if !ok {
			continue
		}
		counts, ok := stat["counts"].(map[string]any)
		if !ok {
			continue
		}
		groups := make([]string, 0, len(counts))
		for group := range counts {
			groups = append(groups, group)
		}
		sort.Strings(groups)
		for _, group := range groups {
			item, ok := counts[group].(map[string]any)
			if !ok {
				continue
			}
			requests, ok := item["top_requests"].([]any)
			if !ok {
				continue
			}
			for _, rawRequest := range requests {
				method, ok := rawRequest.(map[string]any)
				if !ok {
					continue
				}
				row := map[string]any{"network": chain, "group": group, "method": method["method"]}
				for output, input := range map[string]string{"request_count": "count", "credits": "total_cost"} {
					if number, ok := method[input].(json.Number); ok {
						row[output] = number.String()
					}
				}
				rows = append(rows, row)
			}
		}
	}
	return rows
}

func ankrPlatformTotalCost(raw any) (string, bool) {
	stats, ok := raw.(map[string]any)
	if !ok {
		return "", false
	}
	total := new(big.Int)
	for _, rawStat := range stats {
		stat, ok := rawStat.(map[string]any)
		if !ok {
			return "", false
		}
		counts, ok := stat["total"].(map[string]any)
		if !ok {
			return "", false
		}
		value, ok := counts["total_cost"].(json.Number)
		if !ok {
			return "", false
		}
		part, ok := new(big.Int).SetString(value.String(), 10)
		if !ok || part.Sign() < 0 {
			return "", false
		}
		total.Add(total, part)
	}
	return total.String(), true
}
