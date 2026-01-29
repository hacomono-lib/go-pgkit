# Testing

This document covers how to run tests and use the test helpers provided by go-pgkit.

## Prerequisites

- Docker and Docker Compose
- Go 1.25+

## Running Tests

```bash
# Start PostgreSQL (Docker)
make up

# Run all tests in a Docker container
make test

# Stop PostgreSQL and remove volumes
make down
```

`make test` runs `docker compose run --rm test`, which executes:

```bash
go test -count=1 -race -v ./...
```

The test container connects to PostgreSQL with these environment variables (defined in `compose.yaml`):

| Variable | Value |
|----------|-------|
| `DB_HOST` | `postgres` |
| `DB_PORT` | `5432` |
| `DB_USER` | `app_user` |
| `DB_PASSWORD` | `app_password` |
| `DB_NAME` | `pgkit_test` |
| `DB_SCHEMA` | `public` |

## Database Schema

The test database is initialized by scripts in `docker/postgres/`:

- **`init.sql`** — Creates roles (`migration_user` with SUPERUSER, `app_user` with NOBYPASSRLS) and grants permissions
- **`migration.sql`** — Creates tables (`tenants`, `products`) with RLS policies on `products`

These scripts run automatically when the PostgreSQL container starts.

## TestSession

`conn.TestSession` provides test isolation using database transactions. Each test runs inside a transaction that is rolled back on cleanup, leaving the database unchanged.

```go
func TestMyFeature(t *testing.T) {
    cfg := conn.NewConfig(
        conn.WithEnvVars("writer"),
        conn.WithoutTrace(),
    )

    ts := conn.NewTestSession(t, cfg)
    // No need to call ts.Cleanup() — registered via t.Cleanup()

    ctx := ts.Context()

    // All operations run inside the test transaction
    err := ts.DB().WithContext(ctx).Table("products").Create(&product).Error
    require.NoError(t, err)

    // Transaction is rolled back when the test ends
}
```

### TestSession Methods

| Method | Returns | Description |
|--------|---------|-------------|
| `NewTestSession(t, config)` | `*TestSession` | Creates session, starts transaction, registers cleanup |
| `Context()` | `context.Context` | Context with the test transaction embedded |
| `DB()` | `*gorm.DB` | GORM DB bound to the test transaction |
| `QueryDB()` | `*QueryDB` | sqlx QueryDB bound to the test transaction |
| `Session()` | `*Session` | Underlying session |
| `Writer()` | `*WriterSession` | WriterSession wrapper |
| `Reader()` | `*ReaderSession` | ReaderSession wrapper |
| `GetDBOrTx(ctx)` | `*gorm.DB` | Returns transaction from context, or falls back to session DB |
| `GetQueryDBOrTx()` | `(*QueryDB, error)` | Returns transaction-bound QueryDB |
| `Cleanup()` | — | Rolls back transaction and closes session |

### Using QueryDB in Tests

```go
func TestNamedQuery(t *testing.T) {
    ts := conn.NewTestSession(t, cfg)
    ctx := ts.Context()
    qdb := ts.QueryDB()

    rows, err := qdb.NamedQueryContext(ctx, "SELECT * FROM products WHERE name = :name", map[string]any{
        "name": "Widget",
    })
    require.NoError(t, err)
    defer rows.Close()
}
```

## Testing with RLS

`TestSession.SetRLSContext` sets PostgreSQL session variables inside the test transaction using `SET LOCAL` (`is_local=true`), so variables are scoped to the transaction and reset on rollback.

```go
func TestRLSFiltering(t *testing.T) {
    ts := conn.NewTestSession(t, cfg)

    tenantA := uuid.New().String()
    tenantB := uuid.New().String()

    // Insert test data (no RLS filtering during setup)
    ts.DB().Exec("INSERT INTO tenants (id, name) VALUES ($1, $2)", tenantA, "Tenant A")
    ts.DB().Exec("INSERT INTO tenants (id, name) VALUES ($1, $2)", tenantB, "Tenant B")
    ts.DB().Exec("INSERT INTO products (id, tenant_id, name, price, currency) VALUES ($1, $2, $3, $4, $5)",
        uuid.New().String(), tenantA, "Widget", 1000, "JPY")
    ts.DB().Exec("INSERT INTO products (id, tenant_id, name, price, currency) VALUES ($1, $2, $3, $4, $5)",
        uuid.New().String(), tenantB, "Gadget", 2000, "JPY")

    // Set RLS context for tenant A
    ctx := ts.SetRLSContext(
        map[string]string{"app.current_tenant_id": tenantA},
        nil, // no context setter needed for this test
    )

    // Query should only return tenant A's products
    var count int64
    err := ts.DB().WithContext(ctx).Table("products").Count(&count).Error
    require.NoError(t, err)
    require.Equal(t, int64(1), count)
}
```

### SetRLSContext Parameters

```go
func (s *TestSession) SetRLSContext(
    vars map[string]string,                                       // name → value (empty string resets to NULL)
    ctxSetter func(ctx context.Context, name, value string) context.Context, // optional context updater
) context.Context
```

- `vars`: Map of session variable names to values. Empty string values reset the variable to NULL.
- `ctxSetter`: Optional callback to also set values in the Go context (useful when testing code that reads from both PostgreSQL and Go context).

### TestRLSSession

For testing the RLS implementation itself (not application code that relies on RLS), use `TestRLSSession`. It manages two sessions:

- **superuserSession**: Bypasses RLS for test data setup/cleanup
- **rlsSession**: Enforces RLS (same flow as production)

```go
func TestRLSImplementation(t *testing.T) {
    superCfg := conn.NewConfig(
        conn.WithHost("localhost"),
        conn.WithPort(5432),
        conn.WithCredentials("migration_user", "migration_password"),
        conn.WithDatabase("pgkit_test"),
        conn.WithRLS(false),
        conn.WithoutTrace(),
    )

    rlsCfg := conn.NewConfig(
        conn.WithEnvVars("writer"), // app_user with NOBYPASSRLS
        conn.WithoutTrace(),
    )

    ts := conn.NewTestRLSSession(t, superCfg, rlsCfg)

    // Setup data as superuser (bypasses RLS)
    ctx := context.Background()
    ts.ExecAsSuperuser(ctx, "INSERT INTO tenants (id, name) VALUES ($1, $2)", tenantID, "Test")

    // Register cleanup (runs in reverse order on test end)
    ts.RegisterCleanup(func() {
        ts.ExecAsSuperuser(context.Background(), "DELETE FROM tenants WHERE id = $1", tenantID)
    })

    // Test RLS with the app_user session
    ctxWithTenant := rlsctx.WithTenantID(ctx, uuid.MustParse(tenantID))
    var count int64
    err := ts.RLSDB().WithContext(ctxWithTenant).Table("products").Count(&count).Error
    require.NoError(t, err)
}
```

| Method | Description |
|--------|-------------|
| `NewTestRLSSession(t, superCfg, rlsCfg)` | Creates both sessions |
| `SuperuserSession()` / `SuperuserDB()` | Access superuser session (RLS bypassed) |
| `RLSSession()` / `RLSDB()` | Access RLS-enforced session |
| `ExecAsSuperuser(ctx, sql, args...)` | Execute SQL as superuser |
| `ExecAsSuperuserWithCleanup(ctx, sql, cleanupSQL, args...)` | Execute SQL and register cleanup |
| `RegisterCleanup(fn)` | Register cleanup function (executed in LIFO order) |
| `Cleanup()` | Run cleanups and close sessions |
