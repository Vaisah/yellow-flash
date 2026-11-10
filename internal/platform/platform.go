// Package platform declares operating-system services behind interfaces so
// the engine stays platform-neutral. Keychain (keychain*.go) is the
// OS-backed SecretStore: Windows Credential Manager, freedesktop Secret
// Service.
package platform

import (
	"context"
	"errors"
	"sync"
)

// ErrSecretNotFound is returned when no secret is stored under a key.
var ErrSecretNotFound = errors.New("secret not found")

// SecretStore keeps credentials (Authorization, Cookie, ...) out of the
// database and out of logs.
type SecretStore interface {
	Put(ctx context.Context, key string, values map[string]string) error
	Get(ctx context.Context, key string) (map[string]string, error)
	Delete(ctx context.Context, key string) error
}

// MemorySecretStore holds secrets in process memory only. They are lost on
// exit, so credential-bearing downloads must be re-authorised after a
// restart until an OS-backed store is configured.
type MemorySecretStore struct {
	mu     sync.Mutex
	values map[string]map[string]string
}

// NewMemorySecretStore returns an empty in-memory store.
func NewMemorySecretStore() *MemorySecretStore {
	return &MemorySecretStore{values: map[string]map[string]string{}}
}

// Put stores a copy of values.
func (s *MemorySecretStore) Put(_ context.Context, key string, values map[string]string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.values[key] = copyMap(values)
	return nil
}

// Get returns a copy of the stored values.
func (s *MemorySecretStore) Get(_ context.Context, key string) (map[string]string, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	v, ok := s.values[key]
	if !ok {
		return nil, ErrSecretNotFound
	}
	return copyMap(v), nil
}

// Delete removes a key. Deleting a missing key is not an error.
func (s *MemorySecretStore) Delete(_ context.Context, key string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	delete(s.values, key)
	return nil
}

func copyMap(in map[string]string) map[string]string {
	out := make(map[string]string, len(in))
	for k, v := range in {
		out[k] = v
	}
	return out
}
