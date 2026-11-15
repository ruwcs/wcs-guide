package storage

import (
	"context"
	"database/sql"
	"errors"
	"path/filepath"
	"testing"
	"time"

	"github.com/ruwcs/wcs-guide/internal/domain"
)

// ctx — контекст тестовых операций.
var ctx = context.Background()

// clock возвращает тестовую дату.
func clock() time.Time { return time.Date(2026, 11, 15, 12, 0, 0, 0, time.UTC) }

// must проверяет успешное завершение операции.
func must(t *testing.T, err error) {
	t.Helper()
	if err != nil {
		t.Fatal(err)
	}
}

// wantError проверяет ожидаемую ошибку.
func wantError(t *testing.T, err, wantErr error) {
	t.Helper()
	if !errors.Is(err, wantErr) {
		t.Fatalf("ожидалась %v, получено %v", wantErr, err)
	}
}

// openTest открывает временное хранилище.
func openTest(t *testing.T) (*Storage, string) {
	t.Helper()
	path := filepath.Join(t.TempDir(), "test.db")
	store, err := Open(ctx, path)
	must(t, err)
	t.Cleanup(func() { store.Close() })
	return store, path
}

// makeSource создаёт тестовый источник.
func makeSource(t *testing.T, store *Storage, ref string) domain.Source {
	t.Helper()
	source, err := store.CreateSource(ctx, domain.Source{Name: "Источник", Type: domain.SourceTelegramPost, ExternalRef: ref})
	must(t, err)
	return source
}

// makePage создаёт страницу с ручным и импортируемым блоками.
func makePage(t *testing.T, store *Storage, sourceID int64) domain.Page {
	t.Helper()
	page, err := store.CreatePage(ctx, domain.Page{Path: "/test", Title: "Материал", Language: "ru", CreatedAt: clock(), UpdatedAt: clock(), Blocks: []domain.Block{{Type: domain.BlockText, Text: "Введение"}, {Type: domain.BlockImported, Import: &domain.ImportSettings{SourceID: sourceID}}}})
	must(t, err)
	return page
}

// TestImportedReplacement проверяет замену содержимого и публичные даты.
func TestImportedReplacement(t *testing.T) {
	store, path := openTest(t)
	source := makeSource(t, store, "https://example.org/1")
	page := makePage(t, store, source.ID)
	id := page.Blocks[1].ID
	_, err := store.GetPublicPage(ctx, page.Path)
	wantError(t, err, ErrNotFound)
	must(t, store.SaveImportedContent(ctx, id, source.ID, "A", clock()))
	must(t, store.PublishPage(ctx, page.ID, clock()))
	for index, body := range []string{"B", "A", "A"} {
		at := clock().Add(time.Duration(index+1) * time.Hour)
		must(t, store.SaveImportedContent(ctx, id, source.ID, body, at))
		public, err := store.GetPublicPage(ctx, page.Path)
		must(t, err)
		if public.Blocks[1].Content != body {
			t.Fatal(public)
		}
		if index == 2 && !public.UpdatedAt.Equal(clock().Add(2*time.Hour)) {
			t.Fatal("изменилась дата одинакового содержимого")
		}
	}
	// Старые редакторские данные не заменяют результат импорта.
	page.Title = "Новое название"
	page, err = store.SavePage(ctx, page, clock().Add(4*time.Hour))
	must(t, err)
	if page.Blocks[1].Prepared.Content != "A" {
		t.Fatal("импорт перезаписан редактором")
	}
	must(t, store.SetPublicationBlocked(ctx, source.ID, true, clock().Add(5*time.Hour)))
	public, err := store.GetPublicPage(ctx, page.Path)
	must(t, err)
	if len(public.Blocks) != 1 {
		t.Fatal(public)
	}
	must(t, store.SetPublicationBlocked(ctx, source.ID, false, clock().Add(6*time.Hour)))
	must(t, store.Close())
	store, err = Open(ctx, path)
	must(t, err)
	defer store.Close()
	page, err = store.GetPage(ctx, page.ID)
	must(t, err)
	if page.Blocks[1].Prepared.Content != "A" {
		t.Fatal(page)
	}
	must(t, store.UnpublishPage(ctx, page.ID, clock().Add(7*time.Hour)))
	_, err = store.GetPublicPage(ctx, page.Path)
	wantError(t, err, ErrNotFound)
	var count int
	must(t, store.db.QueryRow("SELECT count(*) FROM sqlite_schema WHERE name IN ('snapshots','prepared_contents')").Scan(&count))
	if count != 0 {
		t.Fatal("остались таблицы истории")
	}
}

// TestFailedCommitRecovery проверяет очистку транзакции после ошибки COMMIT.
func TestFailedCommitRecovery(t *testing.T) {
	store, _ := openTest(t)
	_, err := store.db.Exec(`CREATE TABLE parent(id INTEGER PRIMARY KEY); CREATE TABLE child(id INTEGER REFERENCES parent(id) DEFERRABLE INITIALLY DEFERRED)`)
	must(t, err)
	err = store.withTx(ctx, func(tx *sql.Tx) error { _, err := tx.Exec("INSERT INTO child VALUES(1)"); return err })
	if err == nil {
		t.Fatal("ожидалась ошибка COMMIT")
	}
	var count int
	must(t, store.db.QueryRow("SELECT count(*) FROM child").Scan(&count))
	if count != 0 {
		t.Fatal("частичная запись")
	}
	makeSource(t, store, "https://example.org/after")
	canceled, cancel := context.WithCancel(ctx)
	err = store.withTx(canceled, func(tx *sql.Tx) error {
		_, err := tx.ExecContext(canceled, "INSERT INTO parent VALUES(1)")
		cancel()
		return err
	})
	if err == nil {
		t.Fatal("не замечена отмена")
	}
	must(t, store.db.QueryRow("SELECT count(*) FROM parent").Scan(&count))
	if count != 0 {
		t.Fatal("отмена сохранила запись")
	}
}

// TestChangedImportSettings проверяет изменение правил и замену источника блока.
func TestChangedImportSettings(t *testing.T) {
	store, _ := openTest(t)
	source := makeSource(t, store, "https://example.org/a")
	other := makeSource(t, store, "https://example.org/b")
	page := makePage(t, store, source.ID)
	id := page.Blocks[1].ID
	must(t, store.SaveImportedContent(ctx, id, source.ID, "старое", clock()))
	page, err := store.GetPage(ctx, page.ID)
	must(t, err)
	page.Blocks[1].Import.Rules.Start.Text = "якорь"
	page, err = store.SavePage(ctx, page, clock())
	must(t, err)
	if page.Blocks[1].Prepared == nil {
		t.Fatal("потерян результат")
	}
	page.Blocks[1].Import.SourceID = other.ID
	page, err = store.SavePage(ctx, page, clock())
	must(t, err)
	if page.Blocks[1].Prepared != nil {
		t.Fatal("сохранился результат другого источника")
	}
	wantError(t, store.SaveImportedContent(ctx, id, source.ID, "неверный источник", clock()), ErrConflict)
}
