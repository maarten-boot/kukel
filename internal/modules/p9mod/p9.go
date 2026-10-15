//go:build !no9p

package p9mod

import (
	"net"
	"os"
	"sort"
	"strconv"
	"time"

	"github.com/maarten-boot/kukel/internal/modules/timemod"
	"github.com/maarten-boot/kukel/internal/native"
	"github.com/maarten-boot/kukel/internal/value"
)

// NetError is the kind of the exceptions for network problems: a server
// that cannot be reached, an address that cannot be listened on.
const NetError = "NetError"

// Client is the class p9::Client: what p9::dial gives.
var Client = &native.Class{Name: "Client"}

// Tree is the class p9::Tree: the directories and files a server serves.
var Tree = &native.Class{Name: "Tree"}

// Server is the class p9::Server: what p9::serve gives.
var Server = &native.Class{Name: "Server"}

func init() {
	Client.Methods = map[string]value.NativeMethod{
		"version": clientMethod("version", 0, 0, func(a native.Args, c *client) (value.Value, error) {
			return value.MakeString(c.r.version()), nil
		}),
		"readdir": clientPathMethod("readdir", 1, func(a native.Args, c *client, p string) (value.Value, error) {
			var es []entry
			err := c.do(func() (err error) { es, err = c.r.readdir(p); return })
			if err != nil {
				return value.NilValue, c.fail(a, p, err)
			}
			sort.Slice(es, func(i, j int) bool { return es[i].name < es[j].name })
			out := make([]value.Value, len(es))
			for i, e := range es {
				out[i] = e.value()
			}
			return value.MakeVector(out), nil
		}),
		"stat": clientPathMethod("stat", 1, func(a native.Args, c *client, p string) (value.Value, error) {
			var e entry
			if err := c.do(func() (err error) { e, err = c.r.stat(p); return }); err != nil {
				return value.NilValue, c.fail(a, p, err)
			}
			return e.value(), nil
		}),
		"exists": clientPathMethod("exists", 1, func(a native.Args, c *client, p string) (value.Value, error) {
			err := c.do(func() (err error) { _, err = c.r.stat(p); return })
			if c.closed {
				return value.NilValue, c.fail(a, p, err)
			}
			return value.MakeBool(err == nil), nil
		}),
		"read": clientPathMethod("read", 1, func(a native.Args, c *client, p string) (value.Value, error) {
			var data []byte
			if err := c.do(func() (err error) { data, err = c.r.read(p); return }); err != nil {
				return value.NilValue, c.fail(a, p, err)
			}
			return value.MakeString(string(data)), nil
		}),
		"write": clientPathMethod("write", 2, func(a native.Args, c *client, p string) (value.Value, error) {
			data, err := a.String(1)
			if err != nil {
				return value.NilValue, err
			}
			if err := c.do(func() error { return c.r.write(p, []byte(data)) }); err != nil {
				return value.NilValue, c.fail(a, p, err)
			}
			return value.NilValue, nil
		}),
		"mkdir": clientPathMethod("mkdir", 1, func(a native.Args, c *client, p string) (value.Value, error) {
			if err := c.do(func() error { return c.r.mkdir(p) }); err != nil {
				return value.NilValue, c.fail(a, p, err)
			}
			return value.NilValue, nil
		}),
		"remove": clientPathMethod("remove", 1, func(a native.Args, c *client, p string) (value.Value, error) {
			if err := c.do(func() error { return c.r.remove(p) }); err != nil {
				return value.NilValue, c.fail(a, p, err)
			}
			return value.NilValue, nil
		}),
		"close": clientMethod("close", 0, 0, func(a native.Args, c *client) (value.Value, error) {
			if !c.closed {
				c.closed = true
				c.r.close()
			}
			return value.NilValue, nil
		}),
	}
	Client.Show = func(o *value.ObjectObj) string {
		c := o.State.(*client)
		if c.closed {
			return "p9::Client(" + c.url + ", closed)"
		}
		return "p9::Client(" + c.url + ", " + c.r.version() + ")"
	}

	Tree.New = newTree9
	Tree.Methods = map[string]value.NativeMethod{
		"file":   treeMethod("file", 2, 3, treeFile),
		"dir":    treeMethod("dir", 1, 1, treeDir),
		"remove": treeMethod("remove", 1, 1, treeRemove),
	}
	Tree.Show = func(o *value.ObjectObj) string {
		t := o.State.(*tree)
		t.mu.Lock()
		defer t.mu.Unlock()
		if t.files == 1 {
			return "p9::Tree(1 file)"
		}
		return "p9::Tree(" + strconv.Itoa(t.files) + " files)"
	}

	Server.Methods = map[string]value.NativeMethod{
		"endpoint": func(env value.Env, this *value.ObjectObj, args []value.Value) (value.Value, error) {
			if _, err := native.Check("p9::Server::endpoint", args, 0, 0); err != nil {
				return value.NilValue, err
			}
			return value.MakeString(this.State.(*server).endpoint), nil
		},
		"close": func(env value.Env, this *value.ObjectObj, args []value.Value) (value.Value, error) {
			if _, err := native.Check("p9::Server::close", args, 0, 0); err != nil {
				return value.NilValue, err
			}
			this.State.(*server).close()
			return value.NilValue, nil
		},
	}
	Server.Show = func(o *value.ObjectObj) string {
		s := o.State.(*server)
		s.mu.Lock()
		defer s.mu.Unlock()
		if s.closed {
			return "p9::Server(" + s.endpoint + ", closed)"
		}
		return "p9::Server(" + s.endpoint + ")"
	}

	native.Register(&native.Module{
		Name:    "p9",
		Doc:     "the 9P file protocol (9P2000 and 9P2000.L): a client, and a server of a tree the program builds",
		Funcs:   map[string]native.Func{"dial": dial9, "serve": serve9},
		Classes: []*native.Class{Client, Tree, Server},
	})
}

