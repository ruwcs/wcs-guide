package domain

import (
	"log/slog"
	"time"
)

// SourceType — тип источника содержимого.
type SourceType string

// SourceTelegramPost — публикация Telegram.
const SourceTelegramPost SourceType = "telegram_post"

// SyncStatus — результат последней попытки получения содержимого.
type SyncStatus string

const (
	// SyncNever — содержимое ещё не получалось.
	SyncNever SyncStatus = "never"
	// SyncSucceeded — успешное получение содержимого.
	SyncSucceeded SyncStatus = "succeeded"
	// SyncFailed — ошибка получения содержимого.
	SyncFailed SyncStatus = "failed"
)

// Source — внешний источник содержимого.
// Замена внешней публикации требует нового Source, что контролирует приложение.
type Source struct {
	ID                 int64             // Идентификатор источника.
	Name               string            // Название источника для редактора.
	Type               SourceType        // Способ получения содержимого.
	ExternalRef        string            // URL или внешний идентификатор публикации.
	FetchParams        map[string]string // Параметры получения содержимого.
	UpdateInterval     time.Duration     // Интервал автоматического обновления.
	AutoRefresh        bool              // Разрешение автоматического обновления.
	PublicationBlocked bool              // Запрет публичного отображения содержимого источника.
	Sync               SyncState         // Состояние последней завершённой попытки.
}

// SyncState — состояние получения содержимого источника.
// Очистку ошибки от чувствительных данных обеспечивает вызывающий код.
type SyncState struct {
	LastAttemptAt time.Time // Время завершения последней попытки получения.
	LastSuccessAt time.Time // Время последнего успеха; ноль — успехов не было.
	LastError     string    // Безопасное описание последней ошибки; пусто при успехе.
}

// Status возвращает результат последней попытки получения содержимого.
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
	if at.IsZero() {
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
	if at.IsZero() || blank(message) {
		return invalid("SyncState.failure")
	}
	s.LastAttemptAt, s.LastError = at.UTC(), message
	return nil
}

// Validate проверяет источник.
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

// Соответствие интерфейсу slog.LogValuer.
var _ slog.LogValuer = Source{}
