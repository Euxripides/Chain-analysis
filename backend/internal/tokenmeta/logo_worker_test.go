package tokenmeta

import (
	"context"
	"net"
	"testing"
)

func TestLogoSSRFGuardRejectsPrivateAndMetadataAddresses(t *testing.T) {
	for _, raw := range []string{"http://example.com/logo.png", "https://localhost/logo.png", "https://127.0.0.1/logo.png", "https://10.0.0.1/logo.png", "https://172.16.0.1/logo.png", "https://192.168.31.152/logo.png", "https://169.254.169.254/latest/meta-data", "https://[::1]/logo.png", "https://[fe80::1]/logo.png"} {
		if err := ValidatePublicHTTPS(context.Background(), raw, net.DefaultResolver); err == nil {
			t.Fatalf("must reject %s", raw)
		}
	}
}

func TestLogoConfigFetchURLMirrorsOnlyConfiguredHosts(t *testing.T) {
	cfg := LogoConfig{
		MirrorBase:  "https://images.weserv.nl/?url=",
		MirrorHosts: []string{"assets.coingecko.com"},
	}
	cases := []struct{ in, want string }{
		{
			"https://assets.coingecko.com/coins/images/825/large/bnb.png",
			"https://images.weserv.nl/?url=assets.coingecko.com%2Fcoins%2Fimages%2F825%2Flarge%2Fbnb.png",
		},
		{
			// 源站自带的查询串必须被转义，否则会被代理当成自己的参数
			"https://assets.coingecko.com/a.jpg?1696530741",
			"https://images.weserv.nl/?url=assets.coingecko.com%2Fa.jpg%3F1696530741",
		},
		{
			// 未配置镜像的 host（Trust Wallet）保持原样，走直连
			"https://raw.githubusercontent.com/trustwallet/assets/master/x/logo.png",
			"https://raw.githubusercontent.com/trustwallet/assets/master/x/logo.png",
		},
	}
	for _, c := range cases {
		if got := cfg.fetchURL(c.in); got != c.want {
			t.Fatalf("fetchURL(%q) = %q, want %q", c.in, got, c.want)
		}
	}
	// 未配置镜像时一律原样返回（默认行为不变）
	if got := (LogoConfig{}).fetchURL(cases[0].in); got != cases[0].in {
		t.Fatalf("无镜像时不应改写，得到 %q", got)
	}
}
