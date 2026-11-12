package conn

import (
	"context"
	"database/sql/driver"
	"errors"
	"testing"

	"github.com/jackc/pgx/v5/pgconn"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/hacomono-lib/go-pgkit/pgxretry"
)

// TestDiscardConnsOnSQLStates makes the only connection of the writer pool read-only, as a writer demoted to
// a reader by an Aurora failover would be, runs a write, and checks which connection the writer uses after it.
func TestDiscardConnsOnSQLStates(t *testing.T) {
	const writeSQL = "CREATE TEMP TABLE discard_test AS SELECT 1 AS id"

	// currentConn returns the PID of the server process that serves the writer's next query (PostgreSQL runs one
	// process per connection, so it tells the connections apart) and whether that connection is read-only.
	currentConn := func(t *testing.T, ctx context.Context, writer *WriterSession) (pid int, readOnly bool) {
		t.Helper()
		var setting string
		require.NoError(t, writer.DB().WithContext(ctx).Raw("SELECT pg_backend_pid()").Row().Scan(&pid))
		require.NoError(t, writer.DB().WithContext(ctx).Raw("SHOW default_transaction_read_only").Row().Scan(&setting))
		return pid, setting == "on"
	}
	writeWithExec := func(ctx context.Context, writer *WriterSession) error {
		return writer.DB().WithContext(ctx).Exec(writeSQL).Error
	}
	// The way most writes run in apps: TransactionManager runs the whole transaction again.
	writeInTransactionManager := func(ctx context.Context, writer *WriterSession) error {
		return NewTransactionManager(writer, WithRetryDelay(0)).Do(ctx, func(txCtx context.Context) error {
			return writer.GetDBOrTx(txCtx).Exec(writeSQL).Error
		})
	}
	// Fails with 22012 (division_by_zero), which is not in DiscardConnSQLStates.
	divideByZero := func(ctx context.Context, writer *WriterSession) error {
		return writer.DB().WithContext(ctx).Exec("SELECT 1/0").Error
	}
	// A write through database/sql QueryContext, as INSERT ... RETURNING is.
	writeWithQueryContext := func(ctx context.Context, writer *WriterSession) error {
		db, err := writer.DB().DB()
		require.NoError(t, err)
		rows, err := db.QueryContext(ctx, writeSQL)
		if err != nil {
			return err
		}
		return rows.Close()
	}
	// Create runs in a transaction it opens itself (SkipDefaultTransaction is not set). The insert is rejected, so
	// no row is left. A temporary table cannot be used: a read-only transaction can still write to it.
	writeWithCreate := func(ctx context.Context, writer *WriterSession) error {
		return writer.DB().WithContext(ctx).Table("tenants").Create(map[string]any{"id": "discard-conns-test", "name": "discard-conns-test"}).Error
	}

	tests := []struct {
		name string
		opts []ConfigOption
		// pgOptions is set to PGOPTIONS; "-c default_transaction_read_only=on" makes every new connection
		// read-only too, as when all of them still reach the old writer.
		pgOptions       string
		run             func(ctx context.Context, writer *WriterSession) error
		wantSQLState    string // "" means no error
		wantBadConn     bool
		wantRetryableTx bool
		wantNewConn     bool // whether the writer uses another connection after the write
		wantReadOnly    bool
	}{
		{
			name:         "without WithDiscardReadOnlyConns: fails and keeps the read-only connection",
			run:          writeWithExec,
			wantSQLState: pgxretry.SQLStateReadOnlySQLTransaction,
			wantReadOnly: true,
		},
		{
			name:        "WithDiscardReadOnlyConns: runs the write again on a new writable connection",
			opts:        []ConfigOption{WithDiscardReadOnlyConns()},
			run:         writeWithExec,
			wantNewConn: true,
		},
		{
			name:        "WithDiscardReadOnlyConns through QueryContext: runs the write again on a new writable connection",
			opts:        []ConfigOption{WithDiscardReadOnlyConns()},
			run:         writeWithQueryContext,
			wantNewConn: true,
		},
		{
			name:         "WithDiscardReadOnlyConns and another error: fails and keeps the connection",
			opts:         []ConfigOption{WithDiscardReadOnlyConns()},
			run:          divideByZero,
			wantSQLState: "22012",
			wantReadOnly: true,
		},
		{
			name:         "WithDiscardConnSQLStates with another SQLSTATE: discards the connection that returned it",
			opts:         []ConfigOption{WithDiscardConnSQLStates("22012")},
			run:          divideByZero,
			wantSQLState: "22012",
			// Division by zero fails on every connection, so database/sql gives up.
			wantBadConn: true,
			wantNewConn: true,
		},
		{
			name:         "WithDiscardReadOnlyConns with retry and RLS disabled: fails without running it again, but discards the connection",
			opts:         []ConfigOption{WithRetry(false, 0, 0), WithRLS(false), WithDiscardReadOnlyConns()},
			run:          writeWithExec,
			wantSQLState: pgxretry.SQLStateReadOnlySQLTransaction,
			wantNewConn:  true,
		},
		{
			name:            "WithDiscardReadOnlyConns with Create: returns ErrRetryableInTx and discards the connection, since Create runs in its own transaction",
			opts:            []ConfigOption{WithDiscardReadOnlyConns()},
			run:             writeWithCreate,
			wantSQLState:    pgxretry.SQLStateReadOnlySQLTransaction,
			wantRetryableTx: true,
			wantNewConn:     true,
		},
		{
			name:        "WithDiscardReadOnlyConns in TransactionManager: runs the transaction again on a new writable connection",
			opts:        []ConfigOption{WithDiscardReadOnlyConns()},
			run:         writeInTransactionManager,
			wantNewConn: true,
		},
		{
			name:         "WithDiscardReadOnlyConns and every connection is read-only: returns the read-only error after database/sql gives up",
			opts:         []ConfigOption{WithDiscardReadOnlyConns()},
			pgOptions:    "-c default_transaction_read_only=on",
			run:          writeWithExec,
			wantSQLState: pgxretry.SQLStateReadOnlySQLTransaction,
			wantBadConn:  true,
			wantNewConn:  true,
			wantReadOnly: true,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			ctx := context.Background()
			t.Setenv("PGOPTIONS", tt.pgOptions)
			// One connection, so that the queries after the write run on the same connection unless it was discarded.
			opts := append([]ConfigOption{WithEnvVars("writer"), WithConnectionPool(1, 1, 0)}, tt.opts...)
			sm, err := NewSessionManager(NewConfig(opts...), nil)
			require.NoError(t, err)
			defer func() { _ = sm.Close() }()
			writer := sm.Writer()
			require.NoError(t, writer.DB().WithContext(ctx).Exec("SET default_transaction_read_only = on").Error)
			oldPID, _ := currentConn(t, ctx, writer)

			err = tt.run(ctx, writer)

			// Left empty when err is not a PostgreSQL error.
			pgErr := &pgconn.PgError{}
			errors.As(err, &pgErr)
			assert.Equal(t, tt.wantSQLState, pgErr.Code, "err: %v", err)
			assert.Equal(t, tt.wantBadConn, errors.Is(err, driver.ErrBadConn), "err: %v", err)
			assert.Equal(t, tt.wantRetryableTx, errors.Is(err, pgxretry.ErrRetryableInTx), "err: %v", err)
			pid, readOnly := currentConn(t, ctx, writer)
			assert.Equal(t, tt.wantNewConn, pid != oldPID, "whether the writer uses another connection")
			assert.Equal(t, tt.wantReadOnly, readOnly, "whether the writer's connection is read-only")
		})
	}
}
