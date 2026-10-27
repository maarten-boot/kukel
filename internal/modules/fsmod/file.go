package fsmod

// Open files (TODO.md Q83): fs::open gives an fs::File to read and write a
// piece at a time; fs::stdin(), fs::stdout() and fs::stderr() give the
// program's standard streams as such objects.
//
//	with (local log = fs::open("app.log", "a")) log->println(time::now(), "started");
//
// Reads and writes are buffered. A file the program did not close is
// closed when its program ends (VM.End calls openFiles.End).

import (
	"bufio"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"sort"
	"strings"
	"time"

	"github.com/maarten-boot/kukel/internal/native"
	"github.com/maarten-boot/kukel/internal/value"
)

// File is the class fs::File. It has no constructor: files come from
// fs::open and the standard streams.
var File = &native.Class{
	Name: "File",
	Methods: map[string]value.NativeMethod{
		"readline": method("readline", 0, 0, readline),
		"read":     method("read", 0, 1, read),
		"write":    method("write", 1, -1, write),
		"print":    method("print", 0, -1, printer("")),
		"println":  method("println", 0, -1, printer("\n")),
		"seek":     method("seek", 1, 2, seek),
		"tell":     method("tell", 0, 0, tell),
		"truncate": method("truncate", 0, 1, truncate),
		"lock":     method("lock", 0, 2, lock),
		"unlock":   method("unlock", 0, 0, unlock),
		"flush":    method("flush", 0, 0, flush),
		"close":    method("close", 0, 0, closeFile),
		"iter":     method("iter", 0, 0, lines),
		"closed": method("closed", 0, 0, func(a native.Args, env value.Env, f *file) (value.Value, error) {
			return value.MakeBool(f.closed), nil
		}),
		"name": method("name", 0, 0, func(a native.Args, env value.Env, f *file) (value.Value, error) {
			return value.MakeString(f.name), nil
		}),
		"mode": method("mode", 0, 0, func(a native.Args, env value.Env, f *file) (value.Value, error) {
			return value.MakeString(f.mode), nil
		}),
	},
	Show: func(o *value.ObjectObj) string {
		f := o.State.(*file)
		switch {
		case f.std != 0:
			return "fs::File(" + f.name + ")"
		case f.closed:
			return fmt.Sprintf("fs::File(%q, closed)", f.name)
		}
		return fmt.Sprintf("fs::File(%q, %q)", f.name, f.mode)
	},
}

// The standard streams (file.std).
const (
	stdin = 1 + iota
	stdout
	stderr
)

// file is the Go part of an fs::File.
type file struct {
	name, mode    string
	std           int // 0, or stdin, stdout, stderr
	f             *os.File
	r             *bufio.Reader // nil when not open for reading
	w             *bufio.Writer // nil when not open for writing (and for the standard streams)
	env           value.Env     // of a standard stream: the interpreter whose streams it uses
	reads, writes bool          // open for reading, for writing
	waits         bool          // reading is a wait: standard input, a pipe, a terminal, a device
	closed        bool
	locked        string      // "", "shared" or "exclusive"
	open          *openFiles  // the interpreter's open files
	self          value.Value // the fs::File object
}

// modes are the modes of fs::open, with the flags of each.
var modes = map[string]int{
	"r":  os.O_RDONLY,
	"r+": os.O_RDWR,
	"w":  os.O_WRONLY | os.O_CREATE | os.O_TRUNC,
	"w+": os.O_RDWR | os.O_CREATE | os.O_TRUNC,
	"a":  os.O_WRONLY | os.O_CREATE | os.O_APPEND,
	"a+": os.O_RDWR | os.O_CREATE | os.O_APPEND,
	"x":  os.O_WRONLY | os.O_CREATE | os.O_EXCL,
	"x+": os.O_RDWR | os.O_CREATE | os.O_EXCL,
}

const bufSize = 64 << 10

