package pgxretry

import (
	"context"
	"database/sql/driver"
	"errors"
	"fmt"
	"slices"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/jackc/pgx/v5/pgconn"
)

// retryableConn wraps a database connection with retry logic
type retryableConn struct {
	baseConn      driver.Conn
	dsn           string
	config        *Config
	driver        *Driver
	closed        bool
	inTransaction bool
	mu            sync.Mutex
	logger        Logger
	detector      ErrorDetector
	strategy      RetryStrategy
	resetFunc     ConnectionResetFunc
	mustDiscard   atomic.Bool
	// readOnlyCheckedAt is when checkReadOnly last found the connection writable.
	// database/sql calls ResetSession from one goroutine at a time, so it needs no lock.
	readOnlyCheckedAt time.Time
}

// SessionResetter is the interface for resetting session state.
// This is a copy of the internal database/sql interface.
type SessionResetter interface {
	ResetSession(ctx context.Context) error
}

// Ensure we implement all required interfaces
var (
	_ driver.Conn               = (*retryableConn)(nil)
	_ driver.ConnPrepareContext = (*retryableConn)(nil)
	_ driver.ConnBeginTx        = (*retryableConn)(nil)
	_ driver.Pinger             = (*retryableConn)(nil)
	_ driver.QueryerContext     = (*retryableConn)(nil)
	_ driver.ExecerContext      = (*retryableConn)(nil)
	_ SessionResetter           = (*retryableConn)(nil)
	_ driver.Validator          = (*retryableConn)(nil)
)

// IsValid implements driver.Validator. database/sql calls it before putting the connection back into the pool
// and closes the connection instead when it returns false.
func (c *retryableConn) IsValid() bool {
	return !c.mustDiscard.Load()
}

// markMustDiscard sets mustDiscard when err has one of Config.DiscardConnSQLStates,
// so that database/sql discards the connection. It reports whether err has one of them.
func (c *retryableConn) markMustDiscard(ctx context.Context, err error) bool {
	var pgErr *pgconn.PgError
	if !errors.As(err, &pgErr) || !slices.Contains(c.config.DiscardConnSQLStates, pgErr.Code) {
		return false
	}
	if !c.mustDiscard.CompareAndSwap(false, true) {
		return true
	}
	// Outside a transaction the caller gets ErrBadConn and database/sql runs the statement again on another
	// connection, so the error is not seen by the caller. Log it so that a failover can be noticed.
	c.logger.WarnContext(ctx, "discarding the connection because it returned an error in DiscardConnSQLStates",
		"sqlstate", pgErr.Code,
		"error", err.Error(),
	)
	return true
}

// Ping implements driver.Pinger
func (c *retryableConn) Ping(ctx context.Context) error {
	if pinger, ok := c.baseConn.(driver.Pinger); ok {
		return pinger.Ping(ctx)
	}
	// If Ping is not implemented, return nil
	return nil
}

// ResetSession implements SessionResetter.
// It delegates to the base connection's ResetSession if available, then checks whether the connection is
// read-only when Config.CheckReadOnlyConns is set.
// This is called when a connection is reused from the pool.
func (c *retryableConn) ResetSession(ctx context.Context) error {
	if resetter, ok := c.baseConn.(SessionResetter); ok {
		if err := resetter.ResetSession(ctx); err != nil {
			return err
		}
	}
	if !c.config.CheckReadOnlyConns || time.Since(c.readOnlyCheckedAt) < c.config.ReadOnlyCheckInterval {
		return nil
	}
	return c.checkReadOnly(ctx)
}

