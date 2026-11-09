// Package ftptest is a small FTP server for tests: passive data connections
// (EPSV and PASV), REST, SIZE, MDTM, FEAT, logins, explicit and implicit
// TLS, and a connection limit answered with 421. It serves files from
// memory.
package ftptest

import (
	"bufio"
	"crypto/tls"
	"fmt"
	"net"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

// Server is a running fake FTP server.
type Server struct {
	// Addr is host:port of the control connection.
	Addr string
	// Files maps paths (as RETR names them) to contents.
	Files    map[string][]byte
	Modified time.Time

	// Knobs, set before the first connection.
	NoRest, NoEPSV, NoSize bool
	User, Pass             string // empty: anonymous only
	MaxConns               int64  // > 0: more connections get 421
	TLS                    *tls.Config
	Implicit               bool // TLS from the first byte (ftps://)
	Chunk                  int  // bytes per write (default 16 KiB)
	Delay                  time.Duration

	// Counters, read by tests.
	Logins, Retrs, Rests, Refused, Aborted atomic.Int64

	ln     net.Listener
	active atomic.Int64
	wg     sync.WaitGroup
	connMu sync.Mutex
	conns  map[net.Conn]struct{}
}

// New starts a server for files; configure runs before it accepts.
func New(t testing.TB, files map[string][]byte, configure func(*Server)) *Server {
	t.Helper()
	s := &Server{Files: files, Modified: time.Date(2026, 10, 1, 12, 30, 45, 0, time.UTC), Chunk: 16 << 10}
	if configure != nil {
		configure(s)
	}
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	if s.Implicit {
		ln = tls.NewListener(ln, s.TLS)
	}
	s.ln, s.Addr = ln, ln.Addr().String()
	s.wg.Add(1)
	go s.accept()
	t.Cleanup(func() {
		_ = s.ln.Close()
		// Sessions a client keeps in its pool would otherwise hold the
		// test until their read deadline.
		s.connMu.Lock()
		for c := range s.conns {
			_ = c.Close()
		}
		s.connMu.Unlock()
		s.wg.Wait()
	})
	return s
}

func (s *Server) accept() {
	defer s.wg.Done()
	for {
		c, err := s.ln.Accept()
		if err != nil {
			return
		}
		s.connMu.Lock()
		if s.conns == nil {
			s.conns = map[net.Conn]struct{}{}
		}
		s.conns[c] = struct{}{}
		s.connMu.Unlock()
		s.wg.Add(1)
		go func() {
			defer s.wg.Done()
			defer func() {
				s.connMu.Lock()
				delete(s.conns, c)
				s.connMu.Unlock()
				_ = c.Close()
			}()
			if n := s.active.Add(1); s.MaxConns > 0 && n > s.MaxConns {
				s.active.Add(-1)
				s.Refused.Add(1)
				_, _ = fmt.Fprintf(c, "421 Too many connections\r\n")
				return
			}
			defer s.active.Add(-1)
			(&session{s: s, c: c}).run()
		}()
	}
}

type session struct {
	s       *Server
	c       net.Conn
	r       *bufio.Reader
	user    string
	authed  bool
	prot    bool
	rest    int64
	passive net.Listener
	active  string // PORT/EPRT: where to connect for the next transfer
}

// activeAddress reads PORT h1,h2,h3,h4,p1,p2 or EPRT |af|ip|port|.
func activeAddress(cmd, arg string) (string, bool) {
	if cmd == "PORT" {
		f := strings.Split(arg, ",")
		if len(f) != 6 {
			return "", false
		}
		n := make([]int, 6)
		for i, s := range f {
			v, err := strconv.Atoi(strings.TrimSpace(s))
			if err != nil || v < 0 || v > 255 {
				return "", false
			}
			n[i] = v
		}
		return net.JoinHostPort(fmt.Sprintf("%d.%d.%d.%d", n[0], n[1], n[2], n[3]), strconv.Itoa(n[4]*256+n[5])), true
	}
	if len(arg) < 2 {
		return "", false
	}
	f := strings.Split(arg[1:len(arg)-1], arg[:1])
	if len(f) != 3 {
		return "", false
	}
	return net.JoinHostPort(f[1], f[2]), true
}

func (ss *session) reply(format string, args ...any) {
	_, _ = fmt.Fprintf(ss.c, format+"\r\n", args...)
}

func (ss *session) run() {
	defer func() {
		if ss.passive != nil {
			_ = ss.passive.Close()
		}
	}()
	ss.r = bufio.NewReader(ss.c)
	ss.reply("220 ftptest ready")
	for {
		_ = ss.c.SetReadDeadline(time.Now().Add(10 * time.Second))
		line, err := ss.r.ReadString('\n')
		if err != nil {
			return
		}
		line = strings.TrimRight(line, "\r\n")
		cmd, arg, _ := strings.Cut(line, " ")
		switch strings.ToUpper(cmd) {
		case "AUTH":
			if ss.s.TLS == nil || ss.s.Implicit {
				ss.reply("502 No TLS here")
				continue
			}
			ss.reply("234 Proceed")
			tc := tls.Server(ss.c, ss.s.TLS)
			if tc.Handshake() != nil {
				return
			}
			ss.c, ss.r = tc, bufio.NewReader(tc)
		case "PBSZ":
			ss.reply("200 PBSZ=0")
		case "PROT":
			ss.prot = strings.EqualFold(arg, "P")
			ss.reply("200 Protection set")
		case "USER":
			ss.user = arg
			ss.reply("331 Password please")
		case "PASS":
			if ss.s.User == "" || (ss.user == ss.s.User && arg == ss.s.Pass) {
				ss.authed = true
				ss.s.Logins.Add(1)
				ss.reply("230 Logged in")
			} else {
				ss.reply("530 Login incorrect")
			}
		case "TYPE":
			ss.reply("200 Type set")
		case "FEAT":
			feats := []string{"MDTM", "PASV"}
			if !ss.s.NoSize {
				feats = append(feats, "SIZE")
			}
			if !ss.s.NoEPSV {
				feats = append(feats, "EPSV")
			}
			if !ss.s.NoRest {
				feats = append(feats, "REST STREAM")
			}
			ss.reply("211-Features:\r\n %s\r\n211 End", strings.Join(feats, "\r\n "))
		case "NOOP":
			ss.reply("200 OK")
		case "QUIT":
			ss.reply("221 Bye")
			return
		default:
			if !ss.authed {
				ss.reply("530 Not logged in")
				continue
			}
			ss.file(strings.ToUpper(cmd), arg)
		}
	}
}

// file handles the commands that need a login.
func (ss *session) file(cmd, arg string) {
	data, found := ss.s.Files[arg]
	switch cmd {
	case "SIZE":
		switch {
		case ss.s.NoSize:
			ss.reply("502 SIZE not implemented")
		case !found:
			ss.reply("550 %s: No such file", arg)
		default:
			ss.reply("213 %d", len(data))
		}
	case "MDTM":
		if !found {
			ss.reply("550 No such file")
			return
		}
		ss.reply("213 %s", ss.s.Modified.UTC().Format("20060102150405"))
	case "EPSV", "PASV":
		if cmd == "EPSV" && ss.s.NoEPSV {
			ss.reply("502 EPSV not implemented")
			return
		}
		if ss.passive != nil {
			_ = ss.passive.Close()
		}
		ln, err := net.Listen("tcp", "127.0.0.1:0")
		if err != nil {
			ss.reply("425 Can't open")
			return
		}
		ss.passive = ln
		port := ln.Addr().(*net.TCPAddr).Port
		if cmd == "EPSV" {
			ss.reply("229 Entering Extended Passive Mode (|||%d|)", port)
		} else {
			// A NAT'd server's private address: the client should use the
			// control connection's.
			ss.reply("227 Entering Passive Mode (10,9,8,7,%d,%d)", port/256, port%256)
		}
	case "PORT", "EPRT":
		addr, ok := activeAddress(cmd, arg)
		if !ok {
			ss.reply("501 Bad address")
			return
		}
		ss.active = addr
		ss.reply("200 %s OK", cmd)
	case "REST":
		n, err := strconv.ParseInt(arg, 10, 64)
		if ss.s.NoRest || err != nil {
			ss.reply("502 REST not implemented")
			return
		}
		ss.rest = n
		ss.s.Rests.Add(1)
		ss.reply("350 Restarting at %d", n)
	case "RETR":
		from := ss.rest
		ss.rest = 0
		if !found {
			ss.reply("550 %s: No such file", arg)
			return
		}
		var dc net.Conn
		var err error
		switch {
		case ss.active != "":
			ss.reply("150 Opening BINARY mode data connection (active)")
			dc, err = net.DialTimeout("tcp", ss.active, 5*time.Second)
			ss.active = ""
		case ss.passive != nil:
			_ = ss.passive.(*net.TCPListener).SetDeadline(time.Now().Add(5 * time.Second))
			dc, err = ss.passive.Accept()
			_ = ss.passive.Close()
			ss.passive = nil
			if err == nil {
				ss.reply("150 Opening BINARY mode data connection")
			}
		default:
			ss.reply("425 Use PASV or PORT first")
			return
		}
		if err != nil {
			ss.reply("425 No data connection")
			return
		}
		ss.s.Retrs.Add(1)
		if ss.prot {
			tc := tls.Server(dc, ss.s.TLS)
			if tc.Handshake() != nil {
				_ = dc.Close()
				ss.reply("426 TLS failed")
				return
			}
			dc = tc
		}
		if from > int64(len(data)) {
			from = int64(len(data))
		}
		rest := data[from:]
		for len(rest) > 0 {
			n := min(ss.s.Chunk, len(rest))
			if ss.s.Delay > 0 {
				time.Sleep(ss.s.Delay)
			}
			_ = dc.SetWriteDeadline(time.Now().Add(5 * time.Second))
			if _, err := dc.Write(rest[:n]); err != nil {
				_ = dc.Close()
				ss.s.Aborted.Add(1)
				ss.reply("426 Connection closed; transfer aborted")
				return
			}
			rest = rest[n:]
		}
		_ = dc.Close()
		ss.reply("226 Transfer complete")
	default:
		ss.reply("502 %s not implemented", cmd)
	}
}
