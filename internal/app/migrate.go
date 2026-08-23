package app

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/joho/godotenv"

	"github.com/lalyod/mysong/internal/db"
)

type migration struct {
	Version int64
	Name    string
	Path    string
}

func Migrate() error {
	_ = godotenv.Load()

	ctx := context.Background()
	pool, err := db.New(ctx)
	if err != nil {
		return err
	}
	defer pool.Close()

	return migrateUp(ctx, pool, "migrations")
}

func migrateUp(ctx context.Context, pool *pgxpool.Pool, dir string) error {
	migrations, err := discoverMigrations(dir)
	if err != nil {
		return err
	}

	_, err = pool.Exec(ctx, `
		CREATE TABLE IF NOT EXISTS schema_migrations (
			version BIGINT PRIMARY KEY,
			name TEXT NOT NULL,
			applied_at TIMESTAMPTZ NOT NULL DEFAULT now()
		)
	`)
	if err != nil {
		return fmt.Errorf("create schema_migrations: %w", err)
	}

	if err := baselineInitialMigration(ctx, pool, migrations); err != nil {
		return err
	}

	applied := 0
	for _, migration := range migrations {
		var exists bool
		if err := pool.QueryRow(ctx, `
			SELECT EXISTS (SELECT 1 FROM schema_migrations WHERE version = $1)
		`, migration.Version).Scan(&exists); err != nil {
			return fmt.Errorf("check migration %s: %w", migration.Name, err)
		}
		if exists {
			continue
		}

		if err := applyMigration(ctx, pool, migration); err != nil {
			return err
		}
		fmt.Printf("applied %s\n", migration.Name)
		applied++
	}

	if applied == 0 {
		fmt.Println("database is up to date")
	}

	return nil
}

func baselineInitialMigration(ctx context.Context, pool *pgxpool.Pool, migrations []migration) error {
	var schemaExists bool
	var schemaComplete bool
	if err := pool.QueryRow(ctx, `
		SELECT
			to_regclass('public.artists') IS NOT NULL
			OR to_regclass('public.albums') IS NOT NULL
			OR to_regclass('public.tracks') IS NOT NULL,
			to_regclass('public.artists') IS NOT NULL
			AND to_regclass('public.albums') IS NOT NULL
			AND to_regclass('public.tracks') IS NOT NULL
	`).Scan(&schemaExists, &schemaComplete); err != nil {
		return fmt.Errorf("check existing schema: %w", err)
	}
	if !schemaExists {
		return nil
	}
	if !schemaComplete {
		return errors.New("existing music schema is incomplete; cannot baseline migration 001")
	}

	for _, migration := range migrations {
		if migration.Version != 1 {
			continue
		}
		_, err := pool.Exec(ctx, `
			INSERT INTO schema_migrations (version, name)
			VALUES ($1, $2)
			ON CONFLICT (version) DO NOTHING
		`, migration.Version, migration.Name)
		if err != nil {
			return fmt.Errorf("baseline migration %s: %w", migration.Name, err)
		}
		break
	}

	return nil
}

func applyMigration(ctx context.Context, pool *pgxpool.Pool, migration migration) error {
	sql, err := os.ReadFile(migration.Path)
	if err != nil {
		return fmt.Errorf("read migration %s: %w", migration.Name, err)
	}

	tx, err := pool.Begin(ctx)
	if err != nil {
		return fmt.Errorf("begin migration %s: %w", migration.Name, err)
	}
	defer tx.Rollback(ctx)

	if _, err := tx.Exec(ctx, string(sql)); err != nil {
		return fmt.Errorf("apply migration %s: %w", migration.Name, err)
	}
	if _, err := tx.Exec(ctx, `
		INSERT INTO schema_migrations (version, name)
		VALUES ($1, $2)
	`, migration.Version, migration.Name); err != nil {
		return fmt.Errorf("record migration %s: %w", migration.Name, err)
	}
	if err := tx.Commit(ctx); err != nil {
		return fmt.Errorf("commit migration %s: %w", migration.Name, err)
	}

	return nil
}

func discoverMigrations(dir string) ([]migration, error) {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return nil, fmt.Errorf("read migrations: %w", err)
	}

	migrations := make([]migration, 0, len(entries))
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

		migrations = append(migrations, migration{
			Version: version,
			Name:    name,
			Path:    filepath.Join(dir, entry.Name()),
		})
	}

	sort.Slice(migrations, func(i int, j int) bool {
		return migrations[i].Version < migrations[j].Version
	})
	for index := 1; index < len(migrations); index++ {
		if migrations[index].Version == migrations[index-1].Version {
			return nil, fmt.Errorf("duplicate migration version %d", migrations[index].Version)
		}
	}

	return migrations, nil
}
