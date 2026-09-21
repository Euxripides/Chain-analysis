package drpc

// 本文件实现 Cursor / 分页契约（规范 §1.4 关键行为 5、开发文档 §7.3）：
//   - page_token 原样保存，不解析内部结构；
//   - 只对外暴露 sha256 摘要（"sha256:<hex>"），真实 Token 留在 Raw 与 Checkpoint；
//   - 重复 Token、循环 Token、连续两页完全相同 => provider_invalid_response；
//   - 页边界重复行（包含式分页）不是错误：只统计并记录，去重由下载层的幂等键完成。
import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"strconv"
	"strings"
	"sync"

	"github.com/blockchain-platform/blockchain-platform/internal/provider"
)

// PageState 是调用方提供的分页状态（§9.2 page_state 的 Provider 视角子集）。
//
// 跨进程恢复（Worker Kill-9 后继续）时由下载层从数据库读出后经 WithPageState 注入；
// 未注入时本包用进程内有界记忆兜底（见 cursorGuard）。
type PageState struct {
	// NextPageTokenHash 是上一页返回的 token 摘要。
	NextPageTokenHash string
	// SeenTokenHashes 是本 Shard 已使用过的 token 摘要集合。
	SeenTokenHashes []string
	// LastPageDigest 是上一页的 Provider 内容摘要，用于识别"连续两页完全相同"。
	LastPageDigest string
}

type pageStateContextKey struct{}

// WithPageState 把调用方的分页状态注入 ctx（调用方 PageState 优先于进程内记忆）。
func WithPageState(ctx context.Context, st PageState) context.Context {
	return context.WithValue(ctx, pageStateContextKey{}, st)
}

// PageStateFromContext 读取调用方注入的分页状态。
func PageStateFromContext(ctx context.Context) (PageState, bool) {
	st, ok := ctx.Value(pageStateContextKey{}).(PageState)
	return st, ok
}

// TokenHash 计算 page_token 的 sha256 摘要；空 token 返回空串（表示结束，不产生摘要）。
func TokenHash(token string) string {
	if token == "" {
		return ""
	}
	sum := sha256.Sum256([]byte(token))
	return "sha256:" + hex.EncodeToString(sum[:])
}

// PageDigest 计算一页内容摘要：对按返回顺序拼接的行负载取 sha256。
//
// 不排序：Provider 返回的顺序本身是内容的一部分；顺序无关的去重属于下载层职责。
func PageDigest(rows []json.RawMessage) string {
	hash := sha256.New()
	for _, row := range rows {
		hash.Write(row)
		hash.Write([]byte{0})
	}
	return "sha256:" + hex.EncodeToString(hash.Sum(nil))
}

// rowFingerprint 是单行指纹，用于识别页边界重复行。
func rowFingerprint(row json.RawMessage) string {
	sum := sha256.Sum256(row)
	return hex.EncodeToString(sum[:8])
}

// cursorOutcome 是一次分页校验的可见结果。
type cursorOutcome struct {
	// OverlapRows 是与上一页重复的行数：包含式分页是已知 Provider 行为，不视为错误，
	// 但必须可见（否则会误判"已完整下载"，开发文档 §21.2 第 2 项）。
	OverlapRows int
	// NextPageTokenHash 是本页返回 token 的摘要，供调用方写入 manifest / Checkpoint。
	NextPageTokenHash string
}

// cursorGuard 是进程内的分页兜底记忆：调用方未提供 PageState 时仍然能做循环检测。
//
// 局限（必须承认）：进程重启后记忆丢失；跨进程恢复必须依赖调用方 PageState 或数据库
// Checkpoint，这里只保证"同一进程内的连续翻页"不会静默打转。
type cursorGuard struct {
	mu sync.Mutex
	// seen 是 key -> 已用过的 token 摘要（有界，先进先出）。
	seen map[string][]string
	// lastDigest 是 key -> 上一页内容摘要。
	lastDigest map[string]string
	// lastRows 是 key -> 上一页的行指纹集合。
	lastRows map[string]map[string]struct{}
}

const (
	cursorMaxKeys         = 64
	cursorMaxHashesPerKey = 4096
	cursorMaxRowsPerKey   = 2048
)

func newCursorGuard() *cursorGuard {
	return &cursorGuard{
		seen:       map[string][]string{},
		lastDigest: map[string]string{},
		lastRows:   map[string]map[string]struct{}{},
	}
}

