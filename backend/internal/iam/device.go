package iam

import (
	"net"
	"net/http"
	"regexp"
	"strings"
)

// DeviceInfo 记录"客户端声明"的设备信息。UA / Client Hints 可被伪造，
// 后台展示时必须标注为客户端声明，不能作为人员身份结论（§6.6.4）。
type DeviceInfo struct {
	DeviceType     string `json:"device_type"`
	DeviceVendor   string `json:"device_vendor"`
	DeviceModel    string `json:"device_model"`
	OSName         string `json:"os_name"`
	OSVersion      string `json:"os_version"`
	BrowserName    string `json:"browser_name"`
	BrowserVersion string `json:"browser_version"`
	Label          string `json:"label"`
	Bot            bool   `json:"bot"`
}

var (
	reWindows = regexp.MustCompile(`Windows NT ([0-9.]+)`)
	reAndroid = regexp.MustCompile(`Android[ /]([0-9.]+)`)
	reIOS     = regexp.MustCompile(`(?:iPhone|iPad|iPod).*?OS ([0-9_]+)`)
	reMacOS   = regexp.MustCompile(`Mac OS X ([0-9_.]+)`)
	reChrome  = regexp.MustCompile(`(?:Chrome|Chromium)/([0-9.]+)`)
	reFirefox = regexp.MustCompile(`Firefox/([0-9.]+)`)
	reEdge    = regexp.MustCompile(`Edg(?:A|iOS)?/([0-9.]+)`)
	reSafari  = regexp.MustCompile(`Version/([0-9.]+).*Safari`)
	reBot     = regexp.MustCompile(`(?i)(bot|crawler|spider|curl|wget|python-requests|go-http-client|okhttp|headless)`)
)

// ParseUserAgent 做保守解析：无法可靠识别时返回空字符串，不猜测。
func ParseUserAgent(ua string) DeviceInfo {
	info := DeviceInfo{}
	ua = strings.TrimSpace(ua)
	if ua == "" {
		return info
	}
	info.Bot = reBot.MatchString(ua)

	switch {
	case strings.Contains(ua, "Android"):
		info.DeviceType = "mobile"
		if strings.Contains(ua, "Tablet") || strings.Contains(ua, "SM-T") {
			info.DeviceType = "tablet"
		}
		info.DeviceVendor = "Android"
		if m := reAndroid.FindStringSubmatch(ua); m != nil {
			info.OSName, info.OSVersion = "Android", m[1]
		}
	case strings.Contains(ua, "iPhone"), strings.Contains(ua, "iPad"), strings.Contains(ua, "iPod"):
		info.DeviceType = "mobile"
		info.DeviceVendor = "Apple"
		if strings.Contains(ua, "iPad") {
			info.DeviceType = "tablet"
		}
		if m := reIOS.FindStringSubmatch(ua); m != nil {
			info.OSName, info.OSVersion = "iOS", strings.ReplaceAll(m[1], "_", ".")
		}
	case strings.Contains(ua, "Windows"):
		info.DeviceType, info.DeviceVendor, info.OSName = "desktop", "PC", "Windows"
		if m := reWindows.FindStringSubmatch(ua); m != nil {
			info.OSVersion = windowsVersion(m[1])
		}
	case strings.Contains(ua, "Macintosh"), strings.Contains(ua, "Mac OS X"):
		info.DeviceType, info.DeviceVendor, info.OSName = "desktop", "Apple", "macOS"
		if m := reMacOS.FindStringSubmatch(ua); m != nil {
			info.OSVersion = strings.ReplaceAll(m[1], "_", ".")
		}
	case strings.Contains(ua, "CrOS"):
		info.DeviceType, info.DeviceVendor, info.OSName = "desktop", "Google", "ChromeOS"
	case strings.Contains(ua, "Linux"):
		info.DeviceType, info.DeviceVendor, info.OSName = "desktop", "PC", "Linux"
	}

	switch {
	case reEdge.MatchString(ua):
		info.BrowserName = "Edge"
		info.BrowserVersion = reEdge.FindStringSubmatch(ua)[1]
	case reChrome.MatchString(ua):
		info.BrowserName = "Chrome"
		info.BrowserVersion = reChrome.FindStringSubmatch(ua)[1]
	case reFirefox.MatchString(ua):
		info.BrowserName = "Firefox"
		info.BrowserVersion = reFirefox.FindStringSubmatch(ua)[1]
	case reSafari.MatchString(ua):
		info.BrowserName = "Safari"
		info.BrowserVersion = reSafari.FindStringSubmatch(ua)[1]
	case strings.Contains(ua, "Gecko"):
		info.BrowserName = "Gecko"
	}

	info.Label = strings.TrimSpace(strings.Join([]string{info.BrowserName, info.OSName}, " ")) + func() string {
		if info.DeviceType == "" {
			return ""
		}
		return " · " + info.DeviceType
	}()
	return info
}

