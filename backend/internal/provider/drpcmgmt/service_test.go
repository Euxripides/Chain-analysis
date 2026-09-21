package drpcmgmt

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/blockchain-platform/blockchain-platform/internal/secret"
	"github.com/blockchain-platform/blockchain-platform/internal/store"
	"github.com/shopspring/decimal"
)

// ---- 测试替身 ----

type resolverFunc func(ctx context.Context, ref string) (string, error)

func (f resolverFunc) Resolve(ctx context.Context, ref string) (string, error) { return f(ctx, ref) }
func (f resolverFunc) Describe(context.Context, string) secret.RefStatus       { return secret.RefStatus{} }

func staticResolver(tokens map[string]string) secret.Resolver {
	return resolverFunc(func(_ context.Context, ref string) (string, error) {
		if value, ok := tokens[ref]; ok && value != "" {
			return value, nil
		}
		return "", fmt.Errorf("resolve %s: %w", ref, secret.ErrNotConfigured)
	})
}

func discardLogger() *slog.Logger {
	return slog.New(slog.NewTextHandler(io.Discard, nil))
}

type fakeStore struct {
	mu       sync.Mutex
	inserted []store.AccountSnapshotRow
	latest   *store.AccountSnapshotRow
}

func (f *fakeStore) InsertAccountSnapshot(_ context.Context, snap store.AccountSnapshotRow) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.inserted = append(f.inserted, snap)
	return nil
}

func (f *fakeStore) LatestAccountSnapshot(_ context.Context, _ string) (*store.AccountSnapshotRow, bool, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.latest == nil {
		return nil, false, nil
	}
	return f.latest, true, nil
}

func (f *fakeStore) rows() []store.AccountSnapshotRow {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]store.AccountSnapshotRow(nil), f.inserted...)
}

type recordingServer struct {
	*httptest.Server

	mu    sync.Mutex
	calls []string
	times []time.Time
}

func newRecordingServer(handler func(w http.ResponseWriter, r *http.Request)) *recordingServer {
	server := &recordingServer{}
	server.Server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		server.mu.Lock()
		server.calls = append(server.calls, r.URL.RequestURI())
		server.times = append(server.times, time.Now())
		server.mu.Unlock()
		handler(w, r)
	}))
	return server
}

func (s *recordingServer) callCount() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return len(s.calls)
}

func (s *recordingServer) recordedCalls() []string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return append([]string(nil), s.calls...)
}

func (s *recordingServer) recordedTimes() []time.Time {
	s.mu.Lock()
	defer s.mu.Unlock()
	return append([]time.Time(nil), s.times...)
}

func newTestService(opts Options, st snapshotStore) *Service {
	if opts.StatisticsTokenRef == "" {
		opts.StatisticsTokenRef = "env://STAT"
	}
	if opts.KeysTokenRef == "" {
		opts.KeysTokenRef = "env://KEYS"
	}
	if opts.Secrets == nil {
		opts.Secrets = staticResolver(map[string]string{
			"env://STAT": "stat-token",
			"env://KEYS": "keys-token",
		})
	}
	if opts.Log == nil {
		opts.Log = discardLogger()
	}
	opts.Enabled = true
	return newService(opts, st, nil)
}

// ---- 固定响应样例 ----

const runtimeKeyValue = "drpc_live_runtime_key_0123456789abcdef"

const hourMetricsBody = `{
  "data": {
    "balance": "12.345000000000000000",
    "balance_unit": "provider_unit",
    "tier": "paid",
    "updated_at": "2026-09-21T02:00:00Z",
    "request_count": 40,
    "cu": 73680
  }
}`

const dayMetricsBody = `{
  "data": {
    "balance": "12.345000000000000000",
    "balance_unit": "provider_unit",
    "tier": "paid",
    "updated_at": "2026-09-21T00:00:00Z",
    "metrics": [
      {"client_key":"ck_1","network":"solana","connection":"c1","provider_id":"p1","method":"m1","request_count":120,"cu":220440},
      {"client_key":"ck_1","network":"solana","connection":"c1","provider_id":"p1","method":"m2","request_count":30,"cu":600}
    ]
  }
}`

// keysListBody 里含禁用但消耗更大的 Key：指纹匹配必须优先于"消耗最大"回退。
func keysListBody(runtimeKey string) string {
	return fmt.Sprintf(`{
  "data": {
    "keys": [
      {"key_id":"key-old","name":"retired-key","status":false,"cu_spent_today":9999999,
       "cu_day_limit":10000000,"rate_limit":120000,"updated_at":"2026-09-20T00:00:00Z"},
      {"key_id":"key-prod","name":"prod-solana","status":true,"cu_spent_today":1837000,
       "cu_day_limit":10000000,"rate_limit":120000,"updated_at":"2026-09-21T01:59:00Z",
       "api_key":%q}
    ]
  }
}`, runtimeKey)
}

