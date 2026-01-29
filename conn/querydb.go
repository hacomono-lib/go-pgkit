package conn

import (
	"context"
	"database/sql"
	"sync"

	"github.com/jmoiron/sqlx"
)

// QueryDB wraps sqlx query operations and provides named parameter methods.
type QueryDB struct {
	mu         sync.RWMutex
	extContext sqlx.ExtContext
	role       string
	// SpanStarter is called to start a span for SQL operations.
	// If nil, no spans are created.
	SpanStarter SpanStarter
}

// NewQueryDB creates a new QueryDB with the given parameters.
// This is useful for testing.
func NewQueryDB(extContext sqlx.ExtContext, role string, spanStarter SpanStarter) *QueryDB {
	return &QueryDB{
		extContext:  extContext,
		role:        role,
		SpanStarter: spanStarter,
	}
}

// Refresh replaces the internal ExtContext with a new one.
func (q *QueryDB) Refresh(newExtContext sqlx.ExtContext) {
	q.mu.Lock()
	defer q.mu.Unlock()
	q.extContext = newExtContext
}

// getExtContext safely returns the current ExtContext.
func (q *QueryDB) getExtContext() sqlx.ExtContext {
	q.mu.RLock()
	defer q.mu.RUnlock()
	return q.extContext
}

// startSpan starts a span if SpanStarter is configured.
// Returns a no-op finish function and the original context if SpanStarter is nil.
func (q *QueryDB) startSpan(ctx context.Context, operationType, query string) (finish func(err error), newCtx context.Context) {
	if q.SpanStarter == nil {
		return func(err error) {}, ctx
	}
	return q.SpanStarter(ctx, operationType, query, q.role)
}

// QueryRowContext executes a query that returns a single row.
// Note: sql.Row is lazily evaluated — errors are not known until Scan() is called,
// so the span is finished immediately (error tracking is not possible here).
func (q *QueryDB) QueryRowContext(ctx context.Context, query string, args ...interface{}) *sql.Row {
	finish, ctx := q.startSpan(ctx, "query_row", query)
	defer finish(nil)

	ext := q.getExtContext()
	if db, ok := ext.(*sqlx.DB); ok {
		return db.QueryRowContext(ctx, query, args...)
	}
	if tx, ok := ext.(*sqlx.Tx); ok {
		return tx.QueryRowContext(ctx, query, args...)
	}
	// Fallback — should not be reached in normal usage
	panic("QueryRowContext: unknown ExtContext type")
}

// NamedQueryRowContext executes a named query that returns a single row.
// Like QueryRowContext, the result set is automatically closed,
// so it is safe to use within transactions.
func (q *QueryDB) NamedQueryRowContext(ctx context.Context, query string, arg interface{}) (row *sql.Row, err error) {
	finish, ctx := q.startSpan(ctx, "named_query_row", query)
	defer func() {
		finish(err)
	}()

	// Bind named parameters
	var boundQuery string
	var boundArgs []interface{}
	boundQuery, boundArgs, err = q.BindNamed(query, arg)
	if err != nil {
		return nil, err
	}

	// Execute directly on extContext to avoid double span
	ext := q.getExtContext()
	if db, ok := ext.(*sqlx.DB); ok {
		return db.QueryRowContext(ctx, boundQuery, boundArgs...), nil
	}
	if tx, ok := ext.(*sqlx.Tx); ok {
		return tx.QueryRowContext(ctx, boundQuery, boundArgs...), nil
	}
	panic("NamedQueryRowContext: unknown ExtContext type")
}

// NamedQueryContext executes a named query that returns rows.
func (q *QueryDB) NamedQueryContext(ctx context.Context, query string, arg interface{}) (rows *sqlx.Rows, err error) {
	finish, ctx := q.startSpan(ctx, "query", query)
	defer func() {
		finish(err)
	}()

	ext := q.getExtContext()

	switch e := ext.(type) {
	case *sqlx.DB:
		rows, err = e.NamedQueryContext(ctx, query, arg)
	case *sqlx.Tx:
		// Manual binding for transactions since our custom *sqlx.Tx doesn't have proper driver info
		var boundQuery string
		var boundArgs []interface{}
		boundQuery, boundArgs, err = q.BindNamed(query, arg)
		if err != nil {
			return nil, err
		}

		rows, err = e.QueryxContext(ctx, boundQuery, boundArgs...)
	default:
		rows, err = sqlx.NamedQueryContext(ctx, ext, query, arg)
	}

	return rows, err
}

// NamedExecContext executes a named query that does not return rows.
func (q *QueryDB) NamedExecContext(ctx context.Context, query string, arg interface{}) (result sql.Result, err error) {
	finish, ctx := q.startSpan(ctx, "exec", query)
	defer func() {
		finish(err)
	}()

	ext := q.getExtContext()

	switch e := ext.(type) {
	case *sqlx.DB:
		result, err = e.NamedExecContext(ctx, query, arg)
	case *sqlx.Tx:
		// Manual binding for transactions since our custom *sqlx.Tx doesn't have proper driver info
		var boundQuery string
		var boundArgs []interface{}
		boundQuery, boundArgs, err = q.BindNamed(query, arg)
		if err != nil {
			return nil, err
		}
		result, err = e.ExecContext(ctx, boundQuery, boundArgs...)
	default:
		result, err = sqlx.NamedExecContext(ctx, ext, query, arg)
	}

	return result, err
}

// BindNamed binds named parameters in the query to positional parameters.
func (q *QueryDB) BindNamed(query string, arg interface{}) (string, []interface{}, error) {
	ext := q.getExtContext()
	if db, ok := ext.(*sqlx.DB); ok {
		return db.BindNamed(query, arg)
	}
	// For transactions or other cases, always use PostgreSQL-style placeholders
	return sqlx.BindNamed(sqlx.DOLLAR, query, arg)
}
