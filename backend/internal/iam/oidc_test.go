package iam

import (
	"context"
	"crypto"
	"crypto/rand"
	"crypto/rsa"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"errors"
	"math/big"
	"net/http"
	"net/http/httptest"
	"net/url"
	"testing"
	"time"
)

// 隔离测试：本地 httptest 假 IdP，不访问公网、不连接数据库。
// 覆盖 AUTH-011 的协议断言：state 签名、PKCE、nonce、issuer/audience/exp、签名算法。

type fakeIdP struct {
	server      *httptest.Server
	key         *rsa.PrivateKey
	kid         string
	nonce       string
	challenge   string
	signIDToken func(nonce string, exp time.Time) (string, error)
}

func newFakeIdP(t *testing.T) *fakeIdP {
	t.Helper()
	key, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatal(err)
	}
	f := &fakeIdP{key: key, kid: "test-key-1"}
	mux := http.NewServeMux()
	mux.HandleFunc("/.well-known/openid-configuration", func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewEncoder(w).Encode(map[string]any{
			"issuer":                 f.server.URL,
			"authorization_endpoint": f.server.URL + "/authorize",
			"token_endpoint":         f.server.URL + "/token",
			"jwks_uri":               f.server.URL + "/jwks",
		})
	})
	mux.HandleFunc("/jwks", func(w http.ResponseWriter, r *http.Request) {
		n := base64.RawURLEncoding.EncodeToString(f.key.N.Bytes())
		e := base64.RawURLEncoding.EncodeToString(big.NewInt(int64(f.key.E)).Bytes())
		_ = json.NewEncoder(w).Encode(map[string]any{
			"keys": []map[string]any{{"kty": "RSA", "kid": f.kid, "use": "sig", "alg": "RS256", "n": n, "e": e}},
		})
	})
	mux.HandleFunc("/token", func(w http.ResponseWriter, r *http.Request) {
		_ = r.ParseForm()
		verifier := r.Form.Get("code_verifier")
		if verifier == "" {
			http.Error(w, "missing verifier", http.StatusBadRequest)
			return
		}
		sum := sha256.Sum256([]byte(verifier))
		if base64.RawURLEncoding.EncodeToString(sum[:]) != f.challenge {
			http.Error(w, "bad verifier", http.StatusBadRequest)
			return
		}
		nonce := f.nonce
		if r.Form.Get("force_nonce") != "" {
			nonce = r.Form.Get("force_nonce")
		}
		token, err := f.signIDToken(nonce, time.Now().Add(time.Hour))
		if err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
		_ = json.NewEncoder(w).Encode(map[string]any{"access_token": "at", "token_type": "Bearer", "id_token": token})
	})
	f.server = httptest.NewServer(mux)
	f.signIDToken = f.signIDTokenImpl
	t.Cleanup(f.server.Close)
	return f
}

func (f *fakeIdP) signIDTokenImpl(nonce string, exp time.Time) (string, error) {
	header, _ := json.Marshal(map[string]any{"alg": "RS256", "kid": f.kid, "typ": "JWT"})
	claims, _ := json.Marshal(map[string]any{
		"iss": f.server.URL, "aud": "test-client", "sub": "user-1", "email": "alice@example.com",
		"email_verified": true, "name": "Alice", "nonce": nonce,
		"iat": time.Now().Unix(), "exp": exp.Unix(),
	})
	unsigned := base64.RawURLEncoding.EncodeToString(header) + "." + base64.RawURLEncoding.EncodeToString(claims)
	digest := sha256.Sum256([]byte(unsigned))
	sig, err := rsa.SignPKCS1v15(rand.Reader, f.key, crypto.SHA256, digest[:])
	if err != nil {
		return "", err
	}
	return unsigned + "." + base64.RawURLEncoding.EncodeToString(sig), nil
}

func newTestAdapter(f *fakeIdP) *OIDCAdapter {
	return NewOIDCAdapter(OIDCConfig{
		Enabled: true, Issuer: f.server.URL, ClientID: "test-client", ClientSecret: "test-secret",
		RedirectURL: "https://console.example.com/api/auth/v1/oidc/callback",
		Scopes:      []string{"openid", "email", "profile"},
	}, f.server.Client())
}

func TestOIDCHappyPath(t *testing.T) {
	f := newFakeIdP(t)
	adapter := newTestAdapter(f)

	req, err := adapter.BeginAuthorization("/home", AudienceUser)
	if err != nil {
		t.Fatalf("BeginAuthorization: %v", err)
	}
	u, err := url.Parse(req.URL)
	if err != nil {
		t.Fatal(err)
	}
	f.nonce = u.Query().Get("nonce")
	f.challenge = u.Query().Get("code_challenge")
	if f.nonce == "" || f.challenge == "" {
		t.Fatal("authorization URL missing nonce/code_challenge")
	}
	if u.Query().Get("code_challenge_method") != "S256" {
		t.Fatal("PKCE method must be S256")
	}
	result, err := adapter.HandleCallback(context.Background(), "auth-code", req.State)
	if err != nil {
		t.Fatalf("HandleCallback: %v", err)
	}
	if result.Subject != "user-1" || result.Email != "alice@example.com" || !result.EmailVerified {
		t.Fatalf("unexpected claims: %+v", result)
	}
	if result.ReturnTo != "/home" || result.Audience != AudienceUser {
		t.Fatalf("state payload lost: %+v", result)
	}
}