func runtimeKeyFingerprint(key string) string {
	sum := sha256.Sum256([]byte(key))
	return hex.EncodeToString(sum[:])[:16]
}

// ---- (a) 正常合并 ----

func TestSyncMergesStatisticsAndKeys(t *testing.T) {
	server := newRecordingServer(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case StatisticsPath:
			if got := r.Header.Get("Authorization"); got != "Bearer stat-token" {
				t.Errorf("statistics Authorization = %q, want Bearer stat-token", got)
			}
			switch r.URL.Query().Get("range") {
			case "hour":
				io.WriteString(w, hourMetricsBody)
			case "day":
				io.WriteString(w, dayMetricsBody)
			default:
				http.Error(w, "unexpected range", http.StatusBadRequest)
			}
		case KeysPath:
			if got := r.Header.Get("Authorization"); got != "Bearer keys-token" {
				t.Errorf("keys Authorization = %q, want Bearer keys-token", got)
			}
			io.WriteString(w, keysListBody(runtimeKeyValue))
		default:
			http.NotFound(w, r)
		}
	})
	defer server.Close()

	state := &fakeStore{}
	svc := newTestService(Options{BaseURL: server.URL, RuntimeKeyFingerprint: runtimeKeyFingerprint(runtimeKeyValue)}, state)

	snap, err := svc.Sync(context.Background())
	if err != nil {
		t.Fatalf("Sync returned error: %v", err)
	}
	if snap.SyncStatus != SyncStatusOK {
		t.Fatalf("SyncStatus = %q, want %q (class %q)", snap.SyncStatus, SyncStatusOK, snap.SyncErrorClass)
	}
	if snap.SyncErrorClass != "" {
		t.Fatalf("SyncErrorClass = %q, want empty", snap.SyncErrorClass)
	}
	if snap.Tier != "paid" {
		t.Errorf("Tier = %q, want paid", snap.Tier)
	}
	if snap.Balance == nil || !snap.Balance.Equal(decimal.RequireFromString("12.345000000000000000")) {
		t.Errorf("Balance = %v, want 12.345（数值相等即可，原文由 balanceText 断言）", snap.Balance)
	}
	if snap.BalanceUnit != "provider_unit" {
		t.Errorf("BalanceUnit = %q, want provider_unit", snap.BalanceUnit)
	}
	if snap.BalanceString() != "12.345000000000000000" {
		t.Errorf("BalanceString = %q, want 上游原文（尾零不得被 decimal 规范化掉）", snap.BalanceString())
	}
	if snap.RequestsHour != 40 || snap.CUHour != 73680 {
		t.Errorf("hour totals = (%d, %d), want (40, 73680)", snap.RequestsHour, snap.CUHour)
	}
	if snap.RequestsDay != 150 || snap.CUDay != 221040 {
		t.Errorf("day totals = (%d, %d), want (150, 221040)", snap.RequestsDay, snap.CUDay)
	}
	if snap.KeyID != "key-prod" || snap.KeyName != "prod-solana" {
		t.Errorf("selected key = (%q, %q), want (key-prod, prod-solana)", snap.KeyID, snap.KeyName)
	}
	if snap.KeyEnabled == nil || !*snap.KeyEnabled {
		t.Errorf("KeyEnabled = %v, want true", snap.KeyEnabled)
	}
	if snap.CUSpentToday == nil || *snap.CUSpentToday != 1837000 {
		t.Errorf("CUSpentToday = %v, want 1837000", snap.CUSpentToday)
	}
	if snap.CUDayLimit == nil || *snap.CUDayLimit != 10000000 {
		t.Errorf("CUDayLimit = %v, want 10000000", snap.CUDayLimit)
	}
	if snap.CURemainingToday == nil || *snap.CURemainingToday != 8163000 {
		t.Errorf("CURemainingToday = %v, want 8163000", snap.CURemainingToday)
	}
	if snap.RateLimit == nil || *snap.RateLimit != 120000 {
		t.Errorf("RateLimit = %v, want 120000", snap.RateLimit)
	}
	if snap.Source != SourceBoth {
		t.Errorf("Source = %q, want %q", snap.Source, SourceBoth)
	}
	if len(snap.RawHash) != 64 {
		t.Errorf("RawHash = %q, want 64 hex chars", snap.RawHash)
	}
	if snap.SourceUpdatedAt == nil || !snap.SourceUpdatedAt.Equal(time.Date(2026, 9, 21, 0, 0, 0, 0, time.UTC)) {
		t.Errorf("SourceUpdatedAt = %v, want 2026-09-21T00:00:00Z (range=day 优先)", snap.SourceUpdatedAt)
	}
	if snap.SyncedAt.IsZero() {
		t.Error("SyncedAt is zero")
	}

	wantCalls := []string{
		StatisticsPath + "?range=hour",
		StatisticsPath + "?range=day",
		KeysPath,
	}
	if got := server.recordedCalls(); !equalStrings(got, wantCalls) {
		t.Errorf("calls = %v, want %v (串行且 order 固定)", got, wantCalls)
	}
	times := server.recordedTimes()
	for i := 1; i < len(times); i++ {
		if gap := times[i].Sub(times[i-1]); gap < 90*time.Millisecond {
			t.Errorf("calls %d -> %d only %s apart, want >= 100ms spacing", i-1, i, gap)
		}
	}

	rows := state.rows()
	if len(rows) != 1 {
		t.Fatalf("persisted rows = %d, want 1", len(rows))
	}
	row := rows[0]
	if row.ProviderKey != ProviderKey || row.AccountTier != "paid" || row.SyncStatus != SyncStatusOK {
		t.Errorf("row = %+v, want provider=drpc tier=paid status=ok", row)
	}
	if row.AccountBalance != "12.345000000000000000" || row.AccountBalanceUnit != "provider_unit" {
		t.Errorf("row balance = (%q, %q)", row.AccountBalance, row.AccountBalanceUnit)
	}
	if row.KeyID != "key-prod" || row.RequestsDay != 150 || row.CUDay != 221040 {
		t.Errorf("row key/day totals = (%q, %d, %d)", row.KeyID, row.RequestsDay, row.CUDay)
	}
	if row.CURemainingToday == nil || *row.CURemainingToday != 8163000 {
		t.Errorf("row CURemainingToday = %v, want 8163000", row.CURemainingToday)
	}
}

