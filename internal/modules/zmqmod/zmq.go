//go:build !nozmq

package zmqmod

import (
	"context"
	"fmt"
	"io"
	"log"
	"net"
	"reflect"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/go-zeromq/zmq4"

	"github.com/maarten-boot/kukel/internal/modules/eventsmod"
	"github.com/maarten-boot/kukel/internal/native"
	"github.com/maarten-boot/kukel/internal/value"
)

// NetError is the kind of the exceptions for network problems: an address
// that cannot be bound or reached, a message that cannot be sent or
// received.
const NetError = "NetError"

var socketTypes = map[string]func(context.Context, ...zmq4.Option) zmq4.Socket{
	"REQ": zmq4.NewReq, "REP": zmq4.NewRep,
	"PUB": zmq4.NewPub, "SUB": zmq4.NewSub,
	"PUSH": zmq4.NewPush, "PULL": zmq4.NewPull,
	"DEALER": zmq4.NewDealer, "ROUTER": zmq4.NewRouter,
	"PAIR": zmq4.NewPair,
}

// socket is the state of a zmq::Socket object. Only the interpreter's
// goroutine uses it; a Recv runs on a goroutine of its own and hands its
// result over through pending.
type socket struct {
	kind      string
	s         zmq4.Socket
	cancel    context.CancelFunc
	endpoint  string // the last endpoint bound to or connected to
	closed    bool
	pending   chan result // the result of a Recv still running, or nil
	got       *result     // a result poll took from pending, for the next receive
	callbacks bool        // onmessage took over the messages
	handler   *handler    // set by onmessage
	requested bool        // a REQ socket sent a request: it may receive
	lastSend  time.Time   // for the linger of close
}

// handler is what onmessage set up: the function, and the hold on the
// event loop that keeps events::run() going until the socket is closed.
type handler struct {
	a       native.Args
	env     value.Env
	f       value.Value
	loop    *eventsmod.Loop
	release func()
	started bool // its reads run (a REQ socket starts them with its first request)
}

// sendOnly are the socket types that cannot receive: reading one fails at
// once, so receive, onmessage and poll refuse them. receiveOnly cannot
// send: zmq4 fails at once for PULL, and never returns for SUB.
var (
	sendOnly    = map[string]bool{"PUSH": true, "PUB": true}
	receiveOnly = map[string]bool{"PULL": true, "SUB": true}
)

// quiet is the logger of the sockets: zmq4 would write its own lines on
// standard error (a connection that could not be made, ...).
var quiet = log.New(io.Discard, "", 0)

// linger is how long close waits after the last send: zmq4's Send only
// queues a message, and closing at once would drop it.
const linger = 100 * time.Millisecond

type result struct {
	msg zmq4.Msg
	err error
}

// Socket is the class zmq::Socket.
var Socket = &native.Class{Name: "Socket"}

func init() {
	Socket.New = newSocket
	Socket.Methods = map[string]value.NativeMethod{
		"bind":    method("bind", 1, 1, bind),
		"connect": method("connect", 1, 1, connect),
		"endpoint": method("endpoint", 0, 0, func(a native.Args, env value.Env, s *socket) (value.Value, error) {
			return value.MakeString(s.endpoint), nil
		}),
		"type": method("type", 0, 0, func(a native.Args, env value.Env, s *socket) (value.Value, error) {
			return value.MakeString(s.kind), nil
		}),
		"send":        method("send", 1, 2, send),
		"receive":     method("receive", 0, 1, receive),
		"onmessage":   method("onmessage", 1, 1, onmessage),
		"subscribe":   method("subscribe", 1, 1, subscription(zmq4.OptionSubscribe)),
		"unsubscribe": method("unsubscribe", 1, 1, subscription(zmq4.OptionUnsubscribe)),
		"close":       method("close", 0, 0, closeSocket),
		"closed": method("closed", 0, 0, func(a native.Args, env value.Env, s *socket) (value.Value, error) {
			return value.MakeBool(s.closed), nil
		}),
	}
	Socket.Show = func(o *value.ObjectObj) string {
		s := o.State.(*socket)
		switch {
		case s.closed:
			return "zmq::Socket(" + s.kind + ", closed)"
		case s.endpoint != "":
			return "zmq::Socket(" + s.kind + ", " + s.endpoint + ")"
		}
		return "zmq::Socket(" + s.kind + ")"
	}
	native.Register(&native.Module{
		Name:    "zmq",
		Doc:     "ZeroMQ sockets (REQ/REP, PUB/SUB, PUSH/PULL, DEALER/ROUTER, PAIR)",
		Funcs:   map[string]native.Func{"poll": poll},
		Classes: []*native.Class{Socket},
	})
}

