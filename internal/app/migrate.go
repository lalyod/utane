package app

import (
	"context"
	"fmt"
	"io/fs"
	"sort"
	"strconv"
	"strings"

	"github.com/lalyod/utane/internal/db"
)

type migration struct {
	Version int64
	Name    string
	File    string
}

func migrateUp(ctx context.Context, conn *db.DB, migrations fs.FS) error {
	found, err := discoverMigrations(migrations)
	if err != nil {
		return err
	}

	_, err = conn.Exec(ctx, `
		CREATE TABLE IF NOT EXISTS schema_migrations (
			version BIGINT PRIMARY KEY,
			name TEXT NOT NULL,
			applied_at TIMESTAMP NOT NULL DEFAULT CURRENT_TIMESTAMP
		)
	`)
	if err != nil {
		return fmt.Errorf("create schema_migrations: %w", err)
	}

	for _, migration := range found {
		var exists bool
		if err := conn.QueryRow(ctx, `
			SELECT EXISTS (SELECT 1 FROM schema_migrations WHERE version = ?1)
		`, migration.Version).Scan(&exists); err != nil {
			return fmt.Errorf("check migration %s: %w", migration.Name, err)
		}
		if exists {
			continue
		}

		if err := applyMigration(ctx, conn, migrations, migration); err != nil {
			return err
		}
	}

	return nil
}

func applyMigration(ctx context.Context, conn *db.DB, migrations fs.FS, migration migration) error {
	sql, err := fs.ReadFile(migrations, migration.File)
	if err != nil {
		return fmt.Errorf("read migration %s: %w", migration.Name, err)
	}

	tx, err := conn.Begin(ctx)
	if err != nil {
		return fmt.Errorf("begin migration %s: %w", migration.Name, err)
	}
	defer tx.Rollback(ctx)

	if _, err := tx.Exec(ctx, string(sql)); err != nil {
		return fmt.Errorf("apply migration %s: %w", migration.Name, err)
	}
	if _, err := tx.Exec(ctx, `
		INSERT INTO schema_migrations (version, name)
		VALUES (?1, ?2)
	`, migration.Version, migration.Name); err != nil {
		return fmt.Errorf("record migration %s: %w", migration.Name, err)
	}
	if err := tx.Commit(ctx); err != nil {
		return fmt.Errorf("commit migration %s: %w", migration.Name, err)
	}

	return nil
}

func discoverMigrations(migrations fs.FS) ([]migration, error) {
	entries, err := fs.ReadDir(migrations, ".")
	if err != nil {
		return nil, fmt.Errorf("read migrations: %w", err)
	}

	found := make([]migration, 0, len(entries))
	for _, entry := range entries {
		if entry.IsDir() || !strings.HasSuffix(entry.Name(), ".up.sql") {
			continue
		}

		name := strings.TrimSuffix(entry.Name(), ".up.sql")
		versionText, _, ok := strings.Cut(name, "_")
		version, err := strconv.ParseInt(versionText, 10, 64)
		if !ok || err != nil || version <= 0 {
			return nil, fmt.Errorf("invalid migration filename %q", entry.Name())
		}

		found = append(found, migration{
			Version: version,
			Name:    name,
			File:    entry.Name(),
		})
	}

	sort.Slice(found, func(i int, j int) bool {
		return found[i].Version < found[j].Version
	})
	for index := 1; index < len(found); index++ {
		if found[index].Version == found[index-1].Version {
			return nil, fmt.Errorf("duplicate migration version %d", found[index].Version)
		}
	}

	return found, nil
}
