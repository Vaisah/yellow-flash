// Ported from Mister Download Manager, crates/tools/src/openpgp.rs (tests)
// Copyright (C) 2026 Ahmed Nefzaoui and contributors; Go port (C) 2026 the Yellow Flash contributors.
// Translated from Rust to Go and modified. SPDX-License-Identifier: GPL-3.0-or-later

package tools

import (
	"bytes"
	"encoding/base64"
	"encoding/binary"
	"encoding/hex"
	"os"
	"strings"
	"testing"
)

func read(t *testing.T, path string) []byte {
	t.Helper()
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	return b
}

// fixture is a key with a real checksum list and its signature: MDM's
// release key (Ed25519, test data only here) and yt-dlp's (RSA).
type fixture struct{ key, sums, sig []byte }

func fixtures(t *testing.T) []fixture {
	return []fixture{
		{read(t, "testdata/mdm-release.asc"), read(t, "testdata/mdm-SHA256SUMS.txt"), read(t, "testdata/mdm-SHA256SUMS.txt.sig")},
		{read(t, "keys/yt-dlp.asc"), read(t, "testdata/SHA2-256SUMS"), read(t, "testdata/SHA2-256SUMS.sig")},
	}
}

// armor puts sig in ASCII armor with the checksum crc.
func armor(sig []byte, crc uint32) string {
	body := base64.StdEncoding.EncodeToString(sig)
	var lines []string
	for at := 0; at < len(body); at += 64 {
		lines = append(lines, body[at:min(at+64, len(body))])
	}
	sum := base64.StdEncoding.EncodeToString([]byte{byte(crc >> 16), byte(crc >> 8), byte(crc)})
	return "-----BEGIN PGP SIGNATURE-----\r\n\r\n" + strings.Join(lines, "\r\n") + "\r\n=" + sum + "\r\n-----END PGP SIGNATURE-----\r\n"
}

// unhashed is where the unhashed subpackets of the signature packet sig
// are: the one part no signature covers.
func unhashed(sig []byte) (int, int) {
	header := 1 + []int{1, 2, 4}[sig[0]&0x03]
	hashed := int(binary.BigEndian.Uint16(sig[header+4:]))
	at := header + 6 + hashed
	n := int(binary.BigEndian.Uint16(sig[at:]))
	return at + 2, at + 2 + n
}

func TestThePinnedKeysHaveTheFingerprintsWeExpect(t *testing.T) {
	for path, want := range map[string]string{
		"testdata/mdm-release.asc": "5826D399249DE856EA087D802E293C28E8A69D74",
		"keys/yt-dlp.asc":          "AC0CBBE6848D6A873464AF4E57CF65933B5A7581",
	} {
		raw, ok := unarmor(read(t, path), "PUBLIC KEY BLOCK")
		if !ok {
			t.Fatalf("%s: not armored", path)
		}
		keys, ok := publicKeys(raw)
		if !ok || len(keys) != 1 {
			t.Fatalf("%s: %d keys", path, len(keys))
		}
		if got := strings.ToUpper(hex.EncodeToString(keys[0].fingerprint[:])); got != want {
			t.Errorf("%s: %s", path, got)
		}
	}
}

func TestRealSignaturesVerifyAndAnyChangeFailsThem(t *testing.T) {
	for _, f := range fixtures(t) {
		if !verifyPGP(f.key, f.sums, f.sig) {
			t.Fatal("a real signature fails")
		}
		for _, at := range []int{0, len(f.sums) / 2, len(f.sums) - 1} {
			data := bytes.Clone(f.sums)
			data[at] ^= 1
			if verifyPGP(f.key, data, f.sig) {
				t.Errorf("data byte %d changed", at)
			}
		}
		// Every byte of the signature counts but the unhashed subpackets
		// (the issuer, which only names the key).
		from, to := unhashed(f.sig)
		for at := range f.sig {
			if at >= from && at < to {
				continue
			}
			for _, flip := range []byte{0x01, 0x10, 0x80} {
				forged := bytes.Clone(f.sig)
				forged[at] ^= flip
				if verifyPGP(f.key, f.sums, forged) {
					t.Errorf("signature byte %d changed by %#x", at, flip)
				}
			}
		}
		for n := range f.sig {
			if verifyPGP(f.key, f.sums, f.sig[:n]) {
				t.Errorf("cut to %d bytes", n)
			}
		}
		if verifyPGP(f.key, f.sums, append(bytes.Clone(f.sig), 0)) {
			t.Error("a byte more")
		}
		if verifyPGP(f.key, f.sums, append(bytes.Clone(f.sig), f.sig...)) {
			t.Error("two signatures")
		}
	}
}

