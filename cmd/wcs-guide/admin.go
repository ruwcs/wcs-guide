package main

import (
	"context"
	"fmt"
	"io"
	"strings"

	"github.com/ruwcs/wcs-guide/internal/auth"
	"github.com/ruwcs/wcs-guide/internal/config"
	"github.com/ruwcs/wcs-guide/internal/storage"
)

// adminCommand выполняет административную команду.
func adminCommand(ctx context.Context, cfg config.Config, args []string, out io.Writer) error {
	if len(args) < 3 || args[0] != "admin" || (args[1] != "bootstrap" && args[1] != "recover") || len(args) > 4 || (args[1] == "recover" && len(args) != 3) {
		return fmt.Errorf("использование: admin bootstrap LOGIN [DISPLAY_NAME] | admin recover LOGIN")
	}
	store, err := storage.Open(ctx, cfg.DBPath)
	if err != nil {
		return err
	}
	defer store.Close()
	var id int64
	var code string
	if args[1] == "bootstrap" {
		name := strings.ToLower(args[2])
		if len(args) == 4 {
			name = args[3]
		}
		id, code, err = auth.Bootstrap(ctx, store, args[2], name)
	} else {
		id, code, err = auth.Recover(ctx, store, args[2])
	}
	if err != nil {
		return err
	}
	_, err = fmt.Fprintf(out, "Пользователь: %d\nРегистрация: %s/register\nОдноразовый код (30 минут): %s\n", id, cfg.EditorBaseURL.String(), code)
	return err
}