func equalStrings(got, want []string) bool {
	if len(got) != len(want) {
		return false
	}
	for i := range got {
		if got[i] != want[i] {
			return false
		}
	}
	return true
}

// ---- (b) cu_day_limit 空或 0 ----

func TestSyncCUDayLimitUnset(t *testing.T) {
	cases := []struct {
		name      string
		limitJSON string
		wantLimit *int64
	}{
		{name: "null", limitJSON: "null", wantLimit: nil},
		{name: "zero", limitJSON: "0", wantLimit: int64Ptr(0)},
		{name: "empty string", limitJSON: `""`, wantLimit: nil},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			server := newRecordingServer(func(w http.ResponseWriter, r *http.Request) {
				switch r.URL.Path {
				case StatisticsPath:
					io.WriteString(w, hourMetricsBody)
				case KeysPath:
					io.WriteString(w, fmt.Sprintf(`{"data":{"keys":[{"key_id":"k1","name":"n1","status":true,
						"cu_spent_today":500,"cu_day_limit":%s,"rate_limit":10}]}}`, tc.limitJSON))
				default:
					http.NotFound(w, r)
				}
			})
			defer server.Close()

			svc := newTestService(Options{BaseURL: server.URL}, nil)
			snap, err := svc.Sync(context.Background())
			if err != nil {
				t.Fatalf("Sync returned error: %v", err)
			}
			if snap.CURemainingToday != nil {
				t.Fatalf("CURemainingToday = %d, want nil (未设置日上限不能显示剩余 0)", *snap.CURemainingToday)
			}
			switch {
			case tc.wantLimit == nil && snap.CUDayLimit != nil:
				t.Fatalf("CUDayLimit = %d, want nil", *snap.CUDayLimit)
			case tc.wantLimit != nil && (snap.CUDayLimit == nil || *snap.CUDayLimit != *tc.wantLimit):
				t.Fatalf("CUDayLimit = %v, want %d", snap.CUDayLimit, *tc.wantLimit)
			}
		})
	}
}

func int64Ptr(value int64) *int64 { return &value }

// ---- (c) 管理 Token 缺失 ----

