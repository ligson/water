package provider

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
	TypeOpenAICompatible       = "openai-compatible"
	DefaultContextWindowTokens = 8192
)

var ErrNotFound = errors.New("provider not found")

type Provider struct {
	ID                  string    `json:"id"`
	Name                string    `json:"name"`
	Type                string    `json:"type"`
	BaseURL             string    `json:"baseUrl"`
	Model               string    `json:"model"`
	APIKey              string    `json:"-"`
	APIKeyConfigured    bool      `json:"apiKeyConfigured"`
	IsDefault           bool      `json:"isDefault"`
	Enabled             bool      `json:"enabled"`
	ContextWindowTokens int       `json:"contextWindowTokens"`
	TimeoutMS           int       `json:"timeoutMs"`
	MaxRetries          int       `json:"maxRetries"`
	StreamIdleTimeoutMS int       `json:"streamIdleTimeoutMs"`
	HeadersJSON         string    `json:"headersJson"`
	CreatedAt           time.Time `json:"createdAt"`
	UpdatedAt           time.Time `json:"updatedAt"`
}

type Store struct {
	db *sql.DB
}

func NewStore(db *sql.DB) *Store {
	return &Store{db: db}
}

type CreateInput struct {
	Name                string
	Type                string
	BaseURL             string
	Model               string
	APIKey              string
	IsDefault           bool
	Enabled             bool
	ContextWindowTokens int
	TimeoutMS           int
	MaxRetries          int
	StreamIdleTimeoutMS int
	HeadersJSON         string
}

type UpdateInput struct {
	Name                string
	Type                string
	BaseURL             string
	Model               string
	APIKey              *string
	IsDefault           bool
	Enabled             bool
	ContextWindowTokens int
	TimeoutMS           int
	MaxRetries          int
	StreamIdleTimeoutMS int
	HeadersJSON         string
}

func (s *Store) List(ctx context.Context) ([]Provider, error) {
	rows, err := s.db.QueryContext(ctx, `
SELECT id, name, type, base_url, model, api_key, is_default, enabled, context_window_tokens, timeout_ms, max_retries, stream_idle_timeout_ms, headers_json, created_at, updated_at
FROM providers
ORDER BY is_default DESC, updated_at DESC, name ASC`)
	if err != nil {
		return nil, fmt.Errorf("query providers: %w", err)
	}
	defer rows.Close()

	providers := make([]Provider, 0)
	for rows.Next() {
		p, err := scanProvider(rows)
		if err != nil {
			return nil, err
		}
		providers = append(providers, p)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate providers: %w", err)
	}
	return providers, nil
}

func (s *Store) Get(ctx context.Context, id string) (Provider, error) {
	row := s.db.QueryRowContext(ctx, `
SELECT id, name, type, base_url, model, api_key, is_default, enabled, context_window_tokens, timeout_ms, max_retries, stream_idle_timeout_ms, headers_json, created_at, updated_at
FROM providers
WHERE id = ?`, id)

	p, err := scanProvider(row)
	if errors.Is(err, sql.ErrNoRows) {
		return Provider{}, ErrNotFound
	}
	return p, err
}

func (s *Store) GetDefault(ctx context.Context) (Provider, error) {
	row := s.db.QueryRowContext(ctx, `
SELECT id, name, type, base_url, model, api_key, is_default, enabled, context_window_tokens, timeout_ms, max_retries, stream_idle_timeout_ms, headers_json, created_at, updated_at
FROM providers
WHERE is_default = 1
LIMIT 1`)

	p, err := scanProvider(row)
	if errors.Is(err, sql.ErrNoRows) {
		return Provider{}, ErrNotFound
	}
	return p, err
}

