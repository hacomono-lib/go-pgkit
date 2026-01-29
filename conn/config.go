package conn

import (
	"fmt"
	"log/slog"
	"os"
	"strconv"
	"strings"

	"github.com/hacomono-lib/go-pgkit/rlsconn"
	"github.com/hacomono-lib/go-pgkit/rlsctx"
)

// Config holds database connection settings.
type Config struct {
	Role            string // "writer" or "reader"
	Host            string
	Port            int
	User            string
	Password        string
	DBName          string
	MaxOpenConns    int // Maximum open connections (0 = unlimited)
	MaxIdleConns    int // Maximum idle connections (default: 2)
	ConnMaxLifetime int // Maximum connection lifetime in minutes (0 = unlimited)

	// SSL settings
	SSLMode string // SSL mode (default: "disable")

	// Retry settings
	EnableRetry  bool // Enable retry on transient errors
	MaxRetries   int  // Maximum number of retries
	RetryBackoff int  // Retry backoff interval in milliseconds

	// Connection timeout settings
	ConnectTimeout int // TCP connection timeout in seconds (default: 10)

	// Connection reset settings
	GracefulCloseTimeout int // Graceful close timeout in seconds (0 = default 10s)

	// Log level settings
	LogLevel *LogLevel // GORM log level (nil defaults to Info)

	// RLS settings
	EnableRLS bool // Enable Row-Level Security (default: true)

	// RLSSessionVars is a list of session variables to set for RLS.
	// Example: []rlsconn.SessionVar{{Name: "app.current_tenant_id", ValueGetter: GetTenantIDFromContext}}
	RLSSessionVars []rlsconn.SessionVar

	// Logger is the slog logger for database operations.
	Logger *slog.Logger

	// GormOpener is an optional custom GORM opener (e.g., for DataDog APM tracing).
	// If nil, DefaultGormOpener is used.
	GormOpener GormOpener

	// GormCallbackRegistrar is an optional callback registrar (e.g., for DataDog APM resource naming).
	// If nil, no callbacks are registered.
	GormCallbackRegistrar GormCallbackRegistrar

	// SpanStarter is an optional span starter for QueryDB operations.
	// If nil, no spans are created.
	SpanStarter SpanStarter

	// envPrefix is the environment variable prefix used by WithEnvVars/WithEnvVarsWithPrefix.
	// Used by dsn() to read TIMEZONE and SCHEMA env vars with the correct prefix.
	// Defaults to "DB_".
	envPrefix string
}

// ConfigOption is a function type for configuring Config.
type ConfigOption func(*Config)

// String returns a string representation of Config (password is hidden).
func (c *Config) String() string {
	return fmt.Sprintf(
		"Config{role:%s, host:%s, user:%s, dbname:%s, port:%d, maxOpenConns:%d, maxIdleConns:%d, connMaxLifetime:%d, enableRetry:%v, maxRetries:%d, enableRLS:%v}",
		c.Role, c.Host, c.User, c.DBName, c.Port, c.MaxOpenConns, c.MaxIdleConns, c.ConnMaxLifetime, c.EnableRetry, c.MaxRetries, c.EnableRLS,
	)
}

func (c *Config) dsn() string {
	prefix := c.envPrefix
	if prefix == "" {
		prefix = defaultEnvPrefix
	}
	// Get timezone from environment variable, default to UTC
	tz := os.Getenv(prefix + "TIMEZONE")
	if tz == "" {
		tz = "UTC"
	}
	// Get schema from environment variable, default to DB_NAME
	schema := os.Getenv(prefix + "SCHEMA")
	if schema == "" {
		schema = c.DBName
	}
	sslMode := c.SSLMode
	if sslMode == "" {
		sslMode = "disable"
	}
	connectTimeout := c.ConnectTimeout
	if connectTimeout <= 0 {
		connectTimeout = 10
	}
	return fmt.Sprintf(
		"host=%s user=%s password=%s dbname=%s port=%d sslmode=%s TimeZone=%s connect_timeout=%d search_path=%s",
		c.Host, c.User, c.Password, c.DBName, c.Port, sslMode, tz, connectTimeout, schema,
	)
}

func (c *Config) Connect() (*Session, error) {
	session := newSession(c)
	return session.Connect()
}

// WithLogger sets the slog logger for database operations.
func (c *Config) WithLogger(logger *slog.Logger) *Config {
	c.Logger = logger
	return c
}

