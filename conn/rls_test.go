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

// RLSContextTestSuite tests RLS context functions (no database required)
type RLSContextTestSuite struct {
	suite.Suite
}

// RLSIntegrationTestSuite tests RLS database functionality (requires database)
type RLSIntegrationTestSuite struct {
	suite.Suite
	session *TestSession
}

func (suite *RLSIntegrationTestSuite) SetupTest() {
	config := NewConfig(WithEnvVars("writer"))
	suite.session = NewTestSession(suite.T(), config)
}

func (suite *RLSIntegrationTestSuite) TearDownTest() {
	if suite.session != nil {
		suite.session.Cleanup()
	}
}

// TestRLSContextFunctions tests the RLS context helper functions
func (suite *RLSContextTestSuite) TestRLSContextFunctions() {
	ctx := context.Background()

	// Test WithTenantID and GetTenantIDFromContext
	suite.Run("tenant_id_context", func() {
		tenantID := uuid.New()
		ctx := rlsctx.WithTenantID(ctx, tenantID)

		got, ok := rlsctx.GetTenantIDFromContext(ctx)
		suite.True(ok, "should find tenant ID in context")
		suite.Equal(tenantID.String(), got, "tenant ID should match")
	})

	// Test WithUserID and GetUserIDFromContext
	suite.Run("user_id_context", func() {
		userID := uuid.New()
		ctx := rlsctx.WithUserID(ctx, userID)

		got, ok := rlsctx.GetUserIDFromContext(ctx)
		suite.True(ok, "should find user ID in context")
		suite.Equal(userID.String(), got, "user ID should match")
	})

	// Test missing values return false
	suite.Run("missing_values", func() {
		_, ok := rlsctx.GetTenantIDFromContext(ctx)
		suite.False(ok, "should return false when tenant ID not set")

		_, ok = rlsctx.GetUserIDFromContext(ctx)
		suite.False(ok, "should return false when user ID not set")
	})

	// Test both values can coexist
	suite.Run("both_values", func() {
		tenantID := uuid.New()
		userID := uuid.New()

		ctx := rlsctx.WithTenantID(ctx, tenantID)
		ctx = rlsctx.WithUserID(ctx, userID)

		gotTenant, ok := rlsctx.GetTenantIDFromContext(ctx)
		suite.True(ok)
		suite.Equal(tenantID.String(), gotTenant)

		gotUser, ok := rlsctx.GetUserIDFromContext(ctx)
		suite.True(ok)
		suite.Equal(userID.String(), gotUser)
	})
}

// TestConcurrentContexts tests that concurrent operations maintain context isolation
func (suite *RLSContextTestSuite) TestConcurrentContexts() {
	// Test that context values don't leak between concurrent goroutines
	const goroutines = 50
	var wg sync.WaitGroup
	errors := make(chan error, goroutines*2)

	for i := range goroutines {
		wg.Add(2)

		// Goroutine A with unique tenant UUID
		go func(idx int) {
			defer wg.Done()
			ctx := context.Background()
			tenantA := uuid.New()
			ctx = rlsctx.WithTenantID(ctx, tenantA)

			// Small delay to increase chance of interleaving
			got, ok := rlsctx.GetTenantIDFromContext(ctx)
			if !ok || got != tenantA.String() {
				errors <- &contextError{
					expected: tenantA.String(),
					got:      got,
					index:    idx,
					tenant:   "A",
				}
			}
		}(i)

		// Goroutine B with different tenant UUID
		go func(idx int) {
			defer wg.Done()
			ctx := context.Background()
			tenantB := uuid.New()
			ctx = rlsctx.WithTenantID(ctx, tenantB)

			got, ok := rlsctx.GetTenantIDFromContext(ctx)
			if !ok || got != tenantB.String() {
				errors <- &contextError{
					expected: tenantB.String(),
					got:      got,
					index:    idx,
					tenant:   "B",
				}
			}
		}(i)
	}

	wg.Wait()
	close(errors)

	// Check for errors
	for err := range errors {
		suite.Fail("Context leak detected", err.Error())
	}
}

