package route

import (
	"context"
	"errors"
	"strings"
	"sync"
	"testing"

	"github.com/blockchain-platform/blockchain-platform/internal/provider"
)

// stubProvider 是假的 provider.Provider：记录收到的请求，便于断言分派目标。
type stubProvider struct {
	name     string
	caps     provider.Capabilities
	health   provider.HealthStatus
	fetchErr error

	mu    sync.Mutex
	calls []provider.FetchRequest
}

func (s *stubProvider) Name() string { return s.name }

func (s *stubProvider) Capabilities() provider.Capabilities {
	caps := s.caps
	if caps.Name == "" {
		caps.Name = s.name
	}
	return caps
}

func (s *stubProvider) Health(context.Context) provider.HealthStatus {
	status := s.health
	if status.Provider == "" {
		status.Provider = s.name
	}
	if status.State == "" {
		status.State = provider.HealthNormal
	}
	return status
}

func (s *stubProvider) Fetch(_ context.Context, req provider.FetchRequest) (provider.FetchResult, error) {
	s.mu.Lock()
	s.calls = append(s.calls, req)
	s.mu.Unlock()
	if s.fetchErr != nil {
		return provider.FetchResult{}, s.fetchErr
	}
	// 用 RawResponseBody 回带 Provider 名，测试无需 JSON 解析即可断言分派目标。
	return provider.FetchResult{RawResponseBody: []byte(s.name)}, nil
}

func (s *stubProvider) callCount() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return len(s.calls)
}

type stubProfileSource struct {
	profile Profile
	ok      bool

	mu    sync.Mutex
	calls int
}

func (s *stubProfileSource) PerformanceProfile(_ context.Context, providerKey, chain, dataset, operation, mode string) (Profile, bool) {
	s.mu.Lock()
	s.calls++
	s.mu.Unlock()
	if !s.ok {
		return Profile{}, false
	}
	profile := s.profile
	profile.ProviderKey, profile.Chain, profile.Dataset = providerKey, chain, dataset
	profile.OperationClass, profile.Mode = operation, mode
	return profile, true
}

func TestBuiltinRoutesMatchFrozenTable(t *testing.T) {
	registry := NewRegistry(nil)
	registry.SetSolanaRoute(true, ProviderDRPC)

	cases := []struct {
		chain, dataset, providerKey, channel, operation string
	}{
		{ChainSolana, DatasetTransactions, ProviderDRPC, ChannelDataAPI, OperationSolanaHistory},
		{ChainSolana, DatasetTokenTransfers, ProviderDRPC, ChannelDataAPI, OperationSolanaHistory},
		{ChainSolana, DatasetNFTTransfers, ProviderDRPC, ChannelDataAPI, OperationSolanaHistory},
		{ChainSolana, DatasetBalances, ProviderDRPC, ChannelSolanaRPC, OperationSolanaBalance},
		{ChainSolana, DatasetLogs, ProviderDRPC, ChannelSolanaRPC, OperationSolanaDetail},
		{"ethereum", DatasetTransactions, ProviderAnkr, ChannelAnkrAdvanced, OperationEVMHistory},
		{"bsc", DatasetTokenTransfers, ProviderAnkr, ChannelAnkrAdvanced, OperationEVMHistory},
		{"bitcoin", DatasetLogs, ProviderAnkr, ChannelAnkrAdvanced, OperationEVMHistory},
	}
	for _, tc := range cases {
		got, ok := registry.Resolve(tc.chain, tc.dataset)
		if !ok {
			t.Fatalf("Resolve(%s, %s) missed the builtin table", tc.chain, tc.dataset)
		}
		if got.ProviderKey != tc.providerKey || got.APIChannel != tc.channel || got.OperationClass != tc.operation {
			t.Fatalf("Resolve(%s, %s) = %+v, want provider=%s channel=%s operation=%s",
				tc.chain, tc.dataset, got, tc.providerKey, tc.channel, tc.operation)
		}
		if got.PolicyRevision != 0 || !strings.HasPrefix(got.PolicyKey, "builtin:") {
			t.Fatalf("builtin route must be marked as builtin with revision 0: %+v", got)
		}
	}

	// 大小写与空白不影响匹配（下载参数常来自用户输入或历史数据）。
	got, ok := registry.Resolve(" SOLANA ", " Transactions ")
	if !ok || got.OperationClass != OperationSolanaHistory {
		t.Fatalf("case-insensitive resolve failed: %+v ok=%v", got, ok)
	}

	// 未命中：Solana 的 internal_transactions 不在规范表内；空链/空数据集一律不猜。
	for _, miss := range [][2]string{
		{ChainSolana, DatasetInternalTransactions},
		{ChainSolana, "unknown_dataset"},
		{"", DatasetTransactions},
		{"ethereum", ""},
	} {
		if route, ok := registry.Resolve(miss[0], miss[1]); ok {
			t.Fatalf("Resolve(%q, %q) unexpectedly hit: %+v", miss[0], miss[1], route)
		}
	}
}

