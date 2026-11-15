package telegram

import (
	"context"
	"testing"
	"time"

	"github.com/gotd/td/bin"
	td "github.com/gotd/td/telegram"
)

// TestSessionPause проверяет запрет RPC на время FLOOD_WAIT.
func TestSessionPause(t *testing.T) {
	client := New(1, "hash", "1:token", "unused")
	calls := 0
	invoke := client.rateLimit(td.InvokeFunc(func(context.Context, bin.Encoder, bin.Decoder) error {
		calls++
		return &Error{Code: "rate_limit", RetryAfter: 600}
	}))
	if err := invoke(context.Background(), nil, nil); err == nil {
		t.Fatal("нет ошибки")
	}
	if err := invoke(context.Background(), nil, nil); err == nil || calls != 1 {
		t.Fatal("выполнен запрос во время паузы")
	}
	client.mutex.Lock()
	client.pausedUntil = time.Now().Add(-time.Second)
	client.mutex.Unlock()
	invoke(context.Background(), nil, nil)
	if calls != 2 {
		t.Fatal("запрет не завершился")
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if err := invoke(ctx, nil, nil); err != context.Canceled {
		t.Fatal(err)
	}
}
