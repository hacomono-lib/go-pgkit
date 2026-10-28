package conn

import (
	"context"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"

	"github.com/hacomono-lib/go-pgkit/rlsctx"
)

// With retry disabled, the session still goes through pgxretry (unless RLS is also disabled)
// so that retryable errors in transactions are wrapped with ErrRetryableInTx,
// but pgxretry does not re-run a query that fails outside a transaction.
func TestSessionConnector_RetryDisabled(t *testing.T) {
	s := newSession(NewConfig(WithEnvVars("writer"), WithRetry(false, 0, 0)))
	require.NotNil(t, s.retryConfig)
	assert.True(t, s.retryConfig.DisableRetry)

	s = newSession(NewConfig(WithEnvVars("writer"), WithRetry(false, 0, 0), WithRLS(false)))
	assert.Nil(t, s.retryConfig)
}

// TransactionManager re-runs a transaction that fails with a retryable error in a new transaction,
// with retry enabled or disabled (WithRetry applies only to queries outside transactions).
func TestSessionConnector_TransactionManagerRetries(t *testing.T) {
	tests := []struct {
		name string
		opts []ConfigOption
	}{
		{name: "retry enabled", opts: nil},
		{name: "retry disabled", opts: []ConfigOption{WithRetry(false, 0, 0)}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			writer, err := NewWriterSession(NewConfig(append([]ConfigOption{WithEnvVars("writer")}, tt.opts...)...))
			require.NoError(t, err)
			t.Cleanup(func() { _ = writer.Close() })

			tm := NewTransactionManager(writer, WithRetryDelay(10*time.Millisecond))
			var attempts int
			err = tm.Do(context.Background(), func(txCtx context.Context) error {
				attempts++
				if attempts == 1 {
					// Terminate the connection in the transaction (SQLSTATE 57P01, retryable).
					return writer.GetDBOrTx(txCtx).Exec("SELECT pg_terminate_backend(pg_backend_pid())").Error
				}
				return writer.GetDBOrTx(txCtx).Exec("SELECT 1").Error
			})

			require.NoError(t, err)
			assert.Equal(t, 2, attempts)
		})
	}
}

// Each session uses its own connector, so RLS is applied only to sessions with RLS enabled,
// whether or not retry is enabled and whichever session connects first.
func TestSessionConnector_RLSPerSession(t *testing.T) {
	tests := []struct {
		name string
		opts []ConfigOption
	}{
		{name: "retry enabled", opts: nil},
		{name: "retry disabled", opts: []ConfigOption{WithRetry(false, 0, 0)}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// Connects first, without RLS.
			noRLS, err := NewConfig(append([]ConfigOption{WithEnvVars("writer"), WithRLS(false)}, tt.opts...)...).Connect()
			require.NoError(t, err)
			t.Cleanup(func() { _ = noRLS.Close() })

			rls, err := NewConfig(append([]ConfigOption{WithEnvVars("writer"), WithRLS(true)}, tt.opts...)...).Connect()
			require.NoError(t, err)
			t.Cleanup(func() { _ = rls.Close() })

			tenantID := uuid.New()
			ctx := rlsctx.WithTenantID(context.Background(), tenantID)

			got, err := rls.GetCurrentTenantID(ctx)
			require.NoError(t, err)
			assert.Equal(t, tenantID.String(), got)

			// The RLS session variables are not set on the session without RLS.
			got, err = noRLS.GetCurrentTenantID(ctx)
			require.NoError(t, err)
			assert.Empty(t, got)
		})
	}
}

// When GORM fails to open the session (e.g., its ping fails), the connection pool it created is closed,
// with or without the session's own connector.
func TestSessionConnector_ClosesDBWhenOpenFails(t *testing.T) {
	tests := []struct {
		name string
		opts []ConfigOption
	}{
		{name: "with connector", opts: nil},
		{name: "without connector", opts: []ConfigOption{WithRetry(false, 0, 0), WithRLS(false)}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			env := NewConfig(WithEnvVars("writer"))
			var opened *gorm.DB
			opener := func(dialector gorm.Dialector, config *gorm.Config) (*gorm.DB, error) {
				db, err := DefaultGormOpener(dialector, config)
				opened = db
				return db, err
			}
			// The test database rejects a wrong password, so GORM's ping fails.
			cfg := NewConfig(append([]ConfigOption{
				WithEnvVars("writer"), WithCredentials(env.User, "wrong"), WithGormOpener(opener),
			}, tt.opts...)...)

			_, err := cfg.Connect()
			require.Error(t, err)

			// gorm.Open returns the *gorm.DB with its connection pool even when the ping fails.
			require.NotNil(t, opened)
			sqlDB, err := opened.DB()
			require.NoError(t, err)
			assert.EqualError(t, sqlDB.Ping(), "sql: database is closed")
		})
	}
}
