// Ported from Mister Download Manager, crates/cli/src/lib.rs (torrent-create, torrent-info, torrent-get)
// Copyright (C) 2026 Ahmed Nefzaoui and contributors; Go port (C) 2026 the Yellow Flash contributors.
// Translated from Rust to Go and modified. SPDX-License-Identifier: GPL-3.0-or-later

package main

// Torrents without the app. As in the app, peers never see this computer's
// address: torrent-get and a magnet's torrent-info go through a SOCKS5
// proxy (--proxy), or don't run.

import (
	"bytes"
	"context"
	"errors"
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/anacrolix/torrent/bencode"
	"github.com/anacrolix/torrent/metainfo"
	"github.com/vaisah/internal/ipc"
	"github.com/vaisah/internal/torrent"
)

// errNoProxy is torrent.ErrNoProxy said for the command line.
var errNoProxy = errors.New("give --proxy socks5://HOST:PORT: torrents go only through a SOCKS5 proxy, so peers never see this computer's address")

func cmdTorrentCreate(_ context.Context, args []string) error {
	fs := flag.NewFlagSet("torrent-create", flag.ContinueOnError)
	out := fs.String("o", "", "the .torrent file to write")
	fs.StringVar(out, "output", "", "the .torrent file to write")
	rest, err := parse(fs, args)
	if err != nil {
		return err
	}
	if len(rest) != 1 || *out == "" {
		return usageErr("a file or folder, and -o FILE")
	}
	root, err := filepath.Abs(rest[0])
	if err != nil {
		return err
	}
	var total int64
	err = filepath.Walk(root, func(_ string, fi os.FileInfo, err error) error {
		if err == nil && fi.Mode().IsRegular() {
			total += fi.Size()
		}
		return err
	})
	if err != nil {
		return err
	}
	info := metainfo.Info{PieceLength: metainfo.ChoosePieceLength(total)}
	if err := info.BuildFromFilePath(root); err != nil {
		return err
	}
	ib, err := bencode.Marshal(info)
	if err != nil {
		return err
	}
	mi := metainfo.MetaInfo{InfoBytes: ib, CreatedBy: name + " " + ipc.AppVersion, CreationDate: time.Now().Unix()}
	var b bytes.Buffer
	if err := mi.Write(&b); err != nil {
		return err
	}
	if _, err := torrent.MetaFromTorrent(b.Bytes()); err != nil {
		return err // a name no system here could write
	}
	if err := os.WriteFile(*out, b.Bytes(), 0o644); err != nil {
		return err
	}
	link, _ := torrent.MagnetLink(b.Bytes())
	hash := mi.HashInfoBytes().HexString()
	if jsonOut {
		emit(map[string]any{"torrent": *out, "infoHash": hash, "size": total, "magnet": link})
	} else {
		fmt.Fprintf(stdout, "wrote %s (%s, %s)\n%s\n", *out, humanBytes(total), hash, link)
	}
	return nil
}

// startTorrents is a torrent session through proxy, its checked pieces in
// <data>/cli/torrents, and its end (the command line's folder let go).
func startTorrents(proxy string) (*torrent.Torrents, func(), error) {
	if strings.TrimSpace(proxy) == "" {
		return nil, nil, errNoProxy
	}
	dir, err := cliDir()
	if err != nil {
		return nil, nil, err
	}
	lock, err := ipc.Lock(dir)
	if err != nil {
		return nil, nil, fmt.Errorf("another %s download is running (%v)", name, err)
	}
	s, err := torrent.Start(torrent.NewConfig(filepath.Join(dir, "torrents"), proxy))
	if err != nil {
		lock.Close()
		return nil, nil, err
	}
	return s, func() { s.Stop(); lock.Close() }, nil
}

// readMeta is what source holds: a .torrent file read here, or a magnet
// link asked of its peers through s.
func readMeta(ctx context.Context, source string, s *torrent.Torrents) (torrent.Meta, error) {
	if !torrent.IsMagnet(source) {
		b, err := torrent.ReadFile(source)
		if err != nil {
			return torrent.Meta{}, err
		}
		return torrent.MetaFromTorrent(b)
	}
	if !jsonOut {
		fmt.Fprintln(stderr, "asking the torrent's peers for its files...")
	}
	ctx, cancel := context.WithTimeout(ctx, 2*time.Minute)
	defer cancel()
	return s.Metadata(ctx, source)
}

func printMeta(m torrent.Meta) {
	if jsonOut {
		emit(map[string]any{"name": m.Name, "infoHash": m.InfoHash, "files": m.Files, "total": m.Total()})
		return
	}
	fmt.Fprintf(stdout, "%s (%s)\n%s\n", m.Name, humanBytes(m.Total()), m.InfoHash)
	for i, f := range m.Files {
		if !f.Padding {
			fmt.Fprintf(stdout, "%5d  %10s  %s\n", i, humanBytes(f.Size), f.Path)
		}
	}
}

