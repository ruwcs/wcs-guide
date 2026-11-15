package importer

import (
	"context"

	"github.com/ruwcs/wcs-guide/internal/domain"
	"github.com/ruwcs/wcs-guide/internal/telegram"
)

// Fetcher получает публикации Telegram.
type Fetcher interface {
	// Fetch получает публикацию источника.
	Fetch(context.Context, domain.Source) (telegram.Publication, error)
}
