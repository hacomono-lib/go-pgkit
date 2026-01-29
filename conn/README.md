# conn

Session management for PostgreSQL with GORM integration, Writer/Reader separation, transactions, and connection pooling.

Part of [go-pgkit](../README.md).

## Overview

The `conn` package provides the main application-facing API for database access. It depends on **GORM** (`gorm.io/gorm`) and **sqlx** (`github.com/jmoiron/sqlx`) — `Session.DB()` returns `*gorm.DB` and `Session.QueryDB()` provides sqlx-based named queries. It also integrates pgxretry for automatic retry and rlsconn for Row-Level Security.

## Creating a Session

```go
cfg := conn.NewConfig(
    conn.WithHost("localhost"),
    conn.WithPort(5432),
    conn.WithCredentials("app_user", "app_password"),
    conn.WithDatabase("mydb"),
    conn.WithConnectionPool(25, 5, 60),
)

session, err := cfg.Connect()
if err != nil {
    log.Fatal(err)
}
defer session.Close()
```

Or from environment variables:

```go
cfg := conn.NewConfig(conn.WithEnvVars("writer"))

session, err := cfg.Connect()
```

See [docs/configuration.md](../docs/configuration.md) for the full options reference.

## SessionManager

`SessionManager` manages separate writer and reader sessions:

```go
writerCfg := conn.NewConfig(conn.WithEnvVars("writer"))
readerCfg := conn.NewConfig(conn.WithEnvVars("reader"))

sm, err := conn.NewSessionManager(writerCfg, readerCfg)
if err != nil {
    log.Fatal(err)
}
defer sm.Close()

// Use writer for mutations
sm.Writer().DB()         // *gorm.DB
sm.Writer().QueryDB()    // *QueryDB

// Use reader for queries
sm.Reader().DB()         // *gorm.DB
sm.Reader().QueryDB()    // *QueryDB
```

- `WriterSession` exposes all `Session` methods including `Transaction()`
- `ReaderSession` exposes a read-safe subset: `DB()`, `QueryDB()`, `GetDBOrTx()`, `ResetConnection()`, `GetPoolStats()`, `Close()`

## Transactions

Use `WriterSession.Transaction` to execute code within a database transaction:

```go
err := sm.Writer().Transaction(ctx, func(txCtx context.Context) error {
    // All operations using txCtx share the same transaction
    if err := sm.Writer().GetDBOrTx(txCtx).Create(&order).Error; err != nil {
        return err  // triggers rollback
    }
    if err := sm.Writer().GetDBOrTx(txCtx).Create(&orderItem).Error; err != nil {
        return err  // triggers rollback
    }
    return nil  // triggers commit
})
```

- Returning `nil` commits the transaction
- Returning an error rolls back and propagates the error
- Panics are caught, the transaction is rolled back, and the panic is re-raised
- `GetDBOrTx(ctx)` returns the transaction if one exists in the context, otherwise the base DB

### TransactionManager

`TransactionManager` wraps `WriterSession.Transaction` with automatic retry for `pgxretry.ErrRetryableInTx` errors. It is the application-level counterpart to pgxretry's driver-level retry: pgxretry retries individual queries outside transactions, while TransactionManager retries entire transactions.

```go
tm := conn.NewTransactionManager(sm.Writer())

err := tm.Do(ctx, func(txCtx context.Context) error {
    if err := sm.Writer().GetDBOrTx(txCtx).Create(&order).Error; err != nil {
        return err
    }
    if err := sm.Writer().GetDBOrTx(txCtx).Create(&orderItem).Error; err != nil {
        return err
    }
    return nil
})
```

Customize retry behavior with functional options:

```go
tm := conn.NewTransactionManager(sm.Writer(),
    conn.WithMaxRetries(5),
    conn.WithRetryDelay(500 * time.Millisecond),
    conn.WithLogger(customLogger),
)
```

- Retries only on `pgxretry.ErrRetryableInTx` (connection errors that occurred inside a transaction)
- Calls `ResetConnection` before each retry to recover the underlying connection
- Non-retryable errors are returned immediately without retry
- Respects context cancellation between retries
- Defaults: 3 retries, 1 second delay

## Raw SQL with QueryDB

`QueryDB` wraps sqlx for named parameter queries:

```go
qdb := session.QueryDB()

// Named query (struct or map args)
rows, err := qdb.NamedQueryContext(ctx, "SELECT * FROM products WHERE tenant_id = :tenant_id", map[string]any{
    "tenant_id": tenantID,
})
defer rows.Close()

// Named exec
result, err := qdb.NamedExecContext(ctx, "UPDATE products SET price = :price WHERE id = :id", product)

// Single row
row, err := qdb.NamedQueryRowContext(ctx, "SELECT COUNT(*) FROM products WHERE tenant_id = :tenant_id", map[string]any{
    "tenant_id": tenantID,
})

// Positional query
row := qdb.QueryRowContext(ctx, "SELECT COUNT(*) FROM products WHERE tenant_id = $1", tenantID)
```

## Connection Pool Management

### Pool Statistics

```go
stats := session.GetPoolStats()
// Returns: max_open_connections, open_connections, in_use, idle,
//          wait_count, wait_duration, max_idle_closed, max_idle_time_closed, max_lifetime_closed
```

### Connection Reset

`ResetConnection` replaces the current connection with a new one, useful for recovering from persistent connection issues:

```go
err := session.ResetConnection(ctx)
```

- Creates a new connection and pings it before swapping
- Closes the old connection gracefully in a background goroutine
- Has a 1-second cooldown to prevent rapid successive resets
- Thread-safe with minimal lock duration

See [docs/architecture.md](../docs/architecture.md) for the full reset flow.

## Caveats

- **Driver registration is process-global**: The first `Config.Connect()` call registers the pgxretry driver with its retry and RLS settings. All subsequent connections in the process use the same driver configuration. If RLS is enabled, the first connection must have RLS configured.
- **`sslmode` defaults to `disable`**. Override with `WithSSLMode` or `DB_SSLMODE`. See [docs/configuration.md — Caveats](../docs/configuration.md#caveats).
- **GORM prepared statements are disabled** to ensure per-request context propagation for RLS session variables.

## Further Reading

- [Configuration](../docs/configuration.md) — All config options, environment variables, defaults
- [RLS Guide](../docs/rls-guide.md) — Row-Level Security setup and integration
- [Testing](../docs/testing.md) — TestSession, TestRLSSession, Docker setup
- [Architecture](../docs/architecture.md) — Driver layering, connection lifecycle
