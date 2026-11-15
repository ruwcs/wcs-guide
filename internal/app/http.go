package app

import (
	"log/slog"
	"net/http"
	"time"
)

const (
	// readHeaderTimeout — таймаут получения HTTP-заголовков.
	readHeaderTimeout = 5 * time.Second
	// readTimeout — таймаут чтения HTTP-запроса.
	readTimeout = 15 * time.Second
	// writeTimeout — таймаут отправки HTTP-ответа.
	writeTimeout = 30 * time.Second
	// idleTimeout — таймаут простоя HTTP-соединения.
	idleTimeout = 60 * time.Second
	// maxHeaderBytes — максимальный размер HTTP-заголовков, 1 МиБ.
	maxHeaderBytes = 1 << 20
)

// newServer возвращает новый веб-сервер.
func newServer(logger *slog.Logger, handler http.Handler) *http.Server {
	return &http.Server{
		Handler:           handler,
		ReadHeaderTimeout: readHeaderTimeout,
		ReadTimeout:       readTimeout,
		WriteTimeout:      writeTimeout,
		IdleTimeout:       idleTimeout,
		MaxHeaderBytes:    maxHeaderBytes,
		ErrorLog:          slog.NewLogLogger(logger.Handler(), slog.LevelError),
	}
}
