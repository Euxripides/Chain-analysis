package api

import (
	"bytes"
	"context"
	"errors"
	"io"
	"net"
	"net/http"
	"os"
	"strconv"
	"sync"
	"time"
)

// ---------------------------------------------------------------------------
// C1-SCOPE-RACE-STALE, second half: the shared "late scope gate".
//
// Measured defect (c1-session-scope-convergence-20260913T181457Z, RACE B): a
// state-changing request that was authenticated while the session was in
// workspace A kept its authenticated principal while its BODY was still being
// uploaded (curl --limit-rate 40k, 160 KB, about 4 s of real in-flight time).
// The session was switched to workspace B inside that window and the request
// still committed a case into A.
//
// Why a middleware-time check alone cannot close it: identityMiddleware reads
// the session row and chi dispatches the matched route BEFORE the request body
// has been received, so a scope re-read at that point compares the snapshot with
// the very row it was derived from. The window that matters is "body still in
// flight", and it is only over once the body has been read to EOF.
//
// scopeBodyMiddleware therefore reads the whole body of a session-scoped
// state-changing request up front (bounded), and only then lets the request
// continue into the router. require/requireAny still make the scope decision --
// after the permission and CSRF checks, so the 401/403 matrix is unchanged --
// but that decision now runs after the last byte of the body has arrived, which
// is immediately before the handler commits. Any scope change that lands while
// the body is in flight is observed and refused with 409 before the handler
// runs, so no business object is created.
//
// Fail closed: a body that cannot be read to EOF (client disconnect), a body
// above the per-request bound, a body above the process-wide budget and a body
// that misses the read deadline are all refused before the handler runs, so none
// of them can produce a business object either.
// ---------------------------------------------------------------------------

// scopeBodyCtxKey carries the outcome of the late body gate for one request.
type scopeBodyCtxKey struct{}

type scopeBodyGateState struct {
	fullyReceived bool
}

func withScopeBodyReceived(ctx context.Context) context.Context {
	return context.WithValue(ctx, scopeBodyCtxKey{}, scopeBodyGateState{fullyReceived: true})
}

// scopeBodyFullyReceived reports whether the whole request body had already been
// received when the authorization decision ran. The scope guard requires it, so
// removing or reordering the gate can only ever refuse traffic (fail closed),
// never silently reopen the race window.
func scopeBodyFullyReceived(ctx context.Context) bool {
	st, ok := ctx.Value(scopeBodyCtxKey{}).(scopeBodyGateState)
	return ok && st.fullyReceived
}

// maxScopeGatedBodyBytes bounds what the gate will buffer for ONE request. Every
// JSON body the product accepts is decoded under a 10 MiB limit (handlers.go
// decodeJSON), so 12 MiB admits every legitimate request and refuses anything
// larger before the handler runs.
const maxScopeGatedBodyBytes = 12 << 20

// ---------------------------------------------------------------------------
// P1-A residual: the per-request bound above is not a cap on the process.
//
// Eight concurrent 12 MiB uploads showed a peak of +30.8 MiB of RSS rather than
// 96 MiB, because Go reclaimed the buffers quickly -- but that is an observation
// about scheduling, not a bound. Nothing stopped a larger number of concurrent
// (or deliberately slow) uploads from holding arbitrarily many buffers, so the
// process-wide cost of this middleware was unbounded.
//
// The three limits below make it bounded, fail closed, and independent of the
// request that happened to lose the race:
//
//	scopeBodyBudgetBytes      process-wide byte budget for buffered bodies
//	scopeBodyBudgetInflight   process-wide count of buffered bodies
//	scopeBodyReadDeadline     how long a single body may take to arrive
//
// A request reserves its declared Content-Length (or the whole 12 MiB bound when
// the length is not declared, so an undeclared length can never under-reserve)
// before a single byte is buffered, and releases the reservation once the handler
// has returned, because the buffer stays alive until then. Exhaustion refuses
// with 429 and a stable code BEFORE any body byte is read; a body that does not
// arrive inside the deadline is refused with 408 through a real connection read
// deadline. Neither refusal costs a database round trip, so /health/ready and the
// rest of the product keep working while the budget is exhausted.
//
// The defaults are deliberately far above any legitimate concurrent upload this
// deployment sees; the limits are env-overridable so a gate can drive the
// boundary exactly instead of generating hundreds of megabytes of traffic.
// ---------------------------------------------------------------------------

