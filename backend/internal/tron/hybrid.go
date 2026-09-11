package tron

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"sync"
	"time"

	"github.com/blockchain-platform/blockchain-platform/internal/observability"
	"github.com/blockchain-platform/blockchain-platform/internal/provider"
)

const hybridCursorPrefix = "tron:hybrid:"

// RangeResolver is implemented by AnkrClient. It keeps automatic time/block
// resolution inside the provider layer and out of the UI/API contract.
type RangeResolver interface {
	LatestBlock(context.Context) (uint64, error)
	ResolveBlockByTimestamp(context.Context, time.Time) (uint64, error)
}

type HybridOptions struct {
	FallbackEnabled bool
	MaxWindows      uint64
	MaxWindowSpan   uint64
}

// FallbackTooExpensiveError is a safety decision, not a failed download. The
// scheduler pauses the task so it can resume on TronGrid recovery.
type FallbackTooExpensiveError struct {
	Blocks, Windows, EstimatedRequests uint64
}

func (e *FallbackTooExpensiveError) Error() string {
	return fmt.Sprintf("TRON fallback paused: %d blocks require %d windows and about %d requests", e.Blocks, e.Windows, e.EstimatedRequests)
}

func IsFallbackTooExpensive(err error) bool {
	var target *FallbackTooExpensiveError
	return errors.As(err, &target)
}

type hybridCursor struct {
	Provider     string `json:"provider"`
	Token        string `json:"token"`
	CoveredStart uint64 `json:"covered_start,omitempty"`
	CoveredEnd   uint64 `json:"covered_end,omitempty"`
}

type HybridCheckpointInfo struct {
	ActiveProvider             string
	CoveredStart, CoveredEnd   uint64
	FallbackStart, FallbackEnd uint64
}

func InspectHybridCursor(token string) (HybridCheckpointInfo, bool) {
	cursor, err := decodeHybridCursor(token)
	if err != nil || !strings.HasPrefix(token, hybridCursorPrefix) {
		return HybridCheckpointInfo{}, false
	}
	info := HybridCheckpointInfo{ActiveProvider: cursor.Provider, CoveredStart: cursor.CoveredStart, CoveredEnd: cursor.CoveredEnd}
	if cursor.Provider == "ankr" {
		if fallback, err := decodeAnkrCursor(cursor.Token); err == nil {
			info.FallbackStart, info.FallbackEnd = fallback.NextBlock, fallback.EndBlock
		}
	}
	return info, true
}

// HybridProvider routes full/time TRC20 history through TronGrid and exact
// block queries through Ankr. An exhausted primary retry budget may switch the
// same provider-neutral shard to Ankr without changing its identity.
type HybridProvider struct {
	Primary  provider.Provider
	Fallback provider.Provider
	Resolver RangeResolver
	Options  HybridOptions

	mu               sync.Mutex
	primaryState     provider.HealthState
	primaryLastError string
	primaryOpenedAt  time.Time
	halfOpenProbe    bool
}

func (h *HybridProvider) TRC20ActivityBounds(ctx context.Context, address string) (time.Time, time.Time, error) {
	if resolver, ok := h.Primary.(interface {
		TRC20ActivityBounds(context.Context, string) (time.Time, time.Time, error)
	}); ok {
		return resolver.TRC20ActivityBounds(ctx, address)
	}
	return time.Time{}, time.Time{}, &provider.Error{Provider: h.Name(), Class: provider.ClassUnsupported, Message: "TRC20 activity bounds unsupported"}
}

func (h *HybridProvider) TRC20ActivityProfile(ctx context.Context, address string) (provider.TRC20ActivityProfile, error) {
	if resolver, ok := h.Primary.(interface {
		TRC20ActivityProfile(context.Context, string) (provider.TRC20ActivityProfile, error)
	}); ok {
		return resolver.TRC20ActivityProfile(ctx, address)
	}
	return provider.TRC20ActivityProfile{}, &provider.Error{Provider: h.Name(), Class: provider.ClassUnsupported, Message: "TRC20 activity profile unsupported"}
}

func (h *HybridProvider) Name() string { return "tron-trc20-hybrid" }

func (h *HybridProvider) Capabilities() provider.Capabilities {
	return provider.Capabilities{Name: h.Name(), Datasets: []string{"token_transfers"}, MaxPageSize: 200,
		SupportsTimeRange: map[string]bool{"token_transfers": true}}
}

func (h *HybridProvider) Health(ctx context.Context) provider.HealthStatus {
	h.mu.Lock()
	state, message := h.primaryState, h.primaryLastError
	h.mu.Unlock()
	if state == "" {
		state = provider.HealthNormal
	}
	return provider.HealthStatus{Provider: h.Name(), State: state, Error: message, LastCheck: time.Now()}
}