func TestSolanaBuiltinRouteIsGatedBySwitch(t *testing.T) {
	registry := NewRegistry(nil)

	// 默认关闭：dRPC 未启用时 Solana 必须未命中，SS 调用方才能回退 provider/solana。
	for _, dataset := range []string{DatasetTransactions, DatasetTokenTransfers, DatasetNFTTransfers, DatasetBalances, DatasetLogs} {
		if route, ok := registry.Resolve(ChainSolana, dataset); ok {
			t.Fatalf("solana route must be off by default, got %+v", route)
		}
	}

	registry.SetSolanaRoute(true, "")
	if route, ok := registry.Resolve(ChainSolana, DatasetTransactions); !ok || route.ProviderKey != ProviderDRPC {
		t.Fatalf("empty provider key must default to drpc: %+v ok=%v", route, ok)
	}

	registry.SetSolanaRoute(true, "Solana-RPC")
	route, ok := registry.Resolve(ChainSolana, DatasetLogs)
	if !ok || route.ProviderKey != "solana-rpc" {
		t.Fatalf("provider key override was not applied: %+v ok=%v", route, ok)
	}

	registry.SetSolanaRoute(false, ProviderDRPC)
	if route, ok := registry.Resolve(ChainSolana, DatasetTransactions); ok {
		t.Fatalf("disabling the switch must make solana miss again, got %+v", route)
	}
}

func TestTronBuiltinRouteNeedsInjectedRouter(t *testing.T) {
	registry := NewRegistry(nil)
	if route, ok := registry.Resolve(ChainTron, DatasetTransactions); ok {
		t.Fatalf("tron must miss without an injected router, got %+v", route)
	}

	registry.SetTronRouter(func(string) (string, string) { return ProviderTronGrid, "" })
	for _, dataset := range []string{DatasetTransactions, DatasetTokenTransfers, DatasetInternalTransactions} {
		route, ok := registry.Resolve(ChainTron, dataset)
		if !ok {
			t.Fatalf("tron %s must resolve once the router is injected", dataset)
		}
		if route.ProviderKey != ProviderTronGrid || route.APIChannel != ChannelTronGrid || route.OperationClass != OperationTronHistory {
			t.Fatalf("tron %s route = %+v", dataset, route)
		}
	}

	// providerKey=ankr 时通道必须是 ankr_tron_node（内置表的 trongrid/ankr_tron_node 二选一）。
	registry.SetTronRouter(func(string) (string, string) { return ProviderAnkr, "" })
	if route, _ := registry.Resolve(ChainTron, DatasetTokenTransfers); route.APIChannel != ChannelAnkrTronNode {
		t.Fatalf("ankr tron route channel = %q, want %q", route.APIChannel, ChannelAnkrTronNode)
	}

	// 注入方显式给出通道时以它为准；未知 Provider 且未给通道时留空，不臆造。
	registry.SetTronRouter(func(dataset string) (string, string) {
		if dataset == DatasetTokenTransfers {
			return "ankr", "ankr_tron_advanced"
		}
		return "custom-tron", ""
	})
	if route, _ := registry.Resolve(ChainTron, DatasetTokenTransfers); route.APIChannel != "ankr_tron_advanced" {
		t.Fatalf("explicit channel was overridden: %+v", route)
	}
	if route, _ := registry.Resolve(ChainTron, DatasetTransactions); route.ProviderKey != "custom-tron" || route.APIChannel != "" {
		t.Fatalf("unknown tron provider must keep an empty channel: %+v", route)
	}

	// TRON 不在规范表内的数据集仍然未命中（保持现有 TRON 分支）。
	if route, ok := registry.Resolve(ChainTron, DatasetLogs); ok {
		t.Fatalf("tron logs is not in the builtin table, got %+v", route)
	}
}