// ClientHints 提取浏览器实际提供的 Sec-CH-UA 系列字段（§6.6.2）。
// 采集范围严格限定在 UA 客户端提示，不引入跨站指纹。
func ClientHints(h http.Header) map[string]string {
	out := map[string]string{}
	for _, k := range []string{"Sec-CH-UA", "Sec-CH-UA-Platform", "Sec-CH-UA-Platform-Version",
		"Sec-CH-UA-Mobile", "Sec-CH-UA-Model", "Sec-CH-UA-Arch", "Sec-CH-UA-Bitness", "Sec-CH-UA-Full-Version-List"} {
		if v := strings.TrimSpace(h.Get(k)); v != "" {
			out[strings.ToLower(k)] = v
		}
	}
	return out
}

func windowsVersion(nt string) string {
	switch nt {
	case "10.0":
		return "10/11"
	case "6.3":
		return "8.1"
	case "6.2":
		return "8"
	case "6.1":
		return "7"
	default:
		return nt
	}
}

// ClientIP 解析结果区分直连地址与可信代理链解析出的客户端地址（§6.6.4）。
type ClientIP struct {
	PeerIP     string
	ClientIP   string
	ProxyChain []string
}

// ResolveClientIP 仅在请求来自配置的可信代理时才读取转发头；
// 不可信来源携带的 Forwarded / X-Forwarded-For / CF-Connecting-IP 一律丢弃。
func ResolveClientIP(r *http.Request, trustedProxies []*net.IPNet) ClientIP {
	res := ClientIP{}
	peerHost, _, err := net.SplitHostPort(r.RemoteAddr)
	if err != nil {
		peerHost = r.RemoteAddr
	}
	peer := net.ParseIP(peerHost)
	if peer != nil {
		res.PeerIP = peer.String()
	}
	res.ClientIP = res.PeerIP

	if !ipTrusted(peer, trustedProxies) {
		return res
	}
	cfIP := strings.TrimSpace(r.Header.Get("CF-Connecting-IP"))
	chain := splitForwarded(r.Header.Get("X-Forwarded-For"))
	// 只有全部中间跳都在可信列表内才继续向左解析，避免客户端伪造左侧条目。
	forwarded := false
	if cfIP != "" {
		if ip := net.ParseIP(cfIP); ip != nil {
			res.ClientIP = ip.String()
			forwarded = true
		}
	}
	if len(chain) > 0 {
		res.ProxyChain = chain
		if !forwarded {
			for i := len(chain) - 1; i >= 0; i-- {
				ip := net.ParseIP(strings.TrimSpace(chain[i]))
				if ip == nil {
					break
				}
				if !ipTrusted(ip, trustedProxies) {
					res.ClientIP = ip.String()
					forwarded = true
					break
				}
			}
		}
	}
	if !forwarded && peer != nil && !ipTrusted(peer, trustedProxies) {
		res.ClientIP = peer.String()
	}
	return res
}

func splitForwarded(v string) []string {
	if strings.TrimSpace(v) == "" {
		return nil
	}
	parts := strings.Split(v, ",")
	out := make([]string, 0, len(parts))
	for _, p := range parts {
		if s := strings.TrimSpace(p); s != "" {
			out = append(out, s)
		}
	}
	return out
}

func ipTrusted(ip net.IP, nets []*net.IPNet) bool {
	if len(nets) == 0 {
		return false
	}
	if ip == nil {
		return false
	}
	for _, n := range nets {
		if n != nil && n.Contains(ip) {
			return true
		}
	}
	return false
}

// ParseTrustedProxies 解析 CIDR 列表；非法条目直接报错，避免静默放宽信任。
func ParseTrustedProxies(values []string) ([]*net.IPNet, error) {
	var out []*net.IPNet
	for _, v := range values {
		v = strings.TrimSpace(v)
		if v == "" {
			continue
		}
		if !strings.Contains(v, "/") {
			ip := net.ParseIP(v)
			if ip == nil {
				return nil, &net.ParseError{Type: "IP address", Text: v}
			}
			bits := 32
			if ip.To4() == nil {
				bits = 128
			}
			v = v + "/" + itoa(bits)
		}
		_, n, err := net.ParseCIDR(v)
		if err != nil {
			return nil, err
		}
		out = append(out, n)
	}
	return out, nil
}

func itoa(n int) string {
	if n == 0 {
		return "0"
	}
	var b [4]byte
	i := len(b)
	for n > 0 {
		i--
		b[i] = byte('0' + n%10)
		n /= 10
	}
	return string(b[i:])
}

// MaskEmail 生成用于失败登录记录的掩码账号，避免账号枚举同时在库中留可读线索。
func MaskEmail(email string) string {
	email = strings.TrimSpace(strings.ToLower(email))
	at := strings.LastIndex(email, "@")
	if at <= 0 {
		return "***"
	}
	local, domain := email[:at], email[at:]
	if len(local) <= 2 {
		return "**" + domain
	}
	return local[:2] + strings.Repeat("*", len(local)-2) + domain
}
