//go:build !no9p

package p9mod

import (
	"errors"
	"hash/fnv"
	"io/fs"
	"os"
	"path"
	"sort"
	"strings"
	"time"

	"github.com/hugelgupf/p9/linux"
)

// A disk is a directory of the local disk that a server serves as it is
// (p9::disk), on its own or mounted in a tree. os.Root keeps clients in
// it: no path, also through a symbolic link, leads out. Symbolic links
// that stay inside are followed; the others are left out of listings.
// Unless the program asked otherwise, nothing can be changed.
type disk struct {
	root     *os.Root
	dir      string // as the program gave it
	readonly bool
}

func openDisk(dir string, readonly bool) (*disk, error) {
	root, err := os.OpenRoot(dir)
	if err != nil {
		return nil, err
	}
	fi, err := root.Stat(".")
	if err != nil {
		root.Close()
		return nil, err
	}
	if !fi.IsDir() {
		root.Close()
		return nil, &fs.PathError{Op: "open", Path: dir, Err: errors.New("not a directory")}
	}
	return &disk{root: root, dir: dir, readonly: readonly}, nil
}

var errReadOnlyDisk = &fsError{linux.EROFS, "read-only file system"}

// diskError turns an error of the file system into one for clients: the
// errno and its text, without the server's paths.
func diskError(err error) error {
	switch {
	case err == nil:
		return nil
	case errors.Is(err, fs.ErrNotExist):
		return errNotFound
	}
	if _, ok := err.(*fsError); ok {
		return err
	}
	e := linux.ExtractErrno(err)
	if e == linux.EIO && strings.Contains(err.Error(), "path escapes from parent") {
		e = linux.EACCES // os.Root: out of the directory
	}
	return &fsError{e, e.Error()}
}

func validName(name string) bool {
	return name != "" && name != "." && name != ".." && !strings.ContainsAny(name, "/\x00")
}

// info tells about a node of the disk, as it is now.
func (d *disk) info(n *node) info {
	i := info{name: n.name, dir: n.dir, mode: 0o555, path: diskPath(n.rel)}
	fi, err := d.root.Stat(n.rel)
	if err != nil { // gone meanwhile
		return i
	}
	i.dir = fi.IsDir()
	i.mode = uint32(fi.Mode().Perm())
	if d.readonly {
		i.mode &^= 0o222
	}
	if !i.dir {
		i.size = uint64(fi.Size())
	}
	i.mtime = fi.ModTime()
	i.version = uint32(fi.ModTime().UnixNano())
	if id, ok := fileID(fi); ok {
		i.path = id
	}
	return i
}

// diskPath is the qid path of a file of a disk when the system gives no
// file number: from its path. Tree nodes count from 1; disk files have the
// top bit set.
func diskPath(rel string) uint64 {
	h := fnv.New64a()
	h.Write([]byte(rel))
	return h.Sum64() | 1<<63
}

func (d *disk) walk(n *node, name string) (*node, error) {
	switch {
	case !n.dir:
		return nil, errNotDir
	case name == "..":
		if n.parent == nil {
			return n, nil
		}
		return n.parent, nil
	case name == "." || name == "":
		return n, nil
	case !validName(name):
		return nil, errNotFound
	}
	rel := path.Join(n.rel, name)
	fi, err := d.root.Stat(rel)
	if err != nil {
		return nil, diskError(err)
	}
	return &node{name: name, parent: n, dir: fi.IsDir(), disk: d, rel: rel}, nil
}

func (d *disk) open(t *tree, n *node, read, write, trunc bool) (*handle, error) {
	if (write || trunc) && d.readonly {
		return nil, errReadOnlyDisk
	}
	if n.dir {
		if write || trunc {
			return nil, errIsDir
		}
		kids, err := d.list(n)
		if err != nil {
			return nil, err
		}
		return &handle{t: t, n: n, kids: kids}, nil
	}
	flags := os.O_RDONLY
	switch {
	case read && write:
		flags = os.O_RDWR
	case write:
		flags = os.O_WRONLY
	}
	if trunc {
		flags |= os.O_TRUNC
	}
	f, err := d.root.OpenFile(n.rel, flags, 0)
	if err != nil {
		return nil, diskError(err)
	}
	return &handle{t: t, n: n, f: f, writing: write || trunc}, nil
}

// list gives the entries of a directory, by name; links that lead out of
// the disk, or nowhere, are left out.
func (d *disk) list(n *node) ([]*node, error) {
	f, err := d.root.Open(n.rel)
	if err != nil {
		return nil, diskError(err)
	}
	des, err := f.ReadDir(-1)
	f.Close()
	if err != nil {
		return nil, diskError(err)
	}
	kids := make([]*node, 0, len(des))
	for _, de := range des {
		rel := path.Join(n.rel, de.Name())
		dir := de.IsDir()
		if de.Type()&fs.ModeSymlink != 0 {
			fi, err := d.root.Stat(rel)
			if err != nil {
				continue
			}
			dir = fi.IsDir()
		}
		kids = append(kids, &node{name: de.Name(), parent: n, dir: dir, disk: d, rel: rel})
	}
	sort.Slice(kids, func(i, j int) bool { return kids[i].name < kids[j].name })
	return kids, nil
}

