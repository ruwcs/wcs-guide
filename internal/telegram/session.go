package telegram

import (
	"context"
	"os"

	"github.com/gotd/td/session"
)

// CheckSession проверяет файл сессии Telegram.
func CheckSession(ctx context.Context, path string) error {
	file, err := os.OpenFile(path, os.O_RDWR, 0600)
	if os.IsNotExist(err) {
		file, err = os.OpenFile(path, os.O_CREATE|os.O_EXCL|os.O_RDWR, 0600)
		if err != nil {
			return &Error{Code: "session_access"}
		}
		if err = file.Close(); err != nil {
			return &Error{Code: "session_access"}
		}
		if err = os.Remove(path); err != nil {
			return &Error{Code: "session_access"}
		}
		return nil
	}
	if err != nil {
		return &Error{Code: "session_access"}
	}
	info, err := file.Stat()
	file.Close()
	if err != nil || !info.Mode().IsRegular() || info.Size() == 0 {
		return &Error{Code: "session_access"}
	}
	loader := session.Loader{Storage: &session.FileStorage{Path: path}}
	if _, err := loader.Load(ctx); err != nil {
		return &Error{Code: "session_invalid"}
	}
	return nil
}
