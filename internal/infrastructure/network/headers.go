// Ported from Mister Download Manager, crates/engine/src/verify.rs and crates/engine/src/naming.rs
// Copyright (C) 2026 Ahmed Nefzaoui and contributors; Go port (C) 2026 the Yellow Flash contributors.
// Translated from Rust to Go and modified. SPDX-License-Identifier: GPL-3.0-or-later

package network

import (
	"encoding/base64"
	"net/http"
	"net/url"
	"strings"
	"unicode/utf8"

	domain "github.com/vaisah/internal/domain/download"
)

// ChecksumFromHeaders returns the strongest whole-file hash the server
// announces. Repr-Digest (RFC 9530), Digest (RFC 3230) and Google Cloud
// Storage's x-goog-hash describe the whole representation. Content-MD5 and
// Content-Digest describe the message body, so they count only when
// fullBody is true. Malformed values are ignored rather than trusted.
func ChecksumFromHeaders(h http.Header, fullBody bool) domain.Checksum {
	var best domain.Checksum
	consider := func(algo domain.ChecksumAlgorithm, b64 string) {
		value, err := base64.StdEncoding.DecodeString(strings.Trim(strings.TrimSpace(b64), ":\""))
		if err != nil || len(value) != algo.Size() || algo.Strength() <= best.Algorithm.Strength() {
			return
		}
		best = domain.Checksum{Algorithm: algo, Value: value}
	}
	names := []string{"Repr-Digest", "Digest"}
	if fullBody {
		names = append(names, "Content-Digest")
		if v := h.Get("Content-MD5"); v != "" {
			consider(domain.MD5, v)
		}
	}
	for _, name := range names {
		for _, v := range h.Values(name) {
			for _, part := range strings.Split(v, ",") {
				algo, value, ok := strings.Cut(strings.TrimSpace(part), "=")
				if !ok {
					continue
				}
				switch strings.ToLower(strings.TrimSpace(algo)) {
				case "sha-512", "sha512":
					consider(domain.SHA512, value)
				case "sha-256", "sha256":
					consider(domain.SHA256, value)
				case "sha", "sha-1", "sha1":
					consider(domain.SHA1, value)
				case "md5":
					consider(domain.MD5, value)
				}
			}
		}
	}
	// x-goog-hash: crc32c=…, md5=… (crc32c is not CRC32, so it is skipped).
	for _, v := range h.Values("X-Goog-Hash") {
		for _, part := range strings.Split(v, ",") {
			if value, ok := strings.CutPrefix(strings.TrimSpace(part), "md5="); ok {
				consider(domain.MD5, value)
			}
		}
	}
	return best
}

// FilenameFromContentDisposition returns the file name a Content-Disposition
// value suggests (RFC 6266), read as browsers do: filename* (RFC 8187,
// charset'language'percent-encoded, UTF-8 or ISO-8859-1) wins over
// filename, which is only the fallback for clients that cannot decode it. A
// plain filename is unquoted (with \ escapes), and percent-encoded UTF-8 in
// it is decoded, as Chromium does. Parameters without a disposition type
// before them ("filename=x", seen in the wild) count too. The result is not
// sanitized; "" when there is none.
func FilenameFromContentDisposition(value string) string {
	params := dispositionParams(value)
	param := func(key string) (string, bool) {
		for _, p := range params {
			if strings.EqualFold(p[0], key) {
				return p[1], true
			}
		}
		return "", false
	}
	// An empty filename* does not hide the plain name.
	if v, ok := param("filename*"); ok {
		if name, ok := decodeExtended(v); ok && strings.TrimSpace(name) != "" {
			return name
		}
	}
	if v, ok := param("filename"); ok {
		if name := decodePlain(v); strings.TrimSpace(name) != "" {
			return name
		}
	}
	return ""
}

// dispositionParams returns the name=value parameters after the
// disposition type, values unquoted. A quoted value may hold ';' and '\"'.
func dispositionParams(value string) [][2]string {
	rest := ""
	switch kind, after, found := strings.Cut(value, ";"); {
	case found && strings.Contains(kind, "="):
		rest = value
	case found:
		rest = after
	case strings.Contains(value, "="):
		rest = value
	}
	var out [][2]string
	rs := []rune(rest)
	for i := 0; i < len(rs); {
		var name strings.Builder
		for ; i < len(rs); i++ {
			if rs[i] == '=' {
				i++
				break
			}
			if rs[i] == ';' { // a parameter without a value
				name.Reset()
				continue
			}
			name.WriteRune(rs[i])
		}
		for i < len(rs) && (rs[i] == ' ' || rs[i] == '\t') {
			i++
		}
		var val strings.Builder
		if i < len(rs) && rs[i] == '"' {
			for i++; i < len(rs); i++ {
				if rs[i] == '\\' && i+1 < len(rs) {
					i++
					val.WriteRune(rs[i])
					continue
				}
				if rs[i] == '"' {
					i++
					break
				}
				val.WriteRune(rs[i])
			}
			// Anything after the closing quote, up to the next parameter.
			for i < len(rs) && rs[i] != ';' {
				i++
			}
			i++
		} else {
			for ; i < len(rs) && rs[i] != ';'; i++ {
				val.WriteRune(rs[i])
			}
			i++
		}
		if n := strings.TrimSpace(name.String()); n != "" {
			out = append(out, [2]string{n, strings.TrimRight(val.String(), " \t")})
		}
	}
	return out
}

// decodeExtended reads "UTF-8”na%C3%AFve.txt" (RFC 8187). Charsets other
// than UTF-8 and ISO-8859-1 (and its subset US-ASCII) are refused, like
// bytes that are not valid UTF-8: the plain filename is used instead.
func decodeExtended(v string) (string, bool) {
	parts := strings.SplitN(v, "'", 3)
	if len(parts) != 3 {
		return "", false
	}
	raw, err := url.PathUnescape(strings.TrimSpace(parts[2]))
	if err != nil {
		return "", false
	}
	switch strings.ToLower(strings.TrimSpace(parts[0])) {
	case "utf-8", "utf8":
		if !utf8.ValidString(raw) {
			return "", false
		}
		return raw, true
	case "iso-8859-1", "iso_8859-1", "latin1", "us-ascii":
		rs := make([]rune, 0, len(raw))
		for i := 0; i < len(raw); i++ {
			rs = append(rs, rune(raw[i]))
		}
		return string(rs), true
	}
	return "", false
}

// decodePlain returns a plain filename as given, or its percent-encoded
// UTF-8 decoded when that is what it holds (non-ASCII once decoded:
// "100%25.txt" stays as it is).
func decodePlain(v string) string {
	if !strings.Contains(v, "%") {
		return v
	}
	decoded, err := url.PathUnescape(v)
	if err != nil || !utf8.ValidString(decoded) {
		return v
	}
	for i := 0; i < len(decoded); i++ {
		if decoded[i] >= utf8.RuneSelf {
			return decoded
		}
	}
	return v
}
