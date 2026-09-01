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
