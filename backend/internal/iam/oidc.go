package iam

import (
	"context"
	"crypto"
	"crypto/hmac"
	"crypto/rand"
	"crypto/rsa"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math/big"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"time"
)

// OIDCConfig 是 OIDC Authorization Code + PKCE 适配器配置。
// 未配置完整时 Enabled=false：生产环境必须保持禁用，接口返回 NOT_IMPLEMENTED/未配置，
// 不得在没有真实 IdP 的情况下伪装成可用登录方式（AD-004、AUTH-011）。
type OIDCConfig struct {
	Enabled      bool
	Issuer       string
	ClientID     string
	ClientSecret string
	RedirectURL  string
	Scopes       []string
}

// Configured 表示四要素齐全且 issuer/redirect 为 http(s) URL。
func (c OIDCConfig) Configured() bool {
	if !c.Enabled {
		return false
	}
	if strings.TrimSpace(c.Issuer) == "" || strings.TrimSpace(c.ClientID) == "" ||
		strings.TrimSpace(c.ClientSecret) == "" || strings.TrimSpace(c.RedirectURL) == "" {
		return false
	}
	issuer, err1 := url.Parse(c.Issuer)
	redirect, err2 := url.Parse(c.RedirectURL)
	return err1 == nil && err2 == nil && issuer.Scheme != "" && redirect.Scheme != ""
}

type oidcDiscovery struct {
	Issuer                string `json:"issuer"`
	AuthorizationEndpoint string `json:"authorization_endpoint"`
	TokenEndpoint         string `json:"token_endpoint"`
	JWKSURI               string `json:"jwks_uri"`
}

type oidcJWKS struct {
	Keys []struct {
		Kty string `json:"kty"`
		Kid string `json:"kid"`
		Use string `json:"use"`
		Alg string `json:"alg"`
		N   string `json:"n"`
		E   string `json:"e"`
	} `json:"keys"`
}

// OIDCAdapter 实现 OIDC 协议的最小完整闭环：discovery、PKCE、state、token exchange、
// ID Token 签名/issuer/audience/exp/nonce 校验。它不负责身份自动开通：
// 回调完成后由 API 层按“已验证邮箱匹配既有 identity”的策略建立 Session。
type OIDCAdapter struct {
	cfg    OIDCConfig
	client *http.Client

	mu        sync.Mutex
	discovery *oidcDiscovery
	jwks      *oidcJWKS
	cachedAt  time.Time
}

func NewOIDCAdapter(cfg OIDCConfig, client *http.Client) *OIDCAdapter {
	if client == nil {
		client = &http.Client{Timeout: 10 * time.Second}
	}
	return &OIDCAdapter{cfg: cfg, client: client}
}

func (a *OIDCAdapter) Enabled() bool { return a != nil && a.cfg.Configured() }

var (
	ErrOIDCDisabled = errors.New("oidc is not configured")
	ErrOIDCProtocol = errors.New("oidc protocol error")
	ErrOIDCState    = errors.New("oidc state invalid")
	ErrOIDCToken    = errors.New("oidc id token invalid")
	ErrOIDCClaims   = errors.New("oidc claims invalid")
)

// Methods 返回不含 secret 的能力描述；discovery 失败时不暴露内部错误。
func (a *OIDCAdapter) Methods(ctx context.Context) (bool, error) {
	if !a.Enabled() {
		return false, ErrOIDCDisabled
	}
	if _, err := a.loadDiscovery(ctx); err != nil {
		return true, err
	}
	return true, nil
}

type AuthRequest struct {
	URL   string
	State string
}

type oidcState struct {
	Nonce    string `json:"nonce"`
	Verifier string `json:"verifier"`
	ReturnTo string `json:"return_to"`
	Audience string `json:"audience"`
	IssuedAt int64  `json:"iat"`
	Expires  int64  `json:"exp"`
}

type CallbackResult struct {
	Subject       string
	Email         string
	EmailVerified bool
	DisplayName   string
	ReturnTo      string
	Audience      string
	Claims        map[string]any
}

