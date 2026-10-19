//go:build !no9p

package p9mod

import (
	"fmt"
	"io"
	"os"
	"os/user"
	"path"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/hugelgupf/p9/linux"

	"github.com/maarten-boot/kukel/internal/modules/eventsmod"
	"github.com/maarten-boot/kukel/internal/native"
	"github.com/maarten-boot/kukel/internal/value"
)

// A tree is what p9::serve serves: directories and files the program
// makes with p9::Tree. A file holds a string, or a function that gives the
// string when a client opens the file; a file with a write function takes
// what a client writes, and the function gets it when the client closes
// the file. Servers use the tree from their own goroutines; mu guards it.
type tree struct {
	local *local // for calling the functions

	mu    sync.Mutex
	root  *node
	paths uint64 // the last qid path handed out
	files int    // files, not directories
}

type node struct {
	name    string
	parent  *node
	dir     bool
	kids    map[string]*node // of a directory
	data    string           // a file's content, when read is nil
	read    value.Value      // a function giving the content, or nil
	write   value.Value      // a function taking what is written, or nil
	path    uint64           // the qid path: unique in the tree
	version uint32           // the qid version: changes with the content
	mtime   time.Time
	removed bool

	// A node of a disk directory (disk.go), or the directory a disk is
	// mounted on (rel "."): rel is its path in the disk. Such nodes are
	// made by each walk, and do not change.
	disk *disk
	rel  string
}

func newTree(l *local) *tree {
	t := &tree{local: l}
	t.root = t.newNode("/", true)
	return t
}

func (t *tree) newNode(name string, dir bool) *node {
	t.paths++
	n := &node{name: name, dir: dir, path: t.paths, mtime: time.Now()}
	if dir {
		n.kids = map[string]*node{}
	}
	return n
}

// names splits a path of the tree ("/a/b", "a/b"; ".." stops at the top)
// into its names; the root has none.
func names(p string) []string {
	p = path.Clean("/" + p)
	if p == "/" {
		return nil
	}
	return strings.Split(p[1:], "/")
}

// at finds the node at path p, or nil. t.mu is held.
func (t *tree) at(p string) *node {
	n := t.root
	for _, name := range names(p) {
		if !n.dir || n.disk != nil {
			return nil
		}
		if n = n.kids[name]; n == nil {
			return nil
		}
	}
	return n
}

// mountOn gives the mounted disk directory that path p goes into, or nil.
// t.mu is held.
func (t *tree) mountOn(p string) *node {
	n := t.root
	for _, name := range names(p) {
		if n.disk != nil {
			return n
		}
		if !n.dir {
			return nil
		}
		if n = n.kids[name]; n == nil {
			return nil
		}
	}
	return nil
}

// make finds or makes the node at path p (a directory if dir) and the
// directories above it. t.mu is held.
func (t *tree) make(p string, dir bool) (*node, error) {
	ns := names(p)
	if len(ns) == 0 {
		if !dir {
			return nil, errorf(linux.EISDIR, "the top of the tree is a directory")
		}
		return t.root, nil
	}
	n := t.root
	for i, name := range ns {
		if n.disk != nil {
			return nil, errorf(linux.EPERM, "%s is a mounted disk directory: the tree does not reach into it", t.pathOfLocked(n))
		}
		last := i == len(ns)-1
		k := n.kids[name]
		switch {
		case k == nil:
			k = t.newNode(name, dir || !last)
			k.parent = n
			n.kids[name] = k
			n.mtime = time.Now()
			if !k.dir {
				t.files++
			}
		case !k.dir && !last:
			return nil, errorf(linux.ENOTDIR, "%s is a file, not a directory", "/"+path.Join(ns[:i+1]...))
		case last && k.dir != dir:
			if k.dir {
				return nil, errorf(linux.EISDIR, "%s is a directory", "/"+path.Join(ns...))
			}
			return nil, errorf(linux.ENOTDIR, "%s is a file", "/"+path.Join(ns...))
		}
		n = k
	}
	return n, nil
}

