package conn

import (
	"context"
	"fmt"
	"os"
	"sync"
	"testing"

	"github.com/google/uuid"
	"github.com/stretchr/testify/suite"

	"github.com/hacomono-lib/go-pgkit/rlsctx"
)

// RLSQueryDBTestSuite tests RLS functionality with QueryDB (sqlx wrapper)
type RLSQueryDBTestSuite struct {
	suite.Suite
}

func TestRLSQueryDBTestSuite(t *testing.T) {
	suite.Run(t, new(RLSQueryDBTestSuite))
}

// TestQueryDBWithRLS verifies that RLS works correctly with QueryDB (sqlx wrapper).
// This test creates data with a superuser session and verifies RLS filtering via QueryDB.
func (suite *RLSQueryDBTestSuite) TestQueryDBWithRLS() {
	ctx := context.Background()

	// Create superuser session for setup/cleanup (bypasses RLS)
	superuserConfig := NewConfig(
		WithHost(os.Getenv("DB_HOST")),
		WithPort(atoi(getEnvWithRole("DB_PORT", "", defaultEnvPrefix))),
		WithCredentials("migration_user", "migration_password"),
		WithDatabase(os.Getenv("DB_NAME")),
		WithRLS(false),
		WithoutTrace(),
	)
	superuserSession, err := superuserConfig.Connect()
	suite.Require().NoError(err)
	defer superuserSession.Close()

	// Create RLS-enabled session for verification (uses app_user)
	rlsConfig := NewConfig(
		WithEnvVars("writer"),
		WithRLS(true),
	)
	rlsSession, err := rlsConfig.Connect()
	suite.Require().NoError(err)
	defer rlsSession.Close()

	// Step 1: Create a tenant using superuser
	tenantID := uuid.New()
	err = superuserSession.DB().WithContext(ctx).Exec(
		"INSERT INTO tenants (id, name, created_at, updated_at) VALUES (?, ?, NOW(), NOW())",
		tenantID.String(), "Test Tenant for QueryDB RLS",
	).Error
	suite.Require().NoError(err, "Superuser should be able to create tenant")

	// Step 2: Create products using superuser
	productID1 := uuid.New()
	productID2 := uuid.New()
	err = superuserSession.DB().WithContext(ctx).Exec(
		"INSERT INTO products (id, tenant_id, name, price, currency, created_at, updated_at) VALUES (?, ?, ?, ?, ?, NOW(), NOW())",
		productID1.String(), tenantID.String(), "Test Product 1", 1000, "JPY",
	).Error
	suite.Require().NoError(err)

	err = superuserSession.DB().WithContext(ctx).Exec(
		"INSERT INTO products (id, tenant_id, name, price, currency, created_at, updated_at) VALUES (?, ?, ?, ?, ?, NOW(), NOW())",
		productID2.String(), tenantID.String(), "Test Product 2", 2000, "JPY",
	).Error
	suite.Require().NoError(err)

	// Cleanup
	defer func() {
		superuserSession.DB().WithContext(ctx).Exec("DELETE FROM products WHERE tenant_id = ?", tenantID.String())
		superuserSession.DB().WithContext(ctx).Exec("DELETE FROM tenants WHERE id = ?", tenantID.String())
	}()

	queryDB := rlsSession.QueryDB()
	suite.Require().NotNil(queryDB, "QueryDB should not be nil")

	// Test 1: QueryDB with correct tenant context - should see products
	suite.Run("querydb_with_tenant_context", func() {
		ctxWithTenant := rlsctx.WithTenantID(ctx, tenantID)

		rows, err := queryDB.NamedQueryContext(ctxWithTenant,
			"SELECT id, name FROM products WHERE tenant_id = :tenant_id",
			map[string]any{"tenant_id": tenantID.String()},
		)
		suite.Require().NoError(err)
		defer rows.Close()

		var count int
		for rows.Next() {
			var id, name string
			err := rows.Scan(&id, &name)
			suite.Require().NoError(err)
			count++
		}
		suite.Equal(2, count, "Should see 2 products with correct tenant context")
	})

	// Test 2: QueryDB without tenant context - should see NO products (RLS blocks)
	suite.Run("querydb_without_tenant_context", func() {
		rows, err := queryDB.NamedQueryContext(ctx,
			"SELECT id, name FROM products WHERE tenant_id = :tenant_id",
			map[string]any{"tenant_id": tenantID.String()},
		)
		suite.Require().NoError(err)
		defer rows.Close()

		var count int
		for rows.Next() {
			count++
		}
		suite.Equal(0, count, "Should see 0 products without tenant context (RLS)")
	})

	// Test 3: QueryDB with wrong tenant context - should see NO products
	suite.Run("querydb_with_wrong_tenant_context", func() {
		wrongTenantID := uuid.New()
		ctxWrongTenant := rlsctx.WithTenantID(ctx, wrongTenantID)

		rows, err := queryDB.NamedQueryContext(ctxWrongTenant,
			"SELECT id, name FROM products WHERE tenant_id = :tenant_id",
			map[string]any{"tenant_id": tenantID.String()},
		)
		suite.Require().NoError(err)
		defer rows.Close()

		var count int
		for rows.Next() {
			count++
		}
		suite.Equal(0, count, "Should see 0 products with wrong tenant context")
	})

	// Test 4: NamedExecContext with RLS
	suite.Run("named_exec_with_rls", func() {
		ctxWithTenant := rlsctx.WithTenantID(ctx, tenantID)
		newProductID := uuid.New()

		// Insert via QueryDB with correct tenant context
		result, err := queryDB.NamedExecContext(ctxWithTenant,
			"INSERT INTO products (id, tenant_id, name, price, currency, created_at, updated_at) VALUES (:id, :tenant_id, :name, :price, :currency, NOW(), NOW())",
			map[string]any{
				"id":        newProductID.String(),
				"tenant_id": tenantID.String(),
				"name":      "QueryDB Product",
				"price":     3000,
				"currency":  "JPY",
			},
		)
		suite.Require().NoError(err)
		affected, _ := result.RowsAffected()
		suite.Equal(int64(1), affected)

		// Cleanup the new product
		defer superuserSession.DB().WithContext(ctx).Exec("DELETE FROM products WHERE id = ?", newProductID.String())

		// Verify via GORM that the product was created
		var count int64
		err = rlsSession.DB().WithContext(ctxWithTenant).Table("products").Where("id = ?", newProductID.String()).Count(&count).Error
		suite.Require().NoError(err)
		suite.Equal(int64(1), count, "Product should be visible via GORM with correct tenant context")
	})

	// Test 5: Verify session variable is set correctly
	suite.Run("verify_session_variable_via_querydb", func() {
		ctxWithTenant := rlsctx.WithTenantID(ctx, tenantID)

		row, err := queryDB.NamedQueryRowContext(ctxWithTenant,
			"SELECT COALESCE(current_setting('app.current_tenant_id', true), '') as tenant_id",
			map[string]any{},
		)
		suite.Require().NoError(err)

		var currentTenantID string
		err = row.Scan(&currentTenantID)
		suite.Require().NoError(err)
		suite.Equal(tenantID.String(), currentTenantID, "Session variable should match the context tenant ID")
	})
}

