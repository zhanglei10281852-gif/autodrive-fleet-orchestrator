package sqlite_test

import (
	"context"
	"database/sql"
	"path/filepath"
	"testing"

	storage "github.com/zhanglei10281852-gif/autodrive-fleet-orchestrator/internal/storage/sqlite"
)

func TestRestartRejectsMigrationChecksumRewrite(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	tamperedPath := filepath.Join(t.TempDir(), "tampered.db")

	store, err := storage.Open(ctx, tamperedPath)
	if err != nil {
		t.Fatalf("create migrated database: %v", err)
	}
	var version string
	if err := store.DB().QueryRowContext(ctx,
		"SELECT version FROM schema_migrations ORDER BY version LIMIT 1").Scan(&version); err != nil {
		store.Close()
		t.Fatalf("select applied migration: %v", err)
	}
	const tamperedChecksum = "operator-tampered-checksum"
	if _, err := store.DB().ExecContext(ctx,
		"UPDATE schema_migrations SET checksum = ? WHERE version = ?",
		tamperedChecksum, version); err != nil {
		store.Close()
		t.Fatalf("tamper migration ledger: %v", err)
	}
	if err := store.Close(); err != nil {
		t.Fatalf("close tampered database: %v", err)
	}

	reopened, err := storage.Open(ctx, tamperedPath)
	if reopened != nil {
		reopened.Close()
	}
	if err == nil {
		t.Fatal("restart succeeded after an applied migration checksum changed")
	}

	raw, err := sql.Open("sqlite", "file:"+filepath.ToSlash(tamperedPath))
	if err != nil {
		t.Fatalf("open ledger for inspection: %v", err)
	}
	defer raw.Close()
	var persistedChecksum string
	if err := raw.QueryRowContext(ctx,
		"SELECT checksum FROM schema_migrations WHERE version = ?", version).Scan(&persistedChecksum); err != nil {
		t.Fatalf("read ledger after rejected restart: %v", err)
	}
	if persistedChecksum != tamperedChecksum {
		t.Fatalf("restart mutated unverifiable migration history: got %q, want %q", persistedChecksum, tamperedChecksum)
	}

	intactPath := filepath.Join(t.TempDir(), "intact.db")
	intact, err := storage.Open(ctx, intactPath)
	if err != nil {
		t.Fatalf("create intact database: %v", err)
	}
	if err := intact.Close(); err != nil {
		t.Fatalf("close intact database: %v", err)
	}
	intact, err = storage.Open(ctx, intactPath)
	if err != nil {
		t.Fatalf("restart intact database: %v", err)
	}
	if err := intact.Close(); err != nil {
		t.Fatalf("close restarted intact database: %v", err)
	}
}
