// Ported from Mister Download Manager, crates/cli/src/lib.rs (install-*, video-*, playlist-info, gallery-get)
// Copyright (C) 2026 Ahmed Nefzaoui and contributors; Go port (C) 2026 the Yellow Flash contributors.
// Translated from Rust to Go and modified. SPDX-License-Identifier: GPL-3.0-or-later

package main

// The helper programs (the app's, in <data>/tools) and the downloads they
// run: videos with yt-dlp, galleries with gallery-dl.

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/vaisah/internal/infrastructure/filesystem"
	"github.com/vaisah/internal/infrastructure/network"
	"github.com/vaisah/internal/tools"
)

func toolsDir() (string, error) {
	d, err := dataDir()
	if err != nil {
		return "", err
	}
	return tools.Dir(d), nil
}

func cmdInstall(helper string) func(context.Context, []string) error {
	return func(ctx context.Context, args []string) error {
		if len(args) != 0 {
			return usageErr("no arguments")
		}
		dir, err := toolsDir()
		if err != nil {
			return err
		}
		client := network.NewClient(network.Options{})
		last := time.Time{}
		progress := func(got, total int64) {
			if time.Since(last) < 500*time.Millisecond && got != total {
				return
			}
			last = time.Now()
			if jsonOut {
				emit(map[string]any{"event": "progress", "downloaded": got, "total": total})
			} else {
				fmt.Fprintf(stderr, "\r%s: %s / %s   ", helper, humanBytes(got), humanBytes(total))
			}
		}
		var inst tools.Installed
		switch helper {
		case "yt-dlp":
			inst, err = tools.InstallYtDlp(ctx, dir, client, progress)
		case "ffmpeg":
			inst, err = tools.InstallFfmpeg(ctx, dir, client, progress)
		default:
			inst, err = tools.InstallGalleryDl(ctx, dir, client, progress)
		}
		if err != nil {
			return err
		}
		if jsonOut {
			emit(map[string]any{"event": "installed", "helper": helper, "version": inst.Version, "exe": inst.Exe})
		} else {
			fmt.Fprintf(stdout, "\n%s %s: %s\n", helper, inst.Version, inst.Exe)
		}
		return nil
	}
}

// helper is an installed helper, or how to install it.
func helper(h tools.Helper) (tools.Installed, error) {
	dir, err := toolsDir()
	if err != nil {
		return tools.Installed{}, err
	}
	var (
		inst tools.Installed
		ok   bool
	)
	switch h {
	case tools.HelperYtDlp:
		inst, ok = tools.YtDlp(dir)
	case tools.HelperFfmpeg:
		inst, ok = tools.Ffmpeg(dir)
	default:
		inst, ok = tools.GalleryDl(dir)
	}
	if !ok {
		return inst, fmt.Errorf("%s is not installed (%s install-%s)", h, name, strings.ReplaceAll(string(h), "yt-dlp", "ytdlp"))
	}
	return inst, nil
}

func webPage(raw string) (*url.URL, error) {
	u, err := network.ValidateURL(strings.TrimSpace(raw))
	if err != nil {
		return nil, err
	}
	if u.Scheme != "http" && u.Scheme != "https" {
		return nil, errors.New("not a web page's address")
	}
	return u, nil
}

func cmdVideoInfo(ctx context.Context, args []string) error {
	if len(args) != 1 {
		return usageErr("one page address")
	}
	if _, err := webPage(args[0]); err != nil {
		return err
	}
	yt, err := helper(tools.HelperYtDlp)
	if err != nil {
		return err
	}
	looked, err := tools.LookUpPage(ctx, yt.Exe, args[0], tools.FromBrowser{})
	if err != nil {
		return err
	}
	if looked.List != nil {
		if jsonOut {
			emit(map[string]any{"list": looked.List})
		} else {
			fmt.Fprintf(stdout, "%s: a list of videos (%s playlist-info lists them)\n", looked.List.Title, name)
		}
		return nil
	}
	v := *looked.Video
	_, ffErr := helper(tools.HelperFfmpeg)
	choices := tools.Choices(v, ffErr == nil)
	subs := tools.SubChoices(v, "en")
	if jsonOut {
		emit(map[string]any{"title": v.Title, "duration": v.Duration, "choices": choices, "subs": subs,
			"needsFfmpeg": ffErr != nil && tools.NeedsMerging(v)})
		return nil
	}
	fmt.Fprintf(stdout, "%s (%s)\n\nChoices (video-get -f):\n", v.Title, time.Duration(v.Duration*float64(time.Second)).Round(time.Second))
	for _, c := range choices {
		size := ""
		if c.Size > 0 {
			size = " ~" + humanBytes(c.Size)
		}
		fmt.Fprintf(stdout, "  %-30s %s%s\n", c.Selector, c.Label, size)
	}
	if len(subs) > 0 {
		fmt.Fprintln(stdout, "\nSubtitles (video-get --subs):")
		for _, s := range subs {
			fmt.Fprintf(stdout, "  %+v\n", s)
		}
	}
	if ffErr != nil && tools.NeedsMerging(v) {
		fmt.Fprintf(stdout, "\nffmpeg would bring better qualities (%s install-ffmpeg).\n", name)
	}
	return nil
}

