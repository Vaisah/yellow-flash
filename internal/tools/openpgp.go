// Ported from Mister Download Manager, crates/tools/src/openpgp.rs
// Copyright (C) 2026 Ahmed Nefzaoui and contributors; Go port (C) 2026 the Yellow Flash contributors.
// Translated from Rust to Go and modified. SPDX-License-Identifier: GPL-3.0-or-later

package tools

// Just enough OpenPGP (RFC 9580, and RFC 4880 before it) to check a detached
// signature by one of the keys we pin: yt-dlp's and gallery-dl's (RSA), and
// Ed25519 keys in their legacy EdDSA form (algorithm 22). Publishers sign
// their checksum lists as binary documents with v4 signatures; that, with
// SHA-256 or SHA-512, is all this understands, and anything else fails. The
// keys are built into the program, so their self-signatures aren't checked:
// every key and subkey in a pinned key is trusted to sign.
//
// The input is untrusted: every read is bounds-checked, and a malformed byte
// only makes a check fail.

import (
	"bytes"
	"crypto"
	"crypto/ed25519"
	"crypto/rsa"
	"crypto/sha1" //nolint:gosec // OpenPGP v4 fingerprints are SHA-1 by definition; they only name keys here.
	"crypto/sha256"
	"crypto/sha512"
	"encoding/base64"
	"encoding/binary"
	"hash"
	"math/big"
	"strings"
	"time"
)

// Packet tags (RFC 9580 §5).
const (
	tagSignature    = 2
	tagPublicKey    = 6
	tagPublicSubkey = 14
)

// Public-key algorithms (§9.1).
const (
	algRSA         = 1
	algEdDSALegacy = 22
)

// Hash algorithms (§9.5).
const (
	hashSHA256 = 8
	hashSHA512 = 10
)

// Signature subpackets (§5.2.3.7).
const (
	subCreated           = 2
	subExpires           = 3
	subIssuer            = 16
	subIssuerFingerprint = 33
)

// ed25519OID is Ed25519's curve OID as legacy EdDSA keys carry it
// (1.3.6.1.4.1.11591.15.1, §9.2).
var ed25519OID = []byte{0x2b, 0x06, 0x01, 0x04, 0x01, 0xda, 0x47, 0x0f, 0x01}

// verifyPGP reports whether sig (one signature packet, binary or
// ASCII-armored) is a valid signature of data by the public key key (binary
// or ASCII-armored) or one of its subkeys.
func verifyPGP(key, data, sig []byte) bool {
	k, ok1 := unarmor(key, "PUBLIC KEY BLOCK")
	s, ok2 := unarmor(sig, "SIGNATURE")
	if !ok1 || !ok2 {
		return false
	}
	keys, ok1 := publicKeys(k)
	parsed, ok2 := parseSignature(s)
	if !ok1 || !ok2 {
		return false
	}
	for i := range keys {
		if parsed.verifies(&keys[i], data) {
			return true
		}
	}
	return false
}

// unarmor returns the packets of armored if it's an ASCII-armored block of
// kind ("SIGNATURE", ...), or armored itself if it's binary.
func unarmor(armored []byte, kind string) ([]byte, bool) {
	// A binary packet starts with a byte whose top bit is set, which no
	// armor does.
	if len(armored) > 0 && armored[0]&0x80 != 0 {
		return armored, true
	}
	var lines []string
	for _, l := range strings.Split(string(armored), "\n") {
		lines = append(lines, strings.TrimRight(l, " \t\r"))
	}
	begin, end := "-----BEGIN PGP "+kind+"-----", "-----END PGP "+kind+"-----"
	i := 0
	for i < len(lines) && lines[i] != begin {
		i++
	}
	if i == len(lines) {
		return nil, false
	}
	i++
	// Headers ("Version: ..."), up to the blank line before the data.
	for ; i < len(lines); i++ {
		if lines[i] == "" {
			break
		}
		if !strings.Contains(lines[i], ": ") {
			return nil, false
		}
	}
	i++
	var body strings.Builder
	crc, hasCRC := uint32(0), false
	for {
		if i >= len(lines) {
			return nil, false
		}
		line := lines[i]
		i++
		if line == end {
			break
		}
		if sum, ok := strings.CutPrefix(line, "="); ok {
			// The optional checksum comes last, just before the end line.
			b, err := base64.StdEncoding.DecodeString(sum)
			if err != nil || len(b) != 3 {
				return nil, false
			}
			crc, hasCRC = uint32(b[0])<<16|uint32(b[1])<<8|uint32(b[2]), true
			if i >= len(lines) || lines[i] != end {
				return nil, false
			}
			break
		}
		body.WriteString(line)
	}
	packets, err := base64.StdEncoding.DecodeString(body.String())
	if err != nil {
		return nil, false
	}
	if hasCRC && crc != crc24(packets) {
		return nil, false
	}
	return packets, true
}

