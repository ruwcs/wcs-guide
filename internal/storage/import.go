package storage

import (
	"context"
	"database/sql"

	"github.com/ruwcs/wcs-guide/internal/domain"
)

// ListBlocksBySource возвращает блоки указанного источника.
func (s *Storage) ListBlocksBySource(ctx context.Context, sourceID int64) (out []domain.Block, err error) {
	err = s.withTx(ctx, func(tx *sql.Tx) error {
		rows, err := tx.QueryContext(ctx, "SELECT id FROM blocks WHERE source_id=? ORDER BY id", sourceID)
		if err != nil {
			return err
		}
		var ids []int64
		for rows.Next() {
			var id int64
			if err := rows.Scan(&id); err != nil {
				rows.Close()
				return err
			}
			ids = append(ids, id)
		}
		err = rows.Err()
		rows.Close()
		if err != nil {
			return err
		}
		for _, id := range ids {
			block, err := getBlock(ctx, tx, id)
			if err != nil {
				return err
			}
			out = append(out, block)
		}
		return nil
	})
	if err != nil {
		out = nil
	}
	return
}