// newDefaultConfig creates a Config with default values.
func newDefaultConfig() *Config {
	return &Config{
		MaxIdleConns:         2,
		ConnectTimeout:       10, // 10 seconds
		EnableRetry:          true,
		MaxRetries:           3,
		RetryBackoff:         100, // 100 milliseconds
		GracefulCloseTimeout: 10,  // 10 seconds
		EnableRLS:            true,
		RLSSessionVars: []rlsconn.SessionVar{
			{
				Name:        "app.current_tenant_id",
				ValueGetter: rlsctx.GetTenantIDFromContext,
			},
			{
				Name:        "app.current_user_id",
				ValueGetter: rlsctx.GetUserIDFromContext,
			},
		},
	}
}

// NewConfig creates a new Config by applying the given options to default values.
//
// Examples:
//
//	// Basic usage
//	cfg := NewConfig(
//	    WithHost("localhost"),
//	    WithPort(5432),
//	    WithCredentials("user", "password"),
//	    WithDatabase("mydb"),
//	)
//
//	// Load from environment variables
//	cfg := NewConfig(WithEnvVars("writer"))
func NewConfig(opts ...ConfigOption) *Config {
	cfg := newDefaultConfig()

	for _, opt := range opts {
		opt(cfg)
	}

	return cfg
}

// WithRole sets the connection role ("writer" or "reader").
func WithRole(role string) ConfigOption {
	return func(c *Config) {
		c.Role = role
	}
}

// WithHost sets the database host.
func WithHost(host string) ConfigOption {
	return func(c *Config) {
		c.Host = host
	}
}

// WithPort sets the database port.
func WithPort(port int) ConfigOption {
	return func(c *Config) {
		c.Port = port
	}
}

// WithCredentials sets the database user and password.
func WithCredentials(user, password string) ConfigOption {
	return func(c *Config) {
		c.User = user
		c.Password = password
	}
}

// WithDatabase sets the database name.
func WithDatabase(dbName string) ConfigOption {
	return func(c *Config) {
		c.DBName = dbName
	}
}

// WithSSLMode sets the SSL mode for the database connection.
// Valid values: "disable", "require", "verify-ca", "verify-full", "prefer", "allow".
// Default: "disable".
func WithSSLMode(sslMode string) ConfigOption {
	return func(c *Config) {
		c.SSLMode = sslMode
	}
}

// WithConnectionPool configures connection pool settings.
// maxLifetime is in minutes.
func WithConnectionPool(maxOpen, maxIdle, maxLifetime int) ConfigOption {
	return func(c *Config) {
		c.MaxOpenConns = maxOpen
		if maxIdle > 0 {
			c.MaxIdleConns = maxIdle
		}
		c.ConnMaxLifetime = maxLifetime
	}
}

// WithRetry configures retry behavior.
func WithRetry(enableRetry bool, maxRetries, retryBackoffMs int) ConfigOption {
	return func(c *Config) {
		c.EnableRetry = enableRetry
		c.MaxRetries = maxRetries
		c.RetryBackoff = retryBackoffMs
	}
}

// WithConnectTimeout sets the TCP connection timeout in seconds.
// Default: 10 seconds.
func WithConnectTimeout(seconds int) ConfigOption {
	return func(c *Config) {
		c.ConnectTimeout = seconds
	}
}

// WithGracefulCloseTimeout sets the timeout for graceful connection close on reset.
func WithGracefulCloseTimeout(seconds int) ConfigOption {
	return func(c *Config) {
		c.GracefulCloseTimeout = seconds
	}
}

// WithSilentGormLogger disables GORM log output.
// Useful for production environments to suppress log noise.
func WithSilentGormLogger() ConfigOption {
	return WithLogLevel(LogLevelSilent)
}

// WithLogLevel sets the GORM log level.
// Valid values: LogLevelSilent, LogLevelError, LogLevelWarn, LogLevelInfo.
func WithLogLevel(level LogLevel) ConfigOption {
	return func(c *Config) {
		c.LogLevel = &level
	}
}

// WithRLS enables or disables Row-Level Security.
func WithRLS(enableRLS bool) ConfigOption {
	return func(c *Config) {
		c.EnableRLS = enableRLS
	}
}

// WithRLSSessionVars sets the RLS session variables.
func WithRLSSessionVars(vars []rlsconn.SessionVar) ConfigOption {
	return func(c *Config) {
		c.RLSSessionVars = vars
	}
}

// WithoutTrace disables all tracing hooks (GormOpener, GormCallbackRegistrar, SpanStarter).
func WithoutTrace() ConfigOption {
	return func(c *Config) {
		c.GormOpener = nil
		c.GormCallbackRegistrar = nil
		c.SpanStarter = nil
	}
}