// crc24 is the armor's checksum (RFC 9580 §6.1.1).
func crc24(b []byte) uint32 {
	crc := uint32(0xb704ce)
	for _, c := range b {
		crc ^= uint32(c) << 16
		for range 8 {
			crc <<= 1
			if crc&0x1000000 != 0 {
				crc ^= 0x1864cfb
			}
		}
	}
	return crc & 0xffffff
}

// reader reads through a packet or subpacket, failing at its end.
type reader struct{ b []byte }

func (r *reader) take(n int) ([]byte, bool) {
	if n < 0 || n > len(r.b) {
		return nil, false
	}
	head := r.b[:n]
	r.b = r.b[n:]
	return head, true
}

func (r *reader) u8() (byte, bool) {
	b, ok := r.take(1)
	if !ok {
		return 0, false
	}
	return b[0], true
}

func (r *reader) u16() (int, bool) {
	b, ok := r.take(2)
	if !ok {
		return 0, false
	}
	return int(binary.BigEndian.Uint16(b)), true
}

func (r *reader) u32() (uint32, bool) {
	b, ok := r.take(4)
	if !ok {
		return 0, false
	}
	return binary.BigEndian.Uint32(b), true
}

// mpi reads a multiprecision integer (§3.2): big-endian, no leading zeros,
// its length in bits counted from its first set bit. Any other length fails.
func (r *reader) mpi() ([]byte, bool) {
	bits, ok := r.u16()
	if !ok {
		return nil, false
	}
	b, ok := r.take((bits + 7) / 8)
	if !ok {
		return nil, false
	}
	top, whole := 0, 0
	if len(b) > 0 {
		for v := b[0]; v != 0; v >>= 1 {
			top++
		}
		whole = (len(b) - 1) * 8
	}
	return b, bits == whole+top
}

type packet struct {
	tag  byte
	body []byte
}

// packets splits b into its packets; false if any packet is malformed.
func packets(b []byte) ([]packet, bool) {
	r := reader{b}
	var out []packet
	for len(r.b) > 0 {
		first, _ := r.u8()
		if first&0x80 == 0 {
			return nil, false
		}
		var tag byte
		var n int
		if first&0x40 != 0 {
			// The current format (§4.2.1). Partial lengths are only for data
			// packets.
			l, ok := r.u8()
			if !ok {
				return nil, false
			}
			switch {
			case l < 192:
				n = int(l)
			case l < 224:
				l2, ok := r.u8()
				if !ok {
					return nil, false
				}
				n = (int(l-192) << 8) + int(l2) + 192
			case l == 255:
				v, ok := r.u32()
				if !ok || v > 1<<30 {
					return nil, false
				}
				n = int(v)
			default:
				return nil, false
			}
			tag = first & 0x3f
		} else {
			// The legacy format (§4.2.2), which gpg still writes; never of
			// indeterminate length.
			var ok bool
			switch first & 0x03 {
			case 0:
				var l byte
				l, ok = r.u8()
				n = int(l)
			case 1:
				n, ok = r.u16()
			case 2:
				var v uint32
				v, ok = r.u32()
				ok = ok && v <= 1<<30
				n = int(v)
			default:
				return nil, false
			}
			if !ok {
				return nil, false
			}
			tag = (first >> 2) & 0x0f
		}
		body, ok := r.take(n)
		if !ok {
			return nil, false
		}
		out = append(out, packet{tag, body})
	}
	return out, true
}

// pgpKey is a v4 public key or subkey this can check signatures with.
type pgpKey struct {
	// fingerprint is SHA-1 over the key packet (§5.5.4.2); its last 8 bytes
	// are the key ID.
	fingerprint [20]byte
	rsaN, rsaE  []byte
	ed          ed25519.PublicKey
}

func (k *pgpKey) algorithm() byte {
	if k.ed != nil {
		return algEdDSALegacy
	}
	return algRSA
}

func (k *pgpKey) keyID() []byte { return k.fingerprint[12:] }

