package sqlite

import (
	"context"
	"database/sql"
	"database/sql/driver"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"time"

	_ "github.com/mattn/go-sqlite3"
)

type DB struct {
	rwDB   *sql.DB // Read-write pool: single connection, BEGIN IMMEDIATE
	roDB   *sql.DB // Read-only pool: multiple connections, BEGIN DEFERRED
	ctx    context.Context
	cancel func()

	DSN string
	Now func() time.Time
}

func NewDB(dsn string) *DB {
	db := &DB{
		DSN: dsn,
		Now: time.Now,
	}
	db.ctx, db.cancel = context.WithCancel(context.Background())
	return db
}

func (db *DB) Open() (err error) {
	if db.DSN == "" {
		return fmt.Errorf("dsn required")
	}

	if db.DSN == ":memory:" {
		return db.openMemory()
	}

	if err := os.MkdirAll(filepath.Dir(db.DSN), 0700); err != nil {
		return err
	}

	baseDSN := db.DSN + "?_journal_mode=wal&_foreign_keys=on&_busy_timeout=5000"

	if db.rwDB, err = sql.Open("sqlite3", baseDSN); err != nil {
		return err
	}
	db.rwDB.SetMaxOpenConns(1)
	db.rwDB.SetMaxIdleConns(1)
	db.rwDB.SetConnMaxLifetime(0)
	db.rwDB.SetConnMaxIdleTime(0)

	roDSN := baseDSN + "&mode=ro"
	if db.roDB, err = sql.Open("sqlite3", roDSN); err != nil {
		db.rwDB.Close()
		return err
	}
	db.roDB.SetMaxOpenConns(10)
	db.roDB.SetMaxIdleConns(5)

	return nil
}

func (db *DB) openMemory() (err error) {
	dsn := "file::memory:?cache=shared&_foreign_keys=on"

	if db.rwDB, err = sql.Open("sqlite3", dsn); err != nil {
		return err
	}
	db.rwDB.SetMaxOpenConns(1)
	db.rwDB.SetMaxIdleConns(1)
	db.rwDB.SetConnMaxLifetime(0)
	db.rwDB.SetConnMaxIdleTime(0)

	if db.roDB, err = sql.Open("sqlite3", dsn); err != nil {
		db.rwDB.Close()
		return err
	}
	db.roDB.SetMaxOpenConns(10)
	db.roDB.SetMaxIdleConns(5)

	return nil
}

// Migrate runs all SQL migrations from the provided filesystem.
// Migration files should be in a "migration" subdirectory and named with a numeric prefix
// for ordering (e.g., "migration/00001_init.sql", "migration/00002_users.sql").
// Each migration runs once and is tracked in a migrations table.
func (db *DB) Migrate(migrationFS fs.FS) error {
	if _, err := db.rwDB.Exec(`CREATE TABLE IF NOT EXISTS migrations (name TEXT PRIMARY KEY);`); err != nil {
		return fmt.Errorf("cannot create migrations table: %w", err)
	}

	names, err := fs.Glob(migrationFS, "migration/*.sql")
	if err != nil {
		return err
	}
	sort.Strings(names)

	for _, name := range names {
		if err := db.migrateFile(migrationFS, name); err != nil {
			return fmt.Errorf("migration error: name=%q err=%w", name, err)
		}
	}
	return nil
}

func (db *DB) migrateFile(migrationFS fs.FS, name string) error {
	tx, err := db.rwDB.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()

	var n int
	if err := tx.QueryRow(`SELECT COUNT(*) FROM migrations WHERE name = ?`, name).Scan(&n); err != nil {
		return err
	} else if n != 0 {
		return nil
	}

	if buf, err := fs.ReadFile(migrationFS, name); err != nil {
		return err
	} else if _, err := tx.Exec(string(buf)); err != nil {
		return err
	}

	if _, err := tx.Exec(`INSERT INTO migrations (name) VALUES (?)`, name); err != nil {
		return err
	}

	return tx.Commit()
}

func (db *DB) Close() error {
	db.cancel()

	var errs []error
	if db.rwDB != nil {
		if err := db.rwDB.Close(); err != nil {
			errs = append(errs, err)
		}
	}
	if db.roDB != nil {
		if err := db.roDB.Close(); err != nil {
			errs = append(errs, err)
		}
	}
	if len(errs) > 0 {
		return errs[0]
	}
	return nil
}

// BeginTx starts a read-write transaction using BEGIN IMMEDIATE.
// Use this for any operation that may write to the database.
func (db *DB) BeginTx(ctx context.Context, opts *sql.TxOptions) (*Tx, error) {
	tx, err := db.rwDB.BeginTx(ctx, opts)
	if err != nil {
		return nil, err
	}

	if _, err := tx.ExecContext(ctx, "ROLLBACK; BEGIN IMMEDIATE"); err != nil {
		tx.Rollback()
		return nil, err
	}

	return &Tx{
		Tx:  tx,
		db:  db,
		now: db.Now().UTC().Truncate(time.Second),
	}, nil
}

// BeginReadTx starts a read-only transaction using BEGIN DEFERRED.
// Use this for operations that only read from the database.
func (db *DB) BeginReadTx(ctx context.Context) (*Tx, error) {
	tx, err := db.roDB.BeginTx(ctx, &sql.TxOptions{ReadOnly: true})
	if err != nil {
		return nil, err
	}

	return &Tx{
		Tx:  tx,
		db:  db,
		now: db.Now().UTC().Truncate(time.Second),
	}, nil
}

type Tx struct {
	*sql.Tx
	db  *DB
	now time.Time
}

// Now returns the transaction's timestamp, frozen at transaction start.
func (tx *Tx) Now() time.Time {
	return tx.now
}

type NullTime time.Time

func (n *NullTime) Scan(value interface{}) error {
	if value == nil {
		*(*time.Time)(n) = time.Time{}
		return nil
	} else if value, ok := value.(string); ok {
		*(*time.Time)(n), _ = time.Parse(time.RFC3339, value)
		return nil
	}
	return fmt.Errorf("NullTime: cannot scan to time.Time: %T", value)
}

func (n *NullTime) Value() (driver.Value, error) {
	if n == nil || (*time.Time)(n).IsZero() {
		return nil, nil
	}
	return (*time.Time)(n).UTC().Format(time.RFC3339), nil
}

func FormatLimitOffset(limit, offset int) string {
	if limit > 0 && offset > 0 {
		return fmt.Sprintf(`LIMIT %d OFFSET %d`, limit, offset)
	} else if limit > 0 {
		return fmt.Sprintf(`LIMIT %d`, limit)
	} else if offset > 0 {
		return fmt.Sprintf(`OFFSET %d`, offset)
	}
	return ""
}
