package storage

import (
	"database/sql"
	"path/filepath"
	"testing"
	"testing/fstest"
)

// TestSequentialMigrations проверяет последовательное применение миграций.
func TestSequentialMigrations(t *testing.T) {
	path := filepath.Join(t.TempDir(), "migration.db")
	db, err := sql.Open("sqlite", path)
	must(t, err)
	defer db.Close()
	db.SetMaxOpenConns(1)
	store := &Storage{db: db}
	files := fstest.MapFS{
		"migrations/001_initial.sql": &fstest.MapFile{Data: []byte("CREATE TABLE first(id INTEGER PRIMARY KEY) STRICT; INSERT INTO first VALUES(1);")},
		"migrations/002_next.sql":    &fstest.MapFile{Data: []byte("CREATE TABLE second(id INTEGER) STRICT; INVALID SQL;")},
	}
	if err := store.migrate(ctx, files); err == nil {
		t.Fatal("ожидалась ошибка миграции")
	}
	var version, count int
	must(t, db.QueryRow("PRAGMA user_version").Scan(&version))
	if version != 1 {
		t.Fatal(version)
	}
	must(t, db.QueryRow("SELECT count(*) FROM sqlite_schema WHERE name='second'").Scan(&count))
	if count != 0 {
		t.Fatal("частичная миграция")
	}
	files["migrations/002_next.sql"].Data = []byte("CREATE TABLE second(id INTEGER) STRICT;")
	must(t, store.migrate(ctx, files))
	must(t, store.migrate(ctx, files))
	must(t, db.QueryRow("SELECT count(*) FROM first").Scan(&count))
	if count != 1 {
		t.Fatal(count)
	}
	must(t, db.Close())
	db, err = sql.Open("sqlite", path)
	must(t, err)
	defer db.Close()
	store.db = db
	must(t, store.migrate(ctx, files))
	must(t, db.QueryRow("PRAGMA user_version").Scan(&version))
	if version != 2 {
		t.Fatal(version)
	}
}

// TestLegacySchemaRemainsUntouched проверяет сохранность неподдерживаемой экспериментальной БД.
func TestLegacySchemaRemainsUntouched(t *testing.T) {
	path := filepath.Join(t.TempDir(), "legacy.db")
	db, err := sql.Open("sqlite", path)
	must(t, err)
	_, err = db.Exec("CREATE TABLE storage_meta(id INTEGER PRIMARY KEY,schema_hash TEXT); INSERT INTO storage_meta VALUES(1,'original')")
	must(t, err)
	must(t, db.Close())
	_, err = Open(ctx, path)
	wantError(t, err, ErrSchemaIncompatible)
	db, err = sql.Open("sqlite", path)
	must(t, err)
	defer db.Close()
	var hash string
	var version int
	must(t, db.QueryRow("SELECT schema_hash FROM storage_meta WHERE id=1").Scan(&hash))
	must(t, db.QueryRow("PRAGMA user_version").Scan(&version))
	if hash != "original" || version != 0 {
		t.Fatal("изменена старая схема")
	}
}
