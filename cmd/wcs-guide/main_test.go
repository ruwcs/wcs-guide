package main

import (
	"bytes"
	"context"
	"log/slog"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/ruwcs/wcs-guide/internal/storage"
)

// TestStartupArgumentsAndConfiguration проверяет аргументы запуска и ошибки конфигурации.
func TestStartupArgumentsAndConfiguration(t *testing.T) {
	original := os.Args
	defer func() { os.Args = original }()
	dbPath := filepath.Join(t.TempDir(), "must-not-exist.db")
	t.Setenv("DB_PATH", dbPath)
	t.Setenv("BASE_URL", "")
	for _, command := range []string{"serve", "version", "help"} {
		os.Args = []string{"wcs-guide", command}
		if code := run(); code != 2 {
			t.Fatal(code)
		}
	}
	os.Args = []string{"wcs-guide"}
	if code := run(); code != 1 {
		t.Fatal(code)
	}
	if _, err := os.Stat(dbPath); !os.IsNotExist(err) {
		t.Fatal("БД создана до проверки конфигурации")
	}
}

// TestStartupReadOnlyDatabase проверяет отказ запуска с недоступной для записи БД.
func TestStartupReadOnlyDatabase(t *testing.T) {
	if runtime.GOOS == "windows" || os.Geteuid() == 0 {
		t.Skip("проверка POSIX-прав требует непривилегированного пользователя")
	}
	path := filepath.Join(t.TempDir(), "test.db")
	store, err := storage.Open(context.Background(), path)
	if err != nil {
		t.Fatal(err)
	}
	if err := store.Close(); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(path, 0400); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := os.Chmod(path, 0600); err != nil {
			t.Error(err)
		}
	})
	originalArgs, originalLogger := os.Args, slog.Default()
	t.Cleanup(func() { os.Args = originalArgs; slog.SetDefault(originalLogger) })
	os.Args = []string{"wcs-guide"}
	var logs bytes.Buffer
	slog.SetDefault(slog.New(slog.NewTextHandler(&logs, nil)))
	t.Setenv("BASE_URL", "http://localhost:8080")
	t.Setenv("DB_PATH", path)
	t.Setenv("TELEGRAM_API_ID", "123")
	t.Setenv("TELEGRAM_API_HASH", strings.Repeat("a", 32))
	t.Setenv("TELEGRAM_BOT_TOKEN", "7:SECRET")
	t.Setenv("TELEGRAM_SESSION_PATH", filepath.Join(t.TempDir(), "bot.session"))
	if code := run(); code != 1 {
		t.Fatalf("код завершения: %d; ожидался 1", code)
	}
	if !strings.Contains(logs.String(), "инициализация хранилища") {
		t.Fatal("нет сообщения об ошибке хранилища")
	}
	if strings.Contains(logs.String(), "[http] listening") {
		t.Fatal("HTTP запущен с недоступным для записи хранилищем")
	}
}
