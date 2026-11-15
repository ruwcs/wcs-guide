package importer

import (
	"context"
	"errors"
	"log/slog"
	"time"
)

// Run автоматически обновляет источники.
func (i *Importer) Run(ctx context.Context, interval time.Duration, logger *slog.Logger) {
	logger.Info("[refresh] запуск", "interval", interval)
	defer logger.Info("[refresh] завершение")
	for ctx.Err() == nil {
		logger.Info("[refresh] начало цикла")
		delay := interval
		sources, err := i.Store.ListAutoRefreshSources(ctx)
		if err != nil {
			logger.Error("[refresh] ошибка списка источников", "error", err)
		}
		for _, source := range sources {
			if ctx.Err() != nil {
				return
			}
			current, err := i.Store.GetSource(ctx, source.ID)
			if err != nil || !current.AutoRefresh || current.PublicationBlocked {
				continue
			}
			report, err := i.Sync(ctx, source.ID)
			if ctx.Err() != nil {
				return
			}
			if errors.Is(err, ErrBusy) {
				break
			}
			if err != nil {
				logger.Error("[refresh] ошибка обновления", "source_id", source.ID, "error", err)
			}
			for _, block := range report.Blocks {
				if block.Err != nil {
					logger.Error("[refresh] ошибка блока", "block_id", block.BlockID, "stage", block.Stage, "error", block.Err)
				} else if block.Changed {
					logger.Info("[refresh] содержимое обновлено", "block_id", block.BlockID)
				} else if block.Diagnostics > 0 {
					logger.Debug("[refresh] упрощение оформления", "block_id", block.BlockID, "codes", block.DiagnosticCodes)
				}
			}
			if report.RetryAfter > 0 {
				if report.RetryAfter > delay {
					delay = report.RetryAfter
				}
				logger.Warn("[refresh] ограничение Telegram", "retry_after", report.RetryAfter)
				break
			}
		}
		logger.Info("[refresh] завершение цикла")
		timer := time.NewTimer(delay)
		select {
		case <-ctx.Done():
			timer.Stop()
			return
		case <-timer.C:
		}
	}
}
