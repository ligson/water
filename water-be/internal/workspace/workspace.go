package workspace

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"time"

	"github.com/ligson/water/water-be/internal/dbutil"
	"github.com/ligson/water/water-be/internal/uid"
)

const (
	PermissionModeRequestApproval = "request_approval"
	PermissionModeFullAccess      = "full_access"

	PathTypeFile      = "file"
	PathTypeDirectory = "directory"

	AccessModeRead  = "read"
	AccessModeWrite = "write"
)

var ErrNotFound = errors.New("workspace not found")

type Workspace struct {
	ID                string    `json:"id"`
	Name              string    `json:"name"`
	RootPath          string    `json:"rootPath"`
	DefaultProviderID string    `json:"defaultProviderId,omitempty"`
	PermissionMode    string    `json:"permissionMode"`
	Trusted           bool      `json:"trusted"`
	CreatedAt         time.Time `json:"createdAt"`
	UpdatedAt         time.Time `json:"updatedAt"`
	LastOpenedAt      time.Time `json:"lastOpenedAt,omitempty"`
}

type ExternalPath struct {
	ID           string     `json:"id"`
	WorkspaceID  string     `json:"workspaceId"`
	Path         string     `json:"path"`
	PathType     string     `json:"pathType"`
	AccessMode   string     `json:"accessMode"`
	SourceTaskID string     `json:"sourceTaskId,omitempty"`
	CreatedAt    time.Time  `json:"createdAt"`
	LastUsedAt   *time.Time `json:"lastUsedAt,omitempty"`
}

type Store struct {
	db *sql.DB
}

func NewStore(db *sql.DB) *Store {
	return &Store{db: db}
}

type CreateInput struct {
	Name              string
	RootPath          string
	DefaultProviderID string
	PermissionMode    string
	Trusted           bool
}

type UpdateInput struct {
	Name              string
	RootPath          string
	DefaultProviderID string
	PermissionMode    string
	Trusted           bool
}

type CreateExternalPathInput struct {
	Path         string
	PathType     string
	AccessMode   string
	SourceTaskID string
}

func (s *Store) List(ctx context.Context) ([]Workspace, error) {
	rows, err := s.db.QueryContext(ctx, `
SELECT id, name, root_path, COALESCE(default_provider_id, ''), permission_mode, trusted, created_at, updated_at, COALESCE(last_opened_at, '')
FROM workspaces
ORDER BY last_opened_at DESC, updated_at DESC, name ASC`)
	if err != nil {
		return nil, fmt.Errorf("query workspaces: %w", err)
	}
	defer rows.Close()

	workspaces := make([]Workspace, 0)
	for rows.Next() {
		w, err := scanWorkspace(rows)
		if err != nil {
			return nil, err
		}
		workspaces = append(workspaces, w)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate workspaces: %w", err)
	}
	return workspaces, nil
}

func (s *Store) Get(ctx context.Context, id string) (Workspace, error) {
	row := s.db.QueryRowContext(ctx, `
SELECT id, name, root_path, COALESCE(default_provider_id, ''), permission_mode, trusted, created_at, updated_at, COALESCE(last_opened_at, '')
FROM workspaces
WHERE id = ?`, id)

	w, err := scanWorkspace(row)
	if errors.Is(err, sql.ErrNoRows) {
		return Workspace{}, ErrNotFound
	}
	return w, err
}

func (s *Store) Create(ctx context.Context, input CreateInput) (Workspace, error) {
	now := time.Now()
	w := Workspace{
		ID:                uid.New("ws"),
		Name:              input.Name,
		RootPath:          input.RootPath,
		DefaultProviderID: input.DefaultProviderID,
		PermissionMode:    dbutil.WithDefault(input.PermissionMode, PermissionModeRequestApproval),
		Trusted:           input.Trusted,
		CreatedAt:         now,
		UpdatedAt:         now,
		LastOpenedAt:      now,
	}

	_, err := s.db.ExecContext(ctx, `
INSERT INTO workspaces (id, name, root_path, default_provider_id, permission_mode, trusted, created_at, updated_at, last_opened_at)
VALUES (?, ?, ?, NULLIF(?, ''), ?, ?, ?, ?, ?)`,
		w.ID,
		w.Name,
		w.RootPath,
		w.DefaultProviderID,
		w.PermissionMode,
		dbutil.BoolInt(w.Trusted),
		dbutil.FormatTime(w.CreatedAt),
		dbutil.FormatTime(w.UpdatedAt),
		dbutil.FormatTime(w.LastOpenedAt),
	)
	if err != nil {
		return Workspace{}, fmt.Errorf("insert workspace: %w", err)
	}

	return s.Get(ctx, w.ID)
}