// fs::open(path [, mode]): an fs::File.
func open(env value.Env, args []value.Value) (value.Value, error) {
	a, err := native.Check("fs::open", args, 1, 2)
	if err != nil {
		return value.NilValue, err
	}
	path, err := a.String(0)
	if err != nil {
		return value.NilValue, err
	}
	mode := "r"
	if a.Has(1) {
		if mode, err = a.String(1); err != nil {
			return value.NilValue, err
		}
	}
	flags, ok := modes[mode]
	if !ok {
		return value.NilValue, a.Errorf(value.ValueError, `the mode %q is not one of "r", "w", "a" and "x", with or without "+"`, mode)
	}
	if info, err := os.Stat(path); err == nil && info.Mode()&fs.ModeNamedPipe != 0 {
		defer native.Wait(env).Done() // opening a FIFO waits for its other end
	}
	osf, err := os.OpenFile(path, openFlags(mode, flags), 0o644)
	if err != nil {
		return value.NilValue, ioError(a, err)
	}
	info, err := osf.Stat()
	if err == nil && info.IsDir() {
		err = &fs.PathError{Op: "open", Path: path, Err: errors.New("is a directory")}
	}
	if err != nil {
		osf.Close()
		return value.NilValue, ioError(a, err)
	}
	return newFile(env, path, mode, osf, !info.Mode().IsRegular()), nil
}

// newFile makes the fs::File of osf, opened as mode, and counts it among
// the interpreter's open files.
func newFile(env value.Env, name, mode string, osf *os.File, waits bool) value.Value {
	f := &file{name: name, mode: mode, f: osf, waits: waits,
		reads: mode[0] == 'r' || len(mode) == 2, writes: mode[0] != 'r' || len(mode) == 2}
	if f.reads {
		f.r = bufio.NewReaderSize(osf, bufSize)
	}
	if f.writes {
		f.w = bufio.NewWriterSize(osf, bufSize)
	}
	f.open = opened(env)
	f.open.files[f] = struct{}{}
	f.self = File.Object(f)
	return f.self
}

// fs::temp([pattern [, dir]]): a new file, open "w+", named by pattern
// with its last * made random, in dir or the system's temporary directory.
// It stays until the program removes it.
func temp(env value.Env, args []value.Value) (value.Value, error) {
	a, pattern, dir, err := tempArgs("fs::temp", args)
	if err != nil {
		return value.NilValue, err
	}
	osf, err := os.CreateTemp(dir, pattern)
	if err != nil {
		return value.NilValue, ioError(a, err)
	}
	return newFile(env, osf.Name(), "w+", osf, false), nil
}

// fs::tempdir([pattern [, dir]]): the path of a new directory, named as
// for fs::temp.
func tempdir(env value.Env, args []value.Value) (value.Value, error) {
	a, pattern, dir, err := tempArgs("fs::tempdir", args)
	if err != nil {
		return value.NilValue, err
	}
	path, err := os.MkdirTemp(dir, pattern)
	if err != nil {
		return value.NilValue, ioError(a, err)
	}
	return value.MakeString(path), nil
}

func tempArgs(name string, args []value.Value) (a native.Args, pattern, dir string, err error) {
	if a, err = native.Check(name, args, 0, 2); err != nil {
		return
	}
	pattern = "kukel-*"
	if a.Has(0) {
		if pattern, err = a.String(0); err != nil {
			return
		}
		if strings.ContainsAny(pattern, `/`+string(os.PathSeparator)) {
			err = a.Errorf(value.ValueError, "the pattern %q has a path separator (the directory is the second argument)", pattern)
			return
		}
	}
	if a.Has(1) {
		dir, err = a.String(1)
	}
	return
}

type stdKey int

// standard makes fs::stdin(), fs::stdout() and fs::stderr(): the same
// object each time in one interpreter.
func standard(which int, name string) native.Func {
	return func(env value.Env, args []value.Value) (value.Value, error) {
		if _, err := native.Check("fs::"+name, args, 0, 0); err != nil {
			return value.NilValue, err
		}
		return env.Local(stdKey(which), func() any {
			f := &file{name: "<" + name + ">", mode: "w", std: which, env: env, writes: true}
			if which == stdin {
				f.mode, f.r, f.reads, f.writes, f.waits = "r", env.Stdin(), true, false, true
			}
			f.self = File.Object(f)
			return f.self
		}).(value.Value), nil
	}
}

type openKey struct{}

// openFiles are the files an interpreter opened and did not close.
type openFiles struct {
	files map[*file]struct{}
}

func opened(env value.Env) *openFiles {
	return env.Local(openKey{}, func() any { return &openFiles{files: map[*file]struct{}{}} }).(*openFiles)
}

// End closes the files the program left open, when it ends (VM.End), in
// the order of their names; it gives the first failure.
func (o *openFiles) End() error {
	fs := make([]*file, 0, len(o.files))
	for f := range o.files {
		fs = append(fs, f)
	}
	sort.Slice(fs, func(i, j int) bool { return fs[i].name < fs[j].name })
	var first error
	for _, f := range fs {
		if err := f.close(); err != nil && first == nil {
			first = errors.New(native.IOMessage(err))
		}
	}
	return first
}

