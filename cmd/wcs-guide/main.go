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

func main() { os.Exit(run()) }
func run() int {
	logger := slog.Default()
	if len(os.Args) != 1 {
		logger.Error("[app] аргументы не поддерживаются")
		return 2
	}
	cfg, err := config.Load(os.LookupEnv)
	if err != nil {
		logger.Error("[app] ошибка конфигурации", "error", err)
		return 1
	}
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	if err := app.Run(ctx, cfg, logger); err != nil {
		logger.Error("[app] завершение с ошибкой", "error", err)
		return 1
	}
	logger.Info("[app] stopped")
	return 0
}