// value is the map of an entry, as fs::stat gives it: name, size, dir,
// link, mode and time.
func (e entry) value() value.Value {
	m := value.NewMap()
	set := func(k string, v value.Value) { m.Set(value.MakeString(k), v) } // string keys cannot fail
	set("name", value.MakeString(e.name))
	set("size", value.MakeInt(int64(e.size)))
	set("dir", value.MakeBool(e.mode.IsDir()))
	set("link", value.MakeBool(e.mode&os.ModeSymlink != 0))
	set("mode", value.MakeString(e.mode.String()))
	set("time", timemod.Time.Object(e.mtime))
	return value.MakeMap(m)
}

func callable(v value.Value) bool {
	switch v.Kind() {
	case value.Function, value.Closure, value.Builtin, value.BoundMethod:
		return true
	}
	return false
}

// ----------------------------------------------------------------------
// The client

// client is the state of a p9::Client object.
type client struct {
	r      remote
	url    string
	l      *local
	closed bool
}

// p9::dial(url [, options]): connects to the 9P server at url
// ("tcp://host:port", "unix:///path"). The options (a map): "version"
// ("9P2000.L" or "9P2000"; without it 9P2000.L is tried first), "user"
// (for 9P2000; the user running the program), "aname" (the tree to attach
// to, when the server has several) and "timeout" (seconds to connect,
// default 10).
func dial9(env value.Env, args []value.Value) (value.Value, error) {
	a, err := native.Check("p9::dial", args, 1, 2)
	if err != nil {
		return value.NilValue, err
	}
	u, err := a.String(0)
	if err != nil {
		return value.NilValue, err
	}
	network, addr, ok := address(u, false)
	if !ok {
		return value.NilValue, a.Errorf(value.ValueError, "%q is not a 9P address such as \"tcp://host:564\" or \"unix:///path\"", u)
	}
	o := dialOptions{user: os.Getenv("USER"), timeout: 10 * time.Second}
	if o.user == "" {
		o.user, _ = owner()
	}
	if a.Has(1) {
		if err := dialOpts(a, &o); err != nil {
			return value.NilValue, err
		}
	}
	c := &client{url: u, l: localOf(env)}
	if err := c.do(func() (err error) { c.r, err = dial(network, addr, o); return }); err != nil {
		if env.Err() != nil {
			return value.NilValue, err
		}
		return value.NilValue, a.Errorf(NetError, "%s: %v", u, err)
	}
	return Client.Object(c), nil
}

