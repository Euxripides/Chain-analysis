package api

// sprint6_drpc.go 的纯函数回归：额度合并/收口、密钥占位与引用白名单。
// 这些语义直接决定后台展示（"未设置日上限"不得显示成"剩余 0"）与安全边界
// （secret_ref 不得混入明文），因此用最小单测钉住，不依赖 PostgreSQL/httptest。

import (
	"testing"
	"time"
)

func TestS6QuotaFinalizeDayLimitSemantics(t *testing.T) {
	s := &Server{}
	for _, limit := range []any{nil, 0, int64(0)} {
		quota := s.s6QuotaFinalize(map[string]any{
			"cu_day_limit": limit, "cu_spent_today": int64(120),
			"synced_at": time.Now().UTC().Add(-time.Minute),
		})
		if quota["cu_remaining_today"] != nil {
			t.Fatalf("limit=%v: cu_remaining_today=%v, want nil", limit, quota["cu_remaining_today"])
		}
	}
	quota := s.s6QuotaFinalize(map[string]any{
		"cu_day_limit": int64(1000), "cu_spent_today": int64(400),
		"synced_at": time.Now().UTC(),
	})
	if got, _ := quota["cu_remaining_today"].(int64); got != 600 {
		t.Fatalf("cu_remaining_today=%v, want 600", quota["cu_remaining_today"])
	}
	if quota["account_tier"] != "unknown" || quota["tier"] != "unknown" {
		t.Fatalf("tier 别名未补齐: %v", quota)
	}
}

func TestS6QuotaFinalizeStale(t *testing.T) {
	s := &Server{}
	fresh := s.s6QuotaFinalize(map[string]any{"synced_at": time.Now().UTC().Add(-time.Minute)})
	if fresh["stale"] != false {
		t.Fatalf("1 分钟前的快照不应 stale: %v", fresh)
	}
	old := s.s6QuotaFinalize(map[string]any{"synced_at": time.Now().UTC().Add(-30 * time.Minute)})
	if old["stale"] != true {
		t.Fatalf("30 分钟前的快照必须 stale（阈值 15 分钟）: %v", old)
	}
	never := s.s6QuotaFinalize(map[string]any{})
	if never["stale"] != true || never["sync_status"] != "never_synced" || never["synced_at"] != nil {
		t.Fatalf("从未同步必须是 stale + never_synced: %v", never)
	}
}

func TestS6QuotaMergePrefersBase(t *testing.T) {
	base := map[string]any{"balance": "10", "balance_unit": "provider_unit", "key_enabled": false}
	fallback := map[string]any{"balance": "99", "balance_unit": "usd", "cu_spent_today": int64(5), "key_enabled": true}
	out := s6QuotaMerge(base, fallback)
	if out["balance"] != "10" || out["balance_unit"] != "provider_unit" {
		t.Fatalf("较新的 base 必须优先: %v", out)
	}
	if out["key_enabled"] != false {
		t.Fatalf("key_enabled=false 是有效业务值，不能被 fallback 覆盖: %v", out)
	}
	if out["cu_spent_today"] != int64(5) {
		t.Fatalf("base 缺失的字段应由 fallback 补齐: %v", out)
	}
}

func TestS6EnsureSecretKindRowsAddsMissingKinds(t *testing.T) {
	rows := s6EnsureSecretKindRows([]map[string]any{{"secret_kind": "runtime_api_key", "configured": true}})
	if len(rows) != 3 {
		t.Fatalf("三类凭据必须各占一行，got %d", len(rows))
	}
	want := []string{s6SecretKindRuntimeAPIKey, s6SecretKindStatisticsAPIToken, s6SecretKindKeysAPIToken}
	for i, kind := range want {
		if rows[i]["secret_kind"] != kind {
			t.Fatalf("第 %d 行 kind=%v, want %s", i, rows[i]["secret_kind"], kind)
		}
	}
	if rows[1]["configured"] != false || rows[1]["status"] != "missing" {
		t.Fatalf("缺失 kind 必须是未配置占位: %v", rows[1])
	}
}

func TestS6SecretRefAllowed(t *testing.T) {
	for _, ref := range []string{"env://DRPC_API_KEY", "store://drpc/runtime_api_key", "ENV://X", "Store://drpc/keys_api_token"} {
		if !s6SecretRefAllowed(ref) {
			t.Fatalf("%q 应被接受", ref)
		}
	}
	for _, ref := range []string{"", "   ", "sk-live-plaintext", "vault://secret/drpc", "file:///etc/passwd"} {
		if s6SecretRefAllowed(ref) {
			t.Fatalf("%q 必须被拒绝（禁止明文/未知 scheme）", ref)
		}
	}
}

func TestS6ChannelState(t *testing.T) {
	if got := s6ChannelState(map[string]any{"state": "NORMAL"}); got != "NORMAL" {
		t.Fatalf("state=%q", got)
	}
	if got := s6ChannelState("DEGRADED"); got != "DEGRADED" {
		t.Fatalf("state=%q", got)
	}
	if got := s6ChannelState(map[string]any{"other": 1}); got != "UNKNOWN" {
		t.Fatalf("未知结构必须归一为 UNKNOWN, got %q", got)
	}
}

// testChannelProbe 复刻 drpc 包内 channelProbe 的 JSON 形态：ProbeChannels 返回的是结构体，
// 不是 map[string]any，API 层必须经 JSON 归一后再判定成功/状态。
type testChannelProbe struct {
	OK    bool   `json:"ok"`
	State string `json:"state"`
}

func TestS6ChannelsOKHandlesStructProbes(t *testing.T) {
	channels := map[string]any{
		"checked_at": "2026-09-21T00:00:00Z",
		"data_api":   testChannelProbe{OK: true, State: "NORMAL"},
		"solana_rpc": testChannelProbe{OK: false, State: "DEGRADED"},
	}
	if s6ChannelsOK(channels) {
		t.Fatal("任一条通道失败时 ok 必须为 false（结构体断言失败不得被误判为成功）")
	}
	if got := s6ChannelStates(channels)["solana_rpc"]; got != "DEGRADED" {
		t.Fatalf("solana_rpc state=%q, want DEGRADED", got)
	}
	if !s6ChannelsOK(map[string]any{"data_api": testChannelProbe{OK: true, State: "NORMAL"}}) {
		t.Fatal("通道全部成功时 ok 必须为 true")
	}
	if s6ChannelsOK(map[string]any{"checked_at": "2026-09-21T00:00:00Z"}) {
		t.Fatal("没有任何通道结果时 ok 必须为 false（fail closed）")
	}
}

func TestS6SnapshotReused(t *testing.T) {
	start := time.Now().UTC()
	if !s6SnapshotReused(start, start.Add(-time.Minute)) {
		t.Fatal("早于本次调用开始时间的快照应判定为 debounced")
	}
	if s6SnapshotReused(start, start.Add(time.Second)) {
		t.Fatal("本次调用之后写入的快照是新鲜同步")
	}
	if !s6SnapshotReused(start, time.Time{}) {
		t.Fatal("零值 synced_at 视为没有新鲜同步")
	}
}
