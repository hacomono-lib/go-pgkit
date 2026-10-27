package conn

import (
	"context"
	"testing"

	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/hacomono-lib/go-pgkit/rlsctx"
)

// With retry disabled, the session does not go through pgxretry,
// so MaxRetries is not raised to pgxretry's default.
func TestSessionConnector_RetryDisabledSkipsPgxretry(t *testing.T) {
	s := newSession(NewConfig(WithEnvVars("writer"), WithRetry(false, 0, 0), WithRLS(true)))
	assert.Nil(t, s.retryConfig)
	assert.NotNil(t, s.rlsConfig)
}

// Each session uses its own connector, so RLS is applied only to sessions with RLS enabled,
// whether or not retry is enabled and whichever session connects first.
func TestSessionConnector_RLSPerSession(t *testing.T) {
	tests := []struct {
		name string
		opts []ConfigOption
	}{
		{name: "retry enabled", opts: nil},
		{name: "retry disabled", opts: []ConfigOption{WithRetry(false, 0, 0)}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// Connects first, without RLS.
			noRLS, err := NewConfig(append([]ConfigOption{WithEnvVars("writer"), WithRLS(false)}, tt.opts...)...).Connect()
			require.NoError(t, err)
			t.Cleanup(func() { _ = noRLS.Close() })

			rls, err := NewConfig(append([]ConfigOption{WithEnvVars("writer"), WithRLS(true)}, tt.opts...)...).Connect()
			require.NoError(t, err)
			t.Cleanup(func() { _ = rls.Close() })

			tenantID := uuid.New()
			ctx := rlsctx.WithTenantID(context.Background(), tenantID)

			got, err := rls.GetCurrentTenantID(ctx)
			require.NoError(t, err)
			assert.Equal(t, tenantID.String(), got)

			// The RLS session variables are not set on the session without RLS.
			got, err = noRLS.GetCurrentTenantID(ctx)
			require.NoError(t, err)
			assert.Empty(t, got)
		})
	}
}