// remove takes the node at path p and all below it out of the tree.
// t.mu is held.
func (t *tree) remove(p string) error {
	n := t.at(p)
	switch {
	case n == nil && t.mountOn(p) != nil:
		return errorf(linux.EPERM, "%s is a mounted disk directory: the tree does not reach into it", t.pathOfLocked(t.mountOn(p)))
	case n == nil:
		return errorf(linux.ENOENT, "%s does not exist", path.Clean("/"+p))
	case n == t.root:
		return errorf(linux.EPERM, "the top of the tree cannot be removed")
	}
	delete(n.parent.kids, n.name)
	n.parent.mtime = time.Now()
	var mark func(n *node)
	mark = func(n *node) {
		n.removed = true
		if !n.dir {
			t.files--
		}
		for _, k := range n.kids {
			mark(k)
		}
	}
	mark(n)
	return nil
}

// ----------------------------------------------------------------------
// What the servers see

// fsError is what a request fails with: 9P2000.L sends the errno, 9P2000
// the message.
type fsError struct {
	errno linux.Errno
	msg   string
}

func (e *fsError) Error() string { return e.msg }
func (e *fsError) Unwrap() error { return e.errno }

func errorf(errno linux.Errno, format string, args ...any) error {
	return &fsError{errno, fmt.Sprintf(format, args...)}
}

// errnoOf gives the errno of an error of the tree.
func errnoOf(err error) linux.Errno {
	if e, ok := err.(*fsError); ok {
		return e.errno
	}
	return linux.EIO
}

var (
	errNotFound = &fsError{linux.ENOENT, "file does not exist"}
	errNotDir   = &fsError{linux.ENOTDIR, "not a directory"}
	errIsDir    = &fsError{linux.EISDIR, "is a directory"}
	errReadOnly = &fsError{linux.EACCES, "permission denied: the file cannot be written"}
	errNoCreate = &fsError{linux.EACCES, "permission denied: the tree is made by the server program"}
	errBadFid   = &fsError{linux.EBADF, "unknown fid"}
	errNotOpen  = &fsError{linux.EBADF, "the fid is not open"}
	errTooBig   = &fsError{linux.EFBIG, "the data is too large"}
)

// maxWrite is how much a client can write to a file before closing it.
const maxWrite = 1 << 30

// info is what a server tells about a node.
type info struct {
	name    string
	dir     bool
	size    uint64
	mode    uint32 // the permission bits
	path    uint64
	version uint32
	mtime   time.Time
}

func (t *tree) info(n *node) info {
	if n.disk != nil {
		return n.disk.info(n)
	}
	t.mu.Lock()
	defer t.mu.Unlock()
	return t.infoLocked(n)
}

func (t *tree) infoLocked(n *node) info {
	i := info{name: n.name, dir: n.dir, path: n.path, version: n.version, mtime: n.mtime, mode: 0o444}
	switch {
	case n.dir:
		i.mode = 0o555
	case n.read.IsNil():
		i.size = uint64(len(n.data)) // a function's content has no size until it is read
	}
	if !n.dir && !n.write.IsNil() {
		i.mode |= 0o222
	}
	return i
}

// walk goes from n to the node called name in it ("..": up).
func (t *tree) walk(n *node, name string) (*node, error) {
	if n.disk != nil {
		return n.disk.walk(n, name)
	}
	t.mu.Lock()
	defer t.mu.Unlock()
	switch {
	case n.removed:
		return nil, errNotFound
	case !n.dir:
		return nil, errNotDir
	case name == "..":
		if n.parent == nil {
			return n, nil
		}
		return n.parent, nil
	case name == "." || name == "":
		return n, nil
	}
	k := n.kids[name]
	if k == nil {
		return nil, errNotFound
	}
	return k, nil
}

