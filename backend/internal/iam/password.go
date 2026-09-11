// Package iam 实现身份、凭据、会话与登录安全（docs/dual-system-development-plan.md §6.3 / §6.6）。
package iam

import (
	"crypto/rand"
	"crypto/subtle"
	"encoding/base64"
	"errors"
	"fmt"
	"strings"

	"golang.org/x/crypto/argon2"
)

// ErrInvalidCredentials 对外统一返回，避免账号枚举（§6.6.2）。
var ErrInvalidCredentials = errors.New("invalid credentials")

// Params 是 Argon2id 参数。默认取 OWASP 推荐档（19MiB/2 迭代在 386 上过慢，
// 这里取 64MiB / t=3 / p=4，与 §6.3 "密码仅使用 Argon2id 哈希" 一致）。
type Params struct {
	Memory      uint32
	Iterations  uint32
	Parallelism uint8
	KeyLength   uint32
	SaltLength  uint32
}

func DefaultParams() Params {
	return Params{Memory: 64 * 1024, Iterations: 3, Parallelism: 4, KeyLength: 32, SaltLength: 16}
}

// HashPassword 返回 PHC 字符串（$argon2id$v=19$m=...,t=...,p=...$salt$hash）。
func HashPassword(password string, p Params) (string, error) {
	if password == "" {
		return "", errors.New("password must not be empty")
	}
	salt := make([]byte, p.SaltLength)
	if _, err := rand.Read(salt); err != nil {
		return "", fmt.Errorf("salt: %w", err)
	}
	key := argon2.IDKey([]byte(password), salt, p.Iterations, p.Memory, p.Parallelism, p.KeyLength)
	return fmt.Sprintf("$argon2id$v=%d$m=%d,t=%d,p=%d$%s$%s",
		argon2.Version, p.Memory, p.Iterations, p.Parallelism,
		base64.RawStdEncoding.EncodeToString(salt),
		base64.RawStdEncoding.EncodeToString(key)), nil
}

// VerifyPassword 使用恒定时间比较校验密码，并返回解析出的参数（便于逐步升级参数）。
func VerifyPassword(encoded, password string) (bool, Params, error) {
	p, salt, want, err := decodeHash(encoded)
	if err != nil {
		return false, Params{}, err
	}
	got := argon2.IDKey([]byte(password), salt, p.Iterations, p.Memory, p.Parallelism, p.KeyLength)
	if subtle.ConstantTimeCompare(got, want) != 1 {
		return false, p, nil
	}
	return true, p, nil
}

// NeedsRehash 判断存量哈希是否需要按新参数重算。
func NeedsRehash(encoded string, want Params) bool {
	p, _, _, err := decodeHash(encoded)
	if err != nil {
		return true
	}
	return p.Memory != want.Memory || p.Iterations != want.Iterations || p.Parallelism != want.Parallelism
}

func decodeHash(encoded string) (Params, []byte, []byte, error) {
	parts := strings.Split(encoded, "$")
	if len(parts) != 6 || parts[1] != "argon2id" {
		return Params{}, nil, nil, errors.New("unsupported password hash format")
	}
	var version int
	if _, err := fmt.Sscanf(parts[2], "v=%d", &version); err != nil {
		return Params{}, nil, nil, fmt.Errorf("parse version: %w", err)
	}
	if version != argon2.Version {
		return Params{}, nil, nil, fmt.Errorf("unsupported argon2 version %d", version)
	}
	var p Params
	if _, err := fmt.Sscanf(parts[3], "m=%d,t=%d,p=%d", &p.Memory, &p.Iterations, &p.Parallelism); err != nil {
		return Params{}, nil, nil, fmt.Errorf("parse params: %w", err)
	}
	salt, err := base64.RawStdEncoding.DecodeString(parts[4])
	if err != nil {
		return Params{}, nil, nil, fmt.Errorf("decode salt: %w", err)
	}
	key, err := base64.RawStdEncoding.DecodeString(parts[5])
	if err != nil {
		return Params{}, nil, nil, fmt.Errorf("decode key: %w", err)
	}
	p.SaltLength = uint32(len(salt))
	p.KeyLength = uint32(len(key))
	return p, salt, key, nil
}
