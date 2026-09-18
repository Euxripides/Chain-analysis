// Package mfa：TOTP（RFC 6238）与恢复码的**纯逻辑**实现。
//
// 背景：计划 §19.9 / AUTH-004 要求 MFA/TOTP，平台此前只有占位
// （POST /api/auth/v1/mfa/verify 直接返回 MFA_NOT_IMPLEMENTED），step-up 用的是密码重认证。
// 本包只做可独立验证的密码学与编码部分（不碰会话、不碰数据库、不碰 HTTP），
// 这样它可以先用 RFC 官方测试向量钉死，再往上接注册/校验/登录第二步与页面。
package mfa

import (
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha1"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/base32"
	"encoding/binary"
	"fmt"
	"strings"
)

const (
	// Digits 是平台采用的验证码位数（与主流 Authenticator 默认一致）。
	Digits = 6
	// PeriodSeconds 是 TOTP 步长（认证器默认 30 秒）。
	PeriodSeconds = 30
	// secretBytes 是共享密钥长度：20 字节 = 160 bit，base32 后 32 个字符。
	secretBytes = 20
	// DefaultSkewSteps 允许前后各一个步长的时钟漂移（±30 秒）。
	DefaultSkewSteps = 1
)

// NewSecret 生成一个新的 base32（无 padding、大写）共享密钥，供二维码/手工录入使用。
func NewSecret() (string, error) {
	buf := make([]byte, secretBytes)
	if _, err := rand.Read(buf); err != nil {
		return "", fmt.Errorf("mfa: generate secret: %w", err)
	}
	return base32.StdEncoding.WithPadding(base32.NoPadding).EncodeToString(buf), nil
}

// NormalizeSecret 接受用户手工输入（大小写、空格、填充都可能不规范）并还原为标准 base32。
func NormalizeSecret(secret string) (string, error) {
	cleaned := strings.ToUpper(strings.ReplaceAll(strings.TrimSpace(secret), " ", ""))
	cleaned = strings.TrimRight(cleaned, "=")
	if cleaned == "" {
		return "", fmt.Errorf("mfa: empty secret")
	}
	raw, err := base32.StdEncoding.WithPadding(base32.NoPadding).DecodeString(cleaned)
	if err != nil {
		return "", fmt.Errorf("mfa: invalid base32 secret: %w", err)
	}
	if len(raw) < 10 {
		return "", fmt.Errorf("mfa: secret too short (%d bytes)", len(raw))
	}
	return base32.StdEncoding.WithPadding(base32.NoPadding).EncodeToString(raw), nil
}

// CodeAt 计算 counter 对应的 TOTP 码（RFC 6238 §5.2 / RFC 4226 动态截断）。
func CodeAt(secret string, counter uint64, digits int) (string, error) {
	key, err := base32.StdEncoding.WithPadding(base32.NoPadding).DecodeString(strings.ToUpper(strings.TrimRight(secret, "=")))
	if err != nil {
		return "", fmt.Errorf("mfa: decode secret: %w", err)
	}
	if digits <= 0 || digits > 9 {
		return "", fmt.Errorf("mfa: unsupported digits %d", digits)
	}
	var msg [8]byte
	binary.BigEndian.PutUint64(msg[:], counter)
	mac := hmac.New(sha1.New, key)
	mac.Write(msg[:])
	sum := mac.Sum(nil)
	offset := sum[len(sum)-1] & 0x0f
	truncated := binary.BigEndian.Uint32(sum[offset:offset+4]) & 0x7fffffff
	mod := uint32(1)
	for i := 0; i < digits; i++ {
		mod *= 10
	}
	return fmt.Sprintf("%0*d", digits, truncated%mod), nil
}

// Verify 校验用户输入的验证码：允许 skewSteps 个步长的时钟漂移，比较是常量时间的。
func Verify(secret, code string, unixSeconds int64, skewSteps int) bool {
	code = strings.TrimSpace(code)
	if len(code) == 0 {
		return false
	}
	if skewSteps < 0 {
		skewSteps = DefaultSkewSteps
	}
	counter := unixSeconds / PeriodSeconds
	for delta := -int64(skewSteps); delta <= int64(skewSteps); delta++ {
		if counter+delta < 0 {
			continue
		}
		expected, err := CodeAt(secret, uint64(counter+delta), len(code))
		if err != nil {
			return false
		}
		if subtle.ConstantTimeCompare([]byte(expected), []byte(code)) == 1 {
			return true
		}
	}
	return false
}

// ProvisioningURI 生成 otpauth:// 链接，供客户端渲染二维码（不额外依赖二维码库）。
func ProvisioningURI(issuer, account, secret string) string {
	label := issuer + ":" + account
	return fmt.Sprintf("otpauth://totp/%s?secret=%s&issuer=%s&algorithm=SHA1&digits=%d&period=%d",
		urlEscape(label), secret, urlEscape(issuer), Digits, PeriodSeconds)
}

func urlEscape(v string) string {
	replacer := strings.NewReplacer(" ", "%20", ":", "%3A", "/", "%2F", "?", "%3F", "&", "%26", "#", "%23", "@", "%40")
	return replacer.Replace(v)
}

// RecoveryCodes 生成 n 个一次性恢复码，并返回明文与各自的 SHA-256（只存摘要）。
// 明文只在这一个响应里出现一次；服务端只保存摘要 + 使用状态。
func RecoveryCodes(n int) (plain []string, digests [][32]byte, err error) {
	if n <= 0 {
		n = 10
	}
	alphabet := "ABCDEFGHJKLMNPQRSTUVWXYZ23456789" // 去掉易混淆的 I/O/0/1
	for i := 0; i < n; i++ {
		raw := make([]byte, 10)
		if _, err := rand.Read(raw); err != nil {
			return nil, nil, fmt.Errorf("mfa: generate recovery code: %w", err)
		}
		var b strings.Builder
		for j, v := range raw {
			if j == 5 {
				b.WriteByte('-')
			}
			b.WriteByte(alphabet[int(v)%len(alphabet)])
		}
		code := b.String()
		plain = append(plain, code)
		digests = append(digests, HashRecoveryCode(code))
	}
	return plain, digests, nil
}

// HashRecoveryCode 归一化（去空格/连字符、大写）后取 SHA-256，比较时用常量时间。
func HashRecoveryCode(code string) [32]byte {
	cleaned := strings.ToUpper(strings.ReplaceAll(strings.ReplaceAll(strings.TrimSpace(code), "-", ""), " ", ""))
	return sha256.Sum256([]byte(cleaned))
}

// RecoveryCodeMatches 常量时间比较恢复码摘要。
func RecoveryCodeMatches(code string, digest [32]byte) bool {
	got := HashRecoveryCode(code)
	return subtle.ConstantTimeCompare(got[:], digest[:]) == 1
}