// TestSessionVariableSetting tests that PostgreSQL session variables are correctly set
// via context-based approach (WithTenantID/WithUserID)
//
// Note: This test uses a non-transactional session because session variables are set
// per-connection by RLSSessionResetter. Within transactions, the session variables
// would not be re-set for each query.
func (suite *RLSIntegrationTestSuite) TestSessionVariableSetting() {
	// Create RLS-enabled session (non-transactional)
	config := NewConfig(
		WithEnvVars("writer"),
		WithRLS(true),
	)
	session, err := config.Connect()
	suite.Require().NoError(err)
	defer session.Close()

	db := session.DB()

	// Test setting tenant ID via context
	suite.Run("set_tenant_id", func() {
		tenantID := uuid.New()

		// Set tenant ID in context - session variable will be set automatically before query
		ctxWithTenant := rlsctx.WithTenantID(context.Background(), tenantID)

		// Verify it was set by querying current_setting
		var result string
		err := db.WithContext(ctxWithTenant).Raw("SELECT COALESCE(current_setting('app.current_tenant_id', true), '')").Scan(&result).Error
		suite.Require().NoError(err)
		suite.Equal(tenantID.String(), result)
	})

	// Test setting user ID via context
	suite.Run("set_user_id", func() {
		userID := uuid.New()

		// Set user ID in context - session variable will be set automatically before query
		ctxWithUser := rlsctx.WithUserID(context.Background(), userID)

		// Verify it was set by querying current_setting
		var result string
		err := db.WithContext(ctxWithUser).Raw("SELECT COALESCE(current_setting('app.current_user_id', true), '')").Scan(&result).Error
		suite.Require().NoError(err)
		suite.Equal(userID.String(), result)
	})

	// Test empty context results in empty session variables
	suite.Run("reset_session_var", func() {
		// First set a value via context
		tenantID := uuid.New()
		ctxWithTenant := rlsctx.WithTenantID(context.Background(), tenantID)

		var result string
		err := db.WithContext(ctxWithTenant).Raw("SELECT COALESCE(current_setting('app.current_tenant_id', true), '')").Scan(&result).Error
		suite.Require().NoError(err)
		suite.Equal(tenantID.String(), result)

		// Now query without tenant in context - should be empty
		ctxEmpty := context.Background()
		err = db.WithContext(ctxEmpty).Raw("SELECT COALESCE(current_setting('app.current_tenant_id', true), '')").Scan(&result).Error
		suite.Require().NoError(err)
		suite.Empty(result, "should be empty when context has no tenant ID")
	})
}

// TestGetCurrentTenantID tests the debug helper for getting current tenant ID
func (suite *RLSIntegrationTestSuite) TestGetCurrentTenantID() {
	// Create RLS-enabled session (non-transactional)
	config := NewConfig(
		WithEnvVars("writer"),
		WithRLS(true),
	)
	session, err := config.Connect()
	suite.Require().NoError(err)
	defer session.Close()

	// Set tenant ID via context
	tenantID := uuid.New()
	ctxWithTenant := rlsctx.WithTenantID(context.Background(), tenantID)

	// Use the debug helper - it will set session variable via context before querying
	got, err := session.GetCurrentTenantID(ctxWithTenant)
	suite.Require().NoError(err)
	suite.Equal(tenantID.String(), got)
}

// TestGetCurrentUserID tests the debug helper for getting current user ID
func (suite *RLSIntegrationTestSuite) TestGetCurrentUserID() {
	// Create RLS-enabled session (non-transactional)
	config := NewConfig(
		WithEnvVars("writer"),
		WithRLS(true),
	)
	session, err := config.Connect()
	suite.Require().NoError(err)
	defer session.Close()

	// Set user ID via context
	userID := uuid.New()
	ctxWithUser := rlsctx.WithUserID(context.Background(), userID)

	// Use the debug helper - it will set session variable via context before querying
	got, err := session.GetCurrentUserID(ctxWithUser)
	suite.Require().NoError(err)
	suite.Equal(userID.String(), got)
}

