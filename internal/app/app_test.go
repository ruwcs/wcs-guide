package app

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"net"
	"net/http"
	"net/http/httptest"
	"net/url"
	"path/filepath"
	"testing"
	"time"

	"github.com/ruwcs/wcs-guide/internal/config"
	"github.com/ruwcs/wcs-guide/internal/storage"
	"github.com/ruwcs/wcs-guide/internal/web"
)

// TestHealth проверяет ответ проверки работоспособности.
func TestHealth(t *testing.T) {
	store, err := storage.Open(context.Background(), filepath.Join(t.TempDir(), "health.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	base, _ := url.Parse("http://localhost:8080")
	handler, err := web.New(context.Background(), store, *base, "dev", slog.Default())
	if err != nil {
		t.Fatal(err)
	}
	for _, method := range []string{"GET", "HEAD"} {
		response := httptest.NewRecorder()
		handler.ServeHTTP(response, httptest.NewRequest(method, "/healthz", nil))
		if response.Code != 200 || response.Header().Get("Cache-Control") != "no-store" {
			t.Fatal(response)
		}
		expected := "ok\n"
		if method == "HEAD" {
			expected = ""
		}
		if response.Body.String() != expected {
			t.Fatal(response.Body.String())
		}
	}
	for _, path := range []string{"/unknown"} {
		response := httptest.NewRecorder()
		handler.ServeHTTP(response, httptest.NewRequest("GET", path, nil))
		if response.Code != 404 {
			t.Fatal(response.Code)
		}
	}
}

// TestShutdown проверяет штатное завершение веб-сервера.
func TestShutdown(t *testing.T) {
	for _, timeout := range []bool{false, true} {
		listener, err := net.Listen("tcp", "127.0.0.1:0")
		if err != nil {
			t.Fatal(err)
		}
		server := newServer(slog.Default(), http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { w.Write([]byte("ok")) }))
		started := make(chan struct{})
		if timeout {
			server.Handler = http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { close(started); <-r.Context().Done() })
		}
		ctx, cancel := context.WithCancel(context.Background())
		done := make(chan error, 1)
		go func() { done <- serve(ctx, server, listener, 20*time.Millisecond, slog.Default()) }()
		requestDone := make(chan struct{})
		go func() {
			defer close(requestDone)
			client := http.Client{Timeout: time.Second}
			response, err := client.Get("http://" + listener.Addr().String() + "/healthz")
			if err == nil {
				io.Copy(io.Discard, response.Body)
				response.Body.Close()
			}
		}()
		if timeout {
			<-started
		} else {
			<-requestDone
		}
		cancel()
		select {
		case err := <-done:
			if timeout && !errors.Is(err, context.DeadlineExceeded) {
				t.Fatal(err)
			}
			if !timeout && err != nil {
				t.Fatal(err)
			}
		case <-time.After(2 * time.Second):
			t.Fatal("зависание завершения")
		}
		<-requestDone
	}
}

// TestStartupFailure проверяет ошибки инициализации приложения.
func TestStartupFailure(t *testing.T) {
	cfg := config.Config{TelegramSessionPath: filepath.Join(t.TempDir(), "bot.session"), DBPath: filepath.Join(t.TempDir(), "missing", "test.db")}
	if _, err := New(context.Background(), cfg, slog.Default()); err == nil {
		t.Fatal("ожидалась ошибка хранилища")
	}
	listener, err := net.Listen("tcp", httpAddr)
	if err != nil {
		t.Skip("порт уже занят внешним процессом")
	}
	defer listener.Close()
	cfg.DBPath = filepath.Join(t.TempDir(), "test.db")
	application, err := New(context.Background(), cfg, slog.Default())
	if err != nil {
		t.Fatal(err)
	}
	defer application.Close()
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if err := application.Run(ctx); err == nil {
		t.Fatal("ожидалась ошибка занятого порта")
	}

}
