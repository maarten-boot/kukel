// Package fsmod is the native module "fs": directories and files of the
// local file system (TODO.md Q61).
//
//	use fs;
//	for (local e in fs::readdir(".")) println(e["name"], e["dir"] ? "/" : e["size"]);
//	fs::walk("src", fn(path, e) { if (fs::ext(path) == ".kkl") println(path); });
//
// An entry is a map: name, size, dir, link, mode and time (a time::Time).
// What fails is an IOError. Symbolic links are not followed by walk.
// fs::open gives an fs::File (file.go).
package fsmod

import (
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"syscall"

	"github.com/maarten-boot/kukel/internal/modules/timemod"
	"github.com/maarten-boot/kukel/internal/native"
	"github.com/maarten-boot/kukel/internal/value"
)

func init() {
	native.Register(&native.Module{
		Name: "fs",
		Doc:  "directories and files: open files, temporary files, readdir, stat, walk, mkdir, remove, rename, chmod, paths",
		Funcs: map[string]native.Func{
			"readdir":   readdir,
			"stat":      stat,
			"exists":    exists,
			"walk":      walk,
			"mkdir":     pathOp("fs::mkdir", func(p string) error { return os.Mkdir(p, 0o777) }),
			"mkdirall":  pathOp("fs::mkdirall", func(p string) error { return os.MkdirAll(p, 0o777) }),
			"remove":    pathOp("fs::remove", os.Remove),
			"removeall": pathOp("fs::removeall", removeAll),
			"rename":    rename,
			"chmod":     chmod,
			"temp":      temp,
			"tempdir":   tempdir,
			"join":      join,
			"base":      pathFunc("fs::base", filepath.Base),
			"dir":       pathFunc("fs::dir", filepath.Dir),
			"ext":       pathFunc("fs::ext", filepath.Ext),
			"open":      open,
			"stdin":     standard(stdin, "stdin"),
			"stdout":    standard(stdout, "stdout"),
			"stderr":    standard(stderr, "stderr"),
		},
		Classes: []*native.Class{File},
	})
}

// entry is the map of what info tells about a file called name.
func entry(name string, info fs.FileInfo, link bool) value.Value {
	m := value.NewMap()
	set := func(k string, v value.Value) { m.Set(value.MakeString(k), v) } // string keys cannot fail
	set("name", value.MakeString(name))
	set("size", value.MakeInt(info.Size()))
	set("dir", value.MakeBool(info.IsDir()))
	set("link", value.MakeBool(link))
	set("mode", value.MakeString(ModeString(info.Mode())))
	set("time", timemod.Time.Object(info.ModTime()))
	return value.MakeMap(m)
}

// ModeString is mode as ls shows it: "-rw-r--r--", "drwxr-xr-x",
// "lrwxrwxrwx"; s, S and t, T for setuid, setgid and sticky.
func ModeString(m fs.FileMode) string {
	b := []byte("----------")
	switch {
	case m&fs.ModeDir != 0:
		b[0] = 'd'
	case m&fs.ModeSymlink != 0:
		b[0] = 'l'
	case m&fs.ModeNamedPipe != 0:
		b[0] = 'p'
	case m&fs.ModeSocket != 0:
		b[0] = 's'
	case m&fs.ModeCharDevice != 0:
		b[0] = 'c'
	case m&fs.ModeDevice != 0:
		b[0] = 'b'
	}
	const rwx = "rwxrwxrwx"
	for i := 0; i < 9; i++ {
		if m&(1<<(8-i)) != 0 {
			b[i+1] = rwx[i]
		}
	}
	special := func(i int, on bool, lower byte) {
		if on {
			if b[i] == 'x' {
				b[i] = lower
			} else {
				b[i] = lower - 'a' + 'A'
			}
		}
	}
	special(3, m&fs.ModeSetuid != 0, 's')
	special(6, m&fs.ModeSetgid != 0, 's')
	special(9, m&fs.ModeSticky != 0, 't')
	return string(b)
}

// ioError is the IOError for err (native.IOError: without Go's name of
// the operation).
var ioError = native.IOError

// fs::readdir(path): the entries of a directory, by name; a symbolic link
// is an entry of its own (link true), not what it points to.
func readdir(env value.Env, args []value.Value) (value.Value, error) {
	a, path, err := pathArg("fs::readdir", args, 1)
	if err != nil {
		return value.NilValue, err
	}
	des, err := os.ReadDir(path)
	if err != nil {
		return value.NilValue, ioError(a, err)
	}
	out := make([]value.Value, 0, len(des))
	for _, de := range des {
		info, err := de.Info()
		if errors.Is(err, fs.ErrNotExist) {
			continue // removed while it was read
		}
		if err != nil {
			return value.NilValue, ioError(a, err)
		}
		out = append(out, entry(de.Name(), info, de.Type()&fs.ModeSymlink != 0))
	}
	return value.MakeVector(out), nil
}

// fs::stat(path): the entry of a file or directory (of what a symbolic
// link points to, with link true).
func stat(env value.Env, args []value.Value) (value.Value, error) {
	a, path, err := pathArg("fs::stat", args, 1)
	if err != nil {
		return value.NilValue, err
	}
	l, err := os.Lstat(path)
	if err != nil {
		return value.NilValue, ioError(a, err)
	}
	info, link := l, l.Mode()&fs.ModeSymlink != 0
	if link {
		if info, err = os.Stat(path); err != nil {
			return value.NilValue, ioError(a, err)
		}
	}
	return entry(filepath.Base(path), info, link), nil
}

