// Ported from Mister Download Manager, crates/engine/src/verify.rs
// Copyright (C) 2026 Ahmed Nefzaoui and contributors; Go port (C) 2026 the Yellow Flash contributors.
// Translated from Rust to Go and modified. SPDX-License-Identifier: GPL-3.0-or-later

package engine

import (
	"bytes"
	"context"
	"crypto/md5"
	"crypto/sha1"
	"crypto/sha256"
	"crypto/sha512"
	"errors"
	"fmt"
	"hash"
	"hash/crc32"
	"io"
	"os"

	domain "github.com/vaisah/internal/domain/download"
)

// ErrIntegrity is returned when a completed file fails verification.
var ErrIntegrity = errors.New("integrity check failed")

// verifyFile checks the final size and the whole-file hash: the one the
// user gave (expected, in ParseChecksum's forms) or else the one the server
// announced. It returns the verified size.
func verifyFile(ctx context.Context, path string, meta domain.RemoteMetadata, expected string) (int64, error) {
	info, err := os.Stat(path)
	if err != nil {
		return 0, err
	}
	if !info.Mode().IsRegular() {
		return 0, fmt.Errorf("%w: %s is not a regular file", ErrIntegrity, path)
	}
	if meta.Size >= 0 && info.Size() != meta.Size {
		return 0, fmt.Errorf("%w: size %d, expected %d", ErrIntegrity, info.Size(), meta.Size)
	}
	want := meta.Checksum
	if expected != "" {
		if want, err = domain.ParseChecksum(expected); err != nil {
			return 0, fmt.Errorf("%w: %w", ErrIntegrity, err)
		}
	}
	if want.IsZero() {
		return info.Size(), nil
	}
	if err = verifyChecksum(ctx, path, want); err != nil {
		return 0, err
	}
	return info.Size(), nil
}

// verifyChecksum hashes the file at path and compares it with want.
func verifyChecksum(ctx context.Context, path string, want domain.Checksum) error {
	got, err := digest(ctx, path, want.Algorithm)
	if err != nil {
		return err
	}
	if !bytes.Equal(got, want.Value) {
		return fmt.Errorf("%w: %s checksum mismatch", ErrIntegrity, want.Algorithm)
	}
	return nil
}

// digest hashes the whole file with algo. CRC32 is IEEE, big-endian, as
// sfv files and MDM write it.
func digest(ctx context.Context, path string, algo domain.ChecksumAlgorithm) ([]byte, error) {
	var h hash.Hash
	switch algo {
	case domain.CRC32:
		h = crc32.NewIEEE()
	case domain.MD5:
		h = md5.New()
	case domain.SHA1:
		h = sha1.New()
	case domain.SHA256:
		h = sha256.New()
	case domain.SHA512:
		h = sha512.New()
	default:
		return nil, fmt.Errorf("%w: unsupported checksum algorithm %q", ErrIntegrity, algo)
	}
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	buf := make([]byte, 1<<20)
	for {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		n, readErr := f.Read(buf)
		h.Write(buf[:n])
		if readErr == io.EOF {
			return h.Sum(nil), nil
		}
		if readErr != nil {
			return nil, readErr
		}
	}
}
