package terminal

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"os"
	"strings"
	"time"

	"github.com/ligson/water/water-be/internal/dbutil"
	"github.com/ligson/water/water-be/internal/uid"
)

const (
	AuthTypePassword   = "password"
	AuthTypePrivateKey = "private_key"

	SessionStatusCreated    = "created"
	SessionStatusConnecting = "connecting"
	SessionStatusActive     = "active"
	SessionStatusClosed     = "closed"
	SessionStatusError      = "error"
)

var ErrNotFound = errors.New("terminal resource not found")

type Profile struct {
	ID                   string    `json:"id"`
	WorkspaceID          string    `json:"workspaceId"`
	Name                 string    `json:"name"`
	Host                 string    `json:"host"`
	Port                 int       `json:"port"`
	Username             string    `json:"username"`
	AuthType             string    `json:"authType"`
	Password             string    `json:"-"`
	PasswordConfigured   bool      `json:"passwordConfigured"`
	PrivateKey           string    `json:"-"`
	PrivateKeyConfigured bool      `json:"privateKeyConfigured"`
	Passphrase           string    `json:"-"`
	PassphraseConfigured bool      `json:"passphraseConfigured"`
	DefaultCwd           string    `json:"defaultCwd"`
	HostFingerprint      string    `json:"hostFingerprint"`
	Enabled              bool      `json:"enabled"`
	CreatedAt            time.Time `json:"createdAt"`
	UpdatedAt            time.Time `json:"updatedAt"`
}

type Session struct {
	ID           string     `json:"id"`
	WorkspaceID  string     `json:"workspaceId"`
	ProfileID    string     `json:"profileId"`
	Status       string     `json:"status"`
	Cwd          string     `json:"cwd"`
	Cols         int        `json:"cols"`
	Rows         int        `json:"rows"`
	CreatedAt    time.Time  `json:"createdAt"`
	UpdatedAt    time.Time  `json:"updatedAt"`
	LastActiveAt *time.Time `json:"lastActiveAt,omitempty"`
	ClosedAt     *time.Time `json:"closedAt,omitempty"`
}

type Store struct {
	db *sql.DB
}

func NewStore(db *sql.DB) *Store {
	return &Store{db: db}
}

type CreateProfileInput struct {
	WorkspaceID     string
	Name            string
	Host            string
	Port            int
	Username        string
	AuthType        string
	Password        string
	PrivateKey      string
	Passphrase      string
	DefaultCwd      string
	HostFingerprint string
	Enabled         bool
}

type UpdateProfileInput struct {
	Name            string
	Host            string
	Port            int
	Username        string
	AuthType        string
	Password        *string
	PrivateKey      *string
	Passphrase      *string
	DefaultCwd      string
	HostFingerprint string
	Enabled         bool
}

type CreateSessionInput struct {
	WorkspaceID string
	ProfileID   string
	Cwd         string
	Cols        int
	Rows        int
}

func (s *Store) ListProfiles(ctx context.Context, workspaceID string) ([]Profile, error) {
	rows, err := s.db.QueryContext(ctx, `
SELECT id, workspace_id, name, host, port, username, auth_type, password, private_key, passphrase, default_cwd, host_fingerprint, enabled, created_at, updated_at
FROM terminal_profiles
WHERE workspace_id = ?
ORDER BY updated_at DESC, name ASC`, workspaceID)
	if err != nil {
		return nil, fmt.Errorf("query terminal profiles: %w", err)
	}
	defer rows.Close()

	items := make([]Profile, 0)
	for rows.Next() {
		item, err := scanProfile(rows)
		if err != nil {
			return nil, err
		}
		items = append(items, item)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate terminal profiles: %w", err)
	}
	return items, nil
}

func (s *Store) GetProfile(ctx context.Context, id string) (Profile, error) {
	row := s.db.QueryRowContext(ctx, `
SELECT id, workspace_id, name, host, port, username, auth_type, password, private_key, passphrase, default_cwd, host_fingerprint, enabled, created_at, updated_at
FROM terminal_profiles
WHERE id = ?`, id)
	item, err := scanProfile(row)
	if errors.Is(err, sql.ErrNoRows) {
		return Profile{}, ErrNotFound
	}
	return item, err
}

