package pgxretry_test

import (
	"context"
	"database/sql/driver"
	"errors"
	"fmt"
	"io"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/hacomono-lib/go-pgkit/pgxretry"

	"github.com/jackc/pgx/v5/pgconn"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/suite"
)

type ConnTestSuite struct {
	suite.Suite
}

func TestConnSuite(t *testing.T) {
	suite.Run(t, new(ConnTestSuite))
}

// mockConn implements driver.Conn for testing
type mockConn struct {
	mock.Mock
}

func (m *mockConn) Prepare(query string) (driver.Stmt, error) {
	args := m.Called(query)
	if stmt := args.Get(0); stmt != nil {
		return stmt.(driver.Stmt), args.Error(1)
	}
	return nil, args.Error(1)
}

func (m *mockConn) Close() error {
	args := m.Called()
	return args.Error(0)
}

func (m *mockConn) Begin() (driver.Tx, error) {
	args := m.Called()
	if tx := args.Get(0); tx != nil {
		return tx.(driver.Tx), args.Error(1)
	}
	return nil, args.Error(1)
}

// mockConnPinger implements driver.Conn and driver.Pinger
type mockConnPinger struct {
	mockConn
}

func (m *mockConnPinger) Ping(ctx context.Context) error {
	args := m.Called(ctx)
	return args.Error(0)
}

// mockConnQueryer implements driver.Conn and driver.QueryerContext
type mockConnQueryer struct {
	mockConn
}

func (m *mockConnQueryer) QueryContext(ctx context.Context, query string, args []driver.NamedValue) (driver.Rows, error) {
	mockArgs := m.Called(ctx, query, args)
	if rows := mockArgs.Get(0); rows != nil {
		return rows.(driver.Rows), mockArgs.Error(1)
	}
	return nil, mockArgs.Error(1)
}

// mockConnExecer implements driver.Conn and driver.ExecerContext
type mockConnExecer struct {
	mockConn
}

func (m *mockConnExecer) ExecContext(ctx context.Context, query string, args []driver.NamedValue) (driver.Result, error) {
	mockArgs := m.Called(ctx, query, args)
	if result := mockArgs.Get(0); result != nil {
		return result.(driver.Result), mockArgs.Error(1)
	}
	return nil, mockArgs.Error(1)
}

// mockConnPrepareContext implements driver.ConnPrepareContext
type mockConnPrepareContext struct {
	mockConn
}

func (m *mockConnPrepareContext) PrepareContext(ctx context.Context, query string) (driver.Stmt, error) {
	args := m.Called(ctx, query)
	if stmt := args.Get(0); stmt != nil {
		return stmt.(driver.Stmt), args.Error(1)
	}
	return nil, args.Error(1)
}

// mockConnBeginTx implements driver.Conn and driver.ConnBeginTx
type mockConnBeginTx struct {
	mockConn
}

func (m *mockConnBeginTx) BeginTx(ctx context.Context, opts driver.TxOptions) (driver.Tx, error) {
	args := m.Called(ctx, opts)
	if tx := args.Get(0); tx != nil {
		return tx.(driver.Tx), args.Error(1)
	}
	return nil, args.Error(1)
}

// Combined mock that implements multiple interfaces for full connection
type mockFullConn struct {
	mockConn
}

func (m *mockFullConn) Ping(ctx context.Context) error {
	args := m.Called(ctx)
	return args.Error(0)
}

func (m *mockFullConn) QueryContext(ctx context.Context, query string, args []driver.NamedValue) (driver.Rows, error) {
	mockArgs := m.Called(ctx, query, args)
	if rows := mockArgs.Get(0); rows != nil {
		return rows.(driver.Rows), mockArgs.Error(1)
	}
	return nil, mockArgs.Error(1)
}

func (m *mockFullConn) ExecContext(ctx context.Context, query string, args []driver.NamedValue) (driver.Result, error) {
	mockArgs := m.Called(ctx, query, args)
	if result := mockArgs.Get(0); result != nil {
		return result.(driver.Result), mockArgs.Error(1)
	}
	return nil, mockArgs.Error(1)
}

func (m *mockFullConn) PrepareContext(ctx context.Context, query string) (driver.Stmt, error) {
	args := m.Called(ctx, query)
	if stmt := args.Get(0); stmt != nil {
		return stmt.(driver.Stmt), args.Error(1)
	}
	return nil, args.Error(1)
}

