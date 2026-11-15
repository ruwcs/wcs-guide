package telegram

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/gotd/td/session"
	"github.com/gotd/td/telegram/auth"
	"github.com/gotd/td/tg"
)

// fakeAuthorizer — тестовая авторизация Telegram.
type fakeAuthorizer struct {
	Authorized bool     // Имеется действующая сессия.
	Calls      int      // Число новых входов.
	User       *tg.User // Установленная идентичность.
}

// Status реализует интерфейс botAuthorizer для fakeAuthorizer.
func (f *fakeAuthorizer) Status(context.Context) (*auth.Status, error) {
	return &auth.Status{Authorized: f.Authorized, User: f.User}, nil
}

// Bot реализует интерфейс botAuthorizer для fakeAuthorizer.
func (f *fakeAuthorizer) Bot(context.Context, string) (*tg.AuthAuthorization, error) {
	f.Calls++
	return &tg.AuthAuthorization{User: f.User}, nil
}

// TestReuseBotAuthorization проверяет повторное использование авторизации бота.
func TestReuseBotAuthorization(t *testing.T) {
	existing := &fakeAuthorizer{Authorized: true, User: &tg.User{ID: 7, Bot: true}}
	if err := authorizeBot(context.Background(), existing, "7:SECRET"); err != nil || existing.Calls != 0 {
		t.Fatal("повторный вход для действующей сессии", err)
	}
	existing.Authorized = false
	if err := authorizeBot(context.Background(), existing, "7:SECRET"); err != nil || existing.Calls != 1 {
		t.Fatal("не выполнен первый вход", err)
	}
	existing.Authorized = true
	existing.User = &tg.User{ID: 7, Bot: false}
	if err := authorizeBot(context.Background(), existing, "7:SECRET"); err == nil {
		t.Fatal("принята пользовательская сессия")
	}
	existing.User = &tg.User{ID: 8, Bot: true}
	if err := authorizeBot(context.Background(), existing, "7:SECRET"); err == nil {
		t.Fatal("принята сессия другого бота")
	}
}

// TestSessionValidation проверяет доступность и целостность сессии.
func TestSessionValidation(t *testing.T) {
	ctx := context.Background()
	path := filepath.Join(t.TempDir(), "bot.session")
	if err := CheckSession(ctx, path); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(path); !os.IsNotExist(err) {
		t.Fatal("оставлен пустой файл")
	}
	for _, data := range []string{"", "not json", `{"Version":999,"Data":{}}`} {
		if err := os.WriteFile(path, []byte(data), 0600); err != nil {
			t.Fatal(err)
		}
		if err := CheckSession(ctx, path); err == nil {
			t.Fatal("принята поврежденная сессия")
		}
	}
	loader := session.Loader{Storage: &session.FileStorage{Path: path}}
	if err := loader.Save(ctx, &session.Data{}); err != nil {
		t.Fatal(err)
	}
	if err := CheckSession(ctx, path); err != nil {
		t.Fatal(err)
	}
	if err := CheckSession(ctx, filepath.Join(t.TempDir(), "missing", "session")); err == nil {
		t.Fatal("нет каталога")
	}
}

// TestAuthorizationRetry проверяет паузы повторного входа и отмену ожидания.
func TestAuthorizationRetry(t *testing.T) {
	if retryDelay(&Error{Code: "authorization"}) != 5*time.Minute {
		t.Fatal("неверный интервал")
	}
	if retryDelay(rpcError(&Error{Code: "rate_limit", RetryAfter: 900})) != 15*time.Minute {
		t.Fatal("потерян FLOOD_WAIT")
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	client := New(123, "hash", "7:token", "unused")
	if err := client.WaitReady(ctx); err != context.Canceled {
		t.Fatal(err)
	}
}
