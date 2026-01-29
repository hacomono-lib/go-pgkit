package rlsconn

import (
	"context"
	"database/sql/driver"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// mockConn is a mock driver.Conn for testing
type mockConn struct {
	execQueries []execCall
	closed      bool
}

type execCall struct {
	query string
	args  []driver.NamedValue
}

func (m *mockConn) Prepare(query string) (driver.Stmt, error) {
	return nil, nil
}

func (m *mockConn) Close() error {
	m.closed = true
	return nil
}

func (m *mockConn) Begin() (driver.Tx, error) {
	return nil, nil
}

// ExecContext implements driver.ExecerContext
func (m *mockConn) ExecContext(ctx context.Context, query string, args []driver.NamedValue) (driver.Result, error) {
	m.execQueries = append(m.execQueries, execCall{query: query, args: args})
	return &mockResult{}, nil
}

type mockResult struct{}

func (m *mockResult) LastInsertId() (int64, error) { return 0, nil }
func (m *mockResult) RowsAffected() (int64, error) { return 0, nil }

// Context key for testing
type (
	tenantIDKey struct{}
	userIDKey   struct{}
)

func TestRLSSessionResetter_ResetSession(t *testing.T) {
	tests := []struct {
		name          string
		sessionVars   []SessionVar
		ctxSetup      func(context.Context) context.Context
		expectedQuery string
		expectedArgs  []driver.NamedValue
	}{
		{
			name: "sets tenant ID when present",
			sessionVars: []SessionVar{
				{
					Name: "app.current_tenant_id",
					ValueGetter: func(ctx context.Context) (string, bool) {
						v, ok := ctx.Value(tenantIDKey{}).(string)
						return v, ok
					},
				},
			},
			ctxSetup: func(ctx context.Context) context.Context {
				return context.WithValue(ctx, tenantIDKey{}, "tenant-123")
			},
			expectedQuery: "SELECT set_config($1, $2, false)",
			expectedArgs: []driver.NamedValue{
				{Ordinal: 1, Value: "app.current_tenant_id"},
				{Ordinal: 2, Value: "tenant-123"},
			},
		},
		{
			name: "resets tenant ID when not present",
			sessionVars: []SessionVar{
				{
					Name: "app.current_tenant_id",
					ValueGetter: func(ctx context.Context) (string, bool) {
						v, ok := ctx.Value(tenantIDKey{}).(string)
						return v, ok
					},
				},
			},
			ctxSetup: func(ctx context.Context) context.Context {
				return ctx // No tenant ID set
			},
			expectedQuery: "SELECT set_config($1, NULL, false)",
			expectedArgs: []driver.NamedValue{
				{Ordinal: 1, Value: "app.current_tenant_id"},
			},
		},
		{
			name: "sets multiple session vars in a single query",
			sessionVars: []SessionVar{
				{
					Name: "app.current_tenant_id",
					ValueGetter: func(ctx context.Context) (string, bool) {
						v, ok := ctx.Value(tenantIDKey{}).(string)
						return v, ok
					},
				},
				{
					Name: "app.current_user_id",
					ValueGetter: func(ctx context.Context) (string, bool) {
						v, ok := ctx.Value(userIDKey{}).(string)
						return v, ok
					},
				},
			},
			ctxSetup: func(ctx context.Context) context.Context {
				ctx = context.WithValue(ctx, tenantIDKey{}, "tenant-123")
				ctx = context.WithValue(ctx, userIDKey{}, "user-456")
				return ctx
			},
			// Multiple set_config calls combined into a single query for performance
			expectedQuery: "SELECT set_config($1, $2, false), set_config($3, $4, false)",
			expectedArgs: []driver.NamedValue{
				{Ordinal: 1, Value: "app.current_tenant_id"},
				{Ordinal: 2, Value: "tenant-123"},
				{Ordinal: 3, Value: "app.current_user_id"},
				{Ordinal: 4, Value: "user-456"},
			},
		},
		{
			name: "handles mixed set and reset in a single query",
			sessionVars: []SessionVar{
				{
					Name: "app.current_tenant_id",
					ValueGetter: func(ctx context.Context) (string, bool) {
						v, ok := ctx.Value(tenantIDKey{}).(string)
						return v, ok
					},
				},
				{
					Name: "app.current_user_id",
					ValueGetter: func(ctx context.Context) (string, bool) {
						v, ok := ctx.Value(userIDKey{}).(string)
						return v, ok
					},
				},
			},
			ctxSetup: func(ctx context.Context) context.Context {
				// Only set tenant ID, not user ID
				return context.WithValue(ctx, tenantIDKey{}, "tenant-123")
			},
			// tenant_id gets SET, user_id gets RESET (NULL)
			expectedQuery: "SELECT set_config($1, $2, false), set_config($3, NULL, false)",
			expectedArgs: []driver.NamedValue{
				{Ordinal: 1, Value: "app.current_tenant_id"},
				{Ordinal: 2, Value: "tenant-123"},
				{Ordinal: 3, Value: "app.current_user_id"},
			},
		},
		{
			name:        "no session vars results in no query",
			sessionVars: []SessionVar{},
			ctxSetup: func(ctx context.Context) context.Context {
				return ctx
			},
			expectedQuery: "", // No query should be executed
			expectedArgs:  nil,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// Arrange
			mockConn := &mockConn{}
			config := &Config{SessionVars: tt.sessionVars}
			resetter := NewRLSSessionResetter(mockConn, config)

			ctx := tt.ctxSetup(context.Background())

			// Act
			err := resetter.ResetSession(ctx)

			// Assert
			require.NoError(t, err)

			if tt.expectedQuery == "" {
				// No query should be executed
				assert.Empty(t, mockConn.execQueries)
			} else {
				// Exactly one query should be executed (all vars combined)
				require.Len(t, mockConn.execQueries, 1)
				assert.Equal(t, tt.expectedQuery, mockConn.execQueries[0].query)
				assert.Equal(t, tt.expectedArgs, mockConn.execQueries[0].args)
			}
		})
	}
}

func TestRLSSessionResetter_DelegatesMethods(t *testing.T) {
	mockConn := &mockConn{}
	config := &Config{}
	resetter := NewRLSSessionResetter(mockConn, config)

	// Test Close delegates
	err := resetter.Close()
	assert.NoError(t, err)
	assert.True(t, mockConn.closed)
}

func TestRLSSessionResetter_IsValid(t *testing.T) {
	t.Run("returns true when base conn doesn't implement Validator", func(t *testing.T) {
		mockConn := &mockConn{}
		resetter := NewRLSSessionResetter(mockConn, &Config{})

		assert.True(t, resetter.IsValid())
	})

	t.Run("delegates to base conn when it implements Validator", func(t *testing.T) {
		mockConn := &mockConnWithValidator{valid: false}
		resetter := NewRLSSessionResetter(mockConn, &Config{})

		assert.False(t, resetter.IsValid())
	})
}

// mockConnWithValidator implements driver.Validator
type mockConnWithValidator struct {
	mockConn
	valid bool
}

func (m *mockConnWithValidator) IsValid() bool {
	return m.valid
}
