// Package netmod is the native module "net": TCP connections (and
// connections over Unix sockets) and UDP packets (TODO.md Q103).
//
//	use net;
//	local c = net::connect("tcp://example.com:80", 5);
//	c->write("HEAD / HTTP/1.0\r\nHost: example.com\r\n\r\n");
//	println(c->readline());
//	c->close();
//
// Addresses are "tcp://host:port", "unix:///path" and "udp://host:port",
// as for zmq and p9. Data is strings (bytes). Calls that wait (connect,
// accept, read, write, receive) can be cancelled by the Go program that
// runs the interpreter; ondata, onconnect and onmessage hand what comes to
// the event loop (events) instead. What a program leaves open is closed
// when it ends (VM.End).
package netmod

import (
	"bytes"
	"context"
	"errors"
	"io"
	"net"
	"os"
	"strconv"
	"strings"
	"time"

	"github.com/maarten-boot/kukel/internal/modules/eventsmod"
	"github.com/maarten-boot/kukel/internal/native"
	"github.com/maarten-boot/kukel/internal/value"
)

// NetError is the kind of the exceptions for network problems.
const NetError = "NetError"

const (
	chunk   = 64 << 10 // what a read takes at most from the system, and read()'s default
	maxLine = 1 << 20  // the longest line readline gives (without its \n)
	maxUDP  = 64 << 10 // more than the largest UDP packet
)

// The classes of the module. None has a constructor: objects come from
// net::connect, net::listen and net::udp.
var (
	Conn     = &native.Class{Name: "Conn"}
	Listener = &native.Class{Name: "Listener"}
	UDP      = &native.Class{Name: "UDP"}
)

func init() {
	Conn.Methods = map[string]value.NativeMethod{
		"read":       connMethod("read", 0, 1, read),
		"readline":   connMethod("readline", 0, 0, readline),
		"readall":    connMethod("readall", 0, 0, readall),
		"iter":       connMethod("iter", 0, 0, lines),
		"write":      connMethod("write", 1, -1, write),
		"print":      connMethod("print", 0, -1, printer("")),
		"println":    connMethod("println", 0, -1, printer("\n")),
		"timeout":    connMethod("timeout", 1, 1, setTimeout),
		"closewrite": connMethod("closewrite", 0, 0, closeWrite),
		"ondata":     connMethod("ondata", 1, 1, ondata),
		"endpoint": connMethod("endpoint", 0, 0, func(a native.Args, env value.Env, c *conn) (value.Value, error) {
			return value.MakeString(c.local), nil
		}),
		"peer": connMethod("peer", 0, 0, func(a native.Args, env value.Env, c *conn) (value.Value, error) {
			return value.MakeString(c.remote), nil
		}),
		"close": connMethod("close", 0, 0, func(a native.Args, env value.Env, c *conn) (value.Value, error) {
			c.shut()
			return value.NilValue, nil
		}),
		"closed": connMethod("closed", 0, 0, func(a native.Args, env value.Env, c *conn) (value.Value, error) {
			return value.MakeBool(c.closed), nil
		}),
	}
	Conn.Show = func(o *value.ObjectObj) string {
		c := o.State.(*conn)
		name := c.remote
		if name == "" { // the server's side of a Unix socket
			name = c.local
		}
		if c.closed {
			return "net::Conn(" + name + ", closed)"
		}
		return "net::Conn(" + name + ")"
	}
	Listener.Methods = map[string]value.NativeMethod{
		"accept":    listenerMethod("accept", 0, 1, accept),
		"onconnect": listenerMethod("onconnect", 1, 1, onconnect),
		"endpoint": listenerMethod("endpoint", 0, 0, func(a native.Args, env value.Env, l *listener) (value.Value, error) {
			return value.MakeString(l.endpoint), nil
		}),
		"close": listenerMethod("close", 0, 0, func(a native.Args, env value.Env, l *listener) (value.Value, error) {
			l.shut()
			return value.NilValue, nil
		}),
		"closed": listenerMethod("closed", 0, 0, func(a native.Args, env value.Env, l *listener) (value.Value, error) {
			return value.MakeBool(l.closed), nil
		}),
	}
	Listener.Show = func(o *value.ObjectObj) string {
		l := o.State.(*listener)
		if l.closed {
			return "net::Listener(" + l.endpoint + ", closed)"
		}
		return "net::Listener(" + l.endpoint + ")"
	}
	UDP.Methods = map[string]value.NativeMethod{
		"sendto":    udpMethod("sendto", 2, 2, sendto),
		"receive":   udpMethod("receive", 0, 1, receive),
		"onmessage": udpMethod("onmessage", 1, 1, onmessage),
		"endpoint": udpMethod("endpoint", 0, 0, func(a native.Args, env value.Env, u *udp) (value.Value, error) {
			return value.MakeString(u.endpoint), nil
		}),
		"close": udpMethod("close", 0, 0, func(a native.Args, env value.Env, u *udp) (value.Value, error) {
			u.shut()
			return value.NilValue, nil
		}),
		"closed": udpMethod("closed", 0, 0, func(a native.Args, env value.Env, u *udp) (value.Value, error) {
			return value.MakeBool(u.closed), nil
		}),
	}
	UDP.Show = func(o *value.ObjectObj) string {
		u := o.State.(*udp)
		if u.closed {
			return "net::UDP(" + u.endpoint + ", closed)"
		}
		return "net::UDP(" + u.endpoint + ")"
	}
	native.Register(&native.Module{
		Name: "net",
		Doc:  "TCP connections, Unix sockets and UDP packets",
		Funcs: map[string]native.Func{
			"connect": connect,
			"listen":  listen,
			"udp":     newUDP,
			"lookup":  lookup,
		},
		Classes: []*native.Class{Conn, Listener, UDP},
	})
}

