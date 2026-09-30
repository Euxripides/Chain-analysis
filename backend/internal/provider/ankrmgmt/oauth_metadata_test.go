package ankrmgmt

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/blockchain-platform/blockchain-platform/internal/config"
)

func TestMetadataClientIDRequiresAdvertisedSupport(t *testing.T) {
	for _, tc := range []struct {
		name       string
		supported  bool
		wrongIssue bool
		wantErr    error
	}{
		{name: "supported", supported: true},
		{name: "not_advertised", wantErr: ErrClientMetadataUnsupported},
		{name: "wrong_issuer", supported: true, wrongIssue: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var server *httptest.Server
			server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.Method != http.MethodGet || r.URL.Path != "/.well-known/oauth-authorization-server" {
					t.Errorf("unexpected request: %s %s", r.Method, r.URL.Path)
				}
				issuer := server.URL
				if tc.wrongIssue {
					issuer = "https://wrong.example"
				}
				fmt.Fprintf(w, `{"issuer":%q,"client_id_metadata_document_supported":%t}`, issuer, tc.supported)
			}))
			defer server.Close()
			svc := &Service{cfg: config.AnkrManagementConfig{
				IssuerURL: server.URL, ClientMetadataURL: "https://example.com/admin/ankr-client-metadata.json",
			}, http: server.Client()}
			got, err := svc.metadataClientID(context.Background())
			if tc.wantErr != nil {
				if !errors.Is(err, tc.wantErr) {
					t.Fatalf("error = %v, want %v", err, tc.wantErr)
				}
				return
			}
			if tc.wrongIssue {
				if err == nil {
					t.Fatal("expected issuer mismatch")
				}
				return
			}
			if err != nil || got != svc.cfg.ClientMetadataURL {
				t.Fatalf("client ID = %q, error = %v", got, err)
			}
		})
	}
}

func TestRegisterClientDeclaresNativeForLoopback(t *testing.T) {
	const callback = "http://127.0.0.1:41974/api/v1/api-control/provider-accounts/ankr/oauth/callback"
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost || r.URL.Path != "/register" {
			t.Errorf("unexpected request: %s %s", r.Method, r.URL.Path)
		}
		var metadata struct {
			ApplicationType string   `json:"application_type"`
			RedirectURIs    []string `json:"redirect_uris"`
		}
		if err := json.NewDecoder(r.Body).Decode(&metadata); err != nil {
			t.Fatal(err)
		}
		if metadata.ApplicationType != "native" || len(metadata.RedirectURIs) != 1 || metadata.RedirectURIs[0] != callback {
			t.Fatalf("unexpected native registration metadata: %+v", metadata)
		}
		w.WriteHeader(http.StatusCreated)
		fmt.Fprint(w, `{"client_id":"registered-native-client"}`)
	}))
	defer server.Close()
	svc := &Service{cfg: config.AnkrManagementConfig{IssuerURL: server.URL, RedirectURI: callback}, http: server.Client()}
	clientID, err := svc.registerClient(context.Background())
	if err != nil || clientID != "registered-native-client" {
		t.Fatalf("client ID = %q, error = %v", clientID, err)
	}
}
