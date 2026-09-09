// V1.3 Count Oracle Provider Port Service（§2/§29/§46-§49/§52-§56/§89-§93）。
// Service = Registry + Router + Scheduler(Executor) + Canonical Cache + Singleflight + Calibration + Fail Open。
// 不 import 任何具体 Provider 实现；Chainbase / MockParallel 都是注册进来的 Adapter。
package countoracle

import (
	"context"
	"fmt"
	"log/slog"
	"math"
	"strings"
	"sync"
	"time"

	"github.com/blockchain-platform/blockchain-platform/internal/config"
	"github.com/blockchain-platform/blockchain-platform/internal/model"
	pc "github.com/blockchain-platform/blockchain-platform/internal/provider/count"
	"github.com/blockchain-platform/blockchain-platform/internal/store"
)

// Service Count Oracle Provider Port 实现。
type Service struct {
	cfg   *config.Config
	store *store.Store // nil = 无 DB 环境（测试）；所有 DB 副作用自动跳过
	log   *slog.Logger

	enabled bool
	aware   bool

	useCache  bool
	useSingle bool
	useCalib  bool

	// Registry（注册顺序 = 路由优先顺序）
	regMu        sync.RWMutex
	providerList []pc.CountProvider
	policyMap    map[string]*ProviderPolicy
	executors    map[string]*executor

	deploy *deployment

	// 全局 Count 预算（§28/§97：COUNT_PROVIDER_GLOBAL_MAX_INFLIGHT）
	global chan struct{}

	cache     *countCache
	cacheHits int64

	sfMu sync.Mutex
	sf   map[string]*sfRes

	calibMu sync.Mutex
	calib   map[string]*model.CountCalibration // provider|chain|dataset

	startOnce sync.Once
}

// sfRes 单飞共享解析结果。
type sfRes struct {
	done chan struct{}
	res  CountResolution
}

// NewService 构造 Provider Port Service。Provider 通过 Register 注入；Deployment 通过 SetDeployment。
func NewService(cfg *config.Config, st *store.Store, log *slog.Logger) *Service {
	enabled := cfg.CountOracle.Enabled
	svc := &Service{
		cfg: cfg, store: st, log: log,
		enabled:   enabled,
		aware:     enabled && cfg.CountOracle.CountAwareShardingEnabled,
		useCache:  cfg.CountOracle.CacheEnabled,
		useSingle: cfg.CountOracle.SingleflightEnabled,
		useCalib:  cfg.CountOracle.CalibrationEnabled,
		policyMap: map[string]*ProviderPolicy{},
		executors: map[string]*executor{},
		deploy:    &deployment{chainIDByChain: map[string]uint64{}, chainByID: map[uint64]string{}, countable: map[string]bool{}},
		cache:     newCountCache(),
		sf:        map[string]*sfRes{},
		calib:     map[string]*model.CountCalibration{},
	}
	if g := cfg.CountProvider.GlobalMaxInflight; g > 0 {
		svc.global = make(chan struct{}, g)
	}
	return svc
}

// Enabled 总开关（§97：COUNT_ORACLE_ENABLED && 至少一个 Provider 注册）。
func (s *Service) Enabled() bool {
	s.regMu.RLock()
	defer s.regMu.RUnlock()
	return s.enabled && len(s.providerList) > 0
}

// Aware Count-aware Planning（false = Shadow Mode §89/§97）。
func (s *Service) Aware() bool { return s.aware }

// Shadow Port Shadow（§97：Port 计算/记录，旧 Planner 仍做最终决策）。
func (s *Service) Shadow() bool {
	return s.cfg.CountProvider.PortEnabled && s.cfg.CountProvider.Shadow
}

// Configured 是否存在可用 Provider（chainbase adapter 需要 API Key；mock 恒可用）。
func (s *Service) Configured() bool {
	s.regMu.RLock()
	defer s.regMu.RUnlock()
	if !s.enabled || len(s.providerList) == 0 {
		return false
	}
	for _, p := range s.providerList {
		h := p.Health(context.Background())
		if h.Healthy {
			return true
		}
	}
	return false
}

// Register 注册 Provider（构造 Executor 与 Policy；重复注册报错）。
func (s *Service) Register(p pc.CountProvider) error {
	s.regMu.Lock()
	defer s.regMu.Unlock()
	for _, exist := range s.providerList {
		if exist.ID() == p.ID() {
			return fmt.Errorf("count provider already registered: %s", p.ID())
		}
	}
	pol := s.policyFor(p.ID())
	hook := s.onAttemptHook()
	ex := newExecutor(p, pol, hook)
	s.providerList = append(s.providerList, p)
	s.policyMap[p.ID()] = pol
	s.executors[p.ID()] = ex
	s.log.Info("count provider registered", "provider", p.ID(),
		"max_concurrency", pol.MaxConcurrency, "max_rps", pol.MaxRPS, "queue", pol.QueueCapacity)
	return nil
}

