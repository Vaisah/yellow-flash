package engine

import (
	"bytes"
	"context"
	"crypto/md5"
	"crypto/sha256"
	"errors"
	"fmt"
	"hash"
	"io"
	"os"

	domain "github.com/vaisah/internal/domain/download"
)

// ErrIntegrity is returned when a completed file fails verification.
var ErrIntegrity = errors.New("integrity check failed")

// verifyFile checks the final size and, when the server advertised one, the
// whole-file digest. It returns the verified size.
func verifyFile(ctx context.Context, path string, meta domain.RemoteMetadata) (int64, error) {
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
	if len(meta.Checksum) == 0 {
		return info.Size(), nil
	}
	var h hash.Hash
	switch meta.ChecksumAlgorithm {
	case "md5":
		h = md5.New()
	case "sha-256":
		h = sha256.New()
	default:
		return 0, fmt.Errorf("%w: unsupported checksum algorithm %q", ErrIntegrity, meta.ChecksumAlgorithm)
	}
	f, err := os.Open(path)
	if err != nil {
		return 0, err
	}
	defer f.Close()
	buf := make([]byte, 1<<20)
	for {
		if err := ctx.Err(); err != nil {
			return 0, err
		}
		n, readErr := f.Read(buf)
		h.Write(buf[:n])
		if readErr == io.EOF {
			break
		}
		if readErr != nil {
			return 0, readErr
		}
	}
	if !bytes.Equal(h.Sum(nil), meta.Checksum) {
		return 0, fmt.Errorf("%w: %s checksum mismatch", ErrIntegrity, meta.ChecksumAlgorithm)
	}
	return info.Size(), nil
}
