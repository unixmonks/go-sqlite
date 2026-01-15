package sqlite_test

import (
	"context"
	"testing"
	"testing/fstest"
	"time"

	"github.com/unixmonks/go-sqlite"
)

func TestDB_OpenClose(t *testing.T) {
	db := sqlite.NewDB(":memory:")
	if err := db.Open(); err != nil {
		t.Fatalf("failed to open db: %v", err)
	}
	if err := db.Close(); err != nil {
		t.Fatalf("failed to close db: %v", err)
	}
}

func TestDB_OpenEmptyDSN(t *testing.T) {
	db := sqlite.NewDB("")
	if err := db.Open(); err == nil {
		t.Fatal("expected error for empty DSN")
	}
}

func TestDB_Migrate(t *testing.T) {
	db := sqlite.NewDB(":memory:")
	if err := db.Open(); err != nil {
		t.Fatalf("failed to open db: %v", err)
	}
	defer db.Close()

	migrationFS := fstest.MapFS{
		"migration/00001_init.sql": &fstest.MapFile{
			Data: []byte(`CREATE TABLE users (id INTEGER PRIMARY KEY, name TEXT);`),
		},
		"migration/00002_posts.sql": &fstest.MapFile{
			Data: []byte(`CREATE TABLE posts (id INTEGER PRIMARY KEY, user_id INTEGER, title TEXT);`),
		},
	}

	if err := db.Migrate(migrationFS); err != nil {
		t.Fatalf("failed to migrate: %v", err)
	}

	// Run again to verify idempotency
	if err := db.Migrate(migrationFS); err != nil {
		t.Fatalf("failed to migrate again: %v", err)
	}

	// Verify tables exist by writing and reading
	tx, err := db.BeginTx(context.Background(), nil)
	if err != nil {
		t.Fatalf("failed to begin tx: %v", err)
	}
	defer tx.Rollback()

	if _, err := tx.Exec(`INSERT INTO users (name) VALUES ('test')`); err != nil {
		t.Fatalf("failed to insert user: %v", err)
	}
	if err := tx.Commit(); err != nil {
		t.Fatalf("failed to commit: %v", err)
	}
}

func TestDB_BeginTx(t *testing.T) {
	db := sqlite.NewDB(":memory:")
	if err := db.Open(); err != nil {
		t.Fatalf("failed to open db: %v", err)
	}
	defer db.Close()

	migrationFS := fstest.MapFS{
		"migration/00001_init.sql": &fstest.MapFile{
			Data: []byte(`CREATE TABLE items (id INTEGER PRIMARY KEY, name TEXT);`),
		},
	}
	if err := db.Migrate(migrationFS); err != nil {
		t.Fatalf("failed to migrate: %v", err)
	}

	tx, err := db.BeginTx(context.Background(), nil)
	if err != nil {
		t.Fatalf("failed to begin tx: %v", err)
	}

	if _, err := tx.Exec(`INSERT INTO items (name) VALUES ('item1')`); err != nil {
		tx.Rollback()
		t.Fatalf("failed to insert: %v", err)
	}

	if err := tx.Commit(); err != nil {
		t.Fatalf("failed to commit: %v", err)
	}

	// Verify with read tx
	readTx, err := db.BeginReadTx(context.Background())
	if err != nil {
		t.Fatalf("failed to begin read tx: %v", err)
	}
	defer readTx.Rollback()

	var count int
	if err := readTx.QueryRow(`SELECT COUNT(*) FROM items`).Scan(&count); err != nil {
		t.Fatalf("failed to query: %v", err)
	}
	if count != 1 {
		t.Fatalf("expected 1 item, got %d", count)
	}
}

func TestTx_Now(t *testing.T) {
	db := sqlite.NewDB(":memory:")
	fixedTime := time.Date(2025, 1, 15, 12, 0, 0, 0, time.UTC)
	db.Now = func() time.Time { return fixedTime }

	if err := db.Open(); err != nil {
		t.Fatalf("failed to open db: %v", err)
	}
	defer db.Close()

	tx, err := db.BeginTx(context.Background(), nil)
	if err != nil {
		t.Fatalf("failed to begin tx: %v", err)
	}
	defer tx.Rollback()

	if !tx.Now().Equal(fixedTime) {
		t.Fatalf("expected tx.Now() = %v, got %v", fixedTime, tx.Now())
	}
}

func TestNullTime_ScanValue(t *testing.T) {
	t.Run("nil", func(t *testing.T) {
		var nt sqlite.NullTime
		if err := nt.Scan(nil); err != nil {
			t.Fatalf("failed to scan nil: %v", err)
		}
		if !time.Time(nt).IsZero() {
			t.Fatal("expected zero time for nil")
		}
	})

	t.Run("string", func(t *testing.T) {
		var nt sqlite.NullTime
		if err := nt.Scan("2025-01-15T12:00:00Z"); err != nil {
			t.Fatalf("failed to scan string: %v", err)
		}
		expected := time.Date(2025, 1, 15, 12, 0, 0, 0, time.UTC)
		if !time.Time(nt).Equal(expected) {
			t.Fatalf("expected %v, got %v", expected, time.Time(nt))
		}
	})

	t.Run("value_nil", func(t *testing.T) {
		var nt sqlite.NullTime
		v, err := nt.Value()
		if err != nil {
			t.Fatalf("failed to get value: %v", err)
		}
		if v != nil {
			t.Fatalf("expected nil value for zero time, got %v", v)
		}
	})

	t.Run("value_set", func(t *testing.T) {
		nt := sqlite.NullTime(time.Date(2025, 1, 15, 12, 0, 0, 0, time.UTC))
		v, err := nt.Value()
		if err != nil {
			t.Fatalf("failed to get value: %v", err)
		}
		expected := "2025-01-15T12:00:00Z"
		if v != expected {
			t.Fatalf("expected %q, got %q", expected, v)
		}
	})
}

func TestFormatLimitOffset(t *testing.T) {
	tests := []struct {
		limit, offset int
		want          string
	}{
		{0, 0, ""},
		{10, 0, "LIMIT 10"},
		{0, 5, "OFFSET 5"},
		{10, 5, "LIMIT 10 OFFSET 5"},
	}

	for _, tt := range tests {
		got := sqlite.FormatLimitOffset(tt.limit, tt.offset)
		if got != tt.want {
			t.Errorf("FormatLimitOffset(%d, %d) = %q, want %q", tt.limit, tt.offset, got, tt.want)
		}
	}
}
