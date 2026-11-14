// Package config читает конфигурацию окружения и сведения сборки.
package config

import (
	"fmt"
	"net/url"
	"strconv"
	"strings"
	"unicode"
)

// Config содержит обязательные внешние параметры приложения.
type Config struct {
	BaseURL url.URL // Канонический внешний адрес сайта.
	DBPath  string  // Явно заданный путь к файлу SQLite.
}

// Load проверяет окружение без открытия файлов и сетевых запросов.
func Load(lookup func(string) (string, bool)) (Config, error) {
	var cfg Config
	required := func(name string) (string, error) {
		value, ok := lookup(name)
		if !ok || strings.TrimSpace(value) == "" {
			return "", fmt.Errorf("%s: необходимо непустое значение", name)
		}
		return value, nil
	}
	base, err := required("BASE_URL")
	if err != nil {
		return cfg, err
	}
	parsed, err := url.Parse(base)
	if err != nil || parsed.Hostname() == "" || (!strings.EqualFold(parsed.Scheme, "http") && !strings.EqualFold(parsed.Scheme, "https")) || parsed.User != nil || parsed.Opaque != "" || parsed.RawQuery != "" || parsed.ForceQuery || strings.Contains(base, "#") || (parsed.Path != "" && parsed.Path != "/") || parsed.RawPath != "" || strings.ContainsFunc(base, func(r rune) bool { return unicode.IsSpace(r) || unicode.IsControl(r) || r == '\\' }) {
		return cfg, fmt.Errorf("BASE_URL: требуется абсолютный http/https URL в корне сайта")
	}
	if port := parsed.Port(); port != "" {
		number, err := strconv.Atoi(port)
		if err != nil || number < 1 || number > 65535 {
			return cfg, fmt.Errorf("BASE_URL: некорректный порт")
		}
	}
	parsed.Scheme = strings.ToLower(parsed.Scheme)
	parsed.Path = ""
	cfg.BaseURL = *parsed
	cfg.DBPath, err = required("DB_PATH")
	if err != nil {
		return Config{}, err
	}
	if cfg.DBPath == ":memory:" || strings.HasPrefix(cfg.DBPath, "file:") || strings.ContainsRune(cfg.DBPath, 0) {
		return Config{}, fmt.Errorf("DB_PATH: требуется файловый путь SQLite")
	}
	return cfg, nil
}