// method makes a member function that checks its arguments and refuses a
// closed socket (except for close, closed, type and endpoint).
func method(name string, min, max int, fn func(a native.Args, env value.Env, s *socket) (value.Value, error)) value.NativeMethod {
	full := "zmq::Socket::" + name
	always := name == "close" || name == "closed" || name == "type" || name == "endpoint"
	return func(env value.Env, this *value.ObjectObj, args []value.Value) (value.Value, error) {
		a, err := native.Check(full, args, min, max)
		if err != nil {
			return value.NilValue, err
		}
		s := this.State.(*socket)
		if s.closed && !always {
			return value.NilValue, a.Errorf(value.ValueError, "the socket is closed")
		}
		return fn(a, env, s)
	}
}

// new zmq::Socket(type [, identity]): type is one of socketTypes; the
// identity names the socket for a ROUTER it connects to (else the ROUTER
// makes one up).
func newSocket(env value.Env, this *value.ObjectObj, args []value.Value) (value.Value, error) {
	a, err := native.Check("zmq::Socket::Socket", args, 1, 2)
	if err != nil {
		return value.NilValue, err
	}
	kind, err := a.String(0)
	if err != nil {
		return value.NilValue, err
	}
	var opts []zmq4.Option
	if a.Has(1) {
		id, err := a.String(1)
		if err != nil {
			return value.NilValue, err
		}
		if id == "" || len(id) > 255 {
			return value.NilValue, a.Errorf(value.ValueError, "an identity is 1 to 255 bytes long")
		}
		opts = append(opts, zmq4.WithID(zmq4.SocketIdentity(id)))
	}
	newSock, ok := socketTypes[kind]
	if !ok {
		return value.NilValue, a.Errorf(value.ValueError, "unknown socket type %q (one of %s)", kind, strings.Join(typeNames(), ", "))
	}
	ctx, cancel := context.WithCancel(context.Background())
	// zmq4 gives up a send after its timeout (5 minutes by default) and
	// drops the message without an error: send has a timeout of its own.
	opts = append(opts, zmq4.WithLogger(quiet), zmq4.WithTimeout(100*365*24*time.Hour))
	this.State = &socket{kind: kind, s: newSock(ctx, opts...), cancel: cancel}
	return value.NilValue, nil
}

// typeNames lists the socket types, sorted.
func typeNames() []string {
	names := make([]string, 0, len(socketTypes))
	for n := range socketTypes {
		names = append(names, n)
	}
	sort.Strings(names)
	return names
}

func endpointArg(a native.Args) (string, error) {
	ep, err := a.String(0)
	if err != nil {
		return "", err
	}
	scheme, rest, found := strings.Cut(ep, "://")
	if !found {
		return "", a.Errorf(value.ValueError, "%q is not an endpoint such as \"tcp://localhost:5555\"", ep)
	}
	switch scheme {
	case "tcp":
		_, port, err := net.SplitHostPort(rest)
		if err != nil {
			return "", a.Errorf(value.ValueError, "%q is not an endpoint such as \"tcp://localhost:5555\" (a host and a port)", ep)
		}
		if n, err := strconv.Atoi(port); port != "*" && port != "" && (err != nil || n < 0 || n > 65535) {
			return "", a.Errorf(value.ValueError, "%q: the port must be a number from 0 to 65535", ep)
		}
	case "ipc":
		if len(rest) > native.MaxSocketPath {
			return "", a.Errorf(value.ValueError, "%q: the socket path is longer than %d bytes", ep, native.MaxSocketPath)
		}
	case "inproc":
	default:
		return "", a.Errorf(value.ValueError, "%q: the transports are tcp://, ipc:// and inproc://", ep)
	}
	return ep, nil
}

// s->bind(endpoint): listen on endpoint ("tcp://*:5555"; port 0 picks a
// free port, which endpoint() then shows).
func bind(a native.Args, env value.Env, s *socket) (value.Value, error) {
	ep, err := endpointArg(a)
	if err != nil {
		return value.NilValue, err
	}
	if err := s.s.Listen(ep); err != nil {
		return value.NilValue, a.Errorf(NetError, "%s: %s", ep, native.NetMessage(err))
	}
	s.endpoint = ep
	if addr := s.s.Addr(); addr != nil && strings.HasPrefix(ep, "tcp://") {
		s.endpoint = "tcp://" + addr.String()
	}
	return value.NilValue, nil
}