// WithGormOpener sets a custom GORM opener function.
func WithGormOpener(opener GormOpener) ConfigOption {
	return func(c *Config) {
		c.GormOpener = opener
	}
}

// WithGormCallbackRegistrar sets a GORM callback registration function.
func WithGormCallbackRegistrar(registrar GormCallbackRegistrar) ConfigOption {
	return func(c *Config) {
		c.GormCallbackRegistrar = registrar
	}
}

// WithSpanStarter sets the span starter for QueryDB tracing.
func WithSpanStarter(starter SpanStarter) ConfigOption {
	return func(c *Config) {
		c.SpanStarter = starter
	}
}

// defaultEnvPrefix is the default environment variable prefix.
const defaultEnvPrefix = "DB_"

// WithEnvVars loads configuration from environment variables with the default "DB_" prefix.
// When a role is specified, role-specific variables (e.g., DB_WRITER_HOST) take precedence.
// Only non-empty environment variables override defaults; unset variables are ignored.
func WithEnvVars(role string) ConfigOption {
	return withEnvVars(role, defaultEnvPrefix)
}

// WithEnvVarsWithPrefix loads configuration from environment variables with a custom prefix.
// For example, WithEnvVarsWithPrefix("writer", "MYAPP_") reads MYAPP_HOST, MYAPP_WRITER_HOST, etc.
func WithEnvVarsWithPrefix(role, prefix string) ConfigOption {
	return withEnvVars(role, prefix)
}

func withEnvVars(role, prefix string) ConfigOption {
	return func(c *Config) {
		c.Role = role
		c.envPrefix = prefix

		if v := getEnvWithRole(prefix+"HOST", role, prefix); v != "" {
			c.Host = v
		}
		if v := getEnvWithRole(prefix+"PORT", role, prefix); v != "" {
			c.Port = atoi(v)
		}
		if v := getEnvWithRole(prefix+"USER", role, prefix); v != "" {
			c.User = v
		}
		if v := getEnvWithRole(prefix+"PASSWORD", role, prefix); v != "" {
			c.Password = v
		}
		if v := getEnvWithRole(prefix+"NAME", role, prefix); v != "" {
			c.DBName = v
		}
		if v := getEnvWithRole(prefix+"SSLMODE", role, prefix); v != "" {
			c.SSLMode = v
		}

		// Connection pool settings
		if v := getEnvWithRole(prefix+"MAX_OPEN_CONNS", role, prefix); v != "" {
			c.MaxOpenConns = atoi(v)
		}
		if v := getEnvWithRole(prefix+"MAX_IDLE_CONNS", role, prefix); v != "" {
			c.MaxIdleConns = atoi(v)
		}
		if v := getEnvWithRole(prefix+"CONN_MAX_LIFETIME", role, prefix); v != "" {
			c.ConnMaxLifetime = atoi(v)
		}

		// Retry settings
		if v := getEnvWithRole(prefix+"ENABLE_RETRY", role, prefix); v != "" {
			c.EnableRetry = v == "true"
		}
		if v := getEnvWithRole(prefix+"MAX_RETRIES", role, prefix); v != "" {
			c.MaxRetries = atoi(v)
		}
		if v := getEnvWithRole(prefix+"RETRY_BACKOFF_MS", role, prefix); v != "" {
			c.RetryBackoff = atoi(v)
		}

		// Connection timeout
		if v := getEnvWithRole(prefix+"CONNECT_TIMEOUT", role, prefix); v != "" {
			c.ConnectTimeout = atoi(v)
		}

		// Graceful close timeout
		if v := getEnvWithRole(prefix+"GRACEFUL_CLOSE_TIMEOUT", role, prefix); v != "" {
			c.GracefulCloseTimeout = atoi(v)
		}
	}
}

// getEnvWithRole retrieves an environment variable with role-based override.
// For example, getEnvWithRole("DB_HOST", "writer", "DB_") checks DB_WRITER_HOST first,
// then falls back to DB_HOST. When role is empty, only the base key is checked.
func getEnvWithRole(key, role, prefix string) string {
	if role != "" {
		roleKey := strings.Replace(key, prefix, prefix+strings.ToUpper(role)+"_", 1)
		if override := os.Getenv(roleKey); override != "" {
			return override
		}
	}
	return os.Getenv(key)
}

func atoi(s string) int {
	i, err := strconv.Atoi(s)
	if err != nil {
		return 0
	}
	return i
}
