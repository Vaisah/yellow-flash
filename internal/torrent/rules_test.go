// Ported from Mister Download Manager, crates/torrent/src/{names,trackers,blocklist}.rs (tests)
// Copyright (C) 2026 Ahmed Nefzaoui and contributors; Go port (C) 2026 the Yellow Flash contributors.
// Translated from Rust to Go and modified. SPDX-License-Identifier: GPL-3.0-or-later

package torrent

import (
	"bytes"
	"compress/gzip"
	"errors"
	"fmt"
	"net/netip"
	"slices"
	"strings"
	"testing"
)

func names(list ...[]string) [][]string { return list }

func p(parts ...string) []string { return parts }

func TestOrdinaryTorrentsPass(t *testing.T) {
	ok := names(p("Big Buck Bunny", "bunny.mp4"), p("Big Buck Bunny", "Subs", "en.srt"), p("readme.txt"), p("Ünïcødé", "файл.txt"))
	if err := checkNamesFor(ok, true, true); err != nil {
		t.Error(err)
	}
	if err := checkNamesFor(ok, false, false); err != nil {
		t.Error(err)
	}
}

func TestColonsAreRefusedOnWindows(t *testing.T) {
	for _, bad := range []string{"a:b.bin", "Q:", "q:x.txt", "x.bin:hidden"} {
		var u *UnsafeName
		if err := checkNamesFor(names(p("folder", bad)), true, true); !errors.As(err, &u) || u.Path != "folder/"+bad || !strings.Contains(u.Why, "Windows") {
			t.Errorf("%s: %v", bad, err)
		}
	}
	// Elsewhere a : is an ordinary character (Linux).
	if err := checkNamesFor(names(p("Movie: Part 2", "a.mkv")), false, false); err != nil {
		t.Error(err)
	}
}

func TestWindowsNamesItCannotWriteAreRefused(t *testing.T) {
	for _, bad := range []string{"CON", "nul.txt", "Com1", "LPT9.log", "COM¹", "CONIN$", "a?.txt", "a<b", "x\"y", "trailing.", "trailing ", "con .txt"} {
		if checkNamesFor(names(p(bad)), true, true) == nil {
			t.Errorf("%q accepted", bad)
		}
	}
	// Not devices: longer names, or the number missing.
	for _, good := range []string{"CONSOLE.txt", "COM", "COM10", "nullable", "auxiliary.c"} {
		if err := checkNamesFor(names(p(good)), true, true); err != nil {
			t.Errorf("%q: %v", good, err)
		}
	}
}

func TestTraversalSeparatorsAndControlCharactersEverywhere(t *testing.T) {
	for _, bad := range []string{"", ".", "..", "a/b", `a\b`, "tab\there", "nul\x00byte", "bad\xffutf8"} {
		if checkNamesFor(names(p("dir", bad)), true, true) == nil || checkNamesFor(names(p("dir", bad)), false, false) == nil {
			t.Errorf("%q accepted", bad)
		}
	}
	if checkNamesFor(names(p()), true, true) == nil {
		t.Error("a file without a name")
	}
}

func TestCaseTwinsCollideWhereCaseIsIgnored(t *testing.T) {
	twins := names(p("Dir", "A.txt"), p("dir", "a.txt"))
	if checkNamesFor(twins, true, true) == nil {
		t.Error("case twins on Windows")
	}
	if err := checkNamesFor(twins, false, false); err != nil {
		t.Error(err)
	}
	if checkNamesFor(names(p("a.txt"), p("a.txt")), false, false) == nil {
		t.Error("exact twins")
	}
}