// s->connect(endpoint): connect to a socket bound at endpoint. zmq4 retries
// for a while; a program cancelled meanwhile closes the socket.
func connect(a native.Args, env value.Env, s *socket) (value.Value, error) {
	ep, err := endpointArg(a)
	if err != nil {
		return value.NilValue, err
	}
	native.Flush(env) // it may retry for a while
	done := make(chan error, 1)
	sock := s.s
	go func() { done <- sock.Dial(ep) }()
	select {
	case err = <-done:
	case <-env.Done():
		s.shut() // stops the retries
		return value.NilValue, env.Err()
	}
	if err != nil {
		return value.NilValue, a.Errorf(NetError, "%s: %s", ep, native.NetMessage(err))
	}
	s.endpoint = ep
	return value.NilValue, nil
}

// s->send(message [, timeout]): a string, or a vector of strings for a
// message of several parts. PUSH, DEALER, PAIR and ROUTER sockets wait
// for a peer to take the message, at most timeout seconds if given: then
// the message has not left, and the socket is closed, so that it never
// does (zmq4 cannot take back a message it waits to send).
func send(a native.Args, env value.Env, s *socket) (value.Value, error) {
	if receiveOnly[s.kind] {
		return value.NilValue, a.Errorf(value.ValueError, "a %s socket cannot send messages", s.kind)
	}
	var msg zmq4.Msg
	switch v := a.V[0]; v.Kind() {
	case value.String:
		msg = zmq4.NewMsgString(v.AsString())
	case value.Vector:
		elems := v.AsVector().Elems
		if len(elems) == 0 {
			return value.NilValue, a.Errorf(value.ValueError, "a message needs at least one part")
		}
		frames := make([][]byte, len(elems))
		for i, e := range elems {
			if e.Kind() != value.String {
				return value.NilValue, a.Errorf(value.TypeError, "part %d of the message must be a string, not %s", i+1, native.TypeName(e))
			}
			frames[i] = []byte(e.AsString())
		}
		msg = zmq4.NewMsgFrom(frames...)
	default:
		return value.NilValue, a.TypeError(0, "a string or a vector of strings")
	}
	timeout := time.Duration(-1)
	if a.Has(1) {
		var err error
		if timeout, err = a.Seconds(1, "a usable timeout"); err != nil {
			return value.NilValue, err
		}
	}
	done := make(chan error, 1)
	sock := s.s
	go func() {
		if len(msg.Frames) > 1 {
			done <- sock.SendMulti(msg)
		} else {
			done <- sock.Send(msg)
		}
	}()
	var err error
	select {
	case err = <-done: // it left at once
	default:
		native.Flush(env)
		var expired <-chan time.Time
		if timeout >= 0 {
			t := time.NewTimer(timeout)
			defer t.Stop()
			expired = t.C
		}
		select {
		case err = <-done:
		case <-expired:
			s.shut()
			return value.NilValue, a.Errorf(NetError, "no peer took the message within %s seconds: it was not sent, and the socket is closed",
				strconv.FormatFloat(timeout.Seconds(), 'g', -1, 64))
		case <-env.Done(): // the program was cancelled
			s.shut()
			return value.NilValue, env.Err()
		}
	}
	if err != nil {
		if strings.Contains(err.Error(), "no connections available") { // zmq4 adds "%!w(<nil>)"
			return value.NilValue, a.Errorf(NetError, "no peer is connected to send to")
		}
		return value.NilValue, a.Errorf(NetError, "%s", native.NetMessage(err))
	}
	s.lastSend = time.Now()
	if s.kind == "REQ" {
		s.requested = true
		if h := s.handler; h != nil && !h.started { // the reply can come now
			s.startHandler()
		}
	}
	return value.NilValue, nil
}

// message turns a received message into a string, or a vector of strings
// for several parts.
func message(m zmq4.Msg) value.Value {
	if len(m.Frames) == 1 {
		return value.MakeString(string(m.Frames[0]))
	}
	parts := make([]value.Value, len(m.Frames))
	for i, f := range m.Frames {
		parts[i] = value.MakeString(string(f))
	}
	return value.MakeVector(parts)
}