// policyFor provider-specific 执行策略（§29/§76/§84：来自配置，禁止写死）。
func (s *Service) policyFor(id string) *ProviderPolicy {
	cp := s.cfg.CountProvider
	timeout := s.cfg.Chainbase.CountTimeout
	if timeout <= 0 {
		timeout = 3 * time.Second
	}
	cooldown := time.Duration(cp.ChainbaseRateCooldownMS) * time.Millisecond
	if cooldown <= 0 {
		cooldown = 1800 * time.Millisecond
	}
	switch id {
	case "chainbase":
		return &ProviderPolicy{
			ProviderID: id, Enabled: cp.ChainbaseEnabled && s.cfg.Chainbase.Enabled,
			MaxConcurrency:        cp.ChainbaseMaxConcurrency,
			MaxRPS:                cp.ChainbaseMaxRPS,
			Burst:                 1,
			QueueCapacity:         cp.ChainbaseQueueCapacity,
			Retries:               s.cfg.Chainbase.MaxRetries,
			RateLimitCooldown:     cooldown,
			QuotaCooldown:         time.Minute,
			CallTimeout:           timeout,
			CostPerRequestCredits: s.cfg.Chainbase.CreditsPerRequest,
			CostPerRequestUSD:     s.cfg.Chainbase.CostPerRequestUSD,
		}
	case "mockparallel":
		mc := cp.MockParallelMaxConcurrency
		if mc <= 0 {
			mc = 100
		}
		q := cp.MockParallelQueueCapacity
		if q <= 0 {
			q = 2048
		}
		return &ProviderPolicy{
			ProviderID: id, Enabled: cp.MockParallelEnabled,
			MaxConcurrency: mc, MaxRPS: cp.MockParallelMaxRPS, Burst: mc,
			QueueCapacity: q, Retries: 0,
			RateLimitCooldown: 200 * time.Millisecond, QuotaCooldown: time.Second,
			CallTimeout: 10 * time.Second,
		}
	default:
		return &ProviderPolicy{
			ProviderID: id, Enabled: true,
			MaxConcurrency: 8, MaxRPS: 0, Burst: 8,
			QueueCapacity: 256, Retries: 0,
			RateLimitCooldown: time.Second, QuotaCooldown: time.Minute,
			CallTimeout: timeout,
		}
	}
}

// providerEnabled 直接配置开关（Router 使用）。
func (s *Service) providerEnabled(id string) bool {
	s.regMu.RLock()
	defer s.regMu.RUnlock()
	if pol, ok := s.policyMap[id]; ok {
		return pol.Enabled
	}
	return false
}

// Capable 该 (chain,dataset) 是否可路由到某个可用 Provider。
func (s *Service) Capable(ctx context.Context, chain, dataset string) bool {
	if !s.Enabled() {
		return false
	}
	ds, ok := datasetOf(dataset)
	if !ok {
		return false
	}
	chainID := s.deploy.chainID(chain)
	if chainID == 0 {
		return false // 部署矩阵外 chain：不参与 Count 平面
	}
	for _, p := range s.providers() {
		pol := s.policyMap[p.ID()]
		if pol == nil || !pol.Enabled {
			continue
		}
		caps := p.Capabilities(ctx)
		dc, ok := caps.DatasetCap(chainID, ds)
		if !ok || !dc.Enabled {
			continue
		}
		if dc.RuntimeStatus == pc.StatusBlocked || dc.RuntimeStatus == pc.StatusDisabled {
			continue
		}
		if p.ID() == "chainbase" && !s.deploy.chainbaseCountable(ctx, chainID, string(ds)) {
			continue
		}
		if ex, ok := s.executors[p.ID()]; ok {
			if routable, _ := ex.routable(); routable {
				return true
			}
		}
	}
	return false
}

func (s *Service) onlyChainbase() bool {
	s.regMu.RLock()
	defer s.regMu.RUnlock()
	return len(s.providerList) == 1 && s.providerList[0].ID() == "chainbase"
}

// ---- Planner 兼容门面：model.CountRequest → model.CountResult（内部全部走 Canonical Port） ----

// Count 单次 Count（Cache → Singleflight → Router → Executor），任何失败 RawCount=-1 + FailureClass（§17）。
func (s *Service) Count(ctx context.Context, r model.CountRequest) model.CountResult {
	if !s.Enabled() {
		return modelFailResult(r, "disabled", "count oracle disabled", "")
	}
	if !s.Configured() {
		return modelFailResult(r, "no_key", "no configured count provider", "")
	}
	chainID := uint64(r.ChainID)
	if chainID <= 0 {
		chainID = s.deploy.chainID(r.Chain)
	}
	if chainID <= 0 && s.onlyChainbase() {
		return modelFailResult(r, "unsupported_chain", "chain not in deployment matrix: "+r.Chain, "chainbase")
	}
	deadline := time.Now().Add(s.deadlineFor(ctx))
	creq, ok := canonicalRequest(r, chainID, deadline, pc.PurposePlanning)
	if !ok {
		return modelFailResult(r, "unsupported_dataset", "dataset not supported: "+r.Dataset, "")
	}
	res := s.resolveCore(ctx, creq, s.ttlFor(r))
	return s.resolutionToModel(ctx, res, r)
}

// Resolve 规范解析入口（§46）——Oracle interface。
func (s *Service) Resolve(ctx context.Context, req pc.CountRequest) CountResolution {
	if req.Deadline.IsZero() {
		req.Deadline = time.Now().Add(s.deadlineFor(ctx))
	}
	if req.RequestID == "" {
		req.RequestID = canonicalHash(req)
	}
	if req.Priority == "" {
		req.Priority = pc.PriorityPlanner
	}
	if req.QueryShapeVersion == 0 {
		req.QueryShapeVersion = 1
	}
	return s.resolveCore(ctx, req, 0)
}

