# go-sqlite

A lightweight SQLite wrapper for Go with dual connection pools and embedded migrations.

## Features

- **Dual connection pools** - Single writer (serialized), multiple readers (concurrent)
- **WAL mode** - Write-ahead logging enabled by default for better concurrency
- **Embedded migrations** - Run SQL migrations from `embed.FS` with tracking
- **In-memory support** - Shared cache for testing with both read/write pools
- **Transaction helpers** - `BeginTx()` for writes (BEGIN IMMEDIATE), `BeginReadTx()` for reads

## Install

```bash
go get github.com/unixmonks/go-sqlite
```

## Usage

```go
package main

import (
    "context"
    "embed"
    "log"

    "github.com/unixmonks/go-sqlite"
)

//go:embed migration/*.sql
var migrationFS embed.FS

func main() {
    db := sqlite.NewDB("./data.db")
    if err := db.Open(); err != nil {
        log.Fatal(err)
    }
    defer db.Close()

    // Run migrations (optional)
    if err := db.Migrate(migrationFS); err != nil {
        log.Fatal(err)
    }

    // Write transaction
    tx, err := db.BeginTx(context.Background(), nil)
    if err != nil {
        log.Fatal(err)
    }
    tx.Exec(`INSERT INTO users (name) VALUES (?)`, "alice")
    tx.Commit()

    // Read transaction
    readTx, _ := db.BeginReadTx(context.Background())
    defer readTx.Rollback()
    var name string
    readTx.QueryRow(`SELECT name FROM users WHERE id = ?`, 1).Scan(&name)
}
```

## Migrations

Create SQL files in a `migration/` directory with numeric prefixes:

```
migration/
├── 00001_init.sql
├── 00002_add_users.sql
└── 00003_add_posts.sql
```

Each migration runs once and is tracked in a `migrations` table.

## API

| Function | Description |
|----------|-------------|
| `NewDB(dsn)` | Create new DB instance |
| `db.Open()` | Open connection pools |
| `db.Migrate(fs)` | Run migrations from embedded filesystem |
| `db.Close()` | Close all connections |
| `db.BeginTx(ctx, opts)` | Start read-write transaction (BEGIN IMMEDIATE) |
| `db.BeginReadTx(ctx)` | Start read-only transaction |
| `tx.Now()` | Get frozen timestamp for the transaction |
| `NullTime` | Scanner/Valuer for nullable RFC3339 timestamps |
| `FormatLimitOffset(limit, offset)` | SQL pagination helper |

## Connection Settings

**Read-write pool:**
- Max 1 connection (serialized writes)
- BEGIN IMMEDIATE for immediate write lock

**Read-only pool:**
- Max 10 connections (concurrent reads)
- BEGIN DEFERRED

**Pragmas:**
- `journal_mode=wal`
- `foreign_keys=on`
- `busy_timeout=5000`

## License

MIT
