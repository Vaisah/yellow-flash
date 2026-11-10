//go:build !windows && !linux

package platform

import "context"

// Keychain is the system's keychain; on this system there is none Yellow
// Flash uses yet, and secrets stay in memory.
type Keychain struct{}

// NewKeychain returns ErrNoKeychain here.
func NewKeychain(string) (*Keychain, error) { return nil, ErrNoKeychain }

// Put returns ErrNoKeychain.
func (*Keychain) Put(context.Context, string, map[string]string) error { return ErrNoKeychain }

// Get returns ErrNoKeychain.
func (*Keychain) Get(context.Context, string) (map[string]string, error) { return nil, ErrNoKeychain }

// Delete returns ErrNoKeychain.
func (*Keychain) Delete(context.Context, string) error { return ErrNoKeychain }
