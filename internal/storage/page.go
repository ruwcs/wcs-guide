package storage

import (
	"context"
	"database/sql"
	"math"
	"time"

	"github.com/ruwcs/wcs-guide/internal/domain"
)

// PageRecord содержит страницу и условия редакторского сохранения.
type PageRecord struct {
	Page           domain.Page     // Страница с упорядоченными блоками.
	EditRevision   int64           // Редакция редакторских изменений.
	BlockRevisions map[int64]int64 // Редакции настроек и результатов блоков.
}

func pageIDs(ctx context.Context, tx *sql.Tx, published bool) (ids []int64, err error) {
	q := "SELECT id FROM pages"
	if published {
		q += " WHERE status=1"
	}
	q += " ORDER BY id"
	rows, err := tx.QueryContext(ctx, q)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	for rows.Next() {
		var id int64
		if err = rows.Scan(&id); err != nil {
			return nil, err
		}
		ids = append(ids, id)
	}
	return ids, rows.Err()
}
func getPage(ctx context.Context, tx *sql.Tx, id int64) (record PageRecord, err error) {
	var path, createdAt, updatedAt, publishedAt sql.NullString
	err = tx.QueryRowContext(ctx, `SELECT id, path, title, description, machine_context, language,
        status, created_at, updated_at, published_at, edit_revision
        FROM pages
        WHERE id = ?`, id).Scan(&record.Page.ID, &path, &record.Page.Title, &record.Page.Description, &record.Page.MachineContext, &record.Page.Language, &record.Page.Status, &createdAt, &updatedAt, &publishedAt, &record.EditRevision)
	if err != nil {
		return
	}
	record.Page.Path = path.String
	record.Page.CreatedAt, err = parsed(createdAt)
	if err != nil {
		return
	}
	record.Page.UpdatedAt, err = parsed(updatedAt)
	if err != nil {
		return
	}
	record.Page.PublishedAt, err = parsed(publishedAt)
	if err != nil {
		return
	}
	rows, err := tx.QueryContext(ctx, "SELECT id FROM blocks WHERE page_id=? ORDER BY position", id)
	if err != nil {
		return record, err
	}
	var ids []int64
	for rows.Next() {
		var blockID int64
		if err = rows.Scan(&blockID); err != nil {
			rows.Close()
			return record, err
		}
		ids = append(ids, blockID)
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		return record, err
	}
	record.BlockRevisions = make(map[int64]int64)
	for _, blockID := range ids {
		block, revision, err := getBlock(ctx, tx, blockID)
		if err != nil {
			return record, err
		}
		record.Page.Blocks = append(record.Page.Blocks, block)
		record.BlockRevisions[blockID] = revision
	}
	return record, record.Page.Validate()
}

// GetPage возвращает страницу и редакции одним согласованным чтением.
func (s *Storage) GetPage(ctx context.Context, id int64) (r PageRecord, err error) {
	err = s.withTx(ctx, func(tx *sql.Tx) error { var err error; r, err = getPage(ctx, tx, id); return err })
	return
}

// GetPageByPath ищет редакторскую страницу по точному каноническому адресу.
func (s *Storage) GetPageByPath(ctx context.Context, path string) (r PageRecord, err error) {
	err = s.withTx(ctx, func(tx *sql.Tx) error {
		var id int64
		err := tx.QueryRowContext(ctx, "SELECT id FROM pages WHERE path=?", path).Scan(&id)
		if err != nil {
			return err
		}
		r, err = getPage(ctx, tx, id)
		return err
	})
	return
}
func (s *Storage) listPages(ctx context.Context, published bool) (out []domain.Page, err error) {
	err = s.withTx(ctx, func(tx *sql.Tx) error {
		ids, err := pageIDs(ctx, tx, published)
		if err != nil {
			return err
		}
		for _, id := range ids {
			r, err := getPage(ctx, tx, id)
			if err != nil {
				return err
			}
			out = append(out, r.Page)
		}
		return nil
	})
	return
}

// ListPages возвращает редакторский список страниц.
func (s *Storage) ListPages(ctx context.Context) ([]domain.Page, error) {
	return s.listPages(ctx, false)
}

// CreatePage создаёт черновик и назначает ID странице и новым блокам.
func (s *Storage) CreatePage(ctx context.Context, p domain.Page) (r PageRecord, err error) {
	if p.ID != 0 || p.Status != domain.PageDraft || !p.PublishedAt.IsZero() {
		return r, invalid("page.identity")
	}
	if err = p.Validate(); err != nil {
		return
	}
	if err = validTime(p.CreatedAt); err != nil {
		return
	}
	if err = validTime(p.UpdatedAt); err != nil {
		return
	}
	p.Blocks = append([]domain.Block(nil), p.Blocks...)
	err = s.withTx(ctx, func(tx *sql.Tx) error {
		res, err := tx.ExecContext(ctx, "INSERT INTO pages(path,title,description,machine_context,language,created_at,updated_at) VALUES(?,?,?,?,?,?,?)", nullable(p.Path), p.Title, p.Description, p.MachineContext, p.Language, stamp(p.CreatedAt), stamp(p.UpdatedAt))
		if err != nil {
			return err
		}
		id, err := res.LastInsertId()
		if err != nil {
			return err
		}
		for i, b := range p.Blocks {
			if b.ID != 0 || b.Prepared != nil {
				return invalid("block.identity")
			}
			b.PageID = id
			if _, err = insertBlock(ctx, tx, b, i); err != nil {
				return err
			}
		}
		r, err = getPage(ctx, tx, id)
		return err
	})
	if err != nil {
		r = PageRecord{}
	}
	return
}

