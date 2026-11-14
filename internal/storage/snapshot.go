package storage

import (
	"context"
	"database/sql"
	"errors"
	"time"

	"github.com/ruwcs/wcs-guide/internal/domain"
)

func getSnapshot(ctx context.Context, tx *sql.Tx, id int64) (snapshot domain.Snapshot, err error) {
	var metadata, fetchedAt, sourceModifiedAt sql.NullString
	err = tx.QueryRowContext(ctx, `SELECT id, source_id, raw_content, format, checksum, metadata,
        fetched_at, source_modified_at
        FROM snapshots
        WHERE id = ?`, id).Scan(&snapshot.ID, &snapshot.SourceID, &snapshot.RawContent, &snapshot.Format, &snapshot.Checksum, &metadata, &fetchedAt, &sourceModifiedAt)
	if err != nil {
		return
	}
	snapshot.Metadata, err = parseMap(metadata)
	if err != nil {
		return
	}
	snapshot.FetchedAt, err = parsed(fetchedAt)
	if err != nil {
		return
	}
	snapshot.SourceModifiedAt, err = parsed(sourceModifiedAt)
	if err == nil {
		err = snapshot.Validate()
	}
	return
}
func latestID(ctx context.Context, tx *sql.Tx, sourceID int64) (id int64, err error) {
	err = tx.QueryRowContext(ctx, "SELECT id FROM snapshots WHERE source_id=? ORDER BY id DESC LIMIT 1", sourceID).Scan(&id)
	return
}

// GetSnapshot читает неизменяемую версию оригинала.
func (s *Storage) GetSnapshot(ctx context.Context, id int64) (v domain.Snapshot, err error) {
	err = s.withTx(ctx, func(tx *sql.Tx) error { var err error; v, err = getSnapshot(ctx, tx, id); return err })
	return
}

// GetLatestSnapshot возвращает последний принятый снимок источника.
func (s *Storage) GetLatestSnapshot(ctx context.Context, id int64) (v domain.Snapshot, err error) {
	err = s.withTx(ctx, func(tx *sql.Tx) error {
		n, err := latestID(ctx, tx, id)
		if err != nil {
			return err
		}
		v, err = getSnapshot(ctx, tx, n)
		return err
	})
	return
}

// SaveFetch согласованно сохраняет изменившийся оригинал и успешную проверку.
// Булевый результат сообщает, создана ли новая версия.
func (s *Storage) SaveFetch(ctx context.Context, fetchToken FetchToken, snapshot domain.Snapshot, checkedAt time.Time) (out domain.Snapshot, created bool, err error) {
	if snapshot.ID != 0 || snapshot.SourceID != fetchToken.SourceID {
		return out, false, invalid("snapshot.identity")
	}
	if err = snapshot.Validate(); err != nil {
		return
	}
	for _, at := range []time.Time{snapshot.FetchedAt, snapshot.SourceModifiedAt, checkedAt} {
		if err = validTime(at); err != nil {
			return
		}
	}
	if checkedAt.Before(snapshot.FetchedAt) {
		return out, false, invalid("snapshot.time")
	}
	err = s.withTx(ctx, func(tx *sql.Tx) error {
		sourceRecord, err := checkFetch(ctx, tx, fetchToken)
		if err != nil {
			return err
		}
		if err = sourceRecord.Source.Sync.RecordSuccess(checkedAt); err != nil {
			return err
		}
		id, err := latestID(ctx, tx, fetchToken.SourceID)
		if err != nil && !errors.Is(err, sql.ErrNoRows) {
			return err
		}
		if err == nil {
			old, err := getSnapshot(ctx, tx, id)
			if err != nil {
				return err
			}
			if old.Checksum == snapshot.Checksum {
				out = old
			}
		}
		if out.ID == 0 {
			metadata, err := jsonMap(snapshot.Metadata)
			if err != nil {
				return err
			}
			res, err := tx.ExecContext(ctx, `INSERT INTO snapshots (source_id, raw_content, format, checksum,
        metadata, fetched_at, source_modified_at)
        VALUES (?, ?, ?, ?, ?, ?, ?)`, snapshot.SourceID, snapshot.RawContent, snapshot.Format, snapshot.Checksum, metadata, stamp(snapshot.FetchedAt), stamp(snapshot.SourceModifiedAt))
			if err != nil {
				return err
			}
			id, err = res.LastInsertId()
			if err != nil {
				return err
			}
			out, err = getSnapshot(ctx, tx, id)
			if err != nil {
				return err
			}
			created = true
		}
		_, err = tx.ExecContext(ctx, "UPDATE sources SET last_attempt_at=?,last_success_at=?,last_error='',sync_revision=sync_revision+1 WHERE id=?", stamp(checkedAt), stamp(checkedAt), fetchToken.SourceID)
		return err
	})
	if err != nil {
		out = domain.Snapshot{}
		created = false
	}
	return
}
