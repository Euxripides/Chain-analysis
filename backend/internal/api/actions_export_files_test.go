package api

import (
	"testing"

	"github.com/blockchain-platform/blockchain-platform/internal/model"
)

func TestActionsExportUsesPublicBaseURL(t *testing.T) {
	for _, tc := range []struct{ name, actions, mcp, want string }{
		{"default", "", "", "https://aurore.online"},
		{"MCP public URL", "", "https://files.example.com/", "https://files.example.com"},
		{"Actions overrides MCP", " https://actions.example.com/base/ ", "https://mcp.example.com", "https://actions.example.com/base"},
		{"loopback is rejected", "https://127.0.0.1:8080", "https://files.example.com", "https://files.example.com"},
		{"IPv6 loopback is rejected", "https://[::1]:8080", "", "https://aurore.online"},
		{"localhost is rejected", "https://localhost:8080", "", "https://aurore.online"},
		{"non HTTPS is rejected", "http://files.example.com", "", "https://aurore.online"},
		{"credentials are rejected", "https://secret@files.example.com", "", "https://aurore.online"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Setenv("GPT_ACTIONS_PUBLIC_BASE_URL", tc.actions)
			t.Setenv("MCP_PUBLIC_URL", tc.mcp)
			if got := actionsPublicBaseURL(); got != tc.want {
				t.Fatalf("public base = %q, want %q", got, tc.want)
			}
		})
	}
}

func TestActionExportFileDescribesActualContainer(t *testing.T) {
	id := "00000000-0000-4000-8000-000000000000"
	for _, tc := range []struct {
		format                       string
		zip                          bool
		ref, name, mime, compression string
	}{
		{"parquet", false, "export.parquet", "export-00000000.parquet", "application/vnd.apache.parquet", "zstd"},
		{"csv", true, "export.zip", "export-00000000.zip", "application/zip", "zip"},
		{"parquet", true, "export.zip", "export-00000000.zip", "application/zip", "zip"},
	} {
		exp := &model.Export{ID: id, Format: tc.format, Zip: tc.zip, ObjectRef: tc.ref, Rows: 75554, SHA256: "digest"}
		file := actionExportFile(exp, "https://aurore.online/api/v1/download/ticket", 2840664)
		if file["name"] != tc.name || file["mime_type"] != tc.mime || file["compression"] != tc.compression || file["rows"] != int64(75554) || file["size_bytes"] != int64(2840664) || file["sha256"] != "digest" {
			t.Fatalf("unexpected file descriptor: %#v", file)
		}
	}
}
