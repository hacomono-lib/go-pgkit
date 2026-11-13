package conn

import (
	"context"
	"database/sql/driver"
	"errors"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgconn"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/hacomono-lib/go-pgkit/pgxretry"
)

// TestCheckReadOnlyConns makes the only connection of the writer pool read-only, as a writer demoted to a reader by
// an Aurora failover would be, runs a write, and checks which connection the write ran on.
func TestCheckReadOnlyConns(t *testing.T) {
	const writeSQL = "CREATE TEMP TABLE check_read_only_test AS SELECT pg_backend_pid() AS pid"

	tests := []struct {
		name string
		opts []ConfigOption
		// inTransactionManager runs the write in TransactionManager.Do, without retries.
		inTransactionManager bool
		wantSQLState         string // "" means no error
		wantNewConn          bool   // whether the write ran on another connection
	}{
		{
			name:         "without WithCheckReadOnlyConns: the write runs on the read-only connection and fails",
			wantSQLState: pgxretry.SQLStateReadOnlySQLTransaction,
		},
		{
			name:        "WithCheckReadOnlyConns: the read-only connection is closed before the write takes it",
			opts:        []ConfigOption{WithCheckReadOnlyConns(0)},
			wantNewConn: true,
		},
		{
			name:        "WithCheckReadOnlyConns with retry and RLS disabled: the read-only connection is closed before the write takes it",
			opts:        []ConfigOption{WithRetry(false, 0, 0), WithRLS(false), WithCheckReadOnlyConns(0)},
			wantNewConn: true,
		},
		{
			name:                 "WithCheckReadOnlyConns in TransactionManager: the read-only connection is closed before BEGIN",
			opts:                 []ConfigOption{WithCheckReadOnlyConns(0)},
			inTransactionManager: true,
			wantNewConn:          true,
		},
		{
			name:         "WithCheckReadOnlyConns with an interval not yet passed: the connection is reused without checking",
			opts:         []ConfigOption{WithCheckReadOnlyConns(time.Hour)},
			wantSQLState: pgxretry.SQLStateReadOnlySQLTransaction,
		},
		{
			name:        "WithCheckReadOnlyConns with an interval not yet passed and WithDiscardReadOnlyConns: the write runs again on a new connection",
			opts:        []ConfigOption{WithCheckReadOnlyConns(time.Hour), WithDiscardReadOnlyConns()},
			wantNewConn: true,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			ctx := context.Background()
			// One connection, so that the write runs on the read-only connection unless it was closed.
			opts := append([]ConfigOption{WithEnvVars("writer"), WithConnectionPool(1, 1, 0)}, tt.opts...)
			sm, err := NewSessionManager(NewConfig(opts...), nil)
			require.NoError(t, err)
			defer func() { _ = sm.Close() }()
			writer := sm.Writer()
			var oldPID int
			require.NoError(t, writer.DB().WithContext(ctx).Raw("SELECT pg_backend_pid()").Row().Scan(&oldPID))
			require.NoError(t, writer.DB().WithContext(ctx).Exec("SET default_transaction_read_only = on").Error)

			if tt.inTransactionManager {
				err = NewTransactionManager(writer, WithRetryDelay(0), WithMaxRetries(0)).Do(ctx, func(txCtx context.Context) error {
					return writer.GetDBOrTx(txCtx).Exec(writeSQL).Error
				})
			} else {
				err = writer.DB().WithContext(ctx).Exec(writeSQL).Error
			}

			pgErr := &pgconn.PgError{}
			errors.As(err, &pgErr)
			require.Equal(t, tt.wantSQLState, pgErr.Code, "err: %v", err)
			if tt.wantSQLState != "" {
				return
			}
			// The temporary table exists only on the connection that ran the write.
			var pid int
			require.NoError(t, writer.DB().WithContext(ctx).Raw("SELECT pid FROM check_read_only_test").Row().Scan(&pid))
			assert.Equal(t, tt.wantNewConn, pid != oldPID, "whether the write ran on another connection")
		})
	}
}

// TestCheckReadOnlyConnsWhenEveryConnIsReadOnly makes every new connection read-only, as when all of them still
// reach the old writer, and checks that the session cannot be opened.
func TestCheckReadOnlyConnsWhenEveryConnIsReadOnly(t *testing.T) {
	t.Setenv("PGOPTIONS", "-c default_transaction_read_only=on")

	sm, err := NewSessionManager(NewConfig(WithEnvVars("writer"), WithCheckReadOnlyConns(0)), nil)
	if sm != nil {
		_ = sm.Close()
	}

	require.Error(t, err)
	assert.ErrorIs(t, err, pgxretry.ErrReadOnlyConn)
	assert.ErrorIs(t, err, driver.ErrBadConn)
}