// parseKey reads the key in a public key or subkey packet's body; false if
// it's of another version or algorithm.
func parseKey(body []byte) (pgpKey, bool) {
	var k pgpKey
	r := reader{body}
	if v, ok := r.u8(); !ok || v != 4 {
		return k, false
	}
	if _, ok := r.u32(); !ok { // created
		return k, false
	}
	alg, ok := r.u8()
	if !ok {
		return k, false
	}
	switch alg {
	case algRSA:
		n, ok1 := r.mpi()
		e, ok2 := r.mpi()
		if !ok1 || !ok2 {
			return k, false
		}
		k.rsaN, k.rsaE = n, e
	case algEdDSALegacy:
		l, ok := r.u8()
		if !ok {
			return k, false
		}
		oid, ok := r.take(int(l))
		if !ok || !bytes.Equal(oid, ed25519OID) {
			return k, false
		}
		// The point, prefixed with 0x40 (native encoding, §11.2.2).
		point, ok := r.mpi()
		if !ok || len(point) != 33 || point[0] != 0x40 {
			return k, false
		}
		k.ed = ed25519.PublicKey(bytes.Clone(point[1:]))
	default:
		return k, false
	}
	if len(r.b) != 0 || len(body) > 0xffff {
		return k, false
	}
	h := sha1.New() //nolint:gosec // see the import
	h.Write([]byte{0x99, byte(len(body) >> 8), byte(len(body))})
	h.Write(body)
	copy(k.fingerprint[:], h.Sum(nil))
	return k, true
}

// publicKeys returns the usable keys in a transferable public key (§10.1):
// the primary key and its subkeys. The other packets (user IDs,
// self-signatures) are skipped.
func publicKeys(b []byte) ([]pgpKey, bool) {
	ps, ok := packets(b)
	if !ok || len(ps) == 0 || ps[0].tag != tagPublicKey {
		return nil, false
	}
	var keys []pgpKey
	for _, p := range ps {
		if p.tag != tagPublicKey && p.tag != tagPublicSubkey {
			continue
		}
		if k, ok := parseKey(p.body); ok {
			keys = append(keys, k)
		}
	}
	return keys, true
}

// pgpSignature is a v4 signature of a binary document (type 0x00).
type pgpSignature struct {
	algorithm, hash byte
	// hashed is the signature's hashed part: version, type, algorithms and
	// hashed subpackets.
	hashed []byte
	left16 [2]byte
	// created and expires are seconds since 1970 and for how long it holds
	// (0: forever).
	created, expires  uint32
	issuer            []byte // 8 bytes, or nil
	issuerFingerprint []byte // 20 bytes, or nil
	// mpis are RSA's signature, or Ed25519's R and S.
	mpis [][]byte
}

// parseSignature reads the only packet in b, a signature this understands.
func parseSignature(b []byte) (*pgpSignature, bool) {
	ps, ok := packets(b)
	if !ok || len(ps) != 1 || ps[0].tag != tagSignature {
		return nil, false
	}
	body := ps[0].body
	r := reader{body}
	head, ok := r.take(4)
	if !ok || head[0] != 4 || head[1] != 0x00 {
		return nil, false
	}
	s := &pgpSignature{algorithm: head[2], hash: head[3]}
	hashedLen, ok := r.u16()
	if !ok {
		return nil, false
	}
	hashedArea, ok := r.take(hashedLen)
	if !ok {
		return nil, false
	}
	unhashedLen, ok := r.u16()
	if !ok {
		return nil, false
	}
	unhashedArea, ok := r.take(unhashedLen)
	if !ok {
		return nil, false
	}
	left, ok := r.take(2)
	if !ok {
		return nil, false
	}
	copy(s.left16[:], left)
	s.hashed = body[:6+hashedLen]
	var created *uint32
	if !s.subpackets(hashedArea, true, &created) || !s.subpackets(unhashedArea, false, &created) {
		return nil, false
	}
	// The creation time must be there, and signed (§5.2.3.11).
	if created == nil {
		return nil, false
	}
	s.created = *created
	count := 0
	switch s.algorithm {
	case algRSA:
		count = 1
	case algEdDSALegacy:
		count = 2
	default:
		return nil, false
	}
	for range count {
		m, ok := r.mpi()
		if !ok {
			return nil, false
		}
		s.mpis = append(s.mpis, m)
	}
	return s, len(r.b) == 0
}

