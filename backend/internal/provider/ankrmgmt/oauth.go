package ankrmgmt

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"
)

type clientRegistration struct {
	ClientID string `json:"client_id"`
}

type oauthToken struct {
	AccessToken  string    `json:"access_token"`
	RefreshToken string    `json:"refresh_token,omitempty"`
	TokenType    string    `json:"token_type"`
	Scope        string    `json:"scope,omitempty"`
	ExpiresIn    int64     `json:"expires_in,omitempty"`
	ExpiresAt    time.Time `json:"expires_at,omitempty"`
}

type oauthServerMetadata struct {
	Issuer                            string `json:"issuer"`
	ClientIDMetadataDocumentSupported bool   `json:"client_id_metadata_document_supported"`
}

func (s *Service) metadataClientID(ctx context.Context) (string, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet,
		strings.TrimRight(s.cfg.IssuerURL, "/")+"/.well-known/oauth-authorization-server", nil)
	if err != nil {
		return "", err
	}
	req.Header.Set("Accept", "application/json")
	resp, err := s.http.Do(req)
	if err != nil {
		return "", fmt.Errorf("discover Ankr authorization server: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return "", sanitizedHTTPError("discover Ankr authorization server", resp)
	}
	var metadata oauthServerMetadata
	if err := json.NewDecoder(io.LimitReader(resp.Body, 1<<20)).Decode(&metadata); err != nil {
		return "", fmt.Errorf("decode Ankr authorization server metadata: %w", err)
	}
	if metadata.Issuer != strings.TrimRight(s.cfg.IssuerURL, "/") {
		return "", fmt.Errorf("Ankr authorization server issuer mismatch")
	}
	if !metadata.ClientIDMetadataDocumentSupported {
		return "", ErrClientMetadataUnsupported
	}
	return s.cfg.ClientMetadataURL, nil
}

func (s *Service) registerClient(ctx context.Context) (string, error) {
	applicationType := "web"
	if redirect, err := url.Parse(s.cfg.RedirectURI); err == nil && redirect.Scheme == "http" &&
		(redirect.Hostname() == "127.0.0.1" || redirect.Hostname() == "localhost" || redirect.Hostname() == "::1") {
		applicationType = "native"
	}
	body := map[string]any{
		"client_name":                "Blockchain Platform API Control Center",
		"application_type":           applicationType,
		"redirect_uris":              []string{s.cfg.RedirectURI},
		"grant_types":                []string{"authorization_code"},
		"response_types":             []string{"code"},
		"token_endpoint_auth_method": "none",
	}
	var out clientRegistration
	if err := s.doJSON(ctx, http.MethodPost, strings.TrimRight(s.cfg.IssuerURL, "/")+"/register", "", body, &out); err != nil {
		return "", fmt.Errorf("register Ankr OAuth client: %w", err)
	}
	if out.ClientID == "" {
		return "", fmt.Errorf("Ankr OAuth registration returned no client_id")
	}
	return out.ClientID, nil
}

