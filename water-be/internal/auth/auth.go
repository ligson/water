package auth

import (
	"context"
	cryptoRand "crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"database/sql"
	"encoding/hex"
	"errors"
	"fmt"
	"math/big"
	"strings"
	"time"

	"github.com/ligson/water/water-be/internal/dbutil"
)

var ErrNotConfigured = errors.New("auth not configured")

const (
	defaultSessionTTL = 30 * 24 * time.Hour
	stateRowID        = 1
)

type Store struct {
	db *sql.DB
}

type Status struct {
	Configured       bool
	Authenticated    bool
	SessionExpiresAt time.Time
	LastUnlockedAt   time.Time
}

type Session struct {
	Token          string
	ExpiresAt      time.Time
	LastUnlockedAt time.Time
}

func NewStore(db *sql.DB) *Store {
	return &Store{db: db}
}

func (s *Store) Ensure(ctx context.Context, bootstrapPin string) (string, error) {
	state, err := s.loadState(ctx)
	if err != nil {
		if !errors.Is(err, sql.ErrNoRows) {
			return "", err
		}
		return s.createState(ctx, bootstrapPin)
	}
	if bootstrapPin != "" {
		if err := s.updatePIN(ctx, state, bootstrapPin); err != nil {
			return "", err
		}
	}
	return "", nil
}

func (s *Store) Status(ctx context.Context, token string) (Status, error) {
	state, err := s.loadState(ctx)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return Status{}, ErrNotConfigured
		}
		return Status{}, err
	}

	status := Status{Configured: true}
	if token == "" {
		return status, nil
	}
	valid, expiresAt, lastUnlockedAt, err := s.validateToken(ctx, state, token)
	if err != nil {
		return Status{}, err
	}
	status.Authenticated = valid
	status.SessionExpiresAt = expiresAt
	status.LastUnlockedAt = lastUnlockedAt
	return status, nil
}

func (s *Store) Unlock(ctx context.Context, pin string) (Session, error) {
	state, err := s.loadState(ctx)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return Session{}, ErrNotConfigured
		}
		return Session{}, err
	}
	if !comparePIN(state.PinHash, state.PinSalt, pin) {
		return Session{}, errors.New("pin incorrect")
	}
	return s.issueSession(ctx, state)
}

func (s *Store) Lock(ctx context.Context, token string) error {
	state, err := s.loadState(ctx)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return ErrNotConfigured
		}
		return err
	}
	_, err = s.clearSession(ctx, state)
	return err
}

func (s *Store) ChangePIN(ctx context.Context, currentPIN, newPIN string) (Session, error) {
	state, err := s.loadState(ctx)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return Session{}, ErrNotConfigured
		}
		return Session{}, err
	}
	if !comparePIN(state.PinHash, state.PinSalt, currentPIN) {
		return Session{}, errors.New("pin incorrect")
	}
	state.PinSalt = randomSalt()
	state.PinHash = hashPIN(state.PinSalt, newPIN)
	if _, err := s.persistState(ctx, state); err != nil {
		return Session{}, err
	}
	return s.issueSession(ctx, state)
}

func (s *Store) ValidateToken(ctx context.Context, token string) (bool, error) {
	state, err := s.loadState(ctx)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return false, ErrNotConfigured
		}
		return false, err
	}
	valid, _, _, err := s.validateToken(ctx, state, token)
	return valid, err
}

func (s *Store) loadState(ctx context.Context) (stateRecord, error) {
	var state stateRecord
	row := s.db.QueryRowContext(ctx, `
SELECT id, pin_hash, pin_salt, COALESCE(session_token_hash, ''), COALESCE(session_expires_at, ''), COALESCE(session_issued_at, ''), COALESCE(last_unlocked_at, ''), created_at, updated_at
FROM auth_state
WHERE id = ?`, stateRowID)
	if err := row.Scan(
		&state.ID,
		&state.PinHash,
		&state.PinSalt,
		&state.SessionTokenHash,
		&state.SessionExpiresAtRaw,
		&state.SessionIssuedAtRaw,
		&state.LastUnlockedAtRaw,
		&state.CreatedAtRaw,
		&state.UpdatedAtRaw,
	); err != nil {
		return stateRecord{}, err
	}
	return state, nil
}

func (s *Store) createState(ctx context.Context, bootstrapPin string) (string, error) {
	generated := bootstrapPin == ""
	if bootstrapPin == "" {
		bootstrapPin = generatePIN()
	}
	now := time.Now()
	state := stateRecord{
		ID:           stateRowID,
		PinSalt:      randomSalt(),
		CreatedAtRaw: dbutil.FormatTime(now),
		UpdatedAtRaw: dbutil.FormatTime(now),
	}
	state.PinHash = hashPIN(state.PinSalt, bootstrapPin)
	if _, err := s.db.ExecContext(ctx, `
INSERT INTO auth_state (id, pin_hash, pin_salt, session_token_hash, session_expires_at, session_issued_at, last_unlocked_at, created_at, updated_at)
VALUES (?, ?, ?, '', '', '', '', ?, ?)`,
		state.ID, state.PinHash, state.PinSalt, state.CreatedAtRaw, state.UpdatedAtRaw); err != nil {
		return "", fmt.Errorf("insert auth state: %w", err)
	}
	if generated {
		return bootstrapPin, nil
	}
	return "", nil
}

