// Ported from Mister Download Manager, crates/torrent/src/lib.rs
// Copyright (C) 2026 Ahmed Nefzaoui and contributors; Go port (C) 2026 the Yellow Flash contributors.
// Translated from Rust to Go and modified. SPDX-License-Identifier: GPL-3.0-or-later

// Package torrent is BitTorrent and magnet downloads behind the few calls
// the app needs: read what a torrent holds without starting it, add it with
// the files and folder the person chose, then watch it (stats, peers),
// pause, resume and remove it. Torrents are named by their info hash (40
// hex digits). MDM's crate wraps librqbit; this one wraps anacrolix/torrent,
// with MDM's rules: file names checked (names.go), trackers on this
// computer or its network dropped (trackers.go), web seeds and a magnet's
// download sources off (they are web addresses the torrent names), and
// blocklists (blocklist.go).
package torrent

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"math"
	"net"
	"net/http"
	"net/netip"
	"net/url"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"time"

	bt "github.com/anacrolix/torrent"
	"github.com/anacrolix/torrent/dialer"
	"github.com/anacrolix/torrent/iplist"
	"github.com/anacrolix/torrent/metainfo"
	"github.com/anacrolix/torrent/storage"
	xproxy "golang.org/x/net/proxy"
	"golang.org/x/time/rate"

	"github.com/vaisah/internal/ipc"
)

var (
	// ErrNotATorrent: not a magnet link or a .torrent file.
	ErrNotATorrent = errors.New("this is not a magnet link or a .torrent file")
	// ErrMetadataTimeout: no peer sent the torrent's file list in time.
	ErrMetadataTimeout = errors.New("no peer sent the torrent's file list in time")
	// ErrUnknown: the torrent is not in the session.
	ErrUnknown = errors.New("this torrent is not in the list")
	// ErrNoProxy: torrents run only through a SOCKS5 proxy, so peers never
	// see this computer's address.
	ErrNoProxy = errors.New("torrents need a SOCKS5 proxy (set one in the connection settings), so peers don't see this computer's address")
	// ErrProxyKind: the proxy can't carry peer connections (only SOCKS5
	// can).
	ErrProxyKind = errors.New("torrents need a SOCKS5 proxy: an HTTP proxy can't carry connections to peers")
)

// MaxTorrentFile is the most a .torrent file may be: the largest real ones
// (a great many files) are a few MB; anything bigger isn't one.
const MaxTorrentFile = 10 << 20

// ReadFile reads a .torrent file from disk, at most MaxTorrentFile.
func ReadFile(path string) ([]byte, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	b, err := io.ReadAll(io.LimitReader(f, MaxTorrentFile+1))
	if err != nil {
		return nil, err
	}
	if len(b) > MaxTorrentFile {
		return nil, fmt.Errorf("%w: larger than a .torrent file can be", ErrNotATorrent)
	}
	return b, nil
}

// IsMagnet reports a magnet link.
func IsMagnet(text string) bool {
	t := strings.TrimSpace(text)
	return len(t) >= 8 && strings.EqualFold(t[:8], "magnet:?")
}

// FileEntry is one file inside a torrent.
type FileEntry struct {
	// Path is relative to the torrent's folder, parts joined by /.
	Path string `json:"path"`
	Size int64  `json:"size"`
	// Padding: BEP 47 padding files, never shown, never chosen.
	Padding bool `json:"padding"`
}

// Meta is what a torrent holds, before anything is downloaded.
type Meta struct {
	Name        string      `json:"name"`
	InfoHash    string      `json:"infoHash"`
	Files       []FileEntry `json:"files"`
	PieceLength int64       `json:"pieceLength"`
	// Torrent is a .torrent file for it (for a magnet: built from the
	// metadata the peers sent), for the app to keep so the torrent can be
	// added again without asking the swarm.
	Torrent []byte `json:"-"`
	// multi: its files go in a folder of its own.
	multi  bool
	pieces int
}

// Total is the size of its files (padding never counts).
func (m Meta) Total() int64 {
	var n int64
	for _, f := range m.Files {
		if !f.Padding {
			n += f.Size
		}
	}
	return n
}

// FolderIn is where its files go when saved in dir: a torrent of several
// files gets its own folder (named after it), a single file goes straight
// in.
func (m Meta) FolderIn(dir string) string {
	if m.multi {
		return filepath.Join(dir, m.Name)
	}
	return dir
}