// method makes a member function of fs::File that checks its arguments
// and, except for close, closed, name and mode, that the file is open.
func method(name string, min, max int, fn func(a native.Args, env value.Env, f *file) (value.Value, error)) value.NativeMethod {
	full := "fs::File::" + name
	anyTime := name == "close" || name == "closed" || name == "name" || name == "mode"
	return func(env value.Env, this *value.ObjectObj, args []value.Value) (value.Value, error) {
		a, err := native.Check(full, args, min, max)
		if err != nil {
			return value.NilValue, err
		}
		f := this.State.(*file)
		if f.closed && !anyTime {
			return value.NilValue, a.Errorf(value.ValueError, "%s is closed", f.name)
		}
		return fn(a, env, f)
	}
}

// reader is the file's reader, after what was written is written out.
func (f *file) reader(a native.Args) (*bufio.Reader, error) {
	if !f.reads {
		return nil, a.Errorf(value.ValueError, "%s is not open for reading", f.name)
	}
	if f.w != nil && f.w.Buffered() > 0 {
		if err := f.w.Flush(); err != nil {
			return nil, ioError(a, err)
		}
	}
	return f.r, nil
}

// put writes s, after giving back what was read ahead.
func (f *file) put(a native.Args, s string) error {
	if !f.writes {
		return a.Errorf(value.ValueError, "%s is not open for writing", f.name)
	}
	var err error
	switch f.std {
	case stdout:
		_, err = io.WriteString(f.env.Stdout(), s)
	case stderr:
		if fl, ok := f.env.Stdout().(interface{ Flush() error }); ok {
			fl.Flush() // in order with what was printed, as eprint
		}
		_, err = io.WriteString(f.env.Stderr(), s)
	default:
		if f.r != nil && f.r.Buffered() > 0 && !f.waits { // (a pipe or terminal reads and writes apart: nothing to give back)
			if _, err := f.f.Seek(-int64(f.r.Buffered()), io.SeekCurrent); err != nil {
				return ioError(a, err)
			}
			f.r.Reset(f.f)
		}
		_, err = f.w.WriteString(s)
	}
	if err != nil {
		return ioError(a, err)
	}
	return nil
}

// f->readline(): the next line without its line ending, or nil at the end.
func readline(a native.Args, env value.Env, f *file) (value.Value, error) {
	r, err := f.reader(a)
	if err != nil {
		return value.NilValue, err
	}
	if f.waits {
		defer native.Wait(env).Done()
	}
	line, err := r.ReadString('\n')
	if err != nil && err != io.EOF {
		return value.NilValue, ioError(a, err)
	}
	if err == io.EOF && line == "" {
		return value.NilValue, nil
	}
	line = strings.TrimSuffix(line, "\n") // as the built-in readline
	line = strings.TrimSuffix(line, "\r")
	return value.MakeString(line), nil
}

// f->iter(): a generator of the lines from where the file is, as readline
// reads them: for (local line in f).
func lines(a native.Args, env value.Env, f *file) (value.Value, error) {
	return native.Generator(env, "fs::File::iter", func() (value.Value, bool, error) {
		if f.closed {
			return value.NilValue, false, a.Errorf(value.ValueError, "%s is closed", f.name)
		}
		line, err := readline(a, env, f)
		return line, err == nil && !line.IsNil(), err
	})
}

// f->read([n]): up to n bytes, or the rest; nil at the end.
func read(a native.Args, env value.Env, f *file) (value.Value, error) {
	var src io.Reader
	r, err := f.reader(a)
	if err != nil {
		return value.NilValue, err
	}
	src = r
	if a.Has(0) {
		n, err := a.Int(0)
		if err != nil {
			return value.NilValue, err
		}
		if n < 0 {
			return value.NilValue, a.Errorf(value.ValueError, "a negative number of bytes (%d)", n)
		}
		if n == 0 {
			return value.MakeString(""), nil
		}
		src = io.LimitReader(r, n)
	}
	if f.waits {
		defer native.Wait(env).Done()
	}
	data, err := io.ReadAll(src)
	if err != nil {
		return value.NilValue, ioError(a, err)
	}
	if len(data) == 0 {
		return value.NilValue, nil
	}
	return value.MakeString(string(data)), nil
}

