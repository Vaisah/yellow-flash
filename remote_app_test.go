package main

import (
	"context"
	"crypto/tls"
	"encoding/json"
	"io"
	"net/http"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/vaisah/internal/application/download"
	"github.com/vaisah/internal/application/queues"
	domain "github.com/vaisah/internal/domain/download"
	"github.com/vaisah/internal/domain/queue"
	"github.com/vaisah/internal/engine"
	"github.com/vaisah/internal/infrastructure/persistence/jsonstore"
	"github.com/vaisah/internal/ipc"
	"github.com/vaisah/internal/remote"
)

// remoteApp is an app with its engine and queues, its downloads going to a
// temporary folder, commands handled as the running app does.
func remoteApp(t *testing.T) (*App, context.Context) {
	t.Helper()
	dir := t.TempDir()
	store, err := jsonstore.Open(filepath.Join(dir, "downloads.json"))
	if err != nil {
		t.Fatal(err)
	}
	a := &App{store: store, dataDir: dir, commands: make(chan ipc.Incoming, commandBacklog), stop: make(chan struct{})}
	mgr, err := engine.New(store, engine.Config{AllowPrivateNetworks: true})
	if err != nil {
		t.Fatal(err)
	}
	a.manager, a.service = mgr, download.New(mgr)
	ctx, cancel := context.WithCancel(context.Background())
	r, err := queues.New(ctx, mgr, store.Queues(), nil)
	if err != nil {
		t.Fatal(err)
	}
	a.queues.Store(r)
	s := a.GetSettings()
	s.DownloadDirectory = filepath.Join(dir, "files")
	if _, err := a.SaveSettings(s); err != nil {
		t.Fatal(err)
	}
	// The command loop, as domReady starts it (no window, so dispatch's
	// own path for the rest is left out).
	go func() {
		for {
			select {
			case <-a.stop:
				return
			case in := <-a.commands:
				if in.Reply != nil {
					a.answer(ctx, in)
				}
			}
		}
	}()
	t.Cleanup(func() {
		if a.remote != nil {
			a.remote.Stop()
		}
		a.stopCommands()
		cancel()
		_ = mgr.Shutdown(context.Background())
		_ = store.Close()
	})
	return a, ctx
}

func create(url string) domain.CreateRequest { return domain.CreateRequest{URL: url} }

func ask(t *testing.T, a *App, cmd ipc.Command) string {
	t.Helper()
	cmd.Remote = true
	line, ok := ipc.NewSink(a.handleCommand).Ask(cmd, 5*time.Second)
	if !ok {
		t.Fatal("no answer")
	}
	return line
}

func TestRemoteSettingsAreChecked(t *testing.T) {
	a, _ := remoteApp(t)
	if c := a.GetRemoteSettings(); c.On || c.Port != remote.Port {
		t.Errorf("%+v", c)
	}
	for _, bad := range []remote.Config{{Port: 0}, {Port: 70000}, {Port: 1, Hosts: []string{"a b"}}, {Port: 1, Hosts: []string{"evil.example/x"}}} {
		if _, err := a.SaveRemoteSettings(bad); err == nil {
			t.Errorf("%+v saved", bad)
		}
	}
	c, err := a.SaveRemoteSettings(remote.Config{Port: 9000, Hosts: []string{" NAS.Example. ", "nas.example", ""}})
	if err != nil || !slices.Equal(c.Hosts, []string{"nas.example"}) || a.GetRemoteSettings().Port != 9000 {
		t.Error(c, err)
	}
}