func TestLoadPoliciesOverrideBuiltin(t *testing.T) {
	registry := NewRegistry(nil)
	registry.SetSolanaRoute(true, ProviderDRPC)

	registry.LoadPolicies([]PolicyRow{{
		PolicyKey: "solana:transactions", Chain: "Solana", Dataset: "Transactions", Enabled: true,
		PrimaryProvider: "DRPC-Canary", APIChannel: ChannelDataAPI, OperationClass: OperationSolanaHistory,
		Revision: 42,
	}})
	route, ok := registry.Resolve(ChainSolana, DatasetTransactions)
	if !ok {
		t.Fatal("database policy row must resolve")
	}
	if route.ProviderKey != "drpc-canary" || route.PolicyKey != "solana:transactions" || route.PolicyRevision != 42 {
		t.Fatalf("policy row was not applied (revision must be transparent): %+v", route)
	}
	// 未被子策略覆盖的行继续走内置表。
	if other, ok := registry.Resolve(ChainSolana, DatasetBalances); !ok || other.PolicyRevision != 0 {
		t.Fatalf("untouched dataset must fall back to the builtin table: %+v ok=%v", other, ok)
	}

	// 显式停用必须 fail closed，而不是回落到内置表（否则运维以为已经停掉了）。
	registry.LoadPolicies([]PolicyRow{{
		PolicyKey: "solana:transactions", Chain: ChainSolana, Dataset: DatasetTransactions,
		Enabled: false, PrimaryProvider: ProviderDRPC, Revision: 43,
	}})
	if route, ok := registry.Resolve(ChainSolana, DatasetTransactions); ok {
		t.Fatalf("disabled policy row must not fall back to the builtin table, got %+v", route)
	}

	// 缺少 PrimaryProvider 的行同样 fail closed。
	registry.LoadPolicies([]PolicyRow{{
		PolicyKey: "solana:transactions", Chain: ChainSolana, Dataset: DatasetTransactions,
		Enabled: true, PrimaryProvider: "", Revision: 44,
	}})
	if route, ok := registry.Resolve(ChainSolana, DatasetTransactions); ok {
		t.Fatalf("policy row without provider must miss, got %+v", route)
	}

	// 通道/操作类缺失且 Provider 与内置一致时用内置补齐；Provider 不同则不臆造通道。
	registry.LoadPolicies([]PolicyRow{
		{PolicyKey: "solana:balances", Chain: ChainSolana, Dataset: DatasetBalances, Enabled: true, PrimaryProvider: ProviderDRPC, Revision: 7},
		{PolicyKey: "evm:logs", Chain: "bsc", Dataset: DatasetLogs, Enabled: true, PrimaryProvider: "other-evm", Revision: 8},
	})
	completed, _ := registry.Resolve(ChainSolana, DatasetBalances)
	if completed.APIChannel != ChannelSolanaRPC || completed.OperationClass != OperationSolanaBalance || completed.PolicyRevision != 7 {
		t.Fatalf("missing channel/operation must be completed from the builtin row: %+v", completed)
	}
	incomplete, _ := registry.Resolve("bsc", DatasetLogs)
	if incomplete.APIChannel != "" || incomplete.OperationClass != "" {
		t.Fatalf("channel must not be copied across providers: %+v", incomplete)
	}

	// dataset 通配行覆盖该链的所有数据集；同 key 多行时 revision 大者胜出。
	registry.LoadPolicies([]PolicyRow{
		{PolicyKey: "evm:wildcard:old", Chain: "ethereum", Dataset: "", Enabled: true, PrimaryProvider: "old-evm", Revision: 5},
		{PolicyKey: "evm:wildcard:new", Chain: "ethereum", Dataset: "*", Enabled: true, PrimaryProvider: "new-evm", Revision: 9},
	})
	wildcard, ok := registry.Resolve("ethereum", DatasetTokenTransfers)
	if !ok || wildcard.ProviderKey != "new-evm" || wildcard.PolicyRevision != 9 || wildcard.PolicyKey != "evm:wildcard:new" {
		t.Fatalf("wildcard/revision selection failed: %+v ok=%v", wildcard, ok)
	}
	// 精确行优先于通配行。
	registry.LoadPolicies([]PolicyRow{
		{PolicyKey: "evm:wildcard", Chain: "ethereum", Dataset: "*", Enabled: true, PrimaryProvider: "wildcard-evm", Revision: 9},
		{PolicyKey: "evm:transactions", Chain: "ethereum", Dataset: DatasetTransactions, Enabled: true, PrimaryProvider: "exact-evm", Revision: 4},
	})
	exact, _ := registry.Resolve("ethereum", DatasetTransactions)
	if exact.ProviderKey != "exact-evm" || exact.PolicyRevision != 4 {
		t.Fatalf("exact policy row must beat the wildcard row: %+v", exact)
	}

	// 清空策略快照后回到内置表。
	registry.LoadPolicies(nil)
	back, ok := registry.Resolve("ethereum", DatasetTransactions)
	if !ok || back.ProviderKey != ProviderAnkr || back.PolicyRevision != 0 {
		t.Fatalf("clearing policies must restore the builtin table: %+v ok=%v", back, ok)
	}
}

