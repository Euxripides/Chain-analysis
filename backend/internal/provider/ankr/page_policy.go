package ankr

import (
	"sort"
	"sync"
	"time"

	"github.com/blockchain-platform/blockchain-platform/internal/config"
	"github.com/blockchain-platform/blockchain-platform/internal/observability"
	"github.com/blockchain-platform/blockchain-platform/internal/provider"
)

// PagePolicyState 每个 (chain|dataset|subject) 的自适应状态（§17），可序列化进 Checkpoint。
type PagePolicyState struct {
	CurrentPageSize        int   `json:"current_page_size"`
	ConsecutiveSuccess     int   `json:"consecutive_success"`
	ConsecutiveFastSuccess int   `json:"consecutive_fast_success"`
	LastBodyBytes          int64 `json:"last_response_bytes"`
	LastDurationMs         int64 `json:"last_duration_ms"`
	TimeoutCount           int   `json:"timeout_count"`
	TooLargeCount          int   `json:"too_large_count"`
	LastFailedLevel        int   `json:"last_failed_page_level"`
	CooldownRemaining      int   `json:"cooldown_remaining"`
	// 画像累计（§32）：timeout_rate / oversized_rate / 平均延迟
	Successes     int64     `json:"successes"`
	Failures      int64     `json:"failures"`
	TotalTimeout  int64     `json:"total_timeouts"`
	TotalTooLarge int64     `json:"total_too_large"`
	UpdatedAt     time.Time `json:"updated_at"`
}

// PageDecision 一页失败后的控制器决策（§21 矩阵）。
type PageDecision struct {
	NextPageSize      int
	SplitRange        bool
	ReduceRate        bool
	ReduceConcurrency bool
	RetryAfter        time.Duration
	Backoff           time.Duration
}

// PagePolicy 按 key(chain|dataset|subject_type|subject) 维护自适应 pageSize（§16）。
type PagePolicy struct {
	mu     sync.Mutex
	cfg    *config.Config
	levels []int
	states map[string]*PagePolicyState
}

func NewPagePolicy(cfg *config.Config) *PagePolicy {
	levels := append([]int(nil), cfg.Ankr.PageLevels...)
	sort.Ints(levels)
	return &PagePolicy{cfg: cfg, levels: levels, states: map[string]*PagePolicyState{}}
}

func (p *PagePolicy) Key(chain, dataset, subjectType, subject string) string {
	return chain + "|" + dataset + "|" + subjectType + "|" + subject
}

// InitialSize 数据集初始 pageSize（§33：logs 更小）。
func (p *PagePolicy) InitialSize(dataset string) int {
	a := &p.cfg.Ankr
	switch dataset {
	case "logs":
		if a.PageSizeLogs > 0 {
			return clampLevel(p.levels, a.PageSizeLogs)
		}
	case "transactions":
		if a.PageSizeTransactions > 0 {
			return clampLevel(p.levels, a.PageSizeTransactions)
		}
	case "token_transfers":
		if a.PageSizeTokenTransfers > 0 {
			return clampLevel(p.levels, a.PageSizeTokenTransfers)
		}
	case "nft_transfers":
		if a.PageSizeNftTransfers > 0 {
			return clampLevel(p.levels, a.PageSizeNftTransfers)
		}
	}
	return p.levels[len(p.levels)-1]
}

// clampLevel 将 size 收敛到阶梯（不小于最小、不大于最大）。
func clampLevel(levels []int, size int) int {
	if size <= levels[0] {
		return levels[0]
	}
	for i := len(levels) - 1; i >= 0; i-- {
		if size >= levels[i] {
			return levels[i]
		}
	}
	return levels[0]
}

func (p *PagePolicy) state(key string) *PagePolicyState {
	s, ok := p.states[key]
	if !ok {
		s = &PagePolicyState{UpdatedAt: time.Now()}
		p.states[key] = s
	}
	return s
}

// Current 返回当前页大小（未初始化则用 initialSize）。
func (p *PagePolicy) Current(key, dataset string) int {
	p.mu.Lock()
	defer p.mu.Unlock()
	s := p.state(key)
	if s.CurrentPageSize <= 0 {
		s.CurrentPageSize = p.InitialSize(dataset)
	}
	return s.CurrentPageSize
}

// Restore Checkpoint 恢复（§18：Resume 必须继承上次自适应状态）。
func (p *PagePolicy) Restore(key string, st *PagePolicyState) {
	p.mu.Lock()
	defer p.mu.Unlock()
	if st == nil || st.CurrentPageSize <= 0 {
		return
	}
	cp := *st
	cp.CurrentPageSize = clampLevel(p.levels, cp.CurrentPageSize)
	cp.UpdatedAt = time.Now()
	p.states[key] = &cp
}

// Snapshot 供 Checkpoint 持久化。
func (p *PagePolicy) Snapshot(key string) *PagePolicyState {
	p.mu.Lock()
	defer p.mu.Unlock()
	s := p.state(key)
	cp := *s
	return &cp
}

