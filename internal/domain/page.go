package domain

import (
	"log/slog"
	"path"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"
)

// PageStatus — состояние публикации страницы.
type PageStatus uint8

const (
	// PageDraft — черновик страницы.
	PageDraft PageStatus = iota
	// PagePublished — опубликованная страница.
	PagePublished
)

// String реализует интерфейс fmt.Stringer для PageStatus.
func (s PageStatus) String() string {
	switch s {
	case PageDraft:
		return "draft"
	case PagePublished:
		return "published"
	default:
		return "unknown"
	}
}

// Page — страница сайта.
type Page struct {
	ID             int64      // Идентификатор страницы.
	Path           string     // Канонический незакодированный локальный адрес страницы.
	Title          string     // Единый заголовок страницы и её представлений.
	Description    string     // Краткое текстовое описание для метаданных и каталогов.
	MachineContext string     // Дополнительный публичный контекст для машинных потребителей.
	Language       string     // Основной язык содержимого.
	Status         PageStatus // Состояние публичности страницы.
	CreatedAt      time.Time  // Время создания страницы.
	UpdatedAt      time.Time  // Время редакторского изменения страницы или её статуса.
	PublishedAt    time.Time  // Последняя явная публикация; ноль — не публиковалась.
	Blocks         []Block    // Блоки в порядке отображения на странице.
}

// Validate проверяет страницу и её блоки.
func (p Page) Validate() error {
	if p.ID < 0 || (p.Status != PageDraft && p.Status != PagePublished) {
		return invalid("Page.identity")
	}
	for _, s := range []string{p.Path, p.Title, p.Description, p.MachineContext, p.Language} {
		if !utf8.ValidString(s) {
			return invalid("Page.text")
		}
	}
	if err := ValidatePagePath(p.Path); err != nil {
		return err
	}

	if p.CreatedAt.IsZero() || p.UpdatedAt.IsZero() {
		return invalid("Page.time")
	}
	if p.Status == PagePublished && (blank(p.Title) || p.Path == "" || blank(p.Language) || p.PublishedAt.IsZero()) {
		return invalid("Page.publication")
	}
	ids := make(map[int64]bool)
	for _, b := range p.Blocks {
		if b.PageID != p.ID || (b.ID != 0 && ids[b.ID]) {
			return invalid("Page.blocks")
		}
		if err := b.Validate(); err != nil {
			return err
		}
		if b.ID != 0 {
			ids[b.ID] = true
		}
	}
	return nil
}

// Publish публикует страницу.
func (p *Page) Publish(at time.Time) error {
	if err := p.Validate(); err != nil {
		return err
	}
	if at.IsZero() {
		return invalid("Page.at")
	}
	if p.Status == PagePublished {
		return nil
	}
	if blank(p.Title) || p.Path == "" || blank(p.Language) {
		return invalid("Page.publication")
	}
	p.Status, p.PublishedAt, p.UpdatedAt = PagePublished, at.UTC(), at.UTC()
	return nil
}

// Unpublish снимает страницу с публикации.
func (p *Page) Unpublish(at time.Time) error {
	if err := p.Validate(); err != nil {
		return err
	}
	if at.IsZero() {
		return invalid("Page.at")
	}
	if p.Status == PageDraft {
		return nil
	}
	p.Status, p.UpdatedAt = PageDraft, at.UTC()
	return nil
}

// LogValue реализует интерфейс slog.LogValuer для Page.
func (p Page) LogValue() slog.Value {
	return slog.GroupValue(slog.Int64("id", p.ID), slog.String("status", p.Status.String()), slog.Int("block_count", len(p.Blocks)))
}

// Соответствие интерфейсу slog.LogValuer.
var _ slog.LogValuer = Page{}

// ValidatePagePath проверяет канонический адрес и отсутствие конфликтов с HTTP-маршрутами.
// Пустой адрес допустим для незавершённого черновика.
func ValidatePagePath(value string) error {
	if value == "" {
		return nil
	}
	if !utf8.ValidString(value) || !strings.HasPrefix(value, "/") || path.Clean(value) != value || strings.ContainsAny(value, "%?#\\") || strings.ContainsFunc(value, func(r rune) bool { return unicode.IsSpace(r) || unicode.IsControl(r) }) {
		return invalid("Page.path")
	}
	if value == "/healthz" || value == "/robots.txt" || value == "/sitemap.xml" || value == "/static" || strings.HasPrefix(value, "/static/") || strings.HasSuffix(value, ".md") {
		return invalid("Page.reserved_path")
	}
	return nil
}
