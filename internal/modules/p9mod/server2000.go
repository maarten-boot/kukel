//go:build !no9p

package p9mod

import (
	"bufio"
	"io"
	"os"
	"strings"
	"time"

	"9fans.net/go/plan9"
	"github.com/hugelgupf/p9/linux"
)

// The 9P2000 server: one goroutine per connection answers its requests in
// order (with the message codec of 9fans.net/go/plan9).

// maxMsize is the largest message size the server agrees to.
const maxMsize = 1 << 20

// fid2000 is a fid of a 9P2000 connection.
type fid2000 struct {
	n      *node
	h      *handle // when open
	read   bool    // opened for reading
	rclose bool    // remove the file when the fid is clunked (ORCLOSE)
	next   int     // a directory: the entry the next read gives first
}

type conn2000 struct {
	t     *tree
	w     io.Writer
	msize uint32
	fids  map[uint32]*fid2000
}

// serve2000 answers the requests of one client until it goes away.
func serve2000(t *tree, r *bufio.Reader, w io.Writer) {
	c := &conn2000{t: t, w: w, msize: maxMsize, fids: map[uint32]*fid2000{}}
	defer c.clunkAll()
	for {
		tx, err := plan9.ReadFcall(r)
		if err != nil {
			return
		}
		rx := c.handle(tx)
		rx.Tag = tx.Tag
		if err := plan9.WriteFcall(w, rx); err != nil {
			return
		}
	}
}

// clunkAll closes the fids a client left open (what it wrote still goes to
// the write functions).
func (c *conn2000) clunkAll() {
	for _, f := range c.fids {
		if f.h != nil {
			f.h.close()
		}
	}
	c.fids = map[uint32]*fid2000{}
}

func rerror(err error) *plan9.Fcall {
	return &plan9.Fcall{Type: plan9.Rerror, Ename: err.Error()}
}

func (c *conn2000) qid(n *node) plan9.Qid {
	i := c.t.info(n)
	q := plan9.Qid{Path: i.path, Vers: i.version, Type: plan9.QTFILE}
	if i.dir {
		q.Type = plan9.QTDIR
	}
	return q
}

func (c *conn2000) dir(n *node) plan9.Dir {
	i := c.t.info(n)
	user, _ := owner()
	d := plan9.Dir{
		Qid:    plan9.Qid{Path: i.path, Vers: i.version, Type: plan9.QTFILE},
		Mode:   plan9.Perm(i.mode),
		Atime:  uint32(i.mtime.Unix()),
		Mtime:  uint32(i.mtime.Unix()),
		Length: i.size,
		Name:   i.name,
		Uid:    user,
		Gid:    user,
		Muid:   user,
	}
	if i.dir {
		d.Qid.Type = plan9.QTDIR
		d.Mode |= plan9.DMDIR
		d.Length = 0
	}
	return d
}

