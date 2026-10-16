//go:build !no9p && unix

package p9mod

import (
	"os"
	"syscall"
)

// fileID is the qid path of a file of a disk: from its device and inode
// numbers, with the top bit set (tree nodes count from 1).
func fileID(fi os.FileInfo) (uint64, bool) {
	st, ok := fi.Sys().(*syscall.Stat_t)
	if !ok {
		return 0, false
	}
	return (uint64(st.Ino) ^ uint64(st.Dev)<<48) | 1<<63, true
}
