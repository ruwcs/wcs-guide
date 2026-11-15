package storage

import (
	"context"
	"database/sql"
	"time"

	"github.com/ruwcs/wcs-guide/internal/domain"
)

// getBlock читает блок.
func getBlock(ctx context.Context, tx *sql.Tx, id int64) (block domain.Block, err error) {
	var sourceID sql.NullInt64
	rules := domain.ExtractionRules{}
	err = tx.QueryRowContext(ctx, `SELECT id, page_id, type, text, source_id, start_anchor,
        include_start, end_anchor, include_end
        FROM blocks
        WHERE id = ?`, id).Scan(&block.ID, &block.PageID, &block.Type, &block.Text, &sourceID, &rules.Start.Text, &rules.Start.Include, &rules.End.Text, &rules.End.Include)
	if err != nil {
		return
	}
	if sourceID.Valid {
		block.Import = &domain.ImportSettings{SourceID: sourceID.Int64, Rules: rules}
	}
	var content, importedAt sql.NullString
	err = tx.QueryRowContext(ctx, "SELECT imported_content,imported_at,import_error FROM blocks WHERE id=?", id).Scan(&content, &importedAt, &block.ImportError)
	if err == nil && content.Valid {
		at, parseErr := parsed(importedAt)
		if parseErr != nil {
			return block, parseErr
		}
		block.Prepared = &domain.PreparedContent{Content: content.String, PreparedAt: at}
	}
	if err == nil {
		err = block.Validate()
	}
	return
}

// insertBlock сохраняет новый блок страницы.
func insertBlock(ctx context.Context, tx *sql.Tx, b domain.Block, position int) (int64, error) {
	if err := b.Validate(); err != nil {
		return 0, err
	}
	var sid any
	var r domain.ExtractionRules
	if b.Import != nil {
		sid = b.Import.SourceID
		r = b.Import.Rules
	}
	res, err := tx.ExecContext(ctx, `INSERT INTO blocks (page_id, position, type, text, source_id,
        start_anchor, include_start, end_anchor, include_end)
        VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?)`, b.PageID, position, b.Type, b.Text, sid, r.Start.Text, r.Start.Include, r.End.Text, r.End.Include)
	if err != nil {
		return 0, err
	}
	return res.LastInsertId()
}

// SaveImportedContent сохраняет импортированное содержимое блока.
func (s *Storage) SaveImportedContent(ctx context.Context, blockID int64, expected domain.Source, settings domain.ImportSettings, content string, at time.Time) (changed bool, err error) {
	prepared := domain.PreparedContent{Content: content, PreparedAt: at}
	if err := prepared.Validate(); err != nil {
		return false, err
	}
	if err := validTime(at); err != nil {
		return false, err
	}
	err = s.withTx(ctx, func(tx *sql.Tx) error {
		block, err := checkBlock(ctx, tx, blockID, expected, settings)
		if err != nil {
			return err
		}
		changed = block.Prepared == nil || block.Prepared.Content != content
		ids := []int64{block.PageID}
		before, err := publicState(ctx, tx, ids)
		if err != nil {
			return err
		}
		if _, err = tx.ExecContext(ctx, "UPDATE blocks SET imported_content=?,imported_at=?,import_error='' WHERE id=?", content, stamp(at), blockID); err != nil {
			return err
		}
		return refreshPublic(ctx, tx, ids, before, at)
	})
	if err != nil {
		changed = false
	}
	return
}

// checkBlock проверяет назначение и настройки импорта блока.
func checkBlock(ctx context.Context, tx *sql.Tx, id int64, expected domain.Source, settings domain.ImportSettings) (domain.Block, error) {
	source, err := checkSource(ctx, tx, expected)
	if err != nil {
		return domain.Block{}, err
	}
	block, err := getBlock(ctx, tx, id)
	if err != nil {
		return block, err
	}
	if source.PublicationBlocked || block.Import == nil || *block.Import != settings || settings.SourceID != expected.ID {
		return block, ErrConflict
	}
	return block, nil
}

// SaveBlockImportError сохраняет ошибку подготовки блока.
func (s *Storage) SaveBlockImportError(ctx context.Context, id int64, expected domain.Source, settings domain.ImportSettings, message string) error {
	return s.withTx(ctx, func(tx *sql.Tx) error {
		if _, err := checkBlock(ctx, tx, id, expected, settings); err != nil {
			return err
		}
		_, err := tx.ExecContext(ctx, "UPDATE blocks SET import_error=? WHERE id=?", message, id)
		return err
	})
}
