// Ported from Mister Download Manager, crates/cli/src/lib.rs and crates/cli/src/main.rs
// Copyright (C) 2026 Ahmed Nefzaoui and contributors; Go port (C) 2026 the Yellow Flash contributors.
// Translated from Rust to Go and modified. SPDX-License-Identifier: GPL-3.0-or-later

// Command yellowflash-cli is Yellow Flash's command line: downloads,
// videos, galleries and torrents without the window, and the running app's
// automation API.
//
// MDM's command line runs inside the app's program (one engine shipped);
// Yellow Flash builds the same engine into this program instead. Its own
// downloads are kept in <data>/cli (never the app's list); its helpers
// (yt-dlp, ffmpeg, gallery-dl) are the app's, in <data>/tools.
package main

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"os/signal"
	"path/filepath"
	"slices"
	"strings"
	"syscall"

	"github.com/vaisah/internal/ipc"
)

// name is the program's name, as its help shows it.
const name = "yellowflash-cli"

var (
	stdout io.Writer = os.Stdout
	stderr io.Writer = os.Stderr
	// jsonOut: --json, output for programs.
	jsonOut bool
)

// errReported: the failure was printed already (the app's own {"error"}
// answer); exit 1, print nothing more.
var errReported = errors.New("reported")

// errUsage: the arguments are wrong (exit 2).
var errUsage = errors.New("usage")

type command struct {
	name, args, help string
	run              func(ctx context.Context, args []string) error
}

func commands() []command {
	return []command{
		{"get", "URL [-o FOLDER] [--name FILE] [-c N] [-H 'Name: value']... [--checksum HASH]",
			"Download an address (http, https, ftp). Ctrl+C pauses; continue with resume.", cmdGet},
		{"resume", "ID [-c N]", "Continue a download get paused or that failed, by its id.", cmdResume},
		{"list", "", "This command line's own downloads (not the app's: see status).", cmdList},
		{"status", "", "The running app's downloads, as JSON (automation API; turn it on with api on).", cmdStatus},
		{"add", "URL [-o FOLDER] [--name FILE] [--later] [--queue NAME]", "Add an address or magnet link to the running app.", cmdAdd},
		{"pause", "ID...", "Pause downloads in the running app, by their ids from status (automation API).", cmdAct("pause")},
		{"start", "ID...", "Start (or resume) downloads in the running app, by id (automation API).", cmdAct("start")},
		{"remove", "ID...", "Take downloads off the running app's list, keeping their files (automation API).", cmdAct("remove")},
		{"delete", "ID...", "Take downloads off the running app's list and delete their files (automation API).", cmdAct("delete")},
		{"pair", "", "Show a new code that pairs a phone with the running app's remote control: its address, the code and a QR code.", cmdPair},
		{"api", "on|off", "Turn the running app's automation API on or off (off by default).", cmdAPI},
		{"install-ytdlp", "", "Install (or update) yt-dlp for videos: its checksum list is verified against yt-dlp's signing key.", cmdInstall("yt-dlp")},
		{"install-ffmpeg", "", "Install (or update) ffmpeg, for the video qualities that merge video and audio (Windows; elsewhere the system's).", cmdInstall("ffmpeg")},
		{"install-gallery-dl", "", "Install (or update) gallery-dl for image galleries: its checksum list is verified against its author's key.", cmdInstall("gallery-dl")},
		{"video-info", "URL", "What video a page holds, and the choices to download (needs install-ytdlp).", cmdVideoInfo},
		{"video-get", "URL [-f CHOICE] [-o FOLDER] [--subs LANGS] [--auto-subs] [--embed-subs]", "Download a video with yt-dlp (needs install-ytdlp).", cmdVideoGet},
		{"playlist-info", "URL [--from N] [--count N]", "The videos of a playlist or a channel, listed as they come (needs install-ytdlp).", cmdPlaylistInfo},
		{"gallery-get", "URL [-o FOLDER] [--limit N]", "Download an image gallery's files with gallery-dl (needs install-gallery-dl).", cmdGalleryGet},
		{"torrent-create", "PATH -o FILE", "Make a .torrent of a file or folder (no trackers).", cmdTorrentCreate},
		{"torrent-info", "SOURCE [--proxy socks5://HOST:PORT]", "What a torrent holds: its name, files (numbered) and size. A magnet link needs the proxy.", cmdTorrentInfo},
		{"torrent-get", "SOURCE --proxy socks5://HOST:PORT [-o FOLDER] [--files 0,2] [--seconds N] [--use-existing]", "Download a torrent, every connection through the SOCKS5 proxy (peers never see this computer's address).", cmdTorrentGet},
	}
}

