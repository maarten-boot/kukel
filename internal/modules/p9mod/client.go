//go:build !no9p

package p9mod

import (
	"errors"
	"io"
	"net"
	"os"
	"path"
	"time"

	"9fans.net/go/plan9"
	plan9client "9fans.net/go/plan9/client"
	"github.com/hugelgupf/p9/linux"
	"github.com/hugelgupf/p9/p9"
)

// What a p9::Client does, in either version of the protocol. Paths are
// paths on the server, from the directory the client attached to.
type remote interface {
	version() string
	stat(p string) (entry, error)
	readdir(p string) ([]entry, error)
	read(p string) ([]byte, error)
	write(p string, data []byte) error
	mkdir(p string) error
	remove(p string) error
	rename(from, to string) error
	close() error
}

// entry is what a server tells about a file.
type entry struct {
	name  string
	size  uint64
	mode  os.FileMode
	mtime time.Time
}

// baseName is the name of the file at p ("/" for the top).
func baseName(p string) string {
	if ns := names(p); len(ns) > 0 {
		return ns[len(ns)-1]
	}
	return "/"
}

// split gives the directory and the name of the file at p.
func split(p string) (dir, name string, err error) {
	ns := names(p)
	if len(ns) == 0 {
		return "", "", errors.New("not for the top directory")
	}
	return path.Join(ns[:len(ns)-1]...), ns[len(ns)-1], nil
}

const chunk = 64 << 10

// dialOptions are the options of p9::dial.
type dialOptions struct {
	user, aname, version string
	timeout              time.Duration
}

// dial connects to a 9P server. With no version asked for, it tries
// 9P2000.L first and then 9P2000.
func dial(network, addr string, o dialOptions) (remote, error) {
	connect := func() (net.Conn, error) {
		return net.DialTimeout(network, addr, o.timeout)
	}
	var errL error
	if o.version != "9P2000" {
		c, err := connect()
		if err != nil {
			return nil, err
		}
		r, err := dialL(c, o.aname)
		if err == nil || o.version == "9P2000.L" {
			return r, err
		}
		errL = err
	}
	c, err := connect()
	if err != nil {
		return nil, err
	}
	r, err := dial2000(c, o.user, o.aname)
	if err != nil && errL != nil {
		return nil, errors.New("the server speaks neither 9P2000.L (" + errL.Error() + ") nor 9P2000 (" + err.Error() + ")")
	}
	return r, err
}

// ----------------------------------------------------------------------
// 9P2000.L, with github.com/hugelgupf/p9

type clientL struct {
	c    *p9.Client
	root p9.File
}

func dialL(c net.Conn, aname string) (remote, error) {
	if aname == "" {
		aname = "/"
	}
	// The deadline is for a server that does not answer a version it does
	// not know.
	c.SetDeadline(time.Now().Add(10 * time.Second))
	pc, err := p9.NewClient(c, p9.WithMessageSize(chunk+4096))
	if err != nil {
		c.Close()
		return nil, err
	}
	root, err := pc.Attach(aname)
	if err != nil {
		pc.Close()
		return nil, err
	}
	c.SetDeadline(time.Time{})
	return &clientL{pc, root}, nil
}

func (r *clientL) version() string { return "9P2000.L" }

func (r *clientL) walk(p string) (p9.File, error) {
	_, f, err := r.root.Walk(names(p))
	return f, err
}

func (r *clientL) stat(p string) (entry, error) {
	f, err := r.walk(p)
	if err != nil {
		return entry{}, err
	}
	defer f.Close()
	return statL(f, baseName(p))
}

func statL(f p9.File, name string) (entry, error) {
	_, _, a, err := f.GetAttr(p9.AttrMask{Mode: true, Size: true, MTime: true})
	if err != nil {
		return entry{}, err
	}
	return entry{name, a.Size, a.Mode.OSMode(), time.Unix(int64(a.MTimeSeconds), int64(a.MTimeNanoSeconds))}, nil
}

