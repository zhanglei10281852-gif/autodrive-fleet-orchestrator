package sqlite

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"time"

	"github.com/zhanglei10281852-gif/autodrive-fleet-orchestrator/internal/domain/auth"
	"github.com/zhanglei10281852-gif/autodrive-fleet-orchestrator/internal/domain/common"
)

func (s *Store) CreateUser(ctx context.Context, user auth.User) error {
	if err := user.Validate(); err != nil {
		return err
	}
	_, err := s.db.ExecContext(ctx, `
		INSERT INTO users(id, username, password_hash, role, active, created_at, updated_at)
		VALUES(?, ?, ?, ?, ?, ?, ?)`,
		user.ID, user.Username, user.PasswordHash, user.Role, boolInt(user.Active),
		formatTime(user.CreatedAt), formatTime(user.UpdatedAt))
	return mapSQLError(err, "user")
}

func (s *Store) UpsertBootstrapUser(ctx context.Context, user auth.User) error {
	if err := user.Validate(); err != nil {
		return err
	}
	_, err := s.db.ExecContext(ctx, `
		INSERT INTO users(id, username, password_hash, role, active, created_at, updated_at)
		VALUES(?, ?, ?, ?, ?, ?, ?)
		ON CONFLICT(username) DO UPDATE SET
			active = CASE WHEN users.active = 0 THEN 0 ELSE excluded.active END,
			updated_at = excluded.updated_at`,
		user.ID, user.Username, user.PasswordHash, user.Role, boolInt(user.Active),
		formatTime(user.CreatedAt), formatTime(user.UpdatedAt))
	return mapSQLError(err, "bootstrap user")
}

func (s *Store) UserByUsername(ctx context.Context, username string) (auth.User, error) {
	return scanUser(s.db.QueryRowContext(ctx, `
		SELECT id, username, password_hash, role, active, created_at, updated_at
		FROM users WHERE username = ?`, username))
}

func (s *Store) UserByID(ctx context.Context, id string) (auth.User, error) {
	return scanUser(s.db.QueryRowContext(ctx, `
		SELECT id, username, password_hash, role, active, created_at, updated_at
		FROM users WHERE id = ?`, id))
}

func scanUser(row *sql.Row) (auth.User, error) {
	var user auth.User
	var role string
	var active int
	var created, updated string
	err := row.Scan(&user.ID, &user.Username, &user.PasswordHash, &role, &active, &created, &updated)
	if err != nil {
		return auth.User{}, mapSQLError(err, "user")
	}
	user.Role = auth.Role(role)
	user.Active = active == 1
	var parseErr error
	if user.CreatedAt, parseErr = parseTime(created); parseErr != nil {
		return auth.User{}, parseErr
	}
	if user.UpdatedAt, parseErr = parseTime(updated); parseErr != nil {
		return auth.User{}, parseErr
	}
	return user, nil
}

func (s *Store) CreateSession(ctx context.Context, session auth.Session) error {
	if session.ID == "" || session.UserID == "" || session.TokenHash == "" {
		return common.ErrInvalid
	}
	_, err := s.db.ExecContext(ctx, `
		INSERT INTO sessions(id, user_id, token_hash, expires_at, revoked_at, created_at, last_seen_at)
		VALUES(?, ?, ?, ?, ?, ?, ?)`,
		session.ID, session.UserID, session.TokenHash, formatTime(session.ExpiresAt),
		nullableTime(session.RevokedAt), formatTime(session.CreatedAt), formatTime(session.LastSeen))
	return mapSQLError(err, "session")
}

func (s *Store) CreateSessionWithAudit(ctx context.Context, session auth.Session, auditID, requestID string) error {
	return s.WithTx(ctx, func(tx *sql.Tx) error {
		if _, err := tx.ExecContext(ctx, `
			INSERT INTO sessions(id, user_id, token_hash, expires_at, revoked_at, created_at, last_seen_at)
			VALUES(?, ?, ?, ?, NULL, ?, ?)`,
			session.ID, session.UserID, session.TokenHash, formatTime(session.ExpiresAt),
			formatTime(session.CreatedAt), formatTime(session.LastSeen)); err != nil {
			return mapSQLError(err, "session")
		}
		if _, err := tx.ExecContext(ctx, `
			INSERT INTO audit_events(id, actor_id, actor_role, action, object_type, object_id, result, request_id, details, created_at)
			SELECT ?, id, role, 'auth.login', 'session', ?, 'success', ?, '{}', ? FROM users WHERE id = ?`,
			auditID, session.ID, requestID, formatTime(session.CreatedAt), session.UserID); err != nil {
			return mapSQLError(err, "login audit")
		}
		return nil
	})
}