func (h *HybridProvider) Fetch(ctx context.Context, req provider.FetchRequest) (provider.FetchResult, error) {
	if req.Chain != "tron" || req.Dataset != "token_transfers" {
		return provider.FetchResult{}, &provider.Error{Provider: h.Name(), Class: provider.ClassUnsupported, Message: "hybrid provider only supports TRON token_transfers"}
	}
	mode := strings.ToLower(strings.TrimSpace(req.QueryMode))
	if mode == "" {
		if req.FromTime != nil || req.ToTime != nil {
			mode = "time"
		} else {
			mode = "block"
		}
	}
	cursor, err := decodeHybridCursor(req.PageToken)
	if err != nil {
		return provider.FetchResult{}, &provider.Error{Provider: h.Name(), Class: provider.ClassInvalidRequest, Message: err.Error()}
	}
	if mode == "block" || cursor.Provider == "ankr" || h.primaryUnavailable() {
		return h.fetchFallback(ctx, req, mode, cursor)
	}

	primaryReq := req
	primaryReq.PageToken = cursor.Token
	observability.TronTRC20PrimaryRequests.Inc()
	result, fetchErr := h.Primary.Fetch(ctx, primaryReq)
	if fetchErr == nil {
		h.setPrimaryHealth(provider.HealthNormal, "")
		result.Rows, result.HasMore = trimPrimaryResult(result.Rows, result.HasMore, req, mode)
		observability.TronTRC20PrimaryRows.Add(float64(len(result.Rows)))
		cursor.Provider, cursor.Token = "trongrid", result.NextPageToken
		updateCovered(&cursor, result.Rows)
		if result.HasMore {
			result.NextPageToken = encodeHybridCursor(cursor)
		} else {
			result.NextPageToken = ""
		}
		return result, nil
	}
	if !h.Options.FallbackEnabled || !isProviderFailure(fetchErr) {
		return provider.FetchResult{}, fetchErr
	}
	// Client.Fetch has already completed its configured retry/backoff budget.
	// Opening here therefore never converts a single timeout into a fallback.
	h.setPrimaryHealth(provider.HealthOpen, fetchErr.Error())
	observability.TronTRC20ProviderSwitches.Inc()
	return h.fetchFallback(ctx, req, mode, cursor)
}

func trimPrimaryResult(rows []json.RawMessage, hasMore bool, req provider.FetchRequest, mode string) ([]json.RawMessage, bool) {
	filtered := make([]json.RawMessage, 0, len(rows))
	oldest := uint64(0)
	for _, raw := range rows {
		var row map[string]any
		if json.Unmarshal(raw, &row) != nil {
			continue
		}
		block := uintValue(row["block_number"])
		if oldest == 0 || (block > 0 && block < oldest) {
			oldest = block
		}
		if mode != "block" && req.FromBlock != nil && req.ToBlock != nil && *req.ToBlock > 0 && (block < *req.FromBlock || block > *req.ToBlock) {
			continue
		}
		if mode == "time" {
			ts := int64(uintValue(row["block_timestamp"]))
			if req.FromTime != nil && ts < req.FromTime.UnixMilli() {
				continue
			}
			if req.ToTime != nil && ts > req.ToTime.UnixMilli() {
				continue
			}
		}
		filtered = append(filtered, raw)
	}
	if req.FromBlock != nil && *req.FromBlock > 0 && oldest > 0 && oldest <= *req.FromBlock {
		hasMore = false
	}
	return filtered, hasMore
}

func (h *HybridProvider) fetchFallback(ctx context.Context, req provider.FetchRequest, mode string, cursor hybridCursor) (provider.FetchResult, error) {
	if h.Fallback == nil || h.Resolver == nil {
		return provider.FetchResult{}, &provider.Error{Provider: h.Name(), Class: provider.ClassUnsupported, Message: "TRON TRC20 fallback is unavailable"}
	}
	fallbackReq := req
	if cursor.Provider == "ankr" {
		fallbackReq.PageToken = cursor.Token
	} else {
		fallbackReq.PageToken = ""
		start, end, err := h.resolveFallbackRange(ctx, req, mode, cursor)
		if err != nil {
			return provider.FetchResult{}, err
		}
		fallbackReq.FromBlock, fallbackReq.ToBlock = &start, &end
		fallbackReq.FromTime, fallbackReq.ToTime = nil, nil
	}
	result, err := h.Fallback.Fetch(ctx, fallbackReq)
	if err != nil {
		return provider.FetchResult{}, err
	}
	observability.TronTRC20FallbackWindows.Inc()
	observability.TronTRC20FallbackRequests.Add(2)
	observability.TronTRC20FallbackRows.Add(float64(len(result.Rows)))
	cursor.Provider, cursor.Token = "ankr", result.NextPageToken
	updateCovered(&cursor, result.Rows)
	if result.HasMore {
		result.NextPageToken = encodeHybridCursor(cursor)
	}
	return result, nil
}

