# go-pgkit

A PostgreSQL toolkit for Go providing session management, automatic query retry, and Row-Level Security (RLS) support. Built on [GORM](https://gorm.io/) and [sqlx](https://github.com/jmoiron/sqlx).

## Why go-pgkit

go-pgkit integrates three production-critical features into a single toolkit:

- **R/W separation** — Type-safe `WriterSession` / `ReaderSession`. `ReaderSession` does not expose `Transaction()`
- **Row-Level Security** — Automatic `set_config()` on every connection via `database/sql`'s `SessionResetter` interface. Works transparently with both GORM and sqlx
- **Cached plan error recovery** — Detects `cached plan must not change result type`, resets the connection, and retries. Enables zero-downtime online DDL migrations

These features work through the `database/sql` driver layer and are transparent to application code.

## Usage

### 1. Setup

```go
writerCfg := conn.NewConfig(conn.WithEnvVars("writer"))
readerCfg := conn.NewConfig(conn.WithEnvVars("reader"))

sm, err := conn.NewSessionManager(writerCfg, readerCfg)
if err != nil {
    log.Fatal(err)
}
defer sm.Close()
```

### 2. Writer and Reader

`sm.Writer()` returns a `*WriterSession` for write operations, `sm.Reader()` returns a `*ReaderSession` for read-only queries. `ReaderSession` does not expose `Transaction()`.

```go
// Reads — use Reader
sm.Reader().GetDBOrTx(ctx).Find(&products)

// Writes — use Writer
sm.Writer().GetDBOrTx(ctx).Create(&product)
```

### 3. GetDBOrTx

`GetDBOrTx(ctx)` returns `*gorm.DB` with the request context applied. **Always use this instead of `DB()`** — it propagates RLS tenant/user IDs to the driver and returns the active transaction from context if one exists.

```go
// Correct — context is propagated, RLS and retry work
sm.Writer().GetDBOrTx(ctx).Create(&product)

// Wrong — context is not propagated, RLS session variables will not be set
sm.Writer().DB().Create(&product)
```

### 4. Transactions

```go
err := sm.Writer().Transaction(ctx, func(txCtx context.Context) error {
    if err := sm.Writer().GetDBOrTx(txCtx).Create(&order).Error; err != nil {
        return err // rollback
    }
    return nil // commit
})
```

### 5. QueryDB (sqlx)

```go
rows, err := sm.Reader().QueryDB().NamedQueryContext(ctx,
    "SELECT id, name FROM products WHERE tenant_id = :tenant_id",
    map[string]any{"tenant_id": tenantID},
)
```

See [conn/README.md](./conn) for the full API reference including all QueryDB methods, connection pool management, and connection reset.

## Features

- **Session management** with Writer/Reader separation built on GORM and sqlx
- **Automatic query retry** on transient PostgreSQL errors (cached plan, connection reset, deadlock)
- **Row-Level Security** with per-connection session variable injection via `set_config()`
- **Connection pooling** with graceful reset and cooldown protection
- **Transaction support** with context propagation and automatic rollback on panic
- **Named query support** via sqlx (`NamedQueryContext`, `NamedExecContext`)
- **Environment variable** configuration with role-based overrides
- **Structured logging** with slog

## Dependencies

The `conn` package requires **GORM** (`gorm.io/gorm`), **sqlx** (`github.com/jmoiron/sqlx`), and **go-errorsx** (`github.com/hacomono-lib/go-errorsx`) as core dependencies. `Session.DB()` returns `*gorm.DB`, and `Session.QueryDB()` provides sqlx-based named query support. Error types (`ErrConnection`, `ErrTransaction`, `ErrInitialization`) are built on go-errorsx.

The lower-level packages (`pgxretry`, `rlsconn`, `rlsctx`) depend only on `database/sql/driver` interfaces and can be used independently without GORM.

## Installation

```bash
go get github.com/hacomono-lib/go-pgkit
```

## Quick Start

```go
package main

import (
    "context"
    "log"

    "github.com/google/uuid"
    "github.com/hacomono-lib/go-pgkit/conn"
    "github.com/hacomono-lib/go-pgkit/rlsctx"
)

func main() {
    // Create config from environment variables
    cfg := conn.NewConfig(
        conn.WithEnvVars("writer"),
        conn.WithConnectionPool(25, 5, 60),
    )

    // Connect
    session, err := cfg.Connect()
    if err != nil {
        log.Fatal(err)
    }
    defer session.Close()

    // Set RLS context (typically done in HTTP middleware)
    ctx := rlsctx.WithTenantID(context.Background(), uuid.MustParse("550e8400-e29b-41d4-a716-446655440000"))
    ctx = rlsctx.WithUserID(ctx, uuid.MustParse("7c9e6679-7425-40de-944b-e07fc1f90ae7"))

    // Query — RLS session variables are set automatically on the connection
    var count int64
    err = session.GetDBOrTx(ctx).Table("products").Count(&count).Error
    if err != nil {
        log.Fatal(err)
    }
    log.Printf("Found %d products for this tenant", count)
}
```

## Package Overview

| Package | Description |
|---------|-------------|
| [`conn`](./conn) | Session management, Writer/Reader separation, transactions, QueryDB |
| [`pgxretry`](./pgxretry) | PostgreSQL driver wrapper with automatic retry on transient errors |
| [`rlsconn`](./rlsconn) | `database/sql` Connector/SessionResetter wrapper for RLS session variables |
| [`rlsctx`](#rlsctx) | Context helpers for tenant ID and user ID (see below) |

## Architecture

```
Application
    │
    ├── conn.SessionManager
    │     ├── WriterSession ─── Session ─── GORM DB + QueryDB (sqlx)
    │     └── ReaderSession ─── Session ─── GORM DB + QueryDB (sqlx)
    │
    ▼
database/sql
    │
    ├── pgxretry (driver wrapper)     ← automatic retry
    │     └── retryableConn
    │
    ├── rlsconn (connector wrapper)   ← RLS session variables
    │     ├── RLSConnector            ← wraps driver.Connector
    │     └── RLSSessionResetter      ← wraps driver.Conn
    │
    ▼
pgx/v5/stdlib ── pgx ── PostgreSQL
```

See [docs/architecture.md](./docs/architecture.md) for detailed driver layering and connection lifecycle.

## rlsctx

The `rlsctx` package provides context helpers for storing and retrieving RLS session variable values. It has only four functions:

```go
import (
    "github.com/google/uuid"
    "github.com/hacomono-lib/go-pgkit/rlsctx"
)

// Store tenant/user IDs in context (typically in HTTP middleware)
ctx = rlsctx.WithTenantID(ctx, uuid.MustParse("..."))
ctx = rlsctx.WithUserID(ctx, uuid.MustParse("..."))

// Retrieve from context (used internally by rlsconn)
tenantID, ok := rlsctx.GetTenantIDFromContext(ctx) // (string, bool)
userID, ok := rlsctx.GetUserIDFromContext(ctx)     // (string, bool)
```

Values are stored as UUID strings. When the value is present, `(value, true)` is returned. When absent, `("", false)` is returned, causing `rlsconn` to RESET the corresponding PostgreSQL session variable to NULL.

## Configuration

`conn.NewConfig` accepts functional options and environment variables (`WithEnvVars`). All `DB_*` environment variables support role-based overrides (e.g., `DB_WRITER_HOST` takes precedence over `DB_HOST`).

See [docs/configuration.md](./docs/configuration.md) for the full options reference, environment variables, pool settings, retry configuration, defaults, and caveats.

## Documentation

- [Architecture](./docs/architecture.md) — Driver layering, connection lifecycle, session model, reset flow
- [Configuration](./docs/configuration.md) — Full options reference, environment variables, defaults
- [RLS Guide](./docs/rls-guide.md) — End-to-end Row-Level Security setup (PostgreSQL + Go)
- [Testing](./docs/testing.md) — TestSession, Docker setup, RLS testing patterns
- [conn package](./conn) — Session, SessionManager, Transaction, QueryDB usage
- [pgxretry package](./pgxretry) — Retry driver configuration and strategies
- [rlsconn package](./rlsconn) — RLS connector and session resetter

## Running Tests

```bash
# Start PostgreSQL
make up

# Run all tests
make test

# Stop and remove volumes
make down
```

`make test` runs tests inside a Docker container with `go test -count=1 -race -v ./...`.

## License

MIT