func (m *mockFullConn) BeginTx(ctx context.Context, opts driver.TxOptions) (driver.Tx, error) {
	args := m.Called(ctx, opts)
	if tx := args.Get(0); tx != nil {
		return tx.(driver.Tx), args.Error(1)
	}
	return nil, args.Error(1)
}

// mockRows implements driver.Rows
type mockRows struct {
	mock.Mock
}

func (m *mockRows) Columns() []string {
	args := m.Called()
	if cols := args.Get(0); cols != nil {
		return cols.([]string)
	}
	return nil
}

func (m *mockRows) Close() error {
	args := m.Called()
	return args.Error(0)
}

func (m *mockRows) Next(dest []driver.Value) error {
	args := m.Called(dest)
	return args.Error(0)
}

// mockTx implements driver.Tx
type mockTx struct {
	mock.Mock
}

func (m *mockTx) Commit() error {
	args := m.Called()
	return args.Error(0)
}

func (m *mockTx) Rollback() error {
	args := m.Called()
	return args.Error(0)
}

// mockStmt implements driver.Stmt
type mockStmt struct {
	mock.Mock
}

func (m *mockStmt) Close() error {
	return nil
}

func (m *mockStmt) NumInput() int {
	return -1
}

func (m *mockStmt) Exec(args []driver.Value) (driver.Result, error) {
	return nil, nil
}

func (m *mockStmt) Query(args []driver.Value) (driver.Rows, error) {
	return nil, nil
}

// Helper to create retryableConn with mock for testing
func (s *ConnTestSuite) createRetryableConn(mockBaseConn driver.Conn) *pgxretry.RetryableConn {
	config := pgxretry.DefaultConfig()
	drv := pgxretry.NewDriver(config)

	conn := pgxretry.NewRetryableConnForTest(mockBaseConn, "test-dsn", config, drv)

	return conn
}

// Test Ping method
func (s *ConnTestSuite) TestPing() {
	s.Run("successful ping", func() {
		mockConn := &mockConnPinger{}
		mockConn.On("Ping", mock.Anything).Return(nil).Once()

		conn := s.createRetryableConn(mockConn)
		err := conn.Ping(context.Background())

		s.NoError(err)
		mockConn.AssertExpectations(s.T())
	})

	s.Run("ping not implemented returns nil", func() {
		mockConn := &mockConn{}

		conn := s.createRetryableConn(mockConn)
		err := conn.Ping(context.Background())

		s.NoError(err)
	})
}

// Test QueryContext with retry logic
func (s *ConnTestSuite) TestQueryContext() {
	s.Run("successful query", func() {
		mockConn := &mockConnQueryer{}
		mockRows := &mockRows{}
		mockConn.On("QueryContext", mock.Anything, "SELECT 1", mock.Anything).Return(mockRows, nil).Once()

		conn := s.createRetryableConn(mockConn)
		rows, err := conn.QueryContext(context.Background(), "SELECT 1", nil)

		s.NoError(err)
		s.NotNil(rows)
		mockConn.AssertExpectations(s.T())
	})

	s.Run("query with retryable error", func() {
		mockConn := &mockConnQueryer{}
		mockRows := &mockRows{}

		// First call fails with retryable error
		mockConn.On("QueryContext", mock.Anything, "SELECT 1", mock.Anything).
			Return(nil, io.EOF).Once()
		// Second call succeeds
		mockConn.On("QueryContext", mock.Anything, "SELECT 1", mock.Anything).
			Return(mockRows, nil).Once()

		conn := s.createRetryableConn(mockConn)
		rows, err := conn.QueryContext(context.Background(), "SELECT 1", nil)

		s.NoError(err)
		s.NotNil(rows)
		mockConn.AssertExpectations(s.T())
	})

	s.Run("query with non-retryable error", func() {
		mockConn := &mockConnQueryer{}
		syntaxErr := errors.New("syntax error")

		mockConn.On("QueryContext", mock.Anything, "SELECT 1", mock.Anything).
			Return(nil, syntaxErr).Once()

		conn := s.createRetryableConn(mockConn)
		rows, err := conn.QueryContext(context.Background(), "SELECT 1", nil)

		s.Error(err)
		s.Nil(rows)
		s.Equal(syntaxErr, err)
		mockConn.AssertExpectations(s.T())
	})

	s.Run("query with cached plan error triggers reset", func() {
		resetCalled := false
		config := pgxretry.DefaultConfig().
			WithConnectionReset(func(ctx context.Context) error {
				resetCalled = true
				return nil
			})

		mockConn := &mockConnQueryer{}
		mockRows := &mockRows{}
		cachedPlanErr := &pgconn.PgError{
			Code:    "0A000",
			Message: "cached plan must not change result type",
		}

		// First call fails with cached plan error
		mockConn.On("QueryContext", mock.Anything, "SELECT 1", mock.Anything).
			Return(nil, cachedPlanErr).Once()
		// Second call succeeds
		mockConn.On("QueryContext", mock.Anything, "SELECT 1", mock.Anything).
			Return(mockRows, nil).Once()

		drv := pgxretry.NewDriver(config)
		conn := pgxretry.NewRetryableConnForTest(mockConn, "test-dsn", config, drv)

		rows, err := conn.QueryContext(context.Background(), "SELECT 1", nil)

		s.NoError(err)
		s.NotNil(rows)
		s.True(resetCalled)
		mockConn.AssertExpectations(s.T())
	})
}