func dialOpts(a native.Args, o *dialOptions) error {
	if a.V[1].Kind() != value.Map {
		return a.TypeError(1, "a map of options")
	}
	var bad error
	a.V[1].AsMap().Each(func(k, v value.Value) {
		if bad != nil {
			return
		}
		key := k.String()
		switch {
		case k.Kind() != value.String:
			bad = a.Errorf(value.ValueError, "an option is named by a string, not %s", k.Kind())
		case key == "timeout":
			if v.Kind() != value.Int && v.Kind() != value.Float {
				bad = a.Errorf(value.TypeError, "option timeout must be a number of seconds, not %s", v.Kind())
				return
			}
			secs := v.AsFloat()
			if v.Kind() == value.Int {
				secs = float64(v.AsInt())
			}
			if secs != secs || secs <= 0 || secs > 1e9 {
				bad = a.Errorf(value.ValueError, "%s seconds is not a usable timeout", v.String())
				return
			}
			o.timeout = time.Duration(secs * float64(time.Second))
		case key == "user" || key == "aname" || key == "version":
			if v.Kind() != value.String {
				bad = a.Errorf(value.TypeError, "option %s must be a string, not %s", key, v.Kind())
				return
			}
			s := v.AsString()
			switch key {
			case "user":
				o.user = s
			case "aname":
				o.aname = s
			default:
				if s != "9P2000" && s != "9P2000.L" {
					bad = a.Errorf(value.ValueError, "version %q: the versions are \"9P2000.L\" and \"9P2000\"", s)
					return
				}
				o.version = s
			}
		default:
			bad = a.Errorf(value.ValueError, "unknown option %q (the options are version, user, aname and timeout)", key)
		}
	})
	return bad
}

// do runs fn, which talks with the server, and waits for it: meanwhile the
// functions of the program's own servers can run. When the program is
// cancelled, the connection is closed.
func (c *client) do(fn func() error) error {
	done := make(chan struct{})
	var err error
	go func() {
		defer close(done)
		err = fn()
	}()
	if werr := c.l.wait(done); werr != nil {
		if c.r != nil && !c.closed {
			c.closed = true
			c.r.close()
		}
		return werr
	}
	return err
}

// fail makes the exception for an error of the server about path p.
func (c *client) fail(a native.Args, p string, err error) error {
	if c.l.env.Err() != nil { // cancelled: passed on as it is
		return err
	}
	if c.closed {
		return a.Errorf(value.ValueError, "the client is closed")
	}
	return a.Errorf(value.IOError, "%s: %v", p, err)
}

func clientMethod(name string, min, max int, fn func(a native.Args, c *client) (value.Value, error)) value.NativeMethod {
	full := "p9::Client::" + name
	always := name == "close" || name == "version"
	return func(env value.Env, this *value.ObjectObj, args []value.Value) (value.Value, error) {
		a, err := native.Check(full, args, min, max)
		if err != nil {
			return value.NilValue, err
		}
		c := this.State.(*client)
		if c.closed && !always {
			return value.NilValue, a.Errorf(value.ValueError, "the client is closed")
		}
		return fn(a, c)
	}
}

// clientPathMethod is a member function whose first argument is a path on
// the server.
func clientPathMethod(name string, n int, fn func(a native.Args, c *client, p string) (value.Value, error)) value.NativeMethod {
	return clientMethod(name, n, n, func(a native.Args, c *client) (value.Value, error) {
		p, err := a.String(0)
		if err != nil {
			return value.NilValue, err
		}
		return fn(a, c, p)
	})
}

