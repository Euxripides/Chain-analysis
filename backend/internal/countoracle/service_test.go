// V1.3 Count Oracle Provider Port 验收测试（§84-§88/§104 Gates）。
// 无 DB / 无网络：MockParallelProvider 验证 Port 没被全局串行化。
package countoracle

import (
	"context"
	"fmt"
	"io"
	"log/slog"
	"sort"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/blockchain-platform/blockchain-platform/internal/config"
	pc "github.com/blockchain-platform/blockchain-platform/internal/provider/count"
	"github.com/blockchain-platform/blockchain-platform/internal/provider/mockparallel"
)

func testConfig(t *testing.T, mockMaxConc, mockLatencyMs int) *config.Config {
	t.Helper()
	cfg := &config.Config{}
	cfg.CountOracle.Enabled = true
	cfg.CountOracle.CountAwareShardingEnabled = false
	cfg.CountOracle.CacheEnabled = true
	cfg.CountOracle.SingleflightEnabled = true
	cfg.CountOracle.CalibrationEnabled = false
	cfg.CountOracle.PlanningDeadline = 5 * time.Second
	cfg.CountOracle.CacheTTLHistoric = 24 * time.Hour
	cfg.CountOracle.CacheTTLNearHead = 60 * time.Second
	cfg.CountProvider.PortEnabled = true
	cfg.CountProvider.Shadow = false
	cfg.CountProvider.RecordAttempts = true
	cfg.CountProvider.MockParallelEnabled = true
	cfg.CountProvider.MockParallelMaxConcurrency = mockMaxConc
	cfg.CountProvider.MockParallelLatencyMS = mockLatencyMs
	cfg.CountProvider.MockParallelQueueCapacity = 4096
	cfg.CountProvider.GlobalMaxInflight = 0
	return cfg
}

func newTestService(t *testing.T, cfg *config.Config) *Service {
	t.Helper()
	log := slog.New(slog.NewTextHandler(io.Discard, nil))
	svc := NewService(cfg, nil, log)
	svc.setDeployment(&deployment{
		chainIDByChain: map[string]uint64{"bsc": 56},
		chainByID:      map[uint64]string{56: "bsc"},
		countable:      map[string]bool{"bsc|transactions": true, "bsc|token_transfers": true},
	})
	return svc
}

func runService(ctx context.Context, svc *Service) { go svc.Run(ctx) }

func req(i int, ds pc.DatasetType) pc.CountRequest {
	return pc.CountRequest{
		ChainID:           56,
		Dataset:           ds,
		Subject:           pc.SubjectRef{Type: pc.SubjectAddress, Value: fmt.Sprintf("0x%040x", i)},
		Range:             pc.BlockRange{Start: 1, End: 1001},
		Purpose:           pc.PurposePlanning,
		QueryShapeVersion: 1,
	}
}

// §85 Parallel Port Gate：1000 independent requests @ concurrency 100 / 200ms
// → >=350 valid counts/s 且 observed inflight <= 100（证明没有全局串行化）。
func TestParallelPortGate(t *testing.T) {
	const n = 1000
	cfg := testConfig(t, 100, 200)
	svc := newTestService(t, cfg)
	mp := mockparallel.New(mockparallel.Options{MaxConcurrency: 100, Latency: 200 * time.Millisecond})
	if err := svc.Register(mp); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	runService(ctx, svc)
	start := time.Now()
	okCount := atomic.Int64{}
	var wg sync.WaitGroup
	for i := 0; i < n; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			r := svc.Resolve(ctx, req(i, pc.DatasetTransactions))
			if r.Status == StatusResolved && r.Result != nil && r.Result.Count >= 0 {
				okCount.Add(1)
			} else {
				t.Logf("unexpected resolution: %s %s", r.Status, r.Reason)
			}
		}(i)
	}
	wg.Wait()
	elapsed := time.Since(start)
	st := mp.Stats()
	t.Logf("1000 reqs wall=%v ok=%d rps=%.0f calls=%d maxInflight=%d avgLat=%v",
		elapsed, okCount.Load(), float64(okCount.Load())/elapsed.Seconds(), st.Calls, st.MaxObservedInflight, st.AvgLatency)
	if okCount.Load() != n {
		t.Fatalf("valid counts = %d, want %d", okCount.Load(), n)
	}
	rps := float64(okCount.Load()) / elapsed.Seconds()
	if rps < 350 {
		t.Fatalf("valid count rps = %.0f < 350 gate（Port 疑似被串行化）", rps)
	}
	if st.MaxObservedInflight > 100 {
		t.Fatalf("observed inflight = %d > 100", st.MaxObservedInflight)
	}
	if elapsed > 10*time.Second {
		t.Fatalf("wall %v too slow", elapsed)
	}
}