// fs::exists(path): whether there is a file or directory (a link to
// nothing, or a loop of links, does not count). It is false when the
// path, or a directory on the way, is not there or is a file; other
// failures (no permission) are an IOError.
func exists(env value.Env, args []value.Value) (value.Value, error) {
	a, path, err := pathArg("fs::exists", args, 1)
	if err != nil {
		return value.NilValue, err
	}
	switch _, err = os.Stat(path); {
	case err == nil:
		return value.True, nil
	case errors.Is(err, fs.ErrNotExist) || errors.Is(err, syscall.ENOTDIR) || errors.Is(err, syscall.ELOOP):
		return value.False, nil
	}
	return value.NilValue, ioError(a, err)
}

// fs::walk(path, f): calls f(p, entry) for path and every file and
// directory below it, depth first, by name; p is path joined with the
// names. When f returns false for a directory, walk does not go into it.
// Symbolic links are not followed.
func walk(env value.Env, args []value.Value) (value.Value, error) {
	a, root, err := pathArg("fs::walk", args, 2)
	if err != nil {
		return value.NilValue, err
	}
	f := a.V[1]
	switch f.Kind() {
	case value.Function, value.Closure, value.Builtin, value.BoundMethod:
	default:
		return value.NilValue, a.TypeError(1, "a function")
	}
	var inner error // an exception of f, passed on unchanged
	err = filepath.WalkDir(root, func(p string, de fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		info, err := de.Info()
		if err != nil {
			return err
		}
		r, err := env.Call(f, []value.Value{value.MakeString(p), entry(de.Name(), info, de.Type()&fs.ModeSymlink != 0)})
		if err != nil {
			inner = err
			return err
		}
		if de.IsDir() && r.Kind() == value.Bool && !r.AsBool() {
			return filepath.SkipDir
		}
		return nil
	})
	switch {
	case inner != nil:
		return value.NilValue, inner
	case err != nil:
		return value.NilValue, ioError(a, err)
	}
	return value.NilValue, nil
}

// fs::chmod(path, mode): sets the permissions, written as ls shows them
// ("rw-r--r--", or an entry's "-rw-r--r--").
func chmod(env value.Env, args []value.Value) (value.Value, error) {
	a, path, err := pathArg("fs::chmod", args, 2)
	if err != nil {
		return value.NilValue, err
	}
	text, err := a.String(1)
	if err != nil {
		return value.NilValue, err
	}
	mode, ok := ParseMode(text)
	if !ok {
		return value.NilValue, a.Errorf(value.ValueError, `the mode %q is not written as ls shows it ("rw-r--r--")`, text)
	}
	if err := os.Chmod(path, mode); err != nil {
		return value.NilValue, ioError(a, err)
	}
	return value.NilValue, nil
}

// ParseMode reads permissions as ModeString writes them: nine letters
// ("rwxr-x---"), or ten with the kind of file first, which is ignored;
// s, S, t and T for setuid, setgid and sticky.
func ParseMode(s string) (fs.FileMode, bool) {
	if len(s) == 10 {
		s = s[1:]
	}
	if len(s) != 9 {
		return 0, false
	}
	var m fs.FileMode
	special := [3]fs.FileMode{fs.ModeSetuid, fs.ModeSetgid, fs.ModeSticky}
	for i := 0; i < 9; i++ {
		bit := fs.FileMode(1) << (8 - i)
		c := s[i]
		switch {
		case c == '-':
		case c == "rwx"[i%3]:
			m |= bit
		case i%3 == 2 && c == "sst"[i/3]:
			m |= bit | special[i/3]
		case i%3 == 2 && c == "SST"[i/3]:
			m |= special[i/3]
		default:
			return 0, false
		}
	}
	return m, true
}

// removeAll is os.RemoveAll, but a path that does not exist is an error,
// as for remove.
func removeAll(p string) error {
	if _, err := os.Lstat(p); err != nil {
		return err
	}
	return os.RemoveAll(p)
}

func rename(env value.Env, args []value.Value) (value.Value, error) {
	a, from, err := pathArg("fs::rename", args, 2)
	if err != nil {
		return value.NilValue, err
	}
	to, err := a.String(1)
	if err != nil {
		return value.NilValue, err
	}
	if err := os.Rename(from, to); err != nil {
		return value.NilValue, ioError(a, err)
	}
	return value.NilValue, nil
}

// fs::join(part, ...): the parts joined with the separator, cleaned.
func join(env value.Env, args []value.Value) (value.Value, error) {
	a, err := native.Check("fs::join", args, 1, -1)
	if err != nil {
		return value.NilValue, err
	}
	parts := make([]string, len(args))
	for i := range args {
		if parts[i], err = a.String(i); err != nil {
			return value.NilValue, err
		}
	}
	return value.MakeString(filepath.Join(parts...)), nil
}

// pathArg checks the arguments of a function that takes a path first.
func pathArg(name string, args []value.Value, n int) (native.Args, string, error) {
	a, err := native.Check(name, args, n, n)
	if err != nil {
		return a, "", err
	}
	p, err := a.String(0)
	return a, p, err
}

// pathOp makes a function of a path that changes the file system.
func pathOp(name string, op func(string) error) native.Func {
	return func(env value.Env, args []value.Value) (value.Value, error) {
		a, p, err := pathArg(name, args, 1)
		if err != nil {
			return value.NilValue, err
		}
		if err := op(p); err != nil {
			return value.NilValue, ioError(a, err)
		}
		return value.NilValue, nil
	}
}

// pathFunc makes a function of a path that gives a string.
func pathFunc(name string, f func(string) string) native.Func {
	return func(env value.Env, args []value.Value) (value.Value, error) {
		_, p, err := pathArg(name, args, 1)
		if err != nil {
			return value.NilValue, err
		}
		return value.MakeString(f(p)), nil
	}
}