// --- what an interpreter has open ---

type openKey struct{}

// shutter is a connection, listener or UDP socket.
type shutter interface{ shut() }

// openThings are what an interpreter opened and did not close yet.
type openThings struct{ items map[shutter]struct{} }

func opened(env value.Env) *openThings {
	return env.Local(openKey{}, func() any { return &openThings{items: map[shutter]struct{}{}} }).(*openThings)
}

// End closes what the program left open, when it ends (VM.End).
func (o *openThings) End() error {
	for s := range o.items {
		s.shut()
	}
	return nil
}

// --- helpers ---

// isFunction reports whether v can be called as a handler.
func isFunction(v value.Value) bool {
	switch v.Kind() {
	case value.Function, value.Closure, value.Builtin, value.BoundMethod:
		return true
	}
	return false
}

func seconds(d time.Duration) string { return strconv.FormatFloat(d.Seconds(), 'g', -1, 64) }

// address reads an address of one of the schemes ("tcp", "unix", "udp"),
// for listening or for connecting, as a network and an address for Go.
func address(a native.Args, i int, listen bool, schemes ...string) (network, addr string, err error) {
	s, err := a.String(i)
	if err != nil {
		return "", "", err
	}
	scheme, rest, found := strings.Cut(s, "://")
	ok := false
	for _, sc := range schemes {
		ok = ok || found && sc == scheme
	}
	if !ok {
		examples := map[string]string{"tcp": `"tcp://host:port"`, "unix": `"unix:///path"`, "udp": `"udp://host:port"`}
		var ex []string
		for _, sc := range schemes {
			ex = append(ex, examples[sc])
		}
		return "", "", a.Errorf(value.ValueError, "%q is not an address such as %s", s, strings.Join(ex, " or "))
	}
	if scheme == "unix" {
		switch {
		case rest == "":
			return "", "", a.Errorf(value.ValueError, "%q has no socket path", s)
		case len(rest) > native.MaxSocketPath:
			return "", "", a.Errorf(value.ValueError, "%q: the socket path is longer than %d bytes", s, native.MaxSocketPath)
		}
		return "unix", rest, nil
	}
	host, port, err := net.SplitHostPort(rest)
	if err != nil || strings.ContainsAny(rest, "/?#") {
		return "", "", a.Errorf(value.ValueError, "%q is not an address such as \"%s://host:port\" (a host and a port)", s, scheme)
	}
	if n, err := strconv.Atoi(port); err != nil || n < 0 || n > 65535 {
		return "", "", a.Errorf(value.ValueError, "%q: the port must be a number from 0 to 65535", s)
	}
	if host == "*" && listen {
		host = ""
	}
	return scheme, net.JoinHostPort(host, port), nil
}

