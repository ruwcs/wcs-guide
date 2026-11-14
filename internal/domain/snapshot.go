package domain

import (
	"log/slog"
	"time"
)

// Snapshot представляет версию оригинальных данных до обработки.
// После создания снимок логически неизменяем; владельцем среза остаётся вызывающий код.
type Snapshot struct {
	ID               int64             // Идентификатор для ссылок на версию оригинала.
	SourceID         int64             // Источник, которому принадлежит снимок.
	RawContent       []byte            // Оригинальные данные до нормализации и обработки.
	Format           string            // Формат для интерпретации оригинальных данных.
	Metadata         map[string]string // Дополнительные сведения об оригинале.
	Checksum         string            // Контрольная сумма для обнаружения изменений.
	FetchedAt        time.Time         // Время получения этой версии оригинала.
	SourceModifiedAt time.Time         // Время изменения оригинала; ноль — неизвестно.
}

// Validate проверяет полноту снимка без интерпретации формата и контрольной суммы.
func (s Snapshot) Validate() error {
	if s.ID < 0 || s.SourceID <= 0 || len(s.RawContent) == 0 || blank(s.Format) || blank(s.Checksum) || s.FetchedAt.IsZero() {
		return invalid("Snapshot")
	}
	return nil
}

// LogValue реализует интерфейс slog.LogValuer для Snapshot.
func (s Snapshot) LogValue() slog.Value {
	return slog.GroupValue(slog.Int64("id", s.ID), slog.Int64("source_id", s.SourceID), slog.Int("raw_size", len(s.RawContent)))
}

var _ slog.LogValuer = Snapshot{}
