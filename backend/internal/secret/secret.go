// Package secret 解析受控密钥引用，供 Provider 在真正发请求时取用凭据。
//
// 为什么要有受控引用：明文 Key 一旦进入配置、数据库或日志，就会随备份、导出和排障外泄
// （开发文档 §11.1/§16）。因此配置里只出现 env://NAME 或 store://<provider>/<kind>，
// 明文只存在于环境变量或 0600 的受管文件；对外（Describe）只能给出前缀与指纹。
package secret

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"time"
)

// Resolver 解析受控密钥引用。
//
// Resolve 只在需要凭据的那一刻返回值：调用方不得把它写进日志、审计或数据库。
type Resolver interface {
	Resolve(ctx context.Context, ref string) (string, error)
	Describe(ctx context.Context, ref string) RefStatus
}

// RefStatus 是引用的可对外展示状态：只有前缀与指纹，没有明文。
type RefStatus struct {
	Ref         string `json:"ref"`
	Kind        string `json:"kind"` // env | store | empty
	Name        string `json:"name"`
	Configured  bool   `json:"configured"`
	Prefix      string `json:"prefix"`      // 明文前 6 位
	Fingerprint string `json:"fingerprint"` // sha256 前 16 hex
}

// ErrNotConfigured 表示引用本身合法但没有可用值：引用为空串、env 未设置或为空、
// store 中没有该键。调用方用 errors.Is 判断后回退旧链路（例如 DRPC_ENABLED=false 时
// 继续用 provider/solana），不能把它当成需要重试的故障。
var ErrNotConfigured = errors.New("secret not configured")

// 引用类型。KindUnknown 只用于非法引用：Describe 如实标注，Resolve 直接报错，
// 绝不把拼错的引用当成"未配置"而静默回退。
const (
	KindEnv     = "env"
	KindStore   = "store"
	KindEmpty   = "empty"
	KindUnknown = "unknown"
)

const (
	schemeEnv   = "env://"
	schemeStore = "store://"

	// 前缀与指纹长度由规范 §1.1 固定：6 位足以人工比对，指纹 16 hex 足以区分 Key，
	// 两者都不足以还原明文。
	prefixRunes      = 6
	fingerprintChars = 16

	defaultDir  = ".blockchain-platform"
	defaultFile = "provider-secrets.json"
)

// ManagerOptions 构造受管密钥解析器。
type ManagerOptions struct {
	FilePath string // 受管密钥文件（0600，JSON map[ref]plaintext）
	Log      *slog.Logger
}

// Manager 是 Resolver 的默认实现：env:// 实时读环境变量，store:// 读 0600 的受管 JSON 文件。
//
// 并发模型：读路径持 RLock 共享已发布的 map；写路径持写锁，重新读盘后**复制一份新 map**
// 再落盘并发布。已发布的 map 永不再改，因此读路径无需深拷贝也不会有 data race。
type Manager struct {
	log *slog.Logger

	// pathErr 记录构造期错误：NewManager 不返回 error，路径定不下来时把错误留给
	// 真正需要文件的操作暴露（fail closed），而不是悄悄写到一个相对路径。
	pathErr  error
	filePath string

	mu      sync.RWMutex
	secrets map[string]string
	loaded  bool
	loadErr error
	modTime time.Time
	size    int64
}

var _ Resolver = (*Manager)(nil)

// NewManager 构造受管密钥解析器。FilePath 为空时默认 $HOME/.blockchain-platform/provider-secrets.json
// （与 config.DRPCConfig.ProviderSecretsFilePath 同一口径：systemd 以该用户运行，无需额外写权限）。
// 文件不存在不是错误：此时只有 env:// 引用可解析。
func NewManager(opts ManagerOptions) *Manager {
	if opts.Log == nil {
		opts.Log = slog.Default()
	}
	m := &Manager{log: opts.Log, filePath: strings.TrimSpace(opts.FilePath)}
	if m.filePath == "" {
		home, err := os.UserHomeDir()
		switch {
		case err != nil:
			m.pathErr = fmt.Errorf("resolve provider secrets path: %w", err)
		case strings.TrimSpace(home) == "":
			m.pathErr = errors.New("resolve provider secrets path: HOME is empty")
		default:
			m.filePath = filepath.Join(home, defaultDir, defaultFile)
		}
	}
	return m
}

// FilePath 返回受管文件路径（HOME 不可用时为空字符串）：仅供管理后台展示与排障。
func (m *Manager) FilePath() string { return m.filePath }