func TestFetchDispatchesToResolvedProvider(t *testing.T) {
	drpcStub := &stubProvider{name: "drpc"}
	ankrStub := &stubProvider{name: "ankr"}
	solanaStub := &stubProvider{name: "solana-rpc"}
	fallbackStub := &stubProvider{name: "fallback"}

	registry := NewRegistry(nil)
	registry.Register(ProviderDRPC, "dRPC", []string{ChannelDataAPI, ChannelSolanaRPC}, drpcStub)
	registry.Register(ProviderAnkr, "Ankr", []string{ChannelAnkrAdvanced}, ankrStub)
	registry.Register("solana-rpc", "Solana JSON-RPC", []string{ChannelSolanaRPC, ChannelDataAPI}, solanaStub)
	registry.SetFallback(fallbackStub)
	registry.SetSolanaRoute(true, ProviderDRPC)

	ctx := context.Background()
	result, err := registry.Fetch(ctx, provider.FetchRequest{Chain: ChainSolana, Dataset: DatasetTransactions})
	if err != nil || string(result.RawResponseBody) != "drpc" {
		t.Fatalf("solana history must dispatch to drpc: %q %v", result.RawResponseBody, err)
	}
	if drpcStub.callCount() != 1 || fallbackStub.callCount() != 0 {
		t.Fatalf("unexpected dispatch counts: drpc=%d fallback=%d", drpcStub.callCount(), fallbackStub.callCount())
	}

	if result, err = registry.Fetch(ctx, provider.FetchRequest{Chain: "ethereum", Dataset: DatasetTransactions}); err != nil || string(result.RawResponseBody) != "ankr" {
		t.Fatalf("evm history must dispatch to ankr: %q %v", result.RawResponseBody, err)
	}

	// 未命中（tron 未注入 router）回退 fallback。
	if result, err = registry.Fetch(ctx, provider.FetchRequest{Chain: ChainTron, Dataset: DatasetTransactions}); err != nil || string(result.RawResponseBody) != "fallback" {
		t.Fatalf("miss must fall back: %q %v", result.RawResponseBody, err)
	}
	if fallbackStub.callCount() != 1 {
		t.Fatalf("fallback count = %d, want 1", fallbackStub.callCount())
	}

	// 路由命中但 Provider 未注册：仍然回退 fallback，绝不改投另一个已注册 Provider。
	registry.LoadPolicies([]PolicyRow{{
		PolicyKey: "evm:transactions", Chain: "ethereum", Dataset: DatasetTransactions, Enabled: true,
		PrimaryProvider: "not-registered", APIChannel: ChannelAnkrAdvanced, OperationClass: OperationEVMHistory, Revision: 11,
	}})
	if result, err = registry.Fetch(ctx, provider.FetchRequest{Chain: "ethereum", Dataset: DatasetTransactions}); err != nil || string(result.RawResponseBody) != "fallback" {
		t.Fatalf("unregistered routed provider must fall back: %q %v", result.RawResponseBody, err)
	}
	if ankrStub.callCount() != 1 {
		t.Fatalf("ankr must not serve a route that resolves elsewhere, calls = %d", ankrStub.callCount())
	}

	// 没有 fallback 时 fail closed，并且错误必须是结构化 unsupported。
	bare := NewRegistry(nil)
	_, err = bare.Fetch(ctx, provider.FetchRequest{Chain: ChainTron, Dataset: DatasetTransactions})
	var providerErr *provider.Error
	if !errors.As(err, &providerErr) || providerErr.Class != provider.ClassUnsupported {
		t.Fatalf("expected a structured unsupported error, got %v", err)
	}
}