// SavePage сохраняет композицию, не перезаписывая результаты фоновой обработки.
func (s *Storage) SavePage(ctx context.Context, page domain.Page, revision int64, at time.Time) (record PageRecord, err error) {
	if err = page.Validate(); err != nil {
		return
	}
	if err = validTime(at); err != nil {
		return
	}
	err = s.withTx(ctx, func(tx *sql.Tx) error {
		old, err := getPage(ctx, tx, page.ID)
		if err != nil {
			return err
		}
		if old.EditRevision != revision || page.Status != old.Page.Status || !page.CreatedAt.Equal(old.Page.CreatedAt) || !page.PublishedAt.Equal(old.Page.PublishedAt) {
			return ErrConflict
		}
		if at.IsZero() || at.Before(old.Page.UpdatedAt) {
			return invalid("page.at")
		}
		ids := []int64{page.ID}
		before, err := publicState(ctx, tx, ids)
		if err != nil {
			return err
		}
		existing := make(map[int64]domain.Block)
		for _, block := range old.Page.Blocks {
			existing[block.ID] = block
		}
		var maxPosition sql.NullInt64
		if err = tx.QueryRowContext(ctx, "SELECT max(position) FROM blocks WHERE page_id=?", page.ID).Scan(&maxPosition); err != nil {
			return err
		}
		shift := maxPosition.Int64 + int64(len(page.Blocks)) + 1
		if shift < 1 || maxPosition.Int64 > math.MaxInt64-shift {
			return invalid("block.position")
		}
		if _, err = tx.ExecContext(ctx, "UPDATE blocks SET position=position+? WHERE page_id=?", shift, page.ID); err != nil {
			return err
		}
		for i, block := range page.Blocks {
			if block.ID == 0 {
				if block.Prepared != nil {
					return invalid("block.prepared")
				}
				if _, err = insertBlock(ctx, tx, block, i); err != nil {
					return err
				}
				continue
			}
			prev, ok := existing[block.ID]
			if !ok || prev.Type != block.Type {
				return ErrConflict
			}
			if block.Type == domain.BlockImported && *prev.Import != *block.Import {
				return ErrConflict
			}
			if _, err = tx.ExecContext(ctx, "UPDATE blocks SET position=?,text=? WHERE id=?", i, block.Text, block.ID); err != nil {
				return err
			}
			delete(existing, block.ID)
		}
		for id := range existing {
			if _, err = tx.ExecContext(ctx, "DELETE FROM blocks WHERE id=?", id); err != nil {
				return err
			}
		}
		// Изменение только настроек обрабатывается отдельными методами.
		changed := page.Title != old.Page.Title || page.Description != old.Page.Description || page.MachineContext != old.Page.MachineContext || page.Language != old.Page.Language || page.Path != old.Page.Path || len(page.Blocks) != len(old.Page.Blocks)
		if !changed {
			for i, block := range page.Blocks {
				if block.ID != old.Page.Blocks[i].ID || block.Text != old.Page.Blocks[i].Text {
					changed = true
					break
				}
			}
		}
		updated := old.Page.UpdatedAt
		if changed {
			updated = at
		}
		if _, err = tx.ExecContext(ctx, `UPDATE pages
        SET path = ?, title = ?, description = ?, machine_context = ?,
        language = ?, updated_at = ?, edit_revision = edit_revision + 1
        WHERE id = ?`, nullable(page.Path), page.Title, page.Description, page.MachineContext, page.Language, stamp(updated), page.ID); err != nil {
			return err
		}
		if err = refreshPublic(ctx, tx, ids, before, at, false); err != nil {
			return err
		}
		record, err = getPage(ctx, tx, page.ID)
		return err
	})
	if err != nil {
		record = PageRecord{}
	}
	return
}
func (s *Storage) setPublished(ctx context.Context, id, revision int64, at time.Time, publish bool) error {
	return s.withTx(ctx, func(tx *sql.Tx) error {
		record, err := getPage(ctx, tx, id)
		if err != nil {
			return err
		}
		if record.EditRevision != revision {
			return ErrConflict
		}
		if err = validTime(at); err != nil {
			return err
		}
		if !publish {
			if at.Before(record.Page.UpdatedAt) && !at.IsZero() {
				at = record.Page.UpdatedAt
			}
			if err = record.Page.Unpublish(at); err != nil {
				return err
			}
			if record.Page.Status == domain.PageDraft {
				_, err = tx.ExecContext(ctx, "UPDATE pages SET status=0,updated_at=?,edit_revision=edit_revision+1 WHERE id=? AND status=1", stamp(record.Page.UpdatedAt), id)
				return err
			}
		}
		ids := []int64{id}
		before, err := publicState(ctx, tx, ids)
		if err != nil {
			return err
		}
		old := record.Page.Status
		err = record.Page.Publish(at)
		if err != nil {
			return err
		}
		if old == record.Page.Status {
			return nil
		}
		if _, err = tx.ExecContext(ctx, "UPDATE pages SET status=?,updated_at=?,published_at=?,edit_revision=edit_revision+1 WHERE id=?", record.Page.Status, stamp(record.Page.UpdatedAt), stamp(record.Page.PublishedAt), id); err != nil {
			return err
		}
		if publish {
			if err = publishReadyForPage(ctx, tx, id, at); err != nil {
				return err
			}
		}
		return refreshPublic(ctx, tx, ids, before, at, false)
	})
}

// PublishPage явно публикует текущую страницу.
func (s *Storage) PublishPage(ctx context.Context, id, revision int64, at time.Time) error {
	return s.setPublished(ctx, id, revision, at, true)
}

// UnpublishPage снимает страницу, не удаляя её данные.
func (s *Storage) UnpublishPage(ctx context.Context, id, revision int64, at time.Time) error {
	return s.setPublished(ctx, id, revision, at, false)
}
