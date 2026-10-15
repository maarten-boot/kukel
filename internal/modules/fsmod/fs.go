// Package fsmod is the native module "fs": directories and files of the
// local file system (TODO.md Q61).
//
//	use fs;
//	for (local e in fs::readdir(".")) println(e["name"], e["dir"] ? "/" : e["size"]);
//	fs::walk("src", fn(path, e) { if (fs::ext(path) == ".kkl") println(path); });
//
// An entry is a map: name, size, dir, link, mode and time (a time::Time).
// What fails is an IOError. Symbolic links are not followed by walk.
package fsmod

import (
	"errors"
	"io/fs"
	"os"
	"path/filepath"

	"github.com/maarten-boot/kukel/internal/modules/timemod"
	"github.com/maarten-boot/kukel/internal/native"
	"github.com/maarten-boot/kukel/internal/value"
)

func init() {
	native.Register(&native.Module{
		Name: "fs",
		Doc:  "directories and files: readdir, stat, walk, mkdir, remove, rename, paths",
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
			"join":      join,
			"base":      pathFunc("fs::base", filepath.Base),
			"dir":       pathFunc("fs::dir", filepath.Dir),
			"ext":       pathFunc("fs::ext", filepath.Ext),
		},
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
	set("mode", value.MakeString(info.Mode().String()))
	set("time", timemod.Time.Object(info.ModTime()))
	return value.MakeMap(m)
}

func ioError(a native.Args, err error) error {
	return a.Errorf(value.IOError, "%v", err)
}

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
// nothing does not count).
func exists(env value.Env, args []value.Value) (value.Value, error) {
	_, path, err := pathArg("fs::exists", args, 1)
	if err != nil {
		return value.NilValue, err
	}
	_, err = os.Stat(path)
	return value.MakeBool(err == nil), nil
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
