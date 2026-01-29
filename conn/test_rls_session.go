//go:build !production
// +build !production

package conn

import (
	"context"
	"log"
	"testing"

	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
)

// TestRLSSession provides a test session for verifying the RLS implementation itself.
//
// Manages two sessions:
//   - superuserSession: for data setup/cleanup (bypasses RLS)
//   - rlsSession: for verifying RLS behavior (RLS enforced, same flow as production)
//
// Usage:
//
//	func TestRLSImplementation(t *testing.T) {
//	    ts := conn.NewTestRLSSession(t, superuserConfig, rlsConfig)
//	    defer ts.Cleanup()
//
//	    // Setup test data (as superuser, bypasses RLS)
//	    ts.ExecAsSuperuser(ctx, "INSERT INTO tenants ...")
//
//	    // Verify RLS (production-equivalent is_local=false flow)
//	    var count int64
//	    err := ts.RLSSession().DB().WithContext(ctxWithTenant).
//	        Table("products").Count(&count).Error
//	}
type TestRLSSession struct {
	t                *testing.T
	superuserSession *Session
	rlsSession       *Session
	cleanupFuncs     []func()
}

// NewTestRLSSession creates a new RLS test session.
// Accepts both a superuser config (for RLS bypass) and an RLS config (for RLS enforcement).
func NewTestRLSSession(t *testing.T, superuserConfig, rlsConfig *Config) *TestRLSSession {
	t.Helper()

	superuserSession, err := superuserConfig.Connect()
	require.NoError(t, err, "failed to connect superuser session")

	rlsSession, err := rlsConfig.Connect()
	require.NoError(t, err, "failed to connect RLS session")

	ts := &TestRLSSession{
		t:                t,
		superuserSession: superuserSession,
		rlsSession:       rlsSession,
		cleanupFuncs:     make([]func(), 0),
	}

	t.Cleanup(ts.Cleanup)

	return ts
}

// SuperuserSession returns the session that bypasses RLS.
func (ts *TestRLSSession) SuperuserSession() *Session {
	return ts.superuserSession
}

// RLSSession returns the session with RLS enforced.
func (ts *TestRLSSession) RLSSession() *Session {
	return ts.rlsSession
}

// SuperuserDB returns the GORM DB for the superuser session.
func (ts *TestRLSSession) SuperuserDB() *gorm.DB {
	return ts.superuserSession.DB()
}

// RLSDB returns the GORM DB for the RLS-enforced session.
func (ts *TestRLSSession) RLSDB() *gorm.DB {
	return ts.rlsSession.DB()
}

// RegisterCleanup registers a cleanup function.
// Registered functions are executed in reverse order (LIFO).
func (ts *TestRLSSession) RegisterCleanup(fn func()) {
	ts.cleanupFuncs = append(ts.cleanupFuncs, fn)
}

// Cleanup runs registered cleanup functions and closes both sessions.
func (ts *TestRLSSession) Cleanup() {
	// Run cleanup functions in reverse order (child tables first)
	for i := len(ts.cleanupFuncs) - 1; i >= 0; i-- {
		ts.cleanupFuncs[i]()
	}

	if ts.rlsSession != nil {
		if err := ts.rlsSession.Close(); err != nil {
			log.Printf("Failed to close RLS session: %v", err)
		}
	}
	if ts.superuserSession != nil {
		if err := ts.superuserSession.Close(); err != nil {
			log.Printf("Failed to close superuser session: %v", err)
		}
	}
}

// ExecAsSuperuser executes arbitrary SQL using the superuser session.
func (ts *TestRLSSession) ExecAsSuperuser(ctx context.Context, sql string, args ...any) {
	ts.t.Helper()
	err := ts.superuserSession.DB().WithContext(ctx).Exec(sql, args...).Error
	require.NoError(ts.t, err, "failed to execute SQL: %s", sql)
}

// ExecAsSuperuserWithCleanup executes SQL using the superuser session
// and registers a cleanup SQL statement to be executed on test teardown.
func (ts *TestRLSSession) ExecAsSuperuserWithCleanup(ctx context.Context, sql, cleanupSQL string, args ...any) {
	ts.t.Helper()
	ts.ExecAsSuperuser(ctx, sql, args...)

	ts.RegisterCleanup(func() {
		_ = ts.superuserSession.DB().WithContext(context.Background()).Exec(cleanupSQL, args...).Error
	})
}
