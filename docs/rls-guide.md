# Row-Level Security (RLS) Guide

This guide covers end-to-end setup of PostgreSQL Row-Level Security with go-pgkit: from database schema to Go application code.

## Overview

Row-Level Security (RLS) allows PostgreSQL to filter rows automatically based on session variables. go-pgkit sets these session variables on every connection acquired from the pool, ensuring tenant isolation without modifying application queries.

RLS is **enabled by default** in go-pgkit. Every connection sets `app.current_tenant_id` and `app.current_user_id` from the request context before executing queries.

## PostgreSQL Setup

### 1. Create Roles

Create an application user with `NOBYPASSRLS` to ensure RLS policies are always enforced:

```sql
-- Superuser for migrations (bypasses RLS)
CREATE ROLE migration_user WITH LOGIN PASSWORD 'migration_password' SUPERUSER;

-- Application user (RLS enforced)
CREATE ROLE app_user WITH LOGIN PASSWORD 'app_password'
  NOSUPERUSER NOCREATEDB NOCREATEROLE NOBYPASSRLS;
```

### 2. Create Tables and Enable RLS

```sql
SET ROLE migration_user;

CREATE TABLE tenants (
    id VARCHAR(255) PRIMARY KEY,
    name VARCHAR(255) NOT NULL,
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

CREATE TABLE products (
    id VARCHAR(255) PRIMARY KEY,
    tenant_id VARCHAR(255) NOT NULL REFERENCES tenants(id),
    name VARCHAR(255) NOT NULL,
    price BIGINT NOT NULL,
    currency VARCHAR(10) NOT NULL,
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

-- Enable and force RLS (FORCE applies even to table owners)
ALTER TABLE products ENABLE ROW LEVEL SECURITY;
ALTER TABLE products FORCE ROW LEVEL SECURITY;
```

### 3. Create Policies

```sql
CREATE POLICY products_tenant_isolation ON products
    FOR ALL
    USING (tenant_id = current_setting('app.current_tenant_id', true))
    WITH CHECK (tenant_id = current_setting('app.current_tenant_id', true));
```

- `current_setting('app.current_tenant_id', true)` — the `true` parameter returns NULL instead of raising an error when the variable is not set
- `USING` filters rows on SELECT, UPDATE, DELETE
- `WITH CHECK` validates rows on INSERT and UPDATE

### 4. Grant Permissions

```sql
GRANT ALL PRIVILEGES ON ALL TABLES IN SCHEMA public TO app_user;
GRANT ALL PRIVILEGES ON ALL SEQUENCES IN SCHEMA public TO app_user;

RESET ROLE;
```

## Go Setup

### 1. HTTP Middleware — Set Context

```go
func TenantMiddleware(next http.Handler) http.Handler {
    return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
        tenantID, err := uuid.Parse(r.Header.Get("X-Tenant-ID"))
        if err != nil {
            http.Error(w, "invalid tenant ID", http.StatusBadRequest)
            return
        }
        userID, err := uuid.Parse(r.Header.Get("X-User-ID"))
        if err != nil {
            http.Error(w, "invalid user ID", http.StatusBadRequest)
            return
        }

        ctx := r.Context()
        ctx = rlsctx.WithTenantID(ctx, tenantID)
        ctx = rlsctx.WithUserID(ctx, userID)

        next.ServeHTTP(w, r.WithContext(ctx))
    })
}
```

### 2. Configuration

```go
cfg := conn.NewConfig(
    conn.WithEnvVars("writer"),
    conn.WithConnectionPool(25, 5, 60),
    // RLS is enabled by default with app.current_tenant_id and app.current_user_id
)

session, err := cfg.Connect()
if err != nil {
    log.Fatal(err)
}
```

### 3. Query

```go
func ListProducts(ctx context.Context, session *conn.Session) ([]Product, error) {
    var products []Product
    // RLS automatically filters by the tenant ID in the context
    err := session.GetDBOrTx(ctx).Find(&products).Error
    return products, err
}
```

No tenant filter in the query — PostgreSQL enforces it via the RLS policy.

## How It Works Internally

When `session.GetDBOrTx(ctx)` acquires a connection from the pool:

```
1. database/sql acquires connection (new or from pool)
       │
2. Calls RLSSessionResetter.ResetSession(ctx)
       │
3. For each configured SessionVar:
   ├── ValueGetter(ctx) returns (value, true)  → set_config(name, value, false)
   └── ValueGetter(ctx) returns ("", false)    → set_config(name, NULL, false)
       │
4. Single query executed:
   SELECT set_config($1, $2, false), set_config($3, $4, false)
   -- $1='app.current_tenant_id', $2='<tenant-uuid>'
   -- $3='app.current_user_id',   $4='<user-uuid>'
       │
5. Connection returned with session variables set
       │
6. Application query executes with RLS filtering active
```

- `is_local=false` makes the setting session-scoped (persists until the connection is returned to the pool and re-acquired)
- `set_config()` with parameterized arguments is SQL-injection safe
- All variables are set in a single round-trip for efficiency

## Custom Session Variables

To use custom session variables instead of the defaults:

```go
cfg := conn.NewConfig(
    conn.WithEnvVars("writer"),
    conn.WithRLSSessionVars([]rlsconn.SessionVar{
        {
            Name: "app.current_tenant_id",
            ValueGetter: rlsctx.GetTenantIDFromContext,
        },
        {
            Name: "app.current_organization_id",
            ValueGetter: func(ctx context.Context) (string, bool) {
                orgID, ok := ctx.Value(orgIDKey{}).(string)
                return orgID, ok
            },
        },
    }),
)
```

Variable names must match the pattern `namespace.name` (e.g., `app.current_tenant_id`). Only alphanumeric characters and underscores are allowed. This is validated at startup to prevent injection.

## Disabling RLS

```go
cfg := conn.NewConfig(
    conn.WithEnvVars("writer"),
    conn.WithRLS(false),
)
```

When disabled, no session variables are set and the `rlsconn` wrapper is not applied.

## Security Considerations

1. **Use `NOBYPASSRLS`** on the application database role. Without this, a superuser or role with `BYPASSRLS` skips all policies.

2. **Use `FORCE ROW LEVEL SECURITY`** on tables. Without `FORCE`, the table owner bypasses policies.

3. **Variable name validation**: `rlsconn` validates all session variable names against the regex `^[a-zA-Z_][a-zA-Z0-9_]*\.[a-zA-Z_][a-zA-Z0-9_]*$` at connector creation time. Invalid names cause a panic (or error with `NewRLSConnectorWithValidation`).

4. **Parameterized queries**: Session variable values are passed as `$1, $2, ...` parameters to `set_config()`, not interpolated into SQL strings.

5. **Fail-closed**: If the driver does not support `ExecerContext` or `Execer`, `RLSSessionResetter` returns an error rather than silently skipping session variable setup. Queries will not execute without proper tenant isolation.

6. **Connection pool safety**: Session variables are reset on every connection acquire (both new and pooled connections), so a connection cannot leak tenant context from a previous request.