func usage(w io.Writer) {
	fmt.Fprintf(w, "%s %s: Yellow Flash's command line.\n\nUsage: %s [--json] COMMAND [ARGUMENTS]\n\n", name, ipc.AppVersion, name)
	for _, c := range commands() {
		fmt.Fprintf(w, "  %s %s\n      %s\n", c.name, c.args, c.help)
	}
	fmt.Fprintf(w, "\n--json prints JSON for programs: one object per line, {\"error\": …} on failure.\n")
}

func main() {
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	code := run(ctx, os.Args[1:])
	stop()
	os.Exit(code)
}

// run runs the command line args (without the program's name): its exit
// code (1 when a command fails, 2 for wrong arguments).
func run(ctx context.Context, args []string) int {
	jsonOut = slices.Contains(args, "--json")
	args = slices.DeleteFunc(slices.Clone(args), func(a string) bool { return a == "--json" })
	if len(args) == 0 || args[0] == "help" || args[0] == "-h" || args[0] == "--help" {
		usage(stdout)
		return 0
	}
	if args[0] == "--version" || args[0] == "-V" || args[0] == "version" {
		fmt.Fprintln(stdout, name+" "+ipc.AppVersion)
		return 0
	}
	i := slices.IndexFunc(commands(), func(c command) bool { return c.name == args[0] })
	if i < 0 {
		fmt.Fprintf(stderr, "%s: no command %q (%s help lists them)\n", name, args[0], name)
		return 2
	}
	c := commands()[i]
	err := c.run(ctx, args[1:])
	switch {
	case err == nil:
		return 0
	case errors.Is(err, errReported):
		return 1
	case errors.Is(err, errUsage):
		fmt.Fprintf(stderr, "usage: %s %s %s\n", name, c.name, c.args)
		if msg := strings.TrimPrefix(err.Error(), errUsage.Error()); msg != "" {
			fmt.Fprintln(stderr, strings.TrimPrefix(msg, ": "))
		}
		return 2
	}
	if jsonOut {
		emit(map[string]any{"error": err.Error()})
	} else {
		fmt.Fprintf(stderr, "%s: %v\n", name, err)
	}
	return 1
}

// emit prints one JSON line (--json).
func emit(v any) {
	b, _ := json.Marshal(v)
	fmt.Fprintln(stdout, string(b))
}

func usageErr(format string, args ...any) error {
	return fmt.Errorf("%w: "+format, append([]any{errUsage}, args...)...)
}

// parse reads a command's flags wherever they are among its arguments
// (get URL -o folder, as well as get -o folder URL): its other arguments.
func parse(fs *flag.FlagSet, args []string) ([]string, error) {
	fs.SetOutput(io.Discard)
	var rest []string
	for {
		if err := fs.Parse(args); err != nil {
			return nil, usageErr("%v", err)
		}
		args = fs.Args()
		if len(args) == 0 {
			return rest, nil
		}
		if args[0] == "--" {
			return append(rest, args[1:]...), nil
		}
		rest = append(rest, args[0])
		args = args[1:]
	}
}

// listFlag is a flag given several times (-H).
type listFlag []string

func (l *listFlag) String() string     { return strings.Join(*l, ", ") }
func (l *listFlag) Set(v string) error { *l = append(*l, v); return nil }

// dataDir is the app's data folder (YELLOWFLASH_DATA_DIR overrides it).
func dataDir() (string, error) { return ipc.DataDir() }

// cliDir is this command line's own folder in the data folder.
func cliDir() (string, error) {
	d, err := dataDir()
	if err != nil {
		return "", err
	}
	dir := filepath.Join(d, "cli")
	return dir, ipc.PrivateDir(dir)
}
