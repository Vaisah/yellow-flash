// Ported from Mister Download Manager, crates/tools/src/resolve.rs (tests)
// Copyright (C) 2026 Ahmed Nefzaoui and contributors; Go port (C) 2026 the Yellow Flash contributors.
// Translated from Rust to Go and modified. SPDX-License-Identifier: GPL-3.0-or-later

package tools

import (
	"reflect"
	"strings"
	"testing"
)

func labels(cs []Choice, keepAudio bool) []string {
	var out []string
	for _, c := range cs {
		if keepAudio || !strings.HasPrefix(c.Label, AudioOnly) {
			out = append(out, c.Label)
		}
	}
	return out
}

func parse(t *testing.T, raw string) Video {
	t.Helper()
	v, ok := ParseVideo([]byte(raw))
	if !ok {
		t.Fatalf("not parsed: %s", raw)
	}
	return v
}

func TestAStreamsVariantsByBitrate(t *testing.T) {
	f := func(id string, tbr float64) Format {
		return Format{ID: id, Ext: "mp4", Video: true, Audio: true, TBR: tbr}
	}
	v := Video{Title: "t", Formats: []Format{f("hls-640", 640), f("hls-1280", 1280), f("hls-1280b", 1280)}}
	if got := labels(Choices(v, true), false); !reflect.DeepEqual(got, []string{"MP4 1280 kbps", "MP4 640 kbps"}) {
		t.Errorf("best first, twins once: %q", got)
	}
}

const sample = `{
	"title": "A talk", "duration": 61.5,
	"formats": [
		{"format_id": "140", "ext": "m4a", "vcodec": "none", "acodec": "mp4a", "filesize": 1000000},
		{"format_id": "18", "ext": "mp4", "height": 360, "vcodec": "avc1", "acodec": "mp4a", "filesize": 5000000},
		{"format_id": "136", "ext": "mp4", "height": 720, "fps": 30, "vcodec": "avc1", "acodec": "none", "filesize": 20000000},
		{"format_id": "299", "ext": "mp4", "height": 1080, "fps": 60, "vcodec": "avc1", "acodec": "none", "filesize_approx": 60000000}
	]}`

func TestChoicesWithAndWithoutMerging(t *testing.T) {
	v := parse(t, sample)
	if v.Title != "A talk" {
		t.Error(v.Title)
	}
	merged := Choices(v, true)
	want := []string{"1080p 60fps MP4", "720p MP4", "360p MP4", "Audio only original M4A", "Audio only MP3 VBR",
		"Audio only MP3 320 kbps", "Audio only MP3 256 kbps", "Audio only MP3 192 kbps", "Audio only MP3 128 kbps",
		"Audio only OPUS", "Audio only FLAC"}
	if got := labels(merged, true); !reflect.DeepEqual(got, want) {
		t.Fatalf("merged %q", got)
	}
	// Opus from M4A is encoded (160 kbit/s); FLAC about 900 kbit/s (61.5 s).
	if c := merged[len(merged)-2]; c.Selector != OpusSelector || c.Size != 1_230_000 {
		t.Errorf("opus %+v", c)
	}
	if c := merged[len(merged)-1]; c.Selector != FlacSelector || c.Size != 6_918_750 {
		t.Errorf("flac %+v", c)
	}
	if merged[0].Size != 61_000_000 {
		t.Errorf("video + best audio: %d", merged[0].Size)
	}
	// MP3: converted from the best sound, sized from the duration.
	if c := merged[5]; c.Selector != "mp3:320" || c.Ext != "mp3" || c.Size != 2_460_000 {
		t.Errorf("mp3 %+v", c)
	}
	// Without ffmpeg only the format that has both survives, and no MP3.
	plain := Choices(v, false)
	if got := labels(plain, true); !reflect.DeepEqual(got, []string{"360p MP4", "Audio only original M4A"}) || plain[0].Selector != "b[height=360]" {
		t.Errorf("plain %q %+v", got, plain[0])
	}
}

// What yt-dlp lists on YouTube with no JavaScript runtime: video-only
// streams and sound, nothing carrying both. Without ffmpeg only the sound is
// offered, and the window has to say that ffmpeg brings the qualities.
func TestOnlyTheSoundWithoutFfmpegAndAMuxedFormat(t *testing.T) {
	v := parse(t, `{"title": "T", "formats": [
		{"format_id": "sb0", "ext": "mhtml", "vcodec": "none", "acodec": "none"},
		{"format_id": "140-0", "ext": "m4a", "vcodec": "none", "acodec": "mp4a.40.2", "filesize": 900},
		{"format_id": "312", "ext": "mp4", "height": 1080, "vcodec": "avc1.64002A", "acodec": "none", "filesize": 9000},
		{"format_id": "230", "ext": "mp4", "height": 360, "vcodec": "avc1.4D401E", "acodec": "none", "filesize": 1000}]}`)
	if got := labels(Choices(v, false), true); !reflect.DeepEqual(got, []string{"Audio only original M4A"}) {
		t.Errorf("without: %q", got)
	}
	if !NeedsMerging(v) {
		t.Error("merging would help")
	}
	if got := labels(Choices(v, true), true); !reflect.DeepEqual(got[:2], []string{"1080p MP4", "360p MP4"}) {
		t.Errorf("with: %q", got)
	}
	// A format carrying both, as good as any video: nothing more to get.
	if NeedsMerging(parse(t, `{"title": "T", "formats": [
		{"format_id": "18", "ext": "mp4", "height": 360, "vcodec": "avc1", "acodec": "mp4a"},
		{"format_id": "230", "ext": "mp4", "height": 360, "vcodec": "avc1", "acodec": "none"}]}`)) {
		t.Error("muxed")
	}
	if NeedsMerging(parse(t, `{"title": "T", "formats": [{"format_id": "140", "ext": "m4a", "vcodec": "none", "acodec": "mp4a"}]}`)) {
		t.Error("sound alone")
	}
}

