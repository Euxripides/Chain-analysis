package api

import (
	"encoding/json"
	"net/http/httptest"
	"testing"
	"time"
)

type providerAccountEnvelope struct {
	Accounts []struct {
		Status            string   `json:"status"`
		OfficialAvailable bool     `json:"official_available"`
		RemainingCredits  *float64 `json:"remaining_credits"`
		BalanceUSD        *float64 `json:"balance_usd"`
		BalanceKind       string   `json:"balance_kind"`
		Reason            string   `json:"reason"`
	} `json:"accounts"`
}

func TestProviderAccountsDoesNotRepresentUnavailableBalanceAsZero(t *testing.T) {
	for _, key := range []string{"ANKR_ACCOUNT_REMAINING_CREDITS", "ANKR_ACCOUNT_BALANCE_USD", "ANKR_ACCOUNT_PLAN", "ANKR_ACCOUNT_SYNCED_AT"} {
		t.Setenv(key, "")
	}
	recorder := httptest.NewRecorder()
	(&Server{}).handleAPIControlProviderAccounts(recorder, httptest.NewRequest("GET", "/", nil))

	var body providerAccountEnvelope
	if err := json.Unmarshal(recorder.Body.Bytes(), &body); err != nil {
		t.Fatal(err)
	}
	if len(body.Accounts) != 1 || body.Accounts[0].OfficialAvailable {
		t.Fatalf("expected one unavailable account, got %+v", body.Accounts)
	}
	account := body.Accounts[0]
	if account.RemainingCredits != nil || account.BalanceUSD != nil {
		t.Fatalf("unavailable official values must be null, got credits=%v balance=%v", account.RemainingCredits, account.BalanceUSD)
	}
	if account.Status != "UNAVAILABLE" || account.BalanceKind != "remaining" || account.Reason == "" {
		t.Fatalf("missing scope metadata: %+v", account)
	}
}

func TestProviderAccountsKeepsOfficialRemainingBalanceSeparate(t *testing.T) {
	t.Setenv("ANKR_ACCOUNT_REMAINING_CREDITS", "93000000")
	t.Setenv("ANKR_ACCOUNT_BALANCE_USD", "9.26")
	t.Setenv("ANKR_ACCOUNT_PLAN", "PAYG")
	t.Setenv("ANKR_ACCOUNT_SYNCED_AT", time.Now().UTC().Format(time.RFC3339))
	recorder := httptest.NewRecorder()
	(&Server{}).handleAPIControlProviderAccounts(recorder, httptest.NewRequest("GET", "/", nil))

	var body providerAccountEnvelope
	if err := json.Unmarshal(recorder.Body.Bytes(), &body); err != nil {
		t.Fatal(err)
	}
	account := body.Accounts[0]
	if !account.OfficialAvailable || account.Status != "AVAILABLE" {
		t.Fatalf("expected available official snapshot, got %+v", account)
	}
	if account.RemainingCredits == nil || *account.RemainingCredits != 93000000 || account.BalanceUSD == nil || *account.BalanceUSD != 9.26 {
		t.Fatalf("official remaining balance changed: %+v", account)
	}
}
