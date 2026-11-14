package domain

import (
	"log/slog"
	"time"
)

// SourceType обозначает способ получения содержимого.
type SourceType string

// SourceTelegramPost обозначает конкретную публикацию Telegram.
const SourceTelegramPost SourceType = "telegram_post"

// SyncStatus описывает последнюю завершённую попытку.
type SyncStatus string

const (
	// SyncNever означает отсутствие завершённых попыток.
	SyncNever SyncStatus = "never"
	// SyncSucceeded означает успешное получение.
	SyncSucceeded SyncStatus = "succeeded"
	// SyncFailed означает ошибку получения.
	SyncFailed SyncStatus = "failed"
)

// Source описывает происхождение контента независимо от fetcher и хранения.
// Замена внешней публикации требует нового Source, что контролирует приложение.
type Source struct {
	ID                 int64             // Идентификатор для ссылок на источник.
	Name               string            // Название источника для редактора.
	Type               SourceType        // Способ получения содержимого.
	ExternalRef        string            // URL или внешний идентификатор публикации.
	FetchParams        map[string]string // Параметры получения для fetcher.
	UpdateInterval     time.Duration     // Интервал автоматического обновления.
	AutoRefresh        bool              // Разрешение автоматического обновления.
	PublicationBlocked bool              // Запрет публичного отображения содержимого источника.
	Sync               SyncState         // Состояние последней завершённой попытки.
}

// SyncState хранит времена завершения попыток и безопасное описание ошибки.
// Очистку ошибки от чувствительных данных обеспечивает вызывающий код.
type SyncState struct {
	LastAttemptAt time.Time // Время завершения последней попытки получения.
	LastSuccessAt time.Time // Время последнего успеха; ноль — успехов не было.
	LastError     string    // Безопасное описание последней ошибки; пусто при успехе.
}

// Status выводит статус без дублирующего изменяемого поля.
func (s SyncState) Status() SyncStatus {
	if s.LastAttemptAt.IsZero() {
		return SyncNever
	}
	if s.LastError != "" {
		return SyncFailed
	}
	return SyncSucceeded
}

// Validate проверяет согласованность завершённых попыток.
func (s SyncState) Validate() error {
	if s.LastAttemptAt.IsZero() {
		if !s.LastSuccessAt.IsZero() || s.LastError != "" {
			return invalid("SyncState.attempt")
		}
		return nil
	}
	if s.LastSuccessAt.After(s.LastAttemptAt) {
		return invalid("SyncState.success")
	}
	if s.LastError == "" {
		if s.LastSuccessAt.IsZero() || !s.LastSuccessAt.Equal(s.LastAttemptAt) {
			return invalid("SyncState.success")
		}
	} else if blank(s.LastError) {
		return invalid("SyncState.error")
	}
	return nil
}

// RecordSuccess фиксирует успех и очищает предыдущую ошибку.
func (s *SyncState) RecordSuccess(at time.Time) error {
	if err := s.Validate(); err != nil {
		return err
	}
	if at.IsZero() || at.Before(s.LastAttemptAt) {
		return invalid("SyncState.at")
	}
	*s = SyncState{LastAttemptAt: at.UTC(), LastSuccessAt: at.UTC()}
	return nil
}

// RecordFailure фиксирует неудачу, сохраняя время последнего успеха.
func (s *SyncState) RecordFailure(at time.Time, message string) error {
	if err := s.Validate(); err != nil {
		return err
	}
	if at.IsZero() || at.Before(s.LastAttemptAt) || blank(message) {
		return invalid("SyncState.failure")
	}
	s.LastAttemptAt, s.LastError = at.UTC(), message
	return nil
}

// Validate проверяет локальные свойства, не существование внешнего источника.
func (s Source) Validate() error {
	if s.ID < 0 || blank(s.Name) || s.Type != SourceTelegramPost || blank(s.ExternalRef) {
		return invalid("Source.identity")
	}
	if s.UpdateInterval < 0 || (s.AutoRefresh && s.UpdateInterval == 0) {
		return invalid("Source.interval")
	}
	return s.Sync.Validate()
}

// LogValue реализует интерфейс slog.LogValuer для Source.
func (s Source) LogValue() slog.Value {
	return slog.GroupValue(slog.Int64("id", s.ID), slog.String("type", string(s.Type)), slog.Bool("auto_refresh", s.AutoRefresh), slog.Bool("publication_blocked", s.PublicationBlocked), slog.String("sync_status", string(s.Sync.Status())))
}

var _ slog.LogValuer = Source{}