// Resolve 解析引用。返回值是明文，调用方必须只在构造请求时使用。
func (m *Manager) Resolve(ctx context.Context, ref string) (string, error) {
	if err := ctx.Err(); err != nil {
		return "", err
	}
	switch trimmed := strings.TrimSpace(ref); {
	case trimmed == "":
		return "", fmt.Errorf("resolve secret: %w", ErrNotConfigured)
	case strings.HasPrefix(trimmed, schemeEnv):
		name := strings.TrimSpace(strings.TrimPrefix(trimmed, schemeEnv))
		if name == "" {
			return "", fmt.Errorf("invalid secret ref %q: empty env name", ref)
		}
		// 空值按未配置处理：一个空 Key 发出去只会拿到 401，报"未配置"更接近事实。
		if value, ok := os.LookupEnv(name); ok && strings.TrimSpace(value) != "" {
			return value, nil
		}
		return "", fmt.Errorf("resolve %s: %w", trimmed, ErrNotConfigured)
	case strings.HasPrefix(trimmed, schemeStore):
		providerKey, kind, err := parseStoreRef(trimmed)
		if err != nil {
			return "", err
		}
		return m.resolveStore(trimmed, providerKey, kind)
	default:
		// 不认识的 scheme 是配置错误：报错而不是当作未配置，否则 vault:// 拼错会
		// 表现为"Provider 没配 Key"这种误导性现象。
		return "", fmt.Errorf("unsupported secret ref %q (want env://NAME or store://<provider>/<kind>)", ref)
	}
}

// Describe 返回可对外展示的状态。文件损坏时 Configured=false 且不返回错误
// （RefStatus 没有错误位），真实错误由 Resolve/Put 报出。
func (m *Manager) Describe(_ context.Context, ref string) RefStatus {
	trimmed := strings.TrimSpace(ref)
	status := RefStatus{Ref: trimmed}
	switch {
	case trimmed == "":
		status.Kind = KindEmpty
	case strings.HasPrefix(trimmed, schemeEnv):
		status.Kind = KindEnv
		status.Name = strings.TrimSpace(strings.TrimPrefix(trimmed, schemeEnv))
		if status.Name != "" {
			if value, ok := os.LookupEnv(status.Name); ok && strings.TrimSpace(value) != "" {
				status.Configured = true
				status.Prefix, status.Fingerprint = preview(value)
			}
		}
	case strings.HasPrefix(trimmed, schemeStore):
		status.Kind = KindStore
		providerKey, kind, err := parseStoreRef(trimmed)
		if err != nil {
			status.Kind = KindUnknown
			status.Name = strings.TrimSpace(strings.TrimPrefix(trimmed, schemeStore))
			return status
		}
		status.Name = providerKey + "/" + kind
		if value, err := m.resolveStore(trimmed, providerKey, kind); err == nil {
			status.Configured = true
			status.Prefix, status.Fingerprint = preview(value)
		}
	default:
		status.Kind = KindUnknown
	}
	return status
}

// Put 写入受管 store 并返回规范化引用 store://<provider>/<kind>。
// 明文不落 PostgreSQL、不进日志、不进审计：日志里只有引用与指纹。
func (m *Manager) Put(providerKey, kind, plaintext string) (ref string, status RefStatus, err error) {
	providerKey, kind = normalizeSegment(providerKey), normalizeSegment(kind)
	if err := validateSegment("provider key", providerKey); err != nil {
		return "", RefStatus{}, err
	}
	if err := validateSegment("secret kind", kind); err != nil {
		return "", RefStatus{}, err
	}
	// 空明文会写出一个永远解析不出值的引用；要清除请用 Delete（语义明确）。
	if strings.TrimSpace(plaintext) == "" {
		return "", RefStatus{}, errors.New("secret plaintext must not be empty")
	}
	ref = schemeStore + providerKey + "/" + kind
	err = m.update(func(current map[string]string) bool {
		current[ref] = plaintext
		return true
	})
	if err != nil {
		return "", RefStatus{}, err
	}
	status = m.Describe(context.Background(), ref)
	m.log.Info("provider secret stored",
		"provider", providerKey, "kind", kind, "ref", ref, "fingerprint", status.Fingerprint)
	return ref, status, nil
}

// Delete 删除受管密钥。幂等：键不存在时返回 nil，便于"先删后写"的轮换流程重跑。
func (m *Manager) Delete(providerKey, kind string) error {
	providerKey, kind = normalizeSegment(providerKey), normalizeSegment(kind)
	if err := validateSegment("provider key", providerKey); err != nil {
		return err
	}
	if err := validateSegment("secret kind", kind); err != nil {
		return err
	}
	bare := providerKey + "/" + kind
	ref := schemeStore + bare
	return m.update(func(current map[string]string) bool {
		changed := false
		// 同时清理手写文件里可能存在的简写键，避免"删了还能解析"的假象。
		for _, key := range []string{ref, bare, strings.ToLower(ref), strings.ToLower(bare)} {
			if _, ok := current[key]; ok {
				delete(current, key)
				changed = true
			}
		}
		return changed
	})
}

