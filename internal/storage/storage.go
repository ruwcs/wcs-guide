// Package storage хранит сущности CMS в SQLite. Сеть, обработка контента и рендеринг находятся вне пакета.
package storage

import (
	"context"
	"database/sql"
	"net/url"
	"os"
	"path/filepath"
	"strings"
)

// Storage владеет единственным пулом соединений SQLite.
type Storage struct {
	db *sql.DB // Закрытый пул с одним соединением.
}

// Open открывает файловую БД и проверяет совместимость схемы.
func Open(ctx context.Context, filename string) (*Storage, error) {
	if filename == "" || filename == ":memory:" || strings.HasPrefix(filename, "file:") {
		return nil, invalid("database path")
	}
	filename, err := filepath.Abs(filename)
	if err != nil {
		return nil, err
	}
	info, err := os.Stat(filepath.Dir(filename))
	if err != nil || !info.IsDir() {
		return nil, invalid("database directory")
	}
	// Новый файл создаётся с закрытыми правами; существующий не открывается
	// через os.File, чтобы не затронуть блокировки другого SQLite-соединения.
	f, createErr := os.OpenFile(filename, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0600)
	if createErr == nil {
		if closeErr := f.Close(); closeErr != nil {
			return nil, protect(closeErr)
		}
	} else if !os.IsExist(createErr) {
		return nil, protect(createErr)
	}
	u := url.URL{Scheme: "file", Path: filename}
	q := u.Query()
	for _, p := range []string{"busy_timeout(5000)", "foreign_keys(ON)", "synchronous(EXTRA)"} {
		q.Add("_pragma", p)
	}
	q.Set("_txlock", "immediate")
	u.RawQuery = q.Encode()
	db, err := sql.Open("sqlite", u.String())
	if err != nil {
		return nil, protect(err)
	}
	db.SetMaxOpenConns(1)
	db.SetMaxIdleConns(1)
	s := &Storage{db: db}
	if err = db.PingContext(ctx); err == nil {
		err = s.checkPragmas(ctx)
	}
	if err == nil {
		err = s.initialize(ctx)
	}
	if err != nil {
		db.Close()
		return nil, protect(err)
	}
	return s, nil
}

// Close закрывает соединения по контракту database/sql.
func (s *Storage) Close() error { return protect(s.db.Close()) }

func (s *Storage) checkPragmas(ctx context.Context) error {
	for name, want := range map[string]string{"foreign_keys": "1", "journal_mode": "delete", "synchronous": "3", "busy_timeout": "5000"} {
		var got string
		if err := s.db.QueryRowContext(ctx, "PRAGMA "+name).Scan(&got); err != nil {
			return err
		}
		if got != want {
			return ErrSchemaIncompatible
		}
	}
	return nil
}

// withTx выполняет короткую транзакцию без сетевой работы.
// BEGIN IMMEDIATE применяется также при чтении: один экземпляр и одно соединение
// сериализуют операции; внешний писатель может вызвать ожидание busy_timeout.
func (s *Storage) withTx(ctx context.Context, fn func(*sql.Tx) error) error {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		if ctx.Err() != nil {
			return ctx.Err()
		}
		return protect(err)
	}
	defer tx.Rollback()
	if err = fn(tx); err != nil {
		if ctx.Err() != nil {
			return ctx.Err()
		}
		return protect(err)
	}
	err = tx.Commit()
	if err != nil && ctx.Err() != nil {
		return ctx.Err()
	}
	return protect(err)
}