func (c *conn2000) handle(tx *plan9.Fcall) *plan9.Fcall {
	switch tx.Type {
	case plan9.Tversion:
		c.clunkAll()
		msize := min(tx.Msize, maxMsize)
		if msize < plan9.IOHDRSZ+64 {
			return rerror(errorf(linux.EINVAL, "msize %d is too small", tx.Msize))
		}
		c.msize = msize
		if !strings.HasPrefix(tx.Version, "9P2000") {
			return &plan9.Fcall{Type: plan9.Rversion, Msize: msize, Version: "unknown"}
		}
		return &plan9.Fcall{Type: plan9.Rversion, Msize: msize, Version: "9P2000"}
	case plan9.Tauth:
		return rerror(errorf(linux.EINVAL, "authentication not required"))
	case plan9.Tflush: // requests are answered in order: the old one already was
		return &plan9.Fcall{Type: plan9.Rflush}
	case plan9.Tattach:
		if c.fids[tx.Fid] != nil {
			return rerror(errorf(linux.EINVAL, "fid %d is in use", tx.Fid))
		}
		n := c.t.root
		for _, name := range names(tx.Aname) {
			k, err := c.t.walk(n, name)
			if err != nil {
				return rerror(err)
			}
			n = k
		}
		if !c.t.info(n).dir { // the top a client sees is a directory
			return rerror(errorf(linux.ENOTDIR, "not a directory"))
		}
		c.fids[tx.Fid] = &fid2000{n: n}
		return &plan9.Fcall{Type: plan9.Rattach, Qid: c.qid(n)}
	}
	f := c.fids[tx.Fid]
	if f == nil {
		return rerror(errBadFid)
	}
	switch tx.Type {
	case plan9.Twalk:
		if f.h != nil {
			return rerror(errorf(linux.EINVAL, "the fid is open"))
		}
		if tx.Newfid != tx.Fid && c.fids[tx.Newfid] != nil {
			return rerror(errorf(linux.EINVAL, "fid %d is in use", tx.Newfid))
		}
		if len(tx.Wname) > plan9.MAXWELEM {
			return rerror(errorf(linux.EINVAL, "too many names in a walk"))
		}
		n := f.n
		var qids []plan9.Qid
		for _, name := range tx.Wname {
			k, err := c.t.walk(n, name)
			if err != nil {
				if len(qids) == 0 {
					return rerror(err)
				}
				return &plan9.Fcall{Type: plan9.Rwalk, Wqid: qids} // as far as it went
			}
			n = k
			qids = append(qids, c.qid(n))
		}
		c.fids[tx.Newfid] = &fid2000{n: n}
		return &plan9.Fcall{Type: plan9.Rwalk, Wqid: qids}
	case plan9.Topen:
		if f.h != nil {
			return rerror(errorf(linux.EINVAL, "the fid is already open"))
		}
		read, write := modes(tx.Mode)
		if tx.Mode&plan9.ORCLOSE != 0 {
			if f.n.disk == nil {
				return rerror(errNoCreate)
			}
			if f.n.disk.readonly {
				return rerror(errReadOnlyDisk)
			}
		}
		h, err := c.t.open(f.n, read, write, tx.Mode&plan9.OTRUNC != 0)
		if err != nil {
			return rerror(err)
		}
		f.h, f.read, f.rclose = h, read, tx.Mode&plan9.ORCLOSE != 0
		return &plan9.Fcall{Type: plan9.Ropen, Qid: c.qid(f.n), Iounit: c.msize - plan9.IOHDRSZ}
	case plan9.Tcreate:
		if f.h != nil {
			return rerror(errorf(linux.EINVAL, "the fid is open"))
		}
		if tx.Perm&(plan9.DMSYMLINK|plan9.DMDEVICE|plan9.DMNAMEDPIPE|plan9.DMSOCKET|plan9.DMAUTH|plan9.DMMOUNT) != 0 {
			return rerror(errorf(linux.EINVAL, "only files and directories can be made"))
		}
		read, _ := modes(tx.Mode)
		isDir := tx.Perm&plan9.DMDIR != 0
		n, h, err := c.t.create(f.n, tx.Name, isDir, os.FileMode(tx.Perm&0o777), read || isDir, !isDir)
		if err != nil {
			return rerror(err)
		}
		f.n, f.h, f.read, f.rclose = n, h, read || isDir, tx.Mode&plan9.ORCLOSE != 0
		return &plan9.Fcall{Type: plan9.Rcreate, Qid: c.qid(n), Iounit: c.msize - plan9.IOHDRSZ}
	case plan9.Tread:
		if f.h == nil || !f.read {
			return rerror(errNotOpen)
		}
		count := min(tx.Count, c.msize-plan9.IOHDRSZ)
		if f.n.dir {
			return c.readDir(f, tx.Offset, count)
		}
		buf := make([]byte, count)
		n, err := f.h.readAt(buf, tx.Offset)
		if err != nil {
			return rerror(err)
		}
		return &plan9.Fcall{Type: plan9.Rread, Data: buf[:n]}
	case plan9.Twrite:
		if f.h == nil {
			return rerror(errNotOpen)
		}
		n, err := f.h.writeAt(tx.Data, tx.Offset)
		if err != nil {
			return rerror(err)
		}
		return &plan9.Fcall{Type: plan9.Rwrite, Count: uint32(n)}
	case plan9.Tclunk, plan9.Tremove:
		delete(c.fids, tx.Fid) // also when the remove fails
		var err error
		if f.h != nil {
			err = f.h.close()
		}
		if tx.Type == plan9.Tremove || f.rclose {
			if rerr := c.t.removeNode(f.n); err == nil {
				err = rerr
			}
		}
		switch {
		case err != nil:
			return rerror(err)
		case tx.Type == plan9.Tremove:
			return &plan9.Fcall{Type: plan9.Rremove}
		}
		return &plan9.Fcall{Type: plan9.Rclunk}
	case plan9.Tstat:
		d := c.dir(f.n)
		b, err := d.Bytes()
		if err != nil {
			return rerror(err)
		}
		return &plan9.Fcall{Type: plan9.Rstat, Stat: b}
	case plan9.Twstat:
		return c.wstat(f, tx.Stat)
	}
	return rerror(errorf(linux.EINVAL, "unknown message type %d", tx.Type))
}

