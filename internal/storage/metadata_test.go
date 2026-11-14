package storage

import (
	"bytes"
	"database/sql"
	"os"
	"path/filepath"
	"reflect"
	"testing"
	"time"

	"github.com/ruwcs/wcs-guide/internal/domain"
)

func TestMachineContextLifecycle(t *testing.T) {
	store, path := openTest(t)
	const contextText = "WCS — West Coast Swing.\n한국 <материал> & \"пояснение\""
	source := makeSource(t, store, "metadata")
	fetch(t, store, source.Source.ID, "A", clock())
	record := makePage(t, store, source.Source.ID)
	if record.Page.MachineContext != "" {
		t.Fatal("непустой контекст по умолчанию")
	}
	record.Page.MachineContext = contextText
	saved, err := store.SavePage(ctx, record.Page, record.EditRevision, clock().Add(time.Hour))
	must(t, err)
	if saved.Page.MachineContext != contextText || !saved.Page.UpdatedAt.Equal(clock().Add(time.Hour)) || saved.EditRevision != record.EditRevision+1 {
		t.Fatal(saved)
	}
	_, err = store.GetPublicPage(ctx, saved.Page.Path)
	wantError(t, err, ErrNotFound)
	_, err = store.SavePage(ctx, record.Page, record.EditRevision, clock().Add(2*time.Hour))
	wantError(t, err, ErrConflict)
	token, result := prepare(t, store, saved.Page.Blocks[1].ID, *saved.Page.Blocks[1].Import, "импорт")
	must(t, store.ApplyPrepared(ctx, token, result, clock().Add(2*time.Hour)))
	must(t, store.PublishPage(ctx, saved.Page.ID, saved.EditRevision, clock().Add(3*time.Hour)))
	before, err := store.GetPage(ctx, saved.Page.ID)
	must(t, err)
	before.Page.MachineContext = contextText + "\nДополнение"
	changed, err := store.SavePage(ctx, before.Page, before.EditRevision, clock().Add(4*time.Hour))
	must(t, err)
	if !reflect.DeepEqual(before.Page.Blocks, changed.Page.Blocks) {
		t.Fatal("изменены блоки")
	}
	if !changed.Page.PublishedAt.Equal(before.Page.PublishedAt) || !changed.Page.CreatedAt.Equal(before.Page.CreatedAt) {
		t.Fatal("изменены даты создания или публикации")
	}
	public, err := store.GetPublicPage(ctx, saved.Page.Path)
	must(t, err)
	if public.MachineContext != changed.Page.MachineContext || !public.UpdatedAt.Equal(clock().Add(4*time.Hour)) {
		t.Fatal(public)
	}
	unchanged, err := store.SavePage(ctx, changed.Page, changed.EditRevision, clock().Add(5*time.Hour))
	must(t, err)
	if !unchanged.Page.UpdatedAt.Equal(changed.Page.UpdatedAt) {
		t.Fatal("идентичный контекст изменил дату")
	}
	same, err := store.GetPublicPage(ctx, saved.Page.Path)
	must(t, err)
	if !same.UpdatedAt.Equal(public.UpdatedAt) {
		t.Fatal("изменилась публичная дата")
	}
	must(t, store.Close())
	reopened, err := Open(ctx, path)
	must(t, err)
	defer reopened.Close()
	restored, err := reopened.GetPage(ctx, saved.Page.ID)
	must(t, err)
	if !reflect.DeepEqual(restored, unchanged) {
		t.Fatal("потеря данных после переоткрытия")
	}
	restored.Page.MachineContext = ""
	cleared, err := reopened.SavePage(ctx, restored.Page, restored.EditRevision, clock().Add(6*time.Hour))
	must(t, err)
	public, err = reopened.GetPublicPage(ctx, saved.Page.Path)
	must(t, err)
	if public.MachineContext != "" || !public.UpdatedAt.Equal(clock().Add(6*time.Hour)) {
		t.Fatal(public)
	}
	must(t, reopened.UnpublishPage(ctx, cleared.Page.ID, cleared.EditRevision, clock().Add(7*time.Hour)))
	_, err = reopened.GetPublicPage(ctx, saved.Page.Path)
	wantError(t, err, ErrNotFound)
	// Создание сразу с заполненным контекстом и чтение редакторскими методами.
	input := domain.Page{Path: "/context", Title: "Контекст", Language: "ru", MachineContext: contextText, CreatedAt: clock(), UpdatedAt: clock()}
	created, err := reopened.CreatePage(ctx, input)
	must(t, err)
	byPath, err := reopened.GetPageByPath(ctx, input.Path)
	must(t, err)
	if byPath.Page.MachineContext != contextText || created.Page.MachineContext != contextText {
		t.Fatal("контекст не сохранён")
	}
	listed, err := reopened.ListPages(ctx)
	must(t, err)
	if len(listed) != 2 || listed[1].MachineContext != contextText {
		t.Fatal("контекст отсутствует в редакторском списке")
	}
	summaries, err := reopened.ListPublicPages(ctx)
	must(t, err)
	if len(summaries) != 0 {
		t.Fatal("непубличные страницы в списке")
	}
}

