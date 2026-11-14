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
	"github.com/ruwcs/wcs-guide/internal/storage"
)

// httpAddr фиксирует адрес и порт прослушивания приложения.
const httpAddr = ":8080"

// shutdownTimeout ограничивает время штатного завершения HTTP-сервера.
const shutdownTimeout = 10 * time.Second

// Run открывает хранилище, обслуживает HTTP и освобождает ресурсы перед возвратом.
func Run(ctx context.Context, cfg config.Config, logger *slog.Logger) (err error) {
	logger.Info("[app] starting", "version", config.Version())
	store, err := storage.Open(ctx, cfg.DBPath)
	if err != nil {
		return fmt.Errorf("инициализация хранилища: %w", err)
	}
	defer func() { err = errors.Join(err, store.Close()); logger.Info("[storage] closed") }()
	logger.Info("[storage] initialized")
	listener, err := net.Listen("tcp", httpAddr)
	if err != nil {
		return fmt.Errorf("запуск HTTP: %w", err)
	}
	server := newServer(logger)
	logger.Info("[http] listening", "addr", httpAddr)
	return serve(ctx, server, listener, shutdownTimeout, logger)
}

// serve завершает HTTP перед возвратом и не закрывает хранилище самостоятельно.
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
