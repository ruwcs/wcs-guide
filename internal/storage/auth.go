package storage

import (
	"context"
	"database/sql"
	"time"

	"github.com/ruwcs/wcs-guide/internal/domain"
)

// getUser читает пользователя.
func getUser(ctx context.Context, tx *sql.Tx, id int64) (u domain.User, err error) {
	var created string
	err = tx.QueryRowContext(ctx, `-- Пользователь редактора.
SELECT id, login, display_name, webauthn_id, role, active, created_at
FROM users
WHERE id = ?`, id).Scan(&u.ID, &u.Login, &u.DisplayName, &u.WebAuthnID, &u.Role, &u.Active, &created)
	if err != nil {
		return
	}
	u.CreatedAt, err = time.Parse(timeFormat, created)
	if err == nil {
		err = u.Validate()
	}
	return
}

// GetUserByLogin возвращает пользователя по логину.
func (s *Storage) GetUserByLogin(ctx context.Context, login string) (u domain.User, err error) {
	err = s.withTx(ctx, func(tx *sql.Tx) error {
		var id int64
		if err := tx.QueryRowContext(ctx, "SELECT id FROM users WHERE login=?", login).Scan(&id); err != nil {
			return err
		}
		u, err = getUser(ctx, tx, id)
		return err
	})
	return
}

// UserCredentials возвращает ключи доступа пользователя.
func (s *Storage) UserCredentials(ctx context.Context, id int64) (data [][]byte, err error) {
	err = s.withTx(ctx, func(tx *sql.Tx) error {
		rows, err := tx.QueryContext(ctx, `-- Зарегистрированные ключи пользователя.
SELECT credential_json
FROM credentials
WHERE user_id = ?
ORDER BY id`, id)
		if err != nil {
			return err
		}
		defer rows.Close()
		for rows.Next() {
			var value string
			if err := rows.Scan(&value); err != nil {
				return err
			}
			data = append(data, []byte(value))
		}
		return rows.Err()
	})
	return
}

// BootstrapAdmin создаёт первого администратора и разрешение регистрации.
func (s *Storage) BootstrapAdmin(ctx context.Context, u domain.User, hash []byte, expires time.Time) (id int64, err error) {
	if err = u.Validate(); err != nil {
		return
	}
	err = s.withTx(ctx, func(tx *sql.Tx) error {
		var count int
		if err := tx.QueryRowContext(ctx, "SELECT count(*) FROM users").Scan(&count); err != nil {
			return err
		}
		if count != 0 {
			return ErrConflict
		}
		res, err := tx.ExecContext(ctx, `-- Первый администратор.
INSERT INTO users (
    login, display_name, webauthn_id, role, active, created_at
) VALUES (?, ?, ?, ?, ?, ?)`, u.Login, u.DisplayName, u.WebAuthnID, domain.RoleAdmin, true, stamp(u.CreatedAt))
		if err != nil {
			return err
		}
		id, err = res.LastInsertId()
		if err != nil {
			return err
		}
		_, err = tx.ExecContext(ctx, "INSERT INTO registration_grants VALUES(?,?,?,?)", hash, id, stamp(u.CreatedAt), stamp(expires))
		return err
	})
	return
}

// RecoverAdmin восстанавливает доступ пользователя.
func (s *Storage) RecoverAdmin(ctx context.Context, login string, hash []byte, at, expires time.Time) (id int64, err error) {
	err = s.withTx(ctx, func(tx *sql.Tx) error {
		if err := tx.QueryRowContext(ctx, "SELECT id FROM users WHERE login=?", login).Scan(&id); err != nil {
			return err
		}
		for _, query := range []string{
			"DELETE FROM credentials WHERE user_id=?",
			"DELETE FROM sessions WHERE user_id=?",
			"DELETE FROM registration_grants WHERE user_id=?",
		} {
			if _, err := tx.ExecContext(ctx, query, id); err != nil {
				return err
			}
		}
		if _, err := tx.ExecContext(ctx, "UPDATE users SET role='admin',active=1 WHERE id=?", id); err != nil {
			return err
		}
		_, err := tx.ExecContext(ctx, "INSERT INTO registration_grants VALUES(?,?,?,?)", hash, id, stamp(at), stamp(expires))
		return err
	})
	return
}

