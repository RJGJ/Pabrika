// Package store owns the SQLite databases: pools, pragmas, migrations and transactions.
// It is the only package that imports the SQLite driver.
package store

//go:generate go run github.com/sqlc-dev/sqlc/cmd/sqlc@v1.31.1 generate -f ../../sqlc.yaml

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"sync/atomic"
	"time"

	"github.com/pressly/goose/v3"
	"modernc.org/sqlite"

	"github.com/RJGJ/Pabrika/internal/store/db"
	"github.com/RJGJ/Pabrika/migrations"
)

// Store holds a single-connection write pool and a multi-connection read pool
// (for the in-memory store both are the same one-connection pool).
type Store struct {
	write  *sql.DB
	read   *sql.DB
	memory bool
	count  atomic.Int64
}

// Open opens (creating the parent directory and file) a WAL database. It does NOT migrate.
func Open(ctx context.Context, path string) (*Store, error) {
	if dir := filepath.Dir(path); dir != "" {
		if err := os.MkdirAll(dir, 0o755); err != nil {
			return nil, fmt.Errorf("create database directory: %w", err)
		}
	}
	abs, err := filepath.Abs(path)
	if err != nil {
		return nil, err
	}
	w, err := sql.Open("sqlite", fileDSN(abs, true))
	if err != nil {
		return nil, err
	}
	w.SetMaxOpenConns(1)
	w.SetMaxIdleConns(1)
	// Switching a fresh file to WAL needs an exclusive lock, which can briefly be busy when
	// another process is creating/migrating the same file; retry a few times.
	for attempt := 0; ; attempt++ {
		err = w.PingContext(ctx)
		if err == nil {
			break
		}
		if attempt >= 20 || ctx.Err() != nil {
			w.Close()
			return nil, fmt.Errorf("open database %s: %w", path, err)
		}
		time.Sleep(50 * time.Millisecond)
	}
	r, err := sql.Open("sqlite", fileDSN(abs, false))
	if err != nil {
		w.Close()
		return nil, err
	}
	n := runtime.NumCPU()
	if n < 4 {
		n = 4
	}
	r.SetMaxOpenConns(n)
	r.SetMaxIdleConns(n)
	if err := r.PingContext(ctx); err != nil {
		w.Close()
		r.Close()
		return nil, fmt.Errorf("open read pool: %w", err)
	}
	return &Store{write: w, read: r}, nil
}

// OpenMemory returns a fresh, isolated in-memory database served by ONE connection for
// reads and writes. Never hold a Rows open while calling the write path.
func OpenMemory(ctx context.Context) (*Store, error) {
	d, err := sql.Open("sqlite", memoryDSN())
	if err != nil {
		return nil, err
	}
	d.SetMaxOpenConns(1)
	d.SetMaxIdleConns(1)
	d.SetConnMaxLifetime(0)
	d.SetConnMaxIdleTime(0)
	if err := d.PingContext(ctx); err != nil {
		d.Close()
		return nil, err
	}
	return &Store{write: d, read: d, memory: true}, nil
}

// Migrate applies embedded migrations and returns the current schema version.
// Idempotent; refuses (changing nothing) a database newer than the embedded migrations.
func (s *Store) Migrate(ctx context.Context) (int64, error) {
	p, err := goose.NewProvider(goose.DialectSQLite3, s.write, migrations.FS)
	if err != nil {
		return 0, err
	}
	sources := p.ListSources()
	var latest int64
	if len(sources) > 0 {
		latest = sources[len(sources)-1].Version
	}
	cur, err := p.GetDBVersion(ctx)
	if err != nil {
		return 0, err
	}
	if cur > latest {
		return cur, fmt.Errorf("database schema version %d is newer than this binary supports (%d)", cur, latest)
	}
	if _, err := p.Up(ctx); err != nil {
		// A concurrent migrator may have won the race; accept if the schema is now current.
		if v, verr := p.GetDBVersion(ctx); verr == nil && v == latest {
			return v, nil
		}
		return 0, fmt.Errorf("migrate: %w", err)
	}
	return p.GetDBVersion(ctx)
}