func (m *Manager) resolveStore(ref, providerKey, kind string) (string, error) {
	secrets, err := m.snapshot()
	if err != nil {
		return "", err
	}
	bare := providerKey + "/" + kind
	// 规范化引用优先；简写键与大小写变体只是为了兼容手工维护的文件。
	for _, key := range []string{ref, bare, strings.ToLower(ref), strings.ToLower(bare)} {
		if value, ok := secrets[key]; ok && strings.TrimSpace(value) != "" {
			return value, nil
		}
	}
	return "", fmt.Errorf("resolve %s: %w", ref, ErrNotConfigured)
}

// snapshot 返回当前密钥快照；磁盘文件比缓存新时先重读。
func (m *Manager) snapshot() (map[string]string, error) {
	m.mu.RLock()
	if m.freshLocked() {
		secrets, err := m.secrets, m.loadErr
		m.mu.RUnlock()
		if err != nil {
			return nil, err
		}
		return secrets, nil
	}
	m.mu.RUnlock()

	m.mu.Lock()
	defer m.mu.Unlock()
	if m.freshLocked() {
		if m.loadErr != nil {
			return nil, m.loadErr
		}
		return m.secrets, nil
	}
	return m.loadLocked()
}

// freshLocked 判断缓存是否仍与磁盘一致。stat 失败（文件被删或不可访问）视为过期，
// 让下一次读取重新尝试并报出当时的真实错误。
func (m *Manager) freshLocked() bool {
	if !m.loaded {
		return false
	}
	if m.pathErr != nil {
		return true // 路径都定不下来：错误是终态，不必每次 stat
	}
	st, err := os.Stat(m.filePath)
	if err != nil {
		return false
	}
	return st.ModTime().Equal(m.modTime) && st.Size() == m.size
}

// loadLocked 无条件重读磁盘（调用方持写锁）。坏文件不删除也不静默降级：
// 记录明确错误并由 Resolve 报出，绝不让调用方以为"只是没配 Key"。
func (m *Manager) loadLocked() (map[string]string, error) {
	if m.pathErr != nil {
		m.loaded, m.loadErr, m.secrets = true, m.pathErr, nil
		return nil, m.pathErr
	}
	raw, err := os.ReadFile(m.filePath)
	switch {
	case errors.Is(err, os.ErrNotExist):
		m.loaded, m.loadErr, m.secrets = true, nil, nil
		m.modTime, m.size = time.Time{}, 0
		return nil, nil
	case err != nil:
		m.loaded, m.secrets = true, nil
		m.loadErr = fmt.Errorf("read provider secrets %s: %w", m.filePath, err)
		m.recordStatLocked()
		return nil, m.loadErr
	}
	m.enforcePermLocked()
	secrets := map[string]string{}
	if err := json.Unmarshal(raw, &secrets); err != nil {
		m.loaded, m.secrets = true, nil
		m.loadErr = fmt.Errorf("parse provider secrets %s: %w", m.filePath, err)
		m.recordStatLocked()
		return nil, m.loadErr
	}
	m.loaded, m.loadErr, m.secrets = true, nil, secrets
	m.recordStatLocked()
	return secrets, nil
}

func (m *Manager) recordStatLocked() {
	if m.pathErr != nil {
		return
	}
	st, err := os.Stat(m.filePath)
	if err != nil {
		m.modTime, m.size = time.Time{}, 0
		return
	}
	m.modTime, m.size = st.ModTime(), st.Size()
}

// enforcePermLocked 收紧过宽的权限。为什么自动收紧而不是报错：明文已经落盘，报错并不能
// 降低暴露面；直接 chmod 0600 并告警，既恢复安全基线又不让下载链路因运维手滑而中断。
func (m *Manager) enforcePermLocked() {
	if runtime.GOOS == "windows" {
		return
	}
	st, err := os.Stat(m.filePath)
	if err != nil || st.Mode().Perm()&0o077 == 0 {
		return
	}
	m.log.Warn("provider secrets file is readable by other users; tightening to 0600",
		"path", m.filePath, "mode", st.Mode().Perm().String())
	if err := os.Chmod(m.filePath, 0o600); err != nil {
		m.log.Warn("tighten provider secrets permissions failed", "path", m.filePath, "error", err)
	}
}