func TestFetchPropagatesProviderError(t *testing.T) {
	want := &provider.Error{Provider: "drpc", Class: provider.ClassQuota, Message: "DRPC_QUOTA_EXHAUSTED"}
	stub := &stubProvider{name: "drpc", fetchErr: want}
	registry := NewRegistry(nil)
	registry.Register(ProviderDRPC, "dRPC", nil, stub)
	registry.SetFallback(&stubProvider{name: "fallback"})
	registry.SetSolanaRoute(true, ProviderDRPC)

	_, err := registry.Fetch(context.Background(), provider.FetchRequest{Chain: ChainSolana, Dataset: DatasetTransactions})
	if !errors.Is(err, want) {
		t.Fatalf("provider error must pass through unchanged, got %v", err)
	}
}

func TestHealthPrefersFallback(t *testing.T) {
	registry := NewRegistry(nil)
	registry.Register(ProviderDRPC, "dRPC", nil, &stubProvider{name: "drpc"})

	status := registry.Health(context.Background())
	if status.Provider != Name || status.State != provider.HealthNormal || status.LastCheck.IsZero() {
		t.Fatalf("health without fallback = %+v, want route/NORMAL with a timestamp", status)
	}

	fallback := &stubProvider{name: "ankr", health: provider.HealthStatus{State: provider.HealthDegraded, ConsecutiveFailure: 3}}
	registry.SetFallback(fallback)
	status = registry.Health(context.Background())
	if status.Provider != "ankr" || status.State != provider.HealthDegraded || status.ConsecutiveFailure != 3 {
		t.Fatalf("health must come from the fallback provider: %+v", status)
	}
}

func TestCapabilitiesFollowFallbackOrUnion(t *testing.T) {
	registry := NewRegistry(nil)
	registry.Register(ProviderDRPC, "dRPC", nil, &stubProvider{name: "drpc", caps: provider.Capabilities{Datasets: []string{DatasetLogs}, MaxPageSize: 1000}})
	registry.Register(ProviderAnkr, "Ankr", nil, &stubProvider{name: "ankr", caps: provider.Capabilities{Datasets: []string{DatasetTransactions, DatasetTokenTransfers}, MaxPageSize: 500}})

	union := registry.Capabilities()
	if union.Name != Name || union.MaxPageSize != 1000 {
		t.Fatalf("union capabilities = %+v", union)
	}
	want := []string{DatasetLogs, DatasetTokenTransfers, DatasetTransactions}
	if strings.Join(union.Datasets, ",") != strings.Join(want, ",") {
		t.Fatalf("union datasets = %v, want %v (sorted, deduped)", union.Datasets, want)
	}

	fallback := &stubProvider{name: "fallback", caps: provider.Capabilities{Name: "fallback", Datasets: []string{DatasetTransactions}, MaxPageSize: 100}}
	registry.SetFallback(fallback)
	if got := registry.Capabilities(); got.Name != "fallback" || got.MaxPageSize != 100 {
		t.Fatalf("capabilities must come from the fallback provider: %+v", got)
	}
}