func (s *Store) Update(ctx context.Context, id string, input UpdateInput) (Workspace, error) {
	result, err := s.db.ExecContext(ctx, `
UPDATE workspaces
SET name = ?, root_path = ?, default_provider_id = NULLIF(?, ''), permission_mode = ?, trusted = ?, updated_at = ?
WHERE id = ?`,
		input.Name,
		input.RootPath,
		input.DefaultProviderID,
		dbutil.WithDefault(input.PermissionMode, PermissionModeRequestApproval),
		dbutil.BoolInt(input.Trusted),
		dbutil.FormatTime(time.Now()),
		id,
	)
	if err != nil {
		return Workspace{}, fmt.Errorf("update workspace: %w", err)
	}
	affected, err := result.RowsAffected()
	if err != nil {
		return Workspace{}, fmt.Errorf("read affected rows: %w", err)
	}
	if affected == 0 {
		return Workspace{}, ErrNotFound
	}

	return s.Get(ctx, id)
}

func (s *Store) Delete(ctx context.Context, id string) error {
	result, err := s.db.ExecContext(ctx, `DELETE FROM workspaces WHERE id = ?`, id)
	if err != nil {
		return fmt.Errorf("delete workspace: %w", err)
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

func (s *Store) ListExternalPaths(ctx context.Context, workspaceID string) ([]ExternalPath, error) {
	if _, err := s.Get(ctx, workspaceID); err != nil {
		return nil, err
	}

	rows, err := s.db.QueryContext(ctx, `
SELECT id, workspace_id, path, path_type, access_mode, COALESCE(source_task_id, ''), created_at, COALESCE(last_used_at, '')
FROM workspace_external_paths
WHERE workspace_id = ?
ORDER BY created_at DESC`, workspaceID)
	if err != nil {
		return nil, fmt.Errorf("query external paths: %w", err)
	}
	defer rows.Close()

	paths := make([]ExternalPath, 0)
	for rows.Next() {
		p, err := scanExternalPath(rows)
		if err != nil {
			return nil, err
		}
		paths = append(paths, p)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate external paths: %w", err)
	}
	return paths, nil
}

func (s *Store) CreateExternalPath(ctx context.Context, workspaceID string, input CreateExternalPathInput) (ExternalPath, error) {
	if _, err := s.Get(ctx, workspaceID); err != nil {
		return ExternalPath{}, err
	}

	now := time.Now()
	path := ExternalPath{
		ID:           uid.New("wsep"),
		WorkspaceID:  workspaceID,
		Path:         input.Path,
		PathType:     input.PathType,
		AccessMode:   input.AccessMode,
		SourceTaskID: input.SourceTaskID,
		CreatedAt:    now,
	}

	_, err := s.db.ExecContext(ctx, `
INSERT INTO workspace_external_paths (id, workspace_id, path, path_type, access_mode, source_task_id, created_at, last_used_at)
VALUES (?, ?, ?, ?, ?, NULLIF(?, ''), ?, NULL)`,
		path.ID,
		path.WorkspaceID,
		path.Path,
		path.PathType,
		path.AccessMode,
		path.SourceTaskID,
		dbutil.FormatTime(path.CreatedAt),
	)
	if err != nil {
		return ExternalPath{}, fmt.Errorf("insert external path: %w", err)
	}

	return s.GetExternalPath(ctx, workspaceID, path.ID)
}

func (s *Store) GetExternalPath(ctx context.Context, workspaceID string, id string) (ExternalPath, error) {
	row := s.db.QueryRowContext(ctx, `
SELECT id, workspace_id, path, path_type, access_mode, COALESCE(source_task_id, ''), created_at, COALESCE(last_used_at, '')
FROM workspace_external_paths
WHERE workspace_id = ? AND id = ?`, workspaceID, id)

	path, err := scanExternalPath(row)
	if errors.Is(err, sql.ErrNoRows) {
		return ExternalPath{}, ErrNotFound
	}
	return path, err
}

func (s *Store) DeleteExternalPath(ctx context.Context, workspaceID string, id string) error {
	result, err := s.db.ExecContext(ctx, `DELETE FROM workspace_external_paths WHERE workspace_id = ? AND id = ?`, workspaceID, id)
	if err != nil {
		return fmt.Errorf("delete external path: %w", err)
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

func scanWorkspace(row scanner) (Workspace, error) {
	var w Workspace
	var trusted int
	var createdAt string
	var updatedAt string
	var lastOpenedAt string
	if err := row.Scan(&w.ID, &w.Name, &w.RootPath, &w.DefaultProviderID, &w.PermissionMode, &trusted, &createdAt, &updatedAt, &lastOpenedAt); err != nil {
		return Workspace{}, err
	}
	w.Trusted = trusted == 1
	w.CreatedAt = dbutil.ParseTime(createdAt)
	w.UpdatedAt = dbutil.ParseTime(updatedAt)
	w.LastOpenedAt = dbutil.ParseTime(lastOpenedAt)
	return w, nil
}

func scanExternalPath(row scanner) (ExternalPath, error) {
	var p ExternalPath
	var createdAt string
	var lastUsedAt string
	if err := row.Scan(&p.ID, &p.WorkspaceID, &p.Path, &p.PathType, &p.AccessMode, &p.SourceTaskID, &createdAt, &lastUsedAt); err != nil {
		return ExternalPath{}, err
	}
	p.CreatedAt = dbutil.ParseTime(createdAt)
	if lastUsedAt != "" {
		parsed := dbutil.ParseTime(lastUsedAt)
		p.LastUsedAt = &parsed
	}
	return p, nil
}