// GrantUser возвращает пользователя действующего разрешения.
func (s *Storage) GrantUser(ctx context.Context, hash []byte, at time.Time) (u domain.User, err error) {
	err = s.withTx(ctx, func(tx *sql.Tx) error {
		var id int64
		if err := tx.QueryRowContext(ctx, `-- Пользователь действующего разрешения.
SELECT user_id
FROM registration_grants
WHERE token_hash = ? AND expires_at > ?`, hash, stamp(at)).Scan(&id); err != nil {
			return err
		}
		u, err = getUser(ctx, tx, id)
		return err
	})
	return
}

// RegisterCredential сохраняет ключ и погашает разрешение.
func (s *Storage) RegisterCredential(ctx context.Context, userID int64, grant, id, data []byte, at time.Time) error {
	return s.withTx(ctx, func(tx *sql.Tx) error {
		u, err := getUser(ctx, tx, userID)
		if err != nil {
			return err
		}
		if !u.Active {
			return ErrConflict
		}
		res, err := tx.ExecContext(ctx, `-- Погашение действующего разрешения регистрации.
DELETE FROM registration_grants
WHERE token_hash = ? AND user_id = ? AND expires_at > ?`, grant, userID, stamp(at))
		if err != nil {
			return err
		}
		count, err := res.RowsAffected()
		if err != nil {
			return err
		}
		if count != 1 {
			return ErrConflict
		}
		_, err = tx.ExecContext(ctx, "INSERT INTO credentials VALUES(?,?,?,?)", id, userID, string(data), stamp(at))
		return err
	})
}

// LoginUser обновляет ключ и создаёт сессию.
func (s *Storage) LoginUser(ctx context.Context, userID int64, id, previous, data, hash []byte, at, expires time.Time) error {
	return s.withTx(ctx, func(tx *sql.Tx) error {
		u, err := getUser(ctx, tx, userID)
		if err != nil {
			return err
		}
		if !u.Active {
			return ErrConflict
		}
		res, err := tx.ExecContext(ctx, `-- Обновление проверенного ключа доступа.
UPDATE credentials
SET credential_json = ?
WHERE id = ? AND user_id = ? AND credential_json = ?`, string(data), id, userID, string(previous))
		if err != nil {
			return err
		}
		count, err := res.RowsAffected()
		if err != nil {
			return err
		}
		if count != 1 {
			return ErrConflict
		}
		if _, err = tx.ExecContext(ctx, "DELETE FROM sessions WHERE expires_at<=?", stamp(at)); err != nil {
			return err
		}
		_, err = tx.ExecContext(ctx, "INSERT INTO sessions VALUES(?,?,?,?)", hash, userID, stamp(at), stamp(expires))
		return err
	})
}

// SessionUser возвращает пользователя действующей сессии.
func (s *Storage) SessionUser(ctx context.Context, hash []byte, at time.Time) (u domain.User, err error) {
	err = s.withTx(ctx, func(tx *sql.Tx) error {
		var id int64
		if err := tx.QueryRowContext(ctx, `-- Пользователь действующей сессии.
SELECT user_id
FROM sessions
WHERE token_hash = ? AND expires_at > ?`, hash, stamp(at)).Scan(&id); err != nil {
			return err
		}
		u, err = getUser(ctx, tx, id)
		if err == nil && !u.Active {
			return ErrNotFound
		}
		return err
	})
	return
}

// Logout отзывает сессию.
func (s *Storage) Logout(ctx context.Context, hash []byte) error {
	return s.withTx(ctx, func(tx *sql.Tx) error {
		_, err := tx.ExecContext(ctx, "DELETE FROM sessions WHERE token_hash=?", hash)
		return err
	})
}

// SetUserAccess изменяет права пользователя.
func (s *Storage) SetUserAccess(ctx context.Context, id int64, role domain.Role, active bool) error {
	if role != domain.RoleAdmin && role != domain.RoleEditor {
		return invalid("user.role")
	}
	return s.withTx(ctx, func(tx *sql.Tx) error {
		if _, err := getUser(ctx, tx, id); err != nil {
			return err
		}
		if _, err := tx.ExecContext(ctx, "UPDATE users SET role=?,active=? WHERE id=?", role, active, id); err != nil {
			return err
		}
		if !active {
			_, err := tx.ExecContext(ctx, "DELETE FROM sessions WHERE user_id=?", id)
			return err
		}
		return nil
	})
}