// checkReadOnly returns an error that is driver.ErrBadConn, so that database/sql closes the connection and takes
// another one, when the connection is read-only or the check fails.
func (c *retryableConn) checkReadOnly(ctx context.Context) error {
	readOnly, err := c.isReadOnly(ctx)
	if err != nil {
		c.logger.WarnContext(ctx, "discarding the connection because checking whether it is read-only failed",
			"error", err.Error(),
		)
		return fmt.Errorf("%w: checking whether the connection is read-only: %w", driver.ErrBadConn, err)
	}
	if readOnly {
		c.logger.WarnContext(ctx, "discarding the connection because it is read-only")
		return fmt.Errorf("%w (%w)", ErrReadOnlyConn, driver.ErrBadConn)
	}
	c.readOnlyCheckedAt = time.Now()
	return nil
}

// isReadOnly reports whether a statement run on the connection outside a transaction would be read-only,
// as pgconn.ValidateConnectTargetSessionAttrsReadWrite (target_session_attrs=read-write) does.
func (c *retryableConn) isReadOnly(ctx context.Context) (bool, error) {
	queryer, ok := c.baseConn.(driver.QueryerContext)
	if !ok {
		return false, errors.New("the base connection does not implement driver.QueryerContext")
	}
	rows, err := queryer.QueryContext(ctx, "SHOW transaction_read_only", nil)
	if err != nil {
		return false, err
	}
	defer func() { _ = rows.Close() }()
	dest := make([]driver.Value, 1)
	if err := rows.Next(dest); err != nil {
		return false, err
	}
	switch v := dest[0].(type) {
	case string:
		return v == "on", nil
	case []byte:
		return string(v) == "on", nil
	default:
		return false, fmt.Errorf("unexpected transaction_read_only value of type %T", v)
	}
}

// QueryContext implements driver.QueryerContext
func (c *retryableConn) QueryContext(ctx context.Context, query string, args []driver.NamedValue) (driver.Rows, error) {
	return executeWithRetry(ctx, c, func() (driver.Rows, error) {
		return c.executeQuery(ctx, query, args)
	})
}

// executeQuery executes the actual query
func (c *retryableConn) executeQuery(ctx context.Context, query string, args []driver.NamedValue) (driver.Rows, error) {
	if queryContext, ok := c.baseConn.(driver.QueryerContext); ok {
		return queryContext.QueryContext(ctx, query, args)
	}

	// Fallback to prepare/query pattern
	values := make([]driver.Value, len(args))
	for i, nv := range args {
		values[i] = nv.Value
	}

	var stmt driver.Stmt
	var err error

	if prepareContext, ok := c.baseConn.(driver.ConnPrepareContext); ok {
		stmt, err = prepareContext.PrepareContext(ctx, query)
	} else {
		stmt, err = c.baseConn.Prepare(query)
	}

	if err != nil {
		return nil, err
	}
	defer func() { _ = stmt.Close() }()

	// Convert to NamedValues and use QueryContext
	namedValues := make([]driver.NamedValue, len(values))
	for i, v := range values {
		namedValues[i] = driver.NamedValue{
			Ordinal: i + 1,
			Value:   v,
		}
	}

	if stmtQueryCtx, ok := stmt.(driver.StmtQueryContext); ok {
		return stmtQueryCtx.QueryContext(ctx, namedValues)
	}

	// Fallback error
	return nil, fmt.Errorf("statement does not support context-aware query")
}

// ExecContext implements driver.ExecerContext
func (c *retryableConn) ExecContext(ctx context.Context, query string, args []driver.NamedValue) (driver.Result, error) {
	return executeWithRetry(ctx, c, func() (driver.Result, error) {
		return c.executeExec(ctx, query, args)
	})
}

