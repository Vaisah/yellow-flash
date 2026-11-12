package main

import (
	"context"
	"fmt"
	"testing"
	"time"

	"github.com/vaisah/internal/tools"
)

// A page from the browser on this computer or its network never reaches
// yt-dlp.
func TestPublicPageRefusesLocalAddresses(t *testing.T) {
	for _, u := range []string{
		"http://127.0.0.1/watch", "http://[::1]/watch", "http://192.168.1.10/v",
		"http://10.0.0.1/v", "http://localhost:8080/v", "http://a.localhost/v",
		"http://169.254.169.254/latest", "file:///etc/passwd", "not a url",
	} {
		if err := publicPage(context.Background(), u); err == nil {
			t.Errorf("%s: taken", u)
		}
	}
	if err := publicPage(context.Background(), "https://93.184.215.14/watch"); err != nil {
		t.Errorf("a public address: %v", err)
	}
}

// What the browser sent goes with its page only, and not forever.
func TestBrowserVideosKeepsEachPagesContext(t *testing.T) {
	var b browserVideos
	from := tools.FromBrowser{Referer: "https://tube.test/", Cookies: "a=1", UserAgent: "UA"}
	b.put("https://tube.test/watch?v=1", from)
	if got := b.get("https://tube.test/watch?v=1"); got.Cookies != "a=1" || got.Referer != from.Referer {
		t.Errorf("got %+v", got)
	}
	if got := b.get("https://tube.test/watch?v=2"); got.Cookies != "" {
		t.Errorf("another page got %+v", got)
	}
	b.mu.Lock()
	b.pages["https://tube.test/watch?v=1"] = browserVideo{from: from, at: time.Now().Add(-2 * browserVideoKeep)}
	b.mu.Unlock()
	if got := b.get("https://tube.test/watch?v=1"); got.Cookies != "" {
		t.Errorf("an old page got %+v", got)
	}
	for i := range browserVideoMax + 5 {
		b.put(fmt.Sprintf("https://tube.test/watch?v=%d", i), from)
	}
	if n := len(b.pages); n > browserVideoMax {
		t.Errorf("%d pages kept", n)
	}
}

type fakeMediaJob struct {
	p       tools.VideoProgress
	stopped bool
}

func (f *fakeMediaJob) Progress() tools.VideoProgress { return f.p }
func (f *fakeMediaJob) Stop()                         { f.stopped = true }

// A video in the list opens its saved file once done, shows its folder
// before, and leaves the list (stopped, its file kept) when removed.
func TestMediaRowsOpenAndRemove(t *testing.T) {
	a := &App{}
	job := &fakeMediaJob{p: tools.VideoProgress{State: tools.VideoRunning}}
	a.media.add(&mediaEntry{row: MediaRow{ID: "v1", Kind: "video", Dir: "/videos"}, job: job})
	if path, done, err := a.media.target("v1"); err != nil || done || path != "/videos" {
		t.Errorf("running: %q %v %v", path, done, err)
	}
	if err := a.OpenMediaFile("v1"); err == nil {
		t.Error("a running video opened")
	}
	job.p = tools.VideoProgress{State: tools.VideoDone, File: "/videos/a.mp4"}
	if path, done, err := a.media.target("v1"); err != nil || !done || path != "/videos/a.mp4" {
		t.Errorf("done: %q %v %v", path, done, err)
	}
	if err := a.RemoveMediaDownload("v1"); err != nil || !job.stopped {
		t.Errorf("remove: %v, stopped %v", err, job.stopped)
	}
	if len(a.ListMediaDownloads()) != 0 {
		t.Error("still listed")
	}
	if err := a.RemoveMediaDownload("v1"); err == nil {
		t.Error("removed twice")
	}
}
