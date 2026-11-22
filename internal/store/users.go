package store

import (
	"context"
	"time"

	"github.com/finn-freitag/tossbox/internal/model"
)

// User row.
type User struct {
	ID           string
	Username     string
	PasswordHash string
	Role         string
	SSHKeys      []string
	Quota        model.Quota
	CreatedAt    time.Time
}

const userCols = `id, username, password_hash, role, ssh_keys, quota, created_at`

func scanUser(row interface{ Scan(...any) error }) (*User, error) {
	u := &User{}
	if err := row.Scan(&u.ID, &u.Username, &u.PasswordHash, &u.Role, &u.SSHKeys, &u.Quota, &u.CreatedAt); err != nil {
		return nil, mapErr(err)
	}
	if u.SSHKeys == nil {
		u.SSHKeys = []string{}
	}
	u.Quota = u.Quota.Normalize()
	return u, nil
}

// CreateUser inserts a user.
func (s *Store) CreateUser(ctx context.Context, u *User) error {
	if u.SSHKeys == nil {
		u.SSHKeys = []string{}
	}
	err := s.pool.QueryRow(ctx,
		`INSERT INTO users (id, username, password_hash, role, ssh_keys, quota) VALUES ($1,$2,$3,$4,$5,$6) RETURNING created_at`,
		u.ID, u.Username, u.PasswordHash, u.Role, u.SSHKeys, u.Quota.Normalize()).Scan(&u.CreatedAt)
	return mapErr(err)
}

// GetUser by ID.
func (s *Store) GetUser(ctx context.Context, id string) (*User, error) {
	return scanUser(s.pool.QueryRow(ctx, `SELECT `+userCols+` FROM users WHERE id = $1`, id))
}

// GetUserByUsername by name (case-insensitive).
func (s *Store) GetUserByUsername(ctx context.Context, name string) (*User, error) {
	return scanUser(s.pool.QueryRow(ctx, `SELECT `+userCols+` FROM users WHERE lower(username) = lower($1)`, name))
}