// OnSuccess 记录成功页并决定下一页是否升档（§14/§15：连续快成功 + cooldown）。
func (p *PagePolicy) OnSuccess(key, dataset string, pageSize, bodyBytes int64, duration time.Duration) int {
	a := &p.cfg.Ankr
	p.mu.Lock()
	defer p.mu.Unlock()
	s := p.state(key)
	if s.CurrentPageSize <= 0 {
		s.CurrentPageSize = p.InitialSize(dataset)
	}
	s.ConsecutiveSuccess++
	s.Successes++
	fast := bodyBytes < a.PageUpMaxBodyBytes && duration < a.PageUpMaxDuration
	if fast {
		s.ConsecutiveFastSuccess++
		if s.CooldownRemaining > 0 {
			s.CooldownRemaining--
		}
	} else {
		s.ConsecutiveFastSuccess = 0
	}
	s.LastBodyBytes = bodyBytes
	s.LastDurationMs = duration.Milliseconds()
	s.UpdatedAt = time.Now()

	// 升档条件：cooldown 结束 + 连续快成功达标 + 一次一级
	if s.CooldownRemaining == 0 && s.ConsecutiveFastSuccess >= a.PageUpSuccessCount {
		next := nextLevel(p.levels, s.CurrentPageSize)
		if next > s.CurrentPageSize {
			s.CurrentPageSize = next
			s.ConsecutiveFastSuccess = 0
			observability.AnkrPageSizeUpgrade.Inc()
		}
	}
	return s.CurrentPageSize
}

// OnError 失败页决策（§13/§20/§21/§27/§28）。
func (p *PagePolicy) OnError(key, dataset string, pe *provider.Error, pageSize int) PageDecision {
	a := &p.cfg.Ankr
	p.mu.Lock()
	defer p.mu.Unlock()
	s := p.state(key)
	if s.CurrentPageSize <= 0 {
		s.CurrentPageSize = p.InitialSize(dataset)
	}
	s.ConsecutiveSuccess = 0
	s.ConsecutiveFastSuccess = 0
	s.Failures++
	s.UpdatedAt = time.Now()
	if pe.Class == provider.ClassRequestTimeout {
		s.TotalTimeout++
	}
	if pe.Class == provider.ClassResponseTooLarge {
		s.TotalTooLarge++
	}
	d := PageDecision{NextPageSize: s.CurrentPageSize, RetryAfter: pe.RetryAfter}

	switch pe.Class {
	case provider.ClassResponseTooLarge:
		// 只在最小档累计失败次数（§20：最小档反复失败才拆 Range）
		if s.CurrentPageSize == p.levels[0] {
			s.TooLargeCount++
		} else {
			s.TooLargeCount = 0
		}
		s.LastFailedLevel = s.CurrentPageSize
		d.NextPageSize = prevLevel(p.levels, s.CurrentPageSize)
		// 降档后进入 cooldown（§15：防振荡）
		s.CooldownRemaining = a.PageDownCooldownSuccess
		observability.AnkrPageSizeDowngrade.Inc()
		// ResponseTooLarge 是请求规模问题，短抖动重试（§27）
		d.Backoff = time.Duration(50+time.Now().UnixNano()%250) * time.Millisecond
		if s.CurrentPageSize == p.levels[0] && s.TooLargeCount >= a.RangeSplitOnMinPageFailures {
			d.SplitRange = true
		}

	case provider.ClassRequestTimeout:
		if s.CurrentPageSize == p.levels[0] {
			s.TimeoutCount++
		} else {
			s.TimeoutCount = 0
		}
		s.LastFailedLevel = s.CurrentPageSize
		d.NextPageSize = prevLevel(p.levels, s.CurrentPageSize)
		s.CooldownRemaining = a.PageDownCooldownSuccess
		observability.AnkrPageSizeDowngrade.Inc()
		// Timeout 短抖动重试（§28：200~1000ms）
		d.Backoff = time.Duration(200+time.Now().UnixNano()%800) * time.Millisecond
		if s.CurrentPageSize == p.levels[0] && s.TimeoutCount >= a.RangeSplitOnMinPageFailures {
			d.SplitRange = true
		}

	case provider.ClassRateLimit:
		d.ReduceRate = true
		d.Backoff = pe.RetryAfter
		if d.Backoff <= 0 {
			d.Backoff = 200 * time.Millisecond
		}

	case provider.ClassConnectionLimit:
		d.ReduceConcurrency = true
		d.Backoff = 300 * time.Millisecond

	case provider.ClassRequestTooLarge:
		d.SplitRange = true

	case provider.ClassInvalidJSON, provider.ClassUpstream:
		// 有限重试（§21），不动 pageSize
		d.Backoff = 500 * time.Millisecond

	case provider.ClassBodyTruncated:
		// 传输层截断：短抖动重试同一逻辑页（§66），不动 pageSize
		d.Backoff = 200 * time.Millisecond
		// Large responses that repeatedly end before the JSON document closes
		// should not replay the same expensive page unchanged. Small transport
		// interruptions keep their existing page size.
		if pe.BodyBytes >= 1<<20 && pe.Duration > 30*time.Second {
			d.NextPageSize = prevLevel(p.levels, prevLevel(p.levels, s.CurrentPageSize))
			s.CooldownRemaining = a.PageDownCooldownSuccess
			if d.NextPageSize < s.CurrentPageSize {
				observability.AnkrPageSizeDowngrade.Inc()
			}
		}

	case provider.ClassBodyIdleTimeout:
		// Body Idle：Provider 拥塞或超大响应，短抖动重试（§22/§68）
		d.Backoff = 300 * time.Millisecond
	}

	// 状态落定：CurrentPageSize 与决策同步（Checkpoint/Resume 依赖）
	if d.NextPageSize != s.CurrentPageSize {
		s.CurrentPageSize = d.NextPageSize
		observability.AnkrPageSizeGauge.WithLabelValues(key).Set(float64(d.NextPageSize))
	}
	return d
}

// prevLevel 降一级（最小不再降）。
func prevLevel(levels []int, cur int) int {
	for i := len(levels) - 1; i >= 1; i-- {
		if cur >= levels[i] {
			return levels[i-1]
		}
	}
	return levels[0]
}

// nextLevel 升一级（最大不再升）。
func nextLevel(levels []int, cur int) int {
	for i := 0; i < len(levels)-1; i++ {
		if cur <= levels[i] {
			return levels[i+1]
		}
	}
	return levels[len(levels)-1]
}
