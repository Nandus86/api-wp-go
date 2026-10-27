package auth

import (
	"context"
	"crypto/rand"
	"database/sql"
	"encoding/hex"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"time"

	"github.com/lib/pq"
)

var (
	ErrInvalidKey   = errors.New("invalid or expired API key")
	ErrNotFound     = errors.New("api key not found")
	ErrLastAdmin    = errors.New("cannot revoke, delete or downgrade the last active admin key")
	ErrInvalidInput = errors.New("invalid input")
)

// InstanceLookup resolves a per-instance api_key to its instance id.
type InstanceLookup func(token string) (instanceID string, name string, ok bool)

type APIKey struct {
	ID               string     `json:"id"`
	Name             string     `json:"name"`
	Prefix           string     `json:"prefix"`
	Role             Role       `json:"role"`
	AllowedInstances []string   `json:"allowed_instances"`
	CreatedBy        string     `json:"created_by,omitempty"`
	CreatedAt        time.Time  `json:"created_at"`
	LastUsedAt       *time.Time `json:"last_used_at,omitempty"`
	ExpiresAt        *time.Time `json:"expires_at,omitempty"`
	RevokedAt        *time.Time `json:"revoked_at,omitempty"`
}

func (k *APIKey) active(now time.Time) bool {
	return k.RevokedAt == nil && (k.ExpiresAt == nil || k.ExpiresAt.After(now))
}

type cacheEntry struct {
	key      *APIKey
	fetched  time.Time
	lastSeen time.Time
}

type Service struct {
	db          *sql.DB
	lookup      InstanceLookup
	envAdminKey string

	mu    sync.Mutex
	cache map[string]*cacheEntry // by key hash
}

const cacheTTL = 30 * time.Second

func NewService(db *sql.DB, envAdminKey string, lookup InstanceLookup) *Service {
	return &Service{db: db, lookup: lookup, envAdminKey: envAdminKey, cache: map[string]*cacheEntry{}}
}

// Authenticate resolves a token to a principal.
func (s *Service) Authenticate(ctx context.Context, token string) (*Principal, error) {
	if token == "" {
		return nil, ErrInvalidKey
	}
	if s.envAdminKey != "" && constantTimeEqual(token, s.envAdminKey) {
		return &Principal{Kind: KindKey, ID: "env-admin", Name: "ADMIN_API_KEY", Role: RoleAdmin}, nil
	}

	hash := HashKey(token)
	key, err := s.lookupHash(ctx, hash)
	if err == nil && key != nil {
		if !key.active(time.Now()) {
			return nil, ErrInvalidKey
		}
		return &Principal{Kind: KindKey, ID: key.ID, Name: key.Name, Role: key.Role, AllowedInstances: key.AllowedInstances}, nil
	}
	if err != nil && !errors.Is(err, sql.ErrNoRows) {
		return nil, err
	}

	if s.lookup != nil {
		if id, name, ok := s.lookup(token); ok {
			return &Principal{Kind: KindInstance, ID: id, Name: name, Role: RoleInstance, InstanceID: id}, nil
		}
	}
	return nil, ErrInvalidKey
}

func (s *Service) lookupHash(ctx context.Context, hash string) (*APIKey, error) {
	now := time.Now()
	s.mu.Lock()
	if e, ok := s.cache[hash]; ok && now.Sub(e.fetched) < cacheTTL {
		touch := now.Sub(e.lastSeen) > time.Minute
		if touch {
			e.lastSeen = now
		}
		s.mu.Unlock()
		if e.key != nil && touch {
			go s.touch(e.key.ID)
		}
		if e.key == nil {
			return nil, sql.ErrNoRows
		}
		return e.key, nil
	}
	s.mu.Unlock()

	key, err := s.getBy(ctx, "key_hash", hash)
	if err != nil && !errors.Is(err, sql.ErrNoRows) {
		return nil, err
	}
	s.mu.Lock()
	// Negative results are cached too, so random invalid tokens cannot hammer the database.
	s.cache[hash] = &cacheEntry{key: key, fetched: now, lastSeen: now}
	if len(s.cache) > 10000 {
		s.cache = map[string]*cacheEntry{}
	}
	s.mu.Unlock()
	if key != nil {
		go s.touch(key.ID)
		return key, nil
	}
	return nil, sql.ErrNoRows
}