// handle is a node a client opened.
type handle struct {
	t       *tree
	n       *node
	f       *os.File // a file of a disk
	data    []byte   // what reads give: the content when the file was opened
	kids    []*node  // of a directory: its entries when it was opened, by name
	writing bool

	mu    sync.Mutex // a client may write in parallel
	buf   []byte     // what the client wrote
	wrote bool       // the client wrote, or truncated the file
}

// open opens n for reading and/or writing; a function giving the content
// is called now.
func (t *tree) open(n *node, read, write, trunc bool) (*handle, error) {
	if n.disk != nil {
		return n.disk.open(t, n, read, write, trunc)
	}
	t.mu.Lock()
	removed, dir, data, readFn, writeFn := n.removed, n.dir, n.data, n.read, n.write
	var kids []*node
	if dir {
		kids = make([]*node, 0, len(n.kids))
		for _, k := range n.kids {
			kids = append(kids, k)
		}
		sort.Slice(kids, func(i, j int) bool { return kids[i].name < kids[j].name })
	}
	t.mu.Unlock()
	switch {
	case removed:
		return nil, errNotFound
	case dir && (write || trunc):
		return nil, errIsDir
	case (write || trunc) && writeFn.IsNil():
		return nil, errReadOnly
	}
	h := &handle{t: t, n: n, kids: kids, writing: write || trunc, wrote: trunc}
	if read && !dir {
		if !readFn.IsNil() {
			v, err := t.local.call(readFn, nil)
			if err != nil {
				return nil, err
			}
			if v.Kind() != value.String {
				return nil, errorf(linux.EIO, "the content function of %s gave %s, not a string", t.pathOf(n), v.Kind())
			}
			data = v.AsString()
		}
		h.data = []byte(data)
	}
	return h, nil
}

func (h *handle) readAt(p []byte, off uint64) (int, error) {
	if h.f != nil {
		n, err := h.f.ReadAt(p, int64(off))
		if err == io.EOF {
			err = nil
		}
		return n, diskError(err)
	}
	if off >= uint64(len(h.data)) {
		return 0, nil
	}
	return copy(p, h.data[off:]), nil
}

func (h *handle) writeAt(p []byte, off uint64) (int, error) {
	if !h.writing {
		return 0, errNotOpen
	}
	if h.f != nil {
		n, err := h.f.WriteAt(p, int64(off))
		return n, diskError(err)
	}
	end := off + uint64(len(p))
	if end > maxWrite || end < off {
		return 0, errTooBig
	}
	h.mu.Lock()
	defer h.mu.Unlock()
	if end > uint64(len(h.buf)) {
		h.buf = append(h.buf, make([]byte, int(end)-len(h.buf))...)
	}
	copy(h.buf[off:], p)
	h.wrote = true
	return len(p), nil
}

// truncate sets the size of what was written (0 from Linux for O_TRUNC).
func (h *handle) truncate(size uint64) error {
	if h.f != nil {
		return diskError(h.f.Truncate(int64(size)))
	}
	if size > maxWrite {
		return errTooBig
	}
	h.mu.Lock()
	defer h.mu.Unlock()
	if size < uint64(len(h.buf)) {
		h.buf = h.buf[:size]
	} else {
		h.buf = append(h.buf, make([]byte, int(size)-len(h.buf))...)
	}
	h.wrote = true
	return nil
}

// close gives what the client wrote to the write function of the file.
func (h *handle) close() error {
	if h.f != nil {
		return diskError(h.f.Close())
	}
	h.mu.Lock()
	wrote, data := h.wrote, string(h.buf)
	h.wrote = false
	h.mu.Unlock()
	if !wrote {
		return nil
	}
	h.t.mu.Lock()
	fn, removed := h.n.write, h.n.removed
	h.t.mu.Unlock()
	if removed || fn.IsNil() {
		return nil
	}
	_, err := h.t.local.call(fn, []value.Value{value.MakeString(data)})
	return err
}

func (t *tree) pathOf(n *node) string {
	t.mu.Lock()
	defer t.mu.Unlock()
	return t.pathOfLocked(n)
}

