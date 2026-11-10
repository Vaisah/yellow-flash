// Ported from Mister Download Manager, crates/tools/src/resolve.rs
// Copyright (C) 2026 Ahmed Nefzaoui and contributors; Go port (C) 2026 the Yellow Flash contributors.
// Translated from Rust to Go and modified. SPDX-License-Identifier: GPL-3.0-or-later

package tools

// What a page holds, by yt-dlp -J: the title and the formats, turned into
// the few choices a person picks from (one per resolution, plus audio
// only), as IDM's video panel offers them.

import (
	"context"
	"encoding/json"
	"fmt"
	"math"
	"slices"
	"sort"
	"strconv"
	"strings"
	"time"
)

// Video is what a video page holds.
type Video struct {
	Title string
	// Duration is in seconds; 0 when unknown.
	Duration float64
	Formats  []Format
	// Subtitles are the uploader's, by name; Automatic the site's automatic
	// captions (speech recognition, and their machine translations).
	Subtitles, Automatic []Track
	// Language is the one the video is spoken in, when the site says.
	Language string
}

// Track is one language of subtitles a page has.
type Track struct {
	// Code is yt-dlp's language code, what --sub-langs takes (en, pt-BR,
	// en-orig); Name the site's name for it, else the code.
	Code, Name string
}

// Format is one of a page's formats.
type Format struct {
	ID, Ext      string
	Height       int     // 0: unknown
	FPS          float64 // 0: unknown
	Video, Audio bool
	// Size is in bytes, exact or estimated; 0: unknown.
	Size int64
	// TBR is the bitrate in kbit/s, when the site says; 0: unknown.
	TBR float64
}

// Choice is one line of the choice list: what to show and what to ask
// yt-dlp for.
type Choice struct {
	Label string `json:"label"`
	// Selector is yt-dlp's -f selector (or one of the audio conversions).
	Selector string `json:"selector"`
	// Ext is the container of the result (for the file name).
	Ext  string `json:"ext"`
	Size int64  `json:"size"`
}

type rawFormat struct {
	FormatID       string   `json:"format_id"`
	Ext            string   `json:"ext"`
	Height         *float64 `json:"height"`
	FPS            *float64 `json:"fps"`
	VCodec         *string  `json:"vcodec"`
	ACodec         *string  `json:"acodec"`
	FileSize       *float64 `json:"filesize"`
	FileSizeApprox *float64 `json:"filesize_approx"`
	TBR            *float64 `json:"tbr"`
}

type rawSub struct {
	Name *string `json:"name"`
}

type rawVideo struct {
	Kind              string              `json:"_type"`
	Title             *string             `json:"title"`
	Duration          *float64            `json:"duration"`
	PlaylistCount     *float64            `json:"playlist_count"`
	Formats           []rawFormat         `json:"formats"`
	Subtitles         map[string][]rawSub `json:"subtitles"`
	AutomaticCaptions map[string][]rawSub `json:"automatic_captions"`
	Language          *string             `json:"language"`
}

// notSubtitles aren't subtitles, though yt-dlp lists them with them: a live
// stream's chat replay, comments laid over the picture.
var notSubtitles = []string{"live_chat", "danmaku"}

// tracks are a page's subtitle languages, by name (the code when the site
// gives none).
func tracks(raw map[string][]rawSub) []Track {
	codes := make([]string, 0, len(raw))
	for c := range raw {
		codes = append(codes, c)
	}
	sort.Strings(codes)
	var out []Track
	for _, code := range codes {
		files := raw[code]
		if len(files) == 0 || slices.Contains(notSubtitles, code) {
			continue
		}
		name := code
		for _, f := range files {
			if f.Name != nil {
				if n := strings.TrimSpace(*f.Name); n != "" {
					name = n
				}
				break
			}
		}
		out = append(out, Track{Code: code, Name: name})
	}
	sort.SliceStable(out, func(i, j int) bool { return strings.ToLower(out[i].Name) < strings.ToLower(out[j].Name) })
	return out
}

func orZero(f *float64) float64 {
	if f == nil || math.IsNaN(*f) || math.IsInf(*f, 0) {
		return 0
	}
	return *f
}

// ParseVideo reads yt-dlp -J output as a video.
func ParseVideo(raw []byte) (Video, bool) {
	var r rawVideo
	if json.Unmarshal(raw, &r) != nil {
		return Video{}, false
	}
	return videoOf(r), true
}