func TestAnIssuerThatNamesAnotherKeyFails(t *testing.T) {
	for _, f := range fixtures(t) {
		_, to := unhashed(f.sig)
		forged := bytes.Clone(f.sig)
		forged[to-1] ^= 1
		if verifyPGP(f.key, f.sums, forged) {
			t.Error("another issuer")
		}
	}
}

func TestEachKeySignsForItselfOnly(t *testing.T) {
	fs := fixtures(t)
	mdm, ytdlp := fs[0], fs[1]
	for name, ok := range map[string]bool{
		"yt-dlp's key, MDM's list": verifyPGP(ytdlp.key, mdm.sums, mdm.sig),
		"MDM's key, yt-dlp's list": verifyPGP(mdm.key, ytdlp.sums, ytdlp.sig),
		"a key as the signature":   verifyPGP(mdm.key, mdm.sums, mdm.key),
		"a signature as the key":   verifyPGP(mdm.sig, mdm.sums, mdm.sig),
	} {
		if ok {
			t.Error(name)
		}
	}
	gallery := read(t, "keys/gallery-dl.asc")
	if !verifyPGP(gallery, read(t, "testdata/gallery-dl-SHA256SUMS"), read(t, "testdata/gallery-dl-SHA256SUMS.sig")) {
		t.Error("gallery-dl's real signature fails")
	}
	if verifyPGP(gallery, ytdlp.sums, ytdlp.sig) {
		t.Error("gallery-dl's key, yt-dlp's list")
	}
}

func TestArmorIsCheckedByItsChecksum(t *testing.T) {
	f := fixtures(t)[0]
	if !verifyPGP(f.key, f.sums, []byte(armor(f.sig, crc24(f.sig)))) {
		t.Error("armored signature")
	}
	if verifyPGP(f.key, f.sums, []byte(armor(f.sig, crc24(f.sig)^1))) {
		t.Error("a wrong armor checksum")
	}
	// A damaged key: "mDME" begins its data.
	damaged := strings.Replace(string(f.key), "mDME", "mDMF", 1)
	if damaged == string(f.key) || verifyPGP([]byte(damaged), f.sums, f.sig) {
		t.Error("a damaged key")
	}
}

func TestGarbageFailsWithoutPanicking(t *testing.T) {
	fs := fixtures(t)
	mdm, ytdlp := fs[0], fs[1]
	// xorshift64: the same "random" bytes on every run.
	state := uint64(0x2545f4914f6cdd1d)
	next := func() uint64 {
		state ^= state << 13
		state ^= state >> 7
		state ^= state << 17
		return state
	}
	for round := range 20000 {
		n := int(next() % 600)
		b := make([]byte, n)
		for i := range b {
			b[i] = byte(next())
		}
		// Half begin like the real signature, to get past the first checks.
		if round%2 == 0 && n >= 6 {
			copy(b, ytdlp.sig[:6])
		}
		if verifyPGP(ytdlp.key, ytdlp.sums, b) || verifyPGP(b, ytdlp.sums, ytdlp.sig) {
			t.Fatal("garbage verified")
		}
		text := make([]byte, n)
		for i, c := range b {
			text[i] = '+' + c%80
		}
		if verifyPGP(mdm.key, mdm.sums, []byte("-----BEGIN PGP SIGNATURE-----\n\n"+string(text)+"\n=")) {
			t.Fatal("armored garbage verified")
		}
	}
	// The real keys and signatures, each byte damaged in turn, and cut short.
	for _, f := range fs {
		key, _ := unarmor(f.key, "PUBLIC KEY BLOCK")
		for at := range key {
			bad := bytes.Clone(key)
			bad[at] = byte(next())
			_ = verifyPGP(bad, f.sums, f.sig)
			_ = verifyPGP(key[:at], f.sums, f.sig)
		}
		for at := range f.sig {
			bad := bytes.Clone(f.sig)
			bad[at] = 0xff
			_ = verifyPGP(key, f.sums, bad)
		}
	}
}
