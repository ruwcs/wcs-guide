package config

import (
	"strings"
	"testing"
)

// TestLoad проверяет загрузку конфигурации окружения.
func TestLoad(t *testing.T) {
	cases := []struct {
		base  string // Внешний адрес для проверки.
		path  string // Путь к БД.
		valid bool   // Допустимость конфигурации.
	}{{"http://localhost:8080", "./data/test.db", true}, {"https://wcs.guide/", "/data/test.db", true}, {"https://wcs.guide:65536", "test.db", false}, {"HTTP://localhost:8080", "test.db", true}, {"", "a.db", false}, {"https://wcs.guide", "", false}, {"https://user:SECRET@wcs.guide", "a.db", false}, {"https://wcs.guide/sub", "a.db", false}, {"https://wcs.guide?x=1", "a.db", false}, {"https://wcs.guide#", "a.db", false}, {"https://wcs.guide", "file:a.db", false}, {"https://wcs.guide", ":memory:", false}, {"localhost:8080", "a.db", false}}
	for _, tc := range cases {
		cfg, err := Load(func(name string) (string, bool) {
			if name == "BASE_URL" {
				return tc.base, true
			}
			if name == "DB_PATH" {
				return tc.path, true
			}
			return map[string]string{"TELEGRAM_API_ID": "123", "TELEGRAM_API_HASH": strings.Repeat("a", 32), "TELEGRAM_BOT_TOKEN": "7:SECRET", "TELEGRAM_SESSION_PATH": "test.session"}[name], true
		})
		if (err == nil) != tc.valid {
			t.Fatalf("некорректный результат: %v", err)
		}
		if err != nil && strings.Contains(err.Error(), "SECRET") {
			t.Fatal("утечка конфигурации")
		}
		if err == nil && cfg.BaseURL.Path != "" {
			t.Fatal("не приведён корневой путь")
		}
	}
	if _, err := Load(func(string) (string, bool) { return "", false }); err == nil {
		t.Fatal("нет обязательной переменной")
	}
}

// TestBuildVersion проверяет версию приложения.
func TestBuildVersion(t *testing.T) {
	originalVersion := version
	defer func() { version = originalVersion }()
	version = ""
	if Version() != "dev" {
		t.Fatal("неверные значения по умолчанию")
	}
	version = "test-build"
	if Version() != "test-build" {
		t.Fatal("потеряны значения сборки")
	}
}

// TestRequiredConfiguration проверяет обязательные параметры конфигурации.
func TestRequiredConfiguration(t *testing.T) {
	values := map[string]string{"BASE_URL": "https://wcs.guide", "TELEGRAM_SESSION_PATH": "test.session", "DB_PATH": "./test.db", "TELEGRAM_API_ID": "123", "TELEGRAM_API_HASH": strings.Repeat("a", 32), "TELEGRAM_BOT_TOKEN": "7:TOKEN_SECRET"}
	lookup := func(name string) (string, bool) { value, ok := values[name]; return value, ok }
	if _, err := Load(lookup); err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"TELEGRAM_API_ID", "TELEGRAM_API_HASH", "TELEGRAM_BOT_TOKEN", "DB_PATH", "BASE_URL", "TELEGRAM_SESSION_PATH"} {
		original := values[name]
		values[name] = ""
		if _, err := Load(lookup); err == nil {
			t.Fatal(name)
		} else if strings.Contains(err.Error(), "SECRET") {
			t.Fatal("утечка секрета")
		}
		values[name] = original
	}
}
