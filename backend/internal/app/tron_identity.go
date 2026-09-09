package app

import (
	"context"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"log/slog"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/blockchain-platform/blockchain-platform/internal/clickhouse"
	"github.com/blockchain-platform/blockchain-platform/internal/provider"
	"github.com/blockchain-platform/blockchain-platform/internal/tron"
	"golang.org/x/sync/singleflight"
)

// localTRC20IdentityResolver turns repeat and overlapping address downloads
// into an index lookup. Consensus identity is immutable, so an existing exact
// (tx, token, from, to, amount) match can safely supply block/logIndex while
// only cold misses continue to the node resolver.
type localTRC20IdentityResolver struct {
	ch     *clickhouse.Conn
	remote tron.TRC20IdentityResolver
	log    *slog.Logger
	mu     sync.Mutex
	cache  map[string]map[string][]localTRC20Candidate
	order  []string
	group  singleflight.Group
}

type localTRC20Candidate struct {
	logIndex    uint32
	blockNumber uint64
	provider    string
}

func newTRC20IdentityResolver(ch *clickhouse.Conn, p provider.Provider, log *slog.Logger, localEnabled bool) tron.TRC20IdentityResolver {
	remote, _ := p.(tron.TRC20IdentityResolver)
	if remote == nil || ch == nil || !localEnabled {
		return remote
	}
	return &localTRC20IdentityResolver{ch: ch, remote: remote, log: log, cache: make(map[string]map[string][]localTRC20Candidate)}
}

func (r *localTRC20IdentityResolver) EnrichTRC20Identity(ctx context.Context, req provider.FetchRequest, rows []json.RawMessage) ([]json.RawMessage, error) {
	resolved, misses, missIndexes, err := r.resolveLocal(ctx, req.Address, rows)
	if err != nil {
		if r.log != nil {
			r.log.Warn("TRC20 local identity lookup failed; using node resolver", "err", err)
		}
		return r.remote.EnrichTRC20Identity(ctx, req, rows)
	}
	if len(misses) == 0 {
		return resolved, nil
	}
	remoteRows, err := r.remote.EnrichTRC20Identity(ctx, req, misses)
	if err != nil {
		return nil, err
	}
	for i, index := range missIndexes {
		resolved[index] = remoteRows[i]
	}
	return resolved, nil
}

func (r *localTRC20IdentityResolver) EnrichTRC20IdentityBatch(ctx context.Context, req provider.FetchRequest, pages [][]json.RawMessage) ([][]json.RawMessage, error) {
	lengths := make([]int, len(pages))
	flat := make([]json.RawMessage, 0)
	for i, page := range pages {
		lengths[i] = len(page)
		flat = append(flat, page...)
	}
	resolved, err := r.EnrichTRC20Identity(ctx, req, flat)
	if err != nil {
		return nil, err
	}
	out := make([][]json.RawMessage, len(pages))
	offset := 0
	for i, length := range lengths {
		out[i] = append([]json.RawMessage(nil), resolved[offset:offset+length]...)
		offset += length
	}
	return out, nil
}

func (r *localTRC20IdentityResolver) resolveLocal(ctx context.Context, address string, rawRows []json.RawMessage) ([]json.RawMessage, []json.RawMessage, []int, error) {
	decoded := make([]map[string]any, len(rawRows))
	for i, raw := range rawRows {
		if err := json.Unmarshal(raw, &decoded[i]); err != nil {
			return nil, nil, nil, err
		}
	}
	address = strings.TrimSpace(address)
	if address == "" {
		return append([]json.RawMessage(nil), rawRows...), append([]json.RawMessage(nil), rawRows...), sequence(len(rawRows)), nil
	}
	var fromMillis, toMillis int64
	for _, row := range decoded {
		millis := localActivityTimestampMillis(row["block_timestamp"])
		if millis <= 0 {
			continue
		}
		if fromMillis == 0 || millis < fromMillis {
			fromMillis = millis
		}
		if millis > toMillis {
			toMillis = millis
		}
	}
	if fromMillis == 0 || toMillis == 0 {
		return append([]json.RawMessage(nil), rawRows...), append([]json.RawMessage(nil), rawRows...), sequence(len(rawRows)), nil
	}
	candidates, err := r.addressCandidates(ctx, address, time.UnixMilli(fromMillis).UTC(), time.UnixMilli(toMillis).UTC())
	if err != nil {
		return nil, nil, nil, err
	}

	resolved := append([]json.RawMessage(nil), rawRows...)
	misses := make([]json.RawMessage, 0)
	missIndexes := make([]int, 0)
	used := make(map[string]int)
	for i, row := range decoded {
		info, _ := row["token_info"].(map[string]any)
		key := identityContentKey(textValue(row["transaction_id"]), textValue(info["address"]), textValue(row["from"]), textValue(row["to"]), textValue(row["value"]))
		items, offset := candidates[key], used[key]
		if offset >= len(items) {
			misses = append(misses, rawRows[i])
			missIndexes = append(missIndexes, i)
			continue
		}
		item := items[offset]
		used[key] = offset + 1
		row["_tron_event_index"] = item.logIndex
		row["block_number"] = item.blockNumber
		if item.provider != "" {
			row["_provider"] = item.provider
		}
		resolved[i], _ = json.Marshal(row)
	}
	return resolved, misses, missIndexes, nil
}