// Test ExecContext with retry logic
func (s *ConnTestSuite) TestExecContext() {
	s.Run("successful exec", func() {
		mockConn := &mockConnExecer{}
		mockResult := &mockResult{}
		mockConn.On("ExecContext", mock.Anything, "INSERT INTO test", mock.Anything).Return(mockResult, nil).Once()

		conn := s.createRetryableConn(mockConn)
		result, err := conn.ExecContext(context.Background(), "INSERT INTO test", nil)

		s.NoError(err)
		s.NotNil(result)
		mockConn.AssertExpectations(s.T())
	})

	s.Run("exec with retry", func() {
		mockConn := &mockConnExecer{}
		mockResult := &mockResult{}

		// First call fails
		mockConn.On("ExecContext", mock.Anything, "INSERT INTO test", mock.Anything).
			Return(nil, io.EOF).Once()
		// Second call succeeds
		mockConn.On("ExecContext", mock.Anything, "INSERT INTO test", mock.Anything).
			Return(mockResult, nil).Once()

		conn := s.createRetryableConn(mockConn)
		result, err := conn.ExecContext(context.Background(), "INSERT INTO test", nil)

		s.NoError(err)
		s.NotNil(result)
		mockConn.AssertExpectations(s.T())
	})
}

// Test PrepareContext
func (s *ConnTestSuite) TestPrepareContext() {
	s.Run("Prepare without context", func() {
		mockConn := &mockConn{}
		mockStmt := &mockStmt{}
		mockConn.On("Prepare", "SELECT ?").Return(mockStmt, nil).Once()

		conn := s.createRetryableConn(mockConn)
		stmt, err := conn.Prepare("SELECT ?")

		s.NoError(err)
		s.NotNil(stmt)
		mockConn.AssertExpectations(s.T())
	})

	s.Run("successful prepare", func() {
		mockConn := &mockConnPrepareContext{}
		mockStmt := &mockStmt{}
		mockConn.On("PrepareContext", mock.Anything, "SELECT ?").Return(mockStmt, nil).Once()

		conn := s.createRetryableConn(mockConn)
		stmt, err := conn.PrepareContext(context.Background(), "SELECT ?")

		s.NoError(err)
		s.NotNil(stmt)
		mockConn.AssertExpectations(s.T())
	})

	s.Run("prepare with fallback to Prepare", func() {
		mockConn := &mockConn{}
		mockStmt := &mockStmt{}
		mockConn.On("Prepare", "SELECT ?").Return(mockStmt, nil).Once()

		conn := s.createRetryableConn(mockConn)
		stmt, err := conn.PrepareContext(context.Background(), "SELECT ?")

		s.NoError(err)
		s.NotNil(stmt)
		mockConn.AssertExpectations(s.T())
	})
}

