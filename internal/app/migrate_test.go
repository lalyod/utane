package app

import (
	"testing"
	"testing/fstest"
)

func TestDiscoverMigrations(t *testing.T) {
	dir := fstest.MapFS{
		"010_tenth.up.sql":  {},
		"README.md":         {},
		"002_second.up.sql": {},
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
