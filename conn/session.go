package conn

import (
	"context"
	"database/sql"
	"database/sql/driver"
	"fmt"
	"log"
	"log/slog"
	"os"
	"sync"
	"time"

	"github.com/jmoiron/sqlx"
	"gorm.io/driver/postgres"
	"gorm.io/gorm"
	"gorm.io/gorm/logger"

	"github.com/hacomono-lib/go-pgkit/pgxretry"
	"github.com/hacomono-lib/go-pgkit/rlsconn"
)

// registerOnce ensures driver registration is called only once
var registerOnce sync.Once

type txKey struct{}

// Session provides database connection and transaction functionality.
type Session struct {
	role          string       // "writer" or "reader"
	logger        *slog.Logger // cached with connection_role attribute
	db            *gorm.DB
	queryDB       *QueryDB // sqlx wrapper for the same connection
	config        *Config
	resetMu       sync.RWMutex  // protects db/queryDB during ResetConnection
	lastResetTime time.Time     // timestamp of the last connection reset
	resetCooldown time.Duration // minimum interval between resets

	gormConfig  *gorm.Config
	retryConfig *pgxretry.Config // pgxretry driver configuration
	rlsConfig   *rlsconn.Config  // RLS connector configuration

	// gracefulCloseTimeout is the maximum wait time for in-flight queries
	// during connection reset (configurable for testing).
	gracefulCloseTimeout time.Duration
}

// String returns a string representation of the session.
func (s *Session) String() string {
	sqlDB, err := s.db.DB()
	stats := "unknown"
	if err == nil {
		dbStats := sqlDB.Stats()
		stats = fmt.Sprintf(
			"maxOpenConns:%d,idle:%d,inUse:%d",
			dbStats.MaxOpenConnections,
			dbStats.Idle,
			dbStats.InUse,
		)
	}
	return fmt.Sprintf("Session{role:%s, database:%s, stats:%s, config:%s}", s.role, s.db.Name(), stats, s.config.String())
}

// newSession creates a new database session from the given config.
func newSession(config *Config) *Session {
	// Configure GORM logger
	var loglevel logger.LogLevel
	logColor := true

	if config.LogLevel != nil {
		loglevel = *config.LogLevel
		if loglevel == logger.Silent {
			logColor = false
		}
	} else {
		// Default to Info
		loglevel = logger.Info
	}

	gormLogger := logger.New(log.New(os.Stderr, "\r\n", log.LstdFlags), logger.Config{
		SlowThreshold:             200 * time.Millisecond,
		LogLevel:                  loglevel,
		IgnoreRecordNotFoundError: false,
		Colorful:                  logColor,
	})

	gormConfig := &gorm.Config{
		Logger:         gormLogger,
		TranslateError: true,
		// PrepareStmt is intentionally left disabled (the default) to ensure
		// the request context is passed through database/sql to
		// SessionResetter.ResetSession(ctx) on every query.
	}

	var slogLogger *slog.Logger
	if config.Logger != nil {
		slogLogger = config.Logger.With("connection_role", config.Role)
	} else {
		slogLogger = slog.Default().With("connection_role", config.Role)
	}

	// Configure retry.
	// When RLS is enabled, pgxretry's ConnectorWrapper is used to inject rlsconn,
	// so retryConfig is created even if retry itself is disabled.
	var retryConfig *pgxretry.Config
	if config.EnableRetry || config.EnableRLS {
		retryConfig = &pgxretry.Config{
			MaxRetries:     config.MaxRetries,
			InitialBackoff: time.Duration(config.RetryBackoff) * time.Millisecond,
			BackoffFactor:  2.0,
			MaxBackoff:     5 * time.Second,
		}

		// When retry is explicitly disabled, set MaxRetries to 0
		if !config.EnableRetry {
			retryConfig.MaxRetries = 0
		}

		retryConfig.WithSlog(slogLogger)
	}

	// Configure RLS
	var rlsConfig *rlsconn.Config
	if config.EnableRLS && len(config.RLSSessionVars) > 0 {
		rlsConfig = &rlsconn.Config{
			SessionVars: config.RLSSessionVars,
		}

		rlsConfig.WithSlog(slogLogger)

		// Only set the debug hook when debug logging is enabled (zero overhead in production)
		if slogLogger.Enabled(context.Background(), slog.LevelDebug) {
			rlsConfig.OnExecQuery = func(ctx context.Context, query string, args []any) {
				slogLogger.DebugContext(ctx, "RLS session variables set",
					"query", query,
					"args", args,
				)
			}
		}
	}

	// Convert graceful close timeout to time.Duration
	gracefulCloseTimeout := time.Duration(config.GracefulCloseTimeout) * time.Second
	if gracefulCloseTimeout <= 0 {
		gracefulCloseTimeout = 10 * time.Second
	}

	session := &Session{
		role:                 config.Role,
		logger:               slogLogger,
		config:               config,
		gormConfig:           gormConfig,
		retryConfig:          retryConfig,
		rlsConfig:            rlsConfig,
		resetCooldown:        1 * time.Second,
		gracefulCloseTimeout: gracefulCloseTimeout,
	}

	return session
}