// paths are where its files (not padding) are written in dir.
func (m Meta) paths(dir string) []string {
	var out []string
	for _, f := range m.Files {
		if f.Padding {
			continue
		}
		if m.multi {
			out = append(out, filepath.Join(dir, m.Name, filepath.FromSlash(f.Path)))
		} else {
			out = append(out, filepath.Join(dir, m.Name))
		}
	}
	return out
}

// ExistingIn are its files that already exist where it would write them in
// dir.
func (m Meta) ExistingIn(dir string) []string {
	var out []string
	for _, p := range m.paths(dir) {
		if _, err := os.Stat(p); err == nil {
			out = append(out, p)
		}
	}
	return out
}

// MetaFromTorrent reads a .torrent file's bytes: a torrent whose names are
// safe for this system, or an error.
func MetaFromTorrent(b []byte) (Meta, error) {
	mi, err := metainfo.Load(bytes.NewReader(b))
	if err != nil {
		return Meta{}, ErrNotATorrent
	}
	info, err := mi.UnmarshalInfo()
	// Torrents are named by their v1 hash (v2-only torrents, BEP 52,
	// have none).
	if err != nil || !info.HasV1() {
		return Meta{}, ErrNotATorrent
	}
	m := Meta{Name: info.BestName(), InfoHash: mi.HashInfoBytes().HexString(), PieceLength: info.PieceLength,
		Torrent: b, multi: len(info.Files) > 0, pieces: info.NumPieces()}
	var names [][]string
	for _, f := range info.UpvertedFiles() {
		padding := strings.Contains(f.Attr, "p")
		path := f.BestPath()
		m.Files = append(m.Files, FileEntry{Path: strings.Join(path, "/"), Size: f.Length, Padding: padding})
		if padding {
			continue
		}
		if m.multi {
			names = append(names, append([]string{m.Name}, path...))
		} else {
			names = append(names, []string{m.Name})
		}
	}
	if err := CheckNames(names); err != nil {
		return Meta{}, err
	}
	return m, nil
}

// MagnetLink is a magnet link for a .torrent: its hash, its name and its
// trackers.
func MagnetLink(b []byte) (string, bool) {
	m, err := MetaFromTorrent(b)
	if err != nil {
		return "", false
	}
	link := "magnet:?xt=urn:btih:" + m.InfoHash
	if m.Name != "" {
		link += "&dn=" + url.QueryEscape(m.Name)
	}
	for _, t := range TrackersListed(b) {
		link += "&tr=" + url.QueryEscape(t)
	}
	return link, true
}

// Phase is where a torrent is.
type Phase string

const (
	Downloading Phase = "downloading"
	// Finished: everything chosen is there (it may still share).
	Finished Phase = "finished"
	Paused   Phase = "paused"
)

// Stats is a torrent's state.
type Stats struct {
	Phase Phase `json:"phase"`
	// Done and Total are bytes of the chosen files there (checked) and in
	// all.
	Done     int64 `json:"done"`
	Total    int64 `json:"total"`
	Uploaded int64 `json:"uploaded"`
	// DownSpeed and UpSpeed are bytes/s.
	DownSpeed float64 `json:"downSpeed"`
	UpSpeed   float64 `json:"upSpeed"`
	// FileDone is per file, bytes there, in the order of Meta.Files.
	FileDone  []int64 `json:"fileDone"`
	PeersLive int     `json:"peersLive"`
	PeersSeen int     `json:"peersSeen"`
}

// Peer is one peer of a torrent.
type Peer struct {
	Addr   string `json:"addr"`
	Client string `json:"client"`
	// Fetched and Uploaded are bytes from and to it.
	Fetched  int64  `json:"fetched"`
	Uploaded int64  `json:"uploaded"`
	Kind     string `json:"kind"`
}

// Config is how the session runs.
//
// Peers never see this computer's address: every connection to a peer and
// to a tracker goes through Proxy (SOCKS5), and nothing that would be
// reached directly runs: no listening for peers, no UPnP, no DHT and no
// UDP trackers (UDP can't go through the proxy), no web seeds or
// WebTorrent. Without a proxy torrents are refused (ErrNoProxy).
type Config struct {
	// StateDir keeps the pieces already checked, and magnet look-ups.
	StateDir string
	// Proxy is a socks5:// or socks5h:// address (credentials may be in
	// it; they are never shown in errors).
	Proxy string
	// Blocklist: peers in it are never dialled.
	Blocklist *Blocklist
	// Offline is for tests: peers on this computer are allowed, and with
	// no Proxy the session connects directly and listens on loopback.
	Offline bool
}