// endpoint writes a Go address the way the module takes it.
func endpoint(addr net.Addr) string {
	if addr == nil || addr.String() == "" {
		return ""
	}
	switch addr.Network() {
	case "unix":
		return "unix://" + addr.String()
	case "udp":
		return "udp://" + addr.String()
	}
	return "tcp://" + addr.String()
}

// contextOf gives a context that ends when the program is cancelled; stop
// must be called when the call is done.
func contextOf(env value.Env) (ctx context.Context, stop func()) {
	ctx, cancel := context.WithCancel(context.Background())
	if done := env.Done(); done != nil {
		end := make(chan struct{})
		go func() {
			select {
			case <-done:
				cancel()
			case <-end:
			}
		}()
		return ctx, func() { close(end); cancel() }
	}
	return ctx, cancel
}

// past is a deadline that has passed: it ends a read or write at once.
var past = time.Unix(1, 0)

// watch makes a read or write that is waiting end when the program is
// cancelled, by giving it a deadline that has passed (set); stop must be
// called when the read or write is done.
func watch(env value.Env, set func(time.Time) error) (stop func()) {
	done := env.Done()
	if done == nil {
		return func() {}
	}
	end := make(chan struct{})
	go func() {
		select {
		case <-done:
			set(past)
		case <-end:
		}
	}()
	return func() { close(end) }
}

// timedOut reports whether err is a deadline or timeout that passed.
func timedOut(err error) bool {
	var ne net.Error
	return errors.Is(err, os.ErrDeadlineExceeded) || errors.As(err, &ne) && ne.Timeout()
}

// --- net::connect, net::listen, net::lookup ---

// net::connect(address [, timeout]): a net::Conn.
func connect(env value.Env, args []value.Value) (value.Value, error) {
	a, err := native.Check("net::connect", args, 1, 2)
	if err != nil {
		return value.NilValue, err
	}
	network, addr, err := address(a, 0, false, "tcp", "unix")
	if err != nil {
		return value.NilValue, err
	}
	var d net.Dialer
	if a.Has(1) {
		if d.Timeout, err = a.Seconds(1, "a usable timeout"); err != nil {
			return value.NilValue, err
		}
	}
	ctx, stop := contextOf(env)
	defer stop()
	w := native.Wait(env)
	c, err := d.DialContext(ctx, network, addr)
	w.Done()
	if err != nil {
		if env.Err() != nil {
			return value.NilValue, env.Err()
		}
		if timedOut(err) {
			return value.NilValue, a.Errorf(NetError, "%s: no connection within %s seconds", a.V[0].AsString(), seconds(d.Timeout))
		}
		return value.NilValue, a.Errorf(NetError, "%s: %s", a.V[0].AsString(), native.NetMessage(err))
	}
	return newConn(env, c), nil
}

// net::listen(address): a net::Listener.
func listen(env value.Env, args []value.Value) (value.Value, error) {
	a, err := native.Check("net::listen", args, 1, 1)
	if err != nil {
		return value.NilValue, err
	}
	network, addr, err := address(a, 0, true, "tcp", "unix")
	if err != nil {
		return value.NilValue, err
	}
	ln, err := net.Listen(network, addr)
	if err != nil {
		return value.NilValue, a.Errorf(NetError, "%s: %s", a.V[0].AsString(), native.NetMessage(err))
	}
	l := &listener{l: ln, endpoint: endpoint(ln.Addr()), open: opened(env)}
	l.open.items[l] = struct{}{}
	return Listener.Object(l), nil
}

// net::lookup(host): the addresses of host, as strings.
func lookup(env value.Env, args []value.Value) (value.Value, error) {
	a, err := native.Check("net::lookup", args, 1, 1)
	if err != nil {
		return value.NilValue, err
	}
	host, err := a.String(0)
	if err != nil {
		return value.NilValue, err
	}
	if host == "" {
		return value.NilValue, a.Errorf(value.ValueError, "no host name")
	}
	ctx, stop := contextOf(env)
	defer stop()
	w := native.Wait(env)
	addrs, err := net.DefaultResolver.LookupHost(ctx, host)
	w.Done()
	if err != nil {
		if env.Err() != nil {
			return value.NilValue, env.Err()
		}
		return value.NilValue, a.Errorf(NetError, "%s", native.NetMessage(err))
	}
	vs := make([]value.Value, len(addrs))
	for i, s := range addrs {
		vs[i] = value.MakeString(s)
	}
	return value.MakeVector(vs), nil
}

