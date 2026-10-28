package conn

import (
	"context"
	"sync/atomic"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

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

// BeforeConnect changes the config of the connections of the session that sets it, and only of them,
// whichever session connects first, with or without pgxretry.
func TestSessionConnector_BeforeConnect(t *testing.T) {
	tests := []struct {
		name string
		opts []ConfigOption
	}{
		{name: "retry enabled", opts: nil},
		{name: "retry disabled", opts: []ConfigOption{WithRetry(false, 0, 0)}},
		{name: "retry and RLS disabled", opts: []ConfigOption{WithRetry(false, 0, 0), WithRLS(false)}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			env := NewConfig(WithEnvVars("writer"))
			var calls atomic.Int64
			hook := func(ctx context.Context, cc *pgx.ConnConfig) error {
				calls.Add(1)
				cc.Password = env.Password
				return nil
			}
			// Connect opens a connection (GORM pings on open).
			connect := func(opts ...ConfigOption) error {
				s, err := NewConfig(append(append([]ConfigOption{WithEnvVars("writer")}, tt.opts...), opts...)...).Connect()
				if err == nil {
					t.Cleanup(func() { _ = s.Close() })
				}
				return err
			}

			// The test database rejects a wrong password.
			require.Error(t, connect(WithCredentials(env.User, "wrong")))

			// A session without the hook connects first.
			require.NoError(t, connect())

			// The session connects only if the hook's password replaces the wrong one.
			require.NoError(t, connect(WithCredentials(env.User, "wrong"), WithBeforeConnect(hook)))
			calledWithHook := calls.Load()
			assert.Equal(t, int64(1), calledWithHook)

			// The hook is not called for a session without it.
			require.NoError(t, connect())
			assert.Equal(t, calledWithHook, calls.Load())
		})
	}
}
