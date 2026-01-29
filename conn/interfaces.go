// Package conn provides generic database session and connection management.
// It is designed to be reusable across multiple Go projects.
package conn

import "context"

// SpanStarter is a function that starts a span for SQL operations.
// It returns a finish function that should be called when the operation completes.
type SpanStarter func(ctx context.Context, operationType, query string, role string) (finish func(err error), newCtx context.Context)
