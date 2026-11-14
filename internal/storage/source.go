package storage

import (
	"context"
	"database/sql"
	"reflect"
	"time"

	"github.com/ruwcs/wcs-guide/internal/domain"
)

// SourceRecord дополняет источник условиями согласованной записи.
type SourceRecord struct {
	Source       domain.Source // Текущее состояние источника.
	EditRevision int64         // Редакция настроек и переключателей.
	SyncRevision int64         // Редакция завершённых попыток получения.
}

// FetchToken фиксирует состояние перед внешним получением.
type FetchToken struct {
	SourceID     int64 // Идентификатор источника.
	EditRevision int64 // Ожидаемая редакция настроек.
	SyncRevision int64 // Ожидаемая редакция получения.
}

// sourceColumns задаёт единый порядок столбцов при чтении источника.
const sourceColumns = "id,name,type,external_ref,fetch_params,update_interval_ns,auto_refresh,publication_blocked,last_attempt_at,last_success_at,last_error,edit_revision,sync_revision"

func getSource(ctx context.Context, tx *sql.Tx, id int64) (SourceRecord, error) {
	var record SourceRecord
	var params, lastAttemptAt, lastSuccessAt sql.NullString
	err := tx.QueryRowContext(ctx, "SELECT "+sourceColumns+" FROM sources WHERE id=?", id).Scan(&record.Source.ID, &record.Source.Name, &record.Source.Type, &record.Source.ExternalRef, &params, &record.Source.UpdateInterval, &record.Source.AutoRefresh, &record.Source.PublicationBlocked, &lastAttemptAt, &lastSuccessAt, &record.Source.Sync.LastError, &record.EditRevision, &record.SyncRevision)
	if err != nil {
		return record, err
	}
	record.Source.FetchParams, err = parseMap(params)
	if err != nil {
		return record, &internalError{err}
	}
	record.Source.Sync.LastAttemptAt, err = parsed(lastAttemptAt)
	if err != nil {
		return record, &internalError{err}
	}
	record.Source.Sync.LastSuccessAt, err = parsed(lastSuccessAt)
	if err != nil {
		return record, &internalError{err}
	}
	return record, record.Source.Validate()
}

// CreateSource сохраняет новый источник и возвращает назначенный ID.
func (s *Storage) CreateSource(ctx context.Context, source domain.Source) (record SourceRecord, err error) {
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
		record = SourceRecord{}
	}
	return
}

