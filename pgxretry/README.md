# pgxretry

A PostgreSQL driver wrapper for Go that automatically retries queries on transient errors, particularly useful for handling online DDL operations and cached plan errors.

Part of [go-pgkit](../README.md).

## Features

- **Automatic retry** on PostgreSQL cached plan errors during online DDL
- **Configurable retry strategies** (exponential backoff, fixed delay, custom)
- **Pluggable error detection** for custom retry logic
- **Connection pool integration** with session reset support
- **Transaction-aware** retry behavior - safe ACID compliance
- **Structured logging** support (slog, standard log, custom)
- **Zero configuration** with sensible defaults
- **Production-tested** retry logic

## Limitations

Since this driver uses `pgx/v5/stdlib` to provide `database/sql` compatibility, it inherits the following limitations:

- **Performance overhead**: The `database/sql` abstraction layer is slower compared to using pgx directly
- **Limited PostgreSQL-specific features**: Several advanced PostgreSQL features available in native pgx are not accessible through the `database/sql` interface:
  - COPY protocol for bulk data operations
  - LISTEN/NOTIFY for real-time notifications
  - Batch query execution
  - Single-round trip query mode
  - Binary format for custom types
  - Connection pool after-connect hooks
- **Positional parameters only**: pgx uses PostgreSQL positional parameters ($1, $2), named parameters are not supported

Note: It is possible to access the underlying `pgx.Conn` for specific operations when needed, but the retry mechanism only works with standard `database/sql` operations.

If your application requires these PostgreSQL-specific features or maximum performance, consider using pgx directly with custom retry logic instead of this driver.