func TestAudioConversions(t *testing.T) {
	for sel, want := range map[string]*AudioOut{
		"mp3:320": {"mp3", "320K"}, OpusSelector: {Format: "opus"}, FlacSelector: {Format: "flac"},
		"ba": nil, "bv*[height=720]+ba/b[height=720]": nil, "opus:x": nil,
	} {
		got, ok := AudioConversion(sel)
		if (want == nil) == ok || want != nil && got != *want {
			t.Errorf("%s: %+v %v", sel, got, ok)
		}
	}
	if !IsAudio(Choice{Label: "x", Selector: FlacSelector, Ext: "flac"}) {
		t.Error("FLAC is audio")
	}
}

func TestMP3SelectorsAreTheOfferedOnesOnly(t *testing.T) {
	if q, _ := MP3Quality("mp3:0"); q != "0" {
		t.Error(q)
	}
	if q, _ := MP3Quality("mp3:192"); q != "192K" {
		t.Error(q)
	}
	for _, other := range []string{"mp3:64", "mp3:", "mp3:-1", "mp3:320; rm", "mp3:+192", "mp3:0320", "mp3:000", "mp3: 192", "MP3:192", "ba", "b[height=360]"} {
		if q, ok := MP3Quality(other); ok {
			t.Errorf("%s: %s", other, q)
		}
	}
}

func TestPlainFilesWithoutCodecOrResolution(t *testing.T) {
	v := parse(t, `{"title": "Bunny", "formats": [
		{"format_id": "ogv", "ext": "ogv", "filesize": 100},
		{"format_id": "mp4", "ext": "mp4", "filesize": 200}]}`)
	c := Choices(v, false)
	if got := labels(c, true); !reflect.DeepEqual(got, []string{"OGV", "MP4"}) || c[1].Selector != "mp4" {
		t.Errorf("%q", got)
	}
}

func TestAudioWithoutCodecInfoIsNotAVideoChoice(t *testing.T) {
	v := parse(t, `{"title": "T", "formats": [
		{"format_id": "773", "ext": "m4a", "filesize": 100},
		{"format_id": "140", "ext": "m4a", "vcodec": "none", "acodec": "mp4a", "filesize": 90},
		{"format_id": "18", "ext": "mp4", "height": 360, "vcodec": "avc1", "acodec": "mp4a", "filesize": 500}]}`)
	if got := labels(Choices(v, false), true); !reflect.DeepEqual(got, []string{"360p MP4", "Audio only original M4A"}) {
		t.Errorf("%q", got)
	}
}

// A page's subtitles, as yt-dlp -J lists them: the uploader's by name, the
// chat replay left out; of the automatic ones only the spoken language's and
// the translations into the interface language and English, and none the
// uploader already has.
func TestSubtitleLanguagesWorthOffering(t *testing.T) {
	v := parse(t, `{"title": "T", "language": "en", "formats": [],
		"subtitles": {
			"live_chat": [{"ext": "json"}],
			"fr": [{"ext": "vtt", "name": "French"}, {"ext": "srt", "name": "French"}],
			"de": [{"ext": "vtt"}],
			"empty": []
		},
		"automatic_captions": {
			"en-orig": [{"ext": "vtt", "name": "English (Original)"}],
			"en": [{"ext": "vtt", "name": "English"}],
			"fr": [{"ext": "vtt", "name": "French"}],
			"ar": [{"ext": "vtt", "name": "Arabic"}],
			"ja": [{"ext": "vtt", "name": "Japanese"}],
			"zu": [{"ext": "vtt", "name": "Zulu"}]
		}}`)
	var names []string
	for _, s := range v.Subtitles {
		names = append(names, s.Name)
	}
	if !reflect.DeepEqual(names, []string{"de", "French"}) || v.Language != "en" {
		t.Errorf("by name; no name: the code: %q %q", names, v.Language)
	}
	want := []SubChoice{{"de", "de", false}, {"fr", "French", false}, {"ar", "Arabic", true},
		{"en", "English", true}, {"en-orig", "English (Original)", true}}
	if got := SubChoices(v, "ar"); !reflect.DeepEqual(got, want) {
		t.Errorf("offered %+v", got)
	}
	if got := SubChoices(parse(t, `{"title": "T", "formats": []}`), "en"); len(got) != 0 {
		t.Errorf("none: %+v", got)
	}
}

func TestJunkIsNotAVideo(t *testing.T) {
	if _, ok := ParseVideo([]byte("not json")); ok {
		t.Error("junk parsed")
	}
}

func TestAListPageSaysWhatItIs(t *testing.T) {
	looked, ok := ParseLooked([]byte(`{"_type": "playlist", "title": " Talks ", "playlist_count": 6000, "entries": [{}]}`))
	if !ok || looked.List == nil || *looked.List != (ListPage{Title: "Talks", Total: 6000}) {
		t.Errorf("list %+v", looked.List)
	}
	looked, _ = ParseLooked([]byte(sample))
	if looked.Video == nil || looked.Video.Title != "A talk" {
		t.Error("a video")
	}
	// A size given as a fraction (filesize_approx) is read, not refused.
	v := parse(t, `{"title": "T", "formats": [{"format_id": "1", "ext": "mp4", "height": 720, "filesize_approx": 1234.5}]}`)
	if v.Formats[0].Size != 1234 {
		t.Errorf("size %d", v.Formats[0].Size)
	}
}