func (s *Service) touch(id string) {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	_, _ = s.db.ExecContext(ctx, `UPDATE api_keys SET last_used_at = NOW() WHERE id = $1`, id)
}

func (s *Service) invalidate() {
	s.mu.Lock()
	s.cache = map[string]*cacheEntry{}
	s.mu.Unlock()
}

const keyColumns = `id, name, key_prefix, role, allowed_instances, COALESCE(created_by, ''), created_at, last_used_at, expires_at, revoked_at`

func scanKey(row interface{ Scan(...any) error }) (*APIKey, error) {
	var k APIKey
	var role string
	var allowed pq.StringArray
	var lastUsed, expires, revoked sql.NullTime
	if err := row.Scan(&k.ID, &k.Name, &k.Prefix, &role, &allowed, &k.CreatedBy, &k.CreatedAt, &lastUsed, &expires, &revoked); err != nil {
		return nil, err
	}
	k.Role = Role(role)
	k.AllowedInstances = []string(allowed)
	if k.AllowedInstances == nil {
		k.AllowedInstances = []string{}
	}
	if lastUsed.Valid {
		k.LastUsedAt = &lastUsed.Time
	}
	if expires.Valid {
		k.ExpiresAt = &expires.Time
	}
	if revoked.Valid {
		k.RevokedAt = &revoked.Time
	}
	return &k, nil
}

func (s *Service) getBy(ctx context.Context, column, value string) (*APIKey, error) {
	return scanKey(s.db.QueryRowContext(ctx, `SELECT `+keyColumns+` FROM api_keys WHERE `+column+` = $1`, value))
}

func (s *Service) Get(ctx context.Context, id string) (*APIKey, error) {
	k, err := s.getBy(ctx, "id", id)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, ErrNotFound
	}
	return k, err
}

