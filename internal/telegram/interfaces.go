package telegram

import (
	"context"

	"github.com/gotd/td/telegram/auth"
	"github.com/gotd/td/tg"
)

// botAuthorizer — авторизация бота Telegram.
type botAuthorizer interface {
	// Status возвращает состояние авторизации.
	Status(context.Context) (*auth.Status, error)
	// Bot авторизует бота.
	Bot(context.Context, string) (*tg.AuthAuthorization, error)
}

// richTextBlock — текстовый блок RichMessage.
type richTextBlock interface {
	// GetText возвращает форматированный текст блока.
	GetText() tg.RichTextClass
}
