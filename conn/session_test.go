package conn

import (
	"context"
	"database/sql"
	"testing"

	"github.com/google/uuid"
	"github.com/jmoiron/sqlx"
	"github.com/stretchr/testify/suite"
)

type SessionTestSuite struct {
	suite.Suite
	conn *TestSession
}

// Mock types for testing different ExtContext scenarios

// mockExtContext implements sqlx.ExtContext for testing unknown type panic
type mockExtContext struct{}

func (m *mockExtContext) QueryRowContext(ctx context.Context, query string, args ...interface{}) *sql.Row {
	return nil
}

func (m *mockExtContext) QueryContext(ctx context.Context, query string, args ...interface{}) (*sql.Rows, error) {
	return nil, nil
}

func (m *mockExtContext) ExecContext(ctx context.Context, query string, args ...interface{}) (sql.Result, error) {
	return &mockResult{}, nil
}

func (m *mockExtContext) PrepareContext(ctx context.Context, query string) (*sql.Stmt, error) {
	return nil, nil
}

func (m *mockExtContext) QueryxContext(ctx context.Context, query string, args ...interface{}) (*sqlx.Rows, error) {
	return nil, nil
}

func (m *mockExtContext) QueryRowxContext(ctx context.Context, query string, args ...interface{}) *sqlx.Row {
	return nil
}

func (m *mockExtContext) GetContext(ctx context.Context, dest interface{}, query string, args ...interface{}) error {
	return nil
}

func (m *mockExtContext) SelectContext(ctx context.Context, dest interface{}, query string, args ...interface{}) error {
	return nil
}

func (m *mockExtContext) DriverName() string {
	return "mock"
}

func (m *mockExtContext) Rebind(query string) string {
	return query
}

func (m *mockExtContext) BindNamed(query string, arg interface{}) (string, []interface{}, error) {
	return query, nil, nil
}

// mockResult implements sql.Result for testing
type mockResult struct{}

func (m *mockResult) LastInsertId() (int64, error) {
	return 1, nil
}

func (m *mockResult) RowsAffected() (int64, error) {
	return 1, nil
}

func (suite *SessionTestSuite) SetupTest() {
	config := NewConfig(WithEnvVars("writer"))
	suite.conn = NewTestSession(suite.T(), config)
}

func (suite *SessionTestSuite) TearDownTest() {
	if suite.conn != nil {
		suite.conn.Cleanup()
	}
}

func (suite *SessionTestSuite) TestNamedQueryRowContext_BindError() {
	// Arrange
	ctx := suite.conn.Context()
	db, err := suite.conn.GetQueryDBOrTx()
	suite.Require().NoError(err)

	// Invalid parameter that will cause BindNamed to fail
	invalidQuery := "SELECT :invalid_struct_field as value"
	invalidParams := struct {
		ValidField string
	}{
		ValidField: "test",
	}

	// Act
	row, err := db.NamedQueryRowContext(ctx, invalidQuery, invalidParams)

	// Assert
	suite.Assert().Error(err, "Should return bind error")
	suite.Assert().Nil(row, "Should return nil row when bind fails")
	suite.Assert().Contains(err.Error(), "could not find name")
}

func (suite *SessionTestSuite) TestNamedQueryRowContext_Success() {
	// Arrange
	ctx := suite.conn.Context()
	db, err := suite.conn.GetQueryDBOrTx()
	suite.Require().NoError(err)

	// Valid query that should work (table-independent)
	validQuery := "SELECT 1 as value"
	params := map[string]interface{}{}

	// Act
	row, err := db.NamedQueryRowContext(ctx, validQuery, params)

	// Assert
	suite.Assert().NoError(err, "Should not return error for valid bind")
	suite.Assert().NotNil(row, "Should return valid row when bind succeeds")

	// Verify we can scan from the row
	var value int
	scanErr := row.Scan(&value)
	suite.Assert().NoError(scanErr, "Should be able to scan from returned row")
	suite.Assert().Equal(1, value, "Should return expected value")
}

// QueryRowContext Tests
func (suite *SessionTestSuite) TestQueryRowContext_WithDBContext() {
	// Arrange
	ctx := suite.conn.Context() // Use transactional context
	queryDB, err := suite.conn.GetQueryDBOrTx()
	suite.Require().NoError(err)

	// Act
	row := queryDB.QueryRowContext(ctx, "SELECT 1 as value")

	// Assert
	suite.Assert().NotNil(row, "Should return a valid row")

	var value int
	scanErr := row.Scan(&value)
	suite.Assert().NoError(scanErr, "Should be able to scan result")
	suite.Assert().Equal(1, value, "Should return expected value")
}

