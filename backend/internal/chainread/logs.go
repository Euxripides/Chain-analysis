package chainread

import (
	"context"
	"strings"
	"sync"

	"github.com/blockchain-platform/blockchain-platform/internal/chain"
	rpc "github.com/blockchain-platform/blockchain-platform/internal/provider/rpc"
)

// LogsService eth_getLogs（§95-§98）。
//
// 用途：精确区间查询、数据修复、事件核验、小范围补洞。
// 明确不用于"任意钱包全历史"：address 过滤的是发出事件的合约（§96）。
type LogsService struct {
	reader *Service

	mu        sync.Mutex
	windows   map[string]uint64
	successes map[string]int
	minWindow uint64
}

func NewLogsService(reader *Service) *LogsService {
	return &LogsService{reader: reader, windows: map[string]uint64{}, successes: map[string]int{}, minWindow: 50}
}

// MaxLogResults 单次 Fetch 的结果上限，避免无界内存。
const MaxLogResults = 200000

func (l *LogsService) window(chainSlug string) uint64 {
	l.mu.Lock()
	defer l.mu.Unlock()
	if current, ok := l.windows[chainSlug]; ok && current > 0 {
		return current
	}
	initial := uint64(2000)
	if config, ok := l.reader.chains.Get(chainSlug); ok && config.MaxLogBlockRange > 0 {
		initial = config.MaxLogBlockRange
	}
	l.windows[chainSlug] = initial
	return initial
}

// shrink 区间过大 / 超时 / 响应过大 → 缩小窗口（§98）。
func (l *LogsService) shrink(chainSlug string) uint64 {
	l.mu.Lock()
	defer l.mu.Unlock()
	current := l.windows[chainSlug]
	if current == 0 {
		current = 2000
	}
	next := current / 2
	if next < l.minWindow {
		next = l.minWindow
	}
	l.windows[chainSlug] = next
	l.successes[chainSlug] = 0
	return next
}

// grow 连续成功后缓慢恢复（§98）。
func (l *LogsService) grow(chainSlug string) {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.successes[chainSlug]++
	if l.successes[chainSlug] < 5 {
		return
	}
	l.successes[chainSlug] = 0
	limit := uint64(2000)
	if config, ok := l.reader.chains.Get(chainSlug); ok && config.MaxLogBlockRange > 0 {
		limit = config.MaxLogBlockRange
	}
	current := l.windows[chainSlug]
	next := current * 2
	if next > limit {
		next = limit
	}
	l.windows[chainSlug] = next
}

// Window 当前自适应窗口（观测/测试用）。
func (l *LogsService) Window(chainSlug string) uint64 { return l.window(chainSlug) }

// Fetch 按区间抓取日志，自动拆分与自适应缩放（§97/§98）。
func (l *LogsService) Fetch(ctx context.Context, chainSlug string, filter LogFilter, opt Options) ([]Log, error) {
	if filter.ToBlock < filter.FromBlock {
		return nil, &rpc.Error{Class: rpc.ClassInvalidParams, Chain: chainSlug,
			Message: "to_block must be >= from_block"}
	}
	if opt.Purpose == "" {
		opt.Purpose = rpc.PurposeLogsRepair
	}
	out := make([]Log, 0, 256)
	start := filter.FromBlock
	for start <= filter.ToBlock {
		window := l.window(chainSlug)
		end := start + window - 1
		if end > filter.ToBlock {
			end = filter.ToBlock
		}
		logs, err := l.fetchRange(ctx, chainSlug, filter, start, end, opt)
		if err != nil {
			mapped := rpc.AsError(err)
			// 区间过大 / 超时 / 响应过大 → 缩小窗口重试同一段（§98）
			if mapped.Class == rpc.ClassRangeTooLarge || mapped.Class == rpc.ClassTimeout ||
				mapped.Class == rpc.ClassResponseTooLarge {
				next := l.shrink(chainSlug)
				if next >= window && window <= l.minWindow {
					return out, err
				}
				continue
			}
			return out, err
		}
		l.grow(chainSlug)
		out = append(out, logs...)
		if len(out) > MaxLogResults {
			return out, &rpc.Error{Class: rpc.ClassRangeTooLarge, Chain: chainSlug,
				Message: "log result set exceeds the configured maximum, narrow the range"}
		}
		start = end + 1
	}
	return out, nil
}

func (l *LogsService) fetchRange(ctx context.Context, chainSlug string, filter LogFilter,
	from, to uint64, opt Options) ([]Log, error) {
	criteria := map[string]any{
		"fromBlock": chain.Uint64ToHex(from),
		"toBlock":   chain.Uint64ToHex(to),
	}
	if len(filter.Addresses) > 0 {
		addresses := make([]string, len(filter.Addresses))
		for i, address := range filter.Addresses {
			addresses[i] = strings.ToLower(string(address))
		}
		criteria["address"] = addresses
	}
	if len(filter.Topics) > 0 {
		criteria["topics"] = filter.Topics
	}
	req := opt.request(chainSlug, "eth_getLogs", []any{criteria}, rpc.ClassLogs,
		chain.AtBlock(to), rpc.PurposeLogsRepair)
	response, err := l.reader.call(ctx, req)
	if err != nil {
		return nil, err
	}
	var items []rawLog
	if err := jsonUnmarshal(response.Result, &items); err != nil {
		return nil, &rpc.Error{Class: rpc.ClassInvalidResponse, Chain: chainSlug,
			Method: "eth_getLogs", Message: "logs payload is not an array"}
	}
	logs, err := convertLogs(items)
	if err != nil {
		return nil, &rpc.Error{Class: rpc.ClassInvalidResponse, Chain: chainSlug,
			Method: "eth_getLogs", Message: err.Error()}
	}
	return logs, nil
}