// ListUsers returns all users ordered by name.
func (s *Store) ListUsers(ctx context.Context) ([]*User, error) {
	rows, err := s.pool.Query(ctx, `SELECT `+userCols+` FROM users ORDER BY username`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []*User
	for rows.Next() {
		u, err := scanUser(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, u)
	}
	return out, rows.Err()
}

// CountUsers returns the number of users.
func (s *Store) CountUsers(ctx context.Context) (int, error) {
	var n int
	err := s.pool.QueryRow(ctx, `SELECT count(*) FROM users`).Scan(&n)
	return n, err
}

// UpdateUser writes password hash, role, ssh keys and quota.
func (s *Store) UpdateUser(ctx context.Context, u *User) error {
	tag, err := s.pool.Exec(ctx, `UPDATE users SET password_hash=$2, role=$3, ssh_keys=$4, quota=$5 WHERE id=$1`,
		u.ID, u.PasswordHash, u.Role, u.SSHKeys, u.Quota.Normalize())
	if err != nil {
		return mapErr(err)
	}
	if tag.RowsAffected() == 0 {
		return ErrNotFound
	}
	return nil
}

// DeleteUser deletes a user; fails with ErrConflict if devices still reference it.
func (s *Store) DeleteUser(ctx context.Context, id string) error {
	var n int
	if err := s.pool.QueryRow(ctx, `SELECT count(*) FROM devices WHERE owner_id=$1`, id).Scan(&n); err != nil {
		return err
	}
	if n > 0 {
		return ErrConflict
	}
	tag, err := s.pool.Exec(ctx, `DELETE FROM users WHERE id=$1`, id)
	if err != nil {
		return mapErr(err)
	}
	if tag.RowsAffected() == 0 {
		return ErrNotFound
	}
	return nil
}

// CreateSession stores a session token hash.
func (s *Store) CreateSession(ctx context.Context, hash, userID string, expires time.Time) error {
	_, err := s.pool.Exec(ctx, `INSERT INTO sessions (token_hash, user_id, expires_at) VALUES ($1,$2,$3)`, hash, userID, expires)
	return mapErr(err)
}

// SessionUser returns the user of a valid session.
func (s *Store) SessionUser(ctx context.Context, hash string) (*User, error) {
	return scanUser(s.pool.QueryRow(ctx, `SELECT `+prefixed("u", userCols)+` FROM sessions s JOIN users u ON u.id = s.user_id WHERE s.token_hash=$1 AND s.expires_at > now()`, hash))
}

// DeleteSession removes a session.
func (s *Store) DeleteSession(ctx context.Context, hash string) error {
	_, err := s.pool.Exec(ctx, `DELETE FROM sessions WHERE token_hash=$1`, hash)
	return err
}

// DeleteUserSessions removes all sessions of a user (password change).
func (s *Store) DeleteUserSessions(ctx context.Context, userID, exceptHash string) error {
	_, err := s.pool.Exec(ctx, `DELETE FROM sessions WHERE user_id=$1 AND token_hash<>$2`, userID, exceptHash)
	return err
}

// PurgeExpiredSessions deletes expired sessions.
func (s *Store) PurgeExpiredSessions(ctx context.Context) error {
	_, err := s.pool.Exec(ctx, `DELETE FROM sessions WHERE expires_at < now()`)
	return err
}

// APIToken row.
type APIToken struct {
	ID         string     `json:"id"`
	UserID     string     `json:"-"`
	Name       string     `json:"name"`
	CreatedAt  time.Time  `json:"created_at"`
	LastUsedAt *time.Time `json:"last_used_at"`
	ExpiresAt  *time.Time `json:"expires_at"`
}

// CreateAPIToken stores a token hash.
func (s *Store) CreateAPIToken(ctx context.Context, t *APIToken, hash string) error {
	err := s.pool.QueryRow(ctx, `INSERT INTO api_tokens (id, user_id, name, token_hash, expires_at) VALUES ($1,$2,$3,$4,$5) RETURNING created_at`,
		t.ID, t.UserID, t.Name, hash, t.ExpiresAt).Scan(&t.CreatedAt)
	return mapErr(err)
}

// ListAPITokens of a user.
func (s *Store) ListAPITokens(ctx context.Context, userID string) ([]*APIToken, error) {
	rows, err := s.pool.Query(ctx, `SELECT id, user_id, name, created_at, last_used_at, expires_at FROM api_tokens WHERE user_id=$1 ORDER BY created_at`, userID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []*APIToken{}
	for rows.Next() {
		t := &APIToken{}
		if err := rows.Scan(&t.ID, &t.UserID, &t.Name, &t.CreatedAt, &t.LastUsedAt, &t.ExpiresAt); err != nil {
			return nil, err
		}
		out = append(out, t)
	}
	return out, rows.Err()
}

// DeleteAPIToken deletes a token of a user.
func (s *Store) DeleteAPIToken(ctx context.Context, userID, id string) error {
	tag, err := s.pool.Exec(ctx, `DELETE FROM api_tokens WHERE id=$1 AND user_id=$2`, id, userID)
	if err != nil {
		return mapErr(err)
	}
	if tag.RowsAffected() == 0 {
		return ErrNotFound
	}
	return nil
}

// APITokenUser resolves a token hash to its user and records the use.
func (s *Store) APITokenUser(ctx context.Context, hash string) (*User, error) {
	u, err := scanUser(s.pool.QueryRow(ctx, `UPDATE api_tokens t SET last_used_at = now() FROM users u
		WHERE t.token_hash=$1 AND u.id = t.user_id AND (t.expires_at IS NULL OR t.expires_at > now())
		RETURNING `+prefixed("u", userCols), hash))
	return u, err
}

// Audit writes an audit log entry (errors are ignored by callers on purpose).
func (s *Store) Audit(ctx context.Context, userID, action, target string, detail any) error {
	var uid any
	if userID != "" {
		uid = userID
	}
	if detail == nil {
		detail = map[string]any{}
	}
	_, err := s.pool.Exec(ctx, `INSERT INTO audit_log (user_id, action, target, detail) VALUES ($1,$2,$3,$4)`, uid, action, target, detail)
	return err
}

// GetSettings returns the stored settings merged over the defaults.
func (s *Store) GetSettings(ctx context.Context) (model.Settings, error) {
	st := model.DefaultSettings()
	err := s.pool.QueryRow(ctx, `SELECT value FROM settings WHERE id=1`).Scan(&st)
	if err = mapErr(err); err != nil && err != ErrNotFound {
		return st, err
	}
	if st.DefaultServices == nil {
		st.DefaultServices = []string{}
	}
	return st, nil
}

// PutSettings stores the settings.
func (s *Store) PutSettings(ctx context.Context, st model.Settings) error {
	_, err := s.pool.Exec(ctx, `INSERT INTO settings (id, value) VALUES (1, $1) ON CONFLICT (id) DO UPDATE SET value = EXCLUDED.value`, st)
	return err
}

func prefixed(alias, cols string) string {
	parts := splitCols(cols)
	for i, p := range parts {
		parts[i] = alias + "." + p
	}
	return joinCols(parts)
}