// startRecv makes sure a Recv is running and gives the channel its result
// arrives on. A Recv that a timeout left running is used by the next
// receive, so no message is lost.
func (s *socket) startRecv() chan result {
	if s.pending == nil {
		ch := make(chan result, 1)
		s.pending = ch
		sock := s.s
		go func() {
			m, err := sock.Recv()
			ch <- result{m, err}
		}()
	}
	return s.pending
}

// s->receive([timeout]): the next message: a string, or a vector of strings
// for several parts; nil after timeout seconds.
func receive(a native.Args, env value.Env, s *socket) (value.Value, error) {
	if sendOnly[s.kind] {
		return value.NilValue, a.Errorf(value.ValueError, "a %s socket cannot receive messages", s.kind)
	}
	if s.callbacks {
		return value.NilValue, a.Errorf(value.ValueError, "messages go to the onmessage handler, receive() cannot take them")
	}
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
	if r := s.got; r != nil { // poll already took it
		s.got = nil
		return received(a, *r)
	}
	select {
	case r := <-s.startRecv(): // there already
		s.pending = nil
		return received(a, r)
	default:
	}
	native.Flush(env)
	select {
	case r := <-s.startRecv():
		s.pending = nil
		return received(a, r)
	case <-expired:
		return value.NilValue, nil
	case <-env.Done(): // the program was cancelled
		return value.NilValue, env.Err()
	}
}

// received gives the message of a finished Recv, or its error.
func received(a native.Args, r result) (value.Value, error) {
	if r.err != nil {
		return value.NilValue, recvError(a, r.err)
	}
	return message(r.msg), nil
}

// recvError is the NetError for a Recv that failed.
func recvError(a native.Args, err error) error {
	if strings.Contains(err.Error(), "no connections available") {
		return a.Errorf(NetError, "no peer is connected (a REQ socket receives only after it sent a request)")
	}
	return a.Errorf(NetError, "the connection is lost: %s", native.NetMessage(err))
}

// s->onmessage(f): the event loop calls f(message) for every message, and
// events::run() keeps going until the socket is closed. A REQ socket
// starts reading when it sends its first request (zmq4 fails a read
// before that).
func onmessage(a native.Args, env value.Env, s *socket) (value.Value, error) {
	f := a.V[0]
	switch f.Kind() {
	case value.Function, value.Closure, value.Builtin, value.BoundMethod:
	default:
		return value.NilValue, a.TypeError(0, "a function")
	}
	if sendOnly[s.kind] {
		return value.NilValue, a.Errorf(value.ValueError, "a %s socket cannot receive messages", s.kind)
	}
	if s.callbacks {
		return value.NilValue, a.Errorf(value.ValueError, "an onmessage handler is already set")
	}
	s.callbacks = true
	loop := eventsmod.For(env)
	s.handler = &handler{a: a, env: env, f: f, loop: loop, release: loop.Hold()}
	if s.kind != "REQ" || s.requested || s.got != nil || s.pending != nil {
		s.startHandler()
	}
	return value.NilValue, nil
}

// startHandler starts the goroutine that reads the messages for the
// handler and posts them to the event loop. A read that fails, other than
// because the socket was closed, is a NetError of the loop (events::run()
// throws it, or the onerror handler gets it), and ends the reads.
func (s *socket) startHandler() {
	h := s.handler
	h.started = true
	var first chan result
	if s.got != nil { // poll took a result: it is the first message
		first = make(chan result, 1)
		first <- *s.got
		s.got = nil
	} else {
		first = s.startRecv() // a Recv may already be running
	}
	s.pending = nil
	sock := s.s
	go func() {
		defer h.release()
		r := <-first
		for r.err == nil {
			msg := message(r.msg)
			h.loop.Post(func() error { // on the interpreter's goroutine
				if s.closed { // by a handler: the messages queued after that are dropped
					return nil
				}
				_, err := h.env.Call(h.f, []value.Value{msg})
				return err
			})
			m, err := sock.Recv()
			r = result{m, err}
		}
		err := r.err
		h.loop.Post(func() error {
			if s.closed { // Recv fails once the socket is closed
				return nil
			}
			return recvError(h.a, err)
		})
	}()
}

// subscription implements s->subscribe(prefix) and s->unsubscribe(prefix)
// of a SUB socket.
func subscription(option string) func(a native.Args, env value.Env, s *socket) (value.Value, error) {
	return func(a native.Args, env value.Env, s *socket) (value.Value, error) {
		if s.kind != "SUB" {
			return value.NilValue, a.Errorf(value.ValueError, "only a SUB socket subscribes, not a %s socket", s.kind)
		}
		prefix, err := a.String(0)
		if err != nil {
			return value.NilValue, err
		}
		if err := s.s.SetOption(option, prefix); err != nil {
			return value.NilValue, a.Errorf(NetError, "%s", native.NetMessage(err))
		}
		return value.NilValue, nil
	}
}