// Test transaction behavior
func (s *ConnTestSuite) TestTransactionBehavior() {
	s.Run("Begin without options", func() {
		mockConn := &mockConnBeginTx{}
		mockTx := &mockTx{}
		mockConn.On("BeginTx", mock.Anything, driver.TxOptions{}).Return(mockTx, nil).Once()

		conn := s.createRetryableConn(mockConn)
		tx, err := conn.Begin()

		s.NoError(err)
		s.NotNil(tx)
		s.True(conn.GetInTransaction())
		mockConn.AssertExpectations(s.T())
	})

	s.Run("begin transaction marks connection as in transaction", func() {
		mockConn := &mockConnBeginTx{}
		mockTx := &mockTx{}
		mockConn.On("BeginTx", mock.Anything, mock.Anything).Return(mockTx, nil).Once()

		conn := s.createRetryableConn(mockConn)
		tx, err := conn.BeginTx(context.Background(), driver.TxOptions{})

		s.NoError(err)
		s.NotNil(tx)
		s.True(conn.GetInTransaction())
		mockConn.AssertExpectations(s.T())
	})

	s.Run("queries in transaction do not retry", func() {
		mockConn := &mockFullConn{}
		mockTx := &mockTx{}

		// Begin transaction
		mockConn.On("BeginTx", mock.Anything, mock.Anything).Return(mockTx, nil).Once()

		// Query fails with retryable error, but should not retry
		mockConn.On("QueryContext", mock.Anything, "SELECT 1", mock.Anything).
			Return(nil, io.EOF).Once()

		conn := s.createRetryableConn(mockConn)

		// Start transaction
		tx, err := conn.BeginTx(context.Background(), driver.TxOptions{})
		s.NoError(err)
		s.NotNil(tx)

		// Query should fail without retry
		rows, err := conn.QueryContext(context.Background(), "SELECT 1", nil)

		s.Error(err)
		s.Nil(rows)
		// Error should be wrapped with ErrRetryableInTx
		s.True(errors.Is(err, pgxretry.ErrRetryableInTx))
		mockConn.AssertExpectations(s.T())
	})

	s.Run("commit ends transaction state", func() {
		mockConn := &mockConnBeginTx{}
		mockTx := &mockTx{}
		mockConn.On("BeginTx", mock.Anything, mock.Anything).Return(mockTx, nil).Once()
		mockTx.On("Commit").Return(nil).Once()

		conn := s.createRetryableConn(mockConn)
		tx, err := conn.BeginTx(context.Background(), driver.TxOptions{})
		s.NoError(err)

		// Commit transaction
		err = tx.Commit()
		s.NoError(err)
		s.False(conn.GetInTransaction())

		mockConn.AssertExpectations(s.T())
		mockTx.AssertExpectations(s.T())
	})

	s.Run("rollback ends transaction state", func() {
		mockConn := &mockConnBeginTx{}
		mockTx := &mockTx{}
		mockConn.On("BeginTx", mock.Anything, mock.Anything).Return(mockTx, nil).Once()
		mockTx.On("Rollback").Return(nil).Once()

		conn := s.createRetryableConn(mockConn)
		tx, err := conn.BeginTx(context.Background(), driver.TxOptions{})
		s.NoError(err)

		// Rollback transaction
		err = tx.Rollback()
		s.NoError(err)
		s.False(conn.GetInTransaction())

		mockConn.AssertExpectations(s.T())
		mockTx.AssertExpectations(s.T())
	})

	s.Run("begin transaction fails", func() {
		mockConn := &mockConnBeginTx{}
		beginErr := errors.New("cannot start transaction")
		mockConn.On("BeginTx", mock.Anything, mock.Anything).Return(nil, beginErr).Once()

		conn := s.createRetryableConn(mockConn)
		tx, err := conn.BeginTx(context.Background(), driver.TxOptions{})

		s.Error(err)
		s.Nil(tx)
		s.Equal(beginErr, err)
		s.False(conn.GetInTransaction()) // Should not be marked as in transaction
		mockConn.AssertExpectations(s.T())
	})

	s.Run("connection does not support transactions", func() {
		mockConn := &mockConn{} // Does not implement ConnBeginTx

		conn := s.createRetryableConn(mockConn)
		tx, err := conn.BeginTx(context.Background(), driver.TxOptions{})

		s.Error(err)
		s.Nil(tx)
		s.Contains(err.Error(), "does not support transaction context")
		s.False(conn.GetInTransaction())
	})
}

// Test Close method
func (s *ConnTestSuite) TestClose() {
	s.Run("close connection", func() {
		mockConn := &mockConn{}
		mockConn.On("Close").Return(nil).Once()

		conn := s.createRetryableConn(mockConn)
		err := conn.Close()

		s.NoError(err)
		s.True(conn.GetClosed())
		mockConn.AssertExpectations(s.T())
	})

	s.Run("close already closed connection", func() {
		mockConn := &mockConn{}
		mockConn.On("Close").Return(nil).Once()

		conn := s.createRetryableConn(mockConn)

		// First close
		err := conn.Close()
		s.NoError(err)

		// Second close should not call underlying Close
		err = conn.Close()
		s.NoError(err)

		mockConn.AssertExpectations(s.T())
	})
}

