//go:build linux

package platform

// The freedesktop Secret Service (org.freedesktop.secrets on the session
// bus: GNOME Keyring, KWallet, KeePassXC), as its specification documents
// it: items in the default collection, found by their attributes
// {application: yellowflash, service, key}. A locked keyring is unlocked
// through the service's own prompt (the desktop asks the user). The
// session is "plain": the secret crosses only this user's session bus.

import (
	"context"
	"errors"
	"fmt"

	"github.com/godbus/dbus/v5"
)

const (
	secretsName   = "org.freedesktop.secrets"
	secretsPath   = dbus.ObjectPath("/org/freedesktop/secrets")
	serviceIface  = "org.freedesktop.Secret.Service"
	itemIface     = "org.freedesktop.Secret.Item"
	promptIface   = "org.freedesktop.Secret.Prompt"
	noPrompt      = dbus.ObjectPath("/")
	labelProperty = "org.freedesktop.Secret.Item.Label"
	attrsProperty = "org.freedesktop.Secret.Item.Attributes"
)

// secret is the specification's Secret struct (oayays).
type secret struct {
	Session     dbus.ObjectPath
	Parameters  []byte
	Value       []byte
	ContentType string
}

// Keychain is the system's keychain under one service name.
type Keychain struct{ service string }

// NewKeychain is the keychain's secrets under service ("Yellow Flash"), or
// ErrNoKeychain when the session has no Secret Service.
func NewKeychain(service string) (*Keychain, error) {
	conn, err := dbus.ConnectSessionBus()
	if err != nil {
		return nil, ErrNoKeychain
	}
	defer conn.Close()
	var has bool
	if err := conn.BusObject().Call("org.freedesktop.DBus.NameHasOwner", 0, secretsName).Store(&has); err != nil {
		return nil, ErrNoKeychain
	}
	if !has {
		var names []string
		if err := conn.BusObject().Call("org.freedesktop.DBus.ListActivatableNames", 0).Store(&names); err != nil {
			return nil, ErrNoKeychain
		}
		found := false
		for _, n := range names {
			found = found || n == secretsName
		}
		if !found {
			return nil, ErrNoKeychain
		}
	}
	return &Keychain{service: service}, nil
}

func (k *Keychain) attributes(key string) map[string]string {
	return map[string]string{"application": "yellowflash", "service": k.service, "key": key}
}

// session is a connection and a plain session on it.
type session struct {
	conn *dbus.Conn
	path dbus.ObjectPath
}

func (k *Keychain) open() (*session, error) {
	conn, err := dbus.ConnectSessionBus()
	if err != nil {
		return nil, ErrNoKeychain
	}
	var out dbus.Variant
	var path dbus.ObjectPath
	if err := conn.Object(secretsName, secretsPath).Call(serviceIface+".OpenSession", 0, "plain", dbus.MakeVariant("")).
		Store(&out, &path); err != nil {
		conn.Close()
		return nil, fmt.Errorf("the Secret Service: %w", err)
	}
	return &session{conn: conn, path: path}, nil
}

func (s *session) close() {
	_ = s.conn.Object(secretsName, s.path).Call("org.freedesktop.Secret.Session.Close", 0).Err
	s.conn.Close()
}

func (s *session) service() dbus.BusObject { return s.conn.Object(secretsName, secretsPath) }

// prompt runs a prompt the service asked for (the desktop asks the user)
// and waits for it; false: the user dismissed it.
func (s *session) prompt(ctx context.Context, p dbus.ObjectPath) (bool, error) {
	if p == noPrompt || p == "" {
		return true, nil
	}
	if err := s.conn.AddMatchSignal(dbus.WithMatchObjectPath(p), dbus.WithMatchInterface(promptIface),
		dbus.WithMatchMember("Completed")); err != nil {
		return false, err
	}
	ch := make(chan *dbus.Signal, 4)
	s.conn.Signal(ch)
	defer s.conn.RemoveSignal(ch)
	if err := s.conn.Object(secretsName, p).Call(promptIface+".Prompt", 0, "").Err; err != nil {
		return false, err
	}
	for {
		select {
		case <-ctx.Done():
			_ = s.conn.Object(secretsName, p).Call(promptIface+".Dismiss", 0).Err
			return false, ctx.Err()
		case sig := <-ch:
			if sig.Path != p || len(sig.Body) < 1 {
				continue
			}
			dismissed, _ := sig.Body[0].(bool)
			return !dismissed, nil
		}
	}
}

