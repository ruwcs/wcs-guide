package domain

import (
	"log/slog"
	"time"
	"unicode/utf8"
)

// BlockType обозначает вид содержимого блока.
type BlockType string

const (
	// BlockText обозначает ручной Markdown-текст.
	BlockText BlockType = "text"
	// BlockImported обозначает импортируемое содержимое.
	BlockImported BlockType = "imported"
)

// Block принадлежит странице; порядок определяется положением в Page.Blocks.
type Block struct {
	ID       int64            // Идентификатор блока для ссылок и изменений.
	PageID   int64            // Страница, которой принадлежит блок.
	Type     BlockType        // Вид содержимого блока.
	Text     string           // Markdown ручного блока.
	Import   *ImportSettings  // Источник и правила импорта; nil для ручного блока.
	Prepared *PreparedContent // Последний успешный результат; nil до первой подготовки.
}

// ImportSettings содержит источник и действующие правила блока.
type ImportSettings struct {
	SourceID int64           // Источник импортируемого содержимого.
	Rules    ExtractionRules // Действующие правила извлечения блока.
}

// PreparedContent сохраняет Markdown-результат и фактически применённые правила.
// Нулевое PublishedAt означает, что результат ещё не публиковался.
type PreparedContent struct {
	Content     string          // Подготовленный Markdown для отображения.
	SourceID    int64           // Источник, из которого получен результат.
	SnapshotID  int64           // Снимок, из которого получен результат.
	Rules       ExtractionRules // Копия фактически применённых правил.
	PreparedAt  time.Time       // Время успешной подготовки результата.
	PublishedAt time.Time       // Время публикации результата; ноль — не публиковался.
}

// Validate проверяет настройки без обращения к источнику.
func (s ImportSettings) Validate() error {
	if s.SourceID <= 0 {
		return invalid("ImportSettings.source_id")
	}
	return s.Rules.Validate()
}

// Validate проверяет результат; пустое содержимое допустимо.
func (p PreparedContent) Validate() error {
	if p.SourceID <= 0 || p.SnapshotID <= 0 || p.PreparedAt.IsZero() || !utf8.ValidString(p.Content) || (!p.PublishedAt.IsZero() && p.PublishedAt.Before(p.PreparedAt)) {
		return invalid("PreparedContent")
	}
	return p.Rules.Validate()
}

// Validate проверяет совместимость полей с типом блока.
func (b Block) Validate() error {
	if b.ID < 0 || b.PageID < 0 {
		return invalid("Block.identity")
	}
	switch b.Type {
	case BlockText:
		if b.Import != nil || b.Prepared != nil || !utf8.ValidString(b.Text) {
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
			if b.Prepared.SourceID != b.Import.SourceID {
				return invalid("Block.prepared_source")
			}
			return b.Prepared.Validate()
		}
	default:
		return invalid("Block.type")
	}
	return nil
}

// ApplyPrepared заменяет результат только после успешной проверки.
// Источник и правила должны совпадать с действующими настройками.
// Актуальность снимка проверяет вызывающий код при транзакционной записи.
// Метод не публикует страницу и не назначает время публикации.
func (b *Block) ApplyPrepared(result PreparedContent) error {
	if err := b.Validate(); err != nil {
		return err
	}
	if b.Type != BlockImported {
		return invalid("Block.type")
	}
	if err := result.Validate(); err != nil {
		return err
	}
	if result.SourceID != b.Import.SourceID || result.Rules != b.Import.Rules {
		return invalid("Block.prepared_settings")
	}
	b.Prepared = &result
	return nil
}

// ReplaceImport согласованно заменяет настройки и проверенный результат.
// Метод не подтверждает предпросмотр и не заменяет транзакцию хранилища.
func (b *Block) ReplaceImport(settings ImportSettings, result PreparedContent) error {
	if err := b.Validate(); err != nil {
		return err
	}
	if b.Type != BlockImported {
		return invalid("Block.type")
	}
	next := *b
	next.Import = &settings
	next.Prepared = nil
	if err := next.ApplyPrepared(result); err != nil {
		return err
	}
	*b = next
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

// Проверка контракта безопасного логирования сущности при компиляции.
var _ slog.LogValuer = Block{}