// Close checkpoints the WAL (best effort) and closes both pools.
func (s *Store) Close() error {
	if !s.memory {
		_, _ = s.write.Exec("PRAGMA wal_checkpoint(TRUNCATE)")
	}
	err := s.write.Close()
	if s.read != s.write {
		err = errors.Join(err, s.read.Close())
	}
	return err
}

// QueryCount returns the number of statements issued through Queries handed out by this
// store so far (tests use deltas to prove batching).
func (s *Store) QueryCount() int64 { return s.count.Load() }

// Read returns Queries on the read pool; each statement is its own snapshot.
func (s *Store) Read() *db.Queries { return db.New(&counting{db: s.read, n: &s.count}) }

// WithReadTx runs fn in a read-only transaction: one consistent snapshot.
func (s *Store) WithReadTx(ctx context.Context, fn func(q *db.Queries) error) error {
	tx, err := s.read.BeginTx(ctx, &sql.TxOptions{ReadOnly: !s.memory})
	if err != nil {
		return err
	}
	defer tx.Rollback() //nolint:errcheck
	if err := fn(db.New(&counting{db: tx, n: &s.count})); err != nil {
		return err
	}
	return tx.Commit()
}

// WithTx runs fn in a write transaction (BEGIN IMMEDIATE). It rolls back on error or panic
// (re-raising the panic). Never nest: use only the q passed to fn.
func (s *Store) WithTx(ctx context.Context, fn func(q *db.Queries) error) (err error) {
	tx, err := s.write.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	committed := false
	defer func() {
		if !committed {
			_ = tx.Rollback()
		}
	}()
	if err := fn(db.New(&counting{db: tx, n: &s.count})); err != nil {
		return err
	}
	if err := tx.Commit(); err != nil {
		return err
	}
	committed = true
	return nil
}

// Exec runs a raw statement on the write pool (tests and maintenance only).
func (s *Store) Exec(ctx context.Context, query string, args ...any) error {
	_, err := s.write.ExecContext(ctx, query, args...)
	return err
}

// RawQueryRow runs a raw single-row query on the write pool (tests and maintenance only;
// never call it from inside a WithTx callback).
func (s *Store) RawQueryRow(ctx context.Context, query string, args ...any) *sql.Row {
	return s.write.QueryRowContext(ctx, query, args...)
}

// IsUniqueViolation reports a SQLite UNIQUE or PRIMARY KEY constraint failure (codes 2067, 1555).
func IsUniqueViolation(err error) bool {
	var se *sqlite.Error
	if errors.As(err, &se) {
		c := se.Code()
		return c == 2067 || c == 1555
	}
	return false
}

// IsUniqueViolationOn is IsUniqueViolation restricted to errors mentioning column, e.g. "projects.key".
func IsUniqueViolationOn(err error, column string) bool {
	return IsUniqueViolation(err) && containsFold(err.Error(), column)
}

// counting wraps a DBTX and counts statements.
type counting struct {
	db interface {
		ExecContext(context.Context, string, ...interface{}) (sql.Result, error)
		PrepareContext(context.Context, string) (*sql.Stmt, error)
		QueryContext(context.Context, string, ...interface{}) (*sql.Rows, error)
		QueryRowContext(context.Context, string, ...interface{}) *sql.Row
	}
	n *atomic.Int64
}

func (c *counting) ExecContext(ctx context.Context, q string, a ...interface{}) (sql.Result, error) {
	c.n.Add(1)
	return c.db.ExecContext(ctx, q, a...)
}
func (c *counting) PrepareContext(ctx context.Context, q string) (*sql.Stmt, error) {
	return c.db.PrepareContext(ctx, q)
}
func (c *counting) QueryContext(ctx context.Context, q string, a ...interface{}) (*sql.Rows, error) {
	c.n.Add(1)
	return c.db.QueryContext(ctx, q, a...)
}
func (c *counting) QueryRowContext(ctx context.Context, q string, a ...interface{}) *sql.Row {
	c.n.Add(1)
	return c.db.QueryRowContext(ctx, q, a...)
}
