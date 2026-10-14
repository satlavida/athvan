// Package keys stores invite-only API keys in a JSON file.
//
// Only a SHA-256 hash of each key is kept; the full key is returned once,
// at creation. Keys are 32 random bytes, so a fast hash is enough.
package keys

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"maps"
	"net/http"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"
)

const (
	keyPrefix = "athv_"
	dailyDays = 30
)

// ErrNotFound is returned for an unknown key id.
var ErrNotFound = errors.New("key not found")

// Key is one API key. Hash is never sent to clients.
type Key struct {
	ID         string           `json:"id"`
	Name       string           `json:"name"`
	Note       string           `json:"note,omitempty"`
	Prefix     string           `json:"prefix"`
	Hash       string           `json:"hash"`
	CreatedAt  time.Time        `json:"created_at"`
	RevokedAt  *time.Time       `json:"revoked_at,omitempty"`
	LastUsedAt *time.Time       `json:"last_used_at,omitempty"`
	Uses       int64            `json:"uses"`
	Daily      map[string]int64 `json:"daily,omitempty"` // YYYY-MM-DD (UTC) → uses
}

// Active reports whether the key may be used.
func (k *Key) Active() bool { return k.RevokedAt == nil }

// Store is a file-backed set of keys. Usage counters are flushed
// periodically by Run; changes to the key set are written immediately.
type Store struct {
	mu     sync.Mutex
	path   string
	keys   map[string]*Key // by id
	byHash map[string]*Key
	dirty  bool
}

// Open loads the store at path, creating its directory if needed.
func Open(path string) (*Store, error) {
	s := &Store{path: path, keys: map[string]*Key{}, byHash: map[string]*Key{}}
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return nil, err
	}
	data, err := os.ReadFile(path)
	switch {
	case errors.Is(err, os.ErrNotExist):
		return s, nil
	case err != nil:
		return nil, err
	}
	var file struct {
		Keys []*Key `json:"keys"`
	}
	if err := json.Unmarshal(data, &file); err != nil {
		return nil, err
	}
	for _, k := range file.Keys {
		s.keys[k.ID] = k
		s.byHash[k.Hash] = k
	}
	return s, nil
}

func randomString(n int) string {
	b := make([]byte, n)
	if _, err := rand.Read(b); err != nil {
		panic(err)
	}
	return base64.RawURLEncoding.EncodeToString(b)
}

func hashKey(secret string) string {
	h := sha256.Sum256([]byte(secret))
	return hex.EncodeToString(h[:])
}

// Create makes a new key and returns it with the full secret, which is
// not stored and cannot be recovered later.
func (s *Store) Create(name, note string) (Key, string, error) {
	secret := keyPrefix + randomString(32)
	now := time.Now().UTC()
	k := &Key{
		ID:        randomString(9),
		Name:      name,
		Note:      note,
		Prefix:    secret[:len(keyPrefix)+6],
		Hash:      hashKey(secret),
		CreatedAt: now,
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	s.keys[k.ID] = k
	s.byHash[k.Hash] = k
	if err := s.writeLocked(); err != nil {
		delete(s.keys, k.ID)
		delete(s.byHash, k.Hash)
		return Key{}, "", err
	}
	return *k, secret, nil
}

// List returns copies of all keys, newest first.
func (s *Store) List() []Key {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := make([]Key, 0, len(s.keys))
	for _, k := range s.keys {
		c := *k
		c.Daily = maps.Clone(k.Daily)
		out = append(out, c)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].CreatedAt.After(out[j].CreatedAt) })
	return out
}

// SetRevoked revokes or restores a key.
func (s *Store) SetRevoked(id string, revoked bool) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	k, ok := s.keys[id]
	if !ok {
		return ErrNotFound
	}
	if revoked && k.RevokedAt == nil {
		now := time.Now().UTC()
		k.RevokedAt = &now
	} else if !revoked {
		k.RevokedAt = nil
	}
	return s.writeLocked()
}

// Delete removes a key permanently.
func (s *Store) Delete(id string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	k, ok := s.keys[id]
	if !ok {
		return ErrNotFound
	}
	delete(s.keys, id)
	delete(s.byHash, k.Hash)
	return s.writeLocked()
}

// Verify checks a presented key and, if it is active, records one use.
func (s *Store) Verify(secret string) (Key, bool) {
	if !strings.HasPrefix(secret, keyPrefix) {
		return Key{}, false
	}
	h := hashKey(secret)
	s.mu.Lock()
	defer s.mu.Unlock()
	k, ok := s.byHash[h]
	if !ok || !k.Active() {
		return Key{}, false
	}
	now := time.Now().UTC()
	k.Uses++
	k.LastUsedAt = &now
	if k.Daily == nil {
		k.Daily = map[string]int64{}
	}
	k.Daily[now.Format("2006-01-02")]++
	cutoff := now.AddDate(0, 0, -dailyDays).Format("2006-01-02")
	for d := range k.Daily {
		if d < cutoff {
			delete(k.Daily, d)
		}
	}
	s.dirty = true
	return *k, true
}

// Flush writes pending usage counters.
func (s *Store) Flush() error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if !s.dirty {
		return nil
	}
	return s.writeLocked()
}

// Run flushes usage counters every interval until ctx ends, then once more.
func (s *Store) Run(ctx context.Context, interval time.Duration) {
	t := time.NewTicker(interval)
	defer t.Stop()
	for {
		select {
		case <-t.C:
			s.Flush()
		case <-ctx.Done():
			s.Flush()
			return
		}
	}
}

// writeLocked replaces the file atomically. Caller holds s.mu.
func (s *Store) writeLocked() error {
	file := struct {
		Keys []*Key `json:"keys"`
	}{}
	for _, k := range s.keys {
		file.Keys = append(file.Keys, k)
	}
	sort.Slice(file.Keys, func(i, j int) bool { return file.Keys[i].CreatedAt.Before(file.Keys[j].CreatedAt) })
	data, err := json.MarshalIndent(file, "", "  ")
	if err != nil {
		return err
	}
	tmp := s.path + ".tmp"
	if err := os.WriteFile(tmp, data, 0o600); err != nil {
		return err
	}
	if err := os.Rename(tmp, s.path); err != nil {
		return err
	}
	s.dirty = false
	return nil
}

// FromRequest extracts a presented key from the Authorization (Bearer)
// header, the X-API-Key header, or the key query parameter.
func FromRequest(r *http.Request) string {
	if h := r.Header.Get("Authorization"); len(h) > 7 && strings.EqualFold(h[:7], "bearer ") {
		return strings.TrimSpace(h[7:])
	}
	if h := r.Header.Get("X-API-Key"); h != "" {
		return strings.TrimSpace(h)
	}
	return r.URL.Query().Get("key")
}
