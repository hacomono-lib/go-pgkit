package conn

import (
	"regexp"
	"testing"

	"github.com/jackc/pgx/v5/pgconn"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestConfigDSN_PreservesSpecialCharacters(t *testing.T) {
	tests := []struct {
		name     string
		password string
	}{
		{name: "plain", password: "app_password"},
		{name: "space", password: "pass word"},
		{name: "single quote", password: "pass'word"},
		{name: "backslash", password: `pass\word`},
		{name: "trailing backslash", password: `password\`},
		{name: "escaped quote sequence", password: `pass\'word`},
		{name: "equals sign", password: "pass=word"},
		{name: "mixed", password: `p a\s's\\word`},
		{name: "time zone keyword", password: "TimeZone=x"},
		{name: "lowercase time zone keyword", password: "timezone=x"},
		{name: "snake case time zone keyword", password: "time_zone=x"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Setenv("DB_TIMEZONE", "Asia/Tokyo")
			t.Setenv("DB_SCHEMA", "my schema")
			cfg := NewConfig(
				WithHost("localhost"),
				WithPort(5432),
				WithCredentials("app user", tt.password),
				WithDatabase("pgkit_test"),
			)

			parsed, err := pgconn.ParseConfig(cfg.dsn())
			require.NoError(t, err)

			assert.Equal(t, tt.password, parsed.Password)
			assert.Equal(t, "app user", parsed.User)
			assert.Equal(t, "localhost", parsed.Host)
			assert.Equal(t, uint16(5432), parsed.Port)
			assert.Equal(t, "pgkit_test", parsed.Database)
			assert.Equal(t, "Asia/Tokyo", parsed.RuntimeParams["TimeZone"])
			assert.Equal(t, "my schema", parsed.RuntimeParams["search_path"])

			match := gormTimeZoneMatcher.FindStringSubmatch(cfg.dsn())
			require.Len(t, match, 4)
			assert.Equal(t, "Asia/Tokyo", match[2])
		})
	}
}

// gormTimeZoneMatcher is the regex gorm.io/driver/postgres uses to read the time zone from the raw DSN
// when no custom driver is set.
var gormTimeZoneMatcher = regexp.MustCompile("(time_zone|TimeZone|timezone)=(.*?)($|&| )")

func TestConfigConnect_PasswordWithSpecialCharacters(t *testing.T) {
	tests := []struct {
		name string
		opts []ConfigOption
	}{
		{name: "pgxretry driver", opts: nil},
		{name: "gorm default driver", opts: []ConfigOption{WithRetry(false, 0, 0), WithRLS(false)}},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Setenv("DB_TIMEZONE", "Asia/Tokyo")
			opts := append([]ConfigOption{
				WithEnvVars("writer"),
				WithCredentials("special_password_user", `timezone=x p a\s's\word\`),
			}, tt.opts...)

			session, err := NewConfig(opts...).Connect()
			require.NoError(t, err)
			t.Cleanup(func() { _ = session.Close() })

			var got struct {
				CurrentUser string
				TimeZone    string
			}
			require.NoError(t, session.DB().Raw("SELECT current_user AS current_user, current_setting('TimeZone') AS time_zone").Scan(&got).Error)
			assert.Equal(t, "special_password_user", got.CurrentUser)
			assert.Equal(t, "Asia/Tokyo", got.TimeZone)
		})
	}
}
