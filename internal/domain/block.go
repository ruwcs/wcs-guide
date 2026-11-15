package domain

import (
	"log/slog"
	"time"
	"unicode/utf8"
)

// BlockType — вид содержимого блока.
type BlockType string

const (
	// BlockText — ручной Markdown-блок.
	BlockText BlockType = "text"
	// BlockImported — импортируемый блок.
	BlockImported BlockType = "imported"
)

// Block — блок содержимого страницы.
type Block struct {
	ImportError string           // Последняя ошибка подготовки импортируемого блока.
	ID          int64            // Идентификатор блока.
	PageID      int64            // Идентификатор страницы блока.
	Type        BlockType        // Вид содержимого блока.
	Text        string           // Markdown ручного блока.
	Import      *ImportSettings  // Источник и правила импорта; nil для ручного блока.
	Prepared    *PreparedContent // Последний успешный результат; nil до первой подготовки.
}

// ImportSettings — настройки импорта блока.
type ImportSettings struct {
	SourceID int64           // Идентификатор источника импортируемого содержимого.
	Rules    ExtractionRules // Действующие правила извлечения блока.
}

// PreparedContent — последнее успешно импортированное содержимое блока.
type PreparedContent struct {
	Content    string    // Содержимое блока в Markdown.
	PreparedAt time.Time // Время успешного импорта.
}

// Validate проверяет настройки импорта.
func (s ImportSettings) Validate() error {
	if s.SourceID <= 0 {
		return invalid("ImportSettings.source_id")
	}
	return s.Rules.Validate()
}

// Validate проверяет результат; пустое содержимое допустимо.
func (p PreparedContent) Validate() error {
	if p.PreparedAt.IsZero() || !utf8.ValidString(p.Content) {
		return invalid("PreparedContent")
	}
	return nil
}

// Validate проверяет совместимость полей с типом блока.
func (b Block) Validate() error {
	if b.ID < 0 || b.PageID < 0 || !utf8.ValidString(b.ImportError) {
		return invalid("Block.identity")
	}
	switch b.Type {
	case BlockText:
		if b.Import != nil || b.Prepared != nil || b.ImportError != "" || !utf8.ValidString(b.Text) {
			return invalid("Block.text")
		}
	case BlockImported:
		if b.Import == nil || b.Text != "" {
			return invalid("Block.import")
		}
		if err := b.Import.Validate(); err != nil {
			return err
		}
		if b.Prepared != nil {
			return b.Prepared.Validate()
		}
	default:
		return invalid("Block.type")
	}
	return nil
}

// LogValue реализует интерфейс slog.LogValuer для Block.
func (b Block) LogValue() slog.Value {
	attrs := []slog.Attr{slog.Int64("id", b.ID), slog.Int64("page_id", b.PageID), slog.String("type", string(b.Type)), slog.Bool("has_prepared", b.Prepared != nil)}
	if b.Type == BlockImported && b.Import != nil {
		attrs = append(attrs, slog.Int64("source_id", b.Import.SourceID))
	}
	return slog.GroupValue(attrs...)
}

// Соответствие интерфейсу slog.LogValuer.
var _ slog.LogValuer = Block{}