// Test retry mechanism with backoff
func (s *ConnTestSuite) TestRetryWithBackoff() {
	s.Run("retry with exponential backoff", func() {
		mockConn := &mockConnQueryer{}
		mockRows := &mockRows{}

		// Track call times
		var callTimes []time.Time
		var mu sync.Mutex

		mockConn.On("QueryContext", mock.Anything, "SELECT 1", mock.Anything).
			Return(nil, io.EOF).
			Run(func(args mock.Arguments) {
				mu.Lock()
				callTimes = append(callTimes, time.Now())
				mu.Unlock()
			}).Times(2)

		// Third call succeeds
		mockConn.On("QueryContext", mock.Anything, "SELECT 1", mock.Anything).
			Return(mockRows, nil).
			Run(func(args mock.Arguments) {
				mu.Lock()
				callTimes = append(callTimes, time.Now())
				mu.Unlock()
			}).Once()

		config := pgxretry.DefaultConfig()
		config.InitialBackoff = 10 * time.Millisecond

		drv := pgxretry.NewDriver(config)
		conn := pgxretry.NewRetryableConnForTest(mockConn, "test-dsn", config, drv)

		rows, err := conn.QueryContext(context.Background(), "SELECT 1", nil)

		s.NoError(err)
		s.NotNil(rows)

		// Verify backoff intervals
		mu.Lock()
		defer mu.Unlock()
		s.Len(callTimes, 3)

		// First retry should have ~10ms delay
		interval1 := callTimes[1].Sub(callTimes[0])
		s.GreaterOrEqual(interval1, 10*time.Millisecond)

		// Second retry should have ~20ms delay (exponential)
		interval2 := callTimes[2].Sub(callTimes[1])
		s.GreaterOrEqual(interval2, 20*time.Millisecond)

		mockConn.AssertExpectations(s.T())
	})
}

// Test error detection
func (s *ConnTestSuite) TestErrorDetection() {
	s.Run("retryable error detection", func() {
		detector := pgxretry.NewDefaultErrorDetector()

		// Test various error types
		s.True(detector.IsRetryableError(io.EOF))
		s.True(detector.IsRetryableError(&pgconn.PgError{Code: "0A000", Message: "cached plan must not change result type"}))
		s.True(detector.IsRetryableError(errors.New("connection reset by peer")))
		s.False(detector.IsRetryableError(errors.New("syntax error")))
	})
}

// Test cached plan error detection
func (s *ConnTestSuite) TestCachedPlanErrorDetection() {
	s.Run("identifies cached plan errors", func() {
		conn := s.createRetryableConn(&mockConn{})

		// Test nil error
		s.False(conn.IsCachedPlanError(nil))

		// Test cached plan error
		s.True(conn.IsCachedPlanError(errors.New("cached plan must not change result type")))

		// Test non-cached plan error
		s.False(conn.IsCachedPlanError(errors.New("syntax error")))

		// Test wrapped cached plan error
		wrappedErr := fmt.Errorf("database error: %w", errors.New("cached plan must not change result type"))
		s.True(conn.IsCachedPlanError(wrappedErr))
	})
}

// Test query with named values
func (s *ConnTestSuite) TestQueryWithNamedValues() {
	s.Run("QueryContext with named values", func() {
		mockConn := &mockConnQueryer{}
		mockRows := &mockRows{}

		namedValues := []driver.NamedValue{
			{Name: "id", Ordinal: 1, Value: 123},
			{Name: "name", Ordinal: 2, Value: "test"},
		}

		mockConn.On("QueryContext", mock.Anything, "SELECT * FROM users WHERE id = $1 AND name = $2", namedValues).
			Return(mockRows, nil).Once()

		conn := s.createRetryableConn(mockConn)
		rows, err := conn.QueryContext(context.Background(), "SELECT * FROM users WHERE id = $1 AND name = $2", namedValues)

		s.NoError(err)
		s.NotNil(rows)
		mockConn.AssertExpectations(s.T())
	})

	s.Run("ExecContext with named values", func() {
		mockConn := &mockConnExecer{}
		mockResult := &mockResult{}

		namedValues := []driver.NamedValue{
			{Name: "name", Ordinal: 1, Value: "test"},
			{Name: "email", Ordinal: 2, Value: "test@example.com"},
		}

		mockConn.On("ExecContext", mock.Anything, "INSERT INTO users (name, email) VALUES ($1, $2)", namedValues).
			Return(mockResult, nil).Once()

		conn := s.createRetryableConn(mockConn)
		result, err := conn.ExecContext(context.Background(), "INSERT INTO users (name, email) VALUES ($1, $2)", namedValues)

		s.NoError(err)
		s.NotNil(result)
		mockConn.AssertExpectations(s.T())
	})
}

