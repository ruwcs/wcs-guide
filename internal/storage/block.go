package storage

import (
	"context"
	"database/sql"
	"errors"
	"time"

	"github.com/ruwcs/wcs-guide/internal/domain"
)

// PreparationToken фиксирует условия применения подготовленного результата.
type PreparationToken struct {
	BlockID        int64                 // Блок назначения.
	BlockRevision  int64                 // Ожидаемая редакция блока.
	Settings       domain.ImportSettings // Целевые настройки обработки.
	SourceRevision int64                 // Ожидаемая редакция источника.
	SnapshotID     int64                 // Последний снимок на момент начала обработки.
}

// PreparationInput содержит неизменяемый вход и условия записи результата.
type PreparationInput struct {
	Token    PreparationToken // Условия согласованной записи.
	Snapshot domain.Snapshot  // Оригинал для внешней обработки.
}

func getBlock(ctx context.Context, tx *sql.Tx, id int64) (block domain.Block, rev int64, err error) {
	var sourceID sql.NullInt64
	rules := domain.ExtractionRules{}
	err = tx.QueryRowContext(ctx, `SELECT id, page_id, type, text, source_id, start_anchor,
        include_start, end_anchor, include_end, revision
        FROM blocks
        WHERE id = ?`, id).Scan(&block.ID, &block.PageID, &block.Type, &block.Text, &sourceID, &rules.Start.Text, &rules.Start.Include, &rules.End.Text, &rules.End.Include, &rev)
	if err != nil {
		return
	}
	if sourceID.Valid {
		block.Import = &domain.ImportSettings{SourceID: sourceID.Int64, Rules: rules}
	}
	var prepared domain.PreparedContent
	var preparedAt, publishedAt sql.NullString
	err = tx.QueryRowContext(ctx, `SELECT source_id, snapshot_id, content, start_anchor, include_start
, end_anchor, include_end, prepared_at, published_at
        FROM prepared_contents
        WHERE block_id = ?`, id).Scan(&prepared.SourceID, &prepared.SnapshotID, &prepared.Content, &prepared.Rules.Start.Text, &prepared.Rules.Start.Include, &prepared.Rules.End.Text, &prepared.Rules.End.Include, &preparedAt, &publishedAt)
	if errors.Is(err, sql.ErrNoRows) {
		err = nil
	} else if err == nil {
		prepared.PreparedAt, err = parsed(preparedAt)
		if err == nil {
			prepared.PublishedAt, err = parsed(publishedAt)
		}
		block.Prepared = &prepared
	}
	if err == nil {
		err = block.Validate()
	}
	return
}
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
func savePrepared(ctx context.Context, tx *sql.Tx, bid int64, p domain.PreparedContent) error {
	_, err := tx.ExecContext(ctx, `INSERT INTO prepared_contents (block_id, source_id, snapshot_id,
        content, start_anchor, include_start, end_anchor, include_end,
        prepared_at, published_at)
        VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
        ON CONFLICT (block_id) DO UPDATE
        SET source_id = excluded.source_id, snapshot_id = excluded .
        snapshot_id, content = excluded.content, start_anchor = excluded .
        start_anchor, include_start = excluded.include_start, end_anchor =
        excluded.end_anchor, include_end = excluded.include_end,
        prepared_at = excluded.prepared_at, published_at = excluded .
        published_at`, bid, p.SourceID, p.SnapshotID, p.Content, p.Rules.Start.Text, p.Rules.Start.Include, p.Rules.End.Text, p.Rules.End.Include, stamp(p.PreparedAt), stamp(p.PublishedAt))
	return err
}

// GetPreparation получает токен для текущих либо предлагаемых настроек без их сохранения.
func (s *Storage) GetPreparation(ctx context.Context, id int64, settings domain.ImportSettings) (out PreparationInput, err error) {
	if err = settings.Validate(); err != nil {
		return
	}
	err = s.withTx(ctx, func(tx *sql.Tx) error {
		block, blockRevision, err := getBlock(ctx, tx, id)
		if err != nil {
			return err
		}
		if block.Type != domain.BlockImported {
			return invalid("block.type")
		}
		source, err := getSource(ctx, tx, settings.SourceID)
		if err != nil {
			return err
		}
		snapshotID, err := latestID(ctx, tx, settings.SourceID)
		if err != nil {
			return err
		}
		out.Snapshot, err = getSnapshot(ctx, tx, snapshotID)
		out.Token = PreparationToken{id, blockRevision, settings, source.EditRevision, snapshotID}
		return err
	})
	return
}

