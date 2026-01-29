# Architecture

This document describes the internal architecture of go-pgkit: package dependencies, driver layering, connection lifecycle, session model, and connection reset flow.

## Package Dependency Graph (Go imports)

```
rlsctx          pgxretry          rlsconn
(no internal    (no internal      (no internal
 dependencies)   dependencies)     dependencies)
   │                │                 │
   └────────────────┼─────────────────┘
                    │
                    ▼
                  conn          ← imports all three
                    │
                    ▼
               Application
```

- **rlsctx**, **pgxretry**, **rlsconn** are all independent packages with no cross-imports. Each can be used standalone.
- **conn** is the composition layer that imports all three and wires them together.
- At runtime, `conn` injects `rlsconn` into `pgxretry` via the `ConnectorWrapper` function type, and passes `rlsctx` getter functions to `rlsconn` via `SessionVar.ValueGetter`. These are plain function values — no import dependency exists between the lower-level packages.

## Driver Layering

go-pgkit composes multiple driver wrappers in a layered architecture:

```
Application (GORM / sqlx)
        │
        ▼
  database/sql
        │
        ├── driver.Connector ─────────────┐
        │                                  │
        │   pgxretry.Connector             │
        │       │                          │
        │       ▼                          │
        │   ConnectorWrapper (rlsconn)     │
        │       │                          │
        │       ▼                          │
        │   pgx/v5/stdlib.Connector        │ ← base pgx connector
        │                                  │
        ├── driver.Conn ──────────────────┐│
        │                                 ││
        │   pgxretry.retryableConn        ││ ← retry logic
        │       │                         ││
        │       ▼                         ││
        │   rlsconn.RLSSessionResetter    ││ ← sets session vars
        │       │                         ││
        │       ▼                         ││
        │   pgx/v5/stdlib.Conn            ││ ← base pgx connection
        │                                 ││
        ▼                                 ▼▼
     PostgreSQL
```

### Connector Chain

1. `database/sql` opens a connection via the registered `driver.Connector`
2. `pgxretry.Connector` delegates to its `ConnectorWrapper`
3. `rlsconn.RLSConnector` creates a base connection, wraps it with `RLSSessionResetter`, and calls `ResetSession` to set initial session variables
4. The wrapped connection is returned to `pgxretry`, which wraps it in `retryableConn`

### Connection Chain

When a query is executed:

1. `retryableConn` intercepts the call and applies retry logic
2. `RLSSessionResetter` delegates the query to the base connection
3. The base `pgx/v5/stdlib.Conn` executes the query against PostgreSQL

When a connection is acquired from the pool:

1. `database/sql` calls `ResetSession(ctx)` on the connection
2. `retryableConn.ResetSession` delegates to `RLSSessionResetter.ResetSession`
3. `RLSSessionResetter` builds a `SELECT set_config(...)` query from context values
4. Session variables are set on the connection before any application query

## Connection Lifecycle

```
Config.Connect()
    │
    ▼
newSession(config)
    ├── creates pgxretry.Config (if retry or RLS enabled)
    ├── creates rlsconn.Config (if RLS enabled with session vars)
    └── sets ConnectionResetFunc, ConnectorWrapper
    │
    ▼
session.connect()
    │
    ├── pgxretry.Register(retryConfig)     ← registers driver (once per process)
    │
    ├── postgres.New(DriverName: "pgxretry", DSN: ...)
    │       └── creates GORM dialector with pgxretry driver
    │
    ├── gormOpener(dialector, gormConfig)   ← opens GORM connection
    │
    └── configureConnectionPool(sqlDB)      ← sets MaxOpen, MaxIdle, MaxLifetime
    │
    ▼
Session ready (GORM DB + QueryDB)
```

### Driver Registration

The pgxretry driver is registered exactly once per process via `sync.Once`. The first caller's configuration is used for all subsequent connections. This means:

- If RLS is enabled, the first session to connect must have RLS configured
- The `ConnectorWrapper` from the first registration applies to all connections

## Session Model

### Session

`Session` holds a single database connection (GORM DB + sqlx QueryDB) with:

- Role identifier (`"writer"` or `"reader"`)
- Connection pool configuration
- Reset mutex for thread-safe connection swapping
- Cooldown timer to prevent rapid successive resets

### WriterSession and ReaderSession