func (s *Service) StartOAuth(ctx context.Context) (string, error) {
	if err := s.configurationError(); err != nil {
		return "", err
	}
	var clientID, registeredRedirect string
	_ = s.pool.QueryRow(ctx, `SELECT client_id,redirect_uri FROM ankr_management_connection WHERE id=1`).Scan(&clientID, &registeredRedirect)
	storedClientID := clientID
	if s.cfg.ClientMetadataURL != "" {
		var err error
		clientID, err = s.metadataClientID(ctx)
		if err != nil {
			return "", err
		}
	}
	if clientID == "" || registeredRedirect != s.cfg.RedirectURI || clientID != storedClientID {
		var err error
		if s.cfg.ClientMetadataURL == "" {
			clientID, err = s.registerClient(ctx)
			if err != nil {
				return "", err
			}
		}
		_, err = s.pool.Exec(ctx, `UPDATE ankr_management_connection SET client_id=$1,redirect_uri=$2,updated_at=now() WHERE id=1`, clientID, s.cfg.RedirectURI)
		if err != nil {
			return "", err
		}
	}
	state, err := randomURLSafe(32)
	if err != nil {
		return "", err
	}
	verifier, err := randomURLSafe(64)
	if err != nil {
		return "", err
	}
	ciphertext, nonce, err := seal(s.aead, []byte(verifier))
	if err != nil {
		return "", err
	}
	_, _ = s.pool.Exec(ctx, `DELETE FROM ankr_management_oauth_state WHERE expires_at<=now()`)
	_, err = s.pool.Exec(ctx, `INSERT INTO ankr_management_oauth_state(state_hash,client_id,redirect_uri,verifier_ciphertext,verifier_nonce,expires_at)
		VALUES($1,$2,$3,$4,$5,now()+interval '10 minutes')`, sha256URLSafe(state), clientID, s.cfg.RedirectURI, ciphertext, nonce)
	if err != nil {
		return "", err
	}
	q := url.Values{
		"response_type": {"code"}, "client_id": {clientID}, "redirect_uri": {s.cfg.RedirectURI},
		"scope": {"mcp:tools"}, "state": {state}, "code_challenge": {sha256URLSafe(verifier)},
		"code_challenge_method": {"S256"}, "resource": {s.cfg.MCPURL},
	}
	return strings.TrimRight(s.cfg.IssuerURL, "/") + "/authorize?" + q.Encode(), nil
}

// RestartOAuth removes the complete saved MCP connection before beginning an
// OAuth flow.  It is used for an upstream "saved connection not recognised"
// response, where retrying an old dynamically registered client cannot work.
func (s *Service) RestartOAuth(ctx context.Context) (string, error) {
	if err := s.Disconnect(ctx); err != nil {
		return "", fmt.Errorf("clear saved Ankr MCP connection: %w", err)
	}
	return s.StartOAuth(ctx)
}

func (s *Service) CompleteOAuth(ctx context.Context, state, code string) error {
	if strings.TrimSpace(state) == "" || strings.TrimSpace(code) == "" {
		return fmt.Errorf("OAuth callback is missing state or code")
	}
	var clientID, redirectURI string
	var ciphertext, nonce []byte
	err := s.pool.QueryRow(ctx, `DELETE FROM ankr_management_oauth_state WHERE state_hash=$1 AND expires_at>now()
		RETURNING client_id,redirect_uri,verifier_ciphertext,verifier_nonce`, sha256URLSafe(state)).Scan(&clientID, &redirectURI, &ciphertext, &nonce)
	if err != nil {
		return fmt.Errorf("OAuth state is invalid, expired, or already used")
	}
	verifier, err := open(s.aead, ciphertext, nonce)
	if err != nil {
		return err
	}
	form := url.Values{
		"grant_type": {"authorization_code"}, "code": {code}, "client_id": {clientID},
		"redirect_uri": {redirectURI}, "code_verifier": {string(verifier)}, "resource": {s.cfg.MCPURL},
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, strings.TrimRight(s.cfg.IssuerURL, "/")+"/token", strings.NewReader(form.Encode()))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	resp, err := s.http.Do(req)
	if err != nil {
		return fmt.Errorf("exchange Ankr OAuth code: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return sanitizedHTTPError("exchange Ankr OAuth code", resp)
	}
	var token oauthToken
	if err := json.NewDecoder(resp.Body).Decode(&token); err != nil {
		return fmt.Errorf("decode Ankr OAuth token: %w", err)
	}
	if token.AccessToken == "" {
		return fmt.Errorf("Ankr OAuth token response contained no access_token")
	}
	if token.ExpiresIn > 0 {
		token.ExpiresAt = time.Now().Add(time.Duration(token.ExpiresIn) * time.Second)
	}
	encoded, _ := json.Marshal(token)
	tokenCiphertext, tokenNonce, err := seal(s.aead, encoded)
	if err != nil {
		return err
	}
	var expiresAt any
	if !token.ExpiresAt.IsZero() {
		expiresAt = token.ExpiresAt
	}
	_, err = s.pool.Exec(ctx, `UPDATE ankr_management_connection SET token_ciphertext=$1,token_nonce=$2,token_expires_at=$3,
		connected_at=now(),last_error='',updated_at=now() WHERE id=1`, tokenCiphertext, tokenNonce, expiresAt)
	if err != nil {
		return err
	}
	return nil
}
