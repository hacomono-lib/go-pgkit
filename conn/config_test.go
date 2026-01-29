package conn_test

import (
	"testing"

	"github.com/hacomono-lib/go-pgkit/conn"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/suite"
)

type SessionConfigTestSuite struct {
	suite.Suite
}

func TestSessionConfigTestSuite(t *testing.T) {
	suite.Run(t, new(SessionConfigTestSuite))
}

func (s *SessionConfigTestSuite) TestNewConfig() {
	tests := []struct {
		name     string
		options  []conn.ConfigOption
		validate func(t *testing.T, cfg *conn.Config)
	}{
		{
			name:    "default config",
			options: []conn.ConfigOption{},
			validate: func(t *testing.T, cfg *conn.Config) {
				cfgStr := cfg.String()
				assert.Contains(t, cfgStr, "maxIdleConns:2") // default value
			},
		},
		{
			name: "host and port",
			options: []conn.ConfigOption{
				conn.WithHost("localhost"),
				conn.WithPort(5432),
			},
			validate: func(t *testing.T, cfg *conn.Config) {
				cfgStr := cfg.String()
				assert.Contains(t, cfgStr, "host:localhost")
				assert.Contains(t, cfgStr, "port:5432")
			},
		},
		{
			name: "credentials",
			options: []conn.ConfigOption{
				conn.WithCredentials("testuser", "testpass"),
			},
			validate: func(t *testing.T, cfg *conn.Config) {
				cfgStr := cfg.String()
				assert.Contains(t, cfgStr, "user:testuser")
				// Password should not appear in string representation
				assert.NotContains(t, cfgStr, "testpass")
			},
		},
		{
			name: "database name",
			options: []conn.ConfigOption{
				conn.WithDatabase("testdb"),
			},
			validate: func(t *testing.T, cfg *conn.Config) {
				cfgStr := cfg.String()
				assert.Contains(t, cfgStr, "dbname:testdb")
			},
		},
		{
			name: "silent log level",
			options: []conn.ConfigOption{
				conn.WithSilentGormLogger(),
			},
			validate: func(t *testing.T, cfg *conn.Config) {
				// WithSilentGormLogger sets LogLevel to Silent
				assert.NotNil(t, cfg.LogLevel, "LogLevel should be set by WithSilentGormLogger")
			},
		},
		{
			name: "connection pool",
			options: []conn.ConfigOption{
				conn.WithConnectionPool(100, 10, 30),
			},
			validate: func(t *testing.T, cfg *conn.Config) {
				cfgStr := cfg.String()
				assert.Contains(t, cfgStr, "maxOpenConns:100")
				assert.Contains(t, cfgStr, "maxIdleConns:10")
				assert.Contains(t, cfgStr, "connMaxLifetime:30")
			},
		},
		{
			name: "connection pool with maxIdle=0 uses default",
			options: []conn.ConfigOption{
				conn.WithConnectionPool(100, 0, 30),
			},
			validate: func(t *testing.T, cfg *conn.Config) {
				cfgStr := cfg.String()
				assert.Contains(t, cfgStr, "maxOpenConns:100")
				assert.Contains(t, cfgStr, "maxIdleConns:2") // default value
				assert.Contains(t, cfgStr, "connMaxLifetime:30")
			},
		},
		{
			name: "multiple options combined",
			options: []conn.ConfigOption{
				conn.WithHost("localhost"),
				conn.WithPort(5432),
				conn.WithCredentials("user", "pass"),
				conn.WithDatabase("mydb"),
				conn.WithConnectionPool(50, 5, 60),
			},
			validate: func(t *testing.T, cfg *conn.Config) {
				cfgStr := cfg.String()
				assert.Contains(t, cfgStr, "host:localhost")
				assert.Contains(t, cfgStr, "port:5432")
				assert.Contains(t, cfgStr, "user:user")
				assert.Contains(t, cfgStr, "dbname:mydb")
				assert.Contains(t, cfgStr, "maxOpenConns:50")
				assert.Contains(t, cfgStr, "maxIdleConns:5")
				assert.Contains(t, cfgStr, "connMaxLifetime:60")
			},
		},
		{
			name: "connect timeout",
			options: []conn.ConfigOption{
				conn.WithConnectTimeout(30),
			},
			validate: func(t *testing.T, cfg *conn.Config) {
				assert.Equal(t, 30, cfg.ConnectTimeout)
			},
		},
		{
			name:    "default connect timeout",
			options: []conn.ConfigOption{},
			validate: func(t *testing.T, cfg *conn.Config) {
				assert.Equal(t, 10, cfg.ConnectTimeout)
			},
		},
		{
			name: "disable tracing",
			options: []conn.ConfigOption{
				conn.WithoutTrace(),
			},
			validate: func(t *testing.T, cfg *conn.Config) {
				assert.Nil(t, cfg.GormOpener)
				assert.Nil(t, cfg.GormCallbackRegistrar)
				assert.Nil(t, cfg.SpanStarter)
			},
		},
	}

	for _, tt := range tests {
		s.Run(tt.name, func() {
			cfg := conn.NewConfig(tt.options...)
			tt.validate(s.T(), cfg)
		})
	}
}