func TestLocalAndOddTrackersAreDropped(t *testing.T) {
	for _, ok := range []string{"http://tracker.example.org:80/announce", "https://t.example/announce",
		"udp://tracker.opentrackr.org:1337/announce", "http://8.8.8.8/announce"} {
		if !TrackerAllowed(ok, false) {
			t.Errorf("%s dropped", ok)
		}
	}
	for _, bad := range []string{"http://127.0.0.1:8080/announce", "http://localhost/announce", "http://router.local/announce",
		"http://192.168.1.1/announce", "http://10.0.0.5/a", "http://169.254.1.1/a", "http://100.64.0.1/a", "http://[::1]/a",
		"http://[fd00::1]/a", "http://[::ffff:192.168.0.1]/a", "file:///etc/passwd", "wss://t.example/a", "not a url",
		// Numeric forms some resolvers read as addresses.
		"http://0x7f.1/a", "http://2130706433/a", "http://127.1/a"} {
		if TrackerAllowed(bad, false) {
			t.Errorf("%s allowed", bad)
		}
	}
	if TrackerAllowed("udp://tracker.example:1337/a", true) {
		t.Error("UDP goes around the proxy")
	}
	if !TrackerAllowed("https://t.example/a", true) {
		t.Error("HTTPS through the proxy")
	}
}

func TestMagnetsLoseOnlyTheTrackersDropped(t *testing.T) {
	m := "magnet:?xt=urn:btih:abc&dn=x&tr=udp%3A%2F%2Ft.example%3A1337&tr=http%3A%2F%2F127.0.0.1%2Fa"
	if got := FilterMagnet(m, false); got != "magnet:?xt=urn:btih:abc&dn=x&tr=udp%3A%2F%2Ft.example%3A1337" {
		t.Error(got)
	}
	if got := FilterMagnet(m, true); got != "magnet:?xt=urn:btih:abc&dn=x" {
		t.Error(got)
	}
	if got := TrackersInMagnet(m + "&tr=udp%3A%2F%2Ft.example%3A1337&tr="); !slices.Equal(got, []string{"udp://t.example:1337", "http://127.0.0.1/a"}) {
		t.Errorf("%q", got)
	}
	if len(TrackersInMagnet("magnet:?xt=urn:btih:abc")) != 0 {
		t.Error("none")
	}
}

const testInfo = "d6:lengthi5e4:name1:a12:piece lengthi16384e6:pieces20:aaaaaaaaaaaaaaaaaaaae"

func bs(v string) string { return fmt.Sprintf("%d:%s", len(v), v) }

func withInfo(head string) []byte { return []byte("d" + head + "4:info" + testInfo + "e") }

// New trackers come after the ones there, each once, keys still sorted, the
// info dictionary byte for byte.
func TestTrackersAreAddedAfterTheOnesThere(t *testing.T) {
	a, b, c := "http://a.example/announce", "udp://b.example:1337/announce", "https://c.example/announce"
	// Only announce: a list with it first, then the new ones, each a tier.
	one := withInfo("8:announce" + bs(a))
	out, ok := AddTrackers(one, []string{b, a, b, " ", c})
	want := withInfo("8:announce" + bs(a) + "13:announce-listll" + bs(a) + "el" + bs(b) + "el" + bs(c) + "ee")
	if !ok || !bytes.Equal(out, want) {
		t.Errorf("got  %s\nwant %s", out, want)
	}
	if got := TrackersListed(out); !slices.Equal(got, []string{a, b, c}) {
		t.Errorf("%q", got)
	}
	// A list already: kept as it is, the new one after; a comment stays.
	listed := withInfo("8:announce" + bs(a) + "13:announce-listll" + bs(a) + bs(b) + "ee7:comment2:hi")
	out, _ = AddTrackers(listed, []string{c})
	want = withInfo("8:announce" + bs(a) + "13:announce-listll" + bs(a) + bs(b) + "el" + bs(c) + "ee7:comment2:hi")
	if !bytes.Equal(out, want) {
		t.Errorf("got  %s\nwant %s", out, want)
	}
	// None at all (a magnet's): announce and a list.
	out, _ = AddTrackers(withInfo(""), []string{b})
	if want := withInfo("8:announce" + bs(b) + "13:announce-listll" + bs(b) + "ee"); !bytes.Equal(out, want) {
		t.Errorf("got  %s\nwant %s", out, want)
	}
	// Nothing new: the same bytes.
	if again, _ := AddTrackers(out, []string{b}); !bytes.Equal(again, out) {
		t.Error("changed with nothing new")
	}
	if _, ok := AddTrackers([]byte("junk"), []string{a}); ok {
		t.Error("junk")
	}
	if out, _ := AddTrackers(one, []string{c}); !bytes.Contains(out, []byte(testInfo)) {
		t.Error("the info bytes changed")
	}
}