func (s *Service) deadlineFor(ctx context.Context) time.Duration {
	d := s.cfg.CountOracle.PlanningDeadline
	if d <= 0 {
		d = 5 * time.Second
	}
	if dl, ok := ctx.Deadline(); ok {
		if remain := time.Until(dl); remain > 0 && remain < d {
			d = remain
		}
	}
	return d
}

// ttlFor（§20）：历史固定区间长 TTL；接近链头短 TTL（沿用 V1.2 语义）。
func (s *Service) ttlFor(r model.CountRequest) time.Duration {
	if r.ToBlock > 0 && r.TargetBlock > 0 {
		window := s.cfg.Chainbase.NearHeadWindowBlocks
		if window <= 0 {
			window = 50000
		}
		if r.TargetBlock > uint64(window) && r.ToBlock >= r.TargetBlock-uint64(window) {
			return s.cfg.CountOracle.CacheTTLNearHead
		}
	}
	return s.cfg.CountOracle.CacheTTLHistoric
}

// resolveCore 解析核心：Cache → Singleflight → Router → Executor（bounded queue + deadline aware）。
func (s *Service) resolveCore(ctx context.Context, creq pc.CountRequest, cacheTTL time.Duration) CountResolution {
	key := creq.RequestID
	fail := func(status ResolutionStatus, reason string) CountResolution {
		return CountResolution{Status: status, Reason: reason, FallbackRecommended: true}
	}
	if !s.enabled {
		return fail(StatusFailOpen, "count oracle disabled")
	}
	// 1) Canonical Cache（§52/§54）
	if s.useCache && cacheTTL > 0 {
		if e, ok := s.cache.get(key, time.Now()); ok {
			s.cacheHits++
			countCacheHits.Inc()
			countProviderResolution.WithLabelValues("cache", string(StatusCacheHit)).Inc()
			res := s.cacheHitResolution(creq, e)
			s.recordResolution(ctx, creq, res, 0)
			return res
		}
	}
	// 2) Singleflight（§53：100 个相同请求只启动一个 resolve workflow）
	if s.useSingle {
		s.sfMu.Lock()
		if call, ok := s.sf[key]; ok {
			s.sfMu.Unlock()
			select {
			case <-call.done:
				return call.res
			case <-ctx.Done():
				return fail(StatusDeadline, "count cancelled")
			}
		}
		call := &sfRes{done: make(chan struct{})}
		s.sf[key] = call
		s.sfMu.Unlock()
		res := s.resolveLive(ctx, creq, cacheTTL)
		call.res = res
		close(call.done)
		s.sfMu.Lock()
		delete(s.sf, key)
		s.sfMu.Unlock()
		return res
	}
	return s.resolveLive(ctx, creq, cacheTTL)
}

