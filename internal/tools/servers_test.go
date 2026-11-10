// Ported from Mister Download Manager, crates/tools/src/servers.rs and pages.rs (tests)
// Copyright (C) 2026 Ahmed Nefzaoui and contributors; Go port (C) 2026 the Yellow Flash contributors.
// Translated from Rust to Go and modified. SPDX-License-Identifier: GPL-3.0-or-later

package tools

import (
	"bytes"
	"encoding/json"
	"net/url"
	"reflect"
	"strings"
	"testing"
)

const (
	deadServer = "rr1---sn-u0opnpxuxa-u0oz.googlevideo.com"
	altServer  = "rr1---sn-hpa7znsz.googlevideo.com"
)

// info is yt-dlp's information for a page whose streams sit on deadServer.
func info(t *testing.T) map[string]any {
	t.Helper()
	link := "https://" + deadServer + "/videoplayback?expire=1&mn=sn-u0opnpxuxa-u0oz%2Csn-hpa7znsz&ms=au%2Crdu&itag=139"
	raw, _ := json.Marshal(map[string]any{
		"id": "Tg6byXUo8RU", "filesize": 12345678901234567,
		"formats": []any{
			map[string]any{"format_id": "139", "url": link},
			map[string]any{"format_id": "249", "url": strings.Replace(link, "itag=139", "itag=249", 1)},
			map[string]any{"format_id": "233", "protocol": "m3u8_native", "url": "https://manifest.googlevideo.com/api/manifest/hls"},
		},
		"requested_formats": []any{map[string]any{"url": link}},
	})
	d := json.NewDecoder(bytes.NewReader(raw))
	d.UseNumber()
	var v map[string]any
	if err := d.Decode(&v); err != nil {
		t.Fatal(err)
	}
	return v
}

func encode(v any) string {
	b, _ := json.Marshal(v)
	return string(b)
}

func ids(v map[string]any) []string {
	var out []string
	for _, f := range v["formats"].([]any) {
		out = append(out, f.(map[string]any)["format_id"].(string))
	}
	return out
}

func TestAnUnreachableServerIsSwappedForItsAlternative(t *testing.T) {
	v := info(t)
	r := Reroute(v, func(h string) bool { return h != deadServer }, func(string, map[string]string) bool { return true })
	if !reflect.DeepEqual(r.Swaps, []Swap{{deadServer, altServer}}) || len(r.LeftOut) != 0 {
		t.Errorf("rerouted %+v", r)
	}
	text := encode(v)
	if strings.Contains(text, deadServer) {
		t.Error("a link still names the dead server")
	}
	if n := strings.Count(text, altServer); n != 2 {
		t.Errorf("%d links on the alternative, want both direct formats", n)
	}
	// The rest of the link (signature, parameters) is untouched, and so are
	// numbers too large for a float.
	if !strings.Contains(text, "https://"+altServer+"/videoplayback?expire=1\\u0026mn=") || !strings.Contains(text, "12345678901234567") {
		t.Errorf("changed: %s", text)
	}
	// HLS (its pieces would still come from the dead server) is left out.
	if strings.Contains(text, "manifest.googlevideo.com") || len(ids(v)) != 2 {
		t.Errorf("formats %v", ids(v))
	}
}

func TestReachableServersAndDeadEndsStay(t *testing.T) {
	yes := func(string, map[string]string) bool { return true }
	v := info(t)
	if r := Reroute(v, func(string) bool { return true }, yes); !r.Empty() || !reflect.DeepEqual(v, info(t)) {
		t.Error("all reachable, yet changed")
	}
	// No alternative answers either: nothing better to point at.
	v = info(t)
	if r := Reroute(v, func(string) bool { return false }, yes); !r.Empty() || !reflect.DeepEqual(v, info(t)) {
		t.Error("nothing reachable, yet changed")
	}
}

func TestAStreamTheAlternativeRefusesIsLeftOut(t *testing.T) {
	v := info(t)
	alive := func(h string) bool { return h != deadServer }
	r := Reroute(v, alive, func(link string, _ map[string]string) bool { return !strings.Contains(link, "itag=249") })
	if !reflect.DeepEqual(r.Swaps, []Swap{{deadServer, altServer}}) || !reflect.DeepEqual(r.LeftOut, []string{"249"}) {
		t.Errorf("rerouted %+v", r)
	}
	if got := ids(v); !reflect.DeepEqual(got, []string{"139"}) {
		t.Errorf("formats %v", got)
	}
	// Nothing served anywhere: every stream on the dead server is out.
	v = info(t)
	r = Reroute(v, alive, func(string, map[string]string) bool { return false })
	if len(r.Swaps) != 0 || !reflect.DeepEqual(r.LeftOut, []string{"139", "249"}) || len(ids(v)) != 0 {
		t.Errorf("rerouted %+v, formats %v", r, ids(v))
	}
}

func TestALeftOutStreamIsNamedByItsHeight(t *testing.T) {
	if got := describe(map[string]any{"format_id": "137", "height": json.Number("1080")}); got != "137 (1080p)" {
		t.Error(got)
	}
	if got := describe(map[string]any{"format_id": "251", "vcodec": "none"}); got != "251 (audio)" {
		t.Error(got)
	}
}

