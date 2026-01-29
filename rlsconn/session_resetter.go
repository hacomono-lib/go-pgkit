package rlsconn

import (
	"context"
	"database/sql/driver"
	"fmt"
	"strings"
)

// RLSSessionResetter wraps a database/sql/driver.Conn and adds RLS session variable management.
// It sets PostgreSQL session variables to ensure RLS policies use the correct tenant/user context.
//
// Key design decision: Session variables are set when a connection is acquired, not per-query.
// This works through two mechanisms:
//   - New connections: RLSConnector.Connect(ctx) calls ResetSession(ctx) after creating
//     the connection, ensuring session variables are set immediately
//   - Reused connections: database/sql calls ResetSession(ctx) via SessionResetter interface
//     when acquiring a connection from the pool
//
// In both cases, the query context (containing tenant/user IDs) is passed to ResetSession,
// ensuring proper RLS isolation even with connection pooling.
//
// This struct does NOT manage transactions - that's handled by the application layer.
//
// Go best practices:
// - Does NOT store context as a field (context is passed to methods)
// - Stores baseConn and config as fields
type RLSSessionResetter struct {
	baseConn driver.Conn
	config   *Config
}

// NewRLSSessionResetter creates a new RLSSessionResetter.
// Note: context is NOT passed here (Go best practice).
func NewRLSSessionResetter(baseConn driver.Conn, config *Config) *RLSSessionResetter {
	// Ensure Logger is set
	if config.Logger == nil {
		config.Logger = NewNoopLogger()
	}
	return &RLSSessionResetter{
		baseConn: baseConn,
		config:   config,
	}
}

// ResetSession sets all configured session variables based on the context.
// It implements database/sql/driver.SessionResetter interface.
//
// This method is called in two scenarios:
//   - New connections: RLSConnector.Connect(ctx) calls this after creating the connection,
//     passing the query context
//   - Reused connections: database/sql calls this via SessionResetter interface when acquiring
//     a connection from the pool, passing the query context
//
// In both cases, the context contains the tenant/user IDs needed for RLS.
//
// Important: This sets session variables regardless of whether the table has RLS.
// - Tables with RLS policies: session variables are used for filtering
// - Tables without RLS policies: session variables are ignored (no impact)
// This design allows adding RLS to tables without application changes.
//
// Implementation notes:
//   - Uses set_config() function instead of SET command because SET doesn't support
//     parameterized queries ($1). set_config(name, value, is_local) is SQL injection safe.
//   - Performance optimization: All session variables are set in a single query using
//     multiple set_config() calls (e.g., "SELECT set_config($1, $2, false), set_config($3, $4, false)")
//     to reduce the number of database round-trips.
//
// Why is_local=false (session-scoped) instead of true (transaction-scoped):
//   - ResetSession() is called when acquiring a connection (before query execution)
//   - SET LOCAL (is_local=true) only works within a transaction and would be ineffective here
//   - Session-scoped settings persist until explicitly changed, which is overwritten
//     when the connection is reused with a different context
func (sr *RLSSessionResetter) ResetSession(ctx context.Context) error {
	if len(sr.config.SessionVars) == 0 {
		return nil
	}

	// Build a single query with multiple set_config() calls
	// e.g., "SELECT set_config($1, $2, false), set_config($3, $4, false)"
	var queryParts []string
	var args []any
	argIndex := 1

	for _, sv := range sr.config.SessionVars {
		if value, ok := sv.ValueGetter(ctx); ok {
			// Value exists: SET
			queryParts = append(queryParts, fmt.Sprintf("set_config($%d, $%d, false)", argIndex, argIndex+1))
			args = append(args, sv.Name, value)
			argIndex += 2
		} else {
			// Value doesn't exist: RESET by setting to NULL
			queryParts = append(queryParts, fmt.Sprintf("set_config($%d, NULL, false)", argIndex))
			args = append(args, sv.Name)
			argIndex++
		}
	}

	query := "SELECT " + strings.Join(queryParts, ", ")

	return sr.execQuery(ctx, query, args...)
}

