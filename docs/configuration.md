# Configuration

This document covers all configuration options for go-pgkit.

## Functional Options

`conn.NewConfig` accepts functional options to build a `Config`:

```go
cfg := conn.NewConfig(
    conn.WithHost("localhost"),
    conn.WithPort(5432),
    conn.WithCredentials("user", "password"),
    conn.WithDatabase("mydb"),
)
```

### Options Reference

| Option | Description |
|--------|-------------|
| `WithRole(role string)` | Set connection role (`"writer"` or `"reader"`) |
| `WithHost(host string)` | Set database host |
| `WithPort(port int)` | Set database port |
| `WithCredentials(user, password string)` | Set database user and password |
| `WithDatabase(dbName string)` | Set database name |
| `WithSSLMode(sslMode string)` | Set SSL mode (`"disable"`, `"require"`, `"verify-full"`, etc.) |
| `WithConnectionPool(maxOpen, maxIdle, maxLifetime int)` | Set pool parameters (maxLifetime in minutes) |
| `WithRetry(enable bool, maxRetries int, backoffMs int)` | Configure whether the driver re-runs a query that fails outside a transaction (see [Caveats](#withretry-applies-only-to-queries-outside-transactions)) |
| `WithConnectTimeout(seconds int)` | TCP connection timeout in seconds (default: `10`) |
| `WithGracefulCloseTimeout(seconds int)` | Timeout for graceful connection close on reset |
| `WithLogLevel(level LogLevel)` | Set GORM log level (`LogLevelSilent`, `LogLevelError`, `LogLevelWarn`, `LogLevelInfo`) |
| `WithSilentGormLogger()` | Shortcut for `WithLogLevel(LogLevelSilent)` |
| `WithRLS(enable bool)` | Enable/disable Row-Level Security |
| `WithRLSSessionVars(vars []rlsconn.SessionVar)` | Set custom RLS session variables |
| `WithEnvVars(role string)` | Load configuration from environment variables (prefix: `DB_`) |
| `WithEnvVarsWithPrefix(role, prefix string)` | Load configuration from environment variables with a custom prefix |
| `WithoutTrace()` | Disable all tracing hooks |
| `WithGormOpener(opener GormOpener)` | Custom GORM connection opener (e.g., for APM) |
| `WithGormCallbackRegistrar(registrar GormCallbackRegistrar)` | Register GORM callbacks (e.g., for APM resource naming) |
| `WithSpanStarter(starter SpanStarter)` | Custom span starter for QueryDB tracing |
| `WithBeforeConnect(fn func(context.Context, *pgx.ConnConfig) error)` | Change the connection config before each new connection of the session (e.g., set an RDS IAM auth token as the password) |

Additionally, `Config` has a method (not a `ConfigOption` function):

| Method | Description |
|--------|-------------|
| `config.WithLogger(logger *slog.Logger)` | Set slog logger for database operations |

## Environment Variables

`WithEnvVars(role)` reads the following environment variables (prefix: `DB_`):

| Variable | Description | Default |
|----------|-------------|---------|
| `{PREFIX}HOST` | Database host | — |
| `{PREFIX}PORT` | Database port | — |
| `{PREFIX}USER` | Database user | — |
| `{PREFIX}PASSWORD` | Database password | — |
| `{PREFIX}NAME` | Database name | — |
| `{PREFIX}MAX_OPEN_CONNS` | Maximum open connections | `0` (unlimited) |
| `{PREFIX}MAX_IDLE_CONNS` | Maximum idle connections | `2` |
| `{PREFIX}CONN_MAX_LIFETIME` | Connection max lifetime (minutes) | `0` (unlimited) |
| `{PREFIX}ENABLE_RETRY` | Enable retry (`"true"`/`"false"`) | `true` |
| `{PREFIX}MAX_RETRIES` | Maximum retry attempts | `3` |
| `{PREFIX}RETRY_BACKOFF_MS` | Retry backoff (milliseconds) | `100` |
| `{PREFIX}CONNECT_TIMEOUT` | TCP connection timeout (seconds) | `10` |
| `{PREFIX}GRACEFUL_CLOSE_TIMEOUT` | Graceful close timeout (seconds) | `10` |
| `{PREFIX}SSLMODE` | SSL mode | `disable` |
| `{PREFIX}TIMEZONE` | Connection timezone | `UTC` |
| `{PREFIX}SCHEMA` | Search path schema | Value of `{PREFIX}NAME` |

`{PREFIX}` defaults to `DB_`. Use `WithEnvVarsWithPrefix` to change it:

```go
// Default: reads DB_HOST, DB_PORT, etc.
cfg := conn.NewConfig(conn.WithEnvVars("writer"))

// Custom: reads MYAPP_HOST, MYAPP_PORT, etc.
cfg := conn.NewConfig(conn.WithEnvVarsWithPrefix("writer", "MYAPP_"))
```

### Role-Based Overrides

When a role is specified, role-specific environment variables take precedence. The pattern is `{PREFIX}{ROLE}_{FIELD}`:

```
DB_HOST=default-host          # base value
DB_WRITER_HOST=writer-host    # override for "writer" role
DB_READER_HOST=reader-host    # override for "reader" role
```

Example with writer/reader separation:

```go
writerCfg := conn.NewConfig(conn.WithEnvVars("writer"))  // reads DB_WRITER_HOST, falls back to DB_HOST
readerCfg := conn.NewConfig(conn.WithEnvVars("reader"))  // reads DB_READER_HOST, falls back to DB_HOST
```

All variables listed above support this override pattern, except `{PREFIX}TIMEZONE` and `{PREFIX}SCHEMA` which do not support role-based overrides (see [Caveats](#timezone-and-schema-are-read-at-dsn-build-time-not-by-withenvvars)).

## Default Values

Values applied by `NewConfig` before any options are evaluated:

| Field | Default Value |
|-------|---------------|
| `MaxIdleConns` | `2` |
| `EnableRetry` | `true` |
| `MaxRetries` | `3` |
| `RetryBackoff` | `100` (milliseconds) |
| `ConnectTimeout` | `10` (seconds) |
| `GracefulCloseTimeout` | `10` (seconds) |
| `EnableRLS` | `true` |
| `RLSSessionVars` | `app.current_tenant_id` (from rlsctx) and `app.current_user_id` (from rlsctx) |
| `LogLevel` | `Info` (when nil) |
| Reset cooldown | `1` second (not configurable) |

## pgxretry Configuration

When retry or RLS is enabled, `conn` creates a `pgxretry.Config` internally with these mappings:

| conn field | pgxretry field | Notes |
|------------|---------------|-------|
| `EnableRetry` | `DisableRetry` | `DisableRetry` is `!EnableRetry`. See [Caveats](#withretry-applies-only-to-queries-outside-transactions) |
| `MaxRetries` | `MaxRetries` | Used only when `EnableRetry` is `true`. `0` is replaced with pgxretry's default (`3`) |
| `RetryBackoff` (ms) | `InitialBackoff` | Converted to `time.Duration` |
| — | `BackoffFactor` | Fixed at `2.0` |
| — | `MaxBackoff` | Fixed at `5s` |

For standalone pgxretry usage with more control, see [pgxretry/README.md](../pgxretry/README.md).

## Caveats

### Unset environment variables preserve defaults

`WithEnvVars` only overrides default values when an environment variable is actually set (non-empty string). If an environment variable is not set or empty, the corresponding default value from `NewConfig` is preserved. This means:

- `DB_MAX_IDLE_CONNS` unset → default `2` is kept
- `DB_MAX_IDLE_CONNS=0` → explicitly sets `MaxIdleConns` to `0`
- `DB_MAX_RETRIES=0` → explicitly sets `MaxRetries` to `0` (disables retry)

This also applies when `WithEnvVars` is used after other functional options:

```go
cfg := conn.NewConfig(
    conn.WithPort(9999),
    conn.WithEnvVars("writer"), // does NOT overwrite port if DB_PORT is unset
)
```

### `sslmode` defaults to `disable`

The DSN builder defaults `sslmode` to `disable`. Use `WithSSLMode` or the `DB_SSLMODE` environment variable to override:

```go
cfg := conn.NewConfig(
    conn.WithEnvVars("writer"),
    conn.WithSSLMode("verify-full"),
)
```

Valid values: `disable`, `allow`, `prefer`, `require`, `verify-ca`, `verify-full`.

### `TIMEZONE` and `SCHEMA` are read at DSN build time, not by `WithEnvVars`

`{PREFIX}TIMEZONE` and `{PREFIX}SCHEMA` are read directly from environment variables inside the `dsn()` method, not by `WithEnvVars`. This means they take effect regardless of which configuration method you use (functional options or env vars), and they do not support role-based overrides (`DB_WRITER_TIMEZONE` will not work). They do respect the custom prefix set by `WithEnvVarsWithPrefix`.

### Connection values are passed literally

Host, user, password, database name, `sslmode` and `{PREFIX}SCHEMA` are single-quoted in the DSN, so spaces, quotes and backslashes in them (e.g. in a password) reach the server as-is. Do not escape them yourself, and do not use one of them to pass extra connection parameters (e.g. `sslmode=verify-full sslrootcert=...`); the whole string becomes the value.

`{PREFIX}TIMEZONE` is the exception: it is written unquoted because GORM's postgres driver reads it from the raw DSN. Set it to a plain time zone name such as `Asia/Tokyo`.

### GORM prepared statements are disabled

GORM's `PrepareStmt` mode is not enabled. This ensures that the request context (carrying tenant/user IDs) is correctly passed through `database/sql` to `SessionResetter.ResetSession(ctx)` on every query. Enabling prepared statements would bypass this context propagation.

### Each session has its own connector

Each `Config.Connect()` opens the session with its own `driver.Connector` (`sql.OpenDB`), so retry and RLS settings and `BeforeConnect` apply only to that session, whichever session connects first. See [Architecture — Design Decisions](./architecture.md#why-a-connector-per-session) for details.

### `BeforeConnect` gets a shallow copy of the config and is not covered by `connect_timeout`

The hook gets a shallow copy of the session's `pgx.ConnConfig`. Assigning a field such as `Password` is safe, but maps and pointers (`RuntimeParams`, `TLSConfig`, `Fallbacks`) are shared by all connections of the session; writing to them races when the pool opens connections concurrently, and the change stays for later connections. Copy the config first (`*cc = *cc.Copy()`) to change them.

The hook runs before pgx applies `ConnectTimeout` (`connect_timeout`), and `ctx` may have no deadline (database/sql opens connections with the caller's context, or with a background context in the pool's opener goroutine). Bound any network call in the hook, such as refreshing credentials, with its own timeout.

### `WithRetry` applies only to queries outside transactions

`WithRetry` controls whether pgxretry re-runs a single query that fails with a retryable error outside a transaction (autocommit, e.g., `session.DB().Exec(...)` outside `Transaction`).

Queries in a transaction are never re-run by the driver, with retry enabled or disabled. A retryable error there is returned as `pgxretry.ErrRetryableInTx`, and `TransactionManager` resets the connection and runs the whole transaction function again in a new transaction. This also holds with `WithRetry(false, ...)`, unless RLS is also disabled: with both retry and RLS disabled, the session does not use pgxretry, so errors are not wrapped and `TransactionManager` does not re-run the transaction.
