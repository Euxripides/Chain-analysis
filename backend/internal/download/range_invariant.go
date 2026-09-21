package download

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"sync"

	"github.com/blockchain-platform/blockchain-platform/internal/ingest"
)

var (
	ErrRangeViolation              = errors.New("range violation")
	ErrTokenCrossShard             = errors.New("page token belongs to another shard")
	ErrCheckpointRangeMismatch     = errors.New("checkpoint range mismatch")
	ErrResponseAttributionMismatch = errors.New("response attribution mismatch")
)

// RangeEnvelope is immutable for one shard. End is exclusive.
type RangeEnvelope struct{ Start, End uint64 }

func NewRangeEnvelope(start, endExclusive uint64) (RangeEnvelope, error) {
	if endExclusive <= start {
		return RangeEnvelope{}, fmt.Errorf("invalid range [%d,%d)", start, endExclusive)
	}
	return RangeEnvelope{Start: start, End: endExclusive}, nil
}

func ToAnkrInclusiveRange(start, endExclusive uint64) (uint64, uint64, error) {
	e, err := NewRangeEnvelope(start, endExclusive)
	if err != nil {
		return 0, 0, err
	}
	return e.Start, e.End - 1, nil
}

type RangeInvariantResult struct {
	MinBlock, MaxBlock          uint64
	InRangeRows, OutOfRangeRows int
}

func ValidateTransactionRange(start, endExclusive uint64, rows []json.RawMessage) (RangeInvariantResult, error) {
	e, err := NewRangeEnvelope(start, endExclusive)
	if err != nil {
		return RangeInvariantResult{}, err
	}
	r := RangeInvariantResult{}
	for _, raw := range rows {
		bn, ok := ingest.RawBlockNumber(raw)
		if !ok {
			continue
		}
		if r.MinBlock == 0 || bn < r.MinBlock {
			r.MinBlock = bn
		}
		if bn > r.MaxBlock {
			r.MaxBlock = bn
		}
		if bn < e.Start || bn >= e.End {
			r.OutOfRangeRows++
		} else {
			r.InRangeRows++
		}
	}
	if r.OutOfRangeRows > 0 {
		return r, fmt.Errorf("%w: range=[%d,%d) response=%d..%d out_of_range=%d", ErrRangeViolation, start, endExclusive, r.MinBlock, r.MaxBlock, r.OutOfRangeRows)
	}
	return r, nil
}

func tokenHashP0(token string) string {
	if token == "" {
		return ""
	}
	s := sha256.Sum256([]byte(token))
	return hex.EncodeToString(s[:])
}

type tokenBinding struct {
	start, end uint64
	subject    string
	dataset    string
	shards     map[string]struct{}
}
type tokenRegistry struct {
	mu     sync.Mutex
	owners map[string]tokenBinding
}

func newTokenRegistry() *tokenRegistry { return &tokenRegistry{owners: map[string]tokenBinding{}} }

// claim 把 provider 返回的 pageToken 绑定到"它属于哪条地址流"。
//
// 绑定键是 (subject, dataset, start_block)，**不是**完整区间：同一个地址、
// 同一个 dataset、同一个起始块的游标，在 end_block 不同时仍然指向上游同一
// 个分页位置（例如重复创建的任务 head block 不同，或同一地址被两个任务
// 覆盖）。旧实现要求 end 也完全相同，于是把这类完全合法的响应判成
// "page token belongs to another shard" 并让 Shard 非重试失败 —— 现场
// DL-20260915-0002/0003/0009 因此永久卡住（2026-09-15）。
//
// 仍然硬失败的：游标被另一条地址流、另一个 dataset、或另一个起始块使用。
func (r *tokenRegistry) claim(token, shard, subject, dataset string, start, end uint64) error {
	h := tokenHashP0(token)
	if h == "" {
		return nil
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	if old, ok := r.owners[h]; ok {
		if old.start != start || old.subject != subject || old.dataset != dataset {
			return fmt.Errorf("%w: token_hash=%s old=[subject=%s dataset=%s range=%d..%d) new=[subject=%s dataset=%s range=%d..%d) new_shard=%s",
				ErrTokenCrossShard, h, old.subject, old.dataset, old.start, old.end, subject, dataset, start, end, shard)
		}
		old.shards[shard] = struct{}{}
		r.owners[h] = old
		return nil
	}
	r.owners[h] = tokenBinding{start: start, end: end, subject: subject, dataset: dataset, shards: map[string]struct{}{shard: {}}}
	return nil
}
func (r *tokenRegistry) releaseShard(shard string) {
	r.mu.Lock()
	defer r.mu.Unlock()
	for h, b := range r.owners {
		delete(b.shards, shard)
		if len(b.shards) == 0 {
			delete(r.owners, h)
		} else {
			r.owners[h] = b
		}
	}
}

func validateAttribution(expected, actual string) error {
	if expected == "" || expected != actual {
		return fmt.Errorf("%w: expected=%s actual=%s", ErrResponseAttributionMismatch, expected, actual)
	}
	return nil
}
