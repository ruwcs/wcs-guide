package storage

import (
	"context"
	"database/sql"
	"reflect"
	"time"

	"github.com/ruwcs/wcs-guide/internal/domain"
)

// sourceColumns — столбцы источника для чтения из БД.
const sourceColumns = "id,name,type,external_ref,fetch_params,update_interval_ns,auto_refresh,publication_blocked,last_attempt_at,last_success_at,last_error"

// getSource читает источник.
func getSource(ctx context.Context, tx *sql.Tx, id int64) (domain.Source, error) {
	var record domain.Source
	var params, lastAttemptAt, lastSuccessAt sql.NullString
	err := tx.QueryRowContext(ctx, "SELECT "+sourceColumns+" FROM sources WHERE id=?", id).Scan(&record.ID, &record.Name, &record.Type, &record.ExternalRef, &params, &record.UpdateInterval, &record.AutoRefresh, &record.PublicationBlocked, &lastAttemptAt, &lastSuccessAt, &record.Sync.LastError)
	if err != nil {
		return record, err
	}
	record.FetchParams, err = parseMap(params)
	if err != nil {
		return record, &internalError{err}
	}
	record.Sync.LastAttemptAt, err = parsed(lastAttemptAt)
	if err != nil {
		return record, &internalError{err}
	}
	record.Sync.LastSuccessAt, err = parsed(lastSuccessAt)
	if err != nil {
		return record, &internalError{err}
	}
	return record, record.Validate()
}

// CreateSource создаёт источник.
func (s *Storage) CreateSource(ctx context.Context, source domain.Source) (record domain.Source, err error) {
	if source.ID != 0 {
		return record, invalid("source.id")
	}
	if err = source.Validate(); err != nil {
		return
	}
	if source.Sync != (domain.SyncState{}) {
		return record, invalid("source.sync")
	}
	params, err := jsonMap(source.FetchParams)
	if err != nil {
		return record, err
	}
	err = s.withTx(ctx, func(tx *sql.Tx) error {
		res, err := tx.ExecContext(ctx, `INSERT INTO sources (name, type, external_ref, fetch_params,
        update_interval_ns, auto_refresh, publication_blocked)
        VALUES (?, ?, ?, ?, ?, ?, ?)`, source.Name, source.Type, source.ExternalRef, params, source.UpdateInterval, source.AutoRefresh, source.PublicationBlocked)
		if err != nil {
			return err
		}
		id, err := res.LastInsertId()
		if err != nil {
			return err
		}
		record, err = getSource(ctx, tx, id)
		return err
	})
	if err != nil {
		record = domain.Source{}
	}
	return
}

// GetSource читает источник.
func (s *Storage) GetSource(ctx context.Context, id int64) (r domain.Source, err error) {
	err = s.withTx(ctx, func(tx *sql.Tx) error { var err error; r, err = getSource(ctx, tx, id); return err })
	return
}

// sourceIDs возвращает идентификаторы источников.
func sourceIDs(ctx context.Context, tx *sql.Tx) ([]int64, error) {
	rows, err := tx.QueryContext(ctx, "SELECT id FROM sources ORDER BY id")
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var ids []int64
	for rows.Next() {
		var id int64
		if err = rows.Scan(&id); err != nil {
			return nil, err
		}
		ids = append(ids, id)
	}
	return ids, rows.Err()
}

// ListSources возвращает источники в порядке ID.
func (s *Storage) ListSources(ctx context.Context) (out []domain.Source, err error) {
	err = s.withTx(ctx, func(tx *sql.Tx) error {
		ids, err := sourceIDs(ctx, tx)
		if err != nil {
			return err
		}
		for _, id := range ids {
			record, err := getSource(ctx, tx, id)
			if err != nil {
				return err
			}
			out = append(out, record)
		}
		return nil
	})
	return
}

// ListDueSources возвращает источники, требующие обновления.
func (s *Storage) ListDueSources(ctx context.Context, now time.Time) (out []domain.Source, err error) {
	all, err := s.ListSources(ctx)
	if err != nil {
		return nil, err
	}
	for _, record := range all {
		source := record
		if source.AutoRefresh && (source.Sync.LastAttemptAt.IsZero() || !now.Before(source.Sync.LastAttemptAt.Add(source.UpdateInterval))) {
			out = append(out, record)
		}
	}
	return
}