// --- net::Conn ---

// conn is the Go part of a net::Conn. Only the interpreter's goroutine
// uses it, except the reads of an ondata handler.
type conn struct {
	c             net.Conn
	buf           []byte        // read from the system, not yet given to the program
	ended         bool          // the other side ended, or the connection broke
	timeout       time.Duration // for each call that waits; < 0: none
	local, remote string
	closed        bool
	handled       bool   // ondata takes the data
	release       func() // of the event loop's hold, for ondata
	open          *openThings
	self          value.Value
}

func newConn(env value.Env, c net.Conn) value.Value {
	cn := &conn{c: c, timeout: -1, local: endpoint(c.LocalAddr()), remote: endpoint(c.RemoteAddr()), open: opened(env)}
	cn.open.items[cn] = struct{}{}
	cn.self = Conn.Object(cn)
	return cn.self
}

// connMethod makes a member function of net::Conn that checks its
// arguments and that the connection is open (but for close, closed,
// local and remote).
func connMethod(name string, min, max int, fn func(a native.Args, env value.Env, c *conn) (value.Value, error)) value.NativeMethod {
	full := "net::Conn::" + name
	always := name == "close" || name == "closed" || name == "endpoint" || name == "peer"
	reads := name == "read" || name == "readline" || name == "readall" || name == "iter" || name == "ondata"
	return func(env value.Env, this *value.ObjectObj, args []value.Value) (value.Value, error) {
		a, err := native.Check(full, args, min, max)
		if err != nil {
			return value.NilValue, err
		}
		c := this.State.(*conn)
		switch {
		case c.closed && !always:
			return value.NilValue, a.Errorf(value.ValueError, "the connection is closed")
		case c.handled && reads:
			return value.NilValue, a.Errorf(value.ValueError, "the data goes to the ondata handler")
		}
		return fn(a, env, c)
	}
}

func (c *conn) shut() {
	if c.closed {
		return
	}
	c.closed = true
	delete(c.open.items, c)
	c.c.Close()
	if c.release != nil {
		c.release()
	}
}

// deadline is when a call that starts now must be done (zero: never).
func (c *conn) deadline() time.Time {
	if c.timeout < 0 {
		return time.Time{}
	}
	return time.Now().Add(c.timeout)
}

// fill reads what comes next into buf. It gives io.EOF at the end (also
// after a connection broke: that error came once), a NetError when the
// deadline passed (what came stays in buf), or the program's cancellation.
func (c *conn) fill(a native.Args, env value.Env, deadline time.Time) error {
	if c.ended {
		return io.EOF
	}
	c.c.SetReadDeadline(deadline)
	stop := watch(env, c.c.SetReadDeadline)
	w := native.Wait(env)
	if cap(c.buf)-len(c.buf) < chunk {
		c.buf = append(make([]byte, 0, len(c.buf)+chunk), c.buf...)
	}
	n, err := c.c.Read(c.buf[len(c.buf) : len(c.buf)+chunk])
	w.Done()
	stop()
	c.buf = c.buf[:len(c.buf)+n]
	switch {
	case err == nil:
		return nil
	case env.Err() != nil:
		return env.Err()
	case timedOut(err):
		return a.Errorf(NetError, "no data within %s seconds", seconds(c.timeout))
	case err == io.EOF:
		c.ended = true
		return io.EOF
	}
	c.ended = true
	return a.Errorf(NetError, "the connection is lost: %s", native.NetMessage(err))
}

// take gives the first n bytes of buf as a string and drops them.
func (c *conn) take(n int) value.Value {
	s := string(c.buf[:n])
	c.buf = c.buf[n:]
	if len(c.buf) == 0 {
		c.buf = nil
	}
	return value.MakeString(s)
}