// Test connection reset callback error handling
func (s *ConnTestSuite) TestConnectionResetErrorHandling() {
	s.Run("connection reset function returns error", func() {
		resetErr := errors.New("failed to reset connection")
		config := pgxretry.DefaultConfig().
			WithConnectionReset(func(ctx context.Context) error {
				return resetErr
			})

		mockConn := &mockConnQueryer{}
		mockRows := &mockRows{}
		cachedPlanErr := &pgconn.PgError{
			Code:    "0A000",
			Message: "cached plan must not change result type",
		}

		// First call fails with cached plan error
		mockConn.On("QueryContext", mock.Anything, "SELECT 1", mock.Anything).
			Return(nil, cachedPlanErr).Once()
		// Second call succeeds despite reset error
		mockConn.On("QueryContext", mock.Anything, "SELECT 1", mock.Anything).
			Return(mockRows, nil).Once()

		drv := pgxretry.NewDriver(config)
		conn := pgxretry.NewRetryableConnForTest(mockConn, "test-dsn", config, drv)

		rows, err := conn.QueryContext(context.Background(), "SELECT 1", nil)

		// Query should still succeed even if reset fails
		s.NoError(err)
		s.NotNil(rows)
		mockConn.AssertExpectations(s.T())
	})
}

// Test successful recovery logging
func (s *ConnTestSuite) TestSuccessfulRecoveryLogging() {
	s.Run("logs successful recovery after retry", func() {
		var loggedMessages []string
		var mu sync.Mutex

		logger := &testLogger{
			logFunc: func(level, msg string) {
				mu.Lock()
				loggedMessages = append(loggedMessages, msg)
				mu.Unlock()
			},
		}

		config := pgxretry.DefaultConfig()
		config.Logger = logger
		config.InitialBackoff = 1 * time.Millisecond

		mockConn := &mockConnQueryer{}
		mockRows := &mockRows{}

		// First call fails
		mockConn.On("QueryContext", mock.Anything, "SELECT 1", mock.Anything).
			Return(nil, io.EOF).Once()
		// Second call succeeds
		mockConn.On("QueryContext", mock.Anything, "SELECT 1", mock.Anything).
			Return(mockRows, nil).Once()

		drv := pgxretry.NewDriver(config)
		conn := pgxretry.NewRetryableConnForTest(mockConn, "test-dsn", config, drv)

		rows, err := conn.QueryContext(context.Background(), "SELECT 1", nil)

		s.NoError(err)
		s.NotNil(rows)

		// Check that success after retry was logged
		mu.Lock()
		defer mu.Unlock()

		hasSuccessLog := false
		for _, msg := range loggedMessages {
			if strings.Contains(msg, "database operation succeeded after retry") {
				hasSuccessLog = true
				break
			}
		}
		s.True(hasSuccessLog, "Expected to find success log after retry")

		mockConn.AssertExpectations(s.T())
	})
}

// Test retry strategy returning negative backoff
func (s *ConnTestSuite) TestRetryStrategyNegativeBackoff() {
	s.Run("retry stops when strategy returns negative backoff", func() {
		mockStrategy := &mockRetryStrategy{}
		config := pgxretry.DefaultConfig()
		config.RetryStrategy = mockStrategy

		mockConn := &mockConnQueryer{}

		// First call fails
		mockConn.On("QueryContext", mock.Anything, "SELECT 1", mock.Anything).
			Return(nil, io.EOF).Once()

		// Strategy returns negative backoff, stopping retry
		mockStrategy.On("NextBackoff", 0).Return(-1 * time.Second).Once()

		drv := pgxretry.NewDriver(config)
		conn := pgxretry.NewRetryableConnForTest(mockConn, "test-dsn", config, drv)

		rows, err := conn.QueryContext(context.Background(), "SELECT 1", nil)

		s.Error(err)
		s.Nil(rows)
		s.Equal(io.EOF, err)

		mockConn.AssertExpectations(s.T())
		mockStrategy.AssertExpectations(s.T())
	})
}

// mockRetryStrategy for testing custom strategies
type mockRetryStrategy struct {
	mock.Mock
}