// f->write(s, ...): writes the strings; gives f.
func write(a native.Args, env value.Env, f *file) (value.Value, error) {
	for i := range a.V {
		if _, err := a.String(i); err != nil {
			return value.NilValue, err
		}
	}
	for _, v := range a.V {
		if err := f.put(a, v.AsString()); err != nil {
			return value.NilValue, err
		}
	}
	return f.self, nil
}

// printer makes f->print and f->println: the values as print shows them.
func printer(end string) func(a native.Args, env value.Env, f *file) (value.Value, error) {
	return func(a native.Args, env value.Env, f *file) (value.Value, error) {
		buf := make([]byte, 0, 64)
		for i, v := range a.V {
			if i > 0 {
				buf = append(buf, ' ')
			}
			if v.Kind() == value.String {
				buf = append(buf, v.AsString()...)
			} else {
				buf = append(buf, v.String()...)
			}
		}
		return value.NilValue, f.put(a, string(append(buf, end...)))
	}
}

// noPosition is the IOError of seek and tell on a standard stream.
func (f *file) noPosition(a native.Args) error {
	return a.Errorf(value.IOError, "%s: a standard stream has no position", f.name)
}

// f->seek(offset [, whence]): the new position.
func seek(a native.Args, env value.Env, f *file) (value.Value, error) {
	off, err := a.Int(0)
	if err != nil {
		return value.NilValue, err
	}
	whence := io.SeekStart
	if a.Has(1) {
		w, err := a.String(1)
		if err != nil {
			return value.NilValue, err
		}
		switch w {
		case "start":
		case "current":
			whence = io.SeekCurrent
		case "end":
			whence = io.SeekEnd
		default:
			return value.NilValue, a.Errorf(value.ValueError, `whence must be "start", "current" or "end", not %q`, w)
		}
	}
	if f.std != 0 {
		return value.NilValue, f.noPosition(a)
	}
	if f.w != nil {
		if err := f.w.Flush(); err != nil {
			return value.NilValue, ioError(a, err)
		}
	}
	if whence == io.SeekCurrent && f.r != nil {
		off -= int64(f.r.Buffered())
	}
	pos, err := f.f.Seek(off, whence)
	if err != nil {
		return value.NilValue, ioError(a, err)
	}
	if f.r != nil {
		f.r.Reset(f.f)
	}
	return value.MakeInt(pos), nil
}

// f->tell(): the position.
func tell(a native.Args, env value.Env, f *file) (value.Value, error) {
	if f.std != 0 {
		return value.NilValue, f.noPosition(a)
	}
	pos, err := f.position(a)
	if err != nil {
		return value.NilValue, err
	}
	return value.MakeInt(pos), nil
}

// f->flush(): writes out what is buffered.
func flush(a native.Args, env value.Env, f *file) (value.Value, error) {
	var err error
	switch {
	case f.std == stdout || f.std == stderr:
		for _, w := range []io.Writer{f.env.Stdout(), f.env.Stderr()} { // (native.Flush drops the error)
			if fl, ok := w.(interface{ Flush() error }); ok && err == nil {
				err = fl.Flush()
			}
		}
	case f.w != nil:
		err = f.w.Flush()
	}
	if err != nil {
		return value.NilValue, ioError(a, err)
	}
	return value.NilValue, nil
}

// f->close(): flushes and closes; a closed file is left alone, and a
// standard stream is only flushed.
func closeFile(a native.Args, env value.Env, f *file) (value.Value, error) {
	if f.std != 0 {
		return flush(a, env, f)
	}
	if err := f.close(); err != nil {
		return value.NilValue, ioError(a, err)
	}
	return value.NilValue, nil
}

// close closes the file once, writing out what is buffered first (and
// giving up its lock).
func (f *file) close() error {
	if f.closed {
		return nil
	}
	f.closed = true
	delete(f.open.files, f)
	var err error
	if f.w != nil {
		err = f.w.Flush()
	}
	if f.locked != "" {
		f.locked = ""
		if uerr := unlockFile(f.f); err == nil {
			err = uerr
		}
	}
	if cerr := f.f.Close(); err == nil {
		err = cerr
	}
	return err
}

// position is where the program is in the file: the system's position,
// less what was read ahead, after what is buffered is written out.
func (f *file) position(a native.Args) (int64, error) {
	if f.w != nil {
		if err := f.w.Flush(); err != nil { // (in append mode the position is known only after)
			return 0, ioError(a, err)
		}
	}
	pos, err := f.f.Seek(0, io.SeekCurrent)
	if err != nil {
		return 0, ioError(a, err)
	}
	if f.r != nil {
		pos -= int64(f.r.Buffered())
	}
	return pos, nil
}

