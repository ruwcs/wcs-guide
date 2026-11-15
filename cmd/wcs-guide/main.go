package main

import (
	"context"
	"log/slog"
	"os"
	"os/signal"
	"syscall"

	"github.com/ruwcs/wcs-guide/internal/app"
	"github.com/ruwcs/wcs-guide/internal/config"
)

// main запускает программу.
func main() { os.Exit(run()) }

// run выполняет основной сценарий программы.
func run() int {
	logger := slog.Default()
	if len(os.Args) != 1 {
		logger.Error("[app] команды не поддерживаются")
		return 2
	}
	cfg, err := config.Load(os.LookupEnv)
	if err != nil {
		logger.Error("[app] ошибка конфигурации", "error", err)
		return 1
	}
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	application, err := app.New(ctx, cfg, logger)
	if err != nil {
		logger.Error("[app] ошибка инициализации", "error", err)
		return 1
	}
	defer application.Close()
	if err := application.Run(ctx); err != nil {
		logger.Error("[app] завершение с ошибкой", "error", err)
		return 1
	}
	logger.Info("[app] stopped")
	return 0
}
