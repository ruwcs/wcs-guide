package storage

import (
	"context"
	"database/sql"
	"embed"
	"fmt"
	"io/fs"
	"strconv"
	"strings"
)

//go:embed migrations/*.sql
var migrations embed.FS

func (s *Storage) initialize(ctx context.Context) error { return s.migrate(ctx, migrations) }

// migrate проверяет каталог и применяет каждую версию отдельной транзакцией.
func (s *Storage) migrate(ctx context.Context, files fs.FS) error {
	names, err := fs.Glob(files, "migrations/*.sql")
	if err != nil {
		return err
	}
	if len(names) == 0 {
		return ErrSchemaIncompatible
	}
	for index, name := range names {
		prefix, _, ok := strings.Cut(strings.TrimPrefix(name, "migrations/"), "_")
		version, err := strconv.Atoi(prefix)
		if !ok || err != nil || len(prefix) != 3 || version != index+1 {
			return ErrSchemaIncompatible
		}
	}
	for {
		done := false
		err = s.withTx(ctx, func(tx *sql.Tx) error {
			var version int
			if err := tx.QueryRowContext(ctx, "PRAGMA user_version").Scan(&version); err != nil {
				return err
			}
			if version > len(names) {
				return ErrSchemaTooNew
			}
			if version < 0 {
				return ErrSchemaIncompatible
			}
			if version == 0 {
				var count int
				if err := tx.QueryRowContext(ctx, `SELECT count (*)
        FROM sqlite_schema
        WHERE type = 'table'
        AND name NOT LIKE 'sqlite_%'`).Scan(&count); err != nil {
					return err
				}
				if count != 0 {
					return ErrSchemaIncompatible
				}
			}
			if version == len(names) {
				done = true
				return nil
			}
			script, err := fs.ReadFile(files, names[version])
			if err != nil {
				return err
			}
			if strings.TrimSpace(string(script)) == "" {
				return ErrSchemaIncompatible
			}
			if _, err = tx.ExecContext(ctx, string(script)); err != nil {
				return err
			}
			_, err = tx.ExecContext(ctx, fmt.Sprintf("PRAGMA user_version=%d", version+1))
			return err
		})
		if err != nil {
			return err
		}
		if done {
			return nil
		}
	}
}