func (t *tree) pathOfLocked(n *node) string {
	var parts []string
	for ; n != nil && n.parent != nil; n = n.parent {
		parts = append([]string{n.name}, parts...)
	}
	return "/" + strings.Join(parts, "/")
}

// owner is who the files belong to: the user running the server.
var owner = sync.OnceValues(func() (string, uint32) {
	name := "none"
	if u, err := user.Current(); err == nil {
		name = u.Username
	}
	uid := os.Getuid()
	if uid < 0 {
		uid = 0
	}
	return name, uint32(uid)
})

var group = sync.OnceValue(func() uint32 {
	gid := os.Getgid()
	if gid < 0 {
		gid = 0
	}
	return uint32(gid)
})

// ----------------------------------------------------------------------
// Calling the program's functions

// local is the state of the module in one interpreter. The functions of a
// tree run on the interpreter's goroutine: inside events::run(), or while
// a p9::Client of the same program waits for an answer (so that a program
// can be a client of its own server).
type local struct {
	env  value.Env
	loop *eventsmod.Loop

	mu     sync.Mutex
	jobs   []*job
	signal chan struct{} // capacity 1: a job was added
}

type localKey struct{}

// localOf gives the state of env's interpreter (on its goroutine).
func localOf(env value.Env) *local {
	return env.Local(localKey{}, func() any {
		return &local{env: env, loop: eventsmod.For(env), signal: make(chan struct{}, 1)}
	}).(*local)
}

// job is a call of a function of a tree, for a server goroutine.
type job struct {
	fn   value.Value
	args []value.Value
	done chan jobResult
}

type jobResult struct {
	v   value.Value
	err error
}

// call runs fn(args) on the interpreter's goroutine and waits for it. An
// exception of fn fails the client's request with its message; the
// program goes on.
func (l *local) call(fn value.Value, args []value.Value) (value.Value, error) {
	j := &job{fn: fn, args: args, done: make(chan jobResult, 1)}
	l.mu.Lock()
	l.jobs = append(l.jobs, j)
	l.mu.Unlock()
	select {
	case l.signal <- struct{}{}:
	default:
	}
	l.loop.Post(l.runJobs)
	select {
	case r := <-j.done:
		return r.v, r.err
	case <-l.env.Done():
		return value.NilValue, errorf(linux.EIO, "the server program was stopped")
	}
}

// runJobs runs the jobs waiting, on the interpreter's goroutine. An error
// that is not an exception (exit(), a cancelled program) is passed on.
func (l *local) runJobs() error {
	for {
		l.mu.Lock()
		if len(l.jobs) == 0 {
			l.mu.Unlock()
			return nil
		}
		j := l.jobs[0]
		l.jobs = l.jobs[1:]
		l.mu.Unlock()
		v, err := l.env.Call(j.fn, j.args)
		if err != nil {
			if thrown, ok := l.env.Thrown(err); ok {
				j.done <- jobResult{err: errorf(linux.EIO, "%s", exceptionMessage(thrown))}
				continue
			}
			j.done <- jobResult{err: errorf(linux.EIO, "the server program stopped")}
			return err
		}
		j.done <- jobResult{v: v}
	}
}

// exceptionMessage is the message of a thrown value: of an Error object,
// or the value itself.
func exceptionMessage(v value.Value) string {
	if v.Kind() == value.Object {
		o := v.AsObject()
		if i, ok := o.Class.FieldIndex["message"]; ok && o.Fields[i].Kind() == value.String {
			return o.Fields[i].AsString()
		}
	}
	return v.String()
}

// wait waits until done is closed, running the jobs of the program's own
// servers meanwhile.
func (l *local) wait(done <-chan struct{}) error {
	native.Flush(l.env)
	for {
		select {
		case <-done:
			return nil
		case <-l.signal:
			if err := l.runJobs(); err != nil {
				return err
			}
		case <-l.env.Done():
			return l.env.Err()
		}
	}
}