func cmdVideoGet(ctx context.Context, args []string) error {
	fs := flag.NewFlagSet("video-get", flag.ContinueOnError)
	format := fs.String("f", "b", `the choice's selector from video-info ("b" the best single file; "ba" the sound alone; "mp3:320", "opus:", "flac:" conversions)`)
	fs.StringVar(format, "format", "b", "the choice's selector")
	out := fs.String("o", ".", "folder to save into")
	fs.StringVar(out, "output", ".", "folder to save into")
	subs := fs.String("subs", "none", `subtitles to save: language codes ("en,fr"), "all" or "none"`)
	autoSubs := fs.Bool("auto-subs", false, "the site's automatic captions instead of the uploader's")
	embedSubs := fs.Bool("embed-subs", false, "put the subtitles into the video file (needs ffmpeg)")
	rest, err := parse(fs, args)
	if err != nil {
		return err
	}
	if len(rest) != 1 {
		return usageErr("one page address")
	}
	page := rest[0]
	if _, err := webPage(page); err != nil {
		return err
	}
	yt, err := helper(tools.HelperYtDlp)
	if err != nil {
		return err
	}
	ff, _ := helper(tools.HelperFfmpeg)
	looked, err := tools.LookUpPage(ctx, yt.Exe, page, tools.FromBrowser{})
	if err != nil {
		return err
	}
	if looked.Video == nil {
		return errors.New("this page lists videos: playlist-info lists them, video-get downloads one of their pages")
	}
	v := *looked.Video
	var expected int64
	for _, c := range tools.Choices(v, ff.Exe != "") {
		if c.Selector == *format {
			expected = c.Size
		}
	}
	dir, err := filepath.Abs(*out)
	if err != nil {
		return err
	}
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return err
	}
	stem, err := filesystem.SanitizeFilename(v.Title)
	if err != nil {
		stem = "video"
	}
	job, err := tools.StartVideo(tools.VideoOptions{Exe: yt.Exe, URL: page, Selector: *format, Dir: dir, Stem: stem,
		Ffmpeg: ff.Exe, Expected: expected, Subs: subsArg(*subs, *autoSubs, *embedSubs),
		Client: network.NewClient(network.Options{})})
	if err != nil {
		return err
	}
	return followMedia(ctx, job.Progress, job.Stop)
}

// subsArg reads --subs, --auto-subs and --embed-subs.
func subsArg(langs string, automatic, embed bool) tools.Subs {
	langs = strings.TrimSpace(langs)
	if langs == "" || strings.EqualFold(langs, "none") {
		return tools.Subs{}
	}
	var s tools.Subs
	for _, l := range strings.Split(langs, ",") {
		if l = strings.TrimSpace(l); l != "" {
			s.Langs = append(s.Langs, l)
		}
	}
	if len(s.Langs) == 1 && strings.EqualFold(s.Langs[0], tools.AllUploaders) {
		s.Langs = []string{tools.AllUploaders}
	}
	s.Automatic, s.Embed = automatic, embed
	return s
}

