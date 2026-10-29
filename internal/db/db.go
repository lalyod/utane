package db

import (
	"context"
	"database/sql"

	_ "modernc.org/sqlite"
)

// DB wraps *sql.DB with context-first methods so callers keep the shape they had with pgx.
type DB struct {
	*sql.DB
}

type Tx struct {
	*sql.Tx
}

type Result struct {
	sql.Result
}

// Open opens the SQLite file at path. Foreign keys are off by default in SQLite and the schema
// relies on cascades; immediate transactions replace Postgres row locks (FOR UPDATE).
func Open(path string) (*DB, error) {
	conn, err := sql.Open("sqlite", path+
		"?_pragma=foreign_keys(1)&_pragma=journal_mode(WAL)&_pragma=busy_timeout(5000)&_txlock=immediate")
	if err != nil {
		return nil, err
	}
	if err := conn.Ping(); err != nil {
		conn.Close()

		return nil, err
	}

	return &DB{conn}, nil
}

func (d *DB) Close() { d.DB.Close() }

func (d *DB) Query(ctx context.Context, query string, args ...any) (*sql.Rows, error) {
	return d.QueryContext(ctx, query, args...)
}

func (d *DB) QueryRow(ctx context.Context, query string, args ...any) *sql.Row {
	return d.QueryRowContext(ctx, query, args...)
}

func (d *DB) Exec(ctx context.Context, query string, args ...any) (Result, error) {
	result, err := d.ExecContext(ctx, query, args...)

	return Result{result}, err
}

func (d *DB) Begin(ctx context.Context) (*Tx, error) {
	tx, err := d.BeginTx(ctx, nil)
	if err != nil {
		return nil, err
	}

	return &Tx{tx}, nil
}

func (t *Tx) QueryRow(ctx context.Context, query string, args ...any) *sql.Row {
	return t.QueryRowContext(ctx, query, args...)
}

func (t *Tx) Exec(ctx context.Context, query string, args ...any) (Result, error) {
	result, err := t.ExecContext(ctx, query, args...)

	return Result{result}, err
}

func (t *Tx) Commit(context.Context) error { return t.Tx.Commit() }

func (t *Tx) Rollback(context.Context) error { return t.Tx.Rollback() }

func (r Result) RowsAffected() int64 {
	n, _ := r.Result.RowsAffected()

	return n
}