// resolveLive 真正走 Provider 的路径（单飞领导者 / 非单飞时直接执行）。
func (s *Service) resolveLive(ctx context.Context, creq pc.CountRequest, cacheTTL time.Duration) CountResolution {
	fail := func(status ResolutionStatus, reason string) CountResolution {
		return CountResolution{Status: status, Reason: reason, FallbackRecommended: true}
	}
	// 3) Router（§33/§36/§93）
	dec := s.route(ctx, creq.ChainID, creq.Dataset, creq.Purpose)
	if dec.Strategy == StrategyFailOpen || dec.ProviderID == "" {
		countProviderResolution.WithLabelValues("router", string(StatusUnavailable)).Inc()
		res := fail(StatusUnavailable, dec.Reason)
		s.recordResolution(ctx, creq, res, 0)
		return res
	}
	// 4) 全局 Count 预算（§28/§97）
	if s.global != nil {
		select {
		case s.global <- struct{}{}:
			defer func() { <-s.global }()
		case <-ctx.Done():
			res := fail(StatusDeadline, "global count budget wait cancelled")
			s.recordResolution(ctx, creq, res, 0)
			return res
		default:
			res := fail(StatusDeadline, "global count budget exhausted")
			s.recordResolution(ctx, creq, res, 0)
			return res
		}
	}
	ex := s.executors[dec.ProviderID]
	if ex == nil {
		res := fail(StatusUnavailable, "executor missing: "+dec.ProviderID)
		s.recordResolution(ctx, creq, res, 0)
		return res
	}
	// 5) Deadline-aware 准入（§40：queue wait + p95 > 剩余预算 → 直接 Fail-Open，不发网络请求）
	if !creq.Deadline.IsZero() {
		remaining := time.Until(creq.Deadline)
		est := ex.expectedLatency() + time.Duration(ex.queueDepth())*time.Millisecond
		if remaining <= 0 || est > remaining {
			res := fail(StatusDeadline, "provider_queue_deadline: est="+est.String()+" remaining="+remaining.String())
			countProviderResolution.WithLabelValues(dec.ProviderID, string(StatusDeadline)).Inc()
			s.recordResolution(ctx, creq, res, 0)
			return res
		}
	}
	start := time.Now()
	cr := &callReq{ctx: ctx, req: creq, result: make(chan callResult, 1)}
	if err := ex.enqueue(cr); err != nil {
		res := fail(StatusDeadline, "provider queue full")
		s.recordResolution(ctx, creq, res, 0)
		return res
	}
	var out callResult
	select {
	case out = <-cr.result:
	case <-ctx.Done():
		res := fail(StatusDeadline, "count cancelled while waiting provider")
		s.recordResolution(ctx, creq, res, time.Since(start).Milliseconds())
		return res
	}
	latencyMs := time.Since(start).Milliseconds()
	if out.err != nil {
		cls := pc.Class(out.err)
		status := StatusUnavailable
		if cls == pc.ErrDeadline || cls == pc.ErrCancelled || cls == pc.ErrTimeout {
			status = StatusDeadline
		}
		reason := providerReason(out.err)
		res := CountResolution{
			Status: status, Reason: reason, FallbackRecommended: true,
			Attempts: []ProviderAttemptSummary{{ProviderID: dec.ProviderID, Status: "error", ErrorClass: string(cls), LatencyMS: latencyMs, Reason: reason}},
		}
		countProviderResolution.WithLabelValues(dec.ProviderID, string(status)).Inc()
		s.recordResolution(ctx, creq, res, latencyMs)
		return res
	}
	// 成功：校准（§14/§51）+ 缓存写
	res := out.res
	res.Latency = time.Since(start)
	if s.useCalib && res.Count > 0 {
		s.applyCalibration(ctx, res)
	}
	if s.useCache && cacheTTL > 0 && res.Count >= 0 {
		s.cache.set(keyOf(creq), cacheEntry{
			Count: res.Count, ProviderID: res.ProviderID, Confidence: res.Confidence,
			Calibration: 1, LatencyMS: latencyMs, StoredAt: time.Now(),
			ExpiresAt: time.Now().Add(cacheTTL), CostRequests: res.Cost.Requests, Semantics: string(res.Semantics),
		})
	}
	countProviderResolution.WithLabelValues(res.ProviderID, string(StatusResolved)).Inc()
	if res.Count == 0 {
		countProviderZero.WithLabelValues(res.ProviderID).Inc()
	}
	okRes := CountResolution{Status: StatusResolved, Result: res, Reason: "resolved",
		Attempts: []ProviderAttemptSummary{{ProviderID: res.ProviderID, Status: "ok", LatencyMS: latencyMs}}}
	s.recordResolution(ctx, creq, okRes, latencyMs)
	return okRes
}

// cacheHitResolution Canonical Cache 命中 → Resolution（校准最新 ratio 重放，§52）。
func (s *Service) cacheHitResolution(creq pc.CountRequest, e cacheEntry) CountResolution {
	res := pc.CountResult{
		RequestID:  creq.RequestID,
		ProviderID: e.ProviderID,
		ChainID:    creq.ChainID,
		Dataset:    creq.Dataset,
		Subject:    creq.Subject,
		Range:      creq.Range,
		Count:      e.Count,
		Semantics:  pc.CountSemantics(e.Semantics),
		Accuracy:   pc.AccuracyExact,
		Confidence: e.Confidence,
		Source:     pc.SourceCache,
		ObservedAt: e.StoredAt,
		Latency:    time.Duration(e.LatencyMS) * time.Millisecond,
		Cost:       pc.CountCost{Requests: e.CostRequests},
	}
	if s.useCalib && e.Count > 0 {
		s.applyCalibration(context.Background(), &res)
	}
	return CountResolution{Status: StatusCacheHit, Result: &res, Reason: "cache_hit", FallbackRecommended: false}
}

// resolutionToModel Resolution → V1.2 model.CountResult（Planner 兼容）。
func (s *Service) resolutionToModel(ctx context.Context, res CountResolution, r model.CountRequest) model.CountResult {
	switch res.Status {
	case StatusResolved, StatusCacheHit:
		if res.Result == nil {
			return modelFailResult(r, "unknown", "resolution without result", "")
		}
		m := resultToModel(*res.Result, r)
		m.Source = modelSource(*res.Result)
		if res.Status == StatusCacheHit {
			m.CacheHit = true
			m.Source = "cache"
		}
		// V1.2 语义：AdjustedCount = Raw × calibration（只用于 Planning）
		m.Calibration = 1
		if m.RawCount > 0 && s.useCalib {
			cal := s.getCalibrationFor(ctx, m.Provider, r.Chain, r.Dataset)
			if cal != nil && cal.RatioP50 > 0 {
				adjusted := int64(math.Round(float64(m.RawCount) * cal.RatioP50))
				if adjusted >= 0 {
					m.AdjustedCount = adjusted
					m.Calibration = cal.RatioP50
				}
				spread := math.Abs(cal.RatioP95 - cal.RatioP50)
				m.Confidence = clampF(1-spread, 0.2, 1)
			}
		}
		return m
	case StatusUnavailable, StatusDeadline, StatusFailOpen:
		return modelFailResult(r, legacyClassForStatus(res), truncateReason(res.Reason), providerOfResolution(res))
	default:
		return modelFailResult(r, "unknown", truncateReason(res.Reason), providerOfResolution(res))
	}
}