// SetImportRules меняет правила, сохраняя прежний успешный результат того же источника.
func (s *Storage) SetImportRules(ctx context.Context, id, revision int64, rules domain.ExtractionRules) error {
	if err := rules.Validate(); err != nil {
		return err
	}
	return s.withTx(ctx, func(tx *sql.Tx) error {
		block, blockRevision, err := getBlock(ctx, tx, id)
		if err != nil {
			return err
		}
		if blockRevision != revision {
			return ErrConflict
		}
		if block.Type != domain.BlockImported {
			return invalid("block.type")
		}
		if _, err = tx.ExecContext(ctx, "UPDATE blocks SET start_anchor=?,include_start=?,end_anchor=?,include_end=?,revision=revision+1 WHERE id=?", rules.Start.Text, rules.Start.Include, rules.End.Text, rules.End.Include, id); err != nil {
			return err
		}
		_, err = tx.ExecContext(ctx, "UPDATE pages SET edit_revision=edit_revision+1 WHERE id=?", block.PageID)
		return err
	})
}
func (s *Storage) apply(ctx context.Context, preparationToken PreparationToken, prepared domain.PreparedContent, at time.Time, replace bool) error {
	if !prepared.PublishedAt.IsZero() {
		return invalid("prepared.published_at")
	}
	if err := prepared.Validate(); err != nil {
		return err
	}
	if err := validTime(prepared.PreparedAt); err != nil {
		return err
	}
	if err := validTime(at); err != nil {
		return err
	}
	if at.IsZero() || at.Before(prepared.PreparedAt) {
		return invalid("prepared.at")
	}
	return s.withTx(ctx, func(tx *sql.Tx) error {
		block, revision, err := getBlock(ctx, tx, preparationToken.BlockID)
		if err != nil {
			return err
		}
		if revision != preparationToken.BlockRevision {
			return ErrConflict
		}
		src, err := getSource(ctx, tx, preparationToken.Settings.SourceID)
		if err != nil {
			return err
		}
		snapshotID, err := latestID(ctx, tx, preparationToken.Settings.SourceID)
		if err != nil {
			return err
		}
		if src.EditRevision != preparationToken.SourceRevision || snapshotID != preparationToken.SnapshotID || prepared.SnapshotID != preparationToken.SnapshotID || prepared.SourceID != preparationToken.Settings.SourceID || prepared.Rules != preparationToken.Settings.Rules {
			return ErrConflict
		}
		snapshot, err := getSnapshot(ctx, tx, snapshotID)
		if err != nil {
			return err
		}
		if snapshot.SourceID != prepared.SourceID {
			return ErrConflict
		}
		ids := []int64{block.PageID}
		before, err := publicState(ctx, tx, ids)
		if err != nil {
			return err
		}
		if replace {
			err = block.ReplaceImport(preparationToken.Settings, prepared)
		} else {
			if block.Import == nil || *block.Import != preparationToken.Settings {
				return ErrConflict
			}
			err = block.ApplyPrepared(prepared)
		}
		if err != nil {
			return err
		}
		page, err := getPage(ctx, tx, block.PageID)
		if err != nil {
			return err
		}
		if page.Page.Status == domain.PagePublished && !src.Source.PublicationBlocked {
			prepared.PublishedAt = at.UTC()
		}
		if replace {
			_, err = tx.ExecContext(ctx, "UPDATE blocks SET source_id=?,start_anchor=?,include_start=?,end_anchor=?,include_end=? WHERE id=?", preparationToken.Settings.SourceID, preparationToken.Settings.Rules.Start.Text, preparationToken.Settings.Rules.Start.Include, preparationToken.Settings.Rules.End.Text, preparationToken.Settings.Rules.End.Include, block.ID)
			if err != nil {
				return err
			}
			if _, err = tx.ExecContext(ctx, "UPDATE pages SET edit_revision=edit_revision+1 WHERE id=?", block.PageID); err != nil {
				return err
			}
		}
		if err = savePrepared(ctx, tx, block.ID, prepared); err != nil {
			return err
		}
		if _, err = tx.ExecContext(ctx, "UPDATE blocks SET revision=revision+1 WHERE id=?", block.ID); err != nil {
			return err
		}
		return refreshPublic(ctx, tx, ids, before, at, false)
	})
}

// ApplyPrepared применяет результат лишь при актуальном токене и действующих настройках.
func (s *Storage) ApplyPrepared(ctx context.Context, t PreparationToken, p domain.PreparedContent, at time.Time) error {
	return s.apply(ctx, t, p, at, false)
}

// ReplaceImport согласованно сохраняет предложенные настройки и проверенный результат.
func (s *Storage) ReplaceImport(ctx context.Context, t PreparationToken, p domain.PreparedContent, at time.Time) error {
	return s.apply(ctx, t, p, at, true)
}