For more details on pgx and stdlib differences, see:
- [pgx documentation](https://github.com/jackc/pgx)
- [pgx/v5/stdlib documentation](https://pkg.go.dev/github.com/jackc/pgx/v5/stdlib)

## Installation

```bash
go get github.com/hacomono-lib/go-pgkit/pgxretry
```

## Quick Start

```go
package main

import (
    "database/sql"
    "log"

    "github.com/hacomono-lib/go-pgkit/pgxretry"
)

func main() {
    // Register the driver with default configuration
    pgxretry.Register(nil)

    // Use like any other database/sql driver
    db, err := sql.Open("pgxretry", "postgres://user:password@localhost/mydb")
    if err != nil {
        log.Fatal(err)
    }
    defer db.Close()

    // Use the database normally - retries happen automatically
    var count int
    err = db.QueryRow("SELECT COUNT(*) FROM users").Scan(&count)
    if err != nil {
        log.Fatal(err)
    }

    log.Printf("Found %d users", count)
}
```

## Configuration

### Basic Configuration

```go
import (
    "time"
    "github.com/hacomono-lib/go-pgkit/pgxretry"
)

config := &pgxretry.Config{
    MaxRetries:     5,
    InitialBackoff: 100 * time.Millisecond,
    MaxBackoff:     2 * time.Second,
    BackoffFactor:  2.0,
}

pgxretry.Register(config)
```

### Default Values

| Field | Default |
|-------|---------|
| `MaxRetries` | `3` |
| `InitialBackoff` | `100ms` |
| `MaxBackoff` | `5s` |
| `BackoffFactor` | `2.0` |
| `Logger` | `NoopLogger` |

### With Structured Logging (slog)

```go
import (
    "log/slog"
    "os"
)

logger := slog.New(slog.NewTextHandler(os.Stdout, nil))

config := pgxretry.DefaultConfig().
    WithSlog(logger)

pgxretry.Register(config)
```

### Custom Error Detection

```go
import "strings"

type CustomErrorDetector struct {
    *pgxretry.DefaultErrorDetector
}

func (d *CustomErrorDetector) IsRetryableError(err error) bool {
    if err == nil {
        return false
    }

    // Add application-specific retry logic
    errMsg := err.Error()
    if strings.Contains(errMsg, "temporary_table_locked") ||
       strings.Contains(errMsg, "could not obtain lock") {
        return true
    }

    // Fall back to default detection
    return d.DefaultErrorDetector.IsRetryableError(err)
}

config := pgxretry.DefaultConfig().
    WithErrorDetector(&CustomErrorDetector{
        DefaultErrorDetector: pgxretry.NewDefaultErrorDetector(),
    })

pgxretry.Register(config)
```

### Custom Retry Strategy

```go
// Fixed backoff with 5 retries, 200ms delay
strategy := pgxretry.NewFixedBackoff(5, 200*time.Millisecond)

config := pgxretry.DefaultConfig().
    WithRetryStrategy(strategy)

pgxretry.Register(config)

// Or exponential backoff with custom settings
expStrategy := pgxretry.NewExponentialBackoff(
    5,                    // max attempts
    50*time.Millisecond,  // initial backoff
    3*time.Second,        // max backoff
    1.5,                  // backoff factor
)
config.WithRetryStrategy(expStrategy)
```

### Connection Pool Integration

When using with a connection pool that supports connection reset:

```go
config := pgxretry.DefaultConfig().
    WithConnectionReset(func(ctx context.Context) error {
        // Reset your connection pool here
        // This is called when cached plan errors are detected
        return myConnectionPool.Reset(ctx)
    })

pgxretry.Register(config)
```

## Error Types

The driver handles the following PostgreSQL errors automatically:

- **Cached plan errors** (SQLSTATE 0A000): "cached plan must not change result type"
- **Connection errors** (SQLSTATE 08xxx): connection failures, resets
- **Prepared statement errors** (SQLSTATE 26000): statement does not exist
- **Admin shutdown** (SQLSTATE 57P01): server shutdown
- **Serialization failures** (SQLSTATE 40001) and **deadlocks** (SQLSTATE 40P01)
- **Transaction failures**: properly wrapped with `ErrRetryableInTx`

## Transaction Handling

**Important**: pgxretry does **not** perform automatic retries within transactions to maintain ACID properties. When retryable errors occur within transactions, **you must handle them at the application level** by retrying the entire transaction.

Retryable errors within transactions are wrapped with `ErrRetryableInTx` to indicate that the entire transaction should be retried by your application:

```go
import (
    "context"
    "errors"
    "github.com/hacomono-lib/go-pgkit/pgxretry"
)

tx, err := db.BeginTx(ctx, nil)
if err != nil {
    log.Fatal(err)
}

// Operations within transaction won't be retried
// but retryable errors will be properly identified
_, err = tx.ExecContext(ctx, "UPDATE users SET active = true")
if err != nil {
    tx.Rollback()

    // Check if it was a retryable error that occurred in transaction
    if errors.Is(err, pgxretry.ErrRetryableInTx) {
        log.Println("Retryable error occurred in transaction - you must retry the entire transaction at application level")
        // TODO: Implement application-level transaction retry logic here
    }
    return
}

err = tx.Commit()
```

## Advanced Usage

### Disable Retries for Testing

```go
config := pgxretry.DefaultConfig().
    WithRetryStrategy(pgxretry.NewNoRetry())

pgxretry.Register(config)
```

## How It Works

1. **Wraps pgx driver**: Uses the battle-tested pgx PostgreSQL driver underneath
2. **Intercepts operations**: Monitors query execution for retryable errors
3. **Applies retry logic**: Uses configured strategy to retry failed operations
4. **Transaction awareness**: Disables retry within transactions to maintain ACID properties — application must handle transaction retries
5. **Connection pool friendly**: Optional session reset for cached plan recovery
6. **Structured logging**: Logs retries at WARN level, successes after retry at INFO level

## Important Notes

- **Register once**: Call `pgxretry.Register()` once at application startup. The driver is registered globally via `database/sql.Register` and subsequent calls are ignored.
- **Transaction retries are your responsibility**: pgxretry does not retry within transactions. Check for `ErrRetryableInTx` and retry the entire transaction at the application level.
- When using pgxretry through the `conn` package, retry configuration is managed by `conn.Config`. See [docs/configuration.md](../docs/configuration.md) for details.