func videoOf(r rawVideo) Video {
	// yt-dlp: "none" means the stream isn't there; a missing codec is just
	// unknown (plain files on generic sites carry both).
	has := func(c *string) bool { return c == nil || *c != "none" }
	v := Video{Title: "video", Duration: orZero(r.Duration), Subtitles: tracks(r.Subtitles),
		Automatic: tracks(r.AutomaticCaptions)}
	if r.Title != nil {
		v.Title = *r.Title
	}
	if r.Language != nil {
		v.Language = *r.Language
	}
	for _, f := range r.Formats {
		size := orZero(f.FileSize)
		if f.FileSize == nil {
			size = orZero(f.FileSizeApprox)
		}
		v.Formats = append(v.Formats, Format{
			ID: f.FormatID, Ext: f.Ext, Height: int(orZero(f.Height)), FPS: orZero(f.FPS),
			Video: has(f.VCodec), Audio: has(f.ACodec), Size: int64(max(0, size)), TBR: orZero(f.TBR),
		})
	}
	return v
}

// SubChoice is a subtitle language offered in the video window.
type SubChoice struct {
	Code string `json:"code"`
	Name string `json:"name"`
	// Automatic: the site's automatic captions, not the uploader's.
	Automatic bool `json:"automatic"`
}

// SubChoices are the subtitle languages to offer, the uploader's first (all
// of them), then the automatic captions worth a place: those in the
// language spoken (YouTube marks them -orig), and their translations into
// wanted (the interface language) and English. A site's automatic list
// holds a translation into every language it knows: offered whole, the list
// would be useless.
func SubChoices(v Video, wanted string) []SubChoice {
	var out []SubChoice
	for _, t := range v.Subtitles {
		out = append(out, SubChoice{Code: t.Code, Name: t.Name})
	}
	// pt-BR and pt are the same language here.
	base := func(c string) string {
		if i := strings.IndexAny(c, "-_"); i >= 0 {
			c = c[:i]
		}
		return strings.ToLower(c)
	}
	for _, t := range v.Automatic {
		b := base(t.Code)
		worth := strings.HasSuffix(t.Code, "-orig") || v.Language != "" && base(v.Language) == b ||
			b == base(wanted) || b == "en"
		// The uploader's own in that language is the better one.
		if !worth || slices.ContainsFunc(v.Subtitles, func(u Track) bool { return u.Code == t.Code }) {
			continue
		}
		out = append(out, SubChoice{Code: t.Code, Name: t.Name, Automatic: true})
	}
	return out
}

// ListPage is a page that lists videos (a playlist, a channel, an album):
// what one look-up says of it. Its videos are listed afterwards, streamed
// (playlist.go).
type ListPage struct {
	Title string `json:"title"`
	// Total is how many videos, when the site says (a playlist does, a
	// channel doesn't); 0: unknown.
	Total int64 `json:"total"`
}

// Looked is what a page is: one video, or a list of them.
type Looked struct {
	Video *Video
	List  *ListPage
}

// ParseLooked reads yt-dlp -J output: a video, or a list's first page
// (_type: playlist).
func ParseLooked(raw []byte) (Looked, bool) {
	var r rawVideo
	if json.Unmarshal(raw, &r) != nil {
		return Looked{}, false
	}
	if r.Kind == "playlist" {
		title := "playlist"
		if r.Title != nil && strings.TrimSpace(*r.Title) != "" {
			title = strings.TrimSpace(*r.Title)
		}
		return Looked{List: &ListPage{Title: title, Total: int64(orZero(r.PlaylistCount))}}, true
	}
	v := videoOf(r)
	return Looked{Video: &v}, true
}

// LookUpPage looks pageURL up with yt-dlp: one video (--no-playlist: a
// video in a playlist is that video), or, for a page that is a list, only
// what it is and its first entry, flat (never every video of a 6,000-long
// playlist at once).
func LookUpPage(ctx context.Context, exe, pageURL string, browser FromBrowser) (Looked, error) {
	args := []string{"-J", "--no-playlist", "--flat-playlist", "--playlist-items", "1:1", "--no-warnings"}
	theirs, cookies, err := browser.Args(pageURL)
	if err != nil {
		return Looked{}, err
	}
	defer cookies.Remove() // kept until yt-dlp has answered
	out, err := LookUp(ctx, exe, append(args, theirs...), pageURL, 90*time.Second)
	if err != nil {
		return Looked{}, err
	}
	looked, ok := ParseLooked(out.JSON)
	if !ok {
		return Looked{}, &FailedError{Tool: "yt-dlp", Message: "it gave no video information"}
	}
	return looked, nil
}