// followMedia shows a helper's download until it ends; Ctrl+C stops it.
func followMedia(ctx context.Context, progress func() tools.VideoProgress, stop func()) error {
	tick := time.NewTicker(500 * time.Millisecond)
	defer tick.Stop()
	stopped := false
	for {
		select {
		case <-ctx.Done():
			if !stopped {
				stop()
				stopped = true
			}
		case <-tick.C:
		}
		p := progress()
		if p.State == tools.VideoRunning {
			if jsonOut {
				emit(map[string]any{"event": "progress", "downloaded": p.Downloaded, "total": p.Total, "speed": int64(p.Speed)})
			} else {
				fmt.Fprintf(stderr, "\r%s / %s  %s/s   ", humanBytes(p.Downloaded), humanBytes(p.Total), humanBytes(int64(p.Speed)))
			}
			continue
		}
		if jsonOut {
			emit(map[string]any{"event": string(p.State), "file": p.File, "error": p.Error, "sideFiles": p.SideFiles,
				"subProblems": p.SubProblems})
		} else {
			fmt.Fprintln(stderr)
			for _, sp := range p.SubProblems {
				fmt.Fprintf(stderr, "subtitles: %+v\n", sp)
			}
		}
		switch p.State {
		case tools.VideoDone:
			if !jsonOut {
				fmt.Fprintln(stdout, "saved "+p.File)
				for _, f := range p.SideFiles {
					fmt.Fprintln(stdout, "saved "+f)
				}
			}
			return nil
		case tools.VideoStopped:
			if !jsonOut {
				fmt.Fprintln(stdout, "stopped")
			}
			return errReported
		}
		if jsonOut {
			return errReported
		}
		return errors.New(p.Error)
	}
}

func cmdPlaylistInfo(ctx context.Context, args []string) error {
	fs := flag.NewFlagSet("playlist-info", flag.ContinueOnError)
	from := fs.Int64("from", 1, "the first entry (from 1)")
	count := fs.Int64("count", 0, "how many entries at most (0: to the end of the list)")
	rest, err := parse(fs, args)
	if err != nil {
		return err
	}
	if len(rest) != 1 || *from < 1 || *count < 0 {
		return usageErr("one page address; --from 1 or more")
	}
	if _, err := webPage(rest[0]); err != nil {
		return err
	}
	yt, err := helper(tools.HelperYtDlp)
	if err != nil {
		return err
	}
	var to int64
	if *count > 0 {
		to = *from + *count - 1
	}
	ls, err := tools.StartListing(yt.Exe, rest[0], tools.FromBrowser{}, *from, to)
	if err != nil {
		return err
	}
	defer ls.Cancel()
	seen := 0
	for {
		select {
		case <-ctx.Done():
			ls.Cancel()
		case <-time.After(300 * time.Millisecond):
		}
		for _, e := range ls.EntriesFrom(seen) {
			seen++
			if jsonOut {
				emit(e)
			} else {
				fmt.Fprintf(stdout, "%5d  %s\n       %s\n", e.Index, e.Title, e.URL)
			}
		}
		p := ls.Progress()
		if p.Done {
			if jsonOut {
				emit(map[string]any{"event": "done", "title": p.Title, "listed": p.Listed, "total": p.Total})
			} else {
				fmt.Fprintf(stdout, "%s: %d listed\n", p.Title, p.Listed)
			}
			return nil
		}
	}
}

func cmdGalleryGet(ctx context.Context, args []string) error {
	fs := flag.NewFlagSet("gallery-get", flag.ContinueOnError)
	out := fs.String("o", ".", "folder to save into: a folder named after the gallery is made there")
	fs.StringVar(out, "output", ".", "folder to save into")
	limit := fs.Int64("limit", 0, "at most this many files (0: all)")
	rest, err := parse(fs, args)
	if err != nil {
		return err
	}
	if len(rest) != 1 || *limit < 0 {
		return usageErr("one gallery address")
	}
	u, err := webPage(rest[0])
	if err != nil {
		return err
	}
	g, err := helper(tools.HelperGalleryDl)
	if err != nil {
		return err
	}
	dir, err := filepath.Abs(*out)
	if err != nil {
		return err
	}
	job, err := tools.StartGallery(g, rest[0], filepath.Join(dir, tools.GalleryName(u)), tools.GalleryAsk{Limit: *limit}, tools.FromBrowser{})
	if err != nil {
		return err
	}
	return followMedia(ctx, job.Progress, job.Stop)
}