// NewConfig is a session through proxy.
func NewConfig(stateDir, proxy string) Config {
	return Config{StateDir: stateDir, Proxy: proxy}
}

// proxyDialer is the SOCKS5 dialer for proxy.
func proxyDialer(raw string) (*url.URL, xproxy.ContextDialer, error) {
	u, err := url.Parse(strings.TrimSpace(raw))
	if err != nil || u.Host == "" {
		return nil, nil, ErrProxyKind
	}
	switch strings.ToLower(u.Scheme) {
	case "socks5", "socks5h":
	default:
		return nil, nil, ErrProxyKind
	}
	d, err := xproxy.FromURL(u, xproxy.Direct)
	if err != nil {
		return nil, nil, ErrProxyKind
	}
	cd, ok := d.(xproxy.ContextDialer)
	if !ok {
		return nil, nil, ErrProxyKind
	}
	return u, cd, nil
}

// MinRate is the lowest limit a session can have, bytes/s: a whole 16 KiB
// block is asked of the limiter at once.
const MinRate = 16 * 1024

type item struct {
	t       *bt.Torrent // nil while paused
	meta    Meta
	torrent []byte // with the allowed trackers only
	dir     string
	chosen  map[int]bool // nil: every file
	// The last sample for the speeds.
	at          time.Time
	read, wrote int64
	down, up    float64
	// The stats when it was paused.
	last    Stats
	hasLast bool
}

// Torrents is the session.
type Torrents struct {
	cl       *bt.Client
	pc       storage.PieceCompletion
	down, up *rate.Limiter
	offline  bool
	proxied  bool
	mu       sync.Mutex
	items    map[string]*item
}

func ipRanges(b *Blocklist) []iplist.Range {
	var out []iplist.Range
	for _, r := range b.Ranges {
		out = append(out, iplist.Range{First: net.IP(r.Start.AsSlice()), Last: net.IP(r.End.AsSlice()), Description: "blocklist"})
	}
	slices.SortFunc(out, func(a, b iplist.Range) int { return bytes.Compare(a.First.To16(), b.First.To16()) })
	return out
}

// errNoUDP refuses UDP trackers (they are dropped from every torrent when
// proxied; this is in case one is left).
var errNoUDP = errors.New("UDP can't go through the proxy")

// Start starts the session.
func Start(cfg Config) (*Torrents, error) {
	var (
		proxyURL *url.URL
		dial     xproxy.ContextDialer
	)
	switch {
	case strings.TrimSpace(cfg.Proxy) != "":
		var err error
		if proxyURL, dial, err = proxyDialer(cfg.Proxy); err != nil {
			return nil, err
		}
	case !cfg.Offline:
		return nil, ErrNoProxy
	}
	if err := os.MkdirAll(cfg.StateDir, 0o700); err != nil {
		return nil, err
	}
	pc, err := storage.NewBoltPieceCompletion(cfg.StateDir)
	if err != nil {
		return nil, err
	}
	s := &Torrents{pc: pc, items: map[string]*item{}, offline: cfg.Offline, proxied: dial != nil,
		down: rate.NewLimiter(rate.Inf, 1<<20), up: rate.NewLimiter(rate.Inf, 1<<20)}
	lookups := filepath.Join(cfg.StateDir, "lookups")
	c := bt.NewDefaultClientConfig()
	c.DataDir = lookups
	c.DefaultStorage = s.storageFor(lookups)
	c.Seed = true
	c.NoDHT = true
	c.NoDefaultPortForwarding = true
	// Web seeds are web addresses the torrent names; WebTorrent goes to
	// signalling servers. Neither is wanted.
	c.DisableWebseeds = true
	c.DisableWebtorrent = true
	c.HTTPUserAgent = "YellowFlash/" + ipc.AppVersion
	c.DownloadRateLimiter, c.UploadRateLimiter = s.down, s.up
	if cfg.Blocklist != nil && len(cfg.Blocklist.Ranges) > 0 {
		c.IPBlocklist = iplist.New(ipRanges(cfg.Blocklist))
	}
	if s.proxied {
		// Nothing of its own reaches the network: no sockets (so no address
		// to tell peers or trackers either), peers dialled through the
		// proxy, HTTP trackers fetched through it.
		c.DisableTCP, c.DisableUTP = true, true
		c.AcceptPeerConnections = false
		c.HTTPProxy = http.ProxyURL(proxyURL)
		c.TrackerListenPacket = func(string, string) (net.PacketConn, error) { return nil, errNoUDP }
	} else {
		// Offline (tests): direct, on loopback only.
		c.ListenHost = func(string) string { return "127.0.0.1" }
		c.DisableIPv6 = true
	}
	// With any port (0), the TCP port picked is then asked of UDP too;
	// Windows refuses ports in its reserved ranges (Hyper-V, Docker) with
	// "access denied", which anacrolix doesn't try again: another pick
	// does.
	cl, err := bt.NewClient(c)
	for try := 0; err != nil && strings.Contains(err.Error(), "subsequent listen") && try < 20; try++ {
		cl, err = bt.NewClient(c)
	}
	if err != nil {
		_ = pc.Close()
		return nil, err
	}
	if s.proxied {
		cl.AddDialer(dialer.WithNetwork{Network: "tcp", Dialer: dial})
	}
	s.cl = cl
	return s, nil
}