// TestConcurrentQueryDBRLS tests that RLS correctly isolates data
// when multiple goroutines access the database via QueryDB concurrently.
func (suite *RLSQueryDBTestSuite) TestConcurrentQueryDBRLS() {
	ctx := context.Background()

	// Create superuser session for setup/cleanup
	superuserConfig := NewConfig(
		WithHost(os.Getenv("DB_HOST")),
		WithPort(atoi(getEnvWithRole("DB_PORT", "", defaultEnvPrefix))),
		WithCredentials("migration_user", "migration_password"),
		WithDatabase(os.Getenv("DB_NAME")),
		WithRLS(false),
		WithoutTrace(),
	)
	superuserSession, err := superuserConfig.Connect()
	suite.Require().NoError(err)
	defer superuserSession.Close()

	// Create RLS-enabled session
	rlsConfig := NewConfig(
		WithEnvVars("writer"),
		WithRLS(true),
		WithConnectionPool(5, 2, 0), // Limit connections to avoid exhaustion
	)
	rlsSession, err := rlsConfig.Connect()
	suite.Require().NoError(err)
	defer rlsSession.Close()

	// Setup test data
	tenantID := uuid.New()
	productIDs := []uuid.UUID{uuid.New(), uuid.New(), uuid.New()}

	err = superuserSession.DB().WithContext(ctx).Exec(
		"INSERT INTO tenants (id, name, created_at, updated_at) VALUES (?, ?, NOW(), NOW())",
		tenantID.String(), "Concurrent QueryDB RLS Test Tenant",
	).Error
	suite.Require().NoError(err)

	for i, productID := range productIDs {
		err = superuserSession.DB().WithContext(ctx).Exec(
			"INSERT INTO products (id, tenant_id, name, price, currency, created_at, updated_at) VALUES (?, ?, ?, ?, ?, NOW(), NOW())",
			productID.String(), tenantID.String(), fmt.Sprintf("Concurrent Product %d", i+1), 1000*(i+1), "JPY",
		).Error
		suite.Require().NoError(err)
	}

	defer func() {
		superuserSession.DB().WithContext(ctx).Exec("DELETE FROM products WHERE tenant_id = ?", tenantID.String())
		superuserSession.DB().WithContext(ctx).Exec("DELETE FROM tenants WHERE id = ?", tenantID.String())
	}()

	// Concurrent test
	const goroutines = 30
	var wg sync.WaitGroup
	errors := make(chan string, goroutines*2)
	queryDB := rlsSession.QueryDB()

	for i := range goroutines {
		wg.Add(2)

		// Goroutine with correct tenant - should see products
		go func(idx int) {
			defer wg.Done()
			ctxWithTenant := rlsctx.WithTenantID(context.Background(), tenantID)

			rows, err := queryDB.NamedQueryContext(ctxWithTenant,
				"SELECT id FROM products WHERE tenant_id = :tenant_id",
				map[string]any{"tenant_id": tenantID.String()},
			)
			if err != nil {
				errors <- fmt.Sprintf("goroutine %d (with tenant): query error: %v", idx, err)
				return
			}
			defer rows.Close()

			var count int
			for rows.Next() {
				count++
			}
			if count != 3 {
				errors <- fmt.Sprintf("goroutine %d (with tenant): expected 3 products, got %d", idx, count)
			}
		}(i)

		// Goroutine without tenant - should see NO products
		go func(idx int) {
			defer wg.Done()

			rows, err := queryDB.NamedQueryContext(context.Background(),
				"SELECT id FROM products WHERE tenant_id = :tenant_id",
				map[string]any{"tenant_id": tenantID.String()},
			)
			if err != nil {
				errors <- fmt.Sprintf("goroutine %d (no tenant): query error: %v", idx, err)
				return
			}
			defer rows.Close()

			var count int
			for rows.Next() {
				count++
			}
			if count != 0 {
				errors <- fmt.Sprintf("goroutine %d (no tenant): expected 0 products, got %d - RLS NOT WORKING!", idx, count)
			}
		}(i)
	}

	wg.Wait()
	close(errors)

	var errorList []string
	for err := range errors {
		errorList = append(errorList, err)
	}

	if len(errorList) > 0 {
		suite.Fail("QueryDB RLS isolation failed", "Errors:\n%v", errorList)
	}
}
