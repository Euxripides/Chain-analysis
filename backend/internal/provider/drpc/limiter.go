package drpc

// 本文件实现限流原语。限流顺序（规范 §1.4 关键行为 3）：
//
//	通道信号量(RequestConcurrency) -> CU Token Bucket -> RPM/RPS Token Bucket -> quotaGate
//
// 开发文档 §6.1 把全局机器槽位排在通道槽位之前；全局槽位属于 Worker 职责，不在本包范围内。
// quotaGate 是账户级共享闸门（额度/Key 状态），排在最后是冻结规范的顺序。
//
// 令牌桶容量固定为"单次请求所需"，不蓄积突发额度（开发文档 §6.2：超出的部分在
// Token Bucket 等待，不发出突发请求）。
import (
	"context"
	"sync"
	"time"
)

// sleepFunc 是等待注入点：生产用定时器，测试用假时钟（不需要真的 sleep 60 秒）。
type sleepFunc func(ctx context.Context, d time.Duration) error

func sleepContext(ctx context.Context, d time.Duration) error {
	if d <= 0 {
		return nil
	}
	timer := time.NewTimer(d)
	defer timer.Stop()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-timer.C:
		return nil
	}
}

type tokenBucket struct {
	mu         sync.Mutex
	capacity   float64
	tokens     float64
	ratePerSec float64
	last       time.Time
	now        func() time.Time
	sleep      sleepFunc
}

func newTokenBucket(capacity, ratePerSec float64, now func() time.Time, sleep sleepFunc) *tokenBucket {
	if capacity <= 0 {
		capacity = 1
	}
	if ratePerSec < 0 {
		ratePerSec = 0
	}
	if sleep == nil {
		sleep = sleepContext
	}
	return &tokenBucket{
		capacity: capacity, tokens: capacity, ratePerSec: ratePerSec,
		last: now(), now: now, sleep: sleep,
	}
}

// setRate 更新补充速率（Profile/额度变化时调用）；已积累的令牌不超过容量。
func (b *tokenBucket) setRate(ratePerSec float64) {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.refill(b.now())
	if ratePerSec < 0 {
		ratePerSec = 0
	}
	b.ratePerSec = ratePerSec
	if b.tokens > b.capacity {
		b.tokens = b.capacity
	}
}

func (b *tokenBucket) refill(now time.Time) {
	if now.After(b.last) {
		b.tokens += now.Sub(b.last).Seconds() * b.ratePerSec
		if b.tokens > b.capacity {
			b.tokens = b.capacity
		}
	}
	b.last = now
}

// take 阻塞直到取得 n 个令牌或 ctx 结束。
func (b *tokenBucket) take(ctx context.Context, n float64) error {
	for {
		b.mu.Lock()
		b.refill(b.now())
		if b.tokens >= n {
			b.tokens -= n
			b.mu.Unlock()
			return nil
		}
		if b.ratePerSec <= 0 {
			// 速率为 0：等到 ctx 结束，避免忙等。
			b.mu.Unlock()
			<-ctx.Done()
			return ctx.Err()
		}
		wait := time.Duration((n - b.tokens) / b.ratePerSec * float64(time.Second))
		if wait < time.Millisecond {
			wait = time.Millisecond
		}
		b.mu.Unlock()
		// 等待期间令牌可能被别人取走，因此循环重试。
		if err := b.sleep(ctx, wait); err != nil {
			return err
		}
	}
}

// channelLimiter 是单通道（data_api / solana_rpc）在某个有效模式下的限流器。
type channelLimiter struct {
	sem          chan struct{}
	cu           *tokenBucket
	req          *tokenBucket
	cuPerMinute  float64
	cuPerRequest float64
	reqPerMinute float64
}

func newChannelLimiter(concurrency int, reqPerMinute, cuPerMinute, cuPerRequest float64, now func() time.Time, sleep sleepFunc) *channelLimiter {
	if concurrency <= 0 {
		concurrency = 1
	}
	if cuPerRequest <= 0 {
		cuPerRequest = 1
	}
	return &channelLimiter{
		sem: make(chan struct{}, concurrency),
		// CU 桶容量 = 单次请求所需，不蓄积突发额度。
		cu:           newTokenBucket(cuPerRequest, cuPerMinute/60, now, sleep),
		req:          newTokenBucket(1, reqPerMinute/60, now, sleep),
		cuPerMinute:  cuPerMinute,
		cuPerRequest: cuPerRequest,
		reqPerMinute: reqPerMinute,
	}
}

// configure 按（已钳制的）Profile 更新速率与 CU 上限。
func (l *channelLimiter) configure(reqPerMinute, cuPerMinute, cuPerRequest float64) {
	if cuPerRequest <= 0 {
		cuPerRequest = 1
	}
	l.cuPerMinute = cuPerMinute
	l.cuPerRequest = cuPerRequest
	l.reqPerMinute = reqPerMinute
	l.cu.setRate(cuPerMinute / 60)
	l.req.setRate(reqPerMinute / 60)
}

// acquire 依次通过信号量 -> CU 桶 -> RPM/RPS 桶，返回释放信号量的函数。
func (l *channelLimiter) acquire(ctx context.Context, cu float64) (func(), error) {
	select {
	case l.sem <- struct{}{}:
	case <-ctx.Done():
		return nil, ctx.Err()
	}
	release := func() { <-l.sem }
	if err := l.cu.take(ctx, cu); err != nil {
		release()
		return nil, err
	}
	if err := l.req.take(ctx, 1); err != nil {
		release()
		return nil, err
	}
	return release, nil
}

// effectiveRPM 是 CU 上限与请求速率共同约束下的实际每分钟请求数。
//
// 例：CU 45,000/分钟、单请求 1,837 CU => floor(45000/1837) = 24 次/分钟（开发文档 §6.2）。
func (l *channelLimiter) effectiveRPM() float64 {
	rpm := l.reqPerMinute
	if l.cuPerRequest > 0 {
		if cuBound := l.cuPerMinute / l.cuPerRequest; cuBound < rpm {
			rpm = cuBound
		}
	}
	if rpm < 0 {
		return 0
	}
	return rpm
}

// effectiveRPS 是实际每秒请求数（RPC 通道用）。
func (l *channelLimiter) effectiveRPS() float64 { return l.effectiveRPM() / 60 }