const (
	scopeBodyBudgetBytesDefault    = 128 << 20
	scopeBodyBudgetInflightDefault = 24
	scopeBodyReadDeadlineDefault   = 30 * time.Second
)

var (
	scopeBodyBudgetOnce     sync.Once
	scopeBodyBudgetBytes    int64
	scopeBodyBudgetInflight int
	scopeBodyReadDeadline   time.Duration
	scopeBodyBudgetMu       sync.Mutex
	scopeBodyBudgetUsed     int64
	scopeBodyBudgetHeld     int
)

// errScopeBodyTooLarge marks a body above the per-request gate bound.
var errScopeBodyTooLarge = errors.New("request body exceeds the scope gate bound")

// errScopeBodyTimeout marks a body that did not arrive inside the read deadline.
var errScopeBodyTimeout = errors.New("request body did not arrive inside the read deadline")

func scopeBodyPositiveEnvInt64(name string, fallback int64) int64 {
	if raw := os.Getenv(name); raw != "" {
		if v, err := strconv.ParseInt(raw, 10, 64); err == nil && v > 0 {
			return v
		}
	}
	return fallback
}

func scopeBodyLimits() (int64, int, time.Duration) {
	scopeBodyBudgetOnce.Do(func() {
		scopeBodyBudgetBytes = scopeBodyPositiveEnvInt64("SCOPE_BODY_MAX_TOTAL_BYTES", scopeBodyBudgetBytesDefault)
		scopeBodyBudgetInflight = int(scopeBodyPositiveEnvInt64("SCOPE_BODY_MAX_INFLIGHT", scopeBodyBudgetInflightDefault))
		scopeBodyReadDeadline = time.Duration(scopeBodyPositiveEnvInt64("SCOPE_BODY_READ_TIMEOUT_MS", int64(scopeBodyReadDeadlineDefault/time.Millisecond))) * time.Millisecond
	})
	return scopeBodyBudgetBytes, scopeBodyBudgetInflight, scopeBodyReadDeadline
}

// scopeBodyReserveFor is the amount a request must reserve BEFORE its body is
// buffered. An undeclared length reserves the whole per-request bound: reserving
// less would let a chunked upload bypass the budget.
func scopeBodyReserveFor(r *http.Request) int64 {
	if r.ContentLength > 0 && r.ContentLength <= maxScopeGatedBodyBytes {
		return r.ContentLength
	}
	return maxScopeGatedBodyBytes
}

// scopeBodyBudgetAcquire reserves room for one buffered body. It returns
// (release, true) when the caller may buffer, and (nil, false) when the process
// budget is exhausted. Refusing is the fail-closed direction: no byte is read and
// no handler runs.
func scopeBodyBudgetAcquire(reserve int64) (func(), bool) {
	total, maxInflight, _ := scopeBodyLimits()
	scopeBodyBudgetMu.Lock()
	defer scopeBodyBudgetMu.Unlock()
	if scopeBodyBudgetHeld >= maxInflight || scopeBodyBudgetUsed+reserve > total {
		return nil, false
	}
	scopeBodyBudgetUsed += reserve
	scopeBodyBudgetHeld++
	released := false
	return func() {
		scopeBodyBudgetMu.Lock()
		defer scopeBodyBudgetMu.Unlock()
		if released {
			return
		}
		released = true
		scopeBodyBudgetUsed -= reserve
		scopeBodyBudgetHeld--
		if scopeBodyBudgetUsed < 0 {
			scopeBodyBudgetUsed = 0
		}
		if scopeBodyBudgetHeld < 0 {
			scopeBodyBudgetHeld = 0
		}
	}, true
}

// scopeBodyBudgetSnapshot reports the live reservation, for tests and gates.
func scopeBodyBudgetSnapshot() (int64, int) {
	scopeBodyBudgetMu.Lock()
	defer scopeBodyBudgetMu.Unlock()
	return scopeBodyBudgetUsed, scopeBodyBudgetHeld
}