// Resolve looks pageURL up as one video (a list is an error here).
func Resolve(ctx context.Context, exe, pageURL string, browser FromBrowser) (Video, error) {
	looked, err := LookUpPage(ctx, exe, pageURL, browser)
	if err != nil {
		return Video{}, err
	}
	if looked.List != nil {
		return Video{}, &FailedError{Tool: "yt-dlp", Message: "the address is a list of videos (" + looked.List.Title + ")"}
	}
	return *looked.Video, nil
}

const (
	// AudioOnly begins an audio choice's label (the app translates it).
	AudioOnly = "Audio only"
	// AudioOriginal begins the label of the sound as the site has it, not
	// converted.
	AudioOriginal = "Audio only original"
	// OpusSelector and FlacSelector are the Opus and FLAC choices: the best
	// sound converted by ffmpeg (Opus as is when it already is Opus).
	OpusSelector = "opus:"
	FlacSelector = "flac:"
	// MP3Selector begins an MP3 choice: mp3:<kbps>, the best sound converted
	// at that constant bitrate, or mp3:0 at its best variable one.
	MP3Selector = "mp3:"
)

// MP3Rates are the MP3 bitrates offered, best first.
var MP3Rates = []int{320, 256, 192, 128}

// AudioOut is what an audio choice converts to: the file type and yt-dlp's
// --audio-quality, if any.
type AudioOut struct {
	Format, Quality string
}

// AudioConversion is the conversion an audio choice's selector asks for;
// false for a video or the original sound.
func AudioConversion(selector string) (AudioOut, bool) {
	if q, ok := MP3Quality(selector); ok {
		return AudioOut{"mp3", q}, true
	}
	switch selector {
	case OpusSelector:
		return AudioOut{Format: "opus"}, true
	case FlacSelector:
		return AudioOut{Format: "flac"}, true
	}
	return AudioOut{}, false
}

// MP3Quality is yt-dlp's --audio-quality for an MP3 choice: 0 (the best
// variable bitrate) or a bitrate offered, written exactly (192, not +192 or
// 0192).
func MP3Quality(selector string) (string, bool) {
	kbps, ok := strings.CutPrefix(selector, MP3Selector)
	if !ok {
		return "", false
	}
	if kbps == "0" {
		return "0", true
	}
	for _, k := range MP3Rates {
		if strconv.Itoa(k) == kbps {
			return kbps + "K", true
		}
	}
	return "", false
}

// audioContainer reports containers that only ever hold sound.
func audioContainer(ext string) bool {
	switch strings.ToLower(ext) {
	case "m4a", "mp3", "opus", "aac", "flac", "wav", "weba", "oga", "mka":
		return true
	}
	return false
}

// IsAudio reports a choice that saves only the sound.
func IsAudio(c Choice) bool {
	_, converts := AudioConversion(c.Selector)
	return c.Selector == "ba" || converts || audioContainer(c.Ext)
}

// HoldsSubtitles reports a choice whose file can hold subtitles put into it
// (yt-dlp's --embed-subs).
func HoldsSubtitles(c Choice) bool {
	switch strings.ToLower(c.Ext) {
	case "mp4", "mkv", "mov", "m4v":
		return true
	}
	return false
}

// NeedsMerging reports whether merging (ffmpeg) would bring a better video
// than the ones already carrying their sound. YouTube's only such format is
// 360p, and without a JavaScript runtime yt-dlp often has none: the list
// then holds the sound alone, so the window must say why.
func NeedsMerging(v Video) bool {
	best := func(merging bool) int {
		h := 0
		for _, f := range v.Formats {
			if f.Video && (merging || f.Audio) {
				h = max(h, f.Height)
			}
		}
		return h
	}
	return best(true) > best(false)
}

func round(f float64) string { return strconv.FormatFloat(math.Round(f), 'f', -1, 64) }

// largest is, of formats, the last with the largest size (as Rust's
// max_by_key picks among equals).
func largest(formats []Format, keep func(Format) bool) (Format, bool) {
	var best Format
	found := false
	for _, f := range formats {
		if keep(f) && (!found || f.Size >= best.Size) {
			best, found = f, true
		}
	}
	return best, found
}