// executeExec executes the actual exec
func (c *retryableConn) executeExec(ctx context.Context, query string, args []driver.NamedValue) (driver.Result, error) {
	if execContext, ok := c.baseConn.(driver.ExecerContext); ok {
		return execContext.ExecContext(ctx, query, args)
	}

	// Fallback to prepare/exec pattern
	values := make([]driver.Value, len(args))
	for i, nv := range args {
		values[i] = nv.Value
	}

	var stmt driver.Stmt
	var err error

	if prepareContext, ok := c.baseConn.(driver.ConnPrepareContext); ok {
		stmt, err = prepareContext.PrepareContext(ctx, query)
	} else {
		stmt, err = c.baseConn.Prepare(query)
	}

	if err != nil {
		return nil, err
	}
	defer func() { _ = stmt.Close() }()

	// Convert to NamedValues and use ExecContext
	namedValues := make([]driver.NamedValue, len(values))
	for i, v := range values {
		namedValues[i] = driver.NamedValue{
			Ordinal: i + 1,
			Value:   v,
		}
	}

	if stmtExecCtx, ok := stmt.(driver.StmtExecContext); ok {
		return stmtExecCtx.ExecContext(ctx, namedValues)
	}

	// Fallback error
	return nil, fmt.Errorf("statement does not support context-aware exec")
}

// executeWithRetry executes an operation with retry logic for non-transaction contexts
func executeWithRetry[T any](ctx context.Context, c *retryableConn, operation func() (T, error)) (T, error) {
	var zero T
	var lastErr error

	// Check if in transaction - no retry for transactions
	c.mu.Lock()
	inTx := c.inTransaction
	c.mu.Unlock()

	if inTx {
		// In transaction: execute once and wrap errors
		result, err := operation()
		if c.markMustDiscard(ctx, err) || (err != nil && c.detector.IsRetryableError(err)) {
			// Wrap retryable errors to indicate they occurred in a transaction
			return result, WrapRetryableInTx(err)
		}
		return result, err
	}

	// Not in transaction: run once without re-running or resetting when retry is disabled
	if c.config.DisableRetry {
		result, err := operation()
		c.markMustDiscard(ctx, err)
		return result, err
	}

	// Not in transaction: can retry
	for attempt := 0; attempt <= c.config.MaxRetries; attempt++ {
		result, err := operation()
		// Retrying on this connection would fail the same way. ErrBadConn makes database/sql
		// discard it and run the statement again on another connection.
		if c.markMustDiscard(ctx, err) {
			return zero, &discardedConnError{err: err}
		}
		if err == nil {
			// Log successful recovery after retry
			if attempt > 0 {
				c.logger.InfoContext(ctx, fmt.Sprintf("database operation succeeded after retry (%d/%d attempts)", attempt+1, c.config.MaxRetries+1))
			}
			return result, nil
		}

		lastErr = err

		// Check if the error is retryable
		if !c.detector.IsRetryableError(err) {
			// Non-retryable error
			break
		}

		// Don't retry if this is the last attempt
		if attempt >= c.config.MaxRetries {
			c.logger.ErrorContext(ctx, fmt.Sprintf("database operation failed on final attempt (%d/%d)", attempt+1, c.config.MaxRetries+1),
				"error", err.Error(),
			)
			break
		}

		// Check if cached plan error
		isCachedPlan := c.isCachedPlanError(err)

		// Log the retry attempt
		c.logger.WarnContext(ctx, fmt.Sprintf("database operation failed, retrying (%d/%d)", attempt+1, c.config.MaxRetries+1),
			"error", err.Error(),
		)

		// Reset session for retryable errors (including cached plan errors)
		if c.resetFunc != nil {
			if isCachedPlan {
				c.logger.WarnContext(ctx, "cached plan error detected, calling connection reset function",
					"attempt", attempt+1,
				)
			} else {
				c.logger.WarnContext(ctx, "retryable error detected, calling connection reset function",
					"attempt", attempt+1,
				)
			}

			resetErr := c.resetFunc(ctx)
			if resetErr != nil {
				c.logger.ErrorContext(ctx, "connection reset function failed",
					"error", resetErr.Error(),
					"attempt", attempt+1,
				)
			}
		} else if isCachedPlan {
			c.logger.WarnContext(ctx, "cached plan error detected but connection reset function not available",
				"note", "consider setting ConnectionResetFunc for better error recovery",
			)
		}

		// Calculate backoff
		backoff := c.strategy.NextBackoff(attempt)
		if backoff < 0 {
			// No more retries allowed by strategy
			break
		}

		c.logger.DebugContext(ctx, "applying backoff before retry",
			"backoff_duration", backoff,
			"attempt", attempt+1,
		)

		// Wait with backoff
		select {
		case <-ctx.Done():
			return zero, ctx.Err()
		case <-time.After(backoff):
		}
	}

	return zero, lastErr
}

