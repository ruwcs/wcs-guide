// Package importer обновляет импортируемое содержимое страниц.
package importer

import (
	"context"
	"errors"
	"fmt"
	"sort"
	"time"

	"github.com/ruwcs/wcs-guide/internal/content"
	"github.com/ruwcs/wcs-guide/internal/domain"
	"github.com/ruwcs/wcs-guide/internal/storage"
	"github.com/ruwcs/wcs-guide/internal/telegram"
)

// Importer — импорт содержимого из Telegram.
type Importer struct {
	Store   *storage.Storage // Хранилище.
	Fetcher Fetcher          // Клиент получения публикаций Telegram.
}

// BlockResult — результат подготовки блока.
type BlockResult struct {
	BlockID         int64    // Идентификатор обработанного блока.
	Stage           string   // Этап успешного завершения или ошибки.
	Err             error    // Ошибка обработки блока.
	Diagnostics     int      // Количество упрощений оформления.
	DiagnosticCodes []string // Коды упрощений форматирования.
}

// Report — результат импорта источника.
type Report struct {
	Blocks []BlockResult // Итоги подготовки каждого блока.
}

// Sync обновляет содержимое блоков источника.
func (i *Importer) Sync(ctx context.Context, sourceID int64) (report Report, err error) {
	source, err := i.Store.GetSource(ctx, sourceID)
	if err != nil {
		return report, err
	}
	if source.PublicationBlocked {
		return report, fmt.Errorf("importer: publication blocked")
	}
	if source.Type != domain.SourceTelegramPost {
		return report, fmt.Errorf("importer: unsupported source")
	}
	original, err := i.Fetcher.Fetch(ctx, source)
	if err != nil {
		saveErr := i.Store.SaveFetchResult(ctx, sourceID, time.Now().UTC(), safeFetchError(err))
		var remote *telegram.Error
		if errors.As(err, &remote) && (remote.Code == "identity_changed" || remote.Code == "publication_restricted") {
			saveErr = errors.Join(saveErr, i.Store.SetPublicationBlocked(ctx, sourceID, true, time.Now().UTC()))
		}
		return report, errors.Join(err, saveErr)
	}

	if err := original.Validate(); err != nil {
		return report, errors.Join(err, i.Store.SaveFetchResult(ctx, sourceID, time.Now().UTC(), "telegram: invalid_original"))
	}
	document, err := original.Document()
	if err != nil {
		return report, errors.Join(err, i.Store.SaveFetchResult(ctx, sourceID, time.Now().UTC(), "telegram: invalid_content"))
	}
	if err = i.Store.SaveFetchResult(ctx, sourceID, time.Now().UTC(), ""); err != nil {
		return report, err
	}
	blocks, err := i.Store.ListBlocksBySource(ctx, sourceID)
	if err != nil {
		return report, err
	}
	var failures []error
	for _, block := range blocks {
		if ctx.Err() != nil {
			return report, ctx.Err()
		}
		result := i.prepareBlock(ctx, block, document)
		if result.Err != nil {
			failures = append(failures, fmt.Errorf("importer: block %d failed at %s", block.ID, result.Stage))
		}
		report.Blocks = append(report.Blocks, result)
	}

	return report, errors.Join(failures...)
}

// safeFetchError возвращает описание ошибки получения публикации для журнала синхронизации.
func safeFetchError(err error) string {
	var remote *telegram.Error
	if errors.As(err, &remote) {
		if remote.RetryAfter > 0 {
			return fmt.Sprintf("telegram: %s retry_after_seconds=%d", remote.Code, remote.RetryAfter)
		}
		return "telegram: " + remote.Code
	}
	if errors.Is(err, context.Canceled) {
		return "import cancelled"
	}
	return "telegram fetch failed"
}

// prepareBlock подготавливает содержимое импортируемого блока.
func (i *Importer) prepareBlock(ctx context.Context, block domain.Block, document content.Document) (result BlockResult) {
	result.BlockID = block.ID
	result.Stage = "extraction"
	fragment, err := content.Extract(document, block.Import.Rules)
	if err != nil {
		result.Err = err
		return
	}
	result.Stage = "markdown"
	markdown, err := content.ToMarkdown(fragment)
	if err != nil {
		result.Err = err
		return
	}
	result.Diagnostics = len(markdown.Diagnostics)
	codes := map[string]bool{}
	for _, diagnostic := range markdown.Diagnostics {
		codes[diagnostic.Code] = true
	}
	for code := range codes {
		result.DiagnosticCodes = append(result.DiagnosticCodes, code)
	}
	sort.Strings(result.DiagnosticCodes)
	result.Stage = "save"
	at := time.Now().UTC()
	result.Err = i.Store.SaveImportedContent(ctx, block.ID, block.Import.SourceID, markdown.Text, at)
	if result.Err == nil {
		result.Stage = "complete"
	}
	return
}
