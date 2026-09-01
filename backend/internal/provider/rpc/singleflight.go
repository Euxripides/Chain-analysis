package rpc

import "sync"

// ---- Singleflight（§57/§58）----
//
// 同一瞬间 100 个页面都要 eth_getCode(0xABC)：
// 只应产生 1 次 Provider 请求 + 99 个等待者。

type flightCall struct {
	wg  sync.WaitGroup
	res Response
	err error
}

// Singleflight 合并同 key 的并发请求。
type Singleflight struct {
	mu    sync.Mutex
	calls map[string]*flightCall
}

func NewSingleflight() *Singleflight { return &Singleflight{calls: map[string]*flightCall{}} }

// Do 执行或等待。shared=true 表示本次是搭了别人的车（未产生 Provider 请求）。
func (s *Singleflight) Do(key string, fn func() (Response, error)) (Response, error, bool) {
	s.mu.Lock()
	if call, ok := s.calls[key]; ok {
		s.mu.Unlock()
		call.wg.Wait()
		return call.res, call.err, true
	}
	call := &flightCall{}
	call.wg.Add(1)
	s.calls[key] = call
	s.mu.Unlock()

	call.res, call.err = fn()
	call.wg.Done()

	s.mu.Lock()
	delete(s.calls, key)
	s.mu.Unlock()
	return call.res, call.err, false
}

// InFlight 当前合并中的 key 数（观测用）。
func (s *Singleflight) InFlight() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return len(s.calls)
}
