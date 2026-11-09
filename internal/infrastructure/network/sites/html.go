// Ported from Mister Download Manager, crates/engine/src/html.rs
// Copyright (C) 2026 Ahmed Nefzaoui and contributors; Go port (C) 2026 the Yellow Flash contributors.
// Translated from Rust to Go and modified. SPDX-License-Identifier: GPL-3.0-or-later

package sites

import (
	"strconv"
	"strings"
	"unicode/utf8"
)

// The little of HTML a site's download page needs: tags, their attributes
// and an element's text, as browsers read well-formed pages; not a parser.

type tag struct {
	start int    // byte offset of '<'
	text  string // from '<' to its '>' (or the end)
}

// tags returns each <name …> tag in page (the name in any case, as a whole
// word: <meta but not <metadata).
func tags(page, name string) []tag {
	lower := asciiLower(page)
	open := "<" + strings.ToLower(name)
	var out []tag
	for at := 0; ; {
		i := strings.Index(lower[at:], open)
		if i < 0 {
			return out
		}
		start := at + i
		after := start + len(open)
		at = after
		if after < len(lower) {
			if c := lower[after]; !(isSpace(c) || c == '>' || c == '/') {
				continue
			}
		}
		end := len(lower)
		if e := strings.IndexByte(lower[after:], '>'); e >= 0 {
			end = after + e + 1
		}
		at = end
		out = append(out, tag{start: start, text: page[start:end]})
	}
}

// asciiLower lowers ASCII letters only, so byte offsets match the input's.
func asciiLower(s string) string {
	b := []byte(s)
	for i, c := range b {
		if c >= 'A' && c <= 'Z' {
			b[i] = c + 32
		}
	}
	return string(b)
}

func isSpace(c byte) bool { return c == ' ' || c == '\t' || c == '\n' || c == '\r' || c == '\f' }

// attribute returns the value of attribute name (any case) in the tag, quoted
// or not, as written: entities are left for decode. Attributes are read in
// order, so a name inside another's value or a longer name (data-id) does
// not count.
func attribute(t, name string) (string, bool) {
	rest := strings.TrimPrefix(t, "<")
	// Past the tag's own name.
	i := 0
	for i < len(rest) && !(isSpace(rest[i]) || rest[i] == '>' || rest[i] == '/') {
		i++
	}
	rest = rest[i:]
	for {
		rest = strings.TrimLeft(rest, " \t\n\r\f/")
		if rest == "" || rest[0] == '>' {
			return "", false
		}
		end := 0
		for end < len(rest) && !(isSpace(rest[end]) || rest[end] == '=' || rest[end] == '>' || rest[end] == '/') {
			end++
		}
		if end == 0 { // a stray '='
			rest = rest[1:]
			continue
		}
		key := rest[:end]
		rest = strings.TrimLeft(rest[end:], " \t\n\r\f")
		value := ""
		if after, ok := strings.CutPrefix(rest, "="); ok {
			after = strings.TrimLeft(after, " \t\n\r\f")
			if after != "" && (after[0] == '"' || after[0] == '\'') {
				q := after[0]
				inner := after[1:]
				if close := strings.IndexByte(inner, q); close < 0 {
					value, rest = inner, ""
				} else {
					value, rest = inner[:close], inner[close+1:]
				}
			} else {
				close := 0
				for close < len(after) && !(isSpace(after[close]) || after[close] == '>') {
					close++
				}
				value, rest = after[:close], after[close:]
			}
		}
		if strings.EqualFold(key, name) {
			return value, true
		}
	}
}

// decode decodes &amp; &lt; &gt; &quot; &apos; &nbsp;, &#NN; and &#xNN;.
// Anything else stays as written.
func decode(s string) string {
	var out strings.Builder
	for {
		i := strings.IndexByte(s, '&')
		if i < 0 {
			out.WriteString(s)
			return out.String()
		}
		out.WriteString(s[:i])
		s = s[i:]
		n := strings.IndexByte(s[1:], ';')
		var r rune = -1
		if n >= 0 && n <= 10 {
			switch name := s[1 : 1+n]; name {
			case "amp":
				r = '&'
			case "lt":
				r = '<'
			case "gt":
				r = '>'
			case "quot":
				r = '"'
			case "apos":
				r = '\''
			case "nbsp":
				r = ' '
			default:
				if num, ok := strings.CutPrefix(name, "#"); ok {
					var v uint64
					var err error
					if hex, ok := strings.CutPrefix(strings.ToLower(num), "x"); ok {
						v, err = strconv.ParseUint(hex, 16, 32)
					} else {
						v, err = strconv.ParseUint(num, 10, 32)
					}
					if err == nil && utf8.ValidRune(rune(v)) {
						r = rune(v)
					}
				}
			}
		}
		if r < 0 {
			out.WriteByte('&')
			s = s[1:]
			continue
		}
		out.WriteRune(r)
		s = s[n+2:]
	}
}

// textOfClass returns the text of the first element whose class list holds
// class: tags stripped, references decoded, white space collapsed. It reads
// up to the first closing tag of the element's own name.
func textOfClass(page, class string) (string, bool) {
	lower := asciiLower(page)
	for at := 0; ; {
		i := strings.IndexByte(lower[at:], '<')
		if i < 0 {
			return "", false
		}
		start := at + i
		at = start + 1
		e := strings.IndexByte(lower[start:], '>')
		if e < 0 {
			return "", false
		}
		end := start + e + 1
		classes, ok := attribute(page[start:end], "class")
		if !ok || !in(class, strings.Fields(classes)...) {
			continue
		}
		n := start + 1
		for n < len(lower) && (lower[n] >= 'a' && lower[n] <= 'z' || lower[n] >= '0' && lower[n] <= '9') {
			n++
		}
		innerEnd := len(page)
		if c := strings.Index(lower[end:], "</"+lower[start+1:n]); c >= 0 {
			innerEnd = end + c
		}
		text := strings.Join(strings.Fields(decode(stripTags(page[end:innerEnd]))), " ")
		return text, text != ""
	}
}

// stripTags removes tags (<a href=…>name</a> → name).
func stripTags(html string) string {
	var out strings.Builder
	inTag := false
	for _, c := range html {
		switch {
		case c == '<':
			inTag = true
		case c == '>' && inTag:
			inTag = false
		case !inTag:
			out.WriteRune(c)
		}
	}
	return out.String()
}