// §87 Singleflight Gate：100 identical → provider calls == 1；100 distinct → 并行。
func TestSingleflightGate(t *testing.T) {
	cfg := testConfig(t, 50, 100)
	svc := newTestService(t, cfg)
	mp := mockparallel.New(mockparallel.Options{MaxConcurrency: 50, Latency: 100 * time.Millisecond})
	if err := svc.Register(mp); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	runService(ctx, svc)
	const dup = 100
	base := req(777, pc.DatasetTransactions)
	start := time.Now()
	var wg sync.WaitGroup
	ok := atomic.Int64{}
	for i := 0; i < dup; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			r := svc.Resolve(ctx, base)
			if r.Status == StatusResolved && r.Result != nil && r.Result.Count > 0 {
				ok.Add(1)
			}
		}()
	}
	wg.Wait()
	if got := mp.Stats().Calls; got != 1 {
		t.Fatalf("100 duplicate requests -> provider calls = %d, want 1（Singleflight §87）", got)
	}
	if ok.Load() != dup {
		t.Fatalf("duplicates resolved ok = %d/%d", ok.Load(), dup)
	}
	dupWall := time.Since(start)
	// 100 个不同地址必须真正并行（§87）
	start = time.Now()
	baseCalls := mp.Stats().Calls
	for i := 0; i < 100; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			_ = svc.Resolve(ctx, req(i+2000, pc.DatasetTransactions))
		}(i)
	}
	wg.Wait()
	distinctWall := time.Since(start)
	if got := mp.Stats().Calls - baseCalls; got != 100 {
		t.Fatalf("100 distinct -> provider calls = %d, want 100", got)
	}
	t.Logf("dup wall=%v (expect ~1 provider latency) distinct wall=%v", dupWall, distinctWall)
	if distinctWall > 3*time.Second {
		t.Fatalf("100 distinct requests serialized? wall=%v", distinctWall)
	}
}