// c->read([n]): what has come, at most n bytes (64 KiB by default),
// waiting only when nothing has; nil at the end.
func read(a native.Args, env value.Env, c *conn) (value.Value, error) {
	n := int64(chunk)
	if a.Has(0) {
		var err error
		if n, err = a.Int(0); err != nil {
			return value.NilValue, err
		}
		if n < 0 {
			return value.NilValue, a.Errorf(value.ValueError, "a negative number of bytes (%d)", n)
		}
		if n == 0 {
			return value.MakeString(""), nil
		}
	}
	if len(c.buf) == 0 {
		if err := c.fill(a, env, c.deadline()); err == io.EOF && len(c.buf) == 0 {
			return value.NilValue, nil
		} else if err != nil && err != io.EOF && len(c.buf) == 0 {
			return value.NilValue, err
		}
	}
	return c.take(int(min(n, int64(len(c.buf))))), nil
}

// c->readline(): the next line without its \n (or \r\n); nil at the end.
// A last line without \n is a line too. A line longer than maxLine is a
// NetError (it stays to be read with read).
func readline(a native.Args, env value.Env, c *conn) (value.Value, error) {
	deadline := c.deadline()
	searched := 0
	for {
		if i := bytes.IndexByte(c.buf[searched:], '\n'); i >= 0 {
			line := c.take(searched + i + 1).AsString()
			line = strings.TrimSuffix(line[:len(line)-1], "\r")
			return value.MakeString(line), nil
		}
		searched = len(c.buf)
		if searched > maxLine {
			return value.NilValue, a.Errorf(NetError, "a line longer than %d bytes", maxLine)
		}
		if err := c.fill(a, env, deadline); err == io.EOF {
			if len(c.buf) == 0 {
				return value.NilValue, nil
			}
			return c.take(len(c.buf)), nil
		} else if err != nil {
			return value.NilValue, err
		}
	}
}

// c->readall(): everything until the other side ends ("" if nothing).
func readall(a native.Args, env value.Env, c *conn) (value.Value, error) {
	deadline := c.deadline()
	for {
		if err := c.fill(a, env, deadline); err == io.EOF {
			break
		} else if err != nil {
			return value.NilValue, err
		}
	}
	if len(c.buf) == 0 {
		return value.MakeString(""), nil
	}
	return c.take(len(c.buf)), nil
}

// c->iter(): a generator of the lines, as readline reads them.
func lines(a native.Args, env value.Env, c *conn) (value.Value, error) {
	return native.Generator(env, "net::Conn::iter", func() (value.Value, bool, error) {
		switch {
		case c.closed:
			return value.NilValue, false, a.Errorf(value.ValueError, "the connection is closed")
		case c.handled:
			return value.NilValue, false, a.Errorf(value.ValueError, "the data goes to the ondata handler")
		}
		line, err := readline(a, env, c)
		return line, err == nil && !line.IsNil(), err
	})
}

// put sends s, within the timeout.
func (c *conn) put(a native.Args, env value.Env, s string) error {
	c.c.SetWriteDeadline(c.deadline())
	stop := watch(env, c.c.SetWriteDeadline)
	w := native.Wait(env)
	_, err := io.WriteString(c.c, s)
	w.Done()
	stop()
	switch {
	case err == nil:
		return nil
	case env.Err() != nil:
		return env.Err()
	case timedOut(err):
		return a.Errorf(NetError, "the data could not be sent within %s seconds (part of it may have been)", seconds(c.timeout))
	}
	return a.Errorf(NetError, "the connection is lost: %s", native.NetMessage(err))
}

// c->write(s, ...): sends the strings; gives c.
func write(a native.Args, env value.Env, c *conn) (value.Value, error) {
	var sb strings.Builder
	for i := range a.V {
		s, err := a.String(i)
		if err != nil {
			return value.NilValue, err
		}
		sb.WriteString(s)
	}
	if err := c.put(a, env, sb.String()); err != nil {
		return value.NilValue, err
	}
	return c.self, nil
}