func (suite *SessionTestSuite) TestQueryRowContext_WithTransactionContext() {
	// Arrange
	ctx := suite.conn.Context()
	queryDB, err := suite.conn.GetQueryDBOrTx()
	suite.Require().NoError(err)

	// Act
	row := queryDB.QueryRowContext(ctx, "SELECT 1 as value")

	// Assert
	suite.Assert().NotNil(row, "Should return a valid row")

	var value int
	scanErr := row.Scan(&value)
	suite.Assert().NoError(scanErr, "Should be able to scan result")
	suite.Assert().Equal(1, value, "Should return expected value")
}

func (suite *SessionTestSuite) TestQueryRowContext_WithUnknownExtContext() {
	// Arrange
	ctx := context.Background()
	queryDB := NewQueryDB(&mockExtContext{}, "test", nil)

	// Act & Assert
	suite.Assert().Panics(func() {
		queryDB.QueryRowContext(ctx, "SELECT 1")
	}, "Should panic for unknown ExtContext type")
}

// NamedQueryContext Tests
func (suite *SessionTestSuite) TestNamedQueryContext_WithDBContext_Success() {
	// Arrange
	ctx := suite.conn.Context() // Use transactional context
	queryDB, err := suite.conn.GetQueryDBOrTx()
	suite.Require().NoError(err)
	// Create temporary table for testing
	_, err = queryDB.NamedExecContext(ctx, `
		CREATE TEMP TABLE test_table (
			id TEXT PRIMARY KEY,
			name TEXT
		)
	`, map[string]interface{}{})
	suite.Require().NoError(err, "Should create temporary table")

	query := "SELECT id, name FROM test_table LIMIT :limit"
	params := map[string]interface{}{"limit": 10}

	// Act
	rows, queryErr := queryDB.NamedQueryContext(ctx, query, params)

	// Assert
	suite.Assert().NoError(queryErr, "Should not return error for valid query")
	suite.Assert().NotNil(rows, "Should return valid rows")
	defer rows.Close()
}

func (suite *SessionTestSuite) TestNamedQueryContext_WithDBContext_BindError() {
	// Arrange
	ctx := suite.conn.Context() // Use transactional context
	queryDB, err := suite.conn.GetQueryDBOrTx()
	suite.Require().NoError(err)
	query := "SELECT :missing_param as value"
	params := map[string]interface{}{"wrong_param": "value"}

	// Act
	rows, queryErr := queryDB.NamedQueryContext(ctx, query, params)

	// Assert
	suite.Assert().Error(queryErr, "Should return error for bind failure")
	suite.Assert().Nil(rows, "Should return nil rows on error")
	suite.Assert().Contains(queryErr.Error(), "could not find name")
}

func (suite *SessionTestSuite) TestNamedQueryContext_WithTransactionContext_Success() {
	// Arrange
	ctx := suite.conn.Context()
	queryDB, err := suite.conn.GetQueryDBOrTx()
	suite.Require().NoError(err)
	// Create temporary table for testing
	_, err = queryDB.NamedExecContext(ctx, `
		CREATE TEMP TABLE test_table (
			id TEXT PRIMARY KEY,
			name TEXT
		)
	`, map[string]interface{}{})
	suite.Require().NoError(err, "Should create temporary table")

	query := "SELECT id, name FROM test_table LIMIT :limit"
	params := map[string]interface{}{"limit": 5}

	// Act
	rows, queryErr := queryDB.NamedQueryContext(ctx, query, params)

	// Assert
	suite.Assert().NoError(queryErr, "Should not return error for valid query")
	suite.Assert().NotNil(rows, "Should return valid rows")
	defer rows.Close()
}

func (suite *SessionTestSuite) TestNamedQueryContext_WithTransactionContext_BindError() {
	// Arrange
	ctx := suite.conn.Context()
	queryDB, err := suite.conn.GetQueryDBOrTx()
	suite.Require().NoError(err)
	query := "SELECT :missing_param as value"
	params := map[string]interface{}{"wrong_param": "value"}

	// Act
	rows, queryErr := queryDB.NamedQueryContext(ctx, query, params)

	// Assert
	suite.Assert().Error(queryErr, "Should return error for bind failure")
	suite.Assert().Nil(rows, "Should return nil rows on error")
	suite.Assert().Contains(queryErr.Error(), "could not find name")
}

