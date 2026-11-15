// Package web предоставляет публичные HTML- и Markdown-представления материалов.
package web

import (
	"context"
	"embed"
	"fmt"
	"html/template"
	"log/slog"
	"net/http"
	"net/url"

	"github.com/ruwcs/wcs-guide/internal/domain"
	"github.com/ruwcs/wcs-guide/internal/storage"
)

// resources — шаблоны и статические ресурсы сайта.
//
//go:embed templates/*.html static/site.css
var resources embed.FS

// handler — публичный веб-интерфейс.
type handler struct {
	store     *storage.Storage   // Хранилище.
	base      url.URL            // Адрес сайта.
	version   string             // Версия исполняемого приложения.
	logger    *slog.Logger       // Журнал технических ошибок.
	templates *template.Template // HTML-шаблоны сайта.
}

// view — данные HTML-представления страницы.
type view struct {
	Page      storage.PublicPage          // Публичные свойства материала.
	Blocks    []template.HTML             // Содержимое блоков в HTML.
	Sources   []storage.PublicBlock       // Источники содержимого страницы.
	Catalog   []storage.PublicPageSummary // Каталог главной страницы.
	Canonical string                      // Абсолютный адрес HTML-представления.
	Markdown  string                      // Абсолютный адрес Markdown-представления.
	Version   string                      // Версия приложения для подвала.
	Error     string                      // Сообщение страницы ошибки.
}

// New возвращает обработчик публичного сайта.
func New(ctx context.Context, store *storage.Storage, baseURL url.URL, version string, logger *slog.Logger) (http.Handler, error) {
	templates, err := template.ParseFS(resources, "templates/*.html")
	if err != nil {
		return nil, err
	}
	pages, err := store.ListPublicPages(ctx)
	if err != nil {
		return nil, err
	}
	for _, page := range pages {
		if err := domain.ValidatePagePath(page.Path); err != nil {
			return nil, fmt.Errorf("web: конфликт опубликованного адреса страницы %d: %w", page.ID, err)
		}
	}
	return &handler{store: store, base: baseURL, version: version, logger: logger, templates: templates}, nil
}

// absolute возвращает абсолютный адрес страницы.
func (h *handler) absolute(value string) string {
	target := h.base
	target.Path = value
	target.RawPath = ""
	return target.String()
}
