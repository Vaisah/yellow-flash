// Ported from Mister Download Manager, crates/engine/src/verify.rs (tests)
// Copyright (C) 2026 Ahmed Nefzaoui and contributors; Go port (C) 2026 the Yellow Flash contributors.
// Translated from Rust to Go and modified. SPDX-License-Identifier: GPL-3.0-or-later

package download

import (
	"errors"
	"testing"
)

func TestUserHashesByLengthOrName(t *testing.T) {
	md5, err := ParseChecksum("9e107d9d372bb6826bd81d3542a419d6")
	if err != nil || md5.Algorithm != MD5 {
		t.Fatalf("by length: %+v %v", md5, err)
	}
	sha, err := ParseChecksum("SHA256: d7a8fbb307d7809469ca9abcb0082e4f8d5651e46d3cdb762d02d0bf37c9e592  *x.iso")
	if err != nil || sha.Algorithm != SHA256 {
		t.Fatalf("named, with a file name after: %+v %v", sha, err)
	}
	if c, err := ParseChecksum("SHA-1=2fd4e1c67a2d28fced849ee1bb76e7391b93eb12"); err != nil || c.Algorithm != SHA1 {
		t.Fatalf("SHA-1=: %+v %v", c, err)
	}
	for _, bad := range []string{"md5:abc", "not a hash", "sha256:9e107d9d372bb6826bd81d3542a419d6", "whirlpool:00"} {
		if _, err := ParseChecksum(bad); !errors.Is(err, ErrInvalidChecksum) {
			t.Errorf("%q accepted", bad)
		}
	}
	if c, err := ParseChecksum("  "); err != nil || !c.IsZero() {
		t.Fatal("empty text is no checksum")
	}
	if got := sha.String(); got != "sha256:d7a8fbb307d7809469ca9abcb0082e4f8d5651e46d3cdb762d02d0bf37c9e592" {
		t.Fatalf("text form %q", got)
	}
	if again, _ := ParseChecksum(sha.String()); again.String() != sha.String() {
		t.Fatal("the text form reads back")
	}
}
