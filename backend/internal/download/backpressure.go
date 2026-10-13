// 全局 Backpressure（文档 §71-§74）：
// Spool 水位 / CH 插入 P95 与 Pending / Memory Budget / Provider 健康 → 统一控制 Fetch 节奏。
package download

import (
	"context"
	"fmt"
	"sync"
	"time"

	"github.com/blockchain-platform/blockchain-platform/internal/config"
	"github.com/blockchain-platform/blockchain-platform/internal/observability"
	"github.com/blockchain-platform/blockchain-platform/internal/provider"
	"github.com/blockchain-platform/blockchain-platform/internal/spool"
)

// ControlState §88：NORMAL / DEGRADED / RECOVERING / PROTECTED。
type ControlState int

const (
	stateNormal ControlState = iota
	stateDegraded
	stateRecovering
	stateProtected
)

func (s ControlState) String() string {
	switch s {
	case stateNormal:
		return "NORMAL"
	case stateDegraded:
		return "DEGRADED"
	case stateRecovering:
		return "RECOVERING"
	default:
		return "PROTECTED"
	}
}

// byteBudget 全局 Decoded Inflight 内存预算（§47：只设并发数不够）。
type byteBudget struct {
	mu   sync.Mutex
	cond *sync.Cond
	cap  int64
	used int64
}

func newByteBudget(cap int64) *byteBudget {
	b := &byteBudget{cap: cap}
	b.cond = sync.NewCond(&b.mu)
	return b
}

// Reserve 阻塞直到预算可容纳 n 字节（Fetch 侧的全局内存背压）。
func (b *byteBudget) Reserve(n int64) {
	if n <= 0 {
		return
	}
	b.mu.Lock()
	for b.used+n > b.cap {
		b.cond.Wait()
	}
	b.used += n
	b.mu.Unlock()
}

// ReserveExtra accounts an already-decoded response without blocking its owner.
func (b *byteBudget) ReserveExtra(n int64) {
	if n <= 0 {
		return
	}
	// The decoded body already exists. Account it without waiting while holding
	// another reservation; further admissions block until these owners release.
	b.mu.Lock()
	b.used += n
	b.mu.Unlock()
}

