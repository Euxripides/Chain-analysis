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
	// logoSweepBatch：每轮最多本地化多少个图标。
	// 200 是"整库回填后一轮清空"与"不与请求路径抢带宽"的折中：单张图 ~20-50KB，
	// 200 张约 10MB / 轮；worker 是独立进程，不占用 API 的交互预算。
	logoSweepBatch = 200
	// logoWorkers：并发本地化协程数。
	// 单协程时一次卡住的 MinIO 上传会让整条管线停摆：2026-09-20 实测本地化在 1,163 处
	// 停滞一个多小时、且没有任何错误日志（协程卡在不可中断的调用里，错误永远不返回）。
	logoWorkers = 4
	// logoFetchTimeout：单张图从校验到落库的墙钟上限。
	// 任何一步（DNS / 下载 / 上传）超时都按失败计，下一轮扫描会重新捡起，不会占住协程。
	logoFetchTimeout = 60 * time.Second
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
	for i := 0; i < logoWorkers; i++ {
		go func() {
			for {
				select {
				case <-ctx.Done():
					return
				case meta := <-s.logoQueue:
					itemCtx, cancel := context.WithTimeout(ctx, logoFetchTimeout)
					err := s.localizeLogo(itemCtx, meta)
					cancel()
					if err != nil {
						s.Log.Warn("token logo localization failed", "contract", meta.ContractAddress, "err", err)
						LogoFetchTotal.WithLabelValues("error").Inc()
					} else {
						LogoFetchTotal.WithLabelValues("ok").Inc()
					}
				}
			}
		}()
	}
}

// sweepPendingLogos 把"已有 logo_url、还没有自托管对象"的代币送进队列。
func (s *Service) sweepPendingLogos(ctx context.Context) {
	if !s.Logo.Enabled || s.Raw == nil {
		return
	}
	// 直接取行里的 logo_url 构造队列元素，不再回查 Get：
	// Get 用 normalized_address 做键，TRON 这类大小写敏感的地址会查不到而静默跳过
	//（2026-09-20 实测：33 个 tron 行因此永远排不进队列）。
	rows, err := s.Store.Pool.Query(ctx, `SELECT chain, contract_address, COALESCE(logo_url, '') FROM token_metadata
        WHERE COALESCE(logo_url, '') <> '' AND COALESCE(logo_object_key, '') = ''
        ORDER BY updated_at DESC LIMIT $1`, logoSweepBatch)
	if err != nil {
		s.Log.Warn("token logo sweep query failed", "err", err)
		return
	}
	defer rows.Close()
	queued := 0
	for rows.Next() {
		var chain, contract, logoURL string
		if err := rows.Scan(&chain, &contract, &logoURL); err != nil {
			continue
		}
		select {
		case s.logoQueue <- Metadata{Chain: chain, ContractAddress: contract, LogoURL: logoURL}:
			queued++
		default:
			s.Log.Warn("token logo queue full")
		}
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
	// normalized_address 必须按链归一化：TRON 的 base58 区分大小写，用全局 normalizeAddress
	// 会把地址小写化，UPDATE 匹配不到任何行（图标写进了 MinIO，行却永远停在"待本地化"）。
	_, err = s.Store.Pool.Exec(ctx, `UPDATE token_metadata SET logo_object_key=$3,logo_mime_type=$4,updated_at=now() WHERE chain=$1 AND normalized_address=$2`, meta.Chain, normalizeAddressForChain(meta.Chain, meta.ContractAddress), key, mime)
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
