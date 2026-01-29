# rlsconn

PostgreSQL Row-Level Security connector wrapper for `database/sql`.

Part of [go-pgkit](../README.md).

## Overview

The `rlsconn` package wraps `database/sql/driver.Connector` and `driver.Conn` to set PostgreSQL session variables on every connection acquired from the pool. This enables Row-Level Security policies that reference `current_setting()` to work correctly with connection pooling.

## How It Works

```
database/sql pool
    │
    ├── New connection: RLSConnector.Connect(ctx)
    │     ├── baseConnector.Connect(ctx)  → creates base connection
    │     ├── Wrap with RLSSessionResetter
    │     └── RLSSessionResetter.ResetSession(ctx)  → sets session vars
    │
    └── Reused connection: RLSSessionResetter.ResetSession(ctx)
          └── database/sql calls this via SessionResetter interface
```

In both cases, session variables are set before any application query executes.

### Generated SQL

All session variables are set in a single round-trip using parameterized `set_config()` calls:

```sql
SELECT set_config($1, $2, false), set_config($3, $4, false)
```

Values absent from context are reset to NULL via `set_config(name, NULL, false)`. `is_local=false` makes settings session-scoped. See [docs/rls-guide.md](../docs/rls-guide.md#how-it-works-internally) for the full flow.

## Key Types

### Config

```go
type Config struct {
    SessionVars []SessionVar
    Logger      Logger                                          // Default: NoopLogger
    OnExecQuery func(ctx context.Context, query string, args []any)  // Debug hook
}
```

### SessionVar

```go
type SessionVar struct {
    Name        string                                          // e.g., "app.current_tenant_id"
    ValueGetter func(context.Context) (string, bool)            // (value, exists)
}
```

- `Name` must be in `namespace.name` format (e.g., `app.current_tenant_id`)
- `ValueGetter` returns `(value, true)` to SET or `("", false)` to RESET to NULL

### RLSConnector

Wraps `driver.Connector`. Creates connections with `RLSSessionResetter` applied.

```go
// Panics on invalid config
connector := rlsconn.NewRLSConnector(baseConnector, config)

// Returns error on invalid config
connector, err := rlsconn.NewRLSConnectorWithValidation(baseConnector, config)
```

### RLSSessionResetter

Wraps `driver.Conn`. Implements `driver.SessionResetter` to set session variables when the connection is acquired.

```go
resetter := rlsconn.NewRLSSessionResetter(baseConn, config)
```

Delegates all `driver.Conn` methods (Query, Exec, Prepare, Begin, Close) to the base connection. Also implements `driver.Validator`, `driver.QueryerContext`, `driver.ExecerContext`, `driver.ConnPrepareContext`, and `driver.ConnBeginTx` when the base connection supports them.

## Security

### Name Validation

Session variable names are validated against the regex:

```
^[a-zA-Z_][a-zA-Z0-9_]*\.[a-zA-Z_][a-zA-Z0-9_]*$
```

This ensures names are in `namespace.name` format with only safe characters. Validation happens at connector creation time — invalid names cause a panic with `NewRLSConnector` or an error with `NewRLSConnectorWithValidation`.

### Parameterized Queries

Values are always passed as positional parameters (`$1`, `$2`, ...) to `set_config()`, never interpolated into SQL strings.

### Fail-Closed

If the underlying driver does not implement `ExecerContext` or `Execer`, `ResetSession` returns an error rather than silently skipping session variable setup. This prevents queries from executing without tenant isolation.

## Standalone Usage

`rlsconn` depends only on `database/sql/driver` interfaces and can be used independently of the `conn` package:

```go
import (
    "database/sql"
    "github.com/hacomono-lib/go-pgkit/rlsconn"
    "github.com/jackc/pgx/v5/stdlib"
)

// Create base connector
baseConnector, err := stdlib.GetDefaultDriver().OpenConnector("postgres://...")

// Wrap with RLS
config := &rlsconn.Config{
    SessionVars: []rlsconn.SessionVar{
        {
            Name: "app.current_tenant_id",
            ValueGetter: func(ctx context.Context) (string, bool) {
                // your context extraction logic
            },
        },
    },
}
if err := config.Validate(); err != nil {
    log.Fatal(err)
}

rlsConnector := rlsconn.NewRLSConnector(baseConnector, config)

// Use with database/sql
db := sql.OpenDB(rlsConnector)
```

## Integration with pgxretry

When used through go-pgkit's `conn` package, `rlsconn` is composed with `pgxretry` via the `ConnectorWrapper` interface:

```go
// This happens internally in conn.Session.connect()
retryConfig.WithConnectorWrapper(func(baseConnector driver.Connector) driver.Connector {
    return rlsconn.NewRLSConnector(baseConnector, rlsConfig)
})
```

The wrapper chain is: `pgxretry.Connector` → `RLSConnector` → `pgx/stdlib.Connector`

See [docs/architecture.md](../docs/architecture.md) for the full driver layering.
