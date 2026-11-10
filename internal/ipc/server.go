// Ported from Mister Download Manager, crates/ipc/src/lib.rs
// Copyright (C) 2026 Ahmed Nefzaoui and contributors; Go port (C) 2026 the Yellow Flash contributors.
// Translated from Rust to Go and modified. SPDX-License-Identifier: GPL-3.0-or-later

package ipc

import (
	"bytes"
	"crypto/rand"
	"crypto/subtle"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"net"
	"os"
	"strings"
	"sync"
	"time"
	"unicode/utf8"
)

const (
	// MaxMessage is the largest message the browser's native host takes
	// from the extension (a download with its cookies and a form's body).
	MaxMessage = 1 << 20
	// maxRequest is the largest request line accepted: one MaxMessage,
	// JSON-escaped, fits.
	maxRequest = 2*MaxMessage + 4096
	// requestDeadline is the time a client gets to send its line, so a
	// client trickling bytes cannot hold a connection slot.
	requestDeadline = 3 * time.Second
	// maxInFlight connections are handled at once; more are closed
	// unanswered.
	maxInFlight = 8
	// replyTimeout is how long a query waits for the app's answer.
	replyTimeout = 2 * time.Second
	writeTimeout = 2 * time.Second
)

var (
	errTooLarge = errors.New("request line too large")
	errUnusable = errors.New("request unusable")
)

// Answers that are not JSON.
const (
	answerOK       = "ok"
	answerDenied   = "denied"
	answerGone     = "gone"
	answerTooLarge = "too-large"
	answerTimeout  = "timeout"
)

// Server is the running app's end of the channel.
type Server struct {
	ln      net.Listener
	token   string
	handle  Handler
	dataDir string // where ipc.json is; removed on Close if still ours
	slots   chan struct{}
	wg      sync.WaitGroup
	once    sync.Once
}

// Serve listens on a random 127.0.0.1 port, writes ipc.json to dataDir and
// hands each authenticated command to h. Close stops it.
func Serve(dataDir string, h Handler) (*Server, error) {
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		return nil, err
	}
	token, err := randomToken()
	if err != nil {
		_ = ln.Close()
		return nil, err
	}
	s := newServer(ln, token, h)
	s.dataDir = dataDir
	ep := endpoint{Port: ln.Addr().(*net.TCPAddr).Port, Token: token, PID: os.Getpid()}
	data, err := json.Marshal(ep)
	if err == nil {
		if err = PrivateDir(dataDir); err == nil {
			err = PrivateWrite(endpointPath(dataDir), data)
		}
	}
	if err != nil {
		_ = ln.Close()
		return nil, err
	}
	s.start()
	return s, nil
}

func newServer(ln net.Listener, token string, h Handler) *Server {
	return &Server{ln: ln, token: token, handle: h, slots: make(chan struct{}, maxInFlight)}
}

func (s *Server) start() {
	s.wg.Add(1)
	go s.acceptLoop()
}

// Addr is the listening address.
func (s *Server) Addr() net.Addr { return s.ln.Addr() }

// Close stops listening, waits for connections in progress and removes
// ipc.json if it still names this server.
func (s *Server) Close() error {
	var err error
	s.once.Do(func() {
		err = s.ln.Close()
		s.wg.Wait()
		if s.dataDir != "" {
			if ep, readErr := readEndpoint(s.dataDir); readErr == nil && ep.Token == s.token {
				_ = os.Remove(endpointPath(s.dataDir))
			}
		}
	})
	return err
}

// acceptLoop runs each loopback connection on its own goroutine, so a slow
// or hostile client cannot hold up a real command, at most maxInFlight at a
// time.
func (s *Server) acceptLoop() {
	defer s.wg.Done()
	for {
		conn, err := s.ln.Accept()
		if err != nil {
			if errors.Is(err, net.ErrClosed) {
				return
			}
			time.Sleep(10 * time.Millisecond) // transient, e.g. too many open files
			continue
		}
		if !isLoopback(conn.RemoteAddr()) {
			_ = conn.Close()
			continue
		}
		select {
		case s.slots <- struct{}{}:
		default:
			_ = conn.Close() // over the limit: closed unanswered
			continue
		}
		s.wg.Add(1)
		go func() {
			defer s.wg.Done()
			defer func() { <-s.slots }()
			defer conn.Close()
			s.serveConn(conn)
		}()
	}
}

