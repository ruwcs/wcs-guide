package importer

import (
	"context"
	"errors"
	"github.com/ruwcs/wcs-guide/internal/domain"
	"github.com/ruwcs/wcs-guide/internal/storage"
	"github.com/ruwcs/wcs-guide/internal/telegram"
	"github.com/ruwcs/wcs-guide/internal/web"
	"log/slog"
	"net/http/httptest"
	"net/url"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// fakeFetcher — тестовый источник публикации.
type fakeFetcher struct {
	Text  string // Текст публикации.
	Err   error  // Ошибка получения.
	Calls int    // Количество получений.
}

// Fetch реализует интерфейс Fetcher для fakeFetcher.
func (f *fakeFetcher) Fetch(context.Context, domain.Source) (telegram.Publication, error) {
	f.Calls++
	return telegram.Publication{Text: f.Text, Entities: []telegram.Entity{{Kind: "messageEntityTextUrl", Offset: 0, Length: 6, URL: "https://example.org/community"}}}, f.Err
}

// TestImportReplacement проверяет замену содержимого и независимость блоков.
func TestImportReplacement(t *testing.T) {
	ctx := context.Background()
	store, err := storage.Open(ctx, filepath.Join(t.TempDir(), "import.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	source, err := store.CreateSource(ctx, domain.Source{Name: "Источник", Type: domain.SourceTelegramPost, ExternalRef: "https://t.me/example/1"})
	if err != nil {
		t.Fatal(err)
	}
	at := time.Now().UTC()
	page, err := store.CreatePage(ctx, domain.Page{Path: "/test", Title: "Материал", Language: "ru", CreatedAt: at, UpdatedAt: at, Blocks: []domain.Block{
		{Type: domain.BlockImported, Import: &domain.ImportSettings{SourceID: source.ID}},
		{Type: domain.BlockImported, Import: &domain.ImportSettings{SourceID: source.ID}},
		{Type: domain.BlockImported, Import: &domain.ImportSettings{SourceID: source.ID}},
	}})
	if err != nil {
		t.Fatal(err)
	}
	if err := store.PublishPage(ctx, page.ID, at); err != nil {
		t.Fatal(err)
	}
	fetcher := &fakeFetcher{Text: "Первый 😀"}
	runner := Importer{Store: store, Fetcher: fetcher}
	if _, err := runner.Sync(ctx, source.ID); err != nil {
		t.Fatal(err)
	}
	page, err = store.GetPage(ctx, page.ID)
	if err != nil {
		t.Fatal(err)
	}
	page.Blocks[1].Import.Rules.End.Text = "Отсутствующий якорь"
	if _, err := store.SavePage(ctx, page, time.Now().UTC()); err != nil {
		t.Fatal(err)
	}
	fetcher.Text = "Второй 🇷🇺"
	report, err := runner.Sync(ctx, source.ID)
	if err == nil || len(report.Blocks) != 3 {
		t.Fatal(report, err)
	}
	if report.Blocks[0].Err != nil || report.Blocks[1].Err == nil || report.Blocks[2].Err != nil {
		t.Fatal(report)
	}
	public, err := store.GetPublicPage(ctx, page.Path)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(public.Blocks[0].Content, "Второй") || !strings.Contains(public.Blocks[1].Content, "Первый") {
		t.Fatal(public)
	}
	previous := public.UpdatedAt
	if _, err := runner.Sync(ctx, source.ID); err == nil {
		t.Fatal("не замечен неверный якорь")
	}
	public, err = store.GetPublicPage(ctx, page.Path)
	if err != nil || !public.UpdatedAt.Equal(previous) {
		t.Fatal("изменилась дата", err)
	}
	page, err = store.GetPage(ctx, page.ID)
	if err != nil {
		t.Fatal(err)
	}
	page.Blocks[1].Import.Rules = domain.ExtractionRules{}
	if _, err := store.SavePage(ctx, page, time.Now().UTC()); err != nil {
		t.Fatal(err)
	}
	if _, err := runner.Sync(ctx, source.ID); err != nil {
		t.Fatal(err)
	}
	fetcher.Err = errors.New("недоступен")
	if _, err := runner.Sync(ctx, source.ID); err == nil {
		t.Fatal("ошибка получения потеряна")
	}
	base, _ := url.Parse("https://example.org")
	handler, err := web.New(ctx, store, *base, "dev", slog.Default())
	if err != nil {
		t.Fatal(err)
	}
	for _, path := range []string{"/test", "/test.md"} {
		response := httptest.NewRecorder()
		handler.ServeHTTP(response, httptest.NewRequest("GET", path, nil))
		if response.Code != 200 || !strings.Contains(response.Body.String(), "Второй") || !strings.Contains(response.Body.String(), "https://example.org/community") {
			t.Fatal(path, response.Code)
		}
	}
	if err := store.SetPublicationBlocked(ctx, source.ID, true, time.Now().UTC()); err != nil {
		t.Fatal(err)
	}
	public, err = store.GetPublicPage(ctx, page.Path)
	if err != nil || len(public.Blocks) != 0 {
		t.Fatal(public, err)
	}
	if fetcher.Calls != 5 {
		t.Fatal("неверное количество получений", fetcher.Calls)
	}
}