// update 在写锁内完成"重读磁盘 → 改一份新 map → 原子落盘 → 发布新快照"。
//
// 每次都重读磁盘：后台 API 与下载 Worker 是不同进程，只合并本地缓存会丢掉对方的写入。
func (m *Manager) update(mutate func(current map[string]string) bool) error {
	if m.pathErr != nil {
		return m.pathErr
	}
	dir := filepath.Dir(m.filePath)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return fmt.Errorf("create provider secrets dir %s: %w", dir, err)
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	current, err := m.loadLocked()
	if err != nil {
		return err
	}
	next := make(map[string]string, len(current)+1)
	for key, value := range current {
		next[key] = value
	}
	if !mutate(next) {
		return nil // 无变化：不落盘，也不刷新 mtime
	}
	raw, err := json.MarshalIndent(next, "", "  ")
	if err != nil {
		return fmt.Errorf("encode provider secrets: %w", err)
	}
	if err := writeFileAtomic(m.filePath, raw); err != nil {
		return err
	}
	m.secrets, m.loaded, m.loadErr = next, true, nil
	m.recordStatLocked()
	return nil
}

// writeFileAtomic 以 0600 写临时文件、fsync 后 rename：崩溃或并发读都不会看到半截密钥文件。
func writeFileAtomic(path string, data []byte) error {
	dir := filepath.Dir(path)
	f, err := os.CreateTemp(dir, filepath.Base(path)+".tmp-*")
	if err != nil {
		return fmt.Errorf("create temp provider secrets: %w", err)
	}
	tmp := f.Name()
	defer func() { _ = os.Remove(tmp) }() // rename 成功后删除失败无副作用

	if err := f.Chmod(0o600); err != nil {
		f.Close()
		return fmt.Errorf("chmod temp provider secrets: %w", err)
	}
	if _, err := f.Write(data); err != nil {
		f.Close()
		return fmt.Errorf("write temp provider secrets: %w", err)
	}
	if err := f.Sync(); err != nil {
		f.Close()
		return fmt.Errorf("sync temp provider secrets: %w", err)
	}
	if err := f.Close(); err != nil {
		return fmt.Errorf("close temp provider secrets: %w", err)
	}
	if err := os.Rename(tmp, path); err != nil {
		return fmt.Errorf("replace provider secrets: %w", err)
	}
	if runtime.GOOS != "windows" {
		// 目录项持久化；失败不阻断：内容已 fsync，rename 的原子可见性不受影响。
		if d, err := os.Open(dir); err == nil {
			_ = d.Sync()
			_ = d.Close()
		}
	}
	return nil
}

// parseStoreRef 拆解 store://<provider>/<kind>：两段都非空才合法。
func parseStoreRef(ref string) (providerKey, kind string, err error) {
	rest := strings.TrimPrefix(ref, schemeStore)
	parts := strings.Split(rest, "/")
	if len(parts) != 2 {
		return "", "", fmt.Errorf("invalid secret ref %q (want store://<provider>/<kind>)", ref)
	}
	providerKey, kind = strings.TrimSpace(parts[0]), strings.TrimSpace(parts[1])
	if providerKey == "" || kind == "" {
		return "", "", fmt.Errorf("invalid secret ref %q (want store://<provider>/<kind>)", ref)
	}
	return providerKey, kind, nil
}

// normalizeSegment 统一小写并去空白：provider_key 与 secret_kind 在库内均为小写字面量
// （'drpc' / 'runtime_api_key'），规范化后同一个引用只有一种写法。
func normalizeSegment(s string) string { return strings.ToLower(strings.TrimSpace(s)) }

// validateSegment 只接受 [a-z0-9._-]：受控引用最终会拼进文件名之外的 JSON 键与 URL，
// 放开任意字符会让拼错的引用看起来像合法配置。
func validateSegment(label, segment string) error {
	if segment == "" {
		return fmt.Errorf("%s is required", label)
	}
	for _, r := range segment {
		switch {
		case r >= 'a' && r <= 'z', r >= '0' && r <= '9', r == '_', r == '-', r == '.':
		default:
			return fmt.Errorf("invalid %s %q: want [a-z0-9._-]", label, segment)
		}
	}
	return nil
}

// preview 返回对外可见的指纹信息：前 6 个字符 + sha256 前 16 hex。
// 指纹用于核对"前后两次写入的是不是同一个 Key"，不能反推明文。
func preview(plaintext string) (prefix, fingerprint string) {
	runes := []rune(plaintext)
	if len(runes) > prefixRunes {
		runes = runes[:prefixRunes]
	}
	sum := sha256.Sum256([]byte(plaintext))
	return string(runes), hex.EncodeToString(sum[:])[:fingerprintChars]
}