// A paired device's commands: the list has no paths, actions say what was
// done and skipped, and a path on this computer is never taken.
func TestTheAppAnswersAPairedDevice(t *testing.T) {
	a, ctx := remoteApp(t)
	// The remote control off: a device's command is refused.
	if _, e, _ := ipc.ParseAnswer[ipc.ListAnswer](ask(t, a, ipc.Command{Query: "list"})); e == nil || e.Code != "remote-off" {
		t.Error(e)
	}
	a.startRemote()
	a.remote.Apply(remote.Config{On: true, Port: 0, Listen: remote.ThisComputer})
	d, err := a.service.Create(ctx, create("https://example.com/files/a.zip"))
	if err != nil {
		t.Fatal(err)
	}

	list, e, err := ipc.ParseAnswer[ipc.ListAnswer](ask(t, a, ipc.Command{Query: "list"}))
	if err != nil || e != nil {
		t.Fatal(err, e)
	}
	if len(list.Downloads) != 1 || list.Downloads[0].ID != d.ID || list.Downloads[0].Path != "" || list.Downloads[0].File != "a.zip" {
		t.Errorf("%+v", list.Downloads)
	}
	if len(list.Queues) < 1 || list.Queues[0].Name == "" || list.Free == nil {
		t.Errorf("%+v %v", list.Queues, list.Free)
	}

	act, _, _ := ipc.ParseAnswer[ipc.ActAnswer](ask(t, a, ipc.Command{Action: "pause", IDs: []string{d.ID, "nope", "torrent:" + strings.Repeat("ab", 20)}}))
	if len(act.Done) != 0 || len(act.Skipped) != 3 || act.Skipped[0].Why != "not running" || act.Skipped[1].Why != "no such download" || act.Skipped[2].Why != "no such download" {
		t.Errorf("%+v", act)
	}

	// Added to a queue, not started.
	add, e, _ := ipc.ParseAnswer[ipc.AddAnswer](ask(t, a, ipc.Command{URL: "https://example.com/files/b.zip", Later: true, Silent: true}))
	if e != nil || add.ID == "" {
		t.Fatal(e, add)
	}
	if !slices.ContainsFunc(a.queues.Load().Queues(), func(q queue.Queue) bool { return q.ID == queue.MainID && slices.Contains(q.Items, add.ID) }) {
		t.Error("not in the main queue")
	}
	// Not a web address: refused, and a .torrent path on this computer is
	// never read.
	for _, bad := range []string{`C:\secret\x.torrent`, "/etc/passwd.torrent", "file:///etc/hosts"} {
		if _, e, _ := ipc.ParseAnswer[ipc.AddAnswer](ask(t, a, ipc.Command{URL: bad, Silent: true})); e == nil || e.Code != "unsupported" {
			t.Errorf("%s: %+v", bad, e)
		}
	}

	if act, _, _ := ipc.ParseAnswer[ipc.ActAnswer](ask(t, a, ipc.Command{Action: "remove", IDs: []string{d.ID}})); !slices.Equal(act.Done, []string{d.ID}) {
		t.Errorf("%+v", act)
	}
	if _, err := a.service.Get(ctx, d.ID); err == nil {
		t.Error("still in the list")
	}

	q := a.queues.Load().Queues()[0]
	if act, e, _ := ipc.ParseAnswer[ipc.ActAnswer](ask(t, a, ipc.Command{Action: "start_queue", Queue: q.Name})); e != nil || !slices.Equal(act.Done, []string{q.Name}) {
		t.Error(act, e)
	}
	if _, e, _ := ipc.ParseAnswer[ipc.ActAnswer](ask(t, a, ipc.Command{Action: "stop_queue", Queue: "no such queue"})); e == nil {
		t.Error("an unknown queue")
	}
	if _, e, _ := ipc.ParseAnswer[ipc.ListAnswer](ask(t, a, ipc.Command{Query: "settings"})); e == nil {
		t.Error("only the list is asked")
	}

	// The same question from another program: the automation API is off.
	line, _ := ipc.NewSink(a.handleCommand).Ask(ipc.Command{Query: "list"}, 5*time.Second)
	if _, e, _ := ipc.ParseAnswer[ipc.ListAnswer](line); e == nil || e.Code != ipc.APIOff {
		t.Error(line)
	}
}

// The automation API: off until turned on; programs on this computer see
// paths, paired devices don't, nor passwords; pair gives a code and a QR
// code while the remote control listens.
func TestTheAutomationAPI(t *testing.T) {
	a, ctx := remoteApp(t)
	local := func(cmd ipc.Command) string {
		line, ok := ipc.NewSink(a.handleCommand).Ask(cmd, 5*time.Second)
		if !ok {
			t.Fatal("no answer")
		}
		return line
	}
	if _, e, _ := ipc.ParseAnswer[ipc.ListAnswer](local(ipc.Command{Query: "list"})); e == nil || e.Code != ipc.APIOff {
		t.Error("on by default", e)
	}
	on := true
	if ans, _, _ := ipc.ParseAnswer[ipc.APIAnswer](local(ipc.Command{API: &on})); !ans.API || !a.apiOn() {
		t.Fatal("not turned on")
	}
	d, err := a.service.Create(ctx, create("https://example.com/files/p.zip"))
	if err != nil {
		t.Fatal(err)
	}
	list, e, _ := ipc.ParseAnswer[ipc.ListAnswer](local(ipc.Command{Query: "list"}))
	if e != nil || len(list.Downloads) != 1 || list.Downloads[0].Path != filepath.Join(d.Destination, d.Filename) {
		t.Errorf("%+v %v", list.Downloads, e)
	}
	for _, bad := range []ipc.Command{{Query: "settings"}, {Action: "exit", IDs: []string{d.ID}}} {
		if _, e, _ := ipc.ParseAnswer[ipc.ListAnswer](local(bad)); e == nil {
			t.Errorf("%+v answered", bad)
		}
	}
	if _, e, _ := ipc.ParseAnswer[ipc.PairAnswer](local(ipc.Command{Pair: true})); e == nil || e.Code != "remote-off" {
		t.Error("paired while off", e)
	}

	a.startRemote()
	a.remote.Apply(remote.Config{On: true, Port: 0, Listen: remote.ThisComputer})
	remoteList, _, _ := ipc.ParseAnswer[ipc.ListAnswer](ask(t, a, ipc.Command{Query: "list"}))
	if got := remoteList.Downloads[0]; got.Path != "" || got.URL != "https://example.com/files/p.zip" {
		t.Errorf("%+v", got)
	}
	p, e, _ := ipc.ParseAnswer[ipc.PairAnswer](local(ipc.Command{Pair: true}))
	if e != nil || len(p.Code) != 8 || p.Minutes != 10 || len(p.URLs) != 1 || !strings.Contains(p.QR, "█") {
		t.Fatalf("%+v %v", p, e)
	}
	if code, _, _ := a.remote.PairingState(); code != p.Code {
		t.Error("not the code shown")
	}

	off := false
	local(ipc.Command{API: &off})
	if _, e, _ := ipc.ParseAnswer[ipc.ListAnswer](local(ipc.Command{Query: "list"})); e == nil || e.Code != ipc.APIOff {
		t.Error("still on", e)
	}
}