func (s *SessionConfigTestSuite) TestWithEnvVars() {
	envVars := map[string]string{
		"DB_HOST":              "envhost",
		"DB_PORT":              "5433",
		"DB_USER":              "envuser",
		"DB_PASSWORD":          "envpass",
		"DB_NAME":              "envdb",
		"DB_MAX_OPEN_CONNS":    "200",
		"DB_MAX_IDLE_CONNS":    "20",
		"DB_CONN_MAX_LIFETIME": "120",
	}

	for key, value := range envVars {
		s.T().Setenv(key, value)
	}

	cfg := conn.NewConfig(
		conn.WithEnvVars(""),
	)

	cfgStr := cfg.String()
	assert.Contains(s.T(), cfgStr, "host:envhost")
	assert.Contains(s.T(), cfgStr, "port:5433")
	assert.Contains(s.T(), cfgStr, "user:envuser")
	assert.Contains(s.T(), cfgStr, "dbname:envdb")
	assert.Contains(s.T(), cfgStr, "maxOpenConns:200")
	assert.Contains(s.T(), cfgStr, "maxIdleConns:20")
	assert.Contains(s.T(), cfgStr, "connMaxLifetime:120")
}

func (s *SessionConfigTestSuite) TestWithEnvVarsDefaultValues() {
	envVars := []string{
		"DB_HOST", "DB_PORT", "DB_USER", "DB_PASSWORD", "DB_NAME",
		"DB_MAX_OPEN_CONNS", "DB_MAX_IDLE_CONNS", "DB_CONN_MAX_LIFETIME",
	}
	for _, key := range envVars {
		s.T().Setenv(key, "")
	}

	cfg := conn.NewConfig(conn.WithEnvVars(""))

	cfgStr := cfg.String()
	assert.Contains(s.T(), cfgStr, "maxIdleConns:2")
	assert.Contains(s.T(), cfgStr, "maxOpenConns:0")
	assert.Contains(s.T(), cfgStr, "connMaxLifetime:0")
}

