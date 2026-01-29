// Package rlsctx provides context helpers for Row Level Security (RLS) session variables.
// It provides functions to store and retrieve tenant/user IDs from context,
// which can then be used by rlsconn to set PostgreSQL session variables.
package rlsctx

import (
	"context"

	"github.com/google/uuid"
)

// Context keys for RLS session variables (package-private)
type (
	tenantIDKey struct{}
	userIDKey   struct{}
)

// WithTenantID sets the tenant ID in the context for RLS.
// The tenantID is stored as a string representation of the UUID.
func WithTenantID(ctx context.Context, tenantID uuid.UUID) context.Context {
	return context.WithValue(ctx, tenantIDKey{}, tenantID.String())
}

// GetTenantIDFromContext retrieves the tenant ID from the context.
// Returns (tenantID, true) if set, ("", false) otherwise.
func GetTenantIDFromContext(ctx context.Context) (string, bool) {
	tenantID, ok := ctx.Value(tenantIDKey{}).(string)
	return tenantID, ok
}

// WithUserID sets the user ID in the context for RLS.
// The userID is stored as a string representation of the UUID.
func WithUserID(ctx context.Context, userID uuid.UUID) context.Context {
	return context.WithValue(ctx, userIDKey{}, userID.String())
}

// GetUserIDFromContext retrieves the user ID from the context.
// Returns (userID, true) if set, ("", false) otherwise.
func GetUserIDFromContext(ctx context.Context) (string, bool) {
	userID, ok := ctx.Value(userIDKey{}).(string)
	return userID, ok
}
