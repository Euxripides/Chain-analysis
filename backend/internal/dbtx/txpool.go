// Package dbtx is the P1-A / AUTH-007 transaction seam.
//
// The commit gate used to hold a row lock on the caller session row from its
// re-check until the handler had committed, so a scope mutation (workspace
// switch, logout, admin force logout) had to queue behind the whole handler.
// AUTH-007 requires those mutations to take effect within 5 seconds, so the
// critical section must not contain the handler.
//
// The replacement is an optimistic commit token plus a real transaction:
//
//	entry    read the session scope token WITHOUT a lock
//	handler  every PG write of the request joins that same transaction
//	release  re-read the session row FOR UPDATE and compare the token
//	         token unchanged -> COMMIT
//	         token changed   -> ROLLBACK (the whole request is discarded)
//
// The transaction is carried in the request context, so no handler signature
// changes. Every store in this product reaches PostgreSQL through
// store.Store.Pool; that field is now *dbtx.Pool, a thin wrapper whose
// Exec/Query/QueryRow/Begin route to the context transaction when there is one
// and to the embedded *pgxpool.Pool otherwise. The 325 direct Pool call sites
// keep their code and their signatures exactly as they were.
//
// Deliberately NOT covered by this seam (documented, and asserted by tests):
//
//   - audit writes: audit.Writer keeps the raw *pgxpool.Pool so a deny audit
//     still lands when the business transaction is rolled back. The one
//     exception is an audit write performed INSIDE a gate request (WithGate),
//     which joins the request transaction instead of opening a second
//     connection: see WithGate for the self-deadlock that exception exists to
//     prevent.
//   - Acquire: handing out a second connection inside a gated request would
//     let a write escape the transaction, so it fails closed instead.
//   - nested Begin: routed to a SAVEPOINT on the same connection, never to a
//     second pooled connection.
//
// Package dbtx must not import any other internal package: it sits under every
// service.
package dbtx

import (
	"context"
	"errors"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"
)

// ErrNoPool is returned when a wrapped pool has no underlying pool. It exists
// so that a nil store keeps failing closed (an error) instead of panicking.
var ErrNoPool = errors.New("dbtx: no underlying pool")

// ErrConnectionNotAllowedInsideTx is returned when request code asks for a
// separate connection while a request transaction is active. It is fail-closed
// on purpose: a second connection would commit outside the gate transaction and
// silently break the all-or-nothing contract the gate promises.
var ErrConnectionNotAllowedInsideTx = errors.New("dbtx: a separate connection was requested inside a request transaction")

// holder keeps the transaction inside the context value so that a nil
// transaction is representable. context.WithValue refuses a nil value, which is
// exactly what WithoutTx has to install.
type holder struct{ tx pgx.Tx }

type ctxKey struct{}

// WithTx returns ctx carrying tx as the request transaction. A nil tx returns
// ctx unchanged.
func WithTx(ctx context.Context, tx pgx.Tx) context.Context {
	if ctx == nil || tx == nil {
		return ctx
	}
	return context.WithValue(ctx, ctxKey{}, holder{tx: tx})
}

// FromContext returns the request transaction carried by ctx, or nil.
func FromContext(ctx context.Context) pgx.Tx {
	if ctx == nil {
		return nil
	}
	h, ok := ctx.Value(ctxKey{}).(holder)
	if !ok || h.tx == nil {
		return nil
	}
	return h.tx
}

// WithoutTx returns ctx with the request transaction removed while every other
// value is preserved.
//
// This exists because context.WithoutCancel -- used by auditRequestDetached --
// keeps context VALUES, so a detached audit write would otherwise still find the
// gate transaction and roll back together with the business write it is
// supposed to record.
//
// The gate marker is removed as well: a context that may no longer join the
// request transaction must not claim to be a gate request either.
func WithoutTx(ctx context.Context) context.Context {
	if ctx == nil || (FromContext(ctx) == nil && !IsGateRequest(ctx)) {
		return ctx
	}
	ctx = context.WithValue(ctx, ctxKey{}, holder{})
	return context.WithValue(ctx, gateKey{}, gateHolder{})
}

// gateHolder keeps the gate marker inside the context value.
//
// It carries an explicit flag instead of relying on an empty string: WithGate must
// keep the join behaviour even when the request id is missing (a marker that
// silently switches itself off would silently bring the D-15 deadlock back), while
// WithoutTx must be able to install the "no longer a gate request" state.
type gateHolder struct {
	active    bool
	requestID string
}

type gateKey struct{}

// WithGate marks ctx as a commit-gate request context and returns it.
//
// The marker is what lets the audit writer tell the two kinds of "write outside
// the business transaction" apart inside one process:
//
//   - A gated request holds ONE transaction for its whole handler, and any
//     in-transaction audit write joins it (see internal/dbtx doc and
//     internal/api/scopecommit_gate.go), so that transaction owns the
//     audit_chain_heads row lock.
//   - An audit write that opens its OWN connection inside that same request
//     therefore waits for a lock its own request transaction can only release
//     after the handler returns: the request deadlocks against itself and is
//     only unstuck by the HTTP timeout, and the audit row that was supposed to
//     be recorded is dropped.
//
// audit.Writer therefore joins the request transaction when, and only when, the
// context carries this marker (see audit.Writer.Write).
func WithGate(ctx context.Context, requestID string) context.Context {
	if ctx == nil {
		return ctx
	}
	return context.WithValue(ctx, gateKey{}, gateHolder{active: true, requestID: requestID})
}