func (s *Session) Connect() (*Session, error) {
	db, sqlDB, err := s.connect()
	if err != nil {
		return nil, err
	}

	s.db = db
	s.queryDB = &QueryDB{
		extContext:  sqlx.NewDb(sqlDB, "postgres"),
		role:        s.role,
		SpanStarter: s.config.SpanStarter,
	}

	return s, nil
}

// Logger returns the logger for database operations.
func (s *Session) Logger() *slog.Logger {
	return s.logger
}

// Role returns the session role.
func (s *Session) Role() string {
	return s.role
}

// GetTx retrieves the transaction from the context.
// Returns an error if no transaction is set.
func (s *Session) GetTx(ctx context.Context) (*gorm.DB, error) {
	tx, ok := ctx.Value(txKey{}).(*gorm.DB)
	if !ok {
		return nil, ErrTransaction.WithReason("[%s] transaction not set in context", s.role).WithCallerStack()
	}
	return tx, nil
}

// HasTx checks whether a transaction is set in the context.
func (s *Session) HasTx(ctx context.Context) bool {
	_, ok := ctx.Value(txKey{}).(*gorm.DB)
	return ok
}

// DB returns the GORM DB instance for use outside transactions.
// Note: the returned instance may reference a stale connection after ResetConnection.
func (s *Session) DB() *gorm.DB {
	if s == nil {
		return nil
	}
	s.resetMu.RLock()
	db := s.db
	s.resetMu.RUnlock()
	return db
}

// QueryDB returns the QueryDB instance for raw SQL operations.
func (s *Session) QueryDB() *QueryDB {
	if s == nil {
		return nil
	}
	s.resetMu.RLock()
	queryDB := s.queryDB
	s.resetMu.RUnlock()
	return queryDB
}

// Transaction starts a transaction and executes fn within it.
// On success, the transaction is committed. On error, it is rolled back.
// On panic, the transaction is rolled back and the panic is re-raised.
//
// This method wraps GORM's transaction management to provide:
//  1. Stack traces via errorsx
//  2. Detailed error information (including rollback failures)
//  3. Application-specific context management
func (s *Session) Transaction(ctx context.Context, fn func(txCtx context.Context) error) error {
	// Snapshot the db pointer under read lock
	s.resetMu.RLock()
	db := s.db
	s.resetMu.RUnlock()

	if db == nil {
		return ErrTransaction.WithReason("[%s] database not connected or reset", s.role).WithCallerStack()
	}

	tx := db.WithContext(ctx).Begin()
	if tx.Error != nil {
		return ErrTransaction.WithReason("[%s] failed to begin transaction", s.role).WithCause(tx.Error)
	}

	// Embed the transaction in the context
	txCtx := context.WithValue(ctx, txKey{}, tx)

	defer func() {
		if r := recover(); r != nil {
			if rbErr := tx.Rollback().Error; rbErr != nil {
				panic(ErrTransaction.WithReason("[%s] panic in transaction AND rollback failed: %v", s.role, r).WithCause(rbErr))
			}
			panic(ErrTransaction.WithReason("[%s] %v", s.role, r).WithCallerStack())
		}
	}()

	if err := fn(txCtx); err != nil {
		if rbErr := tx.Rollback().Error; rbErr != nil {
			return ErrTransaction.WithReason("[%s] transaction failed and rollback failed: %v", s.role, rbErr).WithCause(err)
		}
		return err // Return original error as-is (may already be wrapped)
	}

	if err := tx.Commit().Error; err != nil {
		return ErrTransaction.WithReason("[%s] failed to commit transaction", s.role).WithCause(err)
	}

	return nil
}