func TestGetAndList(t *testing.T) {
	registry := NewRegistry(nil)
	if _, ok := registry.Get("drpc"); ok {
		t.Fatal("Get must miss before Register")
	}
	registry.Register("DRPC", "", []string{ChannelDataAPI, "DATA_API", ""}, &stubProvider{name: "drpc"})
	registry.Register(ProviderAnkr, "Ankr", nil, &stubProvider{name: "ankr"})
	registry.Register("", "broken", nil, &stubProvider{name: "broken"})
	registry.Register("nil-provider", "broken", nil, nil)

	if _, ok := registry.Get(" drpc "); !ok {
		t.Fatal("Get must normalize the provider key")
	}
	if _, ok := registry.Get("broken"); ok {
		t.Fatal("registrations without key or implementation must be ignored")
	}

	list := registry.List()
	if len(list) != 2 || list[0].Key != ProviderAnkr || list[1].Key != ProviderDRPC {
		t.Fatalf("List must be sorted by key: %+v", list)
	}
	if list[1].DisplayName != ProviderDRPC {
		t.Fatalf("empty display name must fall back to the key: %+v", list[1])
	}
	if len(list[1].Channels) != 1 || list[1].Channels[0] != ChannelDataAPI {
		t.Fatalf("channels must be normalized and deduped: %v", list[1].Channels)
	}

	// List 返回副本：调用方改 snapshot 不能影响注册表。
	list[1].Channels[0] = "tampered"
	again := registry.List()
	if again[1].Channels[0] != ChannelDataAPI {
		t.Fatalf("List leaked internal state: %v", again[1].Channels)
	}
}

func TestProfileFallsBackFromSourceToBuiltin(t *testing.T) {
	registry := NewRegistry(nil)
	ctx := context.Background()

	// 数据库命中：原样返回，revision 来自数据库。
	source := &stubProfileSource{ok: true, profile: Profile{ShardConcurrency: 9, RequestConcurrency: 9, Revision: 77, VerificationSource: "load-test"}}
	registry.SetProfileSource(source)
	got, ok := registry.Profile(ctx, ProviderDRPC, ChainSolana, DatasetTransactions, OperationSolanaHistory, ModeFast)
	if !ok || got.Revision != 77 || got.ShardConcurrency != 9 || got.VerificationSource != "load-test" {
		t.Fatalf("database profile must win: %+v ok=%v", got, ok)
	}
	if source.calls != 1 {
		t.Fatalf("profile source calls = %d, want 1", source.calls)
	}

	// 数据库未命中：回退内置矩阵。
	source.ok = false
	got, ok = registry.Profile(ctx, ProviderDRPC, ChainSolana, DatasetTransactions, OperationSolanaHistory, ModeFast)
	if !ok {
		t.Fatal("builtin profile must be found after a source miss")
	}
	if got != defaultProfiles[1] {
		t.Fatalf("builtin profile = %+v, want %+v", got, defaultProfiles[1])
	}
	if source.calls != 2 {
		t.Fatalf("source must be consulted before the builtin table, calls = %d", source.calls)
	}

	registry.SetProfileSource(nil)
	if _, ok := registry.Profile(ctx, ProviderDRPC, ChainSolana, DatasetTransactions, OperationSolanaHistory, ModeFast); !ok {
		t.Fatal("clearing the source must keep the builtin matrix usable")
	}

	// 未知组合一律未命中，不猜档位。
	for _, miss := range []struct{ providerKey, chain, dataset, operation, mode string }{
		{"unknown", ChainSolana, DatasetTransactions, OperationSolanaHistory, ModeFast},
		{ProviderDRPC, "ethereum", DatasetTransactions, OperationSolanaHistory, ModeFast},
		{ProviderDRPC, ChainSolana, DatasetTransactions, "unknown_operation", ModeFast},
		{ProviderDRPC, ChainSolana, DatasetTransactions, OperationSolanaHistory, "extreme"},
		{"", ChainSolana, DatasetTransactions, OperationSolanaHistory, ModeFast},
	} {
		if profile, ok := registry.Profile(ctx, miss.providerKey, miss.chain, miss.dataset, miss.operation, miss.mode); ok {
			t.Fatalf("Profile(%+v) unexpectedly hit: %+v", miss, profile)
		}
	}
}

