package main

import (
	"context"
	"errors"
	"net"
	"net/netip"
	"strings"
	"sync"
	"time"

	"github.com/vaisah/internal/infrastructure/network"
	"github.com/vaisah/internal/ipc"
	"github.com/vaisah/internal/tools"
	"github.com/wailsapp/wails/v2/pkg/runtime"
)

// The browser's video button opens the Download video window for the page
// (event "app:video"). What the browser's request for the page carried
// (cookies, referer, User-Agent) stays here, not in the UI, and goes to
// yt-dlp when the window looks that page up or downloads it.

// VideoPrefill opens the Download video window on a page.
type VideoPrefill struct {
	URL string `json:"url"`
}

// browserVideoKeep is how long a page's browser context is kept for the
// window to use; browserVideoMax how many pages are kept at once.
const (
	browserVideoKeep = time.Hour
	browserVideoMax  = 32
)

type browserVideo struct {
	from tools.FromBrowser
	at   time.Time
}

// browserVideos are the pages the browser sent, by address.
type browserVideos struct {
	mu    sync.Mutex
	pages map[string]browserVideo
}

func (b *browserVideos) put(page string, from tools.FromBrowser) {
	b.mu.Lock()
	defer b.mu.Unlock()
	if b.pages == nil {
		b.pages = map[string]browserVideo{}
	}
	now := time.Now()
	oldest := ""
	for k, v := range b.pages {
		if now.Sub(v.at) > browserVideoKeep {
			delete(b.pages, k)
		} else if oldest == "" || v.at.Before(b.pages[oldest].at) {
			oldest = k
		}
	}
	if _, there := b.pages[page]; !there && len(b.pages) >= browserVideoMax && oldest != "" {
		delete(b.pages, oldest)
	}
	b.pages[page] = browserVideo{from: from, at: now}
}

// get is what the browser sent with page, if it sent it lately; a page
// typed into the window has none.
func (b *browserVideos) get(page string) tools.FromBrowser {
	b.mu.Lock()
	defer b.mu.Unlock()
	v, ok := b.pages[page]
	if !ok || time.Since(v.at) > browserVideoKeep {
		return tools.FromBrowser{}
	}
	return v.from
}

// publicPage refuses a page from the browser on this computer or its
// network: a web page could otherwise have yt-dlp, which has no such guard,
// fetch from the intranet. Every address the host has must be public.
func publicPage(ctx context.Context, raw string) error {
	u, err := webPageURL(raw)
	if err != nil {
		return err
	}
	host := u.Hostname()
	if ip, err := netip.ParseAddr(host); err == nil {
		if !network.IsPublicAddress(ip) {
			return errors.New("the page is on this computer or its network")
		}
		return nil
	}
	if h := strings.ToLower(strings.TrimSuffix(host, ".")); h == "localhost" || strings.HasSuffix(h, ".localhost") {
		return errors.New("the page is on this computer")
	}
	ctx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	ips, err := net.DefaultResolver.LookupNetIP(ctx, "ip", host)
	if err != nil {
		return err
	}
	for _, ip := range ips {
		if !network.IsPublicAddress(ip) {
			return errors.New("the page is on this computer or its network")
		}
	}
	return nil
}

// videoFromBrowser opens the Download video window on the page the
// browser's video button sent.
func (a *App) videoFromBrowser(ctx context.Context, cmd ipc.Command) {
	page := strings.TrimSpace(cmd.VideoPage)
	if err := publicPage(ctx, page); err != nil {
		a.focusWindow(ctx)
		a.notice(ctx, "error", "Cannot download the video at "+page+": "+err.Error())
		return
	}
	a.browserVideos.put(page, tools.FromBrowser{Referer: cmd.Referer, UserAgent: cmd.UserAgent,
		Cookies: cmd.Cookies, Jar: cmd.CookieJar})
	a.focusWindow(ctx)
	runtime.EventsEmit(ctx, "app:video", VideoPrefill{URL: page})
}
