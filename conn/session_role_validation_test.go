package conn_test

import (
	"testing"

	"github.com/hacomono-lib/go-pgkit/conn"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestSessionRoleValidation(t *testing.T) {
	t.Run("WriterSession rejects non-writer role", func(t *testing.T) {
		config := conn.NewConfig(
			conn.WithHost("localhost"),
			conn.WithPort(5432),
			conn.WithCredentials("user", "pass"),
			conn.WithDatabase("testdb"),
			conn.WithRole("reader"), // not writer
		)

		session, err := conn.NewWriterSession(config)
		require.Error(t, err)
		assert.Nil(t, session)
		assert.Contains(t, err.Error(), "invalid role for WriterSession")
		assert.Contains(t, err.Error(), "expected 'writer'")
		assert.Contains(t, err.Error(), "got 'reader'")
	})

	t.Run("WriterSession accepts writer role", func(t *testing.T) {
		// Requires actual DB connection; role validation is covered above
		t.Skip("Requires actual database connection")
	})

	t.Run("ReaderSession rejects non-reader role", func(t *testing.T) {
		config := conn.NewConfig(
			conn.WithHost("localhost"),
			conn.WithPort(5432),
			conn.WithCredentials("user", "pass"),
			conn.WithDatabase("testdb"),
			conn.WithRole("writer"), // not reader
		)

		session, err := conn.NewReaderSession(config)
		require.Error(t, err)
		assert.Nil(t, session)
		assert.Contains(t, err.Error(), "invalid role for ReaderSession")
		assert.Contains(t, err.Error(), "expected 'reader'")
		assert.Contains(t, err.Error(), "got 'writer'")
	})

	t.Run("ReaderSession accepts reader role", func(t *testing.T) {
		// Requires actual DB connection; role validation is covered above
		t.Skip("Requires actual database connection")
	})

	t.Run("Empty role should fail", func(t *testing.T) {
		config := conn.NewConfig(
			conn.WithHost("localhost"),
			conn.WithPort(5432),
			conn.WithCredentials("user", "pass"),
			conn.WithDatabase("testdb"),
			// WithRole() not called (role="")
		)

		writerSession, writerErr := conn.NewWriterSession(config)
		require.Error(t, writerErr)
		assert.Nil(t, writerSession)
		assert.Contains(t, writerErr.Error(), "invalid role for WriterSession")

		readerSession, readerErr := conn.NewReaderSession(config)
		require.Error(t, readerErr)
		assert.Nil(t, readerSession)
		assert.Contains(t, readerErr.Error(), "invalid role for ReaderSession")
	})
}
