// Package ankrrpc：Ankr Chain RPC Provider 适配器（V1.5 §10/§189/§190）。
//
// 硬约束：
//   - 业务层永远不接触本包，只经 RPCGateway（§8/§247-1）；
//   - endpoint 用模板 + 凭证引用在运行时拼接，明文 Secret 不进日志/错误/响应（§190/§31）；
//   - 响应体必须 hard limit，不允许无限 io.ReadAll（§120/§247-11）。
package ankrrpc

import (
	"strings"
	"time"

	rpc "github.com/blockchain-platform/blockchain-platform/internal/provider/rpc"
)

// DefaultEndpointTemplate Ankr Chain RPC 端点模板（§5）。
const DefaultEndpointTemplate = "https://rpc.ankr.com/{chain}/{key}"

// Options 适配器配置。
type Options struct {
	// EndpointTemplate 支持 {chain} 与 {key} 占位符
	EndpointTemplate string
	APIKey           string

	// ChainPaths 平台链 slug → Ankr 链路径（默认同名）
	ChainPaths map[string]string

	RequestTimeout        time.Duration
	ResponseHeaderTimeout time.Duration
	ResponseHardLimit     int64

	MaxIdleConns        int
	MaxIdleConnsPerHost int
	MaxConnsPerHost     int

	// StaticCapabilities 静态已知能力（§20 Static Known Config）
	StaticCapabilities map[string]rpc.CapabilitySet
}

func (o Options) withDefaults() Options {
	if strings.TrimSpace(o.EndpointTemplate) == "" {
		o.EndpointTemplate = DefaultEndpointTemplate
	}
	if o.RequestTimeout <= 0 {
		o.RequestTimeout = 30 * time.Second
	}
	if o.ResponseHeaderTimeout <= 0 {
		o.ResponseHeaderTimeout = o.RequestTimeout
	}
	if o.ResponseHardLimit <= 0 {
		o.ResponseHardLimit = 64 << 20
	}
	if o.MaxIdleConns <= 0 {
		o.MaxIdleConns = 256
	}
	if o.MaxIdleConnsPerHost <= 0 {
		o.MaxIdleConnsPerHost = 128
	}
	if o.MaxConnsPerHost <= 0 {
		o.MaxConnsPerHost = 128
	}
	return o
}

// defaultChainPaths 平台 slug 与 Ankr 路径的差异集中在这里，避免业务层散落 switch（§247-27）。
var defaultChainPaths = map[string]string{
	"bsc":       "bsc",
	"eth":       "eth",
	"ethereum":  "eth",
	"polygon":   "polygon",
	"arbitrum":  "arbitrum",
	"base":      "base",
	"optimism":  "optimism",
	"avalanche": "avalanche",
}

func (c *Client) chainPath(slug string) (string, bool) {
	key := strings.ToLower(strings.TrimSpace(slug))
	if path, ok := c.opts.ChainPaths[key]; ok && path != "" {
		return path, true
	}
	path, ok := defaultChainPaths[key]
	return path, ok
}

// endpoint 运行时拼接（Secret 永不落入日志）。
func (c *Client) endpoint(slug string) (string, bool) {
	path, ok := c.chainPath(slug)
	if !ok {
		return "", false
	}
	url := strings.ReplaceAll(c.opts.EndpointTemplate, "{chain}", path)
	url = strings.ReplaceAll(url, "{key}", c.opts.APIKey)
	return strings.TrimSuffix(url, "/"), true
}

// SafeEndpoint 脱敏后的端点（日志/管理接口用，§31）。
func (c *Client) SafeEndpoint(slug string) string {
	url, ok := c.endpoint(slug)
	if !ok {
		return ""
	}
	return rpc.RedactURL(url)
}
