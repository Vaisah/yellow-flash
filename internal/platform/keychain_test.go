package platform

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"testing"
	"time"
)

// keychain is the system's keychain under a name of this test's own, or a
// skip where the system has none (a Linux session without a Secret Service).
func keychain(t *testing.T) *Keychain {
	t.Helper()
	k, err := NewKeychain(fmt.Sprintf("Yellow Flash test %d", time.Now().UnixNano()))
	if errors.Is(err, ErrNoKeychain) {
		t.Skip("no system keychain here")
	}
	if err != nil {
		t.Fatal(err)
	}
	return k
}

var _ SecretStore = (*Keychain)(nil)

func TestTheKeychainKeepsReplacesAndForgetsASecret(t *testing.T) {
	k := keychain(t)
	ctx := context.Background()
	t.Cleanup(func() { _ = k.Delete(ctx, "acct-1") })
	if _, err := k.Get(ctx, "acct-1"); !errors.Is(err, ErrSecretNotFound) {
		t.Fatal("nothing yet:", err)
	}
	if err := k.Put(ctx, "acct-1", map[string]string{"kind": "token", "token": "s3cr3t"}); err != nil {
		t.Fatal(err)
	}
	got, err := k.Get(ctx, "acct-1")
	if err != nil || got["token"] != "s3cr3t" || got["kind"] != "token" {
		t.Fatal(got, err)
	}
	if err := k.Put(ctx, "acct-1", map[string]string{"kind": "oauth", "refresh_token": "r2"}); err != nil {
		t.Fatal(err)
	}
	if got, _ := k.Get(ctx, "acct-1"); got["refresh_token"] != "r2" || got["token"] != "" {
		t.Fatal("replaced:", got)
	}
	if err := k.Delete(ctx, "acct-1"); err != nil {
		t.Fatal(err)
	}
	if _, err := k.Get(ctx, "acct-1"); !errors.Is(err, ErrSecretNotFound) {
		t.Fatal("forgotten:", err)
	}
	if err := k.Delete(ctx, "acct-1"); err != nil {
		t.Fatal("deleting twice is no error:", err)
	}
}

func TestAnOversizedSecretIsRefused(t *testing.T) {
	k := keychain(t)
	err := k.Put(context.Background(), "big", map[string]string{"x": strings.Repeat("a", maxSecret)})
	if err == nil {
		_ = k.Delete(context.Background(), "big")
		t.Fatal("more than the keychain keeps")
	}
}
