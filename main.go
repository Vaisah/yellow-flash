package main

import (
	"embed"
	"errors"
	"io"
	"log"
	"os"
	"path/filepath"
	"time"

	"github.com/vaisah/internal/crash"
	"github.com/vaisah/internal/ipc"
	"github.com/vaisah/internal/platform"
	"github.com/wailsapp/wails/v2"
	"github.com/wailsapp/wails/v2/pkg/options"
	"github.com/wailsapp/wails/v2/pkg/options/assetserver"
	"github.com/wailsapp/wails/v2/pkg/options/linux"
	"github.com/wailsapp/wails/v2/pkg/options/windows"
)

//go:embed all:frontend/dist
var assets embed.FS

// handOverWait is how long a second launch keeps trying to reach an
// instance that holds the lock but is not listening yet (or is quitting).
const handOverWait = 10 * time.Second

func main() {
	// IDM-style command line: /d URL [/p dir] [/f name] [/a] [/n] [/s] ...
	cmd, hasCmd := ipc.ParseArgs(os.Args[1:])
	absolutePaths(&cmd)
	dataDir, err := ipc.DataDir()
	if err != nil {
		log.Fatal(err)
	}

	// One instance per data folder: two would corrupt each other's
	// downloads (each one's Recover pauses the other's). A running instance
	// gets this launch's command; a plain second launch brings its window
	// to the front.
	lock, ok := becomeInstance(dataDir, cmd, hasCmd)
	if !ok {
		return
	}
	defer lock.Close()

	// The last run's crash, if it crashed, becomes a report; this run's
	// would be written for the next start (internal/crash). After the lock:
	// a second launch must not take the first's.
	lastCrash, err := crash.Collect(dataDir, crash.ThisSystem(AppName, AppVersion))
	if err != nil {
		log.Printf("crash report: %v", err)
	}
	if _, err := crash.Install(dataDir); err != nil {
		log.Printf("crash reports unavailable: %v", err)
	}

	app, err := NewApp(dataDir)
	if err != nil {
		log.Fatal(err)
	}
	app.lastCrash = lastCrash
	if hasCmd {
		app.handleCommand(ipc.Incoming{Cmd: cmd})
	}
	// Without the channel the app still works; it just cannot receive
	// commands from a second launch.
	if srv, err := ipc.Serve(dataDir, app.handleCommand); err != nil {
		log.Printf("command channel unavailable: %v", err)
	} else {
		defer srv.Close()
	}

	err = wails.Run(&options.App{
		Title:            "Yellow Flash Download Manager",
		Width:            1280,
		Height:           820,
		MinWidth:         980,
		MinHeight:        620,
		BackgroundColour: &options.RGBA{R: 12, G: 12, B: 14, A: 255},
		AssetServer:      &assetserver.Options{Assets: assets},
		OnStartup:        app.Startup,
		OnDomReady:       app.domReady,
		OnShutdown:       app.Shutdown,
		Bind:             []interface{}{app},
		Windows:          &windows.Options{Theme: windows.Dark},
		Linux:            &linux.Options{ProgramName: "yellow-flash"},
	})
	if err != nil {
		log.Fatal(err)
	}
}

// becomeInstance hands cmd to a running instance, or takes the instance
// lock. ok is false when this launch's work is done (handed over, or /exit
// with nothing running).
func becomeInstance(dataDir string, cmd ipc.Command, hasCmd bool) (io.Closer, bool) {
	send := func() bool {
		if pid, ok := ipc.AppPID(dataDir); ok {
			platform.AllowForeground(pid)
		}
		return ipc.Send(dataDir, cmd) == nil
	}
	if send() || (hasCmd && cmd.Exit) {
		return nil, false
	}
	// No instance answered. Only one launch may become the instance (two
	// started together both get here); the other hands its command over once
	// the winner listens, or takes over if the holder was quitting.
	deadline := time.Now().Add(handOverWait)
	for {
		lock, err := ipc.Lock(dataDir)
		if err == nil {
			return lock, true
		}
		if !errors.Is(err, ipc.ErrAlreadyRunning) {
			log.Fatal(err)
		}
		if send() {
			return nil, false
		}
		if time.Now().After(deadline) {
			log.Printf("another instance holds %s but does not answer", dataDir)
			return nil, false
		}
		time.Sleep(100 * time.Millisecond)
	}
}

// absolutePaths resolves a relative /p folder or .torrent path against this
// launch's working directory: the running instance has its own.
func absolutePaths(cmd *ipc.Command) {
	if cmd.Dir != "" {
		if abs, err := filepath.Abs(cmd.Dir); err == nil {
			cmd.Dir = abs
		}
	}
	if u := cmd.URL; u != "" && !filepath.IsAbs(u) && filepath.Ext(u) == ".torrent" {
		if _, err := os.Stat(u); err == nil {
			if abs, err := filepath.Abs(u); err == nil {
				cmd.URL = abs
			}
		}
	}
}
