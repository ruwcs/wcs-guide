package domain

import (
	"log/slog"
	"path"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"
)

// PageStatus обозначает публичность страницы.
type PageStatus uint8

const (
	// PageDraft — состояние новой или снятой страницы.
	PageDraft PageStatus = iota
	// PagePublished — явно опубликованная страница.
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

// Page содержит упорядоченные блоки независимо от URL-иерархии.
type Page struct {
	ID             int64      // Идентификатор страницы для связи с блоками.
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

// Validate проверяет страницу и её блоки без проверки уникальности адреса на сайте.
func (p Page) Validate() error {
	if p.ID < 0 || (p.Status != PageDraft && p.Status != PagePublished) {
		return invalid("Page.identity")
	}
	for _, s := range []string{p.Path, p.Title, p.Description, p.MachineContext, p.Language} {
		if !utf8.ValidString(s) {
			return invalid("Page.text")
		}
	}
	if p.Path != "" {
		if !strings.HasPrefix(p.Path, "/") || path.Clean(p.Path) != p.Path || strings.ContainsAny(p.Path, "%?#\\") || strings.ContainsFunc(p.Path, func(r rune) bool { return unicode.IsSpace(r) || unicode.IsControl(r) }) {
			return invalid("Page.path")
		}
	}
	if p.CreatedAt.IsZero() || p.UpdatedAt.Before(p.CreatedAt) || (!p.PublishedAt.IsZero() && (p.PublishedAt.Before(p.CreatedAt) || p.PublishedAt.After(p.UpdatedAt))) {
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

// Publish явно публикует страницу; повторный вызов сохраняет дату.
func (p *Page) Publish(at time.Time) error {
	if err := p.Validate(); err != nil {
		return err
	}
	if at.IsZero() || at.Before(p.UpdatedAt) {
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

// Unpublish снимает страницу, сохраняя содержимое и прежнюю дату публикации.
func (p *Page) Unpublish(at time.Time) error {
	if err := p.Validate(); err != nil {
		return err
	}
	if at.IsZero() || at.Before(p.UpdatedAt) {
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

var _ slog.LogValuer = Page{}
