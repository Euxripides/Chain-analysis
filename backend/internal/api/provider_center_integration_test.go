package api

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/blockchain-platform/blockchain-platform/internal/dbtx"
	"github.com/blockchain-platform/blockchain-platform/internal/policy"
	"github.com/blockchain-platform/blockchain-platform/internal/provider/providercontrol"
	"github.com/blockchain-platform/blockchain-platform/internal/store"
	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
)

func TestProviderCenterConfigSavesPlatformKeyWithoutReasonOrPassword(t *testing.T) {
	dsn := os.Getenv("PROVIDER_CONTROL_ISOLATED_TEST_DSN")
	if dsn == "" {
		t.Skip("isolated provider control database not configured")
	}
	ctx := context.Background()
	pool, err := pgxpool.New(ctx, dsn)
	if err != nil {
		t.Fatal(err)
	}
	defer pool.Close()
	var database string
	if err := pool.QueryRow(ctx, `SELECT current_database()`).Scan(&database); err != nil {
		t.Fatal(err)
	}
	if database != "provider_center_test" && database != "provider_center_test_20260930" {
		t.Fatalf("refusing to mutate non-test database %q", database)
	}
	if _, err := pool.Exec(ctx, `DELETE FROM provider_runtime_settings WHERE provider_key='ankr'`); err != nil {
		t.Fatal(err)
	}
	defer func() {
		_, _ = pool.Exec(context.Background(), `DELETE FROM provider_runtime_settings WHERE provider_key='ankr'`)
	}()
	control := providercontrol.New(dbtx.New(pool), "isolated-test-encryption-key-123456789")
	s := &Server{deps: Deps{Store: &store.Store{Pool: dbtx.New(pool)}}, providerControl: control}
	router := chi.NewRouter()
	router.Put("/provider-center/{id}/config", s.handleProviderCenterConfig)
	key := "isolated-platform-key"
	body := []byte(`{"enabled":false,"endpoints":{},"revision":0,"platform_key":"` + key + `"}`)
	send := func(permissions ...string) *httptest.ResponseRecorder {
		recorder := httptest.NewRecorder()
		request := httptest.NewRequest(http.MethodPut, "/provider-center/ankr/config", bytes.NewReader(body))
		request = request.WithContext(withPrincipal(request.Context(), policy.Principal{
			Authenticated: true, Permissions: permissions,
		}))
		router.ServeHTTP(recorder, request)
		return recorder
	}
	if response := send("admin:provider:update"); response.Code != http.StatusForbidden {
		t.Fatalf("key write without rotate permission returned %d: %s", response.Code, response.Body.String())
	}
	response := send("admin:provider:update", "admin:provider:key:rotate")
	if response.Code != http.StatusOK {
		t.Fatalf("key write without reason or password returned %d: %s", response.Code, response.Body.String())
	}
	var ciphertext []byte
	if err := pool.QueryRow(ctx, `SELECT platform_ciphertext FROM provider_runtime_settings WHERE provider_key='ankr'`).Scan(&ciphertext); err != nil {
		t.Fatal(err)
	}
	if len(ciphertext) == 0 || string(ciphertext) == key {
		t.Fatal("platform key was not stored encrypted")
	}
}

func TestProviderCenterReturnsPersistedOfficialAndLocalData(t *testing.T) {
	dsn := os.Getenv("PROVIDER_CONTROL_ISOLATED_TEST_DSN")
	if dsn == "" {
		t.Skip("isolated provider control database not configured")
	}
	ctx := context.Background()
	pool, err := pgxpool.New(ctx, dsn)
	if err != nil {
		t.Fatal(err)
	}
	defer pool.Close()
	var database string
	if err := pool.QueryRow(ctx, `SELECT current_database()`).Scan(&database); err != nil {
		t.Fatal(err)
	}
	if database != "provider_center_test" && database != "provider_center_test_20260930" {
		t.Fatalf("refusing to mutate non-test database %q", database)
	}
	for _, statement := range []string{
		`DELETE FROM provider_runtime_settings WHERE provider_key='drpc'`,
		`DELETE FROM provider_official_snapshots WHERE provider_key='drpc'`,
		`DELETE FROM provider_usage WHERE provider='drpc'`,
	} {
		if _, err := pool.Exec(ctx, statement); err != nil {
			t.Fatal(err)
		}
	}
	defer func() {
		_, _ = pool.Exec(context.Background(), `DELETE FROM provider_runtime_settings WHERE provider_key='drpc'`)
		_, _ = pool.Exec(context.Background(), `DELETE FROM provider_official_snapshots WHERE provider_key='drpc'`)
		_, _ = pool.Exec(context.Background(), `DELETE FROM provider_usage WHERE provider='drpc'`)
	}()
	control := providercontrol.New(dbtx.New(pool), "isolated-test-encryption-key-123456789")
	key := "hidden-runtime-key"
	if _, err := control.Save(ctx, "drpc", 0, providercontrol.Update{Enabled: true,
		Endpoints: map[string]string{"solana_rpc": "https://lb.drpc.live/solana/{key}"}, RuntimeKey: &key}); err != nil {
		t.Fatal(err)
	}
	s := &Server{deps: Deps{Store: &store.Store{Pool: dbtx.New(pool)}}, providerControl: control}
	if err := s.saveOfficialSnapshot(ctx, providercontrol.OfficialSnapshot{Provider: "drpc", ConnectionStatus: "available",
		Balance: "19.26", BalanceUnit: "provider_unit", Keys: []map[string]any{}, Usage: map[string]any{"requests": 534},
		Source: "dRPC Statistics API", SyncedAt: time.Now().UTC()}); err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(ctx, `INSERT INTO provider_usage(id,provider,request_time,method,chain,status,http_status,duration_ms,error_class)
		VALUES($1,'drpc',now(),'getHealth','solana','success',200,42,'')`, uuid.NewString()); err != nil {
		t.Fatal(err)
	}
	router := chi.NewRouter()
	router.Get("/provider-center/{id}", s.handleProviderCenter)
	recorder := httptest.NewRecorder()
	router.ServeHTTP(recorder, httptest.NewRequest(http.MethodGet, "/provider-center/drpc", nil))
	if recorder.Code != http.StatusOK {
		t.Fatalf("GET returned %d: %s", recorder.Code, recorder.Body.String())
	}
	if strings.Contains(recorder.Body.String(), key) {
		t.Fatal("runtime key leaked in GET response")
	}
	var payload struct {
		Data struct {
			Config struct {
				Enabled              bool `json:"enabled"`
				RuntimeKeyConfigured bool `json:"runtime_key_configured"`
			} `json:"config"`
			Official struct {
				Balance          string `json:"balance"`
				ConnectionStatus string `json:"connection_status"`
			} `json:"official"`
			Local struct {
				Calls24H    int   `json:"calls_24h"`
				RecentCalls []any `json:"recent_calls"`
			} `json:"local"`
		} `json:"data"`
	}
	if err := json.Unmarshal(recorder.Body.Bytes(), &payload); err != nil {
		t.Fatal(err)
	}
	if !payload.Data.Config.Enabled || !payload.Data.Config.RuntimeKeyConfigured || payload.Data.Official.Balance != "19.26" ||
		payload.Data.Official.ConnectionStatus != "available" || payload.Data.Local.Calls24H != 1 || len(payload.Data.Local.RecentCalls) != 1 {
		t.Fatalf("unexpected provider center response: %+v", payload.Data)
	}
}

