package conn

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
)

// When GORM fails to open the session (e.g., its ping fails), the connection pool it created is closed,
// with or without the session's own connector.
func TestSession_ClosesDBWhenOpenFails(t *testing.T) {
	tests := []struct {
		name string
		opts []ConfigOption
	}{
		{name: "with connector", opts: nil},
		{name: "without connector", opts: []ConfigOption{WithRetry(false, 0, 0), WithRLS(false)}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			env := NewConfig(WithEnvVars("writer"))
			var opened *gorm.DB
			opener := func(dialector gorm.Dialector, config *gorm.Config) (*gorm.DB, error) {
				db, err := DefaultGormOpener(dialector, config)
				opened = db
				return db, err
			}
			// The test database rejects a wrong password, so GORM's ping fails.
			cfg := NewConfig(append([]ConfigOption{
				WithEnvVars("writer"), WithCredentials(env.User, "wrong"), WithGormOpener(opener),
			}, tt.opts...)...)

			_, err := cfg.Connect()
			require.Error(t, err)

			// gorm.Open returns the *gorm.DB with its connection pool even when the ping fails.
			require.NotNil(t, opened)
			sqlDB, err := opened.DB()
			require.NoError(t, err)
			assert.EqualError(t, sqlDB.Ping(), "sql: database is closed")
		})
	}
}