// yt-dlp's words for a server it couldn't reach name the server; other
// failures don't.
func TestUnreachableServersFromYtDlpsFailure(t *testing.T) {
	dns := "[download] Got error: HTTPSConnection(host='rr1---sn-u0opnpxuxa-u0oz.googlevideo.com', port=443): Failed to resolve 'rr1---sn-u0opnpxuxa-u0oz.googlevideo.com' ([Errno 11004] getaddrinfo failed). Giving up after 10 retries"
	if got := UnreachableIn(dns); !reflect.DeepEqual(got, []string{deadServer}) {
		t.Errorf("dns: %v", got)
	}
	if got := UnreachableIn("Unable to connect to rr3---sn-abc.googlevideo.com: timed out"); len(got) != 1 {
		t.Errorf("refused: %v", got)
	}
	if got := UnreachableIn("HTTP Error 403: Forbidden (rr1---sn-x.googlevideo.com)"); len(got) != 0 {
		t.Errorf("403: %v", got)
	}
	if got := UnreachableIn("Failed to resolve 'example.com'"); len(got) != 0 {
		t.Errorf("another host: %v", got)
	}
	if !FormatUnavailable("[youtube] jvfC98jCBuk: Requested format is not available. Use --list-formats for a list of available formats") || FormatUnavailable("HTTP Error 403") {
		t.Error("FormatUnavailable")
	}
}

func TestABlackholedNameIsUnreachable(t *testing.T) {
	// A hosts-file block answers 0.0.0.0.
	if Reachable("0.0.0.0") {
		t.Error("0.0.0.0")
	}
}

func parsed(t *testing.T, s string) *url.URL {
	t.Helper()
	u, err := url.Parse(s)
	if err != nil {
		t.Fatal(err)
	}
	return u
}

func TestGalleryPagesAreRecognised(t *testing.T) {
	for _, s := range []string{
		"https://commons.wikimedia.org/wiki/Category:Featured_pictures_of_birds",
		"https://www.flickr.com/photos/nasawebbtelescope/albums/72177720313923911",
		"https://www.flickr.com/photos/nasawebbtelescope/",
		"https://www.deviantart.com/someone/gallery",
		"https://imgur.com/a/abc123",
		"https://www.pixiv.net/en/users/123456",
		"https://danbooru.donmai.us/posts?tags=landscape",
		"https://bsky.app/profile/nasa.gov",
	} {
		if !IsGalleryPage(parsed(t, s)) {
			t.Errorf("not a gallery: %s", s)
		}
	}
	// A page that's no gallery, a picture's own address, a video page.
	for _, s := range []string{
		"https://commons.wikimedia.org/wiki/Main_Page",
		"https://upload.wikimedia.org/wikipedia/commons/9/9d/Rhea.jpg",
		"https://www.flickr.com/photos/x/123/in/album/a.JPG",
		"https://www.youtube.com/watch?v=Tg6byXUo8RU",
		"ftp://commons.wikimedia.org/wiki/Category:Birds",
	} {
		if IsGalleryPage(parsed(t, s)) {
			t.Errorf("a gallery: %s", s)
		}
	}
}

func TestVideoPagesAreRecognised(t *testing.T) {
	for _, s := range []string{
		// A watch page from a mix (list=RD…, start_radio): one video.
		"https://www.youtube.com/watch?v=Tg6byXUo8RU&list=RDTg6byXUo8RU&start_radio=1",
		"https://youtube.com/watch?v=Tg6byXUo8RU",
		"https://m.youtube.com/watch?v=Tg6byXUo8RU&t=42s",
		"https://music.youtube.com/watch?v=Tg6byXUo8RU",
		"https://www.youtube.com/shorts/abcDEF12345",
		"https://youtu.be/Tg6byXUo8RU?si=x",
		"https://vimeo.com/76979871",
		"https://www.dailymotion.com/video/x8abcd",
		"https://www.twitch.tv/videos/123456",
		"https://www.tiktok.com/@someone/video/7300000000000000000",
		"https://x.com/someone/status/1800000000000000000",
		"https://www.instagram.com/reel/Cabc123/",
		"https://www.reddit.com/r/videos/comments/abc123/title/",
		"https://soundcloud.com/artist/track",
	} {
		if !IsVideoPage(parsed(t, s)) {
			t.Errorf("not a video page: %s", s)
		}
	}
	for _, s := range []string{
		"https://www.youtube.com/",
		"https://www.youtube.com/watch",
		"https://www.youtube.com/@channel/videos",
		"https://www.youtube.com/results?search_query=x",
		"https://www.youtube.com/playlist?list=PLx",
		"https://vimeo.com/channels/staffpicks",
		"https://example.com/video.mp4",
		"https://x.com/someone",
		"ftp://youtube.com/watch?v=x",
	} {
		if IsVideoPage(parsed(t, s)) {
			t.Errorf("a video page: %s", s)
		}
	}
}