```go
type WriterSession struct {
    *Session    // Full access including Transaction()
}

type ReaderSession struct {
    session *Session    // Restricted access: DB, QueryDB, GetDBOrTx (no Transaction)
}
```

- `WriterSession` embeds `Session` directly, exposing all methods including `Transaction()`
- `ReaderSession` wraps `Session` with a limited method set (no transaction support)

### SessionManager

```go
type SessionManager struct {
    writer *WriterSession
    reader *ReaderSession
}
```

Creates both writer and reader sessions from separate configs. Use `Writer()` and `Reader()` to access them.

## Connection Reset Flow

`ResetConnection` replaces the current database connection with a new one while minimizing disruption to in-flight queries.

```
ResetConnection(ctx)
    │
    ├── Check cooldown (RLock)
    │     └── Skip if less than 1 second since last reset
    │
    ├── Create new connection (no lock held)
    │     └── connect() → new GORM DB + sql.DB
    │
    ├── Ping new connection (5 second timeout)
    │     └── Close and return error on failure
    │
    ├── Acquire write lock
    │     ├── Double-check cooldown (another goroutine may have reset)
    │     ├── Swap s.db to new connection
    │     ├── Refresh QueryDB with new sqlx.DB
    │     └── Update lastResetTime
    │
    └── Graceful close of old connection (background goroutine)
          ├── Poll InUse count every 100ms
          ├── Wait up to GracefulCloseTimeout (default: 10s)
          └── Close old sql.DB
```

The reset flow is designed to:

- **Minimize lock duration**: New connection is created outside the write lock
- **Prevent thundering herd**: Cooldown period (1 second) prevents rapid successive resets
- **Double-check pattern**: Cooldown is verified both before and after acquiring the write lock
- **Graceful degradation**: Old connections are closed in a background goroutine after waiting for in-flight queries to complete

## Design Decisions

### Why GORM + sqlx dual dependency?

GORM provides ORM features (model binding, migrations, callbacks, association handling) that cover the majority of CRUD operations. However, GORM's API for complex raw SQL with named parameters (`:name` style) is limited. sqlx fills this gap with `NamedQueryContext` and `NamedExecContext`, which are more ergonomic for reporting queries, bulk operations, and cases where ORM overhead is undesirable.

Both share the same underlying `*sql.DB` connection pool. `QueryDB` wraps `sqlx.ExtContext`, not a separate connection.

### Why `database/sql` layer instead of pgxpool directly?

Wrapping at the `database/sql/driver` level (Connector, Conn, SessionResetter) enables:

1. **Transparent composition**: Retry logic and RLS session variable injection happen below the ORM layer. GORM and sqlx are unaware of these wrappers.
2. **Connection pool reuse**: `database/sql`'s built-in pool calls `SessionResetter.ResetSession(ctx)` on every connection acquire, which is the hook go-pgkit uses to inject RLS session variables with the current request's context.
3. **Library compatibility**: Any library that works with `database/sql` (GORM, sqlx, stdlib, etc.) can sit on top.

The tradeoff is that pgx-specific features (COPY, LISTEN/NOTIFY, batch queries) are not available through this path. See [pgxretry limitations](../pgxretry/README.md#limitations) for the full list.

### Why is RLS enabled by default?

go-pgkit is designed for multi-tenant applications where tenant isolation is a security requirement. Defaulting to enabled means:

- Forgetting to configure RLS does not silently expose cross-tenant data
- The default session variables (`app.current_tenant_id`, `app.current_user_id`) cover the most common multi-tenant pattern
- Disabling is a single option: `WithRLS(false)`

If your application is not multi-tenant, disable RLS explicitly.

### Why `sync.Once` for driver registration?

`database/sql.Register` requires a globally unique driver name and panics on duplicate registration. Since pgxretry registers under the name `"pgxretry"`, it can only be registered once per process. `sync.Once` ensures this.

The consequence is that the **first** `Config.Connect()` call determines the driver configuration (retry settings, ConnectorWrapper) for all subsequent connections in the process. This is a deliberate constraint — it matches how `database/sql` drivers work and avoids surprising behavior from mid-process reconfiguration.

### Why does `sslmode` default to `disable`?

The DSN builder defaults `sslmode` to `disable` for local development convenience. Production deployments should override this via `WithSSLMode("verify-full")` or the `DB_SSLMODE` environment variable. See [Configuration — Caveats](./configuration.md#caveats).
