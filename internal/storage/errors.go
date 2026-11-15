package storage

import (
	"context"
	"database/sql"
	"errors"
	"fmt"

	"github.com/ruwcs/wcs-guide/internal/domain"
	sqlite "modernc.org/sqlite"
	sqlite3 "modernc.org/sqlite/lib"
)

var (
	// ErrNotFound — отсутствие сущности.
	ErrNotFound = errors.New("storage: сущность не найдена")
	// ErrUnique — конфликт уникальности.
	ErrUnique = errors.New("storage: значение уже занято")
	// ErrConflict — изменение состояния или несогласованные ссылки.
	ErrConflict = errors.New("storage: конфликт состояния")
	// ErrSchemaIncompatible — неподдерживаемую схему.
	ErrSchemaIncompatible = errors.New("storage: несовместимая схема; данные оставлены без изменений")
	// ErrSchemaTooNew — версию выше поддерживаемой.
	ErrSchemaTooNew = errors.New("storage: версия схемы новее поддерживаемой")
)

// internalError — внутренняя ошибка хранилища.
type internalError struct {
	cause error // Исходная ошибка для errors.Is и errors.As.
}

// Error реализует интерфейс error для internalError.
func (err *internalError) Error() string {
	return "storage: ошибка SQLite или повреждённые данные"
}

// Unwrap реализует интерфейс interface{ Unwrap() error } для internalError.
func (err *internalError) Unwrap() error { return err.cause }

// invalid возвращает ошибку проверки данных.
func invalid(field string) error { return fmt.Errorf("%w: storage.%s", domain.ErrInvalid, field) }

// protect классифицирует ошибку хранилища.
func protect(err error) error {
	if err == nil {
		return nil
	}
	if errors.Is(err, sql.ErrNoRows) {
		return ErrNotFound
	}
	for _, known := range []error{domain.ErrInvalid, ErrNotFound, ErrUnique, ErrConflict, ErrSchemaIncompatible, ErrSchemaTooNew, context.Canceled, context.DeadlineExceeded} {
		if errors.Is(err, known) {
			return err
		}
	}
	var sqliteErr *sqlite.Error
	if errors.As(err, &sqliteErr) {
		switch sqliteErr.Code() {
		case sqlite3.SQLITE_CONSTRAINT_PRIMARYKEY, sqlite3.SQLITE_CONSTRAINT_UNIQUE:
			return errors.Join(ErrUnique, &internalError{err})
		case sqlite3.SQLITE_BUSY, sqlite3.SQLITE_LOCKED, sqlite3.SQLITE_CONSTRAINT, sqlite3.SQLITE_CONSTRAINT_FOREIGNKEY, sqlite3.SQLITE_CONSTRAINT_TRIGGER, sqlite3.SQLITE_CONSTRAINT_CHECK:
			return errors.Join(ErrConflict, &internalError{err})
		}
		if sqliteErr.Code()&255 == sqlite3.SQLITE_CONSTRAINT || sqliteErr.Code()&255 == sqlite3.SQLITE_BUSY || sqliteErr.Code()&255 == sqlite3.SQLITE_LOCKED {
			return errors.Join(ErrConflict, &internalError{err})
		}
		return &internalError{err}
	}
	var internal *internalError
	if errors.As(err, &internal) {
		return err
	}
	return &internalError{err}
}