// storageFor writes a torrent's files in dir, in place (files.go), its
// checked pieces kept in the session's store.
func (s *Torrents) storageFor(dir string) storage.ClientImpl {
	return fileStore{dir: dir, pc: s.pc}
}

// strip takes out of a spec what the torrent names that isn't an allowed
// tracker or a peer on the internet: web seeds, a magnet's download sources
// (xs, as), and peers it names (x.pe) on this computer or its network
// (allowed offline: the tests' loopback swarm).
func (s *Torrents) strip(spec *bt.TorrentSpec) {
	spec.Webseeds, spec.Sources = nil, nil
	if !s.offline {
		spec.PeerAddrs = peersAllowed(spec.PeerAddrs)
	}
}

// peersAllowed are the peers on the internet among addrs.
func peersAllowed(addrs []string) []string {
	var out []string
	for _, a := range addrs {
		if ap, err := netip.ParseAddrPort(strings.TrimSpace(a)); err == nil && publicAddr(ap.Addr()) {
			out = append(out, a)
		}
	}
	return out
}

// Metadata asks the swarm what a magnet link's torrent holds (contacting
// only the trackers allowed), nothing downloaded, until ctx ends. Its
// Torrent holds the trackers too.
func (s *Torrents) Metadata(ctx context.Context, magnet string) (Meta, error) {
	if !IsMagnet(magnet) {
		return Meta{}, ErrNotATorrent
	}
	filtered := FilterMagnet(strings.TrimSpace(magnet), s.proxied)
	spec, err := bt.TorrentSpecFromMagnetUri(filtered)
	if err != nil {
		return Meta{}, ErrNotATorrent
	}
	s.mu.Lock()
	if it, ok := s.items[spec.InfoHash.HexString()]; ok {
		s.mu.Unlock()
		return it.meta, nil
	}
	s.mu.Unlock()
	s.strip(spec)
	spec.DisallowDataDownload, spec.DisallowDataUpload = true, true
	t, isNew, err := s.cl.AddTorrentSpec(spec)
	if err != nil {
		return Meta{}, err
	}
	if isNew {
		defer t.Drop()
	}
	select {
	case <-t.GotInfo():
	case <-ctx.Done():
		return Meta{}, ErrMetadataTimeout
	}
	mi := t.Metainfo()
	mi.Announce, mi.AnnounceList = "", nil
	var b bytes.Buffer
	if err := mi.Write(&b); err != nil {
		return Meta{}, err
	}
	torrent := b.Bytes()
	if trackers := TrackersInMagnet(filtered); len(trackers) > 0 {
		if with, ok := AddTrackers(torrent, trackers); ok {
			torrent = with
		}
	}
	return MetaFromTorrent(torrent)
}

