//go:build !no9p

package p9mod

import (
	"sync"

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
	n                   *node

	mu sync.Mutex
	h  *handle // when open
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
	n := f.n
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
	i := f.t.info(f.n)
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

// SetAttr allows a size for a file open for writing (a truncate), and
// changes of the size or times of a file that can be written (Linux does
// that before it opens a file with O_TRUNC, or for touch): they do nothing.
func (f *fileL) SetAttr(valid p9.SetAttrMask, attr p9.SetAttr) error {
	other := valid
	other.Size, other.ATime, other.MTime, other.CTime, other.ATimeNotSystemTime, other.MTimeNotSystemTime = false, false, false, false, false, false
	if !other.Empty() || f.t.info(f.n).mode&0o222 == 0 {
		return linux.EPERM
	}
	if h := f.handle(); valid.Size && h != nil && h.writing {
		return h.truncate(attr.Size)
	}
	return nil
}

func (f *fileL) Open(mode p9.OpenFlags) (p9.QID, uint32, error) {
	if f.handle() != nil {
		return p9.QID{}, 0, linux.EBADF
	}
	m := mode.Mode()
	h, err := f.t.open(f.n, m != p9.WriteOnly, m != p9.ReadOnly, false)
	if err != nil {
		return p9.QID{}, 0, err
	}
	f.mu.Lock()
	f.h = h
	f.mu.Unlock()
	return qidL(f.t.info(f.n)), 0, nil
}

func (f *fileL) ReadAt(p []byte, offset int64) (int, error) {
	h := f.handle()
	if h == nil {
		return 0, linux.EBADF
	}
	if f.n.dir {
		return 0, linux.EISDIR
	}
	return h.readAt(p, uint64(offset)), nil
}

func (f *fileL) WriteAt(p []byte, offset int64) (int, error) {
	h := f.handle()
	if h == nil {
		return 0, linux.EBADF
	}
	return h.writeAt(p, uint64(offset))
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
	if !f.n.dir {
		return nil, linux.ENOTDIR
	}
	kids := []*node(nil)
	if h := f.handle(); h != nil {
		kids = h.kids
	} else {
		h, err := f.t.open(f.n, true, false, false)
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

// Clients cannot make, rename or remove files: the program builds the
// tree.

func (f *fileL) Create(string, p9.OpenFlags, p9.FileMode, p9.UID, p9.GID) (p9.File, p9.QID, uint32, error) {
	return nil, p9.QID{}, 0, linux.EACCES
}

func (f *fileL) Mkdir(string, p9.FileMode, p9.UID, p9.GID) (p9.QID, error) {
	return p9.QID{}, linux.EACCES
}

func (f *fileL) Symlink(string, string, p9.UID, p9.GID) (p9.QID, error) {
	return p9.QID{}, linux.EACCES
}

func (f *fileL) Link(p9.File, string) error { return linux.EACCES }

func (f *fileL) Mknod(string, p9.FileMode, uint32, uint32, p9.UID, p9.GID) (p9.QID, error) {
	return p9.QID{}, linux.EACCES
}

func (f *fileL) Rename(p9.File, string) error           { return linux.EACCES }
func (f *fileL) RenameAt(string, p9.File, string) error { return linux.EACCES }
func (f *fileL) UnlinkAt(string, uint32) error          { return linux.EACCES }
func (f *fileL) Readlink() (string, error)              { return "", linux.EINVAL }
