package tokenmeta

import (
	"bytes"
	"context"
	"encoding/binary"
	"fmt"
	"image"
	_ "image/jpeg"
	_ "image/png"
	"io"
	"net"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/minio/minio-go/v7"

	"github.com/blockchain-platform/blockchain-platform/internal/rawstore"
)

type LogoConfig struct {
	Enabled             bool
	MaxBytes            int64
	MaxWidth, MaxHeight int
}

func ValidatePublicHTTPS(ctx context.Context, rawURL string, resolver *net.Resolver) error {
	u, err := url.Parse(rawURL)
	if err != nil {
		return err
	}
	if u.Scheme != "https" {
		return fmt.Errorf("logo requires https")
	}
	if u.Hostname() == "" {
		return fmt.Errorf("logo host required")
	}
	host := strings.ToLower(u.Hostname())
	if host == "localhost" || strings.HasSuffix(host, ".localhost") {
		return fmt.Errorf("local host rejected")
	}
	if resolver == nil {
		resolver = net.DefaultResolver
	}
	ips, err := resolver.LookupIPAddr(ctx, host)
	if err != nil {
		return err
	}
	if len(ips) == 0 {
		return fmt.Errorf("logo host has no address")
	}
	for _, item := range ips {
		if blockedIP(item.IP) {
			return fmt.Errorf("private or special address rejected: %s", item.IP)
		}
	}
	return nil
}
func blockedIP(ip net.IP) bool {
	return ip == nil || ip.IsLoopback() || ip.IsPrivate() || ip.IsUnspecified() || ip.IsLinkLocalUnicast() || ip.IsLinkLocalMulticast() || ip.IsMulticast() || ip.Equal(net.ParseIP("169.254.169.254"))
}

func (s *Service) ConfigureLogo(raw *rawstore.Store, cfg LogoConfig) {
	s.Raw = raw
	s.Logo = cfg
	if s.logoQueue == nil {
		s.logoQueue = make(chan Metadata, 128)
	}
}

const (
	// logoSweepInterval：存量扫描周期。目录回填（cmd/tokenindex -resolve-logos）是批量
	// UPDATE，不会产生逐条"元数据已解析"事件，因此必须靠扫描把它们捡起来。
	logoSweepInterval = 5 * time.Minute
	// logoSweepBatch：每轮最多本地化多少个图标，避免与请求路径抢带宽。
	logoSweepBatch = 50
)

func (s *Service) StartLogoWorker(ctx context.Context) {
	if !s.Logo.Enabled || s.Raw == nil {
		return
	}
	go func() {
		ticker := time.NewTicker(logoSweepInterval)
		defer ticker.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
				s.sweepPendingLogos(ctx)
			}
		}
	}()
	go func() {
		for {
			select {
			case <-ctx.Done():
				return
			case meta := <-s.logoQueue:
				if err := s.localizeLogo(ctx, meta); err != nil {
					s.Log.Warn("token logo localization failed", "contract", meta.ContractAddress, "err", err)
					LogoFetchTotal.WithLabelValues("error").Inc()
				} else {
					LogoFetchTotal.WithLabelValues("ok").Inc()
				}
			}
		}
	}()
}

// sweepPendingLogos 把"已有 logo_url、还没有自托管对象"的代币送进队列。
func (s *Service) sweepPendingLogos(ctx context.Context) {
	if !s.Logo.Enabled || s.Raw == nil {
		return
	}
	rows, err := s.Store.Pool.Query(ctx, `SELECT chain, contract_address FROM token_metadata
        WHERE COALESCE(logo_url, '') <> '' AND COALESCE(logo_object_key, '') = ''
        ORDER BY updated_at DESC LIMIT $1`, logoSweepBatch)
	if err != nil {
		s.Log.Warn("token logo sweep query failed", "err", err)
		return
	}
	defer rows.Close()
	queued := 0
	for rows.Next() {
		var chain, contract string
		if err := rows.Scan(&chain, &contract); err != nil {
			continue
		}
		s.queueLogo(ctx, chain, contract)
		queued++
	}
	if queued > 0 {
		s.Log.Info("token logo sweep queued", "count", queued)
	}
}

func (s *Service) queueLogo(ctx context.Context, chain, contract string) {
	if !s.Logo.Enabled || s.Raw == nil {
		return
	}
	m, err := s.Get(ctx, chain, contract)
	if err != nil || m == nil || m.LogoURL == "" || m.LogoObjectKey != "" {
		return
	}
	select {
	case s.logoQueue <- *m:
	default:
		s.Log.Warn("token logo queue full")
	}
}