// s->close(): close the socket; closing it again has no effect. If the
// socket sent a message less than linger ago, close first waits until
// linger has passed, so that the message still leaves.
func closeSocket(a native.Args, env value.Env, s *socket) (value.Value, error) {
	if !s.closed {
		if wait := linger - time.Since(s.lastSend); wait > 0 {
			time.Sleep(wait)
		}
		s.shut()
	}
	return value.NilValue, nil
}

// shut closes the socket at once, and lets events::run() end if only an
// onmessage handler of the socket kept it going.
func (s *socket) shut() {
	if s.closed {
		return
	}
	s.closed = true
	s.s.Close()
	s.cancel()
	if s.handler != nil {
		s.handler.release()
	}
}

// zmq::poll(sockets [, timeout]): waits until one of the sockets (a vector)
// has a message, and gives that socket, whose receive() then gives the
// message at once; nil after timeout seconds. The first socket in the
// vector wins when several have one. No message is lost: reads that are
// still running stay with their sockets.
func poll(env value.Env, args []value.Value) (value.Value, error) {
	a, err := native.Check("zmq::poll", args, 1, 2)
	if err != nil {
		return value.NilValue, err
	}
	if args[0].Kind() != value.Vector {
		return value.NilValue, a.TypeError(0, "a vector of sockets")
	}
	if len(args[0].AsVector().Elems) == 0 {
		return value.NilValue, a.Errorf(value.ValueError, "no sockets to wait for")
	}
	elems := args[0].AsVector().Elems
	socks := make([]*socket, len(elems))
	for i, v := range elems {
		switch {
		case !Socket.Derived(v):
			return value.NilValue, a.Errorf(value.TypeError, "element %d of argument 1 must be a zmq::Socket, not %s", i+1, native.TypeName(v))
		case !Socket.Is(v):
			return value.NilValue, a.NotSetUp(fmt.Sprintf("element %d of argument 1", i+1), Socket, v)
		}
		s := v.AsObject().State.(*socket)
		switch {
		case s.closed:
			return value.NilValue, a.Errorf(value.ValueError, "socket %d is closed", i+1)
		case s.callbacks:
			return value.NilValue, a.Errorf(value.ValueError, "socket %d gives its messages to onmessage", i+1)
		case sendOnly[s.kind]:
			return value.NilValue, a.Errorf(value.ValueError, "socket %d is a %s socket, which cannot receive messages", i+1, s.kind)
		}
		socks[i] = s
	}
	timeout := time.Duration(-1)
	if a.Has(1) {
		if timeout, err = a.Seconds(1, "a usable timeout"); err != nil {
			return value.NilValue, err
		}
	}
	for i, s := range socks { // a message poll found earlier, not yet received
		if s.got != nil {
			return elems[i], nil
		}
	}
	cases := make([]reflect.SelectCase, 0, len(socks)+2)
	for _, s := range socks {
		cases = append(cases, reflect.SelectCase{Dir: reflect.SelectRecv, Chan: reflect.ValueOf(s.startRecv())})
	}
	cases = append(cases, reflect.SelectCase{Dir: reflect.SelectRecv, Chan: reflect.ValueOf(env.Done())})
	if timeout >= 0 {
		t := time.NewTimer(timeout)
		defer t.Stop()
		cases = append(cases, reflect.SelectCase{Dir: reflect.SelectRecv, Chan: reflect.ValueOf(t.C)})
	}
	native.Flush(env)
	chosen, recv, _ := reflect.Select(cases)
	switch {
	case chosen < len(socks):
		r := recv.Interface().(result)
		socks[chosen].got, socks[chosen].pending = &r, nil
		// Select picks at random among sockets that are ready together:
		// an earlier one that has a message too wins.
		for j := 0; j < chosen; j++ {
			select {
			case r := <-socks[j].pending:
				socks[j].got, socks[j].pending = &r, nil
				return elems[j], nil
			default:
			}
		}
		return elems[chosen], nil
	case chosen == len(socks): // the program was cancelled
		return value.NilValue, env.Err()
	}
	return value.NilValue, nil // the time is up
}