// scopeBodyReadTimedOut reports whether a read error is a deadline expiry rather
// than a client disconnect.
func scopeBodyReadTimedOut(err error) bool {
	if errors.Is(err, os.ErrDeadlineExceeded) {
		return true
	}
	var ne net.Error
	return errors.As(err, &ne) && ne.Timeout()
}

// stateChangingMethod mirrors the method set used by the scope guard.
func stateChangingMethod(method string) bool {
	switch method {
	case http.MethodGet, http.MethodHead, http.MethodOptions:
		return false
	}
	return true
}

// readBoundedBody reads r to EOF, refusing (instead of truncating) anything
// above limit.
func readBoundedBody(r io.Reader, limit int64) ([]byte, error) {
	buf, err := io.ReadAll(io.LimitReader(r, limit+1))
	if err != nil {
		return nil, err
	}
	if int64(len(buf)) > limit {
		return nil, errScopeBodyTooLarge
	}
	return buf, nil
}

// bufferScopeGatedBody reads the body of a scope-guarded request to EOF and
// returns the request carrying a replayable body plus the "body received"
// marker. A nil result means the request was already refused and the caller must
// not run the handler.
func (s *Server) bufferScopeGatedBody(w http.ResponseWriter, r *http.Request) *http.Request {
	if r.Body == nil {
		return r.WithContext(withScopeBodyReceived(r.Context()))
	}
	// A real connection read deadline, so a client that stops sending inside the
	// body cannot hold a reservation (and the goroutine reading it) for ever.
	_, _, readDeadline := scopeBodyLimits()
	rc := http.NewResponseController(w)
	if err := rc.SetReadDeadline(time.Now().Add(readDeadline)); err == nil {
		defer func() { _ = rc.SetReadDeadline(time.Time{}) }()
	}
	buf, err := readBoundedBody(r.Body, maxScopeGatedBodyBytes)
	_ = r.Body.Close()
	if err != nil {
		status := http.StatusBadRequest
		code := "request_body_unreadable"
		message := "The request body could not be read in full; the request was refused before any business logic ran."
		switch {
		case errors.Is(err, errScopeBodyTooLarge):
			status = http.StatusRequestEntityTooLarge
			code = "request_body_too_large"
			message = "The request body is larger than this endpoint accepts; the request was refused before any business logic ran."
		case scopeBodyReadTimedOut(err):
			status = http.StatusRequestTimeout
			code = "request_body_timeout"
			message = "The request body did not arrive in time; the request was refused before any business logic ran."
		}
		writeErrorCode(w, r, status, code, message, nil)
		return nil
	}
	r.Body = io.NopCloser(bytes.NewReader(buf))
	r.ContentLength = int64(len(buf))
	return r.WithContext(withScopeBodyReceived(r.Context()))
}

// scopeBodyMiddleware is registered right after mustChangePasswordMiddleware so
// a request that is going to be refused anyway is never buffered. Requests
// without a session-scoped principal (anonymous, header/system, organisation
// level) are left untouched: the scope guard does not apply to them, so their
// body must stay on the wire exactly as before.
func (s *Server) scopeBodyMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !stateChangingMethod(r.Method) {
			next.ServeHTTP(w, r)
			return
		}
		if !needsSessionWorkspaceCheck(r, PrincipalFrom(r.Context())) {
			next.ServeHTTP(w, r)
			return
		}
		// Reserve BEFORE reading a byte. The reservation is released only after the
		// handler has returned, because the buffered body lives until then.
		release, ok := scopeBodyBudgetAcquire(scopeBodyReserveFor(r))
		if !ok {
			w.Header().Set("Retry-After", "1")
			writeErrorCode(w, r, http.StatusTooManyRequests, "request_body_budget_exhausted",
				"This instance is already buffering as many request bodies as it accepts; the request was refused before any byte was read.", nil)
			return
		}
		defer release()
		gated := s.bufferScopeGatedBody(w, r)
		if gated == nil {
			return
		}
		next.ServeHTTP(w, gated)
	})
}