// Add adds a torrent (its .torrent bytes) to save in dir, only the files
// chosen (indexes in Meta.Files; nil: all), started unless paused. It
// returns the torrent's info hash; one already in the session is left as
// it is.
func (s *Torrents) Add(torrent []byte, dir string, chosen []int, paused bool) (string, error) {
	meta, err := MetaFromTorrent(torrent)
	if err != nil {
		return "", err
	}
	if filtered, ok := FilterTorrent(torrent, s.proxied); ok {
		torrent = filtered
	}
	it := &item{meta: meta, torrent: torrent, dir: dir}
	if chosen != nil {
		it.chosen = map[int]bool{}
		for _, i := range chosen {
			it.chosen[i] = true
		}
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if _, ok := s.items[meta.InfoHash]; ok {
		return meta.InfoHash, nil
	}
	if !paused {
		if err := s.startLocked(it); err != nil {
			return "", err
		}
	}
	s.items[meta.InfoHash] = it
	return meta.InfoHash, nil
}

func (s *Torrents) startLocked(it *item) error {
	mi, err := metainfo.Load(bytes.NewReader(it.torrent))
	if err != nil {
		return ErrNotATorrent
	}
	spec, err := bt.TorrentSpecFromMetaInfoErr(mi)
	if err != nil {
		return ErrNotATorrent
	}
	s.strip(spec)
	spec.Storage = s.storageFor(it.dir)
	t, _, err := s.cl.AddTorrentSpec(spec)
	if err != nil {
		return err
	}
	<-t.GotInfo()
	for i, f := range t.Files() {
		if it.wanted(i) {
			f.SetPriority(bt.PiecePriorityNormal)
		} else {
			f.SetPriority(bt.PiecePriorityNone)
		}
	}
	it.t, it.at, it.read, it.wrote, it.down, it.up = t, time.Now(), 0, 0, 0, 0
	return nil
}

// wanted reports file i chosen (padding never is).
func (it *item) wanted(i int) bool {
	return i < len(it.meta.Files) && !it.meta.Files[i].Padding && (it.chosen == nil || it.chosen[i])
}

func (s *Torrents) get(hash string) (*item, error) {
	it, ok := s.items[strings.ToLower(strings.TrimSpace(hash))]
	if !ok {
		return nil, ErrUnknown
	}
	return it, nil
}

// Has reports a torrent in the session.
func (s *Torrents) Has(hash string) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	_, err := s.get(hash)
	return err == nil
}

// Hashes are the torrents in the session, sorted.
func (s *Torrents) Hashes() []string {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := make([]string, 0, len(s.items))
	for h := range s.items {
		out = append(out, h)
	}
	slices.Sort(out)
	return out
}

// Meta is what a torrent in the session holds.
func (s *Torrents) Meta(hash string) (Meta, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	it, err := s.get(hash)
	if err != nil {
		return Meta{}, err
	}
	return it.meta, nil
}

// Stats is a torrent's state.
func (s *Torrents) Stats(hash string) (Stats, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	it, err := s.get(hash)
	if err != nil {
		return Stats{}, err
	}
	if it.t != nil {
		return it.stats(), nil
	}
	st := it.last
	if !it.hasLast {
		for i, f := range it.meta.Files {
			if it.wanted(i) {
				st.Total += f.Size
			}
		}
	}
	st.Phase, st.DownSpeed, st.UpSpeed, st.PeersLive = Paused, 0, 0, 0
	return st, nil
}

func (it *item) stats() Stats {
	var st Stats
	for i, f := range it.t.Files() {
		done := f.BytesCompleted()
		st.FileDone = append(st.FileDone, done)
		if it.wanted(i) {
			st.Done += done
			st.Total += f.Length()
		}
	}
	ts := it.t.Stats()
	read, wrote := ts.BytesReadData.Int64(), ts.BytesWrittenData.Int64()
	st.Uploaded = wrote
	if now := time.Now(); now.Sub(it.at) >= 500*time.Millisecond {
		secs := now.Sub(it.at).Seconds()
		it.down, it.up = math.Max(0, float64(read-it.read)/secs), math.Max(0, float64(wrote-it.wrote)/secs)
		it.at, it.read, it.wrote = now, read, wrote
	}
	st.DownSpeed, st.UpSpeed = it.down, it.up
	st.PeersLive, st.PeersSeen = ts.ActivePeers, ts.TotalPeers
	st.Phase = Downloading
	if st.Done >= st.Total {
		st.Phase = Finished
	}
	return st
}

