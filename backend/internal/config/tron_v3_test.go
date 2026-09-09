package config

import (
	"testing"
	"time"
)

func TestTronProviderForDatasetUsesV3Overrides(t *testing.T) {
	c := TronConfig{
		Provider:             "ankr",
		DefaultProvider:      "ankr",
		TransactionsProvider: "trongrid",
		TRC20PrimaryProvider: "trongrid",
		InternalProvider:     "ankr",
		ReceiptProvider:      "ankr",
		TxDetailProvider:     "ankr",
	}
	if got := c.ProviderForDataset("transactions"); got != "trongrid" {
		t.Fatalf("transactions provider = %q", got)
	}
	if got := c.ProviderForDataset("token_transfers"); got != "trongrid" {
		t.Fatalf("TRC20 provider = %q", got)
	}
	if got := c.ProviderForDataset("internal_transactions"); got != "ankr" {
		t.Fatalf("internal provider = %q", got)
	}
	if got := c.ProviderForCapability("receipt"); got != "ankr" {
		t.Fatalf("receipt provider = %q", got)
	}
	if got := c.ProviderForCapability("tx_detail"); got != "ankr" {
		t.Fatalf("tx detail provider = %q", got)
	}
}

func TestTronProviderForDatasetFallsBackToLegacyProvider(t *testing.T) {
	c := TronConfig{Provider: "trongrid"}
	if got := c.ProviderForDataset("transactions"); got != "ankr" {
		t.Fatalf("provider = %q", got)
	}
	if got := c.ProviderForDataset("unknown"); got != "ankr" {
		t.Fatalf("unknown dataset provider = %q", got)
	}
}

func TestLoadDoesNotPromoteLegacyTronGridGlobalSwitch(t *testing.T) {
	t.Setenv("TRON_PROVIDER", "trongrid")
	t.Setenv("TRON_DEFAULT_PROVIDER", "")
	t.Setenv("TRON_TRANSACTIONS_PROVIDER", "")
	t.Setenv("TRON_TRC20_PROVIDER", "")
	t.Setenv("TRON_INTERNAL_PROVIDER", "")
	c := Load()
	if c.Tron.DefaultProvider != "ankr" {
		t.Fatalf("default provider inherited legacy global switch: %q", c.Tron.DefaultProvider)
	}
	if got := c.Tron.ProviderForDataset("token_transfers"); got != "trongrid" {
		t.Fatalf("token transfers provider = %q", got)
	}
}

func TestValidateRejectsTronGridNodeCapabilityRouting(t *testing.T) {
	c := Load()
	c.Env = "production"
	c.Tron.Enabled = true
	c.Tron.Provider = "ankr"
	c.Tron.DefaultProvider = "ankr"
	c.Tron.TransactionsProvider = "trongrid"
	c.Tron.TRC20Provider = "ankr"
	c.Tron.InternalProvider = "ankr"
	c.Tron.ReceiptProvider = "trongrid"
	c.Tron.TxDetailProvider = "ankr"
	c.Tron.TronGridEnabled = true
	c.Tron.BaseURL = "https://api.trongrid.io"
	c.Tron.APIKey = "test-key"
	c.Tron.TronGridPageSize = 200
	c.Tron.TronGridInitialConcurrency = 4
	c.Tron.TronGridNormalConcurrency = 6
	c.Tron.TronGridMaxConcurrency = 8
	if err := c.Validate(); err == nil {
		t.Fatal("TronGrid receipt routing was accepted")
	}
}

func TestLoadDefaultsReceiptAndTxDetailToAnkr(t *testing.T) {
	t.Setenv("TRON_RECEIPT_PROVIDER", "")
	t.Setenv("TRON_TX_DETAIL_PROVIDER", "")
	t.Setenv("TRONGRID_MIN_REQUEST_INTERVAL_MS", "")
	c := Load()
	if got := c.Tron.ProviderForCapability("receipt"); got != "ankr" {
		t.Fatalf("receipt provider=%q", got)
	}
	if got := c.Tron.ProviderForCapability("tx_detail"); got != "ankr" {
		t.Fatalf("tx detail provider=%q", got)
	}
	if c.Tron.TronGridMinRequestInterval != 100*time.Millisecond {
		t.Fatalf("min request interval=%s", c.Tron.TronGridMinRequestInterval)
	}
}

func TestLoadKeepsTronGridDatasetPoolsIndependent(t *testing.T) {
	t.Setenv("TRONGRID_TX_MAX_CONCURRENCY", "8")
	t.Setenv("TRONGRID_TRC20_MAX_CONCURRENCY", "24")
	t.Setenv("TRONGRID_TX_REQUEST_TIMEOUT", "60s")
	t.Setenv("TRONGRID_TRC20_REQUEST_TIMEOUT", "15s")
	c := Load()
	if c.Tron.TronGridTXMaxConcurrency != 8 || c.Tron.TronGridTRC20MaxConcurrency != 24 {
		t.Fatalf("dataset pools collapsed: tx=%d trc20=%d", c.Tron.TronGridTXMaxConcurrency, c.Tron.TronGridTRC20MaxConcurrency)
	}
	if c.Tron.TronGridTXRequestTimeout != 60*time.Second || c.Tron.TronGridTRC20RequestTimeout != 15*time.Second {
		t.Fatalf("dataset timeouts collapsed: tx=%s trc20=%s", c.Tron.TronGridTXRequestTimeout, c.Tron.TronGridTRC20RequestTimeout)
	}
}