func (s *Store) SessionByTokenHash(ctx context.Context, tokenHash string) (auth.Session, auth.User, error) {
	row := s.db.QueryRowContext(ctx, `
		SELECT s.id, s.user_id, s.token_hash, s.expires_at, s.revoked_at, s.created_at, s.last_seen_at,
		       u.id, u.username, u.password_hash, u.role, u.active, u.created_at, u.updated_at
		FROM sessions s JOIN users u ON u.id = s.user_id
		WHERE s.token_hash = ?`, tokenHash)
	var session auth.Session
	var user auth.User
	var expires, created, seen, userCreated, userUpdated string
	var revoked sql.NullString
	var role string
	var active int
	if err := row.Scan(
		&session.ID, &session.UserID, &session.TokenHash, &expires, &revoked, &created, &seen,
		&user.ID, &user.Username, &user.PasswordHash, &role, &active, &userCreated, &userUpdated,
	); err != nil {
		return auth.Session{}, auth.User{}, mapSQLError(err, "session")
	}
	var err error
	if session.ExpiresAt, err = parseTime(expires); err != nil {
		return auth.Session{}, auth.User{}, err
	}
	if session.RevokedAt, err = parseNullableTime(revoked); err != nil {
		return auth.Session{}, auth.User{}, err
	}
	if session.CreatedAt, err = parseTime(created); err != nil {
		return auth.Session{}, auth.User{}, err
	}
	if session.LastSeen, err = parseTime(seen); err != nil {
		return auth.Session{}, auth.User{}, err
	}
	if user.CreatedAt, err = parseTime(userCreated); err != nil {
		return auth.Session{}, auth.User{}, err
	}
	if user.UpdatedAt, err = parseTime(userUpdated); err != nil {
		return auth.Session{}, auth.User{}, err
	}
	user.Role = auth.Role(role)
	user.Active = active == 1
	return session, user, nil
}

func (s *Store) RevokeSession(ctx context.Context, sessionID, userID, auditID, requestID string, at time.Time) error {
	return s.WithTx(ctx, func(tx *sql.Tx) error {
		result, err := tx.ExecContext(ctx, `
			UPDATE sessions SET revoked_at = ?
			WHERE id = ? AND user_id = ? AND revoked_at IS NULL`, formatTime(at), sessionID, userID)
		if err != nil {
			return mapSQLError(err, "session")
		}
		changed, err := result.RowsAffected()
		if err != nil {
			return fmt.Errorf("read revoke result: %w", err)
		}
		if changed == 0 {
			return common.ErrNotFound
		}
		if _, err := tx.ExecContext(ctx, `
			INSERT INTO audit_events(id, actor_id, actor_role, action, object_type, object_id, result, request_id, details, created_at)
			SELECT ?, id, role, 'auth.logout', 'session', ?, 'success', ?, '{}', ? FROM users WHERE id = ?`,
			auditID, sessionID, requestID, formatTime(at), userID); err != nil {
			return mapSQLError(err, "logout audit")
		}
		return nil
	})
}

func (s *Store) TouchSession(ctx context.Context, sessionID string, at time.Time) error {
	_, err := s.db.ExecContext(ctx, `UPDATE sessions SET last_seen_at = ? WHERE id = ? AND revoked_at IS NULL`, formatTime(at), sessionID)
	return mapSQLError(err, "session")
}

func (s *Store) DeleteExpiredSessions(ctx context.Context, before time.Time, limit int) (int64, error) {
	if limit <= 0 || limit > 1000 {
		limit = 200
	}
	result, err := s.db.ExecContext(ctx, `
		DELETE FROM sessions WHERE id IN (
			SELECT id FROM sessions
			WHERE expires_at <= ? OR revoked_at IS NOT NULL
			ORDER BY expires_at LIMIT ?
		)`, formatTime(before), limit)
	if err != nil {
		return 0, mapSQLError(err, "expired sessions")
	}
	count, err := result.RowsAffected()
	if err != nil {
		return 0, fmt.Errorf("read expired session result: %w", err)
	}
	return count, nil
}

func (s *Store) SessionCount(ctx context.Context, userID string, onlyActive bool, now time.Time) (int, error) {
	query := "SELECT COUNT(*) FROM sessions WHERE user_id = ?"
	args := []any{userID}
	if onlyActive {
		query += " AND revoked_at IS NULL AND expires_at > ?"
		args = append(args, formatTime(now))
	}
	var count int
	if err := s.db.QueryRowContext(ctx, query, args...).Scan(&count); err != nil {
		return 0, mapSQLError(err, "session count")
	}
	return count, nil
}

func (s *Store) DisableUser(ctx context.Context, id string, expectedUpdated time.Time, at time.Time) error {
	return s.WithTx(ctx, func(tx *sql.Tx) error {
		result, err := tx.ExecContext(ctx, `
			UPDATE users SET active = 0, updated_at = ? WHERE id = ? AND updated_at = ?`,
			formatTime(at), id, formatTime(expectedUpdated))
		if err != nil {
			return mapSQLError(err, "user")
		}
		changed, _ := result.RowsAffected()
		if changed == 0 {
			return common.ErrConflict
		}
		if _, err := tx.ExecContext(ctx, `UPDATE sessions SET revoked_at = ? WHERE user_id = ? AND revoked_at IS NULL`, formatTime(at), id); err != nil {
			return mapSQLError(err, "user sessions")
		}
		return nil
	})
}

func isNoRows(err error) bool { return errors.Is(err, sql.ErrNoRows) }