func TestSyncWithoutManagementTokens(t *testing.T) {
	server := newRecordingServer(func(w http.ResponseWriter, r *http.Request) {
		t.Errorf("unexpected management call %s：没有 Token 时不应发请求", r.URL.Path)
		http.Error(w, "should not be called", http.StatusInternalServerError)
	})
	defer server.Close()

	state := &fakeStore{}
	svc := newTestService(Options{BaseURL: server.URL, Secrets: staticResolver(nil)}, state)
	snap, err := svc.Sync(context.Background())
	if err != nil {
		t.Fatalf("Sync returned error: %v", err)
	}
	if snap.SyncStatus != SyncStatusNoToken {
		t.Fatalf("SyncStatus = %q, want %q", snap.SyncStatus, SyncStatusNoToken)
	}
	if server.callCount() != 0 {
		t.Fatalf("management calls = %d, want 0", server.callCount())
	}
	if snap.Balance != nil || snap.CUSpentToday != nil || snap.CURemainingToday != nil {
		t.Fatalf("snapshot must not invent values: %+v", snap)
	}
	if snap.Tier != TierUnknown || snap.BalanceUnit != DefaultBalanceUnit {
		t.Errorf("defaults = (%q, %q), want (unknown, provider_unit)", snap.Tier, snap.BalanceUnit)
	}
	if snap.Source != "" {
		t.Errorf("Source = %q, want empty", snap.Source)
	}
	if rows := state.rows(); len(rows) != 0 {
		t.Errorf("rows = %d, want 0 (没有数据来源时不写额度快照)", len(rows))
	}

	last, err := svc.LastSnapshot(context.Background())
	if err != nil || last == nil {
		t.Fatalf("LastSnapshot = (%v, %v), want snapshot", last, err)
	}
	if last.SyncStatus != SyncStatusNoToken {
		t.Errorf("LastSnapshot status = %q", last.SyncStatus)
	}
}

func TestSyncWithOnlyStatisticsToken(t *testing.T) {
	server := newRecordingServer(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != StatisticsPath {
			t.Errorf("unexpected call %s", r.URL.Path)
			http.NotFound(w, r)
			return
		}
		if r.URL.Query().Get("range") == "day" {
			io.WriteString(w, dayMetricsBody)
			return
		}
		io.WriteString(w, hourMetricsBody)
	})
	defer server.Close()

	svc := newTestService(Options{
		BaseURL: server.URL,
		Secrets: staticResolver(map[string]string{"env://STAT": "stat-token"}),
	}, nil)
	snap, err := svc.Sync(context.Background())
	if err != nil {
		t.Fatalf("Sync returned error: %v", err)
	}
	if snap.SyncStatus != SyncStatusOK {
		t.Fatalf("SyncStatus = %q, want ok", snap.SyncStatus)
	}
	if !strings.Contains(snap.SyncErrorClass, ErrClassNoToken) {
		t.Errorf("SyncErrorClass = %q, want contains %q", snap.SyncErrorClass, ErrClassNoToken)
	}
	if snap.RequestsDay != 150 {
		t.Errorf("RequestsDay = %d, want 150", snap.RequestsDay)
	}
	if snap.KeyID != "" || snap.KeyEnabled != nil {
		t.Errorf("key fields must stay empty without keys token: %q %v", snap.KeyID, snap.KeyEnabled)
	}
	if snap.Source != SourceStatistics {
		t.Errorf("Source = %q, want %q", snap.Source, SourceStatistics)
	}
}

// ---- (d) 401 / 500 ----

func TestSyncHTTPFailures(t *testing.T) {
	cases := []struct {
		name      string
		status    int
		wantClass string
	}{
		{name: "401", status: http.StatusUnauthorized, wantClass: ErrClassUnauthorized},
		{name: "403", status: http.StatusForbidden, wantClass: ErrClassUnauthorized},
		{name: "500", status: http.StatusInternalServerError, wantClass: ErrClassServerError},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			server := newRecordingServer(func(w http.ResponseWriter, r *http.Request) {
				http.Error(w, "upstream failure", tc.status)
			})
			defer server.Close()

			state := &fakeStore{}
			svc := newTestService(Options{BaseURL: server.URL}, state)
			snap, err := svc.Sync(context.Background())
			if err == nil || !errors.Is(err, ErrManagementSync) {
				t.Fatalf("err = %v, want ErrManagementSync", err)
			}
			if snap.SyncStatus != SyncStatusError {
				t.Fatalf("SyncStatus = %q, want error", snap.SyncStatus)
			}
			if !strings.Contains(snap.SyncErrorClass, tc.wantClass) {
				t.Fatalf("SyncErrorClass = %q, want contains %q", snap.SyncErrorClass, tc.wantClass)
			}
			if snap.Source != SourceBoth {
				t.Errorf("Source = %q, want %q (失败也要记录尝试过的接口)", snap.Source, SourceBoth)
			}
			rows := state.rows()
			if len(rows) != 1 || rows[0].SyncStatus != SyncStatusError {
				t.Fatalf("rows = %+v, want 1 row with sync_status=error", rows)
			}
			if !strings.Contains(rows[0].SyncErrorClass, tc.wantClass) {
				t.Errorf("row SyncErrorClass = %q", rows[0].SyncErrorClass)
			}
		})
	}
}

// ---- (e) 60 秒防抖 ----