// subpackets reads the subpackets in area (§5.2.3.7). A critical one this
// doesn't know, or one that is malformed, fails the signature; times only
// count from the hashed area.
func (s *pgpSignature) subpackets(area []byte, hashed bool, created **uint32) bool {
	r := reader{area}
	for len(r.b) > 0 {
		l, _ := r.u8()
		var n int
		switch {
		case l < 192:
			n = int(l)
		case l < 255:
			l2, ok := r.u8()
			if !ok {
				return false
			}
			n = (int(l-192) << 8) + int(l2) + 192
		default:
			v, ok := r.u32()
			if !ok || v > 1<<30 {
				return false
			}
			n = int(v)
		}
		raw, ok := r.take(n)
		if !ok {
			return false
		}
		sub := reader{raw}
		kind, ok := sub.u8()
		if !ok {
			return false
		}
		critical := kind&0x80 != 0
		switch k := kind & 0x7f; {
		case k == subCreated && hashed:
			v, ok := sub.u32()
			if !ok || *created != nil {
				return false
			}
			*created = &v
		case k == subExpires && hashed:
			v, ok := sub.u32()
			if !ok {
				return false
			}
			s.expires = v
		case k == subIssuer:
			id, ok := sub.take(8)
			if !ok {
				return false
			}
			s.issuer = id
		case k == subIssuerFingerprint:
			// A v4 key's fingerprint; any other version can't be one of ours.
			v, ok := sub.u8()
			if !ok || v != 4 {
				return false
			}
			fp, ok := sub.take(20)
			if !ok {
				return false
			}
			s.issuerFingerprint = fp
		case critical:
			return false
		default:
			continue
		}
		if len(sub.b) != 0 {
			return false
		}
	}
	return true
}

// verifies reports whether s signs data with k.
func (s *pgpSignature) verifies(k *pgpKey, data []byte) bool {
	if s.algorithm != k.algorithm() ||
		(s.issuer != nil && !bytes.Equal(s.issuer, k.keyID())) ||
		(s.issuerFingerprint != nil && !bytes.Equal(s.issuerFingerprint, k.fingerprint[:])) ||
		s.expired() {
		return false
	}
	digest, h, ok := s.digest(data)
	if !ok {
		return false
	}
	// A quick check first (§5.2.4): the hash's first two bytes are in the
	// packet.
	if digest[0] != s.left16[0] || digest[1] != s.left16[1] {
		return false
	}
	switch {
	case k.ed == nil && len(s.mpis) == 1:
		n := new(big.Int).SetBytes(k.rsaN)
		e := new(big.Int).SetBytes(k.rsaE)
		// As MDM's library does: 2048- to 8192-bit moduli only.
		if n.BitLen() < 2048 || n.BitLen() > 8192 || !e.IsInt64() || e.Int64() < 3 || e.Int64() > 1<<31-1 {
			return false
		}
		// As long as the modulus (the MPI dropped the leading zeros).
		sig, ok := leftPad(s.mpis[0], len(k.rsaN))
		if !ok {
			return false
		}
		pub := &rsa.PublicKey{N: n, E: int(e.Int64())}
		return rsa.VerifyPKCS1v15(pub, h, digest, sig) == nil
	case k.ed != nil && len(s.mpis) == 2:
		// Legacy EdDSA signs the hash; R and S are 32 bytes each.
		r, ok1 := leftPad(s.mpis[0], 32)
		ss, ok2 := leftPad(s.mpis[1], 32)
		if !ok1 || !ok2 {
			return false
		}
		return ed25519.Verify(k.ed, digest, append(r, ss...))
	}
	return false
}

// digest is the hash the signature is over: the data, the signature's
// hashed part and the v4 trailer (§5.2.4).
func (s *pgpSignature) digest(data []byte) ([]byte, crypto.Hash, bool) {
	var h hash.Hash
	var id crypto.Hash
	switch s.hash {
	case hashSHA256:
		h, id = sha256.New(), crypto.SHA256
	case hashSHA512:
		h, id = sha512.New(), crypto.SHA512
	default:
		return nil, 0, false
	}
	h.Write(data)
	h.Write(s.hashed)
	h.Write([]byte{4, 0xff})
	h.Write(binary.BigEndian.AppendUint32(nil, uint32(len(s.hashed))))
	return h.Sum(nil), id, true
}

// expired reports whether the signature has run out. Most never do (no
// expiry); one made "in the future" by a clock ahead of this one counts.
func (s *pgpSignature) expired() bool {
	if s.expires == 0 {
		return false
	}
	return time.Now().Unix() >= int64(s.created)+int64(s.expires)
}

// leftPad is b with zeros in front up to n bytes; false if it's longer.
func leftPad(b []byte, n int) ([]byte, bool) {
	if len(b) > n {
		return nil, false
	}
	out := make([]byte, n-len(b), n)
	return append(out, b...), true
}
