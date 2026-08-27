package sqlite_test

import (
	"context"
	"database/sql"
	"path/filepath"
	"testing"

	storage "github.com/zhanglei10281852-gif/autodrive-fleet-orchestrator/internal/storage/sqlite"
)

func TestTransactionPanicRollsBackBeforePropagation(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	store, err := storage.Open(ctx, filepath.Join(t.TempDir(), "panic.db"))
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	defer store.Close()

	panicValue := "callback failed after write"
	func() {
		defer func() {
			if recovered := recover(); recovered != panicValue {
				t.Fatalf("recovered=%v, want original panic %q", recovered, panicValue)
			}
		}()
		_ = store.WithTx(ctx, func(tx *sql.Tx) error {
			if _, err := tx.ExecContext(ctx, "CREATE TABLE panic_probe (id INTEGER PRIMARY KEY, value TEXT NOT NULL)"); err != nil {
				t.Fatalf("create panic probe: %v", err)
			}
			if _, err := tx.ExecContext(ctx, "INSERT INTO panic_probe(id, value) VALUES(1, 'partial')"); err != nil {
				t.Fatalf("insert panic probe: %v", err)
			}
			panic(panicValue)
		})
	}()

	var panicTableCount int
	if err := store.DB().QueryRowContext(ctx,
		"SELECT COUNT(*) FROM sqlite_master WHERE type = 'table' AND name = 'panic_probe'").Scan(&panicTableCount); err != nil {
		t.Fatalf("inspect panic probe: %v", err)
	}
	if panicTableCount != 0 {
		t.Fatalf("panic transaction persisted table count=%d, want rollback", panicTableCount)
	}

	if err := store.WithTx(ctx, func(tx *sql.Tx) error {
		_, err := tx.ExecContext(ctx, "CREATE TABLE committed_probe (id INTEGER PRIMARY KEY)")
		return err
	}); err != nil {
		t.Fatalf("commit legal transaction: %v", err)
	}
	var committedTableCount int
	if err := store.DB().QueryRowContext(ctx,
		"SELECT COUNT(*) FROM sqlite_master WHERE type = 'table' AND name = 'committed_probe'").Scan(&committedTableCount); err != nil {
		t.Fatalf("inspect committed probe: %v", err)
	}
	if committedTableCount != 1 {
		t.Fatalf("legal transaction table count=%d, want one", committedTableCount)
	}
}