// GetPoolStats returns connection pool statistics for monitoring/debugging.
func (s *Session) GetPoolStats() map[string]interface{} {
	s.resetMu.RLock()
	db := s.db
	s.resetMu.RUnlock()

	if db == nil {
		return map[string]interface{}{"error": "db is nil"}
	}

	sqlDB, err := db.DB()
	if err != nil {
		return map[string]interface{}{"error": err.Error()}
	}

	stats := sqlDB.Stats()
	return map[string]interface{}{
		"max_open_connections": stats.MaxOpenConnections,
		"open_connections":     stats.OpenConnections,
		"in_use":               stats.InUse,
		"idle":                 stats.Idle,
		"wait_count":           stats.WaitCount,
		"wait_duration":        stats.WaitDuration.String(),
		"max_idle_closed":      stats.MaxIdleClosed,
		"max_idle_time_closed": stats.MaxIdleTimeClosed,
		"max_lifetime_closed":  stats.MaxLifetimeClosed,
	}
}

// ResetConnection closes and recreates the database connection.
// Uses a write lock to prevent concurrent resets and a cooldown to avoid rapid successive resets.
func (s *Session) ResetConnection(ctx context.Context) error {
	log := s.Logger()

	// Early cooldown check without write lock (final check done after acquiring write lock)
	s.resetMu.RLock()
	last := s.lastResetTime
	cooldown := s.resetCooldown
	s.resetMu.RUnlock()
	if time.Since(last) < cooldown {
		log.InfoContext(ctx, "skipping connection reset due to cooldown",
			"last_reset", last, "cooldown", cooldown)
		return nil
	}

	log.InfoContext(ctx, "resetting database connection: "+s.config.String())

	// Establish new connection outside the lock
	newDB, newSQLDB, err := s.connect()
	if err != nil {
		return ErrConnection.WithReason("[%s] failed to create new connection", s.role).WithCause(err)
	}

	// Test the new connection
	testCtx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()

	if err := newSQLDB.PingContext(testCtx); err != nil {
		_ = newSQLDB.Close()
		return ErrConnection.WithReason("[%s] failed to ping new connection", s.role).WithCause(err)
	}

	// Acquire write lock for final cooldown check and swap
	s.resetMu.Lock()
	defer s.resetMu.Unlock()

	if time.Since(s.lastResetTime) < s.resetCooldown {
		// Another reset completed recently; close the new connection and skip
		_ = newSQLDB.Close()
		log.InfoContext(ctx, "skipping swap because another reset completed recently")
		return nil
	}

	oldDB := s.db
	s.db = newDB
	s.queryDB.Refresh(sqlx.NewDb(newSQLDB, "postgres"))
	s.lastResetTime = time.Now()

	// Gracefully close the old connection (waits for in-flight queries)
	if oldDB != nil {
		gracefulTimeout := s.gracefulCloseTimeout
		go func(ctx context.Context, old *gorm.DB, log *slog.Logger, timeout time.Duration) {
			sqlDB, err := old.DB()
			if err != nil {
				xerr := ErrConnection.WithReason("[%s] failed to get sql.DB for closing old connection after reset", s.role).WithCause(err)
				log.ErrorContext(ctx, "failed to get sql.DB for closing old connection after reset", "error", xerr)
				return
			}

			startTime := time.Now()
			deadline := startTime.Add(timeout)

			// Poll until InUse reaches 0 or timeout
			ticker := time.NewTicker(100 * time.Millisecond)
			defer ticker.Stop()

			for time.Now().Before(deadline) {
				stats := sqlDB.Stats()
				if stats.InUse == 0 {
					break
				}
				<-ticker.C
			}

			finalStats := sqlDB.Stats()
			if err := sqlDB.Close(); err != nil {
				xerr := ErrConnection.WithReason("[%s] failed to close old connection after reset", s.role).WithCause(err)
				log.ErrorContext(ctx, "failed to close old connection after reset", "error", xerr)
			} else {
				log.InfoContext(ctx, "gracefully closed old connection",
					"wait_duration", time.Since(startTime).String(),
					"final_in_use", finalStats.InUse,
				)
			}
		}(context.WithoutCancel(ctx), oldDB, log, gracefulTimeout)
	} else {
		log.InfoContext(ctx, "no old connection to close after reset")
	}

	// Log pool statistics (lock is already held, so access db directly)
	var stats map[string]interface{}
	if newSQLDB != nil {
		dbStats := newSQLDB.Stats()
		stats = map[string]interface{}{
			"max_open_connections": dbStats.MaxOpenConnections,
			"open_connections":     dbStats.OpenConnections,
			"in_use":               dbStats.InUse,
			"idle":                 dbStats.Idle,
		}
	}

	log.InfoContext(ctx, "database connection reset completed",
		"stats", stats,
	)

	return nil
}

