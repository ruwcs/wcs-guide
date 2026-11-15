package main

import (
	"bytes"
	"context"
	"net/url"
	"path/filepath"
	"strings"
	"testing"

	"github.com/ruwcs/wcs-guide/internal/config"
	"github.com/ruwcs/wcs-guide/internal/storage"
)

// TestAdminCommands проверяет создание и восстановление администратора.
func TestAdminCommands(t *testing.T) {
	origin, _ := url.Parse("http://localhost:8080")
	cfg := config.Config{DBPath: filepath.Join(t.TempDir(), "admin.db"), EditorBaseURL: *origin}
	ctx := context.Background()
	var out bytes.Buffer
	if err := adminCommand(ctx, cfg, []string{"admin", "bootstrap", "owner", "Владелец"}, &out); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out.String(), "http://localhost:8080/register") {
		t.Fatal("нет адреса регистрации")
	}
	out.Reset()
	if err := adminCommand(ctx, cfg, []string{"admin", "bootstrap", "other"}, &out); err == nil {
		t.Fatal("повторный bootstrap")
	}
	if out.Len() != 0 {
		t.Fatal("выдан код после ошибки")
	}
	if err := adminCommand(ctx, cfg, []string{"admin", "recover", "owner"}, &out); err != nil {
		t.Fatal(err)
	}
	store, err := storage.Open(ctx, cfg.DBPath)
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	u, err := store.GetUserByLogin(ctx, "owner")
	if err != nil || !u.Active {
		t.Fatal(u, err)
	}
	if err := adminCommand(ctx, cfg, []string{"admin", "recover", "unknown"}, &out); err == nil {
		t.Fatal("создан неизвестный пользователь")
	}
}
