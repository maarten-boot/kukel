//go:build !no9p

package p9mod

import (
	"os"
	"sync"
	"time"

	"github.com/hugelgupf/p9/fsimpl/templatefs"
	"github.com/hugelgupf/p9/linux"
	"github.com/hugelgupf/p9/p9"
)

// The 9P2000.L server: github.com/hugelgupf/p9 answers the requests and
// asks a fileL for what is in the tree. Each fid has a fileL of its own.

type attacher struct{ t *tree }

func (a attacher) Attach() (p9.File, error) { return &fileL{t: a.t, n: a.t.root}, nil }

type fileL struct {
	templatefs.NoopFile // what is not below: not implemented
	t                   *tree

	mu sync.Mutex
	n  *node   // changes when the file is renamed
	h  *handle // when open
}

func (f *fileL) node() *node {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.n
}

func (f *fileL) handle() *handle {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.h
}

var _ p9.File = &fileL{}

func qidL(i info) p9.QID {
	q := p9.QID{Type: p9.TypeRegular, Version: i.version, Path: i.path}
	if i.dir {
		q.Type = p9.TypeDir
	}
	return q
}

func (f *fileL) Walk(names []string) ([]p9.QID, p9.File, error) {
	n := f.node()
	var qids []p9.QID
	for _, name := range names {
		k, err := f.t.walk(n, name)
		if err != nil {
			return nil, nil, err
		}
		n = k
		qids = append(qids, qidL(f.t.info(n)))
	}
	return qids, &fileL{t: f.t, n: n}, nil
}

func (f *fileL) StatFS() (p9.FSStat, error) {
	return p9.FSStat{Type: 0x01021997 /* V9FS_MAGIC */, BlockSize: 4096, NameLength: 255}, nil
}

func (f *fileL) GetAttr(req p9.AttrMask) (p9.QID, p9.AttrMask, p9.Attr, error) {
	i := f.t.info(f.node())
	_, uid := owner()
	a := p9.Attr{
		Mode:             p9.ModeRegular | p9.FileMode(i.mode),
		UID:              p9.UID(uid),
		GID:              p9.GID(group()),
		NLink:            1,
		Size:             i.size,
		BlockSize:        4096,
		Blocks:           (i.size + 511) / 512,
		ATimeSeconds:     uint64(i.mtime.Unix()),
		ATimeNanoSeconds: uint64(i.mtime.Nanosecond()),
		MTimeSeconds:     uint64(i.mtime.Unix()),
		MTimeNanoSeconds: uint64(i.mtime.Nanosecond()),
		CTimeSeconds:     uint64(i.mtime.Unix()),
		CTimeNanoSeconds: uint64(i.mtime.Nanosecond()),
	}
	if i.dir {
		a.Mode = p9.ModeDirectory | p9.FileMode(i.mode)
		a.NLink = 2
	}
	valid := p9.AttrMask{Mode: true, NLink: true, UID: true, GID: true, Size: true, Blocks: true, ATime: true, MTime: true, CTime: true}
	return qidL(i), valid, a, nil
}

// SetAttr changes the size, permissions and times, as far as the tree
// allows (tree.setattr); the owner and group cannot change.
func (f *fileL) SetAttr(valid p9.SetAttrMask, attr p9.SetAttr) error {
	if valid.UID || valid.GID {
		return linux.EPERM
	}
	var c change
	if valid.Size {
		c.size = &attr.Size
	}
	if valid.Permissions {
		perm := os.FileMode(attr.Permissions.Permissions())
		c.perm = &perm
	}
	now := time.Now()
	if valid.ATime {
		a := now
		if valid.ATimeNotSystemTime {
			a = time.Unix(int64(attr.ATimeSeconds), int64(attr.ATimeNanoSeconds))
		}
		c.atime = &a
	}
	if valid.MTime {
		m := now
		if valid.MTimeNotSystemTime {
			m = time.Unix(int64(attr.MTimeSeconds), int64(attr.MTimeNanoSeconds))
		}
		c.mtime = &m
	}
	if c.empty() { // only the change time
		return nil
	}
	return f.t.setattr(f.node(), f.handle(), c)
}

func (f *fileL) Open(mode p9.OpenFlags) (p9.QID, uint32, error) {
	if f.handle() != nil {
		return p9.QID{}, 0, linux.EBADF
	}
	n := f.node()
	m := mode.Mode()
	h, err := f.t.open(n, m != p9.WriteOnly, m != p9.ReadOnly, false)
	if err != nil {
		return p9.QID{}, 0, err
	}
	f.mu.Lock()
	f.h = h
	f.mu.Unlock()
	return qidL(f.t.info(n)), 0, nil
}