func randomURLSafe(n int) (string, error) {
	buf := make([]byte, n)
	if _, err := rand.Read(buf); err != nil {
		return "", err
	}
	return base64.RawURLEncoding.EncodeToString(buf), nil
}

func (a *OIDCAdapter) BeginAuthorization(returnTo, audience string) (AuthRequest, error) {
	if !a.Enabled() {
		return AuthRequest{}, ErrOIDCDisabled
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	discovery, err := a.loadDiscovery(ctx)
	if err != nil {
		return AuthRequest{}, err
	}
	nonce, err := randomURLSafe(24)
	if err != nil {
		return AuthRequest{}, err
	}
	verifier, err := randomURLSafe(32)
	if err != nil {
		return AuthRequest{}, err
	}
	sum := sha256.Sum256([]byte(verifier))
	challenge := base64.RawURLEncoding.EncodeToString(sum[:])
	now := time.Now().UTC()
	payload, err := json.Marshal(oidcState{
		Nonce: nonce, Verifier: verifier, ReturnTo: returnTo, Audience: audience,
		IssuedAt: now.Unix(), Expires: now.Add(10 * time.Minute).Unix(),
	})
	if err != nil {
		return AuthRequest{}, err
	}
	state, err := a.signState(payload)
	if err != nil {
		return AuthRequest{}, err
	}
	scopes := a.cfg.Scopes
	if len(scopes) == 0 {
		scopes = []string{"openid", "email", "profile"}
	}
	q := url.Values{}
	q.Set("response_type", "code")
	q.Set("client_id", a.cfg.ClientID)
	q.Set("redirect_uri", a.cfg.RedirectURL)
	q.Set("scope", strings.Join(scopes, " "))
	q.Set("state", state)
	q.Set("nonce", nonce)
	q.Set("code_challenge", challenge)
	q.Set("code_challenge_method", "S256")
	authURL := discovery.AuthorizationEndpoint
	if strings.Contains(authURL, "?") {
		authURL += "&" + q.Encode()
	} else {
		authURL += "?" + q.Encode()
	}
	return AuthRequest{URL: authURL, State: state}, nil
}

func (a *OIDCAdapter) signState(payload []byte) (string, error) {
	mac := hmac.New(sha256.New, []byte(a.cfg.ClientSecret))
	mac.Write(payload)
	return base64.RawURLEncoding.EncodeToString(payload) + "." + base64.RawURLEncoding.EncodeToString(mac.Sum(nil)), nil
}

func (a *OIDCAdapter) parseState(state string) (oidcState, error) {
	var out oidcState
	parts := strings.Split(state, ".")
	if len(parts) != 2 {
		return out, ErrOIDCState
	}
	payload, err := base64.RawURLEncoding.DecodeString(parts[0])
	if err != nil {
		return out, ErrOIDCState
	}
	signature, err := base64.RawURLEncoding.DecodeString(parts[1])
	if err != nil {
		return out, ErrOIDCState
	}
	mac := hmac.New(sha256.New, []byte(a.cfg.ClientSecret))
	mac.Write(payload)
	if !hmac.Equal(signature, mac.Sum(nil)) {
		return out, ErrOIDCState
	}
	if err := json.Unmarshal(payload, &out); err != nil {
		return out, ErrOIDCState
	}
	if out.Expires < time.Now().UTC().Unix() {
		return out, ErrOIDCState
	}
	if out.Audience != AudienceUser && out.Audience != AudienceAdmin {
		return out, ErrOIDCState
	}
	return out, nil
}

func (a *OIDCAdapter) HandleCallback(ctx context.Context, code, state string) (CallbackResult, error) {
	if !a.Enabled() {
		return CallbackResult{}, ErrOIDCDisabled
	}
	if strings.TrimSpace(code) == "" {
		return CallbackResult{}, ErrOIDCProtocol
	}
	st, err := a.parseState(state)
	if err != nil {
		return CallbackResult{}, err
	}
	discovery, err := a.loadDiscovery(ctx)
	if err != nil {
		return CallbackResult{}, err
	}
	form := url.Values{}
	form.Set("grant_type", "authorization_code")
	form.Set("code", code)
	form.Set("redirect_uri", a.cfg.RedirectURL)
	form.Set("client_id", a.cfg.ClientID)
	form.Set("client_secret", a.cfg.ClientSecret)
	form.Set("code_verifier", st.Verifier)
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, discovery.TokenEndpoint, strings.NewReader(form.Encode()))
	if err != nil {
		return CallbackResult{}, err
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.Header.Set("Accept", "application/json")
	resp, err := a.client.Do(req)
	if err != nil {
		return CallbackResult{}, fmt.Errorf("%w: token endpoint: %v", ErrOIDCProtocol, err)
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return CallbackResult{}, fmt.Errorf("%w: token endpoint status %d", ErrOIDCProtocol, resp.StatusCode)
	}
	var token struct {
		AccessToken string `json:"access_token"`
		IDToken     string `json:"id_token"`
		TokenType   string `json:"token_type"`
	}
	if err := json.Unmarshal(body, &token); err != nil || strings.TrimSpace(token.IDToken) == "" {
		return CallbackResult{}, fmt.Errorf("%w: missing id_token", ErrOIDCProtocol)
	}
	claims, err := a.verifyIDToken(ctx, token.IDToken, st.Nonce)
	if err != nil {
		return CallbackResult{}, err
	}
	subject, _ := claims["sub"].(string)
	email, _ := claims["email"].(string)
	if strings.TrimSpace(subject) == "" || strings.TrimSpace(email) == "" {
		return CallbackResult{}, fmt.Errorf("%w: sub/email required", ErrOIDCClaims)
	}
	verified, _ := claims["email_verified"].(bool)
	display, _ := claims["name"].(string)
	return CallbackResult{
		Subject: subject, Email: strings.ToLower(strings.TrimSpace(email)), EmailVerified: verified,
		DisplayName: display, ReturnTo: st.ReturnTo, Audience: st.Audience, Claims: claims,
	}, nil
}

func (a *OIDCAdapter) verifyIDToken(ctx context.Context, raw, nonce string) (map[string]any, error) {
	parts := strings.Split(raw, ".")
	if len(parts) != 3 {
		return nil, ErrOIDCToken
	}
	headerRaw, err := base64.RawURLEncoding.DecodeString(parts[0])
	if err != nil {
		return nil, ErrOIDCToken
	}
	var header struct {
		Alg string `json:"alg"`
		Kid string `json:"kid"`
		Typ string `json:"typ"`
	}
	if err := json.Unmarshal(headerRaw, &header); err != nil {
		return nil, ErrOIDCToken
	}
	if header.Alg != "RS256" {
		return nil, fmt.Errorf("%w: unsupported alg %q", ErrOIDCToken, header.Alg)
	}
	signature, err := base64.RawURLEncoding.DecodeString(parts[2])
	if err != nil {
		return nil, ErrOIDCToken
	}
	signed := []byte(parts[0] + "." + parts[1])
	digest := sha256.Sum256(signed)
	pub, err := a.publicKey(ctx, header.Kid)
	if err != nil {
		return nil, err
	}
	if err := rsa.VerifyPKCS1v15(pub, crypto.SHA256, digest[:], signature); err != nil {
		return nil, fmt.Errorf("%w: signature", ErrOIDCToken)
	}
	payloadRaw, err := base64.RawURLEncoding.DecodeString(parts[1])
	if err != nil {
		return nil, ErrOIDCToken
	}
	var claims map[string]any
	if err := json.Unmarshal(payloadRaw, &claims); err != nil {
		return nil, ErrOIDCToken
	}
	discovery, err := a.loadDiscovery(ctx)
	if err != nil {
		return nil, err
	}
	if iss, _ := claims["iss"].(string); iss != discovery.Issuer && iss != a.cfg.Issuer {
		return nil, fmt.Errorf("%w: issuer mismatch", ErrOIDCClaims)
	}
	if !audienceContains(claims["aud"], a.cfg.ClientID) {
		return nil, fmt.Errorf("%w: audience mismatch", ErrOIDCClaims)
	}
	now := time.Now().UTC().Unix()
	if exp, ok := numberClaim(claims["exp"]); !ok || exp < now-60 {
		return nil, fmt.Errorf("%w: token expired", ErrOIDCClaims)
	}
	if nbf, ok := numberClaim(claims["nbf"]); ok && nbf > now+60 {
		return nil, fmt.Errorf("%w: token not yet valid", ErrOIDCClaims)
	}
	if gotNonce, _ := claims["nonce"].(string); gotNonce != nonce {
		return nil, fmt.Errorf("%w: nonce mismatch", ErrOIDCClaims)
	}
	return claims, nil
}

func (a *OIDCAdapter) loadDiscovery(ctx context.Context) (*oidcDiscovery, error) {
	a.mu.Lock()
	if a.discovery != nil && time.Since(a.cachedAt) < 10*time.Minute {
		d := a.discovery
		a.mu.Unlock()
		return d, nil
	}
	a.mu.Unlock()
	endpoint := strings.TrimSuffix(a.cfg.Issuer, "/") + "/.well-known/openid-configuration"
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint, nil)
	if err != nil {
		return nil, err
	}
	resp, err := a.client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("%w: discovery: %v", ErrOIDCProtocol, err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("%w: discovery status %d", ErrOIDCProtocol, resp.StatusCode)
	}
	var d oidcDiscovery
	if err := json.NewDecoder(io.LimitReader(resp.Body, 1<<20)).Decode(&d); err != nil {
		return nil, fmt.Errorf("%w: discovery json", ErrOIDCProtocol)
	}
	if d.AuthorizationEndpoint == "" || d.TokenEndpoint == "" || d.JWKSURI == "" {
		return nil, fmt.Errorf("%w: discovery incomplete", ErrOIDCProtocol)
	}
	a.mu.Lock()
	a.discovery = &d
	a.cachedAt = time.Now()
	a.mu.Unlock()
	return &d, nil
}