// TestRLSWithSuperuserSetup verifies RLS behavior when data is created by a superuser
// and accessed by regular users with RLS enforcement.
//
// Note: testSession (which uses transactions) is NOT suitable for RLS testing because:
// - RLS session variables are set per-connection, not per-transaction
// - Within a transaction, session variables set by RLSSessionResetter don't take effect
// - Therefore, we use separate sessions: superuser for setup, app_user for RLS verification
func (suite *RLSIntegrationTestSuite) TestRLSWithSuperuserSetup() {
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
		tenantID.String(), "Test Tenant for RLS",
	).Error
	suite.Require().NoError(err, "Superuser should be able to create tenant")

	// Step 2: Create a product using superuser
	productID := uuid.New()
	err = superuserSession.DB().WithContext(ctx).Exec(
		"INSERT INTO products (id, tenant_id, name, price, currency, created_at, updated_at) VALUES (?, ?, ?, ?, ?, NOW(), NOW())",
		productID.String(), tenantID.String(), "Test Product", 1000, "JPY",
	).Error
	suite.Require().NoError(err, "Superuser should be able to create product")

	// Cleanup
	defer func() {
		superuserSession.DB().WithContext(ctx).Exec("DELETE FROM products WHERE id = ?", productID.String())
		superuserSession.DB().WithContext(ctx).Exec("DELETE FROM tenants WHERE id = ?", tenantID.String())
	}()

	// Step 3: Verify product is visible with correct tenant context (RLS enforced)
	ctxWithTenant := rlsctx.WithTenantID(ctx, tenantID)
	var count int64
	err = rlsSession.DB().WithContext(ctxWithTenant).Table("products").Where("id = ?", productID.String()).Count(&count).Error
	suite.Require().NoError(err)
	suite.Equal(int64(1), count, "Product should be visible with correct tenant context")

	// Step 4: Verify product is NOT visible without tenant context (RLS blocks)
	err = rlsSession.DB().WithContext(ctx).Table("products").Where("id = ?", productID.String()).Count(&count).Error
	suite.Require().NoError(err)
	suite.Equal(int64(0), count, "Product should NOT be visible without tenant context (RLS)")

	// Step 5: Verify product is NOT visible with wrong tenant context
	wrongTenantID := uuid.New()
	ctxWrongTenant := rlsctx.WithTenantID(ctx, wrongTenantID)
	err = rlsSession.DB().WithContext(ctxWrongTenant).Table("products").Where("id = ?", productID.String()).Count(&count).Error
	suite.Require().NoError(err)
	suite.Equal(int64(0), count, "Product should NOT be visible with wrong tenant context")
}

type contextError struct {
	expected string
	got      string
	index    int
	tenant   string
}

func (e *contextError) Error() string {
	return "tenant " + e.tenant + ": expected " + e.expected + ", got " + e.got
}