func (m *mockRetryStrategy) NextBackoff(attempt int) time.Duration {
	args := m.Called(attempt)
	return args.Get(0).(time.Duration)
}

func (m *mockRetryStrategy) MaxAttempts() int {
	args := m.Called()
	return args.Get(0).(int)
}

func (m *mockRetryStrategy) Reset() {
	m.Called()
}

// Add mockResult for testing ExecContext
type mockResult struct {
	mock.Mock
}

func (m *mockResult) LastInsertId() (int64, error) {
	args := m.Called()
	return args.Get(0).(int64), args.Error(1)
}

func (m *mockResult) RowsAffected() (int64, error) {
	args := m.Called()
	return args.Get(0).(int64), args.Error(1)
}

// Test that all retries are exhausted
func (s *ConnTestSuite) TestMaxRetriesExceeded() {
	s.Run("query fails after max retries", func() {
		config := pgxretry.DefaultConfig()
		config.MaxRetries = 2
		config.InitialBackoff = 1 * time.Millisecond

		mockConn := &mockConnQueryer{}

		// All calls fail
		mockConn.On("QueryContext", mock.Anything, "SELECT 1", mock.Anything).
			Return(nil, io.EOF).Times(3) // initial + 2 retries

		drv := pgxretry.NewDriver(config)
		conn := pgxretry.NewRetryableConnForTest(mockConn, "test-dsn", config, drv)

		rows, err := conn.QueryContext(context.Background(), "SELECT 1", nil)

		s.Error(err)
		s.Nil(rows)
		s.Equal(io.EOF, err)
		mockConn.AssertExpectations(s.T())
	})
}

// Test context cancellation during retry
func (s *ConnTestSuite) TestContextCancellation() {
	s.Run("retry stops on context cancellation", func() {
		config := pgxretry.DefaultConfig()
		config.InitialBackoff = 100 * time.Millisecond

		mockConn := &mockConnQueryer{}

		// First call fails
		mockConn.On("QueryContext", mock.Anything, "SELECT 1", mock.Anything).
			Return(nil, io.EOF).Once()

		drv := pgxretry.NewDriver(config)
		conn := pgxretry.NewRetryableConnForTest(mockConn, "test-dsn", config, drv)

		ctx, cancel := context.WithCancel(context.Background())

		// Cancel context after a short delay
		go func() {
			time.Sleep(10 * time.Millisecond)
			cancel()
		}()

		rows, err := conn.QueryContext(ctx, "SELECT 1", nil)

		s.Error(err)
		s.Nil(rows)
		s.Equal(context.Canceled, err)
		mockConn.AssertExpectations(s.T())
	})
}

