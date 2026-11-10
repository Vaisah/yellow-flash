package main

import (
	"strings"
	"testing"
	"time"

	"github.com/vaisah/internal/tools"
)

func TestHelpersStartAbsentAndAddressesAreChecked(t *testing.T) {
	a := &App{dataDir: t.TempDir()}
	for _, h := range a.GetHelpers() {
		if h.Name == "ffmpeg" {
			continue // Linux and macOS may use the system's
		}
		if h.Exe != "" || h.Chosen {
			t.Errorf("%s present: %+v", h.Name, h)
		}
	}
	if _, err := a.ChooseHelper("aria2c", "x"); err == nil {
		t.Error("an unknown helper")
	}
	if _, err := a.LookUpVideo("ftp://x.test/v", "en"); err == nil {
		t.Error("not a web page")
	}
	if _, err := a.LookUpVideo("https://www.youtube.com/watch?v=x", "en"); err == nil || !strings.Contains(err.Error(), "yt-dlp is not installed") {
		t.Errorf("no yt-dlp: %v", err)
	}
	if _, err := a.StartGalleryDownload(GalleryRequest{URL: "https://imgur.com/a/x"}); err == nil || !strings.Contains(err.Error(), "gallery-dl") {
		t.Errorf("no gallery-dl: %v", err)
	}
}

type fakeMedia struct {
	state   tools.VideoState
	stopped bool
}

func (f *fakeMedia) Progress() tools.VideoProgress { return tools.VideoProgress{State: f.state} }
func (f *fakeMedia) Stop()                         { f.stopped, f.state = true, tools.VideoStopped }

func TestMediaDownloadsAreListedInOrderAndStopped(t *testing.T) {
	a := &App{}
	first, second := &fakeMedia{state: tools.VideoDone}, &fakeMedia{state: tools.VideoRunning}
	a.track(&mediaEntry{row: MediaRow{ID: "1", Kind: "video"}, job: first})
	time.Sleep(time.Millisecond)
	a.track(&mediaEntry{row: MediaRow{ID: "2", Kind: "gallery"}, job: second})
	rows := a.ListMediaDownloads()
	if len(rows) != 2 || rows[0].ID != "1" || rows[1].Progress.State != tools.VideoRunning {
		t.Fatalf("rows %+v", rows)
	}
	if err := a.StopMediaDownload("2"); err != nil || !second.stopped {
		t.Errorf("stop: %v", err)
	}
	if err := a.StopMediaDownload("nope"); err == nil {
		t.Error("an unknown download")
	}
}