func TestDefaultProfileMatrixMatchesSection62(t *testing.T) {
	profiles := DefaultProfiles()
	if len(profiles) != 9 {
		t.Fatalf("DefaultProfiles() returned %d rows, want 9 (3 operations x 3 modes)", len(profiles))
	}

	type dataAPIWant struct {
		shard, request         int
		rpm, requestsPerSecond float64
		cu                     int64
		pageSize, weight       int
		timeout, hardCeiling   int
	}
	dataAPI := map[string]dataAPIWant{
		ModeSafe:  {1, 2, 10, 10.0 / 60, 18370, 100, 4, 60, 8},
		ModeFast:  {2, 4, 30, 0.5, 55110, 100, 4, 60, 8},
		ModeTurbo: {4, 8, 60, 1, 110220, 100, 4, 60, 8},
	}
	for mode, want := range dataAPI {
		profile, ok := lookupDefaultProfile(ProviderDRPC, ChainSolana, DatasetTransactions, OperationSolanaHistory, mode)
		if !ok {
			t.Fatalf("missing data_api %s profile", mode)
		}
		if profile.ShardConcurrency != want.shard || profile.RequestConcurrency != want.request ||
			profile.RequestsPerMinute != want.rpm || profile.RequestsPerSecond != want.requestsPerSecond ||
			profile.CUPerMinute != want.cu || profile.PageSize != want.pageSize ||
			profile.RequestWeight != want.weight || profile.BatchSize != 1 ||
			profile.RequestTimeoutSeconds != want.timeout || profile.HardCeiling != want.hardCeiling {
			t.Fatalf("data_api %s profile = %+v, want %+v", mode, profile, want)
		}
		if profile.OperationClass != OperationSolanaHistory || profile.Dataset != wildcard {
			t.Fatalf("data_api %s profile identity = %+v", mode, profile)
		}
	}

	rpc := map[string]struct {
		request, pageSize, batchSize, timeout, hardCeiling int
		rps, rpm                                           float64
		cu                                                 int64
	}{
		ModeSafe:  {8, 100, 1, 30, 48, 20, 1200, 24000},
		ModeFast:  {16, 100, 1, 30, 48, 40, 2400, 48000},
		ModeTurbo: {32, 100, 1, 30, 48, 80, 4800, 96000},
	}
	for mode, want := range rpc {
		for _, operation := range []string{OperationSolanaBalance, OperationSolanaDetail} {
			profile, ok := lookupDefaultProfile(ProviderDRPC, ChainSolana, DatasetLogs, operation, mode)
			if !ok {
				t.Fatalf("missing rpc %s/%s profile", operation, mode)
			}
			if profile.RequestConcurrency != want.request || profile.RequestsPerSecond != want.rps ||
				profile.RequestsPerMinute != want.rpm || profile.CUPerMinute != want.cu ||
				profile.PageSize != want.pageSize || profile.BatchSize != want.batchSize ||
				profile.RequestWeight != 1 || profile.RequestTimeoutSeconds != want.timeout ||
				profile.HardCeiling != want.hardCeiling || profile.ShardConcurrency != 0 {
				t.Fatalf("rpc %s/%s profile = %+v, want %+v", operation, mode, profile, want)
			}
		}
	}

	// 内置行必须自洽：RPM 与 RPS 是同一个速率，CU 是名义额度（正数）。
	for _, profile := range profiles {
		if profile.Revision != 1 || profile.VerificationSource != "initial" || profile.VerifiedAt != nil {
			t.Fatalf("builtin profile metadata = %+v", profile)
		}
		if profile.RequestsPerMinute <= 0 || profile.RequestsPerSecond <= 0 || profile.CUPerMinute <= 0 {
			t.Fatalf("builtin profile must carry nominal limits: %+v", profile)
		}
		if diff := profile.RequestsPerMinute - profile.RequestsPerSecond*60; diff > 0.0001 || diff < -0.0001 {
			t.Fatalf("RPM and RPS disagree: %+v", profile)
		}
	}

	// 返回值是副本：修改调用方持有的切片不影响内置表。
	profiles[0].RequestConcurrency = 999
	if again := DefaultProfiles(); again[0].RequestConcurrency == 999 {
		t.Fatal("DefaultProfiles must return a copy")
	}
}

