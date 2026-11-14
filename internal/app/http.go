package app

import (
	"log/slog"
	"net/http"
	"time"
)

const (
	// readHeaderTimeout ограничивает время получения HTTP-заголовков.
	readHeaderTimeout = 5 * time.Second
	// readTimeout ограничивает время чтения HTTP-запроса.
	readTimeout = 15 * time.Second
	// writeTimeout ограничивает время отправки HTTP-ответа.
	writeTimeout = 30 * time.Second
	// idleTimeout ограничивает простой соединения между запросами.
	idleTimeout = 60 * time.Second
	// maxHeaderBytes ограничивает размер заголовков одним МиБ.
	maxHeaderBytes = 1 << 20
)

func newServer(logger *slog.Logger) *http.Server {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /healthz", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/plain; charset=utf-8")
		w.Header().Set("Cache-Control", "no-store")
		w.WriteHeader(http.StatusOK)
		if r.Method != http.MethodHead {
			_, _ = w.Write([]byte("ok\n"))
		}
	})
	return &http.Server{
		Handler:           mux,
		ReadHeaderTimeout: readHeaderTimeout,
		ReadTimeout:       readTimeout,
		WriteTimeout:      writeTimeout,
		IdleTimeout:       idleTimeout,
		MaxHeaderBytes:    maxHeaderBytes,
		ErrorLog:          slog.NewLogLogger(logger.Handler(), slog.LevelError),
	}
}