func (s *Service) List(ctx context.Context) ([]APIKey, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT `+keyColumns+` FROM api_keys ORDER BY created_at DESC`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []APIKey{}
	for rows.Next() {
		k, err := scanKey(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, *k)
	}
	return out, rows.Err()
}

type CreateKeyInput struct {
	Name             string     `json:"name"`
	Role             Role       `json:"role"`
	AllowedInstances []string   `json:"allowed_instances"`
	ExpiresAt        *time.Time `json:"expires_at,omitempty"`
}

// Create stores a new key and returns it together with the plaintext (shown only once).
func (s *Service) Create(ctx context.Context, in CreateKeyInput, createdBy string) (*APIKey, string, error) {
	if in.Name == "" || !in.Role.Valid() {
		return nil, "", fmt.Errorf("%w: name and a valid role (admin, operator, viewer) are required", ErrInvalidInput)
	}
	if in.AllowedInstances == nil {
		in.AllowedInstances = []string{}
	}
	plain, prefix := GenerateKey()
	id := newID()
	_, err := s.db.ExecContext(ctx, `INSERT INTO api_keys (id, name, key_prefix, key_hash, role, allowed_instances, created_by, expires_at)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8)`,
		id, in.Name, prefix, HashKey(plain), string(in.Role), pq.Array(in.AllowedInstances), createdBy, in.ExpiresAt)
	if err != nil {
		return nil, "", err
	}
	s.invalidate()
	k, err := s.Get(ctx, id)
	return k, plain, err
}

type UpdateKeyInput struct {
	Name             *string    `json:"name,omitempty"`
	Role             *Role      `json:"role,omitempty"`
	AllowedInstances *[]string  `json:"allowed_instances,omitempty"`
	ExpiresAt        *time.Time `json:"expires_at,omitempty"`
	Revoked          *bool      `json:"revoked,omitempty"`
}

func (s *Service) Update(ctx context.Context, id string, in UpdateKeyInput) (*APIKey, error) {
	cur, err := s.Get(ctx, id)
	if err != nil {
		return nil, err
	}
	if in.Role != nil && !in.Role.Valid() {
		return nil, fmt.Errorf("%w: invalid role", ErrInvalidInput)
	}
	losingAdmin := cur.Role == RoleAdmin && cur.RevokedAt == nil &&
		((in.Role != nil && *in.Role != RoleAdmin) || (in.Revoked != nil && *in.Revoked) ||
			(in.AllowedInstances != nil && len(*in.AllowedInstances) > 0))
	if losingAdmin {
		if err := s.ensureAnotherAdmin(ctx, id); err != nil {
			return nil, err
		}
	}

	if in.Name != nil {
		cur.Name = *in.Name
	}
	if in.Role != nil {
		cur.Role = *in.Role
	}
	if in.AllowedInstances != nil {
		cur.AllowedInstances = *in.AllowedInstances
	}
	if in.ExpiresAt != nil {
		cur.ExpiresAt = in.ExpiresAt
	}
	revokedSQL := "revoked_at"
	if in.Revoked != nil {
		if *in.Revoked {
			revokedSQL = "COALESCE(revoked_at, NOW())"
		} else {
			revokedSQL = "NULL"
		}
	}
	_, err = s.db.ExecContext(ctx, `UPDATE api_keys SET name = $2, role = $3, allowed_instances = $4, expires_at = $5, revoked_at = `+revokedSQL+` WHERE id = $1`,
		id, cur.Name, string(cur.Role), pq.Array(cur.AllowedInstances), cur.ExpiresAt)
	if err != nil {
		return nil, err
	}
	s.invalidate()
	return s.Get(ctx, id)
}

func (s *Service) Delete(ctx context.Context, id string) error {
	cur, err := s.Get(ctx, id)
	if err != nil {
		return err
	}
	if cur.Role == RoleAdmin && cur.RevokedAt == nil {
		if err := s.ensureAnotherAdmin(ctx, id); err != nil {
			return err
		}
	}
	if _, err := s.db.ExecContext(ctx, `DELETE FROM api_keys WHERE id = $1`, id); err != nil {
		return err
	}
	s.invalidate()
	return nil
}

// ensureAnotherAdmin refuses changes that would leave nobody able to manage keys.
// The ADMIN_API_KEY environment key counts as an admin.
func (s *Service) ensureAnotherAdmin(ctx context.Context, excludeID string) error {
	if s.envAdminKey != "" {
		return nil
	}
	var n int
	err := s.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM api_keys
		WHERE role = 'admin' AND revoked_at IS NULL AND (expires_at IS NULL OR expires_at > NOW())
		  AND cardinality(allowed_instances) = 0 AND id <> $1`, excludeID).Scan(&n)
	if err != nil {
		return err
	}
	if n == 0 {
		return ErrLastAdmin
	}
	return nil
}

// Bootstrap guarantees that an admin credential exists. When ADMIN_API_KEY is not set and
// there is no active admin key, it generates one, stores its hash and writes the plaintext
// to <dataDir>/.api-key (mode 0600). It returns the generated key ("" if none was created).
func (s *Service) Bootstrap(ctx context.Context, dataDir string) (string, string, error) {
	if s.envAdminKey != "" {
		return "", "", nil
	}
	var n int
	if err := s.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM api_keys
		WHERE role = 'admin' AND revoked_at IS NULL AND (expires_at IS NULL OR expires_at > NOW())`).Scan(&n); err != nil {
		return "", "", err
	}
	if n > 0 {
		return "", "", nil
	}
	_, plain, err := s.Create(ctx, CreateKeyInput{Name: "bootstrap-admin", Role: RoleAdmin}, "system")
	if err != nil {
		return "", "", err
	}
	path := ""
	if dataDir != "" {
		if err := os.MkdirAll(dataDir, 0o700); err == nil {
			path = filepath.Join(dataDir, ".api-key")
			if werr := os.WriteFile(path, []byte(plain), 0o600); werr != nil {
				path = ""
			}
		}
	}
	return plain, path, nil
}

func newID() string {
	b := make([]byte, 16)
	_, _ = rand.Read(b)
	b[6] = (b[6] & 0x0f) | 0x40
	b[8] = (b[8] & 0x3f) | 0x80
	h := hex.EncodeToString(b)
	return h[0:8] + "-" + h[8:12] + "-" + h[12:16] + "-" + h[16:20] + "-" + h[20:]
}
