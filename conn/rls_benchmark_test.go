package conn

import (
	"context"
	"fmt"
	"os"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/hacomono-lib/go-pgkit/rlsctx"
)

// Note: Full RLS overhead benchmark (comparing queries with and without RLS) requires
// superuser access to create test data. Use TestRLSLatencyDistribution for that purpose.
//
// For quick overhead measurement, use BenchmarkRLSSetConfigOnly which measures
// just the set_config() call overhead.

// BenchmarkRLSSetConfigOnly measures the overhead of just the set_config call.
func BenchmarkRLSSetConfigOnly(b *testing.B) {
	if os.Getenv("DB_HOST") == "" {
		b.Skip("DB_HOST not set, skipping benchmark")
	}

	config := NewConfig(
		WithEnvVars("writer"),
		WithRLS(false),
	)

	session, err := config.Connect()
	if err != nil {
		b.Fatalf("failed to connect: %v", err)
	}
	defer session.Close()

	tenantID := uuid.New().String()
	userID := uuid.New().String()

	b.Run("SingleSetConfig", func(b *testing.B) {
		b.ResetTimer()
		for i := 0; i < b.N; i++ {
			if err := session.DB().Exec(
				"SELECT set_config($1, $2, false)",
				"app.current_tenant_id", tenantID,
			).Error; err != nil {
				b.Fatalf("set_config failed: %v", err)
			}
		}
	})

	b.Run("DoubleSetConfig_Combined", func(b *testing.B) {
		b.ResetTimer()
		for i := 0; i < b.N; i++ {
			if err := session.DB().Exec(
				"SELECT set_config($1, $2, false), set_config($3, $4, false)",
				"app.current_tenant_id", tenantID,
				"app.current_user_id", userID,
			).Error; err != nil {
				b.Fatalf("set_config failed: %v", err)
			}
		}
	})
}

// TestRLSLatencyDistribution runs a latency distribution test to understand
// the real-world impact of RLS overhead.
func TestRLSLatencyDistribution(t *testing.T) {
	if os.Getenv("DB_HOST") == "" {
		t.Skip("DB_HOST not set, skipping test")
	}

	if testing.Short() {
		t.Skip("skipping latency distribution test in short mode")
	}

	// Setup sessions
	baseConfig := NewConfig(
		WithEnvVars("writer"),
		WithRLS(false),
	)

	rlsConfig := NewConfig(
		WithEnvVars("writer"),
		WithRLS(true),
	)

	baseSession, err := baseConfig.Connect()
	if err != nil {
		t.Fatalf("failed to connect base session: %v", err)
	}
	defer baseSession.Close()

	rlsSession, err := rlsConfig.Connect()
	if err != nil {
		t.Fatalf("failed to connect RLS session: %v", err)
	}
	defer rlsSession.Close()

	// Create test data
	tenantID := uuid.New()
	productID := uuid.New()

	superConfig := NewConfig(
		WithEnvVars("writer"),
		WithCredentials("migration_user", "migration_password"),
		WithRLS(false),
	)
	superSession, err := superConfig.Connect()
	if err != nil {
		t.Fatalf("failed to connect super session: %v", err)
	}
	defer superSession.Close()

	// Set tenant context for superSession because FORCE ROW LEVEL SECURITY
	// applies RLS policies even to superusers
	ctxSuper := rlsctx.WithTenantID(context.Background(), tenantID)

	if err := superSession.DB().WithContext(ctxSuper).Exec(`
		INSERT INTO tenants (id, name, created_at, updated_at)
		VALUES (?, 'Latency Test Tenant', NOW(), NOW())
		ON CONFLICT (id) DO NOTHING
	`, tenantID).Error; err != nil {
		t.Fatalf("failed to insert tenant: %v", err)
	}

	if err := superSession.DB().WithContext(ctxSuper).Exec(`
		INSERT INTO products (id, tenant_id, name, price, currency, created_at, updated_at)
		VALUES (?, ?, 'Latency Test Product', 1000, 'JPY', NOW(), NOW())
		ON CONFLICT (id) DO NOTHING
	`, productID, tenantID).Error; err != nil {
		t.Fatalf("failed to insert product: %v", err)
	}

	defer func() {
		superSession.DB().WithContext(ctxSuper).Exec("DELETE FROM products WHERE id = ?", productID)
		superSession.DB().WithContext(ctxSuper).Exec("DELETE FROM tenants WHERE id = ?", tenantID)
	}()

	ctx := context.Background()
	ctxWithTenant := rlsctx.WithTenantID(ctx, tenantID)

	const iterations = 1000

	// Measure baseline
	baseLatencies := make([]time.Duration, iterations)
	for i := 0; i < iterations; i++ {
		start := time.Now()
		var count int64
		if err := baseSession.DB().WithContext(ctx).Table("products").Where("tenant_id = ?", tenantID).Count(&count).Error; err != nil {
			t.Fatalf("baseline query failed: %v", err)
		}
		baseLatencies[i] = time.Since(start)
	}

	// Measure RLS
	rlsLatencies := make([]time.Duration, iterations)
	for i := 0; i < iterations; i++ {
		start := time.Now()
		var count int64
		if err := rlsSession.DB().WithContext(ctxWithTenant).Table("products").Count(&count).Error; err != nil {
			t.Fatalf("RLS query failed: %v", err)
		}
		rlsLatencies[i] = time.Since(start)
	}

	// Calculate statistics
	baseStats := calculateStats(baseLatencies)
	rlsStats := calculateStats(rlsLatencies)

	t.Logf("\n=== RLS Latency Distribution (%d iterations) ===\n", iterations)
	t.Logf("Baseline (No RLS):")
	t.Logf("  Mean:   %v", baseStats.mean)
	t.Logf("  Median: %v", baseStats.median)
	t.Logf("  P95:    %v", baseStats.p95)
	t.Logf("  P99:    %v", baseStats.p99)
	t.Logf("  Min:    %v", baseStats.min)
	t.Logf("  Max:    %v", baseStats.max)
	t.Logf("")
	t.Logf("With RLS (set_config per query):")
	t.Logf("  Mean:   %v", rlsStats.mean)
	t.Logf("  Median: %v", rlsStats.median)
	t.Logf("  P95:    %v", rlsStats.p95)
	t.Logf("  P99:    %v", rlsStats.p99)
	t.Logf("  Min:    %v", rlsStats.min)
	t.Logf("  Max:    %v", rlsStats.max)
	t.Logf("")
	t.Logf("Overhead:")
	t.Logf("  Mean:   +%v (%.1f%%)", rlsStats.mean-baseStats.mean, float64(rlsStats.mean-baseStats.mean)/float64(baseStats.mean)*100)
	t.Logf("  Median: +%v (%.1f%%)", rlsStats.median-baseStats.median, float64(rlsStats.median-baseStats.median)/float64(baseStats.median)*100)
	t.Logf("  P95:    +%v (%.1f%%)", rlsStats.p95-baseStats.p95, float64(rlsStats.p95-baseStats.p95)/float64(baseStats.p95)*100)
}