// IsGateRequest reports whether ctx belongs to a commit-gate request whose
// transaction is still open for joining.
func IsGateRequest(ctx context.Context) bool {
	if ctx == nil {
		return false
	}
	h, ok := ctx.Value(gateKey{}).(gateHolder)
	return ok && h.active
}

var _ = pgx.ErrNoRows

// Pool wraps *pgxpool.Pool. It embeds the pool so every method that is not
// overridden below (Ping, Close, Stat, Config, AcquireAllIdle, ...) keeps
// working unchanged.
type Pool struct {
	*pgxpool.Pool
}

// New wraps p. A nil p yields a nil *Pool so call sites keep their existing
// "pool == nil means no database" behaviour.
func New(p *pgxpool.Pool) *Pool {
	if p == nil {
		return nil
	}
	return &Pool{Pool: p}
}

// Raw returns the underlying pool. Audit code uses it explicitly, because audit
// writes must never join the request transaction.
func (p *Pool) Raw() *pgxpool.Pool {
	if p == nil {
		return nil
	}
	return p.Pool
}

func (p *Pool) missing() bool { return p == nil || p.Pool == nil }

// Exec runs sql in the request transaction when one is active.
func (p *Pool) Exec(ctx context.Context, sql string, args ...any) (pgconn.CommandTag, error) {
	if tx := FromContext(ctx); tx != nil {
		return tx.Exec(ctx, sql, args...)
	}
	if p.missing() {
		return pgconn.CommandTag{}, ErrNoPool
	}
	return p.Pool.Exec(ctx, sql, args...)
}

// Query runs sql in the request transaction when one is active.
func (p *Pool) Query(ctx context.Context, sql string, args ...any) (pgx.Rows, error) {
	if tx := FromContext(ctx); tx != nil {
		return tx.Query(ctx, sql, args...)
	}
	if p.missing() {
		return nil, ErrNoPool
	}
	return p.Pool.Query(ctx, sql, args...)
}

// QueryRow runs sql in the request transaction when one is active.
func (p *Pool) QueryRow(ctx context.Context, sql string, args ...any) pgx.Row {
	if tx := FromContext(ctx); tx != nil {
		return tx.QueryRow(ctx, sql, args...)
	}
	if p.missing() {
		return errRow{err: ErrNoPool}
	}
	return p.Pool.QueryRow(ctx, sql, args...)
}

// SendBatch runs the batch in the request transaction when one is active.
func (p *Pool) SendBatch(ctx context.Context, b *pgx.Batch) pgx.BatchResults {
	if tx := FromContext(ctx); tx != nil {
		return tx.SendBatch(ctx, b)
	}
	if p.missing() {
		return errBatchResults{err: ErrNoPool}
	}
	return p.Pool.SendBatch(ctx, b)
}

// CopyFrom runs the copy in the request transaction when one is active.
func (p *Pool) CopyFrom(ctx context.Context, table pgx.Identifier, columns []string, src pgx.CopyFromSource) (int64, error) {
	if tx := FromContext(ctx); tx != nil {
		return tx.CopyFrom(ctx, table, columns, src)
	}
	if p.missing() {
		return 0, ErrNoPool
	}
	return p.Pool.CopyFrom(ctx, table, columns, src)
}

// Begin returns the request transaction as a SAVEPOINT when one is active, so
// that existing "service opens its own transaction" code keeps its all-or-
// nothing meaning INSIDE the request, and never escapes to a second connection.
func (p *Pool) Begin(ctx context.Context) (pgx.Tx, error) {
	if tx := FromContext(ctx); tx != nil {
		return tx.Begin(ctx)
	}
	if p.missing() {
		return nil, ErrNoPool
	}
	return p.Pool.Begin(ctx)
}

// BeginTx is Begin with explicit options. Transaction options cannot be changed
// inside a running transaction, so a non-default request inside a gated request
// fails closed instead of being silently downgraded.
func (p *Pool) BeginTx(ctx context.Context, opts pgx.TxOptions) (pgx.Tx, error) {
	if tx := FromContext(ctx); tx != nil {
		if opts.IsoLevel != "" || opts.AccessMode != "" || opts.DeferrableMode != "" || opts.BeginQuery != "" {
			return nil, ErrConnectionNotAllowedInsideTx
		}
		return tx.Begin(ctx)
	}
	if p.missing() {
		return nil, ErrNoPool
	}
	return p.Pool.BeginTx(ctx, opts)
}

// Acquire fails closed inside a request transaction (see the package comment).
func (p *Pool) Acquire(ctx context.Context) (*pgxpool.Conn, error) {
	if FromContext(ctx) != nil {
		return nil, ErrConnectionNotAllowedInsideTx
	}
	if p.missing() {
		return nil, ErrNoPool
	}
	return p.Pool.Acquire(ctx)
}

type errRow struct{ err error }

func (r errRow) Scan(...any) error { return r.err }

type errBatchResults struct{ err error }

func (b errBatchResults) Exec() (pgconn.CommandTag, error) { return pgconn.CommandTag{}, b.err }
func (b errBatchResults) Query() (pgx.Rows, error)         { return nil, b.err }
func (b errBatchResults) QueryRow() pgx.Row                { return errRow{err: b.err} }
func (b errBatchResults) Close() error                     { return b.err }