// Choices are the choices, best first: each resolution (merged with the
// best audio when ffmpeg is there, else only formats that carry both), then
// files whose resolution isn't known, then audio only.
func Choices(v Video, canMerge bool) []Choice {
	var heights []int
	for _, f := range v.Formats {
		if f.Video && (canMerge || f.Audio) && f.Height > 0 && !slices.Contains(heights, f.Height) {
			heights = append(heights, f.Height)
		}
	}
	sort.Sort(sort.Reverse(sort.IntSlice(heights)))
	bestAudio, hasBestAudio := largest(v.Formats, func(f Format) bool { return f.Audio && !f.Video })
	var out []Choice
	for _, h := range heights {
		// The largest format of that height stands for its size.
		best, _ := largest(v.Formats, func(f Format) bool { return f.Video && f.Height == h && (canMerge || f.Audio) })
		fps := ""
		if best.FPS > 31 {
			fps = " " + round(best.FPS) + "fps"
		}
		c := Choice{}
		if canMerge {
			c.Selector = fmt.Sprintf("bv*[height=%d]+ba/b[height=%d]", h, h)
			c.Ext = "mp4"
			if best.Size > 0 {
				c.Size = best.Size
				if !best.Audio && hasBestAudio {
					c.Size += bestAudio.Size
				}
			}
		} else {
			c.Selector = fmt.Sprintf("b[height=%d]", h)
			c.Ext = best.Ext
			if c.Ext == "" {
				c.Ext = "mp4"
			}
			c.Size = best.Size
		}
		c.Label = fmt.Sprintf("%dp%s %s", h, fps, strings.ToUpper(c.Ext))
		out = append(out, c)
	}
	// Files whose resolution isn't known (generic sites, a stream's variants
	// with a bitrate only): offered by type, and by bitrate when there are
	// several, best first. Audio containers, and streams with no video codec
	// named, aren't videos.
	var plain []Format
	for _, f := range v.Formats {
		if f.Video && f.Audio && f.Height == 0 && !audioContainer(f.Ext) {
			plain = append(plain, f)
		}
	}
	sort.SliceStable(plain, func(i, j int) bool { return plain[i].TBR > plain[j].TBR })
	labels := map[string]bool{}
	for _, f := range plain {
		label := strings.ToUpper(f.Ext)
		if len(plain) > 1 && f.TBR > 0 {
			label += " " + round(f.TBR) + " kbps"
		}
		// Twins (the same type and rate under two IDs) once.
		if labels[label] {
			continue
		}
		labels[label] = true
		out = append(out, Choice{Label: label, Selector: f.ID, Ext: f.Ext, Size: f.Size})
	}
	if hasBestAudio {
		out = append(out, Choice{Label: AudioOriginal + " " + strings.ToUpper(bestAudio.Ext), Selector: "ba",
			Ext: bestAudio.Ext, Size: bestAudio.Size})
	}
	// MP3, converted by ffmpeg from the best sound: its best variable
	// bitrate, then each constant one. Sizes from the duration (a variable
	// one's is about 245 kbit/s).
	hasAudio := slices.ContainsFunc(v.Formats, func(f Format) bool { return f.Audio })
	if canMerge && hasAudio {
		size := func(kbps int) int64 {
			if v.Duration <= 0 {
				return 0
			}
			return int64(v.Duration * float64(kbps) * 125)
		}
		out = append(out, Choice{Label: AudioOnly + " MP3 VBR", Selector: MP3Selector + "0", Ext: "mp3", Size: size(245)})
		for _, kbps := range MP3Rates {
			out = append(out, Choice{Label: fmt.Sprintf("%s MP3 %d kbps", AudioOnly, kbps),
				Selector: MP3Selector + strconv.Itoa(kbps), Ext: "mp3", Size: size(kbps)})
		}
		// Opus: about the size of the best sound when that is Opus already
		// (it is only moved into an .opus file), else of a 160 kbit/s
		// encoding.
		opusSize := size(160)
		if hasBestAudio && bestAudio.Size > 0 && (strings.EqualFold(bestAudio.Ext, "webm") || strings.EqualFold(bestAudio.Ext, "opus")) {
			opusSize = bestAudio.Size
		}
		out = append(out, Choice{Label: AudioOnly + " OPUS", Selector: OpusSelector, Ext: "opus", Size: opusSize})
		// FLAC: lossless from a lossy source, about 900 kbit/s for 48 kHz
		// stereo.
		out = append(out, Choice{Label: AudioOnly + " FLAC", Selector: FlacSelector, Ext: "flac", Size: size(900)})
	}
	return out
}