// UpdateSource изменяет название и параметры получения источника.
func (s *Storage) UpdateSource(ctx context.Context, source domain.Source, at time.Time) (out domain.Source, err error) {
	err = s.withTx(ctx, func(tx *sql.Tx) error {
		old, err := getSource(ctx, tx, source.ID)
		if err != nil {
			return err
		}
		if old.Type != source.Type || old.ExternalRef != source.ExternalRef {
			return ErrConflict
		}
		for _, key := range []string{"telegram_channel_id", "telegram_message_id"} {
			if previous := old.FetchParams[key]; previous != "" && source.FetchParams[key] != previous {
				return ErrConflict
			}
		}
		ids, err := sourcePageIDs(ctx, tx, source.ID)
		if old.Name == source.Name {
			ids = nil
		}
		if err != nil {
			return err
		}
		before, err := publicState(ctx, tx, ids)
		if err != nil {
			return err
		}
		next := old
		next.Name = source.Name
		next.FetchParams = source.FetchParams
		if err = next.Validate(); err != nil {
			return err
		}
		params, err := jsonMap(next.FetchParams)
		if err != nil {
			return err
		}
		if !reflect.DeepEqual(old.FetchParams, next.FetchParams) || old.Name != next.Name {
			if _, err = tx.ExecContext(ctx, "UPDATE sources SET name=?,fetch_params=? WHERE id=?", next.Name, params, source.ID); err != nil {
				return err
			}
		}
		if err = refreshPublic(ctx, tx, ids, before, at); err != nil {
			return err
		}
		out, err = getSource(ctx, tx, source.ID)
		return err
	})
	if err != nil {
		out = domain.Source{}
	}
	return
}

// SetAutoRefresh изменяет настройки автоматического обновления.
func (s *Storage) SetAutoRefresh(ctx context.Context, id int64, enabled bool, interval time.Duration) error {
	return s.withTx(ctx, func(tx *sql.Tx) error {
		record, err := getSource(ctx, tx, id)
		if err != nil {
			return err
		}
		record.AutoRefresh = enabled
		record.UpdateInterval = interval
		if err = record.Validate(); err != nil {
			return err
		}
		_, err = tx.ExecContext(ctx, "UPDATE sources SET auto_refresh=?,update_interval_ns=? WHERE id=?", enabled, interval, id)
		return err
	})
}

// SetPublicationBlocked изменяет запрет публикации содержимого источника.
func (s *Storage) SetPublicationBlocked(ctx context.Context, id int64, blocked bool, at time.Time) error {
	return s.withTx(ctx, func(tx *sql.Tx) error {
		record, err := getSource(ctx, tx, id)
		if err != nil {
			return err
		}
		if record.PublicationBlocked == blocked {
			return nil
		}
		ids, err := sourcePageIDs(ctx, tx, id)
		if err != nil {
			return err
		}
		before, err := publicState(ctx, tx, ids)
		if err != nil {
			return err
		}
		_, err = tx.ExecContext(ctx, "UPDATE sources SET publication_blocked=? WHERE id=?", blocked, id)
		if err != nil {
			return err
		}
		return refreshPublic(ctx, tx, ids, before, at)
	})
}

// SaveFetchResult сохраняет результат получения источника.
func (s *Storage) SaveFetchResult(ctx context.Context, sourceID int64, at time.Time, message string) error {
	return s.withTx(ctx, func(tx *sql.Tx) error {
		source, err := getSource(ctx, tx, sourceID)
		if err != nil {
			return err
		}
		if err = validTime(at); err != nil {
			return err
		}
		if message == "" {
			err = source.Sync.RecordSuccess(at)
		} else {
			err = source.Sync.RecordFailure(at, message)
		}
		if err != nil {
			return err
		}
		_, err = tx.ExecContext(ctx, `UPDATE sources
   SET last_attempt_at=?, last_success_at=?, last_error=? WHERE id=?`, stamp(source.Sync.LastAttemptAt), stamp(source.Sync.LastSuccessAt), source.Sync.LastError, sourceID)
		return err
	})
}