func (s *Store) CreateProfile(ctx context.Context, input CreateProfileInput) (Profile, error) {
	now := time.Now()
	item := Profile{
		ID:              uid.New("tp"),
		WorkspaceID:     input.WorkspaceID,
		Name:            input.Name,
		Host:            input.Host,
		Port:            dbutil.WithDefaultInt(input.Port, 22),
		Username:        input.Username,
		AuthType:        dbutil.WithDefault(input.AuthType, AuthTypePassword),
		Password:        input.Password,
		PrivateKey:      input.PrivateKey,
		Passphrase:      input.Passphrase,
		DefaultCwd:      input.DefaultCwd,
		HostFingerprint: input.HostFingerprint,
		Enabled:         input.Enabled,
		CreatedAt:       now,
		UpdatedAt:       now,
	}
	_, err := s.db.ExecContext(ctx, `
INSERT INTO terminal_profiles (id, workspace_id, name, host, port, username, auth_type, password, private_key, passphrase, default_cwd, host_fingerprint, enabled, created_at, updated_at)
VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		item.ID, item.WorkspaceID, item.Name, item.Host, item.Port, item.Username, item.AuthType, item.Password, item.PrivateKey, item.Passphrase, item.DefaultCwd, item.HostFingerprint, dbutil.BoolInt(item.Enabled), dbutil.FormatTime(item.CreatedAt), dbutil.FormatTime(item.UpdatedAt),
	)
	if err != nil {
		return Profile{}, fmt.Errorf("insert terminal profile: %w", err)
	}
	return s.GetProfile(ctx, item.ID)
}

func (s *Store) EnsureLocalProfile(ctx context.Context, workspaceID string, defaultCwd string) (Profile, error) {
	if item, err := s.GetProfile(ctx, workspaceID); err == nil {
		return item, nil
	} else if !errors.Is(err, ErrNotFound) {
		return Profile{}, err
	}

	now := time.Now()
	item := Profile{
		ID:              workspaceID,
		WorkspaceID:     workspaceID,
		Name:            "本机终端",
		Host:            "localhost",
		Port:            22,
		Username:        localShellUsername(),
		AuthType:        AuthTypePassword,
		DefaultCwd:      strings.TrimSpace(defaultCwd),
		HostFingerprint: "",
		Enabled:         true,
		CreatedAt:       now,
		UpdatedAt:       now,
	}
	_, err := s.db.ExecContext(ctx, `
INSERT INTO terminal_profiles (id, workspace_id, name, host, port, username, auth_type, password, private_key, passphrase, default_cwd, host_fingerprint, enabled, created_at, updated_at)
VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		item.ID, item.WorkspaceID, item.Name, item.Host, item.Port, item.Username, item.AuthType, "", "", "", item.DefaultCwd, item.HostFingerprint, dbutil.BoolInt(item.Enabled), dbutil.FormatTime(item.CreatedAt), dbutil.FormatTime(item.UpdatedAt),
	)
	if err != nil {
		return Profile{}, fmt.Errorf("insert local terminal profile: %w", err)
	}
	return s.GetProfile(ctx, item.ID)
}

func (s *Store) UpdateProfile(ctx context.Context, id string, input UpdateProfileInput) (Profile, error) {
	current, err := s.GetProfile(ctx, id)
	if err != nil {
		return Profile{}, err
	}
	password := current.Password
	if input.Password != nil {
		password = *input.Password
	}
	privateKey := current.PrivateKey
	if input.PrivateKey != nil {
		privateKey = *input.PrivateKey
	}
	passphrase := current.Passphrase
	if input.Passphrase != nil {
		passphrase = *input.Passphrase
	}

	result, err := s.db.ExecContext(ctx, `
UPDATE terminal_profiles
SET name = ?, host = ?, port = ?, username = ?, auth_type = ?, password = ?, private_key = ?, passphrase = ?, default_cwd = ?, host_fingerprint = ?, enabled = ?, updated_at = ?
WHERE id = ?`,
		input.Name,
		input.Host,
		dbutil.WithDefaultInt(input.Port, 22),
		input.Username,
		dbutil.WithDefault(input.AuthType, AuthTypePassword),
		password,
		privateKey,
		passphrase,
		input.DefaultCwd,
		input.HostFingerprint,
		dbutil.BoolInt(input.Enabled),
		dbutil.FormatTime(time.Now()),
		id,
	)
	if err != nil {
		return Profile{}, fmt.Errorf("update terminal profile: %w", err)
	}
	affected, err := result.RowsAffected()
	if err != nil {
		return Profile{}, fmt.Errorf("read affected rows: %w", err)
	}
	if affected == 0 {
		return Profile{}, ErrNotFound
	}
	return s.GetProfile(ctx, id)
}

func (s *Store) DeleteProfile(ctx context.Context, id string) error {
	result, err := s.db.ExecContext(ctx, `DELETE FROM terminal_profiles WHERE id = ?`, id)
	if err != nil {
		return fmt.Errorf("delete terminal profile: %w", err)
	}
	affected, err := result.RowsAffected()
	if err != nil {
		return fmt.Errorf("read affected rows: %w", err)
	}
	if affected == 0 {
		return ErrNotFound
	}
	return nil
}