func TestSyncDebouncedWithinWindow(t *testing.T) {
	server := newRecordingServer(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case StatisticsPath:
			io.WriteString(w, hourMetricsBody)
		case KeysPath:
			io.WriteString(w, keysListBody(runtimeKeyValue))
		default:
			http.NotFound(w, r)
		}
	})
	defer server.Close()

	svc := newTestService(Options{BaseURL: server.URL}, nil)
	now := time.Now()
	svc.now = func() time.Time { return now }

	first, err := svc.SyncDebounced(context.Background())
	if err != nil {
		t.Fatalf("first SyncDebounced: %v", err)
	}
	callsAfterFirst := server.callCount()

	second, err := svc.SyncDebounced(context.Background())
	if err != nil {
		t.Fatalf("second SyncDebounced: %v", err)
	}
	if server.callCount() != callsAfterFirst {
		t.Fatalf("management calls after debounced repeat = %d, want %d (60 秒内不得再打接口)",
			server.callCount(), callsAfterFirst)
	}
	if !second.SyncedAt.Equal(first.SyncedAt) {
		t.Fatalf("debounced snapshot SyncedAt = %v, want cached %v", second.SyncedAt, first.SyncedAt)
	}

	now = now.Add(61 * time.Second)
	if _, err := svc.SyncDebounced(context.Background()); err != nil {
		t.Fatalf("third SyncDebounced: %v", err)
	}
	if server.callCount() <= callsAfterFirst {
		t.Fatalf("management calls after window expiry = %d, want > %d", server.callCount(), callsAfterFirst)
	}

	// 非防抖入口（Run 的周期同步）不受 60 秒窗口限制。
	before := server.callCount()
	if _, err := svc.Sync(context.Background()); err != nil {
		t.Fatalf("Sync: %v", err)
	}
	if server.callCount() <= before {
		t.Fatalf("Sync must always hit the API: calls = %d, want > %d", server.callCount(), before)
	}
}

// ---- (f) 响应形态异常不 panic ----

func TestSyncTolerantShapes(t *testing.T) {
	cases := []struct {
		name        string
		metricsBody string
		wantStatus  string
		wantClass   string
		wantReq     int64
		wantCU      int64
		wantBalance string
	}{
		{
			name:        "invalid json",
			metricsBody: "not json at all {",
			wantStatus:  SyncStatusError,
			wantClass:   ErrClassInvalidJSON,
		},
		{
			name:        "unrelated object",
			metricsBody: `{"foo":{"bar":[1,2,3]}}`,
			wantStatus:  SyncStatusOK,
			wantClass:   ErrClassUnexpectedShape,
		},
		{
			name: "top level array of groups",
			metricsBody: `[{"network":"solana","request_count":3,"cu":60},
			                   {"network":"eth","request_count":4,"cu":80}]`,
			wantStatus: SyncStatusOK,
			wantClass:  ErrClassUnexpectedShape,
			wantReq:    7,
			wantCU:     140,
		},
		{
			name:        "string numbers with separators",
			metricsBody: `{"data":{"request_count":"12","cu":"1,500","balance":"1.25"}}`,
			wantStatus:  SyncStatusOK,
			wantClass:   "",
			wantReq:     12,
			wantCU:      1500,
			wantBalance: "1.25",
		},
		{
			name:        "total wins over nested detail",
			metricsBody: `{"balance":12.5,"data":{"metrics":[{"cu":10},{"cu":20}]}}`,
			wantStatus:  SyncStatusOK,
			wantClass:   ErrClassUnexpectedShape,
			wantReq:     0,
			wantCU:      30,
			wantBalance: "12.5",
		},
		{
			name:        "explicit nulls",
			metricsBody: `{"data":{"balance":null,"request_count":null,"cu":null}}`,
			wantStatus:  SyncStatusOK,
			wantClass:   ErrClassUnexpectedShape,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			server := newRecordingServer(func(w http.ResponseWriter, r *http.Request) {
				switch r.URL.Path {
				case StatisticsPath:
					io.WriteString(w, tc.metricsBody)
				case KeysPath:
					// Keys 响应也故意给"不像列表"的形态：不得 panic，也不得编造 Key。
					io.WriteString(w, `{"data":{"keys":"not-a-list"}}`)
				default:
					http.NotFound(w, r)
				}
			})
			defer server.Close()

			svc := newTestService(Options{BaseURL: server.URL}, nil)
			snap, err := svc.Sync(context.Background())
			if snap.SyncStatus != tc.wantStatus {
				t.Fatalf("SyncStatus = %q, want %q (class %q, err %v)", snap.SyncStatus, tc.wantStatus, snap.SyncErrorClass, err)
			}
			if tc.wantClass == "" && snap.SyncErrorClass != "" {
				t.Fatalf("SyncErrorClass = %q, want empty", snap.SyncErrorClass)
			}
			if tc.wantClass != "" && !strings.Contains(snap.SyncErrorClass, tc.wantClass) {
				t.Fatalf("SyncErrorClass = %q, want contains %q", snap.SyncErrorClass, tc.wantClass)
			}
			if snap.RequestsHour != tc.wantReq || snap.CUHour != tc.wantCU {
				t.Errorf("hour totals = (%d, %d), want (%d, %d)", snap.RequestsHour, snap.CUHour, tc.wantReq, tc.wantCU)
			}
			if tc.wantBalance == "" {
				if snap.Balance != nil {
					t.Errorf("Balance = %v, want nil", snap.Balance)
				}
			} else if snap.Balance == nil || snap.Balance.String() != tc.wantBalance {
				t.Errorf("Balance = %v, want %s", snap.Balance, tc.wantBalance)
			}
			if snap.KeyID != "" || snap.KeyEnabled != nil || snap.CUSpentToday != nil {
				t.Errorf("key fields must stay empty for odd keys payload: %+v", snap)
			}
			if snap.CURemainingToday != nil {
				t.Errorf("CURemainingToday = %v, want nil", snap.CURemainingToday)
			}
		})
	}
}