// readDir gives the entries of an open directory: whole entries, as many
// as fit in count bytes. Offset 0 starts again from the first; any other
// offset continues where the last read stopped.
func (c *conn2000) readDir(f *fid2000, offset uint64, count uint32) *plan9.Fcall {
	if offset == 0 {
		f.next = 0
	}
	var data []byte
	for ; f.next < len(f.h.kids); f.next++ {
		d := c.dir(f.h.kids[f.next])
		b, err := d.Bytes()
		if err != nil {
			return rerror(err)
		}
		if len(data)+len(b) > int(count) {
			if len(data) == 0 {
				return rerror(errorf(linux.EINVAL, "the count is too small for a directory entry"))
			}
			break
		}
		data = append(data, b...)
	}
	return &plan9.Fcall{Type: plan9.Rread, Data: data}
}

// modes tells whether an open mode reads and writes.
func modes(mode uint8) (read, write bool) {
	m := mode & 3
	return m == plan9.OREAD || m == plan9.ORDWR || m == plan9.OEXEC, m == plan9.OWRITE || m == plan9.ORDWR
}

// wstat changes the name (in the same directory), length, permissions and
// modification time of a file, as far as the tree allows; what changes
// nothing is a "sync". The owner, group and type cannot change.
func (c *conn2000) wstat(f *fid2000, stat []byte) *plan9.Fcall {
	d, err := plan9.UnmarshalDir(stat)
	if err != nil {
		return rerror(err)
	}
	var none plan9.Dir
	none.Null()
	if d.Type != none.Type || d.Dev != none.Dev || d.Qid != none.Qid || d.Uid != "" || d.Gid != "" || d.Muid != "" {
		return rerror(errorf(linux.EPERM, "permission denied: the type, owner and group of a file cannot change"))
	}
	var ch change
	if d.Length != none.Length {
		ch.size = &d.Length
	}
	if d.Mode != none.Mode {
		if (d.Mode&plan9.DMDIR != 0) != f.n.dir {
			return rerror(errorf(linux.EPERM, "permission denied: a file cannot become a directory, nor the other way round"))
		}
		perm := os.FileMode(d.Mode & 0o777)
		ch.perm = &perm
	}
	if d.Mtime != none.Mtime {
		m := time.Unix(int64(d.Mtime), 0)
		ch.mtime = &m
	}
	if d.Atime != none.Atime {
		a := time.Unix(int64(d.Atime), 0)
		ch.atime = &a
	}
	if !ch.empty() {
		if err := c.t.setattr(f.n, f.h, ch); err != nil {
			return rerror(err)
		}
	}
	if d.Name != "" && d.Name != f.n.name {
		dir := f.n.parent
		if dir == nil {
			return rerror(errorf(linux.EBUSY, "the top cannot be renamed"))
		}
		if err := c.t.rename(f.n, dir, d.Name); err != nil {
			return rerror(err)
		}
		f.n = renamed(f.n, dir, d.Name)
	}
	return &plan9.Fcall{Type: plan9.Rwstat}
}
