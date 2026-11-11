package platform

// The system's keychain as a SecretStore: Windows Credential Manager, or
// the freedesktop Secret Service on Linux (GNOME Keyring, KWallet). Secrets
// kept there survive restarts and never touch Yellow Flash's own files.
// Each secret is stored as one JSON object, named "<service>/<key>".

import (
	"encoding/json"
	"errors"
	"fmt"
)

// ErrNoKeychain means this system has no keychain Yellow Flash can use (a
// Linux session without a Secret Service, another OS): secrets stay in
// memory only.
var ErrNoKeychain = errors.New("no system keychain is available")

// maxSecret bounds one stored secret (Windows' credential blob limit is
// 2560 bytes; the others are kept to the same).
const maxSecret = 2560

// sealValues is the stored form of values.
func sealValues(values map[string]string) ([]byte, error) {
	b, err := json.Marshal(values)
	if err != nil {
		return nil, err
	}
	if len(b) > maxSecret {
		return nil, fmt.Errorf("a secret of %d bytes is more than the keychain keeps (%d)", len(b), maxSecret)
	}
	return b, nil
}

// openValues reads a stored secret, and wipes the bytes it was read from.
func openValues(b []byte) (map[string]string, error) {
	defer clear(b)
	var v map[string]string
	if err := json.Unmarshal(b, &v); err != nil {
		return nil, errors.New("the keychain holds an unreadable secret")
	}
	return v, nil
}
