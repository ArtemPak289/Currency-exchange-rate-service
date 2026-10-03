package postgres

import (
	"context"
	"database/sql"
	"embed"
	"fmt"
	"log/slog"
)

//go:embed migrations/*.sql
var migrationFS embed.FS

// RunMigrations applies embedded database migrations automatically.
func RunMigrations(ctx context.Context, db *sql.DB) error {
	slog.Info("Running database migrations...")

	// 1. Create schema_migrations table if it doesn't exist
	createTableSQL := `
		CREATE TABLE IF NOT EXISTS schema_migrations (
			version VARCHAR(255) PRIMARY KEY,
			applied_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
		);
	`
	if _, err := db.ExecContext(ctx, createTableSQL); err != nil {
		return fmt.Errorf("failed to create schema_migrations table: %w", err)
	}

	// 2. Read migration file
	content, err := migrationFS.ReadFile("migrations/000001_init.up.sql")
	if err != nil {
		return fmt.Errorf("failed to read migration file: %w", err)
	}

	version := "000001_init"

	// 3. Check if already applied
	var exists bool
	checkSQL := `SELECT EXISTS(SELECT 1 FROM schema_migrations WHERE version = $1);`
	if err := db.QueryRowContext(ctx, checkSQL, version).Scan(&exists); err != nil {
		return fmt.Errorf("failed to check migration status: %w", err)
	}

	if exists {
		slog.Info("Migration already applied", "version", version)
		return nil
	}

	// 4. Apply migration within a transaction
	tx, err := db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("failed to begin transaction: %w", err)
	}
	defer tx.Rollback()

	if _, err := tx.ExecContext(ctx, string(content)); err != nil {
		return fmt.Errorf("failed to execute migration %s: %w", version, err)
	}

	recordSQL := `INSERT INTO schema_migrations (version) VALUES ($1);`
	if _, err := tx.ExecContext(ctx, recordSQL, version); err != nil {
		return fmt.Errorf("failed to record migration %s: %w", version, err)
	}

	if err := tx.Commit(); err != nil {
		return fmt.Errorf("failed to commit migration %s: %w", version, err)
	}

	slog.Info("Successfully applied database migration", "version", version)
	return nil
}
