package conn

import "github.com/hacomono-lib/go-errorsx"

// Error types for database operations
var (
	TypeConnection     errorsx.ErrorType = "db.connection"
	TypeTransaction    errorsx.ErrorType = "db.transaction"
	TypeInitialization errorsx.ErrorType = "db.initialization"

	ErrConnection     = errorsx.New("database connection error", errorsx.WithType(TypeConnection), errorsx.WithHTTPStatus(500))
	ErrTransaction    = errorsx.New("database transaction error", errorsx.WithType(TypeTransaction), errorsx.WithHTTPStatus(500))
	ErrInitialization = errorsx.New("database initialization error", errorsx.WithType(TypeInitialization), errorsx.WithHTTPStatus(500))
)