// printer makes c->print and c->println: the values as print shows them.
func printer(end string) func(a native.Args, env value.Env, c *conn) (value.Value, error) {
	return func(a native.Args, env value.Env, c *conn) (value.Value, error) {
		var sb strings.Builder
		for i, v := range a.V {
			if i > 0 {
				sb.WriteByte(' ')
			}
			if v.Kind() == value.String {
				sb.WriteString(v.AsString())
			} else {
				sb.WriteString(v.String())
			}
		}
		sb.WriteString(end)
		return value.NilValue, c.put(a, env, sb.String())
	}
}

// c->timeout(seconds): the longest each later call may wait (nil: no
// limit); gives c.
func setTimeout(a native.Args, env value.Env, c *conn) (value.Value, error) {
	if a.V[0].IsNil() {
		c.timeout = -1
		return c.self, nil
	}
	d, err := a.Seconds(0, "a usable timeout")
	if err != nil {
		return value.NilValue, err
	}
	c.timeout = d
	return c.self, nil
}

// c->closewrite(): tells the other side that no more data comes; reading
// goes on.
func closeWrite(a native.Args, env value.Env, c *conn) (value.Value, error) {
	cw, ok := c.c.(interface{ CloseWrite() error })
	if !ok {
		return value.NilValue, a.Errorf(value.ValueError, "this connection cannot be closed for writing alone")
	}
	if err := cw.CloseWrite(); err != nil {
		return value.NilValue, a.Errorf(NetError, "%s", native.NetMessage(err))
	}
	return value.NilValue, nil
}

// c->ondata(f): the event loop calls f(data) for what comes, and f(nil)
// once at the end; events::run() keeps going until then or until the
// connection is closed. A connection that breaks is a NetError of the
// loop.
func ondata(a native.Args, env value.Env, c *conn) (value.Value, error) {
	f := a.V[0]
	if !isFunction(f) {
		return value.NilValue, a.TypeError(0, "a function")
	}
	c.handled = true
	loop := eventsmod.For(env)
	c.release = loop.Hold()
	first, ended, nc := c.buf, c.ended, c.c
	c.buf = nil
	nc.SetReadDeadline(time.Time{})
	call := func(v value.Value) {
		loop.Post(func() error {
			if c.closed { // by a handler: what came after is dropped
				return nil
			}
			_, err := env.Call(f, []value.Value{v})
			return err
		})
	}
	go func() {
		defer c.release()
		if len(first) > 0 {
			call(value.MakeString(string(first)))
		}
		var err error = io.EOF
		if !ended {
			buf := make([]byte, chunk)
			for {
				var n int
				n, err = nc.Read(buf)
				if n > 0 {
					call(value.MakeString(string(buf[:n])))
				}
				if err != nil {
					break
				}
			}
		}
		if err == io.EOF {
			call(value.NilValue)
			return
		}
		loop.Post(func() error {
			if c.closed { // the read failed because the program closed it
				return nil
			}
			return a.Errorf(NetError, "the connection is lost: %s", native.NetMessage(err))
		})
	}()
	return value.NilValue, nil
}

// --- net::Listener ---

type accepted struct {
	c   net.Conn
	err error
}

// listener is the Go part of a net::Listener. An Accept that a timeout
// left running is used by the next accept, so no connection is lost.
type listener struct {
	l        net.Listener
	endpoint string
	pending  chan accepted
	closed   bool
	handled  bool // onconnect takes the connections
	release  func()
	open     *openThings
}

func listenerMethod(name string, min, max int, fn func(a native.Args, env value.Env, l *listener) (value.Value, error)) value.NativeMethod {
	full := "net::Listener::" + name
	always := name == "close" || name == "closed" || name == "endpoint"
	return func(env value.Env, this *value.ObjectObj, args []value.Value) (value.Value, error) {
		a, err := native.Check(full, args, min, max)
		if err != nil {
			return value.NilValue, err
		}
		l := this.State.(*listener)
		switch {
		case l.closed && !always:
			return value.NilValue, a.Errorf(value.ValueError, "the listener is closed")
		case l.handled && !always:
			return value.NilValue, a.Errorf(value.ValueError, "the connections go to the onconnect handler")
		}
		return fn(a, env, l)
	}
}

func (l *listener) startAccept() chan accepted {
	if l.pending == nil {
		ch := make(chan accepted, 1)
		l.pending = ch
		ln := l.l
		go func() {
			c, err := ln.Accept()
			ch <- accepted{c, err}
		}()
	}
	return l.pending
}