// ----------------------------------------------------------------------
// What clients change. In the tree the program builds they can change
// nothing; in a disk what the program allowed.

// create makes a file (opened as asked) or a directory called name in dir.
func (t *tree) create(dir *node, name string, isDir bool, perm os.FileMode, read, write bool) (*node, *handle, error) {
	d := dir.disk
	switch {
	case d == nil:
		return nil, nil, errNoCreate
	case !dir.dir:
		return nil, nil, errNotDir
	case d.readonly:
		return nil, nil, errReadOnlyDisk
	case !validName(name):
		return nil, nil, errorf(linux.EINVAL, "%q cannot be a file name", name)
	}
	rel := path.Join(dir.rel, name)
	n := &node{name: name, parent: dir, dir: isDir, disk: d, rel: rel}
	if isDir {
		if err := d.root.Mkdir(rel, perm&0o777); err != nil {
			return nil, nil, diskError(err)
		}
		if !read {
			return n, nil, nil
		}
		h, err := d.open(t, n, true, false, false)
		return n, h, err
	}
	flags := os.O_CREATE | os.O_EXCL | os.O_WRONLY
	if read {
		flags = os.O_CREATE | os.O_EXCL | os.O_RDWR
	}
	// A file made for reading only is still opened for writing too: Plan 9
	// allows writing to a file one just made, whatever its mode.
	f, err := d.root.OpenFile(rel, flags, perm&0o777)
	if err != nil {
		return nil, nil, diskError(err)
	}
	return n, &handle{t: t, n: n, f: f, writing: true}, nil
}

// removeNode removes the file or empty directory n.
func (t *tree) removeNode(n *node) error {
	d := n.disk
	switch {
	case d == nil:
		return errNoCreate
	case d.readonly:
		return errReadOnlyDisk
	case n.rel == ".":
		return errorf(linux.EBUSY, "the top of a disk cannot be removed")
	}
	err := d.root.Remove(n.rel)
	if n.dir && errors.Is(err, fs.ErrExist) { // POSIX allows EEXIST for a directory that is not empty
		return &fsError{linux.ENOTEMPTY, linux.ENOTEMPTY.Error()}
	}
	return diskError(err)
}

// rename moves n to the name newName in dir.
func (t *tree) rename(n, dir *node, newName string) error {
	d := n.disk
	switch {
	case d == nil || dir.disk == nil:
		return errNoCreate
	case dir.disk != d:
		return errorf(linux.EXDEV, "cannot move between disks")
	case d.readonly:
		return errReadOnlyDisk
	case n.rel == ".":
		return errorf(linux.EBUSY, "the top of a disk cannot be renamed")
	case !validName(newName):
		return errorf(linux.EINVAL, "%q cannot be a file name", newName)
	}
	return diskError(d.root.Rename(n.rel, path.Join(dir.rel, newName)))
}

// renamed is n after a rename to newName in dir.
func renamed(n, dir *node, newName string) *node {
	m := *n
	m.name, m.parent = newName, dir
	if n.disk != nil && dir.disk == n.disk {
		m.rel = path.Join(dir.rel, newName)
	}
	return &m
}

// change is what a client asks to change of a file: what is not nil.
type change struct {
	size  *uint64
	perm  *os.FileMode
	atime *time.Time
	mtime *time.Time
}

func (c change) empty() bool {
	return c.size == nil && c.perm == nil && c.atime == nil && c.mtime == nil
}

// setattr changes n (open as h, or h nil). In the tree: only the size of a
// file open for writing, and nothing at all (a size or times) for a file
// that can be written, since Linux sets these before writing.
func (t *tree) setattr(n *node, h *handle, c change) error {
	d := n.disk
	if d == nil {
		writable := t.info(n).mode&0o222 != 0
		switch {
		case c.perm != nil || !writable:
			return errorf(linux.EPERM, "permission denied: the server program decides about the files of its tree")
		case c.size != nil && h != nil && h.writing:
			return h.truncate(*c.size)
		case c.size != nil && *c.size != 0:
			return errReadOnly
		}
		return nil // Linux truncates a file before it opens it with O_TRUNC
	}
	if d.readonly {
		return errReadOnlyDisk
	}
	if c.size != nil {
		if h != nil && h.f != nil && h.writing {
			if err := h.f.Truncate(int64(*c.size)); err != nil {
				return diskError(err)
			}
		} else {
			f, err := d.root.OpenFile(n.rel, os.O_WRONLY, 0)
			if err != nil {
				return diskError(err)
			}
			err = f.Truncate(int64(*c.size))
			f.Close()
			if err != nil {
				return diskError(err)
			}
		}
	}
	if c.perm != nil {
		if err := d.root.Chmod(n.rel, *c.perm&0o777); err != nil {
			return diskError(err)
		}
	}
	if c.atime != nil || c.mtime != nil {
		var a, m time.Time // zero: unchanged
		if c.atime != nil {
			a = *c.atime
		}
		if c.mtime != nil {
			m = *c.mtime
		}
		if err := d.root.Chtimes(n.rel, a, m); err != nil {
			return diskError(err)
		}
	}
	return nil
}

// sync writes what was written to a file of a disk to the disk.
func (h *handle) sync() error {
	if h.f == nil {
		return nil
	}
	return diskError(h.f.Sync())
}