func isLoopback(a net.Addr) bool {
	tcp, ok := a.(*net.TCPAddr)
	return ok && tcp.IP.IsLoopback()
}

func (s *Server) serveConn(conn net.Conn) {
	line, err := readRequest(conn)
	if errors.Is(err, errTooLarge) {
		// Said so, so the browser's host does not take silence for an app
		// that is not there.
		writeLine(conn, answerTooLarge)
		lingerClose(conn)
		return
	}
	if err != nil {
		return
	}
	// A web page reaching the port (fetch, form post): no answer at all.
	if looksLikeHTTP(line) {
		return
	}
	var env envelope
	if json.Unmarshal([]byte(line), &env) != nil || !sameSecret(env.Token, s.token) {
		writeLine(conn, answerDenied)
		return
	}
	if !env.WantsReply() {
		if s.handle(Incoming{Cmd: env.Command}) {
			writeLine(conn, answerOK)
		} else {
			writeLine(conn, answerGone) // the app is quitting: the command would be lost
		}
		return
	}
	reply := make(chan string, 1)
	if !s.handle(Incoming{Cmd: env.Command, Reply: reply}) {
		writeLine(conn, answerGone)
		return
	}
	timer := time.NewTimer(replyTimeout)
	defer timer.Stop()
	select {
	case answer := <-reply:
		writeLine(conn, answer)
	case <-timer.C:
		writeLine(conn, answerTimeout) // a stuck app means no answer
	}
}

func writeLine(conn net.Conn, s string) {
	_ = conn.SetWriteDeadline(time.Now().Add(writeTimeout))
	_, _ = io.WriteString(conn, s+"\n")
}

// lingerClose closes after an answer given before the request was read to
// its end: no more writing, then what the client still sends is read and
// dropped (up to a limit, for a second at most). Closing with data unread
// makes the OS reset the connection, and a reset can lose the answer before
// the client reads it.
func lingerClose(conn net.Conn) {
	if tcp, ok := conn.(*net.TCPConn); ok {
		_ = tcp.CloseWrite()
	}
	_ = conn.SetReadDeadline(time.Now().Add(time.Second))
	_, _ = io.CopyN(io.Discard, conn, 4*maxRequest)
}

// looksLikeHTTP reports an HTTP request line ("POST / HTTP/1.1") instead of
// our JSON. A JSON object never is one, even if its last word is "HTTP/…".
func looksLikeHTTP(line string) bool {
	line = strings.TrimRight(line, "\r")
	if strings.HasPrefix(strings.TrimLeft(line, " \t"), "{") {
		return false
	}
	last := line[strings.LastIndexByte(line, ' ')+1:]
	return strings.HasPrefix(last, "HTTP/")
}

// sameSecret compares tokens in time independent of where they differ.
func sameSecret(a, b string) bool {
	return subtle.ConstantTimeCompare([]byte(a), []byte(b)) == 1
}

// readRequest reads one request line with a size cap checked as bytes
// arrive and an overall deadline. errUnusable: closed early, too slow, or
// not UTF-8.
func readRequest(conn net.Conn) (string, error) {
	if err := conn.SetReadDeadline(time.Now().Add(requestDeadline)); err != nil {
		return "", errUnusable
	}
	var buf []byte
	// Large reads: a request up to the limit (2 MB) arrives well within the
	// deadline even on a busy machine.
	chunk := make([]byte, 64<<10)
	for {
		if len(buf) > maxRequest {
			return "", errTooLarge
		}
		n, err := conn.Read(chunk)
		if n > 0 {
			if i := bytes.IndexByte(chunk[:n], '\n'); i >= 0 {
				buf = append(buf, chunk[:i]...)
				break
			}
			buf = append(buf, chunk[:n]...)
		}
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			return "", errUnusable
		}
	}
	if len(buf) > maxRequest {
		return "", errTooLarge
	}
	if !utf8.Valid(buf) {
		return "", errUnusable
	}
	return string(buf), nil
}

// randomToken returns 256 random bits from the OS as 64 hex characters.
func randomToken() (string, error) {
	b := make([]byte, 32)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	return hex.EncodeToString(b), nil
}