func (s *SessionConfigTestSuite) TestWithEnvVarsRoleSpecific() {
	s.Run("load env vars with writer role", func() {
		baseVars := map[string]string{
			"DB_HOST":              "base-host",
			"DB_PORT":              "5432",
			"DB_USER":              "base-user",
			"DB_PASSWORD":          "base-pass",
			"DB_NAME":              "base-db",
			"DB_MAX_OPEN_CONNS":    "50",
			"DB_MAX_IDLE_CONNS":    "5",
			"DB_CONN_MAX_LIFETIME": "30",
		}
		writerVars := map[string]string{
			"DB_WRITER_HOST":              "writer-host",
			"DB_WRITER_PORT":              "5433",
			"DB_WRITER_USER":              "writer-user",
			"DB_WRITER_PASSWORD":          "writer-pass",
			"DB_WRITER_MAX_OPEN_CONNS":    "100",
			"DB_WRITER_MAX_IDLE_CONNS":    "10",
			"DB_WRITER_CONN_MAX_LIFETIME": "60",
		}

		for key, value := range baseVars {
			s.T().Setenv(key, value)
		}
		for key, value := range writerVars {
			s.T().Setenv(key, value)
		}

		cfg := conn.NewConfig(
			conn.WithEnvVars("writer"),
		)

		cfgStr := cfg.String()
		// Writer-specific values should take precedence
		assert.Contains(s.T(), cfgStr, "role:writer")
		assert.Contains(s.T(), cfgStr, "host:writer-host")
		assert.Contains(s.T(), cfgStr, "port:5433")
		assert.Contains(s.T(), cfgStr, "user:writer-user")
		assert.Contains(s.T(), cfgStr, "maxOpenConns:100")
		assert.Contains(s.T(), cfgStr, "maxIdleConns:10")
		assert.Contains(s.T(), cfgStr, "connMaxLifetime:60")
	})

	s.Run("load env vars with reader role", func() {
		baseVars := map[string]string{
			"DB_HOST":     "base-host",
			"DB_PORT":     "5432",
			"DB_USER":     "base-user",
			"DB_PASSWORD": "base-pass",
			"DB_NAME":     "base-db",
		}
		readerVars := map[string]string{
			"DB_READER_HOST":     "reader-host",
			"DB_READER_PORT":     "5434",
			"DB_READER_USER":     "reader-user",
			"DB_READER_PASSWORD": "reader-pass",
		}

		for key, value := range baseVars {
			s.T().Setenv(key, value)
		}
		for key, value := range readerVars {
			s.T().Setenv(key, value)
		}

		cfg := conn.NewConfig(
			conn.WithEnvVars("reader"),
		)

		cfgStr := cfg.String()
		// Reader-specific values should take precedence
		assert.Contains(s.T(), cfgStr, "role:reader")
		assert.Contains(s.T(), cfgStr, "host:reader-host")
		assert.Contains(s.T(), cfgStr, "port:5434")
		assert.Contains(s.T(), cfgStr, "user:reader-user")
	})

	s.Run("falls back to base values when role-specific vars are absent", func() {
		// Clear role-specific env vars
		s.T().Setenv("DB_WRITER_HOST", "")
		s.T().Setenv("DB_WRITER_PORT", "")
		s.T().Setenv("DB_WRITER_USER", "")

		// Set base env vars
		s.T().Setenv("DB_HOST", "base-host")
		s.T().Setenv("DB_PORT", "5432")
		s.T().Setenv("DB_USER", "base-user")
		s.T().Setenv("DB_PASSWORD", "base-pass")
		s.T().Setenv("DB_NAME", "base-db")

		// Load with writer role but no writer-specific vars — should use base values
		cfg := conn.NewConfig(
			conn.WithEnvVars("writer"),
		)

		cfgStr := cfg.String()
		assert.Contains(s.T(), cfgStr, "role:writer")
		assert.Contains(s.T(), cfgStr, "host:base-host")
		assert.Contains(s.T(), cfgStr, "port:5432")
		assert.Contains(s.T(), cfgStr, "user:base-user")
	})

	s.Run("reads only base env vars when role is empty", func() {
		baseVars := map[string]string{
			"DB_HOST": "base-host",
			"DB_PORT": "5432",
		}
		writerVars := map[string]string{
			"DB_WRITER_HOST": "writer-host",
			"DB_WRITER_PORT": "5433",
		}

		for key, value := range baseVars {
			s.T().Setenv(key, value)
		}
		for key, value := range writerVars {
			s.T().Setenv(key, value)
		}

		cfg := conn.NewConfig(
			conn.WithEnvVars(""),
		)

		cfgStr := cfg.String()
		// Base values should be used (role-specific values are not applied)
		assert.Contains(s.T(), cfgStr, "host:base-host")
		assert.Contains(s.T(), cfgStr, "port:5432")
	})
}

