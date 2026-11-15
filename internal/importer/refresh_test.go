package importer

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"path/filepath"
	"testing"
	"time"

	"github.com/ruwcs/wcs-guide/internal/domain"
	"github.com/ruwcs/wcs-guide/internal/storage"
	"github.com/ruwcs/wcs-guide/internal/telegram"
)

// refreshFetcher — источник с управляемым завершением получения.
type refreshFetcher struct {
	calls   chan int64    // Начатые получения.
	release chan struct{} // Разрешение завершить получение.
	rate    bool          // Ограничение Telegram.
}

// Fetch реализует интерфейс Fetcher для refreshFetcher.
func (f *refreshFetcher) Fetch(ctx context.Context, source domain.Source) (telegram.Publication, error) {
	select {
	case f.calls <- source.ID:
	case <-ctx.Done():
		return telegram.Publication{}, ctx.Err()
	}
	if f.release != nil {
		select {
		case <-f.release:
		case <-ctx.Done():
			return telegram.Publication{}, ctx.Err()
		}
	}
	if f.rate {
		return telegram.Publication{}, &telegram.Error{Code: "rate_limit", RetryAfter: 3600}
	}
	if source.Name == "failure" {
		return telegram.Publication{}, errors.New("недоступен")
	}
	return telegram.Publication{Text: "Материал"}, nil
}

// nextCall ожидает вызов тестового источника.
func nextCall(t *testing.T, calls <-chan int64) int64 {
	t.Helper()
	select {
	case id := <-calls:
		return id
	case <-time.After(time.Second):
		t.Fatal("нет вызова")
		return 0
	}
}

// TestRefreshSequential проверяет последовательность, повтор цикла и отмену.
func TestRefreshSequential(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	store, err := storage.Open(ctx, filepath.Join(t.TempDir(), "refresh.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	first, err := store.CreateSource(ctx, domain.Source{Name: "failure", Type: domain.SourceTelegramPost, ExternalRef: "first", AutoRefresh: true})
	if err != nil {
		t.Fatal(err)
	}
	second, err := store.CreateSource(ctx, domain.Source{Name: "second", Type: domain.SourceTelegramPost, ExternalRef: "second", AutoRefresh: true})
	if err != nil {
		t.Fatal(err)
	}
	disabled, err := store.CreateSource(ctx, domain.Source{Name: "disabled", Type: domain.SourceTelegramPost, ExternalRef: "disabled"})
	if err != nil {
		t.Fatal(err)
	}
	fetcher := &refreshFetcher{calls: make(chan int64, 8), release: make(chan struct{})}
	runner := &Importer{Store: store, Fetcher: fetcher}
	done := make(chan struct{})
	go func() {
		defer close(done)
		runner.Run(ctx, 10*time.Millisecond, slog.New(slog.NewTextHandler(io.Discard, nil)))
	}()
	if nextCall(t, fetcher.calls) != first.ID {
		t.Fatal("неверный порядок")
	}
	if _, err := runner.Sync(ctx, first.ID); !errors.Is(err, ErrBusy) {
		t.Fatal(err)
	}
	select {
	case <-fetcher.calls:
		t.Fatal("наложение операций")
	default:
	}
	fetcher.release <- struct{}{}
	if nextCall(t, fetcher.calls) != second.ID {
		t.Fatal("ошибка остановила цикл")
	}
	if err := store.SetAutoRefresh(ctx, first.ID, false); err != nil {
		t.Fatal(err)
	}
	if err := store.SetAutoRefresh(ctx, disabled.ID, true); err != nil {
		t.Fatal(err)
	}
	fetcher.release <- struct{}{}
	if nextCall(t, fetcher.calls) != second.ID {
		t.Fatal("устаревший список")
	}
	fetcher.release <- struct{}{}
	if nextCall(t, fetcher.calls) != disabled.ID {
		t.Fatal("новый источник пропущен")
	}
	cancel()
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("зависла отмена")
	}
	select {
	case <-fetcher.calls:
		t.Fatal("запуск после отмены")
	default:
	}
}

// TestRefreshSessionPause проверяет остановку цикла при ограничении Telegram.
func TestRefreshSessionPause(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	store, err := storage.Open(ctx, filepath.Join(t.TempDir(), "pause.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	for _, ref := range []string{"one", "two"} {
		if _, err := store.CreateSource(ctx, domain.Source{Name: ref, Type: domain.SourceTelegramPost, ExternalRef: ref, AutoRefresh: true}); err != nil {
			t.Fatal(err)
		}
	}
	fetcher := &refreshFetcher{calls: make(chan int64, 8), rate: true}
	runner := &Importer{Store: store, Fetcher: fetcher}
	done := make(chan struct{})
	go func() {
		defer close(done)
		runner.Run(ctx, time.Millisecond, slog.New(slog.NewTextHandler(io.Discard, nil)))
	}()
	nextCall(t, fetcher.calls)
	select {
	case <-fetcher.calls:
		t.Fatal("запрос во время паузы")
	case <-time.After(20 * time.Millisecond):
	}
	cancel()
	<-done
}