func (l *listener) shut() {
	if l.closed {
		return
	}
	l.closed = true
	delete(l.open.items, l)
	l.l.Close()
	if p := l.pending; p != nil { // a connection that came but was not taken
		go func() {
			if r := <-p; r.c != nil {
				r.c.Close()
			}
		}()
	}
	if l.release != nil {
		l.release()
	}
}

// l->accept([timeout]): the next connection, a net::Conn; nil after
// timeout seconds.
func accept(a native.Args, env value.Env, l *listener) (value.Value, error) {
	var expired <-chan time.Time
	if a.Has(0) {
		d, err := a.Seconds(0, "a usable timeout")
		if err != nil {
			return value.NilValue, err
		}
		t := time.NewTimer(d)
		defer t.Stop()
		expired = t.C
	}
	w := native.Wait(env)
	defer w.Done()
	select {
	case r := <-l.startAccept():
		l.pending = nil
		if r.err != nil {
			return value.NilValue, a.Errorf(NetError, "%s", native.NetMessage(r.err))
		}
		return newConn(env, r.c), nil
	case <-expired:
		return value.NilValue, nil
	case <-env.Done():
		return value.NilValue, env.Err()
	}
}

// l->onconnect(f): the event loop calls f(conn) for each connection that
// comes; events::run() keeps going until the listener is closed.
func onconnect(a native.Args, env value.Env, l *listener) (value.Value, error) {
	f := a.V[0]
	if !isFunction(f) {
		return value.NilValue, a.TypeError(0, "a function")
	}
	l.handled = true
	loop := eventsmod.For(env)
	l.release = loop.Hold()
	first := l.startAccept()
	l.pending = nil
	ln := l.l
	go func() {
		defer l.release()
		r := <-first
		for r.err == nil {
			c := r.c
			loop.Post(func() error {
				if l.closed { // by a handler: the connections that came after are dropped
					c.Close()
					return nil
				}
				_, err := env.Call(f, []value.Value{newConn(env, c)})
				return err
			})
			nc, err := ln.Accept()
			r = accepted{nc, err}
		}
		err := r.err
		loop.Post(func() error {
			if l.closed {
				return nil
			}
			return a.Errorf(NetError, "%s", native.NetMessage(err))
		})
	}()
	return value.NilValue, nil
}

// --- net::UDP ---

type packet struct {
	data []byte
	from net.Addr
	err  error
}

// udp is the Go part of a net::UDP. A ReadFrom that a timeout left
// running is used by the next receive, so no packet is lost.
type udp struct {
	pc       net.PacketConn
	endpoint string
	pending  chan packet
	closed   bool
	handled  bool // onmessage takes the packets
	release  func()
	open     *openThings
}

// net::udp(address): a net::UDP bound to address ("udp://127.0.0.1:0").
func newUDP(env value.Env, args []value.Value) (value.Value, error) {
	a, err := native.Check("net::udp", args, 1, 1)
	if err != nil {
		return value.NilValue, err
	}
	_, addr, err := address(a, 0, true, "udp")
	if err != nil {
		return value.NilValue, err
	}
	pc, err := net.ListenPacket("udp", addr)
	if err != nil {
		return value.NilValue, a.Errorf(NetError, "%s: %s", a.V[0].AsString(), native.NetMessage(err))
	}
	u := &udp{pc: pc, endpoint: endpoint(pc.LocalAddr()), open: opened(env)}
	u.open.items[u] = struct{}{}
	return UDP.Object(u), nil
}

func udpMethod(name string, min, max int, fn func(a native.Args, env value.Env, u *udp) (value.Value, error)) value.NativeMethod {
	full := "net::UDP::" + name
	always := name == "close" || name == "closed" || name == "endpoint"
	reads := name == "receive" || name == "onmessage"
	return func(env value.Env, this *value.ObjectObj, args []value.Value) (value.Value, error) {
		a, err := native.Check(full, args, min, max)
		if err != nil {
			return value.NilValue, err
		}
		u := this.State.(*udp)
		switch {
		case u.closed && !always:
			return value.NilValue, a.Errorf(value.ValueError, "the socket is closed")
		case u.handled && reads:
			return value.NilValue, a.Errorf(value.ValueError, "the packets go to the onmessage handler")
		}
		return fn(a, env, u)
	}
}