// ---- Key 选择 ----

func TestSelectTeamKey(t *testing.T) {
	enabled, disabled := true, false
	spent := func(value int64) *int64 { return &value }

	primary := teamKey{ID: "key-a", Name: "alpha", Enabled: &enabled, CUSpentToday: spent(10), Fingerprint: "0123456789abcdef"}
	busy := teamKey{ID: "key-b", Name: "beta", Enabled: &enabled, CUSpentToday: spent(50)}
	richButDisabled := teamKey{ID: "key-c", Name: "gamma", Enabled: &disabled, CUSpentToday: spent(999)}
	keys := []teamKey{primary, busy, richButDisabled}

	cases := []struct {
		name     string
		selector string
		wantID   string
		wantOK   bool
	}{
		{name: "fingerprint", selector: "0123456789ABCDEF", wantID: "key-a", wantOK: true},
		{name: "name", selector: "beta", wantID: "key-b", wantOK: true},
		{name: "key id", selector: "KEY-C", wantID: "key-c", wantOK: true},
		{name: "fallback picks enabled max spent", selector: "", wantID: "key-b", wantOK: true},
		{name: "no match falls back", selector: "ffffffffffffffff", wantID: "key-b", wantOK: true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got, ok := selectTeamKey(keys, tc.selector)
			if ok != tc.wantOK || got.ID != tc.wantID {
				t.Fatalf("selectTeamKey(%q) = (%q, %v), want (%q, %v)", tc.selector, got.ID, ok, tc.wantID, tc.wantOK)
			}
		})
	}

	if _, ok := selectTeamKey([]teamKey{richButDisabled}, ""); ok {
		t.Fatal("selectTeamKey must not pick a disabled key when no enabled key exists")
	}
}

// ---- 剩余额度与告警阈值 ----

func TestRemainingToday(t *testing.T) {
	cases := []struct {
		name  string
		spent *int64
		limit *int64
		want  *int64
	}{
		{name: "no limit", limit: nil, want: nil},
		{name: "zero limit", limit: int64Ptr(0), want: nil},
		{name: "negative limit", limit: int64Ptr(-1), want: nil},
		{name: "no spend yet", limit: int64Ptr(100), want: int64Ptr(100)},
		{name: "partial spend", spent: int64Ptr(30), limit: int64Ptr(100), want: int64Ptr(70)},
		{name: "over limit clamps to zero", spent: int64Ptr(150), limit: int64Ptr(100), want: int64Ptr(0)},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := remainingToday(tc.spent, tc.limit)
			switch {
			case tc.want == nil && got != nil:
				t.Fatalf("remainingToday = %d, want nil", *got)
			case tc.want != nil && (got == nil || *got != *tc.want):
				t.Fatalf("remainingToday = %v, want %d", got, *tc.want)
			}
		})
	}
}