// validate 在拿到一页响应后调用。
//
// incoming 是本页请求带的 token，outgoing 是响应返回的 token，rows 是本页行集合。
// digest 必须是**Provider 内容**的摘要（见 providerPageDigest / signaturePageDigest）：
// 本地投影后的行含每次请求不同的 sourceRequestId，直接摘要会永远不相等。
func (g *cursorGuard) validate(key, incoming, outgoing, digest string, rows []json.RawMessage, st PageState, hasState bool) (cursorOutcome, error) {
	out := cursorOutcome{NextPageTokenHash: TokenHash(outgoing)}
	fingerprints := fingerprintRows(rows)
	outHash := out.NextPageTokenHash

	// 1) 空 Token 表示结束：记住本页内容后直接返回。
	if outgoing == "" {
		g.remember(key, incoming, digest, fingerprints)
		return out, nil
	}
	// 2) 同一 Token 原样返回：Provider 没有推进游标。
	if outgoing == incoming {
		return out, paginationInvalid("dRPC returned the same page_token it was given (" + outHash + ")")
	}
	var previousDigest string
	if hasState {
		// 3) 调用方 PageState 优先：上一页 token 与历史 token 集合来自数据库。
		if st.NextPageTokenHash != "" && outHash == st.NextPageTokenHash {
			return out, paginationInvalid("dRPC repeated the previous page_token (" + outHash + ")")
		}
		for _, seen := range st.SeenTokenHashes {
			if seen != "" && seen == outHash {
				return out, paginationInvalid("dRPC page_token loop detected (" + outHash + ")")
			}
		}
		previousDigest = st.LastPageDigest
	} else {
		if g.hasSeen(key, outHash) {
			return out, paginationInvalid("dRPC page_token loop detected (" + outHash + ")")
		}
		previousDigest = g.previousDigest(key)
		out.OverlapRows = g.overlap(key, fingerprints)
	}
	// 4) 连续两页完全相同：即使 token 变了也是分页异常。
	if digest != "" && previousDigest != "" && digest == previousDigest {
		return out, paginationInvalid("dRPC returned two identical consecutive pages")
	}
	g.remember(key, incoming, digest, fingerprints)
	g.rememberToken(key, outgoing)
	return out, nil
}

func fingerprintRows(rows []json.RawMessage) map[string]struct{} {
	if len(rows) == 0 {
		return nil
	}
	out := make(map[string]struct{}, len(rows))
	for _, row := range rows {
		out[rowFingerprint(row)] = struct{}{}
	}
	return out
}

func (g *cursorGuard) hasSeen(key, hash string) bool {
	if hash == "" {
		return false
	}
	g.mu.Lock()
	defer g.mu.Unlock()
	for _, item := range g.seen[key] {
		if item == hash {
			return true
		}
	}
	return false
}

func (g *cursorGuard) previousDigest(key string) string {
	g.mu.Lock()
	defer g.mu.Unlock()
	return g.lastDigest[key]
}

func (g *cursorGuard) overlap(key string, fingerprints map[string]struct{}) int {
	if len(fingerprints) == 0 {
		return 0
	}
	g.mu.Lock()
	defer g.mu.Unlock()
	previous := g.lastRows[key]
	if len(previous) == 0 {
		return 0
	}
	count := 0
	for fingerprint := range fingerprints {
		if _, ok := previous[fingerprint]; ok {
			count++
		}
	}
	return count
}

func (g *cursorGuard) remember(key, token, digest string, fingerprints map[string]struct{}) {
	g.mu.Lock()
	defer g.mu.Unlock()
	if len(g.seen) > cursorMaxKeys {
		// 简单的容量保护：整表清理，而不是引入 LRU 依赖。
		g.seen = map[string][]string{}
		g.lastDigest = map[string]string{}
		g.lastRows = map[string]map[string]struct{}{}
	}
	if digest != "" {
		g.lastDigest[key] = digest
	}
	if len(fingerprints) > 0 {
		if len(fingerprints) > cursorMaxRowsPerKey {
			trimmed := make(map[string]struct{}, cursorMaxRowsPerKey)
			count := 0
			for fingerprint := range fingerprints {
				trimmed[fingerprint] = struct{}{}
				count++
				if count >= cursorMaxRowsPerKey {
					break
				}
			}
			fingerprints = trimmed
		}
		g.lastRows[key] = fingerprints
	}
	if token != "" {
		g.rememberTokenLocked(key, TokenHash(token))
	}
}

func (g *cursorGuard) rememberToken(key, hash string) {
	g.mu.Lock()
	defer g.mu.Unlock()
	g.rememberTokenLocked(key, hash)
}

func (g *cursorGuard) rememberTokenLocked(key, hash string) {
	if hash == "" {
		return
	}
	hashes := g.seen[key]
	if len(hashes) >= cursorMaxHashesPerKey {
		hashes = hashes[len(hashes)-cursorMaxHashesPerKey/2:]
	}
	g.seen[key] = append(hashes, hash)
}

// cursorKey 是进程内记忆的分组键：同一 shard 的连续翻页才应该互相校验。
func cursorKey(req provider.FetchRequest) string {
	bounds := []string{req.Dataset, req.Address}
	if req.FromBlock != nil {
		bounds = append(bounds, "fb:"+strconv.FormatUint(*req.FromBlock, 10))
	}
	if req.ToBlock != nil {
		bounds = append(bounds, "tb:"+strconv.FormatUint(*req.ToBlock, 10))
	}
	if req.FromTime != nil {
		bounds = append(bounds, "ft:"+strconv.FormatInt(req.FromTime.Unix(), 10))
	}
	if req.ToTime != nil {
		bounds = append(bounds, "tt:"+strconv.FormatInt(req.ToTime.Unix(), 10))
	}
	return strings.Join(bounds, "|")
}