func (r *clientL) readdir(p string) ([]entry, error) {
	d, err := r.walk(p)
	if err != nil {
		return nil, err
	}
	defer d.Close()
	_, open, err := d.Walk(nil)
	if err != nil {
		return nil, err
	}
	defer open.Close()
	if q, _, err := open.Open(p9.ReadOnly); err != nil {
		return nil, err
	} else if q.Type&p9.TypeDir == 0 {
		return nil, linux.ENOTDIR
	}
	var out []entry
	for offset := uint64(0); ; {
		ds, err := open.Readdir(offset, chunk)
		if err != nil {
			return nil, err
		}
		if len(ds) == 0 {
			return out, nil
		}
		for _, de := range ds {
			offset = de.Offset
			if de.Name == "." || de.Name == ".." {
				continue
			}
			_, k, err := d.Walk([]string{de.Name})
			if err != nil {
				if errors.Is(err, linux.ENOENT) {
					continue // removed meanwhile
				}
				return nil, err
			}
			e, err := statL(k, de.Name)
			k.Close()
			if err != nil {
				return nil, err
			}
			out = append(out, e)
		}
	}
}

func (r *clientL) read(p string) ([]byte, error) {
	f, err := r.walk(p)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	if q, _, err := f.Open(p9.ReadOnly); err != nil {
		return nil, err
	} else if q.Type&p9.TypeDir != 0 {
		return nil, linux.EISDIR
	}
	var data []byte
	buf := make([]byte, chunk)
	for {
		n, err := f.ReadAt(buf, int64(len(data)))
		data = append(data, buf[:n]...)
		if n == 0 || err == io.EOF {
			return data, nil
		}
		if err != nil {
			return nil, err
		}
	}
}

func ids() (p9.UID, p9.GID) {
	_, uid := owner()
	return p9.UID(uid), p9.GID(group())
}

func (r *clientL) write(p string, data []byte) error {
	f, err := r.walk(p)
	switch {
	case errors.Is(err, linux.ENOENT):
		dir, name, err := split(p)
		if err != nil {
			return err
		}
		d, err := r.walk(dir)
		if err != nil {
			return err
		}
		uid, gid := ids()
		if f, _, _, err = d.Create(name, p9.WriteOnly, 0o666, uid, gid); err != nil {
			d.Close()
			return err
		}
	case err != nil:
		return err
	default:
		if _, _, err := f.Open(p9.WriteOnly); err != nil {
			f.Close()
			return err
		}
		if err := f.SetAttr(p9.SetAttrMask{Size: true}, p9.SetAttr{Size: 0}); err != nil {
			f.Close()
			return err
		}
	}
	for off := 0; off < len(data); {
		n, err := f.WriteAt(data[off:min(off+chunk, len(data))], int64(off))
		if err != nil {
			f.Close()
			return err
		}
		off += n
	}
	return f.Close()
}

func (r *clientL) mkdir(p string) error {
	dir, name, err := split(p)
	if err != nil {
		return err
	}
	f, err := r.walk(dir)
	if err != nil {
		return err
	}
	defer f.Close()
	uid, gid := ids()
	_, err = f.Mkdir(name, 0o777, uid, gid)
	return err
}

func (r *clientL) remove(p string) error {
	e, err := r.stat(p)
	if err != nil {
		return err
	}
	dir, name, err := split(p)
	if err != nil {
		return err
	}
	f, err := r.walk(dir)
	if err != nil {
		return err
	}
	defer f.Close()
	flags := uint32(0)
	if e.mode.IsDir() {
		flags = 0x200 // AT_REMOVEDIR
	}
	return f.UnlinkAt(name, flags)
}

