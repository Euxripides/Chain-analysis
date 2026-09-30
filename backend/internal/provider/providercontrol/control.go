package providercontrol

import (
	"context"
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/url"
	"strings"
	"sync"
	"time"

	"github.com/blockchain-platform/blockchain-platform/internal/dbtx"
	"github.com/jackc/pgx/v5"
)

// State is an effective runtime configuration. Its credentials must never be
// serialized, logged, or returned by an admin read endpoint.
type State struct {
	Enabled           bool
	Endpoints         map[string]string
	RuntimeKey        string
	StatisticsToken   string
	KeysToken         string
	PlatformKey       string
	PlatformExpiresAt *time.Time
	Revision          int64
}

type Update struct {
	Enabled           bool
	Endpoints         map[string]string
	RuntimeKey        *string
	StatisticsToken   *string
	KeysToken         *string
	PlatformKey       *string
	PlatformExpiresAt *time.Time
}

var ErrRevisionConflict = errors.New("provider configuration revision has changed")

type cached struct {
	state   State
	expires time.Time
}

type Controller struct {
	pool  *dbtx.Pool
	aead  cipher.AEAD
	mu    sync.Mutex
	cache map[string]cached
}

func New(pool *dbtx.Pool, encryptionKey string) *Controller {
	c := &Controller{pool: pool, cache: make(map[string]cached)}
	if len(encryptionKey) >= 32 {
		key := sha256.Sum256([]byte(encryptionKey))
		if block, err := aes.NewCipher(key[:]); err == nil {
			c.aead, _ = cipher.NewGCM(block)
		}
	}
	return c
}

func (c *Controller) CredentialWritesAvailable() bool { return c != nil && c.aead != nil }

func (c *Controller) Invalidate(key string) {
	if c == nil {
		return
	}
	c.mu.Lock()
	delete(c.cache, key)
	c.mu.Unlock()
}

func clone(s State) State {
	copy := s
	copy.Endpoints = make(map[string]string, len(s.Endpoints))
	if s.PlatformExpiresAt != nil {
		at := *s.PlatformExpiresAt
		copy.PlatformExpiresAt = &at
	}
	for k, v := range s.Endpoints {
		copy.Endpoints[k] = v
	}
	return copy
}