func modelSource(res pc.CountResult) string {
	switch res.Source {
	case pc.SourceCache:
		return "cache"
	case pc.SourceLiveProvider:
		return res.ProviderID
	case "":
		return res.ProviderID
	default:
		return strings.ToLower(string(res.Source))
	}
}

func providerOfResolution(res CountResolution) string {
	if res.Result != nil {
		return res.Result.ProviderID
	}
	if len(res.Attempts) > 0 {
		return res.Attempts[0].ProviderID
	}
	return "chainbase"
}

func legacyClassForStatus(res CountResolution) string {
	switch res.Status {
	case StatusDeadline:
		if len(res.Attempts) > 0 && res.Attempts[0].ErrorClass != "" {
			return mapLegacyClass(res.Attempts[0].ErrorClass)
		}
		return "timeout"
	case StatusUnavailable:
		if len(res.Attempts) > 0 && res.Attempts[0].ErrorClass != "" {
			return mapLegacyClass(res.Attempts[0].ErrorClass)
		}
		return "upstream"
	default:
		return "unknown"
	}
}

func truncateReason(s string) string {
	if len(s) <= 220 {
		return s
	}
	return s[:220] + "..."
}

// mapLegacyClass port ErrorClass → V1.2 model 分类字符串（仅用于决策日志/观测）。
func mapLegacyClass(cls string) string {
	switch cls {
	case string(pc.ErrRateLimit):
		return "rate_limit"
	case string(pc.ErrTimeout):
		return "timeout"
	case string(pc.ErrConnection):
		return "connection"
	case string(pc.ErrAuth):
		return "auth"
	case string(pc.ErrQuota):
		return "quota"
	case string(pc.ErrUnsupported):
		return "unsupported"
	case string(pc.ErrInvalidResponse):
		return "invalid_json"
	case string(pc.ErrCircuitOpen):
		return "circuit_open"
	case string(pc.ErrProvider5xx):
		return "upstream"
	case string(pc.ErrDeadline):
		return "timeout"
	case string(pc.ErrCancelled):
		return "cancelled"
	default:
		return "unknown"
	}
}

// providerReason CountError → 人读原因。
func providerReason(err error) string {
	if err == nil {
		return ""
	}
	if ce, ok := err.(*pc.CountError); ok {
		if ce.Message != "" {
			return string(ce.Class) + ": " + ce.Message
		}
		return string(ce.Class)
	}
	return err.Error()
}

func keyOf(creq pc.CountRequest) string { return creq.RequestID }

// ---- Calibration（§14/§51：provider 维度聚合） ----

func calibMapKey(provider, chain, dataset string) string {
	return provider + "|" + chain + "|" + dataset
}

func (s *Service) getCalibrationFor(ctx context.Context, provider, chain, dataset string) *model.CountCalibration {
	key := calibMapKey(provider, chain, dataset)
	s.calibMu.Lock()
	defer s.calibMu.Unlock()
	if c, ok := s.calib[key]; ok {
		return c
	}
	if s.store == nil {
		return nil
	}
	cal, err := s.store.GetCalibration(ctx, provider, chain, dataset)
	if err != nil || cal == nil || cal.SampleCount == 0 {
		return nil
	}
	s.calib[key] = cal
	return cal
}

// applyCalibration 把最新校准 ratio 应用到规范结果（Confidence 由校准 spread 调整）。
func (s *Service) applyCalibration(ctx context.Context, res *pc.CountResult) {
	if res.Count <= 0 || !s.useCalib {
		return
	}
	chain := s.deploy.chainName(res.ChainID)
	if chain == "" {
		return
	}
	cal := s.getCalibrationFor(ctx, res.ProviderID, chain, string(res.Dataset))
	if cal == nil {
		return
	}
	if cal.RatioP50 > 0 {
		spread := math.Abs(cal.RatioP95 - cal.RatioP50)
		res.Confidence = clampF(1-spread, 0.2, 1)
	}
}

func clampF(v, lo, hi float64) float64 {
	if v < lo {
		return lo
	}
	if v > hi {
		return hi
	}
	return v
}

// ---- 观测 / 校准样本 / Shadow Diff（异步；失败只记日志） ----

// RecordObservation 记录一次观测（§92/§93）；异步写入。
func (s *Service) RecordObservation(obs *model.CountObservation) {
	if s.store == nil {
		return
	}
	if obs.Provider == "" {
		obs.Provider = "chainbase"
	}
	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
		defer cancel()
		if err := s.store.InsertCountObservation(ctx, obs); err != nil {
			s.log.Warn("count observation insert failed", "err", err)
		}
	}()
}

// RecordActual 任务完成后样本（predicted vs actual）→ Calibration（§22/§24/§51）。
func (s *Service) RecordActual(provider, chain, dataset, addressHash string, predicted, actual int64) {
	if s.store == nil {
		return
	}
	if predicted < 0 {
		return
	}
	if actual < 0 {
		actual = 0
	}
	if provider == "" {
		provider = "chainbase"
	}
	s.calibMu.Lock()
	delete(s.calib, calibMapKey(provider, chain, dataset))
	s.calibMu.Unlock()
	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
		defer cancel()
		sample := &model.CalibrationSample{Provider: provider, Chain: chain, Dataset: dataset,
			AddressHash: addressHash, PredictedCount: predicted, ActualCount: actual}
		if err := s.store.InsertCalibrationSample(ctx, sample); err != nil {
			s.log.Warn("calibration sample insert failed", "err", err)
			return
		}
		if _, err := s.store.RefreshCalibration(ctx, provider, chain, dataset, 0); err != nil {
			s.log.Warn("calibration refresh failed", "provider", provider, "chain", chain, "dataset", dataset, "err", err)
		}
	}()
}