// ----------------------------------------------------------------------
// The tree and the server

func newTree9(env value.Env, this *value.ObjectObj, args []value.Value) (value.Value, error) {
	if _, err := native.Check("p9::Tree::Tree", args, 0, 0); err != nil {
		return value.NilValue, err
	}
	this.State = newTree(localOf(env))
	return value.NilValue, nil
}

func treeMethod(name string, min, max int, fn func(a native.Args, t *tree, p string) error) value.NativeMethod {
	full := "p9::Tree::" + name
	return func(env value.Env, this *value.ObjectObj, args []value.Value) (value.Value, error) {
		a, err := native.Check(full, args, min, max)
		if err != nil {
			return value.NilValue, err
		}
		p, err := a.String(0)
		if err != nil {
			return value.NilValue, err
		}
		t := this.State.(*tree)
		t.mu.Lock()
		defer t.mu.Unlock()
		if err := fn(a, t, p); err != nil {
			if e, ok := err.(*fsError); ok {
				return value.NilValue, a.Errorf(value.ValueError, "%s", e.msg)
			}
			return value.NilValue, err
		}
		return value.NilValue, nil
	}
}

// t->file(path, content [, write]): a file at path (directories above it
// are made). content is a string, or a function giving the string each
// time a client opens the file. write, a function, makes the file
// writable: when a client closes the file, write(data) gets what it wrote.
// A file already at path is replaced.
func treeFile(a native.Args, t *tree, p string) error {
	content := a.V[1]
	if content.Kind() != value.String && !callable(content) {
		return a.TypeError(1, "a string or a function")
	}
	write := value.NilValue
	if a.Has(2) && !a.V[2].IsNil() {
		if !callable(a.V[2]) {
			return a.TypeError(2, "a function")
		}
		write = a.V[2]
	}
	n, err := t.make(p, false)
	if err != nil {
		return err
	}
	n.data, n.read, n.write = "", value.NilValue, write
	if content.Kind() == value.String {
		n.data = content.AsString()
	} else {
		n.read = content
	}
	n.version++
	n.mtime = time.Now()
	return nil
}

// t->dir(path): a directory at path (and those above it).
func treeDir(a native.Args, t *tree, p string) error {
	_, err := t.make(p, true)
	return err
}

// t->remove(path): takes the file or directory at path (and all in it)
// out of the tree. Clients that have it open still read what they read.
func treeRemove(a native.Args, t *tree, p string) error {
	return t.remove(p)
}

// p9::serve(tree, url): serves the tree at url ("tcp://*:564", port 0 for
// a free port; "unix:///path"), in both versions, until the server is
// closed; events::run() keeps going meanwhile. Gives a p9::Server.
func serve9(env value.Env, args []value.Value) (value.Value, error) {
	a, err := native.Check("p9::serve", args, 2, 2)
	if err != nil {
		return value.NilValue, err
	}
	st, err := a.Object(0, Tree)
	if err != nil {
		return value.NilValue, err
	}
	t := st.(*tree)
	if t.local.env != env {
		return value.NilValue, a.Errorf(value.ValueError, "the tree belongs to another interpreter")
	}
	u, err := a.String(1)
	if err != nil {
		return value.NilValue, err
	}
	network, addr, ok := address(u, true)
	if !ok {
		return value.NilValue, a.Errorf(value.ValueError, "%q is not a 9P address such as \"tcp://*:564\" or \"unix:///path\"", u)
	}
	ln, err := net.Listen(network, addr)
	if err != nil {
		return value.NilValue, a.Errorf(NetError, "%v", err)
	}
	endpoint := u
	if network == "tcp" {
		endpoint = "tcp://" + ln.Addr().String()
	}
	return Server.Object(newServer(t, ln, endpoint)), nil
}
