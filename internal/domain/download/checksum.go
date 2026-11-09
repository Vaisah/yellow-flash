// Ported from Mister Download Manager, crates/engine/src/verify.rs
// Copyright (C) 2026 Ahmed Nefzaoui and contributors; Go port (C) 2026 the Yellow Flash contributors.
// Translated from Rust to Go and modified. SPDX-License-Identifier: GPL-3.0-or-later

package download

import (
	"encoding/hex"
	"errors"
	"strings"
)

// ChecksumAlgorithm names a whole-file hash.
type ChecksumAlgorithm string

// Supported algorithms, as written in a checksum's text form ("sha256:…").
const (
	CRC32  ChecksumAlgorithm = "crc32"
	MD5    ChecksumAlgorithm = "md5"
	SHA1   ChecksumAlgorithm = "sha1"
	SHA256 ChecksumAlgorithm = "sha256"
	SHA512 ChecksumAlgorithm = "sha512"
)

// ErrInvalidChecksum is returned for checksum text that cannot be used.
var ErrInvalidChecksum = errors.New("invalid checksum")

// Size is the digest length in bytes; 0 for an unknown algorithm.
func (a ChecksumAlgorithm) Size() int {
	switch a {
	case CRC32:
		return 4
	case MD5:
		return 16
	case SHA1:
		return 20
	case SHA256:
		return 32
	case SHA512:
		return 64
	}
	return 0
}

// Strength orders algorithms: when a server announces several hashes the
// strongest is checked.
func (a ChecksumAlgorithm) Strength() int {
	switch a {
	case CRC32:
		return 1
	case MD5:
		return 2
	case SHA1:
		return 3
	case SHA256:
		return 4
	case SHA512:
		return 5
	}
	return 0
}

// Checksum is an expected whole-file hash: given by the user, or announced
// by the server (Repr-Digest, Digest, Content-MD5, x-goog-hash). The
// representation check catches a changed file; this catches a corrupt one.
type Checksum struct {
	Algorithm ChecksumAlgorithm `json:"algorithm"`
	Value     []byte            `json:"value"`
}

// IsZero reports an absent checksum.
func (c Checksum) IsZero() bool { return c.Algorithm == "" || len(c.Value) == 0 }

// String is the text form, "sha256:<hex>"; "" when absent.
func (c Checksum) String() string {
	if c.IsZero() {
		return ""
	}
	return string(c.Algorithm) + ":" + hex.EncodeToString(c.Value)
}

// ParseChecksum reads a hash typed or pasted by the user: hex, optionally
// preceded by "algo:" or "algo=" ("sha256:…", "MD5=…", "SHA-256: …").
// Without a name the length decides (8 hex digits CRC32, 32 MD5, 40 SHA-1,
// 64 SHA-256, 128 SHA-512). Text after the digits, such as the file name in
// sha256sum output ("… *file.iso"), is ignored. Empty text is the zero
// Checksum and no error.
func ParseChecksum(text string) (Checksum, error) {
	text = strings.TrimSpace(text)
	if text == "" {
		return Checksum{}, nil
	}
	name, rest := "", text
	if i := strings.IndexAny(text, ":="); i >= 0 && isAlgoName(text[:i]) {
		name, rest = strings.ReplaceAll(strings.ToLower(text[:i]), "-", ""), text[i+1:]
	}
	fields := strings.Fields(rest)
	if len(fields) == 0 {
		return Checksum{}, ErrInvalidChecksum
	}
	value, err := hex.DecodeString(fields[0])
	if err != nil {
		return Checksum{}, ErrInvalidChecksum
	}
	algo := ChecksumAlgorithm(name)
	if name == "" {
		for _, a := range []ChecksumAlgorithm{CRC32, MD5, SHA1, SHA256, SHA512} {
			if a.Size() == len(value) {
				algo = a
			}
		}
	}
	if algo.Size() == 0 || algo.Size() != len(value) {
		return Checksum{}, ErrInvalidChecksum
	}
	return Checksum{Algorithm: algo, Value: value}, nil
}

func isAlgoName(s string) bool {
	if s == "" {
		return false
	}
	for _, r := range s {
		if !(r >= 'a' && r <= 'z' || r >= 'A' && r <= 'Z' || r >= '0' && r <= '9' || r == '-') {
			return false
		}
	}
	return true
}
