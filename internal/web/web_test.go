package web

import (
	"bytes"
	"context"
	"encoding/xml"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/ruwcs/wcs-guide/internal/domain"
	"github.com/ruwcs/wcs-guide/internal/storage"
	"github.com/yuin/goldmark"
	"github.com/yuin/goldmark/extension"
)

// TestPublicSite проверяет публичные представления страниц.
func TestPublicSite(t *testing.T) {
	ctx := context.Background()
	store, err := storage.Open(ctx, filepath.Join(t.TempDir(), "site.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	at := time.Now().UTC()
	record, err := store.CreatePage(ctx, domain.Page{Path: "/школы/россия", Title: "Школы <script>", Description: "Описание", MachineContext: "MACHINE_ONLY <secret>", Language: "ru", CreatedAt: at, UpdatedAt: at, Blocks: []domain.Block{{Type: domain.BlockText, Text: "```\nкод"}, {Type: domain.BlockText, Text: "[Школа](https://example.org)\n\n![alt](https://example.org/image)\n\n<script>alert(1)</script>"}}})
	if err != nil {
		t.Fatal(err)
	}
	base, _ := url.Parse("https://wcs.guide")
	handler, err := New(ctx, store, *base, "dev", slog.Default())
	if err != nil {
		t.Fatal(err)
	}
	request := func(method, path string) *httptest.ResponseRecorder {
		response := httptest.NewRecorder()
		req := httptest.NewRequest(method, path, nil)
		req.Host = "evil.example"
		handler.ServeHTTP(response, req)
		return response
	}
	if request("GET", "/школы/россия").Code != 404 {
		t.Fatal("черновик раскрыт")
	}
	if err := store.PublishPage(ctx, record.ID, at); err != nil {
		t.Fatal(err)
	}
	response := request("GET", "/школы/россия")
	body := response.Body.String()
	if response.Code != 200 {
		t.Fatal(body)
	}
	for _, value := range []string{"<script>", "<img", "evil.example"} {
		if strings.Contains(body, value) {
			t.Fatal("небезопасный HTML", value)
		}
	}
	if !strings.Contains(body, "wcs-guide:machine-context") || strings.Contains(body[strings.Index(body, "<body>"):], "MACHINE_ONLY") {
		t.Fatal("неправильный контекст")
	}
	markdown := request("GET", "/школы/россия.md")
	if markdown.Code != 200 || !strings.Contains(markdown.Body.String(), "MACHINE\\_ONLY") {
		t.Fatal(markdown.Body.String())
	}
	var rendered bytes.Buffer
	parser := goldmark.New(goldmark.WithExtensions(extension.Strikethrough))
	if err := parser.Convert(markdown.Body.Bytes(), &rendered); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(rendered.String(), "<a href=\"https://example.org\">Школа</a>") {
		t.Fatal("незакрытый код поглотил следующий блок", rendered.String())
	}
	for _, path := range []string{"/", "/.md", "/robots.txt", "/sitemap.xml", "/static/site.css"} {
		if request("GET", path).Code != 200 {
			t.Fatal(path)
		}
	}
	if !strings.Contains(strings.ToLower(request("GET", "/").Body.String()), strings.ToLower((&url.URL{Path: "/школы/россия"}).EscapedPath())) {
		t.Fatal("нет навигации")
	}

	var sitemap sitemapDocument
	if err := xml.Unmarshal(request("GET", "/sitemap.xml").Body.Bytes(), &sitemap); err != nil {
		t.Fatal(err)
	}
	if len(sitemap.URLs) != 2 || sitemap.URLs[0].Loc != "https://wcs.guide/" || sitemap.URLs[0].Lastmod != "" || sitemap.URLs[1].Lastmod == "" {
		t.Fatal(sitemap)
	}
	for _, path := range []string{"/missing", "/missing.md", "/static/../go.mod", "/templates/layout.html", "/школы%2Fроссия"} {
		if request("GET", path).Code != 404 {
			t.Fatal(path)
		}
	}
	for _, path := range []string{"/", "/.md", "/школы/россия", "/healthz"} {
		if request("POST", path).Code != 405 {
			t.Fatal(path)
		}
		head := request("HEAD", path)
		if head.Code != 200 || head.Body.Len() != 0 {
			t.Fatal(path)
		}
	}
	if err := store.UnpublishPage(ctx, record.ID, at); err != nil {
		t.Fatal(err)
	}
	if request("GET", "/школы/россия").Code != 404 {
		t.Fatal("снятая страница раскрыта")
	}
	store.Close()
	if request("GET", "/").Code != 500 {
		t.Fatal("не скрыта ошибка хранилища")
	}
}

// TestMarkdownIsolation проверяет независимость Markdown-блоков.
func TestMarkdownIsolation(t *testing.T) {
	for _, value := range []string{"[link][ref]\n\n[ref]: https://example.org", "**жирный** и *курсив* и ~~зачёркнутый~~", "`код`", " > цитата\n\nконец", "- один\n- два", "[x](javascript:alert(1))", "<iframe src=x></iframe>"} {
		html, markdown, err := renderMarkdown(value)
		if err != nil {
			t.Fatal(value, err)
		}
		var result bytes.Buffer
		parser := goldmark.New(goldmark.WithExtensions(extension.Strikethrough))
		if err := parser.Convert([]byte(markdown), &result); err != nil {
			t.Fatal(err)
		}
		if strings.Contains(result.String(), "<iframe") || strings.Contains(html, "href=\"javascript:") {
			t.Fatal("небезопасный вывод")
		}
	}
}

var _ http.Handler = (*handler)(nil) // Проверка контракта HTTP-обработчика.

// TestBlockedSourceAndFixtures проверяет исключение заблокированных материалов.
func TestBlockedSourceAndFixtures(t *testing.T) {
	ctx := context.Background()
	store, err := storage.Open(ctx, filepath.Join(t.TempDir(), "test.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	at := time.Now().UTC()
	source, err := store.CreateSource(ctx, domain.Source{Name: "SOURCE_MARKER", Type: domain.SourceTelegramPost, ExternalRef: "https://example.org/original"})
	if err != nil {
		t.Fatal(err)
	}

	record, err := store.CreatePage(ctx, domain.Page{Path: "/calendar", Title: "Календарь", Language: "ru", CreatedAt: at, UpdatedAt: at, Blocks: []domain.Block{{Type: domain.BlockText, Text: "MANUAL_MARKER"}, {Type: domain.BlockImported, Import: &domain.ImportSettings{SourceID: source.ID}}}})
	if err != nil {
		t.Fatal(err)
	}
	block := record.Blocks[1]
	if _, err := store.SaveImportedContent(ctx, block.ID, source, *block.Import, "IMPORTED_MARKER", at); err != nil {
		t.Fatal(err)
	}
	if err := store.PublishPage(ctx, record.ID, at); err != nil {
		t.Fatal(err)
	}
	base, _ := url.Parse("https://wcs.guide")
	handler, err := New(ctx, store, *base, "dev", slog.Default())
	if err != nil {
		t.Fatal(err)
	}
	for _, blocked := range []bool{false, true, false} {
		if err := store.SetPublicationBlocked(ctx, source.ID, blocked, at); err != nil {
			t.Fatal(err)
		}
		for _, address := range []string{"/calendar", "/calendar.md"} {
			response := httptest.NewRecorder()
			handler.ServeHTTP(response, httptest.NewRequest("GET", address, nil))
			body := response.Body.String()
			if response.Code != 200 || !strings.Contains(body, "MANUAL") {
				t.Fatal(body)
			}
			if strings.Contains(body, "IMPORTED") == blocked || strings.Contains(body, "SOURCE") == blocked || strings.Contains(body, "ORIGINAL_SECRET") {
				t.Fatal("нарушена публичная проекция", body)
			}
		}
	}
	for _, name := range []string{"calendar", "directory"} {
		source, err := os.ReadFile("../content/testdata/" + name + ".md")
		if err != nil {
			t.Fatal(err)
		}
		html, normalized, err := renderMarkdown(string(source))
		if err != nil {
			t.Fatal(err)
		}
		var result bytes.Buffer
		parser := goldmark.New(goldmark.WithExtensions(extension.Strikethrough))
		if err := parser.Convert([]byte(normalized), &result); err != nil {
			t.Fatal(err)
		}
		if strings.Count(html, "<a ") != strings.Count(result.String(), "<a ") {
			t.Fatal("потеряны ссылки", name)
		}
	}
}
