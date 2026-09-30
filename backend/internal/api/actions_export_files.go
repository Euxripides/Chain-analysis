package api

import (
	"fmt"
	"net"
	"net/url"
	"os"
	"strings"

	"github.com/blockchain-platform/blockchain-platform/internal/model"
)

// Public export links must not depend on the internal MCP-to-REST request Host.
func actionsPublicBaseURL() string {
	for _, key := range []string{"GPT_ACTIONS_PUBLIC_BASE_URL", "MCP_PUBLIC_URL"} {
		base := strings.TrimRight(strings.TrimSpace(os.Getenv(key)), "/")
		u, err := url.Parse(base)
		if err != nil || u.Scheme != "https" || u.Host == "" || u.User != nil || u.RawQuery != "" || u.Fragment != "" {
			continue
		}
		host := strings.ToLower(u.Hostname())
		if host == "localhost" || strings.HasSuffix(host, ".localhost") {
			continue
		}
		if ip := net.ParseIP(host); ip != nil && (ip.IsLoopback() || ip.IsUnspecified()) {
			continue
		}
		return base
	}
	return "https://aurore.online"
}

func actionExportFile(exp *model.Export, downloadURL string, size int64) map[string]any {
	name := fmt.Sprintf("export-%s.%s", exp.ID[:8], exp.Format)
	mimeType, compression := "application/vnd.apache.parquet", "zstd"
	if exp.Zip || strings.HasSuffix(exp.ObjectRef, ".zip") {
		mimeType, compression = "application/zip", "zip"
		name = fmt.Sprintf("export-%s.zip", exp.ID[:8])
	} else if exp.Format == "csv" {
		mimeType, compression = "text/csv", ""
	}
	return map[string]any{"name": name, "url": downloadURL, "rows": exp.Rows, "size_bytes": size, "sha256": exp.SHA256, "mime_type": mimeType, "compression": compression}
}