// f->truncate([size]): cuts (or extends) the file to size bytes, by
// default to the position; the position stays.
func truncate(a native.Args, env value.Env, f *file) (value.Value, error) {
	if !f.writes {
		return value.NilValue, a.Errorf(value.ValueError, "%s is not open for writing", f.name)
	}
	if f.std != 0 {
		return value.NilValue, a.Errorf(value.IOError, "%s: a standard stream cannot be truncated", f.name)
	}
	pos, err := f.position(a)
	if err != nil {
		return value.NilValue, err
	}
	size := pos
	if a.Has(0) {
		if size, err = a.Int(0); err != nil {
			return value.NilValue, err
		}
		if size < 0 {
			return value.NilValue, a.Errorf(value.ValueError, "a negative size (%d)", size)
		}
	}
	if err := f.f.Truncate(size); err != nil {
		return value.NilValue, ioError(a, err)
	}
	if f.r != nil && f.r.Buffered() > 0 { // what was read ahead may be gone: read again from the position
		if _, err := f.f.Seek(pos, io.SeekStart); err != nil {
			return value.NilValue, ioError(a, err)
		}
		f.r.Reset(f.f)
	}
	return value.NilValue, nil
}

// f->lock([kind [, timeout]]): takes (or changes) the lock on the whole
// file, waiting until it is free, at most timeout seconds; whether it got
// it. A waiting flock cannot be interrupted, so it tries again and again,
// a little longer apart each time, and stops when the program is
// cancelled.
func lock(a native.Args, env value.Env, f *file) (value.Value, error) {
	kind := "exclusive"
	if a.Has(0) {
		var err error
		if kind, err = a.String(0); err != nil {
			return value.NilValue, err
		}
		if kind != "exclusive" && kind != "shared" {
			return value.NilValue, a.Errorf(value.ValueError, `the kind of lock must be "exclusive" or "shared", not %q`, kind)
		}
	}
	limit, timed := time.Duration(0), a.Has(1)
	if timed {
		var err error
		if limit, err = a.Seconds(1, "a usable timeout"); err != nil {
			return value.NilValue, err
		}
	}
	if f.std != 0 {
		return value.NilValue, a.Errorf(value.IOError, "%s: a standard stream cannot be locked", f.name)
	}
	if f.locked == kind {
		return value.True, nil
	}
	// What was written before goes out first (a shared lock on Windows
	// stops even the holder from writing), and what was read ahead goes
	// back: under the lock, reading starts from the file as it is.
	if f.w != nil {
		if err := f.w.Flush(); err != nil {
			return value.NilValue, ioError(a, err)
		}
	}
	if f.r != nil && f.r.Buffered() > 0 {
		if _, err := f.f.Seek(-int64(f.r.Buffered()), io.SeekCurrent); err != nil {
			return value.NilValue, ioError(a, err)
		}
		f.r.Reset(f.f)
	}
	try := func() (bool, error) {
		ok, kept, err := tryLock(f.f, kind == "exclusive", f.locked != "")
		switch {
		case ok:
			f.locked = kind
		case !kept:
			f.locked = "" // a failed change of kind that lost the old lock
		}
		if err != nil {
			return false, ioError(a, err)
		}
		return ok, nil
	}
	if ok, err := try(); ok || err != nil || (timed && limit == 0) {
		return value.MakeBool(ok), err
	}
	defer native.Wait(env).Done()
	deadline := time.Now().Add(limit)
	for pause := time.Millisecond; ; pause = min(2*pause, 50*time.Millisecond) {
		if timed {
			if left := time.Until(deadline); left <= 0 {
				return value.False, nil
			} else if pause > left {
				pause = left
			}
		}
		select {
		case <-time.After(pause):
		case <-env.Done(): // the program was cancelled
			return value.NilValue, env.Err()
		}
		if ok, err := try(); ok || err != nil {
			return value.MakeBool(ok), err
		}
	}
}

// f->unlock(): writes out what is buffered (written under the lock), then
// gives up the lock (none: nothing).
func unlock(a native.Args, env value.Env, f *file) (value.Value, error) {
	if f.locked == "" {
		return value.NilValue, nil
	}
	var err error
	if f.w != nil {
		err = f.w.Flush()
	}
	f.locked = ""
	if uerr := unlockFile(f.f); err == nil {
		err = uerr
	}
	if err != nil {
		return value.NilValue, ioError(a, err)
	}
	return value.NilValue, nil
}