// execQuery executes a query on the base connection.
// It tries driver.ExecerContext first, then falls back to driver.Execer.
// Returns an error if neither interface is supported (fail-closed for security).
func (sr *RLSSessionResetter) execQuery(ctx context.Context, query string, args ...any) error {
	// Call debug hook if configured (zero overhead if nil)
	if sr.config.OnExecQuery != nil {
		sr.config.OnExecQuery(ctx, query, args)
	}

	// Try ExecerContext first (preferred, supports context)
	if execer, ok := sr.baseConn.(driver.ExecerContext); ok {
		var namedValues []driver.NamedValue
		for i, arg := range args {
			namedValues = append(namedValues, driver.NamedValue{
				Ordinal: i + 1,
				Value:   arg,
			})
		}
		_, err := execer.ExecContext(ctx, query, namedValues)
		return err
	}

	// Fallback to Execer (deprecated but still supported by some drivers)
	if execer, ok := sr.baseConn.(driver.Execer); ok { //nolint:staticcheck // fallback for older drivers
		var values []driver.Value
		for _, arg := range args {
			values = append(values, arg)
		}
		_, err := execer.Exec(query, values) //nolint:staticcheck // fallback for older drivers
		return err
	}

	// Fail-closed: if we can't set RLS session variables, return an error
	// This prevents queries from executing without proper tenant isolation
	return fmt.Errorf("RLSSessionResetter.execQuery: driver does not support ExecerContext or Execer, cannot set RLS session variables")
}

// IsValid checks if the connection is still valid.
// Implements database/sql/driver.Validator interface.
func (sr *RLSSessionResetter) IsValid() bool {
	if validator, ok := sr.baseConn.(driver.Validator); ok {
		return validator.IsValid()
	}
	return true
}

// ----------------------------------------------------------------
// driver.Conn interface implementation (delegate to baseConn)
// These methods are required to satisfy the driver.Conn interface.
// RLSSessionResetter does NOT manage transactions - just delegates.
// ----------------------------------------------------------------

// Prepare prepares a statement.
func (sr *RLSSessionResetter) Prepare(query string) (driver.Stmt, error) {
	return sr.baseConn.Prepare(query)
}

// Close closes the connection.
func (sr *RLSSessionResetter) Close() error {
	return sr.baseConn.Close()
}

// Begin starts a transaction.
// Note: This just delegates to baseConn. Transaction management is at application layer.
//
// Deprecated: Use BeginTx instead. This method is kept for driver.Conn interface compatibility.
func (sr *RLSSessionResetter) Begin() (driver.Tx, error) {
	// Use BeginTx with default options (Go 1.8+)
	beginner := sr.baseConn.(driver.ConnBeginTx)
	return beginner.BeginTx(context.Background(), driver.TxOptions{})
}

// ----------------------------------------------------------------
// Optional interface implementations (delegate to baseConn if supported)
// ----------------------------------------------------------------

// QueryContext implements driver.QueryerContext if baseConn supports it.
// Note: ResetSession is NOT called here. Session variables are set via:
//   - New connections: RLSConnector.Connect(ctx) calls ResetSession
//   - Reused connections: database/sql calls ResetSession via SessionResetter interface
//     when acquiring a connection from the pool (with the query context)
func (sr *RLSSessionResetter) QueryContext(ctx context.Context, query string, args []driver.NamedValue) (driver.Rows, error) {
	queryer, ok := sr.baseConn.(driver.QueryerContext)
	if !ok {
		return nil, driver.ErrSkip
	}
	return queryer.QueryContext(ctx, query, args)
}

// ExecContext implements driver.ExecerContext if baseConn supports it.
// Note: ResetSession is NOT called here. Session variables are set via:
//   - New connections: RLSConnector.Connect(ctx) calls ResetSession
//   - Reused connections: database/sql calls ResetSession via SessionResetter interface
//     when acquiring a connection from the pool (with the query context)
func (sr *RLSSessionResetter) ExecContext(ctx context.Context, query string, args []driver.NamedValue) (driver.Result, error) {
	execer, ok := sr.baseConn.(driver.ExecerContext)
	if !ok {
		return nil, driver.ErrSkip
	}
	return execer.ExecContext(ctx, query, args)
}

// PrepareContext implements driver.ConnPrepareContext if baseConn supports it.
// Note: ResetSession is NOT called here - it was already called when the connection
// was acquired (via RLSConnector.Connect or SessionResetter interface).
func (sr *RLSSessionResetter) PrepareContext(ctx context.Context, query string) (driver.Stmt, error) {
	if preparer, ok := sr.baseConn.(driver.ConnPrepareContext); ok {
		return preparer.PrepareContext(ctx, query)
	}
	return sr.baseConn.Prepare(query)
}

// BeginTx implements driver.ConnBeginTx.
func (sr *RLSSessionResetter) BeginTx(ctx context.Context, opts driver.TxOptions) (driver.Tx, error) {
	beginner := sr.baseConn.(driver.ConnBeginTx)
	return beginner.BeginTx(ctx, opts)
}
