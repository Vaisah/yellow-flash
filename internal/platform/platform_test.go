package platform

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"
)

func TestPathChecksRejectUnsafeInput(t *testing.T) {
	dir := t.TempDir()
	for _, p := range []string{"relative/file.txt", filepath.Join(dir, "missing.bin"), filepath.Join(dir, `quote".bin`), ""} {
		if err := checkPath(p); !errors.Is(err, ErrInvalidPath) {
			t.Errorf("checkPath(%q) = %v", p, err)
		}
	}
	ok := filepath.Join(dir, "ok.bin")
	if err := os.WriteFile(ok, nil, 0o600); err != nil {
		t.Fatal(err)
	}
	if err := checkPath(ok); err != nil {
		t.Fatal(err)
	}
}

func TestMemorySecretStore(t *testing.T) {
	s := NewMemorySecretStore()
	ctx := context.Background()
	in := map[string]string{"Authorization": "Bearer x"}
	_ = s.Put(ctx, "a", in)
	in["Authorization"] = "changed"
	got, err := s.Get(ctx, "a")
	if err != nil || got["Authorization"] != "Bearer x" {
		t.Fatalf("got %v %v", got, err)
	}
	_ = s.Delete(ctx, "a")
	if _, err = s.Get(ctx, "a"); !errors.Is(err, ErrSecretNotFound) {
		t.Fatal("secret not deleted")
	}
}
