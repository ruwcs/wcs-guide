package storage

import (
	"context"
	"database/sql"
	"reflect"
	"time"

	"github.com/ruwcs/wcs-guide/internal/domain"
)

// PublicPage — публичное представление страницы.
type PublicPage struct {
	ID             int64         // Идентификатор страницы.
	Path           string        // Постоянный адрес.
	Title          string        // Заголовок материала.
	Description    string        // Краткое текстовое описание для метаданных и каталогов.
	MachineContext string        // Дополнительный публичный контекст для машинных потребителей.
	Language       string        // Основной язык.
	UpdatedAt      time.Time     // Дата фактического публичного изменения.
	Blocks         []PublicBlock // Доступные блоки в порядке страницы.
}

// PublicBlock — блок публичной страницы.
type PublicBlock struct {
	Content     string // Содержимое блока в Markdown.
	SourceID    int64  // Идентификатор источника; ноль для ручного блока.
	SourceName  string // Публичное название источника.
	ExternalRef string // Адрес оригинала.
}

// publicPage читает публичное содержимое страницы.
func publicPage(ctx context.Context, tx *sql.Tx, id int64) (out PublicPage, err error) {
	record, err := getPage(ctx, tx, id)
	if err != nil {
		return
	}
	page := record
	if page.Status != domain.PagePublished {
		return out, ErrNotFound
	}
	out = PublicPage{ID: page.ID, Path: page.Path, Title: page.Title, Description: page.Description, MachineContext: page.MachineContext, Language: page.Language}
	var updatedAt sql.NullString
	if err = tx.QueryRowContext(ctx, "SELECT public_updated_at FROM pages WHERE id=?", id).Scan(&updatedAt); err != nil {
		return
	}
	out.UpdatedAt, err = parsed(updatedAt)
	if err != nil {
		return
	}
	for _, block := range page.Blocks {
		if block.Type == domain.BlockText {
			out.Blocks = append(out.Blocks, PublicBlock{Content: block.Text})
			continue
		}
		if block.Prepared == nil {
			continue
		}
		source, err := getSource(ctx, tx, block.Import.SourceID)
		if err != nil {
			return out, err
		}
		if source.PublicationBlocked {
			continue
		}
		out.Blocks = append(out.Blocks, PublicBlock{Content: block.Prepared.Content, SourceID: source.ID, SourceName: source.Name, ExternalRef: source.ExternalRef})
	}
	return
}

// publicState читает публичное состояние указанных страниц.
func publicState(ctx context.Context, tx *sql.Tx, ids []int64) (map[int64]PublicPage, error) {
	out := make(map[int64]PublicPage)
	for _, id := range ids {
		var published bool
		if err := tx.QueryRowContext(ctx, "SELECT status=1 FROM pages WHERE id=?", id).Scan(&published); err != nil {
			return nil, err
		}
		if !published {
			continue
		}
		page, err := publicPage(ctx, tx, id)
		if err != nil {
			return nil, err
		}
		out[id] = page
	}
	return out, nil
}

// sourcePageIDs возвращает идентификаторы страниц источника.
func sourcePageIDs(ctx context.Context, tx *sql.Tx, sourceID int64) ([]int64, error) {
	rows, err := tx.QueryContext(ctx, "SELECT DISTINCT page_id FROM blocks WHERE source_id=? ORDER BY page_id", sourceID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var ids []int64
	for rows.Next() {
		var id int64
		if err := rows.Scan(&id); err != nil {
			return nil, err
		}
		ids = append(ids, id)
	}
	return ids, rows.Err()
}

// refreshPublic обновляет даты изменения публичного содержимого.
func refreshPublic(ctx context.Context, tx *sql.Tx, ids []int64, before map[int64]PublicPage, at time.Time) error {
	after, err := publicState(ctx, tx, ids)
	if err != nil {
		return err
	}
	for id, next := range after {
		old, exists := before[id]
		old.UpdatedAt = time.Time{}
		next.UpdatedAt = time.Time{}
		if exists && reflect.DeepEqual(old, next) {
			continue
		}
		if err = validTime(at); err != nil {
			return err
		}
		if at.IsZero() {
			return invalid("public.at")
		}
		if _, err = tx.ExecContext(ctx, "UPDATE pages SET public_updated_at=? WHERE id=?", stamp(at), id); err != nil {
			return err
		}
	}
	return nil
}

// GetPublicPage возвращает публичное представление страницы.
func (s *Storage) GetPublicPage(ctx context.Context, path string) (out PublicPage, err error) {
	err = s.withTx(ctx, func(tx *sql.Tx) error {
		var id int64
		err := tx.QueryRowContext(ctx, "SELECT id FROM pages WHERE path=? AND status=1", path).Scan(&id)
		if err != nil {
			return err
		}
		out, err = publicPage(ctx, tx, id)
		return err
	})
	if err != nil {
		out = PublicPage{}
	}
	return
}

// PublicPageSummary — краткое публичное представление страницы.
type PublicPageSummary struct {
	ID          int64     // Идентификатор страницы.
	Path        string    // Постоянный адрес.
	Title       string    // Заголовок материала.
	Description string    // Краткое описание.
	Language    string    // Основной язык.
	UpdatedAt   time.Time // Дата изменения публичного материала.
}

// ListPublicPages возвращает список опубликованных страниц.
func (s *Storage) ListPublicPages(ctx context.Context) (out []PublicPageSummary, err error) {
	err = s.withTx(ctx, func(tx *sql.Tx) error {
		rows, err := tx.QueryContext(ctx, `SELECT id, path, title, description, language, public_updated_at
        FROM pages
        WHERE status = 1
        ORDER BY id`)
		if err != nil {
			return err
		}
		defer rows.Close()
		for rows.Next() {
			var page PublicPageSummary
			var updated sql.NullString
			if err := rows.Scan(&page.ID, &page.Path, &page.Title, &page.Description, &page.Language, &updated); err != nil {
				return err
			}
			page.UpdatedAt, err = parsed(updated)
			if err != nil {
				return err
			}
			out = append(out, page)
		}
		return rows.Err()
	})
	if err != nil {
		out = nil
	}
	return
}
