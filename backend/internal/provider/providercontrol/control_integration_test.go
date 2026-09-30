package providercontrol

import (
	"context"
	"os"
	"testing"
	"time"

	"github.com/blockchain-platform/blockchain-platform/internal/dbtx"
	"github.com/jackc/pgx/v5/pgxpool"
)

func TestControlPersistedSwitchAndSecret(t *testing.T) {
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

	key := "test-credential-123"
	platformKey := "test-platform-credential-456"
	expiresAt := time.Now().UTC().Add(30 * 24 * time.Hour).Truncate(time.Second)
	defaults := State{Enabled: true, Endpoints: map[string]string{"advanced": "https://rpc.ankr.com/multichain"}}
	writer := New(dbtx.New(pool), "isolated-test-encryption-key-123456789")
	revision, err := writer.Save(ctx, "ankr", 0, Update{Enabled: false, Endpoints: defaults.Endpoints,
		RuntimeKey: &key, PlatformKey: &platformKey, PlatformExpiresAt: &expiresAt})
	if err != nil || revision != 1 {
		t.Fatalf("first save: revision=%d err=%v", revision, err)
	}
	var cipher []byte
	var platformCipher []byte
	if err := pool.QueryRow(ctx, `SELECT runtime_ciphertext,platform_ciphertext FROM provider_runtime_settings WHERE provider_key='ankr'`).Scan(&cipher, &platformCipher); err != nil {
		t.Fatal(err)
	}
	if string(cipher) == key || string(platformCipher) == platformKey {
		t.Fatal("credential was stored in plaintext")
	}
	reader := New(dbtx.New(pool), "isolated-test-encryption-key-123456789")
	state, err := reader.Resolve(ctx, "ankr", defaults)
	if err != nil || state.Enabled || state.RuntimeKey != key || state.PlatformKey != platformKey ||
		state.PlatformExpiresAt == nil || !state.PlatformExpiresAt.Equal(expiresAt) {
		t.Fatalf("saved state not resolved: %+v, %v", state, err)
	}
	if _, err := writer.Save(ctx, "ankr", 0, Update{Enabled: true, Endpoints: defaults.Endpoints}); err == nil {
		t.Fatal("stale revision was accepted")
	}
	revision, err = writer.Save(ctx, "ankr", revision, Update{Enabled: true, Endpoints: defaults.Endpoints})
	if err != nil || revision != 2 {
		t.Fatalf("second save: revision=%d err=%v", revision, err)
	}
	time.Sleep(1100 * time.Millisecond)
	state, err = reader.Resolve(ctx, "ankr", defaults)
	if err != nil || !state.Enabled || state.RuntimeKey != key || state.PlatformKey != platformKey || state.Revision != 2 {
		t.Fatalf("reader did not observe update: %+v, %v", state, err)
	}
}