func (s *Store) CreateSession(ctx context.Context, input CreateSessionInput) (Session, error) {
	now := time.Now()
	item := Session{
		ID:          uid.New("ts"),
		WorkspaceID: input.WorkspaceID,
		ProfileID:   input.ProfileID,
		Status:      SessionStatusCreated,
		Cwd:         input.Cwd,
		Cols:        dbutil.WithDefaultInt(input.Cols, 100),
		Rows:        dbutil.WithDefaultInt(input.Rows, 30),
		CreatedAt:   now,
		UpdatedAt:   now,
	}
	_, err := s.db.ExecContext(ctx, `
INSERT INTO terminal_sessions (id, workspace_id, profile_id, status, cwd, cols, rows, created_at, updated_at, last_active_at, closed_at)
VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, NULL, NULL)`,
		item.ID, item.WorkspaceID, item.ProfileID, item.Status, item.Cwd, item.Cols, item.Rows, dbutil.FormatTime(item.CreatedAt), dbutil.FormatTime(item.UpdatedAt),
	)
	if err != nil {
		return Session{}, fmt.Errorf("insert terminal session: %w", err)
	}
	return s.GetSession(ctx, item.ID)
}

func (s *Store) GetSession(ctx context.Context, id string) (Session, error) {
	row := s.db.QueryRowContext(ctx, `
SELECT id, workspace_id, profile_id, status, cwd, cols, rows, created_at, updated_at, COALESCE(last_active_at, ''), COALESCE(closed_at, '')
FROM terminal_sessions
WHERE id = ?`, id)
	item, err := scanSession(row)
	if errors.Is(err, sql.ErrNoRows) {
		return Session{}, ErrNotFound
	}
	return item, err
}

func (s *Store) UpdateSessionStatus(ctx context.Context, id string, status string) error {
	var closedAt interface{}
	if status == SessionStatusClosed || status == SessionStatusError {
		closedAt = dbutil.FormatTime(time.Now())
	}
	result, err := s.db.ExecContext(ctx, `
UPDATE terminal_sessions
SET status = ?, updated_at = ?, last_active_at = ?, closed_at = COALESCE(?, closed_at)
WHERE id = ?`,
		status, dbutil.FormatTime(time.Now()), dbutil.FormatTime(time.Now()), closedAt, id,
	)
	if err != nil {
		return fmt.Errorf("update terminal session status: %w", err)
	}
	affected, err := result.RowsAffected()
	if err != nil {
		return fmt.Errorf("read affected rows: %w", err)
	}
	if affected == 0 {
		return ErrNotFound
	}
	return nil
}

type scanner interface {
	Scan(dest ...interface{}) error
}

func scanProfile(row scanner) (Profile, error) {
	var item Profile
	var enabled int
	var createdAt string
	var updatedAt string
	if err := row.Scan(&item.ID, &item.WorkspaceID, &item.Name, &item.Host, &item.Port, &item.Username, &item.AuthType, &item.Password, &item.PrivateKey, &item.Passphrase, &item.DefaultCwd, &item.HostFingerprint, &enabled, &createdAt, &updatedAt); err != nil {
		return Profile{}, err
	}
	item.PasswordConfigured = item.Password != ""
	item.PrivateKeyConfigured = item.PrivateKey != ""
	item.PassphraseConfigured = item.Passphrase != ""
	item.Enabled = enabled == 1
	item.CreatedAt = dbutil.ParseTime(createdAt)
	item.UpdatedAt = dbutil.ParseTime(updatedAt)
	return item, nil
}

func scanSession(row scanner) (Session, error) {
	var item Session
	var createdAt string
	var updatedAt string
	var lastActiveAt string
	var closedAt string
	if err := row.Scan(&item.ID, &item.WorkspaceID, &item.ProfileID, &item.Status, &item.Cwd, &item.Cols, &item.Rows, &createdAt, &updatedAt, &lastActiveAt, &closedAt); err != nil {
		return Session{}, err
	}
	item.CreatedAt = dbutil.ParseTime(createdAt)
	item.UpdatedAt = dbutil.ParseTime(updatedAt)
	if lastActiveAt != "" {
		parsed := dbutil.ParseTime(lastActiveAt)
		item.LastActiveAt = &parsed
	}
	if closedAt != "" {
		parsed := dbutil.ParseTime(closedAt)
		item.ClosedAt = &parsed
	}
	return item, nil
}

func localShellUsername() string {
	if current := strings.TrimSpace(os.Getenv("USER")); current != "" {
		return current
	}
	if current := strings.TrimSpace(os.Getenv("USERNAME")); current != "" {
		return current
	}
	return currentShellUsername()
}