func TestPasswordsAreTakenOut(t *testing.T) {
	for in, want := range map[string]string{
		"https://u:p@h/x": "https://u@h/x", "ftp://u@h/x": "ftp://u@h/x", "https://h/x?a=b": "https://h/x?a=b",
	} {
		if got := withoutPassword(in); got != want {
			t.Errorf("%s: %s", in, got)
		}
	}
}

// End to end: a phone pairs with the remote control and sees the app's
// list.
func TestAPhoneSeesTheListThroughTheRemoteControl(t *testing.T) {
	a, ctx := remoteApp(t)
	if _, err := a.service.Create(ctx, create("https://example.com/files/c.iso")); err != nil {
		t.Fatal(err)
	}
	a.startRemote()
	if _, err := a.StartRemotePairing(); err == nil {
		t.Error("pairing while off")
	}
	// Any free port, on this computer only.
	a.remote.Apply(remote.Config{On: true, Port: 0, Listen: remote.ThisComputer})
	info := a.GetRemoteInfo()
	if info.Status.State != remote.Listening || len(info.URLs) != 1 || info.Fingerprint == "" {
		t.Fatalf("%+v", info)
	}
	p, err := a.StartRemotePairing()
	if err != nil || len(p.Code) != 8 || p.SecondsLeft < 590 {
		t.Fatal(p, err)
	}
	c := &http.Client{Timeout: 10 * time.Second, Transport: &http.Transport{TLSClientConfig: &tls.Config{InsecureSkipVerify: true}}}
	res, err := c.Post(info.URLs[0]+"api/pair", "application/json", strings.NewReader(`{"code":"`+p.Code+`","name":"Phone"}`))
	if err != nil {
		t.Fatal(err)
	}
	var paired map[string]any
	_ = json.NewDecoder(res.Body).Decode(&paired)
	res.Body.Close()
	token, _ := paired["token"].(string)
	if res.StatusCode != 200 || token == "" {
		t.Fatal(res.StatusCode, paired)
	}
	if got := a.GetRemotePairing(); got.Paired != "Phone" || len(a.GetRemoteInfo().Devices) != 1 {
		t.Errorf("%+v", got)
	}
	req, _ := http.NewRequest("GET", info.URLs[0]+"api/list", nil)
	req.Header.Set("Authorization", "Bearer "+token)
	res, err = c.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	b, _ := io.ReadAll(res.Body)
	res.Body.Close()
	list, e, err := ipc.ParseAnswer[ipc.ListAnswer](string(b))
	if err != nil || e != nil || len(list.Downloads) != 1 || list.Downloads[0].File != "c.iso" {
		t.Fatalf("%s %v %v", b, e, err)
	}
	// No folder of this computer in what the phone gets.
	escaped, _ := json.Marshal(a.dataDir)
	if strings.Contains(string(b), strings.Trim(string(escaped), `"`)) || list.Downloads[0].Path != "" {
		t.Errorf("a folder in the list: %s", b)
	}

	id := a.GetRemoteInfo().Devices[0].ID
	if err := a.RemoveRemoteDevice(id); err != nil || a.RemoveRemoteDevice(id) == nil {
		t.Error("RemoveRemoteDevice", err)
	}
	a.StopRemotePairing()
	if a.GetRemotePairing().Code != "" {
		t.Error("still shown")
	}
}
