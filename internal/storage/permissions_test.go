package storage

import (
	"os"
	"path/filepath"
	"reflect"
	"runtime"
	"testing"

	"github.com/ruwcs/wcs-guide/internal/domain"
)

// TestOpenPermissions проверяет права доступа к БД и её каталогу.
func TestOpenPermissions(t *testing.T) {
	if runtime.GOOS == "windows" || os.Geteuid() == 0 {
		t.Skip("проверка POSIX-прав требует непривилегированного пользователя")
	}
	for _, test := range []struct {
		name          string      // Сценарий ограничения доступа.
		existing      bool        // Наличие ранее созданной БД.
		directoryMode os.FileMode // Права каталога при открытии.
		fileMode      os.FileMode // Права существующей БД при открытии.
	}{
		{"cannot_create", false, 0500, 0600},
		{"cannot_read", true, 0700, 0000},
		{"readonly_file", true, 0700, 0400},
		{"cannot_create_journal", true, 0500, 0600},
	} {
		t.Run(test.name, func(t *testing.T) {
			directory := t.TempDir()
			path := filepath.Join(directory, "test.db")
			var original domain.Source
			if test.existing {
				store, err := Open(ctx, path)
				must(t, err)
				original = makeSource(t, store, "permission-test")
				must(t, store.Close())
				must(t, os.Chmod(path, test.fileMode))
			}
			must(t, os.Chmod(directory, test.directoryMode))
			t.Cleanup(func() {
				must(t, os.Chmod(directory, 0700))
				if test.existing {
					must(t, os.Chmod(path, 0600))
				}
			})
			store, err := Open(ctx, path)
			if store != nil {
				store.Close()
			}
			if err == nil {
				t.Fatal("ожидалась ошибка доступа к хранилищу")
			}
			must(t, os.Chmod(directory, 0700))
			if !test.existing {
				return
			}
			must(t, os.Chmod(path, 0600))
			store, err = Open(ctx, path)
			must(t, err)
			defer store.Close()
			got, err := store.GetSource(ctx, original.ID)
			must(t, err)
			if !reflect.DeepEqual(got, original) {
				t.Fatal("проверка открытия изменила данные")
			}
			var version int
			must(t, store.db.QueryRowContext(ctx, "PRAGMA user_version").Scan(&version))
			if version != 1 {
				t.Fatalf("изменилась версия схемы: %d", version)
			}
		})
	}
}