func (s *SessionConfigTestSuite) TestWithEnvVarsEdgeCases() {
	s.Run("MaxRetries=0 is settable via env var", func() {
		s.T().Setenv("DB_MAX_RETRIES", "0")
		s.T().Setenv("DB_HOST", "localhost")

		cfg := conn.NewConfig(conn.WithEnvVars(""))

		assert.Equal(s.T(), 0, cfg.MaxRetries, "MaxRetries should be 0 when DB_MAX_RETRIES=0")
	})

	s.Run("RetryBackoff=0 is settable via env var", func() {
		s.T().Setenv("DB_RETRY_BACKOFF_MS", "0")

		cfg := conn.NewConfig(conn.WithEnvVars(""))

		assert.Equal(s.T(), 0, cfg.RetryBackoff, "RetryBackoff should be 0 when DB_RETRY_BACKOFF_MS=0")
	})

	s.Run("GracefulCloseTimeout=0 is settable via env var", func() {
		s.T().Setenv("DB_GRACEFUL_CLOSE_TIMEOUT", "0")

		cfg := conn.NewConfig(conn.WithEnvVars(""))

		assert.Equal(s.T(), 0, cfg.GracefulCloseTimeout, "GracefulCloseTimeout should be 0 when DB_GRACEFUL_CLOSE_TIMEOUT=0")
	})

	s.Run("MaxIdleConns=0 is settable via env var", func() {
		s.T().Setenv("DB_MAX_IDLE_CONNS", "0")

		cfg := conn.NewConfig(conn.WithEnvVars(""))

		assert.Equal(s.T(), 0, cfg.MaxIdleConns, "MaxIdleConns should be 0 when DB_MAX_IDLE_CONNS=0")
	})

	s.Run("ConnectTimeout is settable via env var", func() {
		s.T().Setenv("DB_CONNECT_TIMEOUT", "30")

		cfg := conn.NewConfig(conn.WithEnvVars(""))

		assert.Equal(s.T(), 30, cfg.ConnectTimeout, "ConnectTimeout should be 30 when DB_CONNECT_TIMEOUT=30")
	})

	s.Run("ConnectTimeout role override", func() {
		s.T().Setenv("DB_CONNECT_TIMEOUT", "10")
		s.T().Setenv("DB_WRITER_CONNECT_TIMEOUT", "5")

		cfg := conn.NewConfig(conn.WithEnvVars("writer"))

		assert.Equal(s.T(), 5, cfg.ConnectTimeout, "Writer role should override base ConnectTimeout")
	})

	s.Run("unset env vars preserve defaults", func() {
		// Ensure all DB_ env vars are unset
		for _, key := range []string{
			"DB_HOST", "DB_PORT", "DB_USER", "DB_PASSWORD", "DB_NAME",
			"DB_MAX_OPEN_CONNS", "DB_MAX_IDLE_CONNS", "DB_CONN_MAX_LIFETIME",
			"DB_MAX_RETRIES", "DB_RETRY_BACKOFF_MS", "DB_GRACEFUL_CLOSE_TIMEOUT",
			"DB_CONNECT_TIMEOUT", "DB_ENABLE_RETRY", "DB_SSLMODE",
		} {
			s.T().Setenv(key, "")
		}

		cfg := conn.NewConfig(conn.WithEnvVars(""))

		// Defaults from newDefaultConfig should be preserved
		assert.Equal(s.T(), 2, cfg.MaxIdleConns, "MaxIdleConns default should be preserved")
		assert.Equal(s.T(), true, cfg.EnableRetry, "EnableRetry default should be preserved")
		assert.Equal(s.T(), 3, cfg.MaxRetries, "MaxRetries default should be preserved")
		assert.Equal(s.T(), 100, cfg.RetryBackoff, "RetryBackoff default should be preserved")
		assert.Equal(s.T(), 10, cfg.GracefulCloseTimeout, "GracefulCloseTimeout default should be preserved")
		assert.Equal(s.T(), 10, cfg.ConnectTimeout, "ConnectTimeout default should be preserved")
	})

	s.Run("WithEnvVars does not overwrite prior options when env vars are unset", func() {
		// Ensure DB_PORT is unset
		s.T().Setenv("DB_PORT", "")
		s.T().Setenv("DB_HOST", "")

		cfg := conn.NewConfig(
			conn.WithHost("myhost"),
			conn.WithPort(9999),
			conn.WithEnvVars(""), // should not overwrite host/port since env vars are unset
		)

		cfgStr := cfg.String()
		assert.Contains(s.T(), cfgStr, "host:myhost", "WithHost should not be overwritten by unset env var")
		assert.Contains(s.T(), cfgStr, "port:9999", "WithPort should not be overwritten by unset env var")
	})

	s.Run("empty role does not generate double-underscore key", func() {
		// Set a key with double underscore to verify it's NOT picked up
		s.T().Setenv("DB__HOST", "should-not-be-used")
		s.T().Setenv("DB_HOST", "correct-host")

		cfg := conn.NewConfig(conn.WithEnvVars(""))

		cfgStr := cfg.String()
		assert.Contains(s.T(), cfgStr, "host:correct-host", "Should use DB_HOST, not DB__HOST")
	})
}