func TestInitialSchemaPreservesData(t *testing.T) {
	path := filepath.Join(t.TempDir(), "version-one.db")
	db, err := sql.Open("sqlite", path)
	must(t, err)
	initialStore := &Storage{db: db}
	must(t, initialStore.initialize(ctx))
	// Отсутствие контекста в INSERT проверяет значение по умолчанию в схеме.
	_, err = db.Exec(`INSERT INTO pages (id, path, title, description, language, status
, created_at, updated_at, published_at, public_updated_at,
        edit_revision)
        VALUES (7, '/старый', 'Заголовок', 'Описание', 'ru', 1, ?, ?, ?
, ?, 9)`, stamp(clock()), stamp(clock()), stamp(clock()), stamp(clock()))
	must(t, err)
	_, err = db.Exec("INSERT INTO blocks(id,page_id,position,type,text) VALUES(11,7,0,'text','Исходный текст')")
	must(t, err)
	must(t, db.Close())
	store, err := Open(ctx, path)
	must(t, err)
	defer store.Close()
	page, err := store.GetPage(ctx, 7)
	must(t, err)
	if page.Page.MachineContext != "" || page.EditRevision != 9 || page.Page.Title != "Заголовок" || page.Page.Description != "Описание" || len(page.Page.Blocks) != 1 || page.Page.Blocks[0].ID != 11 || page.Page.Blocks[0].Text != "Исходный текст" {
		t.Fatal(page)
	}
	public, err := store.GetPublicPage(ctx, "/старый")
	must(t, err)
	if !public.UpdatedAt.Equal(clock()) || !page.Page.UpdatedAt.Equal(clock()) || !page.Page.CreatedAt.Equal(clock()) || !page.Page.PublishedAt.Equal(clock()) {
		t.Fatal("повторное открытие изменило даты")
	}
	var version int
	must(t, store.db.QueryRow("PRAGMA user_version").Scan(&version))
	if version != 1 {
		t.Fatal(version)
	}
	must(t, store.Close())
	reopened, err := Open(ctx, path)
	must(t, err)
	defer reopened.Close()
	again, err := reopened.GetPage(ctx, 7)
	must(t, err)
	if !reflect.DeepEqual(page, again) {
		t.Fatal("повторное открытие изменило данные")
	}
}

func TestExperimentalVersionTwoIsRejectedWithoutChanges(t *testing.T) {
	store, path := openTest(t)
	input := domain.Page{MachineContext: "Сохранить данные", CreatedAt: clock(), UpdatedAt: clock()}
	_, err := store.CreatePage(ctx, input)
	must(t, err)
	_, err = store.db.Exec("PRAGMA user_version=2")
	must(t, err)
	must(t, store.Close())
	before, err := os.ReadFile(path)
	must(t, err)
	_, err = Open(ctx, path)
	wantError(t, err, ErrSchemaTooNew)
	after, err := os.ReadFile(path)
	must(t, err)
	if !bytes.Equal(before, after) {
		t.Fatal("отказ открытия изменил БД")
	}
}