// RecordShadowDiff Shadow Diff（§98）——由 Planner 在 Shadow Mode 下调用。
func (s *Service) RecordShadowDiff(ctx context.Context, d *model.ShadowDiff) {
	if s.store == nil || !s.cfg.CountProvider.PortEnabled {
		return
	}
	if !s.cfg.CountProvider.Shadow {
		return
	}
	go func() {
		cctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
		defer cancel()
		if err := s.store.InsertResolutionDecision(cctx, d); err != nil {
			s.log.Warn("shadow diff insert failed", "err", err)
		}
	}()
}

// recordResolution 每次解析结果落 count_resolution_decisions（§66/§98；Port 开启且非 pure-v12）。
func (s *Service) recordResolution(ctx context.Context, creq pc.CountRequest, res CountResolution, latencyMs int64) {
	if s.store == nil || !s.cfg.CountProvider.PortEnabled || !s.cfg.CountProvider.Shadow {
		return
	}
	count := int64(-1)
	provider := providerOfResolution(res)
	if res.Result != nil {
		count = res.Result.Count
	}
	d := &model.ShadowDiff{
		RequestHash: keyOf(creq), ChainID: int64(creq.ChainID), Dataset: string(creq.Dataset),
		SubjectHash: shortHash(creq.Subject.Value),
		RangeStart:  int64(creq.Range.Start), RangeEnd: int64(creq.Range.End),
		Status: string(res.Status), Reason: res.Reason, ProviderID: provider,
		Count: count, LatencyMS: int(latencyMs), FallbackRecommended: res.FallbackRecommended,
		NewSource: provider,
	}
	go func() {
		cctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
		defer cancel()
		if err := s.store.InsertResolutionDecision(cctx, d); err != nil {
			s.log.Warn("resolution decision insert failed", "err", err)
		}
	}()
}

func shortHash(v string) string {
	if len(v) <= 24 {
		return v
	}
	return v[:8] + "..." + v[len(v)-8:]
}

// ---- Attempt 记录 + Metrics 桥（executor → service） ----

func (s *Service) onAttemptHook() func(a attemptRec) {
	if s.store == nil || !s.cfg.CountProvider.RecordAttempts {
		return func(a attemptRec) { s.onAttemptMetrics(a) }
	}
	return func(a attemptRec) {
		s.onAttemptMetrics(a)
		row := &model.CountProviderAttempt{
			RequestHash: a.requestHash, ProviderID: a.providerID, ChainID: int64(a.chainID),
			Dataset: a.dataset, Status: a.status, ErrorClass: a.errorClass,
			LatencyMS: int(a.latencyMs), Count: a.count,
			RequestsCost: a.requestsCost, CreditsCost: a.creditsCost, USDCost: a.usdCost,
		}
		go func() {
			ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
			defer cancel()
			if err := s.store.InsertCountProviderAttempt(ctx, row); err != nil {
				s.log.Warn("count provider attempt insert failed", "err", err)
			}
		}()
	}
}

func (s *Service) onAttemptMetrics(a attemptRec) {
	status := a.status
	if a.errorClass != "" {
		status = a.errorClass
	}
	countProviderRequests.WithLabelValues(a.providerID, a.dataset, status).Inc()
	if a.status == "ok" {
		countProviderValidResults.WithLabelValues(a.providerID, a.dataset).Inc()
		countProviderLatency.WithLabelValues(a.providerID, a.dataset).Observe(float64(a.latencyMs) / 1000)
		countProviderCostCredits.WithLabelValues(a.providerID, a.dataset).Add(a.creditsCost)
	}
	switch a.errorClass {
	case string(pc.ErrRateLimit):
		countProviderRateLimit.WithLabelValues(a.providerID, a.dataset).Inc()
	case string(pc.ErrTimeout), string(pc.ErrDeadline):
		countProviderTimeout.WithLabelValues(a.providerID, a.dataset).Inc()
	}
}

// ---- Run：心跳（Registry/Capabilities/Runtime Profiles/Health 落库，§57/§67/§68） ----

func (s *Service) Run(ctx context.Context) {
	s.startOnce.Do(func() {
		s.regMu.RLock()
		execs := make([]*executor, 0, len(s.executors))
		for _, ex := range s.executors {
			execs = append(execs, ex)
		}
		s.regMu.RUnlock()
		for _, ex := range execs {
			ex.start(ctx)
		}
	})
	ticker := time.NewTicker(5 * time.Second)
	defer ticker.Stop()
	lastTick := time.Now()
	lastCounters := map[string]map[string]int64{}
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			now := time.Now()
			deltaSec := now.Sub(lastTick).Seconds()
			if deltaSec <= 0 {
				deltaSec = 5
			}
			lastTick = now
			s.heartbeat(ctx, now, deltaSec, lastCounters)
		}
	}
}