// TestConcurrentRLSIsolation tests that RLS correctly isolates data
// when multiple goroutines access the database concurrently with different tenant contexts.
// This verifies that SessionResetter correctly sets session variables for each connection.
//
// IMPORTANT: This test creates fresh sessions (not using testSession's transaction)
// because SessionResetter is only called when obtaining a connection from the pool,
// not for queries within an existing transaction.
func (suite *RLSIntegrationTestSuite) TestConcurrentRLSIsolation() {
	ctx := context.Background()

	// Create superuser session for setup/cleanup (SUPERUSER bypasses RLS at database level)
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

	// Create RLS-enabled session for the actual test (uses regular app user)
	// Limit connection pool to avoid exhausting PostgreSQL max_connections during concurrent tests
	rlsConfig := NewConfig(
		WithEnvVars("writer"),
		WithRLS(true),
		WithConnectionPool(5, 2, 0),
	)
	rlsSession, err := rlsConfig.Connect()
	suite.Require().NoError(err)
	defer rlsSession.Close()

	// Generate unique IDs for test data
	testTenantID := uuid.New()
	testProduct1ID := uuid.New()
	testProduct2ID := uuid.New()
	testProduct3ID := uuid.New()

	// Setup: Create test tenant and products using RLS-bypassed session
	suite.T().Log("Setting up test data with RLS-bypassed session")

	// Create tenant
	err = superuserSession.DB().WithContext(ctx).Exec(
		"INSERT INTO tenants (id, name, created_at, updated_at) VALUES (?, ?, NOW(), NOW())",
		testTenantID.String(), "Test Tenant for RLS Concurrent Test",
	).Error
	suite.Require().NoError(err, "Failed to create test tenant")

	// Create products (price and currency are required fields)
	for i, productID := range []uuid.UUID{testProduct1ID, testProduct2ID, testProduct3ID} {
		err = superuserSession.DB().WithContext(ctx).Exec(
			"INSERT INTO products (id, tenant_id, name, price, currency, created_at, updated_at) VALUES (?, ?, ?, ?, ?, NOW(), NOW())",
			productID.String(), testTenantID.String(), fmt.Sprintf("Test Product %d", i+1), 1000*(i+1), "JPY",
		).Error
		suite.Require().NoError(err, "Failed to create test product %d", i+1)
	}

	// Cleanup: Ensure test data is removed after test (using RLS-bypassed session)
	defer func() {
		suite.T().Log("Cleaning up test data with RLS-bypassed session")
		// Delete products first (foreign key constraint)
		_ = superuserSession.DB().WithContext(ctx).Exec(
			"DELETE FROM products WHERE tenant_id = ?", testTenantID.String(),
		).Error
		// Delete tenant
		_ = superuserSession.DB().WithContext(ctx).Exec(
			"DELETE FROM tenants WHERE id = ?", testTenantID.String(),
		).Error
	}()

	// Concurrent test - verify RLS isolation works under concurrent load
	const goroutines = 50
	var wg sync.WaitGroup
	errors := make(chan string, goroutines*2)

	for i := range goroutines {
		wg.Add(2)

		// Goroutine with correct tenant context - should see products
		go func(idx int) {
			defer wg.Done()

			// Create context with test tenant
			ctxWithTenant := rlsctx.WithTenantID(context.Background(), testTenantID)

			// Query products - should see all 3 test products
			var products []struct {
				ID       string `gorm:"column:id"`
				TenantID string `gorm:"column:tenant_id"`
				Name     string `gorm:"column:name"`
			}

			err := rlsSession.DB().WithContext(ctxWithTenant).
				Table("products").
				Select("id, tenant_id, name").
				Where("id IN (?, ?, ?)", testProduct1ID.String(), testProduct2ID.String(), testProduct3ID.String()).
				Scan(&products).Error
			if err != nil {
				errors <- fmt.Sprintf("goroutine %d (with tenant): query error: %v", idx, err)
				return
			}

			// Verify all 3 products are visible
			if len(products) != 3 {
				errors <- fmt.Sprintf("goroutine %d (with tenant): expected 3 products, got %d", idx, len(products))
				return
			}

			// Verify all products belong to the correct tenant
			for _, p := range products {
				if p.TenantID != testTenantID.String() {
					errors <- fmt.Sprintf("goroutine %d (with tenant): wrong tenant_id, expected %s, got %s", idx, testTenantID.String(), p.TenantID)
				}
			}
		}(i)

		// Goroutine without tenant context - should see NO products (RLS blocks)
		go func(idx int) {
			defer wg.Done()

			// Create context WITHOUT tenant (simulates missing auth)
			ctxNoTenant := context.Background()

			// Query products - should see 0 products due to RLS
			var products []struct {
				ID       string `gorm:"column:id"`
				TenantID string `gorm:"column:tenant_id"`
				Name     string `gorm:"column:name"`
			}

			err := rlsSession.DB().WithContext(ctxNoTenant).
				Table("products").
				Select("id, tenant_id, name").
				Where("id IN (?, ?, ?)", testProduct1ID.String(), testProduct2ID.String(), testProduct3ID.String()).
				Scan(&products).Error
			if err != nil {
				errors <- fmt.Sprintf("goroutine %d (no tenant): query error: %v", idx, err)
				return
			}

			// Verify NO products are visible (RLS blocks access)
			if len(products) != 0 {
				errors <- fmt.Sprintf("goroutine %d (no tenant): expected 0 products, got %d - RLS NOT WORKING!", idx, len(products))
			}
		}(i)
	}

	wg.Wait()
	close(errors)

	// Check for errors
	var errorList []string
	for err := range errors {
		errorList = append(errorList, err)
	}

	if len(errorList) > 0 {
		suite.Fail("RLS isolation failed in concurrent access", "Errors:\n%v", errorList)
	}
}

func TestRLSContextTestSuite(t *testing.T) {
	suite.Run(t, new(RLSContextTestSuite))
}

func TestRLSIntegrationTestSuite(t *testing.T) {
	suite.Run(t, new(RLSIntegrationTestSuite))
}