func TestAlertDecisions(t *testing.T) {
	cases := []struct {
		name            string
		snap            Snapshot
		wantFingerprint string
		wantSeverity    string
		wantCount       int
	}{
		{
			name:      "quota below warn has no alert",
			snap:      Snapshot{SyncStatus: SyncStatusOK, CUSpentToday: int64Ptr(500), CUDayLimit: int64Ptr(1000)},
			wantCount: 0,
		},
		{
			name:            "quota at warn line",
			snap:            Snapshot{SyncStatus: SyncStatusOK, CUSpentToday: int64Ptr(800), CUDayLimit: int64Ptr(1000)},
			wantFingerprint: alertFingerprintQuota,
			wantSeverity:    alertSeverityWarning,
			wantCount:       1,
		},
		{
			name:            "quota near limit",
			snap:            Snapshot{SyncStatus: SyncStatusOK, CUSpentToday: int64Ptr(960), CUDayLimit: int64Ptr(1000)},
			wantFingerprint: alertFingerprintQuota,
			wantSeverity:    alertSeverityWarning,
			wantCount:       1,
		},
		{
			name:            "quota exhausted",
			snap:            Snapshot{SyncStatus: SyncStatusOK, CUSpentToday: int64Ptr(1000), CUDayLimit: int64Ptr(1000)},
			wantFingerprint: alertFingerprintQuota,
			wantSeverity:    alertSeverityCritical,
			wantCount:       1,
		},
		{
			name:      "no day limit is not an alert",
			snap:      Snapshot{SyncStatus: SyncStatusOK, CUSpentToday: int64Ptr(10), CUDayLimit: int64Ptr(0)},
			wantCount: 0,
		},
		{
			name:            "zero balance",
			snap:            Snapshot{SyncStatus: SyncStatusOK, Balance: decimalPtr("0")},
			wantFingerprint: alertFingerprintBalance,
			wantSeverity:    alertSeverityCritical,
			wantCount:       1,
		},
		{
			name:      "unknown balance is not an alert",
			snap:      Snapshot{SyncStatus: SyncStatusOK},
			wantCount: 0,
		},
		{
			name:            "sync error",
			snap:            Snapshot{SyncStatus: SyncStatusError, SyncErrorClass: ErrClassServerError},
			wantFingerprint: alertFingerprintSync,
			wantSeverity:    alertSeverityCritical,
			wantCount:       1,
		},
		{
			name:            "no management token",
			snap:            Snapshot{SyncStatus: SyncStatusNoToken},
			wantFingerprint: alertFingerprintSync,
			wantSeverity:    alertSeverityWarning,
			wantCount:       1,
		},
		{
			name:            "partial token gap",
			snap:            Snapshot{SyncStatus: SyncStatusOK, SyncErrorClass: ErrClassNoToken},
			wantFingerprint: alertFingerprintSync,
			wantSeverity:    alertSeverityWarning,
			wantCount:       1,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			decisions := alertDecisions(tc.snap)
			if len(decisions) != tc.wantCount {
				t.Fatalf("alertDecisions = %+v, want %d", decisions, tc.wantCount)
			}
			if tc.wantCount == 0 {
				return
			}
			if decisions[0].Fingerprint != tc.wantFingerprint || decisions[0].Severity != tc.wantSeverity {
				t.Fatalf("alert = %+v, want fingerprint %q severity %q", decisions[0], tc.wantFingerprint, tc.wantSeverity)
			}
			if decisions[0].Title == "" || decisions[0].Message == "" {
				t.Fatalf("alert must carry title and message: %+v", decisions[0])
			}
		})
	}
}

func decimalPtr(text string) *decimal.Decimal {
	value := decimal.RequireFromString(text)
	return &value
}

// ---- 回调、回退与 JSON 形态 ----

func TestOnSnapshotCallback(t *testing.T) {
	t.Run("notified on success", func(t *testing.T) {
		server := newRecordingServer(func(w http.ResponseWriter, r *http.Request) {
			if r.URL.Path == StatisticsPath {
				io.WriteString(w, hourMetricsBody)
				return
			}
			io.WriteString(w, keysListBody(runtimeKeyValue))
		})
		defer server.Close()

		var mu sync.Mutex
		var got []Snapshot
		svc := newTestService(Options{
			BaseURL:    server.URL,
			OnSnapshot: func(snap Snapshot) { mu.Lock(); got = append(got, snap); mu.Unlock() },
		}, nil)
		if _, err := svc.Sync(context.Background()); err != nil {
			t.Fatalf("Sync: %v", err)
		}
		mu.Lock()
		defer mu.Unlock()
		if len(got) != 1 || got[0].SyncStatus != SyncStatusOK {
			t.Fatalf("callback snapshots = %+v, want one ok snapshot", got)
		}
	})

	t.Run("not notified on failure", func(t *testing.T) {
		server := newRecordingServer(func(w http.ResponseWriter, r *http.Request) {
			http.Error(w, "boom", http.StatusInternalServerError)
		})
		defer server.Close()

		called := false
		svc := newTestService(Options{
			BaseURL:    server.URL,
			OnSnapshot: func(Snapshot) { called = true },
		}, nil)
		if _, err := svc.Sync(context.Background()); err == nil {
			t.Fatal("Sync should fail")
		}
		if called {
			t.Fatal("OnSnapshot must not be called with unknown (error) fields")
		}
	})
}

