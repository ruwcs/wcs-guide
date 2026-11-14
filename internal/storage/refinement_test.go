package storage

import (
	"context"
	"database/sql"
	"path/filepath"
	"reflect"
	"testing"
	"testing/fstest"
	"time"
)

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

func TestFailedCommitAndCancellationRecovery(t *testing.T) {
	store, _ := openTest(t)
	source := makeSource(t, store, "first")
	fetch(t, store, source.Source.ID, "A", clock())
	page := makePage(t, store, source.Source.ID)
	block := page.Page.Blocks[1]
	token, result := prepare(t, store, block.ID, *block.Import, "содержимое")
	must(t, store.ApplyPrepared(ctx, token, result, clock().Add(2*time.Hour)))
	other := makeSource(t, store, "second")
	err := store.withTx(ctx, func(tx *sql.Tx) error {
		_, err := tx.ExecContext(ctx, "UPDATE blocks SET source_id=? WHERE id=?", other.Source.ID, block.ID)
		must(t, err) // Нарушение отложенного FK обнаруживается только при COMMIT.
		return nil
	})
	wantError(t, err, ErrConflict)
	restored, err := store.GetPage(ctx, page.Page.ID)
	must(t, err)
	if restored.Page.Blocks[1].Import.SourceID != source.Source.ID {
		t.Fatal("COMMIT сохранил изменения")
	}
	makeSource(t, store, "after-commit")
	canceled, cancel := context.WithCancel(ctx)
	err = store.withTx(canceled, func(tx *sql.Tx) error {
		_, err := tx.ExecContext(canceled, "UPDATE sources SET name='temporary' WHERE id=?", source.Source.ID)
		if err != nil {
			return err
		}
		cancel()
		return canceled.Err()
	})
	wantError(t, err, context.Canceled)
	current, err := store.GetSource(ctx, source.Source.ID)
	must(t, err)
	if current.Source.Name != source.Source.Name {
		t.Fatal("отмена сохранила изменения")
	}
	makeSource(t, store, "after-cancel")
}

func TestScopedPublicUpdatesAndBackwardClock(t *testing.T) {
	store, _ := openTest(t)
	source := makeSource(t, store, "source")
	fetch(t, store, source.Source.ID, "A", clock())
	page := makePage(t, store, source.Source.ID)
	token, result := prepare(t, store, page.Page.Blocks[1].ID, *page.Page.Blocks[1].Import, "материал")
	must(t, store.ApplyPrepared(ctx, token, result, clock().Add(2*time.Hour)))
	must(t, store.PublishPage(ctx, page.Page.ID, page.EditRevision, clock().Add(3*time.Hour)))
	// Вторая страница того же источника получает собственную дату.
	secondInput := page.Page
	secondInput.ID = 0
	secondInput.Path = "/second"
	for i := range secondInput.Blocks {
		secondInput.Blocks[i].ID = 0
		secondInput.Blocks[i].PageID = 0
	}
	second, err := store.CreatePage(ctx, secondInput)
	must(t, err)
	token, result = prepare(t, store, second.Page.Blocks[1].ID, *second.Page.Blocks[1].Import, "другой материал")
	must(t, store.ApplyPrepared(ctx, token, result, clock().Add(2*time.Hour)))
	must(t, store.PublishPage(ctx, second.Page.ID, second.EditRevision, clock().Add(4*time.Hour)))
	thirdInput := secondInput
	thirdInput.Path = "/third"
	thirdInput.Blocks = nil
	third, err := store.CreatePage(ctx, thirdInput)
	must(t, err)
	must(t, store.PublishPage(ctx, third.Page.ID, third.EditRevision, clock().Add(3*time.Hour)))
	_, err = store.db.Exec("UPDATE pages SET public_updated_at='broken' WHERE id=?", third.Page.ID)
	must(t, err)
	// Несвязанная некорректная дата не препятствует изменению источника.
	current, err := store.GetSource(ctx, source.Source.ID)
	must(t, err)
	current.Source.Name = "Новое название"
	_, err = store.UpdateSource(ctx, current.Source, current.EditRevision, clock().Add(5*time.Hour))
	must(t, err)
	must(t, store.SetPublicationBlocked(ctx, source.Source.ID, true, clock()))
	for _, path := range []string{page.Page.Path, second.Page.Path} {
		public, err := store.GetPublicPage(ctx, path)
		must(t, err)
		if len(public.Blocks) != 1 || !public.UpdatedAt.Equal(clock().Add(5*time.Hour)) {
			t.Fatal(public)
		}
	}
	currentPage, err := store.GetPage(ctx, page.Page.ID)
	must(t, err)
	must(t, store.UnpublishPage(ctx, page.Page.ID, currentPage.EditRevision, clock().Add(-time.Hour)))
	_, err = store.GetPublicPage(ctx, page.Page.Path)
	wantError(t, err, ErrNotFound)
	// Устранение искусственного повреждения позволяет проверить публичный список.
	_, err = store.db.Exec("UPDATE pages SET public_updated_at=? WHERE id=?", stamp(clock()), third.Page.ID)
	must(t, err)
	summaries, err := store.ListPublicPages(ctx)
	must(t, err)
	if len(summaries) != 2 {
		t.Fatal(summaries)
	}
	for _, summary := range summaries {
		if summary.ID == page.Page.ID {
			t.Fatal("снятая страница в списке")
		}
	}
}

func TestUnblockingOnlyPublishesTargetSource(t *testing.T) {
	store, _ := openTest(t)
	source := makeSource(t, store, "first")
	fetch(t, store, source.Source.ID, "A", clock())
	page := makePage(t, store, source.Source.ID)
	token, result := prepare(t, store, page.Page.Blocks[1].ID, *page.Page.Blocks[1].Import, "контент")
	must(t, store.ApplyPrepared(ctx, token, result, clock().Add(2*time.Hour)))
	must(t, store.PublishPage(ctx, page.Page.ID, page.EditRevision, clock().Add(3*time.Hour)))
	other := makeSource(t, store, "other")
	// Допустимый готовый, но ещё не опубликованный результат другого источника.
	input := page.Page
	input.ID = 0
	input.Path = "/other"
	for i := range input.Blocks {
		input.Blocks[i].ID = 0
		input.Blocks[i].PageID = 0
	}
	input.Blocks[1].Import.SourceID = other.Source.ID
	fetch(t, store, other.Source.ID, "B", clock())
	another, err := store.CreatePage(ctx, input)
	must(t, err)
	token, result = prepare(t, store, another.Page.Blocks[1].ID, *another.Page.Blocks[1].Import, "закрытый результат")
	must(t, store.ApplyPrepared(ctx, token, result, clock().Add(2*time.Hour)))
	must(t, store.PublishPage(ctx, another.Page.ID, another.EditRevision, clock().Add(3*time.Hour)))
	_, err = store.db.Exec("UPDATE prepared_contents SET published_at=NULL WHERE block_id=?", another.Page.Blocks[1].ID)
	must(t, err)
	before, err := store.GetPage(ctx, another.Page.ID)
	must(t, err)
	must(t, store.SetPublicationBlocked(ctx, source.Source.ID, true, clock().Add(4*time.Hour)))
	must(t, store.SetPublicationBlocked(ctx, source.Source.ID, false, clock().Add(5*time.Hour)))
	after, err := store.GetPage(ctx, another.Page.ID)
	must(t, err)
	if !reflect.DeepEqual(before, after) {
		t.Fatal("изменён блок другого источника")
	}
}

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