// §88 Isolation Gate：低速 Provider backlog 不得拖死高速 Provider（每 Provider Executor 隔离 §30）。
// 判定：中位数多轮测量（抑制 VM 噪声）+ 直接并行证据（loaded 期间 fast executor 并发水位 ≈ solo）。
func TestProviderIsolationGate(t *testing.T) {
	latency := time.Duration(50) * time.Millisecond
	if raceActive {
		latency = 30 * time.Millisecond // race 插桩开销大：用更短延迟控制总时长
	}
	cfg := testConfig(t, 100, int(latency/time.Millisecond))
	svc := newTestService(t, cfg)
	fast := mockparallel.New(mockparallel.Options{MaxConcurrency: 100, Latency: latency})
	if err := svc.Register(fast); err != nil {
		t.Fatal(err)
	}
	slow := mockparallel.New(mockparallel.Options{ID: "mockslow", MaxConcurrency: 8, Latency: 250 * time.Millisecond})
	if err := svc.Register(slow); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	runService(ctx, svc)

	batch := 3000 // 2.5s 级窗口，抑制 VM 噪声
	if raceActive {
		batch = 1200
	}
	measure := func() (rps float64, maxActive int64) {
		start := time.Now()
		var wg sync.WaitGroup
		var ok atomic.Int64
		for i := 0; i < batch; i++ {
			wg.Add(1)
			go func(i int) {
				defer wg.Done()
				r := svc.Resolve(ctx, req(i+3000, pc.DatasetTransactions))
				if r.Status == StatusResolved {
					ok.Add(1)
				}
			}(i)
		}
		// 采样 fast executor 并发水位（直接并行证据）
		var peak atomic.Int64
		stop := make(chan struct{})
		go func() {
			tk := time.NewTicker(2 * time.Millisecond)
			defer tk.Stop()
			for {
				select {
				case <-stop:
					return
				case <-tk.C:
					ex := svc.executors["mockparallel"]
					if ex != nil {
						if v := int64(ex.inflightActive()); v > peak.Load() {
							peak.Store(v)
						}
					}
				}
			}
		}()
		wg.Wait()
		close(stop)
		return float64(ok.Load()) / time.Since(start).Seconds(), peak.Load()
	}
	saturateSlow := func(wg *sync.WaitGroup, n int) {
		ex := svc.executors["mockslow"]
		if ex == nil {
			t.Fatal("mockslow executor not registered")
		}
		for i := 0; i < n; i++ {
			wg.Add(1)
			go func(i int) {
				defer wg.Done()
				call := &callReq{ctx: ctx, req: req(i+9000, pc.DatasetTransactions), result: make(chan callResult, 1)}
				if err := ex.enqueue(call); err != nil {
					return
				}
				select {
				case <-call.result:
				case <-ctx.Done():
				}
			}(i)
		}
	}
	// interleave: S L S L S L → median 比较
	var soloRPS, loadedRPS []float64
	var soloActive, loadedActive []int64
	for r := 0; r < 3; r++ {
		{
			rps, act := measure()
			soloRPS = append(soloRPS, rps)
			soloActive = append(soloActive, act)
		}
		var slowWg sync.WaitGroup
		saturateSlow(&slowWg, 120)
		time.Sleep(150 * time.Millisecond) // 让 slow backlog 铺满 queue
		rps, act := measure()
		loadedRPS = append(loadedRPS, rps)
		loadedActive = append(loadedActive, act)
		slowWg.Wait()
	}
	med := func(v []float64) float64 { s := append([]float64(nil), v...); sort.Float64s(s); return s[len(s)/2] }
	medI := func(v []int64) int64 {
		s := append([]int64(nil), v...)
		sort.Slice(s, func(i, j int) bool { return s[i] < s[j] })
		return s[len(s)/2]
	}
	solo := med(soloRPS)
	loaded := med(loadedRPS)
	peakSolo := medI(soloActive)
	peakLoaded := medI(loadedActive)
	t.Logf("fast solo median rps=%.0f peakActive=%d | loaded median rps=%.0f peakActive=%d degradation=%.1f%%",
		solo, peakSolo, loaded, peakLoaded, (1-loaded/solo)*100)
	// 1) 直接并行证据：loaded 期间 fast executor 必须仍然接近全并发（无跨 Provider 串行化）
	if peakLoaded < int64(float64(peakSolo)*0.8) || peakSolo < 80 {
		t.Fatalf("fast executor active during slow load = %d (solo %d): 疑似全局串行化（§30/§31）", peakLoaded, peakSolo)
	}
	// 2) 吞吐降级阈值：正常模式 <10%；race 模式放宽（race 插桩共享开销放大 CPU 噪声；隔离证据以峰值并发为准）
	limit := 0.10
	if raceActive {
		limit = 0.35
	}
	if limit < 1 && loaded > solo {
		return // loaded 中位数高于 solo：无降级
	}
	if loaded < solo*(1-limit) {
		t.Fatalf("fast provider degraded %.1f%% (limit %.0f%%): slow backlog 泄漏到高速 Executor（§88）", (1-loaded/solo)*100, limit*100)
	}
}

