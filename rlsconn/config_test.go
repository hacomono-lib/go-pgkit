package rlsconn

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestValidateSessionVarName(t *testing.T) {
	tests := []struct {
		name    string
		varName string
		wantErr bool
	}{
		// Valid names
		{name: "valid app namespace", varName: "app.current_tenant_id", wantErr: false},
		{name: "valid rls namespace", varName: "rls.organization_id", wantErr: false},
		{name: "valid with numbers", varName: "app.tenant_123", wantErr: false},
		{name: "valid with underscore prefix", varName: "_custom.var_name", wantErr: false},

		// Invalid names - potential SQL injection
		{name: "empty name", varName: "", wantErr: true},
		{name: "no namespace", varName: "tenant_id", wantErr: true},
		{name: "SQL injection attempt", varName: "app.foo; DROP TABLE users; --", wantErr: true},
		{name: "SQL injection with quotes", varName: "app.foo'", wantErr: true},
		{name: "SQL injection with double quotes", varName: `app.foo"`, wantErr: true},
		{name: "contains spaces", varName: "app.current tenant", wantErr: true},
		{name: "contains dash", varName: "app.current-tenant", wantErr: true},
		{name: "starts with number", varName: "123app.tenant", wantErr: true},
		{name: "namespace starts with number", varName: "app.123tenant", wantErr: true},
		{name: "double dot", varName: "app..tenant", wantErr: true},
		{name: "trailing dot", varName: "app.tenant.", wantErr: true},
		{name: "leading dot", varName: ".app.tenant", wantErr: true},
		{name: "multiple namespaces", varName: "app.sub.tenant", wantErr: true},
		{name: "semicolon", varName: "app.tenant;id", wantErr: true},
		{name: "parentheses", varName: "app.tenant()", wantErr: true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := ValidateSessionVarName(tt.varName)
			if tt.wantErr {
				assert.Error(t, err, "expected error for %q", tt.varName)
			} else {
				assert.NoError(t, err, "unexpected error for %q", tt.varName)
			}
		})
	}
}

func TestConfig_Validate(t *testing.T) {
	validGetter := func(ctx context.Context) (string, bool) { return "value", true }

	tests := []struct {
		name    string
		config  *Config
		wantErr bool
	}{
		{
			name: "valid config",
			config: &Config{
				SessionVars: []SessionVar{
					{Name: "app.tenant_id", ValueGetter: validGetter},
					{Name: "app.user_id", ValueGetter: validGetter},
				},
			},
			wantErr: false,
		},
		{
			name: "empty session vars",
			config: &Config{
				SessionVars: []SessionVar{},
			},
			wantErr: false,
		},
		{
			name: "invalid session var name",
			config: &Config{
				SessionVars: []SessionVar{
					{Name: "invalid", ValueGetter: validGetter},
				},
			},
			wantErr: true,
		},
		{
			name: "nil value getter",
			config: &Config{
				SessionVars: []SessionVar{
					{Name: "app.tenant_id", ValueGetter: nil},
				},
			},
			wantErr: true,
		},
		{
			name: "SQL injection in name",
			config: &Config{
				SessionVars: []SessionVar{
					{Name: "app.foo'; DROP TABLE users; --", ValueGetter: validGetter},
				},
			},
			wantErr: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := tt.config.Validate()
			if tt.wantErr {
				assert.Error(t, err)
			} else {
				assert.NoError(t, err)
			}
		})
	}
}

func TestNewRLSConnectorWithValidation(t *testing.T) {
	t.Run("nil config returns error", func(t *testing.T) {
		_, err := NewRLSConnectorWithValidation(nil, nil)
		require.Error(t, err)
		assert.Contains(t, err.Error(), "config cannot be nil")
	})

	t.Run("invalid config returns error", func(t *testing.T) {
		invalidConfig := &Config{
			SessionVars: []SessionVar{
				{Name: "invalid_no_namespace", ValueGetter: func(ctx context.Context) (string, bool) { return "", false }},
			},
		}
		_, err := NewRLSConnectorWithValidation(nil, invalidConfig)
		require.Error(t, err)
		assert.Contains(t, err.Error(), "config validation failed")
	})
}