// Resolve checks PostgreSQL at most once per second per process. Every API and
// worker process uses the same table, so a saved switch or key reaches all
// request paths without a service restart. A database failure fails closed.
func (c *Controller) Resolve(ctx context.Context, provider string, defaults State) (State, error) {
	if c == nil || c.pool == nil {
		return clone(defaults), nil
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	if item, ok := c.cache[provider]; ok && time.Now().Before(item.expires) {
		return clone(item.state), nil
	}
	state := clone(defaults)
	var enabled bool
	var endpoints, runtimeCipher, runtimeNonce, statsCipher, statsNonce, keysCipher, keysNonce, platformCipher, platformNonce []byte
	var platformExpiresAt *time.Time
	var revision int64
	err := c.pool.QueryRow(ctx, `SELECT enabled,endpoints,runtime_ciphertext,runtime_nonce,
		statistics_ciphertext,statistics_nonce,keys_ciphertext,keys_nonce,
		platform_ciphertext,platform_nonce,platform_expires_at,revision
		FROM provider_runtime_settings WHERE provider_key=$1`, provider).Scan(
		&enabled, &endpoints, &runtimeCipher, &runtimeNonce, &statsCipher, &statsNonce, &keysCipher, &keysNonce,
		&platformCipher, &platformNonce, &platformExpiresAt, &revision)
	if errors.Is(err, pgx.ErrNoRows) {
		c.cache[provider] = cached{state: state, expires: time.Now().Add(time.Second)}
		return clone(state), nil
	}
	if err != nil {
		return State{}, fmt.Errorf("load provider control: %w", err)
	}
	state.Enabled, state.Revision, state.PlatformExpiresAt = enabled, revision, platformExpiresAt
	var stored map[string]string
	if err := json.Unmarshal(endpoints, &stored); err != nil {
		return State{}, fmt.Errorf("decode provider endpoints: %w", err)
	}
	for k, v := range stored {
		state.Endpoints[k] = v
	}
	if len(runtimeCipher) > 0 {
		state.RuntimeKey, err = c.open(runtimeCipher, runtimeNonce)
		if err != nil {
			return State{}, err
		}
	}
	if len(statsCipher) > 0 {
		state.StatisticsToken, err = c.open(statsCipher, statsNonce)
		if err != nil {
			return State{}, err
		}
	}
	if len(keysCipher) > 0 {
		state.KeysToken, err = c.open(keysCipher, keysNonce)
		if err != nil {
			return State{}, err
		}
	}
	if len(platformCipher) > 0 {
		state.PlatformKey, err = c.open(platformCipher, platformNonce)
		if err != nil {
			return State{}, err
		}
	}
	c.cache[provider] = cached{state: state, expires: time.Now().Add(time.Second)}
	return clone(state), nil
}

func (c *Controller) open(ciphertext, nonce []byte) (string, error) {
	if c.aead == nil || len(nonce) != c.aead.NonceSize() {
		return "", errors.New("provider credential encryption is unavailable")
	}
	plain, err := c.aead.Open(nil, nonce, ciphertext, nil)
	if err != nil {
		return "", errors.New("provider credential cannot be decrypted")
	}
	return string(plain), nil
}

func (c *Controller) seal(value *string) ([]byte, []byte, error) {
	if value == nil {
		return nil, nil, nil
	}
	if c.aead == nil {
		return nil, nil, errors.New("PROVIDER_CONTROL_ENCRYPTION_KEY must contain at least 32 characters")
	}
	if strings.TrimSpace(*value) == "" {
		return nil, nil, errors.New("credential cannot be empty")
	}
	nonce := make([]byte, c.aead.NonceSize())
	if _, err := io.ReadFull(rand.Reader, nonce); err != nil {
		return nil, nil, err
	}
	return c.aead.Seal(nil, nonce, []byte(*value), nil), nonce, nil
}

func ValidateEndpoints(provider string, endpoints map[string]string) error {
	allowed := map[string]bool{}
	switch provider {
	case "ankr":
		allowed = map[string]bool{"advanced": true, "rpc": true, "tron_rpc": true, "tron_rest": true}
	case "drpc":
		allowed = map[string]bool{"solana_rpc": true, "solana_archive": true}
	default:
		return errors.New("unsupported provider")
	}
	for channel, raw := range endpoints {
		if !allowed[channel] {
			return fmt.Errorf("unsupported %s endpoint channel", channel)
		}
		if raw == "" {
			continue
		}
		parsed, err := url.Parse(raw)
		if err != nil || len(raw) > 2048 || parsed.Scheme != "https" || parsed.User != nil || parsed.RawQuery != "" || parsed.Fragment != "" ||
			(parsed.Port() != "" && parsed.Port() != "443") || parsed.Opaque != "" || strings.ContainsAny(raw, " \r\n\t") {
			return fmt.Errorf("invalid %s endpoint", channel)
		}
		host := strings.ToLower(parsed.Hostname())
		if provider == "ankr" && host != "rpc.ankr.com" {
			return fmt.Errorf("%s endpoint must use rpc.ankr.com", channel)
		}
		if provider == "drpc" && host != "drpc.live" && !strings.HasSuffix(host, ".drpc.live") {
			return fmt.Errorf("%s endpoint must use dRPC", channel)
		}
		path := parsed.Path
		switch channel {
		case "advanced":
			if path != "/multichain" {
				return errors.New("Ankr Advanced endpoint must use /multichain")
			}
		case "rpc":
			if path != "/{chain}/{key}" {
				return errors.New("Ankr RPC endpoint must use /{chain}/{key}")
			}
		case "tron_rpc":
			if path != "/tron_jsonrpc/{key}" {
				return errors.New("Ankr TRON RPC endpoint is invalid")
			}
		case "tron_rest":
			if path != "/premium-http/tron/{key}" {
				return errors.New("Ankr TRON REST endpoint is invalid")
			}
		case "solana_rpc", "solana_archive":
			if path != "/solana/{key}" {
				return errors.New("dRPC Solana endpoint must use /solana/{key}")
			}
		}
	}
	return nil
}

// PublicEndpoints removes literal credentials from legacy environment URLs.
// Only known provider templates are returned to the browser or audit log.
func PublicEndpoints(provider string, endpoints map[string]string) map[string]string {
	out := make(map[string]string, len(endpoints))
	for channel, raw := range endpoints {
		if raw == "" {
			out[channel] = ""
			continue
		}
		if ValidateEndpoints(provider, map[string]string{channel: raw}) == nil {
			out[channel] = raw
			continue
		}
		parsed, err := url.Parse(raw)
		if err != nil || parsed.Scheme != "https" || parsed.User != nil || parsed.RawQuery != "" || parsed.Fragment != "" {
			out[channel] = ""
			continue
		}
		path := parsed.Path
		switch channel {
		case "rpc", "tron_rpc", "tron_rest", "solana_rpc", "solana_archive":
			pos := strings.LastIndex(path, "/")
			if pos >= 0 && pos < len(path)-1 {
				path = path[:pos+1] + "{key}"
			}
		}
		candidate := "https://" + parsed.Host + path
		if ValidateEndpoints(provider, map[string]string{channel: candidate}) == nil {
			out[channel] = candidate
		} else {
			out[channel] = ""
		}
	}
	return out
}

// Save uses optimistic concurrency. A nil credential keeps the current value;
// credentials are accepted only as write-only input and never returned.
func (c *Controller) Save(ctx context.Context, provider string, expected int64, update Update) (int64, error) {
	if c == nil || c.pool == nil {
		return 0, errors.New("provider control is unavailable")
	}
	if err := ValidateEndpoints(provider, update.Endpoints); err != nil {
		return 0, err
	}
	if provider != "ankr" && (update.PlatformKey != nil || update.PlatformExpiresAt != nil) {
		return 0, errors.New("platform key is only available for Ankr")
	}
	if update.PlatformExpiresAt != nil && !update.PlatformExpiresAt.After(time.Now()) {
		return 0, errors.New("platform key expiry must be in the future")
	}
	endpoints, err := json.Marshal(update.Endpoints)
	if err != nil {
		return 0, err
	}
	rc, rn, err := c.seal(update.RuntimeKey)
	if err != nil {
		return 0, err
	}
	sc, sn, err := c.seal(update.StatisticsToken)
	if err != nil {
		return 0, err
	}
	kc, kn, err := c.seal(update.KeysToken)
	if err != nil {
		return 0, err
	}
	pc, pn, err := c.seal(update.PlatformKey)
	if err != nil {
		return 0, err
	}
	var revision int64
	if expected == 0 {
		err = c.pool.QueryRow(ctx, `INSERT INTO provider_runtime_settings
			(provider_key,enabled,endpoints,runtime_ciphertext,runtime_nonce,statistics_ciphertext,statistics_nonce,
			keys_ciphertext,keys_nonce,platform_ciphertext,platform_nonce,platform_expires_at)
			VALUES($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12) ON CONFLICT(provider_key) DO NOTHING RETURNING revision`,
			provider, update.Enabled, endpoints, rc, rn, sc, sn, kc, kn, pc, pn, update.PlatformExpiresAt).Scan(&revision)
	} else {
		err = c.pool.QueryRow(ctx, `UPDATE provider_runtime_settings SET enabled=$2,endpoints=$3,
			runtime_ciphertext=coalesce($4,runtime_ciphertext),runtime_nonce=coalesce($5,runtime_nonce),
			statistics_ciphertext=coalesce($6,statistics_ciphertext),statistics_nonce=coalesce($7,statistics_nonce),
			keys_ciphertext=coalesce($8,keys_ciphertext),keys_nonce=coalesce($9,keys_nonce),
			platform_ciphertext=coalesce($10,platform_ciphertext),platform_nonce=coalesce($11,platform_nonce),
			platform_expires_at=CASE WHEN $10 IS NOT NULL THEN $12 ELSE coalesce($12,platform_expires_at) END,
			revision=revision+1,updated_at=now() WHERE provider_key=$1 AND revision=$13 RETURNING revision`,
			provider, update.Enabled, endpoints, rc, rn, sc, sn, kc, kn, pc, pn, update.PlatformExpiresAt, expected).Scan(&revision)
	}
	if errors.Is(err, pgx.ErrNoRows) {
		return 0, ErrRevisionConflict
	}
	if err != nil {
		return 0, err
	}
	c.Invalidate(provider)
	return revision, nil
}