func TestOIDCRejectsTamperedState(t *testing.T) {
	f := newFakeIdP(t)
	adapter := newTestAdapter(f)
	req, err := adapter.BeginAuthorization("/home", AudienceUser)
	if err != nil {
		t.Fatal(err)
	}
	tampered := req.State[:len(req.State)-2] + "xx"
	if _, err := adapter.HandleCallback(context.Background(), "code", tampered); !errors.Is(err, ErrOIDCState) {
		t.Fatalf("want ErrOIDCState, got %v", err)
	}
}

func TestOIDCRejectsWrongNonce(t *testing.T) {
	f := newFakeIdP(t)
	adapter := newTestAdapter(f)
	req, err := adapter.BeginAuthorization("/home", AudienceUser)
	if err != nil {
		t.Fatal(err)
	}
	u, _ := url.Parse(req.URL)
	f.nonce = "wrong-nonce"
	f.challenge = u.Query().Get("code_challenge")
	if _, err := adapter.HandleCallback(context.Background(), "code", req.State); !errors.Is(err, ErrOIDCClaims) {
		t.Fatalf("want ErrOIDCClaims, got %v", err)
	}
}

func TestOIDCRejectsTamperedIDToken(t *testing.T) {
	f := newFakeIdP(t)
	adapter := newTestAdapter(f)
	req, err := adapter.BeginAuthorization("/home", AudienceUser)
	if err != nil {
		t.Fatal(err)
	}
	u, _ := url.Parse(req.URL)
	f.nonce = u.Query().Get("nonce")
	f.challenge = u.Query().Get("code_challenge")
	original := f.signIDToken
	f.signIDToken = func(nonce string, exp time.Time) (string, error) {
		token, err := original(nonce, exp)
		if err != nil {
			return "", err
		}
		return token[:len(token)-2] + "AA", nil
	}
	defer func() { f.signIDToken = original }()
	if _, err := adapter.HandleCallback(context.Background(), "code", req.State); !errors.Is(err, ErrOIDCToken) {
		t.Fatalf("want ErrOIDCToken, got %v", err)
	}
}

func TestOIDCRejectsExpiredToken(t *testing.T) {
	f := newFakeIdP(t)
	adapter := newTestAdapter(f)
	req, err := adapter.BeginAuthorization("/home", AudienceUser)
	if err != nil {
		t.Fatal(err)
	}
	u, _ := url.Parse(req.URL)
	f.nonce = u.Query().Get("nonce")
	f.challenge = u.Query().Get("code_challenge")
	original := f.signIDToken
	f.signIDToken = func(nonce string, _ time.Time) (string, error) {
		return original(nonce, time.Now().Add(-time.Hour))
	}
	defer func() { f.signIDToken = original }()
	if _, err := adapter.HandleCallback(context.Background(), "code", req.State); !errors.Is(err, ErrOIDCClaims) {
		t.Fatalf("want ErrOIDCClaims, got %v", err)
	}
}

func TestOIDCDisabledKeepsProductionClosed(t *testing.T) {
	adapter := NewOIDCAdapter(OIDCConfig{Enabled: true, Issuer: "https://idp.example.com"}, nil)
	if adapter.Enabled() {
		t.Fatal("partial OIDC config must not be considered enabled")
	}
	if _, err := adapter.BeginAuthorization("/home", AudienceUser); !errors.Is(err, ErrOIDCDisabled) {
		t.Fatalf("want ErrOIDCDisabled, got %v", err)
	}
	if _, err := adapter.HandleCallback(context.Background(), "code", "state"); !errors.Is(err, ErrOIDCDisabled) {
		t.Fatalf("want ErrOIDCDisabled, got %v", err)
	}
	if enabled, err := adapter.Methods(context.Background()); enabled || !errors.Is(err, ErrOIDCDisabled) {
		t.Fatalf("Methods must report disabled, got enabled=%v err=%v", enabled, err)
	}
}

func TestOIDCRejectsNoneAlg(t *testing.T) {
	f := newFakeIdP(t)
	adapter := newTestAdapter(f)
	if _, err := adapter.verifyIDToken(context.Background(), "eyJhbGciOiJub25lIn0.e30.", "nonce"); !errors.Is(err, ErrOIDCToken) {
		t.Fatalf("want ErrOIDCToken for alg=none, got %v", err)
	}
}
