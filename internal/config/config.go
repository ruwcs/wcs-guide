// Package config читает конфигурацию окружения и сведения сборки.
package config

import (
	"encoding/hex"
	"fmt"
	"net/url"
	"strconv"
	"strings"
	"time"
	"unicode"
)

// Config — конфигурация приложения.
type Config struct {
	EditorBaseURL       url.URL       // Адрес редакторского интерфейса.
	RefreshInterval     time.Duration // Интервал автоматического обновления источников.
	BaseURL             url.URL       // Адрес сайта, например "https://wcs.guide".
	DBPath              string        // Путь к файлу базы данных.
	TelegramAPIID       int           // Идентификатор приложения Telegram.
	TelegramAPIHash     string        // Секрет приложения Telegram.
	TelegramBotToken    string        // Токен бота Telegram.
	TelegramSessionPath string        // Путь к файлу сессии Telegram.
}

// Load загружает конфигурацию.
func Load(lookup func(string) (string, bool)) (Config, error) {
	var cfg Config
	cfg.RefreshInterval = defaultRefreshInterval
	if raw, ok := lookup("REFRESH_INTERVAL"); ok {
		minutes, err := strconv.ParseInt(raw, 10, 64)
		if err != nil || minutes <= 0 || minutes > int64((1<<63-1)/time.Minute) {
			return cfg, fmt.Errorf("REFRESH_INTERVAL: требуется положительное целое число минут")
		}
		cfg.RefreshInterval = time.Duration(minutes) * time.Minute
	}
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
	editor, err := required("EDITOR_BASE_URL")
	if err != nil {
		return cfg, err
	}
	editorURL, err := url.Parse(editor)
	if err != nil || editorURL.Hostname() == "" || editorURL.User != nil || editorURL.Opaque != "" || editorURL.RawQuery != "" || editorURL.ForceQuery || strings.ContainsAny(editor, "#\\") || editorURL.RawPath != "" || (editorURL.Path != "" && editorURL.Path != "/") || strings.ContainsFunc(editor, func(r rune) bool { return unicode.IsSpace(r) || unicode.IsControl(r) }) {
		return cfg, fmt.Errorf("EDITOR_BASE_URL: требуется корневой URL редактора")
	}
	editorURL.Scheme = strings.ToLower(editorURL.Scheme)
	if editorURL.Scheme != "https" && !(editorURL.Scheme == "http" && strings.EqualFold(editorURL.Hostname(), "localhost")) {
		return cfg, fmt.Errorf("EDITOR_BASE_URL: требуется HTTPS или HTTP localhost")
	}
	if port := editorURL.Port(); port != "" {
		number, err := strconv.Atoi(port)
		if err != nil || number < 1 || number > 65535 {
			return cfg, fmt.Errorf("EDITOR_BASE_URL: некорректный порт")
		}
	}
	editorURL.Path = ""
	editorURL.Host = strings.ToLower(editorURL.Host)
	if editorURL.Scheme == "https" && editorURL.Port() == "443" {
		editorURL.Host = strings.TrimSuffix(editorURL.Host, ":443")
	}
	if editorURL.Scheme == "http" && editorURL.Port() == "80" {
		editorURL.Host = strings.TrimSuffix(editorURL.Host, ":80")
	}
	if strings.EqualFold(editorURL.Hostname(), parsed.Hostname()) {
		return cfg, fmt.Errorf("адреса публичного сайта и редактора должны различаться")
	}
	cfg.EditorBaseURL = *editorURL

	cfg.DBPath, err = LoadDBPath(lookup)
	if err != nil {
		return cfg, err
	}
	raw, _ := lookup("TELEGRAM_API_ID")
	cfg.TelegramAPIID, err = strconv.Atoi(raw)
	if err != nil || cfg.TelegramAPIID <= 0 || cfg.TelegramAPIID > 2147483647 {
		return cfg, fmt.Errorf("TELEGRAM_API_ID: требуется положительное 32-битное число")
	}
	cfg.TelegramAPIHash, _ = lookup("TELEGRAM_API_HASH")
	cfg.TelegramBotToken, _ = lookup("TELEGRAM_BOT_TOKEN")
	if strings.TrimSpace(cfg.TelegramAPIHash) == "" || strings.TrimSpace(cfg.TelegramBotToken) == "" {
		return cfg, fmt.Errorf("необходимы TELEGRAM_API_HASH и TELEGRAM_BOT_TOKEN")
	}
	decoded, err := hex.DecodeString(cfg.TelegramAPIHash)
	if err != nil || len(decoded) != 16 {
		return cfg, fmt.Errorf("TELEGRAM_API_HASH: требуется 32 шестнадцатеричных символа")
	}
	botID, secret, found := strings.Cut(cfg.TelegramBotToken, ":")
	number, err := strconv.ParseInt(botID, 10, 64)
	if !found || err != nil || number <= 0 || secret == "" || strings.ContainsFunc(secret, func(char rune) bool {
		return !(char >= 'a' && char <= 'z' || char >= 'A' && char <= 'Z' || char >= '0' && char <= '9' || char == '_' || char == '-')
	}) {
		return cfg, fmt.Errorf("TELEGRAM_BOT_TOKEN: некорректный формат")
	}
	cfg.TelegramSessionPath, err = required("TELEGRAM_SESSION_PATH")
	return cfg, err
}

// LoadDBPath загружает путь к БД.
func LoadDBPath(lookup func(string) (string, bool)) (string, error) {
	value, ok := lookup("DB_PATH")
	if !ok || strings.TrimSpace(value) == "" || value == ":memory:" || strings.HasPrefix(value, "file:") || strings.ContainsRune(value, 0) {
		return "", fmt.Errorf("DB_PATH: требуется файловый путь SQLite")
	}
	return value, nil
}

// defaultRefreshInterval — интервал обновления по умолчанию.
const defaultRefreshInterval = 10 * time.Minute