// GetSource читает источник вместе с редакциями.
func (s *Storage) GetSource(ctx context.Context, id int64) (r SourceRecord, err error) {
	err = s.withTx(ctx, func(tx *sql.Tx) error { var err error; r, err = getSource(ctx, tx, id); return err })
	return
}
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
func (s *Storage) ListSources(ctx context.Context) (out []SourceRecord, err error) {
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

// ListDueSources выбирает источники по последней завершённой попытке.
func (s *Storage) ListDueSources(ctx context.Context, now time.Time) (out []SourceRecord, err error) {
	all, err := s.ListSources(ctx)
	if err != nil {
		return nil, err
	}
	for _, record := range all {
		source := record.Source
		if source.AutoRefresh && (source.Sync.LastAttemptAt.IsZero() || !now.Before(source.Sync.LastAttemptAt.Add(source.UpdateInterval))) {
			out = append(out, record)
		}
	}
	return
}

// UpdateSource меняет только название и параметры, сохраняя Sync и переключатели.
func (s *Storage) UpdateSource(ctx context.Context, source domain.Source, revision int64, at time.Time) (out SourceRecord, err error) {
	err = s.withTx(ctx, func(tx *sql.Tx) error {
		old, err := getSource(ctx, tx, source.ID)
		if err != nil {
			return err
		}
		if old.EditRevision != revision || old.Source.Type != source.Type || old.Source.ExternalRef != source.ExternalRef {
			return ErrConflict
		}
		ids, err := sourcePageIDs(ctx, tx, source.ID)
		if old.Source.Name == source.Name {
			ids = nil
		}
		if err != nil {
			return err
		}
		before, err := publicState(ctx, tx, ids)
		if err != nil {
			return err
		}
		next := old.Source
		next.Name = source.Name
		next.FetchParams = source.FetchParams
		if err = next.Validate(); err != nil {
			return err
		}
		params, err := jsonMap(next.FetchParams)
		if err != nil {
			return err
		}
		if !reflect.DeepEqual(old.Source.FetchParams, next.FetchParams) || old.Source.Name != next.Name {
			if _, err = tx.ExecContext(ctx, "UPDATE sources SET name=?,fetch_params=?,edit_revision=edit_revision+1 WHERE id=?", next.Name, params, source.ID); err != nil {
				return err
			}
		}
		if err = refreshPublic(ctx, tx, ids, before, at, false); err != nil {
			return err
		}
		out, err = getSource(ctx, tx, source.ID)
		return err
	})
	if err != nil {
		out = SourceRecord{}
	}
	return
}

// SetAutoRefresh изменяет только периодичность и разрешение обновления.
func (s *Storage) SetAutoRefresh(ctx context.Context, id int64, enabled bool, interval time.Duration) error {
	return s.withTx(ctx, func(tx *sql.Tx) error {
		record, err := getSource(ctx, tx, id)
		if err != nil {
			return err
		}
		record.Source.AutoRefresh = enabled
		record.Source.UpdateInterval = interval
		if err = record.Source.Validate(); err != nil {
			return err
		}
		_, err = tx.ExecContext(ctx, "UPDATE sources SET auto_refresh=?,update_interval_ns=?,edit_revision=edit_revision+1 WHERE id=?", enabled, interval, id)
		return err
	})
}

// SetPublicationBlocked меняет независимый запрет и публичные даты затронутых страниц.
func (s *Storage) SetPublicationBlocked(ctx context.Context, id int64, blocked bool, at time.Time) error {
	return s.withTx(ctx, func(tx *sql.Tx) error {
		record, err := getSource(ctx, tx, id)
		if err != nil {
			return err
		}
		if record.Source.PublicationBlocked == blocked {
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
		_, err = tx.ExecContext(ctx, "UPDATE sources SET publication_blocked=?,edit_revision=edit_revision+1 WHERE id=?", blocked, id)
		if err != nil {
			return err
		}
		if !blocked {
			if err = publishReadyForSource(ctx, tx, id, at); err != nil {
				return err
			}
		}
		return refreshPublic(ctx, tx, ids, before, at, blocked)
	})
}

// GetFetchInput возвращает источник и токен перед сетевой работой.
func (s *Storage) GetFetchInput(ctx context.Context, id int64) (v domain.Source, t FetchToken, err error) {
	r, err := s.GetSource(ctx, id)
	if err != nil {
		return v, t, err
	}
	return r.Source, FetchToken{id, r.EditRevision, r.SyncRevision}, nil
}
func checkFetch(ctx context.Context, tx *sql.Tx, fetchToken FetchToken) (SourceRecord, error) {
	record, err := getSource(ctx, tx, fetchToken.SourceID)
	if err == nil && (record.EditRevision != fetchToken.EditRevision || record.SyncRevision != fetchToken.SyncRevision) {
		err = ErrConflict
	}
	return record, err
}

// SaveFetchFailure сохраняет безопасное описание неудачи без изменения настроек.
func (s *Storage) SaveFetchFailure(ctx context.Context, fetchToken FetchToken, at time.Time, message string) error {
	return s.withTx(ctx, func(tx *sql.Tx) error {
		record, err := checkFetch(ctx, tx, fetchToken)
		if err != nil {
			return err
		}
		if err = validTime(at); err != nil {
			return err
		}
		if err = record.Source.Sync.RecordFailure(at, message); err != nil {
			return err
		}
		_, err = tx.ExecContext(ctx, "UPDATE sources SET last_attempt_at=?,last_error=?,sync_revision=sync_revision+1 WHERE id=?", stamp(at), message, fetchToken.SourceID)
		return err
	})
}
