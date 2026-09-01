package method

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

// PublicSignatureResolver 公共 4-byte 签名库（§31）。
//
// 约束：
//   - 只能用于候选补全，不能作为唯一可信来源；
//   - 结果一律 source=public_signature、verified=false；
//   - 只允许后台异步调用，Fast Resolver 永远不得触碰（§62-7/§62-8）。
type PublicSignatureResolver struct {
	Endpoint string
	Client   *http.Client
	enabled  bool
}

// DefaultPublicSignatureEndpoint 4byte.directory 兼容接口。
const DefaultPublicSignatureEndpoint = "https://www.4byte.directory/api/v1/signatures/"

func NewPublicSignatureResolver(enabled bool, endpoint string, timeout time.Duration) *PublicSignatureResolver {
	if endpoint == "" {
		endpoint = DefaultPublicSignatureEndpoint
	}
	if timeout <= 0 {
		timeout = 10 * time.Second
	}
	return &PublicSignatureResolver{
		Endpoint: endpoint,
		Client:   &http.Client{Timeout: timeout},
		enabled:  enabled,
	}
}

func (p *PublicSignatureResolver) Enabled() bool { return p != nil && p.enabled && p.Endpoint != "" }

type publicSignatureResponse struct {
	Count   int `json:"count"`
	Results []struct {
		TextSignature string `json:"text_signature"`
		HexSignature  string `json:"hex_signature"`
	} `json:"results"`
}

// Lookup 查询一个 selector 的全部公共候选签名。
func (p *PublicSignatureResolver) Lookup(ctx context.Context, selector string) ([]SignatureRecord, error) {
	if !p.Enabled() {
		return nil, fmt.Errorf("public signature lookup disabled")
	}
	normalized := NormalizeSelector(selector)
	if normalized == "" {
		return nil, fmt.Errorf("invalid selector %q", selector)
	}
	endpoint := p.Endpoint + "?" + url.Values{"hex_signature": []string{normalized}}.Encode()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Accept", "application/json")
	res, err := p.Client.Do(req)
	if err != nil {
		return nil, err
	}
	defer res.Body.Close()
	if res.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("public signature http %d", res.StatusCode)
	}
	body, err := io.ReadAll(io.LimitReader(res.Body, 1<<20))
	if err != nil {
		return nil, err
	}
	var parsed publicSignatureResponse
	if err := json.Unmarshal(body, &parsed); err != nil {
		return nil, fmt.Errorf("public signature json: %w", err)
	}
	out := make([]SignatureRecord, 0, len(parsed.Results))
	for _, item := range parsed.Results {
		signature := strings.TrimSpace(item.TextSignature)
		if signature == "" {
			continue
		}
		// 二次校验：公共库也可能有脏数据，selector 必须与签名自洽
		if SelectorOf(signature) != normalized {
			continue
		}
		out = append(out, SignatureRecord{
			Selector: normalized, Name: NameOf(signature), Signature: signature,
			Source: SourcePublicSignature, Verified: false,
		})
	}
	return out, nil
}