func (f *fileL) ReadAt(p []byte, offset int64) (int, error) {
	h := f.handle()
	if h == nil {
		return 0, linux.EBADF
	}
	if h.n.dir {
		return 0, linux.EISDIR
	}
	return h.readAt(p, uint64(offset))
}

func (f *fileL) WriteAt(p []byte, offset int64) (int, error) {
	h := f.handle()
	if h == nil {
		return 0, linux.EBADF
	}
	return h.writeAt(p, uint64(offset))
}

func (f *fileL) FSync() error {
	if h := f.handle(); h != nil {
		return h.sync()
	}
	return nil
}

func (f *fileL) Close() error {
	f.mu.Lock()
	h := f.h
	f.h = nil
	f.mu.Unlock()
	if h == nil {
		return nil
	}
	return h.close()
}

// Readdir gives the entries from offset on (an index: the entries are
// those of when the directory was opened).
func (f *fileL) Readdir(offset uint64, count uint32) (p9.Dirents, error) {
	n := f.node()
	if !n.dir {
		return nil, linux.ENOTDIR
	}
	kids := []*node(nil)
	if h := f.handle(); h != nil {
		kids = h.kids
	} else {
		h, err := f.t.open(n, true, false, false)
		if err != nil {
			return nil, err
		}
		kids = h.kids
	}
	var out p9.Dirents
	for i := offset; i < uint64(len(kids)); i++ {
		q := qidL(f.t.info(kids[i]))
		out = append(out, p9.Dirent{QID: q, Type: q.Type, Offset: i + 1, Name: kids[i].name})
	}
	return out, nil
}

// Clients make, rename and remove files only in disks that allow it
// (disk.go); in the tree the program builds, they cannot.

func (f *fileL) Create(name string, flags p9.OpenFlags, perm p9.FileMode, _ p9.UID, _ p9.GID) (p9.File, p9.QID, uint32, error) {
	m := flags.Mode()
	n, h, err := f.t.create(f.node(), name, false, os.FileMode(perm.Permissions()), m != p9.WriteOnly, true)
	if err != nil {
		return nil, p9.QID{}, 0, err
	}
	return &fileL{t: f.t, n: n, h: h}, qidL(f.t.info(n)), 0, nil
}

func (f *fileL) Mkdir(name string, perm p9.FileMode, _ p9.UID, _ p9.GID) (p9.QID, error) {
	n, _, err := f.t.create(f.node(), name, true, os.FileMode(perm.Permissions()), false, false)
	if err != nil {
		return p9.QID{}, err
	}
	return qidL(f.t.info(n)), nil
}

func (f *fileL) Symlink(string, string, p9.UID, p9.GID) (p9.QID, error) {
	return p9.QID{}, linux.EPERM
}

func (f *fileL) Link(p9.File, string) error { return linux.EPERM }

func (f *fileL) Mknod(string, p9.FileMode, uint32, uint32, p9.UID, p9.GID) (p9.QID, error) {
	return p9.QID{}, linux.EPERM
}

func (f *fileL) Rename(dir p9.File, name string) error {
	n := f.node()
	if n.parent == nil {
		return linux.EBUSY
	}
	return f.t.rename(n, dir.(*fileL).node(), name)
}

func (f *fileL) RenameAt(oldName string, dir p9.File, newName string) error {
	k, err := f.t.walk(f.node(), oldName)
	if err != nil {
		return err
	}
	return f.t.rename(k, dir.(*fileL).node(), newName)
}

// Renamed is called by the library for each open file that a rename moved
// (also those below a directory that moved): its path in the disk
// changes.
func (f *fileL) Renamed(dir p9.File, name string) {
	d := dir.(*fileL).node()
	f.mu.Lock()
	f.n = renamed(f.n, d, name) // an open file keeps its os.File
	f.mu.Unlock()
}

func (f *fileL) UnlinkAt(name string, flags uint32) error {
	k, err := f.t.walk(f.node(), name)
	if err != nil {
		return err
	}
	if isDir := flags&0x200 != 0; isDir != k.dir { // AT_REMOVEDIR
		if k.dir {
			return linux.EISDIR
		}
		return linux.ENOTDIR
	}
	return f.t.removeNode(k)
}

func (f *fileL) Readlink() (string, error) { return "", linux.EINVAL }
