// Package app связывает хранилище, HTTP и жизненный цикл приложения.
package app

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net"
	"net/http"
	"time"

	"github.com/ruwcs/wcs-guide/internal/config"
	"github.com/ruwcs/wcs-guide/internal/importer"
	"github.com/ruwcs/wcs-guide/internal/storage"
	"github.com/ruwcs/wcs-guide/internal/telegram"
	"github.com/ruwcs/wcs-guide/internal/web"
)

// httpAddr — адрес и порт веб-сервера.
const httpAddr = ":8080"

// shutdownTimeout — время для завершения веб-сервера.
const shutdownTimeout = 10 * time.Second

// App — приложение WCS Guide.
type App struct {
	cfg      config.Config      // Конфигурация приложения.
	logger   *slog.Logger       // Журнал приложения.
	Store    *storage.Storage   // Хранилище.
	Telegram *telegram.Client   // Клиент Telegram.
	Importer *importer.Importer // Импорт материалов.
}

// New возвращает новое приложение.
func New(ctx context.Context, cfg config.Config, logger *slog.Logger) (*App, error) {
	if err := telegram.CheckSession(ctx, cfg.TelegramSessionPath); err != nil {
		return nil, err
	}
	store, err := storage.Open(ctx, cfg.DBPath)
	if err != nil {
		return nil, fmt.Errorf("инициализация хранилища: %w", err)
	}
	client := telegram.New(cfg.TelegramAPIID, cfg.TelegramAPIHash, cfg.TelegramBotToken, cfg.TelegramSessionPath)
	return &App{cfg: cfg, logger: logger, Store: store, Telegram: client, Importer: &importer.Importer{Store: store, Fetcher: client}}, nil
}

// Close освобождает ресурсы приложения.
func (app *App) Close() error { return app.Store.Close() }

// Run запускает приложение.
func (app *App) Run(ctx context.Context) error {
	cfg, logger, store := app.cfg, app.logger, app.Store
	ctx, cancel := context.WithCancel(ctx)
	done := make(chan struct{})
	go func() { defer close(done); app.Telegram.Run(ctx, logger) }()
	defer func() { cancel(); <-done }()
	handler, err := web.New(ctx, store, cfg.BaseURL, config.Version(), logger)
	if err != nil {
		return err
	}
	listener, err := net.Listen("tcp", httpAddr)
	if err != nil {
		return fmt.Errorf("запуск HTTP: %w", err)
	}
	server := newServer(logger, handler)
	logger.Info("[http] listening", "addr", httpAddr)
	return serve(ctx, server, listener, shutdownTimeout, logger)
}

// serve запускает веб-сервер и обрабатывает завершение приложения.
func serve(ctx context.Context, server *http.Server, listener net.Listener, timeout time.Duration, logger *slog.Logger) error {
	defer listener.Close()
	done := make(chan error, 1)
	go func() { done <- server.Serve(listener) }()
	select {
	case err := <-done:
		closeErr := server.Close()
		if errors.Is(err, http.ErrServerClosed) {
			err = nil
		}
		return errors.Join(err, closeErr)
	case <-ctx.Done():
		logger.Info("[app] shutdown requested")
	}
	shutdownCtx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()
	err := server.Shutdown(shutdownCtx)
	if err != nil {
		err = errors.Join(err, server.Close())
	}
	serveErr := <-done
	if errors.Is(serveErr, http.ErrServerClosed) {
		serveErr = nil
	}
	logger.Info("[http] stopped")
	return errors.Join(err, serveErr)
}
