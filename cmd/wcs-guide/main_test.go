package main

import (
	"os"
	"path/filepath"
	"testing"
)

func TestStartupArgumentsAndConfiguration(t *testing.T) {
	original := os.Args
	defer func() { os.Args = original }()
	dbPath := filepath.Join(t.TempDir(), "must-not-exist.db")
	t.Setenv("DB_PATH", dbPath)
	t.Setenv("BASE_URL", "")
	for _, command := range []string{"serve", "version", "help"} {
		os.Args = []string{"wcs-guide", command}
		if code := run(); code != 2 {
			t.Fatal(code)
		}
	}
	os.Args = []string{"wcs-guide"}
	if code := run(); code != 1 {
		t.Fatal(code)
	}
	if _, err := os.Stat(dbPath); !os.IsNotExist(err) {
		t.Fatal("БД создана до проверки конфигурации")
	}
}
