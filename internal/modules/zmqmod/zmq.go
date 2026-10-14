// Package zmqmod is the native module "zmq": ZeroMQ sockets, to talk with
// other programs (in any language) and other machines. It uses the pure Go
// implementation github.com/go-zeromq/zmq4, so no C library is needed.
//
//	use zmq;
//	local s = new zmq::Socket("REQ");
//	s->connect("tcp://localhost:5555");
//	s->send("hello");
//	println(s->receive(5));          // the reply, or nil after 5 seconds
//	s->close();
//
// Messages are strings (bytes); a message of several parts is a vector of
// strings. Sockets of type REQ/REP, PUB/SUB and PUSH/PULL are supported.
package zmqmod

import (
	"context"
	"sort"
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
	callbacks bool        // onmessage took over the messages
	lastSend  time.Time   // for the linger of close
}

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
		"send":        method("send", 1, 1, send),
		"receive":     method("receive", 0, 1, receive),
		"onmessage":   method("onmessage", 1, 1, onmessage),
		"subscribe":   method("subscribe", 1, 1, subscription(zmq4.OptionSubscribe)),
		"unsubscribe": method("unsubscribe", 1, 1, subscription(zmq4.OptionUnsubscribe)),
		"close":       method("close", 0, 0, closeSocket),
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
		Doc:     "ZeroMQ sockets (REQ/REP, PUB/SUB, PUSH/PULL)",
		Classes: []*native.Class{Socket},
	})
}

// method makes a member function that checks its arguments and refuses a
// closed socket (except for close, type and endpoint).
func method(name string, min, max int, fn func(a native.Args, env value.Env, s *socket) (value.Value, error)) value.NativeMethod {
	full := "zmq::Socket::" + name
	always := name == "close" || name == "type" || name == "endpoint"
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

// new zmq::Socket(type): type is "REQ", "REP", "PUB", "SUB", "PUSH" or
// "PULL".
func newSocket(env value.Env, this *value.ObjectObj, args []value.Value) (value.Value, error) {
	a, err := native.Check("zmq::Socket::Socket", args, 1, 1)
	if err != nil {
		return value.NilValue, err
	}
	kind, err := a.String(0)
	if err != nil {
		return value.NilValue, err
	}
	newSock, ok := socketTypes[kind]
	if !ok {
		return value.NilValue, a.Errorf(value.ValueError, "unknown socket type %q (one of %s)", kind, strings.Join(typeNames(), ", "))
	}
	ctx, cancel := context.WithCancel(context.Background())
	this.State = &socket{kind: kind, s: newSock(ctx), cancel: cancel}
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
	if !strings.Contains(ep, "://") {
		return "", a.Errorf(value.ValueError, "%q is not an endpoint such as \"tcp://localhost:5555\"", ep)
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
		return value.NilValue, a.Errorf(NetError, "%v", err)
	}
	s.endpoint = ep
	if addr := s.s.Addr(); addr != nil && strings.HasPrefix(ep, "tcp://") {
		s.endpoint = "tcp://" + addr.String()
	}
	return value.NilValue, nil
}

// s->connect(endpoint): connect to a socket bound at endpoint.
func connect(a native.Args, env value.Env, s *socket) (value.Value, error) {
	ep, err := endpointArg(a)
	if err != nil {
		return value.NilValue, err
	}
	if err := s.s.Dial(ep); err != nil {
		return value.NilValue, a.Errorf(NetError, "%v", err)
	}
	s.endpoint = ep
	return value.NilValue, nil
}

// s->send(message): a string, or a vector of strings for a message of
// several parts.
func send(a native.Args, env value.Env, s *socket) (value.Value, error) {
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
				return value.NilValue, a.Errorf(value.TypeError, "part %d of the message must be a string, not %s", i+1, e.Kind())
			}
			frames[i] = []byte(e.AsString())
		}
		msg = zmq4.NewMsgFrom(frames...)
	default:
		return value.NilValue, a.TypeError(0, "a string or a vector of strings")
	}
	var err error
	if len(msg.Frames) > 1 {
		err = s.s.SendMulti(msg)
	} else {
		err = s.s.Send(msg)
	}
	if err != nil {
		return value.NilValue, a.Errorf(NetError, "%v", err)
	}
	s.lastSend = time.Now()
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
	if s.callbacks {
		return value.NilValue, a.Errorf(value.ValueError, "messages go to the onmessage handler, receive() cannot take them")
	}
	var expired <-chan time.Time
	if a.Has(0) {
		secs, err := a.Number(0)
		if err != nil {
			return value.NilValue, err
		}
		if secs != secs || secs < 0 {
			return value.NilValue, a.Errorf(value.ValueError, "%s seconds is not a usable timeout", value.FormatFloat(secs))
		}
		t := time.NewTimer(time.Duration(secs * float64(time.Second)))
		defer t.Stop()
		expired = t.C
	}
	select {
	case r := <-s.startRecv():
		s.pending = nil
		if r.err != nil {
			return value.NilValue, a.Errorf(NetError, "%v", r.err)
		}
		return message(r.msg), nil
	case <-expired:
		return value.NilValue, nil
	case <-env.Done(): // the program was cancelled
		return value.NilValue, env.Err()
	}
}

// s->onmessage(f): the event loop calls f(message) for every message, and
// events::run() keeps going until the socket is closed.
func onmessage(a native.Args, env value.Env, s *socket) (value.Value, error) {
	f := a.V[0]
	switch f.Kind() {
	case value.Function, value.Closure, value.Builtin, value.BoundMethod:
	default:
		return value.NilValue, a.TypeError(0, "a function")
	}
	if s.callbacks {
		return value.NilValue, a.Errorf(value.ValueError, "an onmessage handler is already set")
	}
	s.callbacks = true
	first := s.startRecv() // a Recv may already be running
	s.pending = nil
	loop := eventsmod.For(env)
	release := loop.Hold()
	sock := s.s
	go func() {
		defer release()
		r := <-first
		for r.err == nil {
			msg := message(r.msg)
			loop.Post(func() error {
				_, err := env.Call(f, []value.Value{msg})
				return err
			})
			m, err := sock.Recv()
			r = result{m, err}
		}
		// Recv fails once the socket is closed (or broken): the source ends.
	}()
	return value.NilValue, nil
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
			return value.NilValue, a.Errorf(NetError, "%v", err)
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
		s.closed = true
		s.s.Close()
		s.cancel()
	}
	return value.NilValue, nil
}