// NamedExecContext Tests
func (suite *SessionTestSuite) TestNamedExecContext_WithDBContext_Success() {
	// Arrange
	ctx := suite.conn.Context() // Use transactional context
	queryDB, err := suite.conn.GetQueryDBOrTx()
	suite.Require().NoError(err)

	// Create temporary table for testing
	_, err = queryDB.NamedExecContext(ctx, `
		CREATE TEMP TABLE test_table (
			id TEXT PRIMARY KEY,
			name TEXT,
			status TEXT
		)
	`, map[string]interface{}{})
	suite.Require().NoError(err, "Should create temporary table")

	// Generate unique ID
	testID := uuid.New().String()

	// Insert into temporary table
	query := `
		INSERT INTO test_table (id, name, status)
		VALUES (:id, :name, :status)
	`
	params := map[string]interface{}{
		"id":     testID,
		"name":   "Test Item",
		"status": "ACTIVE",
	}

	// Act
	result, execErr := queryDB.NamedExecContext(ctx, query, params)

	// Assert
	suite.Assert().NoError(execErr, "Should not return error for valid exec")
	suite.Assert().NotNil(result, "Should return valid result")

	rowsAffected, raErr := result.RowsAffected()
	suite.Assert().NoError(raErr, "Should be able to get rows affected")
	suite.Assert().Equal(int64(1), rowsAffected, "Should affect exactly 1 row")
}

func (suite *SessionTestSuite) TestNamedExecContext_WithDBContext_BindError() {
	// Arrange
	ctx := suite.conn.Context() // Use transactional context
	queryDB, err := suite.conn.GetQueryDBOrTx()
	suite.Require().NoError(err)
	query := "SELECT :missing_param as value WHERE :id = :id"
	params := map[string]interface{}{"wrong_param": "value", "id": "some-id"}

	// Act
	result, execErr := queryDB.NamedExecContext(ctx, query, params)

	// Assert
	suite.Assert().Error(execErr, "Should return error for bind failure")
	suite.Assert().Nil(result, "Should return nil result on error")
	suite.Assert().Contains(execErr.Error(), "could not find name")
}

func (suite *SessionTestSuite) TestNamedExecContext_WithTransactionContext_Success() {
	// Arrange
	ctx := suite.conn.Context()
	queryDB, err := suite.conn.GetQueryDBOrTx()
	suite.Require().NoError(err)

	// Create temporary table for testing
	_, err = queryDB.NamedExecContext(ctx, `
		CREATE TEMP TABLE test_table (
			id TEXT PRIMARY KEY,
			name TEXT,
			status TEXT
		)
	`, map[string]interface{}{})
	suite.Require().NoError(err, "Should create temporary table")

	// Generate unique ID
	testID := uuid.New().String()

	// Insert into temporary table
	query := `
		INSERT INTO test_table (id, name, status)
		VALUES (:id, :name, :status)
	`
	params := map[string]interface{}{
		"id":     testID,
		"name":   "Test Transaction Item",
		"status": "ACTIVE",
	}

	// Act
	result, execErr := queryDB.NamedExecContext(ctx, query, params)

	// Assert
	suite.Assert().NoError(execErr, "Should not return error for valid exec")
	suite.Assert().NotNil(result, "Should return valid result")

	rowsAffected, raErr := result.RowsAffected()
	suite.Assert().NoError(raErr, "Should be able to get rows affected")
	suite.Assert().Equal(int64(1), rowsAffected, "Should affect exactly 1 row")
}

func (suite *SessionTestSuite) TestNamedExecContext_WithTransactionContext_BindError() {
	// Arrange
	ctx := suite.conn.Context()
	queryDB, err := suite.conn.GetQueryDBOrTx()
	suite.Require().NoError(err)
	query := "SELECT :missing_param as value WHERE :id = :id"
	params := map[string]interface{}{"wrong_param": "value", "id": "some-id"}

	// Act
	result, execErr := queryDB.NamedExecContext(ctx, query, params)

	// Assert
	suite.Assert().Error(execErr, "Should return error for bind failure")
	suite.Assert().Nil(result, "Should return nil result on error")
	suite.Assert().Contains(execErr.Error(), "could not find name")
}

