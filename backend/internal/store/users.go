package store

import (
	"context"
	"database/sql"
	"errors"
	"strings"
	"time"
)

// Sentinel errors mapped by the API layer to HTTP status codes.
var (
	ErrNotFound      = errors.New("not found")
	ErrConflict      = errors.New("conflict")
	ErrUsernameTaken = errors.New("username taken")
	ErrInvalid       = errors.New("invalid input")
	ErrNotMember     = errors.New("not a workspace member")
)

type User struct {
	ID       int64  `json:"id"`
	Username string `json:"username"`
}

type Workspace struct {
	ID       int64  `json:"id"`
	Name     string `json:"name"`
	Timezone string `json:"timezone"`
	Role     string `json:"role,omitempty"` // caller's role, set by WorkspacesForUser
}

// CreateUser inserts a user with an already-hashed password.
func (s *Store) CreateUser(ctx context.Context, username, passwordHash string) (*User, error) {
	now := formatTime(nowUTC())
	res, err := s.db.ExecContext(ctx,
		`INSERT INTO users (username, password_hash, created_at, updated_at) VALUES (?, ?, ?, ?)`,
		username, passwordHash, now, now)
	if err != nil {
		if isUniqueViolation(err) {
			return nil, ErrUsernameTaken
		}
		return nil, err
	}
	id, err := res.LastInsertId()
	if err != nil {
		return nil, err
	}
	return &User{ID: id, Username: username}, nil
}

func (s *Store) UserByUsername(ctx context.Context, username string) (*User, string, error) {
	var u User
	var hash string
	err := s.db.QueryRowContext(ctx,
		`SELECT id, username, password_hash FROM users WHERE username = ? COLLATE NOCASE`, username).
		Scan(&u.ID, &u.Username, &hash)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, "", ErrNotFound
	}
	if err != nil {
		return nil, "", err
	}
	return &u, hash, nil
}

// CreateSession stores a new session token.
func (s *Store) CreateSession(ctx context.Context, token string, userID int64, ttl time.Duration) error {
	_, err := s.db.ExecContext(ctx,
		`INSERT INTO sessions (token, user_id, created_at, expires_at) VALUES (?, ?, ?, ?)`,
		token, userID, formatTime(nowUTC()), formatTime(nowUTC().Add(ttl)))
	return err
}

// UserBySession resolves a token to a user, extending a valid session's expiry
// (rolling sessions). Expired or unknown tokens return ErrNotFound.
func (s *Store) UserBySession(ctx context.Context, token string, ttl time.Duration) (*User, error) {
	var u User
	var expires string
	err := s.db.QueryRowContext(ctx,
		`SELECT u.id, u.username, s.expires_at FROM sessions s JOIN users u ON u.id = s.user_id WHERE s.token = ?`, token).
		Scan(&u.ID, &u.Username, &expires)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, ErrNotFound
	}
	if err != nil {
		return nil, err
	}
	exp, err := time.ParseInLocation(timeLayout, expires, time.UTC)
	if err != nil || nowUTC().After(exp) {
		_, _ = s.db.ExecContext(ctx, `DELETE FROM sessions WHERE token = ?`, token)
		return nil, ErrNotFound
	}
	_, _ = s.db.ExecContext(ctx, `UPDATE sessions SET expires_at = ? WHERE token = ?`,
		formatTime(nowUTC().Add(ttl)), token)
	return &u, nil
}

func (s *Store) DeleteSession(ctx context.Context, token string) error {
	_, err := s.db.ExecContext(ctx, `DELETE FROM sessions WHERE token = ?`, token)
	return err
}

// CreateWorkspaceWithOwner creates a workspace and adds the creator as owner,
// in one transaction.
func (s *Store) CreateWorkspaceWithOwner(ctx context.Context, userID int64, name, timezone string) (*Workspace, error) {
	ws := &Workspace{Name: name, Timezone: timezone}
	now := formatTime(nowUTC())
	err := s.ExecTx(ctx, func(tx *sql.Tx) error {
		res, err := tx.ExecContext(ctx,
			`INSERT INTO workspaces (name, timezone, created_by, created_at, updated_at) VALUES (?, ?, ?, ?, ?)`,
			name, timezone, userID, now, now)
		if err != nil {
			return err
		}
		id, err := res.LastInsertId()
		if err != nil {
			return err
		}
		ws.ID = id
		_, err = tx.ExecContext(ctx,
			`INSERT INTO workspace_members (workspace_id, user_id, role, joined_at) VALUES (?, ?, 'owner', ?)`,
			id, userID, now)
		return err
	})
	if err != nil {
		return nil, err
	}
	ws.Role = "owner"
	return ws, nil
}

func (s *Store) WorkspacesForUser(ctx context.Context, userID int64) ([]Workspace, error) {
	rows, err := s.db.QueryContext(ctx,
		`SELECT w.id, w.name, w.timezone, m.role FROM workspaces w
		 JOIN workspace_members m ON m.workspace_id = w.id WHERE m.user_id = ? ORDER BY w.id`, userID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []Workspace{}
	for rows.Next() {
		var w Workspace
		if err := rows.Scan(&w.ID, &w.Name, &w.Timezone, &w.Role); err != nil {
			return nil, err
		}
		out = append(out, w)
	}
	return out, rows.Err()
}

// Membership returns the caller's role, or ErrNotMember.
func (s *Store) Membership(ctx context.Context, userID, wsID int64) (string, error) {
	var role string
	err := s.db.QueryRowContext(ctx,
		`SELECT role FROM workspace_members WHERE workspace_id = ? AND user_id = ?`, wsID, userID).Scan(&role)
	if errors.Is(err, sql.ErrNoRows) {
		return "", ErrNotMember
	}
	if err != nil {
		return "", err
	}
	return role, nil
}

func (s *Store) WorkspaceTimezone(ctx context.Context, wsID int64) (string, error) {
	var tz string
	err := s.db.QueryRowContext(ctx, `SELECT timezone FROM workspaces WHERE id = ?`, wsID).Scan(&tz)
	if errors.Is(err, sql.ErrNoRows) {
		return "", ErrNotFound
	}
	if err != nil {
		return "", err
	}
	if tz == "" {
		tz = "Asia/Shanghai"
	}
	return tz, nil
}

func isUniqueViolation(err error) bool {
	// modernc.org/sqlite surfaces constraint failures in the error message.
	msg := err.Error()
	return strings.Contains(msg, "UNIQUE constraint failed") || strings.Contains(msg, "constraint failed")
}