func (r *localTRC20IdentityResolver) addressCandidates(ctx context.Context, address string, from, to time.Time) (map[string][]localTRC20Candidate, error) {
	key := strings.ToLower(address) + "|" + strconv.FormatInt(from.UnixMilli(), 10) + "|" + strconv.FormatInt(to.UnixMilli(), 10)
	r.mu.Lock()
	if cached := r.cache[key]; cached != nil {
		r.mu.Unlock()
		return cached, nil
	}
	r.mu.Unlock()

	value, err, _ := r.group.Do(key, func() (any, error) {
		r.mu.Lock()
		if cached := r.cache[key]; cached != nil {
			r.mu.Unlock()
			return cached, nil
		}
		r.mu.Unlock()

		// address_activity is ordered by (chain,address,...), so a new-address
		// lookup is an indexed slice instead of a full token_transfers scan on
		// from_address OR to_address. It already carries consensus log_index.
		queryRows, queryErr := r.ch.DB().Query(ctx, `SELECT tx_hash, log_index, token_address, counterparty, direction, amount_raw, block_number, source_provider
			FROM blockchain.address_activity FINAL WHERE chain='tron' AND address=? AND dataset='token_transfers' AND timestamp>=? AND timestamp<=?`, address, from, to)
		if queryErr != nil {
			return nil, queryErr
		}
		defer queryRows.Close()
		candidates := make(map[string][]localTRC20Candidate)
		for queryRows.Next() {
			var tx, token, counterparty, direction, amount, source string
			var logIndex uint32
			var blockNumber uint64
			if scanErr := queryRows.Scan(&tx, &logIndex, &token, &counterparty, &direction, &amount, &blockNumber, &source); scanErr != nil {
				return nil, scanErr
			}
			from, to, ok := localActivityParticipants(address, counterparty, direction)
			if !ok {
				continue
			}
			contentKey := identityContentKey(tx, token, from, to, amount)
			candidates[contentKey] = append(candidates[contentKey], localTRC20Candidate{logIndex: logIndex, blockNumber: blockNumber, provider: source})
		}
		if rowsErr := queryRows.Err(); rowsErr != nil {
			return nil, rowsErr
		}
		for contentKey := range candidates {
			sort.Slice(candidates[contentKey], func(i, j int) bool { return candidates[contentKey][i].logIndex < candidates[contentKey][j].logIndex })
		}

		r.mu.Lock()
		const maxCachedAddresses = 64
		if len(r.order) >= maxCachedAddresses {
			delete(r.cache, r.order[0])
			r.order = r.order[1:]
		}
		r.cache[key] = candidates
		r.order = append(r.order, key)
		r.mu.Unlock()
		return candidates, nil
	})
	if err != nil {
		return nil, err
	}
	return value.(map[string][]localTRC20Candidate), nil
}

func localActivityParticipants(address, counterparty, direction string) (string, string, bool) {
	switch strings.ToLower(strings.TrimSpace(direction)) {
	case "in":
		return counterparty, address, true
	case "out":
		return address, counterparty, true
	case "self":
		return address, address, true
	default:
		return "", "", false
	}
}

func localActivityTimestampMillis(value any) int64 {
	switch typed := value.(type) {
	case float64:
		return int64(typed)
	case json.Number:
		n, _ := typed.Int64()
		return n
	case string:
		n, _ := strconv.ParseInt(strings.TrimSpace(typed), 10, 64)
		return n
	default:
		n, _ := strconv.ParseInt(fmt.Sprint(typed), 10, 64)
		return n
	}
}

func identityContentKey(tx, token, from, to, amount string) string {
	return normalizedTxID(tx) + "\x00" + strings.ToLower(strings.TrimSpace(token)) + "\x00" + strings.ToLower(strings.TrimSpace(from)) + "\x00" + strings.ToLower(strings.TrimSpace(to)) + "\x00" + strings.TrimSpace(amount)
}

func normalizedTxID(value string) string {
	value = strings.ToLower(strings.TrimPrefix(strings.TrimSpace(value), "0x"))
	if len(value) != 64 {
		return ""
	}
	if _, err := hex.DecodeString(value); err != nil {
		return ""
	}
	return value
}

func textValue(value any) string {
	switch typed := value.(type) {
	case string:
		return typed
	case json.Number:
		return typed.String()
	default:
		return ""
	}
}

func sequence(n int) []int {
	out := make([]int, n)
	for i := range out {
		out[i] = i
	}
	return out
}
