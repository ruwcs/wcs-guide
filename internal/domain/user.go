package domain

import (
	"log/slog"
	"regexp"
	"time"
	"unicode/utf8"
)

// Role — роль пользователя.
type Role string

const (
	// RoleAdmin — администратор сайта.
	RoleAdmin Role = "admin"
	// RoleEditor — редактор материалов.
	RoleEditor Role = "editor"
)

// loginPattern — формат логина пользователя.
var loginPattern = regexp.MustCompile(`^[a-z0-9][a-z0-9._-]{0,63}$`)

// User — пользователь редакторского интерфейса.
type User struct {
	ID          int64     // Идентификатор пользователя.
	Login       string    // Уникальный логин.
	DisplayName string    // Отображаемое имя.
	WebAuthnID  []byte    // Постоянный идентификатор пользователя WebAuthn.
	Role        Role      // Роль пользователя.
	Active      bool      // Разрешение доступа.
	CreatedAt   time.Time // Время создания пользователя.
}

// Validate проверяет пользователя.
func (u User) Validate() error {
	if u.ID < 0 || !loginPattern.MatchString(u.Login) || blank(u.DisplayName) || !utf8.ValidString(u.DisplayName) || len(u.DisplayName) > 256 || len(u.WebAuthnID) != 32 || u.CreatedAt.IsZero() || (u.Role != RoleAdmin && u.Role != RoleEditor) {
		return invalid("User")
	}
	return nil
}

// LogValue реализует интерфейс slog.LogValuer для User.
func (u User) LogValue() slog.Value {
	return slog.GroupValue(slog.Int64("id", u.ID), slog.String("role", string(u.Role)), slog.Bool("active", u.Active))
}

// Соответствие интерфейсу slog.LogValuer.
var _ slog.LogValuer = User{}
