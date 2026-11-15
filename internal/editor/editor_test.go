package editor

import (
	"context"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"net/url"
	"path/filepath"
	"strings"
	"testing"

	"github.com/ruwcs/wcs-guide/internal/auth"
	"github.com/ruwcs/wcs-guide/internal/storage"
)

// TestEditorSecurity проверяет защиту редакторских запросов и cookie операции.
func TestEditorSecurity(t *testing.T) {
	ctx := context.Background()
	store, err := storage.Open(ctx, filepath.Join(t.TempDir(), "editor.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	_, code, err := auth.Bootstrap(ctx, store, "owner", "Владелец")
	if err != nil {
		t.Fatal(err)
	}
	origin, _ := url.Parse("https://editor.example.org")
	service, err := auth.New(store, *origin)
	if err != nil {
		t.Fatal(err)
	}
	handler, err := New(service, *origin, slog.Default())
	if err != nil {
		t.Fatal(err)
	}
	for _, from := range []string{"", "https://evil.example.org", "null", origin.String()} {
		request := httptest.NewRequest("POST", origin.String()+"/auth/register/begin", strings.NewReader(`{"Value":"`+code+`"}`))
		request.Header.Set("Content-Type", "application/json")
		if from != "" {
			request.Header.Set("Origin", from)
		}
		response := httptest.NewRecorder()
		handler.ServeHTTP(response, request)
		if from != origin.String() {
			if response.Code != 403 {
				t.Fatal(from, response.Code)
			}
		} else {
			if response.Code != 200 {
				t.Fatal(response.Code, response.Body.String())
			}
			cookie := response.Result().Cookies()[0]
			if !cookie.Secure || !cookie.HttpOnly || cookie.Domain != "" || cookie.Path != "/" || cookie.SameSite != http.SameSiteStrictMode || !strings.HasPrefix(cookie.Name, "__Host-") {
				t.Fatal("небезопасная cookie")
			}
		}
	}
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, httptest.NewRequest("GET", origin.String()+"/", nil))
	if response.Code != 303 || response.Header().Get("Location") != "/login" {
		t.Fatal(response.Code)
	}
	response = httptest.NewRecorder()
	handler.ServeHTTP(response, httptest.NewRequest("GET", origin.String()+"/static/../../go.mod", nil))
	if response.Code != 404 {
		t.Fatal(response.Code)
	}
}