// BindNamed Tests
func (suite *SessionTestSuite) TestBindNamed_WithDBContext() {
	// Arrange
	db, err := suite.conn.GetQueryDBOrTx()
	suite.Require().NoError(err)
	query := "SELECT :id as id, :name as name"
	params := map[string]interface{}{
		"id":   "550e8400-e29b-41d4-a716-446655440000",
		"name": "Test Item",
	}

	// Act
	boundQuery, boundArgs, err := db.BindNamed(query, params)

	// Assert
	suite.Assert().NoError(err, "Should not return error for valid bind")
	suite.Assert().NotEmpty(boundQuery, "Should return bound query")
	suite.Assert().Len(boundArgs, 2, "Should have 2 bound arguments")
	suite.Assert().Contains(boundQuery, "$1", "Should contain PostgreSQL placeholder")
	suite.Assert().Contains(boundQuery, "$2", "Should contain PostgreSQL placeholder")
}

func (suite *SessionTestSuite) TestBindNamed_WithTransactionContext() {
	// Arrange
	queryDB, err := suite.conn.GetQueryDBOrTx()
	suite.Require().NoError(err)
	query := "SELECT :id as id, :status as status"
	params := map[string]interface{}{
		"id":     "550e8400-e29b-41d4-a716-446655440000",
		"status": "ACTIVE",
	}

	// Act
	boundQuery, boundArgs, bindErr := queryDB.BindNamed(query, params)

	// Assert
	suite.Assert().NoError(bindErr, "Should not return error for valid bind")
	suite.Assert().NotEmpty(boundQuery, "Should return bound query")
	suite.Assert().Len(boundArgs, 2, "Should have 2 bound arguments")
	suite.Assert().Contains(boundQuery, "$1", "Should contain PostgreSQL placeholder")
	suite.Assert().Contains(boundQuery, "$2", "Should contain PostgreSQL placeholder")
}

func (suite *SessionTestSuite) TestBindNamed_BindError() {
	// Arrange
	db, err := suite.conn.GetQueryDBOrTx()
	suite.Require().NoError(err)
	// Use invalid struct parameter that will cause binding to fail
	query := "SELECT :missing_param as value"
	params := struct {
		WrongParam string `db:"wrong_param"`
	}{
		WrongParam: "value",
	}

	// Act
	_, _, err = db.BindNamed(query, params)

	// Assert
	suite.Assert().Error(err, "Should return error for missing parameter")
	suite.Assert().Contains(err.Error(), "could not find name")
}

// Test comprehensive parameter binding scenarios
func (suite *SessionTestSuite) TestNamedQueryContext_ParameterTypes() {
	// Arrange
	ctx := suite.conn.Context() // Use transactional context
	queryDB, err := suite.conn.GetQueryDBOrTx()
	suite.Require().NoError(err)
	query := "SELECT :str_param as str_col, :int_param as int_col"
	params := map[string]interface{}{
		"str_param": "test_string",
		"int_param": "42",
	}

	// Act
	rows, queryErr := queryDB.NamedQueryContext(ctx, query, params)

	// Assert
	suite.Assert().NoError(queryErr, "Should handle various parameter types")
	suite.Assert().NotNil(rows, "Should return valid rows")
	defer rows.Close()
}

func (suite *SessionTestSuite) TestNamedExecContext_EmptyParameters() {
	// Arrange
	ctx := suite.conn.Context() // Use transactional context
	queryDB, err := suite.conn.GetQueryDBOrTx()
	suite.Require().NoError(err)
	query := "SELECT 1"
	params := map[string]interface{}{}

	// Act
	rows, queryErr := queryDB.NamedQueryContext(ctx, query, params)

	// Assert
	suite.Assert().NoError(queryErr, "Should handle empty parameters")
	suite.Assert().NotNil(rows, "Should return valid rows")
	defer rows.Close()
}

func (suite *SessionTestSuite) TestBindNamed_ComplexParameters() {
	// Arrange
	queryDB, err := suite.conn.GetQueryDBOrTx()
	suite.Require().NoError(err)
	query := "SELECT :ids as ids, :pattern as pattern, :date as date"
	params := struct {
		IDs     []string `db:"ids"`
		Pattern string   `db:"pattern"`
		Date    string   `db:"date"`
	}{
		IDs:     []string{"id1", "id2", "id3"},
		Pattern: "%test%",
		Date:    "2023-01-01",
	}

	// Act
	boundQuery, boundArgs, bindErr := queryDB.BindNamed(query, params)

	// Assert
	suite.Assert().NoError(bindErr, "Should handle complex struct parameters")
	suite.Assert().NotEmpty(boundQuery, "Should return bound query")
	suite.Assert().Len(boundArgs, 3, "Should have 3 bound arguments")
}

func TestSessionTestSuite(t *testing.T) {
	suite.Run(t, new(SessionTestSuite))
}