// The trackers change; the info dictionary, and so the hash, doesn't.
func TestTorrentsKeepTheirInfoByteForByte(t *testing.T) {
	local, udp := "http://127.0.0.1/announce", "udp://t.example:1337/a"
	torrent := withInfo("8:announce" + bs(local) + "13:announce-listll" + bs(local) + "el" + bs(udp) + "ee")
	out, ok := FilterTorrent(torrent, false)
	if want := withInfo("13:announce-listll" + bs(udp) + "ee"); !ok || !bytes.Equal(out, want) {
		t.Errorf("got  %s\nwant %s", out, want)
	}
	// With a proxy nothing is left: no tracker keys at all.
	if out, _ := FilterTorrent(torrent, true); !bytes.Equal(out, withInfo("")) {
		t.Errorf("proxied %s", out)
	}
	for _, bad := range []string{"not bencode", "d4:infoi1e"} {
		if _, ok := FilterTorrent([]byte(bad), false); ok {
			t.Errorf("%q", bad)
		}
	}
	// Deep nesting is refused, not followed.
	deep := "d4:info" + strings.Repeat("l", 1000) + strings.Repeat("e", 1000) + "e"
	if _, ok := FilterTorrent([]byte(deep), false); ok {
		t.Error("deep nesting")
	}
}

func TestP2PLinesAreKept(t *testing.T) {
	c, ok := ConvertBlocklist([]byte("# a comment\nSome ISP:1.2.3.0-1.2.3.255\nv6 net:2001:db8::-2001:db8::ffff\n"))
	if !ok || c.Text != "yf:1.2.3.0-1.2.3.255\nyf:2001:db8::-2001:db8::ffff\n" || len(c.Ranges) != 2 || c.Skipped != 0 {
		t.Errorf("%+v", c)
	}
	// A name with a comma is still P2P.
	if c, _ := ConvertBlocklist([]byte("Acme, Inc:4.4.4.0-4.4.4.255\n")); c.Text != "yf:4.4.4.0-4.4.4.255\n" {
		t.Errorf("%q", c.Text)
	}
}

// eMule's format, zero-padded, and its "allowed" levels.
func TestEmuleDatIsConverted(t *testing.T) {
	c, _ := ConvertBlocklist([]byte("001.002.003.000 - 001.002.003.255 , 000 , Bad\n010.000.000.000 - 010.255.255.255 , 200 , Allowed\n"))
	if c.Text != "yf:1.2.3.0-1.2.3.255\n" || len(c.Ranges) != 1 || c.Skipped != 1 {
		t.Errorf("%+v", c)
	}
	if !c.Blocks(netip.MustParseAddr("1.2.3.4")) || c.Blocks(netip.MustParseAddr("10.0.0.1")) || !c.Blocks(netip.MustParseAddr("::ffff:1.2.3.9")) {
		t.Error("Blocks")
	}
}

func TestGzippedListsAreRead(t *testing.T) {
	var b bytes.Buffer
	zw := gzip.NewWriter(&b)
	_, _ = zw.Write([]byte("x:5.6.7.8-5.6.7.9\n"))
	_ = zw.Close()
	if c, ok := ConvertBlocklist(b.Bytes()); !ok || c.Text != "yf:5.6.7.8-5.6.7.9\n" {
		t.Errorf("%+v", c)
	}
}

func TestWhatIsntAListIsNone(t *testing.T) {
	for _, bad := range []string{"<html>not found</html>", ""} {
		if _, ok := ConvertBlocklist([]byte(bad)); ok {
			t.Errorf("%q", bad)
		}
	}
	// Reversed, mixed families, bad parts: skipped.
	c, _ := ConvertBlocklist([]byte("a:9.9.9.9-1.1.1.1\nb:1.1.1.1-::1\nc:1.2.3-1.2.4\nd:1.1.1.1-1.1.1.2\n"))
	if len(c.Ranges) != 1 || c.Skipped != 3 {
		t.Errorf("%+v", c)
	}
}