func (s *Store) Create(ctx context.Context, input CreateInput) (Provider, error) {
	now := time.Now()
	p := Provider{
		ID:                  uid.New("prov"),
		Name:                input.Name,
		Type:                dbutil.WithDefault(input.Type, TypeOpenAICompatible),
		BaseURL:             input.BaseURL,
		Model:               input.Model,
		APIKey:              input.APIKey,
		APIKeyConfigured:    input.APIKey != "",
		IsDefault:           input.IsDefault,
		Enabled:             input.Enabled,
		ContextWindowTokens: dbutil.WithDefaultInt(input.ContextWindowTokens, DefaultContextWindowTokens),
		TimeoutMS:           dbutil.WithDefaultInt(input.TimeoutMS, 30000),
		MaxRetries:          dbutil.WithDefaultInt(input.MaxRetries, 2),
		StreamIdleTimeoutMS: dbutil.WithDefaultInt(input.StreamIdleTimeoutMS, 60000),
		HeadersJSON:         dbutil.WithDefault(input.HeadersJSON, "{}"),
		CreatedAt:           now,
		UpdatedAt:           now,
	}

	if err := s.withTx(ctx, func(tx *sql.Tx) error {
		if p.IsDefault {
			if err := clearDefault(ctx, tx); err != nil {
				return err
			}
		}
		_, err := tx.ExecContext(ctx, `
INSERT INTO providers (id, name, type, base_url, model, api_key, is_default, enabled, context_window_tokens, timeout_ms, max_retries, stream_idle_timeout_ms, headers_json, created_at, updated_at)
VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
			p.ID, p.Name, p.Type, p.BaseURL, p.Model, p.APIKey, dbutil.BoolInt(p.IsDefault), dbutil.BoolInt(p.Enabled), p.ContextWindowTokens, p.TimeoutMS, p.MaxRetries, p.StreamIdleTimeoutMS, p.HeadersJSON, dbutil.FormatTime(p.CreatedAt), dbutil.FormatTime(p.UpdatedAt))
		if err != nil {
			return fmt.Errorf("insert provider: %w", err)
		}
		return nil
	}); err != nil {
		return Provider{}, err
	}

	return s.Get(ctx, p.ID)
}

func (s *Store) Update(ctx context.Context, id string, input UpdateInput) (Provider, error) {
	current, err := s.Get(ctx, id)
	if err != nil {
		return Provider{}, err
	}

	apiKey := current.APIKey
	if input.APIKey != nil {
		apiKey = *input.APIKey
	}

	updatedAt := time.Now()
	if err := s.withTx(ctx, func(tx *sql.Tx) error {
		if input.IsDefault {
			if err := clearDefault(ctx, tx); err != nil {
				return err
			}
		}
		result, err := tx.ExecContext(ctx, `
UPDATE providers
SET name = ?, type = ?, base_url = ?, model = ?, api_key = ?, is_default = ?, enabled = ?, context_window_tokens = ?, timeout_ms = ?, max_retries = ?, stream_idle_timeout_ms = ?, headers_json = ?, updated_at = ?
WHERE id = ?`,
			input.Name,
			dbutil.WithDefault(input.Type, TypeOpenAICompatible),
			input.BaseURL,
			input.Model,
			apiKey,
			dbutil.BoolInt(input.IsDefault),
			dbutil.BoolInt(input.Enabled),
			dbutil.WithDefaultInt(input.ContextWindowTokens, DefaultContextWindowTokens),
			dbutil.WithDefaultInt(input.TimeoutMS, 30000),
			dbutil.WithDefaultInt(input.MaxRetries, 2),
			dbutil.WithDefaultInt(input.StreamIdleTimeoutMS, 60000),
			dbutil.WithDefault(input.HeadersJSON, "{}"),
			dbutil.FormatTime(updatedAt),
			id,
		)
		if err != nil {
			return fmt.Errorf("update provider: %w", err)
		}
		affected, err := result.RowsAffected()
		if err != nil {
			return fmt.Errorf("read affected rows: %w", err)
		}
		if affected == 0 {
			return ErrNotFound
		}
		return nil
	}); err != nil {
		return Provider{}, err
	}

	return s.Get(ctx, id)
}

func (s *Store) Delete(ctx context.Context, id string) error {
	result, err := s.db.ExecContext(ctx, `DELETE FROM providers WHERE id = ?`, id)
	if err != nil {
		return fmt.Errorf("delete provider: %w", err)
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

func (s *Store) SetDefault(ctx context.Context, id string) (Provider, error) {
	if _, err := s.Get(ctx, id); err != nil {
		return Provider{}, err
	}

	if err := s.withTx(ctx, func(tx *sql.Tx) error {
		if err := clearDefault(ctx, tx); err != nil {
			return err
		}
		result, err := tx.ExecContext(ctx, `UPDATE providers SET is_default = 1, updated_at = ? WHERE id = ?`, dbutil.FormatTime(time.Now()), id)
		if err != nil {
			return fmt.Errorf("set default provider: %w", err)
		}
		affected, err := result.RowsAffected()
		if err != nil {
			return fmt.Errorf("read affected rows: %w", err)
		}
		if affected == 0 {
			return ErrNotFound
		}
		return nil
	}); err != nil {
		return Provider{}, err
	}

	return s.Get(ctx, id)
}

func (s *Store) withTx(ctx context.Context, fn func(*sql.Tx) error) error {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("begin transaction: %w", err)
	}

	if err := fn(tx); err != nil {
		_ = tx.Rollback()
		return err
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("commit transaction: %w", err)
	}
	return nil
}

type scanner interface {
	Scan(dest ...interface{}) error
}

func scanProvider(row scanner) (Provider, error) {
	var p Provider
	var isDefault int
	var enabled int
	var createdAt string
	var updatedAt string
	if err := row.Scan(&p.ID, &p.Name, &p.Type, &p.BaseURL, &p.Model, &p.APIKey, &isDefault, &enabled, &p.ContextWindowTokens, &p.TimeoutMS, &p.MaxRetries, &p.StreamIdleTimeoutMS, &p.HeadersJSON, &createdAt, &updatedAt); err != nil {
		return Provider{}, err
	}
	if p.ContextWindowTokens <= 0 {
		p.ContextWindowTokens = DefaultContextWindowTokens
	}
	p.APIKeyConfigured = p.APIKey != ""
	p.IsDefault = isDefault == 1
	p.Enabled = enabled == 1
	p.CreatedAt = dbutil.ParseTime(createdAt)
	p.UpdatedAt = dbutil.ParseTime(updatedAt)
	return p, nil
}

func clearDefault(ctx context.Context, tx *sql.Tx) error {
	if _, err := tx.ExecContext(ctx, `UPDATE providers SET is_default = 0 WHERE is_default = 1`); err != nil {
		return fmt.Errorf("clear default provider: %w", err)
	}
	return nil
}