// heartbeat 周期写：registry / capabilities / runtime profiles / count_oracle_status（V1.2 API 兼容）。
func (s *Service) heartbeat(ctx context.Context, now time.Time, deltaSec float64, last map[string]map[string]int64) {
	s.regMu.RLock()
	defer s.regMu.RUnlock()
	if len(s.providerList) == 0 {
		return
	}
	portOn := s.cfg.CountProvider.PortEnabled
	for _, p := range s.providerList {
		id := p.ID()
		pol := s.policyMap[id]
		ex := s.executors[id]
		polEnabled := pol != nil && pol.Enabled
		snap := ex.snapshot()
		// gauges
		countProviderInflight.WithLabelValues(id).Set(float64(snap["active"].(int)))
		countProviderQueueDepth.WithLabelValues(id).Set(float64(snap["queue_depth"].(int)))
		countProviderSafeConcurrency.WithLabelValues(id).Set(float64(snap["max_concurrency"].(int)))

		if s.store != nil {
			if portOn {
				reg := &model.CountProviderRegistryRow{ProviderID: id, DisplayName: displayName(p), Enabled: polEnabled, AdapterType: "adapter"}
				if err := s.store.UpsertCountProviderRegistry(ctx, reg); err != nil {
					s.log.Warn("provider registry upsert failed", "provider", id, "err", err)
				}
			}
			// count_oracle_status（V1.2 API 兼容行：provider 字段 = provider id）
			healthRow := s.healthRow(ctx, id, snap)
			if err := s.store.UpsertCountOracleHealth(ctx, healthRow); err != nil {
				s.log.Warn("count oracle health write failed", "provider", id, "err", err)
			}
		}
		if !portOn || s.store == nil {
			continue
		}
		// capabilities rows
		for _, capRow := range capabilityRows(p, s.deploy) {
			if err := s.store.UpsertCountProviderCapability(ctx, capRow); err != nil {
				s.log.Warn("provider capability upsert failed", "provider", id, "err", err)
				break
			}
		}
		// runtime profiles（chainbase 按部署 chain 展开，其余 provider chain 0）
		rows := runtimeProfileRows(id, p, snap, deltaSec, last, pol, profileChainIDs(id, s.deploy))
		for _, row := range rows {
			if err := s.store.UpsertCountProviderRuntimeProfile(ctx, row); err != nil {
				s.log.Warn("provider runtime profile upsert failed", "provider", id, "err", err)
				break
			}
		}
	}
}

func displayName(p pc.CountProvider) string {
	if dn, ok := p.(interface{ DisplayName() string }); ok {
		if n := dn.DisplayName(); n != "" {
			return n
		}
	}
	return p.ID()
}

// healthRow count_oracle_status 兼容行。
func (s *Service) healthRow(ctx context.Context, id string, snap map[string]any) *model.CountOracleHealth {
	h := &model.CountOracleHealth{Provider: id}
	state, _ := snap["state"].(string)
	h.CircuitState = mapCircuit(state)
	req, _ := snap["requests"].(int64)
	ok, _ := snap["success"].(int64)
	h.Requests = req
	h.SuccessCount = ok
	if p50, ok := snap["p50_ms"].(int); ok {
		h.P50Ms = p50
	}
	if p95, ok := snap["p95_ms"].(int); ok {
		h.P95Ms = p95
	}
	if sr, ok := snap["success_rate"].(float64); ok {
		h.SuccessRate = sr
	}
	if le, ok := snap["last_error"].(string); ok {
		h.LastError = le
	}
	h.QuotaExhausted = strings.Contains(h.LastError, "quota") || strings.Contains(h.LastError, "QUOTA")
	if s.store != nil {
		_ = s.store.Pool.QueryRow(ctx, "SELECT COALESCE(SUM(false_zero_count),0) FROM count_oracle_calibration").Scan(&h.FalseZeroCount)
		_ = s.store.Pool.QueryRow(ctx,
			"SELECT COALESCE(AVG(absolute_error_p50),0) FROM count_oracle_calibration WHERE sample_count>0").Scan(&h.PredictionErrorP50)
	}
	if id == "chainbase" {
		// 无 Key → 保持 V1.2 语义（NO_KEY 行）
		if s.store != nil {
			_ = s.store.Pool.QueryRow(ctx, "SELECT count(*) FROM count_provider_registry WHERE provider_id='chainbase'").Scan(new(int))
		}
	}
	h.UpdatedAt = time.Now()
	return h
}

// mapCircuit provider runtime state → 旧 circuit_state 文案。
func mapCircuit(state string) string {
	switch state {
	case "", "AVAILABLE":
		return "CLOSED"
	case "DEGRADED", "THROTTLED":
		return "DEGRADED"
	case "BLOCKED":
		return "OPEN"
	case "RECOVERING":
		return "HALF_OPEN"
	case "SHADOW":
		return "CLOSED"
	default:
		return "CLOSED"
	}
}