func TestProfileNormalizesModeAndDatasetWildcard(t *testing.T) {
	registry := NewRegistry(nil)
	ctx := context.Background()

	// 空模式按 FAST（初始矩阵通过前 dRPC 默认 FAST）。
	empty, ok := registry.Profile(ctx, ProviderDRPC, ChainSolana, DatasetTransactions, OperationSolanaHistory, "")
	if !ok || empty.Mode != ModeFast {
		t.Fatalf("empty mode must default to fast: %+v ok=%v", empty, ok)
	}
	upper, ok := registry.Profile(ctx, "DRPC", "SOLANA", DatasetTransactions, "SOLANA_HISTORY", "TURBO")
	if !ok || upper.Mode != ModeTurbo || upper.OperationClass != OperationSolanaHistory {
		t.Fatalf("mode/args must be normalized: %+v ok=%v", upper, ok)
	}
	// dataset 通配：transactions / token_transfers / nft_transfers 共用同一档位。
	for _, dataset := range []string{DatasetTransactions, DatasetTokenTransfers, DatasetNFTTransfers, "new_dataset"} {
		profile, ok := registry.Profile(ctx, ProviderDRPC, ChainSolana, dataset, OperationSolanaHistory, ModeSafe)
		if !ok || profile.Dataset != wildcard {
			t.Fatalf("dataset %s must match the wildcard profile: %+v ok=%v", dataset, profile, ok)
		}
	}
}

func TestRegistryIsConcurrencySafe(t *testing.T) {
	registry := NewRegistry(nil)
	registry.SetSolanaRoute(true, ProviderDRPC)
	registry.Register(ProviderDRPC, "dRPC", []string{ChannelDataAPI}, &stubProvider{name: "drpc"})
	registry.Register(ProviderAnkr, "Ankr", nil, &stubProvider{name: "ankr"})
	registry.SetFallback(&stubProvider{name: "fallback"})
	registry.SetTronRouter(func(string) (string, string) { return ProviderTronGrid, "" })
	registry.SetProfileSource(&stubProfileSource{})

	ctx := context.Background()
	var wg sync.WaitGroup
	for i := 0; i < 8; i++ {
		wg.Add(1)
		go func(id int) {
			defer wg.Done()
			for n := 0; n < 50; n++ {
				registry.Resolve(ChainSolana, DatasetTransactions)
				registry.Resolve(ChainTron, DatasetTokenTransfers)
				registry.LoadPolicies([]PolicyRow{{
					PolicyKey: "solana:transactions", Chain: ChainSolana, Dataset: DatasetTransactions,
					Enabled: id%2 == 0, PrimaryProvider: ProviderDRPC, APIChannel: ChannelDataAPI,
					OperationClass: OperationSolanaHistory, Revision: int64(n),
				}})
				registry.Profile(ctx, ProviderDRPC, ChainSolana, DatasetTransactions, OperationSolanaHistory, ModeFast)
				registry.Get(ProviderDRPC)
				registry.List()
				registry.Capabilities()
				registry.Health(ctx)
				if _, err := registry.Fetch(ctx, provider.FetchRequest{Chain: ChainSolana, Dataset: DatasetLogs}); err != nil {
					t.Errorf("concurrent Fetch failed: %v", err)
					return
				}
				registry.LoadPolicies(nil)
			}
		}(i)
	}
	wg.Wait()

	// 并发结束后内置表必须仍然完整（LoadPolicies(nil) 清空的是数据库策略，不是内置表）。
	if route, ok := registry.Resolve(ChainSolana, DatasetBalances); !ok || route.APIChannel != ChannelSolanaRPC {
		t.Fatalf("registry state corrupted by concurrent access: %+v ok=%v", route, ok)
	}
}
