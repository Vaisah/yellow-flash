// Ported from Mister Download Manager, crates/tools/src/pages.rs
// Copyright (C) 2026 Ahmed Nefzaoui and contributors; Go port (C) 2026 the Yellow Flash contributors.
// Translated from Rust to Go and modified. SPDX-License-Identifier: GPL-3.0-or-later

package tools

// Addresses of pages that hold a video or a gallery, not of files: Add URL
// hands these to the video window (yt-dlp) or the gallery window
// (gallery-dl) instead of downloading the page itself. Only addresses whose
// shape says "one video" are listed — a channel, a search or a home page is
// left alone.

import (
	"net/url"
	"strings"
)

func segments(p string) []string {
	var out []string
	for _, s := range strings.Split(p, "/") {
		if s != "" {
			out = append(out, s)
		}
	}
	return out
}

func hasQuery(u *url.URL, k string) bool {
	for _, v := range u.Query()[k] {
		if v != "" {
			return true
		}
	}
	return false
}

func webHost(u *url.URL) (string, bool) {
	if u.Scheme != "http" && u.Scheme != "https" || u.Hostname() == "" {
		return "", false
	}
	return strings.ToLower(u.Hostname()), true
}

// first reports segs[0] is one of names.
func first(segs []string, names ...string) bool {
	if len(segs) == 0 {
		return false
	}
	for _, n := range names {
		if segs[0] == n {
			return true
		}
	}
	return false
}

func isDigits(s string) bool {
	if s == "" {
		return false
	}
	for _, c := range s {
		if c < '0' || c > '9' {
			return false
		}
	}
	return true
}

// IsVideoPage reports a video's page on a site yt-dlp reads (YouTube, Vimeo,
// Dailymotion, Twitch, TikTok, X, Instagram, Facebook, Reddit, SoundCloud,
// Bilibili, Rumble).
func IsVideoPage(u *url.URL) bool {
	host, ok := webHost(u)
	if !ok {
		return false
	}
	if h, cut := strings.CutPrefix(host, "www."); cut {
		host = h
	} else if h, cut := strings.CutPrefix(host, "m."); cut {
		host = h
	}
	path := u.EscapedPath()
	segs := segments(u.Path)
	n := len(segs)
	switch host {
	case "youtube.com", "music.youtube.com", "youtube-nocookie.com":
		return path == "/watch" && hasQuery(u, "v") || n >= 2 && first(segs, "shorts", "live", "embed", "v")
	case "youtu.be", "dai.ly", "clips.twitch.tv", "fb.watch", "v.redd.it":
		return n == 1
	case "vimeo.com":
		for _, s := range segs {
			if isDigits(s) {
				return true
			}
		}
		return false
	case "player.vimeo.com", "dailymotion.com", "bilibili.com":
		return n >= 2 && first(segs, "video")
	case "twitch.tv":
		return n == 2 && segs[0] == "videos" || n == 3 && segs[1] == "clip"
	case "tiktok.com":
		return n == 3 && segs[1] == "video"
	case "x.com", "twitter.com":
		return n >= 3 && segs[1] == "status"
	case "instagram.com":
		return n >= 2 && first(segs, "reel", "reels", "p", "tv")
	case "facebook.com":
		return (path == "/watch" || path == "/watch/") && hasQuery(u, "v") ||
			n >= 2 && segs[1] == "videos" || n >= 2 && segs[0] == "reel"
	case "reddit.com", "old.reddit.com":
		return n >= 4 && segs[0] == "r" && segs[2] == "comments"
	case "soundcloud.com":
		return n == 2
	case "rumble.com":
		return n >= 1 && strings.HasPrefix(segs[0], "v") && strings.Contains(segs[0], "-")
	}
	return false
}

// IsGalleryPage reports an image gallery's page on a site gallery-dl reads:
// a Wikimedia Commons category, a Flickr album or photographer, a
// DeviantArt or ArtStation gallery, an Imgur album, a Pixiv artist or work,
// a Pinterest board, a Danbooru or Gelbooru search, a Tumblr blog, a
// Bluesky profile. A picture's own address (a file) is not one.
func IsGalleryPage(u *url.URL) bool {
	host, ok := webHost(u)
	if !ok {
		return false
	}
	host = strings.TrimPrefix(host, "www.")
	segs := segments(u.Path)
	n := len(segs)
	// A file's own address is downloaded as a file.
	if n > 0 {
		last := strings.ToLower(segs[n-1])
		for _, e := range []string{".jpg", ".jpeg", ".png", ".gif", ".webp", ".avif", ".bmp", ".tif", ".tiff", ".svg"} {
			if strings.HasSuffix(last, e) {
				return false
			}
		}
	}
	switch {
	case host == "commons.wikimedia.org":
		return n == 2 && segs[0] == "wiki" && strings.HasPrefix(segs[1], "Category:")
	case host == "flickr.com":
		return n >= 2 && segs[0] == "photos"
	case host == "deviantart.com":
		return n >= 2 && segs[1] == "gallery"
	case strings.HasSuffix(host, ".deviantart.com"), strings.HasSuffix(host, ".tumblr.com"):
		return true
	case host == "artstation.com":
		return n == 1 || n == 2 && segs[0] == "artwork"
	case host == "imgur.com":
		return n == 2 && first(segs, "a", "gallery")
	case host == "pixiv.net":
		return n >= 2 && segs[0] == "users" || n == 2 && segs[0] == "artworks" ||
			n >= 3 && (segs[1] == "users" || segs[1] == "artworks")
	case host == "pinterest.com":
		// A board (/user/board/) or a pin (/pin/id/).
		return n == 2
	case host == "danbooru.donmai.us", host == "safebooru.donmai.us":
		return n == 1 && segs[0] == "posts" && hasQuery(u, "tags")
	case host == "gelbooru.com":
		return hasQuery(u, "tags")
	case host == "bsky.app":
		return n >= 2 && segs[0] == "profile"
	}
	return false
}