func (u *udp) shut() {
	if u.closed {
		return
	}
	u.closed = true
	delete(u.open.items, u)
	u.pc.Close()
	if u.release != nil {
		u.release()
	}
}

func (u *udp) startRead() chan packet {
	if u.pending == nil {
		ch := make(chan packet, 1)
		u.pending = ch
		pc := u.pc
		go func() {
			buf := make([]byte, maxUDP)
			n, from, err := pc.ReadFrom(buf)
			ch <- packet{buf[:n], from, err}
		}()
	}
	return u.pending
}

// u->sendto(data, address): sends data as one packet.
func sendto(a native.Args, env value.Env, u *udp) (value.Value, error) {
	data, err := a.String(0)
	if err != nil {
		return value.NilValue, err
	}
	_, addr, err := address(a, 1, false, "udp")
	if err != nil {
		return value.NilValue, err
	}
	// The name is looked up on a goroutine of its own, so that a
	// cancelled program does not wait for it.
	type resolved struct {
		addr *net.UDPAddr
		err  error
	}
	ch := make(chan resolved, 1)
	go func() {
		ua, err := net.ResolveUDPAddr("udp", addr)
		ch <- resolved{ua, err}
	}()
	var r resolved
	w := native.Wait(env)
	select {
	case r = <-ch:
	case <-env.Done():
		w.Done()
		return value.NilValue, env.Err()
	}
	w.Done()
	if r.err != nil {
		return value.NilValue, a.Errorf(NetError, "%s: %s", a.V[1].AsString(), native.NetMessage(r.err))
	}
	if _, err := u.pc.WriteTo([]byte(data), r.addr); err != nil {
		return value.NilValue, a.Errorf(NetError, "%s: %s", a.V[1].AsString(), native.NetMessage(err))
	}
	return value.NilValue, nil
}

func (p packet) value() value.Value {
	return value.MakeVector([]value.Value{value.MakeString(string(p.data)), value.MakeString(endpoint(p.from))})
}

// u->receive([timeout]): the next packet as [data, from]; nil after
// timeout seconds.
func receive(a native.Args, env value.Env, u *udp) (value.Value, error) {
	var expired <-chan time.Time
	if a.Has(0) {
		d, err := a.Seconds(0, "a usable timeout")
		if err != nil {
			return value.NilValue, err
		}
		t := time.NewTimer(d)
		defer t.Stop()
		expired = t.C
	}
	w := native.Wait(env)
	defer w.Done()
	select {
	case p := <-u.startRead():
		u.pending = nil
		if p.err != nil {
			return value.NilValue, a.Errorf(NetError, "%s", native.NetMessage(p.err))
		}
		return p.value(), nil
	case <-expired:
		return value.NilValue, nil
	case <-env.Done():
		return value.NilValue, env.Err()
	}
}

// u->onmessage(f): the event loop calls f(data, from) for each packet;
// events::run() keeps going until the socket is closed.
func onmessage(a native.Args, env value.Env, u *udp) (value.Value, error) {
	f := a.V[0]
	if !isFunction(f) {
		return value.NilValue, a.TypeError(0, "a function")
	}
	u.handled = true
	loop := eventsmod.For(env)
	u.release = loop.Hold()
	first := u.startRead()
	u.pending = nil
	pc := u.pc
	go func() {
		defer u.release()
		p := <-first
		buf := make([]byte, maxUDP)
		for p.err == nil {
			data, from := string(p.data), endpoint(p.from)
			loop.Post(func() error {
				if u.closed {
					return nil
				}
				_, err := env.Call(f, []value.Value{value.MakeString(data), value.MakeString(from)})
				return err
			})
			n, addr, err := pc.ReadFrom(buf)
			p = packet{buf[:n], addr, err}
		}
		err := p.err
		loop.Post(func() error {
			if u.closed {
				return nil
			}
			return a.Errorf(NetError, "%s", native.NetMessage(err))
		})
	}()
	return value.NilValue, nil
}