func (r *clientL) rename(from, to string) error {
	dir1, name1, err := split(from)
	if err != nil {
		return err
	}
	dir2, name2, err := split(to)
	if err != nil {
		return err
	}
	d1, err := r.walk(dir1)
	if err != nil {
		return err
	}
	defer d1.Close()
	d2, err := r.walk(dir2)
	if err != nil {
		return err
	}
	defer d2.Close()
	return d1.RenameAt(name1, d2, name2)
}

func (r *clientL) close() error { return r.c.Close() }

// ----------------------------------------------------------------------
// 9P2000, with 9fans.net/go/plan9/client

type client2000 struct {
	conn *plan9client.Conn
	fsys *plan9client.Fsys
}

func dial2000(c net.Conn, user, aname string) (remote, error) {
	conn, err := plan9client.NewConn(c)
	if err != nil {
		c.Close()
		return nil, err
	}
	fsys, err := conn.Attach(nil, user, aname)
	if err != nil {
		conn.Close()
		return nil, err
	}
	return &client2000{conn, fsys}, nil
}

func (r *client2000) version() string { return "9P2000" }

// rel is p as 9fans.net/go/plan9/client walks it.
func rel(p string) string { return path.Join(names(p)...) }

func entry2000(d *plan9.Dir, name string) entry {
	mode := os.FileMode(d.Mode & 0o777)
	if d.Mode&plan9.DMDIR != 0 {
		mode |= os.ModeDir
	}
	if d.Mode&plan9.DMSYMLINK != 0 {
		mode |= os.ModeSymlink
	}
	return entry{name, d.Length, mode, time.Unix(int64(d.Mtime), 0)}
}

func (r *client2000) stat(p string) (entry, error) {
	d, err := r.fsys.Stat(rel(p))
	if err != nil {
		return entry{}, err
	}
	return entry2000(d, baseName(p)), nil
}

func (r *client2000) readdir(p string) ([]entry, error) {
	f, err := r.fsys.Open(rel(p), plan9.OREAD)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	if f.Qid().Type&plan9.QTDIR == 0 {
		return nil, errors.New("not a directory")
	}
	ds, err := f.Dirreadall()
	if err != nil {
		return nil, err
	}
	out := make([]entry, len(ds))
	for i, d := range ds {
		out[i] = entry2000(d, d.Name)
	}
	return out, nil
}

func (r *client2000) read(p string) ([]byte, error) {
	f, err := r.fsys.Open(rel(p), plan9.OREAD)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	if f.Qid().Type&plan9.QTDIR != 0 {
		return nil, errors.New("is a directory")
	}
	return io.ReadAll(f)
}

func (r *client2000) write(p string, data []byte) error {
	f, err := r.fsys.Open(rel(p), plan9.OWRITE|plan9.OTRUNC)
	if err != nil {
		if _, serr := r.fsys.Stat(rel(p)); serr == nil {
			return err // it is there, but cannot be written
		}
		if f, err = r.fsys.Create(rel(p), plan9.OWRITE, 0o666); err != nil {
			return err
		}
	}
	for off := 0; off < len(data); {
		n, err := f.WriteAt(data[off:min(off+chunk, len(data))], int64(off))
		if err != nil {
			f.Close()
			return err
		}
		off += n
	}
	return f.Close()
}

func (r *client2000) mkdir(p string) error {
	f, err := r.fsys.Create(rel(p), plan9.OREAD, plan9.DMDIR|0o777)
	if err != nil {
		return err
	}
	return f.Close()
}

func (r *client2000) remove(p string) error { return r.fsys.Remove(rel(p)) }

// rename: 9P2000 renames a file only within its directory.
func (r *client2000) rename(from, to string) error {
	dir1, _, err := split(from)
	if err != nil {
		return err
	}
	dir2, name, err := split(to)
	if err != nil {
		return err
	}
	if dir1 != dir2 {
		return errors.New("9P2000 renames only within a directory")
	}
	var d plan9.Dir
	d.Null()
	d.Name = name
	return r.fsys.Wstat(rel(from), &d)
}

func (r *client2000) close() error { return r.conn.Close() }