func TestProviderCenterShowsExpiredAnkrSnapshot(t *testing.T) {
	dsn := os.Getenv("PROVIDER_CONTROL_ISOLATED_TEST_DSN")
	if dsn == "" {
		t.Skip("isolated provider control database not configured")
	}
	ctx := context.Background()
	pool, err := pgxpool.New(ctx, dsn)
	if err != nil {
		t.Fatal(err)
	}
	defer pool.Close()
	var database string
	if err := pool.QueryRow(ctx, `SELECT current_database()`).Scan(&database); err != nil {
		t.Fatal(err)
	}
	if database != "provider_center_test" && database != "provider_center_test_20260930" {
		t.Fatalf("refusing to mutate non-test database %q", database)
	}
	if _, err := pool.Exec(ctx, `DELETE FROM provider_official_snapshots WHERE provider_key='ankr'`); err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(ctx, `UPDATE ankr_management_connection SET balance_usd=123.456789,
		remaining_credits=78.123456,last_synced_at=now()-interval '2 days',
		token_expires_at=now()-interval '1 day' WHERE id=1`); err != nil {
		t.Fatal(err)
	}
	defer func() {
		_, _ = pool.Exec(context.Background(), `UPDATE ankr_management_connection SET balance_usd=NULL,
			remaining_credits=NULL,last_synced_at=NULL,token_expires_at=NULL WHERE id=1`)
	}()
	s := &Server{deps: Deps{Store: &store.Store{Pool: dbtx.New(pool)}},
		providerControl: providercontrol.New(dbtx.New(pool), "isolated-test-encryption-key-123456789")}
	router := chi.NewRouter()
	router.Get("/provider-center/{id}", s.handleProviderCenter)
	recorder := httptest.NewRecorder()
	router.ServeHTTP(recorder, httptest.NewRequest(http.MethodGet, "/provider-center/ankr", nil))
	if recorder.Code != http.StatusOK {
		t.Fatalf("GET returned %d: %s", recorder.Code, recorder.Body.String())
	}
	var payload struct {
		Data struct {
			Official struct {
				ConnectionStatus string `json:"connection_status"`
				Balance          string `json:"balance"`
				BalanceUnit      string `json:"balance_unit"`
				ErrorCode        string `json:"error_code"`
			} `json:"official"`
		} `json:"data"`
	}
	if err := json.Unmarshal(recorder.Body.Bytes(), &payload); err != nil {
		t.Fatal(err)
	}
	got := payload.Data.Official
	if got.ConnectionStatus != "expired" || got.Balance != "123.456789" ||
		got.BalanceUnit != "USD" || got.ErrorCode != "authorization_expired" {
		t.Fatalf("unexpected Ankr historical snapshot: %+v", got)
	}
	if err := s.saveOfficialSnapshot(ctx, providercontrol.OfficialSnapshot{Provider: "ankr", ConnectionStatus: "available",
		Balance: "123.456789", BalanceUnit: "USD", Keys: []map[string]any{}, Usage: map[string]any{},
		Source: "Ankr Management MCP", SyncedAt: time.Now().UTC()}); err != nil {
		t.Fatal(err)
	}
	defer func() {
		_, _ = pool.Exec(context.Background(), `DELETE FROM provider_official_snapshots WHERE provider_key='ankr'`)
	}()
	recorder = httptest.NewRecorder()
	router.ServeHTTP(recorder, httptest.NewRequest(http.MethodGet, "/provider-center/ankr", nil))
	if recorder.Code != http.StatusOK {
		t.Fatalf("GET persisted snapshot returned %d: %s", recorder.Code, recorder.Body.String())
	}
	if err := json.Unmarshal(recorder.Body.Bytes(), &payload); err != nil {
		t.Fatal(err)
	}
	if payload.Data.Official.ConnectionStatus != "expired" || payload.Data.Official.ErrorCode != "authorization_expired" {
		t.Fatalf("expired MCP authorization was shown as available: %+v", payload.Data.Official)
	}
}
