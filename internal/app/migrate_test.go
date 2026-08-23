package app

import (
	"os"
	"path/filepath"
	"testing"
)

func TestDiscoverMigrations(t *testing.T) {
	dir := t.TempDir()
	for _, name := range []string{"010_tenth.up.sql", "README.md", "002_second.up.sql"} {
		if err := os.WriteFile(filepath.Join(dir, name), nil, 0o600); err != nil {
			t.Fatal(err)
		}
	}

	migrations, err := discoverMigrations(dir)
	if err != nil {
		t.Fatal(err)
	}
	if len(migrations) != 2 {
		t.Fatalf("migration count = %d", len(migrations))
	}
	if migrations[0].Version != 2 || migrations[1].Version != 10 {
		t.Fatalf("migration versions = %d, %d", migrations[0].Version, migrations[1].Version)
	}
}