// unlock unlocks paths (a collection or items), prompting when needed.
func (s *session) unlock(ctx context.Context, paths []dbus.ObjectPath) error {
	var unlocked []dbus.ObjectPath
	var p dbus.ObjectPath
	if err := s.service().Call(serviceIface+".Unlock", 0, paths).Store(&unlocked, &p); err != nil {
		return err
	}
	ok, err := s.prompt(ctx, p)
	if err != nil {
		return err
	}
	if !ok {
		return errors.New("the keyring stayed locked")
	}
	return nil
}

// search finds key's items, unlocked.
func (k *Keychain) search(ctx context.Context, s *session, key string) ([]dbus.ObjectPath, error) {
	var unlocked, locked []dbus.ObjectPath
	if err := s.service().Call(serviceIface+".SearchItems", 0, k.attributes(key)).Store(&unlocked, &locked); err != nil {
		return nil, err
	}
	if len(locked) > 0 {
		if err := s.unlock(ctx, locked); err != nil {
			return nil, err
		}
		unlocked = append(unlocked, locked...)
	}
	return unlocked, nil
}

// Put stores values under key, replacing what was there.
func (k *Keychain) Put(ctx context.Context, key string, values map[string]string) error {
	blob, err := sealValues(values)
	if err != nil {
		return err
	}
	defer clear(blob)
	s, err := k.open()
	if err != nil {
		return err
	}
	defer s.close()
	var collection dbus.ObjectPath
	if err := s.service().Call(serviceIface+".ReadAlias", 0, "default").Store(&collection); err != nil {
		return err
	}
	if collection == noPrompt || collection == "" {
		return errors.New("the keyring has no default collection")
	}
	if err := s.unlock(ctx, []dbus.ObjectPath{collection}); err != nil {
		return err
	}
	props := map[string]dbus.Variant{
		labelProperty: dbus.MakeVariant(k.service + "/" + key),
		attrsProperty: dbus.MakeVariant(k.attributes(key)),
	}
	sec := secret{Session: s.path, Value: blob, ContentType: "application/json"}
	var item, p dbus.ObjectPath
	if err := s.conn.Object(secretsName, collection).Call("org.freedesktop.Secret.Collection.CreateItem", 0,
		props, sec, true).Store(&item, &p); err != nil {
		return fmt.Errorf("the Secret Service refused the secret: %w", err)
	}
	if ok, err := s.prompt(ctx, p); err != nil || !ok {
		return errors.New("the secret wasn't stored")
	}
	return nil
}

// Get returns the values stored under key, or ErrSecretNotFound.
func (k *Keychain) Get(ctx context.Context, key string) (map[string]string, error) {
	s, err := k.open()
	if err != nil {
		return nil, err
	}
	defer s.close()
	items, err := k.search(ctx, s, key)
	if err != nil {
		return nil, err
	}
	if len(items) == 0 {
		return nil, ErrSecretNotFound
	}
	var secrets map[dbus.ObjectPath]secret
	if err := s.service().Call(serviceIface+".GetSecrets", 0, items[:1], s.path).Store(&secrets); err != nil {
		return nil, err
	}
	sec, ok := secrets[items[0]]
	if !ok {
		return nil, ErrSecretNotFound
	}
	return openValues(sec.Value)
}

// Delete removes key's secret. Deleting a missing key is not an error.
func (k *Keychain) Delete(ctx context.Context, key string) error {
	s, err := k.open()
	if err != nil {
		return err
	}
	defer s.close()
	items, err := k.search(ctx, s, key)
	if err != nil {
		return err
	}
	for _, it := range items {
		var p dbus.ObjectPath
		if err := s.conn.Object(secretsName, it).Call(itemIface+".Delete", 0).Store(&p); err != nil {
			return err
		}
		if _, err := s.prompt(ctx, p); err != nil {
			return err
		}
	}
	return nil
}
