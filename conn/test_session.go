//go:build !production
// +build !production

package conn

import (
	"context"
	"database/sql"
	"log"
	"testing"

	"github.com/jmoiron/sqlx"
	"github.com/jmoiron/sqlx/reflectx"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
)

// TestSession provides a test-scoped session with transaction isolation.
// All operations run inside a transaction that is rolled back on cleanup,
// leaving the database unchanged.
//
// Usage:
//
//	func TestMyFeature(t *testing.T) {
//	    ts := conn.NewTestSession(t, config)
//	    defer ts.Cleanup()
//
//	    ctx := ts.Context()
//	    err := ts.DB().WithContext(ctx).Table("products").Find(&products).Error
//	}
type TestSession struct {
	session *Session
	db      *gorm.DB // Store for transaction
	queryDB *QueryDB
	ctx     context.Context
	cleanup func()
}

// NewTestSession creates a new test session with the given config.
// Starts a transaction and registers automatic rollback via t.Cleanup().
func NewTestSession(t *testing.T, config *Config) *TestSession {
	t.Helper()

	session, err := config.Connect()
	require.NoError(t, err)

	require.NotNil(t, session.QueryDB(), "QueryDB should not be nil")

	// Begin transaction
	tx := session.DB().Begin()
	require.NoError(t, tx.Error)

	// Embed the transaction in the context
	ctx := context.WithValue(context.Background(), txKey{}, tx)

	testSession := &TestSession{
		session: session,
		db:      tx,
		ctx:     ctx,
	}

	// Create a transaction-bound QueryDB for sqlx operations
	queryDB, err := testSession.createTransactionQueryDB(tx)
	require.NoError(t, err)
	testSession.queryDB = queryDB

	cleanup := func() {
		if r := tx.Rollback(); r.Error != nil {
			// Ignore error if the transaction was already committed or rolled back
			if r.Error.Error() != "sql: transaction has already been committed or rolled back" {
				log.Printf("Failed to rollback the transaction: %v", r.Error)
			}
		}
		if err := session.Close(); err != nil {
			log.Printf("Failed to close session: %v", err)
		}
	}

	t.Cleanup(cleanup)

	testSession.cleanup = cleanup
	return testSession
}

// Session returns the underlying Session.
func (s *TestSession) Session() *Session {
	return s.session
}

// Writer returns a WriterSession wrapper for testing.
func (s *TestSession) Writer() *WriterSession {
	return &WriterSession{Session: s.session}
}

// Reader returns a ReaderSession wrapper for testing.
func (s *TestSession) Reader() *ReaderSession {
	return &ReaderSession{session: s.session}
}

// Context returns the context with the test transaction embedded.
func (s *TestSession) Context() context.Context {
	return s.ctx
}

// Cleanup rolls back the transaction and closes the session.
func (s *TestSession) Cleanup() {
	s.cleanup()
}

// DB returns the GORM DB bound to the test transaction.
func (s *TestSession) DB() *gorm.DB {
	return s.db.WithContext(s.ctx)
}

// QueryDB returns the QueryDB for this test session.
func (s *TestSession) QueryDB() *QueryDB {
	return s.queryDB
}

// GetDBOrTx returns the DB with transaction context.
func (s *TestSession) GetDBOrTx(ctx context.Context) *gorm.DB {
	if tx, ok := ctx.Value(txKey{}).(*gorm.DB); ok {
		return tx.WithContext(ctx)
	}
	return s.session.DB().WithContext(ctx)
}

// GetQueryDBOrTx returns a transaction-bound QueryDB if a transaction is set
// in the context, otherwise the default QueryDB.
func (s *TestSession) GetQueryDBOrTx() (*QueryDB, error) {
	if tx, ok := s.ctx.Value(txKey{}).(*gorm.DB); ok {
		return s.createTransactionQueryDB(tx)
	}
	return s.queryDB, nil
}

// SetRLSContext sets RLS session variables within the test transaction
// and returns an updated context.
//
// Uses SET LOCAL (is_local=true) so variables are scoped to the transaction
// and automatically cleared on rollback.
//
// vars maps variable names to values. An empty string value resets the variable to NULL.
// ctxSetter is an optional callback to also set values in the Go context (nil to skip).
func (s *TestSession) SetRLSContext(vars map[string]string, ctxSetter func(ctx context.Context, name, value string) context.Context) context.Context {
	ctx := s.ctx

	for name, value := range vars {
		if value == "" {
			err := s.DB().Exec("SELECT set_config(?, NULL, true)", name).Error
			if err != nil {
				panic("failed to reset " + name + " in transaction: " + err.Error())
			}
		} else {
			err := s.DB().Exec("SELECT set_config(?, ?, true)", name, value).Error
			if err != nil {
				panic("failed to set " + name + " in transaction: " + err.Error())
			}
			if ctxSetter != nil {
				ctx = ctxSetter(ctx, name, value)
			}
		}
	}

	return ctx
}

// createTransactionQueryDB creates a sqlx-based QueryDB from a GORM transaction.
func (s *TestSession) createTransactionQueryDB(tx *gorm.DB) (*QueryDB, error) {
	// Extract *sql.Tx from the GORM transaction
	if tx.Statement == nil || tx.Statement.ConnPool == nil {
		return nil, ErrTransaction.WithReason("[test] GORM transaction statement is not initialized").WithCallerStack()
	}

	// Try direct type assertion to *sql.Tx
	sqlTx, ok := tx.Statement.ConnPool.(*sql.Tx)
	if !ok {
		// Handle wrapped transactions (e.g., retryableTx)
		type unwrapper interface {
			Unwrap() any
		}

		if wrapper, ok := tx.Statement.ConnPool.(unwrapper); ok {
			if innerTx := wrapper.Unwrap(); innerTx != nil {
				if innerSQLTx, ok := innerTx.(*sql.Tx); ok {
					sqlTx = innerSQLTx
				}
			}
		}

		if sqlTx == nil {
			return nil, ErrTransaction.WithReason("[test] failed to cast ConnPool to *sql.Tx").WithCallerStack()
		}
	}

	// sqlTx -> sqlx.Tx -> QueryDB
	mapper := reflectx.NewMapper("db")

	sqlxTx := &sqlx.Tx{
		Tx:     sqlTx,
		Mapper: mapper,
	}
	return NewQueryDB(sqlxTx, "test", nil), nil
}
