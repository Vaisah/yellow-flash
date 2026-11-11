//go:build windows

package platform

// Windows Credential Manager (advapi32's CredWriteW, CredReadW, CredDeleteW):
// generic credentials, kept for this user on this computer (they don't
// roam), shown under "Windows Credentials" as "<service>/<key>".

import (
	"context"
	"errors"
	"unsafe"

	"golang.org/x/sys/windows"
)

var (
	advapi32        = windows.NewLazySystemDLL("advapi32.dll")
	procCredWriteW  = advapi32.NewProc("CredWriteW")
	procCredReadW   = advapi32.NewProc("CredReadW")
	procCredDeleteW = advapi32.NewProc("CredDeleteW")
	procCredFree    = advapi32.NewProc("CredFree")
)

const (
	credTypeGeneric         = 1
	credPersistLocalMachine = 2
)

// credential is CREDENTIALW.
type credential struct {
	Flags              uint32
	Type               uint32
	TargetName         *uint16
	Comment            *uint16
	LastWritten        windows.Filetime
	CredentialBlobSize uint32
	CredentialBlob     *byte
	Persist            uint32
	AttributeCount     uint32
	Attributes         uintptr
	TargetAlias        *uint16
	UserName           *uint16
}

// Keychain is the system's keychain under one service name.
type Keychain struct{ service string }

// NewKeychain is the keychain's secrets under service ("Yellow Flash").
func NewKeychain(service string) (*Keychain, error) {
	if err := procCredReadW.Find(); err != nil {
		return nil, ErrNoKeychain
	}
	return &Keychain{service: service}, nil
}

func (k *Keychain) target(key string) (*uint16, error) {
	return windows.UTF16PtrFromString(k.service + "/" + key)
}

// Put stores values under key, replacing what was there.
func (k *Keychain) Put(_ context.Context, key string, values map[string]string) error {
	blob, err := sealValues(values)
	if err != nil {
		return err
	}
	defer clear(blob)
	target, err := k.target(key)
	if err != nil {
		return err
	}
	user, _ := windows.UTF16PtrFromString(k.service)
	c := credential{Type: credTypeGeneric, TargetName: target, CredentialBlobSize: uint32(len(blob)),
		CredentialBlob: &blob[0], Persist: credPersistLocalMachine, UserName: user}
	if r, _, err := procCredWriteW.Call(uintptr(unsafe.Pointer(&c)), 0); r == 0 {
		return errors.New("Windows Credential Manager refused the secret: " + err.Error())
	}
	return nil
}

// Get returns the values stored under key, or ErrSecretNotFound.
func (k *Keychain) Get(_ context.Context, key string) (map[string]string, error) {
	target, err := k.target(key)
	if err != nil {
		return nil, err
	}
	var c *credential
	if r, _, err := procCredReadW.Call(uintptr(unsafe.Pointer(target)), credTypeGeneric, 0, uintptr(unsafe.Pointer(&c))); r == 0 {
		if errors.Is(err, windows.ERROR_NOT_FOUND) {
			return nil, ErrSecretNotFound
		}
		return nil, errors.New("Windows Credential Manager: " + err.Error())
	}
	defer procCredFree.Call(uintptr(unsafe.Pointer(c)))
	blob := make([]byte, c.CredentialBlobSize)
	if c.CredentialBlobSize > 0 {
		copy(blob, unsafe.Slice(c.CredentialBlob, c.CredentialBlobSize))
	}
	return openValues(blob)
}

// Delete removes key's secret. Deleting a missing key is not an error.
func (k *Keychain) Delete(_ context.Context, key string) error {
	target, err := k.target(key)
	if err != nil {
		return err
	}
	if r, _, err := procCredDeleteW.Call(uintptr(unsafe.Pointer(target)), credTypeGeneric, 0); r == 0 &&
		!errors.Is(err, windows.ERROR_NOT_FOUND) {
		return errors.New("Windows Credential Manager: " + err.Error())
	}
	return nil
}