func (s *SessionConfigTestSuite) TestWithEnvVarsWithPrefix() {
	s.Run("custom prefix reads correct env vars", func() {
		s.T().Setenv("MYAPP_HOST", "custom-host")
		s.T().Setenv("MYAPP_PORT", "9999")
		s.T().Setenv("MYAPP_USER", "custom-user")
		s.T().Setenv("MYAPP_PASSWORD", "custom-pass")
		s.T().Setenv("MYAPP_NAME", "custom-db")
		s.T().Setenv("MYAPP_MAX_OPEN_CONNS", "50")

		cfg := conn.NewConfig(conn.WithEnvVarsWithPrefix("", "MYAPP_"))

		cfgStr := cfg.String()
		assert.Contains(s.T(), cfgStr, "host:custom-host")
		assert.Contains(s.T(), cfgStr, "port:9999")
		assert.Contains(s.T(), cfgStr, "user:custom-user")
		assert.Contains(s.T(), cfgStr, "dbname:custom-db")
		assert.Contains(s.T(), cfgStr, "maxOpenConns:50")
	})

	s.Run("custom prefix with role override", func() {
		s.T().Setenv("MYAPP_HOST", "base-host")
		s.T().Setenv("MYAPP_WRITER_HOST", "writer-host")
		s.T().Setenv("MYAPP_PORT", "5432")
		s.T().Setenv("MYAPP_WRITER_PORT", "5433")

		cfg := conn.NewConfig(conn.WithEnvVarsWithPrefix("writer", "MYAPP_"))

		cfgStr := cfg.String()
		assert.Contains(s.T(), cfgStr, "role:writer")
		assert.Contains(s.T(), cfgStr, "host:writer-host")
		assert.Contains(s.T(), cfgStr, "port:5433")
	})

	s.Run("custom prefix falls back to base when role var is absent", func() {
		s.T().Setenv("MYAPP_HOST", "base-host")
		s.T().Setenv("MYAPP_WRITER_HOST", "")

		cfg := conn.NewConfig(conn.WithEnvVarsWithPrefix("writer", "MYAPP_"))

		cfgStr := cfg.String()
		assert.Contains(s.T(), cfgStr, "host:base-host")
	})

	s.Run("custom prefix does not read DB_ vars", func() {
		s.T().Setenv("DB_HOST", "db-host")
		s.T().Setenv("CUSTOM_HOST", "custom-host")

		cfg := conn.NewConfig(conn.WithEnvVarsWithPrefix("", "CUSTOM_"))

		cfgStr := cfg.String()
		assert.Contains(s.T(), cfgStr, "host:custom-host")
		assert.NotContains(s.T(), cfgStr, "host:db-host")
	})

	s.Run("custom prefix preserves defaults when unset", func() {
		// Ensure MYAPP_ vars are not set
		s.T().Setenv("MYAPP_MAX_IDLE_CONNS", "")
		s.T().Setenv("MYAPP_MAX_RETRIES", "")

		cfg := conn.NewConfig(conn.WithEnvVarsWithPrefix("", "MYAPP_"))

		assert.Equal(s.T(), 2, cfg.MaxIdleConns)
		assert.Equal(s.T(), 3, cfg.MaxRetries)
	})
}