func (s *Store) updatePIN(ctx context.Context, state stateRecord, newPIN string) error {
	state.PinSalt = randomSalt()
	state.PinHash = hashPIN(state.PinSalt, newPIN)
	state.SessionTokenHash = ""
	state.SessionExpiresAtRaw = ""
	state.SessionIssuedAtRaw = ""
	state.LastUnlockedAtRaw = ""
	_, err := s.persistState(ctx, state)
	return err
}

func (s *Store) issueSession(ctx context.Context, state stateRecord) (Session, error) {
	now := time.Now()
	token := generateToken()
	tokenHash := hashToken(token)
	expiresAt := now.Add(defaultSessionTTL)
	state.SessionTokenHash = tokenHash
	state.SessionExpiresAtRaw = dbutil.FormatTime(expiresAt)
	state.SessionIssuedAtRaw = dbutil.FormatTime(now)
	state.LastUnlockedAtRaw = dbutil.FormatTime(now)
	if _, err := s.persistState(ctx, state); err != nil {
		return Session{}, err
	}
	return Session{
		Token:          token,
		ExpiresAt:      expiresAt,
		LastUnlockedAt: now,
	}, nil
}

func (s *Store) clearSession(ctx context.Context, state stateRecord) (stateRecord, error) {
	state.SessionTokenHash = ""
	state.SessionExpiresAtRaw = ""
	state.SessionIssuedAtRaw = ""
	state.LastUnlockedAtRaw = ""
	return s.persistState(ctx, state)
}

func (s *Store) validateToken(ctx context.Context, state stateRecord, token string) (bool, time.Time, time.Time, error) {
	if token == "" || state.SessionTokenHash == "" {
		return false, time.Time{}, time.Time{}, nil
	}
	if subtle.ConstantTimeCompare([]byte(hashToken(token)), []byte(state.SessionTokenHash)) != 1 {
		return false, time.Time{}, time.Time{}, nil
	}
	expiresAt := dbutil.ParseTime(state.SessionExpiresAtRaw)
	if !expiresAt.IsZero() && time.Now().After(expiresAt) {
		return false, time.Time{}, time.Time{}, nil
	}
	return true, expiresAt, dbutil.ParseTime(state.LastUnlockedAtRaw), nil
}

func (s *Store) persistState(ctx context.Context, state stateRecord) (stateRecord, error) {
	now := dbutil.FormatTime(time.Now())
	_, err := s.db.ExecContext(ctx, `
UPDATE auth_state
SET pin_hash = ?, pin_salt = ?, session_token_hash = ?, session_expires_at = ?, session_issued_at = ?, last_unlocked_at = ?, updated_at = ?
WHERE id = ?`,
		state.PinHash,
		state.PinSalt,
		state.SessionTokenHash,
		state.SessionExpiresAtRaw,
		state.SessionIssuedAtRaw,
		state.LastUnlockedAtRaw,
		now,
		state.ID,
	)
	if err != nil {
		return stateRecord{}, fmt.Errorf("update auth state: %w", err)
	}
	return state, nil
}

type stateRecord struct {
	ID                  int
	PinHash             string
	PinSalt             string
	SessionTokenHash    string
	SessionExpiresAtRaw string
	SessionIssuedAtRaw  string
	LastUnlockedAtRaw   string
	CreatedAtRaw        string
	UpdatedAtRaw        string
}

func randomSalt() string {
	return randomHex(16)
}

func randomHex(bytes int) string {
	buf := make([]byte, bytes)
	if _, err := cryptoRand.Read(buf); err != nil {
		panic(fmt.Errorf("random bytes: %w", err))
	}
	return hex.EncodeToString(buf)
}

func generatePIN() string {
	n, err := cryptoRand.Int(cryptoRand.Reader, big.NewInt(900000))
	if err != nil {
		panic(fmt.Errorf("generate pin: %w", err))
	}
	return fmt.Sprintf("%06d", n.Int64()+100000)
}

func generateToken() string {
	return randomHex(32)
}

func hashPIN(salt, pin string) string {
	sum := sha256.Sum256([]byte(salt + ":" + strings.TrimSpace(pin)))
	return hex.EncodeToString(sum[:])
}

func comparePIN(expectedHash, salt, pin string) bool {
	return subtle.ConstantTimeCompare([]byte(expectedHash), []byte(hashPIN(salt, pin))) == 1
}

func hashToken(token string) string {
	sum := sha256.Sum256([]byte("water-auth-token:" + strings.TrimSpace(token)))
	return hex.EncodeToString(sum[:])
}