// Test fallback paths when interfaces are not implemented
func (s *ConnTestSuite) TestFallbackPaths() {
	s.Run("QueryContext fallback to Prepare/Query", func() {
		mockConn := &mockConn{} // Does not implement QueryerContext
		mockStmt := &mockStmtQueryContext{}
		mockRows := &mockRows{}

		mockConn.On("Prepare", "SELECT 1").Return(mockStmt, nil).Once()
		mockStmt.On("QueryContext", mock.Anything, mock.Anything).Return(mockRows, nil).Once()
		mockStmt.On("Close").Return(nil).Once()

		conn := s.createRetryableConn(mockConn)
		rows, err := conn.QueryContext(context.Background(), "SELECT 1", nil)

		s.NoError(err)
		s.NotNil(rows)
		mockConn.AssertExpectations(s.T())
		mockStmt.AssertExpectations(s.T())
	})

	s.Run("ExecContext fallback to Prepare/Exec", func() {
		mockConn := &mockConn{} // Does not implement ExecerContext
		mockStmt := &mockStmtExecContext{}
		mockResult := &mockResult{}

		mockConn.On("Prepare", "INSERT INTO test").Return(mockStmt, nil).Once()
		mockStmt.On("ExecContext", mock.Anything, mock.Anything).Return(mockResult, nil).Once()
		mockStmt.On("Close").Return(nil).Once()

		conn := s.createRetryableConn(mockConn)
		result, err := conn.ExecContext(context.Background(), "INSERT INTO test", nil)

		s.NoError(err)
		s.NotNil(result)
		mockConn.AssertExpectations(s.T())
		mockStmt.AssertExpectations(s.T())
	})

	s.Run("QueryContext fallback with prepare error", func() {
		mockConn := &mockConn{} // Does not implement QueryerContext
		prepareErr := errors.New("prepare failed")

		mockConn.On("Prepare", "SELECT 1").Return(nil, prepareErr).Once()

		conn := s.createRetryableConn(mockConn)
		rows, err := conn.QueryContext(context.Background(), "SELECT 1", nil)

		s.Error(err)
		s.Nil(rows)
		s.Equal(prepareErr, err)
		mockConn.AssertExpectations(s.T())
	})

	s.Run("ExecContext fallback with prepare error", func() {
		mockConn := &mockConn{} // Does not implement ExecerContext
		prepareErr := errors.New("prepare failed")

		mockConn.On("Prepare", "INSERT INTO test").Return(nil, prepareErr).Once()

		conn := s.createRetryableConn(mockConn)
		result, err := conn.ExecContext(context.Background(), "INSERT INTO test", nil)

		s.Error(err)
		s.Nil(result)
		s.Equal(prepareErr, err)
		mockConn.AssertExpectations(s.T())
	})

	s.Run("QueryContext fallback without StmtQueryContext", func() {
		mockConn := &mockConn{} // Does not implement QueryerContext
		mockStmt := &mockStmt{} // Does not implement StmtQueryContext

		mockConn.On("Prepare", "SELECT 1").Return(mockStmt, nil).Once()

		conn := s.createRetryableConn(mockConn)
		rows, err := conn.QueryContext(context.Background(), "SELECT 1", nil)

		s.Error(err)
		s.Nil(rows)
		s.Contains(err.Error(), "statement does not support context-aware query")
		mockConn.AssertExpectations(s.T())
	})

	s.Run("ExecContext fallback without StmtExecContext", func() {
		mockConn := &mockConn{} // Does not implement ExecerContext
		mockStmt := &mockStmt{} // Does not implement StmtExecContext

		mockConn.On("Prepare", "INSERT INTO test").Return(mockStmt, nil).Once()

		conn := s.createRetryableConn(mockConn)
		result, err := conn.ExecContext(context.Background(), "INSERT INTO test", nil)

		s.Error(err)
		s.Nil(result)
		s.Contains(err.Error(), "statement does not support context-aware exec")
		mockConn.AssertExpectations(s.T())
	})
}

// mockStmtQueryContext implements driver.StmtQueryContext
type mockStmtQueryContext struct {
	mock.Mock
}

func (m *mockStmtQueryContext) Close() error {
	args := m.Called()
	return args.Error(0)
}

func (m *mockStmtQueryContext) NumInput() int {
	return -1
}

func (m *mockStmtQueryContext) Exec(args []driver.Value) (driver.Result, error) {
	return nil, nil
}

func (m *mockStmtQueryContext) Query(args []driver.Value) (driver.Rows, error) {
	return nil, nil
}

func (m *mockStmtQueryContext) QueryContext(ctx context.Context, args []driver.NamedValue) (driver.Rows, error) {
	mockArgs := m.Called(ctx, args)
	if rows := mockArgs.Get(0); rows != nil {
		return rows.(driver.Rows), mockArgs.Error(1)
	}
	return nil, mockArgs.Error(1)
}

// mockStmtExecContext implements driver.StmtExecContext
type mockStmtExecContext struct {
	mock.Mock
}

func (m *mockStmtExecContext) Close() error {
	args := m.Called()
	return args.Error(0)
}

func (m *mockStmtExecContext) NumInput() int {
	return -1
}

func (m *mockStmtExecContext) Exec(args []driver.Value) (driver.Result, error) {
	return nil, nil
}

func (m *mockStmtExecContext) Query(args []driver.Value) (driver.Rows, error) {
	return nil, nil
}

func (m *mockStmtExecContext) ExecContext(ctx context.Context, args []driver.NamedValue) (driver.Result, error) {
	mockArgs := m.Called(ctx, args)
	if result := mockArgs.Get(0); result != nil {
		return result.(driver.Result), mockArgs.Error(1)
	}
	return nil, mockArgs.Error(1)
}

// Custom test logger for tracking calls
type testLogger struct {
	logFunc func(level, msg string)
}

func (l *testLogger) DebugContext(ctx context.Context, msg string, args ...any) {
	l.logFunc("debug", msg)
}

func (l *testLogger) InfoContext(ctx context.Context, msg string, args ...any) {
	l.logFunc("info", msg)
}

func (l *testLogger) WarnContext(ctx context.Context, msg string, args ...any) {
	l.logFunc("warn", msg)
}

func (l *testLogger) ErrorContext(ctx context.Context, msg string, args ...any) {
	l.logFunc("error", msg)
}