// capabilityRows provider caps → DB 行（chainbase 按部署 chain 展开，其余 chain 0）。
func capabilityRows(p pc.CountProvider, dep *deployment) []*model.CountProviderCapabilityRow {
	caps := p.Capabilities(context.Background())
	var out []*model.CountProviderCapabilityRow
	chains := []uint64{0}
	if p.ID() == "chainbase" && dep != nil && len(dep.order) > 0 {
		chains = nil
		for _, name := range dep.order {
			chains = append(chains, dep.chainID(name))
		}
	}
	keys := make([]pc.DatasetKey, 0, len(caps.Datasets))
	for k := range caps.Datasets {
		keys = append(keys, k)
	}
	// chain 0 行总是写；具体 chain 行覆盖 chain 0 的 dataset 集合
	for _, chainID := range chains {
		seen := map[string]bool{}
		for _, k := range keys {
			dc := caps.Datasets[k]
			chain := k.ChainID
			if chain == 0 {
				chain = chainID
			}
			dsKey := fmt.Sprintf("%d|%s", chain, k.Dataset)
			if seen[dsKey] {
				continue
			}
			seen[dsKey] = true
			if chain != chainID {
				continue
			}
			out = append(out, &model.CountProviderCapabilityRow{
				ProviderID: p.ID(), ChainID: int64(chain), Dataset: string(k.Dataset),
				Enabled: dc.Enabled, RuntimeStatus: string(dc.RuntimeStatus),
				SupportsRange: dc.SupportsRange, RangeVerified: dc.RangeVerified,
				SupportsBatch: caps.SupportsBatch, MaxBatchSize: caps.MaxBatchSize,
				Semantics: string(dc.Semantics), Accuracy: string(dc.Accuracy), Reason: dc.Reason,
			})
		}
	}
	return out
}

// runtimeProfileRows provider runtime → DB 行（chainIDs 由调用方给出：chainbase=部署 chain，其它=0）。
func runtimeProfileRows(id string, p pc.CountProvider, snap map[string]any, deltaSec float64,
	last map[string]map[string]int64, pol *ProviderPolicy, chainIDs []uint64) []*model.CountProviderRuntimeProfile {

	now := time.Now()
	prev := last[id]
	last[id] = map[string]int64{}
	reqTotal, _ := snap["requests"].(int64)
	okTotal, _ := snap["success"].(int64)
	last[id]["requests"] = reqTotal
	last[id]["success"] = okTotal
	successRPS := diffRate(prev["success"], okTotal, deltaSec)

	state, _ := snap["state"].(string)
	if state == "" {
		state = "UNKNOWN"
	}
	lastErr, _ := snap["last_error"].(string)
	p50, _ := snap["p50_ms"].(int)
	p95, _ := snap["p95_ms"].(int)
	sr, _ := snap["success_rate"].(float64)
	maxConc, _ := snap["max_concurrency"].(int)
	active, _ := snap["active"].(int)
	qd, _ := snap["queue_depth"].(int)

	if len(chainIDs) == 0 {
		chainIDs = []uint64{0}
	}
	caps := p.Capabilities(context.Background())
	var out []*model.CountProviderRuntimeProfile
	for _, chainID := range chainIDs {
		dsSeen := map[string]bool{}
		for k, dc := range caps.Datasets {
			if dsSeen[string(k.Dataset)] {
				continue
			}
			// 具体 chain 能力缺失时回退 chain 0 能力
			cap := dc
			if chainID != 0 {
				if c2, ok := caps.DatasetCap(chainID, k.Dataset); ok {
					cap = c2
				}
			} else if k.ChainID != 0 {
				continue
			}
			dsSeen[string(k.Dataset)] = true
			rowState := state
			if !cap.Enabled || cap.RuntimeStatus == pc.StatusBlocked || cap.RuntimeStatus == pc.StatusDisabled {
				rowState = string(cap.RuntimeStatus)
				if rowState == "" || rowState == "UNKNOWN" {
					rowState = "BLOCKED"
				}
			}
			out = append(out, &model.CountProviderRuntimeProfile{
				ProviderID: id, ChainID: int64(chainID), Dataset: string(k.Dataset),
				State: rowState, Active: active, QueueDepth: qd,
				SafeConcurrency: maxConc, HardCeiling: maxConc,
				SuccessRPS: successRPS, ValidCountRPS: successRPS,
				P50Ms: p50, P95Ms: p95, SuccessRate: sr,
				CostPer1KCounts: costPer1K(pol, successRPS),
				Requests:        reqTotal, SuccessCount: okTotal,
				LastError: lastErr, UpdatedAt: now,
			})
		}
	}
	return out
}

func diffRate(prev, cur int64, deltaSec float64) float64 {
	if cur <= prev || deltaSec <= 0 {
		return 0
	}
	return float64(cur-prev) / deltaSec
}

func costPer1K(pol *ProviderPolicy, validRate float64) float64 {
	if pol == nil || validRate <= 0 {
		return 0
	}
	return pol.CostPerRequestUSD * 1000 / math.Max(validRate, 0.0001)
}

// profileChainIDs runtime profile 行展开的 chain 集合。
func profileChainIDs(id string, dep *deployment) []uint64 {
	if id != "chainbase" || dep == nil || len(dep.order) == 0 {
		return []uint64{0}
	}
	var out []uint64
	for _, name := range dep.order {
		if cid := dep.chainID(name); cid > 0 {
			out = append(out, cid)
		}
	}
	if len(out) == 0 {
		out = []uint64{0}
	}
	return out
}