// TestSetRLSContext tests that testSession.SetRLSContext() works correctly
// with transactions using SET LOCAL (is_local=true).
// This is the recommended approach for most business logic tests.
func TestSetRLSContext(t *testing.T) {
	config := NewConfig(WithEnvVars("writer"))
	ts := NewTestSession(t, config)
	defer ts.Cleanup()

	// Create test tenant (within transaction, will be rolled back)
	// Note: tenants table has no RLS policy, so no context needed
	tenantID := uuid.New()
	err := ts.DB().Exec(
		"INSERT INTO tenants (id, name, created_at, updated_at) VALUES (?, ?, NOW(), NOW())",
		tenantID.String(), "Test Tenant for SetRLSContext",
	).Error
	if err != nil {
		t.Fatalf("failed to create tenant: %v", err)
	}

	// Set RLS context BEFORE creating product (products table has RLS policy)
	ctxWithTenant := ts.SetRLSContext(
		map[string]string{"app.current_tenant_id": tenantID.String()},
		func(ctx context.Context, name, value string) context.Context {
			if name == "app.current_tenant_id" {
				return rlsctx.WithTenantID(ctx, uuid.MustParse(value))
			}
			return ctx
		},
	)

	// Create test product
	productID := uuid.New()
	err = ts.DB().WithContext(ctxWithTenant).Exec(
		"INSERT INTO products (id, tenant_id, name, price, currency, created_at, updated_at) VALUES (?, ?, ?, ?, 'JPY', NOW(), NOW())",
		productID.String(), tenantID.String(), "Test Product", 1000,
	).Error
	if err != nil {
		t.Fatalf("failed to create product: %v", err)
	}

	// Test 1: SetRLSContext sets session variable correctly
	t.Run("sets_session_variable", func(t *testing.T) {
		ctxWithRLS := ts.SetRLSContext(
			map[string]string{"app.current_tenant_id": tenantID.String()},
			func(ctx context.Context, name, value string) context.Context {
				if name == "app.current_tenant_id" {
					return rlsctx.WithTenantID(ctx, uuid.MustParse(value))
				}
				return ctx
			},
		)

		var result string
		err := ts.DB().WithContext(ctxWithRLS).Raw("SELECT COALESCE(current_setting('app.current_tenant_id', true), '')").Scan(&result).Error
		if err != nil {
			t.Fatalf("failed to query session variable: %v", err)
		}
		if result != tenantID.String() {
			t.Errorf("expected tenant_id %s, got %s", tenantID.String(), result)
		}
	})

	// Test 2: RLS policy works with SET LOCAL
	t.Run("rls_policy_works", func(t *testing.T) {
		ctxWithRLS := ts.SetRLSContext(
			map[string]string{"app.current_tenant_id": tenantID.String()},
			func(ctx context.Context, name, value string) context.Context {
				if name == "app.current_tenant_id" {
					return rlsctx.WithTenantID(ctx, uuid.MustParse(value))
				}
				return ctx
			},
		)

		var count int64
		err := ts.DB().WithContext(ctxWithRLS).Table("products").Where("id = ?", productID.String()).Count(&count).Error
		if err != nil {
			t.Fatalf("failed to query products: %v", err)
		}
		if count != 1 {
			t.Errorf("expected 1 product, got %d", count)
		}
	})

	// Test 3: Wrong tenant can't see product
	t.Run("wrong_tenant_blocked", func(t *testing.T) {
		wrongTenantID := uuid.New()
		ctxWrongTenant := ts.SetRLSContext(
			map[string]string{"app.current_tenant_id": wrongTenantID.String()},
			func(ctx context.Context, name, value string) context.Context {
				if name == "app.current_tenant_id" {
					return rlsctx.WithTenantID(ctx, uuid.MustParse(value))
				}
				return ctx
			},
		)

		var count int64
		err := ts.DB().WithContext(ctxWrongTenant).Table("products").Where("id = ?", productID.String()).Count(&count).Error
		if err != nil {
			t.Fatalf("failed to query products: %v", err)
		}
		if count != 0 {
			t.Errorf("expected 0 products with wrong tenant, got %d", count)
		}
	})

	// Test 4: Context carries tenant_id for application code
	t.Run("context_carries_tenant_id", func(t *testing.T) {
		ctxWithRLS := ts.SetRLSContext(
			map[string]string{"app.current_tenant_id": tenantID.String()},
			func(ctx context.Context, name, value string) context.Context {
				if name == "app.current_tenant_id" {
					return rlsctx.WithTenantID(ctx, uuid.MustParse(value))
				}
				return ctx
			},
		)

		gotTenantID, ok := rlsctx.GetTenantIDFromContext(ctxWithRLS)
		if !ok {
			t.Error("expected tenant_id in context, got none")
		}
		if gotTenantID != tenantID.String() {
			t.Errorf("expected tenant_id %s in context, got %s", tenantID.String(), gotTenantID)
		}
	})
}
