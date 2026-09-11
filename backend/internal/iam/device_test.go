package iam

import (
	"net/http"
	"testing"
)

func TestParseUserAgent(t *testing.T) {
	cases := []struct {
		ua        string
		wantOS    string
		wantBrows string
		wantType  string
	}{
		{"Mozilla/5.0 (Windows NT 10.0; Win64; x64) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/120.0.0.0 Safari/537.36", "Windows", "Chrome", "desktop"},
		{"Mozilla/5.0 (Macintosh; Intel Mac OS X 10_15_7) AppleWebKit/605.1.15 (KHTML, like Gecko) Version/17.0 Safari/605.1.15", "macOS", "Safari", "desktop"},
		{"Mozilla/5.0 (iPhone; CPU iPhone OS 17_0 like Mac OS X) AppleWebKit/605.1.15 (KHTML, like Gecko) Version/17.0 Mobile/15E148 Safari/604.1", "iOS", "Safari", "mobile"},
		{"Mozilla/5.0 (Linux; Android 14; Pixel 8) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/119.0.0.0 Mobile Safari/537.36", "Android", "Chrome", "mobile"},
		{"Mozilla/5.0 (Windows NT 10.0; Win64; x64) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/120.0.0.0 Safari/537.36 Edg/120.0.0.0", "Windows", "Edge", "desktop"},
		{"curl/8.4.0", "", "", "desktop"},
	}
	for _, tc := range cases {
		got := ParseUserAgent(tc.ua)
		if got.OSName != tc.wantOS {
			t.Errorf("ua=%q os=%q want %q", tc.ua, got.OSName, tc.wantOS)
		}
		if got.BrowserName != tc.wantBrows {
			t.Errorf("ua=%q browser=%q want %q", tc.ua, got.BrowserName, tc.wantBrows)
		}
		if tc.wantOS != "" && got.DeviceType != tc.wantType {
			t.Errorf("ua=%q type=%q want %q", tc.ua, got.DeviceType, tc.wantType)
		}
	}
	if !ParseUserAgent("Googlebot/2.1 (+http://www.google.com/bot.html)").Bot {
		t.Error("expected bot detection")
	}
}

func TestResolveClientIPIgnoresUntrustedForwarding(t *testing.T) {
	trusted, err := ParseTrustedProxies([]string{"127.0.0.1/32"})
	if err != nil {
		t.Fatal(err)
	}
	// 不可信来源：转发头必须被丢弃。
	req, _ := http.NewRequest("GET", "http://example.test/", nil)
	req.RemoteAddr = "203.0.113.9:5555"
	req.Header.Set("X-Forwarded-For", "1.2.3.4")
	req.Header.Set("CF-Connecting-IP", "5.6.7.8")
	got := ResolveClientIP(req, trusted)
	if got.ClientIP != "203.0.113.9" {
		t.Fatalf("untrusted peer must not be overridden by forwarding headers, got %q", got.ClientIP)
	}
	if got.PeerIP != "203.0.113.9" {
		t.Fatalf("peer ip mismatch: %q", got.PeerIP)
	}

	// 可信代理：采用 CF-Connecting-IP 作为客户端地址。
	req2, _ := http.NewRequest("GET", "http://example.test/", nil)
	req2.RemoteAddr = "127.0.0.1:5555"
	req2.Header.Set("CF-Connecting-IP", "5.6.7.8")
	got2 := ResolveClientIP(req2, trusted)
	if got2.ClientIP != "5.6.7.8" {
		t.Fatalf("trusted proxy should resolve client ip, got %q", got2.ClientIP)
	}
}

func TestMaskEmail(t *testing.T) {
	if got := MaskEmail("Investigators@Example.com"); got != "in***********s@example.com" && got[:2] != "in" {
		t.Fatalf("unexpected mask: %q", got)
	}
	if got := MaskEmail("not-an-email"); got != "***" {
		t.Fatalf("unexpected mask for invalid email: %q", got)
	}
}

func TestClientHints(t *testing.T) {
	h := http.Header{}
	h.Set("Sec-CH-UA-Platform", `"Windows"`)
	h.Set("Sec-CH-UA-Mobile", "?0")
	hints := ClientHints(h)
	if hints["sec-ch-ua-platform"] != `"Windows"` || hints["sec-ch-ua-mobile"] != "?0" {
		t.Fatalf("client hints not captured: %v", hints)
	}
	if _, ok := hints["sec-ch-ua"]; ok {
		t.Fatal("absent headers must not be recorded")
	}
}
