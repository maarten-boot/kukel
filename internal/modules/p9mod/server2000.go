//go:build !no9p

package p9mod

import (
	"bufio"
	"io"
	"strings"

	"9fans.net/go/plan9"
	"github.com/hugelgupf/p9/linux"
)

// The 9P2000 server: one goroutine per connection answers its requests in
// order (with the message codec of 9fans.net/go/plan9).

// maxMsize is the largest message size the server agrees to.
const maxMsize = 1 << 20

// fid2000 is a fid of a 9P2000 connection.
type fid2000 struct {
	n    *node
	h    *handle // when open
	read bool    // opened for reading
	next int     // a directory: the entry the next read gives first
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
		if tx.Mode&plan9.ORCLOSE != 0 {
			return rerror(errNoCreate)
		}
		mode := tx.Mode & 3
		read := mode == plan9.OREAD || mode == plan9.ORDWR || mode == plan9.OEXEC
		write := mode == plan9.OWRITE || mode == plan9.ORDWR
		h, err := c.t.open(f.n, read, write, tx.Mode&plan9.OTRUNC != 0)
		if err != nil {
			return rerror(err)
		}
		f.h, f.read = h, read
		return &plan9.Fcall{Type: plan9.Ropen, Qid: c.qid(f.n), Iounit: c.msize - plan9.IOHDRSZ}
	case plan9.Tcreate:
		return rerror(errNoCreate)
	case plan9.Tread:
		if f.h == nil || !f.read {
			return rerror(errNotOpen)
		}
		count := min(tx.Count, c.msize-plan9.IOHDRSZ)
		if f.n.dir {
			return c.readDir(f, tx.Offset, count)
		}
		buf := make([]byte, count)
		n := f.h.readAt(buf, tx.Offset)
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
		delete(c.fids, tx.Fid)
		var err error
		if f.h != nil {
			err = f.h.close()
		}
		switch {
		case tx.Type == plan9.Tremove:
			return rerror(errNoCreate) // the fid is clunked all the same
		case err != nil:
			return rerror(err)
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

// wstat allows what changes nothing (a "sync"), and a length of 0 for a
// file open for writing (a truncate).
func (c *conn2000) wstat(f *fid2000, stat []byte) *plan9.Fcall {
	d, err := plan9.UnmarshalDir(stat)
	if err != nil {
		return rerror(err)
	}
	var none plan9.Dir
	none.Null()
	length := d.Length
	d.Length = none.Length
	if *d != none {
		return rerror(errorf(linux.EINVAL, "permission denied: the server program decides about names, modes and times"))
	}
	switch {
	case length == none.Length:
	case f.h != nil && f.h.writing:
		if err := f.h.truncate(length); err != nil {
			return rerror(err)
		}
	case length != 0 || c.t.info(f.n).mode&0o222 == 0:
		return rerror(errReadOnly)
	default:
		// Linux truncates a file before it opens it with OTRUNC: nothing
		// to do.
	}
	return &plan9.Fcall{Type: plan9.Rwstat}
}