func TestLastSnapshotFallsBackToStore(t *testing.T) {
	state := &fakeStore{latest: &store.AccountSnapshotRow{
		ProviderKey:        ProviderKey,
		AccountTier:        "paid",
		AccountBalance:     "42.5",
		AccountBalanceUnit: "provider_unit",
		KeyID:              "key-prod",
		CUSpentToday:       int64Ptr(10),
		CUDayLimit:         int64Ptr(100),
		CURemainingToday:   int64Ptr(90),
		SyncStatus:         SyncStatusOK,
		SyncedAt:           time.Unix(1700000000, 0).UTC(),
	}}
	svc := newTestService(Options{BaseURL: "http://127.0.0.1:1"}, state)

	snap, err := svc.LastSnapshot(context.Background())
	if err != nil || snap == nil {
		t.Fatalf("LastSnapshot = (%v, %v), want snapshot", snap, err)
	}
	if snap.Balance == nil || snap.Balance.String() != "42.5" {
		t.Errorf("Balance = %v, want 42.5", snap.Balance)
	}
	if snap.Tier != "paid" || snap.SyncStatus != SyncStatusOK {
		t.Errorf("snapshot = %+v", snap)
	}
}

func TestSnapshotMarshalJSONKeepsBalanceString(t *testing.T) {
	snap := Snapshot{
		Tier:         "paid",
		Balance:      decimalPtr("12.345000000000000000"),
		balanceRaw:   "12.345000000000000000",
		BalanceUnit:  "provider_unit",
		CUSpentToday: int64Ptr(1837000),
		SyncStatus:   SyncStatusOK,
	}
	raw, err := json.Marshal(snap)
	if err != nil {
		t.Fatalf("MarshalJSON: %v", err)
	}
	var decoded map[string]any
	if err := json.Unmarshal(raw, &decoded); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if got, ok := decoded["balance"].(string); !ok || got != "12.345000000000000000" {
		t.Fatalf("balance = %#v, want string 12.345000000000000000", decoded["balance"])
	}
	if decoded["tier"] != "paid" || decoded["balance_unit"] != "provider_unit" {
		t.Fatalf("decoded = %#v", decoded)
	}
	if _, ok := decoded["cu_spent_today"]; !ok {
		t.Fatalf("decoded = %#v, want cu_spent_today field", decoded)
	}

	unknown := Snapshot{Tier: TierUnknown, BalanceUnit: DefaultBalanceUnit, SyncStatus: SyncStatusOK}
	raw, err = json.Marshal(unknown)
	if err != nil {
		t.Fatalf("MarshalJSON (nil balance): %v", err)
	}
	if !strings.Contains(string(raw), `"balance":null`) {
		t.Fatalf("raw = %s, want balance null (不编造 0)", raw)
	}
}

func TestOptionsNormalized(t *testing.T) {
	svc := newTestService(Options{SyncInterval: time.Second, HTTPTimeout: 0, BaseURL: "https://drpc.org/"}, nil)
	if svc.opts.SyncInterval != minSyncInterval {
		t.Errorf("SyncInterval = %s, want clamped to %s", svc.opts.SyncInterval, minSyncInterval)
	}
	if svc.opts.HTTPTimeout != defaultHTTPTimeout {
		t.Errorf("HTTPTimeout = %s, want %s", svc.opts.HTTPTimeout, defaultHTTPTimeout)
	}
	if svc.opts.BaseURL != "https://drpc.org" {
		t.Errorf("BaseURL = %q, want https://drpc.org", svc.opts.BaseURL)
	}
}

func TestSyncDisabled(t *testing.T) {
	svc := newService(Options{Enabled: false, Log: discardLogger()}, nil, nil)
	snap, err := svc.Sync(context.Background())
	if err == nil || !errors.Is(err, ErrManagementSync) {
		t.Fatalf("err = %v, want ErrManagementSync", err)
	}
	if snap.SyncStatus != SyncStatusError || !strings.Contains(snap.SyncErrorClass, ErrClassDisabled) {
		t.Fatalf("snapshot = %+v, want error/disabled", snap)
	}

	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	svc.Run(ctx) // 立即返回：不得阻塞在已取消的 ctx 上
}

func TestRunStopsOnContextCancel(t *testing.T) {
	server := newRecordingServer(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == StatisticsPath {
			io.WriteString(w, hourMetricsBody)
			return
		}
		io.WriteString(w, keysListBody(runtimeKeyValue))
	})
	defer server.Close()

	svc := newTestService(Options{BaseURL: server.URL, SyncInterval: time.Minute}, nil)
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() {
		svc.Run(ctx)
		close(done)
	}()
	time.Sleep(50 * time.Millisecond)
	cancel()
	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("Run did not return after context cancellation")
	}
}