// Close closes the database connection.
func (s *Session) Close() error {
	s.resetMu.RLock()
	db := s.db
	s.resetMu.RUnlock()

	if db == nil {
		return nil
	}

	sqlDB, err := db.DB()
	if err != nil {
		return ErrConnection.WithReason("[%s] failed to get database instance", s.role).WithCause(err)
	}

	return sqlDB.Close()
}

// GetDBOrTx returns the transaction from the context if one is set,
// otherwise the base DB connection. In both cases, WithContext(ctx) is applied
// so that the latest context (carrying RLS session variables) is propagated.
func (s *Session) GetDBOrTx(ctx context.Context) *gorm.DB {
	if tx, ok := ctx.Value(txKey{}).(*gorm.DB); ok {
		return tx.WithContext(ctx)
	}
	s.resetMu.RLock()
	db := s.db
	s.resetMu.RUnlock()

	return db.WithContext(ctx)
}

func (s *Session) connect() (*gorm.DB, *sql.DB, error) {
	var db *gorm.DB
	var err error

	// 1. Create dialector (with or without retry driver)
	var dialector gorm.Dialector

	if s.retryConfig != nil {
		if s.retryConfig.ConnectionResetFunc == nil {
			s.retryConfig.WithConnectionReset(s.ResetConnection)
		}

		// Set RLS connector wrapper so that ResetSession is called on new connections
		if s.rlsConfig != nil && s.retryConfig.ConnectorWrapper == nil {
			rlsConfig := s.rlsConfig // capture for closure
			s.retryConfig.WithConnectorWrapper(func(baseConnector driver.Connector) driver.Connector {
				return rlsconn.NewRLSConnector(baseConnector, rlsConfig)
			})
		}

		// Register the driver (once per process).
		// Note: the first registration's config is used for all connections.
		// When RLS is enabled, the first session to connect must have RLS configured.
		registerOnce.Do(func() {
			pgxretry.Register(s.retryConfig)
			if s.config.Logger != nil {
				s.config.Logger.Info("database driver registered", "driver", pgxretry.DriverName)
			}
		})

		dialector = postgres.New(postgres.Config{
			DriverName: pgxretry.DriverName,
			DSN:        s.config.dsn(),
		})
	} else {
		dialector = postgres.Open(s.config.dsn())
	}

	// 2. Initialize GORM (with optional custom opener)
	gormOpener := s.config.GormOpener
	if gormOpener == nil {
		gormOpener = DefaultGormOpener
	}

	db, err = gormOpener(dialector, s.gormConfig)
	if err != nil {
		return nil, nil, ErrConnection.WithReason("[%s] failed to connect to database", s.role).WithCause(err)
	}

	// 3. Register callbacks if configured
	if s.config.GormCallbackRegistrar != nil {
		s.config.GormCallbackRegistrar(db, s.logger)
	}

	// Configure connection pool
	sqlDB, err := db.DB()
	if err != nil {
		return nil, nil, ErrConnection.WithReason("[%s] failed to get database instance", s.role).WithCause(err)
	}

	s.configureConnectionPool(sqlDB)

	return db, sqlDB, nil
}

// configureConnectionPool applies pool settings to the sql.DB.
func (s *Session) configureConnectionPool(sqlDB *sql.DB) {
	sqlDB.SetMaxOpenConns(s.config.MaxOpenConns)
	sqlDB.SetMaxIdleConns(s.config.MaxIdleConns)
	if s.config.ConnMaxLifetime > 0 {
		sqlDB.SetConnMaxLifetime(time.Duration(s.config.ConnMaxLifetime) * time.Minute)
	}
}

// GetCurrentTenantID retrieves the tenant ID from the PostgreSQL session variable (for debugging).
// Returns an empty string if not set.
func (s *Session) GetCurrentTenantID(ctx context.Context) (string, error) {
	var tenantID string
	db := s.GetDBOrTx(ctx)
	// COALESCE to handle NULL when session variable is not set
	err := db.WithContext(ctx).Raw("SELECT COALESCE(current_setting('app.current_tenant_id', true), '')").Scan(&tenantID).Error
	return tenantID, err
}

// GetCurrentUserID retrieves the user ID from the PostgreSQL session variable (for debugging).
// Returns an empty string if not set.
func (s *Session) GetCurrentUserID(ctx context.Context) (string, error) {
	var userID string
	db := s.GetDBOrTx(ctx)
	// COALESCE to handle NULL when session variable is not set
	err := db.WithContext(ctx).Raw("SELECT COALESCE(current_setting('app.current_user_id', true), '')").Scan(&userID).Error
	return userID, err
}
