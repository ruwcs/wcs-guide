package storage

import (
	"context"
	"database/sql"
	"math"
	"reflect"
	"time"

	"github.com/ruwcs/wcs-guide/internal/domain"
)

// pageIDs возвращает идентификаторы страниц.
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

// getPage читает страницу и её блоки.
func getPage(ctx context.Context, tx *sql.Tx, id int64) (record domain.Page, err error) {
	var path, createdAt, updatedAt, publishedAt sql.NullString
	err = tx.QueryRowContext(ctx, `SELECT id, path, title, description, machine_context, language,
        status, created_at, updated_at, published_at
        FROM pages
        WHERE id = ?`, id).Scan(&record.ID, &path, &record.Title, &record.Description, &record.MachineContext, &record.Language, &record.Status, &createdAt, &updatedAt, &publishedAt)
	if err != nil {
		return
	}
	record.Path = path.String
	record.CreatedAt, err = parsed(createdAt)
	if err != nil {
		return
	}
	record.UpdatedAt, err = parsed(updatedAt)
	if err != nil {
		return
	}
	record.PublishedAt, err = parsed(publishedAt)
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
	for _, blockID := range ids {
		block, err := getBlock(ctx, tx, blockID)
		if err != nil {
			return record, err
		}
		record.Blocks = append(record.Blocks, block)
	}
	return record, record.Validate()
}

// GetPage возвращает страницу.
func (s *Storage) GetPage(ctx context.Context, id int64) (r domain.Page, err error) {
	err = s.withTx(ctx, func(tx *sql.Tx) error { var err error; r, err = getPage(ctx, tx, id); return err })
	return
}

// GetPageByPath возвращает страницу по адресу.
func (s *Storage) GetPageByPath(ctx context.Context, path string) (r domain.Page, err error) {
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

// listPages возвращает страницы из хранилища.
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
			out = append(out, r)
		}
		return nil
	})
	return
}

// ListPages возвращает редакторский список страниц.
func (s *Storage) ListPages(ctx context.Context) ([]domain.Page, error) {
	return s.listPages(ctx, false)
}

// CreatePage создаёт черновик страницы.
func (s *Storage) CreatePage(ctx context.Context, p domain.Page) (r domain.Page, err error) {
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
		r = domain.Page{}
	}
	return
}

// SavePage сохраняет редакторские изменения страницы.
func (s *Storage) SavePage(ctx context.Context, page domain.Page, at time.Time) (record domain.Page, err error) {
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
		if at.IsZero() {
			return invalid("page.at")
		}
		ids := []int64{page.ID}
		before, err := publicState(ctx, tx, ids)
		if err != nil {
			return err
		}
		existing := make(map[int64]domain.Block)
		for _, block := range old.Blocks {
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
			if block.Type == domain.BlockImported {
				if *prev.Import != *block.Import {
					if _, err = tx.ExecContext(ctx, "UPDATE blocks SET import_error='' WHERE id=?", block.ID); err != nil {
						return err
					}
				}
				if prev.Import.SourceID != block.Import.SourceID {
					if _, err = tx.ExecContext(ctx, "UPDATE blocks SET imported_content=NULL,imported_at=NULL WHERE id=?", block.ID); err != nil {
						return err
					}
				}
				if _, err = tx.ExecContext(ctx, `UPDATE blocks
        SET source_id=?, start_anchor=?, include_start=?,
        end_anchor=?, include_end=?
        WHERE id=?`, block.Import.SourceID, block.Import.Rules.Start.Text, block.Import.Rules.Start.Include, block.Import.Rules.End.Text, block.Import.Rules.End.Include, block.ID); err != nil {
					return err
				}
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
		changed := page.Title != old.Title || page.Description != old.Description || page.MachineContext != old.MachineContext || page.Language != old.Language || page.Path != old.Path || len(page.Blocks) != len(old.Blocks)
		if !changed {
			for i, block := range page.Blocks {
				if block.ID != old.Blocks[i].ID || block.Text != old.Blocks[i].Text || !reflect.DeepEqual(block.Import, old.Blocks[i].Import) {
					changed = true
					break
				}
			}
		}
		updated := old.UpdatedAt
		if changed {
			updated = at
		}
		if _, err = tx.ExecContext(ctx, `UPDATE pages
        SET path = ?, title = ?, description = ?, machine_context = ?,
        language = ?, updated_at = ?
        WHERE id = ?`, nullable(page.Path), page.Title, page.Description, page.MachineContext, page.Language, stamp(updated), page.ID); err != nil {
			return err
		}
		if err = refreshPublic(ctx, tx, ids, before, at); err != nil {
			return err
		}
		record, err = getPage(ctx, tx, page.ID)
		return err
	})
	if err != nil {
		record = domain.Page{}
	}
	return
}

// setPublished изменяет состояние публикации страницы.
func (s *Storage) setPublished(ctx context.Context, id int64, at time.Time, publish bool) error {
	return s.withTx(ctx, func(tx *sql.Tx) error {
		record, err := getPage(ctx, tx, id)
		if err != nil {
			return err
		}
		if err = validTime(at); err != nil {
			return err
		}
		if !publish {
			if err = record.Unpublish(at); err != nil {
				return err
			}
			if record.Status == domain.PageDraft {
				_, err = tx.ExecContext(ctx, "UPDATE pages SET status=0,updated_at=? WHERE id=? AND status=1", stamp(record.UpdatedAt), id)
				return err
			}
		}
		ids := []int64{id}
		before, err := publicState(ctx, tx, ids)
		if err != nil {
			return err
		}
		old := record.Status
		err = record.Publish(at)
		if err != nil {
			return err
		}
		if old == record.Status {
			return nil
		}
		if _, err = tx.ExecContext(ctx, "UPDATE pages SET status=?,updated_at=?,published_at=? WHERE id=?", record.Status, stamp(record.UpdatedAt), stamp(record.PublishedAt), id); err != nil {
			return err
		}

		return refreshPublic(ctx, tx, ids, before, at)
	})
}

// PublishPage публикует страницу.
func (s *Storage) PublishPage(ctx context.Context, id int64, at time.Time) error {
	return s.setPublished(ctx, id, at, true)
}

// UnpublishPage снимает страницу с публикации.
func (s *Storage) UnpublishPage(ctx context.Context, id int64, at time.Time) error {
	return s.setPublished(ctx, id, at, false)
}
