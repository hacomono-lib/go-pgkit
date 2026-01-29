package conn

import (
	"context"
	"fmt"
	"log/slog"

	"github.com/hacomono-lib/go-errorsx"
	"gorm.io/gorm"
)

// WriterSession is a session for write operations.
type WriterSession struct {
	*Session
}

func (s *WriterSession) String() string {
	if s == nil || s.Session == nil {
		return "WriterSession{nil}"
	}
	return fmt.Sprintf("WriterSession{%s}", s.Session.String())
}

// ReaderSession is a read-only session.
type ReaderSession struct {
	session *Session
}

func (s *ReaderSession) String() string {
	if s == nil || s.session == nil {
		return "ReaderSession{nil}"
	}
	return fmt.Sprintf("ReaderSession{%s}", s.session.String())
}

// NewWriterSession creates a new WriterSession.
func NewWriterSession(config *Config) (*WriterSession, error) {
	if config.Role != "writer" {
		return nil, ErrConnection.WithReason("invalid role for WriterSession: expected 'writer', got '%s'", config.Role)
	}

	session, err := newSession(config).Connect()
	if err != nil {
		return nil, err
	}
	return &WriterSession{Session: session}, nil
}

// NewReaderSession creates a new ReaderSession.
func NewReaderSession(config *Config) (*ReaderSession, error) {
	if config.Role != "reader" {
		return nil, ErrConnection.WithReason("invalid role for ReaderSession: expected 'reader', got '%s'", config.Role)
	}

	session, err := newSession(config).Connect()
	if err != nil {
		return nil, err
	}
	return &ReaderSession{session: session}, nil
}

// DB returns the GORM DB for read operations.
func (s *ReaderSession) DB() *gorm.DB {
	return s.session.DB()
}

// QueryDB returns the QueryDB for read operations.
func (s *ReaderSession) QueryDB() *QueryDB {
	return s.session.QueryDB()
}

// ResetConnection resets the database connection.
func (s *ReaderSession) ResetConnection(ctx context.Context) error {
	return s.session.ResetConnection(ctx)
}

// Logger returns the session logger.
func (s *ReaderSession) Logger() *slog.Logger {
	return s.session.Logger()
}

// GetPoolStats returns connection pool statistics.
func (s *ReaderSession) GetPoolStats() map[string]any {
	return s.session.GetPoolStats()
}

// GetDBOrTx returns a context-aware DB connection.
// ReaderSession does not support transactions, but needs context for RLS session variables.
func (s *ReaderSession) GetDBOrTx(ctx context.Context) *gorm.DB {
	return s.session.GetDBOrTx(ctx)
}

// Close closes the reader session.
func (s *ReaderSession) Close() error {
	return s.session.Close()
}

// SessionManager manages Writer and Reader sessions.
type SessionManager struct {
	writer *WriterSession
	reader *ReaderSession
}

// Writer returns the WriterSession.
func (sm *SessionManager) Writer() *WriterSession {
	if sm == nil {
		return nil
	}
	return sm.writer
}

// Reader returns the ReaderSession.
func (sm *SessionManager) Reader() *ReaderSession {
	if sm == nil {
		return nil
	}
	return sm.reader
}

// Close closes both sessions.
func (sm *SessionManager) Close() error {
	var writerErr, readerErr error
	if sm.writer != nil {
		writerErr = sm.writer.Close()
	}
	if sm.reader != nil {
		readerErr = sm.reader.Close()
	}
	return errorsx.Join(writerErr, readerErr)
}

func (sm *SessionManager) String() string {
	return fmt.Sprintf("SessionManager{writer:%s, reader:%s}", sm.writer, sm.reader)
}

// NewSessionManager creates a SessionManager from writer and reader configs.
func NewSessionManager(writerConfig, readerConfig *Config) (*SessionManager, error) {
	if writerConfig == nil && readerConfig == nil {
		return nil, ErrInitialization.WithReason("at least one of writerConfig or readerConfig must be provided").WithCallerStack()
	}

	var writer *WriterSession
	var reader *ReaderSession
	var err error

	// Create WriterSession
	if writerConfig != nil {
		writerConfig.Role = "writer"
		writer, err = NewWriterSession(writerConfig)
		if err != nil {
			return nil, err
		}
	}

	// Create ReaderSession
	if readerConfig != nil {
		readerConfig.Role = "reader"
		reader, err = NewReaderSession(readerConfig)
		if err != nil {
			if writer != nil {
				_ = writer.Close()
			}
			return nil, err
		}
	}

	return &SessionManager{
		writer: writer,
		reader: reader,
	}, nil
}