func cmdTorrentInfo(ctx context.Context, args []string) error {
	fs := flag.NewFlagSet("torrent-info", flag.ContinueOnError)
	proxy := fs.String("proxy", "", "the SOCKS5 proxy a magnet link's peers are asked through")
	rest, err := parse(fs, args)
	if err != nil {
		return err
	}
	if len(rest) != 1 {
		return usageErr("a magnet link or a .torrent file")
	}
	var s *torrent.Torrents
	if torrent.IsMagnet(rest[0]) {
		var stop func()
		if s, stop, err = startTorrents(*proxy); err != nil {
			return err
		}
		defer stop()
	}
	m, err := readMeta(ctx, rest[0], s)
	if err != nil {
		return err
	}
	printMeta(m)
	return nil
}

func cmdTorrentGet(ctx context.Context, args []string) error {
	fs := flag.NewFlagSet("torrent-get", flag.ContinueOnError)
	proxy := fs.String("proxy", "", "the SOCKS5 proxy every connection goes through (required)")
	out := fs.String("o", ".", "folder to save into")
	fs.StringVar(out, "output", ".", "folder to save into")
	files := fs.String("files", "", "only these files, by their numbers in torrent-info (e.g. 0,2); all by default")
	seconds := fs.Int("seconds", 0, "stop after this many seconds (a test run)")
	useExisting := fs.Bool("use-existing", false, "use the torrent's files already in the folder (what matches is kept)")
	rest, err := parse(fs, args)
	if err != nil {
		return err
	}
	if len(rest) != 1 {
		return usageErr("a magnet link or a .torrent file, and --proxy")
	}
	var chosen []int
	if *files != "" {
		for _, p := range strings.Split(*files, ",") {
			n, err := strconv.Atoi(strings.TrimSpace(p))
			if err != nil {
				return usageErr("--files takes numbers: %q", p)
			}
			chosen = append(chosen, n)
		}
	}
	dir, err := filepath.Abs(*out)
	if err != nil {
		return err
	}
	s, stop, err := startTorrents(*proxy)
	if err != nil {
		return err
	}
	defer stop()
	m, err := readMeta(ctx, rest[0], s)
	if err != nil {
		return err
	}
	for _, i := range chosen {
		if i < 0 || i >= len(m.Files) || m.Files[i].Padding {
			return fmt.Errorf("no file %d in the torrent (torrent-info numbers them)", i)
		}
	}
	if existing := m.ExistingIn(dir); len(existing) > 0 && !*useExisting {
		return fmt.Errorf("the torrent's files are in %s already (%s…); --use-existing keeps what matches", dir, existing[0])
	}
	hash, err := s.Add(m.Torrent, dir, chosen, false)
	if err != nil {
		return err
	}
	if !jsonOut {
		fmt.Fprintf(stderr, "%s -> %s\n", m.Name, m.FolderIn(dir))
	}
	var deadline <-chan time.Time
	if *seconds > 0 {
		deadline = time.After(time.Duration(*seconds) * time.Second)
	}
	tick := time.NewTicker(time.Second)
	defer tick.Stop()
	for {
		var stopped string
		select {
		case <-ctx.Done():
			stopped = "interrupted"
		case <-deadline:
			stopped = "time up"
		case <-tick.C:
		}
		st, err := s.Stats(hash)
		if err != nil {
			return err
		}
		switch {
		case st.Phase == torrent.Finished:
			if jsonOut {
				emit(map[string]any{"event": "done", "folder": m.FolderIn(dir), "size": st.Total})
			} else {
				fmt.Fprintf(stdout, "\ncompleted %s (%s)\n", m.FolderIn(dir), humanBytes(st.Total))
			}
			return nil
		case stopped != "":
			if jsonOut {
				emit(map[string]any{"event": "stopped", "why": stopped, "done": st.Done, "total": st.Total})
			} else {
				fmt.Fprintf(stdout, "\n%s at %s of %s; run it again with --use-existing to continue\n", stopped, humanBytes(st.Done), humanBytes(st.Total))
			}
			return errReported
		}
		if jsonOut {
			emit(map[string]any{"event": "progress", "done": st.Done, "total": st.Total, "speed": int64(st.DownSpeed),
				"peers": st.PeersLive})
		} else {
			fmt.Fprintf(stderr, "\r%s / %s  %s/s  peers %d   ", humanBytes(st.Done), humanBytes(st.Total),
				humanBytes(int64(st.DownSpeed)), st.PeersLive)
		}
	}
}
