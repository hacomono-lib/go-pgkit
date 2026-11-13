package pgxretry_test

import (
	"context"
	"database/sql/driver"
	"errors"
	"io"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"

	"github.com/hacomono-lib/go-pgkit/pgxretry"
)

// readOnlyConn answers SHOW transaction_read_only with value, or fails with err, and counts the queries.
type readOnlyConn struct {
	driver.Conn
	value   any
	err     error
	queries int
}

func (c *readOnlyConn) QueryContext(_ context.Context, query string, _ []driver.NamedValue) (driver.Rows, error) {
	c.queries++
	if query != "SHOW transaction_read_only" {
		return nil, errors.New("unexpected query: " + query)
	}
	if c.err != nil {
		return nil, c.err
	}
	return &oneValueRows{value: c.value}, nil
}

type oneValueRows struct {
	value any
	done  bool
}

func (r *oneValueRows) Columns() []string { return []string{"transaction_read_only"} }
func (r *oneValueRows) Close() error      { return nil }
func (r *oneValueRows) Next(dest []driver.Value) error {
	if r.done {
		return io.EOF
	}
	r.done = true
	dest[0] = r.value
	return nil
}

func TestResetSessionChecksReadOnly(t *testing.T) {
	queryErr := errors.New("connection reset by peer")
	tests := []struct {
		name         string
		check        bool
		value        any
		err          error
		wantBadConn  bool
		wantReadOnly bool
		wantQueries  int
	}{
		{name: "not configured: does not check", check: false, value: "on", wantQueries: 0},
		{name: "writable", check: true, value: "off", wantQueries: 1},
		{name: "writable as []byte", check: true, value: []byte("off"), wantQueries: 1},
		{name: "read-only", check: true, value: "on", wantBadConn: true, wantReadOnly: true, wantQueries: 1},
		{name: "read-only as []byte", check: true, value: []byte("on"), wantBadConn: true, wantReadOnly: true, wantQueries: 1},
		{name: "check fails", check: true, err: queryErr, wantBadConn: true, wantQueries: 1},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			base := &readOnlyConn{value: tt.value, err: tt.err}
			config := pgxretry.DefaultConfig()
			config.CheckReadOnlyConns = tt.check
			conn := pgxretry.NewRetryableConnForTest(base, "", config, nil)

			err := conn.ResetSession(context.Background())

			assert.Equal(t, tt.wantBadConn, errors.Is(err, driver.ErrBadConn), "err: %v", err)
			assert.Equal(t, tt.wantReadOnly, errors.Is(err, pgxretry.ErrReadOnlyConn), "err: %v", err)
			if tt.err != nil {
				assert.ErrorIs(t, err, tt.err)
			}
			assert.Equal(t, tt.wantQueries, base.queries)
		})
	}
}

func TestResetSessionSkipsReadOnlyCheckWithinInterval(t *testing.T) {
	base := &readOnlyConn{value: "off"}
	conn := pgxretry.NewRetryableConnForTest(base, "", pgxretry.DefaultConfig().WithCheckReadOnlyConns(time.Hour), nil)

	assert.NoError(t, conn.ResetSession(context.Background()))
	base.value = "on"
	// Checked less than an hour ago, so the connection is reused without checking.
	assert.NoError(t, conn.ResetSession(context.Background()))
	assert.Equal(t, 1, base.queries)
}

func TestResetSessionChecksReadOnlyAgainAfterFailure(t *testing.T) {
	base := &readOnlyConn{err: errors.New("timeout")}
	conn := pgxretry.NewRetryableConnForTest(base, "", pgxretry.DefaultConfig().WithCheckReadOnlyConns(time.Hour), nil)

	assert.ErrorIs(t, conn.ResetSession(context.Background()), driver.ErrBadConn)
	base.err = nil
	base.value = "on"
	// A failed check does not count as a check, so the interval does not skip the next one.
	assert.ErrorIs(t, conn.ResetSession(context.Background()), pgxretry.ErrReadOnlyConn)
	assert.Equal(t, 2, base.queries)
}

func TestValidateRejectsNegativeReadOnlyCheckInterval(t *testing.T) {
	config := pgxretry.DefaultConfig().WithCheckReadOnlyConns(-time.Second)
	assert.ErrorIs(t, config.Validate(), pgxretry.ErrInvalidConfig)
}