func (a *OIDCAdapter) publicKey(ctx context.Context, kid string) (*rsa.PublicKey, error) {
	discovery, err := a.loadDiscovery(ctx)
	if err != nil {
		return nil, err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, discovery.JWKSURI, nil)
	if err != nil {
		return nil, err
	}
	resp, err := a.client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("%w: jwks: %v", ErrOIDCProtocol, err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("%w: jwks status %d", ErrOIDCProtocol, resp.StatusCode)
	}
	var jwks oidcJWKS
	if err := json.NewDecoder(io.LimitReader(resp.Body, 1<<20)).Decode(&jwks); err != nil {
		return nil, fmt.Errorf("%w: jwks json", ErrOIDCProtocol)
	}
	for _, key := range jwks.Keys {
		if key.Kty != "RSA" {
			continue
		}
		if kid != "" && key.Kid != "" && key.Kid != kid {
			continue
		}
		nb, err := base64.RawURLEncoding.DecodeString(key.N)
		if err != nil {
			continue
		}
		eb, err := base64.RawURLEncoding.DecodeString(key.E)
		if err != nil {
			continue
		}
		e := 0
		for _, b := range eb {
			e = e<<8 | int(b)
		}
		if e == 0 {
			continue
		}
		return &rsa.PublicKey{N: new(big.Int).SetBytes(nb), E: e}, nil
	}
	return nil, fmt.Errorf("%w: no matching JWKS key", ErrOIDCToken)
}

func audienceContains(v any, clientID string) bool {
	switch aud := v.(type) {
	case string:
		return aud == clientID
	case []any:
		for _, item := range aud {
			if s, ok := item.(string); ok && s == clientID {
				return true
			}
		}
	}
	return false
}

func numberClaim(v any) (int64, bool) {
	switch n := v.(type) {
	case float64:
		return int64(n), true
	case int64:
		return n, true
	case json.Number:
		i, err := n.Int64()
		return i, err == nil
	default:
		return 0, false
	}
}