// §89/§104 Deadline：queue 饱和 + deadline budget → Fail-Open 快速返回，绝不停留。
func TestDeadlineGate(t *testing.T) {
	cfg := testConfig(t, 2, 400)
	cfg.CountProvider.MockParallelMaxConcurrency = 2
	cfg.CountProvider.MockParallelLatencyMS = 400
	svc := newTestService(t, cfg)
	mp := mockparallel.New(mockparallel.Options{MaxConcurrency: 2, Latency: 400 * time.Millisecond})
	if err := svc.Register(mp); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	runService(ctx, svc)
	start := time.Now()
	var deadlineCount atomic.Int64
	var wg sync.WaitGroup
	for i := 0; i < 60; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			r := req(i+8000, pc.DatasetTransactions)
			r.Deadline = time.Now().Add(150 * time.Millisecond)
			res := svc.Resolve(ctx, r)
			if res.Status == StatusDeadline || res.Status == StatusUnavailable || res.Status == StatusFailOpen {
				deadlineCount.Add(1)
			}
		}(i)
	}
	wg.Wait()
	elapsed := time.Since(start)
	t.Logf("60 burst reqs w/ 150ms deadline: wall=%v soft-fail=%d", elapsed, deadlineCount.Load())
	if elapsed > 5*time.Second {
		t.Fatalf("deadline gate hang: wall=%v（Job 不得停留 PLANNING）", elapsed)
	}
	if deadlineCount.Load() < 30 {
		t.Fatalf("expected majority soft-fail under saturated slow queue, got %d", deadlineCount.Load())
	}
}

// Cache + Singleflight 语义：canonical key provider-independent。
func TestCanonicalCacheAndZero(t *testing.T) {
	cfg := testConfig(t, 50, 30)
	svc := newTestService(t, cfg)
	mp := mockparallel.New(mockparallel.Options{MaxConcurrency: 50, Latency: 30 * time.Millisecond})
	if err := svc.Register(mp); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	runService(ctx, svc)
	r1 := req(42, pc.DatasetTransactions)
	ttl := time.Hour
	first := svc.resolveCore(ctx, r1, ttl)
	if first.Status != StatusResolved {
		t.Fatalf("first = %s", first.Status)
	}
	if got := mp.Stats().Calls; got != 1 {
		t.Fatalf("calls after first = %d", got)
	}
	second := svc.resolveCore(ctx, r1, ttl)
	if second.Status != StatusCacheHit {
		t.Fatalf("second resolve = %s, want CACHE_HIT（provider-independent canonical cache）", second.Status)
	}
	if got := mp.Stats().Calls; got != 1 {
		t.Fatalf("cache hit must not call provider, calls = %d", got)
	}
	// 无 Provider 可路由（全部 blocked）→ FAIL_OPEN / UNAVAILABLE 快速返回
	cfg2 := testConfig(t, 50, 30)
	svc2 := newTestService(t, cfg2)
	ctx2, cancel2 := context.WithCancel(context.Background())
	defer cancel2()
	runService(ctx2, svc2)
	blocked := svc2.Resolve(ctx2, req(1, pc.DatasetTransactions))
	if blocked.Status != StatusUnavailable && blocked.Status != StatusFailOpen {
		t.Fatalf("no provider -> %s, want UNAVAILABLE/FAIL_OPEN", blocked.Status)
	}
	if !blocked.FallbackRecommended {
		t.Fatalf("no provider must recommend fallback")
	}
}

// Capable / Configured 路由行为。
func TestRoutingAndCapability(t *testing.T) {
	cfg := testConfig(t, 50, 20)
	svc := newTestService(t, cfg)
	if err := svc.Register(mockparallel.New(mockparallel.Options{MaxConcurrency: 50, Latency: 20 * time.Millisecond})); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	if !svc.Capable(ctx, "bsc", "transactions") {
		t.Fatalf("bsc/transactions should be capable")
	}
	if !svc.Capable(ctx, "bsc", "token_transfers") {
		t.Fatalf("bsc/token_transfers should be capable (mock)")
	}
	if svc.Capable(ctx, "eth", "transactions") {
		t.Fatalf("eth not in deployment -> not capable")
	}
	if !svc.Configured() {
		t.Fatalf("mock provider should be configured")
	}
}

// RecordActual/RecordObservation/RecordShadowDiff nil-store 安全（不 panic）。
func TestNilStoreSafe(t *testing.T) {
	cfg := testConfig(t, 10, 10)
	svc := newTestService(t, cfg)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	svc.RecordObservation(nil)
	svc.RecordActual("mockparallel", "bsc", "transactions", "hash", 5, 6)
	svc.RecordShadowDiff(ctx, nil)
	svc.Resolve(ctx, req(1, pc.DatasetTransactions))
}
