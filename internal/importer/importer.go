// Package importer обновляет импортируемое содержимое страниц.
package importer

import (
	"context"
	"errors"
	"fmt"
	"sort"
	"sync"
	"time"

	"github.com/ruwcs/wcs-guide/internal/content"
	"github.com/ruwcs/wcs-guide/internal/domain"
	"github.com/ruwcs/wcs-guide/internal/storage"
	"github.com/ruwcs/wcs-guide/internal/telegram"
)

// Importer — импорт содержимого из Telegram.
type Importer struct {
	mutex   sync.Mutex       // Последовательное выполнение импорта.
	Store   *storage.Storage // Хранилище.
	Fetcher Fetcher          // Клиент получения публикаций Telegram.
}

// BlockResult — результат подготовки блока.
type BlockResult struct {
	Changed         bool     // Изменение содержимого блока.
	BlockID         int64    // Идентификатор обработанного блока.
	Stage           string   // Этап успешного завершения или ошибки.
	Err             error    // Ошибка обработки блока.
	Diagnostics     int      // Количество упрощений оформления.
	DiagnosticCodes []string // Коды упрощений форматирования.
}

// Report — результат импорта источника.
type Report struct {
	RetryAfter time.Duration // Пауза перед следующей попыткой получения.
	Blocks     []BlockResult // Итоги подготовки каждого блока.
}

// Sync обновляет содержимое блоков источника.
func (i *Importer) Sync(ctx context.Context, sourceID int64) (Report, error) {
	return i.sync(ctx, sourceID, importTimeout)
}

// sync обновляет источник с заданным таймаутом получения.
func (i *Importer) sync(ctx context.Context, sourceID int64, fetchTimeout time.Duration) (report Report, err error) {
	if !i.mutex.TryLock() {
		return report, ErrBusy
	}
	defer i.mutex.Unlock()
	if err := ctx.Err(); err != nil {
		return report, err
	}
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
	fetchCtx, cancelFetch := context.WithTimeout(ctx, fetchTimeout)
	original, err := i.Fetcher.Fetch(fetchCtx, source)
	if err == nil {
		err = fetchCtx.Err()
	}
	cancelFetch()
	saveCtx, cancelSave := context.WithTimeout(ctx, saveResultTimeout)
	defer cancelSave()
	if err != nil {
		saveErr := i.Store.SaveFetchResult(saveCtx, source, time.Now().UTC(), safeFetchError(err))
		var remote *telegram.Error
		if errors.As(err, &remote) {
			report.RetryAfter = time.Duration(remote.RetryAfter) * time.Second
		}
		if saveErr == nil && errors.As(err, &remote) && (remote.Code == "identity_changed" || remote.Code == "publication_restricted") {
			saveErr = errors.Join(saveErr, i.Store.SetPublicationBlocked(saveCtx, sourceID, true, time.Now().UTC()))
		}
		return report, errors.Join(err, saveErr)
	}

	if err = i.Store.SaveFetchResult(saveCtx, source, time.Now().UTC(), ""); err != nil {
		return report, err
	}
	blocks, err := i.Store.ListBlocksBySource(ctx, sourceID)
	if err != nil {
		return report, err
	}
	normalizeErr := original.Validate()
	var document content.Document
	if normalizeErr == nil {
		document, normalizeErr = original.Document()
	}
	var failures []error
	for _, block := range blocks {
		if ctx.Err() != nil {
			return report, ctx.Err()
		}
		var result BlockResult
		if normalizeErr != nil {
			result = BlockResult{BlockID: block.ID, Stage: "normalization", Err: normalizeErr}
		} else {
			result = i.prepareBlock(ctx, source, block, document)
		}
		if result.Err != nil && result.Stage != "save" {
			result.Err = errors.Join(result.Err, i.Store.SaveBlockImportError(ctx, block.ID, source, *block.Import, result.Stage+": "+preparationError(result.Err)))
		}
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
	if errors.Is(err, context.DeadlineExceeded) {
		return "telegram fetch timed out"
	}
	if errors.Is(err, context.Canceled) {
		return "import cancelled"
	}
	return "telegram fetch failed"
}

// prepareBlock подготавливает содержимое импортируемого блока.
func (i *Importer) prepareBlock(ctx context.Context, source domain.Source, block domain.Block, document content.Document) (result BlockResult) {
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
	result.Changed, result.Err = i.Store.SaveImportedContent(ctx, block.ID, source, *block.Import, markdown.Text, at)
	if result.Err == nil {
		result.Stage = "complete"
	}
	return
}

// ErrBusy — импорт уже выполняется.
var ErrBusy = errors.New("importer: обновление уже выполняется")

// importTimeout — таймаут получения публикации.
const importTimeout = time.Minute

// saveResultTimeout — таймаут сохранения результата получения.
const saveResultTimeout = 5 * time.Second

// preparationError возвращает причину ошибки подготовки блока.
func preparationError(err error) string {
	switch {
	case errors.Is(err, content.ErrAnchorNotFound):
		return "anchor_not_found"
	case errors.Is(err, content.ErrAnchorAmbiguous):
		return "anchor_ambiguous"
	case errors.Is(err, content.ErrAnchorOrder):
		return "anchor_order"
	case errors.Is(err, content.ErrEmptyResult):
		return "empty_result"
	case errors.Is(err, content.ErrLimitExceeded):
		return "limit_exceeded"
	default:
		return "invalid_content"
	}
}
