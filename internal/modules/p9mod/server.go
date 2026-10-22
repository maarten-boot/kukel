//go:build !no9p

package p9mod

import (
	"bufio"
	"encoding/binary"
	"errors"
	"fmt"
	"io"
	"net"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/hugelgupf/p9/p9"

	"github.com/maarten-boot/kukel/internal/native"
)

// address reads a 9P address: "tcp://host:port" (the port is 564 when
// left out; for a server, host * or nothing is every interface) or
// "unix:///path/of/socket". The error says what is wrong with u, after
// it ("%q %v").
func address(u string, listen bool) (network, addr string, err error) {
	if p, found := strings.CutPrefix(u, "unix://"); found && p != "" {
		if len(p) > native.MaxSocketPath {
			return "", "", fmt.Errorf("is a socket path longer than %d bytes", native.MaxSocketPath)
		}
		return "unix", p, nil
	}
	rest, found := strings.CutPrefix(u, "tcp://")
	if !found || rest == "" && !listen || strings.ContainsAny(rest, "/?#") {
		example := `"tcp://host:564" or "unix:///path"`
		if listen {
			example = `"tcp://*:564" or "unix:///path"`
		}
		return "", "", errors.New("is not a 9P address such as " + example)
	}
	host, port, err := net.SplitHostPort(rest)
	if err != nil { // no port
		host, port = rest, "564"
		if strings.HasPrefix(host, "[") && strings.HasSuffix(host, "]") {
			host = host[1 : len(host)-1]
		}
	}
	if n, err := strconv.Atoi(port); err != nil || n < 0 || n > 65535 {
		return "", "", errors.New("has no port number from 0 to 65535")
	}
	if listen && host == "*" {
		host = ""
	}
	return "tcp", net.JoinHostPort(host, port), nil
}

// server is the state of a p9::Server object.
type server struct {
	t        *tree
	ln       net.Listener
	endpoint string
	srvL     *p9.Server
	release  func() // of the hold on the event loop

	mu     sync.Mutex
	conns  map[net.Conn]bool
	closed bool
	active sync.WaitGroup // the connections being served
}

func newServer(t *tree, ln net.Listener, endpoint string) *server {
	s := &server{t: t, ln: ln, endpoint: endpoint, srvL: p9.NewServer(attacher{t}), conns: map[net.Conn]bool{}}
	s.release = t.local.loop.Hold()
	go s.accept()
	return s
}

func (s *server) accept() {
	for {
		c, err := s.ln.Accept()
		if err != nil {
			return // closed
		}
		s.mu.Lock()
		if s.closed {
			s.mu.Unlock()
			c.Close()
			return
		}
		s.conns[c] = true
		s.active.Add(1)
		s.mu.Unlock()
		go s.serveConn(c)
	}
}

// serveConn reads the version the client asks for in its first message,
// and has the server of that version answer the client.
func (s *server) serveConn(c net.Conn) {
	defer func() {
		c.Close()
		s.mu.Lock()
		delete(s.conns, c)
		s.mu.Unlock()
		s.active.Done()
	}()
	r := bufio.NewReaderSize(c, 8192)
	// Tversion: size[4] type[1] tag[2] msize[4] version[s]
	head, err := r.Peek(13)
	if err != nil || head[4] != 100 {
		return
	}
	n := int(binary.LittleEndian.Uint16(head[11:13]))
	version := ""
	if b, err := r.Peek(13 + n); err == nil {
		version = string(b[13:])
	}
	if strings.HasPrefix(version, "9P2000.L") {
		s.srvL.Handle(readCloser{r, c}, c)
		return
	}
	serve2000(s.t, r, c)
}

type readCloser struct {
	io.Reader
	io.Closer
}

// close stops the server: the listener is closed, and the connections
// after the requests they are answering (close may be called by a
// function of the tree, for such a request). The event loop keeps going
// until they are done, so that the answers still leave before the program
// ends.
func (s *server) close() {
	s.mu.Lock()
	if s.closed {
		s.mu.Unlock()
		return
	}
	s.closed = true
	conns := s.conns
	s.conns = map[net.Conn]bool{}
	s.mu.Unlock()
	s.ln.Close()
	for c := range conns {
		c.SetReadDeadline(time.Now()) // the next read fails; the answer is still written
	}
	go func() {
		s.active.Wait()
		s.release()
	}()
}