// isCachedPlanError checks if the error is a cached plan error
func (c *retryableConn) isCachedPlanError(err error) bool {
	if err == nil {
		return false
	}
	errMsg := err.Error()
	return strings.Contains(errMsg, "cached plan must not change result type")
}

// Prepare prepares a statement
func (c *retryableConn) Prepare(query string) (driver.Stmt, error) {
	return c.PrepareContext(context.Background(), query)
}

// PrepareContext prepares a statement with context
func (c *retryableConn) PrepareContext(ctx context.Context, query string) (driver.Stmt, error) {
	return executeWithRetry(ctx, c, func() (driver.Stmt, error) {
		return c.prepareDirect(ctx, query)
	})
}

// prepareDirect directly prepares a statement
func (c *retryableConn) prepareDirect(ctx context.Context, query string) (driver.Stmt, error) {
	if prepareContext, ok := c.baseConn.(driver.ConnPrepareContext); ok {
		return prepareContext.PrepareContext(ctx, query)
	}
	return c.baseConn.Prepare(query)
}

// Close closes the connection
func (c *retryableConn) Close() error {
	c.mu.Lock()
	defer c.mu.Unlock()

	if c.closed {
		return nil
	}

	c.closed = true
	return c.baseConn.Close()
}

// Begin starts a transaction
func (c *retryableConn) Begin() (driver.Tx, error) {
	return c.BeginTx(context.Background(), driver.TxOptions{})
}

// BeginTx starts a transaction with options
func (c *retryableConn) BeginTx(ctx context.Context, opts driver.TxOptions) (driver.Tx, error) {
	// Mark as in transaction
	c.mu.Lock()
	c.inTransaction = true
	c.mu.Unlock()

	var tx driver.Tx
	var err error

	beginTx, ok := c.baseConn.(driver.ConnBeginTx)
	if !ok {
		c.mu.Lock()
		c.inTransaction = false
		c.mu.Unlock()
		return nil, fmt.Errorf("connection does not support transaction context")
	}

	tx, err = beginTx.BeginTx(ctx, opts)
	if err != nil {
		c.mu.Lock()
		c.inTransaction = false
		c.mu.Unlock()
		return nil, err
	}

	return &retryableTx{
		baseTx: tx,
		conn:   c,
	}, nil
}

// retryableTx wraps a transaction
type retryableTx struct {
	baseTx driver.Tx
	conn   *retryableConn
}

// Commit commits the transaction
func (t *retryableTx) Commit() error {
	err := t.baseTx.Commit()

	// Mark transaction as ended
	t.conn.mu.Lock()
	t.conn.inTransaction = false
	t.conn.mu.Unlock()

	// Wrap retryable errors so Session.Transaction can recognize them
	if err != nil && t.conn.detector.IsRetryableError(err) {
		return WrapRetryableInTx(err)
	}

	return err
}

// Rollback rolls back the transaction
func (t *retryableTx) Rollback() error {
	err := t.baseTx.Rollback()

	// Mark transaction as ended
	t.conn.mu.Lock()
	t.conn.inTransaction = false
	t.conn.mu.Unlock()

	return err
}

// discardedConnError is driver.ErrBadConn, so that database/sql runs the statement again on another connection,
// and keeps the original error, which database/sql returns when every attempt fails.
type discardedConnError struct {
	err error
}

func (e *discardedConnError) Error() string   { return e.err.Error() }
func (e *discardedConnError) Unwrap() []error { return []error{driver.ErrBadConn, e.err} }