// ReserveContext bounds admission before HTTP and lets shutdown cancel waiters.
func (b *byteBudget) ReserveContext(ctx context.Context, n int64) error {
	if n <= 0 {
		return ctx.Err()
	}
	if n > b.cap {
		return fmt.Errorf("page memory prediction %d exceeds budget %d", n, b.cap)
	}
	stop := context.AfterFunc(ctx, func() { b.mu.Lock(); b.cond.Broadcast(); b.mu.Unlock() })
	defer stop()
	b.mu.Lock()
	defer b.mu.Unlock()
	for b.used+n > b.cap && ctx.Err() == nil {
		b.cond.Wait()
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	b.used += n
	return nil
}

func (b *byteBudget) Release(n int64) {
	if n <= 0 {
		return
	}
	b.mu.Lock()
	b.used -= n
	if b.used < 0 {
		b.used = 0
	}
	b.cond.Broadcast()
	b.mu.Unlock()
}

func (b *byteBudget) Used() int64 {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.used
}

type backpressure struct {
	cfg      *config.Config
	spool    *spool.Store
	mem      *byteBudget
	prov     provider.Provider
	memGuard *memoryGuard

	mu         sync.Mutex
	state      ControlState
	reason     string
	lastUsed   int64
	lastUsedAt time.Time
	goodSince  time.Time // 恢复迟滞（§74）
}

func newBackpressure(cfg *config.Config, sp *spool.Store, mem *byteBudget, prov provider.Provider) *backpressure {
	return &backpressure{cfg: cfg, spool: sp, mem: mem, prov: prov, memGuard: &memoryGuard{}, state: stateNormal}
}

// cachedUsedRatio 5 秒缓存，避免频繁全树统计（§72）。
func (b *backpressure) cachedUsedRatio() float64 {
	if b.spool == nil {
		return 0
	}
	b.mu.Lock()
	defer b.mu.Unlock()
	if time.Since(b.lastUsedAt) < 5*time.Second && b.lastUsedAt.Unix() > 0 {
		return float64(b.lastUsed) / float64(b.cfg.Ankr.SpoolMaxBytes)
	}
	used, err := b.spool.UsedBytes()
	if err != nil {
		return 0
	}
	b.lastUsed = used
	b.lastUsedAt = time.Now()
	return float64(used) / float64(b.cfg.Ankr.SpoolMaxBytes)
}

// Refresh 重算控制状态（由调度循环周期调用）。
func (b *backpressure) Refresh(ctx context.Context, agg *batchAggregator) {
	a := &b.cfg.Ankr
	ratio := b.cachedUsedRatio()
	var spoolPending int64
	if b.spool != nil {
		spoolPending, _ = b.spool.UsedBytes()
	}
	observability.SpoolPendingBytes.Set(float64(spoolPending))
	observability.SpoolUsedRatio.Set(ratio)

	state := stateNormal
	reason := ""
	if a.SystemMemoryGuardEnabled && b.memGuard != nil {
		if snap, err := sampleSystemMemory(); err == nil {
			observability.WorkerRSSBytes.Set(float64(snap.WorkerRSSBytes))
			observability.WorkerSwapBytes.Set(float64(snap.WorkerSwapBytes))
			observability.VMMemAvailableBytes.Set(float64(snap.VMMemAvailableBytes))
			observability.VMSwapUsedBytes.Set(float64(snap.VMSwapUsedBytes))
			observability.VMPSWAPInTotal.Set(float64(snap.PSWAPIn))
			observability.VMPSWAPOutTotal.Set(float64(snap.PSWAPOut))
			observability.CgroupOOMTotal.Set(float64(snap.CgroupOOM + snap.CgroupOOMKill))
			memState, memReason := b.memGuard.Evaluate(snap, a.WorkerRSSHardMaxBytes, a.VMMemAvailableWarnBytes, a.VMMemAvailableProtectBytes, a.SwapGrowthSamples)
			if memState > state {
				state, reason = memState, memReason
			}
		}
	}
	switch {
	case ratio >= a.SpoolWatermarkStop:
		if stateProtected > state {
			state, reason = stateProtected, "spool watermark >= 95%"
		}
	case ratio >= a.SpoolWatermarkPauseBG:
		if stateProtected > state {
			state, reason = stateProtected, "spool watermark >= 85%"
		}
	case ratio >= a.SpoolWatermarkReduce:
		if stateDegraded > state {
			state, reason = stateDegraded, "spool watermark >= 75%"
		}
	case ratio >= a.SpoolWatermarkMild:
		if stateDegraded > state {
			state, reason = stateDegraded, "spool watermark >= 60%"
		}
	}
	// ClickHouse Backpressure（§73）：插入 P95 恶化 或 Pending 超阈值。
	if agg != nil {
		p95 := agg.InsertP95()
		pending := agg.PendingBytes()
		if p95 > 0 && p95 > a.CHInsertP95Thresh {
			if state < stateDegraded {
				state = stateDegraded
			}
			reason = "CH insert P95 " + p95.String()
		}
		if pending > a.CHBatchMaxBytes*4 {
			state = stateProtected
			reason = "CH pending ingest bytes exceeded"
		}
	}
	// Provider 健康（429/超时风暴 → 保护）。
	if hs := b.prov.Health(ctx); hs.State == provider.HealthOpen || hs.RecentErrorRate > 0.25 {
		if state < stateDegraded {
			state = stateDegraded
		}
		if reason == "" {
			reason = "provider error rate elevated"
		}
	}
	b.mu.Lock()
	switch {
	case state > b.state:
		// 恶化立即生效（§74：Multiplicative Decrease）
		b.state = state
		b.reason = reason
		b.goodSince = time.Time{}
	case state < b.state:
		// 恢复：持续良好 60s 才回到 NORMAL（§74：Additive Increase + Hysteresis）
		if b.goodSince.IsZero() {
			b.goodSince = time.Now()
		}
		if time.Since(b.goodSince) > 60*time.Second {
			b.state = state
			b.reason = reason
		} else {
			b.state = stateRecovering
			b.reason = "recovering: " + reason
		}
	default:
		b.state = state
		b.reason = reason
	}
	cur := b.state
	b.mu.Unlock()
	observability.AnkrControlState.Reset()
	observability.AnkrControlState.WithLabelValues(b.reasonFor(cur)).Set(float64(cur))
}

func (b *backpressure) reasonFor(s ControlState) string {
	if s == stateNormal {
		return "none"
	}
	return "backpressure"
}

// FetchAllowed 是否允许发起新页请求（§71/§72）。
func (b *backpressure) FetchAllowed() bool {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.state < stateProtected
}

// DispatchAllowed 是否允许领取新 Shard（STOP 水位下完全停发）。
func (b *backpressure) DispatchAllowed() bool {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.state < stateProtected
}

func (b *backpressure) State() ControlState {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.state
}

func (b *backpressure) Reason() string {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.reason
}

// MemoryAvailable 剩余内存预算字节数。
func (b *backpressure) MemoryAvailable() int64 {
	used := b.mem.Used()
	if used >= b.cfg.Ankr.FetchMemoryBudgetBytes {
		return 0
	}
	return b.cfg.Ankr.FetchMemoryBudgetBytes - used
}