func (s *SessionConfigTestSuite) TestNewConfigWithEnvVars() {
	envVars := map[string]string{
		"DB_HOST":              "localhost",
		"DB_PORT":              "5432",
		"DB_USER":              "user",
		"DB_PASSWORD":          "pass",
		"DB_NAME":              "testdb",
		"DB_MAX_OPEN_CONNS":    "100",
		"DB_MAX_IDLE_CONNS":    "10",
		"DB_CONN_MAX_LIFETIME": "60",
	}

	for key, value := range envVars {
		s.T().Setenv(key, value)
	}

	cfg := conn.NewConfig(
		conn.WithEnvVars(""),
	)

	cfgStr := cfg.String()
	assert.Contains(s.T(), cfgStr, "host:localhost")
	assert.Contains(s.T(), cfgStr, "port:5432")
	assert.Contains(s.T(), cfgStr, "user:user")
	assert.Contains(s.T(), cfgStr, "dbname:testdb")
	assert.Contains(s.T(), cfgStr, "maxOpenConns:100")
	assert.Contains(s.T(), cfgStr, "maxIdleConns:10")
	assert.Contains(s.T(), cfgStr, "connMaxLifetime:60")
}

func (s *SessionConfigTestSuite) TestConfigFunctionalOptions() {
	s.Run("later options override earlier ones", func() {
		cfg := conn.NewConfig(
			conn.WithHost("host1"),
			conn.WithHost("host2"), // later option takes precedence
			conn.WithPort(5432),
			conn.WithPort(5433), // later option takes precedence
		)

		cfgStr := cfg.String()
		assert.Contains(s.T(), cfgStr, "host:host2")
		assert.Contains(s.T(), cfgStr, "port:5433")
	})

	s.Run("partial config", func() {
		cfg := conn.NewConfig(
			conn.WithHost("localhost"),
			conn.WithDatabase("mydb"),
		)

		cfgStr := cfg.String()
		assert.Contains(s.T(), cfgStr, "host:localhost")
		assert.Contains(s.T(), cfgStr, "dbname:mydb")
		assert.Contains(s.T(), cfgStr, "maxIdleConns:2") // default value
	})

	s.Run("WithSilentGormLogger log config", func() {
		s.Run("sets silent log level", func() {
			cfg := conn.NewConfig(
				conn.WithHost("localhost"),
				conn.WithSilentGormLogger(),
			)

			assert.NotNil(s.T(), cfg)
			cfgStr := cfg.String()
			assert.Contains(s.T(), cfgStr, "host:localhost")
			assert.NotNil(s.T(), cfg.LogLevel, "LogLevel should be set by WithSilentGormLogger")
		})

		s.Run("creates config without silent log level", func() {
			cfg := conn.NewConfig(
				conn.WithHost("localhost"),
			)

			assert.NotNil(s.T(), cfg)
			cfgStr := cfg.String()
			assert.Contains(s.T(), cfgStr, "host:localhost")
		})
	})
}

func ExampleNewConfig() {
	// Basic usage
	_ = conn.NewConfig(
		conn.WithHost("localhost"),
		conn.WithPort(5432),
		conn.WithCredentials("user", "password"),
		conn.WithDatabase("mydb"),
	)

	// Load from environment variables (writer role) + silent logging for production
	_ = conn.NewConfig(
		conn.WithEnvVars("writer"),
		conn.WithSilentGormLogger(),
	)

	// Test configuration
	_ = conn.NewConfig(
		conn.WithHost("localhost"),
		conn.WithPort(5432),
		conn.WithDatabase("test_db"),
		conn.WithConnectionPool(10, 2, 30),
	)

	// Disable tracing for high-frequency SQL operations
	_ = conn.NewConfig(
		conn.WithEnvVars("writer"),
		conn.WithSilentGormLogger(),
		conn.WithoutTrace(),
	)
}