func (h *HybridProvider) resolveFallbackRange(ctx context.Context, req provider.FetchRequest, mode string, cursor hybridCursor) (uint64, uint64, error) {
	start, end := uint64(0), uint64(0)
	var err error
	if mode == "time" {
		if req.FromTime != nil {
			start, err = h.Resolver.ResolveBlockByTimestamp(ctx, *req.FromTime)
		}
		if err == nil && req.ToTime != nil {
			end, err = h.Resolver.ResolveBlockByTimestamp(ctx, *req.ToTime)
		}
		if err != nil {
			return 0, 0, err
		}
	} else {
		if req.FromBlock != nil {
			start = *req.FromBlock
		}
		if req.ToBlock != nil {
			end = *req.ToBlock
		} else {
			end, err = h.Resolver.LatestBlock(ctx)
		}
		if err != nil {
			return 0, 0, err
		}
	}
	// TronGrid is newest-first. After partial primary progress, Ankr only owns
	// the older missing interval; overlap is left to the business unique key.
	if cursor.CoveredStart > start && cursor.CoveredStart <= end+1 {
		end = cursor.CoveredStart - 1
	}
	if start > end {
		return 0, 0, &provider.Error{Provider: h.Name(), Class: provider.ClassInvalidRequest, Message: "TRON fallback range is already covered"}
	}
	span := h.Options.MaxWindowSpan
	if span == 0 || span > 5000 {
		span = 5000
	}
	blocks := end - start + 1
	windows := (blocks + span - 1) / span
	if h.Options.MaxWindows > 0 && windows > h.Options.MaxWindows {
		return 0, 0, &FallbackTooExpensiveError{Blocks: blocks, Windows: windows, EstimatedRequests: windows * 2}
	}
	return start, end, nil
}

func (h *HybridProvider) primaryUnavailable() bool {
	h.mu.Lock()
	defer h.mu.Unlock()
	if h.primaryState == provider.HealthOpen {
		if time.Since(h.primaryOpenedAt) < 30*time.Second {
			return true
		}
		h.primaryState, h.halfOpenProbe = provider.HealthHalfOpen, true
		return false
	}
	return h.primaryState == provider.HealthHalfOpen && h.halfOpenProbe
}
func (h *HybridProvider) setPrimaryHealth(state provider.HealthState, message string) {
	h.mu.Lock()
	h.primaryState, h.primaryLastError, h.halfOpenProbe = state, message, false
	if state == provider.HealthOpen {
		h.primaryOpenedAt = time.Now()
	}
	h.mu.Unlock()
}

func isProviderFailure(err error) bool {
	var pe *provider.Error
	if !errors.As(err, &pe) {
		return false
	}
	switch pe.Class {
	case provider.ClassRateLimit, provider.ClassRequestTimeout, provider.ClassConnectTimeout,
		provider.ClassHeaderTimeout, provider.ClassBodyIdleTimeout, provider.ClassOverallTimeout,
		provider.ClassBodyTruncated, provider.ClassUpstream, provider.ClassQuota:
		return true
	default:
		return false
	}
}

func updateCovered(cursor *hybridCursor, rows []json.RawMessage) {
	for _, raw := range rows {
		var row map[string]any
		if json.Unmarshal(raw, &row) != nil {
			continue
		}
		block := uintValue(row["block_number"])
		if block == 0 {
			block = uintValue(row["blockNumber"])
		}
		if block == 0 {
			continue
		}
		if cursor.CoveredStart == 0 || block < cursor.CoveredStart {
			cursor.CoveredStart = block
		}
		if block > cursor.CoveredEnd {
			cursor.CoveredEnd = block
		}
	}
}

func encodeHybridCursor(cursor hybridCursor) string {
	raw, _ := json.Marshal(cursor)
	return hybridCursorPrefix + base64.RawURLEncoding.EncodeToString(raw)
}

func decodeHybridCursor(token string) (hybridCursor, error) {
	if token == "" {
		return hybridCursor{Provider: "trongrid"}, nil
	}
	if !strings.HasPrefix(token, hybridCursorPrefix) {
		return hybridCursor{}, errors.New("invalid TRON hybrid checkpoint cursor")
	}
	raw, err := base64.RawURLEncoding.DecodeString(strings.TrimPrefix(token, hybridCursorPrefix))
	if err != nil {
		return hybridCursor{}, errors.New("invalid TRON hybrid checkpoint cursor")
	}
	var cursor hybridCursor
	if json.Unmarshal(raw, &cursor) != nil || (cursor.Provider != "trongrid" && cursor.Provider != "ankr") {
		return hybridCursor{}, errors.New("invalid TRON hybrid checkpoint cursor")
	}
	return cursor, nil
}