type latencyStats struct {
	mean, median, p95, p99, min, max time.Duration
}

func calculateStats(latencies []time.Duration) latencyStats {
	if len(latencies) == 0 {
		return latencyStats{}
	}

	// Sort for percentiles
	sorted := make([]time.Duration, len(latencies))
	copy(sorted, latencies)
	for i := 0; i < len(sorted); i++ {
		for j := i + 1; j < len(sorted); j++ {
			if sorted[i] > sorted[j] {
				sorted[i], sorted[j] = sorted[j], sorted[i]
			}
		}
	}

	var sum time.Duration
	for _, d := range latencies {
		sum += d
	}

	n := len(sorted)
	return latencyStats{
		mean:   sum / time.Duration(n),
		median: sorted[n/2],
		p95:    sorted[n*95/100],
		p99:    sorted[n*99/100],
		min:    sorted[0],
		max:    sorted[n-1],
	}
}

// TestPrintRLSOverheadSummary prints a human-readable summary of RLS overhead.
func TestPrintRLSOverheadSummary(t *testing.T) {
	if os.Getenv("DB_HOST") == "" {
		t.Skip("DB_HOST not set, skipping test")
	}

	if testing.Short() {
		t.Skip("skipping in short mode")
	}

	config := NewConfig(
		WithEnvVars("writer"),
		WithRLS(false),
	)

	session, err := config.Connect()
	if err != nil {
		t.Fatalf("failed to connect: %v", err)
	}
	defer session.Close()

	tenantID := uuid.New().String()
	userID := uuid.New().String()

	const iterations = 100

	// Measure just set_config
	var setConfigTotal time.Duration
	for i := 0; i < iterations; i++ {
		start := time.Now()
		if err := session.DB().Exec(
			"SELECT set_config($1, $2, false), set_config($3, $4, false)",
			"app.current_tenant_id", tenantID,
			"app.current_user_id", userID,
		).Error; err != nil {
			t.Fatalf("set_config failed: %v", err)
		}
		setConfigTotal += time.Since(start)
	}
	setConfigAvg := setConfigTotal / iterations

	// Measure SELECT 1 (minimal query)
	var selectOneTotal time.Duration
	for i := 0; i < iterations; i++ {
		start := time.Now()
		if err := session.DB().Exec("SELECT 1").Error; err != nil {
			t.Fatalf("SELECT 1 failed: %v", err)
		}
		selectOneTotal += time.Since(start)
	}
	selectOneAvg := selectOneTotal / iterations

	fmt.Printf("\n")
	fmt.Printf("=== RLS Overhead Summary (%d iterations) ===\n", iterations)
	fmt.Printf("\n")
	fmt.Printf("set_config (2 vars, combined): %v per call\n", setConfigAvg)
	fmt.Printf("SELECT 1 (baseline):           %v per call\n", selectOneAvg)
	fmt.Printf("\n")
	fmt.Printf("Overhead ratio: %.2fx\n", float64(setConfigAvg)/float64(selectOneAvg))
	fmt.Printf("\n")
	fmt.Printf("Estimated impact:\n")
	fmt.Printf("  - 1 query/request:   +%v latency\n", setConfigAvg)
	fmt.Printf("  - 10 queries/request: +%v latency\n", setConfigAvg*10)
	fmt.Printf("  - 100 queries/request: +%v latency\n", setConfigAvg*100)
	fmt.Printf("\n")
}
