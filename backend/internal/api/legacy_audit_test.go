package api

import (
	"net/http/httptest"
	"testing"
)

func TestIsLegacyMutation(t *testing.T) {
	cases := []struct {
		method string
		path   string
		want   bool
	}{
		{"POST", "/api/v1/investigations", true},
		{"POST", "/api/v1/download-jobs/abc/pause", true},
		{"DELETE", "/api/v1/cases/1", true},
		{"GET", "/api/v1/investigations", false},
		{"GET", "/api/v1/download/ticket-123", false},
		{"POST", "/api/v1/download/ticket-123", false},
		{"POST", "/api/v2/cases", false},
		{"POST", "/api/admin/v1/users", false},
	}
	for _, tc := range cases {
		r := httptest.NewRequest(tc.method, tc.path, nil)
		if got := isLegacyMutation(r); got != tc.want {
			t.Errorf("isLegacyMutation(%s %s) = %v, want %v", tc.method, tc.path, got, tc.want)
		}
	}
}

// 审计动作名必须把 UUID 归一化，否则每个对象都会产生一个新的 action 值，
// 既拉高标签基数，也让"按动作检索"失去意义。
func TestLegacyActionPathNormalizesUUID(t *testing.T) {
	got := legacyActionPath("/api/v1/download-jobs/3f7c1e2a-9b4d-4c8e-8f1a-2b3c4d5e6f70/pause")
	want := "/download-jobs/{id}/pause"
	if got != want {
		t.Fatalf("legacyActionPath = %q, want %q", got, want)
	}
	if got := legacyActionPath("/api/v1/investigations"); got != "/investigations" {
		t.Fatalf("无 ID 路径不应被改写，got %q", got)
	}
}