// Peers are a torrent's peers now.
func (s *Torrents) Peers(hash string) []Peer {
	s.mu.Lock()
	defer s.mu.Unlock()
	it, err := s.get(hash)
	if err != nil || it.t == nil {
		return nil
	}
	var out []Peer
	for _, pc := range it.t.PeerConns() {
		p := Peer{Addr: pc.RemoteAddr.String(), Kind: pc.Network}
		if name, ok := pc.PeerClientName.Load().(string); ok {
			p.Client = name
		}
		st := pc.Stats()
		p.Fetched, p.Uploaded = st.BytesReadData.Int64(), st.BytesWrittenData.Int64()
		out = append(out, p)
	}
	return out
}

// Pause stops a torrent (what is there stays, checked).
func (s *Torrents) Pause(hash string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	it, err := s.get(hash)
	if err != nil || it.t == nil {
		return err
	}
	it.last, it.hasLast = it.stats(), true
	it.t.Drop()
	it.t = nil
	return nil
}

// Resume starts a paused torrent again.
func (s *Torrents) Resume(hash string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	it, err := s.get(hash)
	if err != nil || it.t != nil {
		return err
	}
	return s.startLocked(it)
}

// Remove takes a torrent out of the session, and its files with it when
// deleteFiles. Its checked pieces are forgotten: added again, it is checked
// again.
func (s *Torrents) Remove(hash string, deleteFiles bool) error {
	s.mu.Lock()
	it, err := s.get(hash)
	if err != nil {
		s.mu.Unlock()
		return err
	}
	if it.t != nil {
		it.t.Drop()
		it.t = nil
	}
	delete(s.items, it.meta.InfoHash)
	s.mu.Unlock()
	h := metainfo.NewHashFromHex(it.meta.InfoHash)
	for i := range it.meta.pieces {
		_ = s.pc.Set(metainfo.PieceKey{InfoHash: h, Index: i}, false)
	}
	if !deleteFiles {
		return nil
	}
	return RemoveFiles(it.meta, it.dir)
}

// RemoveFiles deletes a torrent's files saved in dir (its own, by their
// names: nothing else), then its own folders left empty.
func RemoveFiles(m Meta, dir string) error {
	var firstErr error
	for _, p := range m.paths(dir) {
		if err := os.Remove(p); err != nil && !errors.Is(err, os.ErrNotExist) && firstErr == nil {
			firstErr = err
		}
	}
	if m.multi {
		// The torrent's own folders, deepest first, when they are empty.
		var dirs []string
		_ = filepath.WalkDir(m.FolderIn(dir), func(p string, d os.DirEntry, err error) error {
			if err == nil && d.IsDir() {
				dirs = append(dirs, p)
			}
			return nil
		})
		slices.Reverse(dirs)
		for _, d := range dirs {
			_ = os.Remove(d)
		}
	}
	return firstErr
}

func limit(bps int64) (rate.Limit, int) {
	if bps <= 0 {
		return rate.Inf, 1 << 20
	}
	bps = max(bps, MinRate)
	return rate.Limit(bps), int(min(bps, math.MaxInt32))
}

// SetDownloadLimit caps the session's download in bytes/s (0: none; at
// least MinRate).
func (s *Torrents) SetDownloadLimit(bps int64) {
	l, b := limit(bps)
	s.down.SetLimit(l)
	s.down.SetBurst(b)
}

// SetUploadLimit caps the session's upload in bytes/s (0: none; at least
// MinRate).
func (s *Torrents) SetUploadLimit(bps int64) {
	l, b := limit(bps)
	s.up.SetLimit(l)
	s.up.SetBurst(b)
}

// ListenPort is the port the session listens on: always 0 through a proxy
// (nothing listens); offline, the tests' loopback port.
func (s *Torrents) ListenPort() int {
	for _, a := range s.cl.ListenAddrs() {
		if t, ok := a.(*net.TCPAddr); ok {
			return t.Port
		}
	}
	return 0
}

// addPeer tells a torrent of a peer (the tests' loopback swarm).
func (s *Torrents) addPeer(hash, addr string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if it, err := s.get(hash); err == nil && it.t != nil {
		if tcp, err := net.ResolveTCPAddr("tcp", addr); err == nil {
			it.t.AddPeers([]bt.PeerInfo{{Addr: tcp, Trusted: true}})
		}
	}
}

// Stop ends the session.
func (s *Torrents) Stop() {
	s.cl.Close()
	_ = s.pc.Close()
}
