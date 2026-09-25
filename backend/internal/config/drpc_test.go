package config

import "testing"

// SSRF 防线：只放行 dRPC 自有域名（含付费套餐可能分配的专属子域），其余一律拒绝。
func TestDRPCHostAllowlist(t *testing.T) {
	allowed := []string{"lb.drpc.live", "lb.drpc.org", "drpc.live", "drpc.org",
		"solana.lb.drpc.live", "eu-1.solana.drpc.live", "api.drpc.org"}
	for _, h := range allowed {
		if !drpcHostAllowed(h) {
			t.Errorf("%q 应被允许", h)
		}
	}
	rejected := []string{"", "127.0.0.1", "localhost", "169.254.169.254",
		"evil.com", "drpc.live.evil.com", "notdrpc.live", "10.0.0.5", "metadata.google.internal"}
	for _, h := range rejected {
		if drpcHostAllowed(h) {
			t.Errorf("%q 必须被拒绝（SSRF 防线）", h)
		}
	}
}