func (s *Service) localizeLogo(ctx context.Context, meta Metadata) error {
	resolver := net.DefaultResolver
	if err := ValidatePublicHTTPS(ctx, meta.LogoURL, resolver); err != nil {
		return err
	}
	dialer := &net.Dialer{Timeout: 8 * time.Second}
	transport := &http.Transport{TLSHandshakeTimeout: 8 * time.Second, ResponseHeaderTimeout: 10 * time.Second, DialContext: func(ctx context.Context, network, address string) (net.Conn, error) {
		host, port, err := net.SplitHostPort(address)
		if err != nil {
			return nil, err
		}
		ips, err := resolver.LookupIPAddr(ctx, host)
		if err != nil {
			return nil, err
		}
		for _, item := range ips {
			if blockedIP(item.IP) {
				return nil, fmt.Errorf("resolved private address rejected")
			}
		}
		if len(ips) == 0 {
			return nil, fmt.Errorf("no public address")
		}
		return dialer.DialContext(ctx, network, net.JoinHostPort(ips[0].IP.String(), port))
	}}
	client := &http.Client{Transport: transport, Timeout: 15 * time.Second, CheckRedirect: func(req *http.Request, via []*http.Request) error {
		if len(via) > 5 {
			return fmt.Errorf("too many redirects")
		}
		return ValidatePublicHTTPS(req.Context(), req.URL.String(), resolver)
	}}
	res, err := client.Get(meta.LogoURL)
	if err != nil {
		return err
	}
	defer res.Body.Close()
	if res.StatusCode != http.StatusOK {
		return fmt.Errorf("logo http %d", res.StatusCode)
	}
	max := s.Logo.MaxBytes
	if max <= 0 {
		max = 2 << 20
	}
	body, err := io.ReadAll(io.LimitReader(res.Body, max+1))
	if err != nil {
		return err
	}
	if int64(len(body)) > max {
		return fmt.Errorf("logo exceeds %d bytes", max)
	}
	mime := http.DetectContentType(body)
	if mime != "image/png" && mime != "image/jpeg" && mime != "image/webp" {
		return fmt.Errorf("logo mime rejected: %s", mime)
	}
	width, height, err := imageDimensions(body, mime)
	if err != nil {
		return err
	}
	maxW, maxH := s.Logo.MaxWidth, s.Logo.MaxHeight
	if maxW <= 0 {
		maxW = 4096
	}
	if maxH <= 0 {
		maxH = 4096
	}
	if width <= 0 || height <= 0 || width > maxW || height > maxH {
		return fmt.Errorf("logo dimensions rejected: %dx%d", width, height)
	}
	ext := map[string]string{"image/png": "png", "image/jpeg": "jpg", "image/webp": "webp"}[mime]
	key := fmt.Sprintf("token-assets/%s/%s/logo.%s", meta.Chain, meta.ContractAddress, ext)
	if _, err = s.Raw.Client().PutObject(ctx, s.Raw.Bucket(), key, bytes.NewReader(body), int64(len(body)), minio.PutObjectOptions{ContentType: mime}); err != nil {
		return err
	}
	_, err = s.Store.Pool.Exec(ctx, `UPDATE token_metadata SET logo_object_key=$3,logo_mime_type=$4,updated_at=now() WHERE chain=$1 AND normalized_address=$2`, meta.Chain, normalizeAddress(meta.ContractAddress), key, mime)
	return err
}
func imageDimensions(body []byte, mime string) (int, int, error) {
	if mime != "image/webp" {
		cfg, _, err := image.DecodeConfig(bytes.NewReader(body))
		return cfg.Width, cfg.Height, err
	}
	if len(body) < 30 || string(body[:4]) != "RIFF" || string(body[8:12]) != "WEBP" {
		return 0, 0, fmt.Errorf("invalid webp")
	}
	kind := string(body[12:16])
	switch kind {
	case "VP8X":
		return 1 + int(body[24]) + (int(body[25]) << 8) + (int(body[26]) << 16), 1 + int(body[27]) + (int(body[28]) << 8) + (int(body[29]) << 16), nil
	case "VP8 ":
		return int(binary.LittleEndian.Uint16(body[26:28]) & 0x3fff), int(binary.LittleEndian.Uint16(body[28:30]) & 0x3fff), nil
	case "VP8L":
		if len(body) < 25 || body[20] != 0x2f {
			return 0, 0, fmt.Errorf("invalid lossless webp")
		}
		bits := binary.LittleEndian.Uint32(body[21:25])
		return int(bits&0x3fff) + 1, int((bits>>14)&0x3fff) + 1, nil
	}
	return 0, 0, fmt.Errorf("unsupported webp chunk %s", kind)
}
